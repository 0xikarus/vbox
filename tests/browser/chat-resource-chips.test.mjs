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

test('chat resource chips show usage, reuse RAM settings, and pause while hidden',async()=>{
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
  await page.waitForFunction(()=>document.querySelector('#chat-ram:not([hidden]) .chat-resource-value')?.textContent==='RAM 1.2/2 GB');
  assert.equal(await page.$eval('#chat-disk .chat-resource-value',node=>node.textContent),'Disk 1.4/2 GB');
  await page.waitForFunction(()=>document.querySelector('#chat-usage:not([hidden]) .chat-usage-value')?.textContent==='78%');
  assert.match(await page.$eval('#chat-disk',node=>node.title),/Host disk 71%.*Limit 2 GB is not enforced/);
  await page.click('#chat-ram');
  await page.waitForFunction(()=>!document.querySelector('#chat-resource-popover').hidden&&!document.querySelector('#chat-resource-popover form').hidden);
  assert.equal(await page.$eval('#chat-resource-popover #inspect-memory-settings form [name="memory"]',node=>node.value),'2','RAM popover reuses the Details form');
  await page.$eval('#chat-resource-popover [name="memory"]',node=>{node.value='1';node.dispatchEvent(new Event('input',{bubbles:true}))});
  assert.match(await page.$eval('#chat-resource-popover .memory-usage-warning',node=>node.textContent),/below current usage/);
  assert.equal(await page.$eval('#chat-resource-popover form button',node=>node.disabled),true);
  await page.$eval('#chat-resource-popover [name="memory"]',node=>{node.value='3';node.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.click('#chat-resource-popover form button');
  await page.waitForFunction(()=>document.querySelector('#chat-ram .chat-resource-value')?.textContent==='RAM 1.2/3 GB');
  assert.equal(puts.length,1);assert.equal(puts[0].memoryMiB,3072);
  await page.click('#chat-disk');
  assert.match(await page.$eval('#chat-resource-popover',node=>node.textContent),/Limit 2 GB is not enforced on this shared worker/);
  assert.equal(await page.$('#chat-resource-popover form'),null,'disk details have no resize action');
  await page.keyboard.press('Escape');
  memoryUsed=2.7*gib;diskUsed=2.1*gib;
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('#chat-ram')?.classList.contains('resource-warning')&&document.querySelector('#chat-disk')?.classList.contains('resource-warning'));
  assert.equal(await page.$eval('#chat-disk',node=>node.classList.contains('resource-danger')),false,'unenforced disk has no red alarm');
  memoryUsed=2.97*gib;
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('#chat-ram')?.classList.contains('resource-danger'));
  await page.evaluate(()=>{const native=window.setTimeout.bind(window);window.setTimeout=(fn,delay,...args)=>native(fn,delay===30000?200:delay,...args);Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'))});
  const before=resourceGets;await new Promise(resolve=>setTimeout(resolve,550));assert.equal(resourceGets,before,'resource polling stops while hidden');
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:false});document.dispatchEvent(new Event('visibilitychange'))});
  for(let i=0;i<25&&resourceGets===before;i++)await new Promise(resolve=>setTimeout(resolve,40));
  assert.ok(resourceGets>before,'resource polling resumes when visible');
  memoryMiB=2048;withUsage=false;
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('#chat-ram .chat-resource-value')?.textContent==='RAM –/2 GB');
  assert.equal(await page.$eval('#chat-disk .chat-resource-value',node=>node.textContent),'Disk –/2 GB','missing provider usage keeps the configured limit visible');
  await page.close();
  withUsage=true;memoryMiB=2048;memoryUsed=1.2*gib;diskUsed=1.4*gib;
  for(const theme of ['light','dark'])for(const width of [390,1440]){
   const shot=await browser.newPage();await shot.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await shot.goto(origin+'/chat#box=builder');
   await shot.waitForFunction(()=>document.querySelector('#chat-disk:not([hidden]) .chat-resource-value')?.textContent==='Disk 1.4/2 GB');
   assert.ok(await shot.evaluate(width=>document.documentElement.scrollWidth<=width,width),'chat fits viewport');
   if(captureDir){await shot.screenshot({path:`${captureDir}/chips-${width}-${theme}.png`});await shot.click('#chat-ram');await shot.waitForFunction(()=>!document.querySelector('#chat-resource-popover').hidden);await shot.screenshot({path:`${captureDir}/ram-popover-${width}-${theme}.png`})}
   await shot.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
