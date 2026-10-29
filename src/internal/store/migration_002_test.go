package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"kraken.local/kraken/migrations"
)

// TestMigration002ConvertsViewerToManager builds a schema-001 database with a
// viewer user and an active session, runs Open (which applies migration 002),
// and verifies the viewer became a manager, the session survived FKs, and the
// new scope tables + proxy_accounts.owner column exist.
func TestMigration002ConvertsViewerToManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	// Apply schema 001 verbatim, seed a viewer + session, record the ledger.
	if _, e = db.Exec(migrations.Ordered[0].SQL); e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO schema_migrations(version,checksum,applied_at) VALUES(1,'baseline-v1',0)"); e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO users(username,password_hash,role,enabled) VALUES('viewer-user','hash','viewer',1)"); e != nil {
		db.Close()
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO sessions(token_hash,username,csrf,expires_at,last_seen_at) VALUES('tok','viewer-user','csrf',9999999999,9999999999)"); e != nil {
		db.Close()
		t.Fatal(e)
	}
	db.Close()

	st, e := Open(path)
	if e != nil {
		t.Fatalf("Open after migration 002 failed: %v", e)
	}
	defer st.DB.Close()

	var role string
	if e = st.DB.QueryRow("SELECT role FROM users WHERE username='viewer-user'").Scan(&role); e != nil || role != "manager" {
		t.Fatalf("viewer not converted to manager: role=%q err=%v", role, e)
	}
	var n int
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM sessions WHERE username='viewer-user'").Scan(&n); e != nil || n != 1 {
		t.Fatalf("session not preserved: n=%d err=%v", n, e)
	}
	if e = st.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=2 AND checksum='roles-v1'").Scan(&n); e != nil || n != 1 {
		t.Fatalf("migration 002 ledger missing: n=%d err=%v", n, e)
	}
	for _, table := range []string{"manager_networks", "manager_clients"} {
		var name string
		if e = st.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); e != nil || name != table {
			t.Fatalf("table %s missing: %v", table, e)
		}
	}
	var cols []string
	rows, e := st.DB.Query("PRAGMA table_info(proxy_accounts)")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if e = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		cols = append(cols, name)
	}
	rows.Close()
	found := false
	for _, c := range cols {
		if c == "owner" {
			found = true
		}
	}
	if !found {
		t.Fatal("proxy_accounts.owner column missing")
	}
}
