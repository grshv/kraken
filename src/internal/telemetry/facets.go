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
	facetsMaxWindow  = 31 * 24 * time.Hour
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
	// Windows longer than a day on whole hours with an hourly step read the
	// hourly journal aggregate instead of scanning raw monthly files.
	if longWindow(q) && step%time.Hour == 0 {
		return f, t.facetsLong(ctx, q, step, &f)
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
