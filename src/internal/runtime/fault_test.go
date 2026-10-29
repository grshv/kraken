package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"kraken.local/kraken/internal/policy"
)

// fileState is a durable, file-backed StateStore for the cross-process crash
// test. In-process tests use memState; only the SIGKILL scenario needs state to
// survive process death, which an in-memory store cannot provide.
type fileState struct{ dir string }

func (s *fileState) LoadState(_ context.Context, key string, v any) (bool, error) {
	b, e := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return true, json.Unmarshal(b, v)
}

func (s *fileState) SaveState(_ context.Context, key string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return Atomic(filepath.Join(s.dir, key+".json"), b)
}

// enoscState fails the first SaveState matching phase with ENOSPC, sharing the
// failed flag with the Persist injector so only one failure fires per run.
type enoscState struct {
	inner  *memState
	phase  string
	failed *bool
	mu     *sync.Mutex
}

// phaseKey maps a test phase label to the control-DB state key it targets.
// "promoting" is a journal stage, not a key, so it maps to the journal key;
// the stage check in killState/enoscState ensures the failure fires only on
// the "promoting" entry rather than an earlier first_run/terminal write.
func phaseKey(phase string) string {
	switch phase {
	case stateJournal, "promoting":
		return stateJournal
	case stateLastGood:
		return stateLastGood
	}
	return ""
}

func (s *enoscState) LoadState(ctx context.Context, key string, v any) (bool, error) {
	return s.inner.LoadState(ctx, key, v)
}

func (s *enoscState) SaveState(ctx context.Context, key string, v any) error {
	s.mu.Lock()
	if key == phaseKey(s.phase) && !*s.failed {
		*s.failed = true
		s.mu.Unlock()
		return syscall.ENOSPC
	}
	s.mu.Unlock()
	return s.inner.SaveState(ctx, key, v)
}

func faultManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
	m.State = newMemState()
	m.Command = func(context.Context, ...string) error { return nil }
	m.Reload = func(context.Context, string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestStorageFailuresPreserveRecoverableState(t *testing.T) {
	for _, phase := range []string{"candidate.conf", stateJournal, "squid.conf", stateLastGood} {
		t.Run(phase, func(t *testing.T) {
			m := faultManager(t)
			before, _ := m.ReadManifest()
			failed := false
			var mu sync.Mutex
			m.Persist = func(path string, b []byte) error {
				mu.Lock()
				if filepath.Base(path) == phase && !failed {
					failed = true
					mu.Unlock()
					return syscall.ENOSPC
				}
				mu.Unlock()
				return AtomicMode(path, b, 0640)
			}
			m.State = &enoscState{inner: m.State.(*memState), phase: phase, failed: &failed, mu: &mu}
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

// killState wraps a durable StateStore and SIGKILLs the process after the
// SaveState matching phase succeeds, so the parent observes the partial durable
// state exactly as a real crash would leave it.
type killState struct {
	inner StateStore
	phase string
}

func (s *killState) LoadState(ctx context.Context, key string, v any) (bool, error) {
	return s.inner.LoadState(ctx, key, v)
}

func (s *killState) SaveState(ctx context.Context, key string, v any) error {
	if e := s.inner.SaveState(ctx, key, v); e != nil {
		return e
	}
	if key == phaseKey(s.phase) {
		// For journal-boundary phases, kill only after the "promoting"
		// entry, not an earlier first_run/terminal entry written to the
		// same key.
		if s.phase == stateJournal || s.phase == "promoting" {
			if j, ok := v.(Journal); ok && j.Stage != "promoting" {
				return nil
			}
		}
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	return nil
}

func TestProcessKillRecoveryAtDurableBoundaries(t *testing.T) {
	for _, phase := range []string{"promoting", "squid.conf", stateLastGood} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			dbDir := t.TempDir()
			// Seed durable state (init) in the parent, so the child inherits a
			// terminal journal and a last-good manifest to roll back to.
			seed := New(dir, "/unused", "/dev/null", "", "", "")
			seed.State = &fileState{dir: dbDir}
			seed.Command = func(context.Context, ...string) error { return nil }
			seed.Reload = func(context.Context, string) error { return nil }
			seed.Probe = func(context.Context, string) error { return nil }
			if e := seed.Recover(context.Background(), true); e != nil {
				t.Fatal(e)
			}
			before, _ := seed.ReadManifest()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeCrashChild$")
			cmd.Env = append(os.Environ(),
				"KRAKEN_TEST_CRASH_DIR="+dir,
				"KRAKEN_TEST_CRASH_DB="+dbDir,
				"KRAKEN_TEST_CRASH_PHASE="+phase)
			e := cmd.Run()
			var exited *exec.ExitError
			if !errors.As(e, &exited) {
				t.Fatalf("child did not terminate: %v", e)
			}
			m := New(dir, "/unused", "/dev/null", "", "", "")
			m.State = &fileState{dir: dbDir}
			m.Command = func(context.Context, ...string) error { return nil }
			m.Reload = func(context.Context, string) error { return nil }
			m.Probe = func(context.Context, string) error { return nil }
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
	dir := os.Getenv("KRAKEN_TEST_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	dbDir := os.Getenv("KRAKEN_TEST_CRASH_DB")
	phase := os.Getenv("KRAKEN_TEST_CRASH_PHASE")
	m := New(dir, "/unused", "/dev/null", "", "", "")
	m.State = &killState{inner: &fileState{dir: dbDir}, phase: phase}
	m.Command = func(context.Context, ...string) error { return nil }
	m.Reload = func(context.Context, string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	m.Persist = func(path string, b []byte) error {
		if e := AtomicMode(path, b, 0640); e != nil {
			return e
		}
		if filepath.Base(path) == phase {
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
	m.Reload = func(c context.Context, action string) error {
		if action == "reconfigure" {
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
