# Metrics

> **Where you are:** [docs](../README.md) → reference → metrics
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [config-and-flags.md](./config-and-flags.md) · [file-formats.md](./file-formats.md) · [api.md](./api.md)

**TL;DR** Switchframe exports Prometheus metrics from the admin server at `GET /metrics`. The admin server listens separately from the QUIC/API server (default TCP `:9090`, set with `--admin-addr`), protected by a bearer token when `--admin-token` is configured. Event metrics are defined in [`server/metrics/metrics.go`](../../server/metrics/metrics.go), and the families read at scrape time in a collector per subsystem ([below](#scrape-time-collectors)); all are registered on a non-default Prometheus registry so they never collide with other libraries. Every metric is namespaced with the prefix `switchframe_`.

## The story

Switchframe's metrics surface is deliberately narrow. Not every counter is worth exporting; scrape overhead is cheap but dashboard noise is not. The set exported from [`metrics.go`](../../server/metrics/metrics.go) is the set that has earned its way in — operations that have caused incidents, performance paths with measurable customer impact, and subsystem health signals that a monitoring system needs to page on.

Three broad categories exist. First, **event counters** that tick forward as the server does work: cuts performed, frames mixed, SRT reconnects. Second, **histograms** that characterize latency or size distributions: pipeline encode duration, IDR gate duration, snapshot apply size. Third, **gauges** for values that move up and down: command-queue depth, requests in flight.

Labels are used sparingly. `transitions_completed_total` is labeled by type (mix, dip, ftb) because the operator cares about which transition style is in use; `source_status_changes_total` is labeled by source and the from/to status so alerting can distinguish a flap from a real outage. `http_requests_total` carries method, pattern, status and caller: the standard trio for request dashboards, plus which credential a request came in on. Over-labeling explodes cardinality, so everything else is a plain counter or histogram. The scrape-time collectors label a series by what it describes (a source, output, playout channel, clip player or SRT connection), a bounded set whose series go when it does.

Every label value comes from a bounded set. An HTTP method outside the standard ones is `other`. A request that matches no route, rejected or not, has the pattern `unknown`. A source label is the source key, except for an SRT input: its key is derived from the publisher's stream id, which can carry a stream key, so its label is `srt:` and a 12-character digest of the key ([`metrics.SourceLabel`](../../server/metrics/labels.go)).

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
| `switchframe_http_requests_total` | counter | `method`, `pattern`, `status`, `caller` | Total HTTP requests seen by the API middleware chain ([`control.RouteMetricsMiddleware`](../../server/control/middleware.go)). The pattern is resolved against the API mux before authentication runs, so a 401 or 403 is counted under its route. `caller` names the credential: `token` (the session API token), `operator` (an operator's token), `ticket` (a download ticket), `invalid` (a bearer token that matches neither), `none` (no bearer token), `exempt` (a route that takes none), `unchecked` (no authentication ran, as in demo mode). |
| `switchframe_http_request_duration_seconds` | histogram | `method`, `pattern` | Request latency. Buckets: 0.5 ms, 1 ms, 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s, 2.5 s, 5 s, 10 s, 30 s. No `caller` label: a ratio against `http_requests_total` sums `caller` away. |
| `switchframe_http_requests_in_flight` | gauge | — | HTTP requests currently being served. |

## Switcher metrics

Registered per-instance via `NewMetrics` and attached to the switcher with `Switcher.SetMetrics`.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_cuts_total` | counter | — | Every hard cut. Incremented in `Switcher.Cut`. |
| `switchframe_transitions_completed_total` | counter | `type` (mix, dip, ftb, wipe, stinger, dve-*) | Every completed transition, labeled by the transition type string the engine used. |
| `switchframe_idr_gate_events_total` | counter | — | Every time an SRT caller output, after a reconnect, held its output until a keyframe (or the 2 s gate timeout). |
| `switchframe_idr_gate_duration_seconds` | histogram | — | How long that output was held. Buckets: 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s. |
| `switchframe_program_frame_interval_seconds` | histogram | - | Time between consecutive program frames sent. At a steady program it sits at one frame period; the buckets above two periods count stalls. Buckets: 12.5 ms, 17 ms, 21 ms, 25 ms, 34 ms, 42 ms, 50 ms, 67 ms, 84 ms, 100 ms, 200 ms, 500 ms, 1 s, 2 s. |

## Mixer metrics

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_mixer_frames_mixed_total` | counter | — | Every audio frame emitted from `tick()` (decoded, mixed, re-encoded). |
| `switchframe_mixer_encode_errors_total` | counter | — | Every FDK-AAC encode failure in the mixer. |

## Pipeline metrics

Recorded by the video processing pipeline on every frame.

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_pipeline_encode_errors_total` | counter | — | FFmpeg encoder returned an error; frame is dropped. |
| `switchframe_pipeline_frames_processed_total` | counter | — | Frames successfully run through the YUV processing chain. |
| `switchframe_pipeline_encode_duration_seconds` | histogram | — | Time spent in the encode node. Buckets: 1 ms, 5 ms, 10 ms, 20 ms, 33 ms, 50 ms, 100 ms. |
| `switchframe_pipeline_node_duration_seconds` | histogram | `node` (name from `PipelineNode.Name()`) | Per-node processing duration. Buckets: 10 µs, 100 µs, 1 ms, 10 ms, 100 ms. |

## Output metrics

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_output_ringbuf_overflows_total` | counter | — | Every time an SRT output adapter's ring buffer overflows (downstream too slow; sender drops oldest packets). |
| `switchframe_output_srt_reconnects_total` | counter | — | Every SRT output reconnect attempt. |
| `switchframe_output_recording_bytes_total` | counter | — | Bytes written to recording files. |
| `switchframe_output_srt_bytes_total` | counter | — | Bytes sent over all SRT output connections, one count per client of a listener. |

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
| `switchframe_source_status_changes_total` | counter | `source`, `from_status`, `to_status` | Every source health transition (healthy ↔ stale ↔ no_signal ↔ offline), after hysteresis; a source's first status is not one. Driven by [`healthMonitor`](../../server/switcher/health.go). A removed source's series are deleted with it. |

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

## Clock sync and leadership metrics

Registered with `NewMetrics` in [`metrics.go`](../../server/metrics/metrics.go).

| Metric | Type | Labels | Fires on |
|---|---|---|---|
| `switchframe_clock_sync_synchronized` | gauge | - | 1 while the local clock is synchronized according to the active detector, 0 otherwise. |
| `switchframe_clock_sync_source` | gauge | `source` (systemclock-initializing, systemclock-monitored, ptp) | 1 on the active clock-sync detection source, 0 on the others. |
| `switchframe_clock_sync_window_drift_ns` | gauge | - | Signed wall-clock against monotonic drift accumulated over the sampler window, in nanoseconds; negative when the wall clock ran slower. An integral over the window, not an instantaneous offset. |
| `switchframe_force_leader_total` | counter | `operator`, `reason_class` (partition, recovery, maintenance, test, drill, unspecified, other) | Each operator-triggered force-leader epoch bump. `operator` is the attributed caller, or `control-plane` for a direct call; free-text reasons go to the log, not to a label. |

## Scrape-time collectors

The families below are read from each subsystem's own state when `/metrics` is scraped, by a `prometheus.Collector` per subsystem, registered on the same registry ([`registerCollectors`](../../server/cmd/switchframe/admin.go)) only for the subsystems that run. Nothing is counted on their behalf between scrapes, and a series exists only while what it describes does. Counters among them are the subsystem's own and restart with it.

### Program and source metrics

Read by [`switcher.MetricsCollector`](../../server/switcher/metrics_collector.go), from the switcher's state and frame counters. Every `source` label goes through `SourceLabel`; a source's series exist only while the source does.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_program_source_info` | gauge | `source` | 1 for the source on program. |
| `switchframe_preview_source_info` | gauge | `source` | 1 for the source on preview. |
| `switchframe_transition_active` | gauge | - | 1 while a transition runs. |
| `switchframe_ftb_active` | gauge | - | 1 while the program is faded to black. |
| `switchframe_program_frames_total` | counter | - | Program frames sent to the outputs. |
| `switchframe_program_repeated_frames_total` | counter | - | Program frames re-sent because no new frame was ready in its slot. |
| `switchframe_program_dropped_frames_total` | counter | `stage` | Program frames dropped, by stage: proc (the processing queue was full), encode (the encoder was behind), stale (from a superseded program). |
| `switchframe_pipeline_deadline_violations_total` | counter | - | Program frames whose processing took longer than the frame budget. |
| `switchframe_pipeline_frame_budget_seconds` | gauge | - | Time the pipeline has per frame at the program rate. |
| `switchframe_pipeline_queue_depth` | gauge | - | Frames waiting for the processing pipeline. |
| `switchframe_pipeline_stage_latency_seconds` | gauge | `stage` | Latency of the most recent program frame in each stage, and end to end (stage total). |
| `switchframe_gpu_fallback_frames_total` | counter | - | Frames the GPU pipeline handed back to the CPU pipeline. |
| `switchframe_program_last_frame_age_seconds` | gauge | - | Time since the last program frame was sent. |
| `switchframe_source_info` | gauge | `source`, `type`, `label` | 1 per source, with its type and display label. |
| `switchframe_source_health` | gauge | `source`, `status` | 1 for the source's health status, 0 for the others. |
| `switchframe_source_last_frame_age_seconds` | gauge | `source` | Time since the source last delivered a video frame. |
| `switchframe_source_raw_frames_total` | counter | `source` | Uncompressed video frames the source delivered, such as an MXL input's. A compressed source counts none here; its rate is switchframe_source_decode_fps. |
| `switchframe_source_decode_fps` | gauge | `source` | Frames per second the decoder of a compressed source produces, averaged by the decoder. |
| `switchframe_source_decode_drops_total` | counter | `source` | Frames the source's decoder dropped. |
| `switchframe_source_decode_latency_seconds` | gauge | `source` | Decode time of the source's most recent frame. |
| `switchframe_source_clock_drift_ppm` | gauge | `source` | Estimated drift of the source's clock against the pipeline clock; positive is faster. |

### Audio mix metrics

Read by [`audio.MetricsCollector`](../../server/audio/metrics_collector.go), from the mixer's metering and performance counters.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_audio_loudness_lufs` | gauge | `window` | Program loudness per EBU R128, by window: momentary (400 ms), short_term (3 s) and integrated. Absent below -70 LUFS, the BS.1770 absolute gate, as in silence. |
| `switchframe_audio_program_peak_dbfs` | gauge | `channel` | Program peak level of the most recent frame, per channel; -96 is silence. |
| `switchframe_audio_source_peak_dbfs` | gauge | `source`, `channel` | Peak level of a source's most recent frame, per channel; -96 is silence. |
| `switchframe_audio_source_level_db` | gauge | `source` | A source's fader level. |
| `switchframe_audio_source_muted` | gauge | `source` | 1 while a source is muted. |
| `switchframe_audio_mix_cycle_seconds` | gauge | - | Time the most recent mix cycle took. |
| `switchframe_audio_max_frame_gap_seconds` | gauge | - | Longest gap between program audio frames since the mixer started. |
| `switchframe_audio_decode_errors_total` | counter | - | Source audio frames the mixer failed to decode. |

### Output and recording metrics

Read by [`output.MetricsCollector`](../../server/output/metrics_collector.go), one set per destination. `output` is the destination's generated ID, or `srt` for the legacy single SRT output; no address, URL or stream key appears in a label.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_output_info` | gauge | `output`, `kind`, `name` | 1 per output, with its kind and display name. The legacy single SRT output is output "srt". No address, URL or stream key is exported. |
| `switchframe_output_state` | gauge | `output`, `state` | 1 for the output's state, 0 for the others. |
| `switchframe_output_bytes_total` | counter | `output` | Bytes the output sent since it last started. |
| `switchframe_output_dropped_packets_total` | counter | `output` | TS packets the output dropped because it could not keep up. |
| `switchframe_output_overflows_total` | counter | `output` | Times an SRT caller's reconnect buffer overflowed, losing what it held. |
| `switchframe_output_media_seconds_total` | counter | `output` | Media time an output of timed frames (RTMP) has sent: one program frame interval per video frame written. Its rate is 1 while every program frame reaches the destination, and falls below 1 by the share of frames dropped and while the connection is down. |
| `switchframe_output_timeline_rebases_total` | counter | `output` | Program timeline restarts an output of timed frames (RTMP) carried over: its connection timeline continued one frame interval after the last frame instead of stepping back. |
| `switchframe_output_dropped_frames_total` | counter | `output`, `kind` | Frames an output of timed frames (RTMP) dropped because the destination did not take them in time, by kind (video, audio). |
| `switchframe_output_clients` | gauge | `output` | Clients connected to an SRT listener output. |
| `switchframe_recording_active` | gauge | - | 1 while recording. |
| `switchframe_recording_failed` | gauge | - | 1 while the current or last recording is in error. |
| `switchframe_recording_current_bytes` | gauge | - | Bytes the current recording has written. |
| `switchframe_recording_current_duration_seconds` | gauge | - | How long the current recording has run. |
| `switchframe_recording_dropped_packets_total` | counter | - | TS packets the current recording dropped because the disk could not keep up. |

### SRT connection metrics

Read by [`srt.MetricsCollector`](../../server/srt/metrics_collector.go), from libsrt's statistics per connection. `connection` names what the connection belongs to; an SRT input's goes through `SourceLabel`.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_srt_connected` | gauge | `connection`, `direction` | 1 while the SRT connection is up. connection is the input's source label, the same as in switchframe_source_info, or output:<output id> for an output. |
| `switchframe_srt_rtt_seconds` | gauge | `connection`, `direction` | Smoothed round-trip time. |
| `switchframe_srt_reconnects_total` | counter | `connection`, `direction` | Times the connection was lost. |
| `switchframe_srt_latency_seconds` | gauge | `connection`, `direction` | Latency negotiated with the peer. |
| `switchframe_srt_received_packets_total` | counter | `connection` | Packets received. |
| `switchframe_srt_received_lost_packets_total` | counter | `connection` | Packets the receiver detected as lost. |
| `switchframe_srt_received_dropped_packets_total` | counter | `connection` | Packets dropped for arriving too late to play: what the input actually lost. |
| `switchframe_srt_received_retransmitted_packets_total` | counter | `connection` | Retransmitted packets received. |
| `switchframe_srt_receive_buffer_seconds` | gauge | `connection` | Media held in the receive buffer. |
| `switchframe_srt_receive_rate_bytes` | gauge | `connection` | Receive rate, bytes per second, as SRT measured it over its last window. |
| `switchframe_srt_sent_packets_total` | counter | `connection` | Packets sent. |
| `switchframe_srt_sent_lost_packets_total` | counter | `connection` | Sent packets the receiver reported lost. |
| `switchframe_srt_sent_dropped_packets_total` | counter | `connection` | Packets the sender dropped as too late to send: what the receiver never got. |
| `switchframe_srt_send_buffer_seconds` | gauge | `connection` | Media waiting in the send buffer. |
| `switchframe_srt_send_rate_bytes` | gauge | `connection` | Send rate, bytes per second, as SRT measured it over its last window. |

### Playout metrics

Read by [`playout.MetricsCollector`](../../server/playout/metrics_collector.go), per playout channel, and for the asset cache when one is configured.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_playout_channel_state` | gauge | `channel`, `state` | 1 for the playout channel's state, 0 for the others. |
| `switchframe_playout_item_index` | gauge | `channel` | Position of the item on air in its playlist, from 0. Absent while the playlist is empty. |
| `switchframe_playout_items` | gauge | `channel` | Items in the channel's playlist. |
| `switchframe_playout_item_remaining_seconds` | gauge | `channel` | Time left of the item on air. |
| `switchframe_playout_pods_armed` | gauge | `channel` | Ad pods armed on the channel. |
| `switchframe_playout_pods_completed` | gauge | `channel` | Ad pods on the channel's board that completed: rejoined or dumped out. Deleting a pod from the board lowers it. |
| `switchframe_playout_channel_stalled` | gauge | `channel` | 1 while the watchdog finds a running channel delivering no frames. |
| `switchframe_playout_last_frame_age_seconds` | gauge | `channel` | Time since a watched channel delivered its last frame. |
| `switchframe_playout_ticks_made_up_total` | counter | `channel` | Extra ticks the channel's run loop ran, at most one per frame period, to make up frame periods it fell behind on. |
| `switchframe_playout_ticks_skipped_total` | counter | `channel` | Ticks the channel's run loop dropped when it fell more than a second behind: frames the output lost. |
| `switchframe_playout_cache_used_bytes` | gauge | - | Bytes the playout asset cache holds. |
| `switchframe_playout_cache_max_bytes` | gauge | - | Bytes the playout asset cache may hold. |
| `switchframe_playout_cache_items` | gauge | `state` | Assets in the playout cache, by state: ready, pending (queued for a fetch), fetching (or transcoding) and error. |

### Clip player metrics

Read by [`clip.MetricsCollector`](../../server/clip/metrics_collector.go), per clip player. Clip names are not exported.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_clip_player_state` | gauge | `player`, `state` | 1 for the clip player's state, 0 for the others. |
| `switchframe_clip_player_position_ratio` | gauge | `player` | How far a loaded clip has played, 0 to 1. |
| `switchframe_clip_player_speed` | gauge | `player` | Playback speed of a playing clip; 1 is real time. |

### Replay metrics

Read by [`replay.MetricsCollector`](../../server/replay/metrics_collector.go), per replay buffer, labelled through `SourceLabel`.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_replay_state` | gauge | `state` | 1 for the replay player's state, 0 for the others. |
| `switchframe_replay_buffer_seconds` | gauge | `source` | Media a source's replay buffer holds. |
| `switchframe_replay_buffer_bytes` | gauge | `source` | Bytes a source's replay buffer holds. |

### Graphics metrics

Read by [`graphics.MetricsCollector`](../../server/graphics/metrics_collector.go). Layer names and templates are not exported.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_graphics_layer_on_air` | gauge | `layer` | 1 while the graphics layer is on program. Layer names and templates are left out: the client names both freely. |
| `switchframe_graphics_layers_on_air` | gauge | - | Graphics layers on program. |

### HTML5 graphics metrics

Read by [`htmlgfx.MetricsCollector`](../../server/htmlgfx/metrics_collector.go), registered while the engine runs an HTML5 graphics manager.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_htmlgfx_frames_delivered_total` | counter | - | Frames the sidecar painted that reached the compositor on a graphics layer. Frames for a pool slot with no layer are not counted. |

### Redundancy and NMOS metrics

Read by [`redundancyCollector`](../../server/cmd/switchframe/metrics_redundancy.go), from the peer poller of a redundant pair and the NMOS node.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_peer_role` | gauge | `role` | 1 for this engine's role in its pair, 0 for the others: leader, follower, follower_partitioned (the peer is unreachable and holds authority), or standalone without a peer. |
| `switchframe_peer_reachable` | gauge | - | 1 while the peer engine answers its health polls. |
| `switchframe_peer_consecutive_failures` | gauge | - | Health polls of the peer that failed in a row. |
| `switchframe_peer_last_seen_age_seconds` | gauge | - | Time since the peer last answered a health poll. |
| `switchframe_peer_epoch` | gauge | `side` | Leadership epoch, of this engine (side local) and as the peer last reported it (side peer). A forced leader change raises it. |
| `switchframe_peer_program_agrees` | gauge | - | 1 while the peer last reported the same program source as this engine, 0 when the two disagree. Absent without a reachable peer. |
| `switchframe_nmos_registered` | gauge | - | 1 while this engine holds the node's registration with the NMOS registry. |
| `switchframe_nmos_registrations_total` | counter | `result` | Registrations of the node with the NMOS registry, by result. |
| `switchframe_nmos_heartbeats_total` | counter | `result` | Heartbeats to the NMOS registry, by result. |
| `switchframe_nmos_last_heartbeat_age_seconds` | gauge | - | Time since the registry last accepted a registration or heartbeat from this engine. |
| `switchframe_nmos_activations_total` | counter | `result` | IS-05 activations of an MXL receiver, by result. |

### MXL input metrics

Read by [`mxlReaderCollector`](../../server/cmd/switchframe/metrics_mxl.go), from the readers of each MXL input, labelled by the input's source key through `SourceLabel`.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `switchframe_mxl_reader_skipped_grains_total` | counter | `source` | Video grains an MXL input skipped because its consumer was behind. |
| `switchframe_mxl_reader_resyncs_total` | counter | `source`, `essence` | Times an MXL input jumped to the head after the ring overwrote its read position, losing what lay between, by essence. |
| `switchframe_mxl_reader_lag_grains` | gauge | `source` | Grains an MXL input's video read position trails the head of its flow. |
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
| [`HTTPRequestsTotal` / `HTTPRequestDuration` / `HTTPRequestsInFlight`](../../server/metrics/metrics.go) | metrics/metrics.go | Package-level HTTP metrics (registered at init) |
| [`SourceLabel` / `MethodLabel`](../../server/metrics/labels.go) | metrics/labels.go | The label value a source key or an HTTP method takes |
| [`SetMetrics`](../../server/switcher/switcher.go) (and similarly on Mixer, Manager) | subsystems | Attach `*Metrics` to a subsystem under its mutex |

## Gotchas and invariants

- **Metrics are opt-in per subsystem.** A subsystem's counters only increment once `SetMetrics` has been called with a non-nil `*Metrics`. Before that, metric accessors are guarded by a nil check.
- **The registry is a singleton.** `metrics.GetRegistry()` returns the same `*prometheus.Registry` on every call. `NewMetrics` registers all subsystem metrics on the registry it receives; the app's `initInfra` calls `NewMetrics(metrics.GetRegistry())` exactly once.
- **Label cardinality is intentionally limited.** `source_status_changes_total` carries a `source` label, through `SourceLabel`, and its series are deleted when the source is removed. The scrape-time families label by `output`, `channel`, `player` or `connection` from what exists at scrape time, so a removed one leaves no series behind.
- **The `/metrics` endpoint requires the admin bearer token when configured.** Scrape jobs should use the same token as any other admin endpoint. `/health` and `/ready` are always open.
- **Do not add new metrics to the default Prometheus registry.** Use `GetRegistry()` and register via `NewMetrics` or its callers. Default-registry registration would not be exported by our handler.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md), [locking-and-concurrency.md](../concepts/locking-and-concurrency.md), [gpu.md](../concepts/gpu.md)
- **Reference:** [config-and-flags.md](./config-and-flags.md), [file-formats.md](./file-formats.md), [api.md](./api.md), [state-broadcast.md](./state-broadcast.md)
- **Subsystems:** [switcher.md](../subsystems/switcher.md), [audio.md](../subsystems/audio.md), [output.md](../subsystems/output.md), [control-plane.md](../subsystems/control-plane.md)
- **Operations:** [deployment.md](../operations/deployment.md)
- **Root:** [architecture.md](../architecture.md), [README.md](../README.md)
