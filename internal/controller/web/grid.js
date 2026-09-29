'use strict';
(()=>{
 const $=s=>document.querySelector(s),tiles=[];let boxes=[],epoch=0,timer,automaticLayout=true;const unavailable=new Map();
 const workspaceNav=window.VMBoxWorkspaceNav?.init({menuId:'grid-menu',panelId:'grid-menu-panel',usageId:'grid-usage'});
 const node=(tag,text)=>{const e=document.createElement(tag);if(text)e.textContent=text;return e};
 function connectionBadge(label,title){const badge=node('span');badge.className='connection-badge';badge.title=title;badge.dataset.viewer=label;badge.dataset.state='idle';badge.dataset.ping='';updateConnectionBadge(badge,{state:'idle'});return badge}
 function updateConnectionBadge(badge,metrics){
  if(metrics.state)badge.dataset.state=metrics.state;
  if(Object.hasOwn(metrics,'ping'))badge.dataset.ping=Number.isFinite(metrics.ping)?String(Math.max(0,Math.round(metrics.ping))):'';
  const state=badge.dataset.state,ping=badge.dataset.ping;
  badge.textContent=state==='connected'?'Live · '+(ping!==''?ping:'—')+' ms':state==='disconnected'?'Disconnected':state==='connecting'?'Connecting':'Idle';
  badge.setAttribute('aria-label',badge.dataset.viewer+' '+badge.textContent);
 }
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
 function showLogin(message=''){$('#login').hidden=false;$('#login-error').textContent=message;$('#login-token').focus()}
 async function api(path,method='GET',headers={},body){const r=await fetch(path,{method,credentials:'same-origin',headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});if(r.status===401){stop();$('#grid-app').hidden=true;showLogin('Please log in again.');throw Error('Please log in again.')}if(!r.ok)throw Error('Controller request failed ('+r.status+'). Use Refresh or reconnect.');return r.status===204?null:r.json();}
 function stop(){epoch++;clearTimeout(timer);for(const t of tiles){t.disconnect();t.box.value='';}}
 function fill(){if($('#grid-app').hidden)return;const used=new Set(tiles.map(t=>t.box.value));for(const t of tiles){if(t.box.value)continue;const b=boxes.find(b=>b.state==='running'&&!used.has(b.id)&&(unavailable.get(b.id)||0)<=Date.now());if(b){t.box.value=b.id;used.add(b.id);void t.connect()}}tiles.forEach(picker)}
 function picker(t){const selected=t.box.value;t.box.replaceChildren(new Option('Select a box…',''));for(const b of boxes){const o=new Option(b.name+' · '+b.state,b.id);o.disabled=tiles.some(x=>x!==t&&x.box.value===b.id);t.box.add(o)}t.box.value=selected;syncActions(t);}
 function makeTile(){
  const element=node('section');element.className='tile';const header=node('header'),label=node('label','Box '),box=node('select');label.append(box);header.append(label);
  const sessionLabel=node('label','Session '),session=node('select');sessionLabel.append(session);header.append(sessionLabel);
  const reconnect=node('button','Reconnect'),close=node('button','Next box');header.append(reconnect,close);
  const status=node('p','Select a running box.');status.setAttribute('role','status');
  const controlsBar=node('div');controlsBar.className='tile-controls';
  const desktopPanel=node('section');desktopPanel.className='viewer-panel desktop-panel';desktopPanel.hidden=true;
  const desktopStatus=node('div','Desktop · Checking connection…');desktopStatus.className='viewer-overlay';desktopStatus.setAttribute('role','status');
  const desktopBadge=connectionBadge('Desktop','Round trip over the live VNC desktop connection.');
  const desktopScreen=node('div');desktopScreen.className='screen desktop-screen';
  const desktopControls=node('details');desktopControls.className='desktop-control-menu';desktopControls.hidden=true;desktopControls.append(node('summary','Desktop controls'));const desktopKeys=node('div');desktopKeys.className='desktop-controls';desktopControls.append(desktopKeys);desktopPanel.append(desktopScreen,desktopStatus,desktopBadge);
  const terminalPanel=node('section');terminalPanel.className='viewer-panel terminal-panel';
  const terminalStatus=node('div','TMUX · Select a running box.');terminalStatus.className='viewer-overlay';terminalStatus.setAttribute('role','status');
  const terminalBadge=connectionBadge('TMUX','Round trip over the live TMUX WebSocket connection.');
  const terminalControls=node('details');terminalControls.className='terminal-control-menu';terminalControls.append(node('summary','TMUX keyboard / controls'));const keys=node('div');keys.className='terminal-keys';terminalControls.append(keys);
  const terminalScreen=node('div');terminalScreen.className='screen terminal-screen';terminalPanel.append(terminalScreen,terminalStatus,terminalBadge);
  controlsBar.append(desktopControls,terminalControls);element.append(controlsBar,header,status,desktopPanel,terminalPanel);$('#tiles').append(element);
  let version=0,terminalVersion=0,disposeDesktop=()=>{},disposeTerminal=()=>{};
  function dropped(){unavailable.set(box.value,Date.now()+30000);t.disconnect();box.value='';session.replaceChildren();status.textContent='Waiting for an available box…';fill()}
  function maybeDrop(ticket){if(ticket===version&&!t.desktopPending&&!t.terminalPending&&!t.desktopActive&&!t.terminalActive)dropped()}
  async function connectDesktop(ticket,b){
   t.desktopPending=true;let enabled=false;
   try{enabled=(await api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/desktop')).enabled}catch{enabled=true}
   if(ticket!==version)return;
   if(!enabled){desktopPanel.hidden=true;desktopControls.hidden=true;t.desktopPending=false;maybeDrop(ticket);return}
   desktopPanel.hidden=false;desktopControls.hidden=false;desktopStatus.hidden=false;desktopStatus.textContent='Desktop · Starting…';updateConnectionBadge(desktopBadge,{state:'connecting',ping:null});
   try{
    await api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/desktop','POST');if(ticket!==version)return;
    disposeDesktop=openWorkspaceDesktop(b.id,message=>{if(ticket===version){desktopStatus.textContent=message;status.textContent=b.name+' · '+message}},{root:desktopScreen,controls:desktopKeys,onMetrics:metrics=>{if(ticket!==version)return;updateConnectionBadge(desktopBadge,metrics);if(metrics.state==='connected'){t.desktopActive=true;t.desktopPending=false;desktopStatus.hidden=true}else if(metrics.state==='disconnected'){t.desktopActive=false;t.desktopPending=false;desktopStatus.hidden=false;maybeDrop(ticket)}},onDisconnect:()=>{if(ticket===version){t.desktopActive=false;t.desktopPending=false;desktopStatus.hidden=false;updateConnectionBadge(desktopBadge,{state:'disconnected',ping:null});maybeDrop(ticket)}}});
   }catch(e){if(ticket===version){desktopStatus.textContent=e.message;desktopStatus.hidden=false;updateConnectionBadge(desktopBadge,{state:'disconnected',ping:null});t.desktopPending=false;maybeDrop(ticket)}}
  }
  async function connectTerminal(ticket,b){
   t.terminalPending=true;terminalStatus.hidden=false;terminalStatus.textContent='TMUX · Checking sessions…';updateConnectionBadge(terminalBadge,{state:'connecting',ping:null});
   try{
    const [inv,primary]=await Promise.all([api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/sessions'),api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/sessions/primary')]);if(ticket!==version)return;
    const sessions=(inv.sessions||[]).filter(s=>!s.name.startsWith('task-'));if(inv.partial||inv.state!=='live')throw Error('Session inventory is incomplete; reconnect.');
    for(const s of sessions)session.add(new Option(s.name,s.name));if(sessions.some(s=>s.name===primary.session))session.value=primary.session;
    if(!sessions.length){const created=await api('/v1/logical-boxes/'+encodeURIComponent(b.id)+'/sessions/interactive','POST',{'Content-Type':'application/json'},{agent:'shell',reuseShell:true});if(ticket!==version)return;session.add(new Option(created.session,created.session))}
    t.attach();
   }catch(e){if(ticket===version){terminalStatus.textContent=e.message;terminalStatus.hidden=false;updateConnectionBadge(terminalBadge,{state:'disconnected',ping:null});t.terminalPending=false;maybeDrop(ticket)}}
  }
  const t={element,box,session,status,reconnect,desktopPending:false,terminalPending:false,desktopActive:false,terminalActive:false,disconnect(){version++;terminalVersion++;disposeDesktop();disposeTerminal();disposeDesktop=()=>{};disposeTerminal=()=>{};desktopScreen.replaceChildren();terminalScreen.replaceChildren();desktopKeys.replaceChildren();keys.replaceChildren();desktopPanel.hidden=true;desktopControls.hidden=true;desktopStatus.textContent='Desktop · Checking connection…';desktopStatus.hidden=false;terminalStatus.textContent='TMUX · Select a running box.';terminalStatus.hidden=false;updateConnectionBadge(desktopBadge,{state:'idle',ping:null});updateConnectionBadge(terminalBadge,{state:'idle',ping:null});t.desktopPending=false;t.terminalPending=false;t.desktopActive=false;t.terminalActive=false;},async connect(){
   t.disconnect();const ticket=version,selected=box.value;session.replaceChildren();
   if(!selected){status.textContent='Select a running box.';return}
   const b=boxes.find(b=>b.id===selected);syncActions(t);if(!b||b.state!=='running'){status.textContent=(b?.state||'Unavailable')+(b?.failureReason?' · '+b.failureReason:'')+' · '+(boxPhase(b?.state)==='creating'?'being created; this tile updates automatically':'resume to attach it here');return}
   status.textContent=b.name+' · '+b.state;t.desktopPending=true;t.terminalPending=true;
   await Promise.allSettled([connectDesktop(ticket,b),connectTerminal(ticket,b)]);
  },attach(){const ticket=version,b=boxes.find(b=>b.id===box.value);terminalVersion++;const sessionTicket=terminalVersion;disposeTerminal();disposeTerminal=()=>{};terminalScreen.replaceChildren();keys.replaceChildren();t.terminalActive=false;terminalStatus.hidden=false;updateConnectionBadge(terminalBadge,{state:'connecting',ping:null});if(!b||!session.value){t.terminalPending=false;updateConnectionBadge(terminalBadge,{state:'idle',ping:null});return}t.terminalPending=true;terminalStatus.textContent='TMUX · Connecting to '+session.value+'…';
   try{disposeTerminal=openWorkspaceTerminal(b.id,session.value,message=>{if(ticket===version&&sessionTicket===terminalVersion){terminalStatus.textContent=message;status.textContent=b.name+' · '+message}},{root:terminalScreen,keys,autoFocus:false,onMetrics:metrics=>{if(ticket!==version||sessionTicket!==terminalVersion)return;updateConnectionBadge(terminalBadge,metrics);if(metrics.state==='connected'){t.terminalActive=true;t.terminalPending=false;terminalStatus.hidden=true}else if(metrics.state==='disconnected'){t.terminalActive=false;t.terminalPending=false;terminalStatus.hidden=false;maybeDrop(ticket)}},onDisconnect:()=>{if(ticket===version&&sessionTicket===terminalVersion){t.terminalActive=false;t.terminalPending=false;terminalStatus.hidden=false;updateConnectionBadge(terminalBadge,{state:'disconnected',ping:null});maybeDrop(ticket)}}})}catch(e){terminalStatus.textContent=e.message;terminalStatus.hidden=false;updateConnectionBadge(terminalBadge,{state:'disconnected',ping:null});t.terminalPending=false;maybeDrop(ticket)}
  }};
  box.onchange=()=>{t.connect();tiles.forEach(picker)};session.onchange=()=>t.attach();reconnect.onclick=()=>{const b=boxes.find(x=>x.id===box.value),phase=b?boxPhase(b.state):'';if(phase==='running')t.connect();else if(phase==='stopped'||phase==='failed')void resumeTile(t)};close.onclick=dropped;tiles.push(t);picker(t);
 }
 function layout(){const form=$('#layout'),columns=Number(form.elements.columns.value),rows=automaticLayout?Math.max(1,Math.ceil(boxes.filter(b=>b.state==='running').length/columns)):Number(form.elements.rows.value);while(tiles.length>columns*rows){const t=tiles.pop();t.disconnect();t.element.remove()}while(tiles.length<columns*rows)makeTile();$('#tiles').style.setProperty('--columns',columns);$('#tiles').style.setProperty('--rows',rows);tiles.forEach(picker);fill();}
 async function refresh(){clearTimeout(timer);const ticket=++epoch;try{const current=await api('/v1/grid-boxes');if(ticket!==epoch)return;boxes=current;$('#grid-count').textContent=boxes.filter(box=>box.state==='running').length+' running · '+boxes.length+' total';$('#error').textContent='';for(const t of tiles){const selected=t.box.value,b=boxes.find(b=>b.id===selected);if(selected&&(!b||b.state!=='running')){t.disconnect();t.box.value='';t.session.replaceChildren();t.status.textContent=b?b.name+' · '+b.state+' · not connected.':'Box removed or no longer interactive.';}picker(t)}if(automaticLayout)layout();else fill()}catch(e){if(ticket===epoch)$('#error').textContent=e.message}finally{if(ticket===epoch&&!$('#grid-app').hidden)timer=setTimeout(refresh,15000)}}
 async function enter(initial=false){try{const who=await api('/v1/whoami');workspaceNav?.setOwner(who.role==='owner');$('#login').hidden=true;$('#login-error').textContent='';$('#error').textContent='';$('#logout').hidden=false;$('#grid-app').hidden=false;if(!tiles.length)layout();await refresh()}catch(e){workspaceNav?.setOwner(false);showLogin(initial&&e.message==='Please log in again.'?'':e.message)}}
 $('#layout').onsubmit=e=>{e.preventDefault();automaticLayout=$('#layout').elements.rows.value==='auto';layout()};$('#refresh').onclick=refresh;
 $('#login').onsubmit=async e=>{e.preventDefault();$('#login-error').textContent='';try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();await enter()}catch(err){showLogin(err.message)}};
 $('#logout').onclick=async()=>{workspaceNav?.closeMenu();stop();try{await api('/v1/browser-session','DELETE');workspaceNav?.setOwner(false);$('#grid-app').hidden=true;$('#logout').hidden=true;showLogin()}catch(e){$('#error').textContent=e.message}};
 window.addEventListener('pagehide',stop);enter(true);
})();
