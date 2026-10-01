import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'primary',slotId:'slot-1',assignmentGeneration:3};
const profile={application:'claude',name:'work',snapshot:{windows:[{name:'session',usedPercent:22}]}};
const gib=1024**3;

test('Details resources show usage, reuse RAM settings, and poll only while open',async()=>{
 let memoryMiB=2048,memoryUsed=1.2*gib,diskUsed=1.4*gib,resourceGets=0,withUsage=true;
 const puts=[];
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"account-a"}');
   if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/profile-usage')return res.end(JSON.stringify({profiles:[profile]}));
   if(path==='/v1/logical-boxes/builder/imported-credentials')return res.end(JSON.stringify({profiles:[{application:'claude',name:'work'}]}));
   if(path==='/v1/logical-boxes/builder/resources'){
    if(req.method==='PUT'){
     const chunks=[];for await(const chunk of req)chunks.push(chunk);
     const value=JSON.parse(Buffer.concat(chunks).toString());puts.push(value);memoryMiB=value.memoryMiB;
     return res.end('{"message":"Live limits saved."}');
    }
    resourceGets++;
    if(!withUsage)return res.end(JSON.stringify({slotId:'slot-1',assignmentGeneration:3,resources:{cpu:1,memoryMiB,swapMiB:1024,diskGiB:2}}));
    return res.end(JSON.stringify({slotId:'slot-1',assignmentGeneration:3,resources:{cpu:1,memoryMiB,swapMiB:1024,diskGiB:2},memoryUsedBytes:memoryUsed,swapUsedBytes:.2*gib,diskUsedBytes:diskUsed,diskTotalBytes:2*gib,diskEnforced:false,diskObservedAt:new Date().toISOString(),observedAt:new Date().toISOString(),hostDiskUsedBytes:71*gib,hostDiskTotalBytes:100*gib}));
   }
   if(path==='/v1/fleet/host-resources')return res.end(JSON.stringify({memoryTotalBytes:8*gib,memoryAvailableBytes:4*gib,swapTotalBytes:4*gib,swapFreeBytes:3*gib,observedAt:new Date().toISOString()}));
   if(path==='/v1/logical-boxes/builder/messages')return res.end('[]');
   if(path==='/v1/chat-read-markers'||path==='/v1/chat-sidebar-layout')return res.end('{}');
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   if(path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/box-activity'||path==='/v1/provider-credentials'||path==='/v1/chat-commands')return res.end('[]');
   return res.end('{}');
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');return res.end(await readFile(resolve(web,file)))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const origin='http://127.0.0.1:'+server.address().port,captureDir=process.env.VMBOX_CAPTURE_DIR;
  if(captureDir)await mkdir(captureDir,{recursive:true});
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});
  await page.goto(origin+'/chat#box=builder');
  await page.waitForSelector('#chat-conversation:not([hidden])');
  assert.equal(await page.$('#chat-ram'),null,'RAM chip is absent from header');
  assert.equal(await page.$('#chat-disk'),null,'disk chip is absent from header');
  await page.waitForFunction(()=>document.querySelector('#chat-usage:not([hidden]) .chat-usage-value')?.textContent==='78%');
  await new Promise(resolve=>setTimeout(resolve,250));assert.equal(resourceGets,0,'closed Details does not fetch resources');
  await page.click('#chat-info');
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"] strong')?.textContent==='1.2 / 2 GB');
  assert.equal(await page.$eval('#inspect-resource-rows [data-kind="disk"] strong',node=>node.textContent),'1.4 / 2 GB');
  assert.match(await page.$eval('#inspect-resources-context',node=>node.textContent),/Host disk 71%/);
  assert.match(await page.$eval('#inspect-resources-updated',node=>node.textContent),/^Updated /);
  assert.match(await page.$eval('#inspect-resource-rows [data-kind="disk"]',node=>node.textContent),/Limit not enforced/);
  await page.click('#inspect-resources-adjust');
  await page.waitForFunction(()=>!document.querySelector('#inspect-resource-editor form').hidden);
  assert.equal(await page.$eval('#inspect-resource-editor [name="memory"]',node=>node.value),'2');
  await page.$eval('#inspect-resource-editor [name="memory"]',node=>{node.value='1';node.dispatchEvent(new Event('input',{bubbles:true}))});
  assert.match(await page.$eval('#inspect-resource-editor .memory-usage-warning',node=>node.textContent),/below current usage/);
  assert.equal(await page.$eval('#inspect-resource-editor form button',node=>node.disabled),true);
  await page.$eval('#inspect-resource-editor [name="memory"]',node=>{node.value='3';node.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.click('#inspect-resource-editor form button');
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"] strong')?.textContent==='1.2 / 3 GB');
  assert.equal(puts.length,1);assert.equal(puts[0].memoryMiB,3072);
  memoryUsed=2.7*gib;diskUsed=2.1*gib;
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"]')?.classList.contains('resource-warning')&&document.querySelector('#inspect-resource-rows [data-kind="disk"]')?.classList.contains('resource-warning'));
  assert.equal(await page.$eval('#inspect-resource-rows [data-kind="disk"]',node=>node.classList.contains('resource-danger')),false,'unenforced disk has no red alarm');
  memoryUsed=2.97*gib;
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"]')?.classList.contains('resource-danger'));
  await page.evaluate(()=>{const native=window.setTimeout.bind(window);window.setTimeout=(fn,delay,...args)=>native(fn,delay===30000?200:delay,...args);Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'))});
  const before=resourceGets;await new Promise(resolve=>setTimeout(resolve,550));assert.equal(resourceGets,before,'polling stops while document hidden');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:false});document.dispatchEvent(new Event('visibilitychange'))});
  for(let i=0;i<25&&resourceGets===before;i++)await new Promise(resolve=>setTimeout(resolve,40));
  assert.ok(resourceGets>before,'polling resumes with Details visible');
  const resumedAt=resourceGets;await new Promise(resolve=>setTimeout(resolve,450));assert.ok(resourceGets>resumedAt,'open Details polls again after the 30-second interval');
  await page.$eval('#inspect-close',node=>node.click());const closedAt=resourceGets;
  await new Promise(resolve=>setTimeout(resolve,550));assert.equal(resourceGets,closedAt,'polling stops when Details closes');
  memoryMiB=2048;withUsage=false;
  await page.click('#chat-info');
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"] strong')?.textContent==='– / 2 GB');
  assert.equal(await page.$eval('#inspect-resource-rows [data-kind="disk"] strong',node=>node.textContent),'– / 2 GB');
  assert.equal(await page.$eval('#inspect-resource-rows [data-kind="swap"] strong',node=>node.textContent),'– / 1 GB');
  assert.match(await page.$eval('#inspect-resources-updated',node=>node.textContent),/No live data/);
  await page.close();
  withUsage=true;memoryMiB=2048;memoryUsed=1.2*gib;diskUsed=1.4*gib;
  for(const theme of ['light','dark'])for(const width of [390,1440]){
   const shot=await browser.newPage();await shot.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await shot.goto(origin+'/chat#box=builder');await shot.waitForSelector('#chat-conversation:not([hidden])');
   await shot.waitForSelector('#chat-usage:not([hidden])');
   assert.ok(await shot.evaluate(()=>{const name=document.querySelector('#chat-header-name').getBoundingClientRect(),usage=document.querySelector('#chat-usage').getBoundingClientRect();return Math.abs(name.top-usage.top)<36}),'usage stays on the header row');
   await shot.click('#chat-info');
   await shot.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="disk"] strong')?.textContent==='1.4 / 2 GB');
   assert.ok(await shot.evaluate(width=>document.documentElement.scrollWidth<=width,width),'chat fits viewport');
   if(captureDir)await (await shot.$('#inspect')).screenshot({path:`${captureDir}/details-${width}-${theme}.png`});
   await shot.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
