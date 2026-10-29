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
)

// Page is a generic journal page returned by AuditPage and used as a response
// DTO by httpapi. Cursor is bound to filters; Gap marks retention loss.
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
	Gap        bool    `json:"gap"`
	ObservedAt string  `json:"observed_at"`
}

type AuditQuery struct {
	Actor, Action, EntityID, Cursor string
	Limit                           int
	// Scope filters audit to a manager's view: his actor records and entities
	// in his scope. Empty for admin.
	Scope *AuditScope
}
type AuditScope struct {
	ClientIDs []string
	Networks  []string
	Actor     string
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

// ErrCursor signals an invalid cursor or one bound to different filters.
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

// scopeFingerprint returns a static hashable representation of the scope used
// to bind cursor stability: a change in scope invalidates an old cursor.
func scopeFingerprint(s any) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) AuditPage(q AuditQuery) (Page[AuditEvent], error) {
	p := Page[AuditEvent]{Items: []AuditEvent{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if q.Limit < 1 || q.Limit > 200 {
		return p, errors.New("invalid limit")
	}
	scopeFP := ""
	if q.Scope != nil {
		scopeFP = scopeFingerprint(*q.Scope)
	}
	c, e := pageStart(q.Cursor, "audit\x00"+q.Actor+"\x00"+q.Action+"\x00"+q.EntityID+"\x00"+scopeFP)
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
	if q.EntityID != "" {
		clause += " AND entity_id=?"
		args = append(args, q.EntityID)
	}
	// Manager sees audit where actor = him OR entity in his scope (client IDs).
	if q.Scope != nil {
		if q.Scope.Actor != "" {
			clause += " AND (actor=?"
			args = append(args, q.Scope.Actor)
			if len(q.Scope.ClientIDs) == 0 {
				clause += ")"
			} else {
				ph := placeholdersStr(len(q.Scope.ClientIDs))
				clause += " OR entity_id IN (" + ph + "))"
				for _, id := range q.Scope.ClientIDs {
					args = append(args, id)
				}
			}
		} else if len(q.Scope.ClientIDs) > 0 {
			ph := placeholdersStr(len(q.Scope.ClientIDs))
			clause += " AND entity_id IN (" + ph + ")"
			for _, id := range q.Scope.ClientIDs {
				args = append(args, id)
			}
		}
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

func placeholdersStr(n int) string {
	if n <= 0 {
		return "''"
	}
	out := strings.Repeat("?,", n)
	return out[:len(out)-1]
}

var _ = sql.ErrNoRows
