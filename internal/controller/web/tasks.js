'use strict';
(()=>{
 const $=s=>document.querySelector(s),enc=encodeURIComponent,done=s=>['completed','failed','timed_out','abandoned','cancelled'].includes(s);
 const el=(tag,text)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n};
 let profiles=[],owner=false,cursor='',selected=location.hash.slice(1),detailVersion='',sequence=0,requestKey,requestBody,refreshing=false;
 let taskProfiles=[],editingProfile=null,modelVersion=0;
 const minted=new Map();
 $('#api-context').value=`vbox Tasks API
Base URL: ${location.origin}
Use Authorization: Bearer <TASK_SERVICE_TOKEN> on every request; obtain the scoped token separately from the owner. JSON requests use Content-Type: application/json.

1. GET /v1/task-profiles lists reusable task presets. Create from one with:
POST /v1/tasks (Idempotency-Key: <new UUID>)
{"name":"job-123","goal":"Your task and input","taskProfile":"glm5.3flash"}
The preset supplies the agent, model, resources, login profiles and tools. Supply only name, goal, taskProfile and optional links. Preset edits apply to future tasks; retries keep the original snapshot.

For custom settings, GET /v1/tasks/profiles returns allowed login profiles: {id, application, label}. Select a profile matching the agent; IDs and labels hide private profile names.
2. POST /v1/tasks with Idempotency-Key: <new UUID> and body:
{"name":"research-example","goal":"Your goal and requested deliverables","agent":"codex","loginProfiles":[{"profileId":"<PROFILE_ID>"}],"diskGiB":10,"timeLimitSeconds":14400,"retentionSeconds":86400}
Agents: codex, claude, opencode. Optional resources: memoryGiB, swapGiB, slotId. A successful create returns HTTP 201 with the task id. Reuse the same idempotency key and body when retrying that creation.
3. Poll GET /v1/tasks/{id} every 30 seconds. queued, starting, running are active; completed, failed, timed_out, abandoned, cancelled are final. Maximum 4 active task boxes per account; extra tasks wait without allocating boxes.
4. Read result.text and result.files on completion, or failure.message on failure. Download each file with GET /v1/tasks/{id}/files/{fileId}. Save results before deleteAt; default retention is 24 hours. Finished tasks cannot be reopened; create a new task for follow-up work.

GET /v1/tasks?state=current or ?state=history lists visible tasks; follow nextCursor using ?cursor=<URL-encoded cursor> and the same filter. GET /v1/tasks/{id}/messages and /events show progress. POST /v1/tasks/{id}/cancel cancels; DELETE /v1/tasks/{id} deletes a finished task and its results.
The agent inside the task box finishes with completed_task(text, files?) or failed_task(message); the controller releases its worker slot automatically.`;
 $('#copy-api-context').onclick=async()=>{const field=$('#api-context'),message=$('#api-copy-status');try{await navigator.clipboard.writeText(field.value);message.textContent='Copied — ready to paste into an agent’s context.'}catch{field.focus();field.select();message.textContent='Select and copy the guide manually.'}};
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
   for(const p of profiles.filter(p=>p.application===app))select.add(new Option(p.label+(p.name?' · '+p.name:''),p.id));if([...select.options].some(o=>o.value===old))select.value=old;
  }
 }
 async function catalog(){profiles=await api('/v1/tasks/profiles');syncProfiles();await loadTaskProfiles();if(!owner)return;
  const mapping=await api('/v1/task-service-credentials/profiles'),table=el('table'),head=el('tr');for(const text of ['API label','Saved profile (owner only)'])head.append(el('th',text));table.append(head);
  $('#service-profiles').replaceChildren(el('legend','Allowed profiles'));
  for(const p of mapping){const row=el('tr');row.append(el('td',p.label),el('td',p.name));table.append(row);const label=el('label'),input=el('input');input.type='checkbox';input.name='profileIds';input.value=p.id;label.append(input,document.createTextNode(p.label+' · '+p.name));$('#service-profiles').append(label)}
  for(const p of profiles)p.name=mapping.find(m=>m.id===p.id)?.name;syncProfiles();
  $('#new-task-profile').hidden=false;$('#task-profile-save').hidden=false;
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
  if(t.taskProfile)root.append(el('p','Task profile: '+t.taskProfile));
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
 function configuration(form){
  const data=new FormData(form),req={agent:data.get('agent'),diskGiB:Number(data.get('diskGiB')),timeLimitSeconds:Number(data.get('minutes'))*60,retentionSeconds:Number(data.get('hours'))*3600,desktopResolution:data.get('desktopResolution'),tools:['desktop',...data.getAll('tools')],instructions:data.get('instructions'),loginProfiles:[],roleIds:data.getAll('roleIds')};
  if(data.get('proxy'))req.tools.push(data.get('proxy'));for(const name of ['memoryGiB','swapGiB'])if(data.get(name)!=='')req[name]=Number(data.get(name));if(data.get('slotId'))req.slotId=data.get('slotId');
  if(data.get('profile'))req.loginProfiles.push({profileId:data.get('profile'),model:data.get('model')||undefined,reasoningEffort:data.get('reasoningEffort')||undefined});if(data.get('github'))req.loginProfiles.push({profileId:data.get('github')});
  return req;
 }
 async function modelOptions(){
  const version=++modelVersion,form=$('#create-task'),profile=profiles.find(p=>p.id===form.elements.profile.value);$('#task-model-options').replaceChildren();
  if(!owner||!profile?.name)return;
  try{const catalog=await api('/v1/login-profiles/'+enc(profile.application)+'/'+enc(profile.name)+'/models');if(version!==modelVersion)return;for(const model of catalog.models||[])$('#task-model-options').append(new Option(model.label||model.id,model.id))}catch{/* Manual model entry remains available. */}
 }
 function fillConfiguration(settings){
  const f=$('#create-task').elements;f.agent.value=settings.agent;syncProfiles();
  for(const [name,value] of Object.entries({diskGiB:settings.diskGiB,memoryGiB:settings.memoryGiB||'',swapGiB:settings.swapGiB??'',minutes:settings.timeLimitSeconds/60,hours:settings.retentionSeconds/3600,desktopResolution:settings.desktopResolution,instructions:settings.instructions||'',slotId:settings.slotId||''}))f[name].value=value;
  const agent=(settings.loginProfiles||[]).find(ref=>profiles.find(p=>p.id===ref.profileId)?.application===settings.agent),github=(settings.loginProfiles||[]).find(ref=>profiles.find(p=>p.id===ref.profileId)?.application==='github');
  for(const [name,ref] of [['profile',agent],['github',github]]){if(ref&&![...f[name].options].some(o=>o.value===ref.profileId))f[name].add(new Option('Unavailable login profile',ref.profileId));f[name].value=ref?.profileId||''}
  f.model.value=agent?.model||'';f.reasoningEffort.value=agent?.reasoningEffort||'';f.proxy.value=(settings.tools||[]).find(t=>['tor-socks5','cloudflare-warp'].includes(t))||'';
  $('#create-task').querySelectorAll('input[name=tools]').forEach(i=>i.checked=(settings.tools||[]).includes(i.value));$('#task-roles').querySelectorAll('input').forEach(i=>i.checked=(settings.roleIds||[]).includes(i.value));void modelOptions();
 }
 function resetProfileEditor(){editingProfile=null;$('#task-profile-name').value='';$('#task-profile-name').readOnly=false;$('#save-task-profile').textContent='Save task profile';$('#cancel-profile-edit').hidden=true}
 function selectTaskProfile(){
  resetProfileEditor();const ref=$('#create-task').elements.taskProfile.value,profile=taskProfiles.find(p=>p.name===ref);if(profile)fillConfiguration(profile.settings);$('#task-settings').disabled=!!profile;$('#task-profile-save').hidden=!owner||!!profile;
 }
 async function loadTaskProfiles(){
  taskProfiles=await api('/v1/task-profiles');const select=$('#create-task').elements.taskProfile,old=select.value;select.replaceChildren(new Option('Custom settings',''));for(const p of taskProfiles)select.add(new Option(p.name,p.name));select.value=taskProfiles.some(p=>p.name===old)?old:'';
  $('#task-profiles-list').replaceChildren();if(!taskProfiles.length)$('#task-profiles-list').append(el('p','No task profiles yet.'));
  for(const p of taskProfiles){const card=el('article'),cfg=p.settings;card.className='task-profile-card';card.append(el('h3',p.name));const model=(cfg.loginProfiles||[]).find(r=>r.model);card.append(el('p',cfg.agent+(model?' · '+model.model+(model.reasoningEffort?' · '+model.reasoningEffort:''):'')+' · '+(cfg.memoryGiB?cfg.memoryGiB+' GiB RAM':'Default RAM')+' · '+(cfg.swapGiB!==undefined?cfg.swapGiB+' GiB swap':'Default swap')+' · '+cfg.diskGiB+' GiB disk'));
   const github=(cfg.loginProfiles||[]).some(r=>profiles.find(p=>p.id===r.profileId)?.application==='github'),tools=(cfg.tools||[]).filter(t=>t!=='desktop');card.append(el('p',(github?'GitHub login attached':'No GitHub login')+' · '+(tools.length?tools.join(', '):'No optional tools')));
   const actions=el('div');actions.className='task-actions';actions.append(action('Use profile',()=>{select.value=p.name;selectTaskProfile();$('#create-panel').open=true;$('#create-task').elements.name.focus()}));
   if(owner){actions.append(action('Edit',()=>{select.value='';$('#task-settings').disabled=false;fillConfiguration(cfg);editingProfile=p;$('#task-profile-name').value=p.name;$('#task-profile-name').readOnly=true;$('#save-task-profile').textContent='Update task profile';$('#cancel-profile-edit').hidden=false;$('#task-profile-save').hidden=false;$('#create-panel').open=true;$('#task-profile-name').scrollIntoView({block:'center'})}));actions.append(action('Delete',async()=>{if(!confirm('Delete task profile '+p.name+'? Existing tasks keep their settings.'))return;await api('/v1/task-profiles/'+enc(p.id),'DELETE');await loadTaskProfiles();selectTaskProfile()}))}
   card.append(actions);$('#task-profiles-list').append(card);
  }
 }
 $('#create-task').elements.agent.onchange=()=>{syncProfiles();$('#create-task').elements.model.value='';$('#create-task').elements.reasoningEffort.value='';void modelOptions()};
 $('#create-task').elements.profile.onchange=()=>{ $('#create-task').elements.model.value='';$('#create-task').elements.reasoningEffort.value='';void modelOptions()};
 $('#create-task').elements.taskProfile.onchange=selectTaskProfile;
 $('#new-task-profile').onclick=()=>{$('#create-task').elements.taskProfile.value='';$('#task-settings').disabled=false;resetProfileEditor();$('#task-profile-save').hidden=false;$('#create-panel').open=true;$('#task-profile-name').focus()};
 $('#cancel-profile-edit').onclick=resetProfileEditor;
 $('#save-task-profile').onclick=async()=>{const b=$('#save-task-profile');b.disabled=true;try{const req={name:$('#task-profile-name').value.trim(),settings:configuration($('#create-task'))};await api('/v1/task-profiles'+(editingProfile?'/'+enc(editingProfile.id):''),editingProfile?'PUT':'POST',req);resetProfileEditor();await loadTaskProfiles();status('Task profile saved. Use its name in the API.')}catch(error){status(error.message)}finally{b.disabled=false}};
 $('#create-task').onsubmit=async e=>{e.preventDefault();const f=e.target,b=f.querySelector('button[type=submit]'),ref=f.elements.taskProfile.value,req={name:f.elements.name.value,goal:f.elements.goal.value,...(ref?{taskProfile:ref}:configuration(f))};
  const serialized=JSON.stringify(req);if(requestBody!==serialized){requestBody=serialized;requestKey=crypto.randomUUID()}b.disabled=true;try{const t=await api('/v1/tasks','POST',req,{'Idempotency-Key':requestKey});requestBody=requestKey=null;$('#create-panel').open=false;location.hash=t.id;status('Task queued.');await refresh()}catch(error){status(error.message)}finally{b.disabled=false}};
 $('#service-form').onsubmit=async e=>{e.preventDefault();const f=e.target,b=f.querySelector('button'),data=new FormData(f);b.disabled=true;try{const c=await api('/v1/task-service-credentials','POST',{name:data.get('name'),profileIds:data.getAll('profileIds'),allowControl:data.get('allowControl')==='on',expiresAt:data.get('expiresAt')?new Date(data.get('expiresAt')).toISOString():undefined});$('#new-token textarea').value=c.token;$('#new-token').hidden=false;await services()}catch(error){status(error.message)}finally{b.disabled=false}};
 $('#clear-token').onclick=()=>{$('#new-token textarea').value='';$('#new-token').hidden=true};$('#refresh').onclick=refresh;$('#state-filter').onchange=()=>void refresh();$('#load-more').onclick=()=>void list(true).catch(e=>status(e.message));
 addEventListener('hashchange',()=>{selected=location.hash.slice(1);detailVersion='';document.querySelectorAll('[data-task]').forEach(n=>n.setAttribute('aria-current',String(n.dataset.task===selected)));void detail()});
 void enter();const timer=setInterval(refresh,30000);addEventListener('pagehide',()=>{clearInterval(timer);$('#new-token textarea').value='';minted.clear()},{once:true});
})();
