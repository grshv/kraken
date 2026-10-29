package telemetry

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
)

// Maintain applies retention and housekeeping: deletes monthly files past
// RawRetention or exceeding RawBudgetBytes (oldest first, never the current
// month), trims hourly/daily aggregates by their retentions in 5000-row
// chunks, runs a WAL checkpoint and PRAGMA optimize.
func (t *Store) Maintain(ctx context.Context) error {
	if e := t.retainMonthlyFiles(ctx); e != nil {
		return e
	}
	if e := t.trimAggregates(ctx); e != nil {
		return e
	}
	if e := t.checkpointAndOptimize(ctx); e != nil {
		return e
	}
	return nil
}

// retainMonthlyFiles removes files whose month-end is older than RawRetention
// and then the oldest beyond RawBudgetBytes; the current month is never deleted.
func (t *Store) retainMonthlyFiles(ctx context.Context) error {
	now := t.now().UTC()
	current := monthKey(now)
	months := t.listMonths()
	type fi struct {
		key  string
		size int64
	}
	var files []fi
	for _, key := range months {
		if key == current {
			continue
		}
		p := t.monthPath(key)
		st, e := os.Stat(p)
		if e != nil {
			continue
		}
		files = append(files, fi{key: key, size: st.Size()})
	}
	// Age retention: delete months whose last instant is older than RawRetention.
	retentionCutoff := now.Add(-t.opts.RawRetention)
	for _, f := range files {
		end, ok := monthEndOf(f.key)
		if !ok {
			continue
		}
		if !end.After(retentionCutoff) {
			t.deleteMonth(f.key)
		}
	}
	// Budget retention: oldest beyond the budget.
	var kept []fi
	for _, f := range files {
		if !t.monthDeleted(f.key) {
			kept = append(kept, f)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].key < kept[j].key })
	var total int64
	for _, f := range kept {
		total += f.size
	}
	for _, f := range kept {
		if total <= t.opts.RawBudgetBytes {
			break
		}
		total -= f.size
		t.deleteMonth(f.key)
	}
	return nil
}

func (t *Store) monthDeleted(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.months[key]; ok {
		return false
	}
	_, e := os.Stat(t.monthPath(key))
	return e != nil
}

func (t *Store) deleteMonth(key string) {
	t.mu.Lock()
	if mf, ok := t.months[key]; ok {
		_ = mf.close()
		delete(t.months, key)
	}
	t.mu.Unlock()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(t.monthPath(key) + suffix)
	}
}

// trimAggregates deletes expired aggregate rows in 5000-row chunks. Hourly
// tables use HourlyRetention; daily tables use RollupRetention.
func (t *Store) trimAggregates(ctx context.Context) error {
	nowSecs := t.now().Unix()
	hourlyCut := nowSecs - int64(t.opts.HourlyRetention.Seconds())
	rollCut := nowSecs - int64(t.opts.RollupRetention.Seconds())
	for _, spec := range []struct {
		table string
		col   string
		cut   int64
		pk    []string
	}{
		{"client_host_1h", "bucket", hourlyCut, []string{"bucket", "client_id", "host"}},
		{"unknown_host_1h", "bucket", hourlyCut, []string{"bucket", "source_ip", "host"}},
		{"client_host_1d", "day", rollCut, []string{"day", "client_id", "host"}},
		{"unknown_host_1d", "day", rollCut, []string{"day", "source_ip", "host"}},
		{"client_15m", "bucket", rollCut, []string{"bucket", "client_id"}},
		{"unknown_15m", "bucket", rollCut, []string{"bucket", "source_ip"}},
		{"facet_15m", "bucket", hourlyCut, []string{"bucket", "client_id", "source_ip", "method", "proxy_result", "failed"}},
	} {
		if e := t.trimTable(ctx, spec.table, spec.col, spec.cut, spec.pk...); e != nil {
			return e
		}
	}
	return nil
}

// trimTable deletes rows with col<cut from a WITHOUT ROWID aggregate table.
// Chunking via the composite primary key avoids a long lock on large tables;
// each table passes its PK columns.
func (t *Store) trimTable(ctx context.Context, table, col string, cut int64, pk ...string) error {
	if len(pk) == 0 {
		// Fallback: single-statement delete (used only if a table is added
		// without a registered PK below).
		_, e := t.rollWriter.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+col+"<?")
		return e
	}
	for {
		// Build "(pk1,pk2,...) IN (SELECT pk1,pk2,... FROM tbl WHERE col<? LIMIT 5000)".
		colList := strings.Join(pk, ",")
		var n int64
		if e := t.rollWriter.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM (SELECT "+colList+" FROM "+table+" WHERE "+col+"<? LIMIT 5000)", cut).Scan(&n); e != nil {
			if e == sql.ErrNoRows {
				return nil
			}
			return e
		}
		if n == 0 {
			return nil
		}
		if _, e := t.rollWriter.ExecContext(ctx,
			"DELETE FROM "+table+" WHERE ("+colList+") IN (SELECT "+colList+" FROM "+table+" WHERE "+col+"<? LIMIT 5000)", cut); e != nil {
			return e
		}
		if n < 5000 {
			return nil
		}
	}
}

func (t *Store) checkpointAndOptimize(ctx context.Context) error {
	if _, e := t.rollWriter.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)"); e != nil {
		return e
	}
	if _, e := t.rollWriter.ExecContext(ctx, "PRAGMA optimize"); e != nil {
		return e
	}
	return nil
}
