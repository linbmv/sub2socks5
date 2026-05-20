// 通用前端工具函数模块
// 在所有页面 JS 入口通过 <script type="module" src="..."> 引入

/**
 * 安全的 HTML 转义。使用 textContent 实现，避免漏字符。
 */
export function escapeHtml(value) {
  if (value === null || value === undefined) return '';
  const div = document.createElement('div');
  div.textContent = String(value);
  return div.innerHTML;
}

/**
 * HTML 属性值转义。在 escapeHtml 基础上额外转双引号。
 */
export function escapeHtmlAttr(value) {
  return escapeHtml(value).replace(/"/g, '&quot;');
}

/**
 * 统一的状态栏更新。kind: idle | success | warning | error | info
 */
export function setStatus(el, message, kind = 'idle') {
  if (!el) return;
  el.textContent = message;
  el.className = `status-bar is-${kind}`;
}

/**
 * 统一的 toast 提示。容器需在调用页面预先存在 (#toast)。
 */
export function showToast(message, success = true) {
  const toast = document.getElementById('toast');
  if (!toast) {
    console[success ? 'log' : 'warn'](message);
    return;
  }
  toast.textContent = message;
  toast.className = `toast is-${success ? 'success' : 'error'}`;
  toast.style.opacity = '1';
  clearTimeout(toast._hideTimer);
  toast._hideTimer = setTimeout(() => {
    toast.style.opacity = '0';
  }, 2200);
}

/**
 * 统一的 fetch JSON 封装。失败时抛出含可读 message 的 Error。
 */
export async function api(url, options = {}) {
  const opts = {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {})
    }
  };
  if (opts.body && typeof opts.body !== 'string') {
    opts.body = JSON.stringify(opts.body);
  }
  const response = await fetch(url, opts);
  let data = null;
  try {
    data = await response.json();
  } catch (_) {
    /* 非 JSON 返回 */
  }
  if (!response.ok) {
    const msg = data?.error?.message || `HTTP ${response.status}`;
    const err = new Error(msg);
    err.status = response.status;
    err.data = data;
    throw err;
  }
  return data;
}

export const post = (url, body) => api(url, { method: 'POST', body });
export const get = (url) => api(url);
export const patch = (url, body) => api(url, { method: 'PATCH', body });
export const put = (url, body) => api(url, { method: 'PUT', body });
export const del = (url) => api(url, { method: 'DELETE' });

/**
 * 弹层焦点陷阱。打开弹层时调用，返回 cleanup 函数恢复焦点。
 * 用法: const release = trapFocus(overlayEl);
 *      // 关闭时: release();
 */
export function trapFocus(container) {
  if (!container) return () => {};
  const previouslyFocused = document.activeElement;
  const focusables = () =>
    Array.from(
      container.querySelectorAll(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
      )
    ).filter((el) => el.offsetParent !== null);

  const handleKeyDown = (event) => {
    if (event.key !== 'Tab') return;
    const items = focusables();
    if (items.length === 0) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  container.addEventListener('keydown', handleKeyDown);
  const items = focusables();
  if (items[0]) items[0].focus();

  return () => {
    container.removeEventListener('keydown', handleKeyDown);
    if (previouslyFocused && typeof previouslyFocused.focus === 'function') {
      previouslyFocused.focus();
    }
  };
}

/**
 * 节流函数。
 */
export function throttle(fn, wait) {
  let last = 0;
  let timer = null;
  return function (...args) {
    const now = Date.now();
    const remaining = wait - (now - last);
    if (remaining <= 0) {
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      last = now;
      fn.apply(this, args);
    } else if (!timer) {
      timer = setTimeout(() => {
        last = Date.now();
        timer = null;
        fn.apply(this, args);
      }, remaining);
    }
  };
}

/**
 * 防抖函数。
 */
export function debounce(fn, wait) {
  let timer = null;
  return function (...args) {
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => {
      timer = null;
      fn.apply(this, args);
    }, wait);
  };
}
