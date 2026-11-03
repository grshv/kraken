// Package admin implements operator-facing state mutations that CLI subcommands
// share with the HTTP API. Each change writes an audit row; the last-admin guard
// and session revocation match the /users PATCH contract by delegating to
// store.ChangeUser, which is the single place that enforces those rules.
package admin

import (
	"context"
	"database/sql"
	"errors"

	"kraken.local/kraken/internal/store"
)

var ErrLastAdmin = errors.New("last enabled admin")
var ErrNotFound = errors.New("user not found")

// UserRow is a panel user with its live session count, for CLI listing.
type UserRow struct {
	Username, Role string
	Enabled        bool
	Sessions       int
}

// ListUsers returns every user with the number of currently stored sessions.
func ListUsers(ctx context.Context, st *store.Store) ([]UserRow, error) {
	rows, e := st.Read.QueryContext(ctx, `SELECT username, role, enabled,
		(SELECT COUNT(*) FROM sessions WHERE sessions.username=users.username)
		FROM users ORDER BY username`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var r UserRow
		if e = rows.Scan(&r.Username, &r.Role, &r.Enabled, &r.Sessions); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEnabled toggles a user. Disabling revokes that user's sessions; the last
// enabled admin cannot be disabled. Delegates to store.ChangeUser so the guard,
// revocation and audit transaction stay identical to PATCH /users.
func SetEnabled(ctx context.Context, st *store.Store, username string, enabled bool, actor string) error {
	_, e := st.ChangeUser(ctx, username, nil, &enabled, nil, actor)
	return mapError(e)
}

// SetRole changes a user's role to admin or manager. Demoting the last enabled
// admin fails with ErrLastAdmin; any role change revokes the user's sessions.
func SetRole(ctx context.Context, st *store.Store, username, role, actor string) error {
	if !store.ValidRole(role) {
		return errors.New("invalid role")
	}
	r := role
	_, e := st.ChangeUser(ctx, username, &r, nil, nil, actor)
	return mapError(e)
}

// RevokeSessions deletes sessions for one user, or for every user when username
// is empty, and returns how many were removed. The audit row is written in the
// same transaction so a crash cannot leave a revoke unattributed.
func RevokeSessions(ctx context.Context, st *store.Store, username, actor string) (int, error) {
	tx, e := st.BeginTx(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var res sql.Result
	if username == "" {
		res, e = tx.ExecContext(ctx, "DELETE FROM sessions")
	} else {
		res, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE username=?", username)
	}
	if e != nil {
		return 0, e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return 0, e
	}
	if e = st.AuditTx(ctx, tx, actor, "revoke_sessions", username); e != nil {
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return int(n), nil
}

// mapError translates store sentinel errors into the admin package's contract
// errors so CLI callers can branch on ErrLastAdmin/ErrNotFound without importing
// store. Other errors pass through unchanged.
func mapError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, store.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(e, store.ErrLastAdmin) {
		return ErrLastAdmin
	}
	return e
}
