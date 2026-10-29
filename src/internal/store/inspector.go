package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
)

// inScope reports whether a stored event is visible under the given scope. A
// nil scope admits everything (admin view). A non-nil scope admits an event
// whose client_id is in ClientIDs, or — when client_id is empty — whose
// source_ip is inside one of Networks (CIDR or single address, with
// IPv4-mapped addresses unmapped). Empty lists admit nothing.
func inScope(v events.Event, scope *EventScope) bool {
	if scope == nil {
		return true
	}
	if v.ClientID != nil {
		for _, id := range scope.ClientIDs {
			if id == *v.ClientID {
				return true
			}
		}
		return false
	}
	ip, err := netip.ParseAddr(v.SourceIP)
	if err != nil {
		return false
	}
	for _, raw := range scope.Networks {
		if p, e := netip.ParsePrefix(raw); e == nil && (p.Contains(ip) || p.Contains(ip.Unmap())) {
			return true
		}
		if a, e := netip.ParseAddr(raw); e == nil && (a == ip || a == ip.Unmap()) {
			return true
		}
	}
	return false
}

// scopeAllowsClient reports whether a single client_id is visible under scope.
// A nil scope admits every client; a non-nil scope admits only listed clients.
func scopeAllowsClient(scope *EventScope, clientID string) bool {
	if scope == nil {
		return true
	}
	for _, id := range scope.ClientIDs {
		if id == clientID {
			return true
		}
	}
	return false
}

// scopeFilterClients restricts a client_id list to those visible under scope.
// A nil scope passes the input through unchanged.
func scopeFilterClients(scope *EventScope, clientIDs []string) []string {
	if scope == nil {
		return clientIDs
	}
	allowed := map[string]bool{}
	for _, id := range scope.ClientIDs {
		allowed[id] = true
	}
	out := []string{}
	for _, id := range clientIDs {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}

// ClientStatsBucket is one 15-minute bucket of the inspector client series.
type ClientStatsBucket struct {
	Requests int64
	Denied   int64
	Bytes    int64
}

// ClientTopDestination is a per-host aggregate within the client 24h window.
type ClientTopDestination struct {
	Host     string
	Requests int64
	Denied   int64
}

// ClientStats is the per-client 24h aggregate the inspector renders. Buckets is
// exactly 96 entries (oldest first); the last bucket contains the current
// moment. Denied follows the SaveEvents failed column, not the origin-403 rule.
type ClientStats struct {
	Requests        int64
	Denied          int64
	Bytes           int64
	Buckets         []ClientStatsBucket
	TopDestinations []ClientTopDestination
}

// coarseFrom returns the ingested_at lower bound (unix seconds) for a window of
// the given hours ending at now. Because ingested_at >= Event.Timestamp for
// every retained row (the reader ingests a line after Squid writes it), this is
// a safe superset of the precise Event.Timestamp window and lets the client_id
// and ingested_at indexes select rows without per-row JSON extraction in SQL.
func coarseFrom(now time.Time, hours int) int64 {
	return now.UTC().Add(-time.Duration(hours) * time.Hour).Unix()
}

// hostOf strips the port and IPv6 brackets from a destination stored as host or
// host:port, mirroring how dstdomain matching discards the port. events.Parse
// already reduces a log URL to its bare host, but stored bodies are untrusted
// data, so any smuggled path/query (/...), userinfo (user:pass@) or port is
// stripped here as defence-in-depth before the host reaches a response.
func hostOf(destination string) string {
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

// InspectorClientStats aggregates one client's events over a rolling
// [now-24h, now] window by Event.Timestamp. The client_id index plus a coarse
// ingested_at bound select a superset; Go applies the precise window, 96
// 15-minute buckets and top destinations. Denied uses the stored failed column.
func (s *Store) InspectorClientStats(ctx context.Context, now time.Time, clientID string, scope *EventScope) (ClientStats, error) {
	out := ClientStats{Buckets: make([]ClientStatsBucket, 96), TopDestinations: []ClientTopDestination{}}
	if !scopeAllowsClient(scope, clientID) {
		return out, nil
	}
	from := now.UTC().Add(-24 * time.Hour)
	rows, e := s.DB.QueryContext(ctx, "SELECT body,failed FROM proxy_events WHERE client_id=? AND ingested_at>=? ORDER BY seq DESC LIMIT ?", clientID, coarseFrom(now, 24), EventLimit)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	dests := map[string]*ClientTopDestination{}
	for rows.Next() {
		var body string
		var failed int
		if e = rows.Scan(&body, &failed); e != nil {
			return out, e
		}
		var v events.Event
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			return out, errors.New("invalid stored event")
		}
		if !inScope(v, scope) {
			continue
		}
		t, e := time.Parse(time.RFC3339Nano, v.Timestamp)
		if e != nil {
			return out, errors.New("invalid stored event timestamp")
		}
		if t.Before(from) || t.After(now) {
			continue
		}
		if v.Bytes < 0 || v.Bytes > math.MaxInt64-out.Bytes {
			return out, errors.New("invalid observed byte total")
		}
		bucket := int(t.Sub(from) / (15 * time.Minute))
		if bucket < 0 {
			bucket = 0
		}
		if bucket >= 96 {
			bucket = 95
		}
		out.Requests++
		out.Buckets[bucket].Requests++
		out.Bytes += v.Bytes
		if failed != 0 {
			out.Denied++
			out.Buckets[bucket].Denied++
		}
		host := hostOf(v.Destination)
		d := dests[host]
		if d == nil {
			d = &ClientTopDestination{Host: host}
			dests[host] = d
		}
		d.Requests++
		if failed != 0 {
			d.Denied++
		}
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	for _, d := range dests {
		out.TopDestinations = append(out.TopDestinations, *d)
	}
	sort.Slice(out.TopDestinations, func(i, j int) bool {
		if out.TopDestinations[i].Requests != out.TopDestinations[j].Requests {
			return out.TopDestinations[i].Requests > out.TopDestinations[j].Requests
		}
		return out.TopDestinations[i].Host < out.TopDestinations[j].Host
	})
	if len(out.TopDestinations) > 10 {
		out.TopDestinations = out.TopDestinations[:10]
	}
	return out, nil
}

// InspectorPolicyEvents returns retained events for the given clients over a
// rolling [now-7d, now] window so the handler can count 24h and 7d domain hits.
// The client_id index plus a coarse ingested_at bound select a superset; Go
// applies the precise window. Only timestamp and destination are needed by the
// caller, but the full body is unmarshalled once per row to match the codebase.
func (s *Store) InspectorPolicyEvents(ctx context.Context, now time.Time, clientIDs []string, scope *EventScope) ([]events.Event, error) {
	out := []events.Event{}
	clientIDs = scopeFilterClients(scope, clientIDs)
	if len(clientIDs) == 0 {
		return out, nil
	}
	from := now.UTC().Add(-7 * 24 * time.Hour)
	placeholders := strings.Repeat("?,", len(clientIDs))
	q := "SELECT body FROM proxy_events WHERE client_id IN (" + placeholders[:len(placeholders)-1] + ") AND ingested_at>=? ORDER BY seq DESC LIMIT ?"
	args := make([]any, 0, len(clientIDs)+2)
	for _, id := range clientIDs {
		args = append(args, id)
	}
	args = append(args, coarseFrom(now, 168), EventLimit)
	rows, e := s.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if e = rows.Scan(&body); e != nil {
			return out, e
		}
		var v events.Event
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			return out, errors.New("invalid stored event")
		}
		if !inScope(v, scope) {
			continue
		}
		t, e := time.Parse(time.RFC3339Nano, v.Timestamp)
		if e != nil {
			return out, errors.New("invalid stored event timestamp")
		}
		if t.Before(from) || t.After(now) {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InspectorUnknownEvents returns retained events without a client_id over a
// rolling [now-24h, now] window for the handler to group by source_ip. The
// client_id index selects NULL rows (SQLite indexes NULLs) with a coarse
// ingested_at bound; Go applies the precise window.
func (s *Store) InspectorUnknownEvents(ctx context.Context, now time.Time, scope *EventScope) ([]events.Event, error) {
	out := []events.Event{}
	from := now.UTC().Add(-24 * time.Hour)
	rows, e := s.DB.QueryContext(ctx, "SELECT body FROM proxy_events WHERE client_id IS NULL AND ingested_at>=? ORDER BY seq DESC LIMIT ?", coarseFrom(now, 24), EventLimit)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if e = rows.Scan(&body); e != nil {
			return out, e
		}
		var v events.Event
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			return out, errors.New("invalid stored event")
		}
		if !inScope(v, scope) {
			continue
		}
		t, e := time.Parse(time.RFC3339Nano, v.Timestamp)
		if e != nil {
			return out, errors.New("invalid stored event timestamp")
		}
		if t.Before(from) || t.After(now) {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InspectorRecentEvents returns retained events over a rolling [now-24h, now]
// window for draft-impact assessment. A coarse ingested_at bound selects a
// superset; Go applies the precise window before the handler re-evaluates each
// event against the active policy and the draft.
func (s *Store) InspectorRecentEvents(ctx context.Context, now time.Time, scope *EventScope) ([]events.Event, error) {
	out := []events.Event{}
	from := now.UTC().Add(-24 * time.Hour)
	rows, e := s.DB.QueryContext(ctx, "SELECT body FROM proxy_events WHERE ingested_at>=? ORDER BY seq DESC LIMIT ?", coarseFrom(now, 24), EventLimit)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if e = rows.Scan(&body); e != nil {
			return out, e
		}
		var v events.Event
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			return out, errors.New("invalid stored event")
		}
		if !inScope(v, scope) {
			continue
		}
		t, e := time.Parse(time.RFC3339Nano, v.Timestamp)
		if e != nil {
			return out, errors.New("invalid stored event timestamp")
		}
		if t.Before(from) || t.After(now) {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InspectorEvent returns one event by its event_id, or sql.ErrNoRows. A non-nil
// scope hides events outside the manager's view (foreign client or unknown
// source outside his networks) as sql.ErrNoRows, so the handler answers 404 the
// same as for a missing ID.
func (s *Store) InspectorEvent(ctx context.Context, eventID string, scope *EventScope) (events.Event, error) {
	var v events.Event
	var body string
	e := s.DB.QueryRowContext(ctx, "SELECT body FROM proxy_events WHERE event_id=?", eventID).Scan(&body)
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal([]byte(body), &v); e != nil {
		return v, e
	}
	if !inScope(v, scope) {
		return v, sql.ErrNoRows
	}
	return v, nil
}

// LastEventTimestamp returns the timestamp of the most recently ingested event
// (the last row by seq), or (false) when no events are retained.
func (s *Store) LastEventTimestamp(ctx context.Context) (string, bool, error) {
	var body string
	e := s.DB.QueryRowContext(ctx, "SELECT body FROM proxy_events ORDER BY seq DESC LIMIT 1").Scan(&body)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	var v events.Event
	if e = json.Unmarshal([]byte(body), &v); e != nil {
		return "", false, e
	}
	return v.Timestamp, true, nil
}

// LastAppliedAt returns the updated_at of the most recent succeeded apply
// operation, or (false) when none exists.
func (s *Store) LastAppliedAt(ctx context.Context) (string, bool, error) {
	var body string
	e := s.DB.QueryRowContext(ctx, "SELECT body FROM operations WHERE json_extract(body,'$.status')='succeeded' AND json_extract(body,'$.action')='apply' ORDER BY rowid DESC LIMIT 1").Scan(&body)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	if e != nil {
		return "", false, e
	}
	var op struct {
		Updated string `json:"updated_at"`
	}
	if e = json.Unmarshal([]byte(body), &op); e != nil {
		return "", false, e
	}
	return op.Updated, true, nil
}

// DataDir returns the directory holding the main SQLite database file, derived
// from PRAGMA database_list. Empty for in-memory or fileless databases.
func (s *Store) DataDir() string {
	rows, e := s.DB.Query("PRAGMA database_list")
	if e != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if e := rows.Scan(&seq, &name, &file); e != nil || name != "main" {
			continue
		}
		if file == "" || strings.HasPrefix(file, ":") {
			return ""
		}
		return filepath.Dir(file)
	}
	return ""
}
