package store

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

func TestEventScopeInScope(t *testing.T) {
	c1 := "c1"
	c2 := "c2"
	sc := &EventScope{ClientIDs: []string{"c1"}, Networks: []string{"10.1.0.0/16"}}
	cases := []struct {
		name string
		ev   events.Event
		want bool
	}{
		{"nil scope admits all", events.Event{SourceIP: "10.9.9.9"}, true}, // nil handled below
		{"own client", events.Event{ClientID: &c1, SourceIP: "10.9.9.9"}, true},
		{"foreign client", events.Event{ClientID: &c2, SourceIP: "10.1.0.5"}, false},
		{"unknown in network", events.Event{SourceIP: "10.1.0.5"}, true},
		{"unknown outside network", events.Event{SourceIP: "10.2.0.5"}, false},
		{"unknown unparseable ip", events.Event{SourceIP: "not-an-ip"}, false},
		{"ipv4-mapped in network", events.Event{SourceIP: "::ffff:10.1.0.5"}, true},
		{"ipv4-mapped outside network", events.Event{SourceIP: "::ffff:10.2.0.5"}, false},
	}
	for _, c := range cases {
		scope := sc
		if c.name == "nil scope admits all" {
			scope = nil
		}
		if got := inScope(c.ev, scope); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}

	// Empty lists (non-nil scope) admit nothing: no client matches and no
	// network matches.
	empty := &EventScope{}
	if inScope(events.Event{ClientID: &c1, SourceIP: "10.1.0.5"}, empty) {
		t.Fatal("empty ClientIDs admitted a client event")
	}
	if inScope(events.Event{SourceIP: "10.1.0.5"}, empty) {
		t.Fatal("empty Networks admitted an unknown event")
	}

	// A single address in Networks matches exactly that address (unmapped).
	addr := &EventScope{Networks: []string{"10.1.0.5"}}
	if !inScope(events.Event{SourceIP: "10.1.0.5"}, addr) {
		t.Fatal("single address match failed")
	}
	if inScope(events.Event{SourceIP: "10.1.0.6"}, addr) {
		t.Fatal("single address admitted a different ip")
	}
	if !inScope(events.Event{SourceIP: "::ffff:10.1.0.5"}, addr) {
		t.Fatal("ipv4-mapped single address match failed")
	}

	// scopeAllowsClient: nil admits all, non-nil admits only listed.
	if !scopeAllowsClient(nil, "anything") {
		t.Fatal("nil scopeAllowsClient should admit any client")
	}
	if !scopeAllowsClient(sc, "c1") || scopeAllowsClient(sc, "c2") {
		t.Fatal("scopeAllowsClient client filter wrong")
	}
	if scopeAllowsClient(empty, "c1") {
		t.Fatal("empty scopeAllowsClient admitted a client")
	}

	// scopeFilterClients: nil passes through, non-nil intersects.
	if got := scopeFilterClients(nil, []string{"a", "b"}); len(got) != 2 {
		t.Fatalf("nil scopeFilterClients should pass through, got %v", got)
	}
	if got := scopeFilterClients(sc, []string{"c1", "c2", "c3"}); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("scopeFilterClients intersect wrong: %v", got)
	}
	if got := scopeFilterClients(empty, []string{"c1"}); len(got) != 0 {
		t.Fatalf("empty scopeFilterClients admitted: %v", got)
	}
}

func TestInspectorClientStatsRespectsEventScope(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []events.Event{
		mkEvent("mine", now.Add(-time.Hour), "TCP_MISS/200", 1, "c1", "a.test"),
		mkEvent("theirs", now.Add(-time.Hour), "TCP_MISS/200", 1, "c2", "b.test"),
	}
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	// Admin sees the client's events.
	got, err := s.InspectorClientStats(context.Background(), now, "c1", nil)
	if err != nil || got.Requests != 1 {
		t.Fatalf("admin client stats: %+v %v", got, err)
	}
	// Scope with empty ClientIDs hides the client: scopeAllowsClient is false,
	// so the store returns empty stats without querying.
	got, err = s.InspectorClientStats(context.Background(), now, "c1", &EventScope{})
	if err != nil || got.Requests != 0 {
		t.Fatalf("empty-scope client stats: %+v %v", got, err)
	}
	// Scope listing c1 admits the client's events.
	got, err = s.InspectorClientStats(context.Background(), now, "c1", &EventScope{ClientIDs: []string{"c1"}})
	if err != nil || got.Requests != 1 {
		t.Fatalf("scoped client stats: %+v %v", got, err)
	}
	// Scope listing only c2 hides c1.
	got, err = s.InspectorClientStats(context.Background(), now, "c1", &EventScope{ClientIDs: []string{"c2"}})
	if err != nil || got.Requests != 0 {
		t.Fatalf("foreign-scope client stats: %+v %v", got, err)
	}
}

func TestInspectorUnknownEventsRespectEventScope(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rows := []events.Event{
		mkEvent("uk-in", now.Add(-time.Hour), "TCP_DENIED/403", 1, "", "a.test"),
		mkEvent("uk-out", now.Add(-time.Hour), "TCP_DENIED/403", 1, "", "b.test"),
	}
	// Distinct source IPs: one inside the manager network, one outside.
	rows[0].SourceIP = "10.1.0.5"
	rows[1].SourceIP = "10.2.0.5"
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	alignIngestedAt(t, s)
	// Admin sees both unknown sources.
	got, err := s.InspectorUnknownEvents(context.Background(), now, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("admin unknown: %d %v", len(got), err)
	}
	// Manager sees only the source inside his network.
	got, err = s.InspectorUnknownEvents(context.Background(), now, &EventScope{Networks: []string{"10.1.0.0/16"}})
	if err != nil || len(got) != 1 || got[0].SourceIP != "10.1.0.5" {
		t.Fatalf("scoped unknown: %+v %v", got, err)
	}
	// Empty networks hide everything.
	got, err = s.InspectorUnknownEvents(context.Background(), now, &EventScope{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty-scope unknown: %d %v", len(got), err)
	}
}

func TestInspectorEventRespectsEventScope(t *testing.T) {
	s := eventStore(t)
	now := time.Now().UTC()
	mine := mkEvent("ev-mine", now.Add(-time.Hour), "TCP_MISS/200", 1, "c1", "a.test")
	foreign := mkEvent("ev-foreign", now.Add(-time.Hour), "TCP_MISS/200", 1, "c2", "b.test")
	ukIn := mkEvent("ev-uk-in", now.Add(-time.Hour), "TCP_DENIED/403", 0, "", "c.test")
	ukIn.SourceIP = "10.1.0.5"
	ukOut := mkEvent("ev-uk-out", now.Add(-time.Hour), "TCP_DENIED/403", 0, "", "d.test")
	ukOut.SourceIP = "10.9.9.9"
	if err := s.SaveEvents([]events.Event{mine, foreign, ukIn, ukOut}, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	sc := &EventScope{ClientIDs: []string{"c1"}, Networks: []string{"10.1.0.0/16"}}
	// Admin reads every event by ID.
	for _, id := range []string{"ev-mine", "ev-foreign", "ev-uk-in", "ev-uk-out"} {
		if _, err := s.InspectorEvent(context.Background(), id, nil); err != nil {
			t.Fatalf("admin %s: %v", id, err)
		}
	}
	// Manager reads his client's event and in-network unknown; foreign and
	// out-of-network unknown surface as sql.ErrNoRows (handler maps to 404).
	if _, err := s.InspectorEvent(context.Background(), "ev-mine", sc); err != nil {
		t.Fatalf("manager ev-mine: %v", err)
	}
	if _, err := s.InspectorEvent(context.Background(), "ev-uk-in", sc); err != nil {
		t.Fatalf("manager ev-uk-in: %v", err)
	}
	if _, err := s.InspectorEvent(context.Background(), "ev-foreign", sc); err == nil {
		t.Fatal("manager ev-foreign should be hidden as ErrNoRows")
	}
	if _, err := s.InspectorEvent(context.Background(), "ev-uk-out", sc); err == nil {
		t.Fatal("manager ev-uk-out should be hidden as ErrNoRows")
	}
	// Empty scope hides every event.
	empty := &EventScope{}
	for _, id := range []string{"ev-mine", "ev-foreign", "ev-uk-in", "ev-uk-out"} {
		if _, err := s.InspectorEvent(context.Background(), id, empty); err == nil {
			t.Fatalf("empty scope admitted %s", id)
		}
	}
}
