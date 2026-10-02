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
   await page.$eval('#chat-info',button=>button.click());
   await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
   await page.$eval('.inspect-technical > summary',summary=>{if(!summary.parentElement.open)summary.click()});
   await page.waitForFunction(()=>document.querySelector('.inspect-technical').open);
   return page;
  }
  const fresh=await open('fresh');
  await fresh.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('No reliable specific phrase'));
  const rows=await fresh.$$eval('#inspect-activity-rows > div',elements=>elements.map(row=>({label:row.querySelector('dt').textContent,value:row.querySelector('dd').textContent,note:row.querySelector('small')?.textContent||'',title:row.querySelector('dd').title,noteTitle:row.querySelector('small')?.title||''})));
  assert.deepEqual(rows.map(row=>row.label).slice(2,6),['Last agent activity','Mood classifier','Activity','Last heartbeat']);
  assert.match(rows[3].value,/^Working · 1[0-9] s ago$/);
  assert.match(rows[3].title,/mood idle · activity working/);
  assert.match(rows[4].value,/^Working · 1[0-9] s ago$/);
  assert.equal(rows[4].note,'No reliable specific phrase');
  assert.ok(rows[4].title&&rows[4].noteTitle,'status and observation have absolute-time tooltips');
  assert.match(rows[5].value,/^1[0-9] s ago$/);
  assert.ok(rows[5].title,'heartbeat has an absolute-time tooltip');
  const first=rows[5].value;
  await fresh.waitForFunction(previous=>document.querySelectorAll('#inspect-activity-rows > div')[5]?.querySelector('dd')?.textContent!==previous,{timeout:11000},first);
  const layout=await fresh.evaluate(()=>{
   const row=document.querySelector('#inspect-runtime-rows > div');
   const button=document.querySelector('#inspect-clear-attachments');
   const parent=button.parentElement;
   const style=getComputedStyle(parent);
   return {columns:getComputedStyle(row).gridTemplateColumns,align:getComputedStyle(row.querySelector('dd')).textAlign,buttonWidth:button.getBoundingClientRect().width,parentWidth:parent.getBoundingClientRect().width,paddingLeft:parseFloat(style.paddingLeft),paddingRight:parseFloat(style.paddingRight)};
  });
  assert.equal(layout.align,'left');
  assert.equal(layout.columns.trim().split(' ').length,1,'narrow details use a single readable column');
  assert.ok(layout.buttonWidth>=layout.parentWidth-layout.paddingLeft-layout.paddingRight-1,'attachment action fills the available width: '+JSON.stringify(layout));
  await fresh.close();

  const specific=await open('specific');
  await specific.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('Editing chat.js'));
  assert.match(await specific.$eval('#inspect-activity-rows > div:nth-child(5) dd',element=>element.textContent),/^«Editing chat.js» · 1[0-9] s ago$/);
  await specific.close();

  const quiet=await open('quiet');
  await quiet.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('No new transcript activity'));
  assert.equal(await quiet.$eval('#inspect-activity-rows > div:nth-child(5) dd',element=>element.textContent.startsWith('Idle ·')),true);
  assert.equal(await quiet.$eval('#inspect-subtitle',element=>element.textContent),'codex · idle');
  await quiet.close();

  const stale=await open('stale');
  await stale.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('Heartbeat stale'));
  const staleRows=await stale.$$eval('#inspect-activity-rows > div',elements=>elements.slice(3,6).map(row=>({value:row.querySelector('dd').textContent,note:row.querySelector('small')?.textContent||'',muted:row.querySelector('dd').classList.contains('observation-stale')})));
  assert.match(staleRows[0].value,/^Angry · 1 min ago$/);
  assert.equal(staleRows[0].note,'stale');assert.equal(staleRows[0].muted,true);
  assert.match(staleRows[1].value,/^Unknown · 1 min ago$/);
  assert.equal(staleRows[1].note,'Heartbeat stale; activity unknown');
  assert.equal(staleRows[2].muted,true);
  await stale.close();

  const never=await open('never');
  const neverRows=await never.$$eval('#inspect-activity-rows > div',elements=>elements.slice(3,6).map(row=>row.querySelector('dd').textContent));
  assert.deepEqual(neverRows,['never','never','never']);
  await never.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
