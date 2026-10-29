package httpapi

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestPasswordVerifierIsSaltedPHCOnly(t *testing.T) {
	password := "test-only-salted-password"
	a, e := PasswordHash(password)
	if e != nil {
		t.Fatal(e)
	}
	b, e := PasswordHash(password)
	if e != nil {
		t.Fatal(e)
	}
	if a == b || !strings.HasPrefix(a, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(a, password) {
		t.Fatal("unsafe verifier format or reused salt")
	}
	if !passwordMatches(a, password) || passwordMatches(a, password+"!") {
		t.Fatal("incorrect password verification")
	}
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	// The pre-PHC "argon2id$salt$key" form is no longer accepted.
	unversioned := "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
	if passwordMatches(unversioned, password) {
		t.Fatal("unversioned verifier accepted")
	}
	for _, bad := range []string{"plaintext", strings.Replace(a, "m=19456", "m=4294967295", 1), strings.Replace(a, "p=1", "p=0", 1), strings.Replace(a, "v=19", "v=16", 1), a + "$extra"} {
		if passwordMatches(bad, password) {
			t.Fatal("malformed verifier accepted")
		}
	}
}
func TestResetPasswordRevokesSessionsAndDoesNotPersistPlaintext(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	oldHash := ""
	if e := s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&oldHash); e != nil {
		t.Fatal(e)
	}
	newPassword := "test-only-replacement-secret"
	if e := ResetPassword(s.Store, "admin", newPassword); e != nil {
		t.Fatal(e)
	}
	if w := request(h, "GET", "/status", nil, cookie, "", ""); w.Code != 401 {
		t.Fatal("old session survived reset")
	}
	var current string
	if e := s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&current); e != nil {
		t.Fatal(e)
	}
	if current == oldHash || !passwordMatches(current, newPassword) || passwordMatches(current, "test-password-only-123") || strings.Contains(current, newPassword) {
		t.Fatal("password replacement failed")
	}
	var events int
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='password_reset' AND entity_id='admin'").Scan(&events); e != nil || events != 1 {
		t.Fatal("missing safe audit event")
	}
	// Login which verified the old hash before reset must not mint a new session afterwards.
	if _, e := s.newSession(httptest.NewRecorder(), "admin", oldHash, ""); !errors.Is(e, errCredentialsChanged) {
		t.Fatalf("stale login was accepted: %v", e)
	}
	var sessions int
	s.Store.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE username='admin'").Scan(&sessions)
	if sessions != 0 {
		t.Fatal("reset left an authenticated session")
	}
	rows, e := s.Store.DB.Query("SELECT value FROM kv UNION ALL SELECT body FROM operations UNION ALL SELECT actor||action||entity_id FROM audit_events")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		rows.Scan(&raw)
		if strings.Contains(raw, newPassword) {
			t.Fatal("plaintext persisted outside users table")
		}
	}
}
func TestResetUnknownUserOrWeakPasswordDoesNotChangeCredentials(t *testing.T) {
	s, _ := fixtureServer(t)
	var before string
	s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&before)
	if e := ResetPassword(s.Store, "missing", "test-only-reset-password"); e == nil {
		t.Fatal("unknown user silently created")
	}
	if e := ResetPassword(s.Store, "admin", "short"); e == nil {
		t.Fatal("weak password accepted")
	}
	var after string
	s.Store.DB.QueryRow("SELECT password_hash FROM users WHERE username='admin'").Scan(&after)
	if before != after {
		t.Fatal("rejected reset changed password")
	}
	var value string
	if e := s.Store.DB.QueryRow("SELECT username FROM users WHERE username='missing'").Scan(&value); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("missing user created")
	}
}
