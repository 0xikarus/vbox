import assert from 'node:assert/strict';
import {test} from 'node:test';
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer-core';

const html=await readFile('internal/controller/web/chat.html','utf8');
const js=await readFile('internal/controller/web/chat.js','utf8');
const motionJS=await readFile('internal/controller/web/motion.js','utf8');
const mascotJS=await readFile('internal/controller/web/mascot.js','utf8');
const mascotCSS=await readFile('internal/controller/web/mascot.css','utf8');
const css=await readFile('internal/controller/web/chat.css','utf8');
const appcss=await readFile('internal/controller/web/app.css','utf8');
const markdownJS=await readFile('internal/controller/web/markdown.js','utf8');
const modelPickerJS=await readFile('internal/controller/web/model-picker.js','utf8');

// Deleting a volume can retry for five minutes while the box finishes its
// current setup step. The box under deletion must be captured when the form is
// submitted: opening the delete dialog for another box meanwhile must never
// retarget the in-flight deletion, or it destroys the wrong workspace volume.
test('an in-flight volume delete cannot be retargeted at another box',async()=>{
 const boxes=[{id:'alpha',name:'alpha',state:'running',defaultAgent:'claude',provider:'railway',role:'owner',volumeName:'v1'},
              {id:'beta',name:'beta',state:'running',defaultAgent:'claude',provider:'railway',role:'owner',volumeName:'v2'}];
 const deleted=[];
 const server=http.createServer((req,res)=>{
  const path=req.url.split('?')[0];
  if(path==='/chat'){res.setHeader('Content-Type','text/html');return res.end(html)}
  if(path==='/chat.js'){res.setHeader('Content-Type','text/javascript');return res.end(js)}
  if(path==='/motion.js'){res.setHeader('Content-Type','text/javascript');return res.end(motionJS)}
  if(path==='/mascot.js'){res.setHeader('Content-Type','text/javascript');return res.end(mascotJS)}
  if(path==='/mascot.css'){res.setHeader('Content-Type','text/css');return res.end(mascotCSS)}
  if(path==='/chat.css'){res.setHeader('Content-Type','text/css');return res.end(css)}
  if(path==='/app.css'){res.setHeader('Content-Type','text/css');return res.end(appcss)}
  if(path==='/markdown.js'){res.setHeader('Content-Type','text/javascript');return res.end(markdownJS)}
  if(path==='/model-picker.js'){res.setHeader('Content-Type','text/javascript');return res.end(modelPickerJS)}
  if(!path.startsWith('/v1/'))return res.end('');
  res.setHeader('Content-Type','application/json');
  if(req.method==='DELETE'&&path.endsWith('/volume')){
   let body='';req.on('data',chunk=>body+=chunk);
   // never succeeds, so the client stays in its retry loop for the whole test
   return req.on('end',()=>{deleted.push(path.split('/')[3]);res.statusCode=409;res.end(JSON.stringify({error:'box creation is still active'}))});
  }
  if(path==='/v1/whoami')return res.end(JSON.stringify({role:'owner'}));
  if(path==='/v1/grid-boxes'||path==='/v1/logical-boxes')return res.end(JSON.stringify(boxes));
  if(path==='/v1/tool-presets')return res.end('[]');
  if(path==='/v1/push/vapid-key'){res.statusCode=404;return res.end('{}')}
  if(path.endsWith('/messages'))return res.end('[]');
  return res.end('{}');
 });
 await new Promise(resolve=>server.listen(0,resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:'new',args:['--no-sandbox']});
 try{
  const p=await browser.newPage();
  await p.setViewport({width:420,height:900});
  await p.goto(base+'/chat',{waitUntil:'networkidle0'});
  await p.waitForSelector('#chat-entries li',{timeout:8000});
  const openDeleteFor=name=>p.evaluate(boxName=>{
   const row=[...document.querySelectorAll('#chat-entries li')].find(el=>el.textContent.includes(boxName));
   const rect=row.getBoundingClientRect();
   row.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,clientX:rect.left+20,clientY:rect.top+10}));
   [...document.querySelectorAll('#row-menu button')].find(b=>b.textContent.startsWith('Delete box')).click();
  },name);

  await openDeleteFor('alpha');
  await p.evaluate(()=>document.getElementById('delete-box-form').requestSubmit());
  await p.waitForFunction(()=>document.getElementById('delete-box-status').textContent.startsWith('Waiting for'),{timeout:8000});

  // the user dismisses the dialog and opens the one for another box, without submitting it
  await p.keyboard.press('Escape');
  await openDeleteFor('beta');
  const seen=deleted.length;
  await new Promise(resolve=>setTimeout(resolve,4000));

  assert.ok(deleted.length>seen,'the retry loop must still be running');
  assert.deepEqual([...new Set(deleted)],['alpha'],'every retry must stay on the box whose deletion was submitted');
  await p.close();
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
