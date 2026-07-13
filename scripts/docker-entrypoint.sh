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

exec "$@"
