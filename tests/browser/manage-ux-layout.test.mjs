import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const boxes=[{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex'}];
const providers=[{provider:'railway',name:'primary',config:{}},{provider:'shared-worker',name:'local',config:{host:'worker'}},{provider:'shared-worker',name:'broken',config:{}}];
test('Manage permission, version, and provider layouts retain their content',async()=>{
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://local'),path=url.pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');const json=value=>res.end(JSON.stringify(value));
   if(path==='/v1/whoami')return json({role:'owner',accountId:'acct'});
   if(path==='/v1/capabilities')return json({providerEdits:true});
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return json(boxes);
   if(path==='/v1/instruction-presets')return json({defaultName:'',presets:[]});
   if(path==='/v1/provider-credentials')return json(providers);
   if(path==='/v1/controller-defaults')return json({provider:'railway',providerCredential:'primary'});
   if(path==='/v1/fleet/status')return json({slots:[{id:'s1',state:'free'}],actualSlots:1,freeSlots:1,occupiedSlots:0});
   if(path==='/v1/fleet/slots')return json({compute_box_slots:2});
   if(path==='/v1/fleet/host-resources'){
    if(url.searchParams.get('providerCredential')==='broken'){res.statusCode=502;return json({error:'HTTP 502'})}
    return json({memoryTotalBytes:8*1024**3,memoryAvailableBytes:1*1024**3,swapTotalBytes:4*1024**3,swapFreeBytes:2*1024**3,cpuCores:0,cpuPercent:0,cpuLoad1:0,observedAt:new Date(Date.now()-120000).toISOString()});
   }
   if(path==='/v1/provider-credentials/shared-worker/local/delete-plan')return json({boxes:[],slots:[],cloudServers:0,blockers:[],canDelete:true,isDefault:false});
   if(path==='/v1/agent-cli-versions')return json({claude:'latest',codex:'latest',opencode:'latest'});
   if(path.startsWith('/v1/agent-cli-versions/catalog/'))return json({latest:'1.0.0',versions:['1.0.0']});
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return json({})}
   if(['/v1/login-profiles','/v1/notifications','/v1/tool-presets'].includes(path))return json([]);
   return json({});
  }
  const file=resolve(web,path==='/'?'index.html':path.slice(1));if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.html':'text/html'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base=`http://127.0.0.1:${server.address().port}/`;
  const phone=await browser.newPage();await phone.setViewport({width:390,height:844,isMobile:true,hasTouch:true});await phone.goto(base+'#roles');await phone.waitForSelector('#roles .role-assignment-card');
  const name=await phone.$eval('#roles .role-assignment-identity strong',node=>({text:node.textContent,width:node.clientWidth,scroll:node.scrollWidth}));assert.equal(name.text,'reviewer');assert(name.scroll<=name.width+1,JSON.stringify(name));await phone.close();
  const desktop=await browser.newPage();await desktop.setViewport({width:1440,height:900});await desktop.goto(base+'#roles');await desktop.waitForSelector('#roles .role-assignment-toggle');await desktop.$eval('#roles .role-assignment-toggle',node=>node.click());
  const color=await desktop.$eval('#role-editor-inline .ip-heading',node=>getComputedStyle(node).color);assert.notEqual(color,'rgb(255, 255, 255)');
  assert.equal(await desktop.$eval('#roles .role-assignment-toggle',node=>node.textContent),'Hide permissions');
  await desktop.goto(base+'#profiles');await desktop.waitForSelector('#agent-cli-versions select');await desktop.waitForFunction(()=>document.querySelector('#agent-cli-versions select').selectedOptions[0]?.textContent.includes('1.0.0'));
  assert(await desktop.$eval('#agent-cli-versions select',node=>node.getBoundingClientRect().width)>=350);
  await desktop.goto(base+'#providers');await desktop.waitForFunction(()=>document.querySelectorAll('#provider-list .providers-table tbody tr').length===3&&document.querySelector('#provider-list .providers-row-error'));
  assert.deepEqual(await desktop.$$eval('#provider-list .providers-table th',nodes=>nodes.map(node=>node.textContent)),['Provider','Workers','Slots','RAM','Swap','Disk','CPU','Observed','Actions']);
  const local=await desktop.$eval('#provider-list .providers-table tbody tr:nth-child(2)',node=>({text:node.innerText,cpu:node.querySelector('[data-label="CPU"]')?.textContent,disk:node.querySelector('[data-label="Disk"]')?.textContent,cpuTitle:node.querySelector('[data-label="CPU"]')?.title,diskTitle:node.querySelector('[data-label="Disk"]')?.title,actions:[...node.querySelectorAll('.provider-actions>button')].map(button=>button.textContent)}));
  assert.match(local.text,/0 used · 1 free[\s\S]*of 2 configured[\s\S]*7\.0 GiB \/ 8\.0 GiB/);
  assert.deepEqual([local.cpu,local.disk,local.cpuTitle,local.diskTitle,local.actions],['–','–','Worker update needed','Worker update needed',['Manage']]);
  assert.match(await desktop.$eval('#provider-list .providers-table tbody tr:nth-child(3) .providers-row-error',node=>node.textContent),/Resource usage unavailable: HTTP 502/);
  await desktop.$eval('#provider-list .providers-table tbody tr:nth-child(2) .provider-actions>button',node=>node.click());
  await desktop.waitForSelector('#provider-list .provider-detail-row .provider-panel');
  assert.deepEqual(await desktop.$$eval('.provider-panel-actions button',nodes=>nodes.map(node=>node.textContent)),['Validate','Use as default','Refresh usage','Delete…']);
  assert.match(await desktop.$eval('.provider-panel .provider-other-settings',node=>{node.open=true;return node.innerText}),/host[\s\S]*worker/);
  await desktop.$eval('.provider-panel .provider-menu-delete',node=>node.click());
  await desktop.waitForSelector('.provider-delete-dialog[open]');
  assert.match(await desktop.$eval('.provider-delete-dialog',node=>node.innerText),/Delete local/);
  await desktop.$eval('.provider-delete-dialog button[aria-label="Close delete dialog"]',node=>node.click());
  const captureDir=process.env.VMBOX_CAPTURE_DIR;if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const theme of ['light','dark'])for(const width of [1440,390]){
   const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   if(captureDir){await page.goto(base+'#roles');await page.waitForSelector('#roles .role-assignment-toggle');await page.click('#roles .role-assignment-toggle');await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#role-editor-status').textContent==='');await page.screenshot({path:`${captureDir}/manage-permissions-${width}-${theme}.png`,fullPage:true})}
   await page.goto(base+'#providers');await page.waitForFunction(()=>document.querySelectorAll('#provider-list .providers-table tbody tr').length===3&&document.querySelector('#provider-list .providers-row-error'));
   const layout=await page.$eval('#provider-list .providers-table tbody tr',row=>({display:getComputedStyle(row).display,width:document.documentElement.scrollWidth}));
   assert.equal(layout.width<=width,true,'page fits '+width+'px');if(width===390)assert.equal(layout.display,'grid','mobile rows become cards');
   if(captureDir)await page.screenshot({path:`${captureDir}/providers-page-${width}-${theme}.png`,fullPage:true});await page.close();
  }
  await desktop.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
