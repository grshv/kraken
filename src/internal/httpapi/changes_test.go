package httpapi

import (
	"encoding/json"
	"strconv"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// changesEqual compares change lists by value; pointers compare by pointee.
func changesEqual(got, want []Change) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Entity != w.Entity || g.Action != w.Action || g.Text != w.Text {
			return false
		}
		if (g.ID == nil) != (w.ID == nil) {
			return false
		}
		if g.ID != nil && *g.ID != *w.ID {
			return false
		}
	}
	return true
}

func baseSnapshot() policy.Snapshot {
	s := policy.Empty()
	s.Clients = []policy.Client{}
	s.Policies = []policy.Policy{}
	return s
}

func TestSemanticChangesEmpty(t *testing.T) {
	s := policy.Empty()
	if got := semanticChanges(s, s); !changesEqual(got, []Change{}) {
		t.Fatalf("expected no changes, got %+v", got)
	}
	// An empty result marshals as [], not null.
	raw, _ := json.Marshal(semanticChanges(s, s))
	if string(raw) != "[]" {
		t.Fatalf("empty changes must marshal as [], got %s", raw)
	}
}

func TestSemanticChangesClients(t *testing.T) {
	a := baseSnapshot()
	tls := a.TLSProfiles[0].ID
	a.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
		{ID: "c-b", Name: "bravo", Networks: []string{"10.0.0.2/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
	}
	b := baseSnapshot()
	b.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Description: "changed", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
		{ID: "c-c", Name: "charlie", Networks: []string{"10.0.0.3/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
	}
	got := semanticChanges(a, b)
	want := []Change{
		{Entity: "client", ID: strPtr("c-a"), Action: "mod", Text: "Изменён клиент: alpha"},
		{Entity: "client", ID: strPtr("c-c"), Action: "add", Text: "Добавлен клиент: charlie"},
		{Entity: "client", ID: strPtr("c-b"), Action: "del", Text: "Удалён клиент: bravo"},
	}
	if !changesEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSemanticChangesPolicies(t *testing.T) {
	a := baseSnapshot()
	a.Policies = []policy.Policy{
		{ID: "p-a", Name: "alpha", Destinations: []policy.Destination{}},
		{ID: "p-b", Name: "bravo", Destinations: []policy.Destination{}},
	}
	b := baseSnapshot()
	b.Policies = []policy.Policy{
		{ID: "p-a", Name: "alpha", Destinations: []policy.Destination{{Domain: "x.test"}}},
		{ID: "p-c", Name: "charlie", Destinations: []policy.Destination{}},
	}
	got := semanticChanges(a, b)
	want := []Change{
		{Entity: "policy", ID: strPtr("p-a"), Action: "mod", Text: "Изменена политика: alpha"},
		{Entity: "policy", ID: strPtr("p-c"), Action: "add", Text: "Добавлена политика: charlie"},
		{Entity: "policy", ID: strPtr("p-b"), Action: "del", Text: "Удалена политика: bravo"},
	}
	if !changesEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSemanticChangesSettingsConfigurationTLS(t *testing.T) {
	a := baseSnapshot()
	b := baseSnapshot()
	b.Settings.ConnectTimeout = 45
	b.Mode = "expert"
	b.TLSProfiles = append(b.TLSProfiles, policy.TLSProfile{ID: "t-tls", Name: "TLS proxy / CONNECT", Mode: "tunnel", Transport: "tls", CertRef: strPtr("proxy-server")})
	got := semanticChanges(a, b)
	want := []Change{
		{Entity: "settings", ID: nil, Action: "mod", Text: "Изменены настройки Squid"},
		{Entity: "configuration", ID: nil, Action: "mod", Text: "Изменён режим конфигурации или экспертный squid.conf"},
		{Entity: "tls_profiles", ID: nil, Action: "mod", Text: "Изменены TLS-профили"},
	}
	if !changesEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSemanticChangesOrderAndLimit(t *testing.T) {
	a := baseSnapshot()
	tls := a.TLSProfiles[0].ID
	a.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
		{ID: "c-b", Name: "bravo", Networks: []string{"10.0.0.2/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
	}
	a.Policies = []policy.Policy{
		{ID: "p-a", Name: "alpha", Destinations: []policy.Destination{}},
		{ID: "p-b", Name: "bravo", Destinations: []policy.Destination{}},
	}
	b := baseSnapshot()
	b.Clients = []policy.Client{
		{ID: "c-a", Name: "alpha", Description: "changed", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
		{ID: "c-c", Name: "charlie", Networks: []string{"10.0.0.3/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: tls, PolicyIDs: []string{}, Destinations: []policy.Destination{}},
	}
	b.Policies = []policy.Policy{
		{ID: "p-a", Name: "alpha", Destinations: []policy.Destination{{Domain: "x.test"}}},
		{ID: "p-c", Name: "charlie", Destinations: []policy.Destination{}},
	}
	b.Settings.ConnectTimeout = 45
	b.Mode = "expert"
	b.TLSProfiles = append(b.TLSProfiles, policy.TLSProfile{ID: "t-tls", Name: "TLS proxy / CONNECT", Mode: "tunnel", Transport: "tls", CertRef: strPtr("proxy-server")})
	got := semanticChanges(a, b)
	want := []Change{
		{Entity: "client", ID: strPtr("c-a"), Action: "mod", Text: "Изменён клиент: alpha"},
		{Entity: "client", ID: strPtr("c-c"), Action: "add", Text: "Добавлен клиент: charlie"},
		{Entity: "client", ID: strPtr("c-b"), Action: "del", Text: "Удалён клиент: bravo"},
		{Entity: "policy", ID: strPtr("p-a"), Action: "mod", Text: "Изменена политика: alpha"},
		{Entity: "policy", ID: strPtr("p-c"), Action: "add", Text: "Добавлена политика: charlie"},
		{Entity: "policy", ID: strPtr("p-b"), Action: "del", Text: "Удалена политика: bravo"},
		{Entity: "settings", ID: nil, Action: "mod", Text: "Изменены настройки Squid"},
		{Entity: "configuration", ID: nil, Action: "mod", Text: "Изменён режим конфигурации или экспертный squid.conf"},
		{Entity: "tls_profiles", ID: nil, Action: "mod", Text: "Изменены TLS-профили"},
	}
	if !changesEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Limit: more than 100 client additions are truncated to 100.
	big := baseSnapshot()
	draft := baseSnapshot()
	big.Clients = []policy.Client{}
	draft.Clients = make([]policy.Client, 105)
	for i := range draft.Clients {
		draft.Clients[i] = policy.Client{ID: "c-" + strconv.Itoa(i), Name: "n", Networks: []string{"10.0.0.1/32"}, Enabled: true, Access: policy.AccessList, TLSProfileID: big.TLSProfiles[0].ID, PolicyIDs: []string{}, Destinations: []policy.Destination{}}
	}
	truncated := semanticChanges(big, draft)
	if len(truncated) != 100 {
		t.Fatalf("expected 100 truncated changes, got %d", len(truncated))
	}
	// semanticDiff adds the overflow marker beyond 100 lines.
	diff := semanticDiff(big, draft)
	if len(diff) != 101 || diff[100] != "… остальные изменения показаны в diff" {
		t.Fatalf("expected 100 changes + marker, got %d: %q", len(diff), diff)
	}
}
