'use strict';
(()=>{
 const $=s=>document.querySelector(s);
 const listEl=$('#chat-entries'),messagesEl=$('#chat-messages'),appEl=$('#chat-app'),statusEl=$('#chat-status'),inputEl=$('#chat-input'),composer=$('#chat-composer'),attachBtn=$('#attach'),fileInput=$('#attachments'),draftsEl=$('#chat-image-drafts'),forwardMenu=$('#forward-menu'),filterEl=$('#chat-filter'),pushBtn=$('#push-toggle');
 const boxes=new Map(),rows=new Map(),imageURLs=new Map(),answeredQuestions=new Set();
 let selected='',owner=false,boxTimer,msgTimer,lastSignature='',stickToBottom=true;
 let drafts=[],pendingKey='',pendingFingerprint='';
 let instructionPresets={defaultName:'',presets:[]};
 const presetBodyCache=new Map();
 let boxInstructionTarget=null,boxCredentialTarget=null,createInstructionSource='';
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
   tv.onclick=()=>void openLiveView(box);
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
    await ensureDesktopRunning(box);
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
  // Config the box keeps in sync, editable from the same place it is reported.
  const actions=document.createElement('div'),at=document.createElement('dt'),ad=document.createElement('dd');
  at.textContent='Config';ad.className='inspect-actions';
  const act=(label,title,fn)=>{const b=document.createElement('button');b.type='button';b.textContent=label;b.title=title;b.onclick=fn;ad.append(b)};
  act('Instructions…','Edit the Markdown instructions synced into this box',()=>void openBoxInstructions(box));
  if(owner)act('Credentials…','Replace the login profiles imported into this box',()=>void openBoxCredentials(box));
  if(box.state==='running')act('Re-sync','Re-push the saved config to the running box',()=>void resyncBox(box));
  if(box.state==='running')act('Restart…','Hibernate and start again; running sessions end',()=>void restartBox(box));
  actions.append(at,ad);inspectRows.append(actions);
  maybeLoadInspectContacts(box);
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
  else{clearInterval(inspectTimer);controllerPing=null;inspectContactsFor=''}
 };
 $('#inspect-close').onclick=()=>{inspectOpen=false;inspect.hidden=true;clearInterval(inspectTimer);controllerPing=null;inspectContactsFor=''};

 /* ---------- inspect drawer: per-box contact graph (owner) ---------- */
 const inspectContacts=$('#inspect-contacts');
 let inspectContactsFor='',inspectProtected=false;
 async function loadInspectContacts(box){
  const status=$('#inspect-contact-status'),list=$('#inspect-contact-list');
  status.textContent='Loading…';
  try{
   const [contacts,protection]=await Promise.all([api(boxPath(box.id)+'/contacts'),api(boxPath(box.id)+'/protection')]);
   if(!inspectOpen||selected!==box.id)return;
   inspectProtected=!!protection.protected;
   const role=box.role==='manager'?'manager':'worker';
   $('#inspect-contact-role').textContent=role;
   $('#inspect-toggle-role').textContent=role==='manager'?'Make worker':'Make manager';
   $('#inspect-protection-label').textContent=inspectProtected?'Protected — managers cannot see or message this box':'Not protected';
   $('#inspect-toggle-protection').textContent=inspectProtected?'Remove protection':'Protect box';
   list.replaceChildren();
   if(!contacts.length){const empty=document.createElement('li');empty.textContent='No explicit contacts.';list.append(empty)}
   for(const contact of contacts){
    const item=document.createElement('li');
    item.textContent=contact.contactName+' · '+(contact.contactRole||'worker')+' · '+(contact.contactState||'unknown');
    const remove=document.createElement('button');remove.type='button';remove.className='linkbtn';remove.textContent='Remove';
    remove.onclick=async()=>{remove.disabled=true;try{await api(boxPath(box.id)+'/contacts/'+encodeURIComponent(contact.contactName),'DELETE');await loadInspectContacts(box)}catch(e){status.textContent=e.message;remove.disabled=false}};
    item.append(document.createTextNode(' '),remove);list.append(item);
   }
   status.textContent=role==='manager'?'A manager may message every non-protected box even without explicit contacts.':'A worker may message only the explicit contacts listed above.';
  }catch(e){status.textContent=e.message}
 }
 function maybeLoadInspectContacts(box){
  if(!owner){inspectContacts.hidden=true;inspectContactsFor='';return}
  inspectContacts.hidden=false;
  if(inspectContactsFor===box.id)return;
  inspectContactsFor=box.id;void loadInspectContacts(box);
 }
 $('#inspect-contact-form').onsubmit=async event=>{event.preventDefault();const box=boxes.get(selected);if(!box)return;const button=event.target.querySelector('button');button.disabled=true;try{await api(boxPath(box.id)+'/contacts','PUT',{}, {contact:event.target.elements.contact.value.trim()});event.target.reset();await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}finally{button.disabled=false}};
 $('#inspect-toggle-role').onclick=async()=>{const box=boxes.get(selected);if(!box)return;const next=box.role==='manager'?'worker':'manager';try{await api(boxPath(box.id),'PATCH',{}, {defaultAgent:box.defaultAgent||'shell',role:next});box.role=next;await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};
 $('#inspect-toggle-protection').onclick=async()=>{const box=boxes.get(selected);if(!box)return;try{await api(boxPath(box.id)+'/protection','PUT',{}, {protected:!inspectProtected});await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};

 /* ---------- toasts ---------- */
 function toast(text){const el=document.createElement('div');el.className='toast';el.textContent=text;$('#chat-toasts').append(el);setTimeout(()=>{el.style.opacity='0';setTimeout(()=>el.remove(),400)},3200);}

 /* ---------- live view: large read-only desktop stream ---------- */
 const liveView=$('#live-view'),liveViewScreen=$('#live-view-screen'),liveViewControls=$('#live-view-controls'),liveViewStatus=$('#live-view-status');
 let liveViewDispose=null,liveViewEpoch=0;
 function closeLiveView(){
  liveViewEpoch++;
  liveViewDispose?.();liveViewDispose=null;
  liveView.hidden=true;liveViewScreen.replaceChildren();liveViewControls.replaceChildren();
 }
 // The desktop stream attaches to a running desktop and never starts one, so a
 // reload (or any box whose desktop is not up yet) has to start it first. A
 // worker without the desktop packages needs an explicit enable before start,
 // which is the same ladder the workspace view uses.
 async function ensureDesktopRunning(box){
  try{
   await api(boxPath(box.id)+'/desktop','POST',{});
  }catch(e){
   if(!/components unavailable|enable the desktop/i.test(e.message||''))throw e;
   await api(boxPath(box.id)+'/desktop/enable','POST',{},{},190000);
   await api(boxPath(box.id)+'/desktop','POST',{});
  }
 }
 async function openLiveView(box){
  hideTvPreview();
  if(!liveView.hidden)closeLiveView();
  const epoch=++liveViewEpoch;
  liveView.hidden=false;
  $('#live-view-title').textContent=box.name+' · live view';
  liveViewStatus.textContent='Starting desktop…';
  try{
   await ensureDesktopRunning(box);
   if(epoch!==liveViewEpoch)return;
   liveViewStatus.textContent='Connecting…';
   liveViewDispose=openWorkspaceDesktop(box.id,message=>{liveViewStatus.textContent=message},{root:liveViewScreen,controls:liveViewControls,viewOnly:true,onMetrics:m=>{boxViewerMetrics.set(box.id,m);renderInspect()}});
  }catch(e){if(epoch===liveViewEpoch)liveViewStatus.textContent=e.message}
 }

 /* ---------- new box (full controller feature set: agent, disk, placement defaults, login profiles, tools, setup script) ---------- */
 const newBoxModal=$('#new-box-modal'),createForm=$('#create-box');
 let extrasLoaded=false;
 async function primeBoxExtras(){
  if(extrasLoaded)return;
  try{
   const [tools,profiles,defaults,providers,presetList]=await Promise.all([api('/v1/tool-presets'),api('/v1/login-profiles'),api('/v1/controller-defaults'),api('/v1/provider-credentials').catch(()=>[]),api('/v1/instruction-presets').catch(()=>({defaultName:'',presets:[]}))]);
   applyInstructionPresets(presetList);
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
   let autoIndex='';
   poolStatuses.forEach((status,index)=>{
    if(!status||status.free<1)return;
    if(autoIndex===''||status.free>Number(poolStatuses[Number(autoIndex)]?.free||0))autoIndex=String(index);
   });
   createForm.dataset.autoPool=autoIndex;
   const poolSelect=$('#create-pool');
   poolSelect.replaceChildren(new Option(autoIndex===''?'Automatic (every pool is busy right now)':'Automatic (least busy pool with free slots)',''));
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
   createForm.dataset.pools=JSON.stringify(pools);
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
  const poolIndex=(f.pool.value||createForm.dataset.autoPool||'');
  if(poolIndex!==''){const pool=JSON.parse(createForm.dataset.pools||'[]')[Number(poolIndex)];if(pool){body.provider=pool.provider;body.providerCredential=pool.providerCredential||''}}
  if(loginProfiles.length)body.loginProfiles=loginProfiles;
  if(tools.length)body.tools=tools;
  if(setupScript)body.setupScript=setupScript;
  const instructions=await createInstructionSelection();
  if(instructions)body.instructions=instructions;
  try{
   const created=await api('/v1/logical-boxes','POST',{'Idempotency-Key':crypto.randomUUID()},body);
   newBoxModal.hidden=true;toast('Box '+created.name+' requested — it appears in the list as it starts.');
   await loadBoxes();
   if(created?.id&&boxes.has(created.id)){history.replaceState(null,'',location.pathname+'#box='+created.id);await openBox(created.id);toast('Box '+created.name+' is starting.');}
  }catch(e){
   let message=e.message||'Could not create the box.';
   if(message.includes('no healthy free compute slot')){
    message='No free compute slot in that pool.'+(createForm.dataset.autoPool?' Try Automatic, which picks a pool showing free slots.':' Every configured slot is busy — hibernate a box or add compute slots, then retry.')+' ('+message+')';
   }
   $('#new-box-status').textContent=message;
  }
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
   ['Instructions…',()=>void openBoxInstructions(box)],
  ];
  if(owner)items.push(['Imported profiles…',()=>void openBoxCredentials(box)]);
  if(box.state==='running')items.push(['Re-sync config',()=>void resyncBox(box)],['Restart box…',()=>void restartBox(box)]);
  items.push(['Delete box…',()=>openDeleteModal(box),'danger']);
  if(box.state==='running')items.splice(2,0,['Hibernate box',()=>void hibernateBox(box)]);
  for(const item of items){const b=document.createElement('button');b.type='button';b.textContent=item[0];if(item[2])b.className='danger';b.onclick=()=>{closeRowMenu();item[1]()};rowMenu.append(b)}
  rowMenu.hidden=false;
  rowMenu.style.left=Math.max(8,Math.min(rect.left,innerWidth-rowMenu.offsetWidth-8))+'px';
  rowMenu.style.top=Math.max(8,Math.min((rect.bottom||rect.top)+4,innerHeight-rowMenu.offsetHeight-8))+'px';
 }
 document.addEventListener('click',event=>{if(!rowMenu.hidden&&!rowMenu.contains(event.target))closeRowMenu()});
 addEventListener('keydown',event=>{if(event.key==='Escape'){closeRowMenu();if(!newBoxModal.hidden)newBoxModal.hidden=true;if(!deleteModal.hidden)deleteModal.hidden=true;if(!liveView.hidden)closeLiveView();if(!takeover.hidden)closeTakeover();}});
 // Re-push the instructions the box already carries. A replaced worker or a
 // restored hibernation can leave a running box behind its saved config, and
 // re-typing the same Markdown just to trigger a write is a poor way to fix it.
 async function resyncBox(box){
  try{
   const result=await api(boxPath(box.id)+'/instructions/resync','POST',{'Idempotency-Key':crypto.randomUUID()},{},120000);
   toast(result?.note||'Config re-synced to '+box.name+'.');
   await loadBoxes();
  }catch(e){toast(e.message)}
 }
 // Hibernate then allocate again. Agents and tmux sessions do not survive this,
 // so it asks first; the workspace volume is kept either way.
 async function restartBox(box){
  if(!confirm('Restart "'+box.name+'"? It hibernates and starts again, so running agents and terminal sessions end. The workspace volume is kept, and the box picks up its current instructions and credentials on the way back up.'))return;
  try{
   toast('Restarting '+box.name+'…');
   await api(boxPath(box.id)+'/hibernate','POST',{'Idempotency-Key':crypto.randomUUID()},{});
   const deadline=Date.now()+180000;
   for(;;){
    await new Promise(r=>setTimeout(r,3000));
    const list=await api('/v1/logical-boxes');
    const current=list.find(b=>b.id===box.id);
    if(!current)throw Error('Box disappeared while restarting.');
    if(current.state==='hibernated'||current.state==='stopped'||current.state==='failed')break;
    if(Date.now()>deadline)throw Error('Still '+current.state+' after 3 minutes; start it again from the box menu once it settles.');
   }
   await api(boxPath(box.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'chat'});
   toast(box.name+' is starting again.');
   await loadBoxes();
  }catch(e){toast(e.message)}
 }
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
   $('#presets-toggle').hidden=!owner;
   $('#login').hidden=true;$('#logout').hidden=false;appEl.hidden=false;
   doodle('Loading chats…');
   try{await loadBoxes()}finally{doodle('')}
   const id=new URLSearchParams(location.hash.slice(1)).get('box');
   if(id&&boxes.has(id))await openBox(id);
   schedule();renderPushState();void syncPushSubscription();
  }catch(e){$('#error').textContent=e.message;$('#login').hidden=false}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);for(const url of imageURLs.values())URL.revokeObjectURL(url)});

 /* ---------- instruction presets, box instructions, imported profiles ---------- */
 function mk(tag,text){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el}
 function mdPreview(root,text){root.replaceChildren();root.append(typeof window.markdownToNodes==='function'?window.markdownToNodes(text||''):mk('pre',text||''))}
 document.querySelectorAll('[data-close]').forEach(el=>el.addEventListener('click',()=>{const sheet=el.closest('.sheet');if(sheet)sheet.hidden=true}));
 async function presetBody(name){
  if(presetBodyCache.has(name))return presetBodyCache.get(name);
  const value=await api('/v1/instruction-presets/'+encodeURIComponent(name));
  presetBodyCache.set(name,value.preset.markdown);
  return value.preset.markdown;
 }
 function applyInstructionPresets(list){
  instructionPresets=list&&Array.isArray(list.presets)?list:{defaultName:'',presets:[]};
  presetBodyCache.clear();renderCreateInstructionChoice();renderPresetList();
 }
 function renderPresetList(){
  const root=$('#preset-list');if(!root)return;root.replaceChildren();
  if(!instructionPresets.presets.length){root.append(mk('p','No presets yet. Add one below.'));return}
  const list=mk('ul');list.className='preset-list';
  for(const preset of instructionPresets.presets){
   const item=mk('li');item.append(mk('span',preset.name+' · r'+preset.revision+(preset.default?' · default':'')));
   const actions=mk('span');actions.className='preset-actions';
   const edit=mk('button','Edit');edit.type='button';edit.onclick=()=>void editPreset(preset.name);
   const setDefault=mk('button',preset.default?'Clear default':'Set default');setDefault.type='button';setDefault.onclick=()=>void setDefaultPreset(preset.default?'':preset.name);
   const remove=mk('button','Delete');remove.type='button';remove.onclick=()=>void deletePreset(preset);
   actions.append(edit,setDefault,remove);item.append(actions);list.append(item);
  }
  root.append(list);
 }
 async function openPresetsModal(){
  const status=$('#preset-status');status.textContent='';
  $('#presets-modal').hidden=false;
  try{applyInstructionPresets(await api('/v1/instruction-presets'))}catch(e){status.textContent=e.message}
 }
 $('#presets-toggle').onclick=()=>void openPresetsModal();
 async function editPreset(name){
  const status=$('#preset-status');
  try{const value=await api('/v1/instruction-presets/'+encodeURIComponent(name));const form=$('#preset-form');form.elements.name.value=value.preset.name;form.elements.markdown.value=value.preset.markdown;status.textContent='Editing '+name+' (r'+value.preset.revision+'). Saving updates future selections only; existing boxes keep their snapshot.'}
  catch(e){status.textContent=e.message}
 }
 async function setDefaultPreset(name){
  const status=$('#preset-status');
  try{await api('/v1/instruction-presets-default','PUT',{name});status.textContent=name?'Account default preset: '+name+'.':'Account default preset cleared.';await openPresetsModal()}
  catch(e){status.textContent=e.message}
 }
 async function deletePreset(preset){
  if(!confirm('Delete instruction preset "'+preset.name+'"? Boxes that copied it keep their snapshot.'))return;
  const status=$('#preset-status');
  try{await api('/v1/instruction-presets/'+encodeURIComponent(preset.name),'DELETE');status.textContent='Preset deleted. Existing boxes keep their snapshot.';await openPresetsModal()}
  catch(e){status.textContent=e.message}
 }
 $('#preset-upload').onclick=()=>$('#preset-file').click();
 $('#preset-file').addEventListener('change',async()=>{
  const input=$('#preset-file'),file=input.files&&input.files[0];if(!file)return;
  const status=$('#preset-status'),lower=file.name.toLowerCase(),typeOk=lower.endsWith('.md')||lower.endsWith('.markdown')||['text/markdown','text/plain'].includes(file.type);
  try{
   if(!typeOk)throw Error('Choose a Markdown file (.md or .markdown).');
   if(file.size>65536)throw Error('Markdown files are limited to 64 KiB.');
   let text;try{text=new TextDecoder('utf-8',{fatal:true}).decode(new Uint8Array(await file.arrayBuffer()))}catch{throw Error('The file must be valid UTF-8 text.')}
   if(text.includes('\u0000'))throw Error('The file must not contain NUL bytes.');
   const form=$('#preset-form');form.elements.markdown.value=text;if(!form.elements.name.value)form.elements.name.value=file.name.replace(/\.(md|markdown)$/i,'').slice(0,64);
   status.textContent='Loaded '+file.name+'. Name the preset and save.';
  }catch(e){status.textContent=e.message}
  input.value='';
 });
 $('#preset-preview-toggle').onclick=()=>{const preview=$('#preset-preview'),text=$('#preset-form').elements.markdown.value;if(preview.hidden){mdPreview(preview,text)}preview.hidden=!preview.hidden};
 $('#preset-form').onsubmit=async event=>{
  event.preventDefault();
  const f=event.target.elements,name=f.name.value.trim(),markdown=f.markdown.value,status=$('#preset-status');
  try{
   if(!/^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$/.test(name))throw Error('Preset names use 1–64 letters, digits, dots, underscores or hyphens and start with a letter or digit.');
   if(markdown.length>65536)throw Error('Markdown is limited to 64 KiB.');
   if(!markdown.trim())throw Error('Enter the Markdown instructions to save.');
   const saved=await api('/v1/instruction-presets/'+encodeURIComponent(name),'PUT',{markdown});
   status.textContent='Saved '+name+' · r'+saved.preset.revision+'.';
   event.target.reset();$('#preset-preview').hidden=true;await openPresetsModal();
  }catch(e){status.textContent=e.message}
 };
 function renderCreateInstructionChoice(){
  const select=$('#create-instructions'),previous=select.value;select.replaceChildren();
  const auto=mk('option',instructionPresets.defaultName?'Account default preset ('+instructionPresets.defaultName+')':'Account default preset / none');auto.value='auto';select.append(auto);
  const none=mk('option','None (no managed instructions)');none.value='none';select.append(none);
  for(const preset of instructionPresets.presets){const option=mk('option',preset.name+' · r'+preset.revision+(preset.default?' · default':''));option.value=preset.name;select.append(option)}
  const custom=mk('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
  if([...select.options].some(option=>option.value===previous))select.value=previous;
  void syncCreateInstructionText();
 }
 async function syncCreateInstructionText(){
  const select=$('#create-instructions'),textarea=$('#create-instructions-custom'),meta=$('#create-instructions-meta'),preview=$('#create-instructions-preview');
  const value=select.value;
  $('#create-instructions-label').textContent=select.options[select.selectedIndex]?.textContent||'none';
  if(value==='auto'||value==='none'){
   textarea.value='';textarea.readOnly=true;createInstructionSource=value;
   meta.textContent=value==='auto'?'The account-default preset is copied at creation; with no default the box gets no managed instructions.':'No managed instructions are written for this box.';
   mdPreview(preview,'');preview.hidden=true;return;
  }
  if(value==='custom'){
   textarea.readOnly=false;
   if(createInstructionSource!=='custom'){textarea.value='';createInstructionSource='custom'}
   meta.textContent='Write the box-specific Markdown. It is snapshotted as a custom instruction set.';
   mdPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();return;
  }
  let body='';try{body=await presetBody(value)}catch{body=''}
  textarea.readOnly=false;
  if(createInstructionSource!==value){textarea.value=body;createInstructionSource=value}
  const preset=instructionPresets.presets.find(item=>item.name===value),dirty=textarea.value!==body;
  meta.textContent='Preset '+(preset?'r'+preset.revision+' · ':'')+(dirty?'edited for this box (preset provenance kept)':'copied verbatim')+'.';
  mdPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();
 }
 $('#create-instructions').addEventListener('change',()=>void syncCreateInstructionText());
 $('#create-instructions-custom').addEventListener('input',()=>{const preview=$('#create-instructions-preview'),text=$('#create-instructions-custom').value;mdPreview(preview,text);preview.hidden=!text.trim()});
 async function createInstructionSelection(){
  const value=$('#create-instructions').value,markdown=$('#create-instructions-custom').value;
  if(value==='auto')return null;
  if(value==='none')return {none:true};
  if(value==='custom'){if(!markdown.trim())throw Error('Enter the custom instruction Markdown or choose another source.');return {markdown}}
  const body=await presetBody(value);
  return markdown.trim()&&markdown!==body?{preset:value,markdown}:{preset:value};
 }
 function describeBoxInstructions(state){
  const current=state.instructions||{},parts=['source: '+current.source];
  if(current.preset)parts.push('preset '+current.preset+' r'+current.presetRevision+(current.modified?' (edited for this box)':''));
  if(state.preset&&!state.preset.exists)parts.push('preset deleted; snapshot retained');
  else if(state.preset&&state.preset.stale)parts.push('preset has a newer revision');
  parts.push(state.pending?'pending apply':'applied');
  return parts.join(' · ');
 }
 async function openBoxInstructions(box){
  boxInstructionTarget=box;
  const status=$('#box-instructions-status');status.textContent='Loading…';
  $('#box-instructions-title').textContent='Instructions · '+box.name;
  const select=$('#box-instructions-preset');select.replaceChildren();
  try{
   const state=await api(boxPath(box.id)+'/instructions'),current=state.instructions||{source:'none',markdown:''};
   const none=mk('option','None (clear managed instructions)');none.value='';select.append(none);
   for(const preset of instructionPresets.presets){const option=mk('option',preset.name+' · r'+preset.revision);option.value=preset.name;select.append(option)}
   const custom=mk('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
   select.value=current.source==='preset'&&instructionPresets.presets.some(p=>p.name===current.preset)?current.preset:(current.source==='custom'?'custom':'');
   $('#box-instructions-markdown').value=current.markdown||'';
   mdPreview($('#box-instructions-preview'),current.markdown||'');
   $('#box-instructions-current').textContent=describeBoxInstructions(state);
   status.textContent='';$('#box-instructions-modal').hidden=false;
  }catch(e){status.textContent=e.message}
 }
 $('#box-instructions-preset').addEventListener('change',async()=>{
  const select=$('#box-instructions-preset'),textarea=$('#box-instructions-markdown'),preview=$('#box-instructions-preview');
  if(select.value===''){textarea.value='';mdPreview(preview,'');return}
  if(select.value==='custom'){mdPreview(preview,textarea.value);return}
  try{textarea.value=await presetBody(select.value)}catch(e){$('#box-instructions-status').textContent=e.message;return}
  mdPreview(preview,textarea.value);
 });
 $('#box-instructions-markdown').addEventListener('input',()=>mdPreview($('#box-instructions-preview'),$('#box-instructions-markdown').value));
 $('#box-instructions-apply').onclick=async()=>{
  if(!boxInstructionTarget)return;
  const status=$('#box-instructions-status'),select=$('#box-instructions-preset'),markdown=$('#box-instructions-markdown').value;
  try{
   let body;
   if(select.value==='')body={none:true};
   else if(select.value==='custom'){if(!markdown.trim())throw Error('Enter the custom Markdown or choose another source.');body={markdown}}
   else{const preset=await presetBody(select.value);body=markdown.trim()&&markdown!==preset?{preset:select.value,markdown}:{preset:select.value}}
   status.textContent='Applying…';
   const result=await api(boxPath(boxInstructionTarget.id)+'/instructions','PUT',body);
   status.textContent=result.note||describeBoxInstructions(result);
   if(result.instructions)$('#box-instructions-current').textContent=describeBoxInstructions(result);
   toast('Instructions applied to '+boxInstructionTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };
 async function openBoxCredentials(box){
  boxCredentialTarget=box;
  const status=$('#box-credentials-status');status.textContent='Loading…';
  $('#box-credentials-title').textContent='Imported profiles · '+box.name;
  try{
   const [state,profiles]=await Promise.all([api(boxPath(box.id)+'/imported-credentials'),api('/v1/login-profiles')]);
   const byApplication={};for(const profile of profiles)(byApplication[profile.application]??=[]).push(profile.name);
   const current=new Map((state.profiles||[]).map(ref=>[ref.application,ref.name]));
   const wrap=$('#box-credentials-form');wrap.replaceChildren();
   for(const application of ['claude','codex','opencode','github']){
    const label=mk('label',application+' ');label.className='field';
    const select=document.createElement('select');select.name=application;
    const empty=mk('option','None');empty.value='';select.append(empty);
    for(const name of (byApplication[application]||[]).slice().sort()){const option=mk('option',name);option.value=name;select.append(option)}
    if(current.has(application)&&(byApplication[application]||[]).includes(current.get(application)))select.value=current.get(application);
    label.append(select);
    label.title=(byApplication[application]||[]).length?'':'No saved '+application+' profiles. Upload with: vmbox profiles save '+application+' NAME';
    wrap.append(label);
   }
   const parts=[(state.profiles||[]).length?'Imported: '+(state.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', '):'No imported login profiles recorded'];
   if((state.pending||[]).length)parts.push('Queued for next start: '+(state.pending||[]).map(ref=>ref.application+' · '+ref.name).join(', '));
   $('#box-credentials-current').textContent=parts.join(' · ')+'.';
   status.textContent='';$('#box-credentials-modal').hidden=false;
  }catch(e){status.textContent=e.message}
 }
 $('#box-credentials-apply').onclick=async()=>{
  if(!boxCredentialTarget)return;
  const status=$('#box-credentials-status'),profiles=[...$('#box-credentials-form').querySelectorAll('select')].filter(select=>select.value).map(select=>({application:select.name,name:select.value}));
  status.textContent='Applying credentials…';
  try{
   const result=await api(boxPath(boxCredentialTarget.id)+'/login-profiles','PUT',{profiles});
   status.textContent=result.note||'Saved.';
   $('#box-credentials-current').textContent=(result.profiles||[]).length?'Imported: '+(result.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', ')+'.':'No imported login profiles recorded.';
   toast('Login profiles updated for '+boxCredentialTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };

 void enter();
})();
