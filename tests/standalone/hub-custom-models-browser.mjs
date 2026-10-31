// Isolated browser fixtures: no Docker resources or inference requests.
import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import puppeteer from 'puppeteer-core';

test('custom endpoint discovers models automatically and preserves declared reasoning on creation',{timeout:30000},async()=>{
 const source=await readFile('internal/hub/page.go','utf8'),picker=await readFile('internal/controller/web/model-picker.js','utf8');
 const html=source.match(/const hubPage = `([\s\S]*)`/)[1].replace('<script>',()=>'<script>'+picker+'</script><script>');
 let catalogRequests=0,created,browser;
 const server=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type',u.pathname==='/'?'text/html':'application/json');
  let body='';for await(const part of req)body+=part;
  if(u.pathname==='/')return res.end(html);
  if(u.pathname==='/api/status')return res.end(JSON.stringify({allocated:{running:0,cpus:0,memoryMiB:0},host:{availableMiB:4096,diskFreeMiB:8192},policy:{maxRunning:2,cpus:1,memoryMiB:2048},defaults:{agent:'opencode',cpu:.25,memoryMiB:512,modelURL:'http://model/v1'}}));
  if(u.pathname==='/api/profiles'||u.pathname==='/api/boxes'&&req.method==='GET')return res.end('[]');
  if(u.pathname==='/api/models'){
   catalogRequests++;
   const plain=u.searchParams.get('endpoint').includes('plain');
   return res.end(JSON.stringify({source:'Custom fixture',models:plain?[{id:'plain-model',label:'Plain model',reasoning:false}]:[{id:'custom-model',label:'Custom model',reasoning:true,reasoningEfforts:['xhigh','medium','low'],context:65536}]}));
  }
  if(u.pathname==='/api/boxes'&&req.method==='POST'){created=JSON.parse(body);res.statusCode=202;return res.end('{"id":"fixture","state":"queued"}')}
  res.statusCode=404;res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{
  browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
  const page=await browser.newPage();page.setDefaultTimeout(10000);await page.goto('http://127.0.0.1:'+server.address().port);
  await page.waitForFunction(()=>document.querySelector('#model-catalog-status').textContent.includes('models discovered'));
  assert(catalogRequests>0,'discovery waited for the model dialog');
  assert.equal(await page.$eval('#create-form [name=modelSource]',e=>e.value),'custom');
  await page.click('.model-picker-open');await page.waitForSelector('.model-picker-option[data-model="custom-model"]');await page.click('.model-picker-option[data-model="custom-model"]');
  assert.deepEqual(await page.$$eval('.model-picker-effort option',nodes=>nodes.map(n=>n.value)),['','xhigh','medium','low']);
  await page.select('.model-picker-effort','medium');await page.click('.model-picker-apply');await page.type('#create-form [name=name]','Disposable reasoning fixture');await page.click('#create-form button.primary');
  await page.waitForFunction(()=>document.querySelector('#status').textContent==='Box queued');
  assert.equal(created.agent,'opencode');assert.equal(created.model,'custom-model');assert.equal(created.modelURL,'http://model/v1');assert.equal(created.agentModel,'');assert.equal(created.agentReasoningEffort,'medium');
  const previous=catalogRequests;
  await page.$eval('#create-form [name=modelURL]',e=>{e.value='http://plain/v1';e.dispatchEvent(new Event('input',{bubbles:true}))});
  await page.waitForFunction(()=>document.querySelector('#model-catalog-status').textContent.includes('models discovered'));
  await new Promise(resolve=>setTimeout(resolve,450));assert(catalogRequests>previous,'typing another endpoint did not refresh models');
  await page.click('.model-picker-open');await page.waitForSelector('.model-picker-option[data-model="plain-model"]');await page.click('.model-picker-option[data-model="plain-model"]');
  assert.equal(await page.$eval('.model-picker-effort',e=>e.disabled),true);await page.click('.model-picker-apply');
  assert.equal(await page.$eval('#create-form [name=agentReasoningEffort]',e=>e.value),'');
 }finally{await browser?.close();await new Promise(resolve=>server.close(resolve))}
});
