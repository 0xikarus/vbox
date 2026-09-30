import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const assets=Object.fromEntries(await Promise.all(['motion.js','mascot.js','mascot.css'].map(async name=>[name,await readFile(root+name)])));
const html='<!doctype html><html><head><link rel="stylesheet" href="/mascot.css"></head><body><div id="stage"></div><script src="/motion.js"></script><script src="/mascot.js"></script></body></html>';
const server=http.createServer((req,res)=>{
 const name=new URL(req.url,'http://localhost').pathname.slice(1);
 if(!name){res.setHeader('Content-Type','text/html');return res.end(html)}
 if(name in assets){res.setHeader('Content-Type',name.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[name])}
 res.statusCode=404;res.end();
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
const base='http://127.0.0.1:'+server.address().port;
async function openPage(){
 const page=await browser.newPage();await page.setViewport({width:900,height:650});
 await page.evaluateOnNewDocument(()=>{
  window.__mascotTest={pointerWindow:0,pointerOther:0,rafCalls:0,unobserved:0};
  const add=EventTarget.prototype.addEventListener;
  EventTarget.prototype.addEventListener=function(type,...args){if(type==='pointermove'){if(this===window)window.__mascotTest.pointerWindow++;else window.__mascotTest.pointerOther++}return add.call(this,type,...args)};
  const raf=window.requestAnimationFrame;
  window.requestAnimationFrame=function(callback){window.__mascotTest.rafCalls++;return raf.call(this,callback)};
  const unobserve=IntersectionObserver.prototype.unobserve;
  IntersectionObserver.prototype.unobserve=function(target){window.__mascotTest.unobserved++;return unobserve.call(this,target)};
 });
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 await page.goto(base);await page.waitForFunction(()=>!!window.VBoxMascot?.Mascot);
 return {page,errors};
}
try{
 await test('box hues are stable, distinct in the fixture set, and exclude account coral',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(()=>({colors:VBoxMascot.colors,fixtures:['builder','reviewer','designer','research'].map(name=>VBoxMascot.traits(name).color),repeat:VBoxMascot.traits('builder').color}));
   assert.equal(result.colors.length,8);
   assert.equal(new Set(result.colors.map(color=>color.toLowerCase())).size,8);
   assert.ok(!result.colors.some(color=>color.toLowerCase()==='#ff6f59'));
   assert.equal(new Set(result.fixtures).size,4);
   assert.equal(result.fixtures[0],result.repeat);
   assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('chat signals enter the expected body statechart states',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(()=>{
    const cases=[
     {events:['SLEEP'],state:'sleeping',signal:'sleeping',body:'hibernated'},
     {events:['WORK'],state:'working',signal:'busy',body:'busy'},
     {events:['SEND'],state:'waiting',signal:'asking',body:'asking'},
     {events:['WORK','DONE'],state:'happy',signal:'unread',body:'unread'},
     {events:['ERROR'],state:'angry',signal:'failed',body:'failed'},
     {events:['SLEEP','WAKE'],state:'waking',signal:'starting',body:'waking'}
    ];
    return cases.map((item,index)=>{
     const host=document.createElement('div');host.style.cssText='width:96px;height:96px;display:inline-block';document.querySelector('#stage').append(host);
     const mascot=new VBoxMascot.Mascot(host,'signal-'+index);
     const accepted=item.events.map(event=>mascot.send(event));
     const value={item,accepted,state:mascot.state,signal:mascot.signal,body:mascot.chart.body};mascot.destroy();return value;
    });
   });
   for(const row of result){assert.ok(row.accepted.every(Boolean),row.item.events.join(' → '));assert.equal(row.state,row.item.state);assert.equal(row.signal,row.item.signal);assert.ok(row.item.body==='busy'?row.body.startsWith('busy.'):row.body===row.item.body)}
   assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('mid-transition interrupts retarget from live values and settle at the final state',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(async()=>{
    const host=document.createElement('div');host.style.cssText='width:120px;height:120px';document.querySelector('#stage').append(host);
    const mascot=new VBoxMascot.Mascot(host,'interrupt');
    mascot.send('SEND');await new Promise(resolve=>setTimeout(resolve,70));
    mascot.send('WORK');await new Promise(resolve=>setTimeout(resolve,70));
    mascot.send('ERROR');await new Promise(resolve=>setTimeout(resolve,70));
    mascot.send('CALM');await new Promise(resolve=>setTimeout(resolve,1400));
    const value={state:mascot.state,signal:mascot.signal,body:mascot.chart.body,phase:mascot.phase};mascot.destroy();return value;
   });
   assert.deepEqual(result,{state:'idle',signal:'idle',body:'idle',phase:'settled'});
   assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('comet body motion finishes without decorative particles or trails',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(async()=>{
    const host=document.createElement('div');host.style.cssText='width:120px;height:120px';document.querySelector('#stage').append(host);
    const mascot=new VBoxMascot.Mascot(host,'comet');
    const decorations=host.querySelectorAll('.vbox-mascot-particles,.vbox-mascot-comet,.vbox-mascot-speed-lines').length;
    const completed=await mascot.comet();
    const value={decorations,completed,bodySegments:mascot.motionSegments.body,active:mascot.cometActive};mascot.destroy();return value;
   });
   assert.equal(result.decorations,0);assert.equal(result.completed,true);assert.ok(result.bodySegments>0);assert.equal(result.active,false);assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('destroy releases timers, controls, observation, and instance work',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(async()=>{
    const mascots=Array.from({length:8},(_,i)=>{const host=document.createElement('div');host.style.cssText='width:80px;height:80px;display:inline-block';document.querySelector('#stage').append(host);return new VBoxMascot.Mascot(host,'destroy-'+i)});
    mascots[0].send('WORK');mascots[1].send('SEND');
    mascots.forEach(mascot=>mascot.destroy());
    const before=window.__mascotTest.rafCalls;await new Promise(resolve=>setTimeout(resolve,150));
    return {allDead:mascots.every(m=>m.dead&&!m.svg.isConnected&&m.controls.size===0&&m.timers.length===0),unobserved:window.__mascotTest.unobserved,pointerWindow:window.__mascotTest.pointerWindow,pointerOther:window.__mascotTest.pointerOther,extraRaf:window.__mascotTest.rafCalls-before};
   });
   assert.equal(result.allDead,true);assert.equal(result.unobserved,8);assert.equal(result.pointerWindow,1);assert.equal(result.pointerOther,0);assert.ok(result.extraRaf<=1,JSON.stringify(result));
   assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('reduced motion and hidden/offscreen mascots stop the shared frame loop',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(async()=>{
    const stage=document.querySelector('#stage');
    const visibleHost=document.createElement('div');visibleHost.style.cssText='width:100px;height:100px';stage.append(visibleHost);
    const offscreenHost=document.createElement('div');offscreenHost.style.cssText='position:absolute;top:3000px;width:100px;height:100px';stage.append(offscreenHost);
    const visible=new VBoxMascot.Mascot(visibleHost,'visible'),offscreen=new VBoxMascot.Mascot(offscreenHost,'offscreen');
    await new Promise(resolve=>setTimeout(resolve,150));
    const offscreenPaused=!offscreen.visible&&offscreen.blinkTimer===null&&offscreen.glanceTimer===null;
    VBoxMascot.setReducedMotion(true);await new Promise(resolve=>setTimeout(resolve,150));
    const reducedStart=window.__mascotTest.rafCalls;await new Promise(resolve=>setTimeout(resolve,450));
    const reducedRaf=window.__mascotTest.rafCalls-reducedStart;
    VBoxMascot.setReducedMotion(false);await new Promise(resolve=>setTimeout(resolve,100));
    Object.defineProperty(document,'hidden',{configurable:true,get:()=>true});document.dispatchEvent(new Event('visibilitychange'));
    await new Promise(resolve=>setTimeout(resolve,80));
    const hiddenStart=window.__mascotTest.rafCalls;await new Promise(resolve=>setTimeout(resolve,150));
    const hiddenRaf=window.__mascotTest.rafCalls-hiddenStart;
    delete document.hidden;document.dispatchEvent(new Event('visibilitychange'));
    const resumeStart=window.__mascotTest.rafCalls;await new Promise(resolve=>setTimeout(resolve,120));
    const resumedRaf=window.__mascotTest.rafCalls-resumeStart;
    visible.destroy();offscreen.destroy();return {offscreenPaused,reducedRaf,hiddenRaf,resumedRaf};
   });
   assert.equal(result.offscreenPaused,true,JSON.stringify(result));assert.ok(result.reducedRaf<=1,JSON.stringify(result));assert.ok(result.hiddenRaf<=1,JSON.stringify(result));assert.ok(result.resumedRaf>=3,JSON.stringify(result));
   assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('one passive shared pointermove listener serves N instances',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(async()=>{
    const mascots=Array.from({length:12},(_,i)=>{const host=document.createElement('div');host.style.cssText='width:48px;height:48px;display:inline-block';document.querySelector('#stage').append(host);return new VBoxMascot.Mascot(host,'pointer-'+i)});
    window.dispatchEvent(new PointerEvent('pointermove',{pointerType:'mouse',clientX:400,clientY:100,bubbles:true}));
    await new Promise(resolve=>setTimeout(resolve,100));
    const value={windowListeners:window.__mascotTest.pointerWindow,otherListeners:window.__mascotTest.pointerOther,followers:mascots.filter(m=>m.cursorActive).length};mascots.forEach(m=>m.destroy());return value;
   });
   assert.equal(result.windowListeners,1);assert.equal(result.otherListeners,0);assert.ok(result.followers>=1,JSON.stringify(result));assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('eye outlines remain inside the body silhouette through gaze extremes',async()=>{
  const {page,errors}=await openPage();
  try{
   const result=await page.evaluate(()=>new Promise(resolve=>{
    const host=document.createElement('div');host.style.cssText='width:240px;height:240px;margin:120px';document.querySelector('#stage').append(host);
    const mascot=new VBoxMascot.Mascot(host,'silhouette');const failures=[];let frames=0;
    const targets=[[host.getBoundingClientRect().left-300,host.getBoundingClientRect().top-200],[host.getBoundingClientRect().right+300,host.getBoundingClientRect().bottom+200],[host.getBoundingClientRect().right+300,host.getBoundingClientRect().top-200],[host.getBoundingClientRect().left-300,host.getBoundingClientRect().bottom+200]];
    const sample=()=>{
     if(frames%22===0){const [x,y]=targets[Math.floor(frames/22)%targets.length];window.dispatchEvent(new PointerEvent('pointermove',{pointerType:'mouse',clientX:x,clientY:y,bubbles:true}))}
     if(Number(mascot.eyesGroup.getAttribute('opacity')||1)>.95){const shape=mascot.shapePath,fromScreen=shape.getScreenCTM().inverse();for(const eye of mascot.eyes){const toScreen=eye.getScreenCTM(),length=eye.getTotalLength();for(let i=0;i<24;i++){const point=eye.getPointAtLength(length*i/24).matrixTransform(toScreen).matrixTransform(fromScreen);if(!shape.isPointInFill(point))failures.push({frame:frames,eye:eye.classList.value,x:point.x,y:point.y})}}}
     if(++frames<96)requestAnimationFrame(sample);else{mascot.destroy();resolve({frames,failures:failures.slice(0,5)})}
    };requestAnimationFrame(sample)
   }));
   assert.equal(result.frames,96);assert.deepEqual(result.failures,[]);assert.deepEqual(errors,[]);
  }finally{await page.close()}
 });
 await test('chat renders static mascots when mascot and Motion assets fail to load',async()=>{
  const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex'};
  let serveMascot=false;
  const chatServer=http.createServer(async(req,res)=>{
   const path=new URL(req.url,'http://localhost').pathname;
   if(path==='/motion.js'||path==='/mascot.js'&&!serveMascot){res.statusCode=503;return res.end('unavailable')}
   if(path==='/v1/whoami'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({role:'owner'}))}
   if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify([box]))}
   if(path.startsWith('/v1/')){res.setHeader('Content-Type','application/json');return res.end(path.endsWith('/messages')||path==='/v1/tool-presets'?'[]':'{}')}
   const name=path==='/chat'?'chat.html':path.slice(1);
   if(!/^[\w.-]+$/.test(name)){res.statusCode=404;return res.end()}
   try{const body=await readFile(root+name);res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':name.endsWith('.html')?'text/html':'application/octet-stream');res.end(body)}
   catch{res.statusCode=404;res.end()}
  });
  await new Promise(resolve=>chatServer.listen(0,'127.0.0.1',resolve));
  const errors=[];
  try{
   for(const mascotAvailable of [false,true]){
    serveMascot=mascotAvailable;
    const page=await browser.newPage();page.on('pageerror',error=>errors.push(error.message));
    try{
     await page.goto('http://127.0.0.1:'+chatServer.address().port+'/chat');
     await page.waitForSelector('[data-avatar="builder"] svg.vbox-mascot');
     const result=await page.evaluate(()=>{const avatar=document.querySelector('[data-avatar="builder"] svg');if(window.VBoxMascot)avatar.__vboxMascot.jump('sleeping');return {name:document.querySelector('#chat-entries')?.textContent,shape:!!avatar.querySelector('circle'),eyes:avatar.querySelectorAll('ellipse').length,eyeHeight:avatar.querySelector('ellipse')?.getAttribute('ry'),motion:!!window.Motion,mascot:!!window.VBoxMascot}});
     assert.match(result.name,/Builder/);assert.equal(result.shape,true);assert.equal(result.eyes,2);assert.equal(result.motion,false);assert.equal(result.mascot,mascotAvailable);if(mascotAvailable)assert.equal(result.eyeHeight,'1.5');assert.deepEqual(errors,[]);
    }finally{await page.close()}
   }
  }finally{await new Promise(resolve=>chatServer.close(resolve))}
 });
}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
