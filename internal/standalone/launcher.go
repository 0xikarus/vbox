package standalone

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The shortcut asks the running controller to reconcile its managed session,
// keeping chat readiness and desktop launches on the same lifecycle path.
func writeSessionLauncher(home string) error {
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	script := `#!/usr/bin/env python3
import json,pathlib,urllib.request
p=pathlib.Path.home()/'.config/vmbox/desktop-agent.json'
c=json.loads(p.read_text())
r=urllib.request.Request(c['controller']+'/v1/agent-desktop/start-session',data=b'{}',headers={'Authorization':'DesktopAgent '+c['token'],'Content-Type':'application/json'})
try:
 urllib.request.urlopen(r,timeout=95).close()
except Exception:
 import subprocess
 subprocess.run(['xmessage','-center','The managed session is not ready yet. The controller will retry automatically.'])
`
	if err := os.WriteFile(filepath.Join(bin, "vbox-managed-session"), []byte(script), 0700); err != nil {
		return err
	}
	desktop := filepath.Join(home, "Desktop")
	if out, err := exec.Command("xdg-user-dir", "DESKTOP").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); filepath.IsAbs(p) && p != home {
			desktop = p
		}
	}
	if err := os.MkdirAll(desktop, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(desktop, "vbox-managed-session.desktop"), []byte("[Desktop Entry]\nType=Application\nName=vbox session\nComment=Create or open the managed agent session\nExec="+filepath.Join(bin, "vbox-managed-session")+"\nIcon=utilities-terminal\nTerminal=false\nStartupNotify=true\n"), 0700)
}
