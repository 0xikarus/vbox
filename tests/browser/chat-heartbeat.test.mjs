import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';
const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
const readBody=async req=>{let body='';for await(const part of req)body+=part;return JSON.parse(body||'{}')};
test('heartbeat slash command has inline required arguments and can be stopped in Active Tools',async()=>{
 let timers=[],starts=[],stops=[],ordinaryMessages=0;
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path.endsWith('/heartbeats')&&req.method==='POST'){
    const body=await readBody(req);starts.push({body,key:req.headers['idempotency-key']});
    timers=[{id:'hb-'+starts.length,...body,remaining:body.count,nextAt:new Date(Date.now()+body.intervalMinutes*60000).toISOString(),active:true,paused:false}];res.statusCode=201;return res.end(JSON.stringify(timers[0]));
   }
   if(path.includes('/heartbeats/')&&req.method==='DELETE'){stops.push(path.split('/').at(-1));timers=[];res.statusCode=204;return res.end()}
   if(path.endsWith('/active-tools'))return res.end(JSON.stringify({heartbeats:timers}));
   if(path.endsWith('/messages')&&req.method==='POST'){ordinaryMessages++;return res.end('{}')}
   const data={
    '/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],
    '/v1/box-activity':[], '/v1/box-conversations':[], '/v1/tool-presets':[], '/v1/chat-commands':[],
    '/v1/logical-boxes/builder/messages':[], '/v1/logical-boxes/builder/imported-credentials':{profiles:[]},
    '/v1/logical-boxes/builder/contacts':[], '/v1/login-profiles':[],
    '/v1/logical-boxes/builder/idle-policy':{seconds:0}, '/v1/logical-boxes/builder/run-budget-policy':{seconds:0},
    '/v1/logical-boxes/builder/attachment-storage':{boxBytes:0,boxCount:0,clearableCount:0},
    '/v1/logical-boxes/builder/resources':{resources:{memoryMiB:2048,diskGiB:10}},
    '/v1/fleet/host-resources':{}, '/v1/fleet/status':{slots:[]},
   };
   if(path==='/v1/push/vapid-key')res.statusCode=404;
   return res.end(JSON.stringify(data[path]??{}));
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');res.end(await readFile(resolve(web,file)))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [390,1440]){
   const page=await browser.newPage();await page.setViewport({width,height:900});const errors=[];page.on('pageerror',e=>errors.push(e.message));
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
   await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
   await page.type('#chat-input','/hea');
   await page.waitForSelector('#composer-picker:not([hidden]) button');
   assert.match(await page.$eval('#composer-picker',n=>n.textContent),/count · interval \(minutes\) · message/);
   await page.keyboard.press('Tab');
   await page.waitForSelector('#heartbeat-command:not([hidden])');
   assert.equal(await page.evaluate(()=>document.activeElement.id),'heartbeat-count');
   assert.equal(await page.$eval('#chat-input',n=>n.hidden),true);
   await page.type('#heartbeat-count','3');await page.keyboard.press('Tab');
   assert.equal(await page.evaluate(()=>document.activeElement.id),'heartbeat-interval');
   assert.match(await page.$eval('#heartbeat-parameter-help',n=>n.textContent),/Minutes between/);
   await page.type('#heartbeat-interval','2');await page.keyboard.press('Tab');
   const before=starts.length;await page.click('#send');assert.equal(starts.length,before,'message is required');
   await page.type('#heartbeat-message','Check progress and report back.');
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'composer fits mobile and desktop');
   assert.equal(await page.$eval('#chat-input',n=>n.getBoundingClientRect().width),0,'normal composer and its AI wrapper take no space in command mode');
   assert.ok(await page.$eval('#heartbeat-message',n=>n.getBoundingClientRect().width>=160),'message field remains usable on mobile');
   assert.ok(await page.$eval('#heartbeat-command',n=>n.getBoundingClientRect().width>=Math.min(260,innerWidth-100)),'command uses available composer width');
   if(process.env.VMBOX_SCREENSHOT_DIR)await page.screenshot({path:resolve(process.env.VMBOX_SCREENSHOT_DIR,'heartbeat-composer-'+width+'.png')});
   await page.click('#send');await page.waitForFunction(()=>document.querySelector('#heartbeat-command').hidden);
   assert.equal(starts.length,before+1);assert.deepEqual(starts.at(-1).body,{count:3,intervalMinutes:2,message:'Check progress and report back.'});assert.ok(starts.at(-1).key);
   assert.equal(ordinaryMessages,0,'command itself is not sent to the agent');
   await page.$eval('#chat-info',n=>n.click());await page.waitForSelector('[data-ip-row="activeTools"]');await page.$eval('[data-ip-row="activeTools"]',n=>n.click());
   await page.waitForSelector('.heartbeat-tool');
   assert.equal(await page.$eval('#inspect-title',n=>n.textContent),'Active Tools');
   assert.match(await page.$eval('.heartbeat-tool',n=>n.textContent),/3 of 3 remaining · every 2 min/);
   assert.equal(await page.$eval('.heartbeat-tool-message',n=>n.textContent),'Check progress and report back.');
   await page.$eval('.heartbeat-tool button',n=>n.click());await page.waitForFunction(()=>document.querySelector('[data-ip-page="activeTools"]').textContent.includes('No active chat heartbeats'));
   assert.equal(stops.at(-1),'hb-'+starts.length);
   await page.$eval('#inspect-close',n=>n.click());
   // Escape returns to ordinary chat, without leaving required hidden inputs.
   await page.type('#chat-input','/heartbeat');await page.keyboard.press('Enter');await page.waitForSelector('#heartbeat-command:not([hidden])');await page.keyboard.press('Escape');
   assert.equal(await page.$eval('#heartbeat-command',n=>n.hidden),true);
   assert.equal(await page.$$eval('#heartbeat-command input,#heartbeat-command textarea',nodes=>nodes.every(n=>n.disabled)),true);
   assert.deepEqual(errors,[]);await page.close();
  }
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
