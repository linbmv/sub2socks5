package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type App struct {
	mu                  sync.RWMutex
	cfg                 map[string]any
	subState            map[string]any
	runtimeInfo         map[string]any
	proc                *exec.Cmd
	manualStopRequested bool
	autoRestartAttempts int
	plannedKernel       map[string]any
	releaseList         []any
	downloadState       map[string]any
	rootDir             string
	dataDir             string
	runtimeDir          string
	binDir              string
	publicDir           string
	staticFS            fs.FS
	autoUpdateLastRun   map[string]time.Time
	sessions            map[string]time.Time
	loginAttempts       map[string][]time.Time
	userHash            []byte
	passHash            []byte
}

func Run() error {
	return RunWithStaticFS(nil)
}

func RunWithStaticFS(staticFS fs.FS) error {
	rootDir := strings.TrimSpace(os.Getenv("SUB2SOCKS5_ROOT"))
	if rootDir == "" {
		cwd, err := os.Getwd()
		must(err)
		rootDir = cwd
	}
	app := &App{
		rootDir:    rootDir,
		dataDir:    filepath.Join(rootDir, "internal", "data"),
		runtimeDir: filepath.Join(rootDir, "internal", "runtime"),
		binDir:     filepath.Join(rootDir, "internal", "bin"),
		publicDir:  filepath.Join(rootDir, "internal", "public"),
		staticFS:   staticFS,
		runtimeInfo: map[string]any{
			"state":   "stopped",
			"running": false,
			"logs":    []string{},
		},
		plannedKernel:     nil,
		releaseList:       []any{},
		downloadState:     map[string]any{"active": false, "steps": []any{}, "progress": nil, "updatedAt": nil},
		autoUpdateLastRun: map[string]time.Time{},
		sessions:          map[string]time.Time{},
		loginAttempts:     map[string][]time.Time{},
	}
	pass := os.Getenv("SUB2SOCKS5_PASSWORD")
	if strings.TrimSpace(pass) != "" {
		user := os.Getenv("SUB2SOCKS5_USERNAME")
		if strings.TrimSpace(user) == "" {
			user = "admin"
		}
		uh := sha256.Sum256([]byte(user))
		ph := sha256.Sum256([]byte(pass))
		app.userHash = uh[:]
		app.passHash = ph[:]
	}
	must(os.MkdirAll(app.dataDir, 0o755))
	must(os.MkdirAll(app.runtimeDir, 0o755))
	must(os.MkdirAll(app.binDir, 0o755))
	must(app.loadOrInit())

	if getBool(getMap(app.cfg, "app"), "autoStart", false) {
		app.mu.Lock()
		if err := app.startRuntimeLocked(); err != nil {
			app.appendRuntimeLog("auto start failed: " + err.Error())
		}
		app.mu.Unlock()
	}

	go app.runSubscriptionAutoUpdateScheduler()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/config", app.handleConfig)
	mux.HandleFunc("/api/config/patch", app.handleConfigPatch)
	mux.HandleFunc("/api/services", app.handleServicesList)
	mux.HandleFunc("/api/services/", app.handleServicesItem)
	mux.HandleFunc("/api/runtime/state", app.handleRuntimeState)
	mux.HandleFunc("/api/diagnostics", app.handleDiagnostics)
	mux.HandleFunc("/api/subscription/refresh", app.handleSubscriptionRefresh)
	mux.HandleFunc("/api/subscription/preview", app.handleSubscriptionPreview)
	mux.HandleFunc("/api/nodes", app.handleNodes)
	mux.HandleFunc("/api/nodes/import", app.handleNodeImport)
	mux.HandleFunc("/api/nodes/check", app.handleNodesCheck)
	mux.HandleFunc("/api/nodes/egress", app.handleNodesEgress)
	mux.HandleFunc("/api/ports/next", app.handleNextPort)
	mux.HandleFunc("/api/runtime/generate", app.handleRuntimeGenerate)
	mux.HandleFunc("/api/runtime/start", app.handleRuntimeStart)
	mux.HandleFunc("/api/runtime/stop", app.handleRuntimeStop)
	mux.HandleFunc("/api/runtime/logs", app.handleRuntimeLogs)
	mux.HandleFunc("/api/runtime/generated", app.handleRuntimeGenerated)
	mux.HandleFunc("/api/kernel/architecture", app.handleKernelArch)
	mux.HandleFunc("/api/kernel/status", app.handleKernelStatus)
	mux.HandleFunc("/api/kernel/releases", app.handleKernelReleases)
	mux.HandleFunc("/api/kernel/releases/update", app.handleKernelReleasesUpdate)
	mux.HandleFunc("/api/kernel/plan", app.handleKernelPlan)
	mux.HandleFunc("/api/kernel/download", app.handleKernelDownload)
	mux.HandleFunc("/api/auth/status", app.handleAuthStatus)
	mux.HandleFunc("/api/auth/login", app.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", app.handleAuthLogout)
	mux.HandleFunc("/", app.handleStatic)

	host := getString(getMap(app.cfg, "app"), "host", "0.0.0.0")
	if env := strings.TrimSpace(os.Getenv("SUB2SOCKS5_HOST")); env != "" {
		host = env
	}
	port := getInt(getMap(app.cfg, "app"), "port", 18080)
	if env := strings.TrimSpace(os.Getenv("SUB2SOCKS5_PORT")); env != "" {
		n, err := strconv.Atoi(env)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid SUB2SOCKS5_PORT: %q", env)
		}
		port = n
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	fmt.Printf("Web UI listening on http://%s\n", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.withAuth(withCORS(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	idleConnsClosed := make(chan struct{})
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		sig := <-sigChan
		fmt.Printf("\nReceived signal %v, shutting down gracefully...\n", sig)
		// 1. 停止 sing-box 子进程
		app.mu.Lock()
		app.manualStopRequested = true
		if app.proc != nil && app.proc.Process != nil {
			_ = app.proc.Process.Kill()
		}
		app.mu.Unlock()
		// 2. 关闭 HTTP 服务器（30s 超时给现有请求收尾）
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP shutdown error: %v\n", err)
		}
		close(idleConnsClosed)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	<-idleConnsClosed
	return nil
}

func (a *App) runSubscriptionAutoUpdateScheduler() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		a.mu.Lock()
		a.runSubscriptionAutoUpdateLocked(time.Now())
		a.mu.Unlock()
	}
}

func (a *App) runSubscriptionAutoUpdateLocked(now time.Time) {
	subCfg := getMap(a.cfg, "subscription")
	auto := getMap(subCfg, "autoUpdate")
	scope := strings.TrimSpace(mustStr(auto["scope"]))
	if scope == "" || scope == "off" {
		return
	}

	if scope == "simultaneous" {
		if !shouldRunAutoUpdate(now, a.autoUpdateLastRun["simultaneous"], auto) {
			return
		}
		if err := a.refreshSubscriptionLocked("auto-update(simultaneous)"); err != nil {
			a.appendRuntimeLog("auto update failed: " + err.Error())
			return
		}
		a.autoUpdateLastRun["simultaneous"] = now
		a.persistAutoUpdateLastRunLocked()
		a.appendRuntimeLog("auto update completed (simultaneous)")
		return
	}

	if scope == "independent" {
		urls := normalizeSubscriptionURLs(subCfg)
		items := getSlice(auto, "items")
		if len(urls) == 0 || len(items) == 0 {
			return
		}
		updated := false
		for idx := 0; idx < len(urls) && idx < len(items); idx += 1 {
			item, ok := items[idx].(map[string]any)
			if !ok {
				continue
			}
			key := fmt.Sprintf("independent:%d", idx)
			if !shouldRunAutoUpdate(now, a.autoUpdateLastRun[key], item) {
				continue
			}

			localSub := cloneMap(subCfg)
			localSub["url"] = urls[idx]
			localSub["urls"] = []any{urls[idx]}
			st := fetchSubscription(localSub)
			st["updatedAt"] = now.Format(time.RFC3339)
			a.subState = mergeSubscriptionState(a.subState, st)
			a.autoUpdateLastRun[key] = now
			updated = true
			a.appendRuntimeLog(fmt.Sprintf("auto update completed (independent #%d)", idx+1))
		}
		if updated {
			_ = writeJSON(filepath.Join(a.dataDir, "subscription-state.json"), a.subState)
			a.persistAutoUpdateLastRunLocked()
		}
	}
}

func shouldRunAutoUpdate(now, last time.Time, cfg map[string]any) bool {
	mode := strings.TrimSpace(mustStr(cfg["mode"]))
	if mode == "" {
		mode = "interval"
	}
	if mode == "interval" {
		minutes := int(toFloat(cfg["intervalMinutes"]))
		if minutes <= 0 {
			minutes = 60
		}
		if last.IsZero() {
			return true
		}
		return now.Sub(last) >= time.Duration(minutes)*time.Minute
	}

	if mode == "schedule" {
		timeText := strings.TrimSpace(mustStr(cfg["time"]))
		if timeText == "" {
			timeText = "03:00"
		}
		parts := strings.Split(timeText, ":")
		if len(parts) != 2 {
			return false
		}
		hh, err1 := strconv.Atoi(parts[0])
		mm, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
			return false
		}
		target := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if now.Before(target) {
			return false
		}

		dayMode := strings.TrimSpace(mustStr(cfg["dayMode"]))
		if dayMode == "" {
			dayMode = "daily"
		}
		if last.IsZero() {
			return true
		}
		lastDay := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, last.Location())
		nowDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		days := int(nowDay.Sub(lastDay).Hours() / 24)
		switch dayMode {
		case "daily":
			return days >= 1
		case "every3days":
			return days >= 3
		case "weekly":
			return days >= 7
		default:
			return days >= 1
		}
	}

	return false
}

func normalizeSubscriptionURLs(sub map[string]any) []string {
	urls := []string{}
	for _, v := range getSlice(sub, "urls") {
		s := strings.TrimSpace(mustStr(v))
		if s != "" {
			urls = append(urls, s)
		}
	}
	if len(urls) == 0 {
		if s := strings.TrimSpace(getString(sub, "url", "")); s != "" {
			urls = append(urls, s)
		}
	}
	return urls
}

func mergeSubscriptionState(base, incoming map[string]any) map[string]any {
	out := map[string]any{"raw": "", "nodes": []any{}, "warnings": []any{}, "updatedAt": nil}
	if base != nil {
		out = cloneMap(base)
	}
	nodes := map[string]map[string]any{}
	appendNodes := func(items []any) {
		for _, n := range items {
			m, ok := n.(map[string]any)
			if !ok {
				continue
			}
			tag := strings.TrimSpace(mustStr(m["tag"]))
			if tag == "" {
				continue
			}
			nodes[tag] = m
		}
	}
	appendNodes(getSlice(out, "nodes"))
	appendNodes(getSlice(incoming, "nodes"))
	mergedNodes := make([]any, 0, len(nodes))
	for _, n := range nodes {
		mergedNodes = append(mergedNodes, n)
	}
	sort.SliceStable(mergedNodes, func(i, j int) bool {
		mi, _ := mergedNodes[i].(map[string]any)
		mj, _ := mergedNodes[j].(map[string]any)
		return mustStr(mi["tag"]) < mustStr(mj["tag"])
	})
	out["nodes"] = mergedNodes

	warns := []any{}
	warns = append(warns, getSlice(out, "warnings")...)
	warns = append(warns, getSlice(incoming, "warnings")...)
	out["warnings"] = warns
	out["updatedAt"] = incoming["updatedAt"]
	out["raw"] = incoming["raw"]
	return out
}

func (a *App) refreshSubscriptionLocked(reason string) error {
	subCfg := getMap(a.cfg, "subscription")
	st := fetchSubscription(subCfg)
	st["updatedAt"] = time.Now().Format(time.RFC3339)
	a.subState = st
	if err := writeJSON(filepath.Join(a.dataDir, "subscription-state.json"), st); err != nil {
		return err
	}
	a.appendRuntimeLog("subscription refreshed: " + reason)
	return nil
}

func (a *App) loadOrInit() error {
	cfgPath := filepath.Join(a.dataDir, "app-config.json")
	subPath := filepath.Join(a.dataDir, "subscription-state.json")
	archPath := filepath.Join(a.dataDir, "architecture-info.json")
	plannedPath := filepath.Join(a.dataDir, "planned-kernel-info.json")
	releasePath := filepath.Join(a.dataDir, "release-list.json")
	generatedPath := filepath.Join(a.runtimeDir, "sing-box.json")

	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		a.cfg = defaultConfig()
		if err := writeJSON(cfgPath, a.cfg); err != nil {
			return err
		}
	} else {
		var cfg map[string]any
		if err := readJSON(cfgPath, &cfg); err != nil {
			return err
		}
		a.cfg = mergeMap(defaultConfig(), cfg)
	}

	if _, err := os.Stat(subPath); errors.Is(err, os.ErrNotExist) {
		a.subState = map[string]any{"raw": "", "nodes": []any{}, "warnings": []any{}, "updatedAt": nil}
		if err := writeJSON(subPath, a.subState); err != nil {
			return err
		}
	} else {
		var st map[string]any
		if err := readJSON(subPath, &st); err != nil {
			return err
		}
		a.subState = st
	}

	if _, err := os.Stat(archPath); errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(archPath, detectPlatform()); err != nil {
			return err
		}
	}

	if _, err := os.Stat(plannedPath); errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(plannedPath, nil); err != nil {
			return err
		}
	} else {
		var planned map[string]any
		if err := readJSON(plannedPath, &planned); err == nil {
			a.plannedKernel = planned
		}
	}

	if _, err := os.Stat(releasePath); errors.Is(err, os.ErrNotExist) {
		if err := writeJSON(releasePath, []any{}); err != nil {
			return err
		}
	} else {
		var releases []any
		if err := readJSON(releasePath, &releases); err == nil {
			a.releaseList = releases
		}
	}

	if _, err := os.Stat(generatedPath); errors.Is(err, os.ErrNotExist) {
		generated := buildSingBoxConfig(a.cfg, a.subState)
		if err := writeJSON(generatedPath, generated); err != nil {
			return err
		}
	}

	autoUpdatePath := filepath.Join(a.dataDir, "auto-update-last-run.json")
	if b, err := os.ReadFile(autoUpdatePath); err == nil {
		var stored map[string]string
		if json.Unmarshal(b, &stored) == nil {
			for k, v := range stored {
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					a.autoUpdateLastRun[k] = t
				}
			}
		}
	}

	return nil
}

func (a *App) persistAutoUpdateLastRunLocked() {
	stored := map[string]string{}
	for k, v := range a.autoUpdateLastRun {
		stored[k] = v.Format(time.RFC3339)
	}
	_ = writeJSON(filepath.Join(a.dataDir, "auto-update-last-run.json"), stored)
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		defer a.mu.RUnlock()
		ok(w, map[string]any{
			"config":             a.cfg,
			"subscription":       a.subState,
			"availableOutbounds": collectOutbounds(a.cfg, a.subState),
			"runtime":            a.runtimeInfo,
			"kernel":             a.kernelStatus(),
			"architecture":       detectPlatform(),
			"plannedKernel":      a.plannedKernel,
			"releaseList":        a.releaseList,
			"download":           a.downloadState,
			"authEnabled":        strings.TrimSpace(os.Getenv("SUB2SOCKS5_PASSWORD")) != "",
		})
	case http.MethodPost:
		var body map[string]any
		if err := decodeJSON(r.Body, &body); err != nil {
			fail(w, 400, err.Error())
			return
		}
		skipRuntimeRestart := strings.TrimSpace(r.Header.Get("x-skip-runtime-restart")) == "1"
		a.mu.Lock()
		a.cfg = body
		_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
		generated := buildSingBoxConfig(a.cfg, a.subState)
		_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
		wasRunning := a.proc != nil && a.proc.Process != nil
		if wasRunning && !skipRuntimeRestart {
			if err := a.startRuntimeLocked(); err != nil {
				a.appendRuntimeLog("apply config failed: " + err.Error())
				a.mu.Unlock()
				fail(w, 500, err.Error())
				return
			}
			a.appendRuntimeLog("config applied and runtime restarted")
		}
		runtimeState := a.runtimeInfo
		a.mu.Unlock()
		ok(w, map[string]any{"ok": true, "generated": generated, "runtime": runtimeState})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *App) handleSubscriptionRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.refreshSubscriptionLocked("manual"); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, a.subState)
}

// handleSubscriptionPreview 解析订阅 URL 或粘贴文本，返回节点预览（不写入磁盘）。
// 入参：{url?: string, raw?: string, userAgent?: string}
// 出参：{nodes: [{tag,type,server,port,status,reason,raw}], stats:{valid,invalid,dup,total}, warnings: []string}
// status: valid | dup | invalid
func (a *App) handleSubscriptionPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	urlStr := strings.TrimSpace(mustStr(body["url"]))
	raw := mustStr(body["raw"])
	if urlStr == "" && strings.TrimSpace(raw) == "" {
		fail(w, http.StatusBadRequest, "url 或 raw 至少提供一项")
		return
	}

	warnings := []string{}
	parsedNodes := []map[string]any{}

	if urlStr != "" {
		if err := validateSubscriptionURL(urlStr); err != nil {
			fail(w, http.StatusBadRequest, "订阅地址不安全: "+err.Error())
			return
		}
		userAgent := strings.TrimSpace(mustStr(body["userAgent"]))
		if userAgent == "" {
			a.mu.RLock()
			userAgent = getString(getMap(a.cfg, "subscription"), "userAgent", "sub2socks5-go/0.1.0")
			a.mu.RUnlock()
		}
		client := &http.Client{
			Timeout: 20 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return fmt.Errorf("too many redirects")
				}
				if err := validateSubscriptionURL(req.URL.String()); err != nil {
					return fmt.Errorf("redirect to unsafe URL: %w", err)
				}
				return nil
			},
		}
		req, _ := http.NewRequest(http.MethodGet, urlStr, nil)
		req.Header.Set("user-agent", userAgent)
		resp, err := client.Do(req)
		if err != nil {
			fail(w, http.StatusBadGateway, "拉取订阅失败: "+err.Error())
			return
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			fail(w, http.StatusBadGateway, fmt.Sprintf("拉取订阅失败: HTTP %d", resp.StatusCode))
			return
		}
		pr := parseSubscription(string(respBody))
		parsedNodes = append(parsedNodes, pr.nodes...)
		warnings = append(warnings, pr.warnings...)
	}

	if strings.TrimSpace(raw) != "" {
		pr := parseSubscription(raw)
		parsedNodes = append(parsedNodes, pr.nodes...)
		warnings = append(warnings, pr.warnings...)
	}

	// 收集已有节点的指纹用于重复检测（订阅节点 + 手动节点）。
	existing := map[string]bool{}
	a.mu.RLock()
	for _, n := range getSlice(a.subState, "nodes") {
		if m, ok := n.(map[string]any); ok {
			existing[fingerprintNode(m)] = true
		}
	}
	if nr := getMap(a.cfg, "nodeRegistry"); nr != nil {
		for _, n := range getSlice(nr, "manualNodes") {
			if m, ok := n.(map[string]any); ok {
				existing[fingerprintNode(m)] = true
			}
		}
	}
	a.mu.RUnlock()

	// 同批次内部去重也要标记。
	seenInBatch := map[string]bool{}
	preview := []map[string]any{}
	stats := map[string]int{"valid": 0, "invalid": 0, "dup": 0, "total": 0}

	for _, n := range parsedNodes {
		stats["total"]++
		fp := fingerprintNode(n)
		entry := map[string]any{
			"tag":    mustStr(n["tag"]),
			"type":   mustStr(n["type"]),
			"server": mustStr(n["server"]),
			"port":   n["server_port"],
		}
		// 批量导入向导需要的协议级元数据，便于用户在确认前核对节点细节。
		// 仅透传非空字段以避免 JSON 体积膨胀。
		if v, ok := n["transport"]; ok && v != nil {
			entry["transport"] = v
		}
		if v, ok := n["tls"]; ok && v != nil {
			entry["tls"] = v
		}
		if v, ok := n["network"]; ok && v != nil {
			entry["network"] = v
		}
		if v := strings.TrimSpace(mustStr(n["method"])); v != "" {
			entry["method"] = v
		}
		// 校验关键字段——空 server / port 视为 invalid。
		if mustStr(n["type"]) == "" || mustStr(n["server"]) == "" {
			entry["status"] = "invalid"
			entry["reason"] = "节点缺少 type 或 server"
			stats["invalid"]++
		} else if existing[fp] || seenInBatch[fp] {
			entry["status"] = "dup"
			entry["reason"] = "与现有节点重复"
			stats["dup"]++
		} else {
			entry["status"] = "valid"
			seenInBatch[fp] = true
			stats["valid"]++
		}
		preview = append(preview, entry)
	}

	ok(w, map[string]any{
		"nodes":    preview,
		"stats":    stats,
		"warnings": warnings,
	})
}

// fingerprintNode 计算节点的归一化指纹（type::tag::server::port），用于去重检测。
func fingerprintNode(n map[string]any) string {
	return fmt.Sprintf("%s::%s::%s::%v",
		strings.ToLower(mustStr(n["type"])),
		mustStr(n["tag"]),
		mustStr(n["server"]),
		n["server_port"])
}

func (a *App) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		defer a.mu.RUnlock()
		nr := getMap(a.cfg, "nodeRegistry")
		disabled := toStringSet(getSlice(nr, "disabledSubscriptionTags"))
		nodes := []any{}
		for _, n := range getSlice(a.subState, "nodes") {
			m, okk := n.(map[string]any)
			if !okk || disabled[mustStr(m["tag"])] {
				continue
			}
			nodes = append(nodes, m)
		}
		ok(w, map[string]any{
			"subscriptionNodes":        nodes,
			"disabledSubscriptionTags": getSlice(nr, "disabledSubscriptionTags"),
			"manualNodes":              getSlice(nr, "manualNodes"),
			"groups":                   getSlice(nr, "groups"),
			"chains":                   getSlice(nr, "chains"),
			"availableOutbounds":       collectOutbounds(a.cfg, a.subState),
			"fallbackStates":           map[string]any{},
		})
	case http.MethodPost:
		var body map[string]any
		if err := decodeJSON(r.Body, &body); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.mu.Lock()
		nr := getMap(a.cfg, "nodeRegistry")
		nr["manualNodes"] = getSlice(body, "manualNodes")
		nr["groups"] = getSlice(body, "groups")
		nr["chains"] = getSlice(body, "chains")
		nr["disabledSubscriptionTags"] = getSlice(body, "disabledSubscriptionTags")
		a.cfg["nodeRegistry"] = nr
		_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
		if a.proc != nil && a.proc.Process != nil {
			if err := a.startRuntimeLocked(); err != nil {
				a.appendRuntimeLog("apply node config failed: " + err.Error())
				a.mu.Unlock()
				fail(w, 500, err.Error())
				return
			}
			a.appendRuntimeLog("node config applied and runtime restarted")
		}
		outbounds := collectOutbounds(a.cfg, a.subState)
		a.mu.Unlock()
		ok(w, map[string]any{"ok": true, "manualNodes": nr["manualNodes"], "groups": nr["groups"], "chains": nr["chains"], "availableOutbounds": outbounds})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *App) handleNodeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	res := parseManualNodeInput(mustStr(body["raw"]))
	ok(w, res)
}

func (a *App) handleNodesCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	tags := []string{}
	for _, t := range getSlice(body, "tags") {
		s := strings.TrimSpace(mustStr(t))
		if s != "" {
			tags = append(tags, s)
		}
	}
	if len(tags) == 0 {
		fail(w, 400, "Missing node tags for check")
		return
	}
	urlToTest := mustStr(body["url"])
	if urlToTest == "" {
		urlToTest = "https://www.gstatic.com/generate_204"
	}
	allowedTestURLs := []string{
		"https://www.gstatic.com/generate_204",
		"https://cp.cloudflare.com/generate_204",
		"https://www.google.com/generate_204",
		"http://www.gstatic.com/generate_204",
	}
	allowed := false
	for _, u := range allowedTestURLs {
		if urlToTest == u {
			allowed = true
			break
		}
	}
	if !allowed {
		fail(w, 400, "测速 URL 必须在白名单内")
		return
	}
	timeout := int(toFloat(body["timeoutMs"]))
	if timeout <= 0 {
		timeout = 5000
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(len(tags)*(timeout+500))*time.Millisecond)
	defer cancel()

	results := make(map[string]any)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)

	for _, tag := range tags {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				results[t] = map[string]any{"ok": false, "text": "超时", "error": "context canceled", "checkedAt": time.Now().Format(time.RFC3339), "checkedTag": t}
				mu.Unlock()
				return
			}
			delay, err := measureProxyDelay(t, urlToTest, timeout)
			mu.Lock()
			if err != nil {
				results[t] = map[string]any{"ok": false, "text": "失败", "error": err.Error(), "checkedAt": time.Now().Format(time.RFC3339), "checkedTag": t}
			} else {
				results[t] = map[string]any{"ok": true, "delay": delay, "text": fmt.Sprintf("%d ms", delay), "checkedAt": time.Now().Format(time.RFC3339), "checkedTag": t}
			}
			mu.Unlock()
		}(tag)
	}
	wg.Wait()
	ok(w, map[string]any{"ok": true, "url": urlToTest, "timeoutMs": timeout, "results": results})
}

func (a *App) handleNextPort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	host := mustStr(body["host"])
	if host == "" {
		host = "127.0.0.1"
	}
	start := int(toFloat(body["start"]))
	if start <= 0 {
		fail(w, 400, "Invalid start port")
		return
	}
	p := findPort(host, start)
	ok(w, map[string]any{"host": host, "port": p})
}

func (a *App) handleRuntimeGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	a.mu.RLock()
	generated := buildSingBoxConfig(a.cfg, a.subState)
	a.mu.RUnlock()
	_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
	ok(w, map[string]any{"ok": true, "path": filepath.Join("internal", "runtime", "sing-box.json"), "generated": generated})
}

func (a *App) handleRuntimeStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.startRuntimeLocked(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	rt := a.runtimeInfo
	ok(w, rt)
}

func (a *App) startRuntimeLocked() error {
	if err := ensureNodesLoaded(a.cfg, a.subState); err != nil {
		return err
	}
	generated := buildSingBoxConfig(a.cfg, a.subState)
	cfgPath := filepath.Join(a.runtimeDir, "sing-box.json")
	_ = writeJSON(cfgPath, generated)
	if a.proc != nil && a.proc.Process != nil {
		_ = a.proc.Process.Kill()
		a.proc = nil
	}
	bin, err := a.resolveSingBoxBinaryPathLocked()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "run", "-c", cfgPath)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	a.manualStopRequested = false
	a.autoRestartAttempts = 0
	a.proc = cmd
	a.runtimeInfo["state"] = "running"
	a.runtimeInfo["running"] = true
	a.runtimeInfo["startedAt"] = time.Now().Format(time.RFC3339)
	a.runtimeInfo["pid"] = cmd.Process.Pid
	a.runtimeInfo["runningConfigHash"] = configHashOf(a.cfg)
	a.runtimeInfo["lastError"] = ""
	a.appendRuntimeLog("sing-box started")
	go a.captureLogs(stdout)
	go a.captureLogs(stderr)
	go func(c *exec.Cmd, startTime time.Time) {
		waitErr := c.Wait()
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.proc != c {
			return
		}
		a.proc = nil
		a.runtimeInfo["state"] = "stopped"
		a.runtimeInfo["running"] = false
		a.runtimeInfo["pid"] = 0
		if waitErr != nil {
			a.runtimeInfo["lastError"] = waitErr.Error()
			a.appendRuntimeLog("sing-box exited with error: " + waitErr.Error())
		} else {
			a.appendRuntimeLog("sing-box exited")
		}
		if time.Since(startTime) > 60*time.Second {
			a.autoRestartAttempts = 0
		}
		if !a.manualStopRequested {
			a.autoRestartAttempts += 1
			attempt := a.autoRestartAttempts
			delay := time.Duration(attempt*2) * time.Second
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			a.appendRuntimeLog(fmt.Sprintf("runtime stopped unexpectedly, auto-restart in %ds (attempt %d)", int(delay/time.Second), attempt))
			go a.autoRestartAfter(delay)
		}
	}(cmd, time.Now())
	return nil
}

func (a *App) autoRestartAfter(delay time.Duration) {
	time.Sleep(delay)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc != nil || a.manualStopRequested {
		return
	}
	if err := a.startRuntimeLocked(); err != nil {
		a.appendRuntimeLog("auto restart failed: " + err.Error())
	}
}

func (a *App) handleRuntimeStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	a.mu.Lock()
	a.manualStopRequested = true
	a.autoRestartAttempts = 0
	if a.proc != nil && a.proc.Process != nil {
		_ = a.proc.Process.Kill()
		a.proc = nil
	}
	a.runtimeInfo["state"] = "stopped"
	a.runtimeInfo["running"] = false
	a.runtimeInfo["pid"] = 0
	a.appendRuntimeLog("runtime stop requested")
	rt := a.runtimeInfo
	a.mu.Unlock()
	ok(w, rt)
}

func (a *App) handleRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ok(w, a.runtimeInfo)
}

func (a *App) handleRuntimeGenerated(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	p := filepath.Join(a.runtimeDir, "sing-box.json")
	b, err := os.ReadFile(p)
	if err != nil {
		ok(w, map[string]any{})
		return
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		ok(w, map[string]any{})
		return
	}
	ok(w, v)
}

func (a *App) handleKernelArch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodPost:
		arch := detectPlatform()
		_ = writeJSON(filepath.Join(a.dataDir, "architecture-info.json"), arch)
		if r.Method == http.MethodPost {
			if latest, err := getLatestRelease(arch); err == nil {
				a.mu.Lock()
				a.plannedKernel = latest
				_ = writeJSON(filepath.Join(a.dataDir, "planned-kernel-info.json"), latest)
				a.mu.Unlock()
			}
		}
		a.mu.RLock()
		planned := a.plannedKernel
		a.mu.RUnlock()
		ok(w, map[string]any{"architecture": arch, "stored": true, "plannedKernel": planned, "kernel": a.kernelStatus()})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *App) handleKernelStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	ok(w, a.kernelStatus())
}

func (a *App) handleKernelReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	a.mu.RLock()
	if len(a.releaseList) > 0 {
		cached := a.releaseList
		a.mu.RUnlock()
		ok(w, cached)
		return
	}
	a.mu.RUnlock()
	releases, err := listReleases(detectPlatform())
	if err != nil {
		a.mu.RLock()
		cached := a.releaseList
		a.mu.RUnlock()
		ok(w, cached)
		return
	}
	a.mu.Lock()
	a.releaseList = releases
	_ = writeJSON(filepath.Join(a.dataDir, "release-list.json"), releases)
	a.mu.Unlock()
	ok(w, releases)
}

func (a *App) handleKernelReleasesUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	releases, err := listReleases(detectPlatform())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.mu.Lock()
	a.releaseList = releases
	_ = writeJSON(filepath.Join(a.dataDir, "release-list.json"), releases)
	if len(releases) > 0 {
		if p, okk := releases[0].(map[string]any); okk {
			a.plannedKernel = p
			_ = writeJSON(filepath.Join(a.dataDir, "planned-kernel-info.json"), p)
		}
	}
	planned := a.plannedKernel
	a.mu.Unlock()
	ok(w, map[string]any{"releaseList": releases, "plannedKernel": planned})
}

func (a *App) handleKernelPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	_ = decodeJSON(r.Body, &body)
	version := mustStr(body["version"])
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, item := range a.releaseList {
		m, okk := item.(map[string]any)
		if okk && mustStr(m["version"]) == version {
			a.plannedKernel = m
			_ = writeJSON(filepath.Join(a.dataDir, "planned-kernel-info.json"), m)
			ok(w, m)
			return
		}
	}
	fail(w, 404, "Requested kernel version not found")
}

func (a *App) handleKernelDownload(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		st := a.downloadState
		a.mu.RUnlock()
		ok(w, st)
	case http.MethodPost:
		a.mu.Lock()
		planned := a.plannedKernel
		a.downloadState = map[string]any{"active": true, "steps": []any{}, "progress": map[string]any{"percent": 0, "stage": "prepare", "message": "preparing"}, "updatedAt": time.Now().Format(time.RFC3339)}
		a.pushDownloadStepLocked("prepare", "Prepared download workspace", map[string]any{})
		a.mu.Unlock()
		if planned == nil {
			fail(w, 400, "No planned kernel selected")
			return
		}
		result, err := a.downloadKernel(planned)
		if err != nil {
			a.mu.Lock()
			a.downloadState = map[string]any{"active": false, "steps": []any{map[string]any{"stage": "error", "message": err.Error()}}, "progress": map[string]any{"percent": nil, "stage": "error", "message": err.Error()}, "updatedAt": time.Now().Format(time.RFC3339)}
			ds := a.downloadState
			a.mu.Unlock()
			fail(w, 500, err.Error())
			_ = ds
			return
		}
		a.mu.Lock()
		a.downloadState = map[string]any{"active": false, "steps": []any{map[string]any{"stage": "done", "message": "Kernel installation completed"}}, "progress": map[string]any{"percent": 100, "stage": "done", "message": "Kernel installation completed"}, "updatedAt": time.Now().Format(time.RFC3339)}
		ds := a.downloadState
		a.mu.Unlock()
		ok(w, map[string]any{"result": result, "kernel": a.kernelStatus(), "download": ds})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *App) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/") {
		fail(w, 404, "API route not found")
		return
	}
	if p == "/" {
		p = "/index.html"
	} else if p == "/login" {
		p = "/login.html"
	}
	clean := strings.TrimPrefix(path.Clean(p), "/")
	var (
		b   []byte
		err error
	)
	if a.staticFS != nil {
		b, err = fs.ReadFile(a.staticFS, clean)
	} else {
		full := filepath.Join(a.publicDir, filepath.FromSlash(clean))
		if !strings.HasPrefix(full, a.publicDir) {
			http.Error(w, "Forbidden", 403)
			return
		}
		b, err = os.ReadFile(full)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := "text/plain; charset=utf-8"
	switch filepath.Ext(clean) {
	case ".html":
		ct = "text/html; charset=utf-8"
	case ".css":
		ct = "text/css; charset=utf-8"
	case ".js":
		ct = "application/javascript; charset=utf-8"
	}
	w.Header().Set("content-type", ct)
	_, _ = w.Write(b)
}

func (a *App) kernelStatus() map[string]any {
	p := strings.TrimSpace(os.Getenv("SUB2SOCKS5_SING_BOX_BINARY"))
	if p == "" {
		exe := "sing-box"
		if runtime.GOOS == "windows" {
			exe = "sing-box.exe"
		}
		p = filepath.Join(a.binDir, exe)
	}
	_, err := os.Stat(p)
	installed := err == nil
	var releaseInfo any = nil
	verFile := filepath.Join(a.binDir, "sing-box-version.json")
	if b, err := os.ReadFile(verFile); err == nil {
		var tmp any
		if json.Unmarshal(b, &tmp) == nil {
			releaseInfo = tmp
		}
	}
	return map[string]any{"installed": installed, "binaryPath": p, "platform": detectPlatform(), "releaseInfo": releaseInfo}
}

func (a *App) appendRuntimeLog(msg string) {
	logs := getStringSlice(a.runtimeInfo, "logs")
	logs = append(logs, time.Now().Format(time.RFC3339)+" "+msg)
	if len(logs) > 1000 {
		logs = logs[len(logs)-1000:]
	}
	a.runtimeInfo["logs"] = logs
}

func (a *App) captureLogs(r io.ReadCloser) {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			a.mu.Lock()
			a.appendRuntimeLog(line)
			a.mu.Unlock()
		}
	}
}

func resolveManagedPath(rootDir, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(rootDir, filepath.FromSlash(p))
}

func measureProxyDelay(tag, testURL string, timeoutMs int) (int, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:19090/proxies/%s/delay?url=%s&timeout=%d", url.QueryEscape(tag), url.QueryEscape(testURL), timeoutMs)
	client := &http.Client{Timeout: time.Duration(timeoutMs+1500) * time.Millisecond}
	resp, err := client.Get(endpoint)
	if err != nil {
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "connection refused"):
			return 0, fmt.Errorf("测速控制接口未就绪（connection refused），请先确认 sing-box 已正常启动")
		case strings.Contains(msg, "timeout"):
			return 0, fmt.Errorf("测速控制接口请求超时，请稍后重试")
		default:
			return 0, fmt.Errorf("测速控制接口未就绪，请先确认 sing-box 已正常启动")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		text := strings.TrimSpace(string(body))
		if text != "" {
			return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, text)
		}
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}
	delay := int(toFloat(data["delay"]))
	if delay < 0 {
		return 0, fmt.Errorf("No delay data")
	}
	return delay, nil
}

func (a *App) handleNodesEgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var body map[string]any
	if err := decodeJSON(r.Body, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	tags := []string{}
	for _, t := range getSlice(body, "tags") {
		s := strings.TrimSpace(mustStr(t))
		if s != "" {
			tags = append(tags, s)
		}
	}
	if len(tags) == 0 {
		fail(w, 400, "Missing node tags")
		return
	}
	timeout := int(toFloat(body["timeoutMs"]))
	if timeout <= 0 {
		timeout = 5000
	}

	a.mu.RLock()
	cfg := cloneMap(a.cfg)
	a.mu.RUnlock()
	host, port, err := pickProxySocksPort(cfg)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(len(tags)*(timeout+500))*time.Millisecond)
	defer cancel()

	results := map[string]any{}
	for _, tag := range tags {
		select {
		case <-ctx.Done():
			results[tag] = map[string]any{"ok": false, "error": "context canceled"}
			continue
		default:
		}
		if err := clashSelectProxy("proxy", tag, timeout); err != nil {
			results[tag] = map[string]any{"ok": false, "error": err.Error()}
			continue
		}
		time.Sleep(120 * time.Millisecond)
		ip, ipErr := fetchIPViaSocks(host, port, timeout)
		if ipErr != nil {
			results[tag] = map[string]any{"ok": false, "error": ipErr.Error()}
			continue
		}
		results[tag] = map[string]any{"ok": true, "egressIP": ip}
	}
	ok(w, map[string]any{"ok": true, "results": results})
}

func pickProxySocksPort(cfg map[string]any) (string, int, error) {
	host := "127.0.0.1"
	port := 0
	for _, item := range getSlice(cfg, "ports") {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if mustStr(m["target"]) != "proxy" {
			continue
		}
		if h := strings.TrimSpace(mustStr(m["listen"])); h != "" {
			host = h
		}
		p := int(toFloat(m["port"]))
		if p > 0 {
			port = p
			break
		}
	}
	if port <= 0 {
		return "", 0, fmt.Errorf("no socks5 service targeting proxy found")
	}
	return host, port, nil
}

func clashSelectProxy(groupTag, selectedTag string, timeoutMs int) error {
	endpoint := fmt.Sprintf("http://127.0.0.1:19090/proxies/%s", url.QueryEscape(groupTag))
	payload, _ := json.Marshal(map[string]any{"name": selectedTag})
	req, _ := http.NewRequest(http.MethodPut, endpoint, bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	client := &http.Client{Timeout: time.Duration(timeoutMs+1500) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("selector update failed: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func fetchIPViaSocks(socksHost string, socksPort int, timeoutMs int) (string, error) {
	targets := []struct {
		host string
		path string
	}{
		{host: "api.ipify.org", path: "/?format=text"},
		{host: "ipv4.icanhazip.com", path: "/"},
		{host: "ifconfig.me", path: "/ip"},
		{host: "api.ip.sb", path: "/ip"},
	}

	var lastErr error
	for _, target := range targets {
		ip, err := fetchIPViaSocksTarget(socksHost, socksPort, timeoutMs, target.host, target.path)
		if err == nil && strings.TrimSpace(ip) != "" {
			return strings.TrimSpace(ip), nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no egress ip endpoint available")
}

func fetchIPViaSocksTarget(socksHost string, socksPort int, timeoutMs int, domain string, reqPath string) (string, error) {
	address := net.JoinHostPort(socksHost, strconv.Itoa(socksPort))
	conn, err := net.DialTimeout("tcp", address, time.Duration(timeoutMs)*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Duration(timeoutMs+1500) * time.Millisecond))

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return "", err
	}
	hello := make([]byte, 2)
	if _, err := io.ReadFull(conn, hello); err != nil {
		return "", err
	}
	if hello[0] != 0x05 || hello[1] == 0xff {
		return "", fmt.Errorf("socks handshake failed")
	}

	domainBytes := []byte(domain)
	request := make([]byte, 0, 7+len(domainBytes))
	request = append(request, 0x05, 0x01, 0x00, 0x03, byte(len(domainBytes)))
	request = append(request, domainBytes...)
	request = append(request, 0x00, 0x50)
	if _, err := conn.Write(request); err != nil {
		return "", err
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return "", err
	}
	if head[1] != 0x00 {
		return "", fmt.Errorf("socks connect failed: %d", int(head[1]))
	}
	switch head[3] {
	case 0x01:
		skip := make([]byte, 6)
		if _, err := io.ReadFull(conn, skip); err != nil {
			return "", err
		}
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", err
		}
		skip := make([]byte, int(length[0])+2)
		if _, err := io.ReadFull(conn, skip); err != nil {
			return "", err
		}
	case 0x04:
		skip := make([]byte, 18)
		if _, err := io.ReadFull(conn, skip); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unknown atyp: %d", int(head[3]))
	}

	rawReq := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: sub2socks5-go/0.1.0\r\nConnection: close\r\n\r\n", reqPath, domain)
	if _, err := conn.Write([]byte(rawReq)); err != nil {
		return "", err
	}
	responseBytes, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	text := string(responseBytes)
	if idx := strings.Index(text, "\r\n\r\n"); idx >= 0 {
		text = text[idx+4:]
	}
	ip := strings.TrimSpace(strings.Split(text, "\n")[0])
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid egress ip response: %q", ip)
	}
	return ip, nil
}

func listReleases(platform map[string]any) ([]any, error) {
	req, _ := newGitHubRequest(http.MethodGet, "https://api.github.com/repos/SagerNet/sing-box/releases?per_page=20")
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Failed to fetch sing-box releases: HTTP %d", resp.StatusCode)
	}
	var raw []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []any{}
	suffix := mustStr(platform["assetSuffix"])
	for _, rel := range raw {
		assets, _ := rel["assets"].([]any)
		asset := pickAsset(assets, suffix)
		if asset == nil {
			continue
		}
		out = append(out, map[string]any{
			"version":     mustStr(rel["tag_name"]),
			"publishedAt": mustStr(rel["published_at"]),
			"assetName":   mustStr(asset["name"]),
			"downloadUrl": mustStr(asset["browser_download_url"]),
			"size":        int(toFloat(asset["size"])),
			"platform":    platform,
		})
	}
	return out, nil
}

func getLatestRelease(platform map[string]any) (map[string]any, error) {
	req, _ := newGitHubRequest(http.MethodGet, "https://api.github.com/repos/SagerNet/sing-box/releases/latest")
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Failed to fetch sing-box latest release: HTTP %d", resp.StatusCode)
	}
	var rel map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	assets, _ := rel["assets"].([]any)
	suffix := mustStr(platform["assetSuffix"])
	asset := pickAsset(assets, suffix)
	if asset == nil {
		return nil, fmt.Errorf("No asset found for %s", suffix)
	}
	return map[string]any{
		"version":     mustStr(rel["tag_name"]),
		"publishedAt": mustStr(rel["published_at"]),
		"assetName":   mustStr(asset["name"]),
		"downloadUrl": mustStr(asset["browser_download_url"]),
		"size":        int(toFloat(asset["size"])),
		"platform":    platform,
	}, nil
}

func newGitHubRequest(method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("user-agent", "sub2socks5-go/0.1.0")
	req.Header.Set("accept", "application/vnd.github+json")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	return req, nil
}

func pickAsset(assets []any, suffix string) map[string]any {
	for _, a := range assets {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(mustStr(m["name"]))
		if strings.Contains(name, strings.ToLower(suffix)) && (strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz")) && !strings.Contains(name, "lite") {
			if strings.Contains(name, "legacy") || strings.Contains(name, "windows-7") {
				continue
			}
			return m
		}
	}
	for _, a := range assets {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name := strings.ToLower(mustStr(m["name"]))
		if strings.Contains(name, strings.ToLower(suffix)) && (strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".tar.gz")) {
			return m
		}
	}
	return nil
}

func (a *App) downloadKernel(release map[string]any) (map[string]any, error) {
	urlStr := mustStr(release["downloadUrl"])
	assetName := mustStr(release["assetName"])
	if urlStr == "" || assetName == "" {
		return nil, fmt.Errorf("Missing release information for download")
	}
	tmpDir, err := os.MkdirTemp("", "sub2socks5-go-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, assetName)
	a.mu.Lock()
	a.pushDownloadStepLocked("prepare", "Download workspace ready", map[string]any{"assetName": assetName})
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	req.Header.Set("user-agent", "sub2socks5-go/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Failed to download sing-box: HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(archivePath)
	if err != nil {
		return nil, err
	}
	total := toFloat(resp.ContentLength)
	if total <= 0 {
		total = toFloat(release["size"])
	}
	buf := make([]byte, 64*1024)
	var downloaded float64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				f.Close()
				return nil, wErr
			}
			downloaded += float64(n)
			percent := any(nil)
			if total > 0 {
				percent = float64(int((downloaded/total)*10000)) / 100
			}
			a.mu.Lock()
			a.pushDownloadStepLocked("download", "Downloading kernel archive", map[string]any{
				"downloadedBytes": int(downloaded),
				"totalBytes":      int(total),
				"percent":         percent,
				"threads":         1,
			})
			a.mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return nil, readErr
		}
	}
	_ = f.Close()
	extractDir := filepath.Join(tmpDir, "extract")
	_ = os.MkdirAll(extractDir, 0o755)
	a.mu.Lock()
	a.pushDownloadStepLocked("extract", "Extracting kernel archive", map[string]any{"archivePath": archivePath})
	a.mu.Unlock()
	if strings.HasSuffix(strings.ToLower(assetName), ".zip") {
		if err := extractZip(archivePath, extractDir); err != nil {
			return nil, err
		}
	} else if strings.HasSuffix(strings.ToLower(assetName), ".tar.gz") {
		if err := extractTarGz(archivePath, extractDir); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("Unsupported archive format: %s", assetName)
	}
	exe := mustStr(getMap(release, "platform")["executableName"])
	binSource, err := findBinaryFile(extractDir, exe)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.pushDownloadStepLocked("search", "Locating executable file", map[string]any{"executableName": exe})
	a.mu.Unlock()
	binTarget := filepath.Join(a.binDir, exe)
	b, err := os.ReadFile(binSource)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(binTarget, b, 0o755); err != nil {
		return nil, err
	}
	binSum := sha256.Sum256(b)
	archiveBytes, _ := os.ReadFile(archivePath)
	archiveSum := sha256.Sum256(archiveBytes)
	a.mu.Lock()
	a.pushDownloadStepLocked("install", "Installing kernel binary", map[string]any{"binaryTarget": binTarget})
	a.mu.Unlock()
	releaseWithSum := cloneMap(release)
	releaseWithSum["binarySha256"] = hex.EncodeToString(binSum[:])
	releaseWithSum["archiveSha256"] = hex.EncodeToString(archiveSum[:])
	releaseWithSum["installedAt"] = time.Now().Format(time.RFC3339)
	_ = writeJSON(filepath.Join(a.binDir, "sing-box-version.json"), releaseWithSum)
	a.mu.Lock()
	appCfg := getMap(a.cfg, "app")
	appCfg["singBoxBinary"] = filepath.ToSlash(filepath.Join("internal", "bin", exe))
	a.cfg["app"] = appCfg
	_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
	a.mu.Unlock()
	a.mu.Lock()
	a.pushDownloadStepLocked("done", "Kernel installation completed", map[string]any{"binaryPath": filepath.ToSlash(filepath.Join("internal", "bin", exe))})
	a.mu.Unlock()
	return map[string]any{"ok": true, "binaryPath": filepath.ToSlash(filepath.Join("internal", "bin", exe)), "version": release["version"], "assetName": assetName}, nil
}

func (a *App) pushDownloadStepLocked(stage, message string, details map[string]any) {
	step := map[string]any{"stage": stage, "message": message, "details": details, "time": time.Now().Format(time.RFC3339)}
	steps := getSlice(a.downloadState, "steps")
	steps = append(steps, step)
	if len(steps) > 200 {
		steps = steps[len(steps)-200:]
	}
	a.downloadState["steps"] = steps
	progress := map[string]any{
		"percent":         details["percent"],
		"stage":           stage,
		"message":         message,
		"downloadedBytes": details["downloadedBytes"],
		"totalBytes":      details["totalBytes"],
		"threads":         details["threads"],
	}
	a.downloadState["progress"] = progress
	a.downloadState["updatedAt"] = step["time"]
}

func ensureNodesLoaded(cfg, sub map[string]any) error {
	if len(getSlice(sub, "nodes")) == 0 && len(getSlice(getMap(cfg, "nodeRegistry"), "manualNodes")) == 0 {
		return fmt.Errorf("没有可用节点，请先更新订阅或添加手动节点。")
	}
	return nil
}

func (a *App) resolveSingBoxBinaryPathLocked() (string, error) {
	if env := strings.TrimSpace(os.Getenv("SUB2SOCKS5_SING_BOX_BINARY")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	configured := resolveManagedPath(a.rootDir, getString(getMap(a.cfg, "app"), "singBoxBinary", ""))
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured, nil
		}
	}
	ks := a.kernelStatus()
	installed := mustStr(ks["binaryPath"])
	if installed != "" {
		if _, err := os.Stat(installed); err == nil {
			appCfg := getMap(a.cfg, "app")
			appCfg["singBoxBinary"] = filepath.ToSlash(filepath.Join("internal", "bin", filepath.Base(installed)))
			a.cfg["app"] = appCfg
			_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
			a.appendRuntimeLog("sing-box binary fallback to installed path: " + installed)
			return installed, nil
		}
	}
	return "", fmt.Errorf("sing-box binary not found. configured=%s, installed=%s", emptyIf(configured, "(empty)"), emptyIf(installed, "(none)"))
}

func emptyIf(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func extractZip(archivePath, extractDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		dest := filepath.Join(extractDir, filepath.Clean(f.Name))
		rel, err := filepath.Rel(extractDir, dest)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("zip slip detected: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archivePath, extractDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		dest := filepath.Join(extractDir, filepath.Clean(hdr.Name))
		rel, err := filepath.Rel(extractDir, dest)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("tar slip detected: %s", hdr.Name)
		}
		if hdr.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func findBinaryFile(root, name string) (string, error) {
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == name {
			found = path
			return io.EOF
		}
		return nil
	})
	if err == io.EOF && found != "" {
		return found, nil
	}
	if found != "" {
		return found, nil
	}
	return "", fmt.Errorf("Executable not found in archive: %s", name)
}

func detectPlatform() map[string]any {
	osName := map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[runtime.GOARCH]
	if osName == "" || arch == "" {
		osName = runtime.GOOS
		arch = runtime.GOARCH
	}
	exe := "sing-box"
	if osName == "windows" {
		exe = "sing-box.exe"
	}
	return map[string]any{"detectedAt": time.Now().Format(time.RFC3339), "platform": runtime.GOOS, "arch": runtime.GOARCH, "os": osName, "archName": arch, "assetSuffix": osName + "-" + arch, "executableName": exe}
}

func fetchSubscription(sub map[string]any) map[string]any {
	urls := []string{}
	for _, v := range getSlice(sub, "urls") {
		s := strings.TrimSpace(mustStr(v))
		if s != "" {
			urls = append(urls, s)
		}
	}
	if len(urls) == 0 {
		if s := strings.TrimSpace(getString(sub, "url", "")); s != "" {
			urls = append(urls, s)
		}
	}
	if len(urls) == 0 {
		return map[string]any{"nodes": []any{}, "raw": "", "warnings": []any{"订阅地址为空"}}
	}

	warnings := []any{}
	for _, u := range urls {
		if err := validateSubscriptionURL(u); err != nil {
			warnings = append(warnings, fmt.Sprintf("订阅地址不安全: %s (%v)", u, err))
		}
	}
	if len(warnings) > 0 {
		return map[string]any{"nodes": []any{}, "raw": "", "warnings": warnings}
	}

	rawParts := []string{}
	nodes := []map[string]any{}
	filters := getSlice(sub, "filters")
	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if err := validateSubscriptionURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect to unsafe URL: %w", err)
			}
			return nil
		},
	}
	for idx, u := range urls {
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("user-agent", getString(sub, "userAgent", "sub2socks5-go/0.1.0"))
		resp, err := client.Do(req)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("订阅拉取失败: %s %v", u, err))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			warnings = append(warnings, fmt.Sprintf("订阅拉取失败: %s HTTP %d", u, resp.StatusCode))
			continue
		}
		txt := string(body)
		rawParts = append(rawParts, "### "+u+"\n"+txt)
		parsed := parseSubscription(txt)
		filterMode := "off"
		filterKeywords := []string{}
		if idx < len(filters) {
			if fm, ok := filters[idx].(map[string]any); ok {
				filterMode = strings.TrimSpace(mustStr(fm["mode"]))
				for _, kw := range getSlice(fm, "keywords") {
					s := strings.TrimSpace(mustStr(kw))
					if s != "" {
						filterKeywords = append(filterKeywords, strings.ToLower(s))
					}
				}
			}
		}
		for _, n := range parsed.nodes {
			if shouldKeepNodeByFilter(n, filterMode, filterKeywords) {
				nodes = append(nodes, n)
			}
		}
		for _, w := range parsed.warnings {
			warnings = append(warnings, "["+u+"] "+w)
		}
	}
	return map[string]any{"nodes": dedupeNodes(nodes), "raw": strings.Join(rawParts, "\n\n"), "warnings": warnings}
}

func validateSubscriptionURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http(s) allowed, got: %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return nil
		}
		ip = ips[0]
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("private/loopback IP not allowed: %s", ip)
	}
	return nil
}

func shouldKeepNodeByFilter(node map[string]any, mode string, keywords []string) bool {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" || mode == "off" || len(keywords) == 0 {
		return true
	}
	tag := strings.ToLower(strings.TrimSpace(mustStr(node["tag"])))
	matched := false
	for _, kw := range keywords {
		if kw != "" && strings.Contains(tag, kw) {
			matched = true
			break
		}
	}
	if mode == "whitelist" {
		return matched
	}
	if mode == "blacklist" {
		return !matched
	}
	return true
}

type parseResult struct {
	nodes    []map[string]any
	warnings []string
}

var subscriptionLinkRe = regexp.MustCompile(`(?i)(vmess|vless|trojan|ss|socks5|socks|tuic|hysteria2)://[^\s"'<>]+`)

func parseSubscription(raw string) parseResult {
	txt := strings.TrimSpace(raw)
	txt = decodeMaybeBase64Subscription(txt)
	lines := extractSubscriptionLines(txt)
	out := parseResult{nodes: []map[string]any{}, warnings: []string{}}
	for _, line := range lines {
		line = sanitizeSubscriptionLine(line)
		if line == "" {
			continue
		}
		node, err := parseNodeLine(line)
		if err != nil {
			if looksLikeSubscriptionPayload(line) {
				out.warnings = append(out.warnings, "节点解析失败: "+err.Error())
			}
			continue
		}
		out.nodes = append(out.nodes, node)
	}
	return out
}

func parseManualNodeInput(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{"nodes": []any{}, "warnings": []any{"手动导入内容为空"}}
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			arr := []any{}
			switch t := v.(type) {
			case []any:
				arr = t
			default:
				arr = []any{t}
			}
			nodes := []any{}
			warnings := []any{}
			for _, it := range arr {
				m, ok := it.(map[string]any)
				if !ok {
					warnings = append(warnings, "结构化节点解析失败: 节点必须是对象")
					continue
				}
				if r, ok := m["raw"].(string); ok && strings.TrimSpace(r) != "" {
					n, err := parseNodeLine(strings.TrimSpace(r))
					if err != nil {
						warnings = append(warnings, "结构化节点解析失败: "+err.Error())
						continue
					}
					nodes = append(nodes, n)
					continue
				}
				nodes = append(nodes, m)
			}
			return map[string]any{"nodes": nodes, "warnings": warnings}
		}
	}
	pr := parseSubscription(raw)
	nodes := make([]any, 0, len(pr.nodes))
	for _, n := range pr.nodes {
		nodes = append(nodes, n)
	}
	ws := make([]any, 0, len(pr.warnings))
	for _, w := range pr.warnings {
		ws = append(ws, w)
	}
	return map[string]any{"nodes": nodes, "warnings": ws}
}

func parseNodeLine(line string) (map[string]any, error) {
	line = sanitizeSubscriptionLine(line)
	lower := strings.ToLower(line)
	switch {
	case strings.HasPrefix(lower, "vless://"), strings.HasPrefix(lower, "trojan://"), strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "tuic://"), strings.HasPrefix(lower, "socks5://"), strings.HasPrefix(lower, "socks://"):
		u, err := url.Parse(line)
		if err != nil {
			return nil, err
		}
		tag := strings.TrimPrefix(u.Fragment, "#")
		if d, err := url.QueryUnescape(tag); err == nil {
			tag = d
		}
		if tag == "" {
			tag = u.Host
		}
		node := map[string]any{"tag": tag, "server": u.Hostname(), "server_port": mustAtoiDefault(u.Port(), 443)}
		switch u.Scheme {
		case "vless":
			node["type"] = "vless"
			node["uuid"] = u.User.Username()
			if flow := strings.TrimSpace(u.Query().Get("flow")); flow != "" {
				node["flow"] = flow
			}
			if tls := buildTLSFromURL(u); tls != nil {
				node["tls"] = tls
			}
			if transport := buildTransportFromURL(u); transport != nil {
				node["transport"] = transport
			}
		case "trojan":
			node["type"] = "trojan"
			node["password"] = u.User.Username()
			if tls := buildTLSFromURL(u); tls != nil {
				node["tls"] = tls
			}
		case "hysteria2":
			node["type"] = "hysteria2"
			node["password"] = firstNonEmpty(u.User.Username(), u.Query().Get("auth"), u.Query().Get("password"), u.Query().Get("token"))
			if tls := buildTLSFromURL(u); tls != nil {
				node["tls"] = tls
			}
			if up := firstNonEmpty(u.Query().Get("upmbps"), u.Query().Get("up_mbps"), u.Query().Get("up")); strings.TrimSpace(up) != "" {
				node["up_mbps"] = parseRateMbps(up)
			}
			if down := firstNonEmpty(u.Query().Get("downmbps"), u.Query().Get("down_mbps"), u.Query().Get("down")); strings.TrimSpace(down) != "" {
				node["down_mbps"] = parseRateMbps(down)
			}
			obfsType := firstNonEmpty(u.Query().Get("obfs"), u.Query().Get("obfs-type"), u.Query().Get("obfsType"))
			obfsPassword := firstNonEmpty(u.Query().Get("obfs-password"), u.Query().Get("obfsPassword"), u.Query().Get("salamander"))
			if strings.TrimSpace(obfsType) != "" {
				node["obfs"] = map[string]any{"type": strings.TrimSpace(obfsType), "password": strings.TrimSpace(obfsPassword)}
			}
		case "tuic":
			node["type"] = "tuic"
			node["uuid"] = u.User.Username()
			p, _ := u.User.Password()
			node["password"] = p
			tls := buildTLSFromURL(u)
			if tls == nil {
				tls = map[string]any{"enabled": true, "server_name": u.Hostname(), "insecure": false}
			}
			if alpn := strings.TrimSpace(u.Query().Get("alpn")); alpn != "" {
				tls["alpn"] = splitCSV(alpn)
			}
			node["tls"] = tls
			if cc := strings.TrimSpace(u.Query().Get("congestion_control")); cc != "" {
				node["congestion_control"] = cc
			} else {
				node["congestion_control"] = "bbr"
			}
			if z := strings.TrimSpace(firstNonEmpty(u.Query().Get("zero_rtt_handshake"), u.Query().Get("0rtt"))); z != "" {
				node["zero_rtt_handshake"] = z == "1" || strings.EqualFold(z, "true") || strings.EqualFold(z, "yes")
			}
		default:
			node["type"] = "socks"
			node["server_port"] = mustAtoiDefault(u.Port(), 1080)
			node["username"] = u.User.Username()
			p, _ := u.User.Password()
			node["password"] = p
		}
		return node, nil
	case strings.HasPrefix(lower, "vmess://"):
		s := strings.TrimPrefix(line, "vmess://")
		b, err := base64.StdEncoding.DecodeString(padBase64(s))
		if err != nil {
			return nil, err
		}
		var v map[string]any
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		node := map[string]any{"type": "vmess", "tag": getString(v, "ps", "vmess"), "server": getString(v, "add", ""), "server_port": mustAtoiDefault(getString(v, "port", "0"), 0), "uuid": getString(v, "id", "")}
		if scy := strings.TrimSpace(getString(v, "scy", "")); scy != "" {
			node["security"] = scy
		} else {
			node["security"] = "auto"
		}
		node["alter_id"] = mustAtoiDefault(getString(v, "aid", "0"), 0)
		if strings.EqualFold(getString(v, "tls", ""), "tls") {
			tls := map[string]any{"enabled": true, "server_name": firstNonEmpty(getString(v, "sni", ""), getString(v, "host", ""), getString(v, "add", ""))}
			if getString(v, "allowInsecure", "") == "1" {
				tls["insecure"] = true
			}
			node["tls"] = tls
		}
		if tr := buildVmessTransport(v); tr != nil {
			node["transport"] = tr
		}
		return node, nil
	case strings.HasPrefix(lower, "ss://"):
		s := strings.TrimPrefix(line, "ss://")
		parts := strings.SplitN(s, "#", 2)
		main := parts[0]
		tag := "shadowsocks"
		if len(parts) == 2 {
			tag, _ = url.QueryUnescape(parts[1])
		}
		if !strings.Contains(main, "@") {
			dec, err := base64.StdEncoding.DecodeString(padBase64(main))
			if err == nil {
				main = string(dec)
			}
		} else {
			parts2 := strings.SplitN(main, "@", 2)
			if len(parts2) == 2 {
				if dec, err := base64.StdEncoding.DecodeString(padBase64(parts2[0])); err == nil {
					if strings.Contains(string(dec), ":") {
						main = string(dec) + "@" + parts2[1]
					}
				}
			}
		}
		u, err := url.Parse("ss://" + main)
		if err != nil {
			return nil, err
		}
		pwd, _ := u.User.Password()
		return map[string]any{"type": "shadowsocks", "tag": tag, "server": u.Hostname(), "server_port": mustAtoiDefault(u.Port(), 0), "method": u.User.Username(), "password": pwd}, nil
	default:
		return nil, fmt.Errorf("不支持的协议")
	}
}

func buildTLSFromURL(u *url.URL) map[string]any {
	q := u.Query()
	security := strings.TrimSpace(q.Get("security"))
	isTLS := u.Scheme == "trojan" || q.Get("tls") == "1" || strings.EqualFold(security, "tls") || strings.EqualFold(security, "reality")
	if !isTLS {
		return nil
	}
	fingerprint := firstNonEmpty(q.Get("fp"), q.Get("fingerprint"), q.Get("client-fingerprint"))
	if fingerprint == "" && strings.EqualFold(security, "reality") {
		fingerprint = "chrome"
	}
	tls := map[string]any{
		"enabled":     true,
		"server_name": firstNonEmpty(q.Get("sni"), u.Hostname()),
		"insecure":    q.Get("allowInsecure") == "1",
	}
	if fingerprint != "" && u.Scheme != "hysteria2" && u.Scheme != "tuic" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
	}
	if strings.EqualFold(security, "reality") {
		tls["reality"] = map[string]any{
			"enabled":    true,
			"public_key": emptyToNil(q.Get("pbk")),
			"short_id":   emptyToNil(q.Get("sid")),
		}
	}
	if u.Scheme == "hysteria2" || u.Scheme == "tuic" {
		tls["alpn"] = []any{"h3"}
	}
	return tls
}

func buildTransportFromURL(u *url.URL) map[string]any {
	t := strings.TrimSpace(u.Query().Get("type"))
	if t == "" || t == "tcp" {
		return nil
	}
	q := u.Query()
	switch t {
	case "ws":
		tr := map[string]any{"type": "ws", "path": firstNonEmpty(q.Get("path"), "/")}
		if host := strings.TrimSpace(q.Get("host")); host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		return tr
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
	case "http":
		tr := map[string]any{"type": "http", "path": firstNonEmpty(q.Get("path"), "/")}
		if host := strings.TrimSpace(q.Get("host")); host != "" {
			tr["host"] = []any{host}
		}
		return tr
	default:
		return map[string]any{"type": t}
	}
}

func buildVmessTransport(v map[string]any) map[string]any {
	netType := strings.TrimSpace(getString(v, "net", ""))
	switch netType {
	case "ws":
		tr := map[string]any{"type": "ws", "path": firstNonEmpty(getString(v, "path", ""), "/")}
		if host := strings.TrimSpace(getString(v, "host", "")); host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		return tr
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": getString(v, "path", "")}
	case "http":
		tr := map[string]any{"type": "http", "path": firstNonEmpty(getString(v, "path", ""), "/")}
		if host := strings.TrimSpace(getString(v, "host", "")); host != "" {
			tr["host"] = []any{host}
		}
		return tr
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func splitCSV(v string) []any {
	parts := strings.Split(v, ",")
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		s := strings.TrimSpace(part)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parseRateMbps(v string) int {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimSuffix(v, "mbps")
	v = strings.TrimSuffix(v, "m")
	v = strings.TrimSpace(v)
	return mustAtoiDefault(v, 0)
}

func emptyToNil(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}

func sanitizeSubscriptionLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "\uFEFF")
	line = strings.TrimLeft(line, "`'\"[{(")
	line = strings.TrimRight(line, "`'\"]})],;")
	line = strings.ReplaceAll(line, "&amp;", "&")
	line = strings.Join(strings.Fields(line), "")
	return line
}

func extractSubscriptionLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		clean := sanitizeSubscriptionLine(line)
		if clean == "" {
			continue
		}
		matches := subscriptionLinkRe.FindAllString(clean, -1)
		if len(matches) > 0 {
			out = append(out, matches...)
			continue
		}
		nested := decodeBase64Line(clean)
		if nested != "" {
			nestedLines := strings.Split(strings.ReplaceAll(nested, "\r\n", "\n"), "\n")
			for _, nl := range nestedLines {
				nl = sanitizeSubscriptionLine(nl)
				if nl == "" {
					continue
				}
				nm := subscriptionLinkRe.FindAllString(nl, -1)
				if len(nm) > 0 {
					out = append(out, nm...)
				}
			}
			continue
		}
		out = append(out, clean)
	}
	return out
}

func decodeMaybeBase64Subscription(text string) string {
	clean := strings.TrimSpace(text)
	if subscriptionLinkRe.MatchString(clean) {
		return clean
	}
	n := normalizeBase64(clean)
	if n == "" {
		return clean
	}
	b, err := base64.StdEncoding.DecodeString(n)
	if err != nil {
		return clean
	}
	decoded := strings.TrimSpace(string(b))
	if subscriptionLinkRe.MatchString(decoded) {
		return decoded
	}
	return clean
}

func decodeBase64Line(line string) string {
	n := normalizeBase64(line)
	if n == "" {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(n)
	if err != nil {
		return ""
	}
	decoded := strings.TrimSpace(string(b))
	if subscriptionLinkRe.MatchString(decoded) {
		return decoded
	}
	return ""
}

func normalizeBase64(value string) string {
	compact := strings.Join(strings.Fields(value), "")
	if len(compact) < 16 {
		return ""
	}
	for _, ch := range compact {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '/' || ch == '_' || ch == '+' || ch == '=' || ch == '-') {
			return ""
		}
	}
	base := strings.ReplaceAll(strings.ReplaceAll(compact, "-", "+"), "_", "/")
	if len(base)%4 == 1 {
		return ""
	}
	for len(base)%4 != 0 {
		base += "="
	}
	return base
}

func looksLikeSubscriptionPayload(line string) bool {
	if subscriptionLinkRe.MatchString(line) {
		return true
	}
	if len(line) < 16 {
		return false
	}
	for _, ch := range line {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '/' || ch == '_' || ch == '+' || ch == '=' || ch == '-') {
			return false
		}
	}
	return true
}

func buildSingBoxConfig(cfg, sub map[string]any) map[string]any {
	nr := getMap(cfg, "nodeRegistry")
	nodes := []any{}
	nodes = append(nodes, getSlice(sub, "nodes")...)
	nodes = append(nodes, getSlice(nr, "manualNodes")...)

	outbounds := []any{map[string]any{"type": "direct", "tag": "direct"}, map[string]any{"type": "block", "tag": "block"}}
	normalizedNodeMap := map[string]map[string]any{}
	tags := []string{}
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		normalized := normalizeOutboundForSingBox(m)
		if normalized == nil {
			continue
		}
		outbounds = append(outbounds, normalized)
		normalizedNodeMap[mustStr(normalized["tag"])] = normalized
		m = normalized
		if t := mustStr(m["tag"]); t != "" {
			tags = append(tags, t)
		}
	}

	groupTags := []string{}
	for _, g := range getSlice(nr, "groups") {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		tag := strings.TrimSpace(mustStr(gm["tag"]))
		if tag == "" {
			continue
		}
		members := []string{}
		for _, m := range getSlice(gm, "members") {
			mtag := strings.TrimSpace(mustStr(m))
			if mtag == "" {
				continue
			}
			if _, ok := normalizedNodeMap[mtag]; ok {
				members = append(members, mtag)
			}
		}
		if len(members) == 0 {
			continue
		}
		strategy := strings.TrimSpace(mustStr(gm["strategy"]))
		if strategy == "fallback" {
			outbounds = append(outbounds, map[string]any{
				"type":                        "selector",
				"tag":                         tag,
				"outbounds":                   toAnySliceString(members),
				"default":                     members[0],
				"interrupt_exist_connections": false,
			})
		} else {
			url := strings.TrimSpace(mustStr(gm["url"]))
			if url == "" {
				url = "https://www.gstatic.com/generate_204"
			}
			interval := strings.TrimSpace(mustStr(gm["interval"]))
			if interval == "" {
				interval = "10m"
			}
			outbounds = append(outbounds, map[string]any{
				"type":      "urltest",
				"tag":       tag,
				"outbounds": toAnySliceString(members),
				"url":       url,
				"interval":  interval,
				"tolerance": 50,
			})
		}
		groupTags = append(groupTags, tag)
	}

	chainTags := []string{}
	for _, c := range getSlice(nr, "chains") {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		chainTag := strings.TrimSpace(mustStr(cm["tag"]))
		if chainTag == "" {
			continue
		}
		members := []string{}
		for _, m := range getSlice(cm, "members") {
			mtag := strings.TrimSpace(mustStr(m))
			if _, ok := normalizedNodeMap[mtag]; ok {
				members = append(members, mtag)
			}
		}
		if len(members) == 0 {
			continue
		}
		previous := ""
		for i, memberTag := range members {
			base := normalizeOutboundForSingBox(normalizedNodeMap[memberTag])
			if base == nil {
				continue
			}
			hopTag := fmt.Sprintf("%s__hop_%d", chainTag, i+1)
			base["tag"] = hopTag
			if previous != "" {
				base["detour"] = previous
			}
			outbounds = append(outbounds, base)
			previous = hopTag
		}
		if previous != "" {
			outbounds = append(outbounds, map[string]any{
				"type":                        "selector",
				"tag":                         chainTag,
				"outbounds":                   []any{previous},
				"default":                     previous,
				"interrupt_exist_connections": false,
			})
			chainTags = append(chainTags, chainTag)
		}
	}

	tags = append(tags, groupTags...)
	tags = append(tags, chainTags...)
	if len(tags) > 0 {
		outbounds = append(outbounds, map[string]any{"type": "selector", "tag": "proxy", "outbounds": tags, "default": tags[0]})
		outbounds = append(outbounds, map[string]any{"type": "urltest", "tag": "auto", "outbounds": tags, "url": "https://www.gstatic.com/generate_204", "interval": "10m", "tolerance": 50})
	}

	inbounds := []any{}
	routeRules := []any{}
	for _, p := range getSlice(cfg, "ports") {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		inboundTag := strings.TrimSpace(mustStr(pm["tag"]))
		if inboundTag == "" {
			continue
		}
		// 端口协议：socks（默认，向后兼容）或 http。
		// 同一份用户列表既适用 SOCKS5 鉴权也适用 HTTP Basic 鉴权（sing-box 协议层差异透明）。
		protocol := strings.ToLower(strings.TrimSpace(mustStr(pm["protocol"])))
		switch protocol {
		case "http":
			// keep
		case "", "socks", "socks5":
			protocol = "socks"
		default:
			protocol = "socks"
		}
		inbound := map[string]any{
			"type":        protocol,
			"tag":         inboundTag,
			"listen":      mustStr(pm["listen"]),
			"listen_port": int(toFloat(pm["port"])),
		}
		// 端口鉴权（可选）。
		// users 来自 ports[].users，格式: [{"username":"u","password":"p"}, ...]
		// 同一字段既适用于 SOCKS5 用户名密码也适用于 HTTP Basic 鉴权。
		users := []any{}
		for _, u := range getSlice(pm, "users") {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			username := strings.TrimSpace(mustStr(um["username"]))
			password := strings.TrimSpace(mustStr(um["password"]))
			if username == "" || password == "" {
				continue
			}
			users = append(users, map[string]any{"username": username, "password": password})
		}
		if len(users) > 0 {
			inbound["users"] = users
		}
		inbounds = append(inbounds, inbound)

		target := strings.TrimSpace(mustStr(pm["target"]))
		if target == "" {
			target = "proxy"
		}
		routeRules = append(routeRules, map[string]any{
			"inbound":  []any{inboundTag},
			"outbound": target,
		})
	}

	dnsCfg := getMap(cfg, "dns")
	routing := getMap(cfg, "routing")
	return map[string]any{
		"log":          map[string]any{"level": getString(getMap(cfg, "app"), "logLevel", "info"), "timestamp": true},
		"dns":          map[string]any{"servers": []any{map[string]any{"tag": "dns-remote-default", "type": "https", "server": "cloudflare-dns.com", "path": "/dns-query", "detour": "proxy"}, map[string]any{"tag": "dns-bootstrap", "type": "udp", "server": getString(dnsCfg, "bootstrapServer", "1.1.1.1"), "server_port": 53}, map[string]any{"tag": "dns-direct", "type": "local"}}, "rules": []any{map[string]any{"clash_mode": "Direct", "server": "dns-direct"}, map[string]any{"server": "dns-remote-default"}}, "final": "dns-remote-default", "strategy": getString(dnsCfg, "strategy", "prefer_ipv4")},
		"inbounds":     inbounds,
		"outbounds":    outbounds,
		"route":        map[string]any{"auto_detect_interface": true, "final": getString(routing, "routeFinal", "proxy"), "default_domain_resolver": map[string]any{"server": "dns-bootstrap", "strategy": getString(dnsCfg, "strategy", "prefer_ipv4")}, "rules": routeRules},
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": true, "path": "cache.db", "store_rdrc": true, "store_fakeip": true}, "clash_api": map[string]any{"external_controller": "127.0.0.1:19090", "external_ui": "", "secret": ""}},
	}
}

func toAnySliceString(in []string) []any {
	out := make([]any, 0, len(in))
	for _, item := range in {
		out = append(out, item)
	}
	return out
}

func normalizeOutboundForSingBox(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	cloned := cloneMap(node)
	t := mustStr(cloned["type"])
	if t == "" || mustStr(cloned["tag"]) == "" {
		return nil
	}

	if t == "hysteria2" || t == "tuic" {
		tls, _ := cloned["tls"].(map[string]any)
		if tls == nil {
			tls = map[string]any{}
		}
		tls["enabled"] = true
		if strings.TrimSpace(mustStr(tls["server_name"])) == "" {
			tls["server_name"] = mustStr(cloned["server"])
		}
		if _, ok := tls["insecure"]; !ok {
			tls["insecure"] = false
		}
		if _, ok := tls["alpn"]; !ok {
			tls["alpn"] = []any{"h3"}
		}
		cloned["tls"] = tls
	}

	if t == "vless" || t == "trojan" || t == "vmess" || t == "hysteria2" || t == "tuic" || t == "shadowsocks" || t == "socks" {
		if strings.TrimSpace(mustStr(cloned["server"])) == "" || int(toFloat(cloned["server_port"])) <= 0 {
			return nil
		}
	}

	if t == "vless" && strings.TrimSpace(mustStr(cloned["uuid"])) == "" {
		return nil
	}
	if t == "trojan" && strings.TrimSpace(mustStr(cloned["password"])) == "" {
		return nil
	}
	if t == "hysteria2" && strings.TrimSpace(mustStr(cloned["password"])) == "" {
		return nil
	}
	if t == "tuic" && (strings.TrimSpace(mustStr(cloned["uuid"])) == "" || strings.TrimSpace(mustStr(cloned["password"])) == "") {
		return nil
	}
	if t == "shadowsocks" && (strings.TrimSpace(mustStr(cloned["method"])) == "" || strings.TrimSpace(mustStr(cloned["password"])) == "") {
		return nil
	}

	return cloned
}

func cloneMap(in map[string]any) map[string]any {
	b, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if json.Unmarshal(b, &out) != nil {
		return map[string]any{}
	}
	return out
}

func collectOutbounds(cfg, sub map[string]any) []any {
	nr := getMap(cfg, "nodeRegistry")
	groups := []any{}
	for _, g := range getSlice(nr, "groups") {
		if m, ok := g.(map[string]any); ok {
			groups = append(groups, map[string]any{"tag": mustStr(m["tag"]), "type": mustStr(m["strategy"]), "source": "group", "label": fmt.Sprintf("%s（%s / 节点组）", mustStr(m["tag"]), mustStr(m["strategy"]))})
		}
	}
	chains := []any{}
	for _, c := range getSlice(nr, "chains") {
		if m, ok := c.(map[string]any); ok {
			chains = append(chains, map[string]any{"tag": mustStr(m["tag"]), "type": "chain", "source": "chain", "label": fmt.Sprintf("%s（chain / 链式代理）", mustStr(m["tag"]))})
		}
	}
	manualNodes := []any{}
	for _, n := range getSlice(nr, "manualNodes") {
		if m, ok := n.(map[string]any); ok {
			manualNodes = append(manualNodes, map[string]any{"tag": mustStr(m["tag"]), "type": mustStr(m["type"]), "source": "manual", "label": fmt.Sprintf("%s（%s / 手动）", mustStr(m["tag"]), mustStr(m["type"]))})
		}
	}
	subscriptionNodes := []any{}
	for _, n := range getSlice(sub, "nodes") {
		if m, ok := n.(map[string]any); ok {
			subscriptionNodes = append(subscriptionNodes, map[string]any{"tag": mustStr(m["tag"]), "type": mustStr(m["type"]), "source": "subscription", "label": fmt.Sprintf("%s（%s / 订阅）", mustStr(m["tag"]), mustStr(m["type"]))})
		}
	}
	builtins := []any{
		map[string]any{"tag": "proxy", "type": "selector", "source": "builtin", "label": "proxy（自动选择）"},
		map[string]any{"tag": "auto", "type": "urltest", "source": "builtin", "label": "auto（延迟测试）"},
		map[string]any{"tag": "block", "type": "block", "source": "builtin", "label": "block"},
	}
	return append(append(append(append(groups, chains...), manualNodes...), subscriptionNodes...), builtins...)
}

func defaultConfig() map[string]any {
	exe := filepath.ToSlash(filepath.Join("internal", "bin", map[bool]string{true: "sing-box.exe", false: "sing-box"}[runtime.GOOS == "windows"]))
	return map[string]any{
		"app":          map[string]any{"host": "127.0.0.1", "port": 18080, "singBoxBinary": exe, "autoStart": false, "autoConfigureOnSubscription": false, "logLevel": "info"},
		"subscription": map[string]any{"url": "", "urls": []any{}, "format": "raw", "userAgent": "sub2socks5/0.1.0", "refreshIntervalMinutes": 60, "headers": map[string]any{}},
		"dns":          map[string]any{"strategy": "prefer_ipv4", "remotePreset": "cloudflare", "remoteUrl": "https://cloudflare-dns.com/dns-query", "bootstrapServer": "1.1.1.1"},
		"routing":      map[string]any{"routeFinal": "proxy", "autoDetectInterface": true, "ruleSetUrls": []any{}, "rules": []any{map[string]any{"action": "sniff"}}},
		"nodeRegistry": map[string]any{"manualNodes": []any{}, "groups": []any{}, "chains": []any{}, "disabledSubscriptionTags": []any{}},
		"runtimeState": map[string]any{"fallbackGroups": map[string]any{}},
		"ports":        []any{map[string]any{"tag": "default-socks", "listen": "127.0.0.1", "port": 18081, "target": "proxy", "sniff": true}},
	}
}

// findPort 扫描可用端口。注意：存在 TOCTOU 竞态（返回后到实际 bind 之间可能被占用）。
// 实际使用时应由 sing-box 进程自己 bind，失败时重试或报错。
func findPort(host string, start int) int {
	for p := start; p <= 65535; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p))
		if err == nil {
			_ = l.Close()
			return p
		}
	}
	return start
}

func (a *App) handleConfigPatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		methodNotAllowed(w, "PATCH, POST")
		return
	}
	var patch map[string]any
	if err := decodeJSON(r.Body, &patch); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	a.mu.Lock()
	beforeHash := configHashOf(a.cfg)
	merged := mergePatch(a.cfg, patch)
	a.cfg = merged
	_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
	generated := buildSingBoxConfig(a.cfg, a.subState)
	_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
	afterHash := configHashOf(a.cfg)
	restarted := false
	if afterHash != beforeHash && a.proc != nil && a.proc.Process != nil {
		if err := a.startRuntimeLocked(); err != nil {
			a.appendRuntimeLog("apply config patch failed: " + err.Error())
			a.mu.Unlock()
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		restarted = true
		a.appendRuntimeLog("config patched and runtime restarted")
	}
	state := a.runtimeStateLocked()
	a.mu.Unlock()
	ok(w, map[string]any{
		"config":     a.cfg,
		"configHash": afterHash,
		"restarted":  restarted,
		"runtime":    state,
	})
}

func (a *App) handleServicesList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.mu.RLock()
		defer a.mu.RUnlock()
		ports := getSlice(a.cfg, "ports")
		services := make([]map[string]any, 0, len(ports))
		for _, p := range ports {
			if pm, ok := p.(map[string]any); ok {
				services = append(services, serviceWithID(pm))
			}
		}
		ok(w, map[string]any{"services": services})
	case http.MethodPost:
		var body map[string]any
		if err := decodeJSON(r.Body, &body); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		ports := getSlice(a.cfg, "ports")
		listen := strings.TrimSpace(mustStr(body["listen"]))
		if listen == "" {
			listen = "0.0.0.0"
		}
		port := int(toFloat(body["port"]))
		if port <= 0 {
			port = a.nextAvailableSocksPort(18081, 18100)
		}
		if port == 0 {
			fail(w, http.StatusBadRequest, "无可用端口（18081-18100 已用尽）")
			return
		}
		target := strings.TrimSpace(mustStr(body["target"]))
		if target == "" {
			target = "proxy"
		}
		tag := strings.TrimSpace(mustStr(body["tag"]))
		if tag == "" {
			tag = fmt.Sprintf("socks-%d", port)
		}
		svc := map[string]any{
			"tag":      tag,
			"listen":   listen,
			"port":     port,
			"target":   target,
			"protocol": normalizeServiceProtocol(mustStr(body["protocol"])),
			"sniff":    true,
			"enabled":  true,
			"users":    normalizeServiceUsers(getSlice(body, "users")),
		}
		ports = append(ports, svc)
		a.cfg["ports"] = ports
		_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
		generated := buildSingBoxConfig(a.cfg, a.subState)
		_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
		if a.proc != nil && a.proc.Process != nil {
			_ = a.startRuntimeLocked()
		}
		ok(w, map[string]any{"service": serviceWithID(svc), "configHash": configHashOf(a.cfg)})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (a *App) handleServicesItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/services/")
	if id == "" || strings.Contains(id, "/") {
		fail(w, http.StatusBadRequest, "Invalid service id")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	ports := getSlice(a.cfg, "ports")
	idx := -1
	for i, p := range ports {
		if pm, ok := p.(map[string]any); ok {
			if mustStr(pm["tag"]) == id {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		fail(w, http.StatusNotFound, "Service not found")
		return
	}
	switch r.Method {
	case http.MethodPut, http.MethodPatch:
		var body map[string]any
		if err := decodeJSON(r.Body, &body); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		current, _ := ports[idx].(map[string]any)
		if current == nil {
			current = map[string]any{}
		}
		merged := mergePatch(current, body)
		if _, ok := body["users"]; ok {
			merged["users"] = normalizeServiceUsers(getSlice(body, "users"))
		}
		ports[idx] = merged
		a.cfg["ports"] = ports
		_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
		generated := buildSingBoxConfig(a.cfg, a.subState)
		_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
		if a.proc != nil && a.proc.Process != nil {
			_ = a.startRuntimeLocked()
		}
		ok(w, map[string]any{"service": serviceWithID(merged), "configHash": configHashOf(a.cfg)})
	case http.MethodDelete:
		ports = append(ports[:idx], ports[idx+1:]...)
		a.cfg["ports"] = ports
		_ = writeJSON(filepath.Join(a.dataDir, "app-config.json"), a.cfg)
		generated := buildSingBoxConfig(a.cfg, a.subState)
		_ = writeJSON(filepath.Join(a.runtimeDir, "sing-box.json"), generated)
		if a.proc != nil && a.proc.Process != nil {
			_ = a.startRuntimeLocked()
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w, "PUT, PATCH, DELETE")
	}
}

func serviceWithID(svc map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range svc {
		out[k] = v
	}
	out["id"] = mustStr(svc["tag"])
	return out
}

// normalizeServiceProtocol 将端口协议字段归一化为 sing-box inbound type。
// 接受 socks/socks5（→ socks，向后兼容默认）或 http；其他值回落 socks。
func normalizeServiceProtocol(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "http":
		return "http"
	default:
		return "socks"
	}
}

// normalizeServiceUsers 校验并规范化 SOCKS5 inbound 用户列表。
// 入参形如 [{"username":"u","password":"p"}, ...]，丢弃任一字段为空的项。
// 始终返回非 nil slice（[]any{}），便于持久化到 JSON。
func normalizeServiceUsers(raw []any) []any {
	out := []any{}
	if raw == nil {
		return out
	}
	seen := map[string]bool{}
	for _, u := range raw {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		username := strings.TrimSpace(mustStr(um["username"]))
		password := strings.TrimSpace(mustStr(um["password"]))
		if username == "" || password == "" {
			continue
		}
		if seen[username] {
			continue
		}
		seen[username] = true
		out = append(out, map[string]any{"username": username, "password": password})
	}
	return out
}

func (a *App) handleRuntimeState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	ok(w, a.runtimeStateLocked())
}

func (a *App) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	issues := []map[string]any{}
	bannerHints := []map[string]any{}
	seenTag := map[string]bool{}
	seenListenPort := map[string]bool{}
	inDocker := runningInContainer()
	authEnabled := strings.TrimSpace(os.Getenv("SUB2SOCKS5_PASSWORD")) != ""
	availableTags := map[string]bool{}
	for _, ob := range collectOutbounds(a.cfg, a.subState) {
		if obm, ok := ob.(map[string]any); ok {
			availableTags[mustStr(obm["tag"])] = true
		}
	}

	// 横幅级 1：公网/局域网部署但鉴权未启用。
	host := getString(getMap(a.cfg, "app"), "host", "0.0.0.0")
	if env := strings.TrimSpace(os.Getenv("SUB2SOCKS5_HOST")); env != "" {
		host = env
	}
	if !authEnabled && (inDocker || host == "0.0.0.0" || host == "::") {
		bannerHints = append(bannerHints, map[string]any{
			"code":     "auth_disabled_on_public_bind",
			"severity": "danger",
			"message":  "Web UI 监听非本地地址但未启用鉴权，建议立即设置 SUB2SOCKS5_PASSWORD",
			"hint":     "设置环境变量 SUB2SOCKS5_USERNAME / SUB2SOCKS5_PASSWORD 后重启容器；或将 WEBUI_BIND 改回 127.0.0.1",
		})
	}

	// 横幅级 2：HTTP inbound 暴露公网但凭据为空。
	httpInboundsExposed := []string{}
	for _, p := range getSlice(a.cfg, "ports") {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(mustStr(pm["protocol"])))
		if protocol != "http" {
			continue
		}
		listen := mustStr(pm["listen"])
		users := getSlice(pm, "users")
		hasCreds := false
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(mustStr(um["username"])) != "" && strings.TrimSpace(mustStr(um["password"])) != "" {
				hasCreds = true
				break
			}
		}
		if !hasCreds && (listen == "0.0.0.0" || listen == "::" || inDocker) {
			httpInboundsExposed = append(httpInboundsExposed, mustStr(pm["tag"]))
		}
	}
	if len(httpInboundsExposed) > 0 {
		bannerHints = append(bannerHints, map[string]any{
			"code":     "http_inbound_no_credentials",
			"severity": "danger",
			"message":  "HTTP 代理端口未设置用户名密码：" + strings.Join(httpInboundsExposed, ", "),
			"hint":     "在「编辑服务」中为 HTTP 端口添加 users 凭据，否则任何外部用户都可以滥用代理",
		})
	}

	for i, p := range getSlice(a.cfg, "ports") {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		path := fmt.Sprintf("/ports/%d", i)
		tag := mustStr(pm["tag"])
		listen := mustStr(pm["listen"])
		port := getInt(pm, "port", 0)
		target := mustStr(pm["target"])
		if tag == "" || seenTag[tag] {
			issues = append(issues, map[string]any{
				"code": "duplicate_or_empty_tag", "severity": "error",
				"path": path + "/tag", "message": "服务 tag 重复或为空", "autoFix": "rename",
			})
		}
		seenTag[tag] = true
		key := fmt.Sprintf("%s:%d", listen, port)
		if port < 1 || port > 65535 || seenListenPort[key] {
			issues = append(issues, map[string]any{
				"code": "invalid_or_duplicate_port", "severity": "error",
				"path": path + "/port", "message": "端口非法或重复", "autoFix": "autoPickPort",
			})
		}
		seenListenPort[key] = true
		if target != "" && !availableTags[target] {
			issues = append(issues, map[string]any{
				"code": "missing_target", "severity": "error",
				"path": path + "/target", "message": "出口节点 " + target + " 不存在", "autoFix": "setTarget:proxy",
			})
		}
		if listen == "127.0.0.1" && inDocker {
			issues = append(issues, map[string]any{
				"code": "listen_unreachable_in_docker", "severity": "warning",
				"path": path + "/listen", "message": "Docker 容器内监听 127.0.0.1，外部不可达", "autoFix": "setListen:0.0.0.0",
			})
		}
		if inDocker && (port < 18081 || port > 18100) {
			issues = append(issues, map[string]any{
				"code": "port_outside_published_range", "severity": "warning",
				"path": path + "/port", "message": fmt.Sprintf("端口 %d 不在 compose 默认发布范围 18081-18100", port),
			})
		}
	}
	// 横幅级 3：存在端口超出 compose 发布范围。
	if inDocker {
		outOfRange := []string{}
		for _, p := range getSlice(a.cfg, "ports") {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			port := getInt(pm, "port", 0)
			if port < 18081 || port > 18100 {
				outOfRange = append(outOfRange, fmt.Sprintf("%s:%d", mustStr(pm["tag"]), port))
			}
		}
		if len(outOfRange) > 0 {
			bannerHints = append(bannerHints, map[string]any{
				"code":     "port_outside_compose_range",
				"severity": "warning",
				"message":  "以下端口在容器内监听但 compose 未发布：" + strings.Join(outOfRange, ", "),
				"hint":     "编辑 docker-compose.yml ports 字段，扩展 18081-18100 区间或将端口改回该范围内",
			})
		}
	}

	if !getBool(a.kernelStatus(), "installed", false) {
		issues = append(issues, map[string]any{
			"code": "kernel_missing", "severity": "error",
			"path": "/kernel", "message": "sing-box 内核未安装", "autoFix": "downloadKernel",
		})
		bannerHints = append(bannerHints, map[string]any{
			"code":     "kernel_missing",
			"severity": "danger",
			"message":  "sing-box 内核未安装，所有代理端口都无法工作",
			"hint":     "进入「设置 → 内核管理」点击「拉取内核」，或挂载 ./bin 目录到容器",
		})
	}
	running := a.proc != nil && a.proc.Process != nil
	if running && configHashOf(a.cfg) != mustStr(a.runtimeInfo["runningConfigHash"]) {
		issues = append(issues, map[string]any{
			"code": "restart_needed", "severity": "warning",
			"path": "/", "message": "运行配置已陈旧，重启 sing-box 后生效", "autoFix": "restartRuntime",
		})
		bannerHints = append(bannerHints, map[string]any{
			"code":     "restart_needed",
			"severity": "info",
			"message":  "配置已更新但 sing-box 仍在运行旧版，需要重启使变更生效",
			"hint":     "点击顶栏运行徽章触发重启，或调用 POST /api/runtime/start",
		})
	}
	ok(w, map[string]any{"issues": issues, "bannerHints": bannerHints, "inDocker": inDocker})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authEnabled := strings.TrimSpace(os.Getenv("SUB2SOCKS5_PASSWORD")) != ""

		w.Header().Set("x-content-type-options", "nosniff")
		w.Header().Set("x-frame-options", "DENY")
		w.Header().Set("referrer-policy", "no-referrer")
		w.Header().Set("cross-origin-resource-policy", "same-origin")
		w.Header().Set("cache-control", "no-store")

		// 鉴权未启用时放开 CORS 方便本地调试；启用时不返回 Allow-Origin，
		// 浏览器同源请求不依赖该头，跨域 fetch 因 SameSite=Strict cookie 注定无会话。
		if !authEnabled {
			w.Header().Set("access-control-allow-origin", "*")
		}

		w.Header().Set("access-control-allow-methods", "GET, POST, PATCH, PUT, DELETE, HEAD, OPTIONS")
		w.Header().Set("access-control-allow-headers", "content-type, authorization")
		w.Header().Set("access-control-allow-credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			fetchSite := r.Header.Get("Sec-Fetch-Site")
			if fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
				http.Error(w, "CSRF check failed", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	sanitized := sanitizeErrorMessage(msg)
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": sanitized, "status": status}})
}

func sanitizeErrorMessage(msg string) string {
	msg = regexp.MustCompile(`[A-Za-z]:\\[^\s]+`).ReplaceAllString(msg, "[path]")
	msg = regexp.MustCompile(`/[a-zA-Z0-9/_.-]+`).ReplaceAllString(msg, "[path]")
	msg = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`).ReplaceAllString(msg, "[ip]")
	msg = regexp.MustCompile(`:[0-9]{2,5}\b`).ReplaceAllString(msg, ":[port]")
	return msg
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	fail(w, 405, "Method Not Allowed")
}

func writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(p)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, p)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func decodeJSON(r io.Reader, v any) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	return json.Unmarshal(b, v)
}

// ===== Phase A: configHash / restart-needed / runtime state / diagnostics =====

// runtimeAffectingPaths 列出会影响 sing-box 行为的顶层 cfg 字段。
// 改动这些 key 时需要重启 sing-box；其他 key（如 app.host/port/theme/logLevel）只持久化不重启。
var runtimeAffectingPaths = []string{"ports", "dns", "routing", "nodeRegistry"}

// configHashOf 计算"运行时相关字段"的 sha256 摘要，用于检测是否需要重启 sing-box。
func configHashOf(cfg map[string]any) string {
	subset := map[string]any{}
	for _, k := range runtimeAffectingPaths {
		if v, ok := cfg[k]; ok {
			subset[k] = v
		}
	}
	b, err := canonicalJSON(subset)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonicalJSON 输出 key 字典序的 JSON，保证哈希稳定。
func canonicalJSON(v any) ([]byte, error) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			vb, err := canonicalJSON(x[k])
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			vb, err := canonicalJSON(item)
			if err != nil {
				return nil, err
			}
			buf.Write(vb)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		return json.Marshal(x)
	}
}

// mergePatch 实现 RFC 7396 JSON Merge Patch：用 patch 浅合并到 base，null 表示删除。
func mergePatch(base, patch map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for k, v := range patch {
		if v == nil {
			delete(base, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			if bm, ok := base[k].(map[string]any); ok {
				base[k] = mergePatch(bm, pm)
				continue
			}
			base[k] = mergePatch(map[string]any{}, pm)
			continue
		}
		base[k] = v
	}
	return base
}

// runtimeStateLocked 返回结构化运行时状态，调用方需持有 a.mu。
func (a *App) runtimeStateLocked() map[string]any {
	state := mustStr(a.runtimeInfo["state"])
	if state == "" {
		state = "stopped"
	}
	return map[string]any{
		"state":             state,
		"running":           getBool(a.runtimeInfo, "running", false),
		"pid":               getInt(a.runtimeInfo, "pid", 0),
		"startedAt":         mustStr(a.runtimeInfo["startedAt"]),
		"configHash":        configHashOf(a.cfg),
		"runningConfigHash": mustStr(a.runtimeInfo["runningConfigHash"]),
		"lastError":         mustStr(a.runtimeInfo["lastError"]),
		"restartCount":      a.autoRestartAttempts,
	}
}

// runningInContainer 通过 /.dockerenv 探测是否运行在 Docker 容器中。
func runningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// nextAvailableSocksPort 在 [start, end] 内挑选一个未被 cfg.ports[] 使用且系统上未被监听的端口。
func (a *App) nextAvailableSocksPort(start, end int) int {
	used := map[int]bool{}
	for _, p := range getSlice(a.cfg, "ports") {
		if pm, ok := p.(map[string]any); ok {
			used[getInt(pm, "port", 0)] = true
		}
	}
	for port := start; port <= end; port++ {
		if used[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	return 0
}

func mergeMap(base, incoming map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range incoming {
		if bm, ok := out[k].(map[string]any); ok {
			if im, ok2 := v.(map[string]any); ok2 {
				out[k] = mergeMap(bm, im)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func getMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}
func getSlice(m map[string]any, key string) []any {
	v, ok := m[key]
	if !ok || v == nil {
		return []any{}
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return []any{}
	}
	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i += 1 {
		out = append(out, rv.Index(i).Interface())
	}
	return out
}
func getString(m map[string]any, key, def string) string {
	s := mustStr(m[key])
	if s == "" {
		return def
	}
	return s
}
func getInt(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	v := int(toFloat(m[key]))
	if v == 0 {
		return def
	}
	return v
}
func getBool(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		if s == "true" || s == "1" || s == "yes" || s == "on" {
			return true
		}
		if s == "false" || s == "0" || s == "no" || s == "off" {
			return false
		}
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return def
}
func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	default:
		return 0
	}
}
func mustStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		s := string(b)
		s = strings.Trim(s, `"`)
		return s
	}
}
func mustAtoiDefault(s string, d int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n == 0 {
		return d
	}
	return n
}
func getStringSlice(m map[string]any, key string) []string {
	out := []string{}
	for _, v := range getSlice(m, key) {
		out = append(out, mustStr(v))
	}
	return out
}
func toStringSet(in []any) map[string]bool {
	out := map[string]bool{}
	for _, v := range in {
		out[mustStr(v)] = true
	}
	return out
}
func padBase64(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "-", "+"), "_", "/")
	for len(s)%4 != 0 {
		s += "="
	}
	return s
}
func tryDecodeBase64Subscription(s string) string {
	if strings.Contains(s, "://") {
		return s
	}
	b, err := base64.StdEncoding.DecodeString(padBase64(s))
	if err != nil {
		return s
	}
	t := strings.TrimSpace(string(b))
	if strings.Contains(t, "://") {
		return t
	}
	return s
}
func dedupeNodes(in []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := []map[string]any{}
	for _, n := range in {
		k := fmt.Sprintf("%s::%s::%s::%v", mustStr(n["type"]), mustStr(n["tag"]), mustStr(n["server"]), n["server_port"])
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return mustStr(out[i]["tag"]) < mustStr(out[j]["tag"]) })
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

var _ = exec.Command
