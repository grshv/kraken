package telemetry_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

var base = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func open(t *testing.T, dir string, c *clock, mod func(*telemetry.Options)) *telemetry.Store {
	t.Helper()
	o := telemetry.Options{Dir: dir, Now: c.now}
	if mod != nil {
		mod(&o)
	}
	s, e := telemetry.Open(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var seq int

func ev(at time.Time, client, src, host string, denied bool, bytes int64) events.Event {
	seq++
	status := 200
	result := "TCP_TUNNEL"
	if denied {
		status, result = 403, "TCP_DENIED"
	}
	e := events.Event{ID: fmt.Sprintf("r-%d", seq), Timestamp: at.UTC().Format(time.RFC3339), SourceIP: src, Destination: host + ":443", Method: "CONNECT", ProxyResult: result, ProxyHTTPStatus: &status, Bytes: bytes, TLSMode: "tunnel"}
	if client != "" {
		id, name := client, client+"-name"
		e.ClientID, e.ClientName, e.ClientMatchBasis = &id, &name, "policy_at_ingest"
	}
	return e
}

func ingest(t *testing.T, s *telemetry.Store, items ...events.Event) {
	t.Helper()
	cp, e := s.Checkpoint(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	cp.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if e = s.Ingest(context.Background(), items, cp); e != nil {
		t.Fatal(e)
	}
}

func total(t *testing.T, s *telemetry.Store, from, to time.Time, client string, scope *telemetry.Scope) telemetry.Counts {
	t.Helper()
	b, e := s.Series(context.Background(), from, to, 15*time.Minute, client, scope)
	if e != nil {
		t.Fatal(e)
	}
	var c telemetry.Counts
	for _, x := range b {
		c.Requests += x.Requests
		c.Denied += x.Denied
		c.Bytes += x.Bytes
	}
	return c
}

func TestAcceptIngestVisibleInAggregates(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ingest(t, s,
		ev(base.Add(-10*time.Minute), "c-a", "10.1.0.5", "pypi.org", false, 100),
		ev(base.Add(-10*time.Minute), "c-a", "10.1.0.5", "github.com", true, 0),
		ev(base.Add(-20*time.Minute), "c-b", "10.2.0.5", "pypi.org", false, 50),
		ev(base.Add(-5*time.Minute), "", "10.9.9.9", "evil.example", true, 0))
	got := total(t, s, base.Add(-24*time.Hour), base, "c-a", nil)
	if got != (telemetry.Counts{Requests: 2, Denied: 1, Bytes: 100}) {
		t.Fatalf("c-a counts %+v", got)
	}
	if all := total(t, s, base.Add(-24*time.Hour), base, "", nil); all.Requests != 4 || all.Denied != 2 {
		t.Fatalf("all counts %+v", all)
	}
	b, _ := s.Series(context.Background(), base.Add(-24*time.Hour), base, 15*time.Minute, "", nil)
	if len(b) != 96 {
		t.Fatalf("24h/15m series has %d buckets, want 96", len(b))
	}
	top, e := s.TopHosts(context.Background(), base.Add(-24*time.Hour), base, []string{"c-a", "c-b"}, nil, 10)
	if e != nil || len(top) == 0 || top[0].Host != "pypi.org" || top[0].Requests != 2 {
		t.Fatalf("top hosts %+v %v", top, e)
	}
	src, e := s.UnknownSources(context.Background(), base.Add(-24*time.Hour), base, nil, 10)
	if e != nil || len(src) != 1 || src[0].SourceIP != "10.9.9.9" || src[0].Denied != 1 {
		t.Fatalf("unknown %+v %v", src, e)
	}
}

func TestAcceptRollupIdempotent(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ingest(t, s, ev(base.Add(-time.Minute), "c-a", "10.1.0.5", "a.example", false, 10))
	for i := 0; i < 3; i++ {
		if _, e := s.Rollup(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if got := total(t, s, base.Add(-time.Hour), base, "c-a", nil); got.Requests != 1 || got.Bytes != 10 {
		t.Fatalf("repeated rollup changed counts: %+v", got)
	}
}

func TestAcceptScopeFiltersAggregates(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ingest(t, s,
		ev(base.Add(-time.Minute), "c-a", "10.1.0.5", "a.example", false, 1),
		ev(base.Add(-time.Minute), "c-b", "10.2.0.5", "b.example", false, 1),
		ev(base.Add(-time.Minute), "", "10.1.7.7", "in.example", true, 0),
		ev(base.Add(-time.Minute), "", "10.9.9.9", "out.example", true, 0))
	sc := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	if got := total(t, s, base.Add(-time.Hour), base, "", sc); got.Requests != 2 {
		t.Fatalf("scoped total %+v, want c-a + 10.1.7.7", got)
	}
	if got := total(t, s, base.Add(-time.Hour), base, "c-b", sc); got.Requests != 0 {
		t.Fatalf("foreign client visible: %+v", got)
	}
	top, _ := s.TopHosts(context.Background(), base.Add(-time.Hour), base, []string{"c-a", "c-b"}, sc, 10)
	for _, h := range top {
		if h.Host == "b.example" {
			t.Fatal("foreign host leaked")
		}
	}
	src, _ := s.UnknownSources(context.Background(), base.Add(-time.Hour), base, sc, 10)
	if len(src) != 1 || src[0].SourceIP != "10.1.7.7" {
		t.Fatalf("scoped unknown %+v", src)
	}
	empty := &telemetry.Scope{ClientIDs: []string{}, Networks: []string{}}
	if got := total(t, s, base.Add(-time.Hour), base, "", empty); got.Requests != 0 {
		t.Fatalf("empty scope sees %+v", got)
	}
}

func TestAcceptEventsJournalAndLookup(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	var items []events.Event
	for i := 0; i < 25; i++ {
		items = append(items, ev(base.Add(-time.Duration(i)*time.Minute), "c-a", "10.1.0.5", fmt.Sprintf("h%d.example", i), i%5 == 0, 1))
	}
	ingest(t, s, items...)
	p, e := s.Events(context.Background(), telemetry.EventQuery{Limit: 10})
	if e != nil || len(p.Items) != 10 || p.NextCursor == nil {
		t.Fatalf("page1 %d %v %v", len(p.Items), p.NextCursor, e)
	}
	seen := map[string]bool{}
	for _, it := range p.Items {
		seen[it.ID] = true
	}
	p2, e := s.Events(context.Background(), telemetry.EventQuery{Limit: 10, Cursor: *p.NextCursor})
	if e != nil || len(p2.Items) != 10 {
		t.Fatalf("page2 %d %v", len(p2.Items), e)
	}
	for _, it := range p2.Items {
		if seen[it.ID] {
			t.Fatalf("duplicate across pages: %s", it.ID)
		}
	}
	f, _ := s.Events(context.Background(), telemetry.EventQuery{Limit: 50, Failures: true})
	if len(f.Items) != 5 {
		t.Fatalf("failures filter: %d", len(f.Items))
	}
	if _, e = s.Events(context.Background(), telemetry.EventQuery{Limit: 10, Failures: true, Cursor: *p.NextCursor}); e == nil {
		t.Fatal("cursor reused with other filters must fail")
	}
	got, e := s.Event(context.Background(), p.Items[0].ID, nil)
	if e != nil || got.Destination != p.Items[0].Destination {
		t.Fatalf("lookup %+v %v", got, e)
	}
	if _, e = s.Event(context.Background(), p.Items[0].ID, &telemetry.Scope{ClientIDs: []string{"c-b"}, Networks: []string{}}); !errors.Is(e, telemetry.ErrNotFound) {
		t.Fatalf("out-of-scope lookup: %v", e)
	}
	if _, e = s.Event(context.Background(), "nonsense", nil); !errors.Is(e, telemetry.ErrNotFound) {
		t.Fatalf("bad id: %v", e)
	}
}

func TestAcceptCheckpointSurvivesReopenAndMonthRollover(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC)}
	s := open(t, dir, c, nil)
	cp := events.Checkpoint{Files: map[string]events.Position{"access.log": {Offset: 100}}, SourceAvailable: true}
	if e := s.Ingest(context.Background(), []events.Event{ev(c.now(), "c-a", "10.1.0.5", "a.example", false, 1)}, cp); e != nil {
		t.Fatal(e)
	}
	c.set(time.Date(2026, 11, 1, 0, 1, 0, 0, time.UTC))
	cp.Files["access.log"] = events.Position{Offset: 200}
	if e := s.Ingest(context.Background(), []events.Event{ev(c.now(), "c-a", "10.1.0.5", "b.example", false, 1)}, cp); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s2 := open(t, dir, c, nil)
	got, e := s2.Checkpoint(context.Background())
	if e != nil || got.Files["access.log"].Offset != 200 {
		t.Fatalf("checkpoint after reopen %+v %v", got, e)
	}
	if got := total(t, s2, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), c.now().Add(time.Minute), "c-a", nil); got.Requests != 2 {
		t.Fatalf("events across months: %+v", got)
	}
	p, _ := s2.Events(context.Background(), telemetry.EventQuery{Limit: 10})
	if len(p.Items) != 2 {
		t.Fatalf("journal across months: %d", len(p.Items))
	}
}

func TestAcceptRawRetentionKeepsAggregates(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)}
	s := open(t, dir, c, func(o *telemetry.Options) { o.RawRetention = 60 * 24 * time.Hour })
	for m := 1; m <= 5; m++ {
		c.set(time.Date(2026, time.Month(m), 10, 0, 0, 0, 0, time.UTC))
		ingest(t, s, ev(c.now(), "c-a", "10.1.0.5", "a.example", false, 1))
	}
	if e := s.Maintain(context.Background()); e != nil {
		t.Fatal(e)
	}
	p, _ := s.Events(context.Background(), telemetry.EventQuery{Limit: 50})
	if len(p.Items) >= 5 || len(p.Items) == 0 {
		t.Fatalf("raw retention kept %d events", len(p.Items))
	}
	if got := total(t, s, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), c.now().Add(time.Hour), "c-a", nil); got.Requests != 5 {
		t.Fatalf("aggregates lost with raw retention: %+v", got)
	}
}

func TestAcceptRawBudgetDropsOldestMonthNotCurrent(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)}
	s := open(t, dir, c, func(o *telemetry.Options) { o.RawBudgetBytes = 1 })
	for m := 1; m <= 3; m++ {
		c.set(time.Date(2026, time.Month(m), 10, 0, 0, 0, 0, time.UTC))
		ingest(t, s, ev(c.now(), "c-a", "10.1.0.5", "a.example", false, 1))
	}
	if e := s.Maintain(context.Background()); e != nil {
		t.Fatal(e)
	}
	p, _ := s.Events(context.Background(), telemetry.EventQuery{Limit: 50})
	if len(p.Items) != 1 {
		t.Fatalf("budget should keep only the current month: %d events", len(p.Items))
	}
	ingest(t, s, ev(c.now(), "c-a", "10.1.0.5", "b.example", false, 1))
}

func TestAcceptCorruptFilesDoNotBreakOpen(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: base}
	s := open(t, dir, c, nil)
	ingest(t, s, ev(base, "c-a", "10.1.0.5", "a.example", false, 1))
	s.Close()
	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() && strings.HasSuffix(p, ".db") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) < 2 {
		t.Fatalf("expected rollups and month files, got %v", files)
	}
	for _, f := range files {
		os.Remove(f + "-wal")
		os.Remove(f + "-shm")
		if e := os.WriteFile(f, []byte("this is not a database, just garbage bytes................................................................"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	s2 := open(t, dir, c, nil)
	ingest(t, s2, ev(base, "c-a", "10.1.0.5", "b.example", false, 1))
	if got := total(t, s2, base.Add(-time.Hour), base.Add(time.Hour), "c-a", nil); got.Requests < 1 {
		t.Fatalf("store unusable after corruption: %+v", got)
	}
	var broken int
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".broken") {
			broken++
		}
	}
	if broken == 0 {
		t.Fatal("corrupt files were not set aside")
	}
}

func TestAcceptConcurrentReadsDuringIngest(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, e := s.Series(ctx, base.Add(-24*time.Hour), base, 15*time.Minute, "", nil); e != nil {
					errs <- e
					return
				}
				if _, e := s.Events(ctx, telemetry.EventQuery{Limit: 50}); e != nil {
					errs <- e
					return
				}
			}
		}()
	}
	for b := 0; b < 40; b++ {
		var items []events.Event
		for i := 0; i < 200; i++ {
			items = append(items, ev(base.Add(-time.Duration(i)*time.Second), "c-a", "10.1.0.5", fmt.Sprintf("h%d.example", i%30), i%7 == 0, 10))
		}
		ingest(t, s, items...)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("reader failed during ingest: %v", e)
	}
	if got := total(t, s, base.Add(-time.Hour), base.Add(time.Minute), "c-a", nil); got.Requests != 8000 {
		t.Fatalf("lost events: %+v", got)
	}
}

func TestAcceptContextCancelReturnsPromptly(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Series(ctx, base.Add(-24*time.Hour), base, 15*time.Minute, "", nil); e == nil {
		t.Fatal("canceled context must fail the query")
	}
	if _, e := s.Series(context.Background(), base.Add(-24*time.Hour), base, 15*time.Minute, "", nil); e != nil {
		t.Fatalf("store unusable after cancel: %v", e)
	}
}
