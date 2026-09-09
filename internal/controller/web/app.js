'use strict';
const $=s=>document.querySelector(s);
let token='',defaults=null,epoch=0;
let boxRefreshTimer;
const deletingBoxes=new Set();
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
function renderCapacity(fleet){
 const root=$('#capacity');root.replaceChildren(node('p',`Desired: ${fleet.desiredSlots??'—'} · Total: ${fleet.actualSlots??'—'} · Free: ${fleet.freeSlots??'—'} · Occupied: ${fleet.occupiedSlots??'—'} · Unhealthy: ${fleet.unhealthySlots??'—'}`));
 const slots=fleet.slots||[];
 root.append(slots.length?dataTable(['Slot','State','Health','Location','Box'],slots.map(s=>[s.ordinal,s.state,s.health,s.region,s.logicalBoxName||'—'])):node('p','No compute slots configured. Set capacity below to provision compute.'));
 const detached=fleet.detachedLogicalBoxes||[];if(detached.length)root.append(node('h3','Detached workspaces'),dataTable(['Box','State'],detached.map(b=>[b.name,b.state])));
 if(fleet.unhealthySlots)root.append(node('p','Some slots are unhealthy. Check provider deployments and controller diagnostics before increasing capacity.'));
 root.append(rawDetails(fleet));const input=$('#slots input');if(document.activeElement!==input)input.value=fleet.desiredSlots??'';
}
function renderNotifications(values){const root=$('#destinations');root.replaceChildren();if(!values.length){root.append(node('p','No notification destinations configured. Notifications are optional.'));return}root.append(dataTable(['Name','Type','Status','Allowed users','Allowed chats'],values.map(n=>[n.name,n.kind,n.enabled?'Enabled':'Disabled',(n.allowedUsers||[]).join(', ')||'Not specified',(n.allowedChats||[]).join(', ')||'Not specified'])),rawDetails(values))}
const bp=id=>'/v1/logical-boxes/'+encodeURIComponent(id),pp=(p,n)=>'/v1/provider-credentials/'+encodeURIComponent(p)+'/'+encodeURIComponent(n);
function renderBoxes(boxes){
 clearTimeout(boxRefreshTimer);
 const table=document.createElement('table'),head=document.createElement('tr');['Name','State','Default agent','CLI','Actions'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const b of boxes){
  const row=document.createElement('tr'),cell=document.createElement('td'),select=document.createElement('select'),status=node('td',b.state),actions=document.createElement('td');row.dataset.boxId=b.id;
  for(const agent of ['claude','codex','opencode','shell']){const o=node('option',agent);o.value=agent;select.append(o)}select.value=b.defaultAgent;select.disabled=b.state==='deleting'||deletingBoxes.has(b.id);
  select.addEventListener('change',action(()=>api(bp(b.id),'PATCH',{defaultAgent:select.value})));cell.append(select);
  const name=node('td',''),link=node(b.state==='deleting'?'span':'a',b.name);link.className='table-text';link.title=b.name;if(b.state!=='deleting')link.href='/boxes/'+encodeURIComponent(b.id);name.append(link);
  status.replaceChildren(tableText(b.state));if(b.restorationState)status.append(tableText(b.restorationState));if(b.failureReason){const details=node('details'),summary=node('summary','Error details');details.append(summary,node('pre',b.failureReason));status.append(details)}
  const remove=button(b.state==='deleting'?'Deleting…':'Delete',async()=>{
   if(deletingBoxes.has(b.id)||!confirm('Delete box "'+b.name+'" and its workspace volume? Running processes will stop and all files in the volume will be permanently deleted. This cannot be undone. Shared fleet services and other boxes are kept.'))return;
   const version=epoch;deletingBoxes.add(b.id);remove.disabled=true;remove.textContent='Requesting deletion…';
   try{await api(bp(b.id)+'/volume','DELETE',{confirmation:b.name});const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}
   catch(err){if(version===epoch){remove.disabled=false;remove.textContent='Delete';throw err}}
   finally{deletingBoxes.delete(b.id)}
  });remove.setAttribute('aria-label','Delete box '+b.name);remove.disabled=b.state==='deleting'||deletingBoxes.has(b.id);actions.append(remove);
  const cli=node('td');cli.append(tableText('vmbox '+JSON.stringify(b.name)));row.append(name,status,cell,cli,actions);table.append(row);
 }$('#box-list').replaceChildren(table);
 if(boxes.some(b=>b.state==='deleting')){const version=epoch;boxRefreshTimer=setTimeout(async()=>{try{const boxes=await api('/v1/logical-boxes');if(version===epoch)renderBoxes(boxes)}catch(err){if(version===epoch)$('#error').textContent='Could not check deletion progress. Use Refresh to retry. '+err.message}},5000)}
}
function renderProfiles(identity,profiles){
 const tree=document.createElement('details');tree.open=true;tree.append(node('summary',identity.accountName+' ('+identity.accountId+')'));
 const choices=$('#profile-choices'),selected={};choices.querySelectorAll('select').forEach(s=>selected[s.name]=s.value);choices.replaceChildren();
 for(const app of ['claude','codex','github']){
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
 const chosenTools=new Set([...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value));$('#create-tools').replaceChildren(node('legend','Optional tools'));
 for(const preset of toolPresets){const label=node('label'),input=node('input');input.type='checkbox';input.value=preset.id;input.checked=chosenTools.has(preset.id);label.title=preset.version+' — '+preset.description;label.append(input,document.createTextNode(preset.name));$('#create-tools').append(label)}
 renderProfiles(identity,profiles);
 $('#provider-list').replaceChildren();
 if(!providers.length)$('#provider-list').append(node('p','No providers configured. Add one below, validate it, then select it as the default.'));
 for(const p of providers){const line=node('p',p.provider+' / '+p.name+' ');line.append(button('Edit',()=>{const f=$('#provider').elements;f.provider.value=p.provider;f.alias.value=p.name;f.config.value=JSON.stringify(p.config||{},null,2);f.secret.value='';f.revision.value=p.updatedAt;$('#provider-editor').open=true;f.config.focus()}),button('Validate',async()=>{const result=await api(pp(p.provider,p.name)+'/validate','POST',{});$('#provider-result').textContent=(result.valid?'Validation passed. ':'Validation failed. ')+'Checked: '+(result.checked||[]).join(', ')+'. Not checked: '+(result.unchecked||[]).join(', ')}),button('Use as default',async()=>{await api('/v1/controller-defaults','PUT',{provider:p.provider,providerCredential:p.name});await refresh()}));const details=document.createElement('details');details.append(node('summary','Configuration'),dataTable(['Setting','Value'],Object.entries(p.config||{}).map(([key,value])=>[key,typeof value==='object'?JSON.stringify(value):String(value)])));$('#provider-list').append(line,details)}
 $('#schema').textContent=JSON.stringify(schema,null,2);renderNotifications(notifications);defaults=null;
 try{const d=await api('/v1/controller-defaults');if(version!==epoch)return;$('#provider-default').textContent='Default: '+d.provider+' / '+d.providerCredential;const q=new URLSearchParams({provider:d.provider,providerCredential:d.providerCredential}),fleet=await api('/v1/fleet/status?'+q);if(version!==epoch)return;defaults=d;renderCapacity(fleet)}catch(err){if(version===epoch){$('#capacity').textContent=err.message;if(!defaults)$('#provider-default').textContent='Check the default provider and capacity configuration.'}}
}
$('#login').addEventListener('submit',action(async e=>{token=e.target.elements.token.value;try{await api('/v1/browser-session','POST',{})}finally{token='';e.target.reset()}await refresh();$('#login').hidden=true;$('#app').hidden=false}));
$('#logout').addEventListener('click',action(async()=>{await api('/v1/browser-session','DELETE');epoch++;clearTimeout(boxRefreshTimer);token='';defaults=null;$('#app').hidden=true;$('#login').hidden=false;document.querySelectorAll('form').forEach(f=>f.reset());$('#profile-tree').replaceChildren();$('#profile-choices').replaceChildren();$('#error').textContent=''}));
$('#refresh').addEventListener('click',action(refresh));
$('#create').addEventListener('submit',action(async e=>{const f=e.target.elements,d=await api('/v1/controller-defaults'),loginProfiles=Array.from($('#profile-choices').querySelectorAll('select')).filter(s=>s.value).map(s=>({application:s.name,name:s.value})),tools=[...$('#create-tools').querySelectorAll('input:checked')].map(i=>i.value),setupScript=f.setupScript.value;await api('/v1/logical-boxes','POST',{name:f.name.value,diskGiB:Number(f.disk.value),provider:d.provider,providerCredential:d.providerCredential,loginProfiles,...(tools.length?{tools}:{}),...(setupScript.trim()?{setupScript}:{})},{'Idempotency-Key':crypto.randomUUID()});await refresh()}));
$('#provider').addEventListener('submit',action(async e=>{const f=e.target.elements,rev=f.revision.value,body={config:JSON.parse(f.config.value)};if(f.secret.value){body.secret=JSON.parse(f.secret.value);if(rev)body.replaceSecret=true}await api(pp(f.provider.value,f.alias.value),rev?'PATCH':'PUT',body,rev?{'If-Match':rev}:{});e.target.reset();await refresh()}));
$('#slots').addEventListener('submit',action(async e=>{if(!defaults)throw Error('Configure controller default first');await api('/v1/fleet/slots','PUT',{provider:defaults.provider,providerCredential:defaults.providerCredential,compute_box_slots:Number(e.target.elements.count.value)});await refresh()}));
$('#notification').addEventListener('submit',action(async e=>{const f=e.target.elements,split=s=>s.split(',').map(v=>v.trim()).filter(Boolean);await api('/v1/notifications/'+encodeURIComponent(f.kind.value)+'/'+encodeURIComponent(f.name.value),'PUT',{config:JSON.parse(f.config.value),secret:JSON.parse(f.secret.value),allowedUsers:split(f.users.value),allowedChats:split(f.chats.value)});e.target.reset();await refresh()}));
