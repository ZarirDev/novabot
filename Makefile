SHELL := /bin/bash
ROOT  := $(shell pwd)
DEV   := $(ROOT)/.dev

# ── cgo ────────────────────────────────────────────────
export CGO_ENABLED     = 1
export CGO_LDFLAGS     = -L$(DEV)/lib -lvosk -ldl -lpthread
export LD_LIBRARY_PATH = $(DEV)/lib
export CGO_LDFLAGS_ALLOW = .*

# ── model paths ────────────────────────────────────────
export MODEL_PATH         = $(DEV)/models/default
export ONNX_RUNTIME_PATH  = $(DEV)/runtime/libonnxruntime.so
export OWW_MODEL_DIR      = $(DEV)/models/oww
export WAKE_MODEL_FILE    = hey_nova.onnx

# ── audio devices ──────────────────────────────────────
export MIC_DEVICE      ?= pulse
export SPEAKER_DEVICE  ?= pulse
export AUDIO_QUALITY   ?= standard

# ── wake-word tuning ───────────────────────────────────
export DETECTOR_TYPE         ?= openwakeword
export WAKE_WORD             ?= nova
export WAKE_SILENCE_RMS      ?= 400
export WAKE_SILENCE_FRAMES   ?= 25
export OMP_NUM_THREADS       ?= 1

# ── runtime ────────────────────────────────────────────
export HTTP_PORT       ?= 8080
export UDP_AUDIO_PORT  ?= 4000

.PHONY: all run build test vet docker setup clean nuke

all: build

## primary dev loop
run: setup
	@go build -o $(DEV)/novabot ./cmd/novabot
	@exec $(DEV)/novabot

build: setup
	@go build -o $(DEV)/novabot ./cmd/novabot
	@echo "✓ $(DEV)/novabot"

test: setup
	@go test ./...

vet: setup
	@go vet ./...

## PC_AUDIO client — build natively for the host
client: setup
	@go build -o $(DEV)/novabot-client ./cmd/novabot-client
	@echo "✓ $(DEV)/novabot-client"

## cross-compile the client for Windows (requires mingw-w64)
client-win: setup
	@CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
	 CC=x86_64-w64-mingw32-gcc \
	 CGO_LDFLAGS="" \
	 go build -o $(DEV)/novabot-client.exe ./cmd/novabot-client
	@echo "✓ $(DEV)/novabot-client.exe"

docker:
	@docker compose build
	@docker compose up -d
	@docker compose logs -f novabot

## idempotent — safe to run every time
setup:
	@bash scripts/setup-dev.sh

clean:
	@go clean -cache -testcache
	@rm -f $(DEV)/novabot $(DEV)/novabot-client

nuke: clean
	@rm -rf $(DEV)