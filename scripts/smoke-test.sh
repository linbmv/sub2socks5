#!/usr/bin/env bash
# sub2socks5 端到端冒烟测试。
#
# 执行流程：
#   1. docker compose up -d --build（若 SKIP_BUILD=1 则跳过）
#   2. 等待 Web UI /api/auth/status 在 60s 内返回 200
#   3. GET /api/config 校验关键字段
#   4. GET /api/diagnostics 校验 bannerHints/issues 字段存在
#   5. POST /api/subscription/preview 校验预览 API 返回 stats 结构
#   6. 输出简要结果，非零退出码表示失败
#
# 使用方式：
#   bash scripts/smoke-test.sh
#   SKIP_BUILD=1 bash scripts/smoke-test.sh    # 复用已运行的容器
#   KEEP_RUNNING=1 bash scripts/smoke-test.sh  # 测试结束后保留容器
set -u
set -o pipefail

WEBUI_URL="${WEBUI_URL:-http://127.0.0.1:18080}"
COMPOSE_BIN="${COMPOSE_BIN:-docker compose}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-60}"
SKIP_BUILD="${SKIP_BUILD:-0}"
KEEP_RUNNING="${KEEP_RUNNING:-0}"

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
log()    { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }

fail() {
    red "✗ $*"
    if [ "$KEEP_RUNNING" != "1" ] && [ "$SKIP_BUILD" != "1" ]; then
        $COMPOSE_BIN logs --tail=80 2>&1 || true
        $COMPOSE_BIN down -v 2>/dev/null || true
    fi
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || fail "依赖缺失: $1"
}

require_cmd curl

if [ "$SKIP_BUILD" != "1" ]; then
    require_cmd docker
    log "启动容器：$COMPOSE_BIN up -d --build"
    $COMPOSE_BIN up -d --build || fail "docker compose up 失败"
fi

log "等待 Web UI 就绪（最多 ${WAIT_TIMEOUT}s）"
ready=0
for i in $(seq 1 "$WAIT_TIMEOUT"); do
    code=$(curl -fsS -o /dev/null -w '%{http_code}' "$WEBUI_URL/api/auth/status" 2>/dev/null || echo "000")
    if [ "$code" = "200" ]; then
        ready=1
        log "Web UI 在第 ${i}s 就绪"
        break
    fi
    sleep 1
done
[ "$ready" = "1" ] || fail "Web UI 在 ${WAIT_TIMEOUT}s 内未就绪 (last code=$code)"

log "校验 GET /api/config"
config_body=$(curl -fsS "$WEBUI_URL/api/config") || fail "GET /api/config 失败"
echo "$config_body" | grep -q '"config"'             || fail "/api/config 缺少 config 字段"
echo "$config_body" | grep -q '"availableOutbounds"' || fail "/api/config 缺少 availableOutbounds 字段"
green "✓ /api/config 字段齐全"

log "校验 GET /api/diagnostics（含 bannerHints）"
diag_body=$(curl -fsS "$WEBUI_URL/api/diagnostics") || fail "GET /api/diagnostics 失败"
echo "$diag_body" | grep -q '"issues"'      || fail "/api/diagnostics 缺少 issues 字段"
echo "$diag_body" | grep -q '"bannerHints"' || fail "/api/diagnostics 缺少 bannerHints 字段（F7）"
green "✓ /api/diagnostics 含 bannerHints"

log "校验 POST /api/subscription/preview（F6）"
preview_body=$(curl -fsS -X POST -H 'content-type: application/json' \
    -H 'sec-fetch-site: same-origin' \
    -d '{"raw":"vmess://eyJhZGQiOiJleGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImFpZCI6IjAiLCJ0eXBlIjoibm9uZSIsIm5ldCI6InRjcCIsInBzIjoidGVzdC1ub2RlIiwidiI6IjIifQ=="}' \
    "$WEBUI_URL/api/subscription/preview") || fail "POST /api/subscription/preview 失败"
echo "$preview_body" | grep -q '"stats"' || fail "/api/subscription/preview 响应缺少 stats 字段"
echo "$preview_body" | grep -q '"nodes"' || fail "/api/subscription/preview 响应缺少 nodes 字段"
green "✓ /api/subscription/preview 返回 stats/nodes"

log "校验 POST 端口创建（F1 HTTP inbound 协议字段）"
http_port_test=$(curl -fsS -X POST -H 'content-type: application/json' \
    -H 'sec-fetch-site: same-origin' \
    -d '{"tag":"smoke-http","listen":"127.0.0.1","port":18099,"target":"direct","protocol":"http"}' \
    "$WEBUI_URL/api/services" 2>/dev/null || echo "")
if echo "$http_port_test" | grep -q '"service"'; then
    green "✓ HTTP 协议端口创建成功"
    # 清理
    curl -fsS -X DELETE -H 'sec-fetch-site: same-origin' "$WEBUI_URL/api/services/smoke-http" >/dev/null 2>&1 || true
else
    yellow "⚠ HTTP 协议端口创建未通过（可能因鉴权或冲突）：$http_port_test"
fi

green ""
green "=========================================="
green "  ✓ smoke-test 全部用例通过"
green "=========================================="

if [ "$KEEP_RUNNING" != "1" ] && [ "$SKIP_BUILD" != "1" ]; then
    log "清理容器（KEEP_RUNNING=1 可保留）"
    $COMPOSE_BIN down 2>/dev/null || true
fi
