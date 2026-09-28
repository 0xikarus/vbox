import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

test('saved slash commands stay in drafts until send, mentions send IDs, and stopped boxes cannot send',async()=>{
 const boxes=[{id:'builder',name:'builder',state:'running',defaultAgent:'codex'},{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex'},{id:'sleeping',name:'sleeping',state:'hibernated',defaultAgent:'codex'}];
 const commands=new Map(),posts=[],contactWrites=[];let wakeRequests=0;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/controller-defaults')return res.end(JSON.stringify({provider:'railway',providerCredential:'primary'}));
  if(path==='/v1/provider-credentials')return res.end('[]');
  if(path==='/v1/chat-commands'&&req.method==='GET')return res.end(JSON.stringify([...commands].map(([name,prompt])=>({name,prompt}))));
  if(path.startsWith('/v1/chat-commands/')&&req.method==='PUT'){
   let body='';for await(const chunk of req)body+=chunk;
   const name=decodeURIComponent(path.split('/').pop()),prompt=JSON.parse(body).prompt;commands.set(name,prompt);
   return res.end(JSON.stringify({name,prompt}));
  }
  if(path==='/v1/logical-boxes/builder/contacts'&&req.method==='GET')return res.end(JSON.stringify([{contactBoxId:'reviewer',contactName:'reviewer',contactState:'running',protected:false}]));
  if(path==='/v1/logical-boxes/sleeping/allocate'&&req.method==='POST'){
   wakeRequests++;
   return res.end(JSON.stringify({requestId:'wake-sleeping',state:'reserved'}));
  }
  if(path==='/v1/allocations/wake-sleeping'){
   boxes[2].state='running';
   return res.end(JSON.stringify({requestId:'wake-sleeping',state:'ready'}));
  }
  if(path.endsWith('/contacts')&&req.method==='PUT'){contactWrites.push(path);return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let body='';for await(const chunk of req)body+=chunk;posts.push(JSON.parse(body));
   return res.end(JSON.stringify({message:{state:'delivered'}}));
  }
  if(path.endsWith('/messages'))return res.end('[]');
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1100,height:800});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await page.$eval('#new-box',button=>button.click());
  await page.waitForFunction(()=>!!document.querySelector('#create-instructions option[value="__concise__"]'));
  await page.select('#create-instructions','__concise__');
  assert.match(await page.$eval('#create-instructions-custom',area=>area.value),/few tokens as needed/);
  await page.click('#new-box-close');
  await page.$eval('#new-box',button=>button.click());
  await page.select('#create-instructions','custom');
  await page.type('#create-instructions-custom','# Existing rules');
  await page.select('#create-instructions','__concise__');
  assert.match(await page.$eval('#create-instructions-custom',area=>area.value),/Existing rules[\s\S]*few tokens as needed/,'the option adds guidance to existing instructions');
  await page.click('#new-box-close');
  await page.focus('[data-box-id=reviewer] .chat-meta');
  await page.keyboard.press('Enter');
  await page.waitForFunction(()=>document.querySelector('[data-box-id=reviewer]').classList.contains('active'));
  await page.click('[data-box-id=builder]');
  await page.waitForFunction(()=>document.querySelector('[data-box-id=builder]').classList.contains('active'));
  await page.click('#chat-menu');await page.click('#commands-toggle');
  await page.type('#command-form input[name=name]','review');
  await page.type('#command-form textarea[name=prompt]','Review this change and list two risks.');
  await page.click('#command-form button[type=submit]');
  await page.waitForFunction(()=>document.querySelector('#command-list').textContent.includes('/review'));
  await page.click('#commands-modal button[data-close=commands-modal]');
  assert.equal(await page.$eval('#commands-modal',el=>el.hidden),true,'closing Commands must hide its sheet');
  await page.type('#chat-input','Draft note');
  await page.$eval('#chat-menu',button=>button.click());
  await page.$eval('#commands-toggle',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#commands-modal').hidden && !document.querySelector('#command-use').hidden);
  await page.click('#command-use');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'Draft note\n\n/review','using a command must preserve an unsent draft and its slash token');
  await page.$eval('#chat-input',el=>{el.value='';el.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.type('#chat-input','/rev');
  await page.waitForFunction(()=>!document.querySelector('#composer-picker').hidden);
  await page.keyboard.press('Enter');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'/review');
  assert.equal(posts.length,0,'selecting a slash command must not send it');
  await page.waitForFunction(()=>JSON.parse(localStorage.getItem('vmboxChatInputDrafts')||'{}').builder==='/review');
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='/review');
  await page.type('#chat-input',' Include tests.');
  await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts[0].text,'Review this change and list two risks. Include tests.');
  await page.type('#chat-input','Before /review. Keep /missing and https://example.test/review intact.');
  await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.equal(posts[1].text,'Before Review this change and list two risks.. Keep /missing and https://example.test/review intact.');
  await page.type('#chat-input','Please coordinate with @rev');
  await page.waitForFunction(()=>!document.querySelector('#composer-picker').hidden);
  await page.click('#composer-picker button');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'Please coordinate with @reviewer ');
  assert.equal(posts.length,2,'unsent mention must not create a message');
  assert.equal(contactWrites.length,0,'unsent mention must not alter contacts');
  await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  assert.deepEqual(posts[2].mentionedBoxIds,['reviewer']);
  await page.click('[data-box-id=sleeping]');
  await page.waitForFunction(()=>!document.querySelector('#chat-wake').hidden);
  assert.match(await page.$eval('#chat-banner',el=>el.textContent),/Files and chat history are saved/);
  await page.type('#chat-input','Wait until running');
  assert.equal(await page.$eval('#send',el=>el.disabled),true);
  await page.keyboard.press('Enter');
  assert.equal(posts.length,3,'Enter must not bypass the stopped-box send guard');
  await page.click('#chat-wake');
  await page.waitForFunction(()=>!document.querySelector('#send').disabled);
  assert.equal(wakeRequests,1);
  assert.equal(await page.$eval('#chat-wake',el=>el.hidden),true);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
