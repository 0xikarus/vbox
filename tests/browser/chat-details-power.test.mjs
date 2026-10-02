import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'primary',slotId:'slot-1',assignmentGeneration:3};
const gib=1024**3;

test('Details power section summarizes and saves policies, and Resources Adjust opens RAM controls',async()=>{
 let idleSeconds=14400,runSeconds=0,memoryMiB=2048,swapMiB=1024;
 const writes=[];
 const policy=()=>({seconds:runSeconds,state:'running',runningSince:new Date(Date.now()-65000).toISOString(),...(runSeconds?{remainingSeconds:runSeconds,deadlineAt:new Date(Date.now()+runSeconds*1000).toISOString()}:{})});
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner"}');
   if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/logical-boxes/builder/idle-policy'){
    if(req.method==='PUT'){const body=JSON.parse(await readBody(req));idleSeconds=body.seconds;writes.push(['idle',body.seconds])}
    return res.end(JSON.stringify({seconds:idleSeconds,resumeSeconds:14400}));
   }
   if(path==='/v1/logical-boxes/builder/run-budget-policy'){
    if(req.method==='PUT'){const body=JSON.parse(await readBody(req));runSeconds=body.seconds;writes.push(['run',body.seconds])}
    return res.end(JSON.stringify(policy()));
   }
   if(path==='/v1/logical-boxes/builder/run-budget-policy/adjust')return res.end(JSON.stringify(policy()));
   if(path==='/v1/logical-boxes/builder/resources'){
    if(req.method==='PUT'){const body=JSON.parse(await readBody(req));memoryMiB=body.memoryMiB;swapMiB=body.swapMiB;writes.push(['resources',body]);return res.end('{}')}
    return res.end(JSON.stringify({slotId:'slot-1',assignmentGeneration:3,resources:{cpu:1,memoryMiB,swapMiB,diskGiB:2},memoryUsedBytes:.7*gib,swapUsedBytes:.1*gib,diskUsedBytes:.3*gib,diskTotalBytes:2*gib,observedAt:new Date().toISOString()}));
   }
   if(path==='/v1/fleet/host-resources')return res.end(JSON.stringify({memoryTotalBytes:8*gib,memoryAvailableBytes:5*gib,swapTotalBytes:4*gib,swapFreeBytes:3*gib}));
   if(path==='/v1/logical-boxes/builder/attachment-storage')return res.end(JSON.stringify({boxBytes:0,boxCount:0,clearableCount:0,accountBytes:0,unusedBytes:0,unusedCount:0,limitBytes:gib}));
   if(path==='/v1/logical-boxes/builder/messages'||path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/box-activity'||path==='/v1/provider-credentials'||path==='/v1/chat-commands')return res.end('[]');
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');return res.end(await readFile(resolve(web,file)))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
  await page.click('#chat-info');
  await page.waitForSelector('[data-ip-row="power"]');
  assert.equal(await page.$eval('[data-ip-row="power"] .ip-row-value',element=>element.textContent),'4h idle · No run limit');
  await page.click('[data-ip-row="power"]');
  await page.waitForFunction(()=>!document.querySelector('[data-ip-page="power"]').hidden&&!document.querySelector('#inspect-idle-policy input[type=number]').disabled&&!document.querySelector('#inspect-run-budget-policy .idle-policy-switch input').disabled);
  await page.$eval('#inspect-idle-policy input[type=number]',input=>{input.value='6'});
  await page.click('#inspect-idle-policy .idle-policy-controls button');
  await page.waitForFunction(()=>document.querySelector('[data-ip-row="power"] .ip-row-value').textContent.includes('6h idle'));
  await page.$eval('#inspect-run-budget-policy .idle-policy-switch input',input=>input.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect-run-budget-policy input[type=number]').disabled);
  await page.$eval('#inspect-run-budget-policy input[type=number]',input=>{input.value='2'});
  await page.click('#inspect-run-budget-policy .idle-policy-controls button');
  await page.waitForFunction(()=>document.querySelector('[data-ip-row="power"] .ip-row-value').textContent.includes('2h run'));
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',input=>input.click());
  await page.waitForFunction(()=>document.querySelector('[data-ip-row="power"] .ip-row-value').textContent.includes('Idle off'));
  await page.$eval('#inspect-run-budget-policy .idle-policy-switch input',input=>input.click());
  await page.waitForFunction(()=>document.querySelector('[data-ip-row="power"] .ip-row-value').textContent==='Idle off · No run limit');
  assert.deepEqual(writes.map(([kind])=>kind),['idle','run','run','idle','run']);
  await page.click('#inspect-prototype-back');
  await page.waitForFunction(()=>!document.querySelector('#inspect-prototype-main').hidden);
  await page.click('#inspect-resources-adjust');
  await page.waitForFunction(()=>!document.querySelector('[data-ip-page="resources"]').hidden&&!document.querySelector('#inspect-memory-settings form').hidden);
  await page.$eval('#inspect-memory-settings [name=memory]',input=>{input.value='3';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.$eval('#inspect-memory-settings form',form=>form.requestSubmit());
  await page.waitForFunction(()=>document.querySelector('#inspect-resource-rows [data-kind="ram"] strong')?.textContent.endsWith('/ 3 GB'));
  assert.equal(writes.at(-1)[0],'resources');assert.equal(writes.at(-1)[1].memoryMiB,3072);
  await page.reload();await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.click('#chat-info');await page.click('[data-ip-row="power"]');
  assert.equal(await page.$eval('#inspect-title',element=>element.textContent),'Hibernation & limits');
  assert.equal(await page.$eval('#inspect-idle-policy .idle-policy-switch input',element=>element.checked),false);
  await page.close();

  const captureDir=process.env.VMBOX_CAPTURE_DIR;
  if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const width of [390,1440])for(const theme of ['light','dark']){
   const shot=await browser.newPage();await shot.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await shot.goto(base+'/chat#box=builder');await shot.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
   await shot.click('#chat-info');await shot.click('[data-ip-row="power"]');
   await shot.waitForFunction(()=>!document.querySelector('[data-ip-page="power"]').hidden);
   assert.ok(await shot.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Details fits the viewport');
   if(captureDir)await (await shot.$('#inspect')).screenshot({path:`${captureDir}/details-power-${width}-${theme}.png`});
   await shot.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

async function readBody(request){const chunks=[];for await(const chunk of request)chunks.push(chunk);return Buffer.concat(chunks).toString()}
