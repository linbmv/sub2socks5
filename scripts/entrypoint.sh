#!/bin/sh
# 容器启动钩子：
#   1. 修复挂载卷权限（用户常忘记 chown，导致 sing-box 写不进 internal/bin）
#   2. seed 内置 sing-box 到 internal/bin（仅在目标不存在时）
#   3. 用 setpriv 降权到 sub2socks5 用户运行主程序
set -e

APP_USER=sub2socks5
APP_UID=10001
APP_GID=10001
APP_DIRS="/app/internal/data /app/internal/runtime /app/internal/bin"

SEED_SRC=/opt/sing-box/sing-box
SEED_DST=/app/internal/bin/sing-box
SEED_VERSION_FILE=/app/internal/bin/sing-box-version.json

# 仅当以 root 启动时才修权限 + 降权；否则直接执行（兼容用户在 compose 强制 user: 的场景）
if [ "$(id -u)" = "0" ]; then
    # 修复挂载卷的所有者
    for d in $APP_DIRS; do
        mkdir -p "$d"
        # 仅当目标 owner 不是 sub2socks5 时再 chown，避免大量文件时的耗时
        current_owner=$(stat -c '%u' "$d" 2>/dev/null || echo 0)
        if [ "$current_owner" != "$APP_UID" ]; then
            chown -R "$APP_UID:$APP_GID" "$d"
        fi
    done

    # seed sing-box（root 身份执行，写入权限有保障）
    if [ -f "$SEED_SRC" ] && [ ! -f "$SEED_DST" ]; then
        cp -p "$SEED_SRC" "$SEED_DST"
        chmod +x "$SEED_DST"
        chown "$APP_UID:$APP_GID" "$SEED_DST"
        if [ -n "$SING_BOX_VERSION" ]; then
            cat > "$SEED_VERSION_FILE" <<EOF
{"version":"v$SING_BOX_VERSION","source":"docker-seed","seededAt":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF
            chown "$APP_UID:$APP_GID" "$SEED_VERSION_FILE"
        fi
        echo "[entrypoint] seeded sing-box from $SEED_SRC (version: ${SING_BOX_VERSION:-unknown})"
    fi

    # setpriv 降权运行（util-linux 提供，比 gosu 更通用）
    exec setpriv --reuid="$APP_UID" --regid="$APP_GID" --init-groups /app/sub2socks5 "$@"
fi

# 非 root 启动：跳过 chown / 降权，直接尝试 seed + 启动
if [ -f "$SEED_SRC" ] && [ ! -f "$SEED_DST" ]; then
    cp -p "$SEED_SRC" "$SEED_DST" 2>/dev/null && chmod +x "$SEED_DST" && \
        echo "[entrypoint] seeded sing-box from $SEED_SRC" || \
        echo "[entrypoint] WARN: failed to seed sing-box (target dir not writable; check volume permissions)"
fi
exec /app/sub2socks5 "$@"
