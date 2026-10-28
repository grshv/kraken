package runtime

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testInfo = "Squid Object Cache: Version 6.13\n UP Time: 123.45 seconds\n CPU Time: 1.2 seconds\n CPU Usage: 0.97%\n Maximum Resident Size: 100 KB\n"
const testCounters = "server.all.requests = 40\nserver.all.errors = 2\nserver.all.kbytes_in = 3\nserver.all.kbytes_out = 20\n"

func TestMetricsAggregates(t *testing.T) {
	m, err := parseMetrics(testInfo, testCounters, "sample_start_time = 10\nsample_end_time = 20\nclient_http.kbytes_in = 1.5/sec\nclient_http.kbytes_out = 2/sec\nclient_http.requests = 4/sec\nclient_http.hits = 1/sec", "Connection: 0x1\nuri http://synthetic.test/private?secret=never-return\nConnection: 0x2\n\tlocal: 127.0.0.1:3131\nuri http://squid-control:3130/squid-internal-mgr/active_requests\n")
	if err != nil {
		t.Fatal(err)
	}
	if *m.ActiveRequests != 1 || *m.MemoryPeakBytes != 102400 {
		t.Fatalf("wrong aggregates: %+v", m)
	}
	if m.SquidVersion != "6.13" || *m.UptimeSeconds != 123.45 {
		t.Fatal(m)
	}
}
func TestMetricsMissingReportsAreNotZeros(t *testing.T) {
	if _, err := parseMetrics("garbage", testCounters, "", ""); err == nil {
		t.Fatal("accepted invalid info")
	}
	m, err := parseMetrics(testInfo, testCounters, "sample_start_time = 10\nsample_end_time = 10\nclient_http.kbytes_in = 0/sec", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.OriginBytesInPerSecond != nil || m.ActiveRequests != nil || m.CacheHitRatioPercent != nil {
		t.Fatal("invented zeros", m)
	}
	for _, s := range []string{"NaN", "Inf", "-1", "1e9999"} {
		if number(s) != nil {
			t.Fatal(s)
		}
	}
}
func TestManagerReportGuards(t *testing.T) {
	for _, code := range []int{302, 403, 500} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		client := ts.Client()
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme = "http"
			r.URL.Host = strings.TrimPrefix(ts.URL, "http://")
			return http.DefaultTransport.RoundTrip(r)
		})
		if _, err := managerReport(context.Background(), client, "info"); err == nil {
			t.Fatal(code)
		}
		ts.Close()
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("arbitrary report requested"); return nil, nil })}
	if _, err := managerReport(context.Background(), client, "shutdown"); err == nil {
		t.Fatal("accepted shutdown")
	}
	if v := scaled(number("1"), 1024); v == nil || math.IsNaN(*v) {
		t.Fatal(v)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetricsFailureKeepsObservationAndOldConfigNeedsUpdate(t *testing.T) {
	m := New(t.TempDir(), "squid", "/tmp/access.log", "", "", "")
	observed := time.Now().UTC().Add(-time.Minute)
	uptime := 12.0
	m.metricsLast = Metrics{Available: true, ObservedAt: &observed, UptimeSeconds: &uptime}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := m.Metrics(ctx)
	if result.Available || !result.Stale || result.ObservedAt == nil || !result.ObservedAt.Equal(observed) || *result.UptimeSeconds != 12 {
		t.Fatalf("lost last observation: %+v", result)
	}
	m.metricsAttempt = time.Time{}
	if err := os.WriteFile(m.ActivePath(), []byte("http_port 127.0.0.1:3130\nhttp_access deny all\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result = m.Metrics(context.Background())
	if result.Reason != "configuration_update_required" {
		t.Fatal(result)
	}
	body, err := os.ReadFile(m.ActivePath())
	if err != nil || strings.Contains(string(body), "3131") {
		t.Fatal("GET changed old config")
	}
}
func TestManagerReportBodyBound(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", managerMaxBody+1))), Header: make(http.Header)}, nil
	})}
	if _, err := managerReport(context.Background(), client, "info"); err == nil {
		t.Fatal("accepted oversized manager body")
	}
}

func TestMalformedActiveReportDoesNotReturnNegativeCount(t *testing.T) {
	m, err := parseMetrics(testInfo, testCounters, "", "Connection: 0x1\nuri http://squid-control:3131/squid-internal-mgr/active_requests\nuri http://squid-control:3131/squid-internal-mgr/info\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveRequests != nil {
		t.Fatal("malformed count published", *m.ActiveRequests)
	}
}

func TestSquidManagerRewrittenAuthorityAndIdleSocket(t *testing.T) {
	// Actual Squid 6.13 with canary 3130 first and manager 3131 second:
	// the local socket is 3131 while the report URI is rewritten to 3130.
	active := "Connection: 0x1\n\tlocal: 127.0.0.1:3131\nuri http://squid-control:3130/squid-internal-mgr/active_requests\nConnection: 0x2\n\tlocal: 127.0.0.1:3128\n\tFD desc: Reading next request\n"
	m, err := parseMetrics(testInfo, testCounters, "", active)
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveRequests == nil || *m.ActiveRequests != 0 {
		t.Fatal("manager or idle socket counted as active request", m.ActiveRequests)
	}
	active += "Connection: 0x3\n\tlocal: 127.0.0.1:3128\nuri synthetic.test:443\n"
	m, err = parseMetrics(testInfo, testCounters, "", active)
	if err != nil {
		t.Fatal(err)
	}
	if m.ActiveRequests == nil || *m.ActiveRequests != 1 {
		t.Fatal("real CONNECT request not counted", m.ActiveRequests)
	}
}
