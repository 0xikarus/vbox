import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const boxes=[{id:'reviewer',name:'reviewer',state:'running',defaultAgent:'codex'}];
const providers=[{provider:'railway',name:'primary',config:{}},{provider:'shared-worker',name:'local',config:{}}];
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
  const color=await desktop.$eval('#role-editor-modal .mcp-tool-group strong',node=>getComputedStyle(node).color);assert.notEqual(color,'rgb(255, 255, 255)');
  await desktop.goto(base+'#profiles');await desktop.waitForSelector('#agent-cli-versions select');await desktop.waitForFunction(()=>document.querySelector('#agent-cli-versions select').selectedOptions[0]?.textContent.includes('1.0.0'));
  assert(await desktop.$eval('#agent-cli-versions select',node=>node.getBoundingClientRect().width)>=350);
  await desktop.goto(base+'#providers');await desktop.waitForSelector('#provider-list .provider-actions-more');
  const row=await desktop.$eval('#provider-list .provider-card:last-of-type .provider-actions',node=>({tops:[...node.children].map(child=>Math.round(child.getBoundingClientRect().top)),labels:[...node.children].map(child=>child.textContent.trim())}));
  assert(row.tops.every(top=>top===row.tops[0]),JSON.stringify(row));assert.deepEqual(row.labels.slice(0,3),['Edit','Validate','Delete']);
  await desktop.$eval('#provider-list .provider-actions-more>summary',node=>node.click());assert.match(await desktop.$eval('#provider-list .provider-actions-extra',node=>node.innerText),/Refresh usage[\s\S]*Use as default/);
  await desktop.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
