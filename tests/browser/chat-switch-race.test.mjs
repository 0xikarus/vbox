import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

// Rapid switching must be stateful: a slow response from the box the user
// just left can never paint over the box they moved to.
test('a late response from the previous box cannot overwrite the new one',async()=>{
 const now=new Date().toISOString();
 const alpha=[{id:'a1',direction:'agent',state:'delivered',text:'ALPHA-ONLY',createdAt:now,updatedAt:now}];
 const beta=[{id:'b1',direction:'agent',state:'delivered',text:'BETA-ONLY',createdAt:now,updatedAt:now}];
 const boxes=[{id:'alpha',name:'alpha',state:'running',defaultAgent:'claude',provider:'railway'},{id:'beta',name:'beta',state:'running',defaultAgent:'claude',provider:'railway'}];
 const delay=ms=>new Promise(r=>setTimeout(r,ms));
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/alpha/messages'){await delay(500);return res.end(JSON.stringify(alpha))}
  if(path==='/v1/logical-boxes/beta/messages'){await delay(40);return res.end(JSON.stringify(beta))}
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.setViewport({width:420,height:820,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await p.waitForSelector('[data-box-id="alpha"]',{timeout:8000});
  // Start loading the slow box, then switch away before it answers.
  await p.$eval('[data-box-id="alpha"]',element=>element.click());
  await new Promise(r=>setTimeout(r,60));
  await p.$eval('[data-box-id="beta"]',element=>element.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('BETA-ONLY'),{timeout:8000});
  assert.equal(await p.$eval('#chat-messages',element=>element.textContent.includes('ALPHA-ONLY')),false,'previous transcript must clear on switch');
  // Let alpha's slow response land; it must not replace beta.
  await new Promise(r=>setTimeout(r,700));
  assert.equal(await p.$eval('#chat-messages',element=>element.textContent.includes('ALPHA-ONLY')),false,'late response from the previous box overwrote the view');
  assert.equal(await p.$eval('#chat-messages',element=>element.textContent.includes('BETA-ONLY')),true,'the new box must remain on screen');
  assert.equal(await p.$eval('#chat-header-name',element=>element.textContent),'beta');
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
