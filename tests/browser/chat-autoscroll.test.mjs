import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const motionJS=await readFile('internal/controller/web/motion.js','utf8');
const mascotJS=await readFile('internal/controller/web/mascot.js','utf8');
const mascotCSS=await readFile('internal/controller/web/mascot.css','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const tokensCSS=await readFile('internal/controller/web/vbox-tokens.css','utf8');
const vboxCSS=await readFile('internal/controller/web/vbox-c.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

const atBottom=element=>element.scrollHeight-element.scrollTop-element.clientHeight;

test('new messages follow the bottom without stealing an intentionally scrolled transcript',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'railway'};
 const secondBox={id:'observer',name:'Observer',state:'running',defaultAgent:'claude',provider:'railway'};
 const timestamp=index=>new Date(Date.UTC(2026,8,22,0,index)).toISOString();
 let messages=Array.from({length:30},(_,index)=>({
  id:'message-'+index,direction:index%2?'agent':'user',state:'delivered',
  text:'Message '+index+' '+('content '.repeat(12)),createdAt:timestamp(index),updatedAt:timestamp(index)
 }));
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/motion.js'){res.setHeader('Content-Type','text/javascript');return res.end(motionJS)}
  if(path==='/mascot.js'){res.setHeader('Content-Type','text/javascript');return res.end(mascotJS)}
  if(path==='/mascot.css'){res.setHeader('Content-Type','text/css');return res.end(mascotCSS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/vbox-tokens.css'){res.setHeader('Content-Type','text/css');return res.end(tokensCSS)}
  if(path==='/vbox-c.css'){res.setHeader('Content-Type','text/css');return res.end(vboxCSS)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box,secondBox]));
  if(path==='/v1/tool-presets'||path==='/v1/agent-roles')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let body='';req.on('data',chunk=>body+=chunk);
   return req.on('end',()=>{
    const request=JSON.parse(body),createdAt=timestamp(messages.length+1);
    const message={id:'sent-'+messages.length,direction:'user',state:'delivered',text:request.text,createdAt,updatedAt:createdAt};
    messages=[...messages,message];res.end(JSON.stringify({message}));
   });
  }
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify(messages));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.setViewport({width:900,height:620,deviceScaleFactor:1});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Message 29'));
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await page.$eval('#chat-messages',atBottom)<3,'a newly opened chat starts at its latest message');

  messages=[...messages,{id:'new-message',direction:'agent',state:'delivered',text:'NEW MESSAGE AT THE BOTTOM '+('new '.repeat(20)),createdAt:timestamp(31),updatedAt:timestamp(31)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('NEW MESSAGE AT THE BOTTOM'));
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await page.$eval('#chat-messages',atBottom)<3,'a new message keeps a followed chat pinned to the bottom');

  await page.$eval('#chat-messages',element=>{element.scrollTop=0});
  await page.type('#chat-input','MY NEW MESSAGE');
  await page.click('#send');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('MY NEW MESSAGE'));
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await page.$eval('#chat-messages',atBottom)<3,'sending a new message moves the conversation to the bottom');

  await page.$eval('#chat-messages',element=>{element.scrollTop=0});
  await page.waitForFunction(()=>document.querySelector('#chat-messages').scrollTop===0);
  messages=[...messages,{id:'while-reading',direction:'agent',state:'delivered',text:'MESSAGE WHILE READING',createdAt:timestamp(32),updatedAt:timestamp(32)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('MESSAGE WHILE READING'));
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await page.$eval('#chat-messages',element=>element.scrollTop)<20,'new output must not yank a reader away from older messages');
  assert.equal(await page.$eval('#chat-new-messages',button=>button.hidden),false,'new output is discoverable while reading older messages');
  assert.equal(await page.$eval('[data-box-id="builder"] .unread',badge=>badge.textContent),'1','the left list counts unread replies even in the active chat');
  await page.screenshot({path:'/tmp/vmbox-new-message-desktop.png'});
  await page.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await page.waitForFunction(()=>innerWidth===390&&document.querySelector('#chat-app').classList.contains('in-chat')&&!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden&&document.querySelector('#chat-messages').scrollHeight>document.querySelector('#chat-messages').clientHeight);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  await page.$eval('#chat-messages',element=>element.scrollTo({top:0,behavior:'instant'}));
  await page.waitForFunction(()=>document.querySelector('#chat-messages').scrollTop<3);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
  messages=[...messages,{id:'while-reading-mobile',direction:'agent',state:'delivered',text:'NEW REPLY ON MOBILE',createdAt:timestamp(33),updatedAt:timestamp(33)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#chat-new-messages').hidden);
  await new Promise(resolve=>setTimeout(resolve,250));
  await page.screenshot({path:'/tmp/vmbox-new-message-mobile.png'});
  await page.click('#chat-new-messages');
  assert.ok(await page.$eval('#chat-messages',atBottom)<3,'the new message control jumps to the latest reply');
  assert.equal(await page.$eval('#chat-new-messages',button=>button.hidden),true);
  assert.equal(await page.$eval('[data-box-id="builder"] .unread',badge=>badge.hidden),true,'the left badge clears after jumping to the new reply');

  await page.setViewport({width:900,height:620,deviceScaleFactor:1,isMobile:false,hasTouch:false});
  await page.waitForSelector('[data-box-id="observer"] .chat-meta');
  await page.click('[data-box-id="observer"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Observer');
  await page.click('[data-box-id="builder"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('MESSAGE WHILE READING'));
  messages=[...messages,{id:'after-return',direction:'agent',state:'delivered',text:'VISIBLE AFTER RETURN',createdAt:timestamp(34),updatedAt:timestamp(34)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('VISIBLE AFTER RETURN'));
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await page.$eval('#chat-messages',atBottom)<3,'a chat reopened at the bottom keeps following new replies');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
