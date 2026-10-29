package admin

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"kraken.local/kraken/internal/store"
)

// Backup writes a tar archive of the instance state to out.
//
// The archive never overwrites an existing file; it is written to a temporary
// file next to out and renamed, with mode 0600. Paths inside are relative to
// dataDir:
//   - data/control.sqlite3 is a consistent online copy produced by VACUUM INTO,
//     never the live file or its -wal/-shm sidecars;
//   - data/telemetry/rollups.db, if present, is copied the same way;
//   - certs/** is copied verbatim without following symlinks.
//
// logs/, monthly event files and WAL sidecars are not included. Runtime state
// (journal, last-good manifest) lives inside control.sqlite3; derivative files
// in the tmpfs run directory are never on the volume.
func Backup(ctx context.Context, st *store.Store, dataDir, out string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if _, e := os.Stat(out); e == nil {
		return errors.New("backup file already exists")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return e
	}
	tmpOut, e := uniquePath(filepath.Dir(out), ".kraken-backup-")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(tmpOut, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	cleanup := func() {
		f.Close()
		_ = os.Remove(tmpOut)
	}
	tw := tar.NewWriter(f)
	if e := addVacuumed(ctx, tw, st.DB, filepath.Join(dataDir, "data", "control.sqlite3"), "data/control.sqlite3", tmpOut); e != nil {
		tw.Close()
		cleanup()
		return e
	}
	rollups := filepath.Join(dataDir, "data", "telemetry", "rollups.db")
	if _, e := os.Stat(rollups); e == nil {
		rdb, e := sql.Open("sqlite", "file:"+rollups+"?_pragma=busy_timeout(5000)")
		if e != nil {
			tw.Close()
			cleanup()
			return e
		}
		// Keep a single connection: VACUUM INTO runs on the owner connection and
		// the snapshot must be taken from one handle, not pooled.
		rdb.SetMaxOpenConns(1)
		e = addVacuumed(ctx, tw, rdb, rollups, "data/telemetry/rollups.db", tmpOut)
		rdb.Close()
		if e != nil {
			tw.Close()
			cleanup()
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		tw.Close()
		cleanup()
		return e
	}
	for _, sub := range []string{"certs"} {
		root := filepath.Join(dataDir, sub)
		if _, e := os.Stat(root); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			tw.Close()
			cleanup()
			return e
		}
		if e := addTree(tw, root, sub); e != nil {
			tw.Close()
			cleanup()
			return e
		}
	}
	if e := tw.Close(); e != nil {
		cleanup()
		return e
	}
	if e := f.Sync(); e != nil {
		cleanup()
		return e
	}
	if e := f.Close(); e != nil {
		_ = os.Remove(tmpOut)
		return e
	}
	if e := os.Chmod(tmpOut, 0600); e != nil {
		_ = os.Remove(tmpOut)
		return e
	}
	return os.Rename(tmpOut, out)
}

// addVacuumed runs VACUUM INTO on db into a fresh temporary file in the same
// directory as out, writes that file into the tar under archiveName and removes
// it. The snapshot is transactionally consistent even while writers are active.
func addVacuumed(ctx context.Context, tw *tar.Writer, db *sql.DB, srcPath, archiveName, neighbor string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	vac, e := uniquePath(filepath.Dir(neighbor), ".kraken-vacuum-")
	if e != nil {
		return e
	}
	defer os.Remove(vac)
	// SQLite accepts the target as a string literal; the path is a freshly
	// generated temp name we control, but quote-escape defensively anyway.
	stmt := "VACUUM INTO '" + strings.ReplaceAll(vac, "'", "''") + "'"
	if _, e := db.Exec(stmt); e != nil {
		return fmt.Errorf("vacuum %s: %w", archiveName, e)
	}
	info, e := os.Lstat(vac)
	if e != nil {
		return e
	}
	h, e := tarHeader(archiveName, info, 0)
	if e != nil {
		return e
	}
	h.Size = info.Size()
	h.Mode = 0600 // match the live control DB protection
	if e := tw.WriteHeader(h); e != nil {
		return e
	}
	r, e := os.Open(vac)
	if e != nil {
		return e
	}
	_, e = io.Copy(tw, r)
	r.Close()
	return e
}

// addTree walks root and writes every entry under prefix into the tar without
// following symlinks: a symlink is stored as a symlink entry, not its target.
func addTree(tw *tar.Writer, root, prefix string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		name := prefix
		if rel != "." {
			name = prefix + "/" + filepath.ToSlash(rel)
		}
		switch {
		case info.IsDir():
			h, e := tarHeader(name, info, tar.TypeDir)
			if e != nil {
				return e
			}
			return tw.WriteHeader(h)
		case info.Mode()&os.ModeSymlink != 0:
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			h, e := tarHeader(name, info, tar.TypeSymlink)
			if e != nil {
				return e
			}
			h.Linkname = target
			return tw.WriteHeader(h)
		case info.Mode().IsRegular():
			h, e := tarHeader(name, info, tar.TypeReg)
			if e != nil {
				return e
			}
			h.Size = info.Size()
			if e := tw.WriteHeader(h); e != nil {
				return e
			}
			r, e := os.Open(path)
			if e != nil {
				return e
			}
			_, we := io.Copy(tw, r)
			r.Close()
			return we
		default:
			// Skip fifos/sockets/devices — they have no meaningful tar form and
			// do not appear under runtime/ or certs/.
			return nil
		}
	})
}

// tarHeader builds a PAX header preserving mode, mtime and ownership. uid/gid
// are read best-effort via syscall.Stat_t (linux and darwin share the field).
func tarHeader(name string, info os.FileInfo, typeflag byte) (*tar.Header, error) {
	h := &tar.Header{
		Name:    name,
		Mode:    int64(info.Mode().Perm()),
		ModTime: info.ModTime(),
		Format:  tar.FormatPAX,
	}
	if typeflag != 0 {
		h.Typeflag = typeflag
	} else {
		h.Typeflag = tar.TypeReg
	}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		h.Uid = int(sys.Uid)
		h.Gid = int(sys.Gid)
	} else if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		// Non-unix hosts are not a supported build target; leave uid/gid zero.
	}
	return h, nil
}

// uniquePath returns a non-existing path in dir with the given prefix. The path
// is not created; callers (SQLite VACUUM INTO or O_EXCL open) create it.
func uniquePath(dir, prefix string) (string, error) {
	for i := 0; i < 8; i++ {
		b := make([]byte, 8)
		if _, e := rand.Read(b); e != nil {
			return "", e
		}
		p := filepath.Join(dir, prefix+hex.EncodeToString(b))
		if _, e := os.Stat(p); errors.Is(e, os.ErrNotExist) {
			return p, nil
		} else if e != nil {
			return "", e
		}
	}
	return "", errors.New("could not allocate unique temporary path")
}
