import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const mime={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://localhost').pathname;
 if(path.startsWith('/v1/')){res.setHeader('Content-Type','application/json');if(path==='/v1/browser-session'){res.statusCode=204;return res.end()}if(path==='/v1/whoami')return res.end('{"role":"owner"}');if(path==='/v1/chat-sidebar-layout')return res.end('{"groups":[],"members":{},"mutes":{},"pins":[],"sections":{}}');return res.end('[]')}
 const asset=path==='/'?'index.html':path==='/chat'?'chat.html':path.slice(1);
 if(asset.includes('..')){res.statusCode=404;return res.end()}
 try{res.setHeader('Content-Type',mime[extname(asset)]||'application/octet-stream');res.end(await readFile(resolve(web,asset)))}catch{res.statusCode=404;res.end()}
});

test('desktop model picker keeps actions visible while the model list scrolls on Chat and Manage',async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const route of ['/chat','/'])for(const [width,height] of [[1375,885],[1024,640]]){
   const page=await browser.newPage();await page.setViewport({width,height});await page.goto(`http://127.0.0.1:${server.address().port}${route}`,{waitUntil:'networkidle0'});
   await page.evaluate(async()=>{const input=document.createElement('input');document.body.append(input);const picker=window.VMBoxModelPicker.create(input);picker.setApplication('codex');picker.setOptions(Array.from({length:18},(_,i)=>`model-${i}`));await picker.open()});
   const state=await page.$eval('.model-picker-dialog',dialog=>{const list=dialog.querySelector('.model-picker-list');const apply=dialog.querySelector('.model-picker-apply');const body=dialog.querySelector('.model-picker-body');list.scrollTop=100;return {dialogBottom:dialog.getBoundingClientRect().bottom,applyBottom:apply.getBoundingClientRect().bottom,bodyHeight:body.clientHeight,listHeight:list.clientHeight,listTotal:list.scrollHeight,listTop:list.scrollTop,display:getComputedStyle(dialog).display,flex:getComputedStyle(body).flex,minHeight:getComputedStyle(body).minHeight,dialogHeight:dialog.clientHeight,headerHeight:dialog.querySelector('.model-picker-header').clientHeight}});
   assert.ok(state.dialogBottom<=height+1,`${route} ${width}: dialog inside viewport ${JSON.stringify(state)}`);
   assert.ok(state.applyBottom<=state.dialogBottom+1,`${route} ${width}: apply inside dialog ${JSON.stringify(state)}`);
   assert.ok(state.bodyHeight>0&&state.listTotal>state.listHeight&&state.listTop>0,`${route} ${width}: list scrolls ${JSON.stringify(state)}`);
   await page.click('.model-picker-option');await page.click('.model-picker-apply');
   assert.equal(await page.$eval('.model-picker-dialog',dialog=>dialog.open),false);
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

test('Chat and Manage sheets keep their action buttons reachable at desktop and mobile sizes',async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const route of ['/chat','/'])for(const [width,height] of [[1440,900],[1280,720],[1024,640],[390,844]]){
   const page=await browser.newPage();await page.setViewport({width,height,isMobile:width<641,hasTouch:width<641});await page.goto(`http://127.0.0.1:${server.address().port}${route}`,{waitUntil:'networkidle0'});
   const results=await page.evaluate(()=>{
    const roots=[...document.querySelectorAll('.sheet:has(.sheet-card),.modal:has(.card),#new-box-modal,#delete-box-modal,#chat-group-dialog,.vb-sheet-dialog,#takeover,#media-viewer,#image-annotation')];
    const out=[];
    for(const root of roots){
     if(root.matches('dialog'))root.showModal();else root.hidden=false;
     const card=root.matches('dialog')?root:root.querySelector('[role=dialog]');
     if(!card){if(root.matches('dialog'))root.close();else root.hidden=true;continue}
     const buttons=[...card.querySelectorAll('button.primary,button[type=submit]')].filter(el=>!el.hidden&&getComputedStyle(el).display!=='none');
     const action=buttons.at(-1);
     const scroll=card.querySelector('.sheet-scroll-body')||card;
     scroll.scrollTop=scroll.scrollHeight;
     const box=card.getBoundingClientRect(),button=action?.getBoundingClientRect();
     out.push({name:root.id||card.className,bottom:box.bottom,top:box.top,buttonBottom:button?.bottom,scroll:scroll.scrollHeight-scroll.clientHeight,button:action?.textContent.trim()});
     if(root.matches('dialog'))root.close();else root.hidden=true;
    }
    return out;
   });
   assert.ok(results.length>=(route==='/chat'?13:4),`${route} ${width}: expected every sheet and dialog`);
   for(const item of results){assert.ok(item.bottom<=height+2&&item.top>=-2,`${route} ${width} ${item.name}: dialog in viewport ${JSON.stringify(item)}`);if(item.buttonBottom!==undefined)assert.ok(item.buttonBottom<=item.bottom+2,`${route} ${width} ${item.name}: action reachable ${JSON.stringify(item)}`)}
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
