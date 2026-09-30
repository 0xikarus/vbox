import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const motionJS=await readFile('internal/controller/web/motion.js','utf8');
const mascotJS=await readFile('internal/controller/web/mascot.js','utf8');
const mascotCSS=await readFile('internal/controller/web/mascot.css','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const tokensCSS=await readFile('internal/controller/web/vbox-tokens.css','utf8');
const vboxCSS=await readFile('internal/controller/web/vbox-c.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

// A running box uses its desktop thumbnail when available and keeps its
// generated mascot visible when the thumbnail is missing or invalid.
test('running boxes show their desktop thumbnail in the list and navbar',async()=>{
 const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const now=new Date().toISOString();
 const boxes=[{id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'railway',role:'owner'},{id:'ghost',name:'ghost',state:'running',defaultAgent:'claude',provider:'railway',role:'worker'},{id:'bad',name:'bad',state:'running',defaultAgent:'codex',provider:'railway',role:'worker'},{id:'corrupt',name:'corrupt',state:'running',defaultAgent:'codex',provider:'railway',role:'worker'}];
 const messages=[{id:'m1',direction:'agent',state:'delivered',text:'Ready.',createdAt:now,updatedAt:now}];
 let thumbnails=0,corruptRequests=0;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/motion.js'){res.setHeader('Content-Type','text/javascript');return res.end(motionJS)}
  if(path==='/mascot.js'){res.setHeader('Content-Type','text/javascript');return res.end(mascotJS)}
  if(path==='/mascot.css'){res.setHeader('Content-Type','text/css');return res.end(mascotCSS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/vbox-tokens.css'){res.setHeader('Content-Type','text/css');return res.end(tokensCSS)}
  if(path==='/vbox-c.css'){res.setHeader('Content-Type','text/css');return res.end(vboxCSS)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  if(path.endsWith('/desktop/screenshot')){
   if(path.includes('/logical-boxes/ghost/')){res.statusCode=409;return res.end('{}')}
   if(path.includes('/logical-boxes/bad/')){res.setHeader('Content-Type','application/json');return res.end('{}')}
   if(path.includes('/logical-boxes/corrupt/')){corruptRequests++;res.setHeader('Content-Type','image/png');return res.end('not a PNG')}
   thumbnails++;
   if(req.url.includes('thumbnail=true')){res.setHeader('Content-Type','image/png');return res.end(png)}
   res.statusCode=404;return res.end('{}');
  }
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'||path==='/v1/logical-boxes/ghost/messages'||path==='/v1/logical-boxes/bad/messages'||path==='/v1/logical-boxes/corrupt/messages')return res.end(JSON.stringify(messages));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-app').hidden,{timeout:8000});
  // list avatar gets the thumbnail image
  await p.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="builder"] img')?.naturalWidth>=1,{timeout:8000});
  // selecting a box builds the navbar avatar and it also gets the thumbnail
  await p.waitForFunction(()=>document.querySelector('#chat-header-avatar [data-avatar="builder"] img')?.naturalWidth>=1,{timeout:8000});
  assert.ok(thumbnails>=1,'the controller must have served a desktop thumbnail');
  assert.equal(await p.$eval('#chat-header-name',element=>element.textContent),'builder');
  // Missing and invalid thumbnails keep the generated face visible.
  await p.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="ghost"] .avatar-mascot svg'),{timeout:5000});
  await p.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="bad"] .avatar-mascot svg'),{timeout:5000});
  await p.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="corrupt"] .avatar-mascot svg'),{timeout:5000});
  for(let attempt=0;attempt<40&&!corruptRequests;attempt++)await new Promise(resolve=>setTimeout(resolve,50));
  assert.ok(corruptRequests,'the invalid image request was made');
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.equal(await p.$eval('#chat-entries [data-avatar="ghost"]',element=>!!element.querySelector('img')),false,'a missing thumbnail must not show a stale image');
  assert.equal(await p.$eval('#chat-entries [data-avatar="bad"]',element=>!!element.querySelector('img')),false,'an invalid thumbnail must not show a broken image');
  assert.equal(await p.$eval('#chat-entries [data-avatar="corrupt"]',element=>!!element.querySelector('img')),false,'an undecodable image must fall back to the generated face');
  await p.click('#chat-back');
  await p.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await new Promise(resolve=>setTimeout(resolve,350));
  await p.screenshot({path:'/tmp/vmbox-avatar-fallback-mobile.png'});
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
