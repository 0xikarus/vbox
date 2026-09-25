import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import {after,before,test} from 'node:test';
import {resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

const root=resolve('internal/controller/web'),requests=[];
let server,browser,base;
const revision='2026-09-05T12:00:00Z';
let fixtureRoles=[],fixtureBoxRoleIds=[],fixturePolicy={capabilities:{requestMoreTime:{maxExtensionMinutes:0,maxTotalMinutes:0},queueFollowup:{maxPending:0},createAgentBox:{maxBoxes:0,maxDiskGiB:0},createEmailAddress:{maxAddresses:0}}};
before(async()=>{
 server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://test').pathname;
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const body=chunks.length?JSON.parse(Buffer.concat(chunks)):null;
  requests.push({path,method:req.method,body,revision:req.headers['if-match']});
   if(['/','/app.js','/app.css','/controller.css','/manager-theme.css','/markdown.js','/model-picker.js','/ai-helper.js','/ai-helper.css','/workspace-nav.js','/workspace-nav.css','/favicon.ico','/workspace.js','/workspace-terminal.js','/workspace-desktop.js','/novnc.js','/workspace.css','/xterm.js','/xterm-fit.js','/xterm.css','/boxes/box-1'].includes(path)){
   const file=path==='/boxes/box-1'?'workspace.html':path==='/'?'index.html':path==='/favicon.ico'?'favicon.svg':path.slice(1);
   res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
   return res.end(await readFile(resolve(root,file)));
  }
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/browser-session' && ['POST','DELETE'].includes(req.method)){res.statusCode=204;return res.end()}
  const values={
   '/v1/tool-presets':[{id:'desktop',name:'Enable desktop',version:'worker packages',description:'Desktop and browser'},{id:'foundry',name:'Foundry',version:'v1.8.1',description:'forge, cast, anvil, chisel'},{id:'blender',name:'Blender',version:'5.1.2',description:'3D editor + desktop + MCP'}],
   '/v1/capabilities':{providerEdits:true,nativeAttach:true},
   '/v1/logical-boxes':[{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude',roles:fixtureRoles.filter(role=>fixtureBoxRoleIds.includes(role.id)).map(({id,name})=>({id,name}))}],
   '/v1/logical-boxes/box-1':{id:'box-1',name:'helper ü',state:'running',roles:fixtureRoles.filter(role=>fixtureBoxRoleIds.includes(role.id)).map(({id,name})=>({id,name}))},
   '/v1/agent-roles':fixtureRoles,
   '/v1/provider-credentials':[{provider:'railway',name:'primary',config:{projectId:'p',environmentId:'e',image:'old'},updatedAt:revision}],
   '/v1/provider-schemas':{providers:{railway:{image:'string'}}},
   '/v1/controller-defaults':{provider:'railway',providerCredential:'primary'},
   '/v1/fleet/status':{desiredSlots:2,actualSlots:2,freeSlots:1,occupiedSlots:1,unhealthySlots:0,slots:[{ordinal:1,state:'occupied',health:'healthy',region:'europe-west4',logicalBoxName:'helper ü'},{ordinal:2,state:'free',health:'healthy',region:'europe-west4'}]},
   '/v1/fleet/costs':{provider:'railway',providerCredential:'primary',period:'current provider billing period',observedAt:revision,total:{currency:'USD',accrued:1.23,available:true,detail:'Sum of available fleet service costs.'},availableSlotCount:1,unavailableSlotCount:1,slots:[{ordinal:1,state:'occupied',logicalBoxName:'helper ü',cost:{currency:'USD',accrued:1.23,available:true,detail:'Railway service entries'}},{ordinal:2,state:'free',cost:{currency:'USD',available:false,detail:'Project token cannot read billing'}}]},
   '/v1/notifications':[],
   '/v1/whoami':{accountId:'account-1',accountName:'Team',role:'owner'},
   '/v1/login-profiles':[{application:'claude',name:'personal',model:'opus[1m]',createdAt:revision},{application:'codex',name:'personal-codex',model:'account-codex-model',createdAt:revision},{application:'opencode',name:'openrouter',model:'openrouter/deepseek/deepseek-v4.1-flash',createdAt:revision},{application:'opencode',name:'venice',model:'venice/deepseek-v4-1-flash',createdAt:revision},{application:'github',name:'gh-work',createdAt:revision}],
   '/v1/instruction-presets':{defaultName:'general',presets:[{name:'general',revision:2,sizeBytes:64,default:true,createdAt:revision,updatedAt:revision}]},
   '/v1/instruction-presets/general':{preset:{name:'general',revision:2,sizeBytes:64,default:true,markdown:'# House rules\nAlways answer briefly. <img src=x onerror="window.pwned=1">',createdAt:revision,updatedAt:revision}},
   '/v1/logical-boxes/box-1/instructions':{instructions:{source:'none',markdown:'',updatedAt:revision},effectiveMarkdown:'## vmbox chat delivery\nUse chat_message with Message-ID.',pending:false},
   '/v1/logical-boxes/box-1/imported-credentials':{profiles:[],pending:[],verified:true},
  };
  if(req.method==='GET' && path in values)return res.end(JSON.stringify(values[path]));
  if(path==='/v1/ai/openrouter'&&req.method==='GET')return res.end(JSON.stringify({configured:false,model:'openrouter/auto'}));
  if(req.method==='GET' && path==='/v1/login-profiles/claude/personal/models')return res.end(JSON.stringify({source:'Claude Code catalog',models:['sonnet','opus','haiku','fable','sonnet[1m]','opus[1m]','claude-sonnet-5','claude-opus-5','claude-haiku-4-5-20251001','claude-fable-5-1'].map(id=>({id,label:id}))}));
  if(req.method==='GET' && path==='/v1/login-profiles/opencode/openrouter/models')return res.end(JSON.stringify({source:'OpenRouter live catalog',models:[{id:'openrouter/deepseek/deepseek-v4.1-flash',label:'DeepSeek Flash',reasoning:false},{id:'openrouter/google/gemini-test',label:'Gemini test',reasoning:true}]}));
  if(req.method==='GET' && path==='/v1/login-profiles/codex/personal-codex/models')return res.end(JSON.stringify({source:'Codex account catalog',models:[{id:'account-codex-model',label:'Account Codex Model',reasoning:true,reasoningEfforts:['low','ultra']}]}));
  if(req.method==='POST' && path==='/v1/logical-boxes/box-1/sessions/interactive')return res.end(JSON.stringify({session:'persistent-shell'}));
  if(req.method==='PATCH' && (path==='/v1/logical-boxes/box-1'||path==='/v1/provider-credentials/railway/primary'))return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/fleet/slots')return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/login-profiles/codex/browser-test')return res.end(JSON.stringify({application:'codex',name:'browser-test'}));
  if(req.method==='PUT' && path==='/v1/logical-boxes/box-1/instructions')return res.end(JSON.stringify({...values['/v1/logical-boxes/box-1/instructions'],note:'fixture applied'}));
  if(req.method==='PUT' && path==='/v1/logical-boxes/box-1/login-profiles')return res.end(JSON.stringify({profiles:body.profiles,pending:[],verified:true,note:'fixture applied'}));
  if(req.method==='GET' && path==='/v1/logical-boxes/box-1/agent-policy')return res.end(JSON.stringify({boxId:'box-1',boxName:'helper ü',...fixturePolicy}));
  if(req.method==='PUT' && path==='/v1/logical-boxes/box-1/agent-policy'){fixturePolicy=body;return res.end(JSON.stringify({boxId:'box-1',boxName:'helper ü',...fixturePolicy}))}
  if(req.method==='POST' && path==='/v1/agent-roles'){const role={id:'role-'+(fixtureRoles.length+1),...body,assignedBoxCount:0,createdAt:revision,updatedAt:revision};fixtureRoles.push(role);res.statusCode=201;return res.end(JSON.stringify(role))}
  if(req.method==='PUT' && path.startsWith('/v1/agent-roles/')){const id=decodeURIComponent(path.split('/').pop()),index=fixtureRoles.findIndex(role=>role.id===id);fixtureRoles[index]={...fixtureRoles[index],...body};return res.end(JSON.stringify(fixtureRoles[index]))}
  if(req.method==='DELETE' && path.startsWith('/v1/agent-roles/')){const id=decodeURIComponent(path.split('/').pop());fixtureRoles=fixtureRoles.filter(role=>role.id!==id);fixtureBoxRoleIds=fixtureBoxRoleIds.filter(roleID=>roleID!==id);res.statusCode=204;return res.end()}
  if(req.method==='PUT' && path==='/v1/agent-role-assignments'){fixtureBoxRoleIds=body.assignments.find(assignment=>assignment.boxId==='box-1')?.roleIds||[];for(const role of fixtureRoles)role.assignedBoxCount=fixtureBoxRoleIds.includes(role.id)?1:0;res.statusCode=204;return res.end()}
  if(req.method==='PUT' && (path==='/v1/instruction-presets-default'||path==='/v1/instruction-presets/general')){res.statusCode=204;return res.end()}
  if(req.method==='POST' && path==='/v1/logical-boxes')return res.end(JSON.stringify({id:'created'}));
  if(req.method==='DELETE' && path==='/v1/login-profiles/claude/personal'){res.statusCode=204;return res.end()}
  res.statusCode=404;res.end(JSON.stringify({error:'unexpected endpoint'}));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-setuid-sandbox']});
});
after(async()=>{await browser?.close();await new Promise(r=>server?.close(r))});
test('management views expose box placement and keep details easy to close',async()=>{
 const page=await browser.newPage();await page.setViewport({width:1280,height:900});
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;
  window.fetch=async(path,options)=>{
   const response=await original(path,options),url=new URL(path,location.origin);
   if(url.pathname==='/v1/logical-boxes'&&(!options?.method||options.method==='GET')){
    const boxes=await response.json();
    return new Response(JSON.stringify(boxes.map(box=>({...box,provider:'railway',providerCredential:'primary',slotId:'slot-1'}))),{status:response.status,headers:response.headers});
   }
   if(url.pathname==='/v1/fleet/status'&&(!options?.method||options.method==='GET')){
    const fleet=await response.json();fleet.slots[0].id='slot-1';fleet.slots[0].serviceName='railway-worker-01';
    return new Response(JSON.stringify(fleet),{status:response.status,headers:response.headers});
   }
   return response;
  };
 });
 await page.goto(base+'/#boxes');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForFunction(()=>document.querySelector('.box-placement')?.textContent.includes('railway-worker-01'));
 assert.equal(await page.$eval('body',body=>body.dataset.manageView),'boxes');
 assert.equal(await page.$eval('.manage-top .brand',brand=>brand.textContent.trim()),'vmbox / boxes');
 assert.equal(await page.title(),'vmbox / boxes');
 assert.equal(await page.$eval('#providers',section=>getComputedStyle(section).display),'none');
 await page.click('[aria-label="Details for box helper ü"]');
 assert.equal(await page.$eval('#box-detail',drawer=>drawer.hidden),false);
 assert.match(await page.$eval('#box-detail-body',body=>body.textContent),/railway-worker-01/);
 await page.keyboard.press('Escape');
 assert.equal(await page.$eval('#box-detail',drawer=>drawer.hidden),true);
 await page.click('.workspace-links a[href="#providers"]');
 assert.equal(await page.$eval('body',body=>body.dataset.manageView),'providers');
 assert.equal(await page.$eval('.manage-top .brand',brand=>brand.textContent.trim()),'vmbox / providers');
 assert.equal(await page.title(),'vmbox / providers');
 assert.equal(await page.$eval('#boxes',section=>getComputedStyle(section).display),'none');
 await page.waitForSelector('.provider-card');
 await page.setViewport({width:390,height:844});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await page.close();
});
test('Profiles has its own view with saved agent and GitHub logins',async()=>{
 const page=await browser.newPage();await page.setViewport({width:1280,height:900});
 await page.goto(base+'/#profiles');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#profile-tree .profile-app[data-application="github"]');
 assert.equal(await page.$eval('body',body=>body.dataset.manageView),'profiles');
 assert.equal(await page.$eval('.manage-top .brand',brand=>brand.textContent.trim()),'vmbox / profiles');
 assert.equal(await page.title(),'vmbox / profiles');
 assert.equal(await page.$eval('.workspace-links a[href="#profiles"]',link=>link.getAttribute('aria-current')),'page');
 assert.equal(await page.$eval('#boxes',section=>getComputedStyle(section).display),'none');
 assert.equal(await page.$eval('#profile-summary',summary=>summary.textContent),'5 saved profiles · Team');
 assert.deepEqual(await page.$$eval('#profile-tree .profile-app',cards=>cards.map(card=>[card.dataset.application,card.querySelectorAll('.profile-row').length])),[['claude',1],['codex',1],['opencode',2],['github',1]]);
 assert.match(await page.$eval('.profile-app[data-application="github"]',card=>card.textContent),/gh-work/);
 assert.match(await page.$eval('.profile-app[data-application="opencode"]',card=>card.textContent),/venice\/deepseek-v4-1-flash/);
 assert.match(await page.$eval('.ai-settings-card',card=>card.textContent),/AI writing helper/);
 if(process.env.VMBOX_AI_SCREENSHOTS)await page.screenshot({path:process.env.VMBOX_AI_SCREENSHOTS+'/ai-profiles-desktop.png'});
 await page.click('#ai-settings-open');await page.waitForSelector('.ai-settings-dialog[open]');
 assert.match(await page.$eval('.ai-settings-state',node=>node.textContent),/No dedicated key/);
 await page.click('.ai-settings-close');
 await page.type('#profile-search','venice');
 assert.deepEqual(await page.$$eval('#profile-tree .profile-app',cards=>cards.map(card=>[card.dataset.application,card.querySelectorAll('.profile-row').length])),[['opencode',1]]);
 await page.setViewport({width:390,height:844});
 await page.$eval('#profile-search',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
 assert.equal(await page.$$('#profile-tree .profile-app').then(cards=>cards.length),4);
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 if(process.env.VMBOX_AI_SCREENSHOTS)await page.screenshot({path:process.env.VMBOX_AI_SCREENSHOTS+'/ai-profiles-mobile.png'});
 await page.click('.workspace-links a[href="#boxes"]');
 await page.waitForFunction(()=>document.body.dataset.manageView==='boxes');
 await page.close();
});
test('secondary management sections and box workspaces use the same page navbar',async()=>{
 const page=await browser.newPage();
 await page.goto(base+'/#roles');
 assert.equal(await page.$eval('.manage-top .brand',brand=>brand.textContent.trim()),'vmbox / permissions');
 assert.equal(await page.title(),'vmbox / permissions');
 assert.equal(await page.$eval('.manage-subnav a[href="#roles"]',link=>link.getAttribute('aria-current')),'page');
 for(const [section,label] of [['instructions','instructions'],['fleet','capacity'],['notifications','notifications']]){
  await page.evaluate(value=>{location.hash=value},section);
  await page.waitForFunction(expected=>document.querySelector('#manage-page-label').textContent===expected,{},label);
  assert.equal(await page.$eval('.manage-top .brand',brand=>brand.textContent.trim()),'vmbox / '+label);
  assert.equal(await page.title(),'vmbox / '+label);
 }
 await page.goto(base+'/boxes/box-1');
 assert.equal(await page.$eval('.workspace-top .brand',brand=>brand.textContent.trim()),'vmbox / workspace');
 assert.deepEqual(await page.$$eval('.workspace-top .workspace-links a',links=>links.map(link=>link.textContent)),['Chats','Grid','Boxes','Providers','Profiles']);
 await page.setViewport({width:390,height:844});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 await page.close();
});
test('direct per-box permissions can be edited without a role matrix',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;
  window.fetch=async(path,options={})=>{
   const response=await original(path,options),url=new URL(path,location.origin),method=options.method||'GET';
   if(method!=='GET'||url.pathname!=='/v1/logical-boxes')return response;
   const boxes=await response.json();
   return new Response(JSON.stringify([...boxes,
    {id:'box-2',name:'planner',state:'running',defaultAgent:'codex',roles:[]},
    {id:'box-3',name:'build runner',state:'running',defaultAgent:'claude',roles:[]},
    {id:'box-4',name:'qa reviewer',state:'hibernated',defaultAgent:'opencode',roles:[]}
   ]),{status:response.status,headers:response.headers});
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#role-assignments .role-assignment-card[data-role-box-id="box-1"]');
 await page.click('#role-assignments .role-assignment-card[data-role-box-id="box-1"] button');
 await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='');
 assert.equal(await page.$eval('#role-editor-form',form=>form.checkValidity()),true,'disabled zero limits must not block permission saving');
 await page.click('#role-editor-form input[name=allContactsEnabled]');
 await page.click('#role-editor-form .mcp-tool-options summary');
 assert.equal(await page.$('#role-editor-form .mcp-tool-group-toggle[value=coordination]'),null);
 await page.click('#role-editor-form .mcp-tool-group-toggle[value=admin_work]');
 await page.click('#role-editor-form .mcp-tool-group-toggle[value=computer_use]');
 await page.click('#role-editor-form button.primary');
 await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('Permissions updated'));
 assert.equal(fixturePolicy.capabilities.allContacts.enabled,true);
 assert.deepEqual(fixturePolicy.capabilities.createAgentBox,{enabled:true,maxBoxes:3,maxDiskGiB:50,allowedAgents:['codex','claude','opencode'],assignableRoleIds:[]});
 assert.equal(fixturePolicy.capabilities.requestMoreTime,undefined);
 assert.equal(fixturePolicy.capabilities.queueFollowup,undefined);
 assert.equal(fixturePolicy.capabilities.sharedChats,undefined);
 assert.equal(fixturePolicy.capabilities.mcpTools.allowedTools.includes('press_keys'),true);
 assert.equal(fixturePolicy.capabilities.mcpTools.allowedTools.includes('secret_request'),false);
 assert.equal(await page.$('#role-assignments table'),null,'permissions must not use a role matrix');
 assert.equal(await page.$('#create-role:not([hidden])'),null,'there is no role creation workflow');
 await page.setViewport({width:1280,height:900});
 await page.$eval('a[href="#roles"]',link=>link.click());
 await (await page.$('#roles')).screenshot({path:resolve('docs/screenshots/agent-roles/permissions-desktop.png')});
 await page.click('#role-assignments .role-assignment-card[data-role-box-id="box-1"] button');
 await page.setViewport({width:1280,height:1200});
 await (await page.$('#role-editor-modal .card')).screenshot({path:resolve('docs/screenshots/agent-roles/permission-editor.png')});
 await page.click('#role-editor-modal header [data-close="role-editor-modal"]');
 await page.setViewport({width:390,height:844});
 assert.equal(await page.$('#role-assignments table'),null,'permissions should not fall back to a matrix on mobile');
 assert.equal(await page.$eval('#role-assignments .role-assignment-list',element=>getComputedStyle(element).display),'grid');
 assert.equal(await page.$eval('#role-assignments .role-assignment-card[data-role-box-id="box-1"]',element=>element.getBoundingClientRect().width>350),true);
 await (await page.$('#roles')).screenshot({path:resolve('docs/screenshots/agent-roles/permissions-mobile.png')});
 await page.close();
});
test('desktop is implicit in creation and Blender remains optional',async()=>{
 const page=await browser.newPage();
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#create-tools input[value=blender]');
 assert.equal(await page.$('#create-tools input[value=desktop]'),null);
 await page.type('#create input[name=name]','disposable-desktop-fixture');
 const created=page.waitForResponse(response=>response.request().method()==='POST'&&response.url().endsWith('/v1/logical-boxes'));
 await page.click('#create button[type=submit]');await created;
 assert.deepEqual(requests.findLast(request=>request.method==='POST'&&request.path==='/v1/logical-boxes').body.tools,['desktop']);
 await page.close();
});
test('successful creation clears the form and returns to the top',async()=>{
 const page=await browser.newPage();await page.setViewport({width:390,height:844});
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#create-tools input[value=foundry]');
 await page.type('#create input[name=name]','disposable-reset-fixture');
 await page.$eval('#create input[name=disk]',input=>input.value='20');
 await page.select('#create select[name=defaultAgent]','opencode');
 await page.select('#profile-choices select[name=loginProfile]',JSON.stringify({application:'opencode',name:'openrouter'}));
 await page.select('#profile-choices select[name=githubProfile]',JSON.stringify({application:'github',name:'gh-work'}));
 await page.click('#create-tools input[value=foundry]');
 await page.type('#create textarea[name=setupScript]','echo fixture');
 await page.select('#create-instructions','none');
 await page.evaluate(()=>window.scrollTo(0,document.body.scrollHeight));
 const created=page.waitForResponse(response=>response.request().method()==='POST'&&response.url().endsWith('/v1/logical-boxes'));
 await page.click('#create button[type=submit]');await created;
 await page.waitForFunction(()=>{
  const form=document.querySelector('#create');
  return form.elements.name.value===''&&form.elements.disk.value==='10'&&form.elements.defaultAgent.value==='claude'&&form.elements.loginProfile.value===''&&form.elements.agentModel.value===''&&form.elements.githubProfile.value===''&&!form.querySelector('input[value=foundry]').checked&&form.elements.setupScript.value===''&&form.elements.instructions.value==='auto'&&form.querySelector('.model-picker-open').textContent==='Choose model'&&window.scrollY===0;
 },{timeout:5000});
 await page.close();
});
test('model choice opens a searchable modal and loads the provider catalog',async()=>{
 const page=await browser.newPage();
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#profile-choices select');
 await page.select('#create select[name=defaultAgent]','opencode');
 await page.select('#profile-choices select',JSON.stringify({application:'opencode',name:'openrouter'}));
 const input='#create input[name=agentModel]';
 assert.equal(await page.$eval(input,e=>e.type),'hidden');
 await page.click('#create .model-picker-open');
 await page.waitForSelector('dialog.model-picker-dialog[open]');
 assert.equal(await page.$eval('dialog.model-picker-dialog .model-picker-effort',element=>element.value),'');
 await page.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('OpenRouter live catalog'));
 assert.equal(await page.$eval('dialog.model-picker-dialog .model-picker-effort',element=>element.disabled),true,'non-reasoning model has no effort choice');
 assert.deepEqual(await page.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.map(node=>node.dataset.model)),['openrouter/deepseek/deepseek-v4.1-flash','openrouter/google/gemini-test']);
 await page.type('.model-picker-search','gemini');
 assert.deepEqual(await page.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.map(node=>node.dataset.model)),['openrouter/google/gemini-test']);
 await page.click('dialog.model-picker-dialog .model-picker-option:not([hidden])');
 assert.equal(await page.$eval(input,e=>e.value),'openrouter/deepseek/deepseek-v4.1-flash','model changes only after applying the popup');
 assert.equal(await page.$eval('dialog.model-picker-dialog',e=>e.open),true);
 await page.select('dialog.model-picker-dialog .model-picker-effort','medium');
 await page.click('dialog.model-picker-dialog .model-picker-apply');
 assert.equal(await page.$eval(input,e=>e.value),'openrouter/google/gemini-test');
 assert.equal(await page.$eval('dialog.model-picker-dialog',e=>e.open),false);
 assert.equal(await page.$eval('#create input[name=agentReasoningEffort]',e=>e.value),'medium');
 assert.ok(requests.some(request=>request.path==='/v1/login-profiles/opencode/openrouter/models'));
 await page.select('#profile-choices select',JSON.stringify({application:'opencode',name:'venice'}));
 await page.click('#create .model-picker-open');
 await page.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('Could not load provider models'));
 assert.deepEqual(await page.$$eval('dialog.model-picker-dialog .model-picker-option',nodes=>nodes.map(node=>node.dataset.model)),['venice/deepseek-v4-1-flash']);
 await page.click('dialog.model-picker-dialog .model-picker-close');
 await page.select('#create select[name=defaultAgent]','codex');
 await page.select('#profile-choices select',JSON.stringify({application:'codex',name:'personal-codex'}));
 await page.click('#create .model-picker-open');
 await page.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('Codex account catalog'));
 assert.deepEqual(await page.$$eval('dialog.model-picker-dialog .model-picker-option',nodes=>nodes.map(node=>node.dataset.model)),['account-codex-model']);
 await page.click('dialog.model-picker-dialog .model-picker-option');
 assert.deepEqual(await page.$$eval('dialog.model-picker-dialog .model-picker-effort option',nodes=>nodes.map(node=>node.value)),['','low','ultra']);
 await page.click('dialog.model-picker-dialog .model-picker-close');
 assert.ok(requests.some(request=>request.path==='/v1/login-profiles/codex/personal-codex/models'));
 await page.close();
});
test('creation offers documented Claude choices and Codex account models',async()=>{
 const page=await browser.newPage();
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#profile-choices select');
 await page.select('#create select[name=defaultAgent]','claude');
 await page.select('#profile-choices select[name=loginProfile]',JSON.stringify({application:'claude',name:'personal'}));
 const input='#create input[name=agentModel]';
 assert.equal(await page.$eval(input,e=>e.value),'opus[1m]');
 await page.click('#create .model-picker-open');
 await page.waitForSelector('dialog.model-picker-dialog[open]');
 await page.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('Claude Code catalog'));
 const claude=await page.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.map(node=>node.dataset.model));
 for(const model of ['sonnet','opus','haiku','fable','sonnet[1m]','opus[1m]','claude-sonnet-5','claude-opus-5','claude-haiku-4-5-20251001','claude-fable-5-1'])assert.ok(claude.includes(model),model+' is offered');
 assert.match(await page.$eval('.model-picker-source',node=>node.textContent),/Claude Code/);
 await page.click('dialog.model-picker-dialog .model-picker-close');
 await page.select('#create select[name=defaultAgent]','codex');
 await page.select('#profile-choices select[name=loginProfile]',JSON.stringify({application:'codex',name:'personal-codex'}));
 assert.equal(await page.$eval(input,e=>e.value),'account-codex-model');
 await page.click('#create .model-picker-open');
 await page.waitForFunction(()=>document.querySelector('.model-picker-source')?.textContent.includes('Codex account catalog'));
 const codex=await page.$$eval('dialog.model-picker-dialog .model-picker-option:not([hidden])',nodes=>nodes.map(node=>node.dataset.model));
 assert.deepEqual(codex,['account-codex-model']);
 await page.click('dialog.model-picker-dialog .model-picker-option:not([hidden])');
 await page.select('dialog.model-picker-dialog .model-picker-effort','ultra');
 await page.click('dialog.model-picker-dialog .model-picker-apply');
 assert.equal(await page.$eval(input,e=>e.value),'account-codex-model');
 await page.type('#create input[name=name]','disposable-model-fixture');
 const created=page.waitForResponse(response=>response.request().method()==='POST'&&response.url().endsWith('/v1/logical-boxes'));
 await page.$eval('#create',form=>form.requestSubmit());await created;
 assert.deepEqual(requests.findLast(request=>request.method==='POST'&&request.path==='/v1/logical-boxes').body.loginProfiles,[{application:'codex',name:'personal-codex',model:'account-codex-model',reasoningEffort:'ultra'}]);
 await page.close();
});
test('creation can import one agent profile alongside a GitHub profile',async()=>{
 const page=await browser.newPage();
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#profile-choices select');
 await page.select('#create select[name=defaultAgent]','opencode');
 await page.select('#profile-choices select[name=loginProfile]',JSON.stringify({application:'opencode',name:'openrouter'}));
 await page.select('#profile-choices select[name=githubProfile]',JSON.stringify({application:'github',name:'gh-work'}));
 await page.type('#create input[name=name]','github-creation-fixture');
 const created=page.waitForResponse(response=>response.request().method()==='POST'&&response.url().endsWith('/v1/logical-boxes'));
 await page.click('#create button[type=submit]');await created;
 assert.deepEqual(requests.findLast(request=>request.method==='POST'&&request.path==='/v1/logical-boxes').body.loginProfiles,[{application:'opencode',name:'openrouter',model:'openrouter/deepseek/deepseek-v4.1-flash'},{application:'github',name:'gh-work'}]);
 await page.close();
});
test('worker placement distinguishes shared hosts and creation targets the selected pool',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.poolCreates=[];window.poolCapacity=[];
  window.fetch=async(path,options={})=>{
   const url=new URL(path,location.origin),method=options.method||'GET';
   if(method==='GET'&&url.pathname==='/v1/provider-credentials')return new Response(JSON.stringify([{provider:'railway',name:'primary',config:{}},{provider:'shared-worker',name:'shared-01',config:{}},{provider:'shared-worker',name:'shared-02',config:{}}]));
   if(method==='GET'&&url.pathname==='/v1/logical-boxes')return new Response(JSON.stringify([
    {id:'dedicated',name:'dedicated-box',provider:'railway',providerCredential:'primary',slotId:'dedicated-slot',state:'running',defaultAgent:'shell'},
    {id:'shared-a',name:'shared-a',provider:'shared-worker',providerCredential:'shared-01',slotId:'shared-slot',state:'running',defaultAgent:'shell'},
    {id:'shared-b',name:'shared-b',provider:'shared-worker',providerCredential:'shared-02',slotId:'shared-slot',state:'running',defaultAgent:'shell'}
   ]));
   if(method==='GET'&&url.pathname==='/v1/fleet/status'){
    const shared=url.searchParams.get('provider')==='shared-worker',alias=url.searchParams.get('providerCredential');
    return new Response(JSON.stringify({desiredSlots:2,actualSlots:2,freeSlots:1,occupiedSlots:1,unhealthySlots:0,slots:[{id:shared?'shared-slot':'dedicated-slot',ordinal:1,state:'occupied',health:'healthy',serviceName:shared?'logical-slot-1':'railway-worker-01',logicalBoxName:shared?(alias==='shared-01'?'shared-a':'shared-b'):'dedicated-box'},{id:'free-slot',ordinal:2,state:'free',health:'healthy',serviceName:shared?'logical-slot-2':'railway-worker-02'}]}));
   }
   if(method==='POST'&&url.pathname==='/v1/logical-boxes'){window.poolCreates.push(JSON.parse(options.body));return new Response(JSON.stringify({id:'created'}))}
   if(method==='PUT'&&url.pathname==='/v1/fleet/slots'){window.poolCapacity.push(JSON.parse(options.body));return new Response(options.body)}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForFunction(()=>document.querySelector('#capacity').textContent.includes('4 workers · 6 compute slots'));
 assert.match(await page.$eval('[data-box-id="dedicated"] .box-placement',e=>e.textContent),/Dedicated · railway-worker-01 · slot 1/);
 assert.match(await page.$eval('[data-box-id="shared-a"] .box-placement',e=>e.textContent),/Shared · shared-01 · slot 1/);
 assert.match(await page.$eval('[data-box-id="shared-b"] .box-placement',e=>e.textContent),/Shared · shared-02 · slot 1/);
 const selected=JSON.stringify({provider:'shared-worker',providerCredential:'shared-02'});
 await page.click('#create details summary');
 await page.select('#create-pool',selected);await page.type('#create input[name=name]','comparison-box');await page.click('#create button[type=submit]');
 await page.waitForFunction(()=>window.poolCreates.length===1);
 const created=await page.evaluate(()=>window.poolCreates[0]);assert.equal(created.provider,'shared-worker');assert.equal(created.providerCredential,'shared-02');
 await page.waitForFunction(()=>document.querySelector('#capacity').textContent.includes('4 workers · 6 compute slots'));
 assert.equal(await page.$eval('#create-pool',e=>e.value),selected);
 await page.select('#capacity-pool',JSON.stringify({provider:'shared-worker',providerCredential:'shared-01'}));await page.click('#slots button');
 await page.waitForFunction(()=>window.poolCapacity.length===1);
 assert.deepEqual(await page.evaluate(()=>window.poolCapacity[0]),{provider:'shared-worker',providerCredential:'shared-01',compute_box_slots:2});
 await page.close();
});
test('automatic placement refreshes capacity and prefers a less occupied pool',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.autoCreates=[];window.capacityMode='free';
  window.fetch=async(path,options={})=>{
   const url=new URL(path,location.origin),method=options.method||'GET';
   if(method==='GET'&&url.pathname==='/v1/provider-credentials')return new Response(JSON.stringify([{provider:'railway',name:'primary'},{provider:'shared-worker',name:'shared-01'},{provider:'shared-worker',name:'shared-02'}]));
   if(method==='GET'&&url.pathname==='/v1/fleet/status'){
    const alias=url.searchParams.get('providerCredential'),free=window.capacityMode==='full'?0:alias==='shared-02'?2:1;
    return new Response(JSON.stringify({actualSlots:2,desiredSlots:2,freeSlots:free,occupiedSlots:2-free,slots:[]}));
   }
   if(method==='POST'&&url.pathname==='/v1/logical-boxes'){window.autoCreates.push(JSON.parse(options.body));return new Response(JSON.stringify({id:'auto-created'}))}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForFunction(()=>document.querySelector('#create-pool').options.length===4);
 assert.equal(await page.$eval('#create-pool',element=>element.value),'');
 assert.equal(await page.$eval('#create details',element=>element.open),false);
 await page.type('#create input[name=name]','auto-box');await page.click('#create button[type=submit]');
 await page.waitForFunction(()=>window.autoCreates.length===1);
 assert.equal(await page.evaluate(()=>window.autoCreates[0].providerCredential),'shared-02');
 await page.evaluate(()=>{window.capacityMode='full'});
 await page.type('#create input[name=name]','fallback-box');
 await page.click('#create button[type=submit]');await page.waitForFunction(()=>window.autoCreates.length===2);
 assert.equal(await page.evaluate(()=>window.autoCreates[1].providerCredential),'primary');
 await page.close();
});
test('box deletion confirms exact identity, prevents repeats and shows asynchronous progress',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.deleteCalls=[];window.deleteState='running';
  window.fetch=async(path,options={})=>{
   if(path==='/v1/logical-boxes'&&(!options.method||options.method==='GET'))return new Response(JSON.stringify([
    ...(window.deleteState==='gone'?[]:[{id:'box-1',name:'helper ü',state:window.deleteState,defaultAgent:'shell',restorationState:window.deleteState==='deleting'?'delete-detaching-volume':'restored'}]),
    {id:'sibling',name:'keep-me',state:'hibernated',defaultAgent:'shell'}
   ]));
   if(path==='/v1/logical-boxes/box-1/volume'&&options.method==='DELETE'){window.deleteCalls.push({path,body:JSON.parse(options.body)});window.deleteState='deleting';return new Response(JSON.stringify({id:'box-1',state:'deleting'}),{status:202})}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]');
 page.once('dialog',d=>{assert.match(d.message(),/helper ü/);assert.match(d.message(),/permanently deleted/);d.dismiss()});await page.click('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]');assert.equal(await page.evaluate(()=>window.deleteCalls.length),0);
 page.once('dialog',d=>d.accept());await page.click('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="box-1"]').textContent.includes('delete-detaching-volume'));
 assert.equal(await page.$eval('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]',b=>b.disabled),true);assert.equal(await page.$('[data-box-id="box-1"] td:first-child a'),null);
 assert.deepEqual(await page.evaluate(()=>window.deleteCalls),[{path:'/v1/logical-boxes/box-1/volume',body:{confirmation:'helper ü'}}]);
 await page.evaluate(()=>window.deleteState='gone');await page.waitForFunction(()=>!document.querySelector('[data-box-id="box-1"]'),{timeout:10000});
 assert.ok(await page.$('[data-box-id="sibling"]'));assert.deepEqual(errors,[]);await page.close();
});
test('mobile box deletion reports a non-transient rejection without replaying deletion',async()=>{
 const page=await browser.newPage();await page.setViewport({width:390,height:844});
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.deleteCalls=0;
  window.fetch=async(path,options={})=>{if(path==='/v1/logical-boxes/box-1/volume'&&options.method==='DELETE'){window.deleteCalls++;return new Response(JSON.stringify({error:'Deletion forbidden by policy.'}),{status:409})}return original(path,options)};
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]');
 page.once('dialog',d=>d.accept());await page.$eval('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]',button=>button.click());await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('Deletion forbidden by policy'));
 assert.equal(await page.$eval('[data-box-id="box-1"] button[aria-label="Delete box helper ü"]',b=>b.disabled),false);assert.equal(await page.evaluate(()=>window.deleteCalls),1);assert.ok(await page.$('[data-box-id="box-1"]'));await page.close();
});
test('table previews stay fixed size and tool choices stay compact',async()=>{
 for(const width of [1280,390]){
  const page=await browser.newPage();await page.setViewport({width,height:844});await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#box-list .table-text');
  const result=await page.evaluate(()=>{
   const row=document.querySelector('#box-list tr:nth-child(2)'),table=row.parentElement;
   const measure=()=>({height:row.getBoundingClientRect().height,width:table.getBoundingClientRect().width,columns:[...row.cells].map(c=>c.getBoundingClientRect().width)});
   const before=measure();row.querySelectorAll('.table-text').forEach(n=>n.textContent='very long description '.repeat(1000));const after=measure();
   const tools=document.querySelector('#create-tools');return {before,after,toolHeight:tools.getBoundingClientRect().height,customCollapsed:!document.querySelector('#create .custom-tools').open};
  });
  assert.deepEqual(result.after,result.before);assert.ok(result.toolHeight<70);assert.equal(result.customCollapsed,true);await page.close();
 }
});
test('rows and the create form lay out in reading order without overlapping values',async()=>{
 const page=await browser.newPage();await page.setViewport({width:1280,height:900});
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;
  window.fetch=async(path,options={})=>{
   const url=new URL(path,location.origin),method=options.method||'GET';
   if(method==='GET'&&url.pathname==='/v1/logical-boxes')return new Response(JSON.stringify([
    {id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude',restorationState:'restored'},
    {id:'box-2',name:'sleepy',state:'hibernated',defaultAgent:'codex',restorationState:'saved'}
   ]));
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#create-tools input[value=blender]');
 const layout=await page.evaluate(()=>{
  const box=element=>element.getBoundingClientRect();
  const stateCell=document.querySelector('[data-box-id="box-1"] td:nth-child(2)');
  const primary=stateCell.querySelector('.table-text:not(.state-note)'),note=stateCell.querySelector('.state-note');
  const labels=[...document.querySelectorAll('#create-tools label')].map(box);
  const cells=[...document.querySelectorAll('#box-list tr.row td')];
  return {
   ownerDialogHidden:document.querySelector('#box-credentials-modal').hidden,
   stateStacked:!!note&&Math.round(box(primary).bottom)<=Math.round(box(note).top),
   stateTexts:[primary.textContent,note&&note.textContent],
   labelGaps:labels.slice(1).map((rect,index)=>Math.round(rect.left-labels[index].right)),
   overflowing:cells.filter(cell=>[...cell.children].some(child=>box(child).right>box(cell).right+1)).length,
   legends:[...document.querySelectorAll('#create>fieldset.group>legend')].map(l=>l.textContent),
   actionBelowGroups:box(document.querySelector('#create .form-actions')).top>=box([...document.querySelectorAll('#create>fieldset.group')].pop()).bottom
  };
 });
 assert.equal(layout.ownerDialogHidden,true);
 assert.deepEqual(layout.stateTexts,['running','restored']);
 assert.equal(layout.stateStacked,true);
 assert.ok(layout.labelGaps.length&&layout.labelGaps.every(gap=>gap>=8),'tool labels need visible separation: '+layout.labelGaps);
 assert.equal(layout.overflowing,0);
 assert.deepEqual(layout.legends,['1 · box','2 · logins','3 · tools','4 · instructions']);
 assert.equal(layout.actionBelowGroups,true);
 await page.close();
});
test('box actions follow state: no resume while creating, resume on failure',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.boxReads=0;
  window.fetch=async(path,options={})=>{
   const url=new URL(path,location.origin),method=options.method||'GET';
   if(method==='GET'&&url.pathname==='/v1/logical-boxes'){window.boxReads++;return new Response(JSON.stringify([
    {id:'creating',name:'building',state:'attaching',defaultAgent:'shell',provider:'railway',providerCredential:'primary',restorationState:'attaching-volume'},
    {id:'broke',name:'broken',state:'failed',defaultAgent:'shell',failureReason:'provider refused the volume'},
    {id:'sleepy',name:'sleepy',state:'hibernated',defaultAgent:'shell'}
   ]))}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('[data-box-id="creating"]');
 const buttons=id=>page.$$eval(`[data-box-id="${id}"] td:last-child button`,nodes=>nodes.map(n=>({aria:n.getAttribute('aria-label'),disabled:n.disabled})));
 // Delete remains available during creation so a stuck box can be cancelled.
 assert.deepEqual(await buttons('creating'),[{aria:'Details for box building',disabled:false},{aria:'Delete box building',disabled:false},{aria:'Instructions for box building',disabled:false},{aria:'Credentials for box building',disabled:false}]);
 assert.deepEqual(await buttons('broke'),[{aria:'Details for box broken',disabled:false},{aria:'Delete box broken',disabled:false},{aria:'Resume box broken',disabled:false},{aria:'Instructions for box broken',disabled:false},{aria:'Credentials for box broken',disabled:false}]);
 assert.deepEqual(await buttons('sleepy'),[{aria:'Details for box sleepy',disabled:false},{aria:'Delete box sleepy',disabled:false},{aria:'Resume box sleepy',disabled:false},{aria:'Instructions for box sleepy',disabled:false},{aria:'Credentials for box sleepy',disabled:false}]);
 assert.match(await page.$eval('[data-box-id="broke"] td:nth-child(2)',n=>n.textContent),/provider refused the volume/);
 await page.waitForFunction(()=>window.boxReads>=2,{timeout:8000});
 await page.close();
});
test('delete retries safely while an attaching box is still creating',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.deleteAttempts=0;
  window.fetch=async(path,options={})=>{
   const url=new URL(path,location.origin),method=options.method||'GET';
   if(method==='GET'&&url.pathname==='/v1/logical-boxes')return new Response(JSON.stringify([
    {id:'creating',name:'building',state:window.deleteAttempts>=2?'deleting':'attaching',defaultAgent:'claude',provider:'railway',providerCredential:'primary'}
   ]));
   if(method==='DELETE'&&url.pathname==='/v1/logical-boxes/creating/volume'){
    window.deleteAttempts++;
    if(window.deleteAttempts===1)return new Response(JSON.stringify({error:'logical box creation is still active; retry deletion shortly'}),{status:409});
    return new Response(JSON.stringify({state:'deleting'}),{status:202});
   }
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('[data-box-id="creating"] button[aria-label="Delete box building"]');
 page.once('dialog',dialog=>dialog.accept());
 await page.$eval('[data-box-id="creating"] button[aria-label="Delete box building"]',button=>button.click());
 await page.waitForFunction(()=>window.deleteAttempts===2,{timeout:10000});
 await page.waitForFunction(()=>document.querySelector('[data-box-id="creating"] button[aria-label="Delete box building"]')?.disabled);
 await page.close();
});
test('controller omits costs and renders compact box actions',async()=>{
 const page=await browser.newPage();await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('[data-box-id="box-1"]');
 assert.equal(await page.$('a[href="#costs"],#costs'),null);
 const actions=await page.$eval('[data-box-id="box-1"] td:last-child',cell=>({text:cell.textContent,labels:[...cell.querySelectorAll('button')].map(button=>button.getAttribute('aria-label')),restart:cell.querySelector('.restart-action')?.innerHTML}));
 assert.equal(actions.text.includes('Re-sync'),false);
 assert(actions.labels.includes('Restart box helper ü'));
 assert.match(actions.restart,/svg/);
 await page.close();
});
test('box link opens separate mobile workspace and reuses shell',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#box-list a',{visible:true});
 await Promise.all([page.waitForNavigation(),page.click('#box-list a')]);
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(new URL(page.url()).pathname,'/boxes/box-1');
 assert(requests.some(r=>r.path.endsWith('/sessions/interactive')&&r.body.agent==='shell'&&r.body.reuseExisting===true));
 assert.deepEqual(errors,[]);await page.close();
});
test('workspace network failure explains safe recovery',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;let failed=false;
  window.fetch=(path,...args)=>{
   if(String(path).endsWith('/sessions/interactive')&&!failed){failed=true;return Promise.reject(new TypeError('Failed to fetch'))}
   return original(path,...args);
  };
 });
 await page.goto(base+'/boxes/box-1');
 await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('operation may still be running'));
 assert.match(await page.$eval('#error',e=>e.textContent),/Resume \/ reconnect.*not replayed/);
 assert.equal(await page.$eval('#connect',e=>e.disabled),false);
 await page.click('#connect');
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(await page.$eval('#error',e=>e.textContent),'');
 await page.close();
});
test('configuration UI stays tiny and has no terminal code',async()=>{
 const css=await readFile(resolve(root,'app.css'),'utf8'),js=await readFile(resolve(root,'app.js'),'utf8');
 assert(Buffer.byteLength(css)<2048);assert(!/@import|url\(/.test(css));
 for(const removed of ['/terminal','/tasks','chat-groups','setInterval'])assert(!js.includes(removed),removed);
});
test('index styles ship in a page-scoped sheet, not inline and not in shared app.css',async()=>{
 const html=await readFile(resolve(root,'index.html'),'utf8'),page=await readFile(resolve(root,'controller.css'),'utf8');
 // The index is served under style-src 'self', so an inline <style> block is
 // blocked outright and the layout silently disappears.
 assert(!/<style[\s>]/.test(html),'controller index must not rely on an inline <style> block');
 assert(/<link rel="stylesheet" href="\/controller.css">/.test(html));
 // Only the index links controller.css: grid, chat and workspace share app.css
 // and their terminal viewers are sensitive to changes in page geometry.
 for(const other of ['grid.html','chat.html','workspace.html'])assert(!(await readFile(resolve(root,other),'utf8')).includes('controller.css'),other);
 assert(Buffer.byteLength(page)<14336);assert(!/@import|url\(/.test(page));
});
test('new box starts automatically and its row follows startup through the temporary saved state',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.createState='';window.boxReads=0;
  window.fetch=async(path,options={})=>{
   if(path==='/v1/logical-boxes'&&options.method==='POST'){
    const body=JSON.parse(options.body);if(body.allocateWhenReady!==true)throw Error('new box did not request startup');
    window.createState='attaching';return new Response(JSON.stringify({id:'created-new',state:'attaching'}),{status:202});
   }
   if(path==='/v1/logical-boxes'&&(!options.method||options.method==='GET')){
    window.boxReads++;
    return new Response(JSON.stringify([{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude'},
     ...(window.createState?[{id:'created-new',name:'automatic',state:window.createState,defaultAgent:'shell'}]:[])]));
   }
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#app:not([hidden])');await page.type('#create input[name=name]','automatic');
 await page.click('#create button[type=submit]');await page.waitForSelector('[data-box-id="created-new"]');
 await page.evaluate(()=>window.createState='hibernated');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="created-new"] td:nth-child(2)').textContent.includes('starting'),{timeout:12000});
 await page.evaluate(()=>window.createState='running');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="created-new"] td:nth-child(2)').textContent.includes('running'),{timeout:12000});
 assert.ok(await page.evaluate(()=>window.boxReads>=3));await page.close();
});
for(const mobile of [false,true])test(mobile?'390x844 configuration controls':'desktop configuration edits',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 // Initialize mobile before navigation; this does not prove a real phone keyboard.
 await page.setViewport(mobile?{width:390,height:844,isMobile:true,hasTouch:true}:{width:1280,height:900});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#app:not([hidden])');
 await page.select('#box-list select','codex');
 await page.waitForFunction(()=>document.querySelector('#provider-list button'));
 await page.click('#provider-list button');
 await page.waitForFunction(()=>document.querySelector('#provider-editor').open);
 await page.$eval('#provider textarea[name=config]',n=>{n.value=JSON.stringify({image:'new'})});
 const saved=page.waitForResponse(r=>r.request().method()==='PATCH'&&r.url().endsWith('/primary'));
 await page.$eval('#provider',form=>form.requestSubmit());await saved;
 const edit=requests.findLast(r=>r.method==='PATCH'&&r.path.endsWith('/primary'));assert.equal(edit.revision,revision);assert.deepEqual(edit.body,{config:{image:'new'}});
 await page.waitForNetworkIdle();
 assert.match(await page.$eval('#profile-summary',n=>n.textContent),/5 saved profiles · Team/);
 assert.deepEqual(await page.$$eval('#profile-tree .profile-app',nodes=>nodes.map(node=>node.dataset.application)),['claude','codex','opencode','github']);
 await page.select('#create select[name=defaultAgent]','opencode');
 await page.select('#profile-choices select',JSON.stringify({application:'opencode',name:'openrouter'}));
 await page.type('#create input[name=name]','profile-box');
 assert.deepEqual(await page.$$eval('#create select[name=defaultAgent] option',nodes=>nodes.map(n=>n.value)),['claude','codex','opencode','shell']);
 assert.deepEqual(await page.$eval('#create select[name=defaultAgent]',select=>({value:select.value,disabled:select.disabled})),{value:'opencode',disabled:false});
 await page.click('#create-tools label:has(input[value=blender])');
 assert.equal(await page.$('#create-tools input[value=desktop]'),null);
 const created=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/v1/logical-boxes'));await page.$eval('#create',form=>form.requestSubmit());await created;
 assert.deepEqual(requests.findLast(r=>r.method==='POST').body.loginProfiles,[{application:'opencode',name:'openrouter',model:'openrouter/deepseek/deepseek-v4.1-flash'}]);
 assert.deepEqual(requests.findLast(r=>r.method==='POST').body.tools,['desktop','blender']);
 assert.equal(requests.findLast(r=>r.method==='POST').body.defaultAgent,'opencode');
 assert.equal(requests.findLast(r=>r.method==='POST').body.allocateWhenReady,true);
 await page.waitForNetworkIdle();
 assert.equal(await page.$('#profile-upload'),null);
 await page.click('.workspace-links a[href="#profiles"]');
 await page.waitForFunction(()=>document.body.dataset.manageView==='profiles');
 const beforeDelete=requests.filter(r=>r.method==='DELETE').length;
 page.once('dialog',d=>d.dismiss());await page.$eval('#profile-tree button',button=>button.click());await page.waitForNetworkIdle();assert.equal(requests.filter(r=>r.method==='DELETE').length,beforeDelete);
 page.once('dialog',d=>d.accept());const deleted=page.waitForResponse(r=>r.request().method()==='DELETE');await page.$eval('#profile-tree button',button=>button.click());await deleted;await page.waitForNetworkIdle();
 await page.click('.workspace-links a[href="#providers"]');
 await page.waitForFunction(()=>document.body.dataset.manageView==='providers');
 await page.waitForFunction(()=>document.querySelector('#capacity').textContent.includes('Free: 1'));
 assert.match(await page.$eval('#capacity',n=>n.textContent),/Free: 1/);
 assert.equal(await page.$eval('#capacity details',n=>n.open),false);
 assert.equal(await page.$eval('#schema',n=>n.parentElement.open),false);
 assert.match(await page.$eval('#destinations',n=>n.textContent),/No notification destinations/);
 assert.equal(await page.$eval('#slots input',n=>n.value),'2');
 const capacitySaved=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().endsWith('/v1/fleet/slots'));
 await page.$eval('#slots',form=>form.requestSubmit());await capacitySaved;
 assert.equal(requests.findLast(r=>r.path==='/v1/fleet/slots').body.compute_box_slots,2);
 assert.equal(await page.$('#terminal'),null);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:mobile?'/tmp/vmbox-config-mobile.png':'/tmp/vmbox-config-desktop.png',fullPage:true});
 await page.click('#manage-menu');await page.click('#logout');await page.waitForFunction(()=>document.querySelector('#app').hidden);assert.equal(await page.$eval('#provider textarea[name=secret]',n=>n.value),'');
 assert.deepEqual(errors,[]);await page.close();
});

test('fleet locations load on demand and preserve occupied fleets on rejection',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.locationWrites=[];
  window.fetch=async(path,options={})=>{
   if(path.startsWith('/v1/fleet/regions?'))return new Response(JSON.stringify([{id:'eu',name:'Europe'},{id:'us',name:'America'}]));
   if(path.startsWith('/v1/fleet/slots?'))return new Response(JSON.stringify({region:'eu'}));
   if(path==='/v1/fleet/location'){locationWrites.push(JSON.parse(options.body));return new Response(JSON.stringify({error:'Location changes require an empty fleet; existing boxes cannot be migrated.'}),{status:409})}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#capacity table');
 assert.equal(await page.$eval('#location-form',e=>e.hidden),true);
 await page.click('#fleet-location summary');await page.click('#load-locations');await page.waitForSelector('#location-form:not([hidden])');
 assert.equal(await page.$eval('#location-form select',e=>e.value),'eu');
 await page.select('#location-form select','us');await page.click('#location-form button');
 await page.waitForFunction(()=>document.querySelector('#location-status').textContent.includes('empty fleet'));
 assert.deepEqual(await page.evaluate(()=>locationWrites),[{provider:'railway',providerCredential:'primary',region:'us'}]);
 assert.match(await page.$eval('#capacity',e=>e.textContent),/helper ü/);
 await page.close();
});
test('instruction presets preview safely, bound size, and apply explicitly to boxes',async()=>{
 const page=await browser.newPage();
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#instruction-list table');
 assert.match(await page.$eval('#instruction-list',element=>element.textContent),/general/);
 assert.match(await page.$eval('#instruction-list',element=>element.textContent),/default/);
 // The creation form offers automatic, none, every preset, and a custom copy.
 await page.evaluate(()=>{document.querySelector('#create-instructions-editor').open=true});
 assert.deepEqual(await page.$$eval('#create-instructions option',nodes=>nodes.map(node=>node.value)),['auto','none','general','custom']);
 await page.select('#create-instructions','general');
 await page.waitForFunction(()=>document.querySelector('#create-instructions-preview').textContent.includes('House rules'));
 // Embedded markup in user Markdown is rendered as text, never as live nodes.
 assert.equal(await page.$eval('#create-instructions-preview',element=>element.querySelectorAll('img,script').length),0);
 assert.equal(await page.evaluate(()=>window.pwned),undefined);
 assert.match(await page.$eval('#create-instructions-preview',element=>element.textContent),/onerror/);
 // An edited box copy keeps preset provenance in the creation payload.
 await page.evaluate(()=>{const area=document.querySelector('#create-instructions-custom');area.value='# House rules\nAlways answer briefly.\nextra';area.dispatchEvent(new Event('input',{bubbles:true}))});
 await page.type('#create input[name=name]','instructions-fixture');
 const created=page.waitForResponse(response=>response.request().method()==='POST'&&response.url().endsWith('/v1/logical-boxes'));
 await page.click('#create button[type=submit]');await created;
 assert.deepEqual(requests.findLast(request=>request.method==='POST'&&request.path==='/v1/logical-boxes').body.instructions,{preset:'general',markdown:'# House rules\nAlways answer briefly.\nextra'});
 // Explicit Apply instructions action for an existing box.
 await page.evaluate(()=>{const row=document.querySelector('[data-box-id="box-1"]');[...row.querySelectorAll('button')].find(button=>button.textContent.includes('Instructions')).click()});
 await page.waitForFunction(()=>!document.querySelector('#box-instructions-modal').hidden);
 assert.match(await page.$eval('#box-instructions-effective',element=>element.textContent),/vmbox chat delivery.*Message-ID/s);
 await page.evaluate(()=>{const area=document.querySelector('#box-instructions-markdown');area.value='# Applied\nDo this.';area.dispatchEvent(new Event('input',{bubbles:true}))});
 await page.select('#box-instructions-preset','custom');
 await page.click('#box-instructions-apply');
 await page.waitForFunction(()=>document.querySelector('#box-instructions-status').textContent.includes('fixture applied'));
 assert.deepEqual(requests.findLast(request=>request.method==='PUT'&&request.path==='/v1/logical-boxes/box-1/instructions').body,{markdown:'# Applied\nDo this.'});
 // A dialog is modal: dismiss it before touching the page underneath.
 await page.click('#box-instructions-modal .form-actions .linkbtn');
 await page.waitForFunction(()=>document.querySelector('#box-instructions-modal').hidden);
 // Editing imported profiles is an explicit, verified selection.
 await page.evaluate(()=>{const row=document.querySelector('[data-box-id="box-1"]');[...row.querySelectorAll('button')].find(button=>button.textContent.includes('Credentials')).click()});
 await page.waitForFunction(()=>!document.querySelector('#box-credentials-modal').hidden);
 await page.select('#box-credentials-form select',JSON.stringify({application:'claude',name:'personal'}));
 await page.click('#box-credentials-apply');
 await page.waitForFunction(()=>document.querySelector('#box-credentials-status').textContent.includes('fixture applied'));
 assert.deepEqual(requests.findLast(request=>request.method==='PUT'&&request.path==='/v1/logical-boxes/box-1/login-profiles').body,{profiles:[{application:'claude',name:'personal'}]});
 await page.click('#box-credentials-modal .form-actions .linkbtn');
 await page.waitForFunction(()=>document.querySelector('#box-credentials-modal').hidden);
 // Oversized preset Markdown is rejected before any request is made.
 await page.evaluate(()=>{document.querySelector('#instruction-editor').open=true});
 await page.type('#instruction-form input[name=name]','huge-preset');
 await page.evaluate(()=>{document.querySelector('#instruction-form textarea[name=markdown]').value='x'.repeat(70000)});
 // Exercise the editor's size guard even if Chromium applies the textarea's
 // native maxlength constraint to a programmatically assigned value.
 await page.evaluate(()=>{const form=document.querySelector('#instruction-form');form.noValidate=true;form.requestSubmit()});
 await page.waitForFunction(()=>document.querySelector('#instruction-status').textContent.includes('64 KiB'));
 assert.equal(requests.some(request=>request.method==='PUT'&&request.path==='/v1/instruction-presets/huge-preset'),false);
 await page.close();
});
