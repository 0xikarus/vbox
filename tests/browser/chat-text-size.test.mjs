import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const now=new Date().toISOString();
const box={id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'shared-worker',createdAt:now,updatedAt:now};
const message={id:'m1',direction:'agent',state:'delivered',text:'A readable message in the chat.',createdAt:now,updatedAt:now};

test('text size defaults by viewport and persists a live menu choice',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
   if(path.endsWith('/messages'))return res.end(JSON.stringify([message]));
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const pages={'/':'index.html','/chat':'chat.html','/grid':'grid.html','/boxes/builder':'workspace.html'};
  const file=resolve(web,pages[path]||path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html','.woff2':'font/woff2'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 const base=`http://127.0.0.1:${server.address().port}`;
 try{
  const page=await browser.newPage();
  const sizes=async()=>page.evaluate(()=>({bubble:parseFloat(getComputedStyle(document.querySelector('#chat-messages .msg')).fontSize),list:parseFloat(getComputedStyle(document.querySelector('#chat-entries .name')).fontSize),scale:getComputedStyle(document.documentElement).getPropertyValue('--vb-text-scale').trim(),choice:document.documentElement.dataset.textSize||''}));
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat#box=builder');
  await page.waitForSelector('#chat-messages .msg');
  const small=await sizes();assert.equal(small.choice,'');assert.equal(small.scale,'.9');assert.ok(small.bubble>=13.5&&small.bubble<=14.5,`mobile default bubble ${small.bubble}px`);
  await page.$eval('#chat-menu',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#chat-menu-sheet').hidden);
  await page.click('#text-size-controls [data-size="medium"]');
  const medium=await sizes();assert.equal(medium.scale,'1');assert.ok(medium.bubble>small.bubble&&medium.list>small.list);
  await page.click('#text-size-controls [data-size="large"]');
  const large=await sizes();assert.equal(large.scale,'1.12');assert.ok(large.bubble>medium.bubble&&large.list>medium.list);
  await page.click('#text-size-controls [data-size="xl"]');
  const xl=await sizes();assert.equal(xl.scale,'1.25');assert.ok(xl.bubble>large.bubble&&xl.list>large.list);
  await page.setViewport({width:360,height:800,isMobile:true,hasTouch:true});
  const controls=await page.$$eval('#text-size-controls button',buttons=>buttons.map(button=>{const r=button.getBoundingClientRect();return {width:r.width,height:r.height,right:r.right}}));
  assert.equal(controls.length,4);assert.ok(controls.every(rect=>rect.width>=44&&rect.height>=44&&rect.right<=360),'XL menu keeps four 44px targets on a 360px phone');
  await page.click('#text-size-controls [data-size="large"]');
  await page.evaluateOnNewDocument(()=>requestAnimationFrame(()=>{window.__textSizeAtFirstFrame={choice:document.documentElement.dataset.textSize,scale:getComputedStyle(document.documentElement).getPropertyValue('--vb-text-scale').trim()}}));
  await page.reload();await page.waitForSelector('#chat-messages .msg');assert.equal((await sizes()).choice,'large','stored size applies before app setup');
  assert.deepEqual(await page.evaluate(()=>window.__textSizeAtFirstFrame),{choice:'large',scale:'1.12'},'stored choice is active by the first paint frame');
  await page.evaluate(()=>{localStorage.removeItem('vboxTextSize')});
  await page.setViewport({width:1440,height:900});await page.reload();await page.waitForSelector('#chat-messages .msg');
  const desktop=await sizes();assert.equal(desktop.choice,'');assert.equal(desktop.scale,'1');assert.ok(desktop.bubble>=15&&desktop.bubble<=16);
  await page.evaluate(()=>localStorage.setItem('vboxTextSize','large'));
  for(const path of ['/','/grid','/boxes/builder']){
   await page.goto(base+path);
   assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--vb-text-scale').trim()),'1.12',`${path} applies the stored choice before app setup`);
  }
  await page.close();
 }finally{await browser.close();server.close()}
});
