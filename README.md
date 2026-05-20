# sub2socks5

一个基于 `Go + sing-box` 的本地代理管理器，用于把机场订阅、手动节点和节点组组织成可视化、可多端口分流的 `SOCKS5` 代理服务。

## 当前能力

- 拉取并解析订阅节点
- 支持多订阅地址
  - 多个订阅 URL 顺序拉取
  - 自动合并节点
  - 自动去重
- 支持 `vmess`、`vless`、`trojan`、`shadowsocks`、`hysteria2`、`tuic`
- 支持 Base64、URL Safe Base64、多行订阅文本
- 支持手动导入节点
  - 单行节点链接
  - 多行节点文本
  - 结构化 JSON
  - 带 `raw` 字段的 JSON
- 支持节点管理
  - 订阅节点
  - 手动节点
  - 节点组
  - 固定保留 `direct` 节点
- 支持多个本地 `SOCKS5` 服务
  - 每个端口可绑定不同节点或节点组
- 支持节点组策略
  - `urltest`
  - `fallback`（当前为应用层第一版）
- 支持内核管理
  - 检测系统架构
  - 获取 release 版本列表
  - 设置计划下载版本
  - 拉取匹配架构的 `sing-box` 内核
- 支持 DNS 防泄漏优化
  - 远端 DoH
  - Bootstrap DNS
  - 默认域名解析器单独配置
  - 每个 `SOCKS5` 目标出口绑定各自的 DoH server
- 支持运行状态与实时日志查看
- 支持保存配置后自动生成 `sing-box` 配置
- 支持运行中自动应用新配置
  - 当前实现方式为自动重启 `sing-box`

## 社区链接
- [LINUX DO](https://linux.do/)

## 项目结构

- `D:\sub2socks5\main.go`
  - 程序入口
  - 嵌入 `internal/public` 静态资源并启动应用
- `D:\sub2socks5\internal\app\app.go`
  - HTTP 服务入口
  - 提供 Web UI 与后端 API
  - 配置管理、订阅解析、运行时控制、内核下载管理
- `D:\sub2socks5\internal\public\index.html`
  - 主页
- `D:\sub2socks5\internal\public\app.js`
  - 主页交互逻辑
- `D:\sub2socks5\internal\public\nodes.html`
  - 节点管理页
- `D:\sub2socks5\internal\public\nodes.js`
  - 节点管理逻辑
- `D:\sub2socks5\internal\public\style.css`
  - 页面样式

### 持久化目录

- `D:\sub2socks5\internal\data`
  - 业务配置
  - 订阅状态
  - 架构信息
  - 版本列表缓存
  - 计划下载版本
- `D:\sub2socks5\internal\runtime`
  - 生成后的 `sing-box.json`
- `D:\sub2socks5\internal\bin`
  - 已安装的 `sing-box` 内核

## 工作流程

1. 启动 Web UI
   - `go run .`
2. 在主页保存基础配置
3. 更新订阅，或在节点管理页导入手动节点
4. 配置节点组与多个本地 `SOCKS5` 服务
5. 保存配置后自动生成 `sing-box` 配置
6. 如果 `sing-box` 正在运行，则自动重启应用新配置
7. 不同本地端口分别通过不同节点或节点组提供代理服务

## DNS 策略

当前支持：

- DoH 服务器预设
  - `https://dns.google/dns-query`
  - `https://cloudflare-dns.com/dns-query`
  - 自定义
- DoH 引导解析 DNS 预设
  - `1.1.1.1`
  - `8.8.8.8`
  - `223.5.5.5`
  - 自定义

当前设计目标：

- 尽量减少本机直连 DNS 泄漏
- 主解析使用远端 DoH
- 用 Bootstrap DNS 解析 DoH 域名
- 每个本地 `SOCKS5` 服务的 DNS 请求跟随其目标出口，而不是统一走默认出口

## 节点组说明

### `urltest`

- 使用 `sing-box` 原生 `urltest`
- 支持测试地址预设
  - `https://www.gstatic.com/generate_204`
  - `https://www.google.com/generate_204`
  - `https://cp.cloudflare.com/generate_204`
  - 自定义
- 定时对组内节点进行延迟测试
- 自动选择更优节点转发流量

### `fallback`

- 当前不是 `sing-box` 原生出站类型
- 目前实现为应用层故障转移第一版
- 后端维护当前活跃节点
- 周期性通过探测结果切换可用节点
- 节点管理页可查看当前活跃成员和最近切换时间

## Web UI 页面

### 首页

支持：

- 检测当前架构
- 检查内核版本
- 检查版本更新
- 设置计划版本
- 拉取 `sing-box` 内核
- 保存基础配置
- 更新订阅
- 启动 / 停止 `sing-box`
- 配置多个订阅地址
- 配置多个 `SOCKS5` 服务
- 配置 DoH 服务器与 Bootstrap DNS 预设
- 查看状态、生成结果和实时日志

首页当前布局：

- 第一行：`Web UI 监听地址`、`Web UI 端口`、`sing-box 二进制路径`、`日志级别`
- 第二行：`DNS 策略`、`DOH 服务器`、`DoH 引导解析 DNS`
- 第三行：`默认路由出口`、`自动启动`

运行状态区域：

- `状态`：显示运行摘要
- `日志`：显示 `sing-box` 实时日志

### 节点管理页

支持：

- 导入手动节点
- 查看 / 删除手动节点
- 添加节点组
- 为节点组设置策略与测试参数
- 按行添加节点组成员
- 查看 `fallback` 当前活跃节点状态
- 现有节点以卡片形式展示
  - 第一行显示节点名称
  - 第二行显示协议和来源标签
- 节点组使用可展开卡片展示
  - 折叠态显示组名、策略、成员数量
  - 展开后显示组内节点与编辑项

## 手动导入节点格式

### 单行节点链接

```text
vless://uuid@example.com:443?security=tls&sni=example.com#my-node
```

### 多行节点文本

```text
vless://...
trojan://...
ss://...
```

### 结构化 JSON

```json
{
  "type": "vless",
  "tag": "my-node",
  "server": "example.com",
  "server_port": 443,
  "uuid": "..."
}
```

### 带 `raw` 的 JSON

```json
{
  "raw": "vless://uuid@example.com:443?security=tls#my-node"
}
```

处理逻辑：

1. 先判断输入是否为 JSON
2. 如果是 JSON，优先按结构化节点处理
3. 如果不是 JSON，则按订阅 / 链接文本解析
4. 先识别协议，再套用对应协议模板解析

## API 列表

### 配置相关

- `GET /api/config`
- `POST /api/config`
  - 保存业务配置
  - 自动生成新的 `sing-box` 配置
  - 如果运行中则自动重启应用新配置

### 订阅相关

- `POST /api/subscription/refresh`

### 节点相关

- `GET /api/nodes`
- `POST /api/nodes`
- `POST /api/nodes/import`

### 内核相关

- `GET /api/kernel/status`
- `POST /api/kernel/architecture`
- `GET /api/kernel/releases`
- `POST /api/kernel/releases/update`
- `POST /api/kernel/plan`
- `GET /api/kernel/download`
- `POST /api/kernel/download`

### 运行时相关

- `POST /api/runtime/generate`
- `POST /api/runtime/start`
- `POST /api/runtime/stop`
- `GET /api/runtime/generated`
- `GET /api/runtime/logs`

## API 调用与测试方法

以下示例以 Windows PowerShell 为准。

### 1. 启动服务

```powershell
node src/server.js
```

访问：

```text
http://127.0.0.1:18080
```

### 2. 获取当前配置

```powershell
Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/config"
```

### 3. 获取节点列表

```powershell
Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/nodes"
```

### 4. 手动导入节点

```powershell
$body = @{
  raw = "vless://uuid@example.com:443?security=tls&sni=example.com#my-node"
} | ConvertTo-Json

Invoke-RestMethod `
  -Uri "http://127.0.0.1:18080/api/nodes/import" `
  -Method Post `
  -ContentType "application/json" `
  -Body $body
```

### 5. 更新订阅

```powershell
Invoke-RestMethod `
  -Uri "http://127.0.0.1:18080/api/subscription/refresh" `
  -Method Post `
  -ContentType "application/json" `
  -Body "{}"
```

### 6. 保存配置并自动应用

```powershell
$config = Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/config"

Invoke-RestMethod `
  -Uri "http://127.0.0.1:18080/api/config" `
  -Method Post `
  -ContentType "application/json" `
  -Body ($config.config | ConvertTo-Json -Depth 20)
```

### 7. 启动 `sing-box`

```powershell
Invoke-RestMethod `
  -Uri "http://127.0.0.1:18080/api/runtime/start" `
  -Method Post `
  -ContentType "application/json" `
  -Body "{}"
```

### 8. 查看运行日志

```powershell
Invoke-RestMethod -Uri "http://127.0.0.1:18080/api/runtime/logs"
```

### 9. 测试本地 `SOCKS5` 端口是否监听

假设当前端口为 `53456`：

```powershell
Test-NetConnection -ComputerName 127.0.0.1 -Port 53456
```

### 10. 测试是否能通过代理访问 Google

```powershell
curl.exe --socks5-hostname 127.0.0.1:53456 --max-time 25 https://www.google.com/generate_204 -I -s -o NUL -w "%{http_code}"
```

预期：

```text
204
```

### 11. 测试是否能通过代理访问 Gstatic

```powershell
curl.exe --socks5-hostname 127.0.0.1:53456 --max-time 25 https://www.gstatic.com/generate_204 -I -s -o NUL -w "%{http_code}"
```

预期：

```text
204
```

## 当前已验证结果

- Web UI 可正常启动在 `18080`
- `GET /api/config` 正常返回
- `GET /api/nodes` 正常返回
- `POST /api/nodes/import` 已验证可成功导入 `vless://...`
- `SOCKS5` 端口已验证可监听
- 已验证可通过代理访问 Google / Gstatic 并返回 `204`
- 已验证多出口 DNS server 生成逻辑正确
- 已验证节点组排序在普通节点前面

## 注意事项

- `fallback` 目前仍是第一版应用层实现
- 某些机场私有字段仍可能需要继续兼容
- 当前“运行中应用新配置”采用重启 `sing-box` 的方式，而不是热重载
- 不建议把运行期文件和本地状态文件提交到 Git

## Docker 部署（VPS 推荐）

Docker 镜像内置 sub2socks5 主程序与 sing-box 默认内核（首次启动会 seed 到挂载卷的 `internal/bin/sing-box`）。`data`、`runtime`、`bin` 三个目录通过 bind mount 持久化。容器启动目录固定为 `/app`，匹配程序基于工作目录解析 `internal/{data,runtime,bin}` 的行为；也可以通过 `SUB2SOCKS5_ROOT` 改用任意路径。

仓库提供**单文件 compose + `.env` 驱动**，覆盖 4 种部署模式。

### 快速开始

在 VPS 上安装 Docker Engine 和 Docker Compose v2 后：

```bash
git clone https://github.com/sglinhome/sub2socks5.git
cd sub2socks5
mkdir -p data runtime bin
sudo chown -R 10001:10001 data runtime bin
cp .env.example .env
# 按需编辑 .env，最简场景（仅本机）保持默认即可
docker compose up -d --build
docker compose logs -f sub2socks5
```

默认仅本机访问：`http://127.0.0.1:18080`。

### 部署模式（由 `.env` 切换）

| 模式 | 关键变量 | 访问方式 |
|---|---|---|
| **A 仅本机** | 全部默认 | `http://127.0.0.1:18080` |
| **B LAN 共享** | `WEBUI_BIND=0.0.0.0` + `AUTH_TOKEN` + `ALLOWED_HOSTS=<lan-ip>` | `http://<lan-ip>:18080` |
| **C CF Tunnel（自起）** | `AUTH_TOKEN` + `ALLOWED_HOSTS=<回源域名>` + `CF_TUNNEL_TOKEN` | `https://<回源域名>` |
| **D CF Tunnel（外部）** | `AUTH_TOKEN` + `ALLOWED_HOSTS=<回源域名>` + `EXTERNAL_NETWORK=<网络名>` | `https://<回源域名>` |

启动命令：

```bash
# A / B / D 直接启动
docker compose up -d --build

# C 自起 cloudflared 容器（需启用 cf-tunnel profile）
docker compose --profile cf-tunnel up -d --build
```

### 首次配置

第一次进入 Web UI 后按以下顺序操作：

1. 在内核管理页面获取 release 列表，并下载匹配当前容器架构的 sing-box 内核。
2. 更新订阅，或在节点管理页导入手动节点。
3. 给每个 SOCKS5 服务配置监听地址、端口、目标出口，**公网暴露的端口务必添加 `username/password` 鉴权用户**。
4. 保存配置后启动运行时。

> ⚠️ **bridge 模式 SOCKS5 配置（最容易遗漏）**
>
> 1. **监听地址** 必须从默认的 `127.0.0.1` 改为 `0.0.0.0`，否则容器外完全无法连接。
> 2. **端口范围** 必须落在 `18081-18100` 内（compose 默认发布范围）。如需扩展，同步改 compose `ports` 段和 `Dockerfile` 的 `EXPOSE`。
> 3. **公网暴露** 必须配置 `username/password` 鉴权用户（在服务卡片中添加），否则任何人可白嫖代理。

### 从本地客户端连接 SOCKS5

```bash
# SSH 隧道方式（仅本机模式）
ssh -L 18081:127.0.0.1:18081 user@your-vps
curl --socks5-hostname 127.0.0.1:18081 https://www.google.com/generate_204 -I

# 公网直连（需在 Web UI 配置 username/password 用户）
curl --proxy socks5://user:pass@your-vps.example.com:18081 https://ifconfig.me
```

### CF Tunnel 模式说明

#### 模式 C：本仓库自起 `cloudflared` 容器

1. [Cloudflare Zero Trust](https://one.dash.cloudflare.com/) → **Networks → Tunnels → Create a tunnel**
2. 选 Docker，复制弹出的 token (`eyJhIjoi...`) 填入 `.env` 的 `CF_TUNNEL_TOKEN`
3. Tunnel **Public Hostname** 添加：
   - Subdomain：任意（例如 `sub2socks5`）
   - Domain：你的域名
   - Service Type：`HTTP`
   - URL：`sub2socks5:18080`
4. `.env` 同时填好 `SUB2SOCKS5_AUTH_TOKEN` + `SUB2SOCKS5_ALLOWED_HOSTS=<回源域名>`
5. `docker compose --profile cf-tunnel up -d --build`

#### 模式 D：复用已有外部 `cloudflared` 容器

适合你已经在跑独立的 `cloudflared`（多个项目共享）的场景。

1. `docker network ls` 找到 cloudflared 所在的 docker network 名
2. `.env` 设 `EXTERNAL_NETWORK=<网络名>`（不要设 `CF_TUNNEL_TOKEN`，避免重复起 cloudflared）
3. 在已有 Tunnel 的 **Public Hostname** 添加 URL：`http://sub2socks5:18080`
4. `.env` 填好 `SUB2SOCKS5_AUTH_TOKEN` + `SUB2SOCKS5_ALLOWED_HOSTS=<回源域名>`
5. `docker compose up -d --build`

> 🛡️ **CF Tunnel 模式的安全模型**
>
> - Web UI 经 CF 反代，VPS 不开放 18080 端口；CF 自动注入 `X-Forwarded-Proto=https`，cookie `Secure` 标志自动启用
> - SOCKS5 直连 VPS（CF Tunnel 免费版不支持 TCP）；**必须** 给每个端口配 `username/password`
> - 建议 VPS 防火墙限制 SOCKS5 端口来源 IP

### 防火墙建议

生产 VPS 默认拒绝公网访问 Web UI（CF Tunnel 模式不需要在 VPS 开 18080）：

```bash
sudo ufw deny 18080/tcp
sudo ufw allow from <可信 IP> to any port 18081:18100 proto tcp
sudo ufw reload
```

### 环境变量

| 变量 | 作用 | 默认值 |
|------|------|--------|
| `SUB2SOCKS5_ROOT` | 覆盖运行根目录 | 容器启动目录 `/app` |
| `SUB2SOCKS5_HOST` | Web UI 监听地址 | 配置文件 `app.host`（默认 `127.0.0.1`） |
| `SUB2SOCKS5_PORT` | Web UI 监听端口 | 配置文件 `app.port`（默认 `18080`） |
| `SUB2SOCKS5_SING_BOX_BINARY` | 指向已有 sing-box 二进制 | 配置文件 `app.singBoxBinary` |
| `SUB2SOCKS5_AUTH_TOKEN` | 启用 Web UI 鉴权（公网部署必填） | 未设置时鉴权关闭 |
| `SUB2SOCKS5_ALLOWED_HOSTS` | 鉴权启用时的 Host 头白名单（防 DNS rebinding），逗号分隔 | `localhost,127.0.0.1,::1` |
| `SUB2SOCKS5_DEPLOYMENT_HINT` | Web UI 顶部部署横幅（`level\|message`） | 不显示 |
| `SUB2SOCKS5_EXTERNAL_HOST` | 复制 SOCKS5 时把 `0.0.0.0`/`::` 替换为该地址 | 保留原 `listen` |
| `WEBUI_BIND` | compose 中 Web UI 端口绑定接口 | `127.0.0.1` |
| `SOCKS5_BIND` | compose 中 SOCKS5 端口绑定接口 | `0.0.0.0` |
| `CF_TUNNEL_TOKEN` | CF Tunnel 模式 C 用，自起 cloudflared | 空 |
| `EXTERNAL_NETWORK` | CF Tunnel 模式 D 用，加入外部 docker network | `sub2socks5_default` |

### Web UI 鉴权（可选）

设置 `SUB2SOCKS5_AUTH_TOKEN=<高熵随机字符串>` 后，所有 HTTP 请求必须携带匹配的 Token。支持三种来源（按优先级）：

1. `Authorization: Bearer <token>` 请求头（命令行/客户端工具推荐）。
2. `sub2socks5_token=<token>` Cookie（浏览器持久化登录态）。
3. URL 查询参数 `?token=<token>`（仅用于浏览器首次登录，命中后服务器会自动写 Cookie 并 303 重定向去掉 token 参数，避免 referer 泄漏）。

校验失败返回 `401 Unauthorized` 并附带 `WWW-Authenticate: Bearer realm="sub2socks5"`。生成 Token 示例：

```bash
openssl rand -hex 32
```

> ⚠️ Token 写入 compose 文件后，请确保该文件不入 Git 公开仓库（建议拆分到 `.env` 并在 compose 中用 `env_file:` 引用）。

### 数据备份

需要备份的目录是 `data`、`runtime`、`bin`。其中 `data` 可能包含订阅 URL、节点配置和访问凭据，备份文件应按敏感数据保存。

```bash
docker compose down
tar -czf sub2socks5-backup-$(date +%F).tar.gz data runtime bin
docker compose up -d
```

恢复时先停止容器，再解压到同一目录后启动：

```bash
docker compose down
tar -xzf sub2socks5-backup-2026-05-18.tar.gz
docker compose up -d
```

### FAQ

**Web UI 打不开（连接被拒绝）？**

检查 SSH 隧道是否建立成功。默认 compose 把 `18080` 绑定到宿主机 `127.0.0.1`，必须通过 `ssh -L 18080:127.0.0.1:18080 user@your-vps` 把端口转发到本机后访问。

**bridge 模式下 SOCKS5 端口连不上？**

通常是 Web UI 里的 SOCKS5 监听地址仍是 `127.0.0.1`。在容器 bridge 网络中，监听 `127.0.0.1` 只对容器内部可见，必须改为 `0.0.0.0`，并且端口要落在 `18081-18100` 范围内。

**首次启动后提示找不到 sing-box？**

镜像已内置默认版本，entrypoint 会在 `internal/bin/sing-box` 缺失时自动从 `/opt/sing-box/sing-box` seed 一份。如果仍提示找不到，通常是挂载卷的宿主目录权限不是 `10001:10001`，导致 seed 写入失败 — 重新执行 `sudo chown -R 10001:10001 data runtime bin` 后重启容器即可。也可以在 Web UI 内核管理页下载更新版本覆盖 seed。

**内核下载失败？**

检查容器出站网络是否畅通、是否触发 GitHub release 的 rate limit。可在本机下载对应架构的 sing-box 二进制后，放入挂载目录 `./bin/sing-box` 并赋予可执行权限，重启容器即可生效。

**重启容器后配置丢失？**

检查 `data`、`runtime`、`bin` 三个目录是否都正确挂载到对应路径。compose 文件中三个 volume 缺一不可：业务配置、生成的 sing-box.json、内核二进制分别持久化在这三处。

**能否把 Web UI 直接公开到公网？**

需要同时启用 `SUB2SOCKS5_AUTH_TOKEN` 鉴权并配合反向代理 / IP 白名单 / VPN。即便如此，也建议优先使用 SSH 隧道：Token 写入 compose 后会成为长期凭据，泄漏后 sing-box 控制权将完全转移。

## 打包方法

当前项目使用 Go 原生构建产出单文件可执行程序，程序逻辑与 `internal/public` 静态资源会一起打包进二进制。

### 前置要求

- 已安装 Go 1.23+
- 在项目根目录执行命令

### 构建 Windows 可执行文件

```powershell
go build -trimpath -ldflags "-s -w" -o dist/sub2socks5-windows-x64.exe .
```

### 输出文件

构建完成后会生成：

- `D:\sub2socks5\dist\sub2socks5-windows-x64.exe`

### 运行方式

```powershell
cd D:\sub2socks5\dist
.\sub2socks5-windows-x64.exe
```

默认 Web UI 地址：

```text
http://127.0.0.1:18080
```

### 首次运行行为

- 如果不存在配置文件，程序会自动生成默认配置
- 运行目录为可执行文件所在目录
- 默认会创建或使用以下子目录：
  - `internal/data`
  - `internal/runtime`
  - `internal/bin`

### 打包说明

- 可执行文件会包含全部 Go 业务逻辑和 `internal/public` 静态资源
- `sing-box` 内核不会嵌入到 exe 中
- 首次运行后，用户可以通过 Web UI 按系统架构下载对应的 `sing-box` 内核
- 因此发布时通常只需要提供：
  - `sub2socks5-sea.exe`
  - 或者由用户首次运行后自行下载内核

### 注意事项

- 用户配置与运行时状态不会嵌入二进制，仍保存在 `internal/data` 和 `internal/runtime`
- 如用于正式分发，建议对可执行文件进行代码签名

## GitHub Actions

项目已提供 GitHub Actions 工作流，支持手动触发全平台构建，以及手动触发构建后发布到 GitHub Release。

### 工作流文件

- `D:\sub2socks5\.github\workflows\reusable-build.yml`
  - 可复用构建模板
  - 统一维护平台与架构矩阵
  - 内置可选 smoke test，用于验证首页 `http://127.0.0.1:18080/` 可访问
- `D:\sub2socks5\.github\workflows\build.yml`
  - 手动触发
  - 只构建，不发布
- `D:\sub2socks5\.github\workflows\release.yml`
  - 手动触发
  - 先构建，再发布到 GitHub Release

### 当前构建目标

- `linux-x64`
- `linux-arm64`
- `windows-x64`
- `windows-arm64`
- `macos-x64`
- `macos-arm64`

### 产物规则

- 每个平台/架构单独构建一个二进制文件
- 每个平台/架构单独打包为一个 zip
- 每个 zip 中只包含一个二进制文件

产物命名示例：

- `sub2socks5-linux-x64.zip`
- `sub2socks5-linux-arm64.zip`
- `sub2socks5-windows-x64.zip`
- `sub2socks5-windows-arm64.zip`
- `sub2socks5-macos-x64.zip`
- `sub2socks5-macos-arm64.zip`

### 手动构建

1. 打开 GitHub 仓库的 `Actions`
2. 选择 `Build`
3. 点击 `Run workflow`

构建完成后，可在该次 workflow 的 `Artifacts` 中下载各平台单个二进制文件。

说明：

- `Build` 工作流不会先手动打 zip
- GitHub Actions 下载 artifact 时仍会以 GitHub 自身的 artifact 压缩形式提供
- 因此 `Build` 用于验证构建是否成功，而不是提供最终发布压缩包

### 手动发布 Release

1. 打开 GitHub 仓库的 `Actions`
2. 选择 `Release`
3. 点击 `Run workflow`
4. 填写：
   - `release_tag_prefix`
   - `release_name`

`Release` 工作流会：

- 自动构建全部平台/架构产物
- 自动按平台/架构分别打包 zip
- 自动收集所有 zip
- 自动创建或更新对应的 GitHub Release
- 自动把所有 zip 上传到 Release 附件

### 说明

- `Build` 适合日常验证构建是否正常
- `Release` 适合正式生成发布附件
- 如果后续要增减平台或架构，只需要修改 `D:\sub2socks5\.github\workflows\reusable-build.yml`
- 当前构建为 Go 单文件模式，`internal/public` 通过 `embed` 打包进二进制

## 现阶段成果（增量）

- 已完成从 Node.js 版本到 Go 版本的核心迁移，当前使用单进程 Go 服务承载配置管理、订阅解析与运行控制
- 已完成静态资源内嵌打包，`internal/public` 随二进制分发，避免运行时依赖外部前端文件
- 已修复多端口出口同 IP 问题，当前每个 SOCKS5 入站端口可按 `target` 正确路由到不同出站
- 已实现自动启动能力：`autoStart=true` 时程序启动后自动拉起 sing-box；手动停止不会被守护逻辑误拉起
- 已补充运行守护能力：sing-box 异常退出时自动退避重启，降低长时间运行中断风险
- 已完善“保存即生效”链路：配置和节点保存后，若运行中会自动重启内核应用新配置
- 已增强节点测速链路：
  - 全量测速并发模型升级为固定 5 并发滑动队列
  - 单节点/全量测速均支持内核未运行时自动拉起并在结束后恢复初始状态
  - 测速错误信息细化（控制接口未就绪、超时、HTTP 错误）
- 已上线一键配置 SOCKS5 服务：
  - 先按节点测速结果筛选可用节点
  - 仅为可用节点创建 SOCKS5 服务
  - 自动分配无冲突端口
  - 增加进度遮罩卡片与可取消流程
- 已补全 HY2/TUIC 关键参数解析兼容：
  - `hysteria2` 支持 `auth/password/token`、`up/down` 多写法、`obfs/salamander` 参数
  - `tuic` 修复 `alpn` 字段位置，写入 `tls.alpn`，避免 sing-box 配置校验失败
- 已补强节点编辑页：
  - 表单模式补全协议参数输入（TLS、obfs、拥塞控制、0-RTT 等）
  - 单行/JSON 导入后增加前端归一化处理，提升兼容性
