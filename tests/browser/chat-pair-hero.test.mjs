import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
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

test('pair hero keeps two view-only tiles, falls back to TMUX, and opens the selected box',async()=>{
 let desktopA=true;
 let activityMood='angry';
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(await readFile(resolve(web,'chat.html')))}
  if(path==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(fakeDesktop)}
  if(path==='/workspace-terminal.js'){res.setHeader('Content-Type','text/javascript');return res.end(mockTerminalSocket+await readFile(resolve(web,'workspace-terminal.js'),'utf8')+trackTerminal)}
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/box-activity')return res.end(JSON.stringify([{boxId:a,mood:activityMood,activity:'working',observedAt:new Date().toISOString()}]));
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify(boxes));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(path==='/v1/box-conversations')return res.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:now,lastText:'Review ready'}]));
   if(path.endsWith('/'+b+'/desktop')||path.endsWith('/'+a+'/desktop'))return res.end(JSON.stringify({enabled:path.includes(a)?desktopA:false}));
   if(path.endsWith('/sessions'))return res.end('{"state":"live","partial":false,"sessions":[{"name":"agent"}]}');
   if(path.endsWith('/sessions/primary'))return res.end('{"session":"agent"}');
   if(path.endsWith('/sessions/interactive'))return res.end('{"session":"agent"}');
   if(path.endsWith('/messages'))return res.end(JSON.stringify([
    {id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Review ready',state:'delivered',createdAt:now,updatedAt:now},
    {id:'m2',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Checking now',state:'delivered',createdAt:now,updatedAt:now},
    {id:'m3',senderBoxId:b,recipientBoxId:a,direction:'box',text:'Looks good',state:'delivered',createdAt:now,updatedAt:now}
   ]));
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
  await page.waitForFunction(()=>window.terminalSockets.length===1&&window.terminalSockets[0].readyState===1);
  const pairMood=()=>page.evaluate(id=>{
   const pose=selector=>document.querySelector(selector)?.dataset.pose||'';
   return [pose('#chat-entries [data-pair-mascot-box="'+id+'"]'),pose('#chat-header-avatar [data-pair-mascot-box="'+id+'"]'),pose('[data-pair-tile-box="'+id+'"] .pair-hero-mascot')];
  },a);
  await page.waitForFunction(id=>['#chat-entries [data-pair-mascot-box="'+id+'"]','#chat-header-avatar [data-pair-mascot-box="'+id+'"]','[data-pair-tile-box="'+id+'"] .pair-hero-mascot'].every(selector=>document.querySelector(selector)?.dataset.pose?.includes('angry')),{},a);
  assert.equal((await pairMood()).every(pose=>pose.includes('angry')),true,'batch mood reaches list, header, and hero mascots');
  activityMood='happy';
  await page.waitForFunction(id=>['#chat-entries [data-pair-mascot-box="'+id+'"]','#chat-header-avatar [data-pair-mascot-box="'+id+'"]','[data-pair-tile-box="'+id+'"] .pair-hero-mascot'].every(selector=>document.querySelector(selector)?.dataset.pose?.includes('happy')),{timeout:6500},a);
  assert.equal((await pairMood()).every(pose=>pose.includes('happy')),true,'the next batch poll updates all three pair mascots');
  assert.match(await page.$eval('[data-pair-tile-box="'+a+'"] .pair-tile-mascot',node=>node.getAttribute('aria-label')),/^Happy · mood updated recently$/);
  const sizeFrames=()=>page.evaluate(()=>window.terminalSockets.flatMap(socket=>socket.frames).filter(frame=>'cols'in frame||'rows'in frame));
  assert.deepEqual(await sizeFrames(),[],'pair tile sends no terminal size on connect');
  const terminalCrop=await page.$eval('.pair-tile[data-mode="tmux"] .pair-tile-screen',screen=>{
   const outer=screen.getBoundingClientRect(),inner=screen.querySelector('.xterm').getBoundingClientRect(),last=screen.querySelector('.xterm-rows').lastElementChild.getBoundingClientRect();
   return {overflow:getComputedStyle(screen).overflow,width:inner.width,visibleWidth:outer.width,height:inner.height,left:inner.left-outer.left,bottom:outer.bottom-inner.bottom,rowHeight:last.height,lastRowBottom:last.bottom-outer.bottom};
  });
  assert.equal(terminalCrop.overflow,'hidden');
  assert.ok(terminalCrop.width>terminalCrop.visibleWidth&&terminalCrop.height===384&&Math.abs(terminalCrop.left-8)<1&&Math.abs(terminalCrop.bottom-8)<1&&terminalCrop.rowHeight===16&&terminalCrop.lastRowBottom<=0,'80×24 terminal has 16px rows and is cropped from the bottom-left');
  await page.setViewport({width:1440,height:900});
  await new Promise(resolve=>setTimeout(resolve,100));
  assert.deepEqual(await sizeFrames(),[],'pair tile sends no terminal size after viewport resize');
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.waitForFunction(()=>document.querySelectorAll('#chat-entries [data-pair-key] .pair-avatar-mascot svg').length===2);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key] .pair-avatar-mascot svg',nodes=>nodes.length),2);
  assert.equal(await page.$$eval('#chat-header-avatar .pair-avatar-mascot svg',nodes=>nodes.length),2);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key] img',nodes=>nodes.length),0);
  const geometry=await page.$$eval('.pair-tile',nodes=>nodes.map(node=>({width:node.getBoundingClientRect().width,height:node.getBoundingClientRect().height,left:node.getBoundingClientRect().left})));
  assert.ok(geometry[0].left<geometry[1].left&&Math.abs(geometry[0].width/geometry[0].height-1.6)<.05);
  const layout=await page.evaluate(()=>{
   const rect=node=>node.getBoundingClientRect();
   const [tile]=document.querySelectorAll('.pair-tile');
   const [first,second,third]=document.querySelectorAll('.pair-msg');
   const day=document.querySelector('.pair-hero+.day-sep');
   return {screenBottom:rect(tile.querySelector('.pair-tile-screen')).bottom,captionTop:rect(tile.querySelector('.pair-tile-label')).top,
    nameRight:rect(tile.querySelector('.pair-tile-name')).right,mascotLeft:rect(tile.querySelector('.pair-tile-mascot')).left,
    dayAbove:rect(day).top-rect(document.querySelector('.pair-hero')).bottom,dayBelow:rect(first).top-rect(day).bottom,
    dayTotal:rect(first).top-rect(document.querySelector('.pair-hero')).bottom,
    sameGap:rect(second).top-rect(first).bottom,senderGap:rect(third).top-rect(second).bottom,
    avatarBackground:getComputedStyle(document.querySelector('[data-pair-key] .pair-avatar-mascot')).backgroundColor};
  });
  assert.ok(layout.screenBottom<=layout.captionTop+1&&layout.nameRight<=layout.mascotLeft,'caption is below the screen and mascot follows its label');
  assert.ok(layout.dayAbove>=15&&layout.dayAbove<=17&&layout.dayBelow>=15&&layout.dayBelow<=17,'today label has 16px breathing room');
  assert.ok(layout.dayTotal>=43&&layout.dayTotal<=45,'hero to first bubble is about 44px including the day label');
  assert.ok(layout.sameGap>=3&&layout.sameGap<=5&&layout.senderGap>=12&&layout.senderGap<=14,'messages group at 4px and separate senders at 12–14px');
  assert.equal(layout.avatarBackground,'rgba(0, 0, 0, 0)','pair mascots have no backing disc');
  await page.setViewport({width:360,height:780,isMobile:true,hasTouch:true});
  assert.ok(await page.$eval('.pair-tile:last-child',node=>node.getBoundingClientRect().right<=360),'both tiles fit a 360px phone');
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const listWidth=()=>page.evaluate(()=>{
   const viewport=innerWidth,list=document.querySelector('#chat-list').getBoundingClientRect(),main=document.querySelector('#chat-main').getBoundingClientRect();
   return {viewport,left:list.left,right:list.right,width:list.width,mainLeft:main.left};
  });
  await page.click('#chat-back');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await new Promise(resolve=>setTimeout(resolve,280));
  let list=await listWidth();
  assert.ok(Math.abs(list.left)<1&&Math.abs(list.right-list.viewport)<1&&Math.abs(list.width-list.viewport)<1&&list.mainLeft>=list.viewport-1,'back tap restores full-width list without a peeking pane');
  await page.$eval('[data-pair-key] .chat-meta',node=>node.click());
  await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat')&&document.querySelector('.pair-hero')&&document.querySelector('.pair-msg'));
  await new Promise(resolve=>setTimeout(resolve,280));
  const client=await page.createCDPSession();
  const touch=(type,x,y)=>client.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd'?[]:[{x,y}]});
  await touch('touchStart',25,450);await new Promise(resolve=>setTimeout(resolve,40));
  await touch('touchMove',100,450);await new Promise(resolve=>setTimeout(resolve,40));
  await touch('touchMove',260,450);await new Promise(resolve=>setTimeout(resolve,40));await touch('touchEnd',260,450);
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await new Promise(resolve=>setTimeout(resolve,280));
  list=await listWidth();
  assert.ok(Math.abs(list.left)<1&&Math.abs(list.right-list.viewport)<1&&Math.abs(list.width-list.viewport)<1&&list.mainLeft>=list.viewport-1,'back swipe restores full-width list without a peeking pane');
  await page.$eval('[data-pair-key] .chat-meta',node=>node.click());
  await page.waitForFunction(()=>document.querySelector('.pair-hero')!==null);
  await page.evaluate(()=>{window.pairHeroBefore=document.querySelector('.pair-hero');window.pairMascotBefore=document.querySelector('.pair-msg .msg-avatar')});
  await page.$eval('#refresh',node=>node.click());
  await page.waitForFunction(()=>document.querySelector('.pair-hero')===window.pairHeroBefore);
  assert.equal(await page.evaluate(()=>document.querySelector('.pair-msg .msg-avatar')===window.pairMascotBefore),true,'polling reuses the live message mascot');
  assert.equal(await page.evaluate(()=>window.fakeDesktop.filter(viewer=>!viewer.closed).length+window.fakeTerminal.filter(viewer=>!viewer.closed).length),2);
  assert.equal(await page.$$eval('.pair-tile-mascot',nodes=>nodes.every(node=>!!node.getAttribute('aria-label'))),true);
  await page.click('.pair-tile-mascot');
  await page.waitForSelector('#mascot-mood-tooltip:not([hidden])');
  assert.equal(await page.$eval('#takeover',node=>node.hidden),true,'tapping a tile mascot shows its tooltip without opening takeover');
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
  await page.evaluate(()=>{
   const root=document.createElement('div'),keys=document.createElement('div');
   root.style.width='70vw';root.style.height='200px';document.body.append(root,keys);
   window.workspaceTestRoot=root;window.closeWorkspaceTestTerminal=openWorkspaceTerminal('workspace','agent',()=>{},{root,keys});
  });
  await page.waitForFunction(()=>window.terminalSockets.at(-1).frames.some(frame=>'cols'in frame&&'rows'in frame));
  const workspaceSocket=await page.evaluate(()=>window.terminalSockets.length-1);
  const beforeResize=await page.evaluate(index=>window.terminalSockets[index].frames.filter(frame=>'cols'in frame&&'rows'in frame).at(-1),workspaceSocket);
  await page.evaluate(()=>{window.workspaceTestRoot.style.width='40vw'});
  await page.waitForFunction((index,cols)=>window.terminalSockets[index].frames.some(frame=>'cols'in frame&&frame.cols!==cols),{},workspaceSocket,beforeResize.cols);
  assert.deepEqual(await page.evaluate(()=>window.terminalSockets.filter(socket=>socket.viewOnly).flatMap(socket=>socket.frames).filter(frame=>'cols'in frame||'rows'in frame)),[],'view-only tiles never send resize while Workspace does');
  await page.evaluate(()=>window.closeWorkspaceTestTerminal());
  if(process.env.VMBOX_CAPTURE_DIR){
   await page.evaluate(()=>window.workspaceTestRoot.remove());
   await mkdir(process.env.VMBOX_CAPTURE_DIR,{recursive:true});
   for(const width of [390,1440])for(const theme of ['light','dark']){
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    if(width===390)await page.$eval('.pair-tile-mascot',node=>node.click());
    else await page.hover('.pair-tile-mascot');
    await page.waitForSelector('#mascot-mood-tooltip:not([hidden])');
    await page.screenshot({path:`${process.env.VMBOX_CAPTURE_DIR}/pair-hero-${width}-${theme}.png`});
   }
  }
  desktopA=false;
  await page.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
