const api=window.VBoxMascot,all=[];
function add(host,seed,mood,expression,signal){const m=new api.Mascot(host,seed);all.push(m);if(mood!=='idle')m.jump(mood,expression,signal);return m}
const hero=add(document.querySelector('#hero'),'vbox-hero','idle');
const states=[['idle','idle',null,'idle'],['busy dots','working','focused','busy'],['asking','waiting','surprised','asking'],['unread','happy','surprised','unread'],['failed','angry','error','failed'],['hibernated','sleeping','sleeping','sleeping'],['starting','waking','surprised','starting']];
for(const [label,mood,expression,signal] of states){const button=document.createElement('button');button.textContent=label;button.onclick=()=>{hero.busyMode='dots';hero.jump(mood,expression,signal)};document.querySelector('#controls').append(button)}
const ballButton=document.createElement('button');ballButton.textContent='busy scanning ball';ballButton.onclick=()=>{hero.busyMode='ball';if(hero.signal==='busy')void hero.startSignal();else hero.jump('working','focused','busy')};document.querySelector('#controls').append(ballButton);
for(const glyph of ['happy','surprised','error','sleeping','focused']){const card=document.createElement('article'),pair=document.createElement('div'),large=document.createElement('div'),small=document.createElement('div'),label=document.createElement('small');pair.className='glyph-pair';large.className='glyph-large';small.className='glyph-small';large.innerHTML=api.svg('glyph-demo','idle',false,{},glyph);small.innerHTML=api.svg('glyph-demo','idle',true,{},glyph);label.textContent=glyph;pair.append(large,small);card.append(pair,label);document.querySelector('#glyphs').append(card)}
for(const [label,mood,expression,signal] of states){const card=document.createElement('article'),pair=document.createElement('div'),large=document.createElement('div'),small=document.createElement('div'),caption=document.createElement('small');pair.className='glyph-pair';large.className='glyph-large';small.className='glyph-small';pair.append(large,small);card.append(pair);caption.textContent=label;card.append(caption);document.querySelector('#props').append(card);add(large,'state-'+label,mood,expression,signal);add(small,'state-'+label,mood,expression,signal);small.querySelector('svg')?.classList.add('vbox-mascot-mini')}
for(let i=0;i<24;i++){const card=document.createElement('article');card.className='card';const host=document.createElement('div');host.className='host';card.append(host);const name=document.createElement('small');name.textContent='box '+String(i+1).padStart(2,'0');card.append(name);document.querySelector('#grid').append(card);add(host,'box-'+i,'idle')}
for(let i=0;i<12;i++){const host=document.createElement('div');host.className='host';document.querySelector('#small').append(host);add(host,'box-'+i,'idle')}
for(const [label,mood,expression,signal] of states){const card=document.createElement('article'),host=document.createElement('div');host.className='host';card.append(host,label);document.querySelector('#moods').append(card);add(host,'mood-'+label,mood,expression,signal)}
document.querySelector('#reduce').onchange=e=>{api.setReducedMotion(e.target.checked);for(const m of all)m.render(true)};
// A deterministic 60-second interrupt test that samples every rendered frame.
const fuzzButton=document.createElement('button');
fuzzButton.textContent='Fuzz statechart (60 s)';
document.querySelector('#controls').append(fuzzButton);
const fuzzReport=document.createElement('pre');
fuzzReport.id='fuzz-report';
fuzzReport.setAttribute('aria-live','polite');
document.querySelector('#controls').after(fuzzReport);

fuzzButton.onclick=async()=>{
 if(fuzzButton.disabled)return;
 fuzzButton.disabled=true;
 hero.host.scrollIntoView({block:'center'});
 hero.visible=true;
 hero.updateVisibility();
 await new Promise(resolve=>setTimeout(resolve,250));
 let rng=0x6A09E667,eventsFired=0;
 const random=()=>((rng=Math.imul(rng,1664525)+1013904223|0)>>>0)/4294967296;
 const events=['WORK','SEND','REPLY','DONE','PRAISE','ERROR','SLEEP','WAKE','READY','STOP','CALM','SETTLE'];
 const start=performance.now(),samples=[];
 const box=node=>{const r=node.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height}};
 const opacity=node=>Number(getComputedStyle(node).opacity);
 const radius=node=>Number(node.getAttribute('r')||0);
 const scale=node=>{const m=node.transform.baseVal.consolidate()?.matrix;return {x:m?Math.hypot(m.a,m.b):1,y:m?Math.hypot(m.c,m.d):1}};
 const tilt=node=>{const m=node.transform.baseVal.consolidate()?.matrix;return Math.atan2(m?.b||0,m?.a||1)*180/Math.PI};
 const fire=setInterval(()=>{hero.send(events[Math.floor(random()*events.length)]);eventsFired++},120);
 const capture=now=>{
  const body=box(hero.shapePath),left=box(hero.eyes[0]),right=box(hero.eyes[1]);
  const leftModel=hero.eyeDimensions(0),rightModel=hero.eyeDimensions(1);
  const bodyScale=scale(hero.body),motionScale=scale(hero.motion);
  const rgb=(getComputedStyle(hero.shapePath).fill.match(/\d+(?:\.\d+)?/g)||[0,0,0]).map(Number);
  const fillOpacity=Number(getComputedStyle(hero.shapePath).fillOpacity);
  const particleNodes=[...hero.particles.children];
  const particleOpacity=opacity(hero.particles);
  const particleAlphas=particleNodes.map(node=>particleOpacity*opacity(node));
  samples.push({
   t:now-start,state:hero.signal,phase:hero.phase,
   bodySegment:hero.motionSegments.body,eyeSegment:hero.motionSegments.eyes,
   gazeSegment:hero.motionSegments.gaze,dotsSegment:hero.motionSegments.dots,
   symbolSegment:hero.motionSegments.symbol,colorSegment:hero.motionSegments.color,
   bodyX:body.x,bodyY:body.y,bodyW:body.w,bodyH:body.h,
   bodyOpacity:opacity(hero.body),shapeOpacity:opacity(hero.shapePath),
   bodyFillOpacity:fillOpacity*opacity(hero.body)*opacity(hero.shapePath),
   bodyFillR:rgb[0],bodyFillG:rgb[1],bodyFillB:rgb[2],
   bodyScaleX:bodyScale.x,bodyScaleY:bodyScale.y,
   motionScaleX:motionScale.x,motionScaleY:motionScale.y,
   eyeLeftX:left.x,eyeLeftY:left.y,eyeLeftW:left.w,eyeLeftH:left.h,
   eyeLeftModelW:leftModel.w,eyeLeftModelH:leftModel.h,
   eyeLeftOpacity:opacity(hero.eyes[0]),eyeLeftTilt:tilt(hero.tiltGroups[0]),
   eyeLeftLidOpacity:opacity(hero.lids[0]),
   eyeRightX:right.x,eyeRightY:right.y,eyeRightW:right.w,eyeRightH:right.h,
   eyeRightModelW:rightModel.w,eyeRightModelH:rightModel.h,
   eyeRightOpacity:opacity(hero.eyes[1]),eyeRightTilt:tilt(hero.tiltGroups[1]),
   eyeRightLidOpacity:opacity(hero.lids[1]),eyesOpacity:opacity(hero.eyesGroup),
   dotsLeftR:radius(hero.dotLeft),dotsRightR:radius(hero.dotRight),
   dotsOpacity:opacity(hero.dots),dotsLeftOpacity:opacity(hero.dotLeft),dotsRightOpacity:opacity(hero.dotRight),
   stemH:box(hero.bangStem).h,noticeScale:hero.noticeScale,
   noticeOpacity:opacity(hero.notice),particlesOpacity:opacity(hero.particles),
   particleCount:particleNodes.length,
   particleEffectiveCount:particleAlphas.reduce((sum,value)=>sum+value,0),
   particle0Opacity:particleAlphas[0]||0,particle1Opacity:particleAlphas[1]||0,
   particle2Opacity:particleAlphas[2]||0,particle3Opacity:particleAlphas[3]||0,
   colorR:rgb[0],colorG:rgb[1],colorB:rgb[2],dim:opacity(hero.shapePath)
  });
  if(now-start<60000){requestAnimationFrame(capture);return}
  clearInterval(fire);
  const keys=Object.keys(samples[0]).filter(key=>typeof samples[0][key]==='number'&&key!=='t'&&!key.endsWith('Segment'));
  const flags=[];
  const region=key=>key.startsWith('bodyFillR')||key.startsWith('bodyFillG')||key.startsWith('bodyFillB')?'colorSegment':
   key.startsWith('body')||key.startsWith('motion')?'bodySegment':
   key.startsWith('eye')?key.endsWith('X')||key.endsWith('Y')?'gazeSegment':'eyeSegment':
   key.startsWith('dots')?'dotsSegment':key.startsWith('color')?'colorSegment':'symbolSegment';
  for(const key of keys){
   const delta=samples.slice(1).map((sample,i)=>Math.abs(sample[key]-samples[i][key])*
    Math.min(1,16.667/Math.max(1,sample.t-samples[i].t)));
   const group=region(key),opacityOrScale=/Opacity|Scale|dim/.test(key),pixelFloor=opacityOrScale?.03:1.5;
   for(let i=0;i<delta.length;i++){
    const old=samples[i][key],value=samples[i+1][key];
    if(opacityOrScale&&((old===0&&value>.3)||(old>.3&&value===0))){
     flags.push(`${(samples[i+1].t/1000).toFixed(2)}s ${key}: ${old.toFixed(3)} → ${value.toFixed(3)}`);continue
    }
    if(key.startsWith('eye')&&!key.includes('Opacity')&&samples[i].eyesOpacity<.01&&samples[i+1].eyesOpacity<.01)continue;
    if(samples[i][group]!==samples[i+1][group])continue;
    const nearby=[];
    for(let j=Math.max(0,i-3);j<Math.min(delta.length,i+4);j++)
     if(j!==i&&delta[j]>1e-4&&samples[j][group]===samples[i][group]&&samples[j+1][group]===samples[i][group])nearby.push(delta[j]);
    nearby.sort((a,b)=>a-b);
    const active=nearby.slice(Math.floor(nearby.length/2));
    if(active.length<3)continue; // rapid interrupt: too few frames to establish a segment median
    const median=active[Math.floor(active.length/2)]||0;
    if(delta[i]>Math.max(3*median,pixelFloor))flags.push(`${(samples[i+1].t/1000).toFixed(2)}s ${key}: ${old.toFixed(3)} → ${value.toFixed(3)}`);
   }
  }
  window.__mascotFuzzSamples=samples;
  window.__mascotFuzzFlags=flags;
  fuzzReport.textContent=`${samples.length} rendered frames, ${eventsFired} events, ${flags.length} flags\n${flags.slice(0,60).join('\n')}`;
  fuzzButton.disabled=false;
 };
 requestAnimationFrame(capture);
};
