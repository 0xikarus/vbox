import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','box-create-limit.js','idle-policy.css','mail.css'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

test('chat details edits one box creation limit without changing its other permissions',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude'};
 const other={id:'reviewer',name:'Reviewer',state:'running',defaultAgent:'codex'};
 const withoutCreate={id:'reader',name:'Reader',state:'running',defaultAgent:'claude'};
 const capabilities={allContacts:{enabled:true},createAgentBox:{enabled:true,maxBoxes:2,maxDiskGiB:50,allowedAgents:['claude']},mcpTools:{enabled:true,allowedTools:['create_agent_box','take_screenshot']}};
 const policies=new Map([[box.id,{boxId:box.id,capabilities}],[other.id,{boxId:other.id,capabilities:{...capabilities,createAgentBox:{...capabilities.createAgentBox,maxBoxes:4}}}],[withoutCreate.id,{boxId:withoutCreate.id,capabilities:{...capabilities,createAgentBox:{...capabilities.createAgentBox,enabled:false}}}]]);
 const updates=[];
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box,other,withoutCreate]));
  if(path.endsWith('/agent-policy')){
   const id=path.split('/')[3];
   if(req.method==='PUT'){
    let raw='';for await(const chunk of req)raw+=chunk;
    const body=JSON.parse(raw);updates.push({id,...body});policies.set(id,{boxId:id,...body});
   }
   return res.end(JSON.stringify(policies.get(id)));
  }
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Builder'&&document.querySelector('#chat-loading').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await page.$eval('[data-ip-row="access"]',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('[data-ip-page="access"]').hidden);
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='2 total');
  await page.$eval('#inspect-create-limit input[type=number]',input=>{input.value='5';input.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.$eval('#inspect-create-limit .idle-policy-controls button',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='5 total');
  assert.equal(updates.length,1);
  assert.equal(updates[0].id,'builder');
  assert.deepEqual(updates[0].capabilities,{...capabilities,createAgentBox:{...capabilities.createAgentBox,maxBoxes:5}});
  await page.$eval('[data-box-id="reviewer"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='4 total');
  await page.$eval('[data-box-id="reader"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='Off');
  assert.equal(await page.$eval('#inspect-create-limit',root=>root.hidden),false);
  assert.equal(await page.$eval('#inspect-create-limit input[type=number]',input=>input.disabled),true);
  assert.equal(await page.$eval('#inspect-create-limit .mail-switch input',input=>input.disabled&&!input.checked),true);
  await page.$eval('[data-box-id="builder"]',row=>row.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='5 total');
  assert.equal(await page.$eval('#inspect-create-limit .mail-switch input',input=>input.checked&&!input.disabled),true);
  await page.$eval('#inspect-create-limit .mail-switch input',input=>{input.checked=false;input.dispatchEvent(new Event('change',{bubbles:true}))});
  await page.waitForFunction(()=>document.querySelector('#inspect-create-limit .idle-policy-badge')?.textContent==='Off');
  assert.equal(await page.$eval('#inspect-create-limit .mail-switch input',input=>input.disabled&&!input.checked),true);
  assert.deepEqual(updates.at(-1).capabilities,{...capabilities,createAgentBox:{...capabilities.createAgentBox,maxBoxes:5,enabled:false},mcpTools:{...capabilities.mcpTools,allowedTools:['take_screenshot']}});
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
