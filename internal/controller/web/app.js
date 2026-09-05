'use strict';
const $=s=>document.querySelector(s);
let token='',defaults=null,epoch=0;
async function api(path,method='GET',body,headers={}){
 const r=await fetch(path,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body)});
 if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}return r.status===204?null:r.json();
}
function action(fn){return async e=>{e?.preventDefault();$('#error').textContent='';try{await fn(e)}catch(err){$('#error').textContent=err.message}}}
function node(tag,text){const n=document.createElement(tag);n.textContent=text;return n}
function button(text,fn){const b=node('button',text);b.type='button';b.addEventListener('click',action(fn));return b}
const bp=id=>'/v1/logical-boxes/'+encodeURIComponent(id),pp=(p,n)=>'/v1/provider-credentials/'+encodeURIComponent(p)+'/'+encodeURIComponent(n);
async function refresh(){
 const version=epoch,[caps,boxes]=await Promise.all([api('/v1/capabilities'),api('/v1/logical-boxes')]);if(version!==epoch)return;
 document.querySelectorAll('[data-owner]').forEach(n=>n.hidden=!caps.providerEdits);
 const table=document.createElement('table'),head=document.createElement('tr');['Name','State','Default agent','CLI'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const b of boxes){const row=document.createElement('tr'),cell=document.createElement('td'),select=document.createElement('select');
 for(const agent of ['claude','codex','opencode','shell']){const o=node('option',agent);o.value=agent;select.append(o)}select.value=b.defaultAgent;
 select.addEventListener('change',action(()=>api(bp(b.id),'PATCH',{defaultAgent:select.value})));cell.append(select);row.append(node('td',b.name),node('td',b.state),cell,node('td','vmbox '+JSON.stringify(b.name)));table.append(row)}$('#box-list').replaceChildren(table);
 if(!caps.providerEdits)return;
 const [providers,schema,notifications]=await Promise.all([api('/v1/provider-credentials'),api('/v1/provider-schemas'),api('/v1/notifications')]);if(version!==epoch)return;
 $('#provider-list').replaceChildren();
 for(const p of providers){const line=node('p',p.provider+' / '+p.name+' ');line.append(button('Edit',()=>{const f=$('#provider').elements;f.provider.value=p.provider;f.alias.value=p.name;f.config.value=JSON.stringify(p.config||{},null,2);f.secret.value='';f.revision.value=p.updatedAt}),button('Validate',async()=>{$('#schema').textContent=JSON.stringify(await api(pp(p.provider,p.name)+'/validate','POST',{}),null,2)}));$('#provider-list').append(line)}
 $('#schema').textContent=JSON.stringify(schema,null,2);$('#destinations').textContent=JSON.stringify(notifications,null,2);defaults=null;
 try{const d=await api('/v1/controller-defaults'),q=new URLSearchParams({provider:d.provider,providerCredential:d.providerCredential}),fleet=await api('/v1/fleet/status?'+q);if(version!==epoch)return;defaults=d;$('#capacity').textContent=JSON.stringify(fleet,null,2)}catch(err){if(version===epoch)$('#capacity').textContent=err.message}
}
$('#login').addEventListener('submit',action(async e=>{token=e.target.elements.token.value;await refresh();e.target.reset();$('#login').hidden=true;$('#app').hidden=false}));
$('#logout').addEventListener('click',()=>{epoch++;token='';defaults=null;$('#app').hidden=true;$('#login').hidden=false;document.querySelectorAll('form').forEach(f=>f.reset());$('#error').textContent=''});
$('#refresh').addEventListener('click',action(refresh));
$('#create').addEventListener('submit',action(async e=>{const f=e.target.elements,d=await api('/v1/controller-defaults');await api('/v1/logical-boxes','POST',{name:f.name.value,diskGiB:Number(f.disk.value),provider:d.provider,providerCredential:d.providerCredential},{'Idempotency-Key':crypto.randomUUID()});await refresh()}));
$('#provider').addEventListener('submit',action(async e=>{const f=e.target.elements,rev=f.revision.value,body={config:JSON.parse(f.config.value)};if(f.secret.value){body.secret=JSON.parse(f.secret.value);if(rev)body.replaceSecret=true}await api(pp(f.provider.value,f.alias.value),rev?'PATCH':'PUT',body,rev?{'If-Match':rev}:{});e.target.reset();await refresh()}));
$('#default').addEventListener('submit',action(async e=>{const f=e.target.elements;await api('/v1/controller-defaults','PUT',{provider:f.provider.value,providerCredential:f.alias.value});await refresh()}));
$('#slots').addEventListener('submit',action(async e=>{if(!defaults)throw Error('Configure controller default first');await api('/v1/fleet/slots','PUT',{provider:defaults.provider,providerCredential:defaults.providerCredential,compute_box_slots:Number(e.target.elements.count.value)});await refresh()}));
$('#notification').addEventListener('submit',action(async e=>{const f=e.target.elements,split=s=>s.split(',').map(v=>v.trim()).filter(Boolean);await api('/v1/notifications/'+encodeURIComponent(f.kind.value)+'/'+encodeURIComponent(f.name.value),'PUT',{config:JSON.parse(f.config.value),secret:JSON.parse(f.secret.value),allowedUsers:split(f.users.value),allowedChats:split(f.chats.value)});e.target.reset();await refresh()}));
$('#coworker-refresh').addEventListener('click',action(async()=>{
 const version=epoch,[coworkers,messages]=await Promise.all([api('/v1/coworkers'),api('/v1/coworkers/messages')]);if(version!==epoch)return;
 $('#coworker-list').replaceChildren(...coworkers.map(c=>node('p',c.name+' · '+c.agent+' · '+c.state+(c.enabled?'':' · disabled'))));
 const table=document.createElement('table'),head=document.createElement('tr');['Sequence','From','To','Kind','Message'].forEach(t=>head.append(node('th',t)));table.append(head);
 for(const m of messages){const row=document.createElement('tr');[m.sequence,m.sender,m.recipient,m.kind,m.data?.text??JSON.stringify(m.data)].forEach(v=>row.append(node('td',v)));table.append(row)}
 $('#coworker-messages').replaceChildren(table);
}));
