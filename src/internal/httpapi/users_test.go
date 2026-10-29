package httpapi

import (
	"encoding/json"
	"kraken.local/kraken/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func loginAs(t *testing.T, h http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	w := request(h, "GET", "/session", nil, nil, "", "")
	var v struct {
		CSRF string `json:"csrf_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	w = request(h, "POST", "/session", map[string]string{"username": username, "password": password}, w.Result().Cookies()[0], v.CSRF, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return w.Result().Cookies()[0], v.CSRF
}
func TestUsersRolesAndServerPermissions(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	w := request(h, "POST", "/users", map[string]string{"username": "observer", "password": "observer-password-123", "role": "viewer"}, admin, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("password data in response")
	}
	viewer, viewerCSRF := loginAs(t, h, "observer", "observer-password-123")
	w = request(h, "GET", "/session", nil, viewer, "", "")
	var v struct {
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v.Role != "viewer" || strings.Join(v.Permissions, ",") != "read,audit" {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/activity", "/squid/metrics", "/status", "/events", "/events/clients", "/audit", "/certificates"} {
		if w := request(h, "GET", path, nil, viewer, "", ""); w.Code != 200 {
			t.Fatalf("viewer cannot read %s: %s", path, w.Body.String())
		}
	}
	for _, path := range []string{"/users", "/proxy-accounts", "/configuration/draft", "/configuration/expert-template", "/configuration/preview", "/revisions", "/revisions/any", "/revisions/any/preview", "/operations/any"} {
		if w := request(h, "GET", path, nil, viewer, "", ""); w.Code != 403 {
			t.Fatalf("viewer accessed %s: %d", path, w.Code)
		}
	}
	for _, target := range []struct{ method, path string }{{"POST", "/users"}, {"PATCH", "/users/admin"}, {"POST", "/proxy-accounts"}, {"PATCH", "/proxy-accounts/any"}, {"PUT", "/configuration/draft"}, {"POST", "/revisions"}, {"POST", "/operations"}} {
		if w := request(h, target.method, target.path, map[string]string{}, viewer, viewerCSRF, "https://panel.test"); w.Code != 403 {
			t.Fatalf("viewer mutation %s: %d", target.path, w.Code)
		}
	}
	if w := request(h, "POST", "/users", map[string]string{"username": "extra", "password": "other-password-123", "role": "operator"}, admin, csrf, "https://panel.test"); w.Code != 422 {
		t.Fatal("unapproved role accepted")
	}
	if w := request(h, "PATCH", "/users/observer", map[string]any{"enabled": false}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/events", nil, viewer, "", ""); w.Code != 401 {
		t.Fatal("disabled viewer session survived")
	}
	// Verify a login begun with an old hash cannot mint a session after disable.
	oldHash, e := s.Store.UserPasswordHashStandalone("observer")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.newSession(httptest.NewRecorder(), "observer", oldHash, ""); e == nil {
		t.Fatal("disabled user logged in")
	}
}
func TestUserChangesRevokeSessionsAndProtectLastAdmin(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	for _, change := range []map[string]any{{"enabled": false}, {"role": "viewer"}} {
		if w := request(h, "PATCH", "/users/admin", change, admin, csrf, "https://panel.test"); w.Code != 409 {
			t.Fatal("last admin lost: " + w.Body.String())
		}
	}
	for _, change := range []map[string]any{{"password": "short"}, {"role": "invalid"}, {}} {
		if w := request(h, "PATCH", "/users/admin", change, admin, csrf, "https://panel.test"); w.Code != 422 {
			t.Fatal("invalid user change accepted")
		}
	}
	w := request(h, "POST", "/users", map[string]string{"username": "second", "password": "second-password-123", "role": "admin"}, admin, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	second, _ := loginAs(t, h, "second", "second-password-123")
	if w := request(h, "PATCH", "/users/second", map[string]string{"role": "viewer"}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/status", nil, second, "", ""); w.Code != 401 {
		t.Fatal("demotion did not revoke sessions")
	}
	second, _ = loginAs(t, h, "second", "second-password-123")
	if w := request(h, "PATCH", "/users/second", map[string]string{"password": "replacement-password-123"}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/status", nil, second, "", ""); w.Code != 401 {
		t.Fatal("password change did not revoke sessions")
	}
	_, _ = loginAs(t, h, "second", "replacement-password-123")
	rows, e := s.Store.Users()
	if e != nil {
		t.Fatal(e)
	}
	for _, u := range rows {
		if !store.ValidRole(u.Role) {
			t.Fatal("bad stored role")
		}
	}
	var raw string
	if e = s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='second'").Scan(&raw); e != nil || !strings.HasPrefix(raw, "$argon2id$") {
		t.Fatal("password verifier not stored")
	}
	var leaks int
	if e = s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE actor LIKE '%password-123%' OR entity_id LIKE '%password-123%'").Scan(&leaks); e != nil || leaks != 0 {
		t.Fatal("password in audit")
	}
}
