package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/store"
	"kraken.local/kraken/internal/telemetry"
)

// CollectEvents is called by one background reader, never by a request handler.
func (s *Server) CollectEvents() error {
	// Below storageLowBytes event collection pauses so telemetry ingestion
	// cannot fill the disk or block the control DB. The next tick after space
	// frees resumes from the last checkpoint automatically.
	if s.freeBytes != nil {
		if free, e := s.freeBytes(); e == nil && free < storageLowBytes {
			s.setIngestion("storage_low")
			s.maybeLogStorageLow()
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cp, e := s.Telemetry.Checkpoint(ctx)
	if e != nil {
		s.setIngestion("error")
		return e
	}
	items, next, e := events.Read(s.Runtime.Options.LogPath, cp)
	if e != nil {
		s.setIngestion("error")
		return e
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
		s.setIngestion("error")
		return e
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
		return e
	}
	s.setIngestion("ok")
	return nil
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
	q := r.URL.Query()
	raw := q.Get("failures_only")
	if raw != "" && raw != "true" && raw != "false" {
		fail(w, 400, "invalid_filter", "Некорректный фильтр отказов")
		return
	}
	if len(q.Get("client_id")) > 128 {
		fail(w, 400, "invalid_filter", "Некорректный клиент")
		return
	}
	filter := telemetry.EventQuery{ClientID: q.Get("client_id"), Failures: raw == "true", Cursor: q.Get("cursor"), Limit: limit,
		SourceIP: q.Get("source_ip"), Destination: strings.TrimSpace(q.Get("destination")), Method: q.Get("method"), Result: q.Get("result")}
	invalid := len(filter.Destination) > 253 || len(filter.Method) > 24 || len(filter.Result) > 64
	if filter.SourceIP != "" {
		if _, err := netip.ParseAddr(filter.SourceIP); err != nil {
			invalid = true
		}
	}
	for _, value := range []string{q.Get("from"), q.Get("to")} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				invalid = true
			}
		}
	}
	var fromT, toT time.Time
	if q.Get("from") != "" {
		fromT, _ = time.Parse(time.RFC3339Nano, q.Get("from"))
		filter.From = fromT
	}
	if q.Get("to") != "" {
		toT, _ = time.Parse(time.RFC3339Nano, q.Get("to"))
		filter.To = toT
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		invalid = true
	}
	if invalid {
		fail(w, 400, "invalid_filter", "Проверьте IP, длину фильтров и диапазон времени")
		return
	}
	if v.Role == "manager" {
		sc, e := s.managerScope(r.Context(), v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		if filter.ClientID != "" && !inScope(sc, filter.ClientID) {
			fail(w, 404, "not_found", "Клиент не найден")
			return
		}
		filter.Scope = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
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
	q := r.URL.Query()
	raw := q.Get("failures_only")
	if raw != "" && raw != "true" && raw != "false" {
		fail(w, 400, "invalid_filter", "Некорректный фильтр отказов")
		return
	}
	if len(q.Get("client_id")) > 128 {
		fail(w, 400, "invalid_filter", "Некорректный клиент")
		return
	}
	filter := telemetry.EventQuery{
		ClientID:    q.Get("client_id"),
		Failures:    raw == "true",
		SourceIP:    q.Get("source_ip"),
		Destination: strings.TrimSpace(q.Get("destination")),
		Method:      q.Get("method"),
		Result:      q.Get("result"),
	}
	invalid := len(filter.Destination) > 253 || len(filter.Method) > 24 || len(filter.Result) > 64
	if filter.SourceIP != "" {
		if _, err := netip.ParseAddr(filter.SourceIP); err != nil {
			invalid = true
		}
	}
	fromT, err := time.Parse(time.RFC3339Nano, q.Get("from"))
	if err != nil {
		invalid = true
	}
	toT, err := time.Parse(time.RFC3339Nano, q.Get("to"))
	if err != nil {
		invalid = true
	}
	if invalid {
		fail(w, 400, "invalid_filter", "Проверьте IP, длину фильтров и диапазон времени")
		return
	}
	filter.From = fromT
	filter.To = toT
	if !filter.From.Before(filter.To) {
		fail(w, 400, "invalid_filter", "Параметр to должен быть позже from")
		return
	}
	step, ok := parseFacetsStep(q.Get("step"))
	if !ok {
		fail(w, 400, "invalid_filter", "Шаг должен быть 1m, 15m или 1h")
		return
	}
	if v.Role == "manager" {
		sc, e := s.managerScope(r.Context(), v)
		if e != nil {
			if s.failStorageBusy(w, e) {
				return
			}
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		if filter.ClientID != "" && !inScope(sc, filter.ClientID) {
			fail(w, 404, "not_found", "Клиент не найден")
			return
		}
		filter.Scope = &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	f, e := s.Telemetry.Facets(ctx, filter, step)
	if e != nil {
		if errors.Is(e, telemetry.ErrFacetsWindow) {
			fail(w, 400, "invalid_filter", "Окно обязательно, не длиннее 7 суток и не больше 1440 корзин")
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
	hist := make([]histBucket, 0, len(f.Histogram))
	for _, b := range f.Histogram {
		hist = append(hist, histBucket{Start: b.Start.UTC().Format(time.RFC3339), Allowed: b.Allowed, Denied: b.Denied})
	}
	respond(w, 200, map[string]any{
		"allowed":   f.Allowed,
		"denied":    f.Denied,
		"clients":   clients,
		"methods":   methods,
		"histogram": hist,
	})
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
