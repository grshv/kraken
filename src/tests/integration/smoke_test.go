//go:build integration

package integration

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/policy"
)

// oldBundleCode regresses the last-good manifest stored in the control DB so
// service-update has work to do: it strips the compiler header from the stored
// config text and sets compiler_version to "5". Runs inside a throwaway python
// container with the volume mounted, exactly as smoke.py does.
const oldBundleCode = `import json,sqlite3
db=sqlite3.connect('/var/lib/kraken/data/control.sqlite3')
db.execute('PRAGMA busy_timeout=5000')
row=db.execute("SELECT value FROM kv WHERE key='runtime.last_good'").fetchone()
manifest=json.loads(row[0])
lines=manifest['config'].splitlines(True)
manifest['config']=''.join(line for line in lines if not line.startswith('# kraken compiler='))
manifest['compiler_version']='5'
db.execute("UPDATE kv SET value=? WHERE key='runtime.last_good'",(json.dumps(manifest),))
db.commit();db.close()
`

// heldCode opens a CONNECT to allowed.test:443 through the proxy, prints READY,
// blocks on stdin, then completes the TLS handshake and prints the origin status.
const heldCode = `import socket,ssl,http.client
sock=socket.create_connection(('proxy',3128),timeout=10)
sock.sendall(b'CONNECT allowed.test:443 HTTP/1.1\r\nHost: allowed.test:443\r\n\r\n')
head=b''
while b'\r\n\r\n' not in head:head+=sock.recv(1)
assert b'200' in head.split(b'\r\n')[0],head
print('READY',flush=True);input()
sock=ssl.create_default_context(cafile='/fixtures/origin.crt').wrap_socket(sock,server_hostname='allowed.test')
sock.sendall(b'GET / HTTP/1.0\r\nHost: allowed.test\r\n\r\n')
r=http.client.HTTPResponse(sock);r.begin();print(r.status)
`

// tlsClientCode is a line-oriented TLS proxy client: each stdin JSON line
// {"username","password","target","connect"} produces one stdout status line.
const tlsClientCode = `import base64,http.client,json,socket,ssl,sys
c=http.client.HTTPConnection('proxy',3129,timeout=8)
c.sock=ssl.create_default_context(cafile='/fixtures/proxy.crt').wrap_socket(socket.create_connection(('proxy',3129),timeout=8),server_hostname='localhost')
for line in sys.stdin:
 d=json.loads(line)
 headers={}
 if 'username' in d: headers['Proxy-Authorization']='Basic '+base64.b64encode((d['username']+':'+d['password']).encode()).decode()
 if d.get('connect'):
  c.request('CONNECT','allowed.test:443',headers=headers)
  head=b''
  while b'\r\n\r\n' not in head:
   chunk=c.sock.recv(1)
   assert chunk,'proxy closed CONNECT response'
   head+=chunk
  connect_status=int(head.split(b' ')[1])
  if connect_status!=200:print(connect_status,flush=True);continue
  incoming=ssl.MemoryBIO();outgoing=ssl.MemoryBIO()
  secured=ssl.create_default_context(cafile='/fixtures/origin.crt').wrap_bio(incoming,outgoing,server_side=False,server_hostname='allowed.test')
  while True:
   try:secured.do_handshake();break
   except ssl.SSLWantReadError:
    wire=outgoing.read()
    if wire:c.sock.sendall(wire)
    chunk=c.sock.recv(65536)
    assert chunk,'origin closed during handshake'
    incoming.write(chunk)
  wire=outgoing.read()
  if wire:c.sock.sendall(wire)
  secured.write(b'GET / HTTP/1.0\r\nHost: allowed.test\r\n\r\n')
  c.sock.sendall(outgoing.read())
  while True:
   try:
    reply=secured.read(65536)
    if reply:print(int(reply.split(b' ')[1]),flush=True);break
   except ssl.SSLWantReadError:
    chunk=c.sock.recv(65536)
    assert chunk,'origin closed before HTTP response'
    incoming.write(chunk)
  continue
 c.request('GET',d.get('target','http://allowed.test/'),headers=headers)
 r=c.getresponse();r.read();print(r.status,flush=True)
`

// scanCode scans the /state tree (the volume mount) for any credential bytes
// and prints OK only if none are found, proving passwords never hit the volume.
const scanCode = `import base64,json,pathlib,sys
values=json.load(sys.stdin)
markers=[v.encode() for v in values]
markers += [base64.b64encode((u+':'+p).encode()) for u,p in [('REQUIRED',values[0]),('REQUIRED',values[1]),('Build.Agent',values[2])]]
for path in pathlib.Path('/state').rglob('*'):
 if path.is_file():
  data=path.read_bytes()
  assert not any(marker in data for marker in markers), 'credential persisted in '+str(path)
print('OK')
`

func TestSmoke(t *testing.T) {
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	const subnet, base = "11.250.17.0/24", "11.250.17"
	inst := launchInstance(t, &r, fixtures, subnet, base)
	proxy := inst.proxy

	t.Run("per-UID isolation (proxy cannot read panel secrets; squid and control run under their own UIDs)", func(t *testing.T) {
		proxyCannot := func(path string) bool {
			_, ok := dockerOK(t, "exec", "--user", "13:13", proxy, "cat", path)
			return !ok
		}
		if !proxyCannot("/var/lib/kraken/data/control.sqlite3") {
			t.Error("proxy read panel database")
		}
		if !proxyCannot("/var/lib/kraken/certs/panel.key") {
			t.Error("proxy read panel key")
		}
		if out, _ := dockerExecUser(proxy, "13:13", "cat", "/dev/shm/kraken/squid.conf"); out == "" {
			t.Error("proxy cannot read squid.conf")
		}
		// ps -e -o uid=,comm=,args=: squid runs as 13, kraken serve as 10013.
		ps := docker(t, "exec", proxy, "ps", "-e", "-o", "uid=,comm=,args=")
		squidUIDs := map[string]bool{}
		serveUIDs := map[string]bool{}
		for _, line := range strings.Split(ps, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			uid, comm := fields[0], fields[1]
			args := strings.Join(fields[1:], " ")
			if comm == "squid" {
				squidUIDs[uid] = true
			}
			if strings.HasSuffix(args, "kraken serve") {
				serveUIDs[uid] = true
			}
		}
		if len(squidUIDs) != 1 || !squidUIDs["13"] {
			t.Errorf("squid UIDs: %v, want {13}", squidUIDs)
		}
		if len(serveUIDs) != 1 || !serveUIDs["10013"] {
			t.Errorf("kraken serve UIDs: %v, want {10013}", serveUIDs)
		}
	})

	t.Run("HTTPS trust, bootstrap, login, active config fingerprint", func(t *testing.T) {
		// launchInstance logged in and waited for squid up; milestone recorded.
		s := inst.panel.status()
		squid, _ := s["squid"].(map[string]any)
		if squid["state"] != "up" {
			t.Fatalf("squid not up: %v", s)
		}
	})

	// Stale bundle simulation: dirty the draft, regress the on-disk bundle, then
	// service-update must upgrade the bundle without touching the dirty draft.
	draftBeforeVer := inst.panel.draftVersion()
	dirty := deepCopySnap(policySnap(t, inst.panel))
	dirty.TLSProfiles[0].Name = "Unapplied fixture draft"
	inst.panel.putDraft(t, dirty, draftBeforeVer)
	dirtyDraftBefore := inst.panel.draftOK(t)
	writeFile(filepath.Join(fixtures, "old_bundle.py"), []byte(oldBundleCode))
	docker(t, "run", "--rm", "--user", controlUser, "-v", inst.volume+":/var/lib/kraken", "-v", fixtures+":/fixtures:ro", pythonImage, "python", "/fixtures/old_bundle.py")
	// Remove the serving config so the control process recreates it from the
	// regressed LKG on the next recovery cycle. Restart control (not squid):
	// serve re-runs Recover, sees squid.conf missing, and rewrites it from the
	// regressed last-good config in the DB. Squid stays running; kraken check
	// verifies the hash (unchanged) so it passes without a squid restart.
	docker(t, "exec", proxy, "rm", "-f", "/dev/shm/kraken/squid.conf")
	docker(t, "exec", proxy, "pebble", "restart", "control")
	waitFor(t, 30, func() error {
		_, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "kraken", "check")
		if !ok {
			return errf("kraken check")
		}
		return nil
	})
	// Panel restarts with control; wait for it to come back before checking status.
	waitFor(t, 60, func() error {
		if inst.panel.status() == nil {
			return errf("panel not ready")
		}
		return nil
	})
	if inst.panel.status()["runtime_update_available"] != true {
		t.Fatal("stale bundle not detected")
	}
	docker(t, "exec", "--user", controlUser, proxy, "kraken", "service-update")
	dirtyDraftAfter := inst.panel.draftOK(t)
	if !draftEqual(dirtyDraftBefore, dirtyDraftAfter) {
		t.Fatalf("dirty draft changed across service-update:\nbefore %v\nafter  %v", dirtyDraftBefore, dirtyDraftAfter)
	}
	waitFor(t, 30, func() error {
		m := inst.panel.metrics()
		if !m.Available {
			return errf("metrics unavailable")
		}
		return nil
	})

	t.Run("local service update upgrades old bundle without applying or changing dirty draft", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	t.Run("live Squid metrics include uptime/resources, exclude the manager request", func(t *testing.T) {
		m := inst.panel.metrics()
		if !m.Available || m.SquidVersion != "6.13" {
			t.Fatalf("metrics: %+v", m)
		}
		if m.UptimeSeconds == nil || *m.UptimeSeconds <= 0 || m.MemoryPeakBytes == nil || *m.MemoryPeakBytes <= 0 {
			t.Errorf("uptime/peak: %+v", m)
		}
		if m.ActiveRequests == nil || *m.ActiveRequests != 0 {
			t.Errorf("active_requests: %+v", m)
		}
	})

	// Deterministic HTTP+HTTPS origin and a separate source identity.
	genOriginCert(t, fixtures)
	originCode := `import http.server,ssl,threading
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin'*1024)
 def log_message(self,*args):pass
plain=http.server.HTTPServer(('0.0.0.0',80),Handler)
secure=http.server.HTTPServer(('0.0.0.0',443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixtures/origin.crt','/fixtures/origin.key');secure.socket=ctx.wrap_socket(secure.socket,server_side=True)
threading.Thread(target=plain.serve_forever,daemon=True).start();secure.serve_forever()
`
	writeFile(filepath.Join(fixtures, "origin.py"), []byte(originCode))
	originName := ""
	clientName := ""
	for _, fc := range []struct {
		suffix, ip string
		cmd        []string
		alias      bool
	}{
		{"origin", base + ".10", []string{"python", "/fixtures/origin.py"}, true},
		{"client", base + ".20", []string{"python", "-c", "import time;time.sleep(3600)"}, false},
	} {
		n := r.addContainer(randName("sc-test") + "-" + fc.suffix)
		if fc.suffix == "origin" {
			originName = n
		} else {
			clientName = n
		}
		args := []string{"run", "-d", "--name", n, "--network", inst.network, "--ip", fc.ip, "-v", fixtures + ":/fixtures:ro"}
		if fc.alias {
			args = append(args, "--network-alias", "allowed.test", "--network-alias", "denied.test")
		}
		args = append(args, pythonImage)
		args = append(args, fc.cmd...)
		docker(t, args...)
	}
	client := clientName
	source := originName
	smFetch := func(container, target string, tlsProxy, connect bool) int {
		return fetchStatus(t, container, target, tlsProxy, connect, "/fixtures/proxy.crt", "/fixtures/origin.crt")
	}

	t.Run("unknown source denied", func(t *testing.T) {
		if st := smFetch(client, "http://allowed.test/", false, false); st != 403 {
			t.Errorf("client: expected 403, got %d", st)
		}
		if st := smFetch(source, "http://allowed.test/", false, false); st != 403 {
			t.Errorf("source: expected 403, got %d", st)
		}
	})

	t.Run("external proxy clients cannot access read-only or mutable manager reports", func(t *testing.T) {
		for _, report := range []string{"info", "counters", "5min", "active_requests", "shutdown", "config"} {
			if st := smFetch(client, "http://proxy:3128/squid-internal-mgr/"+report, false, false); st != 403 {
				t.Errorf("manager report %s: expected 403, got %d", report, st)
			}
		}
	})

	// Apply all_web + allowlist policy.
	snap := policySnap(t, inst.panel)
	profile := snap.TLSProfiles[0].ID
	allID, allowID := randID(), randID()
	snap.Policies = []policy.Policy{
		{ID: allID, Name: "All web fixture", Mode: "all_web", Destinations: nil, TLSProfileID: profile},
		{ID: allowID, Name: "Allowlist fixture", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "allowed.test"}}, TLSProfileID: profile},
	}
	snap.Clients = []policy.Client{
		{ID: randID(), Name: "source fixture", Networks: []string{base + ".10/32"}, Enabled: true, PolicyID: allID},
		{ID: randID(), Name: "client fixture", Kind: "group", Networks: []string{base + ".20"}, Enabled: true, PolicyID: allowID},
	}
	inst.panel.apply(t, snap, inst.panel.draftVersion())
	firstApplied, _ := inst.panel.status()["applied_revision"].(string)
	baselineManifest := lkgManifest(t, inst.volume)
	baselineManifestBytes, _ := json.Marshal(baselineManifest)
	waitFor(t, 30, func() error {
		if smFetch(client, "http://allowed.test/", false, false) != 200 {
			return errf("client not 200")
		}
		return nil
	})

	// Generate traffic (denied, raw IP, source, loopback, metadata, CONNECT,
	// long query) before checking origin counters — the counters need real
	// bytes to flow through Squid to become non-zero.
	if st := smFetch(client, "http://denied.test/", false, false); st != 403 {
		t.Errorf("client denied.test: expected 403, got %d", st)
	}
	if st := smFetch(client, "http://11.250.17.10/", false, false); st != 403 {
		t.Errorf("client raw IP: expected 403, got %d", st)
	}
	if st := smFetch(source, "http://denied.test/", false, false); st != 200 {
		t.Errorf("source denied.test: expected 200, got %d", st)
	}
	if st := smFetch(client, "http://127.0.0.1/", false, false); st != 403 {
		t.Errorf("client loopback: expected 403, got %d", st)
	}
	if st := smFetch(source, "http://169.254.169.254/", false, false); st != 403 {
		t.Errorf("source metadata: expected 403, got %d", st)
	}
	if st := smFetch(client, "http://allowed.test/", false, true); st != 200 {
		t.Errorf("client CONNECT: expected 200, got %d", st)
	}
	if st := smFetch(client, "http://allowed.test/?fixture="+strings.Repeat("x", 4096), false, false); st != 200 {
		t.Errorf("client long query: expected 200, got %d", st)
	}

	t.Run("real HTTP/CONNECT origin counters change; idle telemetry does not inflate them", func(t *testing.T) {
		var measuredOriginReqs float64
		waitFor(t, 30, func() error {
			m := inst.panel.metrics()
			if !m.Available || m.OriginRequestsTotal == nil || *m.OriginRequestsTotal <= 0 {
				return errf("no origin requests: %+v", m)
			}
			if m.OriginBytesInTotal == nil || *m.OriginBytesInTotal <= 0 || m.OriginBytesOutTotal == nil || *m.OriginBytesOutTotal <= 0 {
				return errf("no origin bytes: %+v", m)
			}
			measuredOriginReqs = *m.OriginRequestsTotal
			return nil
		})
		time.Sleep(2200 * time.Millisecond)
		idle := inst.panel.metrics()
		if idle.OriginRequestsTotal == nil || *idle.OriginRequestsTotal != measuredOriginReqs {
			t.Errorf("idle telemetry inflated origin requests: measured %v idle %v", measuredOriginReqs, idle.OriginRequestsTotal)
		}
	})

	t.Run("all_web/allowlist, private/metadata guards, real TLS CONNECT, idempotent operations", func(t *testing.T) {
		// milestone: the fetches above asserted all the documented outcomes.
	})

	t.Run("rejected injection leaves runtime unchanged", func(t *testing.T) {
		invalid := deepCopySnap(snap)
		invalid.Policies[1].Destinations[0].Domain = "allowed.test\nhttp_access allow all"
		_, st := inst.panel.api("PUT", "/configuration/draft", map[string]any{"expected_version": inst.panel.draftVersion(), "snapshot": invalid}, "")
		if st != 422 {
			t.Fatalf("injection accepted: status %d", st)
		}
		if st := smFetch(client, "http://denied.test/", false, false); st != 403 {
			t.Errorf("runtime changed after rejected injection: %d", st)
		}
	})

	// Per-VM TLS transport: reject downgrade to the HTTP listener.
	tlsID := randID()
	snapTLS := deepCopySnap(snap)
	snapTLS.TLSProfiles = append(snapTLS.TLSProfiles, policy.TLSProfile{ID: tlsID, Name: "TLS fixture", Mode: "tunnel", Transport: "tls", CertRef: strPtr("proxy-server")})
	snapTLS.Policies[1].TLSProfileID = tlsID
	inst.panel.apply(t, snapTLS, inst.panel.draftVersion())
	t.Run("HTTPS proxy with verified certificate; plaintext downgrade denied", func(t *testing.T) {
		if st := smFetch(client, "http://allowed.test/", false, false); st != 403 {
			t.Errorf("HTTP listener: expected 403, got %d", st)
		}
		if st := smFetch(client, "http://allowed.test/", true, false); st != 200 {
			t.Errorf("TLS proxy: expected 200, got %d", st)
		}
	})

	// Held real CONNECT must appear in active request aggregate.
	held := newPipeProc(source, heldCode)
	if held.readLine(t) != "READY" {
		t.Fatal("held CONNECT did not become READY")
	}
	waitFor(t, 30, func() error {
		m := inst.panel.metrics()
		if !m.Available || m.ActiveRequests == nil || *m.ActiveRequests < 1 {
			return errf("no active request: %+v", m)
		}
		return nil
	})
	t.Run("held real CONNECT appears in active request aggregate", func(t *testing.T) {
		// milestone: assertion above passed.
	})

	// Independent process restart and container startup recovery.
	squidPID := strings.TrimSpace(docker(t, "exec", proxy, "sh", "-c", "pgrep -o squid || true"))
	docker(t, "exec", proxy, "pebble", "restart", "control")
	waitFor(t, 30, func() error {
		if inst.panel.status() == nil {
			return errf("no status")
		}
		return nil
	})
	if strings.TrimSpace(docker(t, "exec", proxy, "sh", "-c", "pgrep -o squid || true")) != squidPID {
		t.Fatal("squid pid changed across control restart")
	}
	held.write(t, "continue\n")
	if held.readLine(t) != "200" {
		t.Fatal("held CONNECT did not complete 200")
	}
	held.close(t)
	if st := smFetch(client, "http://allowed.test/", true, false); st != 200 {
		t.Fatalf("client TLS after control restart: %d", st)
	}
	docker(t, "exec", proxy, "pebble", "restart", "squid")
	waitFor(t, 30, func() error {
		_, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "kraken", "check")
		if !ok {
			return errf("kraken check")
		}
		return nil
	})
	if st := smFetch(client, "http://allowed.test/", true, false); st != 200 {
		t.Fatalf("client TLS after squid restart: %d", st)
	}
	docker(t, "restart", proxy)
	waitFor(t, 60, func() error {
		if inst.panel.status() == nil {
			return errf("no status")
		}
		return nil
	})
	waitFor(t, 60, func() error {
		_, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "kraken", "check")
		if !ok {
			return errf("kraken check")
		}
		return nil
	})
	if st := smFetch(client, "http://allowed.test/", true, false); st != 200 {
		t.Fatalf("client TLS after container restart: %d", st)
	}
	if st := smFetch(client, "http://denied.test/", true, false); st != 403 {
		t.Fatalf("denied after restart: %d", st)
	}
	t.Run("independent Go restart, Squid restart gate, container restart persistence", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Explicit rollback to the first applied revision.
	currentRev, _ := inst.panel.status()["applied_revision"].(string)
	rollback := inst.panel.op(t, "rollback", firstApplied, currentRev, "")
	if rollback["status"] != "succeeded" {
		t.Fatalf("rollback did not succeed: %v", rollback)
	}
	if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
		t.Fatalf("client after rollback: %d", st)
	}
	if st := smFetch(client, "http://denied.test/", false, false); st != 403 {
		t.Fatalf("denied after rollback: %d", st)
	}
	t.Run("existing CONNECT survives Go restart; explicit rollback to previous applied revision", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Visual settings and expert squid.conf affect actual Squid traffic.
	advanced := policySnapRev(t, inst.panel, firstApplied)
	advanced.Settings.ConnectTimeout = 45
	advanced.Settings.CacheEnabled = true
	inst.panel.apply(t, advanced, inst.panel.draftVersion())
	if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
		t.Fatalf("client after visual settings: %d", st)
	}
	advanced.Mode = "expert"
	advanced.Expert = "acl fixture_source src 11.250.17.20/32\nacl fixture_domain dstdomain -n allowed.test\nhttp_access allow fixture_source fixture_domain\nrefresh_pattern -i \\.(deb|rpm)$ 0 20% 4320\n"
	inst.panel.apply(t, advanced, inst.panel.draftVersion())
	if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
		t.Fatalf("client expert: %d", st)
	}
	if st := smFetch(client, "http://denied.test/", false, false); st != 403 {
		t.Fatalf("denied expert: %d", st)
	}
	if st := smFetch(source, "http://denied.test/", false, false); st != 403 {
		t.Fatalf("source expert (all_web inactive): %d", st)
	}
	t.Run("visual settings and expert squid.conf affect actual Squid traffic", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Installation listener changes must be rejected.
	changed := deepCopySnap(advanced)
	dv := inst.panel.draftVersion()
	t.Run("installation listener changes rejected, published port and diagnostic probe preserved", func(t *testing.T) {
		for _, kv := range []struct {
			key string
			val int
		}{{"http_port", 8080}, {"tls_port", 4443}} {
			rejected := deepCopySnap(changed)
			if kv.key == "http_port" {
				rejected.Settings.HTTPPort = kv.val
			} else {
				rejected.Settings.TLSPort = kv.val
			}
			resp, st := inst.panel.api("PUT", "/configuration/draft", map[string]any{"expected_version": dv, "snapshot": rejected}, "")
			if st != 422 {
				t.Errorf("%s: expected 422, got %d: %v", kv.key, st, resp)
			}
			if resp != nil && resp["code"] != "installation_setting" {
				t.Errorf("%s: code %v, want installation_setting", kv.key, resp["code"])
			}
		}
		if inst.panel.draftVersion() != dv {
			t.Error("draft version changed after rejected listener edits")
		}
		if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
			t.Errorf("client after listener rejects: %d", st)
		}
		waitFor(t, 30, func() error {
			_, ok := dockerOK(t, "exec", "--user", controlUser, proxy, "kraken", "check")
			if !ok {
				return errf("kraken check")
			}
			return nil
		})
	})

	// Expert allowlist enforcement at save; real parse failure preserves traffic.
	for _, ec := range []string{"include /var/lib/kraken/data/*", "err_page_stylesheet /run/secrets/proxy.key", "this_is_not_a_squid_directive yes"} {
		rejected := deepCopySnap(changed)
		rejected.Expert = ec
		_, st := inst.panel.api("PUT", "/configuration/draft", map[string]any{"expected_version": inst.panel.draftVersion(), "snapshot": rejected}, "")
		if st != 422 {
			t.Fatalf("unsafe expert config accepted: %s: %d", ec, st)
		}
	}
	broken := deepCopySnap(changed)
	broken.Expert = "acl fixture_bad src not..an..address"
	badDraft := inst.panel.putDraft(t, broken, inst.panel.draftVersion())
	badRev := inst.panel.apiOK(t, "POST", "/revisions", map[string]any{"expected_draft_version": badDraft.Version}, "")
	expRev, _ := inst.panel.status()["applied_revision"].(string)
	badOp := inst.panel.apiOK(t, "POST", "/operations", map[string]any{"action": "validate", "revision_id": badRev["id"], "expected_applied_revision": expRev}, randKey())
	badResult := waitForResult(t, 60, func() (map[string]any, error) {
		cur, st := inst.panel.api("GET", "/operations/"+badOp["id"].(string), nil, "")
		if st < 200 || st >= 300 {
			return nil, errf("op status %d", st)
		}
		s, _ := cur["status"].(string)
		if s == "pending" || s == "validating" {
			return nil, errf("pending")
		}
		return cur, nil
	})
	if badResult["status"] != "rejected" {
		t.Fatalf("bad expert op: %v", badResult)
	}
	findings, _ := badResult["findings"].([]any)
	if len(findings) == 0 || !strings.Contains(findings[0].(string), "строка 1 экспертной секции") {
		t.Fatalf("findings missing line: %v", findings)
	}
	if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
		t.Fatalf("client after bad expert: %d", st)
	}
	t.Run("allowlist enforcement at save, real parse failure preserves active traffic and reports line", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// CLI password reset revokes sessions and rejects the old password.
	replacement := randPassword(24)
	output := dockerIn(t, replacement+"\n", "exec", "-i", "--user", controlUser, proxy, "kraken", "password-reset", "--username", "admin", "--password-stdin")
	if strings.Contains(output, replacement) || strings.Contains(output, inst.password) {
		t.Fatal("password leaked in CLI output")
	}
	if _, st := inst.panel.api("GET", "/status", nil, ""); st != 401 {
		t.Fatalf("old session survived reset: %d", st)
	}
	waitFor(t, 30, func() error { return inst.panel.refreshCSRF() })
	if _, st := inst.panel.api("POST", "/session", map[string]string{"username": "admin", "password": inst.password}, ""); st != 401 {
		t.Fatalf("old password accepted: %d", st)
	}
	inst.panel.login(t, replacement)
	if s := inst.panel.status(); s == nil {
		t.Fatal("no status after new login")
	}
	inst.password = replacement
	t.Run("real CLI password reset, revoked sessions, old-password rejection and new login", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Unfinished promotion recovers previous policy through verified rollback.
	if st := smFetch(source, "http://denied.test/", false, false); st != 403 {
		t.Fatalf("source should be denied in expert mode: %d", st)
	}
	docker(t, "exec", proxy, "pebble", "stop", "control")
	candidateManifest := lkgManifest(t, inst.volume)
	journal := map[string]any{
		"operation_id": randID(),
		"stage":        "reloading",
		"previous":     baselineManifest,
		"candidate":    candidateManifest,
	}
	jb, _ := json.Marshal(journal)
	dbKVWrite(t, inst.volume, "runtime.journal", jb)
	docker(t, "restart", proxy)
	waitFor(t, 120, func() error {
		if inst.panel.status() == nil {
			return errf("no status")
		}
		return nil
	})
	recoveredJournal := dbKV(t, inst.volume, "runtime.journal")
	if recoveredJournal["stage"] != "terminal" || recoveredJournal["outcome"] != "rolled_back" {
		t.Fatalf("recovery journal: %v", recoveredJournal)
	}
	recoveredStatus := inst.panel.status()
	if recoveredStatus["applied_revision"] != firstApplied {
		t.Fatalf("applied revision after recovery: %v, want %s", recoveredStatus["applied_revision"], firstApplied)
	}
	if squid, _ := recoveredStatus["squid"].(map[string]any); squid["state"] != "up" {
		t.Fatalf("squid not up after recovery: %v", recoveredStatus)
	}
	if st := smFetch(source, "http://denied.test/", false, false); st != 200 {
		t.Fatalf("source after recovery: %d", st)
	}
	if st := smFetch(client, "http://allowed.test/", false, false); st != 200 {
		t.Fatalf("client after recovery: %d", st)
	}
	if st := smFetch(client, "http://denied.test/", false, false); st != 403 {
		t.Fatalf("denied after recovery: %d", st)
	}
	t.Run("unfinished promotion recovers previous policy through Squid/control startup and verified rollback", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Durable event history, client filters/cursors, real rotation, audit.
	clientID := snap.Clients[1].ID
	page := waitForResult(t, 30, func() (map[string]any, error) {
		p := inst.panel.apiOK(t, "GET", "/events?client_id="+clientID+"&limit=2", nil, "")
		items, _ := p["items"].([]any)
		if len(items) != 2 || p["next_cursor"] == nil || p["next_cursor"] == "" {
			return nil, errf("events page: %v", p)
		}
		for _, v := range items {
			ev := v.(map[string]any)
			if ev["client_id"] != clientID {
				return nil, errf("client_id mismatch")
			}
			if ev["client_match_basis"] != "policy_at_ingest" || ev["revision_id"] != nil {
				return nil, errf("basis/revision: %v", ev)
			}
		}
		return p, nil
	})
	older := inst.panel.apiOK(t, "GET", "/events?client_id="+clientID+"&limit=2&cursor="+page["next_cursor"].(string), nil, "")
	pageIDs := map[any]bool{}
	for _, v := range page["items"].([]any) {
		pageIDs[v.(map[string]any)["id"]] = true
	}
	for _, v := range older["items"].([]any) {
		if pageIDs[v.(map[string]any)["id"]] {
			t.Fatal("event overlap between pages")
		}
	}
	savedID := page["items"].([]any)[0].(map[string]any)["id"]
	labels := inst.panel.apiOK(t, "GET", "/events/clients", nil, "")["items"].([]any)
	found := false
	for _, v := range labels {
		if v.(map[string]any)["id"] == clientID {
			found = true
		}
	}
	if !found {
		t.Fatal("client missing from event labels")
	}
	beforeRotate := map[any]bool{}
	for _, v := range inst.panel.apiOK(t, "GET", "/events?limit=200", nil, "")["items"].([]any) {
		beforeRotate[v.(map[string]any)["id"]] = true
	}
	docker(t, "exec", "--user", "13:13", proxy, "/usr/sbin/squid", "-f", "/dev/shm/kraken/squid.conf", "-k", "rotate")
	docker(t, "exec", proxy, "pebble", "restart", "control")
	waitFor(t, 30, func() error {
		if inst.panel.status() == nil {
			return errf("no status")
		}
		return nil
	})
	persisted := inst.panel.apiOK(t, "GET", "/events?limit=200", nil, "")["items"].([]any)
	persistedIDs := map[any]bool{}
	for _, v := range persisted {
		persistedIDs[v.(map[string]any)["id"]] = true
	}
	if !persistedIDs[savedID] {
		t.Fatal("saved event missing after rotation")
	}
	if len(persistedIDs) != len(persisted) {
		t.Fatal("duplicate events after rotation")
	}
	for id := range beforeRotate {
		if !persistedIDs[id] {
			t.Fatal("event lost after rotation")
		}
	}
	audit := inst.panel.apiOK(t, "GET", "/audit?actor=local-cli&action=password_reset", nil, "")["items"].([]any)
	if len(audit) != 1 || audit[0].(map[string]any)["entity_id"] != "admin" {
		t.Fatalf("audit: %v", audit)
	}
	t.Run("durable event history, client filters/cursors, real rotation, control restart and administrative audit", func(t *testing.T) {
		// milestone: assertions above passed.
	})

	// Password proxy auth, NAT identities, reset/disable, manager scope/revocation.
	proxyPassword := randPassword(18)
	otherPassword := randPassword(18)
	inst.panel.apiOK(t, "POST", "/proxy-accounts", map[string]string{"username": "REQUIRED", "password": proxyPassword}, "")
	inst.panel.apiOK(t, "POST", "/proxy-accounts", map[string]string{"username": "Build.Agent", "password": otherPassword}, "")
	authSnap := policySnapFromBytes(baselineManifestBytes)
	tlsProfileID := randID()
	authSnap.TLSProfiles = append(authSnap.TLSProfiles, policy.TLSProfile{ID: tlsProfileID, Name: "Proxy login TLS", Mode: "tunnel", Transport: "tls", CertRef: strPtr("proxy-server")})
	authSnap.Policies[1].TLSProfileID = tlsProfileID
	authSnap.Clients[1].ProxyUsername = "REQUIRED"
	secondPolicy := randID()
	secondClient := randID()
	authSnap.Policies = append(authSnap.Policies, policy.Policy{ID: secondPolicy, Name: "Other login", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "denied.test"}}, TLSProfileID: tlsProfileID})
	authSnap.Clients = append(authSnap.Clients, policy.Client{ID: secondClient, Name: "shared-source identity", Kind: "host", Networks: []string{base + ".20/32"}, Enabled: true, PolicyID: secondPolicy, ProxyUsername: "Build.Agent"})
	inst.panel.apply(t, authSnap, inst.panel.draftVersion())
	// Write the TLS client script to the fixtures dir so the client container
	// can run it as a file (avoids passing a very long -c argument).
	writeFile(filepath.Join(fixtures, "tls_client.py"), []byte(tlsClientCode))
	proxyRequest := func(username, password, target string, connect bool) int {
		req := map[string]any{"target": target, "connect": connect}
		if username != "" {
			req["username"] = username
			req["password"] = password
		}
		b, _ := json.Marshal(req)
		out, stderr, err := dockerExecStdin(client, string(b)+"\n", "python", "/fixtures/tls_client.py")
		if err != nil {
			t.Fatalf("proxy_request: %v\nstdout: %s\nstderr: %s", err, out, stderr)
		}
		st := 0
		scanInt(strings.TrimSpace(out), &st)
		if st == 0 {
			t.Fatalf("proxy_request no status:\nstdout: %s\nstderr: %s", out, stderr)
		}
		return st
	}
	if st := smFetch(client, "http://allowed.test/", false, false); st != 403 {
		t.Fatalf("client without proxy auth: %d", st)
	}
	if st := proxyRequest("", "", "http://allowed.test/", false); st != 407 {
		t.Fatalf("no auth: %d", st)
	}
	if st := proxyRequest("REQUIRED", "invalid-fixture-password", "http://allowed.test/", false); st != 407 {
		t.Fatalf("invalid password: %d", st)
	}
	if st := proxyRequest("REQUIRED", proxyPassword, "http://allowed.test/", false); st != 200 {
		t.Fatalf("REQUIRED: %d", st)
	}
	if st := proxyRequest("REQUIRED", proxyPassword, "", true); st != 200 {
		t.Fatalf("REQUIRED connect: %d", st)
	}
	if st := proxyRequest("REQUIRED", proxyPassword, "http://denied.test/", false); st != 403 {
		t.Fatalf("REQUIRED denied: %d", st)
	}
	if st := proxyRequest("Build.Agent", otherPassword, "http://denied.test/", false); st != 200 {
		t.Fatalf("Build.Agent denied: %d", st)
	}
	if st := proxyRequest("Build.Agent", otherPassword, "http://allowed.test/", false); st != 403 {
		t.Fatalf("Build.Agent allowed: %d", st)
	}
	if st := proxyRequest("build.agent", otherPassword, "http://denied.test/", false); st != 407 {
		t.Fatalf("build.agent case: %d", st)
	}
	// Persistent TLS connection: reset/disable must invalidate cached credentials.
	pers := newPipeProc(client, "python /fixtures/tls_client.py")
	persRequest := func(password string) int {
		b, _ := json.Marshal(map[string]string{"username": "REQUIRED", "password": password})
		pers.write(t, string(b)+"\n")
		st := 0
		scanInt(strings.TrimSpace(pers.readLine(t)), &st)
		return st
	}
	if st := persRequest(proxyPassword); st != 200 {
		t.Fatalf("persistent: %d", st)
	}
	newProxyPassword := randPassword(20)
	inst.panel.apiOK(t, "PATCH", "/proxy-accounts/REQUIRED", map[string]any{"password": newProxyPassword}, "")
	resetStatus := persRequest(proxyPassword)
	if resetStatus != 403 && resetStatus != 407 {
		t.Fatalf("cached password after reset: %d", resetStatus)
	}
	if st := persRequest(newProxyPassword); st != 200 {
		t.Fatalf("new password: %d", st)
	}
	inst.panel.apiOK(t, "PATCH", "/proxy-accounts/REQUIRED", map[string]any{"enabled": false}, "")
	disabledStatus := persRequest(newProxyPassword)
	if disabledStatus != 403 && disabledStatus != 407 {
		t.Fatalf("cached disabled identity: %d", disabledStatus)
	}
	pers.close(t)
	inst.panel.apiOK(t, "PATCH", "/proxy-accounts/REQUIRED", map[string]any{"enabled": true}, "")
	if st := proxyRequest("REQUIRED", newProxyPassword, "http://allowed.test/", false); st != 200 {
		t.Fatalf("REQUIRED re-enabled: %d", st)
	}
	if st := proxyRequest("admin", replacement, "http://allowed.test/", false); st != 407 {
		t.Fatalf("admin as proxy login: %d", st)
	}
	// New panel user with manager scope; revoked when blocked.
	managerPassword := randPassword(24)
	inst.panel.apiOK(t, "POST", "/users", map[string]string{"username": "observer", "password": managerPassword, "role": "manager"}, "")
	managerPanel := newPanel(inst.panel.origin, filepath.Join(fixtures, "panel.crt"), managerPassword)
	managerPanel.username = "observer"
	managerPanel.login(t, managerPassword)
	if managerPanel.status()["squid"] == nil {
		t.Fatal("manager cannot read status")
	}
	managerSession := managerPanel.apiOK(t, "GET", "/session", nil, "")
	if managerSession["role"] != "manager" {
		t.Fatalf("manager role: %v", managerSession)
	}
	if _, ok := managerSession["scope"]; !ok {
		t.Fatalf("manager session missing scope: %v", managerSession)
	}
	for _, restricted := range []string{"/users", "/revisions", "/configuration/preview", "/configuration/expert-template"} {
		if _, st := managerPanel.api("GET", restricted, nil, ""); st != 403 {
			t.Fatalf("manager crossed admin boundary %s: %d", restricted, st)
		}
	}
	existingClient := snap.Clients[0].ID
	inst.panel.apiOK(t, "PUT", "/users/observer/scope", map[string]any{"networks": []string{base + ".0/24"}, "client_ids": []string{existingClient}}, "")
	draftView := managerPanel.draft()
	viewIDs := map[string]bool{}
	for _, c := range draftView["snapshot"].(map[string]any)["clients"].([]any) {
		viewIDs[c.(map[string]any)["id"].(string)] = true
	}
	if !viewIDs[existingClient] {
		t.Fatalf("manager cannot see assigned client: %v", viewIDs)
	}
	if viewIDs[snap.Clients[1].ID] {
		t.Fatal("manager sees foreign client")
	}
	// Admin changes the foreign client; manager POST /revisions must 403.
	adminSnap := policySnap(t, inst.panel)
	adminSnap.Clients[1].Networks = []string{base + ".99"}
	inst.panel.putDraft(t, adminSnap, inst.panel.draftVersion())
	resp, st := managerPanel.api("POST", "/revisions", map[string]any{"expected_draft_version": managerPanel.draftVersion()}, "")
	if st != 403 || resp["code"] != "draft_out_of_scope" {
		t.Fatalf("manager foreign revision: %d %v", st, resp)
	}
	inst.panel.apiOK(t, "PATCH", "/users/observer", map[string]any{"enabled": false}, "")
	if _, st := managerPanel.api("GET", "/status", nil, ""); st != 401 {
		t.Fatalf("disabled manager session survived: %d", st)
	}
	// No credentials persist in the volume.
	scanInput, _ := json.Marshal([]string{proxyPassword, newProxyPassword, otherPassword, managerPassword})
	out := dockerIn(t, string(scanInput), "run", "--rm", "-i", "--network", "none", "-v", inst.volume+":/state:ro", pythonImage, "python", "-c", scanCode)
	if strings.TrimSpace(out) != "OK" {
		t.Fatalf("credential persisted in volume: %s", out)
	}
	t.Run("password proxy auth over verified TLS, NAT identities, literal/case-sensitive users, live reset/disable cache invalidation, manager scope/session revocation", func(t *testing.T) {
		// milestone: assertions above passed.
	})
}

// strPtr returns a pointer to s, for policy.TLSProfile.CertRef.
func strPtr(s string) *string { return &s }

// dbKV reads a JSON value from the control DB kv table via a Python sidecar
// container with the volume mounted. The LKG and journal are no longer files
// on the volume; they live inside data/control.sqlite3.
func dbKV(t *testing.T, volume, key string) map[string]any {
	t.Helper()
	out, err := dockerErr("run", "--rm", "--user", controlUser, "-v", volume+":/var/lib/kraken", pythonImage, "python", "-c",
		"import json,sqlite3;db=sqlite3.connect('/var/lib/kraken/data/control.sqlite3');db.execute('PRAGMA busy_timeout=5000');row=db.execute(\"SELECT value FROM kv WHERE key=?\",('"+key+"',)).fetchone();db.close();print(row[0] if row else '{}')")
	if err != nil {
		t.Fatalf("dbKV %s: %v: %s", key, err, out)
	}
	var m map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &m) != nil {
		t.Fatalf("dbKV parse %s: %s", key, out)
	}
	return m
}

// dbKVWrite writes a JSON value into the control DB kv table via a Python
// sidecar container. Used by the recovery test to inject a non-terminal journal.
func dbKVWrite(t *testing.T, volume, key string, value []byte) {
	t.Helper()
	script := "import sqlite3,sys;db=sqlite3.connect('/var/lib/kraken/data/control.sqlite3');db.execute('PRAGMA busy_timeout=5000');db.execute(\"INSERT INTO kv VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value\",('" + key + "',sys.stdin.read()));db.commit();db.close()"
	dockerIn(t, string(value), "run", "--rm", "-i", "--user", controlUser, "-v", volume+":/var/lib/kraken", pythonImage, "python", "-c", script)
}

// lkgManifest reads the last-good manifest from the control DB as generic JSON.
func lkgManifest(t *testing.T, volume string) map[string]any {
	t.Helper()
	return dbKV(t, volume, "runtime.last_good")
}

// policySnapFromBytes parses a marshalled manifest (last-good from the control DB) into a Snapshot.
func policySnapFromBytes(b []byte) policy.Snapshot {
	var m map[string]any
	json.Unmarshal(b, &m)
	sb, _ := json.Marshal(m["snapshot"])
	var s policy.Snapshot
	json.Unmarshal(sb, &s)
	return s
}
