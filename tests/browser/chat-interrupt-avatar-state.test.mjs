import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile, mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all([
 'chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css',
 'vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'
].map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');

test('interrupt follows selected activity and avatars follow box state',async()=>{
 const now=new Date().toISOString();
 const boxes=[
  {id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'railway'},
  {id:'other',name:'other',state:'running',defaultAgent:'claude',provider:'railway'}
 ];
 let busy=false,streaming=false,interrupts=0,thumbnails=0;
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':path.endsWith('.css')?'text/css':'text/html');return res.end(assets[path])}
  if(path.endsWith('/desktop/screenshot')){thumbnails++;res.setHeader('Content-Type','image/png');return res.end(png)}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-activity')return res.end(JSON.stringify([{boxId:'builder',busy,phrase:busy?'working':''}]));
  if(path==='/v1/logical-boxes/builder/sessions/interactive'&&req.method==='POST')return res.end('{"session":"s1"}');
  if(path==='/v1/logical-boxes/builder/terminal/input'&&req.method==='POST'){interrupts++;return res.end('{}')}
  if(path.endsWith('/messages')){res.setHeader('X-Vmbox-Agent-Busy',String(path.includes('/builder/')&&busy));return res.end(JSON.stringify([{id:'m1',direction:'agent',state:path.includes('/builder/')&&streaming?'streaming':'delivered',text:'Ready.',createdAt:now,updatedAt:now}]))}
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/tool-presets'||path==='/v1/box-conversations')return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  const url='http://127.0.0.1:'+server.address().port+'/chat#box=builder';
  await page.goto(url);
  await page.waitForFunction(()=>document.querySelector('#chat-header-avatar [data-avatar="builder"] img')?.naturalWidth===1);
  assert.equal(await page.$eval('#chat-composer',el=>el.classList.contains('is-processing')),false);
  busy=true;
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('#chat-composer').classList.contains('is-processing'));
  assert.equal(await page.$eval('#chat-interrupt',el=>({visible:getComputedStyle(el).display!=='none',disabled:el.disabled})).then(x=>x.visible&&!x.disabled),true);
  assert.notEqual(await page.$eval('#send',el=>getComputedStyle(el).display),'none');
  if(process.env.VMBOX_CAPTURE_DIR){
   await mkdir(process.env.VMBOX_CAPTURE_DIR,{recursive:true});
   for(const width of [390,1440])for(const theme of ['light','dark']){
    await page.setViewport({width,height:844,deviceScaleFactor:1});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    await page.screenshot({path:process.env.VMBOX_CAPTURE_DIR+'/chat-stop-'+width+'-'+theme+'.png'});
   }
  }
  await page.click('#chat-interrupt');
  await page.waitForFunction(()=>!document.querySelector('#chat-interrupt').disabled);
  assert.equal(interrupts,1);
  busy=false;streaming=true;await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-messages .msg.streaming')&&document.querySelector('#chat-composer').classList.contains('is-processing'));
  assert.equal(await page.$eval('#chat-interrupt',el=>el.disabled),false);
  streaming=false;await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>!document.querySelector('#chat-composer').classList.contains('is-processing'));
  busy=true;await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-composer').classList.contains('is-processing'));
  await page.$eval('[data-box-id="other"]',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='other');
  assert.equal(await page.$eval('#chat-composer',el=>el.classList.contains('is-processing')),false);
  boxes[0].state='hibernated';busy=false;
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="builder"]')?.dataset.state==='hibernated');
  assert.equal(await page.$eval('#chat-entries [data-avatar="builder"]',el=>!!el.querySelector('img')),false);
  await page.$eval('[data-box-id="builder"]',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='builder');
  assert.equal(await page.$eval('#chat-header-avatar [data-avatar="builder"]',el=>!!el.querySelector('img')),false);
  assert.equal(await page.$eval('#chat-composer',el=>el.classList.contains('is-processing')),false);
  boxes[0].state='stopped';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="builder"]')?.dataset.state==='stopped');
  assert.equal(await page.$eval('#chat-header-avatar [data-avatar="builder"]',el=>!!el.querySelector('img')),false);
  if(process.env.VMBOX_CAPTURE_DIR){
   boxes[0].state='hibernated';await page.$eval('#refresh',el=>el.click());
   await page.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="builder"]')?.dataset.state==='hibernated');
   await new Promise(resolve=>setTimeout(resolve,3500));
   for(const width of [390,1440])for(const theme of ['light','dark']){
    await page.setViewport({width,height:844,deviceScaleFactor:1,isMobile:width===390,hasTouch:width===390});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    if(width===390&&await page.$eval('#chat-app',el=>el.classList.contains('in-chat')))await page.click('#chat-back');
    await new Promise(resolve=>setTimeout(resolve,450));
    await page.screenshot({path:process.env.VMBOX_CAPTURE_DIR+'/list-hibernated-'+width+'-'+theme+'.png'});
   }
  }
  boxes[0].state='running';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-entries [data-avatar="builder"] img')?.naturalWidth===1);
  await page.$eval('[data-box-id="builder"]',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('#chat-header-avatar [data-avatar="builder"] img')?.naturalWidth===1);
  assert.ok(thumbnails>=2);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
