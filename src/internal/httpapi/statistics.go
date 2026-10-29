package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"kraken.local/kraken/internal/telemetry"
)

// Statistics describes completed access-log records over a rolling 24h window,
// computed from telemetry aggregates (no raw scan). Denied follows the same
// rule as ingestion; unique_sources counts distinct client ids plus unknown
// source IPs visible in scope.
type Statistics struct {
	From             string          `json:"from"`
	To               string          `json:"to"`
	ObservedAt       string          `json:"observed_at"`
	Requests         int64           `json:"requests"`
	Denied           int64           `json:"denied"`
	BytesObserved    int64           `json:"bytes_observed"`
	UniqueSources    int64           `json:"unique_sources"`
	RecordLimit      int             `json:"record_limit"`
	RetentionLimited bool            `json:"retention_limited"`
	Hourly           []StatisticHour `json:"hourly"`
}

type StatisticHour struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Requests int64  `json:"requests"`
	Denied   int64  `json:"denied"`
}

func (s *Server) statistics(w http.ResponseWriter, r *http.Request, v session) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	from := now.Add(-24 * time.Hour)
	var ts *telemetry.Scope
	if v.Role == "manager" {
		sc, e := s.managerScope(ctx, v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		ts = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
	buckets, err := s.Telemetry.Series(ctx, from, now, time.Hour, "", ts)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "statistics_unavailable", "Статистика временно недоступна")
		return
	}
	format := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	out := Statistics{From: format(from), To: format(now), ObservedAt: format(now), RecordLimit: 0, Hourly: make([]StatisticHour, 24)}
	for i := range out.Hourly {
		start := from.Add(time.Duration(i) * time.Hour)
		out.Hourly[i] = StatisticHour{From: format(start), To: format(start.Add(time.Hour))}
	}
	for i, b := range buckets {
		if i >= 24 {
			break
		}
		out.Requests += b.Requests
		out.Denied += b.Denied
		out.BytesObserved += b.Bytes
		out.Hourly[i].Requests += b.Requests
		out.Hourly[i].Denied += b.Denied
	}
	// Unique sources: distinct unknown source IPs in scope + distinct clients.
	uk, _ := s.Telemetry.UnknownSources(ctx, from, now, ts, 10000)
	unique := int64(len(uk))
	if ts == nil {
		// Admin: also count distinct clients seen in 24h via client_15m.
		unique += s.distinctClients(ctx, from, now, nil)
	} else if len(ts.ClientIDs) > 0 {
		unique += s.distinctClients(ctx, from, now, ts)
	}
	out.UniqueSources = unique
	respond(w, http.StatusOK, out)
}

// distinctClients counts distinct client_ids with activity in the window by
// summing per-client 15m buckets. Used only for the statistics unique count.
func (s *Server) distinctClients(ctx context.Context, from, to time.Time, ts *telemetry.Scope) int64 {
	var n int64
	_ = s.Telemetry.DistinctClients(ctx, from, to, ts, &n)
	return n
}

// Activity is a bounded operational view of completed requests from aggregates.
type ActivitySource struct {
	SourceIP   string  `json:"source_ip"`
	ClientName *string `json:"client_name"`
	Username   *string `json:"proxy_username"`
	Requests   int     `json:"requests"`
	Denied     int     `json:"denied"`
	Errors     int     `json:"errors"`
	Bytes      int64   `json:"bytes_observed"`
	LastSeen   string  `json:"last_seen"`
}
type ActivityIssue struct {
	Username    *string `json:"proxy_username"`
	SourceIP    string  `json:"source_ip"`
	ClientName  *string `json:"client_name"`
	Destination string  `json:"destination"`
	Result      string  `json:"result"`
	Count       int     `json:"count"`
	LastSeen    string  `json:"last_seen"`
}
type Activity struct {
	From             string           `json:"from"`
	To               string           `json:"to"`
	Sources          []ActivitySource `json:"sources"`
	Issues           []ActivityIssue  `json:"issues"`
	TotalSources     int              `json:"total_sources"`
	TotalIssues      int              `json:"total_issues"`
	RetentionLimited bool             `json:"retention_limited"`
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request, v session) {
	minutes := 60
	if raw := r.URL.Query().Get("minutes"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || (value != 15 && value != 60 && value != 1440 && value != 10080 && value != 43200) {
			fail(w, 400, "invalid_window", "Выберите 15 минут, час, день, неделю или месяц")
			return
		}
		minutes = value
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	from := now.Add(-time.Duration(minutes) * time.Minute)
	var ts *telemetry.Scope
	if v.Role == "manager" {
		sc, e := s.managerScope(ctx, v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		ts = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
	srcs, err := s.Telemetry.UnknownSources(ctx, from, now, ts, 10000)
	if err != nil {
		fail(w, 503, "activity_unavailable", "Активность клиентов временно недоступна")
		return
	}
	out := Activity{From: from.UTC().Format(time.RFC3339Nano), To: now.UTC().Format(time.RFC3339Nano), Sources: []ActivitySource{}, Issues: []ActivityIssue{}}
	for _, sc := range srcs {
		last := ""
		if !sc.LastSeen.IsZero() {
			last = sc.LastSeen.UTC().Format(time.RFC3339Nano)
		}
		out.Sources = append(out.Sources, ActivitySource{SourceIP: sc.SourceIP, Requests: int(sc.Requests), Denied: int(sc.Denied), Bytes: sc.Bytes, LastSeen: last})
	}
	sort.Slice(out.Sources, func(i, j int) bool {
		if out.Sources[i].Requests != out.Sources[j].Requests {
			return out.Sources[i].Requests > out.Sources[j].Requests
		}
		return out.Sources[i].SourceIP < out.Sources[j].SourceIP
	})
	out.TotalSources = len(out.Sources)
	if len(out.Sources) > 10 {
		out.Sources = out.Sources[:10]
	}
	cp, err := s.Telemetry.Checkpoint(ctx)
	if err != nil {
		fail(w, 503, "activity_unavailable", "Состояние журнала недоступно")
		return
	}
	var applied any
	manifest, err := s.Runtime.ReadManifest()
	if err == nil {
		enabled, allowlist, allweb := 0, 0, 0
		for _, c := range manifest.Snapshot.Clients {
			if c.Enabled {
				enabled++
				for _, p := range manifest.Snapshot.Policies {
					if p.ID == c.PolicyID {
						if p.Mode == "all_web" {
							allweb++
						} else {
							allowlist++
						}
					}
				}
			}
		}
		applied = map[string]any{"mode": manifest.Snapshot.Mode, "enabled_clients": enabled, "allowlist_clients": allowlist, "all_web_clients": allweb}
	}
	respond(w, 200, map[string]any{"activity": out, "applied": applied, "ingestion": map[string]any{"observed_at": cp.ObservedAt, "source_available": cp.SourceAvailable, "gap": cp.Gap, "backlog": cp.Backlog, "dropped_records": cp.Dropped}})
}

var _ = context.Background
