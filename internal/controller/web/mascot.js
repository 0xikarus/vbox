/* vbox mascot statechart. Parallel regions:
 * BODY: idle | busy.scan | busy.dots | asking | unread | failed | hibernated | waking | sending
 * EYES: default | blink | doubleBlink | wink | round | tall | wide | happy | sleepy | hidden
 * GAZE: ahead | cursor | lookAround | saccade
 * From -> to                 Event/guard                  Choreography, duration
 * BODY any -> busy.scan/dots WORK, signal=busy           .08s anticipate, .38s split, .3s settle
 * BODY any -> asking         SEND, signal=asking          .32s dot, .28s stem shot, dot-pivot spring; bob every 2.5s
 * BODY any -> unread         DONE/PRAISE, unread          .08s anticipate, .34s morph, notice dot scale
 * BODY any -> failed         ERROR, signal=failed         .32s dot, .35s stem shot, heavier dot-pivot spring
 * BODY asking/failed -> any  next signal, stem visible     .08s stretch, .06s gap close, .2s collapse, .34s ball return
 * BODY any -> hibernated     SLEEP, signal=sleeping       .34s soft sink and eye close
 * BODY hibernated -> waking  WAKE/WORK, signal=starting   .15s inhale, .44s grow, .3s spring settle
 * BODY any -> idle           STOP/CALM/READY/SETTLE      .34s return morph, pop, settle
 * BODY any -> sending        comet() when visible         squash, .46s arc/smear, body pop on return
 * EYES default -> blink      timer, settled idle          close .09, hold .04, open .15s
 * EYES default -> doubleBlink second blink queued        same, with .25s gap
 * EYES default -> wink       expression timer            one eye, hold .25s
 * EYES any -> tall            unread/wake/idle moment      W 1.05x, H 1.35x, eye pop; .36s
 * EYES any -> wide            busy-to-idle/idle moment    W 1.6x, H .6x, soft ease; .4s
 * EYES any -> round/happy/sleepy/hidden mood or signal    shape/opacity morph; .3-.48s
 * GAZE any -> cursor         shared pointer, nearby if small spring from live offset; expires after 2.5s
 * GAZE any -> lookAround     1.5-4s idle timer            eased 0.6-2s hold, then centre
 * GAZE any -> saccade        idle target                   .12-.16s eye spring, body follows
 * GAZE any -> scan           occasional idle target       .9s left-to-right target tween
 * GAZE any -> ahead          pointer leave/return         spring from live offset
 * Interrupt: jump/send retarget from the live pose; blink queues through eye morph.
 */
(function(global){
 'use strict';
 // Bright box hues; coral is reserved for the account mascot.
 const COLORS=['#965FF0','#378EF5','#24C77A','#22B8D6','#FF8A38','#F5BC29','#F253B1','#9BCB3C'];
 const MOODS=['idle','working','waiting','happy','laughing','angry','sleeping','waking'];
 const MACHINE={idle:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry',SLEEP:'sleeping',WAKE:'waking'},working:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},waiting:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},happy:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle',SLEEP:'sleeping'},laughing:{SETTLE:'happy',WORK:'working',ERROR:'angry',SLEEP:'sleeping'},angry:{CALM:'idle',PRAISE:'happy',WORK:'working',SLEEP:'sleeping'},sleeping:{WAKE:'waking',WORK:'waking'},waking:{READY:'idle',WORK:'working',SLEEP:'sleeping'}};
 const MOTION=Object.freeze({
  snap:{duration:.24,ease:[.45,0,.3,1]},
  pop:{type:'spring',visualDuration:.28,bounce:.35},
  settle:{type:'spring',visualDuration:.3,bounce:.15},
  float:{period:3.6,amplitude:.02,gestureMin:10000,gestureMax:20000},
  exit:{duration:.16,ease:[.55,0,.8,.3]},
  anticipation:{duration:.08,ease:[.55,0,1,1]},
  eye:{duration:.24,ease:[.2,.9,.1,1]},
  gaze:{maxX:15.2,maxY:7.6,saccade:{stiffness:560,damping:34},cursor:{stiffness:205,damping:25},idle:{stiffness:190,damping:24},reduced:{stiffness:60,damping:16},body:{stiffness:75,damping:18},scanDuration:.9},
  split:{duration:.38,ease:[.2,.9,.1,1],stagger:.035},
  stem:{duration:.34,ease:[.2,.9,.1,1]},
  wake:{duration:.44,ease:[.2,.9,.1,1]},
  comet:{duration:.46,ease:[.2,.9,.1,1]},
  bang:{spring:{type:'spring',stiffness:420,damping:10,mass:.55},heavy:{type:'spring',stiffness:280,damping:11,mass:.9},bobInterval:2500},
 });
 const hash=s=>{let h=2166136261;for(const c of String(s)){h^=c.charCodeAt(0);h=Math.imul(h,16777619)}return h>>>0};
 const traits=seed=>{const h=hash(seed);return {shape:'circle',color:COLORS[(h>>>1)%COLORS.length],eyeSpread:((h>>>12)%7-3)*.35,eyeTilt:((h>>>17)%7-3)*.12,phase:(h%1000)/1000*6.283,blink:2.3+(h%190)/100,eyes:'A'}};
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const eyeGeometry=p=>{const cx=(p[0]+p[6])/2,cy=(p[1]+p[7])/2,dx=p[6]-p[0],dy=p[7]-p[1];return {cx,cy,length:Math.hypot(dx,dy),angle:Math.atan2(dy,dx)*180/Math.PI}};
 const sleepEye=(cx,cy)=>eyeLine(cx,cy,8,0);
 const happyEye=(cx,cy)=>`M${(cx-5).toFixed(3)} ${(cy+3).toFixed(3)} C${(cx-2.8).toFixed(3)} ${(cy-3).toFixed(3)} ${(cx+2.8).toFixed(3)} ${(cy-3).toFixed(3)} ${(cx+5).toFixed(3)} ${(cy+3).toFixed(3)}`;
 const eyeLine=(cx,cy,length,angle)=>{const a=angle*Math.PI/180,dx=Math.cos(a)*length/2,dy=Math.sin(a)*length/2,x1=cx-dx,y1=cy-dy,x2=cx+dx,y2=cy+dy;return `M${x1.toFixed(3)} ${y1.toFixed(3)} C${(x1+(x2-x1)/3).toFixed(3)} ${(y1+(y2-y1)/3).toFixed(3)} ${(x1+2*(x2-x1)/3).toFixed(3)} ${(y1+2*(y2-y1)/3).toFixed(3)} ${x2.toFixed(3)} ${y2.toFixed(3)}`};
 const rawEyesFor=(mood,variant='A')=>{const spread=variant==='C'?1:0,L=39-spread,R=61+spread,lo=53;
  const line=(cx,length=1,dy=0)=>{const x=0,y=4.5*length;return [cx-x,lo+dy-y,cx-x/3,lo+dy-y/3,cx+x/3,lo+dy+y/3,cx+x,lo+dy+y]};
  const poses={curious:[.045,.045],wink:[1,.28],excited:[1,1],working:[.9,.9],waiting:[.85,.85],happy:[.68,.68],laughing:[.52,.52],angry:[.8,.8],sleeping:[.55,.55],waking:[1,1]};
  const lengths=poses[mood]||[1,1];return [line(L,lengths[0]),line(R,lengths[1])];
 };
 const eyesFor=(mood,variant='A',t=null)=>rawEyesFor(mood,variant).map((p,j)=>p.map((v,i)=>i%2===0?50+(j?1:-1)*(13.3+(t?.eyeSpread||0)):57+(v-53)*.79));
 const MOOD_GLYPH={angry:'error'};
 const EXTRA_EVENT={DONE:['happy','happy'],PRAISE:['happy','happy'],THANKS:['happy','happy'],APPROVAL:['waiting',null],NEEDS_ATTENTION:['waiting',null],CRASH:['angry','error'],FAILED:['angry','error'],RETRY:['waiting',null],RESTART:['waking','surprised'],CLEAR_CONTEXT:['waking','surprised'],NEW_CHAT:['waking','surprised'],USAGE:['idle',null]};
 const glyphOne=kind=>{switch(kind){
  case 'happy':return '<path d="M-7 5 Q0 -7 7 5"/>';
  case 'surprised':return '<circle cx="0" cy="0" r="5.5"/>';
  case 'error':return '<path d="M-6 -6 L6 6 M6 -6 L-6 6"/>';
  case 'sleeping':return '<path d="M-6 -5 L6 -5 L-6 5 L6 5"/>';
  case 'focused':return '<circle cx="0" cy="0" r="2.3" fill="white" stroke="none"/>';
  default:return ''}};
 const glyphMarkup=kind=>kind?`<g class="vbox-mascot-glyphs" stroke="#fff" stroke-width="4.4" stroke-linecap="round" stroke-linejoin="round" fill="none"><g transform="translate(36 57)">${glyphOne(kind,0)}</g><g transform="translate(64 57)">${glyphOne(kind,1)}</g></g>`:'';
 const POINTS=64;
 const circlePoints=(radius=36,cx=50,cy=52)=>Array.from({length:POINTS},(_,i)=>{const a=-Math.PI/2+i*2*Math.PI/POINTS;return [cx+radius*Math.cos(a),cy+radius*Math.sin(a)]});
 const stemPath=(t,detach=0)=>{const bottom=82-15*detach,top=bottom-48*t,w=5*t,b=3.125*t,r=4*t,cap=6*detach;return `M${(50-w).toFixed(2)} ${(top+r).toFixed(2)} Q${(50-w).toFixed(2)} ${top.toFixed(2)} 50 ${top.toFixed(2)} Q${(50+w).toFixed(2)} ${top.toFixed(2)} ${(50+w).toFixed(2)} ${(top+r).toFixed(2)} L${(50+b).toFixed(2)} ${bottom.toFixed(2)} Q50 ${(bottom+cap).toFixed(2)} ${(50-b).toFixed(2)} ${bottom.toFixed(2)} Z`};
 const pointsPath=pts=>{let d=`M${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;for(let i=0;i<pts.length;i++){const p0=pts[(i-1+pts.length)%pts.length],p1=pts[i],p2=pts[(i+1)%pts.length],p3=pts[(i+2)%pts.length];d+=` C${(p1[0]+(p2[0]-p0[0])/6).toFixed(2)} ${(p1[1]+(p2[1]-p0[1])/6).toFixed(2)} ${(p2[0]-(p3[0]-p1[0])/6).toFixed(2)} ${(p2[1]-(p3[1]-p1[1])/6).toFixed(2)} ${p2[0].toFixed(2)} ${p2[1].toFixed(2)}`}return d+' Z'};
 // Every eye outline has the same 64 curved segments, including morphs and lids.
 const eyeOutline=(cx,cy,w,h,bend=0)=>pointsPath(Array.from({length:POINTS},(_,i)=>{const a=i*2*Math.PI/POINTS,x=Math.cos(a)*w/2,y=Math.sin(a)*h/2;return [cx+x,cy+y+bend*(1-(2*x/w)**2)]}));
 const eyeProfile=(mood,geo,index)=>{const w=mood==='curious'?8.2:mood==='sleeping'?8.5:mood==='happy'?10:7.5;
  const h=mood==='curious'?8.2:mood==='sleeping'?4.3:mood==='happy'?4.5:mood==='wink'&&index===1?4.2:w+geo.length;
  return {w,h,bend:mood==='happy'?-2.5:mood==='sleeping'?.55:0}};
 const eyeOutlineFor=(mood,geo,index)=>{const p=eyeProfile(mood,geo,index);return eyeOutline(geo.cx,geo.cy,p.w,p.h,p.bend)};
 function svg(seed,mood='idle',mini=false,appearance={},expression=MOOD_GLYPH[mood]||null){const t=traits(seed);if(appearance.color)t.color=appearance.color;const p=pointsPath(circlePoints(39)),variant=appearance.eyes||t.eyes,eyeMood={happy:'happy',surprised:'curious',sleeping:'sleeping',focused:'working'}[expression]||mood,eye=eyesFor(eyeMood,variant,t),eyeGeo=eye.map(eyeGeometry),gid='vb-mascot-hi-'+hash(seed);if(eyeMood!==mood)expression=null;return `<svg class="vbox-mascot vbox-mascot-eyes-${variant}${mini?' vbox-mascot-mini':''}" viewBox="-6 -6 112 112" width="100%" height="100%" preserveAspectRatio="xMidYMid meet" data-mood="${mood}" data-expression="${expression||''}" data-seed="${esc(seed)}" role="img" aria-label="${mood} mascot" ><defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#fff" stop-opacity=".16"/><stop offset=".52" stop-color="#fff" stop-opacity=".03"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient></defs><g class="vbox-mascot-bang-rig"><g class="vbox-mascot-motion"><g class="vbox-mascot-gaze-follow"><g class="vbox-mascot-body" ><g class="vbox-mascot-dot-rig"><path class="vbox-mascot-shape" d="${p}" fill="${mood==='angry'?'#FF6F59':mood==='sleeping'?'#a9a8bf':t.color}"/><path class="vbox-mascot-highlight" d="${p}" fill="url(#${gid})" pointer-events="none"/></g><g class="vbox-mascot-blush" opacity="0"><ellipse cx="27" cy="66" rx="7" ry="3.2"/><ellipse cx="73" cy="66" rx="7" ry="3.2"/></g><g class="vbox-mascot-eyes" opacity="${expression?0:1}">${eyeGeo.map((g,i)=>`<g class="eye-blink" transform="translate(${g.cx} ${g.cy}) scale(1 ${eyeMood==='sleeping'?.45:1}) translate(${-g.cx} ${-g.cy})"><g class="eye-tilt" transform="rotate(0 ${g.cx} ${g.cy})"><g class="eye-shape" transform="translate(${g.cx} ${g.cy}) scale(1 1) translate(${-g.cx} ${-g.cy})"><path class="vbox-mascot-eye ${i?'right':'left'}" vector-effect="none" d="${eyeOutlineFor(eyeMood,g,i)}"/></g></g></g><path class="eye-lid" d="${eyeOutline(g.cx,g.cy,8.5,2.4,.9)}" opacity="0"/>`).join('')}<circle class="vbox-mascot-glint" cx="34" cy="53" r="1.3"/><circle class="vbox-mascot-glint" cx="62" cy="53" r="1.3"/></g><g class="vbox-mascot-glyph-host">${glyphMarkup(expression)}</g></g><path class="vbox-mascot-bang-stem" d="${stemPath(0)}" fill="${t.color}"/></g></g><g class="vbox-mascot-dots" opacity="0"><circle class="vbox-mascot-dot-left" cx="50" cy="52" r="0" fill="${t.color}"/><circle class="vbox-mascot-dot-right" cx="50" cy="52" r="0" fill="${t.color}"/></g><circle class="vbox-mascot-notice" cx="79" cy="22" r="5" fill="#378EF5" stroke="#fff" stroke-width="2" opacity="0"/></g></svg>`}
 const animate=global.Motion?.animate;
 const smooth=t=>t*t*(3-2*t);
 const smoother=t=>t*t*t*(t*(t*6-15)+10);
 if(!animate){
  const staticSvg=(seed,mood='idle',mini=false,appearance={})=>`<svg class="vbox-mascot${mini?' vbox-mascot-mini':''}" viewBox="0 0 100 100" role="img" aria-label="${mood} mascot" data-mood="${mood}"><circle class="vbox-mascot-shape" cx="50" cy="50" r="39" fill="${appearance.color||traits(seed).color}"/><ellipse class="vbox-mascot-eye left" cx="37" cy="55" rx="4.5" ry="9.5" fill="#fff"/><ellipse class="vbox-mascot-eye right" cx="63" cy="55" rx="4.5" ry="9.5" fill="#fff"/></svg>`;
  class StaticMascot{
   constructor(host,seed,{appearance={}}={}){this.host=host;this.seed=seed;this.appearance=appearance;this.state='idle';this.signal='idle';this.expression=null;this.dead=false;host.innerHTML=staticSvg(seed,'idle',false,appearance);this.svg=host.querySelector('svg');this.svg.__vboxMascot=this}
   jump(mood,expression=null,signal=null){if(this.dead)return false;this.state=mood;this.signal=signal||mood;this.expression=expression;this.svg.dataset.mood=mood;this.svg.setAttribute('aria-label',mood+' mascot');this.svg.querySelector('.vbox-mascot-shape').setAttribute('fill',mood==='sleeping'?'#a9a8bf':mood==='angry'?'#FF6F59':this.appearance.color||traits(this.seed).color);for(const eye of this.svg.querySelectorAll('.vbox-mascot-eye')){eye.setAttribute('rx',mood==='sleeping'?'5':'4.5');eye.setAttribute('ry',mood==='sleeping'?'1.5':mood==='waking'?'12':'9.5')}return true}
   send(event){const next=MACHINE[this.state]?.[event];return next?this.jump(next):false}
   showExpression(expression){return this.jump(this.state,expression,this.signal)}
   comet(){return Promise.resolve()}
   destroy(){if(this.dead)return;this.dead=true;this.svg.remove()}
  }
  global.VBoxMascot={Mascot:StaticMascot,svg:staticSvg,miniSVG:(seed,mood='idle')=>staticSvg(seed,mood,true),setReducedMotion:()=>{},setGesturePaused:()=>{},traits,MACHINE,shapes:['circle'],colors:COLORS,moods:MOODS};
  return;
 }
 const active=new Set();let forcedReduce=false,gesturePauseCount=0;
 const reduce=matchMedia('(prefers-reduced-motion: reduce)');
 const observer=typeof IntersectionObserver==='undefined'?null:new IntersectionObserver(entries=>{for(const entry of entries){const mascot=entry.target.__vboxMascot;if(mascot){mascot.visible=entry.isIntersecting;mascot.updateVisibility()}}});
 // Chat re-renders temporary avatars. Release their timers and Motion controls
 // once a previously mounted SVG leaves the document.
 const detachedObserver=typeof MutationObserver==='undefined'?null:new MutationObserver(()=>{
  for(const mascot of active){
   if(mascot.svg.isConnected)mascot.wasMounted=true;
   else if(mascot.wasMounted)mascot.destroy();
  }
 });
 detachedObserver?.observe(document.documentElement,{childList:true,subtree:true});
 const quiet=()=>reduce.matches||forcedReduce||document.hidden;
 let pointerFrame=0,pointerIdleTimer=0;
 const pointer={x:0,y:0,type:'mouse',active:false};
 function dispatchPointer(){pointerFrame=0;if(!pointer.active)return;for(const mascot of active)mascot.followPointer(pointer.x,pointer.y,pointer.type)}
 function releasePointer(){pointer.active=false;clearTimeout(pointerIdleTimer);if(pointerFrame){cancelAnimationFrame(pointerFrame);pointerFrame=0}for(const mascot of active)mascot.releasePointer()}
 function queuePointer(e){if(e.pointerType==='touch'||reduce.matches||forcedReduce)return;pointer.x=e.clientX;pointer.y=e.clientY;pointer.type=e.pointerType||'mouse';pointer.active=true;if(!pointerFrame)pointerFrame=requestAnimationFrame(dispatchPointer);clearTimeout(pointerIdleTimer);pointerIdleTimer=setTimeout(releasePointer,2500)}
 window.addEventListener('pointermove',queuePointer,{passive:true});
 window.addEventListener('pointerdown',e=>{if(e.pointerType!=='touch'||reduce.matches||forcedReduce)return;pointer.x=e.clientX;pointer.y=e.clientY;pointer.type='touch';pointer.active=true;if(!pointerFrame)pointerFrame=requestAnimationFrame(dispatchPointer);clearTimeout(pointerIdleTimer);pointerIdleTimer=setTimeout(releasePointer,850)},{passive:true});
 window.addEventListener('mouseout',e=>{if(!e.relatedTarget)releasePointer()},{passive:true});
 window.addEventListener('blur',releasePointer);
 let morphClock=0;
 function syncMorphClock(){let running=false;if(!quiet()&&!gesturePauseCount)for(const mascot of active)if(!mascot.dead&&mascot.visible){running=true;break}if(running&&!morphClock)morphClock=requestAnimationFrame(morphFrame);else if(!running&&morphClock){cancelAnimationFrame(morphClock);morphClock=0}}
 function morphFrame(now){morphClock=0;if(quiet()||gesturePauseCount)return;for(const mascot of active)if(mascot.visible&&!mascot.dead)mascot.frame(now);syncMorphClock()}
 function setGesturePaused(paused){
  gesturePauseCount=Math.max(0,gesturePauseCount+(paused?1:-1));
  if(paused&&gesturePauseCount===1){
   for(const mascot of active)for(const control of mascot.controls)control.pause?.();
  }else if(!paused&&!gesturePauseCount){
   for(const mascot of active){mascot.lastFrame=performance.now();if(!quiet()&&mascot.visible)for(const control of mascot.controls)control.play?.()}
  }
  syncMorphClock();
 }
 document.addEventListener('visibilitychange',()=>{for(const mascot of active)mascot.updateVisibility()});
 reduce.addEventListener?.('change',()=>{if(reduce.matches)releasePointer();for(const mascot of active)mascot.updateVisibility()});
 class Mascot{
  constructor(host,seed,{onMood,appearance={}}={}){
   this.host=host;this.seed=String(seed);this.traits=traits(seed);if(appearance.color)this.traits.color=appearance.color;this.appearance=appearance;this.onMood=onMood||null;
   this.state='idle';this.expression=null;this.signal='idle';this.visible=true;this.dead=false;this.token=0;this.phase='settled';this.controls=new Set();this.timers=[];this.sleepScale=1;this.busyMode=hash(this.seed)%2?'ball':'dots';this.busyInterval=8000+hash(this.seed+'busy')%4001;this.busyRemaining=this.busyInterval;
   host.innerHTML=svg(seed,'idle',false,{...appearance,animated:true});
   this.svg=host.querySelector('svg');if(host.classList.contains('avatar-mascot')||host.classList.contains('processing-mascot'))this.svg.classList.add('vbox-mascot-mini');this.svg.__vboxMascot=this;
   this.motion=this.svg.querySelector('.vbox-mascot-motion');this.bangRig=this.svg.querySelector('.vbox-mascot-bang-rig');this.gazeFollow=this.svg.querySelector('.vbox-mascot-gaze-follow');this.body=this.svg.querySelector('.vbox-mascot-body');this.dotRig=this.svg.querySelector('.vbox-mascot-dot-rig');this.shapePath=this.svg.querySelector('.vbox-mascot-shape');this.highlight=this.svg.querySelector('.vbox-mascot-highlight');this.eyesGroup=this.svg.querySelector('.vbox-mascot-eyes');this.eyes=[...this.svg.querySelectorAll('.vbox-mascot-eye')];this.blinkGroups=[...this.svg.querySelectorAll('.eye-blink')];this.tiltGroups=[...this.svg.querySelectorAll('.eye-tilt')];this.shapeGroups=[...this.svg.querySelectorAll('.eye-shape')];this.lids=[...this.svg.querySelectorAll('.eye-lid')];this.eyeCenters=eyesFor('idle',this.appearance.eyes||this.traits.eyes,this.traits).map(eyeGeometry);this.glyphHost=this.svg.querySelector('.vbox-mascot-glyph-host');this.blush=this.svg.querySelector('.vbox-mascot-blush');this.dots=this.svg.querySelector('.vbox-mascot-dots');this.dotLeft=this.svg.querySelector('.vbox-mascot-dot-left');this.dotRight=this.svg.querySelector('.vbox-mascot-dot-right');this.notice=this.svg.querySelector('.vbox-mascot-notice');this.noticeScale=0;this.setNoticeScale(0);this.bangStem=this.svg.querySelector('.vbox-mascot-bang-stem');
   this.paths={home:pointsPath(circlePoints(39)),bangDot:pointsPath(circlePoints(5,50,82)),dot:pointsPath(circlePoints(5))};this.glanceCount=0;this.blinkedYet=false;this.expressionCount=0;
   this.breathPhase=this.traits.phase;this.breathSpeed=2*Math.PI/3.6;this.breathAccel=0;this.formAngle=0;this.bangAngle=0;this.stemProgress=0;this.stemDetach=0;this.dotX=1;this.dotY=1;this.bangSignal=null;this.bangWiggleActive=false;this.faceScale=1;this.faceRadius=39;this.blinkScale=1;this.blinking=false;this.blinkGeneration=0;this.blinkQueued=false;this.eyeMorphUntil=0;this.gazeUntil=0;this.pendingLook=null;this.pendingEyes=null;this.dotCenterRadius=5;this.lastFrame=0;
   this.chart={body:'idle',eyes:'default',gaze:'ahead'};this.motionSegments={body:0,eyes:0,gaze:0,dots:0,symbol:0,color:0};this.scanBlend=0;this.idleDriftBlend=1;this.currentPath=this.paths.home;this.setPath(this.paths.home);this.look=[0,0];this.gazeTarget=[0,0];this.gazeVelocity=[0,0];this.rightLook=[0,0];this.rightLookVelocity=[0,0];this.cursorActive=false;this.bodyFollow=[0,0,0];this.bodyFollowVelocity=[0,0,0];this.wasMounted=false;this.setEyes('idle',null,true);
   observer?.observe(this.svg);active.add(this);syncMorphClock();this.scheduleBlink();this.scheduleGlance();this.scheduleExpression();this.scheduleGesture();
  }
  transition(region,to,event,guard=()=>true){if(!guard())return false;const from=this.chart[region];if(from!==to){this.chart[region]=to;this.lastTransition={region,from,to,event,at:performance.now()}}return true}
  track(control){this.controls.add(control);if(gesturePauseCount)control.pause?.();control.finished?.finally(()=>this.controls.delete(control)).catch(()=>{});return control}
  stopControls(){if(this.symbolPose)this.paintSymbolPose(this.symbolPose);if(this.cometPose)this.paintCometPose(this.cometPose);for(const control of this.controls)control.stop?.();this.controls.clear();this.symbolPose=null;this.cometPose=null;this.stemAnimating=false;this.bangWiggleActive=false}
  clearTimers(){for(const t of this.timers)clearTimeout(t);this.timers=[];clearTimeout(this.busyTimer);this.busyTimer=null;clearTimeout(this.expressionTimer);this.expressionTimer=null;clearTimeout(this.gestureTimer);this.gestureTimer=null}
  timer(fn,ms){const t=setTimeout(()=>{const index=this.timers.indexOf(t);if(index>=0)this.timers.splice(index,1);fn()},ms);this.timers.push(t);return t}
  setPath(d){this.currentPath=d;this.shapePath.setAttribute('d',d);this.highlight.setAttribute('d',d)}
  eyeDimensions(index){const eye=this.eyes[index],m=this.shapeGroups[index].transform.baseVal.consolidate()?.matrix,blink=this.blinkGroups[index].transform.baseVal.consolidate()?.matrix,b=eye.getBBox();return {w:b.width*(m?Math.hypot(m.a,m.b):1),h:b.height*(m?Math.hypot(m.c,m.d):1)*(blink?Math.hypot(blink.c,blink.d):1)}}
  updateFaceTransform(radius=this.faceRadius){
   const scale=this.faceScale,maxShift=Math.max(0,radius*.9-19.5*scale),scan=this.scanBlend*3.8*Math.sin(this.breathPhase*1.4);
   const drift=this.idleDriftBlend*.8*Math.sin(this.breathPhase*.8+this.traits.phase),driftY=this.idleDriftBlend*.45*Math.sin(this.breathPhase*.62+this.traits.phase);
   const x=Math.max(-maxShift,Math.min(maxShift,this.look[0]+scan+(this.blinking?this.blinkDriftX:drift)));
   const y=Math.max(-maxShift*.6,Math.min(maxShift*.6,this.look[1]+(this.blinking?this.blinkDriftY:driftY)));
   const transform='translate(50 57) rotate('+(-this.formAngle).toFixed(2)+') scale('+scale.toFixed(3)+') translate(-50 -57) translate('+x.toFixed(2)+' '+y.toFixed(2)+')';
   this.eyesGroup.setAttribute('transform',transform);this.glyphHost.setAttribute('transform',transform)
  }
  frame(now){
   if(this.dead||!this.visible){this.lastFrame=now;return}
   if(this.bodyPose){const p=this.bodyPose,y=p.pivotY||52;this.body.setAttribute('transform','translate(50 '+y+') translate('+(p.dx||0).toFixed(3)+' '+p.dy.toFixed(3)+') rotate('+p.angle.toFixed(3)+') scale('+p.sx.toFixed(4)+' '+p.sy.toFixed(4)+') translate(-50 -'+y+')')}
   if(this.symbolPose)this.paintSymbolPose(this.symbolPose);
   if(this.cometPose)this.paintCometPose(this.cometPose);
   this.smearActive=!!(this.cometSmear||this.stemSmear);if(this.splitPose){const p=this.splitPose;for(const [dot,v,dir] of [[this.dotLeft,p.left,-1],[this.dotRight,p.right,1]]){const cx=50+dir*18*v,r=4*Math.max(0,v);dot.setAttribute('cx',cx.toFixed(3));dot.setAttribute('r',r.toFixed(3));const smear=v>.43&&v<.6;dot.setAttribute('transform',smear?'translate('+cx.toFixed(3)+' 52) scale(1.18 .8475) translate('+(-cx).toFixed(3)+' -52)':'');this.smearActive ||=smear}}
   const dt=this.lastFrame?Math.min(50,now-this.lastFrame)/1000:0;this.lastFrame=now;
   this.updateGaze(dt,reduce.matches||forcedReduce);
   if(reduce.matches||forcedReduce)return;
   const sleep=this.signal==='sleeping',busy=this.signal==='busy',dots=busy&&this.busyMode==='dots';
   const target=2*Math.PI/(sleep?6.5:busy?2.5:3.6);
   this.breathAccel+=(18*(target-this.breathSpeed)-8*this.breathAccel)*dt;
   this.breathSpeed+=this.breathAccel*dt;this.breathPhase+=this.breathSpeed*dt;const scanTarget=this.signal==='busy'&&this.busyMode==='ball'?1:0;this.scanBlend+=(scanTarget-this.scanBlend)*Math.min(1,dt*8);const driftTarget=this.signal==='idle'?1:0;this.idleDriftBlend+=(driftTarget-this.idleDriftBlend)*Math.min(1,dt*8);
   if(this.phase!=='settled'||dots||(this.signal==='asking'||this.signal==='failed')&&this.stemProgress>0)return;
   const base=sleep?.58:1,amplitude=sleep?.008:busy?.024:.02;
   const targetScale=base*(1+amplitude*(.5-.5*Math.cos(this.breathPhase))),motionMatrix=this.motion.transform.baseVal.consolidate()?.matrix,currentScale=motionMatrix?Math.hypot(motionMatrix.a,motionMatrix.b):1,scale=currentScale+(targetScale-currentScale)*Math.min(1,dt*8);
   this.motion.setAttribute('transform','translate(50 52) scale('+scale.toFixed(4)+') translate(-50 -52)');
   this.faceRadius=39;this.faceScale=1;this.formAngle=0;
   this.updateFaceTransform();if(this.signal==='idle'){this.scheduleGlance();this.scheduleExpression();this.scheduleGesture()}
  }
  setBangTransform(angle){this.bangRig.setAttribute('transform','rotate('+angle.toFixed(3)+' 50 82)')}
  setDotTransform(x,y){this.dotX=x;this.dotY=y;this.dotRig.setAttribute('transform','translate(50 82) scale('+x.toFixed(4)+' '+y.toFixed(4)+') translate(-50 -82)')}
  setSleepScale(value){this.sleepScale=value;this.motion.setAttribute('transform','translate(50 52) scale('+value.toFixed(3)+') translate(-50 -52)')}
  morphPath(to,duration=MOTION.stem.duration){const from=this.currentPath;if(from===to)return Promise.resolve();this.motionSegments.body++;const c=this.track(animate(from,to,{duration,ease:smooth,onUpdate:v=>this.setPath(v)}));this.morphControl=c;this.morphing=true;return c.finished.catch(()=>{}).finally(()=>{if(this.morphControl===c){this.morphing=false;this.motionSegments.body++}})}
  morph(name,duration=MOTION.stem.duration){return this.morphPath(this.paths[name],duration)}
  fade(node,to,duration=MOTION.exit.duration){const from=Number(node.getAttribute('opacity')||0);const c=this.track(animate(from,to,{duration,ease:smoother,onUpdate:v=>node.setAttribute('opacity',v.toFixed(3))}));return c.finished.catch(()=>{})}
  followPointer(x,y,type='mouse'){
   if(this.dead||!this.visible||document.hidden||reduce.matches||forcedReduce)return;
   const r=this.host.getBoundingClientRect(),cx=r.left+r.width/2,cy=r.top+r.height/2;
   if(Math.min(r.width,r.height)<40&&Math.hypot(x-cx,y-cy)>200){this.releasePointer();return}
   if(!this.cursorActive){this.cursorActive=true;clearTimeout(this.glanceTimer);this.glanceTimer=null;this.gazeControl?.stop?.();this.transition('gaze',type==='touch'?'lookAround':'cursor',type==='touch'?'TAP':'POINTER');this.motionSegments.gaze++}
   const dx=x-cx,dy=y-cy,maxX=MOTION.gaze.maxX,maxY=MOTION.gaze.maxY;
   this.gazeTarget=[maxX*Math.tanh(dx/Math.max(22,r.width*.62)),maxY*Math.tanh(dy/Math.max(22,r.height*.62))];
  }
  releasePointer(){
   if(!this.cursorActive)return;this.cursorActive=false;
   clearTimeout(this.glanceTimer);this.glanceTimer=null;
   if(this.signal==='idle'&&this.visible&&!document.hidden)this.scheduleGlance(180+hash(this.seed+'resume'+this.glanceCount)%280);
  }
  lookTo(x,y,duration=.15,mode='lookAround'){
   if(this.dead||document.hidden||!this.visible)return;
   if(this.blinking){this.pendingLook=[x,y,duration,mode];return}
   this.gazeControl?.stop?.();this.gazeControl=null;
   this.gazeTarget=[Math.max(-MOTION.gaze.maxX,Math.min(MOTION.gaze.maxX,x)),Math.max(-MOTION.gaze.maxY,Math.min(MOTION.gaze.maxY,y))];
   this.transition('gaze',mode,mode,()=>!this.dead);this.motionSegments.gaze++;
   this.gazeUntil=performance.now()+duration*1000+30;
   if(reduce.matches||forcedReduce){
    const from=this.look.slice(),to=this.gazeTarget.slice();
    this.gazeControl=this.track(animate(0,1,{duration:Math.max(.45,duration),ease:smoother,onUpdate:t=>{
     this.look=[from[0]+(to[0]-from[0])*t,from[1]+(to[1]-from[1])*t];this.rightLook=this.look.slice();
     this.eyes[1].removeAttribute('transform');this.updateFaceTransform();
     for(let i=0;i<2;i++){const {cx,cy}=this.eyeCenters[i],tilt=Math.max(-8,Math.min(8,this.look[0]*.7));this.tiltGroups[i].setAttribute('transform','rotate('+tilt.toFixed(3)+' '+cx.toFixed(3)+' '+cy.toFixed(3)+')')}
    }}));
   }
  }
  scanTo(x,y){
   this.gazeControl?.stop?.();const from=this.gazeTarget.slice();
   this.transition('gaze','lookAround','SCAN');this.motionSegments.gaze++;
   this.gazeUntil=performance.now()+MOTION.gaze.scanDuration*1000+30;
   this.gazeControl=this.track(animate(0,1,{duration:MOTION.gaze.scanDuration,ease:smoother,onUpdate:t=>{this.gazeTarget=[from[0]+(x-from[0])*t,from[1]+(y-from[1])*t]}}));
  }
  updateGaze(dt,reduced=false){
   if(!dt)return;
   const spring=reduced?MOTION.gaze.reduced:this.cursorActive?MOTION.gaze.cursor:this.chart.gaze==='saccade'?MOTION.gaze.saccade:MOTION.gaze.idle;
   const {stiffness,damping}=spring;
   for(let i=0;i<2;i++){
    this.gazeVelocity[i]+=(stiffness*(this.gazeTarget[i]-this.look[i])-damping*this.gazeVelocity[i])*dt;
    this.look[i]+=this.gazeVelocity[i]*dt;
    this.rightLookVelocity[i]+=(stiffness*.83*(this.look[i]-this.rightLook[i])-damping*1.03*this.rightLookVelocity[i])*dt;
    this.rightLook[i]+=this.rightLookVelocity[i]*dt;
   }
   const bodyTarget=this.signal==='idle'&&!reduced?[this.look[0]*.14,this.look[1]*.13,this.look[0]*.14]:[0,0,0];
   for(let i=0;i<3;i++){this.bodyFollowVelocity[i]+=(MOTION.gaze.body.stiffness*(bodyTarget[i]-this.bodyFollow[i])-MOTION.gaze.body.damping*this.bodyFollowVelocity[i])*dt;this.bodyFollow[i]+=this.bodyFollowVelocity[i]*dt}
   this.gazeFollow.setAttribute('transform','translate('+this.bodyFollow[0].toFixed(3)+' '+this.bodyFollow[1].toFixed(3)+') rotate('+this.bodyFollow[2].toFixed(3)+' 50 52)');
   this.eyes[0].removeAttribute('transform');this.eyes[1].setAttribute('transform','translate('+(this.rightLook[0]-this.look[0]).toFixed(3)+' '+(this.rightLook[1]-this.look[1]).toFixed(3)+')');
   for(let i=0;i<2;i++){const {cx,cy}=this.eyeCenters[i],offset=i?this.rightLook[0]:this.look[0],tilt=Math.max(-14,Math.min(14,offset*.91))*(i?.97:1);this.tiltGroups[i].setAttribute('transform','rotate('+tilt.toFixed(3)+' '+cx.toFixed(3)+' '+cy.toFixed(3)+')')}
   this.updateFaceTransform();
  }
  setEyes(mood,expression,instant=false){
   if(this.blinking&&!instant){this.pendingEyes=[mood,expression];return}
   const eyeExpression={happy:'happy',surprised:'curious',sleeping:'sleeping',focused:'working'}[expression];
   if(eyeExpression){mood=eyeExpression;expression=null}
   this.expression=expression;
   this.motionSegments.eyes++;
   this.transition('eyes',expression?'hidden':mood==='curious'?'round':mood==='tall'||mood==='excited'?'tall':mood==='wide'?'wide':mood==='happy'?'happy':mood==='sleeping'?'sleepy':'default',mood,()=>!this.dead);
   this.svg.dataset.expression=expression||'';
   for(const control of this.eyeControls||[])control.stop?.();
   this.eyeControls=[];
   const target=eyesFor(mood,this.appearance.eyes||this.traits.eyes,this.traits);
   const shapeX=mood==='wide'?1.6:mood==='tall'||mood==='excited'?1.05:1;
   const shapeY=mood==='wide'?.6:mood==='tall'||mood==='excited'?1.35:1;
   const shapeDuration=mood==='wide'?.4:mood==='sleeping'?.48:mood==='tall'||mood==='excited'?.36:.3;
   if(!instant)this.eyeMorphUntil=performance.now()+shapeDuration*1000+30;
   for(let i=0;i<2;i++){
    const eye=this.eyes[i],geo=eyeGeometry(target[i]),profile=eyeProfile(mood,geo,i),to=eyeOutlineFor(mood,geo,i);
    const blinkGroup=this.blinkGroups[i],targetBlinkScale=mood==='sleeping'?.45:1;
    const fromBlinkScale=blinkGroup.transform.baseVal.consolidate()?.matrix.d||1;
    const applyBlinkScale=v=>blinkGroup.setAttribute('transform','translate('+geo.cx.toFixed(3)+' '+geo.cy.toFixed(3)+') scale(1 '+v.toFixed(4)+') translate('+(-geo.cx).toFixed(3)+' '+(-geo.cy).toFixed(3)+')');
    if(instant)applyBlinkScale(targetBlinkScale);
    else this.eyeControls.push(this.track(animate(fromBlinkScale,targetBlinkScale,{duration:mood==='sleeping'?.48:.3,ease:smoother,onUpdate:applyBlinkScale})));
    const shapeGroup=this.shapeGroups[i],matrix=shapeGroup.transform.baseVal.consolidate()?.matrix;
    const fromX=matrix?Math.hypot(matrix.a,matrix.b):1,fromY=matrix?Math.hypot(matrix.c,matrix.d):1;
    const applyShape=(x,y)=>shapeGroup.setAttribute('transform','translate('+geo.cx.toFixed(3)+' '+geo.cy.toFixed(3)+') scale('+x.toFixed(4)+' '+y.toFixed(4)+') translate('+(-geo.cx).toFixed(3)+' '+(-geo.cy).toFixed(3)+')');
    if(instant)applyShape(shapeX,shapeY);
    else this.eyeControls.push(this.track(animate(0,1,{duration:shapeDuration+i*.018,ease:'linear',onUpdate:t=>{const u=mood==='tall'||mood==='excited'?smoother(t)+.14*Math.sin(Math.PI*t)*t*t:smoother(t);applyShape(fromX+(shapeX-fromX)*u,fromY+(shapeY-fromY)*u)}})));
    this.lids[i].setAttribute('d',eyeOutline(geo.cx,geo.cy,Math.max(8.5,profile.w*shapeX),2.4,.9));
    if(instant){eye.setAttribute('d',to);eye.setAttribute('opacity','1');this.lids[i].setAttribute('opacity','0')}
    else{
     this.eyeControls.push(this.track(animate(eye.getAttribute('d'),to,{duration:shapeDuration,ease:smooth,onUpdate:v=>eye.setAttribute('d',v)})));
     this.eyeControls.push(this.track(animate(Number(eye.getAttribute('opacity')??1),1,{duration:.18,ease:smoother,onUpdate:v=>eye.setAttribute('opacity',v.toFixed(3))})));
     this.eyeControls.push(this.track(animate(Number(this.lids[i].getAttribute('opacity')??0),0,{duration:.18,ease:smoother,onUpdate:v=>this.lids[i].setAttribute('opacity',v.toFixed(3))})));
    }
   }
   const newMarkup=glyphMarkup(expression);
   if(instant)this.glyphHost.setAttribute('opacity','1');else this.fade(this.glyphHost,1,.22);
   if(instant){this.glyphHost.innerHTML=newMarkup;this.eyesGroup.setAttribute('opacity',expression?'0':'1');return}
   const old=this.glyphHost.firstElementChild;
   if(newMarkup){const wrap=document.createElementNS('http://www.w3.org/2000/svg','g');wrap.innerHTML=newMarkup;wrap.setAttribute('opacity','0');this.glyphHost.append(wrap);this.fade(wrap,1,.22).then(()=>{if(!this.dead)old?.remove()})}
   else if(old)this.fade(old,0,.22).then(()=>old.remove());
   this.fade(this.eyesGroup,expression?0:1,.22)
  }
  paintBlink(progress,index){
   const p=this.blinkParams?.[index];if(!p)return;
   const scale=1-(1-p.closedScale)*progress;
   this.blinkGroups[index].setAttribute('transform','translate('+p.cx.toFixed(3)+' '+p.cy.toFixed(3)+') scale(1 '+scale.toFixed(4)+') translate('+(-p.cx).toFixed(3)+' '+(-p.cy).toFixed(3)+')');const lid=progress;this.eyes[index].setAttribute('opacity',(1-lid).toFixed(3));this.lids[index].setAttribute('opacity',lid.toFixed(3));
  }
  blinkEye(index,generation,offset=0,hold=.04){
   const p=this.blinkParams[index],variation=index===0?1:1.04;
   return new Promise(resolve=>this.timer(()=>{
    if(generation!==this.blinkGeneration||this.dead)return resolve();
    const close=this.track(animate(0,1,{duration:.09*variation,ease:t=>Math.pow(t,1.5),onUpdate:v=>{if(generation===this.blinkGeneration)this.paintBlink(v,index)}}));
    close.finished.then(()=>this.timer(()=>{
     if(generation!==this.blinkGeneration||this.dead)return resolve();
     const open=this.track(animate(1,0,{duration:.15/variation,ease:t=>1-(1-t)*(1-t),onUpdate:v=>{if(generation===this.blinkGeneration)this.paintBlink(v,index)}}));
     open.finished.then(()=>{if(generation===this.blinkGeneration){this.paintBlink(0,index)}resolve()}).catch(resolve);
    },hold*1000)).catch(resolve);
   },offset*1000));
  }
  resetBlink(){this.blinkGeneration++;this.blinking=false;this.blinkQueued=false;this.blinkScale=1;this.blinkParams=null;for(const eye of this.eyes){eye.style.removeProperty('transition')}this.pendingEyes=null;this.pendingLook=null}
  transitionMotion(kind='pop'){
   this.motionSegments.body++;if(quiet()||this.dead)return;
   const m=this.body.transform.baseVal.consolidate()?.matrix;
   const pivotY=kind==='bang'||kind==='failed'?82:52;
   const from={sx:m?Math.hypot(m.a,m.b):1,sy:m?Math.hypot(m.c,m.d):1,angle:m?Math.atan2(m.b,m.a)*180/Math.PI:0,dx:m?m.a*50+m.c*pivotY+m.e-50:0,dy:m?m.b*50+m.d*pivotY+m.f-pivotY:0};
   this.transitionControl?.stop?.();
   const soft=kind==='sleeping',wake=kind==='waking';
   const pose=this.bodyPose={...from,pivotY};
   const anticipation=wake?.15:MOTION.anticipation.duration;
   const sequence=soft?
    [[pose,{sx:.94,sy:.88,angle:0,dx:0,dy:3},{duration:.34,ease:MOTION.snap.ease}]]:
    [[pose,{sx:wake?1.035:1.06,sy:wake?.966:.943,angle:kind==='failed'?-3:0,dx:0,dy:wake?2:0},{duration:anticipation,ease:[.55,0,1,1]}],
     [pose,{sx:1.12,sy:1/1.12,angle:kind==='failed'?4:0,dx:0,dy:kind==='bang'||kind==='failed'?0:-2},{at:anticipation,duration:MOTION.snap.duration,ease:MOTION.snap.ease}],
     [pose,{sx:1,sy:1,angle:0,dx:0,dy:0},{at:anticipation+MOTION.snap.duration,...MOTION.settle}]];
   this.transitioning=true;this.transitionControl=this.track(animate(sequence));
   const control=this.transitionControl;
   if(!soft){this.timer(()=>{if(this.transitionControl===control)this.motionSegments.body++},anticipation*1000);this.timer(()=>{if(this.transitionControl===control)this.motionSegments.body++},(anticipation+MOTION.snap.duration)*1000)}
   control.finished.then(()=>{if(this.transitionControl===control){this.transitioning=false;this.motionSegments.body++;this.bodyPose=null;if(!soft)this.body.removeAttribute('transform')}}).catch(()=>{});
  }
  tint(mood){return mood==='sleeping'?'#9EA7BB':mood==='angry'?'#FF6F59':this.traits.color}
  setColor(mood,instant=false){this.motionSegments.color++;const to=this.tint(mood),from=this.shapePath.getAttribute('fill'),apply=v=>{this.shapePath.setAttribute('fill',v);this.dotLeft.setAttribute('fill',v);this.dotRight.setAttribute('fill',v);this.bangStem.setAttribute('fill',v)};if(instant)apply(to);else this.track(animate(from,to,{duration:MOTION.eye.duration,ease:smoother,onUpdate:apply}))}
  setNoticeScale(value){this.noticeScale=value;this.notice.setAttribute('transform','translate(79 22) scale('+value.toFixed(4)+') translate(-79 -22)')}
  setStem(value,detach){this.stemProgress=value;if(detach===undefined){const u=Math.min(1,Math.max(0,value/.4));detach=u*u*(3-2*u)}this.stemDetach=Math.min(1,Math.max(0,detach));this.bangStem.setAttribute('d',stemPath(value,this.stemDetach));this.stemSmear=!!(this.stemAnimating&&value>.52&&value<.68);this.bangStem.setAttribute('transform',this.stemSmear?'translate(50 82) scale(.85 1.1765) translate(-50 -82)':'')}
  paintSymbolPose(pose){if(pose.d!==this.currentPath)this.setPath(pose.d);if(pose.stem!==this.stemProgress||pose.detach!==this.stemDetach)this.setStem(pose.stem,pose.detach);if(pose.angle!==this.bangAngle){this.bangAngle=pose.angle;this.setBangTransform(pose.angle)}if(pose.dotX!==this.dotX||pose.dotY!==this.dotY)this.setDotTransform(pose.dotX,pose.dotY)}
  paintCometPose(pose){
   if(pose.d!==this.currentPath)this.setPath(pose.d);
   const t=Math.max(0,Math.min(1,pose.travel)),arc=Math.sin(Math.PI*t),x=74*arc,y=-11*arc-2*Math.sin(2*Math.PI*t);
   this.cometSmear=t>.17&&t<.25||t>.75&&t<.83;
   const stretch=this.cometSmear?1.18:1;
   this.motion.setAttribute('transform','translate('+x.toFixed(2)+' '+y.toFixed(2)+') translate(50 52) scale('+stretch.toFixed(3)+' '+(1/stretch).toFixed(3)+') translate(-50 -52)');
  }
  animateStem(to,duration=MOTION.stem.duration){this.motionSegments.symbol++;const from=this.stemProgress;const control=this.track(animate(from,to,{duration,ease:smooth,onUpdate:v=>this.setStem(v)}));this.stemControl=control;this.stemAnimating=true;return control.finished.catch(()=>{}).finally(()=>{if(this.stemControl===control){this.stemAnimating=false;this.motionSegments.symbol++}})}
  async buildBang(token,signal){
   const failed=signal==='failed',rest=failed?-8:0,impact=failed?-18:12,spring=failed?MOTION.bang.heavy:MOTION.bang.spring;
   const pose=this.symbolPose={d:this.currentPath,stem:this.stemProgress,detach:this.stemDetach,angle:this.bangAngle,dotX:this.dotX,dotY:this.dotY};
   this.stemAnimating=true;this.motionSegments.body++;this.motionSegments.symbol++;
   const build=this.track(animate([
    [pose,{d:this.paths.bangDot},{duration:.32,ease:smooth}],
    [pose,{stem:[pose.stem,failed?1.12:1.15]},{at:.2,duration:failed?.35:.28,ease:MOTION.snap.ease}],
    [pose,{detach:[pose.detach,1]},{at:.2,duration:failed?.14:.112,ease:smooth}],
    [pose,{stem:[failed?1.12:1.15,1]},{at:failed?.55:.48,...spring}],
    [pose,{angle:[pose.angle,impact]},{at:failed?.5:.42,duration:.07,ease:smooth}],
    [pose,{angle:[impact,rest]},{at:failed?.57:.49,...spring}],
    [pose,{dotX:[pose.dotX,1.15],dotY:[pose.dotY,.85]},{at:failed?.54:.46,duration:.07,ease:smooth}],
    [pose,{dotX:[1.15,1],dotY:[.85,1]},{at:failed?.61:.53,...spring}]
   ]));
   this.timer(()=>{if(this.symbolPose===pose){this.motionSegments.symbol++;this.bangWiggleActive=true}},420);
   await build.finished.catch(()=>{});if(token!==this.token||this.dead)return false;
   this.paintSymbolPose(pose);this.symbolPose=null;this.stemAnimating=false;this.bangWiggleActive=false;
   this.motionSegments.body++;this.motionSegments.symbol++;this.bangSignal=signal;this.phase='settled';
   this.scheduleBangBob(token,signal);return true
  }
  scheduleBangBob(token,signal){
   this.timer(()=>{if(this.dead||token!==this.token||this.signal!==signal||this.phase!=='settled')return;void this.bobBang(token,signal)},MOTION.bang.bobInterval)
  }
  async bobBang(token,signal){
   const rest=signal==='failed'?-8:0,spring=signal==='failed'?MOTION.bang.heavy:MOTION.bang.spring;
   const pose=this.symbolPose={d:this.currentPath,stem:this.stemProgress,detach:this.stemDetach,angle:this.bangAngle,dotX:this.dotX,dotY:this.dotY};
   this.bangWiggleActive=true;this.motionSegments.symbol++;
   const bob=this.track(animate([
    [pose,{stem:[pose.stem,1.045]},{duration:.1,ease:smooth}],
    [pose,{angle:[pose.angle,rest+(signal==='failed'?-3:4)]},{at:.05,duration:.09,ease:smooth}],
    [pose,{dotX:[pose.dotX,1.05],dotY:[pose.dotY,.96]},{at:.1,duration:.06,ease:smooth}],
    [pose,{stem:[1.045,1]},{at:.1,...spring}],
    [pose,{angle:[rest+(signal==='failed'?-3:4),rest]},{at:.14,...spring}],
    [pose,{dotX:[1.05,1],dotY:[.96,1]},{at:.16,...spring}]
   ]));
   await bob.finished.catch(()=>{});if(this.dead||token!==this.token||this.signal!==signal)return;
   this.paintSymbolPose(pose);this.symbolPose=null;this.bangWiggleActive=false;this.motionSegments.symbol++;
   this.bangBobCount=(this.bangBobCount||0)+1;this.scheduleBangBob(token,signal)
  }
  async retractBang(token,nextSignal){
   const pose=this.symbolPose={d:this.currentPath,stem:this.stemProgress,detach:this.stemDetach,angle:this.bangAngle,dotX:this.dotX,dotY:this.dotY};
   this.bangWiggleActive=true;this.stemAnimating=false;this.motionSegments.body++;this.motionSegments.symbol++;
   const retract=this.track(animate([
    [pose,{stem:[pose.stem,Math.max(this.stemProgress,1)*1.05]},{duration:.08,ease:[.55,0,1,1]}],
    [pose,{detach:[pose.detach,0]},{at:.08,duration:.06,ease:smooth}],
    [pose,{stem:[Math.max(this.stemProgress,1)*1.05,0],angle:[pose.angle,0]},{at:.14,duration:.2,ease:MOTION.exit.ease}],
    [pose,{dotX:[pose.dotX,1.15],dotY:[pose.dotY,.85]},{at:.32,duration:.06,ease:smooth}],
    [pose,{dotX:[1.15,1],dotY:[.85,1]},{at:.38,...MOTION.bang.spring}],
    [pose,{d:this.paths.home},{at:.34,duration:MOTION.stem.duration,ease:smooth}]
   ]));
   this.timer(()=>{if(token===this.token)this.transitionMotion(nextSignal==='sleeping'?'sleeping':nextSignal==='starting'?'waking':nextSignal==='failed'?'failed':nextSignal==='asking'?'bang':'pop')},340);
   await retract.finished.catch(()=>{});if(this.dead||token!==this.token)return false;
   this.paintSymbolPose(pose);this.symbolPose=null;this.bangWiggleActive=false;
   this.motionSegments.body++;this.motionSegments.symbol++;return true
  }
  resetDecor(){this.splitPose=null;this.smearActive=false;this.dotLeft.removeAttribute('transform');this.dotRight.removeAttribute('transform');this.dots.setAttribute('opacity','0');this.dotLeft.setAttribute('cx','50');this.dotRight.setAttribute('cx','50');this.dotLeft.setAttribute('r','0');this.dotRight.setAttribute('r','0');this.setStem(0);this.setBangTransform(0);this.setDotTransform(1,1);this.bangSignal=null;this.bangWiggleActive=false;this.notice.setAttribute('opacity','0');this.setNoticeScale(0);this.sleepScale=1;this.formAngle=0;this.bangAngle=0;this.stemProgress=0;this.stemDetach=0;this.faceScale=1;if(quiet())this.motion.removeAttribute('transform')}
  async leaveDecor(token){this.motionSegments.dots++;this.motionSegments.symbol++;const tasks=[];if(Number(this.dots.getAttribute('opacity'))>0){const centerFrom=this.shapePath.getBBox().width/2;this.dotCenterRadius=centerFrom;tasks.push(this.track(animate(0,1,{duration:.3,ease:smooth,onUpdate:t=>{this.dotCenterRadius=centerFrom+(5-centerFrom)*t+.55*Math.sin(Math.PI*t);this.setPath(pointsPath(circlePoints(this.dotCenterRadius)));this.dotLeft.setAttribute('cx',(50-18*(1-t)).toFixed(2));this.dotRight.setAttribute('cx',(50+18*(1-t)).toFixed(2));this.dotLeft.setAttribute('r',(4*(1-t)).toFixed(2));this.dotRight.setAttribute('r',(4*(1-t)).toFixed(2))}})).finished.catch(()=>{}));tasks.push(this.fade(this.dots,0,.3))}if(Math.abs(this.bangAngle)>.01)tasks.push(this.track(animate(this.bangAngle,0,{duration:.38,ease:'easeInOut',onUpdate:v=>{this.bangAngle=v;this.setBangTransform(v)}})).finished.catch(()=>{}));if(this.stemProgress>0)tasks.push(this.animateStem(0,.38));if(Number(this.notice.getAttribute('opacity'))>0){tasks.push(this.fade(this.notice,0,.2));tasks.push(this.track(animate(this.noticeScale,0,{duration:.2,ease:'easeInOut',onUpdate:v=>this.setNoticeScale(v)})).finished.catch(()=>{}))}await Promise.all(tasks);if(this.dead||token!==this.token)return false;this.resetDecor();this.lookTo(0,0,.3);return true}
  async toHome(token){await this.morphPath(this.paths.home,.5);if(this.dead||token!==this.token)return false;return true}
  async dotsMode(token){
   this.motionSegments.dots++;this.transition('body','busy.dots','BUSY_MODE');
   this.fade(this.eyesGroup,0,MOTION.exit.duration);this.fade(this.glyphHost,0,MOTION.exit.duration);
   this.fade(this.dots,1,MOTION.exit.duration);
   const pose=this.splitPose={left:Number(this.dotLeft.getAttribute('r')||0)/4,right:Number(this.dotRight.getAttribute('r')||0)/4};
   const split=this.track(animate([
    [pose,{left:1},{...MOTION.pop}],
    [pose,{right:1},{at:MOTION.split.stagger,...MOTION.pop}]
   ]));
   await Promise.all([this.morph('dot',MOTION.split.duration),split.finished.catch(()=>{})]);
   if(this.dead||token!==this.token)return;
   this.splitPose=null;this.smearActive=false;this.dotLeft.removeAttribute('transform');this.dotRight.removeAttribute('transform');
   this.formAngle=0;this.dotCenterRadius=5;
   const pulseStart=performance.now();
   this.track(animate(0,1,{duration:.9,repeat:Infinity,ease:'linear',onUpdate:t=>{
    const elapsed=Math.min(1,(performance.now()-pulseStart)/300),ramp=elapsed*elapsed*(3-2*elapsed),wave=phase=>ramp*Math.max(0,Math.sin(2*Math.PI*(t-phase)));
    const left=wave(0),middle=wave(1/3),right=wave(2/3);
    this.dotLeft.setAttribute('r',(4+1.4*left).toFixed(2));this.dotRight.setAttribute('r',(4+1.4*right).toFixed(2));
    this.dotLeft.setAttribute('opacity',(1-.38*ramp+.38*left).toFixed(2));this.dotRight.setAttribute('opacity',(1-.38*ramp+.38*right).toFixed(2));
    this.dotCenterRadius=5+1.75*middle;this.setPath(pointsPath(circlePoints(this.dotCenterRadius)))
   }}));
   this.phase='settled';this.scheduleBusy()
  }
  async ballMode(token){this.transition('body','busy.scan','BUSY_MODE');if(!await this.toHome(token))return;this.setEyes('idle',null);this.lastFrame=0;this.phase='settled';this.scheduleBusy()}
  scheduleBusy(){if(this.dead||quiet()||!this.visible||this.signal!=='busy'||this.busyTimer)return;this.busyStartedAt=Date.now();this.busyTimer=setTimeout(()=>{this.busyTimer=null;this.busyRemaining=this.busyInterval;this.busyMode=this.busyMode==='dots'?'ball':'dots';this.startSignal()},this.busyRemaining)}
  pauseBusy(){if(this.busyTimer){clearTimeout(this.busyTimer);this.busyTimer=null;this.busyRemaining=Math.max(100,this.busyRemaining-(Date.now()-this.busyStartedAt))}}
  async startSignal(){const token=++this.token;this.stopControls();this.resetBlink();this.clearTimers();this.motionSegments.eyes++;this.phase='turning';const signal=this.signal,leavingBang=this.stemProgress>0,reducedMotion=(reduce.matches||forcedReduce)&&!document.hidden&&this.visible;if(!reducedMotion)this.setColor(this.state,quiet());if(reducedMotion){const from=Number(this.svg.getAttribute('opacity')??1);await this.track(animate(from,0,{duration:.15,ease:smooth,onUpdate:v=>this.svg.setAttribute('opacity',v.toFixed(3))})).finished.catch(()=>{});if(token!==this.token)return;this.setColor(this.state,true);this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,signal==='sleeping'?null:this.expression,true);if(signal==='sleeping')this.setSleepScale(.58);this.phase='settled';await this.track(animate(0,1,{duration:.18,ease:smooth,onUpdate:v=>this.svg.setAttribute('opacity',v.toFixed(3))})).finished.catch(()=>{});return}const motionMatrix=this.motion.transform.baseVal.consolidate()?.matrix,liveMotionScale=motionMatrix?Math.hypot(motionMatrix.a,motionMatrix.b):1,wakingFromSleep=liveMotionScale<.98&&signal!=='sleeping',wakeFromScale=liveMotionScale;if(quiet()||!this.visible){this.needsResume=true;this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,signal==='sleeping'?null:this.expression,true);if(quiet()&&signal==='sleeping')this.setSleepScale(.58);this.phase='settled';return}this.needsResume=false;if(wakingFromSleep){this.resetDecor();this.setSleepScale(wakeFromScale);if(signal!=='starting'){const grow=this.track(animate(wakeFromScale,1,{duration:.45,ease:'easeOut',onUpdate:v=>this.setSleepScale(v)}));await grow.finished.catch(()=>{});if(token!==this.token)return;this.sleepScale=1;if(quiet())this.motion.removeAttribute('transform')}}else if(this.stemProgress>0&&signal!==this.bangSignal){if(!await this.retractBang(token,signal))return;this.resetDecor()}else if(signal==='busy'&&this.busyMode==='dots'&&Number(this.dots.getAttribute('opacity'))===0&&this.stemProgress===0){this.fade(this.notice,0,.25)}else if((signal==='asking'||signal==='failed')&&this.stemProgress===0&&Number(this.dots.getAttribute('opacity'))===0&&Number(this.notice.getAttribute('opacity'))===0){this.lookTo(0,0,.3)}else if(!await this.leaveDecor(token))return;
   if(signal==='busy'){if(this.busyMode==='dots'){await this.track(animate(0,1,{duration:MOTION.anticipation.duration,ease:smoother})).finished.catch(()=>{});if(token!==this.token)return;await this.dotsMode(token)}else await this.ballMode(token);return}
   if(signal==='sleeping'){this.fade(this.eyesGroup,0,.2);this.fade(this.glyphHost,0,.2);await this.morph('dot',MOTION.stem.duration);if(token!==this.token)return;const grow=this.track(animate(liveMotionScale,.58,{duration:MOTION.stem.duration,ease:smoother,onUpdate:v=>this.setSleepScale(v)}));await Promise.all([this.morphPath(this.paths.home,MOTION.stem.duration),grow.finished.catch(()=>{})]);if(token!==this.token)return;this.setEyes('sleeping',null);this.lastFrame=0;this.phase='settled';return}
   if(signal==='asking'||signal==='failed'){
    this.fade(this.eyesGroup,0,MOTION.exit.duration);this.fade(this.glyphHost,0,MOTION.exit.duration);
    await this.buildBang(token,signal);return
   }
   if(signal==='starting'){this.fade(this.eyesGroup,0,.18);this.fade(this.glyphHost,0,.18);if(!wakingFromSleep){await this.morph('dot',.25);if(token!==this.token)return}const grow=wakingFromSleep?this.track(animate(wakeFromScale,1,{duration:MOTION.wake.duration,ease:smoother,onUpdate:v=>this.setSleepScale(v)})).finished.catch(()=>{}):Promise.resolve();await Promise.all([this.morphPath(this.paths.home,MOTION.wake.duration),grow]);if(token!==this.token)return;this.sleepScale=1;this.motion.removeAttribute('transform');this.setEyes('tall',null);this.lastFrame=0;this.phase='settled';this.timer(()=>{if(!this.dead&&this.signal==='starting')this.setEyes('idle',null)},800);return}
   const content=this.wideOnArrival&&signal==='idle';this.wideOnArrival=false;this.timer(()=>{if(!this.dead&&token===this.token)this.setEyes(signal==='unread'?'tall':content?'wide':'idle',null)},45);await this.morphPath(this.paths.home,MOTION.stem.duration);if(token!==this.token)return;if(signal==='unread'||content)this.timer(()=>{if(!this.dead&&this.signal===signal)this.setEyes('idle',null)},850);if(signal==='unread')this.track(animate(this.noticeScale,1,{duration:.28,ease:'easeOut',onUpdate:v=>this.setNoticeScale(v*(1+.12*Math.sin(Math.PI*v)))}));else this.track(animate(this.noticeScale,0,{duration:.25,ease:'easeInOut',onUpdate:v=>this.setNoticeScale(v)}));this.fade(this.notice,signal==='unread'?1:0,.25);this.lastFrame=0;this.phase='settled';
  }
  async comet(){
   if(this.dead||quiet()||!this.visible||this.cometActive)return false;
   this.cometActive=true;this.transition('body','sending','COMET');
   const token=++this.token;this.stopControls();this.transitionMotion('comet');this.clearTimers();this.phase='turning';
   if(!await this.leaveDecor(token))return false;
   this.fade(this.eyesGroup,0,MOTION.exit.duration);this.fade(this.glyphHost,0,MOTION.exit.duration);
   const pose=this.cometPose={d:this.currentPath,travel:0};this.motionSegments.body++;
   const timeline=this.track(animate([
    [pose,{d:this.paths.dot},{duration:.25,ease:smooth}],
    [pose,{travel:1},{at:.2,duration:MOTION.comet.duration,ease:smoother}],
    [pose,{d:this.paths.home},{at:.58,duration:MOTION.stem.duration,ease:smooth}]
   ]));
   this.timer(()=>{if(this.cometPose===pose)this.motionSegments.body++},200);
   this.timer(()=>{if(this.cometPose===pose)this.motionSegments.body++},580);
   await timeline.finished.catch(()=>{});if(this.dead||token!==this.token)return false;
   this.paintCometPose(pose);this.cometPose=null;this.motionSegments.body++;
   this.motion.removeAttribute('transform');this.cometSmear=false;
   this.cometActive=false;const pending=this.pendingPose;this.pendingPose=null;
   if(pending)this.jump(...pending);else{void this.startSignal();this.transitionMotion('pop')}
   return true
  }
  send(evt){const next=MACHINE[this.state]?.[evt];return next?this.jump(next,MOOD_GLYPH[next]||null,null,evt):false}
  showExpression(expression){return this.jump(this.state,expression,this.signal)}
  jump(mood,expression=MOOD_GLYPH[mood]||null,signal=null,event=null){if(this.dead||!MOODS.includes(mood))return false;const next=signal||({working:'busy',waiting:'asking',happy:'unread',angry:'failed',sleeping:'sleeping',waking:'starting'}[mood]||'idle');if(this.cometActive){this.pendingPose=[mood,expression,next,event];return true}if(this.state===mood&&this.signal===next&&this.phase==='settled')return false;const old=this.signal,flowing=!quiet()&&this.phase==='settled'&&((old==='idle'&&next==='busy'&&this.busyMode==='ball')||(old==='busy'&&this.busyMode==='ball'&&next==='idle'));this.wideOnArrival=old==='busy'&&next==='idle';this.state=mood;this.expression=expression;this.signal=next;this.transition('body',next==='busy'?(this.busyMode==='dots'?'busy.dots':'busy.scan'):next==='sleeping'?'hibernated':next==='starting'?'waking':next,event||signal||mood,()=>!this.dead);this.svg.dataset.mood=mood;this.svg.dataset.signal=next;this.svg.setAttribute('aria-label',mood+' mascot');this.busyRemaining=this.busyInterval;if(flowing){this.resetBlink();this.updateFaceTransform();clearTimeout(this.busyTimer);this.busyTimer=null;this.setColor(mood);this.setEyes(this.wideOnArrival?'wide':'idle',null);if(this.wideOnArrival){this.wideOnArrival=false;this.timer(()=>{if(!this.dead&&this.signal==='idle')this.setEyes('idle',null)},850)}if(next==='busy')this.scheduleBusy()}else void this.startSignal();if(!((old==='asking'||old==='failed')&&this.stemProgress>0&&next!==old))this.transitionMotion(next==='sleeping'?'sleeping':next==='starting'?'waking':next==='failed'?'failed':next==='asking'?'bang':'pop');this.onMood?.(mood,this);return true}
  updateVisibility(){if(this.dead)return;syncMorphClock();if(gesturePauseCount&&!quiet()){for(const c of this.controls)c.pause?.();return}if(quiet()||!this.visible){this.pauseBusy();for(const c of this.controls)c.pause?.();clearTimeout(this.blinkTimer);this.blinkTimer=null;clearTimeout(this.expressionTimer);this.expressionTimer=null;clearTimeout(this.gestureTimer);this.gestureTimer=null;if(!this.visible||document.hidden){this.cursorActive=false;clearTimeout(this.glanceTimer);this.glanceTimer=null}if(quiet()){this.needsResume=true;this.stopControls();this.resetBlink();this.clearTimers();this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,this.signal==='sleeping'?null:this.expression,true);if(this.signal==='sleeping')this.setSleepScale(.58);this.phase='settled';if(reduce.matches||forcedReduce){this.bodyFollow=[0,0,0];this.bodyFollowVelocity=[0,0,0];this.gazeFollow.removeAttribute('transform')}}}else{if(this.needsResume){void this.startSignal();return}for(const c of this.controls)c.play?.();if(this.signal==='busy'&&this.phase==='settled')this.scheduleBusy();this.scheduleBlink();this.scheduleGlance();if(pointer.active)this.followPointer(pointer.x,pointer.y,pointer.type)}if((reduce.matches||forcedReduce)&&!document.hidden&&this.visible)this.scheduleGlance()}
  blink(allowDouble=true,wink=false,second=false){
   if(this.dead||quiet()||!this.visible||this.phase!=='settled'||this.signal!=='idle'&&!['tall','wide'].includes(this.chart.eyes))return false;
   if(this.blinking||this.eyeMomentActive&&!['tall','wide'].includes(this.chart.eyes)||performance.now()<this.eyeMorphUntil||performance.now()<this.gazeUntil){
    if(!this.blinkQueued){this.blinkQueued=true;this.timer(()=>{this.blinkQueued=false;this.blink(allowDouble,wink)},180)}return false
   }
   this.blinking=true;this.motionSegments.eyes++;this.blinkReturnState=this.chart.eyes;this.transition('eyes',wink?'wink':second?'doubleBlink':'blink','BLINK',()=>this.signal==='idle'||['tall','wide'].includes(this.blinkReturnState));const generation=++this.blinkGeneration;
   this.blinkDriftX=.8*Math.sin(this.breathPhase*.8+this.traits.phase);
   this.blinkDriftY=.45*Math.sin(this.breathPhase*.62+this.traits.phase);
   this.blinkParams=this.eyeCenters.map((g,i)=>({cx:g.cx,cy:g.cy,closedScale:i===0?.08:.084}));
   for(const eye of this.eyes)eye.style.setProperty('transition','none');
   const jobs=wink?[this.blinkEye(1,generation,0,.25)]:[this.blinkEye(0,generation),this.blinkEye(1,generation,.025,.043)];
   Promise.all(jobs).then(()=>{if(generation!==this.blinkGeneration||this.dead)return;this.blinking=false;this.transition('eyes',this.blinkReturnState||'default','BLINK_DONE');for(let i=0;i<2;i++){this.paintBlink(0,i);this.eyes[i].style.removeProperty('transition')}this.blinkParams=null;
    const eyes=this.pendingEyes,look=this.pendingLook;this.pendingEyes=null;this.pendingLook=null;if(eyes)this.setEyes(...eyes);if(look)this.lookTo(...look);
    if(allowDouble&&!wink&&hash(this.seed+Date.now())%5===0)this.timer(()=>this.blink(false,false,true),250)
   }).catch(()=>{});return true
  }
  scheduleBlink(){if(this.blinkTimer||this.dead||quiet()||!this.visible)return;const delay=this.blinkedYet?2800+hash(this.seed+Date.now())%700:1400+hash(this.seed)%1300;this.blinkTimer=setTimeout(()=>{this.blinkTimer=null;if(!quiet()&&this.visible&&this.phase==='settled'&&this.signal==='idle'){this.blinkedYet=true;this.blink()}this.scheduleBlink()},delay)}
  scheduleGlance(delay){
   if(this.glanceTimer||this.dead||document.hidden||!this.visible||this.signal!=='idle'||this.cursorActive)return;
   const n=hash(this.seed+'gaze'+this.glanceCount++),wait=delay??1500+n%2501;
   this.glanceTimer=setTimeout(()=>{
    this.glanceTimer=null;
    if(this.dead||document.hidden||!this.visible||this.signal!=='idle')return;
    if(this.cursorActive||this.phase!=='settled'){this.scheduleGlance(320);return}
    const reduced=reduce.matches||forcedReduce,amp=(reduced?6.6:MOTION.gaze.maxX)*(.82+((n>>>9)%18)/100),up=reduced?-3:-5.8,down=reduced?3.1:7.4;
    const choices=[[-amp,0],[amp,0],[-amp,up],[amp,up],[0,down],[0,0]];
    const hold=600+(n>>>4)%1401,choice=choices[(n>>>15)%choices.length],scan=!reduced&&this.glanceCount%5===0;
    if(scan){
     this.lookTo(-amp,up*.3,.15,'saccade');
     this.timer(()=>{if(!this.dead&&this.signal==='idle'&&!this.cursorActive)this.scanTo(amp,up*.3)},170);
     this.timer(()=>{if(!this.dead&&this.signal==='idle'&&!this.cursorActive)this.lookTo(0,0,.3,'ahead')},1070+hold);
     this.scheduleGlance(Math.min(4000,1700+hold));
    }else{
     this.lookTo(choice[0],choice[1],reduced?.45:.14,reduced?'lookAround':'saccade');
     if(choice[0]||choice[1])this.timer(()=>{if(!this.dead&&this.signal==='idle'&&!this.cursorActive)this.lookTo(0,0,reduced?.6:.28,'ahead')},hold);
     this.scheduleGlance(Math.min(4000,Math.max(1500,hold+650+(n>>>21)%900)));
    }
   },wait)
  }
  scheduleExpression(){if(this.expressionTimer||this.dead||quiet()||!this.visible||this.signal!=='idle')return;const delay=6000+hash(this.seed+'expression'+this.expressionCount++)%6001;this.expressionTimer=setTimeout(()=>{this.expressionTimer=null;if(!this.dead&&!quiet()&&this.visible&&this.phase==='settled'&&this.signal==='idle'){const choices=['happy','wink','curious','tall','wide'];const choice=choices[hash(this.seed+'moment'+this.expressionCount)%choices.length];this.eyeMomentActive=true;if(choice==='wink'){this.eyeMomentActive=false;this.blink(false,true);this.eyeMomentActive=true}else this.setEyes(choice,null);this.timer(()=>{if(!this.dead&&this.signal==='idle'){this.setEyes('idle',null);this.eyeMomentActive=false;this.scheduleExpression()}},850+hash(this.seed+'hold'+this.expressionCount)%450)}else this.scheduleExpression()},delay)}
  scheduleGesture(){
   if(this.gestureTimer||this.dead||quiet()||!this.visible||this.signal!=='idle')return;
   const delay=MOTION.float.gestureMin+hash(this.seed+'gesture'+(this.gestureCount=(this.gestureCount||0)+1))%(MOTION.float.gestureMax-MOTION.float.gestureMin);
   this.gestureTimer=setTimeout(()=>{this.gestureTimer=null;if(this.signal==='idle'&&this.phase==='settled'&&!this.transitioning){
    const m=this.body.transform.baseVal.consolidate()?.matrix,pose=this.bodyPose={sx:m?Math.hypot(m.a,m.b):1,sy:m?Math.hypot(m.c,m.d):1,angle:0,dy:0};
    this.transitioning=true;const control=this.transitionControl=this.track(animate([
     [pose,{sx:1.05,sy:1/1.05,dy:1},{duration:MOTION.anticipation.duration,ease:smoother}],
     [pose,{sx:.97,sy:1/.97,dy:-3},{at:MOTION.anticipation.duration,duration:.14,ease:MOTION.snap.ease}],
     [pose,{sx:1,sy:1,dy:0},{at:MOTION.anticipation.duration+.14,...MOTION.settle}]
    ]));control.finished.then(()=>{if(this.transitionControl===control){this.transitioning=false;this.bodyPose=null;this.body.removeAttribute('transform')}}).catch(()=>{})
   }this.scheduleGesture()},delay)
  }
  render(instant){if(instant){this.stopControls();this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,this.signal==='sleeping'?null:this.expression,true);if(this.signal==='sleeping')this.setSleepScale(.58)}}
  destroy(){if(this.dead)return;this.dead=true;this.token++;this.stopControls();this.clearTimers();clearTimeout(this.blinkTimer);clearTimeout(this.glanceTimer);clearTimeout(this.expressionTimer);clearTimeout(this.gestureTimer);this.blinkTimer=this.glanceTimer=this.expressionTimer=this.gestureTimer=null;observer?.unobserve(this.svg);active.delete(this);syncMorphClock();this.svg.remove()}
 }
 global.VBoxMascot={Mascot,svg,miniSVG:(seed,mood='idle',expression=MOOD_GLYPH[mood]||null)=>svg(seed,mood,true,{},expression),setReducedMotion:value=>{forcedReduce=!!value;if(forcedReduce)releasePointer();for(const mascot of active)mascot.updateVisibility()},setGesturePaused,traits,MACHINE,shapes:['circle'],colors:COLORS,moods:MOODS};
})(window);
