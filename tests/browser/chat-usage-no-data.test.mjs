import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const files=Object.fromEntries(await Promise.all(['chat.html','chat.js','workspace-nav.js','motion.js','mascot.js','mascot.css','chat.css','vbox-tokens.css','vbox-c.css','app.css','markdown.js','model-picker.js'].map(async name=>[name,await readFile('internal/controller/web/'+name)])));
const boxes=[
 {id:'live',name:'Live',state:'running',defaultAgent:'claude'},
 {id:'ghost',name:'Ghost',state:'running',defaultAgent:'claude'},
 {id:'unlinked',name:'Unlinked',state:'running',defaultAgent:'codex'},
 {id:'shell',name:'Shell',state:'running',defaultAgent:'shell'}
];
const refs={live:[{application:'claude',name:'work'}],ghost:[{application:'claude',name:'deleted'}],unlinked:[],shell:[]};
const usage={profiles:[{application:'claude',name:'work',boxes:['Live'],observedAt:'2026-10-01T08:00:00Z',snapshot:{source:'live box',windows:[{name:'session',usedPercent:30}]}}]};

test('chat usage chip says No usage data when the box profile is missing',async()=>{
 const server=http.createServer((request,response)=>{
  const path=request.url.split('?')[0];
  if(path==='/chat'){response.setHeader('Content-Type','text/html');return response.end(files['chat.html'])}
  if(files[path.slice(1)]){response.setHeader('Content-Type',path.endsWith('.css')?'text/css':'text/javascript');return response.end(files[path.slice(1)])}
  response.setHeader('Content-Type','application/json');
  if(path==='/v1/whoami')return response.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return response.end(JSON.stringify(boxes));
  const imported=path.match(/^\/v1\/logical-boxes\/([^/]+)\/imported-credentials$/);
  if(imported)return response.end(JSON.stringify({profiles:refs[imported[1]]||[],pending:[],verified:true}));
  if(path==='/v1/profile-usage')return response.end(JSON.stringify(usage));
  if(path==='/v1/instruction-presets')return response.end(JSON.stringify({defaultName:'',presets:[]}));
  if(path.endsWith('/messages')||['/v1/box-conversations','/v1/tool-presets','/v1/chat-commands','/v1/login-profiles','/v1/provider-credentials'].includes(path))return response.end('[]');
  if(path==='/v1/push/vapid-key'){response.statusCode=404;return response.end('{}')}
  response.end('{}');
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/usr/bin/chromium',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
 try{
  const page=await browser.newPage();await page.setViewport({width:1200,height:800});
  const chip=async id=>{
   await page.goto('http://127.0.0.1:'+server.address().port+'/chat#box='+id);
   await page.waitForFunction(id=>document.querySelector('#chat-header-name')?.textContent===({live:'Live',ghost:'Ghost',unlinked:'Unlinked',shell:'Shell'})[id],{},id);
   await new Promise(resolve=>setTimeout(resolve,600));
   return page.$eval('#chat-usage',button=>({hidden:button.hidden,text:button.querySelector('.chat-usage-value').textContent,none:button.classList.contains('usage-none'),title:button.title,ring:getComputedStyle(button.querySelector('.chat-usage-ring')).display}));
  };
  const live=await chip('live');
  assert.equal(live.hidden,false);assert.equal(live.text,'70%');assert.equal(live.none,false);
  const ghost=await chip('ghost');
  assert.deepEqual([ghost.hidden,ghost.text,ghost.none,ghost.ring],[false,'No usage data',true,'none'],'deleted profile shows a no-data chip');
  assert.match(ghost.title,/claude · deleted no longer exists/);
  await page.click('#chat-usage');
  assert.equal(await page.$eval('#usage-modal',modal=>modal.hidden),true,'no-data chip does not open a scoped usage sheet');
  const unlinked=await chip('unlinked');
  assert.deepEqual([unlinked.hidden,unlinked.text],[false,'No usage data']);
  assert.match(unlinked.title,/No login profile is linked/);
  assert.equal((await chip('shell')).hidden,true,'shell boxes have no usage chip');
 }finally{await browser.close();server.close()}
});
