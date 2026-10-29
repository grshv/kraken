package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestStateStoreNotFound reports found=false for a missing key without error.
func TestStateStoreNotFound(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	var v map[string]any
	found, e := st.LoadState(context.Background(), "runtime.last_good", &v)
	if e != nil || found {
		t.Fatalf("expected (found=false, nil), got %v %v", found, e)
	}
}

// TestStateStoreRoundTrip writes a value and reads it back.
func TestStateStoreRoundTrip(t *testing.T) {
	st, e := Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.DB.Close()
	ctx := context.Background()
	type payload struct {
		Stage    string `json:"stage"`
		Revision string `json:"revision_id"`
	}
	want := payload{Stage: "terminal", Revision: "rev-1"}
	if e := st.SaveState(ctx, "runtime.journal", want); e != nil {
		t.Fatal(e)
	}
	var got payload
	found, e := st.LoadState(ctx, "runtime.journal", &got)
	if e != nil || !found {
		t.Fatalf("expected (found=true, nil), got %v %v", found, e)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
	// Overwrite and re-read to confirm upsert semantics.
	want.Stage = "reloading"
	if e := st.SaveState(ctx, "runtime.journal", want); e != nil {
		t.Fatal(e)
	}
	found, e = st.LoadState(ctx, "runtime.journal", &got)
	if e != nil || !found {
		t.Fatalf("expected (found=true, nil) after overwrite, got %v %v", found, e)
	}
	if got != want {
		t.Fatalf("overwrite mismatch: got %+v, want %+v", got, want)
	}
}
