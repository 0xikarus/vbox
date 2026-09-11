import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/workspace.html','utf8');
const script=await readFile('internal/controller/web/workspace.js','utf8');
test('workspace desktop selection, tabs, and manual fallback',async t=>{
 let tools=['blender'],enabled=true,fail='',hold='',release,role='owner',run=null,state='running';
 let requests=[];
 const server=http.createServer(async(req,res)=>{
  const path=req.url,method=req.method;
  if(path==='/boxes/test')return res.end(html.replace(/<script[\s\S]*$/,`<script>window.attaches=0;window.terminals=0;window.openWorkspaceTerminal=()=>{window.terminals++;return()=>{}};window.openWorkspaceDesktop=(box,status,options)=>{window.attaches++;window.desktopMetrics=options.onMetrics;return()=>{}};</script><script src="/workspace.js"></script>`));
  if(path==='/workspace.js'){res.setHeader('Content-Type','text/javascript');return res.end(script)}
  if(!path.startsWith('/v1/'))return res.end();
  requests.push(method+' '+path);
  if(hold===method+' '+path)await new Promise(r=>{release=r});
  res.setHeader('Content-Type','application/json');
  if(fail===method+' '+path){res.statusCode=409;return res.end(JSON.stringify({error:'Fixture failure'}))}
  let data={};
  if(path==='/v1/whoami')data={role};
  else if(path.endsWith('/run-once'))data=run;
  else if(path==='/v1/run-once/run')data={id:'run',boxId:'test',task:{state:'running',agent:'shell',session:'task-test'}};
  else if(path.endsWith('/sessions/interactive'))data={session:'shell-test'};
  else if(path.endsWith('/desktop')&&method==='GET')data={enabled};
  else if(path==='/v1/logical-boxes/test')data={id:'test',name:'Test',state,tools};
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
  await t.test('enabled Blender opens Desktop first and TMUX attaches lazily',async()=>{
   tools=['BlEnDeR'];enabled=true;const p=await page();await p.waitForFunction(()=>window.attaches===1&&!document.querySelector('#connect').disabled);
   assert.equal(await p.evaluate(()=>window.terminals),0);
   await p.evaluate(()=>desktopMetrics({state:'connected',ping:73}));
   assert.match(await p.$eval('#connection-stats',e=>e.textContent),/Desktop ping: 73 ms/);
   assert.equal(await p.$eval('#connection-stats',e=>e.nextElementSibling.id),'workspace-tabs');

   assert.deepEqual(await selected(p,'#desktop-tab'),{selected:'true',panel:false});
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);
   await p.click('#terminal-tab');await terminalReady(p);
   assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});
   await p.click('#desktop-tab');assert.equal(await p.evaluate(()=>window.attaches),1);
   await p.click('#terminal-tab');assert.equal(await p.evaluate(()=>window.terminals),1);
   await p.reload();await p.waitForFunction(()=>window.attaches===1);assert.equal(await p.evaluate(()=>window.terminals),0);await p.close();
  });
  await t.test('enabled non-Blender desktop also opens automatically',async()=>{
   tools=['foundry'];enabled=true;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.equal(await p.evaluate(()=>window.terminals),0);assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);await p.close();
  });
  await t.test('missing Blender desktop enables before start',async()=>{
   tools=['blender'];enabled=false;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop+'/enable','POST '+desktop]);assert.equal(await p.evaluate(()=>window.terminals),0);await p.close();enabled=true;
  });
  await t.test('disabled non-Blender defaults to TMUX and manual controls work',async()=>{
   tools=['foundry'];enabled=false;const p=await page();await terminalReady(p);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop]);
   assert.deepEqual(await selected(p,'#terminal-tab'),{selected:'true',panel:false});
   await p.click('#desktop-tab');p.on('dialog',d=>d.accept());await p.click('#enable-desktop');await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('packages ready'));
   await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);assert.deepEqual(await selected(p,'#desktop-tab'),{selected:'true',panel:false});await p.close();enabled=true;
  });
  await t.test('legacy Blender worker attaches through idempotent start',async()=>{
   tools=['blender'];enabled=true;fail='GET '+desktop;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);assert.equal(await p.evaluate(()=>window.terminals),0);await p.close();fail='';
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
  await t.test('failed allocation never probes or launches desktop',async()=>{
   state='failed';const p=await page();await p.waitForFunction(()=>document.querySelector('#error').textContent.includes('Fixture stopped box'));
   assert.equal(requests.some(r=>r.includes('/desktop')),false);assert.equal(await p.evaluate(()=>window.terminals),0);await p.close();state='running';
  });
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
