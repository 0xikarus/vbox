import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222';
const key=a+'/'+b,now=new Date().toISOString();
const boxes=[{id:a,name:'Builder',state:'running',defaultAgent:'claude'},{id:b,name:'Reviewer',state:'running',defaultAgent:'codex'}];
const fakeDesktop=`window.fakeDesktop=[];window.NoVNC={default:class {
 constructor(root,url){this.root=root;this.url=url;this.handlers={};this.closed=false;window.fakeDesktop.push(this);root.innerHTML='<div class="fake-desktop">Desktop live</div>';setTimeout(()=>this.emit(window.failDesktop?'disconnect':'connect',{clean:false}),20)}
 addEventListener(type,fn){(this.handlers[type]??=[]).push(fn)}
 emit(type,detail={}){for(const fn of this.handlers[type]||[])fn({detail})}
 disconnect(){this.closed=true}
}};`;
const fakeTerminal=`window.fakeTerminal=[];window.openWorkspaceTerminal=(box,session,onStatus,options={})=>{
 const viewer={box,session,viewOnly:options.viewOnly,closed:false};window.fakeTerminal.push(viewer);
 options.root.innerHTML='<div class="fake-tmux">TMUX live</div>';setTimeout(()=>{if(!viewer.closed)onStatus('Connected · '+session)},20);
 return()=>{viewer.closed=true;options.root.replaceChildren()};
};`;

test('pair hero keeps two view-only tiles, falls back to TMUX, and opens the selected box',async()=>{
 let desktopA=true;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(await readFile(resolve(web,'chat.html')))}
  if(path==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(fakeDesktop)}
  if(path==='/workspace-terminal.js'){res.setHeader('Content-Type','text/javascript');return res.end(fakeTerminal)}
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(path==='/v1/box-conversations')return res.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:now,lastText:'Review ready'}]));
   if(path.endsWith('/'+b+'/desktop')||path.endsWith('/'+a+'/desktop'))return res.end(JSON.stringify({enabled:path.includes(a)?desktopA:false}));
   if(path.endsWith('/sessions'))return res.end('{"state":"live","partial":false,"sessions":[{"name":"agent"}]}');
   if(path.endsWith('/sessions/primary'))return res.end('{"session":"agent"}');
   if(path.endsWith('/sessions/interactive'))return res.end('{"session":"agent"}');
   if(path.endsWith('/messages'))return res.end(JSON.stringify([{id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Review ready',state:'delivered',createdAt:now,updatedAt:now}]));
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   if(['/v1/tool-presets','/v1/chat-commands','/v1/notifications'].includes(path))return res.end('[]');
   return res.end('{}');
  }
  const file=resolve(web,path==='/'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.html':'text/html'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat#pair=${encodeURIComponent(key)}`);
  try{await page.waitForFunction(()=>document.querySelectorAll('.pair-hero .pair-tile').length===2&&document.querySelectorAll('.pair-tile[data-mode="desktop"],.pair-tile[data-mode="tmux"]').length===2,{timeout:7000})}
  catch(error){console.log(await page.evaluate(()=>({text:document.querySelector('#chat-status')?.textContent,tiles:[...document.querySelectorAll('.pair-tile')].map(node=>({mode:node.dataset.mode,label:node.textContent})),desktop:window.fakeDesktop?.length,terminal:window.fakeTerminal?.length,body:document.body.textContent.slice(0,400)})));throw error}
  assert.deepEqual(await page.$$eval('.pair-tile',nodes=>nodes.map(node=>node.dataset.mode)),['desktop','tmux']);
  assert.deepEqual(await page.evaluate(()=>[window.fakeDesktop[0].viewOnly,window.fakeTerminal[0].viewOnly]),[true,true]);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key] .pair-avatar-mascot svg',nodes=>nodes.length),2);
  assert.equal(await page.$$eval('#chat-header-avatar .pair-avatar-mascot svg',nodes=>nodes.length),2);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key] img',nodes=>nodes.length),0);
  const geometry=await page.$$eval('.pair-tile',nodes=>nodes.map(node=>({width:node.getBoundingClientRect().width,height:node.getBoundingClientRect().height,left:node.getBoundingClientRect().left})));
  assert.ok(geometry[0].left<geometry[1].left&&Math.abs(geometry[0].width/geometry[0].height-1.6)<.05);
  await page.setViewport({width:360,height:780,isMobile:true,hasTouch:true});
  assert.ok(await page.$eval('.pair-tile:last-child',node=>node.getBoundingClientRect().right<=360),'both tiles fit a 360px phone');
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.evaluate(()=>{window.pairHeroBefore=document.querySelector('.pair-hero');window.pairMascotBefore=document.querySelector('.pair-msg .msg-avatar')});
  await page.$eval('#refresh',node=>node.click());
  await page.waitForFunction(()=>document.querySelector('.pair-hero')===window.pairHeroBefore);
  assert.equal(await page.evaluate(()=>document.querySelector('.pair-msg .msg-avatar')===window.pairMascotBefore),true,'polling reuses the live message mascot');
  assert.equal(await page.evaluate(()=>window.fakeDesktop.filter(viewer=>!viewer.closed).length+window.fakeTerminal.filter(viewer=>!viewer.closed).length),2);
  await page.$eval('.pair-tile[data-mode="tmux"]',node=>node.click());
  await page.waitForFunction(()=>!document.querySelector('#takeover').hidden);
  assert.equal(await page.$eval('#takeover-title',node=>node.textContent),'Reviewer');
  assert.equal(await page.evaluate(()=>window.fakeDesktop[0].closed&&window.fakeTerminal[0].closed),true,'takeover disconnects both previews');
  await page.click('#takeover-close');
  await page.waitForFunction(()=>document.querySelectorAll('.pair-hero .pair-tile').length===2);
  await page.evaluate(id=>{window.failDesktop=true;document.querySelector('[data-box-id="'+id+'"] .chat-meta').click()},a);
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').classList.contains('pair-view'));
  await page.$eval('[data-pair-key] .chat-meta',node=>node.click());
  await page.waitForFunction(()=>document.querySelectorAll('.pair-tile[data-mode="tmux"]').length===2);
  assert.equal(await page.evaluate(()=>window.fakeDesktop.at(-1).closed),true,'failed desktop falls back to TMUX');
  assert.equal(await page.evaluate(()=>window.fakeTerminal.filter(viewer=>!viewer.closed).length),2,'one fallback stream per tile');
  desktopA=false;
  await page.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
