import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
const now=Date.now();
const messages=[
 {id:'m1',taskId:'task-1',direction:'agent',text:'Selectable agent prose that the owner may want to copy out of the transcript.',state:'delivered',createdAt:new Date(now-120000).toISOString(),updatedAt:new Date(now-120000).toISOString()},
 {id:'m2',taskId:'task-1',direction:'user',text:'Another selectable message with enough words for a double click.',state:'delivered',createdAt:new Date(now-60000).toISOString(),updatedAt:new Date(now-60000).toISOString()},
];

async function withChat(fn){
 const data={
  '/v1/whoami':{role:'owner',accountId:'acct'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],
  '/v1/box-activity':[],'/v1/box-conversations':[],'/v1/tool-presets':[],'/v1/chat-commands':[],
  '/v1/controller-defaults':{provider:'shared-worker',providerCredential:'pool'},'/v1/provider-credentials':[],
  ['/v1/logical-boxes/'+box.id+'/imported-credentials']:{profiles:[{application:'claude',name:'Studio',model:'Sonnet'}]},
  ['/v1/logical-boxes/'+box.id+'/contacts']:[],
  ['/v1/logical-boxes/'+box.id+'/idle-policy']:{seconds:10800},
  ['/v1/logical-boxes/'+box.id+'/attachment-storage']:{boxBytes:1024,boxCount:2,clearableCount:2,accountBytes:1024,limitBytes:1024**3},
  ['/v1/logical-boxes/'+box.id+'/resources']:{slotId:'slot-1',assignmentGeneration:3,resources:{cpu:2,memoryMiB:8192,swapMiB:2048,diskGiB:30},memoryUsedBytes:1024,swapUsedBytes:0,diskUsedBytes:1024,diskTotalBytes:30*1024**3,observedAt:new Date().toISOString()},
  '/v1/fleet/host-resources':{memoryAvailableBytes:1024,memoryTotalBytes:2048,swapFreeBytes:0,swapTotalBytes:0},
  '/v1/fleet/status':{slots:[{id:'slot-1',serviceName:'Worker 1',serviceId:'worker-1'}]},
 };
 const server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');
   if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
   if(path.endsWith('/messages'))return response.end(JSON.stringify(messages));
   return response.end(JSON.stringify(data[path]??{}));
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;return response.end()}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port)}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}
async function openTouch(browser,base){
 const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
 await page.goto(base+'/chat#box=builder');
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
 await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg .text').length>0);
 return page;
}
const anyMenuOpen=page=>page.evaluate(()=>!!document.querySelector('.msg-actions-menu:not([hidden]),#row-menu:not([hidden])'));
const selectionText=page=>page.evaluate(()=>getSelection()?.toString()||'');
const centerOf=async(page,selector)=>{const r=await page.$eval(selector,el=>{const b=el.getBoundingClientRect();return {x:b.left+b.width/2,y:b.top+b.height/2}});return r};

test('Touch: long-press on message text starts selection and opens no actions menu',async()=>{
 await withChat(async(browser,base)=>{
  const page=await openTouch(browser,base);
  const point=await centerOf(page,'#chat-messages .msg .text');
  await page.touchscreen.touchStart(point.x,point.y);
  await new Promise(r=>setTimeout(r,650));
  await page.touchscreen.touchEnd();
  assert.equal(await anyMenuOpen(page),false,'long-press on text must not open the actions menu');
  await page.close();
 });
});

test('Touch: an active text selection blocks the menu and horizontal swipes',async()=>{
 await withChat(async(browser,base)=>{
  const page=await openTouch(browser,base);
  // Open the menu, then simulate the native selection a long-press creates.
  await page.$eval('.msg .msg-more',b=>b.click());
  await page.waitForFunction(()=>!document.querySelector('.msg-actions-menu').hidden);
  await page.evaluate(()=>{const text=document.querySelector('#chat-messages .msg .text');const range=document.createRange();range.selectNodeContents(text);const selection=getSelection();selection.removeAllRanges();selection.addRange(range)});
  assert.ok((await selectionText(page)).length>0,'fixture selection is non-empty');
  // A stray tap must not auto-close the menu while selecting.
  await page.evaluate(()=>document.body.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true})));
  assert.equal(await anyMenuOpen(page),true,'a click while selecting must not auto-close the menu');
  await page.evaluate(()=>getSelection().removeAllRanges());
  await page.evaluate(()=>document.body.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true})));
  await page.waitForFunction(()=>document.querySelector('.msg-actions-menu').hidden);
  // A rightward drag over selected text must not navigate back or move the row.
  await page.evaluate(()=>{const text=document.querySelector('#chat-messages .msg .text');const range=document.createRange();range.selectNodeContents(text);const selection=getSelection();selection.removeAllRanges();selection.addRange(range)});
  const point=await centerOf(page,'#chat-messages .msg .text');
  await page.touchscreen.touchStart(point.x,point.y);
  await new Promise(r=>setTimeout(r,60));
  await page.touchscreen.touchMove(point.x+90,point.y+4);
  await new Promise(r=>setTimeout(r,60));
  await page.touchscreen.touchMove(point.x+190,point.y+8);
  await page.touchscreen.touchEnd();
  await new Promise(r=>setTimeout(r,350));
  assert.equal(await page.evaluate(()=>document.querySelector('#chat-app').classList.contains('in-chat')),true,'selection must not trigger the back swipe');
  assert.equal(await page.evaluate(()=>{const row=document.querySelector('#chat-messages .msg');const t=row.style.transform;return t===''||t==='translate3d(0,0,0)'}),true,'selection must not trigger the reply swipe');
  await page.close();
 });
});

test('Touch: long-press outside the text still opens the actions menu',async()=>{
 await withChat(async(browser,base)=>{
  const page=await openTouch(browser,base);
  const point=await page.$eval('#chat-messages .msg .meta',el=>{const b=el.getBoundingClientRect();return {x:b.left+b.width/2,y:b.top+b.height/2}});
  await page.touchscreen.touchStart(point.x,point.y);
  await new Promise(r=>setTimeout(r,650));
  await page.touchscreen.touchEnd();
  await page.waitForFunction(()=>!document.querySelector('.msg-actions-menu').hidden);
  assert.equal(await page.$eval('.msg-actions-menu',el=>el.textContent.includes('Copy')),true);
  await page.close();
 });
});

test('Desktop: drag-select and double-click select text without swipe, reply or menu',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});
  await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg .text').length>0);
  const boxRect=await page.$eval('#chat-messages .msg .text',el=>{const b=el.getBoundingClientRect();return {left:b.left,top:b.top,right:b.right,bottom:b.bottom}});
  const y=(boxRect.top+boxRect.bottom)/2;
  await page.mouse.move(boxRect.left+4,y);
  await page.mouse.down();
  await page.mouse.move((boxRect.left+boxRect.right)/2,y,{steps:6});
  await page.mouse.move(boxRect.right-4,y,{steps:6});
  await page.mouse.up();
  assert.ok((await selectionText(page)).length>0,'drag-select inside a bubble selects text');
  assert.equal(await anyMenuOpen(page),false,'drag-select must not open the actions menu');
  assert.equal(await page.evaluate(()=>{const row=document.querySelector('#chat-messages .msg');const t=row.style.transform;return t===''||t==='translate3d(0,0,0)'}),true,'drag-select must not start a swipe');
  // Double-click a word selects it.
  await page.evaluate(()=>getSelection().removeAllRanges());
  const word=await page.evaluate(()=>{const node=document.querySelector('#chat-messages .msg .text').firstChild;const range=document.createRange();range.setStart(node,0);range.setEnd(node,Math.min(6,node.length));const rect=range.getBoundingClientRect();return {x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
  await page.mouse.move(word.x,word.y);
  await page.mouse.down({clickCount:1});await page.mouse.up({clickCount:1});
  await page.mouse.down({clickCount:2});await page.mouse.up({clickCount:2});
  assert.ok((await selectionText(page)).trim().length>0,'double-click selects a word');
  await page.close();
 });
});
