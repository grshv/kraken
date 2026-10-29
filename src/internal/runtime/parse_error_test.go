package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"squid-control.local/squid/internal/policy"
)

func fakeSquid(t *testing.T, script string) *Manager {
	dir := t.TempDir()
	bin := filepath.Join(dir, "squid")
	if e := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); e != nil {
		t.Fatal(e)
	}
	return New(filepath.Join(dir, "state"), bin, filepath.Join(dir, "access.log"), "", "", "")
}

func TestParseErrorUsesLastReportedLine(t *testing.T) {
	m := fakeSquid(t, "echo 'ERROR: x line 7: first' >&2\necho 'FATAL: Bungled /x/candidate.conf line 42: SECRET_TOKEN_VALUE' >&2\nexit 1\n")
	e := m.Validate(context.Background(), policy.Empty())
	var pe *ParseError
	if !errors.As(e, &pe) || pe.Line != 42 {
		t.Fatalf("want ParseError line 42, got %v", e)
	}
	if strings.Contains(e.Error(), "SECRET_TOKEN_VALUE") || strings.Contains(e.Error(), "first") {
		t.Fatalf("squid output leaked: %v", e)
	}
	if !strings.Contains(pe.Error(), "line 42") {
		t.Fatalf("message lacks line: %v", pe.Error())
	}
}

func TestParseErrorUnknownLineWithheld(t *testing.T) {
	m := fakeSquid(t, "echo 'FATAL: something SECRET_TOKEN_VALUE' >&2\nexit 1\n")
	e := m.Validate(context.Background(), policy.Empty())
	var pe *ParseError
	if !errors.As(e, &pe) || pe.Line != 0 {
		t.Fatalf("want ParseError line 0, got %v", e)
	}
	if strings.Contains(e.Error(), "SECRET") {
		t.Fatal("leak")
	}
}

func TestParseErrorFromCommandPropagates(t *testing.T) {
	m := fakeSquid(t, "exit 0\n")
	m.Command = func(context.Context, ...string) error { return &ParseError{Line: 5} }
	e := m.Validate(context.Background(), policy.Empty())
	var pe *ParseError
	if !errors.As(e, &pe) || pe.Line != 5 {
		t.Fatalf("fake ParseError lost: %v", e)
	}
}
