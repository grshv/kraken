// Package runtime manages a local Squid; it never calls Docker or a shell.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"squid-control.local/squid/internal/policy"
)

var ErrBusy = errors.New("runtime busy")
var ErrDrift = errors.New("configuration drift detected")

type Manifest struct {
	Compiler string          `json:"compiler_version,omitempty"`
	Revision string          `json:"revision_id"`
	Hash     string          `json:"hash"`
	Config   string          `json:"config"`
	Snapshot policy.Snapshot `json:"snapshot"`
}
type Journal struct {
	Operation string   `json:"operation_id"`
	Stage     string   `json:"stage"`
	Previous  Manifest `json:"previous"`
	Candidate Manifest `json:"candidate"`
	Outcome   string   `json:"outcome,omitempty"`
}
type Manager struct {
	Dir     string
	Binary  string
	Options policy.Options
	Command func(context.Context, ...string) error
	mu      sync.Mutex
	Persist func(string, []byte) error
	Probe   func(context.Context, string) error
}

func New(dir, binary, logPath, cert, key string) *Manager {
	m := &Manager{Dir: dir, Binary: binary, Options: policy.Options{StateDir: dir, LogPath: logPath, ProxyCert: cert, ProxyKey: key}}
	m.Persist = Atomic
	m.Command = m.command
	m.Probe = m.verify
	return m
}
func (m *Manager) ActivePath() string { return filepath.Join(m.Dir, "active.conf") }
func (m *Manager) lock() (func(), error) {
	if !m.mu.TryLock() {
		return nil, ErrBusy
	}
	if e := os.MkdirAll(m.Dir, 0700); e != nil {
		m.mu.Unlock()
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(m.Dir, "runtime.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		m.mu.Unlock()
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		m.mu.Unlock()
		return nil, ErrBusy
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close(); m.mu.Unlock() }, nil
}
func Atomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".stage-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (m *Manager) write(name string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return m.Persist(filepath.Join(m.Dir, name), b)
}
func (m *Manager) ReadManifest() (Manifest, error) {
	var v Manifest
	b, e := os.ReadFile(filepath.Join(m.Dir, "lkg.json"))
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return v, e
}
func (m *Manager) readJournal() (Journal, error) {
	var j Journal
	b, e := os.ReadFile(filepath.Join(m.Dir, "journal.json"))
	if e != nil {
		return j, e
	}
	e = json.Unmarshal(b, &j)
	return j, e
}
func (m *Manager) Journal() (Journal, error) { return m.readJournal() }
func (m *Manager) command(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, m.Binary, args...)
	var out limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if e := cmd.Run(); e != nil {
		return fmt.Errorf("squid control failed: %w (configuration output withheld to avoid leaking credentials)", e)
	}
	return nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < 16384 {
		space := 16384 - b.Len()
		if len(p) > space {
			p = p[:space]
		}
		b.Buffer.Write(p)
	}
	return n, nil
}
func (m *Manager) parse(ctx context.Context, config string) error {
	p := filepath.Join(m.Dir, "candidate.conf")
	if e := m.Persist(p, []byte(config)); e != nil {
		return e
	}
	defer os.Remove(p)
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return m.Command(c, "-f", p, "-k", "parse")
}
func (m *Manager) Validate(ctx context.Context, s policy.Snapshot) error {
	unlock, e := m.lock()
	if e != nil {
		return e
	}
	defer unlock()
	config, e := policy.Render(s, m.Options)
	if e != nil {
		return e
	}
	return m.parse(ctx, config)
}
func (m *Manager) checkDrift(prev Manifest) error {
	b, e := os.ReadFile(m.ActivePath())
	if e != nil {
		return e
	}
	if string(b) != prev.Config {
		return ErrDrift
	}
	return nil
}

// Recover runs before new mutations, and before every launch of Squid.
func (m *Manager) Recover(ctx context.Context, offline bool) error {
	unlock, e := m.lock()
	if e != nil {
		return e
	}
	defer unlock()
	prev, e := m.ReadManifest()
	if errors.Is(e, os.ErrNotExist) {
		if _, je := os.Stat(filepath.Join(m.Dir, "journal.json")); !errors.Is(je, os.ErrNotExist) {
			return errors.New("missing LKG with existing journal")
		}
		s := policy.Empty()
		c, ce := policy.Render(s, m.Options)
		if ce != nil {
			return ce
		}
		prev = Manifest{Compiler: policy.CompilerVersion, Hash: policy.Fingerprint(s, m.Options), Config: c, Snapshot: s}
		if e = m.parse(ctx, c); e != nil {
			return e
		}
		if e = m.Persist(m.ActivePath(), []byte(c)); e != nil {
			return e
		}
		return m.write("lkg.json", prev)
	}
	if e != nil {
		return e
	}
	j, je := m.readJournal()
	if je != nil && !errors.Is(je, os.ErrNotExist) {
		return je
	}
	if je == nil && j.Stage != "terminal" {
		// The previous manifest in the durable journal is authoritative after a crash.
		if j.Previous.Config == "" {
			return errors.New("invalid recovery journal")
		}
		prev = j.Previous
		if e = m.parse(ctx, prev.Config); e != nil {
			return e
		}
		if e = m.Persist(m.ActivePath(), []byte(prev.Config)); e != nil {
			return e
		}
		if !offline {
			if e = m.reloadVerify(ctx, prev.Hash); e != nil {
				return e
			}
		}
		if e = m.write("lkg.json", prev); e != nil {
			return e
		}
		j.Stage = "terminal"
		j.Outcome = "rolled_back"
		return m.write("journal.json", j)
	}
	return m.checkDrift(prev)
}
func (m *Manager) Apply(ctx context.Context, id, revision, expected string, s policy.Snapshot) (string, error) {
	unlock, e := m.lock()
	if e != nil {
		return "rejected", e
	}
	defer unlock()
	if j, je := m.readJournal(); je == nil && j.Stage != "terminal" {
		return "degraded", errors.New("unfinished operation requires recovery")
	} else if je != nil && !errors.Is(je, os.ErrNotExist) {
		return "degraded", je
	}
	prev, e := m.ReadManifest()
	if e != nil {
		return "rejected", e
	}
	if prev.Revision != expected {
		return "rejected", errors.New("applied revision conflict")
	}
	if e = m.checkDrift(prev); e != nil {
		return "degraded", e
	}
	// Ensure Squid is running the old LKG before staging; avoids applying during a launch gap.
	if e = m.Verify(ctx, prev.Hash); e != nil {
		return "rejected", fmt.Errorf("old runtime not ready: %w", e)
	}
	config, e := policy.Render(s, m.Options)
	if e != nil {
		return "rejected", e
	}
	if e = m.parse(ctx, config); e != nil {
		return "rejected", e
	}
	next := Manifest{Compiler: policy.CompilerVersion, Revision: revision, Hash: policy.Fingerprint(s, m.Options), Config: config, Snapshot: s}
	j := Journal{Operation: id, Stage: "promoting", Previous: prev, Candidate: next}
	if e = m.write("journal.json", j); e != nil {
		return "rejected", e
	}
	if e = m.Persist(m.ActivePath(), []byte(config)); e == nil {
		j.Stage = "reloading"
		e = m.write("journal.json", j)
	}
	if e == nil {
		e = m.reloadVerify(ctx, next.Hash)
	}
	if e == nil {
		e = m.write("lkg.json", next)
	}
	if e == nil {
		j.Stage = "terminal"
		j.Outcome = "succeeded"
		e = m.write("journal.json", j)
		if e == nil {
			return "succeeded", nil
		}
	}
	original := e
	rollbackCtx, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancelRollback()
	j.Stage = "rolling_back"
	if e = m.write("journal.json", j); e != nil {
		return "degraded", errors.Join(original, e)
	}
	if e = m.Persist(m.ActivePath(), []byte(prev.Config)); e == nil {
		e = m.reloadVerify(rollbackCtx, prev.Hash)
	}
	if e == nil {
		e = m.write("lkg.json", prev)
	}
	if e != nil {
		return "degraded", errors.Join(original, fmt.Errorf("rollback failed: %w", e))
	}
	j.Stage = "terminal"
	j.Outcome = "rolled_back"
	if e = m.write("journal.json", j); e != nil {
		return "degraded", errors.Join(original, e)
	}
	return "rolled_back", original
}
func (m *Manager) reloadVerify(ctx context.Context, hash string) error {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e := m.Command(c, "-f", m.ActivePath(), "-k", "reconfigure"); e != nil {
		return e
	}
	return m.Verify(ctx, hash)
}

// Verify reads a header on a mandatory-denied loopback destination. It never fetches a user URL.
func (m *Manager) Verify(ctx context.Context, hash string) error { return m.Probe(ctx, hash) }
func (m *Manager) verify(ctx context.Context, hash string) error {
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for {
		d := net.Dialer{Timeout: time.Second}
		// Old revisions keep their original 3128 probe during migration/rollback.
		target := "127.0.0.1:3128"
		if config, e := os.ReadFile(m.ActivePath()); e == nil && strings.Contains(string(config), "http_port 127.0.0.1:3130\n") {
			target = "127.0.0.1:3130"
		}
		c, e := d.DialContext(ctx, "tcp", target)
		if e == nil {
			c.SetDeadline(time.Now().Add(time.Second))
			_, e = io.WriteString(c, "GET http://127.0.0.1/ HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
			if e == nil {
				var r *http.Response
				r, e = http.ReadResponse(bufio.NewReader(c), nil)
				if e == nil {
					if r.StatusCode == 403 && r.Header.Get("X-Squid-Control-Revision") == hash {
						r.Body.Close()
						c.Close()
						return nil
					}
					e = fmt.Errorf("unexpected runtime fingerprint/status: %d", r.StatusCode)
					r.Body.Close()
				}
			}
			c.Close()
		}
		last = e
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (m *Manager) Status() map[string]any {
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	v, e := m.ReadManifest()
	state := "down"
	detail := "Squid not observed"
	var revision any
	if e == nil {
		if v.Revision != "" {
			revision = v.Revision
		}
		if e = m.checkDrift(v); e != nil {
			state = "degraded"
			detail = e.Error()
		} else if e = m.Verify(ctx, v.Hash); e == nil {
			state = "up"
			detail = "Active configuration verified"
		}
	}
	managementState := "up"
	managementDetail := ""
	if j, je := m.readJournal(); je == nil && j.Stage != "terminal" {
		managementState = "degraded"
		managementDetail = "Unfinished operation; recovery required"
	} else if je != nil && !errors.Is(je, os.ErrNotExist) {
		managementState = "degraded"
		managementDetail = "Cannot read runtime journal"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	component := func(s, d string) map[string]any {
		return map[string]any{"state": s, "detail": d, "observed_at": now, "stale": false}
	}
	caps := []string{"http_proxy", "https_connect", "local_reload"}
	if m.Options.ProxyCert != "" {
		caps = append(caps, "tls_proxy_configured")
	}
	return map[string]any{"web": component("up", ""), "runtime": component(managementState, managementDetail), "squid": component(state, detail), "applied_revision": revision, "observed_config_hash": v.Hash, "capabilities": caps}
}
func (m *Manager) Events() ([]map[string]any, error) {
	f, e := os.Open(m.Options.LogPath)
	if errors.Is(e, os.ErrNotExist) {
		return []map[string]any{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	offset := st.Size() - 128*1024
	if offset < 0 {
		offset = 0
	}
	if _, e = f.Seek(offset, 0); e != nil {
		return nil, e
	}
	scanner := bufio.NewScanner(io.LimitReader(f, 128*1024))
	scanner.Buffer(make([]byte, 4096), 16384)
	if offset > 0 {
		scanner.Scan()
	}
	items := []map[string]any{}
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 7 {
			continue
		}
		dest := parts[6]
		if strings.Contains(dest, "://") {
			u, e := url.Parse(dest)
			if e != nil || u.Hostname() == "" {
				dest = "<invalid>"
			} else {
				dest = u.Host
			}
		} else {
			if i := strings.LastIndex(dest, "@"); i >= 0 {
				dest = dest[i+1:]
			}
			if i := strings.IndexAny(dest, "/?#"); i >= 0 {
				dest = dest[:i]
			}
		}
		seconds, e := strconv.ParseFloat(parts[0], 64)
		if e != nil {
			continue
		}
		timestamp := time.UnixMilli(int64(seconds * 1000)).UTC().Format(time.RFC3339Nano)
		duration, _ := strconv.ParseInt(parts[1], 10, 64)
		count, _ := strconv.ParseInt(parts[4], 10, 64)
		codeParts := strings.Split(parts[3], "/")
		var code any
		if len(codeParts) == 2 {
			if n, e := strconv.Atoi(codeParts[1]); e == nil {
				code = n
			}
		}
		mode := "unknown"
		if parts[5] == "CONNECT" {
			mode = "tunnel"
		}
		lineHash := sha256.Sum256(scanner.Bytes())
		items = append(items, map[string]any{"id": hex.EncodeToString(lineHash[:8]), "timestamp": timestamp, "client_id": nil, "source_ip": parts[2], "proxy_result": parts[3], "method": parts[5], "destination": dest, "proxy_http_status": code, "duration_ms": duration, "bytes_observed": count, "tls_mode": mode, "revision_id": nil})
	}
	if len(items) > 100 {
		items = items[len(items)-100:]
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, scanner.Err()
}

// Maintain rotates only fixed known logs. It never interrupts apply or restarts Squid.
func (m *Manager) Maintain(ctx context.Context) error {
	unlock, e := m.lock()
	if errors.Is(e, ErrBusy) {
		return nil
	}
	if e != nil {
		return e
	}
	defer unlock()
	rotate := false
	for _, p := range []string{m.Options.LogPath, filepath.Join(m.Dir, "cache.log")} {
		if st, e := os.Stat(p); e == nil && st.Size() > 10*1024*1024 {
			rotate = true
		}
	}
	if !rotate {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.Command(c, "-f", m.ActivePath(), "-k", "rotate")
}
