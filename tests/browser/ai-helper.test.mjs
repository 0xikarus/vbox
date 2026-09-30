import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const files=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-c.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css'];
const assets=Object.fromEntries(await Promise.all(files.map(async name=>[name,await readFile(root+name)])));
const requests=[];
const sentMessages=[];
let uploaded=0;
let aiSetting={configured:false,model:'openrouter/auto'};
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://local').pathname;
 if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
 if(path.slice(1) in assets){res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':'text/css');return res.end(assets[path.slice(1)])}
 res.setHeader('Content-Type','application/json');
 if(path==='/v1/ai/openrouter'){
  if(req.method==='PUT'){let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);aiSetting={configured:true,key:body.key,model:body.model};return res.end(JSON.stringify(aiSetting))}
  if(req.method==='DELETE'){aiSetting={configured:false,model:'openrouter/auto'};res.statusCode=204;return res.end()}
  return res.end(JSON.stringify(aiSetting));
 }
 if(path==='/v1/ai/openrouter/profiles/saved')return res.end(JSON.stringify({key:'sk-or-v1-imported-test-key',model:'openrouter/auto'}));
 if(path==='/v1/ai/models')return res.end(JSON.stringify({source:'OpenRouter live catalog',models:[{id:'openrouter/auto',label:'Automatic'},{id:'openrouter/anthropic/claude-test',label:'Claude Test'},{id:'openrouter/google/test-model',label:'Google Test'}]}));
 if(path==='/v1/login-profiles')return res.end(JSON.stringify([{application:'opencode',name:'saved',model:'openrouter/auto'}]));
 if(path==='/v1/ai/rewrite'){
  let raw='';for await(const chunk of req)raw+=chunk;
  const body=JSON.parse(raw);requests.push(body);
  return res.end(JSON.stringify({text:body.text.replaceAll('Helo','Hello').replaceAll('wrold','world')}));
 }
 if(path==='/v1/run-once-images'&&req.method==='POST')return res.end(JSON.stringify({id:'uploaded-'+(++uploaded)}));
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'railway',volumeName:'v1'};
 const values={'/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],'/v1/box-conversations':[],'/v1/profile-usage':[],'/v1/chat-commands':[{name:'polish',prompt:'Helo wrold'}],'/v1/tool-presets':[],'/v1/instruction-presets':{defaultName:'',presets:[]}};
 if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
 if(path.endsWith('/messages')){
  if(req.method==='POST'){let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);sentMessages.push(body);return res.end(JSON.stringify({message:{id:'sent-'+sentMessages.length,state:'complete'}}))}
  return res.end('[]');
 }
 return res.end(JSON.stringify(values[path]??{}));
});
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
const base='http://127.0.0.1:'+server.address().port;
const screenshotDir=process.env.VMBOX_AI_SCREENSHOTS;
if(screenshotDir)await mkdir(screenshotDir,{recursive:true});
try{
 await test('owner can configure and remove the dedicated key from chat settings',async()=>{
  const page=await browser.newPage();await page.setViewport({width:1320,height:850});
  await page.goto(base+'/chat');await page.waitForSelector('#ai-settings-toggle:not([hidden])');
  await page.click('#chat-menu');await page.click('#ai-settings-toggle');
  await page.waitForSelector('.ai-settings-dialog[open]');
  await page.waitForFunction(()=>!document.querySelector('.ai-settings-state').textContent.includes('Loading setting'));
  assert.match(await page.$eval('.ai-settings-state',el=>el.textContent),/No dedicated key/);
  await page.type('.ai-settings-dialog input[name="key"]','sk-or-v1-synthetic-test-key');
  await page.$eval('.ai-settings-dialog input[name="model"]',el=>el.value='anthropic/claude-test');
  await page.click('.ai-settings-save');
  await page.waitForFunction(()=>document.querySelector('.ai-settings-state').textContent.includes('Key saved'));
  assert.equal(aiSetting.model,'anthropic/claude-test');
  assert.equal(await page.$eval('.ai-settings-dialog input[name="key"]',el=>el.value),'sk-or-v1-synthetic-test-key');
  await page.click('.ai-key-visibility');
  assert.equal(await page.$eval('.ai-settings-dialog input[name="key"]',el=>el.type),'text');
  await page.$eval('.ai-settings-dialog input[name="model"]',el=>el.value='openrouter/auto');
  await page.click('.ai-settings-save');
  await page.waitForFunction(()=>document.querySelector('.ai-settings-state').textContent.includes('Key saved'));
  assert.equal(aiSetting.model,'openrouter/auto');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-settings-desktop.png'});
  page.once('dialog',dialog=>dialog.accept());await page.click('.ai-settings-remove');
  await page.waitForFunction(()=>document.querySelector('.ai-settings-state').textContent.includes('No dedicated key'));
  assert.equal(aiSetting.configured,false);
  await page.select('.ai-import-label select','saved');
  await page.waitForFunction(()=>document.querySelector('.ai-settings-dialog input[name="key"]').value==='sk-or-v1-imported-test-key');
  await page.click('.ai-settings-dialog .ai-model-row button');
  await page.waitForSelector('.ai-model-dialog[open] .ai-model-option[data-model="openrouter/google/test-model"]');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-model-picker-desktop.png'});
  await page.click('.ai-model-dialog .ai-model-option[data-model="openrouter/google/test-model"]');
  assert.equal(await page.$eval('.ai-settings-dialog input[name="model"]',el=>el.value),'openrouter/google/test-model');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-import-desktop.png'});
  await page.click('.ai-settings-save');
  await page.waitForFunction(()=>document.querySelector('.ai-settings-state').textContent.includes('Key saved'));
  assert.equal(aiSetting.model,'openrouter/google/test-model');
  assert.equal(aiSetting.key,'sk-or-v1-imported-test-key');
  await page.close();
 });
 await test('AI key settings fit on mobile',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');await page.waitForSelector('#ai-settings-toggle:not([hidden])');
  await page.click('#chat-menu');await page.click('#ai-settings-toggle');
  await page.waitForSelector('.ai-settings-dialog[open]');
  const geometry=await page.$eval('.ai-settings-dialog',el=>({right:el.getBoundingClientRect().right,width:el.getBoundingClientRect().width}));
  assert.ok(geometry.right<=391&&geometry.width>=300,JSON.stringify(geometry));
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-settings-mobile.png'});
  await page.click('.ai-settings-dialog .ai-model-row button');
  await page.waitForSelector('.ai-model-dialog[open] .ai-model-option[data-model="openrouter/google/test-model"]');
  const pickerBounds=await page.$eval('.ai-model-dialog',el=>({left:el.getBoundingClientRect().left,right:el.getBoundingClientRect().right,bottom:el.getBoundingClientRect().bottom}));
  assert.ok(pickerBounds.left>=0&&pickerBounds.right<=391&&pickerBounds.bottom<=845,JSON.stringify(pickerBounds));
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-model-picker-mobile.png'});
  await page.type('.ai-model-search','Google');
  assert.equal(await page.$$('.ai-model-option[data-model="openrouter/anthropic/claude-test"]').then(items=>items.length),0);
  await page.click('.ai-model-dialog .ai-model-option[data-model="openrouter/google/test-model"]');
  assert.equal(await page.$eval('.ai-settings-dialog input[name="model"]',el=>el.value),'openrouter/google/test-model');
  await page.click('.ai-settings-dialog .ai-model-row button');
  await page.type('.ai-model-custom input','openrouter/example/custom');
  await page.click('.ai-model-custom button');
  assert.equal(await page.$eval('.ai-settings-dialog input[name="model"]',el=>el.value),'openrouter/example/custom');
  await page.close();
 });
 await test('chat wand rewrites only the draft and long press edits its prompt',async()=>{
  const page=await browser.newPage();await page.setViewport({width:1320,height:850});
  await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden && !!document.querySelector('#chat-composer .ai-wand'));
  await page.type('#chat-input','Helo wrold');
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello world');
  assert.equal(requests.at(-1).kind,'chat');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'Hello world');
  await page.$eval('#chat-input',el=>{el.value='Helo again';el.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.hover('#chat-composer .ai-wand');await page.mouse.down();await new Promise(done=>setTimeout(done,650));await page.mouse.up();
  await page.waitForSelector('.ai-prompt-dialog[open]');
  await page.$eval('.ai-prompt-dialog textarea',el=>el.value='Keep the intent and fix typos.');
  await page.click('.ai-prompt-dialog .ai-model-row button');
  await page.waitForSelector('.ai-model-dialog[open] .ai-model-option[data-model="openrouter/google/test-model"]');
  await page.click('.ai-model-dialog .ai-model-option[data-model="openrouter/google/test-model"]');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-wand-desktop.png'});
  await page.click('.ai-prompt-dialog button.primary');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello again');
  assert.match(requests.at(-1).instruction,/Keep the intent/);
  assert.equal(requests.at(-1).model,'openrouter/google/test-model');
  await page.close();
 });
 await test('chat wand expands a saved slash command before rewriting',async()=>{
  const page=await browser.newPage();await page.setViewport({width:1320,height:850});
  await page.goto(base+'/chat#box=builder');
  await page.evaluate(()=>localStorage.removeItem('vmboxChatInputDrafts'));await page.reload();
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden && !!document.querySelector('#chat-composer .ai-wand'));
  await page.type('#chat-input','/pol');
  await page.waitForFunction(()=>!document.querySelector('#composer-picker').hidden);
  await page.keyboard.press('Enter');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'/polish');
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello world');
  assert.equal(requests.at(-1).text,'Helo wrold');
  await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(sentMessages.at(-1).text,'Hello world');
  await page.close();
 });
 await test('mobile wand and Markdown preset share the same control',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden && !!document.querySelector('#chat-composer .ai-wand'));
  await page.$eval('#chat-input',el=>{el.value='';el.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.type('#chat-input','Helo from mobile');
  const alignment=await page.evaluate(()=>{
   const nodes=['#chat-composer .ai-field','#chat-composer #chat-input','#chat-composer #send','#chat-composer #chat-interrupt'].map(selector=>document.querySelector(selector)).filter(node=>node&&!node.hidden&&getComputedStyle(node).display!=='none');
   return nodes.map(node=>({name:node.id||node.className,bottom:node.getBoundingClientRect().bottom}));
  });
  assert.ok(Math.max(...alignment.map(item=>item.bottom))-Math.min(...alignment.map(item=>item.bottom))<3,JSON.stringify(alignment));
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-wand-mobile.png'});
  await page.hover('#chat-composer .ai-wand');await page.mouse.down();await new Promise(done=>setTimeout(done,650));await page.mouse.up();
  await page.waitForSelector('.ai-prompt-dialog[open]');
  await page.click('.ai-prompt-dialog .ai-model-row button');
  await page.waitForSelector('.ai-model-dialog[open] .ai-model-option[data-model="openrouter/anthropic/claude-test"]');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/ai-prompt-model-picker-mobile.png'});
  await page.click('.ai-model-dialog .ai-model-option[data-model="openrouter/anthropic/claude-test"]');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-wand-prompt-mobile.png'});
  await page.click('.ai-prompt-actions button:nth-child(2)');
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello from mobile');
  await page.evaluate(()=>document.querySelector('#presets-modal').hidden=false);
  await page.$eval('#preset-form textarea[name="markdown"]',input=>{input.value='# Helo skill';input.dispatchEvent(new Event('input',{bubbles:true}))});
  assert.equal(await page.$$('#preset-form .ai-wand').then(items=>items.length),1);
  await page.$eval('#preset-form .ai-wand',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#preset-form textarea[name="markdown"]').value==='# Hello skill');
  assert.equal(requests.at(-1).kind,'markdown');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/markdown-wand-mobile.png'});
  await page.close();
 });
 await test('Send stays manual even when an old Auto mode setting is stored',async()=>{
  for(const mobile of [false,true]){
   const page=await browser.newPage();await page.setViewport(mobile?{width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true}:{width:1320,height:850});
   await page.goto(base+'/chat#box=builder');
   await page.evaluate(()=>{localStorage.setItem('vmbox.aiAuto.chat','1');localStorage.removeItem('vmboxChatInputDrafts')});
   await page.reload();await page.waitForSelector('#chat-composer .ai-wand');
   assert.equal((await page.$$('.ai-auto-toggle')).length,0);
   await page.type('#chat-input','Helo wrold');
   const before=sentMessages.length,rewrites=requests.length;
   if(screenshotDir)await page.screenshot({path:screenshotDir+'/manual-send-'+(mobile?'mobile':'desktop')+'.png'});
   await page.click('#send');
   await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
   assert.equal(sentMessages.length,before+1);
   assert.equal(sentMessages.at(-1).text,'Helo wrold');
   assert.equal(requests.length,rewrites);
   await page.close();
  }
 });
 await test('chat wand includes attached image previews on desktop and mobile',async()=>{
  for(const mobile of [false,true]){
   const page=await browser.newPage();await page.setViewport(mobile?{width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true}:{width:1320,height:850});
   await page.goto(base+'/chat#box=builder');await page.waitForSelector('#chat-composer .ai-wand');
   await page.evaluate(()=>localStorage.removeItem('vmboxChatInputDrafts'));await page.reload();
   await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
   await page.evaluate(async()=>{
    const canvas=document.createElement('canvas');canvas.width=240;canvas.height=140;
    const context=canvas.getContext('2d');context.fillStyle='#3a6fb2';context.fillRect(0,0,240,140);context.fillStyle='#fff';context.font='bold 42px sans-serif';context.fillText('IMG',78,85);
    const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
    const transfer=new DataTransfer();transfer.items.add(new File([blob],'reference.png',{type:'image/png'}));
    const input=document.querySelector('#attachments');input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));
   });
   await page.waitForSelector('#chat-image-drafts .draft img');
   assert.equal((await page.$$('.ai-attachment-context')).length,0);
   await page.type('#chat-input','Helo from image');
   if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-image-context-'+(mobile?'mobile':'desktop')+'.png'});
   await page.click('#chat-composer .ai-wand');
   await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello from image');
   const attachment=requests.at(-1).attachments[0];
   assert.equal(attachment.kind,'image');assert.equal(attachment.number,1);
   assert.match(attachment.image,/^data:image\/jpeg;base64,/);
   assert.equal(requests.at(-1).attachments.length,1);
   const before=sentMessages.length;
   if(!mobile)await page.click('#chat-image-drafts .draft-remove');
   await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
   assert.equal(sentMessages.length,before+1);
   assert.equal(sentMessages.at(-1).text,'Hello from image');
   await page.close();
  }
 });
 await test('video attachments provide a labelled preview frame to the wand',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat#box=builder');await page.waitForSelector('#chat-composer .ai-wand');
  await page.evaluate(()=>localStorage.removeItem('vmboxChatInputDrafts'));await page.reload();
  await page.evaluate(async()=>{
   const canvas=document.createElement('canvas');canvas.width=160;canvas.height=90;const context=canvas.getContext('2d');
   const stream=canvas.captureStream(15),chunks=[];
   const recorder=new MediaRecorder(stream,{mimeType:'video/webm'});recorder.ondataavailable=event=>chunks.push(event.data);
   recorder.start();const timer=setInterval(()=>{context.fillStyle='#455ea5';context.fillRect(0,0,160,90);context.fillStyle='white';context.font='bold 26px sans-serif';context.fillText('VID',52,55)},60);
   await new Promise(resolve=>setTimeout(resolve,650));clearInterval(timer);recorder.stop();
   await new Promise(resolve=>recorder.onstop=resolve);stream.getTracks().forEach(track=>track.stop());
   const transfer=new DataTransfer();transfer.items.add(new File(chunks,'clip.webm',{type:'video/webm'}));
   const input=document.querySelector('#attachments');input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));
  });
  await page.waitForSelector('#chat-image-drafts video');
  assert.equal((await page.$$('.ai-attachment-context')).length,0);
  await page.type('#chat-input','Helo from video');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-video-context-mobile.png'});
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello from video');
  assert.equal(requests.at(-1).attachments[0].kind,'video');
  assert.match(requests.at(-1).attachments[0].image,/^data:image\/jpeg;base64,/);
  await page.close();
 });
}finally{await browser.close();await new Promise(done=>server.close(done))}
