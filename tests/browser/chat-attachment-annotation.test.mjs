import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assetNames=['chat.html','chat.js','chat.css','motion.js','mascot.js','mascot.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(assetNames.map(async name=>[name,await readFile('internal/controller/web/'+name)])));
const rootID='11111111-1111-4111-8111-111111111111';
const threadMessages=[
 {id:rootID,threadId:rootID,direction:'user',text:'Review these images',state:'delivered',createdAt:'2026-09-21T02:00:00Z',updatedAt:'2026-09-21T02:00:00Z'},
 {id:'22222222-2222-4222-8222-222222222222',threadId:rootID,parentMessageId:rootID,direction:'agent',text:'Ready',state:'delivered',createdAt:'2026-09-21T02:01:00Z',updatedAt:'2026-09-21T02:01:00Z'},
];

async function withChat(fn){
 const uploads=[],posts=[];
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(path.startsWith('/fixture/upload/')){res.setHeader('Content-Type','image/png');return res.end(uploads[Number(path.split('/').at(-1))-1])}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([{id:'builder',name:'Builder',state:'running',defaultAgent:'claude'}]));
  if(path==='/v1/box-conversations'||path==='/v1/tool-presets'||path==='/v1/chat-commands')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/run-once-images'&&req.method==='POST'){
   const chunks=[];for await(const chunk of req)chunks.push(chunk);uploads.push(Buffer.concat(chunks));
   return res.end(JSON.stringify({id:'upload-'+uploads.length}));
  }
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   const chunks=[];for await(const chunk of req)chunks.push(chunk);posts.push(JSON.parse(Buffer.concat(chunks).toString()));
   return res.end(JSON.stringify({message:{state:'delivered'}}));
  }
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify(threadMessages));
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{await fn({browser,base:'http://127.0.0.1:'+server.address().port,uploads,posts})}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}

async function addImages(page,{selector='#attachments',target='#chat-composer',names,mode='input',width=640,height=400}){
 await page.evaluate(async({selector,target,names,mode,width,height})=>{
  const canvas=document.createElement('canvas');canvas.width=width;canvas.height=height;
  const ctx=canvas.getContext('2d');ctx.fillStyle='white';ctx.fillRect(0,0,width,height);
  const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
  const transfer=new DataTransfer();
  for(const name of names)transfer.items.add(new File([blob],name,{type:'image/png'}));
  if(mode==='input'){const input=document.querySelector(selector);input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}))}
  else if(mode==='paste')document.querySelector(target).dispatchEvent(new ClipboardEvent('paste',{clipboardData:transfer,bubbles:true,cancelable:true}));
  else document.querySelector(target).dispatchEvent(new DragEvent('drop',{dataTransfer:transfer,bubbles:true,cancelable:true}));
 },{selector,target,names,mode,width,height});
}

async function openBox(page,base){
 await page.goto(base+'/chat#box=builder');
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-header-name')?.textContent==='Builder');
}

async function draw(page,tool,from,to,{shift=false}={}){
 await page.click('[data-annotation-tool="'+tool+'"]');
 const rect=await page.$eval('#image-annotation-canvas',canvas=>canvas.getBoundingClientRect().toJSON());
 const x=point=>rect.x+rect.width*point[0],y=point=>rect.y+rect.height*point[1];
 if(shift)await page.keyboard.down('Shift');
 await page.mouse.move(x(from),y(from));await page.mouse.down();await page.mouse.move(x(to),y(to),{steps:8});await page.mouse.up();
 if(shift)await page.keyboard.up('Shift');
}

test('composer annotation replaces one upload, cancel preserves another, and paste/drop previews can be edited',async()=>{
 await withChat(async({browser,base,uploads,posts})=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:800});await openBox(page,base);
  await addImages(page,{names:['first.png','second.png']});
  await page.waitForFunction(()=>document.querySelectorAll('#chat-image-drafts .draft').length===2);
  assert.equal(uploads.length,2);
  await page.click('#chat-image-drafts .draft:nth-child(2) .draft-annotate');
  await page.waitForSelector('#image-annotation[open]');
  await draw(page,'pen',[.2,.3],[.5,.3]);
  await page.click('#image-annotation-cancel');
  assert.equal(uploads.length,2,'cancel never uploads a replacement');
  await page.click('#chat-image-drafts .draft:first-child .draft-annotate');
  await page.waitForSelector('#image-annotation[open]');
  await draw(page,'pen',[.2,.3],[.5,.3]);
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  assert.equal(uploads.length,3);
  assert.deepEqual([...uploads[2].subarray(0,8)],[137,80,78,71,13,10,26,10]);
  await page.waitForFunction(()=>document.querySelector('#chat-image-drafts .draft:first-child img')?.naturalWidth===640);
  const preview=await page.$eval('#chat-image-drafts .draft:first-child img',img=>({src:img.src,width:img.naturalWidth}));
  assert.match(preview.src,/blob:/);assert.equal(preview.width,640);
  assert.equal(await page.$eval('#chat-image-drafts .draft:first-child',draft=>draft.title),'first-annotated.png');
  await page.click('#chat-image-drafts .draft:first-child .draft-annotate');
  await page.waitForSelector('#image-annotation[open]');
  await draw(page,'line',[.1,.6],[.4,.6]);
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  assert.equal(uploads.length,4,'the annotated draft can be annotated again');
  await addImages(page,{names:['pasted.png'],mode:'paste'});
  await page.waitForFunction(()=>document.querySelectorAll('#chat-image-drafts .draft').length===3);
  await addImages(page,{names:['dropped.png'],mode:'drop'});
  await page.waitForFunction(()=>document.querySelectorAll('#chat-image-drafts .draft').length===4);
  await page.click('#chat-image-drafts .draft:nth-child(3) .draft-annotate');
  await page.waitForSelector('#image-annotation[open]');
  await draw(page,'line',[.1,.2],[.4,.2]);
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  await page.click('#send');
  await page.waitForFunction(()=>document.querySelectorAll('#chat-image-drafts .draft').length===0);
  assert.deepEqual(posts.at(-1).images,[{id:'upload-4',number:1},{id:'upload-2',number:2},{id:'upload-7',number:3},{id:'upload-6',number:4}]);
  await page.close();
 });
});

test('all drawing tools export sharp marks at the original resolution with undo, redo and clear',async()=>{
 await withChat(async({browser,base,uploads})=>{
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});await openBox(page,base);
  await addImages(page,{names:['large.png'],width:5000,height:1200});
  await page.waitForSelector('#chat-image-drafts .draft');
  await page.click('#chat-image-drafts .draft-annotate');await page.waitForSelector('#image-annotation[open]');
  await draw(page,'pen',[.1,.15],[.2,.15]);
  await draw(page,'rectangle',[.28,.1],[.4,.25]);
  await draw(page,'ellipse',[.5,.1],[.62,.3]);
  await draw(page,'line',[.1,.5],[.25,.7]);
  await draw(page,'arrow',[.45,.6],[.7,.6]);
  await draw(page,'rectangle',[.7,.2],[.8,.3],{shift:true});
  await draw(page,'ellipse',[.75,.4],[.85,.5],{shift:true});
  await draw(page,'line',[.72,.75],[.82,.82],{shift:true});
  await draw(page,'rectangle',[.8,.7],[.9,.85]);
  await page.click('#image-annotation-undo');
  await page.click('#image-annotation-redo');
  await page.click('#image-annotation-undo');
  await page.click('#image-annotation-clear');
  await page.click('#image-annotation-undo');
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  const png=uploads.at(-1);
  assert.deepEqual([png.readUInt32BE(16),png.readUInt32BE(20)],[5000,1200],'PNG keeps the source dimensions');
  const samples=await page.evaluate(async()=>{
   const image=new Image();image.src='/fixture/upload/2';await image.decode();
   const canvas=document.createElement('canvas');canvas.width=image.naturalWidth;canvas.height=image.naturalHeight;
   const ctx=canvas.getContext('2d');ctx.drawImage(image,0,0);
   return [[.15,.15],[.34,.1],[.56,.1],[.175,.6],[.575,.6],[.712,.3],[.762,.4],[.77,.75],[.85,.7],[.8,.3],[.77,.82]].map(([x,y])=>[...ctx.getImageData(Math.round(x*canvas.width),Math.round(y*canvas.height),1,1).data]);
  });
  for(const [index,pixel] of samples.entries())assert.equal(pixel.slice(0,3).some(channel=>channel<230),index<8,'shape '+index+' pixel='+pixel);
  await page.close();
 });
});

test('thread composer accepts pasted and dropped images, then annotates and sends only its own drafts',async()=>{
 await withChat(async({browser,base,uploads,posts})=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:800});await openBox(page,base);
  await page.waitForSelector('#chat-messages .msg-thread-line');
  await page.click('#chat-messages .msg-thread-line');
  await page.waitForFunction(()=>!document.querySelector('#thread-panel').hidden);
  await addImages(page,{names:['thread-paste.png'],mode:'paste',target:'#thread-composer'});
  await page.waitForFunction(()=>document.querySelectorAll('#thread-image-drafts .draft').length===1);
  await addImages(page,{names:['thread-drop.png'],mode:'drop',target:'#thread-composer'});
  await page.waitForFunction(()=>document.querySelectorAll('#thread-image-drafts .draft').length===2);
  await page.click('#thread-image-drafts .draft:first-child .draft-annotate');
  await page.waitForSelector('#image-annotation[open]');
  await draw(page,'arrow',[.2,.5],[.6,.5]);
  await page.click('#image-annotation-add');
  await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  await page.click('#thread-send');
  await page.waitForFunction(()=>document.querySelectorAll('#thread-image-drafts .draft').length===0);
  assert.deepEqual(posts.at(-1),{text:'',images:[{id:'upload-3',number:1},{id:'upload-2',number:2}],parentMessageId:rootID});
  assert.equal(uploads.length,3);
  await page.close();
 });
});

test('mobile thumbnail opens a full-screen editor with touch-friendly shape tools',async()=>{
 await withChat(async({browser,base,uploads})=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});await openBox(page,base);
  await addImages(page,{names:['mobile.png']});await page.waitForSelector('#chat-image-drafts .draft');
  await page.click('#chat-image-drafts .draft-open');await page.waitForSelector('#image-annotation[open]');
  const layout=await page.evaluate(()=>{
   const dialog=document.querySelector('#image-annotation').getBoundingClientRect(),bar=document.querySelector('.image-annotation-tools');
   return {width:dialog.width,height:dialog.height,buttonHeights:[...bar.querySelectorAll('button')].map(button=>button.getBoundingClientRect().height),scrollable:bar.scrollWidth>bar.clientWidth};
  });
  assert.equal(Math.round(layout.width),390);assert.equal(Math.round(layout.height),844);
  assert.ok(layout.buttonHeights.every(height=>height>=40),JSON.stringify(layout));
  assert.equal(layout.scrollable,true);
  await page.click('[data-annotation-tool="rectangle"]');
  const rect=await page.$eval('#image-annotation-canvas',canvas=>canvas.getBoundingClientRect().toJSON());
  await page.touchscreen.touchStart(rect.x+rect.width*.2,rect.y+rect.height*.2);
  await page.touchscreen.touchMove(rect.x+rect.width*.6,rect.y+rect.height*.6);
  await page.touchscreen.touchEnd();
  const pixel=await page.$eval('#image-annotation-canvas',canvas=>[...canvas.getContext('2d').getImageData(canvas.width*.4,canvas.height*.2,1,1).data]);
  assert.ok(pixel.slice(0,3).some(channel=>channel<230),'touch drag previews a rectangle');
  await page.click('#image-annotation-add');await page.waitForFunction(()=>!document.querySelector('#image-annotation').open);
  assert.equal(uploads.length,2);await page.close();
 });
});
