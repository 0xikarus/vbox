import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const root='internal/controller/web/';
const names=['chat.html','index.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js','ai-helper.js','ai-helper.css','sheet-scroll.js','sheet-scroll.css','dialog-theme.css','text-size.js','fonts.css'];
const assets=new Map(await Promise.all(names.map(async name=>[name,await readFile(root+name)])));
const phase=process.env.VMBOX_INSTRUCTIONS_CAPTURE_PHASE||'after';
const captureDir='/data/workspace/captures/new-box-instructions-'+phase;
await mkdir(captureDir,{recursive:true});

function geometry(selector='#create-instructions-custom'){
 const input=document.querySelector(selector),button=input.parentElement.querySelector('.ai-wand');
 const textarea=input.getBoundingClientRect(),wand=button.getBoundingClientRect(),style=getComputedStyle(input),wandStyle=getComputedStyle(button);
 const text={left:textarea.left+parseFloat(style.paddingLeft),right:textarea.right-parseFloat(style.paddingRight),top:textarea.top+parseFloat(style.paddingTop),bottom:textarea.bottom-parseFloat(style.paddingBottom)};
 const intersects=text.left<wand.right&&text.right>wand.left&&text.top<wand.bottom&&text.bottom>wand.top;
 return {textarea:{left:textarea.left,right:textarea.right,top:textarea.top,bottom:textarea.bottom},wand:{left:wand.left,right:wand.right,top:wand.top,bottom:wand.bottom,width:wand.width,height:wand.height},text,intersects,background:wandStyle.backgroundColor};
}

test('New box custom Markdown wand stays inside the textarea and preview follows Name',async()=>{
 const rewrites=[];
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname,asset=path==='/chat'?'chat.html':path==='/'?'index.html':path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',asset.endsWith('.html')?'text/html':asset.endsWith('.css')?'text/css':'text/javascript');return res.end(assets.get(asset))}
  if(/^[a-z0-9-]+\.(?:css|js)$/.test(asset))try{const file=await readFile(root+asset);res.setHeader('Content-Type',asset.endsWith('.css')?'text/css':'text/javascript');return res.end(file)}catch{}
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/ai/rewrite'){let raw='';for await(const chunk of req)raw+=chunk;const body=JSON.parse(raw);rewrites.push(body);return res.end(JSON.stringify({text:body.text.replace('Helo','Hello')}))}
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes'||path==='/v1/tool-presets'||path==='/v1/login-profiles'||path==='/v1/provider-credentials'||path==='/v1/box-conversations')return res.end('[]');
  if(path==='/v1/instruction-presets')return res.end(JSON.stringify({defaultName:'',presets:[]}));
  if(path.endsWith('/messages'))return res.end('[]');
  res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const measurements=[];
  for(const width of [1440,390])for(const theme of ['light','dark']){
   const page=await browser.newPage();await page.setViewport({width,height:width===390?844:900,isMobile:width===390,hasTouch:width===390});
   await page.emulateMediaFeatures([{name:'prefers-color-scheme',value:theme}]);
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat');
   await page.waitForSelector('#new-box:not([hidden])');await page.click('#new-box');
   await page.waitForFunction(()=>[...document.querySelector('#create-instructions').options].some(option=>option.value==='custom'));
   await page.$eval('#create-box input[name="name"]',input=>{input.value='BossDev';input.dispatchEvent(new Event('input',{bubbles:true}))});
   await page.select('#create-instructions','custom');
   await page.$eval('#create-instructions-custom',input=>{input.value='# Helo BossDev\nKeep the code clear.';input.dispatchEvent(new Event('input',{bubbles:true}))});
   await page.$eval('#create-instructions-editor',el=>el.scrollIntoView({block:'center'}));
   const measured=await page.evaluate(geometry);measurements.push({width,theme,...measured});
   await page.screenshot({path:captureDir+'/'+width+'-'+theme+'.png'});
   if(phase!=='before'){
    assert.ok(measured.wand.width>=32&&measured.wand.width<=40&&measured.wand.height>=32&&measured.wand.height<=40,JSON.stringify(measured));
    assert.ok(measured.wand.left>=measured.textarea.left&&measured.wand.right<=measured.textarea.right+1&&measured.wand.top>=measured.textarea.top&&measured.wand.bottom<=measured.textarea.bottom+1,'wand stays inside the textarea');
    assert.equal(measured.intersects,false,'textarea text area does not intersect the wand');
    assert.equal(await page.$eval('#create-preview-name',el=>el.textContent),'BossDev');
    assert.equal(await page.$eval('#create-instructions-editor summary',el=>el.textContent),'Customize Markdown');
   }
   if(width===1440&&theme==='light'&&phase!=='before'){
    await page.$eval('#create-box input[name="name"]',input=>{input.value='';input.dispatchEvent(new Event('input',{bubbles:true}))});
    assert.equal(await page.$eval('#create-preview-name',el=>el.textContent),'my-agent-box','placeholder returns only for an empty name');
    await page.$eval('#create-box input[name="name"]',input=>{input.value='BossDev';input.dispatchEvent(new Event('input',{bubbles:true}))});
    await page.click('#create-instructions-editor .ai-wand');
    await page.waitForFunction(()=>document.querySelector('#create-instructions-custom').value.includes('Hello BossDev'));
    assert.equal(rewrites.at(-1).kind,'markdown');
   }
   await page.close();
  }
  await writeFile(captureDir+'/geometry.json',JSON.stringify(measurements,null,2));
  if(phase!=='before'){
   const chat=await browser.newPage();await chat.setViewport({width:1440,height:900});await chat.goto('http://127.0.0.1:'+server.address().port+'/chat');
   await chat.waitForSelector('#preset-form .ai-wand');
   for(const [modal,selector] of [['#presets-modal','#preset-form textarea[name="markdown"]'],['#box-instructions-modal','#box-instructions-markdown']]){
    await chat.$eval(modal,node=>node.hidden=false);
    const measured=await chat.evaluate(geometry,selector);
    assert.ok(measured.wand.width<=40&&measured.wand.height<=40&&!measured.intersects,selector+' wand geometry: '+JSON.stringify(measured));
    await chat.$eval(selector,input=>{input.value='# Helo editor';input.dispatchEvent(new Event('input',{bubbles:true}))});
    await chat.click(modal+' .ai-wand');
    await chat.waitForFunction(selector=>document.querySelector(selector).value.includes('Hello editor'),{},selector);
    await chat.$eval(modal,node=>node.hidden=true);
   }
   await chat.close();
   const manage=await browser.newPage();await manage.setViewport({width:1440,height:900});await manage.goto('http://127.0.0.1:'+server.address().port+'/');
   await manage.waitForSelector('#instruction-form .ai-wand');
   await manage.evaluate(()=>{document.querySelector('#app').hidden=false;document.querySelector('#login').hidden=true;document.querySelector('#instruction-editor').open=true});
   const measured=await manage.evaluate(geometry,'#instruction-form textarea[name="markdown"]');
   assert.ok(measured.wand.width<=40&&measured.wand.height<=40&&!measured.intersects,'Manage instructions wand geometry: '+JSON.stringify(measured));
   await manage.$eval('#instruction-form textarea[name="markdown"]',input=>{input.value='# Helo manage';input.dispatchEvent(new Event('input',{bubbles:true}))});
   await manage.$eval('#instruction-form .ai-wand',button=>button.click());
   await manage.waitForFunction(()=>document.querySelector('#instruction-form textarea[name="markdown"]').value.includes('Hello manage'));
   await manage.close();
  }
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
