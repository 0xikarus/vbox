// Actual Chromium with fixture API responses. This is UI evidence, not live backend proof.
// VMBOX_CHROMIUM=/path/to/chromium node --test tests/browser/tasks-ui.test.mjs
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { before, after, test } from 'node:test';
const { default: puppeteer } = await import(process.env.VMBOX_TASKS_PUPPETEER || 'puppeteer-core');
const source = await readFile(new URL('../../internal/controller/web/tasks.js', import.meta.url), 'utf8');
const css = await readFile(new URL('../../internal/controller/web/tasks.css', import.meta.url), 'utf8');
const appCSS = await readFile(new URL('../../internal/controller/web/app.css', import.meta.url), 'utf8');
let browser;
before(async () => { browser = await puppeteer.launch({ executablePath: process.env.VMBOX_CHROMIUM, headless: true, args: ['--no-sandbox', '--disable-setuid-sandbox'] }); });
after(async () => { await browser?.close(); });
const assignment = { id: 'research', title: 'Compare options', instruction: 'Investigate the question', acceptanceCriteria: ['Explain tradeoffs'], dependsOn: [] };
const record = (id = 'w1', state = 'awaiting_approval') => ({ id, version: 1, state, idea: 'Plan a community event <script>window.pwned=1</script>', agent: 'codex', profile: 'saved', maxWorkers: 2, assetIds: [], messages: [{ role: 'coordinator', text: '<img src=x onerror="window.pwned=1">', createdAt: '2026-09-08' }], plans: [{ revision: 1, summary: 'Compare venue and activity options', questions: [], assignments: [structuredClone(assignment)] }], attempts: [], approvedRevision: 0 });
async function fixture(t, config = {}) {
  const page = await browser.newPage(); t.after(() => page.close());
  await page.setViewport(config.mobile ? { width: 390, height: 844, isMobile: true, hasTouch: true } : { width: 1280, height: 900 });
  const requests = [], errors = []; page.on('pageerror', e => errors.push(e.message)); t.after(() => assert.deepEqual(errors, []));
  const state = { items: [record()], caps: { enabled: true, executionReady: true, agents: [{ name: 'codex', images: true }, { name: 'claude', images: false }], maxWorkers: 6 }, ...config };
  await page.setRequestInterception(true);
  page.on('request', async req => {
    const path = new URL(req.url()).pathname;
    const respond = (data, status = 200) => req.respond({ status, contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/') return req.respond({ status: 200, contentType: 'text/html', body: `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><style>${appCSS}\n${css}</style><main id="root"></main><script type="module">import {mountTasks} from '/tasks.js'; window.mount = () => { window.dispose = mountTasks(document.querySelector('#root'), async (path, method='GET', body, headers={}) => { const r=await fetch(path,{method,headers:{'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body)}); const data=await r.json(); if(!r.ok) throw Error(data.error || 'HTTP '+r.status); return data; }); }; window.mount();</script>` });
    if (path === '/tasks.js') return req.respond({ status: 200, contentType: 'text/javascript', body: source });
    if (path.startsWith('/v1/factory')) {
      const entry = { path: path.slice('/v1/factory'.length), method: req.method(), headers: req.headers(), raw: req.postData() };
      if (entry.raw && entry.path !== '/assets') entry.body = JSON.parse(entry.raw);
      requests.push(entry);
      if (state.handler && await state.handler(entry, respond)) return;
      if (entry.path === '/tasks/capabilities') return respond(state.caps);
      if (entry.path === '/profiles') return respond([{ application: 'codex', name: 'saved' }, { application: 'claude', name: 'other' }]);
      if (entry.path === '/tasks' && entry.method === 'GET') return respond(state.items);
      if (entry.path === '/assets') return respond({ id: 'asset-1' });
      if (entry.path === '/tasks' && entry.method === 'POST') return respond({ ...record('created', 'planning_queued'), ...entry.body, plans: [], messages: [] });
      const item = state.items.find(w => entry.path === '/tasks/' + encodeURIComponent(w.id));
      if (item) return respond(item);
      return respond({ error: 'Unexpected fixture request' }, 404);
    }
    return req.respond({ status: 404, body: '' });
  });
  await page.setCookie({ name: 'session', value: 'fixture-cookie', url: 'https://tasks.test', httpOnly: true, secure: true });
  await page.goto('https://tasks.test'); await page.waitForFunction(() => !document.querySelector('[role=status]').textContent.includes('Loading'));
  return { page, requests, state };
}
const pause = ms => new Promise(r => setTimeout(r, ms));
async function click(page, label) { await page.evaluate(label => [...document.querySelectorAll('button')].find(b => b.textContent === label).click(), label); }
async function saved(page, id = 'w1') { await page.waitForSelector(`[name=task] option[value="${id}"]`); await page.select('[name=task]', id); await page.waitForFunction(id => document.querySelector('.tasks-detail').textContent.includes('Task ' + id), {}, id); }
async function choose(page) { await page.select('[name=agent]', 'codex'); await page.select('[name=profile]', 'saved'); await page.type('[name=idea]', 'Organize a neighborhood picnic'); }
async function upload(page, type = 'image/png', count = 1, size = 68) {
  await page.evaluate(({ type, count, size }) => { const dt = new DataTransfer(); for(let i=0;i<count;i++) dt.items.add(new File([new Uint8Array(size)], 'photo.png', {type})); const n=document.querySelector('[name=images]'); n.files=dt.files; n.dispatchEvent(new Event('change',{bubbles:true})); }, {type, count, size});
}
for (const mobile of [false, true]) test(`${mobile ? 'mobile' : 'desktop'} general create, attachments, profile, duplicate suppression`, async t => {
  let release;
  const { page, requests } = await fixture(t, { mobile, handler: async (r, respond) => { if (r.path === '/tasks' && r.method === 'POST') { release = () => respond({ ...record('created', 'planning_queued'), plans: [], messages: [] }); return true; } } });
  await choose(page); assert.equal(await page.$('[name=repository]'), null);
  await upload(page); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  const asset = requests.find(r => r.path === '/assets'); assert.match(asset.headers.cookie, /session=fixture-cookie/); assert.match(asset.headers['content-type'], /^multipart\/form-data; boundary=/); assert(asset.headers['idempotency-key']);
  await page.select('[name=agent]', 'claude'); await page.select('[name=profile]', 'other'); assert.equal(await page.$eval('button[type=submit]', n => n.disabled), true);
  await page.select('[name=agent]', 'codex'); await page.select('[name=profile]', 'saved');
  await page.evaluate(() => { const f=document.querySelector('form'); f.dispatchEvent(new Event('submit',{cancelable:true})); f.dispatchEvent(new Event('submit',{cancelable:true})); });
  while (!release) await pause(10);
  assert.equal(requests.filter(r => r.path === '/tasks' && r.method === 'POST').length, 1);
  const sent = requests.find(r => r.path === '/tasks' && r.method === 'POST');
  assert.deepEqual(sent.body, { idea: 'Organize a neighborhood picnic', agent: 'codex', profile: 'saved', assetIds: ['asset-1'], maxWorkers: 1 }); assert(sent.headers['idempotency-key']);
  await release(); await page.waitForFunction(() => document.body.textContent.includes('Task created'));
  assert.match(await page.$eval('.tasks-detail', n => n.textContent), /planning_queued/); assert.match(await page.$eval('.tasks-detail', n => n.textContent), /Verdict: Not recorded/);
  assert.equal(requests.filter(r => r.path.endsWith('/run')).length, 0);
  assert.equal(await page.$eval('[name=idea]', n => n.value), ''); assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await page.screenshot({ path: `/tmp/tasks-${mobile ? 'mobile' : 'desktop'}.png`, fullPage: true });
});
test('questions, reply refinement, version lock, unchanged retry key and exact Run revision', async t => {
  const w = record(); w.plans[0].questions = ['How many guests?']; w.state = 'awaiting_reply'; let replies = 0;
  const { page, requests } = await fixture(t, { items: [w], handler: async (r, respond) => {
    if (r.path.endsWith('/messages')) { if (++replies === 1) await respond({ error: 'Version conflict' },409); else await respond({ ...record(), version: 2, plans: [{ ...record().plans[0], revision: 3 }] }); return true; }
    if (r.path.endsWith('/run')) { await respond({ ...record(), version: 3, state: 'running', approvedRevision: 3 }); return true; }
  } });
  await saved(page); assert.equal(await page.$eval('[data-run]', n => n.disabled), true); assert.match(await page.$eval('.tasks-detail', n => n.textContent), /How many guests/);
  await page.type('[name=reply]', 'Thirty guests'); await click(page, 'Send reply / refine'); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Version conflict'));
  assert.equal(await page.$eval('[name=reply]', n => n.value), 'Thirty guests'); await click(page, 'Send reply / refine'); await page.waitForFunction(() => document.body.textContent.includes('Version 2'));
  const posts=requests.filter(r=>r.path.endsWith('/messages')); assert.deepEqual(posts[0].body,{version:1,text:'Thirty guests'}); assert.equal(posts[0].headers['idempotency-key'],posts[1].headers['idempotency-key']);
  await click(page,'Run approved plan'); await page.waitForFunction(()=>document.body.textContent.includes('Version 3'));
  assert.deepEqual(requests.find(r=>r.path.endsWith('/run')).body,{version:2,planRevision:3}); assert.equal(await page.$eval('[name=reply]', n=>n.matches(':disabled')),true);
});
test('plan validation rejects questions, empty criteria, duplicate IDs, missing dependencies, cycles and bounds', async t => {
  const {page,state}=await fixture(t); await saved(page);
  const invalid = [p=>p.questions=['Unresolved'],p=>p.assignments[0].acceptanceCriteria=[],p=>p.assignments[0].acceptanceCriteria=[' '],p=>p.assignments.push(structuredClone(p.assignments[0])),p=>p.assignments[0].dependsOn=['missing'],p=>p.assignments[0].dependsOn=['research'],p=>p.assignments=[],p=>p.assignments=Array.from({length:21},(_,i)=>({...assignment,id:String(i)}))];
  for (const [i,change] of invalid.entries()) { const w=record(); w.version=i+2; change(w.plans[0]); state.items=[w]; await click(page,'Refresh / reconnect'); await page.waitForFunction(v=>document.body.textContent.includes('Version '+v),{},w.version); assert.equal(await page.$eval('[data-run]',n=>n.disabled),true); }
});
test('actual outputs, exits, failures, safe text, explicit retry history and cancellation observation', async t => {
  const w=record('w1','failed'); w.attempts=[{id:'ok',stage:'work',assignmentId:'research',state:'exited',exitCode:0,output:'Actual findings <script>window.pwned=1</script>'},{id:'bad',stage:'work',assignmentId:'research',state:'exited',exitCode:1,signal:9,failure:'Agent authentication failed',boxId:'box/1'},{id:'missing',stage:'synthesize',state:'result_missing',exitCode:null,failure:'Receipt missing'},{id:'live',stage:'plan',state:'running',exitCode:null,failure:'Transient observation problem'}];
  const {page,requests}=await fixture(t,{items:[w],handler:async(r,respond)=>{
    if(r.path.endsWith('/retry')){await respond({...w,version:2,state:'running',attempts:[...w.attempts,{id:'new',stage:'work',assignmentId:'research',state:'queued',exitCode:null}]});return true;}
    if(r.path.endsWith('/cancel')){await respond({...w,version:3,state:'cancelling'});return true;}
  }});
  await saved(page); await page.$$eval('details',ns=>ns.forEach(n=>n.open=true));
  const text=await page.$eval('.tasks-detail',n=>n.textContent); assert.match(text,/exit 0/); assert.match(text,/exit 1 · signal 9/); assert.match(text,/exit not observed/); assert.match(text,/Actual findings/); assert.match(text,/Agent authentication failed/);
  assert.equal(await page.evaluate(()=>window.pwned),undefined); assert.equal(await page.$('.tasks script, .tasks img[onerror]'),null); assert.equal(await page.$('[data-retry=ok]'),null); assert.equal(await page.$('[data-retry=live]'),null);
  assert.equal(await page.$eval('a[href^="/boxes/"]',n=>n.getAttribute('href')),'/boxes/box%2F1');
  await click(page,'Retry attempt bad'); await page.waitForFunction(()=>document.body.textContent.includes('Attempt new'));
  assert.deepEqual(requests.find(r=>r.path.endsWith('/retry')).body,{version:1,attemptId:'bad'}); assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Attempt bad/);
  assert.equal(await page.$eval('details[data-key="attempt:bad"]',n=>n.open),true);
  await click(page,'Cancel task'); await page.waitForFunction(()=>document.body.textContent.includes('Version 3'));
  assert.deepEqual(requests.find(r=>r.path.endsWith('/cancel')).body,{version:2}); assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/still being observed until terminal/); assert.equal(await page.$eval('[data-cancel]',n=>n.disabled),true);
});
test('final synthesis is authoritative and reply starts a new planning revision',async t=>{
  const w={...record('w1','completed'),final:'Recommended venue: the park',verdict:'needs_revision'};
  const {page,requests}=await fixture(t,{items:[w],handler:async(r,respond)=>{if(r.path.endsWith('/messages')){await respond({...w,version:2,state:'planning_queued'});return true;}}});
  await saved(page); assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Verdict: needs_revision/); assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Recommended venue/);
  await page.type('[name=reply]','Consider rain');await click(page,'Send reply / refine');await page.waitForFunction(()=>document.body.textContent.includes('Version 2'));assert.deepEqual(requests.find(r=>r.path.endsWith('/messages')).body,{version:1,text:'Consider rain'});
});
test('selection races and stale reads cannot overwrite newer mutations; drafts survive selection',async t=>{
  let release, hold=false;
  const {page,state}=await fixture(t,{items:[record(),record('w2')],handler:async(r,respond)=>{
    if(r.path==='/tasks/w1'&&hold){release=()=>respond(record());return true;}
    if(r.path.endsWith('/messages')){await respond({...record(),version:5,state:'planning_queued'});return true;}
  }});
  await saved(page);await page.type('[name=reply]','First draft');hold=true;await click(page,'Refresh / reconnect');while(!release)await pause(10);
  await saved(page,'w2');await page.type('[name=reply]','Second draft');await release();await pause(100);assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Task w2/);
  hold=false;await saved(page);assert.equal(await page.$eval('[name=reply]',n=>n.value),'First draft');hold=true;release=null;await click(page,'Refresh / reconnect');while(!release)await pause(10);
  await click(page,'Send reply / refine');await page.waitForFunction(()=>document.body.textContent.includes('Version 5'));await release();await pause(100);assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Version 5/);
  hold=false;state.items[0].version=2;await click(page,'Refresh / reconnect');await pause(100);assert.match(await page.$eval('.tasks-detail',n=>n.textContent),/Version 5/);
  await saved(page,'w2');assert.equal(await page.$eval('[name=reply]',n=>n.value),'Second draft');
});
test('setup unavailable blocks execution, retains drafts and shows no fake pass',async t=>{
  const {page,state,requests}=await fixture(t);await choose(page);await saved(page);await page.type('[name=reply]','Keep my changes');state.caps.executionReady=false;
  await click(page,'Refresh / reconnect');await page.waitForFunction(()=>document.querySelector('[role=status]').textContent.includes('Setup needed'));
  assert.equal(await page.$eval('[data-run]',n=>n.disabled),true);assert.equal(await page.$eval('[name=idea]',n=>n.value),'Organize a neighborhood picnic');assert.equal(await page.$eval('[name=reply]',n=>n.value),'Keep my changes');
  await page.evaluate(()=>document.querySelectorAll('form').forEach(f=>f.dispatchEvent(new Event('submit',{cancelable:true}))));assert.equal(requests.filter(r=>r.method==='POST').length,0);
  state.handler=async(r,respond)=>{if(r.path==='/tasks/capabilities'){await respond({error:'Not installed'},404);return true;}};
  await click(page,'Refresh / reconnect');await page.waitForFunction(()=>document.querySelector('[role=status]').textContent.includes('backend is unavailable'));assert.equal(await page.$eval('fieldset',n=>n.disabled),true);
});
test('failed create and uploads preserve drafts and reuse idempotency; validate image limits',async t=>{
  let uploads=0;
  const {page,requests}=await fixture(t,{handler:async(r,respond)=>{
    if(r.path==='/assets'&&++uploads===1){await respond({error:'Upload unavailable'},503);return true;}
    if(r.path==='/tasks'&&r.method==='POST'){await respond({error:'Lost response'},502);return true;}
  }});await choose(page);
  for(const args of [['image/webp',1,68],['image/png',9,68],['image/jpeg',1,11*1024*1024],['image/jpeg',5,9*1024*1024]]){await upload(page,...args);assert.match(await page.$eval('[role=alert]',n=>n.textContent),/at most 8/);}
  assert.equal(requests.filter(r=>r.path==='/assets').length,0);
  await upload(page,'image/jpeg');await page.waitForFunction(()=>document.querySelector('figcaption')?.textContent.includes('Upload unavailable'));await click(page,'Retry upload');await page.waitForFunction(()=>document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  const sent=requests.filter(r=>r.path==='/assets');assert.equal(sent[0].headers['idempotency-key'],sent[1].headers['idempotency-key']);
  for(let i=0;i<2;i++){await click(page,'Request plan');await page.waitForFunction(()=>document.querySelector('[role=alert]').textContent.includes('Lost response'));}
  const posts=requests.filter(r=>r.path==='/tasks'&&r.method==='POST');assert.equal(posts.length,2);assert.equal(posts[0].headers['idempotency-key'],posts[1].headers['idempotency-key']);assert.equal(await page.$eval('[name=idea]',n=>n.value),'Organize a neighborhood picnic');
  const url=await page.$eval('figure img',n=>n.src);await click(page,'Remove attachment');assert.equal(await page.evaluate(async url=>{try{await fetch(url);return false;}catch{return true;}},url),true);
});
test('poll errors stop, hidden views stop, cleanup invalidates in-flight responses and remount',async t=>{
  let reads=0,release;
  const {page,requests,state}=await fixture(t,{items:[record('w1','running')],handler:async(r,respond)=>{if(r.path==='/tasks/w1'&&++reads===2){await respond({error:'Observation disconnected'},503);return true;}}});
  await saved(page);await page.waitForFunction(()=>document.querySelector('[role=alert]').textContent.includes('Observation disconnected'),{timeout:6000});let count=requests.length;await pause(3200);assert.equal(requests.length,count);
  await click(page,'Refresh / reconnect');await page.waitForFunction(()=>!document.querySelector('[role=alert]').textContent);await page.evaluate(()=>document.querySelector('#root').hidden=true);await pause(100);count=requests.length;await pause(3200);assert.equal(requests.length,count);
  await page.evaluate(()=>document.querySelector('#root').hidden=false);
  state.handler=async(r,respond)=>{if(r.path==='/tasks/w1'){release=()=>respond({...record(),version:9});return true;}};
  await click(page,'Refresh / reconnect');while(!release)await pause(10);await page.evaluate(()=>window.dispose());await release();await pause(100);assert.equal(await page.$eval('#root',n=>n.childElementCount),0);
  await page.evaluate(()=>{window.mount();window.mount();});await page.waitForSelector('[name=task] option[value=w1]');assert.equal(await page.$$eval('.tasks',ns=>ns.length),1);
});
test('keyboard disclosure focus and open state survive polling on mobile',async t=>{
  const w=record('w1','running');w.attempts=[{id:'a1',stage:'work',assignmentId:'research',state:'running',exitCode:null,output:'Partial recorded output'}];
  const {page,state}=await fixture(t,{mobile:true,items:[w]});await saved(page);
  await page.focus('summary[data-focus="attempt:a1"]');await page.keyboard.press('Enter');
  assert.equal(await page.$eval('details[data-key="attempt:a1"]',n=>n.open),true);
  state.items[0]={...w,version:2};await page.waitForFunction(()=>document.body.textContent.includes('Version 2'),{timeout:6000});
  assert.equal(await page.evaluate(()=>document.activeElement.dataset.focus),'attempt:a1');assert.equal(await page.$eval('details[data-key="attempt:a1"]',n=>n.open),true);
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
});
test('cleanup aborts pending uploads and revokes previews; disabled setup makes no task requests',async t=>{
  let release;
  const {page,state,requests}=await fixture(t,{caps:{enabled:false}});
  assert.match(await page.$eval('[role=status]',n=>n.textContent),/Setup needed/);assert.equal(requests.length,1);
  state.caps={enabled:true,executionReady:true,agents:[{name:'codex',images:true}],maxWorkers:2};
  state.handler=async(r,respond)=>{if(r.path==='/assets'){release=()=>respond({id:'late-asset'});return true;}};
  await click(page,'Refresh / reconnect');await page.waitForSelector('[name=agent] option[value=codex]');await choose(page);await upload(page);while(!release)await pause(10);
  const url=await page.$eval('figure img',n=>n.src);await page.evaluate(()=>window.dispose());await release();await pause(100);
  assert.equal(await page.$eval('#root',n=>n.childElementCount),0);assert.equal(await page.evaluate(async url=>{try{await fetch(url);return false;}catch{return true;}},url),true);
});
