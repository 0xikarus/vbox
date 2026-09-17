'use strict';
(()=>{
 const $=s=>document.querySelector(s);
 const listEl=$('#chat-entries'),messagesEl=$('#chat-messages'),appEl=$('#chat-app'),statusEl=$('#chat-status'),inputEl=$('#chat-input'),composer=$('#chat-composer'),attachBtn=$('#attach'),fileInput=$('#attachments'),draftsEl=$('#chat-image-drafts'),forwardMenu=$('#forward-menu'),filterEl=$('#chat-filter'),pushBtn=$('#push-toggle');
 const boxes=new Map(),rows=new Map(),imageURLs=new Map(),answeredQuestions=new Set();
 let selected='',owner=false,boxTimer,msgTimer,lastSignature='';
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

 /* ---------- chat list ---------- */
 const fmtTime=value=>{const d=new Date(value),now=new Date(),sameDay=d.toDateString()===now.toDateString();if(sameDay)return d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});const yesterday=new Date(now);yesterday.setDate(now.getDate()-1);if(d.toDateString()===yesterday.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'2-digit',month:'2-digit',year:'numeric'})};
 function summarize(id){
  const box=boxes.get(id);if(!box)return;
  const ms=box.messages||[];
  box.last=ms[ms.length-1];
  box.streaming=ms.some(m=>m.state==='streaming');
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
  list.sort((a,b)=>{const at=a.last?new Date(a.last.createdAt).getTime():0,bt=b.last?new Date(b.last.createdAt).getTime():0;return bt-at||a.name.localeCompare(b.name)});
  $('#chat-list-empty').hidden=list.length>0;
  for(const box of list){
   let row=rows.get(box.id);
   if(!row){
    row=document.createElement('li');row.dataset.boxId=box.id;
    const meta=document.createElement('div');meta.className='chat-meta';
    const r1=document.createElement('div');r1.className='row1';const name=document.createElement('span');name.className='name';name.textContent=box.name;const time=document.createElement('time');r1.append(name,time);
    const r2=document.createElement('div');r2.className='row2';const badge=document.createElement('span');badge.className='agent-badge';badge.textContent=box.defaultAgent||'agent';const preview=document.createElement('span');preview.className='preview';const unread=document.createElement('span');unread.className='unread';unread.hidden=true;r2.append(badge,preview,unread);
    meta.append(r1,r2);row.prepend(meta);
    row.onclick=()=>{location.hash='box='+box.id;openBox(box.id)};
    rows.set(box.id,row);
   }
   row.classList.toggle('active',box.id===selected);
   const oldAvatar=row.querySelector('.avatar');
   if(oldAvatar&&oldAvatar.dataset.state===box.state){/* keep the stable avatar node */}
   else{const avatar=avatarNode(box,false);if(oldAvatar)oldAvatar.replaceWith(avatar);else row.prepend(avatar)}
   row.querySelector('time').textContent=box.last?fmtTime(box.last.createdAt):'';
   row.querySelector('time').classList.toggle('recent',!!box.unread);
   const preview=row.querySelector('.preview');preview.textContent=box.streaming?'typing…':previewText(box.last);preview.classList.toggle('streaming',!!box.streaming);
   const unread=row.querySelector('.unread');unread.hidden=!box.unread;unread.textContent=box.unread>99?'99+':box.unread;
  }
  for(const [id,row] of rows){if(!boxes.has(id)){row.remove();rows.delete(id)}}
  listEl.replaceChildren(...list.map(b=>rows.get(b.id)));
 }

 /* ---------- messages ---------- */
 const dayLabel=value=>{const d=new Date(value),now=new Date();if(d.toDateString()===now.toDateString())return 'Today';const y=new Date(now);y.setDate(now.getDate()-1);if(d.toDateString()===y.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'numeric',month:'long',year:'numeric'})};
 const stateTicks={queued:'✓',delivering:'✓',delivered:'✓✓',failed:'⚠ failed',ambiguous:'⚠ maybe failed'};
 function imageURL(message,image){
  const key=message.id+':'+image.id;
  if(imageURLs.has(key))return Promise.resolve(imageURLs.get(key));
  return fetch('/v1/messages/'+encodeURIComponent(message.id)+'/images/'+encodeURIComponent(image.id),{credentials:'same-origin',signal:AbortSignal.timeout(30000)})
   .then(r=>{if(!r.ok)throw Error('image unavailable');return r.blob()}).then(b=>{const url=URL.createObjectURL(b);imageURLs.set(key,url);return url}).catch(()=>null);
 }
 const questionSelections=new Map();// messageId -> Set of checked choices; survives poll re-renders
 function questionAnswered(box,message){
  if(answeredQuestions.has(message.id))return true;
  const ms=box.messages||[],index=ms.findIndex(m=>m.id===message.id);
  return index>=0&&ms.slice(index+1).some(m=>m.direction==='user'&&m.text.startsWith('Answer to "'+message.question.text+'":'));
 }
 function questionForm(box,message){
  if(!message.question)return null;
  const form=document.createElement('form');form.className='question';const group='q-'+message.id;
  const saved=questionSelections.get(message.id);
  message.question.choices.forEach((choice,index)=>{
   const label=document.createElement('label'),input=document.createElement('input');
   input.type=message.question.multiple?'checkbox':'radio';input.name=group;input.value=choice;
   if(!message.question.multiple&&index===0)input.required=true;
   if(saved?.has(choice))input.checked=true;
   label.append(input,document.createTextNode(' '+choice));form.append(label);
  });
  form.addEventListener('change',()=>{questionSelections.set(message.id,new Set([...form.querySelectorAll('input:checked')].map(i=>i.value)))});
  const send=document.createElement('button');send.type='submit';send.textContent='Send selection';
  form.append(send);
  const answered=questionAnswered(box,message);
  send.disabled=answered;form.querySelectorAll('input').forEach(i=>i.disabled=answered);
  form.onsubmit=async event=>{
   event.preventDefault();
   const selectedChoices=[...form.querySelectorAll('input:checked')].map(i=>i.value);
   if(!selectedChoices.length){statusEl.textContent='Choose at least one option.';return}
   send.disabled=true;
   try{
    await api(boxPath(selected)+'/messages','POST',{'Idempotency-Key':crypto.randomUUID()},{text:'Answer to "'+message.question.text+'": '+selectedChoices.join(', ')});
    answeredQuestions.add(message.id);questionSelections.delete(message.id);form.querySelectorAll('input').forEach(i=>i.disabled=true);
    statusEl.textContent='Selection sent.';await refreshMessages();
   }catch(e){statusEl.textContent=e.message;send.disabled=false}
  };
  return form;
 }
 function bubble(box,message){
  const row=document.createElement('div');
  if(message.direction==='system'){row.className='msg system';row.append(Object.assign(document.createElement('span'),{className:'text',textContent:message.text}));return row}
  const mine=message.direction==='user';
  row.className='msg '+(mine?'user':'agent')+(message.state==='silent'?' note':'')+(message.state==='streaming'?' streaming':'');
  if(message.state==='silent'){const label=document.createElement('span');label.className='note-label';label.textContent='Note · not sent to the agent';row.append(label)}
  if(message.text.startsWith('Forwarded from ')){const mark=document.createElement('span');mark.className='fwd-mark';const end=message.text.indexOf(':\n');mark.textContent=end>0?message.text.slice(0,end+1):'Forwarded';row.append(mark)}
  const text=document.createElement('span');text.className='text';
  text.textContent=message.text.startsWith('Forwarded from ')&&message.text.indexOf(':\n')>0?message.text.slice(message.text.indexOf(':\n')+2):message.text;
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
 function renderMessages(box){
  const nearBottom=messagesEl.scrollHeight-messagesEl.scrollTop-messagesEl.clientHeight<120;
  messagesEl.replaceChildren();
  let day='';
  for(const message of box.messages||[]){
   const label=dayLabel(message.createdAt);
   if(label!==day){day=label;const sep=document.createElement('div');sep.className='day-sep';sep.textContent=day;messagesEl.append(sep)}
   messagesEl.append(bubble(box,message));
  }
  if(!(box.messages||[]).length){const hint=document.createElement('p');hint.className='day-sep';hint.textContent='No messages yet — say hello to '+box.name;messagesEl.append(hint)}
  if(nearBottom||lastSignature==='')messagesEl.scrollTop=messagesEl.scrollHeight;
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
  $('#chat-header-state').replaceChildren(Object.assign(document.createElement('span'),{className:box.state==='running'?'running':'',textContent:(box.defaultAgent||'agent')+' · '+box.state}));
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
  applySeen(selected);renderRows();
 }
 async function openBox(id){
  if(!boxes.has(id))return;
  selected=id;lastSignature='';
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;
  appEl.classList.add('in-chat');
  renderHeader();
  statusEl.textContent='';
  closeForwardMenu();
  try{await refreshMessages(true)}catch(e){statusEl.textContent=e.message}
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
  try{await api('/v1/browser-session','DELETE')}catch{}
  for(const url of imageURLs.values())URL.revokeObjectURL(url);imageURLs.clear();
  for(const cached of avatarCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
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
   await loadBoxes();
   const id=new URLSearchParams(location.hash.slice(1)).get('box');
   if(id&&boxes.has(id))await openBox(id);
   schedule();renderPushState();void syncPushSubscription();
  }catch(e){$('#error').textContent=e.message;$('#login').hidden=false}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);for(const url of imageURLs.values())URL.revokeObjectURL(url)});
 void enter();
})();
