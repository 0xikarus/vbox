'use strict';
const $=s=>document.querySelector(s);
let token='',defaults=null,epoch=0;
let boxRefreshTimer;
let fleetSnapshots=[];
let ownerTools=false,instructionPresets={defaultName:'',presets:[]};
let agentRoles=[],roleBoxes=[],roleAssignmentDraft=new Map(),roleAssignmentsDirty=false;
const presetBodyCache=new Map();
let boxInstructionTarget=null,boxCredentialTarget=null;
const poolKey=(provider,providerCredential)=>JSON.stringify({provider,providerCredential:providerCredential||''});
const poolLabel=(provider,alias)=>(provider==='shared-worker'?'Shared worker':'Dedicated · '+provider)+' / '+(alias||'default');
function boxPlacement(box){
 if(!box.slotId)return 'Unassigned';
 const fleet=fleetSnapshots.find(f=>f.provider===box.provider&&f.providerCredential===(box.providerCredential||''));
 const slot=fleet?.slots?.find(s=>s.id===box.slotId);
 const worker=box.provider==='shared-worker'?(box.providerCredential||'shared worker'):(slot?.serviceName||slot?.serviceId||box.slotId);
 return (box.provider==='shared-worker'?'Shared':'Dedicated')+' · '+worker+' · slot '+(slot?.ordinal??box.slotId);
}
function updateBoxPlacements(boxes){
 const byID=new Map(boxes.map(box=>[box.id,box]));
 for(const row of document.querySelectorAll('#box-list [data-box-id]')){const box=byID.get(row.dataset.boxId);if(box){const cell=row.querySelector('.box-placement'),text=boxPlacement(box);cell.textContent=text;cell.title=text}}
}
function renderPoolChoices(providers){
 for(const selector of ['#create-pool','#capacity-pool']){
  const select=$(selector),previous=select.value;select.replaceChildren();
  const fallback=node('option',selector==='#create-pool'?'Automatic (available capacity)':'Controller default');fallback.value='';select.append(fallback);
  for(const provider of providers){const option=node('option',poolLabel(provider.provider,provider.name));option.value=poolKey(provider.provider,provider.name);select.append(option)}
  if([...select.options].some(option=>option.value===previous))select.value=previous;
 }
}
async function chooseCreationPool(tools){
 const fallback=await api('/v1/controller-defaults');
 const providers=await api('/v1/provider-credentials');
 const candidates=await Promise.all(providers.map(async provider=>{
  const target={provider:provider.provider,providerCredential:provider.name||''};
  try{const fleet=await api('/v1/fleet/status?'+new URLSearchParams(target));return fleet.freeSlots>0?{...target,load:(fleet.occupiedSlots||0)/Math.max(1,fleet.actualSlots||0),free:fleet.freeSlots}:null}catch{return null}
 }));
 const available=candidates.filter(Boolean).sort((left,right)=>left.load-right.load||right.free-left.free||Number(right.provider===fallback.provider&&right.providerCredential===fallback.providerCredential)-Number(left.provider===fallback.provider&&left.providerCredential===fallback.providerCredential)||poolKey(left.provider,left.providerCredential).localeCompare(poolKey(right.provider,right.providerCredential)));
 if(!available.length)return fallback;
 return {provider:available[0].provider,providerCredential:available[0].providerCredential};
}
const deletingBoxes=new Set();
const startingBoxes=new Set();
async function api(path,method='GET',body,headers={}){
 const r=await fetch(path,{method,credentials:'same-origin',headers:{...(token?{Authorization:'Bearer '+token}:{}),'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body)});
 if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}return r.status===204?null:r.json();
}
function action(fn){return async e=>{e?.preventDefault();$('#error').textContent='';try{await fn(e)}catch(err){$('#error').className='flash error';$('#error').textContent=err.message}}}
// The banner is shared with errors, so success has to put the styling back.
function notice(message){const el=$('#error');el.className='flash';el.textContent=message}
function node(tag,text){const n=document.createElement(tag);n.textContent=text;return n}
function tableText(text){const t=text??'—',n=node('span',t);n.className=t==='—'?'table-text muted':'table-text';n.title=String(t);return n}
// Secondary cell values (restoration state, failure reason) read as a dimmed
// note under the primary value instead of running into it.
function tableNote(text){const n=tableText(text);n.className='table-text state-note';return n}
function button(text,fn){const b=node('button',text);b.type='button';b.className='linkbtn';b.addEventListener('click',action(fn));return b}
const TRASH_ICON='<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>';
const RESTART_ICON='<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 11a9 9 0 1 1 2.4 7"/><path d="M3 4v7h7"/></svg>';
function trashButton(label,fn){const b=node('button');b.type='button';b.className='linkbtn danger';b.setAttribute('aria-label',label);b.title=label;b.innerHTML=TRASH_ICON;b.addEventListener('click',action(fn));return b}
function renderCreationProfileChoices(root,profiles,agentSelect,selected=''){
 root._modelPicker?.destroy();
 root.replaceChildren();
 const profileLabel=node('label','login profile ');profileLabel.className='field';
 const profileSelect=document.createElement('select');profileSelect.name='loginProfile';profileLabel.append(profileSelect);
 const modelLabel=node('label','model ');modelLabel.className='field';
 const modelInput=document.createElement('input');modelInput.name='agentModel';modelInput.maxLength=200;modelLabel.append(modelInput);const modelPicker=window.VMBoxModelPicker.create(modelInput);root._modelPicker=modelPicker;
 const githubLabel=node('label','GitHub profile ');githubLabel.className='field';
 const githubSelect=document.createElement('select');githubSelect.name='githubProfile';githubSelect.append(node('option','None'));githubSelect.options[0].value='';githubLabel.append(githubSelect);
 for(const profile of profiles.filter(profile=>profile.application==='github')){const option=node('option',profile.name);option.value=JSON.stringify({application:'github',name:profile.name});githubSelect.append(option)}
 githubLabel.hidden=githubSelect.options.length===1;
 root.append(profileLabel,modelLabel,githubLabel);
 const populate=()=>{
  const app=agentSelect.value,previous=profileSelect.value||selected;profileSelect.replaceChildren();
  const empty=node('option','None');empty.value='';profileSelect.append(empty);
  const choices=profiles.filter(profile=>profile.application===app);
  for(const profile of choices){const option=node('option',profile.name);option.value=JSON.stringify({application:profile.application,name:profile.name});option.dataset.model=profile.model||'';profileSelect.append(option)}modelPicker.setApplication(app);
  if([...profileSelect.options].some(option=>option.value===previous))profileSelect.value=previous;
  profileLabel.hidden=app==='shell'||choices.length===0;
  root.hidden=profileLabel.hidden&&githubLabel.hidden;
  syncModel();
 };
 const syncModel=()=>{const option=profileSelect.selectedOptions[0],hasProfile=!!profileSelect.value;modelInput.disabled=!hasProfile;modelPicker.setValue(hasProfile?option?.dataset.model||'':'');modelPicker.setReasoningEffort('');modelPicker.setOptions(window.VMBoxModelPicker.optionsFor(agentSelect.value,[option?.dataset.model]));modelLabel.hidden=!hasProfile;const ref=hasProfile?JSON.parse(profileSelect.value):null;modelPicker.setLoader(ref?.application==='opencode'?()=>api('/v1/login-profiles/opencode/'+encodeURIComponent(ref.name)+'/models'):null)};
 profileSelect.addEventListener('change',syncModel);agentSelect.onchange=populate;populate();
 return {profileSelect,modelInput};
}
// Hibernate then allocate again. Agents and tmux sessions do not survive it, so
// it asks first; the workspace volume is kept either way.
async function restartBox(b){
 if(!confirm('Restart box "'+b.name+'"? It hibernates and starts again, so running agents and terminal sessions end. The workspace volume is kept, and the box picks up its current instructions and credentials on the way back up.'))return;
 notice('Restarting '+b.name+'…');
 await api(bp(b.id)+'/hibernate','POST',{'Idempotency-Key':crypto.randomUUID()},{});
 const deadline=Date.now()+180000;
 for(;;){
  await new Promise(r=>setTimeout(r,3000));
  const current=(await api('/v1/logical-boxes')).find(x=>x.id===b.id);
  if(!current)throw Error('Box disappeared while restarting.');
  const phase=boxPhase(current.state);
  if(phase==='stopped'||phase==='failed')break;
  if(Date.now()>deadline)throw Error('Still '+current.state+' after 3 minutes; resume it once it settles.');
 }
 await api(bp(b.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'web'});
 notice(b.name+' is starting again.');
 await refresh();
}

// Box states that are still moving; only show an action the state can satisfy.
const TRANSIENT_STATES=new Set(['reserved','attaching','hibernating','draining','deleting']);
function boxPhase(state){if(state==='running')return 'running';if(state==='failed')return 'failed';if(state==='reserved'||state==='attaching')return 'creating';if(state==='deleting')return 'deleting';if(state==='hibernating'||state==='draining')return 'transitioning';return 'stopped'}
async function deleteBoxWhenReady(box,onWait){
 const deadline=Date.now()+5*60*1000;
 for(;;){
  try{return await api(bp(box.id)+'/volume','DELETE',{confirmation:box.name})}
  catch(err){
   if(Date.now()>=deadline||!/creation is still active|cannot transition from (?:attaching|reserved)|has not released its compute claim|workspace flush is active/i.test(err.message))throw err;
   onWait?.();
   await new Promise(resolve=>setTimeout(resolve,1500));
  }
 }
}
// Restore same-tab navigation without storing credentials in JavaScript storage.
async function restoreLogin(){try{await api('/v1/browser-session');await refresh();$('#login').hidden=true;$('#app').hidden=false}catch{}}
window.addEventListener('DOMContentLoaded',restoreLogin);
function dataTable(headers,rows){const wrap=node('div');wrap.className='table-wrap';const table=node('table');table.className='markets';const head=node('tr');for(const h of headers)head.append(node('th',h));table.append(head);for(const values of rows){const row=node('tr');row.className='row';for(const value of values){const cell=node('td');cell.append(tableText(value));row.append(cell)}table.append(row)}wrap.append(table);return wrap}
function kpi(pairs){const k=node('div');k.className='kpi';for(const [label,value] of pairs){const s=node('span',label+' ');s.append(node('b',String(value)));k.append(s)}return k}
function rawDetails(value){const d=document.createElement('details');d.append(node('summary','Technical details · JSON'),node('pre',JSON.stringify(value,null,2)));return d}
let locationTarget=null,locationLoading=false,locationSaving=false;
function resetLocation(){locationTarget=null;$('#location-form').hidden=true;$('#location-status').textContent='';}
$('#load-locations').addEventListener('click',action(async()=>{
 if(locationLoading||locationSaving)return;if(!defaults)throw Error('Configure controller default first');
 const target={provider:defaults.provider,providerCredential:defaults.providerCredential},version=epoch;locationLoading=true;$('#load-locations').disabled=true;resetLocation();$('#location-status').textContent='Loading locations…';
 try{
  const q=new URLSearchParams({provider:target.provider,providerCredential:target.providerCredential});
  const [regions,config]=await Promise.all([api('/v1/fleet/regions?'+q),api('/v1/fleet/slots?'+q)]);
  if(version!==epoch||defaults?.provider!==target.provider||defaults?.providerCredential!==target.providerCredential)return;
  if(!Array.isArray(regions)||!regions.length)throw Error('This provider returned no available locations.');
  const select=$('#location-form select');select.replaceChildren();
  const placeholder=node('option','Choose a location');placeholder.value='';select.append(placeholder);
  for(const region of regions){const option=node('option',region.name?region.name+' ('+region.id+')':region.id);option.value=region.id;select.append(option)}
  select.value=config.region||'';locationTarget=target;$('#location-form').hidden=false;
  $('#location-status').textContent='Current fleet location: '+(config.region||'Provider default')+'. Applies to '+target.provider+' / '+target.providerCredential+'.';
 }catch(err){if(version===epoch)$('#location-status').textContent=err.message}
 finally{locationLoading=false;$('#load-locations').disabled=false}
}));
$('#location-form').addEventListener('submit',action(async e=>{
 if(locationSaving)return;
 if(!locationTarget||defaults?.provider!==locationTarget.provider||defaults?.providerCredential!==locationTarget.providerCredential)throw Error('Load locations for the current provider first.');
 const target={...locationTarget},region=e.target.elements.region.value,version=epoch;
 if(!region)throw Error('Choose a location.');
 locationSaving=true;e.target.querySelector('button').disabled=true;
 try{await api('/v1/fleet/location','PUT',{...target,region});if(version!==epoch)return;$('#location-status').textContent='Fleet location saved: '+region+'.';await refresh()}
 catch(err){if(version===epoch)$('#location-status').textContent=err.message}
 finally{locationSaving=false;e.target.querySelector('button').disabled=false}
}));
function renderWorkerCapacity(){
 const root=$('#capacity');root.replaceChildren();
 const workers=fleetSnapshots.reduce((total,fleet)=>total+(fleet.error?0:fleet.provider==='shared-worker'?(fleet.slots?.length?1:0):(fleet.slots?.length||0)),0);
 const slots=fleetSnapshots.reduce((total,fleet)=>total+(fleet.actualSlots||0),0);
 root.append(kpi([['Loaded capacity:',workers+' workers · '+slots+' compute slots']]),node('p','Shared slots compete for their host’s CPU and memory.'));
 for(const fleet of fleetSnapshots){
  root.append(node('h3',poolLabel(fleet.provider,fleet.providerCredential)));
  if(fleet.error){root.append(node('p','Capacity unavailable: '+fleet.error));continue}
  root.append(kpi([['Desired:',fleet.desiredSlots],['Free:',fleet.freeSlots],['Occupied:',fleet.occupiedSlots],['Unhealthy:',fleet.unhealthySlots]]));
  root.append(dataTable(['Worker','Slot','State','Health','Location','Box'],(fleet.slots||[]).map(slot=>[fleet.provider==='shared-worker'?fleet.providerCredential:(slot.serviceName||slot.serviceId||slot.id||'—'),slot.ordinal,slot.state,slot.health,slot.region,slot.logicalBoxName||'—'])));
  const detached=fleet.detachedLogicalBoxes||[];if(detached.length)root.append(node('h3','Detached workspaces'),dataTable(['Box','State'],detached.map(box=>[box.name,box.state])));
 }
 const target=$('#capacity-pool').value?JSON.parse($('#capacity-pool').value):defaults;
 const selected=target&&fleetSnapshots.find(fleet=>fleet.provider===target.provider&&fleet.providerCredential===(target.providerCredential||''));
 const input=$('#slots input');if(document.activeElement!==input)input.value=selected?.desiredSlots??'';
 root.append(rawDetails(fleetSnapshots));
}
$('#capacity-pool').addEventListener('change',renderWorkerCapacity);
function renderNotifications(values){const root=$('#destinations');root.replaceChildren();if(!values.length){root.append(node('p','No notification destinations configured. Notifications are optional.'));return}root.append(dataTable(['Name','Type','Status','Allowed users','Allowed chats'],values.map(n=>[n.name,n.kind,n.enabled?'Enabled':'Disabled',(n.allowedUsers||[]).join(', ')||'Not specified',(n.allowedChats||[]).join(', ')||'Not specified'])),rawDetails(values))}
const bp=id=>'/v1/logical-boxes/'+encodeURIComponent(id),pp=(p,n)=>'/v1/provider-credentials/'+encodeURIComponent(p)+'/'+encodeURIComponent(n);
function renderBoxes(boxes){
 clearTimeout(boxRefreshTimer);
 for(const id of startingBoxes){const box=boxes.find(b=>b.id===id);if(!box||box.state==='running'||box.state==='failed'||box.state==='deleting'||box.state==='hibernated'&&box.failureReason)startingBoxes.delete(id)}
 const wrap=node('div');wrap.className='table-wrap';
 const table=document.createElement('table');table.className='markets';const head=document.createElement('tr');['Name','State','Worker / slot','Default agent','Roles','CLI','Actions'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const b of boxes){
  const row=document.createElement('tr');row.className='row';const cell=document.createElement('td'),select=document.createElement('select'),status=node('td',b.state),actions=document.createElement('td');row.dataset.boxId=b.id;
  for(const agent of ['claude','codex','opencode','shell']){const o=node('option',agent);o.value=agent;select.append(o)}select.value=b.defaultAgent;select.disabled=b.state==='deleting'||deletingBoxes.has(b.id);
  select.addEventListener('change',action(()=>api(bp(b.id),'PATCH',{defaultAgent:select.value})));cell.append(select);
  const name=node('td',''),link=node(b.state==='deleting'?'span':'a',b.name);link.className='table-text';link.title=b.name;if(b.state!=='deleting')link.href='/boxes/'+encodeURIComponent(b.id);name.append(link);
  status.replaceChildren(tableText(startingBoxes.has(b.id)&&b.state!=='running'?'starting':b.state));
  if(b.restorationState)status.append(tableNote(b.restorationState));
  if(b.failureReason)status.append(tableNote(b.failureReason));
  if(boxPhase(b.state)==='stopped'||boxPhase(b.state)==='failed'){
   const resume=button('Resume',async()=>{
    if(startingBoxes.has(b.id))return;
    startingBoxes.add(b.id);
    try{await api(bp(b.id)+'/allocate','POST',{leaseOwner:'web'},{'Idempotency-Key':crypto.randomUUID()})}
    catch(err){startingBoxes.delete(b.id);throw err}
    const version=epoch;const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)
   });resume.setAttribute('aria-label','Resume box '+b.name);actions.append(resume);
  }
  const instructions=button('Instructions…',()=>openBoxInstructions(b));instructions.setAttribute('aria-label','Instructions for box '+b.name);actions.append(instructions);
  if(ownerTools){const credentials=button('Credentials…',()=>openBoxCredentials(b));credentials.setAttribute('aria-label','Credentials for box '+b.name);actions.append(credentials)}
  // Restart hibernates first, which only a running box can do; a stopped box
  // already offers Resume, so offering Restart there would just fail.
  if(boxPhase(b.state)==='running'){
   const restart=button('',()=>void restartBox(b));restart.classList.add('restart-action');restart.innerHTML=RESTART_ICON;
   restart.title='Hibernate and start again; running agents and sessions end';
   restart.setAttribute('aria-label','Restart box '+b.name);actions.append(restart);
  }
  const remove=trashButton('Delete box '+b.name,async()=>{
   if(deletingBoxes.has(b.id)||!confirm('Delete box "'+b.name+'" and its workspace volume? Running processes will stop and all files in the volume will be permanently deleted. This cannot be undone. Shared fleet services and other boxes are kept.'))return;
   const version=epoch;deletingBoxes.add(b.id);remove.disabled=true;remove.title='requesting deletion…';
   try{await deleteBoxWhenReady(b,()=>notice('Waiting for '+b.name+' to finish its current setup step before deleting…'));notice('Deleting '+b.name+'…')}
   finally{deletingBoxes.delete(b.id);try{const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}catch{if(version===epoch)remove.disabled=false}}
  });remove.disabled=b.state==='deleting'||deletingBoxes.has(b.id);actions.prepend(remove);
  const placement=node('td'),placementText=tableText(boxPlacement(b));placementText.classList.add('box-placement');placement.append(placementText);const cli=node('td');cli.append(tableText('vmbox '+JSON.stringify(b.name)));const roles=node('td');const roleLink=node('a',(b.roles||[]).map(role=>role.name).join(', ')||'None');roleLink.href='#roles';roleLink.className='table-text';roles.append(roleLink);row.append(name,status,placement,cell,roles,cli,actions);table.append(row);
 }wrap.append(table);$('#box-list').replaceChildren(wrap);
 if(startingBoxes.size||boxes.some(b=>TRANSIENT_STATES.has(b.state))){const version=epoch;boxRefreshTimer=setTimeout(async()=>{try{const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}catch(err){if(version===epoch)$('#error').textContent='Could not check box progress. Use Refresh to retry. '+err.message}},5000)}
}
function setRoleAssignmentsDirty(dirty){roleAssignmentsDirty=dirty;$('#save-role-assignments').disabled=!dirty;$('#discard-role-assignments').disabled=!dirty;$('#role-status').textContent=dirty?'Unsaved assignment changes.':''}
function renderCreationRoles(){const root=$('#create-role-choices'),selected=new Set([...root.querySelectorAll('input:checked')].map(input=>input.value));root.replaceChildren();for(const role of agentRoles){const label=node('label'),input=document.createElement('input');input.type='checkbox';input.name='roleIds';input.value=role.id;input.checked=selected.has(role.id);label.append(input,document.createTextNode(role.name));root.append(label)}if(!agentRoles.length)root.append(node('span','No roles defined. Type an exact name in the role editor, or leave this box unassigned.'))}
function roleIDsForBox(box){return new Set(roleAssignmentDraft.get(box.id)||[])}
function updateDraft(boxID,roleID,checked){const ids=roleIDsForBox({id:boxID});checked?ids.add(roleID):ids.delete(roleID);roleAssignmentDraft.set(boxID,[...ids]);setRoleAssignmentsDirty(true)}
function roleCheckbox(box,role){const input=document.createElement('input');input.type='checkbox';input.checked=roleIDsForBox(box).has(role.id);input.setAttribute('aria-label',role.name+' for '+box.name);input.addEventListener('change',()=>updateDraft(box.id,role.id,input.checked));return input}
function renderRoleMatrix(){const root=$('#role-matrix'),query=$('#role-box-search').value.trim().toLowerCase(),boxes=roleBoxes.filter(box=>!query||box.name.toLowerCase().includes(query));root.replaceChildren();if(!agentRoles.length){const empty=node('p','No roles exist yet. vmbox does not generate role names—define the first exact name yourself.');empty.className='role-empty';root.append(empty);return}
 const wrap=node('div');wrap.className='role-matrix-wrap';const table=node('table');table.className='role-matrix';const head=node('tr');const boxHead=node('th','Box');boxHead.className='sticky-box';head.append(boxHead);for(const role of agentRoles){const th=node('th'),edit=button(role.name,()=>openRoleEditor(role));edit.classList.add('role-heading');th.append(edit);head.append(th)}table.append(head);for(const box of boxes){const row=node('tr'),name=node('th',box.name);name.scope='row';name.className='sticky-box';row.append(name);for(const role of agentRoles){const cell=node('td');cell.append(roleCheckbox(box,role));row.append(cell)}table.append(row)}wrap.append(table);
 const mobile=node('div');mobile.className='role-mobile';const select=document.createElement('select');for(const box of boxes){const option=node('option',box.name);option.value=box.id;select.append(option)}const list=node('div');list.className='role-checklist';const draw=()=>{list.replaceChildren();const box=boxes.find(value=>value.id===select.value);if(!box)return;for(const role of agentRoles){const label=node('label'),input=roleCheckbox(box,role);label.append(input,document.createTextNode(role.name));list.append(label)}};select.addEventListener('change',draw);draw();mobile.append(select,list);root.append(wrap,mobile)}
function renderRoles(roles,boxes,resetDraft=true){agentRoles=roles||[];roleBoxes=boxes||[];if(resetDraft||!roleAssignmentsDirty){roleAssignmentDraft=new Map(roleBoxes.map(box=>[box.id,(box.roles||[]).map(role=>role.id)]));setRoleAssignmentsDirty(false)}renderCreationRoles();renderRoleMatrix()}
function openRoleEditor(role=null){const form=$('#role-editor-form'),cap=role?.capabilities||{};form.reset();form.elements.id.value=role?.id||'';form.elements.name.value=role?.name||'';form.elements.description.value=role?.description||'';form.elements.contactScope.value=role?.contactScope||'none';const set=(name,value)=>{if(value!==undefined&&value!==null)form.elements[name].value=String(value)},check=(name,value)=>form.elements[name].checked=!!value;check('requestMoreTimeEnabled',cap.requestMoreTime?.enabled);set('maxExtensionMinutes',cap.requestMoreTime?.maxExtensionMinutes);set('maxTotalMinutes',cap.requestMoreTime?.maxTotalMinutes);check('queueFollowupEnabled',cap.queueFollowup?.enabled);set('maxDelayMinutes',cap.queueFollowup?.maxDelayMinutes);set('maxPending',cap.queueFollowup?.maxPending);check('createAgentBoxEnabled',cap.createAgentBox?.enabled);set('maxBoxes',cap.createAgentBox?.maxBoxes);set('maxDiskGiB',cap.createAgentBox?.maxDiskGiB);const allowed=new Set(cap.createAgentBox?.allowedAgents||[]);form.querySelectorAll('input[name=allowedAgents]').forEach(input=>input.checked=allowed.has(input.value));check('createEmailAddressEnabled',cap.createEmailAddress?.enabled);set('maxAddresses',cap.createEmailAddress?.maxAddresses);set('emailDomains',(cap.createEmailAddress?.domains||[]).join(', '));set('emailAddressTypes',(cap.createEmailAddress?.addressTypes||[]).join(', '));check('sharedChatDiscover',cap.sharedChats?.discover);check('sharedChatRead',cap.sharedChats?.read);check('sharedChatSubscribe',cap.sharedChats?.subscribe);check('sharedChatCreate',cap.sharedChats?.create);check('sharedChatInvite',cap.sharedChats?.invite);check('sharedChatsEnabled',Object.values(cap.sharedChats||{}).some(Boolean));const mcpInputs=[...form.querySelectorAll('input[name=mcpTools]')],allowedMCP=new Set(cap.mcpTools?.enabled?(cap.mcpTools.allowedTools||[]):[]);mcpInputs.forEach(input=>input.checked=allowedMCP.has(input.value));$('#role-editor-title').textContent=role?'Edit role · '+role.name:'Create role';$('#delete-role').hidden=!role;$('#role-assigned-count').textContent=role?'Assigned to '+role.assignedBoxCount+' box'+(role.assignedBoxCount===1?'':'es')+'.':'Not assigned yet.';const selected=new Set(role?.contactBoxIds||[]),root=$('#role-contact-boxes');root.replaceChildren();for(const box of roleBoxes){const label=node('label'),input=document.createElement('input');input.type='checkbox';input.name='contactBoxIds';input.value=box.id;input.checked=selected.has(box.id);label.append(input,document.createTextNode(box.name));root.append(label)}root.hidden=form.elements.contactScope.value!=='selected';const assignable=new Set(cap.createAgentBox?.assignableRoleIds||[]),assignableRoot=$('#role-assignable-roles');assignableRoot.replaceChildren();for(const candidate of agentRoles){const label=node('label'),input=document.createElement('input');input.type='checkbox';input.name='assignableRoleIds';input.value=candidate.id;input.checked=assignable.has(candidate.id);label.append(input,document.createTextNode(candidate.name));assignableRoot.append(label)}form.querySelectorAll('.role-capability-options').forEach(details=>details.open=false);syncRoleCapabilities(form);syncMCPToolGroups(form);$('#role-editor-status').textContent='';modalEl('role-editor-modal').hidden=false;form.elements.name.focus()}
function syncRoleCapabilities(form=$('#role-editor-form')){form.querySelectorAll('.role-capability-options[data-capability]').forEach(root=>root.hidden=!form.elements[root.dataset.capability].checked)}
function changeRoleCapability(input){if(input.name==='sharedChatsEnabled'&&input.checked&&!['sharedChatDiscover','sharedChatRead','sharedChatSubscribe','sharedChatCreate','sharedChatInvite'].some(name=>input.form.elements[name].checked)){for(const name of ['sharedChatDiscover','sharedChatRead','sharedChatSubscribe','sharedChatCreate','sharedChatInvite'])input.form.elements[name].checked=true}syncRoleCapabilities(input.form)}
function syncMCPToolGroups(form=$('#role-editor-form')){for(const group of form.querySelectorAll('.mcp-tool-group')){const tools=[...group.querySelectorAll('input[name=mcpTools]')],toggle=group.querySelector('.mcp-tool-group-toggle'),selected=tools.filter(input=>input.checked).length;toggle.checked=selected===tools.length;toggle.indeterminate=selected>0&&selected<tools.length}}
function changeMCPToolGroup(toggle){for(const input of toggle.closest('.mcp-tool-group').querySelectorAll('input[name=mcpTools]'))input.checked=toggle.checked;syncMCPToolGroups(toggle.form)}
$('#role-box-search').addEventListener('input',renderRoleMatrix);
$('#create-role').addEventListener('click',()=>openRoleEditor());
$('#discard-role-assignments').addEventListener('click',()=>renderRoles(agentRoles,roleBoxes,true));
$('#save-role-assignments').addEventListener('click',action(async()=>{await api('/v1/agent-role-assignments','PUT',{assignments:roleBoxes.map(box=>({boxId:box.id,roleIds:roleAssignmentDraft.get(box.id)||[]}))});setRoleAssignmentsDirty(false);notice('Role assignments saved.');await refresh()}));
document.querySelectorAll('#role-editor-form input[name=contactScope]').forEach(input=>input.addEventListener('change',()=>{$('#role-contact-boxes').hidden=input.form.elements.contactScope.value!=='selected'}));
document.querySelectorAll('#role-editor-form .role-capability-toggle input[type=checkbox]').forEach(input=>input.addEventListener('change',()=>changeRoleCapability(input)));
document.querySelectorAll('#role-editor-form .mcp-tool-group-toggle').forEach(input=>input.addEventListener('change',()=>changeMCPToolGroup(input)));
document.querySelectorAll('#role-editor-form input[name=mcpTools]').forEach(input=>input.addEventListener('change',()=>syncMCPToolGroups(input.form)));
$('#role-editor-form').addEventListener('submit',action(async e=>{const f=e.target.elements,id=f.id.value,csv=value=>value.split(',').map(item=>item.trim()).filter(Boolean),num=name=>Number.parseInt(f[name].value,10)||0,body={name:f.name.value.trim(),description:f.description.value.trim(),contactScope:f.contactScope.value,contactBoxIds:f.contactScope.value==='selected'?[...e.target.querySelectorAll('input[name=contactBoxIds]:checked')].map(input=>input.value):[],capabilities:{requestMoreTime:{enabled:f.requestMoreTimeEnabled.checked,maxExtensionMinutes:num('maxExtensionMinutes'),maxTotalMinutes:num('maxTotalMinutes')},queueFollowup:{enabled:f.queueFollowupEnabled.checked,maxDelayMinutes:num('maxDelayMinutes'),maxPending:num('maxPending')},createAgentBox:{enabled:f.createAgentBoxEnabled.checked,maxBoxes:num('maxBoxes'),maxDiskGiB:num('maxDiskGiB'),allowedAgents:[...e.target.querySelectorAll('input[name=allowedAgents]:checked')].map(input=>input.value),assignableRoleIds:[...e.target.querySelectorAll('input[name=assignableRoleIds]:checked')].map(input=>input.value)},createEmailAddress:{enabled:f.createEmailAddressEnabled.checked,maxAddresses:num('maxAddresses'),domains:csv(f.emailDomains.value),addressTypes:csv(f.emailAddressTypes.value)},sharedChats:{discover:f.sharedChatsEnabled.checked&&f.sharedChatDiscover.checked,read:f.sharedChatsEnabled.checked&&f.sharedChatRead.checked,subscribe:f.sharedChatsEnabled.checked&&f.sharedChatSubscribe.checked,create:f.sharedChatsEnabled.checked&&f.sharedChatCreate.checked,invite:f.sharedChatsEnabled.checked&&f.sharedChatInvite.checked},mcpTools:{enabled:true,allowedTools:[...e.target.querySelectorAll('input[name=mcpTools]:checked')].map(input=>input.value)}}};$('#role-editor-status').textContent='Saving…';await api('/v1/agent-roles'+(id?'/'+encodeURIComponent(id):''),id?'PUT':'POST',body);modalEl('role-editor-modal').hidden=true;notice(id?'Role updated.':'Role created.');await refresh()}));
$('#delete-role').addEventListener('click',action(async()=>{const form=$('#role-editor-form'),id=form.elements.id.value,name=form.elements.name.value;if(!id||!confirm('Delete role "'+name+'"? Its assignments and grants will be removed. Other roles and manual contact allowances remain effective.'))return;await api('/v1/agent-roles/'+encodeURIComponent(id),'DELETE');modalEl('role-editor-modal').hidden=true;notice('Role deleted.');await refresh()}));
function renderProfiles(identity,profiles){
 const tree=document.createElement('details');tree.open=true;tree.append(node('summary',identity.accountName+' ('+identity.accountId+')'));
 const choices=$('#profile-choices'),selected=choices.querySelector('select')?.value||'';choices.replaceChildren();
 for(const app of ['claude','codex','opencode','github']){
  const entries=profiles.filter(p=>p.application===app),branch=document.createElement('details');branch.open=true;branch.append(node('summary',app+' ('+entries.length+')'));const list=document.createElement('ul');
  for(const p of entries){const item=node('li',p.name+(p.model?' · '+p.model:'')+' · saved '+p.createdAt+' ');item.append(button('Delete',async()=>{if(!confirm('Delete saved profile '+app+' / '+p.name+'? This cannot be undone. Existing boxes keep their copied credentials; pending creations using this profile may fail.'))return;await api('/v1/login-profiles/'+encodeURIComponent(app)+'/'+encodeURIComponent(p.name),'DELETE');await refresh()}));list.append(item)}if(!entries.length)list.append(node('li','No saved profiles'));branch.append(list);tree.append(branch);
 }$('#profile-tree').replaceChildren(tree);
 renderCreationProfileChoices(choices,profiles,$('#create select[name="defaultAgent"]'),selected);
}
async function refresh(){
 const version=epoch,[caps,boxes,instructionList]=await Promise.all([api('/v1/capabilities'),api('/v1/logical-boxes'),api('/v1/instruction-presets').catch(()=>({defaultName:'',presets:[]}))]);if(version!==epoch)return;
 ownerTools=caps.providerEdits;
 document.querySelectorAll('[data-owner]:not(.modal)').forEach(n=>n.hidden=!ownerTools);
 applyInstructionPresets(instructionList);
 renderBoxes(boxes);
 if(!ownerTools)return;
 const [providers,schema,notifications,identity,profiles,toolPresets,roles]=await Promise.all([api('/v1/provider-credentials'),api('/v1/provider-schemas'),api('/v1/notifications'),api('/v1/whoami'),api('/v1/login-profiles'),api('/v1/tool-presets'),api('/v1/agent-roles').catch(()=>[])]);if(version!==epoch)return;
 renderRoles(roles,boxes,!roleAssignmentsDirty);
 renderPoolChoices(providers);
 const fleets=await Promise.all(providers.map(async provider=>{const target={provider:provider.provider,providerCredential:provider.name||''};try{return {...await api('/v1/fleet/status?'+new URLSearchParams(target)),...target}}catch(err){return {...target,error:err.message}}}));if(version!==epoch)return;fleetSnapshots=fleets;updateBoxPlacements(boxes);
 const chosenTools=new Set([...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value));$('#create-tools').replaceChildren(node('legend','Optional tools'));
 for(const preset of toolPresets){if(preset.id==='desktop')continue;const label=node('label'),input=node('input');input.type='checkbox';input.value=preset.id;input.checked=chosenTools.has(preset.id);label.title=preset.version+' — '+preset.description;label.append(input,document.createTextNode(preset.name));$('#create-tools').append(label)}
 renderProfiles(identity,profiles);
 $('#provider-list').replaceChildren();
 if(!providers.length)$('#provider-list').append(node('p','No providers configured. Add one below, validate it, then select it as the default.'));
 for(const p of providers){const line=node('p',p.provider+' / '+p.name+' ');line.append(button('Edit',()=>{const f=$('#provider').elements;f.provider.value=p.provider;f.alias.value=p.name;f.config.value=JSON.stringify(p.config||{},null,2);f.secret.value='';f.revision.value=p.updatedAt;$('#provider-editor').open=true;f.config.focus()}),button('Validate',async()=>{const result=await api(pp(p.provider,p.name)+'/validate','POST',{});$('#provider-result').textContent=(result.valid?'Validation passed. ':'Validation failed. ')+'Checked: '+(result.checked||[]).join(', ')+'. Not checked: '+(result.unchecked||[]).join(', ')}),button('Use as default',async()=>{await api('/v1/controller-defaults','PUT',{provider:p.provider,providerCredential:p.name});await refresh()}));const details=document.createElement('details');details.append(node('summary','Configuration'),dataTable(['Setting','Value'],Object.entries(p.config||{}).map(([key,value])=>[key,typeof value==='object'?JSON.stringify(value):String(value)])));$('#provider-list').append(line,details)}
 $('#schema').textContent=JSON.stringify(schema,null,2);renderNotifications(notifications);defaults=null;
 try{const d=await api('/v1/controller-defaults');if(version!==epoch)return;$('#provider-default').textContent='Default: '+d.provider+' / '+d.providerCredential;if(locationTarget&&(locationTarget.provider!==d.provider||locationTarget.providerCredential!==d.providerCredential))resetLocation();defaults=d;renderWorkerCapacity()}catch(err){if(version===epoch){renderWorkerCapacity();$('#provider-default').textContent='Check the default provider and capacity configuration.'}}
}
$('#login').addEventListener('submit',action(async e=>{token=e.target.elements.token.value;try{await api('/v1/browser-session','POST',{})}finally{token='';e.target.reset()}await refresh();$('#login').hidden=true;$('#app').hidden=false}));
$('#logout').addEventListener('click',action(async()=>{await api('/v1/browser-session','DELETE');epoch++;resetLocation();clearTimeout(boxRefreshTimer);startingBoxes.clear();token='';defaults=null;fleetSnapshots=[];ownerTools=false;agentRoles=[];roleBoxes=[];roleAssignmentDraft=new Map();roleAssignmentsDirty=false;instructionPresets={defaultName:'',presets:[]};presetBodyCache.clear();boxInstructionTarget=null;boxCredentialTarget=null;renderPoolChoices([]);$('#capacity').replaceChildren();$('#box-list').replaceChildren();$('#role-matrix').replaceChildren();$('#instruction-list').replaceChildren();$('#box-credentials-form').replaceChildren();modalEl('box-instructions-modal').hidden=true;modalEl('box-credentials-modal').hidden=true;modalEl('role-editor-modal').hidden=true;$('#app').hidden=true;$('#login').hidden=false;document.querySelectorAll('form').forEach(f=>f.reset());$('#profile-tree').replaceChildren();$('#profile-choices').replaceChildren();$('#error').textContent=''}));
$('#refresh').addEventListener('click',action(refresh));
function resetCreationForm(form){
 const pool=form.elements.pool.value;
 form.reset();createInstructionSource='';
 if([...form.elements.pool.options].some(option=>option.value===pool))form.elements.pool.value=pool;
 form.querySelectorAll('details').forEach(details=>details.open=false);
 void syncCreateInstructionText();
}
$('#create').addEventListener('submit',action(async e=>{const f=e.target.elements,profile=f.loginProfile?.value,profileRef=profile?JSON.parse(profile):null,loginProfiles=profileRef?[{...profileRef,model:f.agentModel.value.trim(),...(f.agentReasoningEffort.value?{reasoningEffort:f.agentReasoningEffort.value}:{})}]:[],github=f.githubProfile?.value,roleIds=[...$('#create-role-choices').querySelectorAll('input:checked')].map(input=>input.value),tools=['desktop',...[...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value)],setupScript=f.setupScript.value,d=f.pool.value?JSON.parse(f.pool.value):await chooseCreationPool(tools),instructions=await createInstructionSelection();if(profileRef&&!loginProfiles[0].model)throw Error('Choose a model');if(github)loginProfiles.push(JSON.parse(github));const createdName=f.name.value.trim(),created=await api('/v1/logical-boxes','POST',{name:createdName,defaultAgent:f.defaultAgent.value,diskGiB:Number(f.disk.value),provider:d.provider,providerCredential:d.providerCredential,allocateWhenReady:true,loginProfiles,roleIds,tools,...(setupScript.trim()?{setupScript}:{}),...(instructions?{instructions}:{})},{'Idempotency-Key':crypto.randomUUID()});if(created?.id)startingBoxes.add(created.id);resetCreationForm(e.target);try{await refresh();notice('Box '+createdName+' is starting.')}finally{document.activeElement?.blur();window.scrollTo(0,0)}}));
$('#provider').addEventListener('submit',action(async e=>{const f=e.target.elements,rev=f.revision.value,body={config:JSON.parse(f.config.value)};if(f.secret.value){body.secret=JSON.parse(f.secret.value);if(rev)body.replaceSecret=true}await api(pp(f.provider.value,f.alias.value),rev?'PATCH':'PUT',body,rev?{'If-Match':rev}:{});e.target.reset();await refresh()}));
$('#slots').addEventListener('submit',action(async e=>{const target=$('#capacity-pool').value?JSON.parse($('#capacity-pool').value):defaults;if(!target)throw Error('Choose a worker pool first');await api('/v1/fleet/slots','PUT',{...target,compute_box_slots:Number(e.target.elements.count.value)});await refresh()}));
$('#notification').addEventListener('submit',action(async e=>{const f=e.target.elements,split=s=>s.split(',').map(v=>v.trim()).filter(Boolean);await api('/v1/notifications/'+encodeURIComponent(f.kind.value)+'/'+encodeURIComponent(f.name.value),'PUT',{config:JSON.parse(f.config.value),secret:JSON.parse(f.secret.value),allowedUsers:split(f.users.value),allowedChats:split(f.chats.value)});e.target.reset();await refresh()}));

/* ---------- instruction presets, box instructions, imported profiles ---------- */
function modalEl(id){return document.getElementById(id)}
function renderMarkdownPreview(root,text){root.replaceChildren();root.append(typeof window.markdownToNodes==='function'?window.markdownToNodes(text||''):node('pre',text||''))}
async function presetBody(name){
 if(presetBodyCache.has(name))return presetBodyCache.get(name);
 const value=await api('/v1/instruction-presets/'+encodeURIComponent(name));
 presetBodyCache.set(name,value.preset.markdown);
 return value.preset.markdown;
}
function applyInstructionPresets(list){
 instructionPresets=list&&Array.isArray(list.presets)?list:{defaultName:'',presets:[]};
 presetBodyCache.clear();renderInstructionList();renderCreateInstructionChoice();
}
function renderInstructionList(){
 const root=$('#instruction-list');root.replaceChildren();
 if(!instructionPresets.presets.length){root.append(node('p','No instruction presets yet. Create one below to reuse Markdown guidance across boxes.'));return}
 const wrap=node('div');wrap.className='table-wrap';const table=document.createElement('table');table.className='markets';
 const head=node('tr');['Name','Revision','Size','Default','Updated','Actions'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const preset of instructionPresets.presets){
  const row=node('tr');row.className='row';
  row.append(node('td',preset.name),node('td','r'+preset.revision),node('td',preset.sizeBytes+' B'),node('td',preset.default?'yes':'no'),node('td',new Date(preset.updatedAt).toLocaleString()));
  const actions=node('td');
  if(ownerTools)actions.append(button('Edit',()=>void editInstructionPreset(preset.name)),button(preset.default?'Clear default':'Set default',()=>setInstructionDefault(preset.default?'':preset.name)),trashButton('Delete instruction preset '+preset.name,()=>deleteInstructionPreset(preset)));
  else actions.append(node('span','—'));
  row.append(actions);table.append(row);
 }
 wrap.append(table);root.append(wrap);
}
async function editInstructionPreset(name){
 const form=$('#instruction-form'),status=$('#instruction-status');
 try{const value=await api('/v1/instruction-presets/'+encodeURIComponent(name));form.elements.name.value=value.preset.name;form.elements.markdown.value=value.preset.markdown;$('#instruction-editor').open=true;renderInstructionPreview();status.textContent='Editing '+name+' (r'+value.preset.revision+'). Saving updates future selections only; existing boxes keep their snapshot.'}
 catch(e){status.textContent=e.message}
}
async function deleteInstructionPreset(preset){
 if(!confirm('Delete instruction preset "'+preset.name+'"? Boxes that already copied it keep their snapshot unchanged.'))return;
 const status=$('#instruction-status');
 try{await api('/v1/instruction-presets/'+encodeURIComponent(preset.name),'DELETE');status.textContent='Preset deleted. Existing boxes keep their snapshot.';await refresh()}
 catch(e){status.textContent=e.message}
}
async function setInstructionDefault(name){
 const status=$('#instruction-status');
 try{await api('/v1/instruction-presets-default','PUT',{name});status.textContent=name?'Account default instruction preset set: '+name+'.':'Account default instruction preset cleared.';await refresh()}
 catch(e){status.textContent=e.message}
}
function renderInstructionPreview(){const text=$('#instruction-form').elements.markdown.value,preview=$('#instruction-preview');renderMarkdownPreview(preview,text);preview.hidden=!text.trim()}
$('#instruction-preview-toggle').addEventListener('click',()=>{const preview=$('#instruction-preview');if(preview.hidden)renderInstructionPreview();preview.hidden=!preview.hidden});
$('#instruction-upload').addEventListener('click',()=>$('#instruction-file').click());
$('#instruction-file').addEventListener('change',action(async()=>{
 const input=$('#instruction-file'),file=input.files&&input.files[0];if(!file)return;
 const status=$('#instruction-status'),lower=file.name.toLowerCase(),typeOk=lower.endsWith('.md')||lower.endsWith('.markdown')||['text/markdown','text/plain'].includes(file.type);
 if(!typeOk)throw Error('Choose a Markdown file (.md or .markdown).');
 if(file.size>65536)throw Error('Markdown files are limited to 64 KiB.');
 let text;try{text=new TextDecoder('utf-8',{fatal:true}).decode(new Uint8Array(await file.arrayBuffer()))}catch{throw Error('The file must be valid UTF-8 text.')}
 if(text.includes('\u0000'))throw Error('The file must not contain NUL bytes.');
 const form=$('#instruction-form');form.elements.markdown.value=text;
 if(!form.elements.name.value)form.elements.name.value=file.name.replace(/\.(md|markdown)$/i,'').slice(0,64);
 $('#instruction-editor').open=true;renderInstructionPreview();status.textContent='Loaded '+file.name+'. Name the preset and save.';
 input.value='';
}));
// Preset problems belong beside the editor, not in the page-wide error banner.
$('#instruction-form').addEventListener('submit',action(async e=>{
 e.preventDefault();const f=e.target.elements,name=f.name.value.trim(),markdown=f.markdown.value,status=$('#instruction-status');
 status.textContent='';
 try{
  if(!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/.test(name))throw Error('Preset names use 1–64 letters, digits, dots, underscores or hyphens and start with a letter or digit.');
  if(markdown.length>65536)throw Error('Markdown is limited to 64 KiB.');
  if(!markdown.trim())throw Error('Enter the Markdown instructions to save.');
  const saved=await api('/v1/instruction-presets/'+encodeURIComponent(name),'PUT',{markdown});
  status.textContent='Saved '+name+' · r'+saved.preset.revision+' ('+saved.preset.sizeBytes+' bytes).';
  e.target.reset();$('#instruction-preview').hidden=true;await refresh();
 }catch(err){status.textContent=err.message}
}));
function renderCreateInstructionChoice(){
 const select=$('#create-instructions'),previous=select.value;select.replaceChildren();
 const auto=node('option',instructionPresets.defaultName?'Default · '+instructionPresets.defaultName:'Default / none');auto.value='auto';select.append(auto);
 const none=node('option','None');none.value='none';select.append(none);
 for(const preset of instructionPresets.presets){const option=node('option',preset.name+(preset.default?' · default':''));option.value=preset.name;select.append(option)}
 const custom=node('option','Custom Markdown');custom.value='custom';select.append(custom);
 if([...select.options].some(option=>option.value===previous))select.value=previous;
 void syncCreateInstructionText();
}
let createInstructionSource='';
async function syncCreateInstructionText(){
 const select=$('#create-instructions'),editor=$('#create-instructions-editor'),textarea=$('#create-instructions-custom'),preview=$('#create-instructions-preview');
 const value=select.value;
 if(value==='auto'||value==='none'){
  textarea.value='';textarea.readOnly=true;createInstructionSource=value;
  editor.hidden=true;editor.open=false;
  renderMarkdownPreview(preview,'');preview.hidden=true;return;
 }
 if(value==='custom'){
  textarea.readOnly=false;
  if(createInstructionSource!=='custom'){textarea.value='';createInstructionSource='custom';editor.open=true}
  editor.hidden=false;
  renderMarkdownPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();return;
 }
 let body='';try{body=await presetBody(value)}catch{body=''}
 textarea.readOnly=false;
 if(createInstructionSource!==value){textarea.value=body;createInstructionSource=value;editor.open=false}
 editor.hidden=false;
 renderMarkdownPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();
}
$('#create-instructions').addEventListener('change',()=>void syncCreateInstructionText());
$('#create-instructions-custom').addEventListener('input',()=>{const preview=$('#create-instructions-preview'),text=$('#create-instructions-custom').value;renderMarkdownPreview(preview,text);preview.hidden=!text.trim()});
async function createInstructionSelection(){
 const value=$('#create-instructions').value,markdown=$('#create-instructions-custom').value;
 if(value==='auto')return null;
 if(value==='none')return {none:true};
 if(value==='custom'){if(!markdown.trim())throw Error('Enter the custom instruction Markdown or choose another source.');return {markdown}}
 const body=await presetBody(value);
 return markdown.trim()&&markdown!==body?{preset:value,markdown}:{preset:value};
}
function describeBoxInstructions(state){
 const current=state.instructions||{},parts=['source: '+current.source];
 if(current.preset)parts.push('preset '+current.preset+' r'+current.presetRevision+(current.modified?' (edited for this box)':''));
 if(state.preset&&!state.preset.exists)parts.push('preset deleted; snapshot retained');
 else if(state.preset&&state.preset.stale)parts.push('preset has a newer revision');
 parts.push(state.pending?'pending apply':'applied to the box');
 return parts.join(' · ');
}
async function openBoxInstructions(box){
 boxInstructionTarget=box;
 const status=$('#box-instructions-status');status.textContent='Loading…';
 $('#box-instructions-title').textContent='Instructions · '+box.name;
 const select=$('#box-instructions-preset');select.replaceChildren();
 try{
  const state=await api(bp(box.id)+'/instructions'),current=state.instructions||{source:'none',markdown:''};
  const none=node('option','None (clear managed instructions)');none.value='';select.append(none);
  for(const preset of instructionPresets.presets){const option=node('option',preset.name+' · r'+preset.revision);option.value=preset.name;select.append(option)}
  const custom=node('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
  select.value=current.source==='preset'&&instructionPresets.presets.some(p=>p.name===current.preset)?current.preset:(current.source==='custom'?'custom':'');
  $('#box-instructions-markdown').value=current.markdown||'';
  renderMarkdownPreview($('#box-instructions-preview'),current.markdown||'');
  $('#box-instructions-current').textContent=describeBoxInstructions(state);
  status.textContent='';
  modalEl('box-instructions-modal').hidden=false;
 }catch(e){status.textContent=e.message}
}
$('#box-instructions-preset').addEventListener('change',action(async()=>{
 const select=$('#box-instructions-preset'),textarea=$('#box-instructions-markdown'),preview=$('#box-instructions-preview');
 if(select.value===''){textarea.value='';renderMarkdownPreview(preview,'');return}
 if(select.value==='custom'){renderMarkdownPreview(preview,textarea.value);return}
 try{textarea.value=await presetBody(select.value)}catch(e){$('#box-instructions-status').textContent=e.message;return}
 renderMarkdownPreview(preview,textarea.value);
}));
$('#box-instructions-markdown').addEventListener('input',()=>renderMarkdownPreview($('#box-instructions-preview'),$('#box-instructions-markdown').value));
$('#box-instructions-apply').addEventListener('click',action(async()=>{
 if(!boxInstructionTarget)return;
 const status=$('#box-instructions-status'),select=$('#box-instructions-preset'),markdown=$('#box-instructions-markdown').value;
 let body;
 if(select.value==='')body={none:true};
 else if(select.value==='custom'){if(!markdown.trim())throw Error('Enter the custom Markdown or choose another source.');body={markdown}}
 else{const preset=await presetBody(select.value);body=markdown.trim()&&markdown!==preset?{preset:select.value,markdown}:{preset:select.value}}
 status.textContent='Applying…';
 const result=await api(bp(boxInstructionTarget.id)+'/instructions','PUT',body);
 status.textContent=result.note||describeBoxInstructions(result);
 if(result.instructions)$('#box-instructions-current').textContent=describeBoxInstructions(result);
 await refresh();
}));
async function openBoxCredentials(box){
 boxCredentialTarget=box;
 const status=$('#box-credentials-status');status.textContent='Loading…';
 $('#box-credentials-title').textContent='Agent profile · '+box.name;
 try{
  const [state,profiles]=await Promise.all([api(bp(box.id)+'/imported-credentials'),api('/v1/login-profiles')]);
  const byApplication={};for(const profile of profiles)(byApplication[profile.application]??=[]).push(profile.name);
  const current=(state.profiles||[])[0];
  const wrap=$('#box-credentials-form');wrap.replaceChildren();
  const label=node('label','login profile ');label.className='field';const select=document.createElement('select');select.name='loginProfile';const empty=node('option','None');empty.value='';select.append(empty);
  for(const application of ['claude','codex','opencode']){const names=(byApplication[application]||[]).slice().sort();if(!names.length)continue;const group=document.createElement('optgroup');group.label=application;for(const name of names){const option=node('option',name);option.value=JSON.stringify({application,name});group.append(option)}select.append(group)}
  const currentValue=current&&JSON.stringify({application:current.application,name:current.name});if(currentValue&&[...select.options].some(option=>option.value===currentValue))select.value=currentValue;label.append(select);wrap.append(label);
  const parts=[(state.profiles||[]).length?'Imported: '+(state.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', '):'No imported login profiles recorded'];
  if((state.pending||[]).length)parts.push('Queued for next start: '+(state.pending||[]).map(ref=>ref.application+' · '+ref.name).join(', '));
  $('#box-credentials-current').textContent=parts.join(' · ')+'.';
  status.textContent='';
  modalEl('box-credentials-modal').hidden=false;
 }catch(e){status.textContent=e.message}
}
$('#box-credentials-apply').addEventListener('click',action(async()=>{
 if(!boxCredentialTarget)return;
 const status=$('#box-credentials-status'),profile=$('#box-credentials-form select')?.value,profiles=profile?[JSON.parse(profile)]:[];
 status.textContent='Applying profile and closing stale agent conversations…';
 const result=await api(bp(boxCredentialTarget.id)+'/login-profiles','PUT',{profiles});
 status.textContent=result.note||'Saved.';
 $('#box-credentials-current').textContent=(result.profiles||[]).length?'Imported: '+(result.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', ')+'.':'No imported login profiles recorded.';
 await refresh();
}));
document.querySelectorAll('[data-close]').forEach(element=>element.addEventListener('click',()=>{const card=element.closest('.modal');if(card)card.hidden=true}));
