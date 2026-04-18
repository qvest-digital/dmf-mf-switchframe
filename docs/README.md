# Switchframe docs

This directory is the technical documentation for Switchframe — a browser-based live video switcher written in Go with a Svelte UI. The top of the tree is organized by what you're trying to do: `architecture.md` tells you the whole story at a glance, `concepts/` gives you the mental models that cut across every subsystem, `reference/` is the lookup material when you know what you need, `subsystems/` is the per-package deep dive when you're about to touch one, `integration/` is the cross-tier contracts for talking to the engine from other processes, and `operations/` is for deploying and running the binary in production.

If you are new to the project, read [architecture.md](architecture.md) first. It is the map to the rest of the docs.

## Getting oriented

- **[architecture.md](architecture.md)** — the spine. The whole engine at a glance with links to everything else. Start here.

## Concepts

Mental models that apply across the whole engine. Read these when you need to reason about how pieces fit together, not just how a single subsystem works.

- **[concepts/pipeline.md](concepts/pipeline.md)** — always-decode-per-source architecture, the atomic pipeline node chain, reference-counted `ProcessingFrame`, the pre-allocated `FramePool`.
- **[concepts/media-path.md](concepts/media-path.md)** — one frame end to end: arrival, synchronization, processing, fan-out. Audio and captions as parallel lanes.
- **[concepts/frame-sync-and-timing.md](concepts/frame-sync-and-timing.md)** — how freerun sources become a smooth output. Tick loop, PTS rewriting, FRC, MCFI, audio PTS correction delta.
- **[concepts/locking-and-concurrency.md](concepts/locking-and-concurrency.md)** — the three rules (mutex for config, atomic for snapshots, channels for hand-off), a lock inventory across every subsystem, and the deadlock-free guarantees.
- **[concepts/gpu.md](concepts/gpu.md)** — CUDA / Metal / CPU-fallback backend selection, the pitched frame pool, upload and download kernels, the full kernel catalog, encoder integration.

## Reference

Lookup material. Shaped for "I know what I need, where is it?" not for reading end to end.

- **[reference/api.md](reference/api.md)** — REST API index. 291 endpoints across 25 subsystem groups, with the global conventions (auth, CORS, error mapping, state broadcast) at the top and per-subsystem detail files linked below.
- **[reference/state-broadcast.md](reference/state-broadcast.md)** — the `ControlRoomState` schema. What fires a broadcast, what every field in the JSON means, how clients should reconcile.
- **[reference/fast-control.md](reference/fast-control.md)** — binary WebTransport datagram protocol for T-bar drag, DVE slot drag, graphics layer drag, PTP clock sync. Full wire format per message type.
- **[reference/metrics.md](reference/metrics.md)** — every Prometheus metric exported, with labels and units.
- **[reference/config-and-flags.md](reference/config-and-flags.md)** — every command-line flag, environment variable, and build tag the engine reads. State-directory layout.
- **[reference/file-formats.md](reference/file-formats.md)** — on-disk JSON and binary formats for presets, macros, operators, SRT sources, stingers, DVE presets, clips, ST maps, SCTE-35 rules, playout cache, as-run logs, crash-recovery snapshots, color LUTs, wipe patterns.

Per-endpoint detail lives under [reference/api/](reference/api/) — one file per subsystem (switching, transition, audio, clips, playout, etc.).

## Subsystems

Per-package deep dives. Read the one for the package you're about to touch; cross-reference liberally because the pieces compose.

- **[subsystems/switcher.md](subsystems/switcher.md)** — the live production engine: state machine (CUT / AUTO / FTB / TRANSITION), source viewers, per-source decoders, health monitor, the atomic processing loop.
- **[subsystems/transition.md](subsystems/transition.md)** — dissolve / dip / wipe / stinger / FTB blending in YUV420 with SIMD kernels, scalers, and CSS-compatible easing curves.
- **[subsystems/graphics-and-dve.md](subsystems/graphics-and-dve.md)** — three compositing stages (upstream key, DVE PIP, DSK graphics) that share YUV420 blend kernels. Animation engines, text rendering, HTML5 bridge.
- **[subsystems/stmap.md](subsystems/stmap.md)** — per-pixel coordinate warp for lens correction and creative effects. Generator catalog, LUT building, SIMD warp kernels.
- **[subsystems/audio.md](subsystems/audio.md)** — clock-driven mixer: per-source DSP chain, ring-buffer decoupling, BS.1770-4 LUFS, brickwall limiter, crossfades, FDK-AAC via cgo.
- **[subsystems/captions.md](subsystems/captions.md)** — CEA-608/708: off / passthrough / author / auto modes, one pair per frame, SEI injection, SMPTE ST 334 VANC for MXL.
- **[subsystems/comms.md](subsystems/comms.md)** — operator voice intercom over WebTransport bidi streams, Opus-encoded audio, N-1 mixer per participant.
- **[subsystems/output.md](subsystems/output.md)** — MPEG-TS muxer, multi-destination fan-out (file / SRT caller / SRT listener / RTMP), CBR pacer, async adapter, confidence monitor.
- **[subsystems/srt-ingest.md](subsystems/srt-ingest.md)** — SRT input in both directions (listener and caller), FFmpeg AVIO bridge, persistence, stats.
- **[subsystems/mxl.md](subsystems/mxl.md)** — POSIX shared-memory V210 + PCM + VANC with NMOS flow discovery. Single-producer / single-consumer steady-rate writer.
- **[subsystems/codec.md](subsystems/codec.md)** — H.264 and HEVC encoder / decoder factories, startup auto-probe, NALU and ADTS helpers.
- **[subsystems/control-plane.md](subsystems/control-plane.md)** — HTTP middleware stack, operator registration, subsystem locking, presets, macros, state broadcast plumbing.
- **[subsystems/replay.md](subsystems/replay.md)** — per-source GOP-aligned circular buffer, slow-motion with phase-vocoder audio time-stretch, clip export bridge.
- **[subsystems/clips.md](subsystems/clips.md)** — media library, four player slots, TS + MP4 demuxers, decode → re-encode pipeline, hold-last-frame.
- **[subsystems/playout.md](subsystems/playout.md)** — schedule-driven virtual channels, pod boards with frame-accurate splice coordination, as-run log, BXF import.
- **[subsystems/scte35.md](subsystems/scte35.md)** — SCTE-35 injector lifecycle, rules engine, bidirectional SCTE-104 VANC translation, per-destination filtering.

## Integration

Cross-tier contracts. Read these if you are writing a client that talks to the engine from another process — a new UI, an automation tool, a monitoring dashboard, or the shipped sidecars.

- **[integration/ui-server-contract.md](integration/ui-server-contract.md)** — REST + MoQ control track + fast-control datagrams + operator auth. What a browser (or any client) needs to implement.
- **[integration/cef-protocol.md](integration/cef-protocol.md)** — the two Unix domain sockets (control JSON + binary frames) between the engine and the CEF graphics sidecar. OGraf / CasparCG template conventions.
- **[integration/asr-sidecar.md](integration/asr-sidecar.md)** — shared-memory audio ring + length-prefixed JSON socket between the engine and the ASR sidecar. Parakeet-TDT and whisper.cpp backends.

## Operations

How to deploy, configure, and run the engine in production.

- **[operations/deployment.md](operations/deployment.md)** — build chain, dev quick start, HTTPS / HTTP/3 cert story, GPU setup, ports, system tuning, Docker, systemd unit, dual-engine active-active.

## Beyond the technical docs

Three durable documents outside this tree describe the project's why, where it's going, and how agents collaborate on it:

- **docs/VISION.md** — the product north star: what Switchframe is, who it's for, the moats, the principles, what we won't do.
- **docs/ROADMAP.md** — the phased plan: what's in Now, Next, and Later.
- **docs/AGENTS.md** — the operating manual for agents working in this codebase: how to pick an item, how to work it, how to commit, how to hand off.

Work-in-progress tracking lives in `docs/backlog/` (file-per-item, gitignored in some checkouts). Implementation plans for in-flight work live in `docs/plans/` (gitignored).
