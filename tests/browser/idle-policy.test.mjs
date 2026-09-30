import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css','idle-policy.js','idle-policy.css'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>[name,await readFile(root+name)])));
let seconds=14400;
let resumeSeconds=14400;
const writes=[];
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'railway',volumeName:'v1'};
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://local').pathname;
 if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
 if(path.slice(1) in assets){res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':'text/css');return res.end(assets[path.slice(1)])}
 res.setHeader('Content-Type','application/json');
 if(path==='/v1/logical-boxes/builder/idle-policy'){
  if(req.method==='PUT'){let raw='';for await(const chunk of req)raw+=chunk;const next=JSON.parse(raw).seconds;if(next>0)resumeSeconds=next;else if(seconds>0)resumeSeconds=seconds;seconds=next;writes.push(seconds)}
  return res.end(JSON.stringify({seconds,resumeSeconds}));
 }
 if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
 if(path.endsWith('/messages'))return res.end('[]');
 const values={'/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],'/v1/box-conversations':[],'/v1/profile-usage':[],'/v1/chat-commands':[],'/v1/tool-presets':[],'/v1/instruction-presets':{defaultName:'',presets:[]}};
 return res.end(JSON.stringify(values[path]??{}));
});
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
const base='http://127.0.0.1:'+server.address().port;
const screenshotDir=process.env.VMBOX_IDLE_SCREENSHOTS;
if(screenshotDir)await mkdir(screenshotDir,{recursive:true});
async function openDetails(page){
 await page.goto(base+'/chat#box=builder');
 await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
 await page.evaluate(()=>{if(document.querySelector('#inspect').hidden)document.querySelector('#chat-info').click()});
 await page.evaluate(()=>{const fold=document.querySelector('details[data-fold=technical]');if(fold&&!fold.open)fold.querySelector('summary').click()});
 await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-switch input:not(:disabled)'));
}
try{
 await test('per-box switch disables and restores automatic hibernation without changing the interval',async()=>{
  const page=await browser.newPage();await page.setViewport({width:1320,height:850});await openDetails(page);
  assert.equal(await page.$eval('#inspect-idle-policy .idle-policy-switch input',el=>el.checked),true);
  await page.$eval('#inspect-idle-policy input[type=number]',el=>el.value='6');
  await page.$eval('#inspect-idle-policy .idle-policy-controls button',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-status').textContent.includes('6 idle hours'));
  assert.equal(seconds,21600);
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-badge').textContent==='Off');
  assert.equal(seconds,0);
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-badge').textContent==='On' && !document.querySelector('#inspect-idle-policy .idle-policy-switch input').disabled);
  assert.deepEqual(writes,[21600,0,21600]);
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/idle-policy-desktop.png'});
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-badge').textContent==='Off' && !document.querySelector('#inspect-idle-policy .idle-policy-switch input').disabled);
  await page.close();
 });
 await test('the same box details control fits mobile',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat');await page.evaluate(()=>localStorage.removeItem('vmbox.idleHours.builder'));
  await openDetails(page);
  assert.equal(await page.$eval('#inspect-idle-policy input[type=number]',el=>el.value),'6','the box retains its interval across browsers');
  await page.$eval('#inspect-idle-policy .idle-policy-switch input',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-idle-policy .idle-policy-badge').textContent==='On' && !document.querySelector('#inspect-idle-policy .idle-policy-switch input').disabled);
  assert.equal(seconds,21600);
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/idle-policy-mobile.png'});
  await page.close();
 });
}finally{await browser.close();await new Promise(done=>server.close(done))}
