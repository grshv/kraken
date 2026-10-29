package httpapi

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/store"
)

// CollectEvents is called by one background reader, never by a request handler.
func (s *Server) CollectEvents() error {
	cp, e := s.Store.EventCheckpoint()
	if e != nil {
		return e
	}
	items, next, e := events.Read(s.Runtime.Options.LogPath, cp)
	if e != nil {
		return e
	}
	manifest, e := s.Runtime.ReadManifest()
	if e != nil {
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
	return s.Store.SaveEvents(items, next)
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
	if errors.Is(e, store.ErrCursor) {
		fail(w, 400, "invalid_cursor", "Страница устарела или фильтры изменились. Откройте последние записи")
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
	filter := store.EventQuery{ClientID: q.Get("client_id"), Failures: raw == "true", Cursor: q.Get("cursor"), Limit: limit,
		SourceIP: q.Get("source_ip"), Destination: strings.TrimSpace(q.Get("destination")), Method: q.Get("method"), Result: q.Get("result"), From: q.Get("from"), To: q.Get("to")}
	invalid := len(filter.Destination) > 253 || len(filter.Method) > 24 || len(filter.Result) > 64
	if filter.SourceIP != "" {
		if _, err := netip.ParseAddr(filter.SourceIP); err != nil {
			invalid = true
		}
	}
	for _, value := range []string{filter.From, filter.To} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				invalid = true
			}
		}
	}
	if filter.From != "" && filter.To != "" {
		from, _ := time.Parse(time.RFC3339Nano, filter.From)
		to, _ := time.Parse(time.RFC3339Nano, filter.To)
		invalid = invalid || !from.Before(to)
	}
	if invalid {
		fail(w, 400, "invalid_filter", "Проверьте IP, длину фильтров и диапазон времени")
		return
	}
	if v.Role == "manager" {
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		// Manager cannot query a foreign client_id directly: treat as not found.
		if filter.ClientID != "" && !inScope(scope, filter.ClientID) {
			fail(w, 404, "not_found", "Клиент не найден")
			return
		}
		filter.Scope = &store.EventScope{ClientIDs: scope.ClientIDs, Networks: scope.Networks}
	}
	p, e := s.Store.EventPage(filter)
	if e != nil {
		queryFailure(w, e)
		return
	}
	cp, e := s.Store.EventCheckpoint()
	if e != nil {
		queryFailure(w, e)
		return
	}
	stale := true
	if t, e := time.Parse(time.RFC3339Nano, cp.ObservedAt); e == nil {
		stale = time.Since(t) > 15*time.Second
	}
	p.Gap = p.Gap || cp.Gap
	respond(w, 200, map[string]any{"items": p.Items, "next_cursor": p.NextCursor, "gap": p.Gap, "observed_at": p.ObservedAt, "ingestion": map[string]any{"observed_at": cp.ObservedAt, "stale": stale, "source_available": cp.SourceAvailable, "backlog": cp.Backlog, "dropped_records": cp.Dropped}, "retention_days": store.EventRetentionDays, "record_limit": store.EventLimit})
}
func (s *Server) eventClients(w http.ResponseWriter, r *http.Request, v session) {
	var items []map[string]string
	if v.Role == "manager" {
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		items, e = s.Store.EventClientsScope(scope.ClientIDs)
		if e != nil {
			queryFailure(w, e)
			return
		}
	} else {
		var e error
		items, e = s.Store.EventClients()
		if e != nil {
			queryFailure(w, e)
			return
		}
	}
	respond(w, 200, map[string]any{"items": items})
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
		scope, e := s.managerScope(v)
		if e != nil {
			fail(w, 503, "storage_unavailable", "Хранилище недоступно")
			return
		}
		aq.Scope = &store.AuditScope{ClientIDs: scope.ClientIDs, Networks: scope.Networks, Actor: v.User}
	}
	p, e := s.Store.AuditPage(aq)
	if e != nil {
		queryFailure(w, e)
		return
	}
	respond(w, 200, p)
}
