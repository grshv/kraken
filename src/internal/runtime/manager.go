// Package runtime manages a local Squid; it never calls Docker or a shell.
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"kraken.local/kraken/internal/policy"
)

var ErrBusy = errors.New("runtime busy")
var ErrDrift = errors.New("configuration drift detected")

// RotateHardCap is the per-log size that forces rotation regardless of the
// collector gate: a Squid rotate action rotates both logs at once, so letting
// either grow unbounded risks filling the disk. At this size rotation loses
// tail access-log records the collector has not read, as before the gate.
const RotateHardCap = 256 << 20

// rotateThreshold is the size above which a log is a candidate for rotation;
// the gate decides whether rotation proceeds now or waits for the collector.
const rotateThreshold = 10 * 1024 * 1024

// ParseError reports the rendered-config line Squid rejected; Squid output is withheld.
type ParseError struct{ Line int } // 1-based line of the rendered config, 0 if unknown

func (e *ParseError) Error() string {
	if e == nil || e.Line == 0 {
		return "squid rejected configuration (output withheld)"
	}
	return fmt.Sprintf("squid rejected configuration at line %d (output withheld)", e.Line)
}

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
	metricsMu      sync.Mutex
	metricsLast    Metrics
	metricsAttempt time.Time

	Dir     string
	AppDir  string
	Binary  string
	Options policy.Options
	// Image is the build-time base image digest (e.g.
	// ubuntu/squid@sha256:...). Recorded into every Manifest the runtime
	// produces so an operator reading lkg.json knows which image produced
	// this LKG without having to cross-reference the revision row.
	Image   string
	Command func(context.Context, ...string) error
	// Reload triggers a Squid reconfigure (action "reconfigure") or log
	// rotation (action "rotate") over the loopback manager listener. The
	// default implementation issues a cache_object request authenticated by
	// the manager token in <AppDir>/manager-token, which Squid reads from
	// the included <StateDir>/manager-auth.conf. Cross-UID signalling via
	// `-k reconfigure/rotate` would require a shared UID, so cachemgr over
	// TCP is used instead.
	Reload func(context.Context, string) error
	// RotateGate gates log rotation in Maintain: when set, Maintain rotates only
	// if RotateGate(size) returns true (in addition to the >10 MiB threshold).
	// nil means rotation is always allowed when the size threshold is crossed.
	RotateGate func(size int64) bool
	mu         sync.Mutex
	Persist    func(string, []byte) error
	Probe      func(context.Context, string) error
	// ParseTimeout bounds `squid -k parse` calls. ReloadTimeout bounds
	// `squid -k reconfigure` acknowledgement (not full reload, which Squid
	// performs asynchronously). VerifyBudget is the worst-case time the verify
	// loop waits for Squid to serve a freshly reconfigured fingerprint after
	// reconfigure returned. All three are overrideable via KRAKEN_PARSE_TIMEOUT /
	// KRAKEN_RELOAD_TIMEOUT / KRAKEN_VERIFY_BUDGET so an operator on slow hardware or
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
		AppDir:        envString("KRAKEN_APP_DIR", "/data/app"),
		Binary:        binary,
		Image:         image,
		Options:       policy.Options{StateDir: dir, SquidDir: envString("KRAKEN_SQUID_DIR", "/data/squid"), LogPath: logPath, ProxyCert: cert, ProxyKey: key, ProxyAccounts: envString("KRAKEN_PROXY_ACCOUNTS", "/data/runtime/proxy-accounts.json")},
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
		{"KRAKEN_PARSE_TIMEOUT", &m.ParseTimeout},
		{"KRAKEN_RELOAD_TIMEOUT", &m.ReloadTimeout},
		{"KRAKEN_VERIFY_BUDGET", &m.VerifyBudget},
	} {
		if v := os.Getenv(ov.env); v != "" {
			if d, e := time.ParseDuration(v); e == nil && d > 0 && d <= 5*time.Minute {
				*ov.dst = d
			}
		}
	}
	// Every StateDir file is read by Squid or squid-start (ADR 0015).
	m.Persist = func(path string, b []byte) error { return AtomicMode(path, b, 0640) }
	m.Command = m.command
	m.Reload = m.defaultReload
	m.Probe = m.verify
	return m
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

// Atomic writes a private (0600) file atomically.
func Atomic(path string, b []byte) error { return AtomicMode(path, b, 0600) }

// AtomicMode writes b atomically with the given file mode.
func AtomicMode(path string, b []byte, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".stage-")
	if e != nil {
		return e
	}
	name := f.Name()
	renamed := false
	defer func() {
		if !renamed {
			os.Remove(name)
		}
	}()
	if e = f.Chmod(mode); e != nil {
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

// ensureManagerAuth persists the manager token in <AppDir>/manager-token and
// the included auth file <StateDir>/manager-auth.conf. The token is 32 random
// bytes (hex) and never appears in Render, Manifest, errors or logs. The
// include line is rewritten only when its content changed.
func (m *Manager) ensureManagerAuth() error {
	token, e := m.retrieveToken()
	if e != nil {
		return e
	}
	want := "cachemgr_passwd " + token + " reconfigure rotate\n"
	path := filepath.Join(m.Dir, "manager-auth.conf")
	if b, e := os.ReadFile(path); e == nil && string(b) == want {
		return nil
	}
	return m.Persist(path, []byte(want))
}

var managerToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (m *Manager) retrieveToken() (string, error) {
	path := filepath.Join(m.AppDir, "manager-token")
	if b, e := os.ReadFile(path); e == nil {
		if token := strings.TrimSpace(string(b)); managerToken.MatchString(token) {
			return token, nil
		}
		return "", errors.New("manager token file is malformed")
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", e
	}
	token := hex.EncodeToString(raw[:])
	if e := os.MkdirAll(m.AppDir, 0700); e != nil {
		return "", e
	}
	if e := AtomicMode(path, []byte(token), 0600); e != nil {
		return "", e
	}
	return token, nil
}

// defaultReload issues a loopback cachemgr request authenticated by the manager
// token; Squid reloads/rotates based on the action path. Errors never include
// the token: a malformed response or non-200 status is reported generically.
func (m *Manager) defaultReload(ctx context.Context, action string) error {
	token, e := m.retrieveToken()
	if e != nil {
		return e
	}
	d := net.Dialer{Timeout: m.ReloadTimeout}
	c, e := d.DialContext(ctx, "tcp", "127.0.0.1:3131")
	if e != nil {
		return fmt.Errorf("squid manager unreachable: %w", e)
	}
	defer c.Close()
	deadline := time.Now().Add(m.ReloadTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	c.SetDeadline(deadline)
	req := "GET cache_object://127.0.0.1/" + action + "@" + token + " HTTP/1.0\r\nHost: 127.0.0.1\r\n\r\n"
	if _, e = io.WriteString(c, req); e != nil {
		return fmt.Errorf("squid manager request failed: %w", e)
	}
	r, e := http.ReadResponse(bufio.NewReader(io.LimitReader(c, 64*1024)), nil)
	if e != nil {
		return fmt.Errorf("squid manager response failed: %w", e)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("squid manager %s returned status %d", action, r.StatusCode)
	}
	return nil
}

// SquidReady reports whether the on-disk state is consistent enough for
// squid-start to exec the binary without recovery. It only reads files; absence
// of lkg.json is a normal pre-init state, not an error.
func (m *Manager) SquidReady() (bool, error) {
	lkg, e := m.ReadManifest()
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	active, e := os.ReadFile(m.ActivePath())
	if e != nil {
		return false, e
	}
	if string(active) != lkg.Config {
		return false, nil
	}
	j, je := m.readJournal()
	if errors.Is(je, os.ErrNotExist) {
		return true, nil
	}
	if je != nil {
		return false, je
	}
	if j.Stage == "terminal" {
		return true, nil
	}
	if j.Previous.Hash == lkg.Hash {
		return true, nil
	}
	return false, nil
}

// write persists JSON state; squid-start reads lkg.json and journal.json.
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
		if hasParseFlag(args) && ctx.Err() == nil {
			return parseErrorFromOutput(out.String())
		}
		return fmt.Errorf("squid control failed: %w (configuration output withheld to avoid leaking credentials)", e)
	}
	return nil
}

// hasParseFlag reports whether argv represents the `squid -k parse` invocation
// the runtime uses for validation, so a failing parse surfaces only the line
// number Squid reports and never the redacted bungled-config body.
func hasParseFlag(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-k" && args[i+1] == "parse" {
			return true
		}
	}
	return false
}

var parseLineRE = regexp.MustCompile(`line (\d+):`)

// parseErrorFromOutput extracts the last `line N:` reference from captured
// Squid output into a ParseError. Squid prints `FATAL: Bungled <path> line N:
// <body>`; the body is never placed in the returned error.
func parseErrorFromOutput(output string) *ParseError {
	matches := parseLineRE.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return &ParseError{Line: 0}
	}
	if n, e := strconv.Atoi(matches[len(matches)-1][1]); e == nil {
		return &ParseError{Line: n}
	}
	return &ParseError{Line: 0}
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
	if e := m.ensureManagerAuth(); e != nil {
		return e
	}
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
	if e = m.ensureManagerAuth(); e != nil {
		return "rejected", e
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
	if e := m.Reload(c, "reconfigure"); e != nil {
		return e
	}
	return m.Verify(ctx, hash)
}

// Timeouts default to the values promised by docs/architecture/
// configuration-lifecycle.md (parse 15 s, reload acknowledgement 10 s, verify
// 30 s) and can be widened via KRAKEN_PARSE_TIMEOUT / KRAKEN_RELOAD_TIMEOUT /
// KRAKEN_VERIFY_BUDGET env vars. They are budgets, not SLAs.
const (
	parseTimeoutDefault  = 15 * time.Second
	reloadTimeoutDefault = 10 * time.Second
	verifyBudgetDefault  = 30 * time.Second
)

// Verify reads a header on a mandatory-denied loopback destination. It never fetches a user URL.
func (m *Manager) Verify(ctx context.Context, hash string) error { return m.Probe(ctx, hash) }

func (m *Manager) verify(ctx context.Context, hash string) error {
	deadline := time.Now().Add(m.VerifyBudget)
	var last error
	for {
		d := net.Dialer{Timeout: time.Second}
		// policy.Render always emits the probe-only listener on 127.0.0.1:3130.
		c, e := d.DialContext(ctx, "tcp", "127.0.0.1:3130")
		if e == nil {
			c.SetDeadline(time.Now().Add(time.Second))
			_, e = io.WriteString(c, "GET http://127.0.0.1/ HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
			if e == nil {
				var r *http.Response
				r, e = http.ReadResponse(bufio.NewReader(io.LimitReader(c, 64*1024)), nil)
				if e == nil {
					if r.StatusCode == 403 && r.Header.Get("X-Kraken-Revision") == hash {
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
	if e := m.ensureManagerAuth(); e != nil {
		return e
	}
	// Squid rotates both logs on a single "rotate" action, so the gate must
	// cover cache.log too: otherwise a big cache.log would tear access.log
	// away from the collector. Rotation proceeds when some log exceeds the
	// threshold and the gate allows it (collector caught up), or when any log
	// hits the hard cap (bounded growth at the cost of tail records).
	var accessSize int64
	overThreshold := false
	hardCap := false
	for _, p := range []string{m.Options.LogPath, filepath.Join(m.Options.SquidDir, "cache.log")} {
		st, e := os.Stat(p)
		if e != nil || st.Size() <= rotateThreshold {
			continue
		}
		overThreshold = true
		if p == m.Options.LogPath {
			accessSize = st.Size()
		}
		if st.Size() >= RotateHardCap {
			hardCap = true
		}
	}
	rotate := hardCap || (overThreshold && (m.RotateGate == nil || m.RotateGate(accessSize)))
	if rotate {
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if e = m.Reload(c, "rotate"); e != nil {
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
