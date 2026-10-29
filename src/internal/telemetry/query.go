package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
)

// Series returns a [from,to) range with the given step (15m, 1h or 24h). The
// slice has exactly ceil((to-from)/step) points, oldest first; empty buckets
// are zero. clientID "" means all visible clients (including unknown sources in
// scope networks).
func (t *Store) Series(ctx context.Context, from, to time.Time, step time.Duration, clientID string, scope *Scope) ([]Bucket, error) {
	from = from.UTC()
	to = to.UTC()
	if to.Before(from) || step <= 0 {
		return []Bucket{}, nil
	}
	fromMS := from.UnixMilli()
	toMS := to.UnixMilli()
	// Bucket boundaries use the same floor as rollup so they hit stored rows.
	bucketStepMS := step.Milliseconds()
	if bucketStepMS <= 0 {
		return []Bucket{}, nil
	}
	n := (toMS - fromMS + bucketStepMS - 1) / bucketStepMS
	if n <= 0 {
		return []Bucket{}, nil
	}
	out := make([]Bucket, n)
	for i := int64(0); i < n; i++ {
		start := from.Unix() + i*int64(step.Seconds())
		out[i] = Bucket{Start: time.Unix(start, 0).UTC()}
	}
	// Decide which aggregate table matches the step.
	table, col, stepSecs, ok := seriesTable(step)
	if !ok {
		// Unsupported step: return zeroed buckets.
		return out, nil
	}
	// Build client filter intersected with scope. An empty (non-nil) scope
	// hides every client, so no client aggregate may contribute — leave
	// clients nil only when there is no scope (admin sees all clients).
	clientFilter := clientID
	clients := []string{}
	restrictClients := false
	if clientFilter != "" {
		if !scopeClientAllowed(scope, clientFilter) {
			return out, nil
		}
		clients = []string{clientFilter}
		restrictClients = true
	} else if scope != nil {
		clients = scopeIntersectClients(scope, scope.ClientIDs)
		restrictClients = true
	}
	fromSecs := from.Unix()
	toSecs := to.Unix()
	query, args := buildSeriesQuery(table, col, stepSecs, fromSecs, toSecs, clients, restrictClients)
	if query == "" {
		return out, nil
	}
	e := t.rollReader(ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var bucket int64
			var requests, denied, bytes int64
			if e = rows.Scan(&bucket, &requests, &denied, &bytes); e != nil {
				return e
			}
			idx := (bucket - fromSecs) / stepSecs
			if idx < 0 || idx >= int64(len(out)) {
				continue
			}
			out[idx].Requests += requests
			out[idx].Denied += denied
			out[idx].Bytes += bytes
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	// When clientID is "" the contract includes unknown sources: admin sees
	// all, a manager sees only unknowns inside his networks. Unknowns live in
	// unknown_15m / unknown_host_1h / unknown_host_1d. We read per
	// (bucket, source_ip) and filter by scope networks in Go (SQL has no CIDR
	// match); unknown sources are few by design. IPv4-mapped addresses are
	// handled by sourceInNetworks via Unmap.
	if clientFilter == "" && (scope == nil || len(scope.Networks) > 0) {
		if utable, ucol, ok := unknownSeriesTable(step); ok {
			uq := fmt.Sprintf("SELECT %s, source_ip, SUM(requests), SUM(denied), SUM(bytes) FROM %s WHERE %s>=? AND %s<? GROUP BY %s, source_ip", ucol, utable, ucol, ucol, ucol)
			uargs := []any{fromSecs, toSecs}
			nets := parseNetworks(scope)
			_ = t.rollReader(ctx, func(r *sql.DB) error {
				rows, e := r.QueryContext(ctx, uq, uargs...)
				if e != nil {
					return e
				}
				defer rows.Close()
				for rows.Next() {
					var bucket int64
					var sourceIP string
					var requests, denied, bytes int64
					if e = rows.Scan(&bucket, &sourceIP, &requests, &denied, &bytes); e != nil {
						return e
					}
					if scope != nil && !sourceInNetworks(sourceIP, nets) {
						continue
					}
					idx := (bucket - fromSecs) / stepSecs
					if idx < 0 || idx >= int64(len(out)) {
						continue
					}
					out[idx].Requests += requests
					out[idx].Denied += denied
					out[idx].Bytes += bytes
				}
				return rows.Err()
			})
		}
	}
	// Boundary supplement: when `to` aligns to a bucket start, events whose
	// ts equals `to` exactly fall in a bucket past our array (the half-open
	// aggregate range misses them). The original per-event filter counted
	// them in the last bucket via clamping; pull them from raw events so the
	// [from, to] window is inclusive at `to`.
	if bucketStepMS > 0 && toMS%bucketStepMS == 0 {
		rows, e := t.boundaryEvents(ctx, to, clients, scope)
		if e != nil {
			return nil, e
		}
		if idx := len(out) - 1; idx >= 0 {
			for _, ev := range rows {
				out[idx].Requests += ev.count
				out[idx].Bytes += ev.bytes
				if ev.failed {
					out[idx].Denied += ev.count
				}
			}
		}
	}
	return out, nil
}

// boundaryEventRow is one event whose ts equals the upper window boundary.
type boundaryEventRow struct {
	host   string
	bytes  int64
	count  int64
	failed bool
	client string // empty for unmatched sources
	source string
}

// boundaryEvents returns events whose ts equals `to` exactly, scoped the same
// way Series and TopHosts scope their aggregate queries. Used to supplement
// half-open aggregate reads so the [from, to] window is inclusive at `to`
// when `to` aligns to a bucket start.
func (t *Store) boundaryEvents(ctx context.Context, to time.Time, clients []string, scope *Scope) ([]boundaryEventRow, error) {
	toMS := to.UnixMilli()
	nets := parseNetworks(scope)
	out := []boundaryEventRow{}
	for _, key := range t.listMonths() {
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return nil, e
		}
		var q string
		var args []any
		switch {
		case len(clients) > 0:
			ph := placeholders(len(clients))
			q = "SELECT e.bytes,e.count,e.failed,e.client_id,e.source_ip,h.name FROM event e JOIN host h ON h.id=e.host_id WHERE e.ts=? AND e.client_id IN (" + ph + ")"
			args = []any{toMS}
			for _, c := range clients {
				args = append(args, c)
			}
		case scope != nil && len(scope.ClientIDs) > 0:
			ph := placeholders(len(scope.ClientIDs))
			q = "SELECT e.bytes,e.count,e.failed,e.client_id,e.source_ip,h.name FROM event e JOIN host h ON h.id=e.host_id WHERE e.ts=? AND (e.client_id IN (" + ph + ") OR e.client_id IS NULL)"
			args = []any{toMS}
			for _, c := range scope.ClientIDs {
				args = append(args, c)
			}
		case scope != nil:
			q = "SELECT e.bytes,e.count,e.failed,e.client_id,e.source_ip,h.name FROM event e JOIN host h ON h.id=e.host_id WHERE e.ts=? AND e.client_id IS NULL"
			args = []any{toMS}
		default:
			q = "SELECT e.bytes,e.count,e.failed,e.client_id,e.source_ip,h.name FROM event e JOIN host h ON h.id=e.host_id WHERE e.ts=?"
			args = []any{toMS}
		}
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			rows, e := r.QueryContext(ctx, q, args...)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var bytes, count int64
				var failed int
				var clientIDNS, sourceIPNS, hostNS sql.NullString
				if e = rows.Scan(&bytes, &count, &failed, &clientIDNS, &sourceIPNS, &hostNS); e != nil {
					return e
				}
				if scope != nil && (!clientIDNS.Valid || clientIDNS.String == "") {
					if !sourceInNetworks(sourceIPNS.String, nets) {
						continue
					}
				}
				if count <= 0 {
					count = 1
				}
				out = append(out, boundaryEventRow{
					host:   hostNS.String,
					bytes:  bytes,
					count:  count,
					failed: failed != 0,
					client: clientIDNS.String,
					source: sourceIPNS.String,
				})
			}
			return rows.Err()
		})
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

// seriesTable maps a step to its aggregate table and column.
func seriesTable(step time.Duration) (table, col string, stepSecs int64, ok bool) {
	switch {
	case step == 15*time.Minute:
		return "client_15m", "bucket", 15 * 60, true
	case step == time.Hour:
		return "client_host_1h", "bucket", 3600, true
	case step == 24*time.Hour:
		return "client_host_1d", "day", 86400, true
	}
	return "", "", 0, false
}

// buildSeriesQuery builds the client-aggregate query for Series. When
// restrictClients is true the caller is scoped and only the listed clients may
// contribute; an empty clients slice under restriction means no client is
// visible, so the query is unsatisfiable and "" is returned.
func buildSeriesQuery(table, col string, stepSecs, from, to int64, clients []string, restrictClients bool) (string, []any) {
	if restrictClients && len(clients) == 0 {
		return "", nil
	}
	var b strings.Builder
	args := []any{}
	fmt.Fprintf(&b, "SELECT %s, SUM(requests), SUM(denied), SUM(bytes) FROM %s WHERE %s>=? AND %s<?", col, table, col, col)
	args = append(args, from, to)
	if len(clients) > 0 {
		ph := placeholders(len(clients))
		b.WriteString(" AND client_id IN (" + ph + ")")
		for _, c := range clients {
			args = append(args, c)
		}
	}
	b.WriteString(" GROUP BY " + col)
	return b.String(), args
}

// unknownSeriesTable maps a Series step to the unknown-source aggregate table
// that carries the matching bucket granularity. 15m reads unknown_15m (no host
// dimension); 1h/24h collapse unknown_host_1h/1d by source_ip.
func unknownSeriesTable(step time.Duration) (table, col string, ok bool) {
	switch step {
	case 15 * time.Minute:
		return "unknown_15m", "bucket", true
	case time.Hour:
		return "unknown_host_1h", "bucket", true
	case 24 * time.Hour:
		return "unknown_host_1d", "day", true
	}
	return "", "", false
}

// HostCount is a per-host aggregate row.
type hostCountWire struct {
	Host string
	Counts
}

// TopHosts returns the busiest hosts for the given clients (intersected with
// scope), sorted by requests desc. For ranges older than HourlyRetention it
// reads daily aggregates (UTC-day aligned); otherwise hourly.
func (t *Store) TopHosts(ctx context.Context, from, to time.Time, clientIDs []string, scope *Scope, limit int) ([]HostCount, error) {
	if limit <= 0 {
		limit = 10
	}
	clientIDs = scopeIntersectClients(scope, clientIDs)
	if len(clientIDs) == 0 {
		return []HostCount{}, nil
	}
	from = from.UTC()
	to = to.UTC()
	hourlyCutoff := t.now().Add(-t.opts.HourlyRetention).Unix()
	useDaily := from.Unix() < hourlyCutoff
	table := "client_host_1h"
	col := "bucket"
	if useDaily {
		table = "client_host_1d"
		col = "day"
	}
	ph := placeholders(len(clientIDs))
	query := "SELECT host, SUM(requests), SUM(denied), SUM(bytes) FROM " + table + " WHERE " + col + ">=? AND " + col + "<? AND client_id IN (" + ph + ") GROUP BY host ORDER BY SUM(requests) DESC, host ASC LIMIT ?"
	args := []any{from.Unix(), to.Unix()}
	for _, c := range clientIDs {
		args = append(args, c)
	}
	args = append(args, limit)
	out := []HostCount{}
	e := t.rollReader(ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var h HostCount
			if e = rows.Scan(&h.Host, &h.Requests, &h.Denied, &h.Bytes); e != nil {
				return e
			}
			out = append(out, h)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	// Boundary supplement: events whose ts equals `to` exactly fall in a
	// bucket past the half-open aggregate range. Add them per host so the
	// [from, to] window is inclusive at `to` when `to` aligns to a bucket
	// start.
	stepMS := int64(3600000)
	if useDaily {
		stepMS = 86400000
	}
	if to.UnixMilli()%stepMS == 0 {
		bRows, e := t.boundaryEvents(ctx, to, clientIDs, scope)
		if e != nil {
			return nil, e
		}
		byHost := map[string]*HostCount{}
		for i := range out {
			byHost[out[i].Host] = &out[i]
		}
		for _, ev := range bRows {
			if ev.client == "" {
				continue // TopHosts counts client events only
			}
			h, ok := byHost[ev.host]
			if !ok {
				out = append(out, HostCount{Host: ev.host})
				h = &out[len(out)-1]
				byHost[ev.host] = h
			}
			h.Requests += ev.count
			h.Bytes += ev.bytes
			if ev.failed {
				h.Denied += ev.count
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Requests != out[j].Requests {
				return out[i].Requests > out[j].Requests
			}
			return out[i].Host < out[j].Host
		})
		if len(out) > limit {
			out = out[:limit]
		}
	}
	return out, nil
}

// SourceCount is an unknown-source aggregate.
type sourceCountWire struct {
	SourceIP string
	Counts
	LastSeen time.Time
	Top      []HostCount
}

// UnknownSources groups unknown-source events over [from,to) by source_ip, each
// with up to 5 top hosts. Scoped to networks in a non-nil scope.
func (t *Store) UnknownSources(ctx context.Context, from, to time.Time, scope *Scope, limit int) ([]SourceCount, error) {
	if limit <= 0 {
		limit = 10
	}
	from = from.UTC()
	to = to.UTC()
	hourlyCutoff := t.now().Add(-t.opts.HourlyRetention).Unix()
	useDaily := from.Unix() < hourlyCutoff
	table := "unknown_host_1h"
	col := "bucket"
	if useDaily {
		table = "unknown_host_1d"
		col = "day"
	}
	query := "SELECT source_ip, SUM(requests), SUM(denied), MAX(last_seen) FROM " + table + " WHERE " + col + ">=? AND " + col + "<? GROUP BY source_ip ORDER BY SUM(requests) DESC, source_ip ASC LIMIT ?"
	args := []any{from.Unix(), to.Unix(), limit}
	type agg struct {
		ip       string
		requests int64
		denied   int64
		last     int64
	}
	var aggs []agg
	e := t.rollReader(ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var a agg
			if e = rows.Scan(&a.ip, &a.requests, &a.denied, &a.last); e != nil {
				return e
			}
			aggs = append(aggs, a)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	nets := parseNetworks(scope)
	out := []SourceCount{}
	for _, a := range aggs {
		if scope != nil && !sourceInNetworks(a.ip, nets) {
			continue
		}
		sc := SourceCount{SourceIP: a.ip, Counts: Counts{Requests: a.requests, Denied: a.denied}, LastSeen: time.UnixMilli(a.last).UTC()}
		sc.Top = t.unknownTopHosts(ctx, table, col, from.Unix(), to.Unix(), a.ip, 5)
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].SourceIP < out[j].SourceIP
	})
	return out, nil
}

func (t *Store) unknownTopHosts(ctx context.Context, table, col string, from, to int64, ip string, limit int) []HostCount {
	q := "SELECT host, requests, denied FROM " + table + " WHERE " + col + ">=? AND " + col + "<? AND source_ip=? ORDER BY requests DESC, host ASC LIMIT ?"
	out := []HostCount{}
	_ = t.rollReader(ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, q, from, to, ip, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var h HostCount
			if e = rows.Scan(&h.Host, &h.Requests, &h.Denied); e != nil {
				return e
			}
			out = append(out, h)
		}
		return rows.Err()
	})
	return out
}

// Info reports telemetry storage size, oldest retained raw event and the count
// of files quarantined as broken.
type Info struct {
	Bytes       int64
	OldestRaw   time.Time
	BrokenFiles int
}

func (t *Store) Info(ctx context.Context) (Info, error) {
	var info Info
	info.BrokenFiles = t.broken
	var total int64
	var oldest int64
	months := t.listMonths()
	for _, key := range months {
		p := t.monthPath(key)
		if st, e := os.Stat(p); e == nil {
			total += st.Size()
			for _, suffix := range []string{"-wal", "-shm"} {
				if st, e := os.Stat(p + suffix); e == nil {
					total += st.Size()
				}
			}
		}
		// Oldest raw event timestamp from the oldest month's first row.
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			continue
		}
		var ts sql.NullInt64
		_ = t.monthReader(mf, ctx, func(r *sql.DB) error {
			return r.QueryRowContext(ctx, "SELECT ts FROM event ORDER BY ts ASC LIMIT 1").Scan(&ts)
		})
		if ts.Valid {
			if oldest == 0 || ts.Int64 < oldest {
				oldest = ts.Int64
			}
		}
	}
	if rp, e := os.Stat(t.rollPath()); e == nil {
		total += rp.Size()
		for _, suffix := range []string{"-wal", "-shm"} {
			if st, e := os.Stat(t.rollPath() + suffix); e == nil {
				total += st.Size()
			}
		}
	}
	info.Bytes = total
	if oldest > 0 {
		info.OldestRaw = time.UnixMilli(oldest).UTC()
	}
	return info, nil
}

// EventClients returns the latest label per client id across retained months.
func (t *Store) EventClients(ctx context.Context, scope *Scope) ([]map[string]string, error) {
	months := t.listMonths()
	type entry struct {
		id, name string
		seq      int64
	}
	latest := map[string]entry{}
	for i := len(months) - 1; i >= 0; i-- {
		key := months[i]
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return nil, e
		}
		_ = t.monthReader(mf, ctx, func(r *sql.DB) error {
			rows, e := r.QueryContext(ctx, "SELECT client_id, client_name, MAX(id) FROM event WHERE client_id IS NOT NULL AND client_id<>'' GROUP BY client_id ORDER BY MAX(id) DESC LIMIT 200")
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var id, name string
				var seq int64
				if e = rows.Scan(&id, &name, &seq); e != nil {
					return e
				}
				if scope != nil && !scopeClientAllowed(scope, id) {
					continue
				}
				if _, ok := latest[id]; !ok {
					latest[id] = entry{id, name, seq}
				}
			}
			return rows.Err()
		})
	}
	out := make([]map[string]string, 0, len(latest))
	for _, e := range latest {
		out = append(out, map[string]string{"id": e.id, "name": e.name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"] < out[j]["id"] })
	if len(out) > 200 {
		out = out[:200]
	}
	return out, nil
}

// DistinctClients counts distinct client_ids with activity in [from,to) from
// the 15m aggregate, scoped to the manager's clients when scope is non-nil.
// An empty (non-nil) scope sees no clients and returns 0.
func (t *Store) DistinctClients(ctx context.Context, from, to time.Time, scope *Scope, n *int64) error {
	clients := []string{}
	restrict := false
	if scope != nil {
		clients = scope.ClientIDs
		restrict = true
	}
	if restrict && len(clients) == 0 {
		*n = 0
		return nil
	}
	query := "SELECT COUNT(DISTINCT client_id) FROM client_15m WHERE bucket>=? AND bucket<?"
	args := []any{from.Unix(), to.Unix()}
	if len(clients) > 0 {
		ph := placeholders(len(clients))
		query += " AND client_id IN (" + ph + ")"
		for _, c := range clients {
			args = append(args, c)
		}
	}
	return t.rollReader(ctx, func(r *sql.DB) error {
		return r.QueryRowContext(ctx, query, args...).Scan(n)
	})
}

var _ = filepath.Join
var _ = events.Event{}
