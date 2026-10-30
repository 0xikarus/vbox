package standalone

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"strings"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if s.Config.PasswordRequired && !s.loggedIn(r) {
		s.authPage(w, r)
		return
	}
	if s.Config.PrivateLinksOnly {
		_, _ = io.WriteString(w, shell("private instance", `<h1>vbox standalone</h1><p>Use your private view, desktop, or chat link. Run <code>vbox standalone links INSTANCE</code> to obtain the links.</p>`))
		return
	}
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()

	caps, _ := json.Marshal(s.Config.Capabilities)
	body := `<header class="topbar workspace-top"><a class="brand" href="/" aria-label="vbox standalone workspace"><img class="brand-logo brand-logo-on-light" src="/vbox-logo.png" alt="vbox"><img class="brand-logo brand-logo-on-dark" src="/vbox-logo-dark.png" alt="vbox"><span class="brand-suffix">Standalone</span></a><span class="spacer"></span><nav aria-label="Instance controls"><a id="open-view" href="` + html.EscapeString("/view#"+s.Config.Capabilities["view"]) + `" target="_blank" rel="noopener">Live view ↗</a><a href="/password">Change password</a><button id="shutdown" class="linkbtn danger" type="button">Shut down</button></nav><span id="instance-state" class="muted" role="status">` + html.EscapeString(state) + `</span></header>`
	if s.Config.HubManaged {
		body = strings.Replace(body, `<a href="/password">Change password</a>`, `<a href="__hub_root__">Hub</a>`, 1)
	}
	chat := workspaceContent("chat")
	if runtime, ok := s.Runtime.(*BoxRuntime); ok {
		name := map[string]string{"claude": "Claude", "codex": "Codex", "opencode": "OpenCode"}[runtime.agent()]
		if name != "" {
			chat = strings.Replace(chat, "Your agent", name, 1)
		}
	}
	body += `<main class="combined"><div class="conversation">` + chat + `</div><section class="stages" aria-label="Desktop and TMUX">` + workspaceContent("desktop") + workspaceContent("terminal") + `</section></main>`
	body += viewerAssets() + `<script>` + workspaceJS + `
 const capabilities=` + string(caps) + `;
 for(const scope of ['desktop','terminal','chat'])bootWorkspace(scope,document.querySelector('[data-scope="'+scope+'"]'),capabilities[scope==='terminal'?'desktop':scope]);
 document.querySelector('#shutdown').onclick=async()=>{
  const button=document.querySelector('#shutdown');button.disabled=true;
  try{const r=await fetch(standalonePath('/v1/standalone/shutdown'),{method:'POST',headers:{Authorization:'Bearer '+capabilities.desktop}});if(!r.ok)throw Error('Shutdown failed: '+r.status);document.querySelector('#instance-state').textContent='instance shutting down · files retained';button.textContent='shutting down'}catch(e){document.querySelector('#instance-state').textContent=e.message;button.disabled=false}
 };
 </script>`
	_, _ = io.WriteString(w, viewerShell("workspace", body, s.Config.BasePath))
}
