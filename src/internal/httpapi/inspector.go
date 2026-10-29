package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kraken.local/kraken/internal/events"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/store"
)

// inspectorEventScope returns the event-scope filter for inspector reads. Nil
// means admin (no filtering). For a manager it is his assigned clients and
// networks from managerScope; the store.Scope is also returned for handler-
// level checks (inScope, managerDraftView). A store error yields ok=false so
// the handler answers 503 storage_unavailable, matching the journal.
func (s *Server) inspectorEventScope(v session) (*store.EventScope, store.Scope, bool) {
	if v.Role != "manager" {
		return nil, store.Scope{}, true
	}
	sc, e := s.managerScope(v)
	if e != nil {
		return nil, store.Scope{}, false
	}
	return &store.EventScope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}, sc, true
}

// explanation is the result of evaluating one event against a snapshot.
type explanation struct {
	decision string
	reason   string
	clientID *string
	policyID *string
}

// explainEvent re-evaluates a completed request against a typed snapshot the
// same way for the active policy and the draft. It mirrors the order of
// http_access protective denials Squid renders: protected destinations and
// blocked-for-all domains run before client rules, so a protected request is
// reported as protected_destination even when a matching client exists. Domain
// hosts are not DNS-resolved, so a domain that resolves to a protected address
// is not flagged; this is a static assessment, not a runtime proof.
func explainEvent(v events.Event, snap policy.Snapshot) explanation {
	settings := policy.EffectiveSettings(snap)
	host, port := splitDestination(v.Destination)
	if policy.ProtectionDenial(host, port, v.Method == "CONNECT", settings) {
		return explanation{"denied", "protected_destination", nil, nil}
	}
	for _, d := range settings.BlockedDomains {
		if policy.DomainMatches(host, d.Domain, d.IncludeSubdomains) {
			return explanation{"denied", "blocked_for_all", nil, nil}
		}
	}
	client, authFailed := matchClient(v, snap)
	if client == nil {
		return explanation{"denied", "unknown_source", nil, nil}
	}
	if !client.Enabled {
		return explanation{"denied", "client_disabled", strPtr(client.ID), strPtr(client.PolicyID)}
	}
	if authFailed {
		return explanation{"denied", "auth_failed", strPtr(client.ID), strPtr(client.PolicyID)}
	}
	for i := range snap.Policies {
		p := &snap.Policies[i]
		if p.ID != client.PolicyID {
			continue
		}
		if p.Mode == "all_web" {
			return explanation{"allowed", "allowed_all_web", strPtr(client.ID), strPtr(p.ID)}
		}
		for _, d := range p.Destinations {
			if policy.DomainMatches(host, d.Domain, d.IncludeSubdomains) {
				return explanation{"allowed", "allowed_by_policy", strPtr(client.ID), strPtr(p.ID)}
			}
		}
		return explanation{"denied", "not_in_allowlist", strPtr(client.ID), strPtr(p.ID)}
	}
	return explanation{"denied", "not_in_allowlist", strPtr(client.ID), nil}
}

// matchClient finds the active-policy client that governs a request, mirroring
// the source-IP/username attribution CollectEvents uses at ingest. It returns
// the first enabled client whose networks contain the source IP (with the
// proxy_username constraint for auth-bound clients); otherwise the first
// disabled IP match (client_disabled); or an enabled auth-bound IP match whose
// username did not match (auth_failed). Validate guarantees at most one enabled
// non-auth client overlap, and non-auth/auth enabled overlaps are rejected.
func matchClient(v events.Event, snap policy.Snapshot) (*policy.Client, bool) {
	ip, err := netip.ParseAddr(v.SourceIP)
	if err != nil {
		return nil, false
	}
	var firstDisabled *policy.Client
	var firstAuth *policy.Client
	for i := range snap.Clients {
		c := &snap.Clients[i]
		if !clientContainsIP(*c, ip) {
			continue
		}
		if c.ProxyUsername != "" {
			if v.ProxyUsername != nil && *v.ProxyUsername == c.ProxyUsername {
				return c, false
			}
			if c.Enabled && firstAuth == nil {
				firstAuth = c
			}
			continue
		}
		if c.Enabled {
			return c, false
		}
		if firstDisabled == nil {
			firstDisabled = c
		}
	}
	if firstAuth != nil {
		return firstAuth, true
	}
	return firstDisabled, false
}

func clientContainsIP(c policy.Client, ip netip.Addr) bool {
	for _, raw := range c.Networks {
		if p, e := netip.ParsePrefix(raw); e == nil {
			if p.Contains(ip) || p.Contains(ip.Unmap()) {
				return true
			}
			continue
		}
		if a, e := netip.ParseAddr(raw); e == nil && (a == ip || a == ip.Unmap()) {
			return true
		}
	}
	return false
}

func splitDestination(destination string) (host string, port int) {
	h, p, err := net.SplitHostPort(destination)
	if err != nil {
		return strings.TrimPrefix(strings.TrimSuffix(destination, "]"), "["), 0
	}
	n, _ := strconv.Atoi(p)
	return h, n
}

func strPtr(s string) *string { return &s }

// eventFailed mirrors the SaveEvents failed rule so the inspector counts
// denials the same way the stored failed column does.
func eventFailed(v events.Event) bool {
	return strings.Contains(v.ProxyResult, "DENIED") || strings.Contains(v.ProxyResult, "FAIL") || (v.ProxyHTTPStatus != nil && *v.ProxyHTTPStatus >= 400)
}

// clientKnown reports whether a client ID exists in the draft or the active
// revision; the inspector does not fabricate stats for unknown IDs.
func (s *Server) clientKnown(id string) bool {
	var d Draft
	if s.Store.Get("draft", &d) == nil {
		for _, c := range d.Snapshot.Clients {
			if c.ID == id {
				return true
			}
		}
	}
	if m, e := s.Runtime.ReadManifest(); e == nil {
		for _, c := range m.Snapshot.Clients {
			if c.ID == id {
				return true
			}
		}
	}
	return false
}

// policySnapshot resolves a policy from the draft first, then the active
// revision. Returns the snapshot that owns the policy so its clients are used.
func (s *Server) policySnapshot(id string) (policy.Snapshot, bool) {
	var d Draft
	if s.Store.Get("draft", &d) == nil {
		for _, p := range d.Snapshot.Policies {
			if p.ID == id {
				return d.Snapshot, true
			}
		}
	}
	if m, e := s.Runtime.ReadManifest(); e == nil {
		for _, p := range m.Snapshot.Policies {
			if p.ID == id {
				return m.Snapshot, true
			}
		}
	}
	return policy.Snapshot{}, false
}

func (s *Server) openConnections(ctx context.Context) (any, any) {
	m := s.Runtime.Metrics(ctx)
	if m.Available && m.ActiveRequests != nil {
		return int(*m.ActiveRequests), nil
	}
	reason := "squid_metrics_unavailable"
	if m.Reason != "" {
		reason = m.Reason
	}
	return nil, reason
}

func (s *Server) inspectorClientStats(w http.ResponseWriter, r *http.Request, v session) {
	id := r.PathValue("id")
	es, scope, ok := s.inspectorEventScope(v)
	if !ok {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	// A manager must not learn whether a foreign client exists: out-of-scope is
	// reported as 404, the same as a missing ID.
	if !s.clientKnown(id) || (es != nil && !inScope(scope, id)) {
		fail(w, 404, "not_found", "Клиент не найден")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	stats, e := s.Store.InspectorClientStats(ctx, now, id, es)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Статистика клиента временно недоступна")
		return
	}
	from := now.Add(-24 * time.Hour)
	series := make([]map[string]any, len(stats.Buckets))
	for i, b := range stats.Buckets {
		series[i] = map[string]any{
			"start":    from.Add(time.Duration(i) * 15 * time.Minute).UTC().Format(time.RFC3339Nano),
			"requests": b.Requests,
			"denied":   b.Denied,
			"bytes":    b.Bytes,
		}
	}
	top := make([]map[string]any, len(stats.TopDestinations))
	for i, d := range stats.TopDestinations {
		top[i] = map[string]any{"host": d.Host, "requests": d.Requests, "denied": d.Denied}
	}
	open, reason := s.openConnections(ctx)
	respond(w, 200, map[string]any{
		"window_hours":                        24,
		"requests":                            stats.Requests,
		"denied":                              stats.Denied,
		"bytes":                               stats.Bytes,
		"series":                              series,
		"open_connections":                    open,
		"open_connections_unavailable_reason": reason,
		"top_destinations":                    top,
	})
}

func (s *Server) inspectorPolicyHits(w http.ResponseWriter, r *http.Request, v session) {
	id := r.PathValue("id")
	snap, found := s.policySnapshot(id)
	if !found {
		fail(w, 404, "not_found", "Политика не найдена")
		return
	}
	es, scope, ok := s.inspectorEventScope(v)
	if !ok {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	// A manager sees a policy only if at least one of his clients references it
	// in the active revision, or no client references it at all. A policy
	// referenced only by foreign clients is reported as 404.
	if es != nil {
		active := s.loadActiveManifestOrDraft()
		refs, mine := 0, false
		for _, c := range active.Clients {
			if c.PolicyID == id {
				refs++
				if inScope(scope, c.ID) {
					mine = true
				}
			}
		}
		if refs > 0 && !mine {
			fail(w, 404, "not_found", "Политика не найдена")
			return
		}
	}
	var clientIDs []string
	for _, c := range snap.Clients {
		if c.PolicyID == id {
			clientIDs = append(clientIDs, c.ID)
		}
	}
	var destinations []policy.Destination
	for _, p := range snap.Policies {
		if p.ID == id {
			destinations = p.Destinations
			break
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	rows, e := s.Store.InspectorPolicyEvents(ctx, now, clientIDs, es)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Попадания по политике временно недоступны")
		return
	}
	from24 := now.Add(-24 * time.Hour)
	from7 := now.Add(-7 * 24 * time.Hour)
	domains := make([]map[string]any, len(destinations))
	for i, d := range destinations {
		h24, h7 := 0, 0
		for _, ev := range rows {
			if !policy.DomainMatches(inspectorHost(ev.Destination), d.Domain, d.IncludeSubdomains) {
				continue
			}
			t, _ := time.Parse(time.RFC3339Nano, ev.Timestamp)
			if !t.Before(from7) && !t.After(now) {
				h7++
			}
			if !t.Before(from24) && !t.After(now) {
				h24++
			}
		}
		domains[i] = map[string]any{"domain": d.Domain, "include_subdomains": d.IncludeSubdomains, "hits_24h": h24, "hits_7d": h7}
	}
	respond(w, 200, map[string]any{"domains": domains})
}

func (s *Server) inspectorUnknownSources(w http.ResponseWriter, r *http.Request, v session) {
	es, _, ok := s.inspectorEventScope(v)
	if !ok {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	rows, e := s.Store.InspectorUnknownEvents(ctx, now, es)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Неописанные источники временно недоступны")
		return
	}
	type dst struct {
		Host     string
		Requests int64
		Denied   int64
	}
	type src struct {
		IP       string
		Requests int64
		Denied   int64
		LastSeen string
		Dests    map[string]*dst
	}
	sources := map[string]*src{}
	for _, ev := range rows {
		s := sources[ev.SourceIP]
		if s == nil {
			s = &src{IP: ev.SourceIP, Dests: map[string]*dst{}}
			sources[ev.SourceIP] = s
		}
		s.Requests++
		failed := eventFailed(ev)
		if failed {
			s.Denied++
		}
		if s.LastSeen == "" || ev.Timestamp > s.LastSeen {
			s.LastSeen = ev.Timestamp
		}
		host := inspectorHost(ev.Destination)
		d := s.Dests[host]
		if d == nil {
			d = &dst{Host: host}
			s.Dests[host] = d
		}
		d.Requests++
		if failed {
			d.Denied++
		}
	}
	items := make([]map[string]any, 0, len(sources))
	for _, s := range sources {
		dests := make([]map[string]any, 0, len(s.Dests))
		for _, d := range s.Dests {
			dests = append(dests, map[string]any{"host": d.Host, "requests": d.Requests, "denied": d.Denied})
		}
		sort.Slice(dests, func(i, j int) bool {
			if dests[i]["requests"].(int64) != dests[j]["requests"].(int64) {
				return dests[i]["requests"].(int64) > dests[j]["requests"].(int64)
			}
			return dests[i]["host"].(string) < dests[j]["host"].(string)
		})
		if len(dests) > 10 {
			dests = dests[:10]
		}
		items = append(items, map[string]any{"source_ip": s.IP, "requests": s.Requests, "denied": s.Denied, "last_seen": s.LastSeen, "top_destinations": dests})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i]["requests"].(int64) != items[j]["requests"].(int64) {
			return items[i]["requests"].(int64) > items[j]["requests"].(int64)
		}
		return items[i]["source_ip"].(string) < items[j]["source_ip"].(string)
	})
	respond(w, 200, map[string]any{"sources": items})
}

func (s *Server) inspectorEventExplain(w http.ResponseWriter, r *http.Request, v session) {
	es, scope, ok := s.inspectorEventScope(v)
	if !ok {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ev, e := s.Store.InspectorEvent(ctx, r.PathValue("event_id"), es)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "not_found", "Событие не найдено")
		return
	}
	if e != nil {
		fail(w, 503, "storage_unavailable", "Событие временно недоступно")
		return
	}
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Активная политика недоступна")
		return
	}
	// A manager explains against his filtered view so a foreign client or an
	// invisible policy never appears in the response; admin uses the full snapshot.
	snap := m.Snapshot
	if es != nil {
		snap = managerDraftView(m.Snapshot, scope)
	}
	ex := explainEvent(ev, snap)
	respond(w, 200, map[string]any{
		"decision":  ex.decision,
		"reason":    ex.reason,
		"client_id": ex.clientID,
		"policy_id": ex.policyID,
	})
}

func (s *Server) inspectorDraftImpact(w http.ResponseWriter, r *http.Request, v session) {
	es, _, ok := s.inspectorEventScope(v)
	if !ok {
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	now := s.now().UTC()
	rows, e := s.Store.InspectorRecentEvents(ctx, now, es)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Журнал временно недоступен")
		return
	}
	m, e := s.Runtime.ReadManifest()
	if e != nil {
		fail(w, 503, "runtime_unavailable", "Активная политика недоступна")
		return
	}
	var d Draft
	if e := s.Store.Get("draft", &d); e != nil {
		fail(w, 503, "storage_unavailable", "Черновик недоступен")
		return
	}
	totalAllowed, totalDenied := 0, 0
	type bucket struct{ allowed, denied int }
	perClient := map[string]*bucket{}
	for _, ev := range rows {
		active := explainEvent(ev, m.Snapshot)
		draft := explainEvent(ev, d.Snapshot)
		if active.decision == draft.decision {
			continue
		}
		if draft.decision == "allowed" {
			totalAllowed++
		} else {
			totalDenied++
		}
		if ev.ClientID != nil {
			b := perClient[*ev.ClientID]
			if b == nil {
				b = &bucket{}
				perClient[*ev.ClientID] = b
			}
			if draft.decision == "allowed" {
				b.allowed++
			} else {
				b.denied++
			}
		}
	}
	clients := make([]map[string]any, 0, len(perClient))
	for id, b := range perClient {
		clients = append(clients, map[string]any{"client_id": id, "newly_allowed": b.allowed, "newly_denied": b.denied})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i]["client_id"].(string) < clients[j]["client_id"].(string) })
	respond(w, 200, map[string]any{
		"window_hours":  24,
		"newly_allowed": totalAllowed,
		"newly_denied":  totalDenied,
		"clients":       clients,
	})
}

// inspectorDomainTemplates is the built-in static set of package-registry
// destinations offered as starting points for allowlist policies.
var inspectorDomainTemplates = []struct {
	ID, Name     string
	Destinations []policy.Destination
}{
	{"ubuntu-apt", "Ubuntu APT", []policy.Destination{
		{Domain: "archive.ubuntu.com", IncludeSubdomains: false},
		{Domain: "security.ubuntu.com", IncludeSubdomains: false},
		{Domain: "ports.ubuntu.com", IncludeSubdomains: false},
		{Domain: "changelogs.ubuntu.com", IncludeSubdomains: false},
	}},
	{"debian-apt", "Debian APT", []policy.Destination{
		{Domain: "deb.debian.org", IncludeSubdomains: false},
		{Domain: "security.debian.org", IncludeSubdomains: false},
		{Domain: "ftp.debian.org", IncludeSubdomains: false},
	}},
	{"pypi", "PyPI", []policy.Destination{
		{Domain: "pypi.org", IncludeSubdomains: false},
		{Domain: "files.pythonhosted.org", IncludeSubdomains: false},
	}},
	{"npm", "npm", []policy.Destination{
		{Domain: "registry.npmjs.org", IncludeSubdomains: false},
	}},
	{"go-modules", "Go modules", []policy.Destination{
		{Domain: "proxy.golang.org", IncludeSubdomains: false},
		{Domain: "sum.golang.org", IncludeSubdomains: false},
	}},
	{"docker-hub", "Docker Hub", []policy.Destination{
		{Domain: "registry-1.docker.io", IncludeSubdomains: false},
		{Domain: "auth.docker.io", IncludeSubdomains: false},
	}},
	{"github", "GitHub", []policy.Destination{
		{Domain: "github.com", IncludeSubdomains: false},
		{Domain: "objects.githubusercontent.com", IncludeSubdomains: false},
		{Domain: "codeload.github.com", IncludeSubdomains: false},
		{Domain: "api.github.com", IncludeSubdomains: false},
		{Domain: "raw.githubusercontent.com", IncludeSubdomains: false},
	}},
	{"maven-central", "Maven Central", []policy.Destination{
		{Domain: "repo1.maven.org", IncludeSubdomains: false},
		{Domain: "repo.maven.apache.org", IncludeSubdomains: false},
	}},
}

func (s *Server) inspectorDomainTemplates(w http.ResponseWriter, r *http.Request, v session) {
	templates := make([]map[string]any, len(inspectorDomainTemplates))
	for i, t := range inspectorDomainTemplates {
		dests := make([]map[string]any, len(t.Destinations))
		for j, d := range t.Destinations {
			dests[j] = map[string]any{"domain": d.Domain, "include_subdomains": d.IncludeSubdomains}
		}
		templates[i] = map[string]any{"id": t.ID, "name": t.Name, "destinations": dests}
	}
	respond(w, 200, map[string]any{"templates": templates})
}

// instanceStatus reports instance identity, data-volume free space and event
// journal freshness for the status panel. Disk figures come from statfs of the
// directory holding the SQLite database; a statfs failure surfaces as null
// rather than a 503 so the rest of status stays useful.
func (s *Server) instanceStatus(ctx context.Context) map[string]any {
	name := os.Getenv("KRAKEN_INSTANCE_NAME")
	if name == "" {
		if h, e := os.Hostname(); e == nil {
			name = h
		}
	}
	var dataFree, dataTotal any
	if dir := s.Store.DataDir(); dir != "" {
		var stat syscall.Statfs_t
		if e := syscall.Statfs(dir, &stat); e == nil {
			dataTotal = uint64(stat.Blocks) * uint64(stat.Bsize)
			dataFree = uint64(stat.Bavail) * uint64(stat.Bsize)
		}
	}
	var lastEvent, checkpointAt, lastApplied any
	if ts, ok, e := s.Store.LastEventTimestamp(ctx); e == nil && ok {
		lastEvent = ts
	}
	if cp, e := s.Store.EventCheckpoint(); e == nil && cp.ObservedAt != "" {
		checkpointAt = cp.ObservedAt
	}
	if ts, ok, e := s.Store.LastAppliedAt(ctx); e == nil && ok {
		lastApplied = ts
	}
	return map[string]any{
		"name":                  name,
		"data_free_bytes":       dataFree,
		"data_total_bytes":      dataTotal,
		"journal_last_event_at": lastEvent,
		"journal_checkpoint_at": checkpointAt,
		"last_applied_at":       lastApplied,
	}
}

// inspectorHost strips the port and IPv6 brackets from a stored destination so
// dstdomain matching and top-destination grouping use the bare hostname.
// events.Parse already reduces a log URL to its bare host, but stored bodies
// are untrusted data, so any smuggled path/query (/...), userinfo (user:pass@)
// or port is stripped here as defence-in-depth before the host reaches a
// response.
func inspectorHost(destination string) string {
	h := destination
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	}
	return host
}
