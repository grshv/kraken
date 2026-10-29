package admin_test

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/admin"
	"kraken.local/kraken/internal/store"
)

func setup(t *testing.T) (*store.Store, string) {
	t.Helper()
	data := t.TempDir()
	st, e := store.Open(filepath.Join(data, "data", "control.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.DB.Close() })
	ctx := context.Background()
	for _, u := range []struct{ name, role string }{{"root", "admin"}, {"mgr", "manager"}} {
		if _, e := st.CreateUser(ctx, u.name, "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g", u.role, "test"); e != nil {
			t.Fatal(e)
		}
	}
	return st, data
}

func session(t *testing.T, st *store.Store, user, hash string) {
	t.Helper()
	ctx := context.Background()
	tx, _ := st.BeginTx(ctx)
	now := time.Now().Unix()
	if e := st.InsertSession(ctx, tx, hash, user, "csrf", now+3600, now); e != nil {
		t.Fatal(e)
	}
	tx.Commit()
}

func TestAcceptListUsers(t *testing.T) {
	st, _ := setup(t)
	users, e := admin.ListUsers(context.Background(), st)
	if e != nil || len(users) != 2 {
		t.Fatalf("%v %v", users, e)
	}
	for _, u := range users {
		if u.Username == "" || u.Role == "" || !u.Enabled {
			t.Fatalf("bad row %+v", u)
		}
	}
}

func TestAcceptLastAdminProtected(t *testing.T) {
	st, _ := setup(t)
	ctx := context.Background()
	if e := admin.SetEnabled(ctx, st, "root", false, "cli"); !errors.Is(e, admin.ErrLastAdmin) {
		t.Fatalf("disable last admin: %v", e)
	}
	if e := admin.SetRole(ctx, st, "root", "manager", "cli"); !errors.Is(e, admin.ErrLastAdmin) {
		t.Fatalf("demote last admin: %v", e)
	}
	if e := admin.SetRole(ctx, st, "mgr", "admin", "cli"); e != nil {
		t.Fatal(e)
	}
	if e := admin.SetEnabled(ctx, st, "root", false, "cli"); e != nil {
		t.Fatalf("second admin exists, disable must work: %v", e)
	}
	if e := admin.SetRole(ctx, st, "ghost", "admin", "cli"); !errors.Is(e, admin.ErrNotFound) {
		t.Fatalf("unknown user: %v", e)
	}
	if e := admin.SetRole(ctx, st, "mgr", "viewer", "cli"); e == nil {
		t.Fatal("invalid role accepted")
	}
}

func TestAcceptDisableRevokesSessions(t *testing.T) {
	st, _ := setup(t)
	session(t, st, "mgr", "h1")
	if e := admin.SetEnabled(context.Background(), st, "mgr", false, "cli"); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	tx, _ := st.BeginTx(ctx)
	defer tx.Rollback()
	if u, _, _, _, _ := st.LookupSession(ctx, tx, "h1"); u != "" {
		t.Fatal("session of disabled user survived")
	}
}

func TestAcceptRevokeSessions(t *testing.T) {
	st, _ := setup(t)
	session(t, st, "root", "a1")
	session(t, st, "root", "a2")
	session(t, st, "mgr", "m1")
	ctx := context.Background()
	n, e := admin.RevokeSessions(ctx, st, "mgr", "cli")
	if e != nil || n != 1 {
		t.Fatalf("revoke mgr: %d %v", n, e)
	}
	n, e = admin.RevokeSessions(ctx, st, "", "cli")
	if e != nil || n != 2 {
		t.Fatalf("revoke all: %d %v", n, e)
	}
}

func TestAcceptBackupArchive(t *testing.T) {
	st, data := setup(t)
	os.MkdirAll(filepath.Join(data, "certs"), 0700)
	os.WriteFile(filepath.Join(data, "certs", "panel.crt"), []byte("CERT"), 0600)
	os.MkdirAll(filepath.Join(data, "log"), 0700)
	os.WriteFile(filepath.Join(data, "log", "access.log"), []byte("secret log"), 0600)
	out := filepath.Join(t.TempDir(), "b.tar")
	if e := admin.Backup(context.Background(), st, data, out); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	names := map[string][]byte{}
	tr := tar.NewReader(f)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(tr)
		names[h.Name] = b
	}
	if _, ok := names["data/control.sqlite3"]; !ok {
		t.Fatalf("no database in backup: %v", keys(names))
	}
	if string(names["certs/panel.crt"]) != "CERT" {
		t.Fatalf("certs missing: %v", keys(names))
	}
	for n := range names {
		if strings.HasPrefix(n, "log/") || strings.HasSuffix(n, "-wal") || strings.HasSuffix(n, "-shm") {
			t.Fatalf("unexpected entry %s", n)
		}
	}
	restored := filepath.Join(t.TempDir(), "control.sqlite3")
	os.WriteFile(restored, names["data/control.sqlite3"], 0600)
	r, e := store.Open(restored)
	if e != nil {
		t.Fatalf("backup database does not open: %v", e)
	}
	defer r.DB.Close()
	if users, _ := r.Users(context.Background()); len(users) != 2 {
		t.Fatalf("users in backup: %d", len(users))
	}
	if e := admin.Backup(context.Background(), st, data, out); e == nil {
		t.Fatal("backup must not overwrite an existing file")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
