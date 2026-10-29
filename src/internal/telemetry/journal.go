package telemetry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
)

// ErrCursor signals an invalid cursor or one bound to different filters/scope.
var ErrCursor = errors.New("invalid cursor or changed filters")

type journalCursor struct {
	Month  string `json:"m"`
	Rowid  int64  `json:"r"`
	Filter string `json:"f"`
	Scope  string `json:"s"`
}

// fingerprint returns a stable hash of the filter and scope so a cursor built
// for one query is rejected under another.
func cursorFingerprint(q EventQuery) string {
	type wire struct {
		SourceIP, Destination, Method, Result, ClientID string
		From, To                                        int64
		Failures                                        bool
	}
	w := wire{q.SourceIP, q.Destination, q.Method, q.Result, q.ClientID, q.From.UnixNano(), q.To.UnixNano(), q.Failures}
	b, _ := json.Marshal(w)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func scopeFingerprint(s *Scope) string {
	if s == nil {
		return ""
	}
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func encodeCursor(c journalCursor) *string {
	b, _ := json.Marshal(c)
	v := base64.RawURLEncoding.EncodeToString(b)
	return &v
}

func decodeCursor(raw string) (journalCursor, error) {
	c := journalCursor{Rowid: 1<<63 - 1}
	if raw == "" {
		return c, nil
	}
	if len(raw) > 512 {
		return c, ErrCursor
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return c, ErrCursor
	}
	if e := json.Unmarshal(b, &c); e != nil || c.Rowid <= 0 {
		return c, ErrCursor
	}
	return c, nil
}

// Events pages the journal newest-to-oldest across all retained monthly files.
// The cursor is bound to the filters and scope; reusing it with different
// values returns ErrCursor.
func (t *Store) Events(ctx context.Context, q EventQuery) (Page, error) {
	p := Page{Items: []events.Event{}}
	if q.Limit < 1 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	fHash := cursorFingerprint(q)
	sHash := scopeFingerprint(q.Scope)
	cur, e := decodeCursor(q.Cursor)
	if e != nil {
		return p, e
	}
	if q.Cursor != "" && (cur.Filter != fHash || cur.Scope != sHash) {
		return p, ErrCursor
	}
	months := t.listMonths()
	// Start at the cursor's month if set, else the newest.
	startIdx := len(months) - 1
	if cur.Month != "" {
		startIdx = -1
		for i, m := range months {
			if m == cur.Month {
				startIdx = i
				break
			}
		}
		if startIdx == -1 {
			// Cursor month was removed by retention: signal a gap and start
			// from the newest available.
			p.Gap = true
			startIdx = len(months) - 1
			cur.Rowid = 1<<63 - 1
		}
	}
	nets := parseNetworks(q.Scope)
	clientSet := map[string]bool{}
	if q.Scope != nil {
		for _, id := range q.Scope.ClientIDs {
			clientSet[id] = true
		}
	}
	remaining := q.Limit
	for i := startIdx; i >= 0 && remaining > 0; i-- {
		key := months[i]
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return p, e
		}
		upper := cur.Rowid
		if i != startIdx {
			upper = 1<<63 - 1
		}
		fetched, last, more, e := t.queryMonthEvents(ctx, mf, key, q, upper, remaining+1, nets, clientSet)
		if e != nil {
			return p, e
		}
		for _, ev := range fetched {
			if len(p.Items) >= q.Limit {
				break
			}
			p.Items = append(p.Items, ev)
			remaining--
			if remaining == 0 {
				break
			}
		}
		if len(p.Items) >= q.Limit && more {
			c := journalCursor{Month: key, Rowid: last, Filter: fHash, Scope: sHash}
			p.NextCursor = encodeCursor(c)
			return p, nil
		}
		if more {
			// Page filled within this month; cursor stays in this month.
			c := journalCursor{Month: key, Rowid: last, Filter: fHash, Scope: sHash}
			p.NextCursor = encodeCursor(c)
			return p, nil
		}
	}
	// If older months are gone, signal a gap.
	if startIdx > 0 && hasOlderMissing(t, months) {
		p.Gap = true
	}
	return p, nil
}

// hasOlderMissing reports whether retention removed months older than the
// oldest retained one (so the journal cannot reach the true beginning).
func hasOlderMissing(t *Store, months []string) bool {
	// Heuristic: if any month file was deleted by retention, we cannot know
	// here without history. Signal gap only when the oldest retained month's
	// first event is not id=1 (i.e. the file itself is complete but earlier
	// files are gone). We approximate: gap if there is at least one retained
	// month and it is not the very first month ever — but we have no origin.
	// Keep it simple: never assert a gap from this path; Page.Gap is set when
	// the cursor's own month vanished, which is the concrete signal.
	_ = t
	_ = months
	return false
}

// buildEventWhere builds the WHERE conditions shared by Events (journal) and
// Facets, applying scope and the EventQuery scalar filters: client_id,
// failures_only, source_ip, method, result and the [From, To) ts range.
// Cursor paging (e.id < upper) and LIMIT are NOT included so Facets can reuse
// the same filter without paging. The destination substring LIKE value is
// returned separately so callers append the host JOIN only when needed.
func buildEventWhere(q EventQuery) (where []string, args []any, destLike string) {
	where = []string{}
	if q.Scope != nil {
		if len(q.Scope.ClientIDs) == 0 {
			where = append(where, "e.client_id IS NULL")
		} else {
			ph := placeholders(len(q.Scope.ClientIDs))
			where = append(where, "(e.client_id IN ("+ph+") OR e.client_id IS NULL)")
			for _, id := range q.Scope.ClientIDs {
				args = append(args, id)
			}
		}
	}
	if q.ClientID != "" {
		where = append(where, "e.client_id=?")
		args = append(args, q.ClientID)
	}
	if q.Failures {
		where = append(where, "e.failed=1")
	}
	if q.SourceIP != "" {
		where = append(where, "e.source_ip=?")
		args = append(args, q.SourceIP)
	}
	if q.Method != "" {
		where = append(where, "e.method=?")
		args = append(args, q.Method)
	}
	if q.Result != "" {
		where = append(where, "e.result=?")
		args = append(args, q.Result)
	}
	if !q.From.IsZero() {
		where = append(where, "e.ts>=?")
		args = append(args, q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		where = append(where, "e.ts<?")
		args = append(args, q.To.UnixMilli())
	}
	if q.Destination != "" {
		// Match host name case-insensitively as a literal substring; escape
		// SQL wildcards so "%" is not a pattern.
		destLike = "%" + strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(q.Destination), "%", "\\%"), "_", "\\_") + "%"
	}
	return where, args, destLike
}

// queryMonthEvents reads up to `cap` events from one month (id < upper),
// applying scope network filtering for unmatched sources in Go. Returns the
// fetched events, the rowid of the last scanned row, and whether more rows
// remain in this month beyond the returned set.
func (t *Store) queryMonthEvents(ctx context.Context, mf *monthFile, key string, q EventQuery, upper int64, cap int, nets []netip.Prefix, clientSet map[string]bool) ([]events.Event, int64, bool, error) {
	where, args, destLike := buildEventWhere(q)
	where = append(where, "e.id<?")
	args = append(args, upper)
	args = append(args, cap)
	query := "SELECT " + eventColumns + " FROM event e JOIN host h ON h.id=e.host_id"
	if destLike != "" {
		query += " JOIN host hd ON hd.id=e.host_id"
	}
	query += " WHERE " + strings.Join(where, " AND ")
	if destLike != "" {
		query += " AND lower(hd.name) LIKE ? ESCAPE '\\'"
		// insert destLike before cap arg
		args = append(args[:len(args)-1], destLike, cap)
	}
	query += " ORDER BY e.id DESC LIMIT ?"
	var out []events.Event
	var last int64
	more := false
	e := t.monthReader(mf, ctx, func(r *sql.DB) error {
		rows, e := r.QueryContext(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			ev, e := scanEventRow(key, rows.Scan)
			if e != nil {
				return e
			}
			// Scope filter unmatched (NULL client) sources by network in Go.
			if q.Scope != nil && (ev.ClientID == nil || *ev.ClientID == "") {
				if !sourceInNetworks(ev.SourceIP, nets) {
					continue
				}
			}
			if len(out) < cap-1 {
				// Cursor is the rowid of the last RETURNED item, so the next
				// page uses e.id < last and does not skip past unreturned rows.
				last = rowidOf(ev.ID)
				out = append(out, ev)
			} else {
				more = true
			}
		}
		return rows.Err()
	})
	return out, last, more, e
}

func rowidOf(id string) int64 {
	_, r, _ := splitEventID(id)
	return r
}

func parseNetworks(scope *Scope) []netip.Prefix {
	if scope == nil {
		return nil
	}
	var nets []netip.Prefix
	for _, raw := range scope.Networks {
		if p, e := netip.ParsePrefix(raw); e == nil {
			nets = append(nets, p.Masked())
		} else if a, e := netip.ParseAddr(raw); e == nil {
			nets = append(nets, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return nets
}

func sourceInNetworks(ipStr string, nets []netip.Prefix) bool {
	ip, e := netip.ParseAddr(ipStr)
	if e != nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) || n.Contains(ip.Unmap()) {
			return true
		}
	}
	return false
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := strings.Repeat("?,", n)
	return s[:len(s)-1]
}

// Event returns one event by its "<YYYYMM>-<rowid>" id, or ErrNotFound when it
// is missing, outside scope, or malformed. As a compatibility fallback a
// legacy source id (the original input v.ID) is accepted and resolved across
// retained months; the API exposes only the compact id form.
func (t *Store) Event(ctx context.Context, id string, scope *Scope) (events.Event, error) {
	compact, rowid, ok := splitEventID(id)
	if ok {
		key := expandMonth(compact)
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return events.Event{}, e
		}
		var ev events.Event
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			row := r.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM event e JOIN host h ON h.id=e.host_id WHERE e.id=?", rowid)
			v, e := scanEventRow(key, row.Scan)
			if e != nil {
				return e
			}
			ev = v
			return nil
		})
		if errors.Is(e, sql.ErrNoRows) {
			return events.Event{}, ErrNotFound
		}
		if e != nil {
			return events.Event{}, e
		}
		if !inScope(ev.ClientID, ev.SourceIP, scope) {
			return events.Event{}, ErrNotFound
		}
		return ev, nil
	}
	// Legacy source-id fallback.
	ev, found, e := t.eventBySourceID(ctx, id, scope)
	if e != nil {
		return events.Event{}, e
	}
	if !found {
		return events.Event{}, ErrNotFound
	}
	return ev, nil
}

// eventBySourceID scans retained months newest-first for an event whose
// source_id matches, applying scope. Returns found=false when no match exists
// or the match is out of scope (so callers answer 404 either way).
func (t *Store) eventBySourceID(ctx context.Context, sourceID string, scope *Scope) (events.Event, bool, error) {
	months := t.listMonths()
	for i := len(months) - 1; i >= 0; i-- {
		key := months[i]
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return events.Event{}, false, e
		}
		var ev events.Event
		var found bool
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			row := r.QueryRowContext(ctx, "SELECT "+eventColumns+" FROM event e JOIN host h ON h.id=e.host_id WHERE e.source_id=?", sourceID)
			v, e := scanEventRow(key, row.Scan)
			if e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return nil
				}
				return e
			}
			ev = v
			found = true
			return nil
		})
		if e != nil {
			return events.Event{}, false, e
		}
		if found {
			if !inScope(ev.ClientID, ev.SourceIP, scope) {
				return events.Event{}, false, nil
			}
			return ev, true, nil
		}
	}
	return events.Event{}, false, nil
}

var _ = fmt.Sprintf

// RecentEvents returns raw events in [from,to) across retained months, newest
// first, applying scope. Used by the inspector's draft-impact assessment which
// must re-evaluate actual requests against the active policy and the draft.
func (t *Store) RecentEvents(ctx context.Context, from, to time.Time, scope *Scope) ([]events.Event, error) {
	out := []events.Event{}
	nets := parseNetworks(scope)
	clientSet := map[string]bool{}
	if scope != nil {
		for _, id := range scope.ClientIDs {
			clientSet[id] = true
		}
	}
	months := t.listMonths()
	for i := len(months) - 1; i >= 0; i-- {
		key := months[i]
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return out, e
		}
		var args []any
		where := []string{"e.ts>=?", "e.ts<=?"}
		args = append(args, from.UnixMilli(), to.UnixMilli())
		if scope != nil {
			if len(scope.ClientIDs) == 0 {
				where = append(where, "client_id IS NULL")
			} else {
				ph := placeholders(len(scope.ClientIDs))
				where = append(where, "(client_id IN ("+ph+") OR client_id IS NULL)")
				for _, id := range scope.ClientIDs {
					args = append(args, id)
				}
			}
		}
		query := "SELECT " + eventColumns + " FROM event e JOIN host h ON h.id=e.host_id WHERE " + strings.Join(where, " AND ") + " ORDER BY e.id DESC LIMIT 5000"
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			rows, e := r.QueryContext(ctx, query, args...)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				ev, e := scanEventRow(key, rows.Scan)
				if e != nil {
					return e
				}
				if scope != nil && (ev.ClientID == nil || *ev.ClientID == "") {
					if !sourceInNetworks(ev.SourceIP, nets) {
						continue
					}
				}
				out = append(out, ev)
			}
			return rows.Err()
		})
		if e != nil {
			return out, e
		}
	}
	return out, nil
}
