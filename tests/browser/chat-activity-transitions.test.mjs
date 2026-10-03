import assert from 'node:assert/strict';
import {test} from 'node:test';
import {execFileSync} from 'node:child_process';
import http from 'node:http';
import {mkdtemp,mkdir,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));

test('send, quiet heartbeat, stale busy signal, and reply keep every activity surface consistent',async()=>{
 const fixtureDir=await mkdtemp(path.join(tmpdir(),'vmbox-activity-'));
 let server,browser;
 try{
  const fixturePath=path.join(fixtureDir,'activity.json');
  execFileSync(process.env.VMBOX_GO||'go',['test','./internal/controller','-run','^TestBoxActivityBrowserPayloads$','-count=1'],{
   env:{...process.env,VMBOX_ACTIVITY_FIXTURE_FILE:fixturePath},stdio:'pipe',timeout:120000,
  });
  const payloads=JSON.parse(await readFile(fixturePath,'utf8'));
  assert.equal(payloads.quiet[0].statusSource,'quiet');
  assert.equal(payloads.stale[0].statusSource,'stale');
  let mode='idle',boxState='running',activityRequests=0,settleSend;
  const now=new Date().toISOString();
  const messages=[{id:'reply-1',direction:'agent',state:'delivered',text:'Previous reply.',createdAt:now,updatedAt:now}];
  server=http.createServer((req,res)=>{
   const url=req.url.split('?')[0];
   if(url==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
   if(assets[url]){res.setHeader('Content-Type',url.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[url])}
   res.setHeader('Content-Type','application/json');
   if(url==='/v1/whoami')return res.end('{"role":"owner"}');
   if(url==='/v1/logical-boxes'||url==='/v1/grid-boxes')return res.end(JSON.stringify([{id:'builder',name:'Builder',state:boxState,defaultAgent:'codex'}]));
   if(url==='/v1/box-activity'){activityRequests++;return res.end(JSON.stringify(payloads[mode]))}
   if(url==='/v1/tool-presets'||url==='/v1/box-conversations')return res.end('[]');
   if(url==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   if(url==='/v1/logical-boxes/builder/messages'){
    if(req.method==='POST'){
     settleSend=()=>{const stamp=new Date().toISOString(),message={id:'request-2',direction:'user',state:'delivered',text:'New request',createdAt:stamp,updatedAt:stamp};messages.push(message);res.end(JSON.stringify({message}))};
     return;
    }
    res.setHeader('X-Vmbox-Agent-Busy',mode==='idle'?'false':'true');
    const state=payloads[mode][0];
    if(state.observedAt){res.setHeader('X-Vmbox-Mascot-Observed-At',state.observedAt);res.setHeader('X-Vmbox-Mascot-Mood',state.mood);res.setHeader('X-Vmbox-Mascot-Activity',state.activity)}
    return res.end(JSON.stringify(messages));
   }
   if(url.endsWith('/messages'))return res.end('[]');
   return res.end('{}');
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
  const page=await browser.newPage();
  await page.setViewport({width:1440,height:900,deviceScaleFactor:1});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · idle');
  const assertState=async(state,preview,bubble)=>{
   assert.equal(await page.$eval('#chat-header-state',el=>el.textContent),'codex · '+state);
   assert.equal(await page.$eval('[data-box-id="builder"] .preview',el=>el.textContent),preview);
   assert.equal(!!await page.$('#chat-messages .msg.processing'),bubble);
   assert.equal(await page.$eval('[data-box-id="builder"] .avatar-mascot svg',el=>el.dataset.mood),state==='working'?'working':'idle');
  };
  const capture=async(phase)=>{
   if(!process.env.VMBOX_CAPTURE_DIR)return;
   await mkdir(process.env.VMBOX_CAPTURE_DIR,{recursive:true});
   for(const width of [1440,390])for(const theme of ['light','dark']){
    await page.setViewport({width,height:width===390?844:900,deviceScaleFactor:1});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    await page.screenshot({path:path.join(process.env.VMBOX_CAPTURE_DIR,`activity-${phase}-${width}-${theme}.png`)});
   }
   await page.setViewport({width:1440,height:900,deviceScaleFactor:1});
  };
  await assertState('idle','Previous reply.',false);
  await capture('before');
  mode='quiet';
  const initialRequests=activityRequests;
  await page.$eval('#refresh',el=>el.click());
  for(let i=0;i<50&&activityRequests===initialRequests;i++)await new Promise(resolve=>setTimeout(resolve,100));
  assert.ok(activityRequests>initialRequests,'quiet batch arrived');
  await assertState('idle','Previous reply.',false);
  await page.type('#chat-input','New request');
  await page.click('#send');
  await page.waitForFunction(()=>!!document.querySelector('#chat-messages .msg.processing'));
  await assertState('working','working…',true);
  await capture('after');
  const beforeHeartbeat=activityRequests;
  await page.$eval('#refresh',el=>el.click());
  for(let i=0;i<50&&activityRequests===beforeHeartbeat;i++)await new Promise(resolve=>setTimeout(resolve,100));
  assert.ok(activityRequests>beforeHeartbeat,'quiet heartbeat arrived during pending send');
  await assertState('working','working…',true);
  assert.ok(settleSend,'send request is pending');
  mode='stale';settleSend();
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · working',{timeout:9000});
  await assertState('working','working…',true);
  mode='idle';
  const stamp=new Date().toISOString();messages.push({id:'reply-2',direction:'agent',state:'delivered',text:'Done.',createdAt:stamp,updatedAt:stamp});
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · idle'&&document.querySelector('[data-box-id="builder"] .preview')?.textContent==='Done.',{timeout:9000});
  await assertState('idle','Done.',false);
  boxState='hibernated';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · hibernated',{timeout:9000});
  assert.equal(await page.$('#chat-messages .msg.processing'),null);
  boxState='stopped';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · stopped',{timeout:9000});
  assert.equal(await page.$('#chat-messages .msg.processing'),null);
 }finally{
  if(browser)await browser.close();
  if(server)await new Promise(resolve=>server.close(resolve));
  await rm(fixtureDir,{recursive:true,force:true});
 }
});
