import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));
const boxes=[{id:'fresh',name:'Fresh',state:'running',defaultAgent:'codex'},{id:'specific',name:'Specific',state:'running',defaultAgent:'codex'},{id:'quiet',name:'Quiet',state:'running',defaultAgent:'codex'},{id:'stale',name:'Stale',state:'running',defaultAgent:'claude'},{id:'never',name:'Never',state:'running',defaultAgent:'codex'}];
const ago=seconds=>new Date(Date.now()-seconds*1000).toISOString();

test('Details shows a specific phrase, busy fallback, quiet state, stale state, and never state',async()=>{
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path])}
  res.setHeader('Content-Type','application/json');
  if(path==='/healthz')return res.end('{"status":"ok"}');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/box-activity')return res.end(JSON.stringify([
   {boxId:'fresh',busy:true,observedAt:ago(12),lastObservedAt:ago(12),lastMood:'idle',lastActivity:'working',lastPhrase:'Fixing mascot eyes',lastPhraseAt:ago(180),status:'Working',statusSource:'fallback',statusAt:ago(12),phrase:'Working'},
   {boxId:'specific',busy:true,observedAt:ago(12),lastObservedAt:ago(12),lastMood:'idle',lastActivity:'working',lastPhrase:'Editing chat.js',lastPhraseAt:ago(12),status:'Editing chat.js',statusSource:'specific',statusAt:ago(12),phrase:'Editing chat.js'},
   {boxId:'quiet',busy:true,observedAt:ago(12),lastObservedAt:ago(12),lastMood:'idle',lastActivity:'idle',lastPhrase:'Old task',lastPhraseAt:ago(180),status:'Idle',statusSource:'quiet',statusAt:ago(12),phrase:'Idle'},
   {boxId:'stale',busy:true,lastObservedAt:ago(70),lastMood:'angry',lastActivity:'idle',lastPhrase:'Reviewing screenshots',lastPhraseAt:ago(1200),status:'Unknown',statusSource:'stale',statusAt:ago(70)},
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
   await page.click('#chat-info');await page.click('[data-ip-row="technical"]');
   await page.waitForFunction(()=>!document.querySelector('[data-ip-page="technical"]').hidden);
   return page;
  }
  const readRows=page=>page.$$eval('#ip-technical-table .ip-technical-row',elements=>elements.map(row=>({label:row.querySelector('span:first-child').textContent,value:row.querySelector('span:nth-child(2)').textContent,note:row.dataset.detailNote||'',title:row.querySelector('span:nth-child(2)').title})));
  const fresh=await open('fresh');
  await fresh.waitForFunction(()=>document.querySelector('#ip-technical-table')?.textContent.includes('No reliable specific phrase')||document.querySelector('#ip-technical-table [data-detail-note="No reliable specific phrase"]'));
  const rows=await readRows(fresh);
  const byLabel=Object.fromEntries(rows.map(row=>[row.label,row]));
  assert.deepEqual(['Last agent activity','Mood classifier','Activity','Last heartbeat'].filter(label=>byLabel[label]),['Mood classifier','Activity','Last heartbeat']);
  assert.match(byLabel['Mood classifier'].value,/^Working · 1[0-9] s ago$/);
  assert.match(byLabel['Mood classifier'].title,/mood idle · activity working/);
  assert.match(byLabel.Activity.value,/^Working · 1[0-9] s ago$/);
  assert.equal(byLabel.Activity.note,'No reliable specific phrase');
  assert.ok(byLabel.Activity.title,'status has an absolute-time tooltip');
  assert.match(byLabel['Last heartbeat'].value,/^1[0-9] s ago$/);
  const first=byLabel['Last heartbeat'].value;
  await fresh.waitForFunction(previous=>[...document.querySelectorAll('#ip-technical-table .ip-technical-row')].find(row=>row.querySelector('span')?.textContent==='Last heartbeat')?.querySelector('span:nth-child(2)')?.textContent!==previous,{timeout:11000},first);
  const layout=await fresh.evaluate(()=>{const row=document.querySelector('#ip-technical-table .ip-technical-row');const label=row.querySelector('span:first-child').getBoundingClientRect();const value=row.querySelector('span:nth-child(2)').getBoundingClientRect();return {sameLine:Math.abs(label.top-value.top)<10,copyIcons:[...document.querySelectorAll('#ip-technical-table .ip-technical-row')].filter(el=>el.querySelector('svg')).map(el=>el.querySelector('span').textContent)}});
  assert.equal(layout.sameLine,true,'technical details use one-line key/value rows');
  assert.ok(layout.copyIcons.every(label=>label==='Slot'||label.endsWith('ID')),'only identifiers show copy actions');
  await fresh.close();

  const specific=await open('specific');
  await specific.waitForFunction(()=>document.querySelector('#ip-technical-table')?.textContent.includes('Editing chat.js'));
  assert.match((await readRows(specific)).find(row=>row.label==='Activity').value,/^«Editing chat.js» · 1[0-9] s ago$/);
  await specific.close();
  const quiet=await open('quiet');
  await quiet.waitForFunction(()=>document.querySelector('#ip-technical-table [data-detail-note^="No new transcript activity"]'));
  assert.equal((await readRows(quiet)).find(row=>row.label==='Activity').value.startsWith('Idle ·'),true);
  assert.equal(await quiet.$eval('#inspect-subtitle',element=>element.textContent),'codex · idle');
  await quiet.close();
  const stale=await open('stale');
  await stale.waitForFunction(()=>document.querySelector('#ip-technical-table [data-detail-note="Heartbeat stale; activity unknown"]'));
  const staleRows=Object.fromEntries((await readRows(stale)).map(row=>[row.label,row]));
  assert.match(staleRows['Mood classifier'].value,/^Angry · 1 min ago$/);
  assert.equal(staleRows['Mood classifier'].note,'stale');
  assert.match(staleRows.Activity.value,/^Unknown · 1 min ago$/);
  assert.equal(staleRows.Activity.note,'Heartbeat stale; activity unknown');
  await stale.close();
  const never=await open('never');
  const neverRows=await readRows(never);
  assert.ok(!neverRows.some(row=>['Mood classifier','Activity','Last heartbeat'].includes(row.label)),'unknown observations do not add placeholder rows');
  await never.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
