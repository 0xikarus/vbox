import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const box={id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'shared-worker'};
const contact={contactBoxId:'reviewer',contactName:'reviewer',contactState:'running',contactAgent:'codex',override:'allow',canMessage:true};

test('Details keeps direct contacts separate from Access actions',async()=>{
 let protectedBox=false,protectionWrites=0,failPolicyOnce=true,policy={capabilities:{mcpTools:{enabled:true,allowedTools:[]},requestMoreTime:{maxExtensionMinutes:7,maxTotalMinutes:14}}};
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
   if(path.endsWith('/desktop'))return res.end('{"enabled":false}');
   if(path.endsWith('/messages'))return res.end('[]');
   if(path.endsWith('/contacts'))return res.end(JSON.stringify([contact]));
   if(path.endsWith('/tags'))return res.end('{"tags":["backend"]}');
   if(path.endsWith('/protection')){
    if(req.method==='PUT'){let body='';for await(const chunk of req)body+=chunk;protectedBox=!!JSON.parse(body).protected;protectionWrites++}
    return res.end(JSON.stringify({protected:protectedBox}));
   }
   if(path.endsWith('/agent-policy')){if(req.method==='PUT'){let body='';for await(const chunk of req)body+=chunk;if(failPolicyOnce){failPolicyOnce=false;res.statusCode=503;return res.end('{"error":"Try again"}')}policy=JSON.parse(body)}return res.end(JSON.stringify(policy))}
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
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect-access').hidden&&document.querySelector('#inspect-tags').textContent==='backend');
  assert.equal(await page.$eval('#inspect-contacts',section=>!!section.querySelector('#inspect-contact-list')&&!section.querySelector('.contact-policy')),true);
  assert.deepEqual(await page.$eval('#inspect-access',section=>[...section.querySelectorAll('.inspect-access-copy strong')].map(node=>node.textContent)),['Labels','Protection']);
  const captureDir='/data/workspace/captures/details-redesign';await mkdir(captureDir,{recursive:true});
  await page.$eval('[data-ip-row="access"]',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-title').textContent==='Access & permissions'&&!document.querySelector('[data-ip-page="access"]').hidden);
  assert.equal(await page.$eval('#inspect-access',node=>getComputedStyle(node).display),'block');
  assert.deepEqual(await page.evaluate(()=>['#inspect-toggle-protection','#inspect-create-limit .mail-switch','#inspect-create-limit input[type=number]','#inspect-create-limit .idle-policy-controls button'].map(selector=>{const rect=document.querySelector(selector).getBoundingClientRect();return [selector,rect.width>=40&&rect.height>=40]})),[['#inspect-toggle-protection',true],['#inspect-create-limit .mail-switch',true],['#inspect-create-limit input[type=number]',true],['#inspect-create-limit .idle-policy-controls button',true]],'Access controls have at least 40px tap targets');
  await page.screenshot({path:captureDir+'/access-390.png'});
  await page.setViewport({width:1440,height:900,isMobile:true,hasTouch:true});await page.screenshot({path:captureDir+'/access-1440.png'});
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden);
  assert.equal(await page.$eval('#role-editor-form',form=>form.querySelectorAll('.inline-permission-card').length),5);
  await page.$eval('#role-editor-form [name=allContactsEnabled]',input=>input.click());
  await page.waitForFunction(()=>document.querySelector('.inline-permission-retry').hidden===false);
  assert.match(await page.$eval('#role-editor-status',node=>node.textContent),/Try again/);
  await page.click('.inline-permission-retry');
  await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
  assert.equal(policy.capabilities.allContacts.enabled,true);
  assert.deepEqual(policy.capabilities.requestMoreTime,{maxExtensionMinutes:7,maxTotalMinutes:14});
  await page.$eval('#inspect-toggle-protection',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-toggle-protection').getAttribute('aria-checked')==='true');
  assert.equal(protectionWrites,1);
  await page.$eval('#inspect-prototype-back',button=>button.click());
  await page.$eval('[data-ip-row="contacts"]',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-title').textContent==='Contacts'&&!document.querySelector('[data-ip-page="contacts"]').hidden);
  assert.ok(await page.evaluate(()=>['#inspect-add-contact','#inspect-contact-list .contact-access','#inspect-contact-permissions'].every(selector=>{const rect=document.querySelector(selector).getBoundingClientRect();return rect.width>=40&&rect.height>=40})),'Contact controls have at least 40px tap targets');
  await page.screenshot({path:captureDir+'/contacts-390.png'});
  await page.setViewport({width:1440,height:900,isMobile:true,hasTouch:true});await page.screenshot({path:captureDir+'/contacts-1440.png'});
  await page.$eval('#inspect-contact-permissions',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden);
  await page.close();
 }finally{await browser.close();server.close()}
});
