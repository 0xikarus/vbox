'use strict';
(()=>{
 const $=s=>document.querySelector(s),tiles=[];let boxes=[],epoch=0,timer,automaticLayout=true;const unavailable=new Map();
 const node=(tag,text)=>{const e=document.createElement(tag);if(text)e.textContent=text;return e};
 // A tile only offers an action its box state can satisfy.
 const boxPhase=state=>state==='running'?'running':state==='failed'?'failed':(state==='reserved'||state==='attaching')?'creating':state==='deleting'?'deleting':(state==='hibernating'||state==='draining')?'transitioning':'stopped';
 function syncActions(t){const b=boxes.find(x=>x.id===t.box.value),phase=b?boxPhase(b.state):'';const available=phase==='running'||phase==='stopped'||phase==='failed';t.reconnect.hidden=!available;t.reconnect.disabled=!available;t.reconnect.textContent=phase==='running'?'Reconnect':'Resume'}
 async function resumeTile(t){
  const b=boxes.find(x=>x.id===t.box.value);if(!b)return;
  const phase=boxPhase(b.state);if(phase!=='stopped'&&phase!=='failed')return;
  t.status.textContent=b.name+' · requesting compute…';
  try{
   let a=await api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/allocate','POST',{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},{leaseOwner:'web'});
   const deadline=Date.now()+180000;
   while(a.state!=='ready'){
    if(['failed','cancelled'].includes(a.state))throw Error(a.failureReason||a.state);
    if(Date.now()>deadline)throw Error('Allocation still running; refresh to check again.');
    t.status.textContent=b.name+' · '+[a.phase,a.state,a.queuePosition?'queue '+a.queuePosition:''].filter(Boolean).join(' · ');
    await new Promise(r=>setTimeout(r,2000));
    a=await api('/v1/allocations/'+encodeURIComponent(a.requestId));
   }
   await refresh();await t.connect();
  }catch(e){t.status.textContent=b.name+' · '+e.message}
 }
 async function api(path,method='GET',headers={},body){const r=await fetch(path,{method,credentials:'same-origin',headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});if(r.status===401){stop();$('#grid-app').hidden=true;$('#login').hidden=false;throw Error('Please log in again.')}if(!r.ok)throw Error('Controller request failed ('+r.status+'). Use Refresh or reconnect.');return r.status===204?null:r.json();}
 function stop(){epoch++;clearTimeout(timer);for(const t of tiles){t.disconnect();t.box.value='';}}
 function fill(){if($('#grid-app').hidden)return;const used=new Set(tiles.map(t=>t.box.value));for(const t of tiles){if(t.box.value)continue;const b=boxes.find(b=>b.state==='running'&&!used.has(b.id)&&(unavailable.get(b.id)||0)<=Date.now());if(b){t.box.value=b.id;used.add(b.id);void t.connect()}}tiles.forEach(picker)}
 function picker(t){const selected=t.box.value;t.box.replaceChildren(new Option('Select a box…',''));for(const b of boxes){const o=new Option(b.name+' · '+b.state,b.id);o.disabled=tiles.some(x=>x!==t&&x.box.value===b.id);t.box.add(o)}t.box.value=selected;syncActions(t);}
 function makeTile(){
  const element=node('section');element.className='tile';const header=node('header'),label=node('label','Box '),box=node('select');label.append(box);header.append(label);
  const sessionLabel=node('label','Session '),session=node('select');sessionLabel.append(session);header.append(sessionLabel);
  const view=node('select');view.setAttribute('aria-label','Viewer');view.add(new Option('Desktop','desktop'));view.add(new Option('TMUX','terminal'));header.append(view);const reconnect=node('button','Reconnect'),close=node('button','Next box');header.append(reconnect,close);
  const status=node('p','Select a running box.');status.setAttribute('role','status');
  const controls=node('details');controls.append(node('summary','Keys / fullscreen'));const keys=node('div');keys.className='terminal-keys';controls.append(keys);
  const screen=node('div');screen.className='screen';element.append(header,status,controls,screen);$('#tiles').append(element);
  let version=0,dispose=()=>{};
  function dropped(){unavailable.set(box.value,Date.now()+30000);t.disconnect();box.value='';session.replaceChildren();status.textContent='Waiting for an available box…';fill()}
  async function desktop(ticket,b){await api('/v1/logical-boxes/'+b.id+'/desktop','POST');if(ticket!==version)return;dispose=openWorkspaceDesktop(b.id,message=>{if(ticket===version)status.textContent=b.name+' · '+message},{root:screen,controls:keys,onDisconnect:()=>{if(ticket===version)dropped()}})}
  const t={element,box,session,status,reconnect,disconnect(){version++;dispose();dispose=()=>{};screen.replaceChildren();keys.replaceChildren();},async connect(preferred='auto'){
   t.disconnect();const ticket=version,selected=box.value;session.replaceChildren();
   if(!selected){status.textContent='Select a running box.';return}
   const b=boxes.find(b=>b.id===selected);syncActions(t);if(!b||b.state!=='running'){status.textContent=(b?.state||'Unavailable')+(b?.failureReason?' · '+b.failureReason:'')+' · '+(boxPhase(b?.state)==='creating'?'being created; this tile updates automatically':'resume to attach it here');return}
   status.textContent=b.name+' · '+b.state+' · checking sessions…';
   try{if(preferred!=='terminal'){let enabled=false;try{enabled=(await api('/v1/logical-boxes/'+selected+'/desktop')).enabled}catch{enabled=true}if(ticket!==version)return;if(enabled||preferred==='desktop'){view.value='desktop';sessionLabel.hidden=true;try{await desktop(ticket,b);return}catch(e){if(ticket!==version)return;if(preferred==='desktop')throw e}}}view.value='terminal';sessionLabel.hidden=false;const [inv,primary]=await Promise.all([api('/v1/logical-boxes/'+selected+'/sessions'),api('/v1/logical-boxes/'+selected+'/sessions/primary')]);if(ticket!==version)return;
    const sessions=(inv.sessions||[]).filter(s=>!s.name.startsWith('task-'));if(inv.partial||inv.state!=='live')throw Error('Session inventory is incomplete; reconnect.');
    for(const s of sessions)session.add(new Option(s.name,s.name));if(sessions.some(s=>s.name===primary.session))session.value=primary.session;
    if(!sessions.length){const created=await api('/v1/logical-boxes/'+selected+'/sessions/interactive','POST',{'Content-Type':'application/json'},{agent:'shell',reuseShell:true});if(ticket!==version)return;session.add(new Option(created.session,created.session))}
    t.attach();
   }catch(e){if(ticket===version){status.textContent=e.message;dropped()}}
  },attach(){t.disconnect();const ticket=version,b=boxes.find(b=>b.id===box.value);if(!b||!session.value)return;status.textContent=b.name+' · connecting…';dispose=openWorkspaceTerminal(b.id,session.value,message=>{if(ticket===version)status.textContent=b.name+' · '+b.state+' · '+message},{root:screen,keys,autoFocus:false,onDisconnect:()=>{if(ticket===version)dropped()}});}};
  view.onchange=()=>t.connect(view.value);box.onchange=()=>{t.connect();tiles.forEach(picker)};session.onchange=()=>t.attach();reconnect.onclick=()=>{const b=boxes.find(x=>x.id===box.value),phase=b?boxPhase(b.state):'';if(phase==='running')t.connect();else if(phase==='stopped'||phase==='failed')void resumeTile(t)};close.onclick=dropped;tiles.push(t);picker(t);
 }
 function layout(){const form=$('#layout'),columns=Number(form.elements.columns.value),rows=automaticLayout?Math.max(1,Math.ceil(boxes.filter(b=>b.state==='running').length/columns)):Number(form.elements.rows.value);while(tiles.length>columns*rows){const t=tiles.pop();t.disconnect();t.element.remove()}while(tiles.length<columns*rows)makeTile();$('#tiles').style.setProperty('--columns',columns);$('#tiles').style.setProperty('--rows',rows);tiles.forEach(picker);fill();}
 async function refresh(){clearTimeout(timer);const ticket=++epoch;try{const current=await api('/v1/grid-boxes');if(ticket!==epoch)return;boxes=current;$('#error').textContent='';for(const t of tiles){const selected=t.box.value,b=boxes.find(b=>b.id===selected);if(selected&&(!b||b.state!=='running')){t.disconnect();t.box.value='';t.session.replaceChildren();t.status.textContent=b?b.name+' · '+b.state+' · not connected.':'Box removed or no longer interactive.';}picker(t)}if(automaticLayout)layout();else fill()}catch(e){if(ticket===epoch)$('#error').textContent=e.message}finally{if(ticket===epoch&&!$('#grid-app').hidden)timer=setTimeout(refresh,15000)}}
 async function enter(){try{await api('/v1/whoami');$('#login').hidden=true;$('#logout').hidden=false;$('#grid-app').hidden=false;if(!tiles.length)layout();await refresh()}catch(e){$('#error').textContent=e.message;$('#login').hidden=false}}
 $('#layout').onsubmit=e=>{e.preventDefault();automaticLayout=$('#layout').elements.rows.value==='auto';layout()};$('#refresh').onclick=refresh;
 $('#login').onsubmit=async e=>{e.preventDefault();try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();await enter()}catch(err){$('#error').textContent=err.message}};
 $('#logout').onclick=async()=>{stop();try{await api('/v1/browser-session','DELETE');$('#grid-app').hidden=true;$('#logout').hidden=true;$('#login').hidden=false}catch(e){$('#error').textContent=e.message}};
 window.addEventListener('pagehide',stop);enter();
})();
