import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

for(const agent of ['codex','claude','opencode'])test(agent+' wake offers an explicit restore choice and blocks sends until chosen',async()=>{
 const box={id:'sleeping',name:'sleeping',state:'hibernated',defaultAgent:agent};
 const candidate={sessionId:'01234567-89ab-cdef-0123-456789abcdef',savedAt:'2026-09-23T12:00:00Z',startedAt:'2026-09-23T11:00:00Z'};
 const decisions=[];let wakeRequests=0,offered=true;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/sleeping/allocate'){wakeRequests++;return res.end(JSON.stringify({requestId:'wake',state:'reserved'}))}
  if(path==='/v1/allocations/wake'){box.state='running';return res.end(JSON.stringify({requestId:'wake',state:'ready'}))}
  if(path==='/v1/logical-boxes/sleeping/agent-resume'){
   if(req.method==='GET')return res.end(JSON.stringify({candidate:offered?candidate:null}));
   let body='';for await(const chunk of req)body+=chunk;
   decisions.push(JSON.parse(body));offered=false;return res.end(JSON.stringify({choice:decisions.at(-1).choice}));
  }
  if(path.endsWith('/messages'))return res.end('[]');
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=sleeping');
  await page.waitForFunction(()=>!document.querySelector('#chat-wake').hidden);
  await page.type('#chat-input','Do not send yet');
  await page.click('#chat-wake');
  await page.waitForSelector('.codex-resume-card');
  assert.match(await page.$eval('.codex-resume-card strong',element=>element.textContent),new RegExp(agent,'i'));
  assert.equal(await page.$eval('#send',element=>element.disabled),true);
  assert.equal(wakeRequests,1);
  await page.click('.codex-resume-actions button:first-child');
  await page.waitForFunction(()=>!document.querySelector('.codex-resume-card'));
  assert.deepEqual(decisions,[{choice:'restore',sessionId:candidate.sessionId,savedAt:candidate.savedAt}]);
  assert.equal(await page.$eval('#send',element=>element.disabled),false);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

test('Chat restart checks for a saved conversation when allocation finishes',async()=>{
 const box={id:'restarting',name:'restarting',state:'running',defaultAgent:'claude'};
 const candidate={sessionId:'01234567-89ab-cdef-0123-456789abcdef',savedAt:'2026-09-27T12:00:00Z',startedAt:'2026-09-27T11:00:00Z'};
 let offered=false;
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/restarting/hibernate'){box.state='hibernated';return res.end(JSON.stringify(box))}
  if(path==='/v1/logical-boxes/restarting/allocate'){box.state='reserved';offered=true;return res.end(JSON.stringify({requestId:'restart',state:'reserved'}))}
  if(path==='/v1/allocations/restart'){box.state='running';return res.end(JSON.stringify({requestId:'restart',state:'ready'}))}
  if(path==='/v1/logical-boxes/restarting/agent-resume')return res.end(JSON.stringify({candidate:offered?candidate:null}));
  if(path.endsWith('/messages'))return res.end('[]');
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();page.on('dialog',dialog=>dialog.accept());
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=restarting');
  await page.waitForSelector('#chat-info:not([hidden])');
  await page.click('#chat-info');
  await page.waitForFunction(()=>[...document.querySelectorAll('#inspect-config-actions button')].some(button=>button.textContent==='Restart…'));
  await page.evaluate(()=>[...document.querySelectorAll('#inspect-config-actions button')].find(button=>button.textContent==='Restart…').click());
  await page.waitForSelector('.codex-resume-card',{timeout:15000});
  assert.match(await page.$eval('.codex-resume-card strong',element=>element.textContent),/Claude/);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
