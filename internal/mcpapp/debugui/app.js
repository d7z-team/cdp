const $ = (id) => document.getElementById(id);
const state = {
  tabs: [], tabID: '', socket: null, frameMeta: null, frameURL: '', requestID: 0,
  selectionGeneration: 0, pendingFrameMeta: null, dialogID: 0,
  dragging: null, suppressClick: false, clickTimer: null, wheelX: 0, wheelY: 0, wheelTimer: null, wheelFrameSequence: 0,
  lastMove: 0, console: [], network: [], healthLoaded: false,
};

async function requestJSON(path, options = {}) {
  const response = await fetch(path, options);
  const value = await response.json().catch(() => ({}));
  if (!response.ok) throw value.error || {message: `HTTP ${response.status}`};
  return value;
}

async function callTool(name, argumentsValue = {}) {
  const output = await requestJSON(`/api/debug/tools/${encodeURIComponent(name)}`, {
    method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(argumentsValue),
  });
  showOutput(output);
  return output;
}

function errorText(error) {
  if (!error) return '操作失败';
  return [error.code, error.op, error.message].filter(Boolean).join(' · ');
}

function setStatus(text, error = false) {
  $('status').textContent = text;
  $('status').classList.toggle('error-text', error);
}

function showOutput(output) {
  const warnings = output?.warnings || [];
  $('warnings').textContent = warnings.join('\n');
  $('warnings').classList.toggle('hidden', warnings.length === 0);
  if (output?.ok === false) setStatus(errorText(output.error), true);
}

async function refreshTabs() {
  try {
    const output = await callTool('browser_tabs', {action: 'list'});
    state.tabs = output.tabs || [];
    renderTabs();
    const health = await requestJSON('/health');
    $('viewer-count').textContent = String(health.viewers ?? 0);
    if (state.tabID && !state.tabs.some((tab) => tab.id === state.tabID)) selectTab('');
    if (!state.tabID && state.tabs.length) {
      const selected = state.tabs.find((tab) => tab.active) || state.tabs[0];
      await selectTab(selected.id);
    }
  } catch (error) {
    setStatus(errorText(error), true);
  }
}

function renderTabs() {
  const root = $('tabs');
  root.replaceChildren();
  for (const tab of state.tabs) {
    const item = document.createElement('div');
    item.className = `tab-item${tab.id === state.tabID ? ' active' : ''}`;
    const main = document.createElement('div');
    main.className = 'tab-main';
    main.innerHTML = `<div class="tab-title"></div><div class="tab-url"></div>`;
    main.querySelector('.tab-title').textContent = tab.title || '(untitled)';
    main.querySelector('.tab-url').textContent = tab.url || tab.id;
    main.addEventListener('click', () => selectTab(tab.id));
    const close = document.createElement('button');
    close.className = 'tab-close'; close.textContent = '×'; close.title = '关闭标签';
    close.addEventListener('click', async () => {
      await callTool('browser_tabs', {action: 'close', tab_id: tab.id});
      if (state.tabID === tab.id) selectTab('');
      refreshTabs();
    });
    item.append(main, close); root.append(item);
  }
}

async function selectTab(tabID) {
  const generation = ++state.selectionGeneration;
  state.tabID = tabID;
  closeStream(); renderTabs(); resetFrame();
  state.console = []; state.network = []; renderEvents('console'); renderEvents('network');
  $('snapshot-markdown').textContent = '尚无 snapshot';
  if (!tabID) {
    $('page-title').textContent = '未选择页面';
    return;
  }
  try {
    const output = await callTool('browser_tabs', {action: 'select', tab_id: tabID});
    if (!output.ok || generation !== state.selectionGeneration || tabID !== state.tabID) return;
  } catch (error) {
    if (generation === state.selectionGeneration) setStatus(errorText(error), true);
    return;
  }
  connectStream(tabID, generation);
  await loadCurrent(tabID, generation);
}

function closeStream() {
  if (state.socket) state.socket.close();
  state.socket = null;
  state.pendingFrameMeta = null;
  resetPendingControls();
  $('connection-dot').className = 'dot'; $('connection-text').textContent = '未连接';
}

function connectStream(tabID, generation) {
  const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
  const socket = new WebSocket(`${scheme}//${location.host}/api/debug/stream?tab_id=${encodeURIComponent(tabID)}`);
  socket.binaryType = 'blob'; state.socket = socket;
  state.healthLoaded = false;
  socket.addEventListener('open', () => {
    if (generation !== state.selectionGeneration || tabID !== state.tabID) { socket.close(); return; }
    $('connection-dot').className = 'dot connected'; $('connection-text').textContent = '已连接';
  });
  socket.addEventListener('close', () => {
    if (state.socket === socket) {
      state.pendingFrameMeta = null;
      resetPendingControls();
      $('connection-dot').className = 'dot error'; $('connection-text').textContent = '已断开';
    }
  });
  socket.addEventListener('message', async (event) => {
    if (state.socket !== socket) return;
    if (!state.healthLoaded) {
      state.healthLoaded = true;
      requestJSON('/health').then((health) => {
        $('viewer-count').textContent = String(health.viewers ?? 0);
      }).catch(() => {});
    }
    if (typeof event.data !== 'string') {
      const frameMeta = state.pendingFrameMeta;
      state.pendingFrameMeta = null;
      if (!frameMeta) return;
      if (state.frameURL) URL.revokeObjectURL(state.frameURL);
      state.frameURL = URL.createObjectURL(event.data);
      state.frameMeta = frameMeta;
      $('frame').src = state.frameURL; $('frame').classList.add('ready'); $('empty-frame').classList.add('hidden');
      return;
    }
    const message = JSON.parse(event.data);
    if (message.type === 'frame') {
      state.pendingFrameMeta = message.frame;
      $('frame-metrics').textContent = `${message.frame.image_width}×${message.frame.image_height} · #${message.frame.sequence}`;
    } else if (message.type === 'state') {
      applyRuntimeState(message.state);
    } else if (message.type === 'events') {
      appendEvents(message.events || []);
    } else if (message.type === 'result') {
      showOutput(message.output || {ok: false, error: message.error});
      setStatus(message.ok ? '远程操作完成' : errorText(message.error || message.output?.error), !message.ok);
      await loadCurrent(tabID, generation);
      refreshTabs();
    } else if (message.type === 'error') {
      setStatus(errorText(message.error), true);
    }
  });
}

function applyRuntimeState(status) {
  if (!status) return;
  $('tab-state').textContent = status.state || '--'; $('tab-phase').textContent = status.phase || '--';
  $('snapshot-id').textContent = status.snapshot_id ? `s${status.snapshot_id}` : '--';
  $('page-title').textContent = status.title || status.url || status.tab_id;
  $('address').value = status.url || $('address').value;
  state.consoleCollected = status.console_collected;
  renderEvents('console');
  const modal = status.state === 'dialog' || status.state === 'file_chooser';
  $('modal-banner').classList.toggle('hidden', !modal);
  $('dialog-actions').classList.toggle('hidden', status.state !== 'dialog');
  $('upload-form').classList.toggle('hidden', status.state !== 'file_chooser');
  $('modal-label').textContent = status.state === 'dialog' ? 'JavaScript Dialog' : status.state === 'file_chooser' ? 'File Chooser' : '';
  $('modal-message').textContent = status.dialog?.message || '';
  const dialogID = status.state === 'dialog' ? (status.dialog?.id || 0) : 0;
  if (dialogID !== state.dialogID) {
    state.dialogID = dialogID;
    $('dialog-prompt').value = status.dialog?.type === 'prompt' ? (status.dialog.default_prompt || '') : '';
  }
}

function sendControl(action, values = {}) {
  if (!state.socket || state.socket.readyState !== WebSocket.OPEN) return;
  if (['move', 'click', 'double_click', 'right_click', 'drag', 'wheel'].includes(action)) {
    if (!state.frameMeta) return;
    if (!values.frame_sequence) values = {...values, frame_sequence: state.frameMeta.sequence};
  }
  state.socket.send(JSON.stringify({id: `c${++state.requestID}`, type: 'control', action, ...values}));
}

function resetPendingControls() {
  clearTimeout(state.clickTimer); clearTimeout(state.wheelTimer);
  state.clickTimer = null; state.wheelTimer = null; state.wheelX = 0; state.wheelY = 0; state.wheelFrameSequence = 0;
  state.dragging = null; state.suppressClick = false; state.lastMove = 0;
  $('cursor').classList.add('hidden');
}

function resetFrame() {
  state.frameMeta = null; state.pendingFrameMeta = null; state.dialogID = 0; resetPendingControls();
  $('frame').classList.remove('ready'); $('frame').removeAttribute('src');
  $('empty-frame').classList.remove('hidden'); $('cursor').classList.add('hidden');
  if (state.frameURL) URL.revokeObjectURL(state.frameURL); state.frameURL = '';
}

function viewportPoint(event) {
  const image = $('frame'); const rect = image.getBoundingClientRect(); const meta = state.frameMeta;
  if (!meta || rect.width <= 0 || rect.height <= 0) return null;
  if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) return null;
  const x = (event.clientX - rect.left) * meta.css_viewport_width / rect.width;
  const y = (event.clientY - rect.top) * meta.css_viewport_height / rect.height;
  return {x: Math.max(0, Math.min(meta.css_viewport_width, x)), y: Math.max(0, Math.min(meta.css_viewport_height, y)), frameSequence: meta.sequence, rect};
}

function positionCursor(point) {
  const viewportRect = $('viewport').getBoundingClientRect();
  $('cursor').style.left = `${point.rect.left - viewportRect.left + point.x * point.rect.width / state.frameMeta.css_viewport_width}px`;
  $('cursor').style.top = `${point.rect.top - viewportRect.top + point.y * point.rect.height / state.frameMeta.css_viewport_height}px`;
  $('cursor').classList.remove('hidden');
}

async function loadCurrent(tabID = state.tabID, generation = state.selectionGeneration) {
  if (!tabID) return;
  try {
    const output = await requestJSON(`/api/debug/current?tab_id=${encodeURIComponent(tabID)}`);
    if (generation !== state.selectionGeneration || tabID !== state.tabID) return;
    $('snapshot-markdown').textContent = output.snapshot?.markdown || '';
    applyRuntimeState(output.state);
  } catch (error) {
    if (generation === state.selectionGeneration && tabID === state.tabID && error.code !== 'stale_target') setStatus(errorText(error), true);
  }
}

function appendEvents(events) {
  for (const event of events) {
    if (event.type === 'console') state.console.push(event); else state.network.push(event);
  }
  state.console = state.console.slice(-300); state.network = state.network.slice(-500);
  renderEvents('console'); renderEvents('network');
}

function renderEvents(kind) {
  const values = state[kind]; const root = $(`${kind}-events`); root.replaceChildren();
  if (kind === 'console' && state.consoleCollected === false) { root.textContent = '未采集日志（diagnostics=off）'; return; }
  for (const event of values) {
    const item = document.createElement('div'); item.className = 'event-item';
    const meta = document.createElement('div'); meta.className = 'event-meta'; meta.textContent = `${event.type} · ${new Date(event.time).toLocaleTimeString()}`;
    const data = document.createElement('div'); data.className = 'event-data'; data.textContent = kind === 'console' ? (event.data?.text || JSON.stringify(event.data)) : JSON.stringify(event.data, null, 2);
    item.append(meta, data); root.append(item);
  }
  root.scrollTop = root.scrollHeight;
}

$('navigate-form').addEventListener('submit', async (event) => {
  event.preventDefault(); const url = $('address').value.trim(); if (!url) return;
  const output = await callTool('browser_navigate', {tab_id: state.tabID || undefined, url});
  if (output.ok) { await refreshTabs(); await selectTab(output.tab_id); $('snapshot-markdown').textContent = output.snapshot?.markdown || ''; }
});
$('new-tab').addEventListener('click', async () => { const output = await callTool('browser_tabs', {action: 'new'}); await refreshTabs(); if (output.tab_id) selectTab(output.tab_id); });
$('refresh-tabs').addEventListener('click', refreshTabs);
$('back').addEventListener('click', async () => { if (state.tabID) { const out = await callTool('browser_history', {tab_id: state.tabID, action: 'back'}); if (out.snapshot) $('snapshot-markdown').textContent = out.snapshot.markdown; } });
$('forward').addEventListener('click', async () => { if (state.tabID) { const out = await callTool('browser_history', {tab_id: state.tabID, action: 'forward'}); if (out.snapshot) $('snapshot-markdown').textContent = out.snapshot.markdown; } });
$('reload').addEventListener('click', async () => { if (state.tabID) { const out = await callTool('browser_history', {tab_id: state.tabID, action: 'reload'}); if (out.snapshot) $('snapshot-markdown').textContent = out.snapshot.markdown; } });
$('capture-snapshot').addEventListener('click', async () => { if (state.tabID) { const out = await callTool('browser_snapshot', {tab_id: state.tabID}); if (out.snapshot) $('snapshot-markdown').textContent = out.snapshot.markdown; } });
$('capture-shot').addEventListener('click', async () => {
  if (!state.tabID) return; const response = await fetch(`/api/debug/screenshot?tab_id=${encodeURIComponent(state.tabID)}&format=png`);
  if (!response.ok) { const value = await response.json(); setStatus(errorText(value.error), true); return; }
  const url = URL.createObjectURL(await response.blob()); const link = document.createElement('a'); link.href = url; link.download = `cdp-${state.tabID}.png`; link.click(); URL.revokeObjectURL(url);
});

document.querySelectorAll('.inspector-tabs button').forEach((button) => button.addEventListener('click', () => {
  document.querySelectorAll('.inspector-tabs button').forEach((item) => item.classList.toggle('active', item === button));
  document.querySelectorAll('.inspector-view').forEach((panel) => panel.classList.toggle('active', panel.id === `panel-${button.dataset.panel}`));
}));
document.querySelectorAll('[data-clear]').forEach((button) => button.addEventListener('click', () => { state[button.dataset.clear] = []; renderEvents(button.dataset.clear); }));

$('find-form').addEventListener('submit', async (event) => {
  event.preventDefault(); if (!state.tabID) return;
  const output = await callTool('browser_find', {tab_id: state.tabID, text: $('find-text').value, role: $('find-role').value, name: $('find-name').value});
  const root = $('find-results'); root.replaceChildren();
  for (const match of output.matches || []) {
    const item = document.createElement('div'); item.className = 'result-item';
    const head = document.createElement('div'); head.className = 'result-head';
    const label = document.createElement('span'); label.textContent = `${match.node.role || match.node.kind} ${match.node.name || match.node.text || ''}`;
    head.append(label);
    if (match.ref) { const button = document.createElement('button'); button.textContent = match.ref; button.className = 'secondary ref'; button.addEventListener('click', async () => { await callTool('browser_click', {tab_id: state.tabID, ref: match.ref}); loadCurrent(); }); head.append(button); }
    item.append(head); root.append(item);
  }
});

$('evaluate-form').addEventListener('submit', async (event) => {
  event.preventDefault(); if (!state.tabID) return;
  const output = await callTool('browser_evaluate', {tab_id: state.tabID, ref: $('evaluate-ref').value || undefined, expression: $('evaluate-expression').value});
  $('evaluate-result').textContent = output.ok ? JSON.stringify(output.value, null, 2) : errorText(output.error);
});

$('dialog-accept').addEventListener('click', () => callTool('browser_dialog', {tab_id: state.tabID, action: 'accept', prompt_text: $('dialog-prompt').value}));
$('dialog-dismiss').addEventListener('click', () => callTool('browser_dialog', {tab_id: state.tabID, action: 'dismiss'}));
$('upload-form').addEventListener('submit', async (event) => {
  event.preventDefault(); const body = new FormData(); body.set('tab_id', state.tabID); body.set('ref', $('upload-ref').value);
  for (const file of $('upload-files').files) body.append('files', file);
  const output = await requestJSON('/api/debug/upload', {method: 'POST', body}); showOutput(output);
});
$('text-form').addEventListener('submit', (event) => { event.preventDefault(); if ($('text-input').value) { sendControl('text', {text: $('text-input').value}); $('text-input').value = ''; } });

const viewport = $('viewport');
viewport.addEventListener('pointerdown', (event) => { const point = viewportPoint(event); if (!point || event.button !== 0) return; viewport.focus({preventScroll: true}); state.dragging = point; state.suppressClick = false; viewport.setPointerCapture(event.pointerId); });
viewport.addEventListener('pointerup', (event) => {
  if (!state.dragging || event.button !== 0) return;
  const from = state.dragging; state.dragging = null; const point = viewportPoint(event); if (!point) return;
  const distance = Math.hypot(point.x - from.x, point.y - from.y);
  if (distance > 5) { state.suppressClick = true; sendControl('drag', {x: from.x, y: from.y, to_x: point.x, to_y: point.y, frame_sequence: from.frameSequence}); }
});
viewport.addEventListener('pointercancel', () => { state.dragging = null; state.suppressClick = false; });
viewport.addEventListener('click', (event) => {
  if (state.suppressClick) { state.suppressClick = false; return; }
  const point = viewportPoint(event); if (!point) return; positionCursor(point); clearTimeout(state.clickTimer);
  state.clickTimer = setTimeout(() => sendControl('click', {x: point.x, y: point.y, frame_sequence: point.frameSequence}), 220);
});
viewport.addEventListener('dblclick', (event) => { const point = viewportPoint(event); if (!point) return; clearTimeout(state.clickTimer); sendControl('double_click', {x: point.x, y: point.y, frame_sequence: point.frameSequence}); });
viewport.addEventListener('contextmenu', (event) => { event.preventDefault(); const point = viewportPoint(event); if (point) sendControl('right_click', {x: point.x, y: point.y, frame_sequence: point.frameSequence}); });
viewport.addEventListener('pointermove', (event) => {
  const point = viewportPoint(event); if (!point) return;
  if ($('sync-hover').checked && performance.now() - state.lastMove > 50) { state.lastMove = performance.now(); sendControl('move', {x: point.x, y: point.y, frame_sequence: point.frameSequence}); }
});
viewport.addEventListener('wheel', (event) => {
  event.preventDefault(); const point = viewportPoint(event); if (!point) return;
  if (state.wheelFrameSequence && state.wheelFrameSequence !== point.frameSequence) { state.wheelX = 0; state.wheelY = 0; }
  state.wheelFrameSequence = point.frameSequence; state.wheelX += event.deltaX; state.wheelY += event.deltaY; clearTimeout(state.wheelTimer);
  state.wheelTimer = setTimeout(() => { sendControl('wheel', {x: point.x, y: point.y, delta_x: state.wheelX, delta_y: state.wheelY, frame_sequence: point.frameSequence}); state.wheelX = 0; state.wheelY = 0; state.wheelFrameSequence = 0; }, 80);
}, {passive: false});
viewport.addEventListener('keydown', (event) => {
  if (!state.tabID) return; event.preventDefault();
  const modifiers = []; if (event.altKey) modifiers.push('Alt'); if (event.ctrlKey) modifiers.push('Control'); if (event.metaKey) modifiers.push('Meta'); if (event.shiftKey) modifiers.push('Shift');
  if (event.key.length === 1 && modifiers.every((value) => value === 'Shift')) sendControl('text', {text: event.key}); else sendControl('key', {key: event.key, modifiers});
});

window.addEventListener('beforeunload', () => { closeStream(); if (state.frameURL) URL.revokeObjectURL(state.frameURL); });
refreshTabs();
