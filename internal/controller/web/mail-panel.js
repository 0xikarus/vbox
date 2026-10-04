'use strict';
window.VBoxMailPanel=(()=>{
 const $=selector=>document.querySelector(selector);
 const esc=value=>String(value??'').replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
 const id=value=>encodeURIComponent(value);
 const stamp=value=>{
  if(!value)return '—';const date=new Date(value);if(Number.isNaN(+date))return '—';
  const now=new Date(),minutes=Math.floor((now-date)/60000);
  if(minutes>=0&&minutes<1)return 'Just now';
  if(minutes>=0&&minutes<60)return minutes+' min ago';
  const today=new Date(now.getFullYear(),now.getMonth(),now.getDate()),yesterday=new Date(today);yesterday.setDate(today.getDate()-1);
  if(date>=yesterday&&date<today)return 'Yesterday';
  if(minutes>=0&&date>=today&&minutes<1440)return Math.floor(minutes/60)+' hr ago';
  return new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric',...(date.getFullYear()===now.getFullYear()?{}:{year:'numeric'})}).format(date);
 };
 const otp=message=>/\b(verification|one.time|security|otp|passcode|sign.in code)\b/i.test([message?.subject,message?.text].join(' '))&&/\b\d{4,8}\b/.test(message?.text||message?.preview||'');
 const mask=text=>String(text||'').replace(/\b\d{4,8}\b/g,'••••••');
 const boxId=item=>item?.boxId||item?.id||item?.box?.id||'';
 const boxName=item=>item?.boxName||item?.name||item?.box?.name||'Box';
 const icon=name=>{const paths={read:'<path d="M3 5h18v14H3zM3 7l9 7 9-7"/>',archive:'<path d="M3 4h18v5H3zM5 9v11h14V9M9 13h6"/>',delete:'<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 10v7M14 10v7"/>',chat:'<path d="M4 5h16v12H8l-4 3zM8 9h8M8 12h5"/>',release:'<path d="M5 20h14V9M10 14 20 4M13 4h7v7"/>',unarchive:'<path d="M3 4h18v5H3zM5 9v11h14V9M15 15H9m0 0 3-3m-3 3 3 3"/>',review:'<path d="m5 12 5 5L20 6"/>',paperclip:'<path d="m20 11.5-7.8 7.8a5 5 0 0 1-7.1-7.1l8.5-8.5a3.3 3.3 0 1 1 4.7 4.7l-8.5 8.5a1.7 1.7 0 0 1-2.4-2.4l7.8-7.8"/>'};return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]||paths.read}</svg>`};
 async function request(path,method='GET',body){
  const response=await fetch(path,{method,credentials:'same-origin',cache:'no-store',headers:body?{'Content-Type':'application/json'}:undefined,body:body?JSON.stringify(body):undefined});
  const value=await response.json().catch(()=>({}));
  if(!response.ok){const error=Error(value.error||`Mail request failed (${response.status})`);error.status=response.status;throw error}
  return value;
 }
 function init(){
  const root=$('#mail-panel'),section=$('#mail'),folders=root.querySelector('.mail-panel-folders'),list=root.querySelector('.mail-panel-list-pane'),detail=root.querySelector('.mail-panel-detail-pane');
  const state={owner:false,available:false,summary:null,summaryError:'',addresses:[],address:'',keepUnknown:false,folder:new URLSearchParams(location.search).get('mail')==='outbox'?'outbox':'all',box:new URLSearchParams(location.search).get('box')||'',q:'',status:'pending_approval',items:[],checked:new Set(),bulkBusy:false,bulkError:'',cursor:'',loading:false,error:'',selected:null,detail:null,detailError:'',detailLoading:false,revealed:false,mobilePage:'list',mobileMenu:'',sequence:0};
  let searchTimer,reviewItem,reviewFocus;
  const review=document.createElement('dialog');review.className='mail-review mail-panel-review';review.id='mail-panel-review';
  review.innerHTML='<form method="dialog" class="mail-review-shell"><header><div><small>OUTBOX APPROVAL</small><h2>Review email</h2></div><button type="button" data-review="close" aria-label="Close review">×</button></header><div class="mail-review-scroll"><p class="mail-review-status" role="status"></p><label>To<input name="to" type="email" multiple required></label><label>Subject<input name="subject" required></label><label>Body<textarea name="text" rows="9" required></textarea></label><div class="mail-reject-reason" hidden><label>Reason for rejection<textarea name="reason" rows="3" placeholder="Tell the agent why this should not be sent"></textarea></label></div></div><footer><button type="button" data-review="reject">Reject</button><button type="button" data-review="approve">Approve and send</button></footer></form>';
  document.body.append(review);
  const addressDialog=document.createElement('dialog');addressDialog.className='mail-address-dialog';addressDialog.id='mail-address-dialog';addressDialog.setAttribute('aria-label','Manage mail addresses');document.body.append(addressDialog);
  review.querySelector('form').addEventListener('submit',event=>event.preventDefault());
  const errorText=error=>error?.message||'Request failed';
  const selectedBox=()=>state.summary?.boxes?.find(box=>boxId(box)===state.box);
  function showNav(){
   const visible=state.owner&&state.available;
   for(const node of document.querySelectorAll('[data-mail-nav]'))node.hidden=!visible;
   section.hidden=!visible;
   const badge=$('#manage-mail-count'),count=Number(state.summary?.unread)||0;
   if(badge){badge.hidden=!visible||!count;badge.textContent=String(count)}
   if(!visible&&location.hash==='#mail'&&state.summary!==null)location.hash='#boxes';
  }
  function renderFolders(){
   const summary=state.summary||{},boxes=Array.isArray(summary.boxes)?summary.boxes:[];
   const disabledExpanded=folders.querySelector('.mail-panel-disabled-boxes')?.open;
   const folderRows=[['all','Inbox',summary.inbox],['unread','Unread',summary.unread],['quarantine','Quarantine',summary.quarantine],['outbox','Outbox',summary.pending],['archive','Archive',summary.archive]];
   const addresses=state.addresses.filter(item=>item.address);
   const enabled=boxes.filter(box=>box.enabled).sort((a,b)=>(Number(b.unread)||0)-(Number(a.unread)||0)||boxName(a).localeCompare(boxName(b)));
   const disabled=boxes.filter(box=>!box.enabled).sort((a,b)=>boxName(a).localeCompare(boxName(b)));
   const boxRow=box=>`<button type="button" data-box="${esc(boxId(box))}" aria-current="${state.box===boxId(box)?'page':'false'}"><span><strong>${esc(boxName(box))}</strong><small>${box.enabled?esc(box.address):'Inbox off · enable in Details'}</small></span>${box.unread?`<b>${esc(box.unread)}</b>`:''}</button>`;
   folders.innerHTML=`<header class="mail-panel-pane-head"><h3>Mail</h3><button type="button" class="mail-panel-sheet-close" data-mobile-close aria-label="Close choices">×</button></header><div class="mail-panel-folder-rows">${folderRows.map(([key,label,count])=>`<button type="button" data-folder="${key}" aria-current="${state.folder===key?'page':'false'}"><span>${esc(label)}</span>${count?`<b>${esc(count)}</b>`:''}</button>`).join('')}</div><div class="mail-panel-scope-rows"><h3 class="mail-panel-subhead">Boxes</h3><div class="mail-panel-box-rows"><button type="button" data-box="" aria-current="${state.box||state.address?'false':'page'}"><span>All boxes</span></button>${enabled.map(boxRow).join('')}</div>${disabled.length?`<details class="mail-panel-disabled-boxes" ${disabledExpanded?'open':''}><summary>${disabled.length} ${disabled.length===1?'box':'boxes'} without inbox</summary><div class="mail-panel-box-rows">${disabled.map(boxRow).join('')}</div></details>`:''}<h3 class="mail-panel-subhead">Addresses</h3><div class="mail-panel-box-rows mail-panel-address-rows"><button type="button" data-address="" aria-current="${state.address?'false':'page'}"><span>All addresses</span></button>${addresses.map(item=>`<button type="button" data-address="${esc(item.address)}" aria-current="${state.address===item.address?'page':'false'}"><span><strong>${esc(item.label||item.address)}</strong><small>${esc(item.address)}</small></span>${item.unread?`<b>${esc(item.unread)}</b>`:''}</button>`).join('')}</div><button type="button" class="mail-panel-manage-addresses" data-action="addresses">Manage addresses</button>${enabled.length?'':'<p class="mail-panel-side-note">No box inboxes yet. Enable mail in a box’s Details.</p>'}</div>`;
  }
  function renderList(){
   const isOutbox=state.folder==='outbox';
   const title=isOutbox?'Outbox':state.folder==='quarantine'?'Quarantine':state.folder==='archive'?'Archive':state.folder==='unread'?'Unread':'Inbox';
   const box=selectedBox();
   const scope=state.address|| (box?boxName(box):'All boxes');
   const heading=`<header class="mail-panel-pane-head mail-panel-list-head"><div class="mail-panel-list-title"><h3>${esc(title)}</h3><small>${esc(scope)}</small></div><div class="mail-panel-mobile-controls"><button type="button" data-mobile-menu="folders" aria-expanded="${state.mobileMenu==='folders'}">${esc(title)} ▾</button><button type="button" data-mobile-menu="scope" aria-expanded="${state.mobileMenu==='scope'}">${esc(scope)} ▾</button><span class="mail-panel-mobile-unread">${Number(state.summary?.unread)||0} unread</span></div></header>`;
   const search=isOutbox?'':`<label class="mail-panel-search"><span>Search mail</span><input type="search" placeholder="Search sender or subject" value="${esc(state.q)}" aria-label="Search mail"></label>`;
   const statusTabs=isOutbox?`<div class="mail-panel-status-tabs" role="tablist" aria-label="Outbox status">${[['pending_approval','Pending'],['sent','Sent'],['rejected','Rejected']].map(([key,label])=>`<button type="button" role="tab" data-status="${key}" aria-selected="${state.status===key}">${label}</button>`).join('')}</div>`:'';
   const bulk=!isOutbox?`<div class="mail-panel-bulk" ${state.checked.size?'':'hidden'}><strong>${state.checked.size} selected</strong><button type="button" data-bulk="read">Mark read</button><button type="button" data-bulk="unread">Mark unread</button><button type="button" data-bulk="archive" ${state.folder==='quarantine'||state.folder==='archive'?'hidden':''}>Archive</button><button type="button" data-bulk="unarchive" ${state.folder==='archive'?'':'hidden'}>Move to Inbox</button><button type="button" data-bulk="release" ${state.folder==='quarantine'?'':'hidden'}>Release</button><button type="button" data-bulk="delete">Delete</button><button type="button" data-bulk="clear" aria-label="Clear selection">×</button></div>${state.bulkError?`<p class="mail-panel-bulk-error" role="alert">${esc(state.bulkError)}</p>`:''}`:'';
   let rows='';
   if(state.error)rows=`<div class="mail-panel-state" role="alert">${esc(state.error)}<button type="button" data-action="retry">Retry</button></div>`;
   else if(state.loading&&!state.items.length)rows='<div class="mail-panel-state">Loading mail…</div>';
   else if(!state.items.length)rows=`<div class="mail-panel-state"><strong>${isOutbox?'No drafts in this view':'No messages here'}</strong><small>${isOutbox?'Agent drafts awaiting approval will appear here.':'Choose another folder or box.'}</small></div>`;
   else rows=state.items.map(item=>{
    const key=isOutbox?item.outboxId:item.id,from=isOutbox?'To '+(Array.isArray(item.to)?item.to.join(', '):item.to||''):item.fromName||item.from;
    const preview=isOutbox?item.text||item.reason||'':otp(item)?mask(item.preview):item.preview;
    const selected=state.selected?.key===key&&state.selected?.kind===(isOutbox?'outbox':'message');
    const meta=`${esc(boxName(item))}${item.address||item.boxAddress?' · '+esc(item.address||item.boxAddress):''}${isOutbox?' · '+esc(item.status.replaceAll('_',' ')):''}`;
    return `<div class="mail-panel-row ${item.unread?'is-unread':''}" data-item="${esc(key)}" aria-current="${selected?'true':'false'}">${isOutbox?'':`<input class="mail-panel-row-check" type="checkbox" data-select="${esc(key)}" aria-label="Select ${esc(item.subject||'(No subject)')}" ${state.checked.has(key)?'checked':''}>`}<span class="mail-panel-row-avatar" aria-hidden="true">${esc((from||'?').trim().charAt(0).toUpperCase())}</span><button type="button" class="mail-panel-row-open" data-item-open="${esc(key)}"><span class="mail-panel-row-sender">${esc(from)}</span><span class="mail-panel-row-subject">${esc(item.subject||'(No subject)')}</span><span class="mail-panel-row-preview">${esc(preview)}</span><span class="mail-panel-row-box">${meta}</span>${item.hasAttachments?`<span class="mail-panel-row-attachment" title="Has attachments" aria-label="Has attachments">${icon('paperclip')}</span>`:''}<time>${esc(stamp(item.receivedAt||item.submittedAt))}</time></button>${isOutbox?'':`<span class="mail-panel-row-actions"><button type="button" data-row-action="${item.unread?'read':'unread'}" data-mail-id="${esc(key)}" aria-label="Mark ${item.unread?'read':'unread'}" title="Mark ${item.unread?'read':'unread'}">${icon('read')}</button>${item.quarantined?`<button type="button" data-row-action="release" data-mail-id="${esc(key)}" aria-label="Release from quarantine" title="Release from quarantine">${icon('release')}</button>`:state.folder==='archive'?`<button type="button" data-row-action="unarchive" data-mail-id="${esc(key)}" aria-label="Move to Inbox" title="Move to Inbox">${icon('unarchive')}</button>`:`<button type="button" data-row-action="archive" data-mail-id="${esc(key)}" aria-label="Archive" title="Archive">${icon('archive')}</button>`}<button type="button" data-row-action="delete" data-mail-id="${esc(key)}" aria-label="Delete" title="Delete">${icon('delete')}</button></span>`}</div>`;
   }).join('');
   list.innerHTML=heading+search+statusTabs+bulk+`<div class="mail-panel-list-scroll">${rows}${state.cursor?'<button type="button" class="mail-panel-more" data-action="more">Load more</button>':''}</div>`;
  }
  function renderDetail(){
   const entry=state.detail;
   const toolbar=entry?state.selected?.kind==='outbox'?`${entry.status==='pending_approval'?`<button type="button" data-action="review" aria-label="Review draft" title="Review draft">${icon('review')}<span>Review draft</span></button>`:''}${boxId(entry)?`<a href="/chat#box=${id(boxId(entry))}" aria-label="Open box chat" title="Open box chat">${icon('chat')}<span>Open box chat</span></a>`:''}`:`<button type="button" data-action="read" aria-label="Mark ${entry.unread?'read':'unread'}" title="Mark ${entry.unread?'read':'unread'}">${icon('read')}<span>Mark ${entry.unread?'read':'unread'}</span></button>${entry.quarantined?`<button type="button" data-action="release" aria-label="Release from quarantine" title="Release from quarantine">${icon('release')}<span>Release from quarantine</span></button>`:state.folder==='archive'?`<button type="button" data-action="unarchive" aria-label="Move to Inbox" title="Move to Inbox">${icon('unarchive')}<span>Move to Inbox</span></button>`:`<button type="button" data-action="archive" aria-label="Archive" title="Archive">${icon('archive')}<span>Archive</span></button>`}<button type="button" data-action="delete" aria-label="Delete" title="Delete">${icon('delete')}<span>Delete</span></button>${boxId(entry)?`<a href="/chat#box=${id(boxId(entry))}" aria-label="Open box chat" title="Open box chat">${icon('chat')}<span>Open box chat</span></a>`:''}`:'';
   const heading=`<header class="mail-panel-pane-head mail-panel-detail-head"><button class="mail-panel-back" type="button" data-back="list" aria-label="Back to list">‹</button><div><h3>Message</h3></div><nav class="mail-panel-detail-tools" aria-label="Message actions">${toolbar}</nav></header>`;
   if(state.detailError){detail.innerHTML=heading+`<div class="mail-panel-state" role="alert">${esc(state.detailError)}<button type="button" data-action="retry-detail">Retry</button></div>`;return}
   if(state.detailLoading){detail.innerHTML=heading+'<div class="mail-panel-state">Loading details…</div>';return}
   if(!entry){detail.innerHTML=heading+'<div class="mail-panel-state">Select a message to read it here.</div>';return}
   if(state.selected.kind==='outbox'){
    const recipients=Array.isArray(entry.to)?entry.to.join(', '):entry.to||'';
    detail.innerHTML=heading+`<div class="mail-panel-detail-scroll"><article class="mail-panel-detail-card"><h2>${esc(entry.subject||'(No subject)')}</h2><div class="mail-panel-detail-meta"><strong>To ${esc(recipients)}</strong><time>${esc(stamp(entry.submittedAt))}</time></div><p class="mail-panel-detail-context">${esc(boxName(entry))} · ${esc(entry.status?.replaceAll('_',' ')||'Draft')}</p></article><article class="mail-panel-detail-card"><div class="mail-panel-body">${esc(entry.text)}</div></article>${entry.reason?`<p class="mail-panel-note">Reason: ${esc(entry.reason)}</p>`:''}</div>`;
    return;
   }
   const secret=otp(entry),body=secret&&!state.revealed?mask(entry.text):entry.text;
   const files=(entry.attachments||[]).map(file=>`<div class="mail-panel-attachment"><span class="mail-panel-attachment-icon">${icon('paperclip')}</span><strong>${esc(file.name)}</strong><small>${esc(Math.ceil((file.size||0)/1024))} KB</small><a href="/v1/mail/messages/${id(entry.id)}/attachments/${id(file.id)}" download="${esc(file.name)}">Download</a></div>`).join('');
   detail.innerHTML=heading+`<div class="mail-panel-detail-scroll"><article class="mail-panel-detail-card mail-panel-message-header"><h2>${esc(entry.subject||'(No subject)')}</h2><div class="mail-panel-detail-meta"><strong>${esc(entry.fromName||entry.from)} &lt;${esc(entry.from)}&gt;</strong><time>${esc(stamp(entry.receivedAt))}</time></div><p class="mail-panel-detail-context">To ${esc(entry.address||entry.boxAddress||entry.to||'—')} · ${esc(boxName(entry))}${entry.quarantined?' · Quarantined':''}</p><div class="mail-panel-auth"><span>SPF ${esc(entry.spf||'unknown')}</span><span>DKIM ${esc(entry.dkim||'unknown')}</span></div></article><div class="mail-panel-untrusted">Untrusted external content · Check links and attachments.</div><article class="mail-panel-detail-card mail-panel-message-body">${secret?`<div class="mail-panel-secret"><span>Verification code ${state.revealed?'shown':'hidden'}</span><button type="button" data-action="reveal">${state.revealed?'Hide':'Reveal'}</button></div>`:''}<div class="mail-panel-body">${esc(body)}</div></article>${files?`<article class="mail-panel-detail-card mail-panel-attachment-strip"><h3>Attachments</h3>${files}</article>`:''}<p class="mail-panel-detail-status" role="status"></p></div>`;
  }
  function render(){if(!state.available)return;showNav();renderFolders();renderList();renderDetail();root.dataset.mobilePage=state.mobilePage;root.dataset.mobileMenu=state.mobileMenu}
  async function loadAddresses(){
   const result=await Promise.allSettled([request('/v1/mail/addresses'),request('/v1/mail/settings')]);
   if(!state.owner||!state.available)return;
   const value=result[0].status==='fulfilled'?result[0].value:null;
   const records=Array.isArray(value)?value:Array.isArray(value?.addresses)?value.addresses:[];
   const addresses=records.filter(item=>item.address).map(item=>({...item,primary:!!item.owningBoxId}));
   const fallback=(state.summary?.boxes||[]).filter(box=>box.address&&!addresses.some(item=>item.address===box.address)).map(box=>({address:box.address,label:boxName(box),boxIds:[boxId(box)],unread:box.unread||0,primary:true}));
   state.addresses=[...addresses.filter(item=>!item.primary),...addresses.filter(item=>item.primary),...fallback];
   state.keepUnknown=!!(result[1].status==='fulfilled'?result[1].value?.keepUnknown:value?.keepUnknown);
   renderFolders();if(addressDialog.open)renderAddressDialog();
  }
  function renderAddressDialog(edit){
   const boxes=state.summary?.boxes||[],extras=state.addresses.filter(item=>!item.primary),owned=state.addresses.filter(item=>item.primary&&item.id),current=edit||null,grantOnly=!!current?.primary;
   const checks=boxes.map(box=>`<label class="mail-address-check"><input type="checkbox" name="boxIds" value="${esc(boxId(box))}" ${current?.boxIds?.includes(boxId(box))?'checked':''} ${grantOnly&&boxId(box)===current.owningBoxId?'disabled':''}><span>${esc(boxName(box))}${grantOnly&&boxId(box)===current.owningBoxId?' · owner':''}</span></label>`).join('');
   addressDialog.innerHTML=`<div class="mail-address-shell"><header><div><small>WORKSPACE MAIL</small><h2>Addresses</h2></div><button type="button" data-address-action="close" aria-label="Close addresses">×</button></header><div class="mail-address-scroll"><p class="mail-address-note">Grant boxes access to shared and box addresses. Mail to unknown addresses is discarded by default.</p><h3 class="mail-address-section">Box addresses</h3><div class="mail-address-list">${owned.map(item=>`<div class="mail-address-row"><div><strong>${esc(item.address)}</strong><small>${esc(boxName(boxes.find(box=>boxId(box)===item.owningBoxId)||{name:item.address}))} · ${(item.boxIds||[]).length} ${(item.boxIds||[]).length===1?'box':'boxes'}</small></div><button type="button" data-address-grants="${esc(item.id)}">Share access</button></div>`).join('')||'<p class="mail-address-empty">No box addresses yet.</p>'}</div><h3 class="mail-address-section">Extra addresses</h3><div class="mail-address-list">${extras.map(item=>`<div class="mail-address-row"><div><strong>${esc(item.label||item.address)}</strong><small>${esc(item.address)} · ${(item.boxIds||[]).length} ${(item.boxIds||[]).length===1?'box':'boxes'}</small></div><button type="button" data-address-edit="${esc(item.id||item.addressId||item.localPart)}">Edit</button><button type="button" data-address-delete="${esc(item.id||item.addressId||item.localPart)}">Delete</button></div>`).join('')||'<p class="mail-address-empty">No extra addresses yet.</p>'}</div><form id="mail-address-form"><h3>${grantOnly?'Also readable by · '+esc(current.address):current?'Edit address':'Add address'}</h3>${grantOnly?'':`<div class="mail-address-fields"><label>Local part<input name="localPart" autocomplete="off" required maxlength="64" placeholder="b" value="${esc(current?.localPart||'')}"></label><label>Label<input name="label" maxlength="80" placeholder="Team inbox" value="${esc(current?.label||'')}"></label></div>`}<fieldset><legend>Boxes that may read this address</legend>${checks||'<p>Create a box first.</p>'}</fieldset><div class="mail-address-form-actions"><button type="button" data-address-action="cancel" ${current?'':'hidden'}>Cancel edit</button><button type="submit" ${boxes.length?'':'disabled'}>${grantOnly?'Save access':current?'Save address':'Create address'}</button></div></form><label class="mail-address-unknown"><span><strong>Keep mail to unknown addresses</strong><small>Off by default; turn on only if you need to review unmatched mail.</small></span><input type="checkbox" id="mail-keep-unknown" ${state.keepUnknown?'checked':''}></label><p class="mail-address-status" role="status"></p></div></div>`;
   addressDialog.dataset.editId=current?String(current.id||current.addressId||current.localPart):'';
  }
  function openAddresses(){renderAddressDialog();addressDialog.showModal();addressDialog.querySelector('[data-address-action="close"]').focus()}
  addressDialog.addEventListener('click',event=>{
   if(event.target===addressDialog){addressDialog.close();return}
   const action=event.target.closest('[data-address-action]')?.dataset.addressAction;
   if(action==='close'){addressDialog.close();return}
   if(action==='cancel'){renderAddressDialog();return}
   const edit=event.target.closest('[data-address-edit]')?.dataset.addressEdit;
   if(edit){renderAddressDialog(state.addresses.find(item=>String(item.id||item.addressId||item.localPart)===edit));addressDialog.querySelector('[name="localPart"]').focus();return}
   const grants=event.target.closest('[data-address-grants]')?.dataset.addressGrants;
   if(grants){renderAddressDialog(state.addresses.find(item=>item.primary&&String(item.id)===grants));addressDialog.querySelector('[name="boxIds"]:not(:disabled)')?.focus();return}
   const deletion=event.target.closest('[data-address-delete]');if(!deletion)return;
   if(!deletion.dataset.confirmed){deletion.dataset.confirmed='1';deletion.textContent='Confirm delete';return}
   deletion.disabled=true;void (async()=>{try{await request('/v1/mail/addresses/'+id(deletion.dataset.addressDelete),'DELETE');await loadAddresses();void summary(false,false)}catch(error){addressDialog.querySelector('.mail-address-status').textContent=errorText(error);deletion.disabled=false}})();
  });
  addressDialog.addEventListener('submit',event=>{
   if(event.target.id!=='mail-address-form')return;event.preventDefault();const form=event.target,fields=form.elements;
   const key=addressDialog.dataset.editId,path='/v1/mail/addresses'+(key?'/'+id(key):'');
   const current=state.addresses.find(item=>String(item.id||item.addressId||item.localPart)===key);
   const boxIds=[...form.querySelectorAll('[name="boxIds"]:checked')].map(input=>input.value);
   const body=current?.primary?{boxIds}:{localPart:fields.localPart.value.trim(),label:fields.label.value.trim(),boxIds};
   form.querySelector('[type="submit"]').disabled=true;
   void (async()=>{try{await request(path,key?'PATCH':'POST',body);await loadAddresses();void summary(false,false)}catch(error){addressDialog.querySelector('.mail-address-status').textContent=errorText(error);form.querySelector('[type="submit"]').disabled=false}})();
  });
  addressDialog.addEventListener('change',event=>{
   if(event.target.id!=='mail-keep-unknown')return;const value=event.target.checked;event.target.disabled=true;
   void (async()=>{try{await request('/v1/mail/settings','PUT',{keepUnknown:value});state.keepUnknown=value;renderAddressDialog()}catch(error){event.target.checked=!value;event.target.disabled=false;addressDialog.querySelector('.mail-address-status').textContent=errorText(error)}})();
  });
  async function summary(reload=true,refreshAddresses=true){
   if(!state.owner)return;
   try{const result=await request('/v1/mail/summary');if(!state.owner)return;state.available=true;state.summary={...result,boxes:Array.isArray(result.boxes)?result.boxes:[]};state.summaryError='';showNav();renderFolders();if(refreshAddresses)void loadAddresses();if(reload&&location.hash==='#mail')void loadList()}
   catch(error){if(!state.owner)return;state.available=error.status!==404;state.summary=error.status===404?{}:null;state.summaryError=errorText(error);showNav();if(state.available){folders.innerHTML=`<div class="mail-panel-state" role="alert">${esc(state.summaryError)}<button type="button" data-action="retry-summary">Retry</button></div>`}}
  }
  async function loadList(append=false,preserve=false){
   if(!state.available||!state.owner)return;
   const seq=++state.sequence,folder=state.folder,box=state.box,address=state.address,q=state.q,status=state.status;
   const searchFocused=document.activeElement?.matches('.mail-panel-search input');
   state.loading=true;state.error='';if(!append&&!preserve){state.items=[];state.checked.clear();state.cursor='';state.selected=null;state.detail=null}render();
   if(searchFocused){const input=list.querySelector('.mail-panel-search input');input?.focus({preventScroll:true});input?.setSelectionRange(input.value.length,input.value.length)}
   const params=new URLSearchParams();if(box)params.set('box',box);if(address&&folder!=='outbox')params.set('address',address);
   if(folder==='outbox'){params.set('status',status)}else{params.set('folder',folder);if(q)params.set('q',q)}
   if(append&&state.cursor)params.set('cursor',state.cursor);
   try{
    const result=await request('/v1/mail/'+(folder==='outbox'?'outbox':'messages')+'?'+params);
    if(seq!==state.sequence||box!==state.box||address!==state.address||folder!==state.folder||q!==state.q||status!==state.status)return;
    const items=Array.isArray(result.items)?result.items:Array.isArray(result.messages)?result.messages:[];
    state.items=append?[...state.items,...items]:items;state.cursor=result.nextCursor||'';
    if(preserve&&state.selected){
     const selected=items.find(item=>(folder==='outbox'?item.outboxId:item.id)===state.selected.key);
     if(selected&&folder==='outbox')state.detail=selected;
     else if(!selected){state.selected=null;state.detail=null;if(state.mobilePage==='detail')state.mobilePage='list'}
     renderDetail();root.dataset.mobilePage=state.mobilePage;
    }else if(!append&&innerWidth>600&&items.length)void openItem(folder==='outbox'?items[0].outboxId:items[0].id,false);
   }catch(error){if(seq===state.sequence)state.error=errorText(error)}
   finally{if(seq===state.sequence){state.loading=false;renderList()}}
  }
  async function openItem(key,mobile=true){
   const kind=state.folder==='outbox'?'outbox':'message',item=state.items.find(value=>(kind==='outbox'?value.outboxId:value.id)===key);if(!item)return;
   state.selected={kind,key};state.detail=kind==='outbox'?item:null;state.detailLoading=kind==='message';state.detailError='';state.revealed=false;if(mobile)state.mobilePage='detail';render();
   if(kind==='outbox')return;
   const seq=state.sequence;
   try{const result=await request('/v1/mail/messages/'+id(key));if(seq===state.sequence&&state.selected?.key===key){state.detail={...item,...result};state.detailLoading=false;renderDetail()}}
   catch(error){if(seq===state.sequence&&state.selected?.key===key){state.detailLoading=false;state.detailError=errorText(error);renderDetail()}}
  }
  function chooseFolder(folder){state.folder=folder;state.mobilePage='list';state.mobileMenu='';root.dataset.mobileMenu='';state.q='';state.status='pending_approval';void loadList()}
  function chooseBox(box){state.box=box;state.address='';state.mobilePage='list';state.mobileMenu='';root.dataset.mobileMenu='';void loadList()}
  function chooseAddress(address){state.address=address;state.box='';if(state.folder==='outbox')state.folder='all';state.mobilePage='list';state.mobileMenu='';root.dataset.mobileMenu='';void loadList()}
  async function changeMessages(keys,action){
   if(state.bulkBusy||!keys.length)return;
   state.bulkBusy=true;state.bulkError='';renderList();
   try{
    for(const key of keys){
     const item=state.items.find(value=>value.id===key);if(!item)continue;
     const path='/v1/mail/messages/'+id(key);
     if(action==='read'||action==='unread'){await request(path+'/read','POST',{read:action==='read'});item.unread=action==='unread';if(state.detail?.id===key)state.detail.unread=item.unread}
     else if(action==='release'){await request(path+'/release','POST',{});item.quarantined=false;item.unread=true}
     else if(action==='archive'||action==='unarchive')await request(path+'/archive','POST',{archived:action==='archive'});
     else if(action==='delete')await request(path,'DELETE');
     if(['release','archive','unarchive','delete'].includes(action)||state.folder==='unread'&&action==='read'){
      state.items=state.items.filter(value=>value.id!==key);
      if(state.selected?.key===key){state.selected=null;state.detail=null;state.mobilePage='list';root.dataset.mobilePage='list'}
     }
     state.checked.delete(key);
    }
    void summary(false,false);
   }catch(error){state.bulkError=errorText(error)}
   finally{state.bulkBusy=false;renderList();renderDetail()}
  }
  function fillReview(item){
   reviewItem=item;const fields=review.querySelector('form').elements;
   fields.to.value=Array.isArray(item.to)?item.to.join(', '):item.to||'';fields.subject.value=item.subject||'';fields.text.value=item.text||'';fields.reason.value='';
   review.querySelector('.mail-reject-reason').hidden=true;review.querySelector('[data-review="reject"]').textContent='Reject';
   review.querySelector('.mail-review-status').textContent=item.status==='pending_approval'?'Nothing is sent until you approve.':'Status: '+item.status;
   for(const button of review.querySelectorAll('[data-review="approve"],[data-review="reject"]'))button.hidden=item.status!=='pending_approval';
   for(const field of review.querySelectorAll('input,textarea'))field.readOnly=item.status!=='pending_approval';
  }
  function closeReview(){if(review.open)review.close();reviewFocus?.focus?.({preventScroll:true});reviewFocus=null}
  async function decide(action){
   if(!reviewItem||reviewItem.status!=='pending_approval')return;
   const fields=review.querySelector('form').elements,status=review.querySelector('.mail-review-status'),reason=fields.reason.value.trim();
   if(action==='reject'&&review.querySelector('.mail-reject-reason').hidden){review.querySelector('.mail-reject-reason').hidden=false;review.querySelector('[data-review="reject"]').textContent='Confirm rejection';fields.reason.focus();return}
   if(action==='reject'&&!reason){status.textContent='Add a reason for the agent.';return}
   if(action==='approve'&&!review.querySelector('form').reportValidity())return;
   const buttons=[...review.querySelectorAll('footer button')];buttons.forEach(button=>button.disabled=true);
   try{
    const path='/v1/logical-boxes/'+id(boxId(reviewItem))+'/mail/outbox/'+id(reviewItem.outboxId);let version=reviewItem.version;
    if(action==='approve'){
     const edited={to:fields.to.value.split(',').map(value=>value.trim()).filter(Boolean),subject:fields.subject.value.trim(),text:fields.text.value.trim(),version};
     const previous=Array.isArray(reviewItem.to)?reviewItem.to.join(', '):reviewItem.to||'';
     if(edited.to.join(', ')!==previous||edited.subject!==reviewItem.subject||edited.text!==reviewItem.text){const updated=await request(path,'PUT',edited);version=updated.version}
     await request(path+'/approve','POST',{version});
    }else await request(path+'/reject','POST',{reason,version});
    closeReview();state.selected=null;state.detail=null;void summary();
   }catch(error){
    if(error.status===409){try{const fresh=await request('/v1/logical-boxes/'+id(boxId(reviewItem))+'/mail/outbox/'+id(reviewItem.outboxId));fillReview(fresh)}catch{}status.textContent='This draft changed; review again'}
    else status.textContent=errorText(error);
   }finally{buttons.forEach(button=>button.disabled=false)}
  }
  review.addEventListener('click',event=>{if(event.target===review){closeReview();return}const action=event.target.closest('[data-review]')?.dataset.review;if(action==='close')closeReview();else if(action==='approve'||action==='reject')void decide(action)});
  review.addEventListener('close',()=>{reviewFocus?.focus?.({preventScroll:true});reviewFocus=null});
  root.addEventListener('click',event=>{
   const target=event.target.closest('button');if(!target)return;
   if(target.dataset.itemOpen){void openItem(target.dataset.itemOpen);return}
   if(target.dataset.rowAction){void changeMessages([target.dataset.mailId],target.dataset.rowAction);return}
   if(target.dataset.bulk){if(target.dataset.bulk==='clear'){state.checked.clear();renderList()}else void changeMessages([...state.checked],target.dataset.bulk);return}
   if(target.dataset.mobileMenu){state.mobileMenu=state.mobileMenu===target.dataset.mobileMenu?'':target.dataset.mobileMenu;root.dataset.mobileMenu=state.mobileMenu;renderList();return}
   if(target.hasAttribute('data-mobile-close')){state.mobileMenu='';root.dataset.mobileMenu='';renderList();return}
   if(target.dataset.folder){chooseFolder(target.dataset.folder);return}
   if(target.hasAttribute('data-box')){chooseBox(target.dataset.box);return}
   if(target.hasAttribute('data-address')){chooseAddress(target.dataset.address);return}
   if(target.dataset.status){state.status=target.dataset.status;void loadList();return}
   if(target.dataset.item){void openItem(target.dataset.item);return}
   if(target.dataset.back){state.mobilePage=target.dataset.back;root.dataset.mobilePage=state.mobilePage;return}
   const action=target.dataset.action;if(!action)return;
   if(action==='retry-summary'){void summary();return}
   if(action==='addresses'){openAddresses();return}
   if(action==='retry'){void loadList();return}
   if(action==='retry-detail'){void openItem(state.selected?.key);return}
   if(action==='more'){void loadList(true);return}
   if(action==='reveal'){state.revealed=!state.revealed;renderDetail();detail.querySelector('[data-action="reveal"]')?.focus();return}
   if(action==='review'&&state.detail){reviewFocus=target;fillReview(state.detail);review.showModal();review.querySelector('[name="to"]').focus();return}
   if(!state.detail||state.selected?.kind!=='message')return;
   if(['read','release','archive','unarchive','delete'].includes(action))void changeMessages([state.detail.id],action==='read'?(state.detail.unread?'read':'unread'):action);
  });
  root.addEventListener('change',event=>{const input=event.target.closest('.mail-panel-row-check');if(!input)return;if(input.checked)state.checked.add(input.dataset.select);else state.checked.delete(input.dataset.select);renderList()});
  root.addEventListener('input',event=>{if(!event.target.matches('.mail-panel-search input'))return;state.q=event.target.value;clearTimeout(searchTimer);searchTimer=setTimeout(()=>void loadList(),250)});
  function setOwner(value){state.owner=!!value;if(!state.owner){state.available=false;state.summary=null;showNav();closeReview();return}void summary()}
  function onRoute(){if(location.hash==='#mail'&&state.owner&&state.available){state.mobilePage='list';state.mobileMenu='';render();void summary(true,false)}}
  async function refreshPending(){
   if(!state.owner||!state.available||document.hidden)return;
   await summary(false,false);
   if(location.hash==='#mail'&&state.folder==='outbox'&&state.summary&&!state.loading&&!review.open)void loadList(false,true);
  }
  navigator.serviceWorker?.addEventListener('message',event=>{if(event.data?.type==='vmbox-push')refreshPending()});
  document.addEventListener('visibilitychange',()=>{if(!document.hidden)refreshPending()});
  window.addEventListener('focus',refreshPending);
  setInterval(refreshPending,15000);
  renderFolders();renderList();renderDetail();
  return {setOwner,onRoute};
 }
 return {init};
})();
