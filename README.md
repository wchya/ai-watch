# AI Watch

AI Watch 是 `ai-watch.sh` 的 Docker Web 客户端。它在容器内运行 Codex 和 Claude CLI，使用同一 Compose 网络中的 Redis 保存热配置、Provider 快照、事件和运行元数据，并通过私有 Mihomo sidecar 为需要代理的测活请求提供网络出口。它提供测活、保活、当前配置与 CC Switch Provider 快照选择、加密手填 Provider、计划任务请求时间线、可靠性趋势与告警、任务停止、实时状态、通知、Redis 管理和全局设置等操作。

Web 界面默认只允许本机访问：<http://127.0.0.1:8787>。

主要页面拥有可直接访问的路径：`/providers`、`/reliability`、`/schedules`、`/events`、`/redis`、`/diagnostics` 和 `/settings`。侧边栏导航会写入浏览器历史，因此前进、后退和刷新都会保持当前页面。

当前主要能力：

- **Provider 管理**：合并当前 CLI 配置、只读 CC Switch Redis 快照和 AES-GCM 加密的手填 Provider；支持默认代理、直连和自定义代理策略。
- **任务与计划**：支持单次/持续测活、单次/持续保活、批量启动与停止，以及按时区、星期和时间窗口执行的计划任务。
- **计划请求时间线**：从计划任务行进入该计划的请求日志，按最新请求在前展示状态、耗时、尝试次数、Job ID 和脱敏供应商返回；支持分页、刷新和状态筛选。
- **事件与可靠性**：普通生命周期事件与 CLI 请求日志分开浏览；可靠性页面提供 24 小时、7 天和 30 天 Provider 对比，并支持滚动 24 小时钉钉告警与恢复通知。
- **Redis 管理**：浏览全部 Key、常用数据类型、TTL、内存和版本信息，并执行受控更新、重命名、删除和应用快照预热。
- **主题与响应式**：深海终端、石墨信号和极昼控制台三套主题由 Redis 持久化，主要页面支持移动端和桌面端直接访问。

## 一键启动

需要 Docker Desktop（macOS）或 Docker Engine + Compose v2（Linux），并确保下面三个目录存在：

```bash
mkdir -p ~/.codex ~/.claude ~/.cc-switch
docker compose up -d --build
```

首次构建会下载 Go、Node.js 依赖，并在最终镜像中安装 Linux 版 `@openai/codex` 和 `@anthropic-ai/claude-code`。启动完成后打开：

```text
http://127.0.0.1:8787
```

检查状态：

```bash
docker compose ps
docker compose logs -f ai-watch
curl http://127.0.0.1:8787/api/health
```

停止服务：

```bash
docker compose down
```

`docker compose down` 会保留任务摘要和设置。需要同时删除 `/data` named volume 时使用：

```bash
docker compose down -v
```

## 配置挂载

Compose 默认把当前用户的配置目录只读挂载到容器：

| 宿主机 | 容器 | 用途 |
| --- | --- | --- |
| `~/.codex` | `/home/aiwatch/.codex` | Codex 配置与认证 |
| `~/.claude` | `/home/aiwatch/.claude` | Claude 配置与认证 |
| `~/.cc-switch` | `/home/aiwatch/.cc-switch` | CC Switch SQLite 启动同步源 |

应用不会修改这些目录。CC Switch SQLite 只会在 AI Watch 启动时读取并同步到 Redis；选择或启动 Provider 任务时不会再次查询 SQLite。任务所需配置只会从 Redis 快照或当前 CLI 配置复制到 `/run/ai-watch` 的任务专属临时目录。

### macOS

Docker Desktop 通常允许共享用户主目录。如果配置位于其他位置，请先在 Docker Desktop 的 **Settings → Resources → File Sharing** 中允许该路径，然后复制环境文件并填写绝对路径：

```bash
cp .env.example .env
```

```dotenv
CODEX_CONFIG_DIR=/Users/your-name/.codex
CLAUDE_CONFIG_DIR=/Users/your-name/.claude
CC_SWITCH_CONFIG_DIR=/Users/your-name/.cc-switch
```

### Linux

默认 `${HOME}` 挂载适用于以当前桌面用户执行 Compose 的情况。使用 `sudo` 可能把 `HOME` 变成 `/root`，建议直接以有 Docker 权限的用户运行，或在 `.env` 中配置绝对路径。

启用 SELinux 的发行版如果拒绝读取 bind mount，可为这些配置目录设置适当的容器只读访问标签。不要直接关闭 SELinux，也不要把配置复制进镜像。

## 环境变量

复制 [.env.example](.env.example) 后可调整：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `AI_WATCH_PORT` | `8787` | 宿主机本地端口，仍只绑定 `127.0.0.1` |
| `AI_WATCH_REDIS_URL` | `redis://redis:6379/0` | AI Watch 容器使用的 Redis 地址 |
| `AI_WATCH_REDIS_REQUIRED` | `true` | Redis 不可用时阻止 AI Watch 进入就绪状态 |
| `AI_WATCH_MASTER_KEY` | 自动生成 | 可选的 32 字节 base64/hex 主密钥；留空时在持久化数据卷生成 `/data/master.key`（`0600`） |
| `REDIS_IMAGE` | `redis:7-alpine` | Redis 镜像，可替换为内部镜像仓库地址 |
| `REDIS_PORT` | `6379` | Redis 映射到宿主机回环地址的端口 |
| `REDIS_MEM_LIMIT` | `512m` | Redis 容器的 Docker 内存上限 |
| `REDIS_MAX_MEMORY` | `384mb` | Redis `maxmemory`，低于容器上限以留出 AOF/运行开销 |
| `MIHOMO_IMAGE` | `metacubex/mihomo:latest` | Mihomo 镜像；生产环境建议固定已验证的版本标签或 digest |
| `MIHOMO_MEM_LIMIT` | `256m` | Mihomo 容器的 Docker 内存上限 |
| `MIHOMO_CONFIG_FILE` | `./config/mihomo/config.yaml.example` | 挂载到 Mihomo 的只读配置文件；含订阅密钥时应指向仓库外的绝对路径 |
| `AI_WATCH_DEFAULT_PROXY_URL` | `http://mihomo:7890` | Provider 选择“默认代理”时使用的服务端代理地址 |
| `AI_WATCH_HTTP_PROXY` / `AI_WATCH_HTTPS_PROXY` | `http://mihomo:7890` | 作为容器内 `HTTP_PROXY` / `HTTPS_PROXY` 注入 AI Watch 与 CLI；独立命名可避免误继承宿主机代理地址 |
| `AI_WATCH_ALL_PROXY` | `socks5://mihomo:7891` | 作为容器内 `ALL_PROXY` 注入的默认 SOCKS5 代理 |
| `AI_WATCH_NO_PROXY` | Compose 内部服务与本机地址 | 作为容器内 `NO_PROXY` 注入，绕过代理访问 `localhost`、Redis 和 Mihomo 等内部服务 |
| `CODEX_CONFIG_DIR` | `${HOME}/.codex` | Codex 配置绝对路径 |
| `CLAUDE_CONFIG_DIR` | `${HOME}/.claude` | Claude 配置绝对路径 |
| `CC_SWITCH_CONFIG_DIR` | `${HOME}/.cc-switch` | CC Switch 配置绝对路径；仅作为应用启动时的只读同步源 |
| `AI_WATCH_CC_SWITCH_SYNC_TIMEOUT_SECONDS` | `10` | 启动时复制并查询 CC Switch SQLite 的单次超时；失败会保留 Redis 上次成功快照 |
| `DINGTALK_WEBHOOK_URL` | 空 | 可选的服务端钉钉 Webhook，禁止提交到 Git |
| `NODE_BASE_IMAGE` | AWS Public ECR Node 22 | Node 构建与运行基础镜像，可自行替换 |
| `GO_BASE_IMAGE` | AWS Public ECR Go 1.24 | Go 构建基础镜像，可自行替换 |
| `CODEX_CLI_VERSION` | `latest` | 构建镜像时安装的 Codex npm 包版本 |
| `CLAUDE_CLI_VERSION` | `latest` | 构建镜像时安装的 Claude Code npm 包版本 |
| `DEBIAN_MIRROR` | 阿里云 Debian 镜像 | Debian 运行依赖下载源；海外环境可改为 `http://deb.debian.org` |
| `NPM_REGISTRY` | npmmirror | Codex 与 Claude CLI 下载源；海外环境可改为 `https://registry.npmjs.org` |

修改 CLI 版本后重新构建：

```bash
docker compose build --no-cache
docker compose up -d
```

## 测活代理（Mihomo）

Compose 默认启动 `mihomo` sidecar。`ai-watch` 通过内部 DNS 名称访问 `http://mihomo:7890`（HTTP/mixed）和 `socks5://mihomo:7891`（SOCKS5）；7890、7891、9090 均不映射到宿主机，因此不能从局域网或公网直接访问。AI Watch 会等待 Mihomo 配置校验健康后再启动。

仓库中的 [`config/mihomo/config.yaml.example`](config/mihomo/config.yaml.example) 是可直接启动的安全基线，所有流量默认走 `DIRECT`。要接入订阅或自建节点，建议把真实配置放到仓库之外，再在 `.env` 中填写绝对路径：

```dotenv
MIHOMO_CONFIG_FILE=/Users/your-name/.config/ai-watch/mihomo.yaml
```

Linux 可使用例如 `/home/your-name/.config/ai-watch/mihomo.yaml`。该文件必须在执行 `docker compose up` 前存在；Docker Desktop 用户还需允许共享其所在目录。配置更新后执行：

```bash
docker compose restart mihomo
docker compose ps mihomo
docker compose logs --tail=100 mihomo
```

订阅 URL 往往包含长期有效的访问密钥。不要把真实 URL、控制器密钥或代理账号密码写入仓库、提交到 Git、粘贴到公开日志，或直接放进 `.env.example`。Mihomo 下载的 provider 文件和缓存保存在私有 named volume `ai-watch-mihomo-data` 中；删除该 volume 前应确认不再需要其中数据。

默认配置把 AI Watch 的全局代理环境指向 Mihomo；Provider 可在应用内选择默认代理、直连或自定义代理。若整个部署不需要代理，可把相关变量留给应用的 Provider 级设置处理，但不要把 `mihomo` 的内部端口映射到宿主机来绕过该边界。

## CC Switch 启动同步

CC Switch SQLite 是外部配置的启动同步源，不是 AI Watch 的运行时数据库。每次 `ai-watch` 容器启动时，应用会读取只读挂载中的 Provider，将完整快照写入 Redis；API、页面、测活、保活和计划任务随后只读取 Redis，不会在任务启动阶段回查 SQLite。

同步遵循“成功后切换、失败时保留”的语义：

- 同步全部成功后，Redis 中的 CC Switch Provider 快照会原子替换为本次结果；CC Switch 中已删除的 Provider 也会从该快照移除。
- SQLite 不存在、暂时不可读或查询失败时，应用仍可启动，并继续使用 Redis 中最后一次成功快照。
- 如果从未成功同步过，则不会生成 CC Switch Provider；当前 CLI 配置和手填 Redis Provider 仍可正常使用。
- CC Switch Provider 在页面中保持只读。宿主机修改 CC Switch 后，重启 `ai-watch` 容器即可触发下一次同步：

```bash
docker compose restart ai-watch
docker compose logs --tail=100 ai-watch
```

“系统诊断”页面会显示同步项数、最后成功时间，以及当前是否正在使用上一次成功快照。告警只展示通用状态，不回显 SQLite 路径或凭据内容。

## 数据与隐私

- CLI 原始输出不会写入 `/data`、文件或数据库。
- 运行期间只在受限大小的内存缓冲中保存实时日志，用于 SSE 推送和短暂断线重连。
- 每轮分类完成后清空对应输出；整个任务结束后立即销毁剩余内存日志。
- `/run/ai-watch` 是 64 MiB 的 `tmpfs`。任务临时配置、凭据副本和运行文件只存在内存中，容器停止后必然消失。
- Redis 使用 AOF `everysec` 保存热更新设置、脱敏任务摘要、供应商示例、CC Switch Provider 运行快照、有界结构化事件，以及经 AES-GCM 加密的 Provider API Key、自定义代理 URL 和钉钉 Webhook；不保存 Prompt 或 CLI 原始输出。
- 结构化事件默认最多保留 30 天、5000 条和 8 MiB 逻辑内容，三项上限可在“设置与通知”中热更新。
- “事件记录”页面支持筛选和手动清空；清空事件不会删除设置、供应商示例或任务摘要。
- 计划任务启动的请求会把 `scheduleId` 写入结构化事件；计划请求时间线按该字段隔离不同计划。关联功能上线前的旧事件无法可靠反推全部历史，页面仅兼容回退到该计划最后一次 Job，并明确提示旧数据边界。
- SQLite 仅在应用启动阶段作为 CC Switch 同步源和旧版本迁移源读取；正常运行和任务启动阶段只使用 Redis。
- Codex、Claude 和 CC Switch 的宿主机目录均为只读挂载。
- 服务端口固定映射到 `127.0.0.1`，不会默认暴露到局域网或公网。
- Redis 通过 `127.0.0.1:${REDIS_PORT:-6379}` 提供宿主机访问，同时保留 Compose 内部地址 `redis:6379`；AOF 数据保存在独立的 `ai-watch-redis-data` volume。
- 测活任务的完整 CLI 日志会在写入前脱敏，并按任务缓存在 Redis 中 24 小时；每个任务最多保留 5000 条或约 2 MiB，保活任务不缓存完整输出。
- `request_end` 长期事件只保存有界、脱敏的响应摘要和请求元数据，用于请求详情、计划时间线、可靠性统计与告警；它不等同于完整 CLI 输出。
- Mihomo 的代理端口和控制器端口均未发布到宿主机，仅能通过 Compose 内部网络访问；运行数据保存在独立的 `ai-watch-mihomo-data` volume。

所有 Web UI 的 `POST`、`PUT`、`PATCH` 和 `DELETE` 请求默认携带 `Idempotency-Key`。服务端按方法、路径和请求体指纹防止重复写入；同 Key 不同请求返回 `409 idempotency_conflict`，Redis 幂等记录保留 24 小时。未携带该 Header 的旧客户端仍保持兼容。

### Redis 启动顺序与持久化

Compose 会先启动 `redis:7-alpine`，等待 `redis-cli ping` 健康检查通过后才启动 AI Watch。AI Watch 的存储层随后连接 Redis、执行一次性命名空间初始化/旧 SQLite 迁移，并在启动阶段尝试把 CC Switch Provider 原子同步到 Redis。CC Switch 同步失败不会阻止服务启动，而是保留并使用最后一次成功快照；Redis 初始化或核心数据预热失败时不会把 HTTP 服务标记为可用。

Redis 使用 AOF `appendfsync everysec`，并设置 `noeviction`：配置和运行元数据不会因为内存压力被静默淘汰。应用层仍负责事件、摘要和计划运行快照的数量/时间/字节上限；Redis 容器本身限制为 512 MiB（默认），AOF 文件位于命名卷中。

Redis 容器移除默认 capabilities，仅保留官方 entrypoint 从 root 降权到 `redis` 用户以及初始化数据卷所需的 `CHOWN`、`SETUID`、`SETGID`；Redis 仅发布到宿主机回环地址，代理不发布宿主机端口。

查看 Redis 状态（不暴露端口，仅通过 Compose exec）：

```bash
docker compose ps redis
docker compose exec redis redis-cli ping
redis-cli -h 127.0.0.1 -p "${REDIS_PORT:-6379}" ping
docker compose logs --tail=100 redis
```

删除 Redis 持久化数据前请确认已完成备份：

```bash
docker compose down
docker volume rm ai-watch_ai-watch-redis-data
```

## 容器结构

镜像使用三阶段构建：

1. Node.js 构建 React/Vite 前端。
2. Go 构建单个后端二进制。
3. 精简 Node.js Debian 运行时安装 Codex、Claude、`bubblewrap`、`sqlite3`、`curl`、`git` 与 `tini`；`sqlite3` 仅用于应用启动阶段的 CC Switch 同步和兼容迁移。

服务默认以容器内 root 用户运行。这是为了可靠读取宿主机中常见的 `0600` 配置文件；Linux bind mount 会保留宿主机 UID 和权限，固定的容器非 root UID 无法在不要求用户 `chmod`、ACL 或 UID 映射的前提下读取这些文件。

容器内 root 不会自动获得宿主机的完整 root 能力，但它仍是更高风险的容器内权限，并且能读取显式挂载进容器的文件。Compose 通过以下边界降低风险：

- 三个宿主机配置目录始终以只读方式挂载。
- Mihomo 配置以只读方式挂载，代理和控制器端口不发布到宿主机。
- 不挂载 Docker socket，也不启用 privileged 模式。
- 默认移除 Linux capabilities，仅重新加入 `SYS_ADMIN`，供 Codex 的 `bubblewrap` 创建隔离 namespace。
- 启用 `no-new-privileges`；同时放宽 Docker 默认 seccomp，以允许 bubblewrap 的 namespace/pivot-root 系统调用。
- 临时任务目录位于带 `noexec`、`nosuid`、`nodev` 的内存文件系统。
- 服务只发布到宿主机 `127.0.0.1`。

`tini` 负责正确转发停止信号和回收 CLI 子进程。不要向 Compose 额外挂载宿主机敏感目录或 Docker socket。

Codex 任务使用 `codex exec ... -` 的 stdin-only 形式传递 Prompt。这样可以规避 Codex CLI 在非 TTY 环境中同时收到 argv Prompt 与 stdin 时卡在 `Reading additional input from stdin...` 的已知问题。Prompt 仍不会进入结构化事件或 Redis；日志中只记录字节数和短哈希等安全摘要。

`SYS_ADMIN` 与 `seccomp:unconfined` 会降低容器自身的隔离强度，但这是 Docker Desktop 中运行 Codex Linux bubblewrap 沙箱的必要条件。AI Watch 通过只读配置挂载、无 Docker socket、非 privileged 模式、`no-new-privileges`、本机端口绑定和任务级只读沙箱降低风险。不要把该服务暴露到不可信网络。

Compose 还限制容器最多使用 2 GiB 内存和 256 个 PID，并把 Docker stdout 日志轮转为最多 3 个、每个 10 MiB。任务临时目录在启动时清理，任务完成后从内存 active map 移除并清空解析后的凭证与事件引用。

## 前端浏览器验收

前端使用 Playwright 和稳定的 API Mock 验证主题切换、Redis 刷新错误恢复、供应商小屏操作区、计划请求日志场景数据与响应式布局，不依赖真实 Redis 或 CLI。真实 Codex/Claude、代理和 Provider 可用性仍需通过运行中的 Compose 环境单独冒烟验证。

首次运行先安装 Chromium：

```bash
cd frontend
npm install
npm run test:e2e:install
```

执行生产构建和浏览器验收：

```bash
npm run build
npm run test:e2e
```

浏览器验收覆盖 320、375、768、1024 和 1440px。失败时截图、视频和 trace 会写入 `frontend/test-results/`，该目录不提交到 Git。

## 可靠性趋势

“可靠性”页面使用长期保存的脱敏 `request_end` 事件，对 24 小时、7 天或 30 天内的 Provider 进行比较。指标包括成功率、请求量、平均与 P95 延迟、失败类型分布、连续失败峰值和异常时段。

主动停止的请求会单独展示，不计入成功率或延迟统计。若选择的时间窗超过当前事件保留天数，页面会明确提示统计只有部分覆盖，不会把缺失数据表达成完整 SLA。

对应只读接口：

```text
GET /api/reliability?range=24h
GET /api/reliability?range=7d
GET /api/reliability?range=30d
```

可靠性告警可在“设置与通知”中启用。服务端会在每次请求结束后评估滚动 24 小时指标，支持成功率、当前连续失败和 P95 延迟阈值，并提供重复告警冷却与连续成功恢复通知。未配置钉钉时仍会写入结构化告警事件，但不会把消息标记为已送达。

## 计划任务请求日志

计划任务列表每一行都有请求日志入口。打开后按请求完成时间倒序展示当前计划 ID 产生的全部现存 `request_end` 事件，最新请求在前。

时间线包含：

- 请求状态、时间、耗时和尝试序号；
- Job ID、Request ID、CLI、Provider 和模型；
- 分类结果、退出码、错误类型；
- 经过脱敏和长度限制的供应商返回摘要。

时间线每页 50 条，支持上一页、下一页、刷新和状态筛选。数据遵循全局事件保留天数、条数与容量限制；保留策略删除的数据不会被空结果伪装成“从未运行”。

对应查询接口：

```text
GET /api/events?scheduleId=<schedule-id>&type=request_end&limit=50&offset=0
```

## Redis Value 展示规则

Redis String、Hash、List、Set 和 ZSet 中的 Value 只有在完整解析为 JSON Object（`{...}`）或 JSON Array（`[...]`）时才进入结构化树形 Viewer。数字、布尔值、`null`、JSON 字符串和普通文本都按原始文本展示，不标记为 `JSON Number` 等伪结构类型。

结构化 Viewer 支持节点折叠、全部展开、全部收起和复制原文；普通值使用轻量文本预览与复制操作。String 编辑模式仍允许保存任意文本，格式化按钮只对 Object/Array 生效。

## 常见问题

### Compose 报 bind source path does not exist

Docker 的长格式 bind mount 不会自动创建源目录。先创建目录，或在 `.env` 中指向真实的绝对路径：

```bash
mkdir -p ~/.codex ~/.claude ~/.cc-switch
```

### 页面显示 CLI 不可用

确认镜像内命令存在：

```bash
docker compose exec ai-watch codex --version
docker compose exec ai-watch claude --version
```

如果上游 npm 包更新导致不兼容，在 `.env` 中固定已验证版本，然后重建镜像。

### 页面显示配置不可读

检查 Compose 展开的挂载路径和容器内权限：

```bash
docker compose config
docker compose exec ai-watch sh -c 'ls -la ~/.codex ~/.claude ~/.cc-switch'
```

应用默认以容器内 root 读取这些只读挂载，因此无需修改宿主机密钥文件的 `0600` 权限。如果仍然出现拒绝访问，通常是 SELinux 标签或 Docker Desktop 文件共享策略导致；不要通过把密钥改成全局可读或可写来规避。

### Codex 日志停在 Reading additional input from stdin

当前代码已使用 stdin-only 方式启动 Codex。若更新后仍看到旧行为，通常是容器仍在运行旧镜像，先重建并确认版本：

```bash
docker compose up -d --build ai-watch
docker compose exec -T ai-watch codex --version
docker compose logs --tail=100 ai-watch
```

修复后 Codex 输出会越过 `Reading additional input from stdin...` 并进入真实模型请求。若随后仍在任务超时时间结束，说明 stdin 阻塞已经解除，但 Provider 或模型响应时间超过当前“单次超时”；可在任务或计划设置中适当提高到 45–60 秒。

### CC Switch 修改后页面没有更新

运行期 Provider 来自 Redis 快照，不会持续监听或查询 CC Switch SQLite。修改宿主机 CC Switch 后重启应用容器触发同步：

```bash
docker compose restart ai-watch
```

如果同步失败，页面会继续显示最后一次成功快照。可在“系统诊断”查看通用同步状态，并通过 `docker compose logs --tail=100 ai-watch` 排查只读挂载、文件共享或 SQLite 可读性问题。

### 健康检查失败

查看容器日志和健康检查详情：

```bash
docker compose logs ai-watch
docker inspect --format '{{json .State.Health}}' ai-watch-ai-watch-1
```

确认宿主机的 `8787` 端口未被占用，或在 `.env` 中修改 `AI_WATCH_PORT`。
