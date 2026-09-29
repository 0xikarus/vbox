import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));

test('chat details shows the last applied instruction time and pending edits',async()=>{
 const box={id:'builder',name:'Builder',state:'running',defaultAgent:'claude'};
 let instructions={instructions:{appliedAt:'2026-09-05T12:00:00Z'},pending:true};
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat')return res.end(assets['chat.html']);
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/builder/instructions')return res.end(JSON.stringify(instructions));
  if(path.endsWith('/messages')||path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=builder');
  await page.waitForSelector('#chat-info:not([hidden])');
  await page.click('#chat-info');
  await page.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('changes pending'));
  assert.match(await page.$eval('#inspect-activity-rows',el=>el.textContent),/Last instructions sync.*2026.*changes pending/);
  await page.click('#inspect-close');
  instructions={instructions:{},pending:true};
  await page.click('#chat-info');
  await page.waitForFunction(()=>document.querySelector('#inspect-activity-rows')?.textContent.includes('Never · changes pending'));
  assert.match(await page.$eval('#inspect-activity-rows',el=>el.textContent),/Last instructions syncNever · changes pending/);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
