#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEV="$ROOT/.dev"
VOSK_VERSION="0.3.45"
MODEL="vosk-model-small-en-us-0.15"

command -v unzip >/dev/null || { echo "need unzip"; exit 1; }
command -v curl  >/dev/null || { echo "need curl";  exit 1; }

mkdir -p "$DEV/lib" "$DEV/models"

if [[ ! -f "$DEV/lib/libvosk.so" ]]; then
  echo "→ fetching libvosk ${VOSK_VERSION}"
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  curl -fsSL -o "$tmp/v.zip" \
    "https://github.com/alphacep/vosk-api/releases/download/v${VOSK_VERSION}/vosk-linux-x86_64-${VOSK_VERSION}.zip"
  unzip -q "$tmp/v.zip" -d "$tmp"
  cp "$tmp/vosk-linux-x86_64-${VOSK_VERSION}/libvosk.so" "$DEV/lib/"
else
  echo "✓ libvosk"
fi

if [[ ! -d "$DEV/models/default" ]]; then
  echo "→ fetching model ${MODEL}"
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  curl -fsSL -o "$tmp/m.zip" "https://alphacephei.com/vosk/models/${MODEL}.zip"
  unzip -q "$tmp/m.zip" -d "$tmp"
  mv "$tmp/${MODEL}" "$DEV/models/default"
else
  echo "✓ model"
fi