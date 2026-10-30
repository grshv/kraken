package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
)

// inspectorClock is the fixed time inspectorFixture drives every window off.
// Tests seed event timestamps relative to it so window boundaries are exact.
var inspectorClock = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

// inspectorSnapshot is the policy applied as the active revision by
// inspectorFixture: two policies (allowlist + all_web) and four clients
// covering every explain reason (enabled, disabled, auth-bound, all_web).
// The auth-bound client requires a TLS-transport profile, so p-allow references
// one; the all_web policy keeps the plaintext HTTP profile.
func inspectorSnapshot() policy.Snapshot {
	st := policy.DefaultSettings()
	st.BlockedDomains = []policy.Destination{{Domain: "blocked.example"}}
	http := "00000000-0000-4000-8000-000000000001"
	tlsRef := "proxy-server"
	tls := "00000000-0000-4000-8000-000000000002"
	return policy.Snapshot{
		Version:  1,
		Mode:     "structured",
		Settings: &st,
		TLSProfiles: []policy.TLSProfile{
			{ID: http, Name: "HTTP proxy / CONNECT", Mode: "tunnel", Transport: "http"},
			{ID: tls, Name: "TLS proxy / CONNECT", Mode: "tunnel", Transport: "tls", CertRef: &tlsRef},
		},
		Policies: []policy.Policy{
			{ID: "p-allow", Name: "allow", Mode: "allowlist", TLSProfileID: tls, Destinations: []policy.Destination{{Domain: "archive.ubuntu.com"}, {Domain: "pypi.org"}}},
			{ID: "p-all", Name: "all", Mode: "all_web", TLSProfileID: http},
		},
		Clients: []policy.Client{
			{ID: "c-a", Name: "alpha", Kind: "host", Networks: []string{"10.1.0.5/32"}, Enabled: true, PolicyID: "p-allow"},
			{ID: "c-dis", Name: "dis", Kind: "host", Networks: []string{"10.2.0.5/32"}, Enabled: false, PolicyID: "p-allow"},
			{ID: "c-auth", Name: "auth", Kind: "host", Networks: []string{"10.3.0.5/32"}, Enabled: true, PolicyID: "p-allow", ProxyUsername: "bob"},
			{ID: "c-all", Name: "allc", Kind: "host", Networks: []string{"10.4.0.5/32"}, Enabled: true, PolicyID: "p-all"},
		},
	}
}

// applySnapshot puts a snapshot into the draft, creates a revision, validates
// and applies it through the real HTTP operation flow so the active manifest
// (lkg.json) carries the policy. Tests that rely on the active policy use this
// rather than writing lkg.json directly.
func applySnapshot(t *testing.T, h http.Handler, s *Server, cookie *http.Cookie, csrf, label string, snap policy.Snapshot) {
	t.Helper()
	d := getDraft(t, h, cookie)
	w := request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": snap}, cookie, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatalf("applySnapshot(%s) PUT draft: %d %s", label, w.Code, w.Body.String())
	}
	d = getDraft(t, h, cookie)
	w = request(h, "POST", "/revisions", map[string]int{"expected_draft_version": d.Version}, cookie, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatalf("applySnapshot(%s) POST revisions: %d %s", label, w.Code, w.Body.String())
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	// The applied revision the operations must expect: null on the first apply.
	var st struct {
		Applied *string `json:"applied_revision"`
	}
	getJSON(t, h, cookie, "/status", &st)
	expected := "null"
	if st.Applied != nil && *st.Applied != "" {
		expected = `"` + *st.Applied + `"`
	}
	vop := postOp(t, h, cookie, csrf, `{"action":"validate","revision_id":"`+rev.ID+`","expected_applied_revision":`+expected+`}`, "inspector-validate-"+label)
	s.Wait()
	vop = getOp(t, h, cookie, vop.ID)
	if vop.Status != "validated" {
		t.Fatalf("applySnapshot(%s) validate: %s %s", label, vop.Status, vop.Findings)
	}
	aop := postOp(t, h, cookie, csrf, `{"action":"apply","revision_id":"`+rev.ID+`","expected_applied_revision":`+expected+`,"validation_operation_id":"`+vop.ID+`"}`, "inspector-apply-"+label)
	s.Wait()
	aop = getOp(t, h, cookie, aop.ID)
	if aop.Status != "succeeded" {
		t.Fatalf("applySnapshot(%s) apply: %s %s", label, aop.Status, aop.Findings)
	}
}

type handler = http.Handler

// inspectorFixture boots a server, applies inspectorSnapshot as the active
// revision, pins the inspector clock, and seeds the event corpus every test
// below asserts against. Each test gets a fresh fixture (fresh temp dir).
func inspectorFixture(t *testing.T) (*Server, handler, *http.Cookie, string) {
	t.Helper()
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	// The auth-bound client requires a TLS-transport profile, which in turn
	// requires non-empty proxy cert/key paths on the runtime options. Validate
	// only checks they are set; the mocked Squid parse never reads the files.
	s.Runtime.Options.ProxyCert = filepath.Join(t.TempDir(), "proxy.crt")
	s.Runtime.Options.ProxyKey = filepath.Join(t.TempDir(), "proxy.key")
	applySnapshot(t, h, s, cookie, csrf, "base", inspectorSnapshot())
	s.now = func() time.Time { return inspectorClock }

	now := inspectorClock
	far := now.Add(-30 * 24 * time.Hour) // outside every window; explain-only events
	cid, cname := "c-a", "alpha"
	mk := func(id string, ago time.Duration, client bool, src, dst, result string, code int, bytes int64, user string) events.Event {
		e := events.Event{ID: id, Timestamp: ts(now, ago), SourceIP: src, Destination: dst, Method: "CONNECT", ProxyResult: result, ProxyHTTPStatus: ptrInt(code), Bytes: bytes, TLSMode: "tunnel"}
		if client {
			e.ClientID, e.ClientName, e.ClientMatchBasis = &cid, &cname, "source_ip"
		}
		if user != "" {
			u := user
			e.ProxyUsername = &u
		}
		return e
	}
	rows := []events.Event{
		// c-a events driving client-stats / policy-hits / draft-impact.
		mk("ev-cs1", 10*time.Minute, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 1000, ""),
		mk("ev-cs2", 20*time.Minute, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 2000, ""),
		mk("ev-cs3", 30*time.Minute, true, "10.1.0.5", "github.com:443", "TCP_DENIED", 403, 0, ""),
		mk("ev-cs4", 3*time.Hour, true, "10.1.0.5", "github.com:443", "TCP_DENIED", 403, 0, ""),
		mk("ev-cs5", 24*time.Hour, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 500, ""),   // 24h lower boundary, included
		mk("ev-cs6", -1*time.Second, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 100, ""), // just after now, excluded
		mk("ev-cs7", 25*time.Hour, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 100, ""),   // outside 24h, inside 7d
		mk("ev-cs8", 3*24*time.Hour, true, "10.1.0.5", "pypi.org:443", "TCP_TUNNEL", 200, 300, ""),           // inside 7d, outside 24h
		mk("ev-cs9", 8*24*time.Hour, true, "10.1.0.5", "pypi.org:443", "TCP_TUNNEL", 200, 300, ""),           // outside 7d
		mk("ev-cs10", 7*24*time.Hour, true, "10.1.0.5", "pypi.org:443", "TCP_TUNNEL", 200, 300, ""),          // 7d lower boundary, included
		mk("ev-cs11", 0, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 1000, ""),            // exactly now, included
		// Unknown-source events (10.9.9.9 has no client).
		mk("ev-uk1", 10*time.Minute, false, "10.9.9.9", "github.com:443", "TCP_DENIED", 403, 0, ""),
		mk("ev-uk2", 20*time.Minute, false, "10.9.9.9", "evil.test:443", "TCP_TUNNEL", 200, 100, ""),
		mk("ev-uk3", 25*time.Hour, false, "10.9.9.9", "github.com:443", "TCP_DENIED", 403, 0, ""), // outside 24h
		// Explain events at far (outside every window; read by ID only).
		// explain re-derives the client by SourceIP, so no ClientID is stored.
		setTime(mk("ev-ex-unknown", 0, false, "10.9.9.9", "archive.ubuntu.com:443", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-protected", 0, false, "10.1.0.5", "169.254.169.254:80", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-blocked", 0, false, "10.1.0.5", "blocked.example:443", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-disabled", 0, false, "10.2.0.5", "archive.ubuntu.com:443", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-authfail", 0, false, "10.3.0.5", "archive.ubuntu.com:443", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-notinallow", 0, false, "10.1.0.5", "github.com:443", "TCP_DENIED", 403, 0, ""), far),
		setTime(mk("ev-ex-allowed", 0, false, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 0, ""), far),
		setTime(mk("ev-ex-allweb", 0, false, "10.4.0.5", "random.test:443", "TCP_TUNNEL", 200, 0, ""), far),
	}
	for i := range rows {
		if rows[i].ClientID != nil && *rows[i].ClientID == "" {
			rows[i].ClientID = nil
		}
	}
	if e := s.Telemetry.Ingest(context.Background(), rows, events.Checkpoint{SourceAvailable: true}); e != nil {
		t.Fatal(e)
	}
	return s, h, cookie, csrf
}

// setTime returns e with its timestamp overridden (used for the far-past
// explain events whose ago offset is irrelevant once the absolute time is set).
func setTime(e events.Event, t time.Time) events.Event {
	e.Timestamp = t.UTC().Format(time.RFC3339Nano)
	return e
}

func ts(now time.Time, ago time.Duration) string { return now.Add(-ago).UTC().Format(time.RFC3339Nano) }
func ptrInt(n int) *int                          { return &n }
func ptrStr(s string) *string                    { return &s }

func getDraft(t *testing.T, h handler, cookie *http.Cookie) Draft {
	t.Helper()
	w := request(h, "GET", "/configuration/draft", nil, cookie, "", "")
	var d Draft
	if e := json.Unmarshal(w.Body.Bytes(), &d); e != nil {
		t.Fatal(e)
	}
	return d
}

func getOp(t *testing.T, h handler, cookie *http.Cookie, id string) Operation {
	t.Helper()
	w := request(h, "GET", "/operations/"+id, nil, cookie, "", "")
	var op Operation
	json.Unmarshal(w.Body.Bytes(), &op)
	return op
}

// postOp sends a POST /operations request with an Idempotency-Key, which the
// shared request helper does not set.
func postOp(t *testing.T, h handler, cookie *http.Cookie, csrf, body, idem string) Operation {
	t.Helper()
	r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(body))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://panel.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Idempotency-Key", idem)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 202 {
		t.Fatalf("POST /operations: %d %s", rec.Code, rec.Body.String())
	}
	var op Operation
	json.Unmarshal(rec.Body.Bytes(), &op)
	return op
}

func getJSON(t *testing.T, h handler, cookie *http.Cookie, path string, v any) int {
	t.Helper()
	w := request(h, "GET", path, nil, cookie, "", "")
	if w.Code == 200 {
		if e := json.Unmarshal(w.Body.Bytes(), v); e != nil {
			t.Fatalf("%s: %v %s", path, e, w.Body.String())
		}
	}
	return w.Code
}

func TestInspectorRequiresSession(t *testing.T) {
	_, h, _, _ := inspectorFixture(t)
	for _, p := range []string{
		"/inspector/clients/c-a/stats",
		"/inspector/policies/p-allow/hits",
		"/inspector/unknown-sources",
		"/inspector/events/ev-cs1/explain",
		"/inspector/draft-impact",
		"/inspector/domain-templates",
	} {
		if w := request(h, "GET", p, nil, nil, "", ""); w.Code != 401 {
			t.Fatalf("%s anonymous: %d", p, w.Code)
		}
	}
}

func TestInspectorClientStatsWindows(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	var v struct {
		WindowHours int   `json:"window_hours"`
		Requests    int   `json:"requests"`
		Denied      int   `json:"denied"`
		Bytes       int64 `json:"bytes"`
		Series      []struct {
			Start    string `json:"start"`
			Requests int    `json:"requests"`
			Denied   int    `json:"denied"`
		} `json:"series"`
		Top []struct {
			Host     string `json:"host"`
			Requests int    `json:"requests"`
			Denied   int    `json:"denied"`
		} `json:"top_destinations"`
	}
	if code := getJSON(t, h, c, "/inspector/clients/c-a/stats", &v); code != 200 {
		t.Fatal(code)
	}
	// 24h window [now-24h, now] includes cs1,cs2,cs3,cs4,cs5,cs11 (6); cs6
	// (now+1s) and cs7 (now-25h) are excluded.
	if v.WindowHours != 24 || v.Requests != 6 || v.Denied != 2 || v.Bytes != 4500 {
		t.Fatalf("totals: %+v", v)
	}
	if len(v.Series) != 96 {
		t.Fatalf("series buckets: %d", len(v.Series))
	}
	sum := 0
	for i, b := range v.Series {
		sum += b.Requests
		if i > 0 && b.Start <= v.Series[i-1].Start {
			t.Fatal("series not ascending")
		}
	}
	if sum != 6 {
		t.Fatalf("series sum %d", sum)
	}
	// Boundary: cs5 at now-24h (=from) lands in bucket 0; cs11 at now lands in
	// bucket 95 (the last bucket, which holds the current moment).
	if v.Series[0].Requests != 1 {
		t.Fatalf("bucket 0 (24h lower boundary): %+v", v.Series[0])
	}
	if v.Series[95].Requests != 2 { // cs1 (now-10min) and cs11 (now)
		t.Fatalf("bucket 95: %+v", v.Series[95])
	}
	// archive.ubuntu.com: cs1,cs2,cs5,cs11 (4, 0 denied); github.com: cs3,cs4 (2, 2 denied).
	if len(v.Top) != 2 || v.Top[0].Host != "archive.ubuntu.com" || v.Top[0].Requests != 4 || v.Top[0].Denied != 0 {
		t.Fatalf("top: %+v", v.Top)
	}
	if v.Top[1].Host != "github.com" || v.Top[1].Requests != 2 || v.Top[1].Denied != 2 {
		t.Fatalf("top[1]: %+v", v.Top[1])
	}
	// Unknown client -> 404.
	if code := getJSON(t, h, c, "/inspector/clients/missing/stats", &v); code != 404 {
		t.Fatalf("missing client: %d", code)
	}
}

func TestInspectorPolicyHitsWindows(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	var v struct {
		Domains []struct {
			Domain string `json:"domain"`
			H24    int    `json:"hits_24h"`
			H7     int    `json:"hits_7d"`
		} `json:"domains"`
	}
	if code := getJSON(t, h, c, "/inspector/policies/p-allow/hits", &v); code != 200 {
		t.Fatal(code)
	}
	got := map[string][2]int{}
	for _, d := range v.Domains {
		got[d.Domain] = [2]int{d.H24, d.H7}
	}
	// archive.ubuntu.com: 24h=[cs1,cs2,cs5,cs11]=4; 7d adds cs7 (now-25h)=5.
	// pypi.org: 24h=0; 7d=[cs8(now-3d),cs10(now-7d boundary)]=2; cs9(now-8d) excluded.
	if got["archive.ubuntu.com"] != [2]int{4, 5} {
		t.Fatalf("archive hits: %+v", got["archive.ubuntu.com"])
	}
	if got["pypi.org"] != [2]int{0, 2} {
		t.Fatalf("pypi hits: %+v", got["pypi.org"])
	}
	if code := getJSON(t, h, c, "/inspector/policies/missing/hits", &v); code != 404 {
		t.Fatalf("missing policy: %d", code)
	}
}

func TestInspectorUnknownSources(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	var v struct {
		Sources []struct {
			IP       string `json:"source_ip"`
			Requests int    `json:"requests"`
			Denied   int    `json:"denied"`
			Last     string `json:"last_seen"`
			Top      []struct {
				Host     string `json:"host"`
				Requests int    `json:"requests"`
				Denied   int    `json:"denied"`
			} `json:"top_destinations"`
		} `json:"sources"`
	}
	if code := getJSON(t, h, c, "/inspector/unknown-sources", &v); code != 200 {
		t.Fatal(code)
	}
	// 10.9.9.9 has uk1+uk2 in 24h; uk3 (now-25h) excluded.
	if len(v.Sources) != 1 || v.Sources[0].IP != "10.9.9.9" || v.Sources[0].Requests != 2 || v.Sources[0].Denied != 1 {
		t.Fatalf("sources: %+v", v.Sources)
	}
	if v.Sources[0].Last != ts(inspectorClock, 10*time.Minute) {
		t.Fatalf("last_seen: %q want %q", v.Sources[0].Last, ts(inspectorClock, 10*time.Minute))
	}
	if len(v.Sources[0].Top) != 2 {
		t.Fatalf("unknown top count: %+v", v.Sources[0].Top)
	}
	// Sort is requests DESC then host ASC; both have 1 request, so evil.test < github.com.
	if v.Sources[0].Top[0].Host != "evil.test" || v.Sources[0].Top[1].Host != "github.com" || v.Sources[0].Top[1].Denied != 1 {
		t.Fatalf("unknown top: %+v", v.Sources[0].Top)
	}
}

func TestInspectorExplainReasons(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	cases := map[string]string{
		"ev-ex-unknown":    "unknown_source",
		"ev-ex-protected":  "protected_destination",
		"ev-ex-blocked":    "blocked_for_all",
		"ev-ex-disabled":   "client_disabled",
		"ev-ex-authfail":   "auth_failed",
		"ev-ex-notinallow": "not_in_allowlist",
		"ev-ex-allowed":    "allowed_by_policy",
		"ev-ex-allweb":     "allowed_all_web",
	}
	for id, want := range cases {
		var v struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if code := getJSON(t, h, c, "/inspector/events/"+id+"/explain", &v); code != 200 || v.Reason != want {
			t.Fatalf("%s: %d %+v want %s", id, code, v, want)
		}
		if v.Decision != "allowed" && v.Decision != "denied" {
			t.Fatalf("%s: decision %q", id, v.Decision)
		}
	}
	// allowed reasons are "allowed"; denied reasons are "denied".
	for id, want := range cases {
		var v struct {
			Decision string `json:"decision"`
		}
		getJSON(t, h, c, "/inspector/events/"+id+"/explain", &v)
		wantAllowed := want == "allowed_by_policy" || want == "allowed_all_web"
		if wantAllowed != (v.Decision == "allowed") {
			t.Fatalf("%s: decision %q for reason %s", id, v.Decision, want)
		}
	}
	var v map[string]any
	if code := getJSON(t, h, c, "/inspector/events/nope/explain", &v); code != 404 {
		t.Fatalf("missing event: %d", code)
	}
}

func TestInspectorDraftImpactBothDirections(t *testing.T) {
	_, h, c, csrf := inspectorFixture(t)
	// Baseline: draft == active, no divergence.
	var v struct {
		WindowHours  int `json:"window_hours"`
		NewlyAllowed int `json:"newly_allowed"`
		NewlyDenied  int `json:"newly_denied"`
		Clients      []struct {
			ClientID     string `json:"client_id"`
			NewlyAllowed int    `json:"newly_allowed"`
			NewlyDenied  int    `json:"newly_denied"`
		} `json:"clients"`
	}
	if code := getJSON(t, h, c, "/inspector/draft-impact", &v); code != 200 || v.NewlyAllowed != 0 || v.NewlyDenied != 0 {
		t.Fatalf("baseline impact: %+v", v)
	}
	// Draft: p-allow now lists github.com (newly allowed) but drops
	// archive.ubuntu.com (newly denied). In 24h, c-a has cs3,cs4 (github,
	// denied by active -> allowed by draft) and cs1,cs2,cs5,cs11 (archive,
	// allowed by active -> denied by draft).
	d := getDraft(t, h, c)
	for i := range d.Snapshot.Policies {
		if d.Snapshot.Policies[i].ID == "p-allow" {
			d.Snapshot.Policies[i].Destinations = []policy.Destination{{Domain: "github.com"}, {Domain: "pypi.org"}}
		}
	}
	w := request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, c, csrf, "https://panel.test")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if code := getJSON(t, h, c, "/inspector/draft-impact", &v); code != 200 {
		t.Fatal(code)
	}
	if v.NewlyAllowed != 2 || v.NewlyDenied != 4 {
		t.Fatalf("impact both directions: %+v", v)
	}
	if len(v.Clients) != 1 || v.Clients[0].ClientID != "c-a" || v.Clients[0].NewlyAllowed != 2 || v.Clients[0].NewlyDenied != 4 {
		t.Fatalf("impact clients: %+v", v.Clients)
	}
}

func TestInspectorNoURLPathQueryOrCredentialsLeak(t *testing.T) {
	s, h, c, _ := inspectorFixture(t)
	now := inspectorClock
	// A corrupt stored body smuggles userinfo, a path/query and a bearer-like
	// username into the destination and proxy_username fields. The inspector
	// must surface only the bare hostname and never the credentials or path.
	leak := events.Event{
		ID: "ev-leak", Timestamp: ts(now, 15*time.Minute), SourceIP: "10.1.0.5",
		Destination: "user:pass@leak.example:443/secret?token=abc-xyz", Method: "CONNECT",
		ProxyResult: "TCP_TUNNEL", ProxyHTTPStatus: ptrInt(200), Bytes: 100, TLSMode: "tunnel",
		ProxyUsername: ptrStr("leak-user-secret"),
	}
	cid, cname := "c-a", "alpha"
	leak.ClientID, leak.ClientName, leak.ClientMatchBasis = &cid, &cname, "source_ip"
	if e := s.Telemetry.Ingest(context.Background(), []events.Event{leak}, events.Checkpoint{}); e != nil {
		t.Fatal(e)
	}
	forbidden := []string{"secret", "token=abc", "abc-xyz", "leak-user-secret", "user:pass", "password"}
	// client-stats: top destination host must be the bare hostname.
	var stats struct {
		Top []struct {
			Host string `json:"host"`
		} `json:"top_destinations"`
	}
	getJSON(t, h, c, "/inspector/clients/c-a/stats", &stats)
	found := false
	for _, d := range stats.Top {
		if d.Host == "leak.example" {
			found = true
		}
		if strings.ContainsAny(d.Host, "@/") {
			t.Fatalf("host leaked userinfo/path: %q", d.Host)
		}
	}
	if !found {
		t.Fatalf("bare host missing from top destinations: %+v", stats.Top)
	}
	// explain returns no destination, no username — only decision/reason/ids.
	var ex map[string]any
	getJSON(t, h, c, "/inspector/events/ev-leak/explain", &ex)
	if _, ok := ex["destination"]; ok {
		t.Fatalf("explain leaked destination: %+v", ex)
	}
	if _, ok := ex["proxy_username"]; ok {
		t.Fatalf("explain leaked username field: %+v", ex)
	}
	// Assert no forbidden substring appears in any inspector response body.
	for _, p := range []string{
		"/inspector/clients/c-a/stats",
		"/inspector/unknown-sources",
		"/inspector/events/ev-leak/explain",
		"/inspector/draft-impact",
		"/inspector/policies/p-allow/hits",
	} {
		w := request(h, "GET", p, nil, c, "", "")
		for _, sub := range forbidden {
			if strings.Contains(w.Body.String(), sub) {
				t.Fatalf("%s body leaked %q: %s", p, sub, w.Body.String())
			}
		}
	}
}

func TestInspectorStatusInstance(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	var st struct {
		Instance struct {
			Name      string  `json:"name"`
			Free      *int64  `json:"data_free_bytes"`
			Total     *int64  `json:"data_total_bytes"`
			LastEvent *string `json:"journal_last_event_at"`
		} `json:"instance"`
	}
	if code := getJSON(t, h, c, "/status", &st); code != 200 {
		t.Fatal(code)
	}
	if st.Instance.Name == "" {
		t.Fatal("instance name empty")
	}
	if st.Instance.Free == nil || st.Instance.Total == nil {
		t.Fatalf("disk figures nil: %+v", st.Instance)
	}
	if st.Instance.LastEvent == nil {
		t.Fatal("journal last event nil after seeded events")
	}
}

func TestInspectorDomainTemplates(t *testing.T) {
	_, h, c, _ := inspectorFixture(t)
	var tpl struct {
		Templates []struct {
			ID           string               `json:"id"`
			Name         string               `json:"name"`
			Destinations []policy.Destination `json:"destinations"`
		} `json:"templates"`
	}
	if code := getJSON(t, h, c, "/inspector/domain-templates", &tpl); code != 200 {
		t.Fatal(code)
	}
	if len(tpl.Templates) < 7 {
		t.Fatalf("templates: %d (want >=7)", len(tpl.Templates))
	}
	for _, x := range tpl.Templates {
		if x.ID == "" || x.Name == "" || len(x.Destinations) == 0 {
			t.Fatalf("empty template: %+v", x)
		}
	}
}

// A source the rollups still remember stops being "unknown" (and its
// notification disappears) once the active configuration covers it.
func TestInspectorUnknownSourcesSkipsDescribed(t *testing.T) {
	s, h, c, csrf := inspectorFixture(t)
	snap := inspectorSnapshot()
	snap.Clients = append(snap.Clients, policy.Client{ID: "c-new", Name: "new", Kind: "network", Networks: []string{"10.9.9.0/24"}, Enabled: true, PolicyID: "p-allow"})
	applySnapshot(t, h, s, c, csrf, "described", snap)
	var v struct {
		Sources []struct {
			IP string `json:"source_ip"`
		} `json:"sources"`
	}
	if code := getJSON(t, h, c, "/inspector/unknown-sources", &v); code != 200 || len(v.Sources) != 0 {
		t.Fatalf("described source still listed: %d %+v", code, v.Sources)
	}
	if n := listNotifications(t, h, c, "?kind=unknown_source"); len(n.Items) != 0 {
		t.Fatalf("notification for described source: %+v", n.Items)
	}
}
