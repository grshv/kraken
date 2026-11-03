package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

func testStore(t *testing.T, opts Options) *Store {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	opts.Now = func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
	s, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testStoreAt opens a store whose Now is `at`; tests move month boundaries
// by reassigning s.now directly (no concurrent queries at that point).
func testStoreAt(t *testing.T, opts Options, at time.Time) *Store {
	t.Helper()
	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	opts.Now = func() time.Time { return at }
	s, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkEv(id string, at time.Time, clientID, src, dst, result string, bytes int64) events.Event {
	v := events.Event{ID: id, Timestamp: at.UTC().Format(time.RFC3339Nano), SourceIP: src, Destination: dst, Method: "CONNECT", ProxyResult: result, Bytes: bytes, TLSMode: "tunnel", ClientMatchBasis: "unknown"}
	if clientID != "" {
		c := clientID
		v.ClientID = &c
		v.ClientMatchBasis = "policy_at_ingest"
	}
	return v
}

func TestSmokeIngestRollupSeries(t *testing.T) {
	s := testStore(t, Options{RawBudgetBytes: 1 << 30})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{
		mkEv("a", now.Add(-10*time.Minute), "c1", "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 100),
		mkEv("b", now.Add(-20*time.Minute), "c1", "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200),
		mkEv("c", now.Add(-30*time.Minute), "c1", "10.1.0.5", "github.com:443", "TCP_DENIED/403", 0),
		mkEv("u", now.Add(-15*time.Minute), "", "10.9.9.9", "github.com:443", "TCP_DENIED/403", 0),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{ObservedAt: "cp"}); e != nil {
		t.Fatal(e)
	}
	cp, e := s.Checkpoint(ctx)
	if e != nil || cp.ObservedAt != "cp" {
		t.Fatalf("checkpoint: %+v %v", cp, e)
	}
	buckets, e := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(buckets) != 96 {
		t.Fatalf("series len %d", len(buckets))
	}
	var reqs int64
	for _, b := range buckets {
		reqs += b.Requests
	}
	if reqs != 3 {
		t.Fatalf("series requests %d", reqs)
	}
	top, e := s.TopHosts(ctx, now.Add(-24*time.Hour), now, []string{"c1"}, nil, 10)
	if e != nil {
		t.Fatal(e)
	}
	if len(top) != 2 || top[0].Host != "archive.ubuntu.com" || top[0].Requests != 2 {
		t.Fatalf("top: %+v", top)
	}
	uk, e := s.UnknownSources(ctx, now.Add(-24*time.Hour), now, nil, 10)
	if e != nil {
		t.Fatal(e)
	}
	if len(uk) != 1 || uk[0].SourceIP != "10.9.9.9" || uk[0].Requests != 1 {
		t.Fatalf("unknown: %+v", uk)
	}
	page, e := s.Events(ctx, EventQuery{Limit: 10})
	if e != nil || len(page.Items) != 4 {
		t.Fatalf("events: %+v %v", page, e)
	}
	// ID format <YYYYMM>-<rowid>.
	if page.Items[0].ID == "" || page.Items[0].ID[:6] != "202610" {
		t.Fatalf("event id: %q", page.Items[0].ID)
	}
	got, e := s.Event(ctx, page.Items[0].ID, nil)
	if e != nil || got.ID != page.Items[0].ID {
		t.Fatalf("event by id: %+v %v", got, e)
	}
	info, e := s.Info(ctx)
	if e != nil || info.Bytes <= 0 || info.BrokenFiles != 0 {
		t.Fatalf("info: %+v %v", info, e)
	}
	_ = filepath.Join
}

func TestSmokeRollupIdempotent(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{mkEv("a", now.Add(-time.Minute), "c1", "10.1.0.5", "x.test:443", "TCP_TUNNEL", 5)}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Ingest already rolled up; a second Rollup must be a no-op (idempotent).
	n1, e := s.Rollup(ctx)
	if e != nil || n1 != 0 {
		t.Fatalf("rollup repeat: %d %v (want 0)", n1, e)
	}
	bk, _ := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	var reqs int64
	for _, b := range bk {
		reqs += b.Requests
	}
	if reqs != 1 {
		t.Fatalf("series requests %d (want 1)", reqs)
	}
}

// TestRollupRecalcOnRandomSet verifies that losing rollups.db and rebuilding
// from raw events reproduces the same aggregates the incremental Rollup did.
func TestRollupRecalcOnRandomSet(t *testing.T) {
	s := testStore(t, Options{RawBudgetBytes: 1 << 30})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{
		mkEv("r1", now.Add(-10*time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
		mkEv("r2", now.Add(-20*time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 200),
		mkEv("r3", now.Add(-30*time.Minute), "c1", "10.1.0.5", "b.test:443", "TCP_DENIED/403", 0),
		mkEv("r4", now.Add(-40*time.Minute), "c2", "10.2.0.5", "a.test:443", "TCP_TUNNEL", 50),
		mkEv("r5", now.Add(-50*time.Minute), "", "10.9.9.9", "a.test:443", "TCP_DENIED/403", 0),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	want15, _ := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	wantTop, _ := s.TopHosts(ctx, now.Add(-24*time.Hour), now, []string{"c1", "c2"}, nil, 10)
	wantUnk, _ := s.UnknownSources(ctx, now.Add(-24*time.Hour), now, nil, 10)

	// Simulate rollups.db loss: close, delete, reopen. Open rebuilds from raw.
	dir := s.opts.Dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(dir, "rollups.db")); e != nil {
		t.Fatal(e)
	}
	s2, e := Open(Options{Dir: dir, Now: func() time.Time { return now }, RawBudgetBytes: 1 << 30})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s2.Close() })
	got15, _ := s2.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	gotTop, _ := s2.TopHosts(ctx, now.Add(-24*time.Hour), now, []string{"c1", "c2"}, nil, 10)
	gotUnk, _ := s2.UnknownSources(ctx, now.Add(-24*time.Hour), now, nil, 10)
	if !sameBuckets(want15, got15) {
		t.Fatalf("series recalc differs: want=%v got=%v", want15, got15)
	}
	if len(wantTop) != len(gotTop) {
		t.Fatalf("top recalc differs: want=%v got=%v", wantTop, gotTop)
	}
	for i := range wantTop {
		if wantTop[i] != gotTop[i] {
			t.Fatalf("top[%d] recalc differs: want=%v got=%v", i, wantTop[i], gotTop[i])
		}
	}
	if len(wantUnk) != len(gotUnk) || wantUnk[0].Requests != gotUnk[0].Requests {
		t.Fatalf("unknown recalc differs: want=%v got=%v", wantUnk, gotUnk)
	}
}

func sameBuckets(a, b []Bucket) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Requests != b[i].Requests || a[i].Denied != b[i].Denied || a[i].Bytes != b[i].Bytes {
			return false
		}
	}
	return true
}

// TestRollupCrashBetweenIngestAndRollup simulates a crash after events are
// committed but before aggregates persist: a follow-up Rollup folds them
// exactly once (no duplicates, no loss).
func TestRollupCrashBetweenIngestAndRollup(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{
		mkEv("c1", now.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
		mkEv("c2", now.Add(-2*time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 200),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Simulate "aggregates lost, watermark lost": clear rollups tables and
	// watermark so the next Rollup sees every event as unrolled.
	for _, tbl := range []string{"client_15m", "client_host_1h", "client_host_1d", "unknown_15m", "unknown_host_1h", "unknown_host_1d", "watermark"} {
		if _, e := s.rollWriter.ExecContext(ctx, "DELETE FROM "+tbl); e != nil {
			t.Fatal(e)
		}
	}
	n, e := s.Rollup(ctx)
	if e != nil || n != 2 {
		t.Fatalf("rollup after crash: %d %v (want 2)", n, e)
	}
	bk, _ := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	var reqs, bytes int64
	for _, b := range bk {
		reqs += b.Requests
		bytes += b.Bytes
	}
	if reqs != 2 || bytes != 300 {
		t.Fatalf("post-crash aggregates: requests=%d bytes=%d (want 2/300)", reqs, bytes)
	}
	// A second Rollup after recovery is a no-op.
	if n, e := s.Rollup(ctx); e != nil || n != 0 {
		t.Fatalf("rollup repeat after recovery: %d %v (want 0)", n, e)
	}
}

// TestMonthChange exercises a month-boundary transition: events land in two
// different monthly files and reads span both.
func TestMonthChange(t *testing.T) {
	oct := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	s := testStoreAt(t, Options{}, oct)
	ctx := context.Background()
	if e := s.Ingest(ctx, []events.Event{mkEv("o1", oct.Add(-time.Hour), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 10)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Advance the clock into the next month and ingest another event.
	nov := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	s.now = func() time.Time { return nov }
	if e := s.Ingest(ctx, []events.Event{mkEv("n1", nov.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 20)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	months := s.listMonths()
	if len(months) != 2 || months[0] != "2026-10" || months[1] != "2026-11" {
		t.Fatalf("months: %v", months)
	}
	page, e := s.Events(ctx, EventQuery{Limit: 10})
	if e != nil || len(page.Items) != 2 {
		t.Fatalf("events across months: %+v %v", page, e)
	}
	// Newest first: n1 then o1.
	if page.Items[0].ID[:6] != "202611" || page.Items[1].ID[:6] != "202610" {
		t.Fatalf("event order: %+v", page.Items)
	}
	bk, _ := s.Series(ctx, oct.Add(-time.Hour), nov, 15*time.Minute, "c1", nil)
	var reqs, bytes int64
	for _, b := range bk {
		reqs += b.Requests
		bytes += b.Bytes
	}
	if reqs != 2 || bytes != 30 {
		t.Fatalf("series across months: requests=%d bytes=%d (want 2/30)", reqs, bytes)
	}
}

// TestRetentionByAge deletes monthly files whose month-end is older than
// RawRetention; the current month is always kept.
func TestRetentionByAge(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	s := testStoreAt(t, Options{RawRetention: 30 * 24 * time.Hour, RawBudgetBytes: 1 << 40}, now)
	ctx := context.Background()
	// Seed three months by moving the clock into each; the oldest gets an
	// mtime past retention.
	for _, m := range []struct {
		key  string
		at   time.Time
		mold time.Time
	}{
		{"2026-08", time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC), now.Add(-31 * 24 * time.Hour)},
		{"2026-09", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC), now.Add(-10 * 24 * time.Hour)},
		{"2026-10", time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), now.Add(-1 * 24 * time.Hour)},
	} {
		s.now = func() time.Time { return m.at }
		if e := s.Ingest(ctx, []events.Event{mkEv(m.key, m.at, "c1", "10.1.0.5", m.key+".test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
			t.Fatal(e)
		}
		p := s.monthPath(m.key)
		if e := os.Chtimes(p, m.mold, m.mold); e != nil {
			t.Fatal(e)
		}
	}
	s.now = func() time.Time { return now }
	if e := s.Maintain(ctx); e != nil {
		t.Fatal(e)
	}
	months := s.listMonths()
	if len(months) != 2 || months[0] != "2026-09" || months[1] != "2026-10" {
		t.Fatalf("retention by age left: %v", months)
	}
}

// TestRetentionByBudget keeps the newest months under the byte budget,
// deleting oldest first; the current month is never deleted.
func TestRetentionByBudget(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	s := testStoreAt(t, Options{RawRetention: 365 * 24 * time.Hour, RawBudgetBytes: 1}, now)
	ctx := context.Background()
	for _, m := range []struct {
		key string
		at  time.Time
	}{
		{"2026-08", time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)},
		{"2026-09", time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)},
		{"2026-10", time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)},
	} {
		s.now = func() time.Time { return m.at }
		if e := s.Ingest(ctx, []events.Event{mkEv(m.key, m.at, "c1", "10.1.0.5", m.key+".test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
			t.Fatal(e)
		}
		p := s.monthPath(m.key)
		if e := os.Chtimes(p, now, now); e != nil {
			t.Fatal(e)
		}
	}
	s.now = func() time.Time { return now }
	if e := s.Maintain(ctx); e != nil {
		t.Fatal(e)
	}
	months := s.listMonths()
	// Current month is always kept; everything else exceeds the budget.
	if len(months) != 1 || months[0] != "2026-10" {
		t.Fatalf("retention by budget left: %v", months)
	}
}

// TestRetentionByAgeMonthEnd verifies age retention uses the month-end derived
// from the file key, not the filesystem mtime: with Now=May 10 and a 60-day
// RawRetention, January and February (whose month-ends are older than the
// cutoff) are deleted even though their files were just written.
func TestRetentionByAgeMonthEnd(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	s := testStoreAt(t, Options{RawRetention: 60 * 24 * time.Hour, RawBudgetBytes: 1 << 40}, now)
	ctx := context.Background()
	// Seed one event per month Jan..May by moving the clock into each month.
	for _, m := range []time.Time{
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
	} {
		s.now = func() time.Time { return m }
		key := m.Format("2006-01")
		if e := s.Ingest(ctx, []events.Event{mkEv(key, m, "c1", "10.1.0.5", key+".test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
			t.Fatal(e)
		}
	}
	s.now = func() time.Time { return now }
	if e := s.Maintain(ctx); e != nil {
		t.Fatal(e)
	}
	months := s.listMonths()
	// Cutoff = now - 60d ~= March 11; Jan (end Jan 31) and Feb (end Feb 28)
	// are older; Mar/Apr/May remain (May is the current month).
	if len(months) != 3 || months[0] != "2026-03" || months[1] != "2026-04" || months[2] != "2026-05" {
		t.Fatalf("retention by month-end left: %v", months)
	}
	page, e := s.Events(ctx, EventQuery{Limit: 10})
	if e != nil || len(page.Items) != 3 {
		t.Fatalf("events after retention: %+v %v", page, e)
	}
}

// TestSeriesIncludesUnknownSources verifies the Series contract: clientID=""
// includes unknown sources — all for admin, only in-network for a manager
// scope. A specific clientID never includes unknowns.
func TestSeriesIncludesUnknownSources(t *testing.T) {
	s := testStore(t, Options{RawBudgetBytes: 1 << 30})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{
		mkEv("a1", now.Add(-10*time.Minute), "c-a", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
		mkEv("a2", now.Add(-20*time.Minute), "c-a", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 200),
		mkEv("b1", now.Add(-30*time.Minute), "c-b", "10.2.0.5", "b.test:443", "TCP_TUNNEL", 50),
		mkEv("u1", now.Add(-15*time.Minute), "", "10.9.9.9", "x.test:443", "TCP_DENIED/403", 0),
		mkEv("u2", now.Add(-25*time.Minute), "", "10.1.7.7", "y.test:443", "TCP_TUNNEL", 10),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	sum := func(bk []Bucket) int64 {
		var r int64
		for _, b := range bk {
			r += b.Requests
		}
		return r
	}
	// Admin all: 3 named (c-a×2, c-b×1) + 2 unknown = 5.
	bk, e := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "", nil)
	if e != nil {
		t.Fatal(e)
	}
	if r := sum(bk); r != 5 {
		t.Fatalf("admin series: %d (want 5)", r)
	}
	// Specific client c-a: only c-a (2), unknowns excluded.
	bk, _ = s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c-a", nil)
	if r := sum(bk); r != 2 {
		t.Fatalf("c-a series: %d (want 2)", r)
	}
	// Manager scope {c-a, 10.1.0.0/16}: c-a (2) + unknown 10.1.7.7 (1) = 3;
	// 10.9.9.9 is outside the scope network.
	sc := &Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	bk, _ = s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "", sc)
	if r := sum(bk); r != 3 {
		t.Fatalf("scope series: %d (want 3)", r)
	}
	// Scope with no networks: only c-a (2), no unknowns visible.
	scNoNet := &Scope{ClientIDs: []string{"c-a"}}
	bk, _ = s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "", scNoNet)
	if r := sum(bk); r != 2 {
		t.Fatalf("scope-no-net series: %d (want 2)", r)
	}
}

// TestEmptyScopeSeesNothing verifies that a non-nil scope with empty ClientIDs
// and Networks hides everything across every telemetry query surface: Series,
// TopHosts, UnknownSources, Events, Event, RecentEvents, DistinctClients,
// EventClients. nil = admin = all; empty scope = nothing.
func TestEmptyScopeSeesNothing(t *testing.T) {
	s := testStore(t, Options{RawBudgetBytes: 1 << 30})
	now := s.now()
	ctx := context.Background()
	rows := []events.Event{
		mkEv("a1", now.Add(-10*time.Minute), "c-a", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
		mkEv("a2", now.Add(-20*time.Minute), "c-a", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 200),
		mkEv("b1", now.Add(-30*time.Minute), "c-b", "10.2.0.5", "b.test:443", "TCP_TUNNEL", 50),
		mkEv("u1", now.Add(-15*time.Minute), "", "10.9.9.9", "x.test:443", "TCP_DENIED/403", 0),
		mkEv("u2", now.Add(-25*time.Minute), "", "10.1.7.7", "y.test:443", "TCP_TUNNEL", 10),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	empty := &Scope{}
	sumReqs := func(bk []Bucket) int64 {
		var r int64
		for _, b := range bk {
			r += b.Requests
		}
		return r
	}
	// Series "" under empty scope: client part suppressed, unknown part suppressed.
	bk, e := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "", empty)
	if e != nil || sumReqs(bk) != 0 {
		t.Fatalf("series empty scope: %d %v", sumReqs(bk), e)
	}
	// A specific foreign client under empty scope: no rows.
	bk, e = s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c-a", empty)
	if e != nil || sumReqs(bk) != 0 {
		t.Fatalf("series c-a empty scope: %d %v", sumReqs(bk), e)
	}
	// TopHosts with nil clientIDs under empty scope: empty.
	top, e := s.TopHosts(ctx, now.Add(-24*time.Hour), now, nil, empty, 10)
	if e != nil || len(top) != 0 {
		t.Fatalf("top empty scope: %+v %v", top, e)
	}
	// TopHosts with explicit clientIDs under empty scope: still empty.
	top, e = s.TopHosts(ctx, now.Add(-24*time.Hour), now, []string{"c-a"}, empty, 10)
	if e != nil || len(top) != 0 {
		t.Fatalf("top c-a empty scope: %+v %v", top, e)
	}
	// UnknownSources under empty scope: empty.
	uk, e := s.UnknownSources(ctx, now.Add(-24*time.Hour), now, empty, 10)
	if e != nil || len(uk) != 0 {
		t.Fatalf("unknown empty scope: %+v %v", uk, e)
	}
	// Events under empty scope: empty.
	page, e := s.Events(ctx, EventQuery{Limit: 10, Scope: empty})
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("events empty scope: %+v %v", page, e)
	}
	// DistinctClients under empty scope: 0.
	var n int64
	if e := s.DistinctClients(ctx, now.Add(-24*time.Hour), now, empty, &n); e != nil || n != 0 {
		t.Fatalf("distinct empty scope: %d %v", n, e)
	}
	// EventClients under empty scope: empty.
	cls, e := s.EventClients(ctx, empty)
	if e != nil || len(cls) != 0 {
		t.Fatalf("eventclients empty scope: %+v %v", cls, e)
	}
	// RecentEvents under empty scope: empty.
	re, e := s.RecentEvents(ctx, now.Add(-24*time.Hour), now, empty)
	if e != nil || len(re) != 0 {
		t.Fatalf("recent empty scope: %+v %v", re, e)
	}
	// Event by legacy id under empty scope: not found.
	if _, e := s.Event(ctx, "a1", empty); !errors.Is(e, ErrNotFound) {
		t.Fatalf("event a1 empty scope: %v (want ErrNotFound)", e)
	}
}

// TestCorruptRollupsDB recreates a lost/corrupt rollups.db from raw events.
func TestCorruptRollupsDB(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	if e := s.Ingest(ctx, []events.Event{
		mkEv("a", now.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
	}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Close, corrupt, reopen: aggregates must rebuild from raw.
	dir := s.opts.Dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "rollups.db"), []byte("not a database"), 0600); e != nil {
		t.Fatal(e)
	}
	s2, e := Open(Options{Dir: dir, Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s2.Close() })
	bk, e := s2.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
	if e != nil {
		t.Fatal(e)
	}
	var reqs int64
	for _, b := range bk {
		reqs += b.Requests
	}
	if reqs != 1 {
		t.Fatalf("rebuild after rollups.db loss: requests=%d (want 1)", reqs)
	}
	info, _ := s2.Info(ctx)
	if info.BrokenFiles == 0 {
		t.Fatal("broken rollups.db not counted in Info")
	}
}

// TestCorruptMonthlyFile recreates a corrupt events-YYYY-MM.db on open.
func TestCorruptMonthlyFile(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	if e := s.Ingest(ctx, []events.Event{
		mkEv("a", now.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 100),
	}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	dir := s.opts.Dir
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "events-2026-10.db"), []byte("not a database"), 0600); e != nil {
		t.Fatal(e)
	}
	s2, e := Open(Options{Dir: dir, Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s2.Close() })
	// A fresh empty month replaces the corrupt one; queries succeed with no rows.
	page, e := s2.Events(ctx, EventQuery{Limit: 10})
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("events after corrupt month: %+v %v", page, e)
	}
	info, _ := s2.Info(ctx)
	if info.BrokenFiles == 0 {
		t.Fatal("broken monthly file not counted in Info")
	}
}

// TestConcurrentReadsDuringIngest verifies the -race checker does not fire when
// a background ingestor writes while readers query.
func TestConcurrentReadsDuringIngest(t *testing.T) {
	s := testStore(t, Options{RawBudgetBytes: 1 << 30})
	now := s.now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Seed one event so the month file is open before readers start; this
	// avoids the monthFor open-race on the first concurrent call.
	if e := s.Ingest(ctx, []events.Event{mkEv("seed", now.Add(-time.Hour), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			ev := mkEv(fmt.Sprintf("w%d", i), now.Add(-time.Duration(i)*time.Second), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)
			_ = s.Ingest(ctx, []events.Event{ev}, events.Checkpoint{})
		}
	}()
	// Concurrent readers on the same month file and on rollups.db.
	var rwg sync.WaitGroup
	for i := 0; i < 3; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for j := 0; j < 10; j++ {
				_, _ = s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil)
				_, _ = s.TopHosts(ctx, now.Add(-24*time.Hour), now, []string{"c1"}, nil, 10)
				_, _ = s.Events(ctx, EventQuery{Limit: 10})
				_, _ = s.UnknownSources(ctx, now.Add(-24*time.Hour), now, nil, 10)
			}
		}()
	}
	rwg.Wait()
	cancel()
	wg.Wait()
}

// TestCtxCancel ensures query methods honor a canceled context.
func TestCtxCancel(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	if e := s.Ingest(context.Background(), []events.Event{mkEv("a", now.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Series(ctx, now.Add(-24*time.Hour), now, 15*time.Minute, "c1", nil); e == nil {
		t.Fatal("Series: expected ctx error")
	}
	if _, e := s.Events(ctx, EventQuery{Limit: 10}); e == nil {
		t.Fatal("Events: expected ctx error")
	}
}

// TestScopeEdgeCases covers empty scope lists (nothing visible) and
// IPv4-mapped IPv6 addresses being matched as their v4 form.
func TestScopeEdgeCases(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	if e := s.Ingest(ctx, []events.Event{
		mkEv("k1", now.Add(-time.Minute), "", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1),
		mkEv("k2", now.Add(-2*time.Minute), "c1", "10.1.0.6", "a.test:443", "TCP_TUNNEL", 1),
	}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	// Empty scope: a non-nil scope with no clients and no networks hides
	// everything (both client and unknown events).
	empty := &Scope{}
	page, e := s.Events(ctx, EventQuery{Limit: 10, Scope: empty})
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("empty scope: %+v %v", page, e)
	}
	// IPv4-mapped IPv6: 10.1.0.5 mapped as ::ffff:10.1.0.5 must match the
	// 10.1.0.0/24 network.
	mapped := &Scope{Networks: []string{"10.1.0.0/24"}}
	if e := s.Ingest(ctx, []events.Event{mkEv("k3", now.Add(-time.Minute), "", "::ffff:10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	page, e = s.Events(ctx, EventQuery{Limit: 10, Scope: mapped})
	if e != nil {
		t.Fatal(e)
	}
	// k1 (10.1.0.5) and k3 (::ffff:10.1.0.5) visible; k2 (client c1) not in scope.
	if len(page.Items) != 2 {
		t.Fatalf("ipv4-mapped scope: %+v", page)
	}
}

// TestEventByLegacySourceID confirms Event accepts the original input id as a
// fallback for the explain endpoint.
func TestEventByLegacySourceID(t *testing.T) {
	s := testStore(t, Options{})
	now := s.now()
	ctx := context.Background()
	if e := s.Ingest(ctx, []events.Event{mkEv("ev-legacy-handle", now.Add(-time.Minute), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	ev, e := s.Event(ctx, "ev-legacy-handle", nil)
	if e != nil || ev.Destination != "a.test:443" {
		t.Fatalf("event by legacy id: %+v %v", ev, e)
	}
	if _, e := s.Event(ctx, "nope", nil); !errors.Is(e, ErrNotFound) {
		t.Fatalf("missing legacy id: %v (want ErrNotFound)", e)
	}
	// A foreign client under a non-nil scope is hidden as ErrNotFound.
	foreign := &Scope{ClientIDs: []string{"c-other"}}
	if _, e := s.Event(ctx, "ev-legacy-handle", foreign); !errors.Is(e, ErrNotFound) {
		t.Fatalf("foreign legacy id: %v (want ErrNotFound)", e)
	}
}

func BenchmarkSeriesYear(b *testing.B) {
	dir := b.TempDir()
	o := Options{Dir: dir, Now: func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }}
	s, e := Open(o)
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := s.now()
	// Seed one event per day for a year.
	rows := make([]events.Event, 0, 365)
	for i := 0; i < 365; i++ {
		rows = append(rows, mkEv(fmt.Sprintf("d%d", i), now.Add(-time.Duration(i)*24*time.Hour), "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1))
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		b.Fatal(e)
	}
	from := now.Add(-365 * 24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Series(ctx, from, now, 24*time.Hour, "c1", nil)
	}
}

func BenchmarkIngestBatch(b *testing.B) {
	dir := b.TempDir()
	o := Options{Dir: dir, Now: func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }}
	s, e := Open(o)
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := s.now()
	row := mkEv("seed", now, "c1", "10.1.0.5", "a.test:443", "TCP_TUNNEL", 1)
	rows := make([]events.Event, 2000)
	for i := range rows {
		rows[i] = row
		rows[i].ID = fmt.Sprintf("b%d", i)
		rows[i].Timestamp = now.Add(-time.Duration(i) * time.Second).UTC().Format(time.RFC3339Nano)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
			b.Fatal(e)
		}
	}
}
