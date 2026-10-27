package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestFirstRunResumesAfterEveryWrite(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
			m.Command = func(context.Context, ...string) error { return nil }
			m.Probe = func(context.Context, string) error { return nil }
			writes := 0
			m.Persist = func(path string, b []byte) error {
				writes++
				if writes == failAt {
					return errors.New("injected storage failure")
				}
				return Atomic(path, b)
			}
			if e := m.Recover(context.Background(), true); e == nil {
				t.Fatal("injection did not fire")
			}
			m.Persist = Atomic
			if e := m.Recover(context.Background(), true); e != nil {
				t.Fatal(e)
			}
			j, e := m.Journal()
			if e != nil || j.Stage != "terminal" || j.Outcome != "initialised" {
				t.Fatalf("init not complete: %+v %v", j, e)
			}
			prev, _ := m.ReadManifest()
			if e := m.checkDrift(prev); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestRecoveryRemainsPendingUntilLiveVerification(t *testing.T) {
	m := testManager(t)
	prev, _ := m.ReadManifest()
	if e := m.write("journal.json", Journal{Stage: "reloading", Previous: prev, Candidate: prev}); e != nil {
		t.Fatal(e)
	}
	m.Command = func(_ context.Context, args ...string) error {
		if args[len(args)-1] == "reconfigure" {
			return errors.New("control channel down")
		}
		return nil
	}
	if e := m.Recover(context.Background(), false); e == nil {
		t.Fatal("unverified recovery accepted")
	}
	assertPending := func() {
		t.Helper()
		j, e := m.Journal()
		if e != nil || j.Stage == "terminal" {
			t.Fatalf("recovery lost: %+v %v", j, e)
		}
	}
	assertPending()
	if e := m.Recover(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	assertPending()
	m.Command = func(context.Context, ...string) error { return nil }
	m.Probe = func(context.Context, string) error { return errors.New("wrong runtime fingerprint") }
	if e := m.Recover(context.Background(), false); e == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	assertPending()
	m.Probe = func(context.Context, string) error { return nil }
	if e := m.Recover(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	j, _ := m.Journal()
	if j.Stage != "terminal" || j.Outcome != "rolled_back" {
		t.Fatalf("verified recovery not complete: %+v", j)
	}
}

func TestFirstRunRejectsMismatchedLKG(t *testing.T) {
	m := testManager(t)
	prev, _ := m.ReadManifest()
	bad := prev
	bad.Hash = "different"
	b, _ := json.Marshal(Journal{Stage: "first_run", Candidate: bad})
	if e := Atomic(filepath.Join(m.Dir, "journal.json"), b); e != nil {
		t.Fatal(e)
	}
	if e := m.Recover(context.Background(), true); e == nil {
		t.Fatal("inconsistent initialisation accepted")
	}
}
