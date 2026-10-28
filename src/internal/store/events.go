package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"squid-control.local/squid/internal/events"
)

const EventRetentionDays = 7
const EventLimit = 50000

func (s *Store) EventCheckpoint() (events.Checkpoint, error) {
	var cp events.Checkpoint
	e := s.Get("event_checkpoint", &cp)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return cp, e
}
func (s *Store) SaveEvents(items []events.Event, cp events.Checkpoint) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, v := range items {
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		failed := strings.Contains(v.ProxyResult, "DENIED") || strings.Contains(v.ProxyResult, "FAIL") || (v.ProxyHTTPStatus != nil && *v.ProxyHTTPStatus >= 400)
		if _, e = tx.Exec("INSERT OR IGNORE INTO proxy_events(event_id,client_id,client_name,failed,ingested_at,body) VALUES(?,?,?,?,?,?)", v.ID, v.ClientID, v.ClientName, failed, now, string(b)); e != nil {
			return e
		}
	}
	b, e := json.Marshal(cp)
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO kv(key,value) VALUES('event_checkpoint',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(b)); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM proxy_events WHERE ingested_at<? OR seq <= (SELECT COALESCE(MAX(seq),0)-? FROM proxy_events)", now-EventRetentionDays*86400, EventLimit); e != nil {
		return e
	}
	return tx.Commit()
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	Gap        bool    `json:"gap"`
	ObservedAt string  `json:"observed_at"`
}
type EventQuery struct {
	SourceIP, Destination, Method, Result, From, To string
	ClientID                                        string
	Failures                                        bool
	Cursor                                          string
	Limit                                           int
}
type AuditQuery struct {
	Actor, Action, Cursor string
	Limit                 int
}
type AuditEvent struct {
	ID        string `json:"id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Entity    string `json:"entity_id"`
	Timestamp string `json:"timestamp"`
}
type cursor struct {
	Before int64  `json:"b"`
	Filter string `json:"f"`
}

var ErrCursor = errors.New("invalid cursor or changed filters")

func pageStart(raw, filter string) (cursor, error) {
	h := sha256.Sum256([]byte(filter))
	hash := hex.EncodeToString(h[:])
	c := cursor{Before: 1<<63 - 1, Filter: hash}
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
	if e = json.Unmarshal(b, &c); e != nil || c.Before <= 0 || c.Filter != hash {
		return c, ErrCursor
	}
	return c, nil
}
func nextCursor(c cursor, id int64) *string {
	c.Before = id
	b, _ := json.Marshal(c)
	v := base64.RawURLEncoding.EncodeToString(b)
	return &v
}

func (s *Store) EventPage(q EventQuery) (Page[events.Event], error) {
	p := Page[events.Event]{Items: []events.Event{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if q.Limit < 1 || q.Limit > 200 {
		return p, errors.New("invalid limit")
	}
	filter := q
	filter.Cursor = ""
	filter.Limit = 0
	encoded, _ := json.Marshal(filter)
	c, e := pageStart(q.Cursor, "events\x00"+string(encoded))
	if e != nil {
		return p, e
	}
	clause := "seq<?"
	args := []any{c.Before}
	if q.ClientID != "" {
		clause += " AND client_id=?"
		args = append(args, q.ClientID)
	}
	if q.Failures {
		clause += " AND failed=1"
	}
	for _, field := range []struct{ column, value string }{
		{"source_ip", q.SourceIP}, {"method", q.Method}, {"proxy_result", q.Result},
	} {
		if field.value != "" {
			clause += " AND json_extract(body,'$." + field.column + "')=?"
			args = append(args, field.value)
		}
	}
	if q.Destination != "" {
		clause += " AND instr(lower(json_extract(body,'$.destination')),lower(?))>0"
		args = append(args, q.Destination)
	}
	if q.From != "" {
		clause += " AND julianday(json_extract(body,'$.timestamp'))>=julianday(?)"
		args = append(args, q.From)
	}
	if q.To != "" {
		clause += " AND julianday(json_extract(body,'$.timestamp'))<julianday(?)"
		args = append(args, q.To)
	}
	args = append(args, q.Limit+1)
	rows, e := s.DB.Query("SELECT seq,body FROM proxy_events WHERE "+clause+" ORDER BY seq DESC LIMIT ?", args...)
	if e != nil {
		return p, e
	}
	var last int64
	for rows.Next() {
		var id int64
		var body string
		if e = rows.Scan(&id, &body); e != nil {
			rows.Close()
			return p, e
		}
		if len(p.Items) == q.Limit {
			p.NextCursor = nextCursor(c, last)
			break
		}
		var v events.Event
		if e = json.Unmarshal([]byte(body), &v); e != nil {
			rows.Close()
			return p, e
		}
		p.Items = append(p.Items, v)
		last = id
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	var floor, high int64
	if e = s.DB.QueryRow("SELECT COALESCE(MIN(seq),0),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='proxy_events'),0) FROM proxy_events").Scan(&floor, &high); e != nil {
		return p, e
	}
	p.Gap = (floor > 1 || (floor == 0 && high > 0)) && p.NextCursor == nil
	return p, nil
}
func (s *Store) AuditPage(q AuditQuery) (Page[AuditEvent], error) {
	p := Page[AuditEvent]{Items: []AuditEvent{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if q.Limit < 1 || q.Limit > 200 {
		return p, errors.New("invalid limit")
	}
	c, e := pageStart(q.Cursor, "audit\x00"+q.Actor+"\x00"+q.Action)
	if e != nil {
		return p, e
	}
	clause := "id<?"
	args := []any{c.Before}
	if q.Actor != "" {
		clause += " AND actor=?"
		args = append(args, q.Actor)
	}
	if q.Action != "" {
		clause += " AND action=?"
		args = append(args, q.Action)
	}
	args = append(args, q.Limit+1)
	rows, e := s.DB.Query("SELECT id,actor,action,entity_id,created_at FROM audit_events WHERE "+clause+" ORDER BY id DESC LIMIT ?", args...)
	if e != nil {
		return p, e
	}
	var last int64
	for rows.Next() {
		var id, ts int64
		var v AuditEvent
		if e = rows.Scan(&id, &v.Actor, &v.Action, &v.Entity, &ts); e != nil {
			rows.Close()
			return p, e
		}
		if len(p.Items) == q.Limit {
			p.NextCursor = nextCursor(c, last)
			break
		}
		v.ID = fmt.Sprint(id)
		v.Timestamp = time.Unix(ts, 0).UTC().Format(time.RFC3339Nano)
		p.Items = append(p.Items, v)
		last = id
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	var floor, high int64
	if e = s.DB.QueryRow("SELECT COALESCE(MIN(id),0),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='audit_events'),0) FROM audit_events").Scan(&floor, &high); e != nil {
		return p, e
	}
	p.Gap = (floor > 1 || (floor == 0 && high > 0)) && p.NextCursor == nil
	return p, nil
}
func (s *Store) EventClients() ([]map[string]string, error) {
	rows, e := s.DB.Query("SELECT client_id,client_name FROM proxy_events WHERE seq IN (SELECT MAX(seq) FROM proxy_events WHERE client_id IS NOT NULL GROUP BY client_id) ORDER BY seq DESC LIMIT 200")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, name string
		if e = rows.Scan(&id, &name); e != nil {
			return nil, e
		}
		items = append(items, map[string]string{"id": id, "name": name})
	}
	return items, rows.Err()
}
