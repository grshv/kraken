package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"kraken.local/kraken/migrations"
)

func TestUserSessionsListAndRevoke(t *testing.T) {
	ctx := context.Background()
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	if _, e = st.CreateUser(ctx, "u", "hash", "admin", "test"); e != nil {
		t.Fatal(e)
	}
	const now = int64(1_800_000_000)
	insert := func(hash string, expires, seen int64) {
		tx, e := st.BeginTx(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = st.InsertSession(ctx, tx, hash, "u", "csrf", expires, seen, SessionClient{Remote: "10.0.0.1", UserAgent: "ua-" + hash}); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	insert("live-old", now+100, now-600)
	insert("live-new", now+100, now-60)
	insert("expired", now-1, now-60)
	insert("idle", now+100, now-3600)
	got, e := st.UserSessions(ctx, "u", now, now-1800)
	if e != nil || len(got) != 2 || got[0].TokenHash != "live-new" || got[1].TokenHash != "live-old" {
		t.Fatalf("live sessions: %+v %v", got, e)
	}
	if got[0].Remote != "10.0.0.1" || got[0].UserAgent != "ua-live-new" || got[0].Created != now-60 {
		t.Fatalf("client fields: %+v", got[0])
	}
	if n, e := st.RevokeUserSessions(ctx, "u", "live-old", "", "admin"); e != nil || n != 1 {
		t.Fatalf("revoke one: %d %v", n, e)
	}
	if n, e := st.RevokeUserSessions(ctx, "u", "", "live-new", "admin"); e != nil || n != 2 {
		t.Fatalf("revoke others (expired, idle): %d %v", n, e)
	}
	if ok, _ := st.SessionExists(ctx, "live-new"); !ok {
		t.Fatal("kept session removed")
	}
	var audits int
	st.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action IN ('revoke_session','revoke_sessions') AND entity_id='u'").Scan(&audits)
	if audits != 2 {
		t.Fatalf("audit rows: %d", audits)
	}
}

// Sessions created before migration 007 survive it with empty client fields.
func TestMigration007KeepsSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); e != nil {
		t.Fatal(e)
	}
	for _, m := range migrations.Ordered[:6] {
		if _, e = db.Exec(m.SQL); e != nil {
			t.Fatalf("migration %d: %v", m.Version, e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?,?,0)", m.Version, m.Checksum); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec("INSERT INTO users(username,password_hash,role,enabled) VALUES('u','h','admin',1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO sessions VALUES('old','u','csrf',9999999999,1800000000)"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	st, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	got, e := st.UserSessions(context.Background(), "u", 1800000000, 0)
	if e != nil || len(got) != 1 || got[0].Created != 0 || got[0].Remote != "" || got[0].UserAgent != "" {
		t.Fatalf("pre-007 session: %+v %v", got, e)
	}
}
