'use strict';
(()=>{
 const $=id=>document.getElementById(id);
 const esc=value=>String(value??'').replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
 const readTools=['list_mail_addresses','list_emails','read_email','mark_email_read','download_email_attachment','subscribe_inbox','unsubscribe_inbox'];
 const composeTools=['send_email','list_outbox','get_outbox_status'];
 const state={boxId:'',settings:null,settingsSaving:false,receiveUndo:false,receiveError:'',globalAvailable:null,navUnread:0,approvals:{pending:0,items:[]},tab:'inbox',folder:'all',outboxStatus:'pending_approval',messages:[],messagesCursor:'',messagesLoaded:false,messagesLoading:false,messagesError:'',outbox:[],outboxCursor:'',outboxLoaded:false,outboxLoading:false,outboxError:'',detail:null,detailLoading:false,detailError:'',otpRevealed:false,policy:null,policyLoading:false,policyError:'',addresses:[],addressesLoaded:false,addressesLoading:false,addressSaving:false,addressQuery:'',addressOpen:false,addressActive:0,labelAddressId:'',undoAddressId:'',addressError:'',addressStatus:''};
 let api,mailPage,detailPage,mainGroup,mainRow,review,reviewItem,returnFocus,requestEpoch=0,approvalTimer,undoTimer,receiveUndoTimer,detailId='',outboxRefreshQueued=false;
 const base=()=>'/v1/logical-boxes/'+encodeURIComponent(state.boxId)+'/mail';
 const count=()=>Number(state.settings?.unread)||0;
 const pending=()=>Number(state.settings?.pending)||0;
 const when=value=>{
  if(!value)return '—';
  const date=new Date(value);if(Number.isNaN(+date))return String(value);
  const now=new Date(),minutes=Math.floor((now-date)/60000);
  if(minutes>=0&&minutes<1)return 'Just now';
  if(minutes>=0&&minutes<60)return minutes+' min ago';
  const today=new Date(now.getFullYear(),now.getMonth(),now.getDate());
  const yesterday=new Date(today);yesterday.setDate(today.getDate()-1);
  if(date>=yesterday&&date<today)return 'Yesterday';
  if(minutes>=0&&date>=today&&minutes<1440)return Math.floor(minutes/60)+' hr ago';
  return new Intl.DateTimeFormat(undefined,{month:'short',day:'numeric',...(date.getFullYear()===now.getFullYear()?{}:{year:'numeric'})}).format(date);
 };
 const card=(title,body,extra='')=>`<section class="ip-card mail-card ${extra}"><h3>${esc(title)}</h3>${body}</section>`;
 const section=(title,body,extra='')=>`<section class="mail-section"><h3 class="ip-heading">${esc(title)}</h3><div class="ip-card mail-card ${extra}">${body}</div></section>`;
 const pill=(label,kind='')=>`<span class="mail-pill ${kind}">${esc(label)}</span>`;
 const otpMail=mail=>/\b(verification|one.time|security|otp|passcode|sign.in code)\b/i.test([mail?.subject,mail?.text].join(' '))&&/\b\d{4,8}\b/.test(mail?.text||mail?.preview||'');
 const maskCode=text=>String(text||'').replace(/\b\d{4,8}\b/g,'••••••');
 const shortError=error=>error?.message||'Request failed';
 function signals(){
  const navVisible=api.isOwner()&&state.globalAvailable===true;
  for(const node of document.querySelectorAll('#chat-menu-sheet [data-mail-nav]'))node.hidden=!navVisible;
  const navCount=$('chat-mail-count');if(navCount){navCount.hidden=!navVisible||!state.navUnread;navCount.textContent=String(state.navUnread)}
  if(mainGroup)mainGroup.hidden=!api.isOwner()||state.globalAvailable!==true;
  if(mainRow){mainRow.hidden=!api.isOwner()||state.globalAvailable!==true||!state.settings;const value=mainRow.querySelector('.ip-row-value');if(value)value.textContent=state.settings?(state.settings.enabled?count()+' unread · '+pending()+' pending':'Off · '+pending()+' pending'):''}
  const n=Number(state.approvals.pending)||0;
  for(const id of ['mail-approval','mail-approval-mobile']){const button=$(id);if(!button)continue;button.hidden=!api.isOwner()||state.globalAvailable!==true||n===0;button.querySelector('b').textContent=String(n);button.setAttribute('aria-label',n+' mail '+(n===1?'approval':'approvals'))}
  for(const node of document.querySelectorAll('#role-editor-form [data-mail-feature]'))node.hidden=!api.isOwner()||state.globalAvailable!==true;
 }
 async function loadNavSummary(){
  try{const summary=await api.request('/v1/mail/summary');if(!api.isOwner()||state.globalAvailable!==true)return;state.navUnread=Number(summary?.unread)||0}
  catch{state.navUnread=0}
  signals();
 }
 async function loadApprovals(){
  if(!api.isOwner()){state.globalAvailable=false;signals();return}
  try{
   const result=await api.request('/v1/mail/approvals');
   state.globalAvailable=true;state.approvals={pending:Number(result?.pending)||0,items:Array.isArray(result?.items)?result.items:[]};
   void loadNavSummary();
   if(state.boxId&&!state.settings)void loadSettings(state.boxId);
  }catch(error){if(error.status===404){state.globalAvailable=false;state.approvals={pending:0,items:[]}}else{state.approvals={pending:0,items:[]};if(state.globalAvailable===null)state.globalAvailable=false}}
  signals();
 }
 async function loadSettings(boxId){
  if(!boxId||!api.isOwner()||state.globalAvailable===false)return;
  const epoch=requestEpoch;
  try{
   const settings=await api.request('/v1/logical-boxes/'+encodeURIComponent(boxId)+'/mail');
   if(epoch!==requestEpoch||boxId!==state.boxId)return;
   state.settings=settings;state.globalAvailable=true;
   void loadPolicy(boxId);
   void loadAddresses(boxId);
   if(state.tab==='inbox'&&!state.messagesLoaded)void loadMessages();
   renderMail();signals();
  }catch(error){
   if(epoch!==requestEpoch||boxId!==state.boxId)return;
   if(error.status===404){state.settings=null;state.globalAvailable=false}else{state.settings={enabled:false,address:'',subscribed:false,filters:{sender:'',subject:''},unread:0,pending:0};state.messagesError=shortError(error)}
   renderMail();signals();
  }
 }
 async function loadPolicy(boxId){
  if(!boxId||state.policyLoading)return;
  const epoch=requestEpoch;
  state.policyLoading=true;state.policyError='';renderMail();
  try{
   const result=await api.request('/v1/logical-boxes/'+encodeURIComponent(boxId)+'/agent-policy');
   if(epoch===requestEpoch&&boxId===state.boxId)state.policy=result?.capabilities||{};
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId)state.policyError=shortError(error)}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.policyLoading=false;renderMail()}}
 }
 async function loadAddresses(boxId){
  if(!boxId||state.addressesLoading)return;
  const epoch=requestEpoch;
  state.addressesLoading=true;state.addressError='';renderMail();
  try{
   const result=await api.request('/v1/mail/addresses');
   if(epoch!==requestEpoch||boxId!==state.boxId)return;
   state.addresses=Array.isArray(result)?result:Array.isArray(result?.addresses)?result.addresses:[];
   state.addressesLoaded=true;
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId)state.addressError=shortError(error)}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.addressesLoading=false;renderMail()}}
 }
 async function saveAddressGrant(addressID,granted){
  const boxId=state.boxId,epoch=requestEpoch;
  if(!boxId||state.addressSaving)return;
  state.addressSaving=true;state.addressError='';state.addressStatus='Saving access…';renderMail();
  try{
   const latest=await api.request('/v1/mail/addresses');
   const address=(Array.isArray(latest)?latest:latest?.addresses||[]).find(item=>item.id===addressID);
   if(!address)throw Error('Address is no longer available');
   if(address.owningBoxId===boxId&&!granted)throw Error('The box always has access to its own address');
   const boxIds=new Set(address.boxIds||[]);
   granted?boxIds.add(boxId):boxIds.delete(boxId);
   await api.request('/v1/mail/addresses/'+encodeURIComponent(addressID),'PATCH',{boxIds:[...boxIds]});
   if(epoch===requestEpoch&&boxId===state.boxId){state.addressStatus=granted?'Address added':'Address removed';state.addressQuery='';state.addressOpen=false;state.undoAddressId=granted?'':addressID;await loadAddresses(boxId);if(!granted){clearTimeout(undoTimer);undoTimer=setTimeout(()=>{state.undoAddressId='';renderMail()},6000)}}
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId){state.addressError=shortError(error);state.addressStatus=''}}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.addressSaving=false;renderMail()}}
 }
 const mailLocalPart=value=>/^(?:[a-z0-9]|[a-z0-9][a-z0-9._+-]{0,62}[a-z0-9])$/.test(value)&&!value.includes('..');
 function addressChoices(){
  const domain=state.settings?.address?.split('@')[1]||'example.test',query=state.addressQuery.trim().toLowerCase();
  const available=state.addresses.filter(item=>item.owningBoxId!==state.boxId&&!(item.boxIds||[]).includes(state.boxId));
  const matches=available.filter(item=>!query||[item.address,item.label,item.localPart].some(value=>String(value||'').toLowerCase().includes(query))).slice(0,5).map(item=>({kind:'grant',item,label:item.address,detail:item.owningBoxId?'Another box’s address':item.label||'Shared address'}));
  const local=query.endsWith('@'+domain)?query.slice(0,-domain.length-1):query;
  const exact=state.addresses.some(item=>item.address.toLowerCase()===local+'@'+domain);
  if(query&&!exact&&mailLocalPart(local))matches.push({kind:'create',local,label:'Create and add '+local+'@'+domain,detail:'New shared address'});
  return matches;
 }
 function renderAddressOptions(){
  const menu=$('mail-address-options'),input=$('mail-address-combobox');if(!menu||!input)return;
  const choices=addressChoices();state.addressActive=Math.max(0,Math.min(state.addressActive,choices.length-1));
  menu.hidden=!state.addressOpen||!choices.length;
  menu.innerHTML=choices.map((choice,index)=>`<button type="button" role="option" id="mail-address-option-${index}" aria-selected="${index===state.addressActive}" data-mail-action="choose-address" data-choice-index="${index}"><strong>${esc(choice.label)}</strong><small>${esc(choice.detail)}</small></button>`).join('');
  input.setAttribute('aria-expanded',String(!menu.hidden));input.setAttribute('aria-activedescendant',menu.hidden?'':'mail-address-option-'+state.addressActive);
 }
 async function createAddress(localPart){
  const boxId=state.boxId,epoch=requestEpoch;if(!boxId||state.addressSaving)return;
  state.addressSaving=true;state.addressError='';state.addressStatus='Creating address…';renderMail();
  try{
   const item=await api.request('/v1/mail/addresses','POST',{localPart,label:'',boxIds:[boxId]});
   if(epoch===requestEpoch&&boxId===state.boxId){state.addressQuery='';state.addressOpen=false;state.labelAddressId=item.id;state.addressStatus='Address added';await loadAddresses(boxId)}
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId){state.addressError=shortError(error);state.addressStatus=''}}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.addressSaving=false;renderMail();if(state.addressError)mailPage.querySelector('#mail-address-combobox')?.focus()}}
 }
 async function saveAddressLabel(form){
  const boxId=state.boxId,epoch=requestEpoch,id=state.labelAddressId;if(!id||state.addressSaving)return;
  state.addressSaving=true;state.addressError='';
  try{await api.request('/v1/mail/addresses/'+encodeURIComponent(id),'PATCH',{label:form.elements.label.value.trim()});if(epoch===requestEpoch&&boxId===state.boxId){state.labelAddressId='';state.addressStatus='Label saved';await loadAddresses(boxId)}}
  catch(error){if(epoch===requestEpoch&&boxId===state.boxId)state.addressError=shortError(error)}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.addressSaving=false;renderMail()}}
 }
 async function saveSettings(patch){
  if(!state.boxId||!state.settings||state.settingsSaving)return;
  const previous=state.settings,boxId=state.boxId,epoch=requestEpoch,path=base();
  const body={enabled:previous.enabled,subscribed:previous.subscribed,...patch,filters:{sender:previous.filters?.sender||'',subject:previous.filters?.subject||'',...patch.filters}};
  if('enabled' in patch)state.receiveError='';
  state.settingsSaving=true;state.settings={...previous,...body};renderMail();signals();
  try{
   const result=await api.request(path,'PUT',body);
   if(epoch!==requestEpoch||boxId!==state.boxId)return false;
   state.settings=result?.enabled!==undefined?result:await api.request(path);
   if(epoch!==requestEpoch||boxId!==state.boxId)return false;
   state.messagesLoaded=false;
   if('enabled' in patch)void loadMessages();
   renderMail();signals();
   return true;
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId){state.settings=previous;if('enabled' in patch)state.receiveError=shortError(error);renderMail();signals()}return false}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.settingsSaving=false;renderMail()}}
 }
 async function setReceiving(enabled){
  if(!enabled){
   if(await saveSettings({enabled:false})&&state.settings?.enabled===false){
    state.receiveUndo=true;clearTimeout(receiveUndoTimer);
    receiveUndoTimer=setTimeout(()=>{state.receiveUndo=false;renderMail()},6000);renderMail();
   }
   return;
  }
  clearTimeout(receiveUndoTimer);
  state.receiveUndo=false;renderMail();
  void saveSettings({enabled:true});
 }
 function setStatus(id,text){const target=$(id);if(target)target.textContent=text}
 async function loadMessages(append=false){
  if(!state.boxId||!state.settings||state.messagesLoading)return;
  const boxId=state.boxId,folder=state.folder,epoch=requestEpoch,cursor=append?state.messagesCursor:'';
  state.messagesLoading=true;state.messagesError='';renderMail();
  try{
   const query=new URLSearchParams({folder});if(cursor)query.set('cursor',cursor);
   const result=await api.request(base()+'/messages?'+query);
   if(epoch!==requestEpoch||boxId!==state.boxId||folder!==state.folder)return;
   const items=(Array.isArray(result?.messages)?result.messages:[]).filter(item=>folder==='quarantine'?item.quarantined:!item.quarantined);
   state.messages=append?[...state.messages,...items]:items;
   state.messagesCursor=result?.nextCursor||'';state.messagesLoaded=true;
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId)state.messagesError=shortError(error)}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.messagesLoading=false;renderMail()}}
 }
 async function loadOutbox(append=false){
  if(!state.boxId)return;
  if(state.outboxLoading){outboxRefreshQueued=true;return}
  const boxId=state.boxId,status=state.outboxStatus,epoch=requestEpoch,cursor=append?state.outboxCursor:'';
  state.outboxLoading=true;state.outboxError='';renderMail();
  try{
   const query=new URLSearchParams({status});if(cursor)query.set('cursor',cursor);
   const result=await api.request(base()+'/outbox?'+query);
   if(epoch!==requestEpoch||boxId!==state.boxId||status!==state.outboxStatus)return;
   state.outbox=append?[...state.outbox,...(result?.items||[])]:Array.isArray(result?.items)?result.items:[];
   state.outboxCursor=result?.nextCursor||'';state.outboxLoaded=true;
  }catch(error){if(epoch===requestEpoch&&boxId===state.boxId)state.outboxError=shortError(error)}
  finally{if(epoch===requestEpoch&&boxId===state.boxId){state.outboxLoading=false;renderMail();if(outboxRefreshQueued){outboxRefreshQueued=false;void loadOutbox()}}}
 }
 function renderAgentAccess(){
  const settings=state.settings||{},read=!!state.policy?.mail?.read;
  const status=state.policyLoading?'Checking access…':state.policyError?'Could not check access':read?'Agent can read mail ✓':'Agent cannot read mail';
  const body=`<div class="mail-setting-row"><span><strong>Notify agent</strong><small>Let the agent know when mail arrives</small></span><label class="mail-switch mail-switch-small"><input id="mail-subscribed" type="checkbox" role="switch" aria-label="Notify agent about new mail" ${settings.subscribed?'checked':''} ${settings.enabled&&!state.settingsSaving?'':'disabled'}><span aria-hidden="true"></span></label></div><details class="mail-filter-details"><summary>Filters</summary><div class="mail-filter-fields"><label>Sender <input id="mail-sender-filter" placeholder="Any sender" value="${esc(settings.filters?.sender||'')}" ${settings.enabled&&settings.subscribed&&!state.settingsSaving?'':'disabled'}></label><label>Subject <input id="mail-subject-filter" placeholder="Any subject" value="${esc(settings.filters?.subject||'')}" ${settings.enabled&&settings.subscribed&&!state.settingsSaving?'':'disabled'}></label></div></details><button type="button" class="mail-access-link" data-mail-action="open-access"><span>${esc(status)}</span><small>Change in Access & permissions ›</small></button>${state.policyError?'<button type="button" class="mail-access-retry" data-mail-action="retry-policy">Retry</button>':''}`;
  return section('Agent',body,'mail-agent-card');
 }
 function renderHeader(){
  const settings=state.settings||{},box=encodeURIComponent(state.boxId);
  const status=settings.enabled?'Receiving':'Paused · new mail is rejected';
  const undo=state.receiveUndo?'<div class="mail-receive-undo" role="status">Receiving paused <button type="button" data-mail-action="undo-receiving">Undo</button></div>':'';
  return `<section class="ip-card mail-summary-card"><div class="mail-summary-top"><code>${esc(settings.address||'Address assigned when receiving is on')}</code><button type="button" data-mail-action="copy" ${settings.address?'':'disabled'}>Copy</button></div><div class="mail-summary-bottom"><span class="mail-summary-counts">${count()} unread · ${pending()} pending</span><div class="mail-receive-control"><span class="${settings.enabled?'is-on':''}">${status}</span><label class="mail-switch mail-switch-small"><input id="mail-enabled" type="checkbox" role="switch" aria-label="Receive mail" ${settings.enabled?'checked':''} ${state.settingsSaving?'disabled':''}><span aria-hidden="true"></span></label></div><a href="/?box=${box}#mail">Open in Mail ›</a></div>${undo}<p id="mail-settings-status" class="mail-inline-status" role="status">${esc(state.receiveError)}</p></section>`;
 }
 function renderAddresses(){
  const own=state.addresses.find(item=>item.owningBoxId===state.boxId),ownAddress=own?.address||state.settings?.address||'Assigned when enabled';
  const granted=state.addresses.filter(item=>item.owningBoxId!==state.boxId&&(item.boxIds||[]).includes(state.boxId));
  const domain=state.settings?.address?.split('@')[1]||'example.test';
  const suggested=state.addresses.filter(item=>item.owningBoxId!==state.boxId&&!(item.boxIds||[]).includes(state.boxId)).slice(0,4).map(item=>({address:item.address,id:item.id}));
  for(const local of ['team','support','contact','billing']){
   if(suggested.length>=4)break;
   const address=local+'@'+domain;
   if(!state.addresses.some(item=>item.address.toLowerCase()===address))suggested.push({address,local});
  }
  const ownRow=`<div class="mail-address-granted mail-address-own"><span><strong>${esc(ownAddress)}</strong><small>This box’s address · always available</small></span><span class="mail-address-fixed">Own</span></div>`;
  const rows=granted.map(item=>`<div class="mail-address-granted"><span><strong>${esc(item.address)}</strong><small>${esc(item.label|| (item.owningBoxId?'Shared from another box':'Shared address'))}</small></span><button type="button" data-mail-action="remove-address" data-address-id="${esc(item.id)}" aria-label="Remove ${esc(item.address)}" title="Remove access" ${state.addressSaving?'disabled':''}>×</button></div>${state.labelAddressId===item.id?`<form class="mail-address-label-form"><label>Label (optional)<input name="label" maxlength="100" placeholder="Team inbox" value="${esc(item.label||'')}"></label><button type="submit">Save label</button><button type="button" data-mail-action="skip-label">Skip</button></form>`:''}`).join('');
  const input=`<div class="mail-address-combobox-wrap"><label for="mail-address-combobox">Add an address</label><input id="mail-address-combobox" role="combobox" aria-autocomplete="list" aria-controls="mail-address-options" aria-expanded="false" autocomplete="off" placeholder="Search or create an address" value="${esc(state.addressQuery)}" ${state.addressSaving?'disabled':''}><div id="mail-address-options" role="listbox" hidden></div></div>`;
  const chips=suggested.length?`<div class="mail-address-suggestions"><small>Suggested</small><div>${suggested.map(item=>`<button type="button" data-mail-action="suggest-address" ${item.id?`data-address-id="${esc(item.id)}"`:`data-local-part="${esc(item.local)}"`} ${state.addressSaving?'disabled':''}>+ ${esc(item.address)}</button>`).join('')}</div></div>`:'';
  const loading=state.addressesLoading&&!state.addressesLoaded?'<p class="mail-address-state">Loading addresses…</p>':state.addressError&&!state.addressesLoaded?'<p class="mail-address-state">Could not load addresses. <button type="button" data-mail-action="retry-addresses">Retry</button></p>':'';
  const toast=state.undoAddressId?`<div class="mail-address-undo" role="status">Address removed <button type="button" data-mail-action="undo-address">Undo</button></div>`:'';
  return section('Addresses',`<div class="mail-address-combined">${input}${loading}${ownRow}${rows}${chips}<a class="mail-all-addresses" href="/#mail">Manage all addresses ›</a><p id="mail-address-status" class="mail-inline-status" role="status">${esc(state.addressError||state.addressStatus)}</p>${toast}</div>`,'mail-address-card');
 }
 function renderInbox(){
  let list='';
  if(state.messagesError)list=`<div class="mail-error"><strong>Mail could not be loaded</strong><p>${esc(state.messagesError)}</p><button type="button" data-mail-action="retry-messages">Retry</button></div>`;
  else if(state.messagesLoading&&!state.messagesLoaded)list='<div class="mail-empty"><strong>Loading mail…</strong></div>';
  else{
   const rows=state.messages.map(mail=>`<button type="button" class="mail-list-row ${mail.unread?'is-unread':''}" data-mail-id="${esc(mail.id)}"><span class="mail-unread-dot" aria-hidden="true"></span><span class="mail-list-content"><span class="mail-list-top"><strong>${esc(mail.fromName||mail.from)}</strong><time>${esc(when(mail.receivedAt))}</time></span><span class="mail-list-subject">${esc(mail.subject)}${mail.hasAttachments?' <span title="Has attachment" aria-label="Has attachment">⌕</span>':''}</span><span class="mail-list-preview">${esc(otpMail(mail)?maskCode(mail.preview):mail.preview)}</span></span></button>`).join('');
   list=rows||'<div class="mail-empty compact"><strong>No mail here</strong></div>';
   if(state.messagesCursor)list+='<button type="button" class="mail-load-more" data-mail-action="more-messages">Load more</button>';
  }
  const tabs=`<div class="mail-tabs" role="tablist" aria-label="Inbox filter">${[['all','All'],['unread','Unread'],['quarantine','Quarantine']].map(([key,label])=>`<button type="button" role="tab" aria-selected="${state.folder===key}" data-mail-filter="${key}">${label}${key==='unread'?` <span>${count()}</span>`:''}</button>`).join('')}</div>`;
  return `<div class="mail-section-heading"><strong>Messages</strong><span>${count()} unread</span></div><section class="ip-card mail-list-card">${tabs}<div class="mail-list">${list}</div></section>`;
 }
 function renderOutbox(){
  let list='';
  if(state.outboxError)list=`<div class="mail-error"><strong>Outbox could not be loaded</strong><p>${esc(state.outboxError)}</p><button type="button" data-mail-action="retry-outbox">Retry</button></div>`;
  else if(state.outboxLoading&&!state.outboxLoaded)list='<div class="mail-empty"><strong>Loading drafts…</strong></div>';
  else{
   list=state.outbox.map(item=>`<button class="mail-outbox-row" type="button" data-outbox-id="${esc(item.outboxId)}"><span class="mail-outbox-icon" aria-hidden="true">${item.status==='sent'?'✓':item.status==='rejected'?'×':item.status==='failed'?'!':'↗'}</span><span><strong>${esc(item.subject)}</strong><small>To ${esc((item.to||[]).join(', '))}</small><em>${esc(item.status==='rejected'?item.reason:item.text||'')}</em></span><time>${esc(when(item.submittedAt))}</time></button>`).join('')||'<div class="mail-empty compact"><strong>No mail in this view</strong></div>';
   if(state.outboxCursor)list+='<button type="button" class="mail-load-more" data-mail-action="more-outbox">Load more</button>';
  }
  const tabs=`<div class="mail-tabs" role="tablist" aria-label="Outbox status">${[['pending_approval','Pending'],['sent','Sent'],['rejected','Rejected'],['failed','Failed']].map(([key,label])=>`<button type="button" role="tab" aria-selected="${state.outboxStatus===key}" data-outbox-tab="${key}">${label}${key==='pending_approval'?` <span>${pending()}</span>`:''}</button>`).join('')}</div>`;
  return `<p class="mail-short-note">Nothing is sent until you approve.</p><section class="ip-card mail-list-card">${tabs}<div class="mail-list">${list}</div></section>`;
 }
 function renderMail(){
  if(!mailPage)return;
  mailPage.setAttribute('aria-busy',String(state.messagesLoading||state.outboxLoading||state.settingsSaving));
  if(!state.settings){mailPage.innerHTML='<div class="mail-empty"><strong>Loading Mail…</strong></div>';return}
  if(!mailPage.querySelector('.mail-messages-section'))mailPage.innerHTML='<div class="mail-summary-slot"></div><div class="mail-messages-section"><div class="mail-notice-slot"></div><div class="mail-main-tabs" role="tablist" aria-label="Mail folders"><button type="button" role="tab" data-mail-tab="inbox">Inbox <span></span></button><button type="button" role="tab" data-mail-tab="outbox">Outbox <span></span></button></div><div class="mail-content"></div><a class="mail-show-all">Show all in Mail ›</a></div><div class="mail-address-slot"></div><div class="mail-agent-slot"></div>';
  const update=(selector,html)=>{
   const node=mailPage.querySelector(selector);if(node._mailTemplate===html)return;
   const input=selector==='.mail-address-slot'&&document.activeElement?.id==='mail-address-combobox'?document.activeElement:null;
   const selection=input?[input.selectionStart,input.selectionEnd]:null;
   node.innerHTML=html;node._mailTemplate=html;
   if(input){const next=node.querySelector('#mail-address-combobox');next?.focus();if(next&&selection[0]!==null)next.setSelectionRange(...selection)}
  };
  update('.mail-summary-slot',renderHeader());
  update('.mail-notice-slot',state.settings.enabled?'':'<p class="mail-paused-notice">Receiving paused · New mail is rejected. Existing mail remains available.</p>');
  for(const tab of mailPage.querySelectorAll('[data-mail-tab]')){tab.setAttribute('aria-selected',String(tab.dataset.mailTab===state.tab));tab.querySelector('span').textContent=String(tab.dataset.mailTab==='inbox'?count():pending())}
  update('.mail-content',state.tab==='inbox'?renderInbox():renderOutbox());
  mailPage.querySelector('.mail-show-all').href='/?mail='+state.tab+'&box='+encodeURIComponent(state.boxId)+'#mail';
  update('.mail-address-slot',renderAddresses());
  const filtersOpen=!!mailPage.querySelector('.mail-filter-details[open]');
  update('.mail-agent-slot',renderAgentAccess());
  if(filtersOpen)mailPage.querySelector('.mail-filter-details')?.setAttribute('open','');
  renderAddressOptions();
 }
 async function loadDetail(id){
  if(!state.boxId||!id)return;
  detailId=id;
  const boxId=state.boxId,epoch=requestEpoch;
  state.detail=null;state.detailError='';state.detailLoading=true;state.otpRevealed=false;renderDetail();
  try{const mail=await api.request(base()+'/messages/'+encodeURIComponent(id));if(epoch===requestEpoch&&boxId===state.boxId){state.detail=mail;state.detailLoading=false;renderDetail()}}
  catch(error){if(epoch===requestEpoch&&boxId===state.boxId){state.detailError=shortError(error);state.detailLoading=false;renderDetail()}}
 }
 function renderDetail(){
  if(!detailPage)return;
  if(state.detailLoading){detailPage.innerHTML='<div class="mail-empty"><strong>Loading email…</strong></div>';return}
  if(state.detailError){detailPage.innerHTML=`<div class="mail-error"><strong>Email could not be loaded</strong><p>${esc(state.detailError)}</p><button type="button" data-mail-action="retry-detail">Retry</button></div>`;return}
  const mail=state.detail;if(!mail){detailPage.innerHTML='';return}
  const secret=otpMail(mail),body=secret&&!state.otpRevealed?maskCode(mail.text):mail.text;
  const reveal=secret?`<div class="mail-secret"><div><strong>Verification code</strong><small>${state.otpRevealed?'Shown until you leave this email.':'Hidden until you choose Reveal.'}</small></div><button type="button" data-mail-action="reveal">${state.otpRevealed?'Hide':'Reveal'}</button></div>`:'';
  const attachments=(mail.attachments||[]).map(file=>`<div class="mail-attachment"><span aria-hidden="true">▣</span><div><strong>${esc(file.name)}</strong><small>${esc(file.contentType)} · ${esc(Math.ceil((file.size||0)/1024))} KB</small></div><a class="mail-download" href="${base()}/messages/${encodeURIComponent(mail.id)}/attachments/${encodeURIComponent(file.id)}" download="${esc(file.name)}">Download</a></div>`).join('');
  detailPage.innerHTML=`<div class="mail-untrusted">Untrusted external content · Check links and attachments.</div><section class="ip-card mail-detail-head"><div>${mail.quarantined?pill('Quarantined','danger'):pill('Received '+when(mail.receivedAt))}</div><h2>${esc(mail.subject)}</h2><dl><div><dt>From</dt><dd>${esc(mail.fromName||'')} &lt;${esc(mail.from)}&gt;</dd></div><div><dt>To</dt><dd>${esc(state.settings?.address||'')}</dd></div></dl><div class="mail-auth">${pill('SPF '+(mail.spf||'Unknown'),mail.spf==='pass'||mail.spf==='Pass'?'good':'danger')}${pill('DKIM '+(mail.dkim||'Unknown'),mail.dkim==='pass'||mail.dkim==='Pass'?'good':'danger')}</div></section>${card('Message',`${reveal}<div class="mail-body">${esc(body)}</div>`)}${attachments?card('Attachments',attachments):''}<button class="ip-page-secondary mail-mark-read" type="button" data-mail-action="read" ${mail.unread?'':'disabled'}>${mail.unread?'Mark as read':'Marked as read'}</button><p id="mail-detail-status" class="mail-inline-status" role="status"></p>`;
 }
 function fillReview(item){
  reviewItem=item;
  review.querySelector('[name="to"]').value=Array.isArray(item.to)?item.to.join(', '):item.to||'';
  review.querySelector('[name="subject"]').value=item.subject||'';
  review.querySelector('[name="text"]').value=item.text||'';
  review.querySelector('[name="reason"]').value=item.reason||'';
  review.querySelector('.mail-reject-reason').hidden=true;
  review.querySelector('[data-review="reject"]').textContent='Reject';
  review.querySelector('.mail-review-status').textContent=item.status==='pending_approval'?'Nothing is sent until you approve.':item.status==='rejected'?'Rejected: '+(item.reason||''):'Status: '+item.status;
  for(const button of review.querySelectorAll('[data-review="approve"],[data-review="reject"]'))button.hidden=item.status!=='pending_approval';
  for(const input of review.querySelectorAll('input,textarea'))input.readOnly=item.status!=='pending_approval';
 }
 function openReview(item,opener){
  if(!item)return;returnFocus=opener;fillReview(item);
  review.showModal();review.querySelector('[name="to"]').focus();
 }
 function closeReview(){if(review.open)review.close();returnFocus?.focus?.({preventScroll:true});returnFocus=null}
 async function decide(action){
  if(!reviewItem||reviewItem.status!=='pending_approval')return;
  const status=review.querySelector('.mail-review-status'),reason=review.querySelector('[name="reason"]').value.trim();
  if(action==='reject'&&review.querySelector('.mail-reject-reason').hidden){review.querySelector('.mail-reject-reason').hidden=false;review.querySelector('[data-review="reject"]').textContent='Confirm rejection';review.querySelector('[name="reason"]').focus();return}
  if(action==='reject'&&!reason){status.textContent='Add a reason for the agent.';return}
  if(action==='approve'&&!review.querySelector('form').reportValidity())return;
  const buttons=[...review.querySelectorAll('footer button')];buttons.forEach(button=>button.disabled=true);
  try{
   const path=base()+'/outbox/'+encodeURIComponent(reviewItem.outboxId);
   let version=reviewItem.version;
   if(action==='approve'){
    const edit={to:review.querySelector('[name="to"]').value.split(',').map(value=>value.trim()).filter(Boolean),subject:review.querySelector('[name="subject"]').value.trim(),text:review.querySelector('[name="text"]').value.trim(),version};
    if(edit.to.join(', ')!==(reviewItem.to||[]).join(', ')||edit.subject!==reviewItem.subject||edit.text!==reviewItem.text){const updated=await api.request(path,'PUT',edit);version=updated.version}
    await api.request(path+'/approve','POST',{version});
   }else await api.request(path+'/reject','POST',{reason,version});
   closeReview();state.outboxLoaded=false;void loadOutbox();void loadSettings(state.boxId);void loadApprovals();
  }catch(error){
   if(error.status===409){
    try{const fresh=await api.request(base()+'/outbox/'+encodeURIComponent(reviewItem.outboxId));fillReview(fresh);status.textContent='This draft changed; review again'}
    catch{status.textContent='This draft changed; review again. Close and reopen Outbox.'}
    state.outboxLoaded=false;void loadOutbox();
   }else status.textContent=shortError(error)
  }
  finally{buttons.forEach(button=>button.disabled=false)}
 }
 function installReview(){
  review=document.createElement('dialog');review.id='mail-review';review.className='mail-review';review.innerHTML='<form method="dialog" class="mail-review-shell"><header><div><small>OUTBOX APPROVAL</small><h2>Review email</h2></div><button type="button" data-review="close" aria-label="Close review">×</button></header><div class="mail-review-scroll"><p class="mail-review-status" role="status"></p><label>To<input name="to" type="email" multiple required></label><label>Subject<input name="subject" required></label><label>Body<textarea name="text" rows="9" required></textarea></label><div class="mail-reject-reason" hidden><label>Reason for rejection<textarea name="reason" rows="3" placeholder="Tell the agent why this should not be sent"></textarea></label></div></div><footer><button type="button" data-review="reject">Reject</button><button type="button" data-review="approve">Approve and send</button></footer></form>';
  document.body.append(review);
  review.querySelector('form').addEventListener('submit',event=>event.preventDefault());
  review.addEventListener('click',event=>{if(event.target===review)closeReview();const action=event.target.closest('[data-review]')?.dataset.review;if(action==='close')closeReview();else if(action==='approve'||action==='reject')void decide(action)});
  review.addEventListener('close',()=>{returnFocus?.focus?.({preventScroll:true});returnFocus=null});
 }
 async function openOutbox(boxId,status='pending_approval'){
  if(boxId&&api.getBox()?.id!==boxId)await api.openBox(boxId);
  if(!api.getBox())return;
  if($('inspect').hidden)$('chat-info').click();
  state.tab='outbox';state.outboxStatus=status;state.outboxLoaded=false;api.navigate('mail');void loadOutbox();
 }
 async function openApprovals(){
  const first=state.approvals.items?.[0];
  await openOutbox(first?.boxId||api.getBox()?.id);
 }
 function refreshDrafts(){
  if(!api.isOwner())return;
  void loadApprovals();
  if(state.boxId){void loadSettings(state.boxId);if(state.tab==='outbox')void loadOutbox()}
 }
 async function openMessage(boxId,id){
  if(boxId&&api.getBox()?.id!==boxId)await api.openBox(boxId);
  if(!api.getBox())return;
  if($('inspect').hidden)$('chat-info').click();
  api.navigate('mailDetail');await loadDetail(id);
 }
 function mount(options){
  api=options;mailPage=api.page('mail');detailPage=api.page('mailDetail');
  const group=document.createElement('section');mainGroup=group;group.className='ip-group mail-group';group.innerHTML='<h3 class="ip-heading">Mail</h3><div class="ip-card ip-list"></div>';group.hidden=true;
  mainRow=api.row('mail','mail','Mail','',()=>{state.tab='inbox';api.navigate('mail');if(!state.messagesLoaded)void loadMessages()});mainRow.hidden=true;
  group.querySelector('.ip-list').append(mainRow);$('inspect-prototype-resources').before(group);
  const top=document.createElement('a');top.id='mail-approval';top.href='/?mail=outbox#mail';top.innerHTML='<span class="mail-approval-label">Approvals</span><span class="mail-approval-icon" aria-hidden="true">✉</span><b>0</b>';top.hidden=true;$('refresh').before(top);
  const mobile=document.createElement('a');mobile.id='mail-approval-mobile';mobile.href='/?mail=outbox#mail';mobile.innerHTML='<span aria-hidden="true">✉</span><b>0</b>';mobile.hidden=true;$('chat-info').before(mobile);
  installReview();
  mailPage.addEventListener('click',async event=>{
   const tab=event.target.closest('[data-mail-tab]');if(tab){state.tab=tab.dataset.mailTab;renderMail();if(state.tab==='inbox'&&!state.messagesLoaded)void loadMessages();if(state.tab==='outbox'&&!state.outboxLoaded)void loadOutbox();return}
   const folder=event.target.closest('[data-mail-filter]');if(folder){state.folder=folder.dataset.mailFilter;state.messagesLoaded=false;state.messagesLoading=false;state.messages=[];state.messagesCursor='';void loadMessages();return}
   const outboxTab=event.target.closest('[data-outbox-tab]');if(outboxTab){state.outboxStatus=outboxTab.dataset.outboxTab;state.outboxLoaded=false;state.outboxLoading=false;state.outbox=[];state.outboxCursor='';void loadOutbox();return}
   const mail=event.target.closest('[data-mail-id]');if(mail){api.navigate('mailDetail');void loadDetail(mail.dataset.mailId);return}
   const draft=event.target.closest('[data-outbox-id]');if(draft){openReview(state.outbox.find(item=>item.outboxId===draft.dataset.outboxId),draft);return}
   const action=event.target.closest('[data-mail-action]')?.dataset.mailAction;
   if(action==='copy'){try{await navigator.clipboard.writeText(state.settings?.address||'');event.target.textContent='Copied'}catch{setStatus('mail-settings-status','Could not copy address')}return}
   if(action==='choose-address'){const choice=addressChoices()[Number(event.target.closest('[data-choice-index]')?.dataset.choiceIndex)];if(choice){if(choice.kind==='grant')void saveAddressGrant(choice.item.id,true);else void createAddress(choice.local)}return}
   if(action==='suggest-address'){const chip=event.target.closest('[data-mail-action]');if(chip.dataset.addressId)void saveAddressGrant(chip.dataset.addressId,true);else if(chip.dataset.localPart)void createAddress(chip.dataset.localPart);return}
   if(action==='remove-address'){void saveAddressGrant(event.target.closest('[data-address-id]').dataset.addressId,false);return}
   if(action==='undo-address'){const id=state.undoAddressId;state.undoAddressId='';clearTimeout(undoTimer);if(id)void saveAddressGrant(id,true);return}
   if(action==='undo-receiving'){void setReceiving(true);return}
   if(action==='skip-label'){state.labelAddressId='';renderMail();return}
   if(action==='open-access'){api.navigate('access');return}
   if(action==='retry-policy'){void loadPolicy(state.boxId);return}
   if(action==='retry-addresses'){void loadAddresses(state.boxId);return}
   if(action==='retry-messages')void loadMessages();if(action==='retry-outbox')void loadOutbox();if(action==='more-messages')void loadMessages(true);if(action==='more-outbox')void loadOutbox(true);
  });
  mailPage.addEventListener('change',event=>{
   if(event.target.id==='mail-enabled')void setReceiving(event.target.checked);
   if(event.target.id==='mail-subscribed')void saveSettings({subscribed:event.target.checked});
   if(event.target.id==='mail-sender-filter')void saveSettings({filters:{sender:event.target.value}});
   if(event.target.id==='mail-subject-filter')void saveSettings({filters:{subject:event.target.value}});
  });
  mailPage.addEventListener('input',event=>{if(event.target.id!=='mail-address-combobox')return;state.addressQuery=event.target.value;state.addressOpen=true;state.addressActive=0;state.addressError='';renderAddressOptions();setStatus('mail-address-status','')});
  mailPage.addEventListener('focusin',event=>{if(event.target.id==='mail-address-combobox'){state.addressOpen=true;renderAddressOptions()}});
  mailPage.addEventListener('keydown',event=>{
   if(event.target.id!=='mail-address-combobox')return;
   const choices=addressChoices();
   if(event.key==='Escape'){state.addressOpen=false;renderAddressOptions();return}
   if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();state.addressOpen=true;state.addressActive=(state.addressActive+(event.key==='ArrowDown'?1:-1)+choices.length)%Math.max(choices.length,1);renderAddressOptions();return}
   if(event.key==='Enter'){event.preventDefault();const choice=choices[state.addressActive];if(choice){state.addressOpen=false;if(choice.kind==='grant')void saveAddressGrant(choice.item.id,true);else void createAddress(choice.local)}else{state.addressError=state.addressQuery.trim()?'Use letters, numbers, dots, underscores, plus or hyphens; begin and end with a letter or number.':'Choose or type an address.';setStatus('mail-address-status',state.addressError)}}
  });
  document.addEventListener('pointerdown',event=>{if(!mailPage?.contains(event.target)||!event.target.closest('.mail-address-combobox-wrap')){state.addressOpen=false;renderAddressOptions()}});
  mailPage.addEventListener('submit',event=>{
   if(!event.target.matches('.mail-address-label-form'))return;
   event.preventDefault();void saveAddressLabel(event.target);
  });
  detailPage.addEventListener('click',async event=>{
   const action=event.target.closest('[data-mail-action]')?.dataset.mailAction;if(!action)return;
   if(action==='retry-detail'){void loadDetail(detailId);return}
   if(action==='reveal'){state.otpRevealed=!state.otpRevealed;renderDetail();detailPage.querySelector('[data-mail-action="reveal"]')?.focus();return}
   if(action==='read'&&state.detail){try{await api.request(base()+'/messages/'+encodeURIComponent(state.detail.id)+'/read','POST',{read:true});state.detail.unread=false;state.settings.unread=Math.max(0,count()-1);renderDetail();renderMail();signals()}catch(error){setStatus('mail-detail-status',shortError(error))}}
  });
  void loadApprovals();approvalTimer=setInterval(()=>{if(!document.hidden)refreshDrafts()},15000);
  document.addEventListener('visibilitychange',()=>{if(!document.hidden)refreshDrafts()});
  signals();renderMail();
  return {backTarget:key=>key==='mailDetail'?'mail':'',onShow:key=>{if(key==='mail'){renderMail();if(state.boxId){void loadPolicy(state.boxId);void loadAddresses(state.boxId)}if(state.tab==='inbox')void loadMessages();if(state.tab==='outbox')void loadOutbox()}},onBox:box=>{if(box?.id===state.boxId)return;requestEpoch++;outboxRefreshQueued=false;clearTimeout(undoTimer);clearTimeout(receiveUndoTimer);state.boxId=box?.id||'';state.settings=null;state.settingsSaving=false;state.receiveUndo=false;state.receiveError='';state.messages=[];state.messagesLoaded=false;state.outbox=[];state.outboxLoaded=false;state.outboxLoading=false;state.detail=null;state.policy=null;state.policyLoading=false;state.policyError='';state.addresses=[];state.addressesLoaded=false;state.addressesLoading=false;state.addressSaving=false;state.addressQuery='';state.addressOpen=false;state.addressActive=0;state.labelAddressId='';state.undoAddressId='';state.addressError='';state.addressStatus='';signals();renderMail();if(box&&state.globalAvailable===true)void loadSettings(box.id)},refreshApprovals:loadApprovals,refreshDrafts,openMessage,openApprovals};
 }
 function notice(box,message){
  const mail=message?.mail;if(message?.direction!=='system'||!mail||!['mail_batch','outbox_status'].includes(mail.kind))return null;
  const row=document.createElement('div');row.className='msg system mail-system-row';
  const details=document.createElement('details');details.className='mail-chat-line';
  const summary=document.createElement('summary');
  if(mail.kind==='mail_batch'){
   const items=Array.isArray(mail.items)?mail.items:[];
   const label=document.createElement('span');label.textContent='✉ '+(items.length+(Number(mail.more)||0))+' new mails · '+items.slice(0,3).map(item=>item.fromName||item.from).join(', ')+(mail.more?' +'+mail.more:'');summary.append(label);
   const content=document.createElement('div');content.className='mail-notice-items';
   const hint=document.createElement('p');hint.textContent='Untrusted external mail';content.append(hint);
   for(const item of items){const button=document.createElement('button');button.type='button';button.textContent=(item.fromName||item.from)+' · '+item.subject+' · '+(otpMail(item)?maskCode(item.preview):item.preview||'');button.onclick=()=>void openMessage(box.id,item.id);content.append(button)}
   if(mail.more){const more=document.createElement('small');more.textContent='+'+mail.more+' more in Inbox';content.append(more)}
   details.append(summary,content);
  }else{
   const status=mail.status,icon=status==='sent'?'✓':status==='rejected'?'×':status==='failed'?'!':'↗';
   const button=document.createElement('button');button.type='button';button.className='mail-status-link';button.title=[mail.subject,mail.reason].filter(Boolean).join(' · ');
   const label=document.createElement('span');label.textContent=icon+' '+(status==='pending_approval'?'Draft to ':'Mail to ')+(mail.to||'')+' '+(status==='pending_approval'?'pending approval':status)+(mail.reason?' · '+mail.reason:'');button.append(label);
   button.onclick=()=>void openOutbox(box.id,status);row.append(button);return row;
  }
  row.append(details);return row;
 }
 window.VBoxMail={mount,notice,readTools,composeTools};
})();
