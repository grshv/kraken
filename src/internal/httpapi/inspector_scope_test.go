package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
)

// inspectorScopeClock is the fixed time the scope fixture drives windows off.
var inspectorScopeClock = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// inspectorScopeSnapshot is the active revision for scope tests: c-a and c-a2
// are the manager's clients; c-b is foreign (outside his network); c-d is
// foreign but sits inside his network (10.1.0.0/16) so "in network but not
// assigned" is covered. Policies cover own/foreign/shared/orphan visibility.
func inspectorScopeSnapshot() policy.Snapshot {
	st := policy.DefaultSettings()
	http := "00000000-0000-4000-8000-000000000001"
	tlsRef := "proxy-server"
	tls := "00000000-0000-4000-8000-000000000002"
	return policy.Snapshot{
		Version:  1,
		Mode:     "structured",
		Settings: &st,
		TLSProfiles: []policy.TLSProfile{
			{ID: http, Name: "HTTP proxy / CONNECT", Mode: "tunnel", Transport: "http"},
			{ID: tls, Name: "TLS proxy / CONNECT", Mode: "tunnel", Transport: "tls", CertRef: &tlsRef},
		},
		Policies: []policy.Policy{
			{ID: "p-own", Name: "own", Mode: "allowlist", TLSProfileID: tls, Destinations: []policy.Destination{{Domain: "own.example"}}},
			{ID: "p-shared", Name: "shared", Mode: "allowlist", TLSProfileID: tls, Destinations: []policy.Destination{{Domain: "shared.example"}}},
			{ID: "p-foreign", Name: "foreign", Mode: "allowlist", TLSProfileID: tls, Destinations: []policy.Destination{{Domain: "foreign.example"}}},
			{ID: "p-orphan", Name: "orphan", Mode: "allowlist", TLSProfileID: tls, Destinations: []policy.Destination{{Domain: "orphan.example"}}},
		},
		Clients: []policy.Client{
			{ID: "c-a", Name: "alpha", Kind: "host", Networks: []string{"10.1.0.5/32"}, Enabled: true, PolicyID: "p-own"},
			{ID: "c-a2", Name: "alpha2", Kind: "host", Networks: []string{"10.1.0.6/32"}, Enabled: true, PolicyID: "p-shared"},
			{ID: "c-b", Name: "bravo", Kind: "host", Networks: []string{"10.2.0.5/32"}, Enabled: true, PolicyID: "p-shared"},
			{ID: "c-d", Name: "delta", Kind: "host", Networks: []string{"10.1.0.9/32"}, Enabled: true, PolicyID: "p-foreign"},
		},
	}
}

// inspectorScopeFixture applies the snapshot, pins the clock, seeds events and
// creates two managers: "mgr" owns 10.1.0.0/16 and clients c-a, c-a2; "empty"
// owns nothing. Returns admin (with CSRF) and the mgr cookie.
func inspectorScopeFixture(t *testing.T) (*Server, http.Handler, *http.Cookie, string, *http.Cookie) {
	t.Helper()
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	s.Runtime.Options.ProxyCert = filepath.Join(t.TempDir(), "proxy.crt")
	s.Runtime.Options.ProxyKey = filepath.Join(t.TempDir(), "proxy.key")
	applySnapshot(t, h, s, admin, csrf, "scope-base", inspectorScopeSnapshot())
	s.now = func() time.Time { return inspectorScopeClock }
	now := inspectorScopeClock
	mk := func(id, clientID, name, src, dst string, ago time.Duration) events.Event {
		e := events.Event{ID: id, Timestamp: ts(now, ago), SourceIP: src, Destination: dst, Method: "CONNECT", ProxyResult: "TCP_TUNNEL", ProxyHTTPStatus: ptrInt(200), Bytes: 100, TLSMode: "tunnel"}
		if clientID != "" {
			c, n := clientID, name
			e.ClientID, e.ClientName, e.ClientMatchBasis = &c, &n, "source_ip"
		}
		return e
	}
	rows := []events.Event{
		mk("ev-a-own", "c-a", "alpha", "10.1.0.5", "own.example:443", 10*time.Minute),
		mk("ev-a2-shared", "c-a2", "alpha2", "10.1.0.6", "shared.example:443", 20*time.Minute),
		mk("ev-b-shared", "c-b", "bravo", "10.2.0.5", "shared.example:443", 30*time.Minute),
		mk("ev-d-foreign", "c-d", "delta", "10.1.0.9", "foreign.example:443", 40*time.Minute),
		// Unknown sources: one inside the manager's network, one outside.
		{ID: "ev-uk-in", Timestamp: ts(now, 10*time.Minute), SourceIP: "10.1.0.99", Destination: "unknown.example:443", Method: "CONNECT", ProxyResult: "TCP_DENIED", ProxyHTTPStatus: ptrInt(403), Bytes: 0, TLSMode: "tunnel"},
		{ID: "ev-uk-out", Timestamp: ts(now, 15*time.Minute), SourceIP: "10.9.9.9", Destination: "unknown.example:443", Method: "CONNECT", ProxyResult: "TCP_DENIED", ProxyHTTPStatus: ptrInt(403), Bytes: 0, TLSMode: "tunnel"},
	}
	if e := s.Store.SaveEvents(rows, events.Checkpoint{SourceAvailable: true}); e != nil {
		t.Fatal(e)
	}
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	createManagerUser(t, h, admin, csrf, "empty", []string{}, []string{})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	return s, h, admin, csrf, mgr
}

// TestInspectorScopeClientStats covers admin sees all and manager scope rules
// for /inspector/clients/{id}/stats: own client, foreign, in-network not
// assigned, missing, and admin parity.
func TestInspectorScopeClientStats(t *testing.T) {
	_, h, admin, _, mgr := inspectorScopeFixture(t)
	var v struct {
		Requests int `json:"requests"`
	}
	// Admin sees every client.
	if code := getJSON(t, h, admin, "/inspector/clients/c-a/stats", &v); code != 200 || v.Requests != 1 {
		t.Fatalf("admin c-a: %d %+v", code, v)
	}
	if code := getJSON(t, h, admin, "/inspector/clients/c-b/stats", &v); code != 200 || v.Requests != 1 {
		t.Fatalf("admin c-b: %d %+v", code, v)
	}
	if code := getJSON(t, h, admin, "/inspector/clients/c-d/stats", &v); code != 200 || v.Requests != 1 {
		t.Fatalf("admin c-d: %d %+v", code, v)
	}
	// Manager: own client -> 200.
	if code := getJSON(t, h, mgr, "/inspector/clients/c-a/stats", &v); code != 200 || v.Requests != 1 {
		t.Fatalf("manager c-a: %d %+v", code, v)
	}
	// Foreign client -> 404.
	if code := getJSON(t, h, mgr, "/inspector/clients/c-b/stats", &v); code != 404 {
		t.Fatalf("manager c-b (foreign): %d", code)
	}
	// Client in manager's network but not assigned -> 404.
	if code := getJSON(t, h, mgr, "/inspector/clients/c-d/stats", &v); code != 404 {
		t.Fatalf("manager c-d (in network, not assigned): %d", code)
	}
	// Missing client -> 404.
	if code := getJSON(t, h, mgr, "/inspector/clients/missing/stats", &v); code != 404 {
		t.Fatalf("manager missing: %d", code)
	}
}

// TestInspectorScopePolicyHits covers policy visibility and per-client hit
// filtering for managers: own, shared (his client + foreign), foreign-only
// (404), orphan (visible, 0 hits), missing (404).
func TestInspectorScopePolicyHits(t *testing.T) {
	_, h, admin, _, mgr := inspectorScopeFixture(t)
	var v struct {
		Domains []struct {
			Domain string `json:"domain"`
			H24    int    `json:"hits_24h"`
		} `json:"domains"`
	}
	h24 := func(domain string) int {
		for _, d := range v.Domains {
			if d.Domain == domain {
				return d.H24
			}
		}
		return -1
	}
	// Admin: p-shared has hits from both c-a2 and c-b (2 in 24h).
	getJSON(t, h, admin, "/inspector/policies/p-shared/hits", &v)
	if h24("shared.example") != 2 {
		t.Fatalf("admin p-shared hits: %+v", v.Domains)
	}
	// Manager: p-shared visible (c-a2 references it); hits only from c-a2 (1).
	getJSON(t, h, mgr, "/inspector/policies/p-shared/hits", &v)
	if h24("shared.example") != 1 {
		t.Fatalf("manager p-shared hits: %+v", v.Domains)
	}
	// Manager: p-own visible (c-a references), 1 hit.
	getJSON(t, h, mgr, "/inspector/policies/p-own/hits", &v)
	if h24("own.example") != 1 {
		t.Fatalf("manager p-own hits: %+v", v.Domains)
	}
	// Manager: p-foreign referenced only by c-d (foreign) -> 404.
	if code := getJSON(t, h, mgr, "/inspector/policies/p-foreign/hits", &v); code != 404 {
		t.Fatalf("manager p-foreign: %d", code)
	}
	// Manager: p-orphan has no clients -> visible, 0 hits.
	getJSON(t, h, mgr, "/inspector/policies/p-orphan/hits", &v)
	if h24("orphan.example") != 0 {
		t.Fatalf("manager p-orphan hits: %+v", v.Domains)
	}
	// Missing policy -> 404.
	if code := getJSON(t, h, mgr, "/inspector/policies/missing/hits", &v); code != 404 {
		t.Fatalf("manager missing policy: %d", code)
	}
}

// TestInspectorScopeUnknownSources covers that admin sees all unknown sources
// and a manager sees only those inside his networks.
func TestInspectorScopeUnknownSources(t *testing.T) {
	_, h, admin, _, mgr := inspectorScopeFixture(t)
	var v struct {
		Sources []struct {
			IP string `json:"source_ip"`
		} `json:"sources"`
	}
	// Admin sees both unknown sources.
	getJSON(t, h, admin, "/inspector/unknown-sources", &v)
	ips := map[string]bool{}
	for _, s := range v.Sources {
		ips[s.IP] = true
	}
	if !ips["10.1.0.99"] || !ips["10.9.9.9"] {
		t.Fatalf("admin unknown sources: %+v", ips)
	}
	// Manager sees only the source inside his networks.
	getJSON(t, h, mgr, "/inspector/unknown-sources", &v)
	ips = map[string]bool{}
	for _, s := range v.Sources {
		ips[s.IP] = true
	}
	if !ips["10.1.0.99"] || ips["10.9.9.9"] {
		t.Fatalf("manager unknown sources: %+v", ips)
	}
}

// TestInspectorScopeExplain covers event-explain scope rules: own client,
// foreign client, in-network not assigned, unknown in/out of networks, missing.
func TestInspectorScopeExplain(t *testing.T) {
	_, h, admin, _, mgr := inspectorScopeFixture(t)
	var v struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	// Admin explains every event.
	for _, id := range []string{"ev-a-own", "ev-b-shared", "ev-d-foreign", "ev-uk-in", "ev-uk-out"} {
		if code := getJSON(t, h, admin, "/inspector/events/"+id+"/explain", &v); code != 200 {
			t.Fatalf("admin %s: %d", id, code)
		}
	}
	// Manager: own client event -> 200, decision/reason present.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-a-own/explain", &v); code != 200 || v.Decision == "" || v.Reason == "" {
		t.Fatalf("manager ev-a-own: %d %+v", code, v)
	}
	// Manager: foreign client event -> 404.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-b-shared/explain", &v); code != 404 {
		t.Fatalf("manager ev-b-shared: %d", code)
	}
	// Manager: in-network but not-assigned client -> 404.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-d-foreign/explain", &v); code != 404 {
		t.Fatalf("manager ev-d-foreign: %d", code)
	}
	// Manager: unknown source in his network -> 200.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-uk-in/explain", &v); code != 200 {
		t.Fatalf("manager ev-uk-in: %d", code)
	}
	// Manager: unknown source outside his networks -> 404.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-uk-out/explain", &v); code != 404 {
		t.Fatalf("manager ev-uk-out: %d", code)
	}
	// Missing event -> 404.
	if code := getJSON(t, h, mgr, "/inspector/events/nope/explain", &v); code != 404 {
		t.Fatalf("manager missing event: %d", code)
	}
}

// TestInspectorScopeExplainNoForeignLeak verifies a manager explain response
// never carries a foreign client_id or an invisible policy_id.
func TestInspectorScopeExplainNoForeignLeak(t *testing.T) {
	_, h, _, _, mgr := inspectorScopeFixture(t)
	// ev-uk-in is an unknown source inside the manager's network; explain must
	// not surface foreign client/policy ids from the full active snapshot.
	var v struct {
		ClientID *string `json:"client_id"`
		PolicyID *string `json:"policy_id"`
	}
	if code := getJSON(t, h, mgr, "/inspector/events/ev-uk-in/explain", &v); code != 200 {
		t.Fatalf("manager ev-uk-in: %d", code)
	}
	for _, forbidden := range []string{"c-b", "c-d", "p-foreign"} {
		if v.ClientID != nil && *v.ClientID == forbidden {
			t.Fatalf("explain leaked client_id %q", *v.ClientID)
		}
		if v.PolicyID != nil && *v.PolicyID == forbidden {
			t.Fatalf("explain leaked policy_id %q", *v.PolicyID)
		}
	}
	// ev-a-own is the manager's own client; its client_id and policy_id are his.
	if code := getJSON(t, h, mgr, "/inspector/events/ev-a-own/explain", &v); code != 200 {
		t.Fatalf("manager ev-a-own: %d", code)
	}
	if v.ClientID == nil || *v.ClientID != "c-a" || v.PolicyID == nil || *v.PolicyID != "p-own" {
		t.Fatalf("manager own-client explain ids: %+v", v)
	}
}

// TestInspectorScopeDraftImpact covers that a manager's draft-impact includes
// only his clients; foreign client ids never appear.
func TestInspectorScopeDraftImpact(t *testing.T) {
	_, h, admin, adminCSRF, mgr := inspectorScopeFixture(t)
	// Baseline: draft == active, no divergence.
	var v struct {
		NewlyAllowed int `json:"newly_allowed"`
		NewlyDenied  int `json:"newly_denied"`
		Clients      []struct {
			ClientID     string `json:"client_id"`
			NewlyAllowed int    `json:"newly_allowed"`
			NewlyDenied  int    `json:"newly_denied"`
		} `json:"clients"`
	}
	if code := getJSON(t, h, mgr, "/inspector/draft-impact", &v); code != 200 || v.NewlyAllowed != 0 || v.NewlyDenied != 0 {
		t.Fatalf("manager baseline impact: %+v", v)
	}
	// Admin edits p-own to drop own.example: c-a's ev-a-own flips
	// allowed (active) -> denied (draft). c-a2 is unaffected (p-shared unchanged).
	d := getDraft(t, h, admin)
	for i := range d.Snapshot.Policies {
		if d.Snapshot.Policies[i].ID == "p-own" {
			d.Snapshot.Policies[i].Destinations = []policy.Destination{{Domain: "other.example"}}
		}
	}
	if w := request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, admin, adminCSRF, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Admin sees the flip too.
	getJSON(t, h, admin, "/inspector/draft-impact", &v)
	if v.NewlyDenied != 1 {
		t.Fatalf("admin impact after edit: %+v", v)
	}
	// Manager sees only c-a; no foreign client ids.
	getJSON(t, h, mgr, "/inspector/draft-impact", &v)
	if v.NewlyDenied != 1 || v.NewlyAllowed != 0 {
		t.Fatalf("manager impact after edit: %+v", v)
	}
	ids := map[string]bool{}
	for _, c := range v.Clients {
		ids[c.ClientID] = true
	}
	if len(ids) != 1 || !ids["c-a"] {
		t.Fatalf("manager impact clients: %+v", ids)
	}
	for _, forbidden := range []string{"c-b", "c-d"} {
		if ids[forbidden] {
			t.Fatalf("manager impact leaked %s", forbidden)
		}
	}
}

// TestInspectorScopeEmptyManager covers a manager with an empty scope: he sees
// no events, no clients and no unknown sources; orphan policies are visible
// with zero hits.
func TestInspectorScopeEmptyManager(t *testing.T) {
	_, h, _, _, _ := inspectorScopeFixture(t)
	empty, _ := loginAs(t, h, "empty", "manager-password-123")
	// Client stats -> 404 (no clients in scope, even existing ones).
	var stats struct {
		Requests int `json:"requests"`
	}
	if code := getJSON(t, h, empty, "/inspector/clients/c-a/stats", &stats); code != 404 {
		t.Fatalf("empty manager c-a: %d", code)
	}
	// Unknown sources -> empty list.
	var uk struct {
		Sources []struct {
			IP string `json:"source_ip"`
		} `json:"sources"`
	}
	if code := getJSON(t, h, empty, "/inspector/unknown-sources", &uk); code != 200 || len(uk.Sources) != 0 {
		t.Fatalf("empty manager unknown-sources: %d %+v", code, uk)
	}
	// Explain -> 404.
	var ex struct {
		Decision string `json:"decision"`
	}
	if code := getJSON(t, h, empty, "/inspector/events/ev-a-own/explain", &ex); code != 404 {
		t.Fatalf("empty manager explain: %d", code)
	}
	// Policy referenced by any client -> 404.
	var ph struct {
		Domains []struct {
			Domain string `json:"domain"`
		} `json:"domains"`
	}
	if code := getJSON(t, h, empty, "/inspector/policies/p-own/hits", &ph); code != 404 {
		t.Fatalf("empty manager p-own: %d", code)
	}
	// Orphan policy (no clients) -> visible, 0 hits.
	var pho struct {
		Domains []struct {
			Domain string `json:"domain"`
			H24    int    `json:"hits_24h"`
		} `json:"domains"`
	}
	if code := getJSON(t, h, empty, "/inspector/policies/p-orphan/hits", &pho); code != 200 {
		t.Fatalf("empty manager p-orphan: %d", code)
	}
	for _, d := range pho.Domains {
		if d.H24 != 0 {
			t.Fatalf("empty manager p-orphan hits nonzero: %+v", d)
		}
	}
	// Draft-impact -> empty result.
	var di struct {
		NewlyAllowed int `json:"newly_allowed"`
		NewlyDenied  int `json:"newly_denied"`
		Clients      []struct {
			ClientID string `json:"client_id"`
		} `json:"clients"`
	}
	if code := getJSON(t, h, empty, "/inspector/draft-impact", &di); code != 200 || di.NewlyAllowed != 0 || di.NewlyDenied != 0 || len(di.Clients) != 0 {
		t.Fatalf("empty manager draft-impact: %+v", di)
	}
	// Domain templates and status remain visible to any authenticated role.
	var tpl struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
	}
	if code := getJSON(t, h, empty, "/inspector/domain-templates", &tpl); code != 200 || len(tpl.Templates) == 0 {
		t.Fatalf("empty manager domain-templates: %d", code)
	}
}
