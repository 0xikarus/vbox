import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {extname} from 'node:path';
import puppeteer from 'puppeteer-core';

async function fixture(t,{history=false}={}){
 const task={id:'fixture-task',name:'queued-example',goal:'fixture goal',state:'queued',queuePosition:2,box:null,links:[],createdAt:new Date().toISOString()};
 const past={...task,id:'finished-task',name:'finished-example',state:'completed',queuePosition:undefined,finishedAt:new Date().toISOString()};
 const calls=[],services=[],presets=[];const errors=[];
 const server=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://localhost'),path=u.pathname;
  if(!path.startsWith('/v1/')){try{const name=path==='/tasks'?'tasks.html':path.slice(1);res.setHeader('Content-Type',extname(name)==='.js'?'text/javascript':extname(name)==='.css'?'text/css':'text/html');return res.end(await readFile('internal/controller/web/'+name))}catch{res.statusCode=404;return res.end()}}
  let body='';for await(const chunk of req)body+=chunk;const data=body?JSON.parse(body):null;calls.push({method:req.method,path,data,key:req.headers['idempotency-key']});res.setHeader('Content-Type','application/json');
  if(path==='/v1/task-profiles'){if(req.method==='POST'){const p={id:'preset-'+(presets.length+1),...data};presets.push(p);res.statusCode=201;return res.end(JSON.stringify(p))}return res.end(JSON.stringify(presets))}
  if(path.startsWith('/v1/task-profiles/')){const index=presets.findIndex(p=>p.id===path.split('/').pop());if(index<0){res.statusCode=404;return res.end('{}')}if(req.method==='DELETE'){presets.splice(index,1);res.statusCode=204;return res.end()}if(req.method==='PUT')Object.assign(presets[index],data);return res.end(JSON.stringify(presets[index]))}
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/tasks/profiles')return res.end(JSON.stringify([{id:'lp_fixture',application:'codex',label:'codex · ABCD'}]));
  if(path==='/v1/task-service-credentials/profiles')return res.end(JSON.stringify([{id:'lp_fixture',application:'codex',label:'codex · ABCD',name:'private@example.test'}]));
  if(path==='/v1/agent-roles')return res.end('[]');
  if(path==='/v1/task-service-credentials'){if(req.method==='POST'){services.push({id:'service1',...data});res.statusCode=201;return res.end(JSON.stringify({id:'service1',token:'fixture-token-shown-once'}))}return res.end(JSON.stringify(services))}
  if(path==='/v1/tasks'){if(req.method==='POST'){Object.assign(task,data);res.statusCode=201;return res.end(JSON.stringify(task))}const state=u.searchParams.get('state'),all=history?[task,past]:[task],finished=s=>['completed','failed','timed_out','abandoned','cancelled'].includes(s);return res.end(JSON.stringify({tasks:all.filter(t=>!state||(state==='current'?!finished(t.state):state==='history'?finished(t.state):t.state===state)),nextCursor:''}))}
  if(path==='/v1/tasks/fixture-task')return res.end(JSON.stringify(task));
  if(path.endsWith('/messages')){if(req.method==='POST'){res.statusCode=202;return res.end('{}')}return res.end(JSON.stringify([{direction:'agent',text:'<script>window.injected=true</script>'}]))}
  if(path.endsWith('/events'))return res.end(JSON.stringify({events:[{kind:'queued',text:'Waiting for capacity',createdAt:task.createdAt}]}));
  if(path.endsWith('/cancel')){task.state='cancelled';delete task.queuePosition;return res.end(JSON.stringify(task))}
  if(path.endsWith('/links')){if(req.method==='POST'){task.links=[{id:'link1',kind:data.kinds[0],expiresAt:new Date(Date.now()+3600000).toISOString()}];res.statusCode=201;return res.end(JSON.stringify([{...task.links[0],url:'http://example.test/t/fixture'}]))}task.links=[];res.statusCode=204;return res.end()}
  if(path.endsWith('/final-screenshot')){res.statusCode=409;return res.end('{}')}
  res.statusCode=404;res.end('{}');
 });await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>server.close());
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});t.after(()=>browser.close());const page=await browser.newPage();page.on('pageerror',e=>errors.push(e.message));
 await page.goto('http://127.0.0.1:'+server.address().port+'/tasks');await page.waitForSelector('.task-card');
 return {page,task,calls,errors};
}
test('task UI queues a resource-configured task with opaque profile IDs',async t=>{
 const {page,calls,errors}=await fixture(t);await page.click('#create-panel summary');await page.type('[name=name]','test-task');await page.type('[name=goal]','Return fixture result');await page.select('[name=profile]','lp_fixture');await page.type('[name=memoryGiB]','4');await page.click('#create-task button[type=submit]');
 await page.waitForFunction(()=>document.querySelector('#task-detail').textContent.includes('Queue position 2'));
 const req=calls.find(c=>c.path==='/v1/tasks'&&c.method==='POST');assert.equal(req.data.diskGiB,10);assert.equal(req.data.memoryGiB,4);assert.equal(req.data.loginProfiles[0].profileId,'lp_fixture');assert.ok(req.key);assert.ok(!JSON.stringify(req.data).includes('private@example.test'));assert.equal(await page.$('#task-message'),null);assert.deepEqual(errors,[]);
});
test('task result UI safely renders files, transcript, and final actions',async t=>{
 const {page,task,errors}=await fixture(t);Object.assign(task,{state:'completed',queuePosition:undefined,result:{text:'<img src=x onerror=alert(1)> done',files:[{id:'file1',name:'report.txt',size:5}]},deleteAt:new Date(Date.now()+86400000).toISOString()});await page.click('.task-card');
 await page.waitForSelector('a[download="report.txt"]');assert.equal(await page.$('#task-message'),null);assert.equal(await page.evaluate(()=>window.injected),undefined);assert.ok(await page.$('a[href$="/files/file1"]'));assert.match(await page.$eval('#task-detail',n=>n.textContent),/Delete task and results/);assert.deepEqual(errors,[]);
});
test('owner can map profiles and mint a scoped token shown once',async t=>{
 const {page,calls,errors}=await fixture(t);await page.click('#service-panel summary');await page.type('#service-form [name=name]','fixture-client');await page.click('#service-profiles input');await page.click('#service-form button');await page.waitForFunction(()=>!document.querySelector('#new-token').hidden);
 const req=calls.find(c=>c.path==='/v1/task-service-credentials'&&c.method==='POST');assert.deepEqual(req.data.profileIds,['lp_fixture']);assert.equal(req.data.allowControl,false);assert.equal(await page.$eval('#new-token textarea',n=>n.value),'fixture-token-shown-once');await page.click('#clear-token');assert.equal(await page.$eval('#new-token textarea',n=>n.value),'');assert.deepEqual(errors,[]);
});

test('task list switches between all, current and historical tasks',async t=>{
 const {page,errors}=await fixture(t,{history:true});
 assert.equal(await page.$$eval('.task-card',nodes=>nodes.length),2);
 await page.select('#state-filter','history');
 await page.waitForFunction(()=>document.querySelectorAll('.task-card').length===1&&document.querySelector('.task-card').dataset.task==='finished-task');
 assert.match(await page.$eval('.task-card',n=>n.textContent),/Finished /);
 await page.select('#state-filter','current');
 await page.waitForFunction(()=>document.querySelectorAll('.task-card').length===1&&document.querySelector('.task-card').dataset.task==='fixture-task');
 await page.select('#state-filter','');
 await page.waitForFunction(()=>document.querySelectorAll('.task-card').length===2);
 assert.deepEqual(errors,[]);
});

test('owner saves, uses, edits and deletes a task profile without creating a task while saving',async t=>{
 const {page,calls,errors}=await fixture(t);
 await page.click('#new-task-profile');
 await page.$eval('#task-profile-name',n=>n.value='codex-test');
 await page.select('#create-task [name=profile]','lp_fixture');
 await page.$eval('#create-task [name=diskGiB]',n=>n.value='4');
 await page.$eval('#create-task [name=memoryGiB]',n=>n.value='2');
 await page.$eval('#create-task [name=swapGiB]',n=>n.value='1');
 await page.$eval('#create-task [name=model]',n=>n.value='gpt-6-sol');
 await page.$eval('#create-task [name=reasoningEffort]',n=>n.value='high');
 await page.click('#save-task-profile');await page.waitForSelector('.task-profile-card');
 const saved=calls.find(c=>c.path==='/v1/task-profiles'&&c.method==='POST').data;
 assert.equal(saved.name,'codex-test');assert.equal(saved.settings.diskGiB,4);assert.equal(saved.settings.memoryGiB,2);assert.equal(saved.settings.swapGiB,1);
 assert.deepEqual(saved.settings.tools,['desktop']);assert.deepEqual(saved.settings.loginProfiles,[{profileId:'lp_fixture',model:'gpt-6-sol',reasoningEffort:'high'}]);
 assert.equal(calls.some(c=>c.path==='/v1/tasks'&&c.method==='POST'),false);
 await page.click('.task-profile-card button');
 assert.equal(await page.$eval('#task-settings',n=>n.disabled),true);
 await page.setViewport({width:390,height:844});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'selected profile configuration fits a phone');
 await page.type('#create-task [name=name]','preset-job');await page.type('#create-task [name=goal]','fixture goal');
 await page.click('#create-task button[type=submit]');
 await page.waitForFunction(()=>document.querySelector('#status').textContent==='Task queued.');
 assert.deepEqual(calls.find(c=>c.path==='/v1/tasks'&&c.method==='POST').data,{name:'preset-job',goal:'fixture goal',taskProfile:'codex-test'});
 await page.$$eval('.task-profile-card button',buttons=>buttons.find(b=>b.textContent==='Edit').click());
 assert.equal(await page.$eval('#task-settings',n=>n.disabled),false);assert.equal(await page.$eval('#task-profile-name',n=>n.readOnly),true);
 await page.$eval('#create-task [name=diskGiB]',n=>n.value='8');await page.click('#save-task-profile');
 await page.waitForFunction(()=>document.querySelector('.task-profile-card').textContent.includes('8 GiB disk'));
 assert.equal(calls.find(c=>c.path==='/v1/task-profiles/preset-1'&&c.method==='PUT').data.settings.diskGiB,8);
 page.once('dialog',dialog=>dialog.accept());await page.$$eval('.task-profile-card button',buttons=>buttons.find(b=>b.textContent==='Delete').click());
 await page.waitForFunction(()=>document.querySelector('#task-profiles-list').textContent.includes('No task profiles yet.'));assert.deepEqual(errors,[]);
});
