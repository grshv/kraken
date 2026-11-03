package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// readyFixture builds a run directory with any subset of the three derivative
// files serve writes after recovery: squid.conf, manager-auth.conf and
// proxy-accounts.json. A nil entry omits the file.
func readyFixture(t *testing.T, files map[string]string) *Manager {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if e := os.WriteFile(filepath.Join(dir, name), []byte(body), 0640); e != nil {
			t.Fatal(e)
		}
	}
	return New(dir, "/unused", "/dev/null", "", "", "")
}

// TestSquidReadyGate checks the squid-start readiness contract: the three
// derivative files must all be present and squid.conf non-empty. Missing any
// one is not-ready (no error); a world-accessible file is an error.
func TestSquidReadyGate(t *testing.T) {
	conf := "# config\n"
	auth := "cachemgr_passwd 0000000000000000000000000000000000000000000000000000000000000000 reconfigure rotate\n"
	accounts := "{}"
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"no files", map[string]string{}, false},
		{"only squid.conf", map[string]string{"squid.conf": conf}, false},
		{"squid and auth", map[string]string{"squid.conf": conf, "manager-auth.conf": auth}, false},
		{"all present", map[string]string{"squid.conf": conf, "manager-auth.conf": auth, "proxy-accounts.json": accounts}, true},
		{"empty squid.conf", map[string]string{"squid.conf": "", "manager-auth.conf": auth, "proxy-accounts.json": accounts}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := readyFixture(t, c.files)
			got, e := m.SquidReady()
			if c.want && (e != nil || !got) {
				t.Fatalf("SquidReady -> %v %v, want ready", got, e)
			}
			if !c.want && (e == nil && got) {
				t.Fatalf("SquidReady -> %v %v, want not ready", got, e)
			}
		})
	}
}

// A world-accessible derivative file is a spoofing risk and must be refused.
func TestSquidReadyRejectsWorldAccessible(t *testing.T) {
	conf := "# config\n"
	auth := "cachemgr_passwd 0000000000000000000000000000000000000000000000000000000000000000 reconfigure rotate\n"
	accounts := "{}"
	for _, name := range []string{"squid.conf", "manager-auth.conf", "proxy-accounts.json"} {
		t.Run(name, func(t *testing.T) {
			m := readyFixture(t, map[string]string{
				"squid.conf":          conf,
				"manager-auth.conf":   auth,
				"proxy-accounts.json": accounts,
			})
			if e := os.Chmod(filepath.Join(m.Dir, name), 0644); e != nil {
				t.Fatal(e)
			}
			if _, e := m.SquidReady(); e == nil {
				t.Fatalf("%s world-accessible accepted", name)
			}
		})
	}
}
