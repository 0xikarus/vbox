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
  if(['/','/app.js','/app.css','/favicon.ico'].includes(path)){
   const file=path==='/'?'index.html':path==='/favicon.ico'?'favicon.svg':path.slice(1);
   res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
   return res.end(await readFile(resolve(root,file)));
  }
  res.setHeader('Content-Type','application/json');
  const values={
   '/v1/capabilities':{providerEdits:true,nativeAttach:true},
   '/v1/logical-boxes':[{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude'}],
   '/v1/provider-credentials':[{provider:'railway',name:'primary',config:{projectId:'p',environmentId:'e',image:'old'},updatedAt:revision}],
   '/v1/provider-schemas':{providers:{railway:{image:'string'}}},
   '/v1/controller-defaults':{provider:'railway',providerCredential:'primary'},
   '/v1/fleet/status':{desiredSlots:2,actualSlots:2,freeSlots:1,occupiedSlots:1,unhealthySlots:0,slots:[{ordinal:1,state:'occupied',health:'healthy',region:'europe-west4',logicalBoxName:'helper ü'},{ordinal:2,state:'free',health:'healthy',region:'europe-west4'}]},
   '/v1/notifications':[],
   '/v1/whoami':{accountId:'account-1',accountName:'Team'},
   '/v1/login-profiles':[{application:'claude',name:'personal',createdAt:revision}],
  };
  if(req.method==='GET' && path in values)return res.end(JSON.stringify(values[path]));
  if(req.method==='PATCH' && (path==='/v1/logical-boxes/box-1'||path==='/v1/provider-credentials/railway/primary'))return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/fleet/slots')return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/login-profiles/codex/browser-test')return res.end(JSON.stringify({application:'codex',name:'browser-test'}));
  if(req.method==='POST' && path==='/v1/logical-boxes')return res.end(JSON.stringify({id:'created'}));
  res.statusCode=404;res.end(JSON.stringify({error:'unexpected endpoint'}));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-setuid-sandbox']});
});
after(async()=>{await browser?.close();await new Promise(r=>server?.close(r))});
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
 await page.click('#profiles > details > summary');await page.select('#profile-upload select','codex');await page.type('#profile-upload input[name=name]','browser-test');
 await page.$eval('#profile-upload input[type=file]',input=>{const data=new DataTransfer();data.items.add(new File(['{"test":"synthetic-only"}'],'auth.json',{type:'application/json'}));input.files=data.files});
 const uploaded=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().includes('/login-profiles/'));await page.click('#profile-upload button');await uploaded;
 assert.equal(Buffer.from(requests.findLast(r=>r.path.includes('/login-profiles/')).body.files['auth.json'],'base64').toString(),'{"test":"synthetic-only"}');
 await page.waitForNetworkIdle();assert.equal(await page.$eval('#profile-upload input[type=file]',n=>n.files.length),0);
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
 await page.click('#logout');assert(await page.$eval('#app',n=>n.hidden));assert.equal(await page.$eval('#provider textarea[name=secret]',n=>n.value),'');
 assert.deepEqual(errors,[]);await page.close();
});
