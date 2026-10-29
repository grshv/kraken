package store

import (
	"context"
	"errors"
	"time"

	"kraken.local/kraken/internal/proxyauth"
)

type ProxyAccount struct {
	Username  string  `json:"username"`
	Enabled   bool    `json:"enabled"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	Owner     *string `json:"owner,omitempty"`
}

var ErrProxyAccountExists = errors.New("proxy account already exists")

func (s *Store) ProxyAccounts() ([]ProxyAccount, error) {
	return s.ProxyAccountsContext(context.Background())
}
func (s *Store) ProxyAccountsContext(ctx context.Context) ([]ProxyAccount, error) {
	rows, e := s.Read.QueryContext(ctx, "SELECT username,enabled,created_at,updated_at,owner FROM proxy_accounts ORDER BY username LIMIT 1024")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []ProxyAccount{}
	for rows.Next() {
		var v ProxyAccount
		var created, updated int64
		if e = rows.Scan(&v.Username, &v.Enabled, &created, &updated, &v.Owner); e != nil {
			return nil, e
		}
		v.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
		v.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CreateProxyAccount(username, hash, owner, actor string) error {
	return s.CreateProxyAccountContext(context.Background(), username, hash, owner, actor)
}
func (s *Store) CreateProxyAccountContext(ctx context.Context, username, hash, owner, actor string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRow("SELECT COUNT(*) FROM proxy_accounts WHERE username=?", username).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return ErrProxyAccountExists
	}
	if e = tx.QueryRow("SELECT COUNT(*) FROM proxy_accounts").Scan(&count); e != nil {
		return e
	}
	if count >= 1024 {
		return errors.New("proxy account capacity reached")
	}
	now := time.Now().Unix()
	ownerArg := interface{}(nil)
	if owner != "" {
		ownerArg = owner
	}
	if _, e = tx.Exec("INSERT INTO proxy_accounts(username,password_hash,enabled,created_at,updated_at,owner) VALUES(?,?,1,?,?,?)", username, hash, now, now, ownerArg); e != nil {
		return e
	}
	if e = s.AuditTx(tx, actor, "create_proxy_account", username); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ChangeProxyAccount(username string, enabled *bool, hash *string, actor string) error {
	return s.ChangeProxyAccountContext(context.Background(), username, enabled, hash, actor)
}
func (s *Store) ChangeProxyAccountContext(ctx context.Context, username string, enabled *bool, hash *string, actor string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var found string
	if e = tx.QueryRow("SELECT username FROM proxy_accounts WHERE username=?", username).Scan(&found); e != nil {
		return e
	}
	if enabled != nil {
		if _, e = tx.Exec("UPDATE proxy_accounts SET enabled=?,updated_at=? WHERE username=?", *enabled, time.Now().Unix(), username); e != nil {
			return e
		}
	}
	if hash != nil {
		if _, e = tx.Exec("UPDATE proxy_accounts SET password_hash=?,updated_at=? WHERE username=?", *hash, time.Now().Unix(), username); e != nil {
			return e
		}
	}
	action := "update_proxy_account"
	if hash != nil {
		action = "reset_proxy_password"
	}
	if e = s.AuditTx(tx, actor, action, username); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ProxyAccount(username string) (ProxyAccount, error) {
	return s.ProxyAccountContext(context.Background(), username)
}
func (s *Store) ProxyAccountContext(ctx context.Context, username string) (ProxyAccount, error) {
	var v ProxyAccount
	var created, updated int64
	e := s.Read.QueryRowContext(ctx, "SELECT username,enabled,created_at,updated_at,owner FROM proxy_accounts WHERE username=?", username).Scan(&v.Username, &v.Enabled, &created, &updated, &v.Owner)
	if e != nil {
		return v, e
	}
	v.CreatedAt = time.Unix(created, 0).UTC().Format(time.RFC3339)
	v.UpdatedAt = time.Unix(updated, 0).UTC().Format(time.RFC3339)
	return v, nil
}

// EnabledProxyAccounts returns the proxy-auth verifiers the helper is allowed
// to read. Only enabled accounts are exported; the panel-side hash is secret
// but safe to share with Squid (a verification hash, not the password).
func (s *Store) EnabledProxyAccounts() ([]proxyauth.Account, error) {
	return s.EnabledProxyAccountsContext(context.Background())
}
func (s *Store) EnabledProxyAccountsContext(ctx context.Context) ([]proxyauth.Account, error) {
	rows, e := s.Read.QueryContext(ctx, "SELECT username,password_hash FROM proxy_accounts WHERE enabled=1 ORDER BY username LIMIT 1024")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []proxyauth.Account{}
	for rows.Next() {
		var v proxyauth.Account
		if e = rows.Scan(&v.Username, &v.PasswordHash); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
