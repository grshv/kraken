package proxyauth

import (
	"bytes"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"squid-control.local/squid/internal/credentials"
	"strings"
	"testing"
)

func helperDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	_, e = db.Exec("CREATE TABLE proxy_accounts(username TEXT PRIMARY KEY,password_hash TEXT NOT NULL,enabled INTEGER NOT NULL)")
	if e != nil {
		t.Fatal(e)
	}
	return path, db
}
func TestHelperProtocolAndURLUnescape(t *testing.T) {
	path, db := helperDatabase(t)
	password := "spaces + percent% password"
	hash, e := credentials.Hash(password)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("INSERT INTO proxy_accounts VALUES('Build.Agent',?,1)", hash)
	if e != nil {
		t.Fatal(e)
	}
	input := "Build.Agent " + url.PathEscape(password) + "\nBuild.Agent invalid-password\nunknown " + url.PathEscape(password) + "\nbuild.agent " + url.PathEscape(password) + "\nmalformed\n" + strings.Repeat("x", MaxLine+20) + "\nBuild.Agent " + url.PathEscape(password) + "\n"
	var output bytes.Buffer
	if e = Run(path, strings.NewReader(input), &output); e != nil {
		t.Fatal(e)
	}
	if output.String() != "OK sc_generation_="+generation(hash)+"\nERR\nERR\nERR\nERR\nERR\nOK sc_generation_="+generation(hash)+"\n" {
		t.Fatalf("protocol mismatch: %q", output.String())
	}
	if strings.Contains(output.String(), password) {
		t.Fatal("helper leaked password")
	}
}
func TestHelperSeesDisableAndResetWithoutRestart(t *testing.T) {
	path, db := helperDatabase(t)
	old, e := credentials.Hash("old-test-password")
	if e != nil {
		t.Fatal(e)
	}
	newHash, e := credentials.Hash("new-test-password")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = db.Exec("INSERT INTO proxy_accounts VALUES('build',?,1)", old)
	// Exercise the read-only connection repeatedly across mutations.
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=query_only(1)"
	ro, e := sql.Open("sqlite", u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer ro.Close()
	if !check(ro, "build old-test-password", old) {
		t.Fatal("initial credential rejected")
	}
	_, _ = db.Exec("UPDATE proxy_accounts SET enabled=0")
	if check(ro, "build old-test-password", old) {
		t.Fatal("disabled credential accepted")
	}
	_, _ = db.Exec("UPDATE proxy_accounts SET enabled=1,password_hash=?", newHash)
	if check(ro, "build old-test-password", old) || !check(ro, "build new-test-password", old) {
		t.Fatal("password reset not observed")
	}
}
func TestMissingStoreFailsClosedAndDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	var output bytes.Buffer
	if e := Run(path, strings.NewReader("build test-only-password\n"), &output); e != nil {
		t.Fatal(e)
	}
	if output.String() != "ERR\n" {
		t.Fatal("missing store granted access")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("helper created missing database")
	}
}

func TestInFlightVerificationCannotBypassResetOrDisable(t *testing.T) {
	for _, action := range []string{"reset", "disable"} {
		t.Run(action, func(t *testing.T) {
			_, db := helperDatabase(t)
			old, e := credentials.Hash("old-test-password")
			if e != nil {
				t.Fatal(e)
			}
			next, e := credentials.Hash("new-test-password")
			if e != nil {
				t.Fatal(e)
			}
			_, _ = db.Exec("INSERT INTO proxy_accounts VALUES('build',?,1)", old)
			changed := false
			allowed := checkWithVerifier(db, "build old-test-password", old, func(hash, password string) bool {
				if !credentials.Matches(hash, password) {
					return false
				}
				changed = true
				var e error
				if action == "reset" {
					_, e = db.Exec("UPDATE proxy_accounts SET password_hash=?", next)
				} else {
					_, e = db.Exec("UPDATE proxy_accounts SET enabled=0")
				}
				if e != nil {
					t.Fatal(e)
				}
				return true
			})
			if !changed || allowed {
				t.Fatal("in-flight credential survived change")
			}
		})
	}
}

func TestStateGuardRejectsCachedCredentialsAfterResetOrDisable(t *testing.T) {
	path, db := helperDatabase(t)
	old, _ := credentials.Hash("old-fixture-password")
	next, _ := credentials.Hash("new-fixture-password")
	_, _ = db.Exec("INSERT INTO proxy_accounts VALUES('build',?,1)", old)
	request := "build " + generation(old) + " -"
	if !checkState(db, request) {
		t.Fatal("current generation rejected")
	}
	_, _ = db.Exec("UPDATE proxy_accounts SET password_hash=?", next)
	if checkState(db, request) {
		t.Fatal("cached old verifier bypassed reset")
	}
	request = "build " + generation(next) + " -"
	if !checkState(db, request) {
		t.Fatal("new generation rejected")
	}
	_, _ = db.Exec("UPDATE proxy_accounts SET enabled=0")
	var out bytes.Buffer
	if e := RunState(path, strings.NewReader(request+"\n"), &out); e != nil || out.String() != "ERR\n" {
		t.Fatal("cached disabled identity accepted")
	}
}
