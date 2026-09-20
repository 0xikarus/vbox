'use strict';
(()=>{
 const $=s=>document.querySelector(s);
 const listEl=$('#chat-entries'),messagesEl=$('#chat-messages'),appEl=$('#chat-app'),statusEl=$('#chat-status'),inputEl=$('#chat-input'),composer=$('#chat-composer'),attachBtn=$('#attach'),fileInput=$('#attachments'),draftsEl=$('#chat-image-drafts'),forwardMenu=$('#forward-menu'),filterEl=$('#chat-filter'),pushBtn=$('#push-toggle');
 const boxes=new Map(),rows=new Map(),imageURLs=new Map(),answeredQuestions=new Set(),pendingSends=new Map();
 let selected='',owner=false,boxTimer,msgTimer,filterTimer,pushTimer,lastSignature='',stickToBottom=true,viewEpoch=0;
 const scrollMemory=new Map();
 const previewFetched=new Map();let boxesPending=null;
 let drafts=[],pendingKey='',pendingFingerprint='';
 let instructionPresets={defaultName:'',presets:[]};
 const presetBodyCache=new Map();
 let boxInstructionTarget=null,boxCredentialTarget=null,createInstructionSource='';
 const seen=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}')}catch{return{}}})();
 const saveSeen=()=>localStorage.setItem('vmboxChatSeen',JSON.stringify(seen));
 // Unsent composer text is kept per box so switching chats (or reloading the
 // page) never loses what you were typing.
 const inputDrafts=(()=>{try{return JSON.parse(localStorage.getItem('vmboxChatInputDrafts')||'{}')}catch{return{}}})();
 const saveInputDrafts=()=>{try{localStorage.setItem('vmboxChatInputDrafts',JSON.stringify(inputDrafts))}catch{}};
 let inputDraftTimer=0;

 async function api(path,method='GET',headers={},body,timeout=60000){
  let r;try{r=await fetch(path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(timeout)})}catch{throw Error('Controller connection interrupted. The operation may still be running.')}
  if(r.status===401){$('#login').hidden=false;$('#login input[name="token"]').focus();throw Error('Please log in to the controller.')}
  if(!r.ok){let e;try{e=await r.json()}catch{}throw Error(e?.error||'Request failed: '+r.status)}
  return r.status===204?null:r.json();
 }
 const boxPath=id=>'/v1/logical-boxes/'+encodeURIComponent(id);

 /* ═══════════════════════════════════════════════════════════════════════
    Generated look: one hex seed grows the whole visual identity.
    The shipped look is daylight. Hue, saturation, radii, type and the
    mascot on the sign-in screen are all derived from a hex seed with the
    same blockies-style PRNG the mascot uses, so two seeds never look alike.
    The seed can be rerolled from the look sheet in the menu.
    ═══════════════════════════════════════════════════════════════════════ */
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
 // The shipped scheme is a dark, Discord-like theme. The seed still shapes the
 // mascot, the corner radii and the type, but the palette is fixed.
 const DISCORD={bg:'#313338',bg2:'#2b2d31',surface:'#2b2d31',surface2:'#383a40',
  ink:'#dbdee1','ink-soft':'#a3a6aa',line:'#45474d',
  accent:'#5865f2','accent-text':'#aab4ff','accent-ink':'#ffffff','accent-soft':'#3b418c',
  pop:'#eb459e','bubble-out':'#5865f2','bubble-in':'#2b2d31','bubble-in-ink':'#dbdee1',
  danger:'#f23f42','danger-text':'#ff9a9b',warn:'#f0b232','warn-text':'#f6d16b',ok:'#23a55a','ok-text':'#5bd67f'};
 function deriveTheme(seed){
  const rnd=mulberry32(xmur3(String(seed))());
  const R=(a,b)=>a+rnd()*(b-a), Ri=(a,b)=>Math.floor(R(a,b+1)), pick=arr=>arr[Ri(0,arr.length-1)];
  const font=pick(['"Baloo 2", ui-rounded, system-ui, sans-serif','ui-rounded, "SF Pro Rounded", system-ui, sans-serif','system-ui, -apple-system, "Segoe UI", sans-serif']);
  const radius=Math.round(R(5,10)), radiusSm=Math.max(3,Math.round(radius*.5)), radiusLg=Math.round(radius*1.5)+2;
  const name=titleCase(pick(THEME_ADJ))+' '+pick(THEME_NOUN);
  const t=Object.assign({},DISCORD,{radius:radius+'px','radius-sm':radiusSm+'px','radius-lg':radiusLg+'px',shadow:'none','shadow-pop':'none',wall:'none'});
  return {name,font,radius,tokens:t};
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
 let mediaGallery=[],mediaIndex=0,mediaReturnFocus=null;
 function renderMediaItem(url,{kind='image',alt=''}={}){
  mediaBody.replaceChildren();
  if(!url){const miss=document.createElement('div');miss.className='media-missing';miss.textContent='Attachment unavailable';mediaBody.append(miss);return}
  if(kind==='video'||kind==='audio'){
   const el=document.createElement(kind);el.src=url;el.controls=true;el.playsInline=true;if(kind==='video')el.autoplay=true;
   el.setAttribute('aria-label',alt||('Embedded '+mediaKindLabel(kind)));mediaBody.append(el);
  }else{
   const img=document.createElement('img');img.src=url;img.alt=alt||'Attachment';mediaBody.append(img);
  }
 }
 async function showMediaAt(index){
  const item=mediaGallery[index];if(!item)return;
  mediaIndex=index;
  let url=item.url;
  if(!url&&item.messageId){
   if(item.kind==='video'||item.kind==='audio'){
    // Point straight at the authenticated endpoint so the browser can range
    // request and stream instead of buffering a blob.
    url=mediaEndpoint(item.messageId,item.imageId);
   }else{
    const box=boxes.get(selected),m=(box&&box.messages||[]).find(x=>x.id===item.messageId),img=m&&(m.images||[]).find(x=>x.id===item.imageId);
    if(m&&img)url=await imageURL(m,img);
   }
  }
  renderMediaItem(url,item);
  if(/^https?:/i.test(url||'')){mediaOpen.hidden=false;mediaOpen.href=url}else{mediaOpen.hidden=true;mediaOpen.removeAttribute('href')}
  const many=mediaGallery.length>1;
  mediaPrev.hidden=!many;mediaNext.hidden=!many;
  mediaPrev.disabled=mediaIndex<=0;mediaNext.disabled=mediaIndex>=mediaGallery.length-1;
  mediaCount.hidden=!many;mediaCount.textContent=(mediaIndex+1)+' / '+mediaGallery.length;
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
   const img=document.createElement('img');img.className='chat-image';img.src=url;img.alt=alt||'';img.loading='lazy';btn.append(img);
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
    openMediaViewer(list.length?list:[{url,kind,alt,label}],index<0?0:index);
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
   wrap.onfocus=()=>showTvPreview(wrap,currentBox());wrap.onblur=scheduleHideTvPreview;
   wrap.onclick=event=>{event.stopPropagation();void openBoxControl(currentBox(),'desktop')};
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
 let tvPreviewBox='',tvPreviewConnected=false,tvPreviewDispose=null,tvPreviewHideTimer=0,tvReplayIndex=null,tvReplayURL='',tvReplayRequest=0,tvReplayTimer=0;
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
  tvPreviewEl.hidden=false;
  tvPreviewEl.style.width=Math.min(860,window.innerWidth-24)+'px';
  tvShotRender(box);tvShotRefresh(box);renderTvTimeline();void loadTvReplay(box);startTvLive(box);
  const rect=button.getBoundingClientRect(),height=tvPreviewEl.offsetHeight;
  const width=tvPreviewEl.offsetWidth;
  const left=Math.min(Math.max(12,rect.left-8),Math.max(12,window.innerWidth-width-12));
  let top=rect.top-height-10;
  if(top<12)top=Math.min(rect.bottom+10,Math.max(12,window.innerHeight-height-12));
  tvPreviewEl.style.left=left+'px';tvPreviewEl.style.top=top+'px';
 }
 function scheduleHideTvPreview(){clearTimeout(tvPreviewHideTimer);tvPreviewHideTimer=setTimeout(hideTvPreview,250)}
 function hideTvPreview(){clearTimeout(tvPreviewHideTimer);stopTvReplay();tvReplayRequest++;if(tvReplayURL)URL.revokeObjectURL(tvReplayURL);tvReplayURL='';tvReplayIndex=null;tvPreviewEl.hidden=true;tvPreviewNoSignal.hidden=true;tvPreviewDispose?.();tvPreviewDispose=null;tvPreviewLive.replaceChildren();tvPreviewLive.classList.remove('connected');tvPreviewConnected=false;tvPreviewBox=''}
 addEventListener('scroll',()=>{if(!tvPreviewEl.matches(':hover')&&!tvPreviewEl.contains(document.activeElement))hideTvPreview()},true);
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
  const pending=pendingSends.get(id);
  const replyDuringSend=pending&&ms.slice(pending.messageCount).some(m=>m.direction==='agent');
  box.processing=agent!=='shell'&&!box.streaming&&((pending&&!replyDuringSend)||(last&&last.direction==='user'&&last.state==='delivered'&&Date.now()-new Date(last.updatedAt||last.createdAt).getTime()<10*60*1000));
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
    row.oncontextmenu=event=>{event.preventDefault();openRowMenu(box,{left:event.clientX,right:event.clientX,bottom:event.clientY+4,top:event.clientY})};
    const meta=document.createElement('div');meta.className='chat-meta';
    const r1=document.createElement('div');r1.className='row1';const name=document.createElement('span');name.className='name';name.textContent=box.name;const state=document.createElement('span');state.className='row-state';const time=document.createElement('time');r1.append(name,time);
    const r2=document.createElement('div');r2.className='row2';const badge=document.createElement('span');badge.className='agent-badge';badge.textContent=box.defaultAgent||'agent';const preview=document.createElement('span');preview.className='preview';const unread=document.createElement('span');unread.className='unread';unread.hidden=true;r2.append(state,badge,preview,unread);
    meta.append(r1,r2);row.append(meta);
    row.onclick=()=>{location.hash='box='+box.id;openBox(box.id)};
    rows.set(box.id,row);
   }
   row.classList.toggle('active',box.id===selected);
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
  const desired=list.map(b=>rows.get(b.id));if(desired.length!==listEl.children.length||desired.some((row,index)=>listEl.children[index]!==row))listEl.replaceChildren(...desired);
 }

 /* ---------- messages ---------- */
 const dayLabel=value=>{const d=new Date(value),now=new Date();if(d.toDateString()===now.toDateString())return 'Today';const y=new Date(now);y.setDate(now.getDate()-1);if(d.toDateString()===y.toDateString())return 'Yesterday';return d.toLocaleDateString([],{day:'numeric',month:'long',year:'numeric'})};
 const stateTicks={queued:'🕐',delivering:'✓',delivered:'✓✓',failed:'⚠ failed',ambiguous:'⚠ maybe failed'};
 function imageURL(message,image){
  const key=message.id+':'+image.id;
  if(imageURLs.has(key))return Promise.resolve(imageURLs.get(key));
  return fetch(mediaEndpoint(message.id,image.id),{credentials:'same-origin',signal:AbortSignal.timeout(30000)})
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
   imageURL(message,image).then(url=>{if(!url)return;const btn=mediaButton(url,{kind:mediaKind(image.mediaType,url),alt,label,messageId:message.id,imageId:image.id});row.insertBefore(btn,row.querySelector('.meta'))});
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
 messagesEl.addEventListener('scroll',()=>{stickToBottom=messagesEl.scrollHeight-messagesEl.scrollTop-messagesEl.clientHeight<120;if(selected)scrollMemory.set(selected,messagesEl.scrollTop)});
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
  if(box.processing&&!box.streaming){
   const t=document.createElement('div');t.className='msg agent processing';
   const mini=document.createElement('span');mini.className='processing-mascot';mini.innerHTML=mascotMiniSVG(box.id);
   const dots=document.createElement('span');dots.className='typing-dots';
   for(let i=0;i<3;i++)dots.append(document.createElement('span'));
   const label=document.createElement('span');label.className='typing-label';label.textContent='agent is processing…';
   const tv=document.createElement('button');tv.type='button';tv.className='tv-button';tv.title='Hover to preview; click for Desktop/TMUX control';tv.setAttribute('aria-label','Preview the desktop and open Desktop or TMUX control');
   tv.append(tvIcon());
   tv.onmouseenter=()=>showTvPreview(tv,box);
   tv.onmouseleave=scheduleHideTvPreview;
   tv.onfocus=()=>showTvPreview(tv,box);
   tv.onblur=scheduleHideTvPreview;
   tv.onclick=()=>void openBoxControl(box,'desktop');
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
  for(const id of [...boxes.keys()])if(!alive.has(id)){const cached=avatarCache.get(id);if(cached?.url)URL.revokeObjectURL(cached.url);boxes.delete(id);avatarCache.delete(id);previewFetched.delete(id);tvReplayCache.delete(id)}
  for(const [id,b] of current)boxes.set(id,b);
  if(selected&&!boxes.has(selected)){selected='';lastSignature='';appEl.classList.remove('in-chat');$('#chat-conversation').hidden=true;$('#chat-empty').hidden=false}
  await loadPreviews(force);
  if(selected)applySeen(selected);
  renderRows();
 }
 async function loadPreviews(force){
  await Promise.allSettled([...boxes.keys()].map(async id=>{
   if(id===selected)return;// open conversation refreshes itself
   if(!force&&Date.now()-(previewFetched.get(id)||0)<30000)return;
   const messages=await api(boxPath(id)+'/messages?limit=20');
   const box=boxes.get(id);
   // The box may have been opened (or fully loaded) while the preview was in
   // flight; never let a 20-message preview overwrite an open conversation.
   if(!box||id===selected||box.historyLoaded)return;
   box.messages=messages||[];box.historyLoaded=false;box.hasOlder=false;previewFetched.set(id,Date.now());
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
  if(key!==headerAvatarKey){headerAvatarKey=key;$('#chat-header-avatar').replaceChildren(avatarNode(box,false,true))}
  $('#chat-clear-context').disabled=box.state!=='running'||(box.defaultAgent||'shell')==='shell';
  $('#chat-workspace').href='/boxes/'+encodeURIComponent(box.id);
 }
 async function refreshMessages(force){
  if(!selected)return;
  const id=selected,box=boxes.get(id);if(!box)return;
  const epoch=viewEpoch;
  const messages=await api(boxPath(id)+'/messages?limit=50');
  // Drop a response that arrives after the user moved to another box.
  if(epoch!==viewEpoch||selected!==id||boxes.get(id)!==box)return;
  const latest=messages||[];
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
  renderRows();renderInspect();
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
  viewEpoch++;
  // Never show one box's transcript while another is loading: drop the old
  // messages (and any floating preview) before the new history arrives.
  if(messagesEl.dataset.box!==id){messagesEl.replaceChildren();messagesEl.dataset.box=id;hideTvPreview()}
  selected=id;lastSignature='';
  // Restore where this box was left instead of always jumping to the bottom;
  // first-time opens (no memory) start at the newest message.
  const savedScroll=scrollMemory.get(id);
  stickToBottom=savedScroll==null;
  const restoredDraft=inputDrafts[id]||'';
  if(inputEl.value!==restoredDraft){inputEl.value=restoredDraft;grow()}
  $('#chat-empty').hidden=true;$('#chat-conversation').hidden=false;
  appEl.classList.add('in-chat');
  renderHeader();
  statusEl.textContent='';
  closeForwardMenu();
  closeTakeover();
  renderInspect();
  if(!(boxes.get(id).messages||[]).length)doodle('Loading messages…');
  try{await refreshMessages(true)}catch(e){statusEl.textContent=e.message}finally{doodle('')}
  if(savedScroll!=null)requestAnimationFrame(()=>{messagesEl.scrollTop=savedScroll});
  inputEl.focus();
 }

 /* ---------- composer ---------- */
 function grow(){inputEl.style.height='auto';inputEl.style.height=Math.min(inputEl.scrollHeight,150)+'px'}
 inputEl.addEventListener('input',()=>{grow();if(!selected)return;inputDrafts[selected]=inputEl.value;clearTimeout(inputDraftTimer);inputDraftTimer=setTimeout(saveInputDrafts,250)});
 inputEl.addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.shiftKey){event.preventDefault();composer.requestSubmit()}});
 function renderDrafts(){
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
   remove.onclick=()=>{drafts=drafts.filter(d=>d!==entry);URL.revokeObjectURL(entry.url);drafts.forEach((d,i)=>d.number=i+1);renderDrafts()};
   wrap.append(open,remove);draftsEl.append(wrap);
  }
 }
 async function uploadImages(files){
  for(const file of files){
   if(drafts.length>=8){statusEl.textContent='Attach at most 8 files.';break}
   const isVideo=file.type==='video/mp4'||file.type==='video/webm';
   const isImage=['image/png','image/jpeg','image/gif'].includes(file.type);
   if(!isVideo&&!isImage){statusEl.textContent='Choose PNG, JPEG, GIF, MP4 or WebM.';continue}
   const limitMiB=isVideo?100:25;
   if(file.size>limitMiB*1024*1024){statusEl.textContent=(isVideo?'Videos':'Images')+' must be at most '+limitMiB+' MiB.';continue}
   try{
    const response=await fetch('/v1/run-once-images',{method:'POST',credentials:'same-origin',body:file,signal:AbortSignal.timeout(120000)});
    let result;try{result=await response.json()}catch{}
    if(!response.ok)throw Error(result?.error||'Upload failed.');
    drafts.push({id:result.id,number:drafts.length+1,url:URL.createObjectURL(file),kind:isVideo?'video':'image',mediaType:file.type});
    renderDrafts();
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
  const text=inputEl.value,images=drafts.map(({id,number})=>({id,number}));
  if(!text.trim()&&!images.length)return;
  const fingerprint=text+'\n'+images.map(i=>i.id).join(',');
  if(fingerprint!==pendingFingerprint||!pendingKey){pendingKey=crypto.randomUUID();pendingFingerprint=fingerprint}
  const send=$('#send');send.disabled=true;
  const showPending=(box.defaultAgent||'shell')!=='shell'&&!/^\/silent(?:\s|$)/.test(text);
  const pendingAt=performance.now();
  // Clear the composer the moment the message is handed off so typing can
  // continue immediately. The text and drafts are restored if the send fails.
  inputEl.value='';grow();
  if(inputDrafts[boxID]){delete inputDrafts[boxID];saveInputDrafts()}
  const sentDrafts=drafts;drafts=[];renderDrafts();
  // Delivery can finish after a fast MCP reply, so show the outgoing message
  // and existing processing state while the synchronous POST is in flight.
  if(showPending){pendingSends.set(boxID,{messageCount:(box.messages||[]).length,text,at:new Date().toISOString()});summarize(boxID);renderHeader();renderRows();renderMessages(box)}
  let settled=false;
  try{
   const result=await api(boxPath(boxID)+'/messages','POST',{'Idempotency-Key':pendingKey},{text,images});
   for(const d of sentDrafts)URL.revokeObjectURL(d.url);
   pendingKey='';pendingFingerprint='';
   statusEl.textContent=result?.message?.state==='silent'?'Note saved without waking the agent.':'';
   if(showPending&&result?.message?.state!=='silent')await new Promise(resolve=>setTimeout(resolve,Math.max(0,350-(performance.now()-pendingAt))));
   pendingSends.delete(boxID);
   if(selected===boxID)await refreshMessages(true);
   else{summarize(boxID);renderRows()}
   settled=true;
  }catch(e){
   statusEl.textContent=e.message;
   if(!inputEl.value)inputEl.value=text;
   inputDrafts[boxID]=inputEl.value;saveInputDrafts();
   // Restore the drafts handed to the failed send alongside anything the user
   // attached meanwhile, so no blob URL is lost or leaked.
   if(sentDrafts.length){drafts=[...sentDrafts,...drafts].map((draft,index)=>({...draft,number:index+1}));renderDrafts();grow()}
  }
  finally{
   if(pendingSends.delete(boxID)||(showPending&&!settled)){
    summarize(boxID);
    if(selected===boxID){renderHeader();renderMessages(box)}
    renderRows();
   }
   send.disabled=false;
  }
 };
 $('#chat-back').onclick=()=>{appEl.classList.remove('in-chat');history.replaceState(null,'',location.pathname)};
 addEventListener('hashchange',()=>{const id=new URLSearchParams(location.hash.slice(1)).get('box');if(id&&id!==selected&&boxes.has(id))void openBox(id)});

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
 const fmtAgo=value=>{const s=Math.max(0,(Date.now()-new Date(value).getTime())/1000);if(s<60)return Math.round(s)+'s ago';if(s<3600)return Math.round(s/60)+' min ago';if(s<86400)return Math.round(s/3600)+' h ago';return Math.round(s/86400)+' d ago'};
 const lastMessage=(messages,direction)=>[...messages].reverse().find(m=>m.direction===direction);
 const stateClass=state=>state==='running'?'ok':state==='starting'?'warn':'alert';
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
  $('#inspect-subtitle').textContent=agent+' · '+(box.provider||'provider unknown');
  const badges=$('#inspect-badges');badges.replaceChildren();
  const badge=(text,cls)=>{const b=document.createElement('span');b.className='inspect-badge'+(cls?' '+cls:'');b.textContent=text;badges.append(b)};
  badge(box.state,stateClass(box.state));
  badge(agent,'agent');
  if(box.provider)badge(box.provider);
  if(box.role)badge(box.role);
  fillRows($('#inspect-runtime-rows'),[
   ['State',stateText,stateClass(box.state)],
   ['Agent',agent],
   ['Provider',box.provider||'—'],
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
  if(box.state==='running'){
   const control=document.createElement('button');control.type='button';control.textContent='Control desktop';
   control.title='Open the live desktop in a popup';control.onclick=()=>void openBoxControl(box,'desktop');quick.append(control);
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
  if(box.state==='running')act('Re-sync','Re-push the saved config to the running box',()=>void resyncBox(box));
  if(box.state==='running')act('Restart…','Hibernate and start again; running sessions end',()=>void restartBox(box));
  maybeLoadInspectContacts(box);
  if(inspectContactsFor===box.id&&inspectContactCache)renderContactCandidates(box);
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
  inspect.classList.toggle('with-contacts',owner);
  $('#chat-info').setAttribute('aria-expanded',String(inspectOpen));
  if(inspectOpen){controllerPing=null;void samplePing();inspectTimer=setInterval(()=>void samplePing(),5000)}
  else{clearInterval(inspectTimer);controllerPing=null;inspectContactsFor='';inspectContactCache=null}
 };
 $('#inspect-close').onclick=()=>{inspectOpen=false;inspect.hidden=true;$('#chat-info').setAttribute('aria-expanded','false');clearInterval(inspectTimer);controllerPing=null;inspectContactsFor='';inspectContactCache=null};

 /* ---------- inspect drawer: per-box contact graph (owner) ---------- */
 const inspectContacts=$('#inspect-contacts');
 let inspectContactsFor='',inspectProtected=false,inspectContactCache=null;
 const contactView=contact=>{
  const known=boxes.get(contact.contactBoxId)||{};
  return {id:contact.contactBoxId||contact.contactName,name:contact.contactName||known.name||'—',state:contact.contactState||known.state||'unknown',role:contact.contactRole||known.role||'worker',defaultAgent:contact.contactAgent||known.defaultAgent||''};
 };
 function graphNode(view,{center=false,contact=false}={}){
  const node=document.createElement('div');node.className='cg-node'+(center?' cg-center':'');
  node.append(avatarNode(view,true));
  const label=document.createElement('span');label.className='cg-label';label.textContent=view.name;label.title=view.name;node.append(label);
  if(contact){const sub=document.createElement('span');sub.className='cg-sub';sub.textContent=(view.role||'worker')+' · '+(view.state||'unknown');node.append(sub)}
  return node;
 }
 // A radial map of the viewed box and its explicit contacts. Contacts beyond
 // the cap stay in the Explicit contacts list below, so the graph never crowds.
 function renderContactGraph(box,contacts){
  const graph=$('#inspect-contact-graph');if(!graph)return;
  const nodes=$('#inspect-contact-graph-nodes'),svg=$('#inspect-contact-graph-lines');
  nodes.replaceChildren();svg.replaceChildren();
  const views=(contacts||[]).slice(0,6).map(contactView);
  const w=graph.clientWidth||320,h=graph.clientHeight||240,cx=w/2,cy=h/2;
  const radius=Math.max(40,Math.min(w,h)/2-46);
  graph.setAttribute('aria-label',box.name+' has '+views.length+' contact'+(views.length===1?'':'s')+' shown in the contact map');
  svg.setAttribute('viewBox','0 0 '+w+' '+h);
  const place=(view,dx,dy,opts)=>{const node=graphNode(view,opts);node.style.left=(cx+dx)+'px';node.style.top=(cy+dy)+'px';nodes.append(node)};
  for(let i=0;i<views.length;i++){
   const angle=-Math.PI/2+(i/views.length)*Math.PI*2;
   const dx=Math.cos(angle)*radius,dy=Math.sin(angle)*radius;
   const line=document.createElementNS('http://www.w3.org/2000/svg','line');
   line.setAttribute('x1',String(cx));line.setAttribute('y1',String(cy));
   line.setAttribute('x2',String(cx+dx));line.setAttribute('y2',String(cy+dy));svg.append(line);
   place(views[i],dx,dy,{contact:true});
  }
  place({id:box.id,name:box.name,state:box.state,role:box.role||'worker',defaultAgent:box.defaultAgent||''},0,0,{center:true});
 }
 // Every other box in the loaded fleet is one click away from being a contact.
 function renderContactCandidates(box){
  const root=$('#inspect-contact-candidates');if(!root)return;
  const filter=($('#inspect-contact-filter')?.value||'').trim().toLowerCase();
  const taken=new Set((inspectContactCache||[]).map(c=>c.contactBoxId||c.contactName));
  const others=[...boxes.values()].filter(b=>b.id!==box.id&&b.state!=='deleting');
  const available=others.filter(b=>!taken.has(b.id)&&!taken.has(b.name))
   .filter(b=>!filter||b.name.toLowerCase().includes(filter)||(b.defaultAgent||'').toLowerCase().includes(filter))
   .sort((a,b)=>a.name.localeCompare(b.name));
  root.replaceChildren();
  if(!available.length){
   const empty=document.createElement('p');empty.className='empty';
   empty.textContent=!others.length?'No other boxes in this fleet yet.':filter?'No box matches “'+filter+'”.':'Every other box is already a contact.';
   root.append(empty);return;
  }
  for(const candidate of available){
   const row=document.createElement('div');row.className='contact-candidate';row.dataset.boxId=candidate.id;
   row.append(avatarNode(candidate,true));
   const info=document.createElement('div');info.className='contact-info';
   const name=document.createElement('div');name.className='contact-name';name.textContent=candidate.name;
   const meta=document.createElement('div');meta.className='contact-meta';meta.textContent=(candidate.role||'worker')+' · '+(candidate.defaultAgent||'agent')+' · '+(candidate.state||'unknown');
   info.append(name,meta);row.append(info);
   const add=document.createElement('button');add.type='button';add.textContent='Add';add.title='Add '+candidate.name+' as a contact';
   add.onclick=async()=>{add.disabled=true;const status=$('#inspect-contact-status');
    try{await api(boxPath(box.id)+'/contacts','PUT',{},{contact:candidate.id});await loadInspectContacts(box)}
    catch(e){status.textContent=e.message;add.disabled=false}};
   row.append(add);root.append(row);
  }
 }
 async function loadInspectContacts(box){
  const status=$('#inspect-contact-status'),list=$('#inspect-contact-list');
  status.textContent='Loading…';
  try{
   const [contacts,protection]=await Promise.all([api(boxPath(box.id)+'/contacts'),api(boxPath(box.id)+'/protection')]);
   if(!inspectOpen||selected!==box.id)return;
   inspectContactCache=contacts||[];
   inspectProtected=!!protection.protected;
   const role=box.role==='manager'?'manager':'worker';
   $('#inspect-contact-role').textContent=role;
   $('#inspect-toggle-role').textContent=role==='manager'?'Make worker':'Make manager';
   $('#inspect-protection-label').textContent=inspectProtected?'Protected — managers cannot see or message this box':'Not protected';
   $('#inspect-toggle-protection').textContent=inspectProtected?'Remove protection':'Protect box';
   list.replaceChildren();
   if(!contacts.length){const empty=document.createElement('li');empty.className='empty';empty.textContent='No explicit contacts yet.';list.append(empty)}
   for(const contact of contacts){
    const item=document.createElement('li');
    const info=document.createElement('div');info.className='contact-info';
    const name=document.createElement('div');name.className='contact-name';name.textContent=contact.contactName;
    const meta=document.createElement('div');meta.className='contact-meta';meta.textContent=(contact.contactRole||'worker')+' · '+(contact.contactState||'unknown');
    info.append(name,meta);item.append(info);
    const remove=document.createElement('button');remove.type='button';remove.className='linkbtn';remove.textContent='Remove';remove.title='Remove '+contact.contactName;
    remove.onclick=async()=>{remove.disabled=true;try{await api(boxPath(box.id)+'/contacts/'+encodeURIComponent(contact.contactName),'DELETE');await loadInspectContacts(box)}catch(e){status.textContent=e.message;remove.disabled=false}};
    item.append(remove);list.append(item);
   }
   renderContactGraph(box,contacts);
   renderContactCandidates(box);
   status.textContent=role==='manager'?'A manager may message every non-protected box even without explicit contacts.':'A worker may message only the explicit contacts listed above.';
  }catch(e){status.textContent=e.message}
 }
 function maybeLoadInspectContacts(box){
  if(!owner){inspectContacts.hidden=true;inspectContactsFor='';inspectContactCache=null;return}
  inspectContacts.hidden=false;
  if(inspectContactsFor===box.id)return;
  inspectContactsFor=box.id;inspectContactCache=null;void loadInspectContacts(box);
 }
 $('#inspect-contact-filter').oninput=()=>{const box=boxes.get(selected);if(box&&inspectContactsFor===box.id)renderContactCandidates(box)};
 addEventListener('resize',()=>{const box=boxes.get(selected);if(inspectOpen&&box&&inspectContactsFor===box.id&&inspectContactCache)renderContactGraph(box,inspectContactCache)});
 $('#inspect-contact-form').onsubmit=async event=>{event.preventDefault();const box=boxes.get(selected);if(!box)return;const button=event.target.querySelector('button');button.disabled=true;try{await api(boxPath(box.id)+'/contacts','PUT',{}, {contact:event.target.elements.contact.value.trim()});event.target.reset();await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}finally{button.disabled=false}};
 $('#inspect-toggle-role').onclick=async()=>{const box=boxes.get(selected);if(!box)return;const next=box.role==='manager'?'worker':'manager';try{await api(boxPath(box.id),'PATCH',{}, {defaultAgent:box.defaultAgent||'shell',role:next});box.role=next;await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};
 $('#inspect-toggle-protection').onclick=async()=>{const box=boxes.get(selected);if(!box)return;try{await api(boxPath(box.id)+'/protection','PUT',{}, {protected:!inspectProtected});await loadInspectContacts(box)}catch(e){$('#inspect-contact-status').textContent=e.message}};

 /* ---------- toasts ---------- */
 function toast(text){const el=document.createElement('div');el.className='toast';el.textContent=text;$('#chat-toasts').append(el);setTimeout(()=>{el.style.opacity='0';setTimeout(()=>el.remove(),400)},3200);}

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
 const newBoxModal=$('#new-box-modal'),createForm=$('#create-box');
 let extrasLoaded=false;
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
  const syncModel=()=>{const option=profileSelect.selectedOptions[0],hasProfile=!!profileSelect.value;modelInput.disabled=!hasProfile;modelPicker.setValue(hasProfile?option?.dataset.model||'':'');modelPicker.setReasoningEffort('');modelPicker.setOptions(window.VMBoxModelPicker.optionsFor(agentSelect.value,[option?.dataset.model]));modelLabel.hidden=!hasProfile;const ref=hasProfile?JSON.parse(profileSelect.value):null;modelPicker.setLoader(ref?.application==='opencode'?()=>api('/v1/login-profiles/opencode/'+encodeURIComponent(ref.name)+'/models'):null)};
  const populate=()=>{
   const previous=profileSelect.value,app=agentSelect.value;profileSelect.replaceChildren(new Option('None',''));
   const choices=profiles.filter(profile=>profile.application===app);
   for(const profile of choices){const option=new Option(profile.name,JSON.stringify({application:profile.application,name:profile.name}));option.dataset.model=profile.model||'';profileSelect.append(option)}modelPicker.setApplication(app);
   if([...profileSelect.options].some(option=>option.value===previous))profileSelect.value=previous;
   profileLabel.hidden=app==='shell'||choices.length===0;root.hidden=profileLabel.hidden&&githubLabel.hidden;syncModel();
  };
  profileSelect.addEventListener('change',syncModel);agentSelect.onchange=populate;populate();
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
 }
 $('#new-box').onclick=openNewBoxModal;
 $('#new-box-close').onclick=()=>{newBoxModal.hidden=true};
 $('#new-box-backdrop').onclick=()=>{newBoxModal.hidden=true};
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
 const rowMenu=$('#row-menu');
 function closeRowMenu(){rowMenu.hidden=true;rowMenu.replaceChildren()}
 function openRowMenu(box,rect){
  rowMenu.replaceChildren();
  const items=[
   ['Show details',()=>{if(!inspectOpen)$('#chat-info').click()}],
   ['Control desktop',()=>{location.hash='box='+box.id;if(box.id!==selected)void openBox(box.id).then(()=>openTakeover('desktop'));else openTakeover('desktop')}],
   ['Instructions…',()=>void openBoxInstructions(box)],
  ];
  const active=selected&&boxes.get(selected);
  if(owner&&active&&active.id!==box.id)items.unshift(['Add '+box.name+' as contact',()=>void addContactFromRow(active,box)]);
  if(owner)items.push(['Imported profiles…',()=>void openBoxCredentials(box)]);
  if(box.state==='running')items.push(['Re-sync config',()=>void resyncBox(box)],['Restart box…',()=>void restartBox(box)]);
  items.push(['Delete box…',()=>openDeleteModal(box),'danger']);
  if(box.state==='running')items.splice(2,0,['Hibernate box',()=>void hibernateBox(box)]);
  for(const item of items){const b=document.createElement('button');b.type='button';b.textContent=item[0];if(item[2])b.className='danger';b.onclick=()=>{closeRowMenu();item[1]()};rowMenu.append(b)}
  rowMenu.hidden=false;
  rowMenu.style.left=Math.max(8,Math.min(rect.left,innerWidth-rowMenu.offsetWidth-8))+'px';
  rowMenu.style.top=Math.max(8,Math.min((rect.bottom||rect.top)+4,innerHeight-rowMenu.offsetHeight-8))+'px';
 }
 // Right-clicking another box while a chat is open is the fastest way to make
 // it a contact of the box the user is looking at.
 async function addContactFromRow(active,target){
  try{
   await api(boxPath(active.id)+'/contacts','PUT',{},{contact:target.id});
   toast(target.name+' is now a contact of '+active.name+'.');
   if(inspectOpen&&selected===active.id)await loadInspectContacts(active);
  }catch(e){toast(e.message||'Could not add that contact')}
 }
 document.addEventListener('click',event=>{if(!rowMenu.hidden&&!rowMenu.contains(event.target))closeRowMenu()});
 addEventListener('keydown',event=>{if(event.key==='Escape'){closeRowMenu();closeSheets();if(!newBoxModal.hidden)newBoxModal.hidden=true;if(!deleteModal.hidden)deleteModal.hidden=true;if(!takeover.hidden)closeTakeover();}});
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
  if(event.data?.type==='vmbox-push'){clearTimeout(pushTimer);pushTimer=setTimeout(()=>{if(document.hidden)return;void refreshMessages();void loadBoxes(true)},250)}
  if(event.data?.type==='vmbox-open'&&event.data.url){const url=new URL(event.data.url,location.origin);if(url.hash!==location.hash)location.hash=url.hash}
 });

 /* ---------- polling ---------- */
 function schedule(){
  clearTimeout(boxTimer);clearTimeout(msgTimer);
  boxTimer=setTimeout(tickBoxes,30000);
  msgTimer=setTimeout(tickMessages,3000);
 }
 async function tickBoxes(){try{if(!document.hidden)await loadBoxes()}catch{}boxTimer=setTimeout(tickBoxes,30000)}
 async function tickMessages(){try{if(!document.hidden&&selected)await refreshMessages()}catch{}msgTimer=setTimeout(tickMessages,3000)}
 document.addEventListener('visibilitychange',()=>{if(!document.hidden){clearTimeout(boxTimer);clearTimeout(msgTimer);void tickBoxes();void tickMessages()}});
 filterEl.addEventListener('input',()=>{clearTimeout(filterTimer);filterTimer=setTimeout(renderRows,130)});
 $('#refresh').onclick=async()=>{try{await loadBoxes(true);if(selected)await refreshMessages(true);$('#error').textContent=''}catch(e){$('#error').textContent=e.message}};

 /* ---------- auth ---------- */
 $('#login').onsubmit=async event=>{
  event.preventDefault();
  try{await api('/v1/browser-session','POST',{Authorization:'Bearer '+event.target.elements.token.value});event.target.reset();await enter()}catch(e){$('#error').textContent=e.message}
 };
 $('#logout').onclick=async()=>{
  clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);
  closeTakeover();
  inspectOpen=false;inspect.hidden=true;clearInterval(inspectTimer);controllerPing=null;
  try{await api('/v1/browser-session','DELETE')}catch{}
  for(const url of imageURLs.values())URL.revokeObjectURL(url);imageURLs.clear();
  for(const cached of avatarCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  for(const cached of tvShotCache.values()){if(cached?.url)URL.revokeObjectURL(cached.url)}
  avatarCache.clear();previewFetched.clear();tvReplayCache.clear();hideTvPreview();headerAvatarKey='';
  for(const d of drafts)URL.revokeObjectURL(d.url);drafts=[];renderDrafts();pendingKey='';pendingFingerprint='';
  boxes.clear();rows.clear();listEl.replaceChildren();messagesEl.replaceChildren();delete messagesEl.dataset.box;
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
  }catch(e){$('#error').textContent=e.message;$('#login').hidden=false;$('#login input[name="token"]').focus()}
 }
 addEventListener('pagehide',()=>{clearTimeout(boxTimer);clearTimeout(msgTimer);clearTimeout(pushTimer);clearTimeout(filterTimer);for(const url of imageURLs.values())URL.revokeObjectURL(url)});

 /* ---------- instruction presets, box instructions, imported profiles ---------- */
 function mk(tag,text){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el}
 function mdPreview(root,text){root.replaceChildren();root.append(typeof window.markdownToNodes==='function'?window.markdownToNodes(text||''):mk('pre',text||''))}
 document.querySelectorAll('[data-close]').forEach(el=>el.addEventListener('click',()=>{const sheet=el.closest('.sheet');if(sheet)sheet.hidden=true}));
 function closeSheets(){document.querySelectorAll('.sheet').forEach(sheet=>{sheet.hidden=true})}
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
   toast('Login profiles updated for '+boxCredentialTarget.name+'.');
  }catch(e){status.textContent=e.message}
 };

 /* ---------- generated-look controls ---------- */
 const randomSeed=()=>'0x'+Array.from({length:8},()=>'0123456789abcdef'[Math.floor(Math.random()*16)]).join('');
 $('#variant-dice').onclick=()=>{theme.seed=randomSeed();saveTheme();applyVariant();toast('Rolled a new daylight seed.')};
 $('#variant-seed').addEventListener('change',event=>{theme.seed=event.target.value.trim()||DEFAULT_SEED;saveTheme();applyVariant()});
 $('#chat-menu').onclick=()=>{closeSheets();$('#chat-menu-sheet').hidden=false};
 $('#logout').addEventListener('click',()=>{$('#chat-menu-sheet').hidden=true},{capture:true});
 applyVariant();

 void enter();
})();
