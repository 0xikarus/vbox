import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

// Attachments and media embeds must be operable without a pointer: message
// images are buttons, embeds are labelled focusable links, and the viewer
// manages focus (opens on the close control, restores it on Escape).
test('chat media is clickable and keyboard focusable',async()=>{
 const fullImage=id=>Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400" viewBox="0 0 640 400"><rect width="640" height="400" fill="#142433"/><rect x="26" y="26" width="588" height="348" rx="16" fill="#20364d" stroke="#81add8"/><text x="50" y="82" fill="#eaf3ff" font-family="sans-serif" font-size="28" font-weight="bold">${id==='img3'?'Separate screenshot':'Build pipeline · '+(id==='img2'?'detail 2':'detail 1')}</text><path d="M205 206h42m145 0h42" stroke="#9ac5ff" stroke-width="5"/><rect x="60" y="155" width="145" height="104" rx="10" fill="#31516b"/><rect x="247" y="155" width="145" height="104" rx="10" fill="#31516b"/><rect x="434" y="155" width="145" height="104" rx="10" fill="#31516b"/><g fill="#eaf3ff" font-family="sans-serif" font-size="21" text-anchor="middle"><text x="132" y="216">Source</text><text x="319" y="216">Build</text><text x="506" y="216">Deploy</text></g></svg>`);
 const preview=Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200" viewBox="0 0 320 200"><rect width="320" height="200" fill="#142433"/><text x="18" y="32" fill="#dceaff" font-family="sans-serif" font-size="16" font-weight="bold">Build pipeline</text><path d="M96 107h25m77 0h25" stroke="#9ac5ff" stroke-width="3"/><path d="m115 101 7 6-7 6m102-12 7 6-7 6" fill="none" stroke="#9ac5ff" stroke-width="3"/><rect x="17" y="78" width="78" height="58" rx="5" fill="#31516b" stroke="#86b8f0"/><rect x="122" y="78" width="75" height="58" rx="5" fill="#31516b" stroke="#86b8f0"/><rect x="224" y="78" width="79" height="58" rx="5" fill="#31516b" stroke="#86b8f0"/><g fill="#e9f3ff" font-family="sans-serif" font-size="13" text-anchor="middle"><text x="56" y="112">Source</text><text x="159" y="112">Build</text><text x="264" y="112">Deploy</text></g><text x="18" y="174" fill="#8ba6bd" font-family="sans-serif" font-size="11">Compressed timeline preview</text></svg>`);
 const imageRequests={thumbnail:[],full:[]};
 const now=new Date().toISOString();
 const galleryMessages=[
  {id:'g1',direction:'agent',state:'delivered',text:'Here is the diagram.',images:[{id:'img1',number:1,mediaType:'image/png'},{id:'img2',number:2,mediaType:'image/png'}],createdAt:now,updatedAt:now},
  {id:'g2',direction:'agent',state:'delivered',text:'See ![pipeline](https://example.com/pipeline.png) and the clip https://example.com/demo.mp4',createdAt:now,updatedAt:now},
  {id:'g3',direction:'agent',state:'delivered',text:'A separate screenshot.',images:[{id:'img3',number:1,mediaType:'image/png'}],createdAt:now,updatedAt:now}
 ];
 const boxes=[{id:'gallery',name:'gallery',state:'running',defaultAgent:'claude',provider:'railway',role:'owner',volumeName:'v1'}];
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/messages/g1/images/img1'||path==='/v1/messages/g1/images/img2'||path==='/v1/messages/g3/images/img3'){
   const thumbnail=new URL(req.url,'http://localhost').searchParams.get('thumbnail')==='true';
   imageRequests[thumbnail?'thumbnail':'full'].push(req.url);
   await new Promise(resolve=>setTimeout(resolve,thumbnail?850:700));
   res.setHeader('Content-Type','image/svg+xml');return res.end(thumbnail?preview:fullImage(path.split('/').at(-1)));
  }
  if(path==='/v1/logical-boxes/gallery/messages')return res.end(JSON.stringify(galleryMessages));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.setViewport({width:420,height:820,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=gallery');
  await p.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
  await p.waitForSelector('.media-button',{timeout:8000});

  // an attachment is a real button with an accessible name and is tabbable
  const button=await p.$eval('.media-button',element=>({tag:element.tagName,tabIndex:element.tabIndex,label:element.getAttribute('aria-label')}));
  assert.equal(button.tag,'BUTTON');
  assert.equal(button.tabIndex,0);
  assert.match(button.label,/^Open image/);
  assert.equal(imageRequests.full.length,0,'the timeline must not download full-size images');
  assert.equal(await p.$eval('.media-preview-placeholder',element=>!element.hidden),true,'a placeholder is visible while the thumbnail loads');
  await p.screenshot({path:'/tmp/vmbox-chat-image-loading.png'});
  await p.waitForFunction(()=>document.querySelectorAll('.media-preview.ready').length===3,{timeout:5000});
  assert.equal(imageRequests.thumbnail.length,3,'the timeline loads compressed previews');
  assert.equal(imageRequests.full.length,0,'full images remain unloaded until opened');
  await p.screenshot({path:'/tmp/vmbox-chat-image-preview.png'});

  // activating it focuses the viewer's close control
  await p.$eval('.media-button',element=>element.focus());
  assert.equal(await p.evaluate(()=>document.activeElement?.classList.contains('media-button')),true);
  await p.keyboard.press('Enter');
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.evaluate(()=>document.activeElement?.id),'media-viewer-close');
  assert.equal(await p.$eval('#media-viewer-body',element=>!!element.querySelector('img')&&element.textContent.includes('Loading full image')),true,'the viewer keeps the preview visible while loading the original');
  await p.screenshot({path:'/tmp/vmbox-chat-image-viewer-loading.png'});
  await p.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=640,{timeout:3000});
  await p.waitForFunction(()=>!document.querySelector('#media-viewer-body .media-viewer-status'),{timeout:3000});
  assert.equal(imageRequests.full.length,1,'opening the viewer downloads one original');
  assert.equal(await p.$eval('#media-viewer-zoom',element=>element.hidden),false,'images have viewer-local zoom controls');
  const beforeZoom=await p.evaluate(()=>({image:document.querySelector('#media-viewer-body img').getBoundingClientRect().width,page:document.documentElement.clientWidth}));
  await p.click('#media-viewer-zoom-in');
  await p.waitForFunction(()=>document.querySelector('#media-viewer-zoom-level')?.textContent==='150%');
  const afterZoom=await p.evaluate(()=>({image:document.querySelector('#media-viewer-body img').getBoundingClientRect().width,page:document.documentElement.clientWidth}));
  assert.ok(afterZoom.image>beforeZoom.image*1.4,'only the image grew');
  assert.equal(afterZoom.page,beforeZoom.page,'the page itself did not zoom');
  await p.click('#media-viewer-zoom-reset');
  assert.equal(await p.$eval('#media-viewer-zoom-level',element=>element.textContent),'100%');
  await p.click('#media-viewer-zoom-in');

  // Arrows, keyboard, and swipe navigate only the selected message's media.
  assert.equal(await p.$eval('#media-viewer-count',element=>element.textContent),'1 / 2');
  assert.equal(await p.$eval('#media-viewer-prev',element=>element.disabled),true,'first image cannot go back');
  await p.keyboard.press('ArrowRight');
  await p.waitForFunction(()=>document.querySelector('#media-viewer-count')?.textContent==='2 / 2',{timeout:3000});
  assert.equal(await p.$eval('#media-viewer-zoom-level',element=>element.textContent),'100%','gallery navigation resets image zoom');
  assert.equal(await p.$eval('#media-viewer-next',element=>element.disabled),true,'last image cannot go forward');
  await p.click('#media-viewer-prev');
  await p.waitForFunction(()=>document.querySelector('#media-viewer-count')?.textContent==='1 / 2',{timeout:3000});

  // Escape closes it and returns focus to the button that opened it
  await p.keyboard.press('Escape');
  await p.waitForFunction(()=>document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.evaluate(()=>document.activeElement?.classList.contains('media-button')),true);
  const messages=await p.$$('#chat-messages .msg');
  await (await messages[2].$('.media-button')).click();
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.$eval('#media-viewer-count',element=>element.hidden),true,'a separate message starts its own gallery');
  assert.equal(await p.$eval('#media-viewer-next',element=>element.hidden),true,'the next message is not in this gallery');
  await p.keyboard.press('Escape');

  // media embeds are labelled focusable links
  const link=await p.$eval('a.media-link',element=>({tag:element.tagName,cls:element.className,label:element.getAttribute('aria-label'),tabIndex:element.tabIndex}));
  assert.equal(link.tag,'A');
  assert.match(link.cls,/media-link/);
  assert.match(link.label,/^Open (image|video)/);
  assert.equal(link.tabIndex,0);
  const kinds=await p.$$eval('a.media-link',nodes=>nodes.map(n=>n.classList.contains('video')?'video':n.classList.contains('image')?'image':'other'));
  assert.ok(kinds.includes('image'),'markdown image embed should be recognised');
  assert.ok(kinds.includes('video'),'media URL embed should be recognised');
  await p.setViewport({width:1200,height:800,deviceScaleFactor:1});
  await (await messages[0].$('.media-button')).click();
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  await p.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=640,{timeout:3000});
  await p.screenshot({path:'/tmp/vmbox-message-gallery-normal-desktop.png'});
  await p.keyboard.press('Escape');
  await p.evaluate(()=>document.activeElement?.blur());
  await p.screenshot({path:'/tmp/vmbox-chat-image-desktop.png'});
  await p.close();
  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await mobile.goto('http://127.0.0.1:'+server.address().port+'/chat#box=gallery');
  await mobile.waitForSelector('#chat-messages .msg .media-button');
  await mobile.$eval('#chat-messages .msg .media-button',button=>button.click());
  await mobile.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  await mobile.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=640,{timeout:8000});
  assert.equal(await mobile.$eval('#media-viewer-count',element=>element.textContent),'1 / 2');
  await mobile.click('#media-viewer-zoom-in');
  assert.equal(await mobile.$eval('#media-viewer-zoom-level',element=>element.textContent),'150%');
  assert.equal(await mobile.$eval('#media-viewer-body',element=>element.scrollWidth>element.clientWidth),true,'mobile zoom stays inside a pannable viewer');
  await mobile.click('#media-viewer-zoom-reset');
  const center=await mobile.$eval('#media-viewer-body',element=>{const rect=element.getBoundingClientRect();return{x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
  const touch=await mobile.createCDPSession();
  await touch.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:center.x-25,y:center.y,id:1},{x:center.x+25,y:center.y,id:2}]});
  await touch.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:center.x-60,y:center.y,id:1},{x:center.x+60,y:center.y,id:2}]});
  await touch.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  await mobile.waitForFunction(()=>parseInt(document.querySelector('#media-viewer-zoom-level')?.textContent||'0',10)>100,{timeout:3000});
  assert.equal(await mobile.evaluate(()=>visualViewport.scale),1,'pinching the image does not zoom the page');
  const scrollBefore=await mobile.$eval('#media-viewer-body',element=>element.scrollLeft);
  await touch.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x:center.x,y:center.y,id:3}]});
  await touch.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:center.x-45,y:center.y,id:3}]});
  await touch.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  assert.ok(await mobile.$eval('#media-viewer-body',element=>element.scrollLeft)>scrollBefore,'a zoomed image pans inside the viewer');
  assert.equal(await mobile.$eval('#media-viewer-count',element=>element.textContent),'1 / 2','panning does not change the selected attachment');
  await mobile.screenshot({path:'/tmp/vmbox-message-gallery-normal-mobile.png'});
  await mobile.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
