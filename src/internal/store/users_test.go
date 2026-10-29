package store

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentDemotionsKeepOneAdmin(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "users.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	for _, name := range []string{"one", "two"} {
		if _, e = s.CreateUser(name, "test-verifier", "admin", "fixture"); e != nil {
			t.Fatal(e)
		}
	}
	role := "viewer"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); _, e := s.ChangeUser(name, &role, nil, nil, "fixture"); errs <- e }(name)
	}
	wg.Wait()
	close(errs)
	success, blocked := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if e == ErrLastAdmin {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || blocked != 1 {
		t.Fatalf("demotions: success=%d blocked=%d", success, blocked)
	}
	var n int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM users WHERE enabled=1 AND role='admin'").Scan(&n); e != nil || n != 1 {
		t.Fatal("no active admin remains")
	}
}
func TestUserAuditFailureRollsBackChangesAndRevocation(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "users.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	for _, name := range []string{"one", "two"} {
		if _, e = s.CreateUser(name, "test-verifier", "admin", "fixture"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.DB.Exec("INSERT INTO sessions VALUES('session','two','csrf',9999999999,9999999999)"); e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec(`CREATE TRIGGER fail_user_audit BEFORE INSERT ON audit_events WHEN NEW.action='update_user' BEGIN SELECT RAISE(ABORT,'injected write failure'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	enabled := false
	if _, e = s.ChangeUser("two", nil, &enabled, nil, "fixture"); e == nil {
		t.Fatal("audit failure ignored")
	}
	var active bool
	var n int
	_ = s.DB.QueryRow("SELECT enabled FROM users WHERE username='two'").Scan(&active)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE username='two'").Scan(&n)
	if !active || n != 1 {
		t.Fatal("partial user mutation committed")
	}
}
