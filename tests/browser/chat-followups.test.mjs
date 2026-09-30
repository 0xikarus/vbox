import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,writeFile,unlink} from 'node:fs/promises';
import {join} from 'node:path';
import zlib from 'node:zlib';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const motionJS=await readFile('internal/controller/web/motion.js','utf8');
const mascotJS=await readFile('internal/controller/web/mascot.js','utf8');
const mascotCSS=await readFile('internal/controller/web/mascot.css','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const tokensCSS=await readFile('internal/controller/web/vbox-tokens.css','utf8');
const vboxCSS=await readFile('internal/controller/web/vbox-c.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

function tinyPNG(){
 const w=8,h=8,stride=w*3+1,raw=Buffer.alloc(stride*h);
 for(let y=0;y<h;y++){raw[y*stride]=0;for(let x=0;x<w;x++){raw[y*stride+1+x*3]=120;raw[y*stride+2+x*3]=160;raw[y*stride+3+x*3]=90}}
 const crc=buf=>{let c=~0;for(const b of buf){c^=b;for(let k=0;k<8;k++)c=(c>>>1)^(0xedb88320&-(c&1));}return (~c)>>>0};
 const chunk=(t,d)=>{const l=Buffer.alloc(4);l.writeUInt32BE(d.length);const ty=Buffer.from(t);const c=Buffer.alloc(4);c.writeUInt32BE(crc(Buffer.concat([ty,d])));return Buffer.concat([l,ty,d,c])};
 const ihdr=Buffer.alloc(13);ihdr.writeUInt32BE(w,0);ihdr.writeUInt32BE(h,4);ihdr[8]=8;ihdr[9]=2;
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',ihdr),chunk('IDAT',zlib.deflateSync(raw)),chunk('IEND',Buffer.alloc(0))]);
}

test('composer drafts persist, attachments inspect, and the shell follows dark preference',async()=>{
 const now=new Date().toISOString();
 const boxes=[{id:'alpha',name:'alpha',state:'running',defaultAgent:'claude',provider:'railway',role:'owner'},{id:'beta',name:'beta',state:'running',defaultAgent:'claude',provider:'railway',role:'worker'}];
 const msgs=id=>[{id:id+'1',direction:'agent',state:'delivered',text:id.toUpperCase()+'-ONLY',createdAt:now,updatedAt:now}];
 // A snap-packaged Chromium gets a private /tmp, so a fixture in os.tmpdir()
 // fails to upload with ERR_FILE_NOT_FOUND. Keep it in the working directory.
 const pngPath=join(process.env.VMBOX_BROWSER_UPLOAD_DIR||process.cwd(),'vmbox-draft-'+process.pid+'.png');
 await writeFile(pngPath,tinyPNG());
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/motion.js'){res.setHeader('Content-Type','text/javascript');return res.end(motionJS)}
  if(path==='/mascot.js'){res.setHeader('Content-Type','text/javascript');return res.end(mascotJS)}
  if(path==='/mascot.css'){res.setHeader('Content-Type','text/css');return res.end(mascotCSS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/vbox-tokens.css'){res.setHeader('Content-Type','text/css');return res.end(tokensCSS)}
  if(path==='/vbox-c.css'){res.setHeader('Content-Type','text/css');return res.end(vboxCSS)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(path==='/v1/run-once-images'){await new Promise(resolve=>setTimeout(resolve,250));res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({id:'img-1'}))}
  if(!path.startsWith('/v1/'))return res.end('');
  if(path.endsWith('/desktop/screenshot')){res.statusCode=409;return res.end('{}')}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/alpha/messages')return res.end(JSON.stringify(msgs('alpha')));
  if(path==='/v1/logical-boxes/beta/messages')return res.end(JSON.stringify(msgs('beta')));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  await p.emulateMediaFeatures([{name:'prefers-color-scheme',value:'dark'}]);
  await p.setViewport({width:420,height:900,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=alpha');
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('ALPHA-ONLY'),{timeout:8000});

  // the top mascots are gone
  assert.equal(await p.$('#list-mascot'),null,'list header mascot removed');
  assert.equal(await p.$('#chat-companion'),null,'conversation header mascot removed');

  // add-box lives in the list header row and never overlaps the composer's send
  assert.equal(await p.$eval('#new-box',el=>!!el.closest('#chat-sidebar-head')),true);
  const overlap=await p.evaluate(()=>{const a=document.querySelector('#new-box').getBoundingClientRect(),b=document.querySelector('#send').getBoundingClientRect();return !(a.right<b.left||a.left>b.right||a.bottom<b.top||a.top>b.bottom)});
  assert.equal(overlap,false,'the add-box button must not overlap send');

  // Quiet workspace shell
  assert.ok(await p.$eval('#chat-shell',el=>getComputedStyle(el).backgroundColor.match(/\d+/g).slice(0,3).every(channel=>Number(channel)<40)),'dark preference gives the shell a dark surface');

  // typing persists per box and across a reload
  await p.type('#chat-input','draft for alpha');
  await new Promise(r=>setTimeout(r,400));
  await p.$eval('[data-box-id="beta"]',el=>el.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('BETA-ONLY'),{timeout:8000});
  assert.equal(await p.$eval('#chat-input',el=>el.value),'','switching boxes shows the new box draft, not the old one');
  await p.$eval('[data-box-id="alpha"]',el=>el.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('ALPHA-ONLY'),{timeout:8000});
  assert.equal(await p.$eval('#chat-input',el=>el.value),'draft for alpha','the box draft is restored on return');
  await p.reload({waitUntil:'domcontentloaded'});
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('ALPHA-ONLY'),{timeout:8000});
  assert.equal(await p.$eval('#chat-input',el=>el.value),'draft for alpha','the box draft survives a reload');

  // Attachment uploads stay with the box where they started, even when the
  // user changes active chats before the upload finishes.
  const fileInput=await p.$('#attachments');
  await fileInput.uploadFile(pngPath);
  await p.$eval('[data-box-id="beta"]',el=>el.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('BETA-ONLY'),{timeout:8000});
  await new Promise(resolve=>setTimeout(resolve,350));
  assert.equal(await p.$$eval('.draft-open',elements=>elements.length),0,'alpha attachment must not appear in beta');
  await p.$eval('[data-box-id="alpha"]',el=>el.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('ALPHA-ONLY'),{timeout:8000});
  assert.equal(await p.$$eval('.draft-open',elements=>elements.length),1,'alpha attachment must be restored with alpha');

  // an attached image can be inspected before sending
  await p.waitForSelector('.draft-open',{timeout:8000});
  await p.$eval('.draft-open',el=>el.click());
  await p.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  assert.equal(await p.$eval('#media-viewer-body img',el=>el.naturalWidth>=1),true);
  assert.equal(await p.$eval('#media-viewer-zoom',el=>el.hidden),false,'unsent attachments can be zoomed too');
  await p.click('#media-viewer-zoom-in');
  assert.equal(await p.$eval('#media-viewer-zoom-level',el=>el.textContent),'150%');
  await p.keyboard.press('Escape');
  await p.waitForFunction(()=>document.querySelector('#media-viewer').hidden,{timeout:3000});
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r));await unlink(pngPath).catch(()=>{})}
});
