const form = document.getElementById('login-form');
const usernameInput = document.getElementById('login-username');
const passwordInput = document.getElementById('login-password');
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
  if (!next) return '/';
  if (!next.startsWith('/')) return '/';
  if (next.startsWith('//')) return '/';
  if (next.includes('\\')) return '/';
  if (next === '/login' || next.startsWith('/login?') || next === '/login.html') return '/';
  return next;
}

form?.addEventListener('submit', async (event) => {
  event.preventDefault();
  hideError();
  const username = (usernameInput?.value || '').trim();
  const password = passwordInput?.value || '';
  if (!username) {
    showError('请输入用户名');
    usernameInput?.focus();
    return;
  }
  if (!password) {
    showError('请输入密码');
    passwordInput?.focus();
    return;
  }
  if (submitBtn) submitBtn.disabled = true;
  try {
    const response = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password })
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok || !data.authenticated) {
      throw new Error(data?.error?.message || '登录失败');
    }
    window.location.replace(getNextUrl());
  } catch (err) {
    showError(err.message || '登录失败');
    if (submitBtn) submitBtn.disabled = false;
    passwordInput?.focus();
    passwordInput?.select();
  }
});

fetch('/api/auth/status')
  .then((r) => r.json())
  .then((d) => {
    if (d?.enabled === false || d?.authenticated) {
      window.location.replace(getNextUrl());
    }
  })
  .catch(() => {});

usernameInput?.focus();
