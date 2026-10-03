'use strict';
const $=s=>document.querySelector(s);
const workspaceNav=window.VMBoxWorkspaceNav?.init({menuId:'manage-menu',panelId:'manage-menu-panel',usageId:'manage-usage',providersId:'manage-providers'});
let token='',defaults=null,epoch=0;
let boxRefreshTimer;
let fleetSnapshots=[];
let ownerTools=false,instructionPresets={defaultName:'',presets:[]};
let roleBoxes=[];
let listedProfiles=[],profileAccountName='';
const presetBodyCache=new Map();
const agentCLICatalogCache=new Map();
window.VMBoxAIHelper?.attach({input:$('#instruction-form textarea[name="markdown"]'),kind:'markdown',status:$('#instruction-status')});
$('#ai-settings-open').addEventListener('click',()=>window.VMBoxAIHelper.openSettings());
let boxInstructionTarget=null,boxCredentialTarget=null,boxCredentialState=null,boxCredentialOptions=[],boxCredentialRequest=0,boxCredentialBusy=false,boxCredentialRetryAction=null;
const poolKey=(provider,providerCredential)=>JSON.stringify({provider,providerCredential:providerCredential||''});
const poolLabel=(provider,alias)=>(provider==='shared-worker'?'Shared worker':'Dedicated · '+provider)+' / '+(alias||'default');
function boxPlacement(box){
 if(!box.slotId)return 'Unassigned';
 const fleet=fleetSnapshots.find(f=>f.provider===box.provider&&f.providerCredential===(box.providerCredential||''));
 const slot=fleet?.slots?.find(s=>s.id===box.slotId);
 const worker=box.provider==='shared-worker'?(box.providerCredential||'shared worker'):(slot?.serviceName||slot?.serviceId||box.slotId);
 return (box.provider==='shared-worker'?'Shared':'Dedicated')+' · slot '+(slot?.ordinal??box.slotId);
}
function boxPlacementFull(box){
 if(!box.slotId)return 'Unassigned';
 const fleet=fleetSnapshots.find(f=>f.provider===box.provider&&f.providerCredential===(box.providerCredential||''));
 const slot=fleet?.slots?.find(s=>s.id===box.slotId);
 const worker=box.provider==='shared-worker'?(box.providerCredential||'shared worker'):(slot?.serviceName||slot?.serviceId||box.slotId);
 return (box.provider==='shared-worker'?'Shared':'Dedicated')+' · '+worker+' · slot '+(slot?.ordinal??box.slotId);
}
function updateBoxPlacements(boxes){
 const byID=new Map(boxes.map(box=>[box.id,box]));
 for(const row of document.querySelectorAll('#box-list [data-box-id]')){const box=byID.get(row.dataset.boxId);if(box){const cell=row.querySelector('.box-placement'),text=boxPlacement(box);cell.textContent=text;cell.title=text}}
 if(selectedManagedBoxID)renderBoxDetail();
}
function renderPoolChoices(providers){
 for(const selector of ['#create-pool','#capacity-pool']){
  const select=$(selector),previous=select.value;select.replaceChildren();
  const fallback=node('option',selector==='#create-pool'?'Automatic (available capacity)':'Controller default');fallback.value='';select.append(fallback);
  for(const provider of providers){const option=node('option',poolLabel(provider.provider,provider.name));option.value=poolKey(provider.provider,provider.name);select.append(option)}
  if([...select.options].some(option=>option.value===previous))select.value=previous;
 }
 syncCreateMemorySettings();
}
function syncCreateMemorySettings(){
 let pool=null;try{pool=$('#create-pool').value?JSON.parse($('#create-pool').value):null}catch{}
 $('#create-memory-settings').hidden=pool?.provider!=='shared-worker';
 if(pool?.provider==='shared-worker')void applyWorkerBoxBounds(pool,$('#create').elements);
}
// Box size inputs follow the selected worker's machine and its default size.
async function applyWorkerBoxBounds(pool,fields){
 let view;try{view=await api('/v1/fleet/worker?'+new URLSearchParams(pool))}catch{return}
 const limits=view?.worker?.limits,defaults=view?.worker?.settings?.boxDefaults;
 if(!limits?.perBoxLimits||!fields.memoryGiB||!fields.swapGiB)return;
 fields.memoryGiB.max=String(limits.boxMax.memoryMiB/1024);fields.swapGiB.max=String(limits.boxMax.swapMiB/1024);
 if(!fields.memoryGiB.dataset.edited)fields.memoryGiB.value=String(defaults.memoryMiB/1024);
 if(!fields.swapGiB.dataset.edited)fields.swapGiB.value=String(defaults.swapMiB/1024);
}
for(const name of ['memoryGiB','swapGiB'])$('#create').elements[name]?.addEventListener('input',e=>{e.target.dataset.edited='1'});
$('#create-pool').addEventListener('change',syncCreateMemorySettings);
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
 if(!r.ok){let e;try{e=await r.json()}catch{}if(r.status===401&&$('#login').hidden){$('#app').hidden=true;$('#login').hidden=false;$('#login-error').textContent='Session expired. Log in again.';$('#login-token').focus();throw Error('Session expired. Log in again.')}throw Error(e?.error||'Request failed: '+r.status)}return r.status===204?null:r.json();
}
function action(fn){return async e=>{e?.preventDefault();$('#error').textContent='';if(!$('#login').hidden)$('#login-error').textContent='';try{await fn(e)}catch(err){if(!$('#login').hidden){$('#login-error').textContent=err.message;$('#login-token').focus()}else{$('#error').className='flash error';$('#error').textContent=err.message}}}}
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
const ICON_MORE='<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="19" cy="12" r="1.7"/></svg>';
function blobSVG(seed){
 return window.VBoxMascot?.miniSVG(seed)||'<svg viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="39" fill="var(--vb-mascot-fallback)"/><ellipse cx="37" cy="55" rx="4.5" ry="9.5" fill="var(--vb-white)"/><ellipse cx="63" cy="55" rx="4.5" ry="9.5" fill="var(--vb-white)"/></svg>';
}

const rowMascots=[];
function boxRowAvatar(box){
 const avatar=node('span');avatar.className='avatar row-avatar';avatar.setAttribute('aria-hidden','true');
 const screen=node('span');screen.className='row-screen';screen.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8m-4-4v4"/></svg>';
 if(box.state==='running'){
  const image=document.createElement('img');image.alt='';image.src='/v1/logical-boxes/'+encodeURIComponent(box.id)+'/desktop/screenshot';image.onerror=()=>image.remove();screen.append(image);
 }
 const host=node('span');host.className='avatar-mascot';
 if(window.VBoxMascot?.Mascot){
  const mascot=new window.VBoxMascot.Mascot(host,box.id);rowMascots.push(mascot);
  if(box.state==='hibernated')mascot.jump('sleeping','sleeping','sleeping');
  else if(box.state==='failed')mascot.jump('angry','error','failed');
  else if(box.state!=='running')mascot.jump('waking','surprised','starting');
 }else host.innerHTML='<svg viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="39" fill="var(--vb-mascot-fallback)"/><ellipse cx="37" cy="55" rx="4.5" ry="9.5" fill="var(--vb-white)"/><ellipse cx="63" cy="55" rx="4.5" ry="9.5" fill="var(--vb-white)"/></svg>';
 avatar.append(screen,host);return avatar;
}
function trashButton(label,fn){const b=node('button');b.type='button';b.className='linkbtn danger';b.setAttribute('aria-label',label);b.title=label;b.innerHTML=TRASH_ICON;b.addEventListener('click',action(fn));return b}
function renderCreationProfileChoices(root,profiles,agentSelect,selected=''){
 root._modelPicker?.destroy();
 root.replaceChildren();
 const profileLabel=node('label','Login profile ');profileLabel.className='field';
 const profileSelect=document.createElement('select');profileSelect.name='loginProfile';profileLabel.append(profileSelect);
 const modelLabel=node('label','model ');modelLabel.className='field';
 const modelInput=document.createElement('input');modelInput.name='agentModel';modelInput.maxLength=200;modelLabel.append(modelInput);const modelPicker=window.VMBoxModelPicker.create(modelInput);root._modelPicker=modelPicker;
 const githubLabel=node('label','GitHub profile ');githubLabel.className='field';
 const githubSelect=document.createElement('select');githubSelect.name='githubProfile';githubSelect.append(node('option','None'));githubSelect.options[0].value='';githubLabel.append(githubSelect);
 for(const profile of profiles.filter(profile=>profile.application==='github')){const option=node('option',profile.name);option.value=JSON.stringify({application:'github',name:profile.name});githubSelect.append(option)}
 githubLabel.hidden=githubSelect.options.length===1;
 const noneNote=node('p','Starts without an agent login; connect one later.');noneNote.className='hint login-none-note';
 root.append(profileLabel,modelLabel,githubLabel,noneNote);
 const populate=()=>{
  const app=agentSelect.value,previous=profileSelect.value||selected;profileSelect.replaceChildren();
  const empty=node('option','None');empty.value='';profileSelect.append(empty);
  const choices=profiles.filter(profile=>profile.application===app);
  for(const profile of choices){const option=node('option',profile.name);option.value=JSON.stringify({application:profile.application,name:profile.name});option.dataset.model=profile.model||'';profileSelect.append(option)}modelPicker.setApplication(app);
  if([...profileSelect.options].some(option=>option.value===previous))profileSelect.value=previous;
  profileLabel.hidden=app==='shell'||choices.length===0;
  noneNote.hidden=profileLabel.hidden;
  root.hidden=profileLabel.hidden&&githubLabel.hidden;
  syncModel();
 };
 const syncModel=()=>{const option=profileSelect.selectedOptions[0],hasProfile=!!profileSelect.value;modelInput.disabled=!hasProfile;modelPicker.setValue(hasProfile?option?.dataset.model||'':'');modelPicker.setReasoningEffort('');modelPicker.setOptions(window.VMBoxModelPicker.optionsFor(agentSelect.value,[option?.dataset.model]));modelLabel.hidden=!hasProfile;const ref=hasProfile?JSON.parse(profileSelect.value):null;modelPicker.setLoader(ref&&['claude','codex','opencode'].includes(ref.application)?()=>api('/v1/login-profiles/'+encodeURIComponent(ref.application)+'/'+encodeURIComponent(ref.name)+'/models'):null)};
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
async function restoreLogin(){try{await api('/v1/browser-session');await refresh();$('#login').hidden=true;$('#app').hidden=false;openProfileFromLink()}catch{}}
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
  const slotGrid=node('div');slotGrid.className='slot-grid';
  for(const slot of fleet.slots||[]){
   const card=node('article');card.className='slot-card';card.dataset.state=slot.state;
   const dot=node('span');dot.className='slot-dot';dot.setAttribute('aria-hidden','true');
   const title=node('strong','Slot '+slot.ordinal);
   const worker=node('small',fleet.provider==='shared-worker'?(fleet.providerCredential||'shared worker'):(slot.serviceName||slot.serviceId||slot.id||'—'));
   const occupant=node('span',slot.logicalBoxName||(slot.state==='free'?'Available':'—'));occupant.className='slot-box';
   const meta=node('em',[slot.state,slot.health,slot.region].filter(Boolean).join(' · '));
   card.append(dot,title,worker,occupant,meta);slotGrid.append(card);
  }
  if(!(fleet.slots||[]).length)slotGrid.append(node('p','No slots reported for this pool.'));
  root.append(slotGrid);
  const detached=fleet.detachedLogicalBoxes||[];if(detached.length)root.append(node('h3','Detached workspaces'),dataTable(['Box','State'],detached.map(box=>[box.name,box.state])));
 }
 const target=$('#capacity-pool').value?JSON.parse($('#capacity-pool').value):defaults;
 const selected=target&&fleetSnapshots.find(fleet=>fleet.provider===target.provider&&fleet.providerCredential===(target.providerCredential||''));
 const input=$('#slots input');if(document.activeElement!==input)input.value=selected?.desiredSlots??'';
 root.append(rawDetails(fleetSnapshots));
}
$('#capacity-pool').addEventListener('change',renderWorkerCapacity);
function renderNotifications(values){const root=$('#destinations');root.replaceChildren();if(!values.length){const empty=node('div');empty.className='notice-empty';empty.innerHTML='<svg class="notice-empty-mascot" viewBox="0 0 100 104" aria-hidden="true"><g fill="var(--vb-brand-coral)"><circle cx="50" cy="52" r="38"/></g><g fill="none" stroke="var(--vb-white)" stroke-width="7.5" stroke-linecap="round"><path d="M38 33 L41 45"/><path d="M59 33 L62 45"/></g></svg>';empty.append(node('p','No notification destinations configured. Notifications are optional.'));root.append(empty);return}
 const list=node('div');list.className='notice-list';
 for(const n of values){
  const row=node('article');row.className='notice-row';row.dataset.enabled=String(!!n.enabled);
  const dot=node('span');dot.className='slot-dot';dot.setAttribute('aria-hidden','true');
  const text=node('div');text.className='notice-text';
  const title=node('strong',n.name);const meta=node('small',(n.kind||'webhook')+' · '+((n.allowedUsers||[]).join(', ')||'everyone')+' · '+((n.allowedChats||[]).join(', ')||'all chats'));
  text.append(title,meta);
  const badge=node('span',n.enabled?'Enabled':'Disabled');badge.className='notice-badge';
  row.append(dot,text,badge);list.append(row);
 }
 root.append(list,rawDetails(values))}
let providerTypes=[];
function renderProviderTypes(schema){
 providerTypes=schema?.types||[];
 const form=$('#provider'),select=form.elements.provider,previous=select.value;
 select.replaceChildren(...providerTypes.map(type=>{const option=node('option',type.label);option.value=type.name;return option}));
 if(providerTypes.some(type=>type.name===previous))select.value=previous;
 if(!form.elements.revision.value)renderProviderFields();
}
function renderProviderFields(existing){
 const form=$('#provider'),type=providerTypes.find(item=>item.name===form.elements.provider.value),root=$('#provider-fields'),editing=!!existing;
 root.replaceChildren();$('#provider-type-help').textContent=type?.help||'';
 for(const field of type?.fields||[]){
  const label=node('label');label.className='field'+(field.type==='boolean'?' provider-check':'');
  let input;
  if(field.type==='select'){input=node('select');const blank=node('option','Default');blank.value='';input.append(blank,...field.options.map(value=>{const option=node('option',value);option.value=value;return option}))}
  else{input=node('input');input.type=field.type==='boolean'?'checkbox':field.secret?'password':'text';input.spellcheck=false}
  input.name=(field.secret?'secret:':'config:')+field.name;input.autocomplete='off';
  const value=existing?.config?.[field.name];
  if(field.type==='boolean')input.checked=value===true;else if(value!==undefined&&value!==null)input.value=String(value);
  if(field.placeholder)input.placeholder=field.placeholder;
  if(field.secret&&editing)input.placeholder='Leave blank to keep the saved value';
  else if(field.required)input.required=true;
  // Target fields are immutable; a different target is a new provider.
  if(editing&&!field.mutable&&!field.secret){input.disabled=true;label.title='Fixed after creation. Add a new provider for a different target.'}
  label.append(document.createTextNode(field.label+(input.required?' *':'')),input);
  if(field.help){const hint=node('small',field.help);hint.className='hint';label.append(hint)}
  root.append(label);
 }
}
let openProviderKey='';
function editProvider(provider){
 const form=$('#provider'),f=form.elements;
 f.provider.value=provider.provider;f.alias.value=provider.name;f.revision.value=provider.updatedAt;f.provider.disabled=true;f.alias.readOnly=true;
 renderProviderFields(provider);$('#provider-editor-title').textContent='Connection';$('#provider-form-status').textContent='';
}
function resetProviderForm(){
 const form=$('#provider'),f=form.elements;form.reset();f.revision.value='';f.provider.disabled=false;f.alias.readOnly=false;
 if(providerTypes.length&&!f.provider.value)f.provider.value=providerTypes[0].name;
 $('#provider-editor-title').textContent='Add provider';$('#provider-form-status').textContent='';renderProviderFields();
}
// The add/edit form is one element: it lives under the list for adding and
// moves into the open provider's panel for editing that provider's connection.
function homeProviderEditor(){const editor=$('#provider-editor');$('#provider-editor-home').append(editor);editor.classList.remove('in-panel');return editor}
function closeProviderPanel(){openProviderKey='';resetProviderForm();homeProviderEditor().open=false}
$('#provider').elements.provider.addEventListener('change',()=>renderProviderFields());
$('#provider-cancel').addEventListener('click',()=>{if(openProviderKey){const provider=listedProviders.find(item=>poolKey(item.provider,item.name)===openProviderKey);if(provider)editProvider(provider);return}resetProviderForm();$('#provider-editor').open=false});
$('#provider-add').addEventListener('click',()=>{closeProviderPanel();renderProviders(listedProviders);const editor=$('#provider-editor');editor.open=true;editor.scrollIntoView({block:'nearest'});$('#provider').elements.alias.focus()});
let listedProviders=[];
const gibText=bytes=>{const value=bytes/(1024**3);return (value>=10?Math.round(value):value.toFixed(1))+' GiB'};
function renderProviders(providers){
 listedProviders=providers;
 const editor=homeProviderEditor(),root=$('#provider-list');root.replaceChildren();
 if(!providers.length){root.append(node('p','No providers configured. Add one below, validate it, then select it as the default.'));return}
 if(openProviderKey&&!providers.some(provider=>poolKey(provider.provider,provider.name)===openProviderKey))closeProviderPanel();
 const {table,rows}=workspaceNav.providerTable(true);root.append(table);
 const columns=table.querySelectorAll('thead th').length;
 for(const provider of providers){
  const key=poolKey(provider.provider,provider.name),open=key===openProviderKey;
  const fleet=fleetSnapshots.find(item=>item.provider===provider.provider&&item.providerCredential===(provider.name||''));
  const isDefault=defaults?.provider===provider.provider&&defaults?.providerCredential===provider.name;
  const row=node('tr');row.className='provider-row'+(open?' is-open':'');rows.append(row);
  const snapshot=fleet?fleet.providerSnapshot:{fleet:null,config:null,host:null,hostError:'',errors:[]};
  workspaceNav.renderProviderRow(row,provider,isDefault,snapshot,cell=>{
   const actions=node('div');actions.className='provider-actions';
   const manage=node('button',open?'Close':'Manage');manage.type='button';manage.className=open?'':'primary';manage.setAttribute('aria-expanded',String(open));manage.setAttribute('aria-controls','provider-panel-'+provider.provider+'-'+provider.name);
   manage.addEventListener('click',()=>{if(open)closeProviderPanel();else{openProviderKey=key;editProvider(provider)}renderProviders(listedProviders);if(!open)document.getElementById('provider-panel-'+provider.provider+'-'+provider.name)?.scrollIntoView({block:'nearest'})});
   actions.append(manage);cell.append(actions);
  });
  if(!open)continue;
  const detail=node('tr');detail.className='provider-detail-row';const cell=node('td');cell.colSpan=columns;detail.append(cell);rows.append(detail);
  cell.append(providerPanel(provider,fleet,isDefault,editor));
 }
}
function providerPanel(provider,fleet,isDefault,editor){
 const panel=node('div');panel.className='provider-panel';panel.id='provider-panel-'+provider.provider+'-'+provider.name;
 const section=(title,hint)=>{const group=node('section');group.className='provider-panel-section';const heading=node('h3',title);group.append(heading);if(hint){const small=node('p',hint);small.className='hint';group.append(small)}return group};
 // Capacity: one slot number, bounded by the worker's machine when it reports one.
 const capacity=section('Slots and box size');
 const settings=node('div');settings.className='provider-capacity-settings';capacity.append(settings);
 if(provider.provider==='shared-worker'){
  settings.append(node('p','Loading worker settings…'));
  const load=async()=>{
   let view;try{view=await api('/v1/fleet/worker?'+new URLSearchParams(workerPoolTarget(provider)))}catch(err){view={error:err.message}}
   if(!settings.isConnected)return;
   if(view.supported&&view.worker)renderWorkerSettings(settings,provider,view,load);
   else{settings.replaceChildren();if(view.error||view.reason){const note=node('p',view.error?'Worker settings unavailable: '+view.error:'This worker reports no machine limits ('+view.reason+'). Set desired slots below.');note.className='worker-settings-note';settings.append(note)}if(!view.error)settings.append(desiredSlotsForm(provider,fleet))}
  };
  void load();
 }else settings.append(desiredSlotsForm(provider,fleet));
 capacity.append(slotGrid(fleet));
 // Connection: the shared add/edit form, prefilled for this provider.
 const connection=section('Connection','Saved secrets are never shown; leave a secret blank to keep it.');
 editor.classList.add('in-panel');editor.open=true;connection.append(editor);
 const known=new Set((providerTypes.find(type=>type.name===provider.provider)?.fields||[]).map(field=>field.name));
 const other=Object.entries(provider.config||{}).filter(([key])=>!known.has(key));
 if(other.length){const extra=node('details');extra.className='provider-other-settings';extra.append(node('summary','Other saved settings'),dataTable(['Setting','Value'],other.map(([key,value])=>[key,typeof value==='object'?JSON.stringify(value):String(value)])));connection.append(extra)}
 // Pool actions.
 const pool=section('Pool');
 const status=node('p');status.className='provider-panel-status';status.setAttribute('role','status');
 const actions=node('div');actions.className='provider-panel-actions';
 actions.append(button('Validate',async()=>{status.textContent='Validating…';const result=await api(pp(provider.provider,provider.name)+'/validate','POST',{});status.textContent=(result.valid?'Validation passed. ':'Validation failed. ')+'Checked: '+(result.checked||[]).join(', ')+'. Not checked: '+(result.unchecked||[]).join(', ')}));
 if(isDefault){const badge=node('span','Default pool for new boxes');badge.className='provider-default-note';actions.append(badge)}
 else actions.append(button('Use as default',async()=>{await api('/v1/controller-defaults','PUT',{provider:provider.provider,providerCredential:provider.name});await refresh()}));
 actions.append(button('Refresh usage',async()=>{const fresh=await workspaceNav.providerSnapshot(provider);const target=fleetSnapshots.find(item=>item.provider===provider.provider&&item.providerCredential===(provider.name||''));if(target){Object.assign(target,fresh.fleet||{},{providerSnapshot:fresh,hostResources:fresh.host,hostError:fresh.hostError});renderProviders(listedProviders)}}));
 const remove=button('Delete…',()=>workspaceNav.openProviderDelete(provider,refresh));remove.classList.add('provider-menu-delete');actions.append(remove);
 pool.append(actions,status);
 panel.append(capacity,connection,pool);
 return panel;
}
function desiredSlotsForm(provider,fleet){
 const form=node('form');form.className='worker-settings-form provider-desired-slots';
 const field=node('label');field.className='field';const input=node('input');input.type='number';input.name='count';input.min='0';input.required=true;input.value=String(fleet?.desiredSlots??'');
 field.append(document.createTextNode('Desired slots'),input);const hint=node('small','Raising may incur provider charges. Lowering drains free slots; occupied slots are not stopped.');hint.className='hint';field.append(hint);
 const save=node('button','Save slots');save.className='primary';const formActions=node('div');formActions.className='form-actions';formActions.append(save);
 form.append(field,formActions);
 form.addEventListener('submit',action(async()=>{save.disabled=true;try{await api('/v1/fleet/slots','PUT',{...workerPoolTarget(provider),compute_box_slots:Number(input.value)});notice('Desired slots for '+provider.name+' saved.');await refresh()}finally{save.disabled=false}}));
 return form;
}
function slotGrid(fleet){
 const grid=node('div');grid.className='slot-grid';
 if(fleet?.error){grid.append(node('p','Capacity unavailable: '+fleet.error));return grid}
 for(const slot of fleet?.slots||[]){
  const card=node('article');card.className='slot-card';card.dataset.state=slot.state;
  const dot=node('span');dot.className='slot-dot';dot.setAttribute('aria-hidden','true');
  const occupant=node('span',slot.logicalBoxName||(slot.state==='free'?'Available':'—'));occupant.className='slot-box';
  card.append(dot,node('strong','Slot '+slot.ordinal),occupant,node('em',[slot.state,slot.health,slot.region].filter(Boolean).join(' · ')));grid.append(card);
 }
 if(!(fleet?.slots||[]).length)grid.append(node('p','No slots reported for this pool.'));
 return grid;
}
function workerPoolTarget(provider){return {provider:provider.provider,providerCredential:provider.name||''}}
function renderWorkerSettings(body,provider,view,reload){
 body.replaceChildren();
 const {settings,specs,limits}=view.worker;
 const facts=[(specs.cpus%1?specs.cpus.toFixed(1):specs.cpus)+' CPU'+(specs.cpus===1?'':'s'),gibText(specs.memoryBytes)+' RAM',specs.swapBytes?gibText(specs.swapBytes)+' swap':'no swap'];
 if(specs.diskTotalBytes)facts.push(gibText(specs.diskFreeBytes)+' free of '+gibText(specs.diskTotalBytes)+' disk');
 if(specs.isolationTier)facts.push(specs.isolationTier+' isolation');
 const machine=node('p','Machine: '+facts.join(' · '));machine.className='worker-specs';machine.title='Machine specs reported by the worker';
 const form=node('form');form.className='worker-settings-form';
 const numberField=(label,name,value,min,max,step,hint)=>{const field=node('label');field.className='field';const input=node('input');input.type='number';input.name=name;input.min=String(min);input.max=String(max);input.step=String(step);input.value=String(value);input.required=true;field.append(document.createTextNode(label),input);if(hint){const small=node('small',hint);small.className='hint';field.append(small)}return field};
 const used=view.worker.occupiedSlots===1?'1 holds a box':view.worker.occupiedSlots+' hold boxes';
 const slotsRow=node('div');slotsRow.className='worker-slots-row';
 slotsRow.append(numberField('Slots','slots',settings.slots,Math.max(1,view.worker.occupiedSlots),limits.maxSlots,1,'Up to '+limits.maxSlots+' on this machine · '+used+'.'));
 form.append(slotsRow);
 if(limits.perBoxLimits){
  const sizes=node('fieldset');sizes.className='worker-box-size';sizes.append(node('legend','New box size'));
  sizes.append(numberField('CPU','cpu',settings.boxDefaults.cpu,limits.boxMin.cpu,limits.boxMax.cpu,limits.cpuStep,'Max '+limits.boxMax.cpu),
   numberField('RAM GiB','memory',settings.boxDefaults.memoryMiB/1024,limits.boxMin.memoryMiB/1024,limits.boxMax.memoryMiB/1024,1,'Max '+limits.boxMax.memoryMiB/1024),
   numberField('Swap GiB','swap',settings.boxDefaults.swapMiB/1024,0,limits.boxMax.swapMiB/1024,1,limits.boxMax.swapMiB?'Max '+limits.boxMax.swapMiB/1024:'No swap on this machine'));
  form.append(sizes);
 }
 const overcommit=node('p');overcommit.className='worker-overcommit';overcommit.setAttribute('role','note');
 const status=node('p');status.setAttribute('role','status');
 const save=node('button','Save slots and box size');save.className='primary';
 const actions=node('div');actions.className='form-actions';actions.append(save);
 form.append(overcommit,actions,status);
 const syncOvercommit=()=>{
  const slots=Number(form.elements.slots.value)||0,memory=limits.perBoxLimits?Number(form.elements.memory.value)||0:0,total=slots*memory;
  overcommit.hidden=!(limits.perBoxLimits&&total*1024**3>specs.memoryBytes);
  overcommit.textContent=slots+' slots × '+memory+' GiB = '+total+' GiB, more than this machine\'s '+gibText(specs.memoryBytes)+' RAM. That is fine while boxes stay light; under load they compete for memory and swap.';
 };
 form.addEventListener('input',syncOvercommit);syncOvercommit();
 form.addEventListener('submit',action(async event=>{
  const f=event.target.elements,request={...workerPoolTarget(provider),revision:settings.revision,slots:Number(f.slots.value)};
  if(limits.perBoxLimits)request.boxDefaults={cpu:Number(f.cpu.value),memoryMiB:Number(f.memory.value)*1024,swapMiB:Number(f.swap.value)*1024};
  save.disabled=true;status.textContent='Saving…';
  try{await api('/v1/fleet/worker','PUT',request);notice('Slots and box size for '+provider.name+' saved. The worker applied them without a restart.');await refresh()}
  catch(err){status.textContent=err.message;save.disabled=false;if(/reload/i.test(err.message))void reload()}
 }));
 body.append(machine,form);
}
const bp=id=>'/v1/logical-boxes/'+encodeURIComponent(id),pp=(p,n)=>'/v1/provider-credentials/'+encodeURIComponent(p)+'/'+encodeURIComponent(n);
let listedBoxes=[],selectedManagedBoxID='',boxDetailTrigger=null,boxDetailInstructions=null,boxDetailInstructionsRequest=0;
function closeBoxDetail(){selectedManagedBoxID='';boxDetailInstructions=null;boxDetailInstructionsRequest++;$('#box-detail').hidden=true;$('#box-detail-backdrop').hidden=true;boxDetailTrigger?.focus();boxDetailTrigger=null}
async function loadBoxDetailInstructions(id){
 const request=++boxDetailInstructionsRequest;
 try{
  const state=await api(bp(id)+'/instructions');
  if(selectedManagedBoxID===id&&boxDetailInstructionsRequest===request){boxDetailInstructions=state;renderBoxDetail()}
 }catch{
  if(selectedManagedBoxID===id&&boxDetailInstructionsRequest===request){boxDetailInstructions={error:true};renderBoxDetail()}
 }
}
function boxDetailSyncLabel(){
 if(!boxDetailInstructions)return 'Loading…';
 if(boxDetailInstructions.error)return 'Unavailable';
 const appliedAt=boxDetailInstructions.instructions?.appliedAt;
 const date=appliedAt?new Date(appliedAt):null;
 const last=date&&!Number.isNaN(date.getTime())?date.toLocaleString():'Never';
 return last+(boxDetailInstructions.pending?' · changes pending':'');
}
function renderBoxDetail(){
 const box=listedBoxes.find(item=>item.id===selectedManagedBoxID);if(!box){closeBoxDetail();return}
 $('#box-detail-title').textContent=box.name;
 const root=$('#box-detail-body');root.replaceChildren();
 const state=node('span',box.state);state.className='box-detail-state';state.dataset.state=box.state;
 const intro=node('div');intro.className='box-detail-intro';intro.append(state,node('span',box.defaultAgent||'shell'));
 const facts=node('dl');facts.className='box-detail-facts';
 for(const [label,value] of [['Worker / slot',boxPlacementFull(box)],['Provider',box.provider||'—'],['Pool',box.providerCredential||'default'],['Slot ID',box.slotId||'Unassigned'],['Last instructions sync',boxDetailSyncLabel()],['Workspace volume',box.volumeName||box.volumeId||'—'],['Box ID',box.id]]){const row=node('div');row.append(node('dt',label),node('dd',value));facts.append(row)}
 if(box.failureReason){const error=node('p',box.failureReason);error.className='box-detail-error';root.append(error)}
 const actions=node('div');actions.className='box-detail-actions';
 const workspace=node('a','Open workspace');workspace.href='/boxes/'+encodeURIComponent(box.id);
 const chat=node('a','Open chat');chat.href='/chat#box='+encodeURIComponent(box.id);
 actions.append(workspace,chat,button('Permissions',()=>openBoxPolicyEditor(box)),button('Instructions',()=>openBoxInstructions(box)));
 if(ownerTools)actions.append(button('Credentials',()=>openBoxCredentials(box)));
 if(boxPhase(box.state)==='running')actions.append(button('Restart…',()=>restartBox(box)));
 if(['stopped','failed'].includes(boxPhase(box.state)))actions.append(button('Resume',()=>{const row=document.querySelector('#box-list [data-box-id="'+CSS.escape(box.id)+'"]');row?.querySelector('[aria-label^="Resume box "]')?.click()}));
 root.append(intro,facts,node('h3','Manage box'),actions);
 if(ownerTools&&window.VMBoxIdlePolicy){const idle=node('div');root.append(idle);window.VMBoxIdlePolicy.mount(idle,{boxId:box.id,boxName:box.name,request:seconds=>api(bp(box.id)+'/idle-policy',seconds===undefined?'GET':'PUT',seconds===undefined?undefined:{seconds})})}
 if(ownerTools&&window.VMBoxRunBudgetPolicy){const budget=node('div');root.append(budget);window.VMBoxRunBudgetPolicy.mount(budget,{boxId:box.id,
  request:seconds=>api(bp(box.id)+'/run-budget-policy',seconds===undefined?'GET':'PUT',seconds===undefined?undefined:{seconds}),
  adjust:(action,seconds,expectedDeadlineAt)=>api(bp(box.id)+'/run-budget-policy/adjust','POST',{action,seconds,expectedDeadlineAt})})}
 if(ownerTools&&window.VMBoxCreateLimit){const limit=node('div');root.append(limit);window.VMBoxCreateLimit.mount(limit,{boxId:box.id,request:body=>api(bp(box.id)+'/agent-policy',body?'PUT':'GET',body)})}
}
function openBoxDetail(box,trigger){selectedManagedBoxID=box.id;boxDetailInstructions=null;boxDetailTrigger=trigger;renderBoxDetail();$('#box-detail').hidden=false;$('#box-detail-backdrop').hidden=false;$('#box-detail-close').focus();void loadBoxDetailInstructions(box.id)}
$('#box-detail-close').onclick=closeBoxDetail;$('#box-detail-backdrop').onclick=closeBoxDetail;
document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!$('#box-detail').hidden&&!document.querySelector('.modal:not([hidden])'))closeBoxDetail()});
function manageView(){
 const section=location.hash.slice(1);
 const pageNames={boxes:'boxes',providers:'providers',profiles:'profiles',roles:'permissions',instructions:'instructions',fleet:'capacity',notifications:'notifications'};
 const page=pageNames[section]||'manage';
 const view=pageNames[section]?section:'all';
 document.body.dataset.manageView=view;
 $('#manage-page-label').textContent=page;
  document.title='vbox / '+page;
 document.querySelectorAll('.workspace-links a,.manage-subnav a').forEach(link=>{
  if(link.getAttribute('href')==='#'+section&&section)link.setAttribute('aria-current','page');
  else link.removeAttribute('aria-current');
 });
}
addEventListener('hashchange',manageView);manageView();
$('#manage-account')?.addEventListener('click',()=>$('#manage-menu')?.click());
function renderBoxes(boxes){
 for(const mascot of rowMascots)mascot.destroy();rowMascots.length=0;
 listedBoxes=boxes;
 clearTimeout(boxRefreshTimer);
 for(const id of startingBoxes){const box=boxes.find(b=>b.id===id);if(!box||box.state==='running'||box.state==='failed'||box.state==='deleting'||box.state==='hibernated'&&box.failureReason)startingBoxes.delete(id)}
 const wrap=node('div');wrap.className='table-wrap';
 const table=document.createElement('table');table.className='markets';const head=document.createElement('tr');['Name','State','Worker / slot','Default agent','Permissions','CLI','Actions'].forEach(t=>head.append(node('th',t)));table.append(head);
 const query=$('#box-search').value.trim().toLocaleLowerCase();
 for(const b of boxes.filter(box=>!query||box.name.toLocaleLowerCase().includes(query))){
  const row=document.createElement('tr');row.className='row';const cell=document.createElement('td'),select=document.createElement('select'),status=node('td',b.state),actions=document.createElement('td');row.dataset.boxId=b.id;
  for(const agent of ['claude','codex','opencode','shell']){const o=node('option',agent);o.value=agent;select.append(o)}select.value=b.defaultAgent;select.disabled=b.state==='deleting'||deletingBoxes.has(b.id);
  select.addEventListener('change',action(()=>api(bp(b.id),'PATCH',{defaultAgent:select.value})));cell.append(select);
  const name=node('td',''),link=node(b.state==='deleting'?'span':'a',b.name);link.className='table-text';link.title=b.name;if(b.state!=='deleting')link.href='/boxes/'+encodeURIComponent(b.id);name.append(link);
  name.prepend(boxRowAvatar(b));
  status.replaceChildren(tableText(startingBoxes.has(b.id)&&b.state!=='running'?'starting':b.state));
  status.dataset.state=startingBoxes.has(b.id)&&b.state!=='running'?'starting':b.state;
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
  const details=button('Details',event=>openBoxDetail(b,event.currentTarget));details.classList.add('box-details-action');details.setAttribute('aria-label','Details for box '+b.name);actions.prepend(details);
  const manage=button('Manage…',()=>openBoxPolicyEditor(b));manage.setAttribute('aria-label','Manage permissions for box '+b.name);actions.append(manage);
  {
   const others=[...actions.children].filter(el=>el!==details);
   if(others.length){
    const overflow=node('div');overflow.className='row-overflow';
    const trigger=button('',()=>{const open=overflow.classList.toggle('open');trigger.setAttribute('aria-expanded',String(open))});
    trigger.className='row-overflow-trigger';trigger.setAttribute('aria-haspopup','menu');trigger.setAttribute('aria-expanded','false');trigger.setAttribute('aria-label','More actions for '+b.name);trigger.title='More actions';trigger.innerHTML=ICON_MORE;
    const menu=node('div');menu.className='row-overflow-menu';menu.setAttribute('role','menu');
    for(const el of others)menu.append(el);
    menu.addEventListener('click',()=>overflow.classList.remove('open'));
    overflow.append(trigger,menu);actions.replaceChildren(details,overflow);
   }
  }
  const placement=node('td'),placementText=tableText(boxPlacement(b));placementText.classList.add('box-placement');placement.append(placementText);const cli=node('td');cli.append(tableText('vbox '+JSON.stringify(b.name)));const permissions=node('td');row.append(name,status,placement,cell,permissions,cli,actions);table.append(row);
 }wrap.append(table);$('#box-list').replaceChildren(wrap);
 if(selectedManagedBoxID)renderBoxDetail();
 if(startingBoxes.size||boxes.some(b=>TRANSIENT_STATES.has(b.state))){const version=epoch;boxRefreshTimer=setTimeout(async()=>{try{const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}catch(err){if(version===epoch)$('#error').textContent='Could not check box progress. Use Refresh to retry. '+err.message}},5000)}
}
$('#box-search').addEventListener('input',()=>renderBoxes(listedBoxes));
document.addEventListener('click',event=>{if(event.target.closest('.row-overflow'))return;document.querySelectorAll('.row-overflow.open').forEach(el=>el.classList.remove('open'))});
function renderPermissionBoxes(boxes){
 roleBoxes=boxes||[];const root=$('#role-assignments'),query=$('#role-box-search').value.trim().toLowerCase(),visible=roleBoxes.filter(box=>!query||box.name.toLowerCase().includes(query));root.replaceChildren();
 if(!visible.length){root.append(node('p',query?'No boxes match this search.':'Create a box to configure agent permissions.'));return}
 const list=node('div');list.className='role-assignment-list';
 for(const box of visible){
  const card=node('article');card.className='role-assignment-card';card.dataset.state=box.state;card.dataset.roleBoxId=box.id;
  const summary=node('div');summary.className='role-assignment-summary';
  const mark=node('span');mark.className='role-box-mark';mark.setAttribute('aria-hidden','true');mark.innerHTML='<span class="role-box-screen"></span><span class="role-box-mascot">'+blobSVG(box.id)+'</span>';
  const identity=node('div');identity.className='role-assignment-identity';identity.append(node('strong',box.name));const meta=node('div');meta.className='role-box-meta';const agent=node('span',box.defaultAgent||'agent');agent.className='role-agent-badge';const state=node('span',box.state);state.className='role-state-badge';meta.append(agent,state);identity.append(meta);
  const current=node('div');current.className='role-assignment-current';current.append(node('span','Direct MCP permissions and contacts'));
  const actions=node('div');actions.className='role-assignment-actions';const manage=button('Edit permissions',()=>openBoxPolicyEditor(box));manage.classList.add('role-assignment-toggle','vb-secondary');const more=node('details');more.className='role-box-more';const moreSummary=node('summary');moreSummary.setAttribute('aria-label','More for '+box.name);moreSummary.innerHTML=ICON_MORE;const contacts=node('a','Manage contacts');contacts.href='/chat#box='+encodeURIComponent(box.id);more.append(moreSummary,contacts);actions.append(manage,more);
  summary.append(mark,identity,current,actions);card.append(summary);list.append(card);
 }
 root.append(list);
}
function populatePolicyEditor(box,cap={}){const form=$('#role-editor-form');form.reset();form.elements.id.value='box:'+box.id;form.elements.name.value=box.name;const set=(name,value)=>{if(value===undefined||value===null)return;const input=form.elements[name];if(input.type==='number'&&input.min!==''&&Number(value)<Number(input.min))return;input.value=String(value)},check=(name,value)=>form.elements[name].checked=!!value;check('allContactsEnabled',cap.allContacts?.enabled);check('mailRead',cap.mail?.read);check('mailCompose',cap.mail?.compose);set('maxBoxes',cap.createAgentBox?.maxBoxes);set('maxDiskGiB',cap.createAgentBox?.maxDiskGiB);const allowed=new Set(cap.createAgentBox?.allowedAgents||[]);form.querySelectorAll('input[name=allowedAgents]').forEach(input=>input.checked=!allowed.size||allowed.has(input.value));const allowedMCP=new Set(cap.mcpTools?.enabled?(cap.mcpTools.allowedTools||[]):[]);form.querySelectorAll('input[name=mcpTools]').forEach(input=>input.checked=allowedMCP.has(input.value));form.querySelectorAll('.role-capability-options').forEach(details=>details.open=false);syncMCPToolGroups(form);$('#role-editor-status').textContent='';modalEl('role-editor-modal').hidden=false;void api('/v1/mail/approvals').then(()=>form.querySelectorAll('[data-mail-feature]').forEach(node=>node.hidden=false)).catch(()=>form.querySelectorAll('[data-mail-feature]').forEach(node=>node.hidden=true))}
function syncMCPToolGroups(form=$('#role-editor-form')){const restart=form.querySelector('input[name=mcpTools][value=restart_agent_box]'),wake=form.querySelector('input[name=mcpTools][value=wake_agent_box]');if(restart&&wake){wake.disabled=restart.checked;if(restart.checked)wake.checked=true}for(const group of form.querySelectorAll('.mcp-tool-group')){const tools=[...group.querySelectorAll('input[name=mcpTools]')],toggle=group.querySelector('.mcp-tool-group-toggle'),selected=tools.filter(input=>input.checked).length;toggle.checked=selected===tools.length;toggle.indeterminate=selected>0&&selected<tools.length}}
const mailReadTools=['list_emails','read_email','search_emails','mark_email_read','download_email_attachment','subscribe_inbox','unsubscribe_inbox'],mailComposeTools=['send_email','list_outbox','get_outbox_status'];
function syncMailGrant(form,source){
 const group=source.closest('[data-mail-feature]');
 if(source.name==='mailRead'||source.name==='mailCompose'){
  const names=source.name==='mailRead'?mailReadTools:mailComposeTools;
  for(const input of form.querySelectorAll('input[name=mcpTools]'))if(names.includes(input.value))input.checked=source.checked;
 }else if(group&&source.matches('input[name=mcpTools],.mcp-tool-group-toggle')){
  form.elements.mailRead.checked=mailReadTools.some(name=>form.querySelector(`input[name=mcpTools][value=${name}]`)?.checked);
  form.elements.mailCompose.checked=mailComposeTools.some(name=>form.querySelector(`input[name=mcpTools][value=${name}]`)?.checked);
 }
 syncMCPToolGroups(form);
}
function changeMCPToolGroup(toggle){for(const input of toggle.closest('.mcp-tool-group').querySelectorAll('input[name=mcpTools]'))input.checked=toggle.checked;syncMailGrant(toggle.form,toggle)}
function controllerDirectPolicyBody(form){
 const f=form.elements,num=name=>Number.parseInt(f[name].value,10)||0,allowedTools=[...form.querySelectorAll('input[name=mcpTools]:checked')].map(input=>input.value),hasTool=name=>allowedTools.includes(name),chosenAgents=[...form.querySelectorAll('input[name=allowedAgents]:checked')].map(input=>input.value);
 return {capabilities:{allContacts:{enabled:f.allContactsEnabled.checked},mail:{read:f.mailRead.checked,compose:f.mailCompose.checked},createAgentBox:{enabled:hasTool('create_agent_box'),maxBoxes:num('maxBoxes'),maxDiskGiB:num('maxDiskGiB'),allowedAgents:chosenAgents.length?chosenAgents:['codex','claude','opencode'],assignableRoleIds:[]},manageAgentBoxes:{list:hasTool('list_agent_boxes'),inspect:hasTool('get_agent_box')||hasTool('get_agent_box_screenshot'),tag:hasTool('set_agent_box_tags'),restart:hasTool('restart_agent_box')||hasTool('wake_agent_box'),delete:hasTool('delete_agent_box')},mcpTools:{enabled:true,allowedTools}}};
}
$('#role-box-search').addEventListener('input',()=>renderPermissionBoxes(roleBoxes));
document.querySelectorAll('#role-editor-form .mcp-tool-group-toggle').forEach(input=>input.addEventListener('change',()=>changeMCPToolGroup(input)));
document.querySelectorAll('#role-editor-form input[name=mcpTools],#role-editor-form input[name=mailRead],#role-editor-form input[name=mailCompose]').forEach(input=>input.addEventListener('change',()=>syncMailGrant(input.form,input)));
$('#role-editor-form').addEventListener('submit',action(async event=>{const id=event.currentTarget.elements.id.value;if(!id.startsWith('box:'))return;$('#role-editor-status').textContent='Saving…';await api(bp(id.slice(4))+'/agent-policy','PUT',controllerDirectPolicyBody(event.currentTarget));modalEl('role-editor-modal').hidden=true;notice('Permissions updated and syncing automatically.');await refresh()}));
async function openBoxPolicyEditor(box){
	const policy=await api(bp(box.id)+'/agent-policy');populatePolicyEditor(box,policy.capabilities||{});
 $('#role-editor-title').textContent='Permissions · '+box.name;$('#delete-role').hidden=true;$('#role-assigned-count').textContent=policy.migratedFromRoles?'Imported the box’s previous role grants. Saving converts them to direct permissions.':'Saved directly on this box. Changes sync automatically to its running MCP client.';
}
function paintProfileLibrary(){
 const query=$('#profile-search').value.trim().toLocaleLowerCase(),root=$('#profile-tree');root.replaceChildren();
 $('#profile-summary').textContent=listedProfiles.length+' saved '+(listedProfiles.length===1?'profile':'profiles')+(profileAccountName?' · '+profileAccountName:'');
 let shown=0;
 for(const [app,label] of [['claude','Claude'],['codex','Codex'],['opencode','OpenCode'],['github','GitHub']]){
  const all=listedProfiles.filter(profile=>profile.application===app),entries=all.filter(profile=>!query||[label,profile.name,profile.model||''].some(value=>value.toLocaleLowerCase().includes(query)));
  if(query&&!entries.length)continue;
  shown+=entries.length;
  const card=node('article');card.className='profile-app';card.dataset.application=app;
  const head=node('header');head.className='profile-app-head';const mark=node('span');mark.className='profile-app-mark';mark.setAttribute('aria-hidden','true');
  const harnessIcons={claude:'/harness-claude.svg',codex:'/harness-codex.svg',opencode:'/harness-opencode-light.svg'};
  if(harnessIcons[app]){const icon=document.createElement('img');icon.src=harnessIcons[app];icon.alt='';mark.append(icon)}else mark.textContent=label.slice(0,1);
  const heading=node('div'),title=node('h3',label),description=node('p',app==='github'?'Git credentials for cloning and pushing.':'Agent login snapshots for new boxes.');heading.append(title,description);
  const count=node('span',String(query?entries.length:all.length));count.className='profile-app-count';count.setAttribute('aria-label',(query?entries.length:all.length)+' '+label+' profiles');head.append(mark,heading,count);card.append(head);
  const list=node('div');list.className='profile-list';
  for(const profile of entries){
   const row=node('div');row.className='profile-row';row.dataset.profileName=profile.name;
   const text=node('div'),name=node('strong',profile.name);text.className='profile-row-text';text.append(name);
   if(profile.model){const model=node('span','Model · '+profile.model);model.className='profile-model';text.append(model)}
   const date=new Date(profile.createdAt),saved=node('small',Number.isNaN(date.getTime())?'Saved date unavailable':'Saved '+date.toLocaleDateString(undefined,{year:'numeric',month:'short',day:'numeric'}));text.append(saved);
   const remove=button('Delete',async()=>{if(!confirm('Delete saved profile '+label+' / '+profile.name+'? This cannot be undone. Existing boxes keep their copied credentials; pending creations using this profile may fail.'))return;await api('/v1/login-profiles/'+encodeURIComponent(app)+'/'+encodeURIComponent(profile.name),'DELETE');await refresh();notice('Deleted '+label+' profile '+profile.name+'.')});remove.classList.add('danger');remove.setAttribute('aria-label','Delete '+label+' profile '+profile.name);
   row.append(text,remove);list.append(row);
  }
  if(!entries.length)list.append(node('p','No saved '+label+' profiles.'));
  card.append(list);root.append(card);
 }
 if(query&&!shown)root.append(node('p','No profiles match “'+$('#profile-search').value.trim()+'”.'));
}
$('#profile-search').addEventListener('input',paintProfileLibrary);
function renderProfiles(identity,profiles){
 listedProfiles=profiles;profileAccountName=identity.accountName||'';paintProfileLibrary();
 const choices=$('#profile-choices'),selected=choices.querySelector('select')?.value||'';choices.replaceChildren();
 renderCreationProfileChoices(choices,profiles,$('#create select[name="defaultAgent"]'),selected);
}
let profileLoginSessionID='',profileLoginTimer=0,profileLoginFromCreate=false,profileVerifiedKey='',profileLoginTerminalStop=null,profileLoginCallbackSent=false;
const profileLoginForm=$('#profile-login-form');
function profileLoginFields(){
 const f=profileLoginForm.elements,app=f.application.value;
 if(app==='opencode')f.method.value='api';
 const apiMode=f.method.value==='api';
 const providers=app==='opencode'?[['openrouter','OpenRouter'],['venice','Venice']]:app==='claude'?[['anthropic','Anthropic']]:[['openai','OpenAI']];
 const previous=f.provider.value;f.provider.replaceChildren();for(const [value,label] of providers){const option=node('option',label);option.value=value;f.provider.append(option)}if(providers.some(([value])=>value===previous))f.provider.value=previous;
 $('.profile-login-provider').hidden=!apiMode;$('.profile-login-key').hidden=!apiMode;$('.profile-login-model').hidden=!apiMode;$('.profile-login-email').hidden=apiMode||app!=='claude';$('.profile-login-flow').hidden=apiMode||app!=='codex';
 $('#profile-login-verify').hidden=!apiMode;$('#profile-login-submit').textContent=apiMode?'Save API profile':'Start browser login';
 $('#profile-login-submit').disabled=apiMode;
 $('#profile-login-method-note').textContent=apiMode?'API usage is billed by '+(app==='opencode'?f.provider.selectedOptions[0]?.textContent:app==='claude'?'Anthropic':'OpenAI')+'. Verify the key to choose an available model.':app==='codex'?(f.flow.value==='browser'?'Open the official ChatGPT sign-in link. After login, copy the localhost callback URL from your browser address bar into this dialog.':'Device code also signs in to your ChatGPT account. Enable it in ChatGPT security or workspace settings, then enter the shown code in your browser.'):'Claude Code opens its official sign-in page. If it asks for a browser code, paste that code here.';
 profileVerifiedKey='';f.model.replaceChildren(new Option('Verify the key first',''));
}
function openProfileLogin(fromCreate=false){
 profileLoginFromCreate=fromCreate;profileLoginCallbackSent=false;profileLoginForm.reset();$('#profile-login-progress').hidden=true;profileLoginForm.hidden=false;$('#profile-login-status').textContent='';$('#profile-login-form-status').textContent='';
 if(fromCreate){const app=$('#create select[name="defaultAgent"]').value;if(['codex','claude','opencode'].includes(app))profileLoginForm.elements.application.value=app}
 profileLoginFields();modalEl('profile-login-modal').hidden=false;profileLoginForm.elements.name.focus();
}
function openProfileFromLink(){
 const url=new URL(location.href);
 if(!ownerTools||url.searchParams.get('add-profile')!=='1')return;
 url.searchParams.delete('add-profile');history.replaceState(null,'',url.pathname+url.search+url.hash);
 openProfileLogin();
}
async function cancelProfileLogin(){
 profileLoginTerminalStop?.();profileLoginTerminalStop=null;
 clearTimeout(profileLoginTimer);const id=profileLoginSessionID;profileLoginSessionID='';
 if(id)try{await api('/v1/login-profiles/browser/'+encodeURIComponent(id),'DELETE')}catch{}
}
function closeProfileLogin(){modalEl('profile-login-modal').hidden=true;profileLoginForm.elements.key.value='';profileVerifiedKey='';void cancelProfileLogin()}
$('#profile-new').addEventListener('click',()=>openProfileLogin());
$('#create-profile-open').addEventListener('click',()=>openProfileLogin(true));
for(const name of ['application','method','provider','flow'])profileLoginForm.elements[name].addEventListener('change',profileLoginFields);
profileLoginForm.elements.key.addEventListener('input',()=>{profileVerifiedKey='';$('#profile-login-submit').disabled=true;profileLoginForm.elements.model.replaceChildren(new Option('Verify the key first',''))});
$('#profile-login-verify').addEventListener('click',action(async()=>{
 const f=profileLoginForm.elements,body={application:f.application.value,provider:f.provider.value,key:f.key.value};
 $('#profile-login-form-status').textContent='Checking key and loading models…';
 const response=await api('/v1/login-profiles/api-key/verify','POST',body);const models=response.models||[];
 f.model.replaceChildren();for(const model of models){const option=node('option',model);option.value=model;f.model.append(option)}
 profileVerifiedKey=[body.application,body.provider,body.key].join('\u0000');
 $('#profile-login-submit').disabled=false;
 $('#profile-login-form-status').textContent='Key verified. Choose a model and save.';
}));
profileLoginForm.addEventListener('submit',action(async event=>{
 const f=event.currentTarget.elements,app=f.application.value,name=f.name.value.trim(),replaceExisting=f.replaceExisting.checked;
 if(f.method.value==='api'){
  if(profileVerifiedKey!==[app,f.provider.value,f.key.value].join('\u0000')||!f.model.value)throw Error('Verify the key and choose a model first.');
  const profile=await api('/v1/login-profiles/api-key','POST',{application:app,provider:f.provider.value,name,key:f.key.value,model:f.model.value,replaceExisting});
  f.key.value='';profileVerifiedKey='';modalEl('profile-login-modal').hidden=true;await refresh();selectNewProfile(profile);notice('Saved '+app+' profile '+name+'.');return;
 }
 const session=await api('/v1/login-profiles/browser','POST',{application:app,name,email:f.email.value.trim(),flow:app==='codex'?f.flow.value:'device',replaceExisting});
 profileLoginSessionID=session.id;profileLoginForm.hidden=true;$('#profile-login-progress').hidden=false;renderProfileLoginStatus(session);
 profileLoginTerminalStop=window.openProfileLoginTerminal(session.id,$('#profile-login-terminal'));pollProfileLogin();
}));
function selectNewProfile(profile){
 if(!profileLoginFromCreate)return;
 const select=$('#profile-choices select[name="loginProfile"]');if(!select)return;
 const value=JSON.stringify({application:profile.application,name:profile.name});
 if([...select.options].some(option=>option.value===value)){select.value=value;select.dispatchEvent(new Event('change'))}
}
function renderProfileLoginStatus(session){
 $('#profile-login-status').textContent=session.message||(session.status==='waiting'&&profileLoginCallbackSent?'Callback sent. Waiting for Codex…':({starting:'Starting isolated login…',waiting:'Finish sign-in in your browser.',saved:'Profile saved.',failed:'Login failed.',expired:'Login timed out.',canceled:'Login canceled.'}[session.status]||session.status));
 const link=$('#profile-login-url');link.hidden=!session.url;if(session.url)link.href=session.url;else link.removeAttribute('href');
 $('#profile-login-device').hidden=!session.code;$('#profile-login-device-code').textContent=session.code||'';
 $('#profile-login-callback-form').hidden=!(session.status==='waiting'&&profileLoginForm.elements.application.value==='codex'&&profileLoginForm.elements.flow.value==='browser');
 $('#profile-login-code-form').hidden=!(session.status==='waiting'&&profileLoginForm.elements.application.value==='claude');
 $('#profile-login-cancel').hidden=['saved','failed','expired','canceled'].includes(session.status);
 $('#profile-login-retry').hidden=!['failed','expired','canceled'].includes(session.status);
}
async function pollProfileLogin(){
 const id=profileLoginSessionID;if(!id)return;
 try{const state=await api('/v1/login-profiles/browser/'+encodeURIComponent(id));if(id!==profileLoginSessionID)return;renderProfileLoginStatus(state);
  if(state.status==='saved'){profileLoginSessionID='';profileLoginTerminalStop?.();profileLoginTerminalStop=null;const profile={application:profileLoginForm.elements.application.value,name:profileLoginForm.elements.name.value.trim()};await refresh();selectNewProfile(profile);return}
  if(['failed','expired','canceled'].includes(state.status)){profileLoginSessionID='';profileLoginTerminalStop?.();profileLoginTerminalStop=null;return}
 }catch(err){$('#profile-login-status').textContent=err.message}
 if(id===profileLoginSessionID)profileLoginTimer=setTimeout(pollProfileLogin,1500);
}
$('#profile-login-code-form').addEventListener('submit',action(async event=>{const code=event.currentTarget.elements.code.value;await api('/v1/login-profiles/browser/'+encodeURIComponent(profileLoginSessionID)+'/code','POST',{code});event.currentTarget.reset();$('#profile-login-status').textContent='Code sent. Waiting for Claude Code…'}));
$('#profile-login-callback-form').addEventListener('submit',action(async event=>{const url=event.currentTarget.elements.url.value;await api('/v1/login-profiles/browser/'+encodeURIComponent(profileLoginSessionID)+'/callback','POST',{url});profileLoginCallbackSent=true;event.currentTarget.reset();$('#profile-login-status').textContent='Callback sent. Waiting for Codex…'}));
$('#profile-login-cancel').addEventListener('click',action(async()=>{await cancelProfileLogin();renderProfileLoginStatus({status:'canceled'})}));
$('#profile-login-retry').addEventListener('click',()=>{profileLoginForm.hidden=false;$('#profile-login-progress').hidden=true;$('#profile-login-status').textContent='';profileLoginForm.elements.name.focus()});
document.querySelectorAll('[data-close="profile-login-modal"]').forEach(element=>element.addEventListener('click',closeProfileLogin));
const agentCLIChoices=['claude','codex','opencode'];
function agentCLIVersionNewestFirst(left,right){
 const leftDash=left.indexOf('-'),rightDash=right.indexOf('-');
 const leftCore=leftDash<0?left:left.slice(0,leftDash),rightCore=rightDash<0?right:right.slice(0,rightDash);
 const leftPre=leftDash<0?'':left.slice(leftDash+1),rightPre=rightDash<0?'':right.slice(rightDash+1);
 const coreOrder=rightCore.localeCompare(leftCore,undefined,{numeric:true});
 if(coreOrder)return coreOrder;
 if(!leftPre&&rightPre)return -1;
 if(leftPre&&!rightPre)return 1;
 return (rightPre||'').localeCompare(leftPre||'',undefined,{numeric:true});
}
function renderAgentCLIVersionChoice(agent,selected,catalog,loading=false){
 const select=$('#agent-cli-versions').elements[agent],options=document.createDocumentFragment(),values=new Set();
 const add=(value,label,disabled=false)=>{const option=node('option',label);option.value=value;option.disabled=disabled;options.append(option);values.add(value)};
 add('','Worker image version');
 add('latest',catalog?.latest?'Latest at box creation (now '+catalog.latest+')':'Latest at box creation');
 if(catalog){
  const versions=[...catalog.versions].sort(agentCLIVersionNewestFirst);
  for(const version of versions)add(version,version===catalog.latest?version+' (current latest)':version);
 }else add('__unavailable',loading?'Loading published versions…':'Published versions unavailable',true);
 if(selected&&!values.has(selected))add(selected,selected+' (saved)');
 select.replaceChildren(options);select.value=selected;
}
async function agentCLICatalog(agent){
 const cached=agentCLICatalogCache.get(agent);
 if(cached&&Date.now()-cached.at<5*60*1000)return cached.value;
 const value=await api('/v1/agent-cli-versions/catalog/'+agent);
 agentCLICatalogCache.set(agent,{at:Date.now(),value});
 return value;
}
async function loadAgentCLIVersionChoices(version){
 const results=await Promise.allSettled(agentCLIChoices.map(agent=>agentCLICatalog(agent)));
 if(version!==epoch)return;
 const failed=[];
 for(let index=0;index<agentCLIChoices.length;index++){
  const agent=agentCLIChoices[index],result=results[index],selected=$('#agent-cli-versions').elements[agent].value;
  renderAgentCLIVersionChoice(agent,selected,result.status==='fulfilled'?result.value:null);
  if(result.status==='rejected')failed.push(agent);
 }
 if(failed.length){const status=$('#agent-cli-versions-status');status.replaceChildren();const icon=node('span','⚠');icon.className='notice-icon';const msg=node('span','Could not load published versions for '+failed.join(', ')+'. Saved choices remain available. ');const retry=button('Retry',()=>$('#refresh')?.click());retry.classList.add('notice-retry');status.append(icon,msg,retry);}
}
async function refresh(){
 const version=epoch,[caps,boxes,instructionList]=await Promise.all([api('/v1/capabilities'),api('/v1/logical-boxes'),api('/v1/instruction-presets').catch(()=>({defaultName:'',presets:[]}))]);if(version!==epoch)return;
 ownerTools=caps.providerEdits;
 if(!ownerTools)workspaceNav?.setOwner(false);
 document.querySelectorAll('[data-owner]:not(.modal)').forEach(n=>n.hidden=!ownerTools);
 applyInstructionPresets(instructionList);
 renderBoxes(boxes);
 if(!ownerTools)return;
 const [providers,schema,notifications,identity,profiles,toolPresets,cliVersions]=await Promise.all([api('/v1/provider-credentials'),api('/v1/provider-schemas'),api('/v1/notifications'),api('/v1/whoami'),api('/v1/login-profiles'),api('/v1/tool-presets'),api('/v1/agent-cli-versions')]);if(version!==epoch)return;
 workspaceNav?.setOwner(identity.role==='owner');
 renderPermissionBoxes(boxes);
 renderPoolChoices(providers);
 const fleets=await Promise.all(providers.map(async provider=>{const target={provider:provider.provider,providerCredential:provider.name||''};const snapshot=await workspaceNav.providerSnapshot(provider);return {...(snapshot.fleet||{}),...target,providerSnapshot:snapshot,hostResources:snapshot.host,hostError:snapshot.hostError,error:snapshot.fleet?undefined:snapshot.errors[0]}}));if(version!==epoch)return;fleetSnapshots=fleets;updateBoxPlacements(boxes);
 const chosenTools=new Set([...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value));$('#create-tools').replaceChildren(node('legend','Optional tools'));
 for(const preset of toolPresets){if(preset.id==='desktop')continue;const label=node('label'),input=node('input');input.type='checkbox';input.value=preset.id;input.checked=chosenTools.has(preset.id);label.title=preset.version+' — '+preset.description;label.append(input,document.createTextNode(preset.name));$('#create-tools').append(label)}
 renderProfiles(identity,profiles);
 for(const agent of agentCLIChoices)renderAgentCLIVersionChoice(agent,cliVersions[agent]||'',null,true);
 void loadAgentCLIVersionChoices(version);
 renderProviders(providers);
 renderProviderTypes(schema);renderNotifications(notifications);defaults=null;
 try{const d=await api('/v1/controller-defaults');if(version!==epoch)return;$('#provider-default').textContent='Default: '+d.provider+' / '+d.providerCredential;if(locationTarget&&(locationTarget.provider!==d.provider||locationTarget.providerCredential!==d.providerCredential))resetLocation();defaults=d;renderWorkerCapacity();renderProviders(providers)}catch(err){if(version===epoch){renderWorkerCapacity();$('#provider-default').textContent='Check the default provider and capacity configuration.'}}
}
$('#login').addEventListener('submit',action(async e=>{token=e.target.elements.token.value;try{await api('/v1/browser-session','POST',{})}finally{token='';e.target.reset()}await refresh();$('#login').hidden=true;$('#login-error').textContent='';$('#app').hidden=false;openProfileFromLink()}));
$('#logout').addEventListener('click',action(async()=>{workspaceNav?.closeMenu();await api('/v1/browser-session','DELETE');workspaceNav?.setOwner(false);epoch++;resetLocation();clearTimeout(boxRefreshTimer);startingBoxes.clear();token='';defaults=null;fleetSnapshots=[];ownerTools=false;roleBoxes=[];listedProfiles=[];profileAccountName='';instructionPresets={defaultName:'',presets:[]};presetBodyCache.clear();boxInstructionTarget=null;boxCredentialTarget=null;renderPoolChoices([]);$('#capacity').replaceChildren();$('#box-list').replaceChildren();$('#role-assignments').replaceChildren();$('#instruction-list').replaceChildren();$('#box-credentials-form').replaceChildren();modalEl('box-instructions-modal').hidden=true;modalEl('box-credentials-modal').hidden=true;modalEl('role-editor-modal').hidden=true;$('#app').hidden=true;$('#login').hidden=false;document.querySelectorAll('form').forEach(f=>f.reset());closeProviderPanel();$('#profile-search').value='';$('#profile-summary').textContent='';$('#profile-tree').replaceChildren();$('#profile-choices').replaceChildren();$('#login-error').textContent='';$('#error').textContent='';$('#login-token').focus()}));
$('#refresh').addEventListener('click',action(refresh));
function resetCreationForm(form){
 const pool=form.elements.pool.value;
 form.reset();createInstructionSource='';
 if([...form.elements.pool.options].some(option=>option.value===pool))form.elements.pool.value=pool;
 form.querySelectorAll('details').forEach(details=>details.open=false);
 void syncCreateInstructionText();
}
const createCard=$('#create-card');
const focusCreateName=()=>createCard?.querySelector('#create input[name="name"]')?.focus({preventScroll:true});
function revealCreateCard(){
 if(!createCard)return;
 createCard.open=true;
 focusCreateName();
 createCard.scrollIntoView({behavior:'smooth',block:'start'});
}
document.querySelector('.section-actions a[href="#create"]')?.addEventListener('click',event=>{event.preventDefault();revealCreateCard()});
window.addEventListener('hashchange',()=>{if(location.hash==='#create')revealCreateCard()});
createCard?.addEventListener('toggle',()=>{if(createCard.open)focusCreateName()});
$('#create-cancel')?.addEventListener('click',()=>{resetCreationForm($('#create'));createCard.open=false;createCard.scrollIntoView({behavior:'smooth',block:'nearest'})});
$('#create').addEventListener('submit',action(async e=>{const f=e.target.elements,profile=f.loginProfile?.value,profileRef=profile?JSON.parse(profile):null,loginProfiles=profileRef?[{...profileRef,model:f.agentModel.value.trim(),...(f.agentReasoningEffort.value?{reasoningEffort:f.agentReasoningEffort.value}:{})}]:[],github=f.githubProfile?.value,tools=['desktop',...[...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value)],setupScript=f.setupScript.value,d=f.pool.value?JSON.parse(f.pool.value):await chooseCreationPool(tools),instructions=await createInstructionSelection();if(profileRef&&!loginProfiles[0].model)throw Error('Choose a model');if(github)loginProfiles.push(JSON.parse(github));const createdName=f.name.value.trim(),created=await api('/v1/logical-boxes','POST',{name:createdName,defaultAgent:f.defaultAgent.value,diskGiB:Number(f.disk.value),provider:d.provider,providerCredential:d.providerCredential,allocateWhenReady:true,loginProfiles,tools,...(d.provider==='shared-worker'?{memoryGiB:Number(f.memoryGiB.value),swapGiB:Number(f.swapGiB.value)}:{}),...(setupScript.trim()?{setupScript}:{}),...(instructions?{instructions}:{})},{'Idempotency-Key':crypto.randomUUID()});if(created?.id)startingBoxes.add(created.id);resetCreationForm(e.target);try{await refresh();notice('Box '+createdName+' is starting.')}finally{document.activeElement?.blur();window.scrollTo(0,0)}}));
$('#agent-cli-versions').addEventListener('submit',action(async e=>{const fields=e.target.elements,values={};for(const agent of ['claude','codex','opencode'])values[agent]=fields[agent].value.trim();await api('/v1/agent-cli-versions','PUT',values);$('#agent-cli-versions-status').textContent='Saved. New boxes will use these versions.'}));
$('#provider').addEventListener('submit',action(async e=>{
 const f=e.target.elements,rev=f.revision.value,type=providerTypes.find(t=>t.name===f.provider.value),config={},secret={};
 for(const field of type?.fields||[]){
  const input=f[(field.secret?'secret:':'config:')+field.name];if(!input||input.disabled)continue;
  if(field.secret){if(input.value)secret[field.name]=input.value;continue}
  if(field.type==='boolean'){config[field.name]=input.checked;continue}
  const value=input.value.trim();if(value)config[field.name]=value;else if(rev)config[field.name]=null;
 }
 const body={config};if(Object.keys(secret).length){body.secret=secret;if(rev)body.replaceSecret=true}
 await api(pp(f.provider.value,f.alias.value),rev?'PATCH':'PUT',body,rev?{'If-Match':rev}:{});
 const saved=f.alias.value;if(!rev){resetProviderForm();$('#provider-editor').open=false;openProviderKey=poolKey(f.provider.value,saved)}notice('Provider '+saved+' saved. Validate it from its Pool actions.');await refresh();if(openProviderKey){const provider=listedProviders.find(item=>poolKey(item.provider,item.name)===openProviderKey);if(provider){editProvider(provider);renderProviders(listedProviders)}}
}));
$('#slots').addEventListener('submit',action(async e=>{const target=$('#capacity-pool').value?JSON.parse($('#capacity-pool').value):defaults;if(!target)throw Error('Choose a worker pool first');await api('/v1/fleet/slots','PUT',{...target,compute_box_slots:Number(e.target.elements.count.value)});await refresh()}));
$('#notification').addEventListener('submit',action(async e=>{const f=e.target.elements,split=s=>s.split(',').map(v=>v.trim()).filter(Boolean);await api('/v1/notifications/'+encodeURIComponent(f.kind.value)+'/'+encodeURIComponent(f.name.value),'PUT',{config:JSON.parse(f.config.value),secret:JSON.parse(f.secret.value),allowedUsers:split(f.users.value),allowedChats:split(f.chats.value)});e.target.reset();await refresh()}));

/* ---------- instruction presets, box instructions, imported profiles ---------- */
const conciseInstructions='## Concise responses\n\nDo the requested work fully. In messages, use as few tokens as needed for a complete, correct answer. Write short, direct sentences. Omit filler, repetition, and unrequested background.\n';
function addConciseInstructions(markdown){if(markdown.includes(conciseInstructions))return markdown;const separator=!markdown||markdown.endsWith('\n\n')?'':markdown.endsWith('\n')?'\n':'\n\n';return markdown+separator+conciseInstructions}
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
 if(!instructionPresets.presets.length){root.append(node('p','No instruction presets yet. Create one to reuse Markdown guidance across boxes.'));return}
 const friendly=value=>{const d=new Date(value);if(Number.isNaN(d.getTime()))return '—';const now=new Date(),same=d.toDateString()===now.toDateString();const time=d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});return same?'Today, '+time:d.toLocaleDateString([],{day:'numeric',month:'short'})+', '+time};
 const list=node('div');list.className='preset-list';
 for(const preset of instructionPresets.presets){
  const row=node('article');row.className='preset-row';row.dataset.presetName=preset.name;
  const main=node('div');main.className='preset-main';
  const title=node('div');title.className='preset-title';title.append(node('strong',preset.name));
  if(preset.default){const pill=node('span','Default');pill.className='pill';title.append(pill)}
  const meta=node('small',['r'+preset.revision,preset.sizeBytes+' B','updated '+friendly(preset.updatedAt)].join(' · '));meta.className='preset-meta';
  main.append(title,meta);
  const actions=node('div');actions.className='preset-actions';
  if(ownerTools){
   actions.append(button('Edit',()=>void editInstructionPreset(preset.name)));
   const more=node('details');more.className='row-overflow';const summary=node('summary');summary.setAttribute('aria-label','More for '+preset.name);summary.innerHTML=ICON_MORE;const menu=node('div');menu.className='row-overflow-menu';menu.append(button(preset.default?'Clear default':'Set default',()=>setInstructionDefault(preset.default?'':preset.name)),trashButton('Delete instruction preset '+preset.name,()=>deleteInstructionPreset(preset)));more.append(summary,menu);actions.append(more);
  } else actions.append(node('span','—'));
  row.append(main,actions);list.append(row);
 }
 root.append(list);
}
document.getElementById('instruction-new')?.addEventListener('click',()=>{const editor=$('#instruction-editor');if(!editor)return;editor.open=true;editor.scrollIntoView({behavior:'smooth',block:'nearest'});const field=editor.querySelector('input[name=name]');if(field)field.focus()});
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
 const concise=node('option','Concise responses');concise.value='__concise__';select.append(concise);
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
 if(value==='custom'||value==='__concise__'){
  textarea.readOnly=false;
  if(createInstructionSource!==value){textarea.value=value==='__concise__'?addConciseInstructions(textarea.value):'';createInstructionSource=value;editor.open=true}
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
 if(value==='custom'||value==='__concise__'){if(!markdown.trim())throw Error('Enter the custom instruction Markdown or choose another source.');return {markdown}}
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
  const none=node('option','No custom instructions (chat conventions only)');none.value='';select.append(none);
  const concise=node('option','Concise responses');concise.value='__concise__';select.append(concise);
  for(const preset of instructionPresets.presets){const option=node('option',preset.name+' · r'+preset.revision);option.value=preset.name;select.append(option)}
  const custom=node('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
  select.value=current.source==='preset'&&instructionPresets.presets.some(p=>p.name===current.preset)?current.preset:(current.source==='custom'?(current.markdown===conciseInstructions?'__concise__':'custom'):'');
  $('#box-instructions-markdown').value=current.markdown||'';
  renderMarkdownPreview($('#box-instructions-preview'),current.markdown||'');
  $('#box-instructions-effective').textContent=state.effectiveMarkdown||'';
  $('#box-instructions-current').textContent=describeBoxInstructions(state);
  status.textContent='';
  modalEl('box-instructions-modal').hidden=false;
 }catch(e){status.textContent=e.message}
}
$('#box-instructions-preset').addEventListener('change',action(async()=>{
 const select=$('#box-instructions-preset'),textarea=$('#box-instructions-markdown'),preview=$('#box-instructions-preview');
 if(select.value===''){textarea.value='';renderMarkdownPreview(preview,'');return}
 if(select.value==='__concise__'){textarea.value=addConciseInstructions(textarea.value);renderMarkdownPreview(preview,textarea.value);return}
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
 else if(select.value==='custom'||select.value==='__concise__'){if(!markdown.trim())throw Error('Enter the custom Markdown or choose another source.');body={markdown}}
 else{const preset=await presetBody(select.value);body=markdown.trim()&&markdown!==preset?{preset:select.value,markdown}:{preset:select.value}}
 status.textContent='Applying…';
 const result=await api(bp(boxInstructionTarget.id)+'/instructions','PUT',body);
 if(selectedManagedBoxID===boxInstructionTarget.id){boxDetailInstructionsRequest++;boxDetailInstructions=result;renderBoxDetail()}
 status.textContent=result.note||describeBoxInstructions(result);
 if(result.instructions)$('#box-instructions-current').textContent=describeBoxInstructions(result);
 $('#box-instructions-effective').textContent=result.effectiveMarkdown||'';
 await refresh();
}));
const boxCredentialSlot=ref=>ref.application==='github'?'github':'agent';
const boxCredentialRefs=state=>(state.pendingSet||(state.pending||[]).length?state.pending:state.profiles)||[];
const boxCredentialRef=(refs,slot)=>refs.find(ref=>boxCredentialSlot(ref)===slot);
function boxCredentialLabel(ref){
 if(!ref)return 'None';
 const profile=boxCredentialOptions.find(item=>item.application===ref.application&&item.name===ref.name);
 const app={claude:'Claude',codex:'Codex',opencode:'OpenCode',github:'GitHub'}[ref.application]||ref.application;
 return [app,ref.name,profile?.email,ref.application==='github'&&[profile?.user,profile?.host].filter(Boolean).join('@')].filter(Boolean).join(' · ');
}
function renderBoxCredentials(){
 const root=$('#box-credentials-form');root.replaceChildren();if(!boxCredentialState)return;
 const effective=boxCredentialRefs(boxCredentialState),imported=boxCredentialState.profiles||[];
 for(const [slot,title] of [['agent','Agent login'],['github','GitHub']]){
  const ref=boxCredentialRef(effective,slot),saved=boxCredentialRef(imported,slot);
  const options=boxCredentialOptions.filter(profile=>boxCredentialSlot(profile)===slot);
  const row=node('section');row.className='credential-slot';row.dataset.slot=slot;
  const heading=node('div');heading.className='credential-slot-heading';heading.append(node('strong',title));
  const account=node('div',boxCredentialLabel(ref));account.className='credential-slot-account';
  const queued=boxCredentialState.pendingSet||(boxCredentialState.pending||[]).length>0;
  const note=node('p',queued&&JSON.stringify(ref)!==JSON.stringify(saved)?'Pending restart · applies on next start':ref?'Applied':'No account connected');note.className='credential-slot-note';
  const actions=node('div');actions.className='credential-slot-actions';
  const change=node('button','Change');change.type='button';change.className='credential-slot-change';change.setAttribute('aria-label','Change '+title);change.disabled=boxCredentialBusy||!options.length;
  if(!options.length)change.title='Save a '+title+' profile first';
  const picker=node('div');picker.className='credential-slot-picker';picker.hidden=true;
  const label=node('label','Saved account');const select=node('select');select.setAttribute('aria-label',title+' saved account');
  for(const profile of options){const option=node('option',boxCredentialLabel(profile));option.value=JSON.stringify({application:profile.application,name:profile.name});select.append(option)}
  const current=ref&&JSON.stringify({application:ref.application,name:ref.name});if(current&&[...select.options].some(option=>option.value===current))select.value=current;
  label.append(select);picker.append(label);
  const pickerActions=node('div');pickerActions.className='credential-picker-actions';
  const cancel=node('button','Cancel');cancel.type='button';cancel.onclick=()=>{picker.hidden=true;change.focus()};
  const save=node('button','Save');save.type='button';save.className='credential-slot-save';save.setAttribute('aria-label','Save '+title);save.onclick=()=>void updateBoxCredentialSlot(slot,JSON.parse(select.value));
  pickerActions.append(cancel,save);picker.append(pickerActions);
  change.onclick=()=>{picker.hidden=false;select.focus()};actions.append(change);
  if(slot==='github'&&ref){const remove=node('button','Remove');remove.type='button';remove.className='credential-slot-remove';remove.setAttribute('aria-label','Remove GitHub');remove.disabled=boxCredentialBusy;remove.onclick=()=>void updateBoxCredentialSlot(slot,null);actions.append(remove)}
  row.append(heading,account,note,actions,picker);root.append(row);
 }
}
function replaceBoxCredentialSlot(refs,slot,replacement){
 const result=[];let replaced=false;
 for(const ref of refs){if(boxCredentialSlot(ref)!==slot){result.push(ref);continue}if(!replaced&&replacement)result.push(replacement);replaced=true}
 if(!replaced&&replacement)result.push(replacement);
 return result;
}
async function openBoxCredentials(box){
 boxCredentialTarget=box;boxCredentialState=null;boxCredentialBusy=false;boxCredentialRetryAction=null;
 const request=++boxCredentialRequest,status=$('#box-credentials-status');status.textContent='Loading accounts…';
 $('#box-credentials-title').textContent='Credentials · '+box.name;$('#box-credentials-form').replaceChildren();$('#box-credentials-retry').hidden=true;$('#box-credentials-retry').textContent='Retry';
 modalEl('box-credentials-modal').hidden=false;
 try{
  const [state,profiles]=await Promise.all([api(bp(box.id)+'/imported-credentials'),api('/v1/login-profiles')]);
  if(request!==boxCredentialRequest)return;
  boxCredentialState=state;boxCredentialOptions=profiles||[];status.textContent='';renderBoxCredentials();
 }catch(e){if(request===boxCredentialRequest){status.textContent='Could not load credentials: '+e.message;$('#box-credentials-retry').hidden=false}}
}
async function updateBoxCredentialSlot(slot,replacement){
 if(!boxCredentialTarget||!boxCredentialState||boxCredentialBusy)return;
 const target=boxCredentialTarget,request=boxCredentialRequest,status=$('#box-credentials-status');
 const profiles=replaceBoxCredentialSlot(boxCredentialRefs(boxCredentialState),slot,replacement);
 boxCredentialRetryAction=null;$('#box-credentials-retry').hidden=true;
 boxCredentialBusy=true;status.textContent='Applying '+(slot==='github'?'GitHub':'agent login')+'…';renderBoxCredentials();
 try{
  const result=await api(bp(target.id)+'/login-profiles','PUT',{profiles});
  if(request!==boxCredentialRequest)return;
  boxCredentialState=result;status.textContent=result.note||'Saved.';
  void refresh().catch(e=>{if(request===boxCredentialRequest)status.textContent=(result.note||'Saved.')+' Box list refresh failed: '+e.message});
 }catch(e){if(request===boxCredentialRequest){status.textContent=e.message;boxCredentialRetryAction={slot,replacement};$('#box-credentials-retry').textContent='Retry change';$('#box-credentials-retry').hidden=false}}
 finally{if(request===boxCredentialRequest){boxCredentialBusy=false;renderBoxCredentials()}}
}
$('#box-credentials-retry').onclick=()=>{if(boxCredentialRetryAction)void updateBoxCredentialSlot(boxCredentialRetryAction.slot,boxCredentialRetryAction.replacement);else if(boxCredentialTarget)void openBoxCredentials(boxCredentialTarget)};
document.querySelectorAll('[data-close]').forEach(element=>element.addEventListener('click',()=>{const card=element.closest('.modal');if(card)card.hidden=true}));
