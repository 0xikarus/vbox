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

test('a reply to a previously opened chat updates its unread notification after switching away',async()=>{
 const earlier=new Date(Date.now()-60000).toISOString();
 const alpha=[{id:'a1',direction:'agent',state:'delivered',text:'Earlier reply',createdAt:earlier,updatedAt:earlier}];
 const beta=[{id:'b1',direction:'agent',state:'delivered',text:'Other chat',createdAt:earlier,updatedAt:earlier}];
 const boxes=[{id:'alpha',name:'alpha',state:'running',defaultAgent:'claude'},{id:'beta',name:'beta',state:'running',defaultAgent:'claude'}];
 const server=http.createServer((req,res)=>{
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
  if(path==='/v1/logical-boxes/alpha/messages')return res.end(JSON.stringify(alpha));
  if(path==='/v1/logical-boxes/beta/messages')return res.end(JSON.stringify(beta));
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await page.waitForSelector('[data-box-id="alpha"]');
  await page.$eval('[data-box-id="alpha"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Earlier reply'));
  await page.$eval('[data-box-id="beta"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Other chat'));
  const now=new Date().toISOString();
  alpha.push({id:'mcp-1',direction:'system',state:'delivered',text:'MCP · get_contacts',createdAt:now,updatedAt:now});
  const mcpRefresh=page.waitForResponse(response=>response.url().includes('/v1/logical-boxes/alpha/messages'));
  await page.evaluate(()=>navigator.serviceWorker.dispatchEvent(new MessageEvent('message',{data:{type:'vmbox-push',box:'alpha'}})));
  await mcpRefresh;
  assert.equal(await page.$eval('[data-box-id="alpha"] .unread',badge=>badge.hidden),true,'MCP activity must not create an unread badge');
  alpha.push({id:'a2',direction:'agent',state:'delivered',text:'New reply after switching',createdAt:now,updatedAt:now});
  await page.evaluate(()=>navigator.serviceWorker.dispatchEvent(new MessageEvent('message',{data:{type:'vmbox-push',box:'alpha'}})));
  await page.waitForFunction(()=>{
   const row=document.querySelector('[data-box-id="alpha"]');
   return row&&!row.querySelector('.unread').hidden&&row.querySelector('.preview').textContent==='New reply after switching';
  });
  assert.equal(await page.$eval('[data-box-id="alpha"] .unread',badge=>badge.textContent),'1');
  await page.$eval('[data-box-id="alpha"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('[data-box-id="alpha"] .unread').hidden);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
