package telemetry_test

import (
	"context"
	"maps"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

// longWindowData spreads requests over three days: clients and an unmatched
// source, two methods, deny reasons, a site error and repeated events.
func longWindowData(t *testing.T) (*telemetry.Store, time.Time, time.Time) {
	t.Helper()
	s := open(t, t.TempDir(), &clock{t: base}, nil)
	from := base.Add(-72 * time.Hour)
	var rows []events.Event
	for day := 0; day < 3; day++ {
		at := from.Add(time.Duration(day)*24*time.Hour + 90*time.Minute + time.Duration(day)*time.Second)
		get := ev(at, "c-a", "10.1.0.5", "pypi.org", false, 100)
		get.Method = "GET"
		rows = append(rows,
			evCount(at, "c-a", "10.1.0.5", "github.com", false, 50, 3),
			get,
			outcomeEv(at, "c-a", "10.1.0.6", "TCP_DENIED/403", 403, "no_rule"),
			outcomeEv(at, "c-b", "10.2.0.5", "TCP_DENIED/403", 403, "private"),
			outcomeEv(at, "c-b", "10.2.0.5", "TCP_MISS/502", 502, ""),
			outcomeEv(at, "", "10.1.9.9", "TCP_DENIED/403", 403, "no_rule"),
			outcomeEv(at, "", "10.9.9.9", "TCP_DENIED/407", 407, ""),
		)
	}
	ingest(t, s, rows...)
	return s, from, base
}

// The hourly aggregate answers a long window exactly like the raw files do
// for the same requests, with filters and manager scope.
func TestLongWindowMatchesRaw(t *testing.T) {
	s, from, to := longWindowData(t)
	ctx := context.Background()
	scope := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	for name, q := range map[string]telemetry.EventQuery{
		"all":         {},
		"denied":      {Failures: true},
		"allowed":     {Allowed: true},
		"clients":     {ClientIDs: []string{"c-b", ""}},
		"method":      {Methods: []string{"GET"}},
		"reason":      {DenyReasons: []string{"no_rule"}},
		"destination": {Destination: "PYPI"},
		"source":      {SourceIP: "10.1.0.6"},
		"manager":     {Scope: scope},
	} {
		long := q
		long.From, long.To = from, to
		f, e := s.Facets(ctx, long, time.Hour)
		if e != nil {
			t.Fatal(name, e)
		}
		o, e := s.Outcomes(ctx, long)
		if e != nil {
			t.Fatal(name, e)
		}
		d, e := s.Domains(ctx, long)
		if e != nil {
			t.Fatal(name, e)
		}
		// The same window as three raw days.
		want := telemetry.Facets{Clients: map[string]int64{}, Methods: map[string]int64{}}
		wantOut := telemetry.Outcomes{DenyReasons: map[string]int64{}, SiteErrors: map[string]int64{}}
		wantHosts := map[string]int64{}
		var wantBytes int64
		for day := 0; day < 3; day++ {
			raw := q
			raw.From = from.Add(time.Duration(day) * 24 * time.Hour)
			raw.To = raw.From.Add(24 * time.Hour)
			rf, _ := s.Facets(ctx, raw, time.Hour)
			want.Allowed += rf.Allowed
			want.Denied += rf.Denied
			for k, v := range rf.Clients {
				want.Clients[k] += v
			}
			for k, v := range rf.Methods {
				want.Methods[k] += v
			}
			ro, _ := s.Outcomes(ctx, raw)
			for k, v := range ro.DenyReasons {
				wantOut.DenyReasons[k] += v
			}
			for k, v := range ro.SiteErrors {
				wantOut.SiteErrors[k] += v
			}
			rd, _ := s.Domains(ctx, raw)
			for _, h := range rd.Items {
				wantHosts[h.Host] += h.Requests
			}
			wantBytes += rd.Bytes
		}
		if f.Allowed != want.Allowed || f.Denied != want.Denied || !maps.Equal(f.Clients, want.Clients) || !maps.Equal(f.Methods, want.Methods) {
			t.Fatalf("%s facets long %+v raw %+v", name, f, want)
		}
		var hist int64
		for _, b := range f.Histogram {
			hist += b.Allowed + b.Denied
		}
		if hist != want.Allowed+want.Denied || len(f.Histogram) != 72 {
			t.Fatalf("%s histogram sum %d buckets %d", name, hist, len(f.Histogram))
		}
		if !maps.Equal(o.DenyReasons, wantOut.DenyReasons) || !maps.Equal(o.SiteErrors, wantOut.SiteErrors) {
			t.Fatalf("%s outcomes long %+v raw %+v", name, o, wantOut)
		}
		hosts := map[string]int64{}
		for _, h := range d.Items {
			hosts[h.Host] = h.Requests
		}
		if !maps.Equal(hosts, wantHosts) || d.Bytes != wantBytes {
			t.Fatalf("%s domains long %v/%d raw %v/%d", name, hosts, d.Bytes, wantHosts, wantBytes)
		}
	}
}

// A 30-day window is allowed and keeps answering after the raw files are gone.
func TestLongWindowThirtyDays(t *testing.T) {
	s, _, to := longWindowData(t)
	ctx := context.Background()
	q := telemetry.EventQuery{From: to.Add(-30 * 24 * time.Hour), To: to}
	f, e := s.Facets(ctx, q, time.Hour)
	if e != nil || f.Allowed+f.Denied != 3*(3+1+5) || len(f.Histogram) != 720 {
		t.Fatalf("30 days: %v %d/%d buckets %d", e, f.Allowed, f.Denied, len(f.Histogram))
	}
	d, e := s.Domains(ctx, q)
	if e != nil || d.UnmatchedSources != 2 || len(d.Items) != 3 {
		t.Fatalf("30 days domains: %v %+v", e, d)
	}
	// An unaligned window keeps the exact raw path.
	q.From = q.From.Add(time.Minute)
	if f2, e := s.Facets(ctx, q, time.Hour); e != nil || f2.Allowed != f.Allowed {
		t.Fatalf("unaligned: %v %+v", e, f2)
	}
}
