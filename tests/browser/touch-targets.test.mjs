import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude',provider:'railway',providerCredential:'primary'};
const now='2026-10-01T10:00:00Z';
const messages=[{id:'photo',direction:'agent',state:'delivered',text:'Here is the preview.',images:[{id:'image',number:1,mediaType:'image/svg+xml'}],createdAt:now,updatedAt:now}];
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400"><rect width="640" height="400" fill="#dae1e8"/><circle cx="320" cy="200" r="95" fill="#fb716c"/></svg>';
const targets={
 chatTop:['#chat-menu','#usage-toggle','#chat-providers','#refresh'],
 composer:['#attach','#chat-composer .ai-wand'],
 viewer:['#media-viewer-close','#media-viewer-zoom-out','#media-viewer-zoom-in','#media-viewer-zoom-reset'],
 boxes:['#manage-menu','#manage-usage','#manage-providers','#refresh','#box-list .box-details-action','#box-list .row-overflow-trigger'],
 providers:['#provider-list .provider-actions>button','#provider-list .provider-actions-more>summary']
};
// Control rectangles measured on origin/main e70be06 at 1440px.
const desktopBounds={
 chatTop:[[41.172,38.391],[61.703,36],[97.656,36],[41.172,38.391]],
 composer:[[40,40],[32,32]],viewer:[[36.797,36.797],[32,32],[32,32],[40.047,32]],
 boxes:[[41.172,38.391],[61.703,36],[97.656,36],[41.172,38.391],[59.797,31.594],[33.594,33.594]]
};
function assertDesktopBounds(kind,items){assert.equal(items.length,desktopBounds[kind].length,kind);items.forEach((item,index)=>{const [width,height]=desktopBounds[kind][index];assert(Math.abs(item.visualWidth-width)<.15&&Math.abs(item.visualHeight-height)<.15,JSON.stringify({kind,index,item,expected:[width,height]}))})}
const dimensions=async(page,selectors)=>page.evaluate(selectors=>selectors.flatMap(selector=>[...document.querySelectorAll(selector)].filter(node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width&&rect.height}).map(node=>{
 const rect=node.getBoundingClientRect(),pseudo=getComputedStyle(node,'::after');
 const active=pseudo.content!=='none'&&pseudo.position==='absolute'&&pseudo.display!=='none';
 const number=value=>Number.parseFloat(value)||0;
 const left=active?number(pseudo.left):0,right=active?number(pseudo.right):0,top=active?number(pseudo.top):0,bottom=active?number(pseudo.bottom):0;
 const ownsPoint=(x,y)=>{const hit=document.elementFromPoint(x,y);return hit===node||node.contains(hit)};
 const edgeY=rect.top-Math.min(3,Math.max(.5,(40-rect.height)/2-.25));
 const edgeX=rect.left-Math.min(3,Math.max(.5,(40-rect.width)/2-.25));
 const scope=node.closest('#chat-top,.workspace-top,#chat-composer,#media-viewer-card,#box-list,#provider-list');
 const neighbours=[...scope.querySelectorAll('button,a,summary,input,select,textarea,[role="button"]')].filter(other=>{
  if(other===node||node.contains(other)||other.contains(node)||!other.checkVisibility())return false;
  const bounds=other.getBoundingClientRect(),style=getComputedStyle(other);
  return bounds.width&&bounds.height&&style.visibility!=='hidden'&&style.display!=='none'&&
   bounds.right>=rect.left-20&&bounds.left<=rect.right+20&&bounds.bottom>=rect.top-20&&bounds.top<=rect.bottom+20;
 }).map(other=>{const bounds=other.getBoundingClientRect(),x=bounds.left+bounds.width/2,y=bounds.top+bounds.height/2,hit=document.elementFromPoint(x,y);
  return {name:other.id||other.getAttribute('aria-label')||other.textContent.trim().slice(0,30),hit:hit===other||other.contains(hit)};
 });
 return {selector:node.id?'#'+node.id:selector,visualWidth:rect.width,visualHeight:rect.height,hitWidth:rect.width-left-right,hitHeight:rect.height-top-bottom,topHit:rect.height>=40||ownsPoint(rect.left+rect.width/2,edgeY),leftHit:rect.width>=40||ownsPoint(edgeX,rect.top+rect.height/2),neighbours};
})),selectors);
function assertNeighbourCentres(kind,items){for(const item of items)for(const neighbour of item.neighbours)assert(neighbour.hit,`${kind}: ${item.selector} covers neighbouring ${neighbour.name}`)}
async function assertProviderTopbarColor(page,usage,providers){
 const colors=await page.evaluate(([usageSelector,providerSelector])=>[usageSelector,providerSelector].map(selector=>{const style=getComputedStyle(document.querySelector(selector));return {background:style.backgroundColor,color:style.color,border:style.borderTopColor}}),[usage,providers]);
 assert.deepEqual(colors[1],colors[0],'Providers resting colors match Usage');
}

test('phone controls have 40px hit areas while desktop bounds stay unchanged',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path.startsWith('/v1/')){
   if(path==='/v1/messages/photo/images/image'){res.setHeader('Content-Type','image/svg+xml');return res.end(svg)}
   res.setHeader('Content-Type','application/json');const json=value=>res.end(JSON.stringify(value));
   if(path==='/v1/whoami')return json({role:'owner',accountId:'acct'});
   if(path==='/v1/capabilities')return json({providerEdits:true});
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return json([box]);
   if(path==='/v1/logical-boxes/builder/messages')return json(messages);
   if(path==='/v1/provider-credentials')return json([{provider:'railway',name:'primary',config:{}},{provider:'shared-worker',name:'local',config:{}}]);
   if(path==='/v1/controller-defaults')return json({provider:'railway',providerCredential:'primary'});
   if(path==='/v1/fleet/status')return json({slots:[{id:'slot-1',state:'free'}],actualSlots:1,freeSlots:1,occupiedSlots:0});
   if(path==='/v1/profile-usage')return json({profiles:[]});
   if(path==='/v1/agent-cli-versions')return json({claude:'latest',codex:'latest',opencode:'latest'});
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return json({})}
   if(['/v1/box-conversations','/v1/tool-presets','/v1/chat-commands','/v1/box-activity','/v1/login-profiles','/v1/notifications'].includes(path))return json([]);
   return json({});
  }
  const file=resolve(web,path==='/chat'?'chat.html':path==='/'?'index.html':path.slice(1));if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const base='http://127.0.0.1:'+server.address().port,captureDir=process.env.VMBOX_CAPTURE_DIR,verify=process.env.VMBOX_TOUCH_VERIFY!=='0';
  if(captureDir)await mkdir(captureDir,{recursive:true});
  for(const width of [390,1440])for(const theme of ['light','dark']){
   const mobile=width===390,phone=await browser.newPage();await phone.setViewport({width,height:mobile?844:900,isMobile:mobile,hasTouch:mobile});await phone.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   const suffix=width+'-'+theme;
   await phone.goto(base+'/chat');await phone.waitForSelector('#chat-menu');await new Promise(done=>setTimeout(done,250));
   let found=await dimensions(phone,targets.chatTop);if(process.env.VMBOX_TOUCH_REPORT)console.log('chatTop',suffix,JSON.stringify(found));assert(found.length>=3,JSON.stringify(found));if(verify&&mobile){assert(found.every(item=>item.hitWidth>=39.9&&item.hitHeight>=39.9&&item.topHit&&item.leftHit),JSON.stringify(found));assertNeighbourCentres('chatTop',found)}if(verify&&!mobile)assertDesktopBounds('chatTop',found);
   await assertProviderTopbarColor(phone,'#usage-toggle','#chat-providers');
   if(captureDir&&mobile)await phone.screenshot({path:`${captureDir}/chat-list-${suffix}.png`});
   await phone.goto(base+'/chat#box=builder');await phone.waitForSelector('.media-button');await phone.waitForSelector('#chat-composer .ai-wand');await new Promise(done=>setTimeout(done,350));
   found=await dimensions(phone,targets.composer);if(process.env.VMBOX_TOUCH_REPORT)console.log('composer',suffix,JSON.stringify(found));assert.equal(found.length,2,JSON.stringify(found));if(verify&&mobile){assert(found.every(item=>item.hitWidth>=39.9&&item.hitHeight>=39.9&&item.topHit&&item.leftHit),JSON.stringify(found));assertNeighbourCentres('composer',found)}if(verify&&!mobile)assertDesktopBounds('composer',found);
   if(captureDir&&mobile)await phone.screenshot({path:`${captureDir}/chat-${suffix}.png`});
   await phone.$eval('.media-button',node=>node.click());await phone.waitForFunction(()=>!document.querySelector('#media-viewer-zoom').hidden);
   found=await dimensions(phone,targets.viewer);if(process.env.VMBOX_TOUCH_REPORT)console.log('viewer',suffix,JSON.stringify(found));assert.equal(found.length,4);if(verify&&mobile){assert(found.every(item=>item.hitWidth>=39.9&&item.hitHeight>=39.9&&item.topHit&&item.leftHit),JSON.stringify(found));assertNeighbourCentres('viewer',found)}if(verify&&!mobile)assertDesktopBounds('viewer',found);
   if(captureDir&&mobile)await phone.screenshot({path:`${captureDir}/viewer-${suffix}.png`});
   await phone.$eval('#media-viewer-zoom-in',node=>node.click());await phone.waitForFunction(()=>document.querySelector('#media-viewer-zoom-level').textContent==='150%');
   await phone.$eval('#media-viewer-close',node=>node.click());await phone.waitForFunction(()=>document.querySelector('#media-viewer').hidden);await phone.close();
   const manage=await browser.newPage();await manage.setViewport({width,height:mobile?844:900,isMobile:mobile,hasTouch:mobile});await manage.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await manage.goto(base+'/#boxes');await manage.waitForSelector('#box-list .box-details-action');await new Promise(done=>setTimeout(done,250));
   found=await dimensions(manage,targets.boxes);if(process.env.VMBOX_TOUCH_REPORT)console.log('boxes',suffix,JSON.stringify(found));assert(found.length>=5,JSON.stringify(found));if(verify&&mobile){assert(found.every(item=>item.hitWidth>=39.9&&item.hitHeight>=39.9&&item.topHit&&item.leftHit),JSON.stringify(found));assertNeighbourCentres('boxes',found)}if(verify&&!mobile)assertDesktopBounds('boxes',found);
   await assertProviderTopbarColor(manage,'#manage-usage','#manage-providers');
   if(captureDir&&mobile)await manage.screenshot({path:`${captureDir}/boxes-${suffix}.png`});
   await manage.$eval('#box-list .box-details-action',node=>node.click());await manage.waitForSelector('#box-detail:not([hidden])');await manage.$eval('#box-detail-close',node=>node.click());
   if(mobile){
    await manage.click('#manage-menu');
    const menuHits=await manage.$$eval('#manage-menu-panel a',links=>links.map(link=>{const rect=link.getBoundingClientRect(),hit=document.elementFromPoint(rect.left+rect.width/2,rect.top+rect.height/2);return {name:link.textContent,hit:hit===link||link.contains(hit)}}));
    assert(menuHits.every(item=>item.hit),JSON.stringify(menuHits));
    await manage.click('#manage-menu-panel a[href="#profiles"]');assert.equal(await manage.$eval('#manage-menu-panel',node=>node.hidden),true);
    await manage.click('#manage-menu');await manage.click('#manage-menu-panel a[href="#providers"]');assert.equal(await manage.$eval('#manage-menu-panel',node=>node.hidden),true);
   }
   await manage.goto(base+'/#providers');await manage.waitForSelector('#provider-list .provider-actions>button');
   found=await dimensions(manage,targets.providers);if(process.env.VMBOX_TOUCH_REPORT)console.log('providers',suffix,JSON.stringify(found));assert.equal(found.length,6,JSON.stringify(found));if(verify&&mobile){assert(found.every(item=>item.hitWidth>=39.9&&item.hitHeight>=39.9&&item.topHit&&item.leftHit),JSON.stringify(found));assertNeighbourCentres('providers',found)}
   if(captureDir&&mobile)await manage.screenshot({path:`${captureDir}/providers-${suffix}.png`});
   await manage.$eval('#provider-list .provider-actions-more>summary',node=>node.click());assert.equal(await manage.$eval('#provider-list .provider-actions-more',node=>node.open),true);await manage.close();
  }
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
