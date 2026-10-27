package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"squid-control.local/squid/migrations"
	"strings"
	"time"
)

var ErrConflict = errors.New("revision conflict")

type Store struct{ DB *sql.DB }

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		// rand.Read failure is exceptionally rare (early boot, entropy exhaustion),
		// but fail() calls this for request_id on every error path — panicking here
		// would shadow the real failure. Fall back to a time-based identifier; it
		// remains unique within a single process and is enough for diagnostics.
		now := uint64(time.Now().UnixNano())
		for i := 0; i < 8; i++ {
			b[i] = byte(now >> (i * 8))
		}
		b[6] = (b[6] & 15) | 64
		b[8] = (b[8] & 63) | 128
		return fmtID(b)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmtID(b)
}
func fmtID(b []byte) string {
	const h = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, h[v>>4], h[v&15])
	}
	return string(out)
}
func Open(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	// Foreign keys are per-connection in SQLite; pass via DSN so the constraint
	// is durable regardless of the connection pool shape (today MaxOpenConns=1,
	// but a future driver tuning cannot silently disable FK enforcement).
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	if strings.HasPrefix(path, ":") || strings.HasPrefix(path, "file:") {
		// In-memory / already-qualified DSNs (mostly tests): keep it simple.
		dsn = path
	}
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Store, error) { db.Close(); return nil, e }
	// PRAGMA synchronous is not a URL parameter in modernc; run it explicitly.
	for _, q := range []string{"PRAGMA synchronous=FULL", migrations.Initial} {
		if _, e = db.Exec(q); e != nil {
			return fail(e)
		}
	}
	if e = os.Chmod(path, 0600); e != nil {
		return fail(e)
	}
	return &Store{db}, nil
}
func (s *Store) Get(key string, v any) error {
	var raw string
	e := s.DB.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&raw)
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(raw), v)
}
func (s *Store) Put(key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.Exec("INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(b))
	return e
}
func (s *Store) Audit(actor, action, entity string) error {
	_, e := s.DB.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, entity, time.Now().UTC().Format(time.RFC3339))
	return e
}

// Prune applies the retention policy documented in docs/architecture/data-model.md:
// audit events older than the cutoff are deleted, and revision blobs that are
// neither the current LKG nor among the most recent N are dropped. Callers run
// this from the periodic maintenance loop so the database cannot grow without
// bound on a long-lived single-node install.
func (s *Store) Prune(auditCutoff time.Time, revisionRetention int) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM audit_events WHERE created_at < ?", auditCutoff.UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM audit_events WHERE id NOT IN (SELECT id FROM audit_events ORDER BY id DESC LIMIT 5000)"); e != nil {
		return e
	}
	if revisionRetention > 0 {
		// Keep revisions referenced by a succeeded operation (so rollback history
		// is never pruned) plus the most recent N unreferenced ones.
		if _, e = tx.Exec(`DELETE FROM kv WHERE key LIKE 'revision:%' AND key NOT IN (
SELECT 'revision:'||json_extract(body,'$.revision_id') FROM operations WHERE json_extract(body,'$.status')='succeeded')
AND key NOT IN (SELECT key FROM kv WHERE key LIKE 'revision:%' ORDER BY rowid DESC LIMIT ?)`, revisionRetention); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// Change writes desired state and its audit event in one durable transaction.
func (s *Store) Change(key string, v any, actor, action string, expected int) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRow("SELECT value FROM kv WHERE key=?", key).Scan(&raw)
	if e != nil {
		return e
	}
	var old struct {
		Version int `json:"version"`
	}
	if e = json.Unmarshal([]byte(raw), &old); e != nil {
		return e
	}
	if old.Version != expected {
		return ErrConflict
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE kv SET value=? WHERE key=?", string(b), key); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, key, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return e
	}
	return tx.Commit()
}
