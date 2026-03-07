# SwitchFrame

A browser-based live video switcher for multi-camera production. Cut, dissolve, and mix between sources in real time — all from a web browser. Built on [Prism](https://github.com/zsiec/prism) (MoQ/WebTransport media server).

![SwitchFrame control room](docs/switchframe-ui.png)

## Features

**Video Switching**
- Hard cut with keyframe gating (no decoder artifacts)
- Mix, dip-to-black, and wipe transitions (100–5000ms)
- 6 wipe directions: horizontal, vertical, box center-out, box edges-in
- Stinger transitions with per-pixel alpha (PNG sequence upload)
- Manual T-bar control at 20Hz
- Fade to black with smooth reverse
- Per-source configurable delay buffer (0–500ms)
- Freerun frame synchronizer for multi-source alignment (90 kHz PTS)
- GOP cache for instant keyframe on cut
- Automatic resolution mismatch handling (bilinear scaler)
- Upstream chroma and luma keying per source (YUV420 domain)

**Audio**
- Server-side FDK AAC decode/mix/encode
- Per-channel faders, mute, and audio-follows-video (AFV)
- Per-channel input trim (−20 to +20 dB)
- 3-band parametric EQ per channel (RBJ biquad, 80–16,000 Hz)
- Single-band compressor per channel (threshold, ratio, attack, release, makeup gain)
- Equal-power crossfade on cuts and transitions
- Brickwall limiter at −1 dBFS on master bus
- Peak metering per channel and program output
- Zero-CPU passthrough when single source at unity gain with no processing
- Client-side PFL (pre-fade listen) with per-operator solo
- Signal chain: Trim → EQ → Compressor → Fader → Mix → Master → Limiter → Encode

**Output**
- MPEG-TS recording with time and size-based rotation
- SRT push (caller) and pull (listener, up to 8 connections)
- Reconnect with 4MB ring buffer and keyframe resume on overflow
- 1 fps confidence monitor (JPEG thumbnail of program output)
- Zero overhead when no outputs are active

**Instant Replay**
- Per-source GOP-aligned circular buffers (configurable 1–300 seconds)
- Mark-in / mark-out with wall-clock precision
- Variable-speed playback (0.25x–1x) with frame duplication
- Loop mode for continuous replay
- Replay output routed to dedicated relay

**Graphics**
- Downstream keyer (DSK) with RGBA alpha compositing
- Upstream chroma key (Cb/Cr distance, spill suppression, smoothness feathering)
- Upstream luma key (clip range, softness)
- Built-in templates: lower third, full-screen card, ticker
- Instant cut or 500ms fade on/off

**Multi-Operator**
- Role-based access control: Director, Audio, Graphics, Viewer
- Subsystem locking (switching, audio, graphics, replay, output)
- Per-operator bearer tokens with session heartbeat
- 60-second stale timeout with automatic lock release
- Director force-unlock capability
- Backward-compatible: all requests pass through when no operators registered

**Macros**
- Sequential macro execution with 5 action types (cut, preview, transition, wait, set_audio)
- File-based JSON storage with atomic writes
- Keyboard triggers (Ctrl+1–9)
- Context cancellation for mid-execution abort

**UI**
- Traditional broadcast layout: multiview, preview/program buses, audio mixer, transition controls
- Simple mode for volunteers (`?mode=simple`) — just sources + CUT/DISSOLVE
- Keyboard shortcuts for every action (press `?` to see them)
- Responsive design with 4 breakpoints (1920/1024/768px) and touch support
- Audio level bars on multiview source tiles
- Optimistic UI with 2-second TTL for instant feedback
- Preset save/recall
- Macro panel with run/edit/delete
- Upstream key configuration panel
- Replay controls (mark-in/out, play, stop, speed)
- Operator registration and subsystem lock indicators

**Infrastructure**
- WebTransport (QUIC/HTTP3) for sub-frame latency state sync
- REST polling fallback when WebTransport is unavailable
- Prometheus metrics + pprof debug endpoints
- Bearer token API authentication
- Single-binary deployment with embedded UI
- Docker image
- GitHub Actions CI (lint, test, Docker build)

## Quick Start

### Prerequisites

| | Version | Install |
|---|---|---|
| Go | 1.25+ | [go.dev/dl](https://go.dev/dl/) |
| Node.js | 22+ | [nodejs.org](https://nodejs.org/) |
| FFmpeg libs | — | See below |

**macOS:**

```bash
brew install ffmpeg fdk-aac pkg-config
```

**Linux (Debian/Ubuntu):**

```bash
sudo apt-get install -y libavcodec-dev libavutil-dev libx264-dev libfdk-aac-dev pkg-config
```

### Run the Demo

```bash
git clone https://github.com/zsiec/switchframe.git
cd switchframe
cd ui && npm ci && cd ..
make demo
```

Open **http://localhost:5173**. Four simulated cameras will appear.

### What You Can Try

| Action | Mouse | Keyboard |
|---|---|---|
| Set preview | Click source tile | `1`–`9` |
| Hard cut | Click **CUT** | `Space` |
| Dissolve | Click **AUTO** | `Enter` |
| Fade to black | Click **FTB** | `F1` |
| Toggle DSK | — | `F2` |
| Manual transition | Drag **T-bar** | — |
| Hot-punch to program | — | `Shift+1`–`9` |
| Run macro | — | `Ctrl+1`–`9` |
| Transition type: mix | — | `Alt+1` |
| Transition type: dip | — | `Alt+2` |
| Switch bottom panel | — | `Ctrl+Shift+1`–`6` |
| Audio faders | Drag fader | — |
| Toggle mute/AFV | Click button | — |
| Record | Click **REC** | — |
| SRT output | Click **SRT** | — |
| Fullscreen | — | `` ` `` |
| Keyboard help | — | `?` |
| Simple mode | Add `?mode=simple` to URL | — |

## Development

```bash
make dev          # Go server + Vite dev server (no demo sources)
make demo         # 4 simulated cameras, open localhost:5173
make build        # Production binary with embedded UI → bin/switchframe
make docker       # Multi-stage Docker image
make test-all     # Go tests + Vitest + Playwright E2E
make lint         # go vet + svelte-check
make format       # gofmt + prettier
make clean        # Remove build artifacts
```

### Running Tests

```bash
# Go (with race detector)
cd server && go test ./... -race

# Frontend unit tests
cd ui && npx vitest run

# E2E tests (builds static app, serves on :4173)
cd ui && npx playwright test
```

~1000 Go tests, 562 Vitest tests, and 45 Playwright E2E tests — all passing with `-race`.

### Project Structure

```
server/                     Go backend (github.com/zsiec/switchframe/server)
  cmd/switchframe/          Binary entry point, admin endpoints, static embed
  switcher/                 Core switching engine (state machine, frame routing, frame sync)
  audio/                    FDK AAC decode/mix/encode, EQ, compressor, crossfade, limiter
  transition/               Transition engine (YUV420 blend, scaler, smoothstep easing)
  output/                   MPEG-TS recording, SRT caller/listener, confidence monitor
  control/                  REST API handlers, auth middleware, MoQ state publisher
  codec/                    FFmpeg/OpenH264 bindings, NALU helpers, encoder auto-detect
  graphics/                 DSK compositor, chroma/luma keyer, upstream key processor
  stinger/                  Stinger clip store (PNG sequence → YUV420 + alpha)
  macro/                    Macro store and sequential runner
  operator/                 Operator registration, sessions, role-based locking
  replay/                   Instant replay buffers, clip extraction, variable-speed player
  preset/                   Preset save/recall (file-based)
  metrics/                  Prometheus counters, gauges, histograms
  debug/                    Snapshot collector, circular event log
  demo/                     Simulated camera sources (synthetic or MPEG-TS clips)
  internal/                 Shared types (ControlRoomState, SourceInfo, etc.)
ui/                         SvelteKit frontend (Svelte 5 + TypeScript)
  src/components/           30+ Svelte 5 components (runes syntax throughout)
  src/lib/api/              REST API client + TypeScript types
  src/lib/state/            Reactive store with optimistic updates
  src/lib/transport/        WebTransport connection + MoQ media pipeline
  src/lib/audio/            Client-side PFL (pre-fade listen)
  src/lib/video/            Dissolve renderer (Canvas 2D + WebGPU stub)
  src/lib/keyboard/         Capture-phase keyboard shortcut handler
  src/lib/graphics/         Graphics template publisher
  src/lib/prism/            Vendored Prism TS modules (MoQ, decode, render)
```

## Architecture

```mermaid
graph TD
    subgraph browser["Browser (Svelte 5 SPA)"]
        mv["Multiview Tiles"]
        pp["Preview / Program"]
        am["Audio Mixer + EQ"]
        tc["Transition Controls"]
        rp["Replay Panel"]
        op["Operator Controls"]
    end

    mv ---|"MoQ/WT"| prism
    pp ---|"MoQ/WT"| prism
    am ---|"REST 20Hz"| prism
    tc ---|"REST"| prism
    rp ---|"REST"| prism
    op ---|"REST"| prism

    subgraph server["Server (Go)"]
        prism["Prism<br/>MoQ/WebTransport :8080 · REST :8081"]
        prism --> switcher["Switcher<br/>cut / fade / dissolve / wipe / stinger"]
        prism --> api["Control API<br/>REST + MoQ state"]
        switcher --> keyer["Upstream Keyer<br/>chroma / luma"]
        keyer --> mixer["Audio Mixer<br/>trim · EQ · compressor · fader · limiter"]
        switcher --> relay["Program Relay"]
        mixer --> relay
        relay --> dsk["DSK Graphics<br/>compositor"]
        dsk --> rec["Recording<br/>MPEG-TS rotation"]
        dsk --> srt["SRT Output<br/>push / pull"]
        dsk --> conf["Confidence<br/>1fps JPEG"]
        switcher --> replay["Instant Replay<br/>per-source buffers"]
        replay --> replayRelay["Replay Relay"]
        api --> operators["Operator Manager<br/>roles · locks · sessions"]
        api --> macros["Macro Runner<br/>sequential execution"]
        admin["Admin :9090<br/>/metrics · /health · /pprof"]
    end
```

**Key design choices:**
- **Server-side switching** — the server produces the authoritative program output, not the browser
- **YUV420 blending (BT.709)** — matches hardware switchers (ATEM, Ross), avoids YUV↔RGB round-trips
- **MoQ for state + media** — single QUIC connection carries both control state and video/audio
- **Audio passthrough optimization** — bypasses decode/encode when single source at 0 dB with no EQ/compressor
- **Lock-free hot path** — `atomic.Pointer` for source viewers, `sync.Map` for health timestamps, `RLock` for frame routing
- **Three-phase transitions** — validate under lock, decode/warm without lock, publish under lock (keeps frame routing unblocked)
- **Web Workers + SharedArrayBuffer** — video decodes in a worker, audio uses AudioWorklet with lock-free ring buffers

## Configuration

### CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--demo` | `false` | Start with 4 simulated camera sources |
| `--demo-video <dir>` | — | Use real MPEG-TS clips from directory (requires `--demo`) |
| `--log-level` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `--admin-addr` | `:9090` | Admin/metrics server listen address |
| `--api-token` | auto-generated | Bearer token for API auth (or set `SWITCHFRAME_API_TOKEN`) |
| `--frame-sync` | `false` | Enable freerun frame synchronizer (aligns sources to common tick) |
| `--replay-buffer-secs` | `60` | Per-source replay buffer duration in seconds (0 to disable, max 300) |

### Environment Variables

| Variable | Description |
|---|---|
| `SWITCHFRAME_API_TOKEN` | API authentication token (overridden by `--api-token` flag) |
| `APP_ENV=production` | Switches to JSON structured logging |

### Ports

| Port | Protocol | Purpose |
|---|---|---|
| 8080 | QUIC/HTTP3 | WebTransport (MoQ media + state) |
| 8081 | HTTP/TCP | REST API (used by Vite dev proxy, curl) |
| 9090 | HTTP/TCP | Admin: Prometheus metrics, health, pprof |
| 9000 | UDP | SRT listener (configurable) |

### Video Codec Auto-Detection

On startup, SwitchFrame probes available H.264 encoders and selects the best one:

1. NVENC (CUDA GPU)
2. VA-API (Linux GPU)
3. VideoToolbox (macOS GPU)
4. libx264 (CPU)
5. OpenH264 (fallback, requires build tag)

Hardware encoder strongly recommended for 1080p transitions. Software-only (libx264) is marginal above 720p.

## API

All endpoints require `Authorization: Bearer <token>` (except in demo mode). When operators are registered, commands also require role permission and subsystem lock ownership.

### Switching

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/switch/state` | Full control room state |
| `POST` | `/api/switch/cut` | Hard cut to source |
| `POST` | `/api/switch/preview` | Set preview source |
| `POST` | `/api/switch/transition` | Start transition (mix/dip/wipe/stinger, 100–5000ms) |
| `POST` | `/api/switch/transition/position` | T-bar position (0–1) |
| `POST` | `/api/switch/ftb` | Fade to black / reverse |

### Sources

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/sources` | List sources with health, tally, delay |
| `POST` | `/api/sources/{key}/label` | Set source display label |
| `POST` | `/api/sources/{key}/delay` | Set source delay (0–500ms) |
| `PUT` | `/api/sources/{key}/position` | Set source display position |
| `PUT` | `/api/sources/{key}/key` | Configure upstream chroma/luma key |
| `GET` | `/api/sources/{key}/key` | Get upstream key config |
| `DELETE` | `/api/sources/{key}/key` | Remove upstream key |

### Audio

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/audio/trim` | Set channel input trim (−20 to +20 dB) |
| `POST` | `/api/audio/level` | Set channel fader level |
| `POST` | `/api/audio/mute` | Set channel mute |
| `POST` | `/api/audio/afv` | Set audio-follows-video |
| `POST` | `/api/audio/master` | Set master output level |
| `PUT` | `/api/audio/{source}/eq` | Set EQ band (Low/Mid/High) |
| `GET` | `/api/audio/{source}/eq` | Get all EQ bands |
| `PUT` | `/api/audio/{source}/compressor` | Set compressor parameters |
| `GET` | `/api/audio/{source}/compressor` | Get compressor settings + gain reduction |

### Output

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/recording/start` | Start MPEG-TS recording |
| `POST` | `/api/recording/stop` | Stop recording |
| `GET` | `/api/recording/status` | Recording status (filename, bytes, duration) |
| `POST` | `/api/output/srt/start` | Start SRT output (caller or listener mode) |
| `POST` | `/api/output/srt/stop` | Stop SRT output |
| `GET` | `/api/output/srt/status` | SRT status (connections, overflow count) |
| `GET` | `/api/output/confidence` | Program thumbnail (JPEG, ≤1fps) |

### Stinger Transitions

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/stinger/list` | List loaded stinger clips |
| `POST` | `/api/stinger/{name}/upload` | Upload PNG sequence as zip (256MB limit) |
| `POST` | `/api/stinger/{name}/cut-point` | Set cut point (0–1) |
| `DELETE` | `/api/stinger/{name}` | Delete stinger clip |

### Graphics (DSK)

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/graphics/on` | Cut overlay on |
| `POST` | `/api/graphics/off` | Cut overlay off |
| `POST` | `/api/graphics/auto-on` | Fade overlay in (500ms) |
| `POST` | `/api/graphics/auto-off` | Fade overlay out (500ms) |
| `GET` | `/api/graphics/status` | Overlay status |
| `POST` | `/api/graphics/frame` | Upload RGBA overlay (up to 4K) |

### Instant Replay

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/replay/mark-in` | Set replay in-point (wall clock) |
| `POST` | `/api/replay/mark-out` | Set replay out-point |
| `POST` | `/api/replay/play` | Play clip (speed 0.25x–1x, loop option) |
| `POST` | `/api/replay/stop` | Stop replay playback |
| `GET` | `/api/replay/status` | Replay state, marks, position |
| `GET` | `/api/replay/sources` | Per-source buffer info (frames, GOPs, duration) |

### Macros

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/macros` | List all macros |
| `GET` | `/api/macros/{name}` | Get macro definition |
| `PUT` | `/api/macros/{name}` | Create or update macro |
| `DELETE` | `/api/macros/{name}` | Delete macro |
| `POST` | `/api/macros/{name}/run` | Run macro (sequential, cancellable) |

### Operators

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/operator/register` | Register operator (returns token) |
| `POST` | `/api/operator/reconnect` | Re-establish session from token |
| `POST` | `/api/operator/heartbeat` | Keep session alive (60s timeout) |
| `GET` | `/api/operator/list` | List operators with connection status |
| `POST` | `/api/operator/lock` | Acquire subsystem lock |
| `POST` | `/api/operator/unlock` | Release own lock |
| `POST` | `/api/operator/force-unlock` | Director force-release any lock |
| `DELETE` | `/api/operator/{id}` | Remove operator |

### Presets

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/presets` | List presets |
| `POST` | `/api/presets` | Create preset from current state |
| `GET` | `/api/presets/{id}` | Get preset |
| `PUT` | `/api/presets/{id}` | Update preset name |
| `DELETE` | `/api/presets/{id}` | Delete preset |
| `POST` | `/api/presets/{id}/recall` | Recall preset (best-effort with warnings) |

### Debug

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/debug/snapshot` | Debug snapshot (all subsystems) |

## Deployment

### Single Binary

```bash
make build
./bin/switchframe --api-token "your-secret-token"
```

The production build embeds the compiled UI — no Node.js runtime needed in production.

### Docker

```bash
make docker
docker run -p 8080:8080 -p 8081:8081 -p 9090:9090 -p 9000:9000/udp \
  -e SWITCHFRAME_API_TOKEN="your-secret-token" \
  switchframe
```

The image is based on `debian:bookworm-slim` (~80MB + runtime libs) with a non-root `switchframe` user. Health check is built in (`/health` on the admin port).

## Browser Requirements

SwitchFrame uses modern web APIs for low-latency media:

| API | Required For | Fallback |
|---|---|---|
| [WebTransport](https://developer.mozilla.org/en-US/docs/Web/API/WebTransport) | Sub-frame state sync | REST polling (500ms) |
| [WebCodecs](https://developer.mozilla.org/en-US/docs/Web/API/WebCodecs_API) | Video/audio decode | — |
| [SharedArrayBuffer](https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/SharedArrayBuffer) | Audio worklet ring buffer | — |
| [WebGPU](https://developer.mozilla.org/en-US/docs/Web/API/WebGPU_API) | Dissolve preview (future) | Canvas 2D |

Tested in Chrome 120+ and Safari 26.4+. The dev server sets the required `Cross-Origin-Opener-Policy` and `Cross-Origin-Embedder-Policy` headers for `SharedArrayBuffer`.

## Built With

- **[Prism](https://github.com/zsiec/prism)** — MoQ/WebTransport media server (Go)
- **[Svelte 5](https://svelte.dev/)** — Frontend framework (runes reactivity)
- **[SvelteKit](https://svelte.dev/docs/kit)** — Static SPA build with adapter-static
- **[FDK-AAC](https://github.com/mstorsjo/fdk-aac)** — AAC audio codec
- **[FFmpeg libavcodec](https://ffmpeg.org/)** — H.264 video encode/decode
- **[go-astits](https://github.com/asticode/go-astits)** — MPEG-TS muxer
- **[srtgo](https://github.com/zsiec/srtgo)** — Pure Go SRT implementation
- **[Prometheus](https://prometheus.io/)** — Metrics and monitoring

## Documentation

- **[API Reference](docs/api.md)** — All endpoints with request/response examples
- **[Deployment Guide](docs/deployment.md)** — Production setup, Docker, TLS, monitoring
- **[Architecture](docs/architecture.md)** — System design, data flow, key decisions

## License

[MIT](LICENSE)
