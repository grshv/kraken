package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// LastAppliedAt returns the updated_at of the most recent succeeded apply
// operation, or (false) when none exists. Operations live in the control DB.
func (s *Store) LastAppliedAt(ctx context.Context) (string, bool, error) {
	var body string
	e := s.DB.QueryRowContext(ctx, "SELECT body FROM operations WHERE json_extract(body,'$.status')='succeeded' AND json_extract(body,'$.action')='apply' ORDER BY rowid DESC LIMIT 1").Scan(&body)
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

// DataDir returns the directory holding the main SQLite database file, derived
// from PRAGMA database_list. Empty for in-memory or fileless databases.
func (s *Store) DataDir() string {
	rows, e := s.DB.Query("PRAGMA database_list")
	if e != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if e := rows.Scan(&seq, &name, &file); e != nil || name != "main" {
			continue
		}
		if file == "" || strings.HasPrefix(file, ":") {
			return ""
		}
		return filepath.Dir(file)
	}
	return ""
}
