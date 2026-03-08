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
  - [PUT /api/sources/{key}/position](#put-apisourceskeyposition)
  - [PUT /api/sources/{key}/key](#put-apisourceskeykey)
  - [GET /api/sources/{key}/key](#get-apisourceskeykey)
  - [DELETE /api/sources/{key}/key](#delete-apisourceskeykey)
- [Audio](#audio)
  - [POST /api/audio/level](#post-apiaudiolevel)
  - [POST /api/audio/mute](#post-apiaudiomute)
  - [POST /api/audio/afv](#post-apiaudioafv)
  - [POST /api/audio/master](#post-apiaudiomaster)
  - [POST /api/audio/trim](#post-apiaudiotrim)
  - [PUT /api/audio/{source}/eq](#put-apiaudiosourceeq)
  - [GET /api/audio/{source}/eq](#get-apiaudiosourceeq)
  - [PUT /api/audio/{source}/compressor](#put-apiaudiosourcecompressor)
  - [GET /api/audio/{source}/compressor](#get-apiaudiosourcecompressor)
- [Recording](#recording)
  - [POST /api/recording/start](#post-apirecordingstart)
  - [POST /api/recording/stop](#post-apirecordingstop)
  - [GET /api/recording/status](#get-apirecordingstatus)
- [SRT Output](#srt-output)
  - [POST /api/output/srt/start](#post-apioutputsrtstart)
  - [POST /api/output/srt/stop](#post-apioutputsrtstop)
  - [GET /api/output/srt/status](#get-apioutputsrtstatus)
- [Multi-Destination SRT Output](#multi-destination-srt-output)
  - [POST /api/output/destinations](#post-apioutputdestinations)
  - [GET /api/output/destinations](#get-apioutputdestinations)
  - [GET /api/output/destinations/{id}](#get-apioutputdestinationsid)
  - [DELETE /api/output/destinations/{id}](#delete-apioutputdestinationsid)
  - [POST /api/output/destinations/{id}/start](#post-apioutputdestinationsidstart)
  - [POST /api/output/destinations/{id}/stop](#post-apioutputdestinationsidstop)
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
- [Confidence Monitor](#confidence-monitor)
  - [GET /api/output/confidence](#get-apioutputconfidence)
- [Stinger Transitions](#stinger-transitions)
  - [GET /api/stinger/list](#get-apistingerlist)
  - [POST /api/stinger/{name}/upload](#post-apistingernameupload)
  - [POST /api/stinger/{name}/cut-point](#post-apistingernamecutpoint)
  - [DELETE /api/stinger/{name}](#delete-apistingername)
- [Instant Replay](#instant-replay)
  - [POST /api/replay/mark-in](#post-apireplaymarkin)
  - [POST /api/replay/mark-out](#post-apireplaymarkout)
  - [POST /api/replay/play](#post-apireplayplay)
  - [POST /api/replay/stop](#post-apireplaystop)
  - [GET /api/replay/status](#get-apireplaystatus)
  - [GET /api/replay/sources](#get-apireplaysources)
- [Macros](#macros)
  - [GET /api/macros](#get-apimacros)
  - [GET /api/macros/{name}](#get-apimacrosname)
  - [PUT /api/macros/{name}](#put-apimacrosname)
  - [DELETE /api/macros/{name}](#delete-apimacrosname)
  - [POST /api/macros/{name}/run](#post-apimacrosnamerun)
- [Operators](#operators)
  - [POST /api/operator/register](#post-apioperatorregister)
  - [POST /api/operator/reconnect](#post-apioperatorreconnect)
  - [POST /api/operator/heartbeat](#post-apioperatorheartbeat)
  - [GET /api/operator/list](#get-apioperatorlist)
  - [POST /api/operator/lock](#post-apioperatorlock)
  - [POST /api/operator/unlock](#post-apioperatorunlock)
  - [POST /api/operator/force-unlock](#post-apioperatorforceunlock)
  - [DELETE /api/operator/{id}](#delete-apioperatorid)
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
    "cam1": {
      "level": 0.0, "trim": 0.0, "muted": false, "afv": true,
      "peakL": -18.5, "peakR": -19.2,
      "eq": [
        { "frequency": 100.0, "gain": 0.0, "q": 1.0, "enabled": false },
        { "frequency": 1000.0, "gain": 0.0, "q": 1.0, "enabled": false },
        { "frequency": 8000.0, "gain": 0.0, "q": 1.0, "enabled": false }
      ],
      "compressor": { "threshold": -20.0, "ratio": 4.0, "attack": 10.0, "release": 100.0, "makeupGain": 0.0 },
      "gainReduction": 0.0
    },
    "cam2": {
      "level": -6.0, "trim": 0.0, "muted": false, "afv": true,
      "peakL": -22.1, "peakR": -21.8,
      "eq": [
        { "frequency": 100.0, "gain": 0.0, "q": 1.0, "enabled": false },
        { "frequency": 1000.0, "gain": 0.0, "q": 1.0, "enabled": false },
        { "frequency": 8000.0, "gain": 0.0, "q": 1.0, "enabled": false }
      ],
      "compressor": { "threshold": -20.0, "ratio": 4.0, "attack": 10.0, "release": 100.0, "makeupGain": 0.0 },
      "gainReduction": 0.0
    }
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
  "replay": {
    "state": "idle",
    "buffers": [
      { "source": "cam1", "frameCount": 1800, "gopCount": 60, "durationSecs": 60.0, "bytesUsed": 52428800 }
    ]
  },
  "operators": [
    { "id": "op_abc123", "name": "Director", "role": "director", "connected": true },
    { "id": "op_def456", "name": "Audio Eng", "role": "audio", "connected": true }
  ],
  "locks": {
    "audio": { "holderId": "op_def456", "holderName": "Audio Eng", "acquiredAt": 1709654300000 }
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
| `transitionType` | `string` | Default transition type: `"mix"`, `"dip"`, `"wipe"`, or `"stinger"` |
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
| `replay` | `object` or `null` | Instant replay state. Omitted when replay manager is not configured. |
| `operators` | `array` | List of registered operators. Omitted when empty. |
| `locks` | `object` | Map of subsystem name to `LockInfo`. Omitted when no locks are held. |
| `seq` | `int` | Monotonically increasing sequence number |
| `timestamp` | `int` | Unix timestamp in milliseconds |

### SourceInfo

| Field | Type | Description |
|-------|------|-------------|
| `key` | `string` | Unique identifier for the source (e.g., `"cam1"`) |
| `label` | `string` | Human-readable label. Omitted if not set. |
| `status` | `string` | Health status: `"healthy"`, `"stale"`, `"no_signal"`, or `"offline"` |
| `delayMs` | `int` | Input delay in milliseconds. Omitted when `0`. |
| `keyConfig` | `object` or `null` | Upstream key configuration. Omitted when no key is configured. See [Source Keying](#put-apisourceskeykey). |

### AudioChannel

| Field | Type | Description |
|-------|------|-------------|
| `level` | `float` | Channel fader level in dB (`-inf` to `+12`) |
| `trim` | `float` | Input trim in dB (`-20` to `+20`). Applied before EQ and fader. |
| `muted` | `bool` | Whether the channel is muted |
| `afv` | `bool` | Audio-follows-video: when `true`, audio is only on-air when the source is on program |
| `peakL` | `float` | Left channel peak level in dBFS. Updated per frame. |
| `peakR` | `float` | Right channel peak level in dBFS. Updated per frame. |
| `eq` | `[3]EQBand` | 3-band parametric EQ settings (Low/Mid/High) |
| `compressor` | `CompressorSettings` | Single-band compressor settings |
| `gainReduction` | `float` | Compressor gain reduction in dB. `0` when no compression active. |

### EQBand

| Field | Type | Description |
|-------|------|-------------|
| `frequency` | `float` | Center frequency in Hz |
| `gain` | `float` | Gain in dB |
| `q` | `float` | Q factor (bandwidth). Higher values = narrower band. |
| `enabled` | `bool` | Whether this band is active |

### CompressorSettings

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Threshold in dB (signal level above which compression applies) |
| `ratio` | `float` | Compression ratio (e.g., `4.0` = 4:1 compression) |
| `attack` | `float` | Attack time in milliseconds |
| `release` | `float` | Release time in milliseconds |
| `makeupGain` | `float` | Makeup gain in dB applied after compression |

### ReplayState

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Player state: `"idle"`, `"loading"`, or `"playing"` |
| `source` | `string` | Source key being played. Omitted when idle. |
| `speed` | `float` | Playback speed (`0.25` to `1.0`). Omitted when idle. |
| `loop` | `bool` | Whether playback loops. Omitted when idle. |
| `position` | `float` | Playback progress from `0.0` to `1.0`. Omitted when idle. |
| `markIn` | `int` or `null` | Mark-in point as Unix timestamp in milliseconds. Omitted when not set. |
| `markOut` | `int` or `null` | Mark-out point as Unix timestamp in milliseconds. Omitted when not set. |
| `markSource` | `string` | Source key for the current mark points. Omitted when not set. |
| `buffers` | `array` | Per-source buffer info. Omitted when empty. |

### SourceBufferInfo

| Field | Type | Description |
|-------|------|-------------|
| `source` | `string` | Source key |
| `frameCount` | `int` | Number of buffered frames |
| `gopCount` | `int` | Number of complete GOPs in buffer |
| `durationSecs` | `float` | Duration of buffered content in seconds |
| `bytesUsed` | `int` | Memory usage in bytes |

### OperatorInfo

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique operator identifier |
| `name` | `string` | Operator display name |
| `role` | `string` | Operator role: `"director"`, `"audio"`, `"graphics"`, or `"viewer"` |
| `connected` | `bool` | Whether the operator has an active session |

### LockInfo

| Field | Type | Description |
|-------|------|-------------|
| `holderId` | `string` | Operator ID holding the lock |
| `holderName` | `string` | Operator name holding the lock |
| `acquiredAt` | `int` | Unix timestamp in milliseconds when the lock was acquired |

---

## Switching

### POST /api/switch/cut

Perform a hard cut to the specified source. The source immediately becomes the program output with no transition effect. The pipeline decoder is warmed via GOP replay for instant output — no keyframe wait required.

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
| `type` | `string` | Yes | Transition type: `"mix"`, `"dip"`, `"wipe"`, or `"stinger"` |
| `durationMs` | `int` | Yes | Duration in milliseconds. Must be `100`-`5000`. |
| `wipeDirection` | `string` | Wipe only | Direction for wipe transitions. Required when `type` is `"wipe"`. |
| `stingerName` | `string` | Stinger only | Name of the loaded stinger clip. Required when `type` is `"stinger"`. |

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

### PUT /api/sources/{key}/position

Set the display position (sort order) for a source in the multiview and source bus. Lower positions appear first.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "position": 2
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `position` | `int` | Yes | Display position index (0-based) |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `404` | Source not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/sources/cam1/position \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"position": 2}'
```

---

### PUT /api/sources/{key}/key

Configure an upstream key (chroma or luma) for a source. Upstream keys are applied per-source before the mix point, allowing compositing effects like green screen removal. Key generation operates in the YUV420 domain to avoid costly colorspace conversion.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body (chroma key example):**

```json
{
  "type": "chroma",
  "enabled": true,
  "keyColorY": 149,
  "keyColorCb": 43,
  "keyColorCr": 21,
  "similarity": 0.4,
  "smoothness": 0.1,
  "spillSuppress": 0.5
}
```

**Request Body (luma key example):**

```json
{
  "type": "luma",
  "enabled": true,
  "lowClip": 0.1,
  "highClip": 0.9,
  "softness": 0.05
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | Yes | Key type: `"chroma"` or `"luma"` |
| `enabled` | `bool` | Yes | Whether the key is active |
| `keyColorY` | `uint8` | Chroma only | Y component of the key color (0-255) |
| `keyColorCb` | `uint8` | Chroma only | Cb component of the key color (0-255) |
| `keyColorCr` | `uint8` | Chroma only | Cr component of the key color (0-255) |
| `similarity` | `float` | Chroma only | Color distance threshold for key generation |
| `smoothness` | `float` | Chroma only | Edge softness for key feathering |
| `spillSuppress` | `float` | Chroma only | Amount of spill suppression to apply |
| `lowClip` | `float` | Luma only | Low luminance clip point (0.0-1.0) |
| `highClip` | `float` | Luma only | High luminance clip point (0.0-1.0) |
| `softness` | `float` | Luma only | Edge softness for the luma key |
| `fillSource` | `string` | No | Source key providing the fill layer. If empty, the source itself is used as both fill and key. |

**Response:** `200 OK` with the applied `KeyConfig`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `type` (must be `"chroma"` or `"luma"`) or invalid JSON |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/sources/cam1/key \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"type": "chroma", "enabled": true, "keyColorY": 149, "keyColorCb": 43, "keyColorCr": 21, "similarity": 0.4, "smoothness": 0.1, "spillSuppress": 0.5}'
```

---

### GET /api/sources/{key}/key

Get the current upstream key configuration for a source.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK` with `KeyConfig`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | No key configured for the source |

**Example:**

```bash
curl http://localhost:8081/api/sources/cam1/key \
  -H "Authorization: Bearer $TOKEN"
```

---

### DELETE /api/sources/{key}/key

Remove the upstream key configuration for a source. The source returns to normal (unkeyed) compositing.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `204 No Content` (empty body)

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/sources/cam1/key \
  -H "Authorization: Bearer $TOKEN"
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

### POST /api/audio/trim

Set the input trim for a source's audio channel. Trim is applied before EQ, compression, and the channel fader. Use it to normalize input levels across sources with different signal strengths.

**Request Body:**

```json
{
  "source": "cam1",
  "trim": 6.0
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key |
| `trim` | `float` | Yes | Trim level in dB. Range: `-20` to `+20`. Use `0.0` for no trim. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source`, `trim` out of range, or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/trim \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "trim": 6.0}'
```

---

### PUT /api/audio/{source}/eq

Set a single EQ band for a source's audio channel. The 3-band parametric EQ uses RBJ Audio EQ Cookbook peakingEQ biquad coefficients. Coefficients are recalculated only on parameter change, not per-frame.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "band": 1,
  "frequency": 2500.0,
  "gain": 3.0,
  "q": 1.4,
  "enabled": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `band` | `int` | Yes | Band index: `0` (Low), `1` (Mid), or `2` (High) |
| `frequency` | `float` | Yes | Center frequency in Hz |
| `gain` | `float` | Yes | Gain in dB |
| `q` | `float` | Yes | Q factor (bandwidth). Must be `> 0`. |
| `enabled` | `bool` | Yes | Whether the band is active |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `band` index, `frequency` out of range, `gain` out of range, `q` invalid, or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/cam1/eq \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"band": 1, "frequency": 2500.0, "gain": 3.0, "q": 1.4, "enabled": true}'
```

---

### GET /api/audio/{source}/eq

Get all 3 EQ band settings for a source's audio channel.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK` with an array of 3 `EQBandSettings`:

```json
[
  { "Frequency": 100.0, "Gain": 0.0, "Q": 1.0, "Enabled": false },
  { "Frequency": 2500.0, "Gain": 3.0, "Q": 1.4, "Enabled": true },
  { "Frequency": 8000.0, "Gain": 0.0, "Q": 1.0, "Enabled": false }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `Frequency` | `float` | Center frequency in Hz |
| `Gain` | `float` | Gain in dB |
| `Q` | `float` | Q factor (bandwidth) |
| `Enabled` | `bool` | Whether the band is active |

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl http://localhost:8081/api/audio/cam1/eq \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/audio/{source}/compressor

Set the compressor parameters for a source's audio channel. The single-band compressor uses an exponential envelope follower and sits in the signal chain after EQ and before the channel fader.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "threshold": -20.0,
  "ratio": 4.0,
  "attack": 10.0,
  "release": 100.0,
  "makeupGain": 6.0
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `threshold` | `float` | Yes | Threshold in dB. Signal above this level is compressed. |
| `ratio` | `float` | Yes | Compression ratio (e.g., `4.0` = 4:1). Must be `>= 1.0`. |
| `attack` | `float` | Yes | Attack time in milliseconds. How quickly compression engages. |
| `release` | `float` | Yes | Release time in milliseconds. How quickly compression releases. |
| `makeupGain` | `float` | Yes | Makeup gain in dB applied after compression. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid threshold, ratio, attack, release, or makeupGain value, or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/cam1/compressor \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"threshold": -20.0, "ratio": 4.0, "attack": 10.0, "release": 100.0, "makeupGain": 6.0}'
```

---

### GET /api/audio/{source}/compressor

Get the current compressor settings and real-time gain reduction for a source's audio channel. The `gainReduction` field reflects the current amount of compression being applied in dB.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK` with compressor settings:

```json
{
  "threshold": -20.0,
  "ratio": 4.0,
  "attack": 10.0,
  "release": 100.0,
  "makeupGain": 6.0,
  "gainReduction": -3.2
}
```

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Threshold in dB |
| `ratio` | `float` | Compression ratio |
| `attack` | `float` | Attack time in milliseconds |
| `release` | `float` | Release time in milliseconds |
| `makeupGain` | `float` | Makeup gain in dB |
| `gainReduction` | `float` | Current gain reduction in dB. `0` when no compression is active. |

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl http://localhost:8081/api/audio/cam1/compressor \
  -H "Authorization: Bearer $TOKEN"
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

## Multi-Destination SRT Output

The multi-destination API allows adding, removing, starting, and stopping independent SRT destinations. Each destination has its own lifecycle and can be a caller (push) or listener (pull). This is the recommended API for managing SRT outputs -- the legacy single-destination endpoints above are still supported for backward compatibility.

### POST /api/output/destinations

Add a new SRT destination.

**Request Body:**

```json
{
  "name": "Platform A",
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9000,
  "latency": 200,
  "streamID": "live/stream1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Human-readable name for the destination |
| `mode` | `string` | Yes | `"caller"` (push) or `"listener"` (pull) |
| `address` | `string` | Caller only | Remote hostname or IP |
| `port` | `int` | Yes | Port number |
| `latency` | `int` | No | SRT latency in milliseconds |
| `streamID` | `string` | No | SRT stream ID |

**Response:** `201 Created` with the destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid mode, missing port, or missing address for caller |
| `501` | Output manager not configured |

---

### GET /api/output/destinations

List all configured SRT destinations with their current status.

**Response:** `200 OK` with array of destination statuses

---

### GET /api/output/destinations/{id}

Get the status of a specific destination.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Destination ID |

**Response:** `200 OK` with destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |

---

### DELETE /api/output/destinations/{id}

Remove a destination. The destination must be stopped first.

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `409` | Destination is currently active |

---

### POST /api/output/destinations/{id}/start

Start a specific destination.

**Response:** `200 OK` with destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `409` | Destination is already active |

---

### POST /api/output/destinations/{id}/stop

Stop a specific destination.

**Response:** `200 OK` with destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `409` | Destination is not active |

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

## Confidence Monitor

The confidence monitor generates low-resolution JPEG thumbnails from the program output at up to 1 frame per second. This provides a lightweight way to verify program output without decoding the full stream. The confidence monitor is automatically started when the first output (recording or SRT) begins and stopped when the last output ends.

### GET /api/output/confidence

Retrieve the latest JPEG confidence thumbnail of the program output. The thumbnail is 320x180 pixels and generated from the most recent program keyframe.

**Request Body:** None

**Response:** `200 OK` with `Content-Type: image/jpeg` and the JPEG thumbnail as the response body. The `Cache-Control: no-store` header prevents browser caching.

If no thumbnail is available (e.g., no output is active), returns `204 No Content` with an empty body.

**Errors:**

| Status | Condition |
|--------|-----------|
| `204` | No thumbnail available |
| `501` | Output manager not configured |

**Example:**

```bash
# Save thumbnail to file
curl -o confidence.jpg http://localhost:8081/api/output/confidence \
  -H "Authorization: Bearer $TOKEN"

# Use in an img tag (polling)
# <img src="/api/output/confidence" />
```

---

## Stinger Transitions

Stinger transitions use pre-loaded PNG image sequences with per-pixel alpha blending to create branded transition effects (e.g., animated logos, sports wipes). Clips are stored in memory as pre-decoded YUV420 + alpha planes for zero-latency playback. Upload clips as zip files containing numbered PNG frames.

All stinger endpoints return `404 Not Found` if the stinger store is not configured.

### GET /api/stinger/list

List all loaded stinger clips. Returns a JSON array of clip names sorted alphabetically.

**Request Body:** None

**Response:** `200 OK` with array of clip names:

```json
["logo-wipe", "sports-swoosh", "star-burst"]
```

**Example:**

```bash
curl http://localhost:8081/api/stinger/list \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/stinger/{name}/upload

Upload a stinger clip as a zip file containing a PNG image sequence. The PNG files must be numbered sequentially (e.g., `frame_001.png`, `frame_002.png`, ...) and are sorted alphabetically within the zip. Each frame must include an alpha channel (RGBA). The frames are pre-decoded to YUV420 + alpha planes on upload for zero-latency playback.

Maximum upload size: 256MB. Maximum clips in memory: 16 (configurable). Each 1080p 30-frame clip uses approximately 156MB of memory.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Clip name. Must be alphanumeric with hyphens/underscores only. |

**Request Body:** Raw zip file bytes (not multipart form). Set `Content-Type` appropriately.

**Response:** `201 Created`

```json
{"status": "ok"}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid clip name (path traversal, empty, or invalid characters) |
| `409` | A clip with this name already exists |
| `413` | Upload exceeds 256MB size limit |
| `500` | Failed to decode PNG frames or internal error |

**Example:**

```bash
curl -X POST http://localhost:8081/api/stinger/logo-wipe/upload \
  -H "Authorization: Bearer $TOKEN" \
  --data-binary @logo-wipe.zip
```

---

### POST /api/stinger/{name}/cut-point

Set the cut point for a stinger clip. The cut point determines at what fraction through the animation the underlying video source switches from A to B. For example, a cut point of `0.5` means the source cuts halfway through the stinger animation.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Clip name |

**Request Body:**

```json
{
  "cutPoint": 0.5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `cutPoint` | `float` | Yes | Cut point from `0.0` to `1.0` |

**Response:** `200 OK`

```json
{"status": "ok"}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | `cutPoint` out of range or invalid JSON |
| `404` | Clip not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/stinger/logo-wipe/cut-point \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"cutPoint": 0.5}'
```

---

### DELETE /api/stinger/{name}

Delete a stinger clip from memory. This frees all memory used by the clip's decoded frame data.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Clip name |

**Request Body:** None

**Response:** `204 No Content` (empty body)

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid clip name |
| `404` | Clip not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/stinger/logo-wipe \
  -H "Authorization: Bearer $TOKEN"
```

---

## Instant Replay

The instant replay system maintains per-source circular buffers of encoded H.264 frames. Operators can set mark-in and mark-out points to define clips, then play them back at variable speeds (0.25x to 1.0x) with frame duplication for slow motion. Replay playback outputs to the program feed as a virtual source.

All replay endpoints return `501 Not Implemented` if the replay manager is not configured.

### POST /api/replay/mark-in

Set the in-point for a replay clip on the specified source. The in-point is recorded as the current wall-clock time, corresponding to the most recent frame in the source's buffer.

**Request Body:**

```json
{
  "source": "cam1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key to mark |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` or invalid JSON |
| `404` | Source not found in replay buffers |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/mark-in \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1"}'
```

---

### POST /api/replay/mark-out

Set the out-point for a replay clip on the specified source. The out-point must be after the previously set in-point and on the same source.

**Request Body:**

```json
{
  "source": "cam1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Source key to mark. Must match the mark-in source. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source`, no mark-in set, mark-out before mark-in, or source mismatch |
| `404` | Source not found in replay buffers |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/mark-out \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1"}'
```

---

### POST /api/replay/play

Begin playback of the marked replay clip. Frames are decoded from the buffer and played back at the specified speed. Slow motion (< 1.0x) is achieved by duplicating frames. Playback outputs to the program feed.

**Request Body:**

```json
{
  "source": "cam1",
  "speed": 0.5,
  "loop": false
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `source` | `string` | Yes | -- | Source key to play from |
| `speed` | `float` | No | `1.0` | Playback speed. Range: `0.25` to `1.0`. Values below `1.0` produce slow motion. |
| `loop` | `bool` | No | `false` | Whether to loop playback continuously |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source`, no mark-in/out set, invalid marks, empty clip, `speed` out of range, or invalid JSON |
| `404` | Source not found in replay buffers |
| `409` | A replay is already playing |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/play \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "speed": 0.5, "loop": false}'
```

---

### POST /api/replay/stop

Stop the current replay playback and return to live program output.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No replay is currently playing |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/stop \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/replay/status

Get the full replay system status including player state, mark points, and per-source buffer information.

**Request Body:** None

**Response:** `200 OK` with `ReplayStatus`:

```json
{
  "state": "playing",
  "source": "cam1",
  "speed": 0.5,
  "loop": false,
  "position": 0.35,
  "markIn": "2026-03-05T14:30:10.000Z",
  "markOut": "2026-03-05T14:30:20.000Z",
  "markSource": "cam1",
  "buffers": [
    {
      "source": "cam1",
      "frameCount": 1800,
      "gopCount": 60,
      "durationSecs": 60.0,
      "bytesUsed": 52428800
    },
    {
      "source": "cam2",
      "frameCount": 1750,
      "gopCount": 58,
      "durationSecs": 58.3,
      "bytesUsed": 48234567
    }
  ]
}
```

### ReplayStatus Fields

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Player state: `"idle"`, `"loading"`, or `"playing"` |
| `source` | `string` | Source key being played. Omitted when idle. |
| `speed` | `float` | Playback speed. Omitted when idle. |
| `loop` | `bool` | Whether playback loops. Omitted when idle. |
| `position` | `float` | Playback progress from `0.0` to `1.0`. Omitted when idle. |
| `markIn` | `string` | Mark-in time (ISO 8601). Omitted when not set. |
| `markOut` | `string` | Mark-out time (ISO 8601). Omitted when not set. |
| `markSource` | `string` | Source key for the current mark points. Omitted when not set. |
| `buffers` | `array` | Per-source buffer info. See `SourceBufferInfo`. Omitted when empty. |

**Example:**

```bash
curl http://localhost:8081/api/replay/status \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/replay/sources

Get per-source replay buffer information. This is a convenience endpoint that returns just the `buffers` array from the full replay status.

**Request Body:** None

**Response:** `200 OK` with array of `SourceBufferInfo`:

```json
[
  {
    "source": "cam1",
    "frameCount": 1800,
    "gopCount": 60,
    "durationSecs": 60.0,
    "bytesUsed": 52428800
  },
  {
    "source": "cam2",
    "frameCount": 1750,
    "gopCount": 58,
    "durationSecs": 58.3,
    "bytesUsed": 48234567
  }
]
```

**Example:**

```bash
curl http://localhost:8081/api/replay/sources \
  -H "Authorization: Bearer $TOKEN"
```

---

## Macros

Macros automate sequences of switcher operations. A macro is a named list of steps that execute sequentially. Steps can include cuts, preview changes, transitions, audio adjustments, and timed waits. Macros are persisted as JSON to `~/.switchframe/macros.json`. Keyboard shortcut `Ctrl+1` through `Ctrl+9` triggers macros by position.

### GET /api/macros

List all saved macros.

**Request Body:** None

**Response:** `200 OK` with array of `Macro`:

```json
[
  {
    "name": "Opening Sequence",
    "steps": [
      { "action": "cut", "params": { "source": "cam1" } },
      { "action": "wait", "params": { "durationMs": 2000 } },
      { "action": "transition", "params": { "source": "cam2", "type": "mix", "durationMs": 1000 } }
    ]
  }
]
```

**Example:**

```bash
curl http://localhost:8081/api/macros \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/macros/{name}

Get a single macro by name.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Macro name |

**Response:** `200 OK` with `Macro`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Macro not found |

**Example:**

```bash
curl http://localhost:8081/api/macros/Opening%20Sequence \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/macros/{name}

Create or update a macro. The name in the URL path takes precedence over any name in the request body.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Macro name |

**Request Body:**

```json
{
  "name": "Opening Sequence",
  "steps": [
    { "action": "cut", "params": { "source": "cam1" } },
    { "action": "wait", "params": { "durationMs": 2000 } },
    { "action": "transition", "params": { "source": "cam2", "type": "mix", "durationMs": 1000 } },
    { "action": "set_audio", "params": { "source": "cam2", "level": -6.0 } }
  ]
}
```

### Macro Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Macro name. Must not be empty. |
| `steps` | `array` | Ordered list of `MacroStep` to execute |

### MacroStep Fields

| Field | Type | Description |
|-------|------|-------------|
| `action` | `string` | Step type (see valid actions below) |
| `params` | `object` | Action-specific parameters |

### Valid Macro Actions

| Action | Params | Description |
|--------|--------|-------------|
| `"cut"` | `{ "source": "cam1" }` | Hard cut to source |
| `"preview"` | `{ "source": "cam2" }` | Set preview source |
| `"transition"` | `{ "source": "cam2", "type": "mix", "durationMs": 1000 }` | Start a transition |
| `"wait"` | `{ "durationMs": 2000 }` | Pause execution for the specified duration |
| `"set_audio"` | `{ "source": "cam1", "level": -6.0 }` | Set audio fader level |

**Response:** `200 OK` with the saved `Macro`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Empty name, no steps provided, or invalid JSON |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/macros/Opening%20Sequence \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Opening Sequence","steps":[{"action":"cut","params":{"source":"cam1"}},{"action":"wait","params":{"durationMs":2000}},{"action":"transition","params":{"source":"cam2","type":"mix","durationMs":1000}}]}'
```

---

### DELETE /api/macros/{name}

Delete a macro by name. This is irreversible.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Macro name |

**Response:** `204 No Content` (empty body)

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Macro not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/macros/Opening%20Sequence \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/macros/{name}/run

Execute a macro. Steps are executed sequentially. Wait steps pause execution for the specified duration. The request blocks until the macro completes or is cancelled (e.g., via request timeout or client disconnect).

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Macro name |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK`

```json
{"status": "ok"}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Macro not found |
| `500` | A step in the macro failed (error message includes the failing step) |

**Example:**

```bash
curl -X POST http://localhost:8081/api/macros/Opening%20Sequence/run \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

## Operators

The operator system provides multi-operator support with role-based subsystem locking. Operators register with a name and role, receive per-operator bearer tokens, and can lock subsystems to prevent conflicting commands from other operators.

### Roles

| Role | Permissions |
|------|-------------|
| `"director"` | Full access to all subsystems. Can force-unlock any subsystem. |
| `"audio"` | Can command and lock the `audio` subsystem only. |
| `"graphics"` | Can command and lock the `graphics` subsystem only. |
| `"viewer"` | Read-only. Cannot command or lock any subsystem. |

### Lockable Subsystems

| Subsystem | Description |
|-----------|-------------|
| `"switching"` | Program/preview cuts and transitions |
| `"audio"` | Audio mixer controls |
| `"graphics"` | DSK graphics overlay |
| `"replay"` | Instant replay system |
| `"output"` | Recording and SRT output |

### POST /api/operator/register

Register a new operator. Returns a unique operator ID and bearer token. The token is used for subsequent authenticated requests (reconnect, heartbeat, lock/unlock). The operator is automatically connected after registration.

**Request Body:**

```json
{
  "name": "Director",
  "role": "director"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Operator display name. Must not be empty and must be unique. |
| `role` | `string` | Yes | Operator role: `"director"`, `"audio"`, `"graphics"`, or `"viewer"` |

**Response:** `200 OK`

```json
{
  "id": "op_abc123",
  "name": "Director",
  "role": "director",
  "token": "a1b2c3d4e5f6..."
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique operator identifier |
| `name` | `string` | Operator display name |
| `role` | `string` | Assigned role |
| `token` | `string` | Bearer token for subsequent requests. Store securely. |

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Empty name or invalid role |
| `409` | Operator name already registered |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/register \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Director", "role": "director"}'
```

---

### POST /api/operator/reconnect

Re-establish a session using a previously issued operator token. Use this after a page refresh or reconnection to restore the operator's session without re-registering.

**Headers:**

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <operator-token>` (the token from `/api/operator/register`) |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK`

```json
{
  "id": "op_abc123",
  "name": "Director",
  "role": "director"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `401` | Missing or invalid operator token |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/reconnect \
  -H "Authorization: Bearer $OPERATOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/operator/heartbeat

Send a heartbeat to keep the operator session alive. Sessions that do not heartbeat are eventually disconnected.

**Headers:**

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <operator-token>` |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `401` | Missing or invalid operator token |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/heartbeat \
  -H "Authorization: Bearer $OPERATOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/operator/list

List all registered operators with their connection status.

**Request Body:** None

**Response:** `200 OK` with array of `OperatorInfo`:

```json
[
  { "id": "op_abc123", "name": "Director", "role": "director", "connected": true },
  { "id": "op_def456", "name": "Audio Eng", "role": "audio", "connected": true },
  { "id": "op_ghi789", "name": "Observer", "role": "viewer", "connected": false }
]
```

**Example:**

```bash
curl http://localhost:8081/api/operator/list \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/operator/lock

Acquire an exclusive lock on a subsystem. Only one operator can hold a lock on a given subsystem at a time. The operator must have the appropriate role permissions to lock the requested subsystem.

**Headers:**

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <operator-token>` |

**Request Body:**

```json
{
  "subsystem": "audio"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `subsystem` | `string` | Yes | Subsystem to lock: `"switching"`, `"audio"`, `"graphics"`, `"replay"`, or `"output"` |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid subsystem name |
| `401` | Missing or invalid operator token |
| `403` | Operator's role does not have permission to lock this subsystem |
| `409` | Subsystem is already locked by another operator |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/lock \
  -H "Authorization: Bearer $OPERATOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subsystem": "audio"}'
```

---

### POST /api/operator/unlock

Release a lock on a subsystem. Only the operator who holds the lock can release it (except via force-unlock).

**Headers:**

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <operator-token>` |

**Request Body:**

```json
{
  "subsystem": "audio"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `subsystem` | `string` | Yes | Subsystem to unlock |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid subsystem name, subsystem not locked, or lock not owned by this operator |
| `401` | Missing or invalid operator token |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/unlock \
  -H "Authorization: Bearer $OPERATOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subsystem": "audio"}'
```

---

### POST /api/operator/force-unlock

Force-release a lock on a subsystem regardless of who holds it. Only operators with the `"director"` role can force-unlock. This is intended for resolving lock conflicts when an operator disconnects unexpectedly.

**Headers:**

| Header | Value |
|--------|-------|
| `Authorization` | `Bearer <operator-token>` (must be a director) |

**Request Body:**

```json
{
  "subsystem": "audio"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `subsystem` | `string` | Yes | Subsystem to force-unlock |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid subsystem name or subsystem not locked |
| `401` | Missing or invalid operator token |
| `403` | Operator is not a director |

**Example:**

```bash
curl -X POST http://localhost:8081/api/operator/force-unlock \
  -H "Authorization: Bearer $DIRECTOR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subsystem": "audio"}'
```

---

### DELETE /api/operator/{id}

Remove a registered operator. The operator's session is disconnected and any locks they hold are released.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Operator ID |

**Request Body:** None

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing operator ID |
| `404` | Operator not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/operator/op_abc123 \
  -H "Authorization: Bearer $TOKEN"
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
