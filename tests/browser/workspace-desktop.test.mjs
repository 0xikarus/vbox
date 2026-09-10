import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/workspace.html','utf8');
const script=await readFile('internal/controller/web/workspace.js','utf8');
test('workspace desktop automation and manual fallback',async t=>{
 let tools=['blender'],enabled=true,fail='',hold='',release,role='owner',run=null,state='running';
 let requests=[];
 const server=http.createServer(async(req,res)=>{
  const path=req.url,method=req.method;
  if(path==='/boxes/test')return res.end(html.replace(/<script[\s\S]*$/,`<script>window.attaches=0;window.terminals=0;window.openWorkspaceTerminal=()=>{window.terminals++;return()=>{}};window.openWorkspaceDesktop=()=>{window.attaches++;return()=>{}};</script><script src="/workspace.js"></script>`));
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
 async function page(){requests=[];const p=await browser.newPage();await p.goto('http://127.0.0.1:'+server.address().port+'/boxes/test');return p}
 async function settled(p){await p.waitForFunction(()=>window.terminals===1&&!document.querySelector('#connect').disabled)}
 try{
  await t.test('Blender opens once, reconnect preserves terminal, reload attaches again',async()=>{
   tools=['BlEnDeR'];const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop]);
   await p.click('#connect');await p.waitForFunction(()=>window.terminals===2);assert.equal(await p.evaluate(()=>window.attaches),1);
   await p.reload();await p.waitForFunction(()=>window.attaches===1);await p.close();
  });
  await t.test('missing packages enable before start',async()=>{
   enabled=false;const p=await page();await p.waitForFunction(()=>window.attaches===1);
   assert.deepEqual(requests.filter(r=>r.includes('/desktop')),['GET '+desktop,'POST '+desktop+'/enable','POST '+desktop]);await p.close();enabled=true;
  });
  await t.test('non-Blender has no automation; both manual controls work',async()=>{
   tools=['foundry'];const p=await page();await settled(p);assert.equal(requests.some(r=>r.includes('/desktop')),false);
   p.on('dialog',d=>d.accept());await p.click('#enable-desktop');await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('packages ready'));
   await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);await p.close();
  });
  for(const failure of ['GET '+desktop,'POST '+desktop+'/enable','POST '+desktop])await t.test('failure falls back without loops: '+failure,async()=>{
   tools=['blender'];enabled=false;fail=failure;const p=await page();await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('Automatic desktop launch failed'));
   const count=requests.filter(r=>r.includes('/desktop')).length;await p.click('#connect');await p.waitForFunction(()=>window.terminals===2);
   assert.equal(requests.filter(r=>r.includes('/desktop')).length,count);assert.equal(await p.evaluate(()=>window.attaches),0);
   fail='';await p.click('#start-desktop');await p.waitForFunction(()=>window.attaches===1);await p.close();enabled=true;
  });
  await t.test('stale enable cannot start or attach after logout',async()=>{
   enabled=false;hold='POST '+desktop+'/enable';const p=await page();await p.waitForFunction(()=>document.querySelector('#desktop-status').textContent.includes('Installing'));
   while(!release)await new Promise(r=>setTimeout(r,10));await p.click('#logout');await p.waitForFunction(()=>document.querySelector('#workspace').hidden);
   release();await p.waitForFunction(()=>!document.querySelector('#start-desktop').disabled);
   assert.equal(requests.includes('POST '+desktop),false);assert.equal(await p.evaluate(()=>window.attaches),0);await p.close();hold='';enabled=true;
  });
  await t.test('Run once and restricted roles do not auto-open',async()=>{
   run={id:'run'};let p=await page();await settled(p);assert.equal(requests.some(r=>r.includes('/desktop')),false);await p.close();run=null;
   role='member';p=await page();await settled(p);assert.equal(requests.some(r=>r.includes('/desktop')),false);await p.close();role='owner';
  });
  await t.test('failed allocation never launches desktop',async()=>{
   state='failed';const p=await page();await p.waitForFunction(()=>document.querySelector('#error').textContent.includes('Fixture stopped box'));
   assert.equal(requests.some(r=>r.includes('/desktop')),false);await p.close();state='running';
  });
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
