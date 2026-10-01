import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const pages=[['chat','/chat','chat-providers'],['manage','/','manage-providers'],['grid','/grid','grid-providers'],['workspace','/boxes/builder','workspace-providers']];
const providers=[{provider:'shared-worker',name:'alpha'},{provider:'shared-worker',name:'beta'}];
const observedAt=new Date(Date.now()-2*60*1000).toISOString();

test('owner provider popup works on every topbar and isolates pool errors',async()=>{
 let hostRequests=0;
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://localhost'),path=url.pathname,credential=url.searchParams.get('providerCredential');
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/provider-credentials')return res.end(JSON.stringify(providers));
   if(path==='/v1/controller-defaults')return res.end(JSON.stringify({provider:'shared-worker',providerCredential:'alpha'}));
   if(path==='/v1/fleet/status')return res.end(JSON.stringify({slots:credential==='alpha'?[{id:'a1',state:'occupied'},{id:'a2',state:'free'}]:[{id:'b1',state:'free'}],occupiedSlots:credential==='alpha'?1:0,freeSlots:1}));
   if(path==='/v1/fleet/slots')return res.end(JSON.stringify({compute_box_slots:credential==='alpha'?4:2}));
   if(path==='/v1/fleet/host-resources'){
    hostRequests++;
    if(credential==='beta'){res.statusCode=503;return res.end('{"error":"Worker offline"}')}
    return res.end(JSON.stringify({memoryTotalBytes:8*1024**3,memoryAvailableBytes:1*1024**3,swapTotalBytes:4*1024**3,swapFreeBytes:Math.floor(.1*1024**3),diskTotalBytes:100*1024**3,diskUsedBytes:87*1024**3,cpuCores:4,cpuLoad1:1.5,cpuPercent:97,observedAt}));
   }
   res.statusCode=404;return res.end('{}');
  }
  const file=path==='/'?'index.html':path==='/chat'?'chat.html':path==='/grid'?'grid.html':path.startsWith('/boxes/')?'workspace.html':path.slice(1);
  if(file.includes('..')){res.statusCode=404;return res.end()}
  try{
   let data=await readFile(resolve(web,file));
   if(file.endsWith('.html'))data=Buffer.from(String(data).replace(/<script src="\/(?!workspace-nav\.js)[^"]+"(?: defer)?><\/script>/g,''));
   res.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');res.end(data);
  }catch{res.statusCode=404;res.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const captureDir=process.env.VMBOX_CAPTURE_DIR;if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const [name,path,id] of pages){
   const page=await browser.newPage();await page.setViewport({width:1440,height:900});
   await page.goto('http://127.0.0.1:'+server.address().port+path);
   await page.evaluate(({name,id})=>{
    const login=document.getElementById('login');if(login)login.hidden=true;
    window.providerNav=name==='chat'?window.VMBoxWorkspaceNav.initProviders(id):window.VMBoxWorkspaceNav.init({usageId:name==='manage'?'manage-usage':name==='grid'?'grid-usage':'workspace-usage',providersId:id});
   },{name,id});
   assert.equal(await page.$eval('#'+id,element=>element.hidden),true,name+' hides providers before owner check');
   await page.evaluate(()=>window.providerNav.setOwner(false));
   assert.equal(await page.$eval('#'+id,element=>element.hidden),true,name+' hides providers for non-owner');
   await page.evaluate(()=>window.providerNav.setOwner(true));
   const button=await page.$eval('#'+id,element=>({hidden:element.hidden,text:element.textContent,height:element.getBoundingClientRect().height,previous:element.previousElementSibling?.textContent}));
   assert.deepEqual(button,{hidden:false,text:'Providers',height:36,previous:'Usage'},name+' shows a text-only Providers control beside Usage');
   await page.click('#'+id);
   await page.waitForFunction(()=>document.querySelectorAll('.providers-dialog tbody tr').length===2&&document.querySelector('.providers-dialog .providers-row-error'));
   const state=await page.evaluate(()=>{
    const dialog=document.querySelector('.providers-dialog'),rows=[...dialog.querySelectorAll('tbody tr')];
    return {open:dialog.open,link:dialog.querySelector('.providers-open-link').getAttribute('href'),rows:rows.map(row=>({text:row.innerText,warning:!!row.querySelector('.is-warning'),danger:!!row.querySelector('.is-danger'),diskWarning:row.querySelector('[data-label="Disk"]')?.classList.contains('is-warning'),cpuDanger:row.querySelector('[data-label="CPU"]')?.classList.contains('is-danger'),error:!!row.querySelector('.providers-row-error')}))};
   });
   assert.equal(state.open,true);assert.equal(state.link,'/#providers');
   assert.match(state.rows[0].text,/alpha[\s\S]*Default[\s\S]*1 used · 1 free[\s\S]*of 4 configured[\s\S]*7\.0 GiB \/ 8\.0 GiB[\s\S]*87\.0 GiB \/ 100\.0 GiB[\s\S]*97% · load 1\.5 \/ 4 cores[\s\S]*2 min ago/);
   assert.equal(state.rows[0].warning,true,'RAM pressure at 87.5% is highlighted');
   assert.equal(state.rows[0].danger,true,'swap pressure at 97.5% uses danger colour');
   assert.equal(state.rows[0].diskWarning,true,'shared filesystem pressure at 87% uses warning colour');
   assert.equal(state.rows[0].cpuDanger,true,'CPU pressure at 97% uses danger colour');
   assert.match(state.rows[1].text,/beta[\s\S]*Resource usage unavailable: Worker offline/);
   assert.equal(state.rows[1].error,true,'failed host observation stays in its row');
   if(name==='chat'){
    const before=hostRequests;await page.click('.providers-refresh');await page.waitForFunction(()=>!document.querySelector('.providers-refresh').disabled);
    assert.ok(hostRequests>=before+2,'Refresh requests fresh resources for each shared pool');
    await page.keyboard.press('Escape');await page.waitForFunction(id=>!document.querySelector('.providers-dialog').open&&document.activeElement?.id===id,{},id);
    assert.equal(await page.evaluate(id=>document.activeElement?.id===id,id),true,'Escape restores focus');
    await page.click('#'+id);await page.waitForFunction(()=>document.querySelectorAll('.providers-dialog tbody tr').length===2);
    await page.mouse.click(2,2);await page.waitForFunction(id=>!document.querySelector('.providers-dialog').open&&document.activeElement?.id===id,{},id);
    assert.equal(await page.evaluate(id=>document.activeElement?.id===id,id),true,'backdrop restores focus');
   }
   await page.evaluate(()=>window.providerNav.setOwner(false));
   assert.equal(await page.$eval('#'+id,element=>element.hidden),true);
   await page.close();
  }
  for(const theme of ['light','dark'])for(const width of [390,1440]){
   const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
   await page.evaluate(()=>{document.getElementById('login').hidden=true;window.providerNav=window.VMBoxWorkspaceNav.initProviders('chat-providers');window.providerNav.setOwner(true)});
   await page.click('#chat-providers');await page.waitForFunction(()=>document.querySelectorAll('.providers-dialog tbody tr').length===2&&document.querySelector('.providers-dialog .providers-row-error'));
   const layout=await page.evaluate(()=>{const style=getComputedStyle(document.querySelector('.providers-table tr'));return {width:document.documentElement.scrollWidth,cards:style.display,columns:style.gridTemplateColumns.split(' ').length}});
   assert.ok(layout.width<=width,'popup fits '+width+'px');if(width===390){assert.equal(layout.cards,'grid','mobile rows become cards');assert.equal(layout.columns,3,'six mobile stats form a 3×2 grid')}
   if(captureDir)await page.screenshot({path:`${captureDir}/providers-${width}-${theme}.png`});
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
