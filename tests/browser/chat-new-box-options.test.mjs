import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const profiles=[
 {application:'claude',name:'Work Claude',model:'claude-sonnet-4-5',email:'work@example.test'},
 {application:'claude',name:'Personal Claude',model:'claude-sonnet-4-5',email:'personal@example.test'},
 {application:'codex',name:'Work Codex',model:'gpt-5',email:'work@example.test'},
 {application:'github',name:'Work GitHub',host:'github.com',user:'work'},
];
const tools=[
 {id:'desktop',name:'Enable desktop',description:'Browser and desktop'},
 {id:'foundry',name:'Foundry',description:'forge and cast'},
 {id:'blender',name:'Blender',description:'3D tools'},
];
const providers=[{provider:'shared-worker',name:'primary'},{provider:'railway',name:'cloud'}];

function fixtureServer(configuredProviders,failedPaths=[],defaultResponse=null,data={}){
 const requests=[],creations=[];
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   requests.push(path);
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/logical-boxes'&&req.method==='POST'){
    let raw='';for await(const chunk of req)raw+=chunk;
    creations.push(JSON.parse(raw));
    return res.end(JSON.stringify({id:'new-box',name:creations.at(-1).name}));
   }
   if(failedPaths.includes(path)){res.statusCode=503;return res.end('{"error":"Fixture service unavailable"}')}
   let value={};
   switch(path){
    case '/v1/whoami':value={role:'owner',accountId:'account-a'};break;
    case '/v1/logical-boxes':case '/v1/grid-boxes':case '/v1/box-conversations':case '/v1/chat-commands':value=[];break;
    case '/v1/chat-sidebar-layout':value={exists:true,groups:[],members:{}};break;
    case '/v1/tool-presets':value=data.tools??tools;break;
    case '/v1/login-profiles':value=data.profiles??profiles;break;
    case '/v1/instruction-presets':value={defaultName:'',presets:[]};break;
    case '/v1/provider-credentials':value=configuredProviders;break;
    case '/v1/controller-defaults':if(defaultResponse)value=defaultResponse;else{res.statusCode=409;value={error:'controller provider default not configured; use providers default PROVIDER NAME'}}break;
    case '/v1/fleet/status':value={freeSlots:2,actualSlots:3,occupiedSlots:1,desiredSlots:3};break;
    case '/v1/push/vapid-key':res.statusCode=404;break;
   }
   return res.end(JSON.stringify(value));
  }
  const file=resolve(web,path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{
   res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html','.woff2':'font/woff2'})[extname(file)]||'text/plain');
   res.end(await readFile(file));
  }catch{res.statusCode=404;res.end()}
 });
 return {server,requests,creations};
}

test('New box loads saved accounts and tools when the default provider is unset',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const [width,configuredProviders,defaultResponse] of [[360,providers,null],[390,providers,null],[1440,providers,null],[390,[],null],[390,[providers[0]],{provider:'shared-worker',providerCredential:'primary',inferred:true}]]){
   const {server,requests,creations}=fixtureServer(configuredProviders,[],defaultResponse);
   await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();
    await page.setViewport({width,height:width<600?844:900,isMobile:width<600,hasTouch:width<600});
    await page.goto(`http://127.0.0.1:${server.address().port}/chat`);
    await page.click('#new-box');
    await page.waitForFunction(()=>document.querySelector('#create-tools label')?.textContent.includes('Foundry'));
    assert.ok(['/v1/login-profiles','/v1/tool-presets','/v1/provider-credentials','/v1/controller-defaults'].every(path=>requests.includes(path)));
    assert.equal(await page.$$eval('.create-profile-option',nodes=>nodes.length),3,'None plus two Claude accounts');
    assert.deepEqual(await page.$$eval('#create-tools label',nodes=>nodes.map(node=>node.textContent.trim())),['Foundry','Blender']);
    assert.deepEqual(await page.$eval('select[name="loginProfile"]',select=>[...select.options].map(option=>option.textContent)),['None','Work Claude','Personal Claude']);
    await page.$eval('.create-profile-option[data-profile*="Personal Claude"]',button=>button.click());
    assert.match(await page.$eval('select[name="loginProfile"]',select=>select.value),/Personal Claude/);
    assert.deepEqual(await page.$eval('select[name="githubProfile"]',select=>[...select.options].map(option=>option.textContent)),['None','Work GitHub']);
    await page.select('select[name="defaultAgent"]','codex');
    assert.deepEqual(await page.$$eval('.create-profile-option strong',nodes=>nodes.map(node=>node.textContent)),['None','Work Codex']);
    const status=await page.$eval('#new-box-status',node=>node.textContent);
    if(configuredProviders.length){
     assert.equal(status,'');
     assert.equal(await page.$eval('#create-pool',select=>select.options.length),configuredProviders.length+1);
     assert.equal(await page.$eval('#create-pool',select=>select.required),!defaultResponse);
     if(defaultResponse)assert.match(await page.$eval('#create-provider-status',node=>node.textContent),/Choose it as your default in Providers/);
     else{
      assert.match(await page.$eval('#new-box-summary',node=>node.textContent),/Choose provider/);
      assert.equal(await page.$eval('#create-preview-card',card=>[...card.querySelectorAll('.preview-row .k')].some(label=>label.textContent==='Pool')),false);
     }
     await page.select('#create-pool','0');
     assert.match(await page.$eval('#new-box-summary',node=>node.textContent),/shared-worker\/primary/);
     await page.$eval('#create-box input[name="name"]',input=>{input.value='new-box';input.dispatchEvent(new Event('input',{bubbles:true}))});
     await page.click('#create-box-submit');
     await page.waitForFunction(()=>document.querySelector('#new-box-modal').hidden);
     assert.equal(creations[0].provider,'shared-worker');
     assert.equal(creations[0].providerCredential,'primary');
    }else{
     assert.equal(status,'Choose a default provider in Providers.');
     assert.equal(await page.$eval('#create-provider-status a',link=>link.getAttribute('href')),'/#providers');
    }
    assert.doesNotMatch(status,/controller provider default|PROVIDER NAME/);
    await page.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});

test('New box reports failed and empty sections without hiding other options',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const failedPath of ['/v1/login-profiles','/v1/tool-presets','/v1/instruction-presets','/v1/provider-credentials','empty']){
   const {server}=fixtureServer(providers,failedPath==='empty'?[]:[failedPath],null,failedPath==='empty'?{profiles:[],tools:[]}:{});
   await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();
    await page.goto(`http://127.0.0.1:${server.address().port}/chat`);
    await page.click('#new-box');
    await page.waitForFunction(()=>document.querySelector('#create-pool').options.length>0);
    assert.equal(await page.$$eval('.create-profile-option',nodes=>nodes.length),failedPath==='/v1/login-profiles'||failedPath==='empty'?0:3);
    assert.equal(await page.$$eval('#create-tools label',nodes=>nodes.length),failedPath==='/v1/tool-presets'||failedPath==='empty'?0:2);
    const status=await page.evaluate(()=>({logins:document.querySelector('#profile-choices-status').textContent,tools:document.querySelector('#create-tools-status').textContent,instructions:document.querySelector('#create-instructions-status').textContent,provider:document.querySelector('#create-provider-status').textContent}));
    if(failedPath==='/v1/login-profiles')assert.match(status.logins,/Could not load saved logins/);
    if(failedPath==='/v1/tool-presets'){
     assert.match(status.tools,/Could not load tool presets/);
     assert.equal(await page.$eval('#create-tools',node=>getComputedStyle(node).display),'none');
    }
    if(failedPath==='/v1/instruction-presets')assert.match(status.instructions,/Could not load saved instructions/);
    if(failedPath==='/v1/provider-credentials')assert.match(status.provider,/Could not load providers/);
    if(failedPath==='empty'){
     assert.match(status.logins,/No saved Claude login/);
     assert.match(status.tools,/No optional tool presets available/);
     assert.equal(await page.$eval('#create-tools',node=>getComputedStyle(node).display),'none');
    }
    await page.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});
