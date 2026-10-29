# API: output

> **Where you are:** [docs](../../../README.md) → reference → api → output
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/output.md](../../subsystems/output.md) · [state-broadcast.md](../state-broadcast.md) · [deployment.md](../../operations/deployment.md)

**TL;DR** Sixteen endpoints cover recording, SRT output, multi-destination streaming (SRT caller, SRT listener, RTMP), the CBR pacer status, and the confidence thumbnail. Legacy single-stream SRT output (`/api/output/srt/*`) coexists with the newer multi-destination API (`/api/output/destinations/*`); new integrations should prefer destinations.

## Recording

### `POST /api/recording/start`

**Purpose:** start writing program output to rotating `.ts` files.
**Auth:** required.
**Handler:** [`control/api_output.go`](../../../server/control/api_output.go) → `(*API).handleRecordingStart`.

**Request body** (`control.recordingStartRequest`):

```json
{
  "outputDir": "/var/switchframe/recordings",
  "rotateAfterMins": 60,
  "maxFileSizeMB": 2048,
  "filenamePrefix": "show1"
}
```

`outputDir` defaults to `$TMPDIR/switchframe-recordings` if empty; must be an absolute path with no `..` traversal. Rotation defaults to 1 hour; max size has no default (unlimited).

**Response 200:** `internal.RecordingStatus`.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON, relative path, path traversal |
| 409 | `output.ErrRecorderActive`, peer-poller gate rejects (follower engine) |
| 501 | output manager not configured |

**Emits:** state broadcast with updated `Recording`. Sets the recording directory used by the clip-import endpoint.

**Related:** [subsystems/output.md#recording](../../subsystems/output.md) · [api/clips.md#clip-from-recording](clips.md)

---

### `POST /api/recording/stop`

**Purpose:** stop the active recording.
**Auth:** required.
**Handler:** `(*API).handleRecordingStop`.

**Response 200:** `internal.RecordingStatus`.

**Errors:** 409 (`output.ErrRecorderNotActive`), 501.

---

### `GET /api/recording/status`

**Purpose:** return the current recording status.
**Auth:** required.
**Handler:** `(*API).handleRecordingStatus`.

**Response 200:** `internal.RecordingStatus`.

---

## Legacy single-stream SRT output

### `POST /api/output/srt/start`

**Purpose:** start SRT output in caller (push) or listener (pull) mode.
**Auth:** required.
**Handler:** `(*API).handleSRTStart`.

**Request body** (`output.SRTConfig`):

```json
{
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9001,
  "streamID": "switchframe-program",
  "latencyMs": 200,
  "passphrase": "...",
  "pbkeylen": 16
}
```

**Errors:** 400 (bad mode / missing address in caller / missing port), 409 (`output.ErrSRTActive`, peer-gate), 501.

---

### `POST /api/output/srt/stop`

**Purpose:** stop the SRT output.
**Handler:** `(*API).handleSRTStop`.

---

### `GET /api/output/srt/status`

**Purpose:** return the current SRT output status.
**Handler:** `(*API).handleSRTStatus`.

**Response 200:** `internal.SRTOutputStatus`.

---

## Multi-destination output

### `GET /api/output/destinations`

**Purpose:** list all configured destinations (SRT caller, SRT listener, RTMP).
**Handler:** `(*API).handleListDestinations`.

**Response 200:** `[]output.DestinationStatus`.

---

### `POST /api/output/destinations`

**Purpose:** create a new destination.
**Handler:** `(*API).handleAddDestination`.

**Request body** (`output.DestinationConfig`): discriminated by `type`, plus an optional `id` of 8 lowercase hex characters. A client of an engine pair sends one `id` to both engines so both store the destination under it. `engine` names the engine of a pair that sends it, `"a"` when omitted on an engine with a label.

Per-type requirements:

- `srt-caller`: `address` + `port` required.
- `srt-listener`: `port` required; if `allowedOutputPorts` is configured, must be in the allowed set.
- `rtmp`: `url` required. An `rtmps://` destination verifies the server certificate against the system CA bundle and the host name; `allowUntrustedCert: true` skips that check.

**Response 201:** `output.DestinationStatus`.

**Errors:** 400 (missing required fields, port not allowed, invalid `id`), 409 (`id` already in use), 501.

---

### `GET /api/output/destinations/{id}`

**Purpose:** get one destination.
**Handler:** `(*API).handleGetDestination`.

**Response 200:** `output.DestinationStatus`.

**Errors:** 404 (`output.ErrDestinationNotFound`), 501.

---

### `DELETE /api/output/destinations/{id}`

**Purpose:** remove a destination.
**Handler:** `(*API).handleRemoveDestination`.

**Response 204.**

---

### `POST /api/output/destinations/{id}/start`

**Purpose:** arm the destination. The engine that sends it starts its adapter; the other engine of a pair holds it on standby and answers 200 with state `standby`.
**Handler:** `(*API).handleStartDestination`.

**Errors:** 409 (`output.ErrDestinationActive`).

---

### `POST /api/output/destinations/{id}/stop`

**Purpose:** disarm the destination and stop its adapter where it runs.
**Handler:** `(*API).handleStopDestination`.

**Errors:** 409 (`output.ErrDestinationStopped`).

---

### `PUT /api/output/destinations/{id}/engine`

**Purpose:** move the sending of a destination to another engine of a pair; sent to both engines, it is the manual switch-over. `""` sends it from every engine.
**Handler:** `(*API).handleReassignDestinationEngine`.

**Request body:** `{ "engine": "a" | "b" | "" }`.

**Response 200:** updated `output.DestinationStatus`.

**Errors:** 409 on a follower cut off from its peer, and on a move to the peer while the peer is down: the engine would take the destination back on its next failover tick.

---

## Confidence and CBR

### `GET /api/output/confidence`

**Purpose:** return the latest JPEG confidence thumbnail generated from the program output stream (captured at ~1 fps by `output.ConfidenceMonitor`).
**Auth:** required.
**Handler:** `(*API).handleConfidence`.

**Response 200:** `image/jpeg` with `Cache-Control: no-store`. Returns 204 when no thumbnail is available yet.

---

### `GET /api/output/cbr`

**Purpose:** return the CBR pacer status. The CBR pacer tops up null packets to hold the outbound MPEG-TS muxrate constant.
**Handler:** `(*API).handleCBRStatus`.

**Response 200:** `output.CBRPacerStatus` or `{ "enabled": false }` when disabled.

## Related docs

- **Concepts:** [pipeline.md](../../concepts/pipeline.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md)
- **Subsystems:** [output.md](../../subsystems/output.md)
- **Operations:** [deployment.md](../../operations/deployment.md)
