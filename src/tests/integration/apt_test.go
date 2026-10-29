//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kraken.local/kraken/internal/policy"
)

// aptFixtureScript generates a signed flat APT repository with one .deb,
// serving over HTTP and HTTPS. Runs inside the sc-apt-fixture image (built once
// from this script + a small Dockerfile). Mirrors apt.py's FIXTURE_SCRIPT.
const aptFixtureScript = `import gzip,hashlib,http.server,os,pathlib,ssl,subprocess,threading
from email.utils import formatdate
REPO=pathlib.Path('/repo');FIX=pathlib.Path('/fixtures')
REPO.mkdir(parents=True,exist_ok=True);FIX.mkdir(parents=True,exist_ok=True)
env=dict(os.environ,GNUPGHOME='/tmp/gnupg');pathlib.Path('/tmp/gnupg').mkdir(mode=0o700,exist_ok=True)
subprocess.run(['gpg','--batch','--yes','--pinentry-mode','loopback','--passphrase','','--quick-generate-key','sc-fixture <sc-fixture@example.test>','default','default','0'],env=env,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
pkg=pathlib.Path('/tmp/pkg');(pkg/'DEBIAN').mkdir(parents=True,exist_ok=True);(pkg/'usr/bin').mkdir(parents=True,exist_ok=True)
(pkg/'DEBIAN/control').write_text('Package: sc-fixture-hello\nVersion: 1.0\nArchitecture: all\nMaintainer: SC Fixture <sc-fixture@example.test>\nDescription: SC fixture hello\n')
s=pkg/'usr/bin/sc-fixture-hello';s.write_text('#!/bin/sh\necho hello\n');s.chmod(0o755)
deb=REPO/'sc-fixture-hello_1.0_all.deb';subprocess.run(['dpkg-deb','--build',str(pkg),str(deb)],check=True)
dd=deb.read_bytes();ds=hashlib.sha256(dd).hexdigest();dn=len(dd)
pt='Package: sc-fixture-hello\nVersion: 1.0\nArchitecture: all\nMaintainer: SC Fixture <sc-fixture@example.test>\nDescription: SC fixture hello\nFilename: sc-fixture-hello_1.0_all.deb\nSize: %d\nSHA256: %s\n\n'%(dn,ds)
(REPO/'Packages').write_text(pt)
with gzip.open(str(REPO/'Packages.gz'),'wb') as f: f.write(pt.encode())
pd=(REPO/'Packages').read_bytes();pg=(REPO/'Packages.gz').read_bytes()
rl=['Origin: SC Fixture','Label: SC Fixture','Suite: stable','Codename: stable','Date: '+formatdate(timeval=None,localtime=False,usegmt=True),'Architectures: all amd64','Components: main','Description: SC fixture repository','MD5Sum:',' '+hashlib.md5(pd).hexdigest()+' '+str(len(pd))+' Packages',' '+hashlib.md5(pg).hexdigest()+' '+str(len(pg))+' Packages.gz','SHA1:',' '+hashlib.sha1(pd).hexdigest()+' '+str(len(pd))+' Packages',' '+hashlib.sha1(pg).hexdigest()+' '+str(len(pg))+' Packages.gz','SHA256:',' '+hashlib.sha256(pd).hexdigest()+' '+str(len(pd))+' Packages',' '+hashlib.sha256(pg).hexdigest()+' '+str(len(pg))+' Packages.gz','']
(REPO/'Release').write_text('\n'.join(rl))
for args in (['gpg','--batch','--yes','--pinentry-mode','loopback','--passphrase','','--local-user','sc-fixture','--clearsign','--output',str(REPO/'InRelease'),str(REPO/'Release')],['gpg','--batch','--yes','--pinentry-mode','loopback','--passphrase','','--local-user','sc-fixture','--detach-sign','--output',str(REPO/'Release.gpg'),str(REPO/'Release')]):
    subprocess.run(args,env=env,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
with open(str(FIX/'repo.gpg'),'wb') as f:
    subprocess.run(['gpg','--export','sc-fixture'],env=env,stdout=f,check=True)
subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout','/tmp/ca.key','-out',str(FIX/'ca.crt'),'-subj','/CN=SC Fixture CA'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
subprocess.run(['openssl','req','-newkey','rsa:2048','-nodes','-keyout','/tmp/server.key','-out','/tmp/server.csr','-subj','/CN=repo.test'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
pathlib.Path('/tmp/san.ext').write_text('subjectAltName=DNS:repo.test\n')
subprocess.run(['openssl','x509','-req','-in','/tmp/server.csr','-CA',str(FIX/'ca.crt'),'-CAkey','/tmp/ca.key','-CAcreateserial','-days','1','-out','/tmp/server.crt','-extfile','/tmp/san.ext'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
class H(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def do_GET(self):
        p=REPO/self.path.lstrip('/')
        if p.is_file():
            d=p.read_bytes();self.send_response(200);self.send_header('Content-Type','application/octet-stream');self.send_header('Content-Length',str(len(d)));self.end_headers();self.wfile.write(d)
        else:
            self.send_response(404);self.send_header('Content-Length','0');self.end_headers()
    def log_message(self,*a):pass
(FIX/'ready').write_text('ok')
httpd=http.server.HTTPServer(('0.0.0.0',80),H);threading.Thread(target=httpd.serve_forever,daemon=True).start()
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/tmp/server.crt','/tmp/server.key')
httpsd=http.server.HTTPServer(('0.0.0.0',443),H);httpsd.socket=ctx.wrap_socket(httpsd.socket,server_side=True);httpsd.serve_forever()
`

const fixtureImage = "sc-apt-fixture:1"

// aptClientImage is the APT test client. Ubuntu (not the Alpine-based kraken
// image) because the client needs apt-get, which Alpine does not ship.
const aptClientImage = "ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"

// aptRun execs apt-get inside the client container and returns combined output
// plus the exit status, matching run_exec in apt.py. A 120s ceiling prevents a
// hung apt-get from stalling the suite.
func aptRun(client string, args ...string) (int, string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	full := append([]string{"exec", client}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	err := cmd.Run()
	out := combined.String()
	if err != nil {
		return 1, out, ""
	}
	return 0, out, ""
}

func TestApt(t *testing.T) {
	t.Parallel()
	fixtures := t.TempDir()
	r := resources{}
	r.track(t)
	const subnet, base = "11.252.17.0/24", "11.252.17"

	// Build the fixture image only if it does not already exist, as Python did.
	if _, ok := dockerOK(t, "image", "inspect", fixtureImage); !ok {
		writeFile(filepath.Join(fixtures, "apt_fixture.py"), []byte(aptFixtureScript))
		writeFile(filepath.Join(fixtures, "Dockerfile.apt"), []byte(
			"FROM "+pythonImage+"\n"+
				"RUN apk add --no-cache gnupg dpkg openssl\n"+
				"COPY apt_fixture.py /apt_fixture.py\n"+
				`CMD ["python", "/apt_fixture.py"]`+"\n"))
		docker(t, "build", "-f", filepath.Join(fixtures, "Dockerfile.apt"), "-t", fixtureImage, fixtures)
	}

	inst := launchInstance(t, &r, fixtures, subnet, base)

	t.Run("HTTPS trust, bootstrap, login", func(t *testing.T) {
		s := inst.panel.status()
		squid, _ := s["squid"].(map[string]any)
		if squid["state"] != "up" {
			t.Fatalf("squid not up: %v", s)
		}
	})

	fixture := r.addContainer(randName("sc-apt") + "-fixture")
	docker(t, "run", "-d", "--name", fixture, "--network", inst.network, "--ip", base+".10", "--network-alias", "repo.test", fixtureImage)
	waitFor(t, 60, func() error {
		if _, ok := dockerOK(t, "exec", fixture, "test", "-f", "/fixtures/ready"); !ok {
			return errf("fixture not ready")
		}
		return nil
	})
	docker(t, "cp", fixture+":/fixtures/repo.gpg", filepath.Join(fixtures, "repo.gpg"))
	docker(t, "cp", fixture+":/fixtures/ca.crt", filepath.Join(fixtures, "ca.crt"))

	client := r.addContainer(randName("sc-apt") + "-client")
	docker(t, "run", "-d", "--name", client, "--network", inst.network, "--ip", base+".20", "--entrypoint", "sleep", aptClientImage, "3600")
	docker(t, "exec", client, "sh", "-c", "rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*")
	docker(t, "exec", client, "mkdir", "-p", "/fixtures")
	docker(t, "cp", filepath.Join(fixtures, "repo.gpg"), client+":/fixtures/repo.gpg")
	docker(t, "cp", filepath.Join(fixtures, "ca.crt"), client+":/fixtures/ca.crt")
	writeFile(filepath.Join(fixtures, "99sc-proxy"), []byte(
		`Acquire::http::Proxy "http://proxy:3128/";
Acquire::https::Proxy "http://proxy:3128/";
Acquire::https::CAInfo "/fixtures/ca.crt";
Acquire::Retries "1";
Acquire::http::Timeout "10";
Acquire::https::Timeout "10";
Acquire::AllowInsecureRepositories "false";
Acquire::AllowWeakRepositories "false";
Acquire::AllowDowngradeToInsecureRepositories "false";
`))
	docker(t, "cp", filepath.Join(fixtures, "99sc-proxy"), client+":/etc/apt/apt.conf.d/99sc-proxy")
	writeFile(filepath.Join(fixtures, "sources.list"), []byte("deb [signed-by=/fixtures/repo.gpg] http://repo.test/ ./\n"))
	docker(t, "cp", filepath.Join(fixtures, "sources.list"), client+":/etc/apt/sources.list")
	docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")

	// isSigError mirrors apt.py: a non-zero exit or a warning/error line naming
	// known GPG failure modes counts as a signature rejection.
	isSigError := func(rc int, out string) bool {
		if rc != 0 {
			return true
		}
		for _, line := range strings.Split(out, "\n") {
			if (strings.HasPrefix(line, "W:") || strings.HasPrefix(line, "E:")) &&
				containsAny(line, "no_pubkey", "badsig", "not signed", "clearsigned", "gpg error") {
				return true
			}
		}
		return false
	}

	t.Run("1. client without policy cannot update (proxy denies)", func(t *testing.T) {
		// Run apt-get update before any policy is applied; the proxy denies all
		// traffic from an unknown source.
		docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
		if rc, _, _ := aptRun(client, "apt-get", "update"); rc == 0 {
			t.Fatal("apt-get update should fail without policy")
		}
	})

	// Apply an allowlist policy with repo.test for the client source.
	snap := policySnap(t, inst.panel)
	profile := snap.TLSProfiles[0].ID
	allowID := randID()
	snap.Policies = []policy.Policy{{ID: allowID, Name: "Repo allowlist", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "repo.test", IncludeSubdomains: false}}, TLSProfileID: profile}}
	snap.Clients = []policy.Client{{ID: randID(), Name: "apt client", Networks: []string{base + ".20"}, Enabled: true, PolicyID: allowID}}
	inst.panel.apply(t, snap, inst.panel.draftVersion())

	t.Run("allowlist policy with repo.test applied", func(t *testing.T) {
		// apply() above asserted success; this records the milestone.
	})

	t.Run("2. HTTP apt-get update + install + run", func(t *testing.T) {
		docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
		if rc, _, _ := aptRun(client, "apt-get", "update"); rc != 0 {
			t.Fatal("apt-get update HTTP failed")
		}
		if rc, _, _ := aptRun(client, "apt-get", "install", "-y", "sc-fixture-hello"); rc != 0 {
			t.Fatal("apt-get install HTTP failed")
		}
		rc, out, _ := aptRun(client, "sc-fixture-hello")
		if rc != 0 || strings.TrimSpace(out) != "hello" {
			t.Fatalf("sc-fixture-hello failed: %q", out)
		}
	})

	t.Run("2. HTTPS apt-get update + install via CONNECT", func(t *testing.T) {
		docker(t, "exec", client, "apt-get", "remove", "-y", "sc-fixture-hello")
		docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
		writeFile(filepath.Join(fixtures, "sources-https.list"), []byte("deb [signed-by=/fixtures/repo.gpg] https://repo.test/ ./\n"))
		docker(t, "cp", filepath.Join(fixtures, "sources-https.list"), client+":/etc/apt/sources.list")
		if rc, _, _ := aptRun(client, "apt-get", "update"); rc != 0 {
			t.Fatal("apt-get update HTTPS failed")
		}
		if rc, _, _ := aptRun(client, "apt-get", "install", "-y", "sc-fixture-hello"); rc != 0 {
			t.Fatal("apt-get install HTTPS failed")
		}
		rc, out, _ := aptRun(client, "sc-fixture-hello")
		if rc != 0 || strings.TrimSpace(out) != "hello" {
			t.Fatalf("sc-fixture-hello HTTPS failed: %q", out)
		}
	})

	t.Run("3. tampered InRelease detected, install blocked", func(t *testing.T) {
		docker(t, "exec", client, "apt-get", "remove", "-y", "sc-fixture-hello")
		docker(t, "exec", client, "apt-get", "clean")
		docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
		docker(t, "cp", filepath.Join(fixtures, "sources.list"), client+":/etc/apt/sources.list")
		docker(t, "exec", fixture, "cp", "/repo/InRelease", "/repo/InRelease.bak")
		docker(t, "exec", fixture, "cp", "/repo/Release.gpg", "/repo/Release.gpg.bak")
		docker(t, "exec", fixture, "sh", "-c", "echo tampered > /repo/InRelease; echo tampered > /repo/Release.gpg")
		rc, out, _ := aptRun(client, "apt-get", "update")
		if !isSigError(rc, out) {
			t.Fatalf("tampered InRelease should cause signature error, got rc=%d out=%q", rc, out)
		}
		if rc, _, _ := aptRun(client, "apt-get", "install", "-y", "sc-fixture-hello"); rc == 0 {
			t.Fatal("install should fail after tampered update")
		}
		docker(t, "exec", fixture, "cp", "/repo/InRelease.bak", "/repo/InRelease")
		docker(t, "exec", fixture, "cp", "/repo/Release.gpg.bak", "/repo/Release.gpg")
	})

	t.Run("4. allowlist without repo.test blocks update", func(t *testing.T) {
		docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
		snap2 := snap
		snap2.Policies = []policy.Policy{{ID: allowID, Name: "Repo allowlist", Mode: "allowlist", Destinations: []policy.Destination{{Domain: "other.test"}}, TLSProfileID: profile}}
		snap2.Clients = snap.Clients
		inst.panel.apply(t, snap2, inst.panel.draftVersion())
		if rc, _, _ := aptRun(client, "apt-get", "update"); rc == 0 {
			t.Fatal("apt-get update should fail without repo.test in allowlist")
		}
	})

	if os.Getenv("KRAKEN_TEST_REAL_APT") == "1" {
		t.Run("5. real APT archive update and download through proxy", func(t *testing.T) {
			docker(t, "exec", client, "sh", "-c", "rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*")
			arch, _ := dockerExec(client, "dpkg", "--print-architecture")
			var mirrors []string
			if strings.TrimSpace(arch) == "amd64" {
				mirrors = []string{"http://archive.ubuntu.com/ubuntu resolute main", "http://archive.ubuntu.com/ubuntu resolute-updates main", "http://security.ubuntu.com/ubuntu resolute-security main"}
			} else {
				mirrors = []string{"http://ports.ubuntu.com/ubuntu-ports resolute main", "http://ports.ubuntu.com/ubuntu-ports resolute-updates main", "http://ports.ubuntu.com/ubuntu-ports resolute-security main"}
			}
			var b strings.Builder
			for _, m := range mirrors {
				b.WriteString("deb " + m + "\n")
			}
			writeFile(filepath.Join(fixtures, "sources-ubuntu.list"), []byte(b.String()))
			docker(t, "cp", filepath.Join(fixtures, "sources-ubuntu.list"), client+":/etc/apt/sources.list")
			docker(t, "exec", client, "sh", "-c", "rm -rf /var/lib/apt/lists/*")
			snap3 := snap
			snap3.Policies = []policy.Policy{{ID: allowID, Name: "Repo allowlist", Mode: "allowlist", Destinations: []policy.Destination{
				{Domain: "archive.ubuntu.com", IncludeSubdomains: true},
				{Domain: "security.ubuntu.com", IncludeSubdomains: true},
				{Domain: "ports.ubuntu.com", IncludeSubdomains: true},
			}, TLSProfileID: profile}}
			snap3.Clients = snap.Clients
			s := *snap3.Settings
			s.DNSNameservers = []string{"1.1.1.1", "8.8.8.8"}
			snap3.Settings = &s
			inst.panel.apply(t, snap3, inst.panel.draftVersion())
			if rc, _, _ := aptRun(client, "apt-get", "update"); rc != 0 {
				t.Fatal("real APT update failed")
			}
			if rc, _, _ := aptRun(client, "sh", "-c", "cd /tmp && apt-get download hello"); rc != 0 {
				t.Fatal("apt-get download hello failed")
			}
		})
	} else {
		t.Run("5. real APT archive", func(t *testing.T) {
			t.Skip("real APT archive (set KRAKEN_TEST_REAL_APT=1)")
		})
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
