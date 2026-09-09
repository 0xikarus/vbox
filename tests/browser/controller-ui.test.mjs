import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import {after,before,test} from 'node:test';
import {resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

const root=resolve('internal/controller/web'),requests=[];
let server,browser,base;
const revision='2026-09-05T12:00:00Z';
before(async()=>{
 server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://test').pathname;
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const body=chunks.length?JSON.parse(Buffer.concat(chunks)):null;
  requests.push({path,method:req.method,body,revision:req.headers['if-match']});
  if(['/','/app.js','/app.css','/run-once.js','/favicon.ico','/workspace.js','/workspace-terminal.js','/workspace-desktop.js','/novnc.js','/workspace.css','/xterm.js','/xterm-fit.js','/xterm.css','/boxes/box-1'].includes(path)){
   const file=path==='/boxes/box-1'?'workspace.html':path==='/'?'index.html':path==='/favicon.ico'?'favicon.svg':path.slice(1);
   res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
   return res.end(await readFile(resolve(root,file)));
  }
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/browser-session' && ['POST','DELETE'].includes(req.method)){res.statusCode=204;return res.end()}
  const values={
   '/v1/capabilities':{providerEdits:true,nativeAttach:true},
   '/v1/logical-boxes':[{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude'}],
   '/v1/logical-boxes/box-1':{id:'box-1',name:'helper ü',state:'running'},
   '/v1/provider-credentials':[{provider:'railway',name:'primary',config:{projectId:'p',environmentId:'e',image:'old'},updatedAt:revision}],
   '/v1/provider-schemas':{providers:{railway:{image:'string'}}},
   '/v1/controller-defaults':{provider:'railway',providerCredential:'primary'},
   '/v1/fleet/status':{desiredSlots:2,actualSlots:2,freeSlots:1,occupiedSlots:1,unhealthySlots:0,slots:[{ordinal:1,state:'occupied',health:'healthy',region:'europe-west4',logicalBoxName:'helper ü'},{ordinal:2,state:'free',health:'healthy',region:'europe-west4'}]},
   '/v1/notifications':[],
   '/v1/whoami':{accountId:'account-1',accountName:'Team'},
   '/v1/login-profiles':[{application:'claude',name:'personal',createdAt:revision}],
  };
  if(req.method==='GET' && path in values)return res.end(JSON.stringify(values[path]));
  if(req.method==='POST' && path==='/v1/logical-boxes/box-1/sessions/interactive')return res.end(JSON.stringify({session:'persistent-shell'}));
  if(req.method==='PATCH' && (path==='/v1/logical-boxes/box-1'||path==='/v1/provider-credentials/railway/primary'))return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/fleet/slots')return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/login-profiles/codex/browser-test')return res.end(JSON.stringify({application:'codex',name:'browser-test'}));
  if(req.method==='POST' && path==='/v1/logical-boxes')return res.end(JSON.stringify({id:'created'}));
  if(req.method==='DELETE' && path==='/v1/login-profiles/claude/personal'){res.statusCode=204;return res.end()}
  res.statusCode=404;res.end(JSON.stringify({error:'unexpected endpoint'}));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-setuid-sandbox']});
});
after(async()=>{await browser?.close();await new Promise(r=>server?.close(r))});
test('Run once queues once, keeps its key on retry and permits cancellation',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.runCalls=[];let state='queued';
  window.fetch=async(path,options={})=>{
   if(!String(path).startsWith('/v1/run-once'))return original(path,options);
   window.runCalls.push({path,options});
   if(String(path).endsWith('/cancel'))state='cancelled';
   const run={id:'queue-fixture',request:{agent:'shell',prompt:'printf unique'},state};
   return new Response(JSON.stringify(path==='/v1/run-once'&&!options.method?[]:run),{headers:{'Content-Type':'application/json'}});
  };
 });
 await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#run-once select[name=provider] option');
 await page.type('#run-once textarea','printf unique');await page.click('#run-once form button');
 await page.waitForFunction(()=>document.querySelector('#run-once-content').textContent.includes('Waiting for a healthy free slot'));
 await page.click('#run-once form button');
 const calls=await page.evaluate(()=>window.runCalls.filter(c=>c.path==='/v1/run-once'&&c.options.method==='POST'));
 assert.equal(calls.length,2);assert.equal(calls[0].options.headers['Idempotency-Key'],calls[1].options.headers['Idempotency-Key']);
 await page.evaluate(()=>[...document.querySelectorAll('#run-once button')].find(b=>b.textContent==='Cancel queued run').click());
 await page.waitForFunction(()=>document.querySelector('#run-once-content').textContent.includes('cancelled'));
 assert.deepEqual(errors,[]);await page.close();
});
test('completed one-shot opens retained output without allocation or a new shell',async()=>{
 const page=await browser.newPage();const start=requests.length;
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;
  window.fetch=async(path,...args)=>{
   if(String(path).startsWith('/v1/run-once/'))return new Response(JSON.stringify({id:'finished',boxId:'box-1',state:'submitted',task:{agent:'shell',session:'task-finished',state:'exited',exitCode:17,finishedAt:'2026-09-09T00:00:00Z',output:'actual recorded fixture output'}}));
   if(path==='/v1/logical-boxes/box-1')return new Response(JSON.stringify({id:'box-1',name:'finished-box',state:'hibernated'}));
   return original(path,...args);
  };
 });
 await page.goto(base+'/boxes/box-1?run=finished');
 await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('exit 17'));
 assert.match(await page.$eval('#terminal-screen',n=>n.textContent),/actual recorded fixture output/);
 await page.click('#connect');await page.waitForNetworkIdle();
 assert.equal(requests.slice(start).filter(r=>r.method==='POST').length,0);
 await page.close();
});
test('box link opens separate mobile workspace and reuses shell',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#box-list a');
 await Promise.all([page.waitForNavigation(),page.click('#box-list a')]);
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(new URL(page.url()).pathname,'/boxes/box-1');
 assert(requests.some(r=>r.path.endsWith('/sessions/interactive')&&r.body.agent==='shell'&&r.body.reuseShell===true));
 assert.deepEqual(errors,[]);await page.close();
});
test('workspace network failure explains safe recovery',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;let failed=false;
  window.fetch=(path,...args)=>{
   if(String(path).endsWith('/sessions/interactive')&&!failed){failed=true;return Promise.reject(new TypeError('Failed to fetch'))}
   return original(path,...args);
  };
 });
 await page.goto(base+'/boxes/box-1');
 await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('operation may still be running'));
 assert.match(await page.$eval('#error',e=>e.textContent),/Resume \/ reconnect.*not replayed/);
 assert.equal(await page.$eval('#connect',e=>e.disabled),false);
 await page.click('#connect');
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(await page.$eval('#error',e=>e.textContent),'');
 await page.close();
});
test('configuration UI stays tiny and has no terminal code',async()=>{
 const css=await readFile(resolve(root,'app.css'),'utf8'),js=await readFile(resolve(root,'app.js'),'utf8');
 assert(Buffer.byteLength(css)<2048);assert(!/@import|url\(/.test(css));
 for(const removed of ['/terminal','/tasks','chat-groups','setInterval'])assert(!js.includes(removed),removed);
});
for(const mobile of [false,true])test(mobile?'390x844 configuration controls':'desktop configuration edits',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 // Initialize mobile before navigation; this does not prove a real phone keyboard.
 await page.setViewport(mobile?{width:390,height:844,isMobile:true,hasTouch:true}:{width:1280,height:900});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#app:not([hidden])');
 await page.select('#box-list select','codex');
 await page.waitForFunction(()=>document.querySelector('#provider-list button'));
 await page.click('#provider-list button');
 await page.$eval('#provider textarea[name=config]',n=>{n.value=JSON.stringify({image:'new'})});
 const saved=page.waitForResponse(r=>r.request().method()==='PATCH'&&r.url().endsWith('/primary'));
 await page.click('#provider button');await saved;
 const edit=requests.findLast(r=>r.method==='PATCH'&&r.path.endsWith('/primary'));assert.equal(edit.revision,revision);assert.deepEqual(edit.body,{config:{image:'new'}});
 await page.waitForNetworkIdle();
 assert.match(await page.$eval('#profile-tree',n=>n.textContent),/Team.*claude.*personal.*codex.*No saved profiles/s);
 await page.select('#profile-choices select[name=claude]','personal');
 await page.type('#create input[name=name]','profile-box');
 const created=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/v1/logical-boxes'));await page.click('#create button');await created;
 assert.deepEqual(requests.findLast(r=>r.method==='POST').body.loginProfiles,[{application:'claude',name:'personal'}]);
 await page.waitForNetworkIdle();
 assert.equal(await page.$('#profile-upload'),null);
 const beforeDelete=requests.filter(r=>r.method==='DELETE').length;
 page.once('dialog',d=>d.dismiss());await page.click('#profile-tree button');await page.waitForNetworkIdle();assert.equal(requests.filter(r=>r.method==='DELETE').length,beforeDelete);
 page.once('dialog',d=>d.accept());const deleted=page.waitForResponse(r=>r.request().method()==='DELETE');await page.click('#profile-tree button');await deleted;await page.waitForNetworkIdle();
 assert.match(await page.$eval('#capacity',n=>n.textContent),/Free: 1/);
 assert.equal(await page.$eval('#capacity details',n=>n.open),false);
 assert.equal(await page.$eval('#schema',n=>n.parentElement.open),false);
 assert.match(await page.$eval('#destinations',n=>n.textContent),/No notification destinations/);
 assert.equal(await page.$eval('#slots input',n=>n.value),'2');
 const capacitySaved=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().endsWith('/v1/fleet/slots'));
 await page.click('#slots button');await capacitySaved;
 assert.equal(requests.findLast(r=>r.path==='/v1/fleet/slots').body.compute_box_slots,2);
 assert.equal(await page.$('#terminal'),null);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:mobile?'/tmp/vmbox-config-mobile.png':'/tmp/vmbox-config-desktop.png',fullPage:true});
 await page.click('#logout');await page.waitForFunction(()=>document.querySelector('#app').hidden);assert.equal(await page.$eval('#provider textarea[name=secret]',n=>n.value),'');
 assert.deepEqual(errors,[]);await page.close();
});
