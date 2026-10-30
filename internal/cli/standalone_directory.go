package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

func (a *App) writeStandaloneHostManifest(ctx context.Context, i *standaloneInstance) error {
	script := `import sys,json,os,pathlib,tempfile
m=json.load(sys.stdin)
p=pathlib.Path(m.get('directory') or pathlib.Path.home()/'.local/share/vbox-standalone/instances'/m['id']).expanduser()
if not p.is_absolute(): raise SystemExit('instance directory must be absolute')
if p.is_symlink(): raise SystemExit('instance directory may not be a symlink')
if p.exists() and any(p.iterdir()):
 f=p/'instance.json'
 if not f.is_file() or json.loads(f.read_text()).get('id')!=m['id']: raise SystemExit('instance directory is not empty or has different ownership')
p.mkdir(parents=True,exist_ok=True,mode=0o700)
m['directory']=str(p)
fd,tmp=tempfile.mkstemp(prefix='.instance-',dir=p)
with os.fdopen(fd,'w') as f: json.dump(m,f);f.flush();os.fsync(f.fileno())
os.replace(tmp,p/'instance.json')
print(json.dumps({'directory':str(p)}))`
	data, err := json.Marshal(i)
	if err != nil {
		return err
	}
	argv := []string{"python3", "-c", script}
	if i.Host != "" {
		argv = []string{"ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", i.Host, "python3 -c " + shellQuote(script)}
	}
	result, err := a.Runner.Run(ctx, argv, bytes.NewReader(data), nil, nil)
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("could not create or update the dedicated host instance directory")
	}
	var response struct {
		Directory string `json:"directory"`
	}
	if err = json.Unmarshal(result.Stdout, &response); err != nil {
		return err
	}
	i.Directory = response.Directory
	return nil
}

func (a *App) cleanupStandaloneDirectory(ctx context.Context, i standaloneInstance, verifyOnly bool) error {
	if i.Directory == "" {
		return nil
	}
	script := `import json,pathlib,sys,shutil
m=json.load(sys.stdin)
p=pathlib.Path(m['directory']).expanduser()
if not p.exists() and not p.is_symlink(): raise SystemExit(0)
if not p.is_absolute() or p.is_symlink() or p.resolve() in (pathlib.Path('/'),pathlib.Path.home().resolve()): raise SystemExit('unsafe instance directory')
f=p/'instance.json'
if f.is_symlink() or not f.is_file() or json.loads(f.read_text()).get('id')!=m['id']: raise SystemExit('directory ownership mismatch')
if not m['verifyOnly']: shutil.rmtree(p)`
	data, err := json.Marshal(map[string]any{"id": i.ID, "directory": i.Directory, "verifyOnly": verifyOnly})
	if err != nil {
		return err
	}
	argv := []string{"python3", "-c", script}
	if i.Host != "" {
		argv = []string{"ssh", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", i.Host, "python3 -c " + shellQuote(script)}
	}
	result, err := a.Runner.Run(ctx, argv, bytes.NewReader(data), nil, nil)
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("could not verify or remove the owned instance directory; registry retained for retry")
	}
	return nil
}
