import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

test('owner can read a separate two-way box conversation with its image',async()=>{
 const [html,js,css]=await Promise.all(['box-chats.html','box-chats.js','box-chats.css'].map(name=>readFile('internal/controller/web/'+name,'utf8')));
 const a='11111111-1111-4111-8111-111111111111',b='22222222-2222-4222-8222-222222222222',now=new Date().toISOString();
 const pixel=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC','base64');
 const server=http.createServer((request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/box-chats')return response.end(html);
  if(path==='/box-chats.js'){response.setHeader('Content-Type','text/javascript');return response.end(js)}
  if(path==='/box-chats.css'){response.setHeader('Content-Type','text/css');return response.end(css)}
  if(path.startsWith('/v1/messages/')){response.setHeader('Content-Type','image/png');return response.end(pixel)}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/box-conversations')return response.end(JSON.stringify([{boxAId:a,boxBId:b,boxAName:'Builder',boxBName:'Reviewer',lastAt:now,lastText:'The review is ready'}]));
  if(path==='/v1/box-conversations/'+a+'/'+b+'/messages')return response.end(JSON.stringify([
   {id:'m1',senderBoxId:a,recipientBoxId:b,direction:'box',text:'Please inspect this image',state:'delivered',createdAt:now,images:[{id:'image-1',number:1,mediaType:'image/png'}]},
   {id:'m2',senderBoxId:b,recipientBoxId:a,direction:'box',text:'The review is ready',state:'delivered',createdAt:now}
  ]));
  response.statusCode=404;response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844});
  await page.goto('http://127.0.0.1:'+server.address().port+'/box-chats');
  await page.waitForSelector('#conversations button');await page.click('#conversations button');
  await page.waitForFunction(()=>document.querySelectorAll('#messages article').length===2);
  assert.equal(await page.$eval('#title',element=>element.textContent),'Builder ↔ Reviewer');
  assert.deepEqual(await page.$$eval('#messages article strong',elements=>elements.map(element=>element.textContent)),['Builder','Reviewer']);
  assert.equal(await page.$eval('#messages img',image=>image.naturalWidth),1);
  assert.equal(await page.$eval('#transcript',element=>getComputedStyle(element).display),'flex');
  const sides=await page.$$eval('#messages article',elements=>elements.map(element=>({side:element.dataset.side,left:element.getBoundingClientRect().left,right:element.getBoundingClientRect().right})));
  assert.deepEqual(sides.map(message=>message.side),['left','right']);
  assert.ok(sides[0].left<sides[1].left&&sides[0].right<sides[1].right,'the two boxes must occupy opposite sides of the transcript');
  await page.click('#back');assert.equal(await page.$eval('#transcript',element=>getComputedStyle(element).display),'none');
  await page.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
