import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const now=new Date().toISOString();
const box={id:'builder',name:'builder',state:'running',defaultAgent:'claude'};
const messages=[
 {id:'m1',threadId:'t1',direction:'user',state:'delivered',text:'A long question about the provider settings and the running boxes.',createdAt:now,updatedAt:now},
 {id:'m2',threadId:'t1',parentMessageId:'m1',direction:'assistant',state:'delivered',text:'The primary provider is healthy and both boxes are running.',createdAt:now,updatedAt:now},
 {id:'m3',direction:'user',state:'delivered',text:'Please send this to the reviewer when ready.',createdAt:now,updatedAt:now}
];

test('desktop thread leaves the full conversation visible with and without Details',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/browser-session'){res.statusCode=204;return res.end()}
   if(path.endsWith('/desktop')&&req.method==='POST'){res.statusCode=503;return res.end('{"error":"Desktop connection failed; check runtime and authentication."}')}
   if(path.endsWith('/messages'))return res.end(JSON.stringify(messages));
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   if(['/v1/box-conversations','/v1/chat-commands','/v1/tool-presets','/v1/box-activity'].includes(path))return res.end('[]');
   return res.end('{}');
  }
  const file=resolve(web,path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.html':'text/html'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [1440,1024]){
   const page=await browser.newPage();await page.setViewport({width,height:900});
   await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
   await page.waitForSelector('.msg-thread');await page.$eval('.msg-thread',node=>node.click());
   await page.waitForFunction(()=>!document.querySelector('#thread-panel').hidden);
   const bounds=()=>page.evaluate(()=>({panel:document.querySelector('#thread-panel').getBoundingClientRect().left,bubbles:[...document.querySelectorAll('#chat-messages .msg')].map(node=>node.getBoundingClientRect().right),main:document.querySelector('#chat-main').getBoundingClientRect().right}));
   let result=await bounds();assert(result.bubbles.length>0);assert(result.bubbles.every(right=>right<=result.panel+1),JSON.stringify(result));assert(result.main<=result.panel+1,JSON.stringify(result));
   await page.$eval('#chat-info',node=>node.click());await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
   result=await bounds();assert(result.bubbles.every(right=>right<=result.panel+1),JSON.stringify(result));
   await page.close();
  }
  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await mobile.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);await mobile.waitForSelector('#chat-conversation:not([hidden])');
  await mobile.$eval('#chat-header-desktop',node=>node.click());await mobile.waitForFunction(()=>!document.querySelector('#takeover-error').hidden);
  assert.equal(await mobile.$eval('#takeover-error',node=>node.textContent),'Desktop connection failed; check runtime and authentication.');
  await mobile.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
