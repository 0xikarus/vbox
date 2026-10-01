import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
test('mobile workspace and takeover key strips scroll to their final key',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path==='/'){res.setHeader('Content-Type','text/html');return res.end('<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/vbox-tokens.css"><link rel="stylesheet" href="/workspace.css"><link rel="stylesheet" href="/vbox-c.css"><div class="workspace-body"><div class="terminal-keys"></div><div id="terminal-screen" style="width:350px;height:120px"></div></div><div id="takeover-controls"><div id="takeover-scroll"></div><div id="takeover-pinned"></div></div><div id="takeover-screen" style="width:350px;height:120px"></div><script src="/xterm.js"></script><script src="/xterm-fit.js"></script><script src="/workspace-terminal.js"></script>')};
  const file=resolve(web,path.slice(1));if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.evaluateOnNewDocument(()=>{window.WebSocket=class{static OPEN=1;readyState=0;bufferedAmount=0;constructor(){}send(){}close(){this.readyState=3}}});
  await page.goto(`http://127.0.0.1:${server.address().port}/`);await page.waitForFunction(()=>!!window.openWorkspaceTerminal,{timeout:5000});
  await page.evaluate(()=>{window.disposeWorkspace=openWorkspaceTerminal('builder','agent',()=>{},{root:document.querySelector('#terminal-screen'),keys:document.querySelector('.terminal-keys'),autoFocus:false})});
  await page.waitForFunction(()=>{const cue=document.querySelector('.workspace-body .term-key-scroll-cue');return cue&&!cue.hidden});
  let data=await page.$eval('.workspace-body .terminal-keys',node=>({left:node.scrollLeft,max:node.scrollWidth-node.clientWidth}));assert(data.max>0);assert.equal(data.left,0);
  await page.$eval('.workspace-body .term-key-scroll-cue',node=>node.click());await page.waitForFunction(()=>document.querySelector('.workspace-body .terminal-keys').scrollLeft>0);
  await page.$eval('.workspace-body .terminal-keys',node=>node.scrollLeft=node.scrollWidth);
  data=await page.$eval('.workspace-body .terminal-keys',node=>{const last=[...node.querySelectorAll('button')].find(button=>button.textContent==='→'),pinned=node.querySelector('.term-key-scroll-cue');return {lastRight:last.getBoundingClientRect().right,cueLeft:pinned.getBoundingClientRect().left,atEnd:node.scrollLeft+node.clientWidth>=node.scrollWidth-2}});
  assert(data.atEnd);assert(data.lastRight<=data.cueLeft+1,JSON.stringify(data));
  await page.evaluate(()=>{window.disposeTakeover=openWorkspaceTerminal('builder','agent',()=>{},{root:document.querySelector('#takeover-screen'),keys:document.querySelector('#takeover-scroll'),pinnedKeys:document.querySelector('#takeover-pinned'),autoFocus:false})});
  await page.waitForFunction(()=>{const cue=document.querySelector('#takeover-scroll .term-key-scroll-cue');return cue&&!cue.hidden});
  assert.equal(await page.$$eval('#takeover-pinned button',buttons=>buttons.length),2);
  await page.$eval('#takeover-scroll .term-key-scroll-cue',node=>node.click());await page.waitForFunction(()=>document.querySelector('#takeover-scroll').scrollLeft>0);
  await page.evaluate(()=>{window.disposeWorkspace();window.disposeTakeover()});await page.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
