package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"kraken.local/kraken/migrations"
)

// TestMigrationsRecordLedgerAndReopen checks a fresh database records every
// migration and reopening does not reapply them.
func TestMigrationsRecordLedgerAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	var versions int
	if e := st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); e != nil || versions != len(migrations.Ordered) {
		t.Fatalf("ledger has %d rows, want %d: %v", versions, len(migrations.Ordered), e)
	}
	st.DB.Close()
	st, e = Open(path)
	if e != nil {
		t.Fatalf("re-open migrated DB: %v", e)
	}
	st.DB.Close()
}

// TestOpenRejectsIncompatibleSchema refuses a ledger written by another build.
func TestOpenRejectsIncompatibleSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES(1,'7d2b8e0b','2026-10-27T00:00:00Z'); INSERT INTO schema_migrations VALUES(5,'proxy-accounts-v1','2026-10-27T00:00:00Z')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if st, e := Open(path); e == nil {
		st.DB.Close()
		t.Fatal("incompatible schema accepted")
	} else if !strings.Contains(e.Error(), "incompatible build") {
		t.Fatalf("unexpected error: %v", e)
	}
}

// TestSessionRequiresExistingUser relies on the sessions → users foreign key.
func TestSessionRequiresExistingUser(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	tx, e := st.BeginTx()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = st.InsertSession(tx, "hash", "nobody", "csrf", 9999999999, 1); e == nil {
		t.Fatal("session for unknown user stored")
	}
}

// TestAuditTimestampIntegerRoundTrip verifies that an Audit insert followed by
// a SELECT returns an INTEGER created_at, not a TEXT RFC3339 string.
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
		t.Fatalf("audit_events.created_at came back as %T (%v), want int64", created, created)
	}
}
