package telemetry

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
	"time"
)

// DomainSource is one source address that requested a domain.
type DomainSource struct {
	ClientID         string // "" = unmatched (unknown) source
	SourceIP         string
	Requests, Denied int64
}

// DomainStat sums the requests to one host over a window. Sources counts the
// distinct source addresses; Top keeps the busiest of them.
type DomainStat struct {
	Host                    string
	Requests, Denied, Bytes int64
	Last                    time.Time
	Ports                   []int
	Sources                 int
	Top                     []DomainSource
}

// DomainReport is the Domains result: hosts busiest first (at most 500,
// Truncated when more) and totals over every matching request: by client
// ("" = unmatched sources) and the distinct unmatched source addresses.
type DomainReport struct {
	Items                   []DomainStat
	Truncated               bool
	Requests, Denied, Bytes int64
	Clients                 map[string]Counts
	UnmatchedSources        int
}

const (
	domainsMaxHosts   = 500
	domainsTopSources = 20
)

// Domains groups the requests of the [q.From, q.To) window by host with the
// same filters and scope as Events, busiest first: up to 500 hosts and the 20
// busiest sources of each, plus totals (see DomainReport). It reads raw monthly
// files, or journal_1h for windows longer than a day (longWindow), bounded like
// Facets. Unmatched sources under a manager scope are kept only inside his
// networks.
func (t *Store) Domains(ctx context.Context, q EventQuery) (DomainReport, error) {
	rep := DomainReport{Items: []DomainStat{}, Clients: map[string]Counts{}}
	if q.From.IsZero() || q.To.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > facetsMaxWindow {
		return rep, ErrFacetsWindow
	}
	if q.Scope != nil && len(q.Scope.ClientIDs) == 0 && len(q.Scope.Networks) == 0 {
		return rep, nil
	}
	if clientsOutsideScope(q) {
		return rep, nil
	}
	nets := parseNetworks(q.Scope)
	where, args, destLike := buildEventWhere(q)
	if destLike != "" {
		where = append(where, "lower(h.name) LIKE ? ESCAPE '\\'")
		args = append(args, destLike)
	}
	query := "SELECT h.name, COALESCE(e.port,0), COALESCE(e.client_id,''), e.source_ip, " +
		"SUM(e.count), SUM(CASE WHEN e.failed=1 THEN e.count ELSE 0 END), SUM(COALESCE(e.bytes,0)), MAX(e.ts) " +
		"FROM event e JOIN host h ON h.id=e.host_id WHERE " + strings.Join(where, " AND ") +
		" GROUP BY 1, 2, 3, 4"

	type sourceKey struct{ client, ip string }
	type acc struct {
		stat    DomainStat
		ports   map[int]bool
		sources map[sourceKey]*DomainSource
	}
	byHost := map[string]*acc{}
	unmatched := map[string]bool{}
	add := func(host string, port int, cid, ip string, requests, denied, bytes, last int64) {
		rep.Requests += requests
		rep.Denied += denied
		rep.Bytes += bytes
		c := rep.Clients[cid]
		c.Requests += requests
		c.Denied += denied
		c.Bytes += bytes
		rep.Clients[cid] = c
		if cid == "" {
			unmatched[ip] = true
		}
		a := byHost[host]
		if a == nil {
			a = &acc{stat: DomainStat{Host: host}, ports: map[int]bool{}, sources: map[sourceKey]*DomainSource{}}
			byHost[host] = a
		}
		a.stat.Requests += requests
		a.stat.Denied += denied
		a.stat.Bytes += bytes
		if at := time.UnixMilli(last).UTC(); at.After(a.stat.Last) {
			a.stat.Last = at
		}
		if port > 0 {
			a.ports[port] = true
		}
		k := sourceKey{cid, ip}
		src := a.sources[k]
		if src == nil {
			src = &DomainSource{ClientID: cid, SourceIP: ip}
			a.sources[k] = src
		}
		src.Requests += requests
		src.Denied += denied
	}
	if longWindow(q) {
		if e := t.journalRows(ctx, q, func(v journalRow) {
			denied := int64(0)
			if v.failed {
				denied = v.requests
			}
			add(v.host, v.port, v.client, v.ip, v.requests, denied, v.bytes, v.last)
		}); e != nil {
			return rep, e
		}
	} else {
		for _, key := range t.listMonths() {
			if key < q.From.UTC().Format("2006-01") || key > q.To.UTC().Format("2006-01") {
				continue
			}
			mf, e := t.monthFor(ctx, key)
			if e != nil {
				return rep, e
			}
			e = t.monthReader(mf, ctx, func(r *sql.DB) error {
				rows, e := r.QueryContext(ctx, query, args...)
				if e != nil {
					return e
				}
				defer rows.Close()
				for rows.Next() {
					var host, cid, ip string
					var port int
					var requests, denied, bytes, last int64
					if e = rows.Scan(&host, &port, &cid, &ip, &requests, &denied, &bytes, &last); e != nil {
						return e
					}
					// A manager sees unmatched sources only inside his networks.
					if q.Scope != nil && cid == "" && !sourceInNetworks(ip, nets) {
						continue
					}
					add(host, port, cid, ip, requests, denied, bytes, last)
				}
				return rows.Err()
			})
			if e != nil {
				return rep, e
			}
		}
	}
	rep.UnmatchedSources = len(unmatched)
	items := make([]DomainStat, 0, len(byHost))
	for _, a := range byHost {
		st := a.stat
		for p := range a.ports {
			st.Ports = append(st.Ports, p)
		}
		slices.Sort(st.Ports)
		ips := map[string]bool{}
		for _, src := range a.sources {
			ips[src.SourceIP] = true
			st.Top = append(st.Top, *src)
		}
		st.Sources = len(ips)
		sort.Slice(st.Top, func(i, j int) bool {
			if st.Top[i].Requests != st.Top[j].Requests {
				return st.Top[i].Requests > st.Top[j].Requests
			}
			return st.Top[i].SourceIP < st.Top[j].SourceIP
		})
		if len(st.Top) > domainsTopSources {
			st.Top = st.Top[:domainsTopSources]
		}
		items = append(items, st)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Requests != items[j].Requests {
			return items[i].Requests > items[j].Requests
		}
		if !items[i].Last.Equal(items[j].Last) {
			return items[i].Last.After(items[j].Last)
		}
		return items[i].Host < items[j].Host
	})
	if len(items) > domainsMaxHosts {
		items, rep.Truncated = items[:domainsMaxHosts], true
	}
	rep.Items = items
	return rep, nil
}
