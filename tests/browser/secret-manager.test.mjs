import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';
const workspace=await readFile('internal/controller/web/workspace.js','utf8');
const script=workspace.slice(workspace.indexOf("const secretForm="),workspace.indexOf('// Preview pixels'));
test('secret manager fill preserves pending status until owner confirms',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage();
  await page.setContent('<p id="secret-status"></p><ul id="secret-list"></ul><button id="load-secrets">Load</button><form id="secret-form"><input name="key"><input name="origin"><input name="generate" type="checkbox" checked><input name="value" type="password"><button>Save</button></form>');
  await page.evaluate(()=>{
   window.bp='/v1/logical-boxes/box';window.requests=[];window.secretStatus='pending';
   window.api=async(path,method='GET',body)=>{
    requests.push({path,method,body});
    if(method==='GET')return[{key:'site-password',origin:'https://site.test',status:secretStatus}];
    if(path.endsWith('/confirm')){secretStatus='confirmed';return{status:'confirmed'}}
    if(path.endsWith('/type'))return{inserted:true};
    throw Error('Unexpected request');
   };
  });
  await page.addScriptTag({content:script});
  await page.click('#load-secrets');
  await page.waitForSelector('#secret-list button');
  await page.click('#secret-list button:first-of-type');
  await page.waitForFunction(()=>document.querySelector('#secret-status').textContent.includes('Password filled'));
  assert.match(await page.$eval('#secret-list',e=>e.textContent),/pending/);
  assert.equal((await page.evaluate(()=>requests)).filter(r=>r.path.endsWith('/confirm')).length,0);
  await page.click('#secret-list button:nth-of-type(2)');
  await page.waitForFunction(()=>document.querySelector('#secret-list').textContent.includes('confirmed'));
  assert.equal(await page.$eval('#secret-list',e=>e.textContent.includes('Mark accepted')),false);
  const writes=(await page.evaluate(()=>requests)).filter(r=>r.method==='POST');
  assert.deepEqual(writes.map(r=>r.path),['/v1/logical-boxes/box/secrets/site-password/type','/v1/logical-boxes/box/secrets/site-password/confirm']);
  assert.ok(writes.every(r=>r.body===undefined));
 }finally{await browser.close()}
});

const privateScript=workspace.slice(workspace.indexOf('const privateRequests='),workspace.indexOf('const interruptAgent='));
test('private request card sends the password only to its private endpoint and clears input',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage();
  await page.setContent('<main id="workspace"><details id="agent-secrets"><div id="private-secret-requests"></div></details></main><button id="logout">Logout</button>');
  await page.evaluate(()=>{
   window.bp='/v1/logical-boxes/fixture';window.runID=null;window.requests=[];window.pending=true;
   window.api=async(path,method='GET',body)=>{
    requests.push({path,method,body:body?JSON.parse(JSON.stringify(body)):undefined});
    if(method==='GET')return pending?[{key:'existingLogin',origin:'https://site.test',status:'pending'}]:[];
    if(method==='POST'){pending=false;return{status:'fulfilled'}};
   };
  });
  await page.addScriptTag({content:privateScript});
  await page.waitForSelector('#private-secret-requests input');
  await page.type('#private-secret-requests input','synthetic-user-value');
  await page.click('#private-secret-requests button');
  await page.waitForFunction(()=>!document.querySelector('#private-secret-requests input'));
  const writes=(await page.evaluate(()=>requests)).filter(r=>r.method==='POST');
  assert.deepEqual(writes,[{path:'/v1/logical-boxes/fixture/secret-requests/existingLogin',method:'POST',body:{value:'synthetic-user-value'}}]);
  assert.equal(await page.$eval('body',e=>e.textContent.includes('synthetic-user-value')),false);
 }finally{await browser.close()}
});
