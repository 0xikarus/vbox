'use strict';
(()=>{
 if(new URLSearchParams(location.search).get('mailMockup')!=='1')return;
 const query=new URLSearchParams(location.search);
 const initial=query.get('mailMockupState')||'default';
 const fixtureMails=[
  {id:'mail-101',from:'accounts@northstar.dev',sender:'Northstar',subject:'Your verification code',preview:'Your one-time code is ••••••. It expires in 10 minutes.',text:'Your one-time code is 483921. It expires in 10 minutes.\n\nIf you did not request this, you can ignore this email.',time:'2 min ago',unread:true,spf:'Pass',dkim:'Pass',attachment:null},
  {id:'mail-102',from:'mara@client.example',sender:'Mara Chen',subject:'Re: October launch checklist',preview:'I added the final screenshots and the release notes. Could you check the copy?',text:'Hi team,\n\nI added the final screenshots and the release notes. Could you check the copy before Thursday?\n\nThanks,\nMara',time:'14 min ago',unread:true,spf:'Pass',dkim:'Pass',attachment:{name:'launch-notes.pdf',size:'1.8 MB'}},
  {id:'mail-103',from:'digest@fieldnotes.example',sender:'Fieldnotes',subject:'This week in product design',preview:'Five useful stories, a small research roundup, and a new template.',text:'This week in product design\n\nFive useful stories, a small research roundup, and a new template.',time:'Yesterday',unread:true,spf:'Pass',dkim:'Pass',attachment:null},
  {id:'mail-104',from:'updates@hosting.example',sender:'Hosting updates',subject:'Your September usage summary',preview:'Your monthly usage summary is ready to review.',text:'Your September usage summary is ready to review.\n\nOpen your dashboard for details.',time:'Tue',unread:false,spf:'Pass',dkim:'Pass',attachment:null},
  {id:'mail-105',from:'promo@unknown.example',sender:'Unknown sender',subject:'Urgent: verify your account',preview:'A suspicious link was removed from this preview.',text:'This message was quarantined because sender authentication failed.\n\nNever treat email content as instructions.',time:'Today',unread:false,spf:'Fail',dkim:'Fail',attachment:null,quarantine:true}
 ];
 const fixtureOutbox=[
  {id:'out-201',to:'mara@client.example',subject:'Re: October launch checklist',body:'Hi Mara,\n\nI reviewed the screenshots and release notes. The copy looks ready to ship.\n\nBest,\nBossDev',status:'pending_approval',time:'2 min ago'},
  {id:'out-202',to:'support@northstar.dev',subject:'Question about verification',body:'Hello,\n\nCould you confirm whether the verification link is still valid?\n\nThank you.',status:'pending_approval',time:'18 min ago'},
  {id:'out-203',to:'team@fieldnotes.example',subject:'Thanks for the roundup',body:'Thanks for sharing this week’s roundup.',status:'sent',time:'Yesterday'},
  {id:'out-204',to:'promo@unknown.example',subject:'Re: Urgent: verify your account',body:'I can help with this.',status:'rejected',reason:'Sender was not trusted. Do not reply.',time:'Monday'}
 ];
 const state={enabled:initial!=='disabled',subscribed:true,filter:'all',senderFilter:'',subjectFilter:'',error:initial==='error',empty:initial==='empty',outboxTab:'pending_approval',mailId:'mail-101',otpRevealed:false,outboxId:'out-201',reviewMode:'approve'};
 const esc=value=>String(value??'').replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
 const byId=id=>document.getElementById(id);
 const unread=()=>state.enabled&&!state.empty?fixtureMails.filter(mail=>mail.unread&&!mail.quarantine).length:0;
 const pending=()=>fixtureOutbox.filter(item=>item.status==='pending_approval').length;
 let api,mailPage,detailPage,outboxPage,mainRow,outboxRow,review,returnFocus,previewObserver;
 const address=()=>{
  const raw=api?.getBox()?.id||'bossdev';
  return raw.toLowerCase().replace(/[^a-z0-9.-]/g,'-')+'@example.test';
 };
 function pill(label,kind=''){return `<span class="mail-pill ${kind}">${esc(label)}</span>`}
 function notice(text,kind=''){return `<div class="mail-notice ${kind}" role="status">${esc(text)}</div>`}
 function card(title,content,extra=''){return `<section class="ip-card mail-card ${extra}"><h3>${esc(title)}</h3>${content}</section>`}
 function renderInbox(){
  const count=unread();
  const status=card('Inbox',`<div class="mail-card-row"><div><strong>${state.enabled?'Receiving mail':'Mail is off'}</strong><small>${state.enabled?'Only known box addresses are accepted at example.test.':'Turn on to receive mail for this box.'}</small></div><label class="mail-switch"><input id="mail-enabled" type="checkbox" role="switch" aria-label="Enable inbox" ${state.enabled?'checked':''}><span aria-hidden="true"></span></label></div>`);
  const addressCard=card('Address',`<div class="mail-address-row"><code>${esc(address())}</code><button type="button" data-mail-action="copy">Copy</button></div><p class="mail-help">Share this address to forward mail to this box. Mail never wakes a hibernated box.</p>`);
  const subscribe=card('Agent subscription',`<div class="mail-card-row"><div><strong>${state.subscribed?'Subscribed':'Not subscribed'}</strong><small>${state.subscribed?'New mail is batched into agent chat.':'Mail stays in the inbox until opened.'}</small></div><label class="mail-switch"><input id="mail-subscribed" type="checkbox" role="switch" aria-label="Subscribe agent to inbox" ${state.subscribed?'checked':''} ${state.enabled?'':'disabled'}><span aria-hidden="true"></span></label></div><div class="mail-filter-fields"><label>Sender filter <input id="mail-sender-filter" placeholder="Any sender" value="${esc(state.senderFilter)}" ${state.enabled&&state.subscribed?'':'disabled'}></label><label>Subject contains <input id="mail-subject-filter" placeholder="Any subject" value="${esc(state.subjectFilter)}" ${state.enabled&&state.subscribed?'':'disabled'}></label></div><p class="mail-help">A sleeping box receives its queued batch on its next start.</p>`);
  let list='';
  if(!state.enabled)list=card('Messages',`<div class="mail-empty"><span class="mail-empty-icon">✉</span><strong>Inbox is off</strong><p>Enable mail above to receive messages at this address.</p></div>`);
  else if(state.error)list=card('Messages',`<div class="mail-error"><strong>Mail could not be loaded</strong><p>The inbox is temporarily unavailable. Your stored messages are safe.</p><button type="button" data-mail-action="retry">Try again</button></div>`);
  else if(state.empty)list=card('Messages',`<div class="mail-empty"><span class="mail-empty-icon">✉</span><strong>No mail yet</strong><p>Messages sent to ${esc(address())} will appear here.</p></div>`);
  else {
   const tabs=`<div class="mail-tabs" role="tablist" aria-label="Inbox filter">${[['all','All'],['unread','Unread'],['quarantine','Quarantine']].map(([key,label])=>`<button type="button" role="tab" aria-selected="${state.filter===key}" data-mail-filter="${key}">${label}${key==='unread'?` <span>${count}</span>`:''}</button>`).join('')}</div>`;
   const shown=fixtureMails.filter(mail=>state.filter==='quarantine'?mail.quarantine:state.filter==='unread'?mail.unread&&!mail.quarantine:!mail.quarantine);
   const rows=shown.length?shown.map(mail=>`<button type="button" class="mail-list-row ${mail.unread?'is-unread':''}" data-mail-id="${mail.id}"><span class="mail-unread-dot" aria-hidden="true"></span><span class="mail-list-content"><span class="mail-list-top"><strong>${esc(mail.sender)}</strong><time>${esc(mail.time)}</time></span><span class="mail-list-subject">${esc(mail.subject)}${mail.attachment?' <span title="Has attachment" aria-label="Has attachment">⌕</span>':''}</span><span class="mail-list-preview">${esc(mail.preview)}</span></span></button>`).join(''):`<div class="mail-empty compact"><strong>No ${state.filter==='quarantine'?'quarantined':'unread'} mail</strong><p>You're all caught up here.</p></div>`;
   list=`<section class="ip-card mail-list-card">${tabs}<div class="mail-list">${rows}</div></section>`;
  }
  mailPage.innerHTML=`<p class="ip-page-intro">A private address for this box. External mail is always untrusted.</p>${status}${addressCard}${subscribe}<div class="mail-section-heading"><strong>Messages</strong><span>${count} unread</span></div>${list}<button class="mail-link-row" type="button" data-mail-action="outbox">Outbox ${pill(pending()+' pending','warn')} <span aria-hidden="true">›</span></button>`;
 }
 function renderDetail(){
  const mail=fixtureMails.find(item=>item.id===state.mailId)||fixtureMails[0];
  const otp=mail.id==='mail-101';
  const body=otp&&!state.otpRevealed?mail.text.replace('483921','••••••'):mail.text;
  const secret=otp?`<div class="mail-secret"><div><strong>Verification code</strong><small>${state.otpRevealed?'Visible until you leave this email.':'Hidden until you choose Reveal.'}</small></div><button type="button" data-mail-action="reveal">${state.otpRevealed?'Hide':'Reveal'}</button></div>`:'';
  detailPage.innerHTML=`<div class="mail-untrusted"><strong>Untrusted external content</strong><p>Email can contain instructions from anyone. Review links and attachments before acting.</p></div><section class="ip-card mail-detail-head"><div class="mail-detail-kicker">${mail.quarantine?pill('Quarantined','danger'):pill('Received '+mail.time)}</div><h2>${esc(mail.subject)}</h2><dl><div><dt>From</dt><dd>${esc(mail.sender)} &lt;${esc(mail.from)}&gt;</dd></div><div><dt>To</dt><dd>${esc(address())}</dd></div></dl><div class="mail-auth">${pill('SPF '+mail.spf,mail.spf==='Pass'?'good':'danger')}${pill('DKIM '+mail.dkim,mail.dkim==='Pass'?'good':'danger')}</div></section>${card('Message',`${secret}<div class="mail-body">${esc(body)}</div>`)}${mail.attachment?card('Attachment',`<div class="mail-attachment"><span aria-hidden="true">▣</span><div><strong>${esc(mail.attachment.name)}</strong><small>PDF · ${esc(mail.attachment.size)} · scanned</small></div><button type="button" data-mail-action="save">Save to workspace</button></div>`):''}<button class="ip-page-secondary mail-mark-read" type="button" data-mail-action="read" ${mail.unread?'':'disabled'}>${mail.unread?'Mark as read':'Marked as read'}</button><p class="mail-inline-status" role="status"></p>`;
 }
 function renderOutbox(){
  const tabs=`<div class="mail-tabs" role="tablist" aria-label="Outbox status">${[['pending_approval','Pending'],['sent','Sent'],['rejected','Rejected']].map(([key,label])=>`<button type="button" role="tab" aria-selected="${state.outboxTab===key}" data-outbox-tab="${key}">${label}${key==='pending_approval'?` <span>${pending()}</span>`:''}</button>`).join('')}</div>`;
  const items=fixtureOutbox.filter(item=>item.status===state.outboxTab);
  const rows=items.length?items.map(item=>`<button class="mail-outbox-row" type="button" data-outbox-id="${item.id}"><span class="mail-outbox-icon" aria-hidden="true">${item.status==='sent'?'✓':item.status==='rejected'?'×':'↗'}</span><span><strong>${esc(item.subject)}</strong><small>To ${esc(item.to)}</small><em>${item.status==='rejected'?esc(item.reason):esc(item.body.replace(/\s+/g,' ').slice(0,95))}</em></span><time>${esc(item.time)}</time></button>`).join(''):`<div class="mail-empty compact"><strong>No ${state.outboxTab==='pending_approval'?'drafts to approve':state.outboxTab+' mail'}</strong><p>${state.outboxTab==='pending_approval'?'Agent drafts will wait here for your review.':'Nothing in this view yet.'}</p></div>`;
  outboxPage.innerHTML=`<p class="ip-page-intro">Every agent email waits for your approval before it is sent.</p>${pending()?notice(pending()+' messages need your review. Nothing has been sent yet.','warn'):notice('No mail is waiting for approval.','good')}<section class="ip-card mail-list-card">${tabs}<div class="mail-list">${rows}</div></section>${card('How approval works','<p class="mail-help">Open a draft to edit To, subject, and body. Approve and send, or reject with a reason the agent can read.</p>')}`;
 }
 function updateSignals(){
  const count=pending();
  if(mainRow){mainRow.querySelector('.ip-row-value').textContent=state.enabled?unread()+' unread · '+address():'Off · '+address();mainRow.hidden=!api.isOwner()}
  if(outboxRow){outboxRow.querySelector('.ip-row-value').textContent=count+' pending';outboxRow.hidden=!api.isOwner()}
  const signal=byId('mail-mockup-approval');if(signal){signal.hidden=!api.isOwner()||!count;signal.querySelector('b').textContent=String(count)}
  const sidebar=byId('mail-mockup-sidebar-signal');if(sidebar){sidebar.hidden=!api.isOwner()||!count;sidebar.querySelector('b').textContent=String(count)}
  const push=byId('mail-mockup-push');if(push){push.hidden=!count;push.querySelector('p').textContent=count+' agent '+(count===1?'draft is':'drafts are')+' waiting in Outbox.'}
 }
 function openReview(item,opener){
  state.outboxId=item.id;state.reviewMode='approve';returnFocus=opener;
  review.querySelector('[name="to"]').value=item.to;
  review.querySelector('[name="subject"]').value=item.subject;
  review.querySelector('[name="body"]').value=item.body;
  review.querySelector('[name="reason"]').value=item.reason||'';
  review.querySelector('.mail-reject-reason').hidden=true;
  review.querySelector('.mail-review-status').textContent=item.status==='pending_approval'?'Nothing is sent until you approve.':item.status==='sent'?'Sent after owner approval.':'Rejected: '+item.reason;
  review.querySelector('[data-review="approve"]').hidden=item.status!=='pending_approval';
  review.querySelector('[data-review="reject"]').hidden=item.status!=='pending_approval';
  review.querySelectorAll('input,textarea').forEach(input=>input.readOnly=item.status!=='pending_approval');
  review.showModal();review.querySelector('[name="to"]').focus();
 }
 function closeReview(){review.close();returnFocus?.focus?.({preventScroll:true});returnFocus=null}
 function installReview(){
  review=document.createElement('dialog');review.id='mail-mockup-review';review.className='mail-review';review.innerHTML=`<form method="dialog" class="mail-review-shell"><header><div><small>OUTBOX APPROVAL</small><h2>Review email</h2></div><button type="button" data-review="close" aria-label="Close review">×</button></header><div class="mail-review-scroll"><p class="mail-review-status" role="status"></p><label>To<input name="to" type="email" required></label><label>Subject<input name="subject" required></label><label>Body<textarea name="body" rows="9" required></textarea></label><div class="mail-reject-reason" hidden><label>Reason for rejection<textarea name="reason" rows="3" placeholder="Tell the agent why this should not be sent"></textarea></label></div></div><footer><button type="button" data-review="reject">Reject</button><button type="button" data-review="approve">Approve and send</button></footer></form>`;
  document.body.append(review);
  review.querySelector('form').addEventListener('submit',event=>event.preventDefault());
  review.addEventListener('click',event=>{if(event.target===review)closeReview()});
  review.addEventListener('close',()=>{returnFocus?.focus?.({preventScroll:true});returnFocus=null});
  review.addEventListener('click',event=>{
   const action=event.target.closest('[data-review]')?.dataset.review;if(!action)return;
   if(action==='close'){closeReview();return}
   const item=fixtureOutbox.find(entry=>entry.id===state.outboxId);if(!item||item.status!=='pending_approval')return;
   if(action==='reject'&&state.reviewMode!=='reject'){state.reviewMode='reject';review.querySelector('.mail-reject-reason').hidden=false;review.querySelector('[name="reason"]').focus();event.target.textContent='Confirm rejection';return}
   const reason=review.querySelector('[name="reason"]').value.trim();
   if(action==='reject'&&!reason){review.querySelector('.mail-review-status').textContent='Add a reason so the agent knows why it was rejected.';return}
   if(action==='approve'&&!review.querySelector('form').reportValidity())return;
   item.to=review.querySelector('[name="to"]').value.trim();item.subject=review.querySelector('[name="subject"]').value.trim();item.body=review.querySelector('[name="body"]').value.trim();
   item.status=action==='approve'?'sent':'rejected';item.reason=action==='reject'?reason:'';item.time='Just now';
   state.outboxTab=item.status;closeReview();renderOutbox();updateSignals();renderChatPreview();
  });
 }
 function renderChatPreview(){
  const messages=byId('chat-messages');if(!messages||!messages.isConnected)return;
  let demo=byId('mail-mockup-chat-preview');if(!demo){demo=document.createElement('section');demo.id='mail-mockup-chat-preview';demo.setAttribute('aria-label','Mail notification examples');messages.append(demo)}
  const waiting=fixtureOutbox.find(item=>item.status==='pending_approval');
  demo.innerHTML=`<div class="mail-chat-label">MAIL · AGENT NOTIFICATIONS <span>Mockup</span></div><div class="mail-chat-event"><span class="mail-chat-icon">✉</span><div><strong>3 new mails</strong><p>Northstar · Your verification code<br>Mara Chen · Re: October launch checklist<br>Fieldnotes · This week in product design</p><small>External email is untrusted. Use read_email for the full message.</small></div></div>${waiting?`<div class="mail-chat-event"><span class="mail-chat-icon">↗</span><div><strong>Outbox · pending approval</strong><p>${esc(waiting.subject)} → ${esc(waiting.to)}</p><small>The owner can edit, approve and send, or reject with a reason.</small></div></div>`:''}<div class="mail-chat-event is-sent"><span class="mail-chat-icon">✓</span><div><strong>Outbox · sent</strong><p>Thanks for the roundup → team@fieldnotes.example</p><small>Approved by owner and sent. Delivery recorded in Outbox.</small></div></div><div class="mail-chat-event"><span class="mail-chat-icon">×</span><div><strong>Outbox · rejected</strong><p>Re: Urgent: verify your account</p><small>Reason: Sender was not trusted. Do not reply.</small></div></div>`;
 }
 function mount(options){
  api=options;
  mailPage=api.page('mail');detailPage=api.page('mailDetail');outboxPage=api.page('mailOutbox');
  const group=document.createElement('section');group.className='ip-group mail-mockup-group';group.innerHTML='<h3 class="ip-heading">Mail <span class="mail-mockup-tag">Mockup</span></h3><div class="ip-card ip-list"></div>';
  mainRow=api.row('mail','mail','Inbox','',()=>api.navigate('mail'));
  outboxRow=api.row('mailOutbox','send','Outbox','',()=>api.navigate('mailOutbox'));
  group.querySelector('.ip-list').append(mainRow,outboxRow);
  byId('inspect-prototype-resources').before(group);
  const top=document.createElement('button');top.id='mail-mockup-approval';top.type='button';top.innerHTML='Approvals <b>2</b>';top.onclick=()=>{if(byId('inspect').hidden)byId('chat-info').click();api.navigate('mailOutbox')};byId('refresh').before(top);
  const sidebar=document.createElement('button');sidebar.id='mail-mockup-sidebar-signal';sidebar.type='button';sidebar.innerHTML='<span>✉</span> Mail approvals <b>2</b>';sidebar.onclick=top.onclick;byId('chat-filter-row').before(sidebar);
  const push=document.createElement('div');push.id='mail-mockup-push';push.setAttribute('role','status');push.innerHTML='<span class="mail-push-icon">✉</span><div><strong>Mail approval needed</strong><p>2 agent drafts are waiting in Outbox.</p><button type="button">Review drafts</button></div><button class="mail-push-close" type="button" aria-label="Dismiss notification">×</button>';document.body.append(push);push.querySelector('button:not(.mail-push-close)').onclick=top.onclick;push.querySelector('.mail-push-close').onclick=()=>push.hidden=true;
  installReview();
  mailPage.addEventListener('click',async event=>{
   const mail=event.target.closest('[data-mail-id]');if(mail){state.mailId=mail.dataset.mailId;state.otpRevealed=false;api.navigate('mailDetail');return}
   const filter=event.target.closest('[data-mail-filter]');if(filter){state.filter=filter.dataset.mailFilter;renderInbox();return}
   const action=event.target.closest('[data-mail-action]')?.dataset.mailAction;if(!action)return;
   if(action==='outbox')api.navigate('mailOutbox');
   if(action==='retry'){state.error=false;renderInbox()}
   if(action==='copy'){try{await navigator.clipboard.writeText(address())}catch{}event.target.textContent='Copied';setTimeout(()=>{if(event.target.isConnected)event.target.textContent='Copy'},1400)}
  });
  mailPage.addEventListener('change',event=>{
   if(event.target.id==='mail-enabled'){state.enabled=event.target.checked;renderInbox();updateSignals()}
   if(event.target.id==='mail-subscribed'){state.subscribed=event.target.checked;renderInbox()}
   if(event.target.id==='mail-sender-filter')state.senderFilter=event.target.value;
   if(event.target.id==='mail-subject-filter')state.subjectFilter=event.target.value;
  });
  mailPage.addEventListener('input',event=>{if(event.target.id==='mail-sender-filter')state.senderFilter=event.target.value;if(event.target.id==='mail-subject-filter')state.subjectFilter=event.target.value});
  detailPage.addEventListener('click',event=>{
   const action=event.target.closest('[data-mail-action]')?.dataset.mailAction;if(!action)return;
   if(action==='read'){const mail=fixtureMails.find(item=>item.id===state.mailId);mail.unread=false;renderDetail();updateSignals()}
   if(action==='reveal'){state.otpRevealed=!state.otpRevealed;renderDetail();detailPage.querySelector('[data-mail-action="reveal"]').focus()}
   if(action==='save')detailPage.querySelector('.mail-inline-status').textContent='Mockup: launch-notes.pdf would be saved to this box’s workspace.';
  });
  outboxPage.addEventListener('click',event=>{
   const tab=event.target.closest('[data-outbox-tab]');if(tab){state.outboxTab=tab.dataset.outboxTab;renderOutbox();return}
   const item=event.target.closest('[data-outbox-id]');if(item)openReview(fixtureOutbox.find(entry=>entry.id===item.dataset.outboxId),item);
  });
  previewObserver=new MutationObserver(()=>{if(!byId('mail-mockup-chat-preview'))renderChatPreview()});previewObserver.observe(byId('chat-messages'),{childList:true});
  renderInbox();renderOutbox();renderDetail();renderChatPreview();updateSignals();
  return {backTarget:key=>key==='mailDetail'?'mail':'',onShow:key=>{if(key==='mail')renderInbox();if(key==='mailDetail')renderDetail();if(key==='mailOutbox')renderOutbox()},onBox:()=>updateSignals()};
 }
 window.VBoxMailMockup={enabled:true,mount};
})();
