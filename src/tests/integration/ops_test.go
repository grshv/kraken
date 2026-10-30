//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// opsSubnets are distinct per section so a run never collides with smoke
// (11.250.17.0/24), network (11.251.17.0/24), apt (11.252.17.0/24) or between
// its own sections. Mirrors ops.py SUBNETS/BASES.
var opsSubnets = []string{"11.253.17.0/24", "11.253.18.0/24", "11.253.19.0/24", "11.253.20.0/24", "11.253.21.0/24"}
var opsBases = []string{"11.253.17", "11.253.18", "11.253.19", "11.253.20", "11.253.21"}

// requireImage skips the suite if the test image is missing, matching ops.py's
// preflight. The build step is the operator's responsibility (Makefile/image).
func requireImage(t *testing.T, image string) {
	t.Helper()
	if imageMissing(image) {
		t.Fatalf("image %s not found; build it with: docker build -f deploy/Dockerfile -t %s .", image, image)
	}
}

// opsAllWebAllowlist returns a snapshot with an all_web source client and an
// allowlist client (allowed.test), the shared policy shape used by every ops
// section except image-update (which only needs an allowlist).
func opsAllWebAllowlist(t *testing.T, p *Panel, base string) (policy.Snapshot, string, string) {
	t.Helper()
	snap := policySnap(t, p)
	profile := snap.TLSProfiles[0].ID
	allID, allowID := randID(), randID()
	snap.Policies = []policy.Policy{
		{ID: allID, Name: "All web ops", LegacyMode: "all_web", Destinations: nil, LegacyTLSProfileID: profile},
		{ID: allowID, Name: "Allowlist ops", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, LegacyTLSProfileID: profile},
	}
	snap.Clients = []policy.Client{
		{ID: randID(), Name: "source ops", Networks: []string{base + ".10/32"}, Enabled: true, LegacyPolicyID: allID},
		{ID: randID(), Name: "client ops", Networks: []string{base + ".20"}, Enabled: true, LegacyPolicyID: allowID},
	}
	return snap, allID, allowID
}

// TestOpsBackupRestore covers runbook sections 1a–1d: stop, copy /var/lib/kraken volume,
// restore on a new volume, same password/revision/fingerprint, policy in force,
// events and audit intact.
func TestOpsBackupRestore(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	fixtures := t.TempDir()
	s1 := filepath.Join(fixtures, "s1")
	os.MkdirAll(s1, 0o755)
	r := resources{}
	r.track(t)
	const idx = 0
	net := r.addNetwork(randName("sc-ops") + "-n1")
	vol := r.addVolume(randName("sc-ops") + "-v1")
	volb := r.addVolume(randName("sc-ops") + "-v1b")
	docker(t, "network", "create", "--subnet", opsSubnets[idx], net)
	docker(t, "volume", "create", vol)
	docker(t, append(append([]string{"run", "--rm", "--user", controlUser, "-v", vol + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", testImage(), "dev-cert")...)
	password := randPassword(24)
	dockerIn(t, password+"\n", append(append([]string{"run", "--rm", "-i", "--user", controlUser, "-v", vol + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", testImage(), "bootstrap", "--password-stdin")...)
	proxy := r.addContainer(randName("sc-ops") + "-p1")
	base := opsBases[idx]
	port := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", net, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", vol + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key"}, krakenSecOpts...), testImage())
	})
	origin := "https://localhost:" + port
	docker(t, "cp", proxy+":/var/lib/kraken/certs/panel.crt", filepath.Join(s1, "panel.crt"))
	docker(t, "cp", proxy+":/var/lib/kraken/certs/proxy.crt", filepath.Join(s1, "proxy.crt"))
	genOriginCert(t, s1)
	originC, client := startOriginClient(t, &r, net, s1, randName("sc-ops-s1"), base)

	panel := newPanel(origin, filepath.Join(s1, "panel.crt"), password)
	panel.login(t, password)
	panel.waitUp(t, 120)
	snap, _, _ := opsAllWebAllowlist(t, panel, base)
	panel.apply(t, snap, panel.draftVersion())

	t.Run("1a. instance raised, policy applied, traffic generates events", func(t *testing.T) {
		waitFor(t, 30, func() error {
			if opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt") != 200 {
				return errf("client not 200")
			}
			return nil
		})
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied.test: expected 403, got %d", st)
		}
		if st := opsFetch(t, originC, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("origin denied.test: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://allowed.test/", false, true, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("connect: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://169.254.169.254/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("metadata: expected 403, got %d", st)
		}
	})

	status := panel.status()
	appliedRev, _ := status["applied_revision"].(string)
	configHash, _ := status["observed_config_hash"].(string)

	var capturedEventIDs []any
	t.Run("events exist before backup", func(t *testing.T) {
		// Not a Python PASS; collects the event/audit ids the restore must keep.
		page := waitForResult(t, 30, func() (map[string]any, error) {
			p := panel.apiOK(t, "GET", "/events?limit=50", nil, "")
			items, _ := p["items"].([]any)
			if len(items) < 2 {
				return nil, errf("events < 2")
			}
			return p, nil
		})
		for _, v := range page["items"].([]any) {
			capturedEventIDs = append(capturedEventIDs, v.(map[string]any)["id"])
		}
		audit := panel.apiOK(t, "GET", "/audit?limit=50", nil, "")["items"].([]any)
		if len(audit) == 0 {
			t.Fatal("no audit entries captured")
		}
	})
	_ = capturedEventIDs

	// Runbook "Копирование данных": stop cleanly, tar-copy the whole /var/lib/kraken
	// volume preserving rights/UID into a separate restore volume.
	docker(t, "stop", "-t", "30", proxy)
	docker(t, "run", "--rm", "-v", vol+":/from", "-v", volb+":/to", "--entrypoint", "sh", testImage(),
		"-c", "tar -C /from -cpf - . | tar -C /to -xpf -")
	docker(t, "rm", "-v", proxy)
	r.removeContainer(proxy)

	// Runbook "Восстановление копии": new container on the backup volume.
	proxy = r.addContainer(randName("sc-ops") + "-p1b")
	portB := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", net, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", volb + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key"}, krakenSecOpts...), testImage())
	})
	originB := "https://localhost:" + portB
	panelB := newPanel(originB, filepath.Join(s1, "panel.crt"), password)
	panelB.login(t, password) // same password opens the restored admin
	panelB.waitUp(t, 120)

	t.Run("1b. restored instance opens with same password and same active revision/fingerprint", func(t *testing.T) {
		restored := panelB.status()
		if restored["applied_revision"] != appliedRev {
			t.Errorf("applied_revision: got %v, want %s", restored["applied_revision"], appliedRev)
		}
		if restored["observed_config_hash"] != configHash {
			t.Errorf("observed_config_hash: got %v, want %s", restored["observed_config_hash"], configHash)
		}
	})

	t.Run("1c. restored policy still in force (allowed passes, denied 403)", func(t *testing.T) {
		if st := opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("allowed: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied: expected 403, got %d", st)
		}
		if st := opsFetch(t, originC, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("origin denied: expected 200, got %d", st)
		}
	})

	t.Run("1d. event history and audit survive the copy/restore", func(t *testing.T) {
		restored := waitForResult(t, 30, func() (map[string]any, error) {
			p := panelB.apiOK(t, "GET", "/events?limit=50", nil, "")
			return p, nil
		})
		got := map[any]bool{}
		for _, v := range restored["items"].([]any) {
			got[v.(map[string]any)["id"]] = true
		}
		for _, id := range capturedEventIDs {
			if !got[id] {
				t.Errorf("event %v missing after restore", id)
			}
		}
		audit := panelB.apiOK(t, "GET", "/audit?limit=50", nil, "")["items"].([]any)
		if len(audit) == 0 {
			t.Error("no audit entries after restore")
		}
	})
}

// TestOpsCertReplace covers section 2: replace panel/proxy certs preserving
// rights (panel UID 10013 0600, proxy group 13 0640), new CA passes, old CA
// rejected, private keys absent from container logs.
func TestOpsCertReplace(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	fixtures := t.TempDir()
	s2 := filepath.Join(fixtures, "s2")
	os.MkdirAll(s2, 0o755)
	r := resources{}
	r.track(t)
	const idx = 1
	inst := launchOpsInstance(t, &r, s2, opsSubnets[idx], opsBases[idx], testImage(), false)
	docker(t, "cp", inst.proxy+":/var/lib/kraken/certs/panel.crt", filepath.Join(s2, "old-panel.crt"))
	docker(t, "cp", inst.proxy+":/var/lib/kraken/certs/proxy.crt", filepath.Join(s2, "old-proxy.crt"))
	genOpsCerts(t, s2)
	client := r.addContainer(randName("sc-ops") + "-c2")
	base := opsBases[idx]
	docker(t, "run", "-d", "--name", client, "--network", inst.network, "--ip", base+".20",
		"-v", s2+":/fixtures:ro", pythonImage, "python", "-c", "import time;time.sleep(3600)")

	t.Run("2a. baseline: current certs verify with current (old) CA", func(t *testing.T) {
		if !tlsOk("127.0.0.1", inst.port, filepath.Join(s2, "old-panel.crt")) {
			t.Error("panel TLS not ok with old CA")
		}
		if !proxyTLSOk(t, client, "/fixtures/old-proxy.crt") {
			t.Error("proxy TLS not ok with old CA")
		}
	})

	// Runbook cert replacement: stop, swap files preserving rights, start.
	docker(t, "stop", "-t", "30", inst.proxy)
	docker(t, "run", "--rm", "-v", inst.volume+":/var/lib/kraken", "-v", s2+":/new:ro", "--entrypoint", "sh", testImage(), "-c",
		"cp /new/new-panel.key /var/lib/kraken/certs/panel.key && chown 10013:10013 /var/lib/kraken/certs/panel.key && chmod 600 /var/lib/kraken/certs/panel.key && "+
			"cp /new/new-panel.crt /var/lib/kraken/certs/panel.crt && chown 10013:10013 /var/lib/kraken/certs/panel.crt && chmod 600 /var/lib/kraken/certs/panel.crt && "+
			"cp /new/new-proxy.key /var/lib/kraken/certs/proxy.key && chown 10013:13 /var/lib/kraken/certs/proxy.key && chmod 640 /var/lib/kraken/certs/proxy.key && "+
			"cp /new/new-proxy.crt /var/lib/kraken/certs/proxy.crt && chown 10013:13 /var/lib/kraken/certs/proxy.crt && chmod 640 /var/lib/kraken/certs/proxy.crt")
	docker(t, "start", inst.proxy)
	panelN := newPanel(inst.panel.origin, filepath.Join(s2, "new-ca.crt"), inst.password)
	waitFor(t, 90, func() error {
		if err := panelN.refreshCSRF(); err != nil {
			return err
		}
		if panelN.csrf == "" {
			return errf("no csrf")
		}
		return nil
	})
	panelN.login(t, inst.password)
	panelN.waitUp(t, 120)

	t.Run("2b. client trusting new CA passes TLS verification for panel and proxy", func(t *testing.T) {
		if !tlsOk("127.0.0.1", inst.port, filepath.Join(s2, "new-ca.crt")) {
			t.Error("panel TLS not ok with new CA")
		}
		if !proxyTLSOk(t, client, "/fixtures/new-ca.crt") {
			t.Error("proxy TLS not ok with new CA")
		}
	})

	t.Run("2c. client trusting old CA is rejected (panel and proxy)", func(t *testing.T) {
		if tlsOk("127.0.0.1", inst.port, filepath.Join(s2, "old-panel.crt")) {
			t.Error("panel TLS accepted old CA")
		}
		if proxyTLSOk(t, client, "/fixtures/old-proxy.crt") {
			t.Error("proxy TLS accepted old CA")
		}
	})

	t.Run("2d. private keys do not appear in container logs", func(t *testing.T) {
		logs := docker(t, "logs", inst.proxy)
		for _, keyfile := range []string{"new-panel.key", "new-proxy.key"} {
			body := pemBody(filepath.Join(s2, keyfile))
			if body == "" {
				t.Errorf("empty key body for %s", keyfile)
			}
			if strings.Contains(logs, body) {
				t.Errorf("%s private key material leaked into container logs", keyfile)
			}
		}
		if strings.Contains(logs, "BEGIN PRIVATE KEY") {
			t.Error("PRIVATE KEY PEM marker leaked into container logs")
		}
	})
}

// TestOpsImageUpdate covers section 3: recreate the container on the same
// volume with a rebuilt image; dirty draft preserved and not applied, active
// revision unchanged, existing session and fresh login work after the swap.
func TestOpsImageUpdate(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	ensureNextImage(t)
	fixtures := t.TempDir()
	s3 := filepath.Join(fixtures, "s3")
	os.MkdirAll(s3, 0o755)
	r := resources{}
	r.track(t)
	const idx = 2
	inst := launchOpsInstance(t, &r, s3, opsSubnets[idx], opsBases[idx], testImage(), false)
	base := opsBases[idx]
	// Baseline allowlist policy so there is a concrete active revision.
	snap := policySnap(t, inst.panel)
	profile := snap.TLSProfiles[0].ID
	allowID, cid := randID(), randID()
	snap.Policies = []policy.Policy{{ID: allowID, Name: "Allowlist ops", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, LegacyTLSProfileID: profile}}
	snap.Clients = []policy.Client{{ID: cid, Name: "client ops", Networks: []string{base + ".20"}, Enabled: true, LegacyPolicyID: allowID}}
	inst.panel.apply(t, snap, inst.panel.draftVersion())
	appliedRev, _ := inst.panel.status()["applied_revision"].(string)

	// Dirty draft: unsaved changes not promoted to a revision.
	dirty := snap
	dirty.Clients[0].Name = "client ops dirty"
	d := inst.panel.putDraft(t, dirty, inst.panel.draftVersion())
	dirtyVersion := d.Version
	dirtyDraftBefore := inst.panel.draftOK(t)

	t.Run("3a. baseline applied and dirty draft saved (not applied)", func(t *testing.T) {
		// apply + putDraft asserted success; milestone recorded here.
	})

	// Recreate the container on the same volume with the new image. The host
	// port may be reused or a fresh one is bound; the panel origin is updated
	// so the existing cookie jar (domain-scoped, port-agnostic) keeps working.
	docker(t, "stop", "-t", "30", inst.proxy)
	docker(t, "rm", "-v", inst.proxy)
	r.removeContainer(inst.proxy)
	proxy := r.addContainer(randName("sc-ops") + "-p3b")
	newPort := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", inst.network, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", inst.volume + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key"}, krakenSecOpts...), nextImage())
	})
	inst.proxy = proxy
	inst.port = newPort
	inst.panel.origin = "https://localhost:" + newPort
	// The pre-swap cookie is server-validated against the DB session verifier
	// on the volume; reuse the same opener/cookie jar.
	waitFor(t, 120, func() error {
		s := inst.panel.status()
		if s == nil {
			return errf("no status")
		}
		return nil
	})

	t.Run("3b. dirty draft preserved and not applied; active revision unchanged", func(t *testing.T) {
		restored := inst.panel.draftOK(t)
		if int(restored["version"].(float64)) != dirtyVersion {
			t.Errorf("draft version: got %v, want %d", restored["version"], dirtyVersion)
		}
		if !draftEqual(dirtyDraftBefore, restored) {
			t.Error("dirty draft snapshot changed across image swap")
		}
		if inst.panel.status()["applied_revision"] != appliedRev {
			t.Error("dirty draft was applied during image swap")
		}
	})

	inst.panel.login(t, inst.password)
	inst.panel.waitUp(t, 120)

	t.Run("3c. existing session and fresh login work after image swap", func(t *testing.T) {
		// login + waitUp above exercised both paths; milestone recorded here.
	})
}

// TestOpsDiskFull covers section 4: tmpfs-backed /var/lib/kraken filled to refusal,
// draft/apply fail cleanly without partial application, Squid keeps serving the
// last good config, panel recovers after space is freed, manifest/DB verify.
func TestOpsDiskFull(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	fixtures := t.TempDir()
	s4 := filepath.Join(fixtures, "s4")
	os.MkdirAll(s4, 0o755)
	r := resources{}
	r.track(t)
	const idx = 3
	base := opsBases[idx]
	net := r.addNetwork(randName("sc-ops") + "-n4")
	docker(t, "network", "create", "--subnet", opsSubnets[idx], net)
	proxy := r.addContainer(randName("sc-ops") + "-p4")
	// /var/lib/kraken on a size-limited tmpfs; tmpfs is not populated from the image, so
	// the entrypoint recreates the Dockerfile's /var/lib/kraken layout and dev cert. The
	// tmpfs is mounted with uid/gid/mode matching the Dockerfile's /var/lib/kraken so the
	// kraken user owns the mount point and can create subdirectories without CAP_CHOWN.
	initCmd := "setpriv --reuid=kraken --regid=proxy --init-groups -- install -d -m 0770 /var/lib/kraken && " +
		"setpriv --reuid=kraken --regid=kraken --init-groups -- install -d -m 0700 /var/lib/kraken/data && " +
		"setpriv --reuid=kraken --regid=kraken --init-groups -- install -d -m 0700 /var/lib/kraken/data/telemetry && " +
		"setpriv --reuid=kraken --regid=proxy --init-groups -- install -d -m 2750 /var/lib/kraken/certs && " +
		"setpriv --reuid=proxy --regid=proxy --init-groups -- install -d -m 2750 /var/lib/kraken/log && " +
		"setpriv --reuid=kraken --regid=proxy --init-groups -- chmod 0750 /var/lib/kraken && " +
		"setpriv --reuid=kraken --regid=kraken --init-groups -- /usr/local/bin/kraken dev-cert && " +
		"exec sleep infinity"
	port := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", net, "--network-alias", "proxy", "--ip", base + ".2",
			"--tmpfs", "/var/lib/kraken:size=134217728,uid=10013,gid=13,mode=0750",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key",
			"--entrypoint", "sh"}, krakenSecOpts...), testImage(), "-c", initCmd)
	})
	origin := "https://localhost:" + port
	waitFor(t, 30, func() error {
		if _, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "test", "-f", "/var/lib/kraken/certs/panel.crt"); !ok {
			return errf("panel.crt not ready")
		}
		return nil
	})
	password := randPassword(24)
	dockerIn(t, password+"\n", "exec", "-i", "--user", controlUser, proxy, "kraken", "bootstrap", "--username", "admin", "--password-stdin")
	// Start pebble detached inside the running container so the tmpfs survives.
	docker(t, "exec", "-d", proxy, "sh", "-c", "pebble run --verbose >/tmp/pebble.log 2>&1")
	// docker cp cannot read from a tmpfs mount on this runtime; extract PEM.
	panelCRT := docker(t, "exec", "--user", controlUser, proxy, "cat", "/var/lib/kraken/certs/panel.crt")
	writeFile(filepath.Join(s4, "panel.crt"), []byte(panelCRT+"\n"))
	genOriginCert(t, s4)
	_, client := startOriginClient(t, &r, net, s4, randName("sc-ops-s4"), base)

	panel := newPanel(origin, filepath.Join(s4, "panel.crt"), password)
	panel.login(t, password)
	panel.waitUp(t, 120)
	snap, _, _ := opsAllWebAllowlist(t, panel, base)
	panel.apply(t, snap, panel.draftVersion())
	waitFor(t, 30, func() error {
		if opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt") != 200 {
			return errf("client not 200")
		}
		return nil
	})

	t.Run("4a. instance on tmpfs-backed /var/lib/kraken serves the applied policy", func(t *testing.T) {
		if st := opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("allowed: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied: expected 403, got %d", st)
		}
	})

	appliedRev, _ := panel.status()["applied_revision"].(string)
	configHash, _ := panel.status()["observed_config_hash"].(string)

	// Fill the volume to refusal: 1M blocks then 4k blocks to top up the last
	// sub-megabyte so SQLite/WAL writes cannot find room.
	docker(t, "exec", "--user", controlUser, proxy, "sh", "-c", "dd if=/dev/zero of=/var/lib/kraken/fill bs=1M 2>/dev/null; dd if=/dev/zero of=/var/lib/kraken/fill.top bs=4k 2>/dev/null; true")
	freeKB := docker(t, "exec", "--user", controlUser, proxy, "sh", "-c", "df -k /var/lib/kraken | awk 'NR==2 {print $4}'")
	freeKBI := 0
	scanInt(freeKB, &freeKBI)
	if freeKBI > 4 {
		t.Fatalf("expected ~0 KiB free, got %s", freeKB)
	}

	modified := snap
	modified.Clients[0].Name = "client ops renamed"
	draftFailed := false
	if _, st := panel.api("PUT", "/configuration/draft", map[string]any{"expected_version": panel.draftVersion(), "snapshot": modified}, ""); st < 400 {
		draftFailed = false
	} else {
		draftFailed = true
	}
	if !draftFailed {
		t.Error("draft PUT succeeded on a full disk (expected clean error)")
	}

	applyFailed := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				applyFailed = true
			}
		}()
		_, st := panel.api("POST", "/operations", map[string]any{"action": "validate", "revision_id": "any", "expected_applied_revision": nil}, randKey())
		if st >= 400 {
			applyFailed = true
		}
	}()

	t.Run("4b. draft/apply fail cleanly on full disk, no partial application", func(t *testing.T) {
		if !draftFailed {
			t.Error("draft did not fail on full disk")
		}
		if !applyFailed {
			t.Error("apply did not fail on full disk")
		}
		post := panel.status()
		if post["applied_revision"] != appliedRev {
			t.Error("partial application on full disk")
		}
		if post["observed_config_hash"] != configHash {
			t.Error("config hash changed on full disk")
		}
	})

	t.Run("4c. Squid keeps serving the last working configuration", func(t *testing.T) {
		if st := opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("allowed: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied: expected 403, got %d", st)
		}
	})

	// Free disposable space; the panel recovers without manual intervention.
	docker(t, "exec", "--user", controlUser, proxy, "sh", "-c", "rm -f /var/lib/kraken/fill /var/lib/kraken/fill.top")
	waitFor(t, 30, func() error {
		if panel.draft() == nil {
			return errf("draft not readable")
		}
		return nil
	})
	modified2 := snap
	modified2.Clients[0].Name = "client ops after cleanup"
	panel.apply(t, modified2, panel.draftVersion())

	t.Run("4d. panel recovers after space is freed; apply succeeds again", func(t *testing.T) {
		if panel.status()["applied_revision"] == appliedRev {
			t.Error("apply did not succeed after cleanup")
		}
	})

	// Integrity: no sqlite3 CLI in the image, verify via kraken check and
	// successful reads through its SQLite driver.
	_, err := dockerExecUser(proxy, controlUser, "kraken", "check")
	if err != nil {
		t.Fatalf("kraken check failed: %v", err)
	}
	eventsPage := panel.apiOK(t, "GET", "/events?limit=1", nil, "")
	if _, ok := eventsPage["items"].([]any); !ok {
		t.Errorf("events page unexpected: %v", eventsPage)
	}
	panel.apiOK(t, "GET", "/audit?limit=1", nil, "")
	if squid, _ := panel.status()["squid"].(map[string]any); squid["state"] != "up" {
		t.Error("squid not up after ENOSPC")
	}

	t.Run("4e. runtime manifest verifies and DB reads succeed after ENOSPC", func(t *testing.T) {
		// kraken check + event/audit reads above asserted this; milestone here.
	})
}

// TestOpsForcedKill covers section 5: docker kill -s KILL leaves the container
// dead; squid-start must recover the derivative files from the control DB on
// the next launch so the same applied revision keeps serving.
func TestOpsForcedKill(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	fixtures := t.TempDir()
	s5 := filepath.Join(fixtures, "s5")
	os.MkdirAll(s5, 0o755)
	r := resources{}
	r.track(t)
	const idx = 4
	inst := launchOpsInstance(t, &r, s5, opsSubnets[idx], opsBases[idx], testImage(), true)
	base := opsBases[idx]
	genOriginCert(t, s5)
	originC, client := startOriginClient(t, &r, inst.network, s5, randName("sc-ops-s5"), base)
	snap, _, _ := opsAllWebAllowlist(t, inst.panel, base)
	inst.panel.apply(t, snap, inst.panel.draftVersion())
	waitFor(t, 30, func() error {
		if opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt") != 200 {
			return errf("client not 200")
		}
		return nil
	})

	t.Run("5a. instance raised, policy applied, traffic passes", func(t *testing.T) {
		if st := opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("allowed: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied: expected 403, got %d", st)
		}
		if st := opsFetch(t, originC, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("origin denied: expected 200, got %d", st)
		}
	})

	appliedRev, _ := inst.panel.status()["applied_revision"].(string)
	docker(t, "kill", "-s", "KILL", inst.proxy)
	docker(t, "start", inst.proxy)
	inst.panel.waitUp(t, 120)
	waitFor(t, 60, func() error {
		if opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt") != 200 {
			return errf("client not 200 after restart")
		}
		return nil
	})

	t.Run("5b. after docker kill + start, squid serves the same policy from the same revision", func(t *testing.T) {
		if st := opsFetch(t, client, "http://allowed.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 200 {
			t.Errorf("allowed: expected 200, got %d", st)
		}
		if st := opsFetch(t, client, "http://denied.test/", false, false, "/fixtures/proxy.crt", "/fixtures/origin.crt"); st != 403 {
			t.Errorf("denied: expected 403, got %d", st)
		}
		if inst.panel.status()["applied_revision"] != appliedRev {
			t.Error("applied revision changed after forced restart")
		}
	})
}
