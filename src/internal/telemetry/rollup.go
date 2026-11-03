package telemetry

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"time"

	"kraken.local/kraken/internal/events"
)

// maxHostsPerBucket caps distinct hosts per (bucket, client); overflow collapses
// into the synthetic host "(other)".
const maxHostsPerBucket = 500

// Rollup folds events with id > month watermark into rollups.db aggregates and
// advances the watermark. Idempotent: a crash between the aggregate write and
// the watermark advance leaves the same events to be re-rolled next time. The
// return value is the number of events folded.
func (t *Store) Rollup(ctx context.Context) (int, error) {
	total := 0
	for _, key := range t.listMonths() {
		n, e := t.rollupMonth(ctx, key)
		if e != nil {
			return total, e
		}
		if _, e := t.rollupJournal(ctx, key); e != nil {
			return total, e
		}
		total += n
	}
	return total, nil
}

// rollupMonth folds one month's unrolled events.
func (t *Store) rollupMonth(ctx context.Context, key string) (int, error) {
	mf, e := t.monthFor(ctx, key)
	if e != nil {
		return 0, e
	}
	var last int64
	if e := t.rollReader(ctx, func(r *sql.DB) error {
		return r.QueryRowContext(ctx, "SELECT COALESCE((SELECT last_event_id FROM watermark WHERE month=?),0)", key).Scan(&last)
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
	// Read unrolled rows in id order and aggregate in Go.
	type hostCounts struct {
		hosts map[string]*Counts
	}
	c15m := map[int64]map[string]*Counts{} // bucket -> client -> counts
	u15m := map[int64]map[string]*Counts{} // bucket -> source_ip -> counts
	ch1h := map[int64]map[string]map[string]*Counts{}
	ch1d := map[int64]map[string]map[string]*Counts{}
	uh1h := map[int64]map[string]map[string]*Counts{}
	uh1d := map[int64]map[string]map[string]*Counts{}
	uh1hSeen := map[int64]map[string]map[string]int64{}
	uh1dSeen := map[int64]map[string]map[string]int64{}
	counted := 0
	e = t.monthReader(mf, ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, `SELECT e.id,e.ts,e.client_id,e.source_ip,h.name,e.bytes,e.failed,e.count FROM event e JOIN host h ON h.id=e.host_id WHERE e.id>? ORDER BY e.id`, last)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, ts int64
			var clientIDNS sql.NullString
			var sourceIP, hostName string
			var bytes int64
			var failed int
			var count int64
			if e = rows.Scan(&id, &ts, &clientIDNS, &sourceIP, &hostName, &bytes, &failed, &count); e != nil {
				return e
			}
			last = id
			counted++
			if count <= 0 {
				count = 1
			}
			clientID := ""
			if clientIDNS.Valid {
				clientID = clientIDNS.String
			}
			b15 := floorBucket(ts, 15*time.Minute)
			h1 := floorBucket(ts, time.Hour)
			d1 := floorDay(ts)
			if clientID != "" {
				addCounts(c15m, b15, clientID, "", hostName, bytes, failed, count)
				addCountsHost(ch1h, h1, clientID, hostName, bytes, failed, count)
				addCountsHost(ch1d, d1, clientID, hostName, bytes, failed, count)
			} else {
				addCountsHost(uh1h, h1, sourceIP, hostName, bytes, failed, count)
				addCounts(u15m, b15, sourceIP, "", "", bytes, failed, count)
				addCountsHost(uh1d, d1, sourceIP, hostName, bytes, failed, count)
				recordSeen(uh1hSeen, h1, sourceIP, hostName, ts)
				recordSeen(uh1dSeen, d1, sourceIP, hostName, ts)
			}
		}
		return rows.Err()
	})
	if e != nil {
		return 0, e
	}
	if counted == 0 {
		return 0, nil
	}
	// Apply cardinality cap for client×host tables: keep top 499 hosts per
	// (bucket, client), collapse the rest into "(other)".
	capHosts(ch1h)
	capHosts(ch1d)
	capHosts(uh1h)
	capHosts(uh1d)
	// Write aggregates in one transaction.
	tx, e := t.rollWriter.BeginTx(ctx, nil)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if e := write15m(ctx, tx, c15m, "client_15m", "client_id"); e != nil {
		return 0, e
	}
	if e := write15m(ctx, tx, u15m, "unknown_15m", "source_ip"); e != nil {
		return 0, e
	}
	if e := writeHostTable(ctx, tx, ch1h, "client_host_1h", "bucket", "client_id", false, nil); e != nil {
		return 0, e
	}
	if e := writeHostTable(ctx, tx, ch1d, "client_host_1d", "day", "client_id", false, nil); e != nil {
		return 0, e
	}
	if e := writeHostTable(ctx, tx, uh1h, "unknown_host_1h", "bucket", "source_ip", true, uh1hSeen); e != nil {
		return 0, e
	}
	if e := writeHostTable(ctx, tx, uh1d, "unknown_host_1d", "day", "source_ip", true, uh1dSeen); e != nil {
		return 0, e
	}
	if _, e := tx.ExecContext(ctx, "INSERT INTO watermark(month,last_event_id) VALUES(?,?) ON CONFLICT(month) DO UPDATE SET last_event_id=excluded.last_event_id", key, last); e != nil {
		return 0, e
	}
	if e := tx.Commit(); e != nil {
		return 0, e
	}
	return counted, nil
}

func addCounts(m map[int64]map[string]*Counts, bucket int64, client, _, host string, bytes int64, failed int, count int64) {
	c, ok := m[bucket]
	if !ok {
		c = map[string]*Counts{}
		m[bucket] = c
	}
	cc, ok := c[client]
	if !ok {
		cc = &Counts{}
		c[client] = cc
	}
	cc.Requests += count
	cc.Bytes += bytes
	if failed != 0 {
		cc.Denied += count
	}
}

func addCountsHost(m map[int64]map[string]map[string]*Counts, bucket int64, key, host string, bytes int64, failed int, count int64) {
	c, ok := m[bucket]
	if !ok {
		c = map[string]map[string]*Counts{}
		m[bucket] = c
	}
	hm, ok := c[key]
	if !ok {
		hm = map[string]*Counts{}
		c[key] = hm
	}
	hc, ok := hm[host]
	if !ok {
		hc = &Counts{}
		hm[host] = hc
	}
	hc.Requests += count
	hc.Bytes += bytes
	if failed != 0 {
		hc.Denied += count
	}
}

func recordSeen(m map[int64]map[string]map[string]int64, bucket int64, key, host string, ts int64) {
	c, ok := m[bucket]
	if !ok {
		c = map[string]map[string]int64{}
		m[bucket] = c
	}
	hm, ok := c[key]
	if !ok {
		hm = map[string]int64{}
		c[key] = hm
	}
	if cur, ok := hm[host]; !ok || ts > cur {
		hm[host] = ts
	}
}

// capHosts keeps the top (maxHostsPerBucket-1) hosts per (bucket, key) and
// folds the remainder into "(other)".
func capHosts(m map[int64]map[string]map[string]*Counts) {
	for _, keys := range m {
		for key, hm := range keys {
			if len(hm) <= maxHostsPerBucket {
				continue
			}
			list := make([]*hostEntry, 0, len(hm))
			for name, c := range hm {
				list = append(list, &hostEntry{name, c})
			}
			sort.Slice(list, func(i, j int) bool {
				if list[i].c.Requests != list[j].c.Requests {
					return list[i].c.Requests > list[j].c.Requests
				}
				return list[i].name < list[j].name
			})
			keep := list[:maxHostsPerBucket-1]
			other := &Counts{}
			newHM := map[string]*Counts{}
			for _, h := range keep {
				newHM[h.name] = h.c
			}
			for _, h := range list[maxHostsPerBucket-1:] {
				other.Requests += h.c.Requests
				other.Denied += h.c.Denied
				other.Bytes += h.c.Bytes
			}
			newHM["(other)"] = other
			keys[key] = newHM
		}
	}
}

type hostEntry struct {
	name string
	c    *Counts
}

// write15m writes a 15-minute aggregate table (client_15m keyed by client_id,
// or unknown_15m keyed by source_ip) with UPSERT accumulation.
func write15m(ctx context.Context, tx *sql.Tx, m map[int64]map[string]*Counts, table, keyCol string) error {
	stmt, e := tx.PrepareContext(ctx, fmt.Sprintf("INSERT INTO %s(bucket,%s,requests,denied,bytes) VALUES(?,?,?,?,?) ON CONFLICT(bucket,%s) DO UPDATE SET requests=requests+excluded.requests,denied=denied+excluded.denied,bytes=bytes+excluded.bytes", table, keyCol, keyCol))
	if e != nil {
		return e
	}
	defer stmt.Close()
	buckets := make([]int64, 0, len(m))
	for b := range m {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	for _, b := range buckets {
		clients := m[b]
		ids := make([]string, 0, len(clients))
		for id := range clients {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := clients[id]
			if _, e := stmt.ExecContext(ctx, b, id, c.Requests, c.Denied, c.Bytes); e != nil {
				return e
			}
		}
	}
	return nil
}

func writeHostTable(ctx context.Context, tx *sql.Tx, m map[int64]map[string]map[string]*Counts, table, col, keyCol string, withSeen bool, seen ...map[int64]map[string]map[string]int64) error {
	var stmt *sql.Stmt
	var e error
	if withSeen {
		stmt, e = tx.PrepareContext(ctx, fmt.Sprintf("INSERT INTO %s(%s,%s,host,requests,denied,bytes,last_seen) VALUES(?,?,?,?,?,?,?) ON CONFLICT(%s,%s,host) DO UPDATE SET requests=requests+excluded.requests,denied=denied+excluded.denied,bytes=bytes+excluded.bytes,last_seen=excluded.last_seen", table, col, keyCol, col, keyCol))
	} else {
		stmt, e = tx.PrepareContext(ctx, fmt.Sprintf("INSERT INTO %s(%s,%s,host,requests,denied,bytes) VALUES(?,?,?,?,?,?) ON CONFLICT(%s,%s,host) DO UPDATE SET requests=requests+excluded.requests,denied=denied+excluded.denied,bytes=bytes+excluded.bytes", table, col, keyCol, col, keyCol))
	}
	if e != nil {
		return e
	}
	defer stmt.Close()
	buckets := make([]int64, 0, len(m))
	for b := range m {
		buckets = append(buckets, b)
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	var seenMap map[int64]map[string]map[string]int64
	if withSeen && len(seen) > 0 {
		seenMap = seen[0]
	}
	for _, b := range buckets {
		keys := m[b]
		ids := make([]string, 0, len(keys))
		for id := range keys {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			hm := keys[id]
			hosts := make([]string, 0, len(hm))
			for h := range hm {
				hosts = append(hosts, h)
			}
			sort.Strings(hosts)
			for _, h := range hosts {
				c := hm[h]
				if withSeen {
					var ls int64
					if seenMap != nil {
						ls = seenMap[b][id][h]
					}
					if _, e := stmt.ExecContext(ctx, b, id, h, c.Requests, c.Denied, c.Bytes, ls); e != nil {
						return e
					}
				} else {
					if _, e := stmt.ExecContext(ctx, b, id, h, c.Requests, c.Denied, c.Bytes); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}

// floorBucket aligns a unix-ms timestamp down to the given duration boundary
// and returns the boundary as unix seconds.
func floorBucket(ms int64, step time.Duration) int64 {
	secs := int64(step.Seconds())
	if secs <= 0 {
		return ms / 1000
	}
	return ((ms / 1000) / secs) * secs
}

// floorDay aligns a unix-ms timestamp down to the UTC day boundary (seconds).
func floorDay(ms int64) int64 {
	return (ms / 86400000) * 86400
}

// rebuildRollups recreates all aggregates from every retained monthly file.
// Used when rollups.db was lost or recreated.
func (t *Store) rebuildRollups() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Reset watermark so every month is re-rolled from id 0.
	if _, e := t.rollWriter.ExecContext(ctx, "DELETE FROM watermark"); e != nil {
		return e
	}
	if _, e := t.rollWriter.ExecContext(ctx, "DELETE FROM journal_watermark"); e != nil {
		return e
	}
	for _, key := range t.listMonths() {
		if _, e := t.rollupMonth(ctx, key); e != nil {
			return e
		}
		if _, e := t.rollupJournal(ctx, key); e != nil {
			return e
		}
	}
	return nil
}

// rollupRebuildForTest forces a full rebuild (used by tests).
func (t *Store) rollupRebuildForTest(ctx context.Context) error {
	if _, e := t.rollWriter.ExecContext(ctx, "DELETE FROM watermark"); e != nil {
		return e
	}
	if _, e := t.rollWriter.ExecContext(ctx, "DELETE FROM journal_watermark"); e != nil {
		return e
	}
	for _, key := range t.listMonths() {
		if _, e := t.rollupMonth(ctx, key); e != nil {
			return e
		}
		if _, e := t.rollupJournal(ctx, key); e != nil {
			return e
		}
	}
	return nil
}

var _ = events.Event{}
var _ = strconv.Itoa
