package admin

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kraken.local/kraken/internal/store"
)

func newTree(t *testing.T) (dataDir, out string, st *store.Store) {
	t.Helper()
	dataDir = filepath.Join(t.TempDir(), "data")
	st, e := store.Open(filepath.Join(dataDir, "data", "control.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.DB.Close() })
	out = filepath.Join(t.TempDir(), "backup.tar")
	return dataDir, out, st
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
}

func makeRollups(t *testing.T, path string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("CREATE TABLE rollup(host TEXT, hits INTEGER)"); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec("INSERT INTO rollup VALUES('demo.test',42)"); e != nil {
		t.Fatal(e)
	}
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
}

// tarIndex returns entry headers keyed by archive name.
func tarIndex(t *testing.T, out string) map[string]*tar.Header {
	t.Helper()
	f, e := os.Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	idx := map[string]*tar.Header{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		idx[h.Name] = h
	}
	return idx
}

func extractFile(t *testing.T, out, name string) []byte {
	t.Helper()
	f, e := os.Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			t.Fatalf("entry %q not in archive", name)
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Name == name {
			b, e := io.ReadAll(tr)
			if e != nil {
				t.Fatal(e)
			}
			return b
		}
	}
}

func openExtracted(t *testing.T, out, name string) *sql.DB {
	t.Helper()
	b := extractFile(t, out, name)
	dir := t.TempDir()
	p := filepath.Join(dir, "copy.db")
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", "file:"+p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBackupArchiveContents(t *testing.T) {
	dataDir, out, st := newTree(t)
	writeFile(t, filepath.Join(dataDir, "certs", "proxy.crt"), "PEM")
	writeFile(t, filepath.Join(dataDir, "log", "access.log"), "secret-noise\n")
	if e := os.Symlink("../certs/proxy.crt", filepath.Join(dataDir, "certs", "panel.link")); e != nil {
		t.Fatal(e)
	}
	if e := Backup(context.Background(), st, dataDir, out); e != nil {
		t.Fatal(e)
	}
	idx := tarIndex(t, out)
	for _, want := range []string{"data/control.sqlite3", "certs/proxy.crt", "certs/panel.link"} {
		if _, ok := idx[want]; !ok {
			t.Errorf("missing %q in archive", want)
		}
	}
	for _, bad := range []string{"log/access.log", "data/control.sqlite3-wal", "data/control.sqlite3-shm"} {
		if _, ok := idx[bad]; ok {
			t.Errorf("archive must not include %q", bad)
		}
	}
	if h := idx["certs/panel.link"]; h == nil || h.Typeflag != tar.TypeSymlink || h.Linkname != "../certs/proxy.crt" {
		t.Errorf("symlink entry wrong: %+v", h)
	}
}

func TestBackupRefuseOverwrite(t *testing.T) {
	dataDir, out, st := newTree(t)
	if e := os.WriteFile(out, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := Backup(context.Background(), st, dataDir, out); e == nil || !strings.Contains(e.Error(), "already exists") {
		t.Fatalf("overwrite: want already-exists error, got %v", e)
	}
	// A failed run must not leave a temp file behind.
	if e, _ := os.Stat(filepath.Dir(out)); e != nil {
	} else {
		entries, _ := os.ReadDir(filepath.Dir(out))
		for _, en := range entries {
			if strings.HasPrefix(en.Name(), ".kraken-") {
				t.Fatalf("temp file left behind: %s", en.Name())
			}
		}
	}
}

func TestBackupFileMode(t *testing.T) {
	dataDir, out, st := newTree(t)
	if e := Backup(context.Background(), st, dataDir, out); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(out)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup mode: want 0600, got %v", info.Mode().Perm())
	}
}

func TestBackupVacuumedCopyIsConsistent(t *testing.T) {
	dataDir, out, st := newTree(t)
	mustCreate(t, st, "alice", "admin")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = st.Audit(context.Background(), "writer", "tick", "")
			time.Sleep(time.Millisecond)
		}
	}()
	e := Backup(context.Background(), st, dataDir, out)
	close(stop)
	wg.Wait()
	if e != nil {
		t.Fatal(e)
	}
	db := openExtracted(t, out, "data/control.sqlite3")
	var check string
	if e := db.QueryRow("PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		t.Fatalf("integrity_check: %v (%q)", e, check)
	}
	var tbl string
	if e := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&tbl); e != nil || tbl != "schema_migrations" {
		t.Fatalf("schema_migrations missing: %v (%q)", e, tbl)
	}
}

func TestBackupIncludesRollupsVacuumed(t *testing.T) {
	dataDir, out, st := newTree(t)
	makeRollups(t, filepath.Join(dataDir, "data", "telemetry", "rollups.db"))
	if e := Backup(context.Background(), st, dataDir, out); e != nil {
		t.Fatal(e)
	}
	idx := tarIndex(t, out)
	if _, ok := idx["data/telemetry/rollups.db"]; !ok {
		t.Fatal("rollups.db missing from archive")
	}
	for _, bad := range []string{"data/telemetry/rollups.db-wal", "data/telemetry/rollups.db-shm"} {
		if _, ok := idx[bad]; ok {
			t.Errorf("archive must not include %q", bad)
		}
	}
	db := openExtracted(t, out, "data/telemetry/rollups.db")
	var hits int
	if e := db.QueryRow("SELECT hits FROM rollup WHERE host='demo.test'").Scan(&hits); e != nil || hits != 42 {
		t.Fatalf("rollup row: %v (%d)", e, hits)
	}
}

func TestBackupDoesNotFollowSymlinkOutside(t *testing.T) {
	dataDir, out, st := newTree(t)
	secret := filepath.Join(filepath.Dir(dataDir), "secret.txt")
	if e := os.WriteFile(secret, []byte("SHOULD-NOT-LEAK"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(dataDir, "certs"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("../secret.txt", filepath.Join(dataDir, "certs", "outside.link")); e != nil {
		t.Fatal(e)
	}
	if e := Backup(context.Background(), st, dataDir, out); e != nil {
		t.Fatal(e)
	}
	idx := tarIndex(t, out)
	h := idx["certs/outside.link"]
	if h == nil || h.Typeflag != tar.TypeSymlink {
		t.Fatalf("outside.link must be a symlink entry, got %+v", h)
	}
	raw, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "SHOULD-NOT-LEAK") {
		t.Fatal("symlink target content leaked into archive")
	}
}

func TestBackupMissingDirsAreSkipped(t *testing.T) {
	dataDir, out, st := newTree(t)
	// Only data/control.sqlite3 exists; certs/ absent.
	if e := Backup(context.Background(), st, dataDir, out); e != nil {
		t.Fatal(e)
	}
	idx := tarIndex(t, out)
	if _, ok := idx["data/control.sqlite3"]; !ok {
		t.Fatal("control.sqlite3 missing")
	}
}

func TestBackupCancelReturns(t *testing.T) {
	dataDir, out, st := newTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Backup(ctx, st, dataDir, out); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled backup: want context.Canceled, got %v", e)
	}
}
