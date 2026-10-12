import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import https from 'node:https';
import {execFileSync} from 'node:child_process';
import {mkdtemp, readFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

// Real Chromium, isolated profile, synthetic cookie, and one-use loopback page.
test('Chromium extension exports a selected profile cookie to the local bridge',async t=>{
 const binary=process.env.VMBOX_TEST_CHROMIUM_EXTENSION;
 if(!binary)return t.skip('set VMBOX_TEST_CHROMIUM_EXTENSION for isolated extension test');
 const profile=await mkdtemp(join(tmpdir(),'vbox-disposable-browser-export-'));
 const keyPath=join(profile,'fixture.key'),certPath=join(profile,'fixture.crt');
 execFileSync('openssl',['req','-x509','-newkey','rsa:2048','-nodes','-keyout',keyPath,'-out',certPath,'-days','1','-subj','/CN=localhost','-addext','subjectAltName=IP:127.0.0.1'],{stdio:'ignore'});
 const siteServer=https.createServer({key:await readFile(keyPath),cert:await readFile(certPath)},(_req,res)=>res.end('<script>localStorage.setItem("fixture","synthetic-local-storage")</script>'));
 await new Promise(resolve=>siteServer.listen(0,'127.0.0.1',resolve));
 const extension=resolve('internal/cli/browser_export_extension');
 const token='a'.repeat(64),path='/vbox-browser-export/'+token;
 let received;
 const server=http.createServer(async(req,res)=>{
  if(req.method==='GET'&&req.url===path){res.setHeader('Content-Type','text/html');res.setHeader('Content-Security-Policy',"default-src 'none'; connect-src 'self'");return res.end('<button id="export-browser-profile" disabled>Export and upload</button><p id="export-status"></p>')}
  if(req.method==='POST'&&req.url===path+'/ingest'){
   let body='';for await(const chunk of req)body+=chunk;received=JSON.parse(body);res.statusCode=202;return res.end();
  }
  res.statusCode=404;res.end();
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 let browser;
 try{
  browser=await puppeteer.launch({executablePath:binary,headless:true,userDataDir:profile,args:['--no-sandbox','--ignore-certificate-errors','--disable-extensions-except='+extension,'--load-extension='+extension,'--no-first-run','--no-default-browser-check']});
  const sitePage=await browser.newPage();await sitePage.goto('https://127.0.0.1:'+siteServer.address().port+'/');
  const page=await browser.newPage(),cdp=await page.createCDPSession();
  await cdp.send('Storage.setCookies',{cookies:[
   {name:'session',value:'synthetic-browser-export',url:'https://example.test/',path:'/',secure:true,httpOnly:true},
   {name:'domain-session',value:'synthetic-domain-export',domain:'.example.test',path:'/',secure:true,httpOnly:true}
  ]});
  await page.goto('http://127.0.0.1:'+server.address().port+path);
  await page.waitForFunction(()=>!document.querySelector('#export-browser-profile').disabled,{timeout:15000});
  await page.click('#export-browser-profile');
  await page.waitForFunction(()=>document.querySelector('#export-status').textContent.includes('Sent'),{timeout:15000});
  assert.equal(received.version,2);
  const site=received.origins.find(entry=>entry.origin==='https://example.test');
  assert.equal(site?.cookies.find(cookie=>cookie.name==='session')?.value,'synthetic-browser-export');
  assert.equal(site?.cookies.find(cookie=>cookie.name==='domain-session')?.domain,'.example.test');
  const storage=received.origins.find(entry=>entry.origin==='https://127.0.0.1:'+siteServer.address().port);
  assert.equal(storage?.localStorage.find(entry=>entry.name==='fixture')?.value,'synthetic-local-storage');
 }finally{
  await browser?.close();await new Promise(resolve=>server.close(resolve));await new Promise(resolve=>siteServer.close(resolve));await rm(profile,{recursive:true,force:true});
 }
});
