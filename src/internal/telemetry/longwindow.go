package telemetry

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Journal windows longer than a day read journal_1h instead of scanning raw
// monthly files: one row per hour, client ('' = unmatched source), source
// address, host, port, method, failed flag, deny reason and outcome key, with
// requests, bytes and the last event time. Every journal filter maps onto these
// columns except the exact Squid result, which keeps the raw path.

// outcomeSQL classifies a failed event the way Outcomes reports it: d:<deny
// reason> (d:proxy_auth for 407, d:other without a reason) for proxy denials,
// e:5xx / e:4xx / e:network for site errors, ” when the request did not fail.
const outcomeSQL = `CASE
 WHEN e.failed=0 THEN ''
 WHEN e.result LIKE '%DENIED%' AND e.http_status=407 THEN 'd:proxy_auth'
 WHEN e.result LIKE '%DENIED%' THEN 'd:'||COALESCE(e.deny_reason,'other')
 WHEN e.http_status>=500 THEN 'e:5xx'
 WHEN e.http_status>=400 THEN 'e:4xx'
 ELSE 'e:network' END`

const hourMS = int64(time.Hour / time.Millisecond)

// journalRetention keeps journal_1h for the longest journal window.
const journalRetention = 32 * 24 * time.Hour

// rollupJournal folds one month's events past its journal watermark into
// journal_1h in one transaction with the watermark, so a crash re-rolls the
// same events. A month never rolled (a new table) backfills from its start.
func (t *Store) rollupJournal(ctx context.Context, key string) (int64, error) {
	mf, e := t.monthFor(ctx, key)
	if e != nil {
		return 0, e
	}
	var last int64
	if e := t.rollReader(ctx, func(r *sql.DB) error {
		return r.QueryRowContext(ctx, "SELECT COALESCE((SELECT last_event_id FROM journal_watermark WHERE month=?),0)", key).Scan(&last)
	}); e != nil {
		return 0, e
	}
	var maxID int64
	if e := t.monthReader(mf, ctx, func(r *sql.DB) error {
		return r.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM event").Scan(&maxID)
	}); e != nil {
		return 0, e
	}
	if maxID <= last {
		return 0, nil
	}
	type row struct {
		bucket                  int64
		client, ip, host        string
		port                    int64
		method                  string
		failed                  int
		reason, outcome         string
		requests, bytes, lastTS int64
	}
	var rows []row
	query := "SELECT (e.ts/?)*?/1000, COALESCE(e.client_id,''), e.source_ip, h.name, COALESCE(e.port,0), COALESCE(e.method,''), e.failed, " +
		"COALESCE(e.deny_reason,''), " + outcomeSQL + ", SUM(MAX(e.count,1)), SUM(COALESCE(e.bytes,0)), MAX(e.ts) " +
		"FROM event e JOIN host h ON h.id=e.host_id WHERE e.id>? AND e.id<=? GROUP BY 1,2,3,4,5,6,7,8,9"
	if e := t.monthReader(mf, ctx, func(r *sql.DB) error {
		rs, e := r.QueryContext(ctx, query, hourMS, hourMS, last, maxID)
		if e != nil {
			return e
		}
		defer rs.Close()
		for rs.Next() {
			var v row
			if e := rs.Scan(&v.bucket, &v.client, &v.ip, &v.host, &v.port, &v.method, &v.failed, &v.reason, &v.outcome, &v.requests, &v.bytes, &v.lastTS); e != nil {
				return e
			}
			rows = append(rows, v)
		}
		return rs.Err()
	}); e != nil {
		return 0, e
	}
	tx, e := t.rollWriter.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	stmt, e := tx.PrepareContext(ctx, "INSERT INTO journal_1h(bucket,client_id,source_ip,host,port,method,failed,deny_reason,outcome,requests,bytes,last_ts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) "+
		"ON CONFLICT(bucket,client_id,source_ip,host,port,method,failed,deny_reason,outcome) DO UPDATE SET requests=requests+excluded.requests,bytes=bytes+excluded.bytes,last_ts=MAX(last_ts,excluded.last_ts)")
	if e != nil {
		return 0, e
	}
	defer stmt.Close()
	for _, v := range rows {
		if _, e := stmt.ExecContext(ctx, v.bucket, v.client, v.ip, v.host, v.port, v.method, v.failed, v.reason, v.outcome, v.requests, v.bytes, v.lastTS); e != nil {
			return 0, e
		}
	}
	if _, e := tx.ExecContext(ctx, "INSERT INTO journal_watermark(month,last_event_id) VALUES(?,?) ON CONFLICT(month) DO UPDATE SET last_event_id=excluded.last_event_id", key, maxID); e != nil {
		return 0, e
	}
	if e := tx.Commit(); e != nil {
		return 0, e
	}
	return maxID - last, nil
}

// longWindow reports whether a query is answered from journal_1h: longer than
// a day, both ends on whole hours and no exact Squid result filter.
func longWindow(q EventQuery) bool {
	from, to := q.From.UnixMilli(), q.To.UnixMilli()
	return q.To.Sub(q.From) > 24*time.Hour && from%hourMS == 0 && to%hourMS == 0 && q.Result == ""
}

type journalRow struct {
	bucket           int64 // hour start, unix seconds
	client, ip, host string
	port             int
	method           string
	failed           bool
	reason, outcome  string
	requests, bytes  int64
	last             int64 // unix ms
}

// journalRows calls fn for every journal_1h row of the window matching q's
// filters and scope. Unmatched sources under a manager scope are kept only
// inside his networks (checked in Go: SQLite has no CIDR match).
func (t *Store) journalRows(ctx context.Context, q EventQuery, fn func(journalRow)) error {
	if q.Scope != nil && len(q.Scope.ClientIDs) == 0 && len(q.Scope.Networks) == 0 {
		return nil
	}
	if clientsOutsideScope(q) {
		return nil
	}
	where := []string{"bucket>=?", "bucket<?"}
	args := []any{q.From.Unix(), q.To.Unix()}
	in := func(col string, values []string) {
		where = append(where, col+" IN ("+placeholders(len(values))+")")
		for _, v := range values {
			args = append(args, v)
		}
	}
	if q.Scope != nil {
		if len(q.Scope.ClientIDs) == 0 {
			where = append(where, "client_id=''")
		} else {
			in("(client_id", q.Scope.ClientIDs)
			where[len(where)-1] += " OR client_id='')"
		}
	}
	if len(q.ClientIDs) > 0 {
		in("client_id", q.ClientIDs)
	}
	if len(q.Methods) > 0 {
		in("method", q.Methods)
	}
	if len(q.DenyReasons) > 0 {
		in("deny_reason", q.DenyReasons)
	}
	if q.SourceIP != "" {
		where = append(where, "source_ip=?")
		args = append(args, q.SourceIP)
	}
	if q.Failures {
		where = append(where, "failed=1")
	}
	if q.Allowed {
		where = append(where, "failed=0")
	}
	if q.Destination != "" {
		where = append(where, "lower(host) LIKE ? ESCAPE '\\'")
		args = append(args, "%"+strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(q.Destination), "%", "\\%"), "_", "\\_")+"%")
	}
	nets := parseNetworks(q.Scope)
	query := "SELECT bucket,client_id,source_ip,host,port,method,failed,deny_reason,outcome,requests,bytes,last_ts FROM journal_1h WHERE " + strings.Join(where, " AND ")
	return t.rollReader(ctx, func(r *sql.DB) error {
		rs, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rs.Close()
		for rs.Next() {
			var v journalRow
			var failed int
			if e := rs.Scan(&v.bucket, &v.client, &v.ip, &v.host, &v.port, &v.method, &failed, &v.reason, &v.outcome, &v.requests, &v.bytes, &v.last); e != nil {
				return e
			}
			if q.Scope != nil && v.client == "" && !sourceInNetworks(v.ip, nets) {
				continue
			}
			v.failed = failed != 0
			fn(v)
		}
		return rs.Err()
	})
}

// facetsLong fills facets from journal_1h; step is a whole number of hours.
func (t *Store) facetsLong(ctx context.Context, q EventQuery, step time.Duration, f *Facets) error {
	stepSecs := int64(step.Seconds())
	from := q.From.Unix()
	return t.journalRows(ctx, q, func(v journalRow) {
		allowed, denied := v.requests, int64(0)
		if v.failed {
			allowed, denied = 0, v.requests
		}
		f.Allowed += allowed
		f.Denied += denied
		f.Clients[v.client] += v.requests
		f.Methods[v.method] += v.requests
		if i := (v.bucket - from) / stepSecs; i >= 0 && i < int64(len(f.Histogram)) {
			f.Histogram[i].Allowed += allowed
			f.Histogram[i].Denied += denied
		}
	})
}

// outcomesLong fills outcomes from journal_1h.
func (t *Store) outcomesLong(ctx context.Context, q EventQuery, out *Outcomes) error {
	return t.journalRows(ctx, q, func(v journalRow) {
		if reason, ok := strings.CutPrefix(v.outcome, "d:"); ok {
			out.DenyReasons[reason] += v.requests
		} else if class, ok := strings.CutPrefix(v.outcome, "e:"); ok {
			out.SiteErrors[class] += v.requests
		}
	})
}
