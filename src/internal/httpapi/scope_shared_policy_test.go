package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// seedSharedPolicyScenario sets up a draft with:
//
//	c-a  (manager's, 10.1.1.0/24, p-own)
//	c-a2 (manager's, 10.1.2.0/24, p-shared)
//	c-b  (foreign,   10.2.0.0/16,  p-shared)
//	c-d  (foreign,   10.1.0.9/32,  p-foreign)
//
// Manager network: 10.1.0.0/16. Manager is assigned c-a and c-a2.
func seedSharedPolicyScenario(t *testing.T, h http.Handler, admin *http.Cookie, csrf string) {
	t.Helper()
	w := request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	profile := d.Snapshot.TLSProfiles[0].ID
	d.Snapshot.Policies = append(d.Snapshot.Policies,
		policy.Policy{ID: "p-own", Name: "own", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, LegacyTLSProfileID: profile},
		policy.Policy{ID: "p-shared", Name: "shared", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "shared.test"}}, LegacyTLSProfileID: profile},
		policy.Policy{ID: "p-foreign", Name: "foreign", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "foreign.test"}}, LegacyTLSProfileID: profile},
	)
	d.Snapshot.Clients = append(d.Snapshot.Clients,
		policy.Client{ID: "c-a", Name: "mine-a", Networks: []string{"10.1.1.0/24"}, Enabled: true, LegacyPolicyID: "p-own"},
		policy.Client{ID: "c-a2", Name: "mine-a2", Networks: []string{"10.1.2.0/24"}, Enabled: true, LegacyPolicyID: "p-shared"},
		policy.Client{ID: "c-b", Name: "foreign-b", Networks: []string{"10.2.0.0/16"}, Enabled: true, LegacyPolicyID: "p-shared"},
		policy.Client{ID: "c-d", Name: "foreign-d", Networks: []string{"10.1.0.9/32"}, Enabled: true, LegacyPolicyID: "p-foreign"},
	)
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

// TestManagerSharedPolicyUnchangedPutSucceeds verifies a manager with a client
// on a shared policy can save an unchanged view (with a trivial client edit)
// without getting a false 403 policy_shared_out_of_scope.
func TestManagerSharedPolicyUnchangedPutSucceeds(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedSharedPolicyScenario(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// GET the manager's view.
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	if e := json.Unmarshal(w.Body.Bytes(), &view); e != nil {
		t.Fatal(e)
	}
	// Verify the view includes p-shared (shared with foreign) and p-own, but
	// not p-foreign (only referenced by foreign client c-d).
	polIDs := map[string]bool{}
	for _, p := range view.Snapshot.Policies {
		polIDs[p.ID] = true
	}
	if !polIDs["p-shared"] {
		t.Fatal("manager view missing shared policy p-shared")
	}
	if !polIDs["p-own"] {
		t.Fatal("manager view missing own policy p-own")
	}
	if polIDs["p-foreign"] {
		t.Fatal("manager view should not include p-foreign (only foreign clients)")
	}
	// Edit c-a's description and PUT back.
	view.Snapshot.Clients[0].Description = "updated description"
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 200 {
		t.Fatalf("expected 200 for unchanged shared policy, got %d %s", w.Code, w.Body.String())
	}
}

// TestManagerSourceOutsideScopeNotMaskedByPolicy verifies source_outside_scope
// is returned (not policy_shared_out_of_scope) when the manager adds a client
// with a network outside his scope but doesn't change any shared policy.
func TestManagerSourceOutsideScopeNotMaskedByPolicy(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedSharedPolicyScenario(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	// Add a new client with network outside scope (10.3.x is not in 10.1.0.0/16).
	profile := view.Snapshot.TLSProfiles[0].ID
	view.Snapshot.Clients = append(view.Snapshot.Clients, policy.Client{ID: "c-new-out", Name: "out", Networks: []string{"10.3.0.1/32"}, Enabled: true, LegacyPolicyID: "p-own"})
	_ = profile
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 422 {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "source_outside_scope" {
		t.Fatalf("expected source_outside_scope, got %v body=%s", body["code"], w.Body.String())
	}
}

// TestManagerSourceOverlapNotMaskedByPolicy verifies source_overlap is returned
// when the manager adds a client whose network overlaps a foreign client.
func TestManagerSourceOverlapNotMaskedByPolicy(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedSharedPolicyScenario(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	// Add a client with 10.1.0.0/24 — within scope but overlaps c-d (10.1.0.9/32).
	view.Snapshot.Clients = append(view.Snapshot.Clients, policy.Client{ID: "c-new-overlap", Name: "overlap", Networks: []string{"10.1.0.0/24"}, Enabled: true, LegacyPolicyID: "p-own"})
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 422 {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "source_overlap" {
		t.Fatalf("expected source_overlap, got %v body=%s", body["code"], w.Body.String())
	}
	// Verify the message doesn't leak the foreign client name or id.
	if msg, ok := body["message"].(string); ok {
		for _, forbidden := range []string{"c-d", "foreign-d", "c-b", "foreign-b"} {
			if containsStr(msg, forbidden) {
				t.Fatalf("overlap message leaked foreign client: %s", msg)
			}
		}
	}
}

// TestManagerSharedPolicyChangeRejected verifies editing a shared policy's
// content returns 403 policy_shared_out_of_scope.
func TestManagerSharedPolicyChangeRejected(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedSharedPolicyScenario(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	// Add a domain to p-shared (shared with foreign client c-b).
	for i := range view.Snapshot.Policies {
		if view.Snapshot.Policies[i].ID == "p-shared" {
			view.Snapshot.Policies[i].Destinations = append(view.Snapshot.Policies[i].Destinations, policy.Destination{Domain: "extra.test"})
		}
	}
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 403 {
		t.Fatalf("expected 403 policy_shared_out_of_scope, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "policy_shared_out_of_scope" {
		t.Fatalf("expected policy_shared_out_of_scope, got %v body=%s", body["code"], w.Body.String())
	}
}

// TestManagerPutPreservesForeignObjects verifies that after a manager PUT,
// foreign clients, foreign-only policies, settings and expert are unchanged.
func TestManagerPutPreservesForeignObjects(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedSharedPolicyScenario(t, h, admin, csrf)
	// Set expert_config and non-default settings so we can verify they survive.
	{
		w := request(h, "GET", "/configuration/draft", nil, admin, "", "")
		var d Draft
		json.Unmarshal(w.Body.Bytes(), &d)
		d.Snapshot.Expert = "http_access allow all\n"
		d.Snapshot.Settings.ConnectTimeout = 45
		if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, admin, csrf, "https://panel.test"); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a", "c-a2"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// Manager GETs and PUTs back with a trivial c-a description edit.
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	view.Snapshot.Clients[0].Description = "edited"
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Admin reads the full draft: foreign clients, p-foreign, settings, expert intact.
	w = request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var full Draft
	json.Unmarshal(w.Body.Bytes(), &full)
	clientIDs := map[string]bool{}
	for _, c := range full.Snapshot.Clients {
		clientIDs[c.ID] = true
	}
	for _, id := range []string{"c-a", "c-a2", "c-b", "c-d"} {
		if !clientIDs[id] {
			t.Fatalf("client %s missing after manager PUT", id)
		}
	}
	polIDs := map[string]bool{}
	for _, p := range full.Snapshot.Policies {
		polIDs[p.ID] = true
	}
	if !polIDs["p-foreign"] {
		t.Fatal("p-foreign missing after manager PUT")
	}
	if full.Snapshot.Expert != "http_access allow all\n" {
		t.Fatalf("expert changed: %q", full.Snapshot.Expert)
	}
	if full.Snapshot.Settings.ConnectTimeout != 45 {
		t.Fatalf("settings changed: %d", full.Snapshot.Settings.ConnectTimeout)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
