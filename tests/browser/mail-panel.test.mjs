import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const stamp=new Date(Date.now()-2*60000).toISOString();
const yesterday=new Date(Date.now()-86400000).toISOString();
const boxes=[{id:'builder',name:'BossDev',state:'running',defaultAgent:'claude',address:'builder@example.test',unread:1},{id:'reviewer',name:'Reviewer',state:'running',defaultAgent:'codex',address:'reviewer@example.test',unread:1}];
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
function fixture(){return {
 messages:[
  {id:'m1',boxId:'builder',boxName:'BossDev',address:'builder@example.test',from:'accounts@northstar.dev',fromName:'Northstar',subject:'Your verification code',preview:'Your code is 483921.',text:'Your code is 483921. It expires in 10 minutes.',receivedAt:stamp,unread:true,quarantined:false,spf:'pass',dkim:'pass',attachments:[]},
  {id:'m2',boxId:'reviewer',boxName:'Reviewer',address:'b@example.test',addressId:'a1',from:'mara@example.com',fromName:'Mara Chen',subject:'Launch checklist',preview:'Please check the new draft.',text:'Please check the new draft.',receivedAt:yesterday,unread:true,quarantined:false,spf:'pass',dkim:'pass',hasAttachments:true,attachments:[{id:'att1',name:'draft.pdf',size:1234}]},
  {id:'m3',boxId:'builder',boxName:'BossDev',address:'builder@example.test',from:'spam@example.com',fromName:'Unknown',subject:'Urgent verify',preview:'Suspicious link removed.',text:'Suspicious link removed.',receivedAt:stamp,unread:false,quarantined:true,spf:'fail',dkim:'fail',attachments:[]},
 ],
 outbox:[{outboxId:'o1',boxId:'builder',boxName:'BossDev',to:['mara@example.com'],subject:'Launch checklist',text:'Looks ready.',status:'pending_approval',version:1,submittedAt:stamp},{outboxId:'o2',boxId:'reviewer',boxName:'Reviewer',to:['support@example.com'],subject:'A question',text:'Can you help?',status:'pending_approval',version:1,submittedAt:stamp},{outboxId:'o3',boxId:'builder',boxName:'BossDev',to:['team@example.com'],subject:'Thanks',text:'Thanks!',status:'sent',version:1,submittedAt:yesterday}],
 addresses:[{id:'a1',localPart:'b',address:'b@example.test',label:'Team',boxIds:['reviewer'],enabled:true,primary:false,unread:1},{id:'builder',localPart:'builder',address:'builder@example.test',label:'',owningBoxId:'builder',boxIds:['builder'],enabled:true,primary:true,unread:1}],keepUnknown:false,writes:[],queries:[],
}}
async function serve(data,{disabled=false,role='owner'}={}){
 const server=http.createServer(async(request,response)=>{
  const url=new URL(request.url,'http://localhost'),path=url.pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');const send=(value,status=200)=>{response.statusCode=status;response.end(JSON.stringify(value))};
   const body=async()=>{let raw='';for await(const chunk of request)raw+=chunk;return raw?JSON.parse(raw):{}};
   if(path==='/v1/whoami')return send({role,accountId:'acct'});
   if(path==='/v1/capabilities')return send({providerEdits:role==='owner'});
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return send(boxes);
   if(path==='/v1/instruction-presets')return send({defaultName:'',presets:[]});
   if(path==='/v1/provider-credentials'||path==='/v1/notifications'||path==='/v1/login-profiles'||path==='/v1/tool-presets')return send([]);
   if(path==='/v1/controller-defaults')return send({provider:'railway',providerCredential:'primary'});
   if(path==='/v1/agent-cli-versions')return send({});
   if(path.startsWith('/v1/agent-cli-versions/catalog/'))return send({latest:'1.0.0',versions:['1.0.0']});
   if(path==='/v1/push/vapid-key')return send({},404);
   if(path.startsWith('/v1/mail/')){
    if(disabled)return send({error:'mail is not configured'},404);
    data.queries.push(path+url.search);
    if(path==='/v1/mail/summary')return send({inbox:data.messages.filter(item=>!item.quarantined).length,unread:data.messages.filter(item=>item.unread&&!item.quarantined).length,quarantine:data.messages.filter(item=>item.quarantined).length,pending:data.outbox.filter(item=>item.status==='pending_approval').length,boxes:boxes.map(box=>({...box,unread:data.messages.filter(item=>item.boxId===box.id&&item.unread&&!item.quarantined).length}))});
    if(path==='/v1/mail/settings'){
     if(request.method==='PUT'){const value=await body();data.keepUnknown=!!value.keepUnknown;data.writes.push({path,method:'PUT',body:value})}
     return send({keepUnknown:data.keepUnknown});
    }
    if(path==='/v1/mail/addresses'){
     if(request.method==='POST'){const value=await body(),id='a'+data.addresses.length;data.addresses.push({...value,id,address:value.localPart+'@example.test',enabled:true,unread:0});data.writes.push({path,method:'POST',body:value});return send(data.addresses.at(-1),201)}
     return send({addresses:data.addresses});
    }
    const addr=path.match(/^\/v1\/mail\/addresses\/([^/]+)$/);
    if(addr){const index=data.addresses.findIndex(item=>item.id===addr[1]);if(index<0)return send({error:'missing'},404);const value=request.method==='DELETE'?{}:await body();data.writes.push({path,method:request.method,body:value});if(data.addresses[index].owningBoxId&&(request.method==='DELETE'||'localPart'in value||'label'in value))return send({error:'box address cannot be renamed or deleted'},400);if(request.method==='DELETE'){data.addresses.splice(index,1);response.statusCode=204;response.end();return}Object.assign(data.addresses[index],value,{address:value.localPart?value.localPart+'@example.test':data.addresses[index].address});return send(data.addresses[index])}
    if(path==='/v1/mail/messages'){
     const folder=url.searchParams.get('folder')||'all',q=url.searchParams.get('q')?.toLowerCase()||'',box=url.searchParams.get('box'),address=url.searchParams.get('address');
     const messages=data.messages.filter(item=>(folder==='quarantine'?item.quarantined:folder==='unread'?item.unread&&!item.quarantined:!item.quarantined)&&(!box||item.boxId===box)&&(!address||item.address===address)&&(!q||[item.from,item.fromName,item.subject,item.preview].join(' ').toLowerCase().includes(q))).map(({text,attachments,...item})=>item);
     return send({messages,nextCursor:''});
    }
    const message=path.match(/^\/v1\/mail\/messages\/([^/]+)$/);
    if(message)return send(data.messages.find(item=>item.id===message[1])||{error:'missing'},data.messages.some(item=>item.id===message[1])?200:404);
    const read=path.match(/^\/v1\/mail\/messages\/([^/]+)\/read$/);
    if(read){const value=await body();data.writes.push({path,method:'POST',body:value});data.messages.find(item=>item.id===read[1]).unread=!value.read;return send({ok:true})}
    const release=path.match(/^\/v1\/mail\/messages\/([^/]+)\/release$/);
    if(release){data.messages.find(item=>item.id===release[1]).quarantined=false;data.writes.push({path,method:'POST'});return send({ok:true})}
    if(path.includes('/attachments/')){response.setHeader('Content-Type','application/pdf');response.end('fixture attachment');return}
    if(path==='/v1/mail/outbox')return send({items:data.outbox.filter(item=>item.status===(url.searchParams.get('status')||'pending_approval')&&(!url.searchParams.get('box')||item.boxId===url.searchParams.get('box'))),nextCursor:''});
   }
   const draft=path.match(/^\/v1\/logical-boxes\/([^/]+)\/mail\/outbox\/([^/]+)(?:\/(approve|reject))?$/);
   if(draft){const item=data.outbox.find(item=>item.outboxId===draft[2]&&item.boxId===draft[1]);if(!item)return send({error:'missing'},404);if(request.method==='GET')return send(item);const value=await body();data.writes.push({path,method:request.method,body:value});if(value.version!==item.version)return send({error:'changed'},409);if(draft[3]==='approve')item.status='sent';else if(draft[3]==='reject'){item.status='rejected';item.reason=value.reason}else{Object.assign(item,value);item.version++}return send(item)}
   return send({});
  }
  const file=path==='/'?'index.html':path.slice(1);if(file.includes('..')){response.statusCode=404;response.end();return}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));return server;
}
const browser=()=>puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
const base=server=>'http://127.0.0.1:'+server.address().port;
async function open(page,server){await page.goto(base(server)+'/#mail');await page.waitForFunction(()=>!document.querySelector('#mail').hidden&&document.querySelector('#mail-panel [data-folder="all"]'));await page.waitForSelector('.mail-panel-row');await page.waitForSelector('[data-address="b@example.test"]')}

test('account Mail panel supports folders, search, message detail, quarantine and drafts at 390/1440 in both themes',async()=>{
 const capture=process.env.MAIL_PANEL_CAPTURE_DIR;if(capture)await mkdir(capture,{recursive:true});
 for(const width of [390,1440])for(const theme of ['light','dark']){
  const data=fixture(),server=await serve(data),chrome=await browser();
  try{
   const page=await chrome.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   const save=async name=>{if(capture)await page.screenshot({path:resolve(capture,`${name}-${width}-${theme}.png`),fullPage:!['review','addresses'].includes(name)})};
   await open(page,server);
   assert.equal(await page.$eval('[data-mail-nav]',node=>node.hidden),false);
   assert.equal(await page.$eval('#mail-panel',node=>document.documentElement.scrollWidth<=innerWidth),true);
   await save('folders');
   if(width===390)await page.click('[data-folder="all"]');
   await page.waitForSelector('.mail-panel-row[data-item="m1"]');await save('inbox');
   assert.equal(await page.$('.mail-panel-row[data-item="m3"]'),null);
   assert.match(await page.$eval('.mail-panel-row[data-item="m1"]',node=>node.textContent),/builder@tra\.vet/);
   await page.click('.mail-panel-row[data-item="m1"]');await page.waitForSelector('.mail-panel-body');await save('detail');
   assert.equal(await page.$eval('.mail-panel-body',node=>node.textContent.includes('483921')),false);
   await page.click('#mail-panel [data-action="reveal"]');assert.equal(await page.$eval('.mail-panel-body',node=>node.textContent.includes('483921')),true);
   await page.click('#mail-panel [data-action="read"]');await page.waitForFunction(()=>document.querySelector('#mail-panel [data-action="read"]')?.textContent==='Mark unread');
   assert.equal(data.writes.some(write=>write.path==='/v1/mail/messages/m1/read'&&write.body.read===true),true);
   if(width===390){await page.click('[data-back="list"]');await page.click('[data-back="folders"]')}
   await page.click('[data-folder="quarantine"]');await page.waitForSelector('.mail-panel-row[data-item="m3"]');
   await page.click('.mail-panel-row[data-item="m3"]');await page.waitForSelector('#mail-panel [data-action="release"]');await save('quarantine');
   await page.click('#mail-panel [data-action="release"]');await page.waitForFunction(()=>document.querySelector('.mail-panel-list-scroll')?.textContent.includes('No messages here'));
   assert.equal(data.writes.some(write=>write.path==='/v1/mail/messages/m3/release'),true);
   if(width===390)await page.click('[data-back="folders"]');
   await page.click('[data-folder="outbox"]');await page.waitForSelector('.mail-panel-row[data-item="o1"]');await save('outbox');
   await page.click('.mail-panel-row[data-item="o1"]');await page.waitForSelector('#mail-panel [data-action="review"]');await page.click('#mail-panel [data-action="review"]');await page.waitForSelector('#mail-panel-review[open]');await save('review');
   assert.equal(await page.$eval('#mail-panel-review',node=>node.getBoundingClientRect().width<=innerWidth),true);
   await page.click('#mail-panel-review [data-review="close"]');
   if(width===390){await page.click('[data-back="list"]');await page.click('[data-back="folders"]')}
   await page.click('[data-action="addresses"]');await page.waitForSelector('#mail-address-dialog[open]');await save('addresses');
   await page.click('[data-address-grants="builder"]');await save('address-grants');
   await page.click('#mail-address-dialog [data-address-action="close"]');
   assert.deepEqual(errors,[]);await page.close();
  }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
 }
});

test('address filters and address grants support create, edit, revoke, delete and keep-unknown',async()=>{
 const data=fixture(),server=await serve(data),chrome=await browser();
 try{
  const page=await chrome.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});await open(page,server);
  await page.click('[data-address="b@example.test"]');await page.waitForSelector('.mail-panel-row[data-item="m2"]');assert.equal(await page.$('.mail-panel-row[data-item="m1"]'),null);
  assert(data.queries.some(path=>path.includes('/v1/mail/messages?')&&path.includes('address=b%40example.test')));
  await page.$eval('[data-action="addresses"]',node=>node.click());await page.waitForSelector('#mail-address-dialog[open]');
  assert.equal(await page.$('[data-address-edit="builder"]'),null);
  assert.equal(await page.$('[data-address-delete="builder"]'),null);
  await page.click('[data-address-grants="builder"]');
  assert.equal(await page.$eval('#mail-address-form [value="builder"]',input=>input.checked&&input.disabled),true);
  await page.click('#mail-address-form [value="reviewer"]');await page.click('#mail-address-form [type="submit"]');
  await page.waitForFunction(()=>document.querySelector('[data-address-grants="builder"]')&&!document.querySelector('#mail-address-form [value="reviewer"]')?.checked);
  assert.deepEqual(data.writes.find(write=>write.path==='/v1/mail/addresses/builder'&&write.method==='PATCH').body,{boxIds:['builder','reviewer']});
  await page.type('#mail-address-form [name="localPart"]','team');await page.type('#mail-address-form [name="label"]','Shared team');
  await page.click('#mail-address-form [value="builder"]');await page.click('#mail-address-form [type="submit"]');
  await page.waitForFunction(()=>document.querySelectorAll('.mail-address-row').length===3);
  assert.equal(await page.$('[data-address-edit="builder"]'),null);
  assert.deepEqual(data.writes.find(write=>write.method==='POST'&&write.path==='/v1/mail/addresses').body,{localPart:'team',label:'Shared team',boxIds:['builder']});
  await page.click('[data-address-edit="a2"]');await page.$eval('#mail-address-form [name="label"]',input=>input.value='Renamed');
  await page.click('#mail-address-form [value="reviewer"]');await page.click('#mail-address-form [value="builder"]');await page.click('#mail-address-form [type="submit"]');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mail-address-row strong')].some(node=>node.textContent==='Renamed'));
  assert.deepEqual(data.writes.find(write=>write.path==='/v1/mail/addresses/a2'&&write.method==='PATCH').body.boxIds,['reviewer']);
  await page.click('#mail-keep-unknown');await page.waitForFunction(()=>document.querySelector('#mail-keep-unknown').checked&&!document.querySelector('#mail-keep-unknown').disabled);
  assert.equal(data.keepUnknown,true);
  await page.click('[data-address-delete="a2"]');await page.click('[data-address-delete="a2"]');await page.waitForFunction(()=>document.querySelectorAll('.mail-address-row').length===2);
  assert.equal(data.writes.some(write=>write.method==='DELETE'&&write.path==='/v1/mail/addresses/a2'),true);
  await page.close();
 }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});

test('account Mail search and outbox review edit, approve and reject use per-box versioned routes',async()=>{
 const data=fixture(),server=await serve(data),chrome=await browser();
 try{
  const page=await chrome.newPage();await page.setViewport({width:1440,height:900});await open(page,server);
  await page.type('.mail-panel-search input','Mara');await page.waitForFunction(()=>document.querySelectorAll('.mail-panel-row').length===1&&document.querySelector('.mail-panel-row[data-item="m2"]'));
  assert(data.queries.some(path=>path.includes('q=Mara')));
  await page.click('[data-box="reviewer"]');await page.waitForFunction(()=>document.querySelector('.mail-panel-row[data-item="m2"]'));
  assert(data.queries.some(path=>path.includes('box=reviewer')));
  await page.click('[data-box=""]');await page.waitForFunction(()=>document.querySelector('[data-box=""]')?.getAttribute('aria-current')==='page');
  await page.evaluate(()=>document.querySelector('[data-folder="outbox"]').click());await page.waitForFunction(()=>document.querySelector('[data-folder="outbox"]')?.getAttribute('aria-current')==='page');
  await page.waitForSelector('.mail-panel-row[data-item="o1"]');
  await page.click('.mail-panel-row[data-item="o1"]');await page.click('#mail-panel [data-action="review"]');await page.waitForSelector('#mail-panel-review[open]');
  await page.$eval('#mail-panel-review [name="subject"]',input=>input.value='Edited subject');await page.click('#mail-panel-review [data-review="approve"]');
  await page.waitForFunction(()=>!document.querySelector('#mail-panel-review').open);
  assert(data.writes.some(write=>write.path.endsWith('/o1')&&write.body.version===1&&write.body.subject==='Edited subject'));
  assert(data.writes.some(write=>write.path.endsWith('/o1/approve')&&write.body.version===2));
  await page.waitForSelector('.mail-panel-row[data-item="o2"]');await page.click('.mail-panel-row[data-item="o2"]');await page.click('#mail-panel [data-action="review"]');
  await page.click('#mail-panel-review [data-review="reject"]');await page.type('#mail-panel-review [name="reason"]','Wrong recipient.');await page.click('#mail-panel-review [data-review="reject"]');
  await page.waitForFunction(()=>!document.querySelector('#mail-panel-review').open);
  assert(data.writes.some(write=>write.path.endsWith('/o2/reject')&&write.body.version===1&&write.body.reason==='Wrong recipient.'));
  await page.waitForFunction(()=>!document.querySelector('.mail-panel-row[data-item="o2"]')&&document.querySelector('.mail-panel-list-scroll')?.textContent.includes('No drafts'));
  await page.click('[data-status="sent"]');await page.waitForSelector('.mail-panel-row[data-item="o1"]');
  await page.click('[data-status="rejected"]');await page.waitForSelector('.mail-panel-row[data-item="o2"]');
  await page.close();
 }finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});

test('Mail nav and panel are absent when the account mail feature is unconfigured',async()=>{
 const server=await serve(fixture(),{disabled:true}),chrome=await browser();
 try{const page=await chrome.newPage();await page.goto(base(server)+'/#mail');await page.waitForFunction(()=>location.hash==='#boxes');assert.equal(await page.$eval('[data-mail-nav]',node=>node.hidden),true);assert.equal(await page.$eval('#mail',node=>node.hidden),true);await page.close()}
 finally{await chrome.close();await new Promise(resolve=>server.close(resolve))}
});
