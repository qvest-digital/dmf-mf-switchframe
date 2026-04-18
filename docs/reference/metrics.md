# Metrics

> **Where you are:** [docs](../README.md) → reference → metrics
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [config-and-flags.md](./config-and-flags.md) · [file-formats.md](./file-formats.md) · [api.md](./api.md)

**TL;DR** Switchframe exports Prometheus metrics from the admin server at `GET /metrics`. The admin server listens separately from the QUIC/API server (default TCP `:9090`, set with `--admin-addr`), protected by a bearer token when `--admin-token` is configured. Metric definitions live in a single file, [`server/metrics/metrics.go`](../../server/metrics/metrics.go), and are registered on a non-default Prometheus registry so they never collide with other libraries. Every metric is namespaced with the prefix `switchframe_`.

## The story

Switchframe's metrics surface is deliberately narrow. Not every counter is worth exporting; scrape overhead is cheap but dashboard noise is not. The set exported from [`metrics.go`](../../server/metrics/metrics.go) is the set that has earned its way in — operations that have caused incidents, performance paths with measurable customer impact, and subsystem health signals that a monitoring system needs to page on.

Three broad categories exist. First, **event counters** that tick forward as the server does work: cuts performed, frames mixed, SRT reconnects. Second, **histograms** that characterize latency or size distributions: pipeline decode duration, IDR gate duration, snapshot apply size. Third, **gauges** for values that move up and down: command-queue depth.

Labels are used sparingly. `transitions_completed_total` is labeled by type (mix, dip, ftb) because the operator cares about which transition style is in use; `source_status_changes_total` is labeled by source key and the from/to status so alerting can distinguish a flap from a real outage. `http_requests_total` carries method, pattern, and status — standard trio for request dashboards. Over-labeling explodes cardinality, so everything else is a plain counter or histogram.

The scrape endpoint sits on a separate port from the QUIC server for three reasons. Prometheus scrapers expect TCP HTTP; the production control surface speaks QUIC. The admin endpoint also carries Go pprof (`/debug/pprof/*`) and the health (`/health`) and readiness (`/ready`) probes — everything operations needs, segregated from operator/API traffic. And binding it to `127.0.0.1:9090` by default means it is not exposed to the internet unless the operator deliberately reconfigures it.

All metrics are registered on the registry returned by [`metrics.GetRegistry()`](../../server/metrics/metrics.go). This registry is separate from the Prometheus default registry so Switchframe's metrics never collide with, e.g., metrics added by imported libraries that call `prometheus.MustRegister` on the default. The registry also includes Go's runtime collector (GC stats, goroutine count, memory) and the process collector (CPU, memory, FD count) — standard Prometheus practice for a Go binary.

## Scrape endpoint

| Aspect | Value |
|---|---|
| Path | `GET /metrics` |
| Default listen | `127.0.0.1:9090` (set with `--admin-addr`) |
| Authentication | Bearer token when `--admin-token` is set; no auth otherwise |
| Format | Prometheus text + OpenMetrics (negotiated via `Accept` header) |
| Handler | [`metrics.Handler()`](../../server/metrics/metrics.go) — `promhttp.HandlerFor` with `EnableOpenMetrics: true` |
| Registered via | [`admin.StartAdminServer`](../../server/cmd/switchframe/admin.go) |

The admin server also mounts `/debug/*` (Go pprof) under the same bearer-token guard, `/health` and `/ready` (unauthenticated — liveness/readiness probes must always work), and `/api/cert-hash` (unauthenticated — browsers need this to bootstrap WebTransport connections).

To enable mutex and block profiling on the pprof endpoint, set `SWITCHFRAME_PROFILING=1` in the environment — this calls `runtime.SetMutexProfileFraction(5)` and `runtime.SetBlockProfileRate(1000)` at init.

## HTTP metrics

Registered at package init in [`metrics.go`](../../server/metrics/metrics.go).

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_http_requests_total` | counter | `method`, `pattern`, `status` | Total HTTP requests seen by the API middleware chain ([`control.MetricsMiddleware`](../../server/control/middleware.go)). |
| `switchframe_http_request_duration_seconds` | histogram | `method`, `pattern` | Request latency. Buckets: 0.5 ms, 1 ms, 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms. |

## Switcher metrics

Registered per-instance via `NewMetrics` and attached to the switcher with `Switcher.SetMetrics`.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_cuts_total` | counter | — | Every hard cut. Incremented in `Switcher.Cut`. |
| `switchframe_transitions_completed_total` | counter | `type` (mix, dip, ftb, wipe, stinger, dve-*) | Every completed transition, labeled by the transition type string the engine used. |
| `switchframe_idr_gate_events_total` | counter | — | Every time the output manager gates frames waiting for the first IDR keyframe after a cut or pipeline rebuild. |
| `switchframe_idr_gate_duration_seconds` | histogram | — | Duration the IDR gate was closed. Buckets: 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s. |

## Mixer metrics

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_mixer_frames_mixed_total` | counter | — | Every audio frame emitted from `tick()` (decoded, mixed, re-encoded). |
| `switchframe_mixer_encode_errors_total` | counter | — | Every FDK-AAC encode failure in the mixer. |
| `switchframe_mixer_passthrough_bypass_total` | counter | — | Every frame where passthrough bypassed mixing (single source at 0 dB, no EQ/compressor active). |

## Pipeline metrics

Recorded by the video processing pipeline on every frame.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_pipeline_decode_errors_total` | counter | — | FFmpeg decoder returned an error in the pipeline loop; frame falls back to passthrough. |
| `switchframe_pipeline_encode_errors_total` | counter | — | FFmpeg encoder returned an error; frame is dropped. |
| `switchframe_pipeline_frames_processed_total` | counter | — | Frames successfully run through the YUV processing chain. |
| `switchframe_pipeline_decode_duration_seconds` | histogram | — | Time spent in the decode node. Buckets: 1 ms, 5 ms, 10 ms, 20 ms, 33 ms, 50 ms, 100 ms. |
| `switchframe_pipeline_encode_duration_seconds` | histogram | — | Time spent in the encode node. Same buckets as decode. |
| `switchframe_pipeline_blend_duration_seconds` | histogram | — | Time spent in the transition blend. Buckets: 0.1 ms, 0.5 ms, 1 ms, 2 ms, 5 ms, 10 ms. |
| `switchframe_pipeline_node_duration_seconds` | histogram | `node` (name from `PipelineNode.Name()`) | Per-node processing duration. Buckets: 10 µs, 100 µs, 1 ms, 10 ms, 100 ms. |

## Output metrics

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_output_ringbuf_overflows_total` | counter | — | Every time an SRT output adapter's ring buffer overflows (downstream too slow; sender drops oldest packets). |
| `switchframe_output_srt_reconnects_total` | counter | — | Every SRT output reconnect attempt. |
| `switchframe_output_recording_bytes_total` | counter | — | Bytes written to recording files. |
| `switchframe_output_srt_bytes_total` | counter | — | Bytes sent over all SRT output connections. |

## CBR pacer metrics

Populated by [`output.CBRPacer`](../../server/output/cbr_pacer.go) when constant-bitrate output is enabled (null-packet padding to a target muxrate).

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_cbr_null_packets_total` | counter | — | Null TS packets inserted by the pacer. |
| `switchframe_cbr_real_bytes_total` | counter | — | Non-null (real payload) bytes sent through the pacer. |
| `switchframe_cbr_pad_bytes_total` | counter | — | Null-padding bytes sent through the pacer. |
| `switchframe_cbr_burst_ticks_total` | counter | — | Tick cycles where real data exceeded the CBR budget (pacer fell behind). |

## Health metrics

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_source_status_changes_total` | counter | `source`, `from_status`, `to_status` | Every source health transition (ok ↔ stale ↔ no_signal ↔ offline). Driven by [`healthMonitor`](../../server/switcher/health.go). |

## Command queue metrics (active-active)

Populated by [`cmdqueue.Queue`](../../server/cmdqueue/queue.go), the PTP-timed command queue that orders operator commands across dual engines.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_cmdqueue_depth` | gauge | — | Current number of commands pending execution. Updated on enqueue/drain. |
| `switchframe_cmdqueue_drained_total` | counter | — | Commands drained (executed) from the queue. |
| `switchframe_cmdqueue_late_total` | counter | — | Commands promoted to immediate execution because their scheduled PTP time already passed (late, not stale). |
| `switchframe_cmdqueue_stale_total` | counter | — | Commands rejected as too old to execute (threshold in queue config). |
| `switchframe_cmdqueue_duplicate_total` | counter | — | Commands rejected because a previous enqueue with the same `command_seq` already exists (idempotency). |
| `switchframe_cmdqueue_clock_jumps_total` | counter | — | Backward PTP clock jumps detected during drain. |

## Snapshot sync metrics (active-active)

Populated when the standby engine applies a state snapshot from the leader.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_snapshot_apply_total` | counter | `result` (success, partial, error, stale) | Each snapshot apply attempt, labeled by outcome. |
| `switchframe_snapshot_apply_duration_seconds` | histogram | — | Duration of apply. Buckets: 1 ms, 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s. |
| `switchframe_snapshot_size_bytes` | histogram | — | Size of applied snapshots. Buckets: 1 KiB, 4 KiB, 16 KiB, 64 KiB, 256 KiB, 1 MiB. |

## Go runtime and process metrics

Registered at package init by [`metrics.go`](../../server/metrics/metrics.go) via `collectors.NewGoCollector()` and `collectors.NewProcessCollector()`.

| Metric family | Source | Description |
|---|---|---|
| `go_*` | `collectors.NewGoCollector` | Goroutine count, GC stats, heap allocations, stack usage. |
| `process_*` | `collectors.NewProcessCollector` | Resident memory, virtual memory, CPU seconds, open file descriptors, start time. |

These come from Prometheus's standard collectors. They are always present and need no Switchframe-specific configuration.

## Key types and entry points

| Symbol | File | Role |
|---|---|---|
| [`Metrics`](../../server/metrics/metrics.go) | metrics/metrics.go | Struct holding every per-subsystem metric |
| [`NewMetrics`](../../server/metrics/metrics.go) | metrics/metrics.go | Construct + register on a `prometheus.Registerer` |
| [`GetRegistry`](../../server/metrics/metrics.go) | metrics/metrics.go | Returns the non-default `*prometheus.Registry` |
| [`Handler`](../../server/metrics/metrics.go) | metrics/metrics.go | `http.Handler` with `promhttp.HandlerOpts{EnableOpenMetrics: true}` |
| [`HTTPRequestsTotal` / `HTTPRequestDuration`](../../server/metrics/metrics.go) | metrics/metrics.go | Package-level HTTP counters (registered at init) |
| [`SetMetrics`](../../server/switcher/switcher.go) (and similarly on Mixer, Manager) | subsystems | Attach `*Metrics` to a subsystem under its mutex |

## Gotchas and invariants

- **Metrics are opt-in per subsystem.** A subsystem's counters only increment once `SetMetrics` has been called with a non-nil `*Metrics`. Before that, metric accessors are guarded by a nil check.
- **The registry is a singleton.** `metrics.GetRegistry()` returns the same `*prometheus.Registry` on every call. `NewMetrics` registers all subsystem metrics on the registry it receives; the app's `initInfra` calls `NewMetrics(metrics.GetRegistry())` exactly once.
- **Label cardinality is intentionally limited.** `source_status_changes_total` carries a `source` label — keep source key counts reasonable. Per-output destination labels would blow up cardinality and are deliberately absent.
- **The `/metrics` endpoint requires the admin bearer token when configured.** Scrape jobs should use the same token as any other admin endpoint. `/health` and `/ready` are always open.
- **Do not add new metrics to the default Prometheus registry.** Use `GetRegistry()` and register via `NewMetrics` or its callers. Default-registry registration would not be exported by our handler.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md), [locking-and-concurrency.md](../concepts/locking-and-concurrency.md), [gpu.md](../concepts/gpu.md)
- **Reference:** [config-and-flags.md](./config-and-flags.md), [file-formats.md](./file-formats.md), [api.md](./api.md), [state-broadcast.md](./state-broadcast.md)
- **Subsystems:** [switcher.md](../subsystems/switcher.md), [audio.md](../subsystems/audio.md), [output.md](../subsystems/output.md), [control-plane.md](../subsystems/control-plane.md)
- **Operations:** [deployment.md](../operations/deployment.md)
- **Root:** [architecture.md](../architecture.md), [README.md](../README.md)
