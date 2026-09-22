SHELL := /bin/bash
ROOT  := $(shell pwd)

BIN     := $(ROOT)/bin
RUNTIME := $(ROOT)/runtime
MODELS  := $(ROOT)/models

# ── cgo ────────────────────────────────────────────────
# Both onnxruntime_go (server) and malgo (client) require CGO.
# onnxruntime_go uses dlopen at runtime via SetSharedLibraryPath,
# so there is no link-time dependency on libonnxruntime — only the
# C shim needs -ldl.
export CGO_ENABLED = 1
export CGO_LDFLAGS = -ldl

# ── runtime config ─────────────────────────────────────
export HTTP_PORT            ?= 8080
export UDP_AUDIO_PORT       ?= 4000
export WAKE_WORD            ?= nova
export WAKE_MODEL_FILE      ?= hey_nova.onnx
export OWW_MODEL_DIR         = $(MODELS)
export ONNX_RUNTIME_PATH     = $(RUNTIME)/libonnxruntime.so
export MIC_DEVICE           ?= pulse
export SPEAKER_DEVICE       ?= pulse
export AUDIO_QUALITY        ?= standard
export OMP_NUM_THREADS      ?= 1
export WAKE_THRESHOLD       ?= 0.5
export WAKE_PATIENCE        ?= 0
export WAKE_SILENCE_RMS     ?= 400
export WAKE_SILENCE_FRAMES  ?= 25

.PHONY: all setup build client run test vet clean install uninstall

all: build

## first-time setup — downloads ONNX Runtime and openWakeWord shared models
setup:
	@bash scripts/setup.sh

## production server binary
build: setup
	@go build -trimpath -ldflags="-s -w" -o $(BIN)/novabot ./cmd/novabot
	@echo "✓ $(BIN)/novabot"

## PC_AUDIO client binary (also needs CGO for miniaudio)
client: setup
	@go build -trimpath -ldflags="-s -w" -o $(BIN)/novabot-client ./cmd/novabot-client
	@echo "✓ $(BIN)/novabot-client"

## dev loop
run: build
	@$(BIN)/novabot

test:
	@go test ./...

vet:
	@go vet ./...

clean:
	@go clean -cache -testcache
	@rm -f $(BIN)/novabot $(BIN)/novabot-client

## install systemd service (must run on the target machine)
install: build
	@bash scripts/install-service.sh

uninstall:
	@bash scripts/uninstall-service.sh