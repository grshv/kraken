package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

// seedEventsForScope writes two access-log lines attributed to two different
// clients, so a manager with only one client sees just his events.
func seedEventsForScope(t *testing.T, h http.Handler, s *Server, myClientID, foreignClientID string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "access.log")
	s.Runtime.Options.LogPath = path
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		t.Fatal(e)
	}
	prof := m.Snapshot.TLSProfiles[0].ID
	m.Snapshot.Policies = append(m.Snapshot.Policies, policy.Policy{ID: "p-evt", Name: "evt", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, TLSProfileID: prof})
	m.Snapshot.Clients = append(m.Snapshot.Clients,
		policy.Client{ID: myClientID, Name: "mine", Networks: []string{"10.1.0.0/16"}, Enabled: true, PolicyID: "p-evt"},
		policy.Client{ID: foreignClientID, Name: "foreign", Networks: []string{"10.2.0.0/16"}, Enabled: true, PolicyID: "p-evt"},
	)
	b, _ := json.Marshal(m)
	if e = os.WriteFile(filepath.Join(s.Runtime.Dir, "lkg.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	// Two events: one from my client's network, one from the foreign client's.
	// Plus an unmatched source inside the manager's networks. Use recent
	// timestamps so the 24h statistics window includes them.
	now := time.Now().Unix()
	line := stampLine(now, "10.1.0.5", "TCP_DENIED/403 42 CONNECT example.test:443 - NONE/- -") +
		stampLine(now+1, "10.2.0.5", "TCP_MISS/200 42 GET http://example.test/path - NONE/- -") +
		stampLine(now+2, "10.1.0.99", "TCP_MISS/200 42 GET http://example.test/x - NONE/- -")
	if e = os.WriteFile(path, []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CollectEvents(); e != nil {
		t.Fatal(e)
	}
}

func stampLine(ts int64, ip, rest string) string {
	return formatStamp(ts) + " 5 " + ip + " " + rest + "\n"
}

func formatStamp(ts int64) string {
	return strconv.FormatInt(ts, 10) + ".000"
}

// TestManagerEventFiltering checks a manager sees only his clients' events and
// unmatched sources inside his networks; a foreign client_id query returns 404.
func TestManagerEventFiltering(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedEventsForScope(t, h, s, "c-mine-evt", "c-foreign-evt")
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-mine-evt"})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	// Manager lists events: sees his client + unmatched source in his network.
	w := request(h, "GET", "/events?limit=10", nil, mgr, "", "")
	var page store.Page[events.Event]
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
		t.Fatal(e)
	}
	for _, ev := range page.Items {
		if ev.ClientID != nil && *ev.ClientID != "c-mine-evt" {
			t.Fatalf("manager saw foreign client event: %+v", ev)
		}
		// Unmatched events must come from inside the manager's networks.
		if ev.ClientID == nil {
			if ev.SourceIP != "" && !startsWith(ev.SourceIP, "10.1.") {
				t.Fatalf("manager saw unmatched foreign source: %+v", ev)
			}
		}
	}
	// Querying a foreign client_id directly → 404.
	if w := request(h, "GET", "/events?client_id=c-foreign-evt", nil, mgr, "", ""); w.Code != 404 {
		t.Fatalf("expected 404 for foreign client_id, got %d", w.Code)
	}
	// /events/clients returns only the manager's clients.
	w = request(h, "GET", "/events/clients", nil, mgr, "", "")
	var clients struct {
		Items []map[string]string `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &clients)
	for _, c := range clients.Items {
		if c["id"] != "c-mine-evt" {
			t.Fatalf("manager saw foreign client label: %+v", c)
		}
	}
}

// TestManagerAuditAndStatisticsFiltering checks audit and statistics only
// include the manager's actor/entity scope and aggregated client activity.
func TestManagerAuditAndStatisticsFiltering(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	seedEventsForScope(t, h, s, "c-mine-aud", "c-foreign-aud")
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-mine-aud"})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")
	// Statistics: manager sees requests attributed to his client + unmatched in
	// his networks, not the foreign client's traffic.
	w := request(h, "GET", "/statistics", nil, mgr, "", "")
	var stats map[string]any
	json.Unmarshal(w.Body.Bytes(), &stats)
	if reqs, ok := stats["requests"].(float64); !ok || reqs < 1 {
		t.Fatalf("manager statistics missing his events: %+v", stats)
	}
	// Audit: manager sees his own actor records + entities in his scope. Admin
	// writes an audit row about the foreign client; manager must not see it.
	s.Store.Audit("admin", "update_client", "c-foreign-aud")
	s.Store.Audit("mgr", "login", "")
	w = request(h, "GET", "/audit?limit=50", nil, mgr, "", "")
	var ap store.Page[store.AuditEvent]
	json.Unmarshal(w.Body.Bytes(), &ap)
	for _, ev := range ap.Items {
		// Foreign-client entity must not appear.
		if ev.Entity == "c-foreign-aud" && ev.Actor != "mgr" {
			t.Fatalf("manager saw foreign audit entity: %+v", ev)
		}
	}
}

// TestManagerRollbackAdminRequired verifies POST /operations with action
// rollback by a manager returns 403 admin_required.
func TestManagerRollbackAdminRequired(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a"})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	body := `{"action":"rollback","revision_id":"any","expected_applied_revision":null}`
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(body))
	r.AddCookie(mgr)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", mgrCSRF)
	r.Header.Set("Idempotency-Key", "mgr-rollback-test-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatalf("expected 403 admin_required for manager rollback, got %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["code"] != "admin_required" {
		t.Fatalf("expected admin_required, got %v", resp["code"])
	}
}

// TestManagerPolicySharedOutOfScope verifies editing a policy referenced by a
// foreign client returns 403 policy_shared_out_of_scope. This is exercised via
// the draft merge path: the manager's input changes a shared policy, and the
// merge refuses because foreign clients reference it.
func TestManagerPolicySharedOutOfScope(t *testing.T) {
	_, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	myCID, foreignCID, polID := seedDraftWithTwoClients(t, h, admin, csrf)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{myCID})
	mgr, mgrCSRF := loginAs(t, h, "mgr", "manager-password-123")
	_ = foreignCID
	// Manager GETs his view (which includes the shared policy since his client
	// references it), then edits the policy's destination. The foreign client
	// also references it, so the merge must refuse.
	w := request(h, "GET", "/configuration/draft", nil, mgr, "", "")
	var view Draft
	json.Unmarshal(w.Body.Bytes(), &view)
	for i := range view.Snapshot.Policies {
		if view.Snapshot.Policies[i].ID == polID {
			view.Snapshot.Policies[i].Destinations = []policy.Destination{{Domain: "changed.test", IncludeSubdomains: false}}
		}
	}
	w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": view.Version, "snapshot": view.Snapshot}, mgr, mgrCSRF, "https://panel.test")
	if w.Code != 403 {
		t.Fatalf("expected 403 policy_shared_out_of_scope, got %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["code"] != "policy_shared_out_of_scope" {
		t.Fatalf("expected policy_shared_out_of_scope, got %v body=%s", body["code"], w.Body.String())
	}
}

func startsWith(s, prefix string) bool { return len(s) >= len(prefix) && s[:len(prefix)] == prefix }
