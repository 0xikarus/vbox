import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3,processing:true};
const gib=1024**3;

test('Details drawer uses grouped rows, subpages and the credentials sheet',async()=>{
 let runSeconds=28800,contextClears=0;
 const server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');
   if(path==='/v1/logical-boxes/builder/run-budget-policy'){
    if(request.method==='PUT')runSeconds=JSON.parse(await readBody(request)).seconds;
    return response.end(JSON.stringify({seconds:runSeconds,state:'running',runningSince:new Date(Date.now()-90*60000).toISOString(),...(runSeconds?{deadlineAt:new Date(Date.now()+runSeconds*1000).toISOString()}:{})}));
   }
   if(path==='/v1/logical-boxes/builder/messages/clear-context'){
    contextClears++;return response.end(JSON.stringify({agent:'claude'}));
   }
   const data={
    '/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],
    '/v1/box-activity':[], '/v1/box-conversations':[], '/v1/tool-presets':[],
    '/v1/logical-boxes/builder/messages':[],
    '/v1/logical-boxes/builder/imported-credentials':{profiles:[{application:'claude',name:'Studio profile',model:'Sonnet',reasoningEffort:'high'}]},
    '/v1/logical-boxes/builder/contacts':[],
    '/v1/login-profiles':[{application:'claude',name:'Studio profile'}],
    '/v1/logical-boxes/builder/idle-policy':{seconds:10800},
    '/v1/logical-boxes/builder/attachment-storage':{boxBytes:1024,boxCount:2,clearableCount:2,accountBytes:1024,limitBytes:gib},
    '/v1/logical-boxes/builder/resources':{slotId:'slot-1',assignmentGeneration:3,resources:{cpu:2,memoryMiB:8192,swapMiB:2048,diskGiB:30},memoryUsedBytes:5*gib,swapUsedBytes:.3*gib,diskUsedBytes:12*gib,diskTotalBytes:30*gib,observedAt:new Date().toISOString()},
    '/v1/fleet/host-resources':{memoryAvailableBytes:12*gib,memoryTotalBytes:32*gib,swapFreeBytes:4*gib,swapTotalBytes:8*gib},
    '/v1/fleet/status':{slots:[{id:'slot-1',serviceName:'Worker 1',serviceId:'worker-1'}]},
   };
   if(path==='/v1/push/vapid-key')response.statusCode=404;
   return response.end(JSON.stringify(data[path]??{}));
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;return response.end()}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  for(const width of [360,390,1440]){
   const page=await browser.newPage();await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});
   await page.goto(base+'/chat#box=builder');
   await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
   await page.$eval('#chat-info',button=>button.click());
   await page.waitForFunction(()=>document.querySelector('[data-ip-row="power"] .ip-row-value').textContent.includes('3h idle'));
   await page.waitForFunction(()=>/^(Working|Running) · \d+h/.test(document.querySelector('#ip-overview .ip-overview-cell:nth-child(2) .ip-cell-value').textContent));
   assert.equal(await page.$$eval('#ip-settings .ip-row',rows=>rows.length),7);
   assert.deepEqual(await page.$$eval('#ip-overview .ip-cell-label',labels=>labels.map(label=>label.textContent)),['Model','Status','Worker','Profile']);
   assert.equal(await page.$$eval('#ip-danger .ip-row',rows=>rows.length),3);
   assert.ok(await page.$$eval('#ip-danger .ip-row',rows=>rows.every(row=>!row.querySelector('.ip-row-chevron'))));
   assert.ok(await page.evaluate(()=>{const actions=document.querySelector('#inspect-actions').getBoundingClientRect(),drawer=document.querySelector('#inspect').getBoundingClientRect();return Math.abs((actions.left+actions.right-drawer.left-drawer.right)/2)<2}));
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'drawer fits '+width+'px viewport');
   assert.equal(await page.$eval('#inspect-prototype-main',node=>node.hidden),false);
   assert.equal(await page.$eval('#inspect-idle-policy',node=>node.closest('#inspect-prototype-page')!==null),true);
   if(width<600)assert.deepEqual(await page.evaluate(()=>['#inspect-close','#inspect-resources-adjust'].flatMap(selector=>{
    const rect=document.querySelector(selector).getBoundingClientRect();return rect.width>=40&&rect.height>=40?[]:[`${selector}: ${rect.width}×${rect.height}`];
   })),[],'phone main-page controls have 40px hit areas');
   await page.$eval('[data-ip-row="power"]',button=>button.click());
   assert.equal(await page.$eval('#inspect-prototype-page',node=>node.hidden),false);
   assert.equal(await page.$eval('#inspect-prototype-main',node=>node.hidden),true);
   assert.equal(await page.$eval('#inspect-title',node=>node.textContent),'Hibernation & limits');
   assert.equal(await page.evaluate(()=>document.activeElement?.id),'inspect-prototype-back','opening a subpage focuses Back');
   await page.waitForFunction(()=>!document.querySelector('#inspect-run-budget-policy .idle-policy-switch input').disabled);
   if(width<600)assert.deepEqual(await page.evaluate(()=>[
    '#inspect-prototype-back','#inspect-idle-policy .idle-policy-switch','#inspect-run-budget-policy .idle-policy-switch',
    '#inspect-idle-policy .ip-unit-field input','#inspect-run-budget-policy .ip-unit-field input',
    '#inspect-idle-policy .idle-policy-controls:not(.run-budget-actions)>button',
    '#inspect-run-budget-policy .idle-policy-controls:not(.run-budget-actions)>button',
    '#inspect-run-budget-policy .run-budget-actions button',
   ].flatMap(selector=>[...document.querySelectorAll(selector)].flatMap(node=>{
    const rect=node.getBoundingClientRect();return rect.width>=40&&rect.height>=40?[]:[`${selector}: ${rect.width}×${rect.height}`];
   }))),[],'phone policy controls have 40px hit areas');
   assert.equal(await page.$eval('#inspect-run-budget-policy .idle-policy-switch input',input=>input.checked),true);
   await page.$eval('#inspect-run-budget-policy .idle-policy-switch input',input=>input.click());
   await page.waitForFunction(()=>document.querySelector('#inspect-run-budget-policy .idle-policy-badge').textContent==='Off');
   assert.equal(runSeconds,0);
   await page.$eval('#inspect-run-budget-policy .idle-policy-switch input',input=>input.click());
   await page.waitForFunction(()=>document.querySelector('#inspect-run-budget-policy .idle-policy-badge').textContent==='On');
   assert.equal(runSeconds,28800);
   await page.$eval('#inspect-prototype-back',button=>button.click());
   assert.equal(await page.evaluate(()=>document.activeElement?.dataset.ipRow),'power','Back restores focus to the opening row');
   await page.$eval('#inspect-resources-adjust',button=>button.click());
   assert.equal(await page.evaluate(()=>document.activeElement?.id),'inspect-prototype-back');
   await page.$eval('#inspect-prototype-back',button=>button.click());
   assert.equal(await page.evaluate(()=>document.activeElement?.id),'inspect-resources-adjust','Back restores focus to Adjust');
   await page.$eval('[data-ip-row="instructions"]',button=>button.click());
   assert.equal(await page.$eval('#ip-resync-instructions',button=>button.hidden),false);
   await page.$eval('#inspect-prototype-back',button=>button.click());
   await page.$eval('[data-ip-row="technical"]',button=>button.click());
   await page.waitForFunction(()=>document.querySelectorAll('#ip-technical-table .ip-technical-row').length>0);
   assert.ok(await page.$$eval('#ip-technical-table .ip-technical-row',rows=>rows.every(row=>row.querySelectorAll('span').length===2&&(row.classList.contains('ip-technical-copy')===!!row.querySelector('svg')))));
   assert.ok(await page.$eval('#ip-technical-table .ip-technical-row',row=>row.classList.contains('ip-technical-copy')));
   await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.copiedDetail=value}}}));
   await page.$eval('#ip-technical-table .ip-technical-copy',row=>row.click());
   await page.waitForFunction(()=>typeof window.copiedDetail==='string');
   assert.equal(await page.evaluate(()=>window.copiedDetail),await page.$eval('#ip-technical-table .ip-technical-copy span:nth-child(2)',value=>value.textContent));
   await page.$eval('#inspect-prototype-back',button=>button.click());
   await page.$eval('[data-ip-row="credentials"]',button=>button.click());
   await page.$eval('.ip-page[data-ip-page="credentials"] .ip-page-action',button=>button.click());
   await page.waitForFunction(()=>!document.querySelector('#box-credentials-modal').hidden&&!!document.querySelector('#box-credentials-form select'));
   assert.equal(await page.$eval('#box-credentials-form select',select=>select.value),JSON.stringify({application:'claude',name:'Studio profile'}));
   await page.close();
  }
  const normal=await browser.newPage();await normal.goto(base+'/chat#box=builder');
  await normal.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await normal.$eval('#chat-info',button=>button.click());
  assert.equal(await normal.$eval('#inspect-prototype',node=>node.hidden),false);
  normal.once('dialog',dialog=>dialog.dismiss());
  await normal.click('[data-ip-row="context"]');
  assert.equal(contextClears,0,'cancelling Clear context keeps the session');
  normal.once('dialog',dialog=>dialog.accept());
  await normal.click('[data-ip-row="context"]');
  await normal.waitForFunction(()=>document.querySelector('#chat-status')?.textContent.includes('Context cleared'));
  assert.equal(contextClears,1,'confirming Clear context calls the existing endpoint');
  await normal.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

async function readBody(request){const chunks=[];for await(const chunk of request)chunks.push(chunk);return Buffer.concat(chunks).toString()}
