package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"squid-control.local/squid/internal/events"
	"strings"
	"testing"
	"time"
)

func eventStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "events.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return s
}
func sample(id string) events.Event {
	return events.Event{ID: id, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), SourceIP: "192.0.2.1", Destination: "example.test", Method: "GET", ProxyResult: "TCP_MISS/200", TLSMode: "unknown"}
}
func TestEventPagesStayStableWhileNewRowsArrive(t *testing.T) {
	s := eventStore(t)
	id, name := "client-a", "Build agent"
	rows := []events.Event{}
	for i := 0; i < 6; i++ {
		v := sample(fmt.Sprint(i))
		if i%2 == 0 {
			v.ClientID = &id
			v.ClientName = &name
		}
		if i == 4 {
			v.ProxyResult = "TCP_DENIED/403"
		}
		rows = append(rows, v)
	}
	if e := s.SaveEvents(rows, events.Checkpoint{ObservedAt: "checkpoint"}); e != nil {
		t.Fatal(e)
	}
	first, e := s.EventPage(EventQuery{Limit: 2})
	if e != nil || first.NextCursor == nil || first.Items[0].ID != "5" {
		t.Fatalf("first: %+v %v", first, e)
	}
	if e = s.SaveEvents([]events.Event{sample("new")}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	second, e := s.EventPage(EventQuery{Limit: 2, Cursor: *first.NextCursor})
	if e != nil || len(second.Items) != 2 || second.Items[0].ID != "3" {
		t.Fatalf("cursor shifted: %+v %v", second, e)
	}
	filtered, e := s.EventPage(EventQuery{Limit: 20, ClientID: id, Failures: true})
	if e != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != "4" {
		t.Fatal("filter did not precede pagination")
	}
	if _, e = s.EventPage(EventQuery{Limit: 2, ClientID: id, Cursor: *first.NextCursor}); e != ErrCursor {
		t.Fatal("cross-filter cursor accepted")
	}
	if _, e = s.AuditPage(AuditQuery{Limit: 2, Cursor: *first.NextCursor}); e != ErrCursor {
		t.Fatal("cross-stream cursor accepted")
	}
	if _, e = s.EventPage(EventQuery{Limit: 2, Cursor: "broken"}); e != ErrCursor {
		t.Fatal("malformed cursor accepted")
	}
	clients, e := s.EventClients()
	if e != nil || len(clients) != 1 || clients[0]["name"] != name {
		t.Fatal("client choices missing")
	}
}
func TestEventCheckpointAndRowsAreAtomic(t *testing.T) {
	s := eventStore(t)
	if _, e := s.DB.Exec(`CREATE TRIGGER fail_checkpoint BEFORE INSERT ON kv WHEN NEW.key='event_checkpoint' BEGIN SELECT RAISE(ABORT,'injected failure'); END;`); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveEvents([]events.Event{sample("one")}, events.Checkpoint{ObservedAt: "new"}); e == nil {
		t.Fatal("injected failure ignored")
	}
	p, _ := s.EventPage(EventQuery{Limit: 20})
	cp, _ := s.EventCheckpoint()
	if len(p.Items) != 0 || cp.ObservedAt != "" {
		t.Fatal("partial collection committed")
	}
	_, _ = s.DB.Exec("DROP TRIGGER fail_checkpoint")
	if e := s.SaveEvents([]events.Event{sample("one")}, events.Checkpoint{ObservedAt: "new"}); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveEvents([]events.Event{sample("one")}, events.Checkpoint{ObservedAt: "new"}); e != nil {
		t.Fatal(e)
	}
	p, _ = s.EventPage(EventQuery{Limit: 20})
	cp, _ = s.EventCheckpoint()
	if len(p.Items) != 1 || cp.ObservedAt != "new" {
		t.Fatal("retry did not deduplicate")
	}
}
func TestEventRetentionAndAuditFilters(t *testing.T) {
	s := eventStore(t)
	v := sample("seed")
	b, _ := json.Marshal(v)
	_, e := s.DB.Exec(`WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<50002) INSERT INTO proxy_events(event_id,failed,ingested_at,body) SELECT printf('test-%d',x),0,?,? FROM n`, time.Now().Unix(), string(b))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveEvents(nil, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM proxy_events").Scan(&n)
	if n != EventLimit {
		t.Fatalf("cap: %d", n)
	}
	_, _ = s.DB.Exec("UPDATE proxy_events SET ingested_at=1")
	if e = s.Prune(time.Now().Add(-90*24*time.Hour), 20); e != nil {
		t.Fatal(e)
	}
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM proxy_events").Scan(&n)
	if n != 0 {
		t.Fatal("TTL not enforced")
	}
	for i := 0; i < 4; i++ {
		if e = s.Audit("<script>", "apply", fmt.Sprint(i)); e != nil {
			t.Fatal(e)
		}
	}
	p, e := s.AuditPage(AuditQuery{Limit: 2, Actor: "<script>", Action: "apply"})
	if e != nil || len(p.Items) != 2 || p.NextCursor == nil {
		t.Fatal("audit filter/page failed")
	}
	p, e = s.AuditPage(AuditQuery{Limit: 2, Actor: "<script>", Action: "apply", Cursor: *p.NextCursor})
	if e != nil || len(p.Items) != 2 || !strings.Contains(p.Items[0].Actor, "script") {
		t.Fatal("audit cursor failed")
	}
	p, e = s.AuditPage(AuditQuery{Limit: 2, Actor: "' OR 1=1 --"})
	if e != nil || len(p.Items) != 0 {
		t.Fatal("SQL filter injection")
	}
}

func TestHistoryAndCheckpointSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	cp := events.Checkpoint{Files: map[string]events.Position{"inode": {Offset: 123, Anchor: "digest", Generation: "source"}}, ObservedAt: "saved"}
	if e = s.SaveEvents([]events.Event{sample("durable")}, cp); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	got, e := s.EventCheckpoint()
	if e != nil || got.Files["inode"].Offset != 123 {
		t.Fatal("checkpoint lost")
	}
	p, e := s.EventPage(EventQuery{Limit: 20})
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != "durable" {
		t.Fatal("history lost")
	}
}

func TestEventFiltersPrecedePaginationAndBindCursor(t *testing.T) {
	s := eventStore(t)
	rows := []events.Event{}
	for i := 0; i < 5; i++ {
		v := sample(fmt.Sprint(i))
		v.Timestamp = "2026-10-28T10:30:00Z"
		if i < 3 {
			v.Destination = "repo.example.test:443"
			v.Method = "CONNECT"
			v.ProxyResult = "TCP_TUNNEL/200"
		}
		rows = append(rows, v)
	}
	if e := s.SaveEvents(rows, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	q := EventQuery{Limit: 1, SourceIP: "192.0.2.1", Destination: "EXAMPLE", Method: "CONNECT", Result: "TCP_TUNNEL/200", From: "2026-10-28T13:00:00+03:00", To: "2026-10-28T14:00:00+03:00"}
	p, e := s.EventPage(q)
	if e != nil || len(p.Items) != 1 || p.Items[0].ID != "2" || p.NextCursor == nil {
		t.Fatalf("%+v %v", p, e)
	}
	q.Cursor = *p.NextCursor
	next, e := s.EventPage(q)
	if e != nil || next.Items[0].ID != "1" {
		t.Fatalf("%+v %v", next, e)
	}
	q.Destination = "other"
	if _, e = s.EventPage(q); e != ErrCursor {
		t.Fatal("cross-filter cursor accepted", e)
	}
	q.Cursor = ""
	q.Destination = "%"
	p, e = s.EventPage(q)
	if e != nil || len(p.Items) != 0 {
		t.Fatal("wildcard treated as SQL pattern", p, e)
	}
	q.Destination = "example"
	q.To = "2026-10-28T10:30:00Z"
	p, e = s.EventPage(q)
	if e != nil || len(p.Items) != 0 {
		t.Fatal("exclusive upper time boundary", p, e)
	}
}
