import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const now=new Date().toISOString();
const boxes=[
 {id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'shared-worker',slotId:'s1',createdAt:now,updatedAt:now},
 {id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex',provider:'shared-worker',slotId:'s2',createdAt:now,updatedAt:now},
];
const novnc=`window.fakeRFB=[];window.fakeFailNext=false;window.NoVNC={default:class {
 constructor(root,url){this.root=root;this.url=url;this.handlers={};this.viewOnly=false;this.scaleViewport=false;this.resizeSession=true;this.closed=false;window.fakeRFB.push(this);root.innerHTML='<div class="fake-live" style="width:100%;height:100%;background:#101b2d;color:#9cf6a9;font:16px monospace;padding:20px;text-align:left">builder@vbox $ go test ./...<br>ok &nbsp; internal/api &nbsp; 0.8s<br>$ _</div>';const fail=window.fakeFailNext;window.fakeFailNext=false;setTimeout(()=>this.emit(fail?'disconnect':'connect',{clean:false}),30)}
 addEventListener(type,fn){(this.handlers[type]??=[]).push(fn)}
 emit(type,detail={}){for(const fn of this.handlers[type]||[])fn({detail})}
 disconnect(){this.closed=true}
}};`;
const shot='<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400"><rect width="640" height="400" fill="#223756"/><rect x="50" y="60" width="540" height="280" rx="12" fill="#0c1624"/><text x="90" y="140" fill="#9cf6a9" font-family="monospace" font-size="25">$ go test ./...</text></svg>';

test('Details hero keeps one view-only desktop, tears it down, and preserves its thumbnail on failure',async()=>{
 let enabled=true;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(await readFile(resolve(web,'chat.html')))}
  if(path==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(novnc)}
  if(path.startsWith('/v1/')){
   if(path.endsWith('/desktop/screenshot')){res.setHeader('Content-Type','image/svg+xml');return res.end(shot)}
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner',accountId:'acct'}));
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/notifications'||path==='/v1/chat-commands')return res.end('[]');
   if(path.endsWith('/desktop'))return res.end(JSON.stringify({enabled}));
   if(path.endsWith('/messages'))return res.end(JSON.stringify([{id:'m1',direction:'agent',state:'delivered',text:'Ready.',createdAt:now,updatedAt:now}]));
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const file=resolve(web,path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  await page.waitForSelector('#chat-info');await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  assert.equal(await page.evaluate(()=>window.fakeRFB.length),0,'closed Details does not connect');
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-screen .inspect-screen-frame').classList.contains('is-live'));
  assert.deepEqual(await page.evaluate(()=>({count:window.fakeRFB.length,viewOnly:window.fakeRFB[0].viewOnly,scaled:window.fakeRFB[0].scaleViewport,chip:document.querySelector('#inspect-screen-live-chip').hidden,controls:document.querySelector('#inspect-screen-live button')?.length||0})),{count:1,viewOnly:true,scaled:true,chip:false,controls:0});
  assert.equal(await page.$eval('#inspect-screen-image',image=>image.hidden),false,'thumbnail remains mounted under the live view');
  await page.screenshot({path:'/tmp/vbox-hero-live-desktop.png'});
  await page.$eval('#inspect-close',button=>button.click());
  assert.equal(await page.evaluate(()=>window.fakeRFB[0].closed),true,'closing Details disconnects');
  await page.$eval('#chat-info',button=>button.click());await page.waitForFunction(()=>window.fakeRFB.length===2);
  await page.evaluate(()=>{location.hash='#box=reviewer'});
  await page.waitForFunction(()=>window.fakeRFB.length===3);
  assert.equal(await page.evaluate(()=>window.fakeRFB[1].closed),true,'switching boxes closes the previous stream');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>true});document.dispatchEvent(new Event('visibilitychange'))});
  assert.equal(await page.evaluate(()=>window.fakeRFB[2].closed),true,'hidden tabs disconnect');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});document.dispatchEvent(new Event('visibilitychange'))});
  await page.waitForFunction(()=>window.fakeRFB.length===4);
  await page.evaluate(()=>{window.fakeFailNext=true;document.querySelector('#inspect-close').click();document.querySelector('#chat-info').click()});
  await page.waitForFunction(()=>window.fakeRFB.length===5&&document.querySelector('#inspect-screen-hint').textContent.includes('unavailable'));
  assert.equal(await page.$eval('#inspect-screen-image',image=>image.hidden),false,'failed live view retains thumbnail');
  assert.equal(await page.$eval('#inspect-screen-live-chip',chip=>chip.hidden),true);
  await page.screenshot({path:'/tmp/vbox-hero-fallback-desktop.png'});
  await page.waitForFunction(()=>window.fakeRFB.length===6&&document.querySelector('#inspect-screen .inspect-screen-frame').classList.contains('is-live'),{timeout:3000});
  assert.equal(await page.evaluate(()=>window.fakeRFB.filter(rfb=>!rfb.closed).length),1,'backoff reconnect keeps a single live hero stream');
  await page.$eval('#inspect-close',button=>button.click());
  enabled=false;await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-screen-off').textContent==='Desktop off');
  assert.equal(await page.evaluate(()=>window.fakeRFB.length),6,'disabled desktop never attaches');
  assert.equal(await page.$eval('#inspect-screen-image',image=>image.hidden),true,'disabled desktop shows the styled off state');
  await page.$eval('#inspect-close',button=>button.click());
  enabled=true;await page.$eval('#chat-info',button=>button.click());await page.waitForFunction(()=>window.fakeRFB.length===7);
  boxes[1].state='hibernated';await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>window.fakeRFB[6].closed&&document.querySelector('#inspect-screen-off').textContent==='Desktop off');
  boxes[1].state='running';
  await page.close();
  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  const frames=process.env.VBOX_HERO_GIF_FRAMES;let frame=0;
  if(frames)await mkdir(frames,{recursive:true});
  async function gifShots(count){if(!frames)return;for(let i=0;i<count;i++)await mobile.screenshot({path:`${frames}/frame-${String(frame++).padStart(3,'0')}.png`})}
  await mobile.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  await mobile.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await mobile.$eval('#chat-info',button=>button.click());
  await mobile.waitForFunction(()=>document.querySelector('#inspect-screen .inspect-screen-frame').classList.contains('is-live'));
  await mobile.screenshot({path:'/tmp/vbox-hero-live-mobile.png'});
  await gifShots(16);
  await mobile.evaluate(()=>{window.fakeFailNext=true;document.querySelector('#inspect-close').click();document.querySelector('#chat-info').click()});
  await mobile.waitForFunction(()=>document.querySelector('#inspect-screen-hint').textContent.includes('unavailable'));
  await mobile.screenshot({path:'/tmp/vbox-hero-fallback-mobile.png'});
  await gifShots(16);
  if(frames){await mobile.waitForFunction(()=>document.querySelector('#inspect-screen .inspect-screen-frame').classList.contains('is-live'),{timeout:3000});await gifShots(16)}
  await mobile.$eval('#inspect-screen',screen=>screen.click());
  await mobile.waitForFunction(()=>!document.querySelector('#takeover').hidden);
  assert.equal(await mobile.$eval('#takeover-title',title=>title.textContent),'builder','the hero opens the full desktop takeover for this box');
  await mobile.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
