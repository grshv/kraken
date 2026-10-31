package httpapi

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

type domainsBody struct {
	Items []struct {
		Host        string `json:"host"`
		Requests    int64  `json:"requests"`
		Denied      int64  `json:"denied"`
		Bytes       int64  `json:"bytes"`
		LastSeen    string `json:"last_seen"`
		Ports       []int  `json:"ports"`
		SourceCount int    `json:"source_count"`
		Sources     []struct {
			ClientID   *string `json:"client_id"`
			ClientName *string `json:"client_name"`
			SourceIP   string  `json:"source_ip"`
			Requests   int64   `json:"requests"`
			Denied     int64   `json:"denied"`
		} `json:"sources"`
	} `json:"items"`
	Truncated bool `json:"truncated"`
	Summary   struct {
		Requests         int64 `json:"requests"`
		Denied           int64 `json:"denied"`
		Bytes            int64 `json:"bytes"`
		UnmatchedSources int   `json:"unmatched_sources"`
		Clients          []struct {
			ClientID   *string `json:"client_id"`
			ClientName *string `json:"client_name"`
			Requests   int64   `json:"requests"`
			Denied     int64   `json:"denied"`
		} `json:"clients"`
	} `json:"summary"`
}

func domainHosts(t *testing.T, raw []byte) map[string]int64 {
	t.Helper()
	var body domainsBody
	if e := json.Unmarshal(raw, &body); e != nil {
		t.Fatal(e)
	}
	out := map[string]int64{}
	for _, d := range body.Items {
		out[d.Host] = d.Requests
	}
	return out
}

func TestEventDomainsGroupsByHostWithSources(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	seedManifestClients(t, s)
	window := "from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Add(time.Minute).Format(time.RFC3339Nano))

	w := request(h, "GET", "/events/domains?"+window, nil, cookie, "", "")
	if w.Code != 200 {
		t.Fatalf("domains: %d %s", w.Code, w.Body.String())
	}
	var body domainsBody
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if len(body.Items) != 4 || body.Truncated {
		t.Fatalf("items=%d truncated=%v", len(body.Items), body.Truncated)
	}
	// Summary: 2 allowed + 2 denied, 15 bytes, two unmatched addresses; the
	// unmatched client total has a null id, known clients their names.
	sum := body.Summary
	if sum.Requests != 4 || sum.Denied != 2 || sum.Bytes != 15 || sum.UnmatchedSources != 2 || len(sum.Clients) != 3 {
		t.Fatalf("summary %+v", sum)
	}
	if top := sum.Clients[0]; top.ClientID != nil || top.Requests != 2 || top.Denied != 1 {
		t.Fatalf("summary unmatched first %+v", top)
	}
	for _, c := range sum.Clients[1:] {
		if c.ClientID == nil || c.ClientName == nil {
			t.Fatalf("summary client without name %+v", c)
		}
	}
	for _, d := range body.Items {
		if d.Host == "a.test" {
			if d.Requests != 1 || d.Denied != 0 || d.Bytes != 10 || d.SourceCount != 1 || len(d.Ports) != 1 || d.Ports[0] != 443 {
				t.Fatalf("a.test %+v", d)
			}
			src := d.Sources[0]
			if src.SourceIP != "10.1.0.5" || src.ClientID == nil || *src.ClientID != "c-mine" || src.ClientName == nil || *src.ClientName != "mine" {
				t.Fatalf("a.test source %+v", src)
			}
		}
		if d.Host == "in.test" && (d.Denied != 1 || d.Sources[0].ClientID != nil || d.Sources[0].ClientName != nil) {
			t.Fatalf("unknown source must have null client: %+v", d)
		}
	}

	// Repeated client_id plus unmatched=true: c-mine and the unknown sources.
	w = request(h, "GET", "/events/domains?"+window+"&client_id=c-mine&unmatched=true", nil, cookie, "", "")
	hosts := domainHosts(t, w.Body.Bytes())
	if len(hosts) != 3 || hosts["a.test"] != 1 || hosts["in.test"] != 1 || hosts["out.test"] != 1 {
		t.Fatalf("client_id+unmatched hosts %v", hosts)
	}
	// unmatched alone: only unknown sources.
	w = request(h, "GET", "/events/domains?"+window+"&unmatched=true", nil, cookie, "", "")
	if hosts = domainHosts(t, w.Body.Bytes()); len(hosts) != 2 || hosts["a.test"] != 0 || hosts["b.test"] != 0 {
		t.Fatalf("unmatched hosts %v", hosts)
	}
	// Two clients at once.
	w = request(h, "GET", "/events/domains?"+window+"&client_id=c-mine&client_id=c-foreign", nil, cookie, "", "")
	if hosts = domainHosts(t, w.Body.Bytes()); len(hosts) != 2 || hosts["a.test"] != 1 || hosts["b.test"] != 1 {
		t.Fatalf("two clients hosts %v", hosts)
	}
	// destination narrows like /events.
	w = request(h, "GET", "/events/domains?"+window+"&destination=OUT", nil, cookie, "", "")
	if hosts = domainHosts(t, w.Body.Bytes()); len(hosts) != 1 || hosts["out.test"] != 1 {
		t.Fatalf("destination hosts %v", hosts)
	}
}

func TestEventDomainsManagerScope(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, csrf := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	seedManifestClients(t, s)
	window := "from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Add(time.Minute).Format(time.RFC3339Nano))
	createManagerUser(t, h, cookie, csrf, "mgr", []string{"10.1.0.0/16"}, []string{"c-mine"})
	mgr, _ := loginAs(t, h, "mgr", "manager-password-123")

	// His client and unknown sources in his network; c-foreign and 10.9.9.9 hidden.
	w := request(h, "GET", "/events/domains?"+window, nil, mgr, "", "")
	if w.Code != 200 {
		t.Fatalf("manager domains: %d %s", w.Code, w.Body.String())
	}
	if hosts := domainHosts(t, w.Body.Bytes()); len(hosts) != 2 || hosts["a.test"] != 1 || hosts["in.test"] != 1 {
		t.Fatalf("manager hosts %v", hosts)
	}
	// A foreign client in the list is 404, even next to his own.
	w = request(h, "GET", "/events/domains?"+window+"&client_id=c-mine&client_id=c-foreign", nil, mgr, "", "")
	if w.Code != 404 {
		t.Fatalf("foreign client: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/events?client_id=c-foreign", nil, mgr, "", "")
	if w.Code != 404 {
		t.Fatalf("events foreign client: %d", w.Code)
	}
}

func TestEventDomainsBadParams(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	from := url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano))
	to := url.QueryEscape(now.Format(time.RFC3339Nano))
	long := url.QueryEscape(now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano))
	for name, query := range map[string]string{
		"no window":      "",
		"no to":          "from=" + from,
		"reversed":       "from=" + to + "&to=" + from,
		"too long":       "from=" + long + "&to=" + to,
		"bad reason":     "from=" + from + "&to=" + to + "&deny_reason=nope",
		"bad unmatched":  "from=" + from + "&to=" + to + "&unmatched=yes",
		"bad ip":         "from=" + from + "&to=" + to + "&source_ip=10.0.0",
		"long method":    "from=" + from + "&to=" + to + "&method=" + string(repeatByte('M', 30)),
		"bad failures":   "from=" + from + "&to=" + to + "&failures_only=1",
		"long client id": "from=" + from + "&to=" + to + "&client_id=" + string(repeatByte('c', 129)),
	} {
		w := request(h, "GET", "/events/domains?"+query, nil, cookie, "", "")
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// A multi-value filter on /events and /events/facets matches any listed value.
func TestEventsMultiValueFilters(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	window := "from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Add(time.Minute).Format(time.RFC3339Nano))

	w := request(h, "GET", "/events?client_id=c-mine&unmatched=true", nil, cookie, "", "")
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || w.Code != 200 {
		t.Fatalf("events: %d %s", w.Code, w.Body.String())
	}
	if len(page.Items) != 3 {
		t.Fatalf("client_id+unmatched events=%d want 3", len(page.Items))
	}
	w = request(h, "GET", "/events/facets?"+window+"&step=1m&client_id=c-mine&client_id=c-foreign", nil, cookie, "", "")
	var body facetsBody
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != 200 {
		t.Fatalf("facets: %d %s", w.Code, w.Body.String())
	}
	if body.Allowed != 1 || body.Denied != 1 {
		t.Fatalf("two clients facets allowed=%d denied=%d", body.Allowed, body.Denied)
	}
	// An empty client_id is no filter, as before.
	w = request(h, "GET", "/events?client_id=", nil, cookie, "", "")
	if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil || len(page.Items) != 4 {
		t.Fatalf("empty client_id events=%d", len(page.Items))
	}
}

// allowed_only is the opposite of failures_only; both at once are rejected.
func TestEventsAllowedOnly(t *testing.T) {
	s, h := fixtureServer(t)
	cookie, _ := loginTest(t, h)
	now := seedFacetsEvents(t, s)
	window := "from=" + url.QueryEscape(now.Add(-time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Add(time.Minute).Format(time.RFC3339Nano))
	w := request(h, "GET", "/events/facets?"+window+"&step=1m&allowed_only=true", nil, cookie, "", "")
	var body facetsBody
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != 200 {
		t.Fatalf("facets: %d %s", w.Code, w.Body.String())
	}
	if body.Allowed != 2 || body.Denied != 0 {
		t.Fatalf("allowed_only facets allowed=%d denied=%d", body.Allowed, body.Denied)
	}
	if hosts := domainHosts(t, request(h, "GET", "/events/domains?"+window+"&allowed_only=true", nil, cookie, "", "").Body.Bytes()); len(hosts) != 2 || hosts["a.test"] != 1 || hosts["out.test"] != 1 {
		t.Fatalf("allowed_only hosts %v", hosts)
	}
	for _, q := range []string{"allowed_only=1", "allowed_only=true&failures_only=true"} {
		if w := request(h, "GET", "/events?"+q, nil, cookie, "", ""); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
}
