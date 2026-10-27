#!/usr/bin/env python3
"""Isolated real-image tests. Creates only uniquely named disposable resources."""
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
PYTHON = 'python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916'
name = 'sc-test-' + uuid.uuid4().hex[:8]
network, volume = name + '-net', name + '-data'
containers = []

def docker(*args, input=None):
    r = subprocess.run(['docker', *args], input=input, text=True, capture_output=True)
    if r.returncode:
        raise RuntimeError(f'docker {args[0]} failed: {r.stderr[-2000:]} {r.stdout[-1000:]}')
    return r.stdout.strip()

def wait_for(fn, seconds=30):
    last = None
    for _ in range(seconds * 5):
        try:
            return fn()
        except Exception as exc:
            last = exc
            time.sleep(.2)
    raise RuntimeError(f'timed out: {last}')

with tempfile.TemporaryDirectory(prefix='squid-control-fixture-') as temporary:
    fixtures = Path(temporary)
    try:
        docker('network', 'create', '--subnet', '11.250.17.0/24', network)
        docker('volume', 'create', volume)
        docker('run', '--rm', '--user', '13:13', '-v', volume+':/data', '--entrypoint', '/usr/local/bin/squid-control', IMAGE, 'dev-cert')
        password = secrets.token_urlsafe(24)
        docker('run', '--rm', '-i', '--user', '13:13', '-v', volume+':/data', '--entrypoint', '/usr/local/bin/squid-control', IMAGE, 'bootstrap', '--password-stdin', input=password+'\n')
        proxy = name+'-proxy'; containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.250.17.2', '-p', '127.0.0.1::8443', '-v', volume+':/data', '-e', 'SC_PUBLIC_ORIGIN=https://localhost', '-e', 'SC_PROXY_CERT=/data/certs/panel.crt', '-e', 'SC_PROXY_KEY=/data/certs/panel.key', IMAGE)
        port = docker('port', proxy, '8443/tcp').rsplit(':',1)[1]
        origin = 'https://localhost:'+port
        # A configured origin must match the actual mapped port.
        docker('rm', '-f', '-v', proxy); containers.remove(proxy)
        containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.250.17.2', '-p', '127.0.0.1:'+port+':8443', '-v', volume+':/data', '-e', 'SC_PUBLIC_ORIGIN='+origin, '-e', 'SC_PROXY_CERT=/data/certs/panel.crt', '-e', 'SC_PROXY_KEY=/data/certs/panel.key', IMAGE)
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
        print('PASS: HTTPS trust, bootstrap, login, active config fingerprint')
        # Deterministic HTTP + HTTPS origin and a separate source identity.
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(fixtures/'origin.key'),'-out',str(fixtures/'origin.crt'),'-subj','/CN=allowed.test','-addext','subjectAltName=DNS:allowed.test,DNS:denied.test'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        origin_code = '''import http.server,ssl,threading,time
class Handler(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin')
 def log_message(self,*args):pass
plain=http.server.HTTPServer(('0.0.0.0',80),Handler)
secure=http.server.HTTPServer(('0.0.0.0',443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixtures/origin.crt','/fixtures/origin.key');secure.socket=ctx.wrap_socket(secure.socket,server_side=True)
threading.Thread(target=plain.serve_forever,daemon=True).start();secure.serve_forever()
'''
        fixtures.joinpath('origin.py').write_text(origin_code)
        for suffix, ip, command in [('origin','11.250.17.10',['python','/fixtures/origin.py']),('client','11.250.17.20',['python','-c','import time;time.sleep(3600)'])]:
            n=name+'-'+suffix; containers.append(n)
            args=['run','-d','--name',n,'--network',network,'--ip',ip,'-v',str(fixtures)+':/fixtures:ro']
            if suffix=='origin': args+=['--network-alias','allowed.test','--network-alias','denied.test']
            docker(*args,PYTHON,*command)
        client=name+'-client'; source=name+'-origin'
        def fetch(container,target='http://allowed.test/',tls_proxy=False,connect=False):
            code = "import socket,ssl,http.client\n"
            if tls_proxy:
                code+="c=http.client.HTTPConnection('proxy',3129);c.sock=ssl.create_default_context(cafile='/fixtures/panel.crt').wrap_socket(socket.create_connection(('proxy',3129)),server_hostname='localhost')\n"
            else:
                code+="c=http.client.HTTPConnection('proxy',3128,timeout=5)\n"
            if connect:
                code+="c.set_tunnel('allowed.test',443);c.connect();sock=ssl.create_default_context(cafile='/fixtures/origin.crt').wrap_socket(c.sock,server_hostname='allowed.test');sock.sendall(b'GET / HTTP/1.0\\r\\nHost: allowed.test\\r\\n\\r\\n');r=http.client.HTTPResponse(sock);r.begin();print(r.status)\n"
            else:
                code+=f"c.request('GET',{target!r});r=c.getresponse();print(r.status)\n"
            return int(docker('exec',container,'python','-c',code))
        assert fetch(client)==403
        assert fetch(source)==403
        print('PASS: unknown source denied')
        d=api('/configuration/draft'); snap=d['snapshot']; profile=snap['tls_profiles'][0]['id']
        all_id,allow_id=str(uuid.uuid4()),str(uuid.uuid4())
        snap['policies']=[{'id':all_id,'name':'All web fixture','mode':'all_web','destinations':[],'tls_profile_id':profile},{'id':allow_id,'name':'Allowlist fixture','mode':'allowlist','destinations':[{'domain':'allowed.test','include_subdomains':False}],'tls_profile_id':profile}]
        snap['clients']=[{'id':str(uuid.uuid4()),'name':'source fixture','source_networks':['11.250.17.10/32'],'enabled':True,'policy_id':all_id},{'id':str(uuid.uuid4()),'name':'client fixture','source_networks':['11.250.17.20'],'kind':'group','enabled':True,'policy_id':allow_id}]
        def apply_snapshot(snapshot,expected_version):
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
        d=apply_snapshot(snap,d['version'])
        first_applied=api('/status')['applied_revision']
        baseline_manifest=json.loads(docker('exec',proxy,'cat','/data/runtime/lkg.json'))
        assert wait_for(lambda: fetch(client))==200
        assert fetch(client,'http://denied.test/')==403
        assert fetch(client,'http://11.250.17.10/')==403
        assert fetch(source,'http://denied.test/')==200
        assert fetch(client,'http://127.0.0.1/')==403
        assert fetch(source,'http://169.254.169.254/')==403
        assert fetch(client,connect=True)==200
        print('PASS: all_web/allowlist, private/metadata guards, real TLS CONNECT, idempotent operations')
        # Invalid desired configuration must leave live traffic unchanged.
        invalid=copy.deepcopy(snap);invalid['policies'][1]['destinations'][0]['domain']='allowed.test\nhttp_access allow all'
        try:api('/configuration/draft','PUT',{'expected_version':d['version'],'snapshot':invalid});raise AssertionError('injection accepted')
        except urllib.error.HTTPError as exc:assert exc.code==422
        assert fetch(client,'http://denied.test/')==403
        print('PASS: rejected injection leaves runtime unchanged')
        # Per-VM TLS transport must reject downgrade to the HTTP listener.
        tls_id=str(uuid.uuid4());snap['tls_profiles'].append({'id':tls_id,'name':'TLS fixture','mode':'tunnel','proxy_transport':'tls','cert_ref':'proxy-server'});snap['policies'][1]['tls_profile_id']=tls_id
        d=apply_snapshot(snap,d['version'])
        assert fetch(client)==403
        assert fetch(client,tls_proxy=True)==200
        print('PASS: HTTPS proxy with verified certificate; plaintext downgrade denied')
        # Separate process restart and container startup recovery.
        held_code="""import socket,ssl,http.client
sock=socket.create_connection(('proxy',3128),timeout=10)
sock.sendall(b'CONNECT allowed.test:443 HTTP/1.1\\r\\nHost: allowed.test:443\\r\\n\\r\\n')
head=b''
while b'\\r\\n\\r\\n' not in head:head+=sock.recv(1)
assert b'200' in head.split(b'\\r\\n')[0],head
print('READY',flush=True);input()
sock=ssl.create_default_context(cafile='/fixtures/origin.crt').wrap_socket(sock,server_hostname='allowed.test')
sock.sendall(b'GET / HTTP/1.0\\r\\nHost: allowed.test\\r\\n\\r\\n')
r=http.client.HTTPResponse(sock);r.begin();print(r.status)
"""
        held=subprocess.Popen(['docker','exec','-i',source,'python','-c',held_code],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        assert held.stdout.readline().strip()=='READY'
        squid_pid=docker('exec',proxy,'cat','/data/runtime/squid.pid')
        docker('exec',proxy,'pebble','restart','control')
        wait_for(lambda:api('/status'))
        assert docker('exec',proxy,'cat','/data/runtime/squid.pid')==squid_pid
        held.stdin.write('continue\n');held.stdin.flush();out,err=held.communicate(timeout=10)
        assert held.returncode==0 and out.strip()=='200',(out,err)
        assert fetch(client,tls_proxy=True)==200
        docker('exec',proxy,'pebble','restart','squid')
        wait_for(lambda:docker('exec','--user','13:13',proxy,'squid-control','check'))
        assert fetch(client,tls_proxy=True)==200
        docker('restart',proxy)
        wait_for(lambda:api('/status'))
        wait_for(lambda:docker('exec','--user','13:13',proxy,'squid-control','check'))
        assert fetch(client,tls_proxy=True)==200
        assert fetch(client,'http://denied.test/',tls_proxy=True)==403
        print('PASS: independent Go restart, Squid restart gate, container restart persistence')
        current=api('/status')['applied_revision']
        rollback=api('/operations','POST',{'action':'rollback','revision_id':first_applied,'expected_applied_revision':current},str(uuid.uuid4()))
        def rollback_finished():
            result=api('/operations/'+rollback['id'])
            if result['status'] in ['pending','reloading']:raise RuntimeError('rollback pending')
            return result
        assert wait_for(rollback_finished)['status']=='succeeded'
        assert fetch(client)==200
        assert fetch(client,'http://denied.test/')==403
        print('PASS: existing CONNECT survives Go restart; explicit rollback to previous applied revision')
        # Visual settings are actually compiled and applied, not merely saved as UI state.
        current_draft=api('/configuration/draft');advanced=copy.deepcopy(api('/revisions/'+first_applied)['snapshot'])
        advanced['settings']['connect_timeout_seconds']=45
        advanced['settings']['cache_enabled']=True
        d=apply_snapshot(advanced,current_draft['version'])
        assert fetch(client)==200
        # Arbitrary supported directives live in a user section, lifecycle remains managed.
        advanced['configuration_mode']='expert'
        advanced['expert_config']='acl fixture_source src 11.250.17.20/32\nacl fixture_domain dstdomain -n allowed.test\nhttp_access allow fixture_source fixture_domain\nrefresh_pattern -i \\.(deb|rpm)$ 0 20% 4320\n'
        d=apply_snapshot(advanced,d['version'])
        assert fetch(client)==200 and fetch(client,'http://denied.test/')==403
        assert fetch(source)==403  # visual all_web policy is retained but inactive in expert mode
        print('PASS: visual settings and expert squid.conf affect actual Squid traffic')
        # Change the public HTTP port; the private runtime probe must remain functional.
        changed=copy.deepcopy(advanced);changed['settings']['http_port']=8080
        d=apply_snapshot(changed,d['version'])
        probe=docker('exec',client,'python','-c',"import http.client;c=http.client.HTTPConnection('proxy',8080);c.request('GET','http://allowed.test/');print(c.getresponse().status)")
        assert probe=='200'
        changed['settings']['http_port']=3128;d=apply_snapshot(changed,d['version'])
        assert fetch(client)==200
        print('PASS: configurable listener port with independent runtime canary')
        forbidden=copy.deepcopy(changed);forbidden['expert_config']='include /data/app/*'
        try:api('/configuration/draft','PUT',{'expected_version':d['version'],'snapshot':forbidden});raise AssertionError('unsafe include accepted')
        except urllib.error.HTTPError as exc:assert exc.code==422
        # A syntax error passes draft editing but is rejected by real Squid validation.
        broken=copy.deepcopy(changed);broken['expert_config']='this_is_not_a_squid_directive yes'
        bad_draft=api('/configuration/draft','PUT',{'expected_version':d['version'],'snapshot':broken})
        bad_rev=api('/revisions','POST',{'expected_draft_version':bad_draft['version']})
        bad_op=api('/operations','POST',{'action':'validate','revision_id':bad_rev['id'],'expected_applied_revision':api('/status')['applied_revision']},str(uuid.uuid4()))
        def bad_finished():
            value=api('/operations/'+bad_op['id'])
            if value['status'] in ['pending','validating']:raise RuntimeError('validation pending')
            return value
        assert wait_for(bad_finished)['status']=='rejected'
        assert fetch(client)==200
        print('PASS: protected expert directives and real parse failure preserve active traffic')
        # Local reset updates only a verifier, revokes old cookies, and rejects the old password.
        replacement=secrets.token_urlsafe(24)
        output=docker('exec','-i','--user','13:13',proxy,'squid-control','password-reset','--username','admin','--password-stdin',input=replacement+'\n')
        assert replacement not in output and password not in output
        try:api('/status');raise AssertionError('old session survived CLI reset')
        except urllib.error.HTTPError as exc:assert exc.code==401
        session=api('/session');csrf=session['csrf_token']
        try:api('/session','POST',{'username':'admin','password':password});raise AssertionError('old password accepted')
        except urllib.error.HTTPError as exc:assert exc.code==401
        session=api('/session','POST',{'username':'admin','password':replacement});csrf=session['csrf_token']
        assert api('/status')['squid']['state']=='up'
        print('PASS: real CLI password reset, revoked sessions, old-password rejection and new login')
        # Exercise both startup processes on a durable unfinished promotion.
        # Only this disposable fixture is mutated: the expert candidate denies
        # the source identity while the previous structured revision allows it.
        assert fetch(source)==403
        docker('exec',proxy,'pebble','stop','control')
        candidate_manifest=json.loads(docker('exec',proxy,'cat','/data/runtime/lkg.json'))
        journal={'operation_id':str(uuid.uuid4()),'stage':'reloading','previous':baseline_manifest,'candidate':candidate_manifest}
        docker('exec','-i','--user','13:13',proxy,'tee','/data/runtime/journal.json',input=json.dumps(journal))
        docker('restart',proxy)
        wait_for(lambda:api('/status'),seconds=60)
        recovered=json.loads(docker('exec',proxy,'cat','/data/runtime/journal.json'))
        assert recovered['stage']=='terminal' and recovered['outcome']=='rolled_back',recovered
        recovered_status=api('/status')
        assert recovered_status['applied_revision']==first_applied and recovered_status['squid']['state']=='up',recovered_status
        assert fetch(source)==200 and fetch(client)==200
        assert fetch(client,'http://denied.test/')==403
        print('PASS: unfinished promotion recovers previous policy through Squid/control startup and verified rollback')
        # Ingestion runs without an open browser; labels and pagination survive
        # rotation and a control-process restart with the same stored checkpoint.
        def collected():
            page=api('/events?client_id='+snap['clients'][1]['id']+'&limit=2')
            assert len(page['items'])==2 and page['next_cursor'],page
            assert all(v['client_id']==snap['clients'][1]['id'] for v in page['items'])
            assert all(v['client_match_basis']=='policy_at_ingest' and v['revision_id'] is None for v in page['items'])
            return page
        page=wait_for(collected)
        older=api('/events?client_id='+snap['clients'][1]['id']+'&limit=2&cursor='+page['next_cursor'])
        assert not ({v['id'] for v in page['items']} & {v['id'] for v in older['items']})
        saved_id=page['items'][0]['id']
        labels=api('/events/clients')['items'];assert any(v['id']==snap['clients'][1]['id'] for v in labels)
        before_rotate={v['id'] for v in api('/events?limit=200')['items']}
        docker('exec','--user','13:13',proxy,'/usr/sbin/squid','-f','/data/runtime/active.conf','-k','rotate')
        docker('exec',proxy,'pebble','restart','control')
        wait_for(lambda:api('/status'))
        persisted=api('/events?limit=200')['items']
        assert saved_id in {v['id'] for v in persisted}
        assert len(persisted)==len({v['id'] for v in persisted})
        assert before_rotate <= {v['id'] for v in persisted}
        audit=api('/audit?actor=local-cli&action=password_reset')['items']
        assert len(audit)==1 and audit[0]['entity_id']=='admin'
        print('PASS: durable event history, client filters/cursors, real rotation, control restart and administrative audit')
        print('ALL REAL-IMAGE SMOKE CHECKS PASSED')
    except Exception:
        if name+'-proxy' in containers:
            print(docker('logs','--tail','80',name+'-proxy'))
        raise
    finally:
        for container in reversed(containers):
            subprocess.run(['docker','rm','-f','-v',container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        subprocess.run(['docker','network','rm',network],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        subprocess.run(['docker','volume','rm',volume],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
