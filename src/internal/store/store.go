package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"squid-control.local/squid/migrations"
	"time"
)

var ErrConflict = errors.New("revision conflict")

type Store struct{ DB *sql.DB }

func ID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
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
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	fail := func(e error) (*Store, error) { db.Close(); return nil, e }
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000", migrations.Initial} {
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
