import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js','box-chats.html','box-chats.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222',pairKey=a+'/'+b,now=new Date().toISOString();
const illustration=id=>Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400" viewBox="0 0 640 400"><rect width="640" height="400" fill="#142433"/><rect x="24" y="24" width="592" height="352" rx="16" fill="${id==='image-3'?'#294b48':'#244060'}" stroke="#8cb8df"/><text x="50" y="85" fill="#f3f8ff" font-family="sans-serif" font-size="28" font-weight="bold">${id==='image-1'?'Build output · 1':id==='image-2'?'Build output · 2':'Review result'}</text><path d="M90 205h460" stroke="#9bc8ec" stroke-width="7"/><g fill="#eaf3ff" font-family="sans-serif" font-size="23"><text x="68" y="175">Source</text><text x="274" y="175">Build</text><text x="472" y="175">Review</text></g><circle cx="95" cy="205" r="19" fill="#94c5ee"/><circle cx="320" cy="205" r="19" fill="#94c5ee"/><circle cx="545" cy="205" r="19" fill="#94c5ee"/></svg>`);

async function withChat(fn,{pairDelay=0,pairMessages=null}={}){
 const server=http.createServer(async(request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'||path==='/box-chats'){response.setHeader('Content-Type','text/html');if(path==='/box-chats')response.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'");return response.end(files[path==='/chat'?'chat.html':'box-chats.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return response.end(files[path.slice(1)])}
  if(path.startsWith('/v1/messages/')){response.setHeader('Content-Type','image/svg+xml');return response.end(illustration(path.split('/').at(-1)))}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify([{id:a,name:'Builder',state:'running',defaultAgent:'claude',provider:'railway'},{id:b,name:'Reviewer',state:'running',defaultAgent:'codex',provider:'railway'}]));
  if(path==='/v1/box-conversations')return response.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:pairMessages?.at(-1)?.createdAt||now,lastText:pairMessages?.at(-1)?.text||'The review is ready'}]));
  if(path==='/v1/box-conversations/'+a+'/'+b+'/messages'){
   if(pairDelay)await new Promise(resolve=>setTimeout(resolve,pairDelay));
   return response.end(JSON.stringify(pairMessages||[
   {id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Please inspect these images',state:'delivered',createdAt:now,updatedAt:now,images:[{id:'image-1',number:1,mediaType:'image/png'},{id:'image-2',number:2,mediaType:'image/png'}]},
   {id:'m2',senderBoxId:b,recipientBoxId:a,direction:'box',text:'The review is ready',state:'delivered',createdAt:now,updatedAt:now,images:[{id:'image-3',number:1,mediaType:'image/png'}]}
   ]));
  }
  if(path==='/v1/logical-boxes/'+a+'/messages')return response.end(JSON.stringify([{id:'owner-1',direction:'agent',text:'Owner chat is here',state:'delivered',createdAt:now,updatedAt:now}]));
  if(path==='/v1/tool-presets')return response.end('[]');
  if(path==='/v1/chat-commands')return response.end('[]');
  if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
  if(path.endsWith('/messages'))return response.end('[]');
  response.statusCode=404;response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port)}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}

test('owner and box conversations share the Chats list and transcript',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===2);
  assert.equal(await page.$$eval('#chat-entries [data-box-id]',rows=>rows.length),2);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key]',rows=>rows.length),1);
  assert.equal(await page.$eval('#chat-header-name',element=>element.textContent),'Builder ↔ Reviewer');
  assert.deepEqual(await page.$$eval('#chat-messages .agent-origin',elements=>elements.map(element=>element.textContent)),['Builder','Reviewer']);
  assert.deepEqual(await page.$$eval('#chat-messages .msg',elements=>elements.map(element=>element.classList.contains('user')?'right':'left')),['left','right']);
  const sides=await page.$$eval('#chat-messages .msg',elements=>elements.map(element=>({left:element.getBoundingClientRect().left,right:element.getBoundingClientRect().right})));
  assert.ok(sides[0].left<sides[1].left&&sides[0].right<sides[1].right,'each box should have its own side');
  assert.equal(await page.$eval('#chat-messages img',image=>image.naturalWidth),640);
  assert.equal(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  assert.equal(await page.$('#chat-messages .msg-actions'),null,'read-only messages have no reply controls');
  const messageRows=await page.$$('#chat-messages .msg');
  await (await messageRows[0].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden);
  assert.equal(await page.$eval('#media-viewer-count',element=>element.textContent),'1 / 2');
  await page.click('#media-viewer-next');
  assert.equal(await page.$eval('#media-viewer-count',element=>element.textContent),'2 / 2');
  await page.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=640);
  await page.screenshot({path:'/tmp/vmbox-message-gallery-pair-desktop.png'});
  await page.keyboard.press('Escape');
  await (await messageRows[1].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden);
  assert.equal(await page.$eval('#media-viewer-count',element=>element.hidden),true,'the next pair message has a separate gallery');
  await page.keyboard.press('Escape');
  await page.click('[data-box-id="'+a+'"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Owner chat is here'));
  assert.notEqual(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  await page.click('[data-pair-key] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('The review is ready'));
  assert.equal(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  await page.close();
 });
});

test('box conversation media gallery supports mobile swipes within one message',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForSelector('#chat-messages .msg .media-button');
  const messageRows=await page.$$('#chat-messages .msg');
  await (await messageRows[0].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden);
  await page.waitForFunction(()=>document.querySelector('#media-viewer-body img')?.naturalWidth>=640);
  assert.equal(await page.$eval('#media-viewer-count',element=>element.textContent),'1 / 2');
  await page.screenshot({path:'/tmp/vmbox-message-gallery-pair-mobile.png'});
  await page.touchscreen.touchStart(270,400);
  await page.touchscreen.touchMove(100,400);
  await page.touchscreen.touchEnd();
  await page.waitForFunction(()=>document.querySelector('#media-viewer-count').textContent==='2 / 2');
  await page.keyboard.press('Escape');
  await (await messageRows[1].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden);
  assert.equal(await page.$eval('#media-viewer-next',element=>element.hidden),true);
  await page.close();
 });
});

test('box conversations open at the latest message on desktop and mobile',async()=>{
 const pairMessages=Array.from({length:36},(_,index)=>({
  id:'pair-'+index,senderBoxId:index%2?a:b,recipientBoxId:index%2?b:a,direction:'box',
  text:index===35?'LATEST BOX MESSAGE':'Box message '+index+' '+('discussion '.repeat(12)),
  state:'delivered',createdAt:new Date(Date.now()-(36-index)*60000).toISOString(),updatedAt:new Date(Date.now()-(36-index)*60000).toISOString()
 }));
 await withChat(async(browser,base)=>{
  const bottom=element=>element.scrollHeight-element.scrollTop-element.clientHeight;
  const desktop=await browser.newPage();await desktop.setViewport({width:1100,height:760});
  await desktop.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await desktop.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===36);
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await desktop.$eval('#chat-messages',bottom)<3,'a direct link starts at the newest box message');
  await desktop.screenshot({path:'/tmp/vmbox-pair-latest-desktop.png'});
  await desktop.$eval('#chat-messages',element=>{element.scrollTop=0});
  await desktop.waitForFunction(()=>document.querySelector('#chat-messages').scrollTop===0);
  await desktop.click('[data-box-id="'+a+'"] .chat-meta');
  await desktop.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Builder');
  pairMessages.push({id:'pair-new',senderBoxId:b,recipientBoxId:a,direction:'box',text:'A NEW BOX REPLY',state:'delivered',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()});
  await desktop.click('#refresh');
  await desktop.waitForFunction(()=>!document.querySelector('[data-pair-key] .unread-note').hidden);
  await desktop.screenshot({path:'/tmp/vmbox-pair-note-desktop.png'});
  await desktop.click('[data-pair-key] .chat-meta');
  await desktop.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===37);
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await desktop.$eval('#chat-messages',bottom)<3,'returning to a box conversation starts at its newest message');
  assert.equal(await desktop.$eval('[data-pair-key] .unread-note',note=>note.hidden),true,'opening the newest pair message clears its left-list note');

  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await mobile.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await mobile.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===37);
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await mobile.$eval('#chat-messages',bottom)<3,'mobile opens at the newest box message');
  await mobile.screenshot({path:'/tmp/vmbox-pair-latest-mobile.png'});
  await mobile.click('#chat-back');
  await mobile.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  pairMessages.push({id:'pair-new-mobile',senderBoxId:a,recipientBoxId:b,direction:'box',text:'NEW BOX REPLY ON MOBILE',state:'delivered',createdAt:new Date(Date.now()+1000).toISOString(),updatedAt:new Date(Date.now()+1000).toISOString()});
  await mobile.click('#refresh');
  await mobile.waitForFunction(()=>!document.querySelector('[data-pair-key] .unread-note').hidden);
  await new Promise(resolve=>setTimeout(resolve,350));
  await mobile.screenshot({path:'/tmp/vmbox-pair-note-mobile.png'});
  await mobile.close();await desktop.close();
 },{pairMessages});
});

test('box and Box ↔ Box conversations can be pinned, reordered, and unpinned on desktop and mobile',async()=>{
 await withChat(async(browser,base)=>{
  const desktop=await browser.newPage();await desktop.setViewport({width:1200,height:800});
  await desktop.goto(base+'/chat#box='+a);
  await desktop.waitForSelector('[data-pair-key]');
  assert.equal(await desktop.$('.chat-pin'),null,'rows do not show a permanent pin control');
  await desktop.click('[data-box-id="'+b+'"]',{button:'right'});
  await desktop.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.equal(await desktop.$eval('#row-menu button:first-child',button=>button.textContent),'Pin chat');
  await desktop.click('#row-menu button:first-child');
  await desktop.click('[data-pair-key]',{button:'right'});
  await desktop.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await desktop.click('#row-menu button:first-child');
  assert.equal(new URL(desktop.url()).hash,'#box='+a,'pinning from a context menu must not navigate away from the open chat');
  assert.deepEqual(await desktop.$$eval('#chat-entries li',items=>items.map(item=>item.className==='conversation-group'||item.className==='conversation-divider'?item.textContent:item.dataset.boxId?'box:'+item.dataset.boxId:'pair:'+item.dataset.pairKey)),['Pinned','box:'+b,'pair:'+pairKey,'Other chats','box:'+a]);
  assert.equal(await desktop.$eval('#chat-entries .conversation-divider',item=>item.getAttribute('role')),'separator');
  await desktop.click('[data-pair-key]',{button:'right'});
  assert.equal(await desktop.$eval('#row-menu button:first-child',button=>button.textContent),'Unpin chat');
  await desktop.click('#chat-list-head h1');
  await desktop.waitForFunction(()=>document.querySelector('#row-menu').hidden);
  await desktop.screenshot({path:'/tmp/vmbox-chat-pins-desktop.png'});
  await desktop.type('#chat-filter','Reviewer');
  await desktop.waitForFunction(()=>!document.querySelector('#chat-entries .conversation-divider'));
  await desktop.$eval('#chat-filter',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await desktop.waitForSelector('#chat-entries .conversation-divider');
  await desktop.reload();
  await desktop.waitForSelector('[data-pair-key]');
  assert.equal(await desktop.$eval('#chat-entries li:first-child',item=>item.textContent),'Pinned','pins survive refresh');

  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await mobile.goto(base+'/chat');
  await mobile.waitForSelector('[data-pair-key]');
  await mobile.waitForSelector('#chat-entries .conversation-divider');
  await mobile.screenshot({path:'/tmp/vmbox-chat-pins-mobile.png'});
  const hold=async selector=>{
   const point=await mobile.$eval(selector,element=>{const rect=element.getBoundingClientRect();return {x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
   await mobile.touchscreen.touchStart(point.x,point.y);
   await new Promise(resolve=>setTimeout(resolve,650));
   await mobile.touchscreen.touchEnd();
   await mobile.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  };
  await hold('[data-pair-key]');
  assert.equal(await mobile.$eval('#row-menu button:first-child',button=>button.textContent),'Unpin chat');
  await mobile.click('#row-menu button:first-child');
  await hold('[data-box-id="'+b+'"]');
  await mobile.click('#row-menu button:first-child');
  assert.equal(await mobile.$('#chat-entries .conversation-group:first-child'),null,'the Pinned section disappears when empty');
  assert.equal(await mobile.$eval('[data-pair-key]',row=>row.textContent.includes('Builder ↔ Reviewer')),true);
  await mobile.close();await desktop.close();
 });
});

test('chat groups combine unread indicators, accept dragged chats, and persist collapse state',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});
  await page.goto(base+'/chat');
  await page.waitForSelector('[data-pair-key]');
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='1',{},a);
  await page.click('[data-box-id="'+a+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New group…').click());
  await page.waitForSelector('#chat-group-dialog[open]');
  await page.type('#chat-group-name','Projects');
  await page.click('#chat-group-form button[type=submit]');
  await page.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Projects');
  assert.equal(await page.$eval('.chat-folder .unread',badge=>badge.textContent),'1');
  await page.$eval('[data-pair-key]',row=>{
   const transfer=new DataTransfer();
   row.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:transfer}));
   const target=document.querySelector('.chat-folder');
   target.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   target.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   row.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:transfer}));
  });
  await page.waitForFunction(()=>document.querySelector('.chat-folder .unread')?.textContent==='2');
  assert.equal(await page.$eval('.chat-folder-count',node=>node.textContent),'2');
  await page.click('.chat-folder-toggle');
  assert.equal(await page.$('[data-box-id="'+a+'"]'),null);
  assert.equal(await page.$('[data-pair-key]'),null);
  assert.equal(await page.$eval('.chat-folder .unread',badge=>badge.textContent),'2','collapsed groups keep their unread indicator');
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.chat-folder .unread')?.textContent==='2');
  assert.equal(await page.$eval('.chat-folder-toggle',button=>button.getAttribute('aria-expanded')),'false');
  await page.type('#chat-filter','Projects');
  await page.waitForSelector('[data-pair-key]');
  assert.ok(await page.$('[data-box-id="'+a+'"]'),'searching a group reveals its members');
  await page.$eval('#chat-filter',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.waitForFunction(()=>!document.querySelector('[data-pair-key]'));
  await page.click('.chat-folder-toggle');
  await page.click('[data-box-id="'+a+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Pin chat').click());
  assert.equal(await page.$eval('#chat-entries li:first-child',node=>node.textContent),'Pinned');
  assert.equal(await page.$eval('.chat-folder-count',node=>node.textContent),'1','pinning removes the chat from its group');
  await page.click('.chat-folder-menu');
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Rename group…').click());
  await page.waitForSelector('#chat-group-dialog[open]');
  await page.$eval('#chat-group-name',input=>{input.value='Reviewed';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.click('#chat-group-form button[type=submit]');
  assert.equal(await page.$eval('.chat-folder-name',node=>node.textContent),'Reviewed');
  page.once('dialog',dialog=>dialog.accept());
  await page.click('.chat-folder-menu');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Delete group…').click());
  await page.waitForFunction(()=>!document.querySelector('.chat-folder'));
  assert.ok(await page.$('[data-pair-key]'),'deleting a group returns its chats to the list');
  await page.close();

  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await mobile.goto(base+'/chat');
  await mobile.waitForSelector('[data-pair-key]');
  const point=await mobile.$eval('[data-pair-key]',row=>{const rect=row.getBoundingClientRect();return {x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
  await mobile.touchscreen.touchStart(point.x,point.y);
  await new Promise(resolve=>setTimeout(resolve,650));
  await mobile.touchscreen.touchEnd();
  await mobile.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await mobile.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New group…').click());
  await mobile.type('#chat-group-name','Mobile');
  await mobile.click('#chat-group-form button[type=submit]');
  await mobile.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Mobile');
  await mobile.click('.chat-folder-toggle');
  assert.equal(await mobile.$('[data-pair-key]'),null);
  assert.equal(await mobile.$eval('.chat-folder .unread',badge=>badge.textContent),'1');
  await mobile.close();
 });
});

test('old box conversation links open the unified mobile chat',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(base+'/box-chats#pair='+pairKey);
  await page.waitForFunction(()=>location.pathname==='/chat'&&document.querySelectorAll('#chat-messages .msg').length===2);
  assert.equal(await page.$eval('#chat-conversation',element=>element.classList.contains('pair-view')),true);
  await page.click('#chat-back');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await page.$eval('[data-pair-key]',element=>element.textContent.includes('Builder ↔ Reviewer')),true);
  await page.close();
 });
});

test('a late box conversation response cannot overwrite the selected owner chat',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelector('#chat-conversation').classList.contains('pair-view'));
  await page.click('[data-box-id="'+a+'"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Owner chat is here'));
  await new Promise(resolve=>setTimeout(resolve,700));
  assert.equal(await page.$eval('#chat-messages',element=>element.textContent.includes('The review is ready')),false);
  assert.equal(await page.$eval('#chat-conversation',element=>element.classList.contains('pair-view')),false);
  await page.close();
 },{pairDelay:500});
});
