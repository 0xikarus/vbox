import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));

test('one activity batch updates all box rows, the header, and the processing bubble',async t=>{
 const boxes=[
  {id:'builder',name:'Builder',state:'running',defaultAgent:'codex'},
  {id:'quiet',name:'Quiet',state:'running',defaultAgent:'claude'},
  {id:'unknown',name:'Unknown',state:'running',defaultAgent:'codex'},
  {id:'unknownReply',name:'Unknown Reply',state:'running',defaultAgent:'codex'},
  {id:'falseRecent',name:'False Recent',state:'running',defaultAgent:'codex'},
  {id:'sleeping',name:'Sleeping',state:'hibernated',defaultAgent:'claude'},
 ];
 const now=new Date().toISOString();
 const messages={
  builder:[{id:'b1',direction:'user',state:'delivered',text:'Please fix the tooltip.',createdAt:now,updatedAt:now}],
  quiet:[{id:'q1',direction:'agent',state:'delivered',text:'Done.',createdAt:now,updatedAt:now}],
  unknown:[{id:'u1',direction:'user',state:'delivered',text:'Please investigate.',createdAt:now,updatedAt:now}],
  unknownReply:[{id:'ur1',direction:'user',state:'delivered',text:'Please check.',createdAt:now,updatedAt:now},{id:'ur2',direction:'agent',state:'delivered',text:'Finished.',createdAt:now,updatedAt:now}],
  falseRecent:[{id:'f1',direction:'user',state:'delivered',text:'Recent request.',createdAt:now,updatedAt:now}],
  sleeping:[{id:'s1',direction:'user',state:'delivered',text:'Old request.',createdAt:now,updatedAt:now}],
 };
 let activityRequests=0,quietMood='happy',builderPhrase='Editing chat.js';
 const activity=()=>[
  {boxId:'builder',busy:true,busySince:now,mood:'idle',activity:'working',phrase:builderPhrase,observedAt:new Date().toISOString()},
  {boxId:'quiet',busy:false,mood:quietMood,activity:'idle',observedAt:new Date().toISOString()},
  {boxId:'unknown',busy:null},
  {boxId:'unknownReply',busy:null},
  {boxId:'falseRecent',busy:false},
  {boxId:'sleeping',busy:true,busySince:now,mood:'idle',activity:'working',phrase:'Old work',observedAt:new Date().toISOString()},
 ];
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-activity'){activityRequests++;return res.end(JSON.stringify(activity()))}
  if(path==='/v1/tool-presets'||path==='/v1/box-conversations')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  const match=path.match(/^\/v1\/logical-boxes\/(builder|quiet|unknown|unknownReply|falseRecent|sleeping)\/messages$/);
  if(match){if(match[1]==='falseRecent')res.setHeader('X-Vmbox-Agent-Busy','false');return res.end(JSON.stringify(messages[match[1]]))}
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 const captureDir=process.env.VMBOX_CAPTURE_DIR;
 if(captureDir)await mkdir(captureDir,{recursive:true});
 try{
  for(const width of [1440,390])for(const theme of ['light','dark']){
   const mobile=width===390,page=await browser.newPage();
   await page.setViewport({width,height:mobile?844:900,deviceScaleFactor:1,isMobile:mobile,hasTouch:mobile});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
   await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .preview')?.textContent==='Editing chat.js');
   assert.equal(await page.$eval('[data-box-id="builder"] .preview',el=>el.textContent),'Editing chat.js');
   assert.equal(await page.$eval('[data-box-id="quiet"] .preview',el=>el.textContent),'Done.');
   assert.equal(await page.$eval('[data-box-id="unknown"] .preview',el=>el.textContent),'working…','unknown busy retains the recent user-message fallback');
   assert.equal(await page.$eval('[data-box-id="unknownReply"] .preview',el=>el.textContent),'Finished.','unknown busy with an agent reply is idle');
   assert.equal(await page.$eval('[data-box-id="falseRecent"] .preview',el=>el.textContent),'You: Recent request.','explicit false overrides a recent user message');
   assert.equal(await page.$eval('[data-box-id="sleeping"] .preview',el=>el.textContent),'You: Old request.');
   assert.equal(await page.$eval('[data-box-id="sleeping"]',el=>el.textContent.includes('processing')||el.textContent.includes('working')),false);
   assert.equal(await page.$eval('#chat-header-state',el=>el.textContent),'codex · Editing chat.js');
   assert.equal(await page.$eval('#chat-messages .msg.processing .typing-phrase-text',el=>el.textContent),'Editing chat.js');
   assert.equal(await page.$eval('#chat-messages .msg.processing .typing-label',el=>el.textContent),'Editing chat.js');
   assert.ok(await page.$('#chat-messages .msg.processing .tv-button'),'TV control remains in the bubble');
   const phraseBubbleHeight=await page.$eval('#chat-messages .msg.processing',el=>el.getBoundingClientRect().height);
   if(mobile){await page.$eval('#chat-back',el=>el.click());await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'))}
   if(captureDir)await page.screenshot({path:`${captureDir}/activity-${width}-${theme}-list.png`});
   if(mobile){await page.$eval('[data-box-id="builder"] .chat-meta',el=>el.click());await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'))}
   if(captureDir){await (await page.$('#chat-header')).screenshot({path:`${captureDir}/activity-${width}-${theme}-header.png`});await (await page.$('#chat-messages .msg.processing')).screenshot({path:`${captureDir}/activity-${width}-${theme}-bubble.png`})}
   if(width===1440&&theme==='light'){
    await page.$eval('[data-box-id="quiet"] .chat-meta',el=>el.click());
    await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='claude · idle');
    await page.$eval('[data-box-id="sleeping"] .chat-meta',el=>el.click());
    await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='claude · hibernated');
    assert.equal(await page.$('#chat-messages .msg.processing'),null,'hibernated box has no processing bubble');
    await t.test('unknown busy plus recent user message is working',async()=>{
     await page.$eval('[data-box-id="unknown"] .chat-meta',el=>el.click());
     await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · working');
     assert.ok(await page.$('#chat-messages .msg.processing'));
    });
    await t.test('unknown busy plus agent reply is idle',async()=>{
     await page.$eval('[data-box-id="unknownReply"] .chat-meta',el=>el.click());
     await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · idle');
     assert.equal(await page.$('#chat-messages .msg.processing'),null);
    });
    await t.test('explicit busy false overrides a recent user message',async()=>{
     await page.$eval('[data-box-id="falseRecent"] .chat-meta',el=>el.click());
     await page.waitForFunction(()=>document.querySelector('#chat-header-state')?.textContent==='codex · idle');
     assert.equal(await page.$('#chat-messages .msg.processing'),null);
    });
    await page.$eval('[data-box-id="builder"] .chat-meta',el=>el.click());
    quietMood='angry';
    await page.waitForFunction(()=>document.querySelector('[data-box-id="quiet"] .avatar-mascot svg')?.dataset.mood==='angry',{timeout:9000});
    assert.equal(await page.$eval('[data-box-id="quiet"] .avatar-mascot',el=>el.getAttribute('aria-label')?.startsWith('Angry')),true,'unopened box mascot follows the batch');
    const before=activityRequests;
    await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'))});
    await new Promise(resolve=>setTimeout(resolve,5600));
    assert.equal(activityRequests,before,'activity requests stop while hidden');
    await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:false});document.dispatchEvent(new Event('visibilitychange'))});
    for(let attempt=0;attempt<30&&activityRequests===before;attempt++)await new Promise(resolve=>setTimeout(resolve,100));
    assert.ok(activityRequests>before,'activity polling resumes when visible');
    builderPhrase='';
    await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .preview')?.textContent==='working…',{timeout:9000});
    assert.equal(await page.$eval('#chat-header-state',el=>el.textContent),'codex · working');
    assert.equal(await page.$$('#chat-messages .msg.processing .typing-dots span').then(nodes=>nodes.length),3,'empty phrase keeps the dots');
    assert.equal(await page.$eval('#chat-messages .msg.processing',el=>el.getBoundingClientRect().height),phraseBubbleHeight,'fallback keeps the bubble height');
    builderPhrase='Editing chat.js';
   }
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
