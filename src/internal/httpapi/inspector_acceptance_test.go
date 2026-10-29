package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
)

func acceptApply(t *testing.T, h http.Handler, c *http.Cookie, csrf, key string) {
	t.Helper()
	var d Draft
	json.Unmarshal(request(h, "GET", "/configuration/draft", nil, c, "", "").Body.Bytes(), &d)
	w := request(h, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, c, csrf, "https://panel.test")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var rev Revision
	json.Unmarshal(w.Body.Bytes(), &rev)
	var preview struct {
		Expected *string `json:"expected_applied_revision"`
	}
	json.Unmarshal(request(h, "GET", "/revisions/"+rev.ID+"/preview", nil, c, "", "").Body.Bytes(), &preview)
	op := func(k string, body map[string]any) Operation {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "https://panel.test/api/v1/operations", strings.NewReader(string(b)))
		r.AddCookie(c)
		r.Header.Set("Origin", "https://panel.test")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", k)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var o Operation
		json.Unmarshal(w.Body.Bytes(), &o)
		for i := 0; i < 200 && (o.Status == "pending" || o.Status == "validating" || o.Status == "reloading"); i++ {
			time.Sleep(20 * time.Millisecond)
			json.Unmarshal(request(h, "GET", "/operations/"+o.ID, nil, c, "", "").Body.Bytes(), &o)
		}
		return o
	}
	v := op(key+"-validate-0001", map[string]any{"action": "validate", "revision_id": rev.ID, "expected_applied_revision": preview.Expected})
	if v.Status != "validated" {
		t.Fatalf("validate: %+v", v)
	}
	if a := op(key+"-apply-000001", map[string]any{"action": "apply", "revision_id": rev.ID, "expected_applied_revision": preview.Expected, "validation_operation_id": v.ID}); a.Status != "succeeded" {
		t.Fatalf("apply: %+v", a)
	}
}

func acceptFixture(t *testing.T) (*Server, http.Handler, *http.Cookie) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	w := request(h, "GET", "/configuration/draft", nil, admin, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	tls := d.Snapshot.TLSProfiles[0].ID
	d.Snapshot.Policies = append(d.Snapshot.Policies, policy.Policy{ID: "p-apt", Name: "apt", Mode: "allowlist", TLSProfileID: tls,
		Destinations: []policy.Destination{{Domain: "archive.ubuntu.com"}, {Domain: "pypi.org"}, {Domain: "unused.example"}}})
	d.Snapshot.Clients = append(d.Snapshot.Clients, policy.Client{ID: "c-a", Name: "alpha", Kind: "host", Networks: []string{"10.1.0.5/32"}, Enabled: true, PolicyID: "p-apt"})
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, admin, csrf, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	acceptApply(t, h, admin, csrf, "inspector-fixture")
	now := time.Now().UTC()
	id, name := "c-a", "alpha"
	status := func(n int) *int { return &n }
	ev := func(n int, ago time.Duration, client bool, src, dst, result string, code int, bytes int64) events.Event {
		e := events.Event{ID: fmt.Sprintf("ev-%03d", n), Timestamp: now.Add(-ago).Format(time.RFC3339), SourceIP: src, Destination: dst, Method: "CONNECT", ProxyResult: result, ProxyHTTPStatus: status(code), Bytes: bytes, TLSMode: "tunnel"}
		if client {
			e.ClientID, e.ClientName, e.ClientMatchBasis = &id, &name, "source_ip"
		}
		return e
	}
	items := []events.Event{
		ev(1, 10*time.Minute, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 1000),
		ev(2, 20*time.Minute, true, "10.1.0.5", "archive.ubuntu.com:443", "TCP_TUNNEL", 200, 2000),
		ev(3, 30*time.Minute, true, "10.1.0.5", "github.com:443", "TCP_DENIED", 403, 0),
		ev(4, 3*time.Hour, true, "10.1.0.5", "github.com:443", "TCP_DENIED", 403, 0),
		ev(5, 3*24*time.Hour, true, "10.1.0.5", "pypi.org:443", "TCP_TUNNEL", 200, 500),
		ev(6, 9*24*time.Hour, true, "10.1.0.5", "pypi.org:443", "TCP_TUNNEL", 200, 500),
		ev(7, 15*time.Minute, false, "10.9.9.9", "github.com:443", "TCP_DENIED", 403, 0),
		ev(8, 16*time.Minute, false, "10.9.9.9", "github.com:443", "TCP_DENIED", 403, 0),
		ev(9, 5*time.Minute, true, "10.1.0.5", "169.254.169.254:80", "TCP_DENIED", 403, 0),
	}
	if e := s.Store.SaveEvents(items, events.Checkpoint{SourceAvailable: true}); e != nil {
		t.Fatal(e)
	}
	return s, h, admin
}

func acceptGet(t *testing.T, h http.Handler, c *http.Cookie, path string, v any) int {
	t.Helper()
	w := request(h, "GET", path, nil, c, "", "")
	if w.Code == 200 {
		if e := json.Unmarshal(w.Body.Bytes(), v); e != nil {
			t.Fatalf("%s: %v %s", path, e, w.Body.String())
		}
	}
	return w.Code
}

func TestAcceptInspectorRequiresSession(t *testing.T) {
	_, h, _ := acceptFixture(t)
	for _, p := range []string{"/inspector/clients/c-a/stats", "/inspector/policies/p-apt/hits", "/inspector/unknown-sources", "/inspector/events/ev-003/explain", "/inspector/draft-impact", "/inspector/domain-templates"} {
		if w := request(h, "GET", p, nil, nil, "", ""); w.Code != 401 {
			t.Fatalf("%s anonymous: %d", p, w.Code)
		}
	}
}

func TestAcceptInspectorClientStats(t *testing.T) {
	_, h, c := acceptFixture(t)
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
		Open *int `json:"open_connections"`
		Top  []struct {
			Host     string `json:"host"`
			Requests int    `json:"requests"`
			Denied   int    `json:"denied"`
		} `json:"top_destinations"`
	}
	if code := acceptGet(t, h, c, "/inspector/clients/c-a/stats", &v); code != 200 {
		t.Fatal(code)
	}
	if v.WindowHours != 24 || v.Requests != 5 || v.Denied != 3 || v.Bytes != 3000 {
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
	if sum != 5 {
		t.Fatalf("series sum %d", sum)
	}
	found := false
	for _, d := range v.Top {
		if strings.HasPrefix(d.Host, "github.com") {
			found = d.Requests == 2 && d.Denied == 2
		}
	}
	if !found {
		t.Fatalf("top destinations: %+v", v.Top)
	}
	if code := acceptGet(t, h, c, "/inspector/clients/missing/stats", &v); code != 404 {
		t.Fatalf("missing client: %d", code)
	}
}

func TestAcceptInspectorPolicyHits(t *testing.T) {
	_, h, c := acceptFixture(t)
	var v struct {
		Domains []struct {
			Domain string `json:"domain"`
			H24    int    `json:"hits_24h"`
			H7     int    `json:"hits_7d"`
		} `json:"domains"`
	}
	if code := acceptGet(t, h, c, "/inspector/policies/p-apt/hits", &v); code != 200 {
		t.Fatal(code)
	}
	got := map[string][2]int{}
	for _, d := range v.Domains {
		got[d.Domain] = [2]int{d.H24, d.H7}
	}
	if got["archive.ubuntu.com"] != [2]int{2, 2} || got["pypi.org"] != [2]int{0, 1} {
		t.Fatalf("hits: %+v", got)
	}
	if _, ok := got["unused.example"]; !ok {
		t.Fatal("domain without hits missing")
	}
	if code := acceptGet(t, h, c, "/inspector/policies/missing/hits", &v); code != 404 {
		t.Fatal(code)
	}
}

func TestAcceptInspectorUnknownSources(t *testing.T) {
	_, h, c := acceptFixture(t)
	var v struct {
		Sources []struct {
			IP       string `json:"source_ip"`
			Requests int    `json:"requests"`
			Denied   int    `json:"denied"`
			Last     string `json:"last_seen"`
		} `json:"sources"`
	}
	if code := acceptGet(t, h, c, "/inspector/unknown-sources", &v); code != 200 {
		t.Fatal(code)
	}
	if len(v.Sources) != 1 || v.Sources[0].IP != "10.9.9.9" || v.Sources[0].Requests != 2 || v.Sources[0].Denied != 2 || v.Sources[0].Last == "" {
		t.Fatalf("sources: %+v", v.Sources)
	}
}

func TestAcceptInspectorExplain(t *testing.T) {
	_, h, c := acceptFixture(t)
	cases := map[string]string{"ev-007": "unknown_source", "ev-003": "not_in_allowlist", "ev-009": "protected_destination", "ev-001": "allowed_by_policy"}
	for id, want := range cases {
		var v struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		if code := acceptGet(t, h, c, "/inspector/events/"+id+"/explain", &v); code != 200 || v.Reason != want {
			t.Fatalf("%s: %d %+v want %s", id, code, v, want)
		}
	}
	var v map[string]any
	if code := acceptGet(t, h, c, "/inspector/events/nope/explain", &v); code != 404 {
		t.Fatal(code)
	}
}

func TestAcceptInspectorDraftImpact(t *testing.T) {
	_, h, c := acceptFixture(t)
	var v struct {
		WindowHours  int `json:"window_hours"`
		NewlyAllowed int `json:"newly_allowed"`
		NewlyDenied  int `json:"newly_denied"`
	}
	if code := acceptGet(t, h, c, "/inspector/draft-impact", &v); code != 200 || v.WindowHours != 24 {
		t.Fatalf("%d %+v", code, v)
	}
	// Add github.com to the draft policy: two denied requests in 24h become allowed.
	w := request(h, "GET", "/configuration/draft", nil, c, "", "")
	var d Draft
	json.Unmarshal(w.Body.Bytes(), &d)
	for i := range d.Snapshot.Policies {
		if d.Snapshot.Policies[i].ID == "p-apt" {
			d.Snapshot.Policies[i].Destinations = []policy.Destination{{Domain: "github.com"}}
		}
	}
	cookie, csrf2 := loginTest(t, h)
	if w = request(h, "PUT", "/configuration/draft", map[string]any{"expected_version": d.Version, "snapshot": d.Snapshot}, cookie, csrf2, "https://panel.test"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if code := acceptGet(t, h, c, "/inspector/draft-impact", &v); code != 200 || v.NewlyAllowed != 2 || v.NewlyDenied != 2 {
		t.Fatalf("impact: %+v", v)
	}
}

func TestAcceptInspectorStatusAndTemplates(t *testing.T) {
	_, h, c := acceptFixture(t)
	var st struct {
		Instance struct {
			Name      string  `json:"name"`
			Free      *int64  `json:"data_free_bytes"`
			Total     *int64  `json:"data_total_bytes"`
			LastEvent *string `json:"journal_last_event_at"`
		} `json:"instance"`
	}
	if code := acceptGet(t, h, c, "/status", &st); code != 200 || st.Instance.Name == "" || st.Instance.LastEvent == nil {
		t.Fatalf("status instance: %+v", st)
	}
	var tpl struct {
		Templates []struct {
			ID           string               `json:"id"`
			Name         string               `json:"name"`
			Destinations []policy.Destination `json:"destinations"`
		} `json:"templates"`
	}
	if code := acceptGet(t, h, c, "/inspector/domain-templates", &tpl); code != 200 || len(tpl.Templates) < 5 {
		t.Fatalf("templates: %+v", tpl)
	}
	for _, x := range tpl.Templates {
		if x.ID == "" || len(x.Destinations) == 0 {
			t.Fatalf("empty template %+v", x)
		}
	}
}
