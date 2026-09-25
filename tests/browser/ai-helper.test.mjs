import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const files=['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css'];
const assets=Object.fromEntries(await Promise.all(files.map(async name=>[name,await readFile(root+name)])));
const requests=[];
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://local').pathname;
 if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
 if(path.slice(1) in assets){res.setHeader('Content-Type',path.endsWith('.js')?'text/javascript':'text/css');return res.end(assets[path.slice(1)])}
 res.setHeader('Content-Type','application/json');
 if(path==='/v1/ai/rewrite'){
  let raw='';for await(const chunk of req)raw+=chunk;
  const body=JSON.parse(raw);requests.push(body);
  return res.end(JSON.stringify({text:body.text.replaceAll('Helo','Hello').replaceAll('wrold','world')}));
 }
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'codex',provider:'railway',volumeName:'v1'};
 const values={'/v1/whoami':{role:'owner'},'/v1/grid-boxes':[box],'/v1/logical-boxes':[box],'/v1/box-conversations':[],'/v1/profile-usage':[],'/v1/chat-commands':[],'/v1/tool-presets':[],'/v1/instruction-presets':{defaultName:'',presets:[]}};
 if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
 if(path.endsWith('/messages'))return res.end('[]');
 return res.end(JSON.stringify(values[path]??{}));
});
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
const base='http://127.0.0.1:'+server.address().port;
const screenshotDir=process.env.VMBOX_AI_SCREENSHOTS;
if(screenshotDir)await mkdir(screenshotDir,{recursive:true});
try{
 await test('chat wand rewrites only the draft and long press edits its prompt',async()=>{
  const page=await browser.newPage();await page.setViewport({width:1320,height:850});
  await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden && !!document.querySelector('#chat-composer .ai-wand'));
  await page.type('#chat-input','Helo wrold');
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello world');
  assert.equal(requests.at(-1).kind,'chat');
  assert.equal(await page.$eval('#chat-input',el=>el.value),'Hello world');
  await page.$eval('#chat-input',el=>{el.value='Helo again';el.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.hover('#chat-composer .ai-wand');await page.mouse.down();await new Promise(done=>setTimeout(done,650));await page.mouse.up();
  await page.waitForSelector('.ai-prompt-dialog[open]');
  await page.$eval('.ai-prompt-dialog textarea',el=>el.value='Keep the intent and fix typos.');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-wand-desktop.png'});
  await page.click('.ai-prompt-dialog button.primary');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello again');
  assert.match(requests.at(-1).instruction,/Keep the intent/);
  await page.close();
 });
 await test('mobile wand and Markdown preset share the same control',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  await page.goto(base+'/chat#box=builder');
  await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden && !!document.querySelector('#chat-composer .ai-wand'));
  await page.$eval('#chat-input',el=>{el.value='';el.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.type('#chat-input','Helo from mobile');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/chat-wand-mobile.png'});
  await page.click('#chat-composer .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#chat-input').value==='Hello from mobile');
  await page.evaluate(()=>document.querySelector('#presets-modal').hidden=false);
  await page.type('#preset-form textarea[name="markdown"]','# Helo skill');
  assert.equal(await page.$$('#preset-form .ai-wand').then(items=>items.length),1);
  await page.click('#preset-form .ai-wand');
  await page.waitForFunction(()=>document.querySelector('#preset-form textarea[name="markdown"]').value==='# Hello skill');
  assert.equal(requests.at(-1).kind,'markdown');
  if(screenshotDir)await page.screenshot({path:screenshotDir+'/markdown-wand-mobile.png'});
  await page.close();
 });
}finally{await browser.close();await new Promise(done=>server.close(done))}
