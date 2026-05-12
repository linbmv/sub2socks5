const statusEl = document.getElementById('socks-status');
const socksListEl = document.getElementById('socks-list');
const addSocksButton = document.getElementById('add-socks');

let formPorts = [];
let fullConfig = {};
let latestAvailableOutbounds = [];

async function load() {
  const response = await fetch('/api/config');
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || '加载配置失败');
  }
  fullConfig = data.config || {};
  latestAvailableOutbounds = data.availableOutbounds || [];
  formPorts = normalizePorts(fullConfig.ports || []);
  render();
}

function normalizePorts(ports) {
  if (!Array.isArray(ports) || !ports.length) {
    return [createDefaultPort()];
  }
  return ports.map((item, index) => ({
    tag: item.tag || `socks-${index + 1}`,
    listen: item.listen || '127.0.0.1',
    port: item.port || '',
    target: item.target || 'proxy',
    sniff: true
  }));
}

function createDefaultPort() {
  return {
    tag: `socks-${formPorts.length + 1 || 1}`,
    listen: '127.0.0.1',
    port: '',
    target: fullConfig?.routing?.routeFinal || 'proxy',
    sniff: true
  };
}

function render() {
  socksListEl.innerHTML = '';
  if (!formPorts.length) {
    socksListEl.innerHTML = '<div class="timeline-item"><div class="title">暂无 SOCKS5 服务</div></div>';
    return;
  }
  for (const [index, portItem] of formPorts.entries()) {
    const item = document.createElement('div');
    item.className = 'timeline-item';
    item.innerHTML = `
      <div class="title">SOCKS5 服务 ${index + 1}</div>
      <div class="form-grid">
        <label>
          <span>tag</span>
          <input data-port-index="${index}" data-port-field="tag" value="${escapeHtmlAttr(portItem.tag || '')}" />
        </label>
        <label>
          <span>监听地址</span>
          <input data-port-index="${index}" data-port-field="listen" value="${escapeHtmlAttr(portItem.listen || '127.0.0.1')}" />
        </label>
        <label>
          <span>端口</span>
          <input data-port-index="${index}" data-port-field="port" type="number" min="1" step="1" value="${escapeHtmlAttr(String(portItem.port || ''))}" />
        </label>
        <label>
          <span>目标出口</span>
          <select data-port-index="${index}" data-port-field="target">
            ${buildOutboundOptionsHtml(portItem.target)}
          </select>
        </label>
      </div>
      <div class="section-heading-actions">
        ${formPorts.length > 1 ? `<button type="button" data-remove-port="${index}">删除</button>` : ''}
      </div>
    `;
    socksListEl.appendChild(item);
  }
}

function buildOutboundOptionsHtml(selectedTag) {
  const outbounds = latestAvailableOutbounds?.length
    ? latestAvailableOutbounds
    : [{ tag: 'direct', label: 'direct', type: 'direct', source: 'builtin' }];
  return outbounds
    .map((optionInfo) => (
      `<option value="${escapeHtmlAttr(optionInfo.tag)}" ${optionInfo.tag === selectedTag ? 'selected' : ''}>${escapeHtml(optionInfo.label || optionInfo.tag)}</option>`
    ))
    .join('');
}

function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

function escapeHtmlAttr(str) {
  return escapeHtml(str).replace(/"/g, '&quot;');
}

function setStatus(message, kind = 'idle') {
  statusEl.textContent = message;
  statusEl.className = `status-bar is-${kind}`;
}

async function resolveNextPort(host, start, exclude = []) {
  const response = await fetch('/api/ports/next', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ host, start, exclude })
  });
  const data = await response.json();
  return Number(data.port || start);
}

async function assignMissingSuggestedPorts() {
  const host = '127.0.0.1';
  const used = new Set(
    formPorts
      .map((item) => Number(item.port))
      .filter((value) => Number.isInteger(value) && value > 0)
  );
  let changed = false;
  for (let index = 0; index < formPorts.length; index += 1) {
    if (Number(formPorts[index].port) > 0) continue;
    const start = index === 0
      ? Number(fullConfig?.app?.port || 18080) + 1
      : Number(formPorts[index - 1].port || 0) + 1;
    const nextPort = await resolveNextPort(host, start, [...used]);
    formPorts[index].port = nextPort;
    used.add(nextPort);
    changed = true;
  }
  return changed;
}

document.getElementById('back-home').addEventListener('click', () => {
  window.location.href = '/';
});

document.getElementById('save-socks').addEventListener('click', async () => {
  try {
    const seenTags = new Set();
    const seenPorts = new Set();
    for (const portItem of formPorts) {
      if (!portItem.tag?.trim()) throw new Error('SOCKS5 服务 tag 不能为空');
      if (seenTags.has(portItem.tag)) throw new Error(`SOCKS5 服务 tag 重复：${portItem.tag}`);
      seenTags.add(portItem.tag);
      if (!portItem.listen?.trim()) throw new Error(`SOCKS5 服务 ${portItem.tag} 监听地址不能为空`);
      const portNum = Number(portItem.port);
      if (!Number.isInteger(portNum) || portNum <= 0) throw new Error(`SOCKS5 服务 ${portItem.tag} 端口无效`);
      if (seenPorts.has(`${portItem.listen}:${portNum}`)) {
        throw new Error(`SOCKS5 服务监听重复：${portItem.listen}:${portNum}`);
      }
      seenPorts.add(`${portItem.listen}:${portNum}`);
      if (!portItem.target) throw new Error(`SOCKS5 服务 ${portItem.tag} 目标出口不能为空`);
    }

    fullConfig.ports = formPorts.map((item, index) => ({
      tag: item.tag?.trim() || `socks-${index + 1}`,
      listen: item.listen?.trim() || '127.0.0.1',
      port: Number(item.port || 0),
      target: item.target || fullConfig?.routing?.routeFinal || 'proxy',
      sniff: true
    }));

    const response = await fetch('/api/config', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(fullConfig)
    });
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data?.error?.message || '保存失败');
    }
    setStatus('SOCKS5 服务已保存', 'success');
  } catch (error) {
    setStatus(`保存失败：${error.message}`, 'error');
  }
});

addSocksButton.addEventListener('click', async () => {
  formPorts.push(createDefaultPort());
  await assignMissingSuggestedPorts();
  render();
});

document.addEventListener('input', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  if (target.dataset.portIndex) {
    const index = Number(target.dataset.portIndex);
    const field = target.dataset.portField;
    formPorts[index][field] = target.value;
  }
});

document.addEventListener('change', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;
  if (target.dataset.portIndex) {
    const index = Number(target.dataset.portIndex);
    const field = target.dataset.portField;
    formPorts[index][field] = target.value;
  }
});

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;
  if (target.dataset.removePort) {
    const index = Number(target.dataset.removePort);
    if (formPorts.length > 1) {
      formPorts.splice(index, 1);
      render();
    }
  }
});

load()
  .then(() => setStatus('准备就绪', 'idle'))
  .catch((error) => setStatus(`加载失败：${error.message}`, 'error'));
