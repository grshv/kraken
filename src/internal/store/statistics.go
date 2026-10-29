package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"time"

	"kraken.local/kraken/internal/events"
)

// Statistics describes completed access-log records retained in SQLite, not
// active requests, available bandwidth or origin traffic hidden in CONNECT.
type Statistics struct {
	From             string          `json:"from"`
	To               string          `json:"to"`
	ObservedAt       string          `json:"observed_at"`
	Requests         int64           `json:"requests"`
	Denied           int64           `json:"denied"`
	BytesObserved    int64           `json:"bytes_observed"`
	UniqueSources    int64           `json:"unique_sources"`
	RecordLimit      int             `json:"record_limit"`
	RetentionLimited bool            `json:"retention_limited"`
	Hourly           []StatisticHour `json:"hourly"`
}

type StatisticHour struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Requests int64  `json:"requests"`
	Denied   int64  `json:"denied"`
}

// EventStatistics uses event time in a rolling [now-24h, now) window. A bounded
// read preserves the existing flat-body schema and makes no external log reads.
// At the retention cap, statistics explicitly describe a potentially incomplete
// retained sample; they never pretend to count records no longer in SQLite.
// When scope is non-nil, only events for the manager's clients (or unmatched
// sources inside his networks) are counted.
func (s *Store) EventStatistics(ctx context.Context, now time.Time, scope *EventScope) (Statistics, error) {
	now = now.UTC()
	from := now.Add(-24 * time.Hour)
	format := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	out := Statistics{From: format(from), To: format(now), ObservedAt: format(now), RecordLimit: EventLimit, Hourly: make([]StatisticHour, 24)}
	for i := range out.Hourly {
		start := from.Add(time.Duration(i) * time.Hour)
		out.Hourly[i] = StatisticHour{From: format(start), To: format(start.Add(time.Hour))}
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT body FROM proxy_events ORDER BY seq DESC LIMIT ?", EventLimit)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	sources := map[string]struct{}{}
	read := 0
	clientSet := map[string]bool{}
	nets := []netip.Prefix{}
	if scope != nil {
		for _, id := range scope.ClientIDs {
			clientSet[id] = true
		}
		for _, raw := range scope.Networks {
			if n, e := netip.ParsePrefix(raw); e == nil {
				nets = append(nets, n.Masked())
			} else if a, e := netip.ParseAddr(raw); e == nil {
				nets = append(nets, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
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
		if scope != nil {
			if event.ClientID != nil && *event.ClientID != "" {
				if !clientSet[*event.ClientID] {
					continue
				}
			} else {
				matched := false
				if ip, e := netip.ParseAddr(event.SourceIP); e == nil {
					for _, n := range nets {
						if n.Contains(ip) {
							matched = true
							break
						}
					}
				}
				if !matched {
					continue
				}
			}
		}
		timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			return out, errors.New("invalid stored event timestamp")
		}
		if timestamp.Before(from) || !timestamp.Before(now) {
			continue
		}
		if event.Bytes < 0 || event.Bytes > math.MaxInt64-out.BytesObserved {
			return out, errors.New("invalid observed byte total")
		}
		hour := int(timestamp.Sub(from) / time.Hour)
		out.Requests++
		out.Hourly[hour].Requests++
		out.BytesObserved += event.Bytes
		// A bare HTTP 403 can be an origin response. Only Squid's DENIED
		// result establishes a proxy denial; it also covers auth 407.
		if strings.Contains(event.ProxyResult, "DENIED") {
			out.Denied++
			out.Hourly[hour].Denied++
		}
		if event.SourceIP != "" {
			sources[event.SourceIP] = struct{}{}
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.UniqueSources = int64(len(sources))
	out.RetentionLimited = read >= EventLimit
	return out, nil
}
