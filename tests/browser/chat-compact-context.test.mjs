import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const baseBox={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
const messages=Array.from({length:6},(_,i)=>({id:'m'+i,taskId:'task-1',direction:i%2?'agent':'user',text:'Turn '+i,state:'delivered',createdAt:new Date(Date.now()-(6-i)*60000).toISOString(),updatedAt:new Date(Date.now()-(6-i)*60000).toISOString()}));

async function withChat({box=baseBox,role='owner',busy=false,compactStatus=202,compactError=''},fn){
 const compactCalls=[];
 const data={
  '/v1/whoami':{role,accountId:'acct'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],
  '/v1/box-activity':[],'/v1/box-conversations':[],'/v1/tool-presets':[],'/v1/chat-commands':[],
  '/v1/controller-defaults':{provider:'shared-worker',providerCredential:'pool'},'/v1/provider-credentials':[],
  ['/v1/logical-boxes/'+box.id+'/imported-credentials']:{profiles:[{application:'claude',name:'Studio',model:'Sonnet',reasoningEffort:'high'}]},
  ['/v1/logical-boxes/'+box.id+'/contacts']:[],
  ['/v1/logical-boxes/'+box.id+'/idle-policy']:{seconds:10800},
  ['/v1/logical-boxes/'+box.id+'/attachment-storage']:{boxBytes:1024,boxCount:2,clearableCount:2,accountBytes:1024,limitBytes:1024**3},
  ['/v1/logical-boxes/'+box.id+'/resources']:{slotId:'slot-1',assignmentGeneration:3,resources:{cpu:2,memoryMiB:8192,swapMiB:2048,diskGiB:30},memoryUsedBytes:1024,swapUsedBytes:0,diskUsedBytes:1024,diskTotalBytes:30*1024**3,observedAt:new Date().toISOString()},
  '/v1/fleet/host-resources':{memoryAvailableBytes:1024,memoryTotalBytes:2048,swapFreeBytes:0,swapTotalBytes:0},
  '/v1/fleet/status':{slots:[{id:'slot-1',serviceName:'Worker 1',serviceId:'worker-1'}]},
 };
 const server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');
   if(path==='/v1/logical-boxes/'+box.id+'/messages/compact'){
    compactCalls.push({method:request.method,key:request.headers['idempotency-key']||'',body:await readBody(request)});
    response.statusCode=compactStatus;return response.end(JSON.stringify(compactStatus===202?{agent:box.defaultAgent,state:'requested'}:{error:compactError||'compaction failed'}));
   }
   if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
   if(path.endsWith('/messages')){if(busy)response.setHeader('X-Vmbox-Agent-Busy','true');return response.end(JSON.stringify(messages))}
   return response.end(JSON.stringify(data[path]??{}));
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;return response.end()}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port,compactCalls)}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}
async function readBody(request){const chunks=[];for await(const chunk of request)chunks.push(chunk);return Buffer.concat(chunks).toString()}
async function open(browser,base,box,width=1440){
 const page=await browser.newPage();await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});
 page.on('dialog',dialog=>dialog.dismiss());
 await page.goto(base+'/chat#box='+box.id);
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
 await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length>0);
 return page;
}
const openDetails=async page=>{await page.$eval('#chat-info',b=>b.click());await page.waitForFunction(()=>!!document.querySelector('[data-ip-row="compact"]'))};
const rowState=page=>page.$eval('[data-ip-row="compact"]',r=>({disabled:r.disabled,title:r.title,value:r.querySelector('.ip-row-value')?.textContent}));
const chipState=page=>page.$eval('#compact-chip',c=>({hidden:c.hidden,state:c.dataset.state,text:c.textContent,confirm:!!c.querySelector('#compact-chip-confirm'),retry:[...c.querySelectorAll('button')].some(b=>b.textContent==='Retry')}));

test('Details: Compact context row confirms inline and calls the endpoint',async()=>{
 await withChat({},async(browser,base,compactCalls)=>{
  const page=await open(browser,base,baseBox);
  await openDetails(page);
  assert.deepEqual(await page.$$eval('#ip-danger .ip-row',rows=>rows.map(r=>r.dataset.ipRow)),['restart','compact','context','clear-attachments'],'Compact sits immediately above Clear context');
  assert.equal(await page.$eval('[data-ip-row="compact"]',r=>r.classList.contains('ip-row-danger')),false,'Compact stays non-danger');
  const idle=await rowState(page);
  assert.equal(idle.disabled,false,'row enabled for a running agent');
  assert.match(idle.value,/Summarize older context/);
  await page.$eval('[data-ip-row="compact"]',r=>r.click());
  await page.waitForFunction(()=>!document.querySelector('#compact-confirm').hidden);
  assert.match(await page.$eval('#compact-confirm',p=>p.textContent),/Clear context is different|Clear context instead/);
  assert.match(await page.$eval('#compact-confirm',p=>p.textContent),/Chat history stays visible/);
  await page.$eval('#compact-confirm-yes',b=>b.click());
  await page.waitForFunction(()=>document.querySelector('#chat-status').textContent.startsWith('Compaction requested'));
  assert.equal(compactCalls.length,1);
  assert.equal(compactCalls[0].method,'POST');
  assert.ok(compactCalls[0].key,'compact request carries an Idempotency-Key');
  assert.equal((await rowState(page)).value,'Context compacted');
  await page.close();
 });
});

test('/compact: picker entry opens a confirmation chip that calls the endpoint',async()=>{
 await withChat({},async(browser,base,compactCalls)=>{
  const page=await open(browser,base,baseBox);
  await page.click('#chat-input');await page.type('#chat-input','/comp');
  await page.waitForFunction(()=>{const p=document.querySelector('#composer-picker');return p&&!p.hidden&&[...p.children].some(b=>b.textContent.includes('/compact'))});
  await page.evaluate(()=>[...document.querySelector('#composer-picker').children].find(b=>b.textContent.includes('/compact')).click());
  await page.waitForFunction(()=>{const c=document.querySelector('#compact-chip');return c&&!c.hidden});
  const idle=await chipState(page);
  assert.equal(idle.state,'idle');assert.equal(idle.confirm,true);
  assert.match(idle.text,/Compact context\?/);
  await page.$eval('#compact-chip-confirm',b=>b.click());
  await page.waitForFunction(()=>document.querySelector('#compact-chip').dataset.state==='success');
  assert.equal(compactCalls.length,1);
  assert.ok(compactCalls[0].key,'compact request carries an Idempotency-Key');
  await page.close();
 });
});

test('Compact surfaces are disabled with a reason for hibernated, shell and busy boxes',async()=>{
 for(const [label,opts,needle] of [
  ['hibernated',{box:{...baseBox,state:'hibernated'}},/hibernated/],
  ['shell',{box:{...baseBox,defaultAgent:'shell'}},/shell session/],
  ['busy',{busy:true},/busy/],
 ]){
  await withChat(opts,async(browser,base,compactCalls)=>{
   const page=await open(browser,base,baseBox,1440);
   if(label==='busy')await page.waitForFunction(()=>/working/i.test(document.querySelector('#chat-header-state').textContent));
   await openDetails(page);
   const row=await rowState(page);
   assert.equal(row.disabled,true,label+' row disabled');
   assert.match(row.title,needle,label+' row explains why ('+row.title+')');
   // /compact is not offered, and typing it surfaces the reason without calling the endpoint.
   await page.type('#chat-input','/compact');
   await page.waitForFunction(()=>document.querySelector('#compact-chip').hidden);
   await page.keyboard.press('Enter');
   await page.waitForFunction(()=>document.querySelector('#chat-status').textContent.length>0);
   assert.match(await page.$eval('#chat-status',s=>s.textContent),needle,label+' send explains why');
   assert.equal(compactCalls.length,0,label+' never calls the endpoint');
   await page.close();
  });
 }
});

test('A failed compact surfaces the error inline for both entry points',async()=>{
 await withChat({compactStatus:409,compactError:'agent is busy; compact after its current work finishes'},async(browser,base,compactCalls)=>{
  const page=await open(browser,base,baseBox);
  await openDetails(page);
  await page.$eval('[data-ip-row="compact"]',r=>r.click());
  await page.waitForFunction(()=>!document.querySelector('#compact-confirm').hidden);
  await page.$eval('#compact-confirm-yes',b=>b.click());
  await page.waitForFunction(()=>document.querySelector('[data-ip-row="compact"] .ip-row-value').textContent.startsWith('Compact failed'));
  assert.match(await page.$eval('#chat-status',s=>s.textContent),/agent is busy/);
  assert.equal(compactCalls.length,1);
  await page.close();
 });
 await withChat({compactStatus:409,compactError:'agent is busy; compact after its current work finishes'},async(browser,base,compactCalls)=>{
  const page=await open(browser,base,baseBox);
  await page.click('#chat-input');await page.type('#chat-input','/compact');
  await page.keyboard.press('Enter');
  await page.waitForFunction(()=>!document.querySelector('#compact-chip').hidden);
  await page.$eval('#compact-chip-confirm',b=>b.click());
  await page.waitForFunction(()=>document.querySelector('#compact-chip').dataset.state==='error');
  const failed=await chipState(page);
  assert.match(failed.text,/agent is busy/);assert.equal(failed.retry,true);
  assert.equal(compactCalls.length,1);
  await page.close();
 });
});

test('A non-owner does not get the compact entry points',async()=>{
 await withChat({role:'member'},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  await openDetails(page);
  assert.equal((await rowState(page)).disabled,true,'member row disabled');
  await page.$eval('#chat-input',i=>{i.value='';i.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.type('#chat-input','/comp');
  await page.waitForFunction(()=>{const p=document.querySelector('#composer-picker');return p.hidden||![...p.children].some(b=>b.textContent.includes('/compact'))});
  await page.close();
 });
});
