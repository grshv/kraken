package telemetry

import (
	"context"
	"database/sql"
	"strings"
)

// Outcomes counts why requests failed over a window, with the same filters
// and scope as Events. DenyReasons keys are policy.DenyReasons values,
// "proxy_auth" (407, missing or wrong proxy password) and "other" (denied by
// an expert rule or logged before reasons existed). SiteErrors counts failed
// requests that were not denied by the proxy: "4xx", "5xx" or "network"
// (no HTTP status, e.g. connect or DNS failure).
type Outcomes struct {
	DenyReasons map[string]int64
	SiteErrors  map[string]int64
}

// Outcomes reads raw monthly files, or journal_1h for windows longer than a
// day (longWindow); the window is bounded like Facets.
func (t *Store) Outcomes(ctx context.Context, q EventQuery) (Outcomes, error) {
	out := Outcomes{DenyReasons: map[string]int64{}, SiteErrors: map[string]int64{}}
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > facetsMaxWindow {
		return out, ErrFacetsWindow
	}
	if q.Scope != nil && len(q.Scope.ClientIDs) == 0 && len(q.Scope.Networks) == 0 {
		return out, nil
	}
	if clientsOutsideScope(q) {
		return out, nil
	}
	if longWindow(q) {
		return out, t.outcomesLong(ctx, q, &out)
	}
	nets := parseNetworks(q.Scope)
	where, args, destLike := buildEventWhere(q)
	where = append(where, "e.failed=1")
	query := `SELECT CASE
 WHEN e.result LIKE '%DENIED%' AND e.http_status=407 THEN 'd:proxy_auth'
 WHEN e.result LIKE '%DENIED%' THEN 'd:'||COALESCE(e.deny_reason,'other')
 WHEN e.http_status>=500 THEN 'e:5xx'
 WHEN e.http_status>=400 THEN 'e:4xx'
 ELSE 'e:network' END AS k,
 CASE WHEN e.client_id IS NULL THEN e.source_ip END, SUM(e.count)
 FROM event e`
	if destLike != "" {
		query += " JOIN host hd ON hd.id=e.host_id"
	}
	query += " WHERE " + strings.Join(where, " AND ")
	if destLike != "" {
		query += " AND lower(hd.name) LIKE ? ESCAPE '\\'"
		args = append(args, destLike)
	}
	query += " GROUP BY 1, 2"
	for _, key := range t.listMonths() {
		if key < q.From.UTC().Format("2006-01") || key > q.To.UTC().Format("2006-01") {
			continue
		}
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return out, e
		}
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			rows, e := r.QueryContext(ctx, query, args...)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var k string
				var unknownIP sql.NullString
				var n int64
				if e = rows.Scan(&k, &unknownIP, &n); e != nil {
					return e
				}
				// A manager sees unmatched sources only inside his networks.
				if q.Scope != nil && unknownIP.Valid && !sourceInNetworks(unknownIP.String, nets) {
					continue
				}
				if reason, ok := strings.CutPrefix(k, "d:"); ok {
					out.DenyReasons[reason] += n
				} else {
					out.SiteErrors[strings.TrimPrefix(k, "e:")] += n
				}
			}
			return rows.Err()
		})
		if e != nil {
			return out, e
		}
	}
	return out, nil
}
