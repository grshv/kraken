package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// LoadState reads one JSON-encoded state value stored under key. found is
// false when no row exists; any other failure is returned. Reads go through the
// reader pool so runtime recovery never contends with control-DB writers.
func (s *Store) LoadState(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	e := s.Read.QueryRowContext(ctx, "SELECT value FROM kv WHERE key=?", key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if e := json.Unmarshal([]byte(raw), v); e != nil {
		return false, e
	}
	return true, nil
}

// SaveState writes one JSON-encoded state value through the writer pool using
// an upsert, so the runtime journal and last-good manifest are durable across
// crashes alongside the rest of the control database.
func (s *Store) SaveState(ctx context.Context, key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = s.DB.ExecContext(ctx, "INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, string(b))
	return e
}
