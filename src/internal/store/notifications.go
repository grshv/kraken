package store

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// NotificationMarkRetention bounds how long a "seen"/"ignored" mark lives;
// Prune deletes older marks and the notification counts as new again.
const NotificationMarkRetention = 90 * 24 * time.Hour

// MaxNotificationMarks caps one bulk mark request.
const MaxNotificationMarks = 500

// NotificationMark is the shared status a user set on a computed notification.
type NotificationMark struct {
	Status   string `json:"status"`
	Actor    string `json:"actor"`
	MarkedAt string `json:"marked_at"`
}

var ErrBadMark = errors.New("invalid notification mark")

// NotificationMarks returns all live marks keyed by notification id. The table
// is bounded by retention and by the number of distinct denied hosts.
func (s *Store) NotificationMarks(ctx context.Context) (map[string]NotificationMark, error) {
	ctx, cancel := opCtx(ctx)
	defer cancel()
	rows, e := s.Read.QueryContext(ctx, "SELECT id,status,actor,marked_at FROM notification_marks")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]NotificationMark{}
	for rows.Next() {
		var id string
		var m NotificationMark
		var at int64
		if e = rows.Scan(&id, &m.Status, &m.Actor, &at); e != nil {
			return nil, e
		}
		m.MarkedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		out[id] = m
	}
	return out, rows.Err()
}

// MarkNotifications sets status ("seen" or "ignored") on ids, or removes their
// marks when status is "new", with one audit row in the same transaction.
func (s *Store) MarkNotifications(ctx context.Context, ids []string, status, actor string) error {
	if len(ids) == 0 || len(ids) > MaxNotificationMarks {
		return ErrBadMark
	}
	if status != "new" && status != "seen" && status != "ignored" {
		return ErrBadMark
	}
	ctx, cancel := opCtx(ctx)
	defer cancel()
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, id := range ids {
		if status == "new" {
			_, e = tx.ExecContext(ctx, "DELETE FROM notification_marks WHERE id=?", id)
		} else {
			_, e = tx.ExecContext(ctx, `INSERT INTO notification_marks(id,status,actor,marked_at) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status, actor=excluded.actor, marked_at=excluded.marked_at`, id, status, actor, now)
		}
		if e != nil {
			return e
		}
	}
	entity := status + ":" + strconv.Itoa(len(ids))
	if len(ids) == 1 {
		entity = status + ":" + ids[0]
	}
	if e = s.AuditTx(ctx, tx, actor, "mark_notifications", entity); e != nil {
		return e
	}
	return tx.Commit()
}
