package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"squid-control.local/squid/internal/events"
	"squid-control.local/squid/internal/store"
)

func TestStatisticsAggregateAndStorageFailure(t *testing.T) {
	s, _ := fixtureServer(t)
	event := events.Event{ID: "stats-fixture", Timestamp: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), SourceIP: "192.0.2.1", Destination: "fixture.test", Method: "CONNECT", ProxyResult: "TCP_DENIED/407", Bytes: 42}
	if err := s.Store.SaveEvents([]events.Event{event}, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	// Route authorization is wired centrally by Server.Handler; this tests the
	// handler's payload and failure behavior without adding a parallel router.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/statistics", nil)
	w := httptest.NewRecorder()
	s.statistics(w, r, session{})
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var got store.Statistics
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Requests != 1 || got.Denied != 1 || got.BytesObserved != 42 || got.UniqueSources != 1 || len(got.Hourly) != 24 {
		t.Fatalf("incorrect statistics: %+v", got)
	}
	_ = s.Store.DB.Close()
	w = httptest.NewRecorder()
	s.statistics(w, r, session{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable storage returned %d", w.Code)
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.Code != "statistics_unavailable" {
		t.Fatalf("unexpected storage error: %s", w.Body.String())
	}
}
