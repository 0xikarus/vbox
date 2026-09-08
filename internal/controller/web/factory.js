// Factory contract v1. The caller owns routing, authentication and stylesheet loading.
const API = '/v1/factory';
const list = value => Array.isArray(value) ? value : [];
const text = value => typeof value === 'string' ? value : value == null ? '' : JSON.stringify(value);
const activeState = state => /^(planning_queued|waiting_capacity|restoring|preparing_inputs|planning|approved_queued|approved|build_queued|queued|implementing|building|verifying|reviewing)$/.test(state);
const mounts = new WeakMap();

export function mountFactory(root, request) {
  mounts.get(root)?.();
  const doc = root.ownerDocument;
  const win = doc.defaultView;
  let disposed = false, busy = false, stopped = false, timer, generation = 0, setupGeneration = 0;
  let historyGeneration = 0, historyLoading = false;
  let capabilities, repositories = [], profiles = [], work, selected = '', cursor = '', summaries = [];
  const drafts = new Map(), intents = new Map(), controllers = new Set();
  const el = (tag, value, className) => {
    const n = doc.createElement(tag);
    if (value !== undefined) n.textContent = text(value);
    if (className) n.className = className;
    return n;
  };
  const button = (label, fn) => {
    const n = el('button', label); n.type = 'button'; n.addEventListener('click', fn); return n;
  };
  const field = (parent, label, tag, name) => {
    const wrap = el('label', label), n = el(tag); n.name = name; wrap.append(n); parent.append(wrap); return n;
  };
  const option = (select, label, value) => { const n = el('option', label); n.value = value; select.append(n); };
  const storage = {
    get(key) { try { return win.sessionStorage.getItem('vmbox.factory.' + key); } catch { return null; } },
    set(key, value) { try { win.sessionStorage.setItem('vmbox.factory.' + key, value); } catch {} },
    remove(key) { try { win.sessionStorage.removeItem('vmbox.factory.' + key); } catch {} },
  };
  const shell = el('div', undefined, 'factory');
  const error = el('p', '', 'factory-error'); error.setAttribute('role', 'alert');
  const notice = el('p', 'Loading Factory…'); notice.setAttribute('role', 'status');
  const reconnect = button('Refresh / reconnect', () => refresh());
  shell.append(el('h2', 'Factory'), notice, error, reconnect);
  const form = el('form'), inputs = el('fieldset'); inputs.disabled = true;
  inputs.append(el('legend', 'Plan new work'));
  const repo = field(inputs, 'Repository', 'select', 'repository'); repo.required = true;
  const base = field(inputs, 'Base branch / revision', 'input', 'baseRef'); base.required = true;
  const agent = field(inputs, 'Planning agent', 'select', 'agent'); agent.required = true;
  const profile = field(inputs, 'Saved profile', 'select', 'profile'); profile.required = true;
  const idea = field(inputs, 'Idea', 'textarea', 'idea'); idea.required = true; idea.rows = 5;
  inputs.append(el('p', 'Plan requests a planning attempt. Implementation starts only after Approve & build.'));
  const initialImages = imageComposer(inputs, 'Planning images');
  const planButton = el('button', 'Plan'); planButton.type = 'submit'; inputs.append(planButton);
  form.append(inputs); shell.append(form);
  const history = el('section'); history.append(el('h3', 'Saved work'));
  const picker = field(history, 'Work item', 'select', 'workItem');
  const more = button('Load more work', () => loadHistory(true).catch(fail)); more.hidden = true;
  history.append(more); shell.append(history);
  const detail = el('section'); shell.append(detail);
  const replyForm = el('form'), replyFields = el('fieldset'); replyFields.append(el('legend', 'Reply / revise plan'));
  const reply = field(replyFields, 'Reply or requested changes', 'textarea', 'reply'); reply.rows = 4; reply.required = true;
  const replyImages = imageComposer(replyFields, 'Reply images');
  const send = el('button', 'Send reply / revise'); send.type = 'submit'; replyFields.append(send);
  replyForm.append(replyFields); replyForm.hidden = true; shell.append(replyForm);
  root.replaceChildren(shell);

  function fail(err) {
    if (disposed) return;
    stopped = true; win.clearTimeout(timer);
    error.textContent = text(err?.message || err) + ' Refresh / reconnect to reload saved work. Your entered content is retained; retry an unchanged submission to reuse its request key.';
  }
  const executionReady = () => capabilities?.executionReady !== false;
  const imageTypes = () => ['image/png', 'image/jpeg'].filter(type => list(capabilities?.imageTypes ?? ['image/png', 'image/jpeg']).includes(type));
  const imageFormats = () => imageTypes().map(type => type === 'image/png' ? 'PNG' : 'JPEG').join(' / ');
  function controls() {
    inputs.disabled = busy || !capabilities?.enabled || !capabilities?.githubConfigured || !executionReady();
    planButton.disabled = inputs.disabled || !repo.value || !profile.value || !agent.value || !base.value.trim() || !idea.value.trim() || initialImages.pending() || !imagesAllowed(initialImages);
    picker.disabled = busy || !capabilities?.enabled; more.disabled = picker.disabled || historyLoading; reconnect.disabled = busy;
    replyForm.hidden = !work;
    replyFields.disabled = busy || !executionReady() || !work || activeState(work.state) || !['plan_ready', 'needs_clarification', 'planning_failed', 'failed'].includes(work.state) || !capabilities?.enabled;
    send.disabled = replyFields.disabled || !reply.value.trim() || replyImages.pending() || !imagesAllowed(replyImages, work?.agent);
    initialImages.update(); replyImages.update();
    const approve = detail.querySelector('[data-approve]');
    if (approve) approve.disabled = busy || !approvalReady(work);
  }
  function imagesAllowed(composer, name = agent.value) {
    return !composer.entries.length || (composer.entries.every(e => imageTypes().includes(e.file.type)) && capabilities?.agents?.some(a => a.name === name && a.images === true));
  }
  function imageComposer(parent, label) {
    const wrap = el('div', undefined, 'factory-images');
    const input = field(wrap, label, 'input', label === 'Planning images' ? 'images' : 'replyImages');
    input.type = 'file'; input.multiple = true;
    const formats = el('p'); wrap.append(formats);
    const support = el('p'); wrap.append(support);
    const gallery = el('div', undefined, 'factory-gallery'); wrap.append(gallery); parent.append(wrap);
    const composer = { entries: [], pending: () => composer.entries.some(e => !e.asset), update() {
      input.accept = imageTypes().join(',');
      input.disabled = busy || !imageTypes().length;
      formats.textContent = (imageFormats() || 'No image formats advertised') + '. Up to 8 images, 10 MiB each, 40 MiB total; server validates the 25 MP limit.';
      const name = label === 'Planning images' ? agent.value : work?.agent;
      support.textContent = composer.entries.length && !capabilities?.agents?.some(a => a.name === name && a.images === true) ? 'The selected agent does not advertise image support. Choose an image-capable agent or remove these images.' : '';
      gallery.querySelectorAll('button').forEach(b => { b.disabled = busy; });
    }, clear() { composer.entries.forEach(release); composer.entries = []; render(); }, render };
    function release(entry) { entry.removed = true; entry.controller?.abort(); if (entry.url) win.URL.revokeObjectURL(entry.url); }
    function render() {
      gallery.replaceChildren();
      for (const entry of composer.entries) {
        const item = el('figure'), img = el('img'); img.src = entry.url; img.alt = entry.file.name;
        item.append(img, el('figcaption', entry.file.name + ' · ' + (entry.asset ? 'Uploaded' : entry.error || 'Uploading…')));
        if (entry.error) item.append(button('Retry upload', () => upload(entry)));
        item.append(button('Remove', () => { release(entry); composer.entries = composer.entries.filter(e => e !== entry); render(); controls(); }));
        gallery.append(item);
      }
      composer.update();
    }
    async function upload(entry) {
      if (entry.uploading || disposed || !composer.entries.includes(entry)) return;
      entry.uploading = true; entry.error = ''; render();
      const controller = new AbortController(); entry.controller = controller; controllers.add(controller);
      try {
        const body = new FormData(); body.append('file', entry.file);
        const response = await win.fetch(API + '/assets', { method: 'POST', credentials: 'same-origin', headers: { 'Idempotency-Key': entry.key }, body, signal: controller.signal });
        let value; try { value = await response.json(); } catch {}
        if (!response.ok) throw Error(value?.error || 'Upload failed: HTTP ' + response.status);
        if (!value?.id) throw Error('Upload returned no asset ID');
        if (!disposed && !entry.removed) entry.asset = value;
      } catch (err) { if (err.name !== 'AbortError' && !disposed && !entry.removed) entry.error = err.message; }
      finally { controllers.delete(controller); entry.uploading = false; if (!disposed) { render(); controls(); } }
    }
    input.addEventListener('change', () => {
      const files = Array.from(input.files); input.value = '';
      if (input.matches(':disabled')) return;
      const total = [...composer.entries.map(e => e.file), ...files];
      if (total.length > 8 || total.reduce((n, f) => n + f.size, 0) > 40 * 1024 * 1024 || files.some(f => f.size > 10 * 1024 * 1024 || !imageTypes().includes(f.type))) {
        error.textContent = `Choose at most 8 images in advertised formats (${imageFormats() || 'none'}), 10 MiB each and 40 MiB total.`; return;
      }
      for (const file of files) {
        const entry = { file, key: win.crypto.randomUUID(), url: win.URL.createObjectURL(file) };
        composer.entries.push(entry); upload(entry);
      }
      controls();
    });
    return composer;
  }
  function savedAssets(parent, assets) {
    const gallery = el('div', undefined, 'factory-gallery');
    for (const asset of list(assets)) {
      const id = typeof asset === 'string' ? asset : asset.id;
      if (!id) continue;
      const a = el('a', typeof asset === 'string' ? id : asset.name || id);
      a.href = API + '/assets/' + encodeURIComponent(id); a.target = '_blank'; a.rel = 'noopener noreferrer';
      const img = el('img'); img.src = a.href; img.alt = a.textContent; img.loading = 'lazy'; a.prepend(img); gallery.append(a);
    }
    parent.append(gallery);
  }
  function link(parent, label, href, external = false) {
    if (external) {
      try { const url = new URL(href); if (url.protocol !== 'https:' || url.username || url.password) return; } catch { return; }
    }
    const a = el('a', label); a.href = href; a.target = '_blank'; a.rel = 'noopener noreferrer'; parent.append(a, doc.createTextNode(' '));
  }
  function validTasks(features) {
    if (!features.length) return false;
    const byID = new Map(features.map(f => [f.id, f]));
    if (byID.size !== features.length || features.some(f => !f.id || !list(f.checks).length || f.checks.some(c => !Array.isArray(c.argv) || !c.argv.length || c.argv.some(a => typeof a !== 'string') || typeof c.cwd !== 'string' || !(c.timeoutSeconds > 0)))) return false;
    const visiting = new Set(), done = new Set();
    function visit(id) {
      if (!byID.has(id) || visiting.has(id)) return false;
      if (done.has(id)) return true;
      visiting.add(id);
      if (!list(byID.get(id).dependsOn).every(visit)) return false;
      visiting.delete(id); done.add(id); return true;
    }
    return features.every(f => visit(f.id));
  }
  function latestPlan(value) { return list(value?.plans).reduce((best, p) => !best || p.revision > best.revision ? p : best, null); }
  function approvalReady(value) {
    const p = latestPlan(value);
    return capabilities?.enabled && executionReady() && value?.state === 'plan_ready' && (p?.inputRevision ?? p?.revision) === value.revision && !list(p.questions).length && validTasks(list(p.features));
  }
  function taskTable(parent, features) {
    if (!features.length) { parent.append(el('p', 'No tasks recorded.')); return; }
    const scroll = el('div', undefined, 'factory-table'), table = el('table'), head = el('thead'), row = el('tr');
    for (const label of ['Task / acceptance', 'Depends on', 'Files', 'Checks', 'State / evidence']) row.append(el('th', label));
    head.append(row); table.append(head); const body = el('tbody');
    for (const f of features) {
      const tr = el('tr'), title = el('td'), evidence = el('td', f.state || '—');
      title.append(el('strong', `${f.id}: ${f.title || ''}`), el('p', f.description), el('pre', list(f.acceptanceCriteria).map(text).join('\n')));
      const checks = list(f.checks).map(c => `${JSON.stringify(c.argv)}\ncwd: ${c.cwd || '.'} · timeout: ${c.timeoutSeconds ?? '—'}s`).join('\n\n');
      if (f.boxId) link(evidence, 'Box', '/boxes/' + encodeURIComponent(f.boxId));
      if (f.issueUrl) link(evidence, 'Issue', f.issueUrl, true);
      if (f.prUrl) link(evidence, 'PR', f.prUrl, true);
      tr.append(title, el('td', list(f.dependsOn).join(', ') || 'None'), el('td', list(f.files).join('\n')), el('td', checks), evidence); body.append(tr);
    }
    table.append(body); scroll.append(table); parent.append(scroll);
  }
  function renderWork() {
    detail.replaceChildren();
    if (!work) { detail.append(el('p', selected ? 'Loading saved work…' : 'Select saved work to view its responses and plans.')); controls(); return; }
    detail.append(el('h3', work.repositoryName || work.repositoryId), el('p', `Work ${work.id} · input revision ${work.revision} · ${work.state}`), el('p', `Agent: ${work.agent} · profile: ${text(work.profile)} · base: ${work.baseRef || ''} ${work.baseSha || ''}`), el('pre', work.idea));
    if (work.boxId) link(detail, work.boxName || 'Planning box', '/boxes/' + encodeURIComponent(work.boxId));
    // Attempts are appended by the server; prefer the highest revision and last retry.
    const attempt = list(work.attempts).reduce((latest, a) => !latest || a.revision >= latest.revision ? a : latest, null);
    const attemptStatus = el('p', attempt ? `Latest planning attempt ${attempt.id} · input revision ${attempt.revision} · ${attempt.state}` : 'No planning attempt recorded.', 'factory-attempt');
    attemptStatus.setAttribute('role', 'status'); detail.append(attemptStatus);
    // work.error is the server's user-safe diagnostic, rendered as inert text.
    if (work.error) detail.append(el('p', work.error, 'factory-error'));
    savedAssets(detail, work.assets);
    detail.append(el('h3', 'Saved responses'));
    if (!list(work.messages).length) detail.append(el('p', 'No responses recorded yet.'));
    for (const m of list(work.messages)) {
      const article = el('article'); article.append(el('h4', `${m.role} · ${m.createdAt || ''}`), el('pre', m.text)); savedAssets(article, m.assetIds); detail.append(article);
    }
    detail.append(el('h3', 'Plan revisions'));
    const latest = latestPlan(work);
    if (!latest) detail.append(el('p', 'No plan recorded yet.'));
    for (const p of [...list(work.plans)].sort((a, b) => b.revision - a.revision)) {
      const revision = el('details'); revision.open = p === latest;
      revision.append(el('summary', `Plan revision ${p.revision} · ${p.createdAt || ''}`), el('p', `Base SHA: ${p.baseSha || 'Not recorded'}`), el('pre', p.markdown));
      if (list(p.questions).length) { revision.append(el('h4', 'Questions')); const ul = el('ul'); for (const q of p.questions) ul.append(el('li', q)); revision.append(ul); }
      taskTable(revision, list(p.features)); detail.append(revision);
    }
    if (list(work.features).length) { detail.append(el('h3', 'Build / review evidence')); taskTable(detail, work.features); }
    const approval = el('form'), workers = field(approval, 'Maximum workers (server policy applies)', 'input', 'maxWorkers');
    workers.type = 'number'; workers.min = '1'; workers.step = '1'; workers.value = '1'; workers.required = true;
    const approve = el('button', 'Approve & build'); approve.type = 'submit'; approve.dataset.approve = ''; approval.append(approve);
    approval.addEventListener('submit', e => {
      e.preventDefault(); const maxWorkers = Number(workers.value);
      if (!approvalReady(work) || !Number.isSafeInteger(maxWorkers) || maxWorkers < 1) return;
      mutate('/work-items/' + encodeURIComponent(work.id) + '/approve', { expectedRevision: work.revision, planRevision: latest.revision, maxWorkers });
    });
    detail.append(approval, el('p', 'Approval starts build scheduling for this revision. It does not authorize an automatic main merge.'));
    if (!approvalReady(work)) detail.append(el('p', 'Approval requires a ready current plan, answered questions, and tasks with valid dependencies and checks. The server validates approval again.'));
    controls();
  }
  function rememberDraft() {
    if (selected) drafts.set(selected, { text: reply.value, entries: replyImages.entries });
  }
  function selectWork(id) {
    rememberDraft(); generation++; selected = id; work = null; stopped = false; win.clearTimeout(timer);
    storage.set('selected', id); picker.value = id;
    const draft = drafts.get(id); reply.value = draft?.text || ''; replyImages.entries = draft?.entries || []; replyImages.render();
    renderWork(); if (id) readWork();
  }
  function accept(value) {
    if (!value?.id || !Number.isInteger(value.revision)) throw Error('Invalid work item response');
    if (work?.id === value.id && value.revision < work.revision) return;
    work = value;
    const index = summaries.findIndex(s => s.id === value.id);
    if (index >= 0) summaries[index] = value; else summaries.unshift(value);
    renderPicker(); renderWork();
  }
  function renderPicker() {
    picker.replaceChildren(); option(picker, 'Choose saved work', '');
    for (const item of summaries) option(picker, `${item.repositoryName || item.repositoryId || item.id} · ${item.state} · ${item.id}`, item.id);
    if (selected && !summaries.some(s => s.id === selected)) option(picker, selected, selected);
    picker.value = selected; more.hidden = !cursor;
  }
  async function loadHistory(append = false) {
    if (append && historyLoading) return;
    const version = setupGeneration, historyVersion = ++historyGeneration;
    historyLoading = true; more.disabled = true;
    try {
      // v1 leaves the pagination envelope unspecified; accept a bare array or {items,nextCursor}.
      const result = await request(API + '/work-items' + (append && cursor ? '?cursor=' + encodeURIComponent(cursor) : ''));
      if (disposed || version !== setupGeneration || historyVersion !== historyGeneration) return;
      const values = Array.isArray(result) ? result : list(result?.items);
      summaries = Array.from(new Map([...(append ? summaries : []), ...values].map(s => [s.id, s])).values());
      cursor = Array.isArray(result) ? '' : result?.nextCursor || ''; renderPicker();
    } finally { if (!disposed && historyVersion === historyGeneration) { historyLoading = false; controls(); } }
  }
  async function readWork() {
    if (disposed || !selected || busy) return;
    const version = ++generation, id = selected;
    win.clearTimeout(timer);
    try {
      const value = await request(API + '/work-items/' + encodeURIComponent(id));
      if (disposed || version !== generation || selected !== id) return;
      if (value?.id !== id) throw Error('Work item response does not match selection');
      accept(value); schedule();
    } catch (err) { if (!disposed && version === generation) fail(err); }
  }
  function visible() { return !doc.hidden && root.isConnected && !root.closest('[hidden]') && root.getClientRects().length > 0; }
  function schedule() {
    win.clearTimeout(timer);
    if (!disposed && capabilities?.enabled && !stopped && !busy && visible() && activeState(work?.state)) timer = win.setTimeout(readWork, 3000);
  }
  async function keyFor(path, body) {
    // Store only a request fingerprint and random key, never credentials or draft text.
    const bytes = new TextEncoder().encode(path + '\n' + JSON.stringify(body));
    const digest = Array.from(new Uint8Array(await win.crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
    const key = intents.get(digest) || storage.get(digest) || win.crypto.randomUUID();
    intents.set(digest, key); storage.set(digest, key); return { digest, key };
  }
  async function mutate(path, body, kind) {
    if (busy || disposed) return;
    busy = true; generation++; setupGeneration++; win.clearTimeout(timer); error.textContent = ''; controls();
    let intent;
    try {
      intent = await keyFor(path, body);
      if (disposed) return;
      const value = await request(API + path, 'POST', body, { 'Idempotency-Key': intent.key });
      if (disposed) return;
      if (!value?.id || !Number.isInteger(value.revision) || (kind !== 'plan' && value.id !== selected)) throw Error('Invalid work item response');
      intents.delete(intent.digest); storage.remove(intent.digest);
      if (kind === 'plan') {
        rememberDraft(); selected = value.id; storage.set('selected', selected);
        idea.value = ''; initialImages.clear(); reply.value = ''; replyImages.entries = []; replyImages.render();
      } else if (kind === 'reply') { reply.value = ''; replyImages.clear(); drafts.delete(selected); }
      stopped = false; accept(value);
    } catch (err) { fail(err); }
    finally { if (!disposed) { busy = false; controls(); schedule(); } }
  }
  function profileChoices() {
    const previous = profile.value; profile.replaceChildren(); option(profile, 'Choose saved profile', '');
    for (const p of profiles.filter(p => p.application === agent.value)) option(profile, p.name, p.name);
    if (Array.from(profile.options).some(o => o.value === previous)) profile.value = previous;
    controls();
  }
  async function refresh() {
    if (busy || disposed) return;
    const version = ++setupGeneration; generation++; win.clearTimeout(timer); error.textContent = ''; stopped = false;
    try {
      const caps = await request(API + '/capabilities');
      if (disposed || version !== setupGeneration) return;
      capabilities = caps; controls();
      if (!caps?.enabled) { notice.textContent = 'Factory is disabled. Ask the controller operator to enable it.'; controls(); return; }
      const [repos, savedProfiles] = await Promise.all([request(API + '/repositories'), request(API + '/profiles')]);
      if (disposed || version !== setupGeneration) return;
      repositories = list(repos); profiles = list(savedProfiles);
      const previousRepo = repo.value, previousAgent = agent.value;
      repo.replaceChildren(); option(repo, 'Choose repository', '');
      for (const r of repositories) option(repo, r.fullName, r.id);
      if (repositories.some(r => r.id === previousRepo)) repo.value = previousRepo;
      agent.replaceChildren(); option(agent, 'Choose planning agent', '');
      for (const a of list(caps.agents)) option(agent, a.name + (a.images ? ' · images supported' : ' · text only'), a.name);
      if (list(caps.agents).some(a => a.name === previousAgent)) agent.value = previousAgent;
      profileChoices();
      notice.textContent = !executionReady() ? 'Planning execution configuration required. Ask the controller operator to configure planning execution, then refresh. Plan and replies are unavailable; new planning requests will not be queued. Saved work remains available to browse.' : !caps.githubConfigured || !repositories.length ? 'Connect the GitHub App through your controller operator, grant it access to the desired repositories, then refresh.' : !profiles.length ? 'No permitted saved profiles. Add an agent login in Profiles, then refresh.' : 'Select a repository and saved agent profile to plan. Saved work is authoritative.';
      await loadHistory();
      if (disposed || version !== setupGeneration) return;
      if (!selected) { const restored = storage.get('selected'); if (restored) selectWork(restored); }
      else await readWork();
      controls();
    } catch (err) { if (!disposed && version === setupGeneration) fail(err); }
  }
  repo.addEventListener('change', () => { base.value = repositories.find(r => r.id === repo.value)?.defaultBranch || ''; controls(); });
  agent.addEventListener('change', profileChoices);
  form.addEventListener('input', controls); form.addEventListener('change', controls);
  replyForm.addEventListener('input', controls);
  picker.addEventListener('change', () => selectWork(picker.value));
  form.addEventListener('submit', e => {
    e.preventDefault(); controls(); if (planButton.disabled) return;
    mutate('/work-items', { repositoryId: repo.value, baseRef: base.value.trim(), idea: idea.value, agent: agent.value, profile: profile.value, assetIds: initialImages.entries.map(e => e.asset.id) }, 'plan');
  });
  replyForm.addEventListener('submit', e => {
    e.preventDefault(); controls(); if (send.disabled) return;
    mutate('/work-items/' + encodeURIComponent(selected) + '/messages', { text: reply.value, assetIds: replyImages.entries.map(e => e.asset.id), expectedRevision: work.revision }, 'reply');
  });
  let wasVisible = visible();
  const visibility = () => {
    const nowVisible = visible();
    if (nowVisible !== wasVisible) { wasVisible = nowVisible; if (nowVisible) schedule(); else win.clearTimeout(timer); }
  };
  doc.addEventListener('visibilitychange', visibility);
  const observer = new win.MutationObserver(visibility);
  observer.observe(doc.documentElement, { attributes: true, attributeFilter: ['hidden', 'style', 'class'], childList: true, subtree: true });
  function unmount() {
    disposed = true; generation++; setupGeneration++; win.clearTimeout(timer); observer.disconnect();
    doc.removeEventListener('visibilitychange', visibility); controllers.forEach(c => c.abort());
    const entries = new Set([...initialImages.entries, ...replyImages.entries, ...Array.from(drafts.values()).flatMap(d => d.entries)]);
    entries.forEach(e => win.URL.revokeObjectURL(e.url));
    if (mounts.get(root) === unmount) { mounts.delete(root); root.replaceChildren(); }
  }
  mounts.set(root, unmount); renderWork(); refresh();
  return unmount;
}
