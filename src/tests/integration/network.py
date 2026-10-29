#!/usr/bin/env python3
"""Network ACL integration tests on a real Squid image.

Fixtures: proxy, HTTP origin (11.251.17.10), DNS server (11.251.17.53),
client (11.251.17.20) on 11.251.17.0/24. The client has all_web policy with
DNS pointed at the fixture server. Verifies protected/private destination
denials (literal and DNS-resolved), DNS rebinding, CONNECT denials,
alternative loopback encodings and allow_private_destinations behaviour.
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

IMAGE = os.environ.get('KRAKEN_TEST_IMAGE', 'kraken:dev')
CONTROL_USER = os.environ.get('KRAKEN_CONTROL_USER', '10013:10013')
PYTHON = 'python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916'
name = 'sc-net-' + uuid.uuid4().hex[:8]
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

with tempfile.TemporaryDirectory(prefix='kraken-net-') as temporary:
    fixtures = Path(temporary)
    try:
        docker('network', 'create', '--subnet', '11.251.17.0/24', network)
        docker('volume', 'create', volume)
        docker('run', '--rm', '--user', CONTROL_USER, '-v', volume+':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'dev-cert')
        password = secrets.token_urlsafe(24)
        docker('run', '--rm', '-i', '--user', CONTROL_USER, '-v', volume+':/data', '--entrypoint', '/usr/local/bin/kraken', IMAGE, 'bootstrap', '--password-stdin', input=password+'\n')
        proxy = name+'-proxy'; containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.251.17.2', '-p', '127.0.0.1::8443', '-v', volume+':/data', '-e', 'KRAKEN_PUBLIC_ORIGIN=https://localhost', '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
        port = docker('port', proxy, '8443/tcp').rsplit(':',1)[1]
        origin = 'https://localhost:'+port
        docker('rm', '-f', '-v', proxy); containers.remove(proxy)
        containers.append(proxy)
        docker('run', '-d', '--name', proxy, '--network', network, '--network-alias', 'proxy', '--ip', '11.251.17.2', '-p', '127.0.0.1:'+port+':8443', '-v', volume+':/data', '-e', 'KRAKEN_PUBLIC_ORIGIN='+origin, '-e', 'KRAKEN_PROXY_CERT=/data/certs/proxy.crt', '-e', 'KRAKEN_PROXY_KEY=/data/certs/proxy.key', IMAGE)
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

        # HTTP origin at 11.251.17.10 logging every accessed path.
        origin_code = '''import http.server,pathlib
LOG=pathlib.Path('/tmp/access.log');LOG.touch(exist_ok=True)
class H(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  with LOG.open('a') as f: f.write(self.path+'\\n')
  self.send_response(200);self.end_headers();self.wfile.write(b'fixture origin')
 def log_message(self,*a):pass
http.server.HTTPServer(('0.0.0.0',80),H).serve_forever()
'''
        fixtures.joinpath('origin.py').write_text(origin_code)

        # Minimal DNS responder: A/AAAA, TTL 0, with rebinding for rebind.test.
        dns_code = '''import socket,struct,threading
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
 if rd:ans=b'\\xc0\\x0c'+struct.pack('!HHIH',qt,1,0,len(rd))+rd
 s.sendto(hdr+q+ans,a)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('0.0.0.0',53))
while True:
 d,a=s.recvfrom(512);threading.Thread(target=rep,args=(d,a,s),daemon=True).start()
'''
        fixtures.joinpath('dns.py').write_text(dns_code)

        for suffix, ip, command in [('origin','11.251.17.10',['python','/fixtures/origin.py']),('dns','11.251.17.53',['python','/fixtures/dns.py']),('client','11.251.17.20',['python','-c','import time;time.sleep(3600)'])]:
            n=name+'-'+suffix; containers.append(n)
            docker('run','-d','--name',n,'--network',network,'--ip',ip,'-v',str(fixtures)+':/fixtures:ro',PYTHON,*command)
        client = name+'-client'

        # all_web policy with DNS pointed at the fixture and short connect timeout.
        d=api('/configuration/draft'); snap=d['snapshot']; profile=snap['tls_profiles'][0]['id']
        all_id=str(uuid.uuid4())
        snap['policies']=[{'id':all_id,'name':'All web network','mode':'all_web','destinations':[],'tls_profile_id':profile}]
        snap['clients']=[{'id':str(uuid.uuid4()),'name':'network client','source_networks':['11.251.17.20'],'enabled':True,'policy_id':all_id}]
        snap['settings']['dns_nameservers']=['11.251.17.53']
        snap['settings']['positive_dns_ttl_seconds']=2
        snap['settings']['negative_dns_ttl_seconds']=1
        snap['settings']['allow_private_destinations']=False
        snap['settings']['connect_timeout_seconds']=3
        snap['settings']['read_timeout_seconds']=3
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
        print('PASS: all_web policy with DNS settings applied')

        def fetch(target='http://public.test/'):
            code="import http.client\n"
            code+="c=http.client.HTTPConnection('proxy',3128,timeout=8)\n"
            code+=f"c.request('GET',{target!r});r=c.getresponse();r.read()\n"
            code+="print(r.status);print(r.getheader('X-Squid-Error') or '')\n"
            out=docker('exec',client,'python','-c',code)
            lines=out.strip().split('\n')
            return int(lines[0]),(lines[1] if len(lines)>1 else '')

        def connect_fetch(target):
            code="import socket\n"
            code+="s=socket.create_connection(('proxy',3128),timeout=8)\n"
            code+=f"s.sendall(b'CONNECT {target} HTTP/1.1\\r\\nHost: {target}\\r\\n\\r\\n')\n"
            code+="data=b''\n"
            code+="while b'\\r\\n\\r\\n' not in data:\n"
            code+=" chunk=s.recv(1024)\n"
            code+=" if not chunk:break\n"
            code+=" data+=chunk\n"
            code+="status_line=data.split(b'\\r\\n')[0]\n"
            code+="parts=status_line.split()\n"
            code+="status=int(parts[1]) if len(parts)>1 else 0\n"
            code+="err=''\n"
            code+="for line in data.split(b'\\r\\n'):\n"
            code+=" if line.lower().startswith(b'x-squid-error:'):\n"
            code+="  err=line.split(b':',1)[1].strip().decode()\n"
            code+="print(status);print(err)\n"
            out=docker('exec',client,'python','-c',code)
            lines=out.strip().split('\n')
            return int(lines[0]),(lines[1] if len(lines)>1 else '')

        def acl_denied(result):
            return result[0]==403 and result[1].startswith('ERR_ACCESS_DENIED')

        # 1. Control: DNS fixture resolves public.test to the origin.
        def control_ok():
            assert fetch('http://public.test/control')[0]==200
            return True
        wait_for(control_ok,seconds=30)
        print('PASS: 1. DNS fixture resolves public.test to origin (200)')

        # 2. Literal IP destinations denied by ACL (403 + ERR_ACCESS_DENIED).
        for target in ['http://127.0.0.1/','http://169.254.169.254/latest/meta-data/','http://10.0.0.1/','http://[::1]/','http://[::ffff:127.0.0.1]/','http://[fe80::1]/','http://[2002:7f00:1::1]/','http://[2001:0:7f00:1::1]/','http://[64:ff9b:1::7f00:1]/','http://[64:ff9b::a9fe:a9fe]/']:
            result=fetch(target)
            assert acl_denied(result),f'{target} expected 403 ACL, got {result}'
        print('PASS: 2. literal IP destinations denied by protected/private ACL')

        # 3. DNS-resolved protected/private names denied by ACL.
        for target in ['http://loopback.test/','http://metadata.test/','http://private.test/','http://v6loop.test/']:
            result=fetch(target)
            assert acl_denied(result),f'{target} expected 403 ACL, got {result}'
        print('PASS: 3. DNS-resolved protected/private names denied by ACL')

        # 4. CONNECT to protected/private denied (403 from proxy).
        for target in ['127.0.0.1:443','[::1]:443','metadata.test:443']:
            result=connect_fetch(target)
            assert result[0]==403,f'CONNECT {target} expected 403, got {result}'
        print('PASS: 4. CONNECT to protected/private denied (403)')

        # 5. Alternative loopback encodings do not reach origin (status != 200).
        for target in ['http://2130706433/','http://0x7f000001/','http://0177.0.0.1/']:
            result=fetch(target)
            assert result[0]!=200,f'{target} expected non-200, got {result}'
        print('PASS: 5. alternative loopback encodings do not reach origin')

        # 6. DNS rebinding: first request 200, after TTL expiry 403 ACL.
        assert fetch('http://rebind.test/rebind-first')[0]==200,'rebind.test first expected 200'
        time.sleep(3)
        result=fetch('http://rebind.test/rebind-second')
        assert acl_denied(result),f'rebind.test second expected 403 ACL, got {result}'
        print('PASS: 6. DNS rebinding denied after TTL expiry')

        # 7. allow_private_destinations lifts private dst; protected still denied.
        applied_rev=api('/status')['applied_revision']
        relaxed=copy.deepcopy(api('/revisions/'+applied_rev)['snapshot'])
        relaxed['settings']['allow_private_destinations']=True
        d=apply_snapshot(relaxed,api('/configuration/draft')['version'])
        result=fetch('http://private.test/')
        assert not acl_denied(result),f'private.test with allow_private should not be 403 ACL, got {result}'
        for target in ['http://loopback.test/','http://metadata.test/','http://[2002:7f00:1::1]/']:
            result=fetch(target)
            assert acl_denied(result),f'{target} still 403 ACL expected with allow_private, got {result}'
        print('PASS: 7. allow_private lifts private dst; protected ranges still denied')

        # 8. Origin log shows only control requests, no forbidden traffic.
        log=docker('exec',name+'-origin','cat','/tmp/access.log')
        assert set(log.split())=={'/control','/rebind-first'},f'unexpected origin requests: {log}'
        print('PASS: 8. origin log shows only control requests, no forbidden traffic')

        print('ALL NETWORK CHECKS PASSED')
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
