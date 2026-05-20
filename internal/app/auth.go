package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// withAuth 是 HTTP 中间件，校验请求附带的会话 ID。
// 仅在 SUB2SOCKS5_AUTH_TOKEN 已配置时生效，否则直通。
func (a *App) withAuth(next http.Handler) http.Handler {
	if len(a.tokenHash) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAuthPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		sessionID := ""
		if c, err := r.Cookie("sub2socks5_session"); err == nil {
			sessionID = c.Value
		}
		if sessionID == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				sessionID = strings.TrimPrefix(h, "Bearer ")
			}
		}
		a.mu.Lock()
		issuedAt, valid := a.sessions[sessionID]
		a.mu.Unlock()
		if !valid || time.Since(issuedAt) > 24*time.Hour {
			if isBrowserNavigation(r) {
				nextPath := r.URL.Path
				if r.URL.RawQuery != "" {
					nextPath += "?" + r.URL.RawQuery
				}
				if !isSafeNextPath(nextPath) {
					nextPath = "/"
				}
				http.Redirect(w, r, "/login?next="+url.QueryEscape(nextPath), http.StatusSeeOther)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="sub2socks5"`)
			w.Header().Set("content-type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Unauthorized", "status": 401}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAuthPublicPath 列出无需鉴权即可访问的路径。
func isAuthPublicPath(p string) bool {
	switch p {
	case "/login", "/login.html", "/login.js", "/style.css", "/shared.js",
		"/api/auth/status", "/api/auth/login", "/api/auth/logout":
		return true
	}
	return false
}

// isSafeNextPath 校验登录后跳转路径是否为同站绝对路径，避免 open redirect。
func isSafeNextPath(p string) bool {
	if p == "" || !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return false
	}
	if p == "/login" || strings.HasPrefix(p, "/login?") || p == "/login.html" {
		return false
	}
	return true
}

// isBrowserNavigation 判断是否为浏览器页面导航请求（决定 401 改用 302 跳登录页）。
func isBrowserNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// setAuthCookie 写入会话 cookie，根据请求协议自动决定 Secure 标志。
func (a *App) setAuthCookie(w http.ResponseWriter, r *http.Request, sessionID string) {
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "sub2socks5_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400,
	})
}

// handleAuthStatus 返回鉴权是否启用以及当前会话是否有效。
func (a *App) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	enabled := len(a.tokenHash) > 0
	authed := false
	if enabled {
		sessionID := ""
		if c, err := r.Cookie("sub2socks5_session"); err == nil {
			sessionID = c.Value
		}
		if sessionID == "" {
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				sessionID = strings.TrimPrefix(h, "Bearer ")
			}
		}
		a.mu.RLock()
		issuedAt, valid := a.sessions[sessionID]
		a.mu.RUnlock()
		authed = valid && time.Since(issuedAt) <= 24*time.Hour
	}
	ok(w, map[string]any{"enabled": enabled, "authenticated": authed || !enabled})
}

// handleAuthLogin 处理登录请求：速率限制 + sha256 等长比对 + 生成会话。
func (a *App) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if len(a.tokenHash) == 0 {
		fail(w, http.StatusBadRequest, "鉴权未启用")
		return
	}
	clientIP := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		clientIP = strings.Split(fwd, ",")[0]
	}
	a.mu.Lock()
	now := time.Now()
	attempts := a.loginAttempts[clientIP]
	recent := []time.Time{}
	for _, t := range attempts {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		a.mu.Unlock()
		fail(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	recent = append(recent, now)
	a.loginAttempts[clientIP] = recent
	a.mu.Unlock()

	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	providedHash := sha256.Sum256([]byte(body.Token))
	if subtle.ConstantTimeCompare(providedHash[:], a.tokenHash) != 1 {
		fail(w, http.StatusUnauthorized, "Token 错误")
		return
	}
	sessionID := generateSessionID()
	a.mu.Lock()
	a.sessions[sessionID] = now
	a.mu.Unlock()
	a.setAuthCookie(w, r, sessionID)
	ok(w, map[string]any{"authenticated": true})
}

// handleAuthLogout 失效会话并清除 cookie。
func (a *App) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	sessionID := ""
	if c, err := r.Cookie("sub2socks5_session"); err == nil {
		sessionID = c.Value
	}
	if sessionID != "" {
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "sub2socks5_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	ok(w, map[string]any{"authenticated": false})
}

// generateSessionID 生成 32 字节随机 base64url 编码会话 ID。
func generateSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
