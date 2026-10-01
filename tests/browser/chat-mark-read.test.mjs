import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const boxes=[{id:'builder',name:'Builder',state:'running',defaultAgent:'claude'},{id:'reviewer',name:'Reviewer',state:'running',defaultAgent:'codex'}];
const now=Date.now();
const messages={
 builder:[{id:'b1',direction:'agent',text:'First',state:'delivered',createdAt:new Date(now-3000).toISOString()},{id:'b2',direction:'agent',text:'Second',state:'delivered',createdAt:new Date(now-1000).toISOString()}],
 reviewer:[{id:'r1',direction:'agent',text:'Review',state:'delivered',createdAt:new Date(now-2000).toISOString()}],
};

test('read actions clear row badges and synced markers advance across devices',async()=>{
 const markers={};
 const layout={exists:true,groups:[{id:'focus',name:'Focus',collapsed:true}],members:{'box:reviewer':'focus'},mutes:{},pins:[],sections:{}};
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/chat-read-markers'){
    if(req.method==='PUT'){let body='';for await(const chunk of req)body+=chunk;for(const [key,value] of Object.entries(JSON.parse(body)))if(!markers[key]||Date.parse(value)>Date.parse(markers[key]))markers[key]=value}
    return res.end(JSON.stringify(markers));
   }
   if(path==='/v1/chat-sidebar-layout')return res.end(JSON.stringify(layout));
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
   if(path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/chat-commands')return res.end('[]');
   const match=path.match(/^\/v1\/logical-boxes\/(builder|reviewer)\/messages$/);
   if(match)return res.end(JSON.stringify(messages[match[1]]));
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const file=resolve(web,path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html','.woff2':'font/woff2'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  const first=await browser.createBrowserContext();const page=await first.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .unread')?.textContent==='2'&&document.querySelector('#chat-back-count')?.textContent==='3');
  assert.equal(await page.$$eval('#chat-entries .chat-folder .unread,#chat-entries .conversation-divider .unread,#chat-entries .conversation-group .unread',nodes=>nodes.length),0,'divider badges are absent');
  await page.$eval('[data-box-id="builder"]',row=>row.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,clientX:100,clientY:300})));
  await page.waitForSelector('#row-menu:not([hidden])');
  await page.$eval('#row-menu button',button=>{if(button.textContent!=='Mark as read')throw Error('missing row action');button.click()});
  await page.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .unread')?.hidden&&document.querySelector('#chat-back-count')?.textContent==='1');
  await page.$eval('[data-group-id="focus"]',row=>row.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,clientX:100,clientY:300})));
  await page.$$eval('#row-menu button',buttons=>{const button=buttons.find(button=>button.textContent==='Mark all as read');if(!button)throw Error('missing group action');button.click()});
  await page.waitForFunction(()=>document.querySelector('#chat-back-count')?.hidden);
  await page.waitForFunction(()=>Object.keys(window.localStorage).includes('vmboxChatSeen'));
  await new Promise(resolve=>setTimeout(resolve,250));
  assert.ok(markers['box:builder']&&markers['box:reviewer'],'both markers reached the server');
  const second=await browser.createBrowserContext();const other=await second.newPage();await other.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await other.goto(base+'/chat');
  await other.waitForFunction(()=>document.querySelector('[data-box-id="builder"] .unread')?.hidden&&document.querySelector('#chat-back-count')?.hidden);
  await other.$eval('[data-group-id="focus"] .chat-folder-toggle',button=>button.click());
  assert.equal(await other.$eval('[data-box-id="reviewer"] .unread',badge=>badge.hidden),true);
  await second.close();await first.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
