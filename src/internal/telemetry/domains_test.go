package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"kraken.local/kraken/internal/telemetry"
)

func TestDomainsAcrossMonthsAndScope(t *testing.T) {
	oct := time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC)
	c := &clock{t: oct}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	ingest(t, s,
		ev(oct.Add(-time.Hour), "c-a", "10.1.0.5", "pypi.org", false, 100),
		ev(oct.Add(-50*time.Minute), "c-a", "10.1.0.6", "pypi.org", true, 0),
		ev(oct.Add(-40*time.Minute), "", "10.9.9.9", "pypi.org", true, 0),
		ev(oct.Add(-30*time.Minute), "c-b", "10.2.0.5", "github.com", false, 7),
	)
	nov := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	c.set(nov)
	ingest(t, s, ev(nov.Add(-time.Minute), "c-a", "10.1.0.5", "pypi.org", false, 20))

	rep, e := s.Domains(ctx, telemetry.EventQuery{From: oct.Add(-2 * time.Hour), To: nov})
	if e != nil || rep.Truncated {
		t.Fatalf("domains: %v truncated=%v", e, rep.Truncated)
	}
	items := rep.Items
	// Totals cover every request: per client, unmatched sources by address.
	if rep.Requests != 5 || rep.Denied != 2 || rep.Bytes != 127 || rep.UnmatchedSources != 1 ||
		rep.Clients["c-a"] != (telemetry.Counts{Requests: 3, Denied: 1, Bytes: 120}) || rep.Clients[""].Requests != 1 {
		t.Fatalf("totals %+v", rep)
	}
	if len(items) != 2 || items[0].Host != "pypi.org" {
		t.Fatalf("order %+v", items)
	}
	p := items[0]
	if p.Requests != 4 || p.Denied != 2 || p.Bytes != 120 || p.Sources != 3 || !p.Last.Equal(nov.Add(-time.Minute)) {
		t.Fatalf("pypi.org %+v", p)
	}
	if p.Top[0].SourceIP != "10.1.0.5" || p.Top[0].Requests != 2 || p.Top[0].ClientID != "c-a" {
		t.Fatalf("top source %+v", p.Top[0])
	}

	// Manager: his client, unknown sources only inside his networks.
	scope := &telemetry.Scope{ClientIDs: []string{"c-a"}, Networks: []string{"10.1.0.0/16"}}
	rep, _ = s.Domains(ctx, telemetry.EventQuery{From: oct.Add(-2 * time.Hour), To: nov, Scope: scope})
	items = rep.Items
	if len(items) != 1 || items[0].Requests != 3 || items[0].Sources != 2 {
		t.Fatalf("manager %+v", items)
	}
	// A foreign client only: nothing.
	rep, _ = s.Domains(ctx, telemetry.EventQuery{From: oct.Add(-2 * time.Hour), To: nov, Scope: scope, ClientIDs: []string{"c-b"}})
	items = rep.Items
	if len(items) != 0 {
		t.Fatalf("foreign client %+v", items)
	}
	// Empty scope: nothing.
	rep, _ = s.Domains(ctx, telemetry.EventQuery{From: oct.Add(-2 * time.Hour), To: nov, Scope: &telemetry.Scope{}})
	items = rep.Items
	if len(items) != 0 {
		t.Fatalf("empty scope %+v", items)
	}
}

func TestDomainsWindowErrors(t *testing.T) {
	s := open(t, t.TempDir(), &clock{t: base}, nil)
	for name, q := range map[string]telemetry.EventQuery{
		"missing from": {To: base},
		"reversed":     {From: base, To: base.Add(-time.Minute)},
		"over 7 days":  {From: base, To: base.Add(7*24*time.Hour + time.Minute)},
	} {
		if _, e := s.Domains(context.Background(), q); !errors.Is(e, telemetry.ErrFacetsWindow) {
			t.Fatalf("%s: %v", name, e)
		}
	}
}
