# ── build stage ────────────────────────────────────────
FROM golang:1.27-bookworm AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev curl unzip ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG VOSK_VERSION=0.3.45
ARG ORT_VERSION=1.29.0

# Vosk
RUN curl -fsSL -o /tmp/vosk.zip \
      "https://github.com/alphacep/vosk-api/releases/download/v${VOSK_VERSION}/vosk-linux-x86_64-${VOSK_VERSION}.zip" \
    && unzip -q /tmp/vosk.zip -d /opt \
    && mv /opt/vosk-linux-x86_64-${VOSK_VERSION} /opt/vosk \
    && rm /tmp/vosk.zip

# ONNX Runtime
RUN curl -fsSL -o /tmp/ort.tgz \
      "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz" \
    && tar -xzf /tmp/ort.tgz -C /opt \
    && mv /opt/onnxruntime-linux-x64-${ORT_VERSION} /opt/ort \
    && rm /tmp/ort.tgz

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ENV CGO_ENABLED=1
ENV CGO_LDFLAGS="-L/opt/vosk -L/opt/ort/lib -lvosk -ldl -lpthread -Wl,-rpath,/usr/local/lib"
ENV CGO_LDFLAGS_ALLOW=".*"

RUN go build -o novabot ./cmd/novabot

# ── runtime stage ──────────────────────────────────────
FROM debian:trixie-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    alsa-utils ca-certificates curl unzip mpv python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

# yt-dlp from pip — always current
RUN pip3 install --break-system-packages --no-cache-dir yt-dlp

# native libraries
COPY --from=builder /opt/vosk/libvosk.so /usr/local/lib/libvosk.so
COPY --from=builder /opt/ort/lib/libonnxruntime.so /usr/local/lib/libonnxruntime.so
RUN ldconfig

# models — Vosk
ARG VOSK_MODEL=vosk-model-small-en-us-0.15
RUN mkdir -p /opt/vosk-models \
    && curl -fsSL -o /tmp/model.zip "https://alphacephei.com/vosk/models/${VOSK_MODEL}.zip" \
    && unzip -q /tmp/model.zip -d /opt/vosk-models \
    && mv /opt/vosk-models/${VOSK_MODEL} /opt/vosk-models/default \
    && rm /tmp/model.zip

# models — openWakeWord shared
RUN mkdir -p /opt/oww-models \
    && for f in melspectrogram.onnx embedding_model.onnx silero_vad.onnx; do \
         curl -fsSL -o "/opt/oww-models/$f" \
           "https://github.com/dscripka/openWakeWord/releases/download/v0.5.1/$f"; \
       done

# user-supplied wake model — expected in the build context root
COPY hey_nova.onnx /opt/oww-models/hey_nova.onnx

WORKDIR /app
COPY --from=builder /app/novabot /app/novabot

ENV MODEL_PATH=/opt/vosk-models/default \
    OWW_MODEL_DIR=/opt/oww-models \
    ONNX_RUNTIME_PATH=/usr/local/lib/libonnxruntime.so \
    WAKE_MODEL_FILE=hey_nova.onnx \
    DETECTOR_TYPE=openwakeword \
    MIC_DEVICE=pulse \
    SPEAKER_DEVICE=pulse \
    OMP_NUM_THREADS=1 \
    GOMEMLIMIT=300MiB \
    GOGC=50

EXPOSE 8080 4000/udp

ENTRYPOINT ["/app/novabot"]