// Package events reads only the managed Squid access log and numbered rotations.
package events

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

type Event struct {
	ID               string  `json:"id"`
	Timestamp        string  `json:"timestamp"`
	ClientID         *string `json:"client_id"`
	ClientName       *string `json:"client_name"`
	ClientMatchBasis string  `json:"client_match_basis"`
	SourceIP         string  `json:"source_ip"`
	Destination      string  `json:"destination"`
	Method           string  `json:"method"`
	ProxyResult      string  `json:"proxy_result"`
	ProxyHTTPStatus  *int    `json:"proxy_http_status"`
	Duration         int64   `json:"duration_ms"`
	Bytes            int64   `json:"bytes_observed"`
	TLSMode          string  `json:"tls_mode"`
	RevisionID       *string `json:"revision_id"`
}

type Position struct {
	Offset     int64  `json:"offset"`
	Anchor     string `json:"anchor"`
	Generation string `json:"generation"`
	Skipping   bool   `json:"skipping"`
	LastSeen   int64  `json:"last_seen"`
}
type Checkpoint struct {
	Files           map[string]Position `json:"files"`
	Dropped         int64               `json:"dropped_records"`
	Gap             bool                `json:"gap"`
	Backlog         bool                `json:"backlog"`
	SourceAvailable bool                `json:"source_available"`
	ObservedAt      string              `json:"observed_at"`
}

const BatchBytes = 256 * 1024
const MaxRecord = 16 * 1024

func identity(st os.FileInfo) string {
	v := st.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", v.Dev, v.Ino)
}
func anchor(f *os.File, offset int64) string {
	n := int64(64)
	if offset < n {
		n = offset
	}
	b := make([]byte, n)
	if _, e := f.ReadAt(b, offset-n); e != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Read is side-effect free: callers commit rows and the returned checkpoint
// together. Repeating a batch from the same checkpoint gives the same row IDs.
func Read(path string, previous Checkpoint) ([]Event, Checkpoint, error) {
	next := previous
	next.Files = map[string]Position{}
	next.Backlog = false
	next.SourceAvailable = false
	items := []Event{}
	budget := int64(BatchBytes)
	seen := map[string]bool{}
	for i := 29; i >= -1; i-- {
		p := path
		if i >= 0 {
			p += "." + strconv.Itoa(i)
		}
		entry, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, previous, e
		}
		if !entry.Mode().IsRegular() {
			return nil, previous, errors.New("log is not a regular file")
		}
		f, e := os.Open(p)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, previous, e
		}
		st, e := f.Stat()
		if e != nil {
			f.Close()
			return nil, previous, e
		}
		if !st.Mode().IsRegular() {
			f.Close()
			return nil, previous, errors.New("log is not a regular file")
		}
		key := identity(st)
		if seen[key] {
			f.Close()
			continue
		}
		seen[key] = true
		if i == -1 {
			next.SourceAvailable = true
		}
		pos, known := previous.Files[key]
		if known && (st.Size() < pos.Offset || anchor(f, pos.Offset) != pos.Anchor) {
			known = false
			next.Gap = true
		}
		if !known {
			pos = Position{Generation: fmt.Sprintf("%s:%d:%d", key, st.ModTime().UnixNano(), st.Size())}
		}
		if _, e = f.Seek(pos.Offset, 0); e != nil {
			f.Close()
			return nil, previous, e
		}
		allowance := budget
		if available := st.Size() - pos.Offset; allowance > available {
			allowance = available
		}
		reader := bufio.NewReaderSize(io.LimitReader(f, allowance), MaxRecord)
		for budget > 0 {
			line, readErr := reader.ReadSlice('\n')
			complete := readErr == nil
			if !complete && !errors.Is(readErr, bufio.ErrBufferFull) {
				if i >= 0 && errors.Is(readErr, io.EOF) && len(line) > 0 && pos.Offset+int64(len(line)) == st.Size() {
					pos.Offset += int64(len(line))
					budget -= int64(len(line))
					if !pos.Skipping {
						next.Dropped++
					}
					next.Gap = true
					pos.Skipping = false
				}
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					f.Close()
					return nil, previous, readErr
				}
				break
			}
			start := pos.Offset
			pos.Offset += int64(len(line))
			budget -= int64(len(line))
			if pos.Skipping || !complete {
				if !pos.Skipping {
					next.Dropped++
					next.Gap = true
				}
				pos.Skipping = !complete
				continue
			}
			event, ok := Parse(strings.TrimSuffix(string(line), "\n"))
			if !ok {
				next.Dropped++
				next.Gap = true
				continue
			}
			h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", pos.Generation, start)))
			event.ID = hex.EncodeToString(h[:])
			items = append(items, event)
		}
		pos.Anchor = anchor(f, pos.Offset)
		pos.LastSeen = time.Now().UnixNano()
		next.Files[key] = pos
		if pos.Offset < st.Size() {
			next.Backlog = true
		}
		f.Close()
	}
	// Keep a bounded set of missing identities so a temporarily absent file
	// can resume at its committed offset when it reappears, without replaying
	// its entire prefix under a fresh generation.
	missing := []string{}
	for key, pos := range previous.Files {
		if !seen[key] {
			next.Gap = true
			next.Files[key] = pos
			missing = append(missing, key)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return next.Files[missing[i]].LastSeen < next.Files[missing[j]].LastSeen })
	for _, key := range missing {
		if len(next.Files) <= 64 {
			break
		}
		delete(next.Files, key)
	}
	next.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return items, next, nil
}

func Parse(line string) (Event, bool) {
	v := Event{ClientMatchBasis: "unknown"}
	if len(line) > MaxRecord || !utf8.ValidString(line) {
		return v, false
	}
	p := strings.Fields(line)
	if len(p) < 7 {
		return v, false
	}
	seconds, e := strconv.ParseFloat(p[0], 64)
	if e != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 253402300799 {
		return v, false
	}
	ip, e := netip.ParseAddr(p[2])
	if e != nil {
		return v, false
	}
	duration, e := strconv.ParseInt(p[1], 10, 64)
	if e != nil || duration < 0 {
		return v, false
	}
	count, e := strconv.ParseInt(p[4], 10, 64)
	if e != nil || count < 0 {
		return v, false
	}
	if !token(p[3], 64) || !token(p[5], 16) {
		return v, false
	}
	target := p[6]
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, e := url.Parse(target)
	if e != nil || u.Hostname() == "" || len(u.Host) > 255 || strings.ContainsFunc(u.Host, func(r rune) bool { return unicode.IsControl(r) }) {
		return v, false
	}
	v.Timestamp = time.UnixMilli(int64(seconds * 1000)).UTC().Format(time.RFC3339Nano)
	v.SourceIP = ip.Unmap().String()
	v.Destination = u.Host
	v.Method = p[5]
	v.ProxyResult = p[3]
	v.Duration = duration
	v.Bytes = count
	v.TLSMode = "unknown"
	if v.Method == "CONNECT" {
		v.TLSMode = "tunnel"
	}
	parts := strings.Split(p[3], "/")
	if len(parts) == 2 {
		if n, e := strconv.Atoi(parts[1]); e == nil && n >= 100 && n <= 599 {
			v.ProxyHTTPStatus = &n
		}
	}
	return v, true
}
func token(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '/') {
			return false
		}
	}
	return true
}
