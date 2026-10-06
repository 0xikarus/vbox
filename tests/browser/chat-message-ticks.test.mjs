import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import path from 'node:path';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','vbox-logo.png','vbox-logo-dark.png','favicon.svg'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));

test('message ticks show sent, delivered, read, uncertain, failed, and live read updates',async()=>{
 const now=Date.now();
 const at=minutes=>new Date(now-minutes*60000).toISOString();
 const messages=[
  {id:'queued',text:'Queued · sent',state:'queued',createdAt:at(8),updatedAt:at(8)},
  {id:'delivering',text:'Delivering · sent',state:'delivering',createdAt:at(7),updatedAt:at(7)},
  {id:'delivered',text:'Delivered to box',state:'delivered',createdAt:at(6),updatedAt:at(6)},
  {id:'read',text:'Read by agent',state:'read',createdAt:at(5),updatedAt:at(5)},
  {id:'ambiguous',text:'Uncertain delivery',state:'ambiguous',createdAt:at(4),updatedAt:at(4)},
  {id:'failed',text:'Failed delivery',state:'failed',createdAt:at(3),updatedAt:at(3)},
  {id:'recent-ambiguous',text:'Checking delivery',state:'ambiguous',createdAt:at(0),updatedAt:at(0)},
 ].map(message=>({...message,direction:'user'}));
 const server=http.createServer((req,res)=>{
  const url=req.url.split('?')[0];
  if(url==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[url]){res.setHeader('Content-Type',url.endsWith('.css')?'text/css':url.endsWith('.png')?'image/png':url.endsWith('.svg')?'image/svg+xml':'text/javascript');return res.end(assets[url])}
  res.setHeader('Content-Type','application/json');
  if(url==='/v1/whoami')return res.end('{"role":"owner"}');
  if(url==='/v1/logical-boxes'||url==='/v1/grid-boxes')return res.end(JSON.stringify([{id:'builder',name:'Builder',state:'running',defaultAgent:'codex'}]));
  if(url==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify(messages));
  if(url==='/v1/tool-presets'||url==='/v1/agent-roles'||url==='/v1/box-conversations')return res.end('[]');
  if(url==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(url.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 let browser;
 try{
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
  const page=await browser.newPage();
  await page.setViewport({width:1440,height:900,deviceScaleFactor:1});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg.user .ticks').length===7);
  const ticks=()=>page.$$eval('#chat-messages .msg.user',rows=>Object.fromEntries(rows.map(row=>[row.dataset.messageId,{icon:row.querySelector('.ticks svg path')?.getAttribute('d'),className:row.querySelector('.ticks').className,title:row.querySelector('.ticks').title,text:row.querySelector('.ticks').textContent}])));
  const initial=await ticks();
  assert.equal(initial.queued.title,'Sent');
  assert.equal(initial.delivering.title,'Sent');
  assert.equal(initial.queued.icon,initial.delivering.icon);
  assert.equal(initial.delivered.title,'Delivered to agent');
  assert.equal(initial.read.title,'Read by agent');
  assert.match(initial.read.className,/read/);
  assert.equal(initial.delivered.icon,initial.read.icon);
  assert.match(initial.ambiguous.className,/uncertain/);
  assert.equal(initial.ambiguous.text,'');
  assert.equal(initial.failed.text,'failed');
  assert.match(initial.failed.className,/failed/);
  assert.equal(initial['recent-ambiguous'].title,'Sent');
  if(process.env.VMBOX_CAPTURE_DIR){
   await mkdir(process.env.VMBOX_CAPTURE_DIR,{recursive:true});
   for(const [width,theme] of [[390,'light'],[390,'dark'],[1440,'light']]){
    await page.setViewport({width,height:width===390?844:900,deviceScaleFactor:1});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    await new Promise(resolve=>setTimeout(resolve,500));
    await page.screenshot({path:path.join(process.env.VMBOX_CAPTURE_DIR,`message-ticks-${width}-${theme}.png`)});
   }
  }
  messages.find(message=>message.id==='delivered').state='read';
  messages.find(message=>message.id==='delivered').updatedAt=new Date().toISOString();
  await page.waitForFunction(()=>document.querySelector('[data-message-id="delivered"] .ticks')?.classList.contains('read'),{timeout:10000});
  assert.equal((await ticks()).delivered.title,'Read by agent');
  await page.$eval('[data-message-id="failed"] .msg-more',button=>button.click());
  await page.$eval('[data-message-id="failed"] .msg-actions-menu button:last-child',button=>button.click());
  assert.equal(await page.$eval('#chat-input',input=>input.value),'Failed delivery');
  assert.equal(await page.$('#chat-messages .msg.processing'),null);
  messages.splice(0,messages.length,{id:'latest-read',direction:'user',state:'read',text:'Agent picked this up',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()});
  await page.waitForFunction(()=>!!document.querySelector('#chat-messages .msg.processing'),{timeout:10000});
  await page.close();
 }finally{if(browser)await browser.close();await new Promise(resolve=>server.close(resolve))}
});
