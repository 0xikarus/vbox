import assert from 'node:assert/strict';
import {before,after,test} from 'node:test';
import {readFile,mkdtemp,rm} from 'node:fs/promises';
import {execFileSync,spawn} from 'node:child_process';
import http from 'node:http';
import {resolve} from 'node:path';
import {WebSocketServer} from 'ws';
import puppeteer from 'puppeteer-core';

let browser,server,wss,base,dir,socket;const calls=[],connections=[],children=new Set();let delayFirst=false;
const boxes=Array.from({length:4},(_,i)=>({id:'box'+(i+1),name:'box'+(i+1),state:'running'}));boxes.push({id:'sleeping',name:'sleeping',state:'hibernated'});
const tmux=(...args)=>execFileSync('tmux',['-S',socket,...args],{encoding:'utf8',env:{...process.env,TMUX:'',TERM:'xterm-256color'}});
before(async()=>{
 dir=await mkdtemp('/tmp/vmbox-grid-test-');socket=dir+'/tmux.sock';
 for(let i=1;i<=4;i++)tmux('-f','/dev/null','new-session','-d','-s','box'+i,'bash --noprofile --norc');
 server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;calls.push({path,method:req.method});
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{}');if(path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
   if(path==='/v1/browser-session'){res.statusCode=204;return res.end();}
   const match=path.match(/^\/v1\/logical-boxes\/(box[1-4])\/sessions(\/primary)?$/);
   if(match){if(delayFirst&&match[1]==='box1'&&!match[2])await new Promise(r=>setTimeout(r,500));return res.end(JSON.stringify(match[2]?{session:match[1]}:{state:'live',partial:false,sessions:[{id:'$0',name:match[1]}]}));}
   res.statusCode=404;return res.end('{}');
  }
  const file=path==='/grid'?'grid.html':path.slice(1);if(!/^[a-z0-9.-]+$/.test(file)){res.statusCode=404;return res.end();}
  try{res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(await readFile(resolve('internal/controller/web',file)));}catch{res.statusCode=404;res.end();}
 });
 wss=new WebSocketServer({server});wss.on('connection',(ws,req)=>{
  const url=new URL(req.url,'http://local'),name=url.pathname.split('/')[3];assert.match(name,/^box[1-4]$/);assert.equal(url.searchParams.get('session'),name);connections.push(name);
  const child=spawn('script',['-q','-f','-c',`tmux -S ${socket} attach-session -t ${name}`,'/dev/null'],{env:{...process.env,TMUX:'',TERM:'xterm-256color'},detached:true});children.add(child);
  child.stdout.on('data',b=>{if(ws.readyState===1)ws.send(b)});child.stderr.on('data',()=>{});
  ws.on('message',raw=>{const frame=JSON.parse(raw);if(frame.data)child.stdin.write(Buffer.from(frame.data,'base64'));if(frame.cols)tmux('resize-window','-t',name,'-x',String(frame.cols),'-y',String(frame.rows));});
  ws.on('close',()=>{try{process.kill(-child.pid,'SIGTERM')}catch{}});child.on('exit',()=>{children.delete(child);ws.close()});
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
});
after(async()=>{await browser?.close();for(const c of children){try{process.kill(-c.pid,'SIGTERM')}catch{}}for(const ws of wss?.clients||[])ws.terminate();await new Promise(r=>wss?.close(r));await new Promise(r=>server?.close(r));try{tmux('kill-server')}catch{}if(dir)await rm(dir,{recursive:true,force:true});});
async function pageReady(){const p=await browser.newPage();await p.evaluateOnNewDocument(()=>{let cls;window.testTerminals=[];Object.defineProperty(window,'Terminal',{configurable:true,get:()=>cls,set:value=>{cls=class extends value{constructor(...args){super(...args);window.testTerminals.push(this)}}}})});await p.setViewport({width:1500,height:1100});await p.goto(base+'/grid');await p.waitForSelector('.tile select option[value=box4]');return p;}
test('four real tmux terminals, isolated input, layouts and viewer-only disconnect',async()=>{
 const p=await pageReady(),errors=[];p.on('pageerror',e=>errors.push(e.message));assert.equal(await p.$$('.tile').then(x=>x.length),4);
 assert.deepEqual(await p.$$eval('.tile header label:first-child select',xs=>xs.map(x=>x.value)),['box1','box2','box3','box4']);
 await p.waitForFunction(()=>[...document.querySelectorAll('.tile p')].every(n=>n.textContent.includes('Connected')));
 for(let i=1;i<=4;i++){
  const a=Math.floor(Math.random()*100000)+10000,answer=a+37,marker='GRID_'+i+'_'+answer;
  await p.click(`.tile:nth-child(${i}) .screen`,{offset:{x:20,y:20}});await p.keyboard.type(`printf 'GRID_${i}_%s\\n' "$(( ${a} + 37 ))"`);await p.keyboard.press('Enter');
  for(let n=0;n<30&&!tmux('capture-pane','-p','-t','box'+i).includes(marker);n++)await new Promise(r=>setTimeout(r,30));
  assert(tmux('capture-pane','-p','-t','box'+i).includes(marker),'real command result missing');
  await p.waitForFunction(marker=>window.testTerminals.some(t=>{const b=t.buffer.active;return Array.from({length:b.length},(_,i)=>b.getLine(i)?.translateToString(true)||'').join('\n').includes(marker)}),{},marker);
  for(let j=1;j<=4;j++)if(j!==i)assert(!tmux('capture-pane','-p','-t','box'+j).includes(marker),'input crossed boxes');
 }
 assert.equal(await p.$eval('#tiles',n=>getComputedStyle(n).gridTemplateColumns.split(' ').length),2);
 assert(await p.evaluate(()=>[...document.querySelectorAll('.tile')].every(t=>t.getBoundingClientRect().bottom<=innerHeight)),'four tiles must fit on one desktop screen');
 await p.click('.tile:first-child summary');await p.$$eval('.tile:first-child .terminal-keys button',xs=>xs.find(x=>x.textContent==='Fullscreen').click());await p.waitForFunction(()=>!!document.fullscreenElement);await p.evaluate(()=>document.exitFullscreen());
 const pid=tmux('display-message','-p','-t','box1','#{pane_pid}');
 await p.$eval('.tile:first-child header button',b=>b.click());await p.waitForFunction(()=>document.querySelector('.tile p').textContent.includes('Connected'));assert.equal(tmux('display-message','-p','-t','box1','#{pane_pid}'),pid);
 await p.screenshot({path:'/tmp/vmbox-grid-desktop.png',fullPage:true});
 await p.select('#layout select[name=rows]','2');await p.select('#layout select[name=columns]','3');await p.$eval('#layout',f=>f.requestSubmit());assert.equal(await p.$$('.tile').then(x=>x.length),6);
 await p.select('#layout select[name=columns]','1');await p.select('#layout select[name=rows]','1');await p.$eval('#layout',f=>f.requestSubmit());assert.equal(await p.$$('.tile').then(x=>x.length),1);
 assert.equal(tmux('list-sessions','-F','#{session_name}').trim().split('\n').length,4);
 await p.$eval('.tile header button:last-child',b=>b.click());assert.equal(tmux('list-sessions','-F','#{session_name}').trim().split('\n').length,4);
 assert.equal(calls.filter(c=>c.method!=='GET').length,0);assert.deepEqual(errors,[]);await p.close();
});
test('delayed selection cannot attach stale box; sleeping boxes never resume; mobile stacks',async()=>{
 const p=await pageReady();await p.waitForFunction(()=>[...document.querySelectorAll('.tile p')].every(n=>n.textContent.includes('Connected')));await p.select('#layout select[name=columns]','1');await p.select('#layout select[name=rows]','1');await p.$eval('#layout',f=>f.requestSubmit());delayFirst=true;const first=connections.length;
 const selector='.tile:first-child header label:first-child select';await p.select(selector,'box1');await p.select(selector,'box2');
 await p.waitForFunction(()=>document.querySelector('.tile p').textContent.includes('Connected'));await new Promise(r=>setTimeout(r,600));
 assert.deepEqual(connections.slice(first),['box2']);
 await p.select(selector,'sleeping');assert.match(await p.$eval('.tile p',n=>n.textContent),/hibernated/);
 assert(!calls.some(c=>c.path.includes('allocate')||c.path.includes('interactive')));
 await p.setViewport({width:390,height:844});assert.equal(await p.$eval('#tiles',n=>getComputedStyle(n).gridTemplateColumns.split(' ').length),1);
 assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await p.close();delayFirst=false;
});
