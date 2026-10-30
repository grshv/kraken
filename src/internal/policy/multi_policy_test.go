package policy

import (
	"encoding/json"
	"strings"
	"testing"
)

func multiSnapshot() Snapshot {
	s := Empty()
	http := s.TLSProfiles[0].ID
	s.Policies = []Policy{
		{ID: "p-pypi", Name: "PyPI", Destinations: []Destination{{Domain: "pypi.org"}, {Domain: "files.pythonhosted.org"}}},
		{ID: "p-deb", Name: "Debian", Destinations: []Destination{{Domain: "debian.org", IncludeSubdomains: true}}},
	}
	s.Clients = []Client{
		{ID: "c-a", Name: "a", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: AccessList, TLSProfileID: http,
			PolicyIDs: []string{"p-pypi", "p-deb"}, Destinations: []Destination{{Domain: "example.com"}, {Domain: "deb.debian.org"}}},
		{ID: "c-web", Name: "web", Networks: []string{"10.0.0.2/32"}, Enabled: true, Access: AccessAllWeb, TLSProfileID: http, PolicyIDs: []string{"p-pypi"}, Destinations: []Destination{}},
		{ID: "c-none", Name: "none", Networks: []string{"10.0.0.3/32"}, Enabled: true, Access: AccessNone, TLSProfileID: http, PolicyIDs: []string{"p-pypi"}, Destinations: []Destination{{Domain: "example.net"}}},
	}
	return s
}

func TestEffectiveDestinationsUnionWithoutCoveredEntries(t *testing.T) {
	s := multiSnapshot()
	got := []string{}
	for _, d := range EffectiveDestinations(s, s.Clients[0]) {
		v := d.Domain
		if d.IncludeSubdomains {
			v = "*." + v
		}
		got = append(got, v)
	}
	// deb.debian.org is covered by *.debian.org from the Debian set.
	want := "*.debian.org example.com files.pythonhosted.org pypi.org"
	if strings.Join(got, " ") != want {
		t.Fatalf("effective: %v", got)
	}
	if !ClientAllows(s, s.Clients[0], "security.debian.org") || !ClientAllows(s, s.Clients[0], "pypi.org") || ClientAllows(s, s.Clients[0], "example.org") {
		t.Fatal("ClientAllows does not follow the union")
	}
	if !ClientAllows(s, s.Clients[1], "anything.example") || ClientAllows(s, s.Clients[2], "example.net") {
		t.Fatal("all_web / none access ignored")
	}
}

// Clients render in id order: c-a, c-none, c-web.
func TestRenderUnionAllWebAndNone(t *testing.T) {
	c, e := Render(multiSnapshot(), Options{StateDir: "/data", SquidDir: "/data/squid", LogPath: "/data/access.log"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, "acl destinations_0 dstdomain -n .debian.org example.com files.pythonhosted.org pypi.org\nhttp_access allow client_0 sc_http_listener destinations_0\n") {
		t.Fatalf("union ACL missing:\n%s", c)
	}
	if !strings.Contains(c, "http_access allow client_2 sc_http_listener\n") {
		t.Fatal("all_web client not allowed")
	}
	if strings.Contains(c, "allow client_1") || strings.Contains(c, "example.net") {
		t.Fatal("access none still renders an allow rule")
	}
}

func TestValidateClientAccessAndPolicies(t *testing.T) {
	o := Options{StateDir: "/data", SquidDir: "/data/squid", LogPath: "/data/access.log"}
	for name, mut := range map[string]func(*Snapshot){
		"unknown policy":   func(s *Snapshot) { s.Clients[0].PolicyIDs = []string{"p-missing"} },
		"duplicate policy": func(s *Snapshot) { s.Clients[0].PolicyIDs = []string{"p-pypi", "p-pypi"} },
		"bad access":       func(s *Snapshot) { s.Clients[0].Access = "maybe" },
		"missing profile":  func(s *Snapshot) { s.Clients[0].TLSProfileID = "nope" },
		"bad own domain":   func(s *Snapshot) { s.Clients[0].Destinations = []Destination{{Domain: "not a domain"}} },
	} {
		s := multiSnapshot()
		mut(&s)
		if Validate(s, o) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if e := Validate(multiSnapshot(), o); e != nil {
		t.Fatal(e)
	}
}

// A stored schema 1 snapshot decodes into the current schema with the same
// meaning: transport and "all web" move to the client, allowlists stay sets.
func TestUpgradeSchemaOne(t *testing.T) {
	raw := `{"schema_version":1,"clients":[
	 {"id":"c-a","name":"a","source_networks":["10.0.0.1/32"],"enabled":true,"policy_id":"p-list"},
	 {"id":"c-b","name":"b","source_networks":["10.0.0.2/32"],"enabled":true,"policy_id":"p-web"},
	 {"id":"c-c","name":"c","source_networks":["10.0.0.3/32"],"enabled":true,"policy_id":"p-empty"}],
	 "policies":[
	 {"id":"p-list","name":"list","mode":"allowlist","destinations":[{"domain":"pypi.org","include_subdomains":false}],"tls_profile_id":"t-tls"},
	 {"id":"p-web","name":"web","mode":"all_web","destinations":[],"tls_profile_id":"t-http"},
	 {"id":"p-empty","name":"empty","mode":"allowlist","destinations":[],"tls_profile_id":"t-http"}],
	 "tls_profiles":[{"id":"t-http","name":"h","mode":"tunnel","proxy_transport":"http","cert_ref":null},
	 {"id":"t-tls","name":"t","mode":"tunnel","proxy_transport":"tls","cert_ref":"proxy-server"}]}`
	var s Snapshot
	if e := json.Unmarshal([]byte(raw), &s); e != nil {
		t.Fatal(e)
	}
	if s.Version != SchemaVersion {
		t.Fatalf("version %d", s.Version)
	}
	a, b, c := s.Clients[0], s.Clients[1], s.Clients[2]
	if a.Access != AccessList || a.TLSProfileID != "t-tls" || len(a.PolicyIDs) != 1 || a.PolicyIDs[0] != "p-list" || a.LegacyPolicyID != "" {
		t.Fatalf("allowlist client: %+v", a)
	}
	if b.Access != AccessAllWeb || b.TLSProfileID != "t-http" || len(b.PolicyIDs) != 0 {
		t.Fatalf("all web client: %+v", b)
	}
	if c.Access != AccessNone {
		t.Fatalf("empty allowlist client: %+v", c)
	}
	if len(s.Policies) != 2 {
		t.Fatalf("all web policy kept: %+v", s.Policies)
	}
	for _, p := range s.Policies {
		if p.LegacyMode != "" || p.LegacyTLSProfileID != "" {
			t.Fatalf("legacy policy fields kept: %+v", p)
		}
	}
	out, _ := json.Marshal(s)
	if strings.Contains(string(out), `"policy_id"`) || strings.Contains(string(out), `"mode":"allowlist"`) {
		t.Fatalf("legacy fields re-encoded: %s", out)
	}
}
