package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"squid-control.local/squid/internal/policy"
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
	m.Command = func(context.Context, ...string) error { return nil }
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
	m.Command = func(ctx context.Context, args ...string) error {
		if args[len(args)-1] == "reconfigure" {
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
	m.Command = func(ctx context.Context, args ...string) error {
		if args[len(args)-1] == "reconfigure" {
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
