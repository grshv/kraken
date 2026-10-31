package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
	"kraken.local/kraken/internal/telemetry"
)

// CollectEvents is called by the background collector (RunCollector); never by
// a request handler. It reads one batch (≤ BatchBytes) and returns backlog=true
// when the checkpoint port indicates more data is pending, so the caller keeps
// reading without waiting for the interval.
func (s *Server) CollectEvents() (backlog bool, err error) {
	// Below storageLowBytes event collection pauses so telemetry ingestion
	// cannot fill the disk or block the control DB. The next tick after space
	// frees resumes from the last checkpoint automatically.
	if s.freeBytes != nil {
		if free, e := s.freeBytes(); e == nil && free < storageLowBytes {
			s.setIngestion("storage_low")
			s.maybeLogStorageLow()
			return false, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cp, e := s.Telemetry.Checkpoint(ctx)
	if e != nil {
		s.setIngestion("error")
		return false, e
	}
	items, next, e := events.Read(s.Runtime.Options.LogPath, cp)
	if e != nil {
		s.setIngestion("error")
		return false, e
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		s.setIngestion("error")
		return false, e
	}
	for i := range items {
		ip, e := netip.ParseAddr(items[i].SourceIP)
		if e != nil {
			continue
		}
		id, name := "", ""
		matches := 0
		for _, client := range manifest.Snapshot.Clients {
			if client.ProxyUsername != "" && (items[i].ProxyUsername == nil || *items[i].ProxyUsername != client.ProxyUsername) {
				continue
			}
			matched := false
			for _, raw := range client.Networks {
				network, e := netip.ParsePrefix(raw)
				if e != nil {
					if addr, ae := netip.ParseAddr(raw); ae == nil {
						network = netip.PrefixFrom(addr, addr.BitLen())
					} else {
						continue
					}
				}
				if network.Contains(ip) {
					matched = true
					break
				}
			}
			if matched {
				matches++
				id = client.ID
				name = client.Name
			}
		}
		if matches == 1 {
			items[i].ClientID = &id
			items[i].ClientName = &name
			items[i].ClientMatchBasis = "policy_at_ingest"
		}
	}
	if e := s.Telemetry.Ingest(ctx, items, next); e != nil {
		s.setIngestion("error")
		return false, e
	}
	s.setIngestion("ok")
	return next.Backlog, nil
}

// RunCollector drives CollectEvents in a loop: when a batch returns backlog
// (and no error) it reads the next batch immediately; otherwise it waits for
// interval or ctx cancellation. It returns when ctx is cancelled, no later
// than after the current batch — ctx is checked before each backlog-driven
// continuation so a continuously-growing log cannot stall shutdown.
func (s *Server) RunCollector(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		backlog, err := s.CollectEvents()
		if err != nil {
			slog.Warn("event collection failed", "error", err)
		}
		if backlog && err == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 200 {
			fail(w, 400, "invalid_limit", "limit должен быть от 1 до 200")
			return 0, false
		}
		limit = n
	}
	return limit, true
}
func queryFailure(w http.ResponseWriter, e error) {
	if errors.Is(e, telemetry.ErrCursor) || errors.Is(e, store.ErrCursor) {
		fail(w, 400, "invalid_cursor", "Страница устарела или фильтры изменились. Откройте последние записи")
		return
	}
	// A busy/deadline condition surfaces as 503 storage_busy so the client can
	// retry; other storage failures stay the generic 503 storage_unavailable.
	if isStorageBusy(e) {
		w.Header().Set("Retry-After", "2")
		fail(w, 503, "storage_busy", "Хранилище занято, повторите запрос")
		return
	}
	fail(w, 503, "storage_unavailable", "История временно недоступна")
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, v session) {
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	filter, ok := s.parseEventQuery(w, r, v, false)
	if !ok {
		return
	}
	filter.Cursor = r.URL.Query().Get("cursor")
	filter.Limit = limit
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	p, e := s.Telemetry.Events(ctx, filter)
	if e != nil {
		queryFailure(w, e)
		return
	}
	cp, e := s.Telemetry.Checkpoint(ctx)
	if e != nil {
		queryFailure(w, e)
		return
	}
	stale := true
	if t, e := time.Parse(time.RFC3339Nano, cp.ObservedAt); e == nil {
		stale = time.Since(t) > 15*time.Second
	}
	observed := time.Now().UTC().Format(time.RFC3339Nano)
	respond(w, 200, map[string]any{"items": p.Items, "next_cursor": p.NextCursor, "gap": p.Gap, "observed_at": observed, "ingestion": map[string]any{"observed_at": cp.ObservedAt, "stale": stale, "source_available": cp.SourceAvailable, "backlog": cp.Backlog, "dropped_records": cp.Dropped}, "retention_days": s.Telemetry.RawRetentionDays(), "record_limit": 0})
}
func (s *Server) eventClients(w http.ResponseWriter, r *http.Request, v session) {
	var scope *telemetry.Scope
	if v.Role == "manager" {
		sc, e := s.managerScope(r.Context(), v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		scope = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	items, e := s.Telemetry.EventClients(ctx, scope)
	if e != nil {
		queryFailure(w, e)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

// eventFacets handles GET /events/facets: server-side counters and histogram
// over a bounded window with the same filters and scope as /events, plus the
// required from/to (RFC3339) and step (1m|15m|1h) parameters. Manager scope is
// applied like /events; bad parameters return 400 invalid_filter.
func (s *Server) eventFacets(w http.ResponseWriter, r *http.Request, v session) {
	filter, ok := s.parseEventQuery(w, r, v, true)
	if !ok {
		return
	}
	step, ok := parseFacetsStep(r.URL.Query().Get("step"))
	if !ok {
		fail(w, 400, "invalid_filter", "Шаг должен быть 1m, 15m или 1h")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	f, e := s.Telemetry.Facets(ctx, filter, step)
	if e != nil {
		if errors.Is(e, telemetry.ErrFacetsWindow) {
			fail(w, 400, "invalid_filter", "Окно обязательно, не длиннее 31 суток и не больше 1440 корзин")
			return
		}
		queryFailure(w, e)
		return
	}
	// Resolve client names from the active snapshot; unknown and missing
	// clients get null names. "" stays as id:"" with name:null.
	names := map[string]string{}
	if m, e := s.Runtime.ReadManifest(); e == nil {
		for _, c := range m.Snapshot.Clients {
			names[c.ID] = c.Name
		}
	}
	type clientFacet struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Count int64   `json:"count"`
	}
	type methodFacet struct {
		Method string `json:"method"`
		Count  int64  `json:"count"`
	}
	type histBucket struct {
		Start   string `json:"start"`
		Allowed int64  `json:"allowed"`
		Denied  int64  `json:"denied"`
	}
	clients := make([]clientFacet, 0, len(f.Clients))
	for id, n := range f.Clients {
		var name *string
		if id != "" {
			if label, ok := names[id]; ok {
				name = &label
			}
		}
		clients = append(clients, clientFacet{ID: id, Name: name, Count: n})
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Count != clients[j].Count {
			return clients[i].Count > clients[j].Count
		}
		return clients[i].ID < clients[j].ID
	})
	methods := make([]methodFacet, 0, len(f.Methods))
	for m, n := range f.Methods {
		methods = append(methods, methodFacet{Method: m, Count: n})
	}
	sort.Slice(methods, func(i, j int) bool {
		if methods[i].Count != methods[j].Count {
			return methods[i].Count > methods[j].Count
		}
		return methods[i].Method < methods[j].Method
	})
	outcomes, e := s.Telemetry.Outcomes(ctx, filter)
	if e != nil {
		queryFailure(w, e)
		return
	}
	hist := make([]histBucket, 0, len(f.Histogram))
	for _, b := range f.Histogram {
		hist = append(hist, histBucket{Start: b.Start.UTC().Format(time.RFC3339), Allowed: b.Allowed, Denied: b.Denied})
	}
	respond(w, 200, map[string]any{
		"allowed":      f.Allowed,
		"denied":       f.Denied,
		"clients":      clients,
		"methods":      methods,
		"histogram":    hist,
		"deny_reasons": countList("reason", outcomes.DenyReasons),
		"site_errors":  countList("class", outcomes.SiteErrors),
	})
}

// validDenyReason accepts an empty filter or a reason logged by squid.conf.
// maxFilterValues bounds repeated client_id, method and deny_reason values.
const maxFilterValues = 64

// parseEventQuery reads the filters shared by /events, /events/facets and
// /events/domains. client_id, method and deny_reason repeat and match any
// value; unmatched=true adds unmatched (unknown) sources to the client filter;
// allowed_only=true is the opposite of failures_only.
// windowRequired demands from and to. A manager's scope is applied and a
// client outside it is 404. ok=false means the response is already written.
func (s *Server) parseEventQuery(w http.ResponseWriter, r *http.Request, v session, windowRequired bool) (telemetry.EventQuery, bool) {
	q := r.URL.Query()
	raw := q.Get("failures_only")
	if raw != "" && raw != "true" && raw != "false" {
		fail(w, 400, "invalid_filter", "Некорректный фильтр отказов")
		return telemetry.EventQuery{}, false
	}
	allowedOnly := q.Get("allowed_only")
	if allowedOnly != "" && allowedOnly != "true" && allowedOnly != "false" || allowedOnly == "true" && raw == "true" {
		fail(w, 400, "invalid_filter", "Некорректный фильтр разрешённых")
		return telemetry.EventQuery{}, false
	}
	unmatched := q.Get("unmatched")
	if unmatched != "" && unmatched != "true" && unmatched != "false" {
		fail(w, 400, "invalid_filter", "Некорректный фильтр неописанных источников")
		return telemetry.EventQuery{}, false
	}
	filter := telemetry.EventQuery{Failures: raw == "true", Allowed: allowedOnly == "true", SourceIP: q.Get("source_ip"),
		Destination: strings.TrimSpace(q.Get("destination")), Result: q.Get("result")}
	values := func(key string, max int) ([]string, bool) {
		var out []string
		for _, value := range q[key] {
			if value == "" {
				continue
			}
			if len(value) > max {
				return nil, false
			}
			if !slices.Contains(out, value) {
				out = append(out, value)
			}
		}
		return out, len(out) <= maxFilterValues
	}
	var okClients, okMethods, okReasons bool
	filter.ClientIDs, okClients = values("client_id", 128)
	if !okClients {
		fail(w, 400, "invalid_filter", "Некорректный клиент")
		return telemetry.EventQuery{}, false
	}
	if unmatched == "true" {
		filter.ClientIDs = append(filter.ClientIDs, "")
	}
	filter.Methods, okMethods = values("method", 24)
	filter.DenyReasons, okReasons = values("deny_reason", 64)
	invalid := !okMethods || !okReasons || len(filter.Destination) > 253 || len(filter.Result) > 64
	for _, reason := range filter.DenyReasons {
		if !validDenyReason(reason) {
			invalid = true
		}
	}
	if filter.SourceIP != "" {
		if _, err := netip.ParseAddr(filter.SourceIP); err != nil {
			invalid = true
		}
	}
	for key, at := range map[string]*time.Time{"from": &filter.From, "to": &filter.To} {
		value := q.Get(key)
		if value == "" {
			invalid = invalid || windowRequired
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			invalid = true
		}
		*at = t
	}
	if invalid {
		fail(w, 400, "invalid_filter", "Проверьте IP, длину фильтров и диапазон времени")
		return telemetry.EventQuery{}, false
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		fail(w, 400, "invalid_filter", "Параметр to должен быть позже from")
		return telemetry.EventQuery{}, false
	}
	if v.Role == "manager" {
		sc, e := s.managerScope(r.Context(), v)
		if e != nil {
			if !s.failStorageBusy(w, e) {
				fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			}
			return telemetry.EventQuery{}, false
		}
		for _, id := range filter.ClientIDs {
			if id != "" && !inScope(sc, id) {
				fail(w, 404, "not_found", "Клиент не найден")
				return telemetry.EventQuery{}, false
			}
		}
		filter.Scope = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
	return filter, true
}

func validDenyReason(r string) bool {
	return r == "" || slices.Contains(policy.DenyReasons, r)
}

// countList turns a facet map into [{<key>: k, count: n}] sorted by count.
func countList(key string, m map[string]int64) []map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{key: k, "count": m[k]})
	}
	return out
}

// parseFacetsStep maps the step query parameter to a duration.
func parseFacetsStep(s string) (time.Duration, bool) {
	switch s {
	case "1m":
		return time.Minute, true
	case "15m":
		return 15 * time.Minute, true
	case "1h":
		return time.Hour, true
	}
	return 0, false
}
func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request, v session) {
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if len(q.Get("actor")) > 256 || len(q.Get("action")) > 128 || len(q.Get("entity_id")) > 256 || strings.ContainsRune(q.Get("actor"), '\x00') {
		fail(w, 400, "invalid_filter", "Некорректный фильтр аудита")
		return
	}
	aq := store.AuditQuery{Actor: q.Get("actor"), Action: q.Get("action"), EntityID: q.Get("entity_id"), Cursor: q.Get("cursor"), Limit: limit}
	if v.Role == "manager" {
		sc, e := s.managerScope(r.Context(), v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		aq.Scope = &store.AuditScope{ClientIDs: sc.ClientIDs, Networks: sc.Networks, Actor: v.User}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	p, e := s.Store.AuditPage(ctx, aq)
	if e != nil {
		queryFailure(w, e)
		return
	}
	respond(w, 200, p)
}

// eventDomains handles GET /events/domains: requests grouped by host over a
// window with the same filters and scope as /events/facets (from and to are
// required, at most 31 days), busiest first, with the busiest sources of each,
// plus totals for the journal summary: requests, denials, bytes, per client
// and the number of distinct unmatched sources.
func (s *Server) eventDomains(w http.ResponseWriter, r *http.Request, v session) {
	filter, ok := s.parseEventQuery(w, r, v, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rep, e := s.Telemetry.Domains(ctx, filter)
	if e != nil {
		if errors.Is(e, telemetry.ErrFacetsWindow) {
			fail(w, 400, "invalid_filter", "Окно обязательно и не длиннее 31 суток")
			return
		}
		queryFailure(w, e)
		return
	}
	names := map[string]string{}
	if m, e := s.Runtime.ReadManifest(); e == nil {
		for _, c := range m.Snapshot.Clients {
			names[c.ID] = c.Name
		}
	}
	type source struct {
		ClientID   *string `json:"client_id"`
		ClientName *string `json:"client_name"`
		SourceIP   string  `json:"source_ip"`
		Requests   int64   `json:"requests"`
		Denied     int64   `json:"denied"`
	}
	type domain struct {
		Host        string   `json:"host"`
		Requests    int64    `json:"requests"`
		Denied      int64    `json:"denied"`
		Bytes       int64    `json:"bytes"`
		LastSeen    string   `json:"last_seen"`
		Ports       []int    `json:"ports"`
		SourceCount int      `json:"source_count"`
		Sources     []source `json:"sources"`
	}
	items := make([]domain, 0, len(rep.Items))
	for _, st := range rep.Items {
		d := domain{Host: st.Host, Requests: st.Requests, Denied: st.Denied, Bytes: st.Bytes,
			LastSeen: st.Last.UTC().Format(time.RFC3339Nano), Ports: st.Ports, SourceCount: st.Sources, Sources: make([]source, 0, len(st.Top))}
		if d.Ports == nil {
			d.Ports = []int{}
		}
		for _, src := range st.Top {
			out := source{SourceIP: src.SourceIP, Requests: src.Requests, Denied: src.Denied}
			if src.ClientID != "" {
				id := src.ClientID
				out.ClientID = &id
				if name, ok := names[id]; ok {
					out.ClientName = &name
				}
			}
			d.Sources = append(d.Sources, out)
		}
		items = append(items, d)
	}
	type clientTotal struct {
		ClientID   *string `json:"client_id"`
		ClientName *string `json:"client_name"`
		Requests   int64   `json:"requests"`
		Denied     int64   `json:"denied"`
	}
	clients := make([]clientTotal, 0, len(rep.Clients))
	for id, c := range rep.Clients {
		out := clientTotal{Requests: c.Requests, Denied: c.Denied}
		if id != "" {
			id := id
			out.ClientID = &id
			if name, ok := names[id]; ok {
				out.ClientName = &name
			}
		}
		clients = append(clients, out)
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Requests != clients[j].Requests {
			return clients[i].Requests > clients[j].Requests
		}
		return clientKey(clients[i].ClientID) < clientKey(clients[j].ClientID)
	})
	respond(w, 200, map[string]any{"items": items, "truncated": rep.Truncated, "summary": map[string]any{
		"requests": rep.Requests, "denied": rep.Denied, "bytes": rep.Bytes,
		"unmatched_sources": rep.UnmatchedSources, "clients": clients,
	}})
}

func clientKey(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}
