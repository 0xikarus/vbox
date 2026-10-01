'use strict';
window.VMBoxWorkspaceNav=(()=>{
 const el=(tag,text)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node};
 const read=async path=>{const response=await fetch(path,{credentials:'same-origin',cache:'no-store'});const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||`Could not load provider data (${response.status})`);return data};
 const number=value=>Number.isFinite(Number(value))?Number(value):0;
 const gib=bytes=>(number(bytes)/(1024**3)).toFixed(1)+' GiB';
 async function openProviderDelete(provider,onDeleted){
  const path='/v1/provider-credentials/'+encodeURIComponent(provider.provider)+'/'+encodeURIComponent(provider.name);
  const dialog=el('dialog');dialog.className='vb-sheet-dialog provider-delete-dialog';dialog.setAttribute('aria-label','Delete provider');
  const form=el('form');form.className='vb-sheet-form';form.method='dialog';
  const header=el('header');header.className='vb-sheet-header';const title=el('h2','Delete '+provider.name);const close=el('button','×');close.type='button';close.setAttribute('aria-label','Close delete dialog');close.onclick=()=>dialog.close();header.append(title,close);
  const frame=el('div');frame.className='sheet-scroll-frame';const body=el('div');body.className='sheet-scroll-body';const status=el('p','Loading deletion plan…');status.setAttribute('role','status');body.append(status);frame.append(body);
  const footer=el('div');footer.className='vb-sheet-footer';const cancel=el('button','Cancel');cancel.type='button';cancel.onclick=()=>dialog.close();const confirm=el('button','Delete provider');confirm.type='submit';confirm.className='primary danger';confirm.disabled=true;footer.append(cancel,confirm);form.append(header,frame,footer);dialog.append(form);document.body.append(dialog);
  dialog.addEventListener('close',()=>dialog.remove(),{once:true});dialog.showModal();
  let plan;
  const input=el('input');input.type='text';input.autocomplete='off';input.setAttribute('aria-label','Type provider name to confirm');
  const replacement=el('select');replacement.setAttribute('aria-label','New default provider');
  const ready=()=>{confirm.disabled=!plan?.canDelete||input.value!==provider.name||(plan.isDefault&&!replacement.value)};
  input.addEventListener('input',ready);replacement.addEventListener('change',ready);
  try{
   plan=await read(path+'/delete-plan');if(!dialog.open)return;
   body.replaceChildren();
   const boxCount=plan.boxes.length;
   const summary=el('p',boxCount?`${boxCount} ${boxCount===1?'box still uses':'boxes still use'} this provider. Delete or move ${boxCount===1?'it':'them'} first.`:'No boxes use this provider.');body.append(summary);
   if(plan.boxes.length){const list=el('ul');for(const box of plan.boxes){const item=el('li');const link=el('a',box.name+' · '+box.state);link.href='/chat#box='+encodeURIComponent(box.id);item.append(link);list.append(item)}body.append(list)}
   const slotCount=plan.slots.length,cloudCount=plan.cloudServers;const workers=el('p',`${slotCount} worker ${slotCount===1?'slot':'slots'}; ${cloudCount} cloud ${cloudCount===1?'server':'servers'} will be deprovisioned.`);body.append(workers);
   const otherBlockers=plan.blockers.filter(blocker=>!boxCount||!blocker.startsWith('Delete or move every box'));
   if(otherBlockers.length){const list=el('ul');list.className='provider-delete-blockers';for(const blocker of otherBlockers)list.append(el('li',blocker));body.append(list)}
   if(plan.canDelete){
    if(plan.isDefault){const label=el('label','New default');const prompt=el('option','Choose a replacement');prompt.value='';replacement.append(prompt);const clear=el('option','No default');clear.value='clear';replacement.append(clear);const providers=await read('/v1/provider-credentials');for(const candidate of providers){if(candidate.provider===provider.provider&&candidate.name===provider.name)continue;const option=el('option',candidate.provider+' / '+candidate.name);option.value=JSON.stringify({provider:candidate.provider,name:candidate.name});replacement.append(option)}label.append(replacement);body.append(label)}
    const label=el('label','Type '+provider.name+' to confirm');label.append(input);body.append(label);
   }else{confirm.remove();cancel.textContent='Close'}
   ready();
  }catch(error){status.textContent=error.message;if(!body.contains(status))body.append(status);return}
  form.addEventListener('submit',async event=>{event.preventDefault();if(confirm.disabled)return;confirm.disabled=true;status.textContent='Deleting provider…';body.append(status);try{
   const newDefault=plan.isDefault?(replacement.value==='clear'?null:JSON.parse(replacement.value)):undefined;
   const response=await fetch(path,{method:'DELETE',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(plan.isDefault?{newDefault}:{})});
   if(!response.ok){const data=await response.json().catch(()=>({}));throw Error(data.error||`Delete failed (${response.status})`)}
   dialog.close();await onDeleted?.();
  }catch(error){status.textContent=error.message;ready()}});
 }
 function age(timestamp){const time=Date.parse(timestamp);if(!Number.isFinite(time))return '—';const seconds=Math.max(0,Math.floor((Date.now()-time)/1000));if(seconds<60)return 'Just now';const minutes=Math.floor(seconds/60);if(minutes<60)return minutes+' min ago';const hours=Math.floor(minutes/60);if(hours<24)return hours+' hr ago';return Math.floor(hours/24)+' d ago'}
 function initProviders(id){
  const button=document.getElementById(id);if(!button)return {setOwner(){}};
  const dialog=el('dialog');dialog.className='vb-sheet-dialog providers-dialog';dialog.setAttribute('aria-labelledby',id+'-title');
  const header=el('header');header.className='vb-sheet-header';const title=el('h2','Providers');title.id=id+'-title';
  const refresh=el('button','Refresh');refresh.type='button';refresh.className='providers-refresh';
  const link=el('a','Open providers →');link.href='/#providers';link.className='providers-open-link';
  const close=el('button','×');close.type='button';close.className='providers-close';close.setAttribute('aria-label','Close providers');close.onclick=()=>dialog.close();
  header.append(title,refresh,link,close);
  const frame=el('div');frame.className='sheet-scroll-frame';const body=el('div');body.className='sheet-scroll-body';
  const status=el('p');status.className='providers-status';status.setAttribute('role','status');
  const table=el('table');table.className='providers-table';const head=el('thead'),heading=el('tr');
  for(const label of ['Provider','Workers','Slots','RAM used / total','Swap used / total','Observed',''])heading.append(el('th',label));
  head.append(heading);const rows=el('tbody');table.append(head,rows);body.append(status,table);frame.append(body);dialog.append(header,frame);document.body.append(dialog);
  let owner=false,generation=0;
  function cell(label,value){const td=el('td',value);td.dataset.label=label;return td}
  function renderRow(row,provider,isDefault,snapshot){
   const {fleet,config,host,errors}=snapshot;
   row.replaceChildren();
   const identity=cell('Provider');identity.className='providers-identity';const name=el('strong',provider.name||'Default');const type=el('small',provider.provider==='shared-worker'?'Shared worker':provider.provider);identity.append(name,type);
   if(isDefault){const badge=el('span','Default');badge.className='providers-default';identity.append(badge)}
   if(errors.length){const error=el('p',errors.join(' · '));error.className='providers-row-error';identity.append(error)}
   const slots=Array.isArray(fleet?.slots)?fleet.slots:[];
   const workers=fleet?(provider.provider==='shared-worker'?(slots.length?1:0):new Set(slots.map(slot=>slot.serviceId||slot.id).filter(Boolean)).size):'—';
   const used=Number.isFinite(fleet?.occupiedSlots)?fleet.occupiedSlots:'—',free=Number.isFinite(fleet?.freeSlots)?fleet.freeSlots:'—';
   const capacity=Number.isFinite(config?.compute_box_slots)?config.compute_box_slots:null;
   const slotCell=cell('Slots');slotCell.append(el('span',used+' used · '+free+' free'));
   if(capacity!==null){const configured=el('small','of '+capacity+' configured');configured.className='providers-configured';slotCell.append(configured)}
   const resource=(label,usedBytes,totalBytes,unavailable)=>{
    const total=number(totalBytes),used=Math.max(0,number(usedBytes)),item=cell(label,unavailable?'Unavailable':total?gib(used)+' / '+gib(total):label==='Swap'?'No swap':'—');
    if(!unavailable&&total){const ratio=used/total;if(ratio>=.95)item.classList.add('is-danger');else if(ratio>=.85)item.classList.add('is-warning')}return item;
   };
   const ram=resource('RAM',number(host?.memoryTotalBytes)-number(host?.memoryAvailableBytes),host?.memoryTotalBytes,!!snapshot.hostError);
   const swap=resource('Swap',number(host?.swapTotalBytes)-number(host?.swapFreeBytes),host?.swapTotalBytes,!!snapshot.hostError);
   if(provider.provider!=='shared-worker'){ram.textContent='—';swap.textContent='—'}
   const observed=cell('Observed',age(host?.observedAt));if(host?.observedAt)observed.title=new Date(host.observedAt).toLocaleString();
   const actions=cell('Actions');actions.className='providers-actions-space';const remove=el('button','Delete');remove.type='button';remove.className='providers-delete';remove.onclick=()=>void openProviderDelete(provider,load);actions.append(remove);
   row.append(identity,cell('Workers',String(workers)),slotCell,ram,swap,observed,actions);
  }
  async function load(){
   const request=++generation;refresh.disabled=true;status.textContent='Loading providers…';table.hidden=true;rows.replaceChildren();
   try{
    const [providers,defaultResult]=await Promise.all([read('/v1/provider-credentials'),read('/v1/controller-defaults').catch(()=>null)]);
    if(request!==generation||!dialog.open)return;
    if(!Array.isArray(providers))throw Error('Provider list unavailable');
    status.textContent=providers.length?'':'No providers configured.';table.hidden=!providers.length;
    await Promise.all(providers.map(async provider=>{
     const row=el('tr');rows.append(row);const identity=cell('Provider',provider.name||'Default');identity.className='providers-identity';row.append(identity,cell('Status','Loading capacity…'));
     const query=new URLSearchParams({provider:provider.provider,providerCredential:provider.name||''});
     const results=await Promise.allSettled([read('/v1/fleet/status?'+query),read('/v1/fleet/slots?'+query),provider.provider==='shared-worker'?read('/v1/fleet/host-resources?'+query):Promise.resolve(null)]);
     if(request!==generation||!dialog.open)return;
     const value=index=>results[index].status==='fulfilled'?results[index].value:null;
     const errors=[];if(results[0].status==='rejected')errors.push('Capacity unavailable: '+results[0].reason.message);if(results[1].status==='rejected')errors.push('Slot settings unavailable: '+results[1].reason.message);
     const hostError=results[2].status==='rejected'?results[2].reason.message:'';if(hostError)errors.push('Resource usage unavailable: '+hostError);
     renderRow(row,provider,defaultResult?.provider===provider.provider&&defaultResult?.providerCredential===(provider.name||''),{fleet:value(0),config:value(1),host:value(2),hostError,errors});
    }));
   }catch(error){if(request===generation&&dialog.open){status.textContent=error.message;table.hidden=true}}
   finally{if(request===generation)refresh.disabled=false}
  }
  button.onclick=()=>{if(!owner)return;dialog.showModal();void load()};refresh.onclick=()=>void load();
  dialog.addEventListener('click',event=>{if(event.target===dialog)dialog.close()});
  dialog.addEventListener('close',()=>{generation++;button.focus()});
  return {setOwner(value){owner=!!value;button.hidden=!owner;if(!owner&&dialog.open)dialog.close()}};
 }
 function updateUsagePill(button,_profiles,owner=true){
  if(!button)return;
  button.hidden=!owner;
  button.textContent='Usage';
  button.setAttribute('aria-label','Usage');
  button.title='Usage';
 }
 function init({menuId,panelId,usageId,providersId}){
  const menu=menuId?document.getElementById(menuId):null,panel=panelId?document.getElementById(panelId):null,usage=document.getElementById(usageId);
  const providers=initProviders(providersId);
  if(!usage)return {setOwner(){},closeMenu(){}};
  const dialog=document.createElement('dialog');dialog.className='workspace-usage-dialog vb-sheet-dialog';dialog.setAttribute('aria-label','Profile usage limits');
  const header=document.createElement('header');header.className='vb-sheet-header';const title=document.createElement('h2');title.textContent='Profile usage limits';
  const close=document.createElement('button');close.type='button';close.textContent='Close';close.onclick=()=>dialog.close();header.append(title,close);
  const status=document.createElement('p');status.setAttribute('role','status');
  const list=document.createElement('div'),frame=document.createElement('div'),body=document.createElement('div');frame.className='sheet-scroll-frame';body.className='sheet-scroll-body';body.append(status,list);frame.append(body);dialog.append(header,frame);document.body.append(dialog);
  const closeMenu=()=>{if(!panel||!menu)return;panel.hidden=true;menu.setAttribute('aria-expanded','false')};
  if(menu&&panel)menu.onclick=()=>{
   if(!panel.hidden){closeMenu();return}
   const rect=menu.getBoundingClientRect();panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';panel.style.top=(rect.bottom+5)+'px';panel.hidden=false;menu.setAttribute('aria-expanded','true');
   // The panel's width is measurable only after it is shown.
   panel.style.left=Math.max(8,Math.min(rect.left,innerWidth-panel.offsetWidth-8))+'px';
  };
  if(menu&&panel){document.addEventListener('pointerdown',event=>{if(!panel.hidden&&!panel.contains(event.target)&&event.target!==menu)closeMenu()});
  document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!panel.hidden){event.preventDefault();closeMenu();menu.focus()}})}
  const format=value=>new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(value);
  let owner=false;
  async function showUsage(){
   closeMenu();list.replaceChildren();status.textContent='Loading profile usage…';dialog.showModal();
   try{
    const response=await fetch('/v1/profile-usage',{credentials:'same-origin',cache:'no-store'});
    const data=await response.json().catch(()=>({}));if(!response.ok)throw Error(data.error||'Could not load usage');
    if(!dialog.open)return;
    const profiles=Array.isArray(data.profiles)?data.profiles:[];
    updateUsagePill(usage,profiles,owner);
    status.textContent=profiles.length?'':'No saved agent profiles yet.';
    for(const profile of profiles){
     const card=document.createElement('article');card.className='workspace-usage-profile';
     const heading=document.createElement('h3');heading.textContent=profile.application+' · '+profile.name;card.append(heading);
     const windows=profile.snapshot?.windows||[];
     if(!windows.length){const note=document.createElement('p');note.textContent=profile.error||'Remaining usage unavailable.';card.append(note)}
     const shortNames={session:'Session',weekly_all:'Week',weekly_scoped:'Week',primary:'Primary',secondary:'Secondary'};
     const duration=minutes=>!minutes?'':minutes>=1440?Math.round(minutes/1440)+'d':minutes>=60?Math.round(minutes/60)+'h':minutes+' min';
     for(const window of windows){
      const percent=Number.isFinite(window.usedPercent)?Math.max(0,Math.min(100,100-window.usedPercent)):null;
      const row=document.createElement('div');row.className='workspace-usage-window';
      if(percent!==null)row.dataset.level=percent>30?'ok':percent>=10?'warn':'low';
      const label=document.createElement('span');const raw=window.name||'';let text=shortNames[raw]||(/^[a-z]+$/.test(raw)?raw.charAt(0).toUpperCase()+raw.slice(1):raw)||'Limit';const dur=duration(window.durationMinutes);if(dur)text+=' · '+dur;else if(window.scope)text+=' · '+window.scope;label.textContent=text;
      const bar=document.createElement('span');bar.className='usage-bar';
      if(percent!==null){const fill=document.createElement('i');fill.style.width=percent+'%';bar.append(fill)}else bar.classList.add('usage-bar-empty');
      const remaining=document.createElement('strong');remaining.textContent=percent!==null?format(percent)+'% left':'—';if(percent!==null&&percent<=10)remaining.classList.add('low');
      row.append(label,bar,remaining);card.append(row);
     }
     if(profile.snapshot?.note){const note=document.createElement('p');note.textContent=profile.snapshot.note;card.append(note)}
     list.append(card);
    }
   }catch(error){if(dialog.open)status.textContent=error.message}
  }
  usage.onclick=()=>void showUsage();
  return {setOwner(value){owner=!!value;updateUsagePill(usage,null,owner);providers.setOwner(owner);if(!owner&&dialog.open)dialog.close()},closeMenu,openProviderDelete};
 }
 return {init,initProviders,updateUsagePill};
})();
