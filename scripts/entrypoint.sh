#!/bin/sh
# 容器启动钩子：若挂载的 internal/bin/sing-box 不存在，则从镜像内置位置 seed。
# 用户后续在 Web UI 下载的新版本会覆盖此 seed 版本。
set -e

SEED_SRC=/opt/sing-box/sing-box
SEED_DST=/app/internal/bin/sing-box

if [ -f "$SEED_SRC" ] && [ ! -f "$SEED_DST" ]; then
    cp "$SEED_SRC" "$SEED_DST"
    chmod +x "$SEED_DST"
    echo "[entrypoint] seeded sing-box from $SEED_SRC"
fi

exec /app/sub2socks5 "$@"
