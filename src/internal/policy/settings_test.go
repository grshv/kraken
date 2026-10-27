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
	for _, text := range []string{"include /data/app/*", "auth_param basic program /bin/sh -c bad", "url_rewrite_program /bin/sh", "pid_filename /data/app/a", "http_port 9000", "https_port 9443", "cache_dir ufs /data/app 50 16 256", "acl sc_safe_ports port 25", "http_access allow sc_probe_listener", "if true\nhttp_access allow all", "endif", "in\\clude /data/app/*", "url_rewrite_\\\nprogram /bin/sh", "request_header_add test value\\"} {
		s := fixture()
		s.Mode = "expert"
		s.Expert = text
		if e := Validate(s, Options{}); e == nil {
			t.Fatalf("accepted dangerous config: %q", text)
		}
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
	cases := map[string]func(*Settings){"reserved port": func(v *Settings) { v.HTTPPort = 3130 }, "SMTP": func(v *Settings) { v.SafePorts = []int{25, 443} }, "CONNECT mismatch": func(v *Settings) { v.ConnectPorts = []int{9999} }, "DNS injection": func(v *Settings) { v.DNSNameservers = []string{"1.1.1.1\nhttp_access allow all"} }, "timeout": func(v *Settings) { v.ReadTimeout = 0 }, "memory": func(v *Settings) { v.CacheMemoryMB = -1 }, "TTL": func(v *Settings) { v.NegativeDNSTTL = v.PositiveDNSTTL }}
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
