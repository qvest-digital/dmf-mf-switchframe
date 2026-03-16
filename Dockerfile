# Stage 1: Build UI
FROM node:22-bookworm-slim AS ui-builder
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: Build Go
FROM golang:1.25-bookworm AS go-builder

# Install cgo dependencies (FFmpeg + fdk-aac for audio)
# fdk-aac is in Debian non-free
RUN sed -i 's/Components: main/Components: main non-free/' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        libavcodec-dev \
        libavutil-dev \
        libavformat-dev \
        libswscale-dev \
        libswresample-dev \
        libx264-dev \
        libfdk-aac-dev \
        pkg-config && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY server/ ./server/

# Copy UI build directly (not a symlink) for go:embed
COPY --from=ui-builder /app/ui/build ./server/cmd/switchframe/ui

RUN cd server && go build -tags embed_ui -o /switchframe ./cmd/switchframe

# Stage 3: Runtime
FROM debian:bookworm-slim

RUN sed -i 's/Components: main/Components: main non-free/' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        libavcodec59 \
        libavformat59 \
        libswscale6 \
        libswresample4 \
        libx264-164 \
        libfdk-aac2 \
        ca-certificates \
        curl && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --create-home switchframe

COPY --from=go-builder /switchframe /usr/local/bin/switchframe

USER switchframe
EXPOSE 8080
EXPOSE 9090
# SRT listener mode (configurable via --srt-port)
EXPOSE 9000/udp
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:9090/health || exit 1
ENTRYPOINT ["switchframe", "--admin-addr=0.0.0.0:9090"]
