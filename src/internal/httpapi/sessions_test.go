package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

type sessionsResp struct {
	Items []sessionItem `json:"items"`
}

func listUserSessions(t *testing.T, h http.Handler, cookie *http.Cookie, user string) sessionsResp {
	t.Helper()
	var v sessionsResp
	if code := getJSON(t, h, cookie, "/users/"+user+"/sessions", &v); code != 200 {
		t.Fatalf("GET sessions of %s: %d", user, code)
	}
	return v
}

func TestSessionsListAndRevoke(t *testing.T) {
	_, h := fixtureServer(t)
	a1, csrf1 := loginTest(t, h)
	a2, _ := loginTest(t, h)
	a3, _ := loginTest(t, h)
	w := request(h, "GET", "/users/admin/sessions", nil, a1, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "token") {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	v := listUserSessions(t, h, a1, "admin")
	if len(v.Items) != 3 {
		t.Fatalf("sessions: %+v", v.Items)
	}
	var other string
	current := 0
	for _, it := range v.Items {
		if len(it.ID) != 16 || it.Remote == nil || *it.Remote != "192.0.2.1" || it.CreatedAt == nil || it.UserAgent != nil {
			t.Fatalf("item: %+v", it)
		}
		if it.Current {
			current++
		} else if other == "" {
			other = it.ID
		}
	}
	if current != 1 {
		t.Fatalf("current marks: %d", current)
	}
	if w := request(h, "DELETE", "/users/admin/sessions/"+other, nil, a1, "", "https://panel.test"); w.Code != 403 {
		t.Fatalf("revoke without CSRF: %d", w.Code)
	}
	if w := request(h, "DELETE", "/users/admin/sessions/0000000000000000", nil, a1, csrf1, "https://panel.test"); w.Code != 404 {
		t.Fatalf("unknown id: %d", w.Code)
	}
	if w := request(h, "DELETE", "/users/admin/sessions/"+other, nil, a1, csrf1, "https://panel.test"); w.Code != 200 {
		t.Fatalf("revoke one: %d %s", w.Code, w.Body.String())
	}
	alive := 0
	for _, c := range []*http.Cookie{a2, a3} {
		if request(h, "GET", "/status", nil, c, "", "").Code == 200 {
			alive++
		}
	}
	if alive != 1 || len(listUserSessions(t, h, a1, "admin").Items) != 2 {
		t.Fatalf("after revoke one: alive=%d", alive)
	}
	// "End other sessions" keeps the caller's own.
	if w := request(h, "DELETE", "/users/admin/sessions", nil, a1, csrf1, "https://panel.test"); w.Code != 200 {
		t.Fatalf("revoke others: %d", w.Code)
	}
	if request(h, "GET", "/status", nil, a1, "", "").Code != 200 {
		t.Fatal("caller session must survive")
	}
	for _, c := range []*http.Cookie{a2, a3} {
		if request(h, "GET", "/status", nil, c, "", "").Code != 401 {
			t.Fatal("other session survived")
		}
	}
	var audit struct {
		Items []struct {
			Action   string `json:"action"`
			EntityID string `json:"entity_id"`
		} `json:"items"`
	}
	getJSON(t, h, a1, "/audit?action=revoke_session", &audit)
	if len(audit.Items) != 1 || audit.Items[0].EntityID != "admin" {
		t.Fatalf("revoke_session audit: %+v", audit.Items)
	}
	getJSON(t, h, a1, "/audit?action=revoke_sessions", &audit)
	if len(audit.Items) != 1 {
		t.Fatalf("revoke_sessions audit: %+v", audit.Items)
	}
}

// A manager manages only their own sessions; an admin manages anyone's.
func TestSessionsOwnership(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{})
	mgr, mcsrf := loginAs(t, h, "mgr", "manager-password-123")
	if w := request(h, "GET", "/users/admin/sessions", nil, mgr, "", ""); w.Code != 404 {
		t.Fatalf("manager lists admin sessions: %d", w.Code)
	}
	if w := request(h, "DELETE", "/users/admin/sessions", nil, mgr, mcsrf, "https://panel.test"); w.Code != 404 {
		t.Fatalf("manager revokes admin sessions: %d", w.Code)
	}
	if request(h, "GET", "/status", nil, admin, "", "").Code != 200 {
		t.Fatal("admin session must survive a foreign revoke attempt")
	}
	own := listUserSessions(t, h, mgr, "mgr")
	if len(own.Items) != 1 || !own.Items[0].Current {
		t.Fatalf("manager own sessions: %+v", own.Items)
	}
	if w := request(h, "DELETE", "/users/admin/sessions/"+own.Items[0].ID, nil, mgr, mcsrf, "https://panel.test"); w.Code != 404 {
		t.Fatalf("manager revokes via admin path: %d", w.Code)
	}
	// Admin ends all of the manager's sessions.
	if got := listUserSessions(t, h, admin, "mgr"); len(got.Items) != 1 || got.Items[0].Current {
		t.Fatalf("admin sees manager sessions: %+v", got.Items)
	}
	if w := request(h, "DELETE", "/users/mgr/sessions", nil, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatalf("admin revokes manager: %d", w.Code)
	}
	if request(h, "GET", "/status", nil, mgr, "", "").Code != 401 {
		t.Fatal("manager session survived")
	}
	if w := request(h, "GET", "/users/mgr/sessions", nil, nil, "", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

func TestSessionClientSanitized(t *testing.T) {
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/session", nil)
	r.RemoteAddr = "[2001:db8::1]:5555"
	r.Header.Set("User-Agent", "Mozilla\x1b[31m\r\nX: y"+strings.Repeat("я", 400))
	c := sessionClient(r)
	if c.Remote != "2001:db8::1" {
		t.Fatalf("remote: %q", c.Remote)
	}
	if strings.ContainsAny(c.UserAgent, "\x1b\r\n") || !utf8.ValidString(c.UserAgent) || utf8.RuneCountInString(c.UserAgent) != 256 {
		t.Fatalf("user agent: %q", c.UserAgent)
	}
	if publicSessionID("abc") == publicSessionID("abd") || len(publicSessionID("abc")) != 16 {
		t.Fatal("public id")
	}
}
