package admin

import (
	"path/filepath"
	"testing"

	"kraken.local/kraken/internal/store"
)

// openStore returns a migrated store in a temp dir and a deferred close.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "control.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}

func mustCreate(t *testing.T, s *store.Store, name, role string) {
	t.Helper()
	if _, e := s.CreateUser(name, "phc-verifier", role, "fixture"); e != nil {
		t.Fatal(e)
	}
}

func mustSession(t *testing.T, s *store.Store, hash, user string) {
	t.Helper()
	if _, e := s.DB.Exec("INSERT INTO sessions VALUES(?,?,?,?,?)", hash, user, "csrf", 9999999999, 0); e != nil {
		t.Fatal(e)
	}
}

func sessionCount(t *testing.T, s *store.Store, user string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE username=?", user).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func auditCount(t *testing.T, s *store.Store, actor, action string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE actor=? AND action=?", actor, action).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestSetEnabledRevokesSessionsAndAudits(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "alice", "admin")
	mustCreate(t, s, "bob", "manager")
	mustSession(t, s, "h-bob", "bob")
	if e := SetEnabled(s, "bob", false, "cli"); e != nil {
		t.Fatal(e)
	}
	if n := sessionCount(t, s, "bob"); n != 0 {
		t.Fatalf("bob sessions after disable: %d", n)
	}
	if n := auditCount(t, s, "cli", "update_user"); n != 1 {
		t.Fatalf("update_user audit rows: %d", n)
	}
}

func TestSetEnabledLastAdminBlocked(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "only", "admin")
	if e := SetEnabled(s, "only", false, "cli"); e != ErrLastAdmin {
		t.Fatalf("disable last admin: want ErrLastAdmin, got %v", e)
	}
	if e := SetEnabled(s, "absent", false, "cli"); e != ErrNotFound {
		t.Fatalf("disable unknown: want ErrNotFound, got %v", e)
	}
}

func TestSetEnabledOneOfTwoAdmins(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "a", "admin")
	mustCreate(t, s, "b", "admin")
	if e := SetEnabled(s, "a", false, "cli"); e != nil {
		t.Fatalf("disable one of two admins: %v", e)
	}
	// Remaining enabled admin is still active and can be disabled.
	if e := SetEnabled(s, "b", false, "cli"); e != ErrLastAdmin {
		t.Fatalf("disable last admin: want ErrLastAdmin, got %v", e)
	}
}

func TestSetRoleDemoteLastAdminBlocked(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "only", "admin")
	if e := SetRole(s, "only", "manager", "cli"); e != ErrLastAdmin {
		t.Fatalf("demote last admin: want ErrLastAdmin, got %v", e)
	}
}

func TestSetRoleRevokesSessions(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "a", "admin")
	mustCreate(t, s, "b", "admin")
	mustSession(t, s, "h-b", "b")
	if e := SetRole(s, "b", "manager", "cli"); e != nil {
		t.Fatal(e)
	}
	if n := sessionCount(t, s, "b"); n != 0 {
		t.Fatalf("b sessions after role change: %d", n)
	}
}

func TestSetRoleInvalid(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "a", "admin")
	if e := SetRole(s, "a", "viewer", "cli"); e == nil {
		t.Fatal("invalid role accepted")
	}
}

func TestRevokeSessionsOneUser(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "a", "admin")
	mustCreate(t, s, "b", "admin")
	mustSession(t, s, "h1", "a")
	mustSession(t, s, "h2", "a")
	mustSession(t, s, "h3", "b")
	n, e := RevokeSessions(s, "a", "cli")
	if e != nil {
		t.Fatal(e)
	}
	if n != 2 {
		t.Fatalf("revoked: want 2, got %d", n)
	}
	if c := sessionCount(t, s, "a"); c != 0 {
		t.Fatalf("a sessions left: %d", c)
	}
	if c := sessionCount(t, s, "b"); c != 1 {
		t.Fatalf("b sessions left: %d", c)
	}
	if c := auditCount(t, s, "cli", "revoke_sessions"); c != 1 {
		t.Fatalf("revoke audit rows: %d", c)
	}
}

func TestRevokeSessionsAll(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "a", "admin")
	mustCreate(t, s, "b", "admin")
	mustSession(t, s, "h1", "a")
	mustSession(t, s, "h2", "b")
	mustSession(t, s, "h3", "b")
	n, e := RevokeSessions(s, "", "cli")
	if e != nil {
		t.Fatal(e)
	}
	if n != 3 {
		t.Fatalf("revoked all: want 3, got %d", n)
	}
}

func TestListUsersIncludesSessionCounts(t *testing.T) {
	s := openStore(t)
	mustCreate(t, s, "alice", "admin")
	mustCreate(t, s, "bob", "manager")
	mustSession(t, s, "h1", "alice")
	mustSession(t, s, "h2", "alice")
	mustSession(t, s, "h3", "bob")
	rows, e := ListUsers(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: %d", len(rows))
	}
	want := map[string]int{"alice": 2, "bob": 1}
	for _, r := range rows {
		if r.Sessions != want[r.Username] {
			t.Fatalf("%s sessions: want %d, got %d", r.Username, want[r.Username], r.Sessions)
		}
	}
}
