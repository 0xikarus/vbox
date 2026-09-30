import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const now=new Date().toISOString();
const message={id:'source-message',direction:'agent',state:'delivered',text:'Please review this diagram.',images:[{id:'diagram',number:1,mediaType:'image/png'}],createdAt:now,updatedAt:now};
const diagram=Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400"><rect width="640" height="400" fill="white"/><rect x="40" y="40" width="140" height="80" fill="#224466"/></svg>');

test('annotated chat image becomes an attachment with text and parent reply',async()=>{
 let uploaded=null,sent=null;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(path==='/v1/messages/source-message/images/diagram'){res.setHeader('Content-Type','image/svg+xml');return res.end(diagram)}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([{id:'builder',name:'Builder',state:'running',defaultAgent:'claude'}]));
  if(path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/chat-commands')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/run-once-images'&&req.method==='POST'){
   const chunks=[];for await(const chunk of req)chunks.push(chunk);uploaded=Buffer.concat(chunks);
   return res.end(JSON.stringify({id:'annotated-image'}));
  }
  if(path==='/v1/logical-boxes/builder/messages'){
   if(req.method==='POST'){
    const chunks=[];for await(const chunk of req)chunks.push(chunk);sent=JSON.parse(Buffer.concat(chunks).toString());
    return res.end(JSON.stringify({message:{state:'delivered'}}));
   }
   return res.end(JSON.stringify([message]));
  }
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1100,height:800});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForSelector('#chat-messages .media-button');
  await page.click('#chat-messages .media-button');
  await page.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth===640);
  await page.click('#media-annotate');
  await page.waitForSelector('#image-annotation[open]');
  assert.equal(await page.$eval('#image-annotation-canvas',canvas=>canvas.width),640);
  const rect=await page.$eval('#image-annotation-canvas',canvas=>{const r=canvas.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}});
  const pixel=async()=>page.$eval('#image-annotation-canvas',canvas=>[...canvas.getContext('2d').getImageData(320,200,1,1).data]);
  const from={x:rect.x+rect.width*.35,y:rect.y+rect.height*.5},to={x:rect.x+rect.width*.65,y:from.y};
  await page.mouse.move(from.x,from.y);await page.mouse.down();await page.mouse.move(to.x,to.y,{steps:12});await page.mouse.up();
  assert.ok((await pixel())[1]<100,'red annotation covers the white source pixel');
  await page.click('#image-annotation-undo');
  assert.ok((await pixel())[1]>240,'undo restores the original pixel');
  await page.mouse.move(from.x,from.y);await page.mouse.down();await page.mouse.move(to.x,to.y,{steps:12});await page.mouse.up();
  await page.type('#image-annotation-text','Please change the marked line to blue.');
  await page.screenshot({path:'/tmp/vmbox-image-annotation-desktop.png'});
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>document.querySelector('#image-annotation').open===false&&document.querySelectorAll('#chat-image-drafts .draft').length===1);
  assert.ok(uploaded?.subarray(0,8).equals(Buffer.from([137,80,78,71,13,10,26,10])),'annotation is uploaded as PNG');
  assert.equal(await page.$eval('#reply-preview',node=>node.hidden),false);
  assert.match(await page.$eval('#reply-preview-text',node=>node.textContent),/Agent: Please review this diagram/);
  assert.equal(await page.$eval('#chat-input',node=>node.value),'Please change the marked line to blue.');
  await page.click('#send');
  await page.waitForFunction(()=>document.querySelector('#reply-preview').hidden);
  assert.deepEqual(sent,{text:'Please change the marked line to blue.',images:[{id:'annotated-image',number:1}],parentMessageId:'source-message',mentionedBoxIds:[]});
  await page.close();

  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await mobile.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await mobile.waitForSelector('#chat-messages .media-button');
  await mobile.click('#chat-messages .media-button');
  await mobile.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth===640);
  await mobile.click('#media-annotate');
  await mobile.waitForSelector('#image-annotation[open]');
  const mobileRect=await mobile.$eval('#image-annotation-canvas',canvas=>{const r=canvas.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height}});
  await mobile.touchscreen.touchStart(mobileRect.x+mobileRect.width*.35,mobileRect.y+mobileRect.height*.5);
  await mobile.touchscreen.touchMove(mobileRect.x+mobileRect.width*.65,mobileRect.y+mobileRect.height*.5);
  await mobile.touchscreen.touchEnd();
  await mobile.screenshot({path:'/tmp/vmbox-image-annotation-mobile.png'});
  assert.ok((await mobile.$eval('#image-annotation-canvas',canvas=>canvas.getContext('2d').getImageData(320,200,1,1).data[1]))<100,'finger drawing marks the canvas');
  await mobile.click('#image-annotation-add');
  await mobile.waitForSelector('#chat-image-drafts .draft');
  assert.equal(await mobile.$eval('#reply-preview',node=>node.hidden),false);
  await mobile.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
