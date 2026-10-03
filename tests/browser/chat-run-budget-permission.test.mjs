import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const assets=Object.fromEntries(await Promise.all(['chat.html','chat.js','chat.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name,'utf8')])));
const box={id:'budget-fixture',name:'budget-fixture',state:'running',defaultAgent:'codex'};

test('run-budget admin tool is selectable and saves its typed grant',async()=>{
 let saved;
 const server=http.createServer(async(req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets['chat.html'])}
  if(assets[path.slice(1)]){res.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return res.end(assets[path.slice(1)])}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify([box]));
  if(path==='/v1/logical-boxes/budget-fixture/messages')return res.end('[]');
  if(path==='/v1/logical-boxes/budget-fixture/agent-policy'){
   if(req.method==='PUT'){let body='';for await(const chunk of req)body+=chunk;saved=JSON.parse(body)}
   return res.end(JSON.stringify({boxId:box.id,boxName:box.name,capabilities:saved?.capabilities||{}}));
  }
  if(path==='/v1/tool-presets'||path==='/v1/agent-roles')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box=budget-fixture');
  await page.waitForFunction(()=>!document.querySelector('#roles-toggle')?.hidden);
  await page.$eval('#roles-toggle',button=>button.click());
  await page.waitForSelector('.role-assignment-toggle');
  await page.click('.role-assignment-toggle');
  await page.waitForFunction(()=>!document.querySelector('#role-editor-inline').hidden&&document.querySelector('#role-editor-status').textContent==='');
  const selector='#role-editor-form input[name=mcpTools][value=set_agent_box_run_budget]';
  assert.equal(await page.$eval(selector,input=>input.checked),false);
  await page.$eval(selector,input=>input.click());
  await page.waitForFunction(()=>document.querySelector('#role-editor-status').textContent==='Saved');
  assert.equal(saved.capabilities.manageAgentBoxes.restart,true);
  assert.equal(saved.capabilities.mcpTools.allowedTools.includes('set_agent_box_run_budget'),true);
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
