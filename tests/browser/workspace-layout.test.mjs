import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const html=await readFile(root+'workspace.html','utf8');
const pageHTML=html.replace(/<script src="\/xterm\.js"[\s\S]*$/, `<script>window.openWorkspaceTerminal=()=>()=>{};window.openWorkspaceDesktop=()=>()=>{};</script><script src="/mascot.js"></script><script src="/workspace.js"></script></html>`);

test('workspace layout, resource visibility polling, and chat navigation',async()=>{
 let resourceGets=0,messageGets=0;
 const errors=[];
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/boxes/test'){res.setHeader('Content-Type','text/html');return res.end(pageHTML)}
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner"}');
   if(path==='/v1/logical-boxes/test')return res.end('{"id":"test","name":"Test box","state":"running","provider":"shared-worker","defaultAgent":"claude","tools":[]}');
   if(path==='/v1/logical-boxes/test/sessions/interactive')return res.end('{"session":"s1"}');
   if(path==='/v1/logical-boxes/test/desktop')return res.end('{"enabled":false}');
   if(path==='/v1/logical-boxes/test/resources'){
    resourceGets++;
    return res.end(JSON.stringify({resources:{memoryMiB:8192,swapMiB:4096,diskGiB:40},memoryUsedBytes:3221225472,swapUsedBytes:1073741824,diskUsedBytes:12884901888,diskTotalBytes:42949672960,observedAt:new Date().toISOString()}));
   }
   if(path.endsWith('/messages')){messageGets++;return res.end('[]')}
   if(path.endsWith('/secret-requests'))return res.end('[]');
   return res.end('{}');
  }
  try{const file=await readFile(root+path);res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':'application/octet-stream');res.end(file)}
  catch{res.statusCode=404;res.end('')}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  page.on('pageerror',error=>errors.push(error.message));
  await page.evaluateOnNewDocument(()=>{
   const original=window.setTimeout.bind(window);
   window.setTimeout=(callback,delay,...args)=>original(callback,delay===30000?70:delay,...args);
  });
  await page.setViewport({width:390,height:900,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto('http://127.0.0.1:'+server.address().port+'/boxes/test');
  await page.waitForFunction(()=>!document.querySelector('#workspace').hidden,{timeout:8000});
  assert.equal(await page.$('#agent-chat'),null);
  assert.equal(await page.$('#agent-message-form'),null);
  assert.equal(await page.$eval('.workspace-top-chat',element=>element.getAttribute('href')),'/chat#box=test');
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.equal(messageGets,0,'Workspace does not poll chat messages');
  assert.equal(resourceGets,0,'Resources below the mobile viewport do not poll');
  const mobile=await page.evaluate(()=>['.viewer-card','[aria-label="Status"]','.workspace-resources','[aria-label="Power"]','[aria-label="More"]'].map(selector=>document.querySelector(selector).getBoundingClientRect().top));
  assert.ok(mobile.every((top,index)=>index===0||top>mobile[index-1]),'mobile order is viewer, Status, Resources, Power, More');
  await page.$eval('.workspace-resources',element=>element.scrollIntoView());
  await page.waitForFunction(()=>document.querySelector('[data-kind="ram"] strong')?.textContent==='3 / 8 GB',{timeout:8000});
  assert.deepEqual(await page.$$eval('#workspace-resource-rows strong',elements=>elements.map(element=>element.textContent)),['3 / 8 GB','1 / 4 GB','12 / 40 GB']);
  await page.waitForFunction(()=>document.querySelector('#workspace-resource-rows [data-kind="ram"] .inspect-resource-track span')?.style.width==='37.5%');
  for(let i=0;i<40&&resourceGets<2;i++)await new Promise(resolve=>setTimeout(resolve,50));
  assert.ok(resourceGets>=2,'visible card polls resources');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>true});document.dispatchEvent(new Event('visibilitychange'))});
  const hiddenCount=resourceGets;await new Promise(resolve=>setTimeout(resolve,250));
  assert.equal(resourceGets,hiddenCount,'hidden tabs stop resource polling');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});document.dispatchEvent(new Event('visibilitychange'))});
  await page.waitForFunction(()=>document.querySelector('#workspace-resource-rows strong')?.textContent==='3 / 8 GB');
  await new Promise(resolve=>setTimeout(resolve,150));
  assert.ok(resourceGets>hiddenCount,'visible tabs resume resource polling');
  await page.evaluate(()=>window.scrollTo(0,0));
  await new Promise(resolve=>setTimeout(resolve,120));
  const awayCount=resourceGets;await new Promise(resolve=>setTimeout(resolve,250));
  assert.equal(resourceGets,awayCount,'offscreen resources stop polling');
  for(const width of [1024,1440]){
   await page.setViewport({width,height:900,deviceScaleFactor:1,isMobile:false,hasTouch:false});
   const layout=await page.evaluate(()=>({viewer:document.querySelector('.viewer-card').getBoundingClientRect().toJSON(),side:document.querySelector('.workspace-side').getBoundingClientRect().toJSON(),status:document.querySelector('[aria-label="Status"]').getBoundingClientRect().toJSON(),resources:document.querySelector('.workspace-resources').getBoundingClientRect().toJSON()}));
   assert.ok(layout.viewer.right<layout.side.left,'viewer and compact side column remain adjacent at '+width);
   assert.ok(layout.viewer.width>layout.side.width,'viewer is wider at '+width);
   assert.ok(layout.resources.top>layout.status.top&&layout.resources.left===layout.status.left,'Resources is directly below Status');
  }
  await page.click('#workspace-resources-adjust');
  await page.waitForFunction(()=>document.querySelector('#box-settings').open&&!document.querySelector('#resource-form').hidden);
  assert.deepEqual(errors,[],'no Workspace JavaScript errors');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
