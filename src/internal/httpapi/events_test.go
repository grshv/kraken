package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

func TestEventCollectionAttributionPrivacyAndRetry(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	path := filepath.Join(t.TempDir(), "access.log")
	s.Runtime.Options.LogPath = path
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	m.Snapshot.Clients = []policy.Client{{ID: "00000000-0000-4000-8000-000000000099", Name: "fixture client", Networks: []string{"192.0.2.0/24"}}}
	b, _ := json.Marshal(m)
	if e = os.WriteFile(filepath.Join(s.Runtime.Dir, "lkg.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	line := "1793059200.000 5 192.0.2.10 TCP_DENIED/403 42 CONNECT example.test:443 - NONE/- -\n1793059201.000 1 198.51.100.10 TCP_MISS/200 42 GET http://user:password@example.test/path?token=secret - NONE/- -\n"
	if e = os.WriteFile(path, []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.CollectEvents(); e != nil {
		t.Fatal(e)
	}
	w := request(h, "GET", "/events?limit=1", nil, cookie, "", "")
	var page store.Page[events.Event]
	if e = json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(page.Items) != 1 || page.Items[0].Destination != "example.test" || page.Items[0].ClientID != nil || page.NextCursor == nil {
		t.Fatal(w.Body.String())
	}
	next := request(h, "GET", "/events?limit=1&cursor="+*page.NextCursor, nil, cookie, "", "")
	_ = json.Unmarshal(next.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].ClientName == nil || *page.Items[0].ClientName != "fixture client" || page.Items[0].RevisionID != nil || page.Items[0].ClientMatchBasis != "policy_at_ingest" {
		t.Fatal(next.Body.String())
	}
	for _, route := range []string{"/events", "/events/clients", "/audit"} {
		if w := request(h, "GET", route, nil, nil, "", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected %s", route)
		}
	}
	if w := request(h, "GET", "/events?client_id=00000000-0000-4000-8000-000000000099&failures_only=true", nil, cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, route := range []string{"/events?source_ip=invalid", "/events?from=bad", "/events?from=2026-10-28T12:00:00Z&to=2026-10-28T11:00:00Z", "/events?cursor=broken", "/events?limit=201", "/events?failures_only=maybe", "/audit?cursor=broken"} {
		if w := request(h, "GET", route, nil, cookie, "", ""); w.Code != 400 {
			t.Fatalf("invalid query %s: %d", route, w.Code)
		}
	}
	// Changed labels never rewrite persisted history.
	m.Snapshot.Clients = nil
	b, _ = json.Marshal(m)
	_ = os.WriteFile(filepath.Join(s.Runtime.Dir, "lkg.json"), b, 0600)
	if e = s.CollectEvents(); e != nil {
		t.Fatal(e)
	}
	p, e := s.Store.EventPage(store.EventQuery{Limit: 20})
	if e != nil || len(p.Items) != 2 || p.Items[1].ClientName == nil {
		t.Fatal("history replayed or reassigned")
	}
	if w := request(h, "GET", "/audit?actor=admin&action=login", nil, cookie, "", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestHistorySurvivesSourceFailureAndReportsFreshness(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	stored := events.Event{ID: "saved", Timestamp: "2026-10-27T00:00:00Z", SourceIP: "192.0.2.1", Destination: "fixture.test", Method: "GET", ProxyResult: "TCP_MISS/200", TLSMode: "unknown", ClientMatchBasis: "unknown"}
	cp := events.Checkpoint{ObservedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), SourceAvailable: true}
	if e := s.Store.SaveEvents([]events.Event{stored}, cp); e != nil {
		t.Fatal(e)
	}
	s.Runtime.Options.LogPath = t.TempDir() // a directory is not a valid log source
	if e := s.CollectEvents(); e == nil {
		t.Fatal("invalid source ignored")
	}
	w := request(h, "GET", "/events", nil, cookie, "", "")
	var body struct {
		Items     []events.Event `json:"items"`
		Ingestion struct {
			Stale     bool `json:"stale"`
			Available bool `json:"source_available"`
		} `json:"ingestion"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if w.Code != 200 || len(body.Items) != 1 || !body.Ingestion.Stale {
		t.Fatal("failed reader fabricated fresh/empty data: " + w.Body.String())
	}
	s.Runtime.Options.LogPath = filepath.Join(t.TempDir(), "not-created.log")
	if e := s.CollectEvents(); e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", "/events", nil, cookie, "", "")
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if len(body.Items) != 1 || body.Ingestion.Available || body.Ingestion.Stale {
		t.Fatal("missing source not visible: " + w.Body.String())
	}
}

// TestAuditByEntityFilter covers GET /audit?entity_id=: admin sees only rows
// for that entity (including update_draft per-entity rows), an over-long
// entity_id is rejected with 400 invalid_filter, and a manager querying a
// foreign client's entity gets an empty list (not 404).
func TestAuditByEntityFilter(t *testing.T) {
	s, h := fixtureServer(t)
	admin, acsrf := loginTest(t, h)
	myCID, foreignCID, _ := seedDraftWithTwoClients(t, h, admin, acsrf)
	// Editing myCID in the draft writes an update_draft audit row whose
	// entity_id is myCID (per-entity, alongside the legacy "draft" row).
	d := acceptDraft(t, h, admin)
	for i := range d.Snapshot.Clients {
		if d.Snapshot.Clients[i].ID == myCID {
			d.Snapshot.Clients[i].Description = "audited change"
		}
	}
	acceptCode(t, acceptPut(h, admin, acsrf, d), 200, "")
	// Admin filters audit by that entity_id: only rows for myCID.
	w := request(h, "GET", "/audit?entity_id="+myCID+"&limit=50", nil, admin, "", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page store.Page[store.AuditEvent]
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) == 0 {
		t.Fatalf("no audit rows for %s", myCID)
	}
	for _, ev := range page.Items {
		if ev.Entity != myCID {
			t.Fatalf("entity filter leaked %q: %+v", ev.Entity, ev)
		}
		if ev.Action != "update_draft" {
			t.Fatalf("unexpected action %q for %s", ev.Action, myCID)
		}
	}
	// Confirm the update_draft per-entity row exists for myCID directly.
	var n int
	if e := s.Store.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='update_draft' AND entity_id=?", myCID).Scan(&n); e != nil || n == 0 {
		t.Fatalf("missing update_draft audit row for %s (n=%d)", myCID, n)
	}
	// A foreign-client entity_id returns no rows for admin either (foreignCID
	// was never changed), proving the filter is exact, not a wildcard.
	w = request(h, "GET", "/audit?entity_id="+foreignCID+"&limit=50", nil, admin, "", "")
	json.Unmarshal(w.Body.Bytes(), &page)
	for _, ev := range page.Items {
		if ev.Entity != foreignCID {
			t.Fatalf("foreign filter leaked %q: %+v", ev.Entity, ev)
		}
	}
	// Over-long entity_id → 400 invalid_filter.
	long := strings.Repeat("a", 257)
	if w := request(h, "GET", "/audit?entity_id="+long, nil, admin, "", ""); w.Code != 400 {
		t.Fatalf("over-long entity_id: %d %s", w.Code, w.Body.String())
	}
	// Manager: querying a foreign client's entity returns an empty list, not 404.
	createManagerUser(t, h, admin, acsrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	w = request(h, "GET", "/audit?entity_id="+foreignCID+"&limit=50", nil, mgr, "", "")
	if w.Code != 200 {
		t.Fatalf("manager foreign entity_id: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 0 {
		t.Fatalf("manager saw foreign audit entity: %+v", page.Items)
	}
}
