package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestFirstRunResumesAfterEveryWrite(t *testing.T) {
	// The first-run sequence performs six durable writes in order: manager-auth
	// include, parse candidate, journal(first_run), squid.conf, LKG and the
	// terminal journal marker. A crash at any boundary must leave Recover able
	// to complete idempotently on the next attempt.
	for _, failAt := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			cf := &failCounter{failAt: failAt}
			m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
			m.State = &failState{inner: newMemState(), cf: cf}
			m.Command = func(context.Context, ...string) error { return nil }
			m.Reload = func(context.Context, string) error { return nil }
			m.Probe = func(context.Context, string) error { return nil }
			m.Persist = persistFail(cf)
			if e := m.Recover(context.Background(), true); e == nil {
				t.Fatal("injection did not fire")
			}
			cf.failAt = 0
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
	if e := m.save(stateJournal, Journal{Stage: "reloading", Previous: prev, Candidate: prev}); e != nil {
		t.Fatal(e)
	}
	m.Reload = func(_ context.Context, action string) error {
		if action == "reconfigure" {
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
	m.Reload = func(context.Context, string) error { return nil }
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
	if e := m.save(stateJournal, Journal{Stage: "first_run", Candidate: bad}); e != nil {
		t.Fatal(e)
	}
	if e := m.Recover(context.Background(), true); e == nil {
		t.Fatal("inconsistent initialisation accepted")
	}
}

// TestRecoverFailsOnLoadStateError asserts that a LoadState error (database
// busy, deadline) is returned as-is, not masked as a missing manifest. The old
// ordering returned os.ErrNotExist when LoadState errored, dropping Recover
// into the first-run branch and overwriting the last-good manifest.
func TestRecoverFailsOnLoadStateError(t *testing.T) {
	loadErr := errors.New("database is locked")
	st := &errLoadState{loadErr: loadErr}
	m := New(t.TempDir(), "/unused", "/dev/null", "", "", "")
	m.State = st
	m.Command = func(context.Context, ...string) error { return nil }
	m.Reload = func(context.Context, string) error { return nil }
	m.Probe = func(context.Context, string) error { return nil }
	err := m.Recover(context.Background(), true)
	if !errors.Is(err, loadErr) {
		t.Fatalf("Recover returned %v, want %v", err, loadErr)
	}
	if st.saved {
		t.Fatal("Reover durable-wrote after a LoadState error (first-run branch)")
	}
}
