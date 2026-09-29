/* vbox mascot system: seeded silhouettes, one animation clock, no dependencies. */
(function(global){
 'use strict';
 const SHAPES=['circle','drop','cloud','square','gem','triangle','pill','bean'];
 const COLORS=['#965FF0','#378EF5','#24C77A','#23C5BB','#FF8A38','#F5BC29','#F253B1','#F25564','#BE67E8','#60C989','#FF6F59','#1B2A41'];
 const MOODS=['idle','working','waiting','happy','laughing','angry','sleeping','waking'];
 const MACHINE={idle:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry',SLEEP:'sleeping',WAKE:'waking'},working:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},waiting:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},happy:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle',SLEEP:'sleeping'},laughing:{SETTLE:'happy',WORK:'working',ERROR:'angry',SLEEP:'sleeping'},angry:{CALM:'idle',PRAISE:'happy',WORK:'working',SLEEP:'sleeping'},sleeping:{WAKE:'waking',WORK:'waking'},waking:{READY:'idle',WORK:'working',SLEEP:'sleeping'}};
 const hash=s=>{let h=2166136261;for(const c of String(s)){h^=c.charCodeAt(0);h=Math.imul(h,16777619)}return h>>>0};
 const traits=seed=>{const h=hash(seed);return {shape:SHAPES[h%SHAPES.length],color:COLORS[Math.floor(h/8)%COLORS.length],wide:1.02+((h>>>12)%9)/100,tall:.91+((h>>>17)%9)/100,tilt:((h>>>22)%9-4)*.35,phase:(h%1000)/1000*6.283,blink:2.3+(h%190)/100,eyes:['A','B','C'][Math.floor(h/64)%3]}};
 const PATHS={
 circle:'M50 11 C71 10 87 26 88 48 C90 72 76 89 50 90 C24 89 10 72 12 48 C13 26 29 10 50 11 Z',
 drop:'M45 14 Q50 7 55 14 C69 29 83 46 85 62 C87 79 73 90 50 91 C27 90 13 79 15 62 C17 46 31 29 45 14 Z',
 cloud:'M16 55 C11 44 19 34 31 35 C31 23 44 19 53 27 C64 19 77 26 76 37 C87 39 92 49 85 60 C88 77 73 90 50 91 C27 90 12 77 16 60 Z',
 square:'M34 11 C20 11 12 20 12 34 L12 65 C12 82 22 90 37 90 L63 90 C78 90 88 82 88 65 L88 34 C88 20 80 11 66 11 Z',
 gem:'M45 12 Q50 9 55 12 L77 25 Q87 31 87 42 L87 63 Q87 75 76 82 L62 89 Q50 94 38 89 L24 82 Q13 75 13 63 L13 42 Q13 31 23 25 Z',
 triangle:'M38 29 Q50 14 62 29 C72 41 83 54 86 68 Q90 86 74 89 Q50 93 26 89 Q10 86 14 68 C17 54 29 37 40 24 Z',
 pill:'M35 24 L65 24 C80 24 89 36 89 51 C89 67 80 79 65 79 L35 79 C20 79 11 67 11 51 C11 36 20 24 35 24 Z',
 bean:'M28 18 C42 11 54 24 68 22 C83 21 91 39 89 57 C87 79 72 90 51 91 C29 90 13 78 11 57 C9 40 17 24 28 18 Z'};
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const eyePath=p=>`M${p[0]} ${p[1]} C${p[2]} ${p[3]} ${p[4]} ${p[5]} ${p[6]} ${p[7]}`;
 const rawEyesFor=(mood,variant='A')=>{const spread=variant==='C'?1:0,L=36-spread,R=64+spread,lo=57;const line=(cx,side=1)=>[cx-4,lo-6*side,cx-2,lo-3*side,cx+2,lo+3*side,cx+4,lo+6*side];switch(mood){
  case 'working':return [[L-5,lo-1,L-2,lo-3,L+2,lo-2,L+5,lo],[R-5,lo,R-2,lo-2,R+2,lo-3,R+5,lo-1]];
  case 'waiting':return [[L-4,lo-8,L-2,lo-12,L+2,lo-12,L+4,lo-8],[R-4,lo-9,R-2,lo-13,R+2,lo-12,R+4,lo-8]];
  case 'happy':return [[L-6,lo+3,L-3,lo-12,L+3,lo-12,L+6,lo+3],[R-6,lo+3,R-3,lo-12,R+3,lo-12,R+6,lo+3]];
  case 'laughing':return [[L-6,lo-7,L+3,lo-2,L+3,lo+1,L-6,lo+7],[R+6,lo-7,R-3,lo-2,R-3,lo+1,R+6,lo+7]];
  case 'angry':return [[L-6,lo-8,L-3,lo-5,L+3,lo+1,L+6,lo+4],[R-6,lo+4,R-3,lo+1,R+3,lo-5,R+6,lo-8]];
  case 'sleeping':return [[L-6,lo-1,L-3,lo+7,L+3,lo+7,L+6,lo-1],[R-6,lo-1,R-3,lo+7,R+3,lo+7,R+6,lo-1]];
  case 'waking':return [line(L,1),[R-5,lo,R-2,lo+3,R+2,lo+3,R+5,lo]];
  default:return variant==='C'?[[L,49,L-1,53,L+1,61,L,65],[R,49,R-1,53,R+1,61,R,65]]:[line(L,1),line(R,-1)];}};
 const eyesFor=(mood,variant='A')=>rawEyesFor(mood,variant).map((p,j)=>p.map((v,i)=>i%2===0?(j?64:36)+(v-(j?64:36))*.6:57+(v-57)*(variant==='C'?.65:.52)));
 function svg(seed,mood='idle',mini=false,appearance={}){const t=traits(seed),p=PATHS[t.shape],variant=appearance.eyes||t.eyes,eye=eyesFor(mood,variant),gid='vb-mascot-hi-'+hash(seed);return `<svg class="vbox-mascot vbox-mascot-eyes-${variant}${mini?' vbox-mascot-mini':''}" viewBox="0 0 100 100" width="100%" height="100%" preserveAspectRatio="xMidYMid meet" data-mood="${mood}" data-seed="${esc(seed)}" role="img" aria-label="${mood} mascot" style="--mascot-color:${t.color};--mascot-delay:${-t.phase.toFixed(2)}s"><defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#fff" stop-opacity=".16"/><stop offset=".52" stop-color="#fff" stop-opacity=".03"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient></defs><g class="vbox-mascot-motion"><g class="vbox-mascot-body" style="transform-origin:50px 60px;transform:scale(${(t.wide*(appearance.chub||1)).toFixed(2)} ${t.tall.toFixed(2)}) rotate(${t.tilt.toFixed(1)}deg)"><path class="vbox-mascot-shape" d="${p}"/><path d="${p}" fill="url(#${gid})" pointer-events="none"/><g class="vbox-mascot-blush" opacity="0"><ellipse cx="27" cy="66" rx="7" ry="3.2"/><ellipse cx="73" cy="66" rx="7" ry="3.2"/></g><g class="vbox-mascot-eyes"><path class="vbox-mascot-eye left" d="${eyePath(eye[0])}"/><path class="vbox-mascot-eye right" d="${eyePath(eye[1])}"/><circle class="vbox-mascot-glint" cx="34" cy="53" r="1.3"/><circle class="vbox-mascot-glint" cx="62" cy="53" r="1.3"/></g></g><text class="vbox-mascot-z" x="78" y="25">z</text></g></svg>`}
 const POSES={};for(const mood of MOODS)POSES[mood]=eyesFor(mood,'soft');
 const hexRgb=hex=>[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16));
 const mix=(a,b,t)=>`rgb(${a.map((v,i)=>Math.round(v+(b[i]-v)*t)).join(' ')})`;
 const ease=t=>1-Math.pow(1-t,3);
 const overshoot=t=>1+2.15*Math.pow(t-1,3)+1.15*Math.pow(t-1,2);
 const active=new Set();let raf=0, last=0, forcedReduce=false;const reduce=matchMedia('(prefers-reduced-motion: reduce)');const observer=typeof IntersectionObserver==='undefined'?null:new IntersectionObserver(entries=>{for(const e of entries){const m=e.target.__vboxMascot;if(m)m.visible=e.isIntersecting}kick()});
 function kick(){if(!raf&&!document.hidden&&!reduce.matches&&!forcedReduce&&[...active].some(m=>m.visible))raf=requestAnimationFrame(tick)}
 function tick(now){raf=0;if(document.hidden||reduce.matches||forcedReduce)return;const dt=Math.min(60,now-last||16);last=now;for(const m of active)if(m.visible)m.frame(now,dt);kick()}
 document.addEventListener('visibilitychange',kick);reduce.addEventListener?.('change',()=>{for(const m of active)m.render(reduce.matches);kick()});
 class Mascot{
  constructor(host,seed,{onMood,appearance={}}={}){this.host=host;this.seed=String(seed);this.traits=traits(seed);this.onMood=onMood;this.appearance=appearance;this.state='idle';this.phase='settled';this.visible=true;this.dead=false;this.motionTime=0;this.blinkAt=performance.now()+this.traits.blink*1000;this.glanceAt=performance.now()+1600+this.traits.phase*500;this.glance=0;this.pose=eyesFor('idle',appearance.eyes||this.traits.eyes).flat();this.target=this.pose.slice();this.fromPose=this.pose.slice();this.color=hexRgb(this.traits.color);this.fromColor=this.color.slice();this.toColor=this.color.slice();this.transition=null;this.blink=0;this.accent=0;this.timers=[];this.host.innerHTML=svg(seed,'idle',false,appearance);this.svg=host.querySelector('svg');this.svg.__vboxMascot=this;this.motion=this.svg.querySelector('.vbox-mascot-motion');this.body=this.svg.querySelector('.vbox-mascot-body');this.eyes=[...this.svg.querySelectorAll('.vbox-mascot-eye')];this.blush=this.svg.querySelector('.vbox-mascot-blush');this.z=this.svg.querySelector('.vbox-mascot-z');this.blinkStarted=0;this.blinkTwice=false;this.enter=()=>{if(!reduce.matches&&!forcedReduce)this.accent=1};this.down=()=>{if(!reduce.matches&&!forcedReduce)this.accent=-1};this.host.addEventListener('pointerenter',this.enter);this.host.addEventListener('pointerdown',this.down);observer?.observe(this.svg);active.add(this);this.render(true);kick()}
  clearTimer(){for(const t of this.timers)clearTimeout(t);this.timers=[]}
  send(evt){const next=MACHINE[this.state]?.[evt];if(!next)return false;return this.jump(next)}
  jump(mood){if(!MOODS.includes(mood)||this.dead||mood===this.state)return false;
   this.clearTimer();const now=performance.now();this.updateTween(now);this.state=mood;this.svg.dataset.mood=mood;this.svg.setAttribute('aria-label',mood+' mascot');
   this.fromPose=this.pose.slice();this.target=eyesFor(mood,this.appearance.eyes||this.traits.eyes).flat();this.fromColor=this.color.slice();this.toColor=hexRgb(mood==='angry'?'#FF6F59':mood==='sleeping'?'#a9a8bf':mood==='working'?'#'+this.traits.color.slice(1).match(/../g).map(x=>Math.min(255,parseInt(x,16)+8).toString(16).padStart(2,'0')).join(''):this.traits.color);
   this.transition={start:now,duration:mood==='happy'||mood==='waking'?420:360};this.accent=mood==='happy'||mood==='waking'?2:mood==='angry'?-2:0;
   if(reduce.matches||forcedReduce){this.transition=null;this.pose=this.target.slice();this.color=this.toColor.slice();this.render(true)}
   this.onMood?.(mood,this);if(mood==='waking'&&!reduce.matches&&!forcedReduce)this.timers.push(setTimeout(()=>{if(this.state!=='waking')return;const now=performance.now();this.updateTween(now);this.fromPose=this.pose.slice();this.target=eyesFor('idle',this.appearance.eyes||this.traits.eyes).flat();this.transition={start:now,duration:300}},240));if(['happy','laughing','angry','waking'].includes(mood))this.timers.push(setTimeout(()=>this.jump(mood==='waking'?'idle':mood==='laughing'?'happy':'idle'),mood==='waking'?1500:3400));return true}
  updateTween(now){if(!this.transition)return 0;const p=Math.min(1,Math.max(0,(now-this.transition.start)/this.transition.duration)),e=overshoot(p),c=ease(p);for(let i=0;i<16;i++)this.pose[i]=this.fromPose[i]+(this.target[i]-this.fromPose[i])*e;this.color=this.fromColor.map((v,i)=>v+(this.toColor[i]-v)*c);if(p>=1){this.pose=this.target.slice();this.color=this.toColor.slice();this.transition=null}return Math.sin(p*Math.PI)}
  frame(now,dt){this.motionTime+=dt/1000;const t=this.motionTime+this.traits.phase;const speed=this.state==='sleeping'?.65:this.state==='working'?4.3:2.24;const breath=Math.sin(t*speed),morph=this.updateTween(now);let bob=breath*2.2,sy=1+breath*.02,sx=1-breath*.018,rot=0;
   if(this.state==='working'){bob=breath*.9;rot=Math.sin(t*3)*.8}
   if(this.state==='waiting'){bob=Math.sin(t*1.1)*1.4;rot=Math.sin(t*1.1)*1.2}
   if(this.state==='angry'){bob=Math.sin(t*32)*1.4;rot=Math.sin(t*32)*1.3;sx+=.02}
   if(this.state==='sleeping'){bob=Math.sin(t*.65)*.8;sx=1+Math.sin(t*.65)*.025;sy=1-Math.sin(t*.65)*.02;this.z.style.transform=`translateY(${(-Math.sin(t*.9)*2).toFixed(1)}px)`}
   if(this.state==='happy'||this.state==='laughing'){const cycle=(t%(this.state==='happy'?1.7:1.15))/(this.state==='happy'?1.7:1.15),jump=cycle<.55?Math.sin(cycle/.55*Math.PI):0,land=cycle>=.55?Math.exp(-(cycle-.55)*14)*Math.sin((cycle-.55)*35):0;bob=-jump*(this.state==='happy'?7:4)+land*.9;sy+=jump*.085-land*.055;sx-=jump*.055-land*.04;rot=Math.sin(cycle*Math.PI*2)*(this.state==='happy'?3:4)*jump}
   if(now>this.blinkAt&&this.state!=='sleeping'){this.blinkStarted=now;this.blinkTwice=hash(this.seed+Math.floor(now/1000))%4===0;this.blinkAt=now+2300+hash(this.seed+Math.floor(now/1000))%4300}
   const age=now-this.blinkStarted,blinkOne=age<170?age:age>255&&this.blinkTwice&&age<425?age-255:-1;this.blink=blinkOne<0?0:blinkOne<48?blinkOne/48:Math.max(0,1-(blinkOne-48)/122);
   if(now>this.glanceAt){this.glance=(hash(this.seed+Math.floor(now/1600))%3)-1;this.glanceAt=now+1700+hash(this.seed+Math.floor(now/900))%2400}
   this.accent*=.88;this.motion.style.transform=`translate3d(${this.state==='angry'?Math.sin(t*32)*1.5:0}px,${(bob-this.accent*2).toFixed(2)}px,0) rotate(${rot.toFixed(2)}deg) scale(${(sx+this.accent*.035+morph*.045).toFixed(3)},${(sy-this.accent*.045-morph*.055).toFixed(3)})`;
   this.render(false,now)}
  render(instant,now=performance.now()){if(instant&&this.transition){this.pose=this.target.slice();this.color=this.toColor.slice();this.transition=null}this.svg.querySelector('.vbox-mascot-shape').style.fill=mix(this.color,this.color,0);this.blush.style.opacity=this.appearance.blush===false||!['happy','laughing'].includes(this.state)?0:'.36';this.z.style.opacity=this.state==='sleeping'?'0.75':'0';this.eyes.forEach((el,j)=>{const p=this.pose.slice(j*8,j*8+8),g=instant?0:this.glance*1.2;for(let k=0;k<8;k+=2)p[k]+=g;if(this.blink&&!instant){for(let k=1;k<8;k+=2)p[k]+=(53-p[k])*this.blink*.93}el.setAttribute('d',eyePath(p.map(v=>+v.toFixed(1))))});if(instant)this.motion.style.transform='none'}
  destroy(){this.dead=true;this.clearTimer();this.host.removeEventListener('pointerenter',this.enter);this.host.removeEventListener('pointerdown',this.down);observer?.unobserve(this.svg);active.delete(this);this.svg.remove();kick()}
 }
 global.VBoxMascot={Mascot,svg,miniSVG:(seed,mood='idle')=>svg(seed,mood,true),setReducedMotion:value=>{forcedReduce=!!value;for(const m of active)m.render(forcedReduce);kick()},traits,shapes:SHAPES,colors:COLORS,moods:MOODS};
})(window);
