import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');

// The chat Details drawer edits the same controller-side contact access graph as the
// single-box workspace page (whose non-owner gating is covered in
// workspace-desktop.test.mjs). This test drives the real page against fixture
// APIs and writes the screenshot the PR references.
test('chat details drawer edits the per-box contact graph',async()=>{
 let protectedBox=false,requests=[],creations=[],fullDesktopShots=0,explicitIdle=false,tags=['backend','priority'];
 const directPolicies=new Map();
 let contacts=[
  {contactBoxId:'reviewer',contactName:'reviewer',contactRoles:[],contactState:'running',contactAgent:'codex',override:'allow',canMessage:true,reason:"Included in this box's direct contact list."},
  {contactBoxId:'planner',contactName:'planner',contactRoles:[],contactState:'running',contactAgent:'claude',override:'inherit',canMessage:false,reason:"Not in this box's direct contact list."},
  {contactBoxId:'auditor',contactName:'auditor',contactRoles:[],contactState:'running',contactAgent:'codex',override:'block',canMessage:false,reason:'Blocked by an explicit connection override.'},
 ];
 let builderMessages=[{id:'m1',direction:'agent',state:'delivered',text:'Ready.',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()}];
 const boxes=[{id:'builder',name:'builder',state:'running',defaultAgent:'claude',provider:'railway',volumeId:'v1',volumeName:'v1'},{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex',provider:'railway',volumeId:'v2',volumeName:'v2'},{id:'planner',name:'planner',state:'running',defaultAgent:'claude',provider:'railway',volumeId:'v3',volumeName:'v3'},{id:'auditor',name:'auditor',state:'running',defaultAgent:'codex',provider:'railway',volumeId:'v4',volumeName:'v4'}];
 const thumbnail=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0],method=req.method;requests.push(method+' '+path);
  if(path==='/chat')return res.end(html);
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(!path.startsWith('/v1/'))return res.end();
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/logical-boxes'&&method==='POST'){let body='';for await(const chunk of req)body+=chunk;creations.push(JSON.parse(body));return res.end(JSON.stringify({id:'created',name:'github-chat-fixture'}))}
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  const policyMatch=path.match(/^\/v1\/logical-boxes\/([^/]+)\/agent-policy$/);
  if(policyMatch){const boxID=decodeURIComponent(policyMatch[1]),box=boxes.find(value=>value.id===boxID);if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;directPolicies.set(boxID,JSON.parse(body))}return res.end(JSON.stringify({boxId:boxID,boxName:box?.name||boxID,...(directPolicies.get(boxID)||{capabilities:{}})}))}
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/login-profiles')return res.end(JSON.stringify([{application:'claude',name:'personal',model:'sonnet'},{application:'claude',name:'other',model:'opus'},{application:'opencode',name:'openrouter',model:'openrouter/saved'},{application:'github',name:'gh-work'}]));
  if(path==='/v1/login-profiles/opencode/openrouter/models')return res.end(JSON.stringify({source:'OpenRouter live catalog',models:[{id:'openrouter/live-model',label:'Live model'}]}));
  if(path==='/v1/controller-defaults')return res.end('{}');
  if(path==='/v1/provider-credentials')return res.end('[]');
  if(path==='/v1/instruction-presets')return res.end(JSON.stringify({defaultName:'',presets:[]}));
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path.endsWith('/desktop/screenshot')){
   if(req.url.includes('thumbnail=true')){res.statusCode=409;return res.end(JSON.stringify({error:'thumbnail offline'}))}
   fullDesktopShots++;res.setHeader('Content-Type','image/png');return res.end(thumbnail);
  }
  if(path.endsWith('/desktop/replay'))return res.end(JSON.stringify([
   {id:'frame-1',capturedAt:new Date(Date.now()-60000).toISOString(),width:1,height:1},
   {id:'frame-2',capturedAt:new Date(Date.now()-30000).toISOString(),width:1,height:1}
  ]));
  if(path.includes('/desktop/replay/')){res.setHeader('Content-Type','image/png');return res.end(thumbnail)}
  if(path==='/v1/logical-boxes/builder/messages'&&method==='GET'){
   if(explicitIdle)res.setHeader('X-Vmbox-Agent-Busy','false');
   return res.end(JSON.stringify(builderMessages));
  }
  if(path==='/v1/logical-boxes/builder/messages'&&method==='POST'){
   await new Promise(resolve=>setTimeout(resolve,600));
   const now=new Date().toISOString();
   builderMessages=[...builderMessages,{id:'m4',direction:'user',state:'delivered',text:'Quick check',createdAt:now,updatedAt:now},{id:'m5',direction:'agent',state:'delivered',text:'Quick answer',createdAt:now,updatedAt:now}];
   return res.end(JSON.stringify({message:{state:'delivered'}}));
  }
  if(path==='/v1/logical-boxes/reviewer/sessions/interactive'&&method==='POST')return res.end(JSON.stringify({session:'codex-reviewer'}));
  if(path==='/v1/logical-boxes/reviewer/desktop'&&method==='POST')return res.end(JSON.stringify({state:'running'}));
  if(path==='/v1/logical-boxes/builder/instructions/resync'&&method==='POST')return res.end(JSON.stringify({pending:false,note:'Instructions re-synced.'}));
  if(path.endsWith('/messages'))return res.end(JSON.stringify([]));
  if(path==='/v1/logical-boxes/builder/contacts'){
   if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;const parsed=JSON.parse(body);const contact=contacts.find(c=>c.contactBoxId===parsed.contact||c.contactName===parsed.contact);contact.override=parsed.state;contact.canMessage=parsed.state==='allow';contact.reason=parsed.state==='allow'?"Included in this box's direct contact list.":"Not in this box's direct contact list.";return res.end(JSON.stringify(contact))}
   return res.end(JSON.stringify(contacts));
  }
  if(path.startsWith('/v1/logical-boxes/builder/contacts/')&&method==='DELETE'){contacts=contacts.filter(c=>c.contactName!==decodeURIComponent(path.split('/').pop()));res.statusCode=204;return res.end()}
  if(path==='/v1/logical-boxes/builder/protection'){
   if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;protectedBox=!!JSON.parse(body).protected}
   return res.end(JSON.stringify({protected:protectedBox}));
  }
  if(path==='/v1/logical-boxes/builder/tags'){
   if(method==='PUT'){let body='';for await(const chunk of req)body+=chunk;tags=JSON.parse(body).tags}
   return res.end(JSON.stringify({tags}));
  }
  if(path==='/v1/logical-boxes/builder')return res.end(JSON.stringify(boxes[0]));
  if(path.startsWith('/v1/logical-boxes/builder'))return res.end(JSON.stringify(boxes[0]));
  res.statusCode=404;return res.end('{}');
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const p=await browser.newPage();
  p.on('dialog',dialog=>dialog.accept());
  await p.evaluateOnNewDocument(()=>{
   window.openWorkspaceDesktop=(id,onStatus,options)=>{
    if(options.viewOnly){window.viewerPreview={id,viewOnly:true};setTimeout(()=>onStatus('Desktop connected'),350);return()=>{window.viewerPreviewClosed=true}};
    window.viewerDesktop={id,root:options.root.id};return()=>{};
   };
   window.openWorkspaceTerminal=(id,session,_status,options)=>{window.viewerTerminal={id,session,root:options.root.id};return()=>{}};
  });
  await p.setViewport({width:420,height:820,deviceScaleFactor:1});
  await p.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await p.waitForFunction(()=>!document.querySelector('#chat-app').hidden);
  await p.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
  await p.evaluate(()=>{document.querySelector('#login').hidden=false});
  const loginLayout=await p.evaluate(()=>{const form=document.querySelector('#login'),card=form.querySelector('.login-card'),style=getComputedStyle(form);return {position:style.position,z:Number(style.zIndex),card:!!card,modal:card?.getAttribute('aria-modal')}});
  assert.deepEqual(loginLayout,{position:'fixed',z:100,card:true,modal:'true'});
  await p.evaluate(()=>{document.querySelector('#login').hidden=true});
  await p.evaluate(()=>document.activeElement?.blur());
  const now=new Date().toISOString();
  builderMessages=[...builderMessages,{id:'m2',direction:'user',state:'delivered',text:'Please work on this.',createdAt:now,updatedAt:now}];
  await p.$eval('[data-box-id="builder"]',element=>element.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages .msg.processing')?.textContent.includes('agent is processing…'),{timeout:1500});
  builderMessages=[...builderMessages,{id:'m3',direction:'agent',state:'delivered',text:'Done.',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString()}];
  await p.$eval('[data-box-id="builder"]',element=>element.click());
  await p.waitForFunction(()=>document.querySelector('#chat-messages .msg.agent .text')?.textContent==='Done.'||[...document.querySelectorAll('#chat-messages .msg.agent .text')].some(e=>e.textContent==='Done.'),{timeout:1500});
  assert.equal(await p.$eval('#chat-messages',element=>!!element.querySelector('.msg.processing')),false,'processing must end when the agent reply appears');
  explicitIdle=true;
  await p.click('#refresh');
  await new Promise(resolve=>setTimeout(resolve,150));
  await p.type('#chat-input','Quick check');
  await p.click('#send');
  assert.equal(await p.$eval('#chat-input',element=>element.value),'','the composer must clear as soon as a message is sent');
  await p.waitForFunction(()=>!!document.querySelector('#chat-messages .msg.processing'),{timeout:400});
  assert.deepEqual(await p.$$eval('#chat-messages .msg',rows=>rows.slice(-2).map(row=>({kind:row.classList.contains('user')?'user':'processing',text:row.textContent.includes('Quick check')?'Quick check':''}))),[{kind:'user',text:'Quick check'},{kind:'processing',text:''}]);
  await p.waitForFunction(()=>[...document.querySelectorAll('#chat-messages .msg.agent .text')].some(e=>e.textContent==='Quick answer'),{timeout:5000});
  assert.equal(await p.$eval('#chat-messages',element=>!!element.querySelector('.msg.processing')),false,'fast replies must clear the in-flight indicator');
  const beforeListRefresh=requests.filter(r=>r==='GET /v1/grid-boxes').length;
  await p.evaluate(()=>{for(let i=0;i<5;i++)navigator.serviceWorker?.dispatchEvent(new MessageEvent('message',{data:{type:'vmbox-push'}}))});
  await new Promise(resolve=>setTimeout(resolve,650));
  assert.equal(requests.filter(r=>r==='GET /v1/grid-boxes').length,beforeListRefresh+1,'push burst should fetch the list once');
  await p.mouse.move(0,0);
  await p.focus('#chat-header-avatar .preview-trigger');
  await p.waitForFunction(()=>!document.querySelector('.tv-preview').hidden);
  await p.evaluate(()=>document.activeElement.blur());
  await p.waitForFunction(()=>document.querySelector('.tv-preview').hidden,{timeout:2000});
  for(const selector of ['#chat-entries [data-avatar="builder"]','#chat-header-avatar [data-avatar="builder"]']){
   await p.$eval(selector,e=>e.dispatchEvent(new MouseEvent('mouseenter')));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),false,selector+' did not open the desktop preview');
   await p.waitForFunction(()=>document.querySelector('.tv-preview img')?.naturalWidth===1);
   await p.waitForFunction(()=>window.viewerPreview?.viewOnly===true,{timeout:5000});
   await p.$eval(selector,e=>e.dispatchEvent(new MouseEvent('mouseleave',{relatedTarget:document.querySelector('.tv-preview')})));
   await p.$eval('.tv-preview',e=>e.dispatchEvent(new MouseEvent('mouseenter')));
   await new Promise(resolve=>setTimeout(resolve,200));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),false,selector+' closed while cursor moved into preview');
   await p.$eval('#refresh',button=>button.click());
   await new Promise(resolve=>setTimeout(resolve,300));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),false,selector+' closed during a chat refresh');
   await p.hover('.tv-preview');
   await p.$eval('#chat-messages',messages=>messages.dispatchEvent(new Event('scroll')));
   assert.equal(await p.$eval('.tv-preview',e=>e.hidden),false,selector+' closed during background chat scrolling');
   await p.waitForFunction(()=>!document.querySelector('.tv-preview-timeline input').disabled);
   await p.$eval('.tv-preview-timeline input',e=>{e.value='0';e.dispatchEvent(new Event('input',{bubbles:true}))});
   await p.waitForFunction(()=>document.querySelector('.tv-preview-note').textContent.startsWith('Replay'));
   assert.equal(await p.$eval('.tv-preview-timeline input',e=>e.value),'0');
   await p.$eval('.tv-preview-timeline button',e=>e.click());
   await p.waitForFunction(()=>document.querySelector('.tv-preview-note').textContent.startsWith('Live'));
   await p.$eval('.tv-preview',e=>e.dispatchEvent(new MouseEvent('mouseleave')));
   await p.waitForFunction(()=>document.querySelector('.tv-preview').hidden,{timeout:2000});
  }
  assert.equal(fullDesktopShots>0,true);
  await p.$eval('#chat-entries [data-avatar="reviewer"]',element=>element.click());
  await p.waitForFunction(()=>!document.querySelector('#takeover').hidden&&document.querySelector('[data-box-id="reviewer"]').classList.contains('active'),{timeout:5000});
  assert.equal(new URL(p.url()).hash,'#box=reviewer');
  assert.equal(await p.$eval('#takeover-tabs [data-kind="desktop"]',button=>button.classList.contains('on')),true);
  await p.click('#takeover-tabs [data-kind="terminal"]');
  await p.waitForFunction(()=>window.viewerTerminal?.id==='reviewer',{timeout:5000});
  assert.deepEqual(await p.evaluate(()=>window.viewerTerminal),{id:'reviewer',session:'codex-reviewer',root:'takeover-screen'});
  await p.click('#takeover-close');await p.waitForFunction(()=>document.querySelector('#takeover').hidden);await p.$eval('[data-box-id="builder"]',element=>element.click());
  await p.waitForFunction(()=>document.querySelector('[data-box-id="builder"]').classList.contains('active'),{timeout:5000});
  assert.equal(await p.$eval('#chat-messages .msg.user .ticks svg',e=>!!e),true,'delivery ticks render as inline Lucide icons');
  await p.setViewport({width:1280,height:900,deviceScaleFactor:1});
  await p.$eval('#chat-info',e=>e.click());
  await p.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  const panelLayout=await p.evaluate(()=>{
   const panel=document.querySelector('#inspect'),header=document.querySelector('#chat-header');
   return {parent:panel.parentElement.id,panel:panel.getBoundingClientRect().toJSON(),header:header.getBoundingClientRect().toJSON()};
  });
  assert.equal(panelLayout.parent,'chat-conversation','details must expand from the viewed chat header');
  assert.ok(panelLayout.panel.top>=panelLayout.header.bottom-2,'details must appear below the chat header');
  assert.ok(panelLayout.panel.width>=panelLayout.header.width*.95,'details must span the chat instead of a side drawer');
  await p.waitForFunction(()=>document.querySelector('#inspect-contact-role').textContent.includes('directly')&&document.querySelector('#inspect-tags').textContent.includes('backend'));
  assert.match(await p.$eval('#inspect-contact-status',e=>e.textContent),/contact directly/);
  assert.equal(await p.$$eval('#inspect-contact-list li',rows=>rows.length),3,'every eligible box renders with its effective access');
  assert.equal(await p.$eval('#inspect-contact-list li:nth-child(2) button',button=>button.textContent),'Add contact');
  await p.click('#inspect-contact-list li:nth-child(2) button');
  await p.waitForFunction(()=>document.querySelector('#inspect-contact-list li:nth-child(2) button').textContent==='Remove contact');
  assert.equal(requests.includes('PUT /v1/logical-boxes/builder/contacts'),true);
  await p.$eval('#inspect-toggle-protection',e=>e.click());
  await p.waitForFunction(()=>document.querySelector('#inspect-protection-label').textContent.startsWith('Protected'));
  assert.equal(requests.includes('PUT /v1/logical-boxes/builder/protection'),true);
  await (await p.$('#inspect')).screenshot({path:'docs/chat-ui/screenshots/desktop-chat-contacts.png'});
  await p.setViewport({width:420,height:1200,deviceScaleFactor:1});
  await p.evaluate(()=>{const inspect=document.querySelector('#inspect'),body=document.querySelector('#inspect-body'),contacts=document.querySelector('#inspect-contacts');inspect.style.maxHeight='none';inspect.style.overflow='visible';body.style.overflow='visible';contacts.style.overflow='visible'});
  await (await p.$('#inspect-contacts')).screenshot({path:'docs/chat-ui/screenshots/mobile-chat-contacts.png'});
  await p.click('#inspect-edit-roles');
  await p.waitForFunction(()=>!document.querySelector('#role-editor-modal').hidden&&document.querySelector('#role-editor-title').textContent.includes('builder'));
  await p.click('#role-editor-form .mcp-tool-options summary');
  assert.match(await p.$eval('#role-editor-form label:has(input[value=secret_request])',element=>element.title),/privately/);
  assert.match(await p.$eval('#role-editor-form label:has(input[value=create_agent_box])',element=>element.title),/startup instructions/);
  await p.click('#role-editor-form .mcp-tool-group-toggle[value=computer_use]');
  assert.equal(await p.$eval('#role-editor-form .mcp-tool-group-toggle[value=computer_use]',input=>input.checked),true);
  assert.equal(await p.$$eval('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=computer_use]) input[name=mcpTools]',inputs=>inputs.every(input=>input.checked)),true);
  await p.click('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=computer_use]) .role-capability-options summary');
  await p.click('#role-editor-form input[name=mcpTools][value=drag_mouse]');
  assert.equal(await p.$eval('#role-editor-form .mcp-tool-group-toggle[value=computer_use]',input=>input.indeterminate),true);
  await p.click('#role-editor-form .mcp-tool-group-toggle[value=computer_use]');
  assert.equal(await p.$$eval('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=computer_use]) input[name=mcpTools]',inputs=>inputs.every(input=>input.checked)),true);
  await p.$eval('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=computer_use]) .role-capability-options',element=>element.open=false);
  await p.click('#role-editor-form .mcp-tool-group-toggle[value=admin_work]');
  assert.deepEqual(await p.$$eval('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=admin_work]) input[name=mcpTools]',inputs=>inputs.map(input=>input.value)),['list_agent_boxes','get_agent_box','create_agent_box','set_agent_box_tags','restart_agent_box','delete_agent_box']);
  await p.click('#role-editor-form .mcp-tool-group:has(.mcp-tool-group-toggle[value=admin_work]) .role-capability-options summary');
  await (await p.$('#role-editor-form .role-capability:has(.mcp-tool-options)')).screenshot({path:'docs/chat-ui/screenshots/chat-permission-mcp-tools.png'});
  await p.$eval('#role-editor-form .mcp-tool-options',element=>element.open=false);
  await p.$eval('.role-capabilities',element=>element.scrollIntoView({block:'start'}));
  await (await p.$('#role-editor-modal .role-editor-card')).screenshot({path:'docs/chat-ui/screenshots/chat-permission-editor.png'});
  await p.$eval('#role-editor-form',form=>form.requestSubmit());
  await p.waitForFunction(()=>document.querySelector('#role-editor-status').textContent.startsWith('Saved.'));
  assert.equal(await p.$eval('#role-editor-modal',modal=>modal.hidden),true,'saving permissions closes the editor');
  const saved=directPolicies.get('builder').capabilities;
  assert.equal(saved.mcpTools.enabled,true);
  assert.equal(saved.mcpTools.allowedTools.includes('drag_mouse'),true);
  assert.equal(saved.mcpTools.allowedTools.includes('click_mouse'),true);
  assert.equal(saved.mcpTools.allowedTools.includes('secret_request'),false);
  assert.deepEqual(saved.manageAgentBoxes,{list:true,inspect:true,tag:true,restart:true,delete:true});
  assert.equal(saved.mcpTools.allowedTools.includes('create_agent_box'),true);
  await p.evaluate(()=>[...document.querySelectorAll('#inspect-config-actions button')].find(button=>button.textContent==='Re-sync instructions').click());
  await p.waitForFunction(()=>document.querySelector('#inspect').hidden);
  assert.equal(requests.includes('POST /v1/logical-boxes/builder/instructions/resync'),true);
  await p.$eval('#roles-toggle',button=>button.click());
  await p.waitForFunction(()=>!document.querySelector('#roles-modal').hidden&&document.querySelectorAll('#role-assignments .role-assignment-card').length===4);
  await p.setViewport({width:1280,height:900,deviceScaleFactor:1});
  await (await p.$('#roles-modal .roles-card')).screenshot({path:'docs/chat-ui/screenshots/chat-permissions.png'});
  await p.setViewport({width:390,height:844,deviceScaleFactor:1});
  assert.equal(await p.$('#role-assignments table'),null,'permissions should not fall back to a matrix on mobile');
  assert.equal(await p.$eval('#role-assignments .role-assignment-list',element=>getComputedStyle(element).display),'grid');
  assert.equal(await p.$eval('#role-assignments .role-assignment-card[data-role-box-id="reviewer"]',element=>element.getBoundingClientRect().width>350),true);
  await p.waitForFunction(()=>document.querySelector('#chat-toasts').childElementCount===0,{timeout:5000});
  await p.$eval('#roles-modal .roles-card',element=>element.scrollTop=0);
  await (await p.$('#roles-modal .roles-card')).screenshot({path:'docs/chat-ui/screenshots/chat-permissions-mobile.png'});
  await p.click('#roles-modal .roles-card [data-close="roles-modal"]');
  await p.setViewport({width:420,height:820,deviceScaleFactor:1});
  await p.$eval('#new-box',button=>button.click());
  await p.waitForSelector('#create-box select[name=loginProfile]',{timeout:5000});
  await p.select('#create-box select[name=defaultAgent]','opencode');
  await p.select('#create-box select[name=loginProfile]',JSON.stringify({application:'opencode',name:'openrouter'}));
  await p.click('#create-box .model-picker-open');
  await p.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('OpenRouter live catalog'));
  assert.ok((await p.$$eval('dialog.model-picker-dialog .model-picker-option',nodes=>nodes.map(node=>node.dataset.model))).includes('openrouter/live-model'));
  await p.click('dialog.model-picker-dialog .model-picker-close');
  await p.select('#create-box select[name=defaultAgent]','claude');
  await p.select('#create-box select[name=loginProfile]',JSON.stringify({application:'claude',name:'personal'}));
  await p.click('#create-box .model-picker-open');
  await p.waitForSelector('dialog.model-picker-dialog[open]');
  const claude=await p.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.map(node=>node.dataset.model));
  for(const model of ['sonnet','opus','haiku','fable','sonnet[1m]','opus[1m]','claude-sonnet-5','claude-opus-5','claude-haiku-4-5-20251001','claude-fable-5-1'])assert.ok(claude.includes(model),model+' is offered');
  await p.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.find(node=>node.dataset.model==='haiku').click());
  assert.equal(await p.$eval('dialog.model-picker-dialog .model-picker-effort',element=>element.disabled),true);
  await p.click('dialog.model-picker-dialog .model-picker-apply');
  assert.equal(await p.$eval('#create-box input[name=agentModel]',input=>input.value),'haiku');
  await p.click('#create-box .model-picker-open');
  await p.keyboard.press('Escape');
  assert.equal(await p.$eval('#new-box-modal',modal=>modal.hidden),false,'closing model choices must not close the box form');
  await p.select('#create-box select[name=githubProfile]',JSON.stringify({application:'github',name:'gh-work'}));
  await p.type('#create-box input[name=name]','github-chat-fixture');
  await p.$eval('#create-box',form=>form.requestSubmit());
  await p.waitForFunction(()=>document.querySelector('#new-box-modal').hidden,{timeout:5000});
  assert.deepEqual(creations.at(-1).loginProfiles,[{application:'claude',name:'personal',model:'haiku'},{application:'github',name:'gh-work'}]);
  assert.equal('roleIds' in creations.at(-1),false,'new boxes do not inherit a role bundle');
  await p.close();
 }finally{await browser.close();await new Promise(r=>server.close(r))}
});
