import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));

test('controller classification drives the chat mascot and expires cleanly',async()=>{
 const now=new Date().toISOString();
 const box={id:'builder',name:'builder',state:'running',defaultAgent:'codex',provider:'railway'};
 const messages=[{id:'m1',direction:'agent',state:'delivered',text:'The build finished.',createdAt:now,updatedAt:now}];
 let mood='angry',activity='idle';
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.html')?'text/html':path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'){
   res.setHeader('X-Vmbox-Agent-Busy','false');
   if(mood)res.setHeader('X-Vmbox-Mascot-Mood',mood);
   if(activity)res.setHeader('X-Vmbox-Mascot-Activity',activity);
   return res.end(JSON.stringify(messages));
  }
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  const pose=()=>page.$eval('[data-box-id="builder"] .avatar-mascot svg',el=>el.dataset.mood);
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .avatar-mascot svg')?.dataset.mood==='angry',{timeout:8000});
  assert.equal(await pose(),'angry');
  mood='happy';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .avatar-mascot svg')?.dataset.mood==='happy',{timeout:8000});
  activity='working';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .avatar-mascot svg')?.dataset.mood==='working',{timeout:8000});
  mood='';activity='';
  await page.$eval('#refresh',el=>el.click());
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .avatar-mascot svg')?.dataset.mood==='idle',{timeout:8000});
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
