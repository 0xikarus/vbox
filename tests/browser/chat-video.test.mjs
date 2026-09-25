import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,writeFile,unlink} from 'node:fs/promises';
import {join} from 'node:path';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

// A video attachment renders a real inline <video> pointed at the authenticated
// same-origin endpoint (so it can range/stream), and video files can be attached.
test('video attachments play inline and can be uploaded',async()=>{
 const now=new Date().toISOString();
 const videoBytes=Buffer.concat([Buffer.from([0x1a,0x45,0xdf,0xa3]),Buffer.alloc(64,7)]);
 const messages=[{id:'m1',direction:'agent',state:'delivered',text:'Here is the clip and a frame.',images:[{id:'v1',number:1,mediaType:'video/mp4'},{id:'i2',number:2,mediaType:'image/svg+xml'}],createdAt:now,updatedAt:now}];
 const boxes=[{id:'gallery',name:'gallery',state:'running',defaultAgent:'claude',provider:'railway',role:'owner'}];
 // A snap-packaged Chromium gets a private /tmp, so a fixture in os.tmpdir()
 // fails to upload with ERR_FILE_NOT_FOUND. Keep it in the working directory.
 const webmPath=join(process.env.VMBOX_BROWSER_UPLOAD_DIR||process.cwd(),'vmbox-clip-'+process.pid+'.webm');
 await writeFile(webmPath,videoBytes);
 let mediaHits=0;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(path==='/v1/messages/m1/images/v1'){mediaHits++;res.setHeader('Content-Type','video/mp4');return res.end(videoBytes)}
  if(path==='/v1/messages/m1/images/i2'){res.setHeader('Content-Type','image/svg+xml');return res.end('<svg xmlns="http://www.w3.org/2000/svg" width="320" height="200"><rect width="320" height="200" fill="#31516b"/></svg>')}
  if(path==='/v1/run-once-images'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({id:'up1'}))}
  if(!path.startsWith('/v1/'))return res.end('');
  if(path.endsWith('/desktop/screenshot')){res.statusCode=409;return res.end('{}')}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/gallery/messages')return res.end(JSON.stringify(messages));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.setViewport({width:420,height:900,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=gallery');
  await p.waitForSelector('.media-button',{timeout:8000});
  // The chip must draw from the message metadata alone. Buffering the clip into
  // a blob here would download a 100 MiB video on every render of the transcript
  // and, past the fetch timeout, drop the attachment from the conversation.
  assert.equal(mediaHits,0,'rendering a video attachment must not download it');
  await p.$eval('.media-button',el=>el.click());
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  const src=await p.$eval('#media-viewer-body video',el=>el.getAttribute('src'));
  assert.match(src,/^\/v1\/messages\/m1\/images\/v1$/,'video must stream from the authenticated endpoint, not a blob');
  assert.equal(await p.$eval('#media-viewer-count',el=>el.textContent),'1 / 2');
  await p.click('#media-viewer-next');
  await p.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth===320);
  assert.equal(await p.$eval('#media-viewer-count',el=>el.textContent),'2 / 2');
  await p.click('#media-viewer-prev');
  assert.equal(await p.$eval('#media-viewer-body video',el=>el.getAttribute('src')),src);
  await p.keyboard.press('Escape');
  await p.waitForFunction(()=>document.querySelector('#media-viewer').hidden,{timeout:3000});

  // the picker advertises video and a video draft can be inspected
  assert.match(await p.$eval('#attachments',el=>el.getAttribute('accept')),/video\/mp4/);
  const fileInput=await p.$('#attachments');
  await fileInput.uploadFile(webmPath);
  await p.waitForSelector('.draft-open video',{timeout:8000});
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));await unlink(webmPath).catch(()=>{})}
});
