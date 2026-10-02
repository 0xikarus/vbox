import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=new Map(await Promise.all(['chat.html','chat.js','chat.css','vbox-c.css','vbox-tokens.css','app.css','motion.js','mascot.js','mascot.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name)])));
const names='ABCDEFGHIJKLM'.split('');
const boxes=names.map(id=>({id,name:id,state:'running',defaultAgent:'codex',provider:'railway',role:'worker',volumeName:'volume-'+id}));
async function waitUntil(check,timeout=10000){const end=Date.now()+timeout;while(!check()&&Date.now()<end)await new Promise(resolve=>setTimeout(resolve,30));assert.ok(check(),'timed out waiting for requests')}

test('chat history cache keeps opened chats above prefetched chats and restores open order',async()=>{
 const historyRequests=[],active={count:0,max:0};
 const historyStart=Date.now()-60_000;
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0],asset=path==='/chat'?'chat.html':path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',asset.endsWith('.html')?'text/html':asset.endsWith('.css')?'text/css':'text/javascript');return res.end(assets.get(asset))}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-conversations'||path==='/v1/tool-presets')return res.end('[]');
  const match=path.match(/^\/v1\/logical-boxes\/([A-M])\/messages$/);
  if(match){
   const limit=Number(new URL(req.url,'http://local').searchParams.get('limit'));
   if(limit===100){historyRequests.push(match[1]);active.count++;active.max=Math.max(active.max,active.count)}
   const messages=Array.from({length:20},(_,i)=>({id:match[1]+'-message-'+i,direction:'agent',state:'delivered',text:'History for '+match[1]+' — long transcript entry '+i+' '.repeat(80),createdAt:new Date(historyStart+i*1000).toISOString(),updatedAt:new Date(historyStart+i*1000).toISOString()}));
   return setTimeout(()=>{if(limit===100)active.count--;res.end(JSON.stringify(messages))},limit===100?120:0);
  }
  if(path.endsWith('/messages'))return res.end('[]');
  res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const url='http://127.0.0.1:'+server.address().port+'/chat';
  await page.goto(url);await page.waitForSelector('[data-box-id="A"]');
  // On a blank device the visible rows prefetch in idle time with at most two requests in flight.
  await page.waitForFunction(()=>document.querySelector('[data-box-id="A"]'));
  for(const name of ['A','B','C']){
   await page.$eval('[data-box-id="'+name+'"]',row=>row.click());
   await page.waitForFunction(expected=>location.hash==='#box='+expected&&document.querySelector('#chat-messages').textContent.includes('History for '+expected),{},name);
   await page.$eval('#chat-back',button=>button.click());
  }
  const opened=await page.evaluate(()=>JSON.parse(localStorage.getItem('vmboxChatOpenOrder')));
  assert.deepEqual(opened.slice(0,3),['box:C','box:B','box:A']);
  while(active.count)await new Promise(resolve=>setTimeout(resolve,20));
  active.max=0;
  // A pile of never-opened prefetches must not evict any opened transcript.
  await page.evaluate(()=>{for(const name of 'DEFGHIJKLM')document.querySelector('[data-box-id="'+name+'"]').dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}))});
  await waitUntil(()=>[...'DEFGHIJKLM'].every(name=>historyRequests.includes(name))&&active.count===0);
  const beforeA=historyRequests.filter(id=>id==='A').length;
  await page.$eval('[data-box-id="A"]',row=>row.click());
  await page.waitForFunction(()=>location.hash==='#box=A'&&document.querySelector('#chat-messages').textContent.includes('History for A'));
  assert.equal(historyRequests.filter(id=>id==='A').length,beforeA,'prefetched entries evict before opened A');
  assert.ok(active.max<=2,'no more than two prefetch requests run concurrently');
  assert.deepEqual((await page.evaluate(()=>JSON.parse(localStorage.getItem('vmboxChatOpenOrder')))).slice(0,3),['box:A','box:C','box:B']);
  await page.$eval('#chat-back',button=>button.click());
  await page.reload();await page.waitForSelector('[data-box-id="A"]');
  const fromReload=historyRequests.length;
  await page.waitForFunction(()=>document.querySelector('#chat-entries').children.length>0);
  await waitUntil(()=>historyRequests.length>=fromReload+3&&active.count===0);
  await new Promise(resolve=>setTimeout(resolve,700)); // allow the throttled renderer to consume all prefetched responses
  assert.deepEqual(historyRequests.slice(fromReload,fromReload+3),['A','C','B'],'reload prefetches recent opens first');
  const captureDir=process.env.SWITCH_CAPTURE_DIR||'/tmp/vmbox-chat-switch';await mkdir(captureDir,{recursive:true});
  await page.emulateCPUThrottling(4);
  const switchTrace=[];
  for(const name of ['B','C','A']){
   const frames=await page.evaluate(async name=>{
    document.querySelector('[data-box-id="'+name+'"]').click();
    const frames=[];
    for(let i=0;i<5;i++)await new Promise(resolve=>requestAnimationFrame(()=>{
     const el=document.querySelector('#chat-messages');
     frames.push({name:document.querySelector('#chat-header-name').textContent,text:el.textContent,loading:!document.querySelector('#chat-loading').hidden,scrollTop:el.scrollTop,scrollHeight:el.scrollHeight,atBottom:el.scrollHeight-el.scrollTop-el.clientHeight<3});resolve();
    }));
    return frames;
   },name);
   const firstFrame=frames[0];switchTrace.push({name,frames});
   assert.ok(firstFrame.text.includes('History for '+name),'cached chat '+name+' paints by the first frame: '+JSON.stringify(frames.map(frame=>({name:frame.name,text:frame.text.slice(0,80),loading:frame.loading}))));
   assert.equal(firstFrame.loading,false,'chat '+name+' does not show loading surface');
   assert.equal(firstFrame.atBottom,true,'chat '+name+' starts at the newest message');
   assert.ok(frames.every(frame=>frame.name===name&&frame.atBottom&&Math.abs(frame.scrollTop-firstFrame.scrollTop)<2&&frame.scrollHeight===firstFrame.scrollHeight),'chat '+name+' stays steady across frames');
   await page.screenshot({path:captureDir+'/'+name+'.png'});
   await page.$eval('#chat-back',button=>button.click());
  }
  await writeFile(captureDir+'/trace.json',JSON.stringify(switchTrace,null,2));
  const beforeHidden=historyRequests.length;
  await page.evaluate(()=>{
   Object.defineProperty(document,'hidden',{configurable:true,get:()=>true});
   document.querySelector('[data-box-id="M"]').dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));
  });
  await new Promise(resolve=>setTimeout(resolve,300));
  assert.equal(historyRequests.length,beforeHidden,'hidden document does not start prefetch');
  await page.evaluate(()=>{
   Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});
   Object.defineProperty(navigator,'connection',{configurable:true,get:()=>({saveData:true})});
   document.querySelector('[data-box-id="K"]').dispatchEvent(new PointerEvent('pointerdown',{bubbles:true}));
  });
  await new Promise(resolve=>setTimeout(resolve,300));
  assert.equal(historyRequests.length,beforeHidden,'save-data mode does not start prefetch');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
