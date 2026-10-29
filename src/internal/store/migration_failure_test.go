package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationFailureRollsBackSchemaAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL);
 CREATE TRIGGER reject_v1 BEFORE INSERT ON schema_migrations WHEN NEW.version=1 BEGIN SELECT RAISE(ABORT,'injected migration failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if st, e := Open(path); e == nil {
		st.DB.Close()
		t.Fatal("migration failure not reported")
	}
	var n int
	if e = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='users'").Scan(&n); e != nil || n != 0 {
		t.Fatalf("partial schema committed: %d %v", n, e)
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); e != nil || n != 0 {
		t.Fatalf("failed migration version committed: %d %v", n, e)
	}
	if _, e = db.Exec("DROP TRIGGER reject_v1"); e != nil {
		t.Fatal(e)
	}
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	st.DB.Close()
}
