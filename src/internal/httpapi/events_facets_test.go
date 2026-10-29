package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
)

// facetsBody decodes the /events/facets response into a struct the tests can
// inspect without depending on every field of the handler response.
type facetsBody struct {
	Allowed int64 `json:"allowed"`
	Denied  int64 `json:"denied"`
	Clients []struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Count int64   `json:"count"`
	} `json:"clients"`
	Methods []struct {
		Method string `json:"method"`
		Count  int64  `json:"count"`
	} `json:"methods"`
	Histogram []struct {
		Start   string `json:"start"`
		Allowed int64  `json:"allowed"`
		Denied  int64  `json:"denied"`
	} `json:"histogram"`
}

// seedFacetsEvents ingests two client events and two unknown-source events so
// scope filtering is observable in facets. Returns the fixed `now` so windows
// are deterministic.
func seedFacetsEvents(t *testing.T, s *Server) time.Time {
	t.Helper()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	c1 := "c-mine"
	c2 := "c-foreign"
	rows := []events.Event{
		{ID: "f1", Timestamp: now.Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano), SourceIP: "10.1.0.5", Destination: "a.test:443", Method: "CONNECT", ProxyResult: "TCP_TUNNEL", Bytes: 10, TLSMode: "tunnel", ClientMatchBasis: "unknown"},
		{ID: "f2", Timestamp: now.Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano), SourceIP: "10.2.0.5", Destination: "b.test:443", Method: "CONNECT", ProxyResult: "TCP_DENIED/403", Bytes: 0, TLSMode: "tunnel", ClientMatchBasis: "unknown"},
		{ID: "f3", Timestamp: now.Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano), SourceIP: "10.1.7.7", Destination: "in.test:443", Method: "CONNECT", ProxyResult: "TCP_DENIED/403", Bytes: 0, TLSMode: "tunnel", ClientMatchBasis: "unknown"},
		{ID: "f4", Timestamp: now.Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano), SourceIP: "10.9.9.9", Destination: "out.test:443", Method: "CONNECT", ProxyResult: "TCP_TUNNEL", Bytes: 5, TLSMode: "tunnel", ClientMatchBasis: "unknown"},
	}
	rows[0].ClientID, rows[0].ClientName, rows[0].ClientMatchBasis = &c1, strPtr("mine"), "policy_at_ingest"
	rows[1].ClientID, rows[1].ClientName, rows[1].ClientMatchBasis = &c2, strPtr("foreign"), "policy_at_ingest"
	if e := s.Telemetry.Ingest(context.Background(), rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	return now
}

// seedManifestClients writes two clients into the runtime manifest so the
// facets handler can resolve their names from the active snapshot.
func seedManifestClients(t *testing.T, s *Server) {
	t.Helper()
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot.Clients = []policy.Client{
		{ID: "c-mine", Name: "mine", Networks: []string{"10.1.0.0/16"}, Enabled: true, PolicyID: "p"},
		{ID: "c-foreign", Name: "foreign", Networks: []string{"10.2.0.0/16"}, Enabled: true, PolicyID: "p"},
	}
	if e = s.Runtime.State.SaveState(context.Background(), "runtime.last_good", m); e != nil {
		t.Fatal(e)
	}
}

func TestEventFacetsAdminAndManagerScope(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	seedManifestClients(t, s)
	from := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	to := now.Add(time.Minute).UTC().Format(time.RFC3339Nano)

	// Admin: 2 allowed (c-mine + 10.9.9.9) + 2 denied (c-foreign + 10.1.7.7).
	w := request(h, "GET", "/events/facets?from="+from+"&to="+to+"&step=1m", nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatalf("admin facets: %d %s", w.Code, w.Body.String())
	}
	var body facetsBody
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if body.Allowed != 2 || body.Denied != 2 {
		t.Fatalf("admin totals allowed=%d denied=%d", body.Allowed, body.Denied)
	}
	if len(body.Histogram) != 61 {
		t.Fatalf("admin histogram buckets=%d want 61", len(body.Histogram))
	}
	// Clients: c-mine(1), c-foreign(1), ""(2 unknowns).
	clientMap := map[string]int64{}
	for _, c := range body.Clients {
		clientMap[c.ID] = c.Count
	}
	if clientMap["c-mine"] != 1 || clientMap["c-foreign"] != 1 || clientMap[""] != 2 {
		t.Fatalf("admin clients %+v", clientMap)
	}
	// Names resolved from active snapshot for known clients; "" has null name.
	for _, c := range body.Clients {
		if c.ID == "c-mine" && (c.Name == nil || *c.Name != "mine") {
			t.Fatalf("c-mine name not resolved: %+v", c)
		}
		if c.ID == "" && c.Name != nil {
			t.Fatalf("unknown client name should be null: %+v", c)
		}
	}

	// Manager scope {c-mine, 10.1.0.0/16}: c-mine(1 allowed) + 10.1.7.7(1 denied);
	// c-foreign and 10.9.9.9 hidden.
	createManagerUser(t, h, cookie, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-mine"})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	w = request(h, "GET", "/events/facets?from="+from+"&to="+to+"&step=1m", nil, mgr, "", "")
	if w.Code != 200 {
		t.Fatalf("manager facets: %d %s", w.Code, w.Body.String())
	}
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if body.Allowed != 1 || body.Denied != 1 {
		t.Fatalf("manager totals allowed=%d denied=%d", body.Allowed, body.Denied)
	}
	for _, c := range body.Clients {
		if c.ID == "c-foreign" {
			t.Fatalf("manager saw foreign client in facets: %+v", c)
		}
	}
}

// TestEventFacetsClientOnlyScopeHidesUnknown verifies the rule from the
// review: a manager assigned clients but no networks must not see unmatched
// (NULL-client) sources in /events/facets, matching /events. Without the
// IS NOT NULL restriction in facetsKnown, COALESCE would fold unknown rows
// into the "" client and inflate totals.
func TestEventFacetsClientOnlyScopeHidesUnknown(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	seedManifestClients(t, s)
	from := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	to := now.Add(time.Minute).UTC().Format(time.RFC3339Nano)
	// Manager with c-mine only, no networks: unknown sources 10.1.7.7 and
	// 10.9.9.9 must be hidden; only c-mine's allowed event shows.
	createManagerUser(t, h, cookie, csrf, "mgr2", []string{}, []string{"c-mine"})
	mgr, _ := loginAs(t, h, "mgr2", "manager-password-123")
	w := request(h, "GET", "/events/facets?from="+from+"&to="+to+"&step=1m", nil, mgr, "", "")
	if w.Code != 200 {
		t.Fatalf("client-only manager facets: %d %s", w.Code, w.Body.String())
	}
	var body facetsBody
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if body.Allowed != 1 || body.Denied != 0 {
		t.Fatalf("client-only manager totals allowed=%d denied=%d, want 1/0", body.Allowed, body.Denied)
	}
	for _, c := range body.Clients {
		if c.ID == "" {
			t.Fatalf("client-only manager saw unknown sources in facets: %+v", c)
		}
		if c.ID == "c-foreign" {
			t.Fatalf("client-only manager saw foreign client in facets: %+v", c)
		}
	}
}

func TestEventFacetsBadParams(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	from := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	to := now.Add(time.Minute).UTC().Format(time.RFC3339Nano)
	cases := []struct {
		name string
		path string
	}{
		{"missing from", "/events/facets?to=" + to + "&step=1m"},
		{"missing to", "/events/facets?from=" + from + "&step=1m"},
		{"missing step", "/events/facets?from=" + from + "&to=" + to},
		{"bad step", "/events/facets?from=" + from + "&to=" + to + "&step=5m"},
		{"bad from", "/events/facets?from=not-a-date&to=" + to + "&step=1m"},
		{"to before from", "/events/facets?from=" + to + "&to=" + from + "&step=1m"},
		{"window over 7 days", "/events/facets?from=" + from + "&to=" + now.Add(8*24*time.Hour).UTC().Format(time.RFC3339Nano) + "&step=1m"},
		{"invalid source_ip", "/events/facets?from=" + from + "&to=" + to + "&step=1m&source_ip=invalid"},
		{"invalid failures_only", "/events/facets?from=" + from + "&to=" + to + "&step=1m&failures_only=maybe"},
	}
	for _, tc := range cases {
		w := request(h, "GET", tc.path, nil, cookie, "", "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d %s", tc.name, w.Code, w.Body.String())
		}
		var v struct {
			Code string `json:"code"`
		}
		json.Unmarshal(w.Body.Bytes(), &v)
		if v.Code != "invalid_filter" {
			t.Fatalf("%s: want invalid_filter, got %s", tc.name, v.Code)
		}
	}
}
