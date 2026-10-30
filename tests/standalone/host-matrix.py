#!/usr/bin/env python3
"""Opt-in sequential lifecycle tests on explicitly authorized disposable hosts.

Targets JSON contains name, ssh (configured alias), address, user, and key (path).
The caller supplies SSH configuration through PATH/config; no host identity checks
are bypassed. Credentials are imported only through the CLI's private profile path.
"""
import argparse
import json
import secrets
import os
from pathlib import Path
import shlex
import socket
import subprocess
import time
import urllib.request


def run(argv, *, data=None, env=None, timeout=240):
    p = subprocess.run(argv, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                       env=env, timeout=timeout)
    if p.returncode:
        # Commands can involve private profile transport. Never include their
        # captured input/output in an exception or report.
        raise RuntimeError("test operation failed: " + Path(argv[0]).name)
    return p.stdout


SMOKE = r'''
import json,pathlib,time,urllib.request
cfg=json.loads(pathlib.Path('/data/standalone/config.json').read_text())
login=urllib.request.Request('http://127.0.0.1:8080/auth/login',data=json.dumps({'password':__TEST_PASSWORD_JSON__}).encode(),headers={'Content-Type':'application/json'})
with urllib.request.urlopen(login,timeout=15) as f:cookie=f.headers['Set-Cookie'].split(';')[0]

def api(path,data=None,key=None):
 h={'Authorization':'Bearer '+cfg['capabilities']['chat'],'Content-Type':'application/json','Cookie':cookie}
 if key:h['Idempotency-Key']=key
 r=urllib.request.Request('http://127.0.0.1:8080'+path,data=None if data is None else json.dumps(data).encode(),headers=h)
 with urllib.request.urlopen(r,timeout=15) as f:return json.load(f)
for _ in range(120):
 if api('/v1/standalone/status')['state']=='ready':break
 time.sleep(1)
else:raise SystemExit('native readiness timeout')
api('/v1/standalone/messages',{'text':'Authorized disposable standalone smoke test: use take_screenshot once, then use chat_message to reply exactly MATRIX_STANDALONE_OK. Do nothing else.'},'matrix-smoke')
for _ in range(180):
 messages=api('/v1/standalone/messages')
 if any(m['direction']=='agent' and 'MATRIX_STANDALONE_OK' in m['text'] for m in messages):break
 time.sleep(1)
else:raise SystemExit('native reply timeout')
counts={'screenshot':0,'reply':0}
for p in (pathlib.Path.home()/'.claude/projects').rglob('*.jsonl'):
 for line in p.open():
  try:o=json.loads(line)
  except ValueError:continue
  blocks=o.get('message',{}).get('content',[])
  if not isinstance(blocks,list):continue
  for b in blocks:
   if not isinstance(b,dict) or b.get('type')!='tool_use':continue
   if b.get('name','').endswith('take_screenshot'):counts['screenshot']+=1
   if b.get('name','').endswith('chat_message') and 'MATRIX_STANDALONE_OK' in str(b.get('input',{}).get('text','')):counts['reply']+=1
assert counts['screenshot'] and counts['reply'],'native tool confirmation missing'
print(json.dumps(counts))
'''

PROXY = r'''
import socket,threading,select
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('0.0.0.0',9223));s.listen(8)
def forward(c):
 try:
  u=socket.create_connection(('127.0.0.1',9222),10)
  with c,u:
   while True:
    r,_,_=select.select([c,u],[],[],30)
    if not r:return
    for src in r:
     data=src.recv(65536)
     if not data:return
     (u if src is c else c).sendall(data)
 except OSError:c.close()
while True:
 c,_=s.accept();threading.Thread(target=forward,args=(c,),daemon=True).start()
'''


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--targets', required=True)
    p.add_argument('--cli', required=True)
    p.add_argument('--image-archive', required=True)
    p.add_argument('--image-tag', required=True)
    p.add_argument('--profile', required=True)
    p.add_argument('--registry', required=True)
    p.add_argument('--report', required=True)
    args = p.parse_args()
    targets = json.loads(Path(args.targets).read_text())
    env = dict(os.environ, VMBOX_CONFIG=str(Path(args.registry)/'config.json'))
    reports = []
    password=secrets.token_urlsafe(32)
    password_file=Path(args.registry)/'disposable-test-password'
    password_file.parent.mkdir(parents=True,exist_ok=True)
    password_file.write_text(password);password_file.chmod(0o600)
    for target in targets:
        result = {'host': target['name'], 'checks': [], 'cleanup': False}
        reports.append(result)
        name = 'standalone-' + target['name'] + '-disposable-test'
        engine = 'ssh://' + target['ssh']
        container = None
        instance = None
        tunnel = None

        def docker(*cmd, data=None):
            return run(['docker', '--host', engine, *cmd], data=data, env=env)

        def cli(*cmd):
            return run([args.cli, 'standalone', *cmd], env=env)

        try:
            print(target['name'] + ': loading test image', flush=True)
            with open(args.image_archive, 'rb') as f:
                r = subprocess.run(['ssh', target['ssh'], 'docker load'], stdin=f,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=600)
            if r.returncode: raise RuntimeError('test image load failed')
            instance = json.loads(cli('create', name, '--host', target['ssh'], '--mode', 'container',
                                      '--agent', 'claude', '--cpus', '1', '--memory', '2g',
                                      '--image', args.image_tag, '--password-file', str(password_file), '--profile', args.profile, '--no-dialog'))
            container = 'vmbox-' + instance['id']
            observed = json.loads(docker('inspect', container))[0]
            limits = observed['HostConfig']
            assert limits['Memory'] == 2*1024**3 and limits['NanoCpus'] == 10**9
            assert limits['MemorySwap'] == limits['Memory']
            result['checks'].append('2 GiB / 1 CPU, no extra swap')

            print(target['name'] + ': testing real Claude + screenshot + MCP reply', flush=True)
            result['native_tools'] = json.loads(docker('exec', '-i', '--user', 'vmbox', container,
                                                      'python3', '-', data=SMOKE.replace('__TEST_PASSWORD_JSON__',json.dumps(password)).encode()))
            result['checks'].append('real Claude profile, screenshot and chat reply')
            port_before = docker('port', container, '8080').decode().strip()
            cli('stop', name)
            assert json.loads(docker('inspect', container))[0]['State']['Pid'] == 0
            cli('start', name, '--port', str(instance['port']))
            assert docker('port', container, '8080').decode().strip() == port_before
            result['checks'].append('shutdown without processes and explicit stable startup port')

            print(target['name'] + ': testing dashboard browser buttons', flush=True)
            docker('exec', '-d', '--user', 'vmbox', container, 'python3', '-c', PROXY)
            docker('exec', '-d', '--user', 'vmbox', container, 'chromium', '--headless', '--no-sandbox',
                   '--disable-gpu', '--disable-dev-shm-usage', '--remote-debugging-port=9222',
                   '--user-data-dir=/tmp/vbox-matrix-browser', 'about:blank')
            observed = json.loads(docker('inspect', container))[0]
            address = next(iter(observed['NetworkSettings']['Networks'].values()))['IPAddress']
            with socket.socket() as s:
                s.bind(('127.0.0.1', 0)); local = s.getsockname()[1]
            tunnel = subprocess.Popen(['/usr/bin/ssh', '-F', '/dev/null', '-i', target['key'],
                                       '-l', target.get('user', 'root'), '-o', 'BatchMode=yes',
                                       '-o', 'StrictHostKeyChecking=yes', '-o', 'ExitOnForwardFailure=yes',
                                       '-N', '-L', f'127.0.0.1:{local}:{address}:9223', target['address']],
                                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            debug = f'http://127.0.0.1:{local}'
            for _ in range(60):
                try:
                    with urllib.request.urlopen(debug+'/json/version', timeout=2): break
                except Exception: time.sleep(1)
            else: raise RuntimeError('disposable browser debugging endpoint unavailable')
            browser_env = dict(env, VMBOX_STANDALONE_PASSWORD_FILE=str(password_file), VMBOX_STANDALONE_CONTAINER=container,
                               VMBOX_STANDALONE_DOCKER_HOST=engine, VMBOX_STANDALONE_CDP=debug,
                               VMBOX_STANDALONE_ORIGIN=f'http://{address}:8080')
            run(['node', 'tests/standalone/browser-live.mjs'], env=browser_env, timeout=180)
            result['checks'].append('dashboard buttons: desktop, read-only live view, persistent chat')
            tunnel.terminate(); tunnel.wait(timeout=10); tunnel = None

            # Simulate an interrupted earlier deletion. The CLI must still find
            # and remove owned volumes/networks when the container is absent.
            cli('stop', name)
            docker('rm', container)
            cli('shutdown', name, '--cleanup', '--yes')
            for kind, resource in [('container', container), ('network', container+'-net'),
                                   ('volume', container+'-data')]:
                r = subprocess.run(['docker', '--host', engine, kind, 'inspect', resource],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
                assert r.returncode, kind+' remains'
            verify = "import pathlib;assert not pathlib.Path("+repr(instance['directory'])+").exists()"
            run(['ssh', target['ssh'], 'python3 -c '+shlex.quote(verify)], env=env)
            assert not (Path(args.registry)/'standalone'/(name+'.json')).exists()
            result['checks'].append('orphan-safe complete teardown: container, network, volume, host directory, registry')
            result['cleanup'] = True
            print(target['name'] + ': PASS; disposable instance fully removed', flush=True)
        except Exception as e:
            result['error'] = type(e).__name__ + ': ' + str(e)
            print(target['name'] + ': FAIL (see sanitized report)', flush=True)
        finally:
            if tunnel:
                tunnel.terminate(); tunnel.wait(timeout=10)
            record = Path(args.registry)/'standalone'/(name+'.json')
            if record.exists():
                try:
                    cli('delete', name, '--yes'); result['cleanup'] = True
                except Exception: result['cleanup'] = False
            # Remove only this test image, and only if no container still uses it.
            try:
                if not docker('ps', '-a', '--filter', 'ancestor='+args.image_tag, '--quiet').strip():
                    docker('image', 'rm', args.image_tag)
                    result['image_removed'] = True
            except Exception: result['image_removed'] = False
            report = Path(args.report); report.parent.mkdir(parents=True, exist_ok=True)
            report.write_text(json.dumps(reports, indent=2)); report.chmod(0o600)
    password_file.unlink(missing_ok=True)
    if any('error' in r or not r['cleanup'] for r in reports): raise SystemExit(1)


if __name__ == '__main__': main()
