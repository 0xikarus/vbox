import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const motionJS=await readFile('internal/controller/web/motion.js','utf8');
const mascotJS=await readFile('internal/controller/web/mascot.js','utf8');
const mascotCSS=await readFile('internal/controller/web/mascot.css','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const vboxCSS=await readFile('internal/controller/web/vbox-c.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');
const screenshotDir=process.env.VMBOX_CHAT_SCREENSHOTS||'docs/chat-ui/screenshots';
if(process.env.VMBOX_CHAT_SCREENSHOTS)await mkdir(screenshotDir,{recursive:true});

const thumbnail=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');

// Touch has no right-click and no hover. Holding a chat row must open the row
// context menu, a tap on the processing bubble must open the TV preview instead
// of the OS "save image" callout, swiping in from the edge pulls the list back,
// and the details drawer must fit a phone.
test('mobile gestures: long-press menu, tap preview, swipe list, fitting details',async()=>{
 const now=new Date(Date.now()-11*60*1000).toISOString();
 let busy=true;
 const boxes=[{id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'railway',role:'worker',volumeName:'v1'},{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex',provider:'railway',role:'worker',volumeName:'v2'}];
 const messages=[{id:'u1',direction:'user',state:'delivered',text:'Please work on this.',createdAt:now,updatedAt:now}];
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/motion.js'){res.setHeader('Content-Type','text/javascript');return res.end(motionJS)}
  if(path==='/mascot.js'){res.setHeader('Content-Type','text/javascript');return res.end(mascotJS)}
  if(path==='/mascot.css'){res.setHeader('Content-Type','text/css');return res.end(mascotCSS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/vbox-c.css'){res.setHeader('Content-Type','text/css');return res.end(vboxCSS)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  if(path==='/v1/whoami'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({role:'owner'}))}
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify(boxes))}
  if(path==='/v1/tool-presets'){res.setHeader('Content-Type','application/json');return res.end('[]')}
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path.endsWith('/desktop/screenshot')){res.setHeader('Content-Type','image/png');return res.end(thumbnail)}
  if(path.endsWith('/desktop/replay')){res.setHeader('Content-Type','application/json');return res.end('[]')}
  if(path==='/v1/logical-boxes/builder/messages'){
   res.setHeader('Content-Type','application/json');res.setHeader('X-Vmbox-Agent-Busy',String(busy));res.setHeader('X-Vmbox-Agent-Busy-Since',now);
   return res.end(JSON.stringify(messages));
  }
  if(path==='/v1/logical-boxes/builder/contacts'){res.setHeader('Content-Type','application/json');return res.end('[]')}
  if(path==='/v1/logical-boxes/builder/protection'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({protected:false}))}
  if(path.endsWith('/messages')){res.setHeader('Content-Type','application/json');return res.end('[]')}
  res.setHeader('Content-Type','application/json');return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.evaluateOnNewDocument(()=>{
   window.openWorkspaceDesktop=(id,_onStatus,options)=>{
    if(options?.viewOnly){window.viewerPreview={id,viewOnly:true};return()=>{window.viewerPreviewClosed=true}}
    window.viewerDesktop={id};return()=>{};
   };
   window.openWorkspaceTerminal=()=>()=>{};
  });
  await p.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await p.waitForFunction(()=>{const el=document.querySelector('.msg.processing .tv-button');if(!el)return false;const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth});
  await new Promise(resolve=>setTimeout(resolve,450)); // let the slide-in transition settle before tapping

  // Tap the processing bubble's TV button: the preview opens, and long-press on
  // the preview is ours (no native save-image).
  const tvPoint=await p.$eval('.msg.processing .tv-button',el=>{const r=el.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}});
  await p.touchscreen.tap(tvPoint.x,tvPoint.y);
  await p.waitForFunction(()=>!document.querySelector('.tv-preview').hidden,{timeout:5000});
  await p.screenshot({path:screenshotDir+'/mobile-chat-tv-preview.png'});
  assert.equal(await p.evaluate(()=>{const el=document.querySelector('.tv-preview');const e=new MouseEvent('contextmenu',{bubbles:true,cancelable:true});el.dispatchEvent(e);return e.defaultPrevented}),true,'long-press on the preview must not offer save-image');
  await p.touchscreen.tap(6,240);
  await p.waitForFunction(()=>document.querySelector('.tv-preview').hidden);
  busy=false;
  await p.click('#refresh');
  await p.waitForFunction(()=>!document.querySelector('.msg.processing'),{timeout:5000});

  // The message chevron lives in the bottom metadata row.
  assert.equal(await p.$eval('.msg.user .msg-more',el=>!!el.closest('.msg-actions')),true);
  const morePoint=await p.$eval('.msg.user .msg-more',el=>{const r=el.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}});
  await p.touchscreen.tap(morePoint.x,morePoint.y);
  await p.waitForFunction(()=>!document.querySelector('.msg-actions-menu').hidden);
  assert.equal(await p.$eval('.msg-actions-menu',el=>['Copy','Forward…'].every(label=>el.textContent.includes(label))),true,'message actions expose Copy and Forward');
  await p.screenshot({path:screenshotDir+'/mobile-chat-message-actions.png'});
  await p.touchscreen.tap(6,300);
  await p.waitForFunction(()=>document.querySelector('.msg-actions-menu').hidden);

  // The details drawer must fit one column on a phone.
  await p.click('#chat-info');
  await p.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await p.waitForFunction(()=>!!document.querySelector('#inspect-contacts'));
  await p.evaluate(()=>{document.activeElement?.blur()});
  await p.screenshot({path:screenshotDir+'/mobile-chat-details.png'});
  const fit=await p.evaluate(()=>{
   const panel=document.querySelector('#inspect'),contacts=document.querySelector('#inspect-contacts');
   return {overflowX:document.documentElement.scrollWidth-document.documentElement.clientWidth,contactsWidth:contacts.getBoundingClientRect().width,panelWidth:panel.getBoundingClientRect().width};
  });
  assert.ok(fit.contactsWidth>fit.panelWidth*0.85,'contacts must span the mobile drawer, not sit in a half column');
  assert.ok(fit.overflowX<=1,'mobile details must not overflow horizontally');
  await p.$eval('#inspect-close',el=>el.click());

  // Swipe in from the left edge to pull the chat list back out.
  assert.equal(await p.evaluate(()=>document.querySelector('#chat-app').classList.contains('in-chat')),true);
  await p.touchscreen.touchStart(12,420);
  await new Promise(r=>setTimeout(r,60));
  await p.touchscreen.touchMove(80,424);
  await new Promise(r=>setTimeout(r,60));
  await p.touchscreen.touchMove(150,428);
  await p.touchscreen.touchEnd();
  await new Promise(r=>setTimeout(r,300));
  await p.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat')===false);
  await p.screenshot({path:screenshotDir+'/mobile-chat-swipe-list.png'});

  // Long-press a row to open the context menu.
  const rowPoint=await p.$eval('[data-box-id="reviewer"]',el=>{const r=el.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}});
  await p.touchscreen.touchStart(rowPoint.x,rowPoint.y);
  await new Promise(resolve=>setTimeout(resolve,650));
  await p.touchscreen.touchEnd();
  await p.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await p.screenshot({path:screenshotDir+'/mobile-chat-row-menu.png'});
  assert.equal(await p.$eval('#row-menu',el=>el.textContent.includes('Show details')),true,'long-press opens the row context menu');
  const mobileMenu=await p.$eval('#row-menu',el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom}});
  assert.ok(Math.abs(mobileMenu.left-rowPoint.x)<=10 && Math.abs(mobileMenu.top-rowPoint.y)<=10,'mobile menu starts at the hold point');

  // Near the screen edges, place the menu above and to the left of the point.
  const edgePoint=await p.evaluate(()=>({x:innerWidth-12,y:innerHeight-12}));
  await p.evaluate(({x,y})=>{document.querySelector('[data-box-id="reviewer"]').dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,clientX:x,clientY:y}))},edgePoint);
  const edgeMenu=await p.$eval('#row-menu',el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom}});
  assert.ok(Math.abs(edgeMenu.right-edgePoint.x)<=10 && Math.abs(edgeMenu.bottom-edgePoint.y)<=10,'menu flips at the screen edge');
  assert.ok(edgeMenu.left>=0&&edgeMenu.top>=0,'menu stays in the viewport');

  await p.close();
  const desktop=await browser.newPage();await desktop.setViewport({width:1280,height:800});
  await desktop.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await desktop.waitForSelector('[data-box-id="reviewer"]');
  const desktopPoint=await desktop.$eval('[data-box-id="reviewer"]',el=>{const r=el.getBoundingClientRect();return {x:r.left+80,y:r.top+25}});
  await desktop.mouse.click(desktopPoint.x,desktopPoint.y,{button:'right'});
  await desktop.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  const desktopMenu=await desktop.$eval('#row-menu',el=>{const r=el.getBoundingClientRect();return {left:r.left,top:r.top}});
  assert.ok(Math.abs(desktopMenu.left-desktopPoint.x)<=10&&Math.abs(desktopMenu.top-desktopPoint.y)<=10,'desktop menu starts at the right-click point');
  await desktop.screenshot({path:screenshotDir+'/desktop-chat-row-menu.png'});
  await desktop.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
