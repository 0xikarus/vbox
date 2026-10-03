import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','box-chats.html','box-chats.js','sheet-scroll.css','dialog-theme.css','workspace-nav.css','login.css','fonts.css','vbox-logo.png','vbox-logo-dark.png'].map(async name=>[name,await readFile('internal/controller/web/'+name,name.endsWith('.png')?undefined:'utf8')])));
const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222',pairKey=a+'/'+b,now=new Date().toISOString();
const illustration=id=>Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400" viewBox="0 0 640 400"><rect width="640" height="400" fill="#142433"/><rect x="24" y="24" width="592" height="352" rx="16" fill="${id==='image-3'?'#294b48':'#244060'}" stroke="#8cb8df"/><text x="50" y="85" fill="#f3f8ff" font-family="sans-serif" font-size="28" font-weight="bold">${id==='image-1'?'Build output · 1':id==='image-2'?'Build output · 2':'Review result'}</text><path d="M90 205h460" stroke="#9bc8ec" stroke-width="7"/><g fill="#eaf3ff" font-family="sans-serif" font-size="23"><text x="68" y="175">Source</text><text x="274" y="175">Build</text><text x="472" y="175">Review</text></g><circle cx="95" cy="205" r="19" fill="#94c5ee"/><circle cx="320" cy="205" r="19" fill="#94c5ee"/><circle cx="545" cy="205" r="19" fill="#94c5ee"/></svg>`);

async function withChat(fn,{pairDelay=0,pairMessages=null,boxMessages=null,boxMessagesB=null,role='owner',createBehavior=null,initialLayout=null,extraBoxes=[],extraPairs=[]}={}){
 let sidebarLayout=initialLayout;
 const createdBoxes=[];
 const server=http.createServer(async(request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'||path==='/box-chats'){response.setHeader('Content-Type','text/html');if(path==='/box-chats')response.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'");return response.end(files[path==='/chat'?'chat.html':'box-chats.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':path.endsWith('.png')?'image/png':'text/javascript');return response.end(files[path.slice(1)])}
  if(path.startsWith('/v1/messages/')){response.setHeader('Content-Type','image/svg+xml');return response.end(illustration(path.split('/').at(-1)))}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role,accountId:'account-a'}));
  if(path==='/v1/chat-sidebar-layout'){
   if(request.method==='PUT'){
    let body='';for await(const chunk of request)body+=chunk;
    sidebarLayout={...JSON.parse(body),exists:true};
   }
   return response.end(JSON.stringify(sidebarLayout||{exists:false,groups:[],members:{}}));
  }
  if(path==='/v1/logical-boxes'&&request.method==='POST'){
   let body='';for await(const chunk of request)body+=chunk;
   if(createBehavior?.fail){response.statusCode=500;return response.end('{"error":"Create failed"}')}
   const submitted=JSON.parse(body),box={id:'created-'+(createdBoxes.length+1),name:submitted.name,state:'running',defaultAgent:submitted.defaultAgent,provider:'railway'};
   createdBoxes.push(box);return response.end(JSON.stringify(box));
  }
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify([{id:a,name:'Builder',state:'running',defaultAgent:'claude',provider:'railway'},{id:b,name:'Reviewer',state:'running',defaultAgent:'codex',provider:'railway'},...extraBoxes,...(createBehavior?.showCreated===false?[]:createdBoxes)]));
  if(path==='/v1/box-conversations')return response.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:pairMessages?.at(-1)?.createdAt||now,lastText:pairMessages?.at(-1)?.text||'The review is ready'},...extraPairs]));
  if(path==='/v1/box-conversations/'+a+'/'+b+'/messages'){
   if(pairDelay)await new Promise(resolve=>setTimeout(resolve,pairDelay));
   return response.end(JSON.stringify(pairMessages||[
   {id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Please inspect these images',state:'delivered',createdAt:now,updatedAt:now,images:[{id:'image-1',number:1,mediaType:'image/png'},{id:'image-2',number:2,mediaType:'image/png'}]},
   {id:'m2',senderBoxId:b,recipientBoxId:a,direction:'box',text:'The review is ready',state:'delivered',createdAt:now,updatedAt:now,images:[{id:'image-3',number:1,mediaType:'image/png'}]}
   ]));
  }
  if(path==='/v1/logical-boxes/'+a+'/messages'){
   const limit=Number(new URL(request.url,'http://fixture').searchParams.get('limit'))||500;
   return response.end(JSON.stringify((boxMessages||[{id:'owner-1',direction:'agent',text:'Owner chat is here',state:'delivered',createdAt:now,updatedAt:now}]).slice(-limit)));
  }
  if(path==='/v1/logical-boxes/'+b+'/messages')return response.end(JSON.stringify(boxMessagesB||[]));
  if(path==='/v1/tool-presets')return response.end('[]');
  if(path==='/v1/login-profiles')return response.end('[]');
  if(path==='/v1/controller-defaults')return response.end(JSON.stringify({provider:'railway',providerCredential:'primary'}));
  if(path==='/v1/provider-credentials')return response.end(JSON.stringify([{provider:'railway',name:'primary'}]));
  if(path==='/v1/chat-commands')return response.end('[]');
  if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
  if(path.endsWith('/messages'))return response.end('[]');
  response.statusCode=404;response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port,()=>sidebarLayout)}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}

test('owner and box conversations share the Chats list and transcript',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===2);
  assert.equal(await page.$('#chat-type-tabs'),null,'the sidebar has no conversation filter tabs');
  assert.equal(await page.$$eval('#chat-entries [data-box-id]',rows=>rows.length),2);
  assert.equal(await page.$$eval('#chat-entries [data-pair-key]',rows=>rows.length),1);
  assert.equal(await page.$eval('#chat-header-name',element=>element.textContent),'Builder ↔ Reviewer');
  assert.equal(await page.$eval('#chat-header-avatar .pair-avatar-stack',node=>node.getAttribute('aria-label')),'Builder and Reviewer');
  await page.hover('#chat-header-avatar .pair-avatar-stack');await new Promise(resolve=>setTimeout(resolve,400));
  assert.equal(await page.$eval('#mascot-mood-tooltip',node=>node.hidden),true,'pair header avatar has no hover tooltip');
  assert.ok(await page.$$eval('#chat-messages .pair-msg',nodes=>nodes.length===2&&nodes.every(node=>{const time=node.querySelector(':scope > .meta time');return time&&getComputedStyle(time.parentElement).display!=='none'&&time.getBoundingClientRect().width>0})), 'each pair message shows its time');
  assert.deepEqual(await page.$$eval('#chat-messages .agent-origin',elements=>elements.map(element=>element.textContent)),['Builder','Reviewer']);
  assert.deepEqual(await page.$$eval('#chat-messages .msg',elements=>elements.map(element=>element.classList.contains('pair-right')?'right':'left')),['left','right']);
  const sides=await page.$$eval('#chat-messages .msg',elements=>elements.map(element=>({left:element.getBoundingClientRect().left,right:element.getBoundingClientRect().right})));
  assert.ok(sides[0].left<sides[1].left&&sides[0].right<sides[1].right,'each box should have its own side');
  assert.equal(await page.$eval('#chat-messages img',image=>image.naturalWidth),640);
  assert.equal(await page.$eval('#chat-composer',element=>getComputedStyle(element).display),'none');
  assert.equal(await page.$('#chat-messages .msg-actions'),null,'read-only messages have no reply controls');
  const messageRows=await page.$$('#chat-messages .msg');
  await (await messageRows[0].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden);
  assert.equal(await page.$eval('#media-annotate',button=>button.hidden),true,'read-only box conversations have no reply action');
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

test('pair messages scroll into view below the sticky hero and media opens by click',async()=>{
 const pairMessages=Array.from({length:36},(_,index)=>({
  id:'scroll-'+index,senderBoxId:index%2?a:b,recipientBoxId:index%2?b:a,direction:'box',
  text:'Discussion '+index+' '+('details '.repeat(10)),state:'delivered',
  createdAt:new Date(Date.now()-(36-index)*60000).toISOString(),updatedAt:new Date(Date.now()-(36-index)*60000).toISOString(),
  ...(index===18?{images:[{id:'image-1',number:1,mediaType:'image/png'}]}:{})
 }));
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===36);
  await page.$$eval('#chat-messages .msg',rows=>rows[18].scrollIntoView({block:'start'}));
  const geometry=await page.evaluate(()=>{
   const row=[...document.querySelectorAll('#chat-messages .msg')][18],hero=document.querySelector('.pair-hero');
   return {rowTop:row.getBoundingClientRect().top,heroBottom:hero.getBoundingClientRect().bottom};
  });
  assert.ok(geometry.rowTop>=geometry.heroBottom,'scrollIntoView places the message below the pinned tiles');
  await (await (await page.$$('#chat-messages .msg'))[18].$('.media-button')).click();
  await page.waitForFunction(()=>!document.querySelector('#media-viewer').hidden,{timeout:3000});
  await page.close();
 },{pairMessages});
});

test('unread badges count replies but exclude MCP activity and system lines',async()=>{
 const boxMessages=[
  ...['First reply','MCP · get_contacts','MCP · chat_message','MCP · heartbeat','Status changed','Second reply','Unsent note'].map((text,index)=>({
   id:'unread-'+index,direction:index===1||index===2||index===3||index===4?'system':index===6?'user':'agent',
   text,state:index===6?'silent':'delivered',createdAt:new Date(Date.now()-(7-index)*1000).toISOString(),updatedAt:new Date(Date.now()-(7-index)*1000).toISOString()
  })),
  {id:'unread-pending',direction:'agent',text:'Reply still pending',state:'pending',createdAt:now,updatedAt:now}
 ];
 const pairNow=Date.now();
 const pairMessages=[
  {id:'pair-1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'First direct message',state:'delivered',createdAt:new Date(pairNow-4000).toISOString(),updatedAt:new Date(pairNow-4000).toISOString()},
  {id:'pair-mcp',senderBoxId:b,recipientBoxId:a,direction:'agent',text:'MCP · chat_message',state:'delivered',createdAt:new Date(pairNow-3000).toISOString(),updatedAt:new Date(pairNow-3000).toISOString()},
  {id:'pair-system',senderBoxId:b,recipientBoxId:a,direction:'system',text:'Status changed',state:'delivered',createdAt:new Date(pairNow-2000).toISOString(),updatedAt:new Date(pairNow-2000).toISOString()},
  {id:'pair-2',senderBoxId:b,recipientBoxId:a,direction:'box',text:'Second direct message',state:'delivered',createdAt:new Date(pairNow).toISOString(),updatedAt:new Date(pairNow).toISOString()}
 ];
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat');
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='2'&&document.querySelector('[data-pair-key] .unread')?.textContent==='2',{},a);
  assert.equal(await page.$eval('#chat-back-count',badge=>badge.textContent),'2','pair activity does not raise the owner alert total');
  assert.equal(await page.$eval('[data-box-id="'+a+'"] .unread',badge=>badge.getAttribute('aria-label')),'2 unread');
  await page.click('[data-box-id="'+a+'"] .chat-meta');
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.hidden&&document.querySelector('#chat-back-count')?.hidden,{},a);
  await page.click('[data-pair-key] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('[data-pair-key] .unread')?.hidden&&document.querySelector('#chat-back-count')?.hidden);
  await page.close();
 },{boxMessages,pairMessages});
});

test('unread box badges expand history and cap display at 99+',async()=>{
 const boxMessages=Array.from({length:105},(_,index)=>({
  id:'long-'+index,direction:'agent',text:'Reply '+index,state:'delivered',
  createdAt:new Date(Date.now()-(105-index)*1000).toISOString(),updatedAt:new Date(Date.now()-(105-index)*1000).toISOString()
 }));
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1100,height:760});
  await page.goto(base+'/chat');
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='99+',{},a);
  assert.equal(await page.$eval('[data-box-id="'+a+'"] .chat-meta',button=>button.getAttribute('aria-label')),'Open chat with Builder · 105 unread');
  assert.equal(await page.$eval('#chat-back-count',badge=>badge.textContent),'99+');
  await page.close();
 },{boxMessages});
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
  await desktop.waitForFunction(()=>document.querySelector('[data-pair-key] .unread')?.textContent==='1');
  await desktop.screenshot({path:'/tmp/vmbox-pair-note-desktop.png'});
  await desktop.click('[data-pair-key] .chat-meta');
  await desktop.waitForFunction(()=>document.querySelectorAll('#chat-messages .msg').length===37);
  await new Promise(resolve=>setTimeout(resolve,200));
  assert.ok(await desktop.$eval('#chat-messages',element=>element.scrollTop)<3,'returning to a box conversation restores its scroll position');
  assert.equal(await desktop.$eval('[data-pair-key] .unread',badge=>badge.hidden),false,'a new reply stays unread while the transcript is scrolled up');
  await desktop.$eval('#chat-messages',element=>{element.scrollTop=element.scrollHeight});
  await desktop.waitForFunction(()=>document.querySelector('[data-pair-key] .unread')?.hidden===true);

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
  await mobile.waitForFunction(()=>document.querySelector('[data-pair-key] .unread')?.textContent==='1');
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
  assert.equal(await desktop.$('#chat-entries .chat-pin'),null,'rows do not show a permanent pin control');
  await desktop.click('[data-box-id="'+b+'"]',{button:'right'});
  await desktop.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.equal(await desktop.$eval('#row-menu > button',button=>button.textContent),'Pin chat');
  await desktop.click('#row-menu > button');
  await desktop.click('[data-pair-key]',{button:'right'});
  await desktop.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await desktop.$$eval('#row-menu > button',buttons=>buttons.find(button=>button.textContent==='Pin chat').click());
  assert.equal(new URL(desktop.url()).hash,'#box='+a,'pinning from a context menu must not navigate away from the open chat');
  assert.deepEqual(await desktop.$$eval('#chat-entries li',items=>items.map(item=>item.dataset.section?item.querySelector('.section-toggle span:nth-child(2)').textContent:item.dataset.boxId?'box:'+item.dataset.boxId:'pair:'+item.dataset.pairKey)),['Chats','box:'+b,'box:'+a,'Box conversations','pair:'+pairKey]);
  assert.equal(await desktop.$eval('[data-box-id="'+b+'"] .chat-row-pin',node=>node.hidden),false);
  assert.equal(await desktop.$eval('[data-pair-key] .chat-row-pin',node=>node.hidden),false);
  assert.equal(await desktop.$eval('#chat-entries .conversation-divider',item=>item.dataset.section),'boxes');
  await desktop.click('[data-pair-key]',{button:'right'});
  assert.ok(await desktop.$$eval('#row-menu > button',buttons=>buttons.some(button=>button.textContent==='Unpin chat')));
  await desktop.click('#chat-filter-row');
  await desktop.waitForFunction(()=>document.querySelector('#row-menu').hidden);
  await desktop.screenshot({path:'/tmp/vmbox-chat-pins-desktop.png'});
  await desktop.type('#chat-filter','Reviewer');
  await desktop.waitForFunction(id=>!document.querySelector('[data-box-id="'+id+'"]'),{},a);
  await desktop.$eval('#chat-filter',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await desktop.waitForSelector('#chat-entries .conversation-divider');
  await desktop.reload();
  await desktop.waitForSelector('[data-pair-key]');
  assert.equal(await desktop.$eval('[data-section="boxes"] + li',item=>item.dataset.boxId),b,'pins survive refresh at the top of Chats');

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
  assert.ok(await mobile.$$eval('#row-menu > button',buttons=>buttons.some(button=>button.textContent==='Unpin chat')));
  await mobile.$$eval('#row-menu > button',buttons=>buttons.find(button=>button.textContent==='Unpin chat').click());
  await hold('[data-box-id="'+b+'"]');
  await mobile.click('#row-menu > button');
  assert.equal(await mobile.$eval('[data-box-id="'+b+'"] .chat-row-pin',node=>node.hidden),true,'unpinning keeps the chat in Chats');
  assert.equal(await mobile.$eval('[data-pair-key]',row=>row.textContent.includes('Builder ↔ Reviewer')),true);
  await mobile.close();await desktop.close();
 });
});

test('pins sort first inside Chats, Box conversations, and custom groups without changing membership',async()=>{
 const c='33333333-3333-4333-8333-333333333333',pairAC=a+'/'+c,pairBC=b+'/'+c;
 const initialLayout={exists:true,groups:[{id:'projects',name:'Projects',collapsed:false}],members:{['box:'+a]:'projects',['pair:'+pairKey]:'projects'},pins:['box:'+b,'pair:'+pairKey,'pair:'+pairBC],mutes:{},sections:{}};
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');await page.waitForSelector('[data-pair-key="'+pairBC+'"]');
  const order=()=>page.evaluate(()=>{
   const result={};let section='';
   for(const row of document.querySelectorAll('#chat-entries > li')){
    if(row.dataset.groupId)section=row.dataset.groupId;
    else if(row.dataset.section)section=row.dataset.section;
    else if(row.dataset.boxId||row.dataset.pairKey)(result[section]??=[]).push(row.dataset.boxId?'box:'+row.dataset.boxId:'pair:'+row.dataset.pairKey);
   }
   return result;
  });
  assert.deepEqual(await order(),{projects:['pair:'+pairKey,'box:'+a],boxes:['box:'+b,'box:'+c],pairs:['pair:'+pairBC,'pair:'+pairAC]});
  await page.click('[data-box-id="'+a+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await page.$$eval('#row-menu > button',buttons=>buttons.find(button=>button.textContent==='Pin chat').click());
  assert.deepEqual((await order()).projects,['box:'+a,'pair:'+pairKey]);
  assert.equal(savedLayout().members['box:'+a],'projects','pinning does not move a chat out of its group');
  await page.reload();await page.waitForSelector('[data-pair-key="'+pairBC+'"]');
  assert.deepEqual((await order()).projects,['box:'+a,'pair:'+pairKey],'grouped pins survive reload');
  await page.$eval('[data-box-id="'+c+'"]',row=>{
   const transfer=new DataTransfer();row.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:transfer}));
   const target=document.querySelector('.chat-pin-drop');
   target.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   target.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   row.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:transfer}));
  });
  await page.waitForFunction(id=>document.querySelector('[data-section="boxes"] + li')?.dataset.boxId===id,{},c);
  assert.ok(savedLayout().pins.includes('box:'+c),'desktop drop still pins the chat');
  assert.equal(savedLayout().members['box:'+a],'projects','desktop pinning does not move grouped chats');
  await page.close();
 },{initialLayout,extraBoxes:[{id:c,name:'Alpha',state:'running',defaultAgent:'claude',provider:'railway'}],extraPairs:[{boxAId:a,boxBId:c,boxAName:'Builder',boxBName:'Alpha',lastAt:now,lastText:'A to C'},{boxAId:b,boxBId:c,boxAName:'Reviewer',boxBName:'Alpha',lastAt:now,lastText:'B to C'}]});
});

test('phone hold then move drags a chat into a group without opening its menu; a still hold opens the menu',async()=>{
 const initialLayout={exists:true,groups:[{id:'projects',name:'Projects',collapsed:false}],members:{['box:'+a]:'projects'},pins:[],mutes:{},sections:{}};
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');await page.waitForSelector('[data-box-id="'+b+'"]');
  const captures=process.env.VMBOX_CHAT_PIN_DRAG_CAPTURES;
  if(captures){await mkdir(captures,{recursive:true});await page.screenshot({path:captures+'/01-before.png'})}
  const center=selector=>page.$eval(selector,node=>{const r=node.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}});
  let start=await center('[data-box-id="'+b+'"]');
  await page.touchscreen.touchStart(start.x,start.y);
  await page.touchscreen.touchMove(start.x,start.y-30);
  await page.touchscreen.touchEnd();
  assert.equal(await page.$eval('#row-menu',menu=>menu.hidden),true,'a quick vertical scroll does not open the menu');
  assert.equal(savedLayout().members['box:'+b],undefined,'a quick vertical scroll does not drag the chat');
  start=await center('[data-box-id="'+b+'"]');
  await page.touchscreen.touchStart(start.x,start.y);
  await page.touchscreen.touchMove(start.x-70,start.y);
  await page.touchscreen.touchEnd();
  assert.equal(await page.$eval('#row-menu',menu=>menu.hidden),true,'a quick horizontal swipe does not open the menu');
  assert.equal(savedLayout().members['box:'+b],undefined,'a quick horizontal swipe does not drag the chat');
  start=await center('[data-box-id="'+b+'"]');
  await page.touchscreen.touchStart(start.x,start.y);
  await new Promise(resolve=>setTimeout(resolve,520));
  assert.equal(await page.$eval('#row-menu',menu=>menu.hidden),true,'holding arms a drag without opening the menu');
  await page.touchscreen.touchMove(start.x,start.y-30);
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"]').classList.contains('touch-chat-dragging'),{},b);
  if(captures)await page.screenshot({path:captures+'/02-dragging.png'});
  const target=await center('.chat-folder');
  await page.touchscreen.touchMove(target.x,target.y);
  await page.waitForFunction(()=>document.querySelector('.chat-folder').classList.contains('drop-target'));
  if(captures)await page.screenshot({path:captures+'/03-over-group.png'});
  await page.touchscreen.touchEnd();
  await page.waitForFunction(id=>document.querySelector('.chat-folder + li')?.dataset.boxId===id,{},a);
  assert.equal(savedLayout().members['box:'+b],'projects','touch drop persists group membership');
  assert.equal(await page.$eval('#row-menu',menu=>menu.hidden),true,'drag never opens the row menu');
  if(captures)await page.screenshot({path:captures+'/04-after-drop.png'});
  const still=await center('[data-box-id="'+b+'"]');
  await page.touchscreen.touchStart(still.x,still.y);
  await new Promise(resolve=>setTimeout(resolve,520));
  await page.touchscreen.touchEnd();
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.equal(await page.$eval('#row-menu',menu=>menu.classList.contains('touch-mode')),true);
  if(captures)await page.screenshot({path:captures+'/05-still-hold-menu.png'});
  await page.close();
 },{initialLayout});
});

test('chat groups combine unread indicators, accept dragged chats, and persist collapse state',async()=>{
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});
  await page.goto(base+'/chat');
  await page.waitForSelector('[data-box-id]');
  await page.waitForSelector('[data-pair-key]');
  assert.ok(await page.$('[data-box-id]'),'agent chats and box conversations share the list');
  await page.screenshot({path:'/tmp/vmbox-chat-unified-list-desktop.png'});
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='1',{},a);
  await page.click('[data-box-id="'+a+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.ok(await page.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.some(button=>button.textContent==='Add to Group›')));
  await page.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Add to Group›').click());
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New group…').click());
  await page.waitForSelector('#chat-group-dialog[open]');
  await page.type('#chat-group-name','Projects');
  await page.click('#chat-group-form button[type=submit]');
  await page.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Projects');
  assert.equal(await page.$('.chat-folder .unread'),null,'group dividers have no badges');
  await page.$eval('[data-pair-key]',row=>{
   const transfer=new DataTransfer();
   row.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:transfer}));
   const target=document.querySelector('.chat-folder');
   target.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   target.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   row.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:transfer}));
  });
  await page.waitForFunction(()=>document.querySelector('.chat-folder-toggle')?.getAttribute('aria-label').includes('2 chats'));
  assert.match(await page.$eval('.chat-folder-toggle',node=>node.getAttribute('aria-label')),/2 chats/);
  await page.click('[data-box-id="'+b+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.equal(await page.$$eval('#row-menu > button',buttons=>buttons.filter(button=>button.textContent.startsWith('Move to ')).length),0,'groups stay inside one submenu');
  await page.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Add to Group›').click());
  await page.$$eval('#row-menu .row-submenu button',buttons=>buttons.find(button=>button.textContent==='Projects').click());
  assert.match(await page.$eval('.chat-folder-toggle',node=>node.getAttribute('aria-label')),/3 chats/);
  assert.equal(await page.$('.chat-folder .unread'),null);
  await page.click('.chat-folder-toggle');
  assert.equal(await page.$('[data-box-id="'+a+'"]'),null);
  assert.equal(await page.$('[data-pair-key]'),null);
  assert.equal(await page.$('.chat-folder .unread'),null,'collapsed groups have no unread badge');
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.chat-folder-toggle')?.getAttribute('aria-label').includes('3 chats'));
  assert.equal(await page.$eval('.chat-folder-toggle',button=>button.getAttribute('aria-expanded')),'false');
  await page.type('#chat-filter','Projects');
  await page.waitForSelector('[data-pair-key]');
  assert.ok(await page.$('[data-box-id="'+a+'"]'),'searching a group reveals its members');
  await page.$eval('#chat-filter',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.waitForFunction(()=>!document.querySelector('[data-pair-key]'));
  await page.click('.chat-folder-toggle');
  await page.click('[data-box-id="'+b+'"]',{button:'right'});
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Pin chat').click());
  assert.equal(await page.$eval('.chat-folder + li',node=>node.dataset.boxId),b,'pinned chat sorts first inside its group');
  assert.match(await page.$eval('.chat-folder-toggle',node=>node.getAttribute('aria-label')),/3 chats/,'pinning preserves group membership');
  assert.equal(await page.$('.chat-folder .unread'),null,'pair unread stays on the row only');
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
  await mobile.waitForSelector('[data-box-id]');
  await mobile.screenshot({path:'/tmp/vmbox-chat-unified-list-mobile.png'});
  await mobile.waitForSelector('[data-pair-key]');
  const point=await mobile.$eval('[data-pair-key]',row=>{const rect=row.getBoundingClientRect();return {x:rect.left+rect.width/2,y:rect.top+rect.height/2}});
  await mobile.touchscreen.touchStart(point.x,point.y);
  await new Promise(resolve=>setTimeout(resolve,650));
  await mobile.touchscreen.touchEnd();
  await mobile.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await mobile.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Add to Group›').click());
  await mobile.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New group…').click());
  await mobile.type('#chat-group-name','Mobile');
  await mobile.click('#chat-group-form button[type=submit]');
  await mobile.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Mobile');
  await mobile.click('.chat-folder-toggle');
  assert.equal(await mobile.$('[data-pair-key]'),null);
  assert.equal(await mobile.$('.chat-folder .unread'),null);
  await mobile.close();
 });
});

test('chat groups sync across browser profiles',async()=>{
 await withChat(async(browser,base,savedLayout)=>{
  const first=await browser.createBrowserContext();
  const page=await first.newPage();
  await page.goto(base+'/chat');
  await page.waitForSelector('[data-box-id]');
  // The visible path is the row's Add to Group menu; use its shared dialog
  // trigger here to create an empty group before assigning a chat.
  await page.$eval('#new-chat-group',button=>button.click());
  await page.type('#chat-group-name','Shared projects');
  await page.click('#chat-group-form button[type=submit]');
  await page.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Shared projects');
  await page.waitForFunction(async()=>{
   const response=await fetch('/v1/chat-sidebar-layout');
   return (await response.json()).groups.some(group=>group.name==='Shared projects');
  });
  const second=await browser.createBrowserContext();
  const other=await second.newPage();
  await other.goto(base+'/chat');
  await other.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Shared projects');
  await other.click('[data-box-id="'+a+'"]',{button:'right'});
  await other.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  await other.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Add to Group›').click());
  await other.$$eval('#row-menu .row-submenu button',buttons=>buttons.find(button=>button.textContent==='Shared projects').click());
  await other.waitForFunction(()=>document.querySelector('.chat-folder-toggle')?.getAttribute('aria-label')?.includes('1 chats'));
  await other.waitForFunction(async()=>{
   const response=await fetch('/v1/chat-sidebar-layout');
   return !!(await response.json()).members['box:11111111-1111-4111-8111-111111111111'];
  });
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.chat-folder-toggle')?.getAttribute('aria-label')?.includes('1 chats'));
  assert.equal(savedLayout().members['box:'+a],savedLayout().groups[0].id);
  other.once('dialog',dialog=>dialog.accept());
  await other.click('.chat-folder-menu');
  await other.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Delete group…').click());
  await other.waitForFunction(async()=>{
   const response=await fetch('/v1/chat-sidebar-layout');
   const layout=await response.json();
   return layout.exists&&layout.groups.length===0;
  });
  await page.reload();
  await page.waitForSelector('[data-box-id]');
  assert.equal(await page.$('.chat-folder'),null,'an empty saved layout replaces stale browser groups');
  await first.close();await second.close();
 });
});

test('new boxes can be created into a group or pinned in Chats from the section menus',async()=>{
 const createBehavior={showCreated:false,fail:false};
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});
  await page.goto(base+'/chat');await page.waitForSelector('[data-box-id]');
  await page.$eval('#new-chat-group',button=>button.click());await page.type('#chat-group-name','Projects');await page.click('#chat-group-form button[type=submit]');
  await page.waitForSelector('.chat-folder');
  await page.click('[data-box-id="'+a+'"]',{button:'right'});
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Pin chat').click());
  await page.waitForSelector('[data-section="boxes"]');
  const firstItem=()=>page.$eval('#row-menu > button',button=>button.textContent);
  await page.click('.chat-folder-menu');assert.equal(await firstItem(),'New box here');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  assert.equal(await page.$eval('#new-box-target-label',node=>node.textContent),'Added to: Projects');
  await page.click('#new-box-target-remove');assert.equal(await page.$eval('#new-box-target',node=>node.hidden),true);
  await page.click('#new-box-cancel');
  await page.click('#new-box');assert.equal(await page.$eval('#new-box-target',node=>node.hidden),true);await page.click('#new-box-close');
  await page.click('[data-section="boxes"] .section-menu');assert.equal(await firstItem(),'New box here');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New pinned box here').click());
  assert.equal(await page.$eval('#new-box-target-label',node=>node.textContent),'Added to: Pinned in Chats');
  await page.click('#new-box-cancel');
  await page.click('[data-section="boxes"] .section-menu');assert.equal(await firstItem(),'New box here');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  assert.equal(await page.$eval('#new-box-target-label',node=>node.textContent),'Added to: Boxes');
  await page.click('#new-box-cancel');
  await page.click('[data-section="pairs"] .section-menu');assert.notEqual(await firstItem(),'New box here');
  await page.keyboard.press('Escape');

  await page.click('.chat-folder-toggle');
  await page.click('.chat-folder-menu');await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  await page.type('#create-box input[name=name]','group-created');await page.click('#create-box-submit');
  await page.waitForFunction(()=>document.querySelector('#new-box-modal').hidden);
  await page.waitForFunction(async()=>!!(await (await fetch('/v1/chat-sidebar-layout')).json()).members?.['box:created-1']);
  assert.equal(savedLayout().members['box:created-1'],savedLayout().groups[0].id);
  assert.equal(await page.$eval('.chat-folder-toggle',button=>button.getAttribute('aria-expanded')),'true');
  assert.equal(await page.$('[data-box-id="created-1"]'),null,'membership is saved before the box reaches the list');
  createBehavior.showCreated=true;await page.click('#refresh');
  await page.waitForSelector('[data-box-id="created-1"]');
  const groupRows=await page.$eval('.chat-folder',header=>{const ids=[];for(let row=header.nextElementSibling;row&&!row.matches('.chat-folder,.conversation-divider,.conversation-group');row=row.nextElementSibling)if(row.dataset.boxId)ids.push(row.dataset.boxId);return ids});
  assert.ok(groupRows.includes('created-1'),'new box appears under its group');
  await page.reload();await page.waitForSelector('[data-box-id="created-1"]');
  assert.equal(savedLayout().members['box:created-1'],savedLayout().groups[0].id,'reload retains server-side membership');
  assert.ok((await page.$eval('.chat-folder',header=>{const ids=[];for(let row=header.nextElementSibling;row&&!row.matches('.chat-folder,.conversation-divider,.conversation-group');row=row.nextElementSibling)if(row.dataset.boxId)ids.push(row.dataset.boxId);return ids})).includes('created-1'),'reloaded row stays in its group');

  await page.click('[data-section="boxes"] .section-menu');await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New pinned box here').click());
  await page.type('#create-box input[name=name]','pinned-created');await page.click('#create-box-submit');
  await page.waitForFunction(()=>document.querySelector('#new-box-modal').hidden);
  await page.waitForFunction(async()=>((await (await fetch('/v1/chat-sidebar-layout')).json()).pins||[]).includes('box:created-2'));
  assert.ok(savedLayout().pins.includes('box:created-2'));
  await page.waitForSelector('[data-box-id="created-2"]');
  const chatOrder=await page.$eval('[data-section="boxes"]',header=>{const ids=[];for(let row=header.nextElementSibling;row&&!row.dataset.section;row=row.nextElementSibling)if(row.dataset.boxId)ids.push(row.dataset.boxId);return ids});
  assert.ok(chatOrder.indexOf('created-2')>=0&&chatOrder.indexOf('created-2')<chatOrder.indexOf(b),'new pinned box sorts before unpinned Chats');

  await page.click('[data-section="boxes"] .section-menu');await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  await page.type('#create-box input[name=name]','boxes-created');await page.click('#create-box-submit');
  await page.waitForFunction(()=>document.querySelector('#new-box-modal').hidden);
  await page.waitForSelector('[data-box-id="created-3"]');
  assert.equal(savedLayout().members['box:created-3'],undefined);
  assert.equal(savedLayout().pins.includes('box:created-3'),false);

  createBehavior.fail=true;const before=JSON.stringify(savedLayout());
  await page.$eval('.chat-folder-menu',button=>button.click());await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  await page.type('#create-box input[name=name]','failed-created');await page.click('#create-box-submit');
  await page.waitForFunction(()=>document.querySelector('#new-box-status').textContent.includes('Create failed'));
  assert.equal(JSON.stringify(savedLayout()),before,'failed creation leaves layout unchanged');
  await page.click('#new-box-cancel');
  await page.close();

  const captures=process.env.VMBOX_GROUP_NEW_BOX_CAPTURES;
  if(captures){
   await mkdir(captures,{recursive:true});
   for(const theme of ['light','dark'])for(const width of [390,1440]){
    const shot=await browser.newPage();await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
    await shot.setViewport({width,height:width===390?844:900,deviceScaleFactor:1,isMobile:width===390,hasTouch:width===390});
    await shot.goto(base+'/chat');await shot.waitForSelector('.chat-folder-menu');
    await shot.click('.chat-folder-menu');await shot.screenshot({path:captures+'/menu-'+theme+'-'+width+'.png'});
    await shot.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
    await shot.waitForFunction(()=>!document.querySelector('#new-box-target').hidden);
    await shot.screenshot({path:captures+'/sheet-'+theme+'-'+width+'.png'});
    await shot.close();
   }
  }
 },{createBehavior});
});

test('deleting a group while New box is open clears its destination',async()=>{
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.goto(base+'/chat');await page.waitForSelector('[data-box-id]');
  await page.$eval('#new-chat-group',button=>button.click());await page.type('#chat-group-name','Temporary');await page.click('#chat-group-form button[type=submit]');
  await page.waitForSelector('.chat-folder-menu');await page.click('.chat-folder-menu');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='New box here').click());
  assert.equal(await page.$eval('#new-box-target-label',node=>node.textContent),'Added to: Temporary');
  page.once('dialog',dialog=>dialog.accept());
  await page.$eval('.chat-folder-menu',button=>button.click());
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Delete group…').click());
  await page.waitForFunction(()=>document.querySelector('#new-box-target').hidden);
  await page.type('#create-box input[name=name]','ungrouped-created');await page.click('#create-box-submit');
  await page.waitForFunction(()=>document.querySelector('#new-box-modal').hidden);
  assert.equal(savedLayout().members['box:created-1'],undefined);
  await page.close();
 });
});

test('non-owners cannot create boxes from group or section menus',async()=>{
 const initialLayout={exists:true,groups:[{id:'saved-group',name:'Projects',collapsed:false}],members:{},pins:['box:'+a],mutes:{},sections:{}};
 await withChat(async(browser,base)=>{
  const page=await browser.newPage();await page.goto(base+'/chat');await page.waitForSelector('.chat-folder-menu');
  for(const selector of ['.chat-folder-menu','[data-section="boxes"] .section-menu']){
   await page.$eval(selector,button=>button.click());
   assert.equal(await page.$$eval('#row-menu button',buttons=>buttons.some(button=>button.textContent==='New box here')),false);
   assert.equal(await page.$$eval('#row-menu button',buttons=>buttons.some(button=>button.textContent==='New pinned box here')),false);
   await page.keyboard.press('Escape');
  }
  await page.close();
 },{role:'viewer',initialLayout});
});

test('chat groups migrate from browser storage when the account has no saved layout',async()=>{
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();
  await page.goto(base+'/chat');
  await page.waitForSelector('[data-box-id]');
  await page.evaluate(id=>localStorage.setItem('vmboxChatSidebarGroups:account-a',JSON.stringify({groups:[{id:'legacy-group',name:'Existing work',collapsed:true}],members:{['box:'+id]:'legacy-group'}})),a);
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.chat-folder-name')?.textContent==='Existing work');
  assert.equal(await page.$eval('.chat-folder-toggle',button=>button.getAttribute('aria-expanded')),'false');
  assert.equal(savedLayout().members['box:'+a],'legacy-group');
  assert.equal(savedLayout().groups[0].name,'Existing work');
  await page.close();
 });
});

test('long-press group mute greys unread badges and excludes them from the back count',async()=>{
 const boxMessagesB=[1,2].map(index=>({id:'mute-'+index,direction:'agent',text:'Review '+index,state:'delivered',createdAt:new Date(Date.now()-index*1000).toISOString(),updatedAt:now}));
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');
  await page.waitForSelector('[data-box-id="'+b+'"]');
  await page.$eval('#new-chat-group',button=>button.click());
  await page.type('#chat-group-name','Review');await page.click('#chat-group-form button[type=submit]');
  await page.waitForSelector('.chat-folder');
  await page.$eval('[data-box-id="'+b+'"]',row=>{
   const transfer=new DataTransfer();row.dispatchEvent(new DragEvent('dragstart',{bubbles:true,dataTransfer:transfer}));
   const group=document.querySelector('.chat-folder');group.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:transfer}));group.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:transfer}));
   row.dispatchEvent(new DragEvent('dragend',{bubbles:true,dataTransfer:transfer}));
  });
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='2',{},b);
  assert.equal(await page.$eval('#chat-back-count',node=>node.textContent),'3');
  const point=await page.$eval('.chat-folder',node=>{const r=node.getBoundingClientRect();return {x:r.left+r.width/2,y:r.top+r.height/2}});
  await page.touchscreen.touchStart(point.x,point.y);await new Promise(resolve=>setTimeout(resolve,520));await page.touchscreen.touchEnd();
  await page.waitForFunction(()=>!document.querySelector('#row-menu').hidden);
  assert.equal(await page.$eval('#row-menu',node=>node.classList.contains('touch-mode')),true);
  assert.equal(await page.$eval('#row-menu .row-menu-heading',node=>node.textContent),'Review');
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Mute›').click());
  await page.$$eval('#row-menu .row-submenu button',buttons=>buttons.find(button=>button.textContent==='1 hour').click());
  await page.waitForFunction(()=>document.querySelector('.chat-folder .mute-bell')?.hidden===false);
  assert.equal(await page.$('.chat-folder .unread'),null);
  assert.equal(await page.$eval('[data-box-id="'+b+'"] .unread',node=>node.classList.contains('muted')),true);
  await page.waitForFunction(async()=>!!(await (await fetch('/v1/chat-sidebar-layout')).json()).mutes?.['group:'+document.querySelector('.chat-folder').dataset.groupId]);
  await page.reload();await page.waitForFunction(()=>document.querySelector('.chat-folder .mute-bell')?.hidden===false);
  await page.click('[data-box-id="'+a+'"]');
  await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await page.$eval('#chat-back-count',node=>node.hidden),true);
  assert.ok(new Date(Object.values(savedLayout().mutes)[0]).getTime()>Date.now());
  await page.click('#chat-back');
  await page.$eval('.chat-folder-menu',button=>button.click());
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Unmute').click());
  await page.waitForFunction(()=>document.querySelector('.chat-folder .mute-bell')?.hidden===true);
  await page.click('[data-box-id="'+a+'"]');
  await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await page.$eval('#chat-back-count',node=>node.textContent),'2');
  await page.click('#chat-back');
  await page.$eval('[data-pair-key]',row=>row.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,clientX:80,clientY:300})));
  assert.equal(await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Always muted')?.disabled),true);
  await page.close();
 },{boxMessagesB});
});

test('individual and Chats section mutes persist and suppress navigation alerts',async()=>{
 const boxMessagesB=[{id:'section-reply',direction:'agent',text:'Status ready',state:'delivered',createdAt:now,updatedAt:now}];
 await withChat(async(browser,base,savedLayout)=>{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});await page.goto(base+'/chat#box='+a);
  await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .unread')?.textContent==='1',{},b);
  await page.click('[data-box-id="'+b+'"]',{button:'right'});
  await page.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Mute›').click());
  await page.$$eval('#row-menu .row-submenu button',buttons=>buttons.find(button=>button.textContent==='Always').click());
  assert.equal(await page.$eval('[data-box-id="'+b+'"] .unread',node=>node.classList.contains('muted')),true);
  assert.equal(await page.$eval('#chat-back-count',node=>node.hidden),true);
  assert.equal(await page.$('[data-section="boxes"] .unread'),null,'section totals are absent');
  assert.equal(savedLayout().mutes['box:'+b],null);
  await page.reload();await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .mute-bell')?.hidden===false,{},b);
  await page.click('[data-box-id="'+b+'"]',{button:'right'});
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Unmute').click());
  await page.waitForFunction(()=>document.querySelector('#chat-back-count')?.textContent==='1');
  assert.equal(await page.$('[data-section="boxes"] .unread'),null);
  await page.$eval('[data-section="boxes"] .section-menu',node=>node.click());
  await page.$$eval('#row-menu .row-submenu-toggle',buttons=>buttons.find(button=>button.textContent==='Mute›').click());
  await page.$$eval('#row-menu .row-submenu button',buttons=>buttons.find(button=>button.textContent==='1 week').click());
  assert.equal(await page.$eval('[data-section="boxes"] .mute-bell',node=>node.hidden),false);
  assert.equal(await page.$eval('[data-box-id="'+b+'"] .unread',node=>node.classList.contains('muted')),true);
  assert.equal(await page.$eval('#chat-back-count',node=>node.hidden),true);
  assert.equal(await page.$('[data-section="boxes"] .unread'),null);
  assert.ok(new Date(savedLayout().mutes['section:boxes']).getTime()>Date.now());
  await page.$eval('[data-section="boxes"] .section-menu',node=>node.click());
  await page.$$eval('#row-menu button',buttons=>buttons.find(button=>button.textContent==='Collapse').click());
  assert.equal(await page.$('[data-box-id="'+b+'"]'),null,'collapsed section hides its rows');
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('[data-section="boxes"] .section-toggle')?.getAttribute('aria-expanded')==='false');
  await page.click('[data-section="boxes"] .section-toggle');
  await page.waitForSelector('[data-box-id="'+b+'"]');
  await page.close();
 },{boxMessagesB});
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
