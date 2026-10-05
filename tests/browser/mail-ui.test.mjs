import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const box={id:'builder',name:'BossDev',state:'running',defaultAgent:'claude',provider:'shared-worker',providerCredential:'pool',slotId:'slot-1',assignmentGeneration:3};
const stamp=new Date(Date.now()-2*60000).toISOString();
const yesterday=(()=>{const date=new Date();date.setDate(date.getDate()-1);date.setHours(12,0,0,0);return date.toISOString()})();
function fixture(){return {
 settings:{enabled:true,address:'builder-ab12@example.test',subscribed:true,filters:{sender:'',subject:''},unread:2,pending:2},
 messages:[
  {id:'m1',from:'accounts@northstar.dev',fromName:'Northstar',subject:'Your verification code',preview:'Your code is 483921.',text:'Your code is 483921. It expires in 10 minutes.',receivedAt:stamp,unread:true,hasAttachments:false,quarantined:false,spf:'pass',dkim:'pass',attachments:[]},
  {id:'m2',from:'mara@example.com',fromName:'Mara Chen',subject:'Launch checklist',preview:'Please check the new draft.',text:'Please check the new draft.',receivedAt:yesterday,unread:true,hasAttachments:true,quarantined:false,spf:'pass',dkim:'pass',attachments:[{id:'a1',name:'draft.pdf',contentType:'application/pdf',size:1234}]},
  {id:'m3',from:'spam@example.com',fromName:'Unknown',subject:'Urgent verify',preview:'Suspicious link removed.',text:'Suspicious link removed.',receivedAt:stamp,unread:false,hasAttachments:false,quarantined:true,spf:'fail',dkim:'fail',attachments:[]},
 ],
 outbox:[
  {outboxId:'o1',to:['mara@example.com'],subject:'Launch checklist',text:'Looks ready.',status:'pending_approval',version:1,submittedAt:stamp},
  {outboxId:'o2',to:['support@example.com'],subject:'A question',text:'Can you help?',status:'pending_approval',version:1,submittedAt:stamp},
  {outboxId:'o3',to:['team@example.com'],subject:'Thanks',text:'Thanks!',status:'sent',version:1,submittedAt:stamp},
 ],
 policy:{capabilities:{allContacts:{enabled:false},mail:{read:false,compose:false},mcpTools:{enabled:true,allowedTools:[]},createAgentBox:{maxBoxes:3,maxDiskGiB:50},manageAgentBoxes:{}}},
 addresses:[
  {id:'own-builder',address:'builder-ab12@example.test',localPart:'builder-ab12',owningBoxId:'builder',primary:true,boxIds:['builder']},
  {id:'extra-1',address:'b@example.test',localPart:'b',label:'Team',boxIds:['builder']},
  {id:'other-box',address:'reviewer@example.test',localPart:'reviewer',owningBoxId:'reviewer',primary:true,boxIds:['reviewer','builder']},
  {id:'available-1',address:'shared@example.test',localPart:'shared',label:'Shared',boxIds:['reviewer']},
 ],
 writes:[],notices:[],
 } }
async function serve(data,{disabled=false,role='owner'}={}){
 const server=http.createServer(async(request,response)=>{
  const url=new URL(request.url,'http://localhost'),path=url.pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');
   const send=(value,status=200)=>{response.statusCode=status;response.end(JSON.stringify(value))};
   const body=async()=>{let text='';for await(const chunk of request)text+=chunk;return text?JSON.parse(text):{}};
   if(path==='/v1/agent-desktop/mail/outbox'&&request.method==='POST'){
    const value=await body(),item={outboxId:'agent-'+(data.outbox.length+1),to:value.to,from:value.from||data.settings.address,subject:value.subject,text:value.text,status:'pending_approval',version:1,submittedAt:new Date().toISOString()};
    data.outbox.unshift(item);data.settings.pending++;data.notices.push({id:'draft-'+item.outboxId,direction:'system',text:'Draft pending',createdAt:item.submittedAt,mail:{kind:'outbox_status',outboxId:item.outboxId,to:item.to.join(', '),subject:item.subject,status:item.status}});
    return send({outboxId:item.outboxId,status:item.status},202);
   }
   if(path==='/v1/whoami')return send({role,accountId:'acct'});
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return send([box]);
   if(path==='/v1/mail/approvals')return disabled?send({error:'Mail disabled'},404):send({pending:data.outbox.filter(item=>item.status==='pending_approval').length,items:data.outbox.filter(item=>item.status==='pending_approval').map(item=>({boxId:'builder',boxName:'BossDev',outboxId:item.outboxId,to:item.to,subject:item.subject,createdAt:item.submittedAt}))});
   if(path==='/v1/mail/addresses'){
    if(request.method==='POST'){
     const value=await body();data.writes.push({path,method:'POST',body:value});
     const item={id:'created-'+(data.addresses.length+1),localPart:value.localPart,address:value.localPart+'@example.test',label:value.label,boxIds:value.boxIds||[]};
     data.addresses.push(item);return send(item,201);
    }
    return send({addresses:data.addresses});
   }
   const addressItem=path.match(/^\/v1\/mail\/addresses\/([^/]+)$/);
   if(addressItem&&request.method==='PATCH'){
    const item=data.addresses.find(entry=>entry.id===addressItem[1]);if(!item)return send({error:'Address unavailable'},404);
    const value=await body();data.writes.push({path,method:'PATCH',body:value});
    Object.assign(item,value);if(value.localPart)item.address=value.localPart+'@example.test';
    return send(item);
   }
   if(path==='/v1/logical-boxes/builder/mail'){
    if(disabled)return send({error:'Mail disabled'},404);
    if(request.method==='PUT'){const value=await body();data.writes.push({path,method:'PUT',body:value});data.settings={...data.settings,...value,address:data.settings.address,filters:value.filters||data.settings.filters}}
    return send(data.settings);
   }
   if(path==='/v1/logical-boxes/builder/mail/messages'){
    const folder=url.searchParams.get('folder')||'all';const messages=data.messages.filter(item=>folder==='quarantine'?item.quarantined:folder==='unread'?item.unread&&!item.quarantined:!item.quarantined).map(({text,attachments,...rest})=>rest);
    return send({messages,nextCursor:''});
   }
   const detail=path.match(/^\/v1\/logical-boxes\/builder\/mail\/messages\/([^/]+)$/);
   if(detail)return send(data.messages.find(item=>item.id===detail[1])||{error:'Missing'},data.messages.some(item=>item.id===detail[1])?200:404);
   const read=path.match(/^\/v1\/logical-boxes\/builder\/mail\/messages\/([^/]+)\/read$/);
   if(read){const value=await body();data.writes.push({path,method:'POST',body:value});const item=data.messages.find(entry=>entry.id===read[1]);if(item&&item.unread&&value.read){item.unread=false;data.settings.unread--}return send({read:true})}
   if(path.includes('/attachments/')){response.setHeader('Content-Type','application/pdf');response.end('fixture attachment');return}
   if(path==='/v1/logical-boxes/builder/mail/outbox')return send({items:data.outbox.filter(item=>item.status===(url.searchParams.get('status')||'pending_approval')),nextCursor:''});
   const draft=path.match(/^\/v1\/logical-boxes\/builder\/mail\/outbox\/([^/]+)(?:\/(approve|reject))?$/);
   if(draft){const item=data.outbox.find(entry=>entry.outboxId===draft[1]);if(!item)return send({error:'Missing'},404);if(request.method==='GET')return send(item);const value=await body();data.writes.push({path,method:request.method,body:value});if(value.version!==item.version)return send({error:'outbox draft changed'},409);if(draft[2]==='approve'){item.status='sent';data.settings.pending--}else if(draft[2]==='reject'){item.status='rejected';item.reason=value.reason;data.settings.pending--}else{Object.assign(item,value);item.version++}return send(item)}
   if(path==='/v1/logical-boxes/builder/agent-policy'){
    if(request.method==='PUT'){data.policy=await body();data.writes.push({path,method:'PUT',body:data.policy})}
    return send(data.policy);
   }
   if(path==='/v1/logical-boxes/builder/messages')return send([{id:'n1',direction:'system',text:'3 new mails',createdAt:stamp,mail:{kind:'mail_batch',items:data.messages.slice(0,2).map(({id,from,fromName,subject,preview,quarantined})=>({id,from,fromName,subject,preview,quarantined})),more:1}},{id:'n2',direction:'system',text:'Draft pending',createdAt:stamp,mail:{kind:'outbox_status',outboxId:'o1',to:'mara@example.com',subject:'Launch checklist',status:'pending_approval'}},...data.notices]);
   const other={'/v1/box-activity':[],'/v1/box-conversations':[],'/v1/tool-presets':[],'/v1/chat-commands':[],'/v1/logical-boxes/builder/imported-credentials':{profiles:[]},'/v1/logical-boxes/builder/contacts':[],'/v1/login-profiles':[],'/v1/logical-boxes/builder/idle-policy':{seconds:10800},'/v1/logical-boxes/builder/run-budget-policy':{seconds:0,state:'running'},'/v1/logical-boxes/builder/attachment-storage':{boxBytes:0,boxCount:0,clearableCount:0,accountBytes:0,limitBytes:1024**3},'/v1/logical-boxes/builder/resources':{slotId:'slot-1',assignmentGeneration:3,resources:{cpu:2,memoryMiB:4096,swapMiB:1024,diskGiB:20}},'/v1/fleet/host-resources':{},'/v1/fleet/status':{slots:[{id:'slot-1',serviceName:'Worker 1',serviceId:'worker-1'}]}};
   if(path==='/v1/push/vapid-key')return send({},404);
   return send(other[path]??{});
  }
  if(process.env.VMBOX_REAL_UI_ORIGIN){const upstream=await fetch(new URL(request.url,process.env.VMBOX_REAL_UI_ORIGIN));response.statusCode=upstream.status;response.setHeader('Content-Type',upstream.headers.get('content-type')||'application/octet-stream');response.end(Buffer.from(await upstream.arrayBuffer()));return}
  const file=path==='/chat'?'chat.html':path==='/'?'index.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;response.end();return}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));return server;
}
const browser=()=>puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
async function open(page,base,beforeDetails){await page.goto(base+'/chat#box=builder');await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);await page.waitForFunction(()=>document.querySelector('#mail-approval')&&!document.querySelector('#mail-approval').hidden);if(beforeDetails)await beforeDetails();await page.click('#chat-info');await page.waitForFunction(()=>document.querySelector('[data-ip-row="mail"]')&&!document.querySelector('[data-ip-row="mail"]').hidden)}

test('new agent draft refreshes the open box Outbox and approvals badge',async()=>{
 const capture=process.env.MAIL_CAPTURE_DIR;if(capture)await mkdir(capture,{recursive:true});
 for(const width of [390,1440])for(const theme of ['light','dark']){
  const data=fixture();data.outbox=[];data.settings.pending=0;
  const server=await serve(data),chrome=await browser();
  try{
   const page=await chrome.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
   await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden&&document.querySelector('#chat-loading').hidden);
   await page.click('#chat-info');await page.waitForSelector('[data-ip-row="mail"]:not([hidden])');await page.click('[data-ip-row="mail"]');
   await page.click('[data-mail-tab="outbox"]');await page.waitForFunction(()=>document.querySelector('.mail-empty.compact')?.textContent.includes('No mail'));
   if(capture)await page.screenshot({path:resolve(capture,`pending-before-${width}-${theme}.png`),fullPage:true});
   const submitted=await page.evaluate(async()=>{const response=await fetch('/v1/agent-desktop/mail/outbox',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({to:['owner@example.net'],from:'shared@example.test',subject:'Agent approval draft',text:'Please approve this draft.',idempotencyKey:'browser-pending'})});return response.status});
   assert.equal(submitted,202);
   await page.waitForSelector('[data-outbox-id="agent-1"]',{timeout:10000});
   await page.waitForFunction(()=>document.querySelector('#mail-approval b')?.textContent==='1',{timeout:10000});
   assert.equal(await page.$eval('[data-outbox-tab="pending_approval"]',node=>node.getAttribute('aria-selected')),'true');
   if(capture)await page.screenshot({path:resolve(capture,`pending-after-${width}-${theme}.png`),fullPage:true});
   assert.deepEqual(errors,[]);await page.close();
  }finally{await chrome.close();await new Promise(done=>server.close(done))}
 }
});

test('Mail owner API: inbox, OTP, read, settings, approvals, notices, and permissions',async()=>{
 const capture=process.env.MAIL_CAPTURE_DIR;if(capture)await mkdir(capture,{recursive:true});
 for(const [width,theme] of [[390,'light'],[390,'dark'],[1440,'light'],[1440,'dark']]){
  const data=fixture(),server=await serve(data),chrome=await browser();
  try{
   const base='http://127.0.0.1:'+server.address().port,page=await chrome.newPage();
   await page.setViewport({width,height:900,isMobile:width<600,hasTouch:width<600});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   const errors=[];page.on('pageerror',error=>errors.push(error.message));
   const save=async name=>{if(capture)await page.screenshot({path:resolve(capture,`${name}-${width}-${theme}.png`),fullPage:true})};
   await open(page,base,()=>save('chat-start'));await save('details');
   if(width===390&&theme==='light'){
    assert.equal(await page.$eval('#mail-approval-mobile',node=>new URL(node.href).searchParams.get('mail')),'outbox');
    assert.equal(await page.$eval('#mail-approval-mobile',node=>new URL(node.href).hash),'#mail');
   }
   await page.click('[data-ip-row="mail"]');await page.waitForSelector('[data-mail-id="m1"]');
   await page.waitForSelector('.mail-address-own');
   assert.match(await page.$eval('.mail-address-card',node=>node.textContent),/b@tra\.vet/);
   assert.match(await page.$eval('.mail-address-card',node=>node.textContent),/reviewer@tra\.vet/);
   assert.equal(await page.$eval('[data-ip-page="mail"]',node=>node.textContent.indexOf('Messages')<node.textContent.indexOf('Addresses')),true);
   assert.equal(await page.$eval('[data-mail-id="m1"]',node=>node.textContent.includes('483921')),false);
   assert.equal(await page.$('[data-mail-id="m3"]'),null);
   assert.match(await page.$eval('[data-mail-id="m1"] time',node=>node.textContent),/min ago/);
   assert.equal(await page.$eval('[data-mail-id="m2"] time',node=>node.textContent),'Yesterday');
   assert.equal(await page.$eval('#mail-settings-status',node=>getComputedStyle(node).display),'none');
   await save('inbox');
   if(capture&&width===390&&theme==='light')await (await page.$('.mail-summary-card')).screenshot({path:resolve(capture,'header-receiving-390-light.png')});
   if(capture){await page.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);await save('sidebar-bottom');await page.$eval('#inspect-prototype-page',node=>node.scrollTop=0)}
   await page.click('[data-mail-filter="quarantine"]');await page.waitForSelector('[data-mail-id="m3"]');
   assert.equal(await page.$('[data-mail-id="m1"]'),null);
   await page.click('[data-mail-filter="unread"]');await page.waitForSelector('[data-mail-id="m1"]');
   assert.equal(await page.$('[data-mail-id="m3"]'),null);
   await page.click('[data-mail-filter="all"]');await page.waitForSelector('[data-mail-id="m1"]');
   await page.click('[data-mail-id="m1"]');await page.waitForSelector('.mail-body');
   assert.equal(await page.$eval('.mail-body',node=>node.textContent.includes('483921')),false);
   await page.click('[data-mail-action="reveal"]');assert.equal(await page.$eval('.mail-body',node=>node.textContent.includes('483921')),true);
   await page.click('[data-mail-action="read"]');await page.waitForFunction(()=>document.querySelector('[data-mail-action="read"]')?.textContent==='Marked as read');assert.equal(data.writes.some(write=>write.path.endsWith('/m1/read')&&write.body.read===true),true);
   await page.click('#inspect-prototype-back');await page.waitForFunction(()=>document.querySelector('[data-ip-page="mail"]')?.getAttribute('aria-busy')==='false'&&document.querySelector('[data-mail-id="m2"]'));
   await page.click('#mail-enabled');await page.waitForFunction(()=>document.querySelector('#mail-enabled').checked===false&&!document.querySelector('#mail-enabled').disabled);
   assert.equal(data.writes.some(write=>write.path.endsWith('/mail')&&write.body.enabled===false),true);
   assert.match(await page.$eval('.mail-summary-card',node=>node.textContent),/Paused · new mail is rejected/);
   assert.equal(await page.$('.mail-receiving-card'),null);
   assert.deepEqual(await page.$$eval('.mail-section>.ip-heading',nodes=>nodes.map(node=>node.textContent)),['Addresses','Agent']);
   assert.equal(await page.$('[data-mail-id="m2"]')!==null,true,'existing mail remains visible while receiving is paused');
   assert.match(await page.$eval('.mail-paused-notice',node=>node.textContent),/Existing mail remains available/);
   await page.waitForSelector('[data-mail-action="undo-receiving"]');
   if(capture&&width===390&&theme==='light'){
    await save('inbox-paused');
    await (await page.$('.mail-summary-card')).screenshot({path:resolve(capture,'header-paused-390-light.png')});
   }
   await page.click('[data-mail-action="undo-receiving"]');await page.waitForFunction(()=>document.querySelector('#mail-enabled').checked&&!document.querySelector('#mail-enabled').disabled);
   assert.equal(await page.$('.mail-paused-notice'),null);
   await page.click('[data-mail-tab="outbox"]');await page.waitForSelector('[data-outbox-id="o1"]');await save('outbox');
   await page.click('[data-outbox-id="o1"]');await page.waitForSelector('#mail-review[open]');await save('review');
   await page.$eval('#mail-review [name="subject"]',node=>node.value='Edited subject');await page.click('[data-review="approve"]');
   await page.waitForFunction(()=>!document.querySelector('#mail-review').open);
   assert.equal(data.writes.some(write=>write.path.endsWith('/o1')&&write.body.subject==='Edited subject'&&write.body.version===1&&Array.isArray(write.body.to)),true);
   assert.equal(data.writes.some(write=>write.path.endsWith('/o1/approve')&&write.body.version===2),true);
   await page.waitForSelector('[data-outbox-id="o2"]');await page.$eval('[data-outbox-id="o2"]',node=>node.click());await page.waitForSelector('#mail-review[open]');await page.click('[data-review="reject"]');await page.type('#mail-review [name="reason"]','Not appropriate.');await page.click('[data-review="reject"]');
   await page.waitForFunction(()=>!document.querySelector('#mail-review').open);
   assert.equal(data.writes.some(write=>write.path.endsWith('/o2/reject')&&write.body.reason==='Not appropriate.'&&write.body.version===1),true);
   await page.click('#inspect-prototype-back');await page.click('[data-ip-row="access"]');await page.waitForSelector('#role-editor-inline:not([hidden])');
   await page.waitForSelector('#inspect-create-limit .idle-policy-controls input');
   assert.equal(await page.$eval('#inspect-create-limit',node=>node.querySelector('input[type=checkbox]')===null&&node.querySelector('.idle-policy-controls input').disabled),true);
   await save('access');
   await page.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);await save('access-bottom');
   await page.$eval('#inspect-prototype-page',node=>node.scrollTop=0);
   await page.$eval('#role-editor-form [value=list_emails]',node=>node.click());await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
   assert.equal(data.writes.some(write=>write.path.endsWith('/agent-policy')&&write.body.capabilities.mail.read===true&&write.body.capabilities.mcpTools.allowedTools.includes('list_emails')),true);
   assert.equal(await page.$eval('#role-editor-form [value="list_emails"]',node=>node.checked),true);
   assert.equal(await page.$$eval('#role-editor-form [name="mcpTools"]',nodes=>nodes.filter(node=>['list_mail_addresses','list_emails','read_email','mark_email_read','download_email_attachment','subscribe_inbox','unsubscribe_inbox','send_email','list_outbox','get_outbox_status'].includes(node.value)).length),10);
   assert.equal(await page.$('#role-editor-form [value="search_emails"]'),null);
   assert.equal(await page.$('#role-editor-form [value="reply_email"]'),null);assert.equal(await page.$('#role-editor-form [value="set_busy"]'),null);
   await page.$eval('#role-editor-form [value="send_email"]',node=>node.click());
   assert.equal(await page.$eval('#role-editor-form [value="send_email"]',node=>node.checked),true);
   await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
   assert.equal(data.writes.some(write=>write.path.endsWith('/agent-policy')&&write.body.capabilities.mail.compose===true&&write.body.capabilities.mcpTools.allowedTools.includes('send_email')),true);
   await page.click('#inspect-close');
   assert.equal(await page.$$eval('.mail-system-row',nodes=>nodes.length),2);
   assert.equal(await page.$eval('.mail-system-row',node=>node.textContent.includes('new mails')),true);
   await save('chat-notices');
   if(width===390){assert.equal(await page.$eval('#chat-header-name',node=>node.scrollWidth<=node.clientWidth),true);assert.deepEqual(await page.evaluate(()=>{const badge=document.querySelector('#mail-approval-mobile'),usage=document.querySelector('#chat-usage'),initial=badge.hidden,state=hidden=>{badge.hidden=hidden;return getComputedStyle(usage).display!=='none'};const result={withoutPending:state(true),withPending:state(false)};badge.hidden=initial;return result}),{withoutPending:true,withPending:false},'phone header keeps the usage meter unless an approval badge needs the space')}
   if(width===390&&theme==='light'){await page.click('.mail-chat-line summary');await page.click('.mail-notice-items button');await page.waitForSelector('[data-ip-page="mailDetail"] .mail-body');assert.equal(await page.$eval('#inspect-title',node=>node.textContent),'Email');await page.click('#inspect-close')}
   await page.click('.mail-status-link');await page.waitForFunction(()=>document.querySelector('[data-mail-tab="outbox"]')?.getAttribute('aria-selected')==='true');
   assert.deepEqual(errors,[]);await page.close();
  }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
 }
});

test('Mail UI is absent when the feature is off and tool lists omit retired tools',async()=>{
 const data=fixture(),server=await serve(data,{disabled:true}),chrome=await browser();
 try{const page=await chrome.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);await page.click('#chat-info');await page.waitForFunction(()=>document.querySelector('.mail-group')?.hidden===true);assert.equal(await page.$eval('#mail-approval',node=>node.hidden),true);assert.equal(await page.$eval('#role-editor-form [data-mail-feature]',node=>node.hidden),true);assert.equal(await page.$eval('#role-editor-form [data-mail-feature]',node=>node.hidden),true);await page.close()}finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
 const manage=await readFile(resolve(web,'index.html'),'utf8');assert.match(manage,/value="list_emails"/);assert.doesNotMatch(manage,/value="reply_email"|value="set_busy"/);
});

test('box Mail addresses use a keyboard combobox, quick grant, revoke, undo, and optional label',async()=>{
 const data=fixture();const capture=process.env.MAIL_CAPTURE_DIR;if(capture)await mkdir(capture,{recursive:true});
 const server=await serve(data),chrome=await browser();
 try{
  const page=await chrome.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await open(page,'http://127.0.0.1:'+server.address().port);await page.click('[data-ip-row="mail"]');
  await page.waitForSelector('.mail-address-granted');
  assert.equal(await page.$('#mail-read-permission'),null);
  assert.match(await page.$eval('.mail-access-link',node=>node.textContent),/Agent cannot read mail.*Change in Access/);
  assert.equal(await page.$eval('.mail-address-own',node=>node.textContent.includes('builder-ab12@example.test')),true);
  assert.equal(await page.$eval('.mail-address-own',node=>node.querySelector('button')===null),true);
  assert.equal(await page.$$eval('.mail-address-suggestions button',nodes=>nodes.length),4);
  if(capture){await page.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);await page.screenshot({path:resolve(capture,'box-mail-redesign-390-light.png'),fullPage:true})}
  await page.click('#mail-address-combobox');await page.type('#mail-address-combobox','sha');
  await page.waitForSelector('#mail-address-options:not([hidden]) [role="option"]');
  assert.match(await page.$eval('#mail-address-options',node=>node.textContent),/shared@example.test/);
  await page.keyboard.press('Enter');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mail-address-granted strong')].some(node=>node.textContent==='shared@example.test'));
  assert.deepEqual(data.writes.findLast(write=>write.path.endsWith('/addresses/available-1')).body.boxIds,['reviewer','builder']);
  await page.click('[data-mail-action="remove-address"][data-address-id="available-1"]');
  await page.waitForSelector('[data-mail-action="undo-address"]');
  assert.deepEqual(data.writes.findLast(write=>write.path.endsWith('/addresses/available-1')).body.boxIds,['reviewer']);
  await page.click('[data-mail-action="undo-address"]');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mail-address-granted strong')].some(node=>node.textContent==='shared@example.test'));
  assert.deepEqual(data.writes.findLast(write=>write.path.endsWith('/addresses/available-1')).body.boxIds,['reviewer','builder']);
  await page.click('#mail-address-combobox');await page.type('#mail-address-combobox','bad..name');await page.keyboard.press('Enter');
  assert.match(await page.$eval('#mail-address-status',node=>node.textContent),/Use letters/);
  assert.equal(data.writes.some(write=>write.method==='POST'&&write.path==='/v1/mail/addresses'),false);
  await page.$eval('#mail-address-combobox',node=>{node.value='';node.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.type('#mail-address-combobox','project');await page.keyboard.press('Enter');
  await page.waitForFunction(()=>document.querySelector('.mail-address-label-form'));
  assert.deepEqual(data.writes.find(write=>write.path==='/v1/mail/addresses'&&write.method==='POST').body,{localPart:'project',label:'',boxIds:['builder']});
  await page.type('.mail-address-label-form [name="label"]','Project inbox');await page.click('.mail-address-label-form [type="submit"]');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mail-address-granted small')].some(node=>node.textContent==='Project inbox'));
  assert.deepEqual(data.writes.findLast(write=>write.path.endsWith('/addresses/created-5')).body,{label:'Project inbox'});
  await page.click('[data-mail-action="open-access"]');await page.waitForSelector('#role-editor-inline:not([hidden])');
  assert.deepEqual(errors,[]);await page.close();
 }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});

test('box Mail read permission is changed only in Access and preserves other tools',async()=>{
 const data=fixture();data.policy.capabilities.mcpTools={enabled:true,allowedTools:['send_email','take_screenshot']};
 const server=await serve(data),chrome=await browser();
 try{
  const page=await chrome.newPage();await open(page,'http://127.0.0.1:'+server.address().port);
  await page.click('[data-ip-row="mail"]');await page.waitForSelector('.mail-access-link');
  assert.equal(await page.$('#mail-read-permission'),null);
  assert.equal(data.writes.some(write=>write.path.endsWith('/agent-policy')),false);
  await page.click('[data-mail-action="open-access"]');await page.waitForSelector('#role-editor-inline:not([hidden])');
  await page.waitForFunction(()=>{const node=document.querySelector('#role-editor-form [value="list_emails"]');return node?.isConnected&&!node.disabled&&document.querySelector('#role-editor-status')?.textContent!=='Loading permissions…'});
  await page.evaluate(()=>document.querySelector('#role-editor-form [value="list_emails"]')?.click());
  await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
  const saved=data.writes.findLast(write=>write.path.endsWith('/agent-policy')).body.capabilities;
  assert.equal(saved.mail.read,true);assert.equal(saved.mcpTools.allowedTools.includes('list_emails'),true);
  assert.equal(saved.mcpTools.allowedTools.includes('send_email'),true);assert.equal(saved.mcpTools.allowedTools.includes('take_screenshot'),true);
  await page.close();
 }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});

test('Mail distinguishes a disabled box from a disabled feature and reloads a changed draft',async()=>{
 const data=fixture();data.settings.enabled=false;data.settings.subscribed=false;
 const server=await serve(data),chrome=await browser();
 try{
  const page=await chrome.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await open(page,'http://127.0.0.1:'+server.address().port);
  assert.equal(await page.$eval('[data-ip-row="mail"]',node=>node.hidden),false);
  await page.click('[data-ip-row="mail"]');
  await page.waitForSelector('[data-mail-id="m1"]');
  assert.match(await page.$eval('.mail-paused-notice',node=>node.textContent),/Existing mail remains available/);
  await page.click('[data-mail-tab="outbox"]');await page.waitForSelector('[data-outbox-id="o1"]');
  assert.equal(await page.$('.mail-paused-notice')!==null,true,'outbox remains visible while receiving is paused');
  await page.click('#mail-enabled');await page.waitForFunction(()=>document.querySelector('#mail-enabled').checked&&!document.querySelector('#mail-enabled').disabled);
  await page.click('[data-outbox-id="o1"]');await page.waitForSelector('#mail-review[open]');
  data.outbox[0].version=2;data.outbox[0].subject='Changed elsewhere';
  await page.click('[data-review="approve"]');
  await page.waitForFunction(()=>document.querySelector('.mail-review-status').textContent==='This draft changed; review again');
  assert.equal(await page.$eval('#mail-review [name="subject"]',node=>node.value),'Changed elsewhere');
  await page.click('[data-review="approve"]');await page.waitForFunction(()=>!document.querySelector('#mail-review').open);
  assert.equal(data.writes.some(write=>write.path.endsWith('/o1/approve')&&write.body.version===2),true);
  await page.close();
 }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});
