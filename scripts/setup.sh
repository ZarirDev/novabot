#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ORT_VERSION="1.29.0"
ORT_PLATFORM="linux"
ORT_ARCH="x64"
OWW_VERSION="0.5.1"
OWW_BASE="https://github.com/dscripka/openWakeWord/releases/download/v${OWW_VERSION}"

command -v curl >/dev/null || { echo "need curl"; exit 1; }
command -v tar  >/dev/null || { echo "need tar";  exit 1; }

mkdir -p "$ROOT/bin" "$ROOT/runtime" "$ROOT/models"

# ── ONNX Runtime ────────────────────────────────────────
if [[ ! -f "$ROOT/runtime/libonnxruntime.so" ]]; then
    echo "→ fetching ONNX Runtime ${ORT_VERSION}"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    curl -fsSL -o "$tmp/ort.tgz" \
        "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-${ORT_PLATFORM}-${ORT_ARCH}-${ORT_VERSION}.tgz"
    tar -xzf "$tmp/ort.tgz" -C "$tmp"
    cp "$tmp/onnxruntime-${ORT_PLATFORM}-${ORT_ARCH}-${ORT_VERSION}/lib/libonnxruntime.so" "$ROOT/runtime/"
else
    echo "✓ onnxruntime"
fi

# ── openWakeWord shared models ─────────────────────────
for f in melspectrogram.onnx embedding_model.onnx silero_vad.onnx; do
    if [[ ! -f "$ROOT/models/$f" ]]; then
        echo "→ fetching openWakeWord $f"
        curl -fsSL -o "$ROOT/models/$f" "${OWW_BASE}/$f"
    else
        echo "✓ models/$f"
    fi
done

# ── wake model ─────────────────────────────────────────
if [[ ! -f "$ROOT/models/hey_nova.onnx" ]]; then
    # The model is committed in the repo root — move it into models/
    if [[ -f "$ROOT/hey_nova.onnx" ]]; then
        mv "$ROOT/hey_nova.onnx" "$ROOT/models/hey_nova.onnx"
        echo "→ moved hey_nova.onnx into models/"
    else
        echo "⚠  models/hey_nova.onnx is missing — place your wake model there"
    fi
fi

# ── system tools check ─────────────────────────────────
missing=""
for bin in mpv yt-dlp pactl aplay arecord; do
    command -v "$bin" >/dev/null 2>&1 || missing="$missing $bin"
done
if [[ -n "$missing" ]]; then
    echo "⚠  missing system tools:$missing"
    echo "   install: sudo apt install mpv pulseaudio-utils alsa-utils && pipx install yt-dlp"
fi