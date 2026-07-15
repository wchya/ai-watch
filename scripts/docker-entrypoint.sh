#!/bin/sh
set -eu

umask 077

if [ ! -d "${AI_WATCH_DATA_DIR:-/data}" ]; then
  echo "AI Watch data directory is unavailable: ${AI_WATCH_DATA_DIR:-/data}" >&2
  exit 1
fi

if [ ! -d "${AI_WATCH_RUNTIME_DIR:-/run/ai-watch}" ]; then
  echo "AI Watch runtime directory is unavailable: ${AI_WATCH_RUNTIME_DIR:-/run/ai-watch}" >&2
  exit 1
fi

if command -v codex >/dev/null 2>&1; then
  AI_WATCH_CODEX_CLI_VERSION="$(codex --version 2>/dev/null | tail -n 1 || true)"
  export AI_WATCH_CODEX_CLI_VERSION
fi

if command -v claude >/dev/null 2>&1; then
  AI_WATCH_CLAUDE_CLI_VERSION="$(claude --version 2>/dev/null | tail -n 1 || true)"
  export AI_WATCH_CLAUDE_CLI_VERSION
fi

exec "$@"
