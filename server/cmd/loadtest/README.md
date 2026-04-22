# `cmd/loadtest`

In-process load harness. Drives N synthetic V210 sources into a `switcher.Switcher` + `audio.Mixer` pair and emits NDJSON samples plus a final summary.

**Scope as of phase 2:** per-source ingest counters, runtime + GC pressure, pipeline latency, mixer output-tick slippage. A mock video encoder and a no-op audio encoder stand in for the real codecs so the harness does not need cgo. A pinned regression test runs the canonical 32-source / 1080p60 / 15 s config and gates on `deadline_violations`, `proc_dropped`, and an absolute `p99 < 5 ms` sanity bound.

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
| `--tick-interval` | 1s | min 10ms; test configs drop this lower |
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
  "t": 6.002,
  "sources": {
    "mxl:loadtest0": { "health": "healthy", "raw_frame_count": 240, "ingest_fps": 59.8 }
  },
  "pipeline": {
    "last_ns": 7250,
    "e2e_last_ns": 0,
    "deadline_violations": 0,
    "queue_len": 0,
    "proc_dropped": 0
  },
  "mixer": {
    "max_inter_frame_gap_ns": 22412000,
    "slippage_ms": 1.0787,
    "frames_output": 280,
    "mix_cycle_last_ns": 0
  },
  "runtime": { "gc_pause_last_ns": 97833, "goroutines": 24 }
}
```

Notes:

- `pipeline.last_ns` is the most-recent `sw.videoProcLastNano` reading (pipeline wall-time for the last processed frame). With the mock video encoder it stays in the single-digit microseconds; a real encoder would dominate.
- `pipeline.deadline_violations` / `proc_dropped` are cumulative switcher counters; the final summary reports the delta across the measure window.
- `mixer.max_inter_frame_gap_ns` is the max-ever gap from `mixer.maxInterFrameNano` (monotonic), not a per-tick delta — read it as "worst output-tick jitter seen since startup."
- `mixer.slippage_ms = abs(max_inter_frame_gap_ns − 21.33 ms)`; 21.33 ms is the expected 1024-sample cadence at 48 kHz. The field is reported as 0 until the mixer has emitted at least one frame.

### Final summary

```json
{
  "phase": "final",
  "duration_s": 15.02,
  "samples": 10,
  "sources": { "min_ingest_fps": 58.7, "mean_ingest_fps": 59.98, "total_raw_frames": 17615 },
  "pipeline": {
    "p50_ns": 4100, "p95_ns": 6800, "p99_ns": 21041, "max_ns": 29000,
    "total_deadline_violations": 0, "total_proc_dropped": 0, "max_queue_len": 0
  },
  "mixer": { "max_slippage_ms": 1.30, "mean_slippage_ms": 1.08, "total_frames_output": 649 },
  "runtime": { "gc_pause_p99_ns": 340000, "max_goroutines": 136 },
  "source_keys": ["mxl:loadtest0", "..."],
  "config": { "sources": 32, "width": 1920, "height": 1080, "fps": 60, "duration_s": 15, "warmup_s": 5, "tick_interval_ms": 1000, "api_token_set": false }
}
```

`pipeline.{p50,p95,p99,max}_ns` is computed across measure-phase `pipeline.last_ns` samples. With a 1 Hz tick and a 10 s measure window, that's only 10 samples — a single GC spike can easily dominate p99. Treat these numbers as a *rough* sense of pipeline cost; use `cpu` / `mem` profiles via `--profile` for real attribution.

`sources.min_ingest_fps` / `mean_ingest_fps`: aggregate across all (source × tick) pairs in the measure phase.

## Pinned regression test

`TestLoadtest_32Sources_1080p60_BaselinePinned` runs the canonical config (32 sources × 1080p60 × 15 s / 5 s warmup / 1 Hz tick) and asserts:

- `total_deadline_violations == 0` over the measure window
- `total_proc_dropped == 0` over the measure window
- `p99_ns < 5 ms` absolute sanity bound

The test is opt-in: it runs only when `LOADTEST_PERF_GATE=1` is set in the environment (and always skips on `-short`). Default-opt-out avoids adding a 17-second floor to every `go test ./... -race` invocation — CI that cares about the gate sets the env var explicitly. It does **not** compare `p99_ns` against the checked-in baseline numerically — with 10 samples at microsecond scale the natural per-run variance is factor-of-many, so any tight numeric comparison will flake. The baseline JSON is kept for humans to diff across structural switcher changes (lock splits, multiview reorganizations, pipeline-node reorders). See `docs/reference/loadtest.md` for re-baselining and interpretation guidance.

## Profiling

- `--profile cpu` → `pprof.StartCPUProfile` wraps the full run. Analyze with `go tool pprof /tmp/loadtest-cpu.prof`.
- `--profile mem` → heap profile via `pprof.WriteHeapProfile` after the run. Stderr warns when combined with `--sources > 16`.
- `--profile both` does both. `--profile none` (default) writes nothing.

## What this harness does NOT do

- **No decode cost.** Synthetic V210 sources skip the real decoder path. Baseline numbers from this harness are only comparable to other synthetic runs, never to production SRT numbers.
- **No real encode cost.** The video encoder factory uses `transition.NewMockEncoder`; the audio encoder is a no-op. Pipeline numbers reflect switcher + compositor + ST-map + sink cost only.
- **No output.** The switcher's program relay is constructed but not read by any viewer; there is no MPEG-TS mux, no SRT push, no recording.
- **No HTTP or control plane.** `--api-token` is parsed and masked but currently unused.

## Token semantics

`--api-token` or env `SWITCHFRAME_API_TOKEN` is accepted for forward compatibility with a future remote-driver mode. It is never logged. The final summary includes `"api_token_set": true|false` without the value.
