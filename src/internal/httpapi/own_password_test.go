package httpapi

import (
	"context"
	"testing"
)

func changePassword(current, next string) map[string]string {
	return map[string]string{"current_password": current, "new_password": next}
}

func TestChangeOwnPasswordKeepsCurrentSessionAndEndsOthers(t *testing.T) {
	s, h := fixtureServer(t)
	a1, csrf1 := loginTest(t, h)
	a2, _ := loginTest(t, h)
	const next = "test-only-own-new-password"
	if w := request(h, "PUT", "/session/password", changePassword("test-password-only-123", next), a1, "", "https://panel.test"); w.Code != 403 {
		t.Fatalf("without CSRF: %d", w.Code)
	}
	if w := request(h, "PUT", "/session/password", changePassword("wrong-password-value", next), a1, csrf1, "https://panel.test"); w.Code != 422 || !contains(w.Body.String(), "invalid_current_password") {
		t.Fatalf("wrong current: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "PUT", "/session/password", changePassword("test-password-only-123", "short"), a1, csrf1, "https://panel.test"); w.Code != 422 {
		t.Fatalf("weak new: %d", w.Code)
	}
	if w := request(h, "PUT", "/session/password", changePassword("test-password-only-123", "test-password-only-123"), a1, csrf1, "https://panel.test"); w.Code != 422 {
		t.Fatalf("same password: %d", w.Code)
	}
	if w := request(h, "GET", "/status", nil, a2, "", ""); w.Code != 200 {
		t.Fatal("rejected attempts ended a session")
	}
	if w := request(h, "PUT", "/session/password", changePassword("test-password-only-123", next), a1, csrf1, "https://panel.test"); w.Code != 204 {
		t.Fatalf("change: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/status", nil, a1, "", ""); w.Code != 200 {
		t.Fatal("current session ended")
	}
	if w := request(h, "GET", "/status", nil, a2, "", ""); w.Code != 401 {
		t.Fatal("other session survived")
	}
	var hash string
	s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&hash)
	if !passwordMatches(hash, next) || passwordMatches(hash, "test-password-only-123") || contains(hash, next) {
		t.Fatal("verifier not replaced")
	}
	var ok, failed int
	s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='change_password' AND actor='admin'").Scan(&ok)
	s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='change_password_failed'").Scan(&failed)
	if ok != 1 || failed != 1 {
		t.Fatalf("audit: ok=%d failed=%d", ok, failed)
	}
	loginAs(t, h, "admin", next)
}

func TestChangeOwnPasswordRateLimitedPerAccount(t *testing.T) {
	_, h := fixtureServer(t)
	a, csrf := loginTest(t, h)
	for i := 0; i < 5; i++ {
		if w := request(h, "PUT", "/session/password", changePassword("wrong-password-value", "test-only-own-new-password"), a, csrf, "https://panel.test"); w.Code != 422 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	if w := request(h, "PUT", "/session/password", changePassword("test-password-only-123", "test-only-own-new-password"), a, csrf, "https://panel.test"); w.Code != 429 {
		t.Fatalf("sixth attempt: %d", w.Code)
	}
}

func TestChangeOwnPasswordLosesToConcurrentReset(t *testing.T) {
	s, _ := fixtureServer(t)
	old, _ := s.Store.UserPasswordHashStandalone(context.Background(), "admin")
	if e := ResetPassword(context.Background(), s.Store, "admin", "test-only-reset-password"); e != nil {
		t.Fatal(e)
	}
	next, _ := PasswordHash("test-only-own-new-password")
	if e := s.Store.ChangeOwnPassword(context.Background(), "admin", old, next, ""); e == nil {
		t.Fatal("stale verifier overwrote a reset")
	}
}
