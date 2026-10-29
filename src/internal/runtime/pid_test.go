package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writePIDFile(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "squid.pid")
	if e := os.WriteFile(p, []byte(body), 0o640); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestRemoveStalePIDNoFile(t *testing.T) {
	removed, e := RemoveStalePID(filepath.Join(t.TempDir(), "missing.pid"),
		func(int) (string, error) { t.Fatal("comm unexpected"); return "", nil })
	if removed || e != nil {
		t.Fatalf("got %v %v", removed, e)
	}
}

// Unparseable, empty or non-positive content never names a live squid.
func TestRemoveStalePIDUnparseable(t *testing.T) {
	cases := []string{"not-a-number\n", "0x10\n", "12abc\n", "", "  \n", "-5\n", "0\n"}
	for _, body := range cases {
		t.Run(strconv.Quote(body), func(t *testing.T) {
			dir := t.TempDir()
			p := writePIDFile(t, dir, body)
			comm := func(int) (string, error) {
				t.Fatal("comm unexpected for unparseable content")
				return "", nil
			}
			removed, e := RemoveStalePID(p, comm)
			if !removed || e != nil {
				t.Fatalf("got %v %v", removed, e)
			}
			if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
				t.Fatalf("file still present: %v", e)
			}
		})
	}
}

func TestRemoveStalePIDDeadProcess(t *testing.T) {
	dir := t.TempDir()
	p := writePIDFile(t, dir, "999999\n")
	comm := func(pid int) (string, error) {
		if pid != 999999 {
			t.Fatalf("got pid %d", pid)
		}
		return "", errors.New("no such process")
	}
	removed, e := RemoveStalePID(p, comm)
	if !removed || e != nil {
		t.Fatalf("got %v %v", removed, e)
	}
	if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("file still present: %v", e)
	}
}

func TestRemoveStalePIDOtherProcess(t *testing.T) {
	dir := t.TempDir()
	p := writePIDFile(t, dir, "1\n")
	comm := func(pid int) (string, error) {
		if pid != 1 {
			t.Fatalf("got pid %d", pid)
		}
		return "systemd", nil
	}
	removed, e := RemoveStalePID(p, comm)
	if !removed || e != nil {
		t.Fatalf("got %v %v", removed, e)
	}
	if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("file still present: %v", e)
	}
}

func TestRemoveStalePIDLiveSquid(t *testing.T) {
	dir := t.TempDir()
	p := writePIDFile(t, dir, "4242\n")
	comm := func(pid int) (string, error) {
		if pid != 4242 {
			t.Fatalf("got pid %d", pid)
		}
		return "squid", nil
	}
	removed, e := RemoveStalePID(p, comm)
	if removed || e != nil {
		t.Fatalf("got %v %v", removed, e)
	}
	if _, e := os.Stat(p); e != nil {
		t.Fatalf("file removed: %v", e)
	}
}

func TestRemoveStalePIDDeleteError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; cannot simulate a read-only parent directory")
	}
	parent := t.TempDir()
	ro := filepath.Join(parent, "ro")
	if e := os.Mkdir(ro, 0o700); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(ro, "squid.pid")
	if e := os.WriteFile(p, []byte("garbage\n"), 0o640); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(ro, 0o500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(ro, 0o700)
	comm := func(int) (string, error) { return "", errors.New("no such process") }
	removed, e := RemoveStalePID(p, comm)
	if removed || e == nil {
		t.Fatalf("expected delete error, got %v %v", removed, e)
	}
}
