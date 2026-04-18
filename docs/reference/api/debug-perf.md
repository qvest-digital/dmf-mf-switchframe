# API: debug and performance

> **Where you are:** [docs](../../../README.md) → reference → api → debug-perf
> **Read this first:** [api.md](../api.md)
> **See also:** [metrics.md](../metrics.md) · [subsystems/control-plane.md](../../subsystems/control-plane.md)

**TL;DR** Five endpoints cover operational introspection: a full debug snapshot (diagnostic dump), live performance metrics with optional baseline comparison, baseline save and delete, and an internal connection-counter endpoint called by `sf-agent` on loopback for idle-session detection.

## Endpoints

### `GET /api/debug/snapshot`

**Purpose:** return a full diagnostic snapshot (all subsystems). Only registered when a debug collector is attached. The response is heavy (all state + ring buffers); intended for support tickets.
**Handler:** `debug.Collector.HandleSnapshot`, wired by [`control/api.go`](../../../server/control/api.go) → `(*API).registerDebugRoutes`.

**Response 200:** JSON with per-subsystem state and event log.

---

### `GET /api/perf`

**Purpose:** return live performance stats (pipeline ns/frame, E2E latency, mix-cycle duration) over a 60 s ring buffer. Optional `?baseline=<name>` compares the current window against a saved baseline.
**Handler:** `perf.Sampler.HandlePerf`.

**Response 200:** `perf.WindowStats` with optional `delta` when `baseline=` is supplied.

---

### `POST /api/perf/baseline`

**Purpose:** save the current perf sample window as a named baseline for regression tracking.
**Handler:** `perf.Sampler.HandleSaveBaseline`.

**Request body:** `{ "name": "1080p60-baseline" }`.

---

### `DELETE /api/perf/baseline/{name}`

**Handler:** `perf.Sampler.HandleDeleteBaseline`.

---

### `GET /api/internal/connections`

**Purpose:** return active connection counts by transport. Called by `sf-agent` on loopback (the agent reads process-level state, not tenant-level). Suitable for idle-session detection in a SaaS deployment.
**Handler:** [`control/api.go`](../../../server/control/api.go) → `(*API).handleInternalConnections`.

**Response 200:**

```json
{ "webtransport": 3, "srt": 2, "moq": 1, "total": 6 }
```

Returns zeroed stats when no connection counter is registered.

## Related docs

- **Reference:** [api.md](../api.md) · [metrics.md](../metrics.md)
- **Subsystems:** [control-plane.md](../../subsystems/control-plane.md)
- **Operations:** [deployment.md](../../deployment.md)
