package telemetry_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

// evCount is ev with an explicit Count for coalescing tests.
func evCount(at time.Time, client, src, host string, denied bool, bytes int64, count int64) events.Event {
	e := ev(at, client, src, host, denied, bytes)
	e.Count = count
	return e
}

// TestCoalesceCollapsesSameSecond verifies that events in the same UTC second
// with identical dimensions collapse into one row: count is the sum, bytes is
// summed, duration_ms is the max, ts/source_id come from the first event.
func TestCoalesceCollapsesSameSecond(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	sec := base.Add(-5 * time.Minute)
	rows := []events.Event{
		evCount(sec, "c1", "10.1.0.5", "a.example", false, 100, 2),
		evCount(sec, "c1", "10.1.0.5", "a.example", false, 50, 3),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	page, e := s.Events(ctx, telemetry.EventQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 {
		t.Fatalf("want 1 coalesced row, got %d", len(page.Items))
	}
	got := page.Items[0]
	if got.Count != 5 {
		t.Fatalf("count=%d want 5", got.Count)
	}
	if got.Bytes != 150 {
		t.Fatalf("bytes=%d want 150", got.Bytes)
	}
	if got.Timestamp != sec.UTC().Format(time.RFC3339) {
		t.Fatalf("ts=%q want %q", got.Timestamp, sec.UTC().Format(time.RFC3339))
	}
}

// TestCoalesceSeparatesBySecondAndResult checks that a neighboring second and a
// different result produce separate rows.
func TestCoalesceSeparatesBySecondAndResult(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	sec0 := base.Add(-5 * time.Minute)
	rows := []events.Event{
		evCount(sec0, "c1", "10.1.0.5", "a.example", false, 100, 1),
		evCount(sec0, "c1", "10.1.0.5", "a.example", false, 100, 1),
		evCount(sec0.Add(time.Second), "c1", "10.1.0.5", "a.example", false, 100, 1),
		evCount(sec0, "c1", "10.1.0.5", "a.example", true, 0, 1),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	page, e := s.Events(ctx, telemetry.EventQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	// 3 groups: (sec0, allowed) count 2, (sec0+1s, allowed) count 1, (sec0, denied) count 1.
	if len(page.Items) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(page.Items), page.Items)
	}
	var total int64
	for _, it := range page.Items {
		total += it.Count
	}
	if total != 4 {
		t.Fatalf("sum of counts=%d want 4", total)
	}
}

// TestCoalesceIdempotentRepeat verifies that re-ingesting the same batch (same
// source_ids) does not duplicate rows.
func TestCoalesceIdempotentRepeat(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	sec := base.Add(-5 * time.Minute)
	rows := []events.Event{
		evCount(sec, "c1", "10.1.0.5", "a.example", false, 100, 2),
		evCount(sec, "c1", "10.1.0.5", "a.example", false, 50, 3),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Re-ingest the exact same batch: ON CONFLICT(source_id) DO NOTHING.
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	page, e := s.Events(ctx, telemetry.EventQuery{Limit: 10})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Items[0].Count != 5 {
		t.Fatalf("want 1 row count 5, got %+v", page.Items)
	}
}

// TestRollupAndFacetsUseCount verifies that rollup aggregates and facets honor
// the count column (requests = SUM(count), denied likewise).
func TestRollupAndFacetsUseCount(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	sec := base.Add(-5 * time.Minute)
	ingest(t, s,
		evCount(sec, "c1", "10.1.0.5", "a.example", false, 100, 4),
		evCount(sec, "c1", "10.1.0.5", "a.example", true, 0, 2),
	)
	// Series reads from rollups: 4 allowed + 2 denied = 6 requests.
	bk, e := s.Series(ctx, base.Add(-time.Hour), base.Add(time.Minute), 15*time.Minute, "c1", nil)
	if e != nil {
		t.Fatal(e)
	}
	var reqs, denied int64
	for _, b := range bk {
		reqs += b.Requests
		denied += b.Denied
	}
	if reqs != 6 || denied != 2 {
		t.Fatalf("series requests=%d denied=%d want 6/2", reqs, denied)
	}
	// Facets read from raw (window <= 24h): SUM(count).
	f, e := s.Facets(ctx, telemetry.EventQuery{From: base.Add(-time.Hour), To: base.Add(time.Minute)}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if f.Allowed != 4 || f.Denied != 2 {
		t.Fatalf("facets allowed=%d denied=%d want 4/2", f.Allowed, f.Denied)
	}
}

// TestFacetsLongWindowFromAggregates verifies the aggregate path:
//   - a 7-day aligned window uses facet_15m (counts survive deletion of raw rows);
//   - a 24h window uses the raw path;
//   - both return the same totals on the same data.
func TestFacetsLongWindowFromAggregates(t *testing.T) {
	c := &clock{t: base}
	dir := t.TempDir()
	s := open(t, dir, c, nil)
	ctx := context.Background()
	// Ingest events well within the 7-day window. base is aligned to a 15-min
	// boundary (12:00:00 UTC), so a 7-day-aligned window is trivially aligned.
	t0 := base.Add(-2 * 24 * time.Hour)
	ingest(t, s,
		evCount(t0, "c1", "10.1.0.5", "a.example", false, 100, 3),
		evCount(t0, "c1", "10.1.0.5", "a.example", true, 0, 1),
	)
	// 7-day window aligned to 15 min: aggregate path.
	from7 := base.Add(-7 * 24 * time.Hour)
	to7 := base
	fAgg, e := s.Facets(ctx, telemetry.EventQuery{From: from7, To: to7}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if fAgg.Allowed != 3 || fAgg.Denied != 1 {
		t.Fatalf("aggregate path allowed=%d denied=%d want 3/1", fAgg.Allowed, fAgg.Denied)
	}
	// 24h window: raw path.
	fRaw, e := s.Facets(ctx, telemetry.EventQuery{From: from7, To: to7, Scope: nil}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	// Force the raw path explicitly via a 24h window that still covers the events.
	f24, e := s.Facets(ctx, telemetry.EventQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if f24.Allowed != 3 || f24.Denied != 1 {
		t.Fatalf("raw path allowed=%d denied=%d want 3/1", f24.Allowed, f24.Denied)
	}
	// Counts must survive deletion of the raw monthly file: close, delete events
	// file, reopen (rollups.db preserved) and re-query the 7-day window.
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	_ = os.Remove(filepath.Join(dir, "events-2026-10.db"))
	s2 := open(t, dir, c, nil)
	fSurv, e := s2.Facets(ctx, telemetry.EventQuery{From: from7, To: to7}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if fSurv.Allowed != 3 || fSurv.Denied != 1 {
		t.Fatalf("aggregate after raw deletion allowed=%d denied=%d want 3/1", fSurv.Allowed, fSurv.Denied)
	}
	// Silence unused warning from the redundant raw-path query above.
	_ = fRaw
}

// TestFacetsAggregateManagerScope verifies that on the aggregate path a manager
// scope sees only his client_id and unknown sources inside his networks.
func TestFacetsAggregateManagerScope(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	t0 := base.Add(-2 * 24 * time.Hour)
	ingest(t, s,
		ev(t0, "c-mine", "10.1.0.5", "a.example", false, 1),
		ev(t0, "c-other", "10.2.0.5", "b.example", false, 1),
		ev(t0, "", "10.1.9.9", "in.example", true, 0),
		ev(t0, "", "10.9.9.9", "out.example", true, 0),
	)
	from7 := base.Add(-7 * 24 * time.Hour)
	to7 := base
	sc := &telemetry.Scope{ClientIDs: []string{"c-mine"}, Networks: []string{"10.1.0.0/16"}}
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from7, To: to7, Scope: sc}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	// c-mine (1 allowed) + unknown 10.1.9.9 (1 denied) = 1/1; c-other and 10.9.9.9 hidden.
	if f.Allowed != 1 || f.Denied != 1 {
		t.Fatalf("manager aggregate allowed=%d denied=%d want 1/1", f.Allowed, f.Denied)
	}
	if f.Clients["c-mine"] != 1 || f.Clients[""] != 1 || len(f.Clients) != 2 {
		t.Fatalf("manager aggregate clients %+v", f.Clients)
	}
}
