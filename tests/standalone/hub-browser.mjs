// Opt-in live check: only an explicitly selected isolated test hub is mutated.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import puppeteer from 'puppeteer-core';
const debug=process.env.VMBOX_HUB_CDP,origin=process.env.VMBOX_HUB_URL,passwordFile=process.env.VMBOX_HUB_PASSWORD_FILE;
assert(debug&&origin&&passwordFile,'Explicit test hub, private password file and test browser required');
const info=await(await fetch(debug+'/json/version')).json(),ws=new URL(info.webSocketDebuggerUrl);ws.host=new URL(debug).host;
const browser=await puppeteer.connect({browserWSEndpoint:ws.href,protocolTimeout:60000});
try{
 const context=await browser.createBrowserContext(),page=await context.newPage();await page.setViewport({width:1440,height:1100});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.bringToFront();await page.goto(origin);await page.waitForSelector('#login');
 await page.type('#password',readFileSync(passwordFile,'utf8').trim());await page.click('#login button');await page.waitForSelector('#boxes .hub-card');
 assert(await page.$('#policy-form'),'Host budget editor missing');assert(await page.$('#create-form'),'Creation form missing');
 await page.waitForSelector('#boxes .hub-card a[href^="/boxes/"]',{timeout:120000});
 const selector=process.env.VMBOX_HUB_TEST_BOX_ID?'#boxes .hub-card[data-box-id="'+process.env.VMBOX_HUB_TEST_BOX_ID+'"] a[href^="/boxes/"]':'#boxes .hub-card a[href^="/boxes/"]';const url=await page.$eval(selector,e=>e.href),id=new URL(url).pathname.split('/')[2];
 const workspace=await context.newPage();workspace.on('pageerror',e=>errors.push(e.message));await workspace.setViewport({width:1440,height:1100});await workspace.bringToFront();await workspace.goto(url);
 try {
  await workspace.waitForFunction(()=>document.querySelector('[data-scope="desktop"] [data-role="status"]')?.textContent==='desktop connected',{timeout:30000});
 } catch(e) {
  const diagnostic=await workspace.evaluate(()=>({path:location.pathname,title:document.title,statuses:[...document.querySelectorAll('[data-role="status"]')].map(e=>e.textContent),base:window.VBOX_BASE_PATH}));
  console.log('Workspace diagnostics:',JSON.stringify(diagnostic),JSON.stringify(errors));
  await workspace.screenshot({path:'/tmp/vbox-hub-workspace-failed.png',fullPage:true});throw e;
 }

 await workspace.waitForFunction(()=>document.querySelector('[data-scope="terminal"] [data-role="status"]')?.textContent.startsWith('connected'),{timeout:60000});
 assert.equal(await workspace.$('iframe'),null);assert.equal(await workspace.$('#chat-list'),null);
 const sizes=await workspace.evaluate(()=>['desktop','terminal'].map(k=>{const x=document.querySelector('[data-scope="'+k+'"] [data-role="'+k+'-screen"]').getBoundingClientRect();return {w:x.width,top:x.top,bottom:x.bottom}}));assert(Math.abs(sizes[0].w-sizes[1].w)<2);assert(sizes[1].top>sizes[0].bottom);
 const live=await workspace.$eval('#open-view',e=>e.href);assert(new URL(live).pathname.startsWith('/boxes/'+id+'/view'));
 const anonymous=await browser.createBrowserContext(),viewer=await anonymous.newPage();await viewer.bringToFront();await viewer.goto(live);await viewer.waitForFunction(()=>document.querySelector('[data-role="desktop-screen"] img')?.naturalWidth>100,{timeout:60000});assert.equal(await viewer.$('[data-role="desktop-controls"] button'),null);
 const denied=await fetch(url+'v1/standalone/messages',{redirect:'manual',headers:{Authorization:'Bearer '+new URL(live).hash.slice(1)}});assert.equal(denied.status,303,'Public view gained private chat access');await anonymous.close();
 await workspace.bringToFront();const response=workspace.waitForResponse(r=>r.url().endsWith('/v1/standalone/messages')&&r.request().method()==='POST');
 await workspace.type('#chat-input','Authorized hub browser test: use chat_message to say exactly HUB_BROWSER_OK. Do nothing else.');await workspace.click('#send');assert.equal((await response).status(),202);
 await workspace.waitForFunction(()=>document.querySelector('[data-role="messages"]').textContent.includes('HUB_BROWSER_OK'),{timeout:15000});
 await workspace.screenshot({path:'/tmp/vbox-hub-workspace-browser.png',fullPage:true});await page.bringToFront();await page.screenshot({path:'/tmp/vbox-hub-browser.png',fullPage:true});
 console.log('hub password login, budget/creation UI, prefixed desktop + TMUX, matching widths, public read-only link, private chat and message submission: PASS');
 await page.bringToFront();await page.setViewport({width:390,height:844});await page.goto(origin);await page.waitForSelector('#boxes');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:'/tmp/vbox-hub-mobile-browser.png',fullPage:true});
 await page.click('#hub-logout, #logout');await page.waitForSelector('#login');
 await workspace.bringToFront();await workspace.waitForFunction(()=>{const s=document.querySelector('[data-scope="desktop"] [data-role="status"]')?.textContent||'';return /disconnected|failed/i.test(s)},{timeout:20000,polling:100});
 assert.deepEqual(errors,[]);console.log('mobile layout and logout closing an active desktop viewer: PASS');
 console.log('test worker ID:',id);
}finally{await browser.close()}
