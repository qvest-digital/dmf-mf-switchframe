# Loadtest

`cmd/loadtest` is an in-process load harness for the switcher + mixer. It drives N synthetic V210 sources at a configurable resolution and frame rate, captures per-tick ingest + runtime + pipeline + mixer counters, and emits NDJSON to stdout (or `--output-file`). See `server/cmd/loadtest/README.md` for the flag reference and full schema.

This doc covers when to use it, how to interpret the metrics, and how to re-baseline.

## What this harness measures

| Metric | Definition | What regresses it |
|---|---|---|
| `sources.*.raw_frame_count` | Monotonic `sw.sourceState.rawFrameCount` per source. | Broken `IngestRawVideo`, source goroutine hang, demo reader throttle. |
| `sources.*.ingest_fps` | Per-tick delta of `raw_frame_count` / elapsed. Stable ~fps = healthy. | Same as above. |
| `sources.*.health` | `switcher.healthMonitor` status (`healthy` / `stale` / `no_signal` / `offline`). | Stale = no frames in health interval. |
| `pipeline.last_ns` / `pXX_ns` | `sw.videoProcLastNano` (pipeline wall-time per frame). Percentiles across measure-phase samples only. | Lock contention, allocation-heavy nodes, encoder regressions. |
| `pipeline.total_deadline_violations` | Delta of `sw.deadlineViolations` across the measure window. | Frame budget exceeded — scaling cliff. |
| `pipeline.total_proc_dropped` | Delta of `sw.videoProcDropped` across the measure window. | Queue overflow — hot path can't keep up. |
| `pipeline.max_queue_len` | Peak `len(videoProcCh)` observed. | Backpressure appearing. |
| `mixer.max_inter_frame_gap_ns` | `mixer.maxInterFrameNano` (max output-tick gap since startup). | Audio tick stalls. |
| `mixer.slippage_ms` | `abs(max_gap − 21.33 ms)` once the mixer has output at least one frame. | Same as above. |
| `mixer.total_frames_output` | `mixer.outputFrameCount` at end of run. | Zero = mixer never produced output (encoder factory missing). |
| `runtime.gc_pause_p99_ns` | 99th pct of tick-observed latest pauses. | GC tail regressions. |
| `runtime.max_goroutines` | Peak during the measure window. | Goroutine leaks. |

## What this harness does NOT measure

- **Real decode cost.** Synthetic V210 sources bypass the decoder path entirely.
- **Real encode cost.** Video uses `transition.NewMockEncoder` (trivial); audio uses a no-op encoder. The mock-encoder choice deliberately keeps the harness self-contained (no cgo required) and isolates the switcher/compositor cost, but it means `pipeline.last_ns` will be much smaller than a production run with a real encoder.
- **Output / record / SRT push.** The program relay has no readers.

If you need real encode numbers, use `cmd/switchframe` with production config and point at a real ingest source.

## Pinned regression test

`TestLoadtest_32Sources_1080p60_BaselinePinned` in `server/cmd/loadtest/baseline_test.go` asserts three invariants on the canonical scaling run (32 sources × 1080p60 × 15 s, 5 s warmup, 1 Hz tick):

1. `total_deadline_violations == 0` over the measure window
2. `total_proc_dropped == 0` over the measure window
3. `p99_ns < 5 ms` absolute sanity bound

**What the test intentionally does NOT do:** compare current `p99_ns` to the baseline numerically. A "within 20 %" target is unachievable with 10 samples at microsecond scale — observed variance between consecutive runs of the identical binary is easily a factor of 30-100. Tightening the check invites flake; loosening it past ~10× makes the check meaningless. The baseline JSON is still checked in and logged on every run so humans can diff it across structural switcher changes.

The test is opt-in:

- runs only when `LOADTEST_PERF_GATE=1` is set in the environment
- always skips on `-short`

Default-opt-out avoids putting a 17-second floor on every `go test ./... -race` invocation. CI workflows that want the perf gate set the env var explicitly.

## When to run the harness

- **Ingest-path smoke test.** Refactoring `switcher.handleRawVideoFrame`, `sourceViewer`, or source goroutine lifecycle: run 32 × 60 s and confirm `total_raw_frames ≈ fps × duration × sources` and `max_goroutines` doesn't balloon.
- **GC pressure A/B.** Pre vs post a hot-path refactor: compare `gc_pause_p99_ns`. 2-3× regression = hot-path allocation.
- **Scaling smoke.** Crank `--sources` until `total_deadline_violations > 0`. That's the current ceiling for this hardware / build.
- **Before a structural switcher refactor.** Capture a baseline JSON pre-change, run again post-change, cite the diff in the commit body or PR description.

## Comparing runs

Same host is the only meaningful comparison. CPU frequency scaling, background processes, container noisy-neighbors, GC noise, and kernel scheduling all dominate tiny changes on different hardware.

**Rules of thumb:**

- Single-host A/B: run 3 iterations of each, take min or median. Discard the first iteration (cold cache).
- Cross-host comparisons are not meaningful.
- Warmup is excluded from the summary via `phase: "warmup"` tagging on per-tick events.
- `-race` adds ~3× pipeline cost. Never compare a race run to a non-race run.

## Re-baselining

The baseline lives at `server/cmd/loadtest/testdata/loadtest_baseline.json`. Re-capture it when:

1. An intentional, measured improvement lands. Cite the delta in the commit body: `loadtest baseline: p99 31.8 μs -> 18.5 μs`.
2. The reference host changes (Go version bump, hardware swap, macOS/Linux change). Record the new spec in this doc and re-baseline.

**Do NOT** re-baseline to silence a regression. If the pinned test fails, diagnose first.

### How to capture

```
cd server
LOADTEST_CAPTURE_BASELINE=1 go test -race -count=1 -run TestCaptureBaseline ./cmd/loadtest -v
```

This writes `server/cmd/loadtest/testdata/loadtest_baseline.json` under the same runtime conditions the pinned test will use (race detector on). Commit the updated file.

## Reference host

The initial Phase 2 baseline was captured on:

- **Host:** macOS arm64, M-series silicon, local dev.
- **Go:** whatever `go.mod` pins at capture time.
- **Build:** `go test -race ./cmd/loadtest` (no `embed_ui`, no GPU tags).
- **Encoder:** `transition.NewMockEncoder` (mock video encoder) + no-op audio encoder.

Numbers at this scale are not comparable across hosts.

## Caveats

- Synthetic sources have zero decode cost. Real SRT adds 20–50 ms jitter.
- Mock encoder means pipeline p99 numbers are a lower bound on production cost.
- No output / record / SRT push. Use `cmd/switchframe` for those paths.
