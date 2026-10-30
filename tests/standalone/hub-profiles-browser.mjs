import assert from 'node:assert/strict';
import {readFileSync,writeFileSync,mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import puppeteer from 'puppeteer-core';
// Opt-in: creates one identified disposable box/profile and deletes only those IDs.
const debug=process.env.VMBOX_HUB_CDP,origin=process.env.VMBOX_HUB_URL,endpoint=process.env.VMBOX_HUB_TEST_MODEL_URL,model=process.env.VMBOX_HUB_TEST_MODEL,contextLimit=Number(process.env.VMBOX_HUB_TEST_CONTEXT||32768);
assert(debug&&origin&&endpoint&&model&&process.env.VMBOX_HUB_PASSWORD_FILE,'Explicit test hub, browser, password file and local test model required');
const fixture=mkdtempSync(join(tmpdir(),'vbox-hub-profile-test-')),suffix=Date.now();let profileID,boxID,page;
async function api(path,method='GET'){return page.evaluate(async(path,method)=>{const r=await fetch('/api/'+path,{method,headers:{'Content-Type':'application/json'},...(method==='POST'?{body:'{}'}:{})});if(!r.ok)throw Error('Test API operation failed: '+r.status);return r.json()},path,method)}
const info=await(await fetch(debug+'/json/version')).json(),ws=new URL(info.webSocketDebuggerUrl);ws.host=new URL(debug).host;
const browser=await puppeteer.connect({browserWSEndpoint:ws.href,protocolTimeout:300000});
try {
 const context=await browser.createBrowserContext();page=await context.newPage();await page.setViewport({width:1440,height:1100});await page.bringToFront();
 const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.goto(origin);await page.waitForSelector('#login');await page.type('#password',readFileSync(process.env.VMBOX_HUB_PASSWORD_FILE,'utf8').trim());await page.click('#login button');await page.waitForSelector('#profile-upload');
 const existing=await api('profiles');await page.waitForFunction(count=>document.querySelectorAll('#profile-list .profile-row').length===count,{},existing.length);
 assert.equal(await page.$eval('#custom-model',e=>e.hidden),true);
 for(const agent of ['claude','codex']){await page.select('#create-form [name=agent]',agent);assert.equal(await page.$eval('#opencode-settings',e=>e.hidden),true);assert.equal(await page.$eval('#create-form [name=modelURL]',e=>e.disabled),true);const labels=await page.$$eval('#create-form [name=profile] option',els=>els.map(e=>e.textContent));assert.deepEqual(labels.slice(1).sort(),existing.filter(p=>p.agent===agent).map(p=>p.name).sort())}
 await page.select('#create-form [name=agent]','opencode');await page.select('#create-form [name=modelSource]','custom');assert.equal(await page.$eval('#custom-model',e=>e.hidden),false);assert.equal(await page.$eval('#create-form [name=modelURL]',e=>e.disabled),false);await page.select('#create-form [name=modelSource]','profile');assert.equal(await page.$eval('#custom-model',e=>e.hidden),true);
 await page.$eval('#profiles',e=>e.open=true);await page.type('#profile-upload [name=name]','Disposable browser provider '+suffix);
 // Browser upload uses the original filename, independently of the source path.
 const source=join(fixture,'opencode.jsonc');writeFileSync(source,'// Portable provider fixture\n'+JSON.stringify({model:'profile-local/'+model,permission:{'*':'deny','vmbox-desktop_chat_message':'allow'},provider:{'profile-local':{npm:'@ai-sdk/openai-compatible',name:'Disposable saved provider',options:{baseURL:endpoint,apiKey:'fixture-only'},models:{[model]:{name:'Profile test model',limit:{context:contextLimit,output:Math.min(4096,Math.floor(contextLimit/8))}}}}}},null,2));
 await page.$eval('#profile-upload [name=files]',(input,text)=>{const transfer=new DataTransfer();transfer.items.add(new File([text],'opencode.jsonc',{type:'application/json'}));input.files=transfer.files;input.dispatchEvent(new Event('change',{bubbles:true}))},readFileSync(source,'utf8'));
 const uploaded=page.waitForResponse(r=>r.url().endsWith('/api/profiles')&&r.request().method()==='POST');await page.click('#profile-upload button.primary');const response=await uploaded;assert.equal(response.status(),201);const profile=await response.json();profileID=profile.id;
 await page.waitForFunction(id=>document.querySelector('#create-form [name=profile]')?.value===id,{},profile.id);
 await page.type('#create-form [name=name]','Disposable saved provider '+suffix);await page.$eval('#create-form [name=cpu]',e=>e.value='.5');await page.$eval('#create-form [name=memoryMiB]',e=>e.value='1536');
 await page.type('#create-form [name=goal]','Authorized disposable profile test: use chat_message to say exactly SAVED_PROVIDER_OK. Do nothing else.');
 const created=page.waitForResponse(r=>r.url().endsWith('/api/boxes')&&r.request().method()==='POST');await page.click('#create-form button.primary');const cr=await created;assert.equal(cr.status(),202);const request=JSON.parse(cr.request().postData());assert.equal(request.modelSource,'profile');for(const k of ['modelURL','model','vision'])assert.equal(k in request,false,'Hidden setting submitted: '+k);const box=await cr.json();boxID=box.id;
 await page.waitForFunction(id=>document.querySelector('[data-box-id="'+id+'"] .state')?.textContent==='running',{timeout:240000,polling:1000},box.id);
 await page.waitForFunction(async id=>{const r=await fetch('/api/boxes/'+id+'/messages');if(!r.ok)return false;return(await r.json()).some(m=>m.direction==='agent'&&m.text.includes('SAVED_PROVIDER_OK'))},{timeout:180000,polling:1000},box.id);await page.screenshot({path:join(fixture,'hub-profiles.png'),fullPage:true});assert.deepEqual(errors,[]);
 console.log('PASS: real profiles visible; agent filtering; custom-only endpoint controls; browser JSONC upload; profile-selected native OpenCode startup; hidden settings omitted');
}finally{
 try {if(page&&boxID){await api('boxes/'+boxID+'/delete','POST');const end=Date.now()+90000;while(Date.now()<end){const exists=await page.evaluate(async id=>(await fetch('/api/boxes/'+id)).status!==404,boxID);if(!exists)break;await new Promise(r=>setTimeout(r,1000))}}if(page&&profileID)await api('profiles/'+profileID,'DELETE')}finally{rmSync(fixture,{recursive:true,force:true});await browser.close()}
}
