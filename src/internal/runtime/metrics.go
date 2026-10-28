package runtime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"squid-control.local/squid/internal/policy"
	"strconv"
	"strings"
	"time"
)

// Metrics contains aggregates only. Raw manager reports never leave this module.
// Origin counters exclude Squid manager polling. Memory peak is not current RSS.
type Metrics struct {
	CacheEnabled            *bool    `json:"cache_enabled"`
	MemoryAccountedBytes    *float64 `json:"memory_accounted_bytes"`
	OriginRequestsPerSecond *float64 `json:"origin_requests_per_second"`
	CPUUsage5mPercent       *float64 `json:"cpu_usage_5m_percent"`
	OriginBytesInTotal      *float64 `json:"origin_bytes_in_total"`
	OriginBytesOutTotal     *float64 `json:"origin_bytes_out_total"`
	OriginBytesInPerSecond  *float64 `json:"origin_bytes_in_per_second"`
	OriginBytesOutPerSecond *float64 `json:"origin_bytes_out_per_second"`

	Available            bool       `json:"available"`
	Stale                bool       `json:"stale"`
	ObservedAt           *time.Time `json:"observed_at"`
	Reason               string     `json:"reason,omitempty"`
	SquidVersion         string     `json:"squid_version,omitempty"`
	UptimeSeconds        *float64   `json:"uptime_seconds"`
	ActiveRequests       *float64   `json:"active_requests"`
	CPUSeconds           *float64   `json:"cpu_seconds"`
	CPUUsagePercent      *float64   `json:"cpu_usage_percent"`
	MemoryPeakBytes      *float64   `json:"memory_peak_bytes"`
	OriginRequestsTotal  *float64   `json:"origin_requests_total"`
	OriginErrorsTotal    *float64   `json:"origin_errors_total"`
	CacheHitRatioPercent *float64   `json:"cache_hit_ratio_percent"`
}

const managerBase = "http://127.0.0.1:3131/squid-internal-mgr/"
const managerMaxBody = 1024 * 1024

func number(s string) *float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	n, e := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(fields[0], "/sec"), "%"), 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return nil
	}
	return &n
}
func scaled(v *float64, scale float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v * scale
	return &n
}
func values(body string) map[string]*float64 {
	result := map[string]*float64{}
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := strings.Cut(line, " = ")
		if ok {
			result[key] = number(val)
		}
	}
	return result
}
func parseMetrics(info, counters, rates, active string) (Metrics, error) {
	var out Metrics
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Squid Object Cache: Version "):
			version := strings.TrimPrefix(line, "Squid Object Cache: Version ")
			if len(version) < 32 && !strings.ContainsAny(version, " <>\"\\") {
				out.SquidVersion = version
			}
		case strings.HasPrefix(line, "UP Time:"):
			out.UptimeSeconds = number(strings.TrimPrefix(line, "UP Time:"))
		case strings.HasPrefix(line, "CPU Time:"):
			out.CPUSeconds = number(strings.TrimPrefix(line, "CPU Time:"))
		case strings.HasPrefix(line, "CPU Usage:"):
			out.CPUUsagePercent = number(strings.TrimPrefix(line, "CPU Usage:"))
		case strings.HasPrefix(line, "Total accounted:"):
			out.MemoryAccountedBytes = scaled(number(strings.TrimPrefix(line, "Total accounted:")), 1024)
		case strings.HasPrefix(line, "Hits as % of all requests:"):
			_, v, ok := strings.Cut(line, "5min:")
			if ok {
				out.CacheHitRatioPercent = number(strings.TrimSpace(strings.Split(v, ",")[0]))
			}
		case strings.HasPrefix(line, "Maximum Resident Size:"):
			out.MemoryPeakBytes = scaled(number(strings.TrimPrefix(line, "Maximum Resident Size:")), 1024)
		}
	}
	c := values(counters)
	out.OriginRequestsTotal = c["server.all.requests"]
	out.OriginErrorsTotal = c["server.all.errors"]
	out.OriginBytesInTotal = scaled(c["server.all.kbytes_in"], 1024)
	out.OriginBytesOutTotal = scaled(c["server.all.kbytes_out"], 1024)
	r := values(rates)
	if start, end := r["sample_start_time"], r["sample_end_time"]; start != nil && end != nil && *end > *start {
		out.OriginRequestsPerSecond = r["server.all.requests"]
		out.CPUUsage5mPercent = r["cpu_usage"]
		out.OriginBytesInPerSecond = scaled(r["server.all.kbytes_in"], 1024)
		out.OriginBytesOutPerSecond = scaled(r["server.all.kbytes_out"], 1024)
	}
	// Squid rewrites internal manager URI authority to its first http_port,
	// which is the canary listener (3130), not this request's listener (3131).
	// Identify diagnostics by the reported local socket plus a fixed URI path.
	// Idle Connection blocks have no URI and are not active requests.
	connections, requests, diagnostic, self := 0, 0, 0, 0
	localAddress := ""
	scanner := bufio.NewScanner(strings.NewReader(active))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Connection: ") {
			connections++
			localAddress = ""
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "local:") {
			localAddress = strings.TrimSpace(strings.TrimPrefix(trimmed, "local:"))
		}
		if !strings.HasPrefix(line, "uri ") {
			continue
		}
		requests++
		uri, err := url.Parse(strings.TrimPrefix(line, "uri "))
		if err != nil {
			continue
		}
		if localAddress == "127.0.0.1:3131" && uri.Scheme == "http" && uri.RawQuery == "" {
			switch uri.Path {
			case "/squid-internal-mgr/info", "/squid-internal-mgr/counters", "/squid-internal-mgr/5min", "/squid-internal-mgr/active_requests":
				diagnostic++
				if uri.Path == "/squid-internal-mgr/active_requests" {
					self++
				}
			}
		} else if localAddress == "127.0.0.1:3130" && uri.String() == "http://127.0.0.1/" {
			diagnostic++
		}
	}
	if scanner.Err() == nil && self == 1 && requests >= 1 && diagnostic <= requests && requests <= connections {
		n := float64(requests - diagnostic)
		out.ActiveRequests = &n
	}
	if out.SquidVersion == "" || out.UptimeSeconds == nil || out.OriginRequestsTotal == nil || out.OriginErrorsTotal == nil || out.OriginBytesInTotal == nil || out.OriginBytesOutTotal == nil {
		return Metrics{}, errors.New("invalid manager report")
	}
	return out, nil
}

func managerReport(ctx context.Context, client *http.Client, report string) (string, error) {
	switch report {
	case "info", "counters", "5min", "active_requests":
	default:
		return "", errors.New("unsupported manager report")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, managerBase+report, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("manager unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, managerMaxBody+1))
	if err != nil || len(body) > managerMaxBody {
		return "", errors.New("manager report exceeds limit")
	}
	return string(body), nil
}

// Metrics serializes and caches polls for two seconds, bounding manager load.
// Failed reads preserve the original observation timestamp and mark the sample stale.
func (m *Manager) Metrics(ctx context.Context) Metrics {
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()
	if time.Since(m.metricsAttempt) < 2*time.Second {
		return m.metricsLast
	}
	m.metricsAttempt = time.Now()
	if config, e := os.ReadFile(m.ActivePath()); e == nil && !strings.Contains(string(config), "http_port 127.0.0.1:3131\n") {
		result := m.metricsLast
		result.Available = false
		result.Stale = result.ObservedAt != nil
		result.Reason = "configuration_update_required"
		m.metricsLast = result
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: time.Second, MaxResponseHeaderBytes: 16 * 1024}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	reports := make([]string, 4)
	var err error
	for i, name := range []string{"info", "counters", "5min", "active_requests"} {
		reports[i], err = managerReport(ctx, client, name)
		if err != nil {
			break
		}
	}
	var result Metrics
	if err == nil {
		result, err = parseMetrics(reports[0], reports[1], reports[2], reports[3])
	}
	if err != nil {
		result = m.metricsLast
		result.Available = false
		result.Stale = result.ObservedAt != nil
		result.Reason = "unavailable"
	} else {
		if manifest, e := m.ReadManifest(); e == nil && m.checkDrift(manifest) == nil {
			// Expert cache ACLs can override the visual fallback. Do not claim
			// a single enabled/disabled state when that policy is conditional.
			known := true
			if manifest.Snapshot.Mode == "expert" {
				for _, line := range strings.Split(manifest.Snapshot.Expert, "\n") {
					fields := strings.Fields(line)
					if len(fields) > 0 && fields[0] == "cache" {
						known = false
						break
					}
				}
			}
			if known {
				v := policy.EffectiveSettings(manifest.Snapshot).CacheEnabled
				result.CacheEnabled = &v
			}
		}
		now := time.Now().UTC()
		result.Available = true
		result.ObservedAt = &now
	}
	m.metricsLast = result
	return result
}
