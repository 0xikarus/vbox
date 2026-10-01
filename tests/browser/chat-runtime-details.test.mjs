import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','run-budget-policy.js','idle-policy.css'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

test('chat details shows live elapsed runtime even with the run-time limit off',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude'};
 const runningSince=new Date(Date.now()-65000).toISOString();
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/builder/run-budget-policy')return res.end(JSON.stringify({seconds:0,state:box.state,...(box.state==='running'?{runningSince}:{})}));
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Builder'&&document.querySelector('#chat-loading').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await page.waitForFunction(()=>/^Current run: \d+m \d+s$/.test(document.querySelector('#inspect-run-budget-policy .run-budget-elapsed')?.textContent||''));
  const before=await page.$eval('#inspect-run-budget-policy .run-budget-elapsed',el=>el.textContent);
  assert.match(before,/Current run: \d+m \d+s/);
  assert.match(await page.$eval('#inspect-run-budget-policy .run-budget-policy',el=>el.textContent),/Run-time limitOff/);
  await page.waitForFunction(previous=>document.querySelector('#inspect-run-budget-policy .run-budget-elapsed')?.textContent!==previous,{},before);
  box.state='hibernated';
  await page.evaluate(()=>document.querySelector('#refresh').click());
  await page.waitForFunction(()=>document.querySelector('#inspect-run-budget-policy .run-budget-elapsed')?.textContent==='Current run: Not running');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

test('owner can add time or reset the current countdown from chat details',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude'};
 const calls=[];
 const policy={seconds:4*3600,remainingSeconds:4*3600,deadlineAt:new Date(Date.now()+4*3600*1000).toISOString(),runningSince:new Date(Date.now()-65000).toISOString(),state:'running'};
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/builder/run-budget-policy/adjust'){
   let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{
    const body=JSON.parse(raw);calls.push(body);
    if(body.expectedDeadlineAt!==policy.deadlineAt){res.statusCode=409;return res.end(JSON.stringify({error:'run-time countdown changed'}))}
    policy.remainingSeconds=body.action==='reset'?policy.seconds:policy.remainingSeconds+body.seconds;
    policy.deadlineAt=new Date(Date.now()+policy.remainingSeconds*1000).toISOString();
    res.end(JSON.stringify(policy));
   });return;
  }
  if(path==='/v1/logical-boxes/builder/run-budget-policy')return res.end(JSON.stringify(policy));
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Builder'&&document.querySelector('#chat-loading').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  const button=caption=>'#inspect-run-budget-policy .run-budget-actions button';
  await page.waitForFunction(()=>[...document.querySelectorAll('#inspect-run-budget-policy .run-budget-actions button')].some(el=>el.textContent==='+4h'&&!el.disabled));
  await page.evaluate(selector=>[...document.querySelectorAll(selector)].find(el=>el.textContent==='+4h').click(),button());
  await page.waitForFunction(()=>document.querySelector('#inspect-run-budget-policy .idle-policy-status')?.textContent.startsWith('Added 4 hours.'));
  assert.deepEqual(calls.map(call=>[call.action,call.seconds]),[['add',4*3600]]);
  await page.evaluate(selector=>[...document.querySelectorAll(selector)].find(el=>el.textContent==='Reset countdown').click(),button());
  await page.waitForFunction(()=>document.querySelector('#inspect-run-budget-policy .idle-policy-status')?.textContent.startsWith('Countdown reset.'));
  assert.deepEqual(calls.map(call=>[call.action,call.seconds]),[['add',4*3600],['reset',0]]);
  assert.equal(policy.remainingSeconds,4*3600);
  box.state='hibernated';policy.state='hibernated';delete policy.deadlineAt;
  await page.evaluate(()=>document.querySelector('#refresh').click());
  await page.waitForFunction(()=>[...document.querySelectorAll('#inspect-run-budget-policy .run-budget-actions button')].every(el=>el.disabled));
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
