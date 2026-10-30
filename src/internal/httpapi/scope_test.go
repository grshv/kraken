package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// createManagerUser helper: admin creates a manager and admin assigns a scope.
func createManagerUser(t *testing.T, h http.Handler, admin *http.Cookie, csrf, username string, networks, clientIDs []string) {
	t.Helper()
	if w := request(h, "POST", "/users", map[string]string{"username": username, "password": "manager-password-123", "role": "manager"}, admin, csrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "PUT", "/users/"+username+"/scope", map[string]any{"networks": networks, "client_ids": clientIDs}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

// draftWithClients seeds the draft with two clients and a shared policy. The
// first client is assigned to the manager; the second is foreign.
func seedDraftWithTwoClients(t *testing.T, h http.Handler, cookie *http.Cookie, csrf string) (myClientID, foreignClientID, policyID string) {
	t.Helper()
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	policyID = "p-scope-test"
	profile := d.Snapshot.TLSProfiles[0].ID
	d.Snapshot.Policies = append(d.Snapshot.Policies, policy.Policy{ID: policyID, Name: "scoped", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test", IncludeSubdomains: false}}, LegacyTLSProfileID: profile})
	myClientID = "c-mine"
	foreignClientID = "c-foreign"
	d.Snapshot.Clients = append(d.Snapshot.Clients,
		policy.Client{ID: myClientID, Name: "mine", Networks: []string{"10.1.0.0/16"}, Enabled: true, LegacyPolicyID: policyID},
		policy.Client{ID: foreignClientID, Name: "foreign", Networks: []string{"10.2.0.0/16"}, Enabled: true, LegacyPolicyID: policyID},
	)
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, cookie, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	return
}

// TestManagerScopeEndpointsAndAdminRequired covers admin-only scope endpoints
// and the not_manager / invalid_cidr error codes.
func TestManagerScopeEndpointsAndAdminRequired(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	// Scope on a non-existent user → 404.
	if w := request(h, "GET", "/users/ghost/scope", nil, admin, "", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Scope on admin (not a manager) → 422 not_manager.
	if w := request(h, "GET", "/users/admin/scope", nil, admin, "", ""); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Create manager, set scope with invalid CIDR → 422.
	if w := request(h, "POST", "/users", map[string]string{"username": "mgr", "password": "manager-password-123", "role": "manager"}, admin, csrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "PUT", "/users/mgr/scope", map[string]any{"networks": []string{"not-a-cidr"}, "client_ids": []string{}}, admin, csrf, "https://panel.test"); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Valid scope.
	if w := request(h, "PUT", "/users/mgr/scope", map[string]any{"networks": []string{"10.1.0.0/16"}, "client_ids": []string{"c-a"}}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// GET returns the normalized scope.
	w := request(h, "GET", "/users/mgr/scope", nil, admin, "", "")
	var scope struct {
		Networks  []string `json:"networks"`
		ClientIDs []string `json:"client_ids"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &scope); e != nil || len(scope.Networks) != 1 || scope.Networks[0] != "10.1.0.0/16" || len(scope.ClientIDs) != 1 || scope.ClientIDs[0] != "c-a" {
		t.Fatal(w.Body.String(), e)
	}
}

// TestManagerSessionContainsScope verifies /session exposes scope for managers.
func TestManagerSessionContainsScope(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a"})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/session", nil, mgr, "", "")
	var v struct {
		Role  string `json:"role"`
		Scope *struct {
			Networks  []string `json:"networks"`
			ClientIDs []string `json:"client_ids"`
		} `json:"scope"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil || v.Role != "manager" || v.Scope == nil || len(v.Scope.Networks) != 1 || v.Scope.Networks[0] != "10.1.0.0/16" {
		t.Fatal(w.Body.String(), e)
	}
	// Admin session has no scope.
	w = request(h, "GET", "/session", nil, admin, "", "")
	var va struct {
		Role  string `json:"role"`
		Scope *struct {
			Networks  []string `json:"networks"`
			ClientIDs []string `json:"client_ids"`
		} `json:"scope"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &va); e != nil || va.Scope != nil {
		t.Fatal("admin scope should be null", w.Body.String())
	}
}

// TestManagerDraftViewFiltersForeignClients checks GET /configuration/draft
// hides foreign clients and expert_config for a manager.
func TestManagerDraftViewFiltersForeignClients(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, foreignCID, _ := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	ids := map[string]bool{}
	for _, c := range d.Snapshot.Clients {
		ids[c.ID] = true
	}
	if !ids[myCID] || ids[foreignCID] {
		t.Fatalf("manager draft client filter wrong: %+v", ids)
	}
	if d.Snapshot.Expert != "" {
		t.Fatal("manager draft leaked expert_config")
	}
}

// TestManagerDraftMergeKeepsForeignObjects verifies PUT /configuration/draft
// from a manager keeps foreign clients and ignores settings/expert changes.
func TestManagerDraftMergeKeepsForeignObjects(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, foreignCID, polID := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// Manager GETs the filtered draft, then PUTs it back unchanged. Foreign
	// client must survive in the stored draft; settings must not be wiped.
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	if e := json.Unmarshal(w.Body.Bytes(), &view); e != nil {
		t.Fatal(e)
	}
	// Tamper settings + expert in the manager's input: they must be ignored.
	view.Snapshot.Settings.HTTPPort = 9999
	view.Snapshot.Expert = "http_access allow all\n"
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Admin reads the full draft: foreign client still there, settings intact.
	w = request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var full Draft
	if e := json.Unmarshal(w.Body.Bytes(), &full); e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, c := range full.Snapshot.Clients {
		seen[c.ID] = true
	}
	if !seen[myCID] || !seen[foreignCID] {
		t.Fatalf("merge lost clients: %+v", seen)
	}
	if full.Snapshot.Settings.HTTPPort == 9999 {
		t.Fatal("manager settings change was applied")
	}
	if full.Snapshot.Expert != "" {
		t.Fatal("manager expert change was applied")
	}
	_ = polID
}

// TestManagerSourceOutsideScope verifies a manager editing a client whose
// network is outside his scope returns 422 source_outside_scope.
func TestManagerSourceOutsideScope(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, _, _ := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	// Move the manager's own client to a network outside his scope.
	view.Snapshot.Clients[0].Networks = []string{"192.168.50.0/24"}
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 422 {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "source_outside_scope" {
		t.Fatalf("expected source_outside_scope, got %v", body["code"])
	}
}

// TestManagerSourceOverlapForeignClient verifies a manager adding a network
// that overlaps a foreign client returns 422 source_overlap without leaking.
func TestManagerSourceOverlapForeignClient(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, _, _ := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.0.0.0/8"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	// Overlap the foreign client's 10.2.0.0/16.
	view.Snapshot.Clients[0].Networks = []string{"10.2.0.0/24"}
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 422 {
		t.Fatalf("expected 422, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "source_overlap" {
		t.Fatalf("expected source_overlap, got %v", body["code"])
	}
	if msg, ok := body["message"].(string); ok {
		for _, forbidden := range []string{"foreign", "c-foreign"} {
			if contains(msg, forbidden) {
				t.Fatalf("overlap message leaked foreign client: %s", msg)
			}
		}
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestManagerDraftOutOfScopeOnRevision verifies POST /revisions by a manager
// whose draft contains a foreign change returns 403 draft_out_of_scope.
func TestManagerDraftOutOfScopeOnRevision(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, foreignCID, _ := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// Admin edits the foreign client (a change outside manager's scope).
	w := request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	for i := range d.Snapshot.Clients {
		if d.Snapshot.Clients[i].ID == foreignCID {
			d.Snapshot.Clients[i].Networks = []string{"10.2.0.0/24"}
		}
	}
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// Manager tries to create a revision from this draft.
	w = request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	json.Unmarshal(w.Body.Bytes(), &d)
	if w = request(h, "POST", "/revisions", map[string]int{"expected_draft_version": d.Version}, mgr, mgrCSRF, "https://panel.test"); w.Code != 403 {
		t.Fatalf("expected 403 draft_out_of_scope, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "draft_out_of_scope" {
		t.Fatalf("expected draft_out_of_scope, got %v", body["code"])
	}
}

// TestManagerProxyAccountOwnership checks manager creates owned accounts, sees
// only his + shared-by-his-clients, and 404 on foreign-owned PATCH.
func TestManagerProxyAccountOwnership(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, _, _ := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// Manager creates an account → owner = him.
	if w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "mgr-login", "password": "test-only-proxy-password"}, mgr, mgrCSRF, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	// Admin creates an account owned by admin (owner = NULL).
	if w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "admin-login", "password": "test-only-proxy-password"}, admin, csrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	// Manager lists: sees only his own.
	w := request(h, "GET", "/proxy-accounts", nil, mgr, "", "")
	var list struct {
		Items []struct {
			Username string  `json:"username"`
			Owner    *string `json:"owner"`
		} `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	seen := map[string]bool{}
	for _, a := range list.Items {
		seen[a.Username] = true
		if a.Owner == nil || *a.Owner != "mgr" {
			t.Fatalf("manager saw foreign account: %+v", a)
		}
	}
	if !seen["mgr-login"] {
		t.Fatal("manager did not see his own account")
	}
	if seen["admin-login"] {
		t.Fatal("manager saw admin-owned account")
	}
	// PATCH foreign-owned account → 404.
	if w := request(h, "PATCH", "/proxy-accounts/admin-login", map[string]any{"enabled": false}, mgr, mgrCSRF, "https://panel.test"); w.Code != 404 {
		t.Fatalf("expected 404 on foreign account, got %d", w.Code)
	}
	// Name collision with admin-owned login → 409 username_taken (no owner leaked).
	w = request(h, "POST", "/proxy-accounts", map[string]any{"username": "admin-login", "password": "test-only-proxy-password"}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 409 {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "username_taken" {
		t.Fatalf("expected username_taken, got %v body=%s", body["code"], w.Body.String())
	}
}

// TestManagerNewClientAutoAssigned verifies a manager creating a new client
// gets it auto-assigned to his scope, so it survives a subsequent GET.
func TestManagerNewClientAutoAssigned(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	// Manager GETs his (empty) view, adds a new client, PUTs it.
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	profile := view.Snapshot.TLSProfiles[0].ID
	polID := "p-auto-test"
	view.Snapshot.Policies = append(view.Snapshot.Policies, policy.Policy{ID: polID, Name: "auto", LegacyMode: "all_web", Destinations: []policy.Destination{}, LegacyTLSProfileID: profile})
	view.Snapshot.Clients = append(view.Snapshot.Clients, policy.Client{ID: "c-auto-new", Name: "auto", Networks: []string{"10.1.0.5/32"}, Enabled: true, LegacyPolicyID: polID})
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// The new client must appear in the manager's next GET (auto-assigned).
	w = request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	json.Unmarshal(w.Body.Bytes(), &view)
	found := false
	for _, c := range view.Snapshot.Clients {
		if c.ID == "c-auto-new" {
			found = true
		}
	}
	if !found {
		t.Fatal("auto-assigned client not visible in manager draft")
	}
	// Admin scope for the manager now includes the new client.
	w = request(h, "GET", "/users/mgr/scope", nil, admin, "", "")
	var scope struct {
		ClientIDs []string `json:"client_ids"`
	}
	json.Unmarshal(w.Body.Bytes(), &scope)
	hasNew := false
	for _, id := range scope.ClientIDs {
		if id == "c-auto-new" {
			hasNew = true
		}
	}
	if !hasNew {
		t.Fatal("new client not assigned in manager scope")
	}
}
