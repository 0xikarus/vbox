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
const mockTerminalSocket=`window.terminalSockets=[];window.WebSocket=class{
 static OPEN=1;
 constructor(url){this.url=url;this.frames=[];this.readyState=0;this.bufferedAmount=0;window.terminalSockets.push(this);setTimeout(()=>{if(this.readyState===0){this.readyState=1;this.onopen?.()}},20)}
 send(data){this.frames.push(JSON.parse(data))}
 close(){this.readyState=3}
};window.fakeTerminal=[];`;
const trackTerminal=`const realOpenWorkspaceTerminal=window.openWorkspaceTerminal;
window.openWorkspaceTerminal=(box,session,onStatus,options={})=>{
 const viewer={box,session,viewOnly:options.viewOnly,closed:false};window.fakeTerminal.push(viewer);
 const dispose=realOpenWorkspaceTerminal(box,session,onStatus,options);
 window.terminalSockets.at(-1).viewOnly=options.viewOnly===true;
 return()=>{viewer.closed=true;dispose()};
};`;

test('pair hero stays pinned above a long transcript while it scrolls',async()=>{
 const desktopA=true;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(await readFile(resolve(web,'chat.html')))}
  if(path==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(fakeDesktop)}
  if(path==='/workspace-terminal.js'){res.setHeader('Content-Type','text/javascript');return res.end(mockTerminalSocket+await readFile(resolve(web,'workspace-terminal.js'),'utf8')+trackTerminal)}
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
   if(path.endsWith('/messages'))return res.end(JSON.stringify(Array.from({length:60},(_,i)=>({id:'m'+i,senderBoxId:i%2?b:a,recipientBoxId:i%2?a:b,direction:'box',text:'Message '+i+' with enough text to wrap onto a second line on a phone',state:'delivered',createdAt:now,updatedAt:now}))));
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
  for(const viewport of [{width:390,height:844,isMobile:true,hasTouch:true},{width:1440,height:900}]){
   const page=await browser.newPage();await page.setViewport(viewport);
   await page.goto(`http://127.0.0.1:${server.address().port}/chat#pair=${encodeURIComponent(key)}`);
   await page.waitForFunction(()=>document.querySelectorAll('.pair-hero .pair-tile').length===2&&document.querySelectorAll('#chat-messages .pair-msg').length===60);
   for(const where of ['bottom','middle']){
    await page.evaluate(where=>{const list=document.querySelector('#chat-messages');list.scrollTop=where==='bottom'?list.scrollHeight:list.scrollHeight/2},where);
    await new Promise(done=>setTimeout(done,150));
    const state=await page.evaluate(()=>{
     const list=document.querySelector('#chat-messages').getBoundingClientRect(),hero=document.querySelector('.pair-hero').getBoundingClientRect(),tile=document.querySelector('.pair-tile').getBoundingClientRect();
     const hit=document.elementFromPoint(tile.left+tile.width/2,tile.top+tile.height/2);
     const strip=document.elementFromPoint(list.left+4,hero.top+hero.height/2);
     const topHit=document.elementFromPoint(list.left+list.width/2,list.top+2);const box=document.querySelector('#chat-messages');return {noSideScroll:box.scrollWidth<=box.clientWidth+1&&document.documentElement.scrollWidth<=innerWidth+1,topCovered:!!topHit?.closest('.pair-hero'),scrolled:document.querySelector('#chat-messages').scrollTop>0,heroTop:hero.top-list.top,heroBottom:hero.bottom-list.top,tileHit:!!hit?.closest('.pair-tile'),stripCovered:!strip?.closest('.pair-msg')};
    });
    assert.ok(state.scrolled,'transcript is scrolled ('+where+')');
    assert.ok(state.heroTop>=-1&&state.heroTop<=13&&state.heroBottom>100,'tiles stay pinned at the top of the transcript ('+viewport.width+'px, '+where+'): '+JSON.stringify(state));
    assert.ok(state.tileHit,'messages do not paint over the tiles ('+viewport.width+'px, '+where+')');
    assert.ok(state.noSideScroll,'the cover strip adds no horizontal scroll ('+viewport.width+'px)');
    assert.ok(state.topCovered,'no message shows in a gap above the tiles ('+viewport.width+'px, '+where+')');
    assert.ok(state.stripCovered,'messages scroll under the hero strip, not beside it ('+viewport.width+'px, '+where+')');
   }
   await page.close();
  }
 }finally{await browser.close();server.close()}
});
