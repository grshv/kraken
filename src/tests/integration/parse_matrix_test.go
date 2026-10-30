//go:build integration

package integration

import (
	"path/filepath"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// TestSquidParseMatrix renders a broad set of policy snapshots through
// policy.Render and checks every produced squid.conf parses inside the real
// image with `squid -k parse`. It is a fast render insurance that runs in
// parallel with the other suites and does not touch host traffic. Scenarios:
// allowlist/all_web, networks and single hosts, allow_private on/off, TLS
// proxy profile with cert, proxy auth required/absent, expert allowlist,
// upstream proxy, cache on/off and extreme settings (ports, timeouts,
// request body limit, log rotations). All configs share one fixtures
// directory and are parsed by a single throwaway container.
func TestSquidParseMatrix(t *testing.T) {
	t.Parallel()
	requireImage(t, testImage())
	fixtures := t.TempDir()

	// Shared installed inputs as they live inside the image mount: a proxy
	// cert/key pair (genOriginCert writes origin.*), the manager auth include
	// and a placeholder proxy-accounts database so rendered paths resolve.
	genOriginCert(t, fixtures)
	writeFile(filepath.Join(fixtures, "manager-auth.conf"), []byte("cachemgr_passwd fixture-token reconfigure rotate\n"))
	writeFile(filepath.Join(fixtures, "proxy-accounts.json"), []byte("[]"))

	opts := policy.Options{
		StateDir:      "/fixture",
		SquidDir:      "/fixture",
		LogPath:       "/fixture/access.log",
		ProxyCert:     "/fixture/origin.crt",
		ProxyKey:      "/fixture/origin.key",
		ProxyAccounts: "/fixture/proxy-accounts.json",
	}

	// baseSnapshot returns a structured allowlist snapshot with an HTTP and a
	// TLS proxy profile so TLS/auth variants only flip a profile id.
	httpProfile := "00000000-0000-4000-8000-000000000001"
	tlsProfile := "00000000-0000-4000-8000-000000000002"
	baseSnapshot := func() policy.Snapshot {
		s := policy.Empty()
		s.TLSProfiles = []policy.TLSProfile{
			{ID: httpProfile, Name: "HTTP proxy / CONNECT", Mode: "tunnel", Transport: "http"},
			{ID: tlsProfile, Name: "TLS proxy", Mode: "tunnel", Transport: "tls", CertRef: strPtr("proxy-server")},
		}
		s.Policies = []policy.Policy{
			{ID: "p-allow", Name: "Allowlist", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, LegacyTLSProfileID: httpProfile},
		}
		s.Clients = []policy.Client{
			{ID: "c-host", Name: "host client", Networks: []string{"192.0.2.10/32"}, Enabled: true, LegacyPolicyID: "p-allow"},
		}
		return s
	}

	// matCase is one rendered snapshot; slug names the output file so the
	// parse report can point at the rejected configuration.
	type matCase struct {
		slug   string
		mutate func(*policy.Snapshot)
	}
	cases := []matCase{
		{"01-allowlist", func(*policy.Snapshot) {}},
		{"02-all_web", func(s *policy.Snapshot) {
			s.Policies[0].LegacyMode = "all_web"
			s.Policies[0].Destinations = nil
		}},
		{"03-networks-and-single-hosts", func(s *policy.Snapshot) {
			s.Clients = append(s.Clients,
				policy.Client{ID: "c-net", Name: "network client", Kind: "network", Networks: []string{"198.51.100.0/24"}, Enabled: true, LegacyPolicyID: "p-allow"},
				policy.Client{ID: "c-group", Name: "group client", Kind: "group", Networks: []string{"203.0.113.5/32"}, Enabled: true, LegacyPolicyID: "p-allow"},
				policy.Client{ID: "c-v6", Name: "v6 client", Networks: []string{"2001:db8::1/128"}, Enabled: true, LegacyPolicyID: "p-allow"},
			)
		}},
		{"04-allow_private_on", func(s *policy.Snapshot) {
			st := *s.Settings
			st.AllowPrivateDestinations = true
			s.Settings = &st
		}},
		{"05-tls-proxy-profile", func(s *policy.Snapshot) {
			s.Policies[0].LegacyTLSProfileID = tlsProfile
		}},
		{"06-proxy-auth-required", func(s *policy.Snapshot) {
			s.Policies[0].LegacyTLSProfileID = tlsProfile
			s.Clients[0].ProxyUsername = "REQUIRED"
		}},
		{"07-proxy-auth-absent", func(s *policy.Snapshot) {
			// Explicit: TLS transport but no proxy username, so no auth_param.
			s.Policies[0].LegacyTLSProfileID = tlsProfile
		}},
		{"08-expert-allowlist", func(s *policy.Snapshot) {
			s.Mode = "expert"
			s.Expert = "acl fixture_src src 192.0.2.1/32\nhttp_access allow fixture_src\nrefresh_pattern -i \\.(deb|rpm)$ 0 20% 4320\nhttp_access deny all\n"
		}},
		{"09-upstream-proxy", func(s *policy.Snapshot) {
			st := *s.Settings
			st.Upstream = &policy.UpstreamProxy{Address: "1.2.3.4", Port: 3128, Exclusive: true}
			s.Settings = &st
		}},
		{"10-cache-on", func(s *policy.Snapshot) {
			st := *s.Settings
			st.CacheEnabled = true
			s.Settings = &st
		}},
		{"11-cache-off", func(s *policy.Snapshot) {
			st := *s.Settings
			st.CacheEnabled = false
			s.Settings = &st
		}},
		{"12-extreme-ports", func(s *policy.Snapshot) {
			st := *s.Settings
			st.HTTPPort = 65535
			st.TLSPort = 65534
			s.Settings = &st
		}},
		{"13-extreme-timeouts", func(s *policy.Snapshot) {
			st := *s.Settings
			st.ConnectTimeout = 1
			st.ReadTimeout = 86400
			st.RequestTimeout = 1
			st.PositiveDNSTTL = 2
			st.NegativeDNSTTL = 1
			s.Settings = &st
		}},
		{"14-extreme-body-and-rotations", func(s *policy.Snapshot) {
			st := *s.Settings
			st.MaxRequestBodyMB = 10240
			st.LogRotations = 30
			s.Settings = &st
		}},
	}

	// Render every snapshot into its own .conf file in the fixtures dir.
	for _, mc := range cases {
		s := baseSnapshot()
		mc.mutate(&s)
		config, err := policy.Render(s, opts)
		if err != nil {
			t.Fatalf("render %s: %v", mc.slug, err)
		}
		writeFile(filepath.Join(fixtures, mc.slug+".conf"), []byte(config))
	}

	// One container parses every config; the script echoes OK/FAIL per file
	// and exits non-zero so a single rejected snapshot fails the test with a
	// readable report. /usr/sbin/squid is the binary path in the kraken image.
	script := "cd /fixture\nrc=0\nfor f in [0-9][0-9]-*.conf; do\n" +
		"  [ -e \"$f\" ] || continue\n" +
		"  if /usr/sbin/squid -k parse -f \"/fixture/$f\" >/tmp/p.out 2>&1; then\n" +
		"    echo \"OK $f\"\n" +
		"  else\n" +
		"    echo \"FAIL $f\"\n" +
		"    cat /tmp/p.out\n" +
		"    rc=1\n" +
		"  fi\n" +
		"done\nexit $rc\n"
	out, err := dockerErr(append(append([]string{"run", "--rm", "-v", fixtures + ":/fixture:ro", "--entrypoint", "/bin/sh"}, krakenSecOpts...), testImage(), "-c", script)...)
	if err != nil {
		t.Fatalf("squid -k parse matrix failed:\n%s", out)
	}
	// Report every result line so the test log records the full matrix.
	for _, line := range splitLines(out) {
		t.Logf("%s", line)
	}
}
