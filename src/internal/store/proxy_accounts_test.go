package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"squid-control.local/squid/internal/credentials"
	"strings"
	"testing"
)

func TestProxyAccountsHashOnlyAndAtomicAudit(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "accounts.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	hash, e := credentials.Hash("test-only-proxy-password")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CreateProxyAccount("build", hash, "admin"); e != nil {
		t.Fatal(e)
	}
	rows, e := s.ProxyAccounts()
	if e != nil || len(rows) != 1 || rows[0].Username != "build" || !rows[0].Enabled {
		t.Fatal("public list failed", e)
	}
	if e = s.CreateProxyAccount("build", hash, "admin"); !errors.Is(e, ErrProxyAccountExists) {
		t.Fatal("duplicate accepted")
	}
	_, e = s.DB.Exec(`CREATE TRIGGER fail_proxy_audit BEFORE INSERT ON audit_events WHEN NEW.action='reset_proxy_password' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END;`)
	if e != nil {
		t.Fatal(e)
	}
	next, e := credentials.Hash("replacement-test-password")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ChangeProxyAccount("build", nil, &next, "admin"); e == nil {
		t.Fatal("missing audit accepted")
	}
	var current string
	if e = s.DB.QueryRow("SELECT password_hash FROM proxy_accounts WHERE username='build'").Scan(&current); e != nil || current != hash {
		t.Fatal("failed change did not rollback")
	}
	_, _ = s.DB.Exec("DROP TRIGGER fail_proxy_audit")
	off := false
	if e = s.ChangeProxyAccount("build", &off, &next, "admin"); e != nil {
		t.Fatal(e)
	}
	account, e := s.ProxyAccount("build")
	if e != nil || account.Enabled {
		t.Fatal("disable not saved")
	}
	var stored string
	if e = s.DB.QueryRow("SELECT password_hash||username FROM proxy_accounts WHERE username='build'").Scan(&stored); e != nil || strings.Contains(stored, "replacement-test-password") {
		t.Fatal("plaintext persisted")
	}
	if e = s.ChangeProxyAccount("unknown", &off, nil, "admin"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("unknown mutation accepted")
	}
	var actions int
	if e = s.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE entity_id='build'").Scan(&actions); e != nil || actions != 2 {
		t.Fatal("safe audit missing", actions, e)
	}
}
