import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const profiles=[{application:'claude',name:'work',snapshot:{windows:[{name:'session',usedPercent:39},{name:'weekly_all',usedPercent:90}]}}];
const pages=[['chat','/chat','usage-toggle'],['manage','/','manage-usage'],['grid','/grid','grid-usage'],['workspace','/boxes/builder','workspace-usage']];

test('all workspace topbars show the lowest all-profile usage and open the sheet',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname;
  if(path==='/v1/profile-usage'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({profiles}))}
  if(path.startsWith('/v1/')){res.setHeader('Content-Type','application/json');return res.end('{}')}
  const file=path==='/'?'index.html':path==='/chat'?'chat.html':path==='/grid'?'grid.html':path.startsWith('/boxes/')?'workspace.html':path.slice(1);
  if(file.includes('..')){res.statusCode=404;return res.end()}
  try{
   let data=await readFile(resolve(web,file));
   // Keep the real page markup and styles; initialize only shared navigation.
   if(file.endsWith('.html'))data=Buffer.from(String(data).replace(/<script src="\/(?!workspace-nav\.js)[^"]+"(?: defer)?><\/script>/g,''));
   res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');res.setHeader('Content-Security-Policy',"default-src 'self'; img-src 'self' data: blob:; script-src 'self'; style-src 'self' 'unsafe-inline'");res.end(data);
  }catch{res.statusCode=404;res.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [390,1440])for(const [name,path,id] of pages){
   const page=await browser.newPage(),errors=[];page.on('pageerror',error=>errors.push(error.message));page.on('console',message=>{if(/Content Security Policy/i.test(message.text()))errors.push(message.text())});
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.goto('http://127.0.0.1:'+server.address().port+path,{waitUntil:'load'});
   await page.evaluate(()=>{const login=document.getElementById('login');if(login)login.hidden=true});
   await page.evaluate(({name,id,profiles})=>{
    const button=document.getElementById(id);
    if(name==='chat'){window.VMBoxWorkspaceNav.updateUsagePill(button,profiles,true);button.onclick=()=>{document.getElementById('usage-modal').hidden=false}}
    else window.VMBoxWorkspaceNav.init({menuId:name==='manage'?'manage-menu':name==='grid'?'grid-menu':undefined,panelId:name==='manage'?'manage-menu-panel':name==='grid'?'grid-menu-panel':undefined,usageId:id}).setOwner(true);
   },{name,id,profiles});
   await page.waitForFunction(id=>!document.getElementById(id).hidden,{},id);
   const state=await page.$eval('#'+id,button=>({text:button.querySelector('.workspace-usage-percent')?.textContent,ring:button.querySelector('.workspace-usage-ring')?.getBoundingClientRect().width,next:button.nextElementSibling?.classList.contains('workspace-nav-refresh'),height:button.getBoundingClientRect().height,scrollWidth:document.documentElement.scrollWidth}));
   assert.equal(state.text,'10%',name+' shows the lowest remaining window');assert.equal(state.ring,16);assert.equal(state.next,true,name+' keeps usage before the rightmost action');assert.equal(state.height,36);assert.ok(state.scrollWidth<=width,name+' fits the viewport');
   if(name==='chat')assert.equal(await page.evaluate(()=>!!document.querySelector('#chat-menu-sheet #usage-toggle')),false);
   if(name==='manage')assert.equal(await page.evaluate(()=>!!document.querySelector('#manage-menu-panel #manage-usage')),false);
   await page.click('#'+id);
   if(name==='chat')assert.equal(await page.$eval('#usage-modal',sheet=>sheet.hidden),false);
   else await page.waitForFunction(()=>document.querySelector('.workspace-usage-dialog')?.open);
   assert.deepEqual(errors,[],name+' has no page or CSP errors');
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
