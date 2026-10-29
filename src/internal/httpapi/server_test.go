package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"squid-control.local/squid/internal/policy"
	local "squid-control.local/squid/internal/runtime"
	"squid-control.local/squid/internal/store"
	"strings"
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
	rt := local.New(t.TempDir(), "/unused", "/dev/null", "", "", "test-image")
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

func TestOperationFindsExpertParseLine(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	d.Snapshot.Mode = "expert"
	d.Snapshot.Expert = "acl fixture_bad src not..an..address\nhttp_access allow fixture_bad\n"
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var d2 Draft
	json.Unmarshal(w.Body.Bytes(), &d2)
	w = request(h, "POST", "/revisions", map[string]int{"expected_draft_version": d2.Version}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)

	rendered, rerr := policy.Render(rev.Snapshot, s.Runtime.Options)
	if rerr != nil {
		t.Fatal(rerr)
	}
	beginLine := 0
	for i, ln := range strings.Split(rendered, "\n") {
		if strings.TrimSpace(ln) == "# BEGIN user policy" {
			beginLine = i + 1
			break
		}
	}
	if beginLine == 0 {
		t.Fatal("no BEGIN marker in rendered config")
	}
	firstExpert := beginLine + 1

	s.Runtime.Command = func(ctx context.Context, args ...string) error {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-k" && args[i+1] == "parse" {
				return &local.ParseError{Line: firstExpert}
			}
		}
		return nil
	}

	body := `{"action":"validate","revision_id":"` + rev.ID + `","expected_applied_revision":null}`
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", "test-parse-error-expert-line-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 202 {
		t.Fatal(rec.Body.String())
	}
	var op Operation
	json.Unmarshal(rec.Body.Bytes(), &op)
	s.Wait()
	w = request(h, "GET", "/operations/"+op.ID, nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var final Operation
	json.Unmarshal(w.Body.Bytes(), &final)
	if final.Status != "rejected" {
		t.Fatalf("expected rejected, got %s", final.Status)
	}
	if len(final.Findings) == 0 || !strings.Contains(final.Findings[0], "строка 1 экспертной секции") {
		t.Fatalf("unexpected findings: %v", final.Findings)
	}
}

func TestOperationFindsTotalConfigParseLineWhenNotExpert(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	// A structured client puts lines into the user section; they must still be
	// reported as rendered-config lines because only expert text is user-authored.
	snapshot := d.Snapshot
	snapshot.Policies = append(snapshot.Policies, policy.Policy{ID: "p1", Name: "web", Mode: "all_web", Destinations: []policy.Destination{}, TLSProfileID: snapshot.TLSProfiles[0].ID})
	snapshot.Clients = append(snapshot.Clients, policy.Client{ID: "c1", Name: "office", Networks: []string{"192.0.2.0/24"}, Enabled: true, PolicyID: "p1"})
	rendered, e := policy.Render(snapshot, s.Runtime.Options)
	if e != nil {
		t.Fatal(e)
	}
	userLine := 0
	for i, ln := range strings.Split(rendered, "\n") {
		if ln == "# BEGIN user policy" {
			userLine = i + 2
			break
		}
	}
	if _, ok := policy.UserSectionLine(rendered, userLine); !ok {
		t.Fatal("fixture has no structured line inside the user section")
	}
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": snapshot}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var d2 Draft
	json.Unmarshal(w.Body.Bytes(), &d2)
	w = request(h, "POST", "/revisions", map[string]int{"expected_draft_version": d2.Version}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)

	s.Runtime.Command = func(ctx context.Context, args ...string) error {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-k" && args[i+1] == "parse" {
				return &local.ParseError{Line: userLine}
			}
		}
		return nil
	}

	body := `{"action":"validate","revision_id":"` + rev.ID + `","expected_applied_revision":null}`
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", "test-parse-error-total-line-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 202 {
		t.Fatal(rec.Body.String())
	}
	var op Operation
	json.Unmarshal(rec.Body.Bytes(), &op)
	s.Wait()
	w = request(h, "GET", "/operations/"+op.ID, nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var final Operation
	json.Unmarshal(w.Body.Bytes(), &final)
	if final.Status != "rejected" {
		t.Fatalf("expected rejected, got %s", final.Status)
	}
	if len(final.Findings) == 0 || final.Findings[0] != fmt.Sprintf("Squid отклонил конфигурацию: строка %d итоговой конфигурации", userLine) {
		t.Fatalf("unexpected findings: %v", final.Findings)
	}
}
