package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestPruneUsesAuditRetention verifies Prune deletes audit_events older than
// AuditRetention and keeps recent ones — the row-count cap is gone.
func TestPruneUsesAuditRetention(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	now := time.Now()
	old := now.Add(-(AuditRetention + 24*time.Hour))
	recent := now.Add(-time.Hour)
	if _, e := st.DB.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", "old-actor", "test", "e-old", old.Unix()); e != nil {
		t.Fatal(e)
	}
	if _, e := st.DB.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", "recent-actor", "test", "e-recent", recent.Unix()); e != nil {
		t.Fatal(e)
	}
	if e := st.Prune(context.Background(), now.Add(-AuditRetention), 0); e != nil {
		t.Fatal(e)
	}
	var n int
	st.Read.QueryRow("SELECT COUNT(*) FROM audit_events WHERE actor='old-actor'").Scan(&n)
	if n != 0 {
		t.Fatal("audit event older than retention was not pruned")
	}
	st.Read.QueryRow("SELECT COUNT(*) FROM audit_events WHERE actor='recent-actor'").Scan(&n)
	if n != 1 {
		t.Fatal("recent audit event was pruned")
	}
}

// TestTouchSessionConditionalAndExistence verifies the conditional touch closes
// the reset race: updating a deleted session affects 0 rows and SessionExists
// confirms revocation, so a revoked session can never be revived.
func TestTouchSessionConditionalAndExistence(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	ctx := context.Background()
	// Bootstrap a user + session via the store helpers.
	tx, e := st.BeginTx(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e := st.InsertUser(ctx, tx, "admin", "phc-hash"); e != nil {
		t.Fatal(e)
	}
	hash := "test-token-hash"
	now := time.Now().Unix()
	if e := st.InsertSession(ctx, tx, hash, "admin", "csrf", now+3600, now); e != nil {
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	// Touch with the correct expected last_seen — should succeed.
	touched, e := st.TouchSessionConditional(ctx, hash, now, now+1)
	if e != nil || !touched {
		t.Fatalf("conditional touch on existing session failed: touched=%v err=%v", touched, e)
	}
	// Delete the session (simulating a concurrent password reset).
	if e := st.DeleteSession(ctx, hash); e != nil {
		t.Fatal(e)
	}
	// Touch again with the stale expected last_seen — should NOT touch (row gone).
	touched, e = st.TouchSessionConditional(ctx, hash, now, now+2)
	if e != nil {
		t.Fatal(e)
	}
	if touched {
		t.Fatal("conditional touch on deleted session affected a row")
	}
	// Existence recheck confirms the session is revoked.
	exists, e := st.SessionExists(ctx, hash)
	if e != nil {
		t.Fatal(e)
	}
	if exists {
		t.Fatal("SessionExists returned true for a deleted session")
	}
}
