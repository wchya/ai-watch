ARG NODE_BASE_IMAGE=public.ecr.aws/docker/library/node:22-bookworm-slim
ARG GO_BASE_IMAGE=public.ecr.aws/docker/library/golang:1.24-bookworm

FROM ${NODE_BASE_IMAGE} AS frontend-builder
WORKDIR /src/frontend

COPY frontend/package*.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install

COPY frontend/ ./
RUN npm run build

FROM ${GO_BASE_IMAGE} AS backend-builder
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/ai-watch ./cmd/ai-watch

FROM ${NODE_BASE_IMAGE} AS runtime

ARG CODEX_CLI_VERSION=latest
ARG CLAUDE_CLI_VERSION=latest
ARG DEBIAN_MIRROR=http://mirrors.aliyun.com
ARG NPM_REGISTRY=https://registry.npmmirror.com
ARG TARGETARCH

RUN sed -i "s|http://deb.debian.org|${DEBIAN_MIRROR}|g; s|http://deb.debian.org/debian-security|${DEBIAN_MIRROR}/debian-security|g" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Retries=5 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        bubblewrap \
        curl \
        git \
        sqlite3 \
        tini \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global --registry="${NPM_REGISTRY}" \
        "@openai/codex@${CODEX_CLI_VERSION}" \
        "@anthropic-ai/claude-code@${CLAUDE_CLI_VERSION}" \
    && CODEX_RESOLVED_VERSION="$(node -p "require('/usr/local/lib/node_modules/@openai/codex/package.json').version")" \
    && case "${TARGETARCH}" in \
         arm64) npm install --global --registry="${NPM_REGISTRY}" "@openai/codex-linux-arm64@npm:@openai/codex@${CODEX_RESOLVED_VERSION}-linux-arm64" ;; \
         amd64) npm install --global --registry="${NPM_REGISTRY}" "@openai/codex-linux-x64@npm:@openai/codex@${CODEX_RESOLVED_VERSION}-linux-x64" ;; \
         *) echo "Unsupported Docker architecture: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && npm cache clean --force \
    && install -d -m 0700 /app /data /run/ai-watch /home/aiwatch

WORKDIR /app

COPY --from=backend-builder /out/ai-watch /app/ai-watch
COPY --from=frontend-builder /src/frontend/dist /app/web
COPY --chmod=0755 scripts/docker-entrypoint.sh /usr/local/bin/ai-watch-entrypoint
RUN sed -i 's/\r$//' /usr/local/bin/ai-watch-entrypoint

ENV HOME=/home/aiwatch \
    AI_WATCH_ADDR=0.0.0.0:8787 \
    AI_WATCH_DATA_DIR=/data \
    AI_WATCH_RUNTIME_DIR=/run/ai-watch \
    AI_WATCH_WEB_DIR=/app/web \
    CODEX_HOME=/home/aiwatch/.codex \
    CLAUDE_CONFIG_DIR=/home/aiwatch/.claude

EXPOSE 8787

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD curl --fail --silent --show-error http://127.0.0.1:8787/api/health >/dev/null || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/ai-watch-entrypoint"]
CMD ["/app/ai-watch"]
