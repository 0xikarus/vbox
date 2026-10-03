import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const mime={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};

test('mobile menu scrolls natively and inline permission tools keep touch targets',async()=>{
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
  const errors=[];page.on('pageerror',error=>errors.push(error.message));page.on('console',message=>{if(/Content Security Policy/i.test(message.text()))errors.push(message.text())});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat`,{waitUntil:'networkidle0'});
  await page.waitForSelector('#chat-menu-sheet .sheet-scroll-body');
  await page.evaluate(()=>document.querySelector('#chat-menu-sheet').hidden=false);
  await page.waitForFunction(()=>document.querySelector('#chat-menu-sheet .sheet-scroll-frame').classList.contains('sheet-can-scroll'));
  const menu=async()=>page.$eval('#chat-menu-sheet .sheet-scroll-body',el=>({top:el.scrollTop,client:el.clientHeight,total:el.scrollHeight,headerTop:el.closest('.sheet-card').querySelector('header').getBoundingClientRect().top,indicator:getComputedStyle(el.parentElement.querySelector('.sheet-scroll-indicator')).display,above:el.parentElement.classList.contains('sheet-has-above'),below:el.parentElement.classList.contains('sheet-has-below')}));
  const atTop=await menu();assert.ok(atTop.total>atTop.client+20);assert.equal(atTop.indicator,'block');assert.equal(atTop.above,false);assert.equal(atTop.below,true);
  const headerY=await page.$eval('#chat-menu-sheet header',header=>Math.round(header.getBoundingClientRect().top+18));
  await page.touchscreen.touchStart(195,headerY);await page.touchscreen.touchMove(195,headerY+50);
  await page.waitForFunction(()=>new DOMMatrix(getComputedStyle(document.querySelector('#chat-menu-sheet .sheet-card')).transform).m42>15,{timeout:1000});
  await page.touchscreen.touchEnd();
  assert.equal(await page.$eval('#chat-menu-sheet',sheet=>sheet.hidden),false,'a short drag springs back without dismissing the sheet');
  await page.waitForFunction(()=>Math.abs(new DOMMatrix(getComputedStyle(document.querySelector('#chat-menu-sheet .sheet-card')).transform).m42)<.5);
  const steadyTop=(await menu()).headerTop;
  await page.mouse.move(200,600);await page.mouse.wheel({deltaY:280});await page.waitForFunction(()=>document.querySelector('#chat-menu-sheet .sheet-scroll-body').scrollTop>10);
  const afterWheel=await menu();assert.ok(Math.abs(afterWheel.headerTop-steadyTop)<1,'the header stays fixed while the body scrolls');assert.equal(afterWheel.above,true);
  await page.evaluate(()=>{const body=document.querySelector('#chat-menu-sheet .sheet-scroll-body');body.scrollTop=0});
  await page.touchscreen.touchStart(180,620);for(const y of [590,560,530,500,470])await page.touchscreen.touchMove(180,y);await page.touchscreen.touchEnd();
  assert.ok((await menu()).top>0,'touch panning moves the sheet body');
  await page.evaluate(()=>{const body=document.querySelector('#chat-menu-sheet .sheet-scroll-body');body.scrollTop=body.scrollHeight});
  await page.waitForFunction(()=>!document.querySelector('#chat-menu-sheet .sheet-scroll-frame').classList.contains('sheet-has-below'));
  assert.equal((await menu()).below,false);
  assert.ok(await page.$eval('#chat-menu-sheet .menu-logout',el=>el.getBoundingClientRect().bottom<=el.closest('.sheet-scroll-body').getBoundingClientRect().bottom+1));

  await page.evaluate(()=>{document.querySelector('#chat-menu-sheet').hidden=true;const editor=document.querySelector('#role-editor-inline');editor.hidden=false;document.body.append(editor)});
  const tool=await page.$eval('input[name=mcpTools]',input=>({width:input.getBoundingClientRect().width,height:input.getBoundingClientRect().height,before:input.checked,row:input.closest('label').getBoundingClientRect().height}));
  assert.ok(tool.width>=18&&tool.height>=18,'MCP choices keep a visible checkbox');
  assert.ok(tool.row>=44,'the whole tool row is a tap target');
  await page.$eval('input[name=mcpTools]',input=>input.closest('label').click());
  assert.equal(await page.$eval('input[name=mcpTools]',input=>input.checked),!tool.before,'tapping the row toggles its checkbox');
  await page.evaluate(()=>{
   document.querySelector('#role-editor-inline').hidden=true;
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
  await page.evaluate(()=>{document.querySelector('.model-picker-dialog').close();document.querySelector('#chat-group-dialog').showModal()});
  assert.equal(await page.$eval('#chat-group-dialog',dialog=>Math.round(dialog.getBoundingClientRect().bottom)),844,'group editor shares the mobile bottom-sheet surface');
  assert.equal(await page.$eval('#chat-group-dialog .sheet-scroll-body',body=>getComputedStyle(body).overflowY),'auto');
  await page.evaluate(()=>{document.querySelector('#chat-group-dialog').close();document.querySelector('#new-box-modal').hidden=false});
  await page.waitForSelector('#new-box-card .sheet-scroll-new-box #create-box');
  const boxSheet=await page.$eval('#new-box-card',card=>({preview:getComputedStyle(card.querySelector('#create-preview')).display,scrollables:[...card.querySelectorAll('*')].filter(el=>getComputedStyle(el).overflowY==='auto'&&el.scrollHeight>el.clientHeight+2).map(el=>el.id||el.className),summary:getComputedStyle(card.querySelector('#new-box-summary')).display}));
  assert.equal(boxSheet.preview,'none');assert.deepEqual(boxSheet.scrollables,['create-box'],'the form is the only scrollable region');assert.notEqual(boxSheet.summary,'none');
  await page.$eval('#create-box',form=>form.scrollTop=form.scrollHeight);
  assert.ok(await page.$eval('#create-box .nb-step:last-child',step=>step.getBoundingClientRect().bottom<=document.querySelector('#create-box').getBoundingClientRect().bottom+2),'the last New box field is reachable');
  assert.deepEqual(errors,[]);
  await page.close();
 }finally{await browser.close();server.close()}
});
