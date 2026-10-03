import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir,writeFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import {execFileSync} from 'node:child_process';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const before=process.env.VMBOX_REPLY_BASELINE==='1';
const captureRoot=process.env.VMBOX_REPLY_CAPTURE_DIR;
const captureDir=captureRoot?resolve(captureRoot,before?'before':'after'):null;
const root='11111111-1111-4111-8111-111111111111',agentParent='22222222-2222-4222-8222-222222222222',olderParent='33333333-3333-4333-8333-333333333333';
const longUser='Please inspect the full deployment history and all retry events before we choose a rollback plan. This parent message is intentionally much longer than the quote strip.';
const longAgent='I checked the provider logs, compared retries, and wrote a detailed plan for the next deployment. Please confirm the short summary.';
const timestamp='2026-10-02T12:00:00Z';
const msg=(id,direction,text,parentMessageId='',threadId=id)=>({id,taskId:'task-1',direction,text,state:'delivered',parentMessageId,threadId,createdAt:timestamp,updatedAt:timestamp});
const fixture=[msg(root,'user',longUser),msg('44444444-4444-4444-8444-444444444444','agent','I can help with that.',root,root),msg(agentParent,'agent',longAgent),msg('55555555-5555-4555-8555-555555555555','agent','Earlier reply has a missing parent in this page.',olderParent,olderParent)];
const oldParent=msg(olderParent,'user','This earlier message supplies the delayed quote after a separate history request.');
const pairRoot='77777777-7777-4777-8777-777777777777',pairReply='88888888-8888-4888-8888-888888888888';
const pairMessages=[{...msg(pairRoot,'box','Builder asks Reviewer to check the whole result before reporting back.'),senderBoxId:'builder'},
 {...msg(pairReply,'box','The result is sound.',pairRoot,pairRoot),senderBoxId:'reviewer'}];

async function source(name){
 if(before&&(name==='chat.js'||name==='chat.css'))return execFileSync('git',['show','origin/main:internal/controller/web/'+name]);
 return readFile(resolve(web,name));
}

function serverFor(assets,posts){
 const messages=fixture.map(message=>({...message})),pending=[],threadRequests=new Map();
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://local'),path=url.pathname;
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets.get('chat.html'))}
  const asset=path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',extname(asset)==='.css'?'text/css':'text/javascript');return res.end(assets.get(asset))}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end('[{"id":"builder","name":"Builder","state":"running","defaultAgent":"claude","provider":"railway"},{"id":"reviewer","name":"Reviewer","state":"running","defaultAgent":"codex","provider":"railway"}]');
  if(path==='/v1/box-conversations')return res.end('[{"boxAId":"builder","boxBId":"reviewer","boxAName":"Builder","boxBName":"Reviewer","lastAt":"2026-10-02T12:00:00Z","lastText":"The result is sound."}]');
  if(path==='/v1/box-conversations/builder/reviewer/messages')return res.end(JSON.stringify(pairMessages));
  if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let raw='';for await(const chunk of req)raw+=chunk;
   posts.push(JSON.parse(raw));pending.push(res);return;
  }
  if(path==='/v1/logical-boxes/builder/messages'&&url.searchParams.has('threadId')){
   await new Promise(done=>setTimeout(done,900));
   const id=url.searchParams.get('threadId');
   threadRequests.set(id,(threadRequests.get(id)||0)+1);
   return res.end(JSON.stringify({messages:id===olderParent?[oldParent,messages[3]]:messages.filter(message=>message.threadId===id)}));
  }
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify(messages));
  if(path.endsWith('/messages')||['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 return {server,messages,pending,threadRequests,release(){for(const res of pending.splice(0)){const body=posts.at(-1),message=msg('66666666-6666-4666-8666-666666666666','user',body.text,body.parentMessageId,agentParent);messages.push(message);res.end(JSON.stringify({message}))}}};
}

test('absent reply parents are fetched once per chat despite repeated renders',async()=>{
 const assetNames=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css','sheet-scroll.js','sheet-scroll.css','workspace-nav.js','workspace-nav.css','dialog-theme.css','text-size.js','fonts.css','idle-policy.js','idle-policy.css'];
 const assets=new Map(await Promise.all(assetNames.map(async name=>[name,await source(name)])));
 const fixtureServer=serverFor(assets,[]),{server}=fixtureServer;
 const missing='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';
 fixtureServer.messages.push(msg('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb','agent','A reply to a deleted message.',missing,missing));
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  await page.waitForFunction(id=>document.querySelector('.msg[data-key="'+id+'"] .msg-parent')?.textContent==='Reply to an earlier message',{},'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb');
  for(let attempt=0;!fixtureServer.threadRequests.get(missing)&&attempt<100;attempt++)await new Promise(done=>setTimeout(done,20));
  assert.equal(fixtureServer.threadRequests.get(missing),1);
  for(let i=0;i<2;i++)await page.$eval('#refresh',button=>button.onclick());
  await new Promise(done=>setTimeout(done,1100));
  assert.equal(fixtureServer.threadRequests.get(missing),1);
  await page.close();
 }finally{await browser.close();server.closeAllConnections();await new Promise(done=>server.close(done))}
});

test('reply strips are immediate, short, and keep a fixed height while parent loads',async()=>{
 if(captureDir)await mkdir(captureDir,{recursive:true});
 const assetNames=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css','sheet-scroll.js','sheet-scroll.css','workspace-nav.js','workspace-nav.css','dialog-theme.css','text-size.js','fonts.css','idle-policy.js','idle-policy.css'];
 const assets=new Map(await Promise.all(assetNames.map(async name=>[name,await source(name)])));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [1440,390]){
   const posts=[],fixtureServer=serverFor(assets,posts),{server}=fixtureServer;
   await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
    await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
    await page.waitForSelector('.msg[data-key="'+agentParent+'"]');
    if(captureDir)await page.screenshot({path:resolve(captureDir,width+'-initial.png')});
    const incoming=await page.$eval('.msg[data-key="44444444-4444-4444-8444-444444444444"]',row=>({quote:row.querySelector('.msg-parent')?.textContent,body:row.querySelector('.text')?.textContent}));
    if(!before){assert.ok(incoming.quote.length<=80);assert.equal(incoming.body,'I can help with that.')}
    const missing='.msg[data-key="55555555-5555-4555-8555-555555555555"]';
    const placeholder=await page.$eval(missing,row=>({quote:row.querySelector('.msg-parent')?.textContent,height:row.getBoundingClientRect().height,stripHeight:row.querySelector('.msg-parent')?.getBoundingClientRect().height}));
    if(!before)assert.equal(placeholder.quote,'Reply to an earlier message');
    await page.evaluate(selector=>{
     window.replyQuoteFrames=[];const start=performance.now();
     function frame(){const row=document.querySelector(selector),strip=row?.querySelector('.msg-parent');if(row&&strip)window.replyQuoteFrames.push({at:performance.now()-start,height:row.getBoundingClientRect().height,stripHeight:strip.getBoundingClientRect().height,text:strip.textContent});if(performance.now()-start<1250)requestAnimationFrame(frame)}
     requestAnimationFrame(frame);
    },missing);
    await page.waitForFunction(selector=>document.querySelector(selector)?.querySelector('.msg-parent')?.textContent.includes('This earlier message'),{timeout:1400},missing).catch(()=>{});
    const filled=await page.$eval(missing,row=>({quote:row.querySelector('.msg-parent')?.textContent,height:row.getBoundingClientRect().height,stripHeight:row.querySelector('.msg-parent')?.getBoundingClientRect().height}));
    await new Promise(done=>setTimeout(done,80));
    const quoteFrames=await page.evaluate(()=>window.replyQuoteFrames);
    if(!before){assert.match(filled.quote,/This earlier message/);assert.equal(filled.height,placeholder.height);assert.equal(filled.stripHeight,placeholder.stripHeight);assert.ok(filled.quote.length<=80);assert.ok(quoteFrames.some(frame=>frame.text==='Reply to an earlier message')&&quoteFrames.some(frame=>frame.text.includes('This earlier message')));assert.equal(new Set(quoteFrames.map(frame=>frame.height)).size,1,'reply bubble height is stable through quote fill')}
    await page.$eval('.msg[data-key="'+agentParent+'"] .msg-reply',button=>button.click());
    await page.type('#chat-input','I will review the short summary.');
    await page.click('#send');
    await page.waitForSelector('.msg[data-key="pending"]');
    await page.waitForFunction(()=>document.querySelector('.msg[data-key="pending"]')?.getBoundingClientRect().height>0);
    const pending=await page.$eval('.msg[data-key="pending"]',row=>({quote:row.querySelector('.msg-parent')?.textContent,body:row.querySelector('.text')?.textContent,height:row.getBoundingClientRect().height}));
    if(!before){assert.ok(pending.quote?.startsWith('Agent:'));assert.ok(pending.quote.length<=80);assert.equal(pending.body,'I will review the short summary.');assert.deepEqual(posts,[{text:'I will review the short summary.',images:[],parentMessageId:agentParent,mentionedBoxIds:[]}])}
    if(captureDir)await page.screenshot({path:resolve(captureDir,width+'-pending.png')});
    await page.evaluate(()=>{
     window.replySendFrames=[];const start=performance.now();
     function frame(){const row=document.querySelector('.msg[data-key="pending"],.msg[data-key="66666666-6666-4666-8666-666666666666"]'),strip=row?.querySelector('.msg-parent');if(row)window.replySendFrames.push({at:performance.now()-start,key:row.dataset.key,height:row.getBoundingClientRect().height,stripHeight:strip?.getBoundingClientRect().height||0,text:strip?.textContent||''});if(performance.now()-start<1100)requestAnimationFrame(frame)}
     requestAnimationFrame(frame);
    });
    fixtureServer.release();
    await page.waitForSelector('.msg[data-key="66666666-6666-4666-8666-666666666666"]');
    const settled=await page.$eval('.msg[data-key="66666666-6666-4666-8666-666666666666"]',row=>({quote:row.querySelector('.msg-parent')?.textContent,body:row.querySelector('.text')?.textContent,height:row.getBoundingClientRect().height}));
    await new Promise(done=>setTimeout(done,80));
    const sendFrames=await page.evaluate(()=>window.replySendFrames);
    if(!before){assert.equal(settled.quote,pending.quote);assert.equal(settled.body,pending.body);assert.equal(settled.height,pending.height);assert.equal(await page.$eval('#reply-preview',bar=>bar.hidden),true,'Replying to clears after send');assert.ok(sendFrames.some(frame=>frame.key==='pending')&&sendFrames.some(frame=>frame.key==='66666666-6666-4666-8666-666666666666'));assert.equal(new Set(sendFrames.map(frame=>frame.height)).size,1,'reply bubble height is stable through send')}
    if(captureDir)await page.screenshot({path:resolve(captureDir,width+'-settled.png')});
    if(captureDir)await writeFile(resolve(captureDir,width+'-frames.json'),JSON.stringify({quoteFrames,sendFrames},null,2));
    if(!before){
     await page.$eval('.msg[data-key="44444444-4444-4444-8444-444444444444"] .msg-parent',button=>button.click());
     assert.equal(await page.$eval('.msg[data-key="'+root+'"]',row=>row.classList.contains('msg-reply-highlight')),true);
     await page.$eval(missing+' .msg-parent',button=>button.click());
     await page.waitForFunction(()=>!document.querySelector('#thread-panel').hidden&&document.querySelectorAll('#thread-messages .msg').length===2);
     assert.equal(await page.$eval('#thread-messages .msg[data-message-id="'+olderParent+'"]',row=>row.classList.contains('msg-reply-highlight')),true);
    }
    await page.close();
    if(!before){
     const question=msg('99999999-9999-4999-8999-999999999999','agent','');
     question.question={text:'Would you approve the full deployment after reviewing all checks?',choices:['Yes','No'],multiple:false};
     fixtureServer.messages.push(question);
     const questionPage=await browser.newPage();await questionPage.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
     await questionPage.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
     await questionPage.waitForSelector('.msg[data-key="'+question.id+'"] .question .choice');
     await questionPage.$eval('.msg[data-key="'+question.id+'"] .question .choice',button=>button.click());
     await questionPage.$eval('.msg[data-key="'+question.id+'"] .question .send',button=>button.click());
     await questionPage.waitForFunction(id=>document.querySelector('.msg[data-key="'+id+'"] .question .send')?.disabled,{},question.id);
     for(let attempt=0;posts.length<2&&attempt<100;attempt++)await new Promise(done=>setTimeout(done,20));
     assert.equal(posts.length,2);
     assert.equal(posts.at(-1).text,'Yes','question choice body excludes the full parent question');
     assert.equal(posts.at(-1).parentMessageId,question.id);
     fixtureServer.release();await questionPage.close();
    }
    const pair=await browser.newPage();await pair.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
    await pair.goto(`http://127.0.0.1:${server.address().port}/chat#pair=builder%2Freviewer`);
    await pair.waitForFunction(()=>document.querySelectorAll('#chat-messages .pair-msg').length===2);
    if(!before){
     const quote=await pair.$eval('#chat-messages .pair-msg:last-child .msg-parent',node=>({text:node.textContent,height:node.getBoundingClientRect().height,lines:getComputedStyle(node).whiteSpace}));
     assert.ok(quote.text.startsWith('Builder:')&&quote.text.length<=80);assert.equal(quote.lines,'nowrap');
     await pair.$eval('#chat-messages .pair-msg:last-child .msg-parent',button=>button.click());
     assert.equal(await pair.$eval('#chat-messages .pair-msg[data-key="'+pairRoot+'"]',row=>row.classList.contains('msg-reply-highlight')),true);
    }
    await pair.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});
