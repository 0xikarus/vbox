import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {WebSocketServer} from 'ws';
import puppeteer from 'puppeteer-core';

test('real noVNC fallback cursor stays inside fullscreen viewer and detaches cleanly',async()=>{
 const bundle=await readFile('internal/controller/web/novnc.js');
 const server=http.createServer((req,res)=>{if(req.url==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(bundle)}res.end('<div id="desktop" style="width:400px;height:300px"></div><button id="full">Fullscreen</button><script src="/novnc.js"></script><script>rfb=new NoVNC.default(document.querySelector("#desktop"),"ws://"+location.host);rfb.showDotCursor=true;document.querySelector("#full").onclick=()=>document.querySelector("#desktop").requestFullscreen();</script>')});
 const wss=new WebSocketServer({server});
 wss.on('connection',ws=>{
  let step=0;ws.send(Buffer.from('RFB 003.008\n'));
  ws.on('message',data=>{
   if(step>=3&&data[0]===248){const response=Buffer.from(data);response.writeUInt32BE(0,4);return setTimeout(()=>ws.send(response),30)}
   if(step++===0)return ws.send(Buffer.from([1,1]));
   if(step===2)return ws.send(Buffer.alloc(4));
   if(step===3){const init=Buffer.alloc(25);init.writeUInt16BE(100,0);init.writeUInt16BE(100,2);init[4]=32;init[5]=24;init[7]=1;for(const i of [8,10,12])init.writeUInt16BE(255,i);init[14]=16;init[15]=8;init.writeUInt32BE(1,20);init[24]=88;ws.send(init);
    const update=Buffer.alloc(34);update.writeUInt16BE(1,2);update.writeUInt16BE(2,8);update.writeUInt16BE(2,10);update.writeInt32BE(-239,12);update.fill(255,16,32);update[32]=192;update[33]=192;ws.send(update);
    const fence=Buffer.alloc(9);fence[0]=248;fence.writeUInt32BE(0x80000000,4);ws.send(fence);
   }
  });
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.setViewport({width:900,height:700,hasTouch:true});await page.goto('http://127.0.0.1:'+server.address().port);
  const cursor='#desktop canvas[style*="pointer-events: none"]';await page.waitForSelector(cursor);await page.waitForFunction(()=>[...document.querySelectorAll('#desktop canvas')].some(c=>c.style.pointerEvents==='none'&&c.width===2));
  await page.waitForFunction(()=>rfb._supportsFence);
  const latency=await page.evaluate(()=>new Promise((resolve,reject)=>{
   const started=performance.now(),timer=setTimeout(()=>reject(Error('Fence response timed out')),2000);
   rfb.addEventListener('fenceresponse',e=>{if(e.detail.payload==='vmbox:test'){clearTimeout(timer);resolve(performance.now()-started)}});
   if(!rfb.requestLatencyProbe('vmbox:test'))reject(Error('Probe not sent'));
  }));
  assert.ok(latency>=25&&latency<2000,'VNC fence measures server response delay');
  await page.click('#full');await page.waitForFunction(()=>!!document.fullscreenElement);
  const position=await page.evaluate(()=>{const canvas=[...document.querySelectorAll('#desktop canvas')].find(c=>c.style.pointerEvents!=='none'),r=canvas.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}});await page.mouse.move(position.x,position.y);
  assert.equal(await page.$eval(cursor,e=>document.fullscreenElement.contains(e)),true);
  assert.equal(await page.$eval(cursor,e=>getComputedStyle(e).visibility),'visible');
  await page.evaluate(()=>document.exitFullscreen());await page.evaluate(()=>rfb.disconnect());await page.waitForFunction(()=>!document.querySelector('#desktop canvas[style*="pointer-events: none"]'));
  assert.deepEqual(errors,[]);
 }finally{await browser.close();for(const ws of wss.clients)ws.terminate();wss.close();await new Promise(r=>server.close(r))}
});
