// Package runtime manages a local Squid; it never calls Docker or a shell.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"squid-control.local/squid/internal/policy"
)

var ErrBusy = errors.New("runtime busy")
var ErrDrift = errors.New("configuration drift detected")

type Manifest struct {
	Compiler    string          `json:"compiler_version,omitempty"`
	Revision    string          `json:"revision_id"`
	Hash        string          `json:"hash"`
	ImageDigest string          `json:"image_digest,omitempty"`
	Config      string          `json:"config"`
	Snapshot    policy.Snapshot `json:"snapshot"`
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
	// Image is the build-time base image digest (e.g.
	// ubuntu/squid@sha256:...). Recorded into every Manifest the runtime
	// produces so an operator reading lkg.json knows which image produced
	// this LKG without having to cross-reference the revision row.
	Image   string
	Command func(context.Context, ...string) error
	mu      sync.Mutex
	Persist func(string, []byte) error
	Probe   func(context.Context, string) error
	// ParseTimeout bounds `squid -k parse` calls. ReloadTimeout bounds
	// `squid -k reconfigure` acknowledgement (not full reload, which Squid
	// performs asynchronously). VerifyBudget is the worst-case time the verify
	// loop waits for Squid to serve a freshly reconfigured fingerprint after
	// reconfigure returned. All three are overrideable via SC_PARSE_TIMEOUT /
	// SC_RELOAD_TIMEOUT / SC_VERIFY_BUDGET so an operator on slow hardware or
	// under load can widen them without recompiling — see docs/architecture/
	// configuration-lifecycle.md. Upper bound of 5 minutes keeps a misconfigured
	// value from stalling the panel indefinitely.
	ParseTimeout  time.Duration
	ReloadTimeout time.Duration
	VerifyBudget  time.Duration
}

func New(dir, binary, logPath, cert, key, image string) *Manager {
	m := &Manager{
		Dir:           dir,
		Binary:        binary,
		Image:         image,
		Options:       policy.Options{StateDir: dir, LogPath: logPath, ProxyCert: cert, ProxyKey: key},
		ParseTimeout:  parseTimeoutDefault,
		ReloadTimeout: reloadTimeoutDefault,
		VerifyBudget:  verifyBudgetDefault,
	}
	// Env override: durations like "30s", "2m". Reject >5m so a typo cannot
	// stall Apply indefinitely; reject <=0 so the verify loop does not busy-spin.
	for _, ov := range []struct {
		env string
		dst *time.Duration
	}{
		{"SC_PARSE_TIMEOUT", &m.ParseTimeout},
		{"SC_RELOAD_TIMEOUT", &m.ReloadTimeout},
		{"SC_VERIFY_BUDGET", &m.VerifyBudget},
	} {
		if v := os.Getenv(ov.env); v != "" {
			if d, e := time.ParseDuration(v); e == nil && d > 0 && d <= 5*time.Minute {
				*ov.dst = d
			}
		}
	}
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
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".stage-")
	if e != nil {
		return e
	}
	name := f.Name()
	// Track whether the staged temp has been promoted via rename. The deferred
	// cleanup used to call os.Remove(name) unconditionally, which silently
	// errored after a successful rename — harmless, but confusing when reading
	// the code and noisy if anyone ever wires errno-level logging.
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(name)
		}
	}()
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
	renamed = true
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
		// Once we hit the cap, mark that further output was dropped. The error
		// message is preserved with redaction by the caller, but at least the
		// truncation is visible in operator logs instead of silently dropping.
		if b.Len() == 16384 {
			b.Buffer.WriteString("\n...truncated...\n")
		}
	}
	return n, nil
}
func (m *Manager) parse(ctx context.Context, config string) error {
	p := filepath.Join(m.Dir, "candidate.conf")
	if e := m.Persist(p, []byte(config)); e != nil {
		return e
	}
	defer os.Remove(p)
	c, cancel := context.WithTimeout(ctx, m.ParseTimeout)
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
		// Allow safe re-entry when a first_run marker exists between
		// Persist(active.conf) and write(lkg.json): the recovery logic is the
		// same idempotent init either way.
		if jb, je2 := os.ReadFile(filepath.Join(m.Dir, "journal.json")); je2 == nil {
			var existing Journal
			if jerr := json.Unmarshal(jb, &existing); jerr == nil && existing.Stage != "first_run" {
				return errors.New("missing LKG with existing journal")
			}
		}
		s := policy.Empty()
		c, ce := policy.Render(s, m.Options)
		if ce != nil {
			return ce
		}
		prev = Manifest{Compiler: policy.CompilerVersion, ImageDigest: m.Image, Hash: policy.Fingerprint(s, m.Options), Config: c, Snapshot: s}
		if e = m.parse(ctx, c); e != nil {
			return e
		}
		// Durable first_run marker records that init is in progress; a crash
		// before lkg.json becomes a clean re-run of this same branch.
		if e = m.write("journal.json", Journal{Stage: "first_run", Candidate: prev}); e != nil {
			return e
		}
		if e = m.Persist(m.ActivePath(), []byte(c)); e != nil {
			return e
		}
		if e = m.write("lkg.json", prev); e != nil {
			return e
		}
		return m.write("journal.json", Journal{Stage: "terminal", Outcome: "initialised", Candidate: prev})
	}
	if e != nil {
		return e
	}
	j, je := m.readJournal()
	if je != nil && !errors.Is(je, os.ErrNotExist) {
		return je
	}
	if je == nil && j.Stage == "first_run" {
		// LKG was persisted but the final init marker was not. This is still
		// initialisation, not an apply with a Previous manifest to roll back to.
		if j.Candidate.Hash != prev.Hash || j.Candidate.Config != prev.Config {
			return errors.New("initialisation journal does not match LKG")
		}
		if e = m.checkDrift(prev); e != nil {
			return e
		}
		j.Stage, j.Outcome = "terminal", "initialised"
		return m.write("journal.json", j)
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
				// Keep the intent durable. A running Squid may still serve the
				// candidate; retry recovery rather than claiming a verified rollback.
				return fmt.Errorf("recovery reload not verified: %w", e)
			}
		}
		if e = m.write("lkg.json", prev); e != nil {
			return e
		}
		if offline {
			// squid-start restores disk state before exec. The serving process
			// must subsequently verify it before marking recovery terminal.
			return nil
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
	next := Manifest{Compiler: policy.CompilerVersion, ImageDigest: m.Image, Revision: revision, Hash: policy.Fingerprint(s, m.Options), Config: config, Snapshot: s}
	j := Journal{Operation: id, Stage: "promoting", Previous: prev, Candidate: next}
	if e = m.write("journal.json", j); e != nil {
		return "rejected", e
	}
	lkgPromoted := false
	if e = m.Persist(m.ActivePath(), []byte(config)); e == nil {
		j.Stage = "reloading"
		e = m.write("journal.json", j)
	}
	if e == nil {
		e = m.reloadVerify(ctx, next.Hash)
	}
	if e == nil {
		e = m.write("lkg.json", next)
		if e == nil {
			lkgPromoted = true
		}
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
	if lkgPromoted {
		// We are about to roll back even though `next` was already promoted to
		// active.conf, reloadVerify succeeded and lkg.json was written. The
		// terminal-journal marker write failed; on next Recover the journal
		// Stage != "terminal" branch will restore prev and overwrite the LKG.
		// This is a legitimate operational surprise — surface it loudly so an
		// operator notices that a momentarily-working apply is being reverted.
		slog.Error("successful apply will be rolled back because terminal journal write failed; recovery will finalize prev on next start", "operation_id", id, "error", e)
	}
	rollbackCtx, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancelRollback()
	j.Stage = "rolling_back"
	if e = m.write("journal.json", j); e != nil {
		// Avoid triple-wrapping the original "configuration output withheld"
		// message; surface a single rolled-back summary plus the rollback step
		// error without repeating the redaction phrase.
		return "degraded", fmt.Errorf("apply failed: %w; rollback journal write failed: %v", original, e)
	}
	if e = m.Persist(m.ActivePath(), []byte(prev.Config)); e == nil {
		e = m.reloadVerify(rollbackCtx, prev.Hash)
	}
	if e == nil {
		e = m.write("lkg.json", prev)
	}
	if e != nil {
		return "degraded", fmt.Errorf("apply failed: %w; rollback failed: %v", original, e)
	}
	j.Stage = "terminal"
	j.Outcome = "rolled_back"
	if e = m.write("journal.json", j); e != nil {
		return "degraded", fmt.Errorf("apply failed: %w; terminal journal write failed: %v", original, e)
	}
	return "rolled_back", original
}
func (m *Manager) reloadVerify(ctx context.Context, hash string) error {
	c, cancel := context.WithTimeout(ctx, m.ReloadTimeout)
	defer cancel()
	if e := m.Command(c, "-f", m.ActivePath(), "-k", "reconfigure"); e != nil {
		return e
	}
	return m.Verify(ctx, hash)
}

// Timeouts default to the values promised by docs/architecture/
// configuration-lifecycle.md (parse 15 s, reload acknowledgement 10 s, verify
// 30 s) and can be widened via SC_PARSE_TIMEOUT / SC_RELOAD_TIMEOUT /
// SC_VERIFY_BUDGET env vars. They are budgets, not SLAs.
const (
	parseTimeoutDefault  = 15 * time.Second
	reloadTimeoutDefault = 10 * time.Second
	verifyBudgetDefault  = 30 * time.Second
)

// Verify reads a header on a mandatory-denied loopback destination. It never fetches a user URL.
func (m *Manager) Verify(ctx context.Context, hash string) error { return m.Probe(ctx, hash) }

// probeTargetFor extracts the diagnostic http_port listener that the policy
// compiler emits for the canary probe. The directive is always written by
// policy.Render as a literal line; comments are skipped so a "# http_port
// 127.0.0.1:3130" annotation cannot shadow the real target.
func probeTargetFor(config string) (string, bool) {
	for _, raw := range strings.Split(config, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "http_port" && strings.HasPrefix(fields[1], "127.0.0.1:3130") {
			return "127.0.0.1:3130", true
		}
	}
	return "", false
}

func (m *Manager) verify(ctx context.Context, hash string) error {
	deadline := time.Now().Add(m.VerifyBudget)
	var last error
	for {
		d := net.Dialer{Timeout: time.Second}
		// Default probe target is the canonical 3130 listener emitted by
		// policy.Render (`http_port 127.0.0.1:3130`). probeTargetFor matches
		// that literal structurally — a `# http_port 127.0.0.1:3130` comment
		// cannot shadow it. The fallback only matters during bootstrap when
		// active.conf has been written but lkg.json hasn't, or after a corrupt
		// active.conf. Always probing 3130 means a misconfigured active.conf
		// without the probe listener fails fast (no listener) instead of
		// accidentally hitting the user's http_port (e.g. 3128) which would
		// return a misleading 403 with the canary header but without the
		// probe-only localport ACL.
		target := "127.0.0.1:3130"
		if manifest, e := m.ReadManifest(); e == nil {
			if probe, ok := probeTargetFor(manifest.Config); ok {
				target = probe
			}
		} else if cfg, e := os.ReadFile(m.ActivePath()); e == nil {
			// Fallback: only used when lkg.json is missing (bootstrap). Match
			// the listener directive literally, not inside a comment.
			if probe, ok := probeTargetFor(string(cfg)); ok {
				target = probe
			}
		}
		c, e := d.DialContext(ctx, "tcp", target)
		if e == nil {
			c.SetDeadline(time.Now().Add(time.Second))
			_, e = io.WriteString(c, "GET http://127.0.0.1/ HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
			if e == nil {
				var r *http.Response
				r, e = http.ReadResponse(bufio.NewReader(io.LimitReader(c, 64*1024)), nil)
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
	// Use a non-blocking TryLock so a long-running Apply does not stall status
	// reads. When Apply owns the lock the in-process mutation is mid-flight, so
	// active.conf may transiently differ from lkg.json — surfacing that as
	// "degraded" would be a false positive. Mark the runtime as "up,
	// operation in progress" instead.
	if m.mu.TryLock() {
		defer m.mu.Unlock()
	} else {
		return m.statusInFlight()
	}
	return m.statusLocked()
}
func (m *Manager) statusInFlight() map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	component := func(s, d string) map[string]any {
		return map[string]any{"state": s, "detail": d, "observed_at": now, "stale": false}
	}
	v, e := m.ReadManifest()
	var revision any
	if e == nil && v.Revision != "" {
		revision = v.Revision
	}
	caps := []string{"http_proxy", "https_connect", "local_reload"}
	if m.Options.ProxyCert != "" {
		caps = append(caps, "tls_proxy_configured")
	}
	return map[string]any{
		"web":                  component("up", ""),
		"runtime":              component("up", "operation in progress"),
		"squid":                component("up", "operation in progress"),
		"applied_revision":     revision,
		"observed_config_hash": v.Hash,
		"capabilities":         caps,
	}
}
func (m *Manager) statusLocked() map[string]any {
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

// Maintain rotates only fixed known logs and prunes historical state. It never
// interrupts apply or restarts Squid.
func (m *Manager) Maintain(ctx context.Context, prune func() error) error {
	unlock, e := m.lock()
	if errors.Is(e, ErrBusy) {
		// Apply in flight: defer log rotation to the next tick, but pruning the
		// SQLite history is independent of the runtime lock and can still run.
		if prune != nil {
			if pe := prune(); pe != nil {
				slog.Warn("store prune failed", "error", pe)
			}
		}
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
	if rotate {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if e = m.Command(c, "-f", m.ActivePath(), "-k", "rotate"); e != nil {
			slog.Warn("log rotation failed", "error", e)
		}
	}
	if prune != nil {
		if pe := prune(); pe != nil {
			slog.Warn("store prune failed", "error", pe)
		}
	}
	return nil
}
