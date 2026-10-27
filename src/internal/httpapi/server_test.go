package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	local "squid-control.local/squid/internal/runtime"
	"squid-control.local/squid/internal/store"
	"testing"
)

func fixtureServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, e := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.DB.Close() })
	if e = Bootstrap(st, "admin", "test-password-only-123"); e != nil {
		t.Fatal(e)
	}
	rt := local.New(t.TempDir(), "/unused", "/dev/null", "", "")
	rt.Command = func(context.Context, ...string) error { return nil }
	rt.Probe = func(context.Context, string) error { return nil }
	if e = rt.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	s, e := New(st, rt, "https://panel.test", http.NotFoundHandler(), "test-image")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Wait)
	return s, s.Handler()
}
func request(h http.Handler, method, path string, data any, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if data != nil {
		json.NewEncoder(&b).Encode(data)
	}
	r := httptest.NewRequest(method, "https://panel.test/api/v1"+path, &b)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func loginTest(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	w := request(h, "GET", "/session", nil, nil, "", "")
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie flags")
	}
	w = request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, cookie, v["csrf_token"].(string), "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	return w.Result().Cookies()[0], v["csrf_token"].(string)
}
func TestAuthenticationCSRFAndLogout(t *testing.T) {
	_, h := fixtureServer(t)
	if w := request(h, "GET", "/status", nil, nil, "", ""); w.Code != 401 {
		t.Fatal("unauthorized status exposed")
	}
	cookie, csrf := loginTest(t, h)
	if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, origin := range []string{"", "https://evil.test"} {
		w := request(h, "DELETE", "/session", nil, cookie, csrf, origin)
		if w.Code != 403 {
			t.Fatal("cross-origin mutation accepted")
		}
	}
	if w := request(h, "DELETE", "/session", nil, cookie, csrf, "https://panel.test"); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code != 401 {
		t.Fatal("revoked session still accepted")
	}
}
func TestDraftConflictAndValidation(t *testing.T) {
	_, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	input := map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}
	w = request(h, "PUT", "/configuration/draft", input, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = request(h, "PUT", "/configuration/draft", input, cookie, csrf, "https://panel.test")
	if w.Code != 409 {
		t.Fatal("stale draft overwrote newer version")
	}
	d.Snapshot.TLSProfiles[0].Mode = "invalid-mode"
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": 2, "snapshot": d.Snapshot}, cookie, csrf, "https://panel.test")
	if w.Code != 422 {
		t.Fatal("invalid tunnel mode accepted")
	}
}
func TestBootstrapIsNotPublicRegistration(t *testing.T) {
	s, _ := fixtureServer(t)
	if e := Bootstrap(s.Store, "another", "test-password-only-123"); e == nil {
		t.Fatal("bootstrap created another admin")
	}
	if h, e := PasswordHash("short"); e == nil || h != "" {
		t.Fatal("weak password accepted")
	}
}
