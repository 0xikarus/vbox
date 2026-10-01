import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));
const boxes=[{id:'fresh',name:'Fresh',state:'running',defaultAgent:'codex'},{id:'stale',name:'Stale',state:'running',defaultAgent:'claude'},{id:'never',name:'Never',state:'running',defaultAgent:'codex'}];
const ago=seconds=>new Date(Date.now()-seconds*1000).toISOString();

test('Details shows model updates, an empty run, stale state, and never state',async()=>{
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path])}
  res.setHeader('Content-Type','application/json');
  if(path==='/healthz')return res.end('{"status":"ok"}');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-activity')return res.end(JSON.stringify([
   {boxId:'fresh',busy:true,lastObservedAt:ago(12),lastMood:'idle',lastActivity:'working',lastPhrase:'Fixing mascot eyes',lastPhraseAt:ago(180)},
   {boxId:'stale',busy:true,lastObservedAt:ago(70),lastMood:'angry',lastActivity:'idle',lastPhrase:'Reviewing screenshots',lastPhraseAt:ago(1200)},
   {boxId:'never',busy:null},
  ]));
  if(path.endsWith('/messages')||path==='/v1/tool-presets'||path==='/v1/box-conversations')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  async function open(id){
   const page=await browser.newPage();await page.setViewport({width:1440,height:900});
   await page.goto(base+'/chat#box='+id);
   await page.waitForFunction(id=>document.querySelector('#chat-header-name')?.textContent===id[0].toUpperCase()+id.slice(1),{},id);
   await page.$eval('#chat-info',button=>button.click());
   await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
   await page.$eval('.inspect-technical > summary',summary=>{if(!summary.parentElement.open)summary.click()});
   await page.waitForFunction(()=>document.querySelector('.inspect-technical').open);
   return page;
  }
  const fresh=await open('fresh');
  await fresh.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('Fixing mascot eyes'));
  const rows=await fresh.$$eval('#inspect-activity-rows > div',elements=>elements.map(row=>({label:row.querySelector('dt').textContent,value:row.querySelector('dd').textContent,note:row.querySelector('small')?.textContent||'',title:row.querySelector('dd').title,noteTitle:row.querySelector('small')?.title||''})));
  assert.deepEqual(rows.map(row=>row.label).slice(2,6),['Last agent activity','Mood classifier','Activity model','Last heartbeat']);
  assert.match(rows[3].value,/^Working · 1[0-9] s ago$/);
  assert.match(rows[3].title,/mood idle · activity working/);
  assert.match(rows[4].value,/^«Fixing mascot eyes» · 3 min ago$/);
  assert.match(rows[4].note,/^last run 1[0-9] s ago: no confident phrase$/);
  assert.ok(rows[4].title&&rows[4].noteTitle,'phrase and last run have absolute-time tooltips');
  assert.match(rows[5].value,/^1[0-9] s ago$/);
  assert.ok(rows[5].title,'heartbeat has an absolute-time tooltip');
  const first=rows[5].value;
  await fresh.waitForFunction(previous=>document.querySelectorAll('#inspect-activity-rows > div')[5]?.querySelector('dd')?.textContent!==previous,{timeout:11000},first);
  await fresh.close();

  const stale=await open('stale');
  await stale.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('Reviewing screenshots'));
  const staleRows=await stale.$$eval('#inspect-activity-rows > div',elements=>elements.slice(3,6).map(row=>({value:row.querySelector('dd').textContent,note:row.querySelector('small')?.textContent||'',muted:row.querySelector('dd').classList.contains('observation-stale')})));
  assert.match(staleRows[0].value,/^Angry · 1 min ago$/);
  assert.equal(staleRows[0].note,'stale');assert.equal(staleRows[0].muted,true);
  assert.match(staleRows[1].value,/^«Reviewing screenshots» · 20 min ago$/);
  assert.match(staleRows[1].note,/no confident phrase$/);
  assert.equal(staleRows[2].muted,true);
  await stale.close();

  const never=await open('never');
  const neverRows=await never.$$eval('#inspect-activity-rows > div',elements=>elements.slice(3,6).map(row=>row.querySelector('dd').textContent));
  assert.deepEqual(neverRows,['never','never','never']);
  await never.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
