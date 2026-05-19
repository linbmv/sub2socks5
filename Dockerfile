# syntax=docker/dockerfile:1.6

ARG GO_VERSION=1.23
ARG DEBIAN_TAG=bookworm-slim
ARG SING_BOX_VERSION=1.11.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src

COPY go.mod ./
COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/sub2socks5 .

# 拉取并解压官方 sing-box 二进制作为镜像内置 seed 内核。
# 用户通过 Web UI 下载的新版本会覆盖该 seed（写入挂载卷的 internal/bin/sing-box）。
FROM debian:${DEBIAN_TAG} AS kernel
ARG TARGETARCH
ARG SING_BOX_VERSION
RUN set -eux; \
    apt-get update; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends curl ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    case "${TARGETARCH}" in \
      amd64) ARCH=amd64 ;; \
      arm64) ARCH=arm64 ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /opt/sing-box /tmp/sing-box; \
    curl -fsSL "https://github.com/SagerNet/sing-box/releases/download/v${SING_BOX_VERSION}/sing-box-${SING_BOX_VERSION}-linux-${ARCH}.tar.gz" \
      | tar -xz --strip-components=1 -C /tmp/sing-box; \
    mv /tmp/sing-box/sing-box /opt/sing-box/sing-box; \
    chmod +x /opt/sing-box/sing-box; \
    rm -rf /tmp/sing-box

FROM debian:${DEBIAN_TAG} AS runtime

RUN set -eux; \
    apt-get update; \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates tzdata; \
    rm -rf /var/lib/apt/lists/*; \
    groupadd --system --gid 10001 sub2socks5; \
    useradd --system --uid 10001 --gid sub2socks5 --home-dir /app --shell /usr/sbin/nologin sub2socks5

WORKDIR /app

# rootDir 由 os.Getwd() 决定（可被 SUB2SOCKS5_ROOT 覆盖），因此 /app 是默认运行时契约。
# internal/bin 必须可写，entrypoint 会按需把 seed 拷贝过来。
RUN mkdir -p /app/internal/data /app/internal/runtime /app/internal/bin \
    && chown -R sub2socks5:sub2socks5 /app

COPY --from=builder --chown=sub2socks5:sub2socks5 /out/sub2socks5 /app/sub2socks5
COPY --from=kernel /opt/sing-box/sing-box /opt/sing-box/sing-box
COPY --chmod=0755 scripts/entrypoint.sh /entrypoint.sh

LABEL org.opencontainers.image.title="sub2socks5" \
      org.opencontainers.image.description="基于 Go + sing-box 的本地代理管理器" \
      org.opencontainers.image.source="https://github.com/sglinhome/sub2socks5" \
      org.opencontainers.image.licenses="MIT"

USER sub2socks5
EXPOSE 18080 18081-18100
ENTRYPOINT ["/entrypoint.sh"]
