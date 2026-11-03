//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// capMask parses a CapXxx hex line from /proc/<pid>/status.
func capMask(t *testing.T, status, field string) uint64 {
	t.Helper()
	for _, l := range splitLines(status) {
		if strings.HasPrefix(l, field+":") {
			v, e := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(l, field+":")), 16, 64)
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
	}
	t.Fatalf("no %s", field)
	return 0
}

var pinnedAlpine = regexp.MustCompile(`(?m)^ARG ALPINE_IMAGE=(alpine:[^@\s]+@sha256:[a-f0-9]{64})$`)

var pinnedSquid = regexp.MustCompile(`(?m)^ARG SQUID_IMAGE=[^:\s]+:([0-9]+(?:\.[0-9]+)+)@sha256:[a-f0-9]{64}$`)

// expectedSquidVersion is the Squid version the image under test must carry:
// KRAKEN_TEST_SQUID_VERSION when a run builds the panel with a fresh Squid
// (squid.yml), otherwise the tag of the Squid image pinned in deploy/Dockerfile.
func expectedSquidVersion(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("KRAKEN_TEST_SQUID_VERSION"); v != "" {
		return v
	}
	b, e := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "Dockerfile"))
	if e != nil {
		t.Fatal(e)
	}
	m := pinnedSquid.FindSubmatch(b)
	if m == nil {
		t.Fatal("deploy/Dockerfile: no ARG SQUID_IMAGE=<repo>:<version>@sha256:<digest>")
	}
	return string(m[1])
}

func TestBaseImage(t *testing.T) {
	t.Parallel()
	requireImage(t, testImage())
	const subnet, base = "11.253.40.0/24", "11.253.40"
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	inst := launchInstance(t, &r, fixtures, subnet, base)

	t.Run("squid on alpine, unmodified upstream build of the expected version", func(t *testing.T) {
		want := expectedSquidVersion(t)
		v, e := dockerExec(inst.proxy, "/usr/sbin/squid", "-v")
		if e != nil || !strings.Contains(v, "Squid Cache: Version "+want+"\n") {
			t.Fatalf("squid -v, want %s: %v %q", want, e, v)
		}
		for _, bad := range []string{"--disable-", "--without-", "--enable-"} {
			for _, f := range strings.Fields(v) {
				if strings.Contains(f, bad) && !strings.Contains(f, "--disable-strict-error-checking") {
					t.Errorf("feature-altering configure option %s", f)
				}
			}
		}
		osr, _ := dockerExec(inst.proxy, "cat", "/etc/os-release")
		if !strings.Contains(osr, "ID=alpine") {
			t.Errorf("not alpine: %q", osr)
		}
		for _, tool := range []string{"gcc", "cc", "make"} {
			if _, e := dockerExec(inst.proxy, "sh", "-c", "command -v "+tool); e == nil {
				t.Errorf("build tool %s present in runtime", tool)
			}
		}
	})

	t.Run("users", func(t *testing.T) {
		for user, want := range map[string]string{"proxy": "uid=13(proxy) gid=13(proxy)", "kraken": "uid=10013(kraken) gid=10013(kraken)"} {
			out, e := dockerExec(inst.proxy, "id", user)
			if e != nil || !strings.HasPrefix(strings.TrimSpace(out), want) {
				t.Errorf("id %s: %q", user, out)
			}
		}
		out, _ := dockerExec(inst.proxy, "id", "-Gn", "kraken")
		if !strings.Contains(" "+out+" ", " proxy") {
			t.Errorf("kraken not in group proxy: %q", out)
		}
	})

	// The identity gates rollback to revisions of the same image, so it must
	// come from the Dockerfile build, not a constant that goes stale on bumps.
	t.Run("image identity is the Alpine and Squid references of the build", func(t *testing.T) {
		b, e := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "Dockerfile"))
		if e != nil {
			t.Fatal(e)
		}
		m := pinnedAlpine.FindSubmatch(b)
		if m == nil {
			t.Fatal("deploy/Dockerfile: no ARG ALPINE_IMAGE=alpine:<tag>@sha256:<digest>")
		}
		out, e := dockerExec(inst.proxy, "kraken", "status")
		if e != nil {
			t.Fatal(e, out)
		}
		var image string
		for _, line := range strings.Split(out, "\n") {
			if v, ok := strings.CutPrefix(line, "Образ: "); ok {
				image = strings.TrimSpace(v)
			}
		}
		alpine, squid, ok := strings.Cut(image, "+")
		if !ok || alpine != string(m[1]) || !strings.Contains(squid, "kraken-squid") {
			t.Fatalf("image identity %q, want %s+<squid image>", image, m[1])
		}
		if os.Getenv("KRAKEN_TEST_SQUID_VERSION") == "" && !strings.Contains(squid, ":"+expectedSquidVersion(t)) {
			t.Errorf("squid part %q does not carry version %s", squid, expectedSquidVersion(t))
		}
		ver, e := dockerExec(inst.proxy, "kraken", "version")
		if e != nil {
			t.Fatal(e, ver)
		}
		first, _, _ := strings.Cut(ver, "\n")
		if !strings.HasPrefix(first, "Kraken ") || strings.TrimSpace(strings.TrimPrefix(first, "Kraken ")) == "" {
			t.Errorf("kraken version first line %q, want \"Kraken <version>\"", first)
		}
		if !strings.Contains(out, first) {
			t.Errorf("kraken status does not show the version line %q:\n%s", first, out)
		}
	})

	t.Run("capabilities limited to CHOWN SETUID SETGID KILL", func(t *testing.T) {
		st, e := dockerExec(inst.proxy, "cat", "/proc/1/status")
		if e != nil {
			t.Fatal(e)
		}
		const allowed = uint64(1<<0 | 1<<5 | 1<<6 | 1<<7) // CHOWN, KILL, SETGID, SETUID
		if b := capMask(t, st, "CapBnd"); b&^allowed != 0 {
			t.Errorf("bounding set %x exceeds %x", b, allowed)
		}
		if n := capMask(t, st, "NoNewPrivs"); n != 1 {
			t.Errorf("NoNewPrivs=%d", n)
		}
		// Squid and the panel run with no effective capabilities.
		for _, pat := range []string{"squid", "kraken serve"} {
			pids, _ := dockerExec(inst.proxy, "pgrep", "-f", pat)
			ps := splitLines(pids)
			if len(ps) == 0 {
				t.Errorf("no process %q", pat)
			}
			for _, p := range ps {
				s, e := dockerExec(inst.proxy, "cat", "/proc/"+p+"/status")
				if e != nil {
					continue // short-lived helper
				}
				if c := capMask(t, s, "CapEff"); c != 0 {
					t.Errorf("%s pid %s CapEff=%x", pat, p, c)
				}
			}
		}
	})

	t.Run("reload through manager works after apply", func(t *testing.T) {
		snap, _, _ := opsAllWebAllowlist(t, inst.panel, base)
		inst.panel.apply(t, snap, inst.panel.draftVersion())
		snap2 := deepCopySnap(snap)
		snap2.Clients[0].Name = "renamed for reconfigure"
		inst.panel.apply(t, snap2, inst.panel.draftVersion())
	})
}
