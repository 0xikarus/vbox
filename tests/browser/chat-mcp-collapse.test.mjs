import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','motion.js','mascot.js','mascot.css','chat.css','vbox-c.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const box={id:'mcp-fixture',name:'MCP fixture',state:'running',defaultAgent:'codex'};
const at=index=>new Date(Date.UTC(2026,8,30,10,index)).toISOString();
const message=(id,direction,text,index)=>({id,direction,text,state:'delivered',createdAt:at(index),updatedAt:at(index)});

test('consecutive MCP calls collapse into a divider and retain their open state on refresh',async()=>{
 let messages=[
  message('user-1','user','Please inspect the box',0),
  message('mcp-1','system','MCP · chat_message · contact',1),
  message('mcp-2','system','MCP · take_screenshot · failed',2),
  message('agent-1','agent','Inspection complete',3),
  message('mcp-3','system','MCP · get_run_budget',4),
 ];
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/mcp-fixture/messages')return res.end(JSON.stringify(messages));
  if(path==='/v1/tool-presets'||path==='/v1/agent-roles')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=mcp-fixture');
  await page.waitForSelector('.mcp-call-group');
  assert.equal(await page.$$('.mcp-call-group').then(groups=>groups.length),2);
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-toggle',button=>button.getAttribute('aria-expanded')),'false');
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-list',list=>getComputedStyle(list).display),'none');
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-count',count=>count.textContent),'2');
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-failures',badge=>badge.textContent),'1 failed');
  await page.screenshot({path:'/tmp/vmbox-mcp-collapsed.png'});
  await page.click('.mcp-call-group .mcp-call-toggle');
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-toggle',button=>button.getAttribute('aria-expanded')),'true');
  assert.equal(await page.$eval('.mcp-call-group .mcp-call-list',list=>list.textContent.includes('Message to contact')&&list.textContent.includes('Take screenshot')),true);
  assert.equal(await page.$eval('.mcp-call-item[data-tool="chat_message"] .mcp-call-icon svg',icon=>icon.querySelector('path')!==null),true);
  assert.equal(await page.$eval('.mcp-call-item[data-tool="take_screenshot"] .mcp-call-failed',badge=>badge.textContent),'Failed');
  await page.screenshot({path:'/tmp/vmbox-mcp-expanded.png'});
  await page.click('#refresh');
  await page.waitForFunction(()=>document.querySelector('.mcp-call-group .mcp-call-toggle')?.getAttribute('aria-expanded')==='true');
  messages=[...messages,message('mcp-4','system','MCP · take_screenshot',5)];
  await page.click('#refresh');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mcp-call-count')].at(-1)?.textContent==='2');
  assert.equal(await page.$$eval('.mcp-call-group .mcp-call-toggle',buttons=>buttons.at(-1).getAttribute('aria-expanded')),'false');
  await page.$$eval('.mcp-call-group .mcp-call-toggle',buttons=>buttons.at(-1).click());
  messages=[...messages,message('mcp-5','system','MCP · get_contacts',6)];
  await page.click('#refresh');
  await page.waitForFunction(()=>[...document.querySelectorAll('.mcp-call-count')].at(-1)?.textContent==='3');
  assert.equal(await page.$$eval('.mcp-call-group .mcp-call-toggle',buttons=>buttons.at(-1).getAttribute('aria-expanded')),'true');
  await page.setViewport({width:390,height:844,deviceScaleFactor:1,isMobile:true,hasTouch:true});
  assert.equal(await page.$eval('#chat-messages',element=>element.scrollWidth<=element.clientWidth),true,'MCP dividers fit the mobile chat width');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
