# `cmd/loadtest`

In-process load harness. Drives N synthetic V210 sources into a `switcher.Switcher` + `audio.Mixer` pair and emits NDJSON samples plus a final summary.

**Phase 1 scope.** The harness measures what the in-process wiring actually exercises: per-source ingest counters and runtime / goroutine / GC pressure. Full pipeline and mixer-cycle instrumentation need `switcher.BuildPipeline()` + an audio encoder factory (cgo); those are deferred to Phase 2 along with a pinned baseline regression test.

## Usage

```
make loadtest
./bin/loadtest \
  --sources 32 \
  --resolution 1920x1080 \
  --fps 60 \
  --duration 60s \
  --warmup 10s \
  --tick-interval 1s \
  --output json \
  --profile none
```

All flags:

| Flag | Default | Notes |
|---|---|---|
| `--sources` | 4 | 1..256 synthetic sources |
| `--resolution` | `1920x1080` | `WxH`, width mult-of-6, height even |
| `--fps` | 60 | frame rate (float) |
| `--duration` | 60s | total run, inclusive of warmup |
| `--warmup` | 10s | excluded from final summary |
| `--tick-interval` | 1s | min 10ms; test config can drop this |
| `--output` | `json` | `csv` reserved (reject with non-zero exit) |
| `--profile` | `none` | `cpu` → `/tmp/loadtest-cpu.prof`, `mem` → `/tmp/loadtest-mem.prof`, `both` |
| `--api-token` | env `SWITCHFRAME_API_TOKEN` | reserved for future remote-driver mode; masked in any dump |
| `--output-file` | stdout | optional NDJSON destination |

## NDJSON output schema

One object per tick (`phase: "warmup" | "measure"`) plus a trailing `phase: "final"` summary.

### Per-tick event

```json
{
  "phase": "measure",
  "t": 1.002,
  "sources": {
    "mxl:loadtest0": { "health": "healthy", "raw_frame_count": 46, "ingest_fps": 60.0 },
    "mxl:loadtest1": { "health": "healthy", "raw_frame_count": 46, "ingest_fps": 60.0 }
  },
  "runtime": { "gc_pause_last_ns": 97833, "goroutines": 24 }
}
```

`ingest_fps` is 0 on the first tick (no previous sample); thereafter it's the delta of `raw_frame_count` divided by wall-clock elapsed. Per-source Health values come straight from `switcher.health`: `healthy`, `stale`, `no_signal`, `offline`.

### Final summary

```json
{
  "phase": "final",
  "duration_s": 60.02,
  "samples": 50,
  "sources": { "min_ingest_fps": 58.7, "mean_ingest_fps": 59.98, "total_raw_frames": 11520 },
  "runtime": { "gc_pause_p99_ns": 340000, "max_goroutines": 38 },
  "source_keys": ["mxl:loadtest0", "mxl:loadtest1", "..."],
  "config": { "sources": 32, "width": 1920, "height": 1080, "fps": 60, "duration_s": 60, "warmup_s": 10, "tick_interval_ms": 1000, "api_token_set": false }
}
```

`sources.min_ingest_fps` is the minimum per-source per-tick ingest rate across the entire measure phase; `mean_ingest_fps` is the unweighted average across all (source, tick) pairs. `total_raw_frames` is the sum of final `raw_frame_count` values across sources.

## Phase 1 limitations

**What this harness does NOT measure yet:**

- **No pipeline latency.** `switcher.BuildPipeline()` is not called (no pipeline codecs wired), so `s.pipeline.Load() == nil` and every frame short-circuits in `broadcastProcessedFromPF` before reaching the video processing loop. `videoProcLastNano` / deadline counters / `videoProcCh` queue depth stay at 0. Adding them to the schema would be dishonest until BuildPipeline is wired.
- **No mixer cycle timing.** The mixer runs with `EncoderFactory: nil`; `tick()` returns early before `recordAndOutput`, so `FramesOutput` / `MaxInterFrameGapNs` stay 0. Mixer slippage calculation therefore does not belong in Phase 1.
- **No decode cost.** Synthetic V210 sources skip the real decoder path. Baseline numbers from this harness are only comparable to other synthetic runs, **never** to production SRT numbers.
- **No output.** The switcher's program relay is constructed but not read by any viewer; there is no MPEG-TS mux, no SRT push, no recording.
- **No HTTP or control plane.** `--api-token` is parsed and masked but unused in Phase 1.

**What it does measure:** per-source ingest rate (catches broken ingest paths), process goroutine count (catches goroutine leaks), GC pause distribution (catches allocation-heavy changes). This is enough to smoke-test DEBT-001's lock-split work on the ingest side, but not enough yet to pin a pipeline-latency baseline (DEBT-003 AC 4, deferred to Phase 2).

## Profiling

- `--profile cpu` calls `pprof.StartCPUProfile` wrapped around the full run (including warmup). Analyze with `go tool pprof /tmp/loadtest-cpu.prof`.
- `--profile mem` writes a heap profile via `pprof.WriteHeapProfile` after the run. Warn printed on stderr if used with `--sources > 16` (heap sampling amplifies memory pressure).
- `--profile both` does both.
- `--profile none` (default) writes nothing.

## Token semantics

`--api-token` or env `SWITCHFRAME_API_TOKEN` is accepted for forward compatibility with a future remote-driver mode. It is never logged. The final summary includes `"api_token_set": true|false` without the value.
