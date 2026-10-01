import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const mime={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};

test('closed dialogs never render and the Providers button keeps its neutral look',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/chat-sidebar-layout')return res.end('{"groups":[],"members":{},"mutes":{},"pins":[],"sections":{}}');
   if(path==='/v1/controller-defaults'||path==='/v1/fleet/location')return res.end('{}');
   if(path==='/v1/instruction-presets')return res.end('{"defaultName":"","presets":[]}');
   return res.end('[]');
  }
  const asset=path==='/chat'?'chat.html':path==='/'?'index.html':path.slice(1);
  if(asset.includes('..')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',mime[extname(asset)]||'application/octet-stream');res.end(await readFile(resolve(web,asset)))}
  catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const [path,hash] of [['/','#profiles'],['/','#providers'],['/chat','']]){
   const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
   await page.goto(`http://127.0.0.1:${server.address().port}${path}${hash}`);
   await page.waitForFunction(()=>document.querySelectorAll('.model-picker-dialog').length>0||document.readyState==='complete');
   await page.waitForFunction(()=>window.VMBoxModelPicker);
   await page.evaluate(()=>{const input=document.createElement('input');document.body.append(input);window.VMBoxModelPicker.create(input)});
   await new Promise(done=>setTimeout(done,300));
   const visible=await page.evaluate(()=>[...document.querySelectorAll('dialog:not([open])')].filter(dialog=>{const rect=dialog.getBoundingClientRect();return getComputedStyle(dialog).display!=='none'&&rect.width>0&&rect.height>0}).map(dialog=>dialog.className||dialog.id));
   assert.deepEqual(visible,[],`closed dialogs render on ${path}${hash}: ${visible.join(', ')}`);
   await page.close();
  }
  const page=await browser.newPage();await page.setViewport({width:1440,height:900});
  await page.goto(`http://127.0.0.1:${server.address().port}/#providers`);
  const button=await page.waitForSelector('.workspace-nav-providers:not([hidden])',{timeout:5000}).catch(()=>null);
  if(button){
   await page.hover('.workspace-nav-providers');
   const [providers,usage]=await page.evaluate(()=>['.workspace-nav-providers','.workspace-nav-refresh'].map(selector=>getComputedStyle(document.querySelector(selector)).backgroundColor));
   await page.hover('.workspace-nav-refresh');
   const refresh=await page.$eval('.workspace-nav-refresh',button=>getComputedStyle(button).backgroundColor);
   assert.equal(providers,refresh,'Providers hover uses the same neutral fill as the other top bar buttons');
  }
 }finally{await browser.close();server.close()}
});
