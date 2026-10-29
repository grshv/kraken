package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
)

// Activity is a bounded operational view of completed requests. It does not
// infer client availability, matched ACL reasons or live CONNECT contents.
type ActivitySource struct {
	SourceIP   string  `json:"source_ip"`
	ClientName *string `json:"client_name"`
	Username   *string `json:"proxy_username"`
	Requests   int     `json:"requests"`
	Denied     int     `json:"denied"`
	Errors     int     `json:"errors"`
	Bytes      int64   `json:"bytes_observed"`
	LastSeen   string  `json:"last_seen"`
}
type ActivityIssue struct {
	Username    *string `json:"proxy_username"`
	SourceIP    string  `json:"source_ip"`
	ClientName  *string `json:"client_name"`
	Destination string  `json:"destination"`
	Result      string  `json:"result"`
	Count       int     `json:"count"`
	LastSeen    string  `json:"last_seen"`
}
type Activity struct {
	From             string           `json:"from"`
	To               string           `json:"to"`
	Sources          []ActivitySource `json:"sources"`
	Issues           []ActivityIssue  `json:"issues"`
	TotalSources     int              `json:"total_sources"`
	TotalIssues      int              `json:"total_issues"`
	RetentionLimited bool             `json:"retention_limited"`
}

func (s *Store) RecentActivity(ctx context.Context, now time.Time, minutes int) (Activity, error) {
	from := now.Add(-time.Duration(minutes) * time.Minute)
	out := Activity{From: from.UTC().Format(time.RFC3339Nano), To: now.UTC().Format(time.RFC3339Nano), Sources: []ActivitySource{}, Issues: []ActivityIssue{}}
	rows, err := s.DB.QueryContext(ctx, "SELECT body FROM proxy_events ORDER BY seq DESC LIMIT ?", EventLimit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	sources := map[string]*ActivitySource{}
	issues := map[string]*ActivityIssue{}
	read := 0
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return out, err
		}
		read++
		var event events.Event
		if err = json.Unmarshal([]byte(body), &event); err != nil {
			return out, errors.New("invalid stored event")
		}
		stamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			return out, errors.New("invalid stored timestamp")
		}
		if stamp.Before(from) || !stamp.Before(now) {
			continue
		}
		username := ""
		if event.ProxyUsername != nil {
			username = *event.ProxyUsername
		}
		key := event.SourceIP + "\x00" + username
		source := sources[key]
		if source == nil {
			source = &ActivitySource{SourceIP: event.SourceIP, ClientName: event.ClientName, Username: event.ProxyUsername}
			sources[key] = source
		}
		source.Requests++
		if event.Bytes < 0 || event.Bytes > math.MaxInt64-source.Bytes {
			return out, errors.New("invalid observed bytes")
		}
		source.Bytes += event.Bytes
		if source.LastSeen == "" || stamp.After(parseActivityTime(source.LastSeen)) {
			source.LastSeen = event.Timestamp
			source.ClientName = event.ClientName
		}
		denied := strings.Contains(event.ProxyResult, "DENIED")
		failed := !denied && (strings.Contains(event.ProxyResult, "FAIL") || strings.Contains(event.ProxyResult, "ABORTED") || (event.ProxyHTTPStatus != nil && *event.ProxyHTTPStatus >= 400))
		if denied {
			source.Denied++
		}
		if failed {
			source.Errors++
		}
		if denied || failed {
			key += "\x00" + event.Destination + "\x00" + event.ProxyResult
			issue := issues[key]
			if issue == nil {
				issue = &ActivityIssue{Username: event.ProxyUsername, SourceIP: event.SourceIP, ClientName: event.ClientName, Destination: event.Destination, Result: event.ProxyResult}
				issues[key] = issue
			}
			issue.Count++
			if issue.LastSeen == "" || stamp.After(parseActivityTime(issue.LastSeen)) {
				issue.LastSeen = event.Timestamp
			}
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, v := range sources {
		out.Sources = append(out.Sources, *v)
	}
	for _, v := range issues {
		out.Issues = append(out.Issues, *v)
	}
	sort.Slice(out.Sources, func(i, j int) bool {
		a, b := out.Sources[i], out.Sources[j]
		if a.Requests != b.Requests {
			return a.Requests > b.Requests
		}
		return a.SourceIP+stringValue(a.Username) < b.SourceIP+stringValue(b.Username)
	})
	sort.Slice(out.Issues, func(i, j int) bool {
		a, b := out.Issues[i], out.Issues[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.SourceIP+a.Destination+a.Result < b.SourceIP+b.Destination+b.Result
	})
	out.TotalSources = len(out.Sources)
	out.TotalIssues = len(out.Issues)
	if len(out.Sources) > 10 {
		out.Sources = out.Sources[:10]
	}
	if len(out.Issues) > 10 {
		out.Issues = out.Issues[:10]
	}
	out.RetentionLimited = read >= EventLimit
	return out, nil
}
func parseActivityTime(value string) time.Time { t, _ := time.Parse(time.RFC3339Nano, value); return t }
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
