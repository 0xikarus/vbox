"""Opt-in credential-free CLI registration and Chromium memory smoke test."""
import subprocess,uuid,time,json,sys
name='vmbox-clients-disposable-'+uuid.uuid4().hex[:10]
def cmd(*args,timeout=60):return subprocess.run(args,check=True,capture_output=True,text=True,timeout=timeout).stdout
def worker(*args,timeout=60):return cmd('docker','exec',name,*args,timeout=timeout)
try:
 cmd('docker','run','-d','--name',name,'--label','io.vmbox.test=desktop-mvp','--network','none',sys.argv[1])
 worker('vmbox-runtime','native-bind','a'*64)
 for agent in ['codex','claude','opencode']:
  worker('vmbox-runtime','desktop-register',agent)
  worker('vmbox-runtime','desktop-register',agent)
  output=worker(agent,'mcp','list')
  assert 'vmbox-desktop' in output, agent+' registration missing'
  print(agent+': '+output.strip())
 worker('vmbox-runtime','interactive-start','a'*64,'shell-fixture','shell')
 cmd('docker','exec','-d',name,'vmbox-runtime','desktop-browser')
 for attempt in range(50):
  ready=subprocess.run(['docker','exec',name,'test','-f','/data/home/.config/vmbox/chromium/DevToolsActivePort'],capture_output=True)
  if ready.returncode==0:break
  time.sleep(.1)
 else:raise AssertionError('Chromium did not start')
 time.sleep(2)
 def rss():
  rows=worker('ps','-eo','rss,comm').splitlines()[1:]
  return sum(int(row.split()[0]) for row in rows if row.split()[-1] in ['chromium','chrome_crashpad'])//1024
 print('Chromium blank-tab process RSS MiB:',rss())
 worker('env','DISPLAY=:99','chromium','--no-sandbox','--user-data-dir=/data/home/.config/vmbox/chromium','--new-tab',*['data:text/html,<title>fixture'+str(i)+'</title><h1>synthetic desktop task</h1>' for i in range(5)])
 time.sleep(2)
 print('Chromium six synthetic tabs process RSS MiB:',rss())
 print('Chromium private persistent profile launch: PASS (explicit dedicated-container mode)')

finally:
 subprocess.run(['docker','rm','-f','-v',name],stdout=subprocess.DEVNULL)
