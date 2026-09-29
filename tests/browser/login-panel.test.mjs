import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import {after,before,test} from 'node:test';
import {resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

const root=resolve('internal/controller/web');
const pages={'/':'index.html','/chat':'chat.html','/grid':'grid.html','/boxes/test-box':'workspace.html'};
let server,browser,base;

before(async()=>{
 server=http.createServer(async(request,response)=>{
  const path=new URL(request.url,'http://test').pathname;
  if(path.startsWith('/v1/')){response.writeHead(401,{'Content-Type':'application/json'});response.end(JSON.stringify({error:'Invalid controller token'}));return}
  const file=pages[path]||path.slice(1);
  if(!/^[a-zA-Z0-9.-]+$/.test(file)){response.writeHead(404);response.end();return}
  try{
   const data=await readFile(resolve(root,file));
   response.setHeader('Content-Type',file.endsWith('.css')?'text/css':file.endsWith('.js')?'text/javascript':file.endsWith('.svg')?'image/svg+xml':'text/html');
   response.end(data);
  }catch{response.writeHead(404);response.end()}
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-setuid-sandbox']});
});
after(async()=>{await browser?.close();await new Promise(resolve=>server?.close(resolve))});

test('all web pages use the same centered login panel and show sign-in errors there',async()=>{
 for(const path of Object.keys(pages)){
  const page=await browser.newPage();
  await page.setViewport({width:390,height:844});
  await page.goto(base+path);
  await page.waitForSelector('#login:not([hidden])');
  const layout=await page.evaluate(()=>{
   const form=document.querySelector('#login'),card=form.querySelector('.login-card');
   const rect=card.getBoundingClientRect();
   return {forms:document.querySelectorAll('#login').length,title:card.querySelector('h1').textContent,
    label:card.querySelector('label').textContent,modal:card.getAttribute('aria-modal'),
    centered:Math.abs(rect.left+rect.width/2-innerWidth/2)<2,
    fits:rect.left>=0&&rect.right<=innerWidth&&rect.top>=0&&rect.bottom<=innerHeight};
  });
  assert.deepEqual(layout,{forms:1,title:'Log in',label:'Controller token',modal:'true',centered:true,fits:true},path);
  assert.equal(await page.$eval('#login-error',error=>error.textContent),'',path+' should open without an error');
  if(process.env.VMBOX_LOGIN_SCREENSHOTS)await page.screenshot({path:process.env.VMBOX_LOGIN_SCREENSHOTS+'/'+(path==='/'?'manage':path.split('/')[1])+'.png'});
  await page.setViewport({width:1280,height:900});
  assert.equal(await page.$eval('.login-card',card=>Math.abs(card.getBoundingClientRect().left+card.getBoundingClientRect().width/2-innerWidth/2)<2),true,path+' desktop');
  await page.type('#login-token','wrong-token');
  await page.click('#login button');
  await page.waitForFunction(()=>document.querySelector('#login-error').textContent.length>0);
  assert.equal(await page.$eval('#login',form=>form.hidden),false,path);
  await page.close();
 }
});
