package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	hubservice "github.com/0xikarus/vmbox-service/internal/hub"
)

func hubLocalPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(p, "\r\n\x00%\"") {
		return "", fmt.Errorf("unsupported hub directory")
	}
	return p, nil
}
func (a *App) hubPython(ctx context.Context, host, script string, input []byte) ([]byte, error) {
	argv := []string{"python3", "-c", script}
	if host != "" {
		argv = []string{"ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", host, "python3 -c " + shellQuote(script)}
	}
	r, err := a.Runner.Run(ctx, argv, bytes.NewReader(input), nil, nil)
	if err != nil || r.ExitCode != 0 {
		return nil, fmt.Errorf("hub host operation failed; verify SSH access and directory ownership")
	}
	return r.Stdout, nil
}
func (a *App) writeHubConfig(ctx context.Context, host, dir string, c hubservice.Config) (string, error) {
	script := `import sys,json,pathlib,os,tempfile
x=json.load(sys.stdin);p=pathlib.Path(x['dir']).expanduser().absolute()
if p==pathlib.Path('/') or p==pathlib.Path.home() or p.is_symlink() or any(c in str(p) for c in '\n\r\0%"'):raise SystemExit('unsafe directory')
if (p/'hub.json').exists():raise SystemExit('hub already initialized')
p.mkdir(parents=True,exist_ok=True,mode=0o700)
if any(p.iterdir()):raise SystemExit('hub directory must be empty')
os.chmod(p,0o700)
fd,tmp=tempfile.mkstemp(prefix='.hub-',dir=p)
with os.fdopen(fd,'w') as f:json.dump(x['config'],f);f.flush();os.fsync(f.fileno())
os.replace(tmp,p/'hub.json');print(str(p))`
	data, _ := json.Marshal(map[string]any{"dir": dir, "config": c})
	out, err := a.hubPython(ctx, host, script, data)
	return strings.TrimSpace(string(out)), err
}
func (a *App) readHubConfig(ctx context.Context, host, dir string) (hubservice.Config, string, error) {
	var result struct {
		Config hubservice.Config `json:"config"`
		Dir    string            `json:"dir"`
	}
	script := `import json,pathlib,sys
p=pathlib.Path(json.load(sys.stdin)['dir']).expanduser().absolute()
print(json.dumps({'dir':str(p),'config':json.loads((p/'hub.json').read_text())}))`
	in, _ := json.Marshal(map[string]string{"dir": dir})
	out, err := a.hubPython(ctx, host, script, in)
	if err != nil {
		return result.Config, "", err
	}
	defer clear(out)
	if err = json.Unmarshal(out, &result); err != nil {
		return result.Config, "", fmt.Errorf("invalid hub configuration")
	}
	if len(result.Config.APIToken) != 64 || !strings.HasPrefix(result.Config.ID, "hub-") || len(result.Config.ID) != 24 {
		return result.Config, "", fmt.Errorf("invalid hub identity")
	}
	return result.Config, result.Dir, nil
}

func (a *App) hubContainerAction(ctx context.Context, host, id, action string) error {
	in, _ := json.Marshal(map[string]string{"id": id, "action": action})
	_, err := a.hubPython(ctx, host, `import json,sys,subprocess
x=json.load(sys.stdin);name='vbox-'+x['id']
if x['action'] not in ('start','stop'):raise SystemExit('invalid hub action')
r=subprocess.run(['docker','inspect',name],capture_output=True,text=True,check=True)
v=json.loads(r.stdout)[0]
if v['Config']['Labels'].get('dev.vmbox.hub')!=x['id']:raise SystemExit('ownership mismatch')
subprocess.run(['docker',x['action'],name],stdout=subprocess.DEVNULL,check=True)`, in)
	return err
}
func (a *App) installHub(ctx context.Context, host, dir string, c hubservice.Config, autostart bool) error {
	if existing, p, err := a.readHubConfig(ctx, host, dir); err == nil {
		c = existing
		dir = p
	} else {
		var err error
		dir, err = a.writeHubConfig(ctx, host, dir, c)
		if err != nil {
			return fmt.Errorf("initialize hub: %w", err)
		}
	}
	root := a.WorkingDir
	if root == "" {
		root, _ = os.Getwd()
	}
	root, _ = filepath.Abs(root)
	for _, name := range []string{"Dockerfile.standalone", "Dockerfile.hub"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("hub installation requires the repository checkout")
		}
	}
	docker := []string{"docker"}
	if host != "" {
		docker = append(docker, "--host", "ssh://"+host)
	}
	hubImage := "vbox-hub:" + c.ID
	for _, build := range []struct{ file, image string }{{"Dockerfile.standalone", c.Image}, {"Dockerfile.hub", hubImage}} {
		fmt.Fprintln(a.Err, "Building "+build.file+" on the hub host…")
		argv := append(append([]string{}, docker...), "build", "--file", filepath.Join(root, build.file), "--tag", build.image, root)
		r, err := a.Runner.Run(ctx, argv, nil, a.Err, a.Err)
		if err != nil || r.ExitCode != 0 {
			return fmt.Errorf("hub image build failed; configuration retained for retry")
		}
	}
	input, _ := json.Marshal(map[string]any{"dir": dir, "id": c.ID, "image": hubImage, "autostart": autostart})
	setup := `import sys,json,pathlib,os,subprocess,stat
x=json.load(sys.stdin);p=pathlib.Path(x['dir']);name='vbox-'+x['id']
info=json.loads(subprocess.check_output(['docker','info','--format','{{json .}}']))
if info.get('OperatingSystem','').lower().startswith('docker desktop'):raise SystemExit('hub requires a native Linux Docker host')
root=info['DockerRootDir'];sock='/var/run/docker.sock'
if not pathlib.Path(root).is_absolute() or root=='/':raise SystemExit('invalid Docker root')
if not stat.S_ISSOCK(os.stat(sock).st_mode):raise SystemExit('local Docker socket required')
result=subprocess.run(['docker','inspect',name],capture_output=True,text=True)
if result.returncode==0:
 old=json.loads(result.stdout)[0]
 if old['Config']['Labels'].get('dev.vmbox.hub')!=x['id']:raise SystemExit('hub container ownership mismatch')
 subprocess.run(['docker','stop',name],check=True,stdout=subprocess.DEVNULL)
 subprocess.run(['docker','rm',name],check=True,stdout=subprocess.DEVNULL)
args=['docker','run','-d','--name',name,'--network','host','--restart','unless-stopped' if x['autostart'] else 'no','--label','dev.vmbox.hub='+x['id'],'--cpus','1','--memory','512m','--memory-swap','512m','--pids-limit','256','--user',str(os.getuid())+':'+str(os.getgid()),'--group-add',str(os.stat(sock).st_gid),'--log-driver','local','--log-opt','max-size=10m','--log-opt','max-file=3','--env','HOME=/tmp','--mount','type=bind,src='+str(p)+',dst='+str(p),'--mount','type=bind,src='+sock+',dst='+sock,'--mount','type=bind,src='+root+',dst='+root+',readonly',x['image'],'vbox','hub','serve','--data',str(p)]
subprocess.run(args,check=True,stdout=subprocess.DEVNULL)
print('Installed the resource-capped hub manager; its Docker socket is never mounted into workers.')`
	if _, err := a.hubPython(ctx, host, setup, input); err != nil {
		return fmt.Errorf("could not start hub container: %w", err)
	}
	fmt.Fprintf(a.Out, "Hub installed and started: %s\nListen: %s\nData: %s\nAutostart with Docker: %t\nWorker files and API credentials remain private.\n", c.ID, c.Listen, dir, autostart)
	return nil
}

// Remote CLI management reuses SSH, supporting private listeners and .local
// names even when the static CLI lacks the host system's mDNS resolver.
func (a *App) hubLoopbackRequest(ctx context.Context, host, dir, base, method, path, key string, in, out any) error {
	input, err := json.Marshal(map[string]any{"dir": dir, "base": base, "method": method, "path": path, "key": key, "body": in})
	if err != nil {
		return err
	}
	script := `import json,sys,pathlib,urllib.request,urllib.error
x=json.load(sys.stdin);c=json.loads((pathlib.Path(x['dir'])/'hub.json').read_text());h={'Authorization':'Bearer '+c['apiToken'],'Content-Type':'application/json'}
if x['key']:h['Idempotency-Key']=x['key']
data=None if x['body'] is None else json.dumps(x['body']).encode()
r=urllib.request.Request(x['base']+'/api/'+x['path'],data=data,method=x['method'],headers=h)
try:
 with urllib.request.urlopen(r,timeout=30) as f:result={'status':f.status,'body':json.load(f)}
except urllib.error.HTTPError as e:result={'status':e.code,'error':e.read(1000).decode(errors='replace')}
print(json.dumps(result))`
	data, err := a.hubPython(ctx, host, script, input)
	if err != nil {
		return err
	}
	defer clear(data)
	var result struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil {
		return fmt.Errorf("invalid hub response")
	}
	if result.Status < 200 || result.Status >= 300 {
		return fmt.Errorf("hub: %s", strings.TrimSpace(result.Error))
	}
	if out != nil {
		return json.Unmarshal(result.Body, out)
	}
	return nil
}
