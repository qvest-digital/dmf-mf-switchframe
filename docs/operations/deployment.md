# Deployment

> **Where you are:** [docs](../README.md) → operations → deployment
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [../reference/config-and-flags.md](../reference/config-and-flags.md) · [../reference/metrics.md](../reference/metrics.md) · [../concepts/gpu.md](../concepts/gpu.md)

**TL;DR** Switchframe ships as a single Go binary plus an embedded SvelteKit UI. Production runs on bare-metal Linux with an NVIDIA GPU and systemd or Nomad process supervision — not Kubernetes. Development runs on macOS or Linux with `make demo`, which spins up four simulated cameras on `localhost:5173`. Browsers reach the server over QUIC/HTTP3 (UDP `:8080`); operations tools reach it over TCP `:9090` for metrics, health, and pprof. For trusted HTTPS during development, run `make setup-mkcert` once to install a mkcert CA. For production, provide your own TLS certificate via `--tls-cert` and `--tls-key`.

## The story

Switchframe is designed to run as a single Unix process. There is a Go binary (`switchframe`) that opens a QUIC listener on `:8080`, an admin HTTP listener on `:9090`, optionally an HTTP/1.1 fallback on `:8081`, and optionally an SRT listener on `:6464`. It talks to a GPU if one is present, falls back to CPU if not, and writes all persistent state under `~/.switchframe/`. The operator supervises it with systemd (or Nomad, or anything else that can run a binary and restart it on crash).

Everything else — the UI, the sidecars, the SDK — is built around that single-binary model. The UI is embedded into the binary with `go:embed` in production builds (via `-tags embed_ui`) so deployment never involves a second artifact for static files. Sidecars (ASR, AI inference, CEF graphics) are separate binaries the main process spawns; they talk over Unix domain sockets and shared memory, and if they crash the main process restarts them. There is no Kubernetes, no ingress controller, no service mesh, no shared filesystem across engines — the substrate for live video is intentionally unlike the substrate for web services.

Two deployment shapes exist.

**Dev / demo shape.** Run `make demo` on a workstation. Four simulated cameras stream over loopback SRT; the Vite dev server proxies browser requests to the Go server; HTTPS is a mkcert-signed cert trusted by the local browser. The whole stack starts in about three seconds and supports hot reload. This is what every contributor uses; it exercises the full pipeline end-to-end.

**Production shape.** A bare-metal Linux host (physical, VM, or cloud-VM with GPU passthrough). A systemd unit runs the binary. Caddy or nginx sits in front for TLS termination if the operator prefers, or the binary terminates TLS itself using a Let's Encrypt cert. Prometheus scrapes `:9090/metrics`. An operator logs in through a browser pointing at the public domain; no other traffic reaches the box. Multi-region is dual-engine: two identical hosts in different regions, both running, with a control-plane secret authorizing leader failover.

The rest of this doc walks the build system, the dev setup, the production deployment shapes, the Docker images, and the runtime knobs (GC, ulimits, profiling).

## How it works

### Build chain

The top-level [`Makefile`](../../Makefile) is the authoritative build system. Targets of interest:

| Target | What it does |
|---|---|
| `make build` | Builds UI (`npm run build`), then `go build -tags embed_ui -o bin/switchframe`. Production binary with embedded UI. |
| `make build-server` | Builds only the Go binary without the UI embed. UI is served separately (via `npm run dev`). |
| `make build-server-mxl` | Adds `cgo mxl` tags; requires `MXL_ROOT` env var pointing to the MXL SDK install dir |
| `make build-asr-sidecar` | Builds `switchframe-asr` with stub backends (no GPU / no cgo) |
| `make build-asr-sidecar-whisper` | Platform-aware: adds `whisper` tag on macOS (Metal), `cuda,tensorrt,whisper` on Linux |
| `make metal-shaders` | Compiles Metal shaders to `switchframe_gpu.metallib` (macOS only; no-op on Linux) |
| `make demo` | Builds + runs `switchframe --demo` with the Vite dev server on `localhost:5173` |
| `make demo-captions` | Same as `make demo` plus the whisper ASR sidecar wired in |
| `make mxl-demo` | Runs [`scripts/mxl-demo.sh`](../../scripts/mxl-demo.sh): MXL GStreamer test sources + Switchframe with MXL integration |
| `make setup-mkcert` | Installs a mkcert CA and generates `~/.switchframe/cert.pem` + `key.pem` |
| `make test` | `go test ./... -race` |
| `make test-all` | Go tests + UI Vitest + Playwright E2E |
| `make docker` | `docker build -t switchframe .` — CPU-only build using `Dockerfile` |
| `make docker-cuda` | `docker build -f Dockerfile.cuda -t switchframe:cuda .` — GPU build with CUDA + TensorRT + ASR |

See [config-and-flags.md](../reference/config-and-flags.md) for the full build-tag catalog.

### Dev quick start

```bash
# One-time setup on macOS or Linux
brew install mkcert pkg-config ffmpeg fdk-aac opus   # or apt equivalents
cd /path/to/switchframe
make setup-mkcert       # trusted HTTPS cert for HTTP/3 dev
cd ui && npm install    # install UI deps

# Run the demo (4 simulated cameras)
make demo
# Opens: http://localhost:5173 (Vite dev server, proxies /api and /moq to the Go server)
# Ctrl+C to stop both
```

`make demo` launches two processes under a shared shell: the Go binary and the Vite dev server. The Vite server proxies API calls to the Go server over HTTP/1.1 on `:8081` (the `--http-fallback` port) because Vite can't speak QUIC to the browser. Browsers, meanwhile, reach the Go server directly over QUIC on `:8080` for WebTransport; the `Alt-Svc` header points them there after the initial HTTPS handshake.

The `--demo` flag auto-enables several useful features: an SRT listener on `:6464`, the playout subsystem, SCTE-35, captions, and the preview proxy encoder. It also disables API authentication so the UI can come up without an operator registration flow.

### Development without demo

For working on a single subsystem without the whole demo stack:

```bash
# Build and run server only, serving UI from Vite
make build-server
./bin/switchframe --http-fallback     # API on :8081, QUIC on :8080

# In another terminal
cd ui && npm run dev                   # Vite dev server on :5173
```

This mode is closer to production — no simulated sources, no auto-enabled subsystems. Add sources via the REST API or the UI.

### Production build

```bash
make build
# Output: bin/switchframe (single binary, UI embedded)
```

The binary is ~200 MB because it includes the SvelteKit build, FFmpeg / FDK-AAC / Opus / SRT shared libraries (when built with cgo), and on macOS, the embedded Metal shader library (`switchframe_gpu.metallib` ~126 KB).

Runtime dependencies on Linux production systems:

- `libavcodec`, `libavformat`, `libswscale`, `libswresample`, `libavutil` (FFmpeg)
- `libx264`, `libfdk-aac`, `libopus`, `libopusfile`
- `libsrt` (v1.5+ for modern SRT Access Control)
- If GPU: CUDA 12.x runtime + NVIDIA driver + (optional) TensorRT
- If whisper ASR: whisper.cpp shared libs (see `Dockerfile.cuda` for exact versions)

The CI pipeline ([`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)) runs `go test ./... -race` plus a `CGO_ENABLED=0 go test -short` pass to guarantee the non-cgo stub builds stay valid.

### HTTPS / HTTP/3 certs

The server always needs a TLS certificate — QUIC requires TLS 1.3. Two paths:

**Self-signed (default).** If no `--tls-cert` / `--tls-key` is provided, the server generates a self-signed cert valid for 14 days on startup ([`certs.Generate`](../../server/cmd/switchframe/app.go)). The cert fingerprint is published at the unauthenticated endpoint `GET /api/cert-hash`. Browsers bootstrap WebTransport by fetching the fingerprint and passing it as a `serverCertificateHashes` parameter — Chrome and Edge accept self-signed certs this way. Firefox and Safari 26.4+ also work; earlier Safari does not support the self-signed flow and needs a trusted cert.

**External cert (mkcert or Let's Encrypt).** Pass `--tls-cert /path/cert.pem --tls-key /path/key.pem`. The server loads the cert at startup, falls back to it when needed, and the browser connects directly without cert-hash bootstrapping. `make setup-mkcert` automates the mkcert path for local development:

```bash
make setup-mkcert
# → installs mkcert root CA, writes ~/.switchframe/cert.pem and key.pem
# Then run:
./bin/switchframe --tls-cert ~/.switchframe/cert.pem --tls-key ~/.switchframe/key.pem
```

For production, use your own PKI or Let's Encrypt. The app does not do ACME itself; run certbot or a reverse proxy (Caddy, nginx) for renewal.

### GPU setup

GPU acceleration is opt-in by build tag. See [gpu.md](../concepts/gpu.md) for the architectural story.

**NVIDIA (Linux):**

```bash
# Build with CUDA tag — requires NVCC and CUDA libs on the build host
cd server
go build -tags "embed_ui cuda" -o ../bin/switchframe ./cmd/switchframe
```

Or use the Docker image which bundles all dependencies:

```bash
make docker-cuda
docker run -d --gpus all \
  -p 8080:8080/udp -p 8080:8080/tcp \
  -p 9090:9090/tcp -p 6464:6464/udp \
  switchframe:cuda --demo --srt-listen :6464
```

The image targets `sm_89` (Ada Lovelace: RTX 4060/4090, L4) by default. Override with `--build-arg GPU_ARCH=sm_86` for Ampere, `sm_80` for A100, etc.

**Apple Silicon (macOS):**

Metal is auto-enabled on `GOOS=darwin` builds via `//go:build cgo && darwin` tags. `make build` triggers `make metal-shaders` which invokes the Metal shader compiler via `xcrun`. The compiled metallib is embedded in the binary via `go:embed`, so the binary can run without the source tree present. Override with `METAL_LIBRARY_PATH` environment variable to point at an external `.metallib` file.

**CPU fallback (everywhere):**

Without `cuda` or on non-Apple platforms, the server logs `"GPU not available, using CPU pipeline"` at startup and runs every operation on the CPU. SIMD kernels (amd64 SSE/AVX, arm64 NEON) make this viable on workstation-class hardware; expect single-source 1080p60 on any modern CPU and multi-source 1080p30 on a workstation.

### System tuning

The binary prints warnings at startup for suboptimal OS limits. Current checks in [`logSystemTuning`](../../server/cmd/switchframe/app.go):

- **File descriptor limit.** Recommend ≥ 65536. Each SRT source, each WebTransport viewer, each output destination uses FDs — running near the default 1024 will cause "too many open files" errors.

Configure with systemd:

```ini
[Service]
LimitNOFILE=65536
```

Or a sysctl pair for network buffer sizes (not checked automatically, but worth setting for UDP-heavy workloads):

```bash
sysctl -w net.core.rmem_max=134217728
sysctl -w net.core.wmem_max=134217728
```

**GC tuning.** [`main.init`](../../server/cmd/switchframe/main.go) sets `debug.SetGCPercent(400)` and `debug.SetMemoryLimit(2 << 30)` (2 GB) when the respective environment variables are unset. `GOGC=400` means GC triggers at 5× live heap rather than the Go default 2× — this reduces GC frequency enough to keep the frame path clean. Lower values cause frame drops; higher values can blow `GOMEMLIMIT`. Override via env var if the workload needs different tuning:

```bash
GOGC=600 GOMEMLIMIT=4GiB ./bin/switchframe
```

**pprof profiling.** Set `SWITCHFRAME_PROFILING=1` in the environment to enable mutex and block profiling. Access profiles via `http://<admin-addr>/debug/pprof/`:

```bash
go tool pprof http://localhost:9090/debug/pprof/profile?seconds=30  # CPU
go tool pprof http://localhost:9090/debug/pprof/heap                # Heap
go tool pprof http://localhost:9090/debug/pprof/goroutine           # Goroutines
go tool pprof http://localhost:9090/debug/pprof/mutex                # Mutex contention (needs profiling=1)
go tool pprof http://localhost:9090/debug/pprof/block                # Blocking ops (needs profiling=1)
```

`/debug/*` requires the admin bearer token when `--admin-token` is set.

### Ports

| Port | Protocol | Direction | Purpose | Flag |
|---|---|---|---|---|
| `8080` | UDP | inbound | QUIC/HTTP3 API + WebTransport media | `--addr` |
| `8080` | TCP | inbound | HTTPS fallback (when cert provided) | same — runs on same address |
| `8081` | TCP | inbound | HTTP/1.1 API fallback | `--http-addr` (needs `--http-fallback`) |
| `9090` | TCP | inbound | Admin: `/metrics`, `/debug/pprof/*`, `/health`, `/ready`, `/api/cert-hash` | `--admin-addr` |
| `6464` | UDP | inbound | SRT input listener | `--srt-listen` |
| `7464`–`7467` | UDP | outbound | SRT output callers | `--srt-output-ports` |
| `8090` | TCP | inbound | NMOS IS-04/IS-05 API (ST 2110) | `--st2110-nmos-port` |

Production firewalls typically allow TCP+UDP `:443` externally (rebind with `--addr :443`) and restrict the admin port (`:9090`) to an internal network or SSH tunnel only. The cert-hash endpoint on `:9090` is unauthenticated — browsers need it — but it only leaks the cert fingerprint, not secrets.

### Observability endpoint

`GET /metrics` on the admin server exposes Prometheus metrics. See [metrics.md](../reference/metrics.md) for the full catalog. Typical scrape config:

```yaml
scrape_configs:
  - job_name: switchframe
    scheme: http
    static_configs:
      - targets: ['switchframe-a.example.com:9090', 'switchframe-b.example.com:9090']
    authorization:
      type: Bearer
      credentials: <admin-token>
```

The `/health` and `/ready` endpoints are always unauthenticated; health probe them for liveness/readiness regardless of admin token.

### Docker

Two Dockerfiles ship in the repo:

**[`Dockerfile`](../../Dockerfile)** — CPU-only build on Debian Bookworm slim. Installs FFmpeg, FDK-AAC (Debian non-free), Opus, x264 from apt. Produces a ~400 MB image suitable for development or CPU-only production.

**[`Dockerfile.cuda`](../../Dockerfile.cuda)** — GPU build on `nvidia/cuda:12.6.3-cudnn-devel-ubuntu24.04`. Compiles FFmpeg, SRT, FDK-AAC, x264 from source to get newer versions and NVENC/CUDA support. Also builds whisper.cpp with CUDA for ASR, ONNX Runtime for Silero VAD, and the Switchframe GPU kernels. Runtime image is `nvidia/cuda:12.6.3-cudnn-runtime-ubuntu24.04` with only the shared libs needed. Produces a ~5 GB image.

Run with GPU passthrough:

```bash
docker run -d --gpus all --restart unless-stopped \
  --name switchframe \
  -p 8080:8080/udp -p 8080:8080/tcp \
  -p 9090:9090/tcp \
  -p 6464:6464/udp \
  -v /var/lib/switchframe:/home/switchframe/.switchframe \
  -e SWITCHFRAME_ADMIN_TOKEN=$(pwgen -s 32 1) \
  switchframe:cuda \
  --admin-addr=0.0.0.0:9090 \
  --admin-token=$SWITCHFRAME_ADMIN_TOKEN \
  --srt-listen :6464
```

### Single sign-on with OpenID Connect

With `--oidc-issuer` set, the UI offers **Sign in** above the API token field. The engine runs the authorization code flow with PKCE under `/auth/` and hands the browser the provider's ID token, which the browser sends as its bearer token to every engine. Each engine verifies that token against the provider's published keys on its own, so both engines of an active-active pair accept a sign-in made at either, and nothing is lost when an engine restarts.

A verified user becomes an operator named after `preferred_username` (or `name`), with the role mapped from `--oidc-role-claim` through `--oidc-role-map`. The operator ID is derived from issuer and subject, so both engines hold locks under the same ID. A user with no mapped value is refused with a message, unless `--oidc-default-role` names a role. The session API token, operator tokens and peer polling keep working beside sign-in.

```bash
docker run -d --name switchframe-a \
  -p 8080:8080/udp -p 8080:8080/tcp -p 8081:8081/tcp \
  -v /var/lib/switchframe-a:/home/switchframe/.switchframe \
  -v /etc/switchframe/oidc-secret:/run/secrets/oidc-secret:ro \
  -e SWITCHFRAME_OIDC_ISSUER=https://id.example.com/realms/media \
  -e SWITCHFRAME_OIDC_CLIENT_ID=switchframe \
  -e SWITCHFRAME_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc-secret \
  -e SWITCHFRAME_OIDC_ROLE_CLAIM=roles \
  -e SWITCHFRAME_OIDC_ROLE_MAP=switchframe-director=director,switchframe-audio=audio,switchframe-graphics=graphics,switchframe-captioner=captioner,switchframe-viewer=viewer \
  switchframe:latest --http-fallback
```

A second engine takes the same `SWITCHFRAME_OIDC_*` values plus its `--engine-label` and peer flags.

**Provider setup** (Keycloak shown; any OpenID Connect provider works the same way):

1. Create a client, e.g. `switchframe`, with the standard (authorization code) flow. Make it confidential and pass its secret through `--oidc-client-secret-file`, or public with PKCE (S256) and no secret.
2. Valid redirect URIs: `https://<engine host>/auth/callback` for every engine the browser reaches.
3. Valid post-logout redirect URIs: `https://<engine host>/` for every engine. Without them the provider refuses sign-out.
4. Roles `switchframe-director`, `switchframe-audio`, `switchframe-graphics`, `switchframe-captioner`, `switchframe-viewer`, assigned to users or groups.
5. A protocol mapper putting the user's roles in the ID token under the claim named by `--oidc-role-claim` (in Keycloak: "User Realm Role", token claim name `roles`, multivalued, added to the ID token).

The ID token expires on the provider's schedule; the UI refreshes it a minute ahead through `POST /auth/refresh`, at the page's engine or, when that one is down, at its peer.

What to know before running it in production:

- **Revocation is not immediate.** Engines verify the ID token without asking the provider, and remember a verified token for up to 30 seconds. Signing out at the provider, disabling a user or changing their roles takes effect when the current ID token expires, so keep the provider's ID token lifetime short (Keycloak's default is minutes).
- **One role per user.** A user whose claim maps to several roles gets the most privileged of them, in the order director, audio, graphics, captioner, viewer.
- **`--oidc-default-role` admits everyone** the provider authenticates for this client, e.g. every user of a Keycloak realm.
- **One client per environment.** Every engine configured with the same issuer and client ID accepts the same tokens.
- **Set `--oidc-redirect-url` behind a proxy.** Without it the engine derives its own address from the request, trusting `X-Forwarded-Proto`.
- **The refresh token is kept in the browser's local storage**, like the API token, so that the peer engine can refresh after a failover. Any script running on the page can read it.
- **Sign-in does not cover the media stream.** The WebTransport session carrying preview video and control tracks takes no bearer token, with or without OpenID Connect.
- Deleting a signed-in operator through the API lasts only until that user's next request, which registers them again.

### Typical production shape

Based on [VISION.md](../VISION.md)'s deployment principles — "systemd / Nomad on bare metal + cloud burst" — the production shape is:

- **Bare-metal or cloud-VM Linux host** with an NVIDIA GPU (L4, RTX 4090, or A10G depending on regional availability).
- **Single `switchframe` process** under systemd. No Kubernetes.
- **Reverse proxy** (Caddy or nginx) for TLS termination + Let's Encrypt, or run the server with its own cert.
- **Prometheus + Grafana** scraping `:9090/metrics`.
- **Persistent state volume** mounted at `$SWITCHFRAME_STATE_DIR` (default `~/.switchframe/`).
- **Two regions for multi-engine active-active** — see the engine-label and peer-engine flags in [config-and-flags.md](../reference/config-and-flags.md). A lightweight control plane issues PTP-timed commands to both engines.
- **No shared filesystem.** Each engine has its own state dir; multi-engine sync is via the command queue and state snapshot replication, not NFS.

A minimal systemd unit:

```ini
[Unit]
Description=Switchframe live video switcher
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=switchframe
Group=switchframe
ExecStart=/usr/local/bin/switchframe \
    --addr :443 \
    --tls-cert /etc/switchframe/fullchain.pem \
    --tls-key /etc/switchframe/privkey.pem \
    --admin-addr 127.0.0.1:9090 \
    --admin-token-file /etc/switchframe/admin-token \
    --http-fallback --http-addr :8081 \
    --srt-listen :6464 \
    --output-bitrate 6000000 --output-gop-secs 2 \
    --preview-proxy --relay-bitrate 2000000
Environment=SWITCHFRAME_STATE_DIR=/var/lib/switchframe
Environment=GOGC=400
Environment=GOMEMLIMIT=4GiB
LimitNOFILE=65536
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### Multi-engine active-active

Two engines run in different regions; the `--engine-label a` and `--engine-label b` flags identify them. Each engine has `--peer-engine-url` pointing at the other so they can health-poll each other. A control plane — lightweight HTTP service, not in this repo — issues commands authorized with `--control-plane-secret`. Commands carry a PTP timestamp for deterministic execution order; the [`cmdqueue`](../../server/cmdqueue/queue.go) subsystem drains them at the scheduled time. Split-brain is prevented by an explicit engine-label guard on destination starts and by the control-plane secret gate on `POST /api/peer/force-leader`.

An engine goes on air at start without an operator cut. When its peer is on air, it reads the peer's state snapshot (`GET /api/sync/state-snapshot`, with its own API token, so both engines need the same `--api-token`; the token travels to `--peer-engine-url`, which is TLS unless the network between the engines is trusted) and applies it, then checks on the next peer poll that both carry the same program, following again if the peer cut meanwhile; this holds for a restarted engine whatever its label. After five applies that do not put the peer's program on air here, the engine goes live on its own source. With no peer on air, the leader by the role tiebreak goes live on `--initial-program-source`, or the first input delivering frames, and the other engine waits for it and follows; it goes live on its own after 20s with the leader still dark, and follows the leader once that is on air. An engine whose peer is unreachable goes live alone, and follows a leader that turns out to have gone live alongside it. Both engines write MXL program grains at the wall-clock grain index, so an engine that follows writes the same indices as its peer.

See [metrics.md](../reference/metrics.md) for the `switchframe_cmdqueue_*` and `switchframe_snapshot_*` metric families that expose active-active health.

## Key types and entry points

| Symbol | File | Role |
|---|---|---|
| [`main`](../../server/cmd/switchframe/main.go) | `cmd/switchframe` | Entry point: `run()` orchestrates `initInfra` → `initPrismServer` → `initCoreEngine` → ... → `app.Run` |
| [`App.Run`](../../server/cmd/switchframe/app.go) | `cmd/switchframe` | Sets `readyFlag`, calls `server.Start(ctx)` to begin the QUIC listener |
| [`StartAdminServer`](../../server/cmd/switchframe/admin.go) | `cmd/switchframe` | Admin HTTP server (metrics, health, ready, pprof, cert-hash) |
| [`startHTTPAPIServer`](../../server/cmd/switchframe/app_http.go) | `cmd/switchframe` | Optional HTTP/1.1 + HTTPS fallback server |
| [`logSystemTuning`](../../server/cmd/switchframe/app.go) | `cmd/switchframe` | Startup check for FD limits |
| [`readyFlag`](../../server/cmd/switchframe/admin.go) | `cmd/switchframe` | `/ready` returns 503 until all subsystems initialized |
| [`certs.Generate`](../../server/cmd/switchframe/app.go) | `cmd/switchframe` | Self-signed TLS cert (14-day validity) |
| [`uiHandler`](../../server/cmd/switchframe/embed_prod.go) | `cmd/switchframe` | `go:embed all:ui` when built with `-tags embed_ui` |

## Gotchas and invariants

- **Demo mode turns off auth.** `--demo` disables API authentication via [`NoopAuthMiddleware`](../../server/control/auth.go). Never expose a `--demo` instance to the public internet.
- **A generated API token is written to the state dir, never logged.** At startup when no token is provided, a fresh 64-char hex token is written to `api-token` in the state dir with mode `0600`, and startup fails if it cannot be written. Neither the log nor stdout carries the token; startup logs only where it came from (`token_source`).
- **HTTPS and HTTP/3 share the same port when cert is provided.** With `--tls-cert`, the server listens TCP on `--addr` (for HTTPS fallback) and UDP on `--addr` (for QUIC). Alt-Svc header guides browsers from TCP to QUIC.
- **Self-signed certs expire after 14 days.** Restart the server or supply an external cert to extend. Production should always use external certs.
- **State dir must be writable and private.** `~/.switchframe/` is created with `0o700` permissions and holds operator tokens in plain text. Do not make it world-readable.
- **GPU build needs NVCC at build time.** `make docker-cuda` builds CUDA kernels during image build; the resulting binary no longer needs NVCC, only the CUDA runtime libraries.
- **Metal shaders compile on macOS at build time.** `make build` invokes `make metal-shaders` which calls `xcrun` — only works on macOS with Xcode command-line tools installed.
- **Embedded UI symlink is a Makefile artifact.** `make ui-build` writes `server/cmd/switchframe/ui → ../../../ui/build`. When building with `-tags embed_ui` manually, ensure this symlink exists or the `//go:embed all:ui` directive will fail.
- **Cluster orchestration is out of scope.** The server expects to be supervised by systemd / Nomad, not Kubernetes. `live video on K8s is a parade of paper cuts` — see [VISION.md](../VISION.md).
- **The demo mode SRT listener is shared.** `--demo` auto-enables `--srt-listen :6464`; SRT test pushers from `test/clips/` can connect immediately. To run with only simulated cameras, override with `--srt-listen ""`.
- **`--warm-cache` is an AMI build hook.** It pre-generates caches for all resolutions and exits. Do not use it for production runs.
- **The admin server also mounts Go pprof.** `/debug/*` is under the same admin token guard as `/metrics`. Never run production without `--admin-token` — an open pprof endpoint exposes memory profiles to anyone who can reach port 9090.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md), [gpu.md](../concepts/gpu.md), [locking-and-concurrency.md](../concepts/locking-and-concurrency.md), [media-path.md](../concepts/media-path.md)
- **Reference:** [config-and-flags.md](../reference/config-and-flags.md), [metrics.md](../reference/metrics.md), [file-formats.md](../reference/file-formats.md), [api.md](../reference/api.md)
- **Subsystems:** [switcher.md](../subsystems/switcher.md), [output.md](../subsystems/output.md), [srt-ingest.md](../subsystems/srt-ingest.md), [control-plane.md](../subsystems/control-plane.md), [codec.md](../subsystems/codec.md), [playout.md](../subsystems/playout.md), [mxl.md](../subsystems/mxl.md)
- **Integration:** [asr-sidecar.md](../integration/asr-sidecar.md), [cef-protocol.md](../integration/cef-protocol.md), [ui-server-contract.md](../integration/ui-server-contract.md)
- **Root:** [architecture.md](../architecture.md), [README.md](../README.md), [VISION.md](../VISION.md), [ROADMAP.md](../ROADMAP.md)
