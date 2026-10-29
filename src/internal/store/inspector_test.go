package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

func mkEvent(id string, at time.Time, result string, bytes int64, clientID, dest string) events.Event {
	v := sample(id)
	v.Timestamp = at.UTC().Format(time.RFC3339Nano)
	v.ProxyResult = result
	v.Bytes = bytes
	v.Destination = dest
	v.Method = "GET"
	if clientID != "" {
		c := clientID
		v.ClientID = &c
	}
	return v
}

// alignIngestedAt sets ingested_at to each event's timestamp so the coarse
// ingested_at bound honours an injected clock that differs from wall time.
func alignIngestedAt(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.DB.Exec(`UPDATE proxy_events SET ingested_at=CAST(strftime('%s', json_extract(body,'$.timestamp')) AS INTEGER)`); e != nil {
		t.Fatal(e)
	}
}

func TestInspectorClientStatsEmpty(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	got, err := s.InspectorClientStats(context.Background(), now, "c1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != 0 || got.Denied != 0 || got.Bytes != 0 || len(got.Buckets) != 96 || len(got.TopDestinations) != 0 {
		t.Fatalf("unexpected empty stats: %+v", got)
	}
}

func TestInspectorClientStatsWindowsBucketsDeniedAndTop(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	status403 := 403
	rows := []events.Event{
		mkEvent("in-from", from, "TCP_MISS/200", 10, "c1", "a.test"),
		mkEvent("in-now", now, "TCP_MISS/200", 20, "c1", "a.test:443"),
		mkEvent("before", from.Add(-time.Second), "TCP_MISS/200", 100, "c1", "b.test"),
		mkEvent("after", now.Add(time.Second), "TCP_MISS/200", 100, "c1", "b.test"),
		mkEvent("denied", from.Add(2*time.Hour), "TCP_DENIED/403", 5, "c1", "a.test"),
		mkEvent("origin403", from.Add(3*time.Hour), "TCP_MISS/403", 7, "c1", "c.test"),
		mkEvent("other", from.Add(time.Hour), "TCP_MISS/200", 1, "other-client", "x.test"),
	}
	rows[5].ProxyHTTPStatus = &status403
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	got, err := s.InspectorClientStats(context.Background(), now, "c1", nil)
	if err != nil {
		t.Fatal(err)
	}
	// in-from, in-now, denied, origin403 = 4 requests; other-client excluded.
	if got.Requests != 4 {
		t.Fatalf("requests=%d", got.Requests)
	}
	// denied (TCP_DENIED/403) and origin403 (status>=400) = 2 denied.
	if got.Denied != 2 {
		t.Fatalf("denied=%d", got.Denied)
	}
	if got.Bytes != 42 {
		t.Fatalf("bytes=%d", got.Bytes)
	}
	// in-from is at the lower boundary -> bucket 0; in-now at now -> bucket 95.
	if got.Buckets[0].Requests != 1 || got.Buckets[95].Requests != 1 {
		t.Fatalf("boundary buckets wrong: 0=%+v 95=%+v", got.Buckets[0], got.Buckets[95])
	}
	// denied is 2h after from -> bucket 8; origin403 3h after -> bucket 12.
	if got.Buckets[8].Requests != 1 || got.Buckets[8].Denied != 1 {
		t.Fatalf("bucket 8: %+v", got.Buckets[8])
	}
	if got.Buckets[12].Requests != 1 || got.Buckets[12].Denied != 1 {
		t.Fatalf("bucket 12: %+v", got.Buckets[12])
	}
	// top destinations: a.test has 3 (in-from, in-now, denied), c.test 1, b.test excluded.
	if len(got.TopDestinations) != 2 || got.TopDestinations[0].Host != "a.test" || got.TopDestinations[0].Requests != 3 || got.TopDestinations[0].Denied != 1 {
		t.Fatalf("top destinations: %+v", got.TopDestinations)
	}
}

func TestInspectorClientStatsStorageErrorsAreNotZeroes(t *testing.T) {
	s := eventStore(t)
	now := time.Now()
	v := sample("bad")
	v.Timestamp = now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	cid := "c1"
	v.ClientID = &cid
	if err := s.SaveEvents([]events.Event{v}, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE proxy_events SET body='{'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectorClientStats(context.Background(), now, "c1", nil); err == nil {
		t.Fatal("corrupt storage represented as successful stats")
	}
}

func TestInspectorPolicyEventsSevenDayWindow(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	from7 := now.Add(-7 * 24 * time.Hour)
	rows := []events.Event{
		mkEvent("in-24h", now.Add(-2*time.Hour), "TCP_MISS/200", 1, "c1", "a.test"),
		mkEvent("in-7d", from7.Add(time.Hour), "TCP_MISS/200", 1, "c1", "b.test"),
		mkEvent("before-7d", from7.Add(-time.Second), "TCP_MISS/200", 1, "c1", "c.test"),
		mkEvent("at-7d", from7, "TCP_MISS/200", 1, "c1", "d.test"),
		mkEvent("at-now", now, "TCP_MISS/200", 1, "c1", "e.test"),
		mkEvent("other-client", now.Add(-time.Hour), "TCP_MISS/200", 1, "c2", "f.test"),
	}
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	got, err := s.InspectorPolicyEvents(context.Background(), now, []string{"c1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 events in [now-7d, now], got %d", len(got))
	}
	// nil clientIDs returns nothing.
	got, err = s.InspectorPolicyEvents(context.Background(), now, nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("nil clientIDs: %d %v", len(got), err)
	}
}

func TestInspectorUnknownEventsWindow(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []events.Event{
		mkEvent("unknown-in", now.Add(-time.Hour), "TCP_DENIED/403", 1, "", "a.test"),
		mkEvent("unknown-out", now.Add(-25*time.Hour), "TCP_MISS/200", 1, "", "b.test"),
		mkEvent("known", now.Add(-time.Hour), "TCP_MISS/200", 1, "c1", "c.test"),
	}
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	got, err := s.InspectorUnknownEvents(context.Background(), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceIP != "192.0.2.1" {
		t.Fatalf("unknown events: %+v", got)
	}
}

func TestInspectorRecentEventsWindow(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []events.Event{
		mkEvent("in", now.Add(-time.Hour), "TCP_MISS/200", 1, "c1", "a.test"),
		mkEvent("out", now.Add(-25*time.Hour), "TCP_MISS/200", 1, "c1", "b.test"),
	}
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	got, err := s.InspectorRecentEvents(context.Background(), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Destination != "a.test" {
		t.Fatalf("recent events: %+v", got)
	}
}

func TestInspectorEventByID(t *testing.T) {
	s := eventStore(t)
	if err := s.SaveEvents([]events.Event{sample("ev-1")}, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.InspectorEvent(context.Background(), "ev-1")
	if err != nil || got.ID != "ev-1" {
		t.Fatalf("event by id: %+v %v", got, err)
	}
	if _, err := s.InspectorEvent(context.Background(), "missing"); err == nil {
		t.Fatal("missing event returned no error")
	}
}

func TestInspectorInstanceStoreMethods(t *testing.T) {
	s := eventStore(t)
	if ts, ok, err := s.LastEventTimestamp(context.Background()); err != nil || ok {
		t.Fatalf("empty last event: %q %v %v", ts, ok, err)
	}
	if ts, ok, err := s.LastAppliedAt(context.Background()); err != nil || ok {
		t.Fatalf("empty last applied: %q %v %v", ts, ok, err)
	}
	if dir := s.DataDir(); dir == "" {
		t.Fatal("data dir not resolved for a file-backed db")
	}
	now := time.Now().UTC()
	v := sample("last")
	v.Timestamp = now.Format(time.RFC3339Nano)
	if err := s.SaveEvents([]events.Event{v}, events.Checkpoint{ObservedAt: "cp-time"}); err != nil {
		t.Fatal(err)
	}
	ts, ok, err := s.LastEventTimestamp(context.Background())
	if err != nil || !ok || !strings.HasPrefix(ts, now.Format("2006-01-02T15:04:05")) {
		t.Fatalf("last event timestamp: %q %v %v", ts, ok, err)
	}
	if cp, err := s.EventCheckpoint(); err != nil || cp.ObservedAt != "cp-time" {
		t.Fatalf("checkpoint: %+v %v", cp, err)
	}
	// A succeeded apply operation surfaces its updated_at.
	op := struct {
		ID      string `json:"id"`
		Action  string `json:"action"`
		Status  string `json:"status"`
		Updated string `json:"updated_at"`
	}{"op-1", "apply", "succeeded", "2026-10-29T10:00:00Z"}
	body, _ := json.Marshal(op)
	if err := s.InsertOperation("op-1", string(body), "k-1", "h", "admin", "apply"); err != nil {
		t.Fatal(err)
	}
	ts, ok, err = s.LastAppliedAt(context.Background())
	if err != nil || !ok || ts != "2026-10-29T10:00:00Z" {
		t.Fatalf("last applied: %q %v %v", ts, ok, err)
	}
	// A non-apply succeeded operation does not surface.
	op2 := struct {
		ID      string `json:"id"`
		Action  string `json:"action"`
		Status  string `json:"status"`
		Updated string `json:"updated_at"`
	}{"op-2", "validate", "succeeded", "2026-10-29T11:00:00Z"}
	body2, _ := json.Marshal(op2)
	if err := s.InsertOperation("op-2", string(body2), "k-2", "h2", "admin", "validate"); err != nil {
		t.Fatal(err)
	}
	ts, _, _ = s.LastAppliedAt(context.Background())
	if ts != "2026-10-29T10:00:00Z" {
		t.Fatalf("validate should not override apply: %q", ts)
	}
}

func TestInspectorClientStatsFiftyThousandEventsUnderASecond(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	v := sample("bulk")
	v.Timestamp = now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	v.Destination = "bulk.test:443"
	cid := "c1"
	v.ClientID = &cid
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	nowUnix := now.Unix()
	// Retain 50000 events spread linearly over the 30-day retention window so
	// the (client_id, ingested_at) index selects only the ~1/30 that fall in
	// the last 24h coarse bound; Go then processes ~1600 rows, not all 50000.
	// ingested_at is aligned to the body timestamp afterwards so the coarse
	// bound stays a safe superset (ingested_at >= Event.Timestamp).
	if _, err = s.DB.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO proxy_events(event_id,client_id,client_name,failed,ingested_at,body) SELECT printf('ev-%d',x),?,?,0,?-(x*2592000/50000),? FROM n`, EventLimit, cid, "", nowUnix, string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE proxy_events SET body=json_set(body,'$.timestamp',strftime('%Y-%m-%dT%H:%M:%SZ',ingested_at,'unixepoch'))`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := s.InspectorClientStats(context.Background(), now, "c1", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests == 0 {
		t.Fatalf("expected events in 24h window, got 0")
	}
	if elapsed >= time.Second {
		t.Fatalf("50000 retained events took %s; expected under 1s", elapsed)
	}
	t.Logf("50000 retained events (%d in 24h) inspector stats in %s", got.Requests, elapsed)
}
