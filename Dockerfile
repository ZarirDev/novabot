# ---------- Build Stage ----------
FROM golang:1.22-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG VOSK_VERSION=0.3.45

# Prebuilt libvosk.so (x86_64)
RUN curl -fsSL -o /tmp/vosk.zip \
      "https://github.com/alphacep/vosk-api/releases/download/v${VOSK_VERSION}/vosk-linux-x86_64-${VOSK_VERSION}.zip" \
    && unzip -q /tmp/vosk.zip -d /opt \
    && mv /opt/vosk-linux-x86_64-${VOSK_VERSION} /opt/vosk \
    && rm /tmp/vosk.zip

WORKDIR /app
COPY go.mod ./
COPY . .

ENV CGO_ENABLED=1
ENV CGO_LDFLAGS="-L/opt/vosk -lvosk -ldl -lpthread -Wl,-rpath,/usr/local/lib"

RUN go build -o novabot ./cmd/novabot

# ---------- Runtime Stage ----------
FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    alsa-utils ca-certificates curl unzip \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /opt/vosk/libvosk.so /usr/local/lib/libvosk.so
RUN ldconfig

ARG VOSK_MODEL=vosk-model-small-en-us-0.15
RUN mkdir -p /opt/vosk-models \
    && curl -fsSL -o /tmp/model.zip "https://alphacephei.com/vosk/models/${VOSK_MODEL}.zip" \
    && unzip -q /tmp/model.zip -d /opt/vosk-models \
    && mv /opt/vosk-models/${VOSK_MODEL} /opt/vosk-models/default \
    && rm /tmp/model.zip

WORKDIR /app
COPY --from=builder /app/novabot /app/novabot

# Keep RSS sane on the shared 8 GB box
ENV MODEL_PATH=/opt/vosk-models/default
ENV DETECTOR_TYPE=vosk
ENV GOMEMLIMIT=300MiB
ENV GOGC=50

EXPOSE 8080 4000/udp

ENTRYPOINT ["/app/novabot"]