'use strict';
(()=>{
 const $=s=>document.querySelector(s);
 const listEl=$('#chat-entries'),messagesEl=$('#chat-messages'),appEl=$('#chat-app'),statusEl=$('#chat-status'),inputEl=$('#chat-input'),composer=$('#chat-composer'),attachBtn=$('#attach'),fileInput=$('#attachments'),draftsEl=$('#chat-image-drafts'),forwardMenu=$('#forward-menu'),filterEl=$('#chat-filter'),pushBtn=$('#push-toggle');
 const boxes=new Map(),rows=new Map(),imageURLs=new Map(),answeredQuestions=new Set();
 let selected='',owner=false,boxTimer,msgTimer,lastSignature='',stickToBottom=true;
 let drafts=[],pendingKey='',pendingFingerprint='';
 const seen=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}')}catch{return{}}})();
 const saveSeen=()=>localStorage.setItem('vmboxChatSeen',JSON.stringify(seen));

 async function api(path,method='GET',headers={},body,timeout=60000){
  let r;try{r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(timeout)})}catch{throw Error('Controller connection interrupted. The operation may still be running.')}
  if(r.status===401){$('#login').hidden=false;throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  return r.status===204?null:r.json();
 }
 const boxPath=id=>'/v1/logical-boxes/'+encodeURIComponent(id);

 /* ---------- avatars: desktop preview thumbnails, blob-cached for 60s ---- */
 const avatarCache=new Map(),avatarPending=new Set();
 function avatarRefresh(box){
  if(box.state!=='running'){avatarCache.set(box.id,{url:null,state:box.state,at:Date.now()});return}
  if(avatarPending.has(box.id))return;
  avatarPending.add(box.id);
  fetch(boxPath(box.id)+'/desktop/screenshot?thumbnail=true',{credentials:'same-origin',signal:AbortSignal.timeout(15000)})
   .then(r=>{if(!r.ok)throw Error(r.status);return r.blob()})
   .then(b=>{
    const old=avatarCache.get(box.id);if(old?.url)URL.revokeObjectURL(old.url);
    avatarCache.set(box.id,{url:URL.createObjectURL(b),state:'running',at:Date.now()});
   })
   .catch(()=>avatarCache.set(box.id,{url:null,state:box.state,at:Date.now()}))
   .finally(()=>{avatarPending.delete(box.id);refreshAvatarNodes(box)});
 }
 function refreshAvatarNodes(box){
  const cached=avatarCache.get(box.id);
  document.querySelectorAll('[data-avatar="'+box.id+'"]').forEach(node=>{
   if(node.dataset.state!==box.state)return;
   let img=node.querySelector('img');
   if(cached?.url){if(!img){img=document.createElement('img');img.alt='';node.prepend(img)}if(img.src!==cached.url)img.src=cached.url}
   else img?.remove();
  });
 }
 function avatarNode(box,small){
  const wrap=document.createElement('span');wrap.className='avatar'+(small?' small':'');
  wrap.dataset.avatar=box.id;wrap.dataset.state=box.state;
  const hue=[...box.id].reduce((a,c)=>a+c.charCodeAt(0),0)%360;
  const initials=document.createElement('span');initials.className='initials';initials.style.background='hsl('+hue+' 35% 55%)';initials.style.position='absolute';initials.style.inset='0';initials.style.display='flex';initials.style.alignItems='center';initials.style.justifyContent='center';
  initials.textContent=(box.name||'?').trim().slice(0,2).toUpperCase();wrap.append(initials);
  let cached=avatarCache.get(box.id);
  if(!cached||cached.state!==box.state||Date.now()-cached.at>60000||box.state!=='running'&&!cached)avatarRefresh(box);
  cached=avatarCache.get(box.id);
  if(cached?.state===box.state&&cached.url){const img=document.createElement('img');img.alt='';img.src=cached.url;wrap.prepend(img)}
  const dot=document.createElement('span');dot.className='dot'+(box.state==='running'?' running':'');wrap.append(dot);
  return wrap;
 }

 /* ---------- TV preview: hover the processing bubble for a bigger view ---- */
 const tvPreviewEl=document.createElement('div');tvPreviewEl.className='tv-preview';tvPreviewEl.hidden=true;
 const tvPreviewImg=document.createElement('img');tvPreviewImg.alt='';tvPreviewImg.hidden=true;
 const tvPreviewNote=document.createElement('span');tvPreviewNote.className='tv-preview-note';
 tvPreviewEl.append(tvPreviewImg,tvPreviewNote);
 document.body.append(tvPreviewEl);
 const tvShotCache=new Map();
 function tvShotRender(box){
  const cached=tvShotCache.get(box.id);
  tvPreviewImg.hidden=!cached?.url;
  if(cached?.url&&tvPreviewImg.src!==cached.url)tvPreviewImg.src=cached.url;
  tvPreviewNote.textContent=cached?.url?'Click for the live view':'no desktop preview yet';
 }
 function tvShotRefresh(box){
  const cached=tvShotCache.get(box.id);
  if(box.state!=='running'||(cached&&Date.now()-cached.at<5000))return;
  tvShotCache.set(box.id,{url:cached?.url||'',at:Date.now()});
  fetch(boxPath(box.id)+'/desktop/screenshot',{credentials:'same-origin',signal:AbortSignal.timeout(15000)})
   .then(response=>{if(!response.ok)throw Error(response.status);return response.blob()})
   .then(blob=>{
    const previous=tvShotCache.get(box.id);
    if(previous?.url)URL.revokeObjectURL(previous.url);
    tvShotCache.set(box.id,{url:URL.createObjectURL(blob),at:Date.now()});
    if(!tvPreviewEl.hidden)tvShotRender(box);
   })
   .catch(()=>tvShotCache.set(box.id,{url:cached?.url||'',at:Date.now()}));
 }
 function tvIcon(){
  const ns='http://www.w3.org/2000/svg';
  const svg=document.createElementNS(ns,'svg');
  svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('fill','none');svg.setAttribute('stroke','currentColor');svg.setAttribute('stroke-width','2');svg.setAttribute('stroke-linecap','round');svg.setAttribute('stroke-linejoin','round');svg.setAttribute('aria-hidden','true');
  const screen=document.createElementNS(ns,'rect');screen.setAttribute('width','20');screen.setAttribute('height','15');screen.setAttribute('x','2');screen.setAttribute('y','7');screen.setAttribute('rx','2');screen.setAttribute('ry','2');
  const antenna=document.createElementNS(ns,'polyline');antenna.setAttribute('points','17 2 12 7 7 2');
  svg.append(screen,antenna);
  return svg;
 }
 function showTvPreview(button,box){
  tvPreviewEl.hidden=false;
  tvPreviewEl.style.width=Math.min(860,window.innerWidth-24)+'px';
  tvShotRender(box);tvShotRefresh(box);
  const rect=button.getBoundingClientRect(),height=tvPreviewEl.offsetHeight;
  const width=tvPreviewEl.offsetWidth;
  const left=Math.min(Math.max(12,rect.left-8),Math.max(12,window.innerWidth-width-12));
  let top=rect.top-height-10;
  if(top<12)top=Math.min(rect.bottom+10,Math.max(12,window.innerHeight-height-12));
  tvPreviewEl.style.left=left+'px';tvPreviewEl.style.top=top+'px';
 }
 function hideTvPreview(){tvPreviewEl.hidden=true}
 addEventListener('scroll',hideTvPreview,true);
 addEventListener('resize',hideTvPreview);

 /* ---------- chat list ---------- */
 const fmtTime=value=>{const d=new Date(value),now=new Date(),sameDay=d.toDateString()===now.toDateString();if(sameDay)return d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});const yesterday=new Date(now);yesterday.setDate(now.getDate()-1);if(d.toDateString()===yesterday.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'2-digit',month:'2-digit',year:'numeric'})};
 function summarize(id){
  const box=boxes.get(id);if(!box)return;
  const ms=box.messages||[];
  box.last=ms[ms.length-1];
  box.streaming=ms.some(m=>m.state==='streaming');
  const agent=(box.defaultAgent||'').toLowerCase();
  const last=box.last;
  box.processing=agent!=='shell'&&!box.streaming&&last&&last.direction==='user'&&last.state==='delivered'&&Date.now()-new Date(last.updatedAt||last.createdAt).getTime()<10*60*1000;
  const marker=seen[id]?new Date(seen[id]).getTime():0;
  box.unread=ms.filter(m=>m.direction!=='user'&&new Date(m.createdAt).getTime()>marker).length;
 }
 function previewText(m){
  if(!m)return 'No messages yet';
  if(m.direction==='system')return m.text;
  const who=m.direction==='user'?'You: ':'';
  let text=m.question?m.question.text:m.text;
  if(m.images?.length)text=(text?text+' ':'')+'📷'.repeat(Math.min(3,m.images.length));
  return who+text;
 }
 function renderRows(){
  const filter=filterEl.value.trim().toLowerCase();
  const list=[...boxes.values()].filter(b=>!filter||b.name.toLowerCase().includes(filter));
  // Keep the list stable: activity must not reshuffle rows under the pointer.
  list.sort((a,b)=>a.name.localeCompare(b.name)||a.id.localeCompare(b.id));
  $('#chat-list-empty').hidden=list.length>0;
  for(const box of list){
   let row=rows.get(box.id);
   if(!row){
    row=document.createElement('li');row.dataset.boxId=box.id;
    const chevron=document.createElement('button');chevron.className='row-chevron';chevron.type='button';chevron.textContent='▾';chevron.title='Box actions';
    chevron.onclick=event=>{event.stopPropagation();openRowMenu(box,{left:event.clientX,right:event.clientX,bottom:event.clientY,top:event.clientY})};
    row.oncontextmenu=event=>{event.preventDefault();openRowMenu(box,{left:event.clientX,right:event.clientX,bottom:event.clientY+4,top:event.clientY})};
    const meta=document.createElement('div');meta.className='chat-meta';
    const r1=document.createElement('div');r1.className='row1';const name=document.createElement('span');name.className='name';name.textContent=box.name;const time=document.createElement('time');r1.append(name,time);
    const r2=document.createElement('div');r2.className='row2';const badge=document.createElement('span');badge.className='agent-badge';badge.textContent=box.defaultAgent||'agent';const preview=document.createElement('span');preview.className='preview';const unread=document.createElement('span');unread.className='unread';unread.hidden=true;r2.append(badge,preview,unread);
    meta.append(r1,r2);row.prepend(meta);row.append(chevron);
    row.onclick=()=>{location.hash='box='+box.id;openBox(box.id)};
    rows.set(box.id,row);
   }
   row.classList.toggle('active',box.id===selected);
   const oldAvatar=row.querySelector('.avatar');
   if(oldAvatar&&oldAvatar.dataset.state===box.state){/* keep the stable avatar node */}
   else{const avatar=avatarNode(box,false);if(oldAvatar)oldAvatar.replaceWith(avatar);else row.prepend(avatar)}
   row.querySelector('time').textContent=box.last?fmtTime(box.last.createdAt):'';
   row.querySelector('time').classList.toggle('recent',!!box.unread);
   const preview=row.querySelector('.preview');preview.textContent=box.streaming?'typing…':box.processing?'processing…':previewText(box.last);preview.classList.toggle('streaming',!!box.streaming&&!box.processing);preview.classList.toggle('processing',!!box.processing&&!box.streaming);
   const unread=row.querySelector('.unread');unread.hidden=!box.unread;unread.textContent=box.unread>99?'99+':box.unread;
  }
  for(const [id,row] of rows){if(!boxes.has(id)){row.remove();rows.delete(id)}}
  listEl.replaceChildren(...list.map(b=>rows.get(b.id)));
 }

 /* ---------- messages ---------- */
 const dayLabel=value=>{const d=new Date(value),now=new Date();if(d.toDateString()===now.toDateString())return 'Today';const y=new Date(now);y.setDate(now.getDate()-1);if(d.toDateString()===y.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'numeric',month:'long',year:'numeric'})};
 const stateTicks={queued:'🕐',delivering:'✓',delivered:'✓✓',failed:'⚠ failed',ambiguous:'⚠ maybe failed'};
 function imageURL(message,image){
  const key=message.id+':'+image.id;
  if(imageURLs.has(key))return Promise.resolve(imageURLs.get(key));
  return fetch('/v1/messages/'+encodeURIComponent(message.id)+'/images/'+encodeURIComponent(image.id),{credentials:'same-origin',signal:AbortSignal.timeout(30000)})
   .then(r=>{if(!r.ok)throw Error('image unavailable');return r.blob()}).then(b=>{const url=URL.createObjectURL(b);imageURLs.set(key,url);return url}).catch(()=>null);
 }
 const questionSelections=new Map();// messageId -> Set of picked choices; survives live re-renders
 function questionAnswered(box,message){
  if(answeredQuestions.has(message.id))return true;
  const ms=box.messages||[],index=ms.findIndex(m=>m.id===message.id);
  return index>=0&&ms.slice(index+1).some(m=>m.direction==='user'&&m.text.startsWith('Answer to "'+message.question.text+'":'));
 }
 function questionForm(box,message){
  if(!message.question)return null;
  const form=document.createElement('form');form.className='question';
  const answered=questionAnswered(box,message);
  const choices=document.createElement('div');choices.className='choices';
  const record=()=>questionSelections.set(message.id,new Set([...choices.children].filter(c=>c.classList.contains('on')).map(c=>c.dataset.value)));
  const saved=questionSelections.get(message.id);
  for(const choice of message.question.choices){
   const btn=document.createElement('button');btn.type='button';btn.className='choice';btn.dataset.value=choice;btn.textContent=choice;
   if(saved?.has(choice))btn.classList.add('on');
   btn.disabled=answered;
   btn.onclick=()=>{
    if(btn.disabled)return;
    if(message.question.multiple)btn.classList.toggle('on');
    else{btn.classList.add('on');for(const sib of choices.children)if(sib!==btn)sib.classList.remove('on')}
    record();
   };
   choices.append(btn);
  }
  const send=document.createElement('button');send.type='submit';send.className='send';send.textContent=answered?'Answer sent':'Send selection';send.disabled=answered;
  form.append(choices,send);
  form.onsubmit=async event=>{
   event.preventDefault();
   const selectedChoices=[...choices.children].filter(c=>c.classList.contains('on')).map(c=>c.dataset.value);
   if(!selectedChoices.length){statusEl.textContent='Pick at least one option.';return}
   send.disabled=true;
   try{
    await api(boxPath(selected)+'/messages','POST',{'Idempotency-Key':crypto.randomUUID()},{text:'Answer to "'+message.question.text+'": '+selectedChoices.join(', ')});
    answeredQuestions.add(message.id);questionSelections.delete(message.id);
    send.textContent='Answer sent';choices.querySelectorAll('button').forEach(c=>{c.disabled=true});
    statusEl.textContent='Selection sent.';await refreshMessages();
   }catch(e){statusEl.textContent=e.message;send.disabled=false}
  };
  return form;
 }
 const linkPattern=/https?:\/\/[^\s<>()"'`]+/gi;
 function linkify(text){
  const fragment=document.createDocumentFragment();
  let last=0,match;
  linkPattern.lastIndex=0;
  while((match=linkPattern.exec(text))){
   if(match.index>last)fragment.append(text.slice(last,match.index));
   const link=document.createElement('a');
   link.href=match[0];link.textContent=match[0];link.target='_blank';link.rel='noopener noreferrer';
   fragment.append(link);
   last=match.index+match[0].length;
  }
  if(last<text.length)fragment.append(text.slice(last));
  return fragment;
 }
 function bubble(box,message){
  const row=document.createElement('div');
  if(message.direction==='system'){row.className='msg system';row.append(Object.assign(document.createElement('span'),{className:'text',textContent:message.text}));return row}
  const mine=message.direction==='user';
  row.className='msg '+(mine?'user':'agent')+(message.state==='silent'?' note':'')+(message.state==='streaming'?' streaming':'');
  if(message.state==='silent'){const label=document.createElement('span');label.className='note-label';label.textContent='Note · not sent to the agent';row.append(label)}
  if(message.text.startsWith('Forwarded from ')){const mark=document.createElement('span');mark.className='fwd-mark';const end=message.text.indexOf(':\n');mark.textContent=end>0?message.text.slice(0,end+1):'Forwarded';row.append(mark)}
  const text=document.createElement('span');text.className='text';
  const body=message.text.startsWith('Forwarded from ')&&message.text.indexOf(':\n')>0?message.text.slice(message.text.indexOf(':\n')+2):message.text;
  text.append(linkify(body));
  row.append(text);
  for(const image of message.images||[]){
   imageURL(message,image).then(url=>{if(!url)return;const img=document.createElement('img');img.className='chat-image';img.src=url;img.alt='Image '+image.number+' from '+message.direction;row.insertBefore(img,row.querySelector('.meta'))});
  }
  const form=questionForm(box,message);if(form)row.append(form);
  const meta=document.createElement('span');meta.className='meta';
  meta.append(Object.assign(document.createElement('time'),{textContent:new Date(message.createdAt).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'})}));
  if(mine&&message.state!=='silent'){const ticks=document.createElement('span');ticks.className='ticks'+(message.state==='failed'||message.state==='ambiguous'?' failed':'');ticks.textContent=stateTicks[message.state]||'';meta.append(ticks)}
  row.append(meta);
  const fwd=document.createElement('button');fwd.type='button';fwd.className='fwd';fwd.title='Forward to another box';fwd.textContent='↪';
  fwd.onclick=event=>{event.stopPropagation();openForwardMenu(fwd,message)};
  row.append(fwd);
  return row;
 }
 function scrollMessagesToBottom(){
  messagesEl.scrollTop=messagesEl.scrollHeight;
  requestAnimationFrame(()=>{messagesEl.scrollTop=messagesEl.scrollHeight});
 }
 // A chat opens at its newest message and keeps following output until the
 // reader scrolls away; scrolling back to the bottom resumes following.
 function followMessages(){stickToBottom=true;requestAnimationFrame(scrollMessagesToBottom)}
 messagesEl.addEventListener('scroll',()=>{stickToBottom=messagesEl.scrollHeight-messagesEl.scrollTop-messagesEl.clientHeight<120});
 function renderMessages(box){
  hideTvPreview();
  const follow=stickToBottom;
  messagesEl.replaceChildren();
  let day='';
  for(const message of box.messages||[]){
   const label=dayLabel(message.createdAt);
   if(label!==day){day=label;const sep=document.createElement('div');sep.className='day-sep';sep.textContent=day;messagesEl.append(sep)}
   messagesEl.append(bubble(box,message));
  }
  if(!(box.messages||[]).length){const hint=document.createElement('p');hint.className='day-sep';hint.textContent='No messages yet — say hello to '+box.name;messagesEl.append(hint)}
  if(box.processing&&!box.streaming){
   const t=document.createElement('div');t.className='msg agent processing';
   const dots=document.createElement('span');dots.className='typing-dots';
   for(let i=0;i<3;i++)dots.append(document.createElement('span'));
   const label=document.createElement('span');label.className='typing-label';label.textContent='agent is processing…';
   const tv=document.createElement('button');tv.type='button';tv.className='tv-button';tv.title='Hover to preview the desktop, click for the live view';tv.setAttribute('aria-label','Preview the desktop and open the live view');
   tv.append(tvIcon());
   tv.onmouseenter=()=>showTvPreview(tv,box);
   tv.onmouseleave=hideTvPreview;
   tv.onfocus=()=>showTvPreview(tv,box);
   tv.onblur=hideTvPreview;
   tv.onclick=()=>openLiveView(box);
   t.append(dots,label,tv);messagesEl.append(t);
  }
  if(follow){
   scrollMessagesToBottom();
   // Late layout and image decoding grow the transcript after the first pass.
   for(const image of messagesEl.querySelectorAll('img'))if(!image.complete)image.addEventListener('load',()=>{if(stickToBottom)scrollMessagesToBottom()},{once:true});
   setTimeout(()=>{if(stickToBottom)scrollMessagesToBottom()},150);
  }
 }

 /* ---------- forwarding ---------- */
 function closeForwardMenu(){forwardMenu.hidden=true;forwardMenu.replaceChildren()}
 async function forwardTo(target,message,button){
  button.disabled=true;
  try{
   const body={text:'Forwarded from '+(boxes.get(selected)?.name||'box')+':\n\n'+(message.question?message.question.text:message.text)};
   if(!message.question&&message.images?.length)body.images=message.images.map(i=>({id:i.id,number:i.number}));
   await api(boxPath(target.id)+'/messages','POST',{'Idempotency-Key':crypto.randomUUID()},body);
   statusEl.textContent='Forwarded to '+target.name+'.';
   closeForwardMenu();
   await loadBoxes();
  }catch(e){statusEl.textContent=e.message;button.disabled=false}
 }
 function openForwardMenu(anchor,message){
  closeForwardMenu();
  const others=[...boxes.values()].filter(b=>b.id!==selected).sort((a,b)=>a.name.localeCompare(b.name));
  const title=document.createElement('h3');title.textContent='Forward message to…';forwardMenu.append(title);
  if(!others.length){const p=document.createElement('h3');p.textContent='No other boxes available.';forwardMenu.append(p)}
  for(const target of others){
   const button=document.createElement('button');button.type='button';
   const name=document.createElement('span');name.textContent=target.name+' · '+target.state;if(target.state!=='running')name.textContent+=' (wakes agent on delivery)';
   button.append(avatarNode(target,true),name);
   button.onclick=()=>void forwardTo(target,message,button);
   forwardMenu.append(button);
  }
  forwardMenu.hidden=false;
  const rect=anchor.getBoundingClientRect();
  forwardMenu.style.left=Math.max(8,Math.min(rect.left,innerWidth-forwardMenu.offsetWidth-8))+'px';
  forwardMenu.style.top=Math.max(8,Math.min(rect.bottom+6,innerHeight-forwardMenu.offsetHeight-8))+'px';
 }
 document.addEventListener('click',event=>{if(!forwardMenu.hidden&&!forwardMenu.contains(event.target))closeForwardMenu()});
 addEventListener('keydown',event=>{if(event.key==='Escape')closeForwardMenu()});

 /* ---------- data loading ---------- */
 const doodle=text=>{const el=$('#chat-loading');$('#chat-loading-text').textContent=text||'';el.hidden=!text;};
 async function loadBoxes(){
  const values=await api(owner?'/v1/grid-boxes':'/v1/logical-boxes');
  const current=new Map();const alive=new Set();
  for(const b of values||[]){alive.add(b.id);current.set(b.id,{...b,messages:boxes.get(b.id)?.messages||[]})}
  for(const id of [...boxes.keys()])if(!alive.has(id)){const cached=avatarCache.get(id);if(cached?.url)URL.revokeObjectURL(cached.url);boxes.delete(id);avatarCache.delete(id)}
  for(const [id,b] of current)boxes.set(id,b);
  if(selected&&!boxes.has(selected)){selected='';lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false}
  await loadPreviews();
  if(selected)applySeen(selected);
  renderRows();
 }
 async function loadPreviews(){
  await Promise.allSettled([...boxes.keys()].map(async id=>{
   if(id===selected)return;// open conversation refreshes itself
   const messages=await api(boxPath(id)+'/messages');
   const box=boxes.get(id);if(box)box.messages=messages||[];
   summarize(id);
  }));
 }
 function applySeen(id){
  const box=boxes.get(id);if(!box)return;
  const last=(box.messages||[]).filter(m=>m.direction!=='user').pop();
  if(last&&new Date(last.createdAt).getTime()>(seen[id]?new Date(seen[id]).getTime():0)){seen[id]=last.createdAt;saveSeen()}
  summarize(id);
 }
 let headerAvatarKey='';
 function renderHeader(){
  const box=boxes.get(selected);if(!box)return;
  $('#chat-header-name').textContent=box.name;
  $('#chat-header-state').replaceChildren(Object.assign(document.createElement('span'),{className:box.state==='running'?'running':'',textContent:(box.defaultAgent||'agent')+' · '+box.state+(box.streaming?' · streaming…':box.processing?' · processing…':'')}));
  const key=box.id+'|'+box.state;
  if(key!==headerAvatarKey){headerAvatarKey=key;$('#chat-header-avatar').replaceChildren(avatarNode(box,false))}
  $('#chat-workspace').href='/boxes/'+encodeURIComponent(box.id);
 }
 async function refreshMessages(force){
  if(!selected)return;
  const box=boxes.get(selected);if(!box)return;
  const messages=await api(boxPath(selected)+'/messages');
  box.messages=messages||[];
  const signature=box.messages.map(m=>m.id+m.updatedAt+m.state).join('|');
  renderHeader();
  if(force||signature!==lastSignature){lastSignature=signature;renderMessages(box)}
  applySeen(selected);renderRows();renderInspect();
 }
 async function openBox(id){
  if(!boxes.has(id))return;
  selected=id;lastSignature='';
  followMessages();
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;
  appEl.classList.add('in-chat');
  renderHeader();
  statusEl.textContent='';
  closeForwardMenu();
  closeTakeover();
  renderInspect();
  if(!(boxes.get(id).messages||[]).length)doodle('Loading messages…');
  try{await refreshMessages(true)}catch(e){statusEl.textContent=e.message}finally{doodle('')}
  inputEl.focus();
 }

 /* ---------- composer ---------- */
 function grow(){inputEl.style.height='auto';inputEl.style.height=Math.min(inputEl.scrollHeight,150)+'px'}
 inputEl.addEventListener('input',grow);
 inputEl.addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.shiftKey){event.preventDefault();composer.requestSubmit()}});
 function renderDrafts(){
  draftsEl.hidden=!drafts.length;draftsEl.replaceChildren();
  for(const entry of drafts){
   const wrap=document.createElement('span');wrap.className='draft';
   const img=document.createElement('img');img.src=entry.url;img.alt='Image '+entry.number;
   const remove=document.createElement('button');remove.type='button';remove.textContent='×';remove.title='Remove image';
   remove.onclick=()=>{drafts=drafts.filter(d=>d!==entry);URL.revokeObjectURL(entry.url);drafts.forEach((d,i)=>d.number=i+1);renderDrafts()};
   wrap.append(img,remove);draftsEl.append(wrap);
  }
 }
 async function uploadImages(files){
  for(const file of files){
   if(drafts.length>=8){statusEl.textContent='Attach at most 8 images.';break}
   if(!['image/png','image/jpeg','image/gif'].includes(file.type)){statusEl.textContent='Choose PNG, JPEG, or GIF images.';continue}
   if(file.size>8*1024*1024){statusEl.textContent='Each image must be at most 8 MiB.';continue}
   try{
    const response=await fetch('/v1/run-once-images',{method:'POST',credentials:'same-origin',body:file,signal:AbortSignal.timeout(60000)});
    let result;try{result=await response.json()}catch{}
    if(!response.ok)throw Error(result?.error||'Image upload failed.');
    drafts.push({id:result.id,number:drafts.length+1,url:URL.createObjectURL(file)});renderDrafts();
   }catch(e){statusEl.textContent=e.message}
  }
  fileInput.value='';
 }
 attachBtn.onclick=()=>fileInput.click();
 fileInput.onchange=()=>void uploadImages([...fileInput.files]);
 composer.addEventListener('paste',event=>{
  const files=[...(event.clipboardData?.items||[])].filter(i=>i.kind==='file'&&i.type.startsWith('image/')).map(i=>i.getAsFile()).filter(Boolean);
  if(files.length){event.preventDefault();void uploadImages(files)}
 });
 composer.addEventListener('dragover',event=>{if([...(event.dataTransfer?.types||[])].includes('Files'))event.preventDefault()});
 composer.addEventListener('drop',event=>{const files=[...(event.dataTransfer?.files||[])];if(files.length){event.preventDefault();void uploadImages(files)}});
 composer.onsubmit=async event=>{
  event.preventDefault();
  if(!selected)return;
  const text=inputEl.value,images=drafts.map(({id,number})=>({id,number}));
  if(!text.trim()&&!images.length)return;
  const fingerprint=text+'\n'+images.map(i=>i.id).join(',');
  if(fingerprint!==pendingFingerprint||!pendingKey){pendingKey=crypto.randomUUID();pendingFingerprint=fingerprint}
  const send=$('#send');send.disabled=true;
  try{
   const result=await api(boxPath(selected)+'/messages','POST',{'Idempotency-Key':pendingKey},{text,images});
   inputEl.value='';grow();for(const d of drafts)URL.revokeObjectURL(d.url);drafts=[];renderDrafts();pendingKey='';pendingFingerprint='';
   statusEl.textContent=result?.message?.state==='silent'?'Note saved without waking the agent.':'';
   await refreshMessages(true);
  }catch(e){statusEl.textContent=e.message}
  finally{send.disabled=false}
 };
 $('#chat-back').onclick=()=>{appEl.classList.remove('in-chat');history.replaceState(null,'',location.pathname)};
 addEventListener('hashchange',()=>{const id=new URLSearchParams(location.hash.slice(1)).get('box');if(id&&id!==selected&&boxes.has(id))void openBox(id)});

 /* ---------- takeover popup: VNC/TMUX control ---------- */
 const takeover=$('#takeover'),takeoverScreen=$('#takeover-screen'),takeoverControls=$('#takeover-controls'),takeoverStatus=$('#takeover-status');
 const boxViewerMetrics=new Map();
 let takeoverDispose=null,takeoverKind='';
 async function openTakeover(kind){
  const box=boxes.get(selected);if(!box)return;
  if(box.state!=='running'){statusEl.textContent=box.name+' is '+box.state+'; resume it from the workspace first.';return}
  takeoverDispose?.();takeoverDispose=null;takeoverScreen.replaceChildren();takeoverControls.replaceChildren();
  takeover.hidden=false;takeoverKind=kind;
  $('#takeover-title').textContent=box.name+' · '+(kind==='desktop'?'Desktop':'TMUX');
  takeoverStatus.textContent=kind==='desktop'?'Starting desktop…':'Opening session…';
  takeover.querySelectorAll('#takeover-tabs button').forEach(b=>b.classList.toggle('on',b.dataset.kind===kind));
  try{
   if(kind==='desktop'){
    await api(boxPath(box.id)+'/desktop','POST',{});
    takeoverDispose=openWorkspaceDesktop(box.id,msg=>{takeoverStatus.textContent=msg},{root:takeoverScreen,controls:takeoverControls,onMetrics:m=>{boxViewerMetrics.set(box.id,m);renderInspect()}});
   }else{
    const s=await api(boxPath(box.id)+'/sessions/interactive','POST',{},{agent:box.defaultAgent||'shell',reuseExisting:true});
    takeoverDispose=openWorkspaceTerminal(box.id,s.session,msg=>{takeoverStatus.textContent=msg},{root:takeoverScreen,keys:takeoverControls,autoFocus:true,onDisconnect:()=>{takeoverStatus.textContent+=' · disconnected'}});
   }
  }catch(e){takeoverStatus.textContent=e.message}
 }
 function closeTakeover(){
  takeoverDispose?.();takeoverDispose=null;takeoverKind='';
  takeover.hidden=true;takeoverScreen.replaceChildren();takeoverControls.replaceChildren();
 }
 $('#chat-control').onclick=()=>void openTakeover('desktop');
 $('#takeover-close').onclick=closeTakeover;
 $('#takeover-backdrop').onclick=closeTakeover;
 $('#live-view-close').onclick=closeLiveView;
 $('#live-view-backdrop').onclick=closeLiveView;
 $('#live-view-control').onclick=()=>{closeLiveView();void openTakeover('desktop')};
 takeover.querySelectorAll('#takeover-tabs button').forEach(b=>b.onclick=()=>void openTakeover(b.dataset.kind));

 /* ---------- interrupt agent ---------- */
 $('#chat-interrupt').onclick=async()=>{
  const box=boxes.get(selected);if(!box)return;
  if(box.state!=='running'){statusEl.textContent=box.name+' is '+box.state+'; resume it from the workspace first.';return}
  const btn=$('#chat-interrupt');btn.disabled=true;
  try{
   const s=await api(boxPath(box.id)+'/sessions/interactive','POST',{},{agent:box.defaultAgent||'shell',reuseExisting:true});
   await api(boxPath(box.id)+'/terminal/input?session='+encodeURIComponent(s.session),'POST',{'Idempotency-Key':crypto.randomUUID()},{keys:[(box.defaultAgent||'shell')==='shell'?'C-c':'Escape']});
   toast('Interrupt sent — your queued message comes next.');
  }catch(e){statusEl.textContent=e.message}
  finally{btn.disabled=false}
 };

 /* ---------- inspect drawer: ping / activity per box ---------- */
 const inspect=$('#inspect'),inspectRows=$('#inspect-rows');
 let inspectOpen=false,inspectTimer,controllerPing=null;
 const fmtAgo=value=>{const s=Math.max(0,(Date.now()-new Date(value).getTime())/1000);if(s<60)return Math.round(s)+'s ago';if(s<3600)return Math.round(s/60)+' min ago';if(s<86400)return Math.round(s/3600)+' h ago';return Math.round(s/86400)+' d ago'};
 const lastMessage=(messages,direction)=>[...messages].reverse().find(m=>m.direction===direction);
 function renderInspect(){
  if(!inspectOpen||!selected)return;
  const box=boxes.get(selected);if(!box)return;
  const msgs=box.messages||[],lastAgent=lastMessage(msgs,'agent'),lastUser=lastMessage(msgs,'user');
  const livePing=boxViewerMetrics.get(box.id)?.ping;
  const waiting=!!lastUser&&(!lastAgent||new Date(lastUser.createdAt)>new Date(lastAgent.createdAt));
  const rows=[
   ['State',box.state+(box.streaming?' · agent streaming…':''),box.state==='running'?'ok':'alert'],
   ['Agent',box.defaultAgent||'—'],
   ['Provider',box.provider||'—'],
   ['Controller ping',controllerPing==null?'—':controllerPing+' ms'],
   ['Box ping (live VNC)',livePing!=null?livePing+' ms':'while the desktop popup is open'],
   ['Last agent activity',box.streaming?'streaming now…':lastAgent?fmtAgo(lastAgent.updatedAt||lastAgent.createdAt):'—'],
   ['Waiting for agent',waiting?'since '+fmtAgo(lastUser.createdAt):'no',waiting?'alert':'ok'],
   ['Messages',String(msgs.length)],
  ];
  $('#inspect-title').textContent=box.name;
  $('#inspect-avatar').replaceChildren(avatarNode(box,false));
  inspectRows.replaceChildren();
  for(const [dt,dd,cls] of rows){
   const row=document.createElement('div'),t=document.createElement('dt'),d=document.createElement('dd');
   t.textContent=dt;d.textContent=dd;if(cls)d.className=cls;row.append(t,d);inspectRows.append(row);
  }
  const row=document.createElement('div'),t=document.createElement('dt'),d=document.createElement('dd'),link=document.createElement('a');
  t.textContent='Workspace';link.href='/boxes/'+encodeURIComponent(box.id);link.textContent='Open full workspace';link.target='_blank';link.rel='noopener';d.append(link);row.append(t,d);inspectRows.append(row);
 }
 async function samplePing(){
  if(!inspectOpen)return;
  if(!document.hidden){
   const t=performance.now();
   try{const r=await fetch('/healthz',{credentials:'same-origin',cache:'no-store',signal:AbortSignal.timeout(5000)});await r.text();controllerPing=r.ok?Math.round(performance.now()-t):null}catch{controllerPing=null}
  }
  renderInspect();
 }
 $('#chat-info').onclick=()=>{
  inspectOpen=!inspectOpen;inspect.hidden=!inspectOpen;
  if(inspectOpen){controllerPing=null;void samplePing();inspectTimer=setInterval(()=>void samplePing(),5000)}
  else{clearInterval(inspectTimer);controllerPing=null}
 };
 $('#inspect-close').onclick=()=>{inspectOpen=false;inspect.hidden=true;clearInterval(inspectTimer);controllerPing=null};

 /* ---------- toasts ---------- */
 function toast(text){const el=document.createElement('div');el.className='toast';el.textContent=text;$('#chat-toasts').append(el);setTimeout(()=>{el.style.opacity='0';setTimeout(()=>el.remove(),400)},3200);}

 /* ---------- live view: large read-only desktop stream ---------- */
 const liveView=$('#live-view'),liveViewScreen=$('#live-view-screen'),liveViewControls=$('#live-view-controls'),liveViewStatus=$('#live-view-status');
 let liveViewDispose=null;
 function closeLiveView(){
  liveViewDispose?.();liveViewDispose=null;
  liveView.hidden=true;liveViewScreen.replaceChildren();liveViewControls.replaceChildren();
 }
 function openLiveView(box){
  hideTvPreview();
  if(!liveView.hidden)closeLiveView();
  liveView.hidden=false;
  $('#live-view-title').textContent=box.name+' · live view';
  liveViewStatus.textContent='Connecting…';
  try{
   liveViewDispose=openWorkspaceDesktop(box.id,message=>{liveViewStatus.textContent=message},{root:liveViewScreen,controls:liveViewControls,viewOnly:true,onMetrics:m=>{boxViewerMetrics.set(box.id,m);renderInspect()}});
  }catch(e){liveViewStatus.textContent=e.message}
 }

 /* ---------- new box (full controller feature set: agent, disk, placement defaults, login profiles, tools, setup script) ---------- */
 const newBoxModal=$('#new-box-modal'),createForm=$('#create-box');
 let extrasLoaded=false;
 async function primeBoxExtras(){
  if(extrasLoaded)return;
  try{
   const [tools,profiles,defaults,providers]=await Promise.all([api('/v1/tool-presets'),api('/v1/login-profiles'),api('/v1/controller-defaults'),api('/v1/provider-credentials').catch(()=>[])]);
   const pools=(providers||[]).map(p=>({provider:p.provider,providerCredential:p.name||''}));
   const poolStatuses=await Promise.all(pools.map(async pool=>{
    try{
     const fleet=await api('/v1/fleet/status?'+new URLSearchParams({provider:pool.provider,providerCredential:pool.providerCredential}));
     return {free:Number(fleet.freeSlots)||0,occupied:Number(fleet.occupiedSlots)||0,actual:Number(fleet.actualSlots)||0,desired:Number(fleet.desiredSlots)||0,queued:Number(fleet.pendingAllocationRequests)||0};
    }catch{return null}
   }));
   const poolLabel=(pool,status)=>{
    const base=(pool.provider==='shared-worker'?'Shared worker':'Dedicated · '+pool.provider)+(pool.providerCredential?' / '+pool.providerCredential:'');
    if(!status)return base+' — slot status unavailable';
    if(status.free>0)return base+' — '+status.free+' free of '+status.actual;
    return base+' — no free slots ('+status.occupied+'/'+status.actual+' busy)';
   };
   const poolSelect=$('#create-pool');poolSelect.replaceChildren(new Option('Automatic (least loaded pool with free slots)',''));
   pools.forEach((pool,index)=>{
    const status=poolStatuses[index],option=new Option(poolLabel(pool,status),String(index));
    option.title=status?('desired '+status.desired+' · actual '+status.actual+' · free '+status.free+' · occupied '+status.occupied+(status.queued?' · '+status.queued+' queued':'')):'slot status unavailable';
    option.disabled=!!status&&status.free===0;
    poolSelect.append(option);
   });
   $('#create-pool-label').hidden=pools.length===0;
   const poolHint=$('#create-pool-status');
   poolHint.hidden=pools.length===0;
   if(pools.length)poolHint.textContent=pools.map((pool,index)=>poolLabel(pool,poolStatuses[index]).replace('Dedicated · ','').replace('Shared worker','shared')).join(' · ')+' — boxes wait in the controller queue when their pool has no free slots.';
   createForm.dataset.pools=JSON.stringify(pools.map(p=>({provider:p.provider,providerCredential:p.name||''})));
   if(defaults.provider){createForm.dataset.provider=defaults.provider;createForm.dataset.providerCredential=defaults.providerCredential||''}
   const byApp={};
   for(const p of profiles)(byApp[p.application]??=[]).push(p.name);
   const profilesWrap=$('#profile-choices');profilesWrap.replaceChildren();
   for(const app of Object.keys(byApp).sort()){
    const label=document.createElement('label');label.className='field profile-field';label.textContent=app;
    const select=document.createElement('select');select.name='profile:'+app;
    select.append(new Option('None',''),...byApp[app].sort().map(n=>new Option(n,n)));
    label.append(select);profilesWrap.append(label);
   }
   profilesWrap.hidden=!Object.keys(byApp).length;
   const toolsSet=$('#create-tools');toolsSet.replaceChildren();
   for(const tool of tools){
    const label=document.createElement('label'),input=document.createElement('input');
    input.type='checkbox';input.value=tool.id;input.name='tool';input.title=tool.description||tool.name;
    label.append(input,document.createTextNode(' '+tool.name));toolsSet.append(label);
   }
   toolsSet.hidden=!tools.length;
   extrasLoaded=true;
  }catch(e){$('#new-box-status').textContent=e.message}
 }
 function openNewBoxModal(){
  createForm.reset();$('#new-box-status').textContent='';newBoxModal.hidden=false;
  void primeBoxExtras();
  createForm.elements.name.focus();
 }
 $('#new-box').onclick=openNewBoxModal;
 $('#new-box-close').onclick=()=>{newBoxModal.hidden=true};
 $('#new-box-backdrop').onclick=()=>{newBoxModal.hidden=true};
 createForm.onsubmit=async event=>{
  event.preventDefault();
  const f=createForm.elements,submit=$('#create-box-submit');submit.disabled=true;$('#new-box-status').textContent='Creating…';
  const tools=[...createForm.querySelectorAll('input[name=tool]:checked')].map(i=>i.value);
  const loginProfiles=[...createForm.querySelectorAll('select')].filter(s=>s.name.startsWith('profile:')&&s.value).map(s=>({application:s.name.slice('profile:'.length),name:s.value}));
  const setupScript=(createForm.elements.setupScript?.value||'').trim();
  const body={name:f.name.value.trim(),defaultAgent:f.defaultAgent.value,diskGiB:Number(f.disk.value)||10,provider:createForm.dataset.provider||'',providerCredential:createForm.dataset.providerCredential||'',allocateWhenReady:true};
  const poolIndex=f.pool.value;
  if(poolIndex!==''){const pool=JSON.parse(createForm.dataset.pools||'[]')[Number(poolIndex)];if(pool){body.provider=pool.provider;body.providerCredential=pool.providerCredential||''}}
  if(loginProfiles.length)body.loginProfiles=loginProfiles;
  if(tools.length)body.tools=tools;
  if(setupScript)body.setupScript=setupScript;
  try{
   const created=await api('/v1/logical-boxes','POST',{'Idempotency-Key':crypto.randomUUID()},body);
   newBoxModal.hidden=true;toast('Box '+created.name+' requested — it appears in the list as it starts.');
   await loadBoxes();
   if(created?.id&&boxes.has(created.id)){history.replaceState(null,'',location.pathname+'#box='+created.id);await openBox(created.id);toast('Box '+created.name+' is starting.');}
  }catch(e){$('#new-box-status').textContent=e.message}
  finally{$('#create-box-submit').disabled=false}
 };

 /* ---------- row menu / hibernate / delete ---------- */
 const rowMenu=$('#row-menu');
 function closeRowMenu(){rowMenu.hidden=true;rowMenu.replaceChildren()}
 function openRowMenu(box,rect){
  rowMenu.replaceChildren();
  const items=[
   ['Show details',()=>{if(!inspectOpen)$('#chat-info').click()}],
   ['Control desktop',()=>{location.hash='box='+box.id;if(box.id!==selected)void openBox(box.id).then(()=>openTakeover('desktop'));else openTakeover('desktop')}],
   ['Delete box…',()=>openDeleteModal(box),'danger'],
  ];
  if(box.state==='running')items.splice(2,0,['Hibernate box',()=>void hibernateBox(box)]);
  for(const item of items){const b=document.createElement('button');b.type='button';b.textContent=item[0];if(item[2])b.className='danger';b.onclick=()=>{closeRowMenu();item[1]()};rowMenu.append(b)}
  rowMenu.hidden=false;
  rowMenu.style.left=Math.max(8,Math.min(rect.left,innerWidth-rowMenu.offsetWidth-8))+'px';
  rowMenu.style.top=Math.max(8,Math.min((rect.bottom||rect.top)+4,innerHeight-rowMenu.offsetHeight-8))+'px';
 }
 document.addEventListener('click',event=>{if(!rowMenu.hidden&&!rowMenu.contains(event.target))closeRowMenu()});
 addEventListener('keydown',event=>{if(event.key==='Escape'){closeRowMenu();if(!newBoxModal.hidden)newBoxModal.hidden=true;if(!deleteModal.hidden)deleteModal.hidden=true;if(!liveView.hidden)closeLiveView();if(!takeover.hidden)closeTakeover();}});
 async function hibernateBox(box){
  try{
   await api(boxPath(box.id)+'/hibernate','POST',{'Idempotency-Key':crypto.randomUUID()},{});
   toast(box.name+' is hibernating.');
   await loadBoxes();
  }catch(e){toast(e.message)}
 }
 const deleteModal=$('#delete-box-modal'),deleteForm=$('#delete-box-form');let deleteTarget=null;
 function openDeleteModal(box){
  $('#delete-box-text').textContent='Deleting "'+box.name+'" permanently removes the box and its entire workspace volume. Hibernate keeps the volume instead.';
  deleteForm.elements.confirmation.value='';$('#delete-box-status').textContent='';$('#delete-box-submit').disabled=true;deleteTarget=box;deleteModal.hidden=false;deleteForm.elements.confirmation.focus();
 }
 deleteForm.addEventListener('input',()=>{$('#delete-box-submit').disabled=!deleteTarget||deleteForm.elements.confirmation.value!==deleteTarget.name});
 deleteForm.onsubmit=async event=>{
  event.preventDefault();
  const submit=$('#delete-box-submit');submit.disabled=true;
  try{
   await api(boxPath(deleteTarget.id)+'/volume','DELETE',{'Idempotency-Key':crypto.randomUUID()},{confirmation:deleteForm.elements.confirmation.value});
   deleteModal.hidden=true;toast('Box "'+deleteTarget.name+'" deleted.');
   if(deleteTarget.id===selected){selected='';lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;history.replaceState(null,'',location.pathname);closeTakeover()}
   deleteTarget=null;
   await loadBoxes();
  }catch(e){$('#delete-box-status').textContent=e.message}
  finally{$('#delete-box-submit').disabled=false}
 };
 $('#delete-box-close').onclick=()=>{deleteModal.hidden=true};
 $('#delete-box-backdrop').onclick=()=>{deleteModal.hidden=true};

 /* ---------- web push ---------- */
 const pushSupported='serviceWorker'in navigator&&'PushManager'in window&&'Notification'in window;
 let swRegistration=null;
 const urlB64ToBytes=value=>{const padding='='.repeat((4-value.length%4)%4);const raw=atob(value.replace(/-/g,'+').replace(/_/g,'/')+padding);return Uint8Array.from([...raw].map(c=>c.charCodeAt(0)))};
 function renderPushState(){
  if(!pushSupported){pushBtn.hidden=true;return}
  pushBtn.hidden=false;
  if(Notification.permission==='denied'){pushBtn.textContent='Notifications blocked';pushBtn.className='';pushBtn.disabled=true;return}
  pushBtn.disabled=false;
  const on=localStorage.getItem('vmboxChatPush')==='on';
  pushBtn.textContent=on?'Notifications on':'Enable notifications';
  pushBtn.classList.toggle('on',on);
 }
 async function syncPushSubscription(){
  if(!pushSupported||Notification.permission!=='granted'||localStorage.getItem('vmboxChatPush')!=='on')return;
  try{
   swRegistration=swRegistration||await navigator.serviceWorker.register('/push-sw.js');
   const {publicKey}=await api('/v1/push/vapid-key');
   let sub=await swRegistration.pushManager.getSubscription();
   if(!sub)sub=await swRegistration.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:urlB64ToBytes(publicKey)});
   await api('/v1/push/subscriptions','PUT',{},{endpoint:sub.endpoint,keys:{p256dh:btoa(String.fromCharCode(...new Uint8Array(sub.getKey('p256dh')))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,''),auth:btoa(String.fromCharCode(...new Uint8Array(sub.getKey('auth')))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')},userAgent:navigator.userAgent.slice(0,200)});
   localStorage.setItem('vmboxChatPush','on');renderPushState();
  }catch(e){$('#error').textContent=e.message}
 }
 pushBtn.onclick=async()=>{
  pushBtn.disabled=true;
  try{
   if(localStorage.getItem('vmboxChatPush')==='on'){
    swRegistration=swRegistration||await navigator.serviceWorker.register('/push-sw.js');
    const sub=await swRegistration.pushManager.getSubscription();
    if(sub){try{await api('/v1/push/subscriptions','DELETE',{},{endpoint:sub.endpoint})}catch{}await sub.unsubscribe()}
    localStorage.setItem('vmboxChatPush','off');renderPushState();return;
   }
   const permission=await Notification.requestPermission();
   if(permission!=='granted'){renderPushState();return}
   localStorage.setItem('vmboxChatPush','on');
   await syncPushSubscription();
  }catch(e){$('#error').textContent=e.message}
  finally{pushBtn.disabled=false;renderPushState()}
 };
 navigator.serviceWorker?.addEventListener('message',event=>{
  if(event.data?.type==='vmbox-push'){void refreshMessages();void loadBoxes()}
  if(event.data?.type==='vmbox-open'&&event.data.url){const url=new URL(event.data.url,location.origin);if(url.hash!==location.hash)location.hash=url.hash}
 });

 /* ---------- polling ---------- */
 function schedule(){
  clearTimeout(boxTimer);clearTimeout(msgTimer);
  boxTimer=setTimeout(tickBoxes,15000);
  msgTimer=setTimeout(tickMessages,3000);
 }
 async function tickBoxes(){try{if(!document.hidden)await loadBoxes()}catch{}boxTimer=setTimeout(tickBoxes,15000)}
 async function tickMessages(){try{if(!document.hidden&&selected)await refreshMessages()}catch{}msgTimer=setTimeout(tickMessages,3000)}
 document.addEventListener('visibilitychange',()=>{if(!document.hidden){void tickBoxes();void tickMessages()}});
 filterEl.addEventListener('input',renderRows);
 $('#refresh').onclick=async()=>{try{await loadBoxes();if(selected)await refreshMessages(true);$('#error').textContent=''}catch(e){$('#error').textContent=e.message}};

 /* ---------- auth ---------- */
 $('#login').onsubmit=async event=>{
  event.preventDefault();
  try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+event.target.elements.token.value});event.target.reset();await enter()}catch(e){$('#error').textContent=e.message}
 };
 $('#logout').onclick=async()=>{
  clearTimeout(boxTimer);clearTimeout(msgTimer);
  closeTakeover();
  inspectOpen=false;inspect.hidden=true;clearInterval(inspectTimer);controllerPing=null;
  try{await api('/v1/browser-session','DELETE')}catch{}
  for(const url of imageURLs.values())URL.revokeObjectURL(url);imageURLs.clear();
  for(const cached of avatarCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  for(const cached of tvShotCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  avatarCache.clear();headerAvatarKey='';
  for(const d of drafts)URL.revokeObjectURL(d.url);drafts=[];renderDrafts();pendingKey='';pendingFingerprint='';
  boxes.clear();rows.clear();listEl.replaceChildren();messagesEl.replaceChildren();
  selected='';lastSignature='';appEl.classList.remove('in-chat');
  $('#chat-app').hidden=true;$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;$('#logout').hidden=true;$('#login').hidden=false;
 };
 async function enter(){
  try{
   const who=await api('/v1/whoami');owner=who.role==='owner';
   $('#login').hidden=true;$('#logout').hidden=false;appEl.hidden=false;
   doodle('Loading chats…');
   try{await loadBoxes()}finally{doodle('')}
   const id=new URLSearchParams(location.hash.slice(1)).get('box');
   if(id&&boxes.has(id))await openBox(id);
   schedule();renderPushState();void syncPushSubscription();
  }catch(e){$('#error').textContent=e.message;$('#login').hidden=false}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);for(const url of imageURLs.values())URL.revokeObjectURL(url)});
 void enter();
})();
