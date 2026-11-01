package runtime

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"syscall"
	"testing"
)

// TestPrepareRunDirSetsSetgid checks that the run directory is created with the
// setgid bit and mode 0750 so files created inside inherit the proxy group.
func TestPrepareRunDirSetsSetgid(t *testing.T) {
	parent := tmpfsTempDir(t)
	dir := filepath.Join(parent, "run")
	if e := PrepareRunDir(dir); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dir)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Errorf("run directory lacks setgid bit: %v", info.Mode())
	}
	if info.Mode().Perm() != 0o750 {
		t.Errorf("run directory mode %o, want 0o750", info.Mode().Perm())
	}
}

// TestPrepareRunDirFixesMissingSetgid checks that an existing directory without
// the setgid bit gets it applied when PrepareRunDir runs.
func TestPrepareRunDirFixesMissingSetgid(t *testing.T) {
	parent := tmpfsTempDir(t)
	dir := filepath.Join(parent, "run")
	if e := os.Mkdir(dir, 0o770); e != nil {
		t.Fatal(e)
	}
	if e := PrepareRunDir(dir); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dir)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Error("setgid bit not applied to existing directory")
	}
}

// TestPersistChownsDerivativeFiles checks that Persist chowns written files to
// the proxy group (set to the runner's GID by TestMain) with mode 0640 so
// Squid can read them.
func TestPersistChownsDerivativeFiles(t *testing.T) {
	dir := t.TempDir()
	m := New(dir, "/unused", "/dev/null", "", "", "")
	m.State = newMemState()
	m.Command = func(context.Context, ...string) error { return nil }
	m.Reload = func(context.Context, string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	path := filepath.Join(dir, "squid.conf")
	if e := m.Persist(path, []byte("# config\n")); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("file mode %o, want 0o640", info.Mode().Perm())
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("cannot inspect file ownership on this platform")
	}
	if int(st.Gid) != os.Getgid() {
		t.Errorf("file gid %d, want %d (proxy group)", st.Gid, os.Getgid())
	}
}

// TestPrepareSquidRunDirPrivateAndStalePID checks Squid's own PID directory:
// created 0700, a dead PID file is removed, and a directory with group access
// is rejected.
func TestPrepareSquidRunDirPrivateAndStalePID(t *testing.T) {
	dir := filepath.Join(tmpfsTempDir(t), "squid")
	if e := PrepareSquidRunDir(dir); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dir)
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("squid run dir mode %o, want 0700", info.Mode().Perm())
	}
	pid := filepath.Join(dir, "squid.pid")
	if e := os.WriteFile(pid, []byte("999999\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	if e := PrepareSquidRunDir(dir); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(pid); !os.IsNotExist(e) {
		t.Fatalf("stale pid file kept: %v", e)
	}
	if e := os.Chmod(dir, 0o750); e != nil {
		t.Fatal(e)
	}
	if e := PrepareSquidRunDir(dir); e == nil {
		t.Fatal("group-accessible squid run dir accepted")
	}
}

// TestRunDirModeIsReadOnlyForGroup checks the panel run directory stays
// read-only for the proxy group.
func TestRunDirModeIsReadOnlyForGroup(t *testing.T) {
	if runDirMode.Perm()&0o020 != 0 {
		t.Fatalf("run directory mode %v grants group write", runDirMode)
	}
}

// tmpfsTempDir returns a temporary directory for run-directory tests. On
// Linux PrepareRunDir requires tmpfs, so the directory is made under /dev/shm
// (tmpfs on Linux hosts and in containers); elsewhere t.TempDir is enough.
func tmpfsTempDir(t *testing.T) string {
	t.Helper()
	if goruntime.GOOS != "linux" {
		return t.TempDir()
	}
	dir, e := os.MkdirTemp("/dev/shm", "kraken-rundir-test-")
	if e != nil {
		t.Skipf("no writable /dev/shm for a tmpfs run directory: %v", e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
