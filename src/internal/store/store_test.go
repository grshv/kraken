package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrationsApplyInOrder verifies the migration runner brings a fresh DB
// to the latest schema, records each version in schema_migrations, and re-runs
// are idempotent. The audit_events.created_at INTEGER type is the visible
// schema property introduced by migration 002; asserting it via PRAGMA
// table_info guards against accidental rollback of the type unification.
func TestMigrationsApplyInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.db")
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	var count int
	if e := st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count < 2 {
		t.Fatalf("expected at least 2 migrations recorded, got %d", count)
	}
	// audit_events.created_at must be INTEGER after migration 002.
	rows, e := st.DB.Query("PRAGMA table_info(audit_events)")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if e = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); e != nil {
			t.Fatal(e)
		}
		if name == "created_at" && ctype != "INTEGER" {
			t.Fatalf("audit_events.created_at is %s, want INTEGER", ctype)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	// Reopening an already-migrated DB must not apply migrations again and
	// must not error on the conditional SELECT FROM schema_migrations.
	if st2, e := Open(path); e != nil {
		t.Fatalf("re-open migrated DB: %v", e)
	} else {
		st2.DB.Close()
	}
}

// TestAuditTimestampIntegerRoundTrip verifies that an Audit insert followed by
// a SELECT returns an INTEGER created_at, not a TEXT RFC3339 string. This
// locks in the migration 002 invariant at the application layer: future code
// that accidentally writes a formatted string would be detected at read time.
func TestAuditTimestampIntegerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, e := Open(filepath.Join(dir, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	if e := st.Audit("tester", "action_under_test", "entity-1"); e != nil {
		t.Fatal(e)
	}
	var created any
	if e := st.DB.QueryRow("SELECT created_at FROM audit_events WHERE actor=? ORDER BY id DESC LIMIT 1", "tester").Scan(&created); e != nil {
		t.Fatal(e)
	}
	switch v := created.(type) {
	case int64:
		if v <= 0 {
			t.Fatalf("non-positive audit timestamp: %d", v)
		}
	default:
		t.Fatalf("audit_events.created_at came back as %T (%v), want int64 after migration 002", created, created)
	}
}
