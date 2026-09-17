'use strict';
const $=s=>document.querySelector(s);
function enforceDesktopToolDependency(root){
 const desktop=root.querySelector('input[value="desktop"]'),blender=root.querySelector('input[value="blender"]');
 if(!desktop)return;
 const update=()=>{desktop.disabled=!!blender?.checked;if(desktop.disabled)desktop.checked=true;desktop.parentElement.title=desktop.disabled?'Required for Blender':'Desktop and Chromium browser without Blender'};
 blender?.addEventListener('change',update);update();
}
let token='',defaults=null,epoch=0;
let boxRefreshTimer;
let fleetSnapshots=[];
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
 for(const row of document.querySelectorAll('#box-list [data-box-id]')){const box=byID.get(row.dataset.boxId);if(box)row.querySelector('.box-placement').textContent=boxPlacement(box)}
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
function action(fn){return async e=>{e?.preventDefault();$('#error').textContent='';try{await fn(e)}catch(err){$('#error').textContent=err.message}}}
function node(tag,text){const n=document.createElement(tag);n.textContent=text;return n}
function tableText(text){const n=node('span',text??'—');n.className='table-text';n.title=String(text??'—');return n}
function button(text,fn){const b=node('button',text);b.type='button';b.addEventListener('click',action(fn));return b}
// Restore same-tab navigation without storing credentials in JavaScript storage.
async function restoreLogin(){try{await api('/v1/browser-session');await refresh();$('#login').hidden=true;$('#app').hidden=false}catch{}}
window.addEventListener('DOMContentLoaded',restoreLogin);
function dataTable(headers,rows){const table=document.createElement('table'),head=document.createElement('tr');for(const h of headers)head.append(node('th',h));table.append(head);for(const values of rows){const row=document.createElement('tr');for(const value of values){const cell=node('td');cell.append(tableText(value));row.append(cell)}table.append(row)}return table}
function rawDetails(value){const d=document.createElement('details');d.append(node('summary','Technical details · JSON'),node('pre',JSON.stringify(value,null,2)));return d}
let locationTarget=null,locationLoading=false,locationSaving=false;
function resetLocation(){locationTarget=null;$('#location-form').hidden=true;$('#location-status').textContent='';}
let costTarget=null,costLoading=false;
function resetCosts(){costTarget=null;$('#cost-overview').replaceChildren(node('p','Costs have not been loaded.'))}
function formatCost(cost){
 if(!cost?.available)return 'Unavailable';
 try{return new Intl.NumberFormat(undefined,{style:'currency',currency:cost.currency||'USD'}).format(cost.accrued||0)+(cost.estimated?' estimated':'')}
 catch{return (cost.currency||'')+' '+Number(cost.accrued||0).toFixed(2)+(cost.estimated?' estimated':'')}
}
function renderCosts(value){
 const root=$('#cost-overview'),coverage=value.availableSlotCount+' of '+value.slots.length+' slots reported';
 root.replaceChildren(node('p',(value.total.available?'Available slot total: '+formatCost(value.total):'Total unavailable')+' · '+coverage));
 root.append(value.slots.length?dataTable(['Slot','State','Box','Current period','Provider detail'],value.slots.map(s=>[s.ordinal,s.state,s.logicalBoxName||'—',formatCost(s.cost),s.cost.detail||'—'])):node('p','No compute slots configured.'));
 if(value.unavailableSlotCount)root.append(node('p','Some service costs are unavailable. The provider detail above explains each missing amount.'));
 if(value.observedAt)root.append(node('p','Observed '+new Date(value.observedAt).toLocaleString()+'.'));
 root.append(rawDetails(value));
}
$('#load-costs').addEventListener('click',action(async()=>{
 if(costLoading)return;if(!defaults)throw Error('Configure controller default first.');
 const target={provider:defaults.provider,providerCredential:defaults.providerCredential},version=epoch;costLoading=true;$('#load-costs').disabled=true;$('#cost-overview').replaceChildren(node('p','Loading provider billing…'));
 try{const q=new URLSearchParams(target),value=await api('/v1/fleet/costs?'+q);if(version!==epoch||defaults?.provider!==target.provider||defaults?.providerCredential!==target.providerCredential)return;costTarget=target;renderCosts(value)}
 catch(err){if(version===epoch)$('#cost-overview').replaceChildren(node('p',err.message))}
 finally{costLoading=false;$('#load-costs').disabled=false}
}));
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
 root.append(node('p',`Loaded capacity: ${workers} workers · ${slots} compute slots. Shared slots compete for their host’s CPU and memory.`));
 for(const fleet of fleetSnapshots){
  root.append(node('h3',poolLabel(fleet.provider,fleet.providerCredential)));
  if(fleet.error){root.append(node('p','Capacity unavailable: '+fleet.error));continue}
  root.append(node('p',`Desired: ${fleet.desiredSlots} · Free: ${fleet.freeSlots} · Occupied: ${fleet.occupiedSlots} · Unhealthy: ${fleet.unhealthySlots}`));
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
 const table=document.createElement('table'),head=document.createElement('tr');['Name','State','Worker / slot','Default agent','CLI','Actions'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const b of boxes){
  const row=document.createElement('tr'),cell=document.createElement('td'),select=document.createElement('select'),status=node('td',b.state),actions=document.createElement('td');row.dataset.boxId=b.id;
  for(const agent of ['claude','codex','opencode','shell']){const o=node('option',agent);o.value=agent;select.append(o)}select.value=b.defaultAgent;select.disabled=b.state==='deleting'||deletingBoxes.has(b.id);
  select.addEventListener('change',action(()=>api(bp(b.id),'PATCH',{defaultAgent:select.value})));cell.append(select);
  const name=node('td',''),link=node(b.state==='deleting'?'span':'a',b.name);link.className='table-text';link.title=b.name;if(b.state!=='deleting')link.href='/boxes/'+encodeURIComponent(b.id);name.append(link);
  status.replaceChildren(tableText(startingBoxes.has(b.id)&&b.state==='hibernated'?'starting':b.state));if(b.restorationState)status.append(tableText(b.restorationState));if(b.failureReason){const details=node('details'),summary=node('summary','Error details');details.append(summary,node('pre',b.failureReason));status.append(details)}
  const remove=button(b.state==='deleting'?'Deleting…':'Delete',async()=>{
   if(deletingBoxes.has(b.id)||!confirm('Delete box "'+b.name+'" and its workspace volume? Running processes will stop and all files in the volume will be permanently deleted. This cannot be undone. Shared fleet services and other boxes are kept.'))return;
   const version=epoch;deletingBoxes.add(b.id);remove.disabled=true;remove.textContent='Requesting deletion…';
   try{await api(bp(b.id)+'/volume','DELETE',{confirmation:b.name});const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}
   catch(err){if(version===epoch){remove.disabled=false;remove.textContent='Delete';throw err}}
   finally{deletingBoxes.delete(b.id)}
  });remove.setAttribute('aria-label','Delete box '+b.name);remove.disabled=b.state==='deleting'||deletingBoxes.has(b.id);actions.append(remove);
  const placement=node('td',boxPlacement(b));placement.className='box-placement';const cli=node('td');cli.append(tableText('vmbox '+JSON.stringify(b.name)));row.append(name,status,placement,cell,cli,actions);table.append(row);
 }$('#box-list').replaceChildren(table);
 if(startingBoxes.size||boxes.some(b=>['attaching','reserved','hibernating','deleting'].includes(b.state))){const version=epoch;boxRefreshTimer=setTimeout(async()=>{try{const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}catch(err){if(version===epoch)$('#error').textContent='Could not check box progress. Use Refresh to retry. '+err.message}},5000)}
}
function renderProfiles(identity,profiles){
 const tree=document.createElement('details');tree.open=true;tree.append(node('summary',identity.accountName+' ('+identity.accountId+')'));
 const choices=$('#profile-choices'),selected={};choices.querySelectorAll('select').forEach(s=>selected[s.name]=s.value);choices.replaceChildren();
 for(const app of ['claude','codex','opencode','github']){
  const entries=profiles.filter(p=>p.application===app),branch=document.createElement('details');branch.open=true;branch.append(node('summary',app+' ('+entries.length+')'));const list=document.createElement('ul');
  for(const p of entries){const item=node('li',p.name+' · saved '+p.createdAt+' ');item.append(button('Delete',async()=>{if(!confirm('Delete saved profile '+app+' / '+p.name+'? This cannot be undone. Existing boxes keep their copied credentials; pending creations using this profile may fail.'))return;await api('/v1/login-profiles/'+encodeURIComponent(app)+'/'+encodeURIComponent(p.name),'DELETE');await refresh()}));list.append(item)}if(!entries.length)list.append(node('li','No saved profiles'));branch.append(list);tree.append(branch);
  const label=node('label',app+' login '),select=document.createElement('select');select.name=app;const empty=node('option','None');empty.value='';select.append(empty);
  for(const p of entries){const option=node('option',p.name);option.value=p.name;select.append(option)}if(entries.some(p=>p.name===selected[app]))select.value=selected[app];label.append(select);choices.append(label);
 }$('#profile-tree').replaceChildren(tree);
}
async function refresh(){
 const version=epoch,[caps,boxes]=await Promise.all([api('/v1/capabilities'),api('/v1/logical-boxes')]);if(version!==epoch)return;
 document.querySelectorAll('[data-owner]').forEach(n=>n.hidden=!caps.providerEdits);
 $('#run-once').hidden=!caps.providerEdits||location.hash!=='#run-once';
 renderBoxes(boxes);
 if(!caps.providerEdits)return;
 const [providers,schema,notifications,identity,profiles,toolPresets]=await Promise.all([api('/v1/provider-credentials'),api('/v1/provider-schemas'),api('/v1/notifications'),api('/v1/whoami'),api('/v1/login-profiles'),api('/v1/tool-presets')]);if(version!==epoch)return;
 renderPoolChoices(providers);
 const fleets=await Promise.all(providers.map(async provider=>{const target={provider:provider.provider,providerCredential:provider.name||''};try{return {...await api('/v1/fleet/status?'+new URLSearchParams(target)),...target}}catch(err){return {...target,error:err.message}}}));if(version!==epoch)return;fleetSnapshots=fleets;updateBoxPlacements(boxes);
 const chosenTools=new Set([...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value));$('#create-tools').replaceChildren(node('legend','Optional tools'));
 for(const preset of toolPresets){const label=node('label'),input=node('input');input.type='checkbox';input.value=preset.id;input.checked=chosenTools.has(preset.id);label.title=preset.version+' — '+preset.description;label.append(input,document.createTextNode(preset.name));$('#create-tools').append(label)}
 enforceDesktopToolDependency($('#create-tools'));
 renderProfiles(identity,profiles);
 $('#provider-list').replaceChildren();
 if(!providers.length)$('#provider-list').append(node('p','No providers configured. Add one below, validate it, then select it as the default.'));
 for(const p of providers){const line=node('p',p.provider+' / '+p.name+' ');line.append(button('Edit',()=>{const f=$('#provider').elements;f.provider.value=p.provider;f.alias.value=p.name;f.config.value=JSON.stringify(p.config||{},null,2);f.secret.value='';f.revision.value=p.updatedAt;$('#provider-editor').open=true;f.config.focus()}),button('Validate',async()=>{const result=await api(pp(p.provider,p.name)+'/validate','POST',{});$('#provider-result').textContent=(result.valid?'Validation passed. ':'Validation failed. ')+'Checked: '+(result.checked||[]).join(', ')+'. Not checked: '+(result.unchecked||[]).join(', ')}),button('Use as default',async()=>{await api('/v1/controller-defaults','PUT',{provider:p.provider,providerCredential:p.name});await refresh()}));const details=document.createElement('details');details.append(node('summary','Configuration'),dataTable(['Setting','Value'],Object.entries(p.config||{}).map(([key,value])=>[key,typeof value==='object'?JSON.stringify(value):String(value)])));$('#provider-list').append(line,details)}
 $('#schema').textContent=JSON.stringify(schema,null,2);renderNotifications(notifications);defaults=null;
 try{const d=await api('/v1/controller-defaults');if(version!==epoch)return;$('#provider-default').textContent='Default: '+d.provider+' / '+d.providerCredential;if(locationTarget&&(locationTarget.provider!==d.provider||locationTarget.providerCredential!==d.providerCredential))resetLocation();if(costTarget&&(costTarget.provider!==d.provider||costTarget.providerCredential!==d.providerCredential))resetCosts();defaults=d;renderWorkerCapacity()}catch(err){if(version===epoch){renderWorkerCapacity();$('#provider-default').textContent='Check the default provider and capacity configuration.'}}
}
$('#login').addEventListener('submit',action(async e=>{token=e.target.elements.token.value;try{await api('/v1/browser-session','POST',{})}finally{token='';e.target.reset()}await refresh();$('#login').hidden=true;$('#app').hidden=false}));
$('#logout').addEventListener('click',action(async()=>{await api('/v1/browser-session','DELETE');epoch++;resetLocation();resetCosts();clearTimeout(boxRefreshTimer);startingBoxes.clear();token='';defaults=null;fleetSnapshots=[];renderPoolChoices([]);$('#capacity').replaceChildren();$('#box-list').replaceChildren();$('#app').hidden=true;$('#login').hidden=false;document.querySelectorAll('form').forEach(f=>f.reset());$('#profile-tree').replaceChildren();$('#profile-choices').replaceChildren();$('#error').textContent=''}));
$('#refresh').addEventListener('click',action(refresh));
$('#create').addEventListener('submit',action(async e=>{const f=e.target.elements,loginProfiles=Array.from($('#profile-choices').querySelectorAll('select')).filter(s=>s.value).map(s=>({application:s.name,name:s.value})),tools=[...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value),setupScript=f.setupScript.value,d=f.pool.value?JSON.parse(f.pool.value):await chooseCreationPool(tools);const created=await api('/v1/logical-boxes','POST',{name:f.name.value,defaultAgent:f.defaultAgent.value,diskGiB:Number(f.disk.value),provider:d.provider,providerCredential:d.providerCredential,allocateWhenReady:true,loginProfiles,...(tools.length?{tools}:{}),...(setupScript.trim()?{setupScript}:{})},{'Idempotency-Key':crypto.randomUUID()});if(created?.id)startingBoxes.add(created.id);await refresh()}));
$('#provider').addEventListener('submit',action(async e=>{const f=e.target.elements,rev=f.revision.value,body={config:JSON.parse(f.config.value)};if(f.secret.value){body.secret=JSON.parse(f.secret.value);if(rev)body.replaceSecret=true}await api(pp(f.provider.value,f.alias.value),rev?'PATCH':'PUT',body,rev?{'If-Match':rev}:{});e.target.reset();await refresh()}));
$('#slots').addEventListener('submit',action(async e=>{const target=$('#capacity-pool').value?JSON.parse($('#capacity-pool').value):defaults;if(!target)throw Error('Choose a worker pool first');await api('/v1/fleet/slots','PUT',{...target,compute_box_slots:Number(e.target.elements.count.value)});await refresh()}));
$('#notification').addEventListener('submit',action(async e=>{const f=e.target.elements,split=s=>s.split(',').map(v=>v.trim()).filter(Boolean);await api('/v1/notifications/'+encodeURIComponent(f.kind.value)+'/'+encodeURIComponent(f.name.value),'PUT',{config:JSON.parse(f.config.value),secret:JSON.parse(f.secret.value),allowedUsers:split(f.users.value),allowedChats:split(f.chats.value)});e.target.reset();await refresh()}));
