package store

import (
	"context"
	"fmt"
	"kraken.local/kraken/internal/events"
	"testing"
	"time"
)

func TestRecentActivityGroupsIssuesAndPreservesMeaning(t *testing.T) {
	s := eventStore(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	name, user := "build", "account"
	records := []events.Event{}
	for i, result := range []string{"TCP_DENIED/403", "TCP_DENIED/403", "TCP_MISS/404", "TCP_TUNNEL/200"} {
		e := sample(fmt.Sprint(i))
		e.ClientName = &name
		e.ProxyUsername = &user
		e.Timestamp = now.Add(-time.Minute).Format(time.RFC3339Nano)
		e.ProxyResult = result
		e.Bytes = 42
		status := 200
		if i == 2 {
			status = 404
		}
		e.ProxyHTTPStatus = &status
		records = append(records, e)
	}
	old := sample("old")
	old.Timestamp = now.Add(-16 * time.Minute).Format(time.RFC3339Nano)
	records = append(records, old)
	future := sample("future")
	future.Timestamp = now.Format(time.RFC3339Nano)
	records = append(records, future)
	if err := s.SaveEvents(records, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentActivity(context.Background(), now, 15)
	if err != nil || len(got.Sources) != 1 || len(got.Issues) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	source := got.Sources[0]
	if source.Requests != 4 || source.Denied != 2 || source.Errors != 1 || source.Bytes != 168 || *source.Username != user {
		t.Fatal(source)
	}
	if got.Issues[0].Count != 2 || got.Issues[0].Result != "TCP_DENIED/403" {
		t.Fatal(got.Issues)
	}
	hour, err := s.RecentActivity(context.Background(), now, 60)
	if err != nil || hour.TotalSources != 2 {
		t.Fatal(hour, err)
	} // Separate unauthenticated source identity.
}
func TestRecentActivityLimitsAndStorageErrors(t *testing.T) {
	s := eventStore(t)
	now := time.Now()
	records := []events.Event{}
	for i := 0; i < 12; i++ {
		e := sample(fmt.Sprint(i))
		e.SourceIP = fmt.Sprintf("192.0.2.%d", i)
		e.Timestamp = now.Add(-time.Minute).Format(time.RFC3339Nano)
		e.ProxyResult = "TCP_DENIED/403"
		records = append(records, e)
	}
	if err := s.SaveEvents(records, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentActivity(context.Background(), now, 60)
	if err != nil || got.TotalSources != 12 || got.TotalIssues != 12 || len(got.Sources) != 10 || len(got.Issues) != 10 {
		t.Fatal(got, err)
	}
	s.DB.Close()
	if _, err = s.RecentActivity(context.Background(), now, 60); err == nil {
		t.Fatal("storage failure hidden")
	}
}

func TestActivityDayWeekMonthWindows(t *testing.T) {
	s := eventStore(t)
	now := time.Now()
	rows := []events.Event{}
	for i, days := range []int{0, 2, 14, 31} {
		v := sample(fmt.Sprint(i))
		v.Timestamp = now.Add(-time.Duration(days)*24*time.Hour - time.Minute).Format(time.RFC3339Nano)
		rows = append(rows, v)
	}
	if err := s.SaveEvents(rows, events.Checkpoint{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ minutes, want int }{{1440, 1}, {10080, 2}, {43200, 3}} {
		got, err := s.RecentActivity(context.Background(), now, c.minutes)
		if err != nil || len(got.Sources) != 1 || got.Sources[0].Requests != c.want {
			t.Fatal(c, got, err)
		}
	}
}
