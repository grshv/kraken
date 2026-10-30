package policy

import (
	"strings"
	"testing"
)

func fixture() Snapshot {
	s := Empty()
	s.Policies = []Policy{{ID: "p", Name: "Packages", Mode: "allowlist", Destinations: []Destination{{Domain: "example.org"}}, TLSProfileID: s.TLSProfiles[0].ID}}
	s.Clients = []Client{{ID: "c", Name: "Build", Networks: []string{"192.0.2.1/32"}, Enabled: true, PolicyID: "p"}}
	return s
}
func TestPolicyBoundaries(t *testing.T) {
	cases := map[string]func(*Snapshot){"CRLF injection": func(s *Snapshot) { s.Policies[0].Destinations[0].Domain = "example.org\nhttp_access allow all" }, "raw IP": func(s *Snapshot) { s.Policies[0].Destinations[0].Domain = "1.1.1.1" }, "overlap": func(s *Snapshot) {
		s.Clients = append(s.Clients, Client{ID: "d", Name: "Other", Networks: []string{"192.0.2.0/24"}, Enabled: true, PolicyID: "p"})
	}, "IPv6 overlap": func(s *Snapshot) { s.Clients[0].Networks = []string{"2001:db8::1/128", "2001:db8::/64"} }, "default route": func(s *Snapshot) { s.Clients[0].Networks = []string{"0.0.0.0/0"} }, "mapped IPv6": func(s *Snapshot) { s.Clients[0].Networks = []string{"::ffff:192.0.2.1/128"} }, "unknown profile": func(s *Snapshot) { s.Policies[0].TLSProfileID = "missing" }, "invalid TLS mode": func(s *Snapshot) { s.TLSProfiles[0].Mode = "invalid-mode" }, "overlapping domains": func(s *Snapshot) {
		s.Policies[0].Destinations = []Destination{{Domain: "example.org", IncludeSubdomains: true}, {Domain: "repo.example.org"}}
	}}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := fixture()
			mutate(&s)
			if e := Validate(s, Options{}); e == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
}
func TestRenderDenyByDefault(t *testing.T) {
	s := fixture()
	o := Options{StateDir: "/data/runtime", LogPath: "/data/logs/access.log"}
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"http_access deny sc_private_dst", "dstdomain -n example.org", "http_access deny sc_r_no_rule all\n", "reply_header_add X-Kraken-Revision " + Fingerprint(s, o)} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %s", want)
		}
	}
	userConfig := strings.ReplaceAll(c, "http_access allow sc_probe_source sc_manager_listener sc_manager_method sc_manager_path manager\n", "")
	userConfig = strings.ReplaceAll(userConfig, "http_access allow sc_probe_source sc_manager_listener sc_manager_control manager\n", "")
	if strings.Index(userConfig, "http_access allow") < strings.Index(c, "http_access deny sc_private_dst") {
		t.Fatal("allow precedes protection")
	}
	s.Policies[0].Destinations = nil
	c, e = Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(c, "http_access allow sc_probe_source sc_manager_listener sc_manager_method sc_manager_path manager\n", ""), "http_access allow sc_probe_source sc_manager_listener sc_manager_control manager\n", ""), "http_access allow") {
		t.Fatal("empty allowlist granted access")
	}
}
func TestTLSDowngradeGuard(t *testing.T) {
	s := fixture()
	s.TLSProfiles[0].Transport = "tls"
	cert := "proxy-server"
	s.TLSProfiles[0].CertRef = &cert
	o := Options{StateDir: "/data", LogPath: "/data/access.log", ProxyCert: "/certs/proxy.crt", ProxyKey: "/certs/proxy.key"}
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, "allow client_0 sc_tls_listener destinations_0") {
		t.Fatal("TLS policy permits plaintext")
	}
	o.ProxyKey = ""
	if _, e = Render(s, o); e == nil {
		t.Fatal("TLS accepted without key")
	}
}

// Every deny reason is an annotate_transaction ACL that ends (or, for the
// final rule, starts before "all") exactly one http_access deny line, and the
// access log uses the kraken format that records it.
func TestDenyReasonsAnnotateRules(t *testing.T) {
	s := Empty()
	st := EffectiveSettings(s)
	st.BlockedDomains = []Destination{{Domain: "x.example"}}
	s.Settings = &st
	s.Clients = []Client{{ID: "c", Name: "c", Networks: []string{"10.0.0.0/8"}, Enabled: true, PolicyID: "p", ProxyUsername: "u"}}
	cert := "proxy-server"
	s.TLSProfiles = append(s.TLSProfiles, TLSProfile{ID: "tls", Name: "tls", Mode: s.TLSProfiles[0].Mode, Transport: "tls", CertRef: &cert})
	s.Policies = []Policy{{ID: "p", Name: "p", Mode: "all_web", TLSProfileID: "tls"}}
	c, e := Render(s, Options{StateDir: "/data", SquidDir: "/data/squid", LogPath: "/data/access.log", ProxyAccounts: "/run/a.json", ProxyCert: "/certs/p.crt", ProxyKey: "/certs/p.key"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, "access_log stdio:/data/access.log kraken !sc_probe_source\n") || !strings.Contains(c, "%{sc_deny}note\n") {
		t.Fatal("access log does not use the kraken format")
	}
	for _, r := range DenyReasons {
		def := "acl sc_r_" + r + " annotate_transaction sc_deny=" + r + "\n"
		if !strings.Contains(c, def) {
			t.Fatalf("missing %q", def)
		}
		uses := 0
		for _, line := range strings.Split(c, "\n") {
			if strings.HasPrefix(line, "http_access deny ") && strings.Contains(line+" ", " sc_r_"+r+" ") {
				uses++
				if r == "no_rule" && line != "http_access deny sc_r_no_rule all" {
					t.Fatalf("final rule: %q", line)
				}
				if r != "no_rule" && !strings.HasSuffix(line, " sc_r_"+r) {
					t.Fatalf("annotation must be the last ACL: %q", line)
				}
			}
		}
		if uses != 1 || strings.Index(c, def) > strings.Index(c, " sc_r_"+r) {
			t.Fatalf("%s: used %d times or before definition", r, uses)
		}
	}
}
