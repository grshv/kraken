package httpapi

import (
	"context"
	"testing"
	"time"

	"kraken.local/kraken/internal/events"
)

func TestProxyAccountActivity(t *testing.T) {
	s, h := fixtureServer(t)
	admin, csrf := loginTest(t, h)
	if w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "build", "password": "test-only-proxy-password"}, admin, csrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	at := func(d time.Duration) string { return s.now().Add(-d).UTC().Format(time.RFC3339) }
	user := "build"
	ok, wrong := 200, 407
	rows := []events.Event{
		{ID: "pa-1", Timestamp: at(time.Hour), SourceIP: "10.1.0.5", Destination: "a.example:443", Method: "CONNECT", ProxyResult: "TCP_TUNNEL/200", ProxyHTTPStatus: &ok, ProxyUsername: &user, TLSMode: "tunnel"},
		{ID: "pa-2", Timestamp: at(30 * time.Minute), SourceIP: "10.9.9.9", Destination: "a.example:443", Method: "CONNECT", ProxyResult: "TCP_DENIED/407", ProxyHTTPStatus: &wrong, ProxyUsername: &user, TLSMode: "tunnel"},
		{ID: "pa-3", Timestamp: at(48 * time.Hour), SourceIP: "10.1.0.5", Destination: "a.example:443", Method: "CONNECT", ProxyResult: "TCP_TUNNEL/200", ProxyHTTPStatus: &ok, ProxyUsername: &user, TLSMode: "tunnel"},
	}
	if e := s.Telemetry.Ingest(context.Background(), rows, events.Checkpoint{SourceAvailable: true}); e != nil {
		t.Fatal(e)
	}
	var v struct {
		WindowHours int     `json:"window_hours"`
		Requests    int64   `json:"requests"`
		Failed      int64   `json:"failed_logins"`
		LastUsed    *string `json:"last_used_at"`
		Sources     []struct {
			SourceIP string `json:"source_ip"`
			Failed   int64  `json:"failed_logins"`
		} `json:"sources"`
	}
	if code := getJSON(t, h, admin, "/proxy-accounts/build/activity", &v); code != 200 {
		t.Fatalf("activity: %d", code)
	}
	if v.WindowHours != 24 || v.Requests != 1 || v.Failed != 1 || v.LastUsed == nil || len(v.Sources) != 2 || v.Sources[0].SourceIP != "10.9.9.9" || v.Sources[0].Failed != 1 {
		t.Fatalf("activity: %+v", v)
	}
	if w := request(h, "GET", "/proxy-accounts/bad%20name/activity", nil, admin, "", ""); w.Code != 400 {
		t.Fatalf("invalid name: %d", w.Code)
	}
	// A manager sees only logins he owns or his clients use.
	createManagerUser(t, h, admin, csrf, "mgr", []string{"10.1.0.0/16"}, []string{})
	mgr, mcsrf := loginAs(t, h, "mgr", "manager-password-123")
	if w := request(h, "GET", "/proxy-accounts/build/activity", nil, mgr, "", ""); w.Code != 404 {
		t.Fatalf("foreign login: %d", w.Code)
	}
	if w := request(h, "POST", "/proxy-accounts", map[string]any{"username": "mine", "password": "test-only-proxy-password"}, mgr, mcsrf, "https://panel.test"); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "GET", "/proxy-accounts/mine/activity", nil, mgr, "", ""); w.Code != 200 {
		t.Fatalf("own login: %d %s", w.Code, w.Body.String())
	}
}
