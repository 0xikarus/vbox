// Opt-in test for an explicitly identified disposable standalone instance.
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {readFileSync} from 'node:fs';
import puppeteer from 'puppeteer-core';
const c=process.env.VMBOX_STANDALONE_CONTAINER,engine=process.env.VMBOX_STANDALONE_DOCKER_HOST,debug=process.env.VMBOX_STANDALONE_CDP,origin=process.env.VMBOX_STANDALONE_ORIGIN,passwordFile=process.env.VMBOX_STANDALONE_PASSWORD_FILE;
assert(c&&engine&&debug&&origin&&passwordFile,'Explicit disposable container and private password file required');
assert.equal(execFileSync('docker',['--host',engine,'inspect','--format','{{index .Config.Labels "dev.vmbox.account"}}',c],{encoding:'utf8'}).trim(),'standalone');
const info=await(await fetch(debug+'/json/version')).json(),ws=new URL(info.webSocketDebuggerUrl);ws.host=new URL(debug).host;
const browser=await puppeteer.connect({browserWSEndpoint:ws.href});
try{
 const context=await browser.createBrowserContext();
 const page=await context.newPage();await page.setViewport({width:1400,height:1000});
 await page.goto(origin);await page.waitForSelector('#auth');assert.equal(await page.$('[data-scope=\"desktop\"]'),null);
 await page.type('#password',readFileSync(passwordFile,'utf8').trim());await page.click('#auth button');await page.waitForSelector('[data-scope="desktop"]');
 assert.equal(await page.$('iframe'),null,'Workspace must use one document');
 assert.equal(await page.$('#chat-list'),null,'Standalone must not include the fleet chat sidebar');
 assert(await page.$('#chat-conversation #chat-composer textarea'),'Use controller chat structure and multiline composer');
 assert.equal(await page.$eval('body',e=>getComputedStyle(e).fontFamily.includes('Inter')),true,'Use shared controller typography');
 const errors=[];page.on('pageerror',e=>errors.push(e.message));

 await page.waitForFunction(()=>document.querySelector('[data-scope="desktop"] [data-role="status"]').textContent==='desktop connected',{timeout:90000});
 await page.waitForFunction(()=>document.querySelector('[data-scope="terminal"] [data-role="status"]').textContent.startsWith('connected'),{timeout:30000});
 await page.waitForSelector('[data-role="compose"]:not([hidden])');
 const boxes=await page.evaluate(()=>['desktop','terminal'].map(scope=>{const r=document.querySelector('[data-scope="'+scope+'"]').getBoundingClientRect();return {y:r.y,bottom:r.bottom,width:r.width}}));assert(boxes[1].y>=boxes[0].bottom-1,'tmux must sit below desktop');assert(Math.abs(boxes[0].width-boxes[1].width)<2,'desktop and TMUX widths must match');
 assert.equal(await page.$$eval('[id]',nodes=>new Set(nodes.map(n=>n.id)).size===nodes.length),true,'IDs must be unique');
 const link=await page.$eval('#open-view',e=>e.href);
 const anonymous=await browser.createBrowserContext(),view=await anonymous.newPage();await view.goto(link);await view.waitForFunction(()=>document.querySelector('[data-role=\"desktop-screen\"] img')?.naturalWidth>100,{timeout:30000});assert.equal(await view.$('[data-role=\"desktop-controls\"] button'),null);await anonymous.close();
 if(process.env.VMBOX_STANDALONE_TEST_IMAGE==='1'){
  await page.waitForFunction(()=>document.querySelector('img.chat-image')?.naturalWidth>100,{timeout:30000});
  await page.click('img.chat-image');await page.waitForSelector('dialog.image-preview[open] img');
  assert(await page.$eval('dialog.image-preview img',e=>e.naturalWidth>100));
  await page.click('dialog.image-preview button');
  console.log('archived chat image and full-size preview: PASS');
 }
 await page.screenshot({path:'/tmp/vbox-compact-browser.png',fullPage:true});
 console.log('password login, simultaneous desktop + tmux + chat, and independent read-only link: PASS');
 const mobile=await context.newPage();await mobile.setViewport({width:390,height:844});await mobile.goto(origin);await mobile.waitForSelector('[data-scope=\"desktop\"]');assert(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'mobile horizontal overflow');await mobile.close();
 console.log('mobile layout: PASS');assert.deepEqual(errors,[],'No browser script errors');
 if(process.env.VMBOX_STANDALONE_TEST_SHUTDOWN==='1'){
  const response=page.waitForResponse(r=>r.url().endsWith('/v1/standalone/shutdown')&&r.request().method()==='POST');
  await page.click('#shutdown');assert.equal((await response).status(),202);console.log('authenticated shutdown button: PASS');
 }

}finally{await browser.close().catch(()=>{})}
