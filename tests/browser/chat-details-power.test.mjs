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
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent==='Hibernate after 4 h · No run limit · RAM 2 GB');
  assert.equal(await page.$eval('#inspect-power',fold=>fold.open),false,'power starts collapsed');
  await page.$eval('details[data-fold=technical]',fold=>fold.open=true);
  await page.waitForFunction(()=>!document.querySelector('#inspect-attachment-empty').hidden);
  assert.equal(await page.$eval('#inspect-attachment-empty',element=>element.textContent),'No attachments');
  assert.deepEqual(await page.$$eval('#inspect-config-actions button',buttons=>buttons.map(button=>button.textContent)),['Instructions…','Credentials…','Re-sync instructions','Restart…']);
  await page.$eval('#inspect-power',fold=>fold.open=true);
  await page.waitForFunction(()=>!document.querySelector('#inspect-idle-policy input[type=number]').disabled&&!document.querySelector('#inspect-run-budget-policy input[type=number]').disabled&&!document.querySelector('#inspect-memory-settings form').hidden);
  await page.$eval('#inspect-idle-policy input[type=number]',input=>{input.value='6'});
  await page.$eval('#inspect-idle-policy .idle-policy-controls button',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent.includes('Hibernate after 6 h'));
  await page.$eval('#inspect-run-budget-policy input[type=number]',input=>{input.value='2'});
  await page.$eval('#inspect-run-budget-policy .idle-policy-controls button',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent.includes('Run limit 2 h'));
  await page.$eval('#inspect-memory-settings [name=memory]',input=>{input.value='3';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.$eval('#inspect-memory-settings form',form=>form.requestSubmit());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent.includes('RAM 3 GB'));
  assert.deepEqual(writes.map(([kind])=>kind),['idle','run','resources']);
  assert.equal(writes[2][1].memoryMiB,3072);
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',input=>input.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent.includes('Hibernate off'));
  await page.$eval('#inspect-run-budget-policy input[type=number]',input=>{input.value='0'});
  await page.$eval('#inspect-run-budget-policy .idle-policy-controls button',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent==='Hibernate off · No run limit · RAM 3 GB');
  assert.deepEqual(writes.slice(-2).map(([kind,value])=>[kind,value]),[['idle',0],['run',0]]);
  await page.reload();
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  assert.equal(await page.$eval('#inspect-power',fold=>fold.open),true,'expanded state survives reload');
  await page.$eval('#inspect-power',fold=>fold.open=false);
  await page.reload();
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  assert.equal(await page.$eval('#inspect-power',fold=>fold.open),false,'collapsed state survives reload');
  await page.$eval('#inspect-resources-adjust',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-power').open&&document.activeElement===document.querySelector('#inspect-memory-settings'));
  await page.close();

  idleSeconds=14400;runSeconds=0;memoryMiB=2048;
  const captureDir=process.env.VMBOX_CAPTURE_DIR;
  if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const width of [390,1440])for(const theme of ['light','dark']){
   const shot=await browser.newPage();await shot.setViewport({width,height:width===390?844:1400,isMobile:width===390,hasTouch:width===390});
   await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await shot.goto(base+'/chat#box=builder');await shot.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
   await shot.$eval('#chat-info',button=>button.click());
   await shot.waitForFunction(()=>document.querySelector('#inspect-power-summary').textContent==='Hibernate after 4 h · No run limit · RAM 2 GB');
   await shot.$eval('#inspect-power',fold=>fold.open=false);
   if(captureDir)await capture(shot,`${captureDir}/details-power-${width}-${theme}-collapsed.png`);
   await shot.$eval('#inspect-power',fold=>fold.open=true);
   await shot.waitForFunction(()=>!document.querySelector('#inspect-memory-settings form').hidden);
   assert.ok(await shot.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'Details fits the viewport');
   if(width===390)assert.ok(await shot.evaluate(()=>{const bounds=document.querySelector('#inspect-power').getBoundingClientRect();return [...document.querySelectorAll('#inspect-power input,#inspect-power button')].filter(element=>element.getBoundingClientRect().width).every(element=>{const rect=element.getBoundingClientRect();return rect.left>=bounds.left-1&&rect.right<=bounds.right+1})}),'mobile fields stay inside the power section');
   if(captureDir)await capture(shot,`${captureDir}/details-power-${width}-${theme}-expanded.png`);
   await shot.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

async function readBody(request){const chunks=[];for await(const chunk of request)chunks.push(chunk);return Buffer.concat(chunks).toString()}
async function capture(page,path){
 await page.evaluate(()=>{const inspect=document.querySelector('#inspect');inspect.style.maxHeight='none';inspect.style.bottom='auto';inspect.style.height='auto';inspect.style.overflow='visible';document.querySelector('#inspect-body').style.overflow='visible';document.querySelector('#chat-main').style.overflow='visible';document.querySelector('#chat-conversation').style.overflow='visible'});
 await (await page.$('#inspect-power')).screenshot({path});
 await page.evaluate(()=>{for(const selector of ['#inspect','#inspect-body','#chat-main','#chat-conversation'])document.querySelector(selector).removeAttribute('style')});
}
