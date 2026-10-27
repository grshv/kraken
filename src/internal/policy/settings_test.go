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
	for _, text := range []string{"ssl_unmanaged allow all", "security_unmanaged on", "ss\\l_unmanaged allow all", "include /data/app/*", "auth_param basic program /bin/sh -c bad", "url_rewrite_program /bin/sh", "pid_filename /data/app/a", "http_port 9000", "https_port 9443", "snmp_port 3401", "icp_port 3130", "htcp_port 4827", "snmp_access allow all", "icp_access allow all", "htcp_access allow all", "adaptation_access allow all", "icap_service foo", "reply_header_access X-Squid-Control-Revision deny all", "reply_header_replace Server server/1.0", "reply_header_remove X-Squid-Control-Revision", "request_header_add X-Probe test", "cache_dir ufs /data/app 50 16 256", "acl sc_safe_ports port 25", "http_access allow sc_probe_listener", "if true\nhttp_access allow all", "endif", "in\\clude /data/app/*", "url_rewrite_\\\nprogram /bin/sh", "request_header_add test value\\"} {
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
