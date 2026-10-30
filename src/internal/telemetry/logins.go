package telemetry

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// LoginSource is one source address that used a proxy login.
type LoginSource struct {
	SourceIP string
	Requests int64
	Failed   int64
	LastSeen time.Time
}

// LoginActivity summarises one proxy login over a window from raw events.
// Squid logs the supplied username on TCP_DENIED/407, so a wrong password is
// an event with http_status 407; Requests counts everything else (allowed and
// policy-denied), Denied the 403 subset.
type LoginActivity struct {
	Requests, Denied, Failed int64
	LastUsed                 *time.Time
	Sources                  []LoginSource
}

// maxLoginSources bounds the per-address list; the totals cover every address.
const maxLoginSources = 50

// ProxyLoginActivity aggregates events with proxy_username = username in
// [from, to) across the monthly files that overlap the window.
func (t *Store) ProxyLoginActivity(ctx context.Context, username string, from, to time.Time) (LoginActivity, error) {
	out := LoginActivity{Sources: []LoginSource{}}
	bySource := map[string]*LoginSource{}
	var lastUsed int64
	first := from.UTC().Format("2006-01")
	for _, key := range t.listMonths() {
		if key < first || key > to.UTC().Format("2006-01") {
			continue
		}
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return out, e
		}
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			rows, e := r.QueryContext(ctx, `SELECT source_ip,
 SUM(CASE WHEN http_status=407 THEN 0 ELSE count END),
 SUM(CASE WHEN http_status=403 THEN count ELSE 0 END),
 SUM(CASE WHEN http_status=407 THEN count ELSE 0 END),
 MAX(ts), MAX(CASE WHEN http_status=407 THEN NULL ELSE ts END)
 FROM event WHERE ts>=? AND ts<? AND proxy_username=? GROUP BY source_ip`, from.UnixMilli(), to.UnixMilli(), username)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var ip string
				var req, denied, failed, seen int64
				var used sql.NullInt64
				if e = rows.Scan(&ip, &req, &denied, &failed, &seen, &used); e != nil {
					return e
				}
				out.Requests += req
				out.Denied += denied
				out.Failed += failed
				if used.Valid && used.Int64 > lastUsed {
					lastUsed = used.Int64
				}
				src := bySource[ip]
				if src == nil {
					src = &LoginSource{SourceIP: ip}
					bySource[ip] = src
				}
				src.Requests += req
				src.Failed += failed
				if t := time.UnixMilli(seen).UTC(); t.After(src.LastSeen) {
					src.LastSeen = t
				}
			}
			return rows.Err()
		})
		if e != nil {
			return out, e
		}
	}
	if lastUsed > 0 {
		t := time.UnixMilli(lastUsed).UTC()
		out.LastUsed = &t
	}
	for _, src := range bySource {
		out.Sources = append(out.Sources, *src)
	}
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].LastSeen.After(out.Sources[j].LastSeen) })
	if len(out.Sources) > maxLoginSources {
		out.Sources = out.Sources[:maxLoginSources]
	}
	return out, nil
}
