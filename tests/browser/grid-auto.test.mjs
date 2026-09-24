import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import puppeteer from 'puppeteer-core';

test('Grid shows desktop above TMUX and replaces boxes only when both viewers drop',async()=>{
 let boxes=Array.from({length:6},(_,i)=>({id:'b'+i,name:'Box '+i,state:'running'}));
 boxes.push({id:'sleep',name:'Sleeping',state:'hibernated'});
 const calls=[];
 const server=http.createServer(async(req,res)=>{
  calls.push([req.method,req.url]);res.setHeader('Content-Type','application/json');
  if(req.url==='/v1/browser-session'){res.statusCode=204;return res.end()}
  if(req.url==='/v1/whoami')return res.end('{}');
  if(req.url==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
  if(req.url==='/v1/logical-boxes/b0/desktop'&&req.method==='GET'){res.statusCode=409;return res.end('{}')}
  if(req.url.endsWith('/desktop'))return res.end(JSON.stringify({enabled:!req.url.includes('/b1/')}));
  if(req.url.endsWith('/sessions/interactive'))return res.end('{"session":"shell"}');
  if(req.url==='/v1/logical-boxes/b1/sessions')return res.end('{"state":"live","sessions":[]}');
  if(req.url.endsWith('/sessions/primary'))return res.end('{"session":"shell"}');
  if(req.url.endsWith('/sessions'))return res.end('{"state":"live","sessions":[{"name":"shell"}]}');
  const file=req.url==='/grid'?'grid.html':req.url.slice(1);
  if(['xterm.js','xterm-fit.js','workspace-terminal.js','novnc.js','workspace-desktop.js'].includes(file)){res.setHeader('Content-Type','text/javascript');return res.end('')}
  try{let data=await readFile('internal/controller/web/'+file,'utf8');res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(data)}catch{res.end('')}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',args:['--no-sandbox']});
 try{
  const p=await browser.newPage();await p.setViewport({width:1400,height:900});const errors=[];p.on('pageerror',e=>errors.push(e.message));
  await p.evaluateOnNewDocument(()=>{
   window.connections=[];
   const attach=(kind,id,cb,o)=>{const c={kind,id,drop:()=>{o.onMetrics?.({state:'disconnected',ping:null});cb('Disconnected');o.onDisconnect?.()},closed:false};connections.push(c);o.root.textContent=kind+' '+id;cb('Connected');o.onMetrics?.({state:'connected',ping:kind==='desktop'?38:12});return()=>{c.closed=true}};
   window.openWorkspaceDesktop=(id,cb,o)=>attach('desktop',id,cb,o);
   window.openWorkspaceTerminal=(id,s,cb,o)=>attach('terminal',id,cb,o);
  });
  await p.goto('http://127.0.0.1:'+server.address().port+'/grid');
  await p.waitForFunction(()=>connections.length===11);
  assert.equal(await p.$$('.tile').then(x=>x.length),6);
  assert.deepEqual(await p.evaluate(()=>{const result={};for(const c of connections)(result[c.id]??=[]).push(c.kind);for(const kinds of Object.values(result))kinds.sort();return result}),Object.fromEntries(Array.from({length:6},(_,i)=>['b'+i,i===1?['terminal']:['desktop','terminal']])));
  assert.equal(await p.$eval('.tile:first-child',tile=>tile.querySelector('.tile-controls').getBoundingClientRect().bottom<=tile.querySelector('header').getBoundingClientRect().top),true,'viewer controls should sit above the box and session selectors');
  assert.equal(await p.$eval('.tile:first-child .desktop-control-menu',menu=>menu.hidden),false);
  assert.equal(await p.$eval('.tile:nth-child(2) .desktop-control-menu',menu=>menu.hidden),true);
  assert.deepEqual(await p.$eval('.tile:first-child',tile=>[tile.querySelector('.desktop-panel .viewer-overlay').hidden,tile.querySelector('.terminal-panel .viewer-overlay').hidden]),[true,true],'viewer labels should disappear after both streams connect');
  assert.deepEqual(await p.$eval('.tile:first-child',tile=>[...tile.querySelectorAll('.connection-badge')].map(b=>b.textContent)),['Live · 38 ms','Live · 12 ms']);
  assert.equal(await p.$eval('.tile:first-child .desktop-panel',panel=>{const p=panel.getBoundingClientRect(),b=panel.querySelector('.connection-badge').getBoundingClientRect();return b.right<=p.right&&p.right-b.right<20&&b.top>=p.top&&b.top-p.top<20}),true,'connection status should overlay the top-right of each viewer');
  assert.equal(await p.$eval('.tile:first-child',tile=>tile.querySelector('.desktop-screen').getBoundingClientRect().bottom<tile.querySelector('.terminal-screen').getBoundingClientRect().top),true);
  assert.equal(await p.evaluate(()=>{const tiles=document.querySelector('#tiles').getBoundingClientRect(),tile=document.querySelector('.tile'),desktop=tile.querySelector('.desktop-screen').getBoundingClientRect(),terminal=tile.querySelector('.terminal-panel').getBoundingClientRect();return Math.abs(tiles.width-innerWidth)<=1&&desktop.left-tile.getBoundingClientRect().left<=1&&tile.getBoundingClientRect().right-desktop.right<=1&&Math.abs(terminal.top-desktop.bottom)<=1}),true,'Grid viewers should fill the tile and meet without a gutter');
  await p.setViewport({width:390,height:844});
  assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await p.setViewport({width:1400,height:900});
  await p.$eval('.tile:first-child header label:nth-child(2) select',select=>select.add(new Option('alternate','alternate')));
  await p.select('.tile:first-child header label:nth-child(2) select','alternate');
  await p.waitForFunction(()=>connections.filter(c=>c.id==='b0'&&c.kind==='terminal').length===2);
  assert.equal(await p.evaluate(()=>connections.find(c=>c.id==='b0'&&c.kind==='desktop').closed),false,'changing TMUX sessions must leave Desktop attached');
  await p.select('#layout select[name=columns]','1');await p.select('#layout select[name=rows]','1');await p.$eval('#layout',f=>f.requestSubmit());
  await p.evaluate(()=>connections.findLast(c=>!c.closed&&c.id==='b0'&&c.kind==='terminal').drop());
  assert.equal(await p.$eval('.tile:first-child header select',select=>select.value),'b0','desktop keeps the box visible when TMUX disconnects');
  assert.deepEqual(await p.$eval('.tile:first-child .terminal-panel .viewer-overlay',overlay=>({visible:!overlay.hidden,text:overlay.textContent})),{visible:true,text:'Disconnected'});
  assert.equal(await p.$eval('.tile:first-child .terminal-panel .connection-badge',badge=>badge.textContent),'Disconnected');
  await p.evaluate(()=>connections.findLast(c=>!c.closed&&c.id==='b0'&&c.kind==='desktop').drop());
  await p.waitForFunction(()=>document.querySelector('.tile .terminal-screen').textContent==='terminal b1');
  boxes=boxes.filter(b=>b.id!=='b1');await p.click('#refresh');
  await p.waitForFunction(()=>document.querySelector('.tile .desktop-screen').textContent==='desktop b2');
  assert(calls.some(([method,url])=>method==='POST'&&url==='/v1/logical-boxes/b1/sessions/interactive'));
  assert(!calls.some(([,url])=>url.includes('/sleep/')||url.includes('/allocate')||url.includes('/enable')));
  assert.deepEqual(errors,[]);
  await p.click('#logout');await p.waitForFunction(()=>document.querySelector('#grid-app').hidden);
  assert.equal(await p.evaluate(()=>connections.filter(c=>!c.closed).length),0);
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
test('grid tiles only offer actions the box state allows',async()=>{
 const boxes=[{id:'run',name:'Run',state:'running'},{id:'sleep',name:'Sleep',state:'hibernated'},{id:'build',name:'Build',state:'attaching'}];
 const calls=[];
 const server=http.createServer(async(req,res)=>{
  calls.push([req.method,req.url]);res.setHeader('Content-Type','application/json');
  if(req.url==='/v1/browser-session'){res.statusCode=204;return res.end()}
  if(req.url==='/v1/whoami')return res.end('{}');
  if(req.url==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
  if(req.url.endsWith('/desktop'))return res.end(JSON.stringify({enabled:false}));
  if(req.url.endsWith('/sessions/interactive'))return res.end('{"session":"shell"}');
  if(req.url.endsWith('/sessions/primary'))return res.end('{"session":"shell"}');
  if(req.url.endsWith('/sessions'))return res.end('{"state":"live","sessions":[{"name":"shell"}]}');
  const file=req.url==='/grid'?'grid.html':req.url.slice(1);
  if(['xterm.js','xterm-fit.js','workspace-terminal.js','novnc.js','workspace-desktop.js'].includes(file)){res.setHeader('Content-Type','text/javascript');return res.end('')}
  try{let data=await readFile('internal/controller/web/'+file,'utf8');res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(data)}catch{res.end('')}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',args:['--no-sandbox']});
 try{
  const p=await browser.newPage();
  await p.evaluateOnNewDocument(()=>{
   window.connections=[];
   const attach=(kind,id,cb,o)=>{const c={kind,id,drop:o.onDisconnect,closed:false};connections.push(c);o.root.textContent=kind+' '+id;cb('Connected');o.onMetrics?.({state:'connected'});return()=>{c.closed=true}};
   window.openWorkspaceDesktop=(id,cb,o)=>attach('desktop',id,cb,o);
   window.openWorkspaceTerminal=(id,s,cb,o)=>attach('terminal',id,s,cb,o);
  });
  await p.goto('http://127.0.0.1:'+server.address().port+'/grid');
  await p.waitForFunction(()=>document.querySelectorAll('.tile').length>=2);
  const action=sel=>p.$eval(sel,e=>({hidden:e.hidden,disabled:e.disabled,text:e.textContent}));
  await p.select('.tile:nth-child(2) header label:first-of-type select','sleep');
  await p.waitForFunction(()=>document.querySelector('.tile:nth-child(2) header button:first-of-type').textContent==='Resume');
  assert.deepEqual(await action('.tile:nth-child(2) header button:first-of-type'),{hidden:false,disabled:false,text:'Resume'});
  await p.select('.tile:nth-child(2) header label:first-of-type select','build');
  await p.waitForFunction(()=>document.querySelector('.tile:nth-child(2) header button:first-of-type').hidden);
  assert.deepEqual(await action('.tile:nth-child(2) header button:first-of-type'),{hidden:true,disabled:true,text:'Resume'});
  assert(!calls.some(([,url])=>url.includes('/allocate')));
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
