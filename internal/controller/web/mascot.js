/* vbox mascot system: seeded silhouettes, one animation clock, no dependencies. */
(function(global){
 'use strict';
 const SHAPES=['circle','drop','cloud','square','gem','triangle','pill','bean'];
 const COLORS=['#8657E8','#328EF0','#23BD70','#19B8AA','#FB852E','#E9AD18','#EC4CA1','#EF4D56','#986849','#69717D','#FF6F59','#1B2A41'];
 const MOODS=['idle','working','waiting','happy','laughing','angry','sleeping','waking'];
 const MACHINE={idle:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry',SLEEP:'sleeping',WAKE:'waking'},working:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},waiting:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},happy:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle',SLEEP:'sleeping'},laughing:{SETTLE:'happy',WORK:'working',ERROR:'angry',SLEEP:'sleeping'},angry:{CALM:'idle',PRAISE:'happy',WORK:'working',SLEEP:'sleeping'},sleeping:{WAKE:'waking',WORK:'waking'},waking:{READY:'idle',WORK:'working',SLEEP:'sleeping'}};
 const hash=s=>{let h=2166136261;for(const c of String(s)){h^=c.charCodeAt(0);h=Math.imul(h,16777619)}return h>>>0};
 const traits=seed=>{const h=hash(seed);return {shape:SHAPES[h%SHAPES.length],color:COLORS[Math.floor(h/8)%COLORS.length],wide:1.02+((h>>>12)%9)/100,tall:.91+((h>>>17)%9)/100,tilt:((h>>>22)%9-4)*.35,phase:(h%1000)/1000*6.283,blink:2.3+(h%190)/100,eyes:['soft','wide','tiny'][Math.floor(h/64)%3]}};
 const PATHS={
 circle:'M50 12 C72 12 87 28 88 49 C90 72 75 88 50 89 C25 88 10 72 12 49 C13 28 28 12 50 12 Z',
 drop:'M50 9 C43 17 25 34 19 53 C13 75 30 89 50 90 C70 89 87 75 81 53 C75 34 57 17 50 9 Z',
 cloud:'M25 42 C18 29 32 19 45 26 C57 14 73 23 74 36 C87 39 93 53 82 65 C85 79 72 87 60 83 C48 93 34 87 30 81 C16 81 9 68 15 57 C11 51 17 44 25 42 Z',
 square:'M31 12 C18 12 12 22 12 34 L12 65 C12 81 21 89 35 89 L65 89 C79 89 88 81 88 65 L88 34 C88 22 82 12 69 12 Z',
 gem:'M50 12 Q59 12 65 17 L82 32 Q89 38 88 48 L86 66 Q85 75 77 80 L61 88 Q50 94 39 88 L23 80 Q15 75 14 66 L12 48 Q11 38 18 32 L35 17 Q41 12 50 12 Z',
 triangle:'M40 21 Q50 8 60 21 C69 33 81 51 87 68 Q93 86 75 88 L25 88 Q7 86 13 68 C19 51 31 33 40 21 Z',
 pill:'M31 28 L69 28 C82 28 90 38 90 51 C90 66 81 76 67 76 L33 76 C19 76 10 66 10 51 C10 38 18 28 31 28 Z',
 bean:'M27 18 C41 11 53 24 67 22 C82 21 91 37 89 55 C87 77 72 89 51 89 C30 89 13 76 11 56 C9 39 16 23 27 18 Z'};
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const eyePath=p=>`M${p[0]} ${p[1]} C${p[2]} ${p[3]} ${p[4]} ${p[5]} ${p[6]} ${p[7]}`;
 const eyesFor=(mood,variant='soft')=>{const d=variant==='wide'?2:0,small=variant==='tiny'?1:0;const L=39-d,R=61+d,lo=53;const line=(cx,slant=1)=>[cx-2+small,lo-4*slant,cx-1,lo-1*slant,cx+1,lo+2*slant,cx+2-small,lo+4*slant];switch(mood){
  case 'working':return [line(L,.5),line(R,-.5)];
  case 'waiting':return [[L-2,lo-5,L-1,lo-4,L+1,lo-2,L+2,lo-1],[R-2,lo-5,R-1,lo-4,R+1,lo-2,R+2,lo-1]];
  case 'happy':return [[L-4,lo+2,L-2,lo-1,L+1,lo-2,L+4,lo+2],[R-4,lo+2,R-1,lo-2,R+2,lo-1,R+4,lo+2]];
  case 'laughing':return [[L-4,lo+3,L-2,lo-2,L+2,lo-2,L+4,lo+3],[R-4,lo+3,R-2,lo-2,R+2,lo-2,R+4,lo+3]];
  case 'angry':return [[L-4,lo-3,L-2,lo-2,L+2,lo+1,L+4,lo+2],[R-4,lo+2,R-2,lo+1,R+2,lo-2,R+4,lo-3]];
  case 'sleeping':return [[L-4,lo,L-2,lo+2,L+2,lo+2,L+4,lo],[R-4,lo,R-2,lo+2,R+2,lo+2,R+4,lo]];
  case 'waking':return [[L-2,lo-4,L-2,lo-4,L+2,lo-4,L+2,lo+3],[R-2,lo-4,R-2,lo-4,R+2,lo-4,R+2,lo+3]];
  default:return [line(L),line(R,-1)];}};
 function svg(seed,mood='idle',mini=false,appearance={}){const t=traits(seed),p=PATHS[t.shape],variant=appearance.eyes||t.eyes,eye=eyesFor(mood,variant),gid='vb-mascot-hi-'+hash(seed);return `<svg class="vbox-mascot${mini?' vbox-mascot-mini':''}" viewBox="0 0 100 100" width="100%" height="100%" preserveAspectRatio="xMidYMid meet" data-mood="${mood}" data-seed="${esc(seed)}" role="img" aria-label="${mood} mascot" style="--mascot-color:${t.color};--mascot-delay:${-t.phase.toFixed(2)}s"><defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#fff" stop-opacity=".16"/><stop offset=".52" stop-color="#fff" stop-opacity=".03"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient></defs><g class="vbox-mascot-motion"><g class="vbox-mascot-body" style="transform-origin:50px 60px;transform:scale(${(t.wide*(appearance.chub||1)).toFixed(2)} ${t.tall.toFixed(2)}) rotate(${t.tilt.toFixed(1)}deg)"><path class="vbox-mascot-shape" d="${p}"/><path d="${p}" fill="url(#${gid})" pointer-events="none"/><g class="vbox-mascot-blush" opacity="0"><ellipse cx="27" cy="66" rx="7" ry="3.2"/><ellipse cx="73" cy="66" rx="7" ry="3.2"/></g><g class="vbox-mascot-eyes"><path class="vbox-mascot-eye left" d="${eyePath(eye[0])}"/><path class="vbox-mascot-eye right" d="${eyePath(eye[1])}"/></g></g></g></svg>`}
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
  constructor(host,seed,{onMood,appearance={}}={}){this.host=host;this.seed=String(seed);this.traits=traits(seed);this.onMood=onMood;this.appearance=appearance;this.state='idle';this.phase='settled';this.visible=true;this.dead=false;this.motionTime=0;this.blinkAt=performance.now()+this.traits.blink*1000;this.glanceAt=performance.now()+1600+this.traits.phase*500;this.glance=0;this.pose=eyesFor('idle',appearance.eyes||this.traits.eyes).flat();this.target=this.pose.slice();this.fromPose=this.pose.slice();this.color=hexRgb(this.traits.color);this.fromColor=this.color.slice();this.toColor=this.color.slice();this.transition=null;this.blink=0;this.accent=0;this.timers=[];this.host.innerHTML=svg(seed,'idle',false,appearance);this.svg=host.querySelector('svg');this.svg.__vboxMascot=this;this.motion=this.svg.querySelector('.vbox-mascot-motion');this.body=this.svg.querySelector('.vbox-mascot-body');this.eyes=[...this.svg.querySelectorAll('.vbox-mascot-eye')];this.blush=this.svg.querySelector('.vbox-mascot-blush');this.blinkStarted=0;this.blinkTwice=false;this.enter=()=>{if(!reduce.matches&&!forcedReduce)this.accent=1};this.down=()=>{if(!reduce.matches&&!forcedReduce)this.accent=-1};this.host.addEventListener('pointerenter',this.enter);this.host.addEventListener('pointerdown',this.down);observer?.observe(this.svg);active.add(this);this.render(true);kick()}
  clearTimer(){for(const t of this.timers)clearTimeout(t);this.timers=[]}
  send(evt){const next=MACHINE[this.state]?.[evt];if(!next)return false;return this.jump(next)}
  jump(mood){if(!MOODS.includes(mood)||this.dead||mood===this.state)return false;
   this.clearTimer();const now=performance.now();this.updateTween(now);this.state=mood;this.svg.dataset.mood=mood;this.svg.setAttribute('aria-label',mood+' mascot');
   this.fromPose=this.pose.slice();this.target=eyesFor(mood,this.appearance.eyes||this.traits.eyes).flat();this.fromColor=this.color.slice();this.toColor=hexRgb(mood==='angry'?'#FF6F59':mood==='sleeping'?'#a9a8bf':mood==='working'?'#'+this.traits.color.slice(1).match(/../g).map(x=>Math.min(255,parseInt(x,16)+8).toString(16).padStart(2,'0')).join(''):this.traits.color);
   this.transition={start:now,duration:mood==='happy'||mood==='waking'?420:360};this.accent=mood==='happy'||mood==='waking'?2:mood==='angry'?-2:0;
   if(reduce.matches||forcedReduce){this.transition=null;this.pose=this.target.slice();this.color=this.toColor.slice();this.render(true)}
   this.onMood?.(mood,this);if(['happy','laughing','angry','waking'].includes(mood))this.timers.push(setTimeout(()=>this.jump(mood==='waking'?'idle':mood==='laughing'?'happy':'idle'),mood==='waking'?1500:3400));return true}
  updateTween(now){if(!this.transition)return 0;const p=Math.min(1,Math.max(0,(now-this.transition.start)/this.transition.duration)),e=overshoot(p),c=ease(p);for(let i=0;i<16;i++)this.pose[i]=this.fromPose[i]+(this.target[i]-this.fromPose[i])*e;this.color=this.fromColor.map((v,i)=>v+(this.toColor[i]-v)*c);if(p>=1){this.pose=this.target.slice();this.color=this.toColor.slice();this.transition=null}return Math.sin(p*Math.PI)}
  frame(now,dt){this.motionTime+=dt/1000;const t=this.motionTime+this.traits.phase;const speed=this.state==='sleeping'?.7:this.state==='working'?4.3:1.8;const breath=Math.sin(t*speed),morph=this.updateTween(now);let bob=breath*1.2,sy=1+breath*.017,sx=1-breath*.014,rot=0;
   if(this.state==='working'){bob=breath*.9;rot=Math.sin(t*3)*.8}
   if(this.state==='waiting'){bob=Math.sin(t*1.1)*1.4;rot=Math.sin(t*1.1)*1.2}
   if(this.state==='angry'){bob=Math.sin(t*32)*1.4;rot=Math.sin(t*32)*1.3;sx+=.02}
   if(this.state==='sleeping'){bob=Math.sin(t*.7)*.6;sx=1+Math.sin(t*.7)*.022;sy=1-Math.sin(t*.7)*.015}
   if(this.state==='happy'||this.state==='laughing'){const cycle=(t%(this.state==='happy'?1.7:1.15))/(this.state==='happy'?1.7:1.15),jump=cycle<.55?Math.sin(cycle/.55*Math.PI):0,land=cycle>=.55?Math.exp(-(cycle-.55)*14)*Math.sin((cycle-.55)*35):0;bob=-jump*(this.state==='happy'?7:4)+land*.9;sy+=jump*.085-land*.055;sx-=jump*.055-land*.04;rot=Math.sin(cycle*Math.PI*2)*(this.state==='happy'?3:4)*jump}
   if(now>this.blinkAt&&this.state!=='sleeping'){this.blinkStarted=now;this.blinkTwice=hash(this.seed+Math.floor(now/1000))%4===0;this.blinkAt=now+2300+hash(this.seed+Math.floor(now/1000))%4300}
   const age=now-this.blinkStarted,blinkOne=age<170?age:age>255&&this.blinkTwice&&age<425?age-255:-1;this.blink=blinkOne<0?0:blinkOne<48?blinkOne/48:Math.max(0,1-(blinkOne-48)/122);
   if(now>this.glanceAt){this.glance=(hash(this.seed+Math.floor(now/1600))%3)-1;this.glanceAt=now+1700+hash(this.seed+Math.floor(now/900))%2400}
   this.accent*=.88;this.motion.style.transform=`translate3d(${this.state==='angry'?Math.sin(t*32)*1.5:0}px,${(bob-this.accent*2).toFixed(2)}px,0) rotate(${rot.toFixed(2)}deg) scale(${(sx+this.accent*.035+morph*.045).toFixed(3)},${(sy-this.accent*.045-morph*.055).toFixed(3)})`;
   this.render(false,now)}
  render(instant,now=performance.now()){if(instant&&this.transition){this.pose=this.target.slice();this.color=this.toColor.slice();this.transition=null}this.svg.querySelector('.vbox-mascot-shape').style.fill=mix(this.color,this.color,0);this.blush.style.opacity=this.appearance.blush===false||!['happy','laughing'].includes(this.state)?0:'.4';this.eyes.forEach((el,j)=>{const p=this.pose.slice(j*8,j*8+8),g=instant?0:this.glance*1.2;for(let k=0;k<8;k+=2)p[k]+=g;if(this.blink&&!instant){for(let k=1;k<8;k+=2)p[k]+=(53-p[k])*this.blink*.93}el.setAttribute('d',eyePath(p.map(v=>+v.toFixed(1))))});if(instant)this.motion.style.transform='none'}
  destroy(){this.dead=true;this.clearTimer();this.host.removeEventListener('pointerenter',this.enter);this.host.removeEventListener('pointerdown',this.down);observer?.unobserve(this.svg);active.delete(this);this.svg.remove();kick()}
 }
 global.VBoxMascot={Mascot,svg,miniSVG:(seed,mood='idle')=>svg(seed,mood,true),setReducedMotion:value=>{forcedReduce=!!value;for(const m of active)m.render(forcedReduce);kick()},traits,shapes:SHAPES,colors:COLORS,moods:MOODS};
})(window);
