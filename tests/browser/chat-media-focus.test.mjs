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
 const png=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const now=new Date().toISOString();
 const galleryMessages=[
  {id:'g1',direction:'agent',state:'delivered',text:'Here is the diagram.',images:[{id:'img1',number:1,mediaType:'image/png'}],createdAt:now,updatedAt:now},
  {id:'g2',direction:'agent',state:'delivered',text:'See ![pipeline](https://example.com/pipeline.png) and the clip https://example.com/demo.mp4',createdAt:now,updatedAt:now}
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
  if(path==='/v1/messages/g1/images/img1'){res.setHeader('Content-Type','image/png');return res.end(png)}
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

  // activating it focuses the viewer's close control
  await p.$eval('.media-button',element=>element.focus());
  assert.equal(await p.evaluate(()=>document.activeElement?.classList.contains('media-button')),true);
  await p.keyboard.press('Enter');
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.evaluate(()=>document.activeElement?.id),'media-viewer-close');
  await p.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=1,{timeout:3000});

  // Escape closes it and returns focus to the button that opened it
  await p.keyboard.press('Escape');
  await p.waitForFunction(()=>document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.evaluate(()=>document.activeElement?.classList.contains('media-button')),true);

  // media embeds are labelled focusable links
  const link=await p.$eval('a.media-link',element=>({tag:element.tagName,cls:element.className,label:element.getAttribute('aria-label'),tabIndex:element.tabIndex}));
  assert.equal(link.tag,'A');
  assert.match(link.cls,/media-link/);
  assert.match(link.label,/^Open (image|video)/);
  assert.equal(link.tabIndex,0);
  const kinds=await p.$$eval('a.media-link',nodes=>nodes.map(n=>n.classList.contains('video')?'video':n.classList.contains('image')?'image':'other'));
  assert.ok(kinds.includes('image'),'markdown image embed should be recognised');
  assert.ok(kinds.includes('video'),'media URL embed should be recognised');
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});