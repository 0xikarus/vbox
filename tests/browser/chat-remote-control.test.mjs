import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {extname,resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const now=new Date(),earlier=new Date(now.getTime()-2*60000).toISOString();
const boxes=[
 {id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'shared-worker',slotId:'slot-builder',lastRemoteControl:{actorBoxId:'manager',actorName:'Manager',actions:3,startedAt:earlier,endedAt:earlier}},
 {id:'manager',name:'Manager',state:'running',defaultAgent:'codex',provider:'shared-worker',slotId:'slot-manager'},
];
const messages=[{id:'notice',direction:'system',state:'delivered',createdAt:earlier,updatedAt:earlier,text:'Controlled by Manager · 3 actions',control:{kind:'remote_control',actorBoxId:'manager',actorName:'Manager',actions:3,firstAt:earlier,lastAt:earlier}}];

async function fixtureServer(){
 const server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   response.setHeader('Content-Type','application/json');
   const data={
    '/v1/whoami':{role:'owner'},'/v1/logical-boxes':boxes,'/v1/grid-boxes':boxes,
    '/v1/box-activity':[],'/v1/box-conversations':[],'/v1/tool-presets':[],
    '/v1/logical-boxes/builder/messages':messages,'/v1/logical-boxes/manager/messages':[],
    '/v1/logical-boxes/builder/agent-policy':{capabilities:{manageAgentBoxes:{control:false},mcpTools:{enabled:true,allowedTools:[]},createAgentBox:{maxBoxes:3,maxDiskGiB:50}}},
    '/v1/logical-boxes/builder/contacts':[],
   };
   if(path==='/v1/mail/approvals'||path==='/v1/push/vapid-key')response.statusCode=404;
   response.end(JSON.stringify(data[path]??{}));return;
  }
  const file=path==='/chat'?'chat.html':path.slice(1);
  if(file.includes('..')){response.statusCode=404;response.end();return}
  try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}catch{response.statusCode=404;response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));return server;
}

test('remote control notice links to the actor and Technical shows the last session',async()=>{
 const capture=process.env.REMOTE_CONTROL_CAPTURE_DIR;if(capture)await mkdir(capture,{recursive:true});
 const server=await fixtureServer(),browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port;
  for(const width of [390,1440])for(const theme of ['light','dark']){
   const page=await browser.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto(base+'/chat#box=builder');
   await page.waitForSelector('.remote-control-link');
   assert.equal(await page.$eval('.remote-control-link',link=>link.textContent),'Controlled by Manager · 3 actions');
   assert.equal(await page.$eval('.remote-control-link',link=>new URL(link.href).hash),'#box=manager');
   const save=async name=>{if(capture)await page.screenshot({path:resolve(capture,`${name}-${width}-${theme}.png`)})};
   await save('chat-notice');
   await page.click('#chat-info');await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
   await page.$eval('[data-ip-row="technical"]',row=>row.click());
   await page.waitForFunction(()=>document.querySelector('#ip-technical-table')?.textContent.includes('Last remote control'));
   assert.match(await page.$eval('#ip-technical-table',table=>table.textContent),/Last remote controlManager · \d+ min ago · 3 actions/);
   await save('technical');
   await page.$eval('#inspect-prototype-back',button=>button.click());
   await page.$eval('[data-ip-row="access"]',row=>row.click());
   await page.waitForFunction(()=>document.querySelector('#role-editor-status')?.textContent==='');
   assert.equal(await page.$eval('#role-editor-form [name=controlOtherDesktops]',input=>input.checked),false);
   await page.$eval('#role-editor-form input[value=remote_control_box]',input=>input.click());
   assert.equal(await page.$eval('#role-editor-form [name=controlOtherDesktops]',input=>input.checked),true);
   assert.equal(await page.$eval('#role-editor-form input[value=remote_control_box]',input=>input.checked),true);
   assert.equal(await page.$eval('#role-editor-form input[value=get_agent_box_screenshot]',input=>input.checked),false,'screenshot access is a separate toggle');
   await save('permissions');
   await page.click('[data-permission-group="manage-boxes"] .inline-permission-tools summary');
   assert.match(await page.$eval('[data-permission-group="manage-boxes"]',group=>group.textContent),/remote_control_box/);
   await page.$eval('#role-editor-form input[value=remote_control_box]',input=>input.scrollIntoView({block:'center'}));
   await save('tool-picker');
   await page.keyboard.press('Escape');
   if(width===390&&theme==='light'){
    messages[0].control.actions=4;
    await page.waitForFunction(()=>document.querySelector('.remote-control-link')?.textContent.includes('4 actions'),{timeout:8000});
    messages[0].control.actions=3;
   }
   await page.click('.remote-control-link');
   await page.waitForFunction(()=>location.hash==='#box=manager');
   assert.deepEqual(errors,[]);await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
