import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile,readdir} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import puppeteer from 'puppeteer-core';

const web=resolve('internal/controller/web');
const id='99999999-9999-4999-8999-999999999999',at='2026-10-02T12:00:00Z';
// multiple is false here on purpose: agents often leave it unset, and the
// owner must still be able to pick several choices.
const question={id,taskId:'task-1',direction:'agent',text:'',state:'delivered',threadId:id,createdAt:at,updatedAt:at,question:{text:'Which areas should we polish?',choices:['Header','Resources','Danger zone'],multiple:false}};

test('chat_ask questions accept several choices even when multiple is unset',async()=>{
 const assets=new Map();
 for(const name of await readdir(web))if(/\.(html|js|css|json)$/.test(name))assets.set(name,await readFile(resolve(web,name)));
 const posts=[];
 const server=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://local'),path=url.pathname;
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(assets.get('chat.html'))}
  const asset=path.slice(1);
  if(assets.has(asset)){res.setHeader('Content-Type',{'.css':'text/css','.json':'application/json','.html':'text/html'}[extname(asset)]||'text/javascript');return res.end(assets.get(asset))}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return res.end('{"role":"owner"}');
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end('[{"id":"builder","name":"Builder","state":"running","defaultAgent":"claude","provider":"railway"}]');
  if(path==='/v1/chat-sidebar-layout')return res.end('{"exists":true,"groups":[],"members":{}}');
  if(path==='/v1/logical-boxes/builder/messages'&&req.method==='POST'){
   let raw='';for await(const chunk of req)raw+=chunk;
   const body=JSON.parse(raw);posts.push(body);
   return res.end(JSON.stringify({message:{id:'66666666-6666-4666-8666-666666666666',taskId:'task-1',direction:'user',text:body.text,state:'delivered',parentMessageId:body.parentMessageId,threadId:id,createdAt:at,updatedAt:at}}));
  }
  if(path==='/v1/logical-boxes/builder/messages')return res.end(JSON.stringify([question]));
  if(path.endsWith('/messages')||['/v1/box-conversations','/v1/tool-presets','/v1/notifications','/v1/chat-commands'].includes(path))return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  return res.end('{}');
 });
 await new Promise(done=>server.listen(0,'127.0.0.1',done));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
  await page.goto(`http://127.0.0.1:${server.address().port}/chat#box=builder`);
  const choice='.msg[data-key="'+id+'"] .question .choice';
  await page.waitForSelector(choice);
  assert.equal(await page.$eval('.msg[data-key="'+id+'"] .question .hint',hint=>!hint.hidden&&hint.textContent),'Pick one or more');
  // Re-query each time: live refreshes may rebuild the transcript.
  const pick=async index=>{const button=(await page.$$(choice))[index];await button.tap()};
  await pick(0);await pick(2);
  assert.deepEqual(await page.$$eval(choice+'.on',items=>items.map(item=>item.dataset.value)),['Header','Danger zone']);
  await pick(2);await pick(1);await pick(2);
  await page.$eval('.msg[data-key="'+id+'"] .question .send',button=>button.click());
  await page.waitForFunction(()=>document.querySelector('.question .send')?.disabled);
  assert.equal(posts.length,1);
  assert.equal(posts[0].text,'Header, Resources, Danger zone');
  assert.equal(posts[0].parentMessageId,id);
 }finally{await browser.close();server.close()}
});
