package telemetry

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

// Ingest writes into the file of the current month (by Now), so an event of
// the last minutes of a month collected after midnight lives in the next
// month's file. Readers must still find it in a window of the old month.
func TestReadersFindEventsIngestedAfterMonthEnd(t *testing.T) {
	ctx := context.Background()
	s := testStoreAt(t, Options{}, time.Date(2026, 11, 1, 0, 5, 0, 0, time.UTC))
	at := time.Date(2026, 10, 31, 23, 55, 0, 0, time.UTC)
	client, login, reason, status := "c-1", "ci-login", "not_allowed", 403
	ev := events.Event{
		ID: "late", Timestamp: at.Format(time.RFC3339Nano), ClientID: &client,
		ClientMatchBasis: "policy_at_ingest", SourceIP: "10.1.0.5", ProxyUsername: &login,
		Destination: "late.test:443", Method: "CONNECT", ProxyResult: "TCP_DENIED/403",
		ProxyHTTPStatus: &status, Count: 1, TLSMode: "tunnel", DenyReason: &reason,
	}
	if e := s.Ingest(ctx, []events.Event{ev}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	if got := s.listMonths(); len(got) != 1 || got[0] != "2026-11" {
		t.Fatalf("fixture: event should sit in the November file, files %v", got)
	}
	q := EventQuery{From: at.Add(-55 * time.Minute), To: at.Add(4 * time.Minute)}

	d, e := s.Domains(ctx, q)
	if e != nil || len(d.Items) != 1 || d.Items[0].Host != "late.test" {
		t.Fatalf("domains: %+v %v", d.Items, e)
	}
	o, e := s.Outcomes(ctx, q)
	if e != nil || o.DenyReasons[reason] != 1 {
		t.Fatalf("outcomes: %+v %v", o, e)
	}
	l, e := s.ProxyLoginActivity(ctx, login, q.From, q.To)
	if e != nil || l.Requests != 1 {
		t.Fatalf("login activity: %+v %v", l, e)
	}
}
