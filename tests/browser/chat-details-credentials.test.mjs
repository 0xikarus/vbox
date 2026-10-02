import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const phase=process.env.VMBOX_CREDENTIALS_CAPTURE_PHASE||'after';
const captures=process.env.VMBOX_CREDENTIALS_CAPTURE_DIR?resolve(process.env.VMBOX_CREDENTIALS_CAPTURE_DIR,'details-credentials-'+phase):null;
const box={id:'builder',name:'Builder',state:'hibernated',defaultAgent:'claude',provider:'shared-worker'};

function makeServer(fail,writes,requests){
 let profileFailures=0;
 return http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path==='/v1/logical-boxes/builder/imported-credentials'||path==='/v1/login-profiles'||path==='/v1/logical-boxes/builder/login-profiles')requests.push(req.method+' '+path);
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner","accountId":"acct"}');
   if(path==='/v1/logical-boxes'||path==='/v1/grid-boxes')return res.end(JSON.stringify([box]));
   if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
   if(path==='/v1/logical-boxes/builder/imported-credentials'){
    await new Promise(done=>setTimeout(done,250));
    if(fail==='state'){res.statusCode=502;return res.end('{"error":"Credential service unavailable"}')}
    return res.end('{"profiles":[{"application":"claude","name":"old"}],"pending":[]}');
   }
   if(path==='/v1/login-profiles'){
    if(fail==='profiles'&&profileFailures++===0){res.statusCode=500;return res.end('{"error":"Could not list login profiles"}')}
    return res.end('[{"application":"claude","name":"old"},{"application":"codex","name":"new"}]');
   }
   if(path==='/v1/logical-boxes/builder/login-profiles'&&req.method==='PUT'){
    let raw='';for await(const chunk of req)raw+=chunk;
    const body=JSON.parse(raw);writes.push(body);
    return res.end(JSON.stringify({profiles:body.profiles,note:'Profile applied.'}));
   }
   if(path.endsWith('/messages')||['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
   if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
   return res.end('{}');
  }
  const file=resolve(web,path==='/chat'?'chat.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.html':'text/html','.woff2':'font/woff2'})[extname(file)]||'text/plain');res.end(await readFile(file))}
  catch{res.statusCode=404;res.end()}
 });
}

function visibility(){
 const modal=document.querySelector('#box-credentials-modal'),card=modal.querySelector('.sheet-card');
 const rect=card.getBoundingClientRect(),top=document.elementFromPoint(rect.left+rect.width/2,rect.top+Math.min(rect.height/2,80));
 return {open:!modal.hidden,top:card.contains(top),loading:document.querySelector('#box-credentials-status').textContent,applyDisabled:document.querySelector('#box-credentials-apply').disabled};
}

test('Details Credentials opens above drawer, applies selection, and shows request errors',async()=>{
 if(captures)await mkdir(captures,{recursive:true});
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  for(const width of [1440,390])for(const fail of ['none','state','profiles']){
   const writes=[],requests=[];const server=makeServer(fail,writes,requests);await new Promise(done=>server.listen(0,'127.0.0.1',done));
   try{
    const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
    await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
    await page.waitForFunction(()=>!document.querySelector('#chat-conversation').hidden);
    await page.$eval('#chat-info',button=>button.click());
    await page.click('.inspect-technical > summary');
    await page.waitForFunction(()=>[...document.querySelectorAll('#inspect-config-actions button')].some(button=>button.textContent.includes('Credentials')));
    await page.evaluate(()=>{window.credentialsClicks=0;document.querySelector('#inspect-config-actions').addEventListener('click',event=>{if(event.target.closest('button')?.textContent.includes('Credentials'))window.credentialsClicks++})});
    const point=await page.evaluate(()=>{
     const button=document.querySelector('#inspect-config-actions button:nth-child(2)');
     button.scrollIntoView({block:'center'});
     const rect=button.getBoundingClientRect();
     return {x:rect.left+rect.width/2,y:rect.top+rect.height/2};
    });
    if(width===390)await page.touchscreen.tap(point.x,point.y);
    else await page.mouse.click(point.x,point.y);
    assert.equal(await page.evaluate(()=>window.credentialsClicks),1,'the physical click reaches the Details action');
    const initial=await page.evaluate(visibility);
    if(phase!=='before'){
     assert.equal(initial.open,true,'modal opens while requests are pending');
     assert.equal(initial.top,true,'modal is above Details');
     assert.match(initial.loading,/Loading/);
     assert.equal(initial.applyDisabled,true);
    }
    await new Promise(done=>setTimeout(done,400));
    assert.ok(requests.includes('GET /v1/logical-boxes/builder/imported-credentials'),'the click sends the box credentials GET');
    assert.ok(requests.includes('GET /v1/login-profiles'),'the click sends the available profiles GET');
    const final=await page.evaluate(visibility);
    if(captures)await page.screenshot({path:resolve(captures,`${width}-${fail}.png`)});
    if(phase!=='before'){
     assert.equal(final.open,true);
     assert.equal(final.top,true);
     if(fail==='profiles'){
      assert.match(final.loading,/Could not load available profiles: Could not list login profiles/);
      assert.equal(final.applyDisabled,true);
      assert.equal(await page.$eval('#box-credentials-retry',button=>!button.hidden),true);
      await page.click('#box-credentials-retry');
      await page.waitForFunction(()=>!document.querySelector('#box-credentials-apply').disabled);
      assert.equal(await page.$eval('#box-credentials-retry',button=>button.hidden),true);
     }else{
      assert.equal(final.applyDisabled,false);
      if(fail==='state'){
       assert.match(await page.$eval('#box-credentials-current',el=>el.textContent),/Current imported profile unknown: Credential service unavailable/);
       assert.match(final.loading,/You can still choose and apply a profile/);
      }
      await page.select('#box-credentials-form select',JSON.stringify({application:'codex',name:'new'}));
      await page.click('#box-credentials-apply');
      await page.waitForFunction(()=>document.querySelector('#box-credentials-status').textContent==='Profile applied.');
      assert.deepEqual(writes,[{profiles:[{application:'codex',name:'new'}]}]);
     }
    }
    await page.close();
   }finally{server.closeAllConnections();await new Promise(done=>server.close(done))}
  }
 }finally{await browser.close()}
});
