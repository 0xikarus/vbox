import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');

// The chat Details drawer edits the same controller-side contact graph as the
// single-box workspace page (whose non-owner gating is covered in
// workspace-desktop.test.mjs). This test drives the real page against fixture
// APIs and writes the screenshot the PR references.
test('chat details drawer edits the per-box contact graph',async()=>{
 let boxRole='worker',protectedBox=false,requests=[],fullDesktopShots=0;
 let contacts=[{contactName:'reviewer',contactRole:'worker',contactState:'running',canMessage:true,canReceive:true}];
 const boxes=[{id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'railway',volumeId:'v1',volumeName:'v1'},{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex',provider:'railway',volumeId:'v2',volumeName:'v2'}];
 const thumbnail=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0],method=req.method;requests.push(method+' '+path);
  if(path==='/chat')return res.end(html);
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(!path.startsWith('/v1/'))return res.end();
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes.map(b=>({...b,role:b.id==='builder'?boxRole:'worker'}))));
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path.endsWith('/desktop/screenshot')){
   if(req.url.includes('thumbnail=true')){res.statusCode=409;return res.end(JSON.stringify({error:'thumbnail offline'}))}
   fullDesktopShots++;res.setHeader('Content-Type','image/png');return res.end(thumbnail);
  }
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify([{id:'m1',direction:'agent',state:'delivered',text:'Ready.',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()}]));
  if(path==='/v1/logical-boxes/reviewer/sessions/interactive'&&method==='POST')return res.end(JSON.stringify({session:'codex-reviewer'}));
  if(path==='/v1/logical-boxes/reviewer/desktop'&&method==='POST')return res.end(JSON.stringify({state:'running'}));
  if(path.endsWith('/messages'))return res.end(JSON.stringify([]));
  if(path==='/v1/logical-boxes/builder/contacts'){
   if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;const parsed=JSON.parse(body);contacts=contacts.filter(c=>c.contactName!==parsed.contact);const created={contactName:parsed.contact,contactRole:'worker',contactState:'running',canMessage:true,canReceive:true};contacts.push(created);return res.end(JSON.stringify(created))}
   return res.end(JSON.stringify(contacts));
  }
  if(path.startsWith('/v1/logical-boxes/builder/contacts/')&&method==='DELETE'){contacts=contacts.filter(c=>c.contactName!==decodeURIComponent(path.split('/').pop()));res.statusCode=204;return res.end()}
  if(path==='/v1/logical-boxes/builder/protection'){
   if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;protectedBox=!!JSON.parse(body).protected}
   return res.end(JSON.stringify({protected:protectedBox}));
  }
  if(path==='/v1/logical-boxes/builder'){
   if(method==='PATCH'){let body='';for await(const chunk of req)body+=chunk;const parsed=JSON.parse(body);if(parsed.role)boxRole=parsed.role}
   return res.end(JSON.stringify({...boxes[0],role:boxRole}));
  }
  if(path.startsWith('/v1/logical-boxes/builder'))return res.end(JSON.stringify({...boxes[0],role:boxRole}));
  res.statusCode=404;return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.evaluateOnNewDocument(()=>{
   window.openWorkspaceDesktop=(id,_status,options)=>{window.viewerDesktop={id,root:options.root.id};return()=>{}};
   window.openWorkspaceTerminal=(id,session,_status,options)=>{window.viewerTerminal={id,session,root:options.root.id};return()=>{}};
  });
  await p.setViewport({width:420,height:820,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
  await p.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  for(const selector of ['#chat-entries [data-avatar="builder"]','#chat-header-avatar [data-avatar="builder"]']){
   await p.$eval(selector,e=>e.dispatchEvent(new MouseEvent('mouseenter')));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),false,selector+' did not open the desktop preview');
   await p.waitForFunction(()=>document.querySelector('.tv-preview img')?.naturalWidth===1);
   await p.$eval(selector,e=>e.dispatchEvent(new MouseEvent('mouseleave')));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),true,selector+' did not close the desktop preview');
  }
  assert.equal(fullDesktopShots>0,true);
  await p.click('#chat-entries [data-avatar="reviewer"]');
  await p.waitForFunction(()=>!document.querySelector('#takeover').hidden&&document.querySelector('[data-box-id="reviewer"]').classList.contains('active'),{timeout:5000});
  await p.waitForFunction(()=>window.viewerDesktop?.id==='reviewer',{timeout:5000});
  assert.equal(new URL(p.url()).hash,'#box=reviewer');
  assert.deepEqual(await p.evaluate(()=>window.viewerDesktop),{id:'reviewer',root:'takeover-screen'});
  await p.click('#takeover-tabs [data-kind="terminal"]');
  await p.waitForFunction(()=>window.viewerTerminal?.id==='reviewer',{timeout:5000});
  assert.deepEqual(await p.evaluate(()=>window.viewerTerminal),{id:'reviewer',session:'codex-reviewer',root:'takeover-screen'});
  await p.click('#takeover-close');await p.waitForFunction(()=>document.querySelector('#takeover').hidden);await p.$eval('[data-box-id="builder"]',element=>element.click());
  await p.waitForFunction(()=>document.querySelector('[data-box-id="builder"]').classList.contains('active'),{timeout:5000});
  await p.$eval('#chat-info',e=>e.click());
  await p.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await p.waitForFunction(()=>document.querySelector('#inspect-contact-role').textContent==='worker');
  assert.match(await p.$eval('#inspect-contact-status',e=>e.textContent),/worker may message only/);
  await p.type('#inspect-contact-form input[name=contact]','planner');
  await p.$eval('#inspect-contact-form button',e=>e.click());
  await p.waitForFunction(()=>document.querySelectorAll('#inspect-contact-list li').length===2);
  assert.equal(requests.includes('PUT /v1/logical-boxes/builder/contacts'),true);
  await p.$eval('#inspect-toggle-role',e=>e.click());
  await p.waitForFunction(()=>document.querySelector('#inspect-contact-role').textContent==='manager');
  assert.equal(requests.includes('PATCH /v1/logical-boxes/builder'),true);
  await p.$eval('#inspect-toggle-protection',e=>e.click());
  await p.waitForFunction(()=>document.querySelector('#inspect-protection-label').textContent.startsWith('Protected'));
  assert.equal(requests.includes('PUT /v1/logical-boxes/builder/protection'),true);
  await p.$eval('#inspect-contact-list li button',e=>e.click());
  await p.waitForFunction(()=>document.querySelectorAll('#inspect-contact-list li').length===1);
  assert.equal(requests.some(r=>r.startsWith('DELETE /v1/logical-boxes/builder/contacts/')),true);
  await (await p.$('#inspect')).screenshot({path:'docs/chat-ui/screenshots/desktop-chat-contacts.png'});
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
