package policy

import (
	"fmt"
	"strings"
	"testing"
)

func blockedSnapshot(ds []Destination) Snapshot {
	s := fixture()
	v := DefaultSettings()
	v.BlockedDomains = ds
	s.Settings = &v
	return s
}

func TestBlockedDomainsValidation(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	// Valid: canonical domains, distinct, no overlap.
	ok := []Destination{{Domain: "example.org"}, {Domain: "repo.example.org"}, {Domain: "cdn.example.net", IncludeSubdomains: true}}
	if e := Validate(blockedSnapshot(ok), o); e != nil {
		t.Fatalf("rejected valid blocked domains: %v", e)
	}
	cases := map[string][]Destination{
		"IP literal":      {{Domain: "1.1.1.1"}},
		"duplicate":       {{Domain: "example.org"}, {Domain: "example.org"}},
		"overlap":         {{Domain: "example.org", IncludeSubdomains: true}, {Domain: "repo.example.org"}},
		"domain too long": {{Domain: strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 63) + "." + strings.Repeat("a", 63)}},
	}
	for name, ds := range cases {
		t.Run(name, func(t *testing.T) {
			if e := Validate(blockedSnapshot(ds), o); e == nil {
				t.Fatalf("accepted invalid blocked domains: %v", ds)
			} else if !strings.HasPrefix(e.Error(), "blocked domains: ") {
				t.Fatalf("error missing blocked domains prefix: %v", e)
			}
		})
	}
	// 1025 entries exceeds the cap.
	tooMany := make([]Destination, 1025)
	for i := range tooMany {
		tooMany[i] = Destination{Domain: fmt.Sprintf("d%d.example.org", i)}
	}
	if e := Validate(blockedSnapshot(tooMany), o); e == nil {
		t.Fatal("accepted 1025 blocked domains")
	}
}

func TestBlockedDomainsRender(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	s := blockedSnapshot([]Destination{{Domain: "example.org"}, {Domain: "cdn.example.net", IncludeSubdomains: true}})
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	// Sorted, subdomains get a leading dot.
	want := "acl sc_blocked_dst dstdomain -n .cdn.example.net example.org\nhttp_access deny sc_blocked_dst\n"
	if !strings.Contains(c, want) {
		t.Fatalf("missing blocked acl/deny\nwant: %q\ngot: %q", want, c)
	}
	protected := strings.Index(c, "http_access deny sc_protected_dst\n")
	private := strings.Index(c, "http_access deny sc_private_dst\n")
	deny := strings.Index(c, "http_access deny sc_blocked_dst\n")
	user := strings.Index(c, "# BEGIN user policy")
	if protected < 0 || private < 0 || deny < 0 || user < 0 {
		t.Fatalf("markers missing:\n%s", c)
	}
	if deny < private {
		t.Fatal("blocked deny must follow private deny")
	}
	if user < deny {
		t.Fatal("blocked deny must precede # BEGIN user policy")
	}
}

func TestBlockedDomainsRenderExpertMode(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	s := blockedSnapshot([]Destination{{Domain: "example.org"}})
	s.Mode = "expert"
	s.Expert = "acl office src 192.0.2.0/24\nhttp_access allow office\n"
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	deny := strings.Index(c, "http_access deny sc_blocked_dst\n")
	user := strings.Index(c, "# BEGIN user policy")
	office := strings.Index(c, "http_access allow office")
	if deny < 0 || user < 0 || office < 0 {
		t.Fatalf("markers missing:\n%s", c)
	}
	if deny > user || deny > office {
		t.Fatal("blocked deny must precede expert user policy")
	}
}

func TestHideClientAddressRender(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	// When true: both directives are emitted.
	s := fixture()
	v := DefaultSettings()
	v.HideClientAddress = true
	s.Settings = &v
	c, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(c, "forwarded_for delete\n") || !strings.Contains(c, "via off\n") {
		t.Fatalf("hide client address directives missing:\n%s", c)
	}
	// When false: neither directive is emitted.
	s2 := fixture()
	v2 := DefaultSettings()
	s2.Settings = &v2
	c2, e := Render(s2, o)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(c2, "forwarded_for delete") || strings.Contains(c2, "via off") {
		t.Fatalf("hide client address directives leaked when false:\n%s", c2)
	}
}

func TestEgressDefaultsByteForByteUnchanged(t *testing.T) {
	o := Options{StateDir: "/data", LogPath: "/data/log"}
	base, e := Render(fixture(), o)
	if e != nil {
		t.Fatal(e)
	}
	// Explicitly empty blocked list and false flag must not alter the output.
	s := fixture()
	v := DefaultSettings()
	v.BlockedDomains = []Destination{}
	v.HideClientAddress = false
	s.Settings = &v
	got, e := Render(s, o)
	if e != nil {
		t.Fatal(e)
	}
	if base != got {
		t.Fatalf("output changed with empty egress settings\nbase: %q\ngot:  %q", base, got)
	}
}
