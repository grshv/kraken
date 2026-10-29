package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"kraken.local/kraken/internal/policy"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
	m.AppDir = t.TempDir()
	m.Command = func(context.Context, ...string) error { return nil }
	m.Reload = func(context.Context, string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	return m
}
func TestApplyAndVerifiedRollback(t *testing.T) {
	m := testManager(t)
	prev, _ := m.ReadManifest()
	s := policy.Empty()
	s.TLSProfiles[0].Name = "Changed"
	calls := 0
	m.Reload = func(ctx context.Context, action string) error {
		if action == "reconfigure" {
			calls++
			if calls == 1 {
				return errors.New("injected reload failure")
			}
		}
		return nil
	}
	out, e := m.Apply(context.Background(), "operation", "revision", "", s)
	if e == nil || out != "rolled_back" {
		t.Fatalf("%s %v", out, e)
	}
	now, _ := m.ReadManifest()
	if now.Hash != prev.Hash {
		t.Fatal("rollback lost LKG")
	}
	if calls != 2 {
		t.Fatal("rollback did not reload")
	}
	j, _ := m.Journal()
	if j.Outcome != "rolled_back" {
		t.Fatal("outcome not durable")
	}
}
func TestCrashRecoveryUsesPrevious(t *testing.T) {
	m := testManager(t)
	prev, _ := m.ReadManifest()
	next := prev
	next.Config = "invalid candidate"
	j := Journal{Operation: "crashed", Stage: "reloading", Previous: prev, Candidate: next}
	if e := m.write("journal.json", j); e != nil {
		t.Fatal(e)
	}
	Atomic(m.ActivePath(), []byte(next.Config))
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(m.ActivePath())
	if string(b) != prev.Config {
		t.Fatal("pending candidate survived restart")
	}
}
func TestParseFailureDoesNotPromote(t *testing.T) {
	m := testManager(t)
	prev, _ := m.ReadManifest()
	m.Command = func(context.Context, ...string) error { return errors.New("invalid configuration") }
	out, e := m.Apply(context.Background(), "operation", "revision", "", policy.Empty())
	if e == nil || out != "rejected" {
		t.Fatalf("%s %v", out, e)
	}
	b, _ := os.ReadFile(m.ActivePath())
	if string(b) != prev.Config {
		t.Fatal("active config changed on parse failure")
	}
}
func TestRollbackFailureRemainsRecoverable(t *testing.T) {
	m := testManager(t)
	m.Reload = func(ctx context.Context, action string) error {
		if action == "reconfigure" {
			return errors.New("injected failure")
		}
		return nil
	}
	out, e := m.Apply(context.Background(), "operation", "revision", "", policy.Empty())
	if e == nil || out != "degraded" {
		t.Fatalf("%s %v", out, e)
	}
	j, _ := m.Journal()
	if j.Stage == "terminal" {
		t.Fatal("failed rollback reported terminal success")
	}
}
func TestDriftAndConcurrentGate(t *testing.T) {
	m := testManager(t)
	unlock, e := m.lock()
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Recover(context.Background(), true); !errors.Is(e, ErrBusy) {
		t.Fatal("lock not enforced")
	}
	unlock()
	Atomic(m.ActivePath(), []byte("manual change"))
	if e = m.Recover(context.Background(), true); !errors.Is(e, ErrDrift) {
		t.Fatalf("drift ignored: %v", e)
	}
}

func writeFakeSquid(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-squid")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\necho '"+body+"' >&2\nexit 1\n"), 0755); e != nil {
		t.Fatal(e)
	}
	return bin
}

func TestParseErrorReportsLineWithoutOutput(t *testing.T) {
	bin := writeFakeSquid(t, "FATAL: Bungled /x/candidate.conf line 42: SECRET_TOKEN_VALUE")
	m := New(t.TempDir(), bin, "/dev/null", "", "", "")
	m.AppDir = t.TempDir()
	m.Probe = func(context.Context, string) error { return nil }
	m.Command = func(context.Context, ...string) error { return nil }
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	m.Command = m.command
	err := m.Validate(context.Background(), policy.Empty())
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T %v", err, err)
	}
	if pe.Line != 42 {
		t.Fatalf("expected line 42, got %d", pe.Line)
	}
	if strings.Contains(err.Error(), "SECRET_TOKEN_VALUE") {
		t.Fatalf("error leaks squid output: %v", err)
	}
	if strings.Contains(pe.Error(), "SECRET_TOKEN_VALUE") {
		t.Fatalf("ParseError leaks squid output: %v", pe)
	}
}

func TestParseErrorWithoutLineNumber(t *testing.T) {
	bin := writeFakeSquid(t, "FATAL: bungled without a line marker")
	m := New(t.TempDir(), bin, "/dev/null", "", "", "")
	m.AppDir = t.TempDir()
	m.Probe = func(context.Context, string) error { return nil }
	m.Command = func(context.Context, ...string) error { return nil }
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	m.Command = m.command
	err := m.Validate(context.Background(), policy.Empty())
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Line != 0 {
		t.Fatalf("expected ParseError line 0, got %v", err)
	}
}

func TestSquidReadyBranches(t *testing.T) {
	// terminal: ready
	m := testManager(t)
	if ready, e := m.SquidReady(); e != nil || !ready {
		t.Fatalf("terminal expected ready, got %v %v", ready, e)
	}
	prev, _ := m.ReadManifest()

	// no lkg: (false, nil)
	os.Remove(filepath.Join(m.Dir, "lkg.json"))
	if ready, e := m.SquidReady(); e != nil || ready {
		t.Fatalf("no lkg expected (false,nil), got %v %v", ready, e)
	}

	// active != lkg.Config: (false, nil)
	Atomic(m.ActivePath(), []byte("drifted config"))
	Atomic(filepath.Join(m.Dir, "lkg.json"), mustJSON(prev))
	if ready, e := m.SquidReady(); e != nil || ready {
		t.Fatalf("drifted active expected (false,nil), got %v %v", ready, e)
	}

	// restore consistent active
	Atomic(m.ActivePath(), []byte(prev.Config))

	// journal not terminal, previous != lkg.Hash: (false, nil)
	other := prev
	other.Hash = "different"
	Atomic(filepath.Join(m.Dir, "journal.json"), mustJSON(Journal{Stage: "reloading", Previous: other, Candidate: prev}))
	if ready, e := m.SquidReady(); e != nil || ready {
		t.Fatalf("non-terminal previous mismatch expected (false,nil), got %v %v", ready, e)
	}

	// journal not terminal, previous.Hash == lkg.Hash: (true, nil)
	Atomic(filepath.Join(m.Dir, "journal.json"), mustJSON(Journal{Stage: "reloading", Previous: prev, Candidate: prev}))
	if ready, e := m.SquidReady(); e != nil || !ready {
		t.Fatalf("non-terminal previous match expected (true,nil), got %v %v", ready, e)
	}

	// terminal: (true, nil)
	Atomic(filepath.Join(m.Dir, "journal.json"), mustJSON(Journal{Stage: "terminal", Previous: prev, Candidate: prev}))
	if ready, e := m.SquidReady(); e != nil || !ready {
		t.Fatalf("terminal expected (true,nil), got %v %v", ready, e)
	}
}

func TestReloadFakeUsedByApplyAndMaintain(t *testing.T) {
	m := testManager(t)
	applied := []string{}
	m.Reload = func(_ context.Context, action string) error {
		applied = append(applied, action)
		return nil
	}
	if _, e := m.Apply(context.Background(), "op", "rev", "", policy.Empty()); e != nil {
		t.Fatal(e)
	}
	if !containsString(applied, "reconfigure") {
		t.Fatalf("Apply did not invoke Reload(reconfigure): %v", applied)
	}
	logDir := t.TempDir()
	logPath := filepath.Join(logDir, "access.log")
	if e := os.WriteFile(logPath, make([]byte, 11*1024*1024), 0640); e != nil {
		t.Fatal(e)
	}
	m.Options.LogPath = logPath
	applied = nil
	if e := m.Maintain(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if !containsString(applied, "rotate") {
		t.Fatalf("Maintain did not invoke Reload(rotate): %v", applied)
	}
}

func TestRealReloadAndTokenLeakage(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:3131")
	if e != nil {
		t.Skipf("cannot bind 3131: %v", e)
	}
	defer ln.Close()
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				req := string(buf[:n])
				if strings.HasPrefix(req, "GET cache_object://127.0.0.1/reconfigure@") {
					io.WriteString(c, "HTTP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n")
				}
			}(c)
		}
	}()
	m := testManager(t)
	m.Reload = m.defaultReload
	if e := m.Reload(context.Background(), "reconfigure"); e != nil {
		t.Fatalf("real reload against fake listener failed: %v", e)
	}
	ln.Close()

	token, _ := os.ReadFile(filepath.Join(m.AppDir, "manager-token"))
	e = m.Reload(context.Background(), "reconfigure")
	if e == nil {
		t.Fatal("expected reload failure after listener closed")
	}
	if len(token) > 0 && strings.Contains(e.Error(), string(token)) {
		t.Fatalf("reload error leaked token: %v", e)
	}
}

func TestManagerTokenAndIncludeModes(t *testing.T) {
	m := testManager(t)
	tokPath := filepath.Join(m.AppDir, "manager-token")
	st, e := os.Stat(tokPath)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("manager-token mode %o", st.Mode().Perm())
	}
	incPath := filepath.Join(m.Dir, "manager-auth.conf")
	st, e = os.Stat(incPath)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm() != 0640 {
		t.Fatalf("manager-auth.conf mode %o", st.Mode().Perm())
	}
	body, _ := os.ReadFile(incPath)
	if !strings.HasPrefix(string(body), "cachemgr_passwd ") || !strings.HasSuffix(string(body), " reconfigure rotate\n") {
		t.Fatalf("manager-auth.conf content unexpected: %s", body)
	}
}

func containsString(items []string, want string) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}

func mustJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
