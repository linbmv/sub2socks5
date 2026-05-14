const statusEl = document.getElementById('edit-node-status');
const inputEl = document.getElementById('edit-manual-node-input');
const importResultEl = document.getElementById('edit-manual-import-result');
const nodeListEl = document.getElementById('edit-node-list');
const formTypeEl = document.getElementById('manual-form-type');
const formTagEl = document.getElementById('manual-form-tag');
const formServerEl = document.getElementById('manual-form-server');
const formPortEl = document.getElementById('manual-form-port');
const formFieldsEl = document.getElementById('manual-form-fields');
const NODES_UPDATED_KEY = 'sub2socks5:nodes-updated-at';

const FORM_PROTOCOLS = {
  vless: [
    { key: 'uuid', label: 'UUID' },
    { key: 'flow', label: 'Flow' },
    { key: 'tlsServerName', label: 'SNI' },
    { key: 'tlsInsecure', label: '允许不安全 TLS', type: 'bool' }
  ],
  vmess: [
    { key: 'uuid', label: 'UUID' },
    { key: 'security', label: 'Security', defaultValue: 'auto' },
    { key: 'alter_id', label: 'Alter ID', defaultValue: '0' },
    { key: 'tlsServerName', label: 'SNI' },
    { key: 'tlsInsecure', label: '允许不安全 TLS', type: 'bool' }
  ],
  trojan: [
    { key: 'password', label: 'Password' },
    { key: 'tlsServerName', label: 'SNI' },
    { key: 'tlsInsecure', label: '允许不安全 TLS', type: 'bool' }
  ],
  shadowsocks: [
    { key: 'method', label: 'Method' },
    { key: 'password', label: 'Password' }
  ],
  socks: [
    { key: 'username', label: 'Username' },
    { key: 'password', label: 'Password' }
  ],
  hysteria2: [
    { key: 'password', label: 'Password' },
    { key: 'tlsServerName', label: 'SNI' },
    { key: 'tlsInsecure', label: '允许不安全 TLS', type: 'bool' },
    { key: 'up_mbps', label: '上行 Mbps' },
    { key: 'down_mbps', label: '下行 Mbps' },
    { key: 'obfsType', label: '混淆类型(obfs)' },
    { key: 'obfsPassword', label: '混淆密码' }
  ],
  tuic: [
    { key: 'uuid', label: 'UUID' },
    { key: 'password', label: 'Password' },
    { key: 'tlsServerName', label: 'SNI' },
    { key: 'tlsInsecure', label: '允许不安全 TLS', type: 'bool' },
    { key: 'congestion_control', label: '拥塞控制', defaultValue: 'bbr' },
    { key: 'alpn', label: 'ALPN(逗号分隔)', defaultValue: 'h3' },
    { key: 'zero_rtt_handshake', label: '0-RTT', type: 'bool' }
  ]
};

let state = {
  subscriptionNodes: [],
  disabledSubscriptionTags: [],
  manualNodes: [],
  groups: [],
  chains: [],
  availableOutbounds: [],
  fallbackStates: {}
};

async function load() {
  const response = await fetch('/api/nodes');
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || '加载节点失败');
  }
  state = data;
  render();
}

function render() {
  renderImportTabs();
  renderFormFields();
  renderNodeList();
}

function renderImportTabs() {
  setTab('form');
  if (!formTypeEl.options.length) {
    for (const key of Object.keys(FORM_PROTOCOLS)) {
      const option = document.createElement('option');
      option.value = key;
      option.textContent = key;
      formTypeEl.appendChild(option);
    }
    formTypeEl.value = 'vless';
    formPortEl.value = '443';
  }
}

function setTab(tab) {
  const isForm = tab === 'form';
  document.getElementById('node-import-tab-form').classList.toggle('is-active', isForm);
  document.getElementById('node-import-tab-raw').classList.toggle('is-active', !isForm);
  document.getElementById('node-import-form-panel').classList.toggle('is-hidden', !isForm);
  document.getElementById('node-import-form-panel').classList.toggle('is-active', isForm);
  document.getElementById('node-import-raw-panel').classList.toggle('is-hidden', isForm);
  document.getElementById('node-import-raw-panel').classList.toggle('is-active', !isForm);
}

function renderFormFields() {
  const type = formTypeEl.value || 'vless';
  const fields = FORM_PROTOCOLS[type] || [];
  formFieldsEl.innerHTML = fields.map((field) => `
    <label>
      <span>${escapeHtml(field.label)}</span>
      ${field.type === 'bool'
        ? `<select data-manual-field="${escapeHtml(field.key)}"><option value="false">否</option><option value="true">是</option></select>`
        : `<input data-manual-field="${escapeHtml(field.key)}" value="${escapeHtml(field.defaultValue || '')}" />`}
    </label>
  `).join('');
  formPortEl.value = type === 'socks' ? '1080' : (formPortEl.value || '443');
}

function renderNodeList() {
  nodeListEl.innerHTML = '';
  const nodes = [
    ...state.subscriptionNodes.map((node) => ({ ...node, source: 'subscription' })),
    ...state.manualNodes.map((node) => ({ ...node, source: 'manual' }))
  ];
  if (!nodes.length) {
    nodeListEl.innerHTML = '<div class="timeline-item"><div class="title">暂无节点</div></div>';
    return;
  }

  for (const node of nodes) {
    const isDisabled = node.source === 'subscription'
      ? state.disabledSubscriptionTags.includes(node.tag)
      : false;
    const card = document.createElement('div');
    card.className = 'node-edit-card';
    const actionAttr = node.source === 'manual'
      ? `data-delete-manual-node="${escapeHtmlAttr(node.tag)}"`
      : isDisabled
        ? `data-enable-subscription-node="${escapeHtmlAttr(node.tag)}"`
        : `data-delete-subscription-node="${escapeHtmlAttr(node.tag)}"`;
    const actionClass = node.source === 'manual'
      ? 'danger-icon-button'
      : isDisabled
        ? 'success-text-button'
        : 'danger-icon-button';
    const actionText = node.source === 'manual'
      ? '🗑'
      : isDisabled
        ? '启用'
        : '🗑';
    const titleClass = isDisabled ? 'node-pill-title is-disabled' : 'node-pill-title';
    card.innerHTML = `
      <div class="node-pill">
        <div class="${titleClass}">${escapeHtml(node.tag || '')}</div>
        <div class="node-pill-tags">
          <span class="node-pill-tag">${escapeHtml(node.type || '')}</span>
          <span class="node-pill-tag is-source">${node.source === 'manual' ? '手动' : '订阅'}</span>
        </div>
      </div>
      <button type="button" class="${actionClass}" ${actionAttr} title="${node.source === 'manual' ? '删除节点' : (isDisabled ? '启用节点' : '禁用节点')}">${actionText}</button>
    `;
    nodeListEl.appendChild(card);
  }
}

function renderImportResult(result) {
  if (!result) {
    importResultEl.innerHTML = '';
    return;
  }

  const warnings = Array.isArray(result.warnings) ? result.warnings : [];
  const items = [];
  if (result.nodes?.length) {
    items.push(`<div class="timeline-item"><div class="title">成功解析 ${result.nodes.length} 个节点</div></div>`);
  }
  for (const warning of warnings) {
    items.push(`<div class="timeline-item"><div class="title">提示</div><div class="details">${escapeHtml(warning)}</div></div>`);
  }
  importResultEl.innerHTML = items.join('') || '<div class="timeline-item"><div class="title">没有可导入节点</div></div>';
}

function buildFormNode() {
  const type = formTypeEl.value;
  const node = {
    type,
    tag: formTagEl.value.trim(),
    server: formServerEl.value.trim(),
    server_port: Number(formPortEl.value || 0)
  };

  for (const fieldEl of formFieldsEl.querySelectorAll('[data-manual-field]')) {
    const key = fieldEl.dataset.manualField;
    const rawValue = fieldEl.value;
    const value = rawValue.trim();
    if (!value) continue;
    if (key === 'tlsServerName') {
      node.tls = {
        enabled: true,
        server_name: value,
        insecure: false
      };
      continue;
    }
    if (key === 'tlsInsecure') {
      node.tls = node.tls || { enabled: true, server_name: node.server || '', insecure: false };
      node.tls.insecure = value === 'true';
      continue;
    }
    if (key === 'obfsType') {
      node.obfs = node.obfs || {};
      node.obfs.type = value;
      continue;
    }
    if (key === 'obfsPassword') {
      node.obfs = node.obfs || {};
      node.obfs.password = value;
      continue;
    }
    if (key === 'alpn') {
      node.tls = node.tls || { enabled: true, server_name: node.server || '', insecure: false };
      node.tls.alpn = value.split(',').map((item) => item.trim()).filter(Boolean);
      continue;
    }
    if (key === 'zero_rtt_handshake') {
      node.zero_rtt_handshake = value === 'true';
      continue;
    }
    node[key] = key === 'alter_id' ? Number(value) : value;
  }

  if (!node.tag || !node.server || !node.server_port) {
    throw new Error('表单节点至少需要名称、服务器和端口');
  }

  return node;
}

function setStatus(message, kind = 'idle') {
  statusEl.textContent = message;
  statusEl.className = `status-bar is-${kind}`;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function escapeHtmlAttr(value) {
  return escapeHtml(value);
}

document.getElementById('back-nodes').addEventListener('click', () => {
  window.location.href = '/nodes.html';
});

document.getElementById('node-import-tab-form').addEventListener('click', () => setTab('form'));
document.getElementById('node-import-tab-raw').addEventListener('click', () => setTab('raw'));
formTypeEl.addEventListener('change', () => renderFormFields());

document.getElementById('add-manual-form-node').addEventListener('click', () => {
  try {
    const node = buildFormNode();
    state.manualNodes.push(node);
    renderNodeList();
    setStatus(`已添加表单节点 ${node.tag}，请记得保存`, 'success');
  } catch (error) {
    setStatus(error.message, 'error');
  }
});

document.getElementById('import-edit-manual-nodes').addEventListener('click', async () => {
  try {
    const response = await fetch('/api/nodes/import', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ raw: inputEl.value })
    });
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data?.error?.message || '导入失败');
    }
    const normalized = normalizeImportedNodes(data.nodes || []);
    state.manualNodes.push(...normalized);
    inputEl.value = '';
    renderImportResult({ ...data, nodes: normalized });
    renderNodeList();
    setStatus(`成功导入 ${normalized.length || 0} 个节点`, 'success');
  } catch (error) {
    setStatus(error.message, 'error');
  }
});

document.getElementById('save-edit-nodes').addEventListener('click', async () => {
  try {
    const duplicateManualTag = findDuplicateTag(state.manualNodes);
    if (duplicateManualTag) {
      throw new Error(`手动节点 tag 重复：${duplicateManualTag}`);
    }
    const response = await fetch('/api/nodes', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        manualNodes: state.manualNodes,
        groups: state.groups,
        chains: state.chains,
        disabledSubscriptionTags: state.disabledSubscriptionTags
      })
    });
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data?.error?.message || '保存失败');
    }
    localStorage.setItem(NODES_UPDATED_KEY, String(Date.now()));
    setStatus('节点配置已保存', 'success');
    await load();
  } catch (error) {
    setStatus(error.message, 'error');
  }
});

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;

  if (target.dataset.deleteManualNode) {
    state.manualNodes = state.manualNodes.filter((node) => node.tag !== target.dataset.deleteManualNode);
    renderNodeList();
    setStatus('已移除手动节点，请记得保存', 'idle');
  }

  if (target.dataset.deleteSubscriptionNode) {
    if (!state.disabledSubscriptionTags.includes(target.dataset.deleteSubscriptionNode)) {
      state.disabledSubscriptionTags.push(target.dataset.deleteSubscriptionNode);
    }
    renderNodeList();
    setStatus('已禁用订阅节点，请记得保存', 'idle');
  }

  if (target.dataset.enableSubscriptionNode) {
    state.disabledSubscriptionTags = state.disabledSubscriptionTags.filter((tag) => tag !== target.dataset.enableSubscriptionNode);
    renderNodeList();
    setStatus('已重新启用订阅节点，请记得保存', 'success');
  }
});

load().catch((error) => setStatus(error.message, 'error'));

function findDuplicateTag(nodes) {
  const seen = new Set();
  for (const node of nodes || []) {
    const tag = String(node?.tag || '').trim();
    if (!tag) continue;
    if (seen.has(tag)) return tag;
    seen.add(tag);
  }
  return '';
}

function normalizeImportedNodes(nodes) {
  const list = Array.isArray(nodes) ? nodes : [];
  return list.map((node) => normalizeSingleNode(node)).filter(Boolean);
}

function normalizeSingleNode(node) {
  if (!node || typeof node !== 'object') return null;

  const protocol = String(node.type || node.protocol || '').toLowerCase().trim();
  if (!protocol) return node;

  if (!node.type && node.protocol) {
    if (protocol === 'hysteria') {
      return normalizeV2rayHysteriaToHy2(node);
    }
    return node;
  }

  const normalized = { ...node };
  if (normalized.type === 'ss') normalized.type = 'shadowsocks';
  if (normalized.type === 'socks5') normalized.type = 'socks';
  return normalized;
}

function normalizeV2rayHysteriaToHy2(node) {
  const settings = node.settings || {};
  const streamSettings = node.streamSettings || {};
  const tlsSettings = streamSettings.tlsSettings || {};
  const hysteriaSettings = streamSettings.hysteriaSettings || {};
  const finalmask = streamSettings.finalmask || {};
  const udpMasks = Array.isArray(finalmask.udp) ? finalmask.udp : [];
  const salamander = udpMasks.find((m) => String(m?.type || '').toLowerCase() === 'salamander');
  const out = {
    type: 'hysteria2',
    tag: String(node.tag || 'hysteria2-node').trim(),
    server: String(settings.address || '').trim(),
    server_port: Number(settings.port || 0),
    password: String(hysteriaSettings.auth || '').trim()
  };

  out.tls = {
    enabled: true,
    server_name: String(tlsSettings.serverName || settings.address || '').trim(),
    insecure: Boolean(tlsSettings.allowInsecure)
  };

  const up = parseRateMbps(hysteriaSettings.up);
  const down = parseRateMbps(hysteriaSettings.down);
  if (up > 0) out.up_mbps = up;
  if (down > 0) out.down_mbps = down;

  if (salamander?.settings?.password) {
    out.obfs = {
      type: 'salamander',
      password: String(salamander.settings.password).trim()
    };
  }
  return out;
}

function parseRateMbps(value) {
  const text = String(value || '').toLowerCase().trim().replace(/mbps$/i, '').replace(/m$/i, '').trim();
  const n = Number(text);
  return Number.isFinite(n) && n > 0 ? Math.round(n) : 0;
}
