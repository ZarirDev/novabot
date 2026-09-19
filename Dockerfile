# Build binary
FROM golang:1.22-bookworm AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o novabot ./cmd/novabot

# Production Debian 13 runtime
FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    alsa-utils \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/novabot /app/novabot

EXPOSE 8080 4000/udp

ENTRYPOINT ["/app/novabot"]