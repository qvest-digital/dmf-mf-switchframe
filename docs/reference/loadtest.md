# Loadtest

`cmd/loadtest` is an in-process load harness for the switcher + mixer. It drives N synthetic V210 sources at a configurable resolution and frame rate, captures per-tick ingest + runtime counters, and emits NDJSON to stdout (or `--output-file`). See `server/cmd/loadtest/README.md` for the flag reference and full schema.

This doc covers when to use it, how to interpret the metrics it *does* produce, and what's scheduled for Phase 2.

## What Phase 1 measures

| Metric | Definition | What regresses it |
|---|---|---|
| `sources.*.raw_frame_count` | Monotonic `sw.sourceState.rawFrameCount` counter per source. | Broken `IngestRawVideo` path, source goroutine hang, demo reader throttle. |
| `sources.*.ingest_fps` | Per-tick delta of `raw_frame_count` divided by wall-clock elapsed. | Same as above; a stable ~FPS means the ingest path is healthy. |
| `sources.*.health` | `switcher.healthMonitor` status (`healthy` / `stale` / `no_signal` / `offline`). | Stale means no frames in the health interval. |
| `runtime.gc_pause_last_ns` | Latest GC pause from `runtime.MemStats.PauseNs[256]`. | Allocation-heavy changes; raw YUV buffers leaking back to GC. |
| `runtime.goroutines` | `runtime.NumGoroutine()`. | Goroutine leaks in source / decoder / health paths. |
| `runtime.gc_pause_p99_ns` (final) | 99th percentile of tick-observed latest pauses. | GC tail latency regressions. |
| `runtime.max_goroutines` (final) | Peak during the measure window. | Scaling regressions that spawn extra workers. |

## What Phase 2 will add

All of these require real pipeline wiring — `sw.SetPipelineCodecs(...)` + `sw.BuildPipeline()` plus an `audio.MixerConfig.EncoderFactory` — and should land together with a pinned baseline test:

- `pipeline.last_ns` / `p50` / `p95` / `p99` / `max` — from `sw.videoProcLastNano`.
- `pipeline.deadline_violations` / `proc_dropped` / `max_queue_len`.
- `mixer.max_inter_frame_gap_ns` — from `mixer.maxInterFrameNano`; needs an encoder factory so `recordAndOutput` actually runs.
- `mixer.slippage_ms` = `abs(max_gap − 21.33 ms)`; only meaningful once frames are emitted.
- `TestLoadtest_32Sources_1080p60_BaselinePinned` — asserts current `p99` within 20 % of `testdata/loadtest_baseline.json`; skip when `LOADTEST_PERF_GATE=0`.

Phase 2 continues on DEBT-003 and will refresh this doc with baseline capture procedure.

## When to run it

- **Ingest-path smoke test.** Before landing any refactor that touches `switcher.handleRawVideoFrame`, `sourceViewer`, or the source goroutine lifecycle, run 32 sources × 60 s and confirm `total_raw_frames ≈ expected_fps × duration × sources` and `max_goroutines` doesn't balloon.
- **GC pressure A/B.** Run pre-change and post-change, compare `gc_pause_p99_ns`. A 2–3× regression means you allocated in the hot path.
- **Scaling smoke test.** Crank `--sources` up. No pipeline-exhaustion signal in Phase 1, but source-goroutine scaling issues will show up as `ingest_fps` falling below the configured `fps`.

## Comparing runs

The harness is deterministic in source behavior (synthetic patterns, constant fps) but the host matters — CPU frequency scaling, background processes, container noisy-neighbors, GC noise, and kernel scheduling all dominate tiny changes.

**Rules of thumb:**

- Single-host A/B: run 3 iterations of each, take min or median of `gc_pause_p99_ns`. Discard the first iteration (cold cache).
- Cross-host comparisons are not meaningful. Numbers from a Mac M2 cannot be compared to a Linux c5.xlarge.
- Warmup is excluded from the summary via `phase: "warmup"` tagging on per-tick events.

## Re-baselining (Phase 2 onward)

The Phase 2 regression test will assert `p99` within 20 % of `testdata/loadtest_baseline.json`. When to re-baseline:

1. Intentional, measured improvement. Run the harness on the reference host with the canonical config. Replace the baseline file, note the delta in the commit body: `loadtest baseline: p99 4.8ms -> 3.1ms`.
2. Reference host changed (Go version bump, kernel upgrade, hardware swap). Record the new host spec in this doc and re-baseline.

Do NOT re-baseline to silence a regression or chase noise.

## Reference host (Phase 2 placeholder)

To be recorded at Phase 2 capture time.

- **Host:** macOS arm64, M-series silicon, local dev machine.
- **Go version:** captured via `go version` at baseline time.
- **Build:** `go build ./cmd/loadtest` (no `embed_ui`, no GPU tags).

## Caveats

- **Synthetic sources have zero decode cost.** Real SRT inputs add 20–50 ms of decoder jitter that this harness does not exercise.
- **CPU-only build.** GPU-enabled builds may behave differently; document the build config when comparing.
- **No output / record / SRT push.** Use `cmd/switchframe` with production config for those paths.
