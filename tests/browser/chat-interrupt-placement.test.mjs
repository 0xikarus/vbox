import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

test('interrupt control sits beside Send in the chat composer',async()=>{
 const html=await readFile('internal/controller/web/chat.html','utf8');
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage','--disable-gpu']});
 try{
  const page=await browser.newPage();
  const placement=await page.evaluate(source=>{
   const doc=new DOMParser().parseFromString(source,'text/html');
   const interrupt=doc.querySelector('#chat-interrupt');
   return {parent:interrupt?.parentElement?.id,next:interrupt?.nextElementSibling?.id,type:interrupt?.getAttribute('type'),label:interrupt?.getAttribute('aria-label')};
  },html);
  assert.deepEqual(placement,{parent:'chat-composer',next:'send',type:'button',label:'Interrupt agent'});
 }finally{await browser.close()}
});
