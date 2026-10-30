import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtemp,readFile,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {test} from 'node:test';
import puppeteer from 'puppeteer-core';

test('standalone shares controller chat styling with a lazy combined Desktop/TMUX panel',{timeout:60000},async()=>{
 const dir=await mkdtemp(join(tmpdir(),'vbox-chat-first-browser-'));
 const child=spawn(process.env.VMBOX_GO||'go',['test','./internal/standalone','-run','^TestChatFirstBrowserFixture$','-count=1','-timeout=60s'],{env:{...process.env,VMBOX_STANDALONE_BROWSER_FIXTURE:dir},stdio:['ignore','pipe','pipe']});
 let log='',browser;child.stdout.on('data',b=>log+=b);child.stderr.on('data',b=>log+=b);
 const exited=new Promise(resolve=>child.on('exit',resolve));
 try{
  let config;
  for(let i=0;i<150;i++){try{config=JSON.parse(await readFile(join(dir,'fixture.json'),'utf8'));break}catch{if(child.exitCode!==null)throw Error('Fixture failed: '+log);await new Promise(r=>setTimeout(r,100))}}
  assert.ok(config,'fixture did not start');
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
  const page=await browser.newPage();await page.setViewport({width:1280,height:900});await page.setRequestInterception(true);
  page.on('request',r=>{
   const p=new URL(r.url()).pathname;
   if(p==='/workspace-desktop.js'||p==='/workspace-terminal.js'){
    const name=p.includes('desktop')?'Desktop':'Terminal';
    return r.respond({status:200,contentType:'text/javascript',body:`window.openWorkspace${name}=(...args)=>{window.viewerStarts=(window.viewerStarts||0)+1;const options=args.at(-1);options.root.textContent='${name} fixture';return()=>{window.viewerStops=(window.viewerStops||0)+1;options.root.replaceChildren()}}`});
   }
   r.continue();
  });
  await page.goto(config.url);await page.waitForSelector('#chat-messages strong');
  assert.equal(await page.$('#chat-list'),null);
  assert.equal(await page.$eval('#desktop-panel',n=>n.open),false);
  assert.equal(await page.evaluate(()=>window.viewerStarts||0),0,'viewers started before Desktop was opened');
  const bounds=await page.$eval('.conversation',n=>({width:n.getBoundingClientRect().width,height:n.getBoundingClientRect().height}));assert.ok(bounds.width>1200&&bounds.height>700);
  await page.type('#chat-input','Browser delivery fixture');await page.click('#send');await page.waitForFunction(()=>document.querySelector('#chat-input').value==='');
  await page.waitForFunction(()=>document.querySelector('#chat-messages').textContent.includes('Browser delivery fixture'));
  await page.screenshot({path:'/tmp/vbox-chat-first-desktop.png'});
  await page.click('#chat-header-desktop');await page.waitForFunction(()=>window.viewerStarts===2);
  const widths=await page.$$eval('#desktop-panel .viewer',nodes=>nodes.map(n=>Math.round(n.getBoundingClientRect().width)));assert.equal(widths[0],widths[1]);
  await page.click('#desktop-panel-close');await page.waitForFunction(()=>window.viewerStops===2);
  await page.click('#chat-header-terminal');await page.waitForFunction(()=>window.viewerStarts===4);await page.keyboard.press('Escape');await page.waitForFunction(()=>window.viewerStops===4);
  await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const fits=await page.evaluate(()=>({page:document.documentElement.scrollWidth<=innerWidth,composer:document.querySelector('#chat-composer').getBoundingClientRect().bottom<=innerHeight+1}));assert.deepEqual(fits,{page:true,composer:true});
  await page.screenshot({path:'/tmp/vbox-chat-first-mobile.png'});
 }finally{await browser?.close();await writeFile(join(dir,'done'),'done');const code=await exited;await rm(dir,{recursive:true,force:true});assert.equal(code,0,log)}
});
