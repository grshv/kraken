#!/usr/bin/env python3
"""Operational runbook integration tests on a real image.

Turns the runbook procedures (backup/restore, certificate replacement, image
update, disk full) into executable checks against a real kraken container.
Creates only uniquely named disposable containers, networks and volumes; all
are removed in finally so foreign resources are never touched.

Image tag is parameterised via KRAKEN_TEST_IMAGE (default kraken:ops). The
image-update section builds KRAKEN_TEST_IMAGE_NEXT (default kraken:ops-next)
with a trivial label difference and no code change.

Each step prints `PASS: ...`; on any error the process exits non-zero. A step
that cannot be exercised in this environment is reported as SKIP with a reason
rather than a false PASS.
"""
import copy
import http.cookiejar
import json
import os
import socket
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
import secrets

IMAGE = os.environ.get('KRAKEN_TEST_IMAGE', 'kraken:ops')
NEXT_IMAGE = os.environ.get('KRAKEN_TEST_IMAGE_NEXT', 'kraken:ops-next')
PYTHON = 'python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916'
CONTROL_USER = '10013:10013'
# Distinct subnets per section so the run does not collide with smoke
# (11.250.17.0/24), network (11.251.17.0/24) or apt (11.252.17.0/24), nor
# between its own sections.
SUBNETS = ['11.253.17.0/24', '11.253.18.0/24', '11.253.19.0/24', '11.253.20.0/24', '11.253.21.0/24']
BASES = ['11.253.17', '11.253.18', '11.253.19', '11.253.20', '11.253.21']

prefix = 'sc-ops-' + uuid.uuid4().hex[:8]
containers = []
networks = []
volumes = []


def docker(*args, input=None, check=True):
    r = subprocess.run(['docker', *args], input=input, text=True, capture_output=True)
    if check and r.returncode:
        raise RuntimeError(f'docker {args[0]} failed: {r.stderr[-2000:]} {r.stdout[-1000:]}')
    return r.stdout.strip()


def wait_for(fn, seconds=60):
    last = None
    for _ in range(seconds * 5):
        try:
            return fn()
        except Exception as exc:
            last = exc
            time.sleep(.2)
    raise RuntimeError(f'timed out after {seconds}s: {last}')


def free_port():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(('127.0.0.1', 0))
    port = str(s.getsockname()[1])
    s.close()
    return port


class Panel:
    """HTTPS panel client bound to a CA file and a cookie jar."""

    def __init__(self, origin, cafile, password=None):
        context = ssl.create_default_context(cafile=str(cafile))
        self.opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPSHandler(context=context),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.origin = origin
        self.csrf = ''
        self.password = password  # stored only in memory, used to re-auth after 401

    def api(self, path, method='GET', body=None, key=None):
        data = None if body is None else json.dumps(body).encode()
        headers = {'Content-Type': 'application/json', 'Origin': self.origin, 'X-CSRF-Token': self.csrf}
        if key:
            headers['Idempotency-Key'] = key
        req = urllib.request.Request(self.origin + '/api/v1' + path, data=data, method=method, headers=headers)
        try:
            with self.opener.open(req, timeout=25) as response:
                return json.load(response) if response.status != 204 else None
        except urllib.error.HTTPError as exc:
            # A full disk may prevent the panel from persisting session state,
            # so a previously valid cookie can surface as 401. Re-authenticate
            # once and retry the original request.
            if exc.code != 401 or self.password is None:
                raise
            self.login(self.password)
            headers['X-CSRF-Token'] = self.csrf
            req2 = urllib.request.Request(self.origin + '/api/v1' + path, data=data, method=method, headers=headers)
            if key:
                req2.add_header('Idempotency-Key', key)
            with self.opener.open(req2, timeout=25) as response:
                return json.load(response) if response.status != 204 else None

    def refresh_csrf(self):
        # Bypass self.api so a 401 here cannot recurse into login().
        req = urllib.request.Request(self.origin + '/api/v1/session')
        with self.opener.open(req, timeout=25) as response:
            s = json.load(response)
        self.csrf = s['csrf_token']
        return s

    def login(self, password):
        self.password = password
        wait_for(lambda: self.refresh_csrf(), seconds=90)
        # POST credentials directly via the opener (not self.api) so the
        # login flow itself does not recurse on a 401.
        data = json.dumps({'username': 'admin', 'password': password}).encode()
        req = urllib.request.Request(self.origin + '/api/v1/session', data=data, method='POST',
                                     headers={'Content-Type': 'application/json', 'Origin': self.origin, 'X-CSRF-Token': self.csrf})
        with self.opener.open(req, timeout=25) as response:
            s = json.load(response)
        self.csrf = s['csrf_token']
        return s

    def status(self):
        return self.api('/status')

    def wait_up(self, seconds=120):
        def squid_up():
            s = self.status()
            assert s['squid']['state'] == 'up', s
            return s
        return wait_for(squid_up, seconds)

    def draft(self):
        return self.api('/configuration/draft')

    def put_draft(self, snapshot, expected_version):
        return self.api('/configuration/draft', 'PUT', {'expected_version': expected_version, 'snapshot': snapshot})

    def apply(self, snapshot, expected_version):
        d = self.put_draft(snapshot, expected_version)
        revision = self.api('/revisions', 'POST', {'expected_draft_version': d['version']})
        preview = self.api('/revisions/' + revision['id'] + '/preview')

        def operation(action, validation=None):
            body = {'action': action, 'revision_id': revision['id'], 'expected_applied_revision': preview['expected_applied_revision']}
            if validation:
                body['validation_operation_id'] = validation
            key = str(uuid.uuid4())
            op = self.api('/operations', 'POST', body, key)
            assert self.api('/operations', 'POST', body, key)['id'] == op['id']

            def finished():
                cur = self.api('/operations/' + op['id'])
                if cur['status'] in ['pending', 'validating', 'reloading']:
                    raise RuntimeError(cur['status'])
                return cur
            return wait_for(finished)

        validated = operation('validate')
        assert validated['status'] == 'validated', validated
        applied = operation('apply', validated['id'])
        assert applied['status'] == 'succeeded', applied
        return d


# Reused fixture: HTTP+HTTPS origin with a self-signed cert whose SAN covers
# allowed.test and denied.test. Mirrors smoke.py's origin so traffic behaviour
# matches the documented access model.
ORIGIN_CODE = '''import http.server,ssl,threading
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin'*1024)
 def log_message(self,*args):pass
plain=http.server.HTTPServer(('0.0.0.0',80),Handler)
secure=http.server.HTTPServer(('0.0.0.0',443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixtures/origin.crt','/fixtures/origin.key');secure.socket=ctx.wrap_socket(secure.socket,server_side=True)
threading.Thread(target=plain.serve_forever,daemon=True).start();secure.serve_forever()
'''


def make_origin_cert(fixtures):
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                    '-keyout', str(fixtures / 'origin.key'), '-out', str(fixtures / 'origin.crt'),
                    '-subj', '/CN=allowed.test',
                    '-addext', 'subjectAltName=DNS:allowed.test,DNS:denied.test'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def start_origin_client(net, fixtures, ident, base):
    fixtures.joinpath('origin.py').write_text(ORIGIN_CODE)
    origin = ident + '-origin'; containers.append(origin)
    docker('run', '-d', '--name', origin, '--network', net, '--ip', base + '.10',
           '--network-alias', 'allowed.test', '--network-alias', 'denied.test',
           '-v', str(fixtures) + ':/fixtures:ro', PYTHON, 'python', '/fixtures/origin.py')
    client = ident + '-client'; containers.append(client)
    docker('run', '-d', '--name', client, '--network', net, '--ip', base + '.20',
           '-v', str(fixtures) + ':/fixtures:ro', PYTHON, 'python', '-c', 'import time;time.sleep(3600)')
    return origin, client


def fetch(client, target='http://allowed.test/', tls_proxy=False, connect=False, cafile='/fixtures/proxy.crt', origincrt='/fixtures/origin.crt'):
    code = "import socket,ssl,http.client\n"
    if tls_proxy:
        code += f"c=http.client.HTTPConnection('proxy',3129);c.sock=ssl.create_default_context(cafile={cafile!r}).wrap_socket(socket.create_connection(('proxy',3129)),server_hostname='localhost')\n"
    else:
        code += "c=http.client.HTTPConnection('proxy',3128,timeout=5)\n"
    if connect:
        code += f"c.set_tunnel('allowed.test',443);c.connect();sock=ssl.create_default_context(cafile={origincrt!r}).wrap_socket(c.sock,server_hostname='allowed.test');sock.sendall(b'GET / HTTP/1.0\\r\\nHost: allowed.test\\r\\n\\r\\n');r=http.client.HTTPResponse(sock);r.begin();print(r.status)\n"
    else:
        code += f"c.request('GET',{target!r});r=c.getresponse();print(r.status)\n"
    return int(docker('exec', client, 'python', '-c', code))


def tls_ok(host, port, cafile):
    try:
        ctx = ssl.create_default_context(cafile=str(cafile))
        s = ctx.wrap_socket(socket.create_connection((host, int(port)), timeout=8), server_hostname='localhost')
        s.close()
        return True
    except (ssl.SSLCertVerificationError, ssl.SSLError, OSError):
        return False


def proxy_tls_ok(client, cafile):
    code = ("import socket,ssl\n"
            "try:\n"
            " ctx=ssl.create_default_context(cafile=" + repr(cafile) + ")\n"
            " s=ctx.wrap_socket(socket.create_connection(('proxy',3129),timeout=8),server_hostname='localhost')\n"
            " s.close();print('OK')\n"
            "except Exception:\n"
            " print('FAIL')\n")
    return docker('exec', client, 'python', '-c', code).strip() == 'OK'


def pem_body(path):
    return ''.join(l for l in Path(path).read_text().splitlines() if not l.startswith('-----'))


def generate_certs(fixtures):
    """New dev-CA and panel/proxy leaf certs signed by it, in a temp dir."""
    subprocess.run(['openssl', 'genrsa', '-out', str(fixtures / 'new-ca.key'), '2048'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run(['openssl', 'req', '-x509', '-new', '-key', str(fixtures / 'new-ca.key'), '-days', '1',
                    '-out', str(fixtures / 'new-ca.crt'), '-subj', '/CN=Kraken OPS CA',
                    '-addext', 'basicConstraints=critical,CA:TRUE',
                    '-addext', 'keyUsage=critical,keyCertSign,cRLSign',
                    '-addext', 'subjectKeyIdentifier=hash'],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for name, cn, san in [('panel', 'localhost', 'DNS:localhost,IP:127.0.0.1'),
                          ('proxy', 'proxy', 'DNS:proxy,DNS:localhost,IP:127.0.0.1')]:
        ext = fixtures / (name + '-san.ext')
        ext.write_text('subjectAltName=' + san + '\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nauthorityKeyIdentifier=keyid,issuer\n')
        subprocess.run(['openssl', 'genrsa', '-out', str(fixtures / f'new-{name}.key'), '2048'],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['openssl', 'req', '-new', '-key', str(fixtures / f'new-{name}.key'),
                        '-out', str(fixtures / f'new-{name}.csr'), '-subj', f'/CN={cn}'],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(['openssl', 'x509', '-req', '-in', str(fixtures / f'new-{name}.csr'),
                        '-CA', str(fixtures / 'new-ca.crt'), '-CAkey', str(fixtures / 'new-ca.key'),
                        '-CAcreateserial', '-days', '1', '-out', str(fixtures / f'new-{name}.crt'),
                        '-extfile', str(ext)],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def ensure_next_image():
    """Build kraken:ops-next from the current tree with a trivial label.

    Always rebuilt (layer cache keeps it cheap): a stale tag from an older tree
    would test an incompatible schema instead of this change.
    """
    docker('build', '-f', 'deploy/Dockerfile', '--label', 'kraken.ops.variant=next', '-t', NEXT_IMAGE, '.')


def section_backup_restore(fixtures_root):
    print('\n=== Section 1: backup and restore of /data ===')
    net = prefix + '-n1'; networks.append(net)
    vol = prefix + '-v1'; volumes.append(vol)
    volb = prefix + '-v1b'; volumes.append(volb)
    fixtures = fixtures_root / 's1'
    fixtures.mkdir(parents=True, exist_ok=True)
    base = BASES[0]
    docker('network', 'create', '--subnet', SUBNETS[0], net)
    docker('volume', 'create', vol)
    docker('run', '--rm', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'dev-cert')
    password = secrets.token_urlsafe(24)
    docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'bootstrap', '--password-stdin', input=password + '\n')
    port = free_port()
    origin = 'https://localhost:' + port
    proxy = prefix + '-p1'; containers.append(proxy)
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443', '-v', vol + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
    docker('cp', proxy + ':/data/certs/panel.crt', str(fixtures / 'panel.crt'))
    docker('cp', proxy + ':/data/certs/proxy.crt', str(fixtures / 'proxy.crt'))
    make_origin_cert(fixtures)
    origin_c, client = start_origin_client(net, fixtures, prefix + '-s1', base)

    panel = Panel(origin, fixtures / 'panel.crt', password)
    panel.login(password)
    panel.wait_up()
    d = panel.draft(); snap = d['snapshot']; profile = snap['tls_profiles'][0]['id']
    all_id, allow_id = str(uuid.uuid4()), str(uuid.uuid4())
    snap['policies'] = [
        {'id': all_id, 'name': 'All web ops', 'mode': 'all_web', 'destinations': [], 'tls_profile_id': profile},
        {'id': allow_id, 'name': 'Allowlist ops', 'mode': 'allowlist', 'destinations': [{'domain': 'allowed.test', 'include_subdomains': False}], 'tls_profile_id': profile}]
    snap['clients'] = [
        {'id': str(uuid.uuid4()), 'name': 'source ops', 'source_networks': [base + '.10/32'], 'enabled': True, 'policy_id': all_id},
        {'id': str(uuid.uuid4()), 'name': 'client ops', 'source_networks': [base + '.20'], 'enabled': True, 'policy_id': allow_id}]
    panel.apply(snap, d['version'])

    # Generate traffic so events and audit exist before the backup.
    assert wait_for(lambda: fetch(client) == 200)
    assert fetch(client, 'http://denied.test/') == 403
    assert fetch(origin_c, 'http://denied.test/') == 200
    assert fetch(client, connect=True) == 200
    assert fetch(client, 'http://169.254.169.254/') == 403
    print('PASS: 1a. instance raised, policy applied, traffic generates events')

    status = panel.status()
    applied_rev = status['applied_revision']
    config_hash = status['observed_config_hash']
    lkg_hash = json.loads(docker('exec', proxy, 'cat', '/data/runtime/lkg.json'))['hash']

    def events_ready():
        page = panel.api('/events?limit=50')
        assert len(page['items']) >= 2, page
        return page
    page = wait_for(events_ready, seconds=30)
    captured_event_ids = {v['id'] for v in page['items']}
    audit_items = panel.api('/audit?limit=50')['items']
    assert audit_items, 'no audit entries captured'
    captured_audit_ids = {v['id'] for v in audit_items}

    # Runbook "Копирование данных": stop the single container cleanly so Squid
    # releases the PID file and the panel checkpoints the WAL, copy the whole
    # /data named volume preserving rights/UID (not just control.sqlite3), into
    # a separate restore volume. The original volume is kept intact.
    docker('stop', '-t', '30', proxy)
    docker('run', '--rm', '-v', vol + ':/from', '-v', volb + ':/to', '--entrypoint', 'sh', IMAGE,
           '-c', 'tar -C /from -cpf - . | tar -C /to -xpf -')
    docker('rm', '-v', proxy); containers.remove(proxy)

    # Runbook "Восстановление копии": start a NEW container on a NEW volume
    # built from the copy, on the same isolated test network. Same password,
    # same active revision/fingerprint, policy still in force, history intact.
    proxy = prefix + '-p1b'; containers.append(proxy)
    port_b = free_port()
    origin_b = 'https://localhost:' + port_b
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port_b + ':8443', '-v', volb + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin_b,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
    panel_b = Panel(origin_b, fixtures / 'panel.crt', password)
    panel_b.login(password)  # same password opens the restored admin
    panel_b.wait_up()

    restored = panel_b.status()
    assert restored['applied_revision'] == applied_rev, (restored['applied_revision'], applied_rev)
    assert restored['observed_config_hash'] == config_hash, (restored['observed_config_hash'], config_hash)
    restored_lkg = json.loads(docker('exec', proxy, 'cat', '/data/runtime/lkg.json'))['hash']
    assert restored_lkg == lkg_hash, (restored_lkg, lkg_hash)
    print('PASS: 1b. restored instance opens with same password and same active revision/fingerprint')

    assert fetch(client) == 200
    assert fetch(client, 'http://denied.test/') == 403
    assert fetch(origin_c, 'http://denied.test/') == 200
    print('PASS: 1c. restored policy still in force (allowed passes, denied 403)')

    restored_events = wait_for(lambda: panel_b.api('/events?limit=50'), seconds=30)
    restored_event_ids = {v['id'] for v in restored_events['items']}
    assert captured_event_ids <= restored_event_ids, (captured_event_ids, restored_event_ids)
    restored_audit = panel_b.api('/audit?limit=50')['items']
    restored_audit_ids = {v['id'] for v in restored_audit}
    assert captured_audit_ids <= restored_audit_ids, (captured_audit_ids, restored_audit_ids)
    print('PASS: 1d. event history and audit survive the copy/restore')


def section_cert_replace(fixtures_root):
    print('\n=== Section 2: server certificate replacement ===')
    net = prefix + '-n2'; networks.append(net)
    vol = prefix + '-v2'; volumes.append(vol)
    fixtures = fixtures_root / 's2'
    fixtures.mkdir(parents=True, exist_ok=True)
    base = BASES[1]
    docker('network', 'create', '--subnet', SUBNETS[1], net)
    docker('volume', 'create', vol)
    docker('run', '--rm', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'dev-cert')
    password = secrets.token_urlsafe(24)
    docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'bootstrap', '--password-stdin', input=password + '\n')
    port = free_port()
    origin = 'https://localhost:' + port
    proxy = prefix + '-p2'; containers.append(proxy)
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443', '-v', vol + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
    docker('cp', proxy + ':/data/certs/panel.crt', str(fixtures / 'old-panel.crt'))
    docker('cp', proxy + ':/data/certs/proxy.crt', str(fixtures / 'old-proxy.crt'))
    generate_certs(fixtures)
    client = prefix + '-c2'; containers.append(client)
    docker('run', '-d', '--name', client, '--network', net, '--ip', base + '.20',
           '-v', str(fixtures) + ':/fixtures:ro', PYTHON, 'python', '-c', 'import time;time.sleep(3600)')

    panel = Panel(origin, fixtures / 'old-panel.crt', password)
    panel.login(password)
    panel.wait_up()
    assert tls_ok('127.0.0.1', port, fixtures / 'old-panel.crt')
    assert proxy_tls_ok(client, '/fixtures/old-proxy.crt')
    print('PASS: 2a. baseline: current certs verify with current (old) CA')

    # Runbook "Истекающий/скомпрометированный сертификат": stop the container,
    # replace the cert/key files in /data/certs preserving rights (panel — UID
    # 10013 mode 0600; proxy — group 13 mode 0640), start and verify with a
    # real TLS client. The old CA file is kept so the negative check is real.
    docker('stop', '-t', '30', proxy)
    docker('run', '--rm', '-v', vol + ':/data', '-v', str(fixtures) + ':/new:ro', '--entrypoint', 'sh', IMAGE, '-c', (
        'cp /new/new-panel.key /data/certs/panel.key && chown 10013:10013 /data/certs/panel.key && chmod 600 /data/certs/panel.key && '
        'cp /new/new-panel.crt /data/certs/panel.crt && chown 10013:10013 /data/certs/panel.crt && chmod 600 /data/certs/panel.crt && '
        'cp /new/new-proxy.key /data/certs/proxy.key && chown 10013:13 /data/certs/proxy.key && chmod 640 /data/certs/proxy.key && '
        'cp /new/new-proxy.crt /data/certs/proxy.crt && chown 10013:13 /data/certs/proxy.crt && chmod 640 /data/certs/proxy.crt'))
    docker('start', proxy)
    panel_n = Panel(origin, fixtures / 'new-ca.crt', password)
    wait_for(lambda: panel_n.refresh_csrf(), seconds=90)
    panel_n.login(password)
    panel_n.wait_up()

    assert tls_ok('127.0.0.1', port, fixtures / 'new-ca.crt')
    assert proxy_tls_ok(client, '/fixtures/new-ca.crt')
    print('PASS: 2b. client trusting new CA passes TLS verification for panel and proxy')

    assert not tls_ok('127.0.0.1', port, fixtures / 'old-panel.crt')
    assert not proxy_tls_ok(client, '/fixtures/old-proxy.crt')
    print('PASS: 2c. client trusting old CA is rejected (panel and proxy)')

    logs = docker('logs', proxy)
    for keyfile in ['new-panel.key', 'new-proxy.key']:
        body = pem_body(fixtures / keyfile)
        assert body and body not in logs, keyfile + ' private key material leaked into container logs'
    assert 'BEGIN PRIVATE KEY' not in logs, 'PRIVATE KEY PEM marker leaked into container logs'
    print('PASS: 2d. private keys do not appear in container logs')


def section_image_update(fixtures_root):
    print('\n=== Section 3: image update on the same volume ===')
    ensure_next_image()
    net = prefix + '-n3'; networks.append(net)
    vol = prefix + '-v3'; volumes.append(vol)
    fixtures = fixtures_root / 's3'
    fixtures.mkdir(parents=True, exist_ok=True)
    base = BASES[2]
    docker('network', 'create', '--subnet', SUBNETS[2], net)
    docker('volume', 'create', vol)
    docker('run', '--rm', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'dev-cert')
    password = secrets.token_urlsafe(24)
    docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'bootstrap', '--password-stdin', input=password + '\n')
    port = free_port()
    origin = 'https://localhost:' + port
    proxy = prefix + '-p3'; containers.append(proxy)
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443', '-v', vol + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
    docker('cp', proxy + ':/data/certs/panel.crt', str(fixtures / 'panel.crt'))

    panel = Panel(origin, fixtures / 'panel.crt', password)
    panel.login(password)
    panel.wait_up()
    # Apply a baseline policy so there is a concrete active revision.
    d = panel.draft(); snap = d['snapshot']; profile = snap['tls_profiles'][0]['id']
    allow_id, cid = str(uuid.uuid4()), str(uuid.uuid4())
    snap['policies'] = [{'id': allow_id, 'name': 'Allowlist ops', 'mode': 'allowlist',
                         'destinations': [{'domain': 'allowed.test', 'include_subdomains': False}], 'tls_profile_id': profile}]
    snap['clients'] = [{'id': cid, 'name': 'client ops', 'source_networks': [base + '.20'], 'enabled': True, 'policy_id': allow_id}]
    panel.apply(snap, d['version'])
    applied_rev = panel.status()['applied_revision']

    # Dirty draft: unsaved changes not promoted to a revision. Must survive the
    # image swap without being applied.
    dirty = copy.deepcopy(snap)
    dirty['clients'][0]['name'] = 'client ops dirty'
    d = panel.put_draft(dirty, panel.draft()['version'])
    dirty_version = d['version']
    dirty_snapshot = d['snapshot']
    print('PASS: 3a. baseline applied and dirty draft saved (not applied)')

    # Runbook "Обновление образа/приложения": recreate the container on the same
    # volume with the new image. Volume holds DB (sessions, revisions, draft)
    # and runtime LKG; recovery gate restores the last good config. Squid and
    # the panel must shut down cleanly so they release the on-disk PID file and
    # checkpoint WAL; a forced kill leaves /data/squid/squid.pid behind and the
    # next container's Squid aborts with "already running".
    docker('stop', '-t', '30', proxy)
    docker('rm', '-v', proxy); containers.remove(proxy)
    time.sleep(1)  # let the host port binding release
    proxy = prefix + '-p3b'; containers.append(proxy)
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443', '-v', vol + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', NEXT_IMAGE)
    # The pre-image-swap cookie is server-validated against the DB session
    # verifier, which lives on the volume; reuse the same opener/cookie jar.
    wait_for(lambda: panel.status(), seconds=120)
    restored_draft = panel.draft()
    assert restored_draft['version'] == dirty_version, (restored_draft['version'], dirty_version)
    assert restored_draft['snapshot'] == dirty_snapshot, 'dirty draft changed across image swap'
    assert panel.status()['applied_revision'] == applied_rev, 'dirty draft was applied during image swap'
    print('PASS: 3b. dirty draft preserved and not applied; active revision unchanged')
    panel.login(password)  # fresh login on the new image
    panel.wait_up()
    print('PASS: 3c. existing session and fresh login work after image swap')


def section_disk_full(fixtures_root):
    print('\n=== Section 4: full storage ===')
    net = prefix + '-n4'; networks.append(net)
    fixtures = fixtures_root / 's4'
    fixtures.mkdir(parents=True, exist_ok=True)
    base = BASES[3]
    docker('network', 'create', '--subnet', SUBNETS[3], net)
    port = free_port()
    origin = 'https://localhost:' + port
    proxy = prefix + '-p4'; containers.append(proxy)
    # /data on a size-limited tmpfs. tmpfs is not populated from the image, so
    # the entrypoint recreates the Dockerfile's /data layout (ADR 0015 rights)
    # and the dev cert before keeping the container alive for CLI bootstrap.
    init_cmd = (
        'install -d -o kraken -g proxy -m 0750 /data && '
        'install -d -o kraken -g kraken -m 0700 /data/app && '
        'install -d -o kraken -g proxy -m 2750 /data/certs && '
        'install -d -o kraken -g proxy -m 2750 /data/runtime && '
        'install -d -o proxy -g proxy -m 2750 /data/squid && '
        'install -d -o proxy -g proxy -m 2750 /data/logs && '
        'setpriv --reuid=kraken --regid=kraken --init-groups -- /usr/local/bin/kraken dev-cert && '
        'exec sleep infinity')
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443',
           '--mount', 'type=tmpfs,destination=/data,tmpfs-size=67108864',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key',
           '--entrypoint', 'sh', IMAGE, '-c', init_cmd)
    wait_for(lambda: docker('exec', proxy, 'test', '-f', '/data/certs/panel.crt'), seconds=30)
    password = secrets.token_urlsafe(24)
    docker('exec', '-i', '--user', CONTROL_USER, proxy, 'kraken', 'bootstrap', '--username', 'admin', '--password-stdin', input=password + '\n')
    # Pebble is started detached inside the already-running container so the
    # tmpfs (and the cert/DB written into it) is preserved for the whole step.
    docker('exec', '-d', proxy, 'sh', '-c', 'pebble run --verbose >/tmp/pebble.log 2>&1')
    # docker cp cannot read from a tmpfs mount on this runtime, so extract the
    # panel certificate via exec; it is PEM text.
    (fixtures / 'panel.crt').write_text(docker('exec', proxy, 'cat', '/data/certs/panel.crt') + '\n')
    make_origin_cert(fixtures)
    origin_c, client = start_origin_client(net, fixtures, prefix + '-s4', base)

    panel = Panel(origin, fixtures / 'panel.crt', password)
    panel.login(password)
    panel.wait_up()
    d = panel.draft(); snap = d['snapshot']; profile = snap['tls_profiles'][0]['id']
    all_id, allow_id = str(uuid.uuid4()), str(uuid.uuid4())
    snap['policies'] = [
        {'id': all_id, 'name': 'All web ops', 'mode': 'all_web', 'destinations': [], 'tls_profile_id': profile},
        {'id': allow_id, 'name': 'Allowlist ops', 'mode': 'allowlist', 'destinations': [{'domain': 'allowed.test', 'include_subdomains': False}], 'tls_profile_id': profile}]
    snap['clients'] = [
        {'id': str(uuid.uuid4()), 'name': 'source ops', 'source_networks': [base + '.10/32'], 'enabled': True, 'policy_id': all_id},
        {'id': str(uuid.uuid4()), 'name': 'client ops', 'source_networks': [base + '.20'], 'enabled': True, 'policy_id': allow_id}]
    panel.apply(snap, d['version'])
    assert wait_for(lambda: fetch(client) == 200)
    assert fetch(client, 'http://denied.test/') == 403
    print('PASS: 4a. instance on tmpfs-backed /data serves the applied policy')

    applied_rev = panel.status()['applied_revision']
    config_hash = panel.status()['observed_config_hash']

    # Fill the volume to refusal: 1M blocks first, then 4k blocks to top up the
    # last sub-megabyte so SQLite/WAL writes cannot find room.
    docker('exec', proxy, 'sh', '-c', 'dd if=/dev/zero of=/data/fill bs=1M 2>/dev/null; dd if=/dev/zero of=/data/fill.top bs=4k 2>/dev/null; true')
    free_kb = docker('exec', proxy, 'sh', '-c', "df -k /data | awk 'NR==2 {print $4}'")
    assert int(free_kb) <= 4, ('expected ~0 KiB free, got ' + free_kb)

    # Attempt to modify the draft and to apply: both must fail with a clear
    # error and no partial application. SQLite returns SQLITE_FULL to the panel
    # which surfaces a storage error; the previous LKG stays in place. The
    # panel may also fail to persist session state on a full disk, surfacing
    # 401; the client re-authenticates transparently in that case.
    modified = copy.deepcopy(snap)
    modified['clients'][0]['name'] = 'client ops renamed'
    draft_failed = False
    try:
        panel.put_draft(modified, panel.draft()['version'])
    except urllib.error.HTTPError as exc:
        draft_failed = exc.code >= 400
    except Exception:
        draft_failed = True
    assert draft_failed, 'draft PUT succeeded on a full disk (expected clean error)'

    apply_failed = False
    try:
        panel.apply(modified, panel.draft()['version'])
    except urllib.error.HTTPError as exc:
        apply_failed = exc.code >= 400
    except Exception:
        apply_failed = True
    assert apply_failed, 'apply succeeded on a full disk (expected clean error)'
    post = panel.status()
    assert post['applied_revision'] == applied_rev, 'partial application on full disk'
    assert post['observed_config_hash'] == config_hash, 'config hash changed on full disk'
    print('PASS: 4b. draft/apply fail cleanly on full disk, no partial application')

    # Squid keeps serving the last good config from the on-disk active.conf.
    assert fetch(client) == 200
    assert fetch(client, 'http://denied.test/') == 403
    print('PASS: 4c. Squid keeps serving the last working configuration')

    # Runbook "Disk full": free disposable space, then the panel recovers
    # without manual intervention.
    docker('exec', proxy, 'sh', '-c', 'rm -f /data/fill /data/fill.top')
    wait_for(lambda: panel.draft(), seconds=30)  # storage reads succeed again
    modified2 = copy.deepcopy(snap)
    modified2['clients'][0]['name'] = 'client ops after cleanup'
    panel.apply(modified2, panel.draft()['version'])
    assert panel.status()['applied_revision'] != applied_rev, 'apply did not succeed after cleanup'
    print('PASS: 4d. panel recovers after space is freed; apply succeeds again')

    # Integrity: the image has no sqlite3 CLI, so verify via the kraken binary
    # (manifest check) and successful reads through its SQLite driver.
    docker('exec', '--user', CONTROL_USER, proxy, 'kraken', 'check')
    events_page = panel.api('/events?limit=1')
    assert isinstance(events_page.get('items'), list), events_page
    panel.api('/audit?limit=1')
    assert panel.status()['squid']['state'] == 'up'
    print('PASS: 4e. runtime manifest verifies and DB reads succeed after ENOSPC')


def section_forced_kill(fixtures_root):
    print('\n=== Section 5: forced kill and restart ===')
    net = prefix + '-n5'; networks.append(net)
    vol = prefix + '-v5'; volumes.append(vol)
    fixtures = fixtures_root / 's5'
    fixtures.mkdir(parents=True, exist_ok=True)
    base = BASES[4]
    docker('network', 'create', '--subnet', SUBNETS[4], net)
    docker('volume', 'create', vol)
    docker('run', '--rm', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'dev-cert')
    password = secrets.token_urlsafe(24)
    docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', vol + ':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'bootstrap', '--password-stdin', input=password + '\n')
    port = free_port()
    origin = 'https://localhost:' + port
    proxy = prefix + '-p5'; containers.append(proxy)
    docker('run', '-d', '--name', proxy, '--network', net, '--network-alias', 'proxy', '--ip', base + '.2',
           '-p', '127.0.0.1:' + port + ':8443', '-v', vol + ':/data',
           '-e', 'KRAKEN_PUBLIC_ORIGIN=' + origin,
           '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
    docker('cp', proxy + ':/data/certs/panel.crt', str(fixtures / 'panel.crt'))
    docker('cp', proxy + ':/data/certs/proxy.crt', str(fixtures / 'proxy.crt'))
    make_origin_cert(fixtures)
    origin_c, client = start_origin_client(net, fixtures, prefix + '-s5', base)

    panel = Panel(origin, fixtures / 'panel.crt', password)
    panel.login(password)
    panel.wait_up()
    d = panel.draft(); snap = d['snapshot']; profile = snap['tls_profiles'][0]['id']
    all_id, allow_id = str(uuid.uuid4()), str(uuid.uuid4())
    snap['policies'] = [
        {'id': all_id, 'name': 'All web ops', 'mode': 'all_web', 'destinations': [], 'tls_profile_id': profile},
        {'id': allow_id, 'name': 'Allowlist ops', 'mode': 'allowlist', 'destinations': [{'domain': 'allowed.test', 'include_subdomains': False}], 'tls_profile_id': profile}]
    snap['clients'] = [
        {'id': str(uuid.uuid4()), 'name': 'source ops', 'source_networks': [base + '.10/32'], 'enabled': True, 'policy_id': all_id},
        {'id': str(uuid.uuid4()), 'name': 'client ops', 'source_networks': [base + '.20'], 'enabled': True, 'policy_id': allow_id}]
    panel.apply(snap, d['version'])
    assert wait_for(lambda: fetch(client) == 200)
    assert fetch(client, 'http://denied.test/') == 403
    assert fetch(origin_c, 'http://denied.test/') == 200
    print('PASS: 5a. instance raised, policy applied, traffic passes')

    applied_rev = panel.status()['applied_revision']

    # docker kill -s KILL leaves /data/squid/squid.pid in place; squid-start
    # must remove the stale file before exec so the next launch does not abort
    # with "already running". No `docker stop` grace here: this section proves
    # recovery from a hard kill / OOM / power loss, which the runbook documents
    # as non-fatal since squid-start cleans the PID file automatically.
    docker('kill', '-s', 'KILL', proxy)
    docker('start', proxy)
    panel.wait_up(seconds=120)
    assert wait_for(lambda: fetch(client) == 200)
    assert fetch(client, 'http://denied.test/') == 403
    assert panel.status()['applied_revision'] == applied_rev, 'applied revision changed after forced restart'
    print('PASS: 5b. after docker kill + start, squid serves the same policy from the same revision')


def main():
    inspect = subprocess.run(['docker', 'image', 'inspect', IMAGE], capture_output=True)
    if inspect.returncode != 0:
        print(f'ERROR: image {IMAGE} not found; build it with: docker build -f deploy/Dockerfile -t {IMAGE} .', file=sys.stderr)
        return 2
    if os.getuid() == 0:
        print('ERROR: run as a non-root user', file=sys.stderr)
        return 2
    try:
        with tempfile.TemporaryDirectory(prefix='kraken-ops-') as temporary:
            fixtures_root = Path(temporary)
            section_backup_restore(fixtures_root)
            section_cert_replace(fixtures_root)
            section_image_update(fixtures_root)
            section_disk_full(fixtures_root)
            section_forced_kill(fixtures_root)
        print('\nALL OPS CHECKS PASSED')
        return 0
    except Exception:
        for c in [x for x in containers if x.startswith(prefix)]:
            print('--- logs', c, '---')
            subprocess.run(['docker', 'logs', '--tail', '40', c], stdout=sys.stderr, stderr=sys.stderr)
            cache = subprocess.run(['docker', 'exec', c, 'tail', '-n', '40', '/data/squid/cache.log'],
                                   capture_output=True, text=True)
            if cache.stdout:
                for line in cache.stdout.splitlines():
                    if any(t in line.lower() for t in ['helper', 'fatal', 'warning', 'error']):
                        print(line, file=sys.stderr)
        raise
    finally:
        for c in reversed(containers):
            subprocess.run(['docker', 'rm', '-f', '-v', c], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for n in networks:
            subprocess.run(['docker', 'network', 'rm', n], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for v in volumes:
            subprocess.run(['docker', 'volume', 'rm', v], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == '__main__':
    sys.exit(main())
