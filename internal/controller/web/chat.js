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
 const pins=(()=>{try{const saved=JSON.parse(localStorage.getItem('vmboxChatPins')||'[]');return new Set(Array.isArray(saved)?saved.filter(key=>typeof key==='string'):[])}catch{return new Set()}})();
 const pinKey=(kind,id)=>kind+':'+id;
 function togglePin(key){
  if(pins.has(key))pins.delete(key);else pins.add(key);
  try{localStorage.setItem('vmboxChatPins',JSON.stringify([...pins]))}catch{}
  renderRows();
 }
 // Unsent composer text is kept per box so switching chats (or reloading the
 // page) never loses what you were typing. Uploaded attachment drafts are also
 // keyed per box below; their local previews intentionally live only this page.
 const inputDrafts=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatInputDrafts')||'{}')}catch{return{}}})();
 const saveInputDrafts=()=>{try{localStorage.setItem('vmboxChatInputDrafts',JSON.stringify(inputDrafts))}catch{}};
 let inputDraftTimer=0;

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
  if(r.status===401){$('#login').hidden=false;$('#login input[name="token"]').focus();throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  return r.status===204?null:r.json();
 }
 async function chatHistory(path){
  let r;try{r=await fetch(path,{credentials:'same-origin',signal:AbortSignal.timeout(60000)})}catch{throw Error('Controller connection interrupted. The operation may still be running.')}
  if(r.status===401){$('#login').hidden=false;$('#login input[name="token"]').focus();throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  const rawBusy=r.headers.get('X-Vmbox-Agent-Busy');
  return {messages:await r.json(),busy:rawBusy===null?null:rawBusy==='true',busySince:r.headers.get('X-Vmbox-Agent-Busy-Since')||''};
 }
 const boxPath=id=>'/v1/logical-boxes/'+encodeURIComponent(id);

 /* The chat palette, type and spacing stay consistent across sessions. The
    optional seed changes the mascot only. */
 const DEFAULT_SEED='0xe57c2091';
 const theme=(()=>{let saved={};try{saved=JSON.parse(localStorage.getItem('vmboxChatTheme')||'{}')}catch{}
  const qp=new URLSearchParams(location.search);
  return {seed:String(saved.seed||qp.get('seed')||DEFAULT_SEED)};})();
 const saveTheme=()=>{try{localStorage.setItem('vmboxChatTheme',JSON.stringify(theme))}catch{}};
 function xmur3(str){let h=1779033703^str.length;for(let i=0;i<str.length;i++){h=Math.imul(h^str.charCodeAt(i),3432918353);h=(h<<13)|(h>>>19);}return()=>{h=Math.imul(h^(h>>>16),2246822507);h=Math.imul(h^(h>>>13),3266489909);return(h^=h>>>16)>>>0;};}
 function mulberry32(a){return()=>{a|=0;a=a+0x6D2B79F5|0;let t=Math.imul(a^a>>>15,1|a);t=t+Math.imul(t^t>>>7,61|t)^t;return((t^t>>>14)>>>0)/4294967296;};}
 const hsl=(h,s,l)=>'hsl('+(((h%360)+360)%360)+' '+s+'% '+l+'%)';
 const THEME_ADJ=['mossy','sunny','plucky','sleepy','brisk','cosy','fizzy','tiny','bold','minty','wobbly','glossy'];
 const THEME_NOUN=['pebble','mochi','bramble','biscuit','comet','dumpling','clover','pixel','marble','sprout','pudding','ember'];
 const titleCase=w=>w.charAt(0).toUpperCase()+w.slice(1);
 const WORKSPACE_THEME={bg:'#111923',bg2:'#18212d',surface:'#1a2230',surface2:'#202e3d',
  ink:'#eaf0f8','ink-soft':'#91a0b3',line:'#2b3848',
  accent:'#8fb8f5','accent-text':'#aaceff','accent-ink':'#102136','accent-soft':'#263749',
  pop:'#b691e6','bubble-out':'#294b65','bubble-in':'#1b2938','bubble-in-ink':'#eaf0f8',
  danger:'#ec727c','danger-text':'#ffadb5',warn:'#e3b66d','warn-text':'#f2cf8e',ok:'#69d3a4','ok-text':'#8ee5ba'};
 function deriveTheme(seed){
  const t=Object.assign({},WORKSPACE_THEME,{radius:'6px','radius-sm':'4px','radius-lg':'9px',shadow:'0 5px 18px #08121d44','shadow-pop':'0 22px 70px #06101bbb',wall:'none'});
  return {name:String(seed),font:'system-ui, -apple-system, "Segoe UI", sans-serif',radius:6,tokens:t};
 }
 function applyVariant(){
  document.documentElement.dataset.variant='A';
  const derived=deriveTheme(theme.seed);
  const root=document.documentElement.style;
  for(const key in derived.tokens)root.setProperty('--'+key,derived.tokens[key]);
  root.setProperty('--font',derived.font);
  const themeColor=document.querySelector('meta[name="theme-color"]');if(themeColor)themeColor.setAttribute('content',derived.tokens.bg);
  const seedInput=document.getElementById('variant-seed');
  if(seedInput&&document.activeElement!==seedInput)seedInput.value=theme.seed;
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
 function makeTraits(seedStr){
  const rnd=mulberry32(xmur3(String(seedStr))());
  const R=(lo,hi)=>lo+rnd()*(hi-lo),Ri=(lo,hi)=>Math.floor(R(lo,hi+1)),pick=arr=>arr[Ri(0,arr.length-1)];
  const W=Math.round(R(138,158)),H=Math.round(R(134,154));
  const t={name:titleCase(pick(THEME_ADJ))+' '+pick(THEME_NOUN),
   hue:Math.round(R(118,292)),spin:rnd()<.5?-1:1,sat:Math.round(R(48,72)),light:Math.round(R(42,56)),
   hAngry:Math.round(R(2,16)),hHappy:Math.round(R(126,152)),hLaugh:Math.round(R(38,50)),W,H,
   shape:pick(MX_SHAPES),wobble:R(.05,.15),phase:R(0,6.28),
   lumps:Array.from({length:Ri(7,10)},()=>rnd()),
   pattern:pick(MX_PATTERNS),patTone:rnd()<.45?'#fff':'var(--mx-dark)',
   patGap:Math.round(R(14,24)),patW:Math.round(R(4,8)),patAngle:pick([0,20,45,70,-25]),
   waveAmp:R(3,7),waveLen:Math.round(R(18,32)),halfOff:R(-1,1),
   spots:Array.from({length:Ri(10,18)},()=>[R(-.8,.8),R(-.8,.8),R(.7,1.8)]),
   splotches:Array.from({length:Ri(3,6)},()=>({x:R(-.55,.55),y:R(-.55,.55),r:R(9,21),k:Array.from({length:6},()=>rnd())})),
   eyeW:Math.round(R(14,22)),eyeH:Math.round(R(26,46)),eyeGap:Math.round(R(34,50)),eyeY:Math.round(R(100,112)),
   round:R(.55,1),glint:rnd()<.75,antenna:pick(['ball','ball','star','heart','bolt','none']),antLen:Math.round(R(14,30)),
   freckles:rnd()<.4,badge:[]};
  for(let y=0;y<5;y++){const row=[];for(let x=0;x<3;x++)row[x]=rnd()<.5?1:0;t.badge.push([row[0],row[1],row[2],row[1],row[0]]);}
  return t;
 }
 function moodPalette(t){return{
  idle:[t.hue,t.sat,t.light],working:[t.hue+t.spin*20,t.sat+6,t.light+3],
  waiting:[t.hue+t.spin*44,t.sat-4,t.light+8],angry:[t.hAngry,78,62],
  happy:[t.hHappy,64,52],laughing:[t.hLaugh,92,62]};}
 let mxUid=0;
 function mascotSVGString(t){
  const uid='mx'+(++mxUid);
  const x=120-t.W/2,y=190-t.H,cxL=120-t.eyeGap/2,cxR=120+t.eyeGap/2,cy=t.eyeY;
  const er=(Math.min(t.eyeW,t.eyeH)/2*t.round).toFixed(1);
  const topY=y+8,tipY=topY-t.antLen,d=mxBodyPath(t);
  const antenna=t.antenna==='none'?'':'<g class="mx-antenna"><path d="M120 '+topY+' L120 '+tipY+'" stroke="var(--mx-dark)" stroke-width="5" stroke-linecap="round" fill="none"/><g class="mx-tip">'+({
   ball:'<circle class="mx-limb" cx="120" cy="'+(tipY-5)+'" r="8"/><circle cx="117" cy="'+(tipY-8)+'" r="2.6" fill="#fff" opacity=".6"/>',
   star:'<path class="mx-limb" transform="translate(120 '+(tipY-6)+') scale(1.1)" d="M0,-9 C1,-3 3,-1 9,0 C3,1 1,3 0,9 C-1,3 -3,1 -9,0 C-3,-1 -1,-3 0,-9 Z"/>',
   heart:'<path class="mx-limb" transform="translate(120 '+(tipY-4)+')" d="M0 6 C-9 0 -9 -8 -4.5 -8 C-1.8 -8 0 -5.6 0 -5.6 C0 -5.6 1.8 -8 4.5 -8 C9 -8 9 0 0 6 Z"/>',
   bolt:'<path class="mx-limb" transform="translate(120 '+(tipY-5)+')" d="M1 -9 L-5 1 L-0.5 1 L-2 9 L5 -1 L0.5 -1 Z"/>'}[t.antenna]||'')+'</g></g>';
  const cell=10,bx=120-cell*2.5,by=cy+8;
  const badge=t.badge.flatMap((row,ry)=>row.map((on,rx)=>!on?'':'<circle cx="'+(bx+rx*cell+cell/2)+'" cy="'+(by+ry*cell+cell/2)+'" r="'+(cell*.42)+'" fill="var(--mx-dark)" opacity=".55"/>')).join('');
  const chX=t.W/2-14,chY=cy+32;
  const freckles=!t.freckles?'':[-1,1].flatMap(s=>[[0,0],[7,4],[-6,5]].map(([dx,dy])=>'<circle cx="'+(120+s*(chX+dx))+'" cy="'+(chY+dy-2)+'" r="1.7"/>')).join('');
  const pat=mxPattern(t);
  const mouthY=((cy-106)*.7+(t.H-146)*.4).toFixed(1);
  const eye=(cx,side)=>'<g class="mx-eye mx-eye-'+side+'" style="transform-origin:'+cx+'px '+cy+'px"><g class="mx-blinker" style="transform-origin:'+cx+'px '+cy+'px"><rect x="'+(cx-t.eyeW/2)+'" y="'+(cy-t.eyeH/2)+'" width="'+t.eyeW+'" height="'+t.eyeH+'" rx="'+er+'" fill="#fff"/><circle cx="'+(cx-t.eyeW*.22)+'" cy="'+(cy-t.eyeH*.26)+'" r="'+(t.glint?Math.min(4,t.eyeW*.22):0)+'" fill="#dceef4"/></g></g>';
  return '<svg class="mascot-svg" data-mood="idle" viewBox="0 0 240 240" role="img" aria-label="generated mascot">'+
   '<defs><clipPath id="'+uid+'-body"><path d="'+d+'"/></clipPath>'+
   '<clipPath id="'+uid+'-mouth"><path d="M101 141 A20 20 0 0 0 141 141 Z"/></clipPath>'+
   '<clipPath id="'+uid+'-laugh"><path d="M93 135 A27 25 0 0 0 147 135 Z"/></clipPath></defs>'+
   '<g class="mx-look"><g class="mx-float"><g class="mx-turn"><g class="mx-pop"><g class="mx-body-g">'+
   antenna+
   '<path class="mx-body" d="'+d+'"/>'+
   '<g clip-path="url(#'+uid+'-body)"><g class="mx-back-view"><path d="'+d+'" fill="var(--mx-dark)" opacity=".18"/><g class="mx-back-pattern">'+pat+'</g>'+
   '<rect x="'+(bx-9)+'" y="'+(by-9)+'" width="'+(cell*5+18)+'" height="'+(cell*5+18)+'" rx="'+((cell*5+18)/2)+'" fill="var(--mx-dark)" opacity=".34"/><g class="mx-badge">'+badge+'</g></g></g>'+
   '<g clip-path="url(#'+uid+'-body)"><g class="mx-face"><g class="mx-pattern">'+pat+'</g><g class="mx-freckles" fill="var(--mx-dark)" opacity=".4">'+freckles+'</g>'+
   '<g class="mx-feat on-angry mx-vein-g" transform="translate('+(x+t.W-26)+' '+(y+28)+')"><g class="mx-vein" stroke="#8c1d18" stroke-width="3.4" stroke-linecap="round" fill="none" opacity=".85"><path d="M-8 -1 L0 -7 L8 -1"/><path d="M-8 6 L0 0 L8 6"/></g></g>'+
   '<g class="mx-feat on-angry mx-ink-s" stroke-width="7"><path d="M'+(cxL-16)+' '+(cy-25)+' L'+(cxL+10)+' '+(cy-15)+'"/><path d="M'+(cxR+16)+' '+(cy-25)+' L'+(cxR-10)+' '+(cy-15)+'"/></g>'+
   '<g class="mx-eyes mx-feat on-idle on-working on-waiting on-angry on-happy">'+eye(cxL,'l')+eye(cxR,'r')+'</g>'+
   '<g class="mx-feat on-laughing mx-ink-s" stroke-width="7"><path d="M'+(cxL-12)+' '+(cy+6)+' Q'+cxL+' '+(cy-12)+' '+(cxL+12)+' '+(cy+6)+'"/><path d="M'+(cxR-12)+' '+(cy+6)+' Q'+cxR+' '+(cy-12)+' '+(cxR+12)+' '+(cy+6)+'"/></g>'+
   '<g class="mx-mouths" transform="translate(0 '+mouthY+')">'+
   '<path class="mx-feat on-idle mx-ink-s" stroke-width="6" d="M107 147 Q120 160 133 147"/>'+
   '<ellipse class="mx-feat on-working mx-ink" cx="120" cy="152" rx="8" ry="10"/>'+
   '<g class="mx-feat on-waiting mx-ink"><circle class="mx-tick1" cx="106" cy="150" r="4.6"/><circle class="mx-tick2" cx="120" cy="150" r="4.6"/><circle class="mx-tick3" cx="134" cy="150" r="4.6"/></g>'+
   '<path class="mx-feat on-angry mx-ink-s" stroke-width="6.5" d="M105 156 Q120 142 135 156"/>'+
   '<g class="mx-feat on-happy"><path class="mx-ink" d="M101 141 A20 20 0 0 0 141 141 Z"/><circle cx="121" cy="163" r="11" fill="#ff8fa0" clip-path="url(#'+uid+'-mouth)"/></g>'+
   '<g class="mx-feat on-laughing"><path class="mx-ink" d="M93 135 A27 25 0 0 0 147 135 Z"/><circle cx="120" cy="163" r="15" fill="#ff8fa0" clip-path="url(#'+uid+'-laugh)"/></g>'+
   '</g></g></g></g></g></g></g></svg>';
 }
 function mascotMiniSVG(seedStr){
  const t=makeTraits(seedStr),[h,s,l]=moodPalette(t).idle;
  const x=120-t.W/2,y=190-t.H,cxL=120-t.eyeGap/2,cxR=120+t.eyeGap/2;
  const er=(Math.min(t.eyeW,t.eyeH)/2*t.round).toFixed(1),topY=y+8,tipY=topY-t.antLen,d=mxBodyPath(t),cid='mxm'+(++mxUid);
  const eye=cx=>'<rect x="'+(cx-t.eyeW/2)+'" y="'+(t.eyeY-t.eyeH/2)+'" width="'+t.eyeW+'" height="'+t.eyeH+'" rx="'+er+'" fill="#fff"/>';
  return '<svg class="mx-mini" viewBox="30 10 180 190" aria-hidden="true">'+
   '<defs><clipPath id="'+cid+'"><path d="'+d+'"/></clipPath></defs>'+
   (t.antenna==='none'?'':'<path d="M120 '+topY+' L120 '+tipY+'" stroke="'+hsl(h,s+8,l-15)+'" stroke-width="5" stroke-linecap="round"/><circle cx="120" cy="'+(tipY-5)+'" r="7" fill="'+hsl(h,s+8,l-15)+'"/>')+
   '<path d="'+d+'" fill="'+hsl(h,s,l)+'"/>'+
   '<g clip-path="url(#'+cid+')" style="--skin-dk:'+hsl(h,s+8,l-15)+';--mx-dark:'+hsl(h,s+8,l-15)+'">'+mxPattern(t)+'</g>'+
   eye(cxL)+eye(cxR)+'<path d="M107 '+(t.eyeY+41)+' Q120 '+(t.eyeY+54)+' 133 '+(t.eyeY+41)+'" stroke="#fff" stroke-width="6" fill="none" stroke-linecap="round"/></svg>';
 }
 const MACHINE={
  idle:{on:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry'}},
  working:{on:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle'}},
  waiting:{on:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle'}},
  happy:{on:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle'},after:{ms:5000,to:'idle',via:'calm down'}},
  laughing:{on:{SETTLE:'happy',WORK:'working',ERROR:'angry'},after:{ms:3800,to:'happy',via:'catch breath'}},
  angry:{on:{CALM:'idle',PRAISE:'happy',WORK:'working'},after:{ms:5600,to:'idle',via:'cool off'}}};
 const MX_TURN={total:1050,swap:570};
 class Mascot{
  constructor(host,seed,{onMood}={}){
   this.host=host;this.onMood=onMood||null;this.state='idle';this.phase='settled';this.pending=null;this.timers=[];this.seed=String(seed);
   this.build();
  }
  build(){
   if(!this.host)return;
   this.traits=makeTraits(this.seed);
   this.host.replaceChildren();
   const template=document.createElement('template');template.innerHTML=mascotSVGString(this.traits).trim();
   this.svg=template.content.firstChild;this.host.append(this.svg);
   this.pop=this.svg.querySelector('.mx-pop');
   this.applyMood(this.state,false);
  }
  applyMood(mood,animate){
   this.state=mood;
   if(!this.svg)return;
   this.svg.dataset.mood=mood;
   const [h,s,l]=moodPalette(this.traits)[mood]||[174,60,48];
   this.svg.style.setProperty('--mx-skin',hsl(h,s,l));
   this.svg.style.setProperty('--mx-dark',hsl(h,s+8,l-15));
   this.svg.style.setProperty('--mx-face',mood==='laughing'?hsl(h,90,22):'#fff');
   if(animate)this.bounce('mx-bounce');
   if(this.onMood)this.onMood(mood,this);
  }
  bounce(cls){if(!this.pop)return;this.pop.classList.remove('mx-bounce','mx-nope');void this.pop.offsetWidth;this.pop.classList.add(cls);}
  go(next,via){
   if(!MACHINE[next]||next===this.state)return false;
   if(this.phase==='turning'){this.pending={next,via};return false;}
   this.phase='turning';this.clearTimer();
   if(this.svg){this.svg.classList.remove('mx-turning');void this.svg.offsetWidth;this.svg.classList.add('mx-turning');}
   this.timers.push(setTimeout(()=>{this.applyMood(next,false)},MX_TURN.swap));
   this.timers.push(setTimeout(()=>{
    if(this.svg)this.svg.classList.remove('mx-turning');
    this.phase='settled';this.bounce('mx-bounce');this.startAuto();
    if(this.pending){const p=this.pending;this.pending=null;this.go(p.next,p.via);}
   },MX_TURN.total));
   return true;
  }
  send(evt){
   const next=MACHINE[this.state]&&MACHINE[this.state].on[evt];
   if(!next){this.bounce('mx-nope');return false;}
   return this.go(next,evt);
  }
  jump(mood){if(!MACHINE[mood])return false;if(this.phase==='turning'){this.pending={next:mood,via:'sync'};return false;}return this.go(mood,'sync');}
  startAuto(){this.clearTimer();const after=MACHINE[this.state]&&MACHINE[this.state].after;if(after)this.timers.push(setTimeout(()=>this.go(after.to,after.via),after.ms));}
  clearTimer(){for(const t of this.timers)clearTimeout(t);this.timers=[];}
  destroy(){this.clearTimer();if(this.svg)this.svg.remove();this.svg=null;}
 }
 const accountMascots=[];
 function refreshAccountMascots(){
  const seed=theme.seed||'vmbox';
  accountMascots.splice(0).forEach(m=>m.destroy&&m.destroy());
  const login=document.getElementById('login-mascot');
  if(login)login.innerHTML=mascotMiniSVG(seed);
  for(const id of ['chat-empty-mascot']){
   const host=document.getElementById(id);if(!host)continue;
   accountMascots.push(new Mascot(host,seed));
  }
 }
 const mxLook={x:0,y:0,tx:0,ty:0};
 const mxReduceMotion=typeof matchMedia==='function'&&matchMedia('(prefers-reduced-motion: reduce)');
 let mxDirty=false;
 addEventListener('pointermove',event=>{
  mxLook.tx=Math.max(-1,Math.min(1,(event.clientX-innerWidth/2)/(innerWidth/2||1)));
  mxLook.ty=Math.max(-1,Math.min(1,(event.clientY-innerHeight/2)/(innerHeight/2||1)));
  mxDirty=true;
 },{passive:true});
 addEventListener('blur',()=>{mxLook.tx=0;mxLook.ty=0;mxDirty=true});
 // Eyes only track while the pointer is actually moving (and never under
 // reduced-motion), instead of doing DOM work every frame forever.
 (function mxFollow(){
  if(mxReduceMotion&&mxReduceMotion.matches){requestAnimationFrame(mxFollow);return}
  if(!mxDirty){requestAnimationFrame(mxFollow);return}
  const dx=mxLook.tx-mxLook.x,dy=mxLook.ty-mxLook.y;
  mxLook.x+=dx*.1;mxLook.y+=dy*.1;
  document.querySelectorAll('.mascot-svg').forEach(svg=>{
   svg.style.setProperty('--mx-look-x',(mxLook.x*7).toFixed(2)+'px');
   svg.style.setProperty('--mx-look-y',(mxLook.y*5).toFixed(2)+'px');
   svg.style.setProperty('--mx-head-x',(mxLook.x*3.5).toFixed(2)+'px');
   svg.style.setProperty('--mx-head-y',(mxLook.y*2.5).toFixed(2)+'px');
  });
  if(Math.abs(dx)<.002&&Math.abs(dy)<.002)mxDirty=false;
  requestAnimationFrame(mxFollow);
 })();

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
  mediaPrev=$('#media-viewer-prev'),mediaNext=$('#media-viewer-next'),mediaCount=$('#media-viewer-count');
 let mediaGallery=[],mediaIndex=0,mediaReturnFocus=null,mediaRequest=0;
 function renderMediaItem(url,{kind='image',alt='',loading=false,status=''}={}){
  mediaBody.replaceChildren();
  if(!url&&!loading){const miss=document.createElement('div');miss.className='media-missing';miss.textContent=status||'Attachment unavailable';mediaBody.append(miss);return}
  if(kind==='video'||kind==='audio'){
   const el=document.createElement(kind);el.src=url;el.controls=true;el.playsInline=true;if(kind==='video')el.autoplay=true;
   el.setAttribute('aria-label',alt||('Embedded '+mediaKindLabel(kind)));mediaBody.append(el);
  }else{
   const frame=document.createElement('div');frame.className='media-viewer-frame';
   if(url){const img=document.createElement('img');img.src=url;img.alt=alt||'Attachment';frame.append(img)}
   if(loading||status){const note=document.createElement('span');note.className='media-viewer-status';note.textContent=status||'Loading full image…';frame.append(note)}
   mediaBody.append(frame);
  }
 }
 async function showMediaAt(index){
  const item=mediaGallery[index];if(!item)return;
  mediaIndex=index;const request=++mediaRequest;
  const many=mediaGallery.length>1;
  mediaPrev.hidden=!many;mediaNext.hidden=!many;
  mediaPrev.disabled=mediaIndex<=0;mediaNext.disabled=mediaIndex>=mediaGallery.length-1;
  mediaCount.hidden=!many;mediaCount.textContent=(mediaIndex+1)+' / '+mediaGallery.length;
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
  mediaBody.replaceChildren();mediaViewer.hidden=true;mediaGallery=[];mediaIndex=0;
  if(mediaReturnFocus&&document.contains(mediaReturnFocus))mediaReturnFocus.focus();
  mediaReturnFocus=null;
 }
 function conversationImageGallery(){
  const list=[],box=boxes.get(selected);
  for(const m of box&&box.messages||[])for(const img of m.images||[])list.push({messageId:m.id,imageId:img.id,kind:mediaKind(img.mediaType,''),alt:'Attachment '+img.number,label:'Attachment '+img.number});
  return list;
 }
 function mediaButton(url,{kind='image',alt='',label='',messageId='',imageId=''}={}){
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
  btn.onclick=()=>{
   if(messageId&&imageId){
    const list=conversationImageGallery();
    const index=list.findIndex(entry=>entry.messageId===messageId&&entry.imageId===imageId);
    if(index>=0)openMediaViewer(list,index);
    else openMediaViewer([{url,kind,alt,label,messageId,imageId}]);
   }else{
    openMediaViewer([{url,kind,alt,label}]);
   }
  };
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
 let swipeX=0,swipeY=0;
 mediaBody.addEventListener('touchstart',event=>{const t=event.changedTouches[0];swipeX=t.clientX;swipeY=t.clientY},{passive:true});
 mediaBody.addEventListener('touchend',event=>{
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
  if(event.key==='Tab'){
   const focusable=[...mediaViewer.querySelectorAll('button,a[href],video,audio')].filter(el=>!el.hidden);
   if(!focusable.length)return;
   const first=focusable[0],last=focusable[focusable.length-1];
   if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus()}
   else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}
  }
 },true);

 /* ---------- avatars: desktop preview thumbnails, blob-cached for 60s ---- */
 const avatarCache=new Map(),avatarPending=new Set();
 const AVATAR_OK_TTL=60000,AVATAR_RETRY_TTL=8000;
 const avatarFresh=(cached,state)=>!!cached&&cached.state===state&&Date.now()-cached.at<=(cached.ttl||AVATAR_OK_TTL);
 function avatarRefresh(box){
  if(box.state!=='running'){avatarCache.set(box.id,{url:null,state:box.state,at:Date.now(),ttl:AVATAR_RETRY_TTL});return}
  if(avatarPending.has(box.id))return;
  avatarPending.add(box.id);
  fetch(boxPath(box.id)+'/desktop/screenshot?thumbnail=true',{credentials:'same-origin',signal:AbortSignal.timeout(15000)})
   .then(r=>{if(!r.ok)throw Error(r.status);return r.blob()})
   .then(b=>{
    const old=avatarCache.get(box.id);if(old?.url)URL.revokeObjectURL(old.url);
    avatarCache.set(box.id,{url:URL.createObjectURL(b),state:'running',at:Date.now(),ttl:AVATAR_OK_TTL});
   })
   .catch(()=>avatarCache.set(box.id,{url:null,state:box.state,at:Date.now(),ttl:AVATAR_RETRY_TTL}))
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
 function avatarNode(box,small,preview){
  const wrap=document.createElement('span');wrap.className='avatar'+(small?' small':'');
  wrap.dataset.avatar=box.id;wrap.dataset.state=box.state;
  const base=document.createElement('span');
  if(box.state==='running'){base.className='avatar-no-signal';base.textContent='NO SIGNAL'}
  else{base.className='avatar-mascot';base.innerHTML=mascotMiniSVG(box.id)}
  wrap.append(base);
  const initials=document.createElement('span');initials.className='initials';initials.hidden=true;initials.textContent=(box.name||'?').trim().slice(0,2).toUpperCase();wrap.append(initials);
  let cached=avatarCache.get(box.id);
  if(!avatarFresh(cached,box.state))avatarRefresh(box);
  cached=avatarCache.get(box.id);
  if(cached?.state===box.state&&cached.url){const img=document.createElement('img');img.alt='';img.src=cached.url;wrap.prepend(img)}
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
 const pairGroup=mk('li','Box conversations');pairGroup.className='conversation-group';
 const pinnedGroup=mk('li','Pinned');pinnedGroup.className='conversation-group';
 function pinButton(key,label){
  const button=document.createElement('button');button.type='button';button.className='chat-pin';
  button.append(lucide('pin'));
  button.onclick=event=>{event.stopPropagation();togglePin(key)};
  updatePinButton(button,key,label);
  return button;
 }
 function updatePinButton(button,key,label){
  const pinned=pins.has(key);
  button.setAttribute('aria-label',(pinned?'Unpin ':'Pin ')+label);
  button.setAttribute('aria-pressed',String(pinned));
  button.title=(pinned?'Unpin ':'Pin ')+label;
  button.classList.toggle('is-pinned',pinned);
 }
 function renderRows(){
  const filter=filterEl.value.trim().toLowerCase();
  const list=[...boxes.values()].filter(b=>!filter||b.name.toLowerCase().includes(filter));
  // Keep the list stable: activity must not reshuffle rows under the pointer.
  list.sort((a,b)=>a.name.localeCompare(b.name)||a.id.localeCompare(b.id));
  for(const box of list){
   let row=rows.get(box.id);
   if(!row){
    row=document.createElement('li');row.dataset.boxId=box.id;
    bindLongPress(row,(x,y)=>{if(rowMenu.hidden)openRowMenu(box,{left:x,right:x,bottom:y+4,top:y})});
    row.oncontextmenu=event=>{event.preventDefault();if(rowMenu.hidden)openRowMenu(box,{left:event.clientX,right:event.clientX,bottom:event.clientY+4,top:event.clientY})};
    const meta=document.createElement('button');meta.type='button';meta.className='chat-meta';meta.setAttribute('aria-label','Open chat with '+box.name);
    const r1=document.createElement('div');r1.className='row1';const name=document.createElement('span');name.className='name';name.textContent=box.name;const state=document.createElement('span');state.className='row-state';const time=document.createElement('time');r1.append(name,time);
    const r2=document.createElement('div');r2.className='row2';const badge=document.createElement('span');badge.className='agent-badge';badge.textContent=box.defaultAgent||'agent';const preview=document.createElement('span');preview.className='preview';const unread=document.createElement('span');unread.className='unread';unread.hidden=true;r2.append(state,badge,preview,unread);
    meta.append(r1,r2);row.append(meta,pinButton(pinKey('box',box.id),box.name));
    row.onclick=()=>{location.hash='box='+box.id;openBox(box.id)};
    rows.set(box.id,row);
   }
   row.classList.toggle('active',box.id===selected&&!selectedPair);
   updatePinButton(row.querySelector('.chat-pin'),pinKey('box',box.id),box.name);
   // Make the box state readable at a glance, not just a tiny dot.
   const starting=['creating','attaching','reserved','starting','allocating','restoring','pending'];
   const stateClass=box.state==='running'?'running':box.state==='failed'?'failed':starting.includes(box.state)?'starting':'muted';
   if(row.dataset.state!==stateClass)row.dataset.state=stateClass;
   const stateEl=row.querySelector('.row-state');if(stateEl.textContent!==box.state)stateEl.textContent=box.state;
   const oldAvatar=row.querySelector('.avatar');
   if(oldAvatar&&oldAvatar.dataset.state===box.state){
    // Keep the node (its hover wiring and live preview), but still retry a
    // thumbnail that is stale or failed while the desktop was starting.
    if(!avatarFresh(avatarCache.get(box.id),box.state))avatarRefresh(box);
   }
   else{const avatar=avatarNode(box,false,true);if(oldAvatar)oldAvatar.replaceWith(avatar);else row.prepend(avatar)}
   const time=row.querySelector('time'),nextTime=box.last?fmtTime(box.last.createdAt):'';if(time.textContent!==nextTime)time.textContent=nextTime;
   row.querySelector('time').classList.toggle('recent',!!box.unread);
   const preview=row.querySelector('.preview'),nextPreview=box.streaming?'typing…':box.processing?'processing…':previewText(box.last);if(preview.textContent!==nextPreview)preview.textContent=nextPreview;preview.classList.toggle('streaming',!!box.streaming&&!box.processing);preview.classList.toggle('processing',!!box.processing&&!box.streaming);
   const unread=row.querySelector('.unread');unread.hidden=!box.unread;unread.textContent=box.unread>99?'99+':box.unread;
  }
  for(const [id,row] of rows){if(!boxes.has(id)){row.remove();rows.delete(id)}}
  const pairList=owner?[...pairs.values()].filter(pair=>!filter||(pair.boxAName+' '+pair.boxBName).toLowerCase().includes(filter)):[];
  pairList.sort((a,b)=>a.boxAName.localeCompare(b.boxAName)||a.boxBName.localeCompare(b.boxBName));
  for(const pair of pairList){
   const key=pairKey(pair);let row=pairRows.get(key);
   if(!row){
    row=document.createElement('li');row.dataset.pairKey=key;
    const avatar=document.createElement('span');avatar.className='pair-avatar';avatar.textContent='↔';avatar.setAttribute('aria-hidden','true');
    const meta=document.createElement('button');meta.type='button';meta.className='chat-meta';meta.setAttribute('aria-label','Open box conversation between '+pair.boxAName+' and '+pair.boxBName);
    const first=document.createElement('div');first.className='row1';first.append(mk('span',pair.boxAName+' ↔ '+pair.boxBName),document.createElement('time'));first.firstChild.className='name';
    const second=document.createElement('div');second.className='row2';const badge=mk('span','Box ↔ Box');badge.className='agent-badge';const preview=mk('span');preview.className='preview';second.append(badge,preview);
    meta.append(first,second);row.append(avatar,meta,pinButton(pinKey('pair',key),pair.boxAName+' ↔ '+pair.boxBName));row.onclick=()=>{location.hash='pair='+encodeURIComponent(key);void openPair(key)};pairRows.set(key,row);
   }
   const name=pair.boxAName+' ↔ '+pair.boxBName;
   row.querySelector('.name').textContent=name;row.querySelector('.name').title=name;
   row.querySelector('.chat-meta').setAttribute('aria-label','Open box conversation between '+pair.boxAName+' and '+pair.boxBName);
   updatePinButton(row.querySelector('.chat-pin'),pinKey('pair',key),name);
   row.classList.toggle('active',key===selectedPair);
   row.querySelector('time').textContent=pair.lastAt?fmtTime(pair.lastAt):'';
   row.querySelector('.preview').textContent=pair.lastText||'No messages yet';
  }
  for(const [key,row] of pairRows)if(!pairs.has(key)){row.remove();pairRows.delete(key)}
  const pinnedBoxes=list.filter(box=>pins.has(pinKey('box',box.id))).map(box=>rows.get(box.id));
  const pinnedPairs=pairList.filter(pair=>pins.has(pinKey('pair',pairKey(pair)))).map(pair=>pairRows.get(pairKey(pair)));
  const otherBoxes=list.filter(box=>!pins.has(pinKey('box',box.id))).map(box=>rows.get(box.id));
  const otherPairs=pairList.filter(pair=>!pins.has(pinKey('pair',pairKey(pair)))).map(pair=>pairRows.get(pairKey(pair)));
  const desired=[];
  if(pinnedBoxes.length||pinnedPairs.length)desired.push(pinnedGroup,...pinnedBoxes,...pinnedPairs);
  desired.push(...otherBoxes);
  if(otherPairs.length)desired.push(pairGroup,...otherPairs);
  $('#chat-list-empty').hidden=list.length+pairList.length>0;
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
  copy:[['rect',{width:'14',height:'14',x:'8',y:'8',rx:'2',ry:'2'}],['path',{d:'M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2'}]],
  forward:[['path',{d:'m15 17 5-5-5-5'}],['path',{d:'M4 18v-2a4 4 0 0 1 4-4h12'}]],
  reply:[['polyline',{points:'9 17 4 12 9 7'}],['path',{d:'M20 18v-2a4 4 0 0 0-4-4H4'}]],
  pin:[['path',{d:'m16 9 2-2V4H6v3l2 2v4l-2 2h12l-2-2V9Z'}],['path',{d:'M12 15v7'}]],
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
  for(const image of message.images||[]){
   const label='Attachment '+image.number,alt=label+' from '+message.direction;
   const kind=mediaKind(image.mediaType,'');
   if(kind==='video'||kind==='audio'){
    // The chip carries no media of its own and the viewer streams from the
    // authenticated endpoint, so never buffer a whole clip into a blob here:
    // a 100 MiB video would download on every transcript render and a slow
    // link would time out and drop the attachment from the conversation.
    row.append(mediaButton(mediaEndpoint(message.id,image.id),{kind,alt,label,messageId:message.id,imageId:image.id}));
    continue;
   }
   const btn=mediaButton('',{kind,alt,label,messageId:message.id,imageId:image.id});row.append(btn);
   imageURL(message.id,image.id,true).then(url=>{if(!btn.isConnected)return;const frame=btn.querySelector('.media-preview'),img=frame?.querySelector('img'),placeholder=frame?.querySelector('.media-preview-placeholder');if(url&&img)img.src=url;else if(placeholder){placeholder.textContent='Preview unavailable';frame.classList.add('failed')}});
  }
  const form=readOnly?null:questionForm(box,message);if(form)row.append(form);
  const meta=document.createElement('span');meta.className='meta';
  const threadSize=readOnly?0:(box.messages||[]).filter(value=>value.threadId&&value.threadId===message.threadId).length;if(message.threadId&&threadSize>2){const thread=document.createElement('button');thread.type='button';thread.className='msg-thread';thread.textContent=threadSize+' in thread';thread.onclick=()=>void openThread(message.threadId);meta.append(thread)}
  meta.append(Object.assign(document.createElement('time'),{textContent:new Date(message.createdAt).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit'})}));
  if(mine&&!readOnly&&message.state!=='silent'){
   const ticks=document.createElement('span');ticks.className='ticks'+(message.state==='failed'||message.state==='ambiguous'?' failed':'');
   ticks.title=stateTicks[message.state]||'';
   const icon=stateIconName[message.state];
   if(icon)ticks.append(lucide(icon));
   if(message.state==='failed'||message.state==='ambiguous')ticks.append(document.createTextNode(message.state==='failed'?'failed':'unconfirmed'));
   meta.append(ticks);
  }
  // Always-visible actions (hover-only controls are invisible on touch): the
  // chevron sits beside the delivery state and time, then reveals the menu.
  if(readOnly){row.append(meta);return row}
  const actions=document.createElement('div');actions.className='msg-actions';
  const toggle=document.createElement('button');toggle.type='button';toggle.className='msg-more';toggle.setAttribute('aria-label','Message actions');toggle.setAttribute('aria-expanded','false');toggle.append(lucide('chevron-down'));
  const menu=document.createElement('div');menu.className='msg-actions-menu';menu.hidden=true;
  const copy=document.createElement('button');copy.type='button';copy.append(lucide('copy'),Object.assign(document.createElement('span'),{textContent:'Copy'}));
  copy.onclick=async()=>{closeAllMsgActions();try{await navigator.clipboard.writeText(message.question?message.question.text:message.text);toast('Message copied.')}catch{toast('Copy is unavailable here.')}};
  const forward=document.createElement('button');forward.type='button';forward.append(lucide('forward'),Object.assign(document.createElement('span'),{textContent:'Forward…'}));
  forward.onclick=()=>{closeAllMsgActions();openForwardMenu(toggle,message)};
  const reply=document.createElement('button');reply.type='button';reply.append(lucide('reply'),Object.assign(document.createElement('span'),{textContent:'Reply in thread'}));reply.onclick=async()=>{closeAllMsgActions();await openThread(message.threadId||message.id);$('#thread-composer textarea').focus()};
  const viewThread=document.createElement('button');viewThread.type='button';viewThread.textContent='View thread';viewThread.onclick=()=>{closeAllMsgActions();void openThread(message.threadId||message.id)};
  menu.append(reply,copy,forward,viewThread);
  if(mine&&message.state==='ambiguous'){
   const inspect=document.createElement('button');inspect.type='button';inspect.append(lucide('help'),Object.assign(document.createElement('span'),{textContent:'Check delivery in TMUX'}));
   inspect.onclick=()=>{closeAllMsgActions();void openTakeover('tmux',box.id)};
   menu.append(inspect);
  }
  toggle.onclick=event=>{event.stopPropagation();const willOpen=menu.hidden;closeAllMsgActions();if(willOpen)openMsgActions(menu,toggle)};
  row.oncontextmenu=event=>{if(event.target.closest('a,button,input,textarea,video,audio'))return;event.preventDefault();closeAllMsgActions();openMsgActions(menu,toggle)};
  actions.append(toggle,menu);meta.append(actions);row.append(meta);
  return row;
 }
 function messageAuthor(message){return message.direction==='user'?'You':message.direction==='box'?(boxes.get(message.senderBoxId)?.name||'Agent box'):'Agent'}
 function setReply(message){replyingTo=message;replyPreview.hidden=false;$('#reply-preview-text').textContent=messageAuthor(message)+': '+(message.question?.text||message.text);inputEl.focus()}
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
 function closeAllMsgActions(){for(const menu of document.querySelectorAll('.msg-actions-menu'))menu.hidden=true;for(const toggle of document.querySelectorAll('.msg-more'))toggle.setAttribute('aria-expanded','false')}
 function openMsgActions(menu,toggle){menu.hidden=false;toggle.setAttribute('aria-expanded','true');placeMsgActions(menu,toggle)}
 function placeMsgActions(menu,toggle){
  const scroller=toggle.closest('#chat-messages,#thread-messages');
  if(!scroller)return;
  const clip=scroller.getBoundingClientRect(),viewport=window.visualViewport;
  const top=Math.max(8,clip.top,viewport?.offsetTop||0),bottom=Math.min(innerHeight-8,clip.bottom,(viewport?.offsetTop||0)+(viewport?.height||innerHeight));
  const left=Math.max(8,clip.left,viewport?.offsetLeft||0),right=Math.min(innerWidth-8,clip.right,(viewport?.offsetLeft||0)+(viewport?.width||innerWidth));
  const anchor=toggle.getBoundingClientRect();
  if(anchor.bottom<top||anchor.top>bottom){closeAllMsgActions();return}
  menu.style.top='100%';menu.style.bottom='auto';menu.style.marginTop='.2rem';menu.style.marginBottom='0';
  menu.style.maxHeight='none';menu.style.transform='none';
  const height=menu.getBoundingClientRect().height,below=Math.max(0,bottom-anchor.bottom-4),above=Math.max(0,anchor.top-top-4);
  const up=height>below&&above>below;
  menu.style.top=up?'auto':'100%';menu.style.bottom=up?'100%':'auto';
  menu.style.marginTop=up?'0':'.2rem';menu.style.marginBottom=up?'.2rem':'0';
  menu.style.maxHeight=Math.max(0,(up?above:below)-4)+'px';
  menu.dataset.placement=up?'up':'down';
  const rect=menu.getBoundingClientRect();
  const shift=rect.left<left?left-rect.left:rect.right>right?right-rect.right:0;
  menu.style.transform='translateX('+shift+'px)';
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
 };
 // A chat opens at its newest message and keeps following output until the
 // reader scrolls away; scrolling back to the bottom resumes following.
 messagesEl.addEventListener('scroll',()=>{
  if(restoringTranscript)return;
  const key=selected&&messagesEl.dataset.box===selected?selected:selectedPair&&messagesEl.dataset.pair===selectedPair?'pair:'+selectedPair:'';
  if(!key)return;
  stickToBottom=messagesEl.scrollHeight-messagesEl.scrollTop-messagesEl.clientHeight<120;
  scrollMemory.set(key,messagesEl.scrollTop);followMemory.set(key,stickToBottom);
  if(stickToBottom)newMessagesBtn.hidden=true;
 });
 function renderMessages(box){
  if(!box||box.id!==selected)return;
  if(tvPreviewBox&&tvPreviewBox!==box.id)hideTvPreview();
  const follow=stickToBottom;
  messagesEl.replaceChildren();
  messagesEl.dataset.box=box.id;
  if(box.hasOlder){const older=document.createElement('button');older.type='button';older.className='load-older';older.textContent=box.historyLoading?'Loading older messages…':'Load older messages';older.disabled=!!box.historyLoading;older.onclick=()=>void loadOlderMessages(box.id);messagesEl.append(older)}
  let day='';
  for(const message of box.messages||[]){
   const label=dayLabel(message.createdAt);
   if(label!==day){day=label;const sep=document.createElement('div');sep.className='day-sep';sep.textContent=day;messagesEl.append(sep)}
   messagesEl.append(bubble(box,message));
  }
  const pending=pendingSends.get(box.id);
  if(pending&&!(box.messages||[]).slice(pending.messageCount).some(m=>m.direction==='user'&&m.text===pending.text)){
   const row=bubble(box,{id:'pending',direction:'user',state:'delivering',text:pending.text||'📷 Image',createdAt:pending.at,images:[]});
   row.querySelector('.fwd')?.remove();messagesEl.append(row);
  }
  if(!(box.messages||[]).length&&!pending){const hint=document.createElement('p');hint.className='day-sep';hint.textContent='No messages yet — say hello to '+box.name;messagesEl.append(hint)}
  if(box.resumeCandidate){
   const card=document.createElement('div');card.className='codex-resume-card';
   const title=document.createElement('strong');title.textContent='Restore your Codex conversation?';
   const detail=document.createElement('span');detail.textContent='Found the last active session saved before hibernation ('+new Date(box.resumeCandidate.lastActiveAt||box.resumeCandidate.startedAt).toLocaleString()+'). Restore its context in the visible terminal, or continue fresh.';
   const actions=document.createElement('div');actions.className='codex-resume-actions';
   for(const [choice,label] of [['restore','Restore session'],['fresh','Start fresh']]){
    const button=document.createElement('button');button.type='button';button.textContent=label;
    button.onclick=()=>void chooseCodexResume(box,choice,actions);actions.append(button);
   }
   card.append(title,detail,actions);messagesEl.append(card);
  }
  if(box.processing&&!box.streaming){
   const t=document.createElement('div');t.className='msg agent processing';
   const mini=document.createElement('span');mini.className='processing-mascot';mini.innerHTML=mascotMiniSVG(box.id);
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
  const values=await api(owner?'/v1/grid-boxes':'/v1/logical-boxes');
  const current=new Map();const alive=new Set();
  for(const b of values||[]){const old=boxes.get(b.id);alive.add(b.id);current.set(b.id,Object.assign(old||{messages:[],historyLoaded:false,hasOlder:false,historyLoading:false},b))}
  for(const id of [...boxes.keys()])if(!alive.has(id)){
   const cached=avatarCache.get(id);if(cached?.url)URL.revokeObjectURL(cached.url);
   for(const draft of attachmentDrafts.get(id)||[])URL.revokeObjectURL(draft.url);
   boxes.delete(id);avatarCache.delete(id);previewFetched.delete(id);tvReplayCache.delete(id);attachmentDrafts.delete(id);
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
   if(!force&&Date.now()-(previewFetched.get(id)||0)<30000)return;
   const history=await chatHistory(boxPath(id)+'/messages?limit=20');
   const box=boxes.get(id);
   // The box may have been opened (or fully loaded) while the preview was in
   // flight; never let a 20-message preview overwrite an open conversation.
   if(!box||id===selected||box.historyLoaded)return;
   applyBusyState(box,history);box.messages=history.messages||[];box.historyLoaded=false;box.hasOlder=false;previewFetched.set(id,Date.now());
   summarize(id);
  }));
 }
 function applyBusyState(box,history){
  if(history.busy===null){delete box.agentBusy;delete box.agentBusySince;return}
  box.agentBusy=history.busy;box.agentBusySince=history.busySince||'';
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
  const state=mk('span');state.className=box.state==='running'?'running':'';
  const agent=mk('span',(box.defaultAgent||'agent')+' · ');agent.className='chat-header-agent';
  state.append(agent,document.createTextNode(box.state+(box.streaming?' · streaming…':box.processing?' · processing…':'')));
  $('#chat-header-state').replaceChildren(state);
  inputEl.placeholder='Message '+box.name+'…';
  const key=box.id+'|'+box.state;
  if(key!==headerAvatarKey){headerAvatarKey=key;$('#chat-header-avatar').replaceChildren(avatarNode(box,false,true))}
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
  if(owner&&box.state==='running'&&box.defaultAgent==='codex')void refreshCodexResume(box,!!force);
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
  renderRows();
 }
 async function openPair(key){
  const pair=pairs.get(key);if(!pair)return;
  const epoch=++viewEpoch;restoringTranscript=true;newMessagesBtn.hidden=true;
  selected='';selectedPair=key;selectedUsageProfile=null;chatUsageRequest++;renderChatUsage();lastSignature='';cancelReply();hideComposerPicker();closeInspect();closeForwardMenu();closeTakeover();
  openThreadID='';threadPanel.hidden=true;threadMessages.replaceChildren();
  messagesEl.replaceChildren();delete messagesEl.dataset.box;messagesEl.dataset.pair=key;
  const savedScroll=scrollMemory.get('pair:'+key);
  stickToBottom=savedScroll==null||followMemory.get('pair:'+key)!==false;
  $('#chat-conversation').classList.add('pair-view');
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;appEl.classList.add('in-chat');
  $('#chat-header-name').textContent=pair.boxAName+' ↔ '+pair.boxBName;
  $('#chat-header-state').textContent='Direct messages between boxes · read only';
  $('#chat-header-avatar').replaceChildren(Object.assign(mk('span','↔'),{className:'pair-avatar'}));headerAvatarKey='';
  setBanner('');statusEl.textContent='';renderRows();doodle('Loading messages…');
  try{await refreshPairMessages(true)}catch(e){if(selectedPair===key)statusEl.textContent=e.message}finally{if(selectedPair===key)doodle('')}
  if(epoch===viewEpoch&&selectedPair===key)requestAnimationFrame(()=>{
   if(epoch!==viewEpoch||selectedPair!==key)return;
   if(stickToBottom)scrollMessagesToBottom();else if(savedScroll!=null)messagesEl.scrollTop=savedScroll;
   restoringTranscript=false;
  });
 }
 async function refreshCodexResume(box,force){
  const prior=resumeChecks.get(box.id);
  if(prior&&(!force&&Date.now()-prior.at<20000||prior.pending))return;
  const check={at:Date.now(),pending:true};resumeChecks.set(box.id,check);
  try{
   const result=await api(boxPath(box.id)+'/codex-resume');
   if(resumeChecks.get(box.id)!==check)return;
   box.resumeCandidate=result.candidate||null;
   if(selected===box.id){renderMessages(box);updateSendState()}
  }catch(e){if(selected===box.id)statusEl.textContent='Could not check saved Codex sessions: '+e.message}
  finally{check.pending=false}
 }
 async function chooseCodexResume(box,choice,actions){
  const candidate=box.resumeCandidate;if(!candidate)return;
  for(const button of actions.querySelectorAll('button'))button.disabled=true;
  try{
   await api(boxPath(box.id)+'/codex-resume','POST',{}, {choice,sessionId:candidate.sessionId,savedAt:candidate.savedAt});
   box.resumeCandidate=null;resumeChecks.delete(box.id);
   if(selected===box.id){renderMessages(box);updateSendState()}
   toast(choice==='restore'?'Saved Codex conversation restored.':'Continuing with a fresh Codex conversation.');
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
 let pickerItems=[],pickerIndex=0,pickerRange=null,pickerRequest=0;
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
   renderComposerPicker(chatCommands.filter(command=>command.name.startsWith(token.query)).map(command=>({kind:'/',name:command.name,detail:command.prompt,prompt:command.prompt})),token);
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
  const insertion=item.kind==='/'?item.prompt:'@'+item.name+' ';
  inputEl.value=inputEl.value.slice(0,range.start)+insertion+inputEl.value.slice(range.end);
  const caret=range.start+insertion.length;hideComposerPicker();inputEl.focus();inputEl.setSelectionRange(caret,caret);
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
  const send=$('#send'),box=boxes.get(selected),running=box?.state==='running'&&!box?.resumeCandidate;send.disabled=!hasContent||!running;
  const count=drafts.length,label=count?'Send ('+count+' attachment'+(count===1?'':'s')+')':'Send';
  send.setAttribute('aria-label',label);
  send.title=running?label+(enterInsertsNewline()?'':' · Enter to send; Shift+Enter for a new line'):box?.resumeCandidate?'Choose whether to restore the saved Codex session first.':'Wait for this box to be running before sending';
 }
 inputEl.addEventListener('input',()=>{grow();updateSendState();void updateComposerPicker();if(!selected)return;inputDrafts[selected]=inputEl.value;clearTimeout(inputDraftTimer);inputDraftTimer=setTimeout(saveInputDrafts,250)});
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
 async function uploadImages(files){
  const boxID=selected;
  if(!boxID)return;
  for(const file of files){
   if((attachmentDrafts.get(boxID)||[]).length>=8){statusEl.textContent='Attach at most 8 files.';break}
   const isVideo=file.type==='video/mp4'||file.type==='video/webm';
   const isImage=['image/png','image/jpeg','image/gif'].includes(file.type);
   if(!isVideo&&!isImage){statusEl.textContent='Choose PNG, JPEG, GIF, MP4 or WebM.';continue}
   const limitMiB=isVideo?100:25;
   if(file.size>limitMiB*1024*1024){statusEl.textContent=(isVideo?'Videos':'Images')+' must be at most '+limitMiB+' MiB.';continue}
   try{
    const response=await fetch('/v1/run-once-images',{method:'POST',credentials:'same-origin',body:file,signal:AbortSignal.timeout(120000)});
    let result;try{result=await response.json()}catch{}
    if(!response.ok)throw Error(result?.error||'Upload failed.');
    // Re-read after the await: another picker/paste may have completed for the
    // same chat while this upload was in flight.
    const drafts=attachmentDrafts.get(boxID)||[];
    if(drafts.length>=8){statusEl.textContent='Attach at most 8 files.';continue}
    drafts.push({id:result.id,number:drafts.length+1,url:URL.createObjectURL(file),kind:isVideo?'video':'image',mediaType:file.type});
    attachmentDrafts.set(boxID,drafts);
    if(selected===boxID)renderDrafts();
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
  const boxID=selected,box=boxes.get(boxID);
  if(box?.resumeCandidate){statusEl.textContent='Choose whether to restore the saved Codex session first.';updateSendState();return}
  if(box?.state!=='running'){statusEl.textContent='Wait for this box to be running before sending.';updateSendState();return}
  const drafts=attachmentDrafts.get(boxID)||[];
  const text=inputEl.value,images=drafts.map(({id,number})=>({id,number})),replyTarget=replyingTo,mentionedBoxIds=mentionedBoxIDs(text);
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
  // Delivery can finish after a fast MCP reply, so show the outgoing message
  // and existing processing state while the synchronous POST is in flight.
  if(showPending){pendingSends.set(boxID,{messageCount:(box.messages||[]).length,text,at:new Date().toISOString()});summarize(boxID);renderHeader();renderRows();renderMessages(box)}
  let settled=false;
  try{
   const result=await api(boxPath(boxID)+'/messages','POST',{'Idempotency-Key':pendingKey},{text,images,parentMessageId:replyTarget?.id||'',mentionedBoxIds});
   for(const d of sentDrafts)URL.revokeObjectURL(d.url);
   pendingKey='';pendingFingerprint='';if(replyingTo?.id===replyTarget?.id)cancelReply();
   statusEl.textContent=result?.message?.state==='silent'?'Note saved without waking the agent.':'';
   if(showPending&&result?.message?.state!=='silent')await new Promise(resolve=>setTimeout(resolve,Math.max(0,350-(performance.now()-pendingAt))));
   pendingSends.delete(boxID);
   if(selected===boxID){await refreshMessages(true);if(replyTarget)await openThread(replyTarget.threadId||replyTarget.id)}
   else{summarize(boxID);renderRows()}
   settled=true;
  }catch(e){
   statusEl.textContent=e.message;
   if(!inputEl.value)inputEl.value=text;if(!replyingTo&&replyTarget)setReply(replyTarget);
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
   ['Waiting for agent',waiting?'since '+fmtAgo(lastUser.createdAt):'no',waiting?'alert':'ok'],
  ]);
  const quick=$('#inspect-quick-actions');quick.replaceChildren();
  const link=document.createElement('a');
  link.href='/boxes/'+encodeURIComponent(box.id);link.textContent='Open workspace';link.target='_blank';link.rel='noopener';
  quick.append(link);
  if(canWakeBox(box)){
   const wake=document.createElement('button');wake.type='button';wake.textContent='Wake box';wake.disabled=wakingBoxes.has(box.id);
   wake.title='Restore the saved workspace and start a fresh agent session';wake.onclick=()=>void wakeBox(box);quick.append(wake);
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
  maybeLoadInspectProfiles(box);
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
 function closeInspect(){inspectOpen=false;inspect.hidden=true;$('#inspect-backdrop').hidden=true;$('#chat-info').setAttribute('aria-expanded','false');clearInterval(inspectTimer);controllerPing=null;inspectContactsFor='';inspectContactCache=null;inspectProfilesFor='';inspectProfileCache=null;inspectWorkerKey='';inspectWorker=null}
 $('#inspect-close').onclick=closeInspect;
 $('#inspect-backdrop').onclick=closeInspect;
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
  let extrasLoaded=false,poolChoices=[],autoPoolIndex='';
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
    return rows;
   }
   return [];
  }
  function renderPreview(){
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
  const profileSelect=document.createElement('select');profileSelect.name='loginProfile';profileLabel.append(profileSelect);
  const modelLabel=document.createElement('label');modelLabel.className='field profile-field';modelLabel.textContent='Model';
  const modelInput=document.createElement('input');modelInput.name='agentModel';modelInput.maxLength=200;modelLabel.append(modelInput);const modelPicker=window.VMBoxModelPicker.create(modelInput);root._modelPicker=modelPicker;
  const githubLabel=document.createElement('label');githubLabel.className='field profile-field';githubLabel.textContent='GitHub profile';
  const githubSelect=document.createElement('select');githubSelect.name='githubProfile';githubSelect.append(new Option('None',''));
  for(const profile of profiles.filter(profile=>profile.application==='github'))githubSelect.append(new Option(profile.name,JSON.stringify({application:'github',name:profile.name})));
  githubLabel.append(githubSelect);githubLabel.hidden=githubSelect.options.length===1;
   root.append(profileLabel,modelLabel,githubLabel);
   const syncModel=()=>{const option=profileSelect.selectedOptions[0],hasProfile=!!profileSelect.value;modelInput.disabled=!hasProfile;modelPicker.setValue(hasProfile?option?.dataset.model||'':'');modelPicker.setReasoningEffort('');modelPicker.setOptions(window.VMBoxModelPicker.optionsFor(agentSelect.value,[option?.dataset.model]));modelLabel.hidden=!hasProfile;const ref=hasProfile?JSON.parse(profileSelect.value):null;modelPicker.setLoader(ref&&['claude','codex','opencode'].includes(ref.application)?()=>api('/v1/login-profiles/'+encodeURIComponent(ref.application)+'/'+encodeURIComponent(ref.name)+'/models'):null);renderPreview()};
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
  }catch(e){$('#new-box-status').textContent=e.message}
 }
  function openNewBoxModal(){
   createForm.reset();createForm.elements.defaultAgent.onchange?.();$('#new-box-status').textContent='';newBoxModal.hidden=false;
   void primeBoxExtras();
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
 const rowMenu=$('#row-menu'),menuBackdrop=$('#menu-backdrop');
 const wakingBoxes=new Set();
 const canWakeBox=box=>box&&['hibernated','detached','failed'].includes(box.state);
 function closeRowMenu(){rowMenu.hidden=true;rowMenu.replaceChildren();rowMenu.classList.remove('sheet-mode');menuBackdrop.hidden=true}
 function openRowMenu(box,rect){
  rowMenu.replaceChildren();
  // Keep the menu small: everything else lives in the Details panel.
  const items=[
   ['Show details',()=>{if(!inspectOpen)$('#chat-info').click()}],
  ];
  if(canWakeBox(box))items.push(['Wake box',()=>void wakeBox(box)]);
  if(box.state==='running')items.push(['Hibernate box',()=>void hibernateBox(box)],['Restart box…',()=>void restartBox(box)]);
  items.push(['Delete box…',()=>openDeleteModal(box),'danger']);
  for(const item of items){const b=document.createElement('button');b.type='button';b.textContent=item[0];if(item[2])b.className='danger';b.onclick=()=>{closeRowMenu();item[1]()};rowMenu.append(b)}
  const sheet=coarsePointer()||innerWidth<=640;
  rowMenu.classList.toggle('sheet-mode',sheet);
  rowMenu.hidden=false;
  if(sheet){
   menuBackdrop.hidden=false;
   try{navigator.vibrate?.(10)}catch{}
   rowMenu.style.left=rowMenu.style.top='';
  }else{
   rowMenu.style.left=Math.max(8,Math.min(rect.left,innerWidth-rowMenu.offsetWidth-8))+'px';
   rowMenu.style.top=Math.max(8,Math.min((rect.bottom||rect.top)+4,innerHeight-rowMenu.offsetHeight-8))+'px';
  }
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
   await api(boxPath(box.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'chat'});
   toast(box.name+' is starting again.');
   await loadBoxes();
  }catch(e){toast(e.message)}
 }
 async function wakeBox(box){
  if(!canWakeBox(box)||wakingBoxes.has(box.id))return;
  wakingBoxes.add(box.id);renderHeader();
  try{
   toast('Waking '+box.name+'…');
   const allocation=await api(boxPath(box.id)+'/allocate','POST',{'Idempotency-Key':crypto.randomUUID()},{leaseOwner:'chat'});
   await loadBoxes();
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
 $('#login').onsubmit=async event=>{
  event.preventDefault();
  try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+event.target.elements.token.value});event.target.reset();await enter()}catch(e){$('#error').textContent=e.message}
 };
 $('#logout').onclick=async()=>{
  clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);clearInterval(usageTimer);stopManualUsageRefresh();
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
  $('#chat-app').hidden=true;$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false;$('#logout').hidden=true;$('#login').hidden=false;
 };
 async function enter(){
  try{
   const who=await api('/v1/whoami');usageGeneration++;owner=who.role==='owner';
   document.querySelectorAll('[data-owner-nav]').forEach(link=>link.hidden=!owner);
   $('#usage-toggle').hidden=!owner;
   $('#presets-toggle').hidden=!owner;
   $('#commands-toggle').hidden=!owner;
   $('#roles-toggle').hidden=!owner;
   $('#login').hidden=true;$('#logout').hidden=false;appEl.hidden=false;applySidebarWidth();applyThreadWidth();
   doodle('Loading chats…');
   try{await loadBoxes()}finally{doodle('')}
   const params=new URLSearchParams(location.hash.slice(1)),id=params.get('box'),pair=params.get('pair');
   if(pair&&owner){const match=[...pairs.keys()].find(key=>key===pair||key.split('/').reverse().join('/')===pair);if(match)await openPair(match)}
   else if(id&&boxes.has(id))await openBox(id);
   if(owner)void loadChatCommands().catch(e=>{if(selected)statusEl.textContent=e.message});
   if(owner){clearInterval(usageTimer);void refreshUsage();usageTimer=setInterval(()=>void refreshUsage(),60000)}
   schedule();renderPushState();void syncPushSubscription();
  }catch(e){$('#error').textContent=e.message;$('#login').hidden=false;$('#login input[name="token"]').focus()}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);clearInterval(usageTimer);clearInterval(usageManualTimer);releaseImageURLs();for(const drafts of attachmentDrafts.values())for(const draft of drafts)URL.revokeObjectURL(draft.url)});

 /* ---------- imported profile usage ---------- */
 function usageNumber(value){return typeof value==='number'&&Number.isFinite(value)?new Intl.NumberFormat(undefined,{maximumFractionDigits:2}).format(value):'—'}
 function usageDate(value){if(!value)return 'unknown';const date=new Date(value);return Number.isNaN(date.getTime())?'unknown':date.toLocaleString()}
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
  renderChatUsage();
  $('#usage-title').textContent=usageScope?'Profile usage · '+usageScope.application+' · '+usageScope.name:'Profile usage limits';
  const visible=usageScope?profiles.filter(profile=>profile.application===usageScope.application&&profile.name===usageScope.name):profiles;
  if(!visible.length){root.append(mk('p',usageScope?'No usage data for this chat’s profile yet.':'No saved agent profiles yet.'));return}
  for(const profile of visible){
   const card=mk('article');card.className='usage-profile';
   const heading=mk('h3',profile.application+' · '+profile.name);card.append(heading);
   const boxes=(profile.boxes||[]).length?'Running: '+profile.boxes.join(', '):'No running box';
   const source=profile.snapshot?.source?' · Source: '+profile.snapshot.source:'';
   card.append(mk('p',boxes+source+' · '+(profile.observedAt?'Observed '+usageDate(profile.observedAt):'Waiting for first check')));
   const snapshot=profile.snapshot;
   for(const window of snapshot?.windows||[]){
    const row=mk('div');row.className='usage-window';
    const names={session:'Current session',weekly_all:'Current week (all models)',weekly_scoped:'Current week',primary:'Primary',secondary:'Secondary'};
    const label=[names[window.name]||window.name,window.scope,window.group&&window.group!==window.name?(names[window.group]||window.group):null,window.durationMinutes?window.durationMinutes+' min':null].filter(Boolean).join(' · ');
    const remaining=remainingPercent(window.usedPercent),head=mk('div');head.className='usage-window-head';
    head.append(mk('span',label),mk('strong',remaining===null?'Remaining unavailable':usageNumber(remaining)+'% remaining'));row.append(head);
    if(remaining!==null){const track=mk('div');track.className='usage-track';track.setAttribute('role','progressbar');track.setAttribute('aria-label',label+' remaining');track.setAttribute('aria-valuemin','0');track.setAttribute('aria-valuemax','100');track.setAttribute('aria-valuenow',String(remaining));const fill=mk('span');fill.style.width=remaining+'%';track.append(fill);row.append(track)}
    row.append(mk('small',(typeof window.usedPercent==='number'?usageNumber(window.usedPercent)+'% used':'Usage unavailable')+(window.resetsAt?' · Resets '+usageDate(window.resetsAt):'')));
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
   if(profile.checkedAt)card.append(mk('small','Last checked '+usageDate(profile.checkedAt)));
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
 $('#command-filter').addEventListener('input',renderChatCommands);
 $('#command-new').onclick=()=>{selectChatCommand(null);$('#command-form input[name="name"]').focus()};
 $('#command-use').onclick=()=>{
  const command=chatCommands.find(item=>item.name===selectedCommandName);if(!command)return;
  if(!selected){$('#command-status').textContent='Open a box chat to use this command.';return}
  const existing=inputEl.value;
  inputEl.value=existing+(existing&&!existing.endsWith('\n')?'\n\n':'')+command.prompt;
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
 function syncMCPToolGroups(form=$('#role-editor-form')){for(const group of form.querySelectorAll('.mcp-tool-group')){const tools=[...group.querySelectorAll('input[name=mcpTools]')],toggle=group.querySelector('.mcp-tool-group-toggle'),selected=tools.filter(input=>input.checked).length;toggle.checked=selected===tools.length;toggle.indeterminate=selected>0&&selected<tools.length}}
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
  admin:['list_agent_boxes','get_agent_box','set_agent_box_tags','restart_agent_box','delete_agent_box']
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
  return {capabilities:{allContacts:{enabled:f.allContactsEnabled.checked},createAgentBox:{enabled:hasTool('create_agent_box'),maxBoxes:num('maxBoxes'),maxDiskGiB:num('maxDiskGiB'),allowedAgents:chosenAgents.length?chosenAgents:['codex','claude','opencode']},manageAgentBoxes:{list:hasTool('list_agent_boxes'),inspect:hasTool('get_agent_box'),tag:hasTool('set_agent_box_tags'),restart:hasTool('restart_agent_box'),delete:hasTool('delete_agent_box')},mcpTools:{enabled:true,allowedTools}}};
 }
 $('#roles-toggle').onclick=()=>void openPermissionsModal();
 $('#inspect-edit-roles').onclick=()=>void openBoxPolicyEditor(selected);
 $('#role-box-search').addEventListener('input',renderPermissionBoxes);
 document.querySelectorAll('#role-editor-form .mcp-tool-group-toggle').forEach(input=>input.addEventListener('change',()=>changeMCPToolGroup(input)));
 document.querySelectorAll('#role-editor-form input[name=mcpTools]').forEach(input=>input.addEventListener('change',()=>syncMCPToolGroups(input.form)));
 $('#role-editor-form').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,boxID=form.elements.id.value,status=$('#role-editor-status');if(!boxID)return;
  status.textContent='Saving permissions…';
  try{const body=directPolicyBody(form),policy=await api(boxPath(boxID)+'/agent-policy','PUT',{},body);policySummaryEpoch++;policySummaries.set(boxID,policy?.capabilities?policy:body);if(!$('#roles-modal').hidden){renderPermissionBoxes();void loadPermissionSummaries()}status.textContent='Saved. Running MCP clients refresh their tools automatically.';$('#role-editor-modal').hidden=true;toast('Permissions saved and synced.');if(selected===boxID){inspectContactsFor='';renderInspect()}}
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
  const auto=mk('option',instructionPresets.defaultName?'Default · '+instructionPresets.defaultName:'Default / none');auto.value='auto';select.append(auto);
  const none=mk('option','None');none.value='none';select.append(none);
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
  if(value==='custom'){
   textarea.readOnly=false;
   if(createInstructionSource!=='custom'){textarea.value='';createInstructionSource='custom';editor.open=true}
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
  closeSheets();
  boxInstructionTarget=box;
  const status=$('#box-instructions-status');status.textContent='Loading…';
  $('#box-instructions-title').textContent='Instructions · '+box.name;
  const select=$('#box-instructions-preset');select.replaceChildren();
  try{
   const state=await api(boxPath(box.id)+'/instructions'),current=state.instructions||{source:'none',markdown:''};
   const none=mk('option','No custom instructions (chat conventions only)');none.value='';select.append(none);
   for(const preset of instructionPresets.presets){const option=mk('option',preset.name+' · r'+preset.revision);option.value=preset.name;select.append(option)}
   const custom=mk('option','Custom Markdown for this box');custom.value='custom';select.append(custom);
   select.value=current.source==='preset'&&instructionPresets.presets.some(p=>p.name===current.preset)?current.preset:(current.source==='custom'?'custom':'');
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
   const result=await api(boxPath(boxCredentialTarget.id)+'/login-profiles','PUT',{profiles});
   status.textContent=result.note||'Saved.';
   $('#box-credentials-current').textContent=(result.profiles||[]).length?'Imported: '+(result.profiles||[]).map(ref=>ref.application+' · '+ref.name).join(', ')+'.':'No imported login profiles recorded.';
   if(selected===boxCredentialTarget.id){const agent=boxes.get(selected)?.defaultAgent;selectedUsageProfile=(result.profiles||[]).find(ref=>ref.application===agent&&['claude','codex','opencode'].includes(ref.application))||null;chatUsageRequest++;renderChatUsage()}
   if(inspectOpen&&selected===boxCredentialTarget.id){inspectProfilesFor='';maybeLoadInspectProfiles(boxCredentialTarget)}
   toast('Login profiles updated for '+boxCredentialTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };

 /* ---------- generated-look controls ---------- */
 const randomSeed=()=>'0x'+Array.from({length:8},()=>'0123456789abcdef'[Math.floor(Math.random()*16)]).join('');
 $('#variant-dice').onclick=()=>{theme.seed=randomSeed();saveTheme();applyVariant();toast('Rolled a new mascot seed.')};
 $('#variant-seed').addEventListener('change',event=>{theme.seed=event.target.value.trim()||DEFAULT_SEED;saveTheme();applyVariant()});
 $('#chat-menu').onclick=()=>{closeSheets();$('#chat-menu-sheet').hidden=false};
 $('#logout').addEventListener('click',()=>{$('#chat-menu-sheet').hidden=true},{capture:true});
 applyVariant();

 void enter();
})();
