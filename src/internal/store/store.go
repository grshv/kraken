package store

import (
	"context"
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

// AuditRetention is the control-DB audit retention window. Prune deletes
// audit_events older than this; there is no row-count cap.
var AuditRetention = 3 * 365 * 24 * time.Hour

// journalSizeLimit caps the WAL sidecar growth for the control DB.
const journalSizeLimit = 64 << 20 // 64 MiB

// OpTimeout is the ceiling every store method applies to its own work, even
// when the caller passed context.Background(). context.WithTimeout honors a
// sooner parent deadline, so the effective bound is min(ctx, now+OpTimeout).
// A held write lock then surfaces as a deadline error (503 storage_busy in the
// API) instead of waiting out busy_timeout.
const OpTimeout = 3 * time.Second

// opCtx returns ctx bounded by OpTimeout. Callers must defer cancel().
func opCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, OpTimeout)
}

// Store splits the control DB into a single-writer pool (DB, _txlock=immediate,
// synchronous=FULL) and a read-only pool (Read, query_only, up to 4 conns) so
// heavy reads (audit, revisions) never block logins, apply or event ingestion.
// For in-memory/file: DSNs the reader equals the writer (one shared pool).
type Store struct {
	DB   *sql.DB
	Read *sql.DB
	// dataDir is captured in Open so DataDir never re-issues SQL.
	dataDir string
}

// Close releases both pools. Tests and CLI should call this instead of DB.Close
// so the reader pool does not leak.
func (s *Store) Close() error {
	err := s.DB.Close()
	if s.Read != nil && s.Read != s.DB {
		if e := s.Read.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

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
	inMemory := strings.HasPrefix(path, ":") || strings.HasPrefix(path, "file:")
	// Foreign keys and busy_timeout are per-connection in SQLite; pass them via
	// DSN so the constraint is durable regardless of the connection pool shape.
	// The writer takes _txlock=immediate so a held read from the reader pool
	// cannot make a begin defer and surface as SQLITE_BUSY to the handler.
	wdsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate", path)
	if inMemory {
		// In-memory / already-qualified DSNs (mostly tests): keep it simple.
		wdsn = path
	}
	db, e := sql.Open("sqlite", wdsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	var read *sql.DB
	cleanup := func() {
		db.Close()
		if read != nil && read != db {
			read.Close()
		}
	}
	// Setup pragmas run before the reader pool exists; a background context is
	// fine — these are one-shot connection-init statements, not request work.
	ctx := context.Background()
	// modernc accepts synchronous via _pragma, but set it explicitly to match
	// the established pattern and keep the writer durable regardless of driver
	// tuning. journal_size_limit bounds the WAL sidecar.
	if _, e = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); e != nil {
		cleanup()
		return nil, e
	}
	if _, e = db.ExecContext(ctx, fmt.Sprintf("PRAGMA journal_size_limit=%d", journalSizeLimit)); e != nil {
		cleanup()
		return nil, e
	}
	if inMemory {
		// DSN pragmas are not applied for raw in-memory DSNs; set them here.
		if _, e = db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); e != nil {
			cleanup()
			return nil, e
		}
		if _, e = db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); e != nil {
			cleanup()
			return nil, e
		}
	}
	if e = migrate(ctx, db); e != nil {
		cleanup()
		return nil, e
	}
	dataDir := ""
	if !inMemory {
		if e = os.Chmod(path, 0600); e != nil {
			cleanup()
			return nil, e
		}
		dataDir = filepath.Dir(path)
		read, e = openReader(path)
		if e != nil {
			cleanup()
			return nil, e
		}
	} else {
		read = db
	}
	return &Store{DB: db, Read: read, dataDir: dataDir}, nil
}

// openReader opens a query_only reader pool sharing the same database file.
// query_only prevents accidental writes through the reader; foreign_keys and
// busy_timeout match the writer so constraints and contention behave identically.
func openReader(path string) (*sql.DB, error) {
	rdsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=query_only(1)", path)
	rdb, e := sql.Open("sqlite", rdsn)
	if e != nil {
		return nil, e
	}
	rdb.SetMaxOpenConns(4)
	return rdb, nil
}

// migrate applies pending migrations, their data and the version ledger in one
// transaction. A recorded checksum that differs from this build's means the
// database came from an incompatible build; Open refuses it.
func migrate(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL)"); e != nil {
		return e
	}
	known := map[int]string{}
	for _, m := range migrations.Ordered {
		known[m.Version] = m.Checksum
	}
	rows, e := tx.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations")
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
		if _, e = tx.ExecContext(ctx, m.SQL); e != nil {
			return fmt.Errorf("migration %d: %w", m.Version, e)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?,?,?)", m.Version, m.Checksum, time.Now().Unix()); e != nil {
			return fmt.Errorf("migration %d: %w", m.Version, e)
		}
	}
	return tx.Commit()
}

// Get loads one kv value through the reader pool.
func (s *Store) Get(ctx context.Context, key string, v any) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var raw string
	e := s.Read.QueryRowContext(ctx, "SELECT value FROM kv WHERE key=?", key).Scan(&raw)
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(raw), v)
}

// Put writes one kv value through the writer.
func (s *Store) Put(ctx context.Context, key string, v any) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(b))
	return e
}

// Audit records an event stamped in INTEGER unix seconds.
func (s *Store) Audit(ctx context.Context, actor, action, entity string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := s.DB.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, entity, time.Now().Unix())
	return e
}

// Operation CRUD. Bodies are opaque JSON strings so the Operation struct can
// stay private to httpapi.

// InsertOperation writes the operation row plus its audit event in one tx so a
// crash between INSERTs cannot leave an audit_events row without its operation
// (or vice versa). The id is passed explicitly because callers already have it
// from ID(); deriving it from opBody JSON would be needlessly fragile.
func (s *Store) InsertOperation(ctx context.Context, id, opBody, idempotencyKey, requestHash, actor, action string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO operations VALUES(?,?,?,?)", id, idempotencyKey, requestHash, opBody); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, id, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}

// UpdateOperation overwrites the body for an existing operation id.
func (s *Store) UpdateOperation(ctx context.Context, id, opBody string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := s.DB.ExecContext(ctx, "UPDATE operations SET body=? WHERE id=?", opBody, id)
	return e
}

// LoadOperation returns the raw JSON body of one operation, or sql.ErrNoRows.
func (s *Store) LoadOperation(ctx context.Context, id string) (string, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var raw string
	e := s.Read.QueryRowContext(ctx, "SELECT body FROM operations WHERE id=?", id).Scan(&raw)
	return raw, e
}

// FindOperationByIdempotencyKey returns (id, request_hash, true) when key
// exists; (false) when not found; or an error for storage failures.
func (s *Store) FindOperationByIdempotencyKey(ctx context.Context, key string) (id, requestHash string, found bool, err error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	e := s.Read.QueryRowContext(ctx, "SELECT id,request_hash FROM operations WHERE idempotency_key=?", key).Scan(&id, &requestHash)
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
func (s *Store) CountSucceededByRevision(ctx context.Context, revID string) (int, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var n int
	e := s.Read.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE json_extract(body,'$.revision_id')=? AND json_extract(body,'$.status')='succeeded'", revID).Scan(&n)
	return n, e
}

// ListOperationBodies returns the (id, body) of every operation row, in any
// order. Used by httpapi's New() reconciliation pass to recover abandoned ops.
func (s *Store) ListOperationBodies(ctx context.Context) ([]struct{ ID, Body string }, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	rows, e := s.Read.QueryContext(ctx, "SELECT id,body FROM operations")
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
// action-specific audit rows alongside the legacy "draft" row. details holds
// the audit detail per entity id and under "draft" for the draft-level row.
func (s *Store) ChangeDraftWithAssignments(ctx context.Context, v any, actor, action string, expected int, username string, newClientIDs, entityIDs []string, details map[string]string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRowContext(ctx, "SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.ExecContext(ctx, "UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, action, "draft", details["draft"], now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, action, id, details[id], now); e != nil {
			return e
		}
	}
	for _, id := range newClientIDs {
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO manager_clients(username,client_id) VALUES(?,?)", username, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// ChangeDraft writes the draft and the draft-level plus per-entity audit rows
// in one transaction. The admin path: no manager assignments are needed.
func (s *Store) ChangeDraft(ctx context.Context, v any, actor, action string, expected int, entityIDs []string, details map[string]string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRowContext(ctx, "SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.ExecContext(ctx, "UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, action, "draft", details["draft"], now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, action, id, details[id], now); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// DiscardDraft resets the draft to the active snapshot (passed already formed by
// the handler), bumps the version by one, and writes the draft-level plus
// per-entity audit rows in one transaction. entityIDs are the client/policy IDs
// that differed between the discarded draft and the active snapshot.
func (s *Store) DiscardDraft(ctx context.Context, v any, actor string, expected int, entityIDs []string, details map[string]string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRowContext(ctx, "SELECT value FROM kv WHERE key='draft'").Scan(&raw)
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
	if _, e = tx.ExecContext(ctx, "UPDATE kv SET value=? WHERE key='draft'", string(b)); e != nil {
		return e
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, "discard_draft", "draft", details["draft"], now); e != nil {
		return e
	}
	for _, id := range entityIDs {
		if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, "discard_draft", id, details[id], now); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// audit_events older than AuditRetention are deleted, and revision blobs that
// are neither the current LKG nor among the most recent N are dropped. Callers
// run this from the periodic maintenance loop so the database cannot grow
// without bound on a long-lived single-node install. Audit retention is
// age-based only; there is no row-count cap.
func (s *Store) Prune(ctx context.Context, auditCutoff time.Time, revisionRetention int) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// created_at is INTEGER unix seconds; comparing against
	// the same unit keeps range deletes O(log n) on the index instead of falling
	// back to a full scan with TEXT comparison.
	if _, e = tx.ExecContext(ctx, "DELETE FROM audit_events WHERE created_at < ?", auditCutoff.Unix()); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM notification_marks WHERE marked_at < ?", time.Now().Add(-NotificationMarkRetention).Unix()); e != nil {
		return e
	}
	if revisionRetention > 0 {
		// Keep revisions referenced by a succeeded operation (so rollback history
		// is never pruned) plus the most recent N unreferenced ones.
		if _, e = tx.ExecContext(ctx, `DELETE FROM kv WHERE key LIKE 'revision:%' AND key NOT IN (
SELECT 'revision:'||json_extract(body,'$.revision_id') FROM operations WHERE json_extract(body,'$.status')='succeeded')
AND key NOT IN (SELECT key FROM kv WHERE key LIKE 'revision:%' ORDER BY rowid DESC LIMIT ?)`, revisionRetention); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// InsertRevision stores the revision JSON under kv key "revision:<id>" with
// the next sequential number in "$.number", and writes the corresponding
// audit_event in one transaction. The counter lives in kv "revision_seq", so
// numbers never repeat after pruning. detail is the audit summary. Returns the
// assigned number.
func (s *Store) InsertRevision(ctx context.Context, revID, revBody, actor, action, detail string) (int64, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var n int64
	if e = tx.QueryRowContext(ctx, `INSERT INTO kv VALUES('revision_seq','1')
 ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1 RETURNING CAST(value AS INTEGER)`).Scan(&n); e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO kv VALUES(?,json_set(?,'$.number',?))", "revision:"+revID, revBody, n); e != nil {
		return 0, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,detail,created_at) VALUES(?,?,?,?,?)", actor, action, revID, detail, time.Now().Unix()); e != nil {
		return 0, e
	}
	return n, tx.Commit()
}

// RevisionNumber reads only "$.number" of a stored revision (0 when the
// revision predates numbering), so frequent status polls skip the snapshot.
func (s *Store) RevisionNumber(ctx context.Context, revID string) (int64, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var n sql.NullInt64
	e := s.Read.QueryRowContext(ctx, "SELECT json_extract(value,'$.number') FROM kv WHERE key=?", "revision:"+revID).Scan(&n)
	return n.Int64, e
}

// ListRevisions returns the most recent 100 revision bodies with their
// applied/latest_status summary. The query that lives here is the doctrinal
// source of "revision is applied iff at least one succeeded operation references
// it" and "latest_status is the most recent operation status for that revision,
// defaulting to 'draft'" — both mirrors of the httpapi contract.
func (s *Store) ListRevisions(ctx context.Context) ([]struct {
	Body         string
	Applied      bool
	LatestStatus string
}, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	rows, e := s.Read.QueryContext(ctx, `SELECT value,
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
func (s *Store) Change(ctx context.Context, key string, v any, actor, action string, expected int) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	e = tx.QueryRowContext(ctx, "SELECT value FROM kv WHERE key=?", key).Scan(&raw)
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
	if _, e = tx.ExecContext(ctx, "UPDATE kv SET value=? WHERE key=?", string(b), key); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, key, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}
