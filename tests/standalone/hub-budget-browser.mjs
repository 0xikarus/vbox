import assert from 'node:assert/strict';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {test} from 'node:test';
import puppeteer from 'puppeteer-core';
test('budget sliders keep exact numeric settings, validate host totals and save only policy fields',async()=>{
 const source=await readFile('internal/hub/page.go','utf8'),picker=await readFile('internal/controller/web/model-picker.js','utf8');
 const html=source.match(/const hubPage = `([\s\S]*)`/)[1].replace('<script>',()=>'<script>'+picker+'</script><script>');let saved,browser;
 const policy={cpus:4,memoryMiB:8000,reserveCPUs:4,reserveMemoryMiB:4000,modelMemoryMiB:8000,minDiskFreeMiB:4096,maxRunning:2};
 const server=http.createServer(async(req,res)=>{let body='';for await(const part of req)body+=part;res.setHeader('Content-Type',req.url==='/'?'text/html':'application/json');if(req.url==='/')return res.end(html);if(req.url==='/api/status')return res.end(JSON.stringify({host:{cpus:16,memoryMiB:32768,availableMiB:20000,diskFreeMiB:50000},allocated:{running:0,cpus:0,memoryMiB:0},policy,defaults:{agent:'claude',cpu:.5,memoryMiB:512}}));if(req.url==='/api/policy'){saved=JSON.parse(body);return res.end('{}')}res.end('[]')});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 try{
 browser=await puppeteer.launch({executablePath:'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});const page=await browser.newPage();await page.goto('http://127.0.0.1:'+server.address().port);await page.waitForSelector('[data-budget=memoryMiB]');await page.$eval('#policy-form',e=>e.closest('details').open=true);
 assert.equal(await page.$eval('#policy-form [name=memoryMiB]',e=>e.value),'8000');
 await page.$eval('[data-budget=memoryMiB]',e=>{e.value='8192';e.dispatchEvent(new Event('input',{bubbles:true}))});assert.equal(await page.$eval('#policy-form [name=memoryMiB]',e=>e.value),'8192');assert(await page.$eval('#budget-summary',e=>e.textContent.includes('GiB RAM planned')));
 await page.$eval('#policy-form [name=cpus]',e=>{e.value='16';e.dispatchEvent(new Event('input',{bubbles:true}))});assert.equal(await page.$eval('#policy-form button',e=>e.disabled),true);
 await page.$eval('[data-budget=cpus]',e=>{e.value='4';e.dispatchEvent(new Event('input',{bubbles:true}))});await page.click('#policy-form button');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Budget saved');assert.deepEqual(saved,{...policy,memoryMiB:8192});
 await page.setViewport({width:390,height:844});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
 }finally{await browser?.close();await new Promise(r=>server.close(r))}
});
