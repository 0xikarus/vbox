import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const providers=[{provider:'railway',name:'primary',config:{}},{provider:'railway',name:'backup',config:{}}];
let deleted=0,planBlocked=false,deleteBody;
const server=http.createServer(async(req,res)=>{
 const path=new URL(req.url,'http://localhost').pathname;
 if(path==='/v1/provider-credentials/railway/primary/delete-plan')return json({isDefault:true,boxes:[],slots:[],cloudServers:0,canDelete:true,blockers:[]});
 if(path==='/v1/provider-credentials/railway/backup/delete-plan')return json({isDefault:false,boxes:planBlocked?[{id:'box-1',name:'Sleepy',state:'hibernated'}]:[],slots:[],cloudServers:0,canDelete:!planBlocked,blockers:planBlocked?['Delete or move every box on this provider first']:[]});
 if(path==='/v1/provider-credentials/railway/backup'&&req.method==='DELETE'){deleteBody=await new Promise(resolve=>{let data='';req.on('data',chunk=>data+=chunk);req.on('end',()=>resolve(JSON.parse(data)))});deleted++;providers.pop();res.statusCode=204;return res.end()}
 if(path==='/v1/provider-credentials')return json(providers);
 if(path==='/v1/controller-defaults')return json({provider:'railway',providerCredential:'primary'});
 if(path.startsWith('/v1/fleet/'))return json(path.includes('/slots')?{compute_box_slots:0}:{slots:[],actualSlots:0,freeSlots:0,occupiedSlots:0});
 if(path==='/')return res.end('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/vbox-tokens.css"><link rel="stylesheet" href="/workspace-nav.css"><link rel="stylesheet" href="/dialog-theme.css"></head><body><button id="manage-providers">Providers</button><script src="/workspace-nav.js"></script></body></html>');
 try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.woff2':'font/woff2'})[extname(path)]||'application/octet-stream');res.end(await readFile(resolve(web,path.slice(1))))}catch{res.statusCode=404;res.end()}
 function json(value){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))}
});

test('provider popup deletion shows blockers, requires typed name, and refreshes after success',async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.goto(`http://127.0.0.1:${server.address().port}/`);await page.evaluate(()=>window.VMBoxWorkspaceNav.initProviders('manage-providers').setOwner(true));
  await page.click('#manage-providers');await page.waitForSelector('.providers-table .providers-delete');
  await page.$$eval('.providers-table .providers-delete',buttons=>buttons[0].click());await page.waitForSelector('.provider-delete-dialog[open]');await page.type('.provider-delete-dialog input','primary');assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),true);await page.select('.provider-delete-dialog select','clear');assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),false);await page.click('.provider-delete-dialog [aria-label="Close delete dialog"]');await page.waitForFunction(()=>!document.querySelector('.provider-delete-dialog'));
  planBlocked=true;await page.$$eval('.providers-table .providers-delete',buttons=>buttons.at(-1).click());await page.waitForSelector('.provider-delete-dialog[open]');
  assert.match(await page.$eval('.provider-delete-dialog',dialog=>dialog.textContent),/Sleepy · hibernated/);
  assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),true);
  await page.click('.provider-delete-dialog [aria-label="Close delete dialog"]');await page.waitForFunction(()=>!document.querySelector('.provider-delete-dialog'));
  planBlocked=false;await page.$$eval('.providers-table .providers-delete',buttons=>buttons.at(-1).click());await page.waitForSelector('.provider-delete-dialog[open]');
  assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),true);
  await page.type('.provider-delete-dialog input','backup');
  assert.equal(await page.$eval('.provider-delete-dialog .danger',button=>button.disabled),false);
  await page.click('.provider-delete-dialog .danger');await page.waitForFunction(()=>document.querySelectorAll('.providers-table .providers-delete').length===1);
  assert.equal(deleted,1);assert.deepEqual(deleteBody,{});
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});

test('provider deletion dialog fits desktop and mobile in both themes',async()=>{
 planBlocked=true;providers.splice(0,providers.length,{provider:'railway',name:'primary',config:{}},{provider:'railway',name:'backup',config:{}});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{for(const theme of ['light','dark'])for(const width of [390,1440]){
  const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);await page.goto(`http://127.0.0.1:${server.address().port}/`);await page.evaluate(()=>window.VMBoxWorkspaceNav.initProviders('manage-providers').setOwner(true));await page.click('#manage-providers');await page.waitForSelector('.providers-delete');await page.$$eval('.providers-delete',buttons=>buttons.at(-1).click());await page.waitForSelector('.provider-delete-dialog[open]');
  const bounds=await page.$eval('.provider-delete-dialog',dialog=>{const card=dialog.getBoundingClientRect(),button=dialog.querySelector('.danger').getBoundingClientRect();return {top:card.top,bottom:card.bottom,left:card.left,right:card.right,buttonBottom:button.bottom}});
  assert.ok(bounds.top>=-1&&bounds.bottom<=page.viewport().height+1&&bounds.left>=-1&&bounds.right<=width+1&&bounds.buttonBottom<=bounds.bottom+1,`${theme} ${width}: ${JSON.stringify(bounds)}`);
  if(process.env.VMBOX_PROVIDER_DELETE_SCREENSHOTS)await page.screenshot({path:`${process.env.VMBOX_PROVIDER_DELETE_SCREENSHOTS}/provider-delete-${theme}-${width}.png`});
  await page.close();
 }}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
