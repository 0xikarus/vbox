import assert from 'node:assert/strict';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const origin=process.env.MAIL_PANEL_REAL_ORIGIN;
const token=process.env.MAIL_PANEL_REAL_TOKEN;
if(!origin||!token)throw Error('real handler URL and token are required');
const capture=process.env.MAIL_PANEL_REAL_CAPTURE_DIR;
if(capture)await mkdir(capture,{recursive:true});
const web=resolve('internal/controller/web');
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'};
const server=http.createServer(async(request,response)=>{
 const url=new URL(request.url,'http://localhost'),path=url.pathname;
 if(path.startsWith('/v1/mail/')){
  const upstream=await fetch(new URL(path+url.search,origin),{headers:{Authorization:'Bearer '+token}});
  response.statusCode=upstream.status;
  response.setHeader('Content-Type',upstream.headers.get('content-type')||'application/json');
  response.end(Buffer.from(await upstream.arrayBuffer()));
  return;
 }
 if(path.startsWith('/v1/')){
  response.setHeader('Content-Type','application/json');
  const send=(value,status=200)=>{response.statusCode=status;response.end(JSON.stringify(value))};
  if(path==='/v1/whoami')return send({role:'owner',accountId:'acct'});
  if(path==='/v1/capabilities')return send({providerEdits:true});
  if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return send([]);
  if(path==='/v1/chat-sidebar-layout')return send({exists:true,groups:[],members:{}});
  if(path==='/v1/box-conversations'||path==='/v1/chat-commands')return send([]);
  if(path==='/v1/instruction-presets')return send({defaultName:'',presets:[]});
  if(path==='/v1/provider-credentials'||path==='/v1/notifications'||path==='/v1/login-profiles'||path==='/v1/tool-presets')return send([]);
  if(path==='/v1/controller-defaults')return send({provider:'railway',providerCredential:'primary'});
  if(path==='/v1/agent-cli-versions')return send({});
  if(path.startsWith('/v1/agent-cli-versions/catalog/'))return send({latest:'1.0.0',versions:['1.0.0']});
  if(path==='/v1/push/vapid-key')return send({},404);
  return send({});
 }
 const file=path==='/'?'index.html':path.slice(1);
 if(file.includes('..')){response.statusCode=404;response.end();return}
 try{response.setHeader('Content-Type',types[extname(file)]||'application/octet-stream');response.end(await readFile(resolve(web,file)))}
 catch{response.statusCode=404;response.end()}
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const base='http://127.0.0.1:'+server.address().port;
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
try{
 for(const width of [390,1440])for(const theme of ['light','dark']){
  const page=await browser.newPage();
  try{
   await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   const stale='00000000-0000-4000-8000-000000000001';
   await page.goto(base+'/?box='+stale+'#mail');
   try{await page.waitForFunction(()=>document.querySelectorAll('.mail-panel-row').length===4,{timeout:30000})}
   catch(error){
    const state=await page.evaluate(()=>({url:location.href,loginHidden:document.querySelector('#login')?.hidden,appHidden:document.querySelector('#app')?.hidden,mailHidden:document.querySelector('#mail')?.hidden,mailText:document.querySelector('#mail')?.textContent.slice(0,350)}));
    throw Error(`${error.message}; state=${JSON.stringify(state)}`);
   }
   assert.equal(await page.$eval('[data-folder="all"] b',node=>node.textContent),'4');
   assert.equal(await page.$eval('[data-box=""]',node=>node.getAttribute('aria-current')),'page');
   assert.equal(await page.evaluate(()=>location.search),'');
   assert.equal(await page.$eval('.mail-panel-list-title small',node=>node.textContent),'All boxes');
   assert.equal(await page.$('.mail-panel-folders h3:not(.mail-panel-subhead)'),null);
   assert.equal(await page.$('.mail-panel-state[role="alert"]'),null);
   if(capture)await page.screenshot({path:resolve(capture,`mail-real-${width}-${theme}.png`),fullPage:true});
   console.log(`real handler ${width} ${theme}: 4 inbox messages`);
  }finally{await page.close()}
 }
}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
