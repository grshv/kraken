//go:build integration

package integration

import (
	"path/filepath"
	"testing"
	"time"

	"kraken.local/kraken/internal/policy"
)

// DNS fixture answers A/AAAA with TTL 0; rebind.test flips to 127.0.0.1 after
// the first lookup so the TTL-expiry denial path can be exercised.
const dnsFixtureCode = `import socket,struct,threading
A={'public.test':'11.251.17.10','loopback.test':'127.0.0.1','metadata.test':'169.254.169.254','private.test':'10.20.30.40'}
AAAA={'v6loop.test':'::1'}
first=[True]
def qn(d,o):
 l=[]
 while d[o]:
  n=d[o];l.append(d[o+1:o+1+n]);o+=1+n
 return b'.'.join(l),o+1
def rep(d,a,s):
 if len(d)<12:return
 tid=d[:2];n,o=qn(d,12);qt=struct.unpack('!H',d[o:o+2])[0];q=d[12:o+4]
 ns=n.decode();rd=None
 if ns=='rebind.test' and qt==1:
  if first[0]:rd=socket.inet_aton('11.251.17.10');first[0]=False
  else:rd=socket.inet_aton('127.0.0.1')
 elif qt==1 and ns in A:rd=socket.inet_aton(A[ns])
 elif qt==28 and ns in AAAA:rd=socket.inet_pton(socket.AF_INET6,AAAA[ns])
 known=ns in A or ns in AAAA or ns=='rebind.test'
 rc=3 if not(rd or known) else 0
 an=1 if rd else 0
 fl=0x8183 if rc==3 else 0x8180
 hdr=tid+struct.pack('!HHHHH',fl,1,an,0,0)
 ans=b''
 if rd:ans=b'\xc0\x0c'+struct.pack('!HHIH',qt,1,0,len(rd))+rd
 s.sendto(hdr+q+ans,a)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('0.0.0.0',53))
while True:
 d,a=s.recvfrom(512);threading.Thread(target=rep,args=(d,a,s),daemon=True).start()
`

// HTTP origin at the fixture IP logs every accessed path to /tmp/access.log
// so the suite can prove forbidden destinations never reached it.
const networkOriginCode = `import http.server,pathlib
LOG=pathlib.Path('/tmp/access.log');LOG.touch(exist_ok=True)
class H(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  with LOG.open('a') as f: f.write(self.path+'\n')
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin')
 def log_message(self,*a):pass
http.server.HTTPServer(('0.0.0.0',80),H).serve_forever()
`

func TestNetwork(t *testing.T) {
	t.Parallel()
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	const subnet, base = "11.251.17.0/24", "11.251.17"
	inst := launchInstance(t, &r, fixtures, subnet, base)

	t.Run("HTTPS trust, bootstrap, login", func(t *testing.T) {
		// launchInstance already logged in and waited for squid up; status is up.
		s := inst.panel.status()
		squid, _ := s["squid"].(map[string]any)
		if squid["state"] != "up" {
			t.Fatalf("squid not up: %v", s)
		}
	})

	// HTTP origin, DNS responder and a client container on the isolated network.
	writeFile(filepath.Join(fixtures, "origin.py"), []byte(networkOriginCode))
	writeFile(filepath.Join(fixtures, "dns.py"), []byte(dnsFixtureCode))
	originName := ""
	for _, fc := range []struct {
		suffix, ip string
		cmd        []string
	}{
		{"origin", base + ".10", []string{"python", "/fixtures/origin.py"}},
		{"dns", base + ".53", []string{"python", "/fixtures/dns.py"}},
		{"client", base + ".20", []string{"python", "-c", "import time;time.sleep(3600)"}},
	} {
		n := r.addContainer(randName("sc-net") + "-" + fc.suffix)
		if fc.suffix == "origin" {
			originName = n
		}
		args := []string{"run", "-d", "--name", n, "--network", inst.network, "--ip", fc.ip, "-v", fixtures + ":/fixtures:ro", pythonImage}
		args = append(args, fc.cmd...)
		docker(t, args...)
	}
	client := r.containers[len(r.containers)-1]

	// all_web policy with DNS pointed at the fixture and short connect timeout.
	snap := policySnap(t, inst.panel)
	profile := snap.TLSProfiles[0].ID
	allID := randID()
	snap.Policies = []policy.Policy{{ID: allID, Name: "All web network", Mode: "all_web", Destinations: nil, TLSProfileID: profile}}
	snap.Clients = []policy.Client{{ID: randID(), Name: "network client", Networks: []string{base + ".20"}, Enabled: true, PolicyID: allID}}
	s := *snap.Settings
	s.DNSNameservers = []string{base + ".53"}
	s.PositiveDNSTTL = 2
	s.NegativeDNSTTL = 1
	s.AllowPrivateDestinations = false
	s.ConnectTimeout = 3
	s.ReadTimeout = 3
	snap.Settings = &s
	inst.panel.apply(t, snap, inst.panel.draftVersion())

	t.Run("all_web policy with DNS settings applied", func(t *testing.T) {
		// apply() already asserted success; the subtest records the milestone.
	})

	// fetch returns (status, x-squid-error) via a Python client through the proxy.
	fetch := func(target string) (int, string) {
		code := "import http.client\n" +
			"c=http.client.HTTPConnection('proxy',3128,timeout=8)\n" +
			"c.request('GET'," + pyRepr(target) + ");r=c.getresponse();r.read()\n" +
			"print(r.status);print(r.getheader('X-Squid-Error') or '')\n"
		out, _ := dockerExec(client, "python", "-c", code)
		lines := splitLines(out)
		st, err := 0, ""
		if len(lines) > 0 {
			scanInt(lines[0], &st)
		}
		if len(lines) > 1 {
			err = lines[1]
		}
		return st, err
	}
	connectFetch := func(target string) (int, string) {
		code := "import socket\n" +
			"s=socket.create_connection(('proxy',3128),timeout=8)\n" +
			"s.sendall(b'CONNECT " + target + " HTTP/1.1\\r\\nHost: " + target + "\\r\\n\\r\\n')\n" +
			"data=b''\n" +
			"while b'\\r\\n\\r\\n' not in data:\n" +
			" chunk=s.recv(1024)\n" +
			" if not chunk:break\n" +
			" data+=chunk\n" +
			"status_line=data.split(b'\\r\\n')[0]\n" +
			"parts=status_line.split()\n" +
			"status=int(parts[1]) if len(parts)>1 else 0\n" +
			"err=''\n" +
			"for line in data.split(b'\\r\\n'):\n" +
			" if line.lower().startswith(b'x-squid-error:'):\n" +
			"  err=line.split(b':',1)[1].strip().decode()\n" +
			"print(status);print(err)\n"
		out, _ := dockerExec(client, "python", "-c", code)
		lines := splitLines(out)
		st, err := 0, ""
		if len(lines) > 0 {
			scanInt(lines[0], &st)
		}
		if len(lines) > 1 {
			err = lines[1]
		}
		return st, err
	}
	aclDenied := func(status int, err string) bool {
		return status == 403 && hasPrefix(err, "ERR_ACCESS_DENIED")
	}
	_ = aclDenied

	t.Run("1. DNS fixture resolves public.test to origin (200)", func(t *testing.T) {
		waitFor(t, 30, func() error {
			st, _ := fetch("http://public.test/control")
			if st != 200 {
				return errf("public.test/control: %d", st)
			}
			return nil
		})
	})

	t.Run("2. literal IP destinations denied by protected/private ACL", func(t *testing.T) {
		for _, target := range []string{
			"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data/", "http://10.0.0.1/",
			"http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://[fe80::1]/",
			"http://[2002:7f00:1::1]/", "http://[2001:0:7f00:1::1]/",
			"http://[64:ff9b:1::7f00:1]/", "http://[64:ff9b::a9fe:a9fe]/",
		} {
			st, err := fetch(target)
			if st != 403 || !hasPrefix(err, "ERR_ACCESS_DENIED") {
				t.Errorf("%s: expected 403 ERR_ACCESS_DENIED, got %d %q", target, st, err)
			}
		}
	})

	t.Run("3. DNS-resolved protected/private names denied by ACL", func(t *testing.T) {
		for _, target := range []string{"http://loopback.test/", "http://metadata.test/", "http://private.test/", "http://v6loop.test/"} {
			st, err := fetch(target)
			if st != 403 || !hasPrefix(err, "ERR_ACCESS_DENIED") {
				t.Errorf("%s: expected 403 ERR_ACCESS_DENIED, got %d %q", target, st, err)
			}
		}
	})

	t.Run("4. CONNECT to protected/private denied (403)", func(t *testing.T) {
		for _, target := range []string{"127.0.0.1:443", "[::1]:443", "metadata.test:443"} {
			st, _ := connectFetch(target)
			if st != 403 {
				t.Errorf("CONNECT %s: expected 403, got %d", target, st)
			}
		}
	})

	t.Run("5. alternative loopback encodings do not reach origin", func(t *testing.T) {
		for _, target := range []string{"http://2130706433/", "http://0x7f000001/", "http://0177.0.0.1/"} {
			st, _ := fetch(target)
			if st == 200 {
				t.Errorf("%s: expected non-200, got 200", target)
			}
		}
	})

	t.Run("6. DNS rebinding denied after TTL expiry", func(t *testing.T) {
		st, _ := fetch("http://rebind.test/rebind-first")
		if st != 200 {
			t.Fatalf("rebind.test first expected 200, got %d", st)
		}
		time.Sleep(3 * time.Second)
		st, err := fetch("http://rebind.test/rebind-second")
		if st != 403 || !hasPrefix(err, "ERR_ACCESS_DENIED") {
			t.Fatalf("rebind.test second expected 403 ERR_ACCESS_DENIED, got %d %q", st, err)
		}
	})

	t.Run("7. allow_private lifts private dst; protected ranges still denied", func(t *testing.T) {
		appliedRev := inst.panel.status()["applied_revision"].(string)
		relaxed := policySnapRev(t, inst.panel, appliedRev)
		relaxed.Settings.AllowPrivateDestinations = true
		inst.panel.apply(t, relaxed, inst.panel.draftVersion())
		st, err := fetch("http://private.test/")
		if st == 403 && hasPrefix(err, "ERR_ACCESS_DENIED") {
			t.Fatalf("private.test with allow_private should not be 403 ACL, got %d %q", st, err)
		}
		for _, target := range []string{"http://loopback.test/", "http://metadata.test/", "http://[2002:7f00:1::1]/"} {
			st, err := fetch(target)
			if st != 403 || !hasPrefix(err, "ERR_ACCESS_DENIED") {
				t.Errorf("%s still 403 ACL expected with allow_private, got %d %q", target, st, err)
			}
		}
	})

	t.Run("8. origin log shows only control requests, no forbidden traffic", func(t *testing.T) {
		out, err := dockerExec(originName, "cat", "/tmp/access.log")
		if err != nil {
			t.Fatalf("read origin log: %v", err)
		}
		got := map[string]bool{}
		for _, p := range splitLines(out) {
			if p != "" {
				got[p] = true
			}
		}
		want := map[string]bool{"/control": true, "/rebind-first": true}
		if len(got) != len(want) {
			t.Fatalf("unexpected origin requests: %v", got)
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("missing %q in origin log: %v", k, got)
			}
		}
	})
}

// policySnap fetches the current draft snapshot as a typed policy.Snapshot.
func policySnap(t *testing.T, p *Panel) policy.Snapshot {
	t.Helper()
	d := p.draftOK(t)
	b, _ := jsonMarshal(d)
	var v struct {
		Snapshot policy.Snapshot `json:"snapshot"`
	}
	jsonUnmarshal(b, &v)
	return v.Snapshot
}

// policySnapRev fetches a stored revision's snapshot as a typed policy.Snapshot.
func policySnapRev(t *testing.T, p *Panel, revID string) policy.Snapshot {
	t.Helper()
	r := waitForResult(t, 60, func() (map[string]any, error) {
		resp, status := p.api("GET", "/revisions/"+revID, nil, "")
		if status < 200 || status >= 300 {
			return nil, errf("revision %s: %d", revID, status)
		}
		return resp, nil
	})
	b, _ := jsonMarshal(r)
	var v struct {
		Snapshot policy.Snapshot `json:"snapshot"`
	}
	jsonUnmarshal(b, &v)
	return v.Snapshot
}
