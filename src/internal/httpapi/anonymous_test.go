package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnonymousFloodDoesNotOccupyLoginPool(t *testing.T) {
	s, h := fixtureServer(t)
	var cookie *http.Cookie
	var v struct {
		CSRF string `json:"csrf_token"`
	}
	for i := 0; i < 300; i++ {
		w := request(h, "GET", "/session", nil, nil, "", "")
		if w.Code != 200 {
			t.Fatalf("anonymous request %d: %d", i, w.Code)
		}
		cookie = w.Result().Cookies()[0]
		if len(cookie.Value) > 128 {
			t.Fatal("cookie exceeds reader bound")
		}
		if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); e != nil || count != 0 {
		t.Fatalf("anonymous sessions persisted: %d %v", count, e)
	}
	w := request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, cookie, v.CSRF, "https://panel.test")
	if w.Code != 200 {
		t.Fatal("flood prevented login: " + w.Body.String())
	}
}

func TestAnonymousCookieIntegrityExpiryAndRestart(t *testing.T) {
	s, _ := fixtureServer(t)
	w := httptest.NewRecorder()
	v := s.newAnonymousSession(w)
	token := w.Result().Cookies()[0].Value
	if got, e := s.readAnonymousSession(token); e != nil || got.CSRF != v.CSRF || got.User != "" {
		t.Fatalf("round trip: %v", e)
	}
	body := "anon.1.random"
	expired := body + "." + s.anonymousMAC("cookie", body)
	for _, bad := range []string{token + "x", strings.Replace(token, "anon.", "anon.0", 1), expired} {
		if _, e := s.readAnonymousSession(bad); e == nil {
			t.Fatal("invalid cookie accepted")
		}
	}
	s.anonymousKey = []byte("different process key")
	if _, e := s.readAnonymousSession(token); e == nil {
		t.Fatal("old process cookie survived")
	}
}

func TestAuthenticatedCapacityDoesNotDeadlockStorage(t *testing.T) {
	s, h := fixtureServer(t)
	_, e := s.Store.DB.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<256)
 INSERT INTO sessions SELECT printf('test-%d',x),'admin','csrf',?,? FROM n`, time.Now().Add(time.Hour).Unix(), time.Now().Unix())
	if e != nil {
		t.Fatal(e)
	}
	w := request(h, "GET", "/session", nil, nil, "", "")
	var v struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	done := make(chan int, 1)
	go func() {
		done <- request(h, "POST", "/session", map[string]string{"username": "admin", "password": "test-password-only-123"}, w.Result().Cookies()[0], v.CSRF, "https://panel.test").Code
	}()
	select {
	case code := <-done:
		if code != 503 {
			t.Fatalf("capacity status: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capacity check deadlocked")
	}
	var count int
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='session_capacity_reached'").Scan(&count); e != nil || count != 1 {
		t.Fatalf("capacity audit unavailable: %d %v", count, e)
	}
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='login'").Scan(&count); e != nil || count != 0 {
		t.Fatalf("rejected login recorded as success: %d %v", count, e)
	}
}
