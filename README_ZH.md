# AI Watch

[English](README.md)

AI Watch 是面向 Codex 与 Claude Provider 的本地运维和稳定性控制台。它在隔离的任务目录中运行官方 CLI，记录结构化请求事实，并将 Provider 验证、计划调度、可靠性分析、事故处置、故障切换和通知集中到一个 Web 界面中。

默认工作流为：

> 配置 Provider → 手动测活或场景验证 → 创建计划 → 查看异常 → 处置或切换线路

Web 控制台默认只绑定本机：

```text
http://127.0.0.1:8787
```

## 核心能力

- **Provider 管理**：合并当前 Codex/Claude CLI 配置、启动时从 CC Switch 导入的快照，以及使用 AES-GCM 加密凭证的手填 Provider。
- **手动与场景验证**：支持单次测活、持续保活，以及在多个 Provider 上运行可复用测试场景并横向比较结果。
- **自动化计划**：按时区、星期和时间窗口执行验证，并始终使用准确的 `scheduleId` 关联计划请求历史。
- **可靠性与 SLO**：按可配置时间范围比较成功率、延迟、失败密度、异常信号和错误预算。
- **事故中心**：将重复失败聚合为可审计时间线，支持确认、静默、备注、恢复和脱敏请求关联。
- **Provider Group 与故障切换**：验证按优先级排列的备用线路，为显式绑定组的计划提供建议式或自动切换。
- **维护窗口**：集中抑制通知、备用验证和自动切换，但不会停止已在运行的任务。
- **事件与请求事实**：分开浏览普通运行事件和脱敏 CLI 请求记录。请求详情可通过 `/requests/:requestId` 直接访问，并支持浏览器返回。
- **通知路由**：按消息类型路由到钉钉渠道，并提供摘要预览和发送控制。
- **只读诊断**：检查 CLI、Redis、配置、运行时和 CC Switch 同步健康状态，不提供通用 Redis Key 管理界面。

## 产品区域

一级导航统一维护在 `frontend/src/navigation.ts`：

| 区域 | 主要路径 | 用途 |
| --- | --- | --- |
| 总览 | `/` | 查看当前 Provider、任务和环境状态 |
| Provider | `/providers` | Provider 发现、手填配置、启停和测活 |
| 验证中心 | `/validation/*` | 测试场景与对比历史 |
| 自动化 | `/automation/*` | 计划、故障切换组和维护窗口 |
| 稳定性 | `/stability/*` | 可靠性、SLO 和事故 |
| 事件 | `/events` | 运行事件和请求记录 |
| 设置 | `/settings/*` | 运行设置、通知、路由和系统诊断 |

旧页面路径会规范化到对应产品区域。请求详情深链接使用 `/requests/:requestId`。

## 快速启动

### 环境要求

- macOS 使用 Docker Desktop，Linux 使用 Docker Engine 与 Compose v2
- 宿主机上存在 Codex、Claude 和 CC Switch 配置目录

创建挂载源目录并启动：

```bash
mkdir -p ~/.codex ~/.claude ~/.cc-switch
docker compose up -d --build
```

Compose 会启动：

- `ai-watch`：Go API、React Web、Codex CLI 和 Claude CLI
- `redis`：必需的内部存储，用于设置、Provider 快照、加密凭证、事件和运行元数据
- `mihomo`：为 Provider 级代理策略提供私有网络出口的 sidecar

服务健康后打开 `http://127.0.0.1:8787`。

查看状态与日志：

```bash
docker compose ps
docker compose logs -f ai-watch
curl http://127.0.0.1:8787/api/health
```

停止服务并保留数据：

```bash
docker compose down
```

停止服务并删除全部 named volume：

```bash
docker compose down -v
```

## 配置来源

Compose 默认只读挂载以下宿主机目录：

| 宿主机路径 | 容器路径 | 用途 |
| --- | --- | --- |
| `~/.codex` | `/home/aiwatch/.codex` | 当前 Codex 配置与认证 |
| `~/.claude` | `/home/aiwatch/.claude` | 当前 Claude 配置与认证 |
| `~/.cc-switch` | `/home/aiwatch/.cc-switch` | 仅启动时使用的 CC Switch SQLite 来源 |

AI Watch 不会修改这些目录。

CC Switch 是启动同步来源，不是运行时数据库。应用启动时会读取挂载的 SQLite 数据库并将 Provider 复制到 Redis；同步失败时保留 Redis 中最后一次成功快照。修改 CC Switch 后需要重启 `ai-watch`：

```bash
docker compose restart ai-watch
docker compose logs --tail=100 ai-watch
```

手动创建的 Provider 保存在 Redis。API Key 在持久化前使用 AES-GCM 加密，保存后不会再返回给浏览器。

## 环境配置

覆盖默认值前先复制示例文件：

```bash
cp .env.example .env
```

常用配置：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AI_WATCH_PORT` | `8787` | 本地 Web 端口，仅绑定 `127.0.0.1` |
| `REDIS_PORT` | `6379` | 本地 Redis 端口，仅绑定 `127.0.0.1` |
| `AI_WATCH_REDIS_URL` | `redis://redis:6379/0` | Compose 内部使用的必需 Redis 连接 |
| `AI_WATCH_MASTER_KEY` | 空 | 可选的 32 字节凭证加密密钥；为空时使用持久化的本地密钥 |
| `CODEX_CONFIG_DIR` | `${HOME}/.codex` | 宿主机 Codex 配置目录 |
| `CLAUDE_CONFIG_DIR` | `${HOME}/.claude` | 宿主机 Claude 配置目录 |
| `CC_SWITCH_CONFIG_DIR` | `${HOME}/.cc-switch` | 启动同步使用的宿主机 CC Switch 目录 |
| `AI_WATCH_CC_SWITCH_SYNC_TIMEOUT_SECONDS` | `10` | 复制和查询 CC Switch 数据库的超时时间 |
| `AI_WATCH_RUNTIME_TMPFS_SIZE` | `256m` | 并发任务和 CC Switch 临时快照使用的内存运行目录容量 |
| `MIHOMO_CONFIG_FILE` | `./config/mihomo/config.yaml.example` | Mihomo 只读配置文件 |
| `AI_WATCH_DEFAULT_PROXY_URL` | `http://mihomo:7890` | Compose 内默认 Provider 代理 |
| `DINGTALK_WEBHOOK_URL` | 空 | 可选的钉钉 Webhook，启动时导入应用配置 |
| `CODEX_CLI_VERSION` | `latest` | 构建镜像时安装的 Codex npm 包版本 |
| `CLAUDE_CLI_VERSION` | `latest` | 构建镜像时安装的 Claude Code npm 包版本 |

`.env.example` 还包含 Redis 限制、镜像覆盖、代理变量、OpenAI/Codex 凭证、Anthropic/Claude 凭证，以及 Bedrock 或 Vertex 相关环境变量。

不要提交 API Key、钉钉 Webhook、订阅地址、代理凭证或云平台凭证。

### 自定义挂载路径

配置目录不在当前用户主目录下时，请使用绝对路径：

```dotenv
CODEX_CONFIG_DIR=/Users/your-name/.codex
CLAUDE_CONFIG_DIR=/Users/your-name/.claude
CC_SWITCH_CONFIG_DIR=/Users/your-name/.cc-switch
```

macOS 用户需要确保 Docker Desktop 允许共享自定义路径。Linux 用户应以配置文件所有者身份运行 Compose，或明确填写绝对路径。请保持宿主机凭证文件的私密权限，不要通过放宽文件权限来绕过 Docker 或 SELinux 配置问题。

## 代理 Sidecar

`config/mihomo/config.yaml.example` 提供默认直连的安全基线。Mihomo 端口不会映射到宿主机，AI Watch 只通过 Compose 内部网络访问 sidecar。

使用私有 Mihomo 配置时，将文件放在仓库外并设置：

```dotenv
MIHOMO_CONFIG_FILE=/absolute/path/to/mihomo.yaml
```

然后重启并检查 sidecar：

```bash
docker compose restart mihomo
docker compose ps mihomo
docker compose logs --tail=100 mihomo
```

Provider 可以选择默认代理、直连或自定义代理。不要提交订阅地址或代理凭证。

## 安全与数据边界

- Web 端口和 Redis 端口只发布到 `127.0.0.1`。
- Codex 和 Claude 任务使用 `/run/ai-watch` 下的独立临时配置目录。
- 宿主机 CLI 与 CC Switch 目录均以只读方式挂载。
- CLI Prompt 和供应商响应在适用场景下只保存脱敏且限制长度的事实。
- Redis 是必需的内部应用存储，只通过健康与诊断信息展示，不提供通用 Key 浏览器或 `/api/redis/*` API。
- Provider Group 切换只改变 AI Watch 状态和显式绑定的计划，不会改写宿主机 Codex、Claude 或 CC Switch 配置。
- 维护窗口会抑制自动动作与通知，但不会终止正在运行的任务。
- 所有前端写请求自动携带 `Idempotency-Key`。

## 本地开发

### 前置依赖

- Go 1.24
- Node.js 22 与 npm
- Redis 7 兼容服务
- 执行真实 Provider 任务时需要 Codex 和/或 Claude CLI

安装前端依赖并启动 Redis：

```bash
cd frontend && npm install
cd ..
docker compose up -d redis
```

在仓库根目录启动后端：

```bash
AI_WATCH_REDIS_URL=redis://127.0.0.1:6379/0 \
AI_WATCH_DATA_DIR=./data \
go run ./cmd/ai-watch
```

在另一个终端启动 Vite：

```bash
cd frontend
npm run dev
```

开发前端监听 `http://127.0.0.1:5173`，并将 `/api` 代理到 `http://127.0.0.1:8787`。

常用构建命令：

```bash
make backend
make frontend
make build
make test
```

## 项目验证

提交变更前运行：

```bash
go test ./...
cd frontend && npm run build && npm run test:e2e
docker compose config --quiet
```

首次运行 Playwright 时安装 Chromium：

```bash
cd frontend
npm run test:e2e:install
```

前端端到端测试会在 `4173` 端口启动独立 Vite 服务，并使用模拟 API 行为，因此不需要真实 Provider 凭证。

## 部署

仅构建并替换应用服务：

```bash
docker compose build ai-watch
docker compose up -d ai-watch
```

生产部署建议固定 `CODEX_CLI_VERSION`、`CLAUDE_CLI_VERSION` 以及镜像 tag 或 digest，以保证构建可复现。

## 常见问题

### Bind source path does not exist

创建默认挂载源，或在 `.env` 中设置绝对路径：

```bash
mkdir -p ~/.codex ~/.claude ~/.cc-switch
```

### CLI 不可用

检查镜像中安装的版本：

```bash
docker compose exec ai-watch codex --version
docker compose exec ai-watch claude --version
```

如果上游版本不兼容，在 `.env` 中固定已验证版本并重新构建。

### 配置无法读取

检查展开后的挂载和容器内路径：

```bash
docker compose config
docker compose exec ai-watch sh -c 'ls -la ~/.codex ~/.claude ~/.cc-switch'
```

应检查 Docker Desktop 文件共享或 SELinux 标签，不要将凭证文件改为全局可读。

### CC Switch 修改后没有更新

CC Switch 只在启动时同步：

```bash
docker compose restart ai-watch
```

如果本次同步失败，AI Watch 会继续使用 Redis 中最后一次成功快照。可通过系统诊断页面和应用日志查看详情。

### 运行时报 `no space left on device`

`/run/ai-watch` 是保存临时任务凭证、CLI 配置和 CC Switch 快照的内存文件系统。在 `.env` 中增大 `AI_WATCH_RUNTIME_TMPFS_SIZE`，然后重新创建应用容器：

```bash
AI_WATCH_RUNTIME_TMPFS_SIZE=512m
docker compose up -d --force-recreate ai-watch
```

### 健康检查失败

```bash
docker compose logs ai-watch
docker compose ps
```

确认 `8787` 和 `6379` 端口可用，或在 `.env` 中修改 `AI_WATCH_PORT` 和 `REDIS_PORT`。

## License

仓库当前未包含 License 文件。
