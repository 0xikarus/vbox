import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-c.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile(root+name)])));
const puts=[],deletes=[];
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://local').pathname;
 if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
 if(path.slice(1) in assets){res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':'text/css');return res.end(assets[path.slice(1)])}
 if(path==='/v1/push/subscriptions'){
  let raw='';for await(const chunk of req)raw+=chunk;
  (req.method==='PUT'?puts:deletes).push(JSON.parse(raw));res.setHeader('Content-Type','application/json');return res.end('{}');
 }
 if(path.startsWith('/v1/')){
  res.setHeader('Content-Type','application/json');
  const values={'/v1/whoami':{role:'owner'},'/v1/logical-boxes':[],'/v1/grid-boxes':[],'/v1/box-conversations':[],'/v1/profile-usage':[],'/v1/chat-commands':[],'/v1/push/vapid-key':{publicKey:'BAAAAA'}};
  return res.end(JSON.stringify(values[path]??{}));
 }
 res.statusCode=404;res.end();
});
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
async function page(permission){
 const p=await browser.newPage();
 await p.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
 await p.setUserAgent('Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Mobile Safari/537.36');
 await p.evaluateOnNewDocument(initial=>{
  window.__pushPermission=initial;window.__permissionRequests=0;window.__subscription=null;
  const subscription=()=>({endpoint:'https://push.example/sub/mobile',getKey:name=>new Uint8Array(name==='p256dh'?65:16).buffer,unsubscribe:async()=>{window.__subscription=null;return true}});
  const reg={pushManager:{getSubscription:async()=>window.__subscription,subscribe:async()=>window.__subscription=subscription()}};
  Object.defineProperty(navigator,'serviceWorker',{configurable:true,value:{register:async()=>reg,addEventListener:()=>{}}});
  window.PushManager=function(){};
  window.Notification=class {static get permission(){return window.__pushPermission}static async requestPermission(){window.__permissionRequests++;window.__pushPermission='granted';return 'granted'}};
 },permission);
 await p.goto('http://127.0.0.1:'+server.address().port+'/chat');
 await p.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
 await p.click('#chat-menu');
 return p;
}
try{
 await test('Android install entry and push permission check follow browser state',async()=>{
  puts.length=0;deletes.length=0;
  const p=await page('default');
  assert.equal(await p.$eval('#install-app',el=>el.hidden),false);
  assert.deepEqual(await p.$eval('#push-toggle',el=>[el.textContent,el.getAttribute('aria-label')]),['Off','Enable notifications']);
  assert.equal(await p.evaluate(()=>window.__permissionRequests),0);
  await p.click('#install-app');
  assert.match(await p.$eval('#install-status',el=>el.textContent),/Chrome.*Install app/);
  await p.click('#push-toggle');
  await p.waitForFunction(()=>document.querySelector('#push-toggle').textContent==='On');
  assert.equal(await p.evaluate(()=>window.__permissionRequests),1);
  assert.equal(puts.length,1);
  assert.match(await p.$eval('#push-status',el=>el.textContent),/subscription active/);
  await p.click('#push-check');await p.waitForFunction(()=>document.querySelector('#push-check').disabled===false);
  assert.ok(puts.length>=2);
  if(process.env.VMBOX_MOBILE_SCREENSHOTS)await p.screenshot({path:process.env.VMBOX_MOBILE_SCREENSHOTS});
  await p.click('#push-toggle');
  await p.waitForFunction(()=>document.querySelector('#push-toggle').textContent==='Off');
  assert.equal(deletes.length,1);
  await p.click('#push-toggle');
  await p.waitForFunction(()=>document.querySelector('#push-toggle').textContent==='On');
  await p.click('#logout');
  await p.waitForFunction(()=>window.__subscription===null);
  assert.equal(deletes.length,2,'logging out removes the device subscription');
  await p.close();
 });
 await test('denied mobile permission points to Android or Chrome settings',async()=>{
  const p=await page('denied');
  assert.equal(await p.$eval('#push-toggle',el=>el.disabled),true);
  assert.match(await p.$eval('#push-status',el=>el.textContent),/Android app or Chrome site settings/);
  if(process.env.VMBOX_MOBILE_SCREENSHOTS_DENIED)await p.screenshot({path:process.env.VMBOX_MOBILE_SCREENSHOTS_DENIED});
  await p.click('#push-check');
  assert.equal(await p.evaluate(()=>window.__permissionRequests),0);
  await p.close();
 });
}finally{await browser.close();await new Promise(done=>server.close(done))}
