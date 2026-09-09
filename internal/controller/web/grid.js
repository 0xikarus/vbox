'use strict';
(()=>{
 const $=s=>document.querySelector(s),tiles=[];let boxes=[],epoch=0,timer;
 const node=(tag,text)=>{const e=document.createElement(tag);if(text)e.textContent=text;return e};
 async function api(path,method='GET',headers={}){const r=await fetch(path,{method,credentials:'same-origin',headers,signal:AbortSignal.timeout(30000)});if(r.status===401){stop();$('#grid-app').hidden=true;$('#login').hidden=false;throw Error('Please log in again.')}if(!r.ok)throw Error('Controller request failed ('+r.status+'). Use Refresh or reconnect.');return r.status===204?null:r.json();}
 function stop(){epoch++;clearTimeout(timer);for(const t of tiles)t.disconnect();}
 function picker(t){const selected=t.box.value;t.box.replaceChildren(new Option('Select a box…',''));for(const b of boxes){const o=new Option(b.name+' · '+b.state,b.id);o.disabled=tiles.some(x=>x!==t&&x.box.value===b.id);t.box.add(o)}t.box.value=selected;}
 function makeTile(){
  const element=node('section');element.className='tile';const header=node('header'),label=node('label','Box '),box=node('select');label.append(box);header.append(label);
  const sessionLabel=node('label','Session '),session=node('select');sessionLabel.append(session);header.append(sessionLabel);
  const reconnect=node('button','Reconnect'),close=node('button','Clear');header.append(reconnect,close);
  const status=node('p','Select a running box.');status.setAttribute('role','status');
  const controls=node('details');controls.append(node('summary','Keys / fullscreen'));const keys=node('div');keys.className='terminal-keys';controls.append(keys);
  const screen=node('div');screen.className='screen';element.append(header,status,controls,screen);$('#tiles').append(element);
  let version=0,dispose=()=>{};
  const t={element,box,session,status,disconnect(){version++;dispose();dispose=()=>{};screen.replaceChildren();keys.replaceChildren();},async connect(){
   t.disconnect();const ticket=version,selected=box.value;session.replaceChildren();
   if(!selected){status.textContent='Select a running box.';return}
   const b=boxes.find(b=>b.id===selected);if(!b||b.state!=='running'){status.textContent=(b?.state||'Unavailable')+' · resume using vmbox BOX or the box workspace, then reconnect.';return}
   status.textContent=b.name+' · '+b.state+' · checking sessions…';
   try{const [inv,primary]=await Promise.all([api('/v1/logical-boxes/'+selected+'/sessions'),api('/v1/logical-boxes/'+selected+'/sessions/primary')]);if(ticket!==version)return;
    const sessions=(inv.sessions||[]).filter(s=>!s.name.startsWith('task-'));if(inv.partial||inv.state!=='live')throw Error('Session inventory is incomplete; reconnect.');
    for(const s of sessions)session.add(new Option(s.name,s.name));if(sessions.some(s=>s.name===primary.session))session.value=primary.session;
    if(!sessions.length){status.textContent=b.name+' · no interactive session. Open it with vmbox '+b.name+' first.';return}
    t.attach();
   }catch(e){if(ticket===version)status.textContent=e.message}
  },attach(){t.disconnect();const ticket=version,b=boxes.find(b=>b.id===box.value);if(!b||!session.value)return;status.textContent=b.name+' · connecting…';dispose=openWorkspaceTerminal(b.id,session.value,message=>{if(ticket===version)status.textContent=b.name+' · '+b.state+' · '+message},{root:screen,keys,autoFocus:false});}};
  box.onchange=()=>{t.connect();tiles.forEach(picker)};session.onchange=()=>t.attach();reconnect.onclick=()=>t.connect();close.onclick=()=>{box.value='';session.replaceChildren();t.disconnect();status.textContent='Select a running box.';tiles.forEach(picker)};tiles.push(t);picker(t);
 }
 function layout(){const form=$('#layout'),columns=Number(form.elements.columns.value),rows=Number(form.elements.rows.value);while(tiles.length>columns*rows){const t=tiles.pop();t.disconnect();t.element.remove()}while(tiles.length<columns*rows)makeTile();$('#tiles').style.setProperty('--columns',columns);$('#tiles').style.setProperty('--rows',rows);tiles.forEach(picker);}
 async function refresh(){clearTimeout(timer);const ticket=++epoch;try{const current=await api('/v1/grid-boxes');if(ticket!==epoch)return;boxes=current;$('#error').textContent='';for(const t of tiles){const selected=t.box.value,b=boxes.find(b=>b.id===selected);if(selected&&(!b||b.state!=='running')){t.disconnect();t.session.replaceChildren();t.status.textContent=b?b.name+' · '+b.state+' · not connected.':'Box removed or no longer interactive.';}picker(t)}}catch(e){if(ticket===epoch)$('#error').textContent=e.message}finally{if(ticket===epoch&&!$('#grid-app').hidden)timer=setTimeout(refresh,15000)}}
 async function enter(){try{await api('/v1/whoami');$('#login').hidden=true;$('#logout').hidden=false;$('#grid-app').hidden=false;if(!tiles.length)layout();await refresh()}catch(e){$('#error').textContent=e.message;$('#login').hidden=false}}
 $('#layout').onsubmit=e=>{e.preventDefault();layout()};$('#refresh').onclick=refresh;
 $('#login').onsubmit=async e=>{e.preventDefault();try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();await enter()}catch(err){$('#error').textContent=err.message}};
 $('#logout').onclick=async()=>{stop();try{await api('/v1/browser-session','DELETE');$('#grid-app').hidden=true;$('#logout').hidden=true;$('#login').hidden=false}catch(e){$('#error').textContent=e.message}};
 window.addEventListener('pagehide',stop);enter();
})();
