package standalone

import (
	"encoding/json"
	webassets "github.com/0xikarus/vmbox-service/internal/controller/web"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Layout adapters only: tokens, controls, transcript bubbles and composer come
// from the same embedded styles as the controller chat.
const reportCSS = `
body.standalone{height:auto;min-height:100dvh;overflow:auto;padding:0;margin:0;font-family:var(--vb-font)}
.standalone [hidden]{display:none!important}.standalone .muted,.standalone .hint,.standalone .sub{color:var(--vb-ink-soft)}
.standalone .topbar{display:flex;align-items:center;gap:16px;padding:12px 20px;border-bottom:1px solid var(--vb-line);flex-wrap:wrap}
.standalone .topbar nav{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.standalone .topbar nav a{font-size:13px;padding:7px 10px;border-radius:var(--r-control);color:var(--vb-ink-soft)}
.standalone .topbar nav a:hover{background:var(--vb-surface-2)}.standalone .topbar .muted{margin-left:auto;font-size:12px}
.standalone .brand-logo{height:24px}.standalone .spacer{flex:1}.standalone .linkbtn{margin:0;font-size:13px}
.standalone h1{font-size:18px;margin:0}.standalone h2{font-size:16px}.standalone p.sub{margin:4px 0 12px;font-size:13px}
.standalone>h1,.standalone>p,.standalone>form{max-width:480px;margin:24px auto;padding:0 20px}
.standalone .field{display:flex;flex-direction:column;gap:6px}.standalone .form-actions{display:flex;gap:10px;align-items:end;margin-top:14px}
.standalone .combined{display:grid;grid-template-columns:minmax(330px,1fr) minmax(0,1.3fr);min-height:calc(100dvh - 66px)}
.standalone .combined>.stages{grid-column:2;grid-row:1;background:var(--vb-bg-soft);padding:16px;min-width:0;border-left:1px solid var(--vb-line)}
.standalone .combined>.conversation{grid-column:1;grid-row:1;min-width:0;height:calc(100dvh - 66px);position:sticky;top:0}
.standalone #chat-conversation{display:flex;flex-direction:column;height:100%;min-width:0;min-height:0;position:relative;inset:auto;transform:none;visibility:visible;overflow:hidden;background:var(--vb-bg)}
.standalone #chat-header{display:flex;align-items:center;gap:12px;padding:14px 20px;min-height:66px;flex:none}
.standalone .chat-header-identity{display:flex;align-items:center;gap:10px;min-width:0}.standalone #chat-header-text{display:flex;flex-direction:column;min-width:0}
.standalone #chat-header-state{font-size:12px}.standalone .standalone-avatar{display:grid;place-items:center;width:36px;height:36px;background:var(--vb-accent-soft);color:var(--vb-accent-text);border-radius:12px;font-weight:700}
.standalone #chat-messages{min-height:180px;scroll-behavior:auto}.standalone .chat-empty{margin:auto;text-align:center;max-width:280px;color:var(--vb-ink-soft);padding:24px}
.standalone .chat-empty strong{display:block;color:var(--vb-ink);font-size:18px;margin-bottom:6px}
.standalone #chat-composer{flex:none;width:calc(100% - 32px);max-width:52rem;margin:4px auto 16px}.standalone #chat-input{min-width:0;max-height:160px}.standalone #send svg{width:20px;height:20px}
.standalone [data-role="thinking-controls"]{display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:8px 18px;font-size:12px}.standalone [data-role="thinking"]{padding:5px 8px;max-width:180px}
.standalone .viewer{min-width:0;max-width:100%}.standalone .stages .viewer{background:var(--vb-surface);border:1px solid var(--vb-line);border-radius:var(--r-surface);overflow:hidden}
.standalone .stages .viewer+.viewer{margin-top:16px}.standalone .viewer-heading{display:flex;align-items:center;gap:12px;padding:10px 14px}.standalone .viewer-heading h1{font-size:13px;font-weight:650}.standalone .viewer-heading [role="status"]{margin:0 0 0 auto;font-size:11px;color:var(--vb-ink-soft)}
.standalone .viewer>.sub{display:none}.standalone [data-role="desktop-screen"],.standalone [data-role="terminal-screen"]{width:100%;height:calc(100dvh - 150px);min-height:160px;overflow:hidden;background:#000}
.standalone .combined [data-role="desktop-screen"]{height:auto;aspect-ratio:16/10;min-height:0}.standalone .combined [data-role="terminal-screen"]{height:340px;min-height:180px}
.standalone [data-role="desktop-screen"] img{display:block;max-width:100%;max-height:100%;object-fit:contain;margin:auto}
.standalone [data-role="desktop-controls"],.standalone .terminal-keys{display:flex;align-items:center;gap:6px;flex-wrap:wrap;padding:8px}.standalone [data-role="desktop-controls"]:empty{display:none}
.standalone [data-role="desktop-controls"] button,.standalone .terminal-keys button{font-size:12px;padding:5px 8px}.standalone [data-role="desktop-controls"] textarea{max-width:180px;min-height:30px;resize:vertical}
.standalone .terminal-keys svg{width:16px;height:16px}.standalone .sr-only{position:absolute;clip:rect(0,0,0,0);width:1px;height:1px;overflow:hidden}
.standalone .msg figure{margin:8px 0}.standalone .msg img.chat-image{max-width:100%;max-height:360px;object-fit:contain;cursor:zoom-in}.standalone .msg figcaption{font-size:11px;opacity:.7}
.standalone .msg .text{overflow-wrap:anywhere}.standalone .msg .meta{display:flex;gap:5px}.standalone .image-preview{border:1px solid var(--vb-line);border-radius:var(--r-surface);background:var(--vb-bg);color:var(--vb-ink);max-width:95vw;max-height:95dvh;padding:12px}.standalone .image-preview::backdrop{background:rgba(0,0,0,.7)}.standalone .image-preview img{display:block;max-width:90vw;max-height:80dvh}.standalone .image-preview button{float:right;margin-bottom:8px}
.standalone>.chat{height:100dvh}.standalone footer{font-size:12px;color:var(--vb-ink-soft);padding:12px 20px}
@media(max-width:850px){.standalone .combined{display:flex;flex-direction:column}.standalone .combined>.stages{border-left:0;border-top:1px solid var(--vb-line);padding:10px}.standalone .combined>.conversation{position:relative;height:70dvh;min-height:480px;order:-1}.standalone .topbar{padding:10px;gap:8px}.standalone .topbar .muted{display:none}.standalone .topbar .brand-suffix{display:none}.standalone .msg{max-width:88%}}
`

func shell(title, body string, bases ...string) string {
	base := ""
	if len(bases) > 0 {
		base = bases[0]
	}
	prefix, _ := json.Marshal(base)
	body = strings.ReplaceAll(body, `src="/`, `src="`+base+`/`)
	body = strings.ReplaceAll(body, `href="/`, `href="`+base+`/`)
	body = strings.ReplaceAll(body, `href="__hub_root__"`, `href="/"`)
	var css strings.Builder
	for _, name := range []string{"vbox-tokens.css", "fonts.css", "chat.css", "vbox-c.css"} {
		b, _ := webassets.ChatStyle.ReadFile(name)
		css.Write(b)
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + ` · vbox</title><script>window.VBOX_BASE_PATH=` + string(prefix) + `;function standalonePath(path){return window.VBOX_BASE_PATH+path}</script><style>` + strings.ReplaceAll(css.String(), `url('/`, `url('`+base+`/`) + reportCSS + `</style></head><body class="standalone">` + body + `</body></html>`
}
func workspaceContent(scope string) string {
	if scope == "chat" {
		return `<section id="chat-conversation" data-scope="chat" class="chat">
<header id="chat-header"><div class="chat-header-identity"><span class="standalone-avatar" aria-hidden="true">v</span><span id="chat-header-text"><strong id="chat-header-name">Your agent</strong><span id="chat-header-state" data-role="status" role="status">Connecting…</span></span></div><span class="spacer"></span><span class="hint">Standalone</span></header>
<div id="chat-messages" data-role="messages" role="log" aria-label="Conversation" aria-live="polite"></div>
<div class="controls" data-role="thinking-controls" hidden><label for="chat-thinking">Thinking</label><select id="chat-thinking" data-role="thinking"><option value="default">Default</option></select><span data-role="thinking-status" role="status"></span></div>
<form id="chat-composer" data-role="compose" autocomplete="off" hidden><textarea id="chat-input" data-role="text" rows="1" required maxlength="100000" aria-label="Message" placeholder="Message your agent…"></textarea><button id="send" class="primary" type="submit" aria-label="Send message" title="Send message"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5"/><path d="m5 12 7-7 7 7"/></svg></button></form>
</section>`
	}
	title, description := scope, "Control the desktop of this instance."
	if scope == "view" {
		title = "live view"
		description = "Live screen · read only."
	}
	if scope == "terminal" {
		title = "tmux"
		description = "The existing agent session · closing this view detaches only."
	}
	body := `<section data-scope="` + html.EscapeString(scope) + `" class="viewer ` + html.EscapeString(scope) + `"><div class="viewer-heading"><h1>` + title + `</h1><p id="status" role="status" class="hint">Connecting…</p></div><p class="sub">` + description + `</p>`
	if scope == "terminal" {
		body += `<div id="terminal-screen"></div><div class="terminal-keys controls"></div>`
	} else {
		body += `<div id="desktop-screen"></div><div id="desktop-controls"></div>`
	}

	body += "</section>"
	// Roles are local to each component; the combined page has no duplicate IDs.
	body = regexp.MustCompile(`id="([a-z-]+)"`).ReplaceAllStringFunc(body, func(match string) string {
		role := strings.TrimSuffix(strings.TrimPrefix(match, `id="`), `"`)
		return `data-role="` + role + `" id="` + scope + `-` + role + `"`
	})
	body = strings.ReplaceAll(body, `for="`, `for="`+scope+`-`)
	return body
}
func viewerAssets() string {
	return `<script src="/novnc.js"></script><script src="/workspace-desktop.js"></script><script src="/xterm.js"></script><script src="/xterm-fit.js"></script><script src="/workspace-terminal.js"></script>`
}
func viewerShell(title, body string, bases ...string) string {
	out := shell(title, body, bases...)
	css, _ := webassets.Desktop.ReadFile("xterm.css")
	return strings.Replace(out, "</style>", string(css)+"</style>", 1)
}
func workspacePage(scope string, bases ...string) string {
	title := scope
	if scope == "terminal" {
		title = "tmux"
	}
	if scope == "view" {
		title = "live view"
	}
	return viewerShell(title, workspaceContent(scope)+viewerAssets()+`<script>`+workspaceJS+`
 const scope=`+strconv.Quote(scope)+`,storageKey='vbox-standalone-token:'+window.VBOX_BASE_PATH+':'+scope,token=location.hash.slice(1)||sessionStorage.getItem(storageKey)||'';
 if(token)sessionStorage.setItem(storageKey,token);history.replaceState(null,'',location.pathname);
 bootWorkspace(scope,document.querySelector('[data-scope]'),token);
 </script>`, bases...)
}
func (s *Server) messageFragment(w http.ResponseWriter, r *http.Request) {
	messages, err := s.Store.List()
	if err != nil {
		http.Error(w, "storage unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	if len(messages) == 0 {
		b.WriteString(`<div class="chat-empty"><strong>What shall we work on?</strong>Send a message to your agent. Its replies and images appear here.</div>`)
	}
	for _, m := range messages {
		stamp := "—"
		if t, e := time.Parse(time.RFC3339Nano, m.Created); e == nil {
			stamp = t.UTC().Format("2006-01-02 15:04") + " UTC"
		}
		direction := "agent"
		if m.Direction == "user" {
			direction = "user"
		}
		b.WriteString(`<article class="msg ` + direction + `" data-message-id="` + html.EscapeString(m.ID) + `"><div class="text">` + html.EscapeString(m.Text) + `</div>`)
		for _, img := range m.Images {
			b.WriteString(`<figure><img class="chat-image" data-image-src="` + html.EscapeString(img.URL) + `" alt="` + html.EscapeString(img.Name) + `"><figcaption>` + html.EscapeString(img.Name) + `</figcaption></figure>`)
		}
		b.WriteString(`<div class="meta"><span>` + html.EscapeString(m.State) + `</span><time>` + html.EscapeString(stamp) + `</time></div></article>`)
	}
	io.WriteString(w, b.String())
}

const workspaceJS = `
function bootWorkspace(scope,root,token){
 const storageKey='vbox-standalone-token:'+window.VBOX_BASE_PATH+':'+scope;
 if(token)sessionStorage.setItem(storageKey,token);
 const headers={Authorization:'Bearer '+token,'Content-Type':'application/json'};
 const role=name=>root.querySelector('[data-role="'+name+'"]');
 const status=role('status');
 async function api(path,options={}){
  const r=await fetch(standalonePath('/v1/standalone/'+path),{...options,headers:{...headers,...options.headers}});
  if(!r.ok)throw Error('Request failed: '+r.status);
  return r.json();
 }
 async function waitReady(){while((await api('status')).state!=='ready'){status.textContent='Waiting for agent…';await new Promise(r=>setTimeout(r,1000))}}
 if(scope==='view'){
  const img=document.createElement('img');img.alt='Live desktop, read only';role('desktop-screen').append(img);let previous;
  async function frame(){
   try{const r=await fetch(standalonePath('/v1/standalone/frame'),{headers});if(!r.ok)throw Error('Frame unavailable');const next=URL.createObjectURL(await r.blob());img.src=next;if(previous)URL.revokeObjectURL(previous);previous=next;status.textContent='Live view · read only'}catch(e){status.textContent=e.message}
   setTimeout(frame,500);
  }
  frame();
 }else if(scope==='chat'){
  const composer=role('compose'),input=role('text'),box=role('messages'),send=composer.querySelector('button[type="submit"]');
  composer.hidden=false;
  api('thinking').then(t=>{
   if(!t.supported)return;
   role('thinking-controls').hidden=false;
   const field=role('thinking');
   field.replaceChildren(...[{value:'default',label:'Default'+(t.default?' ('+t.default+')':'')},...t.efforts.map(v=>({value:v,label:v}))].map(v=>{const o=document.createElement('option');o.value=v.value;o.textContent=v.label;return o}));
   field.value=t.mode;
   field.onchange=async()=>{
    field.disabled=true;
    try{await api('thinking',{method:'POST',body:JSON.stringify({mode:field.value})});role('thinking-status').textContent='Saved · next message'}catch(e){role('thinking-status').textContent=e.message}finally{field.disabled=false}
   };
  }).catch(()=>{});
  let key=null,submittedText=null,sending=false;
  const imageURLs=new Set();
  const nearBottom=()=>box.scrollHeight-box.scrollTop-box.clientHeight<80;
  function bottom(){box.scrollTop=box.scrollHeight}
  const preview=document.createElement('dialog');preview.className='image-preview';preview.setAttribute('aria-label','Image preview');preview.innerHTML='<button type="button" aria-label="Close image preview">Close</button><img alt="">';root.append(preview);
  preview.querySelector('button').onclick=()=>preview.close();
  preview.addEventListener('click',e=>{if(e.target===preview)preview.close()});
  box.addEventListener('click',e=>{if(e.target.matches('img.chat-image[src]')){const image=preview.querySelector('img');image.src=e.target.src;image.alt=e.target.alt;preview.showModal()}});
  async function loadImages(scroll){
   for(const img of box.querySelectorAll('img[data-image-src]')){
    try{
     const r=await fetch(standalonePath(img.dataset.imageSrc),{headers});if(!r.ok)throw Error();
     const url=URL.createObjectURL(await r.blob());
     if(!img.isConnected){URL.revokeObjectURL(url);continue}
     imageURLs.add(url);img.onload=()=>{if(scroll)bottom()};img.src=url;
    }catch{img.alt='Image unavailable: '+img.alt}
   }
  }
  let refreshing=false;
  async function refresh(forceBottom=false){
   if(refreshing)return;refreshing=true;
   try{
    const r=await fetch(standalonePath('/v1/standalone/messages/html'),{headers});if(!r.ok)throw Error('Request failed: '+r.status);
    const content=await r.text();
    if(box.dataset.content!==content){
     const scroll=forceBottom||nearBottom();
     preview.close();for(const url of imageURLs)URL.revokeObjectURL(url);imageURLs.clear();
     box.innerHTML=content;box.dataset.content=content;
     if(scroll)bottom();loadImages(scroll);
    }
    status.textContent=(await api('status')).state;
   }catch(e){status.textContent=e.message}finally{refreshing=false}
  }
  function resize(){input.style.height='auto';input.style.height=Math.min(input.scrollHeight,160)+'px';send.disabled=sending||!input.value.trim()}
  input.addEventListener('input',resize);
  input.addEventListener('keydown',e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing){e.preventDefault();if(!send.disabled)composer.requestSubmit()}});
  composer.onsubmit=async e=>{
   e.preventDefault();if(sending||!input.value.trim())return;
   const text=input.value;
   if(submittedText!==text){key=null;submittedText=text}
   key=key||Array.from(crypto.getRandomValues(new Uint8Array(16)),b=>b.toString(16).padStart(2,'0')).join('');
   sending=true;resize();
   try{
    await api('messages',{method:'POST',headers:{'Idempotency-Key':key},body:JSON.stringify({text})});
    if(input.value===text)input.value='';key=null;submittedText=null;refresh(true);
   }catch(e){status.textContent=e.message}finally{sending=false;resize();input.focus()}
  };
  resize();refresh();setInterval(()=>refresh(),2000);
 }else if(scope==='terminal'){
  waitReady().then(()=>api('stream-ticket?kind=terminal',{method:'POST'})).then(t=>openWorkspaceTerminal('','',s=>status.textContent=s.toLowerCase(),{root:role('terminal-screen'),keys:root.querySelector('.terminal-keys'),streamURL:standalonePath('/v1/standalone/terminal/ws?ticket='+encodeURIComponent(t.ticket))})).catch(e=>status.textContent=e.message);
 }else{
  waitReady().then(()=>api('stream-ticket',{method:'POST'})).then(t=>openWorkspaceDesktop('',s=>status.textContent=s.toLowerCase(),{root:role('desktop-screen'),controls:role('desktop-controls'),streamURL:standalonePath('/v1/standalone/desktop/ws?ticket='+encodeURIComponent(t.ticket))})).catch(e=>status.textContent=e.message);
 }
}
`
