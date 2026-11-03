package policy

import (
	"strings"
	"testing"
)

func TestExpertConfigAndTemplate(t *testing.T) {
	s := fixture()
	s.Mode = "expert"
	s.Expert = "acl office src 192.0.2.0/24\nhttp_access allow office\nrefresh_pattern -i \\.(deb|rpm)$ 0 20% 4320\ncache deny office\n"
	o := Options{StateDir: "/data/runtime", LogPath: "/data/logs/access.log"}
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, s.Expert) || strings.Contains(c, "acl client_0") {
		t.Fatal("expert policy mixed with inactive visual rules")
	}
	if strings.Index(c, "http_access deny sc_protected_dst") > strings.Index(c, "http_access allow office") {
		t.Fatal("expert bypassed protection")
	}
	if strings.Index(c, "cache deny office") > strings.Index(c, "cache deny all") {
		t.Fatal("cache fallback shadows expert exceptions")
	}
	template, e := ExpertTemplate(fixture(), o)
	if e != nil {
		t.Fatal(e)
	}
	s.Expert = template
	if _, e = Render(s, o); e != nil {
		t.Fatalf("generated template is not valid expert config: %v", e)
	}
}
func TestExpertProtectedRuntimeAndExecution(t *testing.T) {
	for _, text := range []string{"ssl_unmanaged allow all", "security_unmanaged on", "ss\\l_unmanaged allow all", "include /data/app/*", "auth_param basic program /bin/sh -c bad", "url_rewrite_program /bin/sh", "pid_filename /data/app/a", "http_port 9000", "https_port 9443", "snmp_port 3401", "icp_port 3130", "htcp_port 4827", "snmp_access allow all", "icp_access allow all", "htcp_access allow all", "adaptation_access allow all", "icap_service foo", "reply_header_access X-Kraken-Revision deny all", "reply_header_replace Server server/1.0", "reply_header_remove X-Kraken-Revision", "request_header_add X-Probe test", "cache_dir ufs /data/app 50 16 256", "acl sc_safe_ports port 25", "http_access allow sc_probe_listener", "if true\nhttp_access allow all", "endif", "in\\clude /data/app/*", "url_rewrite_\\\nprogram /bin/sh", "request_header_add test value\\"} {
		s := fixture()
		s.Mode = "expert"
		s.Expert = text
		if e := Validate(s, Options{}); e == nil {
			t.Fatalf("accepted dangerous config: %q", text)
		}
	}
}

// TestExpertValidateRenderEquivalence guards against divergence between the
// form validateExpert analyzes (comments stripped + line-continuations merged)
// and the form Render writes verbatim into active.conf. A directive that
// validateExpert accepts but Squid would see as forbidden (or vice versa) is
// the failure mode this test catches. Cases cover: comment hiding a forbidden
// directive, line continuation across comment+directive, trailing backslash,
// and quote/whitespace variants that all tokenize identically after Squid's
// own normalization.
func TestExpertValidateRenderEquivalence(t *testing.T) {
	cases := []struct {
		name   string
		expert string
		// accept indicates whether validateExpert should accept this expert
		// block. Render is then invoked and the produced config must contain
		// the same forbidden/allowed directive tokens validateExpert saw.
		accept bool
	}{
		{
			name:   "continuation merges directive across lines",
			expert: "acl office src 192.0.2.0/24 \\\nhttp_access allow office",
			accept: true,
		},
		{
			name:   "comment with hash inside continuation is still a comment",
			expert: "# http_port 9000\nacl office src 192.0.2.0/24\nhttp_access allow office",
			accept: true,
		},
		{
			name:   "forbidden directive split across continuation still rejected",
			expert: "http_\\\nport 9000",
			accept: false,
		},
		{
			name:   "forbidden directive inside comment accepted (comment is dropped before tokenizing)",
			expert: "# http_port 9000\nacl ok src 127.0.0.1\nhttp_access allow ok",
			accept: true,
		},
		{
			name:   "trailing backslash alone is an unfinished continuation",
			expert: "acl ok src 127.0.0.1\\",
			accept: false,
		},
		{
			name:   "quoted directive with hash inside is parsed literally by Squid stripped by guard",
			expert: "acl tag dstdomain \"foo#bar\"\nhttp_access allow tag",
			accept: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.Mode = "expert"
			s.Expert = tc.expert
			err := Validate(s, Options{StateDir: "/data", LogPath: "/data/log"})
			if tc.accept && err != nil {
				t.Fatalf("validateExpert rejected acceptable input: %v", err)
			}
			if !tc.accept && err == nil {
				t.Fatalf("validateExpert accepted forbidden input: %q", tc.expert)
			}
			if !tc.accept {
				return
			}
			// Verify the rendered config preserves the expert body verbatim so
			// Squid parses the same string the guard analyzed.
			c, e := Render(s, Options{StateDir: "/data", LogPath: "/data/log"})
			if e != nil {
				t.Fatalf("Render rejected what validateExpert accepted: %v", e)
			}
			if !strings.Contains(c, tc.expert) && !strings.Contains(c, strings.ReplaceAll(tc.expert, "\\\n", "")) {
				t.Fatalf("rendered config does not contain the expert body.\nexpert: %q\nconfig: %q", tc.expert, c)
			}
		})
	}
}
func TestSettingsValidationAndRendering(t *testing.T) {
	s := fixture()
	v := DefaultSettings()
	s.Settings = &v
	v.HTTPPort = 8080
	v.TLSPort = 8444
	v.SafePorts = append(v.SafePorts, 8081)
	v.CacheEnabled = true
	v.CacheMemoryMB = 96
	v.DNSNameservers = []string{"192.0.2.53"}
	v.AllowPrivateDestinations = true
	c, e := Render(s, Options{StateDir: "/data", LogPath: "/data/access.log"})
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"http_port 127.0.0.1:3130", "http_port 8080", "cache_mem 96 MB", "cache allow all", "dns_nameservers 192.0.2.53", "http_access deny sc_protected_dst"} {
		if !strings.Contains(c, want) {
			t.Fatal("missing " + want)
		}
	}
	if strings.Contains(c, "http_access deny sc_private_dst") {
		t.Fatal("explicit private destination choice ignored")
	}
	cases := map[string]func(*Settings){"reserved port": func(v *Settings) { v.HTTPPort = 3130 }, "smtp": func(v *Settings) { v.SafePorts = []int{25, 443} }, "ssh": func(v *Settings) { v.SafePorts = []int{22, 443} }, "telnet": func(v *Settings) { v.SafePorts = []int{23, 443} }, "tor": func(v *Settings) { v.SafePorts = []int{9050, 443} }, "submission": func(v *Settings) { v.SafePorts = []int{587, 443} }, "CONNECT mismatch": func(v *Settings) { v.ConnectPorts = []int{9999} }, "DNS injection": func(v *Settings) { v.DNSNameservers = []string{"1.1.1.1\nhttp_access allow all"} }, "timeout": func(v *Settings) { v.ReadTimeout = 0 }, "memory": func(v *Settings) { v.CacheMemoryMB = -1 }, "TTL": func(v *Settings) { v.NegativeDNSTTL = v.PositiveDNSTTL }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := DefaultSettings()
			s.Settings = &v
			mutate(&v)
			if e := Validate(s, Options{}); e == nil {
				t.Fatal("invalid setting accepted")
			}
		})
	}
}

func TestManagementListenerIsProtected(t *testing.T) {
	s := Empty()
	config, err := Render(s, Options{StateDir: "/tmp/runtime", LogPath: "/tmp/access.log"})
	if err != nil {
		t.Fatal(err)
	}
	allow := strings.Index(config, "http_access allow sc_probe_source sc_manager_listener sc_manager_method sc_manager_path manager")
	deny := strings.Index(config, "http_access deny sc_manager_listener")
	user := strings.Index(config, "# BEGIN user policy")
	if allow < 0 || deny < allow || user < deny || !strings.Contains(config, "http_port 127.0.0.1:3131") {
		t.Fatal(config)
	}
	s.Mode = "expert"
	s.Expert = "cachemgr_passwd none all"
	if _, err := Render(s, Options{}); err == nil {
		t.Fatal("expert changed manager access")
	}
}

func TestExpertAllowlistRejectsDangerousDirectives(t *testing.T) {
	cases := []struct {
		name   string
		expert string
	}{
		{"err_page_stylesheet reads file", "err_page_stylesheet /x"},
		{"ftp_port new listener", "ftp_port 2121"},
		{"acl reads file via quoted path", "acl x src \"/etc/hosts\""},
		{"acl parameters macro reads file", "acl x dstdomain parameters(\"/etc/hosts\")"},
		{"deny_info bare path", "deny_info /x all"},
		{"cache_peer link-local metadata", "cache_peer 169.254.169.254 parent 80 0 no-query originserver"},
		{"cache_peer loopback", "cache_peer 127.0.0.1 parent 8443 0"},
		{"cache_peer v4-mapped loopback", "cache_peer ::ffff:127.0.0.1 parent 80 0"},
		{"cache_peer private when closed", "cache_peer 10.0.0.5 parent 3128 0"},
		{"cache_peer hostname", "cache_peer peer.example parent 3128 0"},
		{"cache_peer file option", "cache_peer 198.51.100.1 parent 3128 0 tls-cert=/run/secrets/proxy.key"},
		{"directive uppercase token", "HTTP_ACCESS allow all"},
		{"directive quoted token", "\"acl\" x src 1.2.3.4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.Mode = "expert"
			s.Expert = tc.expert
			if e := Validate(s, Options{}); e == nil {
				t.Fatalf("accepted dangerous expert config: %q", tc.expert)
			}
		})
	}
}

func TestExpertAllowlistAcceptsSafeDirectives(t *testing.T) {
	cases := []struct {
		name         string
		expert       string
		allowPrivate bool
	}{
		{"cache_peer private when allowed", "cache_peer 10.0.0.5 parent 3128 0 no-query", true},
		{"cache_peer public host", "cache_peer 8.8.8.8 parent 3128 0", false},
		{"deny_info ERR template", "deny_info ERR_ACCESS_DENIED office", false},
		{"deny_info redirect", "deny_info 302:https://example.org/blocked office", false},
		{"deny_info tcp reset", "deny_info TCP_RESET office", false},
		{"delay_pools", "delay_pools 1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.Mode = "expert"
			settings := EffectiveSettings(s)
			settings.AllowPrivateDestinations = tc.allowPrivate
			s.Settings = &settings
			s.Expert = tc.expert
			if e := Validate(s, Options{StateDir: "/data", LogPath: "/data/log"}); e != nil {
				t.Fatalf("rejected safe expert config: %v", e)
			}
		})
	}
}

func TestProtectedDestinationsRenderedFromList(t *testing.T) {
	s := Empty()
	o := Options{StateDir: "/tmp/runtime", LogPath: "/tmp/access.log"}
	config, err := Render(s, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"2002::/16", "2001::/32", "64:ff9b:1::/48"} {
		if !strings.Contains(config, "acl sc_protected_dst dst ") || !strings.Contains(config, want) {
			t.Fatalf("protected ACL missing %s:\n%s", want, config)
		}
	}
	idx := strings.Index(config, "acl sc_protected_dst dst ")
	end := strings.Index(config[idx:], "\n")
	line := config[idx : idx+end]
	last := -1
	for _, want := range []string{"2001::/32", "2002::/16", "64:ff9b:1::/48"} {
		at := strings.Index(line, want)
		if at < 0 || at < last {
			t.Fatalf("protected ACL ordering broke for %s: %s", want, line)
		}
		last = at
	}
}

func TestUserSectionLine(t *testing.T) {
	config := "header\n# BEGIN user policy\nfirst\nsecond\n# END user policy\ntail\n"
	cases := []struct {
		name       string
		configLine int
		wantN      int
		wantOK     bool
	}{
		{"first line of section", 3, 1, true},
		{"second line of section", 4, 2, true},
		{"begin marker", 2, 0, false},
		{"end marker", 6, 0, false},
		{"line before section", 1, 0, false},
		{"line after end", 7, 0, false},
		{"zero line", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := UserSectionLine(config, tc.configLine)
			if n != tc.wantN || ok != tc.wantOK {
				t.Fatalf("UserSectionLine(%d) = (%d, %v), want (%d, %v)", tc.configLine, n, ok, tc.wantN, tc.wantOK)
			}
		})
	}
}

func TestUpstreamProxyValidation(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	accept := []struct {
		name    string
		address string
		private bool
	}{
		{"public IPv4", "1.1.1.1", false},
		{"public IPv6", "2606:4700:4700::1111", false},
		{"private IPv4 when private closed", "10.0.0.5", false},
		{"private IPv4 when private open", "10.0.0.5", true},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			v := DefaultSettings()
			v.AllowPrivateDestinations = tc.private
			v.Upstream = &UpstreamProxy{Address: tc.address, Port: 3128}
			s.Settings = &v
			if e := Validate(s, o); e != nil {
				t.Fatalf("rejected valid upstream: %v", e)
			}
		})
	}
	reject := []struct {
		name    string
		address string
		port    int
	}{
		{"loopback v4", "127.0.0.1", 3128},
		{"loopback v6", "::1", 3128},
		{"link-local v4", "169.254.169.254", 3128},
		{"v4-mapped link-local", "::ffff:169.254.169.254", 3128},
		{"unspecified v4", "0.0.0.0", 3128},
		{"multicast v4", "224.0.0.1", 3128},
		{"hostname", "peer.example", 3128},
		{"port zero", "1.1.1.1", 0},
		{"port too high", "1.1.1.1", 65536},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			v := DefaultSettings()
			v.Upstream = &UpstreamProxy{Address: tc.address, Port: tc.port}
			s.Settings = &v
			if e := Validate(s, o); e == nil {
				t.Fatal("accepted invalid upstream")
			}
		})
	}
}

func TestUpstreamProxyRender(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	sNil := fixture()
	v := DefaultSettings()
	sNil.Settings = &v
	cNil, e := Render(sNil, o)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(cNil, "cache_peer") || strings.Contains(cNil, "never_direct") {
		t.Fatal("nil upstream leaked cache_peer/never_direct")
	}
	// Non-exclusive upstream: only the cache_peer line is added.
	s := fixture()
	v = DefaultSettings()
	v.Upstream = &UpstreamProxy{Address: "1.1.1.1", Port: 3128}
	s.Settings = &v
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	want := "cache_peer 1.1.1.1 parent 3128 0 no-query no-digest default name=sc_upstream\n"
	if !strings.Contains(c, want) {
		t.Fatalf("missing cache_peer line\nwant: %q\ngot: %q", want, c)
	}
	if strings.Contains(c, "never_direct") {
		t.Fatal("non-exclusive upstream emitted never_direct")
	}
	// Exclusive upstream additionally emits never_direct allow all.
	s2 := fixture()
	v = DefaultSettings()
	v.Upstream = &UpstreamProxy{Address: "1.1.1.1", Port: 3128, Exclusive: true}
	s2.Settings = &v
	c2, e := Render(s2, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c2, want) || !strings.Contains(c2, "never_direct allow all\n") {
		t.Fatalf("exclusive upstream missing directives: %q", c2)
	}
	// IPv6 address rendered as-is, without brackets.
	s3 := fixture()
	v = DefaultSettings()
	v.Upstream = &UpstreamProxy{Address: "2606:4700:4700::1111", Port: 3128}
	s3.Settings = &v
	c3, e := Render(s3, o)
	if e != nil {
		t.Fatal(e)
	}
	want6 := "cache_peer 2606:4700:4700::1111 parent 3128 0 no-query no-digest default name=sc_upstream\n"
	if !strings.Contains(c3, want6) {
		t.Fatalf("IPv6 address not rendered as-is\nwant: %q\ngot: %q", want6, c3)
	}
}

func TestPeerAddressRejectsZone(t *testing.T) {
	s := DefaultSettings()
	s.Upstream = &UpstreamProxy{Address: "2606:4700::1%x\nhttp_port 1", Port: 3128}
	if validateSettings(s) == nil {
		t.Fatal("zoned upstream address accepted")
	}
	if e := validateExpert("cache_peer fe80::1%eth0 parent 3128 0", false); e == nil {
		t.Fatal("zoned expert cache_peer accepted")
	}
}
