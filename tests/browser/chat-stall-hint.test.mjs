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

test('long healthy work stays neutral; stalled work warns and offers the existing actions',async()=>{
 const fixtureDir=await mkdtemp(path.join(tmpdir(),'vmbox-stall-'));
 let server,browser;
 try{
  const fixturePath=path.join(fixtureDir,'activity.json');
  execFileSync(process.env.VMBOX_GO||'go',['test','./internal/controller','-run','^TestBoxActivityBrowserPayloads$','-count=1'],{env:{...process.env,VMBOX_ACTIVITY_FIXTURE_FILE:fixturePath},stdio:'pipe',timeout:120000});
  const payloads=JSON.parse(await readFile(fixturePath,'utf8'));
  assert.equal(payloads.longHealthy[0].statusSource,'fallback');
  assert.equal(payloads.longStalled[0].statusSource,'stale');
  let mode='longHealthy',role='owner',hibernated=false,interrupts=0,interactive=0,allocations=0;
  const initial=new Date(Date.now()-192*60000).toISOString();
  const messages=[{id:'request',direction:'user',state:'delivered',text:'Run the task.',createdAt:initial,updatedAt:initial}];
  server=http.createServer((req,res)=>{
   const url=req.url.split('?')[0];
   if(url==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
   if(assets[url]){res.setHeader('Content-Type',url.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[url])}
   res.setHeader('Content-Type','application/json');
   if(url==='/v1/whoami')return res.end(JSON.stringify({role}));
   if(url==='/v1/logical-boxes'||url==='/v1/grid-boxes')return res.end(JSON.stringify([{id:'builder',name:'Builder',state:hibernated?'hibernated':'running',defaultAgent:'codex'}]));
   if(url==='/v1/box-activity')return res.end(JSON.stringify(payloads[mode]));
   if(url==='/v1/tool-presets'||url==='/v1/box-conversations')return res.end('[]');
   if(url==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   if(url==='/v1/logical-boxes/builder/messages'){
    res.setHeader('X-Vmbox-Agent-Busy','true');res.setHeader('X-Vmbox-Agent-Busy-Since',payloads[mode][0].busySince);
    return res.end(JSON.stringify(messages));
   }
   if(url==='/v1/logical-boxes/builder/sessions/interactive'&&req.method==='POST'){interactive++;return res.end('{"session":"s1"}')}
   if(url==='/v1/logical-boxes/builder/terminal/input'&&req.method==='POST'){interrupts++;return res.end('{}')}
   if(url==='/v1/logical-boxes/builder/hibernate'&&req.method==='POST'){hibernated=true;return res.end('{}')}
   if(url==='/v1/logical-boxes/builder/allocate'&&req.method==='POST'){allocations++;hibernated=false;return res.end('{"state":"ready","requestId":"allocation-1"}')}
   if(url.endsWith('/messages'))return res.end('[]');
   return res.end('{}');
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
  const page=await browser.newPage();
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='working');
  assert.match(await page.$eval('#chat-banner',el=>el.textContent),/^Working for 3 h 12 min · last activity just now$/);
  assert.equal(await page.$('#chat-banner .chat-banner-actions'),null);
  const capture=async phase=>{
   if(!process.env.VMBOX_CAPTURE_DIR)return;
   await mkdir(process.env.VMBOX_CAPTURE_DIR,{recursive:true});
   for(const width of [390,1440])for(const theme of ['light','dark']){
    await page.setViewport({width,height:width===390?844:900,deviceScaleFactor:1});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    if(width===390&&!await page.$eval('#chat-app',el=>el.classList.contains('in-chat'))){
     await page.$eval('[data-box-id="builder"] .chat-meta',el=>el.click());
     await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'));
    }
    assert.equal(await page.$eval('#chat-conversation',el=>getComputedStyle(el).display!=='none'),true,'capture shows the chat');
    await page.screenshot({path:path.join(process.env.VMBOX_CAPTURE_DIR,`stall-${phase}-${width}-${theme}.png`)});
   }
  };
  await capture('healthy');
  mode='longStalled';await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='stalled',{timeout:9000});
  assert.match(await page.$eval('#chat-banner',el=>el.textContent),/^No activity for 14 min — the agent may be stuck/);
  assert.deepEqual(await page.$$eval('#chat-banner button',nodes=>nodes.map(node=>node.textContent)),['Interrupt','Open terminal','Restart']);
  await capture('stalled');
  await page.setViewport({width:390,height:844,deviceScaleFactor:1});
  assert.ok((await page.$$eval('#chat-banner button',nodes=>nodes.map(node=>node.getBoundingClientRect().height))).every(height=>height>=40));
  await page.$eval('#chat-banner button:nth-child(1)',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-interrupt')&&!document.querySelector('#chat-interrupt').disabled);
  assert.equal(interrupts,1);
  const terminalSession=page.waitForResponse(response=>response.url().endsWith('/v1/logical-boxes/builder/sessions/interactive')&&response.request().method()==='POST',{timeout:9000});
  await page.$eval('#chat-banner button:nth-child(2)',el=>el.click());
  const terminalResponse=await terminalSession;
  assert.equal(terminalResponse.status(),200,'terminal action requests an interactive session');
  assert.equal((await terminalResponse.json()).session,'s1');
  await page.waitForFunction(()=>!document.querySelector('#takeover').hidden);
  assert.equal(await page.$eval('#takeover-screen',el=>el.classList.contains('is-terminal')),true,'terminal action opens TMUX');
  assert.equal(interactive,2,'terminal action uses the existing interactive session flow');
  await page.$eval('#takeover-close',el=>el.click());
  page.once('dialog',dialog=>{assert.match(dialog.message(),/^Restart "Builder"/);void dialog.accept()});
  await page.$eval('#chat-banner button:nth-child(3)',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='stalled');
  for(let attempt=0;attempt<50&&allocations===0;attempt++)await new Promise(resolve=>setTimeout(resolve,100));
  assert.equal(allocations,1,'restart confirms, hibernates, and allocates');
  messages.push({id:'tool',direction:'system',state:'delivered',text:'MCP · Running tests',createdAt:new Date().toISOString()});
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='working',{timeout:9000});
  assert.equal(await page.$('#chat-banner .chat-banner-actions'),null,'new tool-call evidence clears warning');
  mode='longHealthy';await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='working');
  role='user';messages.length=1;await page.reload();
  await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent.includes('working'));
  mode='longStalled';await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='stalled',{timeout:9000});
  assert.equal(await page.$('#chat-banner .chat-banner-actions'),null,'non-owner has no recovery actions');
  await page.close();
 }finally{
  if(browser)await browser.close();
  if(server)await new Promise(resolve=>server.close(resolve));
  await rm(fixtureDir,{recursive:true,force:true});
 }
});
