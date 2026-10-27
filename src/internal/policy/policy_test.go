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
	for _, want := range []string{"http_access deny sc_private_dst", "dstdomain -n example.org", "http_access deny all", "reply_header_add X-Squid-Control-Revision " + Fingerprint(s, o)} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Index(c, "http_access allow") < strings.Index(c, "http_access deny sc_private_dst") {
		t.Fatal("allow precedes protection")
	}
	s.Policies[0].Destinations = nil
	c, e = Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(c, "http_access allow") {
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
