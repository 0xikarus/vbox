import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import puppeteer from 'puppeteer-core';

test('Grid fills every running box, prefers desktop, switches viewers and replaces dropouts',async()=>{
 let boxes=Array.from({length:6},(_,i)=>({id:'b'+i,name:'Box '+i,state:'running'}));
 boxes.push({id:'sleep',name:'Sleeping',state:'hibernated'});
 const calls=[];
 const server=http.createServer(async(req,res)=>{
  calls.push([req.method,req.url]);res.setHeader('Content-Type','application/json');
  if(req.url==='/v1/browser-session'){res.statusCode=204;return res.end()}
  if(req.url==='/v1/whoami')return res.end('{}');
  if(req.url==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
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
  const p=await browser.newPage();const errors=[];p.on('pageerror',e=>errors.push(e.message));
  await p.evaluateOnNewDocument(()=>{
   window.connections=[];
   const attach=(kind,id,cb,o)=>{const c={kind,id,drop:o.onDisconnect,closed:false};connections.push(c);o.root.textContent=kind+' '+id;cb('Connected');return()=>{c.closed=true}};
   window.openWorkspaceDesktop=(id,cb,o)=>attach('desktop',id,cb,o);
   window.openWorkspaceTerminal=(id,s,cb,o)=>attach('terminal',id,cb,o);
  });
  await p.goto('http://127.0.0.1:'+server.address().port+'/grid');
  await p.waitForFunction(()=>connections.length===6);
  assert.equal(await p.$$('.tile').then(x=>x.length),6);
  assert.deepEqual(await p.evaluate(()=>connections.map(c=>[c.id,c.kind]).sort()),Array.from({length:6},(_,i)=>['b'+i,i===1?'terminal':'desktop']));
  await p.select('.tile:first-child select[aria-label=Viewer]','terminal');
  await p.waitForFunction(()=>connections.some(c=>c.id==='b0'&&c.kind==='terminal'));
  await p.select('.tile:first-child select[aria-label=Viewer]','desktop');
  await p.waitForFunction(()=>connections.filter(c=>c.id==='b0'&&c.kind==='desktop').length===2);
  await p.select('#layout select[name=columns]','1');await p.select('#layout select[name=rows]','1');await p.$eval('#layout',f=>f.requestSubmit());
  await p.evaluate(()=>connections.findLast(c=>!c.closed&&c.id==='b0').drop());
  await p.waitForFunction(()=>document.querySelector('.tile .screen').textContent==='terminal b1');
  boxes=boxes.filter(b=>b.id!=='b1');await p.click('#refresh');
  await p.waitForFunction(()=>document.querySelector('.tile .screen').textContent==='desktop b2');
  assert(calls.some(([method,url])=>method==='POST'&&url==='/v1/logical-boxes/b1/sessions/interactive'));
  assert(!calls.some(([,url])=>url.includes('/sleep/')||url.includes('/allocate')||url.includes('/enable')));
  assert.deepEqual(errors,[]);
  await p.click('#logout');await p.waitForFunction(()=>document.querySelector('#grid-app').hidden);
  assert.equal(await p.evaluate(()=>connections.filter(c=>!c.closed).length),0);
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
