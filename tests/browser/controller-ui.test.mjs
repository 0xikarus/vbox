import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import http from 'node:http';
import {after,before,test} from 'node:test';
import {resolve} from 'node:path';
import puppeteer from 'puppeteer-core';

const root=resolve('internal/controller/web'),requests=[];
let server,browser,base;
const revision='2026-09-05T12:00:00Z';
before(async()=>{
 server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://test').pathname;
  const chunks=[];for await(const chunk of req)chunks.push(chunk);
  const body=chunks.length?JSON.parse(Buffer.concat(chunks)):null;
  requests.push({path,method:req.method,body,revision:req.headers['if-match']});
  if(['/','/app.js','/app.css','/run-once.js','/favicon.ico','/workspace.js','/workspace-terminal.js','/workspace-desktop.js','/novnc.js','/workspace.css','/xterm.js','/xterm-fit.js','/xterm.css','/boxes/box-1'].includes(path)){
   const file=path==='/boxes/box-1'?'workspace.html':path==='/'?'index.html':path==='/favicon.ico'?'favicon.svg':path.slice(1);
   res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':'text/html');
   return res.end(await readFile(resolve(root,file)));
  }
  res.setHeader('Content-Type','application/json');
  if(path==='/v1/browser-session' && ['POST','DELETE'].includes(req.method)){res.statusCode=204;return res.end()}
  const values={
   '/v1/run-once':[],
   '/v1/tool-presets':[{id:'foundry',name:'Foundry',version:'v1.8.1',description:'forge, cast, anvil, chisel'},{id:'blender',name:'Blender',version:'distribution package',description:'3D editor + desktop automatically enabled; MCP not included'}],
   '/v1/capabilities':{providerEdits:true,nativeAttach:true},
   '/v1/logical-boxes':[{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude'}],
   '/v1/logical-boxes/box-1':{id:'box-1',name:'helper ü',state:'running'},
   '/v1/logical-boxes/box-1/run-once':null,
   '/v1/provider-credentials':[{provider:'railway',name:'primary',config:{projectId:'p',environmentId:'e',image:'old'},updatedAt:revision}],
   '/v1/provider-schemas':{providers:{railway:{image:'string'}}},
   '/v1/controller-defaults':{provider:'railway',providerCredential:'primary'},
   '/v1/fleet/status':{desiredSlots:2,actualSlots:2,freeSlots:1,occupiedSlots:1,unhealthySlots:0,slots:[{ordinal:1,state:'occupied',health:'healthy',region:'europe-west4',logicalBoxName:'helper ü'},{ordinal:2,state:'free',health:'healthy',region:'europe-west4'}]},
   '/v1/fleet/costs':{provider:'railway',providerCredential:'primary',period:'current provider billing period',observedAt:revision,total:{currency:'USD',accrued:1.23,available:true,detail:'Sum of available fleet service costs.'},availableSlotCount:1,unavailableSlotCount:1,slots:[{ordinal:1,state:'occupied',logicalBoxName:'helper ü',cost:{currency:'USD',accrued:1.23,available:true,detail:'Railway service entries'}},{ordinal:2,state:'free',cost:{currency:'USD',available:false,detail:'Project token cannot read billing'}}]},
   '/v1/notifications':[],
   '/v1/whoami':{accountId:'account-1',accountName:'Team'},
   '/v1/login-profiles':[{application:'claude',name:'personal',createdAt:revision}],
  };
  if(req.method==='GET' && path in values)return res.end(JSON.stringify(values[path]));
  if(req.method==='POST' && path==='/v1/logical-boxes/box-1/sessions/interactive')return res.end(JSON.stringify({session:'persistent-shell'}));
  if(req.method==='PATCH' && (path==='/v1/logical-boxes/box-1'||path==='/v1/provider-credentials/railway/primary'))return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/fleet/slots')return res.end(JSON.stringify(body));
  if(req.method==='PUT' && path==='/v1/login-profiles/codex/browser-test')return res.end(JSON.stringify({application:'codex',name:'browser-test'}));
  if(req.method==='POST' && path==='/v1/logical-boxes')return res.end(JSON.stringify({id:'created'}));
  if(req.method==='DELETE' && path==='/v1/login-profiles/claude/personal'){res.statusCode=204;return res.end()}
  res.statusCode=404;res.end(JSON.stringify({error:'unexpected endpoint'}));
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));base='http://127.0.0.1:'+server.address().port;
 browser=await puppeteer.launch({executablePath:process.env.VMBOX_CHROMIUM||'/snap/bin/chromium',headless:true,args:['--no-sandbox','--disable-setuid-sandbox']});
});
after(async()=>{await browser?.close();await new Promise(r=>server?.close(r))});
test('box deletion confirms exact identity, prevents repeats and shows asynchronous progress',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.deleteCalls=[];window.deleteState='running';
  window.fetch=async(path,options={})=>{
   if(path==='/v1/logical-boxes'&&(!options.method||options.method==='GET'))return new Response(JSON.stringify([
    ...(window.deleteState==='gone'?[]:[{id:'box-1',name:'helper ü',state:window.deleteState,defaultAgent:'shell',restorationState:window.deleteState==='deleting'?'delete-detaching-volume':'restored'}]),
    {id:'sibling',name:'keep-me',state:'hibernated',defaultAgent:'shell'}
   ]));
   if(path==='/v1/logical-boxes/box-1/volume'&&options.method==='DELETE'){window.deleteCalls.push({path,body:JSON.parse(options.body)});window.deleteState='deleting';return new Response(JSON.stringify({id:'box-1',state:'deleting'}),{status:202})}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('[data-box-id="box-1"] button');
 page.once('dialog',d=>{assert.match(d.message(),/helper ü/);assert.match(d.message(),/permanently deleted/);d.dismiss()});await page.click('[data-box-id="box-1"] button');assert.equal(await page.evaluate(()=>window.deleteCalls.length),0);
 page.once('dialog',d=>d.accept());await page.click('[data-box-id="box-1"] button');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="box-1"]').textContent.includes('delete-detaching-volume'));
 assert.equal(await page.$eval('[data-box-id="box-1"] button',b=>b.disabled),true);assert.equal(await page.$('[data-box-id="box-1"] a'),null);
 assert.deepEqual(await page.evaluate(()=>window.deleteCalls),[{path:'/v1/logical-boxes/box-1/volume',body:{confirmation:'helper ü'}}]);
 await page.evaluate(()=>window.deleteState='gone');await page.waitForFunction(()=>!document.querySelector('[data-box-id="box-1"]'),{timeout:10000});
 assert.ok(await page.$('[data-box-id="sibling"]'));assert.deepEqual(errors,[]);await page.close();
});
test('mobile box deletion reports rejection without hiding the box or replaying deletion',async()=>{
 const page=await browser.newPage();await page.setViewport({width:390,height:844});
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.deleteCalls=0;
  window.fetch=async(path,options={})=>{if(path==='/v1/logical-boxes/box-1/volume'&&options.method==='DELETE'){window.deleteCalls++;return new Response(JSON.stringify({error:'Creation is still active; try again after it finishes.'}),{status:409})}return original(path,options)};
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('[data-box-id="box-1"] button');
 page.once('dialog',d=>d.accept());await page.click('[data-box-id="box-1"] button');await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('Creation is still active'));
 assert.equal(await page.$eval('[data-box-id="box-1"] button',b=>b.disabled),false);assert.equal(await page.evaluate(()=>window.deleteCalls),1);assert.ok(await page.$('[data-box-id="box-1"]'));await page.close();
});
test('Run once queues once, keeps its key on retry and permits cancellation',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.runCalls=[];let state='queued';
  window.fetch=async(path,options={})=>{
   if(!String(path).startsWith('/v1/run-once'))return original(path,options);
   window.runCalls.push({path,options});
   if(String(path).endsWith('/cancel'))state='cancelled';
   const run={id:'queue-fixture',request:{agent:'shell',prompt:'printf unique'},state};
   return new Response(JSON.stringify(path==='/v1/run-once'&&(!options.method||options.method==='GET')?[]:run),{headers:{'Content-Type':'application/json'}});
  };
 });
 await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#run-once select[name=provider] option');
 await page.type('#run-once textarea[name=prompt]','printf unique');await page.click('#run-once form button');
 await page.waitForFunction(()=>document.querySelector('#run-once-content').textContent.includes('Waiting for a healthy free slot'));
 await page.click('#run-once form button');
 const calls=await page.evaluate(()=>window.runCalls.filter(c=>c.path==='/v1/run-once'&&c.options.method==='POST'));
 assert.equal(calls.length,2);assert.equal(calls[0].options.headers['Idempotency-Key'],calls[1].options.headers['Idempotency-Key']);
 await page.evaluate(()=>[...document.querySelectorAll('#run-once button')].find(b=>b.textContent==='Cancel queued run').click());
 await page.waitForFunction(()=>document.querySelector('#run-once-content').textContent.includes('cancelled'));
 await page.evaluate(()=>[...document.querySelectorAll('#run-once button')].find(b=>b.textContent==='Start another run').click());
 await page.click('#run-once form button');
 await page.waitForFunction(()=>window.runCalls.filter(c=>c.path==='/v1/run-once'&&c.options.method==='POST').length===3);
 const lastKey=await page.evaluate(()=>window.runCalls.filter(c=>c.path==='/v1/run-once'&&c.options.method==='POST').at(-1).options.headers['Idempotency-Key']);
 assert.notEqual(lastKey,calls[0].options.headers['Idempotency-Key']);
 assert.deepEqual(errors,[]);await page.close();
});
test('table previews stay fixed size and tool choices stay compact',async()=>{
 for(const width of [1280,390]){
  const page=await browser.newPage();await page.setViewport({width,height:844});await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#box-list .table-text');
  const result=await page.evaluate(()=>{
   const row=document.querySelector('#box-list tr:nth-child(2)'),table=row.parentElement;
   const measure=()=>({height:row.getBoundingClientRect().height,width:table.getBoundingClientRect().width,columns:[...row.cells].map(c=>c.getBoundingClientRect().width)});
   const before=measure();row.querySelectorAll('.table-text').forEach(n=>n.textContent='very long description '.repeat(1000));const after=measure();
   const tools=document.querySelector('#create-tools');return {before,after,toolHeight:tools.getBoundingClientRect().height,customCollapsed:!document.querySelector('#create .custom-tools').open};
  });
  assert.deepEqual(result.after,result.before);assert.ok(result.toolHeight<70);assert.equal(result.customCollapsed,true);await page.close();
 }
});
test('cost overview loads on demand and reports partial provider coverage',async()=>{
 const page=await browser.newPage();const before=requests.filter(r=>r.path==='/v1/fleet/costs').length;
 await page.goto(base+'/#costs');await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#costs:not([hidden])');
 assert.equal(requests.filter(r=>r.path==='/v1/fleet/costs').length,before);assert.match(await page.$eval('#cost-overview',n=>n.textContent),/not been loaded/);
 await page.click('#load-costs');await page.waitForFunction(()=>document.querySelector('#cost-overview').textContent.includes('1 of 2 slots reported'));
 const text=await page.$eval('#cost-overview',n=>n.textContent);assert.match(text,/1\.23/);assert.match(text,/helper ü/);assert.match(text,/Project token cannot read billing/);
 assert.equal(requests.filter(r=>r.path==='/v1/fleet/costs').length,before+1);await page.screenshot({path:'/tmp/vmbox-cost-overview.png',fullPage:true});await page.close();
});
test('agent model choices are prefilled, kept separate and submitted with custom installation',async()=>{
 const page=await browser.newPage();await page.evaluateOnNewDocument(()=>{const original=window.fetch;window.sentRuns=[];window.fetch=async(path,options={})=>{if(path==='/v1/run-once'&&options.method==='POST'){window.sentRuns.push(JSON.parse(options.body));return new Response(JSON.stringify({id:'chosen-model',state:'blocked'}))}if(path==='/v1/run-once/chosen-model')return new Response(JSON.stringify({id:'chosen-model',state:'blocked'}));return original(path,options)}});
 await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#run-once select[name=provider] option');
 await page.select('#run-once select[name=agent]','codex');assert.ok(await page.$('#run-once select[name=model-mode] option[value="gpt-6-astra"]'));await page.select('#run-once select[name=model-mode]','gpt-5.6-terra');
 await page.select('#run-once select[name=agent]','claude');assert.equal(await page.$eval('#run-once select[name=model-mode]',s=>s.value),'');assert.equal(await page.$('#run-once select[name=model-mode] option[value="gpt-6-astra"]'),null);await page.select('#run-once select[name=model-mode]','sonnet');
 await page.select('#run-once select[name=agent]','codex');assert.equal(await page.$eval('#run-once select[name=model-mode]',s=>s.value),'gpt-5.6-terra');await page.select('#run-once select[name=agent]','claude');assert.equal(await page.$eval('#run-once select[name=model-mode]',s=>s.value),'sonnet');
 await page.select('#run-once select[name=claude]','personal');await page.type('#run-once textarea[name=prompt]','Check installed tools');await page.click('#run-once .custom-tools summary');await page.type('#run-once textarea[name=setupScript]','printf "literal $HOME"');
 await page.evaluate(()=>[...document.querySelectorAll('#run-once form button')].find(b=>b.textContent==='Run once').click());await page.waitForFunction(()=>window.sentRuns.length===1);const body=await page.evaluate(()=>window.sentRuns[0]);assert.equal(body.model,'sonnet');assert.equal(body.setupScript,'printf "literal $HOME"');await page.close();
});
test('agent form preserves literal options and numbered image references',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;let image=0;
  window.fetch=async(path,options={})=>{
   if(path==='/v1/run-once-images')return new Response(JSON.stringify({id:'image-'+(++image)}),{status:201});
   if(path==='/v1/run-once'&&options.method==='POST'){window.submittedRun=JSON.parse(options.body);return new Response(JSON.stringify({id:'image-run',state:'queued'}));}
   if(path==='/v1/run-once/image-run')return new Response(JSON.stringify({id:'image-run',state:'queued'}));
   return original(path,options);
  };
 });
 await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#run-once select[name=claude] option[value=personal]');
 await page.select('#run-once select[name=agent]','claude');await page.select('#run-once select[name=claude]','personal');
 await page.select('#run-once select[name=model-mode]','custom');await page.type('#run-once input[name=model]','custom-model');
 await page.click('#run-once input[type=checkbox][value=foundry]');
 await page.click('#run-once input[type=checkbox][value=blender]');
 await page.click('#run-once details summary');await page.type('#run-once textarea[name=args]','--option\nliteral value; $(false)');
 await page.type('#run-once textarea[name=prompt]','Inspect [Image 1]');
 await page.evaluate(()=>{const input=document.querySelector('#run-once input[type=file]'),data=new DataTransfer();data.items.add(new File(['fixture'],'example.png',{type:'image/png'}));input.files=data.files;input.dispatchEvent(new Event('change'));});
 await page.waitForFunction(()=>document.querySelector('#run-once').textContent.includes('[Image 1] example.png'));
 await page.evaluate(()=>[...document.querySelectorAll('#run-once form button')].find(b=>b.textContent==='Run once').click());
 await page.waitForFunction(()=>window.submittedRun);
 const body=await page.evaluate(()=>window.submittedRun);assert.equal(body.model,'custom-model');assert.deepEqual(body.args,['--option','literal value; $(false)']);assert.deepEqual(body.images,[{id:'image-1',number:1}]);assert.equal(body.prompt,'Inspect [Image 1]');
 assert.deepEqual(body.tools,['foundry','blender']);
 assert.deepEqual(errors,[]);await page.close();
});
test('completed one-shot opens retained output without allocation or a new shell',async()=>{
 const page=await browser.newPage();const start=requests.length;
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;
  window.fetch=async(path,...args)=>{
   if(path==='/v1/logical-boxes/box-1/run-once')return new Response(JSON.stringify({id:'finished'}));
   if(String(path).startsWith('/v1/run-once/'))return new Response(JSON.stringify({id:'finished',boxId:'box-1',state:'submitted',task:{agent:'shell',session:'task-finished',state:'exited',exitCode:17,finishedAt:'2026-09-09T00:00:00Z',output:'actual recorded fixture output'}}));
   if(path==='/v1/logical-boxes/box-1')return new Response(JSON.stringify({id:'box-1',name:'finished-box',state:'hibernated'}));
   return original(path,...args);
  };
 });
 await page.goto(base+'/boxes/box-1');
 await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('Exit code 17'));
 assert.match(await page.$eval('#terminal-screen',n=>n.textContent),/actual recorded fixture output/);
 assert.equal(new URL(page.url()).searchParams.get('run'),'finished');
 await page.click('#connect');await page.waitForNetworkIdle();
 assert.equal(requests.slice(start).filter(r=>r.method==='POST').length,0);
 await page.close();
});
test('model picker separates Codex and Claude and preserves selection',async()=>{
 const page=await browser.newPage();await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#run-once select[name=claude] option[value=personal]');
 const agent='#run-once select[name=agent]',model='#run-once select[name=model-mode]';
 await page.select(agent,'codex');
 assert.deepEqual(await page.$$eval(model+' option',xs=>xs.map(x=>x.value)),['','gpt-6-astra','gpt-5.6-sol','gpt-5.6-terra','gpt-5.6-luna','gpt-5.5','gpt-5.3-codex-spark','custom']);
 await page.select(model,'gpt-5.6-terra');await page.select(agent,'claude');
 assert.deepEqual(await page.$$eval(model+' option',xs=>xs.map(x=>x.value)),['','sonnet','opus','haiku','custom']);
 await page.select(model,'opus');await page.select(agent,'codex');assert.equal(await page.$eval(model,x=>x.value),'gpt-5.6-terra');
 await page.select(agent,'claude');assert.equal(await page.$eval(model,x=>x.value),'opus');
 assert.equal(await page.$eval('#run-once input[name=model]',x=>x.parentElement.hidden),true);
 await page.close();
});
test('pasted and dropped images share numbering; ordinary text paste is not intercepted',async()=>{
 const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.evaluateOnNewDocument(()=>{const original=fetch;let image=0;window.fetch=async(path,options={})=>{
  if(path==='/v1/run-once-images')return new Response(JSON.stringify({id:'image-'+(++image)}),{status:201});
  if(path==='/v1/run-once'&&options.method==='POST'){window.imageSubmission=JSON.parse(options.body);return new Response(JSON.stringify({id:'paste-run',state:'queued'}));}
  if(path==='/v1/run-once/paste-run')return new Response(JSON.stringify({id:'paste-run',state:'queued'}));return original(path,options);
 }});
 await page.goto(base+'/#run-once');await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#run-once select[name=claude] option[value=personal]');
 await page.select('#run-once select[name=agent]','claude');await page.select('#run-once select[name=claude]','personal');await page.select('#run-once select[name=model-mode]','sonnet');
 await page.evaluate(()=>{const d=new DataTransfer();d.items.add(new File(['png'],'pasted.png',{type:'image/png'}));document.querySelector('#run-once textarea[name=prompt]').dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}));});
 await page.waitForFunction(()=>document.querySelector('#run-once').textContent.includes('[Image 1] pasted.png'));
 await page.evaluate(()=>{const d=new DataTransfer();d.items.add(new File(['png'],'dropped.png',{type:'image/png'}));document.querySelector('#run-once form').dispatchEvent(new DragEvent('drop',{dataTransfer:d,bubbles:true,cancelable:true}));});
 await page.waitForFunction(()=>document.querySelector('#run-once').textContent.includes('[Image 2] dropped.png'));
 assert.equal(await page.evaluate(()=>{const d=new DataTransfer();d.setData('text/plain','ordinary paste');return document.querySelector('#run-once textarea[name=prompt]').dispatchEvent(new ClipboardEvent('paste',{clipboardData:d,bubbles:true,cancelable:true}));}),true);
 await page.type('#run-once textarea[name=prompt]','Inspect [Image 1] and [Image 2]');await page.evaluate(()=>document.querySelector('#run-once form').requestSubmit());await page.waitForFunction(()=>window.imageSubmission);
 const body=await page.evaluate(()=>window.imageSubmission);assert.equal(body.model,'sonnet');assert.deepEqual(body.images,[{id:'image-1',number:1},{id:'image-2',number:2}]);assert.deepEqual(errors,[]);await page.close();
});
test('deleted one-shot box still opens archived results without fetching its box',async()=>{
 const page=await browser.newPage();await page.evaluateOnNewDocument(()=>{const original=fetch;window.boxReads=0;window.fetch=async(path,...args)=>{
  if(path==='/v1/run-once/deleted')return new Response(JSON.stringify({id:'deleted',boxId:'box-1',boxDeleted:true,state:'finished',task:{agent:'shell',state:'exited',exitCode:7,finishedAt:'2026-09-09T00:00:00Z',output:'archived after deletion'}}));
  if(path==='/v1/logical-boxes/box-1'){window.boxReads++;return new Response('{}',{status:404});}return original(path,...args);
 }});await page.goto(base+'/boxes/box-1?run=deleted');await page.waitForFunction(()=>document.querySelector('#status').textContent.includes('Box deleted'));
 assert.match(await page.$eval('#terminal-screen',x=>x.textContent),/archived after deletion/);assert.equal(await page.evaluate(()=>window.boxReads),0);await page.close();
});
test('box link opens separate mobile workspace and reuses shell',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.setViewport({width:390,height:844,isMobile:true,hasTouch:true});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#box-list a');
 await Promise.all([page.waitForNavigation(),page.click('#box-list a')]);
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(new URL(page.url()).pathname,'/boxes/box-1');
 assert(requests.some(r=>r.path.endsWith('/sessions/interactive')&&r.body.agent==='shell'&&r.body.reuseShell===true));
 assert.deepEqual(errors,[]);await page.close();
});
test('workspace network failure explains safe recovery',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;let failed=false;
  window.fetch=(path,...args)=>{
   if(String(path).endsWith('/sessions/interactive')&&!failed){failed=true;return Promise.reject(new TypeError('Failed to fetch'))}
   return original(path,...args);
  };
 });
 await page.goto(base+'/boxes/box-1');
 await page.waitForFunction(()=>document.querySelector('#error').textContent.includes('operation may still be running'));
 assert.match(await page.$eval('#error',e=>e.textContent),/Resume \/ reconnect.*not replayed/);
 assert.equal(await page.$eval('#connect',e=>e.disabled),false);
 await page.click('#connect');
 await page.waitForFunction(()=>document.querySelector('#session').textContent.includes('persistent-shell'));
 assert.equal(await page.$eval('#error',e=>e.textContent),'');
 await page.close();
});
test('configuration UI stays tiny and has no terminal code',async()=>{
 const css=await readFile(resolve(root,'app.css'),'utf8'),js=await readFile(resolve(root,'app.js'),'utf8');
 assert(Buffer.byteLength(css)<2048);assert(!/@import|url\(/.test(css));
 for(const removed of ['/terminal','/tasks','chat-groups','setInterval'])assert(!js.includes(removed),removed);
});
test('new box starts automatically and its row follows startup through the temporary saved state',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.createState='';window.boxReads=0;
  window.fetch=async(path,options={})=>{
   if(path==='/v1/logical-boxes'&&options.method==='POST'){
    const body=JSON.parse(options.body);if(body.allocateWhenReady!==true)throw Error('new box did not request startup');
    window.createState='attaching';return new Response(JSON.stringify({id:'created-new',state:'attaching'}),{status:202});
   }
   if(path==='/v1/logical-boxes'&&(!options.method||options.method==='GET')){
    window.boxReads++;
    return new Response(JSON.stringify([{id:'box-1',name:'helper ü',state:'running',defaultAgent:'claude'},
     ...(window.createState?[{id:'created-new',name:'automatic',state:window.createState,defaultAgent:'shell'}]:[])]));
   }
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');
 await page.waitForSelector('#app:not([hidden])');await page.type('#create input[name=name]','automatic');
 await page.click('#create button');await page.waitForSelector('[data-box-id="created-new"]');
 await page.evaluate(()=>window.createState='hibernated');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="created-new"] td:nth-child(2)').textContent.includes('starting'),{timeout:12000});
 await page.evaluate(()=>window.createState='running');
 await page.waitForFunction(()=>document.querySelector('[data-box-id="created-new"] td:nth-child(2)').textContent.includes('running'),{timeout:12000});
 assert.ok(await page.evaluate(()=>window.boxReads>=3));await page.close();
});
for(const mobile of [false,true])test(mobile?'390x844 configuration controls':'desktop configuration edits',async()=>{
 const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 // Initialize mobile before navigation; this does not prove a real phone keyboard.
 await page.setViewport(mobile?{width:390,height:844,isMobile:true,hasTouch:true}:{width:1280,height:900});
 await page.goto(base);await page.type('#login input','test-only-token');await page.click('#login button');await page.waitForSelector('#app:not([hidden])');
 await page.select('#box-list select','codex');
 await page.waitForFunction(()=>document.querySelector('#provider-list button'));
 await page.click('#provider-list button');
 await page.$eval('#provider textarea[name=config]',n=>{n.value=JSON.stringify({image:'new'})});
 const saved=page.waitForResponse(r=>r.request().method()==='PATCH'&&r.url().endsWith('/primary'));
 await page.click('#provider button');await saved;
 const edit=requests.findLast(r=>r.method==='PATCH'&&r.path.endsWith('/primary'));assert.equal(edit.revision,revision);assert.deepEqual(edit.body,{config:{image:'new'}});
 await page.waitForNetworkIdle();
 assert.match(await page.$eval('#profile-tree',n=>n.textContent),/Team.*claude.*personal.*codex.*No saved profiles/s);
 await page.select('#profile-choices select[name=claude]','personal');
 await page.type('#create input[name=name]','profile-box');
 assert.deepEqual(await page.$$eval('#create select[name=defaultAgent] option',nodes=>nodes.map(n=>n.value)),['claude','codex','opencode','shell']);
 const selectedAgent=mobile?'shell':'opencode';await page.select('#create select[name=defaultAgent]',selectedAgent);
 await page.click('#create-tools input[value=blender]');
 const created=page.waitForResponse(r=>r.request().method()==='POST'&&r.url().endsWith('/v1/logical-boxes'));await page.click('#create button');await created;
 assert.deepEqual(requests.findLast(r=>r.method==='POST').body.loginProfiles,[{application:'claude',name:'personal'}]);
 assert.deepEqual(requests.findLast(r=>r.method==='POST').body.tools,['blender']);
 assert.equal(requests.findLast(r=>r.method==='POST').body.defaultAgent,selectedAgent);
 assert.equal(requests.findLast(r=>r.method==='POST').body.allocateWhenReady,true);
 await page.waitForNetworkIdle();
 assert.equal(await page.$('#profile-upload'),null);
 const beforeDelete=requests.filter(r=>r.method==='DELETE').length;
 page.once('dialog',d=>d.dismiss());await page.click('#profile-tree button');await page.waitForNetworkIdle();assert.equal(requests.filter(r=>r.method==='DELETE').length,beforeDelete);
 page.once('dialog',d=>d.accept());const deleted=page.waitForResponse(r=>r.request().method()==='DELETE');await page.click('#profile-tree button');await deleted;await page.waitForNetworkIdle();
 assert.match(await page.$eval('#capacity',n=>n.textContent),/Free: 1/);
 assert.equal(await page.$eval('#capacity details',n=>n.open),false);
 assert.equal(await page.$eval('#schema',n=>n.parentElement.open),false);
 assert.match(await page.$eval('#destinations',n=>n.textContent),/No notification destinations/);
 assert.equal(await page.$eval('#slots input',n=>n.value),'2');
 const capacitySaved=page.waitForResponse(r=>r.request().method()==='PUT'&&r.url().endsWith('/v1/fleet/slots'));
 await page.click('#slots button');await capacitySaved;
 assert.equal(requests.findLast(r=>r.path==='/v1/fleet/slots').body.compute_box_slots,2);
 assert.equal(await page.$('#terminal'),null);
 assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
 await page.screenshot({path:mobile?'/tmp/vmbox-config-mobile.png':'/tmp/vmbox-config-desktop.png',fullPage:true});
 await page.click('#logout');await page.waitForFunction(()=>document.querySelector('#app').hidden);assert.equal(await page.$eval('#provider textarea[name=secret]',n=>n.value),'');
 assert.deepEqual(errors,[]);await page.close();
});

test('fleet locations load on demand and preserve occupied fleets on rejection',async()=>{
 const page=await browser.newPage();
 await page.evaluateOnNewDocument(()=>{
  const original=window.fetch;window.locationWrites=[];
  window.fetch=async(path,options={})=>{
   if(path.startsWith('/v1/fleet/regions?'))return new Response(JSON.stringify([{id:'eu',name:'Europe'},{id:'us',name:'America'}]));
   if(path.startsWith('/v1/fleet/slots?'))return new Response(JSON.stringify({region:'eu'}));
   if(path==='/v1/fleet/location'){locationWrites.push(JSON.parse(options.body));return new Response(JSON.stringify({error:'Location changes require an empty fleet; existing boxes cannot be migrated.'}),{status:409})}
   return original(path,options);
  };
 });
 await page.goto(base);await page.type('#login input','fixture');await page.click('#login button');await page.waitForSelector('#capacity table');
 assert.equal(await page.$eval('#location-form',e=>e.hidden),true);
 await page.click('#fleet-location summary');await page.click('#load-locations');await page.waitForSelector('#location-form:not([hidden])');
 assert.equal(await page.$eval('#location-form select',e=>e.value),'eu');
 await page.select('#location-form select','us');await page.click('#location-form button');
 await page.waitForFunction(()=>document.querySelector('#location-status').textContent.includes('empty fleet'));
 assert.deepEqual(await page.evaluate(()=>locationWrites),[{provider:'railway',providerCredential:'primary',region:'us'}]);
 assert.match(await page.$eval('#capacity',e=>e.textContent),/helper ü/);
 await page.close();
});
