// Package telemetry stores proxy events in monthly SQLite files plus a
// rollups.db of long-lived aggregates, separate from the control database.
// The control DB keeps users, revisions, operations and audit; telemetry holds
// raw events (retained by age/budget) and rolled-up counters (3 years). A lost
// or corrupted telemetry file never breaks the panel.
package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kraken.local/kraken/internal/events"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by Event for a missing, out-of-scope or garbage id.
var ErrNotFound = errors.New("not found")

// ErrClosed is returned by any method called after Close. Statistics and
// inspector handlers translate it into a 503 so a graceful shutdown does not
// panic on in-flight queries.
var ErrClosed = errors.New("telemetry: store closed")

// Options configures Open. Zero durations fall back to defaults.
type Options struct {
	Dir             string
	RawRetention    time.Duration // 0 -> 90 days
	RawBudgetBytes  int64         // 0 -> 5 GiB; summed size of monthly files
	HourlyRetention time.Duration // 0 -> 30 days
	RollupRetention time.Duration // 0 -> 3 years
	Now             func() time.Time
}

// Scope is the manager's view: his client IDs and unmatched sources inside his
// networks. nil *Scope = admin (no filtering); empty lists = nothing visible.
type Scope struct {
	ClientIDs, Networks []string
}

// Counts are the additive counters shared by buckets and host rows.
type Counts struct {
	Requests, Denied, Bytes int64
}

// Bucket is one point of a Series.
type Bucket struct {
	Start time.Time
	Counts
}

// HostCount is a per-host aggregate.
type HostCount struct {
	Host string
	Counts
}

// SourceCount is an unknown-source aggregate with its top destinations.
type SourceCount struct {
	SourceIP string
	Counts
	LastSeen time.Time
	Top      []HostCount // up to 5
}

const (
	defaultRawRetention    = 90 * 24 * time.Hour
	defaultRawBudget       = 5 * 1024 * 1024 * 1024
	defaultHourlyRetention = 30 * 24 * time.Hour
	defaultRollupRetention = 3 * 365 * 24 * time.Hour
	maxOpenMonths          = 4 // keep at most this many old monthly files open
	readerPoolSize         = 4
	brokenSuffix           = ".broken"
)

// Store is the telemetry store. It owns rollups.db and lazily opens monthly
// event files. All methods are safe for concurrent use.
type Store struct {
	opts Options
	now  func() time.Time

	rollWriter *sql.DB      // 1 writer connection, immediate tx lock
	rollPool   chan *sql.DB // reader pool, query_only

	mu        sync.Mutex
	months    map[string]*monthFile // keyed "YYYY-MM"
	openOrder []string              // LRU of opened month keys, oldest first
	broken    int                   // count of files quarantined this process
	closed    bool                  // set under mu by Close; gates further reads
}

// monthFile holds a writer and a reader pool for one monthly events DB.
type monthFile struct {
	key    string // "YYYY-MM"
	path   string
	writer *sql.DB
	pool   chan *sql.DB
}

// Open prepares the telemetry directory, opens rollups.db (rebuilding it from
// raw files if missing/corrupt) and loads the most recent checkpoint. It does
// not open monthly files until they are needed.
func Open(o Options) (*Store, error) {
	if o.Dir == "" {
		return nil, errors.New("telemetry: empty Dir")
	}
	if o.RawRetention <= 0 {
		o.RawRetention = defaultRawRetention
	}
	if o.RawBudgetBytes <= 0 {
		o.RawBudgetBytes = defaultRawBudget
	}
	if o.HourlyRetention <= 0 {
		o.HourlyRetention = defaultHourlyRetention
	}
	if o.RollupRetention <= 0 {
		o.RollupRetention = defaultRollupRetention
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if e := os.MkdirAll(o.Dir, 0700); e != nil {
		return nil, e
	}
	t := &Store{opts: o, now: o.Now, months: map[string]*monthFile{}, rollPool: make(chan *sql.DB, readerPoolSize)}
	if e := t.openRollups(); e != nil {
		return nil, e
	}
	return t, nil
}

// Close closes rollups.db and every open monthly file. It is idempotent and
// safe to call concurrently with in-flight queries: subsequent reads observe
// ErrClosed instead of panicking on a closed pool channel.
func (t *Store) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	var first error
	for _, mf := range t.months {
		if e := mf.close(); e != nil && first == nil {
			first = e
		}
	}
	t.months = nil
	t.openOrder = nil
	if t.rollWriter != nil {
		if e := t.rollWriter.Close(); e != nil && first == nil {
			first = e
		}
		t.rollWriter = nil
	}
	// Drain reader connections and close them. The pool channel itself is left
	// open so a racing rollReader does not panic on send-after-close; its
	// deferred send goes through a select with a default that closes the
	// connection instead.
	for {
		select {
		case r := <-t.rollPool:
			if r != nil {
				r.Close()
			}
		default:
			t.mu.Unlock()
			return first
		}
	}
}

// RawRetentionDays returns the configured raw-event retention in days, for API
// responses that report the journal window.
func (t *Store) RawRetentionDays() int {
	if t.opts.RawRetention <= 0 {
		return 90
	}
	return int(t.opts.RawRetention / (24 * time.Hour))
}

func (mf *monthFile) close() error {
	if mf.writer != nil {
		e := mf.writer.Close()
		mf.writer = nil
		if mf.pool != nil {
			// Close the channel so future receives return nil immediately and
			// drain buffered readers. We do NOT reset mf.pool to nil: reader
			// defers snapshot the field to return their borrowed connection,
			// and reassigning it here would race. A borrowed reader whose
			// defer runs after close recovers from the send and closes its
			// own connection.
			close(mf.pool)
			for r := range mf.pool {
				r.Close()
			}
		}
		return e
	}
	return nil
}

// monthKey returns "YYYY-MM" for a time in UTC.
func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }

// monthEndOf returns the last instant (UTC) of the month encoded by a "YYYY-MM"
// key, or the zero time and false if the key is malformed. Used by age
// retention so a month is deleted when its content period expires, regardless
// of the file's filesystem mtime.
func monthEndOf(key string) (time.Time, bool) {
	start, e := time.Parse("2006-01", key)
	if e != nil {
		return time.Time{}, false
	}
	return start.AddDate(0, 1, 0).Add(-time.Second), true
}

// monthPath returns the events DB path for a key.
func (t *Store) monthPath(key string) string { return filepath.Join(t.opts.Dir, "events-"+key+".db") }

// rollPath returns the rollups.db path.
func (t *Store) rollPath() string { return filepath.Join(t.opts.Dir, "rollups.db") }

// dsn builds a modernc DSN with the requested pragmas and tx lock mode.
func dsn(path, txlock string, queryOnly bool) string {
	q := []string{
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=synchronous(NORMAL)",
	}
	if txlock != "" {
		q = append(q, "_txlock="+txlock)
	}
	if queryOnly {
		q = append(q, "_pragma=query_only(1)")
	}
	return "file:" + path + "?" + strings.Join(q, "&")
}

// openRollups opens rollups.db, recreating it if broken. If the file is absent
// or had to be recreated, aggregates are rebuilt from all retained monthly
// files (loss of rollups.db is recoverable from raw events).
func (t *Store) openRollups() error {
	_, statErr := os.Stat(t.rollPath())
	missing := errors.Is(statErr, os.ErrNotExist)
	rebuilt, e := t.openFileWithRecover(t.rollPath(), rollupsSchema, "rollups")
	if e != nil {
		return e
	}
	if missing {
		// A freshly created rollups.db has no aggregates; rebuild from raw.
		rebuilt = true
	}
	// Reader pool for rollups.db (query_only).
	for i := 0; i < readerPoolSize; i++ {
		r, e := openReader(t.rollPath())
		if e != nil {
			return e
		}
		t.rollPool <- r
	}
	if rebuilt {
		if e := t.rebuildRollups(); e != nil {
			return e
		}
	}
	return nil
}

// openFileWithRecover opens a writer for path, applying schema. On a corrupt or
// incompatible file it quarantines it (with -wal/-shm) and recreates, returning
// rebuilt=true so callers can repopulate. Panics inside open are contained.
func (t *Store) openFileWithRecover(path, schema, label string) (bool, error) {
	rebuilt := false
	open := func() (*sql.DB, error) {
		db, e := sql.Open("sqlite", dsn(path, "immediate", false))
		if e != nil {
			return nil, e
		}
		db.SetMaxOpenConns(1)
		return db, nil
	}
	db, e := open()
	if e != nil {
		return false, e
	}
	apply := func(d *sql.DB) error {
		_, e := d.Exec(schema)
		return e
	}
	if e = apply(db); e != nil {
		db.Close()
		if isCorrupt(e) {
			slog.Warn("telemetry file corrupt, recreating", "label", label)
			t.quarantine(path)
			t.broken++
			rebuilt = true
			db, e = open()
			if e != nil {
				return rebuilt, e
			}
			if e = apply(db); e != nil {
				db.Close()
				return rebuilt, e
			}
		} else {
			return rebuilt, e
		}
	}
	t.rollWriter = db
	return rebuilt, nil
}

// openReader opens a query_only reader connection pool entry.
func openReader(path string) (*sql.DB, error) {
	db, e := sql.Open("sqlite", dsn(path, "", true))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// quarantine renames a broken main file and its WAL/SHM sidecars out of the
// way using a unix timestamp suffix so the new file can be created cleanly.
func (t *Store) quarantine(path string) {
	stamp := t.now().Unix()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := path + suffix
		if _, e := os.Stat(src); e == nil {
			_ = os.Rename(src, src+brokenSuffix+"-"+strconv.FormatInt(stamp, 10))
		}
	}
}

// isCorrupt reports whether an error indicates a corrupt or incompatible file.
func isCorrupt(e error) bool {
	if e == nil {
		return false
	}
	s := e.Error()
	return strings.Contains(s, "file is not a database") || strings.Contains(s, "database disk image is malformed") || strings.Contains(s, "no such table") || strings.Contains(s, "SQLITE_NOTADB") || strings.Contains(s, "malformed")
}

// monthFor returns the monthFile for the given key, opening it lazily. It also
// enforces the bounded open cache: the oldest month beyond the limit is closed.
// The open and the eviction close happen outside t.mu so a slow open or a
// writer close does not stall concurrent readers.
func (t *Store) monthFor(ctx context.Context, key string) (*monthFile, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	if mf, ok := t.months[key]; ok {
		t.touchOpen(key)
		t.mu.Unlock()
		return mf, nil
	}
	t.mu.Unlock()
	path := t.monthPath(key)
	mf, e := t.openMonth(path, key)
	if e != nil {
		return nil, e
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = mf.close()
		return nil, ErrClosed
	}
	// Re-check: another goroutine may have opened the same key while we ran
	// openMonth without the lock. Discard ours and reuse the existing one so
	// we never leak duplicate file handles or grow openOrder with dupes.
	if existing, ok := t.months[key]; ok {
		t.mu.Unlock()
		_ = mf.close()
		return existing, nil
	}
	t.months[key] = mf
	t.openOrder = append(t.openOrder, key)
	t.touchOpen(key)
	toClose := t.evictMonthsLocked()
	t.mu.Unlock()
	for _, c := range toClose {
		_ = c.close()
	}
	return mf, nil
}

// currentMonth returns the monthFile for the current month per Now.
func (t *Store) currentMonth(ctx context.Context) (*monthFile, error) {
	return t.monthFor(ctx, monthKey(t.now()))
}

// openMonth opens a monthly DB, recreating it if broken (counted in
// Info.BrokenFiles). The schema is created if absent.
func (t *Store) openMonth(path, key string) (*monthFile, error) {
	mf := &monthFile{key: key, path: path, pool: make(chan *sql.DB, readerPoolSize)}
	db, e := sql.Open("sqlite", dsn(path, "immediate", false))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(eventsSchema); e != nil {
		db.Close()
		if isCorrupt(e) {
			slog.Warn("telemetry monthly file corrupt, recreating", "month", key)
			t.quarantine(path)
			t.broken++
			db, e = sql.Open("sqlite", dsn(path, "immediate", false))
			if e != nil {
				return nil, e
			}
			db.SetMaxOpenConns(1)
			if _, e = db.Exec(eventsSchema); e != nil {
				db.Close()
				return nil, e
			}
		} else {
			return nil, e
		}
	}
	mf.writer = db
	for i := 0; i < readerPoolSize; i++ {
		r, e := openReader(path)
		if e != nil {
			mf.close()
			return nil, e
		}
		mf.pool <- r
	}
	return mf, nil
}

// touchOpen moves key to the end of openOrder (most recently used).
func (t *Store) touchOpen(key string) {
	for i, k := range t.openOrder {
		if k == key {
			t.openOrder = append(t.openOrder[:i], t.openOrder[i+1:]...)
			t.openOrder = append(t.openOrder, key)
			return
		}
	}
}

// evictMonthsLocked trims the open-month cache to maxOpenMonths. It returns
// the monthFiles that should be closed (the caller closes them outside t.mu so
// a writer close cannot block concurrent readers). The current month is never
// evicted.
func (t *Store) evictMonthsLocked() []*monthFile {
	current := monthKey(t.now())
	var toClose []*monthFile
	for len(t.openOrder) > maxOpenMonths {
		k := t.openOrder[0]
		if k == current {
			// move current to the end and keep going
			t.openOrder = append(t.openOrder[1:], k)
			continue
		}
		t.openOrder = t.openOrder[1:]
		if mf, ok := t.months[k]; ok {
			toClose = append(toClose, mf)
			delete(t.months, k)
		}
	}
	return toClose
}

// listMonths returnskeys of monthly files on disk in ascending order.
func (t *Store) listMonths() []string {
	entries, e := os.ReadDir(t.opts.Dir)
	if e != nil {
		return nil
	}
	var out []string
	for _, en := range entries {
		name := en.Name()
		if !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, "events-"), ".db")
		if len(key) == 7 && key[4] == '-' {
			out = append(out, key)
		}
	}
	sortAsc(out)
	return out
}

func sortAsc(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// rollReader borrows a rollups reader, runs f, and returns it. After Close
// the pool is drained and the closed flag is set; callers see ErrClosed.
func (t *Store) rollReader(ctx context.Context, f func(*sql.DB) error) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return ErrClosed
	}
	select {
	case r := <-t.rollPool:
		if r == nil {
			return ErrClosed
		}
		defer func() {
			sent := false
			func() {
				defer func() {
					if recover() != nil {
						sent = false
					}
				}()
				select {
				case t.rollPool <- r:
					sent = true
				default:
				}
			}()
			if !sent {
				r.Close()
			}
		}()
		return f(r)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// monthReader borrows a reader from a monthFile. If the month was closed
// concurrently the borrowed connection is closed by the defer (rather than
// returned to a closed channel and leaked).
func (t *Store) monthReader(mf *monthFile, ctx context.Context, f func(*sql.DB) error) error {
	select {
	case r := <-mf.pool:
		if r == nil {
			return ErrClosed
		}
		defer func() {
			sent := false
			func() {
				defer func() {
					if recover() != nil {
						sent = false
					}
				}()
				select {
				case mf.pool <- r:
					sent = true
				default:
				}
			}()
			if !sent {
				r.Close()
			}
		}()
		return f(r)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hostOf strips port/brackets/userinfo/path so the host dictionary and
// aggregation use bare hostnames. events.Parse already reduces a log URL, but
// stored data is untrusted.
func hostOf(destination string) string {
	h := destination
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	host, _, err := net.SplitHostPort(h)
	if err != nil {
		return strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	}
	return host
}

// hostPort returns the integer port of a destination (0 if absent).
func hostPort(destination string) int {
	h := destination
	if i := strings.Index(h, "/"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	_, port, err := net.SplitHostPort(h)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

// reconstructDestination rebuilds the stored host:port form for the journal.
func reconstructDestination(host string, port int) string {
	if port <= 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// inScope reports whether an event is visible under scope. nil = admin.
func inScope(clientID *string, sourceIP string, scope *Scope) bool {
	if scope == nil {
		return true
	}
	if clientID != nil && *clientID != "" {
		for _, id := range scope.ClientIDs {
			if id == *clientID {
				return true
			}
		}
		return false
	}
	ip, err := netip.ParseAddr(sourceIP)
	if err != nil {
		return false
	}
	for _, raw := range scope.Networks {
		if p, e := netip.ParsePrefix(raw); e == nil && (p.Contains(ip) || p.Contains(ip.Unmap())) {
			return true
		}
		if a, e := netip.ParseAddr(raw); e == nil && (a == ip || a == ip.Unmap()) {
			return true
		}
	}
	return false
}

// scopeClientAllowed reports whether clientID is listed in a non-nil scope.
func scopeClientAllowed(scope *Scope, clientID string) bool {
	if scope == nil {
		return true
	}
	for _, id := range scope.ClientIDs {
		if id == clientID {
			return true
		}
	}
	return false
}

// scopeIntersectClients restricts clientIDs to those visible under scope.
func scopeIntersectClients(scope *Scope, clientIDs []string) []string {
	if scope == nil {
		return clientIDs
	}
	allowed := map[string]bool{}
	for _, id := range scope.ClientIDs {
		allowed[id] = true
	}
	out := []string{}
	for _, id := range clientIDs {
		if allowed[id] {
			out = append(out, id)
		}
	}
	return out
}

// parseStamp converts an events.Event.Timestamp (RFC3339) to unix milliseconds.
func parseStamp(ts string) (int64, error) {
	t, e := time.Parse(time.RFC3339Nano, ts)
	if e != nil {
		return 0, e
	}
	return t.UnixMilli(), nil
}

// formatStamp converts unix milliseconds to RFC3339Nano UTC.
func formatStamp(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

// eventFailed mirrors the SaveEvents failed rule.
func eventFailed(v events.Event) bool {
	return strings.Contains(v.ProxyResult, "DENIED") || strings.Contains(v.ProxyResult, "FAIL") || (v.ProxyHTTPStatus != nil && *v.ProxyHTTPStatus >= 400)
}

var _ = fmt.Sprintf
