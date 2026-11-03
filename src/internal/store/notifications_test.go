package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNotificationMarksLifecycle(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "marks.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.MarkNotifications(ctx, []string{"source/10.0.0.1", "denied/c/x.test"}, "seen", "admin"); e != nil {
		t.Fatal(e)
	}
	if e = s.MarkNotifications(ctx, []string{"source/10.0.0.1"}, "ignored", "mgr"); e != nil {
		t.Fatal(e)
	}
	m, e := s.NotificationMarks(ctx)
	if e != nil || len(m) != 2 || m["source/10.0.0.1"].Status != "ignored" || m["source/10.0.0.1"].Actor != "mgr" || m["denied/c/x.test"].Status != "seen" {
		t.Fatalf("marks: %+v %v", m, e)
	}
	if e = s.MarkNotifications(ctx, []string{"denied/c/x.test"}, "new", "admin"); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []struct {
		ids    []string
		status string
	}{{nil, "seen"}, {[]string{"a"}, "done"}, {make([]string, MaxNotificationMarks+1), "seen"}} {
		if e = s.MarkNotifications(ctx, bad.ids, bad.status, "admin"); e != ErrBadMark {
			t.Fatalf("%v %q: %v", len(bad.ids), bad.status, e)
		}
	}
	// Marks older than retention are pruned; fresh ones stay.
	if _, e = s.DB.Exec("UPDATE notification_marks SET marked_at=?", time.Now().Add(-NotificationMarkRetention-time.Hour).Unix()); e != nil {
		t.Fatal(e)
	}
	if e = s.MarkNotifications(ctx, []string{"source/10.0.0.2"}, "seen", "admin"); e != nil {
		t.Fatal(e)
	}
	if e = s.Prune(ctx, time.Now().Add(-AuditRetention), 0); e != nil {
		t.Fatal(e)
	}
	m, _ = s.NotificationMarks(ctx)
	if len(m) != 1 || m["source/10.0.0.2"].Status != "seen" {
		t.Fatalf("after prune: %+v", m)
	}
	var n int
	s.DB.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action='mark_notifications'").Scan(&n)
	if n != 4 {
		t.Fatalf("audit rows: %d", n)
	}
}
