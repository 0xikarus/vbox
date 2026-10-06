import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const baseBox={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
let seq=0;
function chatMessages(count=24,{marker=false}={}){
 const list=Array.from({length:count},(_,i)=>({id:'m'+(++seq),taskId:'task-1',direction:i%2?'agent':'user',text:(i%2?'Analysis for step ':'Continue with step ')+i+'.',state:'delivered',createdAt:new Date(Date.now()-(count-i)*60000).toISOString(),updatedAt:new Date(Date.now()-(count-i)*60000).toISOString()}));
 if(marker)list.push({id:'m'+(++seq),taskId:'task-1',direction:'system',text:'context compaction requested · claude',state:'delivered',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()});
 return list;
}

async function withChat({box=baseBox,role='owner',state,onCompact,busy=false},fn){
 state=state||{messages:chatMessages()};
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
    onCompact?.(state);
    response.statusCode=202;return response.end(JSON.stringify({agent:box.defaultAgent,state:'requested'}));
   }
   if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
   if(path.endsWith('/messages')){if(busy)response.setHeader('X-Vmbox-Agent-Busy','true');return response.end(JSON.stringify(state.messages))}
   return response.end(JSON.stringify(data[path]??{}));
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;return response.end()}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port,{state,compactCalls})}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}
async function readBody(request){const chunks=[];for await(const chunk of request)chunks.push(chunk);return Buffer.concat(chunks).toString()}
async function open(browser,base,box,width=1440){
 const page=await browser.newPage();await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});
 await page.goto(base+'/chat#box='+box.id);
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
 await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length>0);
 return page;
}
const hint=page=>page.$eval('#compact-hint',el=>({hidden:el.hidden,text:el.textContent,state:el.dataset.state,disabled:el.disabled}));

test('Compact hint shows for a long idle conversation at phone and desktop widths',async()=>{
 await withChat({},async(browser,base)=>{
  for(const width of [390,1440]){
   const page=await open(browser,base,baseBox,width);
   await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
   const view=await hint(page);
   assert.equal(view.state,'idle');
   assert.match(view.text,/Context is getting long/);
   assert.match(view.text,/Compact/);
   assert.ok(await page.evaluate(()=>{const r=document.querySelector('#compact-hint').getBoundingClientRect(),c=document.querySelector('#chat-composer').getBoundingClientRect();return r.bottom<=c.top+1&&r.left>=0&&r.right<=innerWidth+1}),'hint sits fully above the composer at '+width+'px');
   await page.close();
  }
 });
});

test('Compact hint stays hidden for short, hibernated, shell, busy and non-owner chats',async()=>{
 await withChat({state:{messages:chatMessages(5)}},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  assert.equal((await hint(page)).hidden,true,'short conversation');
  await page.close();
 });
 await withChat({box:{...baseBox,state:'hibernated'}},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  assert.equal((await hint(page)).hidden,true,'hibernated box');
  await page.close();
 });
 await withChat({box:{...baseBox,defaultAgent:'shell'}},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  assert.equal((await hint(page)).hidden,true,'shell agent');
  await page.close();
 });
 await withChat({busy:true},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  await page.waitForFunction(()=>document.querySelector('#chat-header-state').textContent.match(/working/i));
  assert.equal((await hint(page)).hidden,true,'agent working');
  await page.close();
 });
 await withChat({role:'member'},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  assert.equal((await hint(page)).hidden,true,'non-owner');
  await page.close();
 });
});

test('Tapping Compact shows inline progress, calls the endpoint, then success',async()=>{
 await withChat({},async(browser,base,{compactCalls})=>{
  const page=await open(browser,base,baseBox);
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  const busyState=await page.evaluate(()=>{const el=document.querySelector('#compact-hint');el.click();return {state:el.dataset.state,text:el.textContent,disabled:el.disabled}});
  assert.equal(busyState.state,'busy','the hint switches to an inline progress state on tap');
  assert.match(busyState.text,/Compacting/);assert.equal(busyState.disabled,true);
  await page.waitForFunction(()=>document.querySelector('#compact-hint').dataset.state==='success');
  assert.match((await hint(page)).text,/Context compacted/);
  assert.equal(compactCalls.length,1);
  assert.equal(compactCalls[0].method,'POST');
  assert.ok(compactCalls[0].key,'compact request carries an Idempotency-Key');
  await page.close();
 });
});

test('A failed compact shows the error inline and offers Retry without a modal',async()=>{
 await withChat({},async(browser,base,{compactCalls})=>{
  const page=await open(browser,base,baseBox);
  await page.evaluate(()=>{const original=window.fetch;window.fetch=(url,options)=>{if(String(url).includes('/messages/compact'))return Promise.resolve(new Response(JSON.stringify({error:'agent is busy; compact after its current work finishes'}),{status:409,headers:{'Content-Type':'application/json'}}));return original(url,options)}});
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  await page.$eval('#compact-hint',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#compact-hint').dataset.state==='error');
  const view=await hint(page);
  assert.match(view.text,/agent is busy/);assert.match(view.text,/Retry/);
  assert.equal(view.hidden,false);assert.equal(view.disabled,false);
  await page.close();
 });
});

test('The hint hides after a compact and only returns once the conversation grows again',async()=>{
 const t0=Date.now();
 const timed=(prefix,count,endMs,step=60000)=>{return Array.from({length:count},(_,i)=>({id:prefix+'-'+i,taskId:'task-1',direction:i%2?'agent':'user',text:prefix+' turn '+(i+1)+'.',state:'delivered',createdAt:new Date(endMs-(count-1-i)*step).toISOString(),updatedAt:new Date(endMs-(count-1-i)*step).toISOString()}))};
 const initial=timed('old',24,t0-40*60000);
 await withChat({state:{messages:initial},onCompact:s=>{s.messages=[...s.messages,{id:'m-marker',taskId:'task-1',direction:'system',text:'context compaction requested · claude',state:'delivered',createdAt:new Date(t0-30*60000).toISOString(),updatedAt:new Date(t0-30*60000).toISOString()}]}},async(browser,base,{state,compactCalls})=>{
  const page=await open(browser,base,baseBox);
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  await page.$eval('#compact-hint',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#compact-hint').dataset.state==='success');
  assert.equal(compactCalls.length,1);
  // The compaction marker is now the baseline; reload so the client re-reads history.
  await page.reload();
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length>0);
  assert.equal((await hint(page)).hidden,true,'hidden right after compact');
  // Grow the conversation past the threshold since the marker, then reload.
  state.messages=[...state.messages,...timed('new',21,t0-15*60000,30000)];
  await page.reload();
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length>0);
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  assert.equal((await hint(page)).state,'idle','returns after the conversation grows again');
  await page.close();
 });
});

test('Compact hint hides while the composer has text or a reply quote is active',async()=>{
 await withChat({},async(browser,base)=>{
  const page=await open(browser,base,baseBox);
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  await page.type('#chat-input','half-written message');
  await page.waitForFunction(()=>document.querySelector('#compact-hint').hidden);
  await page.$eval('#chat-input',i=>{i.value='';i.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  await page.$eval('#chat-messages .msg.user .msg-reply',b=>b.click());
  await page.waitForFunction(()=>document.querySelector('#compact-hint').hidden);
  await page.$eval('#reply-cancel',b=>b.click());
  await page.waitForFunction(()=>!document.querySelector('#compact-hint').hidden);
  await page.close();
 });
});
