import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/workspace.html','utf8');
const script=await readFile('internal/controller/web/workspace.js','utf8');
test('workspace desktop selection, tabs, and manual fallback',async t=>{
 let tools=['blender'],enabled=true,fail='',hold='',release,role='owner',run=null,state='running',connectionTransport='openssh',thumbnailAvailable=false,thumbnailRequests=0,holdThumbnail=false,releaseThumbnail;
 let requests=[];
 const thumbnail=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const server=http.createServer(async(req,res)=>{
  const path=req.url,method=req.method;
  if(path==='/boxes/test')return res.end(html.replace(/<script[\s\S]*$/,`<script>window.attaches=0;window.terminals=0;window.openWorkspaceTerminal=()=>{window.terminals++;return()=>{}};window.openWorkspaceDesktop=(box,status,options)=>{window.attaches++;window.desktopMetrics=options.onMetrics;return()=>{}};</script><script src="/workspace.js"></script>`));
  if(path==='/workspace.js'){res.setHeader('Content-Type','text/javascript');return res.end(script)}
  if(!path.startsWith('/v1/'))return res.end();
  requests.push(method+' '+path);
  if(path==='/v1/logical-boxes/test/desktop/screenshot?thumbnail=true'){
   thumbnailRequests++;
   const available=thumbnailAvailable;
   if(holdThumbnail){await new Promise(r=>{releaseThumbnail=r});holdThumbnail=false}
   if(!available){res.statusCode=409;return res.end(JSON.stringify({error:'desktop offline'}))}
   res.setHeader('Content-Type','image/png');res.setHeader('X-Captured-At',new Date().toISOString());return res.end(thumbnail);
  }
  if(hold===method+' '+path)await new Promise(r=>{release=r});
  res.setHeader('Content-Type','application/json');
  if(fail===method+' '+path){res.statusCode=409;return res.end(JSON.stringify({error:'Fixture failure'}))}
  let data={};
  if(path==='/v1/whoami')data={role};
  else if(path.endsWith('/run-once'))data=run;
  else if(path==='/v1/run-once/run')data={id:'run',boxId:'test',task:{state:'running',agent:'shell',session:'task-test'}};
  else if(path.endsWith('/messages')||path.endsWith('/secret-requests'))data=[];
  else if(path.endsWith('/sessions/interactive'))data={session:'shell-test'};
  else if(path.endsWith('/desktop')&&method==='GET')data={enabled};
  else if(path==='/v1/logical-boxes/test')data={id:'test',name:'Test',state,tools};
  else if(path.endsWith('/resources'))data={slotId:'slot-test',assignmentGeneration:7,resources:{cpu:2,memoryMiB:12288},message:'Limits submitted. No restart requested.'};
  else if(path.endsWith('/connection'))data={connection:{transport:connectionTransport,endpoint:connectionTransport==='openssh'?'instance@ssh.railway.com':'service-id',metadata:{vmboxRegion:'europe-west4'}}};
  else if(path.endsWith('/allocate'))data={state:'failed',failureReason:'Fixture stopped box'};
  res.end(JSON.stringify(data));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 const desktop='/v1/logical-boxes/test/desktop';
 async function page(){requests=[];release=undefined;const p=await browser.newPage();await p.goto('http://127.0.0.1:'+server.address().port+'/boxes/test');return p}
 async function terminalReady(p){await p.waitForFunction(()=>window.terminals===1&&!document.querySelector('#connect').disabled)}
 async function selected(p,id){return p.$eval(id,e=>({selected:e.getAttribute('aria-selected'),panel:document.getElementById(e.getAttribute('aria-controls')).hidden}))}
 try{
  await t.test('resource and connection controls load on demand without restarting viewers',async()=>{
   const p=await page();await p.waitForFunction(()=>window.attaches===1&&!document.querySelector('#connect').disabled);
   assert.equal(requests.some(r=>/resources|connection$/.test(r)),false);
   await p.click('#box-settings summary');await p.click('#load-resources');await p.waitForFunction(()=>!document.querySelector('#resource-form').hidden);
   assert.equal(await p.$eval('#resource-form input[name=cpu]',e=>e.value),'2');
   p.on('dialog',d=>d.accept());await p.click('#resource-form button');await p.waitForFunction(()=>document.querySelector('#resource-status').textContent.includes('Limits submitted'));
   assert.equal(requests.filter(r=>r==='PUT /v1/logical-boxes/test/resources').length,1);
   await p.click('#load-connection');await p.waitForFunction(()=>!document.querySelector('#connection-details').hidden);
   assert.match(await p.$eval('#forward-command',e=>e.textContent),/-L 127.0.0.1:3000:127.0.0.1:3000 instance@ssh.railway.com/);
   await p.$eval('#forward-port',e=>{e.value='65536';e.dispatchEvent(new Event('input'))});assert.equal(await p.$eval('#forward-command',e=>e.textContent),'');
   assert.equal(await p.evaluate(()=>window.attaches),1);assert.equal(await p.evaluate(()=>window.terminals),1);await p.close();
  });
  await t.test('direct worker connection does not show an unusable SSH command',async()=>{
   connectionTransport='controller-worker';
   const p=await page();await p.waitForFunction(()=>!document.querySelector('#connect').disabled);
   await p.click('#box-settings summary');await p.click('#load-connection');await p.waitForFunction(()=>!document.querySelector('#connection-details').hidden);
   assert.equal(await p.$eval('#ssh-forwarding',e=>e.hidden),true);
   assert.match(await p.$eval('#connection-address',e=>e.textContent),/controller-worker through the controller/);
   assert.match(await p.$eval('#connection-status',e=>e.textContent),/does not provide SSH port forwarding/);
   await p.close();connectionTransport='openssh';
  });
  await t.test('enabled Blender opens Desktop with its managed terminal already attached',async()=>{
   tools=['BlEnDeR'];enabled=true;const p=await page();await p.waitForFunction(()=>window.attaches===1&&!document.querySelector('#connect').disabled);
   assert.equal(await p.evaluate(()=>window.terminals),1);
   await p.evaluate(()=>desktopMetrics({state:'connected',ping:73}));
   assert.match(await p.$eval('#connection-stats',e=>e.textContent),/Desktop ping: 73 ms/);
   assert.equal(await p.$eval('#connection-stats',e=>e.nextElementSibling.id),'workspace-tabs');

   assert.deepEqual(await selected(p,'#desktop-tab'),{selected:'true',panel:false});
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);
   await p.click('#terminal-tab');await terminalReady(p);
   assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});
   await p.click('#desktop-tab');assert.equal(await p.evaluate(()=>window.attaches),1);
   await p.click('#terminal-tab');assert.equal(await p.evaluate(()=>window.terminals),1);
   await p.reload();await p.waitForFunction(()=>window.attaches===1);assert.equal(await p.evaluate(()=>window.terminals),1);await p.close();
  });
  await t.test('enabled non-Blender desktop also opens automatically',async()=>{
   tools=['foundry'];enabled=true;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.equal(await p.evaluate(()=>window.terminals),1);assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);await p.close();
  });
  await t.test('missing Blender desktop enables before start',async()=>{
   tools=['blender'];enabled=false;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop+'/enable','POST '+desktop]);assert.equal(await p.evaluate(()=>window.terminals),1);await p.close();enabled=true;
  });
  await t.test('disabled non-Blender defaults to TMUX and manual controls work',async()=>{
   tools=['foundry'];enabled=false;const p=await page();await terminalReady(p);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop]);
   assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});
   await p.click('#desktop-tab');p.on('dialog',d=>d.accept());await p.click('#enable-desktop');await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('packages ready'));
   await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);assert.deepEqual(await selected(p,'#desktop-tab'),{selected:'true',panel:false});await p.close();enabled=true;
  });
  await t.test('open preview refreshes immediately after starting desktop',async()=>{
   tools=['foundry'];enabled=false;thumbnailAvailable=false;thumbnailRequests=0;
   const p=await page();await terminalReady(p);await p.click('#desktop-preview summary');
   await p.waitForFunction(()=>document.querySelector('#thumbnail-status').textContent.includes('Preview unavailable'));
   assert.equal(thumbnailRequests,1);
   thumbnailAvailable=true;
   await p.click('#desktop-tab');p.on('dialog',d=>d.accept());await p.click('#enable-desktop');
   await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('packages ready'));
   await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);
   await p.waitForFunction(()=>!document.querySelector('#desktop-thumbnail').hidden&&document.querySelector('#desktop-thumbnail').naturalWidth===1,{timeout:3000});
   assert.ok(thumbnailRequests>=2);
   assert.match(await p.$eval('#thumbnail-status',e=>e.textContent),/^Captured /);
   await p.close();enabled=true;
  });
  await t.test('desktop startup queues a fresh preview behind an in-flight capture',async()=>{
   tools=['foundry'];enabled=false;thumbnailAvailable=false;thumbnailRequests=0;holdThumbnail=true;releaseThumbnail=undefined;
   const p=await page();await terminalReady(p);await p.click('#desktop-preview summary');
   while(!releaseThumbnail)await new Promise(r=>setTimeout(r,10));
   thumbnailAvailable=true;
   await p.click('#desktop-tab');p.on('dialog',d=>d.accept());await p.click('#enable-desktop');
   await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('packages ready'));
   await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);
   releaseThumbnail();
   await p.waitForFunction(()=>!document.querySelector('#desktop-thumbnail').hidden&&document.querySelector('#desktop-thumbnail').naturalWidth===1,{timeout:3000});
   assert.equal(thumbnailRequests,2);
   await p.close();enabled=true;
  });
  await t.test('legacy Blender worker attaches through idempotent start',async()=>{
   tools=['blender'];enabled=true;fail='GET '+desktop;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);assert.equal(await p.evaluate(()=>window.terminals),1);await p.close();fail='';
  });
  for(const failure of ['POST '+desktop+'/enable','POST '+desktop])await t.test('failure falls back to TMUX without loops: '+failure,async()=>{
   tools=['blender'];enabled=false;fail=failure;const p=await page();await terminalReady(p);
   assert.match(await p.$eval('#desktop-status',e=>e.textContent),/Automatic desktop launch failed/);
   const count=requests.filter(r=>r.includes('/desktop')).length;await p.click('#connect');await p.waitForFunction(()=>!document.querySelector('#connect').disabled);
   assert.equal(requests.filter(r=>r.includes('/desktop')).length,count);assert.equal(await p.evaluate(()=>window.terminals),1);
   fail='';await p.click('#desktop-tab');await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);await p.close();enabled=true;
  });
  await t.test('a tab choice made during probing is not overwritten',async()=>{
   tools=['foundry'];enabled=true;hold='GET '+desktop;const p=await page();while(!release)await new Promise(r=>setTimeout(r,10));
   await p.click('#terminal-tab');await p.waitForFunction(()=>window.terminals===1);release();await p.waitForFunction(()=>window.attaches===1&&!document.querySelector('#connect').disabled);
   assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});assert.equal(await p.evaluate(()=>window.terminals),1);await p.close();hold='';
  });
  await t.test('stale enable cannot start or attach after logout',async()=>{
   tools=['blender'];enabled=false;hold='POST '+desktop+'/enable';const p=await page();await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('Installing'));
   while(!release)await new Promise(r=>setTimeout(r,10));await p.click('#logout');await p.waitForFunction(()=>document.querySelector('#workspace').hidden);
   release();await p.waitForFunction(()=>!document.querySelector('#start-desktop').disabled);
   assert.equal(requests.includes('POST '+desktop),false);assert.equal(await p.evaluate(()=>window.attaches),0);await p.close();hold='';enabled=true;
  });
  await t.test('run once and restricted roles remain on TMUX',async()=>{
   run={id:'run'};let p=await page();await terminalReady(p);assert.equal(requests.some(r=>r.includes('/desktop')),false);assert.equal(await p.$eval('#workspace-tabs',e=>e.hidden),true);await p.close();run=null;
   role='member';p=await page();await terminalReady(p);assert.equal(requests.some(r=>r.includes('/desktop')),false);assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});await p.close();role='owner';
  });
  await t.test('opening a sleeping box stays read-only until explicit resume',async()=>{
   state='hibernated';const p=await page();await p.waitForFunction(()=>document.querySelector('#connect').textContent==='Resume box'&&!document.querySelector('#connect').disabled);
   assert.equal(requests.some(r=>r.startsWith('POST ')||r.includes('/desktop')),false);assert.equal(await p.evaluate(()=>window.terminals),0);
   await p.click('#connect');await p.waitForFunction(()=>document.querySelector('#error').textContent.includes('Fixture stopped box'));
   assert.equal(requests.filter(r=>r.endsWith('/allocate')).length,1);assert.equal(requests.some(r=>r.includes('/desktop')),false);assert.equal(await p.evaluate(()=>window.terminals),0);await p.close();state='running';
  });
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
