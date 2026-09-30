import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const terminalScript=await readFile('internal/controller/web/workspace-terminal.js','utf8');
const desktopScript=await readFile('internal/controller/web/workspace-desktop.js','utf8');

test('workspace clipboard controls move text through TMUX and Desktop',async()=>{
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox']});
 try{
  const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.setContent('<p id="status"></p><div class="terminal-keys"></div><div id="terminal-screen"></div><div id="desktop-controls"></div><div id="desktop-screen"></div>');
  await page.evaluate(()=>{
   window.clipboardText='from browser';window.clipboardWrites=[];
   window.URL=class{constructor(path){this.href='http://localhost'+path;this.protocol='http:';this.searchParams={set(){}}}};
   Object.defineProperty(navigator,'clipboard',{configurable:true,value:{readText:async()=>window.clipboardText,writeText:async value=>window.clipboardWrites.push(value)}});
   window.ResizeObserver=class{observe(){}disconnect(){}};
   window.WebSocket=class{static OPEN=1;readyState=1;bufferedAmount=0;send(){}close(){}};
   window.FitAddon={FitAddon:class{fit(){}}};
   window.Terminal=class{
    constructor(){window.fakeTerminal=this}
    loadAddon(){}open(){}focus(){}dispose(){}
    onData(fn){this.input=fn;return{dispose(){}}}onBinary(){return{dispose(){}}}onResize(){return{dispose(){}}}
    getSelection(){return this.selection||''}paste(value){this.pasted=value}
    get cols(){return 80}get rows(){return 24}
   };
   window.NoVNC={default:class extends EventTarget{
    constructor(){super();this.keys=[];window.fakeRFB=this}
    clipboardPasteFrom(value){this.pasted=value}focus(){}disconnect(){}sendKey(...args){this.keys.push(args)}sendCtrlAltDel(){}
   }};
  });
  await page.addScriptTag({content:terminalScript});
  await page.addScriptTag({content:desktopScript});
  await page.evaluate(()=>{
   window.closeTerm=openWorkspaceTerminal('test','shell',message=>document.querySelector('#status').textContent=message);
   fakeTerminal.selection='from terminal';
  });
  await page.click('.terminal-keys button:nth-child(1)');
  await page.click('.terminal-keys button:nth-child(2)');
  assert.deepEqual(await page.evaluate(()=>({writes:clipboardWrites,pasted:fakeTerminal.pasted})),{writes:['from terminal'],pasted:'from browser'});

  await page.evaluate(()=>{
   window.closeDesk=openWorkspaceDesktop('test',message=>document.querySelector('#status').textContent=message);
   fakeRFB.dispatchEvent(new CustomEvent('clipboard',{detail:{text:'from desktop'}}));
  });
  await page.click('#desktop-controls [data-action="copy"]');
  await page.evaluate(()=>{window.clipboardText='to desktop'});
  await page.click('#desktop-controls [data-action="paste"]');
  assert.deepEqual(await page.evaluate(()=>({writes:clipboardWrites,pasted:fakeRFB.pasted})),{writes:['from terminal','from desktop'],pasted:'to desktop'});
  assert.equal(await page.$$eval('#desktop-controls [data-action="takeover"], #desktop-controls [data-action="resume"]',buttons=>buttons.length),0);
  assert.match(await page.$eval('#status',element=>element.textContent),/Use Ctrl\+V/);
  await page.evaluate(()=>{navigator.clipboard.readText=async()=>{throw Error('denied')};navigator.clipboard.writeText=async()=>{throw Error('denied')}});
  for(const selector of ['#desktop-controls [data-action="copy"]','#desktop-controls [data-action="paste"]','.terminal-keys button:nth-child(1)','.terminal-keys button:nth-child(2)']){
   await page.click(selector);
   assert.match(await page.$eval('#status',element=>element.textContent),/blocked by the browser/);
  }
  await page.evaluate(()=>{
   closeDesk();closeTerm();
   const scroll=document.createElement('div'),pinned=document.createElement('div'),typeBar=document.createElement('div');
   scroll.id='viewer-scroll';pinned.id='viewer-pinned';typeBar.id='viewer-type';document.body.append(scroll,pinned,typeBar);
   window.closeViewerDesk=openWorkspaceDesktop('test',()=>{},{root:document.querySelector('#desktop-screen'),controls:scroll,typeBar});
  });
  assert.equal(await page.$eval('#viewer-type',node=>node.hidden),true,'desktop keyboard field starts closed');
  await page.click('#viewer-scroll [data-action="type"]');
  await page.type('#viewer-type input','Hi');
  await page.click('#viewer-type button');
  assert.deepEqual(await page.evaluate(()=>fakeRFB.keys.map(args=>args[0])),[72,105],'Type sends text when submitted');
  await page.evaluate(()=>{
   closeViewerDesk();
   window.closeViewerTerm=openWorkspaceTerminal('test','shell',()=>{},{root:document.querySelector('#terminal-screen'),keys:document.querySelector('#viewer-scroll'),pinnedKeys:document.querySelector('#viewer-pinned'),autoFocus:false});
  });
  assert.equal(await page.$$eval('#viewer-pinned button',buttons=>buttons.length),2,'Keyboard and Fullscreen stay outside the scroll strip');
  assert.equal(await page.$$eval('#viewer-scroll .term-key-pinned',buttons=>buttons.length),0);
  await page.evaluate(()=>closeViewerTerm());
  assert.deepEqual(errors,[]);
 }finally{await browser.close()}
});
