import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=new Map(await Promise.all(['chat.html','chat.js','chat.css','vbox-c.css','vbox-tokens.css','app.css','motion.js','mascot.js','mascot.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name)])));
const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElFTkSuQmCC','base64');
const boxes=Array.from({length:12},(_,i)=>({id:'box-'+i,name:'contact '+String(i).padStart(2,'0'),state:'running',defaultAgent:'codex',provider:'railway',role:'worker',volumeName:'volume-'+i}));
const stamp=new Date(Date.now()-60_000).toISOString();
const transcript=Array.from({length:125},(_,i)=>({id:'m'+i,direction:i%3?'agent':'user',state:'delivered',text:i%11===0?'MCP · tool returned '+i:'Message '+i+' with enough detail to wrap across a few lines in a mobile chat. '.repeat(2),createdAt:new Date(Date.now()-125_000+i*1000).toISOString(),updatedAt:stamp,...(i%17===0?{images:[{id:'image-'+i,number:1,mediaType:'image/png'}]}:{})}));
const phase=process.env.SWIPE_PHASE||'after';
const captureRoot=process.env.SWIPE_CAPTURE_DIR||'/tmp/vmbox-swipe-smooth';

test('forward swipe keeps a heavy transcript and list stable',async()=>{
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0],asset=path==='/chat'?'chat.html':path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',asset.endsWith('.html')?'text/html':asset.endsWith('.css')?'text/css':'text/javascript');return res.end(assets.get(asset))}
  if(path.includes('/images/')){res.setHeader('Content-Type','image/png');return setTimeout(()=>res.end(png),600)}
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-conversations'||path==='/v1/tool-presets')return res.end('[]');
  if(path.endsWith('/messages')){res.setHeader('Content-Type','application/json');return setTimeout(()=>res.end(JSON.stringify(path.includes('box-1/')?transcript:transcript.slice(0,4))),path.includes('box-1/')?350:0)}
  if(path.startsWith('/v1/')){res.setHeader('Content-Type','application/json');return res.end('{}')}
  res.end('');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true,deviceScaleFactor:1});await page.emulateCPUThrottling(4);
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=box-0');
  await page.waitForFunction(()=>document.querySelector('#chat-entries [data-box-id="box-1"]')&&document.querySelector('#chat-messages .msg'));
  await page.$eval('#chat-back',el=>el.click());await new Promise(resolve=>setTimeout(resolve,280));
  await page.evaluate(()=>{window.swipeFrames=[];window.recordSwipe=true;const main=document.querySelector('#chat-main'),messages=document.querySelector('#chat-messages'),list=document.querySelector('#chat-list');const rows=[...document.querySelectorAll('#chat-entries li[data-box-id]')].slice(0,5);list.addEventListener('touchend',()=>{window.swipeRelease={before:new DOMMatrix(getComputedStyle(main).transform).m41};queueMicrotask(()=>{window.swipeRelease.after=new DOMMatrix(getComputedStyle(main).transform).m41})},{capture:true,once:true});function frame(){if(!window.recordSwipe)return;const transform=new DOMMatrix(getComputedStyle(main).transform);window.swipeFrames.push({t:performance.now(),x:transform.m41,scrollTop:messages.scrollTop,scrollHeight:messages.scrollHeight,header:document.querySelector('#chat-header-name').textContent,rows:rows.map(row=>{const r=row.getBoundingClientRect();return [row.dataset.boxId,r.top,r.left-list.getBoundingClientRect().left]})});requestAnimationFrame(frame)}requestAnimationFrame(frame)});
  const dir=captureRoot+'/'+phase;await mkdir(dir,{recursive:true});
  const row=await page.$eval('[data-box-id="box-1"]',el=>{const r=el.getBoundingClientRect();return {x:310,y:r.top+r.height/2}});
  await page.touchscreen.touchStart(row.x,row.y);
  let index=0;
  for(const x of [285,255,225,195,165,135,105]){await page.touchscreen.touchMove(x,row.y);await new Promise(resolve=>setTimeout(resolve,80));await page.screenshot({path:dir+'/frame-'+String(index++).padStart(2,'0')+'.png'})}
  await page.touchscreen.touchEnd();
  await new Promise(resolve=>setTimeout(resolve,750));
  await page.screenshot({path:dir+'/frame-'+String(index++).padStart(2,'0')+'.png'});
  const {trace,release}=await page.evaluate(()=>{window.recordSwipe=false;return {trace:window.swipeFrames,release:window.swipeRelease}});
  await writeFile(dir+'/trace.json',JSON.stringify(trace,null,2));
  await writeFile(dir+'/release.json',JSON.stringify(release,null,2));
  assert.equal(await page.evaluate(()=>location.hash),'#box=box-1');
  if(phase==='after'){
   const visible=trace.filter(frame=>frame.x<390&&frame.x>0);
   assert.ok(visible.length>5,'recorded visible drag frames');
   assert.ok(Math.abs(release.before-release.after)<1,'release starts settling from the last drag position');
   assert.ok(visible.every((frame,i)=>i===0||frame.x<=visible[i-1].x+1),'pane moves monotonically with the finger');
   assert.ok(visible.every(frame=>frame.rows.every(([id,y,x],i)=>id===visible[0].rows[i][0]&&Math.abs(y-visible[0].rows[i][1])<1&&Math.abs(x-visible[0].rows[i][2])<1)),'list rows keep their positions and order');
   const settled=trace.filter(frame=>frame.x<300&&frame.scrollHeight>1000);
   assert.ok(settled.length>0&&settled.every(frame=>Math.abs(frame.scrollTop-settled[0].scrollTop)<2),'transcript scroll stays fixed once visible');
  }
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
