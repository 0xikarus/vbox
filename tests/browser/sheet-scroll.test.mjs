import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const mime={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};

test('mobile menu and Permissions sheets scroll natively with a visible position cue',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/browser-session'){res.statusCode=204;return res.end()}
   if(path==='/v1/whoami')return res.end('{"role":"owner"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end('[]');
   if(path==='/v1/chat-sidebar-layout')return res.end('{"groups":[],"members":{},"mutes":{},"pins":[],"sections":{}}');
   return res.end('[]');
  }
  const asset=path==='/chat'?'chat.html':path.slice(1);
  if(asset.includes('..')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',mime[extname(asset)]||'application/octet-stream');res.setHeader('Content-Security-Policy',"default-src 'self'; img-src 'self' data: blob:; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'");res.end(await readFile(resolve(web,asset)))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.goto(`http://127.0.0.1:${server.address().port}/chat`,{waitUntil:'networkidle0'});
  await page.waitForSelector('#chat-menu-sheet .sheet-scroll-body');
  await page.evaluate(()=>document.querySelector('#chat-menu-sheet').hidden=false);
  await page.waitForFunction(()=>document.querySelector('#chat-menu-sheet .sheet-scroll-frame').classList.contains('sheet-can-scroll'));
  const menu=async()=>page.$eval('#chat-menu-sheet .sheet-scroll-body',el=>({top:el.scrollTop,client:el.clientHeight,total:el.scrollHeight,headerTop:el.closest('.sheet-card').querySelector('header').getBoundingClientRect().top,indicator:getComputedStyle(el.parentElement.querySelector('.sheet-scroll-indicator')).display,above:el.parentElement.classList.contains('sheet-has-above'),below:el.parentElement.classList.contains('sheet-has-below')}));
  const atTop=await menu();assert.ok(atTop.total>atTop.client+20);assert.equal(atTop.indicator,'block');assert.equal(atTop.above,false);assert.equal(atTop.below,true);
  await page.mouse.move(200,600);await page.mouse.wheel({deltaY:280});await page.waitForFunction(()=>document.querySelector('#chat-menu-sheet .sheet-scroll-body').scrollTop>10);
  const afterWheel=await menu();assert.equal(afterWheel.headerTop,atTop.headerTop);assert.equal(afterWheel.above,true);
  await page.evaluate(()=>{const body=document.querySelector('#chat-menu-sheet .sheet-scroll-body');body.scrollTop=0});
  await page.touchscreen.touchStart(180,620);for(const y of [590,560,530,500,470])await page.touchscreen.touchMove(180,y);await page.touchscreen.touchEnd();
  assert.ok((await menu()).top>0,'touch panning moves the sheet body');
  await page.evaluate(()=>{const body=document.querySelector('#chat-menu-sheet .sheet-scroll-body');body.scrollTop=body.scrollHeight});
  await page.waitForFunction(()=>!document.querySelector('#chat-menu-sheet .sheet-scroll-frame').classList.contains('sheet-has-below'));
  assert.equal((await menu()).below,false);
  assert.ok(await page.$eval('#chat-menu-sheet .menu-logout',el=>el.getBoundingClientRect().bottom<=el.closest('.sheet-scroll-body').getBoundingClientRect().bottom+1));

  await page.evaluate(()=>{document.querySelector('#chat-menu-sheet').hidden=true;document.querySelector('#role-editor-modal').hidden=false;document.querySelector('.mcp-tool-options').open=true});
  const permissions=async()=>page.$eval('#role-editor-modal .sheet-scroll-body',el=>({top:el.scrollTop,client:el.clientHeight,total:el.scrollHeight,indicator:getComputedStyle(el.parentElement.querySelector('.sheet-scroll-indicator')).display,below:el.parentElement.classList.contains('sheet-has-below')}));
  await page.waitForFunction(()=>document.querySelector('#role-editor-modal .sheet-scroll-frame').classList.contains('sheet-can-scroll'));
  const pTop=await permissions();assert.ok(pTop.total>pTop.client+100);assert.equal(pTop.indicator,'block');assert.equal(pTop.below,true);
  await page.mouse.move(190,580);await page.mouse.wheel({deltaY:430});await page.waitForFunction(()=>document.querySelector('#role-editor-modal .sheet-scroll-body').scrollTop>50);
  await page.evaluate(()=>{const body=document.querySelector('#role-editor-modal .sheet-scroll-body');body.scrollTop=body.scrollHeight});
  await page.waitForFunction(()=>!document.querySelector('#role-editor-modal .sheet-scroll-frame').classList.contains('sheet-has-below'));
  assert.ok(await page.$eval('#role-editor-status',el=>el.getBoundingClientRect().bottom<=el.closest('.sheet-scroll-body').getBoundingClientRect().bottom+1));
  await page.evaluate(()=>{document.querySelector('.mcp-tool-options').open=false});
  await page.waitForFunction(()=>!document.querySelector('#role-editor-modal .sheet-scroll-frame').classList.contains('sheet-can-scroll'));
  assert.equal((await permissions()).indicator,'none');
  await page.evaluate(()=>{
   document.querySelector('#role-editor-modal').hidden=true;
   const input=document.createElement('textarea');document.body.append(input);
   const helper=window.VMBoxAIHelper.attach({input});
   helper.button.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true}));
  });
  await page.waitForSelector('.ai-prompt-dialog[open]');
  const prompt=await page.$eval('.ai-prompt-dialog',dialog=>({bottom:dialog.getBoundingClientRect().bottom,width:dialog.getBoundingClientRect().width,primary:getComputedStyle(dialog.querySelector('.primary')).backgroundColor,ink:getComputedStyle(document.body).color,body:!!dialog.querySelector('.sheet-scroll-body')}));
  assert.equal(prompt.bottom,844);assert.equal(prompt.width,390);assert.equal(prompt.body,true);
  assert.equal(prompt.primary,prompt.ink,'primary uses the shared ink token');
  await page.evaluate(()=>{
   document.querySelector('.ai-prompt-dialog').close();
   const input=document.createElement('input');document.body.append(input);
   window.VMBoxModelPicker.create(input).open();
  });
  await page.waitForSelector('.model-picker-dialog[open]');
  assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('model-picker-search')),false,'touch model picker does not open the keyboard');
  assert.equal(await page.$eval('.model-picker-dialog',dialog=>Math.round(dialog.getBoundingClientRect().bottom)),844);
  assert.deepEqual(errors,[]);
  await page.close();
 }finally{await browser.close();server.close()}
});
