import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const names=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'];
const assets=Object.fromEntries(await Promise.all(names.map(async name=>['/'+name,await readFile('internal/controller/web/'+name)])));
async function assertTooltipInViewport(page){
 const rect=await page.$eval('#mascot-mood-tooltip',el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,width:innerWidth,height:innerHeight}});
 assert.ok(rect.left>=0&&rect.top>=0&&rect.right<=rect.width&&rect.bottom<=rect.height,'tooltip stays within the viewport');
}

test('mascot freshness tooltip updates, expires, and leaves mobile row navigation intact',async()=>{
 const boxes=[{id:'builder',name:'Builder',state:'running',defaultAgent:'codex'},{id:'quiet',name:'Quiet',state:'running',defaultAgent:'claude'}];
 let observation=new Date(Date.now()-3000).toISOString();
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['/chat.html'])}
  if(assets[path]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets'||path==='/v1/box-conversations')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path==='/v1/logical-boxes/builder/messages'){
   if(observation){res.setHeader('X-Vmbox-Mascot-Mood','idle');res.setHeader('X-Vmbox-Mascot-Activity','working');res.setHeader('X-Vmbox-Mascot-Observed-At',observation)}
   return res.end('[]');
  }
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 const captureDir=process.env.VMBOX_CAPTURE_DIR;
 if(captureDir)await mkdir(captureDir,{recursive:true});
 try{
  for(const width of [1440,390])for(const theme of ['light','dark']){
   observation=new Date(Date.now()-3000).toISOString();
   const mobile=width===390,page=await browser.newPage();
   await page.setViewport({width,height:mobile?844:900,deviceScaleFactor:1,isMobile:mobile,hasTouch:mobile});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
   await page.waitForFunction(()=>document.querySelector('#chat-header-avatar .avatar-mascot')?.getAttribute('aria-label')?.includes('mood updated recently'));
   const header='#chat-header-avatar .avatar-mascot',row='[data-box-id="builder"] .avatar-mascot',quiet='[data-box-id="quiet"] .avatar-mascot';
   assert.match(await page.$eval(quiet,el=>el.getAttribute('aria-label')),/^Idle · no fresh observation$/);
   assert.match(await page.$eval(row,el=>el.getAttribute('aria-label')),/^Working · mood updated recently$/);
   if(mobile){
    await page.$eval('#chat-back',el=>el.click());
    await page.waitForFunction(()=>!document.querySelector('#chat-app').classList.contains('in-chat'));
    assert.equal(await page.$eval(row,el=>{el.click();return document.querySelector('#mascot-mood-tooltip').hidden}),false,'mascot tap shows the short-lived tooltip');
    assert.equal(await page.$eval('#chat-app',el=>el.classList.contains('in-chat')),false,'mascot tap must not open chat');
   }else{
    await page.hover(row);
    await page.waitForSelector('#mascot-mood-tooltip:not([hidden])');
   }
   await assertTooltipInViewport(page);
   const first=Number((await page.$eval('#mascot-mood-tooltip',el=>el.textContent)).match(/updated (\d+) s ago/)?.[1]);
   assert.ok(Number.isFinite(first),'fresh tooltip includes seconds');
   await page.waitForFunction(previous=>Number(document.querySelector('#mascot-mood-tooltip')?.textContent.match(/updated (\d+) s ago/)?.[1])>previous,{},first);
   const second=Number((await page.$eval('#mascot-mood-tooltip',el=>el.textContent)).match(/updated (\d+) s ago/)?.[1]);
   assert.ok(second>first,'seconds tick while tooltip is open');
   if(captureDir)await page.screenshot({path:`${captureDir}/mascot-${width}-${theme}-list.png`});
   if(mobile){await page.$eval('[data-box-id="builder"] .chat-meta',el=>el.click())}
   else await page.$eval('#chat-header-avatar .avatar-mascot',el=>el.scrollIntoView());
   await page.waitForFunction(()=>document.querySelector('#chat-app').classList.contains('in-chat'));
   await page.evaluate(()=>document.querySelector('#mascot-mood-tooltip').hidden=true);
   if(mobile)await page.$eval(header,el=>el.click());
   else await page.hover(header);
   await new Promise(resolve=>setTimeout(resolve,400));
   assert.equal(await page.$eval('#mascot-mood-tooltip',el=>el.hidden),true,'header mascot has no hover or tap tooltip');
   if(captureDir)await page.screenshot({path:`${captureDir}/mascot-${width}-${theme}-header.png`});
   await page.$eval('#chat-info',el=>el.click());
   await page.waitForSelector('#inspect:not([hidden]) .inspect-hero-mascot');
   const detail='#inspect .inspect-hero-mascot';
   if(mobile)assert.equal(await page.$eval(detail,el=>{el.click();return document.querySelector('#mascot-mood-tooltip').hidden}),false,'Details mascot tap shows tooltip');
   else{await page.hover(detail);await page.waitForSelector('#mascot-mood-tooltip:not([hidden])')}
   await assertTooltipInViewport(page);
   if(captureDir)await page.screenshot({path:`${captureDir}/mascot-${width}-${theme}-details.png`});
   observation=new Date(Date.now()-42000).toISOString();
   await page.$eval('#refresh',el=>el.click());
   await page.waitForFunction(()=>document.querySelector('#chat-header-avatar .avatar-mascot')?.getAttribute('aria-label')==='Working · no fresh observation');
   assert.equal(await page.$eval(header,el=>el.getAttribute('aria-label')),'Working · no fresh observation','expired header must not keep a stale time in the label');
   observation='';
   await page.$eval('#refresh',el=>el.click());
   await page.waitForFunction(()=>document.querySelector('#chat-header-avatar .avatar-mascot')?.getAttribute('aria-label')==='Idle · no fresh observation');
   assert.equal(await page.$eval(row,el=>el.getAttribute('aria-label')),'Idle · no fresh observation','missing next header clears old row timestamp and mood');
   assert.equal(await page.$eval(detail,el=>el.getAttribute('aria-label')),'Idle · no fresh observation');
   assert.equal(await page.$eval(detail,el=>el.getAttribute('aria-label')),'Idle · no fresh observation');
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
