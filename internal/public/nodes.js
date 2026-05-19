const statusEl = document.getElementById('node-status');
const groupsEl = document.getElementById('groups');
const chainsEl = document.getElementById('chains');
const availableNodeListEl = document.getElementById('available-node-list');
const groupsSectionBodyEl = document.getElementById('groups-section-body');
const chainsSectionBodyEl = document.getElementById('chains-section-body');
const toggleGroupsSectionButton = document.getElementById('toggle-groups-section');
const toggleChainsSectionButton = document.getElementById('toggle-chains-section');
const bulkAddOverlay = document.getElementById('bulk-add-overlay');
const bulkAddTargetEl = document.getElementById('bulk-add-target');
const bulkAddModeKeywordButton = document.getElementById('bulk-add-mode-keyword');
const bulkAddModeRegexButton = document.getElementById('bulk-add-mode-regex');
const bulkAddPatternInput = document.getElementById('bulk-add-pattern');
const bulkAddCaseInput = document.getElementById('bulk-add-case-insensitive');
const bulkAddPreviewEl = document.getElementById('bulk-add-preview');
const bulkAddPreviewSummaryEl = document.getElementById('bulk-add-preview-summary');
const bulkAddReplaceButton = document.getElementById('bulk-add-replace');
const bulkAddAppendButton = document.getElementById('bulk-add-append');
const bulkAddCancelButton = document.getElementById('bulk-add-cancel');
const bulkAddState = { kind: null, index: -1, mode: 'keyword', matchedTags: [] };
const NODES_UPDATED_KEY = 'sub2socks5:nodes-updated-at';
const GROUP_TEST_URL_PRESETS = [
  'https://www.gstatic.com/generate_204',
  'https://www.google.com/generate_204',
  'https://cp.cloudflare.com/generate_204'
];

let state = {
  subscriptionNodes: [],
  manualNodes: [],
  groups: [],
  chains: [],
  availableOutbounds: [],
  fallbackStates: {}
};

let nodeDelayState = {};
const expandedGroups = new Set();
const expandedChains = new Set();
const CHECK_BATCH_SIZE = 5;
const checkingNodeTags = new Set();
let groupsSectionCollapsed = false;
let chainsSectionCollapsed = false;

async function load() {
  const response = await fetch('/api/nodes');
  const data = await response.json();
  if (!response.ok) {
    throw new Error(data?.error?.message || '加载节点失败');
  }
  state = {
    ...data,
    chains: Array.isArray(data.chains) ? data.chains : []
  };
  render();
}

function render() {
  renderAvailableNodes();
  renderGroups();
  renderChains();
  renderSectionToggles();
}

function renderSectionToggles() {
  groupsSectionBodyEl.classList.toggle('is-hidden', groupsSectionCollapsed);
  chainsSectionBodyEl.classList.toggle('is-hidden', chainsSectionCollapsed);
  toggleGroupsSectionButton.textContent = groupsSectionCollapsed ? '展开' : '收起';
  toggleChainsSectionButton.textContent = chainsSectionCollapsed ? '展开' : '收起';
}

function renderAvailableNodes() {
  const visibleNodes = getSelectableNodes();
  availableNodeListEl.innerHTML = '';

  if (!visibleNodes.length) {
    availableNodeListEl.innerHTML = '<div class="timeline-item"><div class="title">暂无节点</div></div>';
    return;
  }

  for (const node of visibleNodes) {
    const delayState = nodeDelayState[node.tag];
    const isChecking = checkingNodeTags.has(node.tag) || delayState?.loading;
    const delayText = isChecking ? '测速中...' : delayState?.text || 'check';
    const card = document.createElement('div');
    card.className = 'node-pill node-pill-checkable';
    card.innerHTML = `
      <div class="node-pill-main">
        <div class="node-pill-title">${escapeHtml(node.tag || '')}</div>
        <div class="node-pill-tags">
          <span class="node-pill-tag">${escapeHtml(node.type || '')}</span>
          <span class="node-pill-tag is-source">${escapeHtml(sourceLabel(node.source))}</span>
        </div>
      </div>
      <button type="button" class="node-check-button ${isChecking ? 'is-loading' : ''}" data-check-node="${escapeHtmlAttr(node.tag)}" title="点击测速" ${isChecking ? 'disabled' : ''}>${escapeHtml(delayText)}</button>
    `;
    availableNodeListEl.appendChild(card);
  }
}

function renderGroups() {
  groupsEl.innerHTML = '';
  if (!state.groups.length) {
    groupsEl.innerHTML = '<div class="timeline-item"><div class="title">暂无节点组</div></div>';
    return;
  }

  const selectableNodes = getSelectableNodesWithoutChains();
  for (const [index, group] of state.groups.entries()) {
    groupsEl.appendChild(buildGroupPanel(index, group, selectableNodes));
  }
}

function buildGroupPanel(index, group, selectableNodes) {
  const fallbackState = state.fallbackStates?.[group.tag] || null;
  const expanded = expandedGroups.has(index);
  const selectedMembers = Array.isArray(group.members) ? group.members : [];
  const summaryCards = selectedMembers.map((memberTag) => {
    const node = selectableNodes.find((item) => item.tag === memberTag);
    return node ? renderNodePill(node) : '';
  }).join('');

  const statusHtml = group.strategy === 'fallback'
    ? `
      <div class="kv-grid">
        <div class="kv-item">
          <div class="key">当前活动节点</div>
          <div class="value">${escapeHtml(fallbackState?.current || group.members?.[0] || '')}</div>
        </div>
        <div class="kv-item">
          <div class="key">最近切换时间</div>
          <div class="value">${escapeHtml(fallbackState?.updatedAt || '')}</div>
        </div>
      </div>
    `
    : '';

  const item = document.createElement('div');
  item.className = 'timeline-item group-panel';
  item.innerHTML = `
    <div class="group-panel-header">
      <div>
        <div class="title">${escapeHtml(group.tag || `节点组 ${index + 1}`)}</div>
        <div class="node-pill-tags">
          <span class="node-pill-tag">${escapeHtml(group.strategy || 'urltest')}</span>
          <span class="node-pill-tag is-source">${selectedMembers.length} 个节点</span>
        </div>
      </div>
      <button type="button" class="group-toggle" data-toggle-group="${index}">${expanded ? '收起' : '展开'}</button>
    </div>
    <div class="node-pill-grid group-summary">
      ${summaryCards || '<div class="timeline-item"><div class="title">暂无成员</div></div>'}
    </div>
    <div class="group-panel-body ${expanded ? '' : 'is-hidden'}">
      <div class="form-grid">
        <label><span>tag</span><input data-kind="group" data-index="${index}" data-field="tag" value="${escapeHtmlAttr(group.tag || '')}" /></label>
        <label>
          <span>策略</span>
          <select data-kind="group" data-index="${index}" data-field="strategy">
            <option value="urltest" ${group.strategy === 'urltest' ? 'selected' : ''}>urltest</option>
            <option value="fallback" ${group.strategy === 'fallback' ? 'selected' : ''}>fallback</option>
          </select>
        </label>
        <label>
          <span>测试地址</span>
          <select data-kind="group-preset" data-index="${index}">
            ${buildGroupUrlPresetOptions(group.url)}
          </select>
        </label>
        <label><span>测试间隔</span><input data-kind="group" data-index="${index}" data-field="interval" value="${escapeHtmlAttr(group.interval || '10m')}" /></label>
        <label><span>超时毫秒</span><input data-kind="group" data-index="${index}" data-field="timeoutMs" type="number" value="${escapeHtmlAttr(String(group.timeoutMs || 5000))}" /></label>
        <label class="${GROUP_TEST_URL_PRESETS.includes(group.url) ? 'is-hidden' : ''}"><span>自定义测试地址</span><input data-kind="group" data-index="${index}" data-field="url" value="${escapeHtmlAttr(group.url || 'https://www.gstatic.com/generate_204')}" /></label>
      </div>
      ${statusHtml}
      <div class="member-selector">${renderGroupMembers(index, group, selectableNodes)}</div>
      <div class="section-heading-actions">
        <button type="button" data-bulk-add-group="${index}">按关键词/正则批量添加</button>
        <button type="button" data-remove-group="${index}">删除</button>
      </div>
    </div>
  `;
  return item;
}

function renderChains() {
  chainsEl.innerHTML = '';
  if (!state.chains.length) {
    chainsEl.innerHTML = '<div class="timeline-item"><div class="title">暂无链式代理</div></div>';
    return;
  }

  const selectableNodes = getSelectableNodesWithoutChains();
  for (const [index, chain] of state.chains.entries()) {
    const expanded = expandedChains.has(index);
    const selectedMembers = Array.isArray(chain.members) ? chain.members : [];
    const chainCards = selectedMembers.map((memberTag, memberIndex) => {
      const node = selectableNodes.find((item) => item.tag === memberTag);
      const card = node ? renderNodePill(node) : '';
      const arrow = memberIndex < selectedMembers.length - 1 ? '<div class="chain-arrow">→</div>' : '';
      return `<div class="chain-segment">${card}${arrow}</div>`;
    }).join('');

    const item = document.createElement('div');
    item.className = 'timeline-item group-panel';
    item.innerHTML = `
      <div class="group-panel-header">
        <div>
          <div class="title">${escapeHtml(chain.tag || `链式代理 ${index + 1}`)}</div>
          <div class="node-pill-tags">
            <span class="node-pill-tag">chain</span>
            <span class="node-pill-tag is-source">${selectedMembers.length} 个节点</span>
          </div>
        </div>
        <button type="button" class="group-toggle" data-toggle-chain="${index}">${expanded ? '收起' : '展开'}</button>
      </div>
      <div class="chain-summary">${chainCards || '<div class="timeline-item"><div class="title">暂无成员</div></div>'}</div>
      <div class="group-panel-body ${expanded ? '' : 'is-hidden'}">
        <div class="form-grid">
          <label><span>名称</span><input data-kind="chain" data-index="${index}" data-field="tag" value="${escapeHtmlAttr(chain.tag || '')}" /></label>
        </div>
        <div class="member-selector">${renderChainMembers(index, chain, selectableNodes)}</div>
        <div class="section-heading-actions">
          <button type="button" data-remove-chain="${index}">删除</button>
        </div>
      </div>
    `;
    chainsEl.appendChild(item);
  }
}

function renderGroupMembers(index, group, selectableNodes) {
  const selected = Array.isArray(group.members) ? group.members : [];
  const rows = selected.map((memberTag, memberIndex) => `
    <div class="member-row">
      <select data-group-member-select="${index}" data-member-index="${memberIndex}">
        ${buildMemberOptions(selectableNodes, selected, memberTag)}
      </select>
      <button type="button" class="member-remove" data-remove-member="${index}" data-member-index="${memberIndex}">删除</button>
    </div>
  `);
  const remaining = selectableNodes.filter((node) => !selected.includes(node.tag));
  rows.push(`<button type="button" class="member-add" data-add-member="${index}" ${remaining.length ? '' : 'disabled'}>+ 添加节点</button>`);
  return rows.join('');
}

function renderChainMembers(index, chain, selectableNodes) {
  const selected = Array.isArray(chain.members) ? chain.members : [];
  const rows = selected.map((memberTag, memberIndex) => `
    <div class="member-row">
      <select data-chain-member-select="${index}" data-member-index="${memberIndex}">
        ${buildMemberOptions(selectableNodes, selected, memberTag)}
      </select>
      <button type="button" class="member-remove" data-remove-chain-member="${index}" data-member-index="${memberIndex}">删除</button>
    </div>
  `);
  const remaining = selectableNodes.filter((node) => !selected.includes(node.tag));
  rows.push(`<button type="button" class="member-add" data-add-chain-member="${index}" ${remaining.length ? '' : 'disabled'}>+ 添加节点</button>`);
  return rows.join('');
}

function buildMemberOptions(selectableNodes, selectedTags, currentTag) {
  return selectableNodes
    .filter((node) => node.tag === currentTag || !selectedTags.includes(node.tag))
    .map((node) => `<option value="${escapeHtmlAttr(node.tag)}" ${node.tag === currentTag ? 'selected' : ''}>${escapeHtml(node.label || node.tag)}</option>`)
    .join('');
}

function buildGroupUrlPresetOptions(currentUrl) {
  const preset = GROUP_TEST_URL_PRESETS.includes(currentUrl) ? currentUrl : 'custom';
  return [
    ...GROUP_TEST_URL_PRESETS.map((url) => `<option value="${escapeHtmlAttr(url)}" ${preset === url ? 'selected' : ''}>${escapeHtml(url)}</option>`),
    `<option value="custom" ${preset === 'custom' ? 'selected' : ''}>自定义</option>`
  ].join('');
}

function getSelectableNodesWithoutChains() {
	return state.availableOutbounds.filter((item) => !['proxy', 'auto', 'block', 'direct'].includes(item.tag) && item.source !== 'chain');
}

function getSelectableNodes() {
	return state.availableOutbounds.filter((item) => !['proxy', 'auto', 'block', 'direct'].includes(item.tag));
}

function renderNodePill(node) {
  return `
    <div class="node-pill">
      <div class="node-pill-title">${escapeHtml(node.tag || '')}</div>
      <div class="node-pill-tags">
        <span class="node-pill-tag">${escapeHtml(node.type || '')}</span>
        <span class="node-pill-tag is-source">${escapeHtml(sourceLabel(node.source))}</span>
      </div>
    </div>
  `;
}

function sourceLabel(source) {
  if (source === 'subscription') return '订阅';
  if (source === 'manual') return '手动';
  if (source === 'group') return '节点组';
  if (source === 'chain') return '链式代理';
  if (source === 'builtin') return '内置';
  return source || '';
}

async function checkNode(tag) {
  if (checkingNodeTags.has(tag)) {
    return;
  }
  checkingNodeTags.add(tag);
  nodeDelayState[tag] = { loading: true, text: '测速中...' };
  renderAvailableNodes();
  try {
    const configResponse = await fetch('/api/config');
    const configData = await configResponse.json();
    if (!configResponse.ok) {
      throw new Error(configData?.error?.message || '读取运行状态失败');
    }
    const runtimeWasRunning = Boolean(configData?.runtime?.running);
    try {
      if (!runtimeWasRunning) {
        const startResponse = await fetch('/api/runtime/start', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: '{}'
        });
        const startData = await startResponse.json();
        if (!startResponse.ok) {
          throw new Error(startData?.error?.message || '启动 sing-box 失败');
        }
        await waitForRuntimeReady();
      }
      const response = await fetch('/api/nodes/check', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ tags: [tag] })
      });
      const data = await response.json();
      if (!response.ok) {
        throw new Error(data?.error?.message || '测速失败');
      }
      nodeDelayState[tag] = data.results?.[tag]?.ok
        ? {
            text: data.results[tag].text,
            checkedAt: data.results[tag].checkedAt,
            checkedTag: data.results[tag].checkedTag
          }
        : { text: '失败', error: data.results?.[tag]?.error || '测速失败' };
      setStatus(`节点 ${tag} 测速完成`, 'success');
    } finally {
      if (!runtimeWasRunning) {
        await fetch('/api/runtime/stop', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: '{}'
        });
      }
    }
  } catch (error) {
    nodeDelayState[tag] = { text: '失败', error: error.message };
    setStatus(error.message, 'error');
  }
  checkingNodeTags.delete(tag);
  renderAvailableNodes();
}

async function checkAllNodes() {
  const tags = getSelectableNodes().map((item) => item.tag);
  if (!tags.length) {
    setStatus('当前没有可测速节点', 'idle');
    return;
  }

  const configResponse = await fetch('/api/config');
  const configData = await configResponse.json();
  if (!configResponse.ok) {
    throw new Error(configData?.error?.message || '读取运行状态失败');
  }
  const runtimeWasRunning = Boolean(configData?.runtime?.running);

  setStatus('正在分批刷新全部节点测速...', 'loading');
  for (const tag of tags) {
    nodeDelayState[tag] = { loading: true, text: '测速中...' };
  }
  renderAvailableNodes();

  let cursor = 0;
  let hasError = false;
  try {
    if (!runtimeWasRunning) {
      const startResponse = await fetch('/api/runtime/start', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: '{}'
      });
      const startData = await startResponse.json();
      if (!startResponse.ok) {
        throw new Error(startData?.error?.message || '启动 sing-box 失败');
      }
      await waitForRuntimeReady();
    }

    const runOne = async () => {
      while (true) {
        const current = cursor;
        cursor += 1;
        if (current >= tags.length) {
          return;
        }
        const tag = tags[current];
        try {
          const response = await fetch('/api/nodes/check', {
            method: 'POST',
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ tags: [tag] })
          });
          const data = await response.json();
          if (!response.ok) {
            throw new Error(data?.error?.message || '批量测速失败');
          }
          const result = data.results?.[tag];
          nodeDelayState[tag] = result?.ok
            ? { text: result.text, checkedAt: result.checkedAt, checkedTag: result.checkedTag }
            : { text: '失败', error: result?.error || '测速失败' };
        } catch (error) {
          nodeDelayState[tag] = { text: '失败', error: error.message };
          hasError = true;
        }
        renderAvailableNodes();
      }
    };

    const workerCount = Math.min(CHECK_BATCH_SIZE, tags.length);
    await Promise.all(Array.from({ length: workerCount }, () => runOne()));
  } finally {
    if (!runtimeWasRunning) {
      await fetch('/api/runtime/stop', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: '{}'
      });
    }
  }

  if (hasError) {
    setStatus('部分节点测速失败，请重试', 'error');
    return;
  }

  setStatus('全部节点测速完成', 'success');
}

async function waitForRuntimeReady(timeoutMs = 8000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const response = await fetch('/api/config');
    const data = await response.json();
    if (response.ok && data?.runtime?.running) {
      await sleep(700);
      return;
    }
    await sleep(250);
  }
  throw new Error('sing-box 启动超时，请检查内核日志');
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
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

document.getElementById('back-home').addEventListener('click', () => {
  window.location.href = '/';
});

document.getElementById('open-node-editor').addEventListener('click', () => {
  window.location.href = '/nodes-edit.html';
});

document.getElementById('check-all-nodes').addEventListener('click', () => {
  checkAllNodes().catch((error) => setStatus(error.message, 'error'));
});

document.getElementById('add-group').addEventListener('click', () => {
  state.groups.push({ tag: '', strategy: 'urltest', url: 'https://www.gstatic.com/generate_204', interval: '10m', timeoutMs: 5000, members: [] });
  expandedGroups.add(state.groups.length - 1);
  renderGroups();
});

document.getElementById('add-chain').addEventListener('click', () => {
  state.chains.push({ tag: '', members: [] });
  expandedChains.add(state.chains.length - 1);
  renderChains();
});

toggleGroupsSectionButton.addEventListener('click', () => {
  groupsSectionCollapsed = !groupsSectionCollapsed;
  renderSectionToggles();
});

toggleChainsSectionButton.addEventListener('click', () => {
  chainsSectionCollapsed = !chainsSectionCollapsed;
  renderSectionToggles();
});

document.getElementById('save-nodes').addEventListener('click', async () => {
  try {
    const duplicateGroupTag = findDuplicateTag(state.groups);
    if (duplicateGroupTag) {
      throw new Error(`节点组 tag 重复：${duplicateGroupTag}`);
    }
    const duplicateChainTag = findDuplicateTag(state.chains);
    if (duplicateChainTag) {
      throw new Error(`链式代理 tag 重复：${duplicateChainTag}`);
    }
    const response = await fetch('/api/nodes', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        manualNodes: state.manualNodes,
        groups: state.groups,
        chains: state.chains,
        disabledSubscriptionTags: state.disabledSubscriptionTags || []
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

document.addEventListener('input', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLInputElement || target instanceof HTMLSelectElement)) return;

  if (target.dataset.kind === 'group') {
    const index = Number(target.dataset.index);
    const field = target.dataset.field;
    state.groups[index][field] = field === 'timeoutMs' ? Number(target.value || 0) : target.value;
  }

  if (target.dataset.kind === 'group-preset') {
    const index = Number(target.dataset.index);
    if (target.value !== 'custom') {
      state.groups[index].url = target.value;
    }
    renderGroups();
  }

  if (target.dataset.kind === 'chain') {
    const index = Number(target.dataset.index);
    const field = target.dataset.field;
    state.chains[index][field] = target.value;
  }

  if (target.dataset.groupMemberSelect) {
    const groupIndex = Number(target.dataset.groupMemberSelect);
    const memberIndex = Number(target.dataset.memberIndex);
    state.groups[groupIndex].members[memberIndex] = target.value;
    renderGroups();
  }

  if (target.dataset.chainMemberSelect) {
    const chainIndex = Number(target.dataset.chainMemberSelect);
    const memberIndex = Number(target.dataset.memberIndex);
    state.chains[chainIndex].members[memberIndex] = target.value;
    renderChains();
  }
});

document.addEventListener('click', (event) => {
  const target = event.target;
  if (!(target instanceof HTMLElement)) return;

  if (target.dataset.toggleGroup) {
    const groupIndex = Number(target.dataset.toggleGroup);
    expandedGroups.has(groupIndex) ? expandedGroups.delete(groupIndex) : expandedGroups.add(groupIndex);
    renderGroups();
  }

  if (target.dataset.toggleChain) {
    const chainIndex = Number(target.dataset.toggleChain);
    expandedChains.has(chainIndex) ? expandedChains.delete(chainIndex) : expandedChains.add(chainIndex);
    renderChains();
  }

  if (target.dataset.removeGroup) {
    const index = Number(target.dataset.removeGroup);
    state.groups.splice(index, 1);
    renderGroups();
  }

  if (target.dataset.removeChain) {
    const index = Number(target.dataset.removeChain);
    state.chains.splice(index, 1);
    renderChains();
  }

  if (target.dataset.addMember) {
    const groupIndex = Number(target.dataset.addMember);
    const selectableNodes = getSelectableNodesWithoutChains();
    const selected = new Set(state.groups[groupIndex].members || []);
    const nextNode = selectableNodes.find((node) => !selected.has(node.tag));
    if (nextNode) {
      state.groups[groupIndex].members.push(nextNode.tag);
      renderGroups();
    }
  }

  if (target.dataset.addChainMember) {
    const chainIndex = Number(target.dataset.addChainMember);
    const selectableNodes = getSelectableNodesWithoutChains();
    const selected = new Set(state.chains[chainIndex].members || []);
    const nextNode = selectableNodes.find((node) => !selected.has(node.tag));
    if (nextNode) {
      state.chains[chainIndex].members.push(nextNode.tag);
      renderChains();
    }
  }

  if (target.dataset.removeMember) {
    const groupIndex = Number(target.dataset.removeMember);
    const memberIndex = Number(target.dataset.memberIndex);
    state.groups[groupIndex].members.splice(memberIndex, 1);
    renderGroups();
  }

  if (target.dataset.removeChainMember) {
    const chainIndex = Number(target.dataset.removeChainMember);
    const memberIndex = Number(target.dataset.memberIndex);
    state.chains[chainIndex].members.splice(memberIndex, 1);
    renderChains();
  }

  if (target.dataset.bulkAddGroup) {
    const groupIndex = Number(target.dataset.bulkAddGroup);
    openBulkAddOverlay('group', groupIndex);
    return;
  }

  if (target.dataset.checkNode) {
    checkNode(target.dataset.checkNode).catch((error) => setStatus(error.message, 'error'));
  }
});

function getBulkAddTargetMembers() {
  if (bulkAddState.kind === 'group') {
    const group = state.groups?.[bulkAddState.index];
    return Array.isArray(group?.members) ? group.members : [];
  }
  return [];
}

function setBulkAddTargetMembers(tags) {
  if (bulkAddState.kind === 'group') {
    const group = state.groups?.[bulkAddState.index];
    if (group) group.members = tags;
  }
}

function openBulkAddOverlay(kind, index) {
  bulkAddState.kind = kind;
  bulkAddState.index = index;
  bulkAddState.mode = 'keyword';
  bulkAddState.matchedTags = [];
  if (bulkAddPatternInput) bulkAddPatternInput.value = '';
  if (bulkAddCaseInput) bulkAddCaseInput.checked = true;
  syncBulkAddModeButtons();
  if (bulkAddTargetEl && kind === 'group') {
    const group = state.groups?.[index];
    bulkAddTargetEl.textContent = `目标节点组：${group?.tag || `节点组 ${index + 1}`}（当前 ${(group?.members || []).length} 个成员）`;
  }
  bulkAddOverlay?.classList.remove('is-hidden');
  bulkAddOverlay?.setAttribute('aria-hidden', 'false');
  renderBulkAddPreview();
  bulkAddPatternInput?.focus();
}

function hideBulkAddOverlay() {
  bulkAddOverlay?.classList.add('is-hidden');
  bulkAddOverlay?.setAttribute('aria-hidden', 'true');
  bulkAddState.kind = null;
  bulkAddState.index = -1;
  bulkAddState.matchedTags = [];
}

function syncBulkAddModeButtons() {
  bulkAddModeKeywordButton?.classList.toggle('is-active', bulkAddState.mode === 'keyword');
  bulkAddModeRegexButton?.classList.toggle('is-active', bulkAddState.mode === 'regex');
  if (bulkAddPatternInput) {
    bulkAddPatternInput.placeholder = bulkAddState.mode === 'regex'
      ? '例：(HK|香港|Hong\\s*Kong)'
      : '例：香港 或 HK,新加坡（逗号分隔多个关键词）';
  }
}

function computeBulkMatchedTags() {
  const raw = (bulkAddPatternInput?.value || '').trim();
  if (!raw) return { tags: [], error: '' };
  const caseInsensitive = !!bulkAddCaseInput?.checked;
  const candidates = getSelectableNodesWithoutChains();
  if (bulkAddState.mode === 'regex') {
    // 兼容 Perl/PCRE 风格内联标志 (?i) (?im) (?ims)；JavaScript RegExp 不支持，需要转成 flags 参数。
    let pattern = raw;
    let flags = caseInsensitive ? 'i' : '';
    const inlineFlag = pattern.match(/^\(\?([imsu]+)\)/);
    if (inlineFlag) {
      pattern = pattern.slice(inlineFlag[0].length);
      for (const ch of inlineFlag[1]) if (!flags.includes(ch)) flags += ch;
    }
    let regex;
    try {
      regex = new RegExp(pattern, flags);
    } catch (err) {
      let hint = '';
      if (/^\(\?[a-zA-Z-]/.test(raw)) {
        hint = '（提示：JavaScript 正则不支持 (?i) 等内联标志，请勾选「忽略大小写」并删掉 (?i)；分组请用 (?:...)）';
      }
      return { tags: [], error: `正则错误：${err.message}${hint}` };
    }
    const tags = candidates.filter((n) => regex.test(n.tag || '')).map((n) => n.tag);
    return { tags, error: '' };
  }
  const keywords = raw.split(/[,，]/).map((s) => s.trim()).filter(Boolean);
  if (!keywords.length) return { tags: [], error: '' };
  const tags = candidates.filter((n) => {
    const tag = caseInsensitive ? (n.tag || '').toLowerCase() : (n.tag || '');
    return keywords.some((kw) => tag.includes(caseInsensitive ? kw.toLowerCase() : kw));
  }).map((n) => n.tag);
  return { tags, error: '' };
}

function renderBulkAddPreview() {
  if (!bulkAddPreviewEl || !bulkAddPreviewSummaryEl) return;
  const { tags, error } = computeBulkMatchedTags();
  bulkAddState.matchedTags = tags;
  if (error) {
    bulkAddPreviewSummaryEl.textContent = error;
    bulkAddPreviewEl.innerHTML = '';
    setBulkAddActionsEnabled(false);
    return;
  }
  if (!tags.length) {
    bulkAddPreviewSummaryEl.textContent = (bulkAddPatternInput?.value || '').trim()
      ? '没有匹配到节点'
      : '输入表达式以预览匹配结果';
    bulkAddPreviewEl.innerHTML = '';
    setBulkAddActionsEnabled(false);
    return;
  }
  bulkAddPreviewSummaryEl.textContent = `匹配到 ${tags.length} 个节点`;
  const candidates = getSelectableNodesWithoutChains();
  const byTag = new Map(candidates.map((n) => [n.tag, n]));
  bulkAddPreviewEl.innerHTML = tags.map((tag) => {
    const node = byTag.get(tag);
    return node ? renderNodePill(node) : '';
  }).join('');
  setBulkAddActionsEnabled(true);
}

function setBulkAddActionsEnabled(enabled) {
  if (bulkAddReplaceButton) bulkAddReplaceButton.disabled = !enabled;
  if (bulkAddAppendButton) bulkAddAppendButton.disabled = !enabled;
}

function applyBulkAdd(mode) {
  if (!bulkAddState.matchedTags.length) return;
  const existing = getBulkAddTargetMembers();
  let next;
  if (mode === 'replace') {
    next = [...bulkAddState.matchedTags];
  } else {
    const seen = new Set(existing);
    next = [...existing];
    for (const tag of bulkAddState.matchedTags) {
      if (!seen.has(tag)) {
        seen.add(tag);
        next.push(tag);
      }
    }
  }
  setBulkAddTargetMembers(next);
  setStatus(`已${mode === 'replace' ? '替换' : '追加'} ${bulkAddState.matchedTags.length} 个节点到当前节点组`, 'success');
  hideBulkAddOverlay();
  renderGroups();
}

bulkAddModeKeywordButton?.addEventListener('click', () => {
  bulkAddState.mode = 'keyword';
  syncBulkAddModeButtons();
  renderBulkAddPreview();
});
bulkAddModeRegexButton?.addEventListener('click', () => {
  bulkAddState.mode = 'regex';
  syncBulkAddModeButtons();
  renderBulkAddPreview();
});
bulkAddPatternInput?.addEventListener('input', () => renderBulkAddPreview());
bulkAddCaseInput?.addEventListener('change', () => renderBulkAddPreview());
bulkAddReplaceButton?.addEventListener('click', () => applyBulkAdd('replace'));
bulkAddAppendButton?.addEventListener('click', () => applyBulkAdd('append'));
bulkAddCancelButton?.addEventListener('click', () => hideBulkAddOverlay());
bulkAddOverlay?.addEventListener('click', (event) => {
  if (event.target === bulkAddOverlay) hideBulkAddOverlay();
});

load().catch((error) => setStatus(error.message, 'error'));

function findDuplicateTag(items) {
  const seen = new Set();
  for (const item of items || []) {
    const tag = String(item?.tag || '').trim();
    if (!tag) continue;
    if (seen.has(tag)) return tag;
    seen.add(tag);
  }
  return '';
}
