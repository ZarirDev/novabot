#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

export CGO_ENABLED=1
export CGO_LDFLAGS="-L${ROOT}/.dev/lib"
export LD_LIBRARY_PATH="${ROOT}/.dev/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"

export HTTP_PORT="${HTTP_PORT:-8080}"
export UDP_AUDIO_PORT="${UDP_AUDIO_PORT:-4000}"
export WAKE_WORD="${WAKE_WORD:-nova}"
export DETECTOR_TYPE="${DETECTOR_TYPE:-vosk}"
export MODEL_PATH="${MODEL_PATH:-${ROOT}/.dev/models/default}"

go build -o "$ROOT/.dev/novabot" ./cmd/novabot
exec "$ROOT/.dev/novabot"