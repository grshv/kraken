//go:build integration

// Package integration runs the real-image smoke, network, APT and ops suites
// against a built kraken container. Each file mirrors the original Python
// scenario step for step; the harness here holds the shared Docker/HTTP/cert
// plumbing so the test files stay focused on assertions.
package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"bufio"
	"kraken.local/kraken/internal/httpapi"
	"kraken.local/kraken/internal/policy"
	"kraken.local/kraken/internal/runtime"
)

const (
	pythonImage = "python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916"
	controlUser = "10013:10013"
)

// krakenSecOpts are the Docker security options every kraken container runs
// with, matching deploy/compose.yaml: all capabilities dropped except
// SETUID/SETGID/KILL (needed to switch UIDs via setpriv and signal Squid)
// plus no-new-privileges (ADR 0019).
var krakenSecOpts = []string{"--cap-drop", "ALL", "--cap-add", "SETUID", "--cap-add", "SETGID", "--cap-add", "KILL", "--security-opt", "no-new-privileges"}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// testImage is the kraken image under test (default kraken:dev).
func testImage() string { return env("KRAKEN_TEST_IMAGE", "kraken:dev") }

// nextImage is the rebuilt-from-tree image for the ops image-update section.
func nextImage() string { return env("KRAKEN_TEST_IMAGE_NEXT", "kraken:ops-next") }

// randHex returns n bytes of crypto-random data as hex (2n chars).
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// randName prefixes a short random suffix to keep Docker resources unique.
func randName(prefix string) string { return prefix + randHex(4) }

// randID is an opaque identifier for policies/clients (matches [a-zA-Z0-9-]{1,64}).
func randID() string { return randHex(16) }

// randKey is an Idempotency-Key (16–128 chars).
func randKey() string { return randHex(16) }

// randPassword mimics secrets.token_urlsafe plus a few special characters so
// proxy-auth and CLI reset exercise non-alphanumeric bytes.
func randPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b) + " +%"
}

// freeHostPort returns an unused localhost TCP port. The caller must bind it
// before relying on exclusivity; the listener is closed before returning.
func freeHostPort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ""
	}
	defer l.Close()
	return fmt.Sprintf("%d", l.Addr().(*net.TCPAddr).Port)
}

// portAllocError reports whether a docker run failure is a host-port bind
// conflict ("port is already allocated" / "address already in use") that a
// fresh port would fix.
func portAllocError(output string) bool {
	low := strings.ToLower(output)
	return strings.Contains(low, "port is already allocated") ||
		strings.Contains(low, "address already in use") ||
		strings.Contains(low, "bind: address already in use")
}

// runPortMapped runs `docker run -d` with a published host port. build receives
// a candidate host port and returns the full arg list (starting with "run"). On
// a port-allocation conflict the helper retries with a fresh free port, up to 5
// attempts. It returns the host port actually bound. Centralising the retry
// here keeps every parallel test safe against the freePort→docker-run race.
func runPortMapped(t *testing.T, build func(hostPort string) []string) string {
	t.Helper()
	var lastOut string
	for attempt := 0; attempt < 5; attempt++ {
		port := freeHostPort()
		if port == "" {
			t.Fatalf("free port: no port available")
		}
		args := build(port)
		out, err := dockerErr(args...)
		if err == nil {
			return port
		}
		lastOut = out
		if !portAllocError(out) {
			t.Fatalf("docker %s: %v\n%s", args[0], err, tail(out, 3000))
		}
		// Port conflict: retry with a new free port.
	}
	t.Fatalf("could not allocate host port after 5 attempts:\n%s", tail(lastOut, 1000))
	return ""
}

// resources tracks disposable Docker containers/networks/volumes so t.Cleanup
// can remove them all even on failure. Order matches the Python finally blocks:
// containers first (reverse), then networks, then volumes.
type resources struct {
	containers []string
	networks   []string
	volumes    []string
}

func (r *resources) addContainer(name string) string {
	r.containers = append(r.containers, name)
	return name
}
func (r *resources) addNetwork(name string) string {
	r.networks = append(r.networks, name)
	return name
}
func (r *resources) addVolume(name string) string {
	r.volumes = append(r.volumes, name)
	return name
}
func (r *resources) track(t *testing.T) { t.Cleanup(func() { r.cleanup(t) }) }

// removeContainer drops a name from the tracked list after it was re-created.
func (r *resources) removeContainer(name string) {
	for i, c := range r.containers {
		if c == name {
			r.containers = append(r.containers[:i], r.containers[i+1:]...)
			return
		}
	}
}

func (r *resources) cleanup(t *testing.T) {
	for i := len(r.containers) - 1; i >= 0; i-- {
		runDocker(t, "rm", "-f", "-v", r.containers[i])
	}
	for _, n := range r.networks {
		runDocker(t, "network", "rm", n)
	}
	for _, v := range r.volumes {
		runDocker(t, "volume", "rm", v)
	}
}

// runDocker executes docker with args, returning trimmed stdout. A non-zero
// exit is a fatal test failure so callers do not need to check every call.
func runDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := dockerErr(args...)
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, tail(out, 3000))
	}
	return strings.TrimSpace(out)
}

// docker runs docker, failing the test on error. Intended for the test goroutine.
func docker(t *testing.T, args ...string) string { return runDocker(t, args...) }

// dockerErr runs docker and returns (combined stderr+stdout, error).
func dockerErr(args ...string) (string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

// dockerIn runs docker feeding input to stdin.
func dockerIn(t *testing.T, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], err, tail(stdout.String()+stderr.String(), 3000))
	}
	return strings.TrimSpace(stdout.String())
}

// dockerOK returns (stdout, true) when the command succeeds. Used for negative
// probes (e.g. "proxy cannot read panel.key" expects failure).
func dockerOK(t *testing.T, args ...string) (string, bool) {
	t.Helper()
	out, err := dockerErr(args...)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// dockerExecStdin runs `docker exec -i` feeding input; returns (stdout, stderr, err).
func dockerExecStdin(container, input string, args ...string) (string, string, error) {
	full := append([]string{"exec", "-i", container}, args...)
	cmd := exec.Command("docker", full...)
	// Use an os.Pipe so docker's stdin stays open until we close it after the
	// process has had time to read. A strings.NewReader closes immediately on
	// EOF, and docker exec -i can race past the data to a closed-pipe read.
	r, w, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	cmd.Stdin = r
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		r.Close()
		w.Close()
		return "", "", err
	}
	// Write input in a goroutine so the docker process can read while we wait
	// for it to finish; close the write end to signal EOF.
	go func() {
		w.Write([]byte(input))
		w.Close()
	}()
	r.Close() // parent doesn't read from r
	err = cmd.Wait()
	return stdout.String(), stderr.String(), err
}

// dockerExec runs `docker exec` (no stdin); returns (stdout, err).
func dockerExec(container string, args ...string) (string, error) {
	full := append([]string{"exec", container}, args...)
	return dockerErr(full...)
}

// dockerExecCombined runs `docker exec` (no stdin) and returns combined
// stdout+stderr plus err, matching Python's capture_output with text=True
// followed by stdout+stderr concatenation.
func dockerExecCombined(container string, args ...string) (string, error) {
	full := append([]string{"exec", container}, args...)
	cmd := exec.Command("docker", full...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	return combined.String(), err
}

// dockerExecUser runs `docker exec --user` and returns (stdout, err).
func dockerExecUser(container, user string, args ...string) (string, error) {
	full := append([]string{"exec", "--user", user, container}, args...)
	return dockerErr(full...)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// waitFor polls fn every 200ms until it returns no error or the deadline
// expires. Mirrors the Python wait_for helper.
func waitFor(t *testing.T, seconds int, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var last error
	for {
		if err := fn(); err == nil {
			return
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %ds: %v", seconds, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitForResult polls fn until it returns (value, nil); returns the value.
func waitForResult[T any](t *testing.T, seconds int, fn func() (T, error)) T {
	t.Helper()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var last error
	for {
		v, err := fn()
		if err == nil {
			return v
		}
		last = err
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %ds: %v", seconds, last)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Panel is an HTTPS panel client: pinned CA, cookie jar, CSRF token, optional
// password for transparent re-auth after a full-disk 401.
type Panel struct {
	origin   string
	client   *http.Client
	csrf     string
	password string
	username string
}

func newPanel(origin string, caPath string, password string) *Panel {
	pool := x509.NewCertPool()
	if b, err := os.ReadFile(caPath); err == nil {
		pool.AppendCertsFromPEM(b)
	}
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	return &Panel{
		origin:   origin,
		client:   &http.Client{Jar: jar, Transport: tr, Timeout: 25 * time.Second},
		password: password,
		username: "admin",
	}
}

// api issues a panel API call and returns the parsed JSON body (or nil for 204)
// together with the HTTP status. Callers assert on the status. A transport
// error (panel restarting, transient TLS EOF) surfaces as status 0 with a nil
// body rather than a panic so callers wrapped in waitFor can retry.
func (p *Panel) api(method, path string, body any, key string) (map[string]any, int) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, p.origin+"/api/v1"+path, reader)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", p.origin)
	req.Header.Set("X-CSRF-Token", p.csrf)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	// A full disk may prevent the panel from persisting session state, so a
	// previously valid cookie can surface as 401. Re-authenticate once and
	// retry the original request, mirroring the Python Panel.api.
	if resp.StatusCode == 401 && p.password != "" {
		p.loginRaw(p.password)
		req.Header.Set("X-CSRF-Token", p.csrf)
		resp2, err := p.client.Do(req.Clone(req.Context()))
		if err != nil {
			panic(err)
		}
		defer resp2.Body.Close()
		data, _ = io.ReadAll(resp2.Body)
		if resp2.StatusCode == 204 || len(data) == 0 {
			return nil, resp2.StatusCode
		}
		var parsed map[string]any
		if json.Unmarshal(data, &parsed) != nil {
			return map[string]any{"raw": string(data)}, resp2.StatusCode
		}
		return parsed, resp2.StatusCode
	}
	if resp.StatusCode == 204 || len(data) == 0 {
		return nil, resp.StatusCode
	}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) != nil {
		return map[string]any{"raw": string(data)}, resp.StatusCode
	}
	return parsed, resp.StatusCode
}

// apiOK expects a 2xx response and returns the body, failing the test otherwise.
// A nil t makes apiOK non-fatal (returns nil on any non-2xx or transport error)
// so internal helpers that may retry can call it without aborting the test.
func (p *Panel) apiOK(t *testing.T, method, path string, body any, key string) map[string]any {
	if t == nil {
		r, status := p.api(method, path, body, key)
		if status < 200 || status >= 300 {
			return nil
		}
		return r
	}
	t.Helper()
	r, status := p.api(method, path, body, key)
	if status < 200 || status >= 300 {
		t.Fatalf("%s %s: expected 2xx, got %d: %v", method, path, status, r)
	}
	return r
}

// refreshCSRF fetches the pre-auth CSRF token without going through api (so a
// 401 here cannot recurse into login). Transient connection errors (panel
// still starting) return an error instead of panicking, so login()'s waitFor
// can retry.
func (p *Panel) refreshCSRF() error {
	req, _ := http.NewRequest("GET", p.origin+"/api/v1/session", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var s map[string]any
	json.NewDecoder(resp.Body).Decode(&s)
	if tok, ok := s["csrf_token"].(string); ok {
		p.csrf = tok
	}
	return nil
}

func (p *Panel) login(t *testing.T, password string) map[string]any {
	t.Helper()
	p.password = password
	waitFor(t, 90, func() error {
		if err := p.refreshCSRF(); err != nil {
			return err
		}
		if p.csrf == "" {
			return fmt.Errorf("no csrf")
		}
		return nil
	})
	return p.loginRaw(password)
}

// loginRaw posts credentials directly (not via api) so login cannot recurse on
// a 401. Used by both login() and the full-disk re-auth path. Returns the
// parsed session body (nil on transport error).
func (p *Panel) loginRaw(password string) map[string]any {
	p.password = password
	body, _ := json.Marshal(map[string]string{"username": p.username, "password": password})
	req, _ := http.NewRequest("POST", p.origin+"/api/v1/session", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", p.origin)
	req.Header.Set("X-CSRF-Token", p.csrf)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var s map[string]any
	json.NewDecoder(resp.Body).Decode(&s)
	if tok, ok := s["csrf_token"].(string); ok {
		p.csrf = tok
	}
	return s
}

func (p *Panel) status() map[string]any {
	r, _ := p.api("GET", "/status", nil, "")
	return r
}

func (p *Panel) waitUp(t *testing.T, seconds int) map[string]any {
	t.Helper()
	return waitForResult(t, seconds, func() (map[string]any, error) {
		s := p.status()
		if s == nil {
			return nil, fmt.Errorf("no status (panel not reachable)")
		}
		if squid, ok := s["squid"].(map[string]any); ok && squid["state"] == "up" {
			return s, nil
		}
		return nil, fmt.Errorf("squid not up: %v", s["squid"])
	})
}

func (p *Panel) draft() map[string]any {
	return p.apiOK(nil, "GET", "/configuration/draft", nil, "")
}

// draftOK retries GET /configuration/draft until it returns a 2xx body. The
// panel may be briefly unreachable right after a restart; callers that need a
// draft right away use this instead of draft(). A nil t keeps it non-fatal
// (returns nil on timeout) for use inside waitFor callbacks.
func (p *Panel) draftOK(t *testing.T) map[string]any {
	if t == nil {
		r := p.draft()
		if r == nil {
			return nil
		}
		return r
	}
	t.Helper()
	return waitForResult(t, 60, func() (map[string]any, error) {
		r := p.draft()
		if r == nil {
			return nil, errf("draft unavailable")
		}
		return r, nil
	})
}

// draftVersion returns the current draft version as an int, retrying briefly
// for transient unavailability. JSON unmarshal into map[string]any yields
// float64 for numbers, so a bare .(int) would panic.
func (p *Panel) draftVersion() int {
	deadline := time.Now().Add(60 * time.Second)
	for {
		r := p.draft()
		if r != nil {
			if v, ok := r["version"].(float64); ok {
				return int(v)
			}
		}
		if time.Now().After(deadline) {
			panic("draft version: panel unreachable")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (p *Panel) putDraft(t *testing.T, snap policy.Snapshot, expected int) httpapi.Draft {
	t.Helper()
	var d httpapi.Draft
	r := p.apiOK(t, "PUT", "/configuration/draft", map[string]any{"expected_version": expected, "snapshot": snap}, "")
	b, _ := json.Marshal(r)
	json.Unmarshal(b, &d)
	return d
}

// apply runs the full validate→apply flow and returns the resulting Draft.
func (p *Panel) apply(t *testing.T, snap policy.Snapshot, expected int) httpapi.Draft {
	t.Helper()
	d := p.putDraft(t, snap, expected)
	rev := p.apiOK(t, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, "")
	prev := p.apiOK(t, "GET", "/revisions/"+rev["id"].(string)+"/preview", nil, "")["expected_applied_revision"]
	validate := p.op(t, "validate", rev["id"].(string), prev, "")
	applied := p.op(t, "apply", rev["id"].(string), prev, validate["id"].(string))
	if applied["status"] != "succeeded" {
		t.Fatalf("apply did not succeed: %v", applied)
	}
	return d
}

// op posts an operation (validate/apply/rollback) with an idempotency key and
// waits until it reaches a terminal status.
func (p *Panel) op(t *testing.T, action, revID string, expected any, validation string) map[string]any {
	t.Helper()
	body := map[string]any{"action": action, "revision_id": revID, "expected_applied_revision": expected}
	if validation != "" {
		body["validation_operation_id"] = validation
	}
	key := randKey()
	op := p.apiOK(t, "POST", "/operations", body, key)
	replay := p.apiOK(t, "POST", "/operations", body, key)
	if replay["id"] != op["id"] {
		t.Fatalf("idempotent replay returned different op: %v vs %v", replay, op)
	}
	return waitForResult(t, 60, func() (map[string]any, error) {
		cur, status := p.api("GET", "/operations/"+op["id"].(string), nil, "")
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("op status %d", status)
		}
		st, _ := cur["status"].(string)
		if st == "pending" || st == "validating" || st == "reloading" {
			return nil, fmt.Errorf("%s pending", st)
		}
		return cur, nil
	})
}

// waitForEvent polls /events with the given query (without the leading "?")
// until at least one item is returned, returning the first (newest) event.
func (p *Panel) waitForEvent(t *testing.T, seconds int, query string) map[string]any {
	t.Helper()
	return waitForResult(t, seconds, func() (map[string]any, error) {
		page := p.apiOK(t, "GET", "/events?"+query, nil, "")
		items, _ := page["items"].([]any)
		if len(items) == 0 {
			return nil, errf("no event: %s", query)
		}
		return items[0].(map[string]any), nil
	})
}

// applyAsManager applies a snapshot as a manager. The revision preview endpoint
// is admin-only, so the caller supplies the current applied revision (from
// /status) as expected_applied_revision instead of deriving it from the preview.
func (p *Panel) applyAsManager(t *testing.T, snap policy.Snapshot, expected int, appliedRev string) {
	t.Helper()
	d := p.putDraft(t, snap, expected)
	rev := p.apiOK(t, "POST", "/revisions", map[string]any{"expected_draft_version": d.Version}, "")
	var prev any
	if appliedRev != "" {
		prev = appliedRev
	}
	validate := p.op(t, "validate", rev["id"].(string), prev, "")
	applied := p.op(t, "apply", rev["id"].(string), prev, validate["id"].(string))
	if applied["status"] != "succeeded" {
		t.Fatalf("manager apply did not succeed: %v", applied)
	}
}

// genOriginCert writes origin.key/origin.crt (RSA 2048, SAN allowed.test and
// denied.test) into dir using crypto/x509, replacing the openssl call.
func genOriginCert(t *testing.T, dir string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("origin key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "allowed.test"},
		DNSNames:     []string{"allowed.test", "denied.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("origin cert: %v", err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	writeFile(dir+"/origin.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	writeFile(dir+"/origin.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// genOpsCerts writes a fresh dev-CA plus panel/proxy leaf certs signed by it.
// Mirrors the openssl generate_certs helper in ops.py.
func genOpsCerts(t *testing.T, dir string) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	caTmpl := x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "Kraken OPS CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTmpl, &caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caKeyDER, _ := x509.MarshalPKCS8PrivateKey(caKey)
	writeFile(dir+"/new-ca.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER}))
	writeFile(dir+"/new-ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	caCert, _ := x509.ParseCertificate(caDER)

	for _, item := range []struct {
		name, cn string
		san      []string
	}{
		{"panel", "localhost", []string{"DNS:localhost", "IP:127.0.0.1"}},
		{"proxy", "proxy", []string{"DNS:proxy", "DNS:localhost", "IP:127.0.0.1"}},
	} {
		leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("%s key: %v", item.name, err)
		}
		leafSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		leafTmpl := x509.Certificate{
			SerialNumber: leafSerial,
			Subject:      pkix.Name{CommonName: item.cn},
			NotBefore:    time.Now().Add(-time.Minute),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		for _, s := range item.san {
			if strings.HasPrefix(s, "DNS:") {
				leafTmpl.DNSNames = append(leafTmpl.DNSNames, strings.TrimPrefix(s, "DNS:"))
			} else if strings.HasPrefix(s, "IP:") {
				leafTmpl.IPAddresses = append(leafTmpl.IPAddresses, net.ParseIP(strings.TrimPrefix(s, "IP:")))
			}
		}
		leafDER, err := x509.CreateCertificate(rand.Reader, &leafTmpl, caCert, &leafKey.PublicKey, caKey)
		if err != nil {
			t.Fatalf("%s cert: %v", item.name, err)
		}
		leafKeyDER, _ := x509.MarshalPKCS8PrivateKey(leafKey)
		writeFile(dir+"/new-"+item.name+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}))
		writeFile(dir+"/new-"+item.name+".crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	}
}

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}

// repoRoot walks up from the working directory (the integration test package)
// until it finds deploy/Dockerfile, returning that directory. Used so the
// image-update section can run `docker build -f deploy/Dockerfile` regardless
// of where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, ok := findRepoRoot()
	if !ok {
		t.Fatal("deploy/Dockerfile not found walking up from working directory")
	}
	return dir
}

// findRepoRoot is the non-fatal variant of repoRoot so ensureNextImage can use
// it inside a sync.Once without calling t.Fatal (which would Goexit the
// once-goroutine and leave waiters without an error).
func findRepoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "Dockerfile")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// nextImageOnce ensures the NEXT image is built at most once per test binary:
// every waiting test sees the same build result. The build uses layer cache so
// it stays cheap; the guard prevents parallel rebuilds racing on the same tag.
var (
	nextImageOnce sync.Once
	nextImageErr  error
)

// splitLines splits trimmed s on newlines.
func splitLines(s string) []string { return split(strings.TrimSpace(s), "\n") }

func split(s, sep string) []string { return strings.Split(s, sep) }

func scanInt(s string, dst *int) { fmt.Sscanf(s, "%d", dst) }

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }

func errf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func jsonUnmarshal(b []byte, v any) { json.Unmarshal(b, v) }

// deepCopySnap returns a value-deep copy of snap via JSON round-trip, so a test
// can mutate one slice element without aliasing the original. Mirrors Python's
// copy.deepcopy used before every modified apply.
func deepCopySnap(snap policy.Snapshot) policy.Snapshot {
	b, _ := json.Marshal(snap)
	var out policy.Snapshot
	json.Unmarshal(b, &out)
	return out
}

// draftEqual compares two draft maps semantically (order-independent) by
// re-marshalling both to canonical JSON. Mirrors Python's dict ==.
func draftEqual(a, b map[string]any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// pemBody strips PEM headers so a leaked key body can be searched in logs.
func pemBody(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(l, "-----") {
			b.WriteString(l)
		}
	}
	return b.String()
}

// tlsOk reports whether a TLS handshake to host:port verifies under caPath.
func tlsOk(host, port, caPath string) bool {
	pool := x509.NewCertPool()
	if b, err := os.ReadFile(caPath); err == nil {
		pool.AppendCertsFromPEM(b)
	}
	conn, err := tls.Dial("tcp", net.JoinHostPort(host, port), &tls.Config{RootCAs: pool, ServerName: "localhost"})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// proxyTLSOk runs a Python one-liner in a client container to test the proxy
// TLS listener (3129) against a CA file inside the container.
func proxyTLSOk(t *testing.T, client, cafile string) bool {
	t.Helper()
	code := "import socket,ssl\n" +
		"try:\n" +
		" ctx=ssl.create_default_context(cafile=" + pyRepr(cafile) + ")\n" +
		" s=ctx.wrap_socket(socket.create_connection(('proxy',3129),timeout=8),server_hostname='localhost')\n" +
		" s.close();print('OK')\n" +
		"except Exception:\n" +
		" print('FAIL')\n"
	out, _ := dockerExec(client, "python", "-c", code)
	return strings.TrimSpace(out) == "OK"
}

// pyRepr returns a Python single-quoted string literal for s.
func pyRepr(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'") + "'"
}

// metrics fetches /squid/metrics as a typed runtime.Metrics value.
func (p *Panel) metrics() runtime.Metrics {
	r, _ := p.api("GET", "/squid/metrics", nil, "")
	b, _ := json.Marshal(r)
	var m runtime.Metrics
	json.Unmarshal(b, &m)
	return m
}

// instance bundles the live proxy container and its panel client. Launched by
// launchInstance for the smoke/network/apt suites; ops builds its own variants.
type instance struct {
	panel    *Panel
	proxy    string
	network  string
	volume   string
	password string
	port     string // host port mapped to 8443
}

// launchInstance brings up a disposable kraken instance: isolated network and
// volume, dev-cert, bootstrap, then a proxy container with a published host
// port. Returns the panel client already logged in with squid up.
func launchInstance(t *testing.T, r *resources, fixturesDir, subnet, base string) instance {
	t.Helper()
	name := randName("sc-test")
	network := r.addNetwork(name + "-net")
	volume := r.addVolume(name + "-data")
	docker(t, "network", "create", "--subnet", subnet, network)
	docker(t, "volume", "create", volume)
	docker(t, append(append([]string{"run", "--rm", "--user", controlUser, "-v", volume + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", testImage(), "dev-cert")...)
	password := randPassword(24)
	dockerIn(t, password+"\n", append(append([]string{"run", "--rm", "-i", "--user", controlUser, "-v", volume + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", testImage(), "bootstrap", "--password-stdin")...)
	proxy := r.addContainer(name + "-proxy")
	port := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", network, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", volume + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key"}, krakenSecOpts...), testImage())
	})
	origin := "https://localhost:" + port
	docker(t, "cp", proxy+":/var/lib/kraken/certs/panel.crt", fixturesDir+"/panel.crt")
	docker(t, "cp", proxy+":/var/lib/kraken/certs/proxy.crt", fixturesDir+"/proxy.crt")
	panel := newPanel(origin, fixturesDir+"/panel.crt", password)
	panel.login(t, password)
	panel.waitUp(t, 60)
	return instance{panel: panel, proxy: proxy, network: network, volume: volume, password: password, port: port}
}

// fetchStatus runs a Python one-liner in a container to fetch through the
// proxy and returns the HTTP status code (int) plus the X-Squid-Error header.
func fetchStatus(t *testing.T, client, target string, tlsProxy, connect bool, cafile, origincrt string) int {
	t.Helper()
	return fetchResult(t, client, target, tlsProxy, connect, cafile, origincrt).status
}

const opsOriginCode = `import http.server,ssl,threading
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin'*1024)
 def log_message(self,*args):pass
plain=http.server.HTTPServer(('0.0.0.0',80),Handler)
secure=http.server.HTTPServer(('0.0.0.0',443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixtures/origin.crt','/fixtures/origin.key');secure.socket=ctx.wrap_socket(secure.socket,server_side=True)
threading.Thread(target=plain.serve_forever,daemon=True).start();secure.serve_forever()
`

// startOriginClient brings up the HTTP+HTTPS origin (with aliases) and a
// sleeping client, both on the fixture network with /fixtures mounted.
// Returns (originContainer, clientContainer).
func startOriginClient(t *testing.T, r *resources, net, fixtures, ident, base string) (string, string) {
	t.Helper()
	writeFile(fixtures+"/origin.py", []byte(opsOriginCode))
	origin := r.addContainer(ident + "-origin")
	docker(t, "run", "-d", "--name", origin, "--network", net, "--ip", base+".10",
		"--network-alias", "allowed.test", "--network-alias", "denied.test",
		"-v", fixtures+":/fixtures:ro", pythonImage, "python", "/fixtures/origin.py")
	client := r.addContainer(ident + "-client")
	docker(t, "run", "-d", "--name", client, "--network", net, "--ip", base+".20",
		"-v", fixtures+":/fixtures:ro", pythonImage, "python", "-c", "import time;time.sleep(3600)")
	return origin, client
}

// launchOpsInstance is the ops-section variant of launchInstance: it accepts
// the image tag so the image-update section can boot the NEXT_IMAGE.
func launchOpsInstance(t *testing.T, r *resources, fixtures, subnet, base, image string, copyProxy bool) instance {
	t.Helper()
	name := randName("sc-ops")
	network := r.addNetwork(name + "-n")
	volume := r.addVolume(name + "-v")
	docker(t, "network", "create", "--subnet", subnet, network)
	docker(t, "volume", "create", volume)
	docker(t, append(append([]string{"run", "--rm", "--user", controlUser, "-v", volume + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", image, "dev-cert")...)
	password := randPassword(24)
	dockerIn(t, password+"\n", append(append([]string{"run", "--rm", "-i", "--user", controlUser, "-v", volume + ":/var/lib/kraken"}, krakenSecOpts...), "--entrypoint", "/usr/local/bin/kraken", image, "bootstrap", "--password-stdin")...)
	proxy := r.addContainer(name + "-p")
	port := runPortMapped(t, func(p string) []string {
		return append(append([]string{"run", "-d", "--name", proxy, "--network", network, "--network-alias", "proxy", "--ip", base + ".2",
			"-v", volume + ":/var/lib/kraken",
			"-p", "127.0.0.1:" + p + ":8443",
			"-e", "KRAKEN_PUBLIC_ORIGIN=https://localhost:" + p,
			"-e", "KRAKEN_PROXY_CERT=/var/lib/kraken/certs/proxy.crt", "-e", "KRAKEN_PROXY_KEY=/var/lib/kraken/certs/proxy.key"}, krakenSecOpts...), image)
	})
	origin := "https://localhost:" + port
	docker(t, "cp", proxy+":/var/lib/kraken/certs/panel.crt", fixtures+"/panel.crt")
	if copyProxy {
		docker(t, "cp", proxy+":/var/lib/kraken/certs/proxy.crt", fixtures+"/proxy.crt")
	}
	panel := newPanel(origin, fixtures+"/panel.crt", password)
	panel.login(t, password)
	panel.waitUp(t, 120)
	return instance{panel: panel, proxy: proxy, network: network, volume: volume, password: password, port: port}
}

// ensureNextImage builds KRAKEN_TEST_IMAGE_NEXT from the current tree with a
// trivial label difference, as ops.py does. Always rebuilt (layer cache keeps
// it cheap) so a stale tag cannot test an incompatible schema. sync.Once
// guarantees the build runs at most once per test binary; every waiting test
// sees the same result.
func ensureNextImage(t *testing.T) {
	t.Helper()
	nextImageOnce.Do(func() {
		root, ok := findRepoRoot()
		if !ok {
			nextImageErr = fmt.Errorf("deploy/Dockerfile not found")
			return
		}
		out, err := dockerErr("build", "-f", filepath.Join(root, "deploy", "Dockerfile"), "--label", "kraken.ops.variant=next", "-t", nextImage(), root)
		if err != nil {
			nextImageErr = fmt.Errorf("docker build next image: %v\n%s", err, tail(out, 3000))
		}
	})
	if nextImageErr != nil {
		t.Fatalf("ensureNextImage: %v", nextImageErr)
	}
}

// opsFetch is the ops.py fetch: a Python one-liner returning the HTTP status.
// cafile/origincrt are paths inside the client container.
func opsFetch(t *testing.T, client, target string, tlsProxy, connect bool, cafile, origincrt string) int {
	t.Helper()
	return fetchStatus(t, client, target, tlsProxy, connect, cafile, origincrt)
}

// imageMissing reports whether the given docker image is absent locally.
func imageMissing(image string) bool {
	_, err := dockerErr("image", "inspect", image)
	return err != nil
}

// newPipeProc is a long-lived `docker exec -i` Python process with separate stdin
// and stdout pipes, used for the held-CONNECT and persistent-TLS-client probes
// in smoke. Reads are line-based via a bufio.Scanner. The `code` argument is
// passed to `python -c` when it starts with "python " (a full command), or as
// a -c argument otherwise.
type pipeProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

func newPipeProc(container, code string) *pipeProc {
	var args []string
	if strings.HasPrefix(code, "python ") {
		args = []string{"exec", "-i", container, strings.SplitN(code, " ", 2)[0], strings.SplitN(code, " ", 2)[1]}
	} else {
		args = []string{"exec", "-i", container, "python", "-c", code}
	}
	cmd := exec.Command("docker", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	return &pipeProc{cmd: cmd, stdin: stdin, stdout: bufio.NewScanner(stdout)}
}

// readLine returns the next stdout line, failing the test if absent.
func (p *pipeProc) readLine(t *testing.T) string {
	t.Helper()
	if !p.stdout.Scan() {
		t.Fatalf("pipe closed: %v", p.stdout.Err())
	}
	return p.stdout.Text()
}

// write sends bytes to the process stdin and flushes.
func (p *pipeProc) write(t *testing.T, s string) {
	t.Helper()
	if _, err := p.stdin.Write([]byte(s)); err != nil {
		t.Fatalf("pipe write: %v", err)
	}
}

// close waits for the process to exit after stdin is closed.
func (p *pipeProc) close(t *testing.T) {
	t.Helper()
	p.stdin.Close()
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("pipe wait: %v", err)
	}
}

type fetchOut struct {
	status int
	err    string
}

func fetchResult(t *testing.T, client, target string, tlsProxy, connect bool, cafile, origincrt string) fetchOut {
	t.Helper()
	var code strings.Builder
	code.WriteString("import socket,ssl,http.client\n")
	if tlsProxy {
		code.WriteString("c=http.client.HTTPConnection('proxy',3129);c.sock=ssl.create_default_context(cafile=" + pyRepr(cafile) + ").wrap_socket(socket.create_connection(('proxy',3129)),server_hostname='localhost')\n")
	} else {
		code.WriteString("c=http.client.HTTPConnection('proxy',3128,timeout=5)\n")
	}
	if connect {
		code.WriteString("c.set_tunnel('allowed.test',443);c.connect();sock=ssl.create_default_context(cafile=" + pyRepr(origincrt) + ").wrap_socket(c.sock,server_hostname='allowed.test');sock.sendall(b'GET / HTTP/1.0\\r\\nHost: allowed.test\\r\\n\\r\\n');r=http.client.HTTPResponse(sock);r.begin();print(r.status)\n")
	} else {
		code.WriteString("c.request('GET'," + pyRepr(target) + ");r=c.getresponse();print(r.status);print(r.getheader('X-Squid-Error') or '')\n")
	}
	out, _ := dockerExec(client, "python", "-c", code.String())
	lines := strings.Split(strings.TrimSpace(out), "\n")
	res := fetchOut{}
	if len(lines) > 0 {
		fmt.Sscanf(lines[0], "%d", &res.status)
	}
	if len(lines) > 1 {
		res.err = lines[1]
	}
	return res
}
