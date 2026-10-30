package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"kraken.local/kraken/migrations"
)

// Numbers are sequential, stored in the revision body and never reused after
// older revisions are pruned.
func TestRevisionNumbersSequentialAcrossPrune(t *testing.T) {
	ctx := context.Background()
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("r%d", i)
		n, e := st.InsertRevision(ctx, id, `{"id":"`+id+`","number":0}`, "admin", "create_revision")
		if e != nil || n != int64(i) {
			t.Fatalf("insert %s: n=%d err=%v", id, n, e)
		}
		if got, e := st.RevisionNumber(ctx, id); e != nil || got != int64(i) {
			t.Fatalf("stored number %s: %d %v", id, got, e)
		}
	}
	if e = st.Prune(ctx, time.Now().Add(-AuditRetention), 1); e != nil {
		t.Fatal(e)
	}
	if _, e = st.RevisionNumber(ctx, "r1"); e != sql.ErrNoRows {
		t.Fatalf("r1 must be pruned: %v", e)
	}
	if n, e := st.InsertRevision(ctx, "r4", `{"id":"r4"}`, "admin", "create_revision"); e != nil || n != 4 {
		t.Fatalf("after prune: n=%d err=%v", n, e)
	}
	rows, e := st.ListRevisions(ctx)
	if e != nil || len(rows) != 2 || rows[0].Body != `{"id":"r4","number":4}` {
		t.Fatalf("list: %+v %v", rows, e)
	}
}

// Migration 006 numbers revisions written before it by insertion order and
// seeds the counter so the next revision continues the sequence.
func TestMigration006NumbersExistingRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	for _, m := range migrations.Ordered[:5] {
		if _, e = db.Exec(m.SQL); e != nil {
			t.Fatalf("migration %d: %v", m.Version, e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", m.Version, m.Checksum); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"old-b", "old-a"} {
		if _, e = db.Exec("INSERT INTO kv VALUES(?,?)", "revision:"+id, `{"id":"`+id+`"}`); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	ctx := context.Background()
	for id, want := range map[string]int64{"old-b": 1, "old-a": 2} {
		if n, e := st.RevisionNumber(ctx, id); e != nil || n != want {
			t.Fatalf("%s: %d %v, want %d", id, n, e, want)
		}
	}
	if n, e := st.InsertRevision(ctx, "new", `{"id":"new"}`, "admin", "create_revision"); e != nil || n != 3 {
		t.Fatalf("next: %d %v", n, e)
	}
}
