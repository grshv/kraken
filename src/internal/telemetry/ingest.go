package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"kraken.local/kraken/internal/events"
)

// checkpointKey is the single row in the checkpoint table of a monthly file.
const checkpointKey = "cp"

// Checkpoint returns the committed access-log checkpoint, taken from the most
// recent monthly file that has one. A zero Checkpoint is returned when no file
// has a checkpoint yet.
func (t *Store) Checkpoint(ctx context.Context) (events.Checkpoint, error) {
	var cp events.Checkpoint
	months := t.listMonths()
	for i := len(months) - 1; i >= 0; i-- {
		key := months[i]
		mf, e := t.monthFor(ctx, key)
		if e != nil {
			return cp, e
		}
		var raw string
		e = t.monthReader(mf, ctx, func(r *sql.DB) error {
			return r.QueryRowContext(ctx, "SELECT v FROM checkpoint WHERE k=?", checkpointKey).Scan(&raw)
		})
		if e == nil {
			_ = json.Unmarshal([]byte(raw), &cp)
			return cp, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return cp, e
		}
	}
	return cp, nil
}

// Ingest atomically writes items and the checkpoint into the current month
// file (by Now), then rolls the new events into aggregates. A failure during
// rollup does not lose data: the watermark stays and the next Ingest or Rollup
// catches up. After a successful return the data is visible to reads.
func (t *Store) Ingest(ctx context.Context, items []events.Event, cp events.Checkpoint) error {
	mf, e := t.currentMonth(ctx)
	if e != nil {
		return e
	}
	tx, e := mf.writer.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	hostIDs := map[string]int64{}
	hostInsert, e := tx.PrepareContext(ctx, "INSERT INTO host(name) VALUES(?) ON CONFLICT(name) DO UPDATE SET name=excluded.name RETURNING id")
	if e != nil {
		return e
	}
	defer hostInsert.Close()
	evInsert, e := tx.PrepareContext(ctx, "INSERT INTO event(ts,client_id,client_name,source_ip,host_id,port,method,result,http_status,duration_ms,bytes,failed,proxy_username,revision_id,match_basis,tls_mode,source_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id) DO NOTHING")
	if e != nil {
		return e
	}
	defer evInsert.Close()
	for _, v := range items {
		ts, e := parseStamp(v.Timestamp)
		if e != nil {
			return fmt.Errorf("telemetry: invalid timestamp: %w", e)
		}
		host := hostOf(v.Destination)
		hid, ok := hostIDs[host]
		if !ok {
			if e := hostInsert.QueryRowContext(ctx, host).Scan(&hid); e != nil {
				return e
			}
			hostIDs[host] = hid
		}
		var clientID, clientName, proxyUser, revID any
		if v.ClientID != nil && *v.ClientID != "" {
			clientID = *v.ClientID
		}
		if v.ClientName != nil {
			clientName = *v.ClientName
		}
		if v.ProxyUsername != nil {
			proxyUser = *v.ProxyUsername
		}
		if v.RevisionID != nil {
			revID = *v.RevisionID
		}
		var httpStatus any
		if v.ProxyHTTPStatus != nil {
			httpStatus = *v.ProxyHTTPStatus
		}
		failed := 0
		if eventFailed(v) {
			failed = 1
		}
		var sourceID any
		if v.ID != "" {
			sourceID = v.ID
		}
		if _, e = evInsert.ExecContext(ctx, ts, clientID, clientName, v.SourceIP, hid, hostPort(v.Destination), v.Method, v.ProxyResult, httpStatus, v.Duration, v.Bytes, failed, proxyUser, revID, v.ClientMatchBasis, v.TLSMode, sourceID); e != nil {
			return e
		}
	}
	body, e := json.Marshal(cp)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO checkpoint(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", checkpointKey, string(body)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	// Roll up the freshly written events; a failure here is non-fatal.
	_, _ = t.Rollup(ctx)
	return nil
}

// EventQuery filters the journal. Zero times mean no bound.
type EventQuery struct {
	SourceIP, Destination, Method, Result, ClientID string
	From, To                                        time.Time
	Failures                                        bool
	Cursor                                          string
	Limit                                           int // 1..200
	Scope                                           *Scope
}

// Page is one journal page.
type Page struct {
	Items      []events.Event
	NextCursor *string
	Gap        bool
}

// scanEventRow scans a joined event+host row into events.Event.
func scanEventRow(mfKey string, sc func(...any) error) (events.Event, error) {
	var v events.Event
	var ts int64
	var rid int64
	var clientID, clientName, sourceIP, hostName, method, result, matchBasis, tlsMode sql.NullString
	var proxyUser, revID sql.NullString
	var port, httpStatus, duration, bytes sql.NullInt64
	var failed int
	if e := sc(&rid, &ts, &clientID, &clientName, &sourceIP, &hostName, &port, &method, &result, &httpStatus, &duration, &bytes, &failed, &proxyUser, &revID, &matchBasis, &tlsMode); e != nil {
		return v, e
	}
	v.ID = compactMonth(mfKey) + "-" + fmt.Sprintf("%d", rid)
	v.Timestamp = formatStamp(ts)
	if clientID.Valid && clientID.String != "" {
		s := clientID.String
		v.ClientID = &s
	}
	if clientName.Valid {
		s := clientName.String
		v.ClientName = &s
	}
	if proxyUser.Valid {
		s := proxyUser.String
		v.ProxyUsername = &s
	}
	if revID.Valid {
		s := revID.String
		v.RevisionID = &s
	}
	v.SourceIP = sourceIP.String
	v.Method = method.String
	v.ProxyResult = result.String
	v.ClientMatchBasis = matchBasis.String
	if v.ClientMatchBasis == "" {
		v.ClientMatchBasis = "unknown"
	}
	v.TLSMode = tlsMode.String
	if httpStatus.Valid {
		n := int(httpStatus.Int64)
		v.ProxyHTTPStatus = &n
	}
	v.Duration = duration.Int64
	v.Bytes = bytes.Int64
	v.Destination = reconstructDestination(hostName.String, int(port.Int64))
	return v, nil
}

var eventColumns = `e.id,e.ts,e.client_id,e.client_name,e.source_ip,h.name,e.port,e.method,e.result,e.http_status,e.duration_ms,e.bytes,e.failed,e.proxy_username,e.revision_id,e.match_basis,e.tls_mode`

func splitEventID(id string) (string, int64, bool) {
	if len(id) < 8 || id[6] != '-' {
		return "", 0, false
	}
	key := id[:6]
	rowid, e := parseInt64(id[7:])
	if e != nil || rowid <= 0 {
		return "", 0, false
	}
	return key, rowid, true
}

// compactMonth turns a "YYYY-MM" file key into the "YYYYMM" form used in ids.
func compactMonth(key string) string {
	if len(key) != 7 || key[4] != '-' {
		return key
	}
	return key[:4] + key[5:]
}

// expandMonth turns a compact "YYYYMM" id prefix back into the "YYYY-MM" file key.
func expandMonth(compact string) string {
	if len(compact) != 6 {
		return compact
	}
	return compact[:4] + "-" + compact[4:]
}

func parseInt64(s string) (int64, error) {
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, errors.New("telemetry: bad integer")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
