/* vbox mascot system: seeded colour, round bodies, and Motion-driven symbols. */
(function(global){
 'use strict';
 const COLORS=['#965FF0','#378EF5','#24C77A','#23C5BB','#FF8A38','#F5BC29','#F253B1','#F25564','#BE67E8','#60C989','#FF6F59','#1B2A41'];
 const MOODS=['idle','working','waiting','happy','laughing','angry','sleeping','waking'];
 const MACHINE={idle:{WORK:'working',SEND:'waiting',PRAISE:'happy',JOKE:'laughing',ERROR:'angry',SLEEP:'sleeping',WAKE:'waking'},working:{SEND:'waiting',DONE:'happy',ERROR:'angry',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},waiting:{REPLY:'happy',TIMEOUT:'angry',WORK:'working',JOKE:'laughing',STOP:'idle',SLEEP:'sleeping'},happy:{JOKE:'laughing',WORK:'working',SEND:'waiting',ERROR:'angry',SETTLE:'idle',SLEEP:'sleeping'},laughing:{SETTLE:'happy',WORK:'working',ERROR:'angry',SLEEP:'sleeping'},angry:{CALM:'idle',PRAISE:'happy',WORK:'working',SLEEP:'sleeping'},sleeping:{WAKE:'waking',WORK:'waking'},waking:{READY:'idle',WORK:'working',SLEEP:'sleeping'}};
 const hash=s=>{let h=2166136261;for(const c of String(s)){h^=c.charCodeAt(0);h=Math.imul(h,16777619)}return h>>>0};
 const traits=seed=>{const h=hash(seed);return {shape:'circle',color:h%37===0?COLORS[11]:COLORS[Math.floor(h/8)%11],eyeSpread:((h>>>12)%7-3)*.35,eyeTilt:((h>>>17)%7-3)*.12,phase:(h%1000)/1000*6.283,blink:2.3+(h%190)/100,eyes:'A'}};
 const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
 const eyePath=p=>`M${p[0]} ${p[1]} C${p[2]} ${p[3]} ${p[4]} ${p[5]} ${p[6]} ${p[7]}`;
 const eyeGeometry=p=>{const cx=(p[0]+p[6])/2,cy=(p[1]+p[7])/2,dx=p[6]-p[0],dy=p[7]-p[1];return {cx,cy,length:Math.hypot(dx,dy),angle:Math.atan2(dy,dx)*180/Math.PI}};
 const eyeLine=(cx,cy,length,angle)=>{const a=angle*Math.PI/180,dx=Math.cos(a)*length/2,dy=Math.sin(a)*length/2,x1=cx-dx,y1=cy-dy,x2=cx+dx,y2=cy+dy;return `M${x1.toFixed(3)} ${y1.toFixed(3)} C${(x1+(x2-x1)/3).toFixed(3)} ${(y1+(y2-y1)/3).toFixed(3)} ${(x1+2*(x2-x1)/3).toFixed(3)} ${(y1+2*(y2-y1)/3).toFixed(3)} ${x2.toFixed(3)} ${y2.toFixed(3)}`};
 const rawEyesFor=(mood,variant='A')=>{const spread=variant==='C'?1:0,L=39-spread,R=61+spread,lo=53;
  const line=(cx,length=1,dy=0)=>{const x=4.1*length,y=9.5*length;return [cx-x,lo+dy-y,cx-x/3,lo+dy-y/3,cx+x/3,lo+dy+y/3,cx+x,lo+dy+y]};
  const poses={curious:[.045,.045],wink:[1,.28],excited:[1.45,1.45],working:[.9,.9],waiting:[.85,.85],happy:[.68,.68],laughing:[.52,.52],angry:[.8,.8],sleeping:[.55,.55],waking:[1,1]};
  const lengths=poses[mood]||[1,1];return [line(L,lengths[0]),line(R,lengths[1])];
 };
 const eyesFor=(mood,variant='A',t=null)=>rawEyesFor(mood,variant).map((p,j)=>p.map((v,i)=>i%2===0?(j?61:39)+(v-(j?61:39))*.8+(j?1:-1)*(t?.eyeSpread||0)+2.5:52.5+(v-57)*(variant==='C'?.72:.64)+(j?1:-1)*(t?.eyeTilt||0)*(v-(j?61:39))));
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
 const stemPath=t=>{const top=76-57*t,bottom=76-8*t,w=5*t,b=2.4*t,r=4*t;return `M${(50-w).toFixed(2)} ${(top+r).toFixed(2)} Q${(50-w).toFixed(2)} ${top.toFixed(2)} 50 ${top.toFixed(2)} Q${(50+w).toFixed(2)} ${top.toFixed(2)} ${(50+w).toFixed(2)} ${(top+r).toFixed(2)} L${(50+b).toFixed(2)} ${bottom.toFixed(2)} Q50 ${(bottom+3*t).toFixed(2)} ${(50-b).toFixed(2)} ${bottom.toFixed(2)} Z`};
 const pointsPath=pts=>{let d=`M${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;for(let i=0;i<pts.length;i++){const p0=pts[(i-1+pts.length)%pts.length],p1=pts[i],p2=pts[(i+1)%pts.length],p3=pts[(i+2)%pts.length];d+=` C${(p1[0]+(p2[0]-p0[0])/6).toFixed(2)} ${(p1[1]+(p2[1]-p0[1])/6).toFixed(2)} ${(p2[0]-(p3[0]-p1[0])/6).toFixed(2)} ${(p2[1]-(p3[1]-p1[1])/6).toFixed(2)} ${p2[0].toFixed(2)} ${p2[1].toFixed(2)}`}return d+' Z'};
 function svg(seed,mood='idle',mini=false,appearance={},expression=MOOD_GLYPH[mood]||null){const t=traits(seed),p=pointsPath(circlePoints(39)),variant=appearance.eyes||t.eyes,eyeMood={happy:'happy',surprised:'curious',sleeping:'sleeping',focused:'working'}[expression]||mood,eye=eyesFor(eyeMood,variant,t),eyeGeo=eye.map(eyeGeometry),gid='vb-mascot-hi-'+hash(seed);if(eyeMood!==mood)expression=null;return `<svg class="vbox-mascot vbox-mascot-eyes-${variant}${mini?' vbox-mascot-mini':''}" viewBox="-6 -6 112 112" width="100%" height="100%" preserveAspectRatio="xMidYMid meet" data-mood="${mood}" data-expression="${expression||''}" data-seed="${esc(seed)}" role="img" aria-label="${mood} mascot" ><defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1" gradientUnits="objectBoundingBox"><stop stop-color="#fff" stop-opacity=".16"/><stop offset=".52" stop-color="#fff" stop-opacity=".03"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient><linearGradient id="vb-comet"><stop stop-color="#FF6F59"/><stop offset=".35" stop-color="#F5BC29"/><stop offset=".7" stop-color="#24C77A"/><stop offset="1" stop-color="#378EF5"/></linearGradient></defs><g class="vbox-mascot-motion"><path class="vbox-mascot-comet" d="M8 65 Q30 61 48 53" fill="none" stroke="url(#vb-comet)" stroke-width="1" stroke-linecap="round" opacity="0"/><g class="vbox-mascot-body" ><path class="vbox-mascot-shape" d="${p}" fill="${mood==='angry'?'#FF6F59':mood==='sleeping'?'#a9a8bf':t.color}"/><path class="vbox-mascot-highlight" d="${p}" fill="url(#${gid})" pointer-events="none"/><g class="vbox-mascot-blush" opacity="0"><ellipse cx="27" cy="66" rx="7" ry="3.2"/><ellipse cx="73" cy="66" rx="7" ry="3.2"/></g><g class="vbox-mascot-eyes" opacity="${expression?0:1}">${eyeGeo.map((g,i)=>`<g class="eye-blink" transform="translate(${g.cx} ${g.cy}) scale(1 ${eyeMood==='sleeping'?.15:1}) translate(${-g.cx} ${-g.cy})"><g class="eye-tilt" transform="rotate(${g.angle} ${g.cx} ${g.cy})"><path class="vbox-mascot-eye ${i?'right':'left'}" vector-effect="${appearance.animated?'non-scaling-stroke':'none'}" d="${eyeLine(g.cx,g.cy,g.length,0)}"/></g></g>`).join('')}<circle class="vbox-mascot-glint" cx="34" cy="53" r="1.3"/><circle class="vbox-mascot-glint" cx="62" cy="53" r="1.3"/></g><g class="vbox-mascot-glyph-host">${glyphMarkup(expression)}</g></g><path class="vbox-mascot-bang-stem" d="${stemPath(0)}" fill="${t.color}"/><g class="vbox-mascot-dots" opacity="0"><circle class="vbox-mascot-dot-left" cx="50" cy="52" r="0" fill="${t.color}"/><circle class="vbox-mascot-dot-right" cx="50" cy="52" r="0" fill="${t.color}"/></g><circle class="vbox-mascot-notice" cx="79" cy="22" r="5" fill="#378EF5" stroke="#fff" stroke-width="2" opacity="0"/><g class="vbox-mascot-particles" opacity="0"><circle cx="24" cy="34" r="2" fill="#FF6F59"/><circle cx="77" cy="36" r="2" fill="#F5BC29"/><circle cx="25" cy="77" r="2" fill="#24C77A"/><circle cx="77" cy="74" r="2" fill="#378EF5"/></g></g></svg>`}
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
 let morphClock=0;
 function morphFrame(now){if(!active.size){morphClock=0;return}morphClock=requestAnimationFrame(morphFrame);if(quiet())return;for(const mascot of active)mascot.frame(now)}
 document.addEventListener('visibilitychange',()=>{for(const mascot of active)mascot.updateVisibility()});
 reduce.addEventListener?.('change',()=>{for(const mascot of active)mascot.updateVisibility()});
 class Mascot{
  constructor(host,seed,{onMood,appearance={}}={}){
   this.host=host;this.seed=String(seed);this.traits=traits(seed);this.appearance=appearance;this.onMood=onMood||null;
   this.state='idle';this.expression=null;this.signal='idle';this.visible=true;this.dead=false;this.token=0;this.phase='settled';this.controls=new Set();this.timers=[];this.sleepScale=1;this.busyMode=hash(this.seed)%2?'ball':'dots';this.busyInterval=8000+hash(this.seed+'busy')%4001;this.busyRemaining=this.busyInterval;
   host.innerHTML=svg(seed,'idle',false,{...appearance,animated:true});
   this.svg=host.querySelector('svg');if(host.classList.contains('avatar-mascot')||host.classList.contains('processing-mascot'))this.svg.classList.add('vbox-mascot-mini');this.svg.__vboxMascot=this;
   this.motion=this.svg.querySelector('.vbox-mascot-motion');this.body=this.svg.querySelector('.vbox-mascot-body');this.shapePath=this.svg.querySelector('.vbox-mascot-shape');this.highlight=this.svg.querySelector('.vbox-mascot-highlight');this.eyesGroup=this.svg.querySelector('.vbox-mascot-eyes');this.eyes=[...this.svg.querySelectorAll('.vbox-mascot-eye')];this.blinkGroups=[...this.svg.querySelectorAll('.eye-blink')];this.tiltGroups=[...this.svg.querySelectorAll('.eye-tilt')];this.glyphHost=this.svg.querySelector('.vbox-mascot-glyph-host');this.blush=this.svg.querySelector('.vbox-mascot-blush');this.dots=this.svg.querySelector('.vbox-mascot-dots');this.dotLeft=this.svg.querySelector('.vbox-mascot-dot-left');this.dotRight=this.svg.querySelector('.vbox-mascot-dot-right');this.notice=this.svg.querySelector('.vbox-mascot-notice');this.bangStem=this.svg.querySelector('.vbox-mascot-bang-stem');this.particles=this.svg.querySelector('.vbox-mascot-particles');this.cometTrail=this.svg.querySelector('.vbox-mascot-comet');
   this.paths={home:pointsPath(circlePoints(39)),bangDot:pointsPath(circlePoints(4,50,82)),dot:pointsPath(circlePoints(5))};this.glanceCount=0;this.blinkedYet=false;this.expressionCount=0;
   this.breathPhase=this.traits.phase;this.breathSpeed=2*Math.PI/3.6;this.breathAccel=0;this.formAngle=0;this.bangAngle=0;this.stemProgress=0;this.faceScale=1;this.faceRadius=39;this.blinkScale=1;this.blinking=false;this.blinkGeneration=0;this.blinkQueued=false;this.eyeMorphUntil=0;this.gazeUntil=0;this.pendingLook=null;this.pendingEyes=null;this.dotCenterRadius=5;this.lastFrame=0;
   this.currentPath=this.paths.home;this.setPath(this.paths.home);this.look=[0,0];this.wasMounted=false;this.setEyes('idle',null,true);
   this.pointerMove=e=>{if(quiet()||!this.visible||host.getBoundingClientRect().width<120||this.expression)return;const r=host.getBoundingClientRect();this.lookTo(((e.clientX-r.left)/r.width-.5)*5,((e.clientY-r.top)/r.height-.5)*3,.22)};
   this.pointerLeave=()=>this.lookTo(0,0,.3);
   host.addEventListener('pointermove',this.pointerMove);host.addEventListener('pointerleave',this.pointerLeave);observer?.observe(this.svg);active.add(this);if(!morphClock)morphClock=requestAnimationFrame(morphFrame);this.scheduleBlink();this.scheduleGlance();this.scheduleExpression();
  }
  track(control){this.controls.add(control);control.finished?.finally(()=>this.controls.delete(control)).catch(()=>{});return control}
  stopControls(){for(const control of this.controls)control.stop?.();this.controls.clear()}
  clearTimers(){for(const t of this.timers)clearTimeout(t);this.timers=[];clearTimeout(this.busyTimer);this.busyTimer=null;clearTimeout(this.expressionTimer);this.expressionTimer=null}
  timer(fn,ms){const t=setTimeout(fn,ms);this.timers.push(t);return t}
  setPath(d){this.currentPath=d;this.shapePath.setAttribute('d',d);this.highlight.setAttribute('d',d)}
  updateFaceTransform(radius=this.faceRadius){
   const scale=this.faceScale,maxShift=Math.max(0,radius*.82-19.5*scale),scan=this.signal==='busy'&&this.busyMode==='ball'?3.8*Math.sin(this.breathPhase*1.4):0;
   const drift=this.signal==='idle'?.8*Math.sin(this.breathPhase*.8+this.traits.phase):0,driftY=this.signal==='idle'?.45*Math.sin(this.breathPhase*.62+this.traits.phase):0;
   const x=Math.max(-maxShift,Math.min(maxShift,this.look[0]+scan+(this.blinking?this.blinkDriftX:drift)));
   const y=Math.max(-maxShift*.6,Math.min(maxShift*.6,this.look[1]+(this.blinking?this.blinkDriftY:driftY)));
   const transform='translate(50 57) rotate('+(-this.formAngle).toFixed(2)+') scale('+scale.toFixed(3)+') translate(-50 -57) translate('+x.toFixed(2)+' '+y.toFixed(2)+')';
   this.eyesGroup.setAttribute('transform',transform);this.glyphHost.setAttribute('transform',transform)
  }
  frame(now){
   if(this.dead||!this.visible){this.lastFrame=now;return}
   const dt=this.lastFrame?Math.min(50,now-this.lastFrame)/1000:0;this.lastFrame=now;
   const sleep=this.signal==='sleeping',busy=this.signal==='busy',dots=busy&&this.busyMode==='dots';
   const target=2*Math.PI/(sleep?6.5:busy?2.5:3.6);
   this.breathAccel+=(18*(target-this.breathSpeed)-8*this.breathAccel)*dt;
   this.breathSpeed+=this.breathAccel*dt;this.breathPhase+=this.breathSpeed*dt;
   if(this.phase!=='settled'||dots||(this.signal==='asking'||this.signal==='failed')&&this.stemProgress>0)return;
   const base=sleep?.58:1,amplitude=sleep?.008:busy?.024:.02;
   const scale=base*(1+amplitude*(.5-.5*Math.cos(this.breathPhase)));
   this.motion.setAttribute('transform','translate(50 52) scale('+scale.toFixed(4)+') translate(-50 -52)');
   this.faceRadius=39;this.faceScale=1;this.formAngle=0;
   this.updateFaceTransform();if(this.signal==='idle')this.scheduleExpression();
  }
  setSleepScale(value){this.sleepScale=value;this.motion.setAttribute('transform','translate(50 52) scale('+value.toFixed(3)+') translate(-50 -52)')}
  morphPath(to,duration=.54){const from=this.currentPath;if(from===to)return Promise.resolve();const c=this.track(animate(from,to,{duration,ease:[.55,0,.2,1],onUpdate:v=>this.setPath(v)}));return c.finished.catch(()=>{})}
  morph(name,duration=.54){return this.morphPath(this.paths[name],duration)}
  fade(node,to,duration=.22){const from=Number(node.getAttribute('opacity')||0);const c=this.track(animate(from,to,{duration,ease:'easeInOut',onUpdate:v=>node.setAttribute('opacity',v.toFixed(3))}));return c.finished.catch(()=>{})}
  lookTo(x,y,duration){
   if(this.dead||quiet())return;
   if(this.blinking){this.pendingLook=[x,y,duration];return}
   this.gazeUntil=performance.now()+duration*1000+30;
   const from=this.look.slice(),generation=(this.gazeGeneration||0)+1;this.gazeGeneration=generation;const lag=Math.min(.025,.012/duration);
   const control=this.track(animate(0,1,{duration,ease:'easeInOut',onUpdate:t=>{if(generation!==this.gazeGeneration)return;this.look=[from[0]+(x-from[0])*t,from[1]+(y-from[1])*t];this.updateFaceTransform();const delayed=Math.max(0,(t-lag)/(1-lag));this.eyes[0].removeAttribute('transform');this.eyes[1].setAttribute('transform','translate('+((x-from[0])*(delayed-t)).toFixed(3)+' '+((y-from[1])*(delayed-t)).toFixed(3)+')')}}));control.finished.then(()=>{if(generation===this.gazeGeneration)for(const eye of this.eyes)eye.removeAttribute('transform')}).catch(()=>{})
  }
  setEyes(mood,expression,instant=false){
   if(this.blinking&&!instant){this.pendingEyes=[mood,expression];return}
   const eyeExpression={happy:'happy',surprised:'curious',sleeping:'sleeping',focused:'working'}[expression];if(eyeExpression){mood=eyeExpression;expression=null}
   this.expression=expression;this.svg.dataset.expression=expression||'';
   for(const control of this.eyeControls||[])control.stop?.();this.eyeControls=[];
   const target=eyesFor(mood,this.appearance.eyes||this.traits.eyes,this.traits);
   const widths={curious:9,wink:9,excited:8.5,working:9,waiting:9,happy:7.5,laughing:8,angry:9,sleeping:4.8,waking:9};const width=(widths[mood]||9)*(Math.min(this.svg.getBoundingClientRect().width,this.svg.getBoundingClientRect().height)||112)/112;
   if(!instant)this.eyeMorphUntil=performance.now()+330;
   for(let i=0;i<2;i++){const eye=this.eyes[i],geo=eyeGeometry(target[i]),to=eyeLine(geo.cx,geo.cy,geo.length,0);this.tiltGroups[i].setAttribute('transform','rotate('+geo.angle.toFixed(3)+' '+geo.cx.toFixed(3)+' '+geo.cy.toFixed(3)+')');const blinkGroup=this.blinkGroups[i],targetScale=mood==='sleeping'?.15:1,fromScale=blinkGroup.transform.baseVal.consolidate()?.matrix.d||1,applyScale=v=>blinkGroup.setAttribute('transform','translate('+geo.cx.toFixed(3)+' '+geo.cy.toFixed(3)+') scale(1 '+v.toFixed(4)+') translate('+(-geo.cx).toFixed(3)+' '+(-geo.cy).toFixed(3)+')');if(instant)applyScale(targetScale);else this.eyeControls.push(this.track(animate(fromScale,targetScale,{duration:mood==='sleeping'?.48:.3,ease:'easeInOut',onUpdate:applyScale})));if(instant){eye.setAttribute('d',to);eye.style.strokeWidth=width}else{this.eyeControls.push(this.track(animate(eye.getAttribute('d'),to,{duration:.3,ease:'easeInOut',onUpdate:v=>eye.setAttribute('d',v)})));const fromWidth=parseFloat(getComputedStyle(eye).strokeWidth)||9;this.eyeControls.push(this.track(animate(0,1,{duration:.3,ease:'easeInOut',onUpdate:t=>eye.style.strokeWidth=(fromWidth+(width-fromWidth)*t+.04*width*Math.sin(Math.PI*t)).toFixed(3)})))}}
   const newMarkup=glyphMarkup(expression);this.glyphHost.setAttribute('opacity','1');
   if(instant){this.glyphHost.innerHTML=newMarkup;this.eyesGroup.setAttribute('opacity',expression?'0':'1');return}
   const old=this.glyphHost.firstElementChild;if(newMarkup){const wrap=document.createElementNS('http://www.w3.org/2000/svg','g');wrap.innerHTML=newMarkup;wrap.setAttribute('opacity','0');this.glyphHost.append(wrap);this.fade(wrap,1,.22).then(()=>{if(!this.dead)old?.remove()})}else if(old)this.fade(old,0,.22).then(()=>old.remove());
   this.fade(this.eyesGroup,expression?0:1,.22)
  }
  paintBlink(progress,index){
   const p=this.blinkParams?.[index];if(!p)return;
   const scale=1-(1-p.closedScale)*progress;
   this.blinkGroups[index].setAttribute('transform','translate('+p.cx.toFixed(3)+' '+p.cy.toFixed(3)+') scale(1 '+scale.toFixed(4)+') translate('+(-p.cx).toFixed(3)+' '+(-p.cy).toFixed(3)+')');
  }
  blinkEye(index,generation,offset=0,hold=.04){
   const p=this.blinkParams[index],variation=index===0?1:1.04;
   return new Promise(resolve=>this.timer(()=>{
    if(generation!==this.blinkGeneration||this.dead)return resolve();
    const close=this.track(animate(0,1,{duration:.09*variation,ease:t=>t*t*t,onUpdate:v=>{if(generation===this.blinkGeneration)this.paintBlink(v,index)}}));
    close.finished.then(()=>this.timer(()=>{
     if(generation!==this.blinkGeneration||this.dead)return resolve();
     const open=this.track(animate(1,0,{duration:.15/variation,ease:t=>1-(1-t)*(1-t)*(1-t),onUpdate:v=>{if(generation===this.blinkGeneration)this.paintBlink(v,index)}}));
     open.finished.then(()=>{if(generation===this.blinkGeneration){this.paintBlink(0,index)}resolve()}).catch(resolve);
    },hold*1000)).catch(resolve);
   },offset*1000));
  }
  resetBlink(){this.blinkGeneration++;this.blinking=false;this.blinkQueued=false;this.blinkScale=1;if(this.blinkParams)for(let i=0;i<2;i++)this.paintBlink(0,i);this.blinkParams=null;for(const eye of this.eyes){eye.style.removeProperty('stroke-width');eye.style.removeProperty('transition')}this.pendingEyes=null;this.pendingLook=null}
  transitionMotion(kind='pop'){
   if(quiet()||this.dead)return;
   this.transitionControl?.stop?.();
   const start=performance.now(),soft=kind==='sleeping',wake=kind==='waking';
   this.transitionControl=this.track(animate(0,1,{duration:soft?.55:wake?.72:.62,ease:'linear',onUpdate:t=>{
    let sx=1,sy=1,angle=0,dy=0;
    if(soft){const u=t*t*(3-2*t);sx=1-.07*u;sy=1-.13*u;dy=3*u}
    else if(t<.15){const u=t/.15;sx=1+.06*u;sy=1-.08*u;angle=(kind==='failed'?-3:2)*u}
    else{const u=(t-.15)/.85,envelope=Math.exp(-4*u),wave=Math.sin((wake?6:5)*Math.PI*u);sx=1+(.12*(1-u)+.05*wave*envelope);sy=1+(.12*(1-u)-.05*wave*envelope);angle=(kind==='failed'?6:3)*wave*envelope}
    this.body.setAttribute('transform','translate(50 52) translate(0 '+dy.toFixed(3)+') rotate('+angle.toFixed(3)+') scale('+sx.toFixed(4)+' '+sy.toFixed(4)+') translate(-50 -52)');
   }}));
   const control=this.transitionControl;control.finished.then(()=>{if(this.transitionControl===control)this.body.removeAttribute('transform')}).catch(()=>{});
  }
  tint(mood){return mood==='sleeping'?'#9EA7BB':mood==='angry'?'#FF6F59':this.traits.color}
  setColor(mood,instant=false){const to=this.tint(mood),from=this.shapePath.getAttribute('fill');if(instant)this.shapePath.setAttribute('fill',to);else this.track(animate(from,to,{duration:.3,onUpdate:v=>this.shapePath.setAttribute('fill',v)}));this.dotLeft.setAttribute('fill',to);this.dotRight.setAttribute('fill',to);this.bangStem.setAttribute('fill',to)}
  setStem(value){this.stemProgress=value;this.bangStem.setAttribute('d',stemPath(value))}
  animateStem(to,duration=.5){const from=this.stemProgress;const control=this.track(animate(from,to,{duration,ease:[.45,0,.2,1],onUpdate:v=>this.setStem(to>from?Math.min(1.1,v+.08*Math.sin(Math.PI*Math.max(0,Math.min(1,(v-from)/(to-from))))):v)}));return control.finished.catch(()=>{})}
  resetDecor(){this.dots.setAttribute('opacity','0');this.dotLeft.setAttribute('cx','50');this.dotRight.setAttribute('cx','50');this.dotLeft.setAttribute('r','0');this.dotRight.setAttribute('r','0');this.setStem(0);this.particles.setAttribute('opacity','0');this.notice.setAttribute('opacity','0');this.sleepScale=1;this.formAngle=0;this.bangAngle=0;this.stemProgress=0;this.faceScale=1;this.motion.removeAttribute('transform')}
  async leaveDecor(token){const tasks=[];if(Number(this.dots.getAttribute('opacity'))>0){const centerFrom=this.dotCenterRadius;tasks.push(this.track(animate(1,0,{duration:.3,ease:'easeInOut',onUpdate:t=>{this.dotCenterRadius=centerFrom+(5-centerFrom)*t+.55*Math.sin(Math.PI*t);this.setPath(pointsPath(circlePoints(this.dotCenterRadius)));this.dotLeft.setAttribute('cx',(50-18*t).toFixed(2));this.dotRight.setAttribute('cx',(50+18*t).toFixed(2));this.dotLeft.setAttribute('r',(4*t).toFixed(2));this.dotRight.setAttribute('r',(4*t).toFixed(2))}})).finished.catch(()=>{}));tasks.push(this.fade(this.dots,0,.3))}if(Math.abs(this.bangAngle)>.01)tasks.push(this.track(animate(this.bangAngle,0,{duration:.38,ease:'easeInOut',onUpdate:v=>{this.bangAngle=v;this.motion.setAttribute('transform','rotate('+v.toFixed(2)+' 50 52)')}})).finished.catch(()=>{}));if(this.stemProgress>0)tasks.push(this.animateStem(0,.38));if(Number(this.notice.getAttribute('opacity'))>0)tasks.push(this.fade(this.notice,0,.2));if(Number(this.particles.getAttribute('opacity'))>0)tasks.push(this.fade(this.particles,0,.2));await Promise.all(tasks);if(this.dead||token!==this.token)return false;this.resetDecor();this.lookTo(0,0,.3);return true}
  async toHome(token){await this.morphPath(this.paths.home,.5);if(this.dead||token!==this.token)return false;return true}
  async dotsMode(token){this.fade(this.eyesGroup,0,.25);this.fade(this.glyphHost,0,.25);const straighten={finished:Promise.resolve()};this.dots.setAttribute('opacity','1');const split=this.track(animate(0,1,{duration:.6,ease:'easeInOut',onUpdate:t=>{const emerge=Math.max(0,(t-.35)/.65);this.dotLeft.setAttribute('cx',(50-18*emerge).toFixed(2));this.dotRight.setAttribute('cx',(50+18*emerge).toFixed(2));const dotPop=(u,delay)=>{const v=Math.max(0,Math.min(1,(u-delay)/(1-delay)));return 4*v*(1+.18*Math.sin(Math.PI*v))};this.dotLeft.setAttribute('r',dotPop(emerge,0).toFixed(2));this.dotRight.setAttribute('r',dotPop(emerge,.12).toFixed(2))}}));await Promise.all([this.morph('dot',.6),split.finished.catch(()=>{}),straighten.finished.catch(()=>{})]);if(this.dead||token!==this.token)return;this.formAngle=0;this.motion.removeAttribute('transform');this.dotCenterRadius=5;const pulseStart=performance.now();this.track(animate(0,1,{duration:.9,repeat:Infinity,ease:'linear',onUpdate:t=>{const elapsed=Math.min(1,(performance.now()-pulseStart)/300),ramp=elapsed*elapsed*(3-2*elapsed),wave=phase=>ramp*Math.max(0,Math.sin(2*Math.PI*(t-phase))),left=wave(0),middle=wave(1/3),right=wave(2/3);this.dotLeft.setAttribute('r',(4+1.4*left).toFixed(2));this.dotRight.setAttribute('r',(4+1.4*right).toFixed(2));this.dotLeft.setAttribute('opacity',(1-.38*ramp+.38*left).toFixed(2));this.dotRight.setAttribute('opacity',(1-.38*ramp+.38*right).toFixed(2));this.dotCenterRadius=5+1.75*middle;this.setPath(pointsPath(circlePoints(this.dotCenterRadius)))}}));this.phase='settled';this.scheduleBusy()}
  async ballMode(token){if(!await this.toHome(token))return;this.setEyes('idle',null);this.lastFrame=0;this.phase='settled';this.scheduleBusy()}
  scheduleBusy(){if(this.dead||quiet()||!this.visible||this.signal!=='busy'||this.busyTimer)return;this.busyStartedAt=Date.now();this.busyTimer=setTimeout(()=>{this.busyTimer=null;this.busyRemaining=this.busyInterval;this.busyMode=this.busyMode==='dots'?'ball':'dots';this.startSignal()},this.busyRemaining)}
  pauseBusy(){if(this.busyTimer){clearTimeout(this.busyTimer);this.busyTimer=null;this.busyRemaining=Math.max(100,this.busyRemaining-(Date.now()-this.busyStartedAt))}}
  async startSignal(){const token=++this.token;this.stopControls();this.resetBlink();this.clearTimers();this.phase='turning';const signal=this.signal;this.setColor(this.state,quiet());const wakingFromSleep=this.sleepScale<.9&&signal!=='sleeping';if(quiet()||!this.visible){this.needsResume=true;this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,signal==='sleeping'?null:this.expression,true);if(quiet()&&signal==='sleeping')this.setSleepScale(.58);this.phase='settled';return}this.needsResume=false;if(wakingFromSleep){this.resetDecor();this.setSleepScale(.58);if(signal!=='starting'){const grow=this.track(animate(.58,1,{duration:.45,ease:'easeOut',onUpdate:v=>this.setSleepScale(v)}));await grow.finished.catch(()=>{});if(token!==this.token)return;this.sleepScale=1;this.motion.removeAttribute('transform')}}else if(this.stemProgress>0&&signal!=='asking'&&signal!=='failed'){const straight=this.track(animate(this.bangAngle,0,{duration:.65,ease:[.55,0,.2,1],onUpdate:v=>{this.bangAngle=v;this.motion.setAttribute('transform','rotate('+v.toFixed(2)+' 50 52)')}}));await Promise.all([this.animateStem(0,.45),straight.finished.catch(()=>{})]);if(token!==this.token)return;await this.morphPath(this.paths.home,.55);if(token!==this.token)return;this.resetDecor()}else if(signal==='busy'&&this.busyMode==='dots'&&Number(this.dots.getAttribute('opacity'))===0&&this.stemProgress===0){this.fade(this.notice,0,.25)}else if((signal==='asking'||signal==='failed')&&this.stemProgress===0&&Number(this.dots.getAttribute('opacity'))===0&&Number(this.notice.getAttribute('opacity'))===0&&Number(this.particles.getAttribute('opacity'))===0){this.lookTo(0,0,.3)}else if(!await this.leaveDecor(token))return;
   if(signal==='busy'){if(this.busyMode==='dots')await this.dotsMode(token);else await this.ballMode(token);return}
   if(signal==='sleeping'){this.fade(this.eyesGroup,0,.2);this.fade(this.glyphHost,0,.2);await this.morph('dot',.32);if(token!==this.token)return;const grow=this.track(animate(1,.58,{duration:.44,ease:'easeInOut',onUpdate:v=>this.setSleepScale(v)}));await Promise.all([this.morphPath(this.paths.home,.44),grow.finished.catch(()=>{})]);if(token!==this.token)return;this.setEyes('sleeping',null);this.lastFrame=0;this.phase='settled';return}
   if(signal==='asking'||signal==='failed'){
    this.fade(this.eyesGroup,0,.22);this.fade(this.glyphHost,0,.22);
    await this.morph('bangDot',.58);if(token!==this.token)return;
    await this.animateStem(1,.56);if(token!==this.token)return;
    const swayStart=performance.now();this.bangSway=this.track(animate(0,1,{duration:2.1,repeat:Infinity,ease:'linear',onUpdate:t=>{const amplitude=Math.min(1,(performance.now()-swayStart)/400);this.bangAngle=3*amplitude*Math.sin(2*Math.PI*t);this.motion.setAttribute('transform','rotate('+this.bangAngle.toFixed(2)+' 50 52)')}}));
    this.phase='settled';
    this.timer(async()=>{if(this.dead||this.signal!==signal)return;this.bangSway?.stop?.();this.phase='turning';const straighten=this.track(animate(this.bangAngle,0,{duration:.25,ease:'easeInOut',onUpdate:v=>{this.bangAngle=v;this.motion.setAttribute('transform','rotate('+v.toFixed(2)+' 50 52)')}}));await straighten.finished.catch(()=>{});if(this.dead||this.signal!==signal)return;this.motion.removeAttribute('transform');await this.animateStem(0,.45);if(this.dead||this.signal!==signal)return;await this.morphPath(this.paths.home,.56);if(this.dead||this.signal!==signal)return;this.setEyes(signal==='failed'?'angry':'curious',signal==='failed'?'error':null);this.lastFrame=0;this.phase='settled';this.timer(()=>{if(!this.dead&&this.signal===signal)this.setEyes('idle',null)},650);this.timer(()=>{if(!this.dead&&this.signal===signal)void this.startSignal()},1900)},1400);return
   }
   if(signal==='starting'){this.fade(this.eyesGroup,0,.18);this.fade(this.glyphHost,0,.18);if(!wakingFromSleep){await this.morph('dot',.25);if(token!==this.token)return}this.particles.setAttribute('opacity','1');const grow=wakingFromSleep?this.track(animate(.58,1,{duration:.5,ease:'easeOut',onUpdate:v=>this.setSleepScale(v)})).finished.catch(()=>{}):Promise.resolve();await Promise.all([this.morphPath(this.paths.home,.5),grow]);if(token!==this.token)return;this.sleepScale=1;this.motion.removeAttribute('transform');this.setEyes('waking','surprised');this.fade(this.particles,0,.4);this.lastFrame=0;this.phase='settled';this.timer(()=>{if(!this.dead&&this.signal==='starting')this.setEyes('idle',null)},650);return}
   await this.morphPath(this.paths.home,.4);if(token!==this.token)return;this.setEyes(signal==='unread'?'waking':'idle',signal==='unread'?'surprised':null);if(signal==='unread'){this.notice.setAttribute('r','0');this.track(animate(0,5,{duration:.28,ease:'easeOut',onUpdate:v=>this.notice.setAttribute('r',(v*(1+.2*Math.sin(Math.PI*v/5))).toFixed(2))}))}this.fade(this.notice,signal==='unread'?1:0,.25);this.lastFrame=0;this.phase='settled';
  }
  async comet(){if(this.dead||quiet()||!this.visible||this.cometActive)return false;this.cometActive=true;const token=++this.token;this.stopControls();this.transitionMotion('pop');this.clearTimers();this.phase='turning';if(!await this.leaveDecor(token))return false;this.fade(this.eyesGroup,0,.18);this.fade(this.glyphHost,0,.18);await this.morph('dot',.25);if(this.dead||token!==this.token)return false;const travel=this.track(animate(0,1,{duration:.75,ease:'easeInOut',onUpdate:t=>{const arc=Math.sin(Math.PI*t),x=74*arc,y=-11*arc;this.motion.setAttribute('transform','translate('+x.toFixed(2)+' '+y.toFixed(2)+')');this.cometTrail.setAttribute('opacity',arc.toFixed(3));this.cometTrail.setAttribute('stroke-dashoffset',(-100*t).toFixed(2))}}));await travel.finished.catch(()=>{});if(this.dead||token!==this.token)return false;this.motion.removeAttribute('transform');this.cometTrail.setAttribute('opacity','0');await this.morphPath(this.paths.home,.36);if(this.dead||token!==this.token)return false;this.cometActive=false;const pending=this.pendingPose;this.pendingPose=null;if(pending)this.jump(...pending);else void this.startSignal();return true}
  send(evt){const next=MACHINE[this.state]?.[evt];return next?this.jump(next):false}
  showExpression(expression){return this.jump(this.state,expression,this.signal)}
  jump(mood,expression=MOOD_GLYPH[mood]||null,signal=null){if(this.dead||!MOODS.includes(mood))return false;const next=signal||({working:'busy',waiting:'asking',happy:'unread',angry:'failed',sleeping:'sleeping',waking:'starting'}[mood]||'idle');if(this.cometActive){this.pendingPose=[mood,expression,next];return true}if(this.state===mood&&this.signal===next&&this.phase==='settled')return false;const old=this.signal,flowing=this.phase==='settled'&&((old==='idle'&&next==='busy'&&this.busyMode==='ball')||(old==='busy'&&this.busyMode==='ball'&&next==='idle'));this.state=mood;this.expression=expression;this.signal=next;this.svg.dataset.mood=mood;this.svg.dataset.signal=next;this.svg.setAttribute('aria-label',mood+' mascot');this.busyRemaining=this.busyInterval;if(flowing){this.resetBlink();this.updateFaceTransform();clearTimeout(this.busyTimer);this.busyTimer=null;this.setColor(mood);this.setEyes('idle',null);if(next==='busy')this.scheduleBusy()}else void this.startSignal();this.transitionMotion(next==='sleeping'?'sleeping':next==='starting'?'waking':next==='failed'?'failed':'pop');this.onMood?.(mood,this);return true}
  updateVisibility(){if(this.dead)return;if(quiet()||!this.visible){this.pauseBusy();for(const c of this.controls)c.pause?.();if(quiet()){this.needsResume=true;this.stopControls();this.resetBlink();this.clearTimers();this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,this.signal==='sleeping'?null:this.expression,true);if(this.signal==='sleeping')this.setSleepScale(.58);this.phase='settled'}}else{if(this.needsResume){void this.startSignal();return}for(const c of this.controls)c.play?.();if(this.signal==='busy'&&this.phase==='settled')this.scheduleBusy();this.scheduleBlink();this.scheduleGlance()}}
  blink(allowDouble=true,wink=false){
   if(this.dead||quiet()||!this.visible||this.signal!=='idle'||this.phase!=='settled')return false;
   if(this.blinking||this.eyeMomentActive||performance.now()<this.eyeMorphUntil||performance.now()<this.gazeUntil){
    if(!this.blinkQueued){this.blinkQueued=true;this.timer(()=>{this.blinkQueued=false;this.blink(allowDouble,wink)},180)}return false
   }
   this.blinking=true;const generation=++this.blinkGeneration;
   this.blinkDriftX=.8*Math.sin(this.breathPhase*.8+this.traits.phase);
   this.blinkDriftY=.45*Math.sin(this.breathPhase*.62+this.traits.phase);
   this.blinkParams=this.eyes.map((eye,i)=>{const g=this.tiltGroups[i],m=g.transform.baseVal.consolidate().matrix,a=eye.getPointAtLength(0),b=eye.getPointAtLength(eye.getTotalLength()),cx=(a.x+b.x)/2,cy=(a.y+b.y)/2;return {cx,cy,angle:Math.atan2(m.b,m.a)*180/Math.PI,closedScale:i===0?.08:.084}});
   for(const eye of this.eyes)eye.style.setProperty('transition','none');
   const jobs=wink?[this.blinkEye(1,generation,0,.25)]:[this.blinkEye(0,generation),this.blinkEye(1,generation,.025,.043)];
   Promise.all(jobs).then(()=>{if(generation!==this.blinkGeneration||this.dead)return;this.blinking=false;for(let i=0;i<2;i++){this.paintBlink(0,i);this.eyes[i].style.removeProperty('transition')}this.blinkParams=null;
    const eyes=this.pendingEyes,look=this.pendingLook;this.pendingEyes=null;this.pendingLook=null;if(eyes)this.setEyes(...eyes);if(look)this.lookTo(...look);
    if(allowDouble&&!wink&&hash(this.seed+Date.now())%5===0)this.timer(()=>this.blink(false),250)
   }).catch(()=>{});return true
  }
  scheduleBlink(){if(this.blinkTimer||this.dead)return;const delay=this.blinkedYet?2800+hash(this.seed+Date.now())%700:1400+hash(this.seed)%1300;this.blinkTimer=setTimeout(()=>{this.blinkTimer=null;if(!quiet()&&this.visible&&this.phase==='settled'&&this.signal==='idle'){this.blinkedYet=true;this.blink()}this.scheduleBlink()},delay)}
  scheduleGlance(){if(this.glanceTimer||this.dead)return;this.glanceTimer=setTimeout(()=>{this.glanceTimer=null;if(!quiet()&&this.visible&&this.phase==='settled'&&this.signal==='idle'){const n=hash(this.seed+Date.now());if(++this.glanceCount%3===0){this.lookTo(-7,-1,.42);this.timer(()=>this.lookTo(7,-1,.52),450);this.timer(()=>this.lookTo(0,0,.43),1100)}else{this.lookTo((n%5-2)*3.5,((n>>>4)%5-2)*1.15,.16);this.timer(()=>this.lookTo(0,0,.42),330)}}this.scheduleGlance()},800+hash(this.seed+Date.now())%1400)}
  scheduleExpression(){if(this.expressionTimer||this.dead||quiet()||!this.visible||this.signal!=='idle')return;const delay=6000+hash(this.seed+'expression'+this.expressionCount++)%6001;this.expressionTimer=setTimeout(()=>{this.expressionTimer=null;if(!this.dead&&!quiet()&&this.visible&&this.phase==='settled'&&this.signal==='idle'){const choices=['happy','wink','curious','excited'];const choice=choices[hash(this.seed+'moment'+this.expressionCount)%choices.length];this.eyeMomentActive=true;if(choice==='wink'){this.eyeMomentActive=false;this.blink(false,true);this.eyeMomentActive=true}else this.setEyes(choice,null);this.timer(()=>{if(!this.dead&&this.signal==='idle'){this.setEyes('idle',null);this.eyeMomentActive=false;this.scheduleExpression()}},850+hash(this.seed+'hold'+this.expressionCount)%450)}else this.scheduleExpression()},delay)}
  render(instant){if(instant){this.stopControls();this.setPath(this.paths.home);this.resetDecor();this.setEyes(this.state,this.signal==='sleeping'?null:this.expression,true);if(this.signal==='sleeping')this.setSleepScale(.58)}}
  destroy(){if(this.dead)return;this.dead=true;this.token++;this.stopControls();this.clearTimers();clearTimeout(this.blinkTimer);clearTimeout(this.glanceTimer);clearTimeout(this.expressionTimer);this.host.removeEventListener('pointermove',this.pointerMove);this.host.removeEventListener('pointerleave',this.pointerLeave);observer?.unobserve(this.svg);active.delete(this);this.svg.remove()}
 }
 global.VBoxMascot={Mascot,svg,miniSVG:(seed,mood='idle',expression=MOOD_GLYPH[mood]||null)=>svg(seed,mood,true,{},expression),setReducedMotion:value=>{forcedReduce=!!value;for(const mascot of active)mascot.updateVisibility()},traits,MACHINE,shapes:['circle'],colors:COLORS,moods:MOODS};
})(window);
