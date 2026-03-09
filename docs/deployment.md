# Switchframe Production Deployment Guide

This guide covers building, configuring, and deploying Switchframe in production environments.

## Table of Contents

- [Building](#building)
- [Docker](#docker)
- [Configuration](#configuration)
- [Network and Ports](#network-and-ports)
- [TLS](#tls)
- [Authentication](#authentication)
- [Monitoring](#monitoring)
- [Hardware Acceleration](#hardware-acceleration)
- [SRT Output](#srt-output)
- [Recording](#recording)
- [Reverse Proxy](#reverse-proxy)
- [Production Logging](#production-logging)
- [Security Hardening](#security-hardening)

---

## Building

### Single Binary (Recommended)

The `make build` target produces a single self-contained binary with the UI embedded:

```bash
make build
# Output: bin/switchframe
```

This:
1. Runs `npm ci && npm run build` in the `ui/` directory to produce the SvelteKit static build.
2. Creates a symlink at `server/cmd/switchframe/ui` pointing to `ui/build/`.
3. Compiles the Go binary with `-tags embed_ui`, which activates `//go:embed ui` in `embed_prod.go`.

The resulting binary serves the entire application (API + UI + WebTransport) with no external file dependencies.

### Build Tags

| Tag | Effect |
|-----|--------|
| `embed_ui` | Embeds the SvelteKit build into the binary. Without this tag, `uiHandler()` returns nil and no static files are served. |
| `noffmpeg` | Disables FFmpeg cgo bindings. Transitions and codec probing become no-ops. |
| `openh264` | Enables the OpenH264 fallback encoder/decoder (requires OpenH264 shared library). |
| `mxl` | Enables MXL shared-memory transport (requires MXL SDK). Without this tag, MXL features return `ErrMXLNotAvailable`. |

### MXL Build (Shared-Memory Transport)

To build with MXL SDK support for shared-memory video/audio I/O:

```bash
export MXL_ROOT=$HOME/dev/mxl/install/Darwin-Clang-Release  # macOS
# export MXL_ROOT=$HOME/dev/mxl/install/Linux-GCC-Release   # Linux

make build-server-mxl
# Output: bin/switchframe (with MXL support, no embedded UI)
```

This adds `-tags "cgo mxl"` and sets `PKG_CONFIG_PATH` to resolve `libmxl`. The MXL SDK must be built and installed at `MXL_ROOT`. See the [MXL Integration Guide](mxl.md) for full details.

### Dev Build (No UI Embed)

For development iteration where the Vite dev server proxies to Go:

```bash
make build-server
# Output: bin/switchframe (API only, no embedded UI)
```

### Build Dependencies

The Go build requires cgo and the following C libraries:

- **libavcodec** and **libavutil** (FFmpeg) -- video encode/decode
- **libx264** -- H.264 software encoding
- **libfdk-aac** -- AAC audio encode/decode
- **pkg-config** -- for cgo flag resolution

On Debian/Ubuntu:
```bash
apt-get install -y libavcodec-dev libavutil-dev libx264-dev libfdk-aac-dev pkg-config
```

On macOS (Homebrew):
```bash
brew install ffmpeg fdk-aac pkg-config
```

---

## Docker

### Building the Image

```bash
make docker
# or directly:
docker build -t switchframe .
```

The Dockerfile uses a three-stage build:

1. **ui-builder** (`node:22-bookworm-slim`) -- Builds the SvelteKit frontend.
2. **go-builder** (`golang:1.25-bookworm`) -- Installs cgo dependencies and compiles the Go binary with `embed_ui`.
3. **runtime** (`debian:bookworm-slim`) -- Minimal image with only runtime libraries, runs as non-root `switchframe` user.

### Exposed Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| `8080` | UDP (QUIC) | HTTP/3 + WebTransport + MoQ + REST API + embedded UI |
| `8080` | TCP | HTTP/3 Alt-Svc advertisement (same port) |
| `9090` | TCP | Admin server (metrics, health, cert-hash, pprof) |
| `9000` | UDP | SRT listener mode (when enabled) |

Note: Port 8081 (plain HTTP API, enabled via `--http-fallback`) is not exposed in the Dockerfile by default. Add it if you need TCP-based API access.

### Docker Run

```bash
docker run -d \
  --name switchframe \
  -p 8080:8080/udp \
  -p 8080:8080/tcp \
  -p 8081:8081/tcp \
  -p 9090:9090/tcp \
  -p 9000:9000/udp \
  -e SWITCHFRAME_API_TOKEN=your-secret-token-here \
  -e APP_ENV=production \
  -v /data/recordings:/recordings \
  switchframe \
    --log-level info \
    --admin-addr :9090
```

### Docker Compose Example

```yaml
version: "3.8"

services:
  switchframe:
    image: switchframe:latest
    build: .
    ports:
      - "8080:8080/udp"    # QUIC / WebTransport / MoQ
      - "8080:8080/tcp"    # HTTP/3 Alt-Svc
      - "8081:8081/tcp"    # Plain HTTP API
      - "9090:9090/tcp"    # Admin (metrics, health, pprof)
      - "9000:9000/udp"    # SRT listener
    environment:
      SWITCHFRAME_API_TOKEN: "${SWITCHFRAME_API_TOKEN}"
      APP_ENV: production
    volumes:
      - recordings:/recordings
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:9090/health"]
      interval: 30s
      timeout: 5s
      retries: 3
    restart: unless-stopped
    # GPU passthrough for NVENC (NVIDIA):
    # deploy:
    #   resources:
    #     reservations:
    #       devices:
    #         - driver: nvidia
    #           count: 1
    #           capabilities: [gpu]

volumes:
  recordings:
```

---

## Configuration

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--demo` | `false` | Start with 4 simulated camera sources (disables auth) |
| `--demo-video <dir>` | `""` | Directory of MPEG-TS clips for real video in demo mode (requires `--demo`) |
| `--log-level <level>` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--admin-addr <addr>` | `:9090` | Admin/metrics server listen address |
| `--api-token <token>` | auto-generated | Bearer token for API authentication |
| `--http-fallback` | `false` | Start a plain HTTP/1.1 API server on TCP :8081 for curl/scripts |
| `--tls-cert <path>` | `""` | Path to TLS certificate PEM file (e.g., from mkcert) |
| `--tls-key <path>` | `""` | Path to TLS private key PEM file |
| `--format <preset>` | `1080p29.97` | Video standard (e.g., `1080p29.97`, `1080p25`, `720p59.94`) |
| `--frame-sync` | `false` | Enable freerun frame synchronizer (aligns sources to common tick boundary) |
| `--frc-quality <mode>` | `""` | Frame rate conversion quality (e.g., `mcfi` for motion-compensated) |
| `--decode-all-sources` | `false` | Enable always-on per-source H.264 decoders (instant cuts, no IDR gating) |
| `--raw-program-monitor` | `false` | Enable raw YUV program monitor on `"program-raw"` MoQ track |
| `--raw-monitor-scale <res>` | `""` | Downscale raw monitor output (e.g., `720p`, `480p`, `360p`) |
| `--replay-buffer-secs <n>` | `60` | Per-source replay buffer duration in seconds (0 to disable, max 300) |
| `--mxl-sources <specs>` | `""` | Comma-separated MXL source specs: `videoUUID`, `videoUUID:audioUUID`, or `videoUUID:audioUUID:dataUUID` (requires `mxl` build tag) |
| `--mxl-output <name>` | `""` | MXL flow name for program output (empty = disabled) |
| `--mxl-output-video-def <path>` | `""` | Path to NMOS IS-04 video flow definition JSON for program output |
| `--mxl-output-audio-def <path>` | `""` | Path to NMOS IS-04 audio flow definition JSON for program output |
| `--mxl-domain <path>` | `/dev/shm/mxl` | MXL shared memory domain directory path |
| `--mxl-discover` | `false` | List available MXL flows and exit (diagnostic tool) |
| `--scte35` | `false` | Enable SCTE-35 splice_insert and time_signal injection into MPEG-TS output |
| `--scte35-pid` | `258` | SCTE-35 PID in MPEG-TS output (decimal, default 0x102) |
| `--scte35-preroll` | `4000` | Default pre-roll time in milliseconds for scheduled cues |
| `--scte35-heartbeat` | `5000` | Heartbeat interval in ms (splice_null, 0 to disable) |
| `--scte35-verify` | `true` | Verify SCTE-35 encoding by round-trip decode |
| `--scte35-webhook` | `""` | Webhook URL for async SCTE-35 event notifications |
| `--scte104` | `false` | Enable SCTE-104 on MXL data flows (requires `--scte35` and MXL build) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `SWITCHFRAME_API_TOKEN` | API authentication token. Overridden by `--api-token` flag if both are set. If neither is set, a random 64-character hex token is auto-generated and printed to stdout. |
| `SWITCHFRAME_MXL_SOURCES` | MXL source specs (same format as `--mxl-sources`). Overridden by the flag if both are set. |
| `APP_ENV` | Set to `production` for JSON-formatted log output (structured logging). Any other value (or unset) produces human-readable text logs. |

### Token Resolution Order

1. `--api-token` flag (highest priority)
2. `SWITCHFRAME_API_TOKEN` environment variable
3. Auto-generated 32-byte random hex token (printed to stdout at startup)

### Fixed Addresses

These listen addresses are not currently configurable via flags:

| Address | Purpose |
|---------|---------|
| `:8080` | Main server (QUIC/HTTP3 + WebTransport + REST API) |
| `:8081` | Plain HTTP/TCP API mirror (opt-in via `--http-fallback`) |

---

## Network and Ports

### Port Summary

| Port | Protocol | Direction | Purpose |
|------|----------|-----------|---------|
| **8080** | UDP | Inbound | QUIC/HTTP3, WebTransport, MoQ subscriptions, REST API, embedded UI |
| **8080** | TCP | Inbound | HTTP/3 Alt-Svc advertisement |
| **8081** | TCP | Inbound | Plain HTTP REST API (opt-in via `--http-fallback`, same endpoints as 8080) |
| **9090** | TCP | Inbound | Admin: Prometheus `/metrics`, `/health`, `/ready`, `/api/cert-hash`, `/debug/pprof/*` |
| **9000** | UDP | Inbound | SRT listener mode (pull connections from downstream) |
| **Ephemeral** | UDP | Outbound | SRT caller mode (push to upstream/platform) |

### Firewall Rules

Minimal production firewall (assuming operators access via reverse proxy on 443):

```bash
# WebTransport / MoQ (required for browser clients)
iptables -A INPUT -p udp --dport 8080 -j ACCEPT

# HTTP/3 Alt-Svc (browsers discover QUIC via TCP first)
iptables -A INPUT -p tcp --dport 8080 -j ACCEPT

# Plain HTTP API (only if --http-fallback is enabled)
iptables -A INPUT -p tcp --dport 8081 -j ACCEPT

# Admin (restrict to monitoring network)
iptables -A INPUT -p tcp --dport 9090 -s 10.0.0.0/8 -j ACCEPT

# SRT listener (only when using pull mode)
iptables -A INPUT -p udp --dport 9000 -j ACCEPT
```

For SRT caller mode (push), no inbound rule is needed -- the server initiates the outbound UDP connection.

---

## TLS

### Self-Signed Certificates (Default)

At startup, Switchframe generates a self-signed TLS certificate using Prism's `certs.Generate()`:

- **Validity:** 14 days (WebTransport requires short-lived self-signed certs)
- **Fingerprint:** Logged at startup and available via `GET /api/cert-hash`
- **Usage:** Browsers use the fingerprint to trust the self-signed cert for WebTransport connections

The certificate fingerprint is served unauthenticated at `/api/cert-hash` on the admin server (port 9090) and the QUIC server (port 8080):

```bash
curl http://localhost:9090/api/cert-hash
# {"hash":"abc123...","addr":":8080","trusted":false}
```

### Trusted Certificates with mkcert (Recommended for Development)

[mkcert](https://github.com/FiloSottile/mkcert) generates locally-trusted certificates, enabling direct HTTP/3 access from browsers without fingerprint pinning. This is the recommended setup for development:

```bash
# One-time setup
make setup-mkcert

# Start with trusted cert
./bin/switchframe --tls-cert ~/.switchframe/cert.pem --tls-key ~/.switchframe/key.pem
```

The `make setup-mkcert` target:
1. Installs the mkcert CA into the system trust store (`mkcert -install`)
2. Generates a certificate for `localhost`, `127.0.0.1`, and `::1`
3. Saves to `~/.switchframe/cert.pem` and `~/.switchframe/key.pem`

When `--tls-cert` and `--tls-key` are provided, Switchframe uses the specified certificate instead of generating a self-signed one. The `trusted` field in the `/api/cert-hash` response will be `true`, and browsers can connect over HTTP/3 directly without needing the certificate hash.

The `make demo` target automatically detects mkcert certificates and uses them if present. Otherwise it falls back to `--http-fallback` mode with a self-signed cert.

### Production TLS

For production with real TLS certificates (e.g., from Let's Encrypt), you have two options:

**Option 1: Direct certificate provisioning.** Use `--tls-cert` and `--tls-key` to provide CA-signed certificates directly to Switchframe. Browsers connect over HTTP/3 (QUIC) without fingerprint pinning.

**Option 2: Reverse proxy.** Place a reverse proxy (Caddy, nginx) in front of Switchframe. The reverse proxy terminates TLS and forwards traffic:

- HTTPS on port 443 proxies to Switchframe's port 8081 (REST API, requires `--http-fallback`)
- WebTransport/QUIC requires direct UDP access to port 8080 (the browser uses the self-signed cert fingerprint)

### Certificate Renewal

Self-signed certificates are regenerated every time the server restarts. For long-running deployments, restart the server at least every 14 days to refresh the certificate. Browsers will re-fetch the fingerprint from `/api/cert-hash` automatically.

When using `--tls-cert`/`--tls-key`, certificate renewal is your responsibility. Restart the server after updating the certificate files.

---

## Authentication

### Token-Based Auth

All `/api/*` endpoints (except `/api/cert-hash`) require a Bearer token:

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" https://localhost:8080/api/switch/state
```

Authentication uses `crypto/subtle.ConstantTimeCompare` for timing-safe token validation.

### Exempt Paths

These paths bypass authentication:
- `/api/cert-hash` -- Required for WebTransport bootstrapping
- `/health` -- Liveness probe (admin server, port 9090)
- `/metrics` -- Prometheus scraping (admin server, port 9090)

### Generating a Token

**Option 1: Let the server auto-generate one.**
Start without `--api-token` or `SWITCHFRAME_API_TOKEN`. The token is printed to stdout:

```
  API Token: a1b2c3d4e5f6...
```

Capture it from stdout (not stderr, so it does not leak into log files):

```bash
switchframe 2>/var/log/switchframe.log | head -5
```

**Option 2: Generate ahead of time.**
Use any method to create a random hex string:

```bash
# 32 bytes = 64 hex characters (same format as auto-generated)
openssl rand -hex 32
```

Then pass it via environment variable or flag:

```bash
export SWITCHFRAME_API_TOKEN=$(openssl rand -hex 32)
switchframe
```

### Demo Mode

When started with `--demo`, authentication is completely disabled. All API requests are accepted without a token. This is intended for local testing only -- never use `--demo` in production.

---

## Monitoring

### Prometheus Metrics

Metrics are served at `http://localhost:9090/metrics` (admin server) with OpenMetrics format enabled.

**HTTP metrics** (all API requests):
- `switchframe_http_requests_total{method, pattern, status}` -- Request counter
- `switchframe_http_request_duration_seconds{method, pattern}` -- Latency histogram

**Switcher metrics:**
- `switchframe_cuts_total` -- Hard cuts performed
- `switchframe_transitions_completed_total{type}` -- Completed transitions (mix, dip, ftb)
- `switchframe_idr_gate_events_total` -- IDR gate activations after cuts
- `switchframe_idr_gate_duration_seconds` -- Time spent waiting for keyframes

**Audio mixer metrics:**
- `switchframe_mixer_frames_mixed_total` -- Audio frames decoded/mixed/encoded
- `switchframe_mixer_encode_errors_total` -- Audio encode failures
- `switchframe_mixer_passthrough_bypass_total` -- Frames bypassed (zero-CPU passthrough)

**Output metrics:**
- `switchframe_output_ringbuf_overflows_total` -- SRT ring buffer overflows
- `switchframe_output_srt_reconnects_total` -- SRT reconnection attempts
- `switchframe_output_recording_bytes_total` -- Bytes written to recordings
- `switchframe_output_srt_bytes_total` -- Bytes sent via SRT

**Source health metrics:**
- `switchframe_source_status_changes_total{source, from_status, to_status}` -- Health transitions

**Go runtime metrics** (via `collectors.NewGoCollector()`):
- `go_goroutines`, `go_memstats_*`, `go_gc_*`, etc.

**Process metrics** (via `collectors.NewProcessCollector()`):
- `process_cpu_seconds_total`, `process_resident_memory_bytes`, `process_open_fds`, etc.

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: switchframe
    scrape_interval: 15s
    static_configs:
      - targets: ["switchframe:9090"]
```

### Health Checks

**Liveness probe** -- always returns 200 if the process is running:
```bash
curl http://localhost:9090/health
# {"status":"ok"}
```

**Readiness probe** -- returns 503 during startup, 200 once all components are initialized:
```bash
curl http://localhost:9090/ready
# {"status":"ready"}    (200)
# {"status":"not_ready"} (503, during startup)
```

The Docker HEALTHCHECK uses the liveness endpoint:
```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:9090/health || exit 1
```

For Kubernetes, use the readiness probe for service routing:
```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 5
readinessProbe:
  httpGet:
    path: /ready
    port: 9090
  initialDelaySeconds: 3
```

### pprof

Go profiling endpoints are available at the admin server:

```bash
# CPU profile (30 seconds)
go tool pprof http://localhost:9090/debug/pprof/profile?seconds=30

# Heap profile
go tool pprof http://localhost:9090/debug/pprof/heap

# Goroutine dump
curl http://localhost:9090/debug/pprof/goroutine?debug=2

# All registered profiles
curl http://localhost:9090/debug/pprof/
```

### Debug Snapshot

A comprehensive system snapshot is available via the API (requires auth):

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8081/api/debug/snapshot
```

Returns JSON with state from all subsystems: switcher, mixer, output, and demo stats.

---

## Hardware Acceleration

### Codec Auto-Detection

At startup, Switchframe probes available H.264 encoder backends in priority order:

1. **NVENC** (`h264_nvenc`) -- NVIDIA GPU (requires CUDA)
2. **VA-API** (`h264_vaapi`) -- Intel/AMD GPU (Linux)
3. **VideoToolbox** (`h264_videotoolbox`) -- macOS (Apple Silicon/Intel)
4. **libx264** -- Software fallback (always available with FFmpeg)
5. **OpenH264** -- Last resort (requires `openh264` build tag and shared library)

The probe creates a tiny 64x64 test encoder, encodes one gray frame, and verifies output. The first successful candidate is cached for the process lifetime.

The selected codec is logged at startup:
```
level=INFO msg="video codec selected" encoder=h264_videotoolbox decoder=h264
```

### NVIDIA GPU Setup

For NVENC acceleration in Docker:

1. Install the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/).
2. Run with GPU access:

```bash
docker run --gpus 1 \
  -p 8080:8080/udp -p 8080:8080/tcp -p 9090:9090 \
  switchframe
```

Or in Compose:

```yaml
deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia
          count: 1
          capabilities: [gpu]
```

The runtime image needs NVIDIA driver libraries. You may need to extend the Dockerfile to install `libnvidia-encode` or use an NVIDIA base image.

### Intel VA-API Setup

For VA-API on Linux (Intel Quick Sync or AMD):

```bash
# Install runtime libraries
apt-get install -y vainfo libva2 intel-media-va-driver-non-free

# Verify
vainfo
```

In Docker, pass the render device:
```bash
docker run --device /dev/dri/renderD128:/dev/dri/renderD128 \
  switchframe
```

### macOS VideoToolbox

VideoToolbox is automatically available on macOS (both Intel and Apple Silicon). No additional setup is needed when building natively.

### Disabling Hardware Acceleration

To force software-only encoding, build with the `noffmpeg` tag and `openh264` tag, or ensure no GPU drivers are installed. The probe will fall through to libx264 or OpenH264.

---

## SRT Output

Switchframe supports two SRT modes for streaming the program output:

### Caller Mode (Push)

Pushes MPEG-TS to a remote SRT receiver (e.g., a streaming platform or media server). The server initiates the outbound connection.

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"mode":"caller","address":"ingest.example.com","port":9000,"latency":120}' \
  http://localhost:8081/api/output/srt/start
```

- **Reconnection:** Exponential backoff from 1s to 30s max.
- **Ring buffer:** 4 MB buffer during reconnection. If it overflows, buffered data is discarded and output waits for the next keyframe.
- **No inbound firewall rule needed.**

### Listener Mode (Pull)

Accepts incoming SRT connections (up to 8 simultaneous by default). Downstream clients pull the program stream.

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"mode":"listener","port":9000,"latency":120}' \
  http://localhost:8081/api/output/srt/start
```

- **Port:** Requires UDP port 9000 (or configured port) to be open inbound.
- **Fan-out:** Each connected client gets its own buffered channel. Slow clients are dropped rather than stalling the pipeline.
- **Max connections:** 8 (hardcoded default).

### SRT Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | required | `"caller"` or `"listener"` |
| `address` | string | required (caller) | Remote host for caller mode |
| `port` | int | required | Remote port (caller) or local listen port (listener) |
| `latency` | int | `120` | SRT latency in milliseconds |
| `streamID` | string | `""` | SRT stream ID (caller mode only) |

### Stopping SRT Output

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8081/api/output/srt/stop
```

---

## Recording

### Starting a Recording

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"outputDir":"/recordings","rotateAfterMins":60,"maxFileSizeMB":2048}' \
  http://localhost:8081/api/recording/start
```

### Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `outputDir` | string | OS temp dir + `switchframe-recordings/` | Absolute path for output files |
| `rotateAfterMins` | int | `60` (1 hour) | Time-based file rotation in minutes. 0 disables. |
| `maxFileSizeMB` | int | `0` (unlimited) | Size-based file rotation in megabytes. 0 disables. |

### File Naming

Files are written as MPEG-TS (`.ts`) with the naming pattern:

```
program_YYYYMMDD_HHMMSS_001.ts
program_YYYYMMDD_HHMMSS_002.ts  (after rotation)
program_YYYYMMDD_HHMMSS_003.ts  (etc.)
```

The timestamp is fixed at recording start; only the index increments on rotation.

### Why MPEG-TS?

MPEG-TS is crash-resilient -- there is no moov atom or file header that must be finalized on clean shutdown. If the process crashes mid-recording, the file is still playable up to the last written frame. This is the same format used for SRT output.

### Docker Volume for Recordings

Mount a host directory or named volume at the recording path:

```bash
docker run -v /data/recordings:/recordings switchframe
```

Then start recording with `"outputDir": "/recordings"`.

### Stopping a Recording

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8081/api/recording/stop
```

---

## Reverse Proxy

### Considerations for WebTransport/QUIC

Switchframe uses HTTP/3 (QUIC) for WebTransport and MoQ. This has important implications for reverse proxies:

1. **QUIC is UDP.** Most reverse proxies are TCP-only. You need a proxy that supports UDP passthrough or HTTP/3 termination.
2. **WebTransport requires direct QUIC access.** The browser connects to the QUIC endpoint directly using a certificate fingerprint.
3. **REST API works over plain HTTP.** Port 8081 (enabled via `--http-fallback`) serves the same API over regular TCP and can be proxied normally.

### Recommended Approach

Use the reverse proxy for the REST API and static UI only. Let browsers connect to the QUIC endpoint directly:

```
Browser --> Reverse Proxy (443/TCP) --> Switchframe :8081 (REST API)
Browser --> Switchframe :8080 (UDP, direct QUIC/WebTransport)
```

### Caddy Example

```caddyfile
switchframe.example.com {
    # REST API proxy
    handle /api/* {
        reverse_proxy localhost:8081
    }

    # Serve UI from the same binary (if using embed_ui build)
    # If separate, proxy to port 8081 for the embedded UI
    handle {
        reverse_proxy localhost:8081
    }
}
```

Browsers fetch the cert fingerprint from `https://switchframe.example.com/api/cert-hash`, then establish a WebTransport connection directly to `switchframe.example.com:8080` using that fingerprint.

### nginx Example

```nginx
server {
    listen 443 ssl;
    server_name switchframe.example.com;

    ssl_certificate     /etc/letsencrypt/live/switchframe.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/switchframe.example.com/privkey.pem;

    # REST API
    location /api/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Request-ID $request_id;
    }

    # Embedded UI (SPA fallback)
    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
    }
}
```

**Note:** nginx does not support HTTP/3 upstream proxying for WebTransport. The QUIC endpoint must be accessed directly by clients on port 8080.

### CORS

All `/api/*` endpoints include CORS headers (`Access-Control-Allow-Origin: *`, `Access-Control-Allow-Methods`, `Access-Control-Allow-Headers`) via `CORSMiddleware`. This enables cross-origin access from the Vite dev server (port 5173) to the Go server (port 8080) during development. Preflight `OPTIONS` requests are handled automatically. In production behind a reverse proxy, the proxy's CORS configuration takes precedence.

---

## Production Logging

### Structured JSON Logging

Set `APP_ENV=production` for JSON-formatted structured logs on stderr:

```bash
APP_ENV=production switchframe --log-level info
```

Output:
```json
{"time":"2026-03-05T10:00:00Z","level":"INFO","msg":"switchframe starting","log_level":"info"}
{"time":"2026-03-05T10:00:00Z","level":"INFO","msg":"certificate generated","fingerprint":"abc123...","expires":"2026-03-19T10:00:00Z"}
```

Without `APP_ENV=production`, logs use human-readable text format:
```
time=2026-03-05T10:00:00Z level=INFO msg="switchframe starting" log_level=info
```

### Log Levels

| Level | Use |
|-------|-----|
| `debug` | Verbose: every HTTP request (including polled endpoints like `/api/switch/state`), codec probe details |
| `info` | Normal operation: startup, cuts, source registration, SRT connect/disconnect |
| `warn` | Recoverable issues: SRT write failures, ring buffer overflow, stream registered before init |
| `error` | Failures: admin server bind, shutdown errors |

### Noisy Path Suppression

The logger automatically downgrades frequently-polled paths to `debug` level:
- `GET /api/switch/state` -- Polled by browsers as REST fallback
- `GET /metrics` -- Prometheus scrape

At `info` level, these requests do not appear in logs.

### Separating Token from Logs

The API token is printed to **stdout** (not stderr) at startup. Route stderr to your log aggregator and capture stdout separately:

```bash
switchframe 2>/var/log/switchframe.log 1>/var/run/switchframe-token.txt
```

---

## Security Hardening

### Non-Root Docker User

The Docker image creates and runs as a dedicated `switchframe` system user:

```dockerfile
RUN useradd --system --no-create-home switchframe
USER switchframe
```

The binary runs without root privileges. Ensure mounted volumes are writable by the `switchframe` user (UID assigned by the system, typically 999).

```bash
# Make recording directory writable
chown 999:999 /data/recordings
# or use a named volume (Docker handles permissions)
```

### Token Management

- **Never commit tokens to version control.** Use environment variables or secret management (Vault, AWS Secrets Manager, Kubernetes Secrets).
- **Rotate tokens** by restarting with a new `SWITCHFRAME_API_TOKEN`. All existing API clients must update.
- **Use long tokens.** The auto-generated token is 64 hex characters (256 bits of entropy). Match this if generating your own.
- **Timing-safe comparison.** The auth middleware uses `crypto/subtle.ConstantTimeCompare`, preventing timing side-channel attacks.

### Network Isolation

- **Admin server (9090):** Restrict to your monitoring network. It exposes pprof, which can leak heap contents and goroutine stacks. Never expose to the public internet.
- **Plain HTTP API (8081):** Only active when `--http-fallback` is enabled. If exposed externally, always place behind a TLS-terminating reverse proxy.
- **SRT ports:** Open only the specific port you configure. Listener mode accepts up to 8 connections by default.

### Preset Storage

Persistent data is stored as JSON files in `~/.switchframe/`:

| File | Contents |
|------|----------|
| `presets.json` | Saved switcher presets (program/preview/audio state) |
| `macros.json` | Macro definitions (sequential action lists) |
| `operators.json` | Registered operators (name, role, token) |
| `scte35_rules.json` | SCTE-35 signal conditioning rules and default action |

In Docker (non-root user with no home dir), these paths resolve based on the `switchframe` user. Mount a volume if data needs to persist across container restarts:

```bash
docker run -v switchframe-data:/home/switchframe/.switchframe switchframe
```

### GC and System Tuning

Switchframe sets `GOGC=400` by default (if the `GOGC` environment variable is not already set). This reduces GC frequency by triggering collection at 5x live heap instead of the default 2x, trading memory for fewer latency spikes during real-time frame processing. Override with `GOGC=100` (Go default) or `GOGC=off` to disable.

At startup, `logSystemTuning()` checks `RLIMIT_NOFILE` and logs a warning if the file descriptor limit is below 65536. On Linux:

```bash
ulimit -n 65536
```

### Recommended Production Checklist

- [ ] Set `SWITCHFRAME_API_TOKEN` explicitly (do not rely on auto-generation)
- [ ] Set `APP_ENV=production` for structured JSON logging
- [ ] Set `--log-level info` (avoid `debug` in production)
- [ ] Restrict port 9090 to internal/monitoring networks
- [ ] Use `--tls-cert`/`--tls-key` with CA-signed certs, or place behind a TLS reverse proxy with `--http-fallback`
- [ ] Mount persistent storage for `/recordings` if recording is used
- [ ] Verify hardware acceleration probe at startup (`"video codec selected"` log line)
- [ ] Configure Prometheus scraping on port 9090
- [ ] Set up alerting on `switchframe_output_ringbuf_overflows_total` and `switchframe_output_srt_reconnects_total`
- [ ] Test the readiness probe (`/ready`) in your orchestrator
- [ ] Size replay buffer memory appropriately (`--replay-buffer-secs` × N sources × ~bitrate)
- [ ] Configure operator tokens if using multi-operator mode (tokens persist in `operators.json`)
- [ ] Plan for server restart every 14 days (self-signed TLS certificate renewal) or use `--tls-cert`/`--tls-key` with CA-signed certs
