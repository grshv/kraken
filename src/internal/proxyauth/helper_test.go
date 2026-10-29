package proxyauth

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"squid-control.local/squid/internal/credentials"
	"strings"
	"testing"
)

func exportPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "proxy-accounts.json")
}

func writeAccounts(t *testing.T, path string, accounts ...Account) {
	t.Helper()
	if e := WriteExport(path, accounts); e != nil {
		t.Fatal(e)
	}
}

func emptyExport(t *testing.T, path string) {
	t.Helper()
	if e := WriteExport(path, nil); e != nil {
		t.Fatal(e)
	}
}

func TestHelperProtocolAndURLUnescape(t *testing.T) {
	path := exportPath(t)
	password := "spaces + percent% password"
	hash, e := credentials.Hash(password)
	if e != nil {
		t.Fatal(e)
	}
	writeAccounts(t, path, Account{Username: "Build.Agent", PasswordHash: hash})
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

func TestMissingExportFailsClosedAndDoesNotCreateFile(t *testing.T) {
	path := exportPath(t)
	var output bytes.Buffer
	if e := Run(path, strings.NewReader("build test-only-password\n"), &output); e != nil {
		t.Fatal(e)
	}
	if output.String() != "ERR\n" {
		t.Fatal("missing export granted access")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("helper created missing export")
	}
}

func TestCorruptExportFailsClosed(t *testing.T) {
	path := exportPath(t)
	if e := os.WriteFile(path, []byte("{not json"), 0640); e != nil {
		t.Fatal(e)
	}
	var output bytes.Buffer
	if e := Run(path, strings.NewReader("build test-only-password\n"), &output); e != nil {
		t.Fatal(e)
	}
	if output.String() != "ERR\n" {
		t.Fatal("corrupt export granted access")
	}
}

func TestDisabledAccountIsRejected(t *testing.T) {
	path := exportPath(t)
	password := "test-only-proxy-password"
	hash, e := credentials.Hash(password)
	if e != nil {
		t.Fatal(e)
	}
	// Enabled accounts live in the export; disabled ones are absent.
	writeAccounts(t, path, Account{Username: "build", PasswordHash: hash})
	var out bytes.Buffer
	if e := Run(path, strings.NewReader("build "+password+"\n"), &out); e != nil || out.String() != "OK sc_generation_="+generation(hash)+"\n" {
		t.Fatal("enabled account rejected", out.String(), e)
	}
	emptyExport(t, path) // disabled accounts are absent from the export
	out.Reset()
	if e := Run(path, strings.NewReader("build "+password+"\n"), &out); e != nil || out.String() != "ERR\n" {
		t.Fatal("disabled identity accepted")
	}
}

func TestPasswordResetObservedWithoutRestart(t *testing.T) {
	path := exportPath(t)
	old, e := credentials.Hash("old-test-password")
	if e != nil {
		t.Fatal(e)
	}
	newHash, e := credentials.Hash("new-test-password")
	if e != nil {
		t.Fatal(e)
	}
	writeAccounts(t, path, Account{Username: "build", PasswordHash: old})
	if credentialGeneration(path, "build old-test-password", old, credentials.Matches) == "" {
		t.Fatal("initial credential rejected")
	}
	writeAccounts(t, path, Account{Username: "build", PasswordHash: newHash})
	if credentialGeneration(path, "build old-test-password", old, credentials.Matches) != "" {
		t.Fatal("old password accepted after reset")
	}
	if credentialGeneration(path, "build new-test-password", old, credentials.Matches) == "" {
		t.Fatal("new password rejected after reset")
	}
}

func TestInFlightVerificationCannotBypassResetOrDisable(t *testing.T) {
	for _, action := range []string{"reset", "disable"} {
		t.Run(action, func(t *testing.T) {
			path := exportPath(t)
			old, e := credentials.Hash("old-test-password")
			if e != nil {
				t.Fatal(e)
			}
			next, e := credentials.Hash("new-test-password")
			if e != nil {
				t.Fatal(e)
			}
			writeAccounts(t, path, Account{Username: "build", PasswordHash: old})
			changed := false
			generation := credentialGeneration(path, "build old-test-password", old, func(hash, password string) bool {
				if !credentials.Matches(hash, password) {
					return false
				}
				changed = true
				if action == "reset" {
					writeAccounts(t, path, Account{Username: "build", PasswordHash: next})
				} else {
					emptyExport(t, path) // disable: empty export
				}
				return true
			})
			if !changed || generation != "" {
				t.Fatal("in-flight credential survived change")
			}
		})
	}
}

func TestStateGuardRejectsCachedCredentialsAfterResetOrDisable(t *testing.T) {
	path := exportPath(t)
	old, _ := credentials.Hash("old-fixture-password")
	next, _ := credentials.Hash("new-fixture-password")
	writeAccounts(t, path, Account{Username: "build", PasswordHash: old})
	request := "build " + generation(old) + " -"
	if !checkState(path, request) {
		t.Fatal("current generation rejected")
	}
	writeAccounts(t, path, Account{Username: "build", PasswordHash: next})
	if checkState(path, request) {
		t.Fatal("cached old verifier bypassed reset")
	}
	request = "build " + generation(next) + " -"
	if !checkState(path, request) {
		t.Fatal("new generation rejected")
	}
	emptyExport(t, path) // disable
	var out bytes.Buffer
	if e := RunState(path, strings.NewReader(request+"\n"), &out); e != nil || out.String() != "ERR\n" {
		t.Fatal("cached disabled identity accepted")
	}
}

func TestWriteExportIsGroupReadableAndAtomic(t *testing.T) {
	path := exportPath(t)
	hash, _ := credentials.Hash("test-only-proxy-password")
	if e := WriteExport(path, []Account{{Username: "build", PasswordHash: hash}}); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm() != 0640 {
		t.Fatalf("export mode %o", st.Mode().Perm())
	}
	var f exportFile
	b, _ := os.ReadFile(path)
	if e := json.Unmarshal(b, &f); e != nil || f.Version != 1 || len(f.Accounts) != 1 || f.Accounts[0].Username != "build" {
		t.Fatalf("export contents: %s", b)
	}
}
