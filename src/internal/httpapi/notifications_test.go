package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
)

type notificationsResp struct {
	Counts map[string]int `json:"counts"`
	Items  []notification `json:"items"`
}

func listNotifications(t *testing.T, h http.Handler, cookie *http.Cookie, query string) notificationsResp {
	t.Helper()
	var v notificationsResp
	if code := getJSON(t, h, cookie, "/notifications"+query, &v); code != 200 {
		t.Fatalf("GET /notifications%s: %d", query, code)
	}
	return v
}

func notificationIDs(v notificationsResp) map[string]notification {
	out := map[string]notification{}
	for _, n := range v.Items {
		out[n.ID] = n
	}
	return out
}

// notificationsFixture extends inspectorFixture with c-a denials that must not
// become notifications: a protected address and a blocked-for-all domain.
func notificationsFixture(t *testing.T) (*Server, http.Handler, *http.Cookie, string) {
	t.Helper()
	s, h, cookie, csrf := inspectorFixture(t)
	cid, cname := "c-a", "alpha"
	rows := []events.Event{
		{ID: "ev-n-prot", Timestamp: ts(inspectorClock, time.Hour), ClientID: &cid, ClientName: &cname, SourceIP: "10.1.0.5", Destination: "169.254.169.254:80", Method: "GET", ProxyResult: "TCP_DENIED", ProxyHTTPStatus: ptrInt(403), TLSMode: "tunnel"},
		{ID: "ev-n-block", Timestamp: ts(inspectorClock, time.Hour), ClientID: &cid, ClientName: &cname, SourceIP: "10.1.0.5", Destination: "blocked.example:443", Method: "CONNECT", ProxyResult: "TCP_DENIED", ProxyHTTPStatus: ptrInt(403), TLSMode: "tunnel"},
		{ID: "ev-n-old", Timestamp: ts(inspectorClock, 8*24*time.Hour), ClientID: &cid, ClientName: &cname, SourceIP: "10.1.0.5", Destination: "old.example:443", Method: "CONNECT", ProxyResult: "TCP_DENIED", ProxyHTTPStatus: ptrInt(403), TLSMode: "tunnel"},
	}
	if e := s.Telemetry.Ingest(context.Background(), rows, events.Checkpoint{SourceAvailable: true}); e != nil {
		t.Fatal(e)
	}
	return s, h, cookie, csrf
}

func TestNotificationsComputed(t *testing.T) {
	_, h, admin, _ := notificationsFixture(t)
	v := listNotifications(t, h, admin, "")
	got := notificationIDs(v)
	d, ok := got["denied/c-a/github.com"]
	if !ok || d.Kind != "denied_domain" || d.Denied != 2 || d.Status != "new" || d.ClientName == nil || *d.ClientName != "alpha" {
		t.Fatalf("denied github.com: %+v in %+v", d, v.Items)
	}
	if src, ok := got["source/10.9.9.9"]; !ok || src.Kind != "unknown_source" {
		t.Fatalf("unknown source missing: %+v", v.Items)
	}
	for _, id := range []string{"denied/c-a/169.254.169.254", "denied/c-a/blocked.example", "denied/c-a/old.example", "denied/c-a/archive.ubuntu.com"} {
		if _, ok := got[id]; ok {
			t.Fatalf("%s must not be a notification", id)
		}
	}
	if len(v.Items) != 2 || v.Counts["new"] != 2 {
		t.Fatalf("items/counts: %+v %+v", v.Items, v.Counts)
	}
	if c := listNotifications(t, h, admin, "?client_id=c-a"); len(c.Items) != 1 || c.Items[0].ID != "denied/c-a/github.com" {
		t.Fatalf("client filter: %+v", c.Items)
	}
	if c := listNotifications(t, h, admin, "?kind=unknown_source"); len(c.Items) != 1 {
		t.Fatalf("kind filter: %+v", c.Items)
	}
	if w := request(h, "GET", "/notifications?status=bogus", nil, admin, "", ""); w.Code != 400 {
		t.Fatalf("bad status: %d", w.Code)
	}
}

func TestNotificationsMarks(t *testing.T) {
	s, h, admin, csrf := notificationsFixture(t)
	mark := func(ids []string, status string) int {
		return request(h, "POST", "/notifications/marks", map[string]any{"ids": ids, "status": status}, admin, csrf, "https://panel.test").Code
	}
	if c := mark([]string{"denied/c-a/github.com"}, "seen"); c != 200 {
		t.Fatalf("mark seen: %d", c)
	}
	if c := mark([]string{"source/10.9.9.9"}, "ignored"); c != 200 {
		t.Fatalf("mark ignored: %d", c)
	}
	v := listNotifications(t, h, admin, "")
	got := notificationIDs(v)
	seen := got["denied/c-a/github.com"]
	if seen.Status != "seen" || seen.MarkedBy == nil || *seen.MarkedBy != "admin" || seen.MarkedAt == nil {
		t.Fatalf("seen mark: %+v", seen)
	}
	if _, ok := got["source/10.9.9.9"]; ok {
		t.Fatal("ignored item must be hidden by default")
	}
	if v.Counts["new"] != 0 || v.Counts["seen"] != 1 || v.Counts["ignored"] != 1 {
		t.Fatalf("counts: %+v", v.Counts)
	}
	if ig := listNotifications(t, h, admin, "?status=ignored"); len(ig.Items) != 1 || ig.Items[0].ID != "source/10.9.9.9" {
		t.Fatalf("ignored filter: %+v", ig.Items)
	}
	if all := listNotifications(t, h, admin, "?status=all"); len(all.Items) != 2 {
		t.Fatalf("all: %+v", all.Items)
	}
	// "new" removes the mark.
	if c := mark([]string{"denied/c-a/github.com", "source/10.9.9.9"}, "new"); c != 200 {
		t.Fatalf("mark new: %d", c)
	}
	if v := listNotifications(t, h, admin, ""); v.Counts["new"] != 2 {
		t.Fatalf("after unmark: %+v", v.Counts)
	}
	var audit struct {
		Items []struct {
			Action   string `json:"action"`
			EntityID string `json:"entity_id"`
		} `json:"items"`
	}
	getJSON(t, h, admin, "/audit?action=mark_notifications", &audit)
	if len(audit.Items) != 3 {
		t.Fatalf("audit rows: %+v", audit.Items)
	}
	_ = s
}

func TestNotificationsMarkValidation(t *testing.T) {
	_, h, admin, csrf := notificationsFixture(t)
	many := make([]string, 501)
	for i := range many {
		many[i] = "source/10.0.0.1"
	}
	cases := []struct {
		name   string
		ids    []string
		status string
	}{
		{"empty", nil, "seen"},
		{"too many", many, "seen"},
		{"bad status", []string{"source/10.0.0.1"}, "done"},
		{"bad prefix", []string{"client/c-a"}, "seen"},
		{"bad ip", []string{"source/not-an-ip"}, "seen"},
		{"no host", []string{"denied/c-a/"}, "seen"},
		{"host with slash", []string{"denied/c-a/a/b"}, "seen"},
		{"too long", []string{"denied/c-a/" + strings.Repeat("a", 300)}, "seen"},
	}
	for _, c := range cases {
		w := request(h, "POST", "/notifications/marks", map[string]any{"ids": c.ids, "status": c.status}, admin, csrf, "https://panel.test")
		if w.Code != 400 {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
	}
	if w := request(h, "POST", "/notifications/marks", map[string]any{"ids": []string{"source/10.0.0.1"}, "status": "seen"}, admin, "", "https://panel.test"); w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
}

func TestNotificationsManagerScope(t *testing.T) {
	_, h, admin, csrf := notificationsFixture(t)
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-a"})
	createManagerUser(t, h, admin, csrf, "empty", []string{}, []string{})
	mgr, mcsrf := loginAs(t, h, "mgr", "manager-password-123")
	v := listNotifications(t, h, mgr, "?status=all")
	if len(v.Items) != 1 || v.Items[0].ID != "denied/c-a/github.com" {
		t.Fatalf("manager sees: %+v", v.Items)
	}
	mark := func(id string) int {
		return request(h, "POST", "/notifications/marks", map[string]any{"ids": []string{id}, "status": "seen"}, mgr, mcsrf, "https://panel.test").Code
	}
	if c := mark("denied/c-a/github.com"); c != 200 {
		t.Fatalf("own client: %d", c)
	}
	if c := mark("source/10.9.9.9"); c != 400 {
		t.Fatalf("foreign network: %d", c)
	}
	if c := mark("denied/c-all/github.com"); c != 400 {
		t.Fatalf("foreign client: %d", c)
	}
	if c := mark("source/10.1.2.3"); c != 200 {
		t.Fatalf("own network: %d", c)
	}
	empty, _ := loginAs(t, h, "empty", "manager-password-123")
	if v := listNotifications(t, h, empty, "?status=all"); len(v.Items) != 0 {
		t.Fatalf("empty scope sees: %+v", v.Items)
	}
}

// A denial is a notification only while the client policy does not cover the
// host; adding the domain, all_web, a disabled client, protection and
// blocked-for-all all make it disappear (it is computed, not stored).
func TestMissingFromPolicy(t *testing.T) {
	snap := inspectorSnapshot()
	st := policy.EffectiveSettings(snap)
	c := snap.Clients[0] // c-a, allowlist archive.ubuntu.com, pypi.org
	cases := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"archive.ubuntu.com", false},
		{"files.pypi.org", true}, // pypi.org without subdomains
		{"blocked.example", false},
		{"169.254.169.254", false},
		{"93.184.216.34", false}, // IP literals are not domains to allow
	}
	for _, k := range cases {
		if got := missingFromPolicy(snap, st, &c, k.host); got != k.want {
			t.Errorf("%s: %v, want %v", k.host, got, k.want)
		}
	}
	snap.Policies[0].Destinations = append(snap.Policies[0].Destinations, policy.Destination{Domain: "github.com"})
	if missingFromPolicy(snap, st, &c, "github.com") {
		t.Error("allowed domain still missing")
	}
	all := snap.Clients[3]
	if missingFromPolicy(snap, st, &all, "github.com") {
		t.Error("all_web client has nothing missing")
	}
	dis := snap.Clients[1]
	if missingFromPolicy(snap, st, &dis, "github.com") {
		t.Error("disabled client denials are not missing domains")
	}
}
