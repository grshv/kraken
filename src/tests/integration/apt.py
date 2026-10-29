#!/usr/bin/env python3
"""APT integration tests on a real Squid image.

Fixtures: proxy, APT repository fixture (11.252.17.10, alias repo.test) serving
a signed flat repo over HTTP and HTTPS, client (11.252.17.20) from the panel
image with apt-get/gpgv. Verifies allowlist-based proxy access, GPG signature
enforcement and HTTPS CONNECT tunnelling for package management.
"""
import copy
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

IMAGE = os.environ.get('SC_TEST_IMAGE', 'squid-control:dev')
CONTROL_USER = os.environ.get('SC_CONTROL_USER', '10013:10013')
PYTHON = 'python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916'
FIXTURE_IMAGE = 'sc-apt-fixture:1'
name = 'sc-apt-' + uuid.uuid4().hex[:8]
network, volume = name + '-net', name + '-data'
containers = []

FIXTURE_SCRIPT = r'''import gzip,hashlib,http.server,os,pathlib,ssl,subprocess,threading
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
    def do_GET(self):
        p=REPO/self.path.lstrip('/')
        if p.is_file():
            d=p.read_bytes();self.send_response(200);self.send_header('Content-Type','application/octet-stream');self.send_header('Content-Length',str(len(d)));self.end_headers();self.wfile.write(d)
        else:
            self.send_response(404);self.end_headers()
    def log_message(self,*a):pass
(FIX/'ready').write_text('ok')
httpd=http.server.HTTPServer(('0.0.0.0',80),H);threading.Thread(target=httpd.serve_forever,daemon=True).start()
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/tmp/server.crt','/tmp/server.key')
httpsd=http.server.HTTPServer(('0.0.0.0',443),H);httpsd.socket=ctx.wrap_socket(httpsd.socket,server_side=True);httpsd.serve_forever()
'''

def docker(*args, input=None):
    r = subprocess.run(['docker', *args], input=input, text=True, capture_output=True)
    if r.returncode:
        raise RuntimeError(f'docker {args[0]} failed: {r.stderr[-2000:]} {r.stdout[-1000:]}')
    return r.stdout.strip()

def run_exec(container, *args, timeout=120):
    return subprocess.run(['docker', 'exec', container, *args], capture_output=True, text=True, timeout=timeout)

def wait_for(fn, seconds=30):
    last = None
    for _ in range(seconds * 5):
        try:
            return fn()
        except Exception as exc:
            last = exc
            time.sleep(.2)
    raise RuntimeError(f'timed out: {last}')

with tempfile.TemporaryDirectory(prefix='squid-control-apt-') as temporary:
    fixtures = Path(temporary)
    try:
        # Build the fixture image only if it does not already exist.
        inspect = subprocess.run(['docker', 'image', 'inspect', FIXTURE_IMAGE], capture_output=True)
        if inspect.returncode != 0:
            fixtures.joinpath('apt_fixture.py').write_text(FIXTURE_SCRIPT)
            fixtures.joinpath('Dockerfile.apt').write_text(
                'FROM ' + PYTHON + '\n'
                'RUN apk add --no-cache gnupg dpkg openssl\n'
                'COPY apt_fixture.py /apt_fixture.py\n'
                'CMD ["python", "/apt_fixture.py"]\n'
            )
            docker('build', '-f', str(fixtures/'Dockerfile.apt'), '-t', FIXTURE_IMAGE, str(fixtures))

        docker('network', 'create', '--subnet', '11.252.17.0/24', network)
        docker('volume', 'create', volume)
        docker('run', '--rm', '--user', CONTROL_USER, '-v', volume+':/data', '--entrypoint', '/usr/local/bin/squid-control', IMAGE, 'dev-cert')
        password = secrets.token_urlsafe(24)
        docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', volume+':/data', '--entrypoint', '/usr/local/bin/squid-control', IMAGE, 'bootstrap', '--password-stdin', input=password+'\n')
        proxy = name+'-proxy'; containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.252.17.2', '-p', '127.0.0.1::8443', '-v', volume+':/data', '-e', 'SC_PUBLIC_ORIGIN=https://localhost', '-e', 'SC_PROXY_CERT=/data/certs/proxy.crt', '-e', 'SC_PROXY_KEY=/data/certs/proxy.key', IMAGE)
        port = docker('port', proxy, '8443/tcp').rsplit(':',1)[1]
        origin = 'https://localhost:'+port
        docker('rm', '-f', '-v', proxy); containers.remove(proxy)
        containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.252.17.2', '-p', '127.0.0.1:'+port+':8443', '-v', volume+':/data', '-e', 'SC_PUBLIC_ORIGIN='+origin, '-e', 'SC_PROXY_CERT=/data/certs/proxy.crt', '-e', 'SC_PROXY_KEY=/data/certs/proxy.key', IMAGE)
        docker('cp', proxy+':/data/certs/panel.crt', str(fixtures/'panel.crt'))
        context = ssl.create_default_context(cafile=str(fixtures/'panel.crt'))
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        csrf = ''
        def api(path, method='GET', body=None, key=None):
            data = None if body is None else json.dumps(body).encode()
            headers = {'Content-Type':'application/json', 'Origin':origin, 'X-CSRF-Token':csrf}
            if key: headers['Idempotency-Key'] = key
            req = urllib.request.Request(origin+'/api/v1'+path, data=data, method=method, headers=headers)
            with opener.open(req, timeout=15) as response:
                return json.load(response) if response.status != 204 else None
        session = wait_for(lambda: api('/session'))
        csrf = session['csrf_token']
        session = api('/session','POST',{'username':'admin','password':password})
        csrf = session['csrf_token']
        status = wait_for(lambda: api('/status'))
        assert status['squid']['state'] == 'up', status
        print('PASS: HTTPS trust, bootstrap, login')

        fixture = name+'-fixture'; containers.append(fixture)
        docker('run', '-d', '--name', fixture, '--network', network, '--ip', '11.252.17.10', '--network-alias', 'repo.test', FIXTURE_IMAGE)
        def fixture_ready():
            docker('exec', fixture, 'test', '-f', '/fixtures/ready')
            return True
        wait_for(fixture_ready, seconds=60)
        docker('cp', fixture+':/fixtures/repo.gpg', str(fixtures/'repo.gpg'))
        docker('cp', fixture+':/fixtures/ca.crt', str(fixtures/'ca.crt'))

        client = name+'-client'; containers.append(client)
        docker('run', '-d', '--name', client, '--network', network, '--ip', '11.252.17.20', '--entrypoint', 'sleep', IMAGE, '3600')
        docker('exec', client, 'sh', '-c', 'rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*')
        docker('exec', client, 'mkdir', '-p', '/fixtures')
        docker('cp', str(fixtures/'repo.gpg'), client+':/fixtures/repo.gpg')
        docker('cp', str(fixtures/'ca.crt'), client+':/fixtures/ca.crt')
        fixtures.joinpath('99sc-proxy').write_text(
            'Acquire::http::Proxy "http://proxy:3128/";\n'
            'Acquire::https::Proxy "http://proxy:3128/";\n'
            'Acquire::https::CAInfo "/fixtures/ca.crt";\n'
            'Acquire::Retries "1";\n'
            'Acquire::http::Timeout "10";\n'
            'Acquire::https::Timeout "10";\n'
            'Acquire::AllowInsecureRepositories "false";\n'
            'Acquire::AllowWeakRepositories "false";\n'
            'Acquire::AllowDowngradeToInsecureRepositories "false";\n')
        docker('cp', str(fixtures/'99sc-proxy'), client+':/etc/apt/apt.conf.d/99sc-proxy')
        fixtures.joinpath('sources.list').write_text('deb [signed-by=/fixtures/repo.gpg] http://repo.test/ ./\n')
        docker('cp', str(fixtures/'sources.list'), client+':/etc/apt/sources.list')
        docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')

        def apply_snapshot(snapshot, expected_version):
            d=api('/configuration/draft','PUT',{'expected_version':expected_version,'snapshot':snapshot})
            revision=api('/revisions','POST',{'expected_draft_version':d['version']})
            preview=api('/revisions/'+revision['id']+'/preview')
            def operation(action,validation=None):
                body={'action':action,'revision_id':revision['id'],'expected_applied_revision':preview['expected_applied_revision']}
                if validation: body['validation_operation_id']=validation
                key=str(uuid.uuid4());op=api('/operations','POST',body,key)
                assert api('/operations','POST',body,key)['id']==op['id']
                def finished():
                    current=api('/operations/'+op['id'])
                    if current['status'] in ['pending','validating','reloading']:raise RuntimeError(current['status'])
                    return current
                return wait_for(finished)
            validated=operation('validate');assert validated['status']=='validated',validated
            applied=operation('apply',validated['id']);assert applied['status']=='succeeded',applied
            return d

        def is_sig_error(r):
            if r.returncode != 0:
                return True
            out = r.stdout + r.stderr
            for line in out.splitlines():
                if (line.startswith('W:') or line.startswith('E:')) and any(kw in line.lower() for kw in ['no_pubkey','badsig','not signed','clearsigned','gpg error']):
                    return True
            return False

        # 1. Without policy the proxy denies all traffic; apt-get update fails.
        r = run_exec(client, 'apt-get', 'update', timeout=60)
        assert r.returncode != 0, 'apt-get update should fail without policy'
        print('PASS: 1. client without policy cannot update (proxy denies)')

        # Apply allowlist policy with repo.test for the client source.
        d = api('/configuration/draft'); snap = d['snapshot']; profile = snap['tls_profiles'][0]['id']
        allow_id = str(uuid.uuid4())
        snap['policies'] = [{'id':allow_id,'name':'Repo allowlist','mode':'allowlist','destinations':[{'domain':'repo.test','include_subdomains':False}],'tls_profile_id':profile}]
        snap['clients'] = [{'id':str(uuid.uuid4()),'name':'apt client','source_networks':['11.252.17.20'],'enabled':True,'policy_id':allow_id}]
        d = apply_snapshot(snap, d['version'])
        print('PASS: allowlist policy with repo.test applied')

        # 2. HTTP: apt-get update + install + run.
        docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')
        r = run_exec(client, 'apt-get', 'update', timeout=60)
        assert r.returncode == 0, f'apt-get update HTTP failed: {r.stderr}'
        r = run_exec(client, 'apt-get', 'install', '-y', 'sc-fixture-hello', timeout=60)
        assert r.returncode == 0, f'apt-get install HTTP failed: {r.stderr}'
        r = run_exec(client, 'sc-fixture-hello')
        assert r.returncode == 0 and r.stdout.strip() == 'hello', f'sc-fixture-hello failed: {r.stdout}'
        print('PASS: 2. HTTP apt-get update + install + run')

        # 2 (HTTPS). Switch sources to HTTPS, clear cache, update + install via CONNECT.
        docker('exec', client, 'apt-get', 'remove', '-y', 'sc-fixture-hello')
        docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')
        fixtures.joinpath('sources-https.list').write_text('deb [signed-by=/fixtures/repo.gpg] https://repo.test/ ./\n')
        docker('cp', str(fixtures/'sources-https.list'), client+':/etc/apt/sources.list')
        r = run_exec(client, 'apt-get', 'update', timeout=60)
        assert r.returncode == 0, f'apt-get update HTTPS failed: {r.stderr}'
        r = run_exec(client, 'apt-get', 'install', '-y', 'sc-fixture-hello', timeout=60)
        assert r.returncode == 0, f'apt-get install HTTPS failed: {r.stderr}'
        r = run_exec(client, 'sc-fixture-hello')
        assert r.returncode == 0 and r.stdout.strip() == 'hello', f'sc-fixture-hello HTTPS failed: {r.stdout}'
        print('PASS: 2. HTTPS apt-get update + install via CONNECT')

        # 3. Tampered InRelease causes a signature error and blocks install.
        docker('exec', client, 'apt-get', 'remove', '-y', 'sc-fixture-hello')
        docker('exec', client, 'apt-get', 'clean')
        docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')
        docker('cp', str(fixtures/'sources.list'), client+':/etc/apt/sources.list')
        docker('exec', fixture, 'cp', '/repo/InRelease', '/repo/InRelease.bak')
        docker('exec', fixture, 'cp', '/repo/Release.gpg', '/repo/Release.gpg.bak')
        docker('exec', fixture, 'sh', '-c', 'echo tampered > /repo/InRelease; echo tampered > /repo/Release.gpg')
        r = run_exec(client, 'apt-get', 'update', timeout=60)
        assert is_sig_error(r), f'tampered InRelease should cause signature error, got rc={r.returncode}, out={r.stdout+r.stderr}'
        r = run_exec(client, 'apt-get', 'install', '-y', 'sc-fixture-hello', timeout=60)
        assert r.returncode != 0, 'install should fail after tampered update'
        print('PASS: 3. tampered InRelease detected, install blocked')
        docker('exec', fixture, 'cp', '/repo/InRelease.bak', '/repo/InRelease')
        docker('exec', fixture, 'cp', '/repo/Release.gpg.bak', '/repo/Release.gpg')

        # 4. Allowlist without repo.test blocks update.
        docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')
        snap2 = copy.deepcopy(snap)
        snap2['policies'][0]['destinations'] = [{'domain':'other.test','include_subdomains':False}]
        apply_snapshot(snap2, api('/configuration/draft')['version'])
        r = run_exec(client, 'apt-get', 'update', timeout=60)
        assert r.returncode != 0, 'apt-get update should fail without repo.test in allowlist'
        print('PASS: 4. allowlist without repo.test blocks update')

        # 5. Optional real APT archive through the proxy.
        if os.environ.get('SC_TEST_REAL_APT') == '1':
            docker('exec', client, 'sh', '-c', 'rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*')
            # amd64 is served by archive/security, every other architecture by ports.
            if docker('exec', client, 'dpkg', '--print-architecture') == 'amd64':
                mirrors = ['http://archive.ubuntu.com/ubuntu noble main', 'http://archive.ubuntu.com/ubuntu noble-updates main', 'http://security.ubuntu.com/ubuntu noble-security main']
            else:
                mirrors = ['http://ports.ubuntu.com/ubuntu-ports noble main', 'http://ports.ubuntu.com/ubuntu-ports noble-updates main', 'http://ports.ubuntu.com/ubuntu-ports noble-security main']
            fixtures.joinpath('sources-ubuntu.list').write_text(''.join('deb '+m+'\n' for m in mirrors))
            docker('cp', str(fixtures/'sources-ubuntu.list'), client+':/etc/apt/sources.list')
            docker('exec', client, 'sh', '-c', 'rm -rf /var/lib/apt/lists/*')
            snap3 = copy.deepcopy(snap)
            snap3['policies'][0]['destinations'] = [
                {'domain':'archive.ubuntu.com','include_subdomains':True},
                {'domain':'security.ubuntu.com','include_subdomains':True},
                {'domain':'ports.ubuntu.com','include_subdomains':True},
            ]
            # Public resolvers: a desktop Docker DNS may answer with 198.18.0.0/15,
            # which the proxy rightly treats as private.
            snap3['settings']['dns_nameservers'] = ['1.1.1.1', '8.8.8.8']
            apply_snapshot(snap3, api('/configuration/draft')['version'])
            r = run_exec(client, 'apt-get', 'update', timeout=300)
            assert r.returncode == 0, f'real APT update failed: {r.stderr}'
            r = run_exec(client, 'sh', '-c', 'cd /tmp && apt-get download hello', timeout=120)
            assert r.returncode == 0, f'apt-get download hello failed: {r.stderr}'
            print('PASS: 5. real APT archive update and download through proxy')
        else:
            print('SKIP: real APT archive (set SC_TEST_REAL_APT=1)')

        print('ALL APT CHECKS PASSED')
    except Exception:
        if name+'-proxy' in containers:
            print(docker('logs','--tail','30',name+'-proxy'))
            details=docker('exec',name+'-proxy','tail','-n','50','/data/squid/cache.log')
            print('\n'.join(line for line in details.splitlines() if any(term in line.lower() for term in ['helper','fatal','warning','error'])))
        raise
    finally:
        for container in reversed(containers):
            subprocess.run(['docker','rm','-f','-v',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        subprocess.run(['docker','network','rm',network],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
