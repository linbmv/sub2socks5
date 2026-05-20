#!/bin/sh
# 容器启动钩子：若挂载的 internal/bin/sing-box 不存在，则从镜像内置位置 seed。
# 用户后续在 Web UI 下载的新版本会覆盖此 seed 版本。
set -e

SEED_SRC=/opt/sing-box/sing-box
SEED_DST=/app/internal/bin/sing-box
SEED_VERSION_FILE=/app/internal/bin/sing-box-version.json

if [ -f "$SEED_SRC" ] && [ ! -f "$SEED_DST" ]; then
    cp -p "$SEED_SRC" "$SEED_DST"
    chmod +x "$SEED_DST"
    # 记录 seed 版本信息（从构建参数传入）
    if [ -n "$SING_BOX_VERSION" ]; then
        echo "{\"version\":\"v$SING_BOX_VERSION\",\"source\":\"docker-seed\",\"seededAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" > "$SEED_VERSION_FILE"
    fi
    echo "[entrypoint] seeded sing-box from $SEED_SRC (version: ${SING_BOX_VERSION:-unknown})"
fi

exec /app/sub2socks5 "$@"
