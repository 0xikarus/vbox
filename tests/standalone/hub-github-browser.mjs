// Browser coverage against isolated fixture APIs; creates no Docker resources.
import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import puppeteer from 'puppeteer-core';

test('GitHub upload remains independent of the agent and is attached explicitly', {timeout:30000}, async()=>{
 const source=await readFile('internal/hub/page.go','utf8');
 const picker=await readFile('internal/controller/web/model-picker.js','utf8');
 const html=source.match(/const hubPage = `([\s\S]*)`/)[1].replace('<script>',()=>'<script>'+picker+'</script><script>');
 const profiles=[{id:'hp-agent',name:'Claude fixture',agent:'claude',model:'sonnet'},{id:'hp-codex',name:'Codex fixture',agent:'codex',model:'old-saved-model'}];
 let uploaded,created,browser;
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://fixture').pathname;
  res.setHeader('Content-Type',path==='/'?'text/html':'application/json');
  if(path==='/')return res.end(html);
  let body='';for await(const part of req)body+=part;
  if(path==='/api/status')return res.end(JSON.stringify({allocated:{running:0,cpus:0,memoryMiB:0},host:{availableMiB:4096,diskFreeMiB:8192},policy:{maxRunning:2,cpus:1,memoryMiB:2048},defaults:{agent:'claude',cpu:.25,memoryMiB:256}}));
  if(path==='/api/profiles'&&req.method==='POST'){uploaded=JSON.parse(body);const p={id:'hp-github',name:uploaded.name,agent:'github'};profiles.push(p);return res.end(JSON.stringify(p))}
  if(path==='/api/profiles/hp-agent/models')return res.end(JSON.stringify({source:'Claude live fixture',models:[{id:'claude-live-model',label:'Live Claude',reasoning:true,reasoningEfforts:['low','high']}]}));
  if(path==='/api/profiles/hp-codex/models')return res.end(JSON.stringify({source:'Codex account fixture',models:[{id:'codex-live-model',label:'Live Codex',reasoning:true,reasoningEfforts:['low','high']}]}));
  if(path==='/api/profiles')return res.end(JSON.stringify(profiles));
  if(path==='/api/boxes'&&req.method==='POST'){created=JSON.parse(body);res.statusCode=202;return res.end(JSON.stringify({id:'hb-fixture',state:'queued'}))}
  if(path==='/api/boxes')return res.end(JSON.stringify([{id:'hb-queued',spec:{name:'Queued box',cpu:.25,memoryMiB:256,agent:'claude'},state:'queued',desired:'running',url:'/boxes/hb-queued/'},{id:'hb-stopped',spec:{name:'Stopped box',cpu:.25,memoryMiB:256,agent:'claude'},state:'stopped',desired:'stopped',url:'/boxes/hb-stopped/'}]));
  res.statusCode=404;res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
  const page=await browser.newPage();page.setDefaultTimeout(8000);page.on('pageerror',e=>console.error('Fixture page error:',e.message));await page.goto('http://127.0.0.1:'+server.address().port);
  await page.waitForFunction(()=>document.querySelector('#create-form').elements.agent.value==='claude');
  assert.equal(await page.$$eval('#boxes a',nodes=>nodes.filter(n=>n.textContent==='Open chat').length),2,'queued and stopped boxes need workspace links');
  await page.evaluate(()=>document.querySelector('#profiles').open=true);
  await page.select('#profile-upload select[name=agent]','github');
  await page.type('#profile-upload input[name=name]','GitHub fixture');
  await page.evaluate(()=>{
   const transfer=new DataTransfer();transfer.items.add(new File(['github.com:\n  user: fixture\n  oauth_token: synthetic-token\n'],'hosts.yml'));
   const input=document.querySelector('#profile-upload input[type=file]');input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}));
  });
  await page.click('#profile-upload button.primary');
  await page.waitForFunction(()=>document.querySelector('#create-form').elements.githubProfile.value==='hp-github');
  assert.equal(uploaded.agent,'github');assert.deepEqual(Object.keys(uploaded.files),['hosts.yml']);
  assert.equal(await page.$eval('#create-form select[name=agent]',n=>n.value),'claude','GitHub upload changed the selected agent');
  await page.evaluate(()=>document.querySelector('#create-form').closest('details').open=true);
  await page.select('#create-form select[name=profile]','hp-agent');
  await page.click('.model-picker-open');await page.waitForSelector('.model-picker-dialog[open]');await page.waitForSelector('.model-picker-option[data-model=claude-live-model]');await page.click('.model-picker-option[data-model=claude-live-model]');assert.equal(await page.$('.model-picker-option[data-model=opus]'),null,'static aliases must not replace the live account catalog');await page.click('.model-picker-apply');
  await page.type('#create-form input[name=name]','Disposable fixture');
  await page.click('#create-form button.primary');
  try { await page.waitForFunction(()=>document.querySelector('#status').textContent==='Box queued') }
  catch(error){
   await page.screenshot({path:'/tmp/vbox-hub-model-picker-failure.png'});
   const details=await page.evaluate(()=>({url:location.href,openDialog:!!document.querySelector('dialog[open]'),submit:document.querySelector('#create-form button.primary').outerHTML,handler:typeof document.querySelector('#create-form').onsubmit,status:document.querySelector('#status').textContent,invalid:[...document.querySelector('#create-form').elements].filter(e=>e.willValidate&&!e.validity.valid).map(e=>({name:e.name,error:e.validationMessage}))}));
   throw Error(error.message+' '+JSON.stringify(details));
  }
  assert.equal(created.agent,'claude');assert.equal(created.agentModel,'claude-live-model');assert.deepEqual(created.profiles,['hp-agent','hp-github']);
  await page.select('#create-form select[name=agent]','codex');
  assert.equal(await page.$eval('#create-form select[name=githubProfile]',n=>n.value),'hp-github');
  await page.select('#create-form select[name=profile]','hp-codex');await page.click('.model-picker-open');
  await page.waitForSelector('.model-picker-option[data-model=codex-live-model]');
  assert.equal(await page.$('.model-picker-option[data-model="gpt-5.6-sol"]'),null,'live Codex models must not be mixed with guesses');
  await page.click('.model-picker-option[data-model=codex-live-model]');await page.select('.model-picker-effort','high');await page.click('.model-picker-apply');
  await page.type('#create-form input[name=name]','Codex fixture');await page.click('#create-form button.primary');
  await page.waitForFunction(()=>document.querySelector('#create-form input[name=name]').value==='');
  assert.equal(created.agent,'codex');assert.equal(created.agentModel,'codex-live-model');assert.equal(created.agentReasoningEffort,'high');assert.deepEqual(created.profiles,['hp-codex','hp-github']);
 }finally{await browser?.close();await new Promise(resolve=>server.close(resolve))}
});
