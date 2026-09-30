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
const vboxCSS=await readFile('internal/controller/web/vbox-c.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

test('re-rendering a chat keeps existing messages and avatars still',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'railway'};
 const timestamp=index=>new Date(Date.UTC(2026,8,22,0,index)).toISOString();
 let messages=Array.from({length:6},(_,index)=>({
  id:'message-'+index,direction:index%2?'agent':'user',state:'delivered',
  text:'Message '+index,createdAt:timestamp(index),updatedAt:timestamp(index)
 }));
 const assets={'/chat':[html,'text/html'],'/chat.js':[js,'text/javascript'],'/motion.js':[motionJS,'text/javascript'],'/mascot.js':[mascotJS,'text/javascript'],'/mascot.css':[mascotCSS,'text/css'],'/chat.css':[css,'text/css'],'/vbox-c.css':[vboxCSS,'text/css'],'/app.css':[appcss,'text/css'],'/markdown.js':[markdownJS,'text/javascript'],'/model-picker.js':[modelPickerJS,'text/javascript']};
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(assets[path]){res.setHeader('Content-Type',assets[path][1]);return res.end(assets[path][0])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/tool-presets'||path==='/v1/agent-roles')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
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
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Message 5'));
  assert.equal(await page.$$eval('#chat-messages .msg-enter',nodes=>nodes.length),0,'opening a chat paints its history without entrance animations');
  await page.$eval('#chat-messages .msg[data-key="message-5"] > .msg-avatar',avatar=>{avatar.dataset.probe='kept'});

  messages=[...messages,{id:'message-6',direction:'user',state:'delivered',text:'Message 6',createdAt:timestamp(6),updatedAt:timestamp(6)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Message 6'));
  assert.deepEqual(await page.$$eval('#chat-messages .msg-enter',nodes=>nodes.map(node=>node.dataset.key)),['message-6'],'only the new message animates in');
  assert.equal(await page.$eval('#chat-messages .msg[data-key="message-5"] > .msg-avatar',avatar=>avatar.dataset.probe),'kept','the unchanged avatar survives the re-render');
  assert.equal(await page.$$eval('#chat-messages .msg:not(.msg-enter)',nodes=>nodes.filter(node=>node.getAnimations().length).length),0,'existing messages do not replay an animation');

  await page.click('#chat-info');
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden&&document.querySelector('#inspect-avatar .inspect-hero-mascot'));
  await page.$eval('#inspect-avatar .inspect-hero-mascot',mascot=>{mascot.dataset.probe='kept'});
  messages=[...messages,{id:'message-7',direction:'agent',state:'delivered',text:'Message 7',createdAt:timestamp(7),updatedAt:timestamp(7)}];
  await page.$eval('#refresh',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Message 7'));
  assert.equal(await page.$eval('#inspect-avatar .inspect-hero-mascot',mascot=>mascot.dataset.probe),'kept','the details mascot keeps its state across refreshes');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
