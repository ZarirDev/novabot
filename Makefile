SHELL := /bin/bash
ROOT  := $(shell pwd)

export CGO_ENABLED  = 1
export CGO_LDFLAGS  = -L$(ROOT)/.dev/lib
export LD_LIBRARY_PATH = $(ROOT)/.dev/lib
export MODEL_PATH   = $(ROOT)/.dev/models/default
export MIC_DEVICE ?= pulse
export SPEAKER_DEVICE ?= pulse

.PHONY: run build test vet docker clean nuke

## primary dev loop
run: .dev/lib/libvosk.so .dev/models/default
	@bash scripts/run-dev.sh

build: .dev/lib/libvosk.so .dev/models/default
	@go build -o .dev/novabot ./cmd/novabot
	@echo "✓ .dev/novabot"

test: .dev/lib/libvosk.so .dev/models/default
	@go test ./...

vet: .dev/lib/libvosk.so
	@go vet ./...

## parity build (real container)
docker:
	@docker compose build
	@docker compose up -d
	@docker compose logs -f novabot

## hygiene
clean:
	@go clean -cache -testcache
	@rm -f .dev/novabot

nuke: clean
	@rm -rf .dev

## setup targets (idempotent)
.dev/lib/libvosk.so .dev/models/default:
	@bash scripts/setup-dev.sh