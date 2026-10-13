'use strict';
(()=>{
 const $=s=>document.querySelector(s),enc=encodeURIComponent,done=s=>['completed','failed','timed_out','abandoned','cancelled'].includes(s);
 const el=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n};
 let profiles=[],owner=false,cursor='',selected=location.hash.slice(1),detailVersion='',sequence=0,requestKey,requestBody,refreshing=false;
 const minted=new Map();
 function status(text){$('#status').textContent=text}
 async function api(path,method='GET',body,headers={}){
  const r=await fetch(path,{method,credentials:'same-origin',cache:'no-store',headers:{...(body===undefined?{}:{'Content-Type':'application/json'}),...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});
  if(r.status===401){$('#login').hidden=false;$('#tasks-app').hidden=true}
  if(!r.ok){const data=await r.json().catch(()=>({}));throw Error(data.error||'Request failed ('+r.status+')')}
  return r.status===204?null:r.json();
 }
 function action(text,fn){const b=el('button',text);b.type='button';b.onclick=async()=>{b.disabled=true;try{await fn()}catch(error){status(error.message)}finally{b.disabled=false}};return b}
 function link(text,url){const a=el('a',text);a.href=url;return a}
 function syncProfiles(){
  const form=$('#create-task');for(const [name,app] of [['profile',form.elements.agent.value],['github','github']]){
   const select=form.elements[name],old=select.value;select.replaceChildren(new Option(name==='github'?'None':'No saved profile',''));
   for(const p of profiles.filter(p=>p.application===app))select.add(new Option(p.label,p.id));if([...select.options].some(o=>o.value===old))select.value=old;
  }
 }
 async function catalog(){profiles=await api('/v1/tasks/profiles');syncProfiles();if(!owner)return;
  const mapping=await api('/v1/task-service-credentials/profiles'),table=el('table'),head=el('tr');for(const text of ['API label','Saved profile (owner only)'])head.append(el('th',text));table.append(head);
  $('#service-profiles').replaceChildren(el('legend','Allowed profiles'));
  for(const p of mapping){const row=el('tr');row.append(el('td',p.label),el('td',p.name));table.append(row);const label=el('label'),input=el('input');input.type='checkbox';input.name='profileIds';input.value=p.id;label.append(input,document.createTextNode(p.label+' · '+p.name));$('#service-profiles').append(label)}
  $('#profile-mapping').replaceChildren(table);$('#service-panel').hidden=false;
  const roles=await api('/v1/agent-roles');$('#task-roles').replaceChildren(el('legend','Roles'));for(const role of roles){const label=el('label'),input=el('input');input.type='checkbox';input.name='roleIds';input.value=role.id;label.append(input,document.createTextNode(role.name));$('#task-roles').append(label)}$('#task-roles').hidden=!roles.length;
  await services();
 }
 async function services(){const rows=await api('/v1/task-service-credentials');$('#service-list').replaceChildren();for(const c of rows){const row=el('p',c.name+' · '+(c.revokedAt?'Revoked':c.expiresAt?'Expires '+new Date(c.expiresAt).toLocaleString():'Active'));if(!c.revokedAt)row.append(' ',action('Revoke',async()=>{await api('/v1/task-service-credentials/'+enc(c.id),'DELETE');await services()}));$('#service-list').append(row)}}
 function taskCard(t){const b=action(t.name,async()=>{location.hash=t.id});b.className='task-card';b.dataset.task=t.id;b.setAttribute('aria-current',String(t.id===selected));b.append(el('small',t.state.replaceAll('_',' ')+(t.queuePosition?' · Queue position '+t.queuePosition:'')));const at=t.finishedAt||t.createdAt;if(at)b.append(el('small',(t.finishedAt?'Finished ':'Created ')+new Date(at).toLocaleString()));return b}
 async function list(more=false){const state=$('#state-filter').value,q=new URLSearchParams({limit:'50'});if(state)q.set('state',state);if(more&&cursor)q.set('cursor',cursor);const result=await api('/v1/tasks?'+q);if(!more)$('#task-list').replaceChildren();for(const t of result.tasks)$('#task-list').append(taskCard(t));if(!$('#task-list').children.length)$('#task-list').append(el('p',state==='current'?'No current tasks.':state==='history'?'No finished tasks within the retention period.':'No tasks in this view.'));cursor=result.nextCursor;$('#load-more').hidden=!cursor}
 async function detail(){if(!selected)return;const id=selected,version=++sequence,path='/v1/tasks/'+enc(id);try{
  const [t,messages,events]=await Promise.all([api(path),api(path+'/messages'),api(path+'/events')]);if(selected!==id||version!==sequence)return;
  const fingerprint=JSON.stringify([t,messages,events]);if(detailVersion===fingerprint)return;detailVersion=fingerprint;
  const previous=$('#task-message')?.value||'',root=$('#task-detail');root.replaceChildren(el('h2',t.name),el('p',t.state+(t.queuePosition?' · Queue position '+t.queuePosition:'')),el('p',t.goal));
  const controls=el('div');controls.className='task-actions';if(t.box){controls.append(link('Open box','/boxes/'+enc(t.box.id)));controls.append(link('Chat','/chat#box='+enc(t.box.id)))}
  if(!done(t.state))controls.append(action('Cancel task',async()=>{await api(path+'/cancel','POST',{});detailVersion='';await refresh()}));
  else{if(t.box)controls.append(action('Delete box now',async()=>{if(!confirm('Delete this finished task’s box? Its saved result remains until the retention period ends.'))return;await api(path+'/box','DELETE');await refresh()}));controls.append(action('Delete task and results',async()=>{if(!confirm('Delete this task, its box and all saved results?'))return;await api(path,'DELETE');status('Deletion queued.');await refresh()}))}
  root.append(controls);
  if(t.deleteAt)root.append(el('p','Results and box will be deleted '+new Date(t.deleteAt).toLocaleString()+'. Download anything you need before then.'));
  if(t.result){root.append(el('h3','Result'));const text=el('div',t.result.text);text.className='task-result';root.append(text);for(const file of t.result.files||[]){const a=link(file.name+' ('+file.size+' bytes)',path+'/files/'+enc(file.id));a.download=file.name;const row=el('p');row.append(a);root.append(row)}}
  if(t.failure)root.append(el('h3',t.state),el('p',t.failure.message));
  if(done(t.state)){const details=el('details'),summary=el('summary','Final desktop and terminal');details.append(summary);const img=el('img');img.alt='Final task desktop';img.src=path+'/final-screenshot';img.onerror=()=>{img.hidden=true};details.append(img,link('Download terminal text',path+'/final-terminal'));root.append(details)}
  if(t.state==='running'){const form=el('form'),label=el('label','Message agent'),input=el('textarea');input.id='task-message';input.required=true;input.maxLength=20000;input.value=previous;label.append(input);form.append(label,el('button','Send message'));form.onsubmit=async e=>{e.preventDefault();const b=form.querySelector('button');b.disabled=true;try{await api(path+'/messages','POST',{text:input.value},{'Idempotency-Key':crypto.randomUUID()});input.value='';detailVersion='';await detail()}catch(error){status(error.message)}finally{b.disabled=false}};root.append(form)}
  const sharing=el('details');sharing.append(el('summary','Share links'));const kinds=el('select');for(const kind of ['view','screenshot',...(owner?['control']:[])])kinds.add(new Option(kind,kind));sharing.append(kinds,action('Create link',async()=>{const links=await api(path+'/links','POST',{kinds:[kinds.value],ttlSeconds:86400});for(const l of links)minted.set(l.id,l.url);detailVersion='';await detail()}));
  for(const l of t.links||[]){const row=el('p',l.kind+' · expires '+new Date(l.expiresAt).toLocaleString());if(minted.has(l.id)){const a=link('Open link',minted.get(l.id));a.target='_blank';a.rel='noopener noreferrer';row.append(' ',a);row.append(' ',action('Copy link',()=>navigator.clipboard.writeText(minted.get(l.id))))}row.append(' ',action('Revoke',async()=>{await api(path+'/links?id='+enc(l.id),'DELETE');minted.delete(l.id);detailVersion='';await detail()}));sharing.append(row)}
  root.append(sharing);
  const transcript=el('details');transcript.append(el('summary','Chat transcript'));for(const m of messages){const row=el('p',m.direction+': '+m.text);row.className='task-result';transcript.append(row)}root.append(transcript);
  const timeline=el('details');timeline.append(el('summary','Timeline'));for(const event of events.events){const row=el('p',new Date(event.createdAt).toLocaleString()+' · '+event.kind+' · '+event.text);row.className='task-event';timeline.append(row)}root.append(timeline);
 }catch(error){if(selected===id)status(error.message)}}
 async function refresh(){if(refreshing||$('#tasks-app').hidden)return;refreshing=true;try{await Promise.all([list(),detail()])}catch(error){status(error.message)}finally{refreshing=false}}
 async function enter(){try{const who=await api('/v1/whoami');owner=who.role==='owner';$('#login').hidden=true;$('#tasks-app').hidden=false;status('');await catalog();await refresh()}catch(error){status(error.message)}}
 $('#login').onsubmit=async e=>{e.preventDefault();try{await api('/v1/browser-session','POST',undefined,{Authorization:'Bearer '+e.target.elements.token.value});e.target.reset();await enter()}catch(error){status(error.message)}};
 $('#create-task').elements.agent.onchange=syncProfiles;
 $('#create-task').onsubmit=async e=>{e.preventDefault();const f=e.target,b=f.querySelector('button[type=submit]'),data=new FormData(f),req={name:data.get('name'),goal:data.get('goal'),agent:data.get('agent'),diskGiB:Number(data.get('diskGiB')),timeLimitSeconds:Number(data.get('minutes'))*60,retentionSeconds:Number(data.get('hours'))*3600,desktopResolution:data.get('desktopResolution'),tools:['desktop',...data.getAll('tools')],instructions:data.get('instructions'),loginProfiles:[],roleIds:data.getAll('roleIds')};
  if(data.get('proxy'))req.tools.push(data.get('proxy'));for(const name of ['memoryGiB','swapGiB'])if(data.get(name)!=='')req[name]=Number(data.get(name));if(data.get('slotId'))req.slotId=data.get('slotId');
  if(data.get('profile'))req.loginProfiles.push({profileId:data.get('profile'),model:data.get('model')||undefined,reasoningEffort:data.get('reasoningEffort')||undefined});if(data.get('github'))req.loginProfiles.push({profileId:data.get('github')});
  const serialized=JSON.stringify(req);if(requestBody!==serialized){requestBody=serialized;requestKey=crypto.randomUUID()}b.disabled=true;try{const t=await api('/v1/tasks','POST',req,{'Idempotency-Key':requestKey});requestBody=requestKey=null;$('#create-panel').open=false;location.hash=t.id;status('Task queued.');await refresh()}catch(error){status(error.message)}finally{b.disabled=false}};
 $('#service-form').onsubmit=async e=>{e.preventDefault();const f=e.target,b=f.querySelector('button'),data=new FormData(f);b.disabled=true;try{const c=await api('/v1/task-service-credentials','POST',{name:data.get('name'),profileIds:data.getAll('profileIds'),allowControl:data.get('allowControl')==='on',expiresAt:data.get('expiresAt')?new Date(data.get('expiresAt')).toISOString():undefined});$('#new-token textarea').value=c.token;$('#new-token').hidden=false;await services()}catch(error){status(error.message)}finally{b.disabled=false}};
 $('#clear-token').onclick=()=>{$('#new-token textarea').value='';$('#new-token').hidden=true};$('#refresh').onclick=refresh;$('#state-filter').onchange=()=>void refresh();$('#load-more').onclick=()=>void list(true).catch(e=>status(e.message));
 addEventListener('hashchange',()=>{selected=location.hash.slice(1);detailVersion='';document.querySelectorAll('[data-task]').forEach(n=>n.setAttribute('aria-current',String(n.dataset.task===selected)));void detail()});
 void enter();const timer=setInterval(refresh,30000);addEventListener('pagehide',()=>{clearInterval(timer);$('#new-token textarea').value='';minted.clear()},{once:true});
})();
