package telemetry_test

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

func TestFacetsAcceptance(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	get := ev(base.Add(-90*time.Second), "c-a", "10.1.0.5", "a.example", false, 1)
	get.Method = "GET"
	ingest(t, s,
		ev(base.Add(-30*time.Second), "c-a", "10.1.0.5", "a.example", false, 1),
		ev(base.Add(-40*time.Second), "c-a", "10.1.0.5", "b.example", true, 0),
		get,
		ev(base.Add(-50*time.Second), "c-b", "10.2.0.5", "a.example", false, 1),
		ev(base.Add(-20*time.Second), "", "10.1.7.7", "x.example", true, 0),
		ev(base.Add(-20*time.Second), "", "10.9.9.9", "y.example", true, 0),
		ev(base.Add(-2*time.Hour), "c-a", "10.1.0.5", "old.example", false, 1))
	q := telemetry.EventQuery{From: base.Add(-3 * time.Minute), To: base}
	f, e := s.Facets(context.Background(), q, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if f.Allowed != 3 || f.Denied != 3 {
		t.Fatalf("result facet %d/%d, want 3/3", f.Allowed, f.Denied)
	}
	if f.Clients["c-a"] != 3 || f.Clients["c-b"] != 1 || f.Clients[""] != 2 {
		t.Fatalf("client facet %v", f.Clients)
	}
	if f.Methods["CONNECT"] != 5 || f.Methods["GET"] != 1 {
		t.Fatalf("method facet %v", f.Methods)
	}
	if len(f.Histogram) != 3 {
		t.Fatalf("histogram has %d buckets, want 3", len(f.Histogram))
	}
	last := f.Histogram[2]
	if !last.Start.Equal(base.Add(-time.Minute)) || last.Allowed != 2 || last.Denied != 3 {
		t.Fatalf("last bucket %+v", last)
	}
	sc := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	q.Scope = sc
	f, _ = s.Facets(context.Background(), q, time.Minute)
	if f.Clients["c-b"] != 0 || f.Clients[""] != 1 || f.Allowed+f.Denied != 4 {
		t.Fatalf("scoped facets %+v", f)
	}
	q.Scope = &telemetry.Scope{ClientIDs: []string{}, Networks: []string{}}
	f, _ = s.Facets(context.Background(), q, time.Minute)
	if f.Allowed+f.Denied != 0 {
		t.Fatalf("empty scope sees %+v", f)
	}
	q.Scope = nil
	q.Failures = true
	f, _ = s.Facets(context.Background(), q, time.Minute)
	if f.Allowed != 0 || f.Denied != 3 {
		t.Fatalf("failures filter %+v", f)
	}
	if _, e = s.Facets(context.Background(), telemetry.EventQuery{}, time.Minute); e == nil {
		t.Fatal("facets without a window must fail")
	}
	_ = events.Event{}
}
