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
 async function request(path,method='GET',body){
  const response=await fetch(path,{method,credentials:'same-origin',cache:'no-store',headers:body?{'Content-Type':'application/json'}:undefined,body:body?JSON.stringify(body):undefined});
  const value=await response.json().catch(()=>({}));
  if(!response.ok){const error=Error(value.error||`Mail request failed (${response.status})`);error.status=response.status;throw error}
  return value;
 }
 function init(){
  const root=$('#mail-panel'),section=$('#mail'),folders=root.querySelector('.mail-panel-folders'),list=root.querySelector('.mail-panel-list-pane'),detail=root.querySelector('.mail-panel-detail-pane');
  const state={owner:false,available:false,summary:null,summaryError:'',addresses:[],address:'',keepUnknown:false,folder:new URLSearchParams(location.search).get('mail')==='outbox'?'outbox':'all',box:'',q:'',status:'pending_approval',items:[],cursor:'',loading:false,error:'',selected:null,detail:null,detailError:'',detailLoading:false,revealed:false,mobilePage:'folders',sequence:0};
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
   const folderRows=[['all','Inbox',summary.inbox],['unread','Unread',summary.unread],['quarantine','Quarantine',summary.quarantine],['outbox','Outbox · pending',summary.pending]];
   const addresses=state.addresses.filter(item=>item.address);
   folders.innerHTML=`<header class="mail-panel-pane-head"><h3>Folders</h3></header><div class="mail-panel-folder-rows">${folderRows.map(([key,label,count])=>`<button type="button" data-folder="${key}" aria-current="${state.folder===key?'page':'false'}"><span>${esc(label)}</span>${count?`<b>${esc(count)}</b>`:''}</button>`).join('')}</div><h3 class="mail-panel-subhead">Boxes</h3><div class="mail-panel-box-rows"><button type="button" data-box="" aria-current="${state.box?'false':'page'}"><span>All boxes</span></button>${boxes.map(box=>`<button type="button" data-box="${esc(boxId(box))}" aria-current="${state.box===boxId(box)?'page':'false'}"><span><strong>${esc(boxName(box))}</strong><small>${esc(box.address||'Inbox off')}</small></span>${box.unread?`<b>${esc(box.unread)}</b>`:''}</button>`).join('')}</div><h3 class="mail-panel-subhead">Addresses</h3><div class="mail-panel-box-rows mail-panel-address-rows"><button type="button" data-address="" aria-current="${state.address?'false':'page'}"><span>All addresses</span></button>${addresses.map(item=>`<button type="button" data-address="${esc(item.address)}" aria-current="${state.address===item.address?'page':'false'}"><span><strong>${esc(item.label||item.address)}</strong><small>${esc(item.address)}</small></span>${item.unread?`<b>${esc(item.unread)}</b>`:''}</button>`).join('')}</div><button type="button" class="mail-panel-manage-addresses" data-action="addresses">Manage addresses</button>${boxes.length?'':'<p class="mail-panel-side-note">No box inboxes yet. Enable mail in a box’s Details.</p>'}`;
  }
  function renderList(){
   const isOutbox=state.folder==='outbox';
   const title=isOutbox?'Outbox':state.folder==='quarantine'?'Quarantine':state.folder==='unread'?'Unread':'Inbox';
   const box=selectedBox();
   const heading=`<header class="mail-panel-pane-head"><button class="mail-panel-back" type="button" data-back="folders" aria-label="Back to folders">‹</button><div><h3>${esc(title)}</h3><small>${esc(state.address|| (box?boxName(box):'All boxes'))}</small></div></header>`;
   const search=isOutbox?'':`<label class="mail-panel-search"><span>Search mail</span><input type="search" placeholder="Search sender or subject" value="${esc(state.q)}" aria-label="Search mail"></label>`;
   const statusTabs=isOutbox?`<div class="mail-panel-status-tabs" role="tablist" aria-label="Outbox status">${[['pending_approval','Pending'],['sent','Sent'],['rejected','Rejected']].map(([key,label])=>`<button type="button" role="tab" data-status="${key}" aria-selected="${state.status===key}">${label}</button>`).join('')}</div>`:'';
   let rows='';
   if(state.error)rows=`<div class="mail-panel-state" role="alert">${esc(state.error)}<button type="button" data-action="retry">Retry</button></div>`;
   else if(state.loading&&!state.items.length)rows='<div class="mail-panel-state">Loading mail…</div>';
   else if(!state.items.length)rows=`<div class="mail-panel-state"><strong>${isOutbox?'No drafts in this view':'No messages here'}</strong><small>${isOutbox?'Agent drafts awaiting approval will appear here.':'Choose another folder or box.'}</small></div>`;
   else rows=state.items.map(item=>{
    const key=isOutbox?item.outboxId:item.id,from=isOutbox?'To '+(Array.isArray(item.to)?item.to.join(', '):item.to||''):item.fromName||item.from;
    const preview=isOutbox?item.text||item.reason||'':otp(item)?mask(item.preview):item.preview;
    const selected=state.selected?.key===key&&state.selected?.kind===(isOutbox?'outbox':'message');
    return `<button type="button" class="mail-panel-row ${item.unread?'is-unread':''}" data-item="${esc(key)}" aria-current="${selected?'true':'false'}"><span class="mail-panel-row-top"><strong>${esc(from)}</strong><time>${esc(stamp(item.receivedAt||item.submittedAt))}</time></span><span class="mail-panel-row-subject">${esc(item.subject||'(No subject)')}${item.hasAttachments?' <span title="Has attachments">⌕</span>':''}</span><span class="mail-panel-row-preview">${esc(preview)}</span><span class="mail-panel-row-box">${esc(boxName(item))}${item.address||item.boxAddress?' · '+esc(item.address||item.boxAddress):''}${isOutbox?' · '+esc(item.status.replaceAll('_',' ')):''}</span></button>`;
   }).join('');
   list.innerHTML=heading+search+statusTabs+`<div class="mail-panel-list-scroll">${rows}${state.cursor?'<button type="button" class="mail-panel-more" data-action="more">Load more</button>':''}</div>`;
  }
  function renderDetail(){
   const entry=state.detail;
   const heading='<header class="mail-panel-pane-head"><button class="mail-panel-back" type="button" data-back="list" aria-label="Back to list">‹</button><div><h3>Message</h3><small>Details</small></div></header>';
   if(state.detailError){detail.innerHTML=heading+`<div class="mail-panel-state" role="alert">${esc(state.detailError)}<button type="button" data-action="retry-detail">Retry</button></div>`;return}
   if(state.detailLoading){detail.innerHTML=heading+'<div class="mail-panel-state">Loading details…</div>';return}
   if(!entry){detail.innerHTML=heading+'<div class="mail-panel-state">Select a message to read it here.</div>';return}
   if(state.selected.kind==='outbox'){
    const recipients=Array.isArray(entry.to)?entry.to.join(', '):entry.to||'';
    detail.innerHTML=heading+`<div class="mail-panel-detail-scroll"><article class="mail-panel-detail-card"><div class="mail-panel-detail-meta"><span class="mail-panel-pill">${esc(entry.status?.replaceAll('_',' ')||'Draft')}</span><time>${esc(stamp(entry.submittedAt))}</time></div><h2>${esc(entry.subject||'(No subject)')}</h2><p class="mail-panel-field"><span>To</span><strong>${esc(recipients)}</strong></p><p class="mail-panel-field"><span>Box</span><strong>${esc(boxName(entry))}</strong></p></article><article class="mail-panel-detail-card"><h3>Message</h3><div class="mail-panel-body">${esc(entry.text)}</div></article>${entry.reason?`<p class="mail-panel-note">Reason: ${esc(entry.reason)}</p>`:''}<div class="mail-panel-actions">${entry.status==='pending_approval'?'<button type="button" data-action="review">Review draft</button>':''}${boxId(entry)?`<a href="/chat#box=${id(boxId(entry))}">Open box chat</a>`:''}</div></div>`;
    return;
   }
   const secret=otp(entry),body=secret&&!state.revealed?mask(entry.text):entry.text;
   const files=(entry.attachments||[]).map(file=>`<div class="mail-panel-attachment"><span>▣</span><strong>${esc(file.name)}</strong><small>${esc(Math.ceil((file.size||0)/1024))} KB</small><a href="/v1/mail/messages/${id(entry.id)}/attachments/${id(file.id)}" download="${esc(file.name)}">Download</a></div>`).join('');
   detail.innerHTML=heading+`<div class="mail-panel-detail-scroll"><div class="mail-panel-untrusted">Untrusted external content · Check links and attachments.</div><article class="mail-panel-detail-card"><div class="mail-panel-detail-meta"><span class="mail-panel-pill ${entry.quarantined?'danger':''}">${entry.quarantined?'Quarantined':'Received '+esc(stamp(entry.receivedAt))}</span></div><h2>${esc(entry.subject||'(No subject)')}</h2><p class="mail-panel-field"><span>From</span><strong>${esc(entry.fromName||entry.from)} &lt;${esc(entry.from)}&gt;</strong></p><p class="mail-panel-field"><span>To</span><strong>${esc(entry.address||entry.boxAddress||entry.to||'—')}</strong></p><p class="mail-panel-field"><span>Box</span><strong>${esc(boxName(entry))}</strong></p><div class="mail-panel-auth"><span>SPF ${esc(entry.spf||'unknown')}</span><span>DKIM ${esc(entry.dkim||'unknown')}</span></div></article><article class="mail-panel-detail-card"><h3>Message</h3>${secret?`<div class="mail-panel-secret"><span>Verification code ${state.revealed?'shown':'hidden'}</span><button type="button" data-action="reveal">${state.revealed?'Hide':'Reveal'}</button></div>`:''}<div class="mail-panel-body">${esc(body)}</div></article>${files?`<article class="mail-panel-detail-card"><h3>Attachments</h3>${files}</article>`:''}<div class="mail-panel-actions"><button type="button" data-action="read">Mark ${entry.unread?'read':'unread'}</button>${entry.quarantined?'<button type="button" data-action="release">Release from quarantine</button>':''}${boxId(entry)?`<a href="/chat#box=${id(boxId(entry))}">Open box chat</a>`:''}</div><p class="mail-panel-detail-status" role="status"></p></div>`;
  }
  function render(){if(!state.available)return;showNav();renderFolders();renderList();renderDetail();root.dataset.mobilePage=state.mobilePage}
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
  async function loadList(append=false){
   if(!state.available||!state.owner)return;
   const seq=++state.sequence,folder=state.folder,box=state.box,address=state.address,q=state.q,status=state.status;
   const searchFocused=document.activeElement?.matches('.mail-panel-search input');
   state.loading=true;state.error='';if(!append){state.items=[];state.cursor='';state.selected=null;state.detail=null}render();
   if(searchFocused){const input=list.querySelector('.mail-panel-search input');input?.focus({preventScroll:true});input?.setSelectionRange(input.value.length,input.value.length)}
   const params=new URLSearchParams();if(box)params.set('box',box);if(address&&folder!=='outbox')params.set('address',address);
   if(folder==='outbox'){params.set('status',status)}else{params.set('folder',folder);if(q)params.set('q',q)}
   if(append&&state.cursor)params.set('cursor',state.cursor);
   try{
    const result=await request('/v1/mail/'+(folder==='outbox'?'outbox':'messages')+'?'+params);
    if(seq!==state.sequence||box!==state.box||address!==state.address||folder!==state.folder||q!==state.q||status!==state.status)return;
    const items=Array.isArray(result.items)?result.items:Array.isArray(result.messages)?result.messages:[];
    state.items=append?[...state.items,...items]:items;state.cursor=result.nextCursor||'';
    if(!append&&innerWidth>600&&items.length)void openItem(folder==='outbox'?items[0].outboxId:items[0].id,false);
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
  function chooseFolder(folder){state.folder=folder;state.mobilePage='list';state.q='';state.status='pending_approval';void loadList()}
  function chooseBox(box){state.box=box;state.mobilePage='list';void loadList()}
  function chooseAddress(address){state.address=address;if(state.folder==='outbox')state.folder='all';state.mobilePage='list';void loadList()}
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
   if(action==='read'||action==='release')void (async()=>{try{const path='/v1/mail/messages/'+id(state.detail.id);if(action==='read'){
    const wasUnread=!!state.detail.unread;await request(path+'/read','POST',{read:wasUnread});state.detail.unread=!wasUnread;
    const item=state.items.find(item=>item.id===state.detail.id);if(item)item.unread=state.detail.unread;
    if(!state.detail.quarantined){const delta=wasUnread?-1:1;state.summary.unread=Math.max(0,(state.summary.unread||0)+delta);const box=state.summary.boxes.find(box=>boxId(box)===boxId(state.detail));if(box)box.unread=Math.max(0,(box.unread||0)+delta);const address=state.addresses.find(address=>address.address===(state.detail.address||state.detail.boxAddress));if(address)address.unread=Math.max(0,(address.unread||0)+delta)}
    renderFolders();renderList();renderDetail();
   }else{await request(path+'/release','POST',{});state.detail.quarantined=false;state.mobilePage='list';root.dataset.mobilePage='list';void summary(true,false)} }catch(error){detail.querySelector('.mail-panel-detail-status').textContent=errorText(error)}})();
  });
  root.addEventListener('input',event=>{if(!event.target.matches('.mail-panel-search input'))return;state.q=event.target.value;clearTimeout(searchTimer);searchTimer=setTimeout(()=>void loadList(),250)});
  function setOwner(value){state.owner=!!value;if(!state.owner){state.available=false;state.summary=null;showNav();closeReview();return}void summary()}
  function onRoute(){if(location.hash==='#mail'&&state.owner&&state.available){render();void loadList()}}
  renderFolders();renderList();renderDetail();
  return {setOwner,onRoute};
 }
 return {init};
})();
