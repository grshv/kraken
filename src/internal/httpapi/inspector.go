package httpapi

import (
	"context"
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
	"kraken.local/kraken/internal/telemetry"
)

// inspectorTelemetryScope returns the telemetry scope for inspector reads and
// the store.Scope for handler-level policy-visibility checks. Nil telemetry
// scope = admin (no filtering). A non-nil error means the manager scope could
// not be loaded; the handler surfaces it via failStorageBusy.
func (s *Server) inspectorTelemetryScope(ctx context.Context, v session) (*telemetry.Scope, store.Scope, error) {
	if v.Role != "manager" {
		return nil, store.Scope{}, nil
	}
	sc, e := s.managerScope(ctx, v)
	if e != nil {
		return nil, store.Scope{}, e
	}
	return &telemetry.Scope{ClientIDs: sc.ClientIDs, Networks: sc.Networks}, sc, nil
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
// hosts are not DNS-resolved here; a domain that resolved to a protected or
// internal address is recognised from the reason Squid logged (deny_reason
// "protected", or "private" while internal destinations stay closed).
func explainEvent(v events.Event, snap policy.Snapshot) explanation {
	settings := policy.EffectiveSettings(snap)
	host, port := splitDestination(v.Destination)
	if policy.ProtectionDenial(host, port, v.Method == "CONNECT", settings) {
		return explanation{"denied", "protected_destination", nil, nil}
	}
	if v.DenyReason != nil && (*v.DenyReason == "protected" || (*v.DenyReason == "private" && !settings.AllowPrivateDestinations)) {
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
		return explanation{"denied", "client_disabled", strPtr(client.ID), nil}
	}
	if authFailed {
		return explanation{"denied", "auth_failed", strPtr(client.ID), nil}
	}
	switch client.Access {
	case policy.AccessAllWeb:
		return explanation{"allowed", "allowed_all_web", strPtr(client.ID), nil}
	case policy.AccessList:
		// policy_id names the set that allows the host; nil means the
		// client's own domains.
		if id, ok := policy.AllowingSource(snap, *client, host); ok {
			var p *string
			if id != "" {
				p = strPtr(id)
			}
			return explanation{"allowed", "allowed_by_policy", strPtr(client.ID), p}
		}
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

// eventFailed mirrors the ingestion failed rule so the inspector counts
// denials the same way the stored failed column does.
func eventFailed(v events.Event) bool {
	return strings.Contains(v.ProxyResult, "DENIED") || strings.Contains(v.ProxyResult, "FAIL") || (v.ProxyHTTPStatus != nil && *v.ProxyHTTPStatus >= 400)
}

// clientKnown reports whether a client ID exists in the draft or the active
// revision; the inspector does not fabricate stats for unknown IDs.
func (s *Server) clientKnown(ctx context.Context, id string) bool {
	_, ok := s.findClient(ctx, id)
	return ok
}

// findClient resolves a client from the draft first, then the active revision.
func (s *Server) findClient(ctx context.Context, id string) (policy.Client, bool) {
	var d Draft
	if s.Store.Get(ctx, "draft", &d) == nil {
		for _, c := range d.Snapshot.Clients {
			if c.ID == id {
				return c, true
			}
		}
	}
	if m, e := s.Runtime.ReadManifest(); e == nil {
		for _, c := range m.Snapshot.Clients {
			if c.ID == id {
				return c, true
			}
		}
	}
	return policy.Client{}, false
}

// policySnapshot resolves a policy from the draft first, then the active
// revision. Returns the snapshot that owns the policy so its clients are used.
func (s *Server) policySnapshot(ctx context.Context, id string) (policy.Snapshot, bool) {
	var d Draft
	if s.Store.Get(ctx, "draft", &d) == nil {
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
	ts, scope, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if !s.clientKnown(ctx, id) || (ts != nil && !inScope(scope, id)) {
		fail(w, 404, "not_found", "Клиент не найден")
		return
	}
	now := s.now().UTC()
	from := now.Add(-24 * time.Hour)
	buckets, e := s.Telemetry.Series(ctx, from, now, 15*time.Minute, id, ts)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Статистика клиента временно недоступна")
		return
	}
	top, e := s.Telemetry.TopHosts(ctx, from, now, []string{id}, ts, 10)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Статистика клиента временно недоступна")
		return
	}
	var stats struct{ requests, denied, bytes int64 }
	series := make([]map[string]any, len(buckets))
	for i, b := range buckets {
		stats.requests += b.Requests
		stats.denied += b.Denied
		stats.bytes += b.Bytes
		series[i] = map[string]any{
			"start":    from.Add(time.Duration(i) * 15 * time.Minute).UTC().Format(time.RFC3339Nano),
			"requests": b.Requests,
			"denied":   b.Denied,
			"bytes":    b.Bytes,
		}
	}
	topOut := make([]map[string]any, len(top))
	for i, d := range top {
		topOut[i] = map[string]any{"host": d.Host, "requests": d.Requests, "denied": d.Denied}
	}
	open, reason := s.openConnections(ctx)
	respond(w, 200, map[string]any{
		"window_hours":                        24,
		"requests":                            stats.requests,
		"denied":                              stats.denied,
		"bytes":                               stats.bytes,
		"series":                              series,
		"open_connections":                    open,
		"open_connections_unavailable_reason": reason,
		"top_destinations":                    topOut,
	})
}

func (s *Server) inspectorPolicyHits(w http.ResponseWriter, r *http.Request, v session) {
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	snap, found := s.policySnapshot(ctx, id)
	if !found {
		fail(w, 404, "not_found", "Политика не найдена")
		return
	}
	ts, scope, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	if ts != nil {
		active := s.loadActiveManifestOrDraft(r.Context())
		refs, mine := 0, false
		for _, c := range active.Clients {
			if policy.UsesPolicy(c, id) {
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
		if policy.UsesPolicy(c, id) {
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
	domains, e := s.destinationHits(ctx, clientIDs, ts, destinations)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Попадания по политике временно недоступны")
		return
	}
	respond(w, 200, map[string]any{"domains": domains})
}

// inspectorClientHits counts 24 h / 7 d requests of the client to each of its
// own domains (attached policies have /inspector/policies/{id}/hits).
func (s *Server) inspectorClientHits(w http.ResponseWriter, r *http.Request, v session) {
	id := r.PathValue("id")
	ts, scope, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	client, ok := s.findClient(ctx, id)
	if !ok || (ts != nil && !inScope(scope, id)) {
		fail(w, 404, "not_found", "Клиент не найден")
		return
	}
	domains, e := s.destinationHits(ctx, []string{id}, ts, client.Destinations)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Попадания по доменам клиента временно недоступны")
		return
	}
	respond(w, 200, map[string]any{"domains": domains})
}

// destinationHits sums the requests of clientIDs to hosts matching each
// destination over the last 24 hours and 7 days (top 500 hosts per window).
func (s *Server) destinationHits(ctx context.Context, clientIDs []string, ts *telemetry.Scope, destinations []policy.Destination) ([]map[string]any, error) {
	now := s.now().UTC()
	from24 := now.Add(-24 * time.Hour)
	from7 := now.Add(-7 * 24 * time.Hour)
	h24, e := s.Telemetry.TopHosts(ctx, from24, now, clientIDs, ts, 500)
	if e != nil {
		return nil, e
	}
	h7, e := s.Telemetry.TopHosts(ctx, from7, now, clientIDs, ts, 500)
	if e != nil {
		return nil, e
	}
	m24 := map[string]int64{}
	for _, h := range h24 {
		m24[h.Host] = h.Requests
	}
	m7 := map[string]int64{}
	for _, h := range h7 {
		m7[h.Host] = h.Requests
	}
	domains := make([]map[string]any, len(destinations))
	for i, d := range destinations {
		h24hits, h7hits := int64(0), int64(0)
		for host, n := range m24 {
			if policy.DomainMatches(host, d.Domain, d.IncludeSubdomains) {
				h24hits += n
			}
		}
		for host, n := range m7 {
			if policy.DomainMatches(host, d.Domain, d.IncludeSubdomains) {
				h7hits += n
			}
		}
		domains[i] = map[string]any{"domain": d.Domain, "include_subdomains": d.IncludeSubdomains, "hits_24h": h24hits, "hits_7d": h7hits}
	}
	return domains, nil
}

func (s *Server) inspectorUnknownSources(w http.ResponseWriter, r *http.Request, v session) {
	ts, _, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	now := s.now().UTC()
	from := now.Add(-24 * time.Hour)
	srcs, e := s.Telemetry.UnknownSources(ctx, from, now, ts, 100)
	if e != nil {
		fail(w, 503, "storage_unavailable", "Неописанные источники временно недоступны")
		return
	}
	// The rollups remember a source for 24 h; once a client covers it in the
	// active configuration it is no longer undescribed.
	snap := s.loadActiveManifestOrDraft(ctx)
	items := make([]map[string]any, 0, len(srcs))
	for _, sc := range srcs {
		if sourceDescribed(snap, sc.SourceIP) {
			continue
		}
		dests := make([]map[string]any, 0, len(sc.Top))
		for _, d := range sc.Top {
			dests = append(dests, map[string]any{"host": d.Host, "requests": d.Requests, "denied": d.Denied})
		}
		last := ""
		if !sc.LastSeen.IsZero() {
			last = sc.LastSeen.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, map[string]any{"source_ip": sc.SourceIP, "requests": sc.Requests, "denied": sc.Denied, "last_seen": last, "top_destinations": dests})
	}
	respond(w, 200, map[string]any{"sources": items})
}

func (s *Server) inspectorEventExplain(w http.ResponseWriter, r *http.Request, v session) {
	ts, scope, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ev, e := s.Telemetry.Event(ctx, r.PathValue("event_id"), ts)
	if errors.Is(e, telemetry.ErrNotFound) {
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
	snap := m.Snapshot
	if ts != nil {
		snap = managerDraftView(m.Snapshot, scope)
	}
	ex := explainEvent(ev, snap)
	respond(w, 200, map[string]any{
		"decision":  ex.decision,
		"reason":    ex.reason,
		"client_id": ex.clientID,
		"policy_id": ex.policyID,
		// logged_reason is what Squid recorded at the time (deny_reason).
		"logged_reason": ev.DenyReason,
	})
}

func (s *Server) inspectorDraftImpact(w http.ResponseWriter, r *http.Request, v session) {
	ts, _, e := s.inspectorTelemetryScope(r.Context(), v)
	if e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
		fail(w, 503, "storage_unavailable", "Хранилище недоступно")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	now := s.now().UTC()
	rows, e := s.Telemetry.RecentEvents(ctx, now.Add(-24*time.Hour), now, ts)
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
	if e := s.Store.Get(ctx, "draft", &d); e != nil {
		if s.failStorageBusy(w, e) {
			return
		}
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
		n := int(ev.Count)
		if n <= 0 {
			n = 1
		}
		if draft.decision == "allowed" {
			totalAllowed += n
		} else {
			totalDenied += n
		}
		if ev.ClientID != nil {
			b := perClient[*ev.ClientID]
			if b == nil {
				b = &bucket{}
				perClient[*ev.ClientID] = b
			}
			if draft.decision == "allowed" {
				b.allowed += n
			} else {
				b.denied += n
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

// instanceStatus reports instance identity, data-volume free space, telemetry
// size and event journal freshness for the status panel. Disk figures come from
// statfs of the directory holding the SQLite database; a statfs failure surfaces
// as null rather than a 503 so the rest of status stays useful.
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
	var lastEvent, checkpointAt, lastApplied, telBytes any
	var eventsOldest any
	var telBroken int
	if s.Telemetry != nil {
		info, e := s.Telemetry.Info(ctx)
		if e == nil {
			telBytes = info.Bytes
			telBroken = info.BrokenFiles
			if !info.OldestRaw.IsZero() {
				eventsOldest = info.OldestRaw.UTC().Format(time.RFC3339)
			}
		}
		if cp, e := s.Telemetry.Checkpoint(ctx); e == nil {
			if ts, ok := lastEventTimestampFromTelemetry(ctx, s); ok {
				lastEvent = ts
			}
			if cp.ObservedAt != "" {
				checkpointAt = cp.ObservedAt
			}
		}
	}
	if ts, ok, e := s.Store.LastAppliedAt(ctx); e == nil && ok {
		lastApplied = ts
	}
	return map[string]any{
		"name":                   name,
		"data_free_bytes":        dataFree,
		"data_total_bytes":       dataTotal,
		"journal_last_event_at":  lastEvent,
		"journal_checkpoint_at":  checkpointAt,
		"last_applied_at":        lastApplied,
		"telemetry_bytes":        telBytes,
		"events_oldest_at":       eventsOldest,
		"telemetry_broken_files": telBroken,
	}
}

// lastEventTimestampFromTelemetry returns the timestamp of the most recent
// ingested event across retained months.
func lastEventTimestampFromTelemetry(ctx context.Context, s *Server) (string, bool) {
	page, e := s.Telemetry.Events(ctx, telemetry.EventQuery{Limit: 1})
	if e != nil || len(page.Items) == 0 {
		return "", false
	}
	return page.Items[0].Timestamp, true
}

// inspectorHost strips the port and IPv6 brackets from a stored destination so
// dstdomain matching and top-destination grouping use the bare hostname.
// events.Parse already reduces a log URL to its bare host, but stored data is
// untrusted, so any smuggled path/query (/...), userinfo (user:pass@) or port
// is stripped here as defence-in-depth before the host reaches a response.
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
