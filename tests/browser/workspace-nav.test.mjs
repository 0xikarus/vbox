import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const source={};
for(const name of ['chat.html','grid.html','index.html','app.css','chat.css','vbox-tokens.css','vbox-c.css','grid.css','controller.css','manager-theme.css','workspace-nav.css','workspace-nav.js'])source[name]=await readFile(root+name);
const server=http.createServer((req,res)=>{
 const path=new URL(req.url,'http://local').pathname;
 if(path==='/v1/profile-usage'){res.setHeader('Content-Type','application/json');return res.end(JSON.stringify({profiles:[{application:'codex',name:'saved',snapshot:{windows:[{name:'session',usedPercent:40}]}}]}))}
 const file=path==='/chat'?'chat.html':path==='/grid'?'grid.html':path==='/'?'index.html':path.slice(1);
 if(file in source){res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');return res.end(source[file])}
 if(file.endsWith('.js')){res.setHeader('Content-Type','text/javascript');return res.end('')}
 if(file.endsWith('.css')){res.setHeader('Content-Type','text/css');return res.end('')}
 res.statusCode=404;res.end();
});
await new Promise(done=>server.listen(0,'127.0.0.1',done));
const base='http://127.0.0.1:'+server.address().port;
const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
const screenshots=process.env.VMBOX_NAV_SCREENSHOTS;
if(screenshots)await mkdir(screenshots,{recursive:true});
try{
 await test('workspace header keeps controls aligned across pages and sizes',async()=>{
  for(const width of [1320,390,320]){
   const views=[];
   for(const [name,path] of [['chats','/chat'],['grid','/grid'],['boxes','/#boxes']]){
    const page=await browser.newPage();await page.setViewport({width,height:width<=390?844:850,deviceScaleFactor:1,isMobile:width<=390,hasTouch:width<=390});
    await page.goto(base+path);
    await page.evaluate(name=>{
     document.querySelector('.workspace-nav-usage').hidden=false;
     if(name==='boxes'){document.getElementById('manage-page-label').textContent='boxes';document.querySelector('.workspace-links a[href="#boxes"]').setAttribute('aria-current','page')}
    },name);
    const boxes=await page.evaluate(()=>{
     const rect=selector=>{const box=document.querySelector(selector).getBoundingClientRect();return {left:box.left,top:box.top,right:box.right,bottom:box.bottom}};
     return {header:rect('.workspace-top'),menu:rect('.workspace-nav-menu'),brand:rect('.brand'),links:rect('.workspace-links'),usage:rect('.workspace-nav-usage'),refresh:rect('.workspace-nav-refresh'),scrollWidth:document.documentElement.scrollWidth};
    });
    assert.ok(boxes.scrollWidth<=width,`${width}px ${name} page overflows horizontally: ${boxes.scrollWidth}px`);
    if(screenshots)await page.screenshot({path:`${screenshots}/${name}-${width}.png`});
    views.push({name,boxes});await page.close();
   }
   const baseline=views[0].boxes;
   for(const {name,boxes} of views.slice(1))for(const [key,edge] of [['menu','left'],['brand','left'],['links','left'],['links','top'],['usage','right'],['refresh','right'],['header','bottom']]){
    assert.ok(Math.abs(boxes[key][edge]-baseline[key][edge])<2,`${width}px ${name} ${key}.${edge}: ${boxes[key][edge]} vs ${baseline[key][edge]}`);
   }
  }
 });
 await test('grid menu and usage actions remain available in the shared header',async()=>{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});await page.goto(base+'/grid');
  await page.evaluate(()=>window.VMBoxWorkspaceNav.init({menuId:'grid-menu',panelId:'grid-menu-panel',usageId:'grid-usage'}).setOwner(true));
  await page.click('#grid-menu');assert.equal(await page.$eval('#grid-menu-panel',el=>el.hidden),false);
  await page.click('#grid-menu');assert.equal(await page.$eval('#grid-menu-panel',el=>el.hidden),true);
  await page.click('#grid-usage');await page.waitForFunction(()=>document.querySelector('.workspace-usage-dialog')?.textContent.includes('60% remaining'));
  await page.click('.workspace-usage-dialog header button');assert.equal(await page.$eval('.workspace-usage-dialog',el=>el.open),false);
  await page.close();
 });
}finally{await browser.close();await new Promise(done=>server.close(done))}
