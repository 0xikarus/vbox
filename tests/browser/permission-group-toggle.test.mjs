import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker'};
const baseline={
 allContacts:{enabled:false,tag:'keep'},mail:{read:false,compose:false,tag:'keep'},
 createAgentBox:{enabled:false,maxBoxes:3,maxDiskGiB:50,allowedAgents:['codex','claude','opencode'],tag:'keep'},
 manageAgentBoxes:{list:false,inspect:false,control:false,tag:false,restart:false,delete:false,unknown:'keep'},
 mcpTools:{enabled:true,allowedTools:[],unknown:'keep'},requestMoreTime:{maxExtensionMinutes:7,maxTotalMinutes:14}
};
const clone=value=>structuredClone(value);
const groups=['manage-boxes','mail','computer-use','passwords','all-contacts'];
const readTools=['list_mail_addresses','list_emails','read_email','mark_email_read','download_email_attachment','subscribe_inbox','unsubscribe_inbox'];
const composeTools=['send_email','list_outbox','get_outbox_status'];
const companions=['wake_agent_box','clear_agent_box_context','compact_agent_box_context'];
function expected(selected,contacts=false){
 const cap=clone(baseline),has=tool=>selected.includes(tool);
 cap.allContacts.enabled=contacts;
 cap.mail.read=readTools.some(has);cap.mail.compose=composeTools.some(has);
 cap.createAgentBox.enabled=has('create_agent_box');
 cap.manageAgentBoxes.list=has('list_agent_boxes');
 cap.manageAgentBoxes.inspect=['list_agent_boxes','get_agent_box','get_agent_box_screenshot'].some(has);
 cap.manageAgentBoxes.control=has('remote_control_box');
 cap.manageAgentBoxes.tag=has('set_agent_box_tags');
 cap.manageAgentBoxes.restart=['restart_agent_box','wake_agent_box','clear_agent_box_context','compact_agent_box_context','set_agent_box_run_budget'].some(has);
 cap.manageAgentBoxes.delete=has('delete_agent_box');
 cap.mcpTools.allowedTools=selected;
 return {capabilities:cap};
}

test('Details and Manage show one master and visible tool checkboxes with exact derived policy PUTs',async()=>{
 let policy={capabilities:clone(baseline)},writes=[];
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://fixture').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');const send=value=>res.end(JSON.stringify(value));
   if(path==='/v1/logical-boxes/builder/agent-policy'){
    if(req.method==='PUT'){let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);writes.push(body);policy=body}
    return send(policy);
   }
   if(path==='/v1/whoami')return send({role:'owner',accountId:'acct'});
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return send([box]);
   if(path==='/v1/mail/approvals')return send({pending:0,items:[]});
   if(path==='/v1/logical-boxes/builder/mail')return send({enabled:false,unread:0,pending:0,address:'',subscribed:false,filters:{sender:'',subject:''}});
   if(path==='/v1/chat-sidebar-layout')return send({exists:true,groups:[],members:{}});
   if(path==='/v1/capabilities')return send({providerEdits:true});
   if(path==='/v1/controller-defaults')return send({provider:'railway',providerCredential:'primary'});
   if(path==='/v1/fleet/status')return send({slots:[],actualSlots:0,freeSlots:0,occupiedSlots:0});
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return send({})}
   if(['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands','/v1/provider-credentials','/v1/login-profiles'].includes(path))return send([]);
   if(path.endsWith('/messages')||path.endsWith('/contacts'))return send([]);
   if(path.endsWith('/tags'))return send({tags:[]});
   if(path.endsWith('/desktop'))return send({enabled:false});
   return send({});
  }
  const file=resolve(web,path==='/'?'index.html':path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base=`http://127.0.0.1:${server.address().port}`;
  for(const view of ['chat','manage']){
   policy={capabilities:clone(baseline)};writes=[];
   const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
   await page.goto(view==='chat'?base+'/chat#box=builder':base+'/#roles');
   if(view==='chat'){
    await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
    await page.click('#chat-info');await page.waitForSelector('[data-ip-row="access"]');await page.$eval('[data-ip-row="access"]',node=>node.click());
   }else{await page.waitForSelector('#roles .role-assignment-toggle');await page.click('#roles .role-assignment-toggle')}
   await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#role-editor-status').textContent==='');
   await page.waitForFunction(()=>!document.querySelector('[data-permission-group="mail"]').hidden);
   const root='#role-editor-form',limit=view==='chat'?'#inspect-create-limit':'#role-create-limit';
   const computerTools=await page.$$eval('[data-permission-group="computer-use"] input[name=mcpTools]',nodes=>nodes.map(node=>node.value));
   assert.equal(computerTools.length,11,view+' lists all computer-use tools');
   for(const tool of ['detect_captcha','show_captcha','solve_captcha'])assert.ok(computerTools.includes(tool),view+' lists '+tool);
   assert.equal(await page.$eval(root,form=>form.querySelectorAll('.inline-permission-quick,.inline-permission-tools,.mcp-tool-group-toggle,input[name=mailRead],input[name=mailCompose],input[name=controlOtherDesktops],input[name=createAgentBoxEnabled]').length),0,view+' has no extra toggles or Tools disclosures');
   assert.equal(await page.$eval(limit,node=>node.querySelectorAll('input[type=checkbox]').length),0,view+' limit has no switch');
   assert.deepEqual(await page.$$eval('[data-permission-group="manage-boxes"] .inline-permission-tool-list > *',nodes=>nodes.map(node=>node.matches('label')?node.querySelector('input[name=mcpTools]')?.value:node.id==='inspect-create-limit'||node.id==='role-create-limit'?'limit':node.classList.contains('role-capability-options')?'advanced':'').filter(Boolean).slice(3,7)),['remote_control_box','create_agent_box','limit','advanced'],view+' places limit under create_agent_box');
   for(const id of groups){
    const section=`[data-permission-group="${id}"]`,master=section+' .inline-permission-master';
    const values=await page.$$eval(section+' input[name=mcpTools]',nodes=>nodes.map(node=>node.value));
    assert.equal(await page.$$eval(section+' .inline-permission-master',nodes=>nodes.length),1,view+' '+id+' has one category switch');
    if(values.length){
     assert.equal(await page.$$eval(section+' .inline-permission-tool-row',nodes=>nodes.every(node=>node.querySelector('strong')?.textContent===node.querySelector('input')?.value&&!!node.querySelector('small')?.textContent)),true,view+' '+id+' lists each tool and description');
     const before=writes.length;await page.$eval(section+' input[name=mcpTools]',node=>node.click());
     await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
     assert.equal(writes.length,before+1,view+' '+id+' one checkbox writes once');
     assert.deepEqual(writes.at(-1),expected([values[0]]),view+' '+id+' exact checkbox body');
     assert.equal(await page.$eval(master,node=>node.indeterminate&&node.getAttribute('aria-checked')==='mixed'),true,view+' '+id+' master is mixed');
     assert.equal(await page.$eval(section+' .inline-permission-group-count',node=>node.textContent),'1 of '+values.length);
    }
    const beforeOn=writes.length;await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOn+1,view+' '+id+' master on writes once');
    assert.equal(await page.$eval(master,node=>node.checked&&!node.indeterminate),true);
    assert.deepEqual(writes.at(-1),expected(values,id==='all-contacts'),view+' '+id+' exact master on body');
    const beforeOff=writes.length;await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOff+1,view+' '+id+' master off writes once');
    assert.deepEqual(writes.at(-1),expected([]),view+' '+id+' exact master off body');
   }
   const allTools=await page.$$eval('#role-editor-form input[name=mcpTools]',nodes=>nodes.map(node=>node.value));
   for(const tool of allTools){
    const selector=`#role-editor-form input[name=mcpTools][value="${tool}"]`,before=writes.length;
    await page.$eval(selector,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,before+1,view+' '+tool+' checkbox writes once');
    const selected=allTools.filter(value=>value===tool||tool==='restart_agent_box'&&companions.includes(value));
    assert.deepEqual(writes.at(-1),expected(selected),view+' '+tool+' exact tool body');
    if(tool==='get_agent_box_screenshot')assert.equal(writes.at(-1).capabilities.manageAgentBoxes.control,false,'screenshot stays independent of control');
    if(tool==='remote_control_box')assert.equal(writes.at(-1).capabilities.manageAgentBoxes.inspect,false,'control stays independent of screenshot');
    if(tool==='create_agent_box')await page.waitForFunction(selector=>!document.querySelector(selector).disabled,{},limit+' .idle-policy-controls input');
    const group=await page.$eval(selector,node=>node.closest('.inline-permission-group').dataset.permissionGroup);
    const master=`[data-permission-group="${group}"] .inline-permission-master`;
    await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    const beforeClear=writes.length;await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeClear+1,view+' '+tool+' reset writes once');
    assert.deepEqual(writes.at(-1),expected([]),view+' '+tool+' reset body');
   }
   await page.waitForFunction(selector=>document.querySelector(selector)?.disabled,{},limit+' .idle-policy-controls input');
   await page.close();
   const capture=process.env.PERMISSION_GROUP_CAPTURE_DIR;if(capture){
    await mkdir(capture,{recursive:true});const shot=await browser.newPage();await shot.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
    await shot.goto(view==='chat'?base+'/chat#box=builder':base+'/#roles');
    if(view==='chat'){await shot.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);await shot.click('#chat-info');await shot.waitForSelector('[data-ip-row="access"]');await shot.$eval('[data-ip-row="access"]',node=>node.click())}
    else{await shot.waitForSelector('#roles .role-assignment-toggle');await shot.click('#roles .role-assignment-toggle')}
    await shot.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#role-editor-status').textContent==='');
    await shot.screenshot({path:resolve(capture,`${view}-tools-390-light.png`)});
    await shot.waitForFunction(()=>!document.querySelector('[data-permission-group="mail"]').hidden);
    await shot.$eval('[data-permission-group="mail"]',node=>{node.scrollIntoView({block:'start',behavior:'instant'});let parent=node.parentElement;while(parent&&parent.scrollHeight<=parent.clientHeight)parent=parent.parentElement;(parent||document.scrollingElement).scrollTop-=100});
    await shot.waitForFunction(()=>{const top=document.querySelector('[data-permission-group="mail"]').getBoundingClientRect().top;return top>=60&&top<160});
    await shot.screenshot({path:resolve(capture,`${view}-mail-390-light.png`)});
    await shot.close();
   }
  }
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
