const form = document.getElementById('login-form');
const tokenInput = document.getElementById('login-token');
const errorEl = document.getElementById('login-error');
const submitBtn = document.getElementById('login-submit');

function showError(message) {
  if (!errorEl) return;
  errorEl.textContent = message;
  errorEl.classList.remove('is-hidden');
}

function hideError() {
  errorEl?.classList.add('is-hidden');
  if (errorEl) errorEl.textContent = '';
}

function getNextUrl() {
  const params = new URLSearchParams(window.location.search);
  const next = params.get('next');
  if (!next || !next.startsWith('/') || next.startsWith('//')) return '/';
  return next;
}

form?.addEventListener('submit', async (event) => {
  event.preventDefault();
  hideError();
  const token = (tokenInput?.value || '').trim();
  if (!token) {
    showError('请输入 Token');
    return;
  }
  if (submitBtn) submitBtn.disabled = true;
  try {
    const response = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token })
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok || !data.authenticated) {
      throw new Error(data?.error?.message || '登录失败');
    }
    window.location.replace(getNextUrl());
  } catch (err) {
    showError(err.message || '登录失败');
    if (submitBtn) submitBtn.disabled = false;
    tokenInput?.focus();
    tokenInput?.select();
  }
});

// 已登录直接跳走
fetch('/api/auth/status')
  .then((r) => r.json())
  .then((d) => {
    if (d?.enabled === false || d?.authenticated) {
      window.location.replace(getNextUrl());
    }
  })
  .catch(() => {});

tokenInput?.focus();
