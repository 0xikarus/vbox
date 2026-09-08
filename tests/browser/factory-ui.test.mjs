// Real Chromium desktop/mobile UI tests with intercepted API fixtures, not live Factory proof.
// These verify controls, rendering and upload requests, not decoder support or live planning execution.
// npm install --prefix /tmp/factory-ui-tools puppeteer
// VMBOX_FACTORY_PUPPETEER=/tmp/factory-ui-tools/node_modules/puppeteer/lib/puppeteer/puppeteer.js node --test tests/browser/factory-ui.test.mjs
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { before, after, test } from 'node:test';
const { default: puppeteer } = await import(process.env.VMBOX_FACTORY_PUPPETEER || 'puppeteer-core');
const source = await readFile(new URL('../../internal/controller/web/factory.js', import.meta.url), 'utf8');
const css = await readFile(new URL('../../internal/controller/web/factory.css', import.meta.url), 'utf8');
const appCSS = await readFile(new URL('../../internal/controller/web/app.css', import.meta.url), 'utf8');
let browser;
before(async () => {
  browser = await puppeteer.launch({ ...(process.env.VMBOX_CHROMIUM ? { executablePath: process.env.VMBOX_CHROMIUM } : {}), headless: true, args: ['--no-sandbox', '--disable-setuid-sandbox'] });
});
after(async () => { await browser?.close(); });
const task = { id: 'task-1', title: '<img src=x onerror=alert(1)>', description: 'Implement', acceptanceCriteria: ['Works'], dependsOn: [], files: ['a.js'], checks: [{ argv: ['node', '--test'], cwd: '.', timeoutSeconds: 30 }], state: 'pending' };
const record = (id = 'w1', state = 'plan_ready') => ({ id, revision: 1, state, repositoryId: 'r1', repositoryName: 'owner/repo', baseRef: 'main', baseSha: 'abc', agent: 'codex', profile: 'saved', idea: '<script>window.pwned=1</script>', assets: [], messages: [{ id: 'm1', role: 'assistant', text: '<img src=x onerror="window.pwned=1">', assetIds: [] }], plans: [{ revision: 1, markdown: '# Plan\n<script>window.pwned=1</script>', questions: [], features: [structuredClone(task)] }], features: [] });
async function fixture(t, config = {}) {
  const page = await browser.newPage(); t.after(() => page.close());
  await page.setViewport(config.mobile ? { width: 390, height: 844, isMobile: true, hasTouch: true } : { width: 1280, height: 900 });
  const requests = [], errors = []; page.on('pageerror', e => errors.push(e.message));
  t.after(() => assert.deepEqual(errors, []));
  await page.setRequestInterception(true);
  const state = { items: [record()], caps: { enabled: true, githubConfigured: true, agents: [{ name: 'codex', images: true }, { name: 'claude', images: false }] }, ...config };
  page.on('request', async req => {
    const url = new URL(req.url()), path = url.pathname;
    const respond = (data, status = 200) => req.respond({ status, contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/') return req.respond({ status: 200, contentType: 'text/html', body: `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><style>${appCSS}\n${css}</style><main id="root"></main><script type="module">import {mountFactory} from '/factory.js'; window.mount = () => { window.dispose = mountFactory(document.querySelector('#root'), async (path, method='GET', body, headers={}) => { const r = await fetch(path, {method, credentials:'same-origin', headers:{'Content-Type':'application/json',...headers}, body:body===undefined?undefined:JSON.stringify(body)}); let data; try {data=await r.json()} catch {} if (!r.ok) throw Error(data?.error || 'HTTP '+r.status); return data; }); }; window.mount();</script>` });
    if (path === '/factory.js') return req.respond({ status: 200, contentType: 'text/javascript', body: source });
    if (path.startsWith('/v1/factory')) {
      const entry = { path: path.slice('/v1/factory'.length), query: url.search, method: req.method(), headers: req.headers(), raw: req.postData() };
      if (entry.raw && !path.endsWith('/assets')) entry.body = JSON.parse(entry.raw);
      requests.push(entry);
      if (state.handler && await state.handler(entry, respond, req)) return;
      if (entry.path === '/capabilities') return respond(state.caps);
      if (entry.path === '/repositories') return respond(state.repos || [{ id: 'r1', fullName: 'owner/repo', defaultBranch: 'main' }]);
      if (entry.path === '/profiles') return respond([{ application: 'codex', name: 'saved' }, { application: 'claude', name: 'other' }]);
      if (entry.path === '/work-items' && entry.method === 'GET') return respond(state.items);
      if (entry.path === '/assets') return respond({ id: 'asset-1', name: 'pixel.png', mediaType: 'image/png', size: 68 });
      if (entry.path.startsWith('/assets/')) return req.respond({ status: 200, contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j3ioAAAAASUVORK5CYII=', 'base64') });
      if (entry.path === '/work-items' && entry.method === 'POST') return respond({ ...record('created', 'planning_queued'), idea: entry.body.idea, plans: [], messages: [] });
      const item = state.items.find(i => entry.path === '/work-items/' + encodeURIComponent(i.id));
      if (item) return respond(item);
      return respond({ error: 'Unexpected fixture request' }, 404);
    }
    return req.respond({ status: 404, body: '' });
  });
  await page.setCookie({ name: 'browser_session', value: 'fixture-cookie', url: 'https://factory.test', httpOnly: true, secure: true });
  await page.goto('https://factory.test');
  await page.waitForFunction(() => !document.querySelector('[role=status]').textContent.includes('Loading'));
  return { page, state, requests };
}
async function choose(page) {
  await page.select('[name=repository]', 'r1'); await page.select('[name=agent]', 'codex'); await page.select('[name=profile]', 'saved');
  await page.type('[name=idea]', 'Make a useful planning UI');
}
async function saved(page, id = 'w1') {
  await page.waitForSelector(`[name=workItem] option[value="${id}"]`); await page.select('[name=workItem]', id);
  await page.waitForFunction(id => document.querySelector('.factory > section:last-of-type').textContent.includes('Work ' + id), {}, id);
}
async function clickText(page, label) { await page.evaluate(label => Array.from(document.querySelectorAll('button')).find(b => b.textContent === label).click(), label); }
async function upload(page, name = 'images', type = 'image/png') {
  await page.evaluate(({ name, type }) => {
    const bytes = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j3ioAAAAASUVORK5CYII='), c => c.charCodeAt(0));
    const dt = new DataTransfer(); dt.items.add(new File([bytes], type === 'image/png' ? 'pixel.png' : 'fixture-image', { type }));
    const input = document.querySelector(`[name=${name}]`); input.files = dt.files; input.dispatchEvent(new Event('change', { bubbles: true }));
  }, { name, type });
}
for (const mobile of [false, true]) test(`${mobile ? 'mobile' : 'desktop'}: native inputs, cookie upload, removal, separate Plan, duplicate suppression`, async t => {
  let release;
  const { page, requests } = await fixture(t, { mobile, handler: async (r, respond) => {
    if (r.path === '/work-items' && r.method === 'POST') { release = () => respond({ ...record('created', 'planning_queued'), messages: [], plans: [] }); return true; }
  } });
  await choose(page); assert.equal(await page.$eval('[name=baseRef]', e => e.value), 'main');
  await upload(page); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  const uploaded = requests.find(r => r.path === '/assets');
  assert.match(uploaded.headers.cookie, /browser_session=fixture-cookie/); assert.match(uploaded.headers['content-type'], /^multipart\/form-data; boundary=/); assert(uploaded.headers['idempotency-key']);
  const objectURL = await page.$eval('figure img', e => e.src); assert(objectURL.startsWith('blob:'));
  await clickText(page, 'Remove'); assert.equal(await page.$$eval('figure', e => e.length), 0);
  await upload(page); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  await page.evaluate(() => { const f = document.querySelector('.factory form'); f.dispatchEvent(new Event('submit', { cancelable: true })); f.dispatchEvent(new Event('submit', { cancelable: true })); });
  await page.waitForFunction(() => document.querySelector('[name=idea]').disabled || document.querySelector('[name=idea]').closest('fieldset').disabled);
  await new Promise(r => setTimeout(r, 100)); assert.equal(requests.filter(r => r.method === 'POST' && r.path === '/work-items').length, 1);
  const sent = requests.find(r => r.method === 'POST' && r.path === '/work-items');
  assert.deepEqual(sent.body, { repositoryId: 'r1', baseRef: 'main', idea: 'Make a useful planning UI', agent: 'codex', profile: 'saved', assetIds: ['asset-1'] });
  await release(); await page.waitForFunction(() => document.body.textContent.includes('Work created'));
  assert(!requests.some(r => r.path.endsWith('/approve'))); assert.equal(await page.$eval('[name=idea]', e => e.value), '');
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await page.screenshot({ path: `/tmp/factory-${mobile ? 'mobile' : 'desktop'}.png`, fullPage: true });
});
test('failed Plan retains inputs and unchanged retry reuses key', async t => {
  let count = 0;
  const { page, requests } = await fixture(t, { handler: async (r, respond) => {
    if (r.path === '/work-items' && r.method === 'POST' && ++count === 1) { await respond({ error: 'Temporary HTTP failure' }, 503); return true; }
  } });
  await choose(page); await clickText(page, 'Plan'); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Temporary HTTP failure'));
  assert.equal(await page.$eval('[name=idea]', e => e.value), 'Make a useful planning UI');
  await clickText(page, 'Plan'); await page.waitForFunction(() => document.body.textContent.includes('Work created'));
  const posts = requests.filter(r => r.path === '/work-items' && r.method === 'POST'); assert.equal(posts.length, 2); assert.equal(posts[0].headers['idempotency-key'], posts[1].headers['idempotency-key']);
});
test('saved text is inert; revisions, DAG, safe evidence links and approval are rendered', async t => {
  const item = record(); item.features = [{ ...task, boxId: 'b/1', issueUrl: 'https://github.com/owner/repo/issues/1', prUrl: 'javascript:alert(1)' }];
  item.plans.push({ revision: 0, markdown: 'Older plan', questions: ['Earlier question'], features: [] });
  const { page, requests } = await fixture(t, { items: [item], handler: async (r, respond) => {
    if (r.path.endsWith('/approve')) { await respond({ ...item, state: 'build_queued' }); return true; }
  } });
  await saved(page);
  assert.equal(await page.evaluate(() => window.pwned), undefined); assert.equal(await page.$$eval('.factory script, .factory img[onerror]', n => n.length), 0);
  assert.match(await page.$eval('.factory', e => e.textContent), /<script>window.pwned=1<\/script>/);
  assert.equal(await page.$$eval('details', n => n.length), 2); assert.equal(await page.$$eval('a[href^="javascript:"]', n => n.length), 0);
  assert.equal(await page.$eval('a[href^="/boxes/"]', n => n.getAttribute('href')), '/boxes/b%2F1');
  await page.$eval('[name=maxWorkers]', n => { n.value = '2'; }); await clickText(page, 'Approve & build');
  await page.waitForFunction(() => document.body.textContent.includes('build_queued'));
  assert.deepEqual(requests.find(r => r.path.endsWith('/approve')).body, { expectedRevision: 1, planRevision: 1, maxWorkers: 2 });
});
test('questions and reply with image; 409 preserves draft and retry key', async t => {
  const item = record('w1', 'needs_clarification'); item.plans[0].questions = ['Which color?']; let attempts = 0;
  const { page, requests } = await fixture(t, { items: [item], handler: async (r, respond) => {
    if (r.path.endsWith('/messages')) { await respond(++attempts === 1 ? { error: 'Conflicting revision' } : { ...item, revision: 2, state: 'planning_queued' }, attempts === 1 ? 409 : 200); return true; }
  } });
  await saved(page); assert.equal(await page.$eval('[data-approve]', n => n.disabled), true);
  await page.type('[name=reply]', 'Use blue'); await upload(page, 'replyImages'); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  await clickText(page, 'Send reply / revise'); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Conflicting revision'));
  assert.equal(await page.$eval('[name=reply]', n => n.value), 'Use blue');
  await clickText(page, 'Send reply / revise'); await page.waitForFunction(() => document.body.textContent.includes('input revision 2'));
  const posts = requests.filter(r => r.path.endsWith('/messages')); assert.deepEqual(posts[0].body, { text: 'Use blue', assetIds: ['asset-1'], expectedRevision: 1 }); assert.equal(posts[0].headers['idempotency-key'], posts[1].headers['idempotency-key']);
});
test('late selected-work response cannot replace current selection; drafts survive selection', async t => {
  let release;
  const { page } = await fixture(t, { items: [record('w1'), record('w2')], handler: async (r, respond) => {
    if (r.path === '/work-items/w1') { release = () => respond(record('w1')); return true; }
  } });
  await page.select('[name=workItem]', 'w1'); await page.waitForFunction(() => document.body.textContent.includes('Loading saved work'));
  await saved(page, 'w2'); await page.type('[name=reply]', 'Draft for second item'); await release();
  await new Promise(r => setTimeout(r, 100)); assert.match(await page.$eval('.factory > section:last-of-type', n => n.textContent), /Work w2/);
  await page.select('[name=workItem]', ''); await saved(page, 'w2'); assert.equal(await page.$eval('[name=reply]', n => n.value), 'Draft for second item');
});
test('poll failures stop until explicit reconnect; hidden/unmounted views do not poll', async t => {
  let reads = 0;
  const { page, requests, state } = await fixture(t, { items: [record('w1', 'planning')], handler: async (r, respond) => {
    if (r.path === '/work-items/w1' && ++reads === 2) { await respond({ error: 'Disconnected' }, 503); return true; }
  } });
  await saved(page); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Disconnected'), { timeout: 6000 });
  const count = requests.length; await new Promise(r => setTimeout(r, 3200)); assert.equal(requests.length, count);
  state.items = [record('w1', 'planning')]; await clickText(page, 'Refresh / reconnect'); await page.waitForFunction(() => !document.querySelector('[role=alert]').textContent);
  await page.evaluate(() => { document.querySelector('#root').hidden = true; });
  await new Promise(r => setTimeout(r, 100)); const hiddenCount = requests.length; await new Promise(r => setTimeout(r, 3200)); assert.equal(requests.length, hiddenCount);
  await page.evaluate(() => window.dispose()); await new Promise(r => setTimeout(r, 3200)); assert.equal(requests.length, hiddenCount);
});
test('reload restores saved selection; pagination uses opaque cursor', async t => {
  const { page, requests } = await fixture(t, { items: [], handler: async (r, respond) => {
    if (r.path === '/work-items' && r.method === 'GET') { await respond(r.query ? { items: [record('w2')] } : { items: [record('w1')], nextCursor: 'next/+?' }); return true; }
    if (r.path === '/work-items/w1' || r.path === '/work-items/w2') { await respond(record(r.path.split('/').at(-1))); return true; }
  } });
  await saved(page); await clickText(page, 'Load more work'); await page.waitForSelector('[name=workItem] option[value=w2]');
  assert(requests.some(r => r.query === '?cursor=next%2F%2B%3F'));
  await saved(page, 'w2');
  await page.reload(); await page.waitForFunction(() => document.body.textContent.includes('Work w2'));
});
test('upload failures are retryable; text-only agents cannot submit images', async t => {
  let uploads = 0;
  const { page, requests } = await fixture(t, { handler: async (r, respond) => {
    if (r.path === '/assets' && ++uploads === 1) { await respond({ error: 'Upload unavailable' }, 500); return true; }
  } });
  await choose(page); await upload(page); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Upload unavailable'));
  assert.equal(await page.$eval('form button[type=submit]', n => n.disabled), true);
  await clickText(page, 'Retry upload'); await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  const uploadsSent = requests.filter(r => r.path === '/assets'); assert.equal(uploadsSent[0].headers['idempotency-key'], uploadsSent[1].headers['idempotency-key']);
  await page.select('[name=agent]', 'claude'); await page.select('[name=profile]', 'other'); assert.equal(await page.$eval('form button[type=submit]', n => n.disabled), true);
  await clickText(page, 'Remove'); assert.equal(await page.$eval('form button[type=submit]', n => n.disabled), false);
});
test('disabled and empty repository states explain setup', async t => {
  const { page, requests, state } = await fixture(t, { caps: { enabled: false } });
  assert.match(await page.$eval('[role=status]', n => n.textContent), /disabled/); assert.equal(requests.length, 1);
  state.caps = { enabled: true, githubConfigured: false, agents: [] }; state.repos = [];
  await clickText(page, 'Refresh / reconnect'); await page.waitForFunction(() => document.querySelector('[role=status]').textContent.includes('Connect the GitHub App'));
  assert.equal(await page.$eval('fieldset', n => n.disabled), true);
});
test('cyclic tasks, missing checks and stale plans block approval', async t => {
  const item = record(); item.plans[0].features[0].dependsOn = ['task-1'];
  const { page, state } = await fixture(t, { items: [item] });
  await saved(page); assert.equal(await page.$eval('[data-approve]', n => n.disabled), true);
  for (const change of [i => { i.plans[0].features[0].dependsOn = []; i.plans[0].features[0].checks = []; }, i => { i.plans[0].features[0].checks = task.checks; i.revision = 2; }]) {
    change(state.items[0]); await clickText(page, 'Refresh / reconnect'); await new Promise(r => setTimeout(r, 100)); assert.equal(await page.$eval('[data-approve]', n => n.disabled), true);
  }
});
test('pending reply image completes across work selection and object URLs are released on unmount', async t => {
  let release;
  const { page } = await fixture(t, { items: [record('w1'), record('w2')], handler: async (r, respond) => {
    if (r.path === '/assets') { release = () => respond({ id: 'asset-1', name: 'pixel.png' }); return true; }
  } });
  await saved(page); await upload(page, 'replyImages');
  await page.waitForSelector('figure img'); const url = await page.$eval('figure img', n => n.src);
  await saved(page, 'w2'); await release(); await saved(page, 'w1');
  await page.waitForFunction(() => document.querySelector('figcaption')?.textContent.includes('Uploaded'));
  await page.evaluate(() => window.dispose());
  assert.equal(await page.evaluate(async url => { try { await fetch(url); return false; } catch { return true; } }, url), true);
});
test('client image count, byte and format limits reject before uploading', async t => {
  const { page, requests } = await fixture(t); await choose(page);
  for (const kind of ['count', 'size', 'total', 'format']) {
    await page.evaluate(kind => {
      const dt = new DataTransfer();
      const count = kind === 'count' ? 9 : kind === 'total' ? 5 : 1;
      const size = kind === 'size' ? 11 * 1024 * 1024 : kind === 'total' ? 9 * 1024 * 1024 : 1;
      for (let i = 0; i < count; i++) dt.items.add(new File([new Uint8Array(size)], 'test', { type: kind === 'format' ? 'image/svg+xml' : 'image/png' }));
      const input = document.querySelector('[name=images]'); input.files = dt.files; input.dispatchEvent(new Event('change', { bubbles: true }));
    }, kind);
    assert.match(await page.$eval('[role=alert]', n => n.textContent), /at most 8/);
  }
  assert.equal(requests.filter(r => r.path === '/assets').length, 0);
});
test('failed submission fingerprint survives reload without persisting draft text', async t => {
  const { page, requests } = await fixture(t, { handler: async (r, respond) => {
    if (r.path === '/work-items' && r.method === 'POST') { await respond({ error: 'Response lost' }, 502); return true; }
  } });
  await choose(page); await clickText(page, 'Plan'); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Response lost'));
  assert.equal(await page.evaluate(() => JSON.stringify(sessionStorage).includes('Make a useful')), false);
  await page.reload(); await page.waitForFunction(() => !document.querySelector('[role=status]').textContent.includes('Loading'));
  await choose(page); await clickText(page, 'Plan'); await page.waitForFunction(() => document.querySelector('[role=alert]').textContent.includes('Response lost'));
  const posts = requests.filter(r => r.path === '/work-items' && r.method === 'POST'); assert.equal(posts.length, 2); assert.equal(posts[0].headers['idempotency-key'], posts[1].headers['idempotency-key']);
});
test('read response started before reply cannot overwrite its newer revision', async t => {
  let release, reads = 0;
  const { page } = await fixture(t, { handler: async (r, respond) => {
    if (r.path === '/work-items/w1' && ++reads === 2) { release = () => respond(record()); return true; }
    if (r.path.endsWith('/messages')) { await respond({ ...record(), revision: 2, state: 'planning_queued' }); return true; }
  } });
  await saved(page); await page.type('[name=reply]', 'Change the plan'); await clickText(page, 'Refresh / reconnect');
  while (!release) await new Promise(r => setTimeout(r, 10));
  await clickText(page, 'Send reply / revise'); await page.waitForFunction(() => document.body.textContent.includes('input revision 2'));
  await release(); await new Promise(r => setTimeout(r, 100)); assert.match(await page.$eval('.factory > section:last-of-type', n => n.textContent), /input revision 2/);
});

for (const mobile of [false, true]) {
  const viewport = mobile ? 'mobile' : 'desktop';
  test(`${viewport}: executionReady false blocks Plan/reply and preserves saved history and drafts`, async t => {
    const caps = { enabled: true, githubConfigured: true, executionReady: false, imageTypes: ['image/png', 'image/jpeg'], agents: [{ name: 'codex', images: true }] };
    const { page, state, requests } = await fixture(t, { mobile, caps, handler: async (r, respond) => {
      if (r.path === '/work-items' && r.method === 'GET') {
        await respond(r.query ? { items: [record('w2')] } : { items: [record()], nextCursor: 'next' }); return true;
      }
      if (r.path === '/work-items/w2') { await respond(record('w2')); return true; }
    } });
    assert.match(await page.$eval('[role=status]', n => n.textContent), /configuration required.*will not be queued.*Saved work remains available/);
    assert.equal(await page.$eval('form button[type=submit]', n => n.disabled), true);
    assert.equal(await page.$eval('[name=workItem]', n => n.disabled), false);
    await saved(page);
    assert.equal(await page.$eval('[name=reply]', n => n.matches(':disabled')), true);
    assert.equal(await page.$eval('[data-approve]', n => n.disabled), true);
    await clickText(page, 'Load more work'); await saved(page, 'w2');
    assert.match(await page.$eval('details[open]', n => n.textContent), /Plan revision 1/);
    state.caps.executionReady = true;
    await clickText(page, 'Refresh / reconnect');
    await page.waitForFunction(() => !document.querySelector('[name=idea]').matches(':disabled'));
    await choose(page); await page.type('[name=reply]', 'Keep this draft');
    state.caps.executionReady = false;
    await clickText(page, 'Refresh / reconnect');
    await page.waitForFunction(() => document.querySelector('[name=idea]').matches(':disabled'));
    await page.evaluate(() => {
      for (const name of ['idea', 'reply']) document.querySelector(`[name=${name}]`).closest('form').dispatchEvent(new Event('submit', { cancelable: true }));
    });
    assert.equal(await page.$eval('form button[type=submit]', n => n.disabled), true);
    assert.equal(await page.$eval('[name=reply]', n => n.closest('form').querySelector('button[type=submit]').disabled), true);
    await saved(page); await saved(page, 'w2');
    assert.equal(await page.$eval('[name=reply]', n => n.value), 'Keep this draft');
    assert.equal(await page.$eval('[name=idea]', n => n.value), 'Make a useful planning UI');
    assert.equal(requests.filter(r => r.method === 'POST').length, 0);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  });
  test(`${viewport}: latest actual planning attempt and safe error survive generic work state`, async t => {
    const item = record('w1', 'planning'); item.revision = 3;
    item.attempts = [{ id: 'old', revision: 1, state: 'failed' }, { id: 'latest', revision: 3, state: 'creation-initializing' }, { id: 'stale', revision: 2, state: 'queued' }];
    const { page, state } = await fixture(t, { mobile, items: [item] });
    await saved(page);
    assert.match(await page.$eval('.factory-attempt', n => n.textContent), /latest · input revision 3 · creation-initializing/);
    const diagnostic = 'The delivery process exited without a durable agent result; its exit code is not the agent outcome. Inspect the saved attempt receipt.';
    for (const attemptState of ['result_missing', 'not_started']) {
      state.items[0].attempts.push({ id: 'retry-' + attemptState, revision: 3, state: attemptState, credentials: 'must-not-render', internalError: 'private-diagnostic' });
      state.items[0].error = attemptState === 'result_missing' ? diagnostic : 'Planning execution is not configured. <img src=x onerror="window.pwned=1">';
      await clickText(page, 'Refresh / reconnect');
      await page.waitForFunction(s => document.querySelector('.factory-attempt').textContent.includes('retry-' + s), {}, attemptState);
      assert.match(await page.$eval('.factory-attempt', n => n.textContent), new RegExp(`retry-${attemptState} · input revision 3 · ${attemptState}`));
      assert.equal(await page.$eval('.factory > section:last-of-type .factory-error', n => n.textContent), state.items[0].error);
      assert.equal(await page.evaluate(() => window.pwned), undefined);
      assert.doesNotMatch(await page.$eval('.factory', n => n.textContent), /must-not-render|private-diagnostic/);
    }
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  });
  test(`${viewport}: both upload composers advertise and validate only supported PNG/JPEG formats`, async t => {
    const { page, state, requests } = await fixture(t, { mobile });
    state.caps.imageTypes = ['image/png', 'image/jpeg', 'image/webp'];
    await clickText(page, 'Refresh / reconnect');
    await page.waitForSelector('[name=repository] option[value=r1]'); await choose(page); await saved(page);
    for (const name of ['images', 'replyImages']) {
      assert.equal(await page.$eval(`[name=${name}]`, n => n.accept), 'image/png,image/jpeg');
      assert.match(await page.$eval(`[name=${name}]`, n => n.closest('.factory-images').textContent), /PNG \/ JPEG/);
      assert.doesNotMatch(await page.$eval(`[name=${name}]`, n => n.closest('.factory-images').textContent), /WebP/i);
      const before = requests.filter(r => r.path === '/assets').length;
      await upload(page, name, 'image/webp');
      assert.match(await page.$eval('[role=alert]', n => n.textContent), /advertised formats \(PNG \/ JPEG\)/);
      assert.equal(requests.filter(r => r.path === '/assets').length, before);
      for (const type of ['image/png', 'image/jpeg']) {
        await upload(page, name, type);
        await page.waitForFunction(name => [...document.querySelector(`[name=${name}]`).closest('.factory-images').querySelectorAll('figcaption')].some(n => n.textContent.includes('Uploaded')), {}, name);
        await page.$eval(`[name=${name}]`, n => [...n.closest('.factory-images').querySelectorAll('button')].find(b => b.textContent === 'Remove').click());
      }
      assert.equal(requests.filter(r => r.path === '/assets').length, before + 2);
    }
    state.caps.imageTypes = ['image/jpeg'];
    await clickText(page, 'Refresh / reconnect');
    await page.waitForFunction(() => document.querySelector('[name=images]').accept === 'image/jpeg');
    const before = requests.filter(r => r.path === '/assets').length;
    await upload(page); assert.equal(requests.filter(r => r.path === '/assets').length, before);
    assert.match(await page.$eval('[role=alert]', n => n.textContent), /advertised formats \(JPEG\)/);
    state.caps.imageTypes = [];
    await clickText(page, 'Refresh / reconnect');
    await page.waitForFunction(() => document.querySelector('[name=images]').disabled);
    assert.match(await page.$eval('.factory-images', n => n.textContent), /No image formats advertised/);
  });
}
