'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
const state = {
  token: sessionStorage.getItem('vmbox.controller.token') || '',
  boxes: [], external: [], groups: [], credentials: [], notifications: [], fleet: null,
  box: null, tasks: [], task: null, taskMessages: [], group: null, groupMessages: [], recipients: new Set(),
  groupScreens: new Map(), groupScreenRefreshAt: 0, previewBoxID: '',
  provider: '', credential: '', chatTimer: null, controllers: new Set(), closed: false,
};

const stateLabel = value => String(value || 'unknown').replaceAll('_', ' ');
const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[character]));
const short = (value, length = 16) => value && value.length > length ? `${value.slice(0, length)}…` : (value || '—');
const stamp = value => value ? new Intl.DateTimeFormat([], {hour:'2-digit', minute:'2-digit'}).format(new Date(value)) : '';
const fullStamp = value => value ? new Intl.DateTimeFormat([], {dateStyle:'medium', timeStyle:'short'}).format(new Date(value)) : '—';
const idempotency = prefix => `${prefix}-${crypto.randomUUID()}`;
const csv = value => String(value || '').split(',').map(item => item.trim()).filter(Boolean);

function toast(message, error = false) {
  const element = $('#toast');
  element.textContent = message;
  element.className = `toast show${error ? ' error' : ''}`;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => element.className = 'toast', 3600);
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

function stopPolling() {
  if (state.chatTimer) clearInterval(state.chatTimer);
  state.chatTimer = null;
}

function startPolling(callback) {
  stopPolling();
  state.chatTimer = setInterval(() => {
    if (!document.hidden && !state.closed) callback(true);
  }, 2500);
}

function logout(showMessage = true) {
  sessionStorage.removeItem('vmbox.controller.token');
  state.token = '';
  stopPolling();
  state.controllers.forEach(controller => controller.abort());
  state.controllers.clear();
  $('#app').hidden = true;
  $('#login').hidden = false;
  $('#token').value = '';
  if (showMessage) toast('Logged out');
}

function showView(name) {
  if (name === 'boxes') {
    setTerminalPane(false);
    $('#app').classList.remove('show-main');
    return;
  }
  const target = name === 'settings' ? 'settings-view' : `${name}-view`;
  $$('.view').forEach(view => view.hidden = view.id !== target);
  $$('.nav-button[data-view]').forEach(button => button.classList.toggle('selected', button.dataset.view === name));
  $('#app').classList.add('show-main');
  if (!['chat', 'group'].includes(name)) stopPolling();
  if (name === 'settings') renderSettings();
  if (name === 'fleet') renderFleet();
}

function setTerminalPane(open) {
  const body = $('#chat-view .chat-body');
  const button = $('#toggle-terminal');
  body.classList.toggle('terminal-open', open);
  button.setAttribute('aria-pressed', String(open));
  button.textContent = open ? 'Chat' : 'Terminal';
  if (open && state.box?.state === 'running') refreshTerminal(true);
}

function avatar(name, group = false) {
  return group ? '#' : String(name || '?').slice(0, 1).toUpperCase();
}

function renderRoster() {
  const filter = $('#box-search').value.trim().toLowerCase();
  const boxes = state.boxes.filter(box => box.name.toLowerCase().includes(filter));
  const groups = state.groups.filter(group => group.name.toLowerCase().includes(filter));
  const external = state.external.filter(box => box.name.toLowerCase().includes(filter));

  $('#group-list').innerHTML = groups.length ? groups.map(group => `
    <button class="roster-row group-row ${state.group?.id === group.id ? 'selected' : ''}" data-group="${escapeHTML(group.id)}">
      <span class="avatar group-avatar">#</span>
      <span class="roster-copy"><strong>${escapeHTML(group.name)}</strong><small>${group.members.length} boxes · durable chat</small></span>
      <span class="row-arrow">›</span>
    </button>`).join('') : '<div class="compact-empty">No groups yet</div>';

  $('#box-list').innerHTML = boxes.length ? boxes.map(box => `
    <button class="roster-row box-row ${escapeHTML(box.state)} ${state.box?.id === box.id ? 'selected' : ''}" data-box="${escapeHTML(box.id)}">
      <span class="avatar">${escapeHTML(avatar(box.name))}</span>
      <span class="roster-copy"><strong>${escapeHTML(box.name)}</strong><small>${escapeHTML(stateLabel(box.state))}${box.slotId ? ' · compute assigned' : ' · storage retained'}</small></span>
      <span class="presence ${box.state === 'running' ? 'online' : ''}" title="${escapeHTML(stateLabel(box.state))}"></span>
    </button>`).join('') : '<div class="compact-empty">No logical boxes yet</div>';

  $('#external-section').hidden = external.length === 0;
  $('#external-list').innerHTML = external.map(box => `
    <button class="roster-row external-row" data-external="${escapeHTML(box.id)}">
      <span class="avatar external-avatar">↗</span>
      <span class="roster-copy"><strong>${escapeHTML(box.name)}</strong><small>external · not controller-managed</small></span>
      <span class="state-pill">${escapeHTML(stateLabel(box.state))}</span>
    </button>`).join('');

  $$('[data-box]').forEach(button => button.addEventListener('click', () => selectBox(button.dataset.box)));
  $$('[data-group]').forEach(button => button.addEventListener('click', () => selectGroup(button.dataset.group)));
  $$('[data-external]').forEach(button => button.addEventListener('click', () => showExternal(button.dataset.external)));
}

function renderMiniFleet() {
  const fleet = state.fleet;
  if (!fleet) {
    $('#fleet-mini').innerHTML = '<div class="fleet-mini-row"><span>Compute fleet</span><strong>Not configured</strong></div>';
    return;
  }
  const percent = fleet.actualSlots ? Math.round((fleet.occupiedSlots / fleet.actualSlots) * 100) : 0;
  $('#fleet-mini').innerHTML = `
    <div class="fleet-mini-row"><span>Compute fleet</span><strong>${fleet.freeSlots} free</strong></div>
    <div class="fleet-mini-row"><small>${fleet.occupiedSlots} occupied · ${fleet.actualSlots}/${fleet.desiredSlots} slots</small><small>${fleet.pendingAllocationRequests || 0} queued</small></div>
    <progress class="capacity-track" max="100" value="${percent}" aria-label="${percent}% of fleet slots occupied"></progress>`;
}

function renderDashboard() {
  const fleet = state.fleet || {};
  const running = state.boxes.filter(box => box.state === 'running').length;
  const sleeping = state.boxes.filter(box => ['detached', 'hibernated'].includes(box.state)).length;
  const cards = [
    ['Persistent boxes', state.boxes.length],
    ['Running now', running],
    ['Free compute', fleet.freeSlots ?? '—'],
    ['Hibernated', sleeping],
  ];
  $('#stats').innerHTML = cards.map(([label, value]) => `<div class="stat"><span>${label}</span><strong>${value}</strong></div>`).join('');
  const slots = fleet.slots || [];
  $('#slot-preview').innerHTML = slots.length ? slots.slice(0, 8).map(slot => `
    <div class="slot-line">
      <i class="slot-orb ${escapeHTML(slot.state)}"></i>
      <div><strong>Slot ${slot.ordinal}</strong><br><small>${escapeHTML(slot.logicalBoxName || 'Available capacity')}</small></div>
      <span class="state-pill ${escapeHTML(slot.state)}">${escapeHTML(stateLabel(slot.state))}</span>
    </div>`).join('') : '<div class="empty">No compute slots configured.</div>';
  $('#activity').innerHTML = state.boxes.length ? state.boxes.slice(0, 7).map(box => `
    <button class="activity-line" data-activity-box="${escapeHTML(box.id)}">
      <span class="avatar">${escapeHTML(avatar(box.name))}</span>
      <span><strong>${escapeHTML(box.name)}</strong><small>${escapeHTML(box.volumeName || 'persistent workspace')}</small></span>
      <span class="state-pill ${escapeHTML(box.state)}">${escapeHTML(stateLabel(box.state))}</span>
    </button>`).join('') : '<div class="empty">Create your first persistent box.</div>';
  $$('[data-activity-box]').forEach(button => button.addEventListener('click', () => selectBox(button.dataset.activityBox)));
}

function renderFleet() {
  const fleet = state.fleet;
  $('#slot-count').value = fleet?.desiredSlots ?? 4;
  if (!fleet) {
    $('#fleet-details').innerHTML = '<section class="panel empty">Add a provider credential to configure compute slots.</section>';
    return;
  }
  const slots = fleet.slots || [];
  $('#fleet-details').innerHTML = slots.length ? slots.map(slot => `
    <article class="slot-card">
      <header><div><p class="eyebrow">COMPUTE SLOT ${slot.ordinal}</p><strong>${escapeHTML(slot.logicalBoxName || 'Available')}</strong></div><span class="state-pill ${escapeHTML(slot.state)}">${escapeHTML(stateLabel(slot.state))}</span></header>
      <div class="detail-list">
        <div><span>Roster visibility</span><strong>${slot.logicalBoxId ? 'via assigned box' : 'fleet only'}</strong></div>
        <div><span>Service</span><strong title="${escapeHTML(slot.serviceId)}">${escapeHTML(slot.serviceName || short(slot.serviceId))}</strong></div>
        <div><span>Region</span><strong>${escapeHTML(slot.region || '—')}</strong></div>
        <div><span>Health</span><strong>${escapeHTML(slot.health || 'unknown')}</strong></div>
        <div><span>Deployment</span><strong title="${escapeHTML(slot.deploymentInstanceId)}">${escapeHTML(short(slot.deploymentInstanceId))}</strong></div>
      </div>
    </article>`).join('') : '<section class="panel empty">No fleet services exist yet.</section>';
}

function showDetail(title, value) {
  $('#detail-title').textContent = title;
  $('#detail-content').textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  $('#detail-dialog').showModal();
}

function renderSettings() {
  $('#credential-list').innerHTML = state.credentials.length ? state.credentials.map(value => `
    <article class="credential-card">
      <header><div><p class="eyebrow">${escapeHTML(value.provider)}</p><strong>${escapeHTML(value.name)}</strong></div><span class="vault-badge">encrypted</span></header>
      <div class="detail-list">
        <div><span>Configuration</span><strong>${escapeHTML(Object.keys(value.config || {}).join(', ') || 'default')}</strong></div>
        <div><span>Secret</span><strong>stored in vault</strong></div>
        <div><span>Updated</span><strong>${escapeHTML(fullStamp(value.updatedAt))}</strong></div>
      </div>
      <footer><button class="text-button credential-detail" data-provider="${escapeHTML(value.provider)}" data-name="${escapeHTML(value.name)}">View config</button><button class="text-button danger delete-credential" data-provider="${escapeHTML(value.provider)}" data-name="${escapeHTML(value.name)}">Remove</button></footer>
    </article>`).join('') : '<section class="panel empty">No provider credentials configured.</section>';

  $('#notification-list').innerHTML = state.notifications.length ? state.notifications.map(value => `
    <article class="credential-card">
      <header><div><p class="eyebrow">${escapeHTML(value.kind)}</p><strong>${escapeHTML(value.name)}</strong></div><span class="state-pill ${value.enabled ? 'running' : ''}">${value.enabled ? 'enabled' : 'paused'}</span></header>
      <div class="detail-list">
        <div><span>Allowed users</span><strong>${escapeHTML((value.allowedUsers || []).join(', ') || '—')}</strong></div>
        <div><span>Allowed chats</span><strong>${escapeHTML((value.allowedChats || []).join(', ') || '—')}</strong></div>
        <div><span>Updated</span><strong>${escapeHTML(fullStamp(value.updatedAt))}</strong></div>
      </div>
      <footer><button class="text-button test-notification" data-kind="${escapeHTML(value.kind)}" data-name="${escapeHTML(value.name)}">Send test</button><button class="text-button danger delete-notification" data-kind="${escapeHTML(value.kind)}" data-name="${escapeHTML(value.name)}">Remove</button></footer>
    </article>`).join('') : '<section class="panel empty">No notification destinations configured.</section>';

  $$('.credential-detail').forEach(button => button.addEventListener('click', () => {
    const item = state.credentials.find(value => value.provider === button.dataset.provider && value.name === button.dataset.name);
    if (item) showDetail(`${item.provider}/${item.name} configuration`, item.config || {});
  }));
  $$('.delete-credential').forEach(button => button.addEventListener('click', async () => {
    if (!confirm(`Remove ${button.dataset.provider}/${button.dataset.name}? Existing volumes and services are not deleted.`)) return;
    try {
      await api(`/v1/provider-credentials/${encodeURIComponent(button.dataset.provider)}/${encodeURIComponent(button.dataset.name)}`, {method:'DELETE'});
      toast('Credential removed'); await refreshAll();
    } catch (error) { toast(error.message, true); }
  }));
  $$('.test-notification').forEach(button => button.addEventListener('click', async () => {
    try {
      await api(`/v1/notifications/${encodeURIComponent(button.dataset.kind)}/${encodeURIComponent(button.dataset.name)}/test`, {method:'POST'});
      toast('Test notification delivered');
    } catch (error) { toast(error.message, true); }
  }));
  $$('.delete-notification').forEach(button => button.addEventListener('click', async () => {
    if (!confirm(`Remove notification destination ${button.dataset.kind}/${button.dataset.name}?`)) return;
    try {
      await api(`/v1/notifications/${encodeURIComponent(button.dataset.kind)}/${encodeURIComponent(button.dataset.name)}`, {method:'DELETE'});
      toast('Notification destination removed'); await refreshAll();
    } catch (error) { toast(error.message, true); }
  }));
}

function populateForms() {
  const options = state.credentials.map(value => `<option value="${escapeHTML(value.name)}" data-provider="${escapeHTML(value.provider)}">${escapeHTML(value.provider)}/${escapeHTML(value.name)}</option>`).join('');
  $('#box-credential').innerHTML = options || '<option value="">default</option>';
  $('#task-box').innerHTML = state.boxes.map(box => `<option value="${escapeHTML(box.id)}">${escapeHTML(box.name)} · ${escapeHTML(stateLabel(box.state))}</option>`).join('');
  $('#forward-box').innerHTML = state.boxes.map(box => `<option value="${escapeHTML(box.id)}">${escapeHTML(box.name)} · ${escapeHTML(box.defaultAgent || 'claude')}</option>`).join('');
}

async function refreshProviderViews(silent) {
  const preferred = state.credentials.find(value => value.provider === 'railway') || state.credentials[0];
  if (!preferred) {
    state.fleet = null; state.provider = ''; state.credential = ''; state.external = [];
    return;
  }
  state.provider = preferred.provider;
  state.credential = preferred.name;
  const fleetQuery = new URLSearchParams({provider: preferred.provider, providerCredential: preferred.name});
  const inventories = await Promise.all(state.credentials.map(async value => {
    const query = new URLSearchParams({provider: value.provider, providerCredential: value.name});
    try { return await api(`/v1/inventory?${query}`); }
    catch (error) { if (!silent) toast(`${value.provider}/${value.name}: ${error.message}`, true); return {connectedBoxes:[]}; }
  }));
  state.external = inventories.flatMap(value => value?.connectedBoxes || []);
  try { state.fleet = await api(`/v1/fleet/status?${fleetQuery}`); }
  catch (error) { state.fleet = null; if (!silent) toast(error.message, true); }
}

async function refreshAll(silent = false) {
  try {
    const [boxes, groups, credentials, notifications] = await Promise.all([
      api('/v1/logical-boxes'),
      api('/v1/chat-groups'),
      api('/v1/provider-credentials').catch(error => error.message.includes('owner role') ? [] : Promise.reject(error)),
      api('/v1/notifications').catch(error => error.message.includes('owner role') ? [] : Promise.reject(error)),
    ]);
    state.boxes = boxes || [];
    state.groups = groups || [];
    state.credentials = credentials || [];
    state.notifications = notifications || [];
    await refreshProviderViews(silent);
    renderRoster(); renderMiniFleet(); renderDashboard(); renderFleet(); renderSettings(); populateForms();
  } catch (error) {
    if (!silent) toast(error.message, true);
    throw error;
  }
}

async function selectBox(id) {
  state.group = null;
  state.box = state.boxes.find(box => box.id === id);
  if (!state.box) return;
  renderRoster();
  $('#chat-name').textContent = state.box.name;
  $('#chat-state').textContent = `${stateLabel(state.box.state)} · ${state.box.slotId ? 'compute assigned' : 'persistent storage retained'}`;
  $('#chat-avatar').textContent = avatar(state.box.name);
  $('#box-default-agent').value = state.box.defaultAgent || 'claude';
  $('#allocate').hidden = !['detached', 'hibernated'].includes(state.box.state);
  $('#hibernate').hidden = state.box.state !== 'running';
  setTerminalPane(false);
  renderTerminalPrompt(null, 'vmbox');
  showView('chat');
  try {
    state.tasks = await api(`/v1/logical-boxes/${encodeURIComponent(id)}/tasks`) || [];
    const active = [...state.tasks].reverse().find(task => task.state === 'active') || state.tasks.at(-1) || null;
    state.task = active;
    renderTaskTabs();
    if (active) await refreshConversation();
    else {
      $('#messages').innerHTML = `<div class="empty"><strong>This box is ready for a conversation.</strong><br>Send a message below. If it is offline, ${escapeHTML(state.box.defaultAgent || 'claude')} and a compute slot start automatically.</div>`;
      $('#terminal').textContent = state.box.state === 'running' ? 'No tmux agent session yet.' : 'The terminal appears after this box gets compute.';
    }
    startPolling(refreshConversation);
  } catch (error) { toast(error.message, true); }
}

function renderTaskTabs() {
  $('#task-tabs').innerHTML = `<button class="task-chip new-task-chip">＋ New session</button>${state.tasks.map(task => `<button class="task-chip ${state.task?.id === task.id ? 'selected' : ''}" data-task="${escapeHTML(task.id)}">${escapeHTML(task.agent)} · ${escapeHTML(stateLabel(task.state))}</button>`).join('')}`;
  $('.new-task-chip')?.addEventListener('click', openTaskDialog);
  $$('[data-task]').forEach(button => button.addEventListener('click', () => selectTask(button.dataset.task)));
}

async function selectTask(id) {
  state.task = state.tasks.find(task => task.id === id) || await api(`/v1/tasks/${encodeURIComponent(id)}`);
  renderTaskTabs();
  $('#chat-state').textContent = `${stateLabel(state.box.state)} · ${state.task.agent} ${stateLabel(state.task.state)}`;
  await refreshConversation();
  startPolling(refreshConversation);
}

async function refreshConversation(silent = false) {
  if (!state.box || document.hidden || state.closed) return;
  try {
    const freshBox = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}`, {timeout:10000});
    state.box = freshBox;
    $('#box-default-agent').value = freshBox.defaultAgent || 'claude';
    const index = state.boxes.findIndex(box => box.id === freshBox.id);
    if (index >= 0) state.boxes[index] = freshBox;
    $('#chat-state').textContent = `${stateLabel(freshBox.state)} · ${freshBox.slotId ? 'compute assigned' : 'persistent storage retained'}`;
    $('#allocate').hidden = !['detached', 'hibernated'].includes(freshBox.state);
    $('#hibernate').hidden = freshBox.state !== 'running';
    if (!state.task) {
      state.tasks = await api(`/v1/logical-boxes/${encodeURIComponent(freshBox.id)}/tasks`) || [];
      state.task = [...state.tasks].reverse().find(task => task.state === 'active') || state.tasks.at(-1) || null;
      renderTaskTabs();
    }
    if (state.task) {
      state.task = await api(`/v1/tasks/${encodeURIComponent(state.task.id)}`, {timeout:10000});
      const messages = await api(`/v1/tasks/${encodeURIComponent(state.task.id)}/messages`, {timeout:10000});
      state.taskMessages = messages || [];
      const container = $('#messages');
      const nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
      container.innerHTML = messages?.length ? messages.map(message => `
        <article class="message ${escapeHTML(message.direction)}">
          <div>${escapeHTML(message.text)}</div>
          <div class="message-meta"><button type="button" class="message-forward" data-forward-task-message="${escapeHTML(message.id)}">Forward</button><span>${escapeHTML(stateLabel(message.state))}</span><time>${escapeHTML(stamp(message.createdAt))}</time></div>
        </article>`).join('') : '<div class="empty">Waiting for the first message.</div>';
      $$('[data-forward-task-message]').forEach(button => button.addEventListener('click', () => {
        const message = state.taskMessages.find(value => value.id === button.dataset.forwardTaskMessage);
        if (message) openForwardDialog(message.text);
      }));
      if (nearBottom) container.scrollTop = container.scrollHeight;
      renderTaskTabs();
    }
    renderRoster();
    if (freshBox.state === 'running') await refreshTerminal(true);
    else {
      $('#terminal').textContent = 'Compute is detached. Sending a message starts this box automatically.';
      renderTerminalPrompt(null, state.task?.session || 'vmbox');
    }
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
    renderTerminalPrompt(snapshot.prompt, session);
  } catch (error) {
    $('#terminal').textContent = `Screen mirror unavailable\n\n${error.message}`;
    renderTerminalPrompt(null, session);
    if (!silent) toast(error.message, true);
  }
}

function renderTerminalPrompt(prompt, session) {
  const panel = $('#terminal-prompt');
  if (!prompt?.id || !prompt.choices?.length) {
    panel.hidden = true;
    panel.replaceChildren();
    return;
  }
  panel.hidden = false;
  panel.innerHTML = `
    <strong>${escapeHTML(prompt.text || 'Terminal input required')}</strong>
    <div class="terminal-prompt-choices">
      ${prompt.choices.map(choice => `<button type="button" class="terminal-prompt-choice" data-prompt-value="${escapeHTML(choice.value)}">${escapeHTML(choice.value)}. ${escapeHTML(choice.label)}</button>`).join('')}
    </div>
    <p>Detected from the live tmux session. Choosing once sends that exact response and presses Enter.</p>`;
  $$('[data-prompt-value]', panel).forEach(button => button.addEventListener('click', async () => {
    const buttons = $$('[data-prompt-value]', panel);
    buttons.forEach(item => { item.disabled = true; });
    try {
      const choice = prompt.choices.find(choice => choice.value === button.dataset.promptValue);
      const endpoint = `/v1/logical-boxes/${encodeURIComponent(state.box.id)}/terminal/input?session=${encodeURIComponent(session)}`;
      await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/terminal/input?session=${encodeURIComponent(session)}`, {
        method:'POST',
        headers:{'Idempotency-Key':`terminal-prompt-${session}-${prompt.id}-${button.dataset.promptValue}`},
        body:JSON.stringify({text:choice?.input || button.dataset.promptValue, submit:choice?.submit !== false}),
      });
      if (prompt.resumeInput) {
        await new Promise(resolve => setTimeout(resolve, 500));
        await api(endpoint, {
          method:'POST',
          headers:{'Idempotency-Key':`terminal-prompt-${session}-${prompt.id}-resume`},
          body:JSON.stringify({text:'\r', submit:false}),
        });
      }
      panel.hidden = true;
      toast(`Sent terminal choice ${button.dataset.promptValue}`);
      setTimeout(() => refreshTerminal(true), 350);
    } catch (error) {
      buttons.forEach(item => { item.disabled = false; });
      toast(error.message, true);
    }
  }));
}

function renderGroupMessages() {
  const container = $('#group-messages');
  const nearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
  container.innerHTML = state.groupMessages.length ? state.groupMessages.map(message => {
    const deliveries = message.deliveries || [];
    return `
      <article class="message user group-message">
        ${message.sourceBoxName ? `<p class="forward-label">Forwarded from ${escapeHTML(message.sourceBoxName)}</p>` : ''}
        <div>${escapeHTML(message.text)}</div>
        <div class="delivery-row">${deliveries.map(item => `<span class="delivery-chip ${escapeHTML(item.state)}" title="${escapeHTML(item.failureReason || item.state)}">${escapeHTML(item.boxName)} · ${escapeHTML(stateLabel(item.state))}</span>`).join('')}</div>
        <div class="message-meta"><button type="button" class="message-forward" data-forward-group-message="${escapeHTML(message.id)}">Forward</button><time>${escapeHTML(stamp(message.createdAt))}</time></div>
      </article>`;
  }).join('') : '<div class="empty"><strong>No messages yet.</strong><br>Select recipients above and start the group.</div>';
  $$('[data-forward-group-message]').forEach(button => button.addEventListener('click', () => {
    const message = state.groupMessages.find(value => value.id === button.dataset.forwardGroupMessage);
    if (message) openForwardDialog(message.text);
  }));
  if (nearBottom) container.scrollTop = container.scrollHeight;
}

function openForwardDialog(source) {
  if (!state.boxes.length) return toast('Create a destination box first', true);
  const form = $('#forward-form');
  form.reset();
  form.elements.source.value = source;
  const alternative = state.boxes.find(box => box.id !== state.box?.id) || state.boxes[0];
  form.elements.box.value = alternative.id;
  $('#forward-dialog').showModal();
}

function renderRecipientStrip() {
  if (!state.group) return;
  $('#group-members').innerHTML = state.group.members.map(member => {
    const screen = state.groupScreens.get(member.logicalBoxId);
    const preview = screen?.content ? screen.content.split('\n').slice(-8).join('\n') : (screen?.status || 'Waiting for live terminal…');
    return `
    <div class="recipient-chip ${state.recipients.has(member.logicalBoxId) ? 'selected' : ''}">
      <label class="recipient-select">
        <input type="checkbox" value="${escapeHTML(member.logicalBoxId)}" ${state.recipients.has(member.logicalBoxId) ? 'checked' : ''} ${member.canReceive ? '' : 'disabled'}>
        <span class="presence ${state.boxes.find(box => box.id === member.logicalBoxId)?.state === 'running' ? 'online' : ''}"></span>
        <span><strong>${escapeHTML(member.boxName)}</strong><small>${escapeHTML(member.agent)} · ${escapeHTML(screen?.session || 'no live session')}</small></span>
      </label>
      <button type="button" class="recipient-screen" data-screen-box="${escapeHTML(member.logicalBoxId)}" aria-label="Open ${escapeHTML(member.boxName)} terminal fullscreen"><pre>${escapeHTML(preview)}</pre><small>Open live screen ↗</small></button>
    </div>`;
  }).join('');
  $$('#group-members input').forEach(input => input.addEventListener('change', () => {
    if (input.checked) state.recipients.add(input.value); else state.recipients.delete(input.value);
    renderRecipientStrip();
  }));
  $$('[data-screen-box]').forEach(button => button.addEventListener('click', () => showGroupTerminal(button.dataset.screenBox)));
}

function showGroupTerminal(boxID) {
  const member = state.group?.members.find(value => value.logicalBoxId === boxID);
  const screen = state.groupScreens.get(boxID);
  state.previewBoxID = boxID;
  $('#terminal-preview-title').textContent = `${member?.boxName || 'Worker'} · ${screen?.agent || member?.agent || 'terminal'}`;
  $('#terminal-preview-fullscreen').textContent = screen?.content || screen?.status || 'Waiting for live terminal output…';
  if (!$('#terminal-preview-dialog').open) $('#terminal-preview-dialog').showModal();
}

async function refreshGroupScreens(force = false) {
  if (!state.group) return;
  if (!force && Date.now() - state.groupScreenRefreshAt < 4000) return;
  state.groupScreenRefreshAt = Date.now();
  const groupID = state.group.id;
  await Promise.all(state.group.members.map(async member => {
    const box = state.boxes.find(value => value.id === member.logicalBoxId);
    if (box?.state !== 'running') {
      state.groupScreens.set(member.logicalBoxId, {agent:member.agent, status:'Compute is hibernated'});
      return;
    }
    try {
      const tasks = await api(`/v1/logical-boxes/${encodeURIComponent(member.logicalBoxId)}/tasks`, {timeout:10000}) || [];
      const task = [...tasks].reverse().find(value => value.agent === member.agent && value.state === 'active') || [...tasks].reverse().find(value => value.state === 'active');
      if (!task) {
        state.groupScreens.set(member.logicalBoxId, {agent:member.agent, status:'No active agent session'});
        return;
      }
      const snapshot = await api(`/v1/logical-boxes/${encodeURIComponent(member.logicalBoxId)}/terminal?session=${encodeURIComponent(task.session)}&history=160`, {timeout:10000});
      state.groupScreens.set(member.logicalBoxId, {...snapshot, agent:task.agent});
    } catch (error) {
      state.groupScreens.set(member.logicalBoxId, {agent:member.agent, status:`Screen unavailable: ${error.message}`});
    }
  }));
  if (state.group?.id !== groupID) return;
  renderRecipientStrip();
  if (state.previewBoxID && $('#terminal-preview-dialog').open) showGroupTerminal(state.previewBoxID);
}

async function selectGroup(id) {
  state.box = null;
  state.task = null;
  state.group = state.groups.find(group => group.id === id);
  if (!state.group) return;
  state.recipients = new Set();
  state.groupScreens.clear();
  state.groupScreenRefreshAt = 0;
  $('#group-name').textContent = state.group.name;
  $('#group-state').textContent = `${state.group.members.length} persistent boxes · recipients selectable per message`;
  renderRecipientStrip();
  renderRoster();
  showView('group');
  await refreshGroupConversation(true);
  startPolling(refreshGroupConversation);
}

async function refreshGroupConversation(silent = false) {
  if (!state.group || document.hidden || state.closed) return;
  try {
    state.groupMessages = await api(`/v1/chat-groups/${encodeURIComponent(state.group.id)}/messages`, {timeout:10000}) || [];
    renderGroupMessages();
    await refreshGroupScreens(false);
  } catch (error) { if (!silent) toast(error.message, true); }
}

function showExternal(id) {
  const box = state.external.find(value => value.id === id);
  if (!box) return;
  showDetail(`${box.name} · external service`, {
    management: 'external — not managed by this controller',
    provider: box.provider,
    credential: box.providerCredential,
    state: box.state,
    providerState: box.providerState,
    region: box.region,
    image: box.image,
    resources: box.resources,
    storage: box.storage || null,
    id: box.id,
  });
}

function openTaskDialog() {
  if (!state.boxes.length) return toast('Create a logical box first', true);
  const form = $('#task-form');
  if (state.box) {
    $('#task-box').value = state.box.id;
    form.elements.agent.value = state.box.defaultAgent || 'claude';
  }
  form.elements.session.value = '';
  $('#task-dialog').showModal();
}

function renderGroupMemberOptions(group = null) {
  const existing = new Map((group?.members || []).map(member => [member.logicalBoxId, member]));
  $('#group-member-options').innerHTML = state.boxes.map(box => {
    const member = existing.get(box.id);
    return `
      <label class="member-option">
        <input type="checkbox" name="member" value="${escapeHTML(box.id)}" ${member ? 'checked' : ''}>
        <span><strong>${escapeHTML(box.name)}</strong><small>${escapeHTML(stateLabel(box.state))}</small></span>
        <select name="agent-${escapeHTML(box.id)}" aria-label="Agent for ${escapeHTML(box.name)}">
          ${['claude','codex','opencode','shell'].map(agent => `<option ${(member?.agent || box.defaultAgent || 'claude') === agent ? 'selected' : ''}>${agent}</option>`).join('')}
        </select>
      </label>`;
  }).join('') || '<div class="empty">Create at least two logical boxes first.</div>';
}

function openGroupDialog(group = null) {
  const form = $('#group-form');
  form.reset();
  form.elements.id.value = group?.id || '';
  form.elements.name.value = group?.name || '';
  $('.dialog-heading h2', form).textContent = group ? 'Edit group' : 'Create a group';
  renderGroupMemberOptions(group);
  $('#group-dialog').showModal();
}

$('#login-form').addEventListener('submit', async event => {
  event.preventDefault();
  state.token = $('#token').value.trim();
  $('#login-error').textContent = '';
  try {
    await api('/v1/logical-boxes');
    sessionStorage.setItem('vmbox.controller.token', state.token);
    $('#login').hidden = true;
    $('#app').hidden = false;
    await refreshAll();
    showView('home');
  } catch (error) {
    $('#login-error').textContent = error.message;
    state.token = '';
  }
});

$('#logout').addEventListener('click', () => logout());
$('#refresh').addEventListener('click', () => refreshAll());
$('#fleet-refresh').addEventListener('click', () => refreshAll());
$('#box-search').addEventListener('input', renderRoster);
$$('[data-view]').forEach(button => button.addEventListener('click', () => showView(button.dataset.view)));
$$('.mobile-back').forEach(button => button.addEventListener('click', () => showView('boxes')));
$('#new-box').addEventListener('click', () => $('#box-dialog').showModal());
$('#new-group').addEventListener('click', () => openGroupDialog());
$('#home-new-task').addEventListener('click', openTaskDialog);
$('#task-box').addEventListener('change', event => {
  const box = state.boxes.find(value => value.id === event.target.value);
  if (box) $('#task-form').elements.agent.value = box.defaultAgent || 'claude';
});
$('#new-credential').addEventListener('click', () => $('#credential-dialog').showModal());
$('#new-notification').addEventListener('click', () => $('#notification-dialog').showModal());
$('#edit-group').addEventListener('click', () => openGroupDialog(state.group));
$$('.close-dialog').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));

$('#box-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const selected = state.credentials.find(value => value.name === form.get('credential'));
  const body = {
    name: form.get('name'),
    provider: selected?.provider || form.get('provider'),
    providerCredential: form.get('credential'),
    defaultAgent: form.get('defaultAgent'),
    region: form.get('region'),
    diskGiB: Number(form.get('disk')),
    allocateWhenReady: form.get('allocate') === 'on',
    allocationIdempotencyKey: idempotency('create-allocate'),
  };
  try {
    await api('/v1/logical-boxes', {method:'POST', headers:{'Idempotency-Key':idempotency('box')}, body:JSON.stringify(body)});
    $('#box-dialog').close();
    event.currentTarget.reset();
    toast('Persistent workspace provisioning started');
    await refreshAll();
  } catch (error) { toast(error.message, true); }
});

$('#task-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const boxID = form.get('box');
  try {
    const task = await api(`/v1/logical-boxes/${encodeURIComponent(boxID)}/tasks`, {method:'POST', headers:{'Idempotency-Key':idempotency('task')}, body:JSON.stringify({agent:form.get('agent'), session:form.get('session'), prompt:form.get('prompt')})});
    $('#task-dialog').close();
    event.currentTarget.reset();
    toast('Agent queued');
    await refreshAll();
    await selectBox(boxID);
    await selectTask(task.id);
  } catch (error) { toast(error.message, true); }
});

$('#group-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const members = form.getAll('member').map(id => ({logicalBoxId:id, agent:form.get(`agent-${id}`) || 'claude', canReceive:true}));
  const id = form.get('id');
  try {
    const group = await api(id ? `/v1/chat-groups/${encodeURIComponent(id)}` : '/v1/chat-groups', {method:id ? 'PUT' : 'POST', body:JSON.stringify({name:form.get('name'), members})});
    $('#group-dialog').close();
    toast(id ? 'Group updated' : 'Group created');
    await refreshAll();
    await selectGroup(group.id);
  } catch (error) { toast(error.message, true); }
});

$('#credential-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  try {
    const secret = JSON.parse(form.get('secret'));
    const config = form.get('config').trim() ? JSON.parse(form.get('config')) : {};
    await api(`/v1/provider-credentials/${encodeURIComponent(form.get('provider'))}/${encodeURIComponent(form.get('name'))}`, {method:'PUT', body:JSON.stringify({secret, config})});
    $('#credential-dialog').close();
    event.currentTarget.reset();
    toast('Credential encrypted and saved');
    await refreshAll();
  } catch (error) { toast(error.message, true); }
});

$('#notification-form [name="kind"]').addEventListener('change', event => {
  const secret = $('#notification-form [name="secret"]');
  const config = $('#notification-form [name="config"]');
  if (event.target.value === 'telegram') {
    secret.placeholder = '{"token":"…","webhookSecret":"…"}';
    config.placeholder = '{"chatId":"-100123","userMap":{"456":"controller-user-id"}}';
  } else if (event.target.value === 'discord') {
    secret.placeholder = '{"webhookUrl":"https://…","publicKey":"…"}';
    config.placeholder = '{"allowedGuilds":["…"],"allowedChannels":["…"]}';
  } else {
    secret.placeholder = '{"url":"https://…","signingSecret":"…"}';
    config.placeholder = '{}';
  }
});

$('#notification-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  try {
    const secret = JSON.parse(form.get('secret'));
    const config = form.get('config').trim() ? JSON.parse(form.get('config')) : {};
    const body = {secret, config, allowedUsers:csv(form.get('users')), allowedChats:csv(form.get('chats')), enabled:form.get('enabled') === 'on'};
    await api(`/v1/notifications/${encodeURIComponent(form.get('kind'))}/${encodeURIComponent(form.get('name'))}`, {method:'PUT', body:JSON.stringify(body)});
    $('#notification-dialog').close();
    event.currentTarget.reset();
    toast('Notification destination saved');
    await refreshAll();
  } catch (error) { toast(error.message, true); }
});

$('#slot-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!state.provider) return toast('Add a provider credential first', true);
  try {
    await api('/v1/fleet/slots', {method:'PUT', body:JSON.stringify({provider:state.provider, providerCredential:state.credential, compute_box_slots:Number($('#slot-count').value)})});
    toast('Fleet target saved; reconciliation is running');
    setTimeout(() => refreshAll(true), 1800);
  } catch (error) { toast(error.message, true); }
});

$('#allocate').addEventListener('click', async () => {
  if (!state.box) return;
  try {
    const allocation = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/allocate`, {method:'POST', headers:{'Idempotency-Key':idempotency('allocate')}, body:'{}'});
    toast(allocation.state === 'queued' ? `Waiting for capacity · queue ${allocation.queuePosition || 1}` : `Allocation ${stateLabel(allocation.phase || allocation.state)}`);
    setTimeout(() => refreshConversation(true), 1500);
  } catch (error) { toast(error.message, true); }
});

$('#hibernate').addEventListener('click', async () => {
  if (!state.box || !confirm(`Hibernate ${state.box.name}? Its volume and restorable tmux state remain. Live processes stop and its compute slot becomes free.`)) return;
  try {
    await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/hibernate`, {method:'POST', body:'{}', timeout:120000});
    toast('Box hibernated; volume retained');
    await refreshAll();
    showView('boxes');
  } catch (error) { toast(error.message, true); }
});

$('#box-menu').addEventListener('click', async () => {
  if (!state.box) return;
  const typed = prompt(`Delete workspace storage permanently?\n\nBox: ${state.box.name}\nVolume: ${state.box.volumeName || state.box.volumeId}\n\nType the exact box name. Cancel keeps everything.`);
  if (typed !== state.box.name) return typed !== null && toast('Name did not match; nothing was deleted', true);
  if (!confirm(`Final confirmation: delete only ${state.box.name}'s volume? Fleet slot count stays unchanged.`)) return;
  try {
    await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/volume`, {method:'DELETE', body:JSON.stringify({confirmation:typed}), timeout:120000});
    toast('Workspace volume deleted; fleet unchanged');
    state.box = null;
    await refreshAll();
    showView('boxes');
  } catch (error) { toast(error.message, true); }
});

$('#message-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!state.box) return;
  const input = $('#message');
  const text = input.value;
  if (!text.trim()) return;
  input.value = '';
  try {
    if (state.task?.state === 'active' && state.box.state === 'running') {
      await api(`/v1/tasks/${encodeURIComponent(state.task.id)}/messages`, {method:'POST', headers:{'Idempotency-Key':idempotency('message')}, body:JSON.stringify({text}), timeout:30000});
      toast(`Message delivered to ${state.task.agent}`);
    } else {
      const pending = state.task && ['queued','waiting_capacity','starting'].includes(state.task.state) ? state.task : null;
      const body = {text, agent:pending?.agent || state.box.defaultAgent || 'claude'};
      if (pending?.session) body.session = pending.session;
      const result = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/messages`, {method:'POST', headers:{'Idempotency-Key':idempotency('direct')}, body:JSON.stringify(body), timeout:30000});
      state.task = result.task;
      if (!state.tasks.some(task => task.id === result.task.id)) state.tasks.push(result.task);
      renderTaskTabs();
      toast(result.started ? `Box is starting ${result.task.agent}` : 'Message queued');
    }
    await refreshConversation(true);
  } catch (error) {
    input.value = text;
    toast(error.message, true);
  }
});

$('#box-default-agent').addEventListener('change', async event => {
  if (!state.box) return;
  const previous = state.box.defaultAgent || 'claude';
  try {
    const box = await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}`, {method:'PATCH', body:JSON.stringify({defaultAgent:event.target.value})});
    state.box = box;
    const index = state.boxes.findIndex(value => value.id === box.id);
    if (index >= 0) state.boxes[index] = box;
    toast(`${box.name} now starts ${box.defaultAgent} by default`);
  } catch (error) {
    event.target.value = previous;
    toast(error.message, true);
  }
});

$('#group-message-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!state.group) return;
  const input = $('#group-message');
  const text = input.value;
  const recipients = [...state.recipients];
  if (!text.trim()) return;
  if (!recipients.length) return toast('Select at least one recipient', true);
  input.value = '';
  try {
    await api(`/v1/chat-groups/${encodeURIComponent(state.group.id)}/messages`, {method:'POST', headers:{'Idempotency-Key':idempotency('group')}, body:JSON.stringify({text, recipientBoxIds:recipients}), timeout:30000});
    toast('Group message queued');
    await refreshGroupConversation(true);
  } catch (error) {
    input.value = text;
    toast(error.message, true);
  }
});

$('#share-terminal').addEventListener('click', () => {
  if (!state.box) return;
  openForwardDialog($('#terminal').textContent);
});

$('#toggle-terminal').addEventListener('click', () => {
  setTerminalPane(!$('#chat-view .chat-body').classList.contains('terminal-open'));
});

$('#forward-form').addEventListener('submit', async event => {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const destination = state.boxes.find(value => value.id === form.get('box'));
  if (!destination) return toast('Choose a destination box', true);
  const append = form.get('append').trim();
  const text = append ? `${form.get('source')}\n\nForwarding note:\n${append}` : form.get('source');
  const agent = form.get('agent') || destination.defaultAgent || 'claude';
  try {
    await api(`/v1/logical-boxes/${encodeURIComponent(destination.id)}/messages`, {method:'POST', headers:{'Idempotency-Key':idempotency('forward')}, body:JSON.stringify({text, agent}), timeout:30000});
    $('#forward-dialog').close();
    toast(`Forwarded to ${destination.name} · ${agent}`);
  } catch (error) { toast(error.message, true); }
});

$('#close-terminal-preview').addEventListener('click', () => {
  state.previewBoxID = '';
  $('#terminal-preview-dialog').close();
});

$('#terminal-form').addEventListener('submit', async event => {
  event.preventDefault();
  if (!state.box) return;
  const input = $('#terminal-input');
  const text = input.value;
  if (!text) return;
  const session = state.task?.session || 'vmbox';
  input.value = '';
  try {
    await api(`/v1/logical-boxes/${encodeURIComponent(state.box.id)}/terminal/input?session=${encodeURIComponent(session)}`, {method:'POST', headers:{'Idempotency-Key':idempotency('terminal')}, body:JSON.stringify({text, submit:true})});
    setTimeout(() => refreshTerminal(true), 350);
  } catch (error) {
    input.value = text;
    toast(error.message, true);
  }
});

$('#copy-detail').addEventListener('click', async () => {
  try { await navigator.clipboard.writeText($('#detail-content').textContent); toast('Copied'); }
  catch { toast('Copy was blocked by the browser', true); }
});

for (const id of ['message', 'group-message']) {
  $(`#${id}`).addEventListener('keydown', event => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      event.target.closest('form').requestSubmit();
    }
  });
}

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    stopPolling();
  } else if (state.group) {
    refreshGroupConversation(true);
    startPolling(refreshGroupConversation);
  } else if (state.box) {
    refreshConversation(true);
    startPolling(refreshConversation);
  }
});

window.addEventListener('pagehide', () => {
  state.closed = true;
  stopPolling();
  state.controllers.forEach(controller => controller.abort());
  state.controllers.clear();
});

window.addEventListener('pageshow', event => {
  state.closed = false;
  if (!event.persisted || !state.token || $('#app').hidden) return;
  refreshAll(true);
  if (state.group) startPolling(refreshGroupConversation);
  else if (state.box) startPolling(refreshConversation);
});

if (state.token) {
  $('#token').value = state.token;
  api('/v1/logical-boxes').then(async boxes => {
    state.boxes = boxes || [];
    $('#login').hidden = true;
    $('#app').hidden = false;
    await refreshAll();
    showView('home');
  }).catch(() => logout(false));
}
