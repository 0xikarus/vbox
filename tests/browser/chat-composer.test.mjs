import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
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

async function withChat(fn,messages=threadMessages,{boxList=boxes,postDelayMs=0}={}){
 const posts=[];
 const server=http.createServer((req,res)=>{
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
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxList));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let body='';req.on('data',chunk=>body+=chunk);
   return req.on('end',()=>{posts.push(JSON.parse(body));setTimeout(()=>res.end(JSON.stringify({message:{state:'delivered'}})),postDelayMs)});
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

test('composer keeps a 16px field radius as it grows and joins the reply preview',async()=>{
 await withChat(async(browser,base)=>{
  for(const width of [390,1000]){
   const p=await browser.newPage();
   await p.setViewport({width,height:844,isMobile:width<600,hasTouch:width<600});
   await p.goto(base+'/chat#box=builder');
   await p.waitForSelector('#chat-messages .msg.user');
   const radius=()=>p.$eval('#chat-composer',element=>getComputedStyle(element).borderTopLeftRadius);
   assert.equal(await radius(),'16px',`${width}px single-line composer`);
   await p.type('#chat-input','first line\nsecond line\nthird line');
   assert.equal(await radius(),'16px',`${width}px multiline composer`);
   await p.$eval('#chat-messages .msg.user .msg-reply',button=>button.click());
   const joined=await p.evaluate(()=>{
    const preview=document.querySelector('#reply-preview');
    const composer=document.querySelector('#chat-composer');
    return {bottomRadius:getComputedStyle(preview).borderBottomLeftRadius,topRadius:getComputedStyle(composer).borderTopLeftRadius,
      gap:Math.round(composer.getBoundingClientRect().top-preview.getBoundingClientRect().bottom)};
   });
   assert.deepEqual(joined,{bottomRadius:'0px',topRadius:'0px',gap:0},`${width}px reply preview joins composer`);
   await p.close();
  }
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

test('a slow send clears the composer and allows the next draft',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();await p.setViewport({width:1000,height:800});await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-input');
  await p.type('#chat-input','First message');await p.click('#send');
  assert.equal(await p.$eval('#chat-input',input=>input.value),'','the first draft clears before the server answers');
  await p.type('#chat-input','Next draft');
  assert.equal(await p.$eval('#send',button=>button.disabled),false,'the next draft can be sent while the first request is pending');
  await p.click('#send');
  assert.equal(await p.$eval('#chat-input',input=>input.value),'','the second send also clears immediately');
  await p.type('#chat-input','Third draft');
  await new Promise(resolve=>setTimeout(resolve,1400));
  assert.equal(await p.$eval('#chat-input',input=>input.value),'Third draft','earlier responses do not erase text typed meanwhile');
  assert.equal(await p.$eval('#send',button=>button.disabled),false);
  assert.deepEqual(posts.map(post=>post.text),['First message','Next draft']);
  await p.close();
 },threadMessages,{postDelayMs:1200});
});

test('a disabled Send button shows why the box cannot accept a message',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();await p.setViewport({width:1000,height:800});await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-input');
  await p.type('#chat-input','Keep this draft');
  assert.equal(await p.$eval('#send',button=>button.disabled),true);
  assert.equal(await p.$eval('#send-blocked-reason',reason=>reason.hidden),false,'the reason is visible below the composer');
  assert.equal(await p.$eval('#send-blocked-reason',reason=>reason.textContent),'Wait for this box to be running before sending');
  await p.keyboard.press('Enter');
  assert.equal(await p.$eval('#chat-input',input=>input.value),'Keep this draft');
  assert.equal(posts.length,0);
  await p.close();
 },threadMessages,{boxList:[{...boxes[0],state:'hibernated'}]});
});

test('Reply uses the main composer without opening the thread sidebar',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();await p.setViewport({width:1000,height:800});await p.goto(base+'/chat#box=builder');await p.waitForSelector('.msg.agent');
  assert.equal(await p.$$eval('#chat-messages .msg',nodes=>nodes.length),2,'the agent reply shown in the chat-list preview must also appear in the open transcript');
  assert.equal(await p.$eval('#chat-messages .msg.agent',node=>node.textContent.includes('verification suite is green')),true);
  assert.equal(await p.$eval('#chat-messages .msg.user + .msg-thread-line',button=>button.textContent),'↳ 1 reply','the thread link is visible below the bubble after the first reply');
  await p.click('.msg.user',{button:'right'});await p.waitForFunction(()=>!document.querySelector('.msg.user .msg-actions-menu').hidden);await p.evaluate(()=>[...document.querySelectorAll('.msg.user .msg-actions-menu button')].find(button=>button.textContent.trim()==='Reply').click());
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'Reply does not open the thread sidebar');
  assert.equal(await p.$eval('#reply-preview',preview=>preview.hidden),false,'the reply target appears above the main composer');
  assert.equal(await p.evaluate(()=>document.activeElement?.id),'chat-input');
  await p.type('#chat-input','Ship it.');await p.click('#send');await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.at(-1).parentMessageId,threadRoot,'the message stays in the selected thread');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'sending the reply does not open the thread sidebar');
  assert.equal(await p.$eval('#reply-preview',preview=>preview.hidden),true,'the reply target clears after sending');
  await p.waitForSelector('#chat-messages .msg.agent .msg-reply');
  await p.$eval('#chat-messages .msg.agent .msg-reply',button=>button.click());
  assert.equal(await p.$eval('#reply-preview-text',preview=>preview.textContent.includes('verification suite is green')),true,'the direct icon chooses that message');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true,'the direct icon leaves the thread closed');
  await p.type('#chat-input','Agreed.');await p.click('#send');await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts.at(-1).parentMessageId,threadMessages[1].id,'replying to a thread member preserves the direct parent');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true);
  await p.click('#chat-messages .msg.user + .msg-thread-line');
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
  await new Promise(resolve=>setTimeout(resolve,350));
  const mobile=await p.evaluate(()=>{const header=document.querySelector('#chat-header').getBoundingClientRect(),back=document.querySelector('#chat-back').getBoundingClientRect(),bubble=document.querySelector('#chat-messages .msg.user').getBoundingClientRect(),thread=document.querySelector('#chat-messages .msg.user + .msg-thread-line').getBoundingClientRect();return {header:header.toJSON(),back:back.toJSON(),bubble:bubble.toJSON(),thread:thread.toJSON()}});
  assert.equal(Math.round(mobile.back.left-mobile.header.left),12,'the back tap target starts at the header gutter');
  assert.ok(mobile.back.width>=44&&mobile.back.height>=44,'the back target is at least 44px: '+JSON.stringify(mobile.back));
  assert.ok(mobile.thread.top>=mobile.bubble.bottom,'the thread link sits outside and below its bubble');
  await p.$eval('#chat-messages .msg.user .msg-reply',button=>button.click());
  assert.equal(await p.$eval('#reply-preview',preview=>preview.hidden),false,'the direct icon also works on mobile');
  assert.equal(await p.$eval('#thread-panel',panel=>panel.hidden),true);
  await p.click('#chat-messages .msg.user + .msg-thread-line');
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
  await p.click('.msg + .msg-thread-line');await p.waitForFunction(()=>!document.querySelector('#thread-panel').hidden&&document.querySelectorAll('#thread-messages .msg').length===4);
  await p.screenshot({path:'docs/chat-ui/screenshots/agent-to-agent-thread.png'});await p.close();
 },agentThreadMessages);
});

test('message actions open toward available space and stay inside the transcript',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:390,height:520});await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-messages .msg.user .msg-more');
  const corners=await p.$eval('#chat-messages .msg.user',message=>({message:message.getBoundingClientRect().toJSON(),text:message.querySelector('.text').getBoundingClientRect().toJSON(),actions:message.querySelector('.msg-actions').getBoundingClientRect().toJSON(),reply:message.querySelector('.msg-reply').getBoundingClientRect().toJSON(),chevron:message.querySelector('.msg-more').getBoundingClientRect().toJSON()}));
  assert.ok(corners.chevron.top-corners.message.top<8,'the chevron is at the top of the message');
  assert.ok(corners.actions.right<=corners.text.left+1,'the floating pill is outside the outgoing text');
  assert.ok(corners.reply.top-corners.message.top<8&&corners.reply.right<corners.chevron.left,'the direct reply icon is beside the chevron');
  await p.evaluate(()=>{
   const messages=document.querySelector('#chat-messages');
   for(const edge of ['before','after']){
    const spacer=document.createElement('div');spacer.style.cssText='height:420px;flex:none';
    if(edge==='before')messages.prepend(spacer);else messages.append(spacer);
   }
  });
  async function check(block,expected){
   await p.$eval('#chat-messages .msg.user .msg-more',(toggle,position)=>toggle.scrollIntoView({block:position}),block);
   await p.focus('#chat-messages .msg.user .msg-more');
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

test('touch swipe left replies to the chosen message, while right and vertical swipes do not',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-messages .msg.agent');
  const client=await p.createCDPSession();
  async function swipe(selector,dx,dy,atEnd,steps=5,trace=false){
   const rect=await p.$eval(selector,element=>element.querySelector('.text').getBoundingClientRect().toJSON());
   const x=Math.round(rect.right-35),y=Math.round(rect.top+Math.min(rect.height/2,22));
   await client.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y,id:1}]});
   if(steps===12&&!trace)await p.evaluate(()=>{window.__swipeFrames=[];window.__trackSwipeFrames=true;const tick=time=>{if(!window.__trackSwipeFrames)return;window.__swipeFrames.push(time);requestAnimationFrame(tick)};requestAnimationFrame(tick)});
   let traceComplete;
   if(trace){traceComplete=new Promise(resolve=>client.once('Tracing.tracingComplete',resolve));await client.send('Tracing.start',{categories:'devtools.timeline',transferMode:'ReturnAsStream'})}
   for(let step=1;step<=steps;step++){
    await client.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:Math.round(x+dx*step/steps),y:Math.round(y+dy*step/steps),id:1}]});
    await new Promise(resolve=>setTimeout(resolve,16));
   }
   if(steps===12&&!trace){
    const gaps=await p.evaluate(()=>{window.__trackSwipeFrames=false;return window.__swipeFrames.slice(1).map((time,index)=>time-window.__swipeFrames[index])});
    const maxGap=Math.max(...gaps);console.log('12-step reply swipe max rAF gap:',maxGap.toFixed(1),'ms');
    // Headless Chromium can miss a scheduler tick when the full test batch is
    // busy. The strict local profile enforces the 20ms target.
    const limit=process.env.VMBOX_STRICT_GESTURE_FRAMES==='1'?20:34;
    assert.ok(gaps.length>=10&&maxGap<=limit,`12-step swipe frame gap ${maxGap.toFixed(1)}ms exceeds ${limit}ms`);
   }
   if(trace){
    await client.send('Tracing.end');const {stream}=await traceComplete;let data='';
    for(;;){const chunk=await client.send('IO.read',{handle:stream});data+=chunk.base64Encoded?Buffer.from(chunk.data,'base64').toString():chunk.data;if(chunk.eof)break}
    await client.send('IO.close',{handle:stream});
    const layouts=JSON.parse(data).traceEvents.filter(event=>event.name==='Layout'&&event.ph==='X');
    console.log('12-step reply swipe Layout events:',layouts.length);
    assert.equal(layouts.length,0,'compositor-only swipe should not request Layout');
   }
   if(atEnd)await atEnd();
   await client.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  }
  await swipe('#chat-messages .msg.user',-35,0);
  assert.equal(await p.$eval('#reply-preview',node=>node.hidden),true,'a short swipe leaves the composer alone');
  await swipe('#chat-messages .msg.agent',-115,0,async()=>{
   const state=await p.$eval('#chat-messages .msg.agent',node=>({shift:new DOMMatrix(getComputedStyle(node).transform).m41,hint:Number(getComputedStyle(node.querySelector('.swipe-reply-hint')).opacity)}));
   assert.ok(state.shift<=-64&&state.shift>=-72,`the bubble follows the finger left with resistance (${state.shift}px)`);
   assert.ok(state.hint>.8,'the reply glyph appears behind the bubble on the right');
  },12);
  await p.waitForFunction(()=>!document.querySelector('#reply-preview').hidden);
  assert.match(await p.$eval('#reply-preview-text',node=>node.textContent),/verification suite is green/,'the reply targets the swiped message');
  assert.equal(await p.evaluate(()=>document.activeElement?.id),'chat-input','the existing reply action focuses the composer');
  await p.click('#reply-cancel');
  await swipe('#chat-messages .msg.agent',-115,0,null,12,true);
  await p.waitForFunction(()=>!document.querySelector('#reply-preview').hidden);
  await p.click('#reply-cancel');
  await swipe('#chat-messages .msg.agent',55,0);
  assert.equal(await p.$eval('#reply-preview',node=>node.hidden),true,'a right swipe never replies');
  await p.evaluate(()=>{const messages=document.querySelector('#chat-messages');messages.prepend(Object.assign(document.createElement('div'),{style:'height:440px;flex:none'}));messages.append(Object.assign(document.createElement('div'),{style:'height:440px;flex:none'}));document.querySelector('.msg.user').scrollIntoView({block:'center'})});
  const before=await p.$eval('#chat-messages',node=>node.scrollTop);
  await swipe('#chat-messages .msg.user',3,-110);
  await new Promise(resolve=>setTimeout(resolve,200));
  const after=await p.$eval('#chat-messages',node=>node.scrollTop);
  assert.ok(after>before+25,`vertical gesture scrolls the transcript (${before} → ${after})`);
  assert.equal(await p.$eval('#reply-preview',node=>node.hidden),true,'vertical scroll does not start a reply');
  await p.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await p.$eval('#chat-messages .msg.agent',node=>node.scrollIntoView({block:'center'}));
  await swipe('#chat-messages .msg.agent',-100,0,async()=>{
   assert.equal(await p.$eval('#chat-messages .msg.agent',node=>new DOMMatrix(getComputedStyle(node).transform).m41),0,'reduced motion detects the swipe without moving the bubble');
  });
  await p.waitForFunction(()=>!document.querySelector('#reply-preview').hidden);
  await client.detach();await p.close();
 });
});

test('a rightward transcript swipe reveals the list and returns to it only past the threshold',async()=>{
 await withChat(async(browser,base)=>{
  const p=await browser.newPage();await p.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await p.goto(base+'/chat#box=builder');await p.waitForSelector('#chat-messages .msg.agent');
  const client=await p.createCDPSession();
  async function drag(dx,hold=0,during=async()=>{},selector=''){
   const rect=await p.$eval(selector||'#chat-messages',element=>element.getBoundingClientRect().toJSON());
   const x=selector?Math.round(rect.left+40):160,y=selector?Math.round(rect.top+rect.height/2):Math.round(rect.top+Math.min(160,rect.height/2));
   await client.send('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[{x,y,id:1}]});
   for(let step=1;step<=6;step++){
    await client.send('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:x+Math.round(dx*step/6),y,id:1}]});
    await new Promise(resolve=>setTimeout(resolve,20));
   }
   await during();
   if(hold)await new Promise(resolve=>setTimeout(resolve,hold));
   await client.send('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
  }
  await drag(42,180);
  await new Promise(resolve=>setTimeout(resolve,320));
  assert.equal(await p.$eval('#chat-app',node=>node.classList.contains('in-chat')),true,'short swipe returns to chat');
  await p.evaluate(()=>{
   const pre=document.createElement('pre');pre.className='back-scroll-probe';pre.textContent='long-code-line-'.repeat(35);
   pre.style.cssText='width:180px;overflow-x:auto;white-space:pre;touch-action:auto';
   document.querySelector('#chat-messages').append(pre);pre.scrollIntoView({block:'center'});pre.scrollLeft=80;
  });
  await drag(95,0,async()=>{
   assert.equal(await p.$eval('#chat-app',node=>node.classList.contains('back-swiping')),false,'a code block scrolled away from its left edge keeps the gesture');
  },'.back-scroll-probe');
  assert.equal(await p.$eval('#chat-app',node=>node.classList.contains('in-chat')),true);
  await drag(150,0,async()=>{
   const state=await p.evaluate(()=>({main:new DOMMatrix(getComputedStyle(document.querySelector('#chat-main')).transform).m41,list:new DOMMatrix(getComputedStyle(document.querySelector('#chat-list')).transform).m41}));
   assert.ok(state.main>=100,'chat follows the finger and reveals the list');
   assert.ok(state.list<0,'list enters with parallax');
  });
  await p.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  assert.equal(await p.evaluate(()=>location.hash),'','back swipe uses the header back action');
  await p.click('[data-box-id="builder"] .chat-meta');
  await p.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'));
  await p.emulateMediaFeatures([{name:'prefers-reduced-motion',value:'reduce'}]);
  await drag(150,0,async()=>{
   assert.equal(await p.$eval('#chat-main',node=>new DOMMatrix(getComputedStyle(node).transform).m41),0,'reduced motion keeps the chat still while detecting the threshold');
  });
  await p.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await client.detach();await p.close();
 });
});
