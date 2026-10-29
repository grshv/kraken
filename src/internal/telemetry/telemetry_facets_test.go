package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

// facSum collapses a Facets.Histogram into totals, mirroring how /events/facets
// sums buckets for comparison with expected counts.
func facSum(f telemetry.Facets) (allowed, denied int64) {
	for _, b := range f.Histogram {
		allowed += b.Allowed
		denied += b.Denied
	}
	return allowed, denied
}

func TestFacetsFiltersAndCounts(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	// Window covers all events with room to spare.
	from := base.Add(-time.Hour)
	to := base.Add(time.Minute)
	ingest(t, s,
		ev(base.Add(-10*time.Minute), "c-a", "10.1.0.5", "pypi.org", false, 100),
		ev(base.Add(-10*time.Minute), "c-a", "10.1.0.5", "github.com", true, 0),
		ev(base.Add(-20*time.Minute), "c-b", "10.2.0.5", "pypi.org", false, 50),
		ev(base.Add(-5*time.Minute), "", "10.9.9.9", "evil.example", true, 0),
	)
	// Admin view: 2 allowed + 2 denied; 4 buckets at 1m step hold them.
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: to}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if f.Allowed != 2 || f.Denied != 2 {
		t.Fatalf("totals allowed=%d denied=%d", f.Allowed, f.Denied)
	}
	if got := len(f.Histogram); got != int(to.Sub(from)/time.Minute) {
		t.Fatalf("histogram buckets=%d want %d", got, int(to.Sub(from)/time.Minute))
	}
	if a, d := facSum(f); a != 2 || d != 2 {
		t.Fatalf("hist sum allowed=%d denied=%d", a, d)
	}
	if f.Clients["c-a"] != 2 || f.Clients["c-b"] != 1 || f.Clients[""] != 1 {
		t.Fatalf("clients %+v", f.Clients)
	}
	if f.Methods["CONNECT"] != 4 {
		t.Fatalf("methods %+v", f.Methods)
	}

	// Filter: failures_only.
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Failures: true}, time.Minute)
	if f.Allowed != 0 || f.Denied != 2 {
		t.Fatalf("failures_only allowed=%d denied=%d", f.Allowed, f.Denied)
	}

	// Filter: client_id c-a.
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, ClientID: "c-a"}, time.Minute)
	if f.Allowed != 1 || f.Denied != 1 || f.Clients["c-a"] != 2 || len(f.Clients) != 1 {
		t.Fatalf("client c-a %+v", f)
	}

	// Filter: source_ip.
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, SourceIP: "10.2.0.5"}, time.Minute)
	if f.Allowed != 1 || f.Denied != 0 || f.Clients["c-b"] != 1 {
		t.Fatalf("source_ip %+v", f)
	}

	// Filter: method (none match GET here, but CONNECT all).
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Method: "GET"}, time.Minute)
	if f.Allowed+f.Denied != 0 {
		t.Fatalf("method GET should be empty: %+v", f)
	}

	// Filter: result exact.
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Result: "TCP_DENIED"}, time.Minute)
	if f.Allowed != 0 || f.Denied != 2 {
		t.Fatalf("result TCP_DENIED %+v", f)
	}

	// Filter: destination substring.
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Destination: "pypi"}, time.Minute)
	if f.Allowed != 2 || f.Denied != 0 {
		t.Fatalf("destination pypi %+v", f)
	}
}

func TestFacetsScopeEmptyAndIPv4MappedAndNetworks(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	from := base.Add(-time.Hour)
	to := base.Add(time.Minute)
	ingest(t, s,
		ev(base.Add(-time.Minute), "c-a", "10.1.0.5", "a.example", false, 1),
		ev(base.Add(-time.Minute), "c-b", "10.2.0.5", "b.example", false, 1),
		ev(base.Add(-time.Minute), "", "10.1.7.7", "in.example", true, 0),
		ev(base.Add(-time.Minute), "", "10.9.9.9", "out.example", true, 0),
	)
	// Empty scope: zero facets (and zero-length map, not nil).
	empty := &telemetry.Scope{}
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Scope: empty}, time.Minute)
	if e != nil || f.Allowed != 0 || f.Denied != 0 || len(f.Clients) != 0 || len(f.Methods) != 0 {
		t.Fatalf("empty scope %+v %v", f, e)
	}
	if f.Histogram == nil {
		t.Fatal("histogram must be allocated even when empty scope")
	}

	// Manager scope {c-a, 10.1.0.0/16}: c-a (1 allowed) + unknown 10.1.7.7 (1 denied) = 2;
	// 10.9.9.9 outside networks, c-b foreign.
	sc := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Scope: sc}, time.Minute)
	if f.Allowed != 1 || f.Denied != 1 {
		t.Fatalf("scoped totals allowed=%d denied=%d", f.Allowed, f.Denied)
	}
	if f.Clients["c-a"] != 1 || f.Clients[""] != 1 || len(f.Clients) != 2 {
		t.Fatalf("scoped clients %+v", f.Clients)
	}
	if _, ok := f.Clients["c-b"]; ok {
		t.Fatal("foreign client leaked into facets")
	}

	// IPv4-mapped IPv6 source matches the same 10.1.0.0/16 network.
	ingest(t, s, ev(base.Add(-2*time.Minute), "", "::ffff:10.1.0.5", "mapped.example", false, 1))
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Scope: sc}, time.Minute)
	if f.Allowed != 2 || f.Denied != 1 {
		t.Fatalf("ipv4-mapped allowed=%d denied=%d", f.Allowed, f.Denied)
	}
	if f.Clients[""] != 2 {
		t.Fatalf("ipv4-mapped unknown count=%d", f.Clients[""])
	}

	// Manager scope with networks but no ClientIDs: only NULL-client unknowns
	// inside networks. 10.1.7.7 (denied) + ::ffff:10.1.0.5 (allowed, mapped);
	// 10.9.9.9 outside; c-a's 10.1.0.5 has a client_id so excluded by IS NULL.
	onlyNet := &telemetry.Scope{Networks: []string{"10.1.0.0/16"}}
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Scope: onlyNet}, time.Minute)
	if f.Allowed != 1 || f.Denied != 1 {
		t.Fatalf("only-networks allowed=%d denied=%d", f.Allowed, f.Denied)
	}
	if f.Clients[""] != 2 || len(f.Clients) != 1 {
		t.Fatalf("only-networks clients %+v", f.Clients)
	}
}

// TestFacetsClientOnlyScopeHidesUnknown verifies the rule from the review: a
// manager scope with clients but no networks must not see unmatched (NULL-
// client) sources in any facet, matching Events. Without the IS NOT NULL
// restriction in facetsKnown, COALESCE would fold NULL rows into Clients[""]
// and bump totals.
func TestFacetsClientOnlyScopeHidesUnknown(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	from := base.Add(-time.Hour)
	to := base.Add(time.Minute)
	ingest(t, s,
		ev(base.Add(-1*time.Minute), "c-a", "10.1.0.5", "a.example", false, 1),
		ev(base.Add(-1*time.Minute), "", "10.1.7.7", "in.example", true, 0),
		ev(base.Add(-1*time.Minute), "", "10.9.9.9", "out.example", true, 0),
	)
	clientOnly := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{}}
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: to, Scope: clientOnly}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	// Only c-a's allowed event is visible; unknowns are hidden entirely.
	if f.Allowed != 1 || f.Denied != 0 {
		t.Fatalf("client-only scope totals allowed=%d denied=%d, want 1/0", f.Allowed, f.Denied)
	}
	if f.Clients[""] != 0 {
		t.Fatalf("client-only scope leaked unknown count=%d", f.Clients[""])
	}
	if _, ok := f.Clients["c-a"]; !ok || f.Clients["c-a"] != 1 {
		t.Fatalf("client-only scope clients %+v, want c-a=1 only", f.Clients)
	}
	// Cross-check: Events must agree (no unknown rows returned).
	page, e := s.Events(ctx, telemetry.EventQuery{From: from, To: to, Scope: clientOnly, Limit: 50})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 {
		t.Fatalf("client-only scope events=%d, facets saw %d total", len(page.Items), f.Allowed+f.Denied)
	}
}

func TestFacetsBucketBoundariesAndWindow(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	// Two events 5 minutes apart, both at exact bucket starts.
	t0 := base.Add(-5 * time.Minute)
	ingest(t, s,
		ev(t0, "c-a", "10.1.0.5", "a.example", false, 1),
		ev(t0.Add(5*time.Minute), "c-a", "10.1.0.5", "b.example", true, 0),
	)
	// Window [t0, t0+6m): 6 one-minute buckets. First holds allowed; last
	// holds the event exactly at t0+5m; the [t0+5m, t0+6m) bucket.
	from := t0
	to := t0.Add(6 * time.Minute)
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: to}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if len(f.Histogram) != 6 {
		t.Fatalf("buckets=%d want 6", len(f.Histogram))
	}
	if f.Histogram[0].Allowed != 1 || f.Histogram[0].Denied != 0 {
		t.Fatalf("bucket[0] %+v", f.Histogram[0])
	}
	if f.Histogram[5].Allowed != 0 || f.Histogram[5].Denied != 1 {
		t.Fatalf("bucket[5] %+v", f.Histogram[5])
	}
	// Buckets 1-4 empty.
	for i := 1; i < 5; i++ {
		if f.Histogram[i].Allowed != 0 || f.Histogram[i].Denied != 0 {
			t.Fatalf("bucket[%d] not zero %+v", i, f.Histogram[i])
		}
	}
	// Event exactly at `to` is excluded (half-open window): ingest one at t0+6m
	// and it must not show up in a 6-bucket window.
	ingest(t, s, ev(t0.Add(6*time.Minute), "c-a", "10.1.0.5", "c.example", false, 1))
	f, _ = s.Facets(ctx, telemetry.EventQuery{From: from, To: to}, time.Minute)
	if f.Allowed != 1 || f.Denied != 1 {
		t.Fatalf("half-open window leaked at `to`: allowed=%d denied=%d", f.Allowed, f.Denied)
	}
}

func TestFacetsAcrossTwoMonthlyFiles(t *testing.T) {
	dir := t.TempDir()
	oct := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	c := &clock{t: oct}
	s := open(t, dir, c, nil)
	ctx := context.Background()
	ingest(t, s, ev(oct.Add(-time.Hour), "c-a", "10.1.0.5", "oct.example", false, 10))
	// Advance the clock into the next month and ingest another event.
	nov := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	c.set(nov)
	ingest(t, s, ev(nov.Add(-time.Minute), "c-a", "10.1.0.5", "nov.example", true, 0))
	// Window spans both months.
	from := oct.Add(-2 * time.Hour)
	to := nov
	f, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: to}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if f.Allowed != 1 || f.Denied != 1 {
		t.Fatalf("across months allowed=%d denied=%d", f.Allowed, f.Denied)
	}
	if f.Clients["c-a"] != 2 {
		t.Fatalf("across months clients %+v", f.Clients)
	}
}

func TestFacetsWindowErrors(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	cases := []struct {
		name string
		q    telemetry.EventQuery
		step time.Duration
	}{
		{"missing from", telemetry.EventQuery{To: base}, time.Minute},
		{"missing to", telemetry.EventQuery{From: base}, time.Minute},
		{"to before from", telemetry.EventQuery{From: base, To: base.Add(-time.Minute)}, time.Minute},
		{"to equals from", telemetry.EventQuery{From: base, To: base}, time.Minute},
		{"window over 7 days", telemetry.EventQuery{From: base, To: base.Add(7*24*time.Hour + time.Minute)}, time.Minute},
		{"zero step", telemetry.EventQuery{From: base, To: base.Add(time.Minute)}, 0},
		{"negative step", telemetry.EventQuery{From: base, To: base.Add(time.Minute)}, -time.Minute},
		{"over 1440 buckets", telemetry.EventQuery{From: base, To: base.Add(24 * time.Hour)}, time.Second},
	}
	for _, tc := range cases {
		_, e := s.Facets(ctx, tc.q, tc.step)
		if !errors.Is(e, telemetry.ErrFacetsWindow) {
			t.Fatalf("%s: want ErrFacetsWindow, got %v", tc.name, e)
		}
	}
}

// BenchmarkFacets500kDay seeds 500k events over 24h and measures Facets with a
// 1-minute step (1440 buckets). Must stay under 200ms per the task spec.
func BenchmarkFacets500kDay(b *testing.B) {
	dir := b.TempDir()
	o := telemetry.Options{Dir: dir, Now: func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }}
	s, e := telemetry.Open(o)
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := o.Now()
	// Seed 500k events spread across the day; reuse one host to keep the host
	// dictionary tiny and the GROUP BY dominated by event rows.
	rows := make([]events.Event, 0, 500000)
	for i := 0; i < 500000; i++ {
		rows = append(rows, mkEvBench("b"+itoa(i), now.Add(-time.Duration(i)*time.Second), "c1", "10.1.0.5", "host.test:443", "TCP_TUNNEL", 1))
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		b.Fatal(e)
	}
	from := now.Add(-24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := s.Facets(ctx, telemetry.EventQuery{From: from, To: now}, time.Minute); e != nil {
			b.Fatal(e)
		}
	}
}

// mkEvBench mirrors the testEvent shape without relying on package-internal
// helpers (this is a telemetry_test package).
func mkEvBench(id string, at time.Time, clientID, src, dst, result string, bytes int64) events.Event {
	v := events.Event{ID: id, Timestamp: at.UTC().Format(time.RFC3339Nano), SourceIP: src, Destination: dst, Method: "CONNECT", ProxyResult: result, Bytes: bytes, TLSMode: "tunnel", ClientMatchBasis: "unknown"}
	if clientID != "" {
		c := clientID
		v.ClientID = &c
		v.ClientMatchBasis = "policy_at_ingest"
	}
	return v
}

// itoa avoids strconv in a hot loop; digits only.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
