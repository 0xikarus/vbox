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

test('reply quotes preserve parent identity and open the thread panel',async()=>{
 await withChat(async(browser,base,posts)=>{
  const p=await browser.newPage();await p.setViewport({width:1000,height:800});await p.goto(base+'/chat#box=builder');await p.waitForSelector('.msg.agent .msg-parent');
  await p.click('.msg.user .msg-more');await p.evaluate(()=>[...document.querySelectorAll('.msg.user .msg-actions-menu button')].find(button=>button.textContent==='Reply').click());
  assert.equal(await p.$eval('#reply-preview',element=>element.hidden),false);await p.type('#chat-input','Ship it.');await p.keyboard.press('Enter');await p.waitForFunction(()=>document.querySelector('#chat-input').value==='');assert.equal(posts.at(-1).parentMessageId,threadRoot);
  await p.click('.msg.agent .msg-parent');await p.waitForFunction(()=>!document.querySelector('#thread-panel').hidden&&document.querySelectorAll('#thread-messages .msg').length===2);
  await (await p.$('#thread-panel')).screenshot({path:'docs/chat-ui/screenshots/chat-thread.png'});await p.close();
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
