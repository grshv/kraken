package telemetry_test

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

func loginEv(at time.Time, src, user string, status int) events.Event {
	e := ev(at, "c1", src, "a.example", status == 403, 10)
	if status == 407 {
		e.ProxyResult = "TCP_DENIED"
	}
	e.ProxyHTTPStatus = &status
	if user != "" {
		e.ProxyUsername = &user
	}
	return e
}

// Wrong passwords (407 with the supplied name), requests and per-address
// counts over a window that spans two monthly files.
func TestProxyLoginActivity(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	sep := time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)
	rows := []events.Event{
		loginEv(sep, "10.0.0.1", "agent", 200),
		loginEv(oct, "10.0.0.1", "agent", 200),
		loginEv(oct.Add(time.Minute), "10.0.0.1", "agent", 403),
		loginEv(oct.Add(2*time.Minute), "10.0.0.2", "agent", 407),
		loginEv(oct.Add(3*time.Minute), "10.0.0.2", "agent", 407),
		loginEv(oct, "10.0.0.3", "other", 200),
		loginEv(oct, "10.0.0.4", "", 407),
		loginEv(sep.Add(-48*time.Hour), "10.0.0.5", "agent", 200),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	from, to := c.now().Add(-24*time.Hour), c.now()
	got, e := s.ProxyLoginActivity(ctx, "agent", from, to)
	if e != nil {
		t.Fatal(e)
	}
	if got.Requests != 3 || got.Denied != 1 || got.Failed != 2 {
		t.Fatalf("totals: %+v", got)
	}
	if got.LastUsed == nil || !got.LastUsed.Equal(oct.Add(time.Minute)) {
		t.Fatalf("last used (407 excluded): %v", got.LastUsed)
	}
	if len(got.Sources) != 2 || got.Sources[0].SourceIP != "10.0.0.2" || got.Sources[0].Failed != 2 || got.Sources[0].Requests != 0 {
		t.Fatalf("sources: %+v", got.Sources)
	}
	if got.Sources[1].SourceIP != "10.0.0.1" || got.Sources[1].Requests != 3 {
		t.Fatalf("source 10.0.0.1: %+v", got.Sources[1])
	}
	none, e := s.ProxyLoginActivity(ctx, "nobody", from, to)
	if e != nil || none.Requests != 0 || none.LastUsed != nil || len(none.Sources) != 0 {
		t.Fatalf("unknown login: %+v %v", none, e)
	}
}
