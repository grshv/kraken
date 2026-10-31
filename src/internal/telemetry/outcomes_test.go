package telemetry_test

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/telemetry"
)

func outcomeEv(at time.Time, client, src, result string, status int, reason string) events.Event {
	e := ev(at, client, src, "a.example", false, 1)
	e.ProxyResult = result
	e.ProxyHTTPStatus = &status
	if status == 0 {
		e.ProxyHTTPStatus = nil
	}
	if reason != "" {
		e.DenyReason = &reason
	}
	return e
}

func TestOutcomesDenyReasonsAndSiteErrors(t *testing.T) {
	c := &clock{t: base}
	s := open(t, t.TempDir(), c, nil)
	ctx := context.Background()
	at := base.Add(-time.Hour)
	rows := []events.Event{
		outcomeEv(at, "c1", "10.1.0.5", "TCP_DENIED/403", 403, "private"),
		outcomeEv(at.Add(time.Second), "c1", "10.1.0.5", "TCP_DENIED/403", 403, "private"),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_DENIED/403", 403, "no_rule"),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_DENIED/407", 407, ""),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_DENIED/403", 403, ""),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_MISS/502", 502, ""),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_MISS/404", 404, ""),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_SWAPFAIL_MISS/000", 0, ""),
		outcomeEv(at, "c1", "10.1.0.5", "TCP_TUNNEL/200", 200, ""),
		outcomeEv(at, "", "10.9.9.9", "TCP_DENIED/403", 403, "no_rule"),
		outcomeEv(at, "", "10.1.7.7", "TCP_DENIED/403", 403, "no_rule"),
	}
	if e := s.Ingest(ctx, rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	q := telemetry.EventQuery{From: base.Add(-24 * time.Hour), To: base}
	o, e := s.Outcomes(ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]int64{"private": 2, "no_rule": 3, "proxy_auth": 1, "other": 1}
	for k, n := range want {
		if o.DenyReasons[k] != n {
			t.Fatalf("deny reasons: %+v", o.DenyReasons)
		}
	}
	if o.SiteErrors["5xx"] != 1 || o.SiteErrors["4xx"] != 1 || o.SiteErrors["network"] != 1 || len(o.SiteErrors) != 3 {
		t.Fatalf("site errors: %+v", o.SiteErrors)
	}
	// Manager: his client plus unmatched sources inside his networks only.
	q.Scope = &telemetry.Scope{ClientIDs: []string{"c1"}, Networks: []string{"10.1.0.0/16"}}
	if o, e = s.Outcomes(ctx, q); e != nil || o.DenyReasons["no_rule"] != 2 {
		t.Fatalf("scoped: %+v %v", o.DenyReasons, e)
	}
	// Filtered by reason, the journal returns only those events.
	p, e := s.Events(ctx, telemetry.EventQuery{DenyReasons: []string{"private"}, Limit: 50})
	if e != nil || len(p.Items) != 2 || p.Items[0].DenyReason == nil || *p.Items[0].DenyReason != "private" {
		t.Fatalf("events by reason: %+v %v", p.Items, e)
	}
	if _, e := s.Outcomes(ctx, telemetry.EventQuery{From: base.Add(-8 * 24 * time.Hour), To: base}); e != telemetry.ErrFacetsWindow {
		t.Fatalf("window: %v", e)
	}
}
