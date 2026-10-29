#!/usr/bin/env python3
"""Generate real local demo requests; preserve draft and restore applied policy."""
from collections import Counter
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import json
import os
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
PROXY = 'kraken-kraken-1'
PYTHON = 'python:3.13-alpine@sha256:8bcfa47b04b853a17aaf909362b847b2112fd6ec4105ea553ef9a43bafbb5916'
RUN = 'sc-demo-' + uuid.uuid4().hex[:8]
BINARY = '/tmp/' + RUN
NETWORK = RUN + '-net'
CONTAINERS = []

def docker(*args, input=None):
    p = subprocess.run(['docker', *args], input=input, text=True, capture_output=True)
    if p.returncode:
        raise RuntimeError('Docker step failed: ' + p.stderr[-1500:])
    return p.stdout.strip()

def database():
    code = '''import json,sqlite3
c=sqlite3.connect('file:/data/app/control.sqlite3?mode=ro',uri=True)
out={'count':c.execute('SELECT COUNT(*) FROM proxy_events').fetchone()[0], 'draft':c.execute("SELECT value FROM kv WHERE key='draft'").fetchone()[0], 'groups':c.execute("SELECT COALESCE(client_name,'unknown'),json_extract(body,'$.proxy_result'),COUNT(*) FROM proxy_events GROUP BY 1,2").fetchall()}
print(json.dumps(out))
'''
    return json.loads(docker('run','--rm','--network','none','--user','10013:10013','-v','kraken_control-data:/data:ro',PYTHON,'python','-c',code))

ORIGIN = r'''import http.server,ssl,threading,time
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_HEAD(self): self.send_response(200);self.end_headers()
 def do_GET(self):
  if self.path.startswith('/slow'): time.sleep(.2)
  status=404 if self.path.startswith('/missing') else 503 if self.path.startswith('/unavailable') else 302 if self.path.startswith('/redirect') else 206 if self.path.startswith('/partial') else 304 if self.path.startswith('/not-modified') else 200
  size=262144 if '/large' in self.path else 65536 if '/package' in self.path else 1024
  body=b'' if status==304 else (b'demo fixture payload\n'*(size//21+1))[:size]
  self.send_response(status)
  self.send_header('Content-Length',str(len(body)))
  self.send_header('Cache-Control','no-store')
  if status==302: self.send_header('Location','https://repo.demo.test/package')
  self.end_headers();self.wfile.write(body)
httpd=http.server.ThreadingHTTPServer(('0.0.0.0',80),Handler)
threading.Thread(target=httpd.serve_forever,daemon=True).start()
tls=http.server.ThreadingHTTPServer(('0.0.0.0',443),Handler)
ctx=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);ctx.load_cert_chain('/fixtures/origin.crt','/fixtures/origin.key');tls.socket=ctx.wrap_socket(tls.socket,server_side=True)
tls.serve_forever()
'''
CLIENT = '''import collections,http.client,json,socket,ssl,sys,time
kind=int(sys.argv[1]);counts=collections.Counter()
def request(host,path,method='GET',tls=False):
 c=http.client.HTTPConnection('proxy-demo',3128,timeout=12)
 if tls:
  c.set_tunnel(host,443);c.connect();c.sock=ssl.create_default_context(cafile='/fixtures/origin.crt').wrap_socket(c.sock,server_hostname=host)
 c.request(method,path if tls else 'http://'+host+path,headers={'Host':host,'Connection':'close'})
 r=c.getresponse();r.read();code=r.status;c.close();return code
if kind==0:
 assert request('repo.demo.test','/ready')==200
for i in range(210 if kind==0 else 150 if kind==1 else 60):
 try:
  if kind==2: code=request('repo.demo.test','/blocked')
  elif kind==1: code=request('repo.demo.test' if i%3==0 else 'blocked.demo.test',['/package','/missing','/partial'][i%3])
  elif i%20==0:
   code=request('repo.demo.test','/package',tls=True);counts['https_connect']+=1
  elif i%17==0: code=request('offline.demo.test','/connection-refused')
  elif i%13==0: code=request('169.254.169.254','/metadata-denied')
  elif i%11==0: code=request('status.demo.test','/unavailable')
  else:
   host=['repo.demo.test','packages.demo.test','mirror.demo.test','downloads.demo.test'][i%4]
   path=['/package','/large','/missing','/redirect','/partial','/not-modified','/slow'][i%7]
   code=request(host,path,'HEAD' if i%19==0 else 'GET')
  counts[str(code)]+=1
 except Exception as e: counts[type(e).__name__]+=1
 time.sleep(.06)
print(json.dumps(counts))
'''

def run():
    original = json.loads(docker('exec',PROXY,'cat','/data/runtime/lkg.json'))
    if original['snapshot'].get('configuration_mode') == 'expert':
        raise RuntimeError('Use a structured-mode local installation for demo traffic')
    before = database()
    if before['count'] > 40000:
        raise RuntimeError('History near retention cap; demo was not started')
    connected = False
    prepared = False
    hosts_block = ""
    with tempfile.TemporaryDirectory(prefix=RUN) as temporary:
        directory = Path(temporary)
        arch = docker('image','inspect','kraken:dev','--format','{{.Architecture}}')
        environment = dict(os.environ,GOOS='linux',GOARCH=arch,CGO_ENABLED='0')
        subprocess.run(['go','build','-o',str(directory/'apply'),'./tools/demo-traffic'],cwd=ROOT/'src',env=environment,check=True)
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(directory/'origin.key'),'-out',str(directory/'origin.crt'),'-subj','/CN=repo.demo.test','-addext','subjectAltName=DNS:repo.demo.test,DNS:packages.demo.test,DNS:mirror.demo.test,DNS:downloads.demo.test,DNS:status.demo.test'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        try:
            docker('network','create','--internal','--subnet','11.251.37.0/24',NETWORK)
            docker('network','connect','--gw-priority','-1','--alias','proxy-demo','--ip','11.251.37.2',NETWORK,PROXY); connected=True
            origin=RUN+'-origin';CONTAINERS.append(origin)
            args=['run','-d','--name',origin,'--network',NETWORK,'--ip','11.251.37.3','-v',temporary+':/fixtures:ro']
            for alias in ['repo.demo.test','packages.demo.test','mirror.demo.test','downloads.demo.test','status.demo.test','blocked.demo.test']:
                args+=['--network-alias',alias]
            docker(*args,PYTHON,'python','-u','-c',ORIGIN)
            ready=False
            for _ in range(30):
                try:
                    docker('exec',origin,'python','-c',"import socket;s=socket.create_connection(('127.0.0.1',80),1);s.close()")
                    ready=True;break
                except RuntimeError: time.sleep(.1)
            if not ready: raise RuntimeError('Demo origin did not start')
            offline=RUN+'-offline';CONTAINERS.append(offline)
            docker('run','-d','--name',offline,'--network',NETWORK,'--ip','11.251.37.4','--network-alias','offline.demo.test',PYTHON,'python','-c','import time;time.sleep(600)')
            hosts_block='\n# '+RUN+' begin\n11.251.37.3 repo.demo.test packages.demo.test mirror.demo.test downloads.demo.test status.demo.test blocked.demo.test\n11.251.37.4 offline.demo.test\n# '+RUN+' end\n'
            docker('exec','--user','0:0','-i',PROXY,'tee','-a','/etc/hosts',input=hosts_block)
            docker('cp',str(directory/'apply'),PROXY+':'+BINARY)
            prepared=True
            print(docker('exec','--user','10013:10013',PROXY,BINARY,'start'),flush=True)
            time.sleep(1)
            def client(kind):
                name=RUN+'-client-'+str(kind);CONTAINERS.append(name)
                return json.loads(docker('run','--rm','-i','--name',name,'--network',NETWORK,'--ip','11.251.37.'+str(10+kind),'-v',temporary+':/fixtures:ro',PYTHON,'python','-',str(kind),input=CLIENT))
            counts=Counter()
            with ThreadPoolExecutor(max_workers=3) as pool:
                for result in pool.map(client,range(3)): counts.update(result)
            print('Actual responses: '+json.dumps(dict(counts),sort_keys=True),flush=True)
            assert counts['200'] and counts['403'] and counts['https_connect'],'Missing successful, denied or HTTPS requests'
            expected=sum(count for status,count in counts.items() if status.isdigit())+1
            for _ in range(50):
                after=database()
                if after['count']-before['count']>=expected: break
                time.sleep(.2)
            print('New persisted events: '+str(after['count']-before['count']),flush=True)
            assert after['count']-before['count']>=expected,'Collector did not persist all requests'
            assert after['draft']==before['draft'],'Draft changed during generation'
        finally:
            cleanup_errors=[]
            def cleanup(action):
                try: action()
                except Exception as error: cleanup_errors.append(str(error))
            def remove_hosts():
                current_hosts=docker('exec',PROXY,'cat','/etc/hosts')+'\n'
                if hosts_block not in current_hosts:
                    raise RuntimeError('Temporary hosts block changed; inspect /etc/hosts')
                docker('exec','--user','0:0','-i',PROXY,'tee','/etc/hosts',input=current_hosts.replace(hosts_block,''))
            def restore():
                print(docker('exec','--user','10013:10013',PROXY,BINARY,'restore'),flush=True)
                restored=json.loads(docker('exec',PROXY,'cat','/data/runtime/lkg.json'))
                assert restored['snapshot']==original['snapshot'],'Original policy not restored'
                docker('exec','--user','0:0',PROXY,'rm','-f',BINARY)
            if hosts_block: cleanup(remove_hosts)
            if prepared: cleanup(restore)
            for name in CONTAINERS:
                # --rm clients may already be absent; inspect before removal.
                if name in docker('ps','-a','--format','{{.Names}}').splitlines():
                    cleanup(lambda name=name: docker('rm','-f','-v',name))
            if connected: cleanup(lambda: docker('network','disconnect',NETWORK,PROXY))
            if NETWORK in docker('network','ls','--format','{{.Name}}').splitlines():
                cleanup(lambda: docker('network','rm',NETWORK))
            if cleanup_errors:
                raise RuntimeError('Demo cleanup needs attention (helper '+BINARY+'): '+'; '.join(cleanup_errors))
    print('Original policy restored; draft unchanged; temporary network removed.',flush=True)
    print('History groups: '+json.dumps(database()['groups'],ensure_ascii=False),flush=True)

if __name__ == '__main__': run()
