# AI Watch

AI Watch 是 `ai-watch.sh` 的 Docker Web 客户端。它在容器内运行 Codex 和 Claude CLI，提供测活、保活、当前配置与 CC Switch Provider 选择、任务停止、实时状态、通知和设置等操作。

Web 界面默认只允许本机访问：<http://127.0.0.1:8787>。

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
| `~/.cc-switch` | `/home/aiwatch/.cc-switch` | CC Switch SQLite 数据库 |

应用不会修改这些目录。选择 Provider 后，所需配置只会复制到 `/run/ai-watch` 的任务专属临时目录。

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
| `CODEX_CONFIG_DIR` | `${HOME}/.codex` | Codex 配置绝对路径 |
| `CLAUDE_CONFIG_DIR` | `${HOME}/.claude` | Claude 配置绝对路径 |
| `CC_SWITCH_CONFIG_DIR` | `${HOME}/.cc-switch` | CC Switch 配置绝对路径 |
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

## 数据与隐私

- CLI 原始输出不会写入 `/data`、文件或数据库。
- 运行期间只在受限大小的内存缓冲中保存实时日志，用于 SSE 推送和短暂断线重连。
- 每轮分类完成后清空对应输出；整个任务结束后立即销毁剩余内存日志。
- `/run/ai-watch` 是 64 MiB 的 `tmpfs`。任务临时配置、凭据副本和运行文件只存在内存中，容器停止后必然消失。
- `/data/ai-watch.db` 使用内嵌 SQLite 保存热更新设置、脱敏任务摘要、非敏感供应商示例和有界结构化事件，不保存 Prompt、API Key、Webhook 或 CLI 原始输出。
- 结构化事件默认最多保留 30 天、5000 条和 8 MiB 逻辑内容，三项上限可在“设置与通知”中热更新。
- “事件记录”页面支持筛选和手动清空；清空事件不会删除设置、供应商示例或任务摘要。
- 从旧版本升级时，`settings.json` 和 `summaries.json` 会一次性导入 SQLite，成功后自动删除旧文件，避免重复数据长期残留。
- Codex、Claude 和 CC Switch 的宿主机目录均为只读挂载。
- 服务端口固定映射到 `127.0.0.1`，不会默认暴露到局域网或公网。

## 容器结构

镜像使用三阶段构建：

1. Node.js 构建 React/Vite 前端。
2. Go 构建单个后端二进制。
3. 精简 Node.js Debian 运行时安装 Codex、Claude、`bubblewrap`、`sqlite3`、`curl`、`git` 与 `tini`。

服务默认以容器内 root 用户运行。这是为了可靠读取宿主机中常见的 `0600` 配置文件；Linux bind mount 会保留宿主机 UID 和权限，固定的容器非 root UID 无法在不要求用户 `chmod`、ACL 或 UID 映射的前提下读取这些文件。

容器内 root 不会自动获得宿主机的完整 root 能力，但它仍是更高风险的容器内权限，并且能读取显式挂载进容器的文件。Compose 通过以下边界降低风险：

- 三个宿主机配置目录始终以只读方式挂载。
- 不挂载 Docker socket，也不启用 privileged 模式。
- 默认移除 Linux capabilities，仅重新加入 `SYS_ADMIN`，供 Codex 的 `bubblewrap` 创建隔离 namespace。
- 启用 `no-new-privileges`；同时放宽 Docker 默认 seccomp，以允许 bubblewrap 的 namespace/pivot-root 系统调用。
- 临时任务目录位于带 `noexec`、`nosuid`、`nodev` 的内存文件系统。
- 服务只发布到宿主机 `127.0.0.1`。

`tini` 负责正确转发停止信号和回收 CLI 子进程。不要向 Compose 额外挂载宿主机敏感目录或 Docker socket。

`SYS_ADMIN` 与 `seccomp:unconfined` 会降低容器自身的隔离强度，但这是 Docker Desktop 中运行 Codex Linux bubblewrap 沙箱的必要条件。AI Watch 通过只读配置挂载、无 Docker socket、非 privileged 模式、`no-new-privileges`、本机端口绑定和任务级只读沙箱降低风险。不要把该服务暴露到不可信网络。

Compose 还限制容器最多使用 2 GiB 内存和 256 个 PID，并把 Docker stdout 日志轮转为最多 3 个、每个 10 MiB。任务临时目录在启动时清理，任务完成后从内存 active map 移除并清空解析后的凭证与事件引用。

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

### 健康检查失败

查看容器日志和健康检查详情：

```bash
docker compose logs ai-watch
docker inspect --format '{{json .State.Health}}' ai-watch-ai-watch-1
```

确认宿主机的 `8787` 端口未被占用，或在 `.env` 中修改 `AI_WATCH_PORT`。
