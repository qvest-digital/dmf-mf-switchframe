# API: source

> **Where you are:** [docs](../../../README.md) → reference → api → source
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/switcher.md](../../subsystems/switcher.md) · [subsystems/srt-ingest.md](../../subsystems/srt-ingest.md) · [api/keying.md](keying.md)

**TL;DR** Twelve endpoints manage the source roster: list, get, create (SRT pull only), delete (SRT only), and per-source mutations for label, input delay, multiview position, SRT latency, and per-source color corrector. SRT stats are exposed for dashboards.

## Endpoints

### `GET /api/sources`

**Purpose:** return the full map of registered sources.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSources`.

**Request body:** none.

**Response 200:** `map[string]SourceInfo` — keyed by source key. See [state-broadcast.md#sourceinfo](../state-broadcast.md#sourceinfo) for the shape.

**Errors:** none.

**Emits:** nothing.

---

### `GET /api/sources/{key}`

**Purpose:** return one source by key, including inline SRT stats when applicable.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleGetSource`.

**Response 200:** `SourceInfo`.

**Errors:**

| Code | When |
|---|---|
| 404 | source key not registered |

---

### `POST /api/sources`

**Purpose:** create a new SRT **pull** source. Only SRT caller mode is supported via this endpoint; listener-mode sources are configured via the SRT listener at startup, and demo/MXL sources are registered by the app layer.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleCreateSource`.

**Request body** (`control.createSourceRequest`):

```json
{
  "type": "srt",
  "mode": "caller",
  "address": "srt.example.com:9001",
  "streamID": "cam1",
  "label": "Camera 1",
  "latencyMs": 200,
  "inputBW": 0,
  "overheadBW": 0
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | string | yes | must be `srt` |
| `mode` | string | yes | must be `caller` |
| `address` | string | yes | host:port of remote SRT listener |
| `streamID` | string | yes | SRT streamID for stream selection |
| `label` | string | no | human label (max 64 chars) |
| `latencyMs` | int | no | SRT receiver latency budget (0-10000) |
| `inputBW` | int64 | no | expected input bitrate in bytes/sec (0 = auto) |
| `overheadBW` | int | no | overhead percentage (0 = default) |

**Response 201:**

```json
{ "key": "srt-cam1-ab12cd34" }
```

**Errors:**

| Code | When |
|---|---|
| 400 | invalid type/mode, missing address or streamID, invalid label |
| 501 | SRT manager not configured |

**Emits:** state broadcast with the new source appearing in `Sources`.

**Related:** [subsystems/srt-ingest.md](../../subsystems/srt-ingest.md)

---

### `POST /api/sources/{key}/label`

**Purpose:** update a source's human label.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSetLabelInner` (wrapped by `timedHandlerWithPath`).

**Request body** (`control.labelRequest`):

```json
{ "label": "Cam 1 - Stage Left" }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `label` | string | yes | max 64 chars (see `control.MaxLabelLen`) |

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | label too long |
| 404 | source not found |

**Emits:** state broadcast.

---

### `POST /api/sources/{key}/delay`

**Purpose:** set a per-source input frame delay (video and audio) in milliseconds, used to align cameras with different glass-to-glass latencies.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSetDelayInner` (wrapped by `timedHandlerWithPath`).

**Request body** (`control.delayRequest`):

```json
{ "delayMs": 120 }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `delayMs` | int | yes | 0-500 |

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | `switcher.ErrInvalidDelay` — out of range |
| 404 | source not found |

**Emits:** state broadcast with `Sources[key].DelayMs`.

**Related:** [subsystems/switcher.md#per-source-delay](../../subsystems/switcher.md#per-source-delay)

---

### `PUT /api/sources/{key}/position`

**Purpose:** change a source's multiview position (stable ordering index).
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSetPositionInner` (wrapped by `timedHandlerWithPath`).

**Request body** (`control.positionRequest`):

```json
{ "position": 3 }
```

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | `switcher.ErrInvalidPosition` |
| 404 | source not found |

**Emits:** state broadcast.

---

### `DELETE /api/sources/{key}`

**Purpose:** remove an SRT pull source and tear down its connection. Only works for sources created via `POST /api/sources`.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleDeleteSource`.

**Response 204:** empty.

**Errors:**

| Code | When |
|---|---|
| 405 | source is not an SRT pull source |
| 501 | SRT manager not configured |
| 500 | stop-pull error from SRT manager |

**Emits:** state broadcast.

---

### `GET /api/sources/{key}/srt/stats`

**Purpose:** return live SRT connection stats (RTT, loss, bitrate, flight size) for a source. Used by the SRT stats popover in the UI.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleGetSourceSRTStats`.

**Response 200:** `srt.ConnStatsSnapshot` — typed shape defined in `srt/stats.go`.

**Errors:**

| Code | When |
|---|---|
| 404 | source not an SRT source, or not found |
| 501 | SRT manager not configured |

**Related:** [subsystems/srt-ingest.md](../../subsystems/srt-ingest.md)

---

### `PUT /api/sources/{key}/srt`

**Purpose:** adjust SRT parameters (currently just latency) on a live connection without reconnecting.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleUpdateSourceSRT`.

**Request body** (`control.updateSourceSRTRequest`):

```json
{ "latencyMs": 300 }
```

**Response 200:** `{ "status": "ok" }`.

**Errors:**

| Code | When |
|---|---|
| 400 | latency out of 0-10000 range |
| 404 | source not an SRT source or not found |
| 501 | SRT manager not configured |

---

### `GET /api/sources/{key}/corrector`

**Purpose:** return the per-source color corrector state (lift/gamma/gain wheels + curves). Only registered when a `SourceCorrectorManager` is configured.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSourceCorrectorGet`.

**Response 200:** `colorgrade.CorrectorState` JSON (lift, gamma, gain triples plus curve points).

**Errors:** none — returns a zero-value state for unknown keys.

---

### `PUT /api/sources/{key}/corrector`

**Purpose:** apply a full corrector state to a source. Replaces any existing per-source corrector.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSourceCorrectorSet`.

**Request body:** `colorgrade.CorrectorState`.

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON or validation failure (`control.validateCorrectorState`) |

**Emits:** state broadcast with updated `Sources[key].Corrector`.

**Related:** [api/colorgrade.md](colorgrade.md) · [videoprocessing colorwheel](../../subsystems/control-plane.md)

---

### `DELETE /api/sources/{key}/corrector`

**Purpose:** clear the per-source corrector, reverting to the default pass-through state.
**Auth:** required.
**Handler:** [`control/api_source.go`](../../../server/control/api_source.go) → `(*API).handleSourceCorrectorReset`.

**Response 200:** full `ControlRoomState`.

**Errors:** none.

**Emits:** state broadcast.

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md) · [api/keying.md](keying.md) · [api/colorgrade.md](colorgrade.md)
- **Subsystems:** [switcher.md](../../subsystems/switcher.md) · [srt-ingest.md](../../subsystems/srt-ingest.md)
