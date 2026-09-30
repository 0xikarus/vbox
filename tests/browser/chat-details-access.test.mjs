import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const box={id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'shared-worker'};
const contact={contactBoxId:'reviewer',contactName:'reviewer',contactState:'running',contactAgent:'codex',override:'allow',canMessage:true};

test('Details keeps direct contacts separate from Access actions',async()=>{
 let protectedBox=false,protectionWrites=0;
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
   if(path.endsWith('/agent-policy'))return res.end('{"capabilities":{"mcpTools":{"enabled":true,"allowedTools":[]}}}');
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
  assert.deepEqual(await page.$eval('#inspect-access',section=>[...section.querySelectorAll('.inspect-access-copy strong')].map(node=>node.textContent)),['Permissions','Labels','Protection']);
  const layout=await page.evaluate(()=>{
   const frame=document.querySelector('.inspect-screen-frame').getBoundingClientRect();
   const mascot=document.querySelector('#inspect-avatar .vbox-mascot-shape').getBoundingClientRect();
   const actions=[...document.querySelectorAll('#inspect-actions .chat-action')];
   const section=document.querySelector('.inspect-technical-inner>.inspect-block');
   return {screenScrolls:document.querySelector('#inspect-screen').parentElement.id==='inspect-body',mascotInside:mascot.bottom<=frame.bottom&&mascot.right<=frame.right,mascotShare:mascot.height/frame.height,actionWidths:actions.map(action=>action.querySelector('.chat-action-icon').getBoundingClientRect().width),nestedBorder:getComputedStyle(section).borderTopWidth};
  });
  assert.equal(layout.screenScrolls,true,'the mobile desktop hero scrolls with Details content');
  assert.equal(layout.mascotInside,true,'the mascot stays inside the desktop preview');
  assert.ok(layout.mascotShare<=.39,'the visible mascot fits the preview height');
  assert.deepEqual(layout.actionWidths,[48,48,48],'all quick actions share one icon style');
  assert.equal(layout.nestedBorder,'0px','Technical details has one card boundary');
  await page.$eval('#inspect-access',section=>section.open=true);
  await page.$eval('#inspect-edit-roles',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#role-editor-modal').hidden);
  await page.$eval('#role-editor-modal [data-close]',button=>button.click());
  await page.$eval('#inspect-toggle-protection',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-toggle-protection').getAttribute('aria-checked')==='true');
  assert.equal(protectionWrites,1);
  await page.$eval('#inspect-contacts',section=>section.open=true);
  await page.$eval('#inspect-contact-permissions',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#role-editor-modal').hidden);
  await page.close();
 }finally{await browser.close();server.close()}
});
