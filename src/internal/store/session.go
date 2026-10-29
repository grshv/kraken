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

// BeginTx is the single way to start a write transaction. The caller owns the
// transaction's lifecycle (Commit/Rollback); pass the same ctx to the per-step
// helpers below so the OpTimeout ceiling bound to the begin also covers them.
func (s *Store) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return s.DB.BeginTx(ctx, nil)
}

// LookupSession loads (username, csrf, expires_at, last_seen_at) for the
// given token hash within tx. Returns sql.ErrNoRows if not found.
func (s *Store) LookupSession(ctx context.Context, tx *sql.Tx, tokenHash string) (username, csrf string, expires, lastSeen int64, err error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	err = tx.QueryRowContext(ctx, "SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", tokenHash).Scan(&username, &csrf, &expires, &lastSeen)
	return
}

// LookupSessionContext is the reader-pool variant used by readSession so a
// session check never takes the write lock.
func (s *Store) LookupSessionContext(ctx context.Context, tokenHash string) (username, csrf string, expires, lastSeen int64, err error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	err = s.Read.QueryRowContext(ctx, "SELECT username,csrf,expires_at,last_seen_at FROM sessions WHERE token_hash=?", tokenHash).Scan(&username, &csrf, &expires, &lastSeen)
	return
}

// TouchSession updates last_seen_at in place within tx. Used by write paths
// that already hold a transaction.
func (s *Store) TouchSession(ctx context.Context, tx *sql.Tx, tokenHash string, now int64) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now, tokenHash)
	return e
}

// TouchSessionConditional updates last_seen_at to now only if the row still
// has the expected last_seen_at, returning whether the update applied. A
// parallel password reset or disable deletes the row (0 rows affected); the
// caller then re-checks existence to tell revocation from a concurrent touch
// so a deleted session can never be revived or reused.
func (s *Store) TouchSessionConditional(ctx context.Context, tokenHash string, expectedLastSeen, now int64) (bool, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
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
	ctx, cancel := opCtx(ctx)
	defer cancel()
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
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return e
}

// DeleteSessionTx is the within-transaction variant of DeleteSession.
func (s *Store) DeleteSessionTx(ctx context.Context, tx *sql.Tx, tokenHash string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return e
}

// DeleteSessionsByUser removes every session for a user. Used by ResetPassword
// to atomically revoke active sessions when the password changes.
func (s *Store) DeleteSessionsByUser(ctx context.Context, tx *sql.Tx, username string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "DELETE FROM sessions WHERE username=?", username)
	return e
}

// DeleteExpiredSessions removes sessions that have passed their expiry or
// have not been seen for 30 minutes, within tx. Used by newSession during
// the pool guard step.
func (s *Store) DeleteExpiredSessions(ctx context.Context, tx *sql.Tx, now int64, idleCutoff int64) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<? OR last_seen_at<?", now, idleCutoff)
	return e
}

// SessionCount returns the stored session count within tx. Used by newSession
// to enforce the persisted session capacity.
func (s *Store) SessionCount(ctx context.Context, tx *sql.Tx) (total int, err error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&total)
	return total, err
}

// InsertSession writes one session row within tx.
func (s *Store) InsertSession(ctx context.Context, tx *sql.Tx, hash, user, csrf string, expires, now int64) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,?,?,?)", hash, user, csrf, expires, now)
	return e
}

// UserPasswordHash loads the salted Argon2id PHC string for one user, or
// returns sql.ErrNoRows. The lookup happens within tx so a parallel
// ResetPassword cannot change the hash between read and use.
func (s *Store) UserPasswordHash(ctx context.Context, tx *sql.Tx, username string) (string, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var h string
	e := tx.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE username=?", username).Scan(&h)
	return h, e
}

// UserPasswordHashStandalone reads the verifier through the reader pool so a
// login probe never blocks apply or event ingestion.
func (s *Store) UserPasswordHashStandalone(ctx context.Context, username string) (string, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var h string
	e := s.Read.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE username=?", username).Scan(&h)
	return h, e
}

// UpdateUserPasswordHash updates the password_hash for an existing user
// within tx. Caller checks RowsAffected to detect unknown user.
func (s *Store) UpdateUserPasswordHash(ctx context.Context, tx *sql.Tx, username, hash string) (int64, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	result, e := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE username=?", hash, username)
	if e != nil {
		return 0, e
	}
	return result.RowsAffected()
}

// UserCount returns the number of users within tx. Used by Bootstrap to
// gate first-admin creation.
func (s *Store) UserCount(ctx context.Context, tx *sql.Tx) (int, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	var n int
	e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, e
}

// InsertUser adds one user row within tx. Used by Bootstrap.
func (s *Store) InsertUser(ctx context.Context, tx *sql.Tx, username, hash string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash) VALUES(?,?)", username, hash)
	return e
}

// AuditTx writes an audit_events row within an existing transaction. Used
// when the audit insert must atomic with the action it describes (e.g.
// ResetPassword changing the hash + revoking sessions + writing the audit
// row in one tx). actor is the entity taking the action; for anonymous
// paths caller should pass the remote host rather than "" so monitoring
// can correlate events to a source even without an authenticated user.
func (s *Store) AuditTx(ctx context.Context, tx *sql.Tx, actor, action, entity string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "INSERT INTO audit_events(actor,action,entity_id,created_at) VALUES(?,?,?,?)", actor, action, entity, time.Now().Unix())
	return e
}
