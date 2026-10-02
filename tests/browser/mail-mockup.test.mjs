import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'BossDev',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
const gib=1024**3;

async function serve(mailCalls){
 const server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   if(/(?:inbox|outbox|inbound-mail)/.test(path))mailCalls.push(path);
   response.setHeader('Content-Type','application/json');
   const data={
    '/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],
    '/v1/box-activity':[],'/v1/box-conversations':[],'/v1/tool-presets':[],
    '/v1/logical-boxes/builder/messages':[],
    '/v1/logical-boxes/builder/imported-credentials':{profiles:[]},
    '/v1/logical-boxes/builder/contacts':[],
    '/v1/login-profiles':[],
    '/v1/logical-boxes/builder/idle-policy':{seconds:10800},
    '/v1/logical-boxes/builder/run-budget-policy':{seconds:0,state:'running'},
    '/v1/logical-boxes/builder/attachment-storage':{boxBytes:0,boxCount:0,clearableCount:0,accountBytes:0,limitBytes:gib},
    '/v1/logical-boxes/builder/resources':{slotId:'slot-1',assignmentGeneration:3,resources:{cpu:2,memoryMiB:4096,swapMiB:1024,diskGiB:20}},
    '/v1/fleet/host-resources':{},'/v1/fleet/status':{slots:[{id:'slot-1',serviceName:'Worker 1',serviceId:'worker-1'}]},
   };
   if(path==='/v1/push/vapid-key')response.statusCode=404;
   response.end(JSON.stringify(data[path]??{}));return;
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;response.end();return}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 return server;
}

async function open(page,base,state=''){
 await page.goto(base+'/chat?mailMockup=1'+(state?'&mailMockupState='+state:'')+'#box=builder');
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
 await page.click('#chat-info');
 await page.waitForSelector('[data-ip-row="mail"]');
}

test('fixture-only mail mockup covers inbox, detail, approval, and quiet states',async()=>{
 const mailCalls=[];
 const server=await serve(mailCalls);
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  const capture=process.env.MAIL_CAPTURE_DIR;
  if(capture)await mkdir(capture,{recursive:true});
  for(const width of [390,1440])for(const theme of ['light','dark']){
   const page=await browser.newPage();
   await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   const errors=[];page.on('pageerror',error=>errors.push(error.message));
   await open(page,base);
   const save=async name=>{if(capture)await page.screenshot({path:resolve(capture,`${name}-${width}-${theme}.png`),fullPage:true})};
   assert.equal(await page.$eval('[data-ip-row="mail"] .ip-row-value',node=>node.textContent.includes('@example.test')),true);
   assert.equal(await page.$eval('#mail-mockup-approval',node=>node.hidden),false);
   await save('details');
   await page.click('[data-ip-row="mail"]');
   assert.equal(await page.$eval('#inspect-title',node=>node.textContent),'Inbox');
   assert.equal(await page.$eval('#mail-enabled',node=>node.checked),true);
   assert.equal(await page.$eval('#mail-subscribed',node=>node.checked),true);
   assert.equal(await page.$$eval('[data-mail-id]',nodes=>nodes.length),4);
   await save('inbox');
   if(width===390)assert.deepEqual(await page.evaluate(()=>[...document.querySelectorAll('[data-ip-page="mail"] button,[data-ip-page="mail"] input')].filter(node=>node.getClientRects().length&&!node.disabled).flatMap(node=>{const r=node.getBoundingClientRect();return r.width>=40&&r.height>=40?[]:[`${node.id||node.textContent.trim()}: ${r.width}×${r.height}`]})),[],'mail targets are at least 40px');
   await page.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);
   await save('inbox-list');
   await page.click('[data-mail-filter="quarantine"]');
   await save('quarantine');
   await page.click('[data-mail-filter="all"]');
   await page.click('[data-mail-id="mail-102"]');
   assert.equal(await page.$eval('.mail-untrusted strong',node=>node.textContent),'Untrusted external content');
   assert.equal(await page.$eval('.mail-body',node=>node.textContent.includes('final screenshots')),true);
   await save('mail-detail');
   await page.click('#inspect-prototype-back');
   assert.equal(await page.$eval('#inspect-title',node=>node.textContent),'Inbox');
   await page.click('[data-mail-action="outbox"]');
   assert.equal(await page.$eval('#inspect-title',node=>node.textContent),'Outbox');
   assert.equal(await page.$$eval('[data-outbox-id]',nodes=>nodes.length),2);
   await save('outbox');
   await page.click('[data-outbox-tab="sent"]');await save('outbox-sent');
   await page.click('[data-outbox-tab="rejected"]');await save('outbox-rejected');
   await page.click('[data-outbox-tab="pending_approval"]');
   await page.click('[data-outbox-id="out-201"]');
   assert.equal(await page.$eval('#mail-mockup-review',node=>node.open),true);
   assert.equal(await page.$eval('#mail-mockup-review [name="to"]',node=>node.value),'mara@client.example');
   await save('review');
   await page.click('#mail-mockup-review [data-review="reject"]');
   await page.type('#mail-mockup-review [name="reason"]','Needs a factual check.');
   await page.click('#mail-mockup-review [data-review="reject"]');
   assert.equal(await page.$eval('#mail-mockup-review',node=>node.open),false);
   assert.equal(await page.$eval('#mail-mockup-approval b',node=>node.textContent),'1');
   await page.click('#inspect-close');
   assert.equal(await page.$eval('#mail-mockup-chat-preview',node=>node.textContent.includes('3 new mails')),true);
   await save('agent-chat');
   await page.click('.mail-push-close');
   await page.$eval('#chat-messages',node=>node.scrollTop=node.scrollHeight);
   await save('agent-chat-status');
   assert.deepEqual(errors,[]);
   if(width===390)assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'no horizontal overflow');
   await page.close();
  }
  for(const variant of ['disabled','empty','error'])for(const width of [390,1440])for(const theme of ['light','dark']){
    const page=await browser.newPage();await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});
    await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    await open(page,base,variant);await page.click('[data-ip-row="mail"]');
    const expected={disabled:'Inbox is off',empty:'No mail yet',error:'Mail could not be loaded'}[variant];
    assert.equal(await page.$eval('[data-ip-page="mail"]',(node,text)=>node.textContent.includes(text),expected),true);
    await page.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);
    if(capture)await page.screenshot({path:resolve(capture,`${variant}-${width}-${theme}.png`),fullPage:true});
    await page.close();
   }
  const page=await browser.newPage();await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.click('#chat-info');
  assert.equal(await page.$('[data-ip-row="mail"]'),null,'mockup absent without the query flag');
  await page.close();
  const approval=await browser.newPage();await approval.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await open(approval,base);await approval.click('[data-ip-row="mailOutbox"]');
  await approval.click('[data-outbox-id="out-202"]');
  await approval.$eval('#mail-mockup-review [name="subject"]',input=>input.value='Edited subject');
  await approval.click('#mail-mockup-review [data-review="approve"]');
  assert.equal(await approval.$eval('[data-ip-page="mailOutbox"]',node=>node.textContent.includes('Edited subject')),true);
  assert.equal(await approval.$eval('#mail-mockup-approval b',node=>node.textContent),'1');
  await approval.close();
  assert.deepEqual(mailCalls,[],'mail mockup never calls a mail backend');
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
