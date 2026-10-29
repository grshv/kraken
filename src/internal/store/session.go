package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session and user CRUD helpers. Multi-step transactions (e.g. NewSession's password-check-then-insert) take a *sql.Tx
// the caller has already started so the cross-statement atomicity is
// preserved without exposing bare SQL to the caller.

// BeginTx returns a transaction the caller can pass to the per-step session
// helpers below. The transaction's lifecycle (Commit/Rollback) stays owned
// by the caller so a multi-statement flow can rollback cleanly on a late
// failure.
func (s *Store) BeginTx() (*sql.Tx, error) { return s.DB.Begin() }

// BeginTxContext starts a write transaction bound to ctx so a handler deadline
// interrupts lock acquisition instead of waiting out busy_timeout.
func (s *Store) BeginTxContext(ctx context.Context) (*sql.Tx, error) {
	return s.DB.BeginTx(ctx, nil)
}

// LookupSession loads (username, csrf, expires_at, last_seen_at) for the
// given token hash within tx. Returns sql.ErrNoRows if not found.
func (s *Store) LookupSession(tx *sql.Tx, tokenHash string) (username, csrf string, expires, lastSeen int64, err error) {
	err = tx.QueryRow("SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", tokenHash).Scan(&username, &csrf, &expires, &lastSeen)
	return
}

// LookupSessionContext is the reader-pool variant used by readSession so a
// session check never takes the write lock.
func (s *Store) LookupSessionContext(ctx context.Context, tokenHash string) (username, csrf string, expires, lastSeen int64, err error) {
	err = s.Read.QueryRowContext(ctx, "SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", tokenHash).Scan(&username, &csrf, &expires, &lastSeen)
	return
}

// TouchSession updates last_seen_at in place within tx. Used by write paths
// that already hold a transaction.
func (s *Store) TouchSession(tx *sql.Tx, tokenHash string, now int64) error {
	_, e := tx.Exec("UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now, tokenHash)
	return e
}

// TouchSessionConditional updates last_seen_at to now only if the row still
// has the expected last_seen_at, returning whether the update applied. A
// parallel password reset or disable deletes the row (0 rows affected); the
// caller then re-checks existence to tell revocation from a concurrent touch
// so a deleted session can never be revived or reused.
func (s *Store) TouchSessionConditional(ctx context.Context, tokenHash string, expectedLastSeen, now int64) (bool, error) {
	res, e := s.DB.ExecContext(ctx, "UPDATE sessions SET last_seen_at=? WHERE token_hash=? AND last_seen_at=?", now, tokenHash, expectedLastSeen)
	if e != nil {
		return false, e
	}
	n, e := res.RowsAffected()
	return n == 1, e
}

// SessionExists reports whether a session row survives for the token hash.
// Used after a conditional touch noop to distinguish revocation from a
// concurrent touch.
func (s *Store) SessionExists(ctx context.Context, tokenHash string) (bool, error) {
	var one int
	e := s.Read.QueryRowContext(ctx, "SELECT 1 FROM sessions WHERE token_hash=?", tokenHash).Scan(&one)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return e == nil, e
}

// DeleteSession removes one row by token hash. Used by logout and by
// newSession when rotating an anonymous CSRF holder into an authenticated
// session.
func (s *Store) DeleteSession(tokenHash string) error {
	return s.DeleteSessionContext(context.Background(), tokenHash)
}
func (s *Store) DeleteSessionContext(ctx context.Context, tokenHash string) error {
	_, e := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return e
}

// DeleteSessionTx is the within-transaction variant of DeleteSession.
func (s *Store) DeleteSessionTx(tx *sql.Tx, tokenHash string) error {
	_, e := tx.Exec("DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return e
}

// DeleteSessionsByUser removes every session for a user. Used by ResetPassword
// to atomically revoke active sessions when the password changes.
func (s *Store) DeleteSessionsByUser(tx *sql.Tx, username string) error {
	_, e := tx.Exec("DELETE FROM sessions WHERE username=?", username)
	return e
}

// DeleteExpiredSessions removes sessions that have passed their expiry or
// have not been seen for 30 minutes, within tx. Used by newSession during
// the pool guard step.
func (s *Store) DeleteExpiredSessions(tx *sql.Tx, now int64, idleCutoff int64) error {
	_, e := tx.Exec("DELETE FROM sessions WHERE expires_at<? OR last_seen_at<?", now, idleCutoff)
	return e
}

// SessionCount returns the stored session count within tx. Used by newSession
// to enforce the persisted session capacity.
func (s *Store) SessionCount(tx *sql.Tx) (total int, err error) {
	err = tx.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&total)
	return total, err
}

// InsertSession writes one session row within tx.
func (s *Store) InsertSession(tx *sql.Tx, hash, user, csrf string, expires, now int64) error {
	_, e := tx.Exec("INSERT INTO sessions VALUES(?,?,?,?,?)", hash, user, csrf, expires, now)
	return e
}

// UserPasswordHash loads the salted Argon2id PHC string for one user, or
// returns sql.ErrNoRows. The lookup happens within tx so a parallel
// ResetPassword cannot change the hash between read and use.
func (s *Store) UserPasswordHash(tx *sql.Tx, username string) (string, error) {
	var h string
	e := tx.QueryRow("SELECT password_hash FROM users WHERE username=?", username).Scan(&h)
	return h, e
}

// UserPasswordHashStandalone is the outside-transaction variant used by
// login() before it has opened a tx.
func (s *Store) UserPasswordHashStandalone(username string) (string, error) {
	return s.UserPasswordHashStandaloneContext(context.Background(), username)
}

// UserPasswordHashStandaloneContext reads the verifier through the reader pool
// so a login probe never blocks apply or event ingestion.
func (s *Store) UserPasswordHashStandaloneContext(ctx context.Context, username string) (string, error) {
	var h string
	e := s.Read.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE username=?", username).Scan(&h)
	return h, e
}

// UpdateUserPasswordHash updates the password_hash for an existing user
// within tx. Caller checks RowsAffected to detect unknown user.
func (s *Store) UpdateUserPasswordHash(tx *sql.Tx, username, hash string) (int64, error) {
	result, e := tx.Exec("UPDATE users SET password_hash=? WHERE username=?", hash, username)
	if e != nil {
		return 0, e
	}
	return result.RowsAffected()
}

// UserCount returns the number of users within tx. Used by Bootstrap to
// gate first-admin creation.
func (s *Store) UserCount(tx *sql.Tx) (int, error) {
	var n int
	e := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, e
}

// InsertUser adds one user row within tx. Used by Bootstrap.
func (s *Store) InsertUser(tx *sql.Tx, username, hash string) error {
	_, e := tx.Exec("INSERT INTO users(username,password_hash) VALUES(?,?)", username, hash)
	return e
}

// AuditTx writes an audit_events row within an existing transaction. Used
// when the audit insert must atomic with the action it describes (e.g.
// ResetPassword changing the hash + revoking sessions + writing the audit
// row in one tx). actor is the entity taking the action; for anonymous
// paths caller should pass the remote host rather than "" so monitoring
// can correlate events to a source even without an authenticated user.
func (s *Store) AuditTx(tx *sql.Tx, actor, action, entity string) error {
	_, e := tx.Exec("INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, entity, time.Now().Unix())
	return e
}
