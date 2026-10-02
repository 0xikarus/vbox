import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=new Map(await Promise.all(['chat.html','chat.js','chat.css','vbox-c.css','vbox-tokens.css','app.css','motion.js','mascot.js','mascot.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name)])));
const boxes=['A','B'].map(id=>({id,name:id,state:'running',defaultAgent:'codex',provider:'railway',role:'worker',volumeName:'volume-'+id}));
const at=new Date(Date.now()-60_000).toISOString();
const messages=id=>[{id:id+'-message',direction:'agent',state:'delivered',text:'Message from '+id,createdAt:at,updatedAt:at}];

test('touch swipes and long press never activate the row or link beneath the finger',async()=>{
 const readWrites=[];
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0],asset=path==='/chat'?'chat.html':path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',asset.endsWith('.html')?'text/html':asset.endsWith('.css')?'text/css':'text/javascript');return res.end(assets.get(asset))}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-conversations'||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/chat-read-markers'){
   if(req.method==='GET')return res.end('{}');
   let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{readWrites.push(JSON.parse(body));res.end('{}')});return;
  }
  const match=path.match(/^\/v1\/logical-boxes\/([AB])\/messages$/);
  if(match)return res.end(JSON.stringify(messages(match[1])));
  if(path.endsWith('/messages'))return res.end('[]');
  res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();
  await page.evaluateOnNewDocument(()=>{
   window.inputEvents=[];
   window.addEventListener('click',event=>window.inputEvents.push({kind:'click',trusted:event.isTrusted,target:event.target.closest?.('[data-box-id]')?.dataset.boxId||event.target.id}),true);
   window.addEventListener('pointerdown',event=>window.inputEvents.push({kind:'pointer',type:event.pointerType}),true);
  });
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});await page.emulateCPUThrottling(4);
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=A');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Message from A'));
  await page.$eval('#chat-back',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  const point=await page.$eval('[data-box-id="B"] .chat-meta',el=>{const r=el.getBoundingClientRect();return {x:120,y:r.top+r.height/2}});
  const swipe=async(xs)=>{
   await page.touchscreen.touchStart(xs[0],point.y);
   assert.equal(await page.evaluate(()=>location.hash),'','touchstart alone leaves the active chat unchanged');
   assert.equal(await page.$eval('#chat-header-name',el=>el.textContent),'A');
   assert.equal(await page.evaluate(()=>JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}').B),undefined,'touchstart does not mark B read');
   for(const x of xs.slice(1))await page.touchscreen.touchMove(x,point.y+2);
   await page.touchscreen.touchEnd();
  };
  const lateClick=async selector=>page.$eval(selector,el=>el.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,detail:1})));

  await swipe([200,120,20]);
  await lateClick('[data-box-id="B"] .chat-meta'); // Some phones synthesize this click after touchend.
  await page.waitForFunction(()=>location.hash==='#box=A'&&document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await page.evaluate(()=>JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}').B),undefined,'B remains unread after forward swipe');
  assert.equal(readWrites.some(batch=>batch['box:B']),false,'B read marker is unchanged');
  assert.equal(await page.$eval('#chat-header-name',el=>el.textContent),'A');

  await page.$eval('#chat-back',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await swipe([120,99,89]);
  await lateClick('[data-box-id="B"] .chat-meta');
  await new Promise(resolve=>setTimeout(resolve,500));
  assert.equal(await page.evaluate(()=>location.hash),'','snap-back keeps the list and URL');
  assert.equal(await page.$eval('#chat-header-name',el=>el.textContent),'A','snap-back keeps A active');
  assert.equal(await page.evaluate(()=>JSON.parse(localStorage.getItem('vmboxChatSeen')||'{}').B),undefined);

  await page.touchscreen.tap(point.x,point.y);
  await page.waitForFunction(()=>location.hash==='#box=B'&&document.querySelector('#chat-messages').textContent.includes('Message from B'));
  assert.ok(await page.evaluate(()=>window.inputEvents.some(event=>event.kind==='click'&&event.trusted&&event.target==='B')),'a real clean tap emits a click and opens B');
  assert.ok(await page.evaluate(()=>window.inputEvents.some(event=>event.kind==='pointer'&&event.type==='touch')),'touchscreen also emits pointer events');

  // A back swipe begun on a link must consume its eventual click as well.
  const link=await page.evaluate(()=>{
   const a=document.createElement('a');a.id='swipe-link-probe';a.href='#unexpected';a.textContent='Swipe link';
   a.style.cssText='display:block;padding:24px';a.addEventListener('click',()=>{window.linkClicks=(window.linkClicks||0)+1});
   const transcript=document.querySelector('#chat-messages');transcript.append(a);transcript.scrollTop=transcript.scrollHeight;const r=a.getBoundingClientRect();return {x:r.left+25,y:r.top+r.height/2};
  });
  await page.touchscreen.touchStart(link.x,link.y);await page.touchscreen.touchMove(link.x+90,link.y+2);await page.touchscreen.touchMove(link.x+190,link.y+2);await page.touchscreen.touchEnd();
  await lateClick('#swipe-link-probe');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await page.evaluate(()=>window.linkClicks||0),0,'back swipe does not activate the link');
  assert.equal(await page.evaluate(()=>location.hash),'');

  await new Promise(resolve=>setTimeout(resolve,400));
  const longPoint=await page.$eval('[data-box-id="B"] .chat-meta',el=>{const r=el.getBoundingClientRect();return {x:120,y:r.top+r.height/2}});
  await page.touchscreen.touchStart(longPoint.x,longPoint.y);await new Promise(resolve=>setTimeout(resolve,650));await page.touchscreen.touchEnd();
  await lateClick('[data-box-id="B"] .chat-meta');
  assert.equal(await page.evaluate(()=>location.hash),'','long press does not open its row');
  assert.equal(await page.$eval('#row-menu',el=>el.hidden),false,'long press still opens the menu');
  assert.ok(await page.evaluate(()=>window.inputEvents.filter(event=>event.kind==='click').length)>=3,'capture log observed synthetic click attempts');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
