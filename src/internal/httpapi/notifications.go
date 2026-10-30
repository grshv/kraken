package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

// notificationWindow is how far back notifications are computed from rollups.
const notificationWindow = 7 * 24 * time.Hour

// notification is one computed item. Kinds: "denied_domain" (a client was
// denied a host its policy does not list; protection and blocked-for-all
// denials are excluded) and "unknown_source" (traffic from an address no
// client describes). The id is stable across requests so marks stick to it.
type notification struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status"`
	ClientID   *string `json:"client_id"`
	ClientName *string `json:"client_name"`
	SourceIP   *string `json:"source_ip"`
	Host       *string `json:"host"`
	Requests   int64   `json:"requests"`
	Denied     int64   `json:"denied"`
	LastSeen   string  `json:"last_seen"`
	MarkedBy   *string `json:"marked_by"`
	MarkedAt   *string `json:"marked_at"`
}

func deniedID(clientID, host string) string { return "denied/" + clientID + "/" + host }
func sourceID(ip string) string             { return "source/" + ip }

// sourceDescribed reports whether a client of snap already covers the source
// address (an unparsable address counts as described and is skipped).
func sourceDescribed(snap policy.Snapshot, source string) bool {
	ip, e := netip.ParseAddr(source)
	if e != nil {
		return true
	}
	for _, c := range snap.Clients {
		if clientContainsIP(c, ip) {
			return true
		}
	}
	return false
}

// missingFromPolicy reports whether a denial of host for client is the kind a
// user can fix by allowing the domain: not a protection or blocked-for-all
// denial, the client is enabled, and its access (all web, own domains or an
// attached policy) does not already allow the host.
func missingFromPolicy(snap policy.Snapshot, settings policy.Settings, c *policy.Client, host string) bool {
	if !c.Enabled || policy.ProtectionDenial(host, 0, false, settings) {
		return false
	}
	if _, e := netip.ParseAddr(host); e == nil {
		return false
	}
	for _, d := range settings.BlockedDomains {
		if policy.DomainMatches(host, d.Domain, d.IncludeSubdomains) {
			return false
		}
	}
	return !policy.ClientAllows(snap, *c, host)
}

// computeNotifications builds the current list from rollups and the active
// snapshot, attaching marks. Scope filtering is done by the telemetry queries.
func (s *Server) computeNotifications(ctx context.Context, v session) ([]notification, error) {
	ts, _, e := s.inspectorTelemetryScope(ctx, v)
	if e != nil {
		return nil, e
	}
	marks, e := s.Store.NotificationMarks(ctx)
	if e != nil {
		return nil, e
	}
	snap := s.loadActiveManifestOrDraft(ctx)
	settings := policy.EffectiveSettings(snap)
	clients := map[string]*policy.Client{}
	for i := range snap.Clients {
		clients[snap.Clients[i].ID] = &snap.Clients[i]
	}
	now := s.now().UTC()
	from := now.Add(-notificationWindow)
	denied, e := s.Telemetry.DeniedHosts(ctx, from, now, ts, 500)
	if e != nil {
		return nil, e
	}
	sources, e := s.Telemetry.UnknownSources(ctx, from, now, ts, 100)
	if e != nil {
		return nil, e
	}
	out := []notification{}
	for _, d := range denied {
		c := clients[d.ClientID]
		if c == nil || !missingFromPolicy(snap, settings, c, d.Host) {
			continue
		}
		out = append(out, notification{
			ID: deniedID(c.ID, d.Host), Kind: "denied_domain",
			ClientID: strPtr(c.ID), ClientName: strPtr(c.Name), Host: strPtr(d.Host),
			Requests: d.Requests, Denied: d.Denied, LastSeen: d.LastSeen.Format(time.RFC3339),
		})
	}
	for _, src := range sources {
		if sourceDescribed(snap, src.SourceIP) {
			continue
		}
		out = append(out, notification{
			ID: sourceID(src.SourceIP), Kind: "unknown_source", SourceIP: strPtr(src.SourceIP),
			Requests: src.Requests, Denied: src.Denied, LastSeen: src.LastSeen.Format(time.RFC3339),
		})
	}
	for i := range out {
		out[i].Status = "new"
		if m, ok := marks[out[i].ID]; ok {
			out[i].Status = m.Status
			out[i].MarkedBy = strPtr(m.Actor)
			out[i].MarkedAt = strPtr(m.MarkedAt)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out, nil
}

// notifications lists computed notifications. Query: status = new | seen |
// ignored | all (default: new and seen), client_id, kind. counts are taken
// before the status filter so the UI can show every tab's size.
func (s *Server) notifications(w http.ResponseWriter, r *http.Request, v session) {
	q := r.URL.Query()
	status := q.Get("status")
	switch status {
	case "", "new", "seen", "ignored", "all":
	default:
		fail(w, 400, "invalid_status", "Неизвестный статус уведомления")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	all, e := s.computeNotifications(ctx, v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Уведомления временно недоступны")
		return
	}
	clientID, kind := q.Get("client_id"), q.Get("kind")
	counts := map[string]int{"new": 0, "seen": 0, "ignored": 0}
	items := []notification{}
	for _, n := range all {
		if clientID != "" && (n.ClientID == nil || *n.ClientID != clientID) {
			continue
		}
		if kind != "" && n.Kind != kind {
			continue
		}
		counts[n.Status]++
		if status == "all" || n.Status == status || (status == "" && n.Status != "ignored") {
			items = append(items, n)
		}
	}
	respond(w, 200, map[string]any{"window_days": int(notificationWindow / (24 * time.Hour)), "counts": counts, "items": items})
}

// validNotificationID checks the id shape and, for managers, that it belongs to
// one of their clients or networks.
func validNotificationID(id string, manager bool, scope store.Scope) bool {
	if len(id) > 300 {
		return false
	}
	if ip, ok := strings.CutPrefix(id, "source/"); ok {
		if _, e := netip.ParseAddr(ip); e != nil {
			return false
		}
		return !manager || networkInScope(scope, ip)
	}
	rest, ok := strings.CutPrefix(id, "denied/")
	if !ok {
		return false
	}
	clientID, host, ok := strings.Cut(rest, "/")
	if !ok || clientID == "" || host == "" || len(host) > 253 || strings.ContainsAny(host, "/ \t\r\n") {
		return false
	}
	return !manager || inScope(scope, clientID)
}

// markNotifications sets one status on a batch of notification ids.
func (s *Server) markNotifications(w http.ResponseWriter, r *http.Request, v session) {
	var in struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > store.MaxNotificationMarks {
		fail(w, 400, "invalid_ids", "Нужно от 1 до 500 уведомлений")
		return
	}
	if in.Status != "new" && in.Status != "seen" && in.Status != "ignored" {
		fail(w, 400, "invalid_status", "Неизвестный статус уведомления")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	scope, e := s.managerScope(ctx, v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	for _, id := range in.IDs {
		if !validNotificationID(id, v.Role == "manager", scope) {
			fail(w, 400, "invalid_ids", "Уведомление не найдено или недоступно")
			return
		}
	}
	if e := s.Store.MarkNotifications(ctx, in.IDs, in.Status, v.User); e != nil {
		if errors.Is(e, store.ErrBadMark) {
			fail(w, 400, "invalid_ids", "Нужно от 1 до 500 уведомлений")
			return
		}
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	respond(w, 200, map[string]any{"marked": len(in.IDs), "status": in.Status})
}
