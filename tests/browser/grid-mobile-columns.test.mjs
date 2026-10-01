import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
test('grid defaults to one phone column and keeps an explicit override',async()=>{
 const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://local').pathname;
  if(path.startsWith('/v1/')){
   res.setHeader('Content-Type','application/json');
   if(path==='/v1/whoami')return res.end('{"role":"owner"}');
   if(path==='/v1/grid-boxes')return res.end('[]');
   if(path==='/v1/browser-session'){res.statusCode=204;return res.end()}
   return res.end('{}');
  }
  const file=resolve(web,path==='/grid'?'grid.html':path.slice(1));
  if(!file.startsWith(web+'/')){res.statusCode=404;return res.end()}
  try{res.setHeader('Content-Type',({'.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.html':'text/html'})[extname(file)]||'application/octet-stream');res.end(await readFile(file))}catch{res.statusCode=404;res.end()}
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  const url=`http://127.0.0.1:${server.address().port}/grid`;await page.goto(url);await page.waitForFunction(()=>!document.querySelector('#grid-app').hidden);
  assert.equal(await page.$eval('#layout [name=columns]',node=>node.value),'1');
  assert.equal(await page.$eval('#tiles',node=>getComputedStyle(node).gridTemplateColumns.split(' ').length),1);
  await page.select('#layout [name=columns]','2');await page.$eval('#layout',node=>node.requestSubmit());
  assert.equal(await page.$eval('#tiles',node=>getComputedStyle(node).gridTemplateColumns.split(' ').length),2);
  await page.reload();await page.waitForFunction(()=>!document.querySelector('#grid-app').hidden);
  assert.equal(await page.$eval('#layout [name=columns]',node=>node.value),'2');
  assert.equal(await page.$eval('#tiles',node=>getComputedStyle(node).gridTemplateColumns.split(' ').length),2);
  await page.close();
 }finally{await browser.close();await new Promise(done=>server.close(done))}
});
