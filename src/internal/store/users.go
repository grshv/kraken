package store

import (
	"context"
	"database/sql"
	"errors"
)

type User struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
}

var ErrLastAdmin = errors.New("last active administrator")
var ErrNotFound = errors.New("not found")
var ErrExists = errors.New("already exists")

func ValidRole(role string) bool { return role == "admin" || role == "manager" }

// Scope is the manager's assigned networks and clients, read fresh per request.
type Scope struct {
	Networks  []string `json:"networks"`
	ClientIDs []string `json:"client_ids"`
}

// UserScope loads the manager scope from the sidecar tables through the reader
// pool. Request handlers use this so loading a manager's scope never takes the
// write lock.
func (s *Store) UserScope(ctx context.Context, username string) (Scope, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	nets, e := s.Read.QueryContext(ctx, "SELECT cidr FROM manager_networks WHERE username=? ORDER BY cidr", username)
	if e != nil {
		return Scope{}, e
	}
	defer nets.Close()
	out := Scope{Networks: []string{}, ClientIDs: []string{}}
	for nets.Next() {
		var cidr string
		if e = nets.Scan(&cidr); e != nil {
			return Scope{}, e
		}
		out.Networks = append(out.Networks, cidr)
	}
	if e = nets.Err(); e != nil {
		return Scope{}, e
	}
	clients, e := s.Read.QueryContext(ctx, "SELECT client_id FROM manager_clients WHERE username=? ORDER BY client_id", username)
	if e != nil {
		return Scope{}, e
	}
	defer clients.Close()
	for clients.Next() {
		var id string
		if e = clients.Scan(&id); e != nil {
			return Scope{}, e
		}
		out.ClientIDs = append(out.ClientIDs, id)
	}
	if e = clients.Err(); e != nil {
		return Scope{}, e
	}
	return out, nil
}

// ReplaceScope overwrites the manager scope within tx.
func (s *Store) ReplaceScope(ctx context.Context, tx *sql.Tx, username string, scope Scope) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	if _, e := tx.ExecContext(ctx, "DELETE FROM manager_networks WHERE username=?", username); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM manager_clients WHERE username=?", username); e != nil {
		return e
	}
	for _, cidr := range scope.Networks {
		if _, e := tx.ExecContext(ctx, "INSERT INTO manager_networks(username,cidr) VALUES(?,?)", username, cidr); e != nil {
			return e
		}
	}
	for _, id := range scope.ClientIDs {
		if _, e := tx.ExecContext(ctx, "INSERT INTO manager_clients(username,client_id) VALUES(?,?)", username, id); e != nil {
			return e
		}
	}
	return nil
}

// PruneManagerClients removes assignments to clients absent from both the draft
// and the active revision. Called from periodic maintenance.
func (s *Store) PruneManagerClients(ctx context.Context, draft, active []string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	known := map[string]bool{}
	for _, id := range draft {
		known[id] = true
	}
	for _, id := range active {
		known[id] = true
	}
	rows, e := tx.QueryContext(ctx, "SELECT DISTINCT client_id FROM manager_clients")
	if e != nil {
		return e
	}
	var orphans []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		if !known[id] {
			orphans = append(orphans, id)
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range orphans {
		if _, e = tx.ExecContext(ctx, "DELETE FROM manager_clients WHERE client_id=?", id); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) User(ctx context.Context, tx *sql.Tx, username string) (User, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	v := User{Username: username}
	e := tx.QueryRowContext(ctx, "SELECT role,enabled FROM users WHERE username=?", username).Scan(&v.Role, &v.Enabled)
	return v, e
}

// UserContext loads one user through the reader pool (readSession checks the
// role/enabled state without taking the write lock).
func (s *Store) UserContext(ctx context.Context, username string) (User, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	v := User{Username: username}
	e := s.Read.QueryRowContext(ctx, "SELECT role,enabled FROM users WHERE username=?", username).Scan(&v.Role, &v.Enabled)
	return v, e
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	rows, e := s.Read.QueryContext(ctx, "SELECT username,role,enabled FROM users ORDER BY username")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var v User
		if e = rows.Scan(&v.Username, &v.Role, &v.Enabled); e != nil {
			return nil, e
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Store) CreateUser(ctx context.Context, username, hash, role, actor string) (User, error) {
	v := User{Username: username, Role: role, Enabled: true}
	if !ValidRole(role) {
		return v, errors.New("invalid role")
	}
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&n); e != nil {
		return v, e
	}
	if n > 0 {
		return v, ErrExists
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,role,enabled) VALUES(?,?,?,1)", username, hash, role); e != nil {
		return v, e
	}
	if e = s.AuditTx(ctx, tx, actor, "create_user", username); e != nil {
		return v, e
	}
	return v, tx.Commit()
}

func (s *Store) ChangeUser(ctx context.Context, username string, role *string, enabled *bool, hash *string, actor string) (User, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return User{}, e
	}
	defer tx.Rollback()
	old, e := s.User(ctx, tx, username)
	if errors.Is(e, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if e != nil {
		return User{}, e
	}
	v := old
	if role != nil {
		v.Role = *role
	}
	if enabled != nil {
		v.Enabled = *enabled
	}
	if !ValidRole(v.Role) {
		return v, errors.New("invalid role")
	}
	if old.Enabled && old.Role == "admin" && (!v.Enabled || v.Role != "admin") {
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE enabled=1 AND role='admin'").Scan(&n); e != nil {
			return v, e
		}
		if n <= 1 {
			return old, ErrLastAdmin
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE users SET role=?,enabled=? WHERE username=?", v.Role, v.Enabled, username); e != nil {
		return v, e
	}
	// Role change (or disable, or password) revokes sessions; scope change does not.
	if hash != nil {
		if _, e = s.UpdateUserPasswordHash(ctx, tx, username, *hash); e != nil {
			return v, e
		}
	}
	roleOrEnabledChanged := (role != nil || enabled != nil || hash != nil)
	if roleOrEnabledChanged {
		if e = s.DeleteSessionsByUser(ctx, tx, username); e != nil {
			return v, e
		}
	}
	if e = s.AuditTx(ctx, tx, actor, "update_user", username); e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// AssignManagerClient records that a client belongs to a manager. Used when a
// manager creates a client: the assignment is made in the same tx as the draft.
func (s *Store) AssignManagerClient(ctx context.Context, tx *sql.Tx, username, clientID string) error {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	_, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO manager_clients(username,client_id) VALUES(?,?)", username, clientID)
	return e
}
