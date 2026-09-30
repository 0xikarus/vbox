'use strict';
(()=>{
 const $=s=>document.querySelector(s);
 const listEl=$('#chat-entries'),messagesEl=$('#chat-messages'),newMessagesBtn=$('#chat-new-messages'),appEl=$('#chat-app'),statusEl=$('#chat-status'),inputEl=$('#chat-input'),composer=$('#chat-composer'),attachBtn=$('#attach'),fileInput=$('#attachments'),draftsEl=$('#chat-image-drafts'),forwardMenu=$('#forward-menu'),filterEl=$('#chat-filter'),pushBtn=$('#push-toggle'),replyPreview=$('#reply-preview'),threadPanel=$('#thread-panel'),threadMessages=$('#thread-messages');
 const boxes=new Map(),rows=new Map(),pairs=new Map(),pairRows=new Map(),imageURLs=new Map(),imagePreviewURLs=new Map(),imagePending=new Map(),answeredQuestions=new Set(),pendingSends=new Map();
 let imageGeneration=0;
 const resumeChecks=new Map();
 let selected='',selectedPair='',owner=false,boxTimer,msgTimer,filterTimer,pushTimer,usageTimer,usageManualTimer,usageManualBaseline=null,usageManualStarted=0,lastSignature='',stickToBottom=true,viewEpoch=0;
 let usageProfiles=[],usageLoaded=false,selectedUsageProfile=null,chatUsageRequest=0,usageScope=null;
 const scrollMemory=new Map(),followMemory=new Map();
 let restoringTranscript=false;
 const previewFetched=new Map();let boxesPending=null;
 const attachmentDrafts=new Map();
 let pendingKey='',pendingFingerprint='',replyingTo=null;
 let instructionPresets={defaultName:'',presets:[]},chatCommands=[];
 const presetBodyCache=new Map();
 let boxInstructionTarget=null,boxCredentialTarget=null,createInstructionSource='';
 const seen=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}')}catch{return{}}})();
 const saveSeen=()=>localStorage.setItem('vmboxChatSeen',JSON.stringify(seen));
 const seenPairs=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatPairSeen')||'{}')}catch{return{}}})();
 const saveSeenPairs=()=>{try{localStorage.setItem('vmboxChatPairSeen',JSON.stringify(seenPairs))}catch{}};
 const pins=(()=>{try{const saved=JSON.parse(localStorage.getItem('vmboxChatPins')||'[]');return new Set(Array.isArray(saved)?saved.filter(key=>typeof key==='string'):[])}catch{return new Set()}})();
 const expandedMCPEvents=new Set();
 const pinKey=(kind,id)=>kind+':'+id;
 let groupStorageKey='';
 const chatGroups=[];
 const chatGroupMembers=new Map();
 let groupSaveQueue=Promise.resolve();
 const groupLayout=()=>({groups:chatGroups,members:Object.fromEntries(chatGroupMembers)});
 function applyChatGroups(saved){
  chatGroups.splice(0,chatGroups.length,...(Array.isArray(saved.groups)?saved.groups.filter(group=>group&&typeof group.id==='string'&&typeof group.name==='string').map(group=>({id:group.id,name:group.name.slice(0,48),collapsed:!!group.collapsed})):[]));
  chatGroupMembers.clear();
  for(const [key,id] of Object.entries(saved.members&&typeof saved.members==='object'?saved.members:{}))if(typeof id==='string'&&chatGroups.some(group=>group.id===id))chatGroupMembers.set(key,id);
  let pinsChanged=false;
  for(const key of chatGroupMembers.keys())if(pins.delete(key))pinsChanged=true;
  if(pinsChanged)savePins();
  groupNodes.clear();
 }
 async function loadChatGroups(accountId){
  groupStorageKey='vmboxChatSidebarGroups:'+accountId;
  let local={};try{local=JSON.parse(localStorage.getItem(groupStorageKey)||'{}')||{}}catch{}
  let saved=local;
  try{
   const remote=await api('/v1/chat-sidebar-layout');
   if(local.dirty||(!remote.exists&&(local.groups?.length||Object.keys(local.members||{}).length))){
    const migrated=await api('/v1/chat-sidebar-layout','PUT',{}, {groups:local.groups||[],members:local.members||{}});
    saved=migrated;
   }else saved=remote;
   try{localStorage.setItem(groupStorageKey,JSON.stringify({...saved,dirty:false}))}catch{}
  }catch(e){if(local.dirty||local.groups?.length)toast('Chat groups could not be synced. They are saved in this browser.');else console.warn('Could not load chat groups:',e)}
  applyChatGroups(saved);
 }
 const groupForChat=key=>chatGroups.find(group=>group.id===chatGroupMembers.get(key));
 function saveChatGroups(){
  const key=groupStorageKey,layout=JSON.parse(JSON.stringify(groupLayout()));
  try{localStorage.setItem(key,JSON.stringify({...layout,dirty:true}))}catch{}
  groupSaveQueue=groupSaveQueue.catch(()=>{}).then(async()=>{
   if(key!==groupStorageKey)return;
   try{
    await api('/v1/chat-sidebar-layout','PUT',{},layout);
    const current=localStorage.getItem(key);
    if(current){const backup=JSON.parse(current);if(JSON.stringify({groups:backup.groups,members:backup.members})===JSON.stringify(layout))localStorage.setItem(key,JSON.stringify({...layout,dirty:false}))}
   }catch{toast('Chat groups could not be synced. They are saved in this browser.')}
  });
 }
 function savePins(){try{localStorage.setItem('vmboxChatPins',JSON.stringify([...pins]))}catch{}}
 function togglePin(key){
  if(pins.has(key))pins.delete(key);else{pins.add(key);if(chatGroupMembers.delete(key))saveChatGroups()}
  savePins();
  renderRows();
 }
 function moveChatToGroup(key,id){
  const group=chatGroups.find(group=>group.id===id);
  if(!group)return;
  chatGroupMembers.set(key,id);group.collapsed=false;saveChatGroups();
  if(pins.delete(key))savePins();
  renderRows();
 }
 function removeChatFromGroup(key){if(chatGroupMembers.delete(key)){saveChatGroups();renderRows()}}
 // Unsent composer text is kept per box so switching chats (or reloading the
 // page) never loses what you were typing. Uploaded attachment drafts are also
 // keyed per box below; their local previews intentionally live only this page.
 const inputDrafts=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatInputDrafts')||'{}')}catch{return{}}})();
 const saveInputDrafts=()=>{try{localStorage.setItem('vmboxChatInputDrafts',JSON.stringify(inputDrafts))}catch{}};
 let inputDraftTimer=0;
 if(window.VMBoxAIHelper){
  window.VMBoxAIHelper.attach({input:inputEl,kind:'chat',status:statusEl,getContext:()=>selected,getAttachments:()=>attachmentDrafts.get(selected)||[],prepareText:expandChatCommands});
  window.VMBoxAIHelper.attach({input:$('#preset-form textarea[name="markdown"]'),kind:'markdown',status:$('#preset-status')});
  window.VMBoxAIHelper.attach({input:$('#box-instructions-markdown'),kind:'markdown',status:$('#box-instructions-status')});
  window.VMBoxAIHelper.attach({input:$('#create-instructions-custom'),kind:'markdown'});
 }

 /* ---------- desktop conversation sidebar ---------- */
 const sidebar=$('#chat-list'),splitter=$('#chat-resizer'),sidebarStorageKey='vmboxChatSidebarWidth';
 let preferredSidebarWidth=0,sidebarDrag=null;
 try{preferredSidebarWidth=Number(localStorage.getItem(sidebarStorageKey))||0}catch{}
 function sidebarLimits(){const width=appEl.getBoundingClientRect().width;return {min:240,max:Math.max(240,Math.min(640,width-464))}}
 function applySidebarWidth(width=preferredSidebarWidth,persist=false){
  if(!matchMedia('(min-width:900px)').matches)return;
  const limits=sidebarLimits();
  const initial=innerWidth>=1100?308:Math.max(256,Math.min(384,appEl.getBoundingClientRect().width*.28));
  const next=Math.round(Math.max(limits.min,Math.min(limits.max,width||initial)));
  appEl.style.setProperty('--chat-sidebar-width',next+'px');
  splitter.setAttribute('aria-valuemin',String(limits.min));splitter.setAttribute('aria-valuemax',String(limits.max));
  splitter.setAttribute('aria-valuenow',String(next));splitter.setAttribute('aria-valuetext',next+' pixels');
  if(persist){preferredSidebarWidth=next;try{localStorage.setItem(sidebarStorageKey,String(next))}catch{}}
  if(!threadPanel.hidden)applyThreadWidth();
 }
 splitter.addEventListener('pointerdown',event=>{
  if(event.button!==0||!matchMedia('(min-width:900px)').matches)return;
  event.preventDefault();sidebarDrag={x:event.clientX,width:sidebar.getBoundingClientRect().width};
  splitter.setPointerCapture(event.pointerId);document.body.classList.add('chat-resizing');
 });
 splitter.addEventListener('pointermove',event=>{if(sidebarDrag)applySidebarWidth(sidebarDrag.width+event.clientX-sidebarDrag.x)});
 function endSidebarDrag(){if(!sidebarDrag)return;sidebarDrag=null;document.body.classList.remove('chat-resizing');applySidebarWidth(Number(splitter.getAttribute('aria-valuenow')),true)}
 splitter.addEventListener('pointerup',endSidebarDrag);splitter.addEventListener('pointercancel',endSidebarDrag);splitter.addEventListener('lostpointercapture',endSidebarDrag);
 splitter.addEventListener('keydown',event=>{
  const limits=sidebarLimits(),current=sidebar.getBoundingClientRect().width;
  const next=event.key==='ArrowLeft'?current-24:event.key==='ArrowRight'?current+24:event.key==='Home'?limits.min:event.key==='End'?limits.max:null;
  if(next===null)return;event.preventDefault();applySidebarWidth(next,true);
 });
 addEventListener('resize',()=>{if(!sidebarDrag)applySidebarWidth()});

 /* ---------- thread sidebar width ---------- */
 const threadSplitter=$('#thread-resizer'),threadStorageKey='vmboxChatThreadWidth';
 let preferredThreadWidth=0,threadDrag=null;
 try{preferredThreadWidth=Number(localStorage.getItem(threadStorageKey))||0}catch{}
 function threadLimits(){const width=appEl.getBoundingClientRect().width;return {min:280,max:Math.max(280,Math.min(760,width-sidebar.getBoundingClientRect().width-160))}}
 function applyThreadWidth(width=preferredThreadWidth,persist=false){
  if(!matchMedia('(min-width:900px)').matches)return;
  const limits=threadLimits(),next=Math.round(Math.max(limits.min,Math.min(limits.max,width||416)));
  threadPanel.style.setProperty('--chat-thread-width',next+'px');
  threadSplitter.setAttribute('aria-valuemin',String(limits.min));threadSplitter.setAttribute('aria-valuemax',String(limits.max));
  threadSplitter.setAttribute('aria-valuenow',String(next));threadSplitter.setAttribute('aria-valuetext',next+' pixels');
  if(persist){preferredThreadWidth=next;try{localStorage.setItem(threadStorageKey,String(next))}catch{}}
 }
 threadSplitter.addEventListener('pointerdown',event=>{
  if(event.button!==0||!matchMedia('(min-width:900px)').matches)return;
  event.preventDefault();threadDrag={x:event.clientX,width:threadPanel.getBoundingClientRect().width};
  threadSplitter.setPointerCapture(event.pointerId);document.body.classList.add('chat-resizing');
 });
 threadSplitter.addEventListener('pointermove',event=>{if(threadDrag)applyThreadWidth(threadDrag.width+threadDrag.x-event.clientX)});
 function endThreadDrag(){if(!threadDrag)return;threadDrag=null;document.body.classList.remove('chat-resizing');applyThreadWidth(Number(threadSplitter.getAttribute('aria-valuenow')),true)}
 threadSplitter.addEventListener('pointerup',endThreadDrag);threadSplitter.addEventListener('pointercancel',endThreadDrag);threadSplitter.addEventListener('lostpointercapture',endThreadDrag);
 threadSplitter.addEventListener('keydown',event=>{
  const limits=threadLimits(),current=threadPanel.getBoundingClientRect().width;
  const next=event.key==='ArrowLeft'?current+24:event.key==='ArrowRight'?current-24:event.key==='Home'?limits.min:event.key==='End'?limits.max:null;
  if(next===null)return;event.preventDefault();applyThreadWidth(next,true);
 });
 addEventListener('resize',()=>{if(!threadDrag)applyThreadWidth()});

 async function api(path,method='GET',headers={},body,timeout=60000){
  let r;try{r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(timeout)})}catch{throw Error('Controller connection interrupted. The operation may still be running.')}
  if(r.status===401){showLogin('Please log in to the controller.');throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  return r.status===204?null:r.json();
 }
 async function chatHistory(path){
  let r;try{r=await fetch(path,{credentials:'same-origin',signal:AbortSignal.timeout(60000)})}catch{throw Error('Controller connection interrupted. The operation may still be running.')}
  if(r.status===401){showLogin('Please log in to the controller.');throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  const rawBusy=r.headers.get('X-Vmbox-Agent-Busy');
  return {messages:await r.json(),busy:rawBusy===null?null:rawBusy==='true',busySince:r.headers.get('X-Vmbox-Agent-Busy-Since')||''};
 }
 const boxPath=id=>'/v1/logical-boxes/'+encodeURIComponent(id);

 /* The chat palette, type, spacing and brand mascot are fixed for vbox. */
 try{localStorage.removeItem('vmboxChatTheme')}catch{}
 const WORKSPACE_THEME={bg:'#111923',bg2:'#18212d',surface:'#1a2230',surface2:'#202e3d',
  ink:'#eaf0f8','ink-soft':'#91a0b3',line:'#2b3848',
  accent:'#8fb8f5','accent-text':'#aaceff','accent-ink':'#102136','accent-soft':'#263749',
  pop:'#b691e6','bubble-out':'#294b65','bubble-in':'#1b2938','bubble-in-ink':'#eaf0f8',
  danger:'#ec727c','danger-text':'#ffadb5',warn:'#e3b66d','warn-text':'#f2cf8e',ok:'#69d3a4','ok-text':'#8ee5ba'};
 function deriveTheme(){
  const t=Object.assign({},WORKSPACE_THEME,{radius:'6px','radius-sm':'4px','radius-lg':'9px',shadow:'0 5px 18px #08121d44','shadow-pop':'0 22px 70px #06101bbb',wall:'none'});
  return {font:'system-ui, -apple-system, "Segoe UI", sans-serif',radius:6,tokens:t};
 }
 function applyVariant(){
  document.documentElement.dataset.variant='A';
  const derived=deriveTheme();
  const root=document.documentElement.style;
  for(const key in derived.tokens)root.setProperty('--'+key,derived.tokens[key]);
  root.setProperty('--font',derived.font);
  const themeColor=document.querySelector('meta[name="theme-color"]');if(themeColor)themeColor.setAttribute('content',derived.tokens.bg);
  refreshAccountMascots();
 }

 /* ═══════════════════════════════════════════════════════════════════════
    Emoji mascot: a seeded blockies-style companion with a real six-mood
    state machine. Every visible trait comes from the seed. State changes
    run through a flat turn-around; events that arrive mid-turn are queued.
    ═══════════════════════════════════════════════════════════════════════ */
 const MX_SHAPES=['round','round','egg','pear','bean','lump','wide','tall'];
 const MX_PATTERNS=['none','none','none','waves','spots','splotch','curvedHalf','ripple','swirl'];
 function smoothClosed(pts){
  const n=pts.length,f=v=>v.toFixed(1);
  let d='M'+f(pts[0][0])+' '+f(pts[0][1]);
  for(let i=0;i<n;i++){const p0=pts[(i-1+n)%n],p1=pts[i],p2=pts[(i+1)%n],p3=pts[(i+2)%n];
   d+='C'+f(p1[0]+(p2[0]-p0[0])/6)+' '+f(p1[1]+(p2[1]-p0[1])/6)+' '+f(p2[0]-(p3[0]-p1[0])/6)+' '+f(p2[1]-(p3[1]-p1[1])/6)+' '+f(p2[0])+' '+f(p2[1]);}
  return d+'Z';
 }
 function blobPoints(cx,cy,rx,ry,t){
  const n=t.lumps.length;
  const pts=t.lumps.map((k,i)=>{const a=-Math.PI/2+i/n*Math.PI*2,sx=Math.cos(a),sy=Math.sin(a);
   let f=1+(k-.5)*2*t.wobble;
   if(t.shape==='egg')f*=1-.16*Math.max(0,-sy);
   if(t.shape==='pear')f*=1+.15*Math.max(0,sy);
   if(t.shape==='bean')f*=1+.13*Math.cos(a*2+t.phase);
   if(t.shape==='lump')f*=1+.11*Math.sin(a*3+t.phase);
   if(t.shape==='wide')f*=1+.13*Math.abs(sx);
   if(t.shape==='tall')f*=1+.13*Math.abs(sy);
   return [cx+rx*f*sx,cy+ry*f*sy];});
  const dy=(cy+ry)-Math.max(...pts.map(p=>p[1]));
  return pts.map(p=>[p[0],p[1]+dy]);
 }
 const mxBodyPath=t=>smoothClosed(blobPoints(120,190-t.H/2,t.W/2,t.H/2,t));
 function mxPattern(t){
  const W=t.W,H=t.H,x=120-W/2,y=190-H,cy=y+H/2,tone=t.patTone,op=tone==='#fff'?.2:.15,sw=t.patW;
  const wrapF=i=>'<g fill="'+tone+'" fill-opacity="'+op+'">'+i+'</g>';
  const wrapS=(i,extra)=>'<g fill="none" stroke="'+tone+'" stroke-opacity="'+op+'" stroke-width="'+sw+'" stroke-linecap="round" '+(extra||'')+'>'+i+'</g>';
  const ripplePts=r=>t.lumps.map((k,j)=>{const a=j/t.lumps.length*Math.PI*2,rr=r*(1+(k-.5)*.35);return [120+rr*Math.cos(a),cy+rr*.92*Math.sin(a)];});
  let out='';
  switch(t.pattern){
   case 'waves':{const span=Math.max(W,H)*1.45,x0=120-span/2,x1=120+span/2;
    for(let yy=cy-span/2;yy<cy+span/2;yy+=t.patGap){let d='M'+x0+' '+yy.toFixed(1),up=true;
     for(let xx=x0;xx<x1;xx+=t.waveLen,up=!up)d+='Q'+(xx+t.waveLen/2).toFixed(1)+' '+(yy+(up?-t.waveAmp:t.waveAmp)).toFixed(1)+' '+(xx+t.waveLen).toFixed(1)+' '+yy.toFixed(1);
     out+='<path d="'+d+'"/>';}
    return wrapS(out,'transform="rotate('+t.patAngle+' 120 '+cy.toFixed(1)+')"');}
   case 'spots':return wrapF(t.spots.map(sp=>'<circle cx="'+(120+sp[0]*W/2).toFixed(1)+'" cy="'+(cy+sp[1]*H/2).toFixed(1)+'" r="'+(sp[2]*sw*.9).toFixed(1)+'"/>').join(''));
   case 'splotch':return wrapF(t.splotches.map(sp=>smoothClosed(sp.k.map((k,i)=>{const a=i/sp.k.length*Math.PI*2,r=sp.r*(1+(k-.5)*.7);return [120+sp.x*W/2+r*Math.cos(a),cy+sp.y*H/2+r*Math.sin(a)];}))).map(d=>'<path d="'+d+'"/>').join(''));
   case 'curvedHalf':{const yy=cy+t.halfOff*H*.16,amp=t.waveAmp*1.8;let d='M'+(x-10)+' '+yy.toFixed(1),up=true;
    for(let xx=x-10;xx<x+W+10;xx+=t.waveLen,up=!up)d+='Q'+(xx+t.waveLen/2).toFixed(1)+' '+(yy+(up?-amp:amp)).toFixed(1)+' '+(xx+t.waveLen).toFixed(1)+' '+yy.toFixed(1);
    d+='L'+(x+W+10)+' '+(y+H+12)+'L'+(x-10)+' '+(y+H+12)+'Z';return wrapF('<path d="'+d+'"/>');}
   case 'ripple':for(let i=1;i<=3;i++)out+='<path d="'+smoothClosed(ripplePts(W*.17*i))+'"/>';return wrapS(out);
   case 'swirl':{let d='M120 '+cy.toFixed(1);
    for(let a=.2;a<Math.PI*6;a+=.18){const r=a*(W*.028);d+='L'+(120+r*Math.cos(a+t.phase)).toFixed(1)+' '+(cy+r*.9*Math.sin(a+t.phase)).toFixed(1);}
    return wrapS('<path d="'+d+'"/>');}
   default:return '';
  }
 }
 function makeTraits(seedStr){return {...window.VBoxMascot.traits(seedStr),seed:String(seedStr)}}
 const MACHINE=window.VBoxMascot.MACHINE;
 class Mascot extends window.VBoxMascot.Mascot{}
 function boxMascotPose(box){
  if(box.state==='hibernated')return ['sleeping','sleeping','sleeping'];
  if(box.state==='failed'||box.last?.state==='failed')return ['angry','error','failed'];
  const lastQuestion=[...(box.messages||[])].reverse().find(message=>message.direction!=='user'&&message.question&&!questionAnswered(box,message));
  if(lastQuestion)return ['waiting','surprised','asking'];
  if(box.processing)return ['working','focused','busy'];
  if(box.unread)return ['happy','surprised','unread'];
  return box.state==='running'?['idle',null,'idle']:['waking','surprised','starting'];
 }
 function syncAvatarMascot(avatar,box){
  if(!avatar)return;
  const mascot=avatar.querySelector('.avatar-mascot svg')?.__vboxMascot;
  if(!mascot)return;
  const [mood,expression,signal]=boxMascotPose(box);
  if(mascot.state!==mood||mascot.expression!==expression||mascot.signal!==signal)mascot.jump(mood,expression,signal);
  avatar.dataset.state=box.state;
  avatar.querySelector('.dot')?.classList.toggle('running',box.state==='running');
 }
 const accountMascots=[];
 function refreshAccountMascots(){
  accountMascots.splice(0).forEach(m=>m.destroy&&m.destroy());
  for(const id of ['chat-empty-mascot']){
   const host=document.getElementById(id);if(!host)continue;
   accountMascots.push(new Mascot(host,'vbox-brand',{appearance:{color:'#FF6F59',eyes:'A'}}));
  }
 }
 /* ═══ message body: tiny Markdown via /markdown.js, bare URLs linkified ═══ */
 const markdownHint=/(^|\n)\s*(#{1,3}\s|[-*+]\s|\d+[.)]\s|>|```|~~~)|\*\*[^*\n]|`[^`\n]|\[[^\]\n]+\]\(/;
 function linkifyTextNodes(root){
  const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
  const targets=[];
  while(walker.nextNode()){const node=walker.currentNode;if(/https?:\/\//i.test(node.nodeValue))targets.push(node);}
  for(const node of targets){
   const replacement=document.createDocumentFragment();
   const text=node.nodeValue;let last=0,match;linkPattern.lastIndex=0;
   while((match=linkPattern.exec(text))){
    if(match.index>last)replacement.append(document.createTextNode(text.slice(last,match.index)));
    const link=document.createElement('a');link.href=match[0];link.textContent=match[0];link.target='_blank';link.rel='noopener noreferrer';
    replacement.append(link);last=match.index+match[0].length;
   }
   if(last<text.length)replacement.append(document.createTextNode(text.slice(last)));
   if(node.parentNode)node.parentNode.replaceChild(replacement,node);
  }
 }
 function renderRichText(el,text){
  if(!text)return;
  if(markdownHint.test(text)&&typeof window.markdownToNodes==='function'){
   const root=window.markdownToNodes(text);
   root.querySelectorAll('.md-p').forEach(p=>{const last=p.lastChild;if(last&&last.nodeType===3)last.nodeValue=last.nodeValue.replace(/\n$/,'')});
   linkifyTextNodes(root);
   enhanceMediaLinks(root);
   el.append(root);
   return;
  }
  el.append(linkify(text));
  enhanceMediaLinks(el);
 }

 /* ═══════════════════════════════════════════════════════════════════════
    Attachments and media embeds are first-class controls: every image is a
    real <button> (tabbable, Enter/Space activates) that opens a focus-managed
    lightbox, and links that point at image/video/audio media are labelled and
    focusable so keyboard users can activate them too. The lightbox supports
    image, video and audio and restores focus when it closes.
    ═══════════════════════════════════════════════════════════════════════ */
 const MEDIA_IMAGE=/\.(png|jpe?g|gif|webp|avif|svg)$/i;
 const MEDIA_VIDEO=/\.(mp4|webm|mov|m4v|ogv|avi)$/i;
 const MEDIA_AUDIO=/\.(mp3|wav|ogg|oga|m4a|aac|flac)$/i;
 function mediaKind(mediaType,url){
  const type=(mediaType||'').toLowerCase();
  if(type.startsWith('video/'))return 'video';
  if(type.startsWith('audio/'))return 'audio';
  if(type.startsWith('image/'))return 'image';
  try{const path=new URL(url,location.href).pathname;
   if(MEDIA_VIDEO.test(path))return 'video';
   if(MEDIA_AUDIO.test(path))return 'audio';
   if(MEDIA_IMAGE.test(path))return 'image';}catch{}
  return 'image';
 }
 const mediaKindLabel=kind=>kind==='video'?'video':kind==='audio'?'audio':'image';
 const mediaEndpoint=(messageID,imageID)=>'/v1/messages/'+encodeURIComponent(messageID)+'/images/'+encodeURIComponent(imageID);
 const mediaGlyph=kind=>kind==='video'?'▶':kind==='audio'?'♪':'▣';
 const mediaViewer=$('#media-viewer'),mediaBody=$('#media-viewer-body'),mediaOpen=$('#media-viewer-open'),
  mediaPrev=$('#media-viewer-prev'),mediaNext=$('#media-viewer-next'),mediaCount=$('#media-viewer-count'),mediaAnnotate=$('#media-annotate'),
  mediaZoomControls=$('#media-viewer-zoom'),mediaZoomOut=$('#media-viewer-zoom-out'),mediaZoomIn=$('#media-viewer-zoom-in'),mediaZoomReset=$('#media-viewer-zoom-reset'),mediaZoomLevel=$('#media-viewer-zoom-level');
 let mediaGallery=[],mediaIndex=0,mediaReturnFocus=null,mediaRequest=0,mediaZoom=1,mediaBaseSize=null;
 const maxMediaZoom=5;
 const mediaImage=()=>mediaBody.querySelector('.media-viewer-frame img');
 function updateMediaZoomControls(){
  const image=mediaImage(),ready=!!image?.naturalWidth;
  mediaBody.classList.toggle('zoomed',mediaZoom>1&&!mediaZoomControls.hidden);
  mediaZoomLevel.textContent=Math.round(mediaZoom*100)+'%';
  mediaZoomOut.disabled=!ready||mediaZoom<=1;
  mediaZoomIn.disabled=!ready||mediaZoom>=maxMediaZoom;
  mediaZoomReset.disabled=!ready||mediaZoom===1;
 }
 function applyMediaZoom(focalX,focalY){
  const image=mediaImage();if(!image?.naturalWidth)return;
  const frame=image.parentElement,rect=mediaBody.getBoundingClientRect();
  if(!mediaBaseSize||mediaBaseSize.image!==image){
   const fit=Math.min(1,Math.max(1,mediaBody.clientWidth)/image.naturalWidth,Math.max(1,Math.min(mediaBody.clientHeight||innerHeight*.8,innerHeight*.82))/image.naturalHeight);
   mediaBaseSize={image,width:image.naturalWidth*fit,height:image.naturalHeight*fit};
  }
  const oldWidth=Number.parseFloat(frame.style.width)||mediaBaseSize.width,oldHeight=Number.parseFloat(frame.style.height)||mediaBaseSize.height;
  const x=focalX==null?rect.width/2:focalX-rect.left,y=focalY==null?rect.height/2:focalY-rect.top;
  const contentX=mediaBody.scrollLeft+x,contentY=mediaBody.scrollTop+y;
  const width=mediaBaseSize.width*mediaZoom,height=mediaBaseSize.height*mediaZoom;
  frame.classList.add('ready');frame.style.width=width+'px';frame.style.height=height+'px';
  image.style.width=width+'px';image.style.height=height+'px';
  mediaBody.scrollLeft=contentX*width/oldWidth-x;
  mediaBody.scrollTop=contentY*height/oldHeight-y;
  updateMediaZoomControls();
 }
 function setMediaZoom(next,focalX,focalY){
  if(mediaZoomControls.hidden)return;
  mediaZoom=Math.max(1,Math.min(maxMediaZoom,Math.round(next*4)/4));
  applyMediaZoom(focalX,focalY);
 }
 function renderMediaItem(url,{kind='image',alt='',loading=false,status=''}={}){
  const playing=mediaBody.querySelector('video,audio');
  if(playing){try{playing.pause()}catch{}playing.removeAttribute('src');try{playing.load()}catch{}}
  mediaBody.replaceChildren();mediaBaseSize=null;
  if(!url&&!loading){const miss=document.createElement('div');miss.className='media-missing';miss.textContent=status||'Attachment unavailable';mediaBody.append(miss);return}
  if(kind==='video'||kind==='audio'){
   const el=document.createElement(kind);el.src=url;el.controls=true;el.playsInline=true;if(kind==='video')el.autoplay=true;
   el.setAttribute('aria-label',alt||('Embedded '+mediaKindLabel(kind)));mediaBody.append(el);
  }else{
   const frame=document.createElement('div');frame.className='media-viewer-frame';
   if(url){const img=document.createElement('img');img.alt=alt||'Attachment';img.draggable=false;img.onload=()=>{if(mediaBody.contains(img))applyMediaZoom()};img.src=url;frame.append(img)}
   if(loading||status){const note=document.createElement('span');note.className='media-viewer-status';note.textContent=status||'Loading full image…';frame.append(note)}
   mediaBody.append(frame);
  }
  updateMediaZoomControls();
 }
 async function showMediaAt(index){
  const item=mediaGallery[index];if(!item)return;
  mediaIndex=index;const request=++mediaRequest;
  mediaZoom=1;mediaBaseSize=null;mediaBody.scrollLeft=0;mediaBody.scrollTop=0;
  mediaTouchPoints.clear();mediaTouchStart=null;mediaPinchStart=null;mediaPinched=false;mediaMousePan=null;
  mediaZoomControls.hidden=item.kind!=='image';mediaBody.classList.toggle('zoomable',item.kind==='image');updateMediaZoomControls();
  const many=mediaGallery.length>1;
  mediaPrev.hidden=!many;mediaNext.hidden=!many;
  mediaPrev.disabled=mediaIndex<=0;mediaNext.disabled=mediaIndex>=mediaGallery.length-1;
  mediaCount.hidden=!many;mediaCount.textContent=(mediaIndex+1)+' / '+mediaGallery.length;
  mediaAnnotate.hidden=true;
  let url=item.url;
  if(!url&&item.messageId){
   if(item.kind==='video'||item.kind==='audio'){
    // Point straight at the authenticated endpoint so the browser can range
    // request and stream instead of buffering a blob.
    url=mediaEndpoint(item.messageId,item.imageId);
   }else{
    const key=item.messageId+':'+item.imageId;
    renderMediaItem(imagePreviewURLs.get(key),{...item,loading:true});
    let fullFinished=false;
    const previewRequest=imageURL(item.messageId,item.imageId,true);
    void previewRequest.then(preview=>{if(request===mediaRequest&&!fullFinished)renderMediaItem(preview,{...item,loading:true})});
    url=await imageURL(item.messageId,item.imageId);
    if(!url)await previewRequest;
    fullFinished=true;
    if(request!==mediaRequest||mediaViewer.hidden)return;
    renderMediaItem(url||imagePreviewURLs.get(key),{...item,status:url?'':'Full image unavailable'});
   }
  }
  if(item.kind!=='image'||!item.messageId||item.url)renderMediaItem(url,item);
  if(/^https?:/i.test(url||'')){mediaOpen.hidden=false;mediaOpen.href=url}else{mediaOpen.hidden=true;mediaOpen.removeAttribute('href')}
  if(url&&item.kind==='image'&&item.replyMessage&&item.boxId===selected&&!selectedPair){
   mediaAnnotate.hidden=false;
   mediaAnnotate.onclick=()=>void openImageAnnotation(item,url);
  }
 }
 function openMediaViewer(gallery,index=0){
  mediaReturnFocus=document.activeElement;
  mediaGallery=(Array.isArray(gallery)?gallery:(gallery?[gallery]:[])).filter(Boolean);
  if(!mediaGallery.length)return;
  mediaViewer.hidden=false;
  void showMediaAt(Math.max(0,Math.min(index,mediaGallery.length-1)));
  $('#media-viewer-close').focus();
 }
 function closeMediaViewer(){
  if(mediaViewer.hidden)return;
  mediaRequest++;
  const playing=mediaBody.querySelector('video,audio');
  if(playing){try{playing.pause()}catch{}playing.removeAttribute('src');try{playing.load()}catch{}}
  mediaBody.replaceChildren();mediaViewer.hidden=true;mediaGallery=[];mediaIndex=0;mediaAnnotate.hidden=true;
  mediaZoom=1;mediaBaseSize=null;mediaZoomControls.hidden=true;mediaBody.classList.remove('zoomable');
  mediaTouchPoints.clear();mediaTouchStart=null;mediaPinchStart=null;mediaPinched=false;mediaMousePan=null;updateMediaZoomControls();
  if(mediaReturnFocus&&document.contains(mediaReturnFocus))mediaReturnFocus.focus();
  mediaReturnFocus=null;
 }
 function messageMediaGallery(message){
  return (message.images||[]).map(image=>({messageId:message.id,imageId:image.id,kind:mediaKind(image.mediaType,''),alt:'Attachment '+image.number+' from '+message.direction,label:'Attachment '+image.number,replyMessage:message,boxId:selected}));
 }
 function mediaButton(url,{kind='image',alt='',label='',gallery=null,index=0}={}){
  const btn=document.createElement('button');btn.type='button';btn.className='media-button';
  btn.setAttribute('aria-label','Open '+mediaKindLabel(kind)+(label?': '+label:alt?': '+alt:''));
  if(kind==='image'){
   const frame=document.createElement('span');frame.className='media-preview';
   const placeholder=document.createElement('span');placeholder.className='media-preview-placeholder';placeholder.textContent='Loading '+(label||'image')+'…';
   const img=document.createElement('img');img.className='chat-image';img.alt=alt||'';img.loading='lazy';
   img.onload=()=>{placeholder.hidden=true;frame.classList.add('ready')};
   img.onerror=()=>{placeholder.textContent='Preview unavailable';frame.classList.add('failed')};
   frame.append(placeholder,img);btn.append(frame);
   if(url)img.src=url;
  }else{
   const chip=document.createElement('span');chip.className='media-chip '+kind;
   const glyph=document.createElement('span');glyph.className='media-glyph';glyph.setAttribute('aria-hidden','true');glyph.textContent=mediaGlyph(kind);
   const name=document.createElement('span');name.textContent=label||alt||(kind==='video'?'Play video':'Play audio');
   chip.append(glyph,name);btn.append(chip);
  }
  btn.onclick=()=>openMediaViewer(gallery?.length?gallery:[{url,kind,alt,label}],index);
  return btn;
 }
 function enhanceMediaLinks(root){
  root.querySelectorAll('a[href]').forEach(anchor=>{
   let path='';try{path=new URL(anchor.getAttribute('href'),location.href).pathname}catch{}
   if(!MEDIA_IMAGE.test(path)&&!MEDIA_VIDEO.test(path)&&!MEDIA_AUDIO.test(path))return;
   const kind=mediaKind('',anchor.getAttribute('href'));
   anchor.classList.add('media-link',kind);
   anchor.setAttribute('aria-label','Open '+mediaKindLabel(kind)+': '+(anchor.textContent||anchor.getAttribute('href')));
  });
 }
 $('#media-viewer-close').onclick=closeMediaViewer;
 $('#media-viewer-backdrop').onclick=closeMediaViewer;
 mediaPrev.onclick=()=>{if(mediaIndex>0)void showMediaAt(mediaIndex-1)};
 mediaNext.onclick=()=>{if(mediaIndex<mediaGallery.length-1)void showMediaAt(mediaIndex+1)};
 mediaZoomOut.onclick=()=>setMediaZoom(mediaZoom-.5);
 mediaZoomIn.onclick=()=>setMediaZoom(mediaZoom+.5);
 mediaZoomReset.onclick=()=>setMediaZoom(1);
 addEventListener('resize',()=>{if(!mediaViewer.hidden&&!mediaZoomControls.hidden){mediaBaseSize=null;applyMediaZoom()}});
 mediaBody.addEventListener('dblclick',event=>{if(mediaZoomControls.hidden)return;event.preventDefault();setMediaZoom(mediaZoom>1?1:2,event.clientX,event.clientY)});
 mediaViewer.addEventListener('wheel',event=>{
  if(mediaZoomControls.hidden||!(event.ctrlKey||event.metaKey))return;
  event.preventDefault();setMediaZoom(mediaZoom+(event.deltaY<0?.25:-.25),event.clientX,event.clientY);
 },{passive:false});
 const mediaTouchPoints=new Map();let mediaTouchStart=null,mediaPinchStart=null,mediaPinched=false,mediaMousePan=null;
 const touchDistance=()=>{const [a,b]=[...mediaTouchPoints.values()];return a&&b?Math.hypot(a.x-b.x,a.y-b.y):0};
 mediaBody.addEventListener('pointerdown',event=>{
  if(!mediaBody.classList.contains('zoomable'))return;
  if(event.pointerType==='touch'){
   mediaTouchPoints.set(event.pointerId,{x:event.clientX,y:event.clientY});
   try{mediaBody.setPointerCapture(event.pointerId)}catch{}
   if(mediaTouchPoints.size===1){mediaTouchStart={x:event.clientX,y:event.clientY,left:mediaBody.scrollLeft,top:mediaBody.scrollTop};mediaPinched=false}
   else if(mediaTouchPoints.size===2){mediaPinchStart={distance:touchDistance(),zoom:mediaZoom};mediaPinched=true}
  }else if(event.button===0&&mediaZoom>1){
   event.preventDefault();
   mediaMousePan={x:event.clientX,y:event.clientY,left:mediaBody.scrollLeft,top:mediaBody.scrollTop};
   try{mediaBody.setPointerCapture(event.pointerId)}catch{}
  }
 });
 mediaBody.addEventListener('pointermove',event=>{
  if(event.pointerType==='touch'&&mediaTouchPoints.has(event.pointerId)){
   mediaTouchPoints.set(event.pointerId,{x:event.clientX,y:event.clientY});
   if(mediaTouchPoints.size>=2&&mediaPinchStart?.distance){
    const [a,b]=[...mediaTouchPoints.values()];
    setMediaZoom(mediaPinchStart.zoom*touchDistance()/mediaPinchStart.distance,(a.x+b.x)/2,(a.y+b.y)/2);
   }else if(mediaZoom>1&&mediaTouchStart){
    mediaBody.scrollLeft=mediaTouchStart.left-(event.clientX-mediaTouchStart.x);
    mediaBody.scrollTop=mediaTouchStart.top-(event.clientY-mediaTouchStart.y);
   }
  }else if(mediaMousePan){
   mediaBody.scrollLeft=mediaMousePan.left-(event.clientX-mediaMousePan.x);
   mediaBody.scrollTop=mediaMousePan.top-(event.clientY-mediaMousePan.y);
  }
 });
 function finishMediaPointer(event,cancelled){
  if(event.pointerType==='touch'&&mediaTouchPoints.has(event.pointerId)){
   if(!cancelled&&!mediaPinched&&mediaZoom===1&&mediaTouchPoints.size===1&&mediaTouchStart){
    const dx=event.clientX-mediaTouchStart.x,dy=event.clientY-mediaTouchStart.y;
    if(Math.abs(dx)>48&&Math.abs(dx)>Math.abs(dy)){
     if(dx<0&&mediaIndex<mediaGallery.length-1)void showMediaAt(mediaIndex+1);
     else if(dx>0&&mediaIndex>0)void showMediaAt(mediaIndex-1);
    }
   }
   mediaTouchPoints.delete(event.pointerId);
   if(mediaTouchPoints.size===1){const point=[...mediaTouchPoints.values()][0];mediaTouchStart={...point,left:mediaBody.scrollLeft,top:mediaBody.scrollTop};mediaPinchStart=null}
   else if(mediaTouchPoints.size===0){mediaTouchStart=null;mediaPinchStart=null;mediaPinched=false}
  }
  if(event.pointerType!=='touch')mediaMousePan=null;
 }
 mediaBody.addEventListener('pointerup',event=>finishMediaPointer(event,false));
 mediaBody.addEventListener('pointercancel',event=>finishMediaPointer(event,true));
 let swipeX=0,swipeY=0;
 mediaBody.addEventListener('touchstart',event=>{if(mediaBody.classList.contains('zoomable'))return;const t=event.changedTouches[0];swipeX=t.clientX;swipeY=t.clientY},{passive:true});
 mediaBody.addEventListener('touchend',event=>{
  if(mediaBody.classList.contains('zoomable'))return;
  const t=event.changedTouches[0],dx=t.clientX-swipeX,dy=t.clientY-swipeY;
  if(Math.abs(dx)<=48||Math.abs(dx)<=Math.abs(dy))return;
  if(dx<0&&mediaIndex<mediaGallery.length-1)void showMediaAt(mediaIndex+1);
  else if(dx>0&&mediaIndex>0)void showMediaAt(mediaIndex-1);
 },{passive:true});
 addEventListener('keydown',event=>{
  if(mediaViewer.hidden)return;
  if(event.key==='Escape'){event.preventDefault();closeMediaViewer();return}
  if(event.key==='ArrowLeft'){event.preventDefault();if(mediaIndex>0)void showMediaAt(mediaIndex-1);return}
  if(event.key==='ArrowRight'){event.preventDefault();if(mediaIndex<mediaGallery.length-1)void showMediaAt(mediaIndex+1);return}
  if(!mediaZoomControls.hidden&&['+','=','-','0'].includes(event.key)){
   event.preventDefault();setMediaZoom(event.key==='0'?1:mediaZoom+(event.key==='-'?-.5:.5));return;
  }
  if(event.key==='Tab'){
   const focusable=[...mediaViewer.querySelectorAll('button,a[href],video,audio')].filter(el=>!el.hidden);
   if(!focusable.length)return;
   const first=focusable[0],last=focusable[focusable.length-1];
   if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}
   else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}
  }
 },true);

 /* ---------- annotate a received image, then attach it to a reply ---------- */
 const annotationDialog=$('#image-annotation'),annotationCanvas=$('#image-annotation-canvas'),annotationContext=annotationCanvas.getContext('2d');
 const annotationStatus=$('#image-annotation-status');
 let annotationImage=null,annotationTarget=null,annotationStrokes=[],activeAnnotationStroke=null,activeAnnotationPointer=null,annotationColor='#f44336',annotationBusy=false;
 function annotationPoint(event){
  const rect=annotationCanvas.getBoundingClientRect();
  return {x:Math.max(0,Math.min(annotationCanvas.width,(event.clientX-rect.left)*annotationCanvas.width/rect.width)),y:Math.max(0,Math.min(annotationCanvas.height,(event.clientY-rect.top)*annotationCanvas.height/rect.height))};
 }
 function drawAnnotationStroke(stroke){
  if(!stroke.points.length)return;
  const ctx=annotationContext;ctx.strokeStyle=stroke.color;ctx.fillStyle=stroke.color;ctx.lineWidth=stroke.width;ctx.lineCap='round';ctx.lineJoin='round';
  if(stroke.points.length===1){const point=stroke.points[0];ctx.beginPath();ctx.arc(point.x,point.y,stroke.width/2,0,Math.PI*2);ctx.fill();return}
  ctx.beginPath();ctx.moveTo(stroke.points[0].x,stroke.points[0].y);
  for(const point of stroke.points.slice(1))ctx.lineTo(point.x,point.y);
  ctx.stroke();
 }
 function redrawAnnotation(){
  if(!annotationImage)return;
  annotationContext.clearRect(0,0,annotationCanvas.width,annotationCanvas.height);
  annotationContext.drawImage(annotationImage,0,0,annotationCanvas.width,annotationCanvas.height);
  for(const stroke of annotationStrokes)drawAnnotationStroke(stroke);
 }
 function updateAnnotationTools(){
  const enabled=annotationStrokes.length>0&&!annotationBusy;
  $('#image-annotation-undo').disabled=!enabled;$('#image-annotation-clear').disabled=!enabled;
 }
 async function openImageAnnotation(item,url){
  const target={boxId:item.boxId,message:item.replyMessage,imageId:item.imageId};
  closeMediaViewer();annotationTarget=target;annotationImage=null;annotationStrokes=[];activeAnnotationStroke=null;activeAnnotationPointer=null;annotationBusy=false;
  $('#image-annotation-text').value='';annotationStatus.textContent='Loading image…';updateAnnotationTools();
  annotationDialog.showModal();
  try{
   const image=new Image();image.src=url;await image.decode();
   if(annotationTarget!==target||!annotationDialog.open)return;
   const scale=Math.min(1,4096/image.naturalWidth,4096/image.naturalHeight,Math.sqrt(8000000/(image.naturalWidth*image.naturalHeight)));
   annotationCanvas.width=Math.max(1,Math.round(image.naturalWidth*scale));annotationCanvas.height=Math.max(1,Math.round(image.naturalHeight*scale));
   annotationImage=image;redrawAnnotation();annotationStatus.textContent='Draw on the image, then add it to your reply.';
  }catch{if(annotationTarget===target)annotationStatus.textContent='Could not open the full image for annotation.'}
 }
 annotationCanvas.addEventListener('pointerdown',event=>{
  if(!annotationImage||annotationBusy||activeAnnotationStroke||(event.button!==0&&event.pointerType!=='touch'))return;
  event.preventDefault();annotationCanvas.setPointerCapture(event.pointerId);
  activeAnnotationPointer=event.pointerId;
  activeAnnotationStroke={color:annotationColor,width:Math.max(3,annotationCanvas.width/260)*Number($('#image-annotation-size').value),points:[annotationPoint(event)]};
  annotationStrokes.push(activeAnnotationStroke);drawAnnotationStroke(activeAnnotationStroke);updateAnnotationTools();
 });
 annotationCanvas.addEventListener('pointermove',event=>{
  if(!activeAnnotationStroke||activeAnnotationPointer!==event.pointerId||!annotationCanvas.hasPointerCapture(event.pointerId))return;
  event.preventDefault();const stroke=activeAnnotationStroke,previous=stroke.points.at(-1),point=annotationPoint(event);
  stroke.points.push(point);annotationContext.strokeStyle=stroke.color;annotationContext.lineWidth=stroke.width;annotationContext.lineCap='round';annotationContext.beginPath();annotationContext.moveTo(previous.x,previous.y);annotationContext.lineTo(point.x,point.y);annotationContext.stroke();
 });
 const finishAnnotationStroke=event=>{if(activeAnnotationPointer===event.pointerId){activeAnnotationStroke=null;activeAnnotationPointer=null}};
 annotationCanvas.addEventListener('pointerup',finishAnnotationStroke);annotationCanvas.addEventListener('pointercancel',finishAnnotationStroke);
 annotationDialog.querySelectorAll('[data-annotation-color]').forEach(button=>button.onclick=()=>{
  annotationColor=button.dataset.annotationColor;
  annotationDialog.querySelectorAll('[data-annotation-color]').forEach(choice=>choice.setAttribute('aria-pressed',String(choice===button)));
 });
 $('#image-annotation-undo').onclick=()=>{annotationStrokes.pop();redrawAnnotation();updateAnnotationTools()};
 $('#image-annotation-clear').onclick=()=>{annotationStrokes=[];redrawAnnotation();updateAnnotationTools()};
 $('#image-annotation-close').onclick=()=>{if(!annotationBusy)annotationDialog.close()};
 annotationDialog.addEventListener('cancel',event=>{if(annotationBusy)event.preventDefault()});
 annotationDialog.addEventListener('close',()=>{annotationTarget=null;annotationImage=null;annotationStrokes=[];activeAnnotationStroke=null;activeAnnotationPointer=null;annotationCanvas.width=0;annotationCanvas.height=0});
 $('#image-annotation-add').onclick=async()=>{
  const target=annotationTarget;if(!target||annotationBusy||!annotationImage)return;
  if(selected!==target.boxId){annotationStatus.textContent='Return to the original chat before adding this image.';return}
  annotationBusy=true;$('#image-annotation-add').disabled=true;$('#image-annotation-close').disabled=true;updateAnnotationTools();
  try{
   annotationStatus.textContent='Preparing image…';
   let blob=await new Promise(resolve=>annotationCanvas.toBlob(resolve,'image/png'));
   if(!blob)throw Error('Could not export the annotated image.');
   let extension='png';
   if(blob.size>25*1024*1024){
    const flattened=document.createElement('canvas');flattened.width=annotationCanvas.width;flattened.height=annotationCanvas.height;
    const ctx=flattened.getContext('2d');ctx.fillStyle='#fff';ctx.fillRect(0,0,flattened.width,flattened.height);ctx.drawImage(annotationCanvas,0,0);
    blob=await new Promise(resolve=>flattened.toBlob(resolve,'image/jpeg',.85));extension='jpg';
    flattened.width=0;flattened.height=0;
    if(!blob)throw Error('Could not export the annotated image.');
   }
   const file=new File([blob],'annotated-'+target.imageId+'.'+extension,{type:blob.type});
   annotationStatus.textContent='Uploading image…';
   if(await uploadImages([file],{boxID:target.boxId,statusTarget:annotationStatus})!==1)return;
   setReply(target.message);
   const note=$('#image-annotation-text').value.trim();
   if(note){inputEl.value=inputEl.value.trim()?inputEl.value.trimEnd()+'\n'+note:note;inputEl.dispatchEvent(new Event('input',{bubbles:true}))}
   annotationDialog.close();toast('Annotated image added to your reply.');inputEl.focus();
  }catch(error){annotationStatus.textContent=error.message||'Could not add the annotated image.'}
  finally{annotationBusy=false;$('#image-annotation-add').disabled=false;$('#image-annotation-close').disabled=false;updateAnnotationTools()}
 };

 /* ---------- avatars: desktop preview thumbnails, blob-cached for 60s ---- */
 const avatarCache=new Map(),avatarPending=new Set();
 const AVATAR_OK_TTL=60000,AVATAR_RETRY_TTL=8000;
 const avatarFresh=(cached,state)=>!!cached&&cached.state===state&&Date.now()-cached.at<=(cached.ttl||AVATAR_OK_TTL);
 function avatarRefresh(box){
  if(box.state!=='running'){avatarCache.set(box.id,{url:null,state:box.state,at:Date.now(),ttl:AVATAR_RETRY_TTL});return}
  if(avatarPending.has(box.id))return;
  avatarPending.add(box.id);
  fetch(boxPath(box.id)+'/desktop/screenshot?thumbnail=true',{credentials:'same-origin',signal:AbortSignal.timeout(15000)})
   .then(r=>{if(!r.ok||!r.headers.get('Content-Type')?.startsWith('image/'))throw Error('No desktop thumbnail');return r.blob()})
   .then(b=>{
    const old=avatarCache.get(box.id);if(old?.url)URL.revokeObjectURL(old.url);
    avatarCache.set(box.id,{url:URL.createObjectURL(b),state:'running',at:Date.now(),ttl:AVATAR_OK_TTL});
   })
   .catch(()=>{const old=avatarCache.get(box.id);if(old?.url)URL.revokeObjectURL(old.url);avatarCache.set(box.id,{url:null,state:box.state,at:Date.now(),ttl:AVATAR_RETRY_TTL})})
   .finally(()=>{avatarPending.delete(box.id);refreshAvatarNodes(box)});
 }
 function avatarImageFailed(box,url){
  if(avatarCache.get(box.id)?.url!==url)return;
  URL.revokeObjectURL(url);
  avatarCache.set(box.id,{url:null,state:box.state,at:Date.now(),ttl:AVATAR_RETRY_TTL});
  refreshAvatarNodes(box);
 }
 function refreshAvatarNodes(box){
  const cached=avatarCache.get(box.id);
  document.querySelectorAll('[data-avatar="'+box.id+'"]').forEach(node=>{
   if(node.dataset.state!==box.state)return;
   let img=node.querySelector('img');
   if(cached?.url){if(!img){img=document.createElement('img');img.alt='';node.prepend(img)}if(img.src!==cached.url){img.onerror=()=>avatarImageFailed(box,cached.url);img.src=cached.url}}
   else img?.remove();
  });
 }
 function avatarNode(box,small,preview){
  const wrap=document.createElement('span');wrap.className='avatar'+(small?' small':'');
  wrap.dataset.avatar=box.id;wrap.dataset.state=box.state;
  const base=document.createElement('span');base.className='avatar-mascot';new Mascot(base,box.id);const [mood,expression,signal]=boxMascotPose(box),mascot=base.querySelector('svg').__vboxMascot;if(mood!=='idle'||expression)mascot.jump(mood,expression,signal);
  wrap.append(base);
  const initials=document.createElement('span');initials.className='initials';initials.hidden=true;initials.textContent=(box.name||'?').trim().slice(0,2).toUpperCase();wrap.append(initials);
  let cached=avatarCache.get(box.id);
  if(!avatarFresh(cached,box.state))avatarRefresh(box);
  cached=avatarCache.get(box.id);
  if(cached?.state===box.state&&cached.url){const img=document.createElement('img');img.alt='';img.onerror=()=>avatarImageFailed(box,cached.url);img.src=cached.url;wrap.prepend(img)}
  const dot=document.createElement('span');dot.className='dot'+(box.state==='running'?' running':'');wrap.append(dot);
  if(preview&&box.state==='running'){
   wrap.classList.add('preview-trigger');wrap.tabIndex=0;wrap.setAttribute('role','button');
   wrap.title='Hover to preview; click for Desktop/TMUX control';wrap.setAttribute('aria-label','Preview '+box.name+' desktop and open Desktop or TMUX control');
   const currentBox=()=>boxes.get(box.id)||box;
   wrap.onmouseenter=()=>showTvPreview(wrap,currentBox());wrap.onmouseleave=scheduleHideTvPreview;
   wrap.onfocus=()=>{if(!coarsePointer())showTvPreview(wrap,currentBox())};wrap.onblur=()=>{if(!coarsePointer())scheduleHideTvPreview()};
   wrap.onclick=event=>{event.stopPropagation();const box=currentBox();if(coarsePointer()){if(tvPreviewEl.hidden||tvPreviewBox!==box.id)showTvPreview(wrap,box);else hideTvPreview();return}void openBoxControl(box,'desktop')};
   wrap.onkeydown=event=>{if(event.key==='Enter'||event.key===' '){event.preventDefault();event.stopPropagation();void openBoxControl(currentBox(),'desktop')}};
  }
  return wrap;
 }

 /* ---------- TV preview: hover the processing bubble for a bigger view ---- */
 const tvPreviewEl=document.createElement('div');tvPreviewEl.className='tv-preview';tvPreviewEl.hidden=true;
 const tvPreviewScreen=document.createElement('div');tvPreviewScreen.className='tv-preview-screen';
 const tvPreviewImg=document.createElement('img');tvPreviewImg.alt='';tvPreviewImg.hidden=true;
 const tvPreviewLive=document.createElement('div');tvPreviewLive.className='tv-preview-live';
 const tvPreviewNoSignal=document.createElement('div');tvPreviewNoSignal.className='tv-preview-nosignal';tvPreviewNoSignal.textContent='NO SIGNAL';
 const tvPreviewControls=document.createElement('div');tvPreviewControls.hidden=true;
 const tvPreviewNote=document.createElement('span');tvPreviewNote.className='tv-preview-note';
 const tvTimeline=document.createElement('div');tvTimeline.className='tv-preview-timeline';
 const tvLiveButton=document.createElement('button');tvLiveButton.type='button';tvLiveButton.textContent='Live';tvLiveButton.title='Return to live desktop';
 const tvPlayButton=document.createElement('button');tvPlayButton.type='button';tvPlayButton.textContent='▶';tvPlayButton.title='Play the last 30 minutes';
 const tvRange=document.createElement('input');tvRange.type='range';tvRange.min='0';tvRange.max='0';tvRange.value='0';tvRange.setAttribute('aria-label','Desktop replay timeline');
 const tvTime=document.createElement('span');tvTime.className='tv-preview-time';tvTime.textContent='No replay yet';
 tvTimeline.append(tvLiveButton,tvPlayButton,tvRange,tvTime);
 tvPreviewScreen.append(tvPreviewImg,tvPreviewLive,tvPreviewNoSignal);tvPreviewEl.append(tvPreviewScreen,tvPreviewNote,tvTimeline,tvPreviewControls);
 document.body.append(tvPreviewEl);
 const tvShotCache=new Map(),tvReplayCache=new Map();
 let tvPreviewBox='',tvPreviewAnchor=null,tvPreviewSidebar=false,tvPreviewConnected=false,tvPreviewDispose=null,tvPreviewHideTimer=0,tvReplayIndex=null,tvReplayURL='',tvReplayRequest=0,tvReplayTimer=0;
 tvPreviewEl.onmouseenter=()=>clearTimeout(tvPreviewHideTimer);
 tvPreviewEl.onmouseleave=scheduleHideTvPreview;
 tvPreviewEl.onclick=event=>{if(event.target.closest('button,input'))return;const box=boxes.get(tvPreviewBox);if(box)void openBoxControl(box,'desktop')};
 tvLiveButton.onclick=()=>{stopTvReplay();tvReplayRequest++;tvReplayIndex=null;const box=boxes.get(tvPreviewBox);if(box){tvShotRender(box);startTvLive(box)}renderTvTimeline()};
 tvPlayButton.onclick=()=>{
  if(tvReplayTimer){stopTvReplay();return}
  const frames=tvReplayCache.get(tvPreviewBox)?.frames||[];if(!frames.length)return;
  void selectTvReplayFrame(tvReplayIndex===null||tvReplayIndex>=frames.length-1?0:tvReplayIndex);
  tvPlayButton.textContent='Ⅱ';
  tvReplayTimer=setInterval(()=>{const current=tvReplayCache.get(tvPreviewBox)?.frames||[];if(tvReplayIndex===null||tvReplayIndex>=current.length-1){stopTvReplay();return}void selectTvReplayFrame(tvReplayIndex+1)},750);
 };
 tvRange.oninput=()=>{stopTvReplay();void selectTvReplayFrame(Number(tvRange.value))};
 function stopTvReplay(){clearInterval(tvReplayTimer);tvReplayTimer=0;tvPlayButton.textContent='▶'}
 function renderTvTimeline(){
  const frames=tvReplayCache.get(tvPreviewBox)?.frames||[];
  tvRange.disabled=!frames.length;tvPlayButton.disabled=!frames.length;
  tvRange.max=String(Math.max(0,frames.length-1));tvRange.value=String(tvReplayIndex===null?Math.max(0,frames.length-1):tvReplayIndex);
  tvLiveButton.classList.toggle('active',tvReplayIndex===null);
  const selectedFrame=tvReplayIndex===null?null:frames[tvReplayIndex];
  tvTime.textContent=selectedFrame?new Date(selectedFrame.capturedAt).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit',second:'2-digit'}):frames.length?frames.length+' frames · 30 min':'No replay yet';
 }
 async function loadTvReplay(box){
  const cached=tvReplayCache.get(box.id);
  if(cached&&Date.now()-cached.at<20000){renderTvTimeline();return}
  try{
   const frames=await api(boxPath(box.id)+'/desktop/replay');
   tvReplayCache.set(box.id,{frames:frames||[],at:Date.now()});
   if(tvPreviewBox===box.id&&!tvPreviewEl.hidden)renderTvTimeline();
  }catch{if(tvPreviewBox===box.id)tvTime.textContent='Replay unavailable'}
 }
 async function selectTvReplayFrame(index){
  const boxID=tvPreviewBox,frames=tvReplayCache.get(boxID)?.frames||[];
  if(!frames[index])return;
  tvReplayIndex=index;tvPreviewDispose?.();tvPreviewDispose=null;tvPreviewConnected=false;tvPreviewLive.classList.remove('connected');
  const request=++tvReplayRequest;
  tvPreviewImg.hidden=false;tvPreviewNoSignal.hidden=true;tvPreviewNote.textContent='Loading replay frame…';renderTvTimeline();
  try{
   const response=await fetch(boxPath(boxID)+'/desktop/replay/'+encodeURIComponent(frames[index].id),{credentials:'same-origin',signal:AbortSignal.timeout(15000)});
   if(!response.ok)throw Error(response.status);
   const blob=await response.blob();
   if(request!==tvReplayRequest||tvPreviewBox!==boxID||tvPreviewEl.hidden)return;
   if(tvReplayURL)URL.revokeObjectURL(tvReplayURL);
   tvReplayURL=URL.createObjectURL(blob);tvPreviewImg.src=tvReplayURL;tvPreviewImg.hidden=false;
   tvPreviewNote.textContent='Replay · click for Desktop/TMUX control';
  }catch{if(request===tvReplayRequest)tvPreviewNote.textContent='Replay frame unavailable'}
 }
 function tvShotRender(box){
  if(tvReplayIndex!==null)return;
  const cached=tvShotCache.get(box.id);
  const hasShot=!!cached?.url;
  tvPreviewImg.hidden=tvPreviewConnected||!hasShot;
  if(hasShot&&tvPreviewImg.src!==cached.url)tvPreviewImg.src=cached.url;
  tvPreviewNoSignal.hidden=tvPreviewConnected||hasShot;
  tvPreviewNote.textContent=tvPreviewConnected?'Live · click for Desktop/TMUX control':hasShot?'Connecting live view… · click for control':'No desktop signal · click for Desktop/TMUX control';
 }
 function startTvLive(box){
  if(tvPreviewDispose||typeof window.openWorkspaceDesktop!=='function')return;
  const previewID=box.id;
  tvPreviewDispose=openWorkspaceDesktop(previewID,status=>{
   if(tvPreviewBox!==previewID||tvPreviewEl.hidden||tvReplayIndex!==null)return;
   tvPreviewConnected=status==='Desktop connected';
   tvPreviewLive.classList.toggle('connected',tvPreviewConnected);
   tvShotRender(box);
  },{root:tvPreviewLive,controls:tvPreviewControls,viewOnly:true,onDisconnect:()=>{
   if(tvPreviewBox!==previewID)return;
   tvPreviewConnected=false;tvPreviewLive.classList.remove('connected');tvShotRender(box);
  }});
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
    if(!tvPreviewEl.hidden&&tvPreviewBox===box.id)tvShotRender(box);
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
  clearTimeout(tvPreviewHideTimer);
  if(tvPreviewBox!==box.id){stopTvReplay();tvReplayRequest++;if(tvReplayURL)URL.revokeObjectURL(tvReplayURL);tvReplayURL='';tvReplayIndex=null;tvPreviewDispose?.();tvPreviewDispose=null;tvPreviewLive.replaceChildren();tvPreviewConnected=false;tvPreviewBox=box.id}
  tvPreviewAnchor=button;
  tvPreviewSidebar=!!button.closest('#chat-list');
  tvPreviewEl.hidden=false;
  tvShotRender(box);tvShotRefresh(box);renderTvTimeline();void loadTvReplay(box);startTvLive(box);
  positionTvPreview();
 }
 function currentTvPreviewAnchor(){
  if(tvPreviewAnchor?.isConnected)return tvPreviewAnchor;
  tvPreviewAnchor=[...document.querySelectorAll('.preview-trigger')].find(node=>node.dataset.avatar===tvPreviewBox&&Boolean(node.closest('#chat-list'))===tvPreviewSidebar)||null;
  return tvPreviewAnchor;
 }
 function positionTvPreview(){
  const anchor=currentTvPreviewAnchor();
  if(tvPreviewEl.hidden||!anchor)return;
  const rect=anchor.getBoundingClientRect(),sidebarPreview=tvPreviewSidebar;
  const sidebarRight=sidebar.getBoundingClientRect().right;
  const sideSpace=window.innerWidth-sidebarRight-24;
  const useSide=sidebarPreview&&sideSpace>=280;
  const availableBelow=Math.max(0,window.innerHeight-rect.bottom-24);
  const availableAbove=Math.max(0,rect.top-24);
  const verticalSpace=Math.max(availableBelow,availableAbove);
  const fitWidth=Math.max(240,Math.floor((verticalSpace-88)*1.6));
  const width=Math.min(860,window.innerWidth-24,useSide?sideSpace:fitWidth);
  tvPreviewEl.style.width=width+'px';
  const height=tvPreviewEl.offsetHeight;
  const left=useSide?sidebarRight+12:Math.min(Math.max(12,rect.left),window.innerWidth-width-12);
  let top;
  if(useSide)top=Math.max(12,Math.min(rect.top,window.innerHeight-height-12));
  else top=availableBelow>=height?rect.bottom+10:rect.top-height-10;
  tvPreviewEl.style.left=left+'px';tvPreviewEl.style.top=Math.max(12,top)+'px';
 }
 function scheduleHideTvPreview(){clearTimeout(tvPreviewHideTimer);tvPreviewHideTimer=setTimeout(()=>{if(!currentTvPreviewAnchor()?.matches(':hover')&&!tvPreviewEl.matches(':hover')&&!tvPreviewEl.contains(document.activeElement))hideTvPreview()},550)}
 function hideTvPreview(){clearTimeout(tvPreviewHideTimer);stopTvReplay();tvReplayRequest++;if(tvReplayURL)URL.revokeObjectURL(tvReplayURL);tvReplayURL='';tvReplayIndex=null;tvPreviewEl.hidden=true;tvPreviewNoSignal.hidden=true;tvPreviewDispose?.();tvPreviewDispose=null;tvPreviewLive.replaceChildren();tvPreviewLive.classList.remove('connected');tvPreviewConnected=false;tvPreviewBox='';tvPreviewAnchor=null;tvPreviewSidebar=false}
 addEventListener('scroll',()=>{if(tvPreviewEl.hidden)return;if(currentTvPreviewAnchor()?.matches(':hover')||tvPreviewEl.matches(':hover')||tvPreviewEl.contains(document.activeElement))positionTvPreview();else hideTvPreview()},true);
 addEventListener('resize',()=>{if(!tvPreviewEl.hidden)positionTvPreview()});
 // Touch has no hover. A tap on a preview trigger opens the TV preview, a tap
 // outside dismisses it, and long-pressing the image must not offer "save as".
 tvPreviewEl.addEventListener('contextmenu',event=>event.preventDefault());
 document.addEventListener('touchstart',event=>{
  if(tvPreviewEl.hidden)return;
  if(tvPreviewEl.contains(event.target))return;
  if(event.target.closest?.('.tv-button,.preview-trigger'))return;
  hideTvPreview();
 },{passive:true});
 // A touch-friendly context menu: hold a chat row instead of right-clicking.
 function bindLongPress(element,handler){
  let timer=0,startX=0,startY=0,fired=false;
  const cancel=()=>{clearTimeout(timer);timer=0};
  element.addEventListener('touchstart',event=>{
   if(event.touches.length!==1)return;
   const touch=event.touches[0];startX=touch.clientX;startY=touch.clientY;fired=false;
   cancel();timer=setTimeout(()=>{fired=true;handler(touch.clientX,touch.clientY)},500);
  },{passive:true});
  element.addEventListener('touchmove',event=>{const touch=event.touches[0];if(!touch)return;if(Math.abs(touch.clientX-startX)>12||Math.abs(touch.clientY-startY)>12)cancel()},{passive:true});
  element.addEventListener('touchend',event=>{cancel();if(fired){fired=false;event.preventDefault();event.stopPropagation()}},{passive:false});
  element.addEventListener('touchcancel',cancel,{passive:true});
 }
 const coarsePointer=()=>matchMedia('(hover:none) and (pointer:coarse)').matches;

 /* ---------- chat list ---------- */
 const fmtTime=value=>{const d=new Date(value),now=new Date(),sameDay=d.toDateString()===now.toDateString();if(sameDay)return d.toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});const yesterday=new Date(now);yesterday.setDate(now.getDate()-1);if(d.toDateString()===yesterday.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'2-digit',month:'2-digit',year:'numeric'})};
 function summarize(id){
  const box=boxes.get(id);if(!box)return;
  const ms=box.messages||[];
  box.last=ms[ms.length-1];
  box.streaming=ms.some(m=>m.state==='streaming');
  const agent=(box.defaultAgent||'').toLowerCase();
  const last=box.last;
  const pending=pendingSends.get(id);
  const replyDuringSend=pending&&ms.slice(pending.messageCount).some(m=>m.direction==='agent');
  const pendingBusy=pending&&!replyDuringSend;
  const inferredBusy=last&&last.direction==='user'&&last.state==='delivered'&&Date.now()-new Date(last.updatedAt||last.createdAt).getTime()<10*60*1000;
  // A controller value from the previous poll must not suppress a send that is
  // currently in flight in this page. Persisted state takes over after it lands.
  box.processing=agent!=='shell'&&!box.streaming&&(pendingBusy||(box.agentBusy===undefined?inferredBusy:box.agentBusy));
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
 const pairKey=pair=>pair.boxAId+'/'+pair.boxBId;
 const pairHasUnread=pair=>!!pair.lastAt&&new Date(pair.lastAt).getTime()>(seenPairs[pairKey(pair)]?new Date(seenPairs[pairKey(pair)]).getTime():0);
 const conversationVisible=()=>!$('#chat-conversation').hidden&&(matchMedia('(min-width:900px)').matches||appEl.classList.contains('in-chat'));
 function applyPairSeen(pair){
  if(!pair||!stickToBottom||!conversationVisible())return;
  const last=pair.messages?.at(-1)?.createdAt||pair.lastAt;
  if(last&&new Date(last).getTime()>(seenPairs[pairKey(pair)]?new Date(seenPairs[pairKey(pair)]).getTime():0)){seenPairs[pairKey(pair)]=last;saveSeenPairs()}
 }
 const pairGroup=mk('li','Box conversations');pairGroup.className='conversation-group';
 const pinnedGroup=mk('li','Pinned');pinnedGroup.className='conversation-group';
 const unpinnedDivider=mk('li');unpinnedDivider.className='conversation-divider';unpinnedDivider.setAttribute('role','separator');unpinnedDivider.setAttribute('aria-label','Other chats');unpinnedDivider.append(mk('span','Other chats'));
 const groupNodes=new Map();
 const knownChatKey=key=>key.startsWith('box:')?boxes.has(key.slice(4)):key.startsWith('pair:')?pairs.has(key.slice(5)):false;
 const draggedChatKey=event=>event.dataTransfer?.getData('application/x-vmbox-chat')||'';
 function bindChatDrag(row,key){
  row.draggable=true;
  row.addEventListener('dragstart',event=>{if(!event.dataTransfer)return;event.dataTransfer.setData('application/x-vmbox-chat',key);event.dataTransfer.effectAllowed='move';row.classList.add('dragging')});
  row.addEventListener('dragend',()=>row.classList.remove('dragging'));
 }
 function bindChatDrop(target,move){
  target.addEventListener('dragover',event=>{if(!Array.from(event.dataTransfer?.types||[]).includes('application/x-vmbox-chat'))return;event.preventDefault();event.dataTransfer.dropEffect='move';target.classList.add('drop-target')});
  target.addEventListener('dragleave',event=>{if(!target.contains(event.relatedTarget))target.classList.remove('drop-target')});
  target.addEventListener('drop',event=>{target.classList.remove('drop-target');const key=draggedChatKey(event);if(!knownChatKey(key))return;event.preventDefault();move(key)});
 }
 function moveChatToPinned(key){if(chatGroupMembers.delete(key))saveChatGroups();if(!pins.has(key)){pins.add(key);savePins()}renderRows()}
 function moveChatToOther(key){if(chatGroupMembers.delete(key))saveChatGroups();if(pins.delete(key))savePins();renderRows()}
 bindChatDrop(pinnedGroup,moveChatToPinned);
 bindChatDrop(unpinnedDivider,moveChatToOther);
 document.addEventListener('dragend',()=>document.querySelectorAll('.drop-target').forEach(node=>node.classList.remove('drop-target')));
 function groupHeader(group,count,unread){
  let row=groupNodes.get(group.id);
  if(!row){
   row=document.createElement('li');row.className='chat-folder';row.dataset.groupId=group.id;
   const toggle=document.createElement('button');toggle.type='button';toggle.className='chat-folder-toggle';
   const arrow=mk('span','▸');arrow.className='chat-folder-arrow';arrow.setAttribute('aria-hidden','true');
   const name=mk('span',group.name);name.className='chat-folder-name';
   const amount=mk('span');amount.className='chat-folder-count';
   const badge=mk('span');badge.className='unread';badge.hidden=true;
   toggle.append(arrow,name,amount,badge);toggle.onclick=()=>{group.collapsed=!group.collapsed;saveChatGroups();renderRows()};
   const menu=document.createElement('button');menu.type='button';menu.className='chat-folder-menu';menu.textContent='⋯';menu.setAttribute('aria-label','Group actions for '+group.name);
   menu.onclick=event=>{event.stopPropagation();const rect=menu.getBoundingClientRect();openGroupMenu(group.id,{x:rect.right,y:rect.bottom})};
   row.oncontextmenu=event=>{event.preventDefault();openGroupMenu(group.id,{x:event.clientX,y:event.clientY})};
   bindLongPress(row,(x,y)=>{if(rowMenu.hidden)openGroupMenu(group.id,{x,y})});
   bindChatDrop(row,key=>moveChatToGroup(key,group.id));
   row.append(toggle,menu);groupNodes.set(group.id,row);
  }
  row.querySelector('.chat-folder-name').textContent=group.name;
  row.querySelector('.chat-folder-menu').setAttribute('aria-label','Group actions for '+group.name);
  row.querySelector('.chat-folder-count').textContent=String(count);
  row.querySelector('.chat-folder-arrow').textContent=group.collapsed?'▸':'▾';
  const toggle=row.querySelector('.chat-folder-toggle');toggle.setAttribute('aria-expanded',String(!group.collapsed));
  toggle.setAttribute('aria-label',group.name+', '+count+' chats, '+unread+' new messages, '+(group.collapsed?'collapsed':'expanded'));
  const badge=row.querySelector('.unread');badge.hidden=!unread;badge.textContent=unread>99?'99+':String(unread);
  return row;
 }
 function renderRows(){
  const filter=filterEl.value.trim().toLowerCase();
  const matchesGroup=key=>groupForChat(key)?.name.toLowerCase().includes(filter);
  const list=[...boxes.values()].filter(b=>!filter||b.name.toLowerCase().includes(filter)||matchesGroup(pinKey('box',b.id)));
  // Keep the list stable: activity must not reshuffle rows under the pointer.
  list.sort((a,b)=>a.name.localeCompare(b.name)||a.id.localeCompare(b.id));
  for(const box of list){
   let row=rows.get(box.id);
   if(!row){
    row=document.createElement('li');row.dataset.boxId=box.id;
    bindChatDrag(row,pinKey('box',box.id));
    bindLongPress(row,(x,y)=>{if(rowMenu.hidden)openRowMenu({box},{x,y})});
    row.oncontextmenu=event=>{event.preventDefault();openRowMenu({box},{x:event.clientX,y:event.clientY})};
    const meta=document.createElement('button');meta.type='button';meta.className='chat-meta';meta.setAttribute('aria-label','Open chat with '+box.name);
    const r1=document.createElement('div');r1.className='row1';const name=document.createElement('span');name.className='name';name.textContent=box.name;const state=document.createElement('span');state.className='row-state';const time=document.createElement('time');r1.append(name,time);
    const r2=document.createElement('div');r2.className='row2';const badge=document.createElement('span');badge.className='agent-badge';badge.textContent=box.defaultAgent||'agent';const preview=document.createElement('span');preview.className='preview';const unread=document.createElement('span');unread.className='unread';unread.hidden=true;r2.append(state,badge,preview,unread);
    const note=mk('span','New messages');note.className='unread-note';note.hidden=true;
    meta.append(r1,r2,note);row.append(meta);
    row.onclick=()=>{location.hash='box='+box.id;openBox(box.id)};
    rows.set(box.id,row);
   }
   row.classList.toggle('active',box.id===selected&&!selectedPair);
   // Make the box state readable at a glance, not just a tiny dot.
   const starting=['creating','attaching','reserved','starting','allocating','restoring','pending'];
   const stateClass=box.state==='running'?'running':box.state==='failed'?'failed':starting.includes(box.state)?'starting':'muted';
   if(row.dataset.state!==stateClass)row.dataset.state=stateClass;
   const stateEl=row.querySelector('.row-state');if(stateEl.textContent!==box.state)stateEl.textContent=box.state;
   const oldAvatar=row.querySelector('.avatar');
   if(oldAvatar){
    syncAvatarMascot(oldAvatar,box);
    // Keep the node (its hover wiring and live preview), but still retry a
    // thumbnail that is stale or failed while the desktop was starting.
    if(!avatarFresh(avatarCache.get(box.id),box.state))avatarRefresh(box);
   }
   else{const avatar=avatarNode(box,false,true);if(oldAvatar)oldAvatar.replaceWith(avatar);else row.prepend(avatar)}
   const time=row.querySelector('time'),nextTime=box.last?fmtTime(box.last.createdAt):'';if(time.textContent!==nextTime)time.textContent=nextTime;
   row.querySelector('time').classList.toggle('recent',!!box.unread);
   const preview=row.querySelector('.preview'),nextPreview=box.streaming?'typing…':box.processing?'processing…':previewText(box.last);if(preview.textContent!==nextPreview)preview.textContent=nextPreview;preview.classList.toggle('streaming',!!box.streaming&&!box.processing);preview.classList.toggle('processing',!!box.processing&&!box.streaming);
   const unread=row.querySelector('.unread');unread.hidden=!box.unread;unread.textContent=box.unread>99?'99+':box.unread;
   const note=row.querySelector('.unread-note');note.hidden=!box.unread;note.textContent=box.unread===1?'New message':'New messages';
  }
  for(const [id,row] of rows){if(!boxes.has(id)){row.remove();rows.delete(id)}}
  const pairList=owner?[...pairs.values()].filter(pair=>!filter||(pair.boxAName+' '+pair.boxBName).toLowerCase().includes(filter)||matchesGroup(pinKey('pair',pairKey(pair)))):[];
  pairList.sort((a,b)=>a.boxAName.localeCompare(b.boxAName)||a.boxBName.localeCompare(b.boxBName));
  for(const pair of pairList){
   const key=pairKey(pair);let row=pairRows.get(key);
   if(!row){
    row=document.createElement('li');row.dataset.pairKey=key;
    bindChatDrag(row,pinKey('pair',key));
    bindLongPress(row,(x,y)=>{if(rowMenu.hidden)openRowMenu({pair},{x,y})});
    row.oncontextmenu=event=>{event.preventDefault();openRowMenu({pair},{x:event.clientX,y:event.clientY})};
    const avatar=document.createElement('span');avatar.className='pair-avatar';avatar.textContent='↔';avatar.setAttribute('aria-hidden','true');
    const meta=document.createElement('button');meta.type='button';meta.className='chat-meta';meta.setAttribute('aria-label','Open box conversation between '+pair.boxAName+' and '+pair.boxBName);
    const first=document.createElement('div');first.className='row1';first.append(mk('span',pair.boxAName+' ↔ '+pair.boxBName),document.createElement('time'));first.firstChild.className='name';
    const second=document.createElement('div');second.className='row2';const badge=mk('span','Box ↔ Box');badge.className='agent-badge';const preview=mk('span');preview.className='preview';second.append(badge,preview);
    const note=mk('span','New messages');note.className='unread-note';note.hidden=true;
    meta.append(first,second,note);row.append(avatar,meta);row.onclick=()=>{location.hash='pair='+encodeURIComponent(key);void openPair(key)};pairRows.set(key,row);
   }
   const name=pair.boxAName+' ↔ '+pair.boxBName;
   row.querySelector('.name').textContent=name;row.querySelector('.name').title=name;
   row.querySelector('.chat-meta').setAttribute('aria-label','Open box conversation between '+pair.boxAName+' and '+pair.boxBName);
   row.classList.toggle('active',key===selectedPair);
   row.querySelector('time').textContent=pair.lastAt?fmtTime(pair.lastAt):'';
   row.querySelector('.preview').textContent=pair.lastText||'No messages yet';
   row.querySelector('.unread-note').hidden=!pairHasUnread(pair);
  }
  for(const [key,row] of pairRows)if(!pairs.has(key)){row.remove();pairRows.delete(key)}
  const ungrouped=key=>!pins.has(key)&&!groupForChat(key);
  const pinnedBoxes=list.filter(box=>pins.has(pinKey('box',box.id))&&!groupForChat(pinKey('box',box.id))).map(box=>rows.get(box.id));
  const pinnedPairs=pairList.filter(pair=>pins.has(pinKey('pair',pairKey(pair)))&&!groupForChat(pinKey('pair',pairKey(pair)))).map(pair=>pairRows.get(pairKey(pair)));
  const otherBoxes=list.filter(box=>ungrouped(pinKey('box',box.id))).map(box=>rows.get(box.id));
  const otherPairs=pairList.filter(pair=>ungrouped(pinKey('pair',pairKey(pair)))).map(pair=>pairRows.get(pairKey(pair)));
  const desired=[];
  if(pinnedBoxes.length||pinnedPairs.length)desired.push(pinnedGroup,...pinnedBoxes,...pinnedPairs);
  for(const group of chatGroups){
   const members=list.filter(box=>chatGroupMembers.get(pinKey('box',box.id))===group.id).map(box=>rows.get(box.id));
   const memberPairs=pairList.filter(pair=>chatGroupMembers.get(pinKey('pair',pairKey(pair)))===group.id).map(pair=>pairRows.get(pairKey(pair)));
   if(filter&&!members.length&&!memberPairs.length&&!group.name.toLowerCase().includes(filter))continue;
   const groupBoxes=[...boxes.values()].filter(box=>chatGroupMembers.get(pinKey('box',box.id))===group.id);
   const groupPairs=owner?[...pairs.values()].filter(pair=>chatGroupMembers.get(pinKey('pair',pairKey(pair)))===group.id):[];
   const unread=groupBoxes.reduce((sum,box)=>sum+(box.unread||0),0)+groupPairs.filter(pairHasUnread).length;
   desired.push(groupHeader(group,groupBoxes.length+groupPairs.length,unread));
   if(!group.collapsed||filter)desired.push(...members,...memberPairs);
  }
  if((pinnedBoxes.length||pinnedPairs.length||chatGroups.length)&&(otherBoxes.length||otherPairs.length))desired.push(unpinnedDivider);
  desired.push(...otherBoxes);
  if(otherPairs.length)desired.push(pairGroup,...otherPairs);
  const empty=$('#chat-list-empty');empty.hidden=desired.length>0;
  empty.textContent=filter?'No matching conversations.':'No conversations yet. Create a box with the + button above.';
  if(desired.length!==listEl.children.length||desired.some((row,index)=>listEl.children[index]!==row))listEl.replaceChildren(...desired);
 }

 /* ---------- messages ---------- */
 const dayLabel=value=>{const d=new Date(value),now=new Date();if(d.toDateString()===now.toDateString())return 'Today';const y=new Date(now);y.setDate(now.getDate()-1);if(d.toDateString()===y.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'numeric',month:'long',year:'numeric'})};
 const stateTicks={queued:'queued',delivering:'sent',delivered:'delivered',failed:'failed',ambiguous:'Delivery unconfirmed. The worker connection ended before confirmation; check TMUX before resending.'};
 const stateIconName={queued:'clock',delivering:'check',delivered:'check-check',failed:'alert',ambiguous:'help'};
 // Inline Lucide icons (24x24, currentColor stroke) so delivery state reads as
 // iconography instead of emoji glyphs.
 const lucideShapes={
  clock:[['circle',{cx:'12',cy:'12',r:'10'}],['polyline',{points:'12 6 12 12 16 14'}]],
  check:[['path',{d:'M20 6 9 17l-5-5'}]],
  'check-check':[['path',{d:'M18 6 7 17l-5-5'}],['path',{d:'m22 10-7.5 7.5L13 16'}]],
  alert:[['path',{d:'m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3'}],['path',{d:'M12 9v4'}],['path',{d:'M12 17h.01'}]],
  help:[['circle',{cx:'12',cy:'12',r:'10'}],['path',{d:'M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3'}],['path',{d:'M12 17h.01'}]],
  'chevron-down':[['path',{d:'m6 9 6 6 6-6'}]],
  'message-square':[['path',{d:'M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z'}]],
  camera:[['path',{d:'M14.5 4h-5L7 7H4a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2h-3z'}],['circle',{cx:'12',cy:'13',r:'3'}]],
  users:[['path',{d:'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2'}],['circle',{cx:'9',cy:'7',r:'4'}],['path',{d:'M22 21v-2a4 4 0 0 0-3-3.87'}],['path',{d:'M16 3.13a4 4 0 0 1 0 7.75'}]],
  activity:[['path',{d:'M22 12h-4l-3 9L9 3l-3 9H2'}]],
  terminal:[['rect',{x:'2',y:'3',width:'20',height:'18',rx:'2'}],['path',{d:'m7 9 3 3-3 3'}],['path',{d:'M13 15h4'}]],
  copy:[['rect',{width:'14',height:'14',x:'8',y:'8',rx:'2',ry:'2'}],['path',{d:'M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2'}]],
  forward:[['path',{d:'m15 17 5-5-5-5'}],['path',{d:'M4 18v-2a4 4 0 0 1 4-4h12'}]],
  reply:[['polyline',{points:'9 17 4 12 9 7'}],['path',{d:'M20 18v-2a4 4 0 0 0-4-4H4'}]],
 };
 function lucide(name){
  const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');
  for(const [key,value] of Object.entries({viewBox:'0 0 24 24',fill:'none',stroke:'currentColor','stroke-width':'2','stroke-linecap':'round','stroke-linejoin':'round','aria-hidden':'true'}))svg.setAttribute(key,value);
  for(const [tag,attrs] of lucideShapes[name]||[]){const el=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const key in attrs)el.setAttribute(key,attrs[key]);svg.append(el)}
  return svg;
 }
 function imageURL(messageID,imageID,thumbnail=false){
  const key=messageID+':'+imageID,cache=thumbnail?imagePreviewURLs:imageURLs,pendingKey=(thumbnail?'thumb:':'full:')+key;
  if(cache.has(key))return Promise.resolve(cache.get(key));
  if(imagePending.has(pendingKey))return imagePending.get(pendingKey);
  const generation=imageGeneration;
  const request=fetch(mediaEndpoint(messageID,imageID)+(thumbnail?'?thumbnail=true':''),{credentials:'same-origin',signal:AbortSignal.timeout(30000)})
   .then(r=>{if(!r.ok)throw Error('image unavailable');return r.blob()}).then(b=>{if(generation!==imageGeneration)return null;const url=URL.createObjectURL(b);cache.set(key,url);return url}).catch(()=>null).finally(()=>{if(imagePending.get(pendingKey)===request)imagePending.delete(pendingKey)});
  imagePending.set(pendingKey,request);return request;
 }
 function releaseImageURLs(){
  imageGeneration++;
  for(const cache of [imageURLs,imagePreviewURLs]){for(const url of cache.values())URL.revokeObjectURL(url);cache.clear()}
  imagePending.clear();
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
    await api(boxPath(selected)+'/messages','POST',{'Idempotency-Key':crypto.randomUUID()},{text:'Answer to "'+message.question.text+'": '+selectedChoices.join(', '),parentMessageId:message.id});
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
 function bubble(box,message,readOnly=false){
  const row=document.createElement('div');
  if(message.direction==='system'){row.className='msg system';row.append(Object.assign(document.createElement('span'),{className:'text',textContent:message.text}));return row}
  const mine=message.direction==='user';
  row.className='msg '+(mine?'user':'agent')+(message.state==='silent'?' note':'')+(message.state==='streaming'?' streaming':'');
  if(message.pairAuthor){const author=document.createElement('span');author.className='agent-origin';author.textContent=message.pairAuthor;row.append(author)}
  if(message.direction==='box'){const origin=document.createElement('span');origin.className='agent-origin';origin.textContent='From '+(boxes.get(message.senderBoxId)?.name||'agent box');row.append(origin)}
  if(!readOnly&&message.parentMessageId){const parent=(box.messages||[]).find(value=>value.id===message.parentMessageId),quote=document.createElement('button');quote.type='button';quote.className='msg-parent';quote.textContent=(parent?messageAuthor(parent)+': ':'')+(parent?.question?.text||parent?.text||'Earlier message');quote.title='Show the full thread';quote.onclick=()=>void openThread(message.threadId||message.parentMessageId);row.append(quote)}
  if(message.state==='silent'){const label=document.createElement('span');label.className='note-label';label.textContent='Note · not sent to the agent';row.append(label)}
  if(message.text.startsWith('Forwarded from ')){const mark=document.createElement('span');mark.className='fwd-mark';const end=message.text.indexOf(':\n');mark.textContent=end>0?message.text.slice(0,end+1):'Forwarded';row.append(mark)}
  const text=document.createElement('div');text.className='text';
  const body=message.text.startsWith('Forwarded from ')&&message.text.indexOf(':\n')>0?message.text.slice(message.text.indexOf(':\n')+2):message.text;
  renderRichText(text,body);
  row.append(text);
  const gallery=messageMediaGallery(message);
  for(const [index,image] of (message.images||[]).entries()){
   const label='Attachment '+image.number,alt=label+' from '+message.direction;
   const kind=mediaKind(image.mediaType,'');
   if(kind==='video'||kind==='audio'){
    // The chip carries no media of its own and the viewer streams from the
    // authenticated endpoint, so never buffer a whole clip into a blob here:
    // a 100 MiB video would download on every transcript render and a slow
    // link would time out and drop the attachment from the conversation.
    row.append(mediaButton(mediaEndpoint(message.id,image.id),{kind,alt,label,gallery,index}));
    continue;
   }
   const btn=mediaButton('',{kind,alt,label,gallery,index});row.append(btn);
   imageURL(message.id,image.id,true).then(url=>{if(!btn.isConnected)return;const frame=btn.querySelector('.media-preview'),img=frame?.querySelector('img'),placeholder=frame?.querySelector('.media-preview-placeholder');if(url&&img)img.src=url;else if(placeholder){placeholder.textContent='Preview unavailable';frame.classList.add('failed')}});
  }
  const form=readOnly?null:questionForm(box,message);if(form)row.append(form);
  const meta=document.createElement('span');meta.className='meta';
  const threadSize=readOnly?0:(box.messages||[]).filter(value=>value.threadId&&value.threadId===message.threadId).length;if(message.threadId&&threadSize>1){const thread=document.createElement('button');thread.type='button';thread.className='msg-thread';thread.textContent=threadSize+' in thread';thread.onclick=()=>void openThread(message.threadId);meta.append(thread)}
  meta.append(Object.assign(document.createElement('time'),{textContent:new Date(message.createdAt).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'})}));
  if(mine&&!readOnly&&message.state!=='silent'){
   const ticks=document.createElement('span');ticks.className='ticks'+(message.state==='failed'||message.state==='ambiguous'?' failed':'');
   ticks.title=stateTicks[message.state]||'';
   const icon=stateIconName[message.state];
   if(icon)ticks.append(lucide(icon));
   if(message.state==='failed'||message.state==='ambiguous')ticks.append(document.createTextNode(message.state==='failed'?'failed':'unconfirmed'));
   meta.append(ticks);
  }
  // Keep direct reply and the menu available on touch at the message's top right.
  if(readOnly){row.append(meta);return row}
  const actions=document.createElement('div');actions.className='msg-actions';
  const replyShortcut=document.createElement('button');replyShortcut.type='button';replyShortcut.className='msg-reply';replyShortcut.setAttribute('aria-label','Reply to message');replyShortcut.title='Reply';replyShortcut.append(lucide('reply'));
  replyShortcut.onclick=()=>{closeAllMsgActions();setReply(message)};
  const toggle=document.createElement('button');toggle.type='button';toggle.className='msg-more';toggle.setAttribute('aria-label','Message actions');toggle.setAttribute('aria-expanded','false');toggle.append(lucide('chevron-down'));
  const menu=document.createElement('div');menu.className='msg-actions-menu';menu.hidden=true;
  const copy=document.createElement('button');copy.type='button';copy.append(lucide('copy'),Object.assign(document.createElement('span'),{textContent:'Copy'}));
  copy.onclick=async()=>{closeAllMsgActions();try{await navigator.clipboard.writeText(message.question?message.question.text:message.text);toast('Message copied.')}catch{toast('Copy is unavailable here.')}};
  const forward=document.createElement('button');forward.type='button';forward.append(lucide('forward'),Object.assign(document.createElement('span'),{textContent:'Forward…'}));
  forward.onclick=()=>{closeAllMsgActions();openForwardMenu(toggle,message)};
  const reply=document.createElement('button');reply.type='button';reply.append(lucide('reply'),Object.assign(document.createElement('span'),{textContent:'Reply'}));reply.onclick=()=>{closeAllMsgActions();setReply(message)};
  menu.append(reply,copy,forward);
  if(mine&&message.state==='ambiguous'){
   const inspect=document.createElement('button');inspect.type='button';inspect.append(lucide('help'),Object.assign(document.createElement('span'),{textContent:'Check delivery in TMUX'}));
   inspect.onclick=()=>{closeAllMsgActions();void openTakeover('tmux',box.id)};
   menu.append(inspect);
  }
  toggle.onclick=event=>{event.stopPropagation();const willOpen=menu.hidden;closeAllMsgActions();if(willOpen)openMsgActions(menu,toggle)};
  row.oncontextmenu=event=>{if(event.target.closest('a,button,input,textarea,video,audio'))return;event.preventDefault();closeAllMsgActions();openMsgActions(menu,toggle,{x:event.clientX,y:event.clientY})};
  actions.append(replyShortcut,toggle,menu);row.append(meta,actions);
  return row;
 }
 const isMCPActivity=message=>message.direction==='system'&&message.text.startsWith('MCP · ');
 function mcpCallDisplay(message){
  const parts=message.text.slice(6).split(' · '),tool=parts[0],contact=parts.includes('contact');
  let label=tool.replaceAll('_',' ');
  label=label.charAt(0).toUpperCase()+label.slice(1);
  if(tool==='chat_message')label=contact?'Message to contact':'Message to owner';
  else if(tool==='chat_ask')label=contact?'Question to contact':'Question to owner';
  else if(tool==='heartbeat'&&parts.includes('start'))label='Start heartbeat';
  else if(tool==='heartbeat'&&parts.includes('stop'))label='Stop heartbeat';
  const icon=tool.startsWith('chat_')?'message-square':tool.includes('screenshot')||tool==='capture_window'?'camera':tool.includes('contact')?'users':tool==='heartbeat'?'activity':'terminal';
  return {tool,label,icon,failed:parts.includes('failed')};
 }
 function mcpCallGroup(messages){
  const group=document.createElement('div');group.className='mcp-call-group';
  const toggle=document.createElement('button');toggle.type='button';toggle.className='mcp-call-toggle';
  const label=document.createElement('span');label.className='mcp-call-label';label.textContent='MCP Calls';
  const count=document.createElement('span');count.className='mcp-call-count';count.textContent=String(messages.length);
  const failed=messages.filter(message=>message.text.endsWith(' · failed')).length;
  const failures=document.createElement('span');failures.className='mcp-call-failures';failures.textContent=failed+' failed';failures.hidden=!failed;
  toggle.append(label,count,failures,lucide('chevron-down'));
  const list=document.createElement('div');list.className='mcp-call-list';list.id='mcp-calls-'+messages[0].id;
  toggle.setAttribute('aria-controls',list.id);
  for(const message of messages){
   const display=mcpCallDisplay(message);
   const row=document.createElement('div');row.className='mcp-call-item';row.title=message.text;row.dataset.tool=display.tool;
   const icon=document.createElement('span');icon.className='mcp-call-icon';icon.append(lucide(display.icon));
   const text=document.createElement('span');text.className='mcp-call-text';text.textContent=display.label;
   if(display.failed){const failure=document.createElement('span');failure.className='mcp-call-failed';failure.textContent='Failed';text.append(failure)}
   const time=document.createElement('time');time.textContent=new Date(message.createdAt).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'});
   row.append(icon,text,time);list.append(row);
  }
  const setOpen=open=>{toggle.setAttribute('aria-expanded',String(open));list.hidden=!open;for(const message of messages){if(open)expandedMCPEvents.add(message.id);else expandedMCPEvents.delete(message.id)}};
  setOpen(messages.some(message=>expandedMCPEvents.has(message.id)));
  toggle.onclick=()=>setOpen(toggle.getAttribute('aria-expanded')!=='true');
  group.append(toggle,list);return group;
 }
 function messageAuthor(message){return message.direction==='user'?'You':message.direction==='box'?(boxes.get(message.senderBoxId)?.name||'Agent box'):'Agent'}
 function setReply(message){replyingTo=message;replyPreview.hidden=false;$('#reply-preview-text').textContent=messageAuthor(message)+': '+(message.question?.text||message.text||(message.images?.length?'Image':'Message'));inputEl.focus()}
 function cancelReply(){replyingTo=null;replyPreview.hidden=true;$('#reply-preview-text').textContent=''}
 $('#reply-cancel').onclick=cancelReply;
 let openThreadID='';
 $('#thread-close').onclick=()=>{openThreadID='';threadPanel.hidden=true;threadMessages.replaceChildren()};
 async function openThread(threadID){
  if(!selected||!threadID)return;openThreadID=threadID;threadPanel.hidden=false;applyThreadWidth();threadMessages.replaceChildren(mk('p','Loading thread…'));
  try{const result=await chatHistory(boxPath(selected)+'/messages?limit=100&threadId='+encodeURIComponent(threadID)),box=boxes.get(selected);threadMessages.replaceChildren();$('#thread-origin').textContent=(box?.name||'Box')+' · '+(box?.defaultAgent||'agent');$('#thread-count').textContent=result.messages.length+' message'+(result.messages.length===1?'':'s');for(const message of result.messages)threadMessages.append(bubble({...box,messages:result.messages},message));const taskID=result.messages.find(message=>message.taskId)?.taskId;if(taskID)void api('/v1/tasks/'+encodeURIComponent(taskID)).then(task=>{if(openThreadID===threadID&&task?.agent)$('#thread-origin').textContent=(box?.name||'Box')+' · '+task.agent}).catch(()=>{})}
  catch(e){threadMessages.replaceChildren(mk('p',e.message))}
 }
 $('#thread-composer').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,text=form.elements.text.value.trim(),button=form.querySelector('button');if(!selected||!openThreadID||!text)return;if(boxes.get(selected)?.state!=='running'){statusEl.textContent='Wait for this box to be running before sending.';return}button.disabled=true;
  try{await api(boxPath(selected)+'/messages','POST',{'Idempotency-Key':crypto.randomUUID()},{text,parentMessageId:openThreadID});form.reset();await refreshMessages(true);await openThread(openThreadID)}
  catch(e){statusEl.textContent=e.message}finally{button.disabled=boxes.get(selected)?.state!=='running'}
 };
 function closeAllMsgActions(){for(const menu of document.querySelectorAll('.msg-actions-menu')){menu.hidden=true;menu._contextPoint=null}for(const toggle of document.querySelectorAll('.msg-more'))toggle.setAttribute('aria-expanded','false')}
 function openMsgActions(menu,toggle,point=null){menu._contextPoint=point;menu.hidden=false;toggle.setAttribute('aria-expanded','true');placeMsgActions(menu,toggle)}
 function placeMsgActions(menu,toggle){
  const scroller=toggle.closest('#chat-messages,#thread-messages');
  if(!scroller)return;
  const clip=scroller.getBoundingClientRect(),viewport=window.visualViewport;
  const top=Math.max(8,clip.top,viewport?.offsetTop||0),bottom=Math.min(innerHeight-8,clip.bottom,(viewport?.offsetTop||0)+(viewport?.height||innerHeight));
  const left=Math.max(8,clip.left,viewport?.offsetLeft||0),right=Math.min(innerWidth-8,clip.right,(viewport?.offsetLeft||0)+(viewport?.width||innerWidth));
  const anchor=toggle.getBoundingClientRect(),point=menu._contextPoint;
  if(anchor.bottom<top||anchor.top>bottom){closeAllMsgActions();return}
  menu.style.left='0';menu.style.top='0';menu.style.maxHeight='none';
  const width=menu.getBoundingClientRect().width,height=menu.getBoundingClientRect().height;
  const downY=point?point.y+2:anchor.bottom+4,upY=point?point.y-2:anchor.top-4;
  const below=Math.max(0,bottom-downY),above=Math.max(0,upY-top),up=height>below&&above>below;
  menu.style.maxHeight=Math.max(0,up?above:below)+'px';
  menu.dataset.placement=up?'up':'down';
  const origin=menu.getBoundingClientRect(),visibleHeight=origin.height;
  const targetX=Math.max(left,Math.min(point?point.x:anchor.right-width,right-width));
  const targetY=Math.max(top,Math.min(up?upY-visibleHeight:downY,bottom-visibleHeight));
  menu.style.left=Math.round(targetX-origin.left)+'px';
  menu.style.top=Math.round(targetY-origin.top)+'px';
 }
 document.addEventListener('click',event=>{if(!event.target.closest('.msg-actions'))closeAllMsgActions()});
 function repositionMsgActions(){for(const menu of document.querySelectorAll('.msg-actions-menu:not([hidden])'))placeMsgActions(menu,menu.parentElement.querySelector('.msg-more'))}
 document.addEventListener('scroll',repositionMsgActions,true);
 addEventListener('resize',repositionMsgActions);
 window.visualViewport?.addEventListener('resize',repositionMsgActions);
 function scrollMessagesToBottom(){
  messagesEl.scrollTop=messagesEl.scrollHeight;
  requestAnimationFrame(()=>{messagesEl.scrollTop=messagesEl.scrollHeight});
 }
 newMessagesBtn.onclick=()=>{
  stickToBottom=true;newMessagesBtn.hidden=true;
  const key=selected||selectedPair&&'pair:'+selectedPair;
  if(key){scrollMemory.delete(key);followMemory.set(key,true)}
  scrollMessagesToBottom();
  if(selected)applySeen(selected);else if(selectedPair)applyPairSeen(pairs.get(selectedPair));
  renderRows();
 };
 // A chat opens at its newest message and keeps following output until the
 // reader scrolls away; scrolling back to the bottom resumes following.
 messagesEl.addEventListener('scroll',()=>{
  if(restoringTranscript)return;
  const key=selected&&messagesEl.dataset.box===selected?selected:selectedPair&&messagesEl.dataset.pair===selectedPair?'pair:'+selectedPair:'';
  if(!key)return;
  const wasFollowing=stickToBottom;
  stickToBottom=messagesEl.scrollHeight-messagesEl.scrollTop-messagesEl.clientHeight<120;
  scrollMemory.set(key,messagesEl.scrollTop);followMemory.set(key,stickToBottom);
  if(stickToBottom){
   newMessagesBtn.hidden=true;
   if(!wasFollowing){if(selected)applySeen(selected);else if(selectedPair)applyPairSeen(pairs.get(selectedPair));renderRows()}
  }
 });
 function renderMessages(box){
  if(!box||box.id!==selected)return;
  if(tvPreviewBox&&tvPreviewBox!==box.id)hideTvPreview();
  const follow=stickToBottom;
  messagesEl.replaceChildren();
  messagesEl.dataset.box=box.id;
  if(box.hasOlder){const older=document.createElement('button');older.type='button';older.className='load-older';older.textContent=box.historyLoading?'Loading older messages…':'Load older messages';older.disabled=!!box.historyLoading;older.onclick=()=>void loadOlderMessages(box.id);messagesEl.append(older)}
  let day='';
  const messages=box.messages||[];
  for(let index=0;index<messages.length;index++){
   const message=messages[index];
   const label=dayLabel(message.createdAt);
   if(label!==day){day=label;const sep=document.createElement('div');sep.className='day-sep';sep.textContent=day;messagesEl.append(sep)}
   if(isMCPActivity(message)){
    const calls=[message];
    while(index+1<messages.length&&isMCPActivity(messages[index+1])&&dayLabel(messages[index+1].createdAt)===label)calls.push(messages[++index]);
    messagesEl.append(mcpCallGroup(calls));
   }else messagesEl.append(bubble(box,message));
  }
  const pending=pendingSends.get(box.id);
  if(pending&&!(box.messages||[]).slice(pending.messageCount).some(m=>m.direction==='user'&&m.text===pending.text)){
   const row=bubble(box,{id:'pending',direction:'user',state:'delivering',text:pending.text||'📷 Image',createdAt:pending.at,images:[]});
   row.querySelector('.fwd')?.remove();messagesEl.append(row);
  }
  if(!(box.messages||[]).length&&!pending){const hint=document.createElement('p');hint.className='day-sep';hint.textContent='No messages yet — say hello to '+box.name;messagesEl.append(hint)}
  if(box.resumeCandidate){
   const card=document.createElement('div');card.className='codex-resume-card';
   const title=document.createElement('strong');title.textContent='Restore your '+agentLabel(box)+' conversation?';
   const detail=document.createElement('span');detail.textContent='Found the last active session saved before hibernation ('+new Date(box.resumeCandidate.lastActiveAt||box.resumeCandidate.startedAt).toLocaleString()+'). Restore its context in the visible terminal, or continue fresh.';
   const actions=document.createElement('div');actions.className='codex-resume-actions';
   for(const [choice,label] of [['restore','Restore session'],['fresh','Start fresh']]){
    const button=document.createElement('button');button.type='button';button.textContent=label;
    button.onclick=()=>void chooseAgentResume(box,choice,actions);actions.append(button);
   }
   card.append(title,detail,actions);messagesEl.append(card);
  }
  if(box.processing&&!box.streaming){
   const t=document.createElement('div');t.className='msg agent processing';
   const mini=document.createElement('span');mini.className='processing-mascot';new Mascot(mini,box.id);mini.querySelector('svg').__vboxMascot.jump('working','focused','busy');
   const dots=document.createElement('span');dots.className='typing-dots';
   for(let i=0;i<3;i++)dots.append(document.createElement('span'));
   const label=document.createElement('span');label.className='typing-label';label.textContent='agent is processing…';
   const tv=document.createElement('button');tv.type='button';tv.className='tv-button';tv.title='Hover or tap to preview; open it for Desktop/TMUX control';tv.setAttribute('aria-label','Preview the desktop and open Desktop or TMUX control');
   tv.append(tvIcon());
   tv.onmouseenter=()=>{if(!coarsePointer())showTvPreview(tv,box)};
   tv.onmouseleave=scheduleHideTvPreview;
   tv.onfocus=()=>{if(!coarsePointer())showTvPreview(tv,box)};
   tv.onblur=()=>{if(!coarsePointer())scheduleHideTvPreview()};
   tv.onclick=()=>{if(coarsePointer()){if(tvPreviewEl.hidden||tvPreviewBox!==box.id)showTvPreview(tv,box);else hideTvPreview();return}void openBoxControl(box,'desktop')};
   t.append(mini,dots,label,tv);messagesEl.append(t);
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
 async function loadBoxes(force=false){
  if(boxesPending)return boxesPending;
  boxesPending=fetchBoxes(force).finally(()=>{boxesPending=null});return boxesPending;
 }
 async function fetchBoxes(force){
  const values=await api('/v1/logical-boxes');
  const current=new Map();const alive=new Set();
  for(const b of values||[]){const old=boxes.get(b.id);alive.add(b.id);if(old&&(old.state!==b.state||old.assignmentGeneration!==b.assignmentGeneration)){resumeChecks.delete(b.id);old.resumeCandidate=null;old.resumeCheckPending=false}current.set(b.id,Object.assign(old||{messages:[],historyLoaded:false,hasOlder:false,historyLoading:false},b))}
  for(const id of [...boxes.keys()])if(!alive.has(id)){
   const cached=avatarCache.get(id);if(cached?.url)URL.revokeObjectURL(cached.url);
   for(const draft of attachmentDrafts.get(id)||[])URL.revokeObjectURL(draft.url);
   boxes.delete(id);resumeChecks.delete(id);avatarCache.delete(id);previewFetched.delete(id);tvReplayCache.delete(id);attachmentDrafts.delete(id);
  }
  for(const [id,b] of current)boxes.set(id,b);
  if(selected&&!boxes.has(selected)){selected='';restoringTranscript=false;newMessagesBtn.hidden=true;selectedUsageProfile=null;chatUsageRequest++;renderChatUsage();lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false}
  await loadPreviews(force);
  if(owner)try{await loadPairs()}catch(e){if(selectedPair)statusEl.textContent='Could not refresh box conversations: '+e.message}
  if(selected)applySeen(selected);
  renderRows();
  if(selected){renderHeader();renderInspect();if(owner)void loadChatUsageProfile(selected)}
 }
 async function loadPairs(){
  const values=await api('/v1/box-conversations');
  if(!Array.isArray(values))return;
  const alive=new Set();
  for(const value of values){const key=pairKey(value);alive.add(key);pairs.set(key,Object.assign(pairs.get(key)||{messages:[]},value))}
  for(const key of pairs.keys())if(!alive.has(key))pairs.delete(key);
  if(selectedPair&&!pairs.has(selectedPair)){
   selectedPair='';restoringTranscript=false;newMessagesBtn.hidden=true;lastSignature='';viewEpoch++;appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;
  }
 }
 async function loadPreviews(force){
  await Promise.allSettled([...boxes.keys()].map(async id=>{
   if(id===selected)return;// open conversation refreshes itself
   const previousFetch=previewFetched.get(id)||0;
   if(!force&&Date.now()-previousFetch<30000)return;
   const history=await chatHistory(boxPath(id)+'/messages?limit=20');
   const box=boxes.get(id);
   // The box may have been opened while the preview was in flight. Keep its
   // full transcript, but refresh the newest messages so previously opened
   // chats still get unread badges after the user switches away.
   if(!box||id===selected||(previewFetched.get(id)||0)!==previousFetch)return;
   applyBusyState(box,history);
   if(box.historyLoaded){
    const merged=new Map((box.messages||[]).map(message=>[message.id,message]));
    for(const message of history.messages||[])merged.set(message.id,message);
    box.messages=[...merged.values()].sort((a,b)=>new Date(a.createdAt)-new Date(b.createdAt)||a.id.localeCompare(b.id));
   }else{box.messages=history.messages||[];box.hasOlder=false}
   previewFetched.set(id,Date.now());
   summarize(id);
  }));
 }
 function applyBusyState(box,history){
  if(history.busy===null){delete box.agentBusy;delete box.agentBusySince;return}
  box.agentBusy=history.busy;box.agentBusySince=history.busySince||'';
 }
 function applySeen(id){
  const box=boxes.get(id);if(!box)return;
  if(id===selected&&(!stickToBottom||!conversationVisible())){summarize(id);return}
  const last=(box.messages||[]).filter(m=>m.direction!=='user').pop();
  if(last&&new Date(last.createdAt).getTime()>(seen[id]?new Date(seen[id]).getTime():0)){seen[id]=last.createdAt;saveSeen()}
  summarize(id);
 }
 let headerAvatarKey='';
 function renderHeader(){
  const box=boxes.get(selected);if(!box)return;
  $('#chat-header-name').textContent=box.name;
  const state=mk('span');state.className=box.state==='running'?'running':'';
  const agent=mk('span',(box.defaultAgent||'agent')+' · ');agent.className='chat-header-agent';
  state.append(agent,document.createTextNode(box.state+(box.streaming?' · streaming…':box.processing?' · processing…':'')));
  $('#chat-header-state').replaceChildren(state);
  inputEl.placeholder='Message '+box.name+'…';
  const key=box.id;
  if(key!==headerAvatarKey){headerAvatarKey=key;$('#chat-header-avatar').replaceChildren(avatarNode(box,false,true))}
  else syncAvatarMascot($('#chat-header-avatar .avatar'),box);
  $('#chat-wake').hidden=!canWakeBox(box);
  $('#chat-wake').disabled=wakingBoxes.has(box.id);
  $('#chat-clear-context').disabled=box.state!=='running'||(box.defaultAgent||'shell')==='shell';
  $('#thread-composer button').disabled=box.state!=='running';
  updateSendState();
  $('#chat-workspace').href='/boxes/'+encodeURIComponent(box.id);
  $('#chat-more-menu [data-action="workspace"]').href='/boxes/'+encodeURIComponent(box.id);
  $('#chat-more-menu [data-action="clear"]').disabled=$('#chat-clear-context').disabled;
  updateBanner();
 }
 // One banner for the two things that silently confuse people: a dropped
 // connection, and an agent that looks stuck on the last request.
 const chatBanner=$('#chat-banner');
 let reconnecting=false,bannerShown='';
 function setBanner(text){if(text===bannerShown)return;bannerShown=text;chatBanner.textContent=text;chatBanner.hidden=!text}
 function updateBanner(){
  if(reconnecting)return setBanner('Reconnecting to the controller…');
  const box=boxes.get(selected);if(!box)return setBanner('');
  if(canWakeBox(box))return setBanner('This box is '+box.state+'. Wake it to chat again. Files and chat history are saved; the agent starts a fresh live session.');
  const last=[...(box.messages||[])].reverse().find(m=>m.direction==='user');
  const started=box.agentBusySince||(last&&(last.updatedAt||last.createdAt));
  const elapsed=started?Date.now()-new Date(started).getTime():0;
  const stalled=box.processing&&elapsed>5*60*1000;
  setBanner(stalled?'Agent has been processing for '+Math.round(elapsed/60000)+' min — it may be stalled.':'');
 }
 async function refreshMessages(force){
  if(!selected)return;
  const id=selected,box=boxes.get(id);if(!box)return;
  const epoch=viewEpoch;
  const history=await chatHistory(boxPath(id)+'/messages?limit=50');
  // Drop a response that arrives after the user moved to another box.
  if(epoch!==viewEpoch||selected!==id||boxes.get(id)!==box)return;
  applyBusyState(box,history);
  const latest=history.messages||[];
  const known=box.historyLoaded?new Set((box.messages||[]).map(message=>message.id)):null;
  const hasNewReply=known&&latest.some(message=>message.direction!=='user'&&!known.has(message.id));
  if(box.historyLoaded){
   const merged=new Map((box.messages||[]).map(message=>[message.id,message]));
   for(const message of latest)merged.set(message.id,message);
   box.messages=[...merged.values()].sort((a,b)=>new Date(a.createdAt)-new Date(b.createdAt)||a.id.localeCompare(b.id));
  }else{box.messages=latest;box.historyLoaded=true;box.hasOlder=latest.length===50}
  previewFetched.set(box.id,Date.now());
  // The processing bubble must use the state of this response, not the
  // previous poll's state (which can leave it beneath an agent reply).
  applySeen(selected);
  const signature=box.messages.map(m=>m.id+m.updatedAt+m.state).join('|')+'|'+box.processing+'|'+box.streaming;
  renderHeader();
  if(force||signature!==lastSignature){lastSignature=signature;renderMessages(box)}
  if(hasNewReply&&!stickToBottom)newMessagesBtn.hidden=false;
  renderRows();renderInspect();
  if(owner&&inspectOpen&&(force||hasNewReply))void loadInspectAttachmentStorage(box);
  if(owner&&box.state==='running'&&['codex','claude','opencode'].includes(box.defaultAgent))void refreshAgentResume(box);
 }
 function renderPairMessages(pair){
  const follow=stickToBottom;
  messagesEl.replaceChildren();delete messagesEl.dataset.box;messagesEl.dataset.pair=pairKey(pair);
  let day='';
  for(const message of pair.messages||[]){
   const label=dayLabel(message.createdAt);
   if(label!==day){day=label;const sep=mk('div',day);sep.className='day-sep';messagesEl.append(sep)}
   const fromB=message.senderBoxId===pair.boxBId;
   messagesEl.append(bubble(pair,{...message,direction:fromB?'user':'agent',pairAuthor:fromB?pair.boxBName:pair.boxAName},true));
  }
  if(!(pair.messages||[]).length){const empty=mk('p','No direct messages between these boxes yet.');empty.className='day-sep';messagesEl.append(empty)}
  statusEl.textContent='Read only · '+(pair.messages||[]).length+' messages'+((pair.messages||[]).length===500?' (latest 500)':'');
  if(follow)scrollMessagesToBottom();
 }
 async function refreshPairMessages(force=false){
  const key=selectedPair,pair=pairs.get(key);if(!pair)return;
  const epoch=viewEpoch;
  const messages=await api('/v1/box-conversations/'+encodeURIComponent(pair.boxAId)+'/'+encodeURIComponent(pair.boxBId)+'/messages');
  if(epoch!==viewEpoch||selectedPair!==key||pairs.get(key)!==pair)return;
  const known=new Set((pair.messages||[]).map(message=>message.id));
  pair.messages=Array.isArray(messages)?messages:[];
  const last=pair.messages.at(-1);
  if(last){pair.lastAt=last.createdAt;pair.lastText=last.text}
  const signature=pair.messages.map(message=>message.id+message.updatedAt+message.state).join('|');
  if(force||signature!==lastSignature){lastSignature=signature;renderPairMessages(pair)}
  if(known.size&&pair.messages.some(message=>!known.has(message.id))&&!stickToBottom)newMessagesBtn.hidden=false;
  applyPairSeen(pair);
  renderRows();
 }
 async function openPair(key){
  const pair=pairs.get(key);if(!pair)return;
  const epoch=++viewEpoch;restoringTranscript=true;newMessagesBtn.hidden=true;
  selected='';selectedPair=key;selectedUsageProfile=null;chatUsageRequest++;renderChatUsage();lastSignature='';cancelReply();hideComposerPicker();closeInspect();closeForwardMenu();closeTakeover();
  openThreadID='';threadPanel.hidden=true;threadMessages.replaceChildren();
  messagesEl.replaceChildren();delete messagesEl.dataset.box;messagesEl.dataset.pair=key;
  // Box-to-box chats are read-only activity logs: opening one starts at its
  // latest message, even if the reader scrolled up on an earlier visit.
  scrollMemory.delete('pair:'+key);followMemory.set('pair:'+key,true);stickToBottom=true;
  $('#chat-conversation').classList.add('pair-view');
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;appEl.classList.add('in-chat');
  $('#chat-header-name').textContent=pair.boxAName+' ↔ '+pair.boxBName;
  $('#chat-header-state').textContent='Direct messages between boxes · read only';
  $('#chat-header-avatar').replaceChildren(Object.assign(mk('span','↔'),{className:'pair-avatar'}));headerAvatarKey='';
  setBanner('');statusEl.textContent='';renderRows();doodle('Loading messages…');
  try{await refreshPairMessages(true)}catch(e){if(selectedPair===key)statusEl.textContent=e.message}finally{if(selectedPair===key)doodle('')}
  if(epoch===viewEpoch&&selectedPair===key)requestAnimationFrame(()=>{
   if(epoch!==viewEpoch||selectedPair!==key)return;
   scrollMessagesToBottom();
   restoringTranscript=false;
  });
 }
 function agentLabel(box){return box.defaultAgent==='claude'?'Claude':box.defaultAgent==='opencode'?'OpenCode':'Codex'}
 async function refreshAgentResume(box){
  const prior=resumeChecks.get(box.id);
  // loadBoxes invalidates the check on wake. A history refresh after sending
  // must not force another blocking check; later periodic retries can run in
  // the background in case a saved session appeared after the first response.
  if(prior&&(prior.pending||Date.now()-prior.at<20000))return;
  const blockSend=!prior?.complete;
  const check={at:Date.now(),pending:true,complete:false};resumeChecks.set(box.id,check);
  if(blockSend){box.resumeCheckPending=true;if(selected===box.id)updateSendState()}
  try{
   const result=await api(boxPath(box.id)+'/agent-resume');
   if(resumeChecks.get(box.id)!==check)return;
   check.complete=true;
   const previous=box.resumeCandidate||null;
   box.resumeCandidate=result.candidate||null;
   if(selected===box.id){if(JSON.stringify(previous)!==JSON.stringify(box.resumeCandidate))renderMessages(box);updateSendState()}
  }catch(e){if(selected===box.id)statusEl.textContent='Could not check saved '+agentLabel(box)+' sessions: '+e.message}
  finally{check.pending=false;if(blockSend&&resumeChecks.get(box.id)===check){box.resumeCheckPending=false;if(selected===box.id)updateSendState()}}
 }
 async function chooseAgentResume(box,choice,actions){
  const candidate=box.resumeCandidate;if(!candidate)return;
  for(const button of actions.querySelectorAll('button'))button.disabled=true;
  try{
   await api(boxPath(box.id)+'/agent-resume','POST',{}, {choice,sessionId:candidate.sessionId,savedAt:candidate.savedAt});
   box.resumeCandidate=null;
   if(selected===box.id){renderMessages(box);updateSendState();void refreshMessages(true).catch(e=>{statusEl.textContent=e.message})}
   toast(choice==='restore'?'Saved '+agentLabel(box)+' conversation restored.':'Continuing with a fresh '+agentLabel(box)+' conversation.');
  }catch(e){statusEl.textContent=e.message;for(const button of actions.querySelectorAll('button'))button.disabled=false}
 }
 async function loadOlderMessages(id){
  const box=boxes.get(id);if(!box||selected!==id||!box.hasOlder||box.historyLoading||!box.messages?.length)return;
  const oldest=box.messages[0],height=messagesEl.scrollHeight,top=messagesEl.scrollTop;
  box.historyLoading=true;renderMessages(box);
  try{
   const query='?limit=50&before='+encodeURIComponent(oldest.createdAt)+'&beforeId='+encodeURIComponent(oldest.id);
   const older=await api(boxPath(id)+'/messages'+query);
   if(selected!==id||boxes.get(id)!==box)return;
   const existing=new Set(box.messages.map(message=>message.id));
   box.messages=[...(older||[]).filter(message=>!existing.has(message.id)),...box.messages];
   box.hasOlder=(older||[]).length===50;
   box.historyLoading=false;lastSignature='';renderMessages(box);
   requestAnimationFrame(()=>{messagesEl.scrollTop=top+messagesEl.scrollHeight-height});
  }catch(e){box.historyLoading=false;statusEl.textContent=e.message;renderMessages(box)}
 }
 async function openBox(id){
  if(!boxes.has(id))return;
  const epoch=++viewEpoch;restoringTranscript=true;newMessagesBtn.hidden=true;
  selectedPair='';$('#chat-conversation').classList.remove('pair-view');
  // Never show one box's transcript while another is loading: drop the old
  // messages (and any floating preview) before the new history arrives.
  if(messagesEl.dataset.box!==id){messagesEl.replaceChildren();delete messagesEl.dataset.pair;messagesEl.dataset.box=id;hideTvPreview()}
  if(selected!==id)cancelReply();selected=id;lastSignature='';hideComposerPicker();
  selectedUsageProfile=null;renderChatUsage();if(owner)void loadChatUsageProfile(id);
  // Restore where this box was left instead of always jumping to the bottom;
  // first-time opens (no memory) start at the newest message.
  const savedScroll=scrollMemory.get(id);
  stickToBottom=savedScroll==null||followMemory.get(id)!==false;
  const restoredDraft=inputDrafts[id]||'';
  if(inputEl.value!==restoredDraft){inputEl.value=restoredDraft;grow()}
  renderDrafts();
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;
  appEl.classList.add('in-chat');
  renderHeader();
  statusEl.textContent='';
  closeForwardMenu();
  closeTakeover();
  renderInspect();
  if(!(boxes.get(id).messages||[]).length)doodle('Loading messages…');
  try{await refreshMessages(true)}catch(e){statusEl.textContent=e.message}finally{doodle('')}
  if(epoch===viewEpoch&&selected===id)requestAnimationFrame(()=>{
   if(epoch!==viewEpoch||selected!==id)return;
   if(stickToBottom)scrollMessagesToBottom();else if(savedScroll!=null)messagesEl.scrollTop=savedScroll;
   restoringTranscript=false;
  });
  // Deliberately do not focus the composer: on phones that pops the keyboard
  // the moment a chat is opened. Focus follows an explicit tap.
 }

 /* ---------- composer ---------- */
 // Grow the composer with the text like WhatsApp, up to a viewport-aware cap so
 // it never eats the transcript on a phone.
 const maxComposerHeight=()=>Math.min(150,Math.max(96,innerHeight*0.35));
 function grow(){inputEl.style.height='auto';inputEl.style.height=Math.min(inputEl.scrollHeight,maxComposerHeight())+'px'}
 const composerPicker=$('#composer-picker'),mentionCache=new Map();
 let pickerItems=[],pickerIndex=0,pickerRange=null,pickerRequest=0,acceptingComposerSuggestion=false;
 function expandChatCommands(text){
  const commands=new Map(chatCommands.map(command=>[command.name,command.prompt]));
  // Expand each draft token once; saved prompt text remains literal.
  return text.replace(/(^|\s)\/([a-z0-9][a-z0-9_-]{0,39})(?=$|[^a-z0-9_-])/g,(match,prefix,name)=>commands.has(name)?prefix+commands.get(name):match);
 }
 function hideComposerPicker(){pickerRequest++;pickerItems=[];pickerRange=null;composerPicker.hidden=true;composerPicker.replaceChildren()}
 function composerToken(){
  const before=inputEl.value.slice(0,inputEl.selectionStart),match=/(^|\s)([\/@])([A-Za-z0-9._-]*)$/.exec(before);
  return match?{kind:match[2],query:match[3].toLowerCase(),start:before.length-match[2].length-match[3].length,end:before.length}:null;
 }
 async function mentionChoices(boxID){
  const cached=mentionCache.get(boxID);
  if(cached&&Date.now()-cached.at<30000)return cached.items;
  const contacts=await api(boxPath(boxID)+'/contacts');
  const items=(contacts||[]).filter(contact=>!contact.protected&&contact.contactState!=='deleting').map(contact=>({kind:'@',id:contact.contactBoxId,name:contact.contactName,detail:contact.contactState||''}));
  mentionCache.set(boxID,{at:Date.now(),items});return items;
 }
 function renderComposerPicker(items,token){
  pickerItems=items.slice(0,8);pickerIndex=0;pickerRange=token;
  composerPicker.replaceChildren();
  for(const [index,item] of pickerItems.entries()){
   const button=document.createElement('button');button.type='button';button.setAttribute('role','option');button.setAttribute('aria-selected',String(index===0));
   const title=document.createElement('strong');title.textContent=(item.kind==='/'?'/':'@')+item.name;
   const detail=document.createElement('span');detail.textContent=item.detail;
   button.append(title,detail);button.onmousedown=event=>event.preventDefault();button.onclick=()=>chooseComposerSuggestion(index);
   composerPicker.append(button);
  }
  composerPicker.hidden=!pickerItems.length;
 }
 async function updateComposerPicker(){
  const token=composerToken(),boxID=selected,request=++pickerRequest;
  if(!owner||!boxID||!token){hideComposerPicker();return}
  if(token.kind==='/'){
   renderComposerPicker(chatCommands.filter(command=>command.name.startsWith(token.query)).map(command=>({kind:'/',name:command.name,detail:command.prompt})),token);
   return;
  }
  try{
   const items=await mentionChoices(boxID);
   if(request!==pickerRequest||selected!==boxID||JSON.stringify(composerToken())!==JSON.stringify(token))return;
   renderComposerPicker(items.filter(item=>item.name.toLowerCase().startsWith(token.query)),token);
  }catch(e){if(request===pickerRequest){hideComposerPicker();statusEl.textContent=e.message}}
 }
 function chooseComposerSuggestion(index){
  const item=pickerItems[index],range=pickerRange;if(!item||!range)return;
  const insertion=item.kind==='/'?'/'+item.name:'@'+item.name+' ';
  inputEl.value=inputEl.value.slice(0,range.start)+insertion+inputEl.value.slice(range.end);
  const caret=range.start+insertion.length;hideComposerPicker();inputEl.focus();inputEl.setSelectionRange(caret,caret);
  acceptingComposerSuggestion=true;
  inputEl.dispatchEvent(new Event('input',{bubbles:true}));
 }
 function mentionedBoxIDs(text){
  if(!owner)return [];
  const names=new Map([...boxes.values()].filter(box=>box.id!==selected).map(box=>[box.name,box.id]));
  const ids=new Set();for(const match of text.matchAll(/(?:^|\s)@([A-Za-z0-9._-]{1,63})(?=$|[^A-Za-z0-9._-])/g)){
   const id=names.get(match[1]);if(id)ids.add(id);
  }
  return [...ids];
 }
 function updateSendState(){
  const drafts=attachmentDrafts.get(selected)||[];
  const hasContent=!!inputEl.value.trim()||drafts.length>0;
  const send=$('#send'),box=boxes.get(selected),running=box?.state==='running'&&!box?.resumeCandidate&&!box?.resumeCheckPending;send.disabled=!hasContent||!running;
  const count=drafts.length,label=count?'Send ('+count+' attachment'+(count===1?'':'s')+')':'Send';
  send.setAttribute('aria-label',label);
  send.title=running?label+(enterInsertsNewline()?'':' · Enter to send; Shift+Enter for a new line'):box?.resumeCandidate?'Choose whether to restore the saved '+agentLabel(box)+' session first.':box?.resumeCheckPending?'Checking for a saved conversation…':'Wait for this box to be running before sending';
  const reason=$('#send-blocked-reason');reason.hidden=!hasContent||running;reason.textContent=reason.hidden?'':send.title;
 }
 inputEl.addEventListener('input',()=>{grow();updateSendState();if(acceptingComposerSuggestion)acceptingComposerSuggestion=false;else void updateComposerPicker();if(!selected)return;inputDrafts[selected]=inputEl.value;clearTimeout(inputDraftTimer);inputDraftTimer=setTimeout(saveInputDrafts,250)});
 let composerHintShown=false;
 inputEl.addEventListener('focus',()=>{
  if(composerHintShown)return;composerHintShown=true;
  try{if(localStorage.getItem('vmbox.composerHint')==='1')return;localStorage.setItem('vmbox.composerHint','1')}catch{}
  statusEl.textContent=enterInsertsNewline()?'Tap Send to send; Enter starts a new line.':'Enter sends; Shift+Enter adds a new line.';
  setTimeout(()=>{if(/^(Enter sends|Tap Send)/.test(statusEl.textContent))statusEl.textContent=''},5000);
 });
 // On a phone or tablet the soft keyboard's Enter is the only convenient way to
 // start a new line, so it inserts a newline there; Send is the explicit button.
 // A hardware keyboard (hover + fine pointer) keeps Enter-to-send.
 const enterInsertsNewline=()=>matchMedia('(hover:none) and (pointer:coarse)').matches;
 inputEl.addEventListener('keydown',event=>{
  if(!composerPicker.hidden){
   if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();pickerIndex=(pickerIndex+(event.key==='ArrowDown'?1:-1)+pickerItems.length)%pickerItems.length;[...composerPicker.children].forEach((button,index)=>button.setAttribute('aria-selected',String(index===pickerIndex)));return}
   if(event.key==='Escape'){event.preventDefault();hideComposerPicker();return}
   if(event.key==='Tab'&&!event.shiftKey&&pickerRange?.kind==='/'){
    event.preventDefault();chooseComposerSuggestion(pickerIndex);
    if(!$('#send').disabled){hideComposerPicker();$('#send').focus()}
    return;
   }
   if(event.key==='Enter'&&!event.shiftKey){event.preventDefault();chooseComposerSuggestion(pickerIndex);return}
  }
  if(event.key!=='Enter'||event.shiftKey)return;
  if(enterInsertsNewline())return;
  event.preventDefault();composer.requestSubmit();
 });
 inputEl.addEventListener('click',()=>void updateComposerPicker());
 function renderDrafts(){
  const drafts=attachmentDrafts.get(selected)||[];
  draftsEl.hidden=!drafts.length;draftsEl.replaceChildren();
  for(const entry of drafts){
   const wrap=document.createElement('span');wrap.className='draft';
   const open=document.createElement('button');open.type='button';open.className='draft-open';
   open.setAttribute('aria-label','Inspect attachment '+entry.number);
   const kind=entry.kind||'image';
   if(kind==='video'||kind==='audio'){
    const el=document.createElement(kind);el.src=entry.url;el.muted=true;el.playsInline=true;el.preload='metadata';el.setAttribute('aria-hidden','true');open.append(el);
   }else{
    const img=document.createElement('img');img.src=entry.url;img.alt='Attachment '+entry.number;open.append(img);
   }
   open.onclick=()=>openMediaViewer([{url:entry.url,kind,alt:'Attachment '+entry.number,label:'Attachment '+entry.number}]);
   const remove=document.createElement('button');remove.type='button';remove.className='draft-remove';remove.textContent='×';remove.title='Remove attachment';
   remove.onclick=()=>{const remaining=drafts.filter(d=>d!==entry);URL.revokeObjectURL(entry.url);remaining.forEach((d,i)=>d.number=i+1);if(remaining.length)attachmentDrafts.set(selected,remaining);else attachmentDrafts.delete(selected);renderDrafts()};
   wrap.append(open,remove);draftsEl.append(wrap);
  }
  updateSendState();
 }
 async function uploadImages(files,{boxID=selected,statusTarget=statusEl}={}){
  if(!boxID)return 0;
  let uploaded=0;
  for(const file of files){
   if((attachmentDrafts.get(boxID)||[]).length>=8){statusTarget.textContent='Attach at most 8 files.';break}
   const isVideo=file.type==='video/mp4'||file.type==='video/webm';
   const isImage=['image/png','image/jpeg','image/gif'].includes(file.type);
   if(!isVideo&&!isImage){statusTarget.textContent='Choose PNG, JPEG, GIF, MP4 or WebM.';continue}
   const limitMiB=isVideo?100:25;
   if(file.size>limitMiB*1024*1024){statusTarget.textContent=(isVideo?'Videos':'Images')+' must be at most '+limitMiB+' MiB.';continue}
   try{
    const response=await fetch('/v1/run-once-images',{method:'POST',credentials:'same-origin',body:file,signal:AbortSignal.timeout(120000)});
    let result;try{result=await response.json()}catch{}
    if(!response.ok)throw Error(result?.error||'Upload failed.');
    // Re-read after the await: another picker/paste may have completed for the
    // same chat while this upload was in flight.
    const drafts=attachmentDrafts.get(boxID)||[];
    if(drafts.length>=8){statusTarget.textContent='Attach at most 8 files.';continue}
    drafts.push({id:result.id,number:drafts.length+1,url:URL.createObjectURL(file),kind:isVideo?'video':'image',mediaType:file.type});
    uploaded++;
    attachmentDrafts.set(boxID,drafts);
    if(selected===boxID)renderDrafts();
    if(owner&&inspectOpen&&selected===boxID)void loadInspectAttachmentStorage(boxes.get(boxID));
   }catch(e){statusTarget.textContent=e.message}
  }
  fileInput.value='';
  return uploaded;
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
  const boxID=selected,box=boxes.get(boxID);
  if(box?.resumeCheckPending){statusEl.textContent='Checking for a saved conversation…';updateSendState();return}
  if(box?.resumeCandidate){statusEl.textContent='Choose whether to restore the saved '+agentLabel(box)+' session first.';updateSendState();return}
  if(box?.state!=='running'){statusEl.textContent='Wait for this box to be running before sending.';updateSendState();return}
  const drafts=attachmentDrafts.get(boxID)||[];
  const draftText=inputEl.value,text=expandChatCommands(draftText),images=drafts.map(({id,number})=>({id,number})),replyTarget=replyingTo,mentionedBoxIds=mentionedBoxIDs(text);
  if(!text.trim()&&!images.length)return;
  if(mentionedBoxIds.length>8){statusEl.textContent='Mention at most eight boxes in one message.';return}
  hideComposerPicker();
  const fingerprint=text+'\n'+images.map(i=>i.id).join(',')+'\n'+(replyTarget?.id||'')+'\n'+mentionedBoxIds.join(',');
  if(fingerprint!==pendingFingerprint||!pendingKey){pendingKey=crypto.randomUUID();pendingFingerprint=fingerprint}
  const send=$('#send');send.disabled=true;
  const showPending=(box.defaultAgent||'shell')!=='shell'&&!/^\/silent(?:\s|$)/.test(text);
  const pendingAt=performance.now();
  // Clear the composer the moment the message is handed off so typing can
  // continue immediately. The text and drafts are restored if the send fails.
  inputEl.value='';grow();
  if(inputDrafts[boxID]){delete inputDrafts[boxID];saveInputDrafts()}
  const sentDrafts=drafts;attachmentDrafts.delete(boxID);renderDrafts();
  // Sending is an explicit jump to the live edge of the conversation. Even
  // if the reader had scrolled up, reveal their outgoing bubble and follow the
  // reply from here; passive inbound refreshes still preserve a scrolled-up
  // reading position.
  stickToBottom=true;scrollMemory.delete(boxID);followMemory.set(boxID,true);newMessagesBtn.hidden=true;
  // Let the cleared composer paint before rebuilding a long transcript for
  // the pending bubble. The network request can start without that render.
  if(showPending){
   const pending={messageCount:(box.messages||[]).length,text,at:new Date().toISOString()};
   pendingSends.set(boxID,pending);
   requestAnimationFrame(()=>setTimeout(()=>{
    if(pendingSends.get(boxID)!==pending)return;
    summarize(boxID);renderRows();
    if(selected===boxID){renderHeader();renderMessages(box)}
    document.querySelectorAll('[data-avatar="'+CSS.escape(boxID)+'"] svg.vbox-mascot').forEach(svg=>void svg.__vboxMascot?.comet());
   },0));
  }
  let settled=false;
  try{
   const result=await api(boxPath(boxID)+'/messages','POST',{'Idempotency-Key':pendingKey},{text,images,parentMessageId:replyTarget?.id||'',mentionedBoxIds});
   for(const d of sentDrafts)URL.revokeObjectURL(d.url);
   pendingKey='';pendingFingerprint='';if(replyingTo?.id===replyTarget?.id)cancelReply();
   statusEl.textContent=result?.message?.state==='silent'?'Note saved without waking the agent.':'';
   if(showPending&&result?.message?.state!=='silent')await new Promise(resolve=>setTimeout(resolve,Math.max(0,350-(performance.now()-pendingAt))));
   pendingSends.delete(boxID);
   if(selected===boxID)await refreshMessages(true);
   else{summarize(boxID);renderRows()}
   settled=true;
  }catch(e){
   statusEl.textContent=e.message;
   if(!inputEl.value)inputEl.value=draftText;if(!replyingTo&&replyTarget)setReply(replyTarget);
   inputDrafts[boxID]=inputEl.value;saveInputDrafts();
   // Restore the drafts handed to the failed send alongside anything the user
   // attached meanwhile, so no blob URL is lost or leaked.
   if(sentDrafts.length){const restored=[...sentDrafts,...(attachmentDrafts.get(boxID)||[])].map((draft,index)=>({...draft,number:index+1}));attachmentDrafts.set(boxID,restored);if(selected===boxID){renderDrafts();grow()}}
  }
  finally{
   if(pendingSends.delete(boxID)||(showPending&&!settled)){
    summarize(boxID);
    if(selected===boxID){renderHeader();renderMessages(box)}
    renderRows();
   }
   updateSendState();
  }
 };
 $('#chat-back').onclick=()=>{appEl.classList.remove('in-chat');history.replaceState(null,'',location.pathname)};
 // Swipe in from the left edge on a phone to pull the chat list back out.
 let listSwipe=null;
 appEl.addEventListener('touchstart',event=>{
  if(event.touches.length!==1)return;
  const touch=event.touches[0];
  listSwipe=touch.clientX<=48?{x:touch.clientX,y:touch.clientY}:null;
 },{passive:true});
 appEl.addEventListener('touchmove',event=>{
  if(!listSwipe)return;
  const touch=event.touches[0];if(!touch)return;
  if(touch.clientX-listSwipe.x>60&&Math.abs(touch.clientY-listSwipe.y)<50){
   listSwipe=null;
   if(appEl.classList.contains('in-chat')){appEl.classList.remove('in-chat');history.replaceState(null,'',location.pathname)}
  }
 },{passive:true});
 appEl.addEventListener('touchend',()=>{listSwipe=null},{passive:true});
 appEl.addEventListener('touchcancel',()=>{listSwipe=null},{passive:true});
 addEventListener('hashchange',()=>{
  const params=new URLSearchParams(location.hash.slice(1)),pair=params.get('pair');
  if(pair){const match=[...pairs.keys()].find(key=>key===pair||key.split('/').reverse().join('/')===pair);if(match&&match!==selectedPair)void openPair(match);return}
  const id=params.get('box');if(id&&id!==selected&&boxes.has(id))void openBox(id);
 });

 /* ---------- takeover popup: VNC/TMUX control ---------- */
 const takeover=$('#takeover'),takeoverScreen=$('#takeover-screen'),takeoverControls=$('#takeover-controls'),takeoverStatus=$('#takeover-status');
 const boxViewerMetrics=new Map();
 let takeoverDispose=null,takeoverKind='';
 async function openTakeover(kind,boxID=selected){
  const box=boxes.get(boxID);if(!box)return;
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
 async function openBoxControl(box,kind){
  hideTvPreview();
  if(selected!==box.id){history.replaceState(null,'',location.pathname+'#box='+encodeURIComponent(box.id));await openBox(box.id)}
  await openTakeover(kind,box.id);
 }
 function closeTakeover(){
  takeoverDispose?.();takeoverDispose=null;takeoverKind='';
  takeover.hidden=true;takeoverScreen.replaceChildren();takeoverControls.replaceChildren();
 }
 $('#chat-control').onclick=()=>void openTakeover('desktop');
 $('#takeover-close').onclick=closeTakeover;
 $('#takeover-backdrop').onclick=closeTakeover;
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

 /* ---------- clear agent context ---------- */
 $('#chat-clear-context').onclick=async()=>{
  const box=boxes.get(selected);if(!box)return;
  if(box.state!=='running'){statusEl.textContent=box.name+' is '+box.state+'; resume it before clearing context.';return}
  if(!confirm('Clear the active '+(box.defaultAgent||'agent')+' context for "'+box.name+'"? Chat history stays visible, but the next message starts without the agent\'s prior context.'))return;
  const btn=$('#chat-clear-context');btn.disabled=true;statusEl.textContent='Clearing agent context…';
  try{
   const result=await api(boxPath(box.id)+'/messages/clear-context','POST',{'Idempotency-Key':crypto.randomUUID()},{},45000);
   statusEl.textContent='Context cleared. The next message continues in the same visible terminal with fresh context.';
   toast('Context cleared for '+result.agent+'.');
   if(selected===box.id)try{await refreshMessages(true)}catch(e){statusEl.textContent='Context cleared; chat refresh failed: '+e.message}
  }catch(e){statusEl.textContent=e.message}
  finally{renderHeader()}
 };

 /* ---------- inspect drawer: ping / activity per box ---------- */
 const inspect=$('#inspect');
 let inspectOpen=false,inspectTimer,controllerPing=null;
 let inspectProfilesFor='',inspectProfileCache=null;
 let inspectWorkerKey='',inspectWorker=null;
 let inspectInstructionsFor='',inspectInstructions=null,inspectInstructionsRequest=0;
 let inspectAttachmentFor='',inspectAttachmentCache=null,inspectAttachmentRequest=0;
 const storageSize=bytes=>bytes>=1024*1024*1024?(bytes/1073741824).toFixed(2)+' GiB':bytes>=1024*1024?(bytes/1048576).toFixed(1)+' MiB':bytes>=1024?(bytes/1024).toFixed(1)+' KiB':bytes+' B';
 async function loadInspectAttachmentStorage(box){
  const request=++inspectAttachmentRequest;inspectAttachmentFor=box.id;inspectAttachmentCache=null;
  try{
   const value=await api(boxPath(box.id)+'/attachment-storage');
   if(inspectOpen&&selected===box.id&&inspectAttachmentFor===box.id&&request===inspectAttachmentRequest){inspectAttachmentCache=value;renderInspect()}
  }catch(e){if(inspectOpen&&selected===box.id&&inspectAttachmentFor===box.id&&request===inspectAttachmentRequest){inspectAttachmentCache={error:e.message};renderInspect()}}
 }
 async function loadInspectInstructions(box){
  const id=box.id,request=++inspectInstructionsRequest;
  inspectInstructionsFor=id;inspectInstructions=null;
  try{
   const state=await api(boxPath(id)+'/instructions');
   if(inspectOpen&&selected===id&&inspectInstructionsFor===id&&inspectInstructionsRequest===request){inspectInstructions=state;renderInspect()}
  }catch{
   if(inspectOpen&&selected===id&&inspectInstructionsFor===id&&inspectInstructionsRequest===request){inspectInstructions={error:true};renderInspect()}
  }
 }
 async function loadInspectWorker(box,key){
  if(!box.slotId){inspectWorker={name:'Unassigned',serviceId:'',slot:'—'};renderInspect();return}
  try{
   const query=new URLSearchParams({provider:box.provider,providerCredential:box.providerCredential||''});
   const fleet=await api('/v1/fleet/status?'+query);
   if(inspectWorkerKey!==key||selected!==box.id||!inspectOpen)return;
   const slot=(fleet.slots||[]).find(item=>item.id===box.slotId);
   inspectWorker={name:slot?.serviceName||slot?.serviceId||box.providerCredential||'Unknown worker',serviceId:slot?.serviceId||'',slot:slot?.ordinal?('#'+slot.ordinal+' · '+box.slotId):box.slotId};
  }catch{
   if(inspectWorkerKey!==key||selected!==box.id||!inspectOpen)return;
   inspectWorker={name:box.providerCredential||'Worker unavailable',serviceId:'',slot:box.slotId};
  }
  renderInspect();
 }
 const fmtAgo=value=>{const s=Math.max(0,(Date.now()-new Date(value).getTime())/1000);if(s<60)return Math.round(s)+'s ago';if(s<3600)return Math.round(s/60)+' min ago';if(s<86400)return Math.round(s/3600)+' h ago';return Math.round(s/86400)+' d ago'};
 function instructionSyncLabel(state){
  if(!state)return 'Loading…';
  if(state.error)return 'Unavailable';
  const appliedAt=state.instructions?.appliedAt;
  const date=appliedAt?new Date(appliedAt):null;
  const last=date&&!Number.isNaN(date.getTime())?date.toLocaleString():'Never';
  return last+(state.pending?' · changes pending':'');
 }
 const lastMessage=(messages,direction)=>[...messages].reverse().find(m=>m.direction===direction);
 const stateClass=state=>state==='running'?'ok':state==='starting'?'warn':'alert';
 const importedProfileLabel=ref=>[ref.application,ref.name,ref.model,ref.reasoningEffort].filter(Boolean).join(' · ');
 const fillRows=(target,rows)=>{
  target.replaceChildren();
  for(const [dt,dd,cls] of rows){
   const row=document.createElement('div'),t=document.createElement('dt'),d=document.createElement('dd');
   t.textContent=dt;d.textContent=dd;if(cls)d.className=cls;row.append(t,d);target.append(row);
  }
 };
 function mountInspectMemory(box){
  const root=$('#inspect-memory-settings');
  if(!owner||box.provider!=='shared-worker'||box.state!=='running'){root.hidden=true;root.dataset.boxId='';root.replaceChildren();return}
  root.hidden=false;
  if(root.dataset.boxId===box.id&&root.dataset.generation===String(box.assignmentGeneration))return;
  root.dataset.boxId=box.id;root.dataset.generation=String(box.assignmentGeneration);
  root.innerHTML='<h4>RAM and swap</h4><div class="memory-availability" aria-live="polite">Checking worker availability…</div><form><label>RAM <span>GiB</span><input name="memory" type="number" min="1" max="8" required></label><label>Swap <span>GiB</span><input name="swap" type="number" min="0" max="4" required></label><button type="submit">Apply live</button></form><p role="status">Loading limits…</p>';
  const form=root.querySelector('form'),status=root.querySelector('[role="status"]'),availability=root.querySelector('.memory-availability');form.hidden=true;
  const hostQuery=new URLSearchParams({provider:box.provider,providerCredential:box.providerCredential||''});
  void api('/v1/fleet/host-resources?'+hostQuery).then(host=>{
   if(root.dataset.boxId!==box.id||root.dataset.generation!==String(box.assignmentGeneration))return;
   const fmt=bytes=>(bytes/(1024**3)).toFixed(1)+' GiB';
   availability.textContent='Worker available now · RAM '+fmt(host.memoryAvailableBytes)+' / '+fmt(host.memoryTotalBytes)+' · swap '+fmt(host.swapFreeBytes)+' / '+fmt(host.swapTotalBytes);
   if(host.observedAt)availability.title='Measured '+new Date(host.observedAt).toLocaleString();
  }).catch(()=>{if(root.dataset.boxId===box.id)availability.textContent='Worker availability unavailable.'});
  const load=async()=>{
   try{
    const current=await api(boxPath(box.id)+'/resources');
    if(root.dataset.boxId!==box.id||root.dataset.generation!==String(box.assignmentGeneration))return null;
    form.elements.memory.value=String(current.resources.memoryMiB/1024);
    form.elements.swap.value=String((current.resources.swapMiB||0)/1024);
    form.hidden=false;status.textContent='Limits apply to this box and survive hibernation.';
    return current;
   }catch(e){if(root.dataset.boxId===box.id)status.textContent=e.message;return null}
  };
  let current=null;void load().then(value=>{current=value});
  form.onsubmit=async event=>{
   event.preventDefault();if(!current)return;
   const memory=Number(form.elements.memory.value),swap=Number(form.elements.swap.value);
   if(!Number.isInteger(memory)||memory<1||memory>8||!Number.isInteger(swap)||swap<0||swap>4){status.textContent='Choose 1–8 GiB RAM and 0–4 GiB swap.';return}
   form.querySelector('button').disabled=true;status.textContent='Applying limits…';
   try{
    await api(boxPath(box.id)+'/resources','PUT',{}, {slotId:current.slotId,assignmentGeneration:current.assignmentGeneration,cpu:1,memoryMiB:memory*1024,swapMiB:swap*1024});
    current=await load();if(current)status.textContent='Live limits saved.';
   }catch(e){status.textContent=e.message}
   finally{form.querySelector('button').disabled=false}
  };
 }
 function renderInspect(){
  if(!inspectOpen||!selected)return;
  const box=boxes.get(selected);if(!box)return;
  if(inspectProfilesFor!==box.id)inspectProfileCache=null;
  const workerKey=box.id+'|'+(box.slotId||'');
  if(inspectWorkerKey!==workerKey){inspectWorkerKey=workerKey;inspectWorker=null;void loadInspectWorker(box,workerKey)}
  const msgs=box.messages||[],lastAgent=lastMessage(msgs,'agent'),lastUser=lastMessage(msgs,'user');
  const livePing=boxViewerMetrics.get(box.id)?.ping;
  const waiting=!!lastUser&&(!lastAgent||new Date(lastUser.createdAt)>new Date(lastAgent.createdAt));
  const agent=box.defaultAgent||'shell';
  const stateText=box.state+(box.streaming?' · agent streaming…':box.processing?' · agent processing…':'');
  $('#inspect-title').textContent=box.name;
  $('#inspect-header-state').textContent=stateText;
  $('#inspect-header-state').className=stateClass(box.state);
  $('#inspect-avatar').replaceChildren(avatarNode(box,false));
  $('#inspect-name').textContent=box.name;
  $('#inspect-subtitle').textContent='';
  const badges=$('#inspect-badges');badges.replaceChildren();
  const badge=(text,cls)=>{const b=document.createElement('span');b.className='inspect-badge'+(cls?' '+cls:'');b.textContent=text;badges.append(b)};
  badge(box.state,stateClass(box.state));
  badge(agent,'agent');
  if(box.provider)badge(box.provider);
  fillRows($('#inspect-runtime-rows'),[
   ['State',stateText,stateClass(box.state)],
   ['Agent',agent],
   ...(owner?[['Imported profiles',inspectProfileCache?inspectProfileCache.error||((inspectProfileCache.profiles||[]).map(importedProfileLabel).join(', ')||'None')+(inspectProfileCache.pending?.length?' · queued: '+inspectProfileCache.pending.map(importedProfileLabel).join(', '):''):'Loading…']]:[]),
   ['Provider',box.provider||'—'],
   ['Worker',inspectWorker?.name||'Loading…'],
   ...(inspectWorker?.serviceId&&inspectWorker.serviceId!==inspectWorker.name?[['Service ID',inspectWorker.serviceId]]:[]),
   ['Slot',inspectWorker?.slot||'Loading…'],
   ['Messages',msgs.length+' total'],
  ]);
  fillRows($('#inspect-activity-rows'),[
   ['Controller ping',controllerPing==null?'—':controllerPing+' ms'],
   ['Box ping (live VNC)',livePing!=null?livePing+' ms':'opens with the desktop popup'],
   ['Last agent activity',box.streaming?'streaming now…':lastAgent?fmtAgo(lastAgent.updatedAt||lastAgent.createdAt):'—'],
   ['Last instructions sync',instructionSyncLabel(inspectInstructionsFor===box.id?inspectInstructions:null)],
   ['Waiting for agent',waiting?'since '+fmtAgo(lastUser.createdAt):'no',waiting?'alert':'ok'],
  ]);
  const attachmentRoot=$('#inspect-attachment-storage');attachmentRoot.hidden=!owner;
  if(owner){
   if(inspectAttachmentFor!==box.id)void loadInspectAttachmentStorage(box);
   const usage=inspectAttachmentCache;
   fillRows($('#inspect-attachment-rows'),usage?.error?[['Storage',usage.error,'alert']]:[
    ['This box',usage?storageSize(usage.boxBytes)+' · '+usage.boxCount+' files':'Loading…'],
    ['Account',usage?storageSize(usage.accountBytes)+' / '+storageSize(usage.limitBytes):'Loading…'],
    ['Unused uploads',usage?storageSize(usage.unusedBytes||0)+' · '+(usage.unusedCount||0)+' files; cleaned on the next upload after 24 hours':'Loading…'],
   ]);
   $('#inspect-clear-attachments').disabled=!usage||!!usage.error||!usage.clearableCount;
  }
  const quick=$('#inspect-quick-actions');quick.replaceChildren();
  const link=document.createElement('a');
  link.href='/boxes/'+encodeURIComponent(box.id);link.textContent='Open workspace';link.target='_blank';link.rel='noopener';
  quick.append(link);
  if(canWakeBox(box)){
   const wake=document.createElement('button');wake.type='button';wake.textContent='Wake box';wake.disabled=wakingBoxes.has(box.id);
   wake.title='Restore the saved workspace and check for a saved agent conversation';wake.onclick=()=>void wakeBox(box);quick.append(wake);
  }
  if(box.state==='running'&&agent!=='shell'){
   const clear=document.createElement('button');clear.type='button';clear.textContent='Clear context';
   clear.title='Start a fresh agent context for this chat';clear.onclick=()=>$('#chat-clear-context').click();quick.append(clear);
  }
  // Config the box keeps in sync, editable from the same place it is reported.
  const actions=$('#inspect-config-actions');actions.replaceChildren();
  const act=(label,title,fn)=>{const b=document.createElement('button');b.type='button';b.textContent=label;b.title=title;b.onclick=fn;actions.append(b)};
  act('Instructions…','Edit the Markdown instructions synced into this box',()=>void openBoxInstructions(box));
  if(owner)act('Credentials…','Replace the login profiles imported into this box',()=>void openBoxCredentials(box));
  if(box.state==='running')act('Re-sync instructions','Re-push saved instructions to the running box',()=>void resyncBox(box));
  if(box.state==='running')act('Restart…','Hibernate and start again; running sessions end',()=>void restartBox(box));
  mountInspectMemory(box);
  const idleRoot=$('#inspect-idle-policy');idleRoot.hidden=!owner;
  if(owner)window.VMBoxIdlePolicy?.mount(idleRoot,{boxId:box.id,boxName:box.name,request:seconds=>api(boxPath(box.id)+'/idle-policy',seconds===undefined?'GET':'PUT',{},seconds===undefined?undefined:{seconds})});
  const budgetRoot=$('#inspect-run-budget-policy');budgetRoot.hidden=!owner;
  if(owner)window.VMBoxRunBudgetPolicy?.mount(budgetRoot,{boxId:box.id,state:box.state,assignmentGeneration:box.assignmentGeneration,
   request:seconds=>api(boxPath(box.id)+'/run-budget-policy',seconds===undefined?'GET':'PUT',{},seconds===undefined?undefined:{seconds}),
   adjust:(action,seconds,expectedDeadlineAt)=>api(boxPath(box.id)+'/run-budget-policy/adjust','POST',{},{action,seconds,expectedDeadlineAt})});
  const limitRoot=$('#inspect-create-limit');
  if(owner)window.VMBoxCreateLimit?.mount(limitRoot,{boxId:box.id,request:body=>api(boxPath(box.id)+'/agent-policy',body?'PUT':'GET',{},body),onSaved:policy=>{policySummaries.set(box.id,policy);if(!$('#roles-modal').hidden)renderPermissionBoxes()}});
  else limitRoot.hidden=true;
  maybeLoadInspectProfiles(box);
  if(inspectInstructionsFor!==box.id)void loadInspectInstructions(box);
  maybeLoadInspectContacts(box);
 }
 function maybeLoadInspectProfiles(box){
  if(!owner||inspectProfilesFor===box.id)return;
  inspectProfilesFor=box.id;inspectProfileCache=null;
  void api(boxPath(box.id)+'/imported-credentials').then(state=>{
   if(!inspectOpen||selected!==box.id||inspectProfilesFor!==box.id)return;
   inspectProfileCache=state;renderInspect();
  }).catch(()=>{
   if(!inspectOpen||selected!==box.id||inspectProfilesFor!==box.id)return;
   inspectProfileCache={error:'Unavailable'};renderInspect();
  });
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
  if(inspectOpen){closeInspect();return}
  inspectOpen=true;inspect.hidden=false;$('#inspect-backdrop').hidden=false;
  inspect.classList.toggle('with-contacts',owner);
  $('#chat-info').setAttribute('aria-expanded',String(inspectOpen));
  controllerPing=null;void samplePing();inspectTimer=setInterval(()=>void samplePing(),5000);
 };
 function closeInspect(){inspectOpen=false;inspect.hidden=true;$('#inspect-backdrop').hidden=true;$('#chat-info').setAttribute('aria-expanded','false');clearInterval(inspectTimer);controllerPing=null;inspectContactsFor='';inspectContactCache=null;inspectProfilesFor='';inspectProfileCache=null;inspectAttachmentFor='';inspectAttachmentCache=null;inspectAttachmentRequest++;inspectInstructionsFor='';inspectInstructions=null;inspectInstructionsRequest++;inspectWorkerKey='';inspectWorker=null;const limit=$('#inspect-create-limit');limit.replaceChildren();delete limit.dataset.createLimitBox;const budget=$('#inspect-run-budget-policy');budget.replaceChildren();delete budget.dataset.budgetKey}
 $('#inspect-close').onclick=closeInspect;
 $('#inspect-backdrop').onclick=closeInspect;
 $('#inspect-clear-attachments').onclick=async()=>{
  const box=boxes.get(selected);if(!box)return;
  if(!confirm('Permanently remove attachments from delivered chat messages in "'+box.name+'"? Message text stays available. Attachments still used by another box remain there.'))return;
  const button=$('#inspect-clear-attachments'),storageStatus=$('#inspect-attachment-status');button.disabled=true;storageStatus.textContent='Clearing attachments…';
  try{
   const result=await api(boxPath(box.id)+'/attachment-storage','DELETE',{}, {confirmation:box.name});
   for(const message of box.messages||[])if(message.state==='delivered')message.images=[];
   if(selected===box.id)await refreshMessages(true);
   if(inspectOpen&&selected===box.id){await loadInspectAttachmentStorage(box);storageStatus.textContent='Removed '+result.removedReferences+' attachment references; freed '+storageSize(result.freedBytes)+'.'}
  }catch(e){storageStatus.textContent=e.message;button.disabled=false}
 };
 // Collapsible details sections, remembered per browser.
 const foldKey='vmbox.inspectFold';
 let foldState={};try{foldState=JSON.parse(localStorage.getItem(foldKey)||'{}')}catch{}
 document.querySelectorAll('#inspect-summary .inspect-fold').forEach(node=>{
  const key=node.dataset.fold;
  if(key in foldState)node.open=!!foldState[key];
  node.addEventListener('toggle',()=>{foldState[key]=node.open;try{localStorage.setItem(foldKey,JSON.stringify(foldState))}catch{}});
 });

 /* ---------- inspect drawer: per-box contact graph (owner) ---------- */
 const inspectContacts=$('#inspect-contacts');
 let inspectContactsFor='',inspectProtected=false,inspectContactCache=null;
 const contactView=contact=>{
  const known=boxes.get(contact.contactBoxId)||{};
  return {id:contact.contactBoxId||contact.contactName,name:contact.contactName||known.name||'—',state:contact.contactState||known.state||'unknown',defaultAgent:contact.contactAgent||known.defaultAgent||''};
 };
 function renderInspectContacts(box){
  const contacts=inspectContactCache||[],selectedList=$('#inspect-contact-list'),options=$('#inspect-contact-options');
  selectedList.replaceChildren();options.replaceChildren();
  const direct=contacts.filter(contact=>contact.override==='allow');
  const available=contacts.filter(contact=>contact.override!=='allow');
  const query=$('#inspect-contact-search').value.trim().toLocaleLowerCase();
  const makeRow=(contact,added)=>{
   const view=contactView(contact),item=document.createElement('li');item.dataset.state=view.state;
   item.append(avatarNode(view,true));
   const meta=document.createElement('div');meta.className='contact-info';
   const name=document.createElement('span');name.className='contact-name';name.textContent=view.name;
   const detail=document.createElement('span');detail.className='contact-meta';detail.textContent=(view.defaultAgent||'agent')+' · '+view.state+(contact.override==='block'?' · blocked':'');
   meta.append(name,detail);item.append(meta);
   const access=document.createElement('button');access.type='button';access.className='contact-access';access.textContent=added?'Remove':'Add';
   access.setAttribute('aria-label',(added?'Remove ':'Add ')+view.name+(added?' from ':' to ')+box.name+' contacts');
   access.onclick=async()=>{access.disabled=true;try{await api(boxPath(box.id)+'/contacts','PUT',{}, {contact:contact.contactBoxId,state:added?'inherit':'allow'});await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message;access.disabled=false}};
   item.append(access);return item;
  };
  if(!direct.length){const empty=document.createElement('li');empty.className='empty';empty.textContent='No direct contacts yet.';selectedList.append(empty)}
  else for(const contact of direct)selectedList.append(makeRow(contact,true));
  const matches=available.filter(contact=>!query||contactView(contact).name.toLocaleLowerCase().includes(query));
  if(!matches.length){const empty=document.createElement('li');empty.className='empty';empty.textContent=available.length?'No matching boxes.':'All eligible boxes are contacts.';options.append(empty)}
  else for(const contact of matches)options.append(makeRow(contact,false));
  $('#inspect-add-contact').disabled=!available.length;
 }
 async function loadInspectContacts(box){
  const status=$('#inspect-contact-status');
  status.textContent='Loading…';
  try{
   const [contacts,protection,tagResult]=await Promise.all([api(boxPath(box.id)+'/contacts'),api(boxPath(box.id)+'/protection'),api(boxPath(box.id)+'/tags')]);
   if(!inspectOpen||selected!==box.id)return;
   inspectContactCache=contacts||[];
   inspectProtected=!!protection.protected;
   $('#inspect-contact-role').textContent='Configured directly on this box';
   $('#inspect-tags').textContent=(tagResult.tags||[]).join(', ')||'None';
   $('#inspect-protection-label').textContent=inspectProtected?'Protected — agents cannot see or message this box':'Not protected';
   $('#inspect-toggle-protection').textContent=inspectProtected?'Remove protection':'Protect box';
   renderInspectContacts(box);
   status.textContent='Add only the boxes this agent should contact directly. All contacts is managed in this box’s MCP permissions; protected boxes remain hidden.';
  }catch(e){status.textContent=e.message}
 }
 function maybeLoadInspectContacts(box){
  if(!owner){inspectContacts.hidden=true;inspectContactsFor='';inspectContactCache=null;return}
  inspectContacts.hidden=false;
  if(inspectContactsFor===box.id)return;
  inspectContactsFor=box.id;inspectContactCache=null;$('#inspect-contact-picker').hidden=true;$('#inspect-add-contact').setAttribute('aria-expanded','false');$('#inspect-contact-search').value='';void loadInspectContacts(box);
 }
 $('#inspect-add-contact').onclick=()=>{const picker=$('#inspect-contact-picker'),open=picker.hidden;picker.hidden=!open;$('#inspect-add-contact').setAttribute('aria-expanded',String(open));if(open)$('#inspect-contact-search').focus()};
 $('#inspect-contact-search').addEventListener('input',()=>{const box=boxes.get(selected);if(box)renderInspectContacts(box)});
 $('#inspect-toggle-protection').onclick=async()=>{const box=boxes.get(selected);if(!box)return;try{await api(boxPath(box.id)+'/protection','PUT',{}, {protected:!inspectProtected});await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};
 $('#inspect-edit-tags').onclick=async()=>{const box=boxes.get(selected);if(!box)return;const current=$('#inspect-tags').textContent==='None'?'':$('#inspect-tags').textContent;const value=prompt('Labels for '+box.name+' (comma separated)',current);if(value===null)return;try{await api(boxPath(box.id)+'/tags','PUT',{}, {tags:value.split(',').map(tag=>tag.trim()).filter(Boolean)});await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};

 /* ---------- toasts ---------- */
 function toast(text,actionLabel,onAction){
  const el=document.createElement('div');el.className='toast';el.append(Object.assign(document.createElement('span'),{textContent:text}));
  if(actionLabel){const button=document.createElement('button');button.type='button';button.className='toast-action';button.textContent=actionLabel;
   button.onclick=()=>{el.remove();onAction?.()};el.append(button)}
  $('#chat-toasts').append(el);
  setTimeout(()=>{el.style.opacity='0';setTimeout(()=>el.remove(),400)},actionLabel?6000:3200);
 }

 /* The desktop stream attaches to a running desktop and never starts one, so a
    reload (or any box whose desktop is not up yet) has to start it first. A
    worker without the desktop packages needs an explicit enable before start,
    which is the same ladder the workspace view uses. */
 async function ensureDesktopRunning(box){
  try{
   await api(boxPath(box.id)+'/desktop','POST',{});
  }catch(e){
   if(!/components unavailable|enable the desktop/i.test(e.message||''))throw e;
   await api(boxPath(box.id)+'/desktop/enable','POST',{},{},190000);
   await api(boxPath(box.id)+'/desktop','POST',{});
  }
 }
  /* ---------- new box (full controller feature set: agent, disk, placement defaults, login profiles, tools, setup script) ---------- */
  const newBoxModal=$('#new-box-modal'),createForm=$('#create-box'),previewCard=$('#create-preview-card');
  let extrasLoaded=false,poolChoices=[],autoPoolIndex='',profileUsageLoadError=false,renderCreateProfileUsage=()=>{};
  const money=n=>'$'+(n>=0.1?n.toFixed(2):n.toFixed(3));
  const ctxLabel=n=>n>=1e6?(n/1e6).toFixed(n%1e6?1:0)+'M':n>=1e3?Math.round(n/1e3)+'k':n?'':'';
  function previewRows(){
   if(!newBoxModal.hidden&&previewCard){
    const f=createForm.elements,model=String(f.agentModel?.value||'').trim();
    const rows=[['Name',f.name.value.trim()||'—'],['Agent',f.defaultAgent.selectedOptions[0]?.textContent||f.defaultAgent.value]];
    const profile=f.loginProfile?.selectedOptions[0];
    if(f.loginProfile&&!f.loginProfile.hidden&&profile?.value)rows.push(['Profile',profile.textContent]);
    if(model)rows.push(['Model',model]);
    const pickerModel=window.VMBoxModelPicker?.catalog?.().find(entry=>entry.id===model);
    if(pickerModel?.inputCost)rows.push(['Price',money(pickerModel.inputCost)+' in · '+money(pickerModel.outputCost)+' out / Mtok']);
    if(pickerModel?.context)rows.push(['Context',ctxLabel(pickerModel.context)+' tokens']);
    if(f.agentReasoningEffort?.value)rows.push(['Reasoning',f.agentReasoningEffort.value]);
    if(poolChoices.length){
     const poolIndex=f.pool?.value??'',pool=poolChoices[Number(poolIndex)]||poolChoices[Number(createForm.dataset.autoPool)];
     if(pool)rows.push(['Pool',pool.label]);
    }
    if(!$('#create-memory-settings').hidden)rows.push(['RAM / swap',(f.memoryGiB?.value||'2')+' / '+(f.swapGiB?.value||'1')+' GiB']);
    return rows;
   }
   return [];
  }
  function renderPreview(){
   const selectedPool=createForm.elements.pool?.value||createForm.dataset.autoPool||'';
   const pool=selectedPool!==''?JSON.parse(createForm.dataset.pools||'[]')[Number(selectedPool)]:null;
   $('#create-memory-settings').hidden=pool?.provider!=='shared-worker';
   if(!previewCard)return;
   previewCard.replaceChildren();
   for(const [key,value] of previewRows()){
    const row=document.createElement('div');row.className='preview-row';
    const k=document.createElement('span');k.className='k';k.textContent=key;
    const v=document.createElement('span');v.className='v';v.textContent=value;
    row.append(k,v);previewCard.append(row);
   }
  }
  function renderCreationProfileChoices(profiles){
  const root=$('#profile-choices'),agentSelect=createForm.elements.defaultAgent;root._modelPicker?.destroy();root.replaceChildren();
  const profileLabel=document.createElement('label');profileLabel.className='field profile-field';profileLabel.textContent='Login profile';
  const profileSelect=document.createElement('select');profileSelect.name='loginProfile';profileSelect.hidden=true;profileLabel.append(profileSelect);
  const profileList=document.createElement('div');profileList.className='create-profile-list';profileList.setAttribute('role','group');profileList.setAttribute('aria-label','Login profiles and remaining usage');
  const modelLabel=document.createElement('label');modelLabel.className='field profile-field';modelLabel.textContent='Model';
  const modelInput=document.createElement('input');modelInput.name='agentModel';modelInput.maxLength=200;modelLabel.append(modelInput);const modelPicker=window.VMBoxModelPicker.create(modelInput);root._modelPicker=modelPicker;
  const githubLabel=document.createElement('label');githubLabel.className='field profile-field';githubLabel.textContent='GitHub profile';
  const githubSelect=document.createElement('select');githubSelect.name='githubProfile';githubSelect.append(new Option('None',''));
  for(const profile of profiles.filter(profile=>profile.application==='github'))githubSelect.append(new Option(profile.name,JSON.stringify({application:'github',name:profile.name})));
  githubLabel.append(githubSelect);githubLabel.hidden=githubSelect.options.length===1;
   root.append(profileLabel,profileList,modelLabel,githubLabel);
  renderCreateProfileUsage=()=>{
   const app=agentSelect.value,choices=profiles.filter(profile=>profile.application===app);
   const focusedValue=profileList.contains(document.activeElement)?document.activeElement.dataset.profile:null;
   profileList.replaceChildren();profileList.hidden=app==='shell'||choices.length===0;
   if(profileList.hidden)return;
   const addChoice=(value,name,summary,details,profile)=>{
    const button=document.createElement('button');button.type='button';button.className='create-profile-option';button.dataset.profile=value;
    button.setAttribute('aria-pressed',String(profileSelect.value===value));
    const heading=document.createElement('span');heading.className='create-profile-option-head';
    const title=document.createElement('strong');title.textContent=name;
    const remaining=document.createElement('span');remaining.className='create-profile-remaining';remaining.textContent=summary;
    const lowest=profile?lowestRemaining(profile):null;
    if(lowest!==null&&lowest<=20)remaining.classList.add('low');
    heading.append(title,remaining);button.append(heading);
    if(details){const detail=document.createElement('small');detail.textContent=details;button.append(detail)}
    button.onclick=()=>{profileSelect.value=value;profileSelect.dispatchEvent(new Event('change',{bubbles:true}))};
    profileList.append(button);
   };
   addChoice('','None','No profile','No saved profile selected');
   for(const profile of choices){
    const ref=JSON.stringify({application:profile.application,name:profile.name});
    const measured=usageProfiles.find(item=>item.application===profile.application&&item.name===profile.name);
    const remaining=lowestRemaining(measured);
    const windows=(measured?.snapshot?.windows||[]).map(window=>{
     const value=remainingPercent(window.usedPercent);if(value===null)return null;
     const labels={session:'Session',weekly_all:'Week',weekly_scoped:'Week',primary:'Primary',secondary:'Secondary'};
     return (labels[window.name]||window.name)+(window.scope?' · '+window.scope:'')+' '+usageNumber(value)+'%';
    }).filter(Boolean);
    let summary=remaining===null?(usageLoaded||profileUsageLoadError?'Usage unavailable':'Checking usage…'):usageNumber(remaining)+'% left';
    let details=windows.slice(0,3).join(' · ');
    if(windows.length>3)details+=' · +'+(windows.length-3)+' more';
    if(!details&&measured?.snapshot?.spend){const spend=measured.snapshot.spend;const left=typeof spend.remaining==='number'?spend.remaining:typeof spend.limit==='number'&&typeof spend.used==='number'?Math.max(0,spend.limit-spend.used):null;if(left!==null){summary=usageNumber(left)+(spend.currency?' '+spend.currency:'')+' left';details='Spending balance'}}
    if(!details&&measured?.snapshot?.balances?.length){const balance=measured.snapshot.balances[0];summary=usageNumber(balance.amount)+(balance.unit?' '+balance.unit:'')+' balance';details='Available balance'}
    if(!details)details=measured?.error?'Last check failed':measured?.checkedAt?'No remaining limit reported':'No measurement yet';
    const checkedAt=measured?.checkedAt||measured?.observedAt;
    if(checkedAt){const date=new Date(checkedAt);if(!Number.isNaN(date.getTime()))details+=' · checked '+(date.toDateString()===new Date().toDateString()?date.toLocaleTimeString(undefined,{hour:'numeric',minute:'2-digit'}):date.toLocaleDateString(undefined,{month:'short',day:'numeric'}))}
    addChoice(ref,profile.name,summary,details,measured);
   }
   if(focusedValue!==null)[...profileList.children].find(button=>button.dataset.profile===focusedValue)?.focus({preventScroll:true});
  };
  const syncModel=()=>{const option=profileSelect.selectedOptions[0],hasProfile=!!profileSelect.value;modelInput.disabled=!hasProfile;modelPicker.setValue(hasProfile?option?.dataset.model||'':'');modelPicker.setReasoningEffort('');modelPicker.setOptions(window.VMBoxModelPicker.optionsFor(agentSelect.value,[option?.dataset.model]));modelLabel.hidden=!hasProfile;const ref=hasProfile?JSON.parse(profileSelect.value):null;modelPicker.setLoader(ref&&['claude','codex','opencode'].includes(ref.application)?()=>api('/v1/login-profiles/'+encodeURIComponent(ref.application)+'/'+encodeURIComponent(ref.name)+'/models'):null);renderCreateProfileUsage();renderPreview()};
  const populate=()=>{
   const previous=profileSelect.value,app=agentSelect.value;profileSelect.replaceChildren(new Option('None',''));
   const choices=profiles.filter(profile=>profile.application===app);
   for(const profile of choices){const option=new Option(profile.name,JSON.stringify({application:profile.application,name:profile.name}));option.dataset.model=profile.model||'';profileSelect.append(option)}modelPicker.setApplication(app);
   if([...profileSelect.options].some(option=>option.value===previous))profileSelect.value=previous;
   profileLabel.hidden=app==='shell'||choices.length===0;root.hidden=profileLabel.hidden&&githubLabel.hidden;syncModel();
  };
   profileSelect.addEventListener('change',syncModel);agentSelect.onchange=populate;populate();
   modelInput.addEventListener('change',renderPreview);
   createForm.elements.name.addEventListener('input',renderPreview);
   createForm.elements.defaultAgent.addEventListener('change',renderPreview);
 }
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
   poolChoices=pools.map((pool,index)=>({pool,label:(poolLabel(pool,poolStatuses[index]).replace('Dedicated · ','').replace('Shared worker','shared').split(' — ')[0])+(index===Number(autoIndex)?' (auto)':'')}));
   poolSelect.addEventListener('change',renderPreview);
   $('#create-pool-label').hidden=pools.length===0;
   const poolHint=$('#create-pool-status');
   poolHint.hidden=pools.length===0;
   if(pools.length)poolHint.textContent=pools.map((pool,index)=>poolLabel(pool,poolStatuses[index]).replace('Dedicated · ','').replace('Shared worker','shared')).join(' · ')+' — boxes wait in the controller queue when their pool has no free slots.';
   createForm.dataset.pools=JSON.stringify(pools);
   if(defaults.provider){createForm.dataset.provider=defaults.provider;createForm.dataset.providerCredential=defaults.providerCredential||''}
   renderCreationProfileChoices(profiles);
   const toolsSet=$('#create-tools');toolsSet.replaceChildren();
   for(const tool of tools){
    if(tool.id==='desktop')continue;
    const label=document.createElement('label'),input=document.createElement('input');
    input.type='checkbox';input.value=tool.id;input.name='tool';input.title=tool.description||tool.name;
    label.append(input,document.createTextNode(' '+tool.name));toolsSet.append(label);
   }
   toolsSet.hidden=!toolsSet.children.length;
   extrasLoaded=true;
   renderPreview();
  }catch(e){$('#new-box-status').textContent=e.message}
 }
  function openNewBoxModal(){
   createForm.reset();createInstructionSource='';void syncCreateInstructionText();createForm.elements.defaultAgent.onchange?.();$('#new-box-status').textContent='';newBoxModal.hidden=false;
   void primeBoxExtras();
   if(owner)void refreshUsage();
   createForm.elements.name.focus();
   renderPreview();
  }
 $('#new-box').onclick=openNewBoxModal;
 $('#new-box-close').onclick=()=>{newBoxModal.hidden=true};
 $('#new-box-backdrop').onclick=()=>{newBoxModal.hidden=true};
 newBoxModal.addEventListener('transitionend',renderPreview);
 document.addEventListener('change',event=>{if(event.target?.name==='agentReasoningEffort'&&!newBoxModal.hidden)renderPreview()});
 createForm.addEventListener('change',event=>{if(event.target?.name==='disk'&&!newBoxModal.hidden)renderPreview()});
 createForm.onsubmit=async event=>{
  event.preventDefault();
  const f=createForm.elements,submit=$('#create-box-submit');submit.disabled=true;$('#new-box-status').textContent='Creating…';
  const tools=['desktop',...[...createForm.querySelectorAll('input[name=tool]:checked')].map(i=>i.value)];
  const selectedProfile=createForm.elements.loginProfile?.value;
  const profileRef=selectedProfile?JSON.parse(selectedProfile):null;
  const loginProfiles=profileRef?[{...profileRef,model:(createForm.elements.agentModel?.value||'').trim(),...(createForm.elements.agentReasoningEffort?.value?{reasoningEffort:createForm.elements.agentReasoningEffort.value}:{})}]:[];
  if(profileRef&&!loginProfiles[0].model){$('#new-box-status').textContent='Choose a model';submit.disabled=false;return}
  const githubProfile=createForm.elements.githubProfile?.value;
  if(githubProfile)loginProfiles.push(JSON.parse(githubProfile));
  const setupScript=(createForm.elements.setupScript?.value||'').trim();
  const body={name:f.name.value.trim(),defaultAgent:f.defaultAgent.value,diskGiB:Number(f.disk.value)||10,provider:createForm.dataset.provider||'',providerCredential:createForm.dataset.providerCredential||'',allocateWhenReady:true};
  const poolIndex=(f.pool.value||createForm.dataset.autoPool||'');
  if(poolIndex!==''){const pool=JSON.parse(createForm.dataset.pools||'[]')[Number(poolIndex)];if(pool){body.provider=pool.provider;body.providerCredential=pool.providerCredential||''}}
  if(body.provider==='shared-worker'){body.memoryGiB=Number(f.memoryGiB.value);body.swapGiB=Number(f.swapGiB.value)}
  if(loginProfiles.length)body.loginProfiles=loginProfiles;
  if(tools.length)body.tools=tools;
  if(setupScript)body.setupScript=setupScript;
  try{
   const instructions=await createInstructionSelection();
   if(instructions)body.instructions=instructions;
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
 const rowMenu=$('#row-menu'),menuBackdrop=$('#menu-backdrop');
 const groupDialog=$('#chat-group-dialog'),groupForm=$('#chat-group-form'),groupName=$('#chat-group-name');
 let editingGroupId='',pendingGroupChat='';
 function openGroupDialog(group=null,key=''){
  editingGroupId=group?.id||'';pendingGroupChat=key;
  groupForm.reset();groupName.setCustomValidity('');groupName.value=group?.name||'';
  $('#chat-group-dialog-title').textContent=group?'Rename group':'New group';
  groupDialog.showModal();groupName.focus();groupName.select();
 }
 $('#new-chat-group').onclick=()=>openGroupDialog();
 $('#chat-group-cancel').onclick=()=>groupDialog.close();
 groupForm.onsubmit=event=>{
  event.preventDefault();const name=groupName.value.trim();
  const duplicate=chatGroups.some(group=>group.id!==editingGroupId&&group.name.toLowerCase()===name.toLowerCase());
  groupName.setCustomValidity(!name?'Enter a group name.':duplicate?'A group with this name already exists.':'');
  if(!groupName.reportValidity())return;
  const group=editingGroupId?chatGroups.find(group=>group.id===editingGroupId):null;
  if(group)group.name=name;else chatGroups.push({id:crypto.randomUUID(),name,collapsed:false});
  const target=group||chatGroups.at(-1),key=pendingGroupChat;
  saveChatGroups();groupDialog.close();
  if(key)moveChatToGroup(key,target.id);else renderRows();
 };
 const wakingBoxes=new Set();
 const canWakeBox=box=>box&&['hibernated','detached','failed'].includes(box.state);
 function closeRowMenu(){rowMenu.hidden=true;rowMenu.replaceChildren();rowMenu.classList.remove('touch-mode');menuBackdrop.hidden=true}
 function positionRowMenu(point){
  const viewport=window.visualViewport,pad=8;
  const minX=(viewport?.offsetLeft||0)+pad,maxX=(viewport?.offsetLeft||0)+(viewport?.width||innerWidth)-pad;
  const minY=(viewport?.offsetTop||0)+pad,maxY=(viewport?.offsetTop||0)+(viewport?.height||innerHeight)-pad;
  rowMenu.style.maxWidth=Math.max(1,maxX-minX)+'px';rowMenu.style.maxHeight=Math.max(1,maxY-minY)+'px';
  const x=point.x,y=point.y,width=rowMenu.offsetWidth,height=rowMenu.offsetHeight;
  const left=x+width<=maxX?Math.max(minX,x):x-width>=minX?x-width:Math.max(minX,maxX-width);
  const top=y+height<=maxY?Math.max(minY,y):y-height>=minY?y-height:Math.max(minY,maxY-height);
  rowMenu.style.left=left+'px';rowMenu.style.top=top+'px';
 }
 function showActionMenu(items,point){
  rowMenu.replaceChildren();
  for(const item of items){
   const b=document.createElement('button');b.type='button';b.setAttribute('role','menuitem');b.textContent=item[0];
   if(item[2])b.className='danger';
   if(item[3]){
    b.classList.add('row-submenu-toggle');b.setAttribute('aria-haspopup','menu');b.setAttribute('aria-expanded','false');b.setAttribute('aria-controls','row-group-options');
    const arrow=mk('span','›');arrow.className='row-submenu-arrow';arrow.setAttribute('aria-hidden','true');b.append(arrow);
    const submenu=document.createElement('div');submenu.id='row-group-options';submenu.className='row-submenu';submenu.setAttribute('role','menu');submenu.hidden=true;
    for(const [label,action] of item[3]){const option=document.createElement('button');option.type='button';option.setAttribute('role','menuitem');option.textContent=label;option.onclick=()=>{closeRowMenu();action()};submenu.append(option)}
    b.onclick=()=>{submenu.hidden=!submenu.hidden;b.setAttribute('aria-expanded',String(!submenu.hidden));positionRowMenu(point)};
    rowMenu.append(b,submenu);
   }else{b.onclick=()=>{closeRowMenu();item[1]()};rowMenu.append(b)}
  }
  const touch=coarsePointer();
  rowMenu.classList.toggle('touch-mode',touch);
  rowMenu.hidden=false;
  menuBackdrop.hidden=!touch;
  if(touch)try{navigator.vibrate?.(10)}catch{}
  positionRowMenu(point);
 }
 function openRowMenu({box,pair},point){
  const key=box?pinKey('box',box.id):pinKey('pair',pairKey(pair));
  const items=[
   [pins.has(key)?'Unpin chat':'Pin chat',()=>togglePin(key)],
  ];
  const current=groupForChat(key);
  const groups=[];
  if(current)groups.push(['Remove from group',()=>removeChatFromGroup(key)]);
  for(const group of chatGroups)groups.push([(current?.id===group.id?'✓ ':'')+group.name,()=>moveChatToGroup(key,group.id)]);
  groups.push(['New group…',()=>openGroupDialog(null,key)]);
  items.push(['Add to Group',null,null,groups]);
  if(box){
   items.push(['Show details',()=>{if(!inspectOpen)$('#chat-info').click()}]);
   if(canWakeBox(box))items.push(['Wake box',()=>void wakeBox(box)]);
   if(box.state==='running')items.push(['Hibernate box',()=>void hibernateBox(box)],['Restart box…',()=>void restartBox(box)]);
   items.push(['Delete box…',()=>openDeleteModal(box),'danger']);
  }
  showActionMenu(items,point);
 }
 function openGroupMenu(id,point){
  const group=chatGroups.find(group=>group.id===id);if(!group)return;
  showActionMenu([
   [group.collapsed?'Expand group':'Collapse group',()=>{group.collapsed=!group.collapsed;saveChatGroups();renderRows()}],
   ['Rename group…',()=>openGroupDialog(group)],
   ['Delete group…',()=>{
    if(!confirm('Delete group "'+group.name+'"? Its chats will move to Other chats.'))return;
    chatGroups.splice(chatGroups.indexOf(group),1);
    for(const [key,groupId] of chatGroupMembers)if(groupId===id)chatGroupMembers.delete(key);
    groupNodes.delete(id);saveChatGroups();renderRows();
   },'danger'],
  ],point);
 }
 menuBackdrop.onclick=closeRowMenu;
 document.addEventListener('click',event=>{if(!rowMenu.hidden&&!rowMenu.contains(event.target))closeRowMenu()});
 addEventListener('keydown',event=>{if(event.key==='Escape'){const overlayOpen=!!document.querySelector('.sheet:not([hidden])')||!newBoxModal.hidden||!deleteModal.hidden||!takeover.hidden;closeRowMenu();closeSheets();if(!newBoxModal.hidden)newBoxModal.hidden=true;if(!deleteModal.hidden)deleteModal.hidden=true;if(!takeover.hidden)closeTakeover();if(inspectOpen&&!overlayOpen){closeInspect();$(matchMedia('(max-width:640px)').matches?'#chat-more':'#chat-info').focus()}}});
 // Re-push the instructions the box already carries. A replaced worker or a
 // restored hibernation can leave a running box behind its saved config, and
 // re-typing the same Markdown just to trigger a write is a poor way to fix it.
 async function resyncBox(box){
  try{
   const result=await api(boxPath(box.id)+'/instructions/resync','POST',{'Idempotency-Key':crypto.randomUUID()},{},120000);
   toast(result?.note||'Config re-synced to '+box.name+'.');
   if(inspectOpen&&selected===box.id){inspectInstructionsRequest++;inspectInstructionsFor=box.id;inspectInstructions=result;renderInspect()}
   if(!result?.pending)closeInspect();
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
   const allocation=await api(boxPath(box.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'chat'});
   await loadBoxes();
   await waitForChatAllocation(box,allocation);
   toast(box.name+' is running again.');
  }catch(e){toast(e.message)}
 }
 async function waitForChatAllocation(box,allocation){
   const deadline=Date.now()+180000;
   let current=allocation;
   while(current.state!=='ready'){
    if(['failed','cancelled'].includes(current.state))throw Error(current.failureReason||'Wake request failed.');
    if(Date.now()>deadline)throw Error('Wake is still in progress. Check the box state or workspace shortly.');
    await new Promise(resolve=>setTimeout(resolve,1500));
    current=await api('/v1/allocations/'+encodeURIComponent(allocation.requestId));
   }
   await loadBoxes();
   if(selected===box.id)await refreshMessages(true);
 }
 async function wakeBox(box){
  if(!canWakeBox(box)||wakingBoxes.has(box.id))return;
  wakingBoxes.add(box.id);renderHeader();
  try{
   toast('Waking '+box.name+'…');
   const allocation=await api(boxPath(box.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'chat'});
   await loadBoxes();
   await waitForChatAllocation(box,allocation);
   toast(box.name+' is running.');
  }catch(e){toast(e.message);await loadBoxes().catch(()=>{})}
  finally{wakingBoxes.delete(box.id);renderHeader()}
 }
 $('#chat-wake').onclick=()=>void wakeBox(boxes.get(selected));
 const moreButton=$('#chat-more'),moreMenu=$('#chat-more-menu');
 function closeChatMore(){moreMenu.hidden=true;moreButton.setAttribute('aria-expanded','false')}
 moreButton.onclick=()=>{const willOpen=moreMenu.hidden;moreMenu.hidden=!willOpen;moreButton.setAttribute('aria-expanded',String(willOpen));if(willOpen)moreMenu.querySelector('[role="menuitem"]')?.focus()};
 moreMenu.onkeydown=event=>{
  if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key))return;
  event.preventDefault();const items=[...moreMenu.querySelectorAll('[role="menuitem"]')].filter(item=>!item.disabled),index=items.indexOf(document.activeElement);
  const next=event.key==='Home'?0:event.key==='End'?items.length-1:(index+(event.key==='ArrowDown'?1:-1)+items.length)%items.length;
  items[next]?.focus();
 };
 moreMenu.onclick=event=>{
  const action=event.target.closest('[data-action]')?.dataset.action;
  if(!action)return;
  closeChatMore();
  if(action==='details')$('#chat-info').click();
  if(action==='control')$('#chat-control').click();
  if(action==='clear')$('#chat-clear-context').click();
 };
 document.addEventListener('pointerdown',event=>{if(!moreMenu.hidden&&!moreMenu.contains(event.target)&&event.target!==moreButton)closeChatMore()});
 document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!moreMenu.hidden){closeChatMore();moreButton.focus()}});
 async function hibernateBox(box){
  try{
   await api(boxPath(box.id)+'/hibernate','POST',{'Idempotency-Key':crypto.randomUUID()},{});
   toast(box.name+' is hibernating.');
   await loadBoxes();
  }catch(e){toast(e.message)}
 }
 const deleteModal=$('#delete-box-modal'),deleteForm=$('#delete-box-form');let deleteTarget=null;
 // Takes the box as a parameter on purpose: this loop can retry for five
 // minutes, and reading the live deleteTarget would let a delete dialog opened
 // meanwhile retarget an in-flight deletion at a different box's volume.
 async function deleteBoxWhenReady(target){
  const deadline=Date.now()+5*60*1000,key=crypto.randomUUID();
  for(;;){
   try{return await api(boxPath(target.id)+'/volume','DELETE',{'Idempotency-Key':key},{confirmation:target.name})}
   catch(error){
    if(Date.now()>=deadline||!/creation is still active|cannot transition from (?:attaching|reserved)|has not released its compute claim|workspace flush is active/i.test(error.message))throw error;
    $('#delete-box-status').textContent='Waiting for the current setup step to finish…';
    await new Promise(resolve=>setTimeout(resolve,1500));
   }
  }
 }
 function openDeleteModal(box){
  $('#delete-box-text').textContent='Deleting "'+box.name+'" permanently removes the box and its entire workspace volume. Hibernate keeps the volume instead.';
  $('#delete-box-status').textContent='';$('#delete-box-submit').disabled=false;deleteTarget=box;deleteModal.hidden=false;$('#delete-box-submit').focus();
 }
 deleteForm.onsubmit=async event=>{
  event.preventDefault();
  const submit=$('#delete-box-submit'),target=deleteTarget;submit.disabled=true;
  try{
   await deleteBoxWhenReady(target);
   deleteModal.hidden=true;toast('Deleting box "'+target.name+'"…');
   if(target.id===selected){selected='';lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;history.replaceState(null,'',location.pathname);closeTakeover()}
   deleteTarget=null;
   await loadBoxes();
  }catch(e){$('#delete-box-status').textContent=e.message}
  finally{$('#delete-box-submit').disabled=false}
 };
 $('#delete-box-close').onclick=()=>{deleteModal.hidden=true};
 $('#delete-box-backdrop').onclick=()=>{deleteModal.hidden=true};

 /* ---------- web push ---------- */
 const installBtn=$('#install-app'),installStatus=$('#install-status'),pushCheck=$('#push-check'),pushStatus=$('#push-status');
 const pushSupported=isSecureContext&&'serviceWorker'in navigator&&'PushManager'in window&&'Notification'in window;
 let swRegistration=null,pushSubscriptionPresent=null,installPrompt=null;
 function renderInstallState(){
  const installed=matchMedia('(display-mode: standalone)').matches||navigator.standalone===true;
  installBtn.hidden=installed||!/Android/i.test(navigator.userAgent);
  if(installed)installStatus.hidden=true;
 }
 addEventListener('beforeinstallprompt',event=>{event.preventDefault();installPrompt=event;renderInstallState()});
 addEventListener('appinstalled',()=>{installPrompt=null;renderInstallState()});
 installBtn.onclick=async()=>{
  if(!installPrompt){installStatus.textContent='In Chrome, open the ⋮ menu and choose Install app or Add to Home screen.';installStatus.hidden=false;return}
  const prompt=installPrompt;installPrompt=null;
  try{
   await prompt.prompt();const choice=await prompt.userChoice;
   installStatus.textContent=choice.outcome==='accepted'?'Installing vbox…':'You can install later from the Chrome menu.';
  }catch{installStatus.textContent='In Chrome, open the ⋮ menu and choose Install app.'}
  installStatus.hidden=false;
 };
 const urlB64ToBytes=value=>{const padding='='.repeat((4-value.length%4)%4);const raw=atob(value.replace(/-/g,'+').replace(/_/g,'/')+padding);return Uint8Array.from([...raw].map(c=>c.charCodeAt(0)))};
 function renderPushState(){
  pushCheck.hidden=false;pushStatus.hidden=false;
  if(!pushSupported){pushBtn.hidden=true;pushCheck.disabled=true;pushStatus.textContent=isSecureContext?'This browser does not support web push.':'Notifications require an HTTPS controller.';return}
  pushBtn.hidden=false;
  const permission=Notification.permission;
  pushBtn.disabled=permission==='denied';
  if(permission==='denied'){
   pushBtn.textContent='Notifications blocked';pushStatus.textContent='Allow notifications in Android app or Chrome site settings, then tap Check notification permission.';
  }else if(permission!=='granted'){
   pushBtn.textContent='Enable notifications';pushStatus.textContent='Permission has not been granted.';
  }else if(pushSubscriptionPresent===null){
   pushBtn.textContent='Checking notifications…';pushStatus.textContent='Checking permission and subscription.';
  }else if(pushSubscriptionPresent){
   pushBtn.textContent='Notifications on';pushStatus.textContent='Permission allowed · push subscription active.';
  }else{
   pushBtn.textContent=localStorage.getItem('vmboxChatPush')==='on'?'Reconnect notifications':'Enable notifications';
   pushStatus.textContent='Permission allowed · push subscription inactive.';
  }
  pushBtn.classList.toggle('on',permission==='granted'&&pushSubscriptionPresent===true);
 }
 async function syncPushSubscription(){
  if(!pushSupported||Notification.permission!=='granted')return false;
  try{
   swRegistration=swRegistration||await navigator.serviceWorker.register('/push-sw.js');
   const {publicKey}=await api('/v1/push/vapid-key');
   let sub=await swRegistration.pushManager.getSubscription();
   if(!sub)sub=await swRegistration.pushManager.subscribe({userVisibleOnly:true,applicationServerKey:urlB64ToBytes(publicKey)});
   await api('/v1/push/subscriptions','PUT',{},{endpoint:sub.endpoint,keys:{p256dh:btoa(String.fromCharCode(...new Uint8Array(sub.getKey('p256dh')))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,''),auth:btoa(String.fromCharCode(...new Uint8Array(sub.getKey('auth')))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'')},userAgent:navigator.userAgent.slice(0,200)});
   pushSubscriptionPresent=true;localStorage.setItem('vmboxChatPush','on');renderPushState();return true;
  }catch(e){pushSubscriptionPresent=false;renderPushState();pushStatus.textContent='Could not connect push: '+e.message;return false}
 }
 async function checkPushState({repair=false}={}){
  if(!pushSupported){renderPushState();return}
  if(Notification.permission!=='granted'){pushSubscriptionPresent=false;renderPushState();return}
  pushCheck.disabled=true;
  try{
   swRegistration=swRegistration||await navigator.serviceWorker.register('/push-sw.js');
   const sub=await swRegistration.pushManager.getSubscription();
   pushSubscriptionPresent=!!sub;
   if(sub)localStorage.setItem('vmboxChatPush','on');
   if(repair&&(sub||localStorage.getItem('vmboxChatPush')==='on'))await syncPushSubscription();
   else renderPushState();
  }catch(e){pushSubscriptionPresent=false;renderPushState();pushStatus.textContent='Could not check push: '+e.message}
  finally{pushCheck.disabled=false}
 }
 async function disablePushSubscription(){
  if(!pushSupported)return true;
  try{
   swRegistration=swRegistration||await navigator.serviceWorker.register('/push-sw.js');
   const sub=await swRegistration.pushManager.getSubscription();
   if(sub){await api('/v1/push/subscriptions','DELETE',{},{endpoint:sub.endpoint});await sub.unsubscribe()}
   pushSubscriptionPresent=false;localStorage.setItem('vmboxChatPush','off');renderPushState();return true;
  }catch(e){pushStatus.textContent='Could not disable push: '+e.message;return false}
 }
 pushBtn.onclick=async()=>{
  pushBtn.disabled=true;
  let error='';
  try{
   if(pushSubscriptionPresent){
    if(!await disablePushSubscription())error=pushStatus.textContent;
    return;
   }
   const permission=Notification.permission==='granted'?'granted':await Notification.requestPermission();
   if(permission!=='granted'){renderPushState();return}
   localStorage.setItem('vmboxChatPush','on');
   await syncPushSubscription();
  }catch(e){error='Could not update push: '+e.message}
  finally{pushBtn.disabled=false;renderPushState();if(error)pushStatus.textContent=error}
 };
 pushCheck.onclick=()=>void checkPushState({repair:true});
 document.addEventListener('visibilitychange',()=>{if(!document.hidden&&!appEl.hidden)void checkPushState({repair:true})});
 navigator.serviceWorker?.addEventListener('message',event=>{
  if(event.data?.type==='vmbox-push'){clearTimeout(pushTimer);pushTimer=setTimeout(()=>{if(document.hidden)return;void refreshMessages();void refreshPairMessages();void loadBoxes(true)},250)}
  if(event.data?.type==='vmbox-open'&&event.data.url){const url=new URL(event.data.url,location.origin);if(url.hash!==location.hash)location.hash=url.hash}
 });

 /* ---------- polling ---------- */
 function schedule(){
  clearTimeout(boxTimer);clearTimeout(msgTimer);
  boxTimer=setTimeout(tickBoxes,30000);
  msgTimer=setTimeout(tickMessages,3000);
 }
 async function tickBoxes(){try{if(!document.hidden)await loadBoxes()}catch{}boxTimer=setTimeout(tickBoxes,30000)}
 async function tickMessages(){try{if(!document.hidden){if(selected)await refreshMessages();if(selectedPair)await refreshPairMessages()}reconnecting=false}catch(e){reconnecting=!!(selected||selectedPair);if(selectedPair)statusEl.textContent=e.message}if(selected)updateBanner();msgTimer=setTimeout(tickMessages,3000)}
 document.addEventListener('visibilitychange',()=>{if(!document.hidden){clearTimeout(boxTimer);clearTimeout(msgTimer);void tickBoxes();void tickMessages()}});
 filterEl.addEventListener('input',()=>{clearTimeout(filterTimer);filterTimer=setTimeout(renderRows,130)});
 $('#refresh').onclick=async()=>{try{await loadBoxes(true);if(selected)await refreshMessages(true);if(selectedPair)await refreshPairMessages(true);$('#error').textContent=''}catch(e){$('#error').textContent=e.message}};

 /* ---------- auth ---------- */
 function showLogin(message=''){$('#login').hidden=false;$('#login-error').textContent=message;$('#login-token').focus()}
 $('#login').onsubmit=async event=>{
  event.preventDefault();
  $('#login-error').textContent='';
  try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+event.target.elements.token.value});event.target.reset();await enter()}catch(e){showLogin(e.message)}
 };
 $('#logout').onclick=async()=>{
  groupStorageKey='';
  clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);clearInterval(usageTimer);stopManualUsageRefresh();
  await disablePushSubscription();
  $('#usage-modal').hidden=true;
  usageGeneration++;usagePending=null;usageProfiles=[];usageLoaded=false;usageScope=null;selectedUsageProfile=null;chatUsageRequest++;$('#usage-list').replaceChildren();$('#usage-status').textContent='';
  $('#usage-toggle').hidden=true;$('#usage-toggle').textContent='Usage';$('#chat-usage').hidden=true;owner=false;
  closeTakeover();
  inspectOpen=false;inspect.hidden=true;clearInterval(inspectTimer);controllerPing=null;
  try{await api('/v1/browser-session','DELETE')}catch{}
  releaseImageURLs();
  for(const cached of avatarCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  for(const cached of tvShotCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  avatarCache.clear();previewFetched.clear();tvReplayCache.clear();hideTvPreview();headerAvatarKey='';
  for(const drafts of attachmentDrafts.values())for(const draft of drafts)URL.revokeObjectURL(draft.url);
  attachmentDrafts.clear();renderDrafts();pendingKey='';pendingFingerprint='';
  boxes.clear();rows.clear();pairs.clear();pairRows.clear();listEl.replaceChildren();messagesEl.replaceChildren();delete messagesEl.dataset.box;delete messagesEl.dataset.pair;
  owner=false;extrasLoaded=false;chatCommands=[];mentionCache.clear();hideComposerPicker();closeSheets();
  selected='';selectedPair='';restoringTranscript=false;newMessagesBtn.hidden=true;scrollMemory.clear();followMemory.clear();lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').classList.remove('pair-view');
  $('#chat-app').hidden=true;$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;$('#logout').hidden=true;showLogin();
 };
 async function enter(initial=false){
  try{
   const who=await api('/v1/whoami');usageGeneration++;owner=who.role==='owner';await loadChatGroups(who.accountId||'default');
   document.querySelectorAll('[data-owner-nav]').forEach(link=>link.hidden=!owner);
   $('#usage-toggle').hidden=!owner;
   $('#presets-toggle').hidden=!owner;
   $('#commands-toggle').hidden=!owner;
   $('#ai-settings-toggle').hidden=!owner;
   $('#roles-toggle').hidden=!owner;
   $('#login').hidden=true;$('#login-error').textContent='';$('#error').textContent='';$('#logout').hidden=false;appEl.hidden=false;applySidebarWidth();applyThreadWidth();
   doodle('Loading chats…');
   try{await loadBoxes()}finally{doodle('')}
   const params=new URLSearchParams(location.hash.slice(1)),id=params.get('box'),pair=params.get('pair');
   if(pair&&owner){const match=[...pairs.keys()].find(key=>key===pair||key.split('/').reverse().join('/')===pair);if(match)await openPair(match)}
   else if(id&&boxes.has(id))await openBox(id);
   if(owner)void loadChatCommands().catch(e=>{if(selected)statusEl.textContent=e.message});
   if(owner){clearInterval(usageTimer);void refreshUsage();usageTimer=setInterval(()=>void refreshUsage(),60000)}
   schedule();renderInstallState();renderPushState();void checkPushState({repair:true});
  }catch(e){showLogin(initial&&e.message==='Please log in to the controller.'?'':e.message)}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);clearInterval(usageTimer);clearInterval(usageManualTimer);releaseImageURLs();for(const drafts of attachmentDrafts.values())for(const draft of drafts)URL.revokeObjectURL(draft.url)});

 /* ---------- imported profile usage ---------- */
 function usageNumber(value){return typeof value==='number'&&Number.isFinite(value)?new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(value):'—'}
 function usageDate(value){if(!value)return 'unknown';const date=new Date(value);return Number.isNaN(date.getTime())?'unknown':date.toLocaleString()}
 function usageCompactDate(value){if(!value)return 'unknown';const date=new Date(value);if(Number.isNaN(date.getTime()))return 'unknown';const options={month:'numeric',day:'numeric',hour:'numeric',minute:'2-digit'};if(date.getFullYear()!==new Date().getFullYear())options.year='numeric';return date.toLocaleString([],options)}
 function remainingPercent(used){return typeof used==='number'&&Number.isFinite(used)?Math.max(0,Math.min(100,100-used)):null}
 function lowestRemaining(profile){
  const remaining=(profile?.snapshot?.windows||[]).map(window=>remainingPercent(window.usedPercent)).filter(value=>value!==null);
  return remaining.length?Math.min(...remaining):null;
 }
 function renderChatUsage(){
  const button=$('#chat-usage');
  const ref=selectedUsageProfile;
  button.hidden=!owner||!selected||!ref||!usageLoaded;
  if(button.hidden)return;
  const profile=usageProfiles.find(item=>item.application===ref.application&&item.name===ref.name);
  const lowest=lowestRemaining(profile);
  const label=mk('span','Usage · ');label.className='chat-usage-label';
  button.replaceChildren(label,mk('span',lowest===null?'unavailable':usageNumber(lowest)+'% left'));
  button.setAttribute('aria-label',ref.application+' '+ref.name+' usage: '+(lowest===null?'remaining unavailable':usageNumber(lowest)+'% remaining'));
  button.title=ref.application+' · '+ref.name+(lowest===null?' · No remaining usage reported':' · Lowest reported remaining window: '+usageNumber(lowest)+'%. Open this profile’s usage details.');
  button.classList.toggle('usage-low',lowest!==null&&lowest<=20);
 }
 async function loadChatUsageProfile(id){
  const request=++chatUsageRequest;
  try{
   const state=await api(boxPath(id)+'/imported-credentials');
   if(request!==chatUsageRequest||selected!==id||!owner)return;
   const agent=boxes.get(id)?.defaultAgent;
   selectedUsageProfile=(state.profiles||[]).find(ref=>ref.application===agent&&['claude','codex','opencode'].includes(ref.application))||null;
  }catch{if(request!==chatUsageRequest||selected!==id||!owner)return;selectedUsageProfile=null}
  renderChatUsage();
 }
 function renderUsage(data){
  const root=$('#usage-list');root.replaceChildren();
  const profiles=Array.isArray(data?.profiles)?data.profiles:[];
  usageProfiles=profiles;
  if(data?.loaded)usageLoaded=true;
  profileUsageLoadError=false;renderCreateProfileUsage();
  renderChatUsage();
  $('#usage-title').textContent=usageScope?'Profile usage · '+usageScope.application+' · '+usageScope.name:'Profile usage limits';
  const visible=usageScope?profiles.filter(profile=>profile.application===usageScope.application&&profile.name===usageScope.name):profiles;
  if(!visible.length){root.append(mk('p',usageScope?'No usage data for this chat’s profile yet.':'No saved agent profiles yet.'));return}
  for(const profile of visible){
   const card=mk('article');card.className='usage-profile';
   const heading=mk('h3',profile.application+' · '+profile.name);card.append(heading);
   const boxes=(profile.boxes||[]).length?'Running: '+profile.boxes.join(', '):'No running box';
   const source=profile.snapshot?.source?' · Source: '+profile.snapshot.source:'';
   card.append(mk('p',boxes+source));
   const snapshot=profile.snapshot;
   for(const window of snapshot?.windows||[]){
    const row=mk('div');row.className='usage-window';
    const names={session:'Current session',weekly_all:'Current week (all models)',weekly_scoped:'Current week',primary:'Primary',secondary:'Secondary'};
    const label=[names[window.name]||window.name,window.scope,window.group&&window.group!==window.name?(names[window.group]||window.group):null,window.durationMinutes?window.durationMinutes+' min':null].filter(Boolean).join(' · ');
    const remaining=remainingPercent(window.usedPercent),head=mk('div');head.className='usage-window-head';
    head.append(mk('span',label),mk('strong',remaining===null?'Remaining unavailable':usageNumber(remaining)+'% remaining'));row.append(head);
    if(remaining!==null){const track=mk('div');track.className='usage-track';track.setAttribute('role','progressbar');track.setAttribute('aria-label',label+' remaining');track.setAttribute('aria-valuemin','0');track.setAttribute('aria-valuemax','100');track.setAttribute('aria-valuenow',String(remaining));const fill=mk('span');fill.style.width=remaining+'%';track.append(fill);row.append(track)}
    if(window.resetsAt){const reset=mk('small','Resets '+usageCompactDate(window.resetsAt));reset.title='Resets '+usageDate(window.resetsAt);row.append(reset)}
    card.append(row);
   }
   if(snapshot?.spend){
    const spend=snapshot.spend,unit=spend.currency||spend.unit||'',remaining=typeof spend.remaining==='number'?spend.remaining:typeof spend.limit==='number'&&typeof spend.used==='number'?Math.max(0,spend.limit-spend.used):null;
    const parts=[];
    if(spend.used!=null)parts.push('used '+usageNumber(spend.used));
    if(spend.limit!=null)parts.push('limit '+usageNumber(spend.limit));
    const summary=mk('p');summary.className='usage-spend';summary.append(mk('strong',remaining===null?'Spend remaining unavailable':usageNumber(remaining)+(unit?' '+unit:'')+' remaining'));
    summary.append(document.createTextNode((parts.length?' · '+parts.join(' · '):'')+(spend.period?' · '+spend.period:'')));card.append(summary);
   }
   if(snapshot?.balances?.length)card.append(mk('p','Available balances: '+snapshot.balances.map(balance=>balance.unit+' '+usageNumber(balance.amount)).join(' · ')));
   if(snapshot?.rateCaps?.length){card.append(mk('p','Configured rate caps (remaining requests unavailable):'));const list=mk('ul');list.className='usage-caps';for(const cap of snapshot.rateCaps)list.append(mk('li',[cap.model,cap.type,usageNumber(cap.amount)].filter(Boolean).join(' · ')));card.append(list)}
   if(snapshot?.note)card.append(mk('p',snapshot.note));
   if(profile.error)card.append(mk('p','Last check failed: '+profile.error));
   const times=[];
   if(profile.observedAt)times.push('Observed '+usageCompactDate(profile.observedAt));
   if(profile.checkedAt)times.push('Checked '+usageCompactDate(profile.checkedAt));
   const timing=mk('small',times.length?times.join(' · '):'Waiting for first check');timing.className='usage-profile-times';timing.title=[profile.observedAt?'Observed '+usageDate(profile.observedAt):'',profile.checkedAt?'Last checked '+usageDate(profile.checkedAt):''].filter(Boolean).join(' · ');card.append(timing);
   root.append(card);
  }
 }
 let usagePending=null,usageGeneration=0;
 async function refreshUsage(){
  if(!owner||document.hidden)return;
  if(usagePending)return usagePending;
  const status=$('#usage-status');
  if(!$('#usage-modal').hidden&&!usageManualBaseline)status.textContent='Loading…';
  const generation=usageGeneration;
  const pending=api('/v1/profile-usage').then(data=>{
   if(generation!==usageGeneration||!owner)return;
   renderUsage({...data,loaded:true});
   if($('#usage-modal').hidden)return data;
   if(usageManualBaseline){
    const profiles=Array.isArray(data?.profiles)?data.profiles:[];
    const done=profiles.every(profile=>{
     const previous=usageManualBaseline.get(profile.application+'\0'+profile.name);
     return profile.checkedAt&&new Date(profile.checkedAt).getTime()>new Date(previous||0).getTime();
    });
    if(done){stopManualUsageRefresh();status.textContent='Usage updated.'}
    else if(Date.now()-usageManualStarted>180000){stopManualUsageRefresh();status.textContent='Checks are taking longer; results will continue to update.'}
    else status.textContent='Checking profiles…';
   }else status.textContent='';
   return data;
  }).catch(error=>{
   if(generation!==usageGeneration||!owner)return;
   profileUsageLoadError=true;renderCreateProfileUsage();
   if(usageManualBaseline&&Date.now()-usageManualStarted>180000)stopManualUsageRefresh();
   if(!$('#usage-modal').hidden)status.textContent=error.message;
  }).finally(()=>{if(usagePending===pending)usagePending=null});
  usagePending=pending;return pending;
 }
 function stopManualUsageRefresh(){clearInterval(usageManualTimer);usageManualBaseline=null;$('#usage-refresh').disabled=false}
 async function requestUsageRefresh(){
  if(!owner||usageManualBaseline)return;
  const button=$('#usage-refresh'),status=$('#usage-status'),generation=usageGeneration;button.disabled=true;
  try{
   const before=await refreshUsage();
   if(generation!==usageGeneration||!owner||$('#usage-modal').hidden)return;
   const result=await api('/v1/profile-usage/refresh','POST');
   if(generation!==usageGeneration||!owner||$('#usage-modal').hidden)return;
   if(!result?.profiles){status.textContent='No saved agent profiles to check.';return}
   usageManualBaseline=new Map((before?.profiles||[]).map(profile=>[profile.application+'\0'+profile.name,profile.checkedAt||'']));
   usageManualStarted=Date.now();status.textContent='Checking profiles…';
   usageManualTimer=setInterval(()=>void refreshUsage(),5000);
   await refreshUsage();
  }catch(error){if(generation===usageGeneration&&!$('#usage-modal').hidden)status.textContent=error.message}
  finally{if(!usageManualBaseline)button.disabled=false}
 }
 $('#usage-toggle').onclick=()=>{if(!owner)return;usageScope=null;renderUsage({profiles:usageProfiles});closeSheets();$('#usage-modal').hidden=false;void refreshUsage()};
 $('#chat-usage').onclick=()=>{if(!owner||!selectedUsageProfile)return;usageScope={...selectedUsageProfile};renderUsage({profiles:usageProfiles});closeSheets();$('#usage-modal').hidden=false;void refreshUsage()};
 $('#usage-refresh').onclick=()=>void requestUsageRefresh();
 document.querySelectorAll('#usage-modal [data-close]').forEach(el=>el.addEventListener('click',stopManualUsageRefresh));
 document.addEventListener('visibilitychange',()=>{if(!document.hidden)void refreshUsage()});

 /* ---------- instruction presets, box instructions, imported profiles ---------- */
 const conciseInstructions='## Concise responses\n\nDo the requested work fully. In messages, use as few tokens as needed for a complete, correct answer. Write short, direct sentences. Omit filler, repetition, and unrequested background.\n';
 function addConciseInstructions(markdown){if(markdown.includes(conciseInstructions))return markdown;const separator=!markdown||markdown.endsWith('\n\n')?'':markdown.endsWith('\n')?'\n':'\n\n';return markdown+separator+conciseInstructions}
 function mk(tag,text){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el}
 function mdPreview(root,text){root.replaceChildren();root.append(typeof window.markdownToNodes==='function'?window.markdownToNodes(text||''):mk('pre',text||''))}
 document.querySelectorAll('[data-close]').forEach(el=>el.addEventListener('click',()=>{const sheet=el.closest('.sheet');if(sheet)sheet.hidden=true}));
 function closeSheets(){stopManualUsageRefresh();document.querySelectorAll('.sheet').forEach(sheet=>{sheet.hidden=true});closeAllMsgActions();closeForwardMenu();closeRowMenu()}

 /* ---------- saved Chat slash commands ---------- */
 let selectedCommandName='';
 async function loadChatCommands(){
  const value=await api('/v1/chat-commands');chatCommands=Array.isArray(value)?value:[];renderChatCommands();
  if(composerToken()?.kind==='/')void updateComposerPicker();
 }
 function selectChatCommand(command){
  const form=$('#command-form');selectedCommandName=command?.name||'';
  form.elements.name.value=command?.name||'';
  form.elements.name.readOnly=!!command;
  form.elements.prompt.value=command?.prompt||'';
  $('#command-form-title').textContent=command?'Edit /'+command.name:'Create a command';
  $('#command-editor-help').textContent=command?'Update this saved prompt or insert it into the open chat.':'Give your prompt a short name and save it for every box chat.';
  $('#command-use').hidden=!command;
  $('#command-delete').hidden=!command;
  $('#command-form button[type="submit"]').textContent=command?'Save changes':'Save command';
  $('#command-delete-confirm').hidden=true;
  $('#command-status').textContent='';
  renderChatCommands();
 }
 function renderChatCommands(){
  const root=$('#command-list'),query=$('#command-filter').value.trim().toLocaleLowerCase();root.replaceChildren();
  $('#command-count').textContent=chatCommands.length+' saved';
  $('#command-new').hidden=!chatCommands.length;
  if(!chatCommands.length){
   const empty=mk('div');empty.className='command-empty';empty.innerHTML='<span aria-hidden="true">/</span><strong>No commands yet</strong><p>Save a prompt you use often to insert it from any chat.</p>';
   root.append(empty);return;
  }
  const filtered=chatCommands.filter(command=>!query||command.name.toLocaleLowerCase().includes(query)||command.prompt.toLocaleLowerCase().includes(query));
  if(!filtered.length){root.append(Object.assign(mk('p','No commands match your search.'),{className:'command-no-results'}));return}
  for(const command of filtered){
   const row=mk('button');row.type='button';row.className='command-row';row.setAttribute('aria-pressed',String(command.name===selectedCommandName));
   row.append(mk('strong','/'+command.name),mk('small',command.prompt));
   row.onclick=()=>selectChatCommand(command);root.append(row);
  }
 }
 $('#commands-toggle').onclick=async()=>{
  closeSheets();$('#commands-modal').hidden=false;$('#command-filter').value='';$('#command-status').textContent='';
  try{await loadChatCommands();selectChatCommand(chatCommands.find(command=>command.name===selectedCommandName)||chatCommands[0]||null);(chatCommands.length?$('#command-filter'):$('#command-form input[name="name"]')).focus()}catch(e){$('#command-status').textContent=e.message}
 };
 $('#ai-settings-toggle').onclick=()=>{closeSheets();window.VMBoxAIHelper.openSettings()};
 $('#command-filter').addEventListener('input',renderChatCommands);
 $('#command-new').onclick=()=>{selectChatCommand(null);$('#command-form input[name="name"]').focus()};
 $('#command-use').onclick=()=>{
  const command=chatCommands.find(item=>item.name===selectedCommandName);if(!command)return;
  if(!selected){$('#command-status').textContent='Open a box chat to use this command.';return}
  const existing=inputEl.value;
  inputEl.value=existing+(existing&&!existing.endsWith('\n')?'\n\n':'')+'/'+command.name;
  inputEl.dispatchEvent(new Event('input',{bubbles:true}));
  $('#commands-modal').hidden=true;inputEl.focus();inputEl.setSelectionRange(inputEl.value.length,inputEl.value.length);
 };
 $('#command-delete').onclick=()=>{
  if(!selectedCommandName)return;
  $('#command-delete-question').textContent='Delete /'+selectedCommandName+'?';
  $('#command-delete-confirm').hidden=false;
  $('#command-delete-yes').focus();
 };
 $('#command-delete-no').onclick=()=>{$('#command-delete-confirm').hidden=true;$('#command-delete').focus()};
 $('#command-delete-yes').onclick=async()=>{
  const name=selectedCommandName;if(!name)return;
  try{await api('/v1/chat-commands/'+encodeURIComponent(name),'DELETE');selectedCommandName='';await loadChatCommands();selectChatCommand(chatCommands[0]||null);$('#command-status').textContent='Deleted /'+name+'.'}
  catch(e){$('#command-status').textContent=e.message}
 };
 $('#command-form').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,name=form.elements.name.value.trim(),prompt=form.elements.prompt.value;
  if(!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(name)){ $('#command-status').textContent='Use 1–40 lowercase letters, digits, hyphens, or underscores.';return}
  if(!selectedCommandName&&chatCommands.some(command=>command.name===name)){$('#command-status').textContent='/'+name+' already exists. Select it in the list to edit.';return}
  try{await api('/v1/chat-commands/'+encodeURIComponent(name),'PUT',{}, {prompt});selectedCommandName=name;await loadChatCommands();selectChatCommand(chatCommands.find(command=>command.name===name)||{name,prompt});$('#command-status').textContent='Saved /'+name+'.'}catch(e){$('#command-status').textContent=e.message}
 };


 /* ---------- direct per-box agent permissions ---------- */
 function syncMCPToolGroups(form=$('#role-editor-form')){const restart=form.querySelector('input[name=mcpTools][value=restart_agent_box]'),wake=form.querySelector('input[name=mcpTools][value=wake_agent_box]');if(restart&&wake){wake.disabled=restart.checked;if(restart.checked)wake.checked=true}for(const group of form.querySelectorAll('.mcp-tool-group')){const tools=[...group.querySelectorAll('input[name=mcpTools]')],toggle=group.querySelector('.mcp-tool-group-toggle'),selected=tools.filter(input=>input.checked).length;toggle.checked=selected===tools.length;toggle.indeterminate=selected>0&&selected<tools.length}}
 function changeMCPToolGroup(toggle){for(const input of toggle.closest('.mcp-tool-group').querySelectorAll('input[name=mcpTools]'))input.checked=toggle.checked;syncMCPToolGroups(toggle.form)}
 function populatePolicyEditor(box,cap={}){
  const form=$('#role-editor-form');form.reset();form.elements.id.value=box.id;form.elements.name.value=box.name;
  const set=(name,value)=>{if(value===undefined||value===null)return;const input=form.elements[name];if(input.type==='number'&&input.min!==''&&Number(value)<Number(input.min))return;input.value=String(value)};
  form.elements.allContactsEnabled.checked=!!cap.allContacts?.enabled;set('maxBoxes',cap.createAgentBox?.maxBoxes);set('maxDiskGiB',cap.createAgentBox?.maxDiskGiB);
  const allowedAgents=new Set(cap.createAgentBox?.allowedAgents||[]);form.querySelectorAll('input[name=allowedAgents]').forEach(input=>input.checked=!allowedAgents.size||allowedAgents.has(input.value));
  const allowedMCP=new Set(cap.mcpTools?.enabled?(cap.mcpTools.allowedTools||[]):[]);form.querySelectorAll('input[name=mcpTools]').forEach(input=>input.checked=allowedMCP.has(input.value));form.querySelectorAll('.role-capability-options').forEach(details=>details.open=false);syncMCPToolGroups(form);$('#role-editor-modal').hidden=false;
 }
 const policySummaries=new Map();let policySummaryEpoch=0;
 const summaryToolGroups={
  computer:['take_screenshot','capture_window','move_mouse','click_mouse','drag_mouse','scroll_mouse','type_text','press_keys'],
  passwords:['secret_request','generate_password','type_secret'],
  admin:['list_agent_boxes','get_agent_box','get_agent_box_screenshot','set_agent_box_tags','restart_agent_box','wake_agent_box','delete_agent_box']
 };
 function policySummaryCell(kind,policy){
  const cell=mk('div');cell.className='role-perm-cell';cell.append(mk('small',kind==='contacts'?'Contacts':kind==='computer'?'Computer':kind==='passwords'?'Passwords':kind==='admin'?'Admin':'Create'));
  const value=mk('strong');cell.append(value);
  if(!policy){value.textContent='Loading…';value.className='unknown';return cell}
  if(policy.error){value.textContent='Unavailable';value.className='unknown';return cell}
  const cap=policy.capabilities||{},allowed=new Set(cap.mcpTools?.enabled?cap.mcpTools.allowedTools||[]:[]);
  if(kind==='contacts'){value.textContent=cap.allContacts?.enabled?'All boxes':'Direct only';value.className=cap.allContacts?.enabled?'on':'limited';return cell}
  if(kind==='create'){const on=!!cap.createAgentBox?.enabled&&allowed.has('create_agent_box');value.textContent=on?'Allowed':'None';value.className=on?'on':'off';return cell}
  const group=summaryToolGroups[kind],count=group.filter(tool=>allowed.has(tool)).length;
  value.textContent=count===0?'None':count===group.length?'All '+count+'/'+group.length:'Some '+count+'/'+group.length;
  value.className=count?'on':'off';return cell;
 }
 function permissionBoxes(){return [...boxes.values()].sort((a,b)=>a.name.localeCompare(b.name)||a.id.localeCompare(b.id))}
 function renderPermissionBoxes(){
  const root=$('#role-assignments');if(!root)return;root.replaceChildren();
  const query=($('#role-box-search')?.value||'').trim().toLocaleLowerCase(),values=permissionBoxes().filter(box=>!query||box.name.toLocaleLowerCase().includes(query));
  if(!values.length){root.append(mk('p',query?'No boxes match this search.':'No boxes available.'));return}
  const header=mk('div');header.className='role-matrix-head';for(const label of ['Box / agent','Contacts','Computer','Passwords','Admin','Create',''])header.append(mk('span',label));root.append(header);
  const list=mk('div');list.className='role-assignment-list';
  for(const box of values){
   const card=mk('article');card.className='role-assignment-card';card.dataset.state=box.state;card.dataset.roleBoxId=box.id;
   const summary=mk('div');summary.className='role-assignment-summary';
   const mark=mk('span',(box.name||'?').slice(0,1).toUpperCase());mark.className='role-box-mark';mark.setAttribute('aria-hidden','true');
   const identity=mk('div');identity.className='role-assignment-identity';identity.append(mk('strong',box.name));const meta=mk('div');meta.className='role-box-meta';meta.append(Object.assign(mk('span',box.defaultAgent||'agent'),{className:'role-agent-badge'}),Object.assign(mk('span',box.state),{className:'role-state-badge'}));identity.append(meta);
   const who=mk('div');who.className='role-box-identity';who.append(mark,identity);summary.append(who);
   for(const kind of ['contacts','computer','passwords','admin','create'])summary.append(policySummaryCell(kind,policySummaries.get(box.id)));
   const manage=mk('button','Edit');manage.type='button';manage.className='role-assignment-toggle';manage.setAttribute('aria-label','Edit permissions for '+box.name);manage.onclick=()=>void openBoxPolicyEditor(box.id);
   summary.append(manage);card.append(summary);list.append(card);
  }
  root.append(list);
 }
 async function loadPermissionSummaries(){
  const epoch=++policySummaryEpoch,queue=permissionBoxes();
  let next=0;
  await Promise.all(Array.from({length:Math.min(5,queue.length)},async()=>{
   while(next<queue.length){
    const box=queue[next++];
    let policy;try{policy=await api(boxPath(box.id)+'/agent-policy')}
    catch(error){policy={error:error.message}}
    if(epoch!==policySummaryEpoch)return;
    policySummaries.set(box.id,policy);
    if(epoch===policySummaryEpoch&&!$('#roles-modal').hidden)renderPermissionBoxes();
   }
  }));
  if(epoch===policySummaryEpoch&&!$('#roles-modal').hidden){const failures=queue.filter(box=>policySummaries.get(box.id)?.error).length;$('#role-status').textContent=failures?failures+' policy summaries are unavailable. Select Edit to retry.':'Select Edit for individual tools and limits.'}
 }
 async function openPermissionsModal(){
  if(!owner)return;closeSheets();$('#roles-modal').hidden=false;$('#role-box-search').value='';$('#role-status').textContent='Loading box permissions…';await loadBoxes(true);renderPermissionBoxes();void loadPermissionSummaries();
 }
 async function openBoxPolicyEditor(boxID){
  if(!owner||!boxID)return;const box=boxes.get(boxID);if(!box)return;
  $('#role-editor-modal').hidden=false;$('#role-editor-title').textContent='Permissions · '+box.name;$('#role-editor-status').textContent='Loading permissions…';
  try{
   const policy=await api(boxPath(boxID)+'/agent-policy');policySummaries.set(boxID,policy);if(!$('#roles-modal').hidden)renderPermissionBoxes();
   populatePolicyEditor(box,policy.capabilities||{});
   $('#role-editor-title').textContent='Permissions · '+box.name;$('#role-assigned-count').textContent=policy.migratedFromRoles?'Existing role grants are shown below; saving converts them into this box’s direct policy.':'Changes sync automatically to the running MCP client.';$('#delete-role').hidden=true;$('#role-editor-status').textContent='';
  }catch(e){$('#role-editor-status').textContent=e.message}
 }
 function directPolicyBody(form){
  const f=form.elements,num=name=>Number.parseInt(f[name].value,10)||0,allowedTools=[...form.querySelectorAll('input[name=mcpTools]:checked')].map(input=>input.value),hasTool=name=>allowedTools.includes(name);
  const chosenAgents=[...form.querySelectorAll('input[name=allowedAgents]:checked')].map(input=>input.value);
  return {capabilities:{allContacts:{enabled:f.allContactsEnabled.checked},createAgentBox:{enabled:hasTool('create_agent_box'),maxBoxes:num('maxBoxes'),maxDiskGiB:num('maxDiskGiB'),allowedAgents:chosenAgents.length?chosenAgents:['codex','claude','opencode']},manageAgentBoxes:{list:hasTool('list_agent_boxes'),inspect:hasTool('get_agent_box')||hasTool('get_agent_box_screenshot'),tag:hasTool('set_agent_box_tags'),restart:hasTool('restart_agent_box')||hasTool('wake_agent_box'),delete:hasTool('delete_agent_box')},mcpTools:{enabled:true,allowedTools}}};
 }
 $('#roles-toggle').onclick=()=>void openPermissionsModal();
 $('#inspect-edit-roles').onclick=()=>void openBoxPolicyEditor(selected);
 $('#role-box-search').addEventListener('input',renderPermissionBoxes);
 document.querySelectorAll('#role-editor-form .mcp-tool-group-toggle').forEach(input=>input.addEventListener('change',()=>changeMCPToolGroup(input)));
 document.querySelectorAll('#role-editor-form input[name=mcpTools]').forEach(input=>input.addEventListener('change',()=>syncMCPToolGroups(input.form)));
 $('#role-editor-form').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,boxID=form.elements.id.value,status=$('#role-editor-status');if(!boxID)return;
  status.textContent='Saving permissions…';
  try{const body=directPolicyBody(form),policy=await api(boxPath(boxID)+'/agent-policy','PUT',{},body);policySummaryEpoch++;policySummaries.set(boxID,policy?.capabilities?policy:body);if(!$('#roles-modal').hidden){renderPermissionBoxes();void loadPermissionSummaries()}status.textContent='Saved. Running MCP clients refresh their tools automatically.';$('#role-editor-modal').hidden=true;toast('Permissions saved and synced.');if(selected===boxID){inspectContactsFor='';const limit=$('#inspect-create-limit');limit.replaceChildren();delete limit.dataset.createLimitBox;renderInspect()}}
  catch(e){status.textContent=e.message}
 };
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
  if(!instructionPresets.presets.length){root.append(mk('p','No presets.'));return}
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
  closeSheets();
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
  try{await api('/v1/instruction-presets-default','PUT',{},{name});status.textContent=name?'Account default preset: '+name+'.':'Account default preset cleared.';await openPresetsModal()}
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
   const saved=await api('/v1/instruction-presets/'+encodeURIComponent(name),'PUT',{},{markdown});
   status.textContent='Saved '+name+' · r'+saved.preset.revision+'.';
   event.target.reset();$('#preset-preview').hidden=true;await openPresetsModal();
  }catch(e){status.textContent=e.message}
 };
 function renderCreateInstructionChoice(){
  const select=$('#create-instructions'),previous=select.value;select.replaceChildren();
  const auto=mk('option',instructionPresets.defaultName?'Default · '+instructionPresets.defaultName:'Default / none');auto.value='auto';select.append(auto);
  const none=mk('option','None');none.value='none';select.append(none);
  const concise=mk('option','Concise responses');concise.value='__concise__';select.append(concise);
  for(const preset of instructionPresets.presets){const option=mk('option',preset.name+(preset.default?' · default':''));option.value=preset.name;select.append(option)}
  const custom=mk('option','Custom Markdown');custom.value='custom';select.append(custom);
  if([...select.options].some(option=>option.value===previous))select.value=previous;
  void syncCreateInstructionText();
 }
 async function syncCreateInstructionText(){
  const select=$('#create-instructions'),editor=$('#create-instructions-editor'),textarea=$('#create-instructions-custom'),preview=$('#create-instructions-preview');
  const value=select.value;
  if(value==='auto'||value==='none'){
   textarea.value='';textarea.readOnly=true;createInstructionSource=value;
   editor.hidden=true;editor.open=false;
   mdPreview(preview,'');preview.hidden=true;return;
  }
  if(value==='custom'||value==='__concise__'){
   textarea.readOnly=false;
   if(createInstructionSource!==value){textarea.value=value==='__concise__'?addConciseInstructions(textarea.value):'';createInstructionSource=value;editor.open=true}
   editor.hidden=false;
   mdPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();return;
  }
  let body='';try{body=await presetBody(value)}catch{body=''}
  textarea.readOnly=false;
  if(createInstructionSource!==value){textarea.value=body;createInstructionSource=value;editor.open=false}
  editor.hidden=false;
  mdPreview(preview,textarea.value);preview.hidden=!textarea.value.trim();
 }
 $('#create-instructions').addEventListener('change',()=>void syncCreateInstructionText());
 $('#create-instructions-custom').addEventListener('input',()=>{const preview=$('#create-instructions-preview'),text=$('#create-instructions-custom').value;mdPreview(preview,text);preview.hidden=!text.trim()});
 async function createInstructionSelection(){
  const value=$('#create-instructions').value,markdown=$('#create-instructions-custom').value;
  if(value==='auto')return null;
  if(value==='none')return {none:true};
  if(value==='custom'||value==='__concise__'){if(!markdown.trim())throw Error('Enter the custom instruction Markdown or choose another source.');return {markdown}}
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
  closeSheets();
  boxInstructionTarget=box;
  const status=$('#box-instructions-status');status.textContent='Loading…';
  $('#box-instructions-title').textContent='Instructions · '+box.name;
  const select=$('#box-instructions-preset');select.replaceChildren();
  try{
   const state=await api(boxPath(box.id)+'/instructions'),current=state.instructions||{source:'none',markdown:''};
   const none=mk('option','No custom instructions (chat conventions only)');none.value='';select.append(none);
   const concise=mk('option','Concise responses');concise.value='__concise__';select.append(concise);
   for(const preset of instructionPresets.presets){const option=mk('option',preset.name+' · r'+preset.revision);option.value=preset.name;select.append(option)}
   const custom=mk('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
   select.value=current.source==='preset'&&instructionPresets.presets.some(p=>p.name===current.preset)?current.preset:(current.source==='custom'?(current.markdown===conciseInstructions?'__concise__':'custom'):'');
   $('#box-instructions-markdown').value=current.markdown||'';
   mdPreview($('#box-instructions-preview'),current.markdown||'');
   $('#box-instructions-effective').textContent=state.effectiveMarkdown||'';
   $('#box-instructions-current').textContent=describeBoxInstructions(state);
   status.textContent='';$('#box-instructions-modal').hidden=false;
  }catch(e){status.textContent=e.message}
 }
 $('#box-instructions-preset').addEventListener('change',async()=>{
  const select=$('#box-instructions-preset'),textarea=$('#box-instructions-markdown'),preview=$('#box-instructions-preview');
  if(select.value===''){textarea.value='';mdPreview(preview,'');return}
  if(select.value==='__concise__'){textarea.value=addConciseInstructions(textarea.value);mdPreview(preview,textarea.value);return}
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
   else if(select.value==='custom'||select.value==='__concise__'){if(!markdown.trim())throw Error('Enter the custom Markdown or choose another source.');body={markdown}}
   else{const preset=await presetBody(select.value);body=markdown.trim()&&markdown!==preset?{preset:select.value,markdown}:{preset:select.value}}
   status.textContent='Applying…';
   const result=await api(boxPath(boxInstructionTarget.id)+'/instructions','PUT',{},body);
   if(inspectOpen&&selected===boxInstructionTarget.id){inspectInstructionsRequest++;inspectInstructionsFor=boxInstructionTarget.id;inspectInstructions=result;renderInspect()}
   status.textContent=result.note||describeBoxInstructions(result);
   if(result.instructions)$('#box-instructions-current').textContent=describeBoxInstructions(result);
   $('#box-instructions-effective').textContent=result.effectiveMarkdown||'';
   toast('Instructions applied to '+boxInstructionTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };
 async function openBoxCredentials(box){
  closeSheets();
  boxCredentialTarget=box;
  const status=$('#box-credentials-status');status.textContent='Loading…';
  $('#box-credentials-title').textContent='Imported profiles · '+box.name;
  try{
   const [state,profiles]=await Promise.all([api(boxPath(box.id)+'/imported-credentials'),api('/v1/login-profiles')]);
   const byApplication={};for(const profile of profiles)(byApplication[profile.application]??=[]).push(profile.name);
   const current=(state.profiles||[])[0];
   const wrap=$('#box-credentials-form');wrap.replaceChildren();
   const label=mk('label','Login profile ');label.className='field';const select=document.createElement('select');select.name='loginProfile';const empty=mk('option','None');empty.value='';select.append(empty);
   for(const application of ['claude','codex','opencode']){const names=(byApplication[application]||[]).slice().sort();if(!names.length)continue;const group=document.createElement('optgroup');group.label=application;for(const name of names){const option=mk('option',name);option.value=JSON.stringify({application,name});group.append(option)}select.append(group)}
   const currentValue=current&&JSON.stringify({application:current.application,name:current.name});if(currentValue&&[...select.options].some(option=>option.value===currentValue))select.value=currentValue;label.append(select);wrap.append(label);
   const parts=[(state.profiles||[]).length?'Imported: '+(state.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', '):'No imported login profiles recorded'];
   if((state.pending||[]).length)parts.push('Queued for next start: '+(state.pending||[]).map(ref=>ref.application+' · '+ref.name).join(', '));
   $('#box-credentials-current').textContent=parts.join(' · ')+'.';
   status.textContent='';$('#box-credentials-modal').hidden=false;
  }catch(e){status.textContent=e.message}
 }
 $('#box-credentials-apply').onclick=async()=>{
  if(!boxCredentialTarget)return;
  const status=$('#box-credentials-status'),profile=$('#box-credentials-form select')?.value,profiles=profile?[JSON.parse(profile)]:[];
  status.textContent='Applying credentials…';
  try{
   const result=await api(boxPath(boxCredentialTarget.id)+'/login-profiles','PUT',{},{profiles});
   status.textContent=result.note||'Saved.';
   $('#box-credentials-current').textContent=(result.profiles||[]).length?'Imported: '+(result.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', ')+'.':'No imported login profiles recorded.';
   if(selected===boxCredentialTarget.id){const agent=boxes.get(selected)?.defaultAgent;selectedUsageProfile=(result.profiles||[]).find(ref=>ref.application===agent&&['claude','codex','opencode'].includes(ref.application))||null;chatUsageRequest++;renderChatUsage()}
   if(inspectOpen&&selected===boxCredentialTarget.id){inspectProfilesFor='';maybeLoadInspectProfiles(boxCredentialTarget)}
   toast('Login profiles updated for '+boxCredentialTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };

 /* ---------- fixed vbox look ---------- */
 $('#chat-menu').onclick=()=>{closeSheets();$('#chat-menu-sheet').hidden=false};
 $('#logout').addEventListener('click',()=>{$('#chat-menu-sheet').hidden=true},{capture:true});
 applyVariant();

 void enter(true);
})();
