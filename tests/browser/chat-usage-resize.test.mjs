import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const usage={profiles:[{application:'claude',name:'work',boxes:['Builder'],observedAt:'2026-09-24T03:00:00Z',checkedAt:'2026-09-24T03:01:00Z',snapshot:{windows:[
 {name:'session',usedPercent:25,resetsAt:'2026-09-24T04:00:00Z'},
 {name:'weekly_all',usedPercent:90,resetsAt:'2026-09-30T00:00:00Z'}
],spend:{currency:'USD',limit:100,used:40},balances:[{unit:'USD',amount:12}],rateCaps:[{model:'example',type:'RPM',amount:100}],source:'live box'}},
 {application:'opencode',name:'spare',boxes:[],observedAt:'2026-09-24T03:00:00Z',snapshot:{source:'saved profile',windows:[{name:'primary',usedPercent:40}]}}]};

test('usage shows remaining capacity and Conversations width can be resized and restored',async()=>{
 const server=http.createServer((request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'){response.setHeader('Content-Type','text/html');return response.end(files['chat.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return response.end(files[path.slice(1)])}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify([{id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'railway'}]));
  if(path==='/v1/logical-boxes/builder/messages'||path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/chat-commands')return response.end('[]');
  if(path==='/v1/profile-usage')return response.end(JSON.stringify(usage));
  if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
  response.statusCode=404;response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await page.waitForFunction(()=>document.querySelector('#usage-toggle')?.textContent.includes('10% left'));
  assert.match(await page.$eval('#usage-toggle',element=>element.title),/Lowest reported remaining/);
  await page.click('#usage-toggle');
  await page.waitForFunction(()=>document.querySelector('#usage-list')?.textContent.includes('75% remaining'));
  const text=await page.$eval('#usage-list',element=>element.textContent);
  assert.match(text,/10% remaining/);
  assert.match(text,/60 USD remaining/);
  assert.match(text,/Available balances: USD 12/);
  assert.match(text,/remaining requests unavailable/);
  assert.match(text,/No running box · Source: saved profile/);
  assert.deepEqual(await page.$$eval('.usage-track',tracks=>tracks.map(track=>track.getAttribute('aria-valuenow'))),['75','10','60']);
  await page.click('#usage-modal button[data-close]');

  const initial=await page.$eval('#chat-list',element=>element.getBoundingClientRect().width);
  const rect=await page.$eval('#chat-resizer',element=>{const r=element.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}});
  await page.mouse.move(rect.x,rect.y);await page.mouse.down();await page.mouse.move(rect.x+120,rect.y,{steps:5});await page.mouse.up();
  const resized=await page.$eval('#chat-list',element=>element.getBoundingClientRect().width);
  assert.ok(resized>=initial+100,'dragging the separator widens the sidebar');
  assert.equal(await page.evaluate(()=>Number(localStorage.getItem('vmboxChatSidebarWidth'))),Math.round(resized));
  await page.reload();await page.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
  assert.ok(Math.abs(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)-resized)<=2,'sidebar width survives reload');
  await page.focus('#chat-resizer');await page.keyboard.press('ArrowLeft');
  assert.ok(Math.abs(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)-(resized-24))<=2);
  await page.keyboard.press('Home');
  assert.equal(Math.round(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)),240);
  await page.keyboard.press('End');
  assert.ok(await page.$eval('#chat-main',element=>element.getBoundingClientRect().width)>=448,'the transcript keeps usable width');
  await page.setViewport({width:900,height:800});
  assert.ok(await page.$eval('#chat-main',element=>element.getBoundingClientRect().width)>=448,'narrow desktop still has room for messages');
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  assert.equal(await page.$eval('#chat-resizer',element=>getComputedStyle(element).display),'none');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
