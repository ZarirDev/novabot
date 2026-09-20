#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEV="$ROOT/.dev"

VOSK_VERSION="0.3.45"
ORT_VERSION="1.29.0"          # ← was 1.26.0; matches onnxruntime_go v1.35+
ORT_ARCH="x64"
ORT_PLATFORM="linux"
VOSK_MODEL="vosk-model-small-en-us-0.15"

command -v unzip >/dev/null || { echo "need unzip"; exit 1; }
command -v curl  >/dev/null || { echo "need curl";  exit 1; }
command -v tar   >/dev/null || { echo "need tar";   exit 1; }

mkdir -p "$DEV/lib" "$DEV/runtime" "$DEV/models" "$DEV/models/oww"

# ---------- Vosk shared library ----------
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

# ---------- Vosk model ----------
if [[ ! -d "$DEV/models/default" ]]; then
    echo "→ fetching Vosk model ${VOSK_MODEL}"
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    curl -fsSL -o "$tmp/m.zip" "https://alphacephei.com/vosk/models/${VOSK_MODEL}.zip"
    unzip -q "$tmp/m.zip" -d "$tmp"
    mv "$tmp/${VOSK_MODEL}" "$DEV/models/default"
else
    echo "✓ Vosk model"
fi

# ---------- ONNX Runtime ----------
if [[ ! -f "$DEV/runtime/libonnxruntime.so" ]]; then
    echo "→ fetching ONNX Runtime ${ORT_VERSION}"
    tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
    curl -fsSL -o "$tmp/ort.tgz" \
        "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-${ORT_PLATFORM}-${ORT_ARCH}-${ORT_VERSION}.tgz"
    tar -xzf "$tmp/ort.tgz" -C "$tmp"
    cp -r "$tmp/onnxruntime-${ORT_PLATFORM}-${ORT_ARCH}-${ORT_VERSION}/lib/"* "$DEV/runtime/"
    rm -rf "$DEV/runtime/cmake" "$DEV/runtime/pkgconfig"
else
    echo "✓ onnxruntime"
fi

# ---------- openWakeWord shared models ----------
OWW_BASE="https://github.com/dscripka/openWakeWord/releases/download/v0.5.1"
for f in melspectrogram.onnx embedding_model.onnx silero_vad.onnx; do
    if [[ ! -f "$DEV/models/oww/$f" ]]; then
        echo "→ fetching openWakeWord $f"
        curl -fsSL -o "$DEV/models/oww/$f" "${OWW_BASE}/$f"
    else
        echo "✓ oww/$f"
    fi
done

# ---------- User-supplied wake model ----------
# Copy any .onnx wake model from the project root into .dev/models/oww/
# so `make nuke` doesn't destroy your hand-trained models.
for f in "$ROOT"/*.onnx; do
    [[ -e "$f" ]] || continue
    base="$(basename "$f")"
    case "$base" in
        melspectrogram.onnx|embedding_model.onnx|silero_vad.onnx) continue ;;
    esac
    if [[ ! -f "$DEV/models/oww/$base" ]]; then
        echo "→ copying user model $base → .dev/models/oww/"
        cp "$f" "$DEV/models/oww/$base"
    fi
done

# Sanity check: the wake model the Makefile expects is present
if [[ ! -f "$DEV/models/oww/${WAKE_MODEL_FILE:-}" ]]; then
    if [[ -n "${WAKE_MODEL_FILE:-}" ]]; then
        echo "⚠  WAKE_MODEL_FILE=$WAKE_MODEL_FILE not found in $DEV/models/oww/"
        echo "   Available: $(ls "$DEV/models/oww/" 2>/dev/null | tr '\n' ' ')"
    fi
fi