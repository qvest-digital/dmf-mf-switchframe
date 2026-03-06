# Switchframe API Reference

Switchframe exposes a REST API for controlling all aspects of the live video switcher: switching sources, managing transitions, audio mixing, recording, SRT output, graphics overlays, and presets.

All endpoints are served over HTTP on port **8081** (TCP) and over HTTP/3 on port **8080** (QUIC/UDP). The API accepts and returns **JSON**. All `POST` and `PUT` requests must include `Content-Type: application/json`.

Base URL: `http://localhost:8081` (TCP) or `https://localhost:8080` (HTTP/3)

---

## Table of Contents

- [Authentication](#authentication)
- [Common Response Patterns](#common-response-patterns)
- [State Object Reference](#state-object-reference)
- [Switching](#switching)
  - [POST /api/switch/cut](#post-apiswitchcut)
  - [POST /api/switch/preview](#post-apiswitchpreview)
  - [POST /api/switch/transition](#post-apiswitchtransition)
  - [POST /api/switch/transition/position](#post-apiswitchtransitionposition)
  - [POST /api/switch/ftb](#post-apiswitchftb)
  - [GET /api/switch/state](#get-apiswitchstate)
- [Sources](#sources)
  - [GET /api/sources](#get-apisources)
  - [POST /api/sources/{key}/label](#post-apisourceskeylabel)
  - [POST /api/sources/{key}/delay](#post-apisourceskeydelay)
- [Audio](#audio)
  - [POST /api/audio/level](#post-apiaudiolevel)
  - [POST /api/audio/mute](#post-apiaudiomute)
  - [POST /api/audio/afv](#post-apiaudioafv)
  - [POST /api/audio/master](#post-apiaudiomaster)
- [Recording](#recording)
  - [POST /api/recording/start](#post-apirecordingstart)
  - [POST /api/recording/stop](#post-apirecordingstop)
  - [GET /api/recording/status](#get-apirecordingstatus)
- [SRT Output](#srt-output)
  - [POST /api/output/srt/start](#post-apioutputsrtstart)
  - [POST /api/output/srt/stop](#post-apioutputsrtstop)
  - [GET /api/output/srt/status](#get-apioutputsrtstatus)
- [Graphics Overlay (DSK)](#graphics-overlay-dsk)
  - [POST /api/graphics/on](#post-apigraphicson)
  - [POST /api/graphics/off](#post-apigraphicsoff)
  - [POST /api/graphics/auto-on](#post-apigraphicsauto-on)
  - [POST /api/graphics/auto-off](#post-apigraphicsauto-off)
  - [GET /api/graphics/status](#get-apigraphicsstatus)
  - [POST /api/graphics/frame](#post-apigraphicsframe)
- [Presets](#presets)
  - [GET /api/presets](#get-apipresets)
  - [POST /api/presets](#post-apipresets)
  - [GET /api/presets/{id}](#get-apipresetsid)
  - [PUT /api/presets/{id}](#put-apipresetsid)
  - [DELETE /api/presets/{id}](#delete-apipresetsid)
  - [POST /api/presets/{id}/recall](#post-apipresetsidrecall)
- [Debug](#debug)
  - [GET /api/debug/snapshot](#get-apidebugsnapshot)
- [Admin Endpoints](#admin-endpoints)
  - [GET /health](#get-health)
  - [GET /ready](#get-ready)
  - [GET /metrics](#get-metrics)
  - [GET /api/cert-hash](#get-apicert-hash)

---

## Authentication

All `/api/*` endpoints require Bearer token authentication (except in demo mode and exempt paths listed below).

### Providing the Token

Include the token in the `Authorization` header:

```
Authorization: Bearer <token>
```

### Obtaining a Token

The API token is resolved in the following priority order:

1. **CLI flag:** `--api-token <token>`
2. **Environment variable:** `SWITCHFRAME_API_TOKEN`
3. **Auto-generated:** If neither is set, a cryptographically random 64-character hex token is generated at startup and printed to stdout.

At startup, the server logs the token prefix to stderr and prints the full token to stdout:

```
  API Token: a1b2c3d4e5f6...
```

### Demo Mode

When started with `--demo`, authentication is disabled entirely. All API requests are accepted without a token.

### Exempt Paths

The following paths bypass authentication even when auth is enabled:

| Path | Purpose |
|------|---------|
| `/api/cert-hash` | WebTransport certificate bootstrapping |
| `/health` | Liveness probe |
| `/metrics` | Prometheus scraping |

### Error Response

Missing or invalid token returns:

```http
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="switchframe"
Content-Type: application/json

{"error": "missing or invalid authorization header"}
```

---

## Common Response Patterns

### Success

Most mutation endpoints (cut, preview, audio, etc.) return the full `ControlRoomState` object on success, providing an immediate snapshot of the entire switcher state after the operation.

### Errors

All error responses use a consistent JSON envelope:

```json
{"error": "human-readable error message"}
```

Common HTTP status codes:

| Code | Meaning |
|------|---------|
| `200` | Success |
| `201` | Created (preset creation) |
| `204` | No Content (preset deletion) |
| `400` | Bad request (invalid JSON, missing required field, out-of-range value) |
| `401` | Unauthorized (missing or invalid token) |
| `404` | Not found (source or preset does not exist) |
| `409` | Conflict (transition already active, recorder already running, etc.) |
| `500` | Internal server error |
| `501` | Not implemented (audio mixer or output manager not configured) |

### Request Tracing

Every response includes an `X-Request-ID` header. If you send `X-Request-ID` in your request, the server preserves it; otherwise, a random ID is generated. This is useful for correlating requests with server logs.

---

## State Object Reference

Many endpoints return the full `ControlRoomState` object. Here is its complete structure:

### ControlRoomState

```json
{
  "programSource": "cam1",
  "previewSource": "cam2",
  "transitionType": "mix",
  "transitionDurationMs": 1000,
  "transitionPosition": 0.0,
  "inTransition": false,
  "ftbActive": false,
  "audioChannels": {
    "cam1": { "level": 0.0, "muted": false, "afv": true },
    "cam2": { "level": -6.0, "muted": false, "afv": true }
  },
  "masterLevel": 0.0,
  "programPeak": [-18.5, -19.2],
  "gainReduction": 0.0,
  "tallyState": {
    "cam1": "program",
    "cam2": "preview",
    "cam3": "idle"
  },
  "recording": {
    "active": true,
    "filename": "program_20260305_143022_001.ts",
    "bytesWritten": 52428800,
    "durationSecs": 120.5,
    "error": ""
  },
  "srtOutput": {
    "active": true,
    "mode": "caller",
    "address": "ingest.example.com",
    "port": 9000,
    "state": "active",
    "connections": 1,
    "bytesWritten": 104857600,
    "error": ""
  },
  "sources": {
    "cam1": { "key": "cam1", "label": "Stage Left", "status": "healthy", "delayMs": 0 },
    "cam2": { "key": "cam2", "label": "Stage Right", "status": "healthy", "delayMs": 100 }
  },
  "presets": [
    { "id": "550e8400-e29b-41d4-a716-446655440000", "name": "Opening" }
  ],
  "graphics": {
    "active": true,
    "template": "lower-third",
    "fadePosition": 1.0
  },
  "seq": 42,
  "timestamp": 1709654400000
}
```

### Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `programSource` | `string` | Key of the source currently on program (live) output |
| `previewSource` | `string` | Key of the source currently on preview |
| `transitionType` | `string` | Default transition type: `"mix"`, `"dip"`, or `"wipe"` |
| `transitionDurationMs` | `int` | Default transition duration in milliseconds |
| `transitionPosition` | `float` | Current T-bar position during a transition (`0.0` to `1.0`). Omitted when `0`. |
| `inTransition` | `bool` | `true` while a dissolve/dip/wipe transition is in progress. Omitted when `false`. |
| `ftbActive` | `bool` | `true` while Fade to Black is active. Omitted when `false`. |
| `audioChannels` | `object` | Map of source key to `AudioChannel` state |
| `masterLevel` | `float` | Master output level in dB |
| `programPeak` | `[float, float]` | Stereo peak levels in dBFS for the program output `[left, right]` |
| `gainReduction` | `float` | Brickwall limiter gain reduction in dB. Omitted when `0`. |
| `tallyState` | `object` | Map of source key to tally status: `"program"`, `"preview"`, or `"idle"` |
| `recording` | `object` or `null` | Recording status. Omitted when not recording. |
| `srtOutput` | `object` or `null` | SRT output status. Omitted when not active. |
| `sources` | `object` | Map of source key to `SourceInfo` |
| `presets` | `array` | List of saved preset summaries `[{id, name}]`. Omitted when empty. |
| `graphics` | `object` or `null` | Graphics overlay state. Omitted when not active. |
| `seq` | `int` | Monotonically increasing sequence number |
| `timestamp` | `int` | Unix timestamp in milliseconds |

### SourceInfo

| Field | Type | Description |
|-------|------|-------------|
| `key` | `string` | Unique identifier for the source (e.g., `"cam1"`) |
| `label` | `string` | Human-readable label. Omitted if not set. |
| `status` | `string` | Health status: `"healthy"`, `"stale"`, `"no_signal"`, or `"offline"` |
| `delayMs` | `int` | Input delay in milliseconds. Omitted when `0`. |

### AudioChannel

| Field | Type | Description |
|-------|------|-------------|
| `level` | `float` | Channel level in dB (`-inf` to `+12`) |
| `muted` | `bool` | Whether the channel is muted |
| `afv` | `bool` | Audio-follows-video: when `true`, audio is only on-air when the source is on program |

---

## Switching

### POST /api/switch/cut

Perform a hard cut to the specified source. The source immediately becomes the program output with no transition effect. After a cut, video and audio are gated until the first IDR keyframe arrives from the new source to prevent decoder artifacts.

**Request Body:**

```json
{
  "source": "cam2"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Key of the source to cut to |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` field or invalid JSON |
| `404` | Source not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/switch/cut \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam2"}'
```

---

### POST /api/switch/preview

Set the preview source without affecting the program output. The preview source is shown in the preview monitor and is the default target for the next transition.

**Request Body:**

```json
{
  "source": "cam3"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Key of the source to preview |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` field or invalid JSON |
| `404` | Source not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/switch/preview \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam3"}'
```

---

### POST /api/switch/transition

Start a dissolve, dip-to-black, or wipe transition to the specified source. The server decodes frames from both sources, blends them with smoothstep easing, and encodes the result. Audio crossfades simultaneously using an equal-power curve.

**Request Body:**

```json
{
  "source": "cam2",
  "type": "mix",
  "durationMs": 1000,
  "wipeDirection": "h-left"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Key of the source to transition to |
| `type` | `string` | Yes | Transition type: `"mix"`, `"dip"`, or `"wipe"` |
| `durationMs` | `int` | Yes | Duration in milliseconds. Must be `100`-`5000`. |
| `wipeDirection` | `string` | Wipe only | Direction for wipe transitions. Required when `type` is `"wipe"`. |

**Valid `wipeDirection` values:**

| Value | Description |
|-------|-------------|
| `"h-left"` | Horizontal wipe from right to left |
| `"h-right"` | Horizontal wipe from left to right |
| `"v-top"` | Vertical wipe from bottom to top |
| `"v-bottom"` | Vertical wipe from top to bottom |
| `"box-center-out"` | Box wipe expanding from center |
| `"box-edges-in"` | Box wipe contracting from edges |

**Response:** `200 OK` with full `ControlRoomState`

The returned state will have `inTransition: true` and `transitionPosition` updating as the transition progresses.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `type`, `durationMs` out of range, source already on program, or invalid `wipeDirection` |
| `404` | Source not found |
| `409` | Another transition or FTB is already active |

**Example:**

```bash
curl -X POST http://localhost:8081/api/switch/transition \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam2", "type": "mix", "durationMs": 1000}'
```

---

### POST /api/switch/transition/position

Set the T-bar position during an active transition for manual control. This endpoint is designed for high-frequency updates from a hardware T-bar or on-screen fader. The client should throttle calls to 50ms / 20Hz maximum.

**Request Body:**

```json
{
  "position": 0.5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `position` | `float` | Yes | Transition position from `0.0` (source A) to `1.0` (source B) |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | `position` out of range (`0`-`1`) or invalid JSON |
| `409` | No active transition |

**Example:**

```bash
curl -X POST http://localhost:8081/api/switch/transition/position \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"position": 0.5}'
```

---

### POST /api/switch/ftb

Start or toggle a Fade to Black transition. When called while the program is live, it fades the output to black. When called while FTB is active, it performs a smooth reverse fade back to the program source.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `ControlRoomState`

The returned state will show `ftbActive: true` once FTB completes.

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | A dissolve/dip/wipe transition is currently active |

**Example:**

```bash
curl -X POST http://localhost:8081/api/switch/ftb \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/switch/state

Retrieve the current switcher state. This is the same `ControlRoomState` object broadcast via MoQ on the `"control"` track. Useful for polling when MoQ/WebTransport is not available.

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`

**Example:**

```bash
curl http://localhost:8081/api/switch/state \
  -H "Authorization: Bearer $TOKEN"
```

---

## Sources

### GET /api/sources

List all registered video sources with their current info and health status.

**Request Body:** None

**Response:** `200 OK` with a map of source key to `SourceInfo`:

```json
{
  "cam1": {
    "key": "cam1",
    "label": "Stage Left",
    "status": "healthy"
  },
  "cam2": {
    "key": "cam2",
    "status": "healthy",
    "delayMs": 100
  },
  "cam3": {
    "key": "cam3",
    "status": "stale"
  }
}
```

**Source Health Statuses:**

| Status | Description |
|--------|-------------|
| `"healthy"` | Receiving frames normally |
| `"stale"` | Frames are arriving but at reduced rate |
| `"no_signal"` | No frames received recently |
| `"offline"` | Source has disconnected |

**Example:**

```bash
curl http://localhost:8081/api/sources \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/sources/{key}/label

Set a human-readable label on a source. Labels are displayed in the UI multiview, source buttons, and audio mixer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "label": "Stage Left"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `label` | `string` | Yes | Display label for the source. Can be empty to clear. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `404` | Source not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/sources/cam1/label \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"label": "Stage Left"}'
```

---

### POST /api/sources/{key}/delay

Set the input delay buffer for a source. This adds a configurable delay (0-500ms) to compensate for transport latency differences between sources. Frames are buffered and released after the specified delay.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "delayMs": 100
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `delayMs` | `int` | Yes | Delay in milliseconds. Range: `0`-`500`. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | `delayMs` out of range or invalid JSON |
| `404` | Source not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/sources/cam1/delay \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"delayMs": 100}'
```

---

## Audio

The audio API controls the server-side FDK AAC mixer. Each source has an independent audio channel with level, mute, and AFV controls. A master level controls the final program output. When only a single source is active at 0 dB with no AFV changes pending, the mixer operates in passthrough mode with zero CPU overhead.

All audio endpoints return `501 Not Implemented` if the audio mixer is not configured.

### POST /api/audio/level

Set the fader level for a source's audio channel.

**Request Body:**

```json
{
  "source": "cam1",
  "level": -6.0
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key |
| `level` | `float` | Yes | Level in dB. Typical range: `-inf` to `+12`. Use `0.0` for unity gain. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/level \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "level": -6.0}'
```

---

### POST /api/audio/mute

Set the mute state for a source's audio channel. When muted, the channel's audio is silenced regardless of its fader level.

**Request Body:**

```json
{
  "source": "cam1",
  "muted": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key |
| `muted` | `bool` | Yes | `true` to mute, `false` to unmute |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/mute \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "muted": true}'
```

---

### POST /api/audio/afv

Set the Audio-Follows-Video (AFV) mode for a source's audio channel. When AFV is enabled, the channel's audio is automatically mixed into the program output only when that source is on program. When the source leaves program, its audio fades out via a crossfade. New sources default to AFV enabled.

**Request Body:**

```json
{
  "source": "cam1",
  "afv": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key |
| `afv` | `bool` | Yes | `true` to enable AFV, `false` to disable (always on-air) |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/afv \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "afv": false}'
```

---

### POST /api/audio/master

Set the master output level. This applies to the final mixed program audio after all channel levels, mutes, and AFV have been applied. A brickwall limiter at -1 dBFS is applied after the master level to prevent clipping.

**Request Body:**

```json
{
  "level": -3.0
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `level` | `float` | Yes | Master level in dB. Use `0.0` for unity gain. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/master \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"level": 0.0}'
```

---

## Recording

Record the program output to MPEG-TS files on disk. MPEG-TS is used for crash resilience (no moov atom to finalize). Files are named `program_YYYYMMDD_HHMMSS_NNN.ts` with sequential numbering across rotations.

All recording endpoints return `501 Not Implemented` if the output manager is not configured.

### POST /api/recording/start

Begin recording the program output to a file. File rotation occurs based on time elapsed and/or file size thresholds.

**Request Body:**

```json
{
  "outputDir": "/recordings",
  "rotateAfterMins": 60,
  "maxFileSizeMB": 4096
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `outputDir` | `string` | No | OS temp dir `/tmp/switchframe-recordings` | Absolute path to the output directory. Must be an absolute path. Created if it does not exist. |
| `rotateAfterMins` | `int` | No | `60` (1 hour) | Rotate to a new file after this many minutes. Set to `0` to disable time-based rotation. |
| `maxFileSizeMB` | `int` | No | `0` (unlimited) | Rotate to a new file after reaching this size in megabytes. |

**Response:** `200 OK` with `RecordingStatus`:

```json
{
  "active": true,
  "filename": "program_20260305_143022_001.ts",
  "bytesWritten": 0,
  "durationSecs": 0.0
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | `outputDir` is not an absolute path, or invalid JSON |
| `409` | Recording is already active |
| `501` | Output manager not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/recording/start \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"outputDir": "/recordings", "rotateAfterMins": 30}'
```

---

### POST /api/recording/stop

Stop the active recording and finalize the current file.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `RecordingStatus`:

```json
{
  "active": false,
  "filename": "program_20260305_143022_001.ts",
  "bytesWritten": 52428800,
  "durationSecs": 120.5
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | No recording is active |
| `501` | Output manager not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/recording/stop \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/recording/status

Get the current recording status without changing state.

**Request Body:** None

**Response:** `200 OK` with `RecordingStatus`:

```json
{
  "active": true,
  "filename": "program_20260305_143022_001.ts",
  "bytesWritten": 52428800,
  "durationSecs": 120.5
}
```

### RecordingStatus Fields

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | Whether recording is in progress |
| `filename` | `string` | Current recording filename. Omitted when not active. |
| `bytesWritten` | `int` | Total bytes written to the current file. Omitted when not active. |
| `durationSecs` | `float` | Elapsed recording time in seconds. Omitted when not active. |
| `error` | `string` | Error message if recording failed. Omitted when no error. |

**Example:**

```bash
curl http://localhost:8081/api/recording/status \
  -H "Authorization: Bearer $TOKEN"
```

---

## SRT Output

Push the program output via SRT (Secure Reliable Transport) to a remote server or accept incoming SRT connections. Uses MPEG-TS muxing over SRT. A 4MB ring buffer provides reconnection resilience for caller mode.

All SRT endpoints return `501 Not Implemented` if the output manager is not configured.

### POST /api/output/srt/start

Start SRT output in either caller (push) or listener (pull) mode.

**Request Body:**

```json
{
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9000,
  "latency": 200,
  "streamID": "live/stream1"
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `mode` | `string` | Yes | -- | `"caller"` (push to remote) or `"listener"` (accept incoming connections) |
| `address` | `string` | Caller only | -- | Remote hostname or IP. Required when `mode` is `"caller"`. |
| `port` | `int` | Yes | -- | Port number. For caller: remote port. For listener: local bind port. |
| `latency` | `int` | No | SRT default | SRT latency in milliseconds |
| `streamID` | `string` | No | -- | SRT stream ID for multiplexing |

**Caller mode** connects to a remote SRT server and pushes the program stream. Reconnection uses exponential backoff (1s to 30s) with a 4MB ring buffer to avoid frame loss during brief disconnects.

**Listener mode** binds a local port and accepts up to 8 incoming SRT connections. All connected clients receive the same program stream.

**Response:** `200 OK` with `SRTOutputStatus`:

```json
{
  "active": true,
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9000,
  "state": "starting",
  "connections": 0,
  "bytesWritten": 0
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `mode`, missing `port`, or missing `address` for caller mode |
| `409` | SRT output is already active |
| `501` | Output manager not configured |

**Example (caller mode):**

```bash
curl -X POST http://localhost:8081/api/output/srt/start \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "caller", "address": "ingest.example.com", "port": 9000}'
```

**Example (listener mode):**

```bash
curl -X POST http://localhost:8081/api/output/srt/start \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "listener", "port": 9000}'
```

---

### POST /api/output/srt/stop

Stop the active SRT output. In caller mode, disconnects from the remote server. In listener mode, closes the listening socket and disconnects all clients.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `SRTOutputStatus`:

```json
{
  "active": false,
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9000,
  "state": "stopped",
  "bytesWritten": 104857600
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | No SRT output is active |
| `501` | Output manager not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/output/srt/stop \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/output/srt/status

Get the current SRT output status without changing state.

**Request Body:** None

**Response:** `200 OK` with `SRTOutputStatus`:

```json
{
  "active": true,
  "mode": "listener",
  "port": 9000,
  "state": "active",
  "connections": 3,
  "bytesWritten": 209715200
}
```

### SRTOutputStatus Fields

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | Whether SRT output is running |
| `mode` | `string` | `"caller"` or `"listener"`. Omitted when not active. |
| `address` | `string` | Remote address (caller mode only). Omitted for listener. |
| `port` | `int` | Port number. Omitted when not active. |
| `state` | `string` | Adapter state: `"starting"`, `"active"`, `"reconnecting"`, `"stopped"`, or `"error"`. Omitted when not active. |
| `connections` | `int` | Number of active connections (listener mode). Omitted when not active. |
| `bytesWritten` | `int` | Total bytes sent. Omitted when not active. |
| `error` | `string` | Error message if output failed. Omitted when no error. |

**Example:**

```bash
curl http://localhost:8081/api/output/srt/status \
  -H "Authorization: Bearer $TOKEN"
```

---

## Graphics Overlay (DSK)

The downstream keyer (DSK) composites an RGBA overlay (lower thirds, logos, score bugs, etc.) onto the program output. The overlay is rendered in the browser and uploaded as raw RGBA pixel data. The server decodes program frames, alpha-blends the overlay, and re-encodes. When inactive, frames pass through unchanged with zero CPU overhead.

### POST /api/graphics/on

Activate the overlay immediately (CUT ON). The overlay appears on the program output at full opacity in a single frame. Requires that an overlay frame has been previously uploaded via `POST /api/graphics/frame`.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `GraphicsState`:

```json
{
  "active": true,
  "template": "lower-third",
  "fadePosition": 1.0
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No overlay frame has been uploaded |
| `409` | Overlay is already active |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/on \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/off

Deactivate the overlay immediately (CUT OFF). The overlay disappears from the program output in a single frame.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `GraphicsState`:

```json
{
  "active": false
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | Overlay is not active |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/off \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/auto-on

Start a 500ms fade-in transition (AUTO ON). The overlay fades in smoothly from invisible to fully opaque over 500ms. Requires that an overlay frame has been previously uploaded.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `GraphicsState`:

```json
{
  "active": true,
  "template": "lower-third",
  "fadePosition": 0.0
}
```

The `fadePosition` will progress from `0.0` to `1.0` over the 500ms duration. State updates are broadcast via MoQ at ~60fps during the fade.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No overlay frame has been uploaded |
| `409` | A fade transition is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/auto-on \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/auto-off

Start a 500ms fade-out transition (AUTO OFF). The overlay fades out smoothly from fully opaque to invisible over 500ms. The overlay becomes inactive when the fade completes.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `GraphicsState`:

```json
{
  "active": true,
  "template": "lower-third",
  "fadePosition": 1.0
}
```

The `fadePosition` will progress from `1.0` to `0.0` over the 500ms duration.

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | Overlay is not active, or a fade transition is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/auto-off \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/graphics/status

Get the current graphics overlay state.

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`:

```json
{
  "active": true,
  "template": "lower-third",
  "fadePosition": 1.0,
  "programWidth": 1920,
  "programHeight": 1080
}
```

### GraphicsState Fields

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | Whether the overlay is currently composited onto program |
| `template` | `string` | Name of the current overlay template. Omitted if not set. |
| `fadePosition` | `float` | Opacity level: `0.0` = invisible, `1.0` = fully visible. Omitted when `0`. |
| `programWidth` | `int` | Current program video width in pixels. Omitted when unknown. |
| `programHeight` | `int` | Current program video height in pixels. Omitted when unknown. |

**Example:**

```bash
curl http://localhost:8081/api/graphics/status \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/frame

Upload an RGBA overlay frame from the browser. The RGBA pixel data is base64-encoded in the JSON body. The overlay resolution must match the program video resolution for compositing to work. This can be called while the overlay is active to update the graphics in real-time (e.g., animated score bug).

Maximum body size: 16MB (supports up to 4K resolution: 3840x2160x4 = ~33MB raw, but base64 overhead keeps practical limit under 16MB for 1080p).

**Request Body:**

```json
{
  "width": 1920,
  "height": 1080,
  "template": "lower-third",
  "rgba": "<base64-encoded RGBA pixel data>"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `width` | `int` | Yes | Overlay width in pixels. Must be `1`-`3840`. |
| `height` | `int` | Yes | Overlay height in pixels. Must be `1`-`2160`. |
| `template` | `string` | Yes | Template name for identification in status/state |
| `rgba` | `string` | Yes | Base64-encoded raw RGBA pixel data. Must be exactly `width * height * 4` bytes when decoded. |

**Response:** `200 OK` with `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid dimensions, resolution exceeds 4K, RGBA data size mismatch, or invalid JSON |

**Example:**

```bash
# Upload a 320x240 test overlay (307,200 bytes of RGBA data, base64-encoded)
curl -X POST http://localhost:8081/api/graphics/frame \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"width": 320, "height": 240, "template": "test", "rgba": "AAAA..."}'
```

---

## Presets

Presets save and recall complete production setups: program source, preview source, transition type, audio channel levels/mute/AFV states, and master level. Presets are persisted as JSON to `~/.switchframe/presets.json`.

### GET /api/presets

List all saved presets, ordered by creation time (oldest first).

**Request Body:** None

**Response:** `200 OK` with array of `Preset`:

```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "Opening",
    "programSource": "cam1",
    "previewSource": "cam2",
    "transitionType": "mix",
    "transitionDurMs": 1000,
    "audioChannels": {
      "cam1": { "level": 0.0, "muted": false, "afv": true },
      "cam2": { "level": -6.0, "muted": false, "afv": true }
    },
    "masterLevel": 0.0,
    "createdAt": "2026-03-05T14:30:22.123456Z"
  }
]
```

**Example:**

```bash
curl http://localhost:8081/api/presets \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/presets

Create a new preset from the current switcher and audio state. The preset captures a snapshot of the program source, preview source, transition settings, all audio channel levels/mute/AFV states, and the master level at the moment of creation.

**Request Body:**

```json
{
  "name": "Opening"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Human-readable name for the preset. Must not be empty. |

**Response:** `201 Created` with the created `Preset`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "Opening",
  "programSource": "cam1",
  "previewSource": "cam2",
  "transitionType": "mix",
  "transitionDurMs": 1000,
  "audioChannels": {
    "cam1": { "level": 0.0, "muted": false, "afv": true }
  },
  "masterLevel": 0.0,
  "createdAt": "2026-03-05T14:30:22.123456Z"
}
```

### Preset Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | UUID v4 identifier |
| `name` | `string` | Human-readable name |
| `programSource` | `string` | Source key that was on program |
| `previewSource` | `string` | Source key that was on preview |
| `transitionType` | `string` | Transition type at time of save |
| `transitionDurMs` | `int` | Transition duration in milliseconds |
| `audioChannels` | `object` | Map of source key to `AudioChannelPreset` |
| `masterLevel` | `float` | Master audio level in dB |
| `createdAt` | `string` | ISO 8601 timestamp |

### AudioChannelPreset Fields

| Field | Type | Description |
|-------|------|-------------|
| `level` | `float` | Channel level in dB |
| `muted` | `bool` | Whether the channel was muted |
| `afv` | `bool` | Whether AFV was enabled |

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing or empty `name` |

**Example:**

```bash
curl -X POST http://localhost:8081/api/presets \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Opening"}'
```

---

### GET /api/presets/{id}

Retrieve a single preset by its ID.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Preset UUID |

**Response:** `200 OK` with `Preset`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Preset not found |

**Example:**

```bash
curl http://localhost:8081/api/presets/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/presets/{id}

Update a preset's name. The preset's captured state (program, preview, audio) is immutable after creation.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Preset UUID |

**Request Body:**

```json
{
  "name": "New Name"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | New name for the preset. Must not be empty. |

**Response:** `200 OK` with updated `Preset`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Empty name |
| `404` | Preset not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/presets/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Interview Setup"}'
```

---

### DELETE /api/presets/{id}

Delete a preset by ID. This is irreversible.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Preset UUID |

**Response:** `204 No Content` (empty body)

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Preset not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/presets/550e8400-e29b-41d4-a716-446655440000 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/presets/{id}/recall

Apply a saved preset to the live switcher and audio mixer. Recall is best-effort: it applies as much of the preset as possible and returns warnings for anything that could not be applied (e.g., a source in the preset that is no longer connected).

The recall performs these operations in order:

1. Cut to the preset's program source
2. Set the preset's preview source
3. Apply all audio channel settings (level, mute, AFV)
4. Set the master level

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Preset UUID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with `RecallPresetResponse`:

```json
{
  "preset": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "Opening",
    "programSource": "cam1",
    "previewSource": "cam2",
    "transitionType": "mix",
    "transitionDurMs": 1000,
    "audioChannels": {
      "cam1": { "level": 0.0, "muted": false, "afv": true }
    },
    "masterLevel": 0.0,
    "createdAt": "2026-03-05T14:30:22.123456Z"
  },
  "warnings": [
    "preview source \"cam4\": source not found"
  ]
}
```

### RecallPresetResponse Fields

| Field | Type | Description |
|-------|------|-------------|
| `preset` | `Preset` | The preset that was recalled |
| `warnings` | `string[]` | List of warnings for operations that failed. Omitted when empty. |

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Preset not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/presets/550e8400-e29b-41d4-a716-446655440000/recall \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Debug

### GET /api/debug/snapshot

Return a comprehensive diagnostic snapshot of all subsystems. This is intended for debugging and includes internal state from the switcher, audio mixer, output manager, and demo subsystem (if running). Also includes the most recent 100 events from the circular event log.

**Request Body:** None

**Response:** `200 OK` with a JSON object containing subsystem snapshots:

```json
{
  "timestamp": "2026-03-05T14:30:22.123456789Z",
  "uptime_ms": 3600000,
  "switcher": {
    "program_source": "cam1",
    "preview_source": "cam2",
    "sources": ["cam1", "cam2", "cam3", "cam4"],
    "in_transition": false
  },
  "mixer": {
    "channels": 4,
    "master_level": 0.0,
    "passthrough": true
  },
  "output": {
    "recording_active": false,
    "srt_active": false
  },
  "demo": {
    "sources": 4,
    "frames_sent": 108000
  },
  "events": [
    {
      "time": "2026-03-05T14:30:20.000Z",
      "event": "cut",
      "details": {"from": "cam2", "to": "cam1"}
    }
  ]
}
```

The exact structure of each subsystem's snapshot depends on its implementation of the `DebugSnapshot()` interface. The fields shown above are illustrative.

**Example:**

```bash
curl http://localhost:8081/api/debug/snapshot \
  -H "Authorization: Bearer $TOKEN"
```

---

## Admin Endpoints

These endpoints are served on the admin server (default port **9090**, configurable via `--admin-addr`). They are separate from the main API and do not require authentication.

### GET /health

Liveness probe. Always returns `200 OK` if the process is running.

**Response:**

```json
{"status": "ok"}
```

---

### GET /ready

Readiness probe. Returns `503 Service Unavailable` during startup until all components are initialized, then `200 OK`.

**Response (ready):**

```json
{"status": "ready"}
```

**Response (not ready):**

```http
HTTP/1.1 503 Service Unavailable

{"status": "not_ready"}
```

---

### GET /metrics

Prometheus metrics endpoint. Returns metrics in Prometheus text exposition format. Includes:

- `http_requests_total` -- Counter by method, route, status
- `http_request_duration_seconds` -- Histogram by method, route
- Switcher, mixer, and output subsystem metrics

---

### GET /api/cert-hash

Returns the WebTransport TLS certificate fingerprint. This is needed by browsers to establish a WebTransport connection to the QUIC server. This endpoint is exempt from authentication.

Served on both the main API (port 8081) and the QUIC server (port 8080).

**Response:**

```json
{
  "hash": "sha-256 base64-encoded fingerprint",
  "addr": ":8080"
}
```

**Example:**

```bash
curl http://localhost:8081/api/cert-hash
```

---

## Real-Time State Updates

In addition to polling `GET /api/switch/state`, clients can receive real-time state updates via the MoQ (Media over QUIC) `"control"` track. Each message on this track is a full `ControlRoomState` JSON snapshot. This is the primary mechanism used by the Switchframe UI for sub-frame latency state synchronization.

The UI falls back to REST polling when WebTransport/MoQ is unavailable (e.g., behind a reverse proxy that does not support QUIC).
