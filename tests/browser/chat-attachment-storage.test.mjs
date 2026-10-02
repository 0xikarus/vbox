import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assetNames=['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','run-budget-policy.js','idle-policy.css'];
const assets=Object.fromEntries(await Promise.all(assetNames.map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

test('box details reports attachment use and clears delivered media after confirmation',async()=>{
 const box={id:'builder',name:'Builder',state:'hibernated',defaultAgent:'codex'};
 const message={id:'message-1',taskId:'task-1',direction:'user',text:'Keep this text',state:'delivered',createdAt:new Date().toISOString(),updatedAt:new Date().toISOString(),images:[{id:'image-1',number:1,mediaType:'image/png'}]};
 let cleared=false,incomplete=false,deleteBody=null;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify([{...message,images:cleared?[]:message.images}]));
  if(path==='/v1/logical-boxes/builder/attachment-storage'){
   if(req.method==='DELETE'){
    const chunks=[];for await(const chunk of req)chunks.push(chunk);
    deleteBody=JSON.parse(Buffer.concat(chunks));cleared=true;
    return res.end(JSON.stringify({freedBytes:5242880,removedReferences:1}));
   }
   if(incomplete)return res.end(JSON.stringify({boxBytes:null,boxCount:undefined,accountBytes:0,unusedBytes:undefined}));
   return res.end(JSON.stringify({boxBytes:cleared?0:5242880,boxCount:cleared?0:1,clearableCount:cleared?0:1,accountBytes:cleared?1048576:6291456,unusedBytes:1048576,unusedCount:2,limitBytes:1073741824}));
  }
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.evaluateOnNewDocument(()=>{window.confirm=()=>{window.__attachmentClearConfirmed=true;return true}});
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForFunction(()=>document.querySelector('#chat-header-name')?.textContent==='Builder'&&document.querySelector('#chat-loading').hidden);
  await page.$eval('#chat-info',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await page.$eval('[data-ip-row="attachments"]',button=>button.click());
  await page.waitForFunction(()=>!document.querySelector('[data-ip-page="attachments"]').hidden);
  await page.waitForFunction(()=>document.querySelector('#inspect-attachment-rows')?.textContent.includes('5.0 MiB'));
  const captureDir='/data/workspace/captures/details-redesign';await mkdir(captureDir,{recursive:true});
  await page.setViewport({width:390,height:844});await page.screenshot({path:captureDir+'/attachments-390.png'});
  await page.setViewport({width:1440,height:900});await page.screenshot({path:captureDir+'/attachments-1440.png'});
  assert.match(await page.$eval('#inspect-attachment-rows',element=>element.textContent),/5\.0 MiB.*6\.0 MiB \/ 1\.00 GiB.*Unused uploads.*1\.0 MiB/);
  assert.doesNotMatch(await page.$eval('[data-ip-row="attachments"]',element=>element.textContent),/1 files/);
  assert.match(await page.$eval('[data-ip-row="attachments"] .ip-row-value',element=>element.textContent),/^1 file$/);
  assert.doesNotMatch(await page.$eval('#inspect-attachment-rows',element=>element.textContent),/1 files/);
  await page.$eval('#inspect-prototype-back',e=>e.click());
  await page.$eval('[data-ip-row="clear-attachments"]',e=>e.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-attachment-status')?.textContent.includes('freed 5.0 MiB'));
  assert.equal(await page.evaluate(()=>window.__attachmentClearConfirmed),true);
  assert.deepEqual(deleteBody,{confirmation:'Builder'});
  assert.match(await page.$eval('#chat-messages',element=>element.textContent),/Keep this text/);
  assert.match(await page.$eval('#inspect-attachment-rows',element=>element.textContent),/0 B/);
  incomplete=true;
  await page.click('#inspect-close');
  await page.waitForFunction(()=>document.querySelector('#inspect').hidden);
  await page.click('#chat-info');
  await page.waitForFunction(()=>!document.querySelector('#inspect').hidden);
  await page.$eval('[data-ip-row="attachments"]',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('#inspect-attachment-rows')?.textContent.includes('No data'));
  const unavailable=await page.$eval('#inspect-attachment-rows',element=>element.textContent);
  assert.equal((unavailable.match(/No data/g)||[]).length,3,'incomplete counts and sizes show a consistent fallback');
  assert.doesNotMatch(unavailable,/undefined|null|NaN/);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
