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

const boxes=[
 {id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'railway',volumeName:'v1'},
 {id:'research',name:'Research',state:'running',defaultAgent:'codex',provider:'railway',volumeName:'v2'}
];
const threadRoot='11111111-1111-4111-8111-111111111111';
const threadMessages=[
 {id:threadRoot,taskId:'task-1',direction:'user',text:'Should we ship the migration today?',state:'delivered',threadId:threadRoot,createdAt:'2026-09-21T02:00:00Z',updatedAt:'2026-09-21T02:00:00Z'},
 {id:'22222222-2222-4222-8222-222222222222',taskId:'task-1',direction:'agent',text:'Yes — the verification suite is green.',state:'delivered',parentMessageId:threadRoot,threadId:threadRoot,createdAt:'2026-09-21T02:01:00Z',updatedAt:'2026-09-21T02:01:00Z'}
];
const agentThreadRoot='33333333-3333-4333-8333-333333333333';
const agentThreadMessages=[
 {id:agentThreadRoot,taskId:'task-agent',direction:'box',senderBoxId:'research',text:'I found three provider options. Can you compare their retry guarantees before we choose one?',state:'delivered',threadId:agentThreadRoot,createdAt:'2026-09-21T10:00:00Z',updatedAt:'2026-09-21T10:00:00Z'},
 {id:'44444444-4444-4444-8444-444444444444',taskId:'task-agent',direction:'agent',text:'Yes. I’ll compare idempotency, webhook redelivery, and failure recovery, then recommend the safest integration.',state:'delivered',parentMessageId:agentThreadRoot,threadId:agentThreadRoot,createdAt:'2026-09-21T10:01:00Z',updatedAt:'2026-09-21T10:01:00Z'},
 {id:'55555555-5555-4555-8555-555555555555',taskId:'task-agent',direction:'box',senderBoxId:'research',text:'Please prioritize providers that keep a stable request ID across retries.',state:'delivered',parentMessageId:'44444444-4444-4444-8444-444444444444',threadId:agentThreadRoot,createdAt:'2026-09-21T10:02:00Z',updatedAt:'2026-09-21T10:02:00Z'},
 {id:'66666666-6666-4666-8666-666666666666',taskId:'task-agent',direction:'agent',text:'Understood. Stable provider-side idempotency will be a hard requirement in the recommendation.',state:'delivered',parentMessageId:'55555555-5555-4555-8555-555555555555',threadId:agentThreadRoot,createdAt:'2026-09-21T10:03:00Z',updatedAt:'2026-09-21T10:03:00Z'}
];

async function withChat(fn,messages=threadMessages){
 const posts=[];
 const server=http.createServer((req,res)=>{
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
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let body='';req.on('data',chunk=>body+=chunk);
   return req.on('end',()=>{posts.push(JSON.parse(body));res.end(JSON.stringify({message:{state:'delivered'}}))});
  }
  if(path.endsWith('/messages'))return res.end(JSON.stringify(messages));
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{await fn(browser,'http://127.0.0.1:'+server.address().port,posts)}
 finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
}

// The composer is a textarea. On a phone the soft keyboard's Enter is how you
// start a new line, so it must not submit the message; opening a chat must not
// pop the keyboard either.
test('mobile Enter inserts a newline and opening a chat does not focus the composer',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();
  await p.setViewport({width:390,height:844,deviceScaleFactor:2,isMobile:true,hasTouch:true});
  await p.goto(base+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  assert.notEqual(await p.evaluate(()=>document.activeElement?.id),'chat-input','opening a chat must not focus the composer');
  await p.type('#chat-input','first line');
  await p.keyboard.press('Enter');
  await p.type('#chat-input','second line');
  assert.equal(await p.$eval('#chat-input',el=>el.value),'first line\nsecond line','mobile Enter must insert a newline');
  await new Promise(resolve=>setTimeout(resolve,250));
  assert.equal(posts.length,0,'mobile Enter must not send the message');
  await p.click('#send');
  await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.length,1,'the Send button still sends on mobile');
  assert.equal(posts[0].text,'first line\nsecond line');
  await new Promise(resolve=>setTimeout(resolve,100));
  assert.equal(await p.$eval('#send',button=>button.disabled),true,'Send must return to disabled after the composer clears');
  await p.close();
 });
});

// A hardware keyboard keeps Enter-to-send and Shift+Enter for a newline.
test('desktop Enter sends and Shift+Enter inserts a newline',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();
  await p.setViewport({width:1000,height:800});
  await p.goto(base+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await p.type('#chat-input','hello');
  await p.keyboard.down('Shift');await p.keyboard.press('Enter');await p.keyboard.up('Shift');
  assert.equal(await p.$eval('#chat-input',el=>el.value),'hello\n','Shift+Enter must insert a newline on desktop');
  assert.equal(posts.length,0);
  await p.type('#chat-input','world');
  await p.keyboard.press('Enter');
  await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.length,1,'desktop Enter must send');
  assert.equal(posts[0].text,'hello\nworld');
  await new Promise(resolve=>setTimeout(resolve,100));
  assert.equal(await p.$eval('#send',button=>button.disabled),true,'Send must return to disabled after the composer clears');
  await p.close();
 });
});

test('Reply uses the main composer without opening the thread sidebar',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();await p.setViewport({width:1000,height:800});await p.goto(base+'/chat#box=builder');await p.waitForSelector('.msg.agent');
  assert.equal(await p.$$eval('#chat-messages .msg',nodes=>nodes.length),2,'the agent reply shown in the chat-list preview must also appear in the open transcript');
  assert.equal(await p.$eval('#chat-messages .msg.agent',node=>node.textContent.includes('verification suite is green')),true);
  assert.equal(await p.$eval('#chat-messages .msg.user .msg-thread',button=>button.textContent),'2 in thread','the thread link is visible after the first reply');
  await p.click('.msg.user',{button:'right'});await p.waitForFunction(()=>!document.querySelector('.msg.user .msg-actions-menu').hidden);await p.evaluate(()=>[...document.querySelectorAll('.msg.user .msg-actions-menu button')].find(button=>button.textContent.trim()==='Reply').click());
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'Reply does not open the thread sidebar');
  assert.equal(await p.$eval('#reply-preview',preview=>preview.hidden),false,'the reply target appears above the main composer');
  assert.equal(await p.evaluate(()=>document.activeElement?.id),'chat-input');
  await p.type('#chat-input','Ship it.');await p.click('#send');await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.at(-1).parentMessageId,threadRoot,'the message stays in the selected thread');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'sending the reply does not open the thread sidebar');
  assert.equal(await p.$eval('#reply-preview',preview=>preview.hidden),true,'the reply target clears after sending');
  await p.click('.msg.agent',{button:'right'});await p.waitForFunction(()=>!document.querySelector('.msg.agent .msg-actions-menu').hidden);
  await p.evaluate(()=>[...document.querySelectorAll('.msg.agent .msg-actions-menu button')].find(button=>button.textContent.trim()==='Reply').click());
  await p.type('#chat-input','Agreed.');await p.click('#send');await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.at(-1).parentMessageId,threadMessages[1].id,'replying to a thread member preserves the direct parent');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true);
  await p.click('#chat-messages .msg.user .msg-thread');
  await p.waitForFunction(()=>!document.querySelector('#thread-panel').hidden&&document.querySelectorAll('#thread-messages .msg').length===2);
  const initialWidth=await p.$eval('#thread-panel',panel=>panel.getBoundingClientRect().width);
  assert.ok(initialWidth>=400,'the thread sidebar starts at its intended desktop width');
  const handle=await p.$eval('#thread-resizer',element=>element.getBoundingClientRect().toJSON());
  await p.mouse.move(handle.x+handle.width/2,handle.y+handle.height/2);await p.mouse.down();await p.mouse.move(handle.x+handle.width/2-72,handle.y+handle.height/2,{steps:5});await p.mouse.up();
  const draggedWidth=await p.$eval('#thread-panel',panel=>panel.getBoundingClientRect().width);
  assert.ok(draggedWidth>=initialWidth+60,'dragging the thread edge left widens the sidebar');
  await p.focus('#thread-resizer');await p.keyboard.press('ArrowRight');
  const keyboardWidth=await p.$eval('#thread-panel',panel=>panel.getBoundingClientRect().width);
  assert.ok(keyboardWidth<draggedWidth,'the thread separator supports keyboard resizing');
  assert.equal(await p.evaluate(()=>Number(localStorage.getItem('vmboxChatThreadWidth'))),keyboardWidth,'thread width is remembered');
  await p.evaluate(()=>document.activeElement.blur());
  await p.screenshot({path:'/tmp/vmbox-chat-thread-resized.png'});
  await p.type('#thread-composer textarea','One more thing.');await p.click('#thread-composer button');await p.waitForFunction(()=>document.querySelector('#thread-composer textarea').value==='');assert.equal(posts.at(-1).parentMessageId,threadRoot);
  await (await p.$('#thread-panel')).screenshot({path:'docs/chat-ui/screenshots/chat-thread.png'});await p.close();
 });
});

test('thread sidebar fits a phone without a resize handle',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-messages .msg.user');
  await p.click('.msg.user',{button:'right'});
  await p.waitForFunction(()=>!document.querySelector('.msg.user .msg-actions-menu').hidden);
  await p.evaluate(()=>[...document.querySelectorAll('.msg.user .msg-actions-menu button')].find(button=>button.textContent.trim()==='Reply').click());
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'Reply keeps the mobile chat visible');
  await p.click('#chat-messages .msg.user .msg-thread');
  await p.waitForFunction(()=>!document.querySelector('#thread-panel').hidden);
  await p.waitForFunction(()=>{const rect=document.querySelector('#thread-panel').getBoundingClientRect();return Math.abs(rect.left)<1&&Math.abs(rect.right-390)<1});
  const layout=await p.evaluate(()=>({panel:document.querySelector('#thread-panel').getBoundingClientRect().toJSON(),handle:getComputedStyle(document.querySelector('#thread-resizer')).display}));
  assert.ok(Math.abs(layout.panel.left)<1&&Math.abs(layout.panel.right-390)<1,'the thread sidebar fills the phone viewport: '+JSON.stringify(layout.panel));
  assert.equal(layout.handle,'none','the desktop resize handle is hidden on phones');
  await p.evaluate(()=>document.activeElement.blur());
  await p.screenshot({path:'/tmp/vmbox-chat-thread-mobile.png'});await p.close();
 });
});

test('agent-to-agent messages identify their source box and preserve the thread',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:1180,height:820,deviceScaleFactor:1});await p.goto(base+'/chat#box=builder');await p.waitForSelector('.msg .agent-origin');
  assert.equal(await p.$eval('.msg .agent-origin',element=>element.textContent),'From Research');
  await p.screenshot({path:'docs/chat-ui/screenshots/agent-to-agent-conversation.png'});
  await p.click('.msg-thread');await p.waitForFunction(()=>!document.querySelector('#thread-panel').hidden&&document.querySelectorAll('#thread-messages .msg').length===4);
  await p.screenshot({path:'docs/chat-ui/screenshots/agent-to-agent-thread.png'});await p.close();
 },agentThreadMessages);
});

test('message actions open toward available space and stay inside the transcript',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:390,height:520});await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-messages .msg.user .msg-more');
  const corners=await p.$eval('#chat-messages .msg.user',message=>({message:message.getBoundingClientRect().toJSON(),chevron:message.querySelector('.msg-more').getBoundingClientRect().toJSON()}));
  assert.ok(corners.chevron.top-corners.message.top<8,'the chevron is at the top of the message');
  assert.ok(corners.message.right-corners.chevron.right<8,'the chevron is at the right edge of the message');
  await p.evaluate(()=>{
   const messages=document.querySelector('#chat-messages');
   for(const edge of ['before','after']){
    const spacer=document.createElement('div');spacer.style.cssText='height:420px;flex:none';
    if(edge==='before')messages.prepend(spacer);else messages.append(spacer);
   }
  });
  async function check(block,expected){
   await p.$eval('#chat-messages .msg.user .msg-more',(toggle,position)=>toggle.scrollIntoView({block:position}),block);
   await p.click('#chat-messages .msg.user .msg-more');
   const result=await p.$eval('#chat-messages .msg.user .msg-actions-menu',menu=>{
    const rect=menu.getBoundingClientRect(),clip=document.querySelector('#chat-messages').getBoundingClientRect();
    return {placement:menu.dataset.placement,top:rect.top,bottom:rect.bottom,left:rect.left,right:rect.right,clipTop:clip.top,clipBottom:clip.bottom,clipLeft:clip.left,clipRight:clip.right};
   });
   assert.equal(result.placement,expected);
   assert.ok(result.top>=result.clipTop-1&&result.bottom<=result.clipBottom+1,'menu must fit vertically inside the transcript: '+JSON.stringify(result));
   assert.ok(result.left>=result.clipLeft-1&&result.right<=result.clipRight+1,'menu must fit horizontally inside the transcript: '+JSON.stringify(result));
   await p.click('#chat-messages .msg.user .msg-more');
  }
  await check('end','up');
  await check('start','down');
  await p.$eval('#chat-messages .msg.user',message=>message.scrollIntoView({block:'center'}));
  const target=await p.$eval('#chat-messages .msg.user .text',text=>text.getBoundingClientRect().toJSON());
  const click={x:Math.round(target.left+Math.min(65,target.width/2)),y:Math.round(target.top+target.height/2)};
  await p.mouse.click(click.x,click.y,{button:'right'});
  const context=await p.$eval('#chat-messages .msg.user .msg-actions-menu',menu=>({hidden:menu.hidden,placement:menu.dataset.placement,rect:menu.getBoundingClientRect().toJSON()}));
  assert.equal(context.hidden,false,'right-click opens the message actions');
  assert.ok(Math.abs(context.rect.left-click.x)<5,'the menu opens at the horizontal click position');
  assert.ok(context.placement==='up'?Math.abs(context.rect.bottom-click.y)<5:Math.abs(context.rect.top-click.y)<5,'the menu opens beside the vertical click position');
  await p.screenshot({path:'/tmp/vmbox-chat-message-context-menu.png'});
  await p.close();
 });
});
