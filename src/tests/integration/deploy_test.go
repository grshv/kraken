//go:build integration

package integration

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDeployFreshForeignVolume follows the README quick start on a state
// directory owned by another user (what a fresh bind mount ./data looks like):
// no certificate step and no prepared permissions. The
// container must prepare the layout, issue a CA-signed chain, serve the panel
// and both proxy listeners, and survive a restart without replacing anything.
func TestDeployFreshForeignVolume(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	const base = "11.253.31"
	net := r.addNetwork(randName("sc-dep") + "-n")
	vol := r.addVolume(randName("sc-dep") + "-v")
	docker(t, "network", "create", "--subnet", base+".0/24", net)
	docker(t, "volume", "create", vol)
	// Empty directory owned by a host user, mode 0755; mounted elsewhere so
	// Docker does not seed it from the image.
	docker(t, "run", "--rm", "-v", vol+":/v", "--entrypoint", "sh", testImage(), "-c", "chown 1000:1000 /v && chmod 0755 /v")
	proxy := r.addContainer(randName("sc-dep") + "-p")
	port := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", net, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", vol + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_CERT_NAMES=proxy"}, krakenSecOpts...), testImage())
	})
	waitFor(t, 60, func() error {
		if _, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "test", "-f", "/var/lib/kraken/certs/panel.crt"); !ok {
			return errf("certificates not issued")
		}
		return nil
	})
	layout := docker(t, "exec", "--user", "kraken", proxy, "stat", "-c", "%n %u:%g %a", "/var/lib/kraken", "/var/lib/kraken/data", "/var/lib/kraken/data/telemetry", "/var/lib/kraken/certs", "/var/lib/kraken/log")
	for _, want := range []string{"/var/lib/kraken 10013:13 750", "/var/lib/kraken/data 10013:10013 700", "/var/lib/kraken/data/telemetry 10013:10013 700", "/var/lib/kraken/certs 10013:13 2750", "/var/lib/kraken/log 13:13 2750"} {
		if !strings.Contains(layout, want) {
			t.Errorf("layout: missing %q in\n%s", want, layout)
		}
	}
	keys := docker(t, "exec", "--user", "kraken", proxy, "stat", "-c", "%n %u:%g %a", "/var/lib/kraken/certs/panel.key", "/var/lib/kraken/certs/ca.key", "/var/lib/kraken/certs/proxy.key")
	for _, want := range []string{"panel.key 10013:13 600", "ca.key 10013:13 600", "proxy.key 10013:13 640"} {
		if !strings.Contains(keys, want) {
			t.Errorf("keys: missing %q in\n%s", want, keys)
		}
	}
	password := randPassword(24)
	dockerIn(t, password+"\n", "exec", "-i", proxy, "kraken", "bootstrap", "--username", "admin", "--password-stdin")
	ca := docker(t, "exec", proxy, "kraken", "cert", "ca")
	writeFile(filepath.Join(fixtures, "ca.crt"), []byte(ca))
	panel := newPanel("https://localhost:"+port, filepath.Join(fixtures, "ca.crt"), password)
	panel.login(t, password)
	panel.waitUp(t, 120)
	conf := docker(t, "exec", "--user", controlUser, proxy, "cat", "/dev/shm/kraken/squid.conf")
	for _, want := range []string{"\nhttp_port 3128\n", "\nhttps_port 3129 tls-cert=/var/lib/kraken/certs/proxy.crt "} {
		if !strings.Contains(conf, want) {
			t.Errorf("squid.conf: missing %q", want)
		}
	}
	client := r.addContainer(randName("sc-dep") + "-c")
	docker(t, "run", "-d", "--name", client, "--network", net, "--ip", base+".20",
		"-v", fixtures+":/fixtures:ro", pythonImage, "python", "-c", "import time;time.sleep(3600)")
	waitFor(t, 30, func() error {
		if !proxyTLSOk(t, client, "/fixtures/ca.crt") {
			return errf("TLS proxy listener not verified with the local CA")
		}
		return nil
	})
	panelBefore := docker(t, "exec", "--user", "kraken", proxy, "cat", "/var/lib/kraken/certs/panel.crt")
	docker(t, "restart", proxy)
	waitFor(t, 120, func() error {
		if !tlsOk("127.0.0.1", port, filepath.Join(fixtures, "ca.crt")) {
			return errf("panel not back after restart")
		}
		return nil
	})
	if after := docker(t, "exec", "--user", "kraken", proxy, "cat", "/var/lib/kraken/certs/panel.crt"); after != panelBefore {
		t.Fatal("restart replaced the panel certificate")
	}
}

// TestDeployRefusesForeignDirectory guards against a mount pointing at the
// wrong host directory: nothing is chowned and the container exits with a
// message instead of taking over unrelated files.
func TestDeployRefusesForeignDirectory(t *testing.T) {
	requireImage(t, testImage())
	t.Parallel()
	r := resources{}
	r.track(t)
	vol := r.addVolume(randName("sc-dep") + "-x")
	docker(t, "volume", "create", vol)
	docker(t, "run", "--rm", "-v", vol+":/v", "--entrypoint", "sh", testImage(), "-c", "mkdir /v/etc && echo x > /v/etc/passwd && chown -R 1000:1000 /v && chmod 0755 /v")
	out, err := dockerErr(append(append([]string{"run", "--rm", "-v", vol + ":/var/lib/kraken"}, krakenSecOpts...), testImage())...)
	if err == nil {
		t.Fatal("container started on a foreign directory")
	}
	if !strings.Contains(out, "посторонние файлы: etc") {
		t.Fatalf("missing explanation:\n%s", out)
	}
	owners := docker(t, "run", "--rm", "-v", vol+":/v", "--entrypoint", "stat", testImage(), "-c", "%n %u:%g %a", "/v", "/v/etc", "/v/etc/passwd")
	for _, want := range []string{"/v 1000:1000 755", "/v/etc 1000:1000 755", "/v/etc/passwd 1000:1000"} {
		if !strings.Contains(owners, want) {
			t.Errorf("foreign files changed: missing %q in\n%s", want, owners)
		}
	}
}
