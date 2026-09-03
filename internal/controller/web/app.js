'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const state = {
  token: sessionStorage.getItem('vmbox.controller.token') || '',
  boxes: [], credentials: [], fleet: null, box: null, tasks: [], task: null,
  provider: '', credential: '', chatTimer: null, controllers: new Set(), closed: false,
};
const stateLabel = value => String(value || 'unknown').replaceAll('_', ' ');
const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[character]));
const short = (value, length = 13) => value && value.length > length ? `${value.slice(0, length)}…` : (value || '—');
const stamp = value => value ? new Intl.DateTimeFormat([], {hour:'2-digit', minute:'2-digit'}).format(new Date(value)) : '';
const idempotency = prefix => `${prefix}-${crypto.randomUUID()}`;

function toast(message, error = false) {
  const element = $('#toast');
  element.textContent = message;
  element.className = `toast show${error ? ' error' : ''}`;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => element.className = 'toast', 3200);
}

function setConnection(online, label = online ? 'Live' : 'Offline') {
  $('#connection-dot').classList.toggle('offline', !online);
  $('#connection-label').textContent = label;
}

async function api(path, options = {}) {
  if (state.closed) throw new Error('Controller view is closed');
  const controller = new AbortController();
  state.controllers.add(controller);
  const timeout = setTimeout(() => controller.abort(), options.timeout || 15000);
  const headers = new Headers(options.headers || {});
  headers.set('Authorization', `Bearer ${state.token}`);
  if (options.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  try {
    const response = await fetch(path, {...options, headers, signal: controller.signal, cache: 'no-store'});
    const text = response.status === 204 ? '' : await response.text();
    let body = null;
    if (text) {
      try { body = JSON.parse(text); } catch { body = text; }
    }
    if (!response.ok) {
      if (response.status === 401) logout(false);
      throw new Error(body?.error || body || `${response.status} ${response.statusText}`);
    }
    setConnection(true);
    return body;
  } catch (error) {
    if (error.name === 'AbortError') setConnection(false, 'Request timed out');
    else if (error instanceof TypeError) setConnection(false);
    throw error;
  } finally {
    clearTimeout(timeout);
    state.controllers.delete(controller);
  }
}

function logout(showMessage = true) {
  sessionStorage.removeItem('vmbox.controller.token');
  state.token = '';
  stopChatPolling();
  $('#app').hidden = true;
  $('#login').hidden = false;
  $('#token').value = '';
  if (showMessage) toast('Logged out');
}

function showView(name) {
  if (name === 'boxes') {
    $('#app').classList.remove('show-main');
    return;
  }
  const target = name === 'settings' ? 'settings-view' : `${name}-view`;
  $$('.view').forEach(view => view.hidden = view.id !== target);
  $$('.nav-button[data-view]').forEach(button => button.classList.toggle('selected', button.dataset.view === name));
  $('#app').classList.add('show-main');
  if (name !== 'chat') stopChatPolling();
  if (name === 'settings') renderCredentials();
  if (name === 'fleet') renderFleet();
}

function renderBoxList() {
  const filter = $('#box-search').value.trim().toLowerCase();
  const values = state.boxes.filter(box => box.name.toLowerCase().includes(filter));
  $('#box-list').innerHTML = values.length ? values.map(box => `
    <button class="box-row ${escapeHTML(box.state)} ${state.box?.id === box.id ? 'selected' : ''}" data-box="${escapeHTML(box.id)}">
      <span class="avatar">${escapeHTML(box.name.slice(0, 1).toUpperCase())}</span>
      <span class="box-copy"><strong>${escapeHTML(box.name)}</strong><small>${escapeHTML(stateLabel(box.state))}${box.slotId ? ` · slot assigned` : ' · volume detached'}</small></span>
      <span class="state-pill ${escapeHTML(box.state)}">${escapeHTML(box.state)}</span>
    </button>`).join('') : '<div class="empty">No logical boxes yet.<br>Create one without adding another compute service.</div>';
  $$('[data-box]').forEach(button => button.addEventListener('click', () => selectBox(button.dataset.box)));
}

function renderMiniFleet() {
  const fleet = state.fleet;
  if (!fleet) {
    $('#fleet-mini').innerHTML = '<div class="fleet-mini-row"><span>Fleet</span><strong>Not configured</strong></div><div class="muted">Add a provider credential first.</div>';
    return;
  }
  const percent = fleet.actualSlots ? Math.round((fleet.occupiedSlots / fleet.actualSlots) * 100) : 0;
  $('#fleet-mini').innerHTML = `<div class="fleet-mini-row"><span>Warm fleet</span><strong>${fleet.freeSlots} free</strong></div><div class="fleet-mini-row"><small>${fleet.occupiedSlots} occupied · ${fleet.actualSlots}/${fleet.desiredSlots} slots</small><small>${fleet.pendingAllocationRequests || 0} queued</small></div><div class="capacity-track"><i style="width:${percent}%"></i></div>`;
}

function renderDashboard() {
  const fleet = state.fleet || {};
  const running = state.boxes.filter(box => box.state === 'running').length;
  const detached = state.boxes.filter(box => ['detached','hibernated'].includes(box.state)).length;
  const cards = [
    ['Logical boxes', state.boxes.length], ['Running now', running], ['Free slots', fleet.freeSlots ?? '—'], ['Detached', detached],
  ];
  $('#stats').innerHTML = cards.map(([label,value]) => `<div class="stat"><span>${label}</span><strong>${value}</strong></div>`).join('');
  const slots = fleet.slots || [];
  $('#slot-preview').innerHTML = slots.length ? slots.slice(0, 8).map(slot => `<div class="slot-line"><i class="slot-orb ${escapeHTML(slot.state)}"></i><div><strong>Slot ${slot.ordinal}</strong><br><small>${escapeHTML(slot.logicalBoxName || slot.serviceName || 'Warm and ready')}</small></div><span class="state-pill ${escapeHTML(slot.state)}">${escapeHTML(slot.state)}</span></div>`).join('') : '<div class="empty">No compute slots are visible yet.</div>';
  $('#activity').innerHTML = state.boxes.length ? state.boxes.slice(0, 7).map(box => `<div class="activity-line"><span class="avatar">${escapeHTML(box.name[0].toUpperCase())}</span><div><strong>${escapeHTML(box.name)}</strong><br><small>${escapeHTML(box.volumeName || 'workspace volume')}</small></div><span class="state-pill ${escapeHTML(box.state)}">${escapeHTML(box.state)}</span></div>`).join('') : '<div class="empty">Create your first logical box.</div>';
}

function renderFleet() {
  const fleet = state.fleet;
  $('#slot-count').value = fleet?.desiredSlots ?? 4;
  if (!fleet) {
    $('#fleet-details').innerHTML = '<section class="panel empty">Add a provider credential to configure compute slots.</section>';
    return;
  }
  const slots = fleet.slots || [];
  $('#fleet-details').innerHTML = slots.length ? slots.map(slot => `<article class="slot-card"><header><strong>Slot ${slot.ordinal}</strong><span class="state-pill ${escapeHTML(slot.state)}">${escapeHTML(slot.state)}</span></header><div class="detail-list"><div><span>Service</span><strong title="${escapeHTML(slot.serviceId)}">${escapeHTML(slot.serviceName || short(slot.serviceId))}</strong></div><div><span>Box</span><strong>${escapeHTML(slot.logicalBoxName || '—')}</strong></div><div><span>Region</span><strong>${escapeHTML(slot.region || '—')}</strong></div><div><span>Health</span><strong>${escapeHTML(slot.health || 'unknown')}</strong></div><div><span>Deployment</span><strong title="${escapeHTML(slot.deploymentInstanceId)}">${escapeHTML(short(slot.deploymentInstanceId))}</strong></div><div><span>Lease</span><strong>${escapeHTML(slot.leaseOwner || '—')}</strong></div></div></article>`).join('') : '<section class="panel empty">Desired slots are configured, but no fleet services exist yet.</section>';
}

function renderCredentials() {
  $('#credential-list').innerHTML = state.credentials.length ? state.credentials.map(value => `<article class="credential-card"><header><div><p class="eyebrow">${escapeHTML(value.provider)}</p><strong>${escapeHTML(value.name)}</strong></div><button class="text-button delete-credential" data-provider="${escapeHTML(value.provider)}" data-name="${escapeHTML(value.name)}">Remove</button></header><div class="detail-list"><div><span>Configuration</span><strong>${escapeHTML(Object.keys(value.config || {}).join(', ') || 'default')}</strong></div><div><span>Secret</span><strong>encrypted · hidden</strong></div><div><span>Updated</span><strong>${escapeHTML(stamp(value.updatedAt))}</strong></div></div></article>`).join('') : '<section class="panel empty">No provider credentials. Add Railway, Docker, or Incus access.</section>';
  $$('.delete-credential').forEach(button => button.addEventListener('click', async () => {
    if (!confirm(`Remove ${button.dataset.provider}/${button.dataset.name}? Existing volumes and services are not deleted.`)) return;
    try {
      await api(`/v1/provider-credentials/${encodeURIComponent(button.dataset.provider)}/${encodeURIComponent(button.dataset.name)}`, {method:'DELETE'});
      toast('Credential removed'); await refreshAll();
    } catch (error) { toast(error.message, true); }
  }));
}

function populateForms() {
  const options = state.credentials.map(value => `<option value="${escapeHTML(value.name)}" data-provider="${escapeHTML(value.provider)}">${escapeHTML(value.provider)}/${escapeHTML(value.name)}</option>`).join('');
  $('#box-credential').innerHTML = options || '<option value="">default</option>';
  $('#task-box').innerHTML = state.boxes.map(box => `<option value="${escapeHTML(box.id)}">${escapeHTML(box.name)} · ${escapeHTML(box.state)}</option>`).join('');
}

async function refreshFleet() {
  const preferred = state.credentials.find(value => value.provider === 'railway') || state.credentials[0];
  if (!preferred) { state.fleet = null; state.provider = ''; state.credential = ''; return; }
  state.provider = preferred.provider; state.credential = preferred.name;
  const query = new URLSearchParams({provider: state.provider, providerCredential: state.credential});
  state.fleet = await api(`/v1/fleet/status?${query}`);
}

async function refreshAll(silent = false) {
  try {
    const [boxes, credentials] = await Promise.all([
      api('/v1/logical-boxes'),
      api('/v1/provider-credentials').catch(error => error.message.includes('owner role') ? [] : Promise.reject(error)),
    ]);
    state.boxes = boxes || []; state.credentials = credentials || [];
    await refreshFleet().catch(error => { state.fleet = null; if (!silent) toast(error.message, true); });
    renderBoxList(); renderMiniFleet(); renderDashboard(); renderFleet(); renderCredentials(); populateForms();
  } catch (error) {
    if (!silent) toast(error.message, true);
    throw error;
  }
}

async function selectBox(id) {
  state.box = state.boxes.find(box => box.id === id);
  if (!state.box) return;
  renderBoxList();
  $('#chat-name').textContent = state.box.name;
  $('#chat-state').textContent = `${stateLabel(state.box.state)} · ${state.box.slotId ? 'compute attached' : 'volume retained'}`;
  $('#chat-avatar').textContent = state.box.name.slice(0,1).toUpperCase();
  $('#allocate').hidden = !['detached','hibernated'].includes(state.box.state);
  $('#hibernate').hidden = state.box.state !== 'running';
  showView('chat');
  try {
    state.tasks = await api(`/v1/logical-boxes/${encodeURIComponent(id)}/tasks`) || [];
    renderTaskTabs();
    const active = [...state.tasks].reverse().find(task => task.state === 'active') || state.tasks.at(-1);
    if (active) await selectTask(active.id);
    else {
      state.task = null; $('#messages').innerHTML = '<div class="empty">No tasks in this box yet.<br><button class="text-button inline-new-task">Start an agent</button></div>';
      $('.inline-new-task')?.addEventListener('click', openTaskDialog);
      $('#terminal').textContent = state.box.state === 'running' ? 'No tmux task session yet.' : 'Start the box to attach a terminal.';
    }
  } catch (error) { toast(error.message, true); }
}

function renderTaskTabs() {
  $('#task-tabs').innerHTML = `<button class="task-chip new-task-chip">＋ New task</button>${state.tasks.map(task => `<button class="task-chip ${state.task?.id === task.id ? 'selected' : ''}" data-task="${escapeHTML(task.id)}">${escapeHTML(task.agent)} · ${escapeHTML(task.state)}</button>`).join('')}`;
  $('.new-task-chip')?.addEventListener('click', openTaskDialog);
  $$('[data-task]').forEach(button => button.addEventListener('click', () => selectTask(button.dataset.task)));
}

async function selectTask(id) {
  state.task = state.tasks.find(task => task.id === id) || await api(`/v1/tasks/${encodeURIComponent(id)}`);
  renderTaskTabs();
  $('#chat-state').textContent = `${stateLabel(state.box.state)} · ${state.task.agent} task ${stateLabel(state.task.state)}`;
  await refreshConversation();
  stopChatPolling();
  state.chatTimer = setInterval(() => refreshConversation(true), 2500);
}

async function refreshConversation(silent = false) {
  if (!state.task || document.hidden || state.closed) return;
  try {
    const messages = await api(`/v1/tasks/${encodeURIComponent(state.task.id)}/messages`, {timeout:10000});
    const container = $('#messages');
    const nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 90;
    container.innerHTML = messages?.length ? messages.map(message => `<article class="message ${escapeHTML(message.direction)}"><div>${escapeHTML(message.text)}</div><div class="message-meta"><span>${escapeHTML(message.state)}</span><time>${escapeHTML(stamp(message.createdAt))}</time></div></article>`).join('') : '<div class="empty">The conversation is waiting for its first message.</div>';
    if (nearBottom) container.scrollTop = container.scrollHeight;
    if (state.box?.state === 'running') await refreshTerminal(true);
  } catch (error) { if (!silent) toast(error.message, true); }
}

async function refreshTerminal(silent = false) {
  if (!state.box || state.box.state !== 'running') return;
  const session = state.task?.session || 'vmbox';
  try {
    const snapshot = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/terminal?session=${encodeURIComponent(session)}&history=300`, {timeout:10000});
    $('#terminal').textContent = snapshot.content || 'Terminal is empty.';
    $('#terminal-title').textContent = `tmux · ${snapshot.session}${snapshot.command ? ` · ${snapshot.command}` : ''}`;
    $('#terminal-size').textContent = snapshot.width && snapshot.height ? `${snapshot.width}×${snapshot.height}` : '';
  } catch (error) {
    $('#terminal').textContent = `Screen mirror unavailable\n\n${error.message}`;
    if (!silent) toast(error.message, true);
  }
}

function stopChatPolling() {
  if (state.chatTimer) clearInterval(state.chatTimer);
  state.chatTimer = null;
}

function openTaskDialog() {
  if (!state.boxes.length) return toast('Create a logical box first', true);
  if (state.box) $('#task-box').value = state.box.id;
  $('#task-dialog').showModal();
}

$('#login-form').addEventListener('submit', async event => {
  event.preventDefault();
  state.token = $('#token').value.trim();
  $('#login-error').textContent = '';
  try {
    await api('/v1/logical-boxes');
    sessionStorage.setItem('vmbox.controller.token', state.token);
    $('#login').hidden = true; $('#app').hidden = false;
    await refreshAll(); showView('home');
  } catch (error) { $('#login-error').textContent = error.message; state.token = ''; }
});

$('#logout').addEventListener('click', () => logout());
$('#refresh').addEventListener('click', () => refreshAll());
$('#fleet-refresh').addEventListener('click', () => refreshAll());
$('#box-search').addEventListener('input', renderBoxList);
$$('[data-view]').forEach(button => button.addEventListener('click', () => showView(button.dataset.view)));
$('#mobile-back').addEventListener('click', () => showView('boxes'));
$('#new-box').addEventListener('click', () => $('#box-dialog').showModal());
$('#home-new-task').addEventListener('click', openTaskDialog);
$('#new-credential').addEventListener('click', () => $('#credential-dialog').showModal());
$$('.close-dialog').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));

$('#box-form').addEventListener('submit', async event => {
  event.preventDefault(); const form = new FormData(event.currentTarget);
  const selected = state.credentials.find(value => value.name === form.get('credential'));
  const body = {name:form.get('name'), provider:selected?.provider || form.get('provider'), providerCredential:form.get('credential'), region:form.get('region'), diskGiB:Number(form.get('disk')), allocateWhenReady:form.get('allocate') === 'on', allocationIdempotencyKey:idempotency('create-allocate')};
  try {
    await api('/v1/logical-boxes', {method:'POST', body:JSON.stringify(body)});
    $('#box-dialog').close(); event.currentTarget.reset(); toast('Workspace volume creation started'); await refreshAll();
  } catch (error) { toast(error.message, true); }
});

$('#task-form').addEventListener('submit', async event => {
  event.preventDefault(); const form = new FormData(event.currentTarget); const boxID = form.get('box');
  try {
    const task = await api(`/v1/logical-boxes/${encodeURIComponent(boxID)}/tasks`, {method:'POST', headers:{'Idempotency-Key':idempotency('task')}, body:JSON.stringify({agent:form.get('agent'), session:form.get('session'), prompt:form.get('prompt')})});
    $('#task-dialog').close(); event.currentTarget.reset(); toast('Task queued'); await refreshAll(); await selectBox(boxID); await selectTask(task.id);
  } catch (error) { toast(error.message, true); }
});

$('#credential-form').addEventListener('submit', async event => {
  event.preventDefault(); const form = new FormData(event.currentTarget);
  try {
    const secret = JSON.parse(form.get('secret')); const config = form.get('config').trim() ? JSON.parse(form.get('config')) : {};
    await api(`/v1/provider-credentials/${encodeURIComponent(form.get('provider'))}/${encodeURIComponent(form.get('name'))}`, {method:'PUT', body:JSON.stringify({secret,config})});
    $('#credential-dialog').close(); event.currentTarget.reset(); toast('Credential encrypted and saved'); await refreshAll();
  } catch (error) { toast(error.message, true); }
});

$('#slot-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!state.provider) return toast('Add a provider credential first', true);
  try {
    await api('/v1/fleet/slots', {method:'PUT', body:JSON.stringify({provider:state.provider, providerCredential:state.credential, compute_box_slots:Number($('#slot-count').value)})});
    toast('Fleet target saved; reconciliation is running'); setTimeout(() => refreshAll(true), 1800);
  } catch (error) { toast(error.message, true); }
});

$('#allocate').addEventListener('click', async () => {
  try {
    const allocation = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/allocate`, {method:'POST', headers:{'Idempotency-Key':idempotency('allocate')}, body:'{}'});
    toast(allocation.state === 'queued' ? `Waiting for capacity · queue ${allocation.queuePosition || 1}` : `Allocation ${stateLabel(allocation.phase || allocation.state)}`);
    setTimeout(async () => { await refreshAll(true); await selectBox(state.box.id); }, 1800);
  } catch (error) { toast(error.message, true); }
});

$('#hibernate').addEventListener('click', async () => {
  if (!confirm(`Hibernate ${state.box.name}? The volume and saved tmux state remain; live processes stop and the compute slot becomes free.`)) return;
  try { await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/hibernate`, {method:'POST', body:'{}', timeout:120000}); toast('Box hibernated; volume retained'); await refreshAll(); showView('boxes'); } catch (error) { toast(error.message, true); }
});

$('#box-menu').addEventListener('click', async () => {
  if (!state.box) return;
  const typed = prompt(`Delete volume permanently?\n\nBox: ${state.box.name}\nVolume: ${state.box.volumeName} (${state.box.volumeId})\n\nType the exact box name to confirm. Cancel keeps everything.`);
  if (typed !== state.box.name) return typed !== null && toast('Name did not match; nothing was deleted', true);
  if (!confirm(`Final confirmation: permanently delete only volume ${state.box.volumeName}? The compute fleet size will not change.`)) return;
  try { await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/volume`, {method:'DELETE', body:JSON.stringify({confirmation:typed}), timeout:120000}); toast('Exact workspace volume deleted'); state.box = null; await refreshAll(); showView('boxes'); } catch (error) { toast(error.message, true); }
});

$('#message-form').addEventListener('submit', async event => {
  event.preventDefault(); if (!state.task) return openTaskDialog(); const input = $('#message'); const text = input.value;
  if (!text.trim()) return;
  input.value = '';
  try { await api(`/v1/tasks/${encodeURIComponent(state.task.id)}/messages`, {method:'POST', headers:{'Idempotency-Key':idempotency('message')}, body:JSON.stringify({text,submit:true}), timeout:30000}); await refreshConversation(); } catch (error) { input.value = text; toast(error.message, true); }
});

$('#terminal-form').addEventListener('submit', async event => {
  event.preventDefault(); if (!state.box) return; const input = $('#terminal-input'); const text = input.value; if (!text) return;
  const session = state.task?.session || 'vmbox'; input.value = '';
  try { await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/terminal/input?session=${encodeURIComponent(session)}`, {method:'POST', headers:{'Idempotency-Key':idempotency('terminal')}, body:JSON.stringify({text,submit:true})}); setTimeout(() => refreshTerminal(true), 350); } catch (error) { input.value = text; toast(error.message, true); }
});

$('#message').addEventListener('input', event => { event.target.style.height = 'auto'; event.target.style.height = `${Math.min(event.target.scrollHeight, 140)}px`; });
$('#message').addEventListener('keydown', event => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); $('#message-form').requestSubmit(); } });
document.addEventListener('visibilitychange', () => { if (document.hidden) stopChatPolling(); else if (state.task) { refreshConversation(true); state.chatTimer = setInterval(() => refreshConversation(true), 2500); } });
window.addEventListener('pagehide', () => { state.closed = true; stopChatPolling(); state.controllers.forEach(controller => controller.abort()); state.controllers.clear(); });
window.addEventListener('pageshow', event => {
  state.closed = false;
  if (!event.persisted || !state.token || $('#app').hidden) return;
  refreshAll(true);
  if (state.task && !document.hidden) {
    stopChatPolling();
    refreshConversation(true);
    state.chatTimer = setInterval(() => refreshConversation(true), 2500);
  }
});

if (state.token) {
  $('#token').value = state.token;
  api('/v1/logical-boxes').then(async boxes => { state.boxes = boxes || []; $('#login').hidden = true; $('#app').hidden = false; await refreshAll(); showView('home'); }).catch(() => logout(false));
}
