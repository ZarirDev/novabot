SHELL := /bin/bash
ROOT  := $(shell pwd)

# ---- cgo / Vosk ----
export CGO_ENABLED     = 1
export CGO_LDFLAGS     = -L$(ROOT)/.dev/lib
export LD_LIBRARY_PATH = $(ROOT)/.dev/lib
export MODEL_PATH      = $(ROOT)/.dev/models/default

# ---- audio devices ----
export MIC_DEVICE      ?= pulse
export SPEAKER_DEVICE  ?= pulse

# ---- wake word backend ----
export DETECTOR_TYPE      ?= openwakeword
export ONNX_RUNTIME_PATH  ?= $(ROOT)/.dev/runtime/libonnxruntime.so
export OWW_MODEL_DIR      ?= $(ROOT)/.dev/models/oww
export WAKE_MODEL_FILE    ?= hey_nova.onnx

# ---- runtime tuning (optional overrides) ----
# export WAKE_CONFIDENCE       ?= 0.75
# export WAKE_COOLDOWN_SECONDS ?= 1.5
# export WAKE_VAD_THRESHOLD    ?= 500

.PHONY: run build test vet docker clean nuke setup

## primary dev loop — always runs setup first (it's idempotent)
run: setup
	@bash scripts/run-dev.sh

build: setup
	@go build -o .dev/novabot ./cmd/novabot
	@echo "✓ .dev/novabot"

test: setup
	@go test ./...

vet: setup
	@go vet ./...

docker:
	@docker compose build
	@docker compose up -d
	@docker compose logs -f novabot

## setup is cheap when everything already exists (just a few `test -f` calls)
setup:
	@bash scripts/setup-dev.sh

clean:
	@go clean -cache -testcache
	@rm -f .dev/novabot

## nuke wipes .dev/ except for user-supplied wake models placed by setup-dev.sh
nuke: clean
	@rm -rf .dev