package proxyauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squid-control.local/squid/internal/credentials"
)

func exportAnswer(t *testing.T, path, line string, state bool) string {
	t.Helper()
	var out bytes.Buffer
	var e error
	if state {
		e = RunState(path, strings.NewReader(line+"\n"), &out)
	} else {
		e = Run(path, strings.NewReader(line+"\n"), &out)
	}
	if e != nil {
		t.Fatal(e)
	}
	return strings.TrimSpace(out.String())
}

func TestExportDrivesHelpers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy-accounts.json")
	password := "correct horse battery"
	hash, e := credentials.Hash(password)
	if e != nil {
		t.Fatal(e)
	}
	if e = WriteExport(path, []Account{{Username: "office.1", PasswordHash: hash}}); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("export mode %v %v", info.Mode().Perm(), e)
	}
	sum := sha256.Sum256([]byte(hash))
	gen := hex.EncodeToString(sum[:])
	line := "office.1 " + url.PathEscape(password)
	if got := exportAnswer(t, path, line, false); got != "OK sc_generation_="+gen {
		t.Fatalf("valid credentials -> %q", got)
	}
	if got := exportAnswer(t, path, "office.1 "+url.PathEscape("wrong password!!"), false); got != "ERR" {
		t.Fatalf("wrong password -> %q", got)
	}
	if got := exportAnswer(t, path, "nobody.x "+url.PathEscape(password), false); got != "ERR" {
		t.Fatalf("unknown user -> %q", got)
	}
	if got := exportAnswer(t, path, "office.1 "+gen+" -", true); got != "OK" {
		t.Fatalf("state current generation -> %q", got)
	}
	// Disabled accounts are absent from the export.
	if e = WriteExport(path, nil); e != nil {
		t.Fatal(e)
	}
	if got := exportAnswer(t, path, line, false); got != "ERR" {
		t.Fatalf("disabled account -> %q", got)
	}
	if got := exportAnswer(t, path, "office.1 "+gen+" -", true); got != "ERR" {
		t.Fatalf("state after disable -> %q", got)
	}
	// Password reset changes the generation.
	other, _ := credentials.Hash("another password 1")
	if e = WriteExport(path, []Account{{Username: "office.1", PasswordHash: other}}); e != nil {
		t.Fatal(e)
	}
	if got := exportAnswer(t, path, "office.1 "+gen+" -", true); got != "ERR" {
		t.Fatalf("stale generation -> %q", got)
	}
	// Missing and corrupt export deny.
	if got := exportAnswer(t, filepath.Join(dir, "missing.json"), line, false); got != "ERR" {
		t.Fatalf("missing export -> %q", got)
	}
	os.WriteFile(path, []byte("{broken"), 0o640)
	if got := exportAnswer(t, path, line, false); got != "ERR" {
		t.Fatalf("corrupt export -> %q", got)
	}
}
