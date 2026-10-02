import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const box={id:'builder',name:'Builder',state:'hibernated',defaultAgent:'claude',provider:'shared-worker'};
const imported=[
 {application:'claude',name:'old',model:'claude-sonnet',reasoningEffort:'high'},
 {application:'github',name:'work'},
];
const available=[
 {application:'claude',name:'old',email:'old@example.test'},
 {application:'codex',name:'new',email:'new@example.test'},
 {application:'github',name:'work',host:'github.com',user:'work-user'},
 {application:'github',name:'personal',host:'github.example',user:'personal-user'},
];

function fixtureServer(fail,writes,requests){
 let failed=false,state={profiles:imported,pending:[],verified:true};
 let releaseCredentials;
 const heldCredentials=new Promise(resolve=>{releaseCredentials=resolve});
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(path==='/v1/logical-boxes/builder/imported-credentials'){
    requests.push('GET state');await new Promise(done=>setTimeout(done,150));
    if(fail==='loading')await heldCredentials;
    if(fail==='state'&&!failed&&requests.includes('GET available')){failed=true;res.statusCode=502;return res.end('{"error":"Credential service unavailable"}')}
    return res.end(JSON.stringify(state));
   }
   if(path==='/v1/login-profiles'){
    requests.push('GET available');
    if(fail==='profiles'&&!failed){failed=true;res.statusCode=500;return res.end('{"error":"Could not list login profiles"}')}
    return res.end(JSON.stringify(available));
   }
   if(path==='/v1/logical-boxes/builder/login-profiles'&&req.method==='PUT'){
    let raw='';for await(const chunk of req)raw+=chunk;
   const body=JSON.parse(raw);writes.push(body);
    if(fail==='put'&&!failed){failed=true;res.statusCode=502;return res.end('{"error":"Credential update unavailable"}')}
    state={...state,pending:body.profiles,pendingSet:true,note:'Changes saved for next start.'};
    return res.end(JSON.stringify(state));
   }
   if(path.endsWith('/messages')||['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const file=resolve(web,path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html','.woff2':'font/woff2'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
 server.releaseCredentials=releaseCredentials;
 return server;
}

async function openCredentials(page){
 await page.goto(page.base+'/chat#box=builder');
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
 await page.click('#chat-info');await page.click('[data-ip-row="credentials"]');
 await page.waitForFunction(()=>!document.querySelector('.ip-page[data-ip-page="credentials"]').hidden);
}

async function saveSlot(page,slot,value){
 await page.click(`[data-slot="${slot}"] .credential-slot-change`);
 if(page.viewport().width<600){
  const undersized=await page.$$eval(`[data-slot="${slot}"] .credential-slot-picker select,[data-slot="${slot}"] .credential-slot-picker button`,nodes=>nodes.filter(node=>{const r=node.getBoundingClientRect();return r.width<40||r.height<40}).map(node=>node.textContent));
  assert.deepEqual(undersized,[],'mobile account picker has 40px targets');
 }
 await page.select(`[data-slot="${slot}"] select`,JSON.stringify(value));
 await page.click(`[data-slot="${slot}"] .credential-slot-save`);
 await page.waitForFunction(()=>document.querySelector('#box-credentials-status').textContent==='Changes saved for next start.');
}

test('Details Credentials changes the agent and GitHub slots independently',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [360,390,1440]){
   const writes=[],requests=[],server=fixtureServer('none',writes,requests);
   await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();page.base=`http://127.0.0.1:${server.address().port}`;
    await page.setViewport({width,height:width<600?844:900,isMobile:width<600,hasTouch:width<600});
    await openCredentials(page);
    await page.waitForSelector('[data-slot="github"] .credential-slot-change');
    assert.ok(requests.includes('GET state')&&requests.includes('GET available'));
    assert.equal(await page.$eval('[data-slot="agent"] .credential-slot-account',el=>el.textContent),'Claude · old · old@example.test');
    assert.equal(await page.$eval('[data-slot="github"] .credential-slot-account',el=>el.textContent),'GitHub · work · work-user@github.com');
    assert.equal(await page.$$eval('[data-slot="agent"] .credential-slot-remove',nodes=>nodes.length),0,'agent login cannot be removed');
    if(width<600){
     const undersized=await page.$$eval('#inspect-prototype-back,.ip-page[data-ip-page="credentials"] .credential-slot-actions button',buttons=>buttons.filter(button=>{const r=button.getBoundingClientRect();return r.width<40||r.height<40}).map(button=>button.textContent));
     assert.deepEqual(undersized,[],'mobile credential actions have 40px targets');
    }
    await saveSlot(page,'github',{application:'github',name:'personal'});
    assert.deepEqual(writes[0],{profiles:[{application:'claude',name:'old',model:'claude-sonnet',reasoningEffort:'high'},{application:'github',name:'personal'}]});
    assert.match(await page.$eval('[data-slot="agent"] .credential-slot-note',el=>el.textContent),/Applied/);
    assert.match(await page.$eval('[data-slot="github"] .credential-slot-note',el=>el.textContent),/Pending/);
    await saveSlot(page,'agent',{application:'codex',name:'new'});
    assert.deepEqual(writes[1],{profiles:[{application:'codex',name:'new'},{application:'github',name:'personal'}]});
    assert.match(await page.$eval('[data-slot="agent"] .credential-slot-note',el=>el.textContent),/Pending/);
    await page.click('[data-slot="github"] .credential-slot-remove');
    await page.waitForFunction(()=>document.querySelector('[data-slot="github"] .credential-slot-account').textContent==='None');
    assert.deepEqual(writes[2],{profiles:[{application:'codex',name:'new'}]});
    assert.match(await page.$eval('[data-slot="github"] .credential-slot-note',el=>el.textContent),/Pending/);
    await page.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});

test('Details Credentials keeps a loading state until the credential refs arrive',async()=>{
 const writes=[],requests=[],server=fixtureServer('loading',writes,requests);
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();page.base=`http://127.0.0.1:${server.address().port}`;
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await openCredentials(page);
  assert.match(await page.$eval('#box-credentials-status',el=>el.textContent),/Loading accounts/);
  assert.equal(await page.$$eval('.credential-slot-change',nodes=>nodes.length),0);
  server.releaseCredentials();
  await page.waitForSelector('[data-slot="github"] .credential-slot-change');
  await page.close();
 }finally{server.releaseCredentials();await browser.close();server.closeAllConnections();await new Promise(done=>server.close(done))}
});

test('Details Credentials shows loading errors and Retry before allowing a PUT',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const fail of ['state','profiles']){
   const writes=[],requests=[],server=fixtureServer(fail,writes,requests);
   await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();page.base=`http://127.0.0.1:${server.address().port}`;
    await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
    await openCredentials(page);
    await page.waitForFunction(()=>!document.querySelector('#box-credentials-retry').hidden);
    assert.match(await page.$eval('#box-credentials-status',el=>el.textContent),/Could not load credentials/);
    assert.equal(await page.$$eval('.credential-slot-change',nodes=>nodes.length),0,'missing refs cannot be overwritten');
    assert.deepEqual(writes,[]);
    await page.click('#box-credentials-retry');
    await page.waitForSelector('[data-slot="agent"] .credential-slot-change');
    assert.equal(await page.$eval('#box-credentials-retry',el=>el.hidden),true);
    await page.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});

test('Details Credentials retries a failed slot change with the same full refs',async()=>{
 const writes=[],requests=[],server=fixtureServer('put',writes,requests);
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();page.base=`http://127.0.0.1:${server.address().port}`;
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await openCredentials(page);await page.waitForSelector('[data-slot="github"] .credential-slot-change');
  await page.click('[data-slot="github"] .credential-slot-change');
  await page.select('[data-slot="github"] select',JSON.stringify({application:'github',name:'personal'}));
  await page.click('[data-slot="github"] .credential-slot-save');
  await page.waitForFunction(()=>!document.querySelector('#box-credentials-retry').hidden);
  assert.match(await page.$eval('#box-credentials-status',el=>el.textContent),/Credential update unavailable/);
  assert.equal(await page.$eval('[data-slot="github"] .credential-slot-account',el=>el.textContent),'GitHub · work · work-user@github.com');
  await page.click('#box-credentials-retry');
  await page.waitForFunction(()=>document.querySelector('[data-slot="github"] .credential-slot-account').textContent.includes('personal'));
  const expected={profiles:[{application:'claude',name:'old',model:'claude-sonnet',reasoningEffort:'high'},{application:'github',name:'personal'}]};
  assert.deepEqual(writes,[expected,expected]);
  assert.equal(await page.$eval('#box-credentials-retry',el=>el.hidden),true);
  await page.close();
 }finally{await browser.close();server.closeAllConnections();await new Promise(done=>server.close(done))}
});
