package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// HistBucket is one histogram bucket: [Start, Start+step).
type HistBucket struct {
	Start           time.Time
	Allowed, Denied int64
}

// Facets are server-side counters over a window with filters and scope
// applied: totals by result, by client_id, by method, and a stepped histogram.
// Clients keys by stable client_id; "" = unmatched (unknown) sources. The
// histogram spans [From, To) with exactly ceil((To-From)/step) buckets, oldest
// first; empty buckets are zero. Cursor/Limit in EventQuery are ignored.
type Facets struct {
	Allowed, Denied int64
	Clients         map[string]int64 // client_id -> count; "" = unmatched
	Methods         map[string]int64
	Histogram       []HistBucket
}

const (
	facetsMaxWindow  = 7 * 24 * time.Hour
	facetsMaxBuckets = 1440
)

// ErrFacetsWindow signals a missing, empty, oversized or too-fine-grained
// facets window. httpapi maps it to 400 invalid_filter.
var ErrFacetsWindow = errors.New("telemetry: invalid facets window")

// Facets aggregates counters over the [q.From, q.To) window across retained
// monthly files with the same filters and scope as Events. Aggregation is done
// by SQL GROUP BY on the event_ts index; unknown sources under a manager
// network scope are the only rows returned to Go (to filter by CIDR, which
// SQLite cannot). An empty (non-nil) scope returns zero facets; a specific
// client_id outside scope returns zeros.
func (t *Store) Facets(ctx context.Context, q EventQuery, step time.Duration) (Facets, error) {
	f := Facets{Clients: map[string]int64{}, Methods: map[string]int64{}, Histogram: []HistBucket{}}
	if q.From.IsZero() || q.To.IsZero() {
		return f, ErrFacetsWindow
	}
	from := q.From.UTC()
	to := q.To.UTC()
	if !to.After(from) {
		return f, ErrFacetsWindow
	}
	if to.Sub(from) > facetsMaxWindow {
		return f, ErrFacetsWindow
	}
	stepMS := step.Milliseconds()
	if stepMS <= 0 {
		return f, ErrFacetsWindow
	}
	fromMS := from.UnixMilli()
	toMS := to.UnixMilli()
	n := (toMS - fromMS + stepMS - 1) / stepMS
	if n <= 0 {
		return f, ErrFacetsWindow
	}
	if n > facetsMaxBuckets {
		return f, ErrFacetsWindow
	}
	for i := int64(0); i < n; i++ {
		f.Histogram = append(f.Histogram, HistBucket{Start: from.Add(time.Duration(i) * step).UTC()})
	}
	// Empty scope: nothing visible, mirroring Events, Series and DistinctClients.
	if q.Scope != nil && len(q.Scope.ClientIDs) == 0 && len(q.Scope.Networks) == 0 {
		return f, nil
	}
	// A client filter outside scope: no rows can contribute.
	if clientsOutsideScope(q) {
		return f, nil
	}
	// Long-window fast path: when the window is over 24h, aligned to 15-minute
	// boundaries, the step is 15m or 1h and there are no Destination/SourceIP
	// filters, read pre-aggregated 15-minute counters from rollups.db instead
	// of scanning raw monthly files. The manager scope (his clients + unknown
	// sources in his networks) is applied in Go, like the raw unknown path.
	if useFacetAggregate(q, from, to, step, fromMS, toMS) {
		if e := t.facetsAggregate(ctx, q, from, to, step, &f); e != nil {
			return f, e
		}
		return f, nil
	}
	// Scope != nil means a manager view: unmatched (NULL-client) sources are
	// visible only when their source_ip is inside a scope network, which SQLite
	// cannot test. facetsKnown therefore excludes NULL-client rows for any
	// non-nil scope; facetsUnknown reads them grouped by (source_ip, method,
	// bucket) and filters by network in Go. Without scope networks the unmatched
	// sources stay hidden, matching Events/Series. Admin (scope == nil) folds
	// NULL-client rows into "" via COALESCE in facetsKnown.
	needsKnownQuery := q.Scope == nil || len(q.Scope.ClientIDs) > 0
	needsUnknownQuery := (len(q.ClientIDs) == 0 || slices.Contains(q.ClientIDs, "")) && q.Scope != nil && len(q.Scope.Networks) > 0
	excludeNullClients := q.Scope != nil
	nets := parseNetworks(q.Scope)

	for _, key := range t.listMonths() {
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return f, e
		}
		if needsKnownQuery {
			if e := t.facetsKnown(ctx, mf, q, fromMS, stepMS, n, &f, excludeNullClients); e != nil {
				return f, e
			}
		}
		if needsUnknownQuery {
			if e := t.facetsUnknown(ctx, mf, q, fromMS, stepMS, nets, &f); e != nil {
				return f, e
			}
		}
	}
	return f, nil
}

// facetsKnown runs the aggregate GROUP BY over known-client (or all, for admin)
// events of one month: one row per (client_id, method, bucket). When
// excludeNullClients is true (any non-nil scope) the WHERE clause restricts to
// non-NULL client_id so NULL-client rows are handled by facetsUnknown (when
// networks allow) or hidden; otherwise COALESCE folds NULL into "" (admin view).
func (t *Store) facetsKnown(ctx context.Context, mf *monthFile, q EventQuery, fromMS, stepMS, n int64, f *Facets, excludeNullClients bool) error {
	where, whereArgs, destLike := buildEventWhere(q)
	if excludeNullClients {
		where = append(where, "e.client_id IS NOT NULL")
	}
	// Placeholder order in the SQL text is: SELECT bucket math first, then
	// WHERE args, then the destination LIKE. Args must follow that order.
	args := make([]any, 0, 2+len(whereArgs)+1)
	args = append(args, fromMS, stepMS)
	args = append(args, whereArgs...)
	var b strings.Builder
	b.WriteString("SELECT COALESCE(e.client_id,'') AS cid, COALESCE(e.method,'') AS m, (e.ts-?)/? AS b, " +
		"SUM(CASE WHEN e.failed=0 THEN e.count ELSE 0 END) AS a, SUM(CASE WHEN e.failed=1 THEN e.count ELSE 0 END) AS d " +
		"FROM event e")
	if destLike != "" {
		b.WriteString(" JOIN host hd ON hd.id=e.host_id")
	}
	b.WriteString(" WHERE " + strings.Join(where, " AND "))
	if destLike != "" {
		b.WriteString(" AND lower(hd.name) LIKE ? ESCAPE '\\'")
		args = append(args, destLike)
	}
	// GROUP BY references the SELECT aliases (cid, m, b); modernc/sqlite
	// supports this so the (ts, step) placeholders appear only once.
	b.WriteString(" GROUP BY cid, m, b")
	query := b.String()
	return t.monthReader(mf, ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var cid, mthd string
			var bucket, allowed, denied int64
			if e = rows.Scan(&cid, &mthd, &bucket, &allowed, &denied); e != nil {
				return e
			}
			f.Allowed += allowed
			f.Denied += denied
			f.Clients[cid] += allowed + denied
			f.Methods[mthd] += allowed + denied
			if bucket >= 0 && bucket < n {
				f.Histogram[bucket].Allowed += allowed
				f.Histogram[bucket].Denied += denied
			}
		}
		return rows.Err()
	})
}

// facetsUnknown runs the aggregate GROUP BY over NULL-client events of one
// month, grouped by (source_ip, method, bucket). Each row is filtered by the
// manager's scope networks in Go (SQLite has no CIDR match) and then folded
// into the "" client key, methods, totals and histogram. Admin scope never
// reaches here: NULL-client rows for admin are folded by facetsKnown via
// COALESCE.
func (t *Store) facetsUnknown(ctx context.Context, mf *monthFile, q EventQuery, fromMS, stepMS int64, nets []netip.Prefix, f *Facets) error {
	where, whereArgs, destLike := buildEventWhere(q)
	// buildEventWhere with a network scope adds "(client_id IN (...) OR
	// client_id IS NULL)" or "client_id IS NULL" when ClientIDs is empty.
	// Restrict to NULL here so we read only the unmatched rows.
	where = append(where, "e.client_id IS NULL")
	// Placeholder order in the SQL text is: SELECT bucket math first, then
	// WHERE args, then the destination LIKE.
	args := make([]any, 0, 2+len(whereArgs)+1)
	args = append(args, fromMS, stepMS)
	args = append(args, whereArgs...)
	var b strings.Builder
	b.WriteString("SELECT e.source_ip AS src, COALESCE(e.method,'') AS m, (e.ts-?)/? AS b, " +
		"SUM(CASE WHEN e.failed=0 THEN e.count ELSE 0 END) AS a, SUM(CASE WHEN e.failed=1 THEN e.count ELSE 0 END) AS d " +
		"FROM event e")
	if destLike != "" {
		b.WriteString(" JOIN host hd ON hd.id=e.host_id")
	}
	b.WriteString(" WHERE " + strings.Join(where, " AND "))
	if destLike != "" {
		b.WriteString(" AND lower(hd.name) LIKE ? ESCAPE '\\'")
		args = append(args, destLike)
	}
	b.WriteString(" GROUP BY src, m, b")
	query := b.String()
	return t.monthReader(mf, ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var src, mthd string
			var bucket, allowed, denied int64
			if e = rows.Scan(&src, &mthd, &bucket, &allowed, &denied); e != nil {
				return e
			}
			if !sourceInNetworks(src, nets) {
				continue
			}
			f.Allowed += allowed
			f.Denied += denied
			f.Clients[""] += allowed + denied
			f.Methods[mthd] += allowed + denied
			// Histogram bucket bounds are validated by the caller's n; an
			// event outside [From, To) cannot reach here because buildEventWhere
			// already constrained ts to the window.
			if bucket >= 0 && bucket < int64(len(f.Histogram)) {
				f.Histogram[bucket].Allowed += allowed
				f.Histogram[bucket].Denied += denied
			}
		}
		return rows.Err()
	})
}

// useFacetAggregate reports whether the long-window aggregate path applies:
// window over 24h, From and To aligned to 15-minute UTC boundaries, step 15m
// or 1h, and no Destination/SourceIP scalar filters (those need raw host/ip
// lookups the aggregate cannot answer).
func useFacetAggregate(q EventQuery, from, to time.Time, step time.Duration, fromMS, toMS int64) bool {
	if to.Sub(from) <= 24*time.Hour {
		return false
	}
	if q.Destination != "" || q.SourceIP != "" || len(q.DenyReasons) > 0 {
		return false
	}
	if step != 15*time.Minute && step != time.Hour {
		return false
	}
	const qMS = 15 * 60 * 1000
	return fromMS%qMS == 0 && toMS%qMS == 0
}

// facetsAggregate reads pre-aggregated 15-minute counters from facet_15m. The
// manager scope (his clients + unknown sources in his networks) is applied in
// Go since SQLite has no CIDR match; the histogram index maps each 15-min
// bucket into the requested step (15m: 1:1, 1h: 4:1). Result is by-construction
// equal to the raw path on the same data.
func (t *Store) facetsAggregate(ctx context.Context, q EventQuery, from, to time.Time, step time.Duration, f *Facets) error {
	fromSecs := from.Unix()
	toSecs := to.Unix()
	stepSecs := int64(step.Seconds())
	var b strings.Builder
	args := []any{fromSecs, toSecs}
	b.WriteString("SELECT COALESCE(client_id,'') AS cid, COALESCE(source_ip,'') AS src, COALESCE(method,'') AS m, failed, bucket, requests FROM facet_15m WHERE bucket>=? AND bucket<?")
	// facet_15m keeps unmatched sources under client_id "", so the filter
	// values map onto it directly.
	if len(q.ClientIDs) > 0 {
		b.WriteString(" AND client_id IN (" + placeholders(len(q.ClientIDs)) + ")")
		for _, id := range q.ClientIDs {
			args = append(args, id)
		}
	}
	if len(q.Methods) > 0 {
		b.WriteString(" AND method IN (" + placeholders(len(q.Methods)) + ")")
		for _, m := range q.Methods {
			args = append(args, m)
		}
	}
	if q.Result != "" {
		b.WriteString(" AND proxy_result=?")
		args = append(args, q.Result)
	}
	if q.Failures {
		b.WriteString(" AND failed=1")
	}
	if q.Allowed {
		b.WriteString(" AND failed=0")
	}
	nets := parseNetworks(q.Scope)
	n := int64(len(f.Histogram))
	e := t.rollReader(ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, b.String(), args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var cid, src, mthd string
			var failed int
			var bucket, requests int64
			if e = rows.Scan(&cid, &src, &mthd, &failed, &bucket, &requests); e != nil {
				return e
			}
			// Manager scope: only his clients and unknown sources in his networks.
			if q.Scope != nil {
				if cid != "" {
					if !scopeClientAllowed(q.Scope, cid) {
						continue
					}
				} else if !sourceInNetworks(src, nets) {
					continue
				}
			}
			allowed, denied := requests, int64(0)
			if failed != 0 {
				allowed, denied = 0, requests
			}
			f.Allowed += allowed
			f.Denied += denied
			f.Clients[cid] += requests
			f.Methods[mthd] += requests
			idx := (bucket - fromSecs) / stepSecs
			if idx >= 0 && idx < n {
				f.Histogram[idx].Allowed += allowed
				f.Histogram[idx].Denied += denied
			}
		}
		return rows.Err()
	})
	return e
}
