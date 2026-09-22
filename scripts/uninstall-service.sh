#!/usr/bin/env bash
set -euo pipefail

sudo systemctl disable --now novabot.service 2>/dev/null || true
sudo rm -f /etc/systemd/system/novabot.service
sudo systemctl daemon-reload
echo "✓ uninstalled"