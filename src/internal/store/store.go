package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"kraken.local/kraken/migrations"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
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
	if _, e = db.Exec("PRAGMA synchronous=FULL"); e != nil {
		return fail(e)
	}
	if e = migrate(db); e != nil {
		return fail(e)
	}
	if e = os.Chmod(path, 0600); e != nil {
		return fail(e)
	}
	return &Store{db}, nil
}

// migrate applies pending migrations, their data and the version ledger in one
// transaction. A recorded checksum that differs from this build's means the
// database came from an incompatible build; Open refuses it.
func migrate(db *sql.DB) error {
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); e != nil {
		return e
	}
	known := map[int]string{}
	for _, m := range migrations.Ordered {
		known[m.Version] = m.Checksum
	}
	rows, e := tx.Query("SELECT version, checksum FROM schema_migrations")
	if e != nil {
		return e
	}
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		var checksum string
		if e = rows.Scan(&version, &checksum); e != nil {
			rows.Close()
			return e
		}
		if known[version] != checksum {
			rows.Close()
			return fmt.Errorf("database schema version %d is from an incompatible build; recreate the data volume", version)
		}
		applied[version] = true
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, m := range migrations.Ordered {
		if applied[m.Version] {
			continue
		}
		if _, e = tx.Exec(m.SQL); e != nil {
			return fmt.Errorf("migration %d: %w", m.Version, e)
		}
		if _, e = tx.Exec("INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?,?,?)", m.Version, m.Checksum, time.Now().Unix()); e != nil {
			return fmt.Errorf("migration %d: %w", m.Version, e)
		}
	}
	return tx.Commit()
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

// Audit records an event stamped in INTEGER unix seconds.
func (s *Store) Audit(actor, action, entity string) error {
	_, e := s.DB.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, entity, time.Now().Unix())
	return e
}

// Operation CRUD. Bodies are opaque JSON strings so the Operation struct can
// stay private to httpapi.

// InsertOperation writes the operation row plus its audit event in one tx so a
// crash between INSERTs cannot leave an audit_events row without its operation
// (or vice versa). The id is passed explicitly because callers already have it
// from ID(); deriving it from opBody JSON would be needlessly fragile.
func (s *Store) InsertOperation(id, opBody, idempotencyKey, requestHash, actor, action string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO operations VALUES(?,?,?,?)", id, idempotencyKey, requestHash, opBody); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, id, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}

// UpdateOperation overwrites the body for an existing operation id.
func (s *Store) UpdateOperation(id, opBody string) error {
	_, e := s.DB.Exec("UPDATE operations SET body=? WHERE id=?", opBody, id)
	return e
}

// LoadOperation returns the raw JSON body of one operation, or sql.ErrNoRows.
func (s *Store) LoadOperation(id string) (string, error) {
	var raw string
	e := s.DB.QueryRow("SELECT body FROM operations WHERE id=?", id).Scan(&raw)
	return raw, e
}

// FindOperationByIdempotencyKey returns (id, request_hash, true) when key
// exists; (false) when not found; or an error for storage failures.
func (s *Store) FindOperationByIdempotencyKey(key string) (id, requestHash string, found bool, err error) {
	e := s.DB.QueryRow("SELECT id,request_hash FROM operations WHERE idempotency_key=?", key).Scan(&id, &requestHash)
	if errors.Is(e, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if e != nil {
		return "", "", false, e
	}
	return id, requestHash, true, nil
}

// CountSucceededByRevision reports how many operations reference the revision
// with status='succeeded'. Used to gate rollback to a previously-applied rev.
func (s *Store) CountSucceededByRevision(revID string) (int, error) {
	var n int
	e := s.DB.QueryRow("SELECT COUNT(*) FROM operations WHERE json_extract(body,'$.revision_id')=? AND json_extract(body,'$.status')='succeeded'", revID).Scan(&n)
	return n, e
}

// ListOperationBodies returns the (id, body) of every operation row, in any
// order. Used by httpapi's New() reconciliation pass to recover abandoned ops.
func (s *Store) ListOperationBodies() ([]struct{ ID, Body string }, error) {
	rows, e := s.DB.Query("SELECT id,body FROM operations")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []struct{ ID, Body string }{}
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			return nil, e
		}
		out = append(out, struct{ ID, Body string }{id, raw})
	}
	return out, rows.Err()
}

// ChangeDraftWithAssignments writes the draft, the draft-level audit row, one
// audit row per changed client/policy and assigns new manager clients in one
// transaction so a crash cannot leave a created client unassigned. entityIDs
// are the client/policy IDs that differ from the active snapshot; they receive
// action-specific audit rows alongside the legacy "draft" row.
func (s *Store) ChangeDraftWithAssignments(v any, actor, action string, expected int, username string, newClientIDs, entityIDs []string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRow("SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.Exec("UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, "draft", now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, id, now); e != nil {
			return e
		}
	}
	for _, id := range newClientIDs {
		if _, e = tx.Exec("INSERT OR IGNORE INTO manager_clients(username,client_id) VALUES(?,?)", username, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// ChangeDraft writes the draft and the draft-level plus per-entity audit rows
// in one transaction. The admin path: no manager assignments are needed.
func (s *Store) ChangeDraft(v any, actor, action string, expected int, entityIDs []string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRow("SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.Exec("UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, "draft", now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, id, now); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// DiscardDraft resets the draft to the active snapshot (passed already formed by
// the handler), bumps the version by one, and writes the draft-level plus
// per-entity audit rows in one transaction. entityIDs are the client/policy IDs
// that differed between the discarded draft and the active snapshot.
func (s *Store) DiscardDraft(v any, actor string, expected int, entityIDs []string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRow("SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.Exec("UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, "discard_draft", "draft", now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, "discard_draft", id, now); e != nil {
			return e
		}
	}
	return tx.Commit()
}

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
	// created_at is INTEGER unix seconds; comparing against
	// the same unit keeps range deletes O(log n) on the index instead of falling
	// back to a full scan with TEXT comparison.
	if _, e = tx.Exec("DELETE FROM audit_events WHERE created_at < ?", auditCutoff.Unix()); e != nil {
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

// InsertRevision stores the revision JSON under kv key "revision:<id>" and
// writes the corresponding audit_event in one transaction. Used by
// httpapi.createRevision so schema details stay inside store.
func (s *Store) InsertRevision(revID, revBody, actor, action string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("INSERT INTO kv VALUES(?,?)", "revision:"+revID, revBody); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, revID, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}

// ListRevisions returns the most recent 100 revision bodies with their
// applied/latest_status summary. The query that lives here is the doctrinal
// source of "revision is applied iff at least one succeeded operation references
// it" and "latest_status is the most recent operation status for that revision,
// defaulting to 'draft'" — both mirrors of the httpapi contract.
func (s *Store) ListRevisions() ([]struct {
	Body         string
	Applied      bool
	LatestStatus string
}, error) {
	rows, e := s.DB.Query(`SELECT value,
 EXISTS(SELECT 1 FROM operations WHERE json_extract(body,'$.revision_id')=json_extract(kv.value,'$.id') AND json_extract(body,'$.status')='succeeded'),
 COALESCE((SELECT json_extract(body,'$.status') FROM operations WHERE json_extract(body,'$.revision_id')=json_extract(kv.value,'$.id') ORDER BY rowid DESC LIMIT 1),'draft')
 FROM kv WHERE key LIKE 'revision:%' ORDER BY rowid DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []struct {
		Body         string
		Applied      bool
		LatestStatus string
	}{}
	for rows.Next() {
		var item struct {
			Body         string
			Applied      bool
			LatestStatus string
		}
		if e = rows.Scan(&item.Body, &item.Applied, &item.LatestStatus); e != nil {
			return nil, e
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
	if _, e = tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, key, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}
