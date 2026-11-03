package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

const origin = "https://panel.test"

func acceptDraft(t *testing.T, h http.Handler, c *http.Cookie) Draft {
	t.Helper()
	w := request(h, "GET", "/configuration/draft", nil, c, "", "")
	if w.Code != 200 {
		t.Fatalf("get draft: %d %s", w.Code, w.Body.String())
	}
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	return d
}

func acceptPut(h http.Handler, c *http.Cookie, csrf string, d Draft) *httptest.ResponseRecorder {
	return request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, c, csrf, origin)
}

func acceptOp(h http.Handler, c *http.Cookie, csrf, key string, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", origin+"/api/v1/operations", strings.NewReader(string(b)))
	r.AddCookie(c)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func acceptWait(t *testing.T, h http.Handler, c *http.Cookie, id string) Operation {
	t.Helper()
	for i := 0; i < 200; i++ {
		var op Operation
		json.Unmarshal(request(h, "GET", "/operations/"+id, nil, c, "", "").Body.Bytes(), &op)
		if op.Status != "" && op.Status != "pending" && op.Status != "validating" && op.Status != "reloading" {
			return op
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return Operation{}
}

// acceptApply creates a revision from the draft as the given user and applies it.
// It returns the HTTP code of POST /revisions (201 when allowed).
func acceptApply(t *testing.T, h http.Handler, admin *http.Cookie, adminCSRF string, c *http.Cookie, csrf, key string) int {
	t.Helper()
	d := acceptDraft(t, h, c)
	w := request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, c, csrf, origin)
	if w.Code != 201 {
		return w.Code
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	var preview struct {
		Expected *string `json:"expected_applied_revision"`
	}
	json.Unmarshal(request(h, "GET", "/revisions/"+rev.ID+"/preview", nil, admin, "", "").Body.Bytes(), &preview)
	w = acceptOp(h, c, csrf, key+"-validate-0001", map[string]any{"action": "validate", "revision_id": rev.ID, "expected_applied_revision": preview.Expected})
	if w.Code >= 300 {
		t.Fatalf("validate: %d %s", w.Code, w.Body.String())
	}
	var op Operation
	json.Unmarshal(w.Body.Bytes(), &op)
	if op = acceptWait(t, h, c, op.ID); op.Status != "validated" {
		t.Fatalf("validation: %+v", op)
	}
	w = acceptOp(h, c, csrf, key+"-apply-00001", map[string]any{"action": "apply", "revision_id": rev.ID, "expected_applied_revision": preview.Expected, "validation_operation_id": op.ID})
	if w.Code >= 300 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &op)
	if op = acceptWait(t, h, c, op.ID); op.Status != "succeeded" && op.Status != "applied" {
		t.Fatalf("apply result: %+v", op)
	}
	return 201
}

func acceptCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var v struct {
		Code string `json:"code"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != status || (code != "" && v.Code != code) {
		t.Fatalf("want %d %s, got %d %s", status, code, w.Code, w.Body.String())
	}
}

func hasClient(s policy.Snapshot, id string) bool {
	for _, c := range s.Clients {
		if c.ID == id {
			return true
		}
	}
	return false
}

// fixture: admin applies clients A (manager's) and B, D (foreign), policies
// own (A only), shared (A and B), foreign (B only); manager "mgr" gets
// networks 10.1.0.0/16 and client A.
func scopeFixture(t *testing.T) (http.Handler, *http.Cookie, string, *http.Cookie, string) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	acceptCode(t, request(h, "POST", "/users", map[string]string{"username": "mgr", "password": "manager-password-123", "role": "manager"}, admin, csrf, origin), 201, "")
	d := acceptDraft(t, h, admin)
	tls := d.Snapshot.TLSProfiles[0].ID
	d.Snapshot.Policies = append(d.Snapshot.Policies,
		policy.Policy{ID: "p-own", Name: "own", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "own.example"}}, LegacyTLSProfileID: tls},
		policy.Policy{ID: "p-shared", Name: "shared", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "shared.example"}}, LegacyTLSProfileID: tls},
		policy.Policy{ID: "p-foreign", Name: "foreign", LegacyMode: "allowlist", Destinations: []policy.Destination{{Domain: "foreign.example"}}, LegacyTLSProfileID: tls})
	d.Snapshot.Clients = append(d.Snapshot.Clients,
		policy.Client{ID: "c-a", Name: "alpha", Kind: "host", Networks: []string{"10.1.0.5/32"}, Enabled: true, LegacyPolicyID: "p-own"},
		policy.Client{ID: "c-a2", Name: "alpha-shared", Kind: "host", Networks: []string{"10.1.0.6/32"}, Enabled: true, LegacyPolicyID: "p-shared"},
		policy.Client{ID: "c-b", Name: "bravo-secret", Kind: "host", Networks: []string{"10.2.0.5/32"}, Enabled: true, LegacyPolicyID: "p-shared"},
		policy.Client{ID: "c-d", Name: "delta-secret", Kind: "host", Networks: []string{"10.1.0.9/32"}, Enabled: true, LegacyPolicyID: "p-foreign"})
	acceptCode(t, acceptPut(h, admin, csrf, d), 200, "")
	if acceptApply(t, h, admin, csrf, admin, csrf, "fixture-admin") != 201 {
		t.Fatal("admin apply failed")
	}
	acceptCode(t, request(h, "PUT", "/users/mgr/scope", map[string]any{"networks": []string{"10.1.0.0/16"}, "client_ids": []string{"c-a", "c-a2"}}, admin, csrf, origin), 200, "")
	mgr, mcsrf := loginAs(t, h, "mgr", "manager-password-123")
	return h, admin, csrf, mgr, mcsrf
}

func TestAcceptRolesAdminAndManagerOnly(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	acceptCode(t, request(h, "POST", "/users", map[string]string{"username": "old", "password": "viewer-password-123", "role": "viewer"}, admin, csrf, origin), 422, "")
	acceptCode(t, request(h, "POST", "/users", map[string]string{"username": "mgr", "password": "manager-password-123", "role": "manager"}, admin, csrf, origin), 201, "")
	acceptCode(t, request(h, "PUT", "/users/admin/scope", map[string]any{"networks": []string{}, "client_ids": []string{}}, admin, csrf, origin), 422, "not_manager")
	acceptCode(t, request(h, "PUT", "/users/mgr/scope", map[string]any{"networks": []string{"not-a-cidr"}, "client_ids": []string{}}, admin, csrf, origin), 422, "")
}

func TestAcceptManagerScopeAndAdminOnlyEndpoints(t *testing.T) {
	h, _, _, mgr, mcsrf := scopeFixture(t)
	var sess struct {
		Role  string `json:"role"`
		Scope struct {
			Networks  []string `json:"networks"`
			ClientIDs []string `json:"client_ids"`
		} `json:"scope"`
	}
	json.Unmarshal(request(h, "GET", "/session", nil, mgr, "", "").Body.Bytes(), &sess)
	if sess.Role != "manager" || strings.Join(sess.Scope.Networks, ",") != "10.1.0.0/16" || len(sess.Scope.ClientIDs) != 2 {
		t.Fatalf("session scope: %+v", sess)
	}
	for _, path := range []string{"/users", "/users/mgr/scope", "/configuration/preview", "/configuration/expert-template", "/revisions"} {
		acceptCode(t, request(h, "GET", path, nil, mgr, "", ""), 403, "admin_required")
	}
	acceptCode(t, request(h, "PUT", "/users/mgr/scope", map[string]any{"networks": []string{"0.0.0.0/0"}, "client_ids": []string{}}, mgr, mcsrf, origin), 403, "admin_required")
	acceptCode(t, request(h, "POST", "/users", map[string]string{"username": "x", "password": "manager-password-123", "role": "admin"}, mgr, mcsrf, origin), 403, "admin_required")
	acceptCode(t, acceptOp(h, mgr, mcsrf, "manager-rollback-0001", map[string]any{"action": "rollback", "revision_id": "any"}), 403, "admin_required")
}

func TestAcceptManagerDraftViewIsScoped(t *testing.T) {
	h, admin, _, mgr, _ := scopeFixture(t)
	d := acceptDraft(t, h, mgr)
	if !hasClient(d.Snapshot, "c-a") || hasClient(d.Snapshot, "c-b") || hasClient(d.Snapshot, "c-d") {
		t.Fatalf("manager draft clients: %+v", d.Snapshot.Clients)
	}
	body, _ := json.Marshal(d.Snapshot)
	for _, secret := range []string{"bravo-secret", "delta-secret", "foreign.example", "10.2.0.5"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("manager draft leaks %s", secret)
		}
	}
	if d.Snapshot.Expert != "" {
		t.Fatal("manager draft carries expert config")
	}
	_ = admin
}

func TestAcceptManagerEditsMergeAndKeepForeignObjects(t *testing.T) {
	h, admin, _, mgr, mcsrf := scopeFixture(t)
	before := acceptDraft(t, h, admin)
	d := acceptDraft(t, h, mgr)
	for i := range d.Snapshot.Clients {
		if d.Snapshot.Clients[i].ID == "c-a" {
			d.Snapshot.Clients[i].Description = "edited by manager"
		}
	}
	d.Snapshot.Clients = append(d.Snapshot.Clients, policy.Client{ID: "c-new", Name: "new-host", Kind: "host", Networks: []string{"10.1.7.7/32"}, Enabled: true, LegacyPolicyID: "p-own"})
	acceptCode(t, acceptPut(h, mgr, mcsrf, d), 200, "")
	after := acceptDraft(t, h, admin)
	if !hasClient(after.Snapshot, "c-b") || !hasClient(after.Snapshot, "c-d") || !hasClient(after.Snapshot, "c-new") {
		t.Fatalf("merge lost objects: %+v", after.Snapshot.Clients)
	}
	if before.Snapshot.Expert != after.Snapshot.Expert || policy.Hash(policy.Snapshot{Settings: before.Snapshot.Settings}) != policy.Hash(policy.Snapshot{Settings: after.Snapshot.Settings}) {
		t.Fatal("manager PUT changed admin-only parts")
	}
	var sess struct {
		Scope struct {
			ClientIDs []string `json:"client_ids"`
		} `json:"scope"`
	}
	json.Unmarshal(request(h, "GET", "/session", nil, mgr, "", "").Body.Bytes(), &sess)
	if !strings.Contains(strings.Join(sess.Scope.ClientIDs, ","), "c-new") {
		t.Fatal("created client not assigned to manager")
	}
}

func TestAcceptManagerDraftRejections(t *testing.T) {
	h, _, _, mgr, mcsrf := scopeFixture(t)
	add := func(c policy.Client) *httptest.ResponseRecorder {
		d := acceptDraft(t, h, mgr)
		d.Snapshot.Clients = append(d.Snapshot.Clients, c)
		return acceptPut(h, mgr, mcsrf, d)
	}
	acceptCode(t, add(policy.Client{ID: "c-out", Name: "out", Kind: "host", Networks: []string{"10.3.0.1/32"}, Enabled: true, LegacyPolicyID: "p-own"}), 422, "source_outside_scope")
	acceptCode(t, add(policy.Client{ID: "c-wide", Name: "wide", Kind: "network", Networks: []string{"10.0.0.0/8"}, Enabled: true, LegacyPolicyID: "p-own"}), 422, "source_outside_scope")
	w := add(policy.Client{ID: "c-ov", Name: "ov", Kind: "network", Networks: []string{"10.1.0.0/24"}, Enabled: true, LegacyPolicyID: "p-own"})
	acceptCode(t, w, 422, "source_overlap")
	if strings.Contains(w.Body.String(), "delta") || strings.Contains(w.Body.String(), "c-d") {
		t.Fatal("overlap error names foreign client")
	}
	d := acceptDraft(t, h, mgr)
	for i := range d.Snapshot.Policies {
		if d.Snapshot.Policies[i].ID == "p-shared" {
			d.Snapshot.Policies[i].Destinations = append(d.Snapshot.Policies[i].Destinations, policy.Destination{Domain: "more.example"})
		}
	}
	acceptCode(t, acceptPut(h, mgr, mcsrf, d), 403, "policy_shared_out_of_scope")
}

func TestAcceptManagerApplyOnlyOwnChanges(t *testing.T) {
	h, admin, csrf, mgr, mcsrf := scopeFixture(t)
	ad := acceptDraft(t, h, admin)
	for i := range ad.Snapshot.Clients {
		if ad.Snapshot.Clients[i].ID == "c-b" {
			ad.Snapshot.Clients[i].Description = "foreign pending change"
		}
	}
	acceptCode(t, acceptPut(h, admin, csrf, ad), 200, "")
	foreign := func(sid *http.Cookie) string {
		w := request(h, "GET", "/configuration/draft", nil, sid, "", "")
		var body map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != 200 {
			t.Fatalf("draft: %d %s", w.Code, w.Body.String())
		}
		v, ok := body["foreign_changes"]
		if !ok {
			return "absent"
		}
		if strings.Contains(w.Body.String(), "foreign pending change") {
			t.Fatal("manager draft reveals the foreign change")
		}
		return fmt.Sprint(v)
	}
	if got := foreign(mgr); got != "true" {
		t.Fatalf("manager foreign_changes before apply = %s, want true", got)
	}
	if got := foreign(admin); got != "absent" {
		t.Fatalf("admin draft carries foreign_changes: %s", got)
	}
	md := acceptDraft(t, h, mgr)
	acceptCode(t, request(h, "POST", "/revisions", map[string]any{"expected_draft_version": md.Version}, mgr, mcsrf, origin), 403, "draft_out_of_scope")
	if acceptApply(t, h, admin, csrf, admin, csrf, "admin-second") != 201 {
		t.Fatal("admin apply failed")
	}
	if got := foreign(mgr); got != "false" {
		t.Fatalf("manager foreign_changes after admin apply = %s, want false", got)
	}
	md = acceptDraft(t, h, mgr)
	for i := range md.Snapshot.Clients {
		if md.Snapshot.Clients[i].ID == "c-a" {
			md.Snapshot.Clients[i].Description = "own pending change"
		}
	}
	acceptCode(t, acceptPut(h, mgr, mcsrf, md), 200, "")
	if code := acceptApply(t, h, admin, csrf, mgr, mcsrf, "manager-own"); code != 201 {
		t.Fatalf("manager could not apply own change: %d", code)
	}
}

func TestAcceptManagerProxyAccounts(t *testing.T) {
	h, admin, csrf, mgr, mcsrf := scopeFixture(t)
	acceptCode(t, request(h, "POST", "/proxy-accounts", map[string]any{"username": "adm-login", "password": "test-only-proxy-password"}, admin, csrf, origin), 201, "")
	acceptCode(t, request(h, "POST", "/proxy-accounts", map[string]any{"username": "mgr-login", "password": "test-only-proxy-password"}, mgr, mcsrf, origin), 201, "")
	w := request(h, "GET", "/proxy-accounts", nil, mgr, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "mgr-login") || strings.Contains(w.Body.String(), "adm-login") {
		t.Fatalf("manager proxy accounts: %d %s", w.Code, w.Body.String())
	}
	acceptCode(t, request(h, "PATCH", "/proxy-accounts/adm-login", map[string]any{"enabled": false}, mgr, mcsrf, origin), 404, "")
	w = request(h, "POST", "/proxy-accounts", map[string]any{"username": "adm-login", "password": "test-only-proxy-password"}, mgr, mcsrf, origin)
	acceptCode(t, w, 409, "username_taken")
	if strings.Contains(w.Body.String(), "admin") {
		t.Fatal("username_taken reveals owner")
	}
}

func TestAcceptManagerEventsForeignClientIsNotFound(t *testing.T) {
	h, _, _, mgr, _ := scopeFixture(t)
	acceptCode(t, request(h, "GET", "/events?client_id=c-b", nil, mgr, "", ""), 404, "")
	if w := request(h, "GET", "/events?client_id=c-a", nil, mgr, "", ""); w.Code != 200 {
		t.Fatalf("own client events: %d %s", w.Code, w.Body.String())
	}
	w := request(h, "GET", "/events/clients", nil, mgr, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "bravo-secret") || strings.Contains(w.Body.String(), "delta-secret") {
		t.Fatalf("event clients leak: %s", w.Body.String())
	}
}

func TestNetworkInScopeRejectsWiderPrefixSharingBase(t *testing.T) {
	scope := store.Scope{Networks: []string{"10.0.0.0/16"}}
	for raw, want := range map[string]bool{"10.0.0.0/8": false, "10.0.0.0/15": false, "10.0.0.0/16": true, "10.0.5.0/24": true, "10.0.0.7": true, "10.1.0.0/24": false} {
		if got := networkInScope(scope, raw); got != want {
			t.Fatalf("%s: got %v want %v", raw, got, want)
		}
	}
}
