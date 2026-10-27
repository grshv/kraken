package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"squid-control.local/squid/internal/policy"
)

func TestStorageFailuresPreserveRecoverableState(t *testing.T) {
	for _, phase := range []string{"candidate.conf", "journal.json", "active.conf", "lkg.json"} {
		t.Run(phase, func(t *testing.T) {
			m := testManager(t)
			before, _ := m.ReadManifest()
			failed := false
			m.Persist = func(path string, b []byte) error {
				if filepath.Base(path) == phase && !failed {
					failed = true
					return syscall.ENOSPC
				}
				return Atomic(path, b)
			}
			next := policy.Empty()
			next.TLSProfiles[0].Name = "Changed"
			out, e := m.Apply(context.Background(), "operation", "revision", "", next)
			if e == nil || out == "succeeded" {
				t.Fatal("disk-full reported success")
			}
			after, _ := m.ReadManifest()
			if after.Hash != before.Hash {
				t.Fatal("disk-full destroyed LKG")
			}
			active, _ := os.ReadFile(m.ActivePath())
			if string(active) != before.Config {
				t.Fatal("failed write changed active config")
			}
		})
	}
}
func TestProcessKillRecoveryAtDurableBoundaries(t *testing.T) {
	for _, phase := range []string{"promoting", "active.conf", "lkg.json"} {
		t.Run(phase, func(t *testing.T) {
			m := testManager(t)
			before, _ := m.ReadManifest()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeCrashChild$")
			cmd.Env = append(os.Environ(), "SC_TEST_CRASH_DIR="+m.Dir, "SC_TEST_CRASH_PHASE="+phase)
			e := cmd.Run()
			var exited *exec.ExitError
			if !errors.As(e, &exited) {
				t.Fatalf("child did not terminate: %v", e)
			}
			if e = m.Recover(context.Background(), true); e != nil {
				t.Fatal(e)
			}
			now, _ := m.ReadManifest()
			if now.Hash != before.Hash {
				t.Fatal("killed candidate became LKG")
			}
			active, _ := os.ReadFile(m.ActivePath())
			if string(active) != before.Config {
				t.Fatal("pending candidate survived SIGKILL")
			}
		})
	}
}
func TestRuntimeCrashChild(t *testing.T) {
	dir := os.Getenv("SC_TEST_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	phase := os.Getenv("SC_TEST_CRASH_PHASE")
	m := New(dir, "/unused", "/dev/null", "", "")
	m.Command = func(context.Context, ...string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	m.Persist = func(path string, b []byte) error {
		if e := Atomic(path, b); e != nil {
			return e
		}
		hit := filepath.Base(path) == phase
		if phase == "promoting" && strings.HasSuffix(path, "journal.json") {
			var j Journal
			json.Unmarshal(b, &j)
			hit = j.Stage == "promoting"
		}
		if hit {
			syscall.Kill(os.Getpid(), syscall.SIGKILL)
		}
		return nil
	}
	next := policy.Empty()
	next.TLSProfiles[0].Name = "Changed in killed process"
	m.Apply(context.Background(), "killed-operation", "candidate", "", next)
	t.Fatal("did not reach requested crash boundary")
}

func TestRollbackSurvivesCancelledOperationContext(t *testing.T) {
	m := testManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reloads := 0
	m.Command = func(c context.Context, args ...string) error {
		if args[len(args)-1] == "reconfigure" {
			reloads++
			if reloads == 1 {
				cancel()
				return context.Canceled
			}
			return c.Err()
		}
		return nil
	}
	out, e := m.Apply(ctx, "operation", "revision", "", policy.Empty())
	if e == nil || out != "rolled_back" || reloads != 2 {
		t.Fatalf("rollback cancelled together with operation: %s %v", out, e)
	}
}
