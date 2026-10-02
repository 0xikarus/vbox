import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const originalProviders=[{provider:'railway',name:'primary',config:{}},{provider:'railway',name:'backup',config:{}}];
let providers=[...originalProviders],deleted=0,planBlocked=false,deleteBody;
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost'),path=url.pathname;
 const json=value=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))};
 if(path==='/v1/provider-credentials/railway/primary/delete-plan')return json({isDefault:true,boxes:[],slots:[],cloudServers:0,canDelete:true,blockers:[]});
 if(path==='/v1/provider-credentials/railway/backup/delete-plan')return json({isDefault:false,boxes:planBlocked?[{id:'box-1',name:'Sleepy',state:'hibernated'}]:[],slots:[],cloudServers:0,canDelete:!planBlocked,blockers:planBlocked?['Delete or move every box on this provider first']:[]});
 if(path==='/v1/provider-credentials/railway/backup'&&req.method==='DELETE'){
  deleteBody=await new Promise(resolve=>{let data='';req.on('data',chunk=>data+=chunk);req.on('end',()=>resolve(JSON.parse(data)))});
  deleted++;providers=providers.filter(provider=>provider.name!=='backup');res.statusCode=204;return res.end();
 }
 if(path.startsWith('/v1/')){
  if(path==='/v1/browser-session')return json({});
  if(path==='/v1/capabilities')return json({providerEdits:true});
  if(path==='/v1/logical-boxes')return json([]);
  if(path==='/v1/instruction-presets')return json({defaultName:'',presets:[]});
  if(path==='/v1/provider-credentials')return json(providers);
  if(path==='/v1/controller-defaults')return json({provider:'railway',providerCredential:'primary'});
  if(path==='/v1/fleet/status')return json({slots:[],actualSlots:0,freeSlots:0,occupiedSlots:0});
  if(path==='/v1/fleet/slots')return json({compute_box_slots:0});
  if(path==='/v1/whoami')return json({accountId:'account-1',accountName:'Team',role:'owner'});
  if(path==='/v1/provider-schemas')return json({providers:{}});
  if(path==='/v1/agent-cli-versions')return json({claude:'latest',codex:'latest',opencode:'latest'});
  if(path.startsWith('/v1/agent-cli-versions/catalog/'))return json({latest:'1.0.0',versions:['1.0.0']});
  if(['/v1/notifications','/v1/login-profiles','/v1/tool-presets'].includes(path))return json([]);
  res.statusCode=404;return json({error:'unexpected endpoint'});
 }
 const file=resolve(web,path==='/'?'index.html':path.slice(1));
 if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
 try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2','.svg':'image/svg+xml','.png':'image/png'})[extname(file)]||'text/html');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
});
const base=()=>`http://127.0.0.1:${server.address().port}/`;
async function openManage(page){
 await page.goto(base()+'#providers');
 await page.waitForFunction(()=>document.querySelectorAll('#provider-list .providers-table tbody tr').length===2);
}
async function openDelete(page,index){
 const row=`#provider-list .providers-table tbody tr:nth-child(${index})`;
 await page.$eval(row+' .provider-actions-more>summary',node=>node.click());
 await page.$eval(row+' .provider-menu-delete',node=>node.click());
 await page.waitForSelector('.provider-delete-dialog[open]');
}
async function closeDelete(page){
 await page.click('.provider-delete-dialog [aria-label="Close delete dialog"]');
 await page.waitForFunction(()=>!document.querySelector('.provider-delete-dialog'));
}

test('Manage table deletion shows blockers, requires typed name, and refreshes after success',async()=>{
 providers=[...originalProviders];deleted=0;planBlocked=false;deleteBody=undefined;
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await openManage(page);
  await page.click('#manage-providers');await page.waitForFunction(()=>document.querySelectorAll('.providers-dialog tbody tr').length===2);
  assert.equal(await page.$$eval('.providers-dialog button',buttons=>buttons.filter(button=>/delete/i.test(button.textContent)).length),0,'popup has no delete button');
  await page.keyboard.press('Escape');await page.waitForFunction(()=>!document.querySelector('.providers-dialog').open);
  await openDelete(page,1);
  await page.type('.provider-delete-dialog input','primary');assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),true);
  await page.select('.provider-delete-dialog select','clear');assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),false);
  await closeDelete(page);
  planBlocked=true;await openDelete(page,2);
  assert.match(await page.$eval('.provider-delete-dialog',dialog=>dialog.textContent),/Sleepy · hibernated/);
  assert.equal(await page.$('.provider-delete-dialog .danger'),null);assert.equal(await page.$('.provider-delete-dialog input'),null);
  assert.match(await page.$eval('.provider-delete-dialog',dialog=>dialog.textContent),/1 box still uses/);
  await closeDelete(page);
  planBlocked=false;await openDelete(page,2);
  assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),true);
  await page.type('.provider-delete-dialog input','backup');assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),false);
  await page.click('.provider-delete-dialog .danger');
  await page.waitForFunction(()=>document.querySelectorAll('#provider-list .providers-table tbody tr').length===1);
  assert.equal(await page.$eval('#provider-list .providers-table tbody tr .providers-identity strong',node=>node.textContent),'primary');
  assert.equal(deleted,1);assert.deepEqual(deleteBody,{});
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

test('Manage table deletion dialog fits desktop and mobile in both themes',async()=>{
 planBlocked=true;providers=[...originalProviders];
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const captureDir=process.env.VMBOX_PROVIDER_DELETE_SCREENSHOTS;if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const theme of ['light','dark'])for(const width of [390,1440]){
   const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await openManage(page);await openDelete(page,2);
   const bounds=await page.$eval('.provider-delete-dialog',dialog=>{const card=dialog.getBoundingClientRect(),button=dialog.querySelector('.vb-sheet-footer button').getBoundingClientRect();return {top:card.top,bottom:card.bottom,left:card.left,right:card.right,buttonBottom:button.bottom}});
   assert.ok(bounds.top>=-1&&bounds.bottom<=page.viewport().height+1&&bounds.left>=-1&&bounds.right<=width+1&&bounds.buttonBottom<=bounds.bottom+1,`${theme} ${width}: ${JSON.stringify(bounds)}`);
   if(captureDir)await page.screenshot({path:`${captureDir}/provider-delete-${theme}-${width}.png`});
   await page.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
