package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// LastAppliedAt returns the updated_at of the most recent succeeded apply
// operation, or (false) when none exists. Operations live in the control DB.
func (s *Store) LastAppliedAt(ctx context.Context) (string, bool, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var body string
	e := s.Read.QueryRowContext(ctx, "SELECT body FROM operations WHERE json_extract(body,'$.status')='succeeded' AND json_extract(body,'$.action')='apply' ORDER BY rowid DESC LIMIT 1").Scan(&body)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	var op struct {
		Updated string `json:"updated_at"`
	}
	if e = json.Unmarshal([]byte(body), &op); e != nil {
		return "", false, e
	}
	return op.Updated, true, nil
}

// Checkpoint runs PRAGMA wal_checkpoint(PASSIVE) on the control DB so the WAL
// sidecar does not grow without bound. PASSIVE never waits on readers and does
// not hold the write lock, unlike TRUNCATE. Called from the periodic
// maintenance loop; the writer serializes this against other writes.
func (s *Store) Checkpoint(ctx context.Context) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := s.DB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	return e
}

// Optimize runs PRAGMA optimize on the control DB. Called daily from the
// maintenance loop so SQLite refreshes its statistics without a full ANALYZE.
func (s *Store) Optimize(ctx context.Context) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := s.DB.ExecContext(ctx, "PRAGMA optimize")
	return e
}

// DataDir returns the directory holding the main SQLite database file, captured
// in Open so no SQL is issued here. Empty for in-memory or fileless databases.
func (s *Store) DataDir() string {
	return s.dataDir
}
