# Stage 1: Build UI
FROM node:22-bookworm-slim AS ui-builder
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: Build Go
FROM golang:1.25-bookworm AS go-builder

# Install cgo dependencies from non-free repos
RUN echo 'deb http://deb.debian.org/debian bookworm non-free' >> /etc/apt/sources.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        libfdk-aac-dev \
        libopenh264-dev \
        pkg-config && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY server/ ./server/

# Copy UI build directly (not a symlink) for go:embed
COPY --from=ui-builder /app/ui/build ./server/cmd/switchframe/ui

RUN cd server && go build -tags embed_ui -o /switchframe ./cmd/switchframe

# Stage 3: Runtime
FROM debian:bookworm-slim

RUN echo 'deb http://deb.debian.org/debian bookworm non-free' >> /etc/apt/sources.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        libfdk-aac2 \
        libopenh264-7 \
        ca-certificates \
        curl && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --no-create-home switchframe

COPY --from=go-builder /switchframe /usr/local/bin/switchframe

USER switchframe
EXPOSE 8080
EXPOSE 9090
# SRT listener mode (configurable via --srt-port)
EXPOSE 9000/udp
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:9090/health || exit 1
ENTRYPOINT ["switchframe"]
