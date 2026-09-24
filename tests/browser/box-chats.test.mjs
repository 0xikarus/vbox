import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js','box-chats.html','box-chats.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222',pairKey=a+'/'+b,now=new Date().toISOString();
const pixel=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');

async function withChat(fn,{pairDelay=0}={}){
 const server=http.createServer(async(request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'||path==='/box-chats'){response.setHeader('Content-Type','text/html');if(path==='/box-chats')response.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'");return response.end(files[path==='/chat'?'chat.html':'box-chats.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return response.end(files[path.slice(1)])}
  if(path.startsWith('/v1/messages/')){response.setHeader('Content-Type','image/png');return response.end(pixel)}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify([{id:a,name:'Builder',state:'running',defaultAgent:'claude',provider:'railway'},{id:b,name:'Reviewer',state:'running',defaultAgent:'codex',provider:'railway'}]));
  if(path==='/v1/box-conversations')return response.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:now,lastText:'The review is ready'}]));
  if(path==='/v1/box-conversations/'+a+'/'+b+'/messages'){
   if(pairDelay)await new Promise(resolve=>setTimeout(resolve,pairDelay));
   return response.end(JSON.stringify([
   {id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Please inspect this image',state:'delivered',createdAt:now,updatedAt:now,images:[{id:'image-1',number:1,mediaType:'image/png'}]},
   {id:'m2',senderBoxId:b,recipientBoxId:a,direction:'box',text:'The review is ready',state:'delivered',createdAt:now,updatedAt:now}
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
  assert.equal(await page.$eval('#chat-messages img',image=>image.naturalWidth),1);
  assert.equal(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  assert.equal(await page.$('#chat-messages .msg-actions'),null,'read-only messages have no reply controls');
  await page.click('[data-box-id="'+a+'"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('Owner chat is here'));
  assert.notEqual(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  await page.click('[data-pair-key] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-messages')?.textContent.includes('The review is ready'));
  assert.equal(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  await page.close();
 });
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
  assert.deepEqual(await desktop.$$eval('#chat-entries li',items=>items.map(item=>item.className==='conversation-group'?item.textContent:item.dataset.boxId?'box:'+item.dataset.boxId:'pair:'+item.dataset.pairKey)),['Pinned','box:'+b,'pair:'+pairKey,'box:'+a]);
  await desktop.click('[data-pair-key]',{button:'right'});
  assert.equal(await desktop.$eval('#row-menu button:first-child',button=>button.textContent),'Unpin chat');
  await desktop.screenshot({path:'/tmp/vmbox-chat-pins-desktop.png'});
  await desktop.reload();
  await desktop.waitForSelector('[data-pair-key]');
  assert.equal(await desktop.$eval('#chat-entries li:first-child',item=>item.textContent),'Pinned','pins survive refresh');

  const mobile=await browser.newPage();await mobile.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await mobile.goto(base+'/chat');
  await mobile.waitForSelector('[data-pair-key]');
  const hold=async selector=>{
   const point=await mobile.$eval(selector,element=>{const rect=element.getBoundingClientRect();return {x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
   await mobile.touchscreen.touchStart(point.x,point.y);
   await new Promise(resolve=>setTimeout(resolve,650));
   await mobile.touchscreen.touchEnd();
   await mobile.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  };
  await hold('[data-pair-key]');
  assert.equal(await mobile.$eval('#row-menu button:first-child',button=>button.textContent),'Unpin chat');
  await mobile.screenshot({path:'/tmp/vmbox-chat-pins-mobile.png'});
  await mobile.click('#row-menu button:first-child');
  await hold('[data-box-id="'+b+'"]');
  await mobile.click('#row-menu button:first-child');
  assert.equal(await mobile.$('#chat-entries .conversation-group:first-child'),null,'the Pinned section disappears when empty');
  assert.equal(await mobile.$eval('[data-pair-key]',row=>row.textContent.includes('Builder ↔ Reviewer')),true);
  await mobile.close();await desktop.close();
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
