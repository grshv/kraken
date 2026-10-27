package store

import (
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

func ValidRole(role string) bool { return role == "admin" || role == "viewer" }
func (s *Store) User(tx *sql.Tx, username string) (User, error) {
	v := User{Username: username}
	e := tx.QueryRow("SELECT role,enabled FROM users WHERE username=?", username).Scan(&v.Role, &v.Enabled)
	return v, e
}
func (s *Store) Users() ([]User, error) {
	rows, e := s.DB.Query("SELECT username,role,enabled FROM users ORDER BY username")
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
func (s *Store) CreateUser(username, hash, role, actor string) (User, error) {
	v := User{Username: username, Role: role, Enabled: true}
	if !ValidRole(role) {
		return v, errors.New("invalid role")
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT COUNT(*) FROM users WHERE username=?", username).Scan(&n); e != nil {
		return v, e
	}
	if n > 0 {
		return v, ErrExists
	}
	if _, e = tx.Exec("INSERT INTO users(username,password_hash,role,enabled) VALUES(?,?,?,1)", username, hash, role); e != nil {
		return v, e
	}
	if e = s.AuditTx(tx, actor, "create_user", username); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Store) ChangeUser(username string, role *string, enabled *bool, hash *string, actor string) (User, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return User{}, e
	}
	defer tx.Rollback()
	old, e := s.User(tx, username)
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
		if e = tx.QueryRow("SELECT COUNT(*) FROM users WHERE enabled=1 AND role='admin'").Scan(&n); e != nil {
			return v, e
		}
		if n <= 1 {
			return old, ErrLastAdmin
		}
	}
	if _, e = tx.Exec("UPDATE users SET role=?,enabled=? WHERE username=?", v.Role, v.Enabled, username); e != nil {
		return v, e
	}
	if hash != nil {
		if _, e = s.UpdateUserPasswordHash(tx, username, *hash); e != nil {
			return v, e
		}
	}
	if e = s.DeleteSessionsByUser(tx, username); e != nil {
		return v, e
	}
	if e = s.AuditTx(tx, actor, "update_user", username); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
