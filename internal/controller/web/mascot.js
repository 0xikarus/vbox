/* vbox mascot system: seeded silhouettes, one animation clock, no dependencies. */
(function(global){
 'use strict';
 const SHAPES=['circle','drop','cloud','square','gem','triangle','pill','bean'];
 const COLORS=['#965FF0','#378EF5','#24C77A','#23C5BB','#FF8A38','#F5BC29','#F253B1','#F25564','#BE67E8','#60C989','#FF6F59','#1B2A41'];
 const MOODS=['idle','working','waiting','happy','laughing','angry','sleeping','waking'];
 const MACHINE={idle:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry',SLEEP:'sleeping',WAKE:'waking'},working:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},waiting:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},happy:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle',SLEEP:'sleeping'},laughing:{SETTLE:'happy',WORK:'working',ERROR:'angry',SLEEP:'sleeping'},angry:{CALM:'idle',PRAISE:'happy',WORK:'working',SLEEP:'sleeping'},sleeping:{WAKE:'waking',WORK:'waking'},waking:{READY:'idle',WORK:'working',SLEEP:'sleeping'}};
 const hash=s=>{let h=2166136261;for(const c of String(s)){h^=c.charCodeAt(0);h=Math.imul(h,16777619)}return h>>>0};
 const traits=seed=>{const h=hash(seed);return {shape:SHAPES[h%SHAPES.length],color:h%37===0?COLORS[11]:COLORS[Math.floor(h/8)%11],wide:1.02+((h>>>12)%9)/100,tall:.91+((h>>>17)%9)/100,tilt:((h>>>22)%9-4)*.35,phase:(h%1000)/1000*6.283,blink:2.3+(h%190)/100,eyes:'A'}};
 const PATHS={
 circle:'M50 11 C71 10 87 26 88 48 C90 72 76 89 50 90 C24 89 10 72 12 48 C13 26 29 10 50 11 Z',
 drop:'M45 14 Q50 7 55 14 C69 29 83 46 85 62 C87 79 73 90 50 91 C27 90 13 79 15 62 C17 46 31 29 45 14 Z',
 cloud:'M17 56 C13 46 20 36 31 36 C30 24 43 19 52 27 C62 20 76 26 76 37 C87 39 91 50 84 60 C87 78 71 90 50 91 C28 90 12 78 17 60 Z',
 square:'M34 11 C20 11 12 20 12 34 L12 65 C12 82 22 90 37 90 L63 90 C78 90 88 82 88 65 L88 34 C88 20 80 11 66 11 Z',
 gem:'M45 12 Q50 9 55 12 L77 25 Q87 31 87 42 L87 63 Q87 75 76 82 L62 89 Q50 94 38 89 L24 82 Q13 75 13 63 L13 42 Q13 31 23 25 Z',
 triangle:'M38 29 Q50 14 62 29 C72 41 83 54 86 68 Q90 86 74 89 Q50 93 26 89 Q10 86 14 68 C17 54 29 37 38 29 Z',
 pill:'M35 24 L65 24 C80 24 89 36 89 51 C89 67 80 79 65 79 L35 79 C20 79 11 67 11 51 C11 36 20 24 35 24 Z',
 bean:'M28 18 C42 11 54 24 68 22 C83 21 91 39 89 57 C87 79 72 90 51 91 C29 90 13 78 11 57 C9 40 17 24 28 18 Z'};
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const eyePath=p=>`M${p[0]} ${p[1]} C${p[2]} ${p[3]} ${p[4]} ${p[5]} ${p[6]} ${p[7]}`;
 const rawEyesFor=(mood,variant='A')=>{const spread=variant==='C'?1:0,L=36-spread,R=64+spread,lo=57;const line=(cx,side=1)=>[cx-2.5,lo-6*side,cx-1.2,lo-3*side,cx+1.2,lo+3*side,cx+2.5,lo+6*side];switch(mood){
  case 'working':return [[L-5,lo-1,L-2,lo-3,L+2,lo-2,L+5,lo],[R-5,lo,R-2,lo-2,R+2,lo-3,R+5,lo-1]];
  case 'waiting':return [[L-4,lo-8,L-2,lo-12,L+2,lo-12,L+4,lo-8],[R-4,lo-9,R-2,lo-13,R+2,lo-12,R+4,lo-8]];
  case 'happy':return [[L-6,lo+3,L-3,lo-12,L+3,lo-12,L+6,lo+3],[R-6,lo+3,R-3,lo-12,R+3,lo-12,R+6,lo+3]];
  case 'laughing':return [[L-6,lo-7,L+3,lo-2,L+3,lo+1,L-6,lo+7],[R+6,lo-7,R-3,lo-2,R-3,lo+1,R+6,lo+7]];
  case 'angry':return [[L-6,lo-8,L-3,lo-5,L+3,lo+1,L+6,lo+4],[R-6,lo+4,R-3,lo+1,R+3,lo-5,R+6,lo-8]];
  case 'sleeping':return [[L-6,lo-1,L-3,lo+7,L+3,lo+7,L+6,lo-1],[R-6,lo-1,R-3,lo+7,R+3,lo+7,R+6,lo-1]];
  case 'waking':return [line(L,1),[R-5,lo,R-2,lo+3,R+2,lo+3,R+5,lo]];
  default:return variant==='C'?[[L,49,L-1,53,L+1,61,L,65],[R,49,R-1,53,R+1,61,R,65]]:[line(L,1),line(R,-1)];}};
 const eyesFor=(mood,variant='A')=>rawEyesFor(mood,variant).map((p,j)=>p.map((v,i)=>i%2===0?(j?64:36)+(v-(j?64:36))*.6:57+(v-57)*(variant==='C'?.65:.52)));
 const MOOD_GLYPH={working:'focused',waiting:'waiting',happy:'happy',laughing:'laughing',angry:'error',sleeping:'sleeping',waking:'surprised'};
 const EXTRA_EVENT={DONE:['happy','sparkle'],PRAISE:['happy','heart'],THANKS:['happy','heart'],APPROVAL:['waiting','attention'],NEEDS_ATTENTION:['waiting','attention'],CRASH:['angry','error'],FAILED:['angry','sad'],RETRY:['waiting','nervous'],RESTART:['waking','dizzy'],CLEAR_CONTEXT:['waking','dizzy'],NEW_CHAT:['waking','surprised'],USAGE:['idle','cost']};
 const glyphOne=(kind,side)=>{switch(kind){
  case 'calm':return '<path d="M-6 0 L6 0"/>';
  case 'happy':return '<path d="M-7 5 Q0 -7 7 5"/>';
  case 'laughing':return side===0?'<path d="M-6 -6 L5 0 L-6 6"/>':'<path d="M6 -6 L-5 0 L6 6"/>';
  case 'sparkle':return '<path d="M0 -8 L0 8 M-8 0 L8 0 M-5 -5 L5 5 M5 -5 L-5 5"/>';
  case 'surprised':return '<circle cx="0" cy="0" r="5.5"/>';
  case 'error':return '<path d="M-6 -6 L6 6 M6 -6 L-6 6"/>';
  case 'sleeping':return '<path d="M-6 -5 L6 -5 L-6 5 L6 5"/>';
  case 'waiting':return '<path d="M-5 -4 Q-4 -9 1 -8 Q8 -7 5 -1 Q3 1 0 2 M0 8 L0 8.2"/>';
  case 'attention':return '<path d="M0 -8 L0 3 M0 8 L0 8.2"/>';
  case 'dizzy':return '<path d="M5 5 C1 8 -6 5 -6 -1 C-6 -7 2 -9 6 -4 C10 1 3 5 -1 2 C-4 0 -2 -4 1 -3 C4 -2 2 1 0 0"/>';
  case 'heart':return '<path d="M0 7 C-13 -1 -7 -10 0 -4 C7 -10 13 -1 0 7 Z"/>';
  case 'sad':return '<path d="M-6 -7 L6 -7 M0 -7 L0 7"/>';
  case 'nervous':return '<path d="M-2 -7 L-2 -6 M1 0 L1 1 Q3 5 -1 8"/>';
  case 'focused':return '<circle cx="0" cy="0" r="2.3" fill="white" stroke="none"/>';
  case 'cost':return '<path d="M5 -6 Q0 -10 -5 -5 Q-8 -1 -1 0 Q8 1 5 6 Q1 10 -5 6 M0 -9 L0 9"/>';
  default:return ''}};
 const glyphMarkup=kind=>kind?`<g class="vbox-mascot-glyphs" stroke="#fff" stroke-width="4.4" stroke-linecap="round" stroke-linejoin="round" fill="none"><g transform="translate(36 57)">${glyphOne(kind,0)}</g><g transform="translate(64 57)">${glyphOne(kind,1)}</g></g>`:'';
 const POINTS=64;
 const circlePoints=(radius=36)=>Array.from({length:POINTS},(_,i)=>{const a=-Math.PI/2+i*2*Math.PI/POINTS;return [50+radius*Math.cos(a),52+radius*Math.sin(a)]});
 const samplePath=path=>{const len=path.getTotalLength(),pts=Array.from({length:POINTS},(_,i)=>{const p=path.getPointAtLength(len*i/POINTS);return[p.x,p.y]});let best=0,score=Infinity;for(let i=0;i<POINTS;i++){const d=(pts[i][0]-50)**2+(pts[i][1]-12)**2;if(d<score){score=d;best=i}}return pts.map((_,i)=>pts[(i+best)%POINTS])};
 const lerpPoints=(a,b,t)=>a.map((p,i)=>[p[0]+(b[i][0]-p[0])*t,p[1]+(b[i][1]-p[1])*t]);
 const pointsPath=pts=>{let d=`M${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;for(let i=0;i<pts.length;i++){const p0=pts[(i-1+pts.length)%pts.length],p1=pts[i],p2=pts[(i+1)%pts.length],p3=pts[(i+2)%pts.length];d+=` C${(p1[0]+(p2[0]-p0[0])/6).toFixed(2)} ${(p1[1]+(p2[1]-p0[1])/6).toFixed(2)} ${(p2[0]-(p3[0]-p1[0])/6).toFixed(2)} ${(p2[1]-(p3[1]-p1[1])/6).toFixed(2)} ${p2[0].toFixed(2)} ${p2[1].toFixed(2)}`}return d+' Z'};
 function svg(seed,mood='idle',mini=false,appearance={},expression=MOOD_GLYPH[mood]||null){const t=traits(seed),p=PATHS[t.shape],variant=appearance.eyes||t.eyes,eye=eyesFor(mood,variant),gid='vb-mascot-hi-'+hash(seed);return `<svg class="vbox-mascot vbox-mascot-eyes-${variant}${mini?' vbox-mascot-mini':''}" viewBox="0 0 100 100" width="100%" height="100%" preserveAspectRatio="xMidYMid meet" data-mood="${mood}" data-expression="${expression||''}" data-seed="${esc(seed)}" role="img" aria-label="${mood} mascot" ><defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#fff" stop-opacity=".16"/><stop offset=".52" stop-color="#fff" stop-opacity=".03"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient></defs><g class="vbox-mascot-motion"><g class="vbox-mascot-body" transform="translate(50 60) rotate(${t.tilt.toFixed(1)}) scale(${(t.wide*(appearance.chub||1)).toFixed(2)} ${t.tall.toFixed(2)}) translate(-50 -60)"><path class="vbox-mascot-shape" d="${p}" fill="${mood==='angry'?'#FF6F59':mood==='sleeping'?'#a9a8bf':t.color}"/><path class="vbox-mascot-highlight" d="${p}" fill="url(#${gid})" pointer-events="none"/><g class="vbox-mascot-blush" opacity="0"><ellipse cx="27" cy="66" rx="7" ry="3.2"/><ellipse cx="73" cy="66" rx="7" ry="3.2"/></g><g class="vbox-mascot-eyes" opacity="${expression?0:1}"><path class="vbox-mascot-eye left" d="${eyePath(eye[0])}"/><path class="vbox-mascot-eye right" d="${eyePath(eye[1])}"/><circle class="vbox-mascot-glint" cx="34" cy="53" r="1.3"/><circle class="vbox-mascot-glint" cx="62" cy="53" r="1.3"/></g><g class="vbox-mascot-glyph-host">${glyphMarkup(expression)}</g></g><text class="vbox-mascot-z" x="78" y="25">z</text></g></svg>`}
 const POSES={};for(const mood of MOODS)POSES[mood]=eyesFor(mood,'soft');
 const hexRgb=hex=>[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16));
 const mix=(a,b,t)=>`rgb(${a.map((v,i)=>Math.round(v+(b[i]-v)*t)).join(' ')})`;
 const ease=t=>1-Math.pow(1-t,3);
 const overshoot=t=>1+2.15*Math.pow(t-1,3)+1.15*Math.pow(t-1,2);
 const animate=global.Motion?.animate;
 if(!animate)throw Error('motion.js must load before mascot.js');
 const active=new Set();let forcedReduce=false;
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
 document.addEventListener('visibilitychange',()=>{for(const mascot of active)mascot.updateVisibility()});
 reduce.addEventListener?.('change',()=>{for(const mascot of active)mascot.updateVisibility()});
 class Mascot{
  constructor(host,seed,{onMood,appearance={}}={}){
   this.host=host;this.seed=String(seed);this.traits=traits(seed);this.appearance=appearance;this.onMood=onMood||null;
   this.state='idle';this.expression=null;this.phase='turning';this.visible=true;this.dead=false;this.token=0;this.controls=new Set();this.timers=[];this.idleControl=null;
   host.innerHTML=svg(seed,'idle',false,appearance);
   this.svg=host.querySelector('svg');this.svg.__vboxMascot=this;this.motion=this.svg.querySelector('.vbox-mascot-motion');this.shapePath=this.svg.querySelector('.vbox-mascot-shape');this.highlight=this.svg.querySelector('.vbox-mascot-highlight');this.eyesGroup=this.svg.querySelector('.vbox-mascot-eyes');this.eyes=[...this.svg.querySelectorAll('.vbox-mascot-eye')];this.glyphHost=this.svg.querySelector('.vbox-mascot-glyph-host');this.blush=this.svg.querySelector('.vbox-mascot-blush');if(appearance.blush==='always')this.blush.setAttribute('opacity','.36');this.z=this.svg.querySelector('.vbox-mascot-z');
   this.shapeD=pointsPath(samplePath(this.shapePath));this.ballD=pointsPath(circlePoints());this.smallBallD=pointsPath(circlePoints(8));this.setPath(this.smallBallD);this.eyesGroup.setAttribute('opacity','0');
   this.enter=()=>{if(!quiet()&&this.visible)this.springMotion(1.04,.94,0)};this.down=()=>{if(!quiet()&&this.visible)this.springMotion(.9,1.08,2)};
   host.addEventListener('pointerenter',this.enter);host.addEventListener('pointerdown',this.down);observer?.observe(this.svg);active.add(this);
   if(quiet()){this.finishTransition();this.eyesGroup.setAttribute('opacity','1')}else{void this.morphTo('idle',null,true);this.scheduleBlink();this.scheduleGlance()}
  }
  track(control){this.controls.add(control);control.finished?.finally(()=>this.controls.delete(control)).catch(()=>{});return control}
  stopControls(){this.cancelWait?.();this.cancelWait=null;for(const control of this.controls)control.stop?.();this.controls.clear()}
  wait(control){return Promise.race([control.finished?.catch(()=>{})||Promise.resolve(),new Promise(resolve=>{this.cancelWait=resolve})])}
  clearTimer(){for(const timer of this.timers)clearTimeout(timer);this.timers=[]}
  setPath(d){this.shapePath.setAttribute('d',d);this.highlight.setAttribute('d',d)}
  tint(mood){if(mood==='angry')return '#FF6F59';if(mood==='sleeping')return '#a9a8bf';if(mood==='working')return '#'+this.traits.color.slice(1).match(/../g).map(x=>Math.min(255,parseInt(x,16)+8).toString(16).padStart(2,'0')).join('');return this.traits.color}
  finishTransition(){if(this.dead)return;this.token++;this.stopControls();if(this.pending&&!this.pending.initial)this.applyMood(this.pending.mood,this.pending.expression,true);this.pending=null;this.phase='settled';this.setPath(this.shapeD);this.eyesGroup.setAttribute('opacity',this.expression?'0':'1');this.glyphHost.setAttribute('opacity','1');if(!quiet()&&this.visible)this.startIdle()}
  updateVisibility(){if(this.dead)return;if(quiet()||!this.visible){if(this.phase==='turning')this.finishTransition();this.idleControl?.pause?.()}else{if(this.idleControl)this.idleControl.play?.();else if(this.phase==='settled')this.startIdle();this.scheduleBlink();this.scheduleGlance()}}
  send(evt){const extra=EXTRA_EVENT[evt];if(extra)return this.jump(extra[0],extra[1]);const next=MACHINE[this.state]?.[evt];return next?this.jump(next):false}
  showExpression(expression){return this.jump(this.state,expression)}
  jump(mood,expression=MOOD_GLYPH[mood]||null){if(this.dead||!MOODS.includes(mood)||mood===this.state&&expression===this.expression&&this.phase==='settled')return false;this.clearTimer();if(quiet()||!this.visible){this.finishTransition();this.applyMood(mood,expression,true);return true}void this.morphTo(mood,expression,false);return true}
  async morphTo(mood,expression,initial){const token=++this.token;this.stopControls();this.idleControl?.stop?.();this.idleControl=null;this.phase='turning';this.pending={mood,expression,initial};const from=this.shapePath.getAttribute('d');
   const into=this.track(animate(from,this.ballD,{duration:.2,ease:'easeIn',onUpdate:value=>this.setPath(value)}));await this.wait(into);if(this.dead||token!==this.token)return;this.setPath(this.ballD);
   if(initial){this.eyesGroup.setAttribute('opacity','1')}else this.applyMood(mood,expression,false);
   await new Promise(resolve=>{const timer=setTimeout(resolve,140);this.timers.push(timer)});if(this.dead||token!==this.token)return;
   this.springMotion(mood==='happy'?.92:1.06,mood==='happy'?1.08:.92,mood==='happy'?-8:2);
   const out=this.track(animate(this.ballD,this.shapeD,{type:'spring',duration:.3,bounce:.27,onUpdate:value=>this.setPath(value)}));await this.wait(out);if(this.dead||token!==this.token)return;
   this.pending=null;this.phase='settled';this.setPath(this.shapeD);this.startIdle();this.scheduleBlink();this.scheduleGlance()}
  applyMood(mood,expression,instant){this.state=mood;this.expression=expression;this.svg.dataset.mood=mood;this.svg.dataset.expression=expression||'';this.svg.setAttribute('aria-label',(expression||mood)+' mascot');
   const poses=eyesFor(mood,this.appearance.eyes||this.traits.eyes);for(let i=0;i<2;i++){const path=this.eyes[i],target=eyePath(poses[i]);if(instant)path.setAttribute('d',target);else this.track(animate(path.getAttribute('d'),target,{duration:.28,ease:'easeOut',onUpdate:value=>path.setAttribute('d',value)}))}
   this.glyphHost.innerHTML=glyphMarkup(expression);this.eyesGroup.setAttribute('opacity',expression?'0':'1');this.glyphHost.setAttribute('opacity','1');
   const targetColor=this.tint(mood),fromColor=this.shapePath.getAttribute('fill');if(instant)this.shapePath.setAttribute('fill',targetColor);else this.track(animate(fromColor,targetColor,{duration:.32,ease:'easeInOut',onUpdate:value=>this.shapePath.setAttribute('fill',value)}));
   const blushOpacity=this.appearance.blush===false||this.appearance.blush!=='always'&&!['happy','laughing'].includes(mood)?0:.36,zOpacity=mood==='sleeping'?.75:0;if(instant){this.blush.setAttribute('opacity',String(blushOpacity));this.z.setAttribute('opacity',String(zOpacity))}else{this.track(animate(this.blush,{opacity:blushOpacity},{duration:.22}));this.track(animate(this.z,{opacity:zOpacity},{duration:.28}))}this.onMood?.(mood,this);
   if(mood==='waking'&&!instant)this.timers.push(setTimeout(()=>{if(this.state==='waking')this.track(animate(this.eyes[1].getAttribute('d'),eyePath(eyesFor('idle',this.appearance.eyes||this.traits.eyes)[1]),{duration:.22,onUpdate:value=>this.eyes[1].setAttribute('d',value)}))},300));
   if(['happy','laughing','angry','waking'].includes(mood))this.timers.push(setTimeout(()=>this.jump(mood==='waking'?'idle':mood==='laughing'?'happy':'idle'),mood==='waking'?1700:3400))}
  springMotion(scaleX,scaleY,y){const up=this.track(animate(this.motion,{scaleX,scaleY,y},{duration:.1,ease:'easeOut'}));up.finished?.then(()=>{if(!this.dead&&!quiet())this.track(animate(this.motion,{scaleX:1,scaleY:1,y:0,rotate:0},{type:'spring',stiffness:380,damping:18}))}).catch(()=>{})}
  startIdle(){if(this.dead||quiet()||!this.visible||this.phase!=='settled')return;this.idleControl?.stop?.();const duration=this.state==='sleeping'?4.8:this.state==='working'?1.25:2.8,range=this.state==='sleeping'?.025:.04,bob=this.state==='sleeping'?.8:this.state==='working'?1.3:2.4;this.idleControl=animate(this.motion,{scaleY:[1,1+range,1],scaleX:[1,1-range*.5,1],y:[0,-bob,0]},{duration,repeat:Infinity,ease:'easeInOut',delay:-(this.traits.phase/6.283)*duration})}
  scheduleBlink(){if(this.blinkTimer||this.dead)return;const ms=2200+hash(this.seed+Date.now())%4200;this.blinkTimer=setTimeout(()=>{this.blinkTimer=null;if(!quiet()&&this.visible&&this.phase==='settled'&&!this.expression)this.blinkOnce();this.scheduleBlink()},ms)}
  blinkOnce(){const poses=eyesFor(this.state,this.appearance.eyes||this.traits.eyes);for(let i=0;i<2;i++){const eye=this.eyes[i],original=eye.getAttribute('d'),closed=eyePath(poses[i].map((v,k)=>k%2?57+(v-57)*.08:v));const close=this.track(animate(original,closed,{duration:.065,onUpdate:value=>eye.setAttribute('d',value)}));close.finished?.then(()=>this.track(animate(closed,original,{duration:.13,ease:'easeOut',onUpdate:value=>eye.setAttribute('d',value)}))).catch(()=>{})}if(hash(this.seed+Date.now())%5===0)this.timers.push(setTimeout(()=>{if(!this.dead&&!this.expression)this.blinkOnce()},270))}
  scheduleGlance(){if(this.glanceTimer||this.dead)return;this.glanceTimer=setTimeout(()=>{this.glanceTimer=null;if(!quiet()&&this.visible&&this.phase==='settled'&&!this.expression){const amount=(hash(this.seed+Date.now())%3-1)*1.8;const glance=this.track(animate(this.eyesGroup,{x:amount},{duration:.25}));glance.finished?.then(()=>this.track(animate(this.eyesGroup,{x:0},{duration:.35}))).catch(()=>{})}this.scheduleGlance()},1800+hash(this.seed+Date.now())%2300)}
  render(instant){if(instant)this.finishTransition()}
  destroy(){if(this.dead)return;this.dead=true;this.token++;this.stopControls();this.idleControl?.stop?.();this.clearTimer();clearTimeout(this.blinkTimer);clearTimeout(this.glanceTimer);this.host.removeEventListener('pointerenter',this.enter);this.host.removeEventListener('pointerdown',this.down);observer?.unobserve(this.svg);active.delete(this);this.svg.remove()}
 }
 global.VBoxMascot={Mascot,svg,miniSVG:(seed,mood='idle',expression=MOOD_GLYPH[mood]||null)=>svg(seed,mood,true,{},expression),setReducedMotion:value=>{forcedReduce=!!value;for(const mascot of active)mascot.updateVisibility()},traits,MACHINE,shapes:SHAPES,colors:COLORS,moods:MOODS};
})(window);
