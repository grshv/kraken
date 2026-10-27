package store

import (
	"database/sql"
	"path/filepath"
	"squid-control.local/squid/migrations"
	"testing"
)

func TestMigrationFailureRollsBackSchemaDataAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(migrations.Initial); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES('admin','login','','2026-10-27T00:00:00Z');
 CREATE TRIGGER reject_v2 BEFORE INSERT ON schema_migrations WHEN NEW.version=2 BEGIN SELECT RAISE(ABORT,'injected migration failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if st, e := Open(path); e == nil {
		st.DB.Close()
		t.Fatal("migration failure not reported")
	}
	var stamp string
	if e = db.QueryRow("SELECT created_at FROM audit_events").Scan(&stamp); e != nil || stamp != "2026-10-27T00:00:00Z" {
		t.Fatalf("old audit lost: %q %v", stamp, e)
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='audit_events_new'").Scan(&n); e != nil || n != 0 {
		t.Fatalf("partial schema committed: %d %v", n, e)
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=2").Scan(&n); e != nil || n != 0 {
		t.Fatalf("failed migration version committed: %d %v", n, e)
	}
	if _, e = db.Exec("DROP TRIGGER reject_v2"); e != nil {
		t.Fatal(e)
	}
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	var seconds int64
	if e = st.DB.QueryRow("SELECT created_at FROM audit_events").Scan(&seconds); e != nil || seconds != 1793059200 {
		t.Fatalf("retry lost audit timestamp: %d %v", seconds, e)
	}
}
