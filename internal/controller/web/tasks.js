// Caller owns navigation, authentication and tasks.css loading. JSON API convention:
// api(path, method = 'GET', body?, headers?) -> parsed JSON, rejecting non-2xx.
// Multipart assets use same-origin fetch, as in factory.js. No controller secrets.
const API = '/v1/factory';
const array = v => Array.isArray(v) ? v : [];
const active = s => ['planning_queued', 'planning', 'running', 'synthesizing', 'cancelling'].includes(s);
const replyable = s => ['awaiting_reply', 'awaiting_approval', 'completed', 'needs_revision', 'failed', 'cancelled'].includes(s);
const terminalFailure = a => a.state === 'result_missing' || a.state === 'exited' && (Boolean(a.failure) || a.signal > 0 || Number.isInteger(a.exitCode) && a.exitCode !== 0);
const mounts = new WeakMap();
const latestPlan = w => array(w?.plans).reduce((best, p) => !best || p.revision > best.revision ? p : best, null);

function validPlan(p) {
  const assignments = array(p?.assignments);
  if (!Number.isSafeInteger(p?.revision) || p.revision < 1 || array(p.questions).length || assignments.length < 1 || assignments.length > 20) return false;
  const byID = new Map(assignments.map(a => [a.id, a]));
  if (byID.size !== assignments.length || assignments.some(a => typeof a.id !== 'string' || !a.id.trim() || !array(a.acceptanceCriteria).length || a.acceptanceCriteria.some(c => typeof c !== 'string' || !c.trim()))) return false;
  const visiting = new Set(), done = new Set();
  function visit(id) {
    if (!byID.has(id) || visiting.has(id)) return false;
    if (done.has(id)) return true;
    visiting.add(id);
    if (!array(byID.get(id).dependsOn).every(visit)) return false;
    visiting.delete(id); done.add(id); return true;
  }
  return assignments.every(a => visit(a.id));
}

export function mountTasks(root, api) {
  mounts.get(root)?.();
  const doc = root.ownerDocument, win = doc.defaultView;
  let disposed = false, busy = false, stopped = false, timer, generation = 0, setupGeneration = 0;
  let caps, profiles = [], items = [], selected = '', work;
  const drafts = new Map(), intents = new Map(), uploads = new Set(), entries = [];
  const el = (tag, value, className) => {
    const n = doc.createElement(tag);
    if (value != null) n.textContent = String(value);
    if (className) n.className = className;
    return n;
  };
  const button = (label, fn) => { const n = el('button', label); n.type = 'button'; n.addEventListener('click', fn); return n; };
  const field = (parent, label, tag, name) => { const wrap = el('label', label), n = el(tag); n.name = name; wrap.append(n); parent.append(wrap); return n; };
  const option = (select, label, value) => { const n = el('option', label); n.value = value; select.append(n); };
  const shell = el('section', null, 'tasks');
  const notice = el('p', 'Loading Tasks…'); notice.setAttribute('role', 'status');
  const error = el('p', '', 'tasks-error'); error.setAttribute('role', 'alert');
  const refreshButton = button('Refresh / reconnect', refresh);
  shell.append(el('h2', 'Tasks'), notice, error, refreshButton);
  const form = el('form'), fields = el('fieldset'); fields.append(el('legend', 'New task'));
  const idea = field(fields, 'Task idea', 'textarea', 'idea'); idea.required = true; idea.rows = 5;
  const agent = field(fields, 'Coordinator agent', 'select', 'agent'); agent.required = true;
  const profile = field(fields, 'Saved profile', 'select', 'profile'); profile.required = true;
  const workers = field(fields, 'Maximum workers', 'input', 'maxWorkers'); workers.type = 'number'; workers.min = '1'; workers.step = '1'; workers.value = '1'; workers.required = true;
  const images = field(fields, 'Optional PNG / JPEG attachments', 'input', 'images'); images.type = 'file'; images.multiple = true; images.accept = 'image/png,image/jpeg';
  fields.append(el('p', 'Up to 8 images, 10 MiB each, 40 MiB total. Server validates the 25 MP limit.'));
  const imageNotice = el('p'), gallery = el('div', null, 'tasks-gallery'); fields.append(imageNotice, gallery);
  const create = el('button', 'Request plan'); create.type = 'submit'; fields.append(create); form.append(fields); shell.append(form);
  const picker = field(shell, 'Saved task', 'select', 'task');
  const detail = el('section', null, 'tasks-detail'); detail.setAttribute('aria-label', 'Selected task'); shell.append(detail);
  const replyForm = el('form'), replyFields = el('fieldset'); replyFields.append(el('legend', 'Reply / refine'));
  const reply = field(replyFields, 'Answer questions or request changes', 'textarea', 'reply'); reply.rows = 4; reply.required = true;
  const send = el('button', 'Send reply / refine'); send.type = 'submit'; replyFields.append(send); replyForm.append(replyFields); shell.append(replyForm);
  root.replaceChildren(shell);

  const ready = () => caps?.enabled === true && caps?.executionReady === true;
  const canRun = () => ready() && work?.state === 'awaiting_approval' && validPlan(latestPlan(work));
  const canRetry = a => ready() && !active(work?.state) && work?.state !== 'cancelled' && terminalFailure(a);
  const canCancel = () => caps?.enabled === true && ['planning_queued', 'planning', 'awaiting_reply', 'awaiting_approval', 'running', 'synthesizing'].includes(work?.state);
  function controls() {
    fields.disabled = busy || !ready(); picker.disabled = busy; refreshButton.disabled = busy;
    const imageCapable = array(caps?.agents).some(a => a.name === agent.value && a.images === true);
    imageNotice.textContent = entries.length && !imageCapable ? 'This agent does not support images. Choose an image-capable agent or remove the attachments.' : '';
    const count = Number(workers.value);
    create.disabled = fields.disabled || !idea.value.trim() || !agent.value || !profile.value || !Number.isSafeInteger(count) || count < 1 || count > Number(workers.max) || entries.some(e => !e.asset) || entries.length > 0 && !imageCapable;
    replyForm.hidden = !work;
    replyFields.disabled = busy || !ready() || !replyable(work?.state);
    send.disabled = replyFields.disabled || !reply.value.trim();
    detail.querySelectorAll('[data-run]').forEach(b => { b.disabled = busy || !canRun(); });
    detail.querySelectorAll('[data-cancel]').forEach(b => { b.disabled = busy || !canCancel(); });
    detail.querySelectorAll('[data-retry]').forEach(b => { b.disabled = busy || !canRetry(array(work?.attempts).find(a => a.id === b.dataset.retry)); });
    gallery.querySelectorAll('button').forEach(b => { b.disabled = busy; });
  }
  function fail(err) {
    if (disposed) return;
    stopped = true; win.clearTimeout(timer);
    error.textContent = (err?.message || String(err)) + ' Your draft is retained. Retry an unchanged request to reuse its key, or Refresh / reconnect for the current task version.';
  }
  function profileChoices() {
    const previous = profile.value; profile.replaceChildren(); option(profile, 'Choose saved profile', '');
    profiles.filter(p => p.application === agent.value).forEach(p => option(profile, p.name, p.name));
    if (Array.from(profile.options).some(o => o.value === previous)) profile.value = previous;
    controls();
  }
  function renderPicker() {
    picker.replaceChildren(); option(picker, 'Choose saved task', '');
    items.forEach(w => option(picker, `${w.idea || w.id} · ${w.state} · ${w.id}`, w.id));
    if (selected && !items.some(w => w.id === selected)) option(picker, selected, selected);
    picker.value = selected;
  }
  function disclosure(parent, key, label, open = false) {
    const n = el('details'), summary = el('summary', label); summary.dataset.focus = key;
    n.dataset.key = key; n.open = open; n.append(summary); parent.append(n); return n;
  }
  function renderWork() {
    const expanded = new Map(Array.from(detail.querySelectorAll('details'), n => [n.dataset.key, n.open]));
    const focused = detail.contains(doc.activeElement) ? doc.activeElement?.dataset.focus : null;
    detail.replaceChildren();
    if (!work) { detail.append(el('p', selected ? 'Loading saved task…' : 'Select a task to view its plan and results.')); controls(); return; }
    detail.append(el('h3', `Task ${work.id}`), el('p', `Version ${work.version} · ${work.state}`), el('p', `${work.agent} · profile ${work.profile} · maximum workers ${work.maxWorkers}`), el('pre', work.idea));
    for (const id of array(work.assetIds)) {
      const a = el('a', `Attachment ${id}`); a.href = API + '/assets/' + encodeURIComponent(id); a.target = '_blank'; a.rel = 'noopener noreferrer'; detail.append(a);
    }
    if (work.failure) detail.append(el('p', work.failure, 'tasks-error'));
    detail.append(el('h4', 'Conversation'));
    for (const m of array(work.messages)) { const a = el('article'); a.append(el('strong', `${m.role} · ${m.createdAt || ''}`), el('pre', m.text)); detail.append(a); }
    detail.append(el('h4', 'Coordinator plans'));
    const latest = latestPlan(work);
    if (!latest) detail.append(el('p', 'No coordinator plan recorded yet.'));
    for (const p of [...array(work.plans)].sort((a, b) => b.revision - a.revision)) {
      const plan = disclosure(detail, `plan:${p.revision}`, `Plan revision ${p.revision}${work.approvedRevision === p.revision ? ' · approved' : ''}`, p === latest);
      plan.append(el('pre', p.summary));
      if (array(p.questions).length) { plan.append(el('h5', 'Questions')); const qs = el('ul'); p.questions.forEach(q => qs.append(el('li', q))); plan.append(qs); }
      const assignments = el('ol');
      for (const a of array(p.assignments)) {
        const li = el('li'); li.append(el('strong', `${a.id}: ${a.title}`), el('pre', a.instruction), el('p', `Depends on: ${array(a.dependsOn).join(', ') || 'None'}`));
        const criteria = el('ul'); array(a.acceptanceCriteria).forEach(c => criteria.append(el('li', c))); li.append(el('p', 'Acceptance criteria'), criteria); assignments.append(li);
      }
      plan.append(assignments);
    }
    const run = button('Run approved plan', () => { if (canRun()) action('run', { planRevision: latestPlan(work).revision }); }); run.dataset.run = ''; run.dataset.focus = 'run'; detail.append(run);
    if (work.state === 'awaiting_approval' && !validPlan(latest)) detail.append(el('p', 'Run requires answered questions and a plan with 1–20 uniquely identified assignments, nonempty criteria and valid acyclic dependencies. Request a refinement.'));
    detail.append(el('p', 'Run approves exactly the displayed latest plan revision. Process exit 0 alone is not acceptance; the coordinator judges actual outputs.'));
    detail.append(el('h4', 'Worker tree'));
    const tree = el('ul', null, 'tasks-tree');
    const groups = new Map([['plan', 'Coordinator · planning'], ['synthesize', 'Coordinator · synthesis']]);
    for (const p of array(work.plans)) for (const a of array(p.assignments)) groups.set('work:' + a.id, `${a.id}: ${a.title}`);
    for (const a of array(work.attempts)) { const key = a.stage === 'work' ? 'work:' + a.assignmentId : a.stage; if (!groups.has(key)) groups.set(key, a.assignmentId || a.stage); }
    for (const [key, label] of groups) {
      const node = el('li'); node.append(el('strong', label));
      const attempts = array(work.attempts).filter(a => (a.stage === 'work' ? 'work:' + a.assignmentId : a.stage) === key);
      if (!attempts.length) node.append(el('p', 'No attempt recorded.'));
      for (const a of attempts) {
        const d = disclosure(node, `attempt:${a.id}`, `Attempt ${a.id} · ${a.state}`);
        d.append(el('p', `Stage: ${a.stage} · ${Number.isInteger(a.exitCode) ? 'exit ' + a.exitCode : 'exit not observed'}${a.signal ? ' · signal ' + a.signal : ''}`));
        if (a.boxId) { const link = el('a', `Box ${a.boxId}`); link.href = '/boxes/' + encodeURIComponent(a.boxId); d.append(link); }
        if (a.taskId) d.append(el('p', `Process task: ${a.taskId}`));
        d.append(el('h5', 'Actual output'), el('pre', a.output || 'No output recorded.'));
        if (a.failure) d.append(el('p', a.failure, 'tasks-error'));
        if (terminalFailure(a)) { const retry = button(`Retry attempt ${a.id}`, () => { if (canRetry(a)) action('retry', { attemptId: a.id }); }); retry.dataset.retry = a.id; retry.dataset.focus = `retry:${a.id}`; d.append(retry); }
      }
      tree.append(node);
    }
    detail.append(tree);
    const cancel = button('Cancel task', () => { if (canCancel()) action('cancel'); }); cancel.dataset.cancel = ''; cancel.dataset.focus = 'cancel'; detail.append(cancel);
    if (work.state === 'cancelling') detail.append(el('p', 'Cancellation requested. New dispatch is stopped; running processes are still being observed until terminal.'));
    detail.append(el('h4', 'Final synthesis'), el('p', `Verdict: ${work.verdict || 'Not recorded'}`), el('pre', work.final || 'No final synthesis recorded.'));
    detail.querySelectorAll('details').forEach(n => { if (expanded.has(n.dataset.key)) n.open = expanded.get(n.dataset.key); });
    if (focused) Array.from(detail.querySelectorAll('[data-focus]')).find(n => n.dataset.focus === focused)?.focus();
    controls();
  }
  function accept(value) {
    if (!value?.id || !Number.isSafeInteger(value.version)) throw Error('Invalid task response');
    if (work?.id === value.id && value.version < work.version) return;
    work = value;
    const i = items.findIndex(w => w.id === value.id); if (i < 0) items.unshift(value); else items[i] = value;
    renderPicker(); renderWork();
  }
  function selectTask(id) {
    if (busy || disposed) return;
    if (selected) drafts.set(selected, reply.value);
    generation++; win.clearTimeout(timer); selected = id; work = null; stopped = false; reply.value = drafts.get(id) || '';
    detail.replaceChildren(); renderWork(); if (id) readWork();
  }
  function visible() { return !doc.hidden && root.isConnected && !root.closest('[hidden]') && root.getClientRects().length > 0; }
  function schedule() {
    win.clearTimeout(timer);
    if (!disposed && !stopped && !busy && caps?.enabled && visible() && active(work?.state)) timer = win.setTimeout(readWork, 3000);
  }
  async function readWork() {
    if (disposed || busy || !selected) return;
    const ticket = ++generation, id = selected; win.clearTimeout(timer);
    try {
      const value = await api(API + '/tasks/' + encodeURIComponent(id));
      if (disposed || ticket !== generation || selected !== id) return;
      if (value?.id !== id) throw Error('Task response does not match selection');
      accept(value); schedule();
    } catch (err) { if (!disposed && ticket === generation) fail(err); }
  }
  async function keyFor(path, body) {
    const digest = Array.from(new Uint8Array(await win.crypto.subtle.digest('SHA-256', new TextEncoder().encode(path + '\n' + JSON.stringify(body)))), b => b.toString(16).padStart(2, '0')).join('');
    let key = intents.get(digest);
    try { key ||= win.sessionStorage.getItem('vmbox.tasks.intent.' + digest); } catch {}
    key ||= win.crypto.randomUUID(); intents.set(digest, key);
    try { win.sessionStorage.setItem('vmbox.tasks.intent.' + digest, key); } catch {}
    return { digest, key };
  }
  function action(name, body = {}) { return mutate('/tasks/' + encodeURIComponent(selected) + '/' + name, { version: work.version, ...body }, name); }
  async function mutate(path, body, kind) {
    if (disposed || busy) return;
    busy = true; generation++; setupGeneration++; win.clearTimeout(timer); error.textContent = ''; controls();
    const id = selected;
    try {
      const intent = await keyFor(path, body); if (disposed) return;
      const value = await api(API + path, 'POST', body, { 'Idempotency-Key': intent.key });
      if (disposed) return;
      if (!value?.id || !Number.isSafeInteger(value.version) || kind !== 'create' && (value.id !== id || value.version < body.version)) throw Error('Invalid mutation response');
      intents.delete(intent.digest); try { win.sessionStorage.removeItem('vmbox.tasks.intent.' + intent.digest); } catch {}
      if (kind === 'create') {
        if (selected) drafts.set(selected, reply.value);
        selected = value.id; work = null; detail.replaceChildren(); reply.value = ''; idea.value = ''; entries.forEach(release); entries.length = 0; renderImages();
      } else if (kind === 'messages') { reply.value = ''; drafts.delete(selected); }
      stopped = false; accept(value);
    } catch (err) { fail(err); }
    finally { if (!disposed) { busy = false; controls(); schedule(); } }
  }
  async function refresh() {
    if (busy || disposed) return;
    const ticket = ++setupGeneration; generation++; win.clearTimeout(timer); stopped = false; error.textContent = '';
    try {
      const value = await api(API + '/tasks/capabilities');
      if (disposed || ticket !== setupGeneration) return;
      caps = value; controls();
      if (!caps?.enabled) { notice.textContent = 'Setup needed: Tasks is not enabled. Ask the controller operator to configure Tasks, then refresh.'; return; }
      const results = await Promise.all([api(API + '/profiles'), api(API + '/tasks')]);
      if (disposed || ticket !== setupGeneration) return;
      profiles = array(results[0]); items = array(results[1]);
      const previous = agent.value; agent.replaceChildren(); option(agent, 'Choose Claude or Codex', '');
      array(caps.agents).filter(a => ['claude', 'codex'].includes(a.name)).forEach(a => option(agent, a.name + (a.images ? ' · images supported' : ' · text only'), a.name));
      if (Array.from(agent.options).some(o => o.value === previous)) agent.value = previous;
      workers.max = String(Number.isSafeInteger(caps.maxWorkers) && caps.maxWorkers > 0 ? Math.min(20, caps.maxWorkers) : 1);
      profileChoices(); renderPicker();
      notice.textContent = !ready() ? 'Setup needed: task execution is unavailable. Configure the backend, then refresh. Saved tasks remain available.' : !profiles.some(p => array(caps.agents).some(a => ['claude', 'codex'].includes(a.name) && a.name === p.application)) ? 'Setup needed: add a permitted Claude or Codex saved profile, then refresh.' : 'Describe any task and request a coordinator plan. Review it before running workers.';
      if (selected) await readWork(); controls();
    } catch (err) { if (!disposed && ticket === setupGeneration) { caps = undefined; notice.textContent = 'Setup needed: Tasks backend is unavailable. Refresh / reconnect after configuration.'; fail(err); controls(); } }
  }
  function release(entry) { entry.removed = true; entry.controller?.abort(); win.URL.revokeObjectURL(entry.url); }
  function renderImages() {
    gallery.replaceChildren();
    for (const entry of entries) {
      const figure = el('figure'), img = el('img'); img.src = entry.url; img.alt = entry.file.name;
      figure.append(img, el('figcaption', `${entry.file.name} · ${entry.asset ? 'Uploaded' : entry.error || 'Uploading…'}`));
      if (entry.error) figure.append(button('Retry upload', () => upload(entry)));
      figure.append(button('Remove attachment', () => { if (busy) return; release(entry); entries.splice(entries.indexOf(entry), 1); renderImages(); controls(); })); gallery.append(figure);
    }
    controls();
  }
  async function upload(entry) {
    if (disposed || busy || entry.uploading || entry.removed) return;
    entry.uploading = true; entry.error = ''; renderImages();
    const controller = new AbortController(); entry.controller = controller; uploads.add(controller);
    try {
      const body = new FormData(); body.append('file', entry.file);
      const response = await win.fetch(API + '/assets', { method: 'POST', credentials: 'same-origin', headers: { 'Idempotency-Key': entry.key }, body, signal: controller.signal });
      let value; try { value = await response.json(); } catch {}
      if (!response.ok || !value?.id) throw Error(value?.error || `Upload failed: HTTP ${response.status}`);
      if (!disposed && !entry.removed) entry.asset = value.id;
    } catch (err) { if (!disposed && !entry.removed && err.name !== 'AbortError') entry.error = err.message; }
    finally { uploads.delete(controller); entry.uploading = false; if (!disposed) renderImages(); }
  }
  images.addEventListener('change', () => {
    const files = Array.from(images.files); images.value = ''; if (images.matches(':disabled')) return;
    const all = [...entries.map(e => e.file), ...files];
    if (all.length > 8 || all.reduce((n, f) => n + f.size, 0) > 40 * 1024 * 1024 || files.some(f => !['image/png', 'image/jpeg'].includes(f.type) || f.size > 10 * 1024 * 1024)) { error.textContent = 'Choose at most 8 PNG / JPEG images, 10 MiB each and 40 MiB total.'; return; }
    files.forEach(file => { const entry = { file, key: win.crypto.randomUUID(), url: win.URL.createObjectURL(file) }; entries.push(entry); upload(entry); }); controls();
  });
  agent.addEventListener('change', profileChoices); form.addEventListener('input', controls); form.addEventListener('change', controls); replyForm.addEventListener('input', controls);
  picker.addEventListener('change', () => selectTask(picker.value));
  form.addEventListener('submit', e => { e.preventDefault(); controls(); if (!create.disabled) mutate('/tasks', { idea: idea.value, agent: agent.value, profile: profile.value, assetIds: entries.map(e => e.asset), maxWorkers: Number(workers.value) }, 'create'); });
  replyForm.addEventListener('submit', e => { e.preventDefault(); controls(); if (!send.disabled) action('messages', { text: reply.value }); });
  let wasVisible = visible();
  const visibility = () => { const now = visible(); if (now !== wasVisible) { wasVisible = now; if (now) schedule(); else win.clearTimeout(timer); } };
  doc.addEventListener('visibilitychange', visibility);
  const observer = new win.MutationObserver(visibility); observer.observe(doc.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'style', 'class'] });
  function cleanup() {
    disposed = true; generation++; setupGeneration++; win.clearTimeout(timer); observer.disconnect(); doc.removeEventListener('visibilitychange', visibility);
    uploads.forEach(c => c.abort()); entries.forEach(release);
    if (mounts.get(root) === cleanup) { mounts.delete(root); root.replaceChildren(); }
  }
  mounts.set(root, cleanup); renderPicker(); renderWork(); refresh(); return cleanup;
}
