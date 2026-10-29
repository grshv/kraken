//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// managerSecretHash returns a hash of the manager auth file so the secret
// itself never reaches test output.
func managerSecretHash(t *testing.T, container string) string {
	t.Helper()
	out, e := dockerExecUser(container, controlUser, "cat", "/dev/shm/kraken/manager-auth.conf")
	if e != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("manager-auth.conf not readable in tmpfs: %v", e)
	}
	h := sha256.Sum256([]byte(out))
	return hex.EncodeToString(h[:])
}

func TestLayout(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	const subnet, base = "11.253.30.0/24", "11.253.30"
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	inst := launchInstance(t, &r, fixtures, subnet, base)

	t.Run("volume holds only state", func(t *testing.T) {
		if _, e := dockerExec(inst.proxy, "test", "-e", "/data"); e == nil {
			t.Error("/data still exists")
		}
		// find must traverse log/ (2750 proxy:proxy); the numeric controlUser
		// spec has no supplementary groups, so drop via setpriv --init-groups
		// which adds proxy membership from /etc/group.
		out, e := dockerExec(inst.proxy, "setpriv", "--reuid=kraken", "--regid=kraken", "--init-groups", "--", "find", "/var/lib/kraken", "-mindepth", "1")
		if e != nil {
			t.Fatal(e)
		}
		allowed := regexp.MustCompile(`^/var/lib/kraken/(data|certs|log)(/|$)`)
		forbidden := regexp.MustCompile(`(manager|journal|lkg|\.lock$|\.pid$|squid\.conf|active\.conf|proxy-accounts)`)
		for _, p := range splitLines(out) {
			if !allowed.MatchString(p) {
				t.Errorf("unexpected path in volume: %s", p)
			}
			if forbidden.MatchString(filepath.Base(p)) {
				t.Errorf("derived or secret file on volume: %s", p)
			}
		}
		for _, p := range []string{"/var/lib/kraken/data/control.sqlite3", "/var/lib/kraken/data/telemetry/rollups.db", "/var/lib/kraken/log/access.log", "/var/lib/kraken/certs/panel.key"} {
			if _, e := dockerExec(inst.proxy, "setpriv", "--reuid=kraken", "--regid=kraken", "--init-groups", "--", "test", "-f", p); e != nil {
				t.Errorf("missing %s", p)
			}
		}
	})

	t.Run("derived files live in tmpfs", func(t *testing.T) {
		fsType, e := dockerExec(inst.proxy, "stat", "-f", "-c", "%T", "/dev/shm/kraken")
		if e != nil || strings.TrimSpace(fsType) != "tmpfs" {
			t.Errorf("/dev/shm/kraken fs type %q", fsType)
		}
		for _, f := range []string{"squid.conf", "manager-auth.conf", "proxy-accounts.json"} {
			if _, e := dockerExecUser(inst.proxy, controlUser, "test", "-f", "/dev/shm/kraken/"+f); e != nil {
				t.Errorf("missing /dev/shm/kraken/%s", f)
			}
		}
		own, _ := dockerExec(inst.proxy, "stat", "-c", "%U:%G %a", "/dev/shm/kraken")
		f := strings.Fields(own)
		if len(f) != 2 || f[0] != "kraken:proxy" || !strings.HasSuffix(f[1], "0") {
			t.Errorf("/dev/shm/kraken owner/mode %q, want kraken:proxy without other access", own)
		}
		// Squid can read the derived files but never create, replace or delete
		// them; its PID lives in its own private tmpfs directory.
		if _, e := dockerExecUser(inst.proxy, "proxy", "touch", "/dev/shm/kraken/planted"); e == nil {
			t.Error("proxy user can create files in /dev/shm/kraken")
		}
		if _, e := dockerExecUser(inst.proxy, "proxy", "mv", "/dev/shm/kraken/squid.conf", "/dev/shm/kraken/x"); e == nil {
			t.Error("proxy user can rename squid.conf")
		}
		if _, e := dockerExecUser(inst.proxy, "proxy", "cat", "/dev/shm/kraken/squid.conf"); e != nil {
			t.Error("proxy user cannot read squid.conf")
		}
		sq, _ := dockerExec(inst.proxy, "stat", "-c", "%U:%G %a", "/dev/shm/kraken-squid")
		if strings.TrimSpace(sq) != "proxy:proxy 700" {
			t.Errorf("/dev/shm/kraken-squid owner/mode %q, want proxy:proxy 700", sq)
		}
		// The squid process cannot read the panel database.
		if _, e := dockerExecUser(inst.proxy, "proxy", "cat", "/var/lib/kraken/data/control.sqlite3"); e == nil {
			t.Error("proxy user can read control.sqlite3")
		}
	})

	pw1 := managerSecretHash(t, inst.proxy)
	snap, _, _ := opsAllWebAllowlist(t, inst.panel, base)

	t.Run("panel restart keeps the manager password and apply works", func(t *testing.T) {
		if out, e := dockerExecCombined(inst.proxy, "pebble", "restart", "control"); e != nil {
			t.Fatalf("pebble restart control: %v %s", e, tail(out, 5))
		}
		inst.panel.waitUp(t, 60)
		if managerSecretHash(t, inst.proxy) != pw1 {
			t.Error("manager password changed on panel-only restart")
		}
		inst.panel.apply(t, snap, inst.panel.draftVersion())
	})

	appliedRev, _ := inst.panel.status()["applied_revision"].(string)

	t.Run("container restart rotates the manager password, config survives", func(t *testing.T) {
		docker(t, "restart", inst.proxy)
		inst.panel.waitUp(t, 120)
		if managerSecretHash(t, inst.proxy) == pw1 {
			t.Error("manager password survived a container restart")
		}
		if inst.panel.status()["applied_revision"] != appliedRev {
			t.Error("applied revision changed after restart")
		}
		// Reconfigure with the new password: apply a changed snapshot.
		snap2 := deepCopySnap(snap)
		snap2.Clients[0].Name = "source ops renamed"
		inst.panel.apply(t, snap2, inst.panel.draftVersion())
	})
}
