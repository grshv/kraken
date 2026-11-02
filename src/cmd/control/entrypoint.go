package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

const (
	proxyUID = 13
	proxyGID = 13
	pebble   = "/usr/local/bin/pebble"
)

// stateLayout is the volume layout (docs/operations/deployment.md, ADR 0015).
// dirMode applies to directories, fileMode to regular files by name.
type layoutEntry struct {
	name     string
	uid, gid int
	dirMode  os.FileMode
	fileMode func(name string) os.FileMode
	create   bool
}

func private(string) os.FileMode { return 0600 }

var stateLayout = []layoutEntry{
	{name: "data", uid: krakenUID, gid: krakenGID, dirMode: 0700, fileMode: private, create: true},
	{name: "data/telemetry", uid: krakenUID, gid: krakenGID, dirMode: 0700, fileMode: private, create: true},
	{name: "certs", uid: krakenUID, gid: proxyGID, dirMode: 0750 | os.ModeSetgid, fileMode: func(n string) os.FileMode {
		switch {
		case n == "ca.crt":
			return 0644
		case strings.HasPrefix(n, "proxy."):
			return 0640
		}
		return 0600
	}, create: true},
	{name: "log", uid: proxyUID, gid: proxyGID, dirMode: 0750 | os.ModeSetgid, fileMode: func(string) os.FileMode { return 0640 }, create: true},
	{name: "backups", uid: krakenUID, gid: krakenGID, dirMode: 0700, fileMode: private},
}

// topLevel are the only names a state directory may contain; anything else
// means the mount points at the wrong directory and nothing is changed.
var topLevel = []string{"data", "certs", "log", "backups", "lost+found"}

// unexpectedEntries returns names outside the state layout.
func unexpectedEntries(names []string) []string {
	var out []string
	for _, n := range names {
		if !slices.Contains(topLevel, n) {
			out = append(out, n)
		}
	}
	return out
}

func statOwner(fi os.FileInfo) (int, int) {
	st := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), int(st.Gid)
}

// runEntrypoint is PID 1: as root it prepares the state volume, then execs
// Pebble. Only CAP_CHOWN is needed: root claims each directory before
// changing it, because without CAP_DAC_OVERRIDE it cannot enter a directory
// it does not own. Services started by Pebble drop to proxy/kraken and keep
// no capabilities.
func runEntrypoint(args []string) error {
	if os.Geteuid() == 0 {
		if e := prepareState(stateDir()); e != nil {
			fmt.Fprintln(os.Stderr, "Каталог данных "+stateDir()+" не подготовлен: "+e.Error())
			fmt.Fprintln(os.Stderr, "Нужен пустой каталог или том Kraken и cap_add: CHOWN в compose (см. deploy/compose.yaml).")
			fmt.Fprintf(os.Stderr, "Либо подготовьте каталог на хосте: sudo chown -R %d:%d <каталог> && sudo chmod 750 <каталог>\n", krakenUID, proxyGID)
			return errQuiet
		}
	}
	return syscall.Exec(pebble, append([]string{pebble}, args...), os.Environ())
}

// prepareState is a no-op for a correctly owned volume (the usual case,
// including Docker named volumes seeded from the image). A foreign-owned
// directory, such as a fresh bind mount, is normalised to the layout.
func prepareState(root string) error {
	fi, e := os.Lstat(root)
	if e != nil {
		return e
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	uid, gid := statOwner(fi)
	if uid == krakenUID && gid == proxyGID && fi.Mode().Perm() == 0750 {
		return nil
	}
	origMode := fi.Mode().Perm() | (fi.Mode() & (os.ModeSetgid | os.ModeSticky))
	// Claim the directory to be able to list it.
	if e := os.Lchown(root, 0, 0); e != nil {
		return fmt.Errorf("chown: %w (нет CAP_CHOWN?)", e)
	}
	if e := os.Chmod(root, 0700); e != nil {
		return e
	}
	entries, e := os.ReadDir(root)
	if e != nil {
		return e
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	if bad := unexpectedEntries(names); len(bad) > 0 {
		// Restore what we touched; this is not a Kraken state directory.
		_ = os.Chmod(root, origMode)
		_ = os.Lchown(root, uid, gid)
		return fmt.Errorf("каталог содержит посторонние файлы: %s", strings.Join(bad, ", "))
	}
	// Claim top-down (data before data/telemetry) so root can reach nested
	// entries of a foreign-owned tree; create what is missing.
	for _, l := range stateLayout {
		p := filepath.Join(root, l.name)
		info, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			if !l.create {
				continue
			}
			if e := os.Mkdir(p, 0700); e != nil {
				return e
			}
			continue
		}
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return fmt.Errorf("%s: expected a directory", l.name)
		}
		if e := os.Lchown(p, 0, 0); e != nil {
			return e
		}
		if e := os.Chmod(p, 0700); e != nil {
			return e
		}
	}
	// Children first: their final owner locks root out of the parent.
	for i := len(stateLayout) - 1; i >= 0; i-- {
		l := stateLayout[i]
		p := filepath.Join(root, l.name)
		if _, e := os.Lstat(p); errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e := normalise(p, l, l.name == "data"); e != nil {
			return fmt.Errorf("%s: %w", l.name, e)
		}
	}
	if e := os.Chmod(root, 0750); e != nil {
		return e
	}
	return os.Lchown(root, krakenUID, proxyGID)
}

// normalise claims path, fixes everything below it and hands it to the
// layout owner. Symlinks, hard links and special files are refused: chown
// must never follow a planted link outside the volume. skipTelemetry leaves
// data/telemetry to its own layout entry.
func normalise(path string, l layoutEntry, skipTelemetry bool) error {
	fi, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !fi.IsDir() {
		return errors.New("expected a directory")
	}
	if e := os.Lchown(path, 0, 0); e != nil {
		return e
	}
	if e := os.Chmod(path, 0700); e != nil {
		return e
	}
	entries, e := os.ReadDir(path)
	if e != nil {
		return e
	}
	for _, en := range entries {
		p := filepath.Join(path, en.Name())
		if skipTelemetry && en.Name() == "telemetry" {
			continue
		}
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		switch {
		case info.IsDir():
			if e := normalise(p, l, false); e != nil {
				return e
			}
		case info.Mode().IsRegular():
			if info.Sys().(*syscall.Stat_t).Nlink > 1 {
				return fmt.Errorf("%s: hard links are not allowed", p)
			}
			if e := os.Lchown(p, 0, 0); e != nil {
				return e
			}
			if e := os.Chmod(p, l.fileMode(en.Name())); e != nil {
				return e
			}
			if e := os.Lchown(p, l.uid, l.gid); e != nil {
				return e
			}
		default:
			return fmt.Errorf("%s: only regular files and directories are allowed", p)
		}
	}
	if e := os.Chmod(path, l.dirMode); e != nil {
		return e
	}
	return os.Lchown(path, l.uid, l.gid)
}
