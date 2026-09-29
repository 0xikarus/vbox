import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const usage={profiles:[{application:'claude',name:'work',boxes:['Builder'],observedAt:'2026-09-24T03:00:00Z',checkedAt:'2026-09-24T03:01:00Z',snapshot:{windows:[
 {name:'session',usedPercent:25,resetsAt:'2026-09-24T04:00:00Z'},
 {name:'weekly_all',usedPercent:90,resetsAt:'2026-09-30T00:00:00Z'}
],spend:{currency:'USD',limit:100,used:40},balances:[{unit:'USD',amount:12}],rateCaps:[{model:'example',type:'RPM',amount:100}],source:'live box'}},
 {application:'claude',name:'personal',boxes:['Writer'],observedAt:'2026-09-24T03:00:00Z',snapshot:{source:'live box',windows:[{name:'session',usedPercent:20}]}},
 {application:'opencode',name:'spare',boxes:[],observedAt:'2026-09-24T03:00:00Z',snapshot:{source:'saved profile',windows:[{name:'primary',usedPercent:40}]}}]};
const screenshotDir=process.env.VMBOX_PROFILE_SCREENSHOTS;

test('usage shows remaining capacity and Conversations width can be resized and restored',async()=>{
 let delayWriterProfile=false,writerProfileRequests=0,manualRefreshes=0;
 const server=http.createServer((request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'){response.setHeader('Content-Type','text/html');return response.end(files['chat.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return response.end(files[path.slice(1)])}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify([{id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'railway'},{id:'writer',name:'Writer',state:'running',defaultAgent:'claude',provider:'railway'},{id:'shell',name:'Terminal',state:'running',defaultAgent:'shell',provider:'railway'}]));
  if(path.endsWith('/imported-credentials')){
   const body=JSON.stringify({profiles:path.includes('/shell/')?[]:[{application:'claude',name:path.includes('/builder/')?'work':'personal'}],pending:[],verified:true});
   if(path.includes('/writer/')){writerProfileRequests++;if(delayWriterProfile)return setTimeout(()=>response.end(body),500)}
   return response.end(body);
  }
  if(path==='/v1/box-conversations')return response.end(JSON.stringify([{boxAId:'builder',boxBId:'writer',boxAName:'Builder',boxBName:'Writer',lastAt:'2026-09-24T03:00:00Z',lastText:'Hello'}]));
  if(path==='/v1/login-profiles')return response.end(JSON.stringify([{application:'claude',name:'work',model:'sonnet'},{application:'claude',name:'personal',model:'opus'},{application:'opencode',name:'spare',model:'openrouter/auto'}]));
  if(path==='/v1/controller-defaults')return response.end('{}');
  if(path==='/v1/provider-credentials')return response.end('[]');
  if(path==='/v1/instruction-presets')return response.end(JSON.stringify({defaultName:'',presets:[]}));
  if(path==='/v1/box-conversations/builder/writer/messages'||path==='/v1/logical-boxes/builder/messages'||path==='/v1/logical-boxes/writer/messages'||path==='/v1/logical-boxes/shell/messages'||path==='/v1/tool-presets'||path==='/v1/chat-commands')return response.end('[]');
  if(path==='/v1/profile-usage/refresh'&&request.method==='POST'){
   manualRefreshes++;
   for(const profile of usage.profiles)profile.checkedAt='2026-09-24T04:00:00Z';
   return response.end(JSON.stringify({profiles:usage.profiles.length}));
  }
  if(path==='/v1/profile-usage')return response.end(JSON.stringify(usage));
  if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
  response.statusCode=404;response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await page.waitForFunction(()=>!document.querySelector('#usage-toggle').hidden&&document.querySelector('#usage-list').textContent.includes('claude · work'));
  assert.equal(await page.$eval('#usage-toggle',element=>element.textContent),'Usage');
  assert.match(await page.$eval('#usage-toggle',element=>element.title),/all saved profiles/);
  await page.click('#usage-toggle');
  await page.waitForFunction(()=>document.querySelector('#usage-list')?.textContent.includes('75% remaining'));
  const text=await page.$eval('#usage-list',element=>element.textContent);
  assert.match(text,/10% remaining/);
  assert.match(text,/60 USD remaining/);
  assert.match(text,/Available balances: USD 12/);
  assert.match(text,/remaining requests unavailable/);
  assert.match(text,/No running box · Source: saved profile/);
  assert.deepEqual(await page.$$eval('.usage-track',tracks=>tracks.map(track=>track.getAttribute('aria-valuenow'))),['75','10','80','60']);
  await page.click('#usage-modal button[data-close]');
  await page.click('#new-box');
  await page.waitForFunction(()=>document.querySelectorAll('.create-profile-option').length===3&&document.querySelector('.create-profile-list').textContent.includes('10% left'));
  const choices=await page.$$eval('.create-profile-option',buttons=>buttons.map(button=>({name:button.querySelector('strong').textContent,summary:button.querySelector('.create-profile-remaining').textContent,details:button.querySelector('small').textContent})));
  assert.deepEqual(choices.map(choice=>choice.name),['None','work','personal']);
  assert.match(choices[1].details,/Session 75%.*Week 10%/);
  assert.equal(choices[2].summary,'80% left');
  await page.click('.create-profile-option[data-profile*="personal"]');
  assert.equal(await page.$eval('#create-box select[name=loginProfile]',element=>JSON.parse(element.value).name),'personal');
  assert.equal(await page.$eval('#create-box input[name=agentModel]',element=>element.value),'opus');
  if(screenshotDir){await mkdir(screenshotDir,{recursive:true});await page.screenshot({path:screenshotDir+'/create-profiles-desktop.png'})}
  await page.click('#new-box-close');

  await page.click('#chat-entries [data-box-id="writer"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-usage')?.textContent.includes('80% left'));
  assert.deepEqual(await page.$eval('#chat-control',element=>({next:element.nextElementSibling?.id,label:element.getAttribute('aria-label'),icon:!!element.querySelector('svg path[d="M12 18h6"]'),text:element.textContent.trim()})),{next:'chat-usage',label:'Open desktop and TMUX controls',icon:true,text:''});
  assert.match(await page.$eval('#chat-usage',element=>element.getAttribute('aria-label')),/claude personal usage: 80% remaining/);
  assert.equal(await page.$eval('#usage-toggle',element=>element.textContent),'Usage','navbar opens all profiles without a percentage');
  await page.click('#chat-usage');
  assert.match(await page.$eval('#usage-title',element=>element.textContent),/claude · personal/);
  assert.match(await page.$eval('#usage-list',element=>element.textContent),/80% remaining/);
  assert.doesNotMatch(await page.$eval('#usage-list',element=>element.textContent),/claude · work/);
  await page.click('#usage-modal button[data-close]');
  await page.click('#chat-entries [data-box-id="builder"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-usage')?.textContent.includes('10% left'));
  await page.click('#usage-toggle');
  assert.match(await page.$eval('#usage-title',element=>element.textContent),/Profile usage limits/);
  assert.match(await page.$eval('#usage-list',element=>element.textContent),/claude · personal/);
  await page.click('#usage-refresh');
  await page.waitForFunction(()=>document.querySelector('#usage-status')?.textContent.includes('Usage updated.'));
  assert.equal(manualRefreshes,1,'manual refresh still checks saved profiles');
  assert.equal(await page.$eval('#usage-refresh',element=>element.disabled),false);
  assert.equal(await page.$eval('#usage-toggle',element=>element.textContent),'Usage');
  await page.click('#usage-modal button[data-close]');
  await page.click('#chat-entries [data-box-id="shell"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Terminal');
  assert.equal(await page.$eval('#chat-usage',element=>element.hidden),true,'shell chat has no profile usage');
  await page.click('#chat-entries [data-pair-key] .chat-meta');
  assert.equal(await page.$eval('#chat-usage',element=>element.hidden),true,'direct box conversation has no single profile');
  delayWriterProfile=true;
  const before=writerProfileRequests;
  await page.click('#chat-entries [data-box-id="writer"] .chat-meta');
  for(let tries=0;writerProfileRequests===before&&tries<50;tries++)await new Promise(resolve=>setTimeout(resolve,10));
  assert.ok(writerProfileRequests>before,'writer profile lookup began');
  await page.click('#chat-entries [data-box-id="builder"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-usage')?.textContent.includes('10% left'));
  await new Promise(resolve=>setTimeout(resolve,550));
  assert.match(await page.$eval('#chat-usage',element=>element.textContent),/10% left/,'a delayed profile response cannot relabel the selected chat');

  const initial=await page.$eval('#chat-list',element=>element.getBoundingClientRect().width);
  const rect=await page.$eval('#chat-resizer',element=>{const r=element.getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}});
  await page.mouse.move(rect.x,rect.y);await page.mouse.down();await page.mouse.move(rect.x+120,rect.y,{steps:5});await page.mouse.up();
  const resized=await page.$eval('#chat-list',element=>element.getBoundingClientRect().width);
  assert.ok(resized>=initial+100,'dragging the separator widens the sidebar');
  assert.equal(await page.evaluate(()=>Number(localStorage.getItem('vmboxChatSidebarWidth'))),Math.round(resized));
  await page.reload();await page.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
  assert.ok(Math.abs(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)-resized)<=2,'sidebar width survives reload');
  await page.focus('#chat-resizer');await page.keyboard.press('ArrowLeft');
  assert.ok(Math.abs(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)-(resized-24))<=2);
  await page.keyboard.press('Home');
  assert.equal(Math.round(await page.$eval('#chat-list',element=>element.getBoundingClientRect().width)),240);
  await page.keyboard.press('End');
  assert.ok(await page.$eval('#chat-main',element=>element.getBoundingClientRect().width)>=448,'the transcript keeps usable width');
  await page.setViewport({width:900,height:800});
  assert.ok(await page.$eval('#chat-main',element=>element.getBoundingClientRect().width)>=448,'narrow desktop still has room for messages');
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').hidden&&!document.querySelector('#usage-toggle').hidden&&document.querySelector('#usage-list').textContent.includes('claude · work'));
  assert.equal(await page.$eval('#chat-resizer',element=>getComputedStyle(element).display),'none');
  await page.click('#chat-entries [data-box-id="writer"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name').textContent==='Writer'&&document.querySelector('#chat-usage').textContent.includes('80% left'));
  assert.equal(await page.$eval('#chat-usage',element=>element.hidden),false,'the selected chat usage is visible on a phone');
  assert.notEqual(await page.$eval('#chat-control',element=>getComputedStyle(element).display),'none','computer icon stays available before usage on a phone');
  assert.equal(await page.$eval('#chat-header-state',element=>element.innerText.trim()),'running','mobile keeps the box state readable');
  await page.click('#chat-usage');
  assert.match(await page.$eval('#usage-title',element=>element.textContent),/claude · personal/);
  assert.doesNotMatch(await page.$eval('#usage-list',element=>element.textContent),/claude · work/);
  await page.click('#usage-modal button[data-close]');
  await page.click('#chat-back');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await new Promise(resolve=>setTimeout(resolve,350));
  await page.click('#new-box');
  await page.waitForFunction(()=>!document.querySelector('#new-box-modal').hidden&&document.querySelector('.create-profile-list')?.textContent.includes('10% left'));
  await page.select('#create-box select[name=defaultAgent]','opencode');
  await page.waitForFunction(()=>document.querySelectorAll('.create-profile-option').length===2&&document.querySelector('.create-profile-list').textContent.includes('60% left'));
  await page.click('.create-profile-option[data-profile*="spare"]');
  assert.equal(await page.$eval('#create-box select[name=loginProfile]',element=>JSON.parse(element.value).name),'spare');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'new box profile list fits mobile width');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/create-profiles-mobile.png'});
  await page.click('#new-box-close');
  await page.click('#usage-toggle');
  assert.match(await page.$eval('#usage-list',element=>element.textContent),/claude · work/);
  assert.match(await page.$eval('#usage-list',element=>element.textContent),/claude · personal/);
  await page.$eval('#usage-modal .usage-card',card=>{card.scrollTop=card.scrollHeight});
  assert.equal(await page.$eval('#usage-modal button[data-close]',button=>{const rect=button.getBoundingClientRect();return rect.top>=0&&rect.bottom<=innerHeight}),true,'usage close stays in view when the profile list scrolls');
  await page.click('#usage-modal button[data-close]');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.setViewport({width:320,height:700,isMobile:true,hasTouch:true});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').hidden&&!document.querySelector('#usage-toggle').hidden&&document.querySelector('#usage-list').textContent.includes('claude · work'));
  await page.click('#chat-entries [data-box-id="writer"] .chat-meta');
  await page.waitForFunction(()=>document.querySelector('#chat-usage').textContent.includes('80% left'));
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'narrow phones do not overflow');
  await page.click('#chat-back');
  await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
  await new Promise(resolve=>setTimeout(resolve,350));
  await page.click('#new-box');
  await page.waitForFunction(()=>!document.querySelector('#new-box-modal').hidden&&document.querySelectorAll('.create-profile-option').length===3);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'profile choices fit a narrow phone');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
