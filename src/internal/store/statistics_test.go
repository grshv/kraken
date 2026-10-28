package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"squid-control.local/squid/internal/events"
)

func TestEventStatisticsEmptyWindowAndUTC(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 27, 18, 12, 34, 123, time.FixedZone("fixture", 3*3600))
	got, err := s.EventStatistics(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.From != now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano) || got.To != now.UTC().Format(time.RFC3339Nano) || got.ObservedAt != got.To {
		t.Fatalf("incorrect UTC window: %+v", got)
	}
	if got.Requests != 0 || got.Denied != 0 || got.BytesObserved != 0 || got.UniqueSources != 0 || len(got.Hourly) != 24 || got.RetentionLimited {
		t.Fatalf("incorrect empty statistics: %+v", got)
	}
	for i, hour := range got.Hourly {
		wantFrom := now.Add(time.Duration(i-24) * time.Hour).UTC().Format(time.RFC3339Nano)
		wantTo := now.Add(time.Duration(i-23) * time.Hour).UTC().Format(time.RFC3339Nano)
		if hour.From != wantFrom || hour.To != wantTo || hour.Requests != 0 || hour.Denied != 0 {
			t.Fatalf("incorrect hour %d: %+v", i, hour)
		}
	}
}

func TestEventStatisticsEventTimeBoundariesDenialsAndBytes(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 27, 13, 42, 18, 0, time.UTC)
	from := now.Add(-24 * time.Hour)
	makeEvent := func(id string, at time.Time, source, result string, bytes int64) events.Event {
		v := sample(id)
		v.Timestamp, v.SourceIP, v.ProxyResult, v.Bytes = at.Format(time.RFC3339Nano), source, result, bytes
		return v
	}
	status403, status407 := 403, 407
	rows := []events.Event{
		makeEvent("start", from, "192.0.2.1", "TCP_DENIED/403", 10),
		makeEvent("auth", from.Add(time.Hour), "192.0.2.1", "TCP_DENIED/407", 20),
		makeEvent("origin-forbidden", now.Add(-time.Nanosecond), "192.0.2.2", "TCP_MISS/403", 30),
		makeEvent("before", from.Add(-time.Nanosecond), "192.0.2.4", "TCP_DENIED/403", 100),
		makeEvent("end", now, "192.0.2.5", "TCP_MISS/200", 100),
		makeEvent("future", now.Add(time.Nanosecond), "192.0.2.6", "TCP_MISS/200", 100),
		makeEvent("null-bytes", from.Add(2*time.Hour), "192.0.2.3", "TCP_TUNNEL/200", 40),
	}
	rows[0].ProxyHTTPStatus = &status403
	rows[1].ProxyHTTPStatus = &status407
	rows[2].ProxyHTTPStatus = &status403
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	// Old/imported nullable byte fields mean no observed byte value, not an
	// invented transfer size. Ingestion time is now for all rows, unlike event time.
	if _, err := s.DB.Exec(`UPDATE proxy_events SET body=json_set(body,'$.bytes_observed',NULL) WHERE event_id='null-bytes'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventStatistics(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != 4 || got.Denied != 2 || got.BytesObserved != 60 || got.UniqueSources != 3 {
		t.Fatalf("incorrect aggregate: %+v", got)
	}
	for i, hour := range got.Hourly {
		wantRequests, wantDenied := int64(0), int64(0)
		if i == 0 || i == 1 || i == 2 || i == 23 {
			wantRequests = 1
		}
		if i == 0 || i == 1 {
			wantDenied = 1
		}
		if hour.Requests != wantRequests || hour.Denied != wantDenied {
			t.Fatalf("incorrect bucket %d: %+v", i, hour)
		}
	}
}

func TestEventStatisticsStorageErrorsAreNotZeroes(t *testing.T) {
	for _, corruption := range []string{"malformed", "timestamp", "negative-bytes", "overflow"} {
		t.Run(corruption, func(t *testing.T) {
			s := eventStore(t)
			now := time.Now()
			v := sample("first")
			v.Timestamp = now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			if corruption == "negative-bytes" {
				v.Bytes = -1
			}
			if corruption == "overflow" {
				v.Bytes = math.MaxInt64
			}
			if err := s.SaveEvents([]events.Event{v}, events.Checkpoint{}); err != nil {
				t.Fatal(err)
			}
			if corruption == "malformed" {
				_, _ = s.DB.Exec(`UPDATE proxy_events SET body='{'`)
			}
			if corruption == "timestamp" {
				_, _ = s.DB.Exec(`UPDATE proxy_events SET body=json_set(body,'$.timestamp','broken')`)
			}
			if corruption == "overflow" {
				v.ID, v.Bytes = "second", 1
				if err := s.SaveEvents([]events.Event{v}, events.Checkpoint{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.EventStatistics(context.Background(), now); err == nil {
				t.Fatal("corrupt storage represented as successful statistics")
			}
		})
	}
	s := eventStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.EventStatistics(ctx, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation ignored: %v", err)
	}
	_ = s.DB.Close()
	if _, err := s.EventStatistics(context.Background(), time.Now()); err == nil {
		t.Fatal("closed database represented as successful statistics")
	}
}

func TestEventStatisticsRetentionBoundIsExplicit(t *testing.T) {
	s := eventStore(t)
	now := time.Now()
	v := sample("bounded")
	v.Timestamp = now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<=?) INSERT INTO proxy_events(event_id,failed,ingested_at,body) SELECT CAST(x AS TEXT),0,?,? FROM n`, EventLimit, now.Unix(), string(body)); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventStatistics(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests != EventLimit || !got.RetentionLimited || got.RecordLimit != EventLimit {
		t.Fatalf("unbounded/incomplete statistics not labelled: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "source_ip") || strings.Contains(string(encoded), "destination") {
		t.Fatal("aggregate exposed raw event data")
	}
}
