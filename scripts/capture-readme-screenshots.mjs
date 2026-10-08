// Capture README images from the controller's real browser UI with fictional API fixtures.
// Run from the repository root: node scripts/capture-readme-screenshots.mjs
import http from 'node:http';
import {readFile, mkdir, writeFile} from 'node:fs/promises';
import {resolve, extname} from 'node:path';
import {spawnSync} from 'node:child_process';
import puppeteer from 'puppeteer-core';

const web = resolve('internal/controller/web');
const output = resolve('docs/assets/ui');
const preview = process.env.VMBOX_SCREENSHOT_PREVIEW_DIR;
const stamp = new Date(Math.floor(Date.now()/60000)*60000 - 5*60000).toISOString();
const at = n => new Date(Date.parse(stamp) + n * 60000).toISOString();
const ids = {builder:'11111111-1111-4111-8111-111111111111', reviewer:'22222222-2222-4222-8222-222222222222', tester:'33333333-3333-4333-8333-333333333333', watcher:'44444444-4444-4444-8444-444444444444', helper:'55555555-5555-4555-8555-555555555555'};
const boxes = [
  {id:ids.builder,name:'builder',state:'running',defaultAgent:'claude',provider:'shared-worker',address:'builder@example.test',enabled:true},
  {id:ids.reviewer,name:'reviewer',state:'running',defaultAgent:'codex',provider:'shared-worker',address:'reviewer@example.test',enabled:true},
  {id:ids.tester,name:'tester',state:'running',defaultAgent:'codex',provider:'shared-worker'},
  {id:ids.watcher,name:'watcher',state:'running',defaultAgent:'claude',provider:'shared-worker'},
  {id:ids.helper,name:'helper',state:'hibernated',defaultAgent:'codex',provider:'shared-worker'},
];
const pairKey = ids.builder+'/'+ids.reviewer;
const msg = (id, direction, text, minute, extra={}) => ({id,direction,text,state:'delivered',createdAt:at(minute),updatedAt:at(minute),...extra});
const pair = (id, senderBoxId, recipientBoxId, text, minute, extra={}) => msg(id,'box',text,minute,{senderBoxId,recipientBoxId,...extra});
const pairMessages = [
  pair('pair-1',ids.builder,ids.reviewer,'Handoff: the sample dashboard is ready. Please review the empty state and the 390 px layout.',1),
  pair('pair-2',ids.reviewer,ids.builder,'The mobile cards wrap cleanly. One issue: the empty state needs a clearer next step.',2,{parentMessageId:'pair-1'}),
  pair('pair-3',ids.builder,ids.reviewer,'Good catch. I changed the button to “Add your first item” and updated the handoff notes.',3,{parentMessageId:'pair-2'}),
  pair('pair-4',ids.reviewer,ids.builder,'Reviewed the revision. The empty state reads well now; ready for owner review.',4),
];
const q1='aaaa1111-1111-4111-8111-111111111111',q2='aaaa2222-2222-4222-8222-222222222222';
const ownerMessages = {
 [ids.builder]:[
  msg(q1,'agent','Which areas should I polish first?',0,{threadId:q1,question:{text:'Which areas should I polish first?',choices:['Empty state','Mobile layout','Keyboard flow'],multiple:true}}),
  msg('owner-3','user','Empty state, Mobile layout',1,{state:'read',parentMessageId:q1}),
  msg('owner-4','agent','Thanks. The empty state and mobile layout are updated; reviewer is checking both.',2),
  msg(q2,'agent','What should I include in the final handoff?',3,{threadId:q2,question:{text:'What should I include in the final handoff?',choices:['Screenshots','Test notes','Open questions'],multiple:true}}),
 ],
 [ids.reviewer]:[msg('reviewer-1','agent','Review notes are ready for builder.',4)],
 [ids.tester]:[msg('tester-1','agent','Three viewport checks passed; one label needs a second look.',4)],
 [ids.watcher]:[
  msg('watcher-1','user','Please check the labels in the fictional sample dataset before handoff.',-33,{state:'read'}),
  msg('watcher-2','agent','I’m comparing the labels with the short copy guide.',-31),
  msg('watcher-3','system','MCP · read fixture labels',-29),
  msg('watcher-4','agent','Two labels wrap on narrow screens. I’m checking shorter wording.',-27),
  msg('watcher-5','user','Please check the 390 px view as well.',-26,{state:'read'}),
 ],
 [ids.helper]:[msg('helper-1','agent','Notes saved for the next session.',-60)],
};
const layout={exists:true,groups:[{id:'sample',name:'Sample project',collapsed:false}],members:{['box:'+ids.builder]:'sample',['box:'+ids.reviewer]:'sample',['pair:'+pairKey]:'sample'},pins:['pair:'+pairKey,'box:'+ids.builder],mutes:{},sections:{}};
const activity=[
 {boxId:ids.builder,busy:true,busySince:at(3),mood:'focused',activity:'working',phrase:'Polishing empty state',observedAt:at(5)},
 {boxId:ids.reviewer,busy:false,mood:'happy',activity:'idle',phrase:'Review complete',observedAt:at(5)},
 {boxId:ids.tester,busy:true,busySince:at(2),mood:'focused',activity:'working',phrase:'Checking mobile layout',observedAt:at(5)},
 {boxId:ids.watcher,busy:true,busySince:at(-34),mood:'idle',activity:'working',statusSource:'stale',phrase:'Checking sample labels',observedAt:at(-21)},
 {boxId:ids.helper,busy:false,mood:'idle',activity:'idle',observedAt:at(5)},
];
const mailMessages=[
 {id:'mail-1',boxId:ids.builder,boxName:'builder',address:'builder@example.test',from:'notes@example.test',fromName:'Sample Notes',subject:'Dashboard copy review',preview:'The new empty state reads clearly.',text:'The new empty state reads clearly. Please keep the short button label and include the 390 px capture in the handoff.',receivedAt:at(4),unread:true,quarantined:false,spf:'pass',dkim:'pass',attachments:[]},
 {id:'mail-2',boxId:ids.reviewer,boxName:'reviewer',address:'reviewer@example.test',from:'updates@example.test',fromName:'Fixture Updates',subject:'Mobile review notes',preview:'Cards and labels look good.',text:'Cards and labels look good in the sample layout.',receivedAt:at(2),unread:true,quarantined:false,spf:'pass',dkim:'pass',attachments:[]},
];
const outbox=[{outboxId:'draft-1',boxId:ids.builder,boxName:'builder',from:'builder@example.test',to:['team@example.test'],subject:'Sample dashboard handoff',text:'Hello team,\n\nThe sample dashboard is ready for review. The empty state and mobile layout are updated.\n\nThanks,\nbuilder',status:'pending_approval',version:1,submittedAt:at(5)}];
const addresses=[{id:'addr-builder',localPart:'builder',address:'builder@example.test',label:'',owningBoxId:ids.builder,boxIds:[ids.builder],enabled:true,primary:true,unread:1},{id:'addr-reviewer',localPart:'reviewer',address:'reviewer@example.test',label:'',owningBoxId:ids.reviewer,boxIds:[ids.reviewer],enabled:true,primary:true,unread:1}];
const policy={capabilities:{allContacts:{enabled:false},mail:{read:true,compose:true},createAgentBox:{enabled:false,maxBoxes:3,maxDiskGiB:50,allowedAgents:['codex','claude','opencode']},manageAgentBoxes:{list:true,inspect:true,control:false,tag:false,restart:false,delete:false},mcpTools:{enabled:true,allowedTools:['get_contacts','chat_message','chat_ask','list_agent_boxes','get_agent_box','list_mail_addresses','list_emails','read_email','send_email']},requestMoreTime:{maxExtensionMinutes:7,maxTotalMinutes:14}}};
// The same NoVNC fixture seam as the pair-hero browser test. Only the two
// desktop surfaces are synthetic; the chat, tiles, and controls are real UI.
const fixtureDesktop=`window.NoVNC={default:class {
 constructor(root){this.handlers={};root.innerHTML='<svg viewBox="0 0 640 340" preserveAspectRatio="xMidYMid slice" xmlns="http://www.w3.org/2000/svg" style="width:100%;height:100%;display:block;background:#142333"><rect x="18" y="18" width="604" height="304" rx="12" fill="#20354e"/><rect x="18" y="18" width="604" height="37" rx="12" fill="#304e71"/><circle cx="42" cy="36" r="5" fill="#ed8a84"/><circle cx="60" cy="36" r="5" fill="#e8ca7c"/><circle cx="78" cy="36" r="5" fill="#7ac9b0"/><rect x="42" y="79" width="245" height="211" rx="7" fill="#183048"/><rect x="308" y="79" width="289" height="211" rx="7" fill="#183048"/><path d="M61 108h115m-115 31h183m-183 31h146m-146 31h200m-200 31h174" stroke="#6dc8b6" stroke-width="7" stroke-linecap="round"/><path d="M329 108h194m-194 31h143m-143 31h218m-218 31h179m-179 31h138" stroke="#a8bde5" stroke-width="7" stroke-linecap="round"/></svg>';setTimeout(()=>this.emit('connect'),20)}
 addEventListener(type,fn){(this.handlers[type]??=[]).push(fn)}
 emit(type){for(const fn of this.handlers[type]||[])fn({detail:{clean:true}})}
 disconnect(){}
}};`;
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2','.json':'application/json'};
let server;
function send(res,value,status=200){res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
async function handler(req,res){
 const url=new URL(req.url,'http://fixture'),path=url.pathname;
 if(path==='/novnc.js'){res.setHeader('Content-Type','text/javascript');return res.end(fixtureDesktop)}
 if(path.startsWith('/v1/')){
  if(path==='/v1/whoami')return send(res,{role:'owner',accountId:'fixture-account'});
  if(path==='/v1/capabilities')return send(res,{providerEdits:true});
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return send(res,boxes);
  if(path==='/v1/chat-sidebar-layout')return send(res,layout);
  if(path==='/v1/box-conversations')return send(res,[{boxAId:ids.builder,boxBId:ids.reviewer,boxAName:'builder',boxBName:'reviewer',lastAt:at(5),lastText:pairMessages.at(-1).text}]);
  if(path==='/v1/box-conversations/'+pairKey+'/messages')return send(res,pairMessages);
  if(path==='/v1/box-activity')return send(res,activity);
  for(const [name,id] of Object.entries(ids))if(path==='/v1/logical-boxes/'+id+'/messages')return send(res,ownerMessages[id]||[]);
  if(path==='/v1/logical-boxes/'+ids.builder+'/agent-policy')return send(res,policy);
  if(path==='/v1/logical-boxes/'+ids.builder+'/mail')return send(res,{enabled:true,unread:1,pending:1,address:'builder@example.test',subscribed:true,filters:{sender:'',subject:''}});
  if(path==='/v1/logical-boxes/'+ids.reviewer+'/mail')return send(res,{enabled:true,unread:1,pending:0,address:'reviewer@example.test',subscribed:true,filters:{sender:'',subject:''}});
  if(path.endsWith('/contacts'))return send(res,[]);
  if(path.endsWith('/tags'))return send(res,{tags:['sample']});
  if(path.endsWith('/desktop'))return send(res,{enabled:true});
  if(path==='/v1/mail/summary')return send(res,{inbox:2,unread:2,quarantine:0,archive:0,pending:1,boxes:boxes.map(box=>({...box,unread:mailMessages.filter(m=>m.boxId===box.id&&m.unread).length}))});
  if(path==='/v1/mail/settings')return send(res,{keepUnknown:false});
  if(path==='/v1/mail/addresses')return send(res,{addresses});
  if(path==='/v1/mail/messages')return send(res,{messages:mailMessages.map(({text,attachments,...item})=>item),nextCursor:''});
  if(path==='/v1/mail/messages/mail-1')return send(res,mailMessages[0]);
  if(path==='/v1/mail/outbox')return send(res,{items:outbox,nextCursor:''});
  if(path==='/v1/logical-boxes/'+ids.builder+'/mail/outbox/draft-1')return send(res,outbox[0]);
  if(path==='/v1/mail/approvals')return send(res,{pending:1,items:outbox});
  if(path==='/v1/controller-defaults')return send(res,{provider:'shared-worker',providerCredential:'fixture'});
  if(path==='/v1/fleet/status')return send(res,{slots:[],actualSlots:0,freeSlots:0,occupiedSlots:0});
  if(path==='/v1/agent-cli-versions')return send(res,{});
  if(path==='/v1/push/vapid-key')return send(res,{},404);
  if(['/v1/tool-presets','/v1/notifications','/v1/chat-commands','/v1/provider-credentials','/v1/login-profiles','/v1/instruction-presets'].includes(path))return send(res,[]);
  if(path.endsWith('/messages'))return send(res,[]);
  return send(res,{});
 }
 const file=path==='/'?'index.html':path==='/chat'?'chat.html':path.slice(1);
 if(file.includes('..'))return send(res,{},404);
 try{res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');res.end(await readFile(resolve(web,file)))}catch{send(res,{},404)}
}
async function capture(page,name){
 await page.evaluate(async()=>{
  document.activeElement?.blur();
  for(const element of document.querySelectorAll('input,textarea,[contenteditable]'))element.style.setProperty('caret-color','transparent','important');
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
 });
 const png=await page.screenshot({type:'png'});
 if(preview){await mkdir(preview,{recursive:true});await writeFile(resolve(preview,name+'.png'),png)}
 const encoded=spawnSync('ffmpeg',['-loglevel','error','-y','-f','image2pipe','-vcodec','png','-i','pipe:0','-frames:v','1','-c:v','libwebp','-quality','82',resolve(output,name+'.webp')],{input:png});
 if(encoded.status!==0)throw new Error('ffmpeg '+name+': '+encoded.stderr);
 console.log(name+'.webp');
}
async function pageFor(browser,width,height,theme='light'){
 const page=await browser.newPage();await page.setViewport({width,height,deviceScaleFactor:1,isMobile:width===390,hasTouch:width===390});
 await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
 return page;
}
const click=async(page,selector)=>page.$eval(selector,node=>node.click());
async function selectHandoffChoices(page){
 await page.waitForSelector('.msg[data-message-id="'+q2+'"] .choice');
 await page.evaluate(id=>{const choices=[...document.querySelectorAll('.msg[data-message-id="'+id+'"] .choice')];for(const choice of choices.slice(0,2))if(!choice.classList.contains('on'))choice.click()},q2);
 await page.waitForFunction(id=>document.querySelectorAll('.msg[data-message-id="'+id+'"] .choice.on').length===2,{},q2);
}
const base=()=>`http://127.0.0.1:${server.address().port}`;
async function main(){
 await mkdir(output,{recursive:true});
 server=http.createServer((req,res)=>void handler(req,res));await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  let page=await pageFor(browser,1440,1000);await page.goto(base()+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .pair-msg').length===4&&[...document.querySelectorAll('.pair-tile')].every(tile=>tile.dataset.mode==='desktop'));
  await page.$eval('#chat-messages',node=>node.scrollTop=0);
  await capture(page,'box-chat-desktop');await page.close();

  page=await pageFor(browser,390,844,'dark');await page.goto(base()+'/chat#pair='+encodeURIComponent(pairKey));
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .pair-msg').length===4&&[...document.querySelectorAll('.pair-tile')].every(tile=>tile.dataset.mode==='desktop'));
  await capture(page,'box-chat-mobile-dark');await page.close();

  page=await pageFor(browser,1440,1000);await page.goto(base()+'/chat#box='+ids.builder);
  await page.waitForFunction(()=>document.querySelectorAll('#chat-messages .question').length===2);
  await selectHandoffChoices(page);
  await page.$eval('#chat-messages',node=>node.scrollTop=0);
  await capture(page,'owner-question');await page.close();

  page=await pageFor(browser,1440,900);await page.goto(base()+'/chat#box='+ids.watcher);
  await page.waitForFunction(()=>document.querySelectorAll('#chat-entries [data-box-id]').length>=5);
  await page.waitForFunction(()=>document.querySelector('#chat-banner')?.dataset.kind==='stalled');
  await capture(page,'chat-list-activity');await page.close();

  page=await pageFor(browser,1440,900);await page.goto(base()+'/#mail');
  await page.waitForSelector('.mail-panel-row[data-item="mail-1"]');
  await click(page,'.mail-panel-row[data-item="mail-1"] .mail-panel-row-open');
  await page.waitForSelector('.mail-panel-body');await capture(page,'mail-inbox');
  await click(page,'[data-folder="outbox"]');await page.waitForSelector('.mail-panel-row[data-item="draft-1"]');
  await click(page,'.mail-panel-row[data-item="draft-1"] .mail-panel-row-open');
  await page.waitForSelector('#mail-panel [data-action="review"]');await click(page,'#mail-panel [data-action="review"]');
  await page.waitForSelector('#mail-panel-review[open]');await capture(page,'mail-outbox-approval');await page.close();

  page=await pageFor(browser,1440,900);await page.goto(base()+'/chat#box='+ids.builder);
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await selectHandoffChoices(page);
  await click(page,'#chat-info');await page.waitForSelector('[data-ip-row="access"]');await click(page,'[data-ip-row="access"]');
  await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#inspect-title').textContent==='Access & permissions'&&document.querySelector('#role-editor-status').textContent===''&&[...document.querySelectorAll('#role-editor-form input[name=mcpTools]')].every(input=>!input.disabled));
  await capture(page,'details-access-overview');
  await page.$eval('[data-permission-group="mail"]',node=>node.scrollIntoView({block:'center'}));
  await capture(page,'details-access-permissions');await page.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
}
await main();
