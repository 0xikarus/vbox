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
const groups=[['manage-boxes','manage'],['mail','mail'],['computer-use','computer'],['passwords','passwords'],['all-contacts','contacts']];
const quickRows=[
 ['see-boxes',['list_agent_boxes','get_agent_box'],['list','inspect']],
 ['see-box-screens',['get_agent_box_screenshot'],['inspect']],
 ['restart-boxes',['restart_agent_box','wake_agent_box','clear_agent_box_context','compact_agent_box_context'],['restart']],
 ['tag-budget',['set_agent_box_tags','set_agent_box_run_budget'],['tag','restart']],
 ['delete-boxes',['delete_agent_box'],['delete']],
 ['heartbeat',['heartbeat'],[]],
 ['see-screen',['take_screenshot','capture_window'],[]],
 ['mouse-keyboard',['move_mouse','click_mouse','drag_mouse','scroll_mouse','type_text','press_keys'],[]],
 ['ask-password',['secret_request'],[]],['generate-password',['generate_password'],[]],['type-password',['type_secret'],[]]
];
function expected(group,on,tools){
 const cap=clone(baseline);cap.mcpTools.allowedTools=on?tools:[];
 if(group==='manage-boxes'){
  cap.createAgentBox.enabled=on;
  for(const key of ['list','inspect','control','tag','restart','delete'])cap.manageAgentBoxes[key]=on;
 }else if(group==='mail'){cap.mail.read=on;cap.mail.compose=on}
 else if(group==='all-contacts')cap.allContacts.enabled=on;
 return {capabilities:cap};
}

test('Details and Manage group switches save all, none, and mixed grants in one PUT',async()=>{
 let policy={capabilities:clone(baseline)},writes=[];
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://fixture').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');const send=value=>res.end(JSON.stringify(value));
   if(path==='/v1/logical-boxes/builder/agent-policy'){
    if(req.method==='PUT'){let text='';for await(const chunk of req)text+=chunk;const body=JSON.parse(text);writes.push(body);policy=body}
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
   for(const [id] of groups){
    const selector=`[data-permission-group="${id}"]`,master=selector+' .inline-permission-master';
    const tools=await page.$$eval(selector+' input[name=mcpTools]',nodes=>nodes.map(node=>node.value));
    if(tools.length){
     await page.$eval(selector+' .inline-permission-tools',node=>node.open=true);
     const before=writes.length;await page.$eval(selector+' input[name=mcpTools]',node=>node.click());
     await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
     assert.equal(writes.length,before+1,view+' '+id+' single tool writes once');
     assert.equal(await page.$eval(master,node=>node.indeterminate),true,view+' '+id+' mixed');
     assert.equal(await page.$eval(master,node=>node.getAttribute('aria-checked')),'mixed');
     assert.match(await page.$eval(selector+' .inline-permission-count',node=>node.textContent),new RegExp(`^1 of ${tools.length}`));
     if(id==='manage-boxes'){
      assert.equal(await page.$eval('#role-editor-form [name=controlOtherDesktops]',node=>node.checked),false,'individual list tool does not grant control');
      await page.$eval(selector+' input[value=get_agent_box_screenshot]',node=>node.click());
      await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
      assert.equal(await page.$eval('#role-editor-form [name=controlOtherDesktops]',node=>node.checked),false,'screenshot remains independent');
     }
    }
    if(id==='computer-use'||id==='passwords')await page.$eval(selector+' .inline-permission-tools',node=>node.open=false);
    const beforeOn=writes.length;if(id==='computer-use'||id==='passwords')await page.click(master);else await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOn+1,view+' '+id+' master on writes once');
    if(id==='computer-use'||id==='passwords')assert.equal(await page.$eval(selector+' .inline-permission-tools',node=>node.open),false,'master does not expand tools');
    assert.equal(await page.$eval(master,node=>node.checked&&!node.indeterminate),true,view+' '+id+' master should turn a mixed group on');
    assert.deepEqual(writes.at(-1),expected(id,true,tools),view+' '+id+' exact on body');
    const beforeOff=writes.length;await page.$eval(master,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOff+1,view+' '+id+' master off writes once');
    assert.equal(await page.$eval(master,node=>node.checked||node.indeterminate),false);
    assert.deepEqual(writes.at(-1),expected(id,false,tools),view+' '+id+' exact off body');
   }
   for(const [key,tools,caps] of quickRows){
    const quick=`[data-quick-permission="${key}"] .inline-permission-quick`;
    assert.ok(await page.$(quick),view+' has '+key+' quick row');
    if(tools.length>1){
     const partial=key==='restart-boxes'?'wake_agent_box':tools[0];
     await page.$eval(`#role-editor-form input[name=mcpTools][value=${partial}]`,node=>node.click());
     await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
     assert.equal(await page.$eval(quick,node=>node.indeterminate),true,view+' '+key+' mixed quick row');
    }
    const beforeOn=writes.length;await page.$eval(quick,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOn+1,view+' '+key+' quick on writes once');
    const expectedOn=expected('',true,tools);for(const cap of caps)expectedOn.capabilities.manageAgentBoxes[cap]=true;
    assert.deepEqual(writes.at(-1),expectedOn,view+' '+key+' exact on body');
    if(key==='see-box-screens')assert.equal(await page.$eval('#role-editor-form [name=controlOtherDesktops]',node=>node.checked),false,'quick screenshot stays independent');
    const beforeOff=writes.length;await page.$eval(quick,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOff+1,view+' '+key+' quick off writes once');
    assert.deepEqual(writes.at(-1),expected('',false,[]),view+' '+key+' exact off body');
   }
   for(const [name,tools,cap] of [['mailRead',['list_emails','read_email','search_emails','mark_email_read','download_email_attachment','subscribe_inbox','unsubscribe_inbox'],'read'],['mailCompose',['send_email','list_outbox','get_outbox_status'],'compose']]){
    await page.$eval(`#role-editor-form input[name=mcpTools][value=${tools[0]}]`,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(await page.$eval(`#role-editor-form input[name=${name}]`,node=>node.indeterminate),true,view+' '+name+' mixed');
    const beforeOn=writes.length;await page.$eval(`#role-editor-form input[name=${name}]`,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOn+1,view+' '+name+' on writes once');
    const expectedOn=expected('',true,tools);expectedOn.capabilities.mail[cap]=true;
    assert.deepEqual(writes.at(-1),expectedOn,view+' '+name+' exact on body');
    const beforeOff=writes.length;await page.$eval(`#role-editor-form input[name=${name}]`,node=>node.click());
    await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
    assert.equal(writes.length,beforeOff+1,view+' '+name+' off writes once');
    assert.deepEqual(writes.at(-1),expected('',false,[]),view+' '+name+' exact off body');
   }
   await page.$eval('#role-editor-form [name=controlOtherDesktops]',node=>node.click());
   await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
   assert.equal(await page.$eval('#role-editor-form input[value=get_agent_box_screenshot]',node=>node.checked),false,view+' control alone does not grant screenshots');
   await page.$eval('#role-editor-form [name=controlOtherDesktops]',node=>node.click());
   await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
   await page.$eval('[data-permission-group="manage-boxes"] input[value=get_agent_box_screenshot]',node=>node.click());
   await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
   await page.close();
   const capture=process.env.PERMISSION_GROUP_CAPTURE_DIR;if(capture){
    await mkdir(capture,{recursive:true});
    for(const width of [390,1440])for(const theme of ['light','dark']){
     const shot=await browser.newPage();await shot.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await shot.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
     await shot.goto(view==='chat'?base+'/chat#box=builder':base+'/#roles');
     if(view==='chat'){await shot.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);await shot.click('#chat-info');await shot.waitForSelector('[data-ip-row="access"]');await shot.$eval('[data-ip-row="access"]',node=>node.click())}
     else{await shot.waitForSelector('#roles .role-assignment-toggle');await shot.click('#roles .role-assignment-toggle')}
     await shot.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#role-editor-status').textContent==='');
     await shot.screenshot({path:resolve(capture,`${view}-groups-${width}-${theme}.png`)});
     if(view==='chat'&&width===390){await shot.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight/2);await shot.screenshot({path:resolve(capture,`${view}-groups-middle-${width}-${theme}.png`)});await shot.$eval('#inspect-prototype-page',node=>node.scrollTop=node.scrollHeight);await shot.screenshot({path:resolve(capture,`${view}-groups-bottom-${width}-${theme}.png`)})}
     if(view==='manage'&&width===390){await shot.evaluate(()=>window.scrollTo(0,document.body.scrollHeight/2));await shot.screenshot({path:resolve(capture,`${view}-groups-middle-${width}-${theme}.png`)});await shot.evaluate(()=>window.scrollTo(0,document.body.scrollHeight));await shot.screenshot({path:resolve(capture,`${view}-groups-bottom-${width}-${theme}.png`)})}
     await shot.close();
    }
   }
  }
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
