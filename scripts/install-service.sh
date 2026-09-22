#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SERVICE="novabot"
SYSTEMD_DIR="/etc/systemd/system"

if [[ ! -x "$ROOT/bin/novabot" ]]; then
    echo "binary not found — run 'make build' first"
    exit 1
fi

# ── .env ───────────────────────────────────────────────
if [[ ! -f "$ROOT/.env" ]]; then
    echo "→ creating $ROOT/.env"
    cat > "$ROOT/.env" <<EOF
HTTP_PORT=8080
UDP_AUDIO_PORT=4000
WAKE_WORD=nova
WAKE_MODEL_FILE=hey_nova.onnx
OWW_MODEL_DIR=$ROOT/models
ONNX_RUNTIME_PATH=$ROOT/runtime/libonnxruntime.so
MIC_DEVICE=pulse
SPEAKER_DEVICE=pulse
AUDIO_QUALITY=standard
OMP_NUM_THREADS=1
WAKE_THRESHOLD=0.7
WAKE_PATIENCE=2
WAKE_SILENCE_RMS=400
WAKE_SILENCE_FRAMES=25
PULSE_SERVER=unix:/run/user/$(id -u)/pulse/native
XDG_RUNTIME_DIR=/run/user/$(id -u)
PULSE_LATENCY_MSEC=100
AUDIO_STATS_LOG=1
WAKEWORD_LOG=1
EOF
fi

# ── service unit ───────────────────────────────────────
sudo tee "$SYSTEMD_DIR/$SERVICE.service" > /dev/null <<EOF
[Unit]
Description=Novabot voice assistant
After=network.target sound.target
Wants=sound.target

[Service]
Type=simple
User=$(id -un)
WorkingDirectory=$ROOT
EnvironmentFile=$ROOT/.env
ExecStart=$ROOT/bin/novabot
Restart=on-failure
RestartSec=3
SupplementaryGroups=audio
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable "$SERVICE.service"

echo "✓ installed"
echo "  start:   sudo systemctl start $SERVICE"
echo "  status:  sudo systemctl status $SERVICE"
echo "  logs:    sudo journalctl -u $SERVICE -f"