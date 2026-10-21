# Configuration and Flags

> **Where you are:** [docs](../README.md) → reference → config-and-flags
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [file-formats.md](./file-formats.md) · [metrics.md](./metrics.md) · [../operations/deployment.md](../operations/deployment.md)

**TL;DR** Switchframe is configured through three layers: command-line flags, environment variables, and on-disk state files. Flag parsing lives in [`parseConfig`](../../server/cmd/switchframe/main.go) in a single file; environment variables are mostly flag overrides (`SWITCHFRAME_*`) plus a few low-level runtime knobs (`GOGC`, `GOMEMLIMIT`, `METAL_LIBRARY_PATH`, `SWITCHFRAME_PROFILING`). State directories default to `~/.switchframe/` and hold JSON stores for presets, macros, operators, SRT sources, clips, playout caches, and more — see [file-formats.md](./file-formats.md) for schemas. Build tags change what compiles: `cgo`, `cuda`, `darwin`, `mxl`, `openh264`, `embed_ui`, `whisper`, `tensorrt`.

## The story

Switchframe's configuration surface is layered from most volatile to most durable. Command-line flags are the right place for everything that changes between runs — which port to listen on, which cameras to expose as sources in demo mode, which GPU model file to load. Environment variables are the right place for secrets and for flag values that operations tooling wants to inject without parsing CLI (API tokens, invite codes, engine labels). State files are the right place for things the operator creates at runtime and expects to persist across restarts (presets they saved, SRT sources they configured, clips they uploaded).

When a flag and an env var both control the same setting, flags take precedence when non-empty — see [`resolveEnvOrFlag`](../../server/cmd/switchframe/main.go). A small set of auto-generation fallbacks exist: if `--api-token` is not provided and `SWITCHFRAME_API_TOKEN` is not set, a fresh token is generated and printed on stdout at startup.

Build tags gate whole subsystems. Without the `cgo` tag, the binary compiles pure-Go stubs for FFmpeg, FDK-AAC, Opus, CUDA, Metal, and MXL — useful for cross-compilation and CI smoke tests, useless for production. The `cuda` tag pulls in NVENC/NVDEC; `darwin` tag pulls in Metal; `mxl` tag pulls in the MXL SDK; `embed_ui` tag embeds the SvelteKit UI into the binary via `go:embed`. Tests under `//go:build correctness` are the correctness test suite, separate from the normal `go test ./...`.

The table format below lists every flag, env var, and build tag the code actually reads. Grep the source if you need the definition line; all flag definitions are in [`parseConfig`](../../server/cmd/switchframe/main.go) unless noted otherwise.

## Command-line flags

All flags are defined in [`parseConfig`](../../server/cmd/switchframe/main.go). Defaults reflect the source as of this doc.

### Core server

| Flag | Type | Default | Description |
|---|---|---|---|
| `--addr` | string | `:8080` | QUIC/HTTP3 listen address |
| `--http-fallback` | bool | `false` | Start a plain HTTP/1.1 API server for curl/scripts |
| `--http-addr` | string | `:8081` | HTTP/1.1 fallback listen address (requires `--http-fallback`) |
| `--tls-cert` | string | `""` | Path to TLS certificate PEM (e.g., from mkcert). Empty → self-signed |
| `--tls-key` | string | `""` | Path to TLS private key PEM |
| `--https-self-signed` | bool | `false` | Without `--tls-cert`, also serve the UI and API over HTTPS on the TCP side of `--addr`, with the generated certificate. Loaded from there, the UI is a secure context once the certificate is accepted, which WebTransport needs away from localhost |
| `--admin-addr` | string | `127.0.0.1:9090` | Admin/metrics server listen address |
| `--admin-token` | string | `""` (env `SWITCHFRAME_ADMIN_TOKEN`) | Bearer token guarding `/metrics` and `/debug/*`. Empty → unprotected |
| `--allowed-origins` | string | `""` | Comma-separated CORS origins. Empty → `*` |
| `--api-token` | string | `""` (env `SWITCHFRAME_API_TOKEN`) | Bearer token for API auth. Empty → auto-generate |
| `--log-level` | string | `info` | One of `debug`, `info`, `warn`, `error` |

### Demo and development

| Flag | Type | Default | Description |
|---|---|---|---|
| `--demo` | bool | `false` | Start with simulated camera sources; also disables API auth |
| `--demo-video` | string | `""` | Directory of MPEG-TS clips to replay as demo sources (requires `--demo`) |

### Pipeline format

| Flag | Type | Default | Description |
|---|---|---|---|
| `--format` | string | `1080p29.97` | Video standard preset; see [`FormatPresets`](../../server/switcher/format.go) |
| `--color-format` | string | `standard` | Pipeline color profile: `standard`, `standard-hevc`, `professional` |
| `--frame-sync` | bool | `false` | Enable freerun frame synchronizer |
| `--low-latency-sync` | bool | `false` | Source-driven frame sync (lower latency, less smooth output) |
| `--frc-quality` | string | `none` | Frame rate conversion: `none`, `nearest`, `blend`, `mcfi` |
| `--ref-clock` | bool | `false` | Use reference clock for synchronous A/V processing (experimental) |

### Relay and preview encoding

| Flag | Type | Default | Description |
|---|---|---|---|
| `--relay-bitrate` | int (bps) | `2_000_000` | Relay encode bitrate for SRT source → WebTransport distribution |
| `--relay-resolution` | string | `720p` | Relay encode resolution (`720p`, `480p`, `360p`, `source`) |
| `--preview-proxy` | bool | `false` | Enable low-bitrate preview encoding for browser source previews |
| `--preview-resolution` | string | `480p` | Preview encode resolution |
| `--preview-bitrate` | int (bps) | `500_000` | Preview bitrate |
| `--preview-frame-interval` | int | `1` | Encode every Nth frame (1=all, 2=half rate) |

### Program output encoder

| Flag | Type | Default | Description |
|---|---|---|---|
| `--output-bitrate` | int (bps) | `0` | Program output bitrate; 0 = auto from resolution |
| `--output-gop-secs` | int | `0` | GOP / keyframe interval in seconds (1, 2, or 4; 0 = 2) |
| `--output-preset` | string | `""` | Encoder preset: `low-latency`, `balanced`, `quality` |
| `--output-timecode` | bool | `false` | Enable SMPTE 12M timecode insertion |

### SCTE-35 signaling

| Flag | Type | Default | Description |
|---|---|---|---|
| `--scte35` | bool | `false` | Enable SCTE-35 insertion |
| `--scte35-pid` | int | `0x102` | SCTE-35 PID in MPEG-TS output (valid range `0x20`–`0x1FFE`) |
| `--scte35-preroll` | int (ms) | `4000` | Default pre-roll for scheduled cues |
| `--scte35-heartbeat` | int (ms) | `5000` | Interval between `splice_null` heartbeats (0 = disabled) |
| `--scte35-verify` | bool | `true` | Round-trip decode verify after encode |
| `--scte35-webhook` | string | `""` | Webhook URL for SCTE-35 event notifications |
| `--scte104` | bool | `false` | Enable SCTE-104 on MXL data flows (requires `--scte35`) |

### Captions

| Flag | Type | Default | Description |
|---|---|---|---|
| `--captions` | bool | `false` | Enable CEA-608/708 closed captioning |

### Clip storage

| Flag | Type | Default | Description |
|---|---|---|---|
| `--clip-storage-max` | int64 (bytes) | `10<<30` (10 GB) | Maximum clip storage |
| `--clip-ephemeral-ttl` | duration | `24h` | TTL for ephemeral clips |

### Playout

| Flag | Type | Default | Description |
|---|---|---|---|
| `--playout` | bool | `false` (true in demo) | Enable playout subsystem |
| `--playout-cache-max` | int (GB) | `200` | Playout asset cache max size |
| `--playout-asrun-retention` | int (days) | `30` | As-run log retention |

### Replay

| Flag | Type | Default | Description |
|---|---|---|---|
| `--replay-buffer-secs` | int | `300` | Per-source replay buffer duration (0 disables, max 300) |
| `--replay-tmpfs-dir` | string | `/dev/shm/switchframe/replay` | Directory for tmpfs-backed replay buffers (`none` disables) |

### SRT input

| Flag | Type | Default | Description |
|---|---|---|---|
| `--srt-listen` | string | `""` | SRT listener address for incoming pushes (e.g., `:6464`) |
| `--srt-latency` | int (ms) | `120` | Default SRT latency |

### Outputs at startup

| Flag | Type | Default | Description |
|---|---|---|---|
| `--outputs-config` | string | `""` | JSON file of SRT/RTMP destinations and a recording to start with the process, in the shapes `POST /api/output/destinations` and `POST /api/recording/start` take; `passphraseFile` and `urlFile` read those fields from files. See [`StartupConfig`](../../server/output/startup.go) |

### SRT output

| Flag | Type | Default | Description |
|---|---|---|---|
| `--srt-output-ports` | string | `""` | Allowed port range for SRT output, e.g., `7464-7467` |
| `--domain` | string | `""` | Public domain for connection URLs (e.g., `switchframe.dev`) |

### MXL integration

| Flag | Type | Default | Description |
|---|---|---|---|
| `--mxl-sources` | string | `""` (env `SWITCHFRAME_MXL_SOURCES`) | Comma-separated MXL source specs `videoUUID[:audioUUID[:dataUUID]]` |
| `--mxl-output` | string | `""` | MXL flow name for program output |
| `--mxl-output-video-def` | string | `""` | Path to MXL output video flow definition JSON |
| `--mxl-output-audio-def` | string | `""` | Path to MXL output audio flow definition JSON |
| `--mxl-domain` | string | `/dev/shm/mxl` | MXL shared memory domain path |
| `--mxl-discover` | bool | `false` | List available MXL flows and exit |

### AI inference

| Flag | Type | Default | Description |
|---|---|---|---|
| `--ai-sidecar` | string | `""` | Path to `switchframe-ai` sidecar binary (enables AI segmentation / depth / SAM / tracking) |
| `--ai-models-dir` | string | `""` | Directory with AI model files (ONNX + `.plan`) |
| `--neural-key-model` | string | `""` | Path to CorridorKey ONNX model (requires CUDA + TensorRT) |

### ASR (speech-to-text)

| Flag | Type | Default | Description |
|---|---|---|---|
| `--asr-sidecar` | string | `""` | Path to `switchframe-asr` sidecar binary |
| `--asr-sidecar-model-dir` | string | `""` | Path to streaming ASR model directory |
| `--asr-whisper-models-dir` | string | `""` | Whisper GGML models directory |
| `--asr-default-backend` | string | `parakeet` | Default ASR backend: `parakeet` or `whisper` |

### Cache

| Flag | Type | Default | Description |
|---|---|---|---|
| `--cache-dir` | string | Linux: `/data/switchframe/cache`; macOS: `~/.switchframe/cache` | Persistent mmap-cached LUT / decoded asset cache |
| `--warm-cache` | bool | `false` | Pre-generate caches for all resolutions and exit (used in AMI build) |

### ST 2110

| Flag | Type | Default | Description |
|---|---|---|---|
| `--st2110-port` | string | `""` | DPDK port identifier (e.g., `0000:31:00.0`) |
| `--st2110-ip` | string | `""` | Local IP for ST 2110 TX |
| `--st2110-inputs` | int | `4` | Number of ST 2110 input receivers |
| `--st2110-ptp` | string | `auto` | PTP mode: `auto`, `l2`, `l4`, `none` |
| `--st2110-nmos-port` | int | `8090` | NMOS API HTTP port (IS-04/IS-05) |

### Multi-engine (active-active)

| Flag | Type | Default | Description |
|---|---|---|---|
| `--peer-engine-url` | string | `""` (env `SWITCHFRAME_PEER_ENGINE_URL`) | URL of peer engine for inter-engine health polling |
| `--engine-label` | string | `""` (env `SWITCHFRAME_ENGINE_LABEL`) | `"a"`, `"b"`, or `""` (single-engine). Other values rejected |
| `--control-plane-secret` | string | `""` (env `SWITCHFRAME_CONTROL_PLANE_SECRET`) | Shared secret authorizing `POST /api/peer/force-leader`. Empty disables endpoint |

## Environment variables

Most env vars are flag overrides or fallbacks. A few are low-level runtime knobs.

### Secrets and tokens

| Variable | Fallback for | Effect |
|---|---|---|
| `SWITCHFRAME_API_TOKEN` | `--api-token` | Used when the flag is empty; otherwise flag wins |
| `SWITCHFRAME_ADMIN_TOKEN` | `--admin-token` | Used when the flag is empty |
| `SWITCHFRAME_INVITE_TOKENS` | — | Comma-separated `role:token` pairs. Parsed into a token→role map for operator registration |
| `SWITCHFRAME_CONTROL_PLANE_SECRET` | `--control-plane-secret` | Used when the flag is empty |

### Overrides

| Variable | Fallback for | Effect |
|---|---|---|
| `SWITCHFRAME_STATE_DIR` | — | Overrides default `~/.switchframe` for on-disk state |
| `SWITCHFRAME_MXL_SOURCES` | `--mxl-sources` | Comma-separated MXL source specs |
| `SWITCHFRAME_PEER_ENGINE_URL` | `--peer-engine-url` | Peer engine URL for active-active |
| `SWITCHFRAME_ENGINE_LABEL` | `--engine-label` | Engine label for active-active pairs |

### Runtime knobs

| Variable | Where used | Effect |
|---|---|---|
| `GOGC` | [`main.init`](../../server/cmd/switchframe/main.go) | Set by `debug.SetGCPercent(400)` if unset. GC triggers at 5× live heap (vs Go default 2×) — matters for real-time processing |
| `GOMEMLIMIT` | [`main.init`](../../server/cmd/switchframe/main.go) | Set by `debug.SetMemoryLimit(2 << 30)` if unset. 2 GB soft memory limit |
| `APP_ENV` | [`app.initInfra`](../../server/cmd/switchframe/app.go) | When `production`, logs use JSON handler; else text handler |
| `SWITCHFRAME_PROFILING` | [`admin.init`](../../server/cmd/switchframe/admin.go) | When `1`, enables `runtime.SetMutexProfileFraction(5)` and `runtime.SetBlockProfileRate(1000)` for pprof analysis |
| `METAL_LIBRARY_PATH` | [`gpu/metal_context.go`](../../server/gpu/metal_context.go) | Path to `switchframe_gpu.metallib`. Falls back to next-to-binary, cwd-relative paths, then embedded copy |
| `HOME` | [`warm_cache_gpu.go`](../../server/cmd/switchframe/warm_cache_gpu.go) | Model cache location under `$HOME/.switchframe/models` |
| `CGO_LDFLAGS` | Makefile | Linker flags (macOS adds `-Wl,-no_warn_duplicate_libraries`) |
| `MXL_ROOT` | Makefile | MXL SDK install dir (required by `make build-server-mxl`) |
| `PKG_CONFIG_PATH` | Makefile | pkg-config search path (MXL build prepends `$MXL_ROOT/lib/pkgconfig`) |
| `WHISPER_MODELS_DIR` | Makefile | Override path to whisper.cpp GGML models for `make demo-captions` (default `/opt/whisper.cpp/models`) |
| `COLOR_FORMAT` | Makefile | Passed as `--color-format` to `make demo` |
| `PRISM_TS_SRC` | Makefile | Path to the Prism web source for TypeScript vendor sync |

## Build tags

Build tags gate whole subsystems. Combine them with `go build -tags "tag1 tag2"`.

| Tag | Effect | Required platform/deps |
|---|---|---|
| `cgo` | Enable cgo for all FFmpeg, FDK-AAC, Opus, MXL, CUDA, Metal code. Without it, the binary uses pure-Go stubs | Default on `go build`; set `CGO_ENABLED=0` to disable |
| `cuda` | Compile NVENC/NVDEC, CUDA kernels, CUDA `gpu.Context` | Linux + NVIDIA GPU + CUDA toolkit |
| `darwin` | Compile Metal kernels, Metal `gpu.Context`. Auto-applied on `GOOS=darwin` | macOS |
| `mxl` | Compile MXL SDK bindings | Requires MXL SDK (`MXL_ROOT`) and `pkg-config` |
| `openh264` | Compile OpenH264 fallback encoder/decoder | OpenH264 library |
| `embed_ui` | Embed SvelteKit UI build under `server/cmd/switchframe/ui` via `go:embed` | Run `make ui-build` first (symlinks `ui/build` into place) |
| `whisper` | Compile whisper.cpp ASR backend | whisper.cpp build at `/opt/whisper.cpp` (or override) |
| `tensorrt` | Compile TensorRT AI inference backends | Linux + NVIDIA + TensorRT |
| `optix` | Compile OptiX ray-traced effects | CUDA + OptiX SDK |
| `noffmpeg` | Disable FFmpeg even with `cgo` (uses stub) | — |
| `correctness` | Compile the correctness test suite | Test-only |
| `integration` | Compile integration tests | Test-only |

Typical build commands:

- `make build` — `go build -tags embed_ui` (production, UI embedded)
- `make build-server` — `go build` (dev, UI served separately by Vite)
- `make build-server-mxl` — adds `cgo mxl`
- `make build-asr-sidecar-whisper` — adds `whisper` (Mac) or `cuda,tensorrt,whisper` (Linux)
- `make docker-cuda` — builds the CUDA-enabled Docker image

## On-disk state layout

All state defaults to `~/.switchframe/` (override with `SWITCHFRAME_STATE_DIR`). Contents:

| Path | Format | Owner | Purpose |
|---|---|---|---|
| `presets.json` | JSON array of `preset.Preset` | [`preset.Store`](../../server/preset/store.go) | Saved production snapshots |
| `macros.json` | JSON array of `macro.Macro` | [`macro.Store`](../../server/macro/store.go) | Named operation sequences |
| `operators.json` | JSON array of `operator.Operator` | [`operator.Store`](../../server/operator/store.go) | Registered operators (name, role, token) |
| `srt_sources.json` | JSON `{sources: map[key]SourceConfig}` | [`srt.Store`](../../server/srt/store.go) | Persisted SRT source configs |
| `stingers/<name>/` | PNG sequence + optional WAV | [`stinger.Store`](../../server/stinger/store.go) | Stinger transition clips |
| `wipe_patterns/` | — | [`wipemap.WipeMapStore`](../../server/transition/wipemap/store.go) | Gradient maps for wipe transitions |
| `dve_presets/<id>.json` | JSON `dve.Preset` | [`dve.PresetStore`](../../server/dve/preset.go) | Saved DVE layouts |
| `stmaps/` | `<name>.stmap` (binary) + `<name>.stmap-anim.json` | [`stmap.Store`](../../server/stmap/store.go) | ST map LUTs and animated metadata |
| `clips/` | `clips.json` + media files | [`clip.Store`](../../server/clip/store.go) | Uploaded media clips |
| `luts/` | .cube / .json | [`colorgrade.Store`](../../server/colorgrade/store.go) | Color grading looks |
| `playout/cache_meta.json` | JSON array of `playout.CacheEntry` | [`playout.Cache`](../../server/playout/cache.go) | LRU media cache metadata |
| `playout/playout-state.json` | JSON `playout.PersistedState` | [`playout.StatePersister`](../../server/playout/persist.go) | Crash-recoverable channel state |
| `playout/asrun/asrun-YYYY-MM-DD.jsonl` | JSONL `playout.AsRunEvent` | [`playout.AsRunLogger`](../../server/playout/asrun.go) | As-run event log |
| `scte35-rules.json` | JSON `{rules: [], defaultAction: ...}` | [`scte35.RulesStore`](../../server/scte35/rules_store.go) | SCTE-35 signal conditioning rules |

Persistent cache is separate (`--cache-dir`):

| Path | Contents |
|---|---|
| `<cache-dir>/stingers/<name>.bin` | mmap-backed decoded stinger frames |
| `<cache-dir>/stmap/<name>_<hash>/` | mmap-backed ST map LUTs |
| `<cache-dir>/models/` | AI model working files |
| `<cache-dir>/<playout-cache>/assets/` | Asset-hashed media files (playout `--playout-cache-max`) |

See [file-formats.md](./file-formats.md) for field-level schemas.

## Ports

| Port | Protocol | Flag | Purpose |
|---|---|---|---|
| `8080` | UDP/QUIC | `--addr` | Primary API + WebTransport (HTTP/3) |
| `8081` | TCP HTTP/1.1 | `--http-addr` | Optional HTTP fallback API (for curl/browsers without HTTP/3) |
| `9090` | TCP HTTP/1.1 | `--admin-addr` | Admin server: `/metrics`, `/debug/pprof/*`, `/health`, `/ready`, `/api/cert-hash` |
| `6464` | UDP | `--srt-listen` | SRT input listener (optional) |
| `7464`–`7467` | UDP | `--srt-output-ports` | SRT output port range (optional) |
| `8090` | TCP HTTP/1.1 | `--st2110-nmos-port` | NMOS IS-04/IS-05 API (when ST 2110 enabled) |

## Gotchas and invariants

- **Demo mode disables API auth.** `--demo` causes `initInfra` to install a no-op auth middleware. Never run `--demo` on a public-reachable address.
- **Auto-generated API tokens print to stdout, not stderr.** The token is printed once at startup to stdout so operators can capture it without log routing capturing it. Pipe stderr but preserve stdout.
- **`--engine-label` only accepts `""`, `"a"`, or `"b"`.** Any other value is rejected at startup. This prevents silent split-brain from typos.
- **Flag precedence is flag → env → default.** An empty flag value uses the env var; a set flag wins even if the env var is also set. `resolveEnvOrFlag` encodes this.
- **Auto-enabled in demo mode:** `--srt-listen` (`:6464`), `--playout`, `--scte35`. These are set if unset when `--demo` is true.
- **Build tag combinations are additive.** You cannot build `cuda,darwin` simultaneously (the files are mutually exclusive). Cross-compilation paths: on macOS, build Linux binaries via a container (see the `Dockerfile` and `Dockerfile.cuda`).
- **`GOGC=400` is the default.** Lower than 400 (more frequent GC) will cause frame-drop bursts; higher than 400 (less frequent GC) can exceed `GOMEMLIMIT`. Change only with a benchmark.
- **MXL discovery mode is a clean-exit flag.** `--mxl-discover` lists available MXL flows on stdout and exits zero. It is not a server mode.
- **`--warm-cache` is an AMI build hook.** It pre-generates ST map LUTs and exits. Do not use it in production runs.
- **The cert-hash endpoint is always unauthenticated.** Browsers use it to bootstrap WebTransport with self-signed certs; authentication would block the UI from loading.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md), [gpu.md](../concepts/gpu.md), [locking-and-concurrency.md](../concepts/locking-and-concurrency.md)
- **Reference:** [metrics.md](./metrics.md), [file-formats.md](./file-formats.md), [api.md](./api.md), [state-broadcast.md](./state-broadcast.md), [fast-control.md](./fast-control.md)
- **Subsystems:** [switcher.md](../subsystems/switcher.md), [srt-ingest.md](../subsystems/srt-ingest.md), [output.md](../subsystems/output.md), [mxl.md](../subsystems/mxl.md), [playout.md](../subsystems/playout.md), [codec.md](../subsystems/codec.md), [control-plane.md](../subsystems/control-plane.md)
- **Integration:** [asr-sidecar.md](../integration/asr-sidecar.md), [cef-protocol.md](../integration/cef-protocol.md)
- **Operations:** [deployment.md](../operations/deployment.md)
- **Root:** [architecture.md](../architecture.md), [README.md](../README.md)
