import assert from 'node:assert/strict';
import {access, readFile} from 'node:fs/promises';
import http from 'node:http';
import {after, before, test} from 'node:test';
import {dirname, extname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import puppeteer from 'puppeteer-core';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const webRoot = resolve(root, 'internal/controller/web');
const requests = [];
let terminalDelay = 0;
const now = '2026-09-04T10:00:00Z';
const boxes = [
  {id:'box-1', name:'codex-box', provider:'railway', providerCredential:'primary', defaultAgent:'codex', state:'running', volumeId:'volume-1', volumeName:'codex-data', slotId:'slot-1'},
  {id:'box-2', name:'claude-box', provider:'railway', providerCredential:'primary', defaultAgent:'claude', state:'running', volumeId:'volume-2', volumeName:'claude-data', slotId:'slot-2'},
];
const tasks = {
  'box-1': [{id:'task-1', logicalBoxId:'box-1', boxName:'codex-box', agent:'codex', session:'codex-live', prompt:'work', state:'active', createdAt:now, updatedAt:now}],
  'box-2': [{id:'task-2', logicalBoxId:'box-2', boxName:'claude-box', agent:'claude', session:'claude-live', prompt:'work', state:'active', createdAt:now, updatedAt:now}],
};
const groups = [{id:'group-1', name:'Research crew', members:[
  {logicalBoxId:'box-1', boxName:'codex-box', agent:'codex', canReceive:true},
  {logicalBoxId:'box-2', boxName:'claude-box', agent:'claude', canReceive:true},
]}];

function json(response, status, value) {
  response.writeHead(status, {'content-type':'application/json'});
  response.end(JSON.stringify(value));
}

async function requestBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const text = Buffer.concat(chunks).toString();
  return text ? JSON.parse(text) : null;
}

let server;
let browser;
let baseURL;

before(async () => {
  server = http.createServer(async (request, response) => {
    const url = new URL(request.url, 'http://controller.test');
    const body = ['POST','PUT','PATCH','DELETE'].includes(request.method) ? await requestBody(request) : null;
    requests.push({method:request.method, path:url.pathname, search:url.search, body});

    if (request.method === 'GET' && ['/', '/index.html', '/app.js', '/app.css', '/favicon.svg', '/favicon.ico'].includes(url.pathname)) {
      const file = url.pathname === '/' ? '/index.html' : url.pathname;
      const asset = file === '/favicon.ico' ? '/favicon.svg' : file;
      const type = {'.html':'text/html', '.js':'text/javascript', '.css':'text/css', '.svg':'image/svg+xml'}[extname(asset)];
      response.writeHead(200, {'content-type':type});
      response.end(await readFile(resolve(webRoot, `.${asset}`)));
      return;
    }
    if (request.method === 'GET' && url.pathname === '/v1/logical-boxes') return json(response, 200, boxes);
    if (request.method === 'GET' && url.pathname === '/v1/chat-groups') return json(response, 200, groups);
    if (request.method === 'GET' && url.pathname === '/v1/provider-credentials') return json(response, 200, [{provider:'railway', name:'primary', config:{}, updatedAt:now}]);
    if (request.method === 'GET' && url.pathname === '/v1/notifications') return json(response, 200, []);
    if (request.method === 'GET' && url.pathname === '/v1/inventory') return json(response, 200, {logicalBoxes:boxes, connectedBoxes:[]});
    if (request.method === 'GET' && url.pathname === '/v1/fleet/status') return json(response, 200, {provider:'railway', providerCredential:'primary', desiredSlots:2, actualSlots:2, occupiedSlots:2, freeSlots:0, slots:[]});
    if (request.method === 'POST' && url.pathname === '/v1/logical-boxes') {
      const box = {id:'box-3', name:body.name, provider:body.provider, providerCredential:body.providerCredential, defaultAgent:body.defaultAgent, state:'hibernated', volumeId:'volume-3', volumeName:`${body.name}-data`};
      boxes.push(box);
      tasks[box.id] = [];
      return json(response, 202, box);
    }
    if (request.method === 'GET' && /^\/v1\/logical-boxes\/box-[12]$/.test(url.pathname)) return json(response, 200, boxes.find(box => url.pathname.endsWith(box.id)));
    if (request.method === 'PATCH' && /^\/v1\/logical-boxes\/box-[12]$/.test(url.pathname)) {
      const box = boxes.find(value => url.pathname.endsWith(value.id));
      box.defaultAgent = body.defaultAgent;
      return json(response, 200, box);
    }
    const hibernate = url.pathname.match(/^\/v1\/logical-boxes\/(box-[12])\/hibernate$/);
    if (request.method === 'POST' && hibernate) {
      const box = boxes.find(value => value.id === hibernate[1]);
      box.state = 'hibernating';
      box.restorationState = 'saving-workspace';
      return json(response, 202, box);
    }
    const taskList = url.pathname.match(/^\/v1\/logical-boxes\/(box-[12])\/tasks$/);
    if (request.method === 'GET' && taskList) return json(response, 200, tasks[taskList[1]]);
    const taskDetail = url.pathname.match(/^\/v1\/tasks\/(task-[12])$/);
    if (request.method === 'GET' && taskDetail) return json(response, 200, Object.values(tasks).flat().find(task => task.id === taskDetail[1]));
    const taskMessages = url.pathname.match(/^\/v1\/tasks\/(task-[12])\/messages$/);
    if (request.method === 'GET' && taskMessages) return json(response, 200, [
      {id:`message-${taskMessages[1]}`, taskId:taskMessages[1], direction:'user', text:'Initial research prompt', state:'delivered', createdAt:now, updatedAt:now},
      {id:`stream-${taskMessages[1]}`, taskId:taskMessages[1], direction:'agent', text:'Checking the repository now', state:'streaming', createdAt:now, updatedAt:now},
    ]);
    if (request.method === 'POST' && taskMessages) return json(response, 202, {id:'delivered-message', taskId:taskMessages[1], direction:'user', text:body.text, state:'delivered', createdAt:now, updatedAt:now});
    const terminal = url.pathname.match(/^\/v1\/logical-boxes\/(box-[12])\/terminal$/);
    if (request.method === 'GET' && terminal) {
      if (terminal[1] === 'box-1' && terminalDelay) await new Promise(resolve => setTimeout(resolve, terminalDelay));
      return json(response, 200, {session:url.searchParams.get('session'), command:'arbitrary-program', content:`${terminal[1]} live agent output\nworking safely`, width:120, height:35, capturedAt:now});
    }
    const terminalInput = url.pathname.match(/^\/v1\/logical-boxes\/(box-[12])\/terminal\/input$/);
    if (request.method === 'POST' && terminalInput) {
      response.writeHead(204);
      response.end();
      return;
    }
    if (request.method === 'GET' && url.pathname === '/v1/chat-groups/group-1/messages') return json(response, 200, [{id:'group-message-1', groupId:'group-1', text:'Review the chain notes', deliveries:[], createdAt:now}]);
    if (request.method === 'POST' && url.pathname === '/v1/chat-groups/group-1/messages') return json(response, 202, {id:'new-group-message', groupId:'group-1', text:body.text, deliveries:body.recipientBoxIds.map(logicalBoxId => ({logicalBoxId, state:'queued'})), createdAt:now});
    const direct = url.pathname.match(/^\/v1\/logical-boxes\/(box-[12])\/messages$/);
    if (request.method === 'POST' && direct) {
      const task = tasks[direct[1]][0];
      return json(response, 202, {task, message:{id:'forwarded-message', taskId:task.id, direction:'user', text:body.text, state:'queued', createdAt:now}, started:false, boxState:'running'});
    }
    json(response, 404, {error:`unhandled ${request.method} ${url.pathname}`});
  });
  await new Promise(resolveReady => server.listen(0, '127.0.0.1', resolveReady));
  baseURL = `http://127.0.0.1:${server.address().port}`;
  const executablePath = process.env.VMBOX_CHROMIUM || '/snap/bin/chromium';
  await access(executablePath);
  browser = await puppeteer.launch({executablePath, headless:true, args:['--no-sandbox','--disable-setuid-sandbox']});
});

test('UI remains small and self-contained', async () => {
  const css = await readFile(resolve(webRoot, 'app.css'), 'utf8');
  assert(Buffer.byteLength(css) < 5 * 1024, 'Keep the shared stylesheet below 5 KiB');
  assert(!/@import|url\(/.test(css), 'No font or asset downloads');
});

test('terminal input, selection races, fullscreen, and reconnect', async () => {
  boxes[0].state = 'running';
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(baseURL, {waitUntil:'networkidle0'});
  await page.type('#token', 'browser-test-password');
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('[data-box="box-1"]');
  terminalDelay = 600;
  const pending = page.waitForRequest(request => request.url().includes('/box-1/terminal?'));
  await page.click('[data-box="box-1"]');
  await pending;
  await page.click('[data-box="box-2"]');
  await page.waitForFunction(() => document.querySelector('#terminal').textContent.startsWith('box-2'));
  await new Promise(resolve => setTimeout(resolve, 750));
  terminalDelay = 0;
  assert.match(await page.$eval('#terminal', el => el.textContent), /^box-2/);
  assert.equal(await page.$eval('#chat-name', el => el.textContent), 'claude-box');

  const start = requests.length;
  await page.focus('#terminal');
  for (const key of ['Backspace', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight']) await page.keyboard.press(key);
  await page.keyboard.down('Control');
  await page.keyboard.press('c');
  await page.keyboard.up('Control');
  await page.evaluate(() => {
    const screen = document.querySelector('#terminal');
    screen.dispatchEvent(new KeyboardEvent('keydown', {key:'@',ctrlKey:true,altKey:true,bubbles:true,cancelable:true}));
    const data = new DataTransfer();
    data.setData('text/plain', 'äöü pasted\nsecond line');
    screen.dispatchEvent(new ClipboardEvent('paste', {clipboardData:data,bubbles:true,cancelable:true}));
  });
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('#connection-label').textContent === 'Live');
  for (let i=0;i<60 && requests.slice(start).filter(r => r.method === 'POST').length < 8;i++) await new Promise(resolve => setTimeout(resolve,50));
  const inputs = requests.slice(start).filter(r => r.method === 'POST');
  assert(inputs.every(r => r.path === '/v1/logical-boxes/box-2/terminal/input' && r.search.includes('claude-live')));
  assert.deepEqual(inputs.flatMap(r => r.body.keys || []), ['BSpace','Up','Down','Left','Right','C-C','Enter']);
  assert.equal(inputs.map(r => r.body.text || '').join(''), '@äöü pasted\nsecond line');

  await page.click('#fullscreen-terminal');
  await page.waitForFunction(() => document.fullscreenElement || document.querySelector('.fullscreen-fallback'));
  await page.click('#fullscreen-terminal');
  await page.waitForFunction(() => !document.fullscreenElement && !document.querySelector('.fullscreen-fallback'));
  await page.setOfflineMode(true);
  await page.waitForFunction(() => document.querySelector('#terminal-status').textContent.includes('retrying'), {timeout:15000});
  assert.match(await page.$eval('#terminal', el => el.textContent), /^box-2/);
  await page.setOfflineMode(false);
  await page.waitForFunction(() => document.querySelector('#terminal-status').hidden, {timeout:15000});
  const mutations = requests.filter(r => r.method !== 'GET').length;
  await page.click('#detach-terminal');
  assert.equal(requests.filter(r => r.method !== 'GET').length, mutations);
  assert.deepEqual(errors, []);
  await page.close();
});

after(async () => {
  await browser?.close();
  await new Promise(resolveClosed => server?.close(resolveClosed));
});

test('controller routes exact sessions and supports safe group collaboration', async () => {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.setViewport({width:1440, height:900});
  await page.goto(baseURL, {waitUntil:'networkidle0'});
  await page.type('#token', 'browser-test-password');
  await Promise.all([page.click('#login-form button[type="submit"]'), page.waitForSelector('#app:not([hidden])')]);
  await page.waitForSelector('[data-box="box-1"]');
  assert.equal(await page.evaluate(() => fetch('/favicon.ico').then(response => response.status)), 200);

  await page.click('#new-box');
  await page.type('#box-form [name="name"]', 'browser-created');
  await page.click('#box-form .primary');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('provisioning started'));
  await page.waitForSelector('[data-box="box-3"]');
  assert(requests.some(value => value.method === 'POST' && value.path === '/v1/logical-boxes' && value.body.name === 'browser-created'));
  await page.click('#new-box');
  assert.equal(await page.$eval('#box-form [name="name"]', element => element.value), '');
  await page.click('#box-dialog .close-dialog');

  await page.click('[data-box="box-1"]');
  await page.waitForSelector('[data-task="task-1"].selected');
  await page.waitForFunction(() => document.querySelector('#terminal').textContent.includes('box-1 live agent output'));
  assert.equal(await page.$('#message-form'), null);
  assert.equal(await page.$('#messages'), null);
  assert.equal(await page.$('#terminal-prompt'), null);
  const widths = await page.evaluate(() => ({
    body: document.querySelector('.chat-body').clientWidth,
    terminal: document.querySelector('.terminal-column').clientWidth,
  }));
  assert.equal(widths.terminal, widths.body);
  assert(!requests.some(value => value.path === '/v1/tasks/task-1/messages'));

  assert.equal(await page.$eval('#fullscreen-terminal', element => element.textContent), 'Fullscreen');
  const interactiveStart = requests.filter(value => value.method === 'POST' && value.path === '/v1/logical-boxes/box-1/terminal/input').length;
  await page.click('#terminal');
  await page.keyboard.type('pwd');
  await page.keyboard.press('Enter');
  for (let attempt = 0; attempt < 40 && requests.filter(value => value.method === 'POST' && value.path === '/v1/logical-boxes/box-1/terminal/input').length < interactiveStart + 2; attempt++) {
    await new Promise(resolveWait => setTimeout(resolveWait, 50));
  }
  const interactivePosts = requests.filter(value => value.method === 'POST' && value.path === '/v1/logical-boxes/box-1/terminal/input').slice(interactiveStart);
  assert.equal(interactivePosts[0].body.text, 'pwd');
  assert.equal(interactivePosts[0].body.submit, false);
  assert.deepEqual(interactivePosts[1].body.keys, ['Enter']);

  await page.select('#box-default-agent', 'claude');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('starts claude'));
  assert(requests.some(value => value.method === 'PATCH' && value.path === '/v1/logical-boxes/box-1' && value.body.defaultAgent === 'claude'));

  await page.click('#share-terminal');
  await page.select('#forward-box', 'box-2');
  await page.type('#forward-form [name="append"]', 'Compare this with your findings.');
  await page.click('#forward-form .primary');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('Forwarded to claude-box'));
  const forwarded = requests.find(value => value.method === 'POST' && value.path === '/v1/logical-boxes/box-2/messages');
  assert.match(forwarded.body.text, /box-1 live agent output/);
  assert.match(forwarded.body.text, /Compare this with your findings/);
  assert.equal(forwarded.body.agent, 'claude');

  await page.click('[data-group="group-1"]');
  await page.waitForSelector('.recipient-screen', {visible:true});
  assert.equal(await page.$$eval('#group-members input:checked', elements => elements.length), 0);
  assert.equal(await page.$$eval('.recipient-screen', elements => elements.length), 2);
  await page.click('.recipient-screen');
  await page.waitForSelector('#terminal-preview-dialog[open]');
  assert.match(await page.$eval('#terminal-preview-fullscreen', element => element.textContent), /live agent output/);
  await page.click('#close-terminal-preview');

  const groupPostsBefore = requests.filter(value => value.method === 'POST' && value.path === '/v1/chat-groups/group-1/messages').length;
  await page.type('#group-message', 'Do not broadcast this');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('Select at least one recipient'));
  assert.equal(requests.filter(value => value.method === 'POST' && value.path === '/v1/chat-groups/group-1/messages').length, groupPostsBefore);
  await page.click('#group-members input[value="box-1"]');
  await page.type('#group-message', 'Only Codex receives this');
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('Group message queued'));
  const groupPost = requests.filter(value => value.method === 'POST' && value.path === '/v1/chat-groups/group-1/messages').at(-1);
  assert.deepEqual(groupPost.body.recipientBoxIds, ['box-1']);

  await page.click('[data-box="box-1"]');
  page.once('dialog', dialog => dialog.accept());
  await page.click('#hibernate');
  await page.waitForFunction(() => document.querySelector('#toast').textContent.includes('progress continues if this page closes'));
  await page.waitForFunction(() => document.querySelector('#chat-state').textContent.includes('saving workspace'));
  assert(requests.some(value => value.method === 'POST' && value.path === '/v1/logical-boxes/box-1/hibernate'));

  await page.close();

  const mobile = await browser.newPage();
  await mobile.setViewport({width:390, height:844, isMobile:true});
  await mobile.goto(baseURL, {waitUntil:'networkidle0'});
  await mobile.type('#token', 'browser-test-password');
  await Promise.all([mobile.click('#login-form button[type="submit"]'), mobile.waitForSelector('#app:not([hidden])')]);
  await mobile.click('[data-view="boxes"]');
  await mobile.waitForSelector('[data-box="box-1"]', {visible:true});
  assert.equal(await mobile.evaluate(() => fetch('/favicon.svg').then(response => response.status)), 200);
  await mobile.click('[data-box="box-1"]');
  await mobile.waitForSelector('#chat-view:not([hidden])');
  assert.equal(await mobile.$('#message-form'), null);
  await mobile.waitForSelector('#terminal', {visible:true});
  const layout = await mobile.evaluate(() => {
    const bounds = document.querySelector('#terminal').getBoundingClientRect();
    return {width:bounds.width, height:bounds.height, overflow:document.documentElement.scrollWidth > innerWidth};
  });
  assert(layout.width >= 380);
  assert(layout.height > 300);
  assert.equal(layout.overflow, false);
  assert.deepEqual(errors, []);
  await mobile.close();
});
