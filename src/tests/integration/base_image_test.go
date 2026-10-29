//go:build integration

package integration

import (
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

func TestBaseImage(t *testing.T) {
	t.Parallel()
	requireImage(t, testImage())
	const subnet, base = "11.253.40.0/24", "11.253.40"
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	inst := launchInstance(t, &r, fixtures, subnet, base)

	t.Run("squid 7.7 on alpine, unmodified upstream build", func(t *testing.T) {
		v, e := dockerExec(inst.proxy, "/usr/sbin/squid", "-v")
		if e != nil || !strings.Contains(v, "Squid Cache: Version 7.7") {
			t.Fatalf("squid -v: %v %q", e, v)
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

	t.Run("capabilities limited to SETUID SETGID KILL", func(t *testing.T) {
		st, e := dockerExec(inst.proxy, "cat", "/proc/1/status")
		if e != nil {
			t.Fatal(e)
		}
		const allowed = uint64(1<<5 | 1<<6 | 1<<7) // KILL, SETGID, SETUID
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
