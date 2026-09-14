import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';
const source=await readFile('internal/browser/password.go','utf8');
const script=name=>source.match(new RegExp('const '+name+' = `([\\s\\S]*?)`'))[1];
test('password bridge checks origin, element identity, focus and navigation',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 try {
  const page=await browser.newPage();
  await page.setRequestInterception(true);
  page.on('request',r=>r.respond({status:200,contentType:'text/html',body:'<input id="pw" type="password"><input id="other" type="password"><input id="text"><script>window.submits=0;document.addEventListener("submit",()=>submits++);</script>'}));
  await page.goto('https://password.test/');await page.bringToFront();await page.focus('#pw');
  const cdp=await page.createCDPSession();
  const {frameTree}=await cdp.send('Page.getFrameTree');
  const {executionContextId}=await cdp.send('Page.createIsolatedWorld',{frameId:frameTree.frame.id,worldName:'vmbox-private-input'});
  const probe=()=>cdp.send('Runtime.evaluate',{contextId:executionContextId,expression:script('passwordProbe'),returnByValue:false});
  const {result}=await probe();assert.ok(result.objectId);
  const insert=(origin='https://password.test')=>cdp.send('Runtime.callFunctionOn',{objectId:result.objectId,functionDeclaration:script('passwordInsert'),arguments:[{value:origin},{value:'synthetic-password'}],returnByValue:true});
  assert.equal((await insert('https://wrong.test')).result.value,false);
  assert.equal(await page.$eval('#pw',e=>e.value),'');
  await page.focus('#other');assert.equal((await insert()).result.value,false);
  await page.focus('#pw');assert.equal((await insert()).result.value,true);
  assert.equal(await page.$eval('#pw',e=>e.value),'synthetic-password');assert.equal(await page.evaluate(()=>submits),0);
  await page.focus('#text');assert.equal((await probe()).result.subtype,'null');
  await page.focus('#pw');await page.$eval('#pw',e=>e.readOnly=true);assert.equal((await insert()).result.value,false);
  await page.goto('https://password.test/next');
  await assert.rejects(insert());
 } finally {await browser.close()}
});
