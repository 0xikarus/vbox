package hub

import (
	"encoding/json"
	"html"
	"io"
	"net/http"

	"github.com/0xikarus/vmbox-service/internal/standalone"
)

// Visiting a box never starts it. The status page remains useful before its
// worker is ready and exposes an explicit owner action for stopped boxes.
func (s *Server) pendingWorkspace(w http.ResponseWriter, r *http.Request, b Box) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	id, _ := json.Marshal(b.ID)
	body := `<main class="pending-workspace" style="max-width:640px;margin:10vh auto;padding:24px"><a href="/">← Hub</a><h1>` + html.EscapeString(b.Spec.Name) + `</h1><p id="box-state" role="status">` + html.EscapeString(b.State) + `</p><p id="box-reason">` + html.EscapeString(b.Reason) + `</p><button id="start-box" class="primary" hidden>Start box</button><p>Chat opens here when the box is ready. Starting a stopped box is always an explicit action.</p></main><script>
const id=` + string(id) + `,button=document.querySelector('#start-box'),state=document.querySelector('#box-state'),reason=document.querySelector('#box-reason');
async function refresh(){try{const response=await fetch('/api/boxes/'+id);if(response.status===401){location.href='/';return}if(!response.ok)throw Error('Could not load box');const box=await response.json();state.textContent=box.state;reason.textContent=box.reason||'';button.hidden=box.desired!=='stopped';if(box.state==='running'){location.replace('/boxes/'+id+'/');return}}catch(error){reason.textContent=error.message}setTimeout(refresh,1500)}
button.onclick=async()=>{button.disabled=true;try{const response=await fetch('/api/boxes/'+id+'/start',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});if(!response.ok)throw Error(await response.text());state.textContent='Queued';button.hidden=true}catch(error){reason.textContent=error.message}finally{button.disabled=false}};refresh();
</script>`
	_, _ = io.WriteString(w, standalone.Page(b.Spec.Name, body))
}
