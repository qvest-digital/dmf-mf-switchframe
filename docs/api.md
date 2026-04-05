# Switchframe API Reference

Switchframe exposes a REST API for controlling all aspects of the live video switcher: switching sources, managing transitions, audio mixing, recording, SRT input and output, graphics overlays, and presets.

All endpoints are served over HTTP/3 on port **8080** (QUIC/UDP). An optional plain HTTP/1.1 server on TCP port **8081** can be enabled with `--http-fallback` for curl, scripts, and environments that cannot speak QUIC. The API accepts and returns **JSON**. All `POST` and `PUT` requests must include `Content-Type: application/json`.

Base URL: `https://localhost:8080` (HTTP/3, primary) or `http://localhost:8081` (TCP, requires `--http-fallback`)

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
  - [GET /api/sources/{key}](#get-apisourceskey)
  - [POST /api/sources](#post-apisources)
  - [DELETE /api/sources/{key}](#delete-apisourceskey)
  - [POST /api/sources/{key}/label](#post-apisourceskeylabel)
  - [POST /api/sources/{key}/delay](#post-apisourceskeydelay)
  - [PUT /api/sources/{key}/position](#put-apisourceskeyposition)
  - [GET /api/sources/{key}/srt/stats](#get-apisourceskeysrtstats)
  - [PUT /api/sources/{key}/srt](#put-apisourceskeysrt)
  - [PUT /api/sources/{key}/key](#put-apisourceskeykey)
  - [GET /api/sources/{key}/key](#get-apisourceskeykey)
  - [DELETE /api/sources/{key}/key](#delete-apisourceskeykey)
  - [GET /api/sources/{key}/corrector](#get-apisourceskeycorrector)
  - [PUT /api/sources/{key}/corrector](#put-apisourceskeycorrector)
  - [DELETE /api/sources/{key}/corrector](#delete-apisourceskeycorrector)
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
  - [PUT /api/audio/{source}/audio-delay](#put-apiaudiosourceaudio-delay)
  - [POST /api/audio/phase](#post-apiaudiophase)
  - [POST /api/audio/balance](#post-apiaudiobalance)
  - [PUT /api/audio/{source}/hpf](#put-apiaudiosourcehpf)
  - [GET /api/audio/{source}/hpf](#get-apiaudiosourcehpf)
  - [PUT /api/audio/{source}/gate](#put-apiaudiosourcegate)
  - [GET /api/audio/{source}/gate](#get-apiaudiosourcegate)
  - [PUT /api/audio/limiter](#put-apiaudiolimiter)
  - [GET /api/audio/limiter](#get-apiaudiolimiter)
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
- [Graphics (Multi-Layer DSK)](#graphics-multi-layer-dsk)
  - [POST /api/graphics](#post-apigraphics)
  - [GET /api/graphics](#get-apigraphics)
  - [DELETE /api/graphics/{id}](#delete-apigraphicsid)
  - [POST /api/graphics/{id}/on](#post-apigraphicsidon)
  - [POST /api/graphics/{id}/off](#post-apigraphicsidoff)
  - [POST /api/graphics/{id}/auto-on](#post-apigraphicsidauto-on)
  - [POST /api/graphics/{id}/auto-off](#post-apigraphicsidauto-off)
  - [POST /api/graphics/{id}/frame](#post-apigraphicsidframe)
  - [POST /api/graphics/{id}/animate](#post-apigraphicsidanimate)
  - [POST /api/graphics/{id}/animate/stop](#post-apigraphicsidanimatestop)
  - [PUT /api/graphics/{id}/rect](#put-apigraphicsidrect)
  - [PUT /api/graphics/{id}/zorder](#put-apigraphicsidzorder)
  - [POST /api/graphics/{id}/fly-in](#post-apigraphicsidfly-in)
  - [POST /api/graphics/{id}/fly-out](#post-apigraphicsidfly-out)
  - [POST /api/graphics/{id}/slide](#post-apigraphicsidslide)
  - [POST /api/graphics/{id}/fly-on](#post-apigraphicsidfly-on)
  - [POST /api/graphics/{id}/image](#post-apigraphicsidimage)
  - [GET /api/graphics/{id}/image](#get-apigraphicsidimage)
  - [DELETE /api/graphics/{id}/image](#delete-apigraphicsidimage)
  - [POST /api/graphics/{id}/text-animate](#post-apigraphicsidtext-animate)
  - [POST /api/graphics/{id}/text-animate/stop](#post-apigraphicsidtext-animatestop)
  - [POST /api/graphics/{id}/ticker](#post-apigraphicsidticker)
  - [POST /api/graphics/{id}/ticker/stop](#post-apigraphicsidtickerstop)
  - [PUT /api/graphics/{id}/ticker/text](#put-apigraphicsidtickertext)
- [HTML5 Graphics](#html5-graphics)
  - [GET /api/graphics/html5/templates](#get-apigraphicshtml5templates)
  - [POST /api/graphics/{id}/html5](#post-apigraphicsidhtml5)
  - [PUT /api/graphics/{id}/html5/data](#put-apigraphicsidhtml5data)
  - [POST /api/graphics/{id}/html5/play](#post-apigraphicsidhtml5play)
  - [POST /api/graphics/{id}/html5/stop](#post-apigraphicsidhtml5stop)
  - [POST /api/graphics/{id}/html5/next](#post-apigraphicsidhtml5next)
  - [POST /api/graphics/{id}/html5/custom](#post-apigraphicsidhtml5custom)
  - [DELETE /api/graphics/{id}/html5](#delete-apigraphicsidhtml5)
- [DVE (Multi-Source Compositor)](#dve-multi-source-compositor)
  - [GET /api/dve](#get-apidve)
  - [PUT /api/dve](#put-apidve)
  - [DELETE /api/dve](#delete-apidve)
  - [PUT /api/dve/slots/{id}](#put-apidveslotsid)
  - [POST /api/dve/slots/{id}/on](#post-apidveslotsidon)
  - [POST /api/dve/slots/{id}/off](#post-apidveslotsidoff)
  - [GET /api/dve/presets](#get-apidvepresets)
  - [POST /api/dve/presets](#post-apidvepresets)
  - [PUT /api/dve/presets/{id}/recall](#put-apidvepresetsidrrecall)
  - [DELETE /api/dve/presets/{id}](#delete-apidvepresetsid)
- [Presets](#presets)
  - [GET /api/presets](#get-apipresets)
  - [POST /api/presets](#post-apipresets)
  - [GET /api/presets/{id}](#get-apipresetsid)
  - [PUT /api/presets/{id}](#put-apipresetsid)
  - [DELETE /api/presets/{id}](#delete-apipresetsid)
  - [POST /api/presets/{id}/recall](#post-apipresetsidrecall)
- [Confidence Monitor](#confidence-monitor)
  - [GET /api/output/confidence](#get-apioutputconfidence)
  - [GET /api/output/cbr](#get-apioutputcbr)
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
  - [POST /api/replay/quick](#post-apireplayquick)
  - [POST /api/replay/pause](#post-apireplaypause)
  - [POST /api/replay/resume](#post-apireplayresume)
  - [PATCH /api/replay/seek](#patch-apireplayseek)
  - [PATCH /api/replay/speed](#patch-apireplayspeed)
  - [PATCH /api/replay/marks](#patch-apireplaymarks)
  - [GET /api/replay/peek](#get-apireplaypeek)
  - [PUT /api/replay/shutter-angle](#put-apireplayshutter-angle)
  - [GET /api/replay/shutter-angle](#get-apireplayshutter-angle)
- [Macros](#macros)
  - [GET /api/macros](#get-apimacros)
  - [GET /api/macros/{name}](#get-apimacrosname)
  - [PUT /api/macros/{name}](#put-apimacrosname)
  - [DELETE /api/macros/{name}](#delete-apimacrosname)
  - [POST /api/macros/{name}/run](#post-apimacrosnamerun)
  - [DELETE /api/macros/execution](#delete-apimacrosexecution)
  - [POST /api/macros/execution/cancel](#post-apimacrosexecutioncancel)
- [Operators](#operators)
  - [POST /api/operator/register](#post-apioperatorregister)
  - [POST /api/operator/reconnect](#post-apioperatorreconnect)
  - [POST /api/operator/heartbeat](#post-apioperatorheartbeat)
  - [GET /api/operator/list](#get-apioperatorlist)
  - [POST /api/operator/lock](#post-apioperatorlock)
  - [POST /api/operator/unlock](#post-apioperatorunlock)
  - [POST /api/operator/force-unlock](#post-apioperatorforceunlock)
  - [DELETE /api/operator/{id}](#delete-apioperatorid)
  - [GET /api/operator/invite-tokens](#get-apioperatorinvite-tokens)
- [Operator Comms](#operator-comms)
  - [POST /api/comms/join](#post-apicommsjoin)
  - [POST /api/comms/leave](#post-apicommsleave)
  - [PUT /api/comms/mute](#put-apicommsmute)
  - [GET /api/comms/status](#get-apicommsstatus)
- [SCTE-35 Ad Insertion](#scte-35-ad-insertion)
  - [POST /api/scte35/cue](#post-apiscte35cue)
  - [POST /api/scte35/return](#post-apiscte35return)
  - [POST /api/scte35/return/{eventId}](#post-apiscte35returneventid)
  - [POST /api/scte35/cancel/{eventId}](#post-apiscte35canceleventid)
  - [POST /api/scte35/cancel-segmentation/{segEventId}](#post-apiscte35cancel-segmentationsegeventid)
  - [POST /api/scte35/hold/{eventId}](#post-apiscte35holdeventid)
  - [POST /api/scte35/extend/{eventId}](#post-apiscte35extendeventid)
  - [GET /api/scte35/status](#get-apiscte35status)
  - [GET /api/scte35/log](#get-apiscte35log)
  - [GET /api/scte35/active](#get-apiscte35active)
  - [GET /api/scte35/rules](#get-apiscte35rules)
  - [POST /api/scte35/rules](#post-apiscte35rules)
  - [PUT /api/scte35/rules/{id}](#put-apiscte35rulesid)
  - [DELETE /api/scte35/rules/{id}](#delete-apiscte35rulesid)
  - [PUT /api/scte35/rules/default](#put-apiscte35rulesdefault)
  - [POST /api/scte35/rules/reorder](#post-apiscte35rulesreorder)
  - [GET /api/scte35/rules/templates](#get-apiscte35rulestemplates)
  - [POST /api/scte35/rules/from-template](#post-apiscte35rulesfrom-template)
- [Captions](#captions)
  - [POST /api/captions/mode](#post-apicaptionsmode)
  - [POST /api/captions/text](#post-apicaptionstext)
  - [GET /api/captions/state](#get-apicaptionsstate)
- [Clips](#clips)
  - [GET /api/clips](#get-apiclips)
  - [GET /api/clips/{id}](#get-apiclipsid)
  - [POST /api/clips/upload](#post-apiclipsupload)
  - [PUT /api/clips/{id}](#put-apiclipsid)
  - [DELETE /api/clips/{id}](#delete-apiclipsid)
  - [POST /api/clips/{id}/pin](#post-apiclipsidpin)
  - [GET /api/clips/recordings](#get-apiclipsrecordings)
  - [POST /api/clips/from-recording](#post-apiclipsfrom-recording)
  - [GET /api/clips/players](#get-apiclipsplayers)
  - [POST /api/clips/players/{n}/load](#post-apiclipsplayersnload)
  - [POST /api/clips/players/{n}/eject](#post-apiclipsplayersneject)
  - [POST /api/clips/players/{n}/play](#post-apiclipsplayersnplay)
  - [POST /api/clips/players/{n}/pause](#post-apiclipsplayersnpause)
  - [POST /api/clips/players/{n}/stop](#post-apiclipsplayersnstop)
  - [POST /api/clips/players/{n}/seek](#post-apiclipsplayersnseek)
  - [POST /api/clips/players/{n}/speed](#post-apiclipsplayersnspeed)
  - [POST /api/clips/players/{n}/loop](#post-apiclipsplayersnloop)
- [ST Maps](#st-maps)
  - [GET /api/stmap](#get-apistmap)
  - [GET /api/stmap/state](#get-apistmapstate)
  - [GET /api/stmap/generators](#get-apistmapgenerators)
  - [POST /api/stmap/generate](#post-apistmapgenerate)
  - [POST /api/stmap/upload/{name}](#post-apistmapuploadname)
  - [GET /api/stmap/{name}](#get-apistmapname)
  - [DELETE /api/stmap/{name}](#delete-apistmapname)
  - [GET /api/stmap/{name}/download](#get-apistmapnamedownload)
  - [PUT /api/stmap/source/{sourceKey}](#put-apistmapsourcesourcekey)
  - [DELETE /api/stmap/source/{sourceKey}](#delete-apistmapsourcesourcekey)
  - [PUT /api/stmap/program](#put-apistmapprogram)
  - [DELETE /api/stmap/program](#delete-apistmapprogram)
- [Format](#format)
  - [GET /api/format](#get-apiformat)
  - [PUT /api/format](#put-apiformat)
- [Encoder](#encoder)
  - [GET /api/encoder](#get-apiencoder)
  - [PUT /api/encoder](#put-apiencoder)
- [ASR (Automatic Speech Recognition)](#asr-automatic-speech-recognition)
  - [GET /api/asr/status](#get-apiasrstatus)
  - [PUT /api/asr/config](#put-apiasrconfig)
  - [GET /api/asr/models](#get-apiasrmodels)
- [Color Grading](#color-grading)
  - [GET /api/colorgrade](#get-apicolorgrade)
  - [PUT /api/colorgrade](#put-apicolorgrade)
  - [POST /api/colorgrade/upload](#post-apicolorgradeupload)
  - [DELETE /api/colorgrade/{name}](#delete-apicolorgradename)
  - [GET /api/colorgrade/corrector](#get-apicolorgradecorrector)
  - [PUT /api/colorgrade/corrector](#put-apicolorgradecorrector)
  - [DELETE /api/colorgrade/corrector](#delete-apicolorgradecorrector)
  - [POST /api/colorgrade/save](#post-apicolorgradesave)
  - [GET /api/colorgrade/looks/{name}/settings](#get-apicolorgradelooksnamesettings)
- [AI Background Segmentation](#ai-background-segmentation)
  - [GET /api/ai-segment/status](#get-apiai-segmentstatus)
  - [PUT /api/sources/{source}/ai-segment](#put-apisourcessourceai-segment)
  - [GET /api/sources/{source}/ai-segment](#get-apisourcessourceai-segment)
  - [DELETE /api/sources/{source}/ai-segment](#delete-apisourcessourceai-segment)
- [Debug](#debug)
  - [GET /api/debug/snapshot](#get-apidebugsnapshot)
- [Performance](#performance)
  - [GET /api/perf](#get-apiperf)
  - [POST /api/perf/baseline](#post-apiperfbaseline)
  - [DELETE /api/perf/baseline/{name}](#delete-apiperfbaselinename)
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
  "transitionEasing": "smoothstep",
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
      "gainReduction": 0.0,
      "audioDelayMs": 80
    }
  },
  "masterLevel": 0.0,
  "programPeak": [-18.5, -19.2],
  "gainReduction": 0.0,
  "momentaryLufs": -23.1,
  "shortTermLufs": -22.8,
  "integratedLufs": -23.0,
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
    "droppedPackets": 0
  },
  "srtOutput": {
    "active": true,
    "mode": "caller",
    "address": "ingest.example.com",
    "port": 9000,
    "state": "active",
    "connections": 1,
    "bytesWritten": 104857600,
    "droppedPackets": 0,
    "overflowCount": 0
  },
  "destinations": [
    { "id": "d1", "name": "YouTube", "type": "srt-caller", "address": "ingest.yt.com", "port": 9000, "state": "active" }
  ],
  "sources": {
    "cam1": { "key": "cam1", "label": "Stage Left", "type": "demo", "status": "healthy", "position": 1 },
    "cam2": { "key": "cam2", "label": "Stage Right", "type": "demo", "status": "healthy", "position": 2, "delayMs": 100 },
    "srt:encoder1": {
      "key": "srt:encoder1", "label": "Remote Encoder", "type": "srt", "status": "healthy", "position": 3,
      "srt": {
        "mode": "caller", "streamID": "live/encoder1", "remoteAddr": "192.168.1.100:6464",
        "latencyMs": 200, "rttMs": 2.5, "lossRate": 0.001, "bitrateKbps": 5000.0, "recvBufMs": 245.0, "connected": true
      }
    }
  },
  "presets": [
    { "id": "550e8400-e29b-41d4-a716-446655440000", "name": "Opening" }
  ],
  "graphics": {
    "layers": [
      {
        "id": 0,
        "active": true,
        "template": "lower-third",
        "fadePosition": 1.0,
        "zOrder": 0,
        "x": 0, "y": 0, "width": 1920, "height": 1080
      }
    ]
  },
  "layout": {
    "activePreset": "pip-bottom-right",
    "slots": [
      { "id": 0, "sourceKey": "cam2", "enabled": true, "x": 1440, "y": 720, "width": 480, "height": 360, "zOrder": 1 }
    ]
  },
  "replay": {
    "state": "idle",
    "buffers": [
      { "source": "cam1", "frameCount": 1800, "gopCount": 60, "durationSecs": 60.0, "bytesUsed": 52428800 }
    ]
  },
  "pipelineFormat": {
    "width": 1920, "height": 1080, "fpsNum": 30000, "fpsDen": 1001, "name": "1080p29.97"
  },
  "scte35": {
    "enabled": true,
    "activeEvents": {},
    "eventLog": [],
    "heartbeatOk": true,
    "config": { "heartbeatIntervalMs": 5000, "defaultPreRollMs": 4000, "pid": 258, "verifyEncoding": false }
  },
  "captions": {
    "mode": "passthrough"
  },
  "macro": null,
  "operators": [
    { "id": "op_abc123", "name": "Director", "role": "director", "connected": true },
    { "id": "op_def456", "name": "Audio Eng", "role": "audio", "connected": true }
  ],
  "locks": {
    "audio": { "holderId": "op_def456", "holderName": "Audio Eng", "acquiredAt": 1709654300000 }
  },
  "comms": {
    "active": true,
    "participants": [
      { "operatorId": "op_abc123", "name": "Director", "muted": false, "speaking": true },
      { "operatorId": "op_def456", "name": "Audio Eng", "muted": false, "speaking": false }
    ]
  },
  "lastChangedBy": "Director",
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
| `transitionDurationMs` | `int` | Default transition duration in milliseconds. Omitted when `0`. |
| `transitionPosition` | `float` | Current T-bar position during a transition (`0.0` to `1.0`). Omitted when `0`. |
| `transitionEasing` | `string` | Easing curve name for transitions. Omitted when not set. |
| `inTransition` | `bool` | `true` while a dissolve/dip/wipe transition is in progress. Omitted when `false`. |
| `ftbActive` | `bool` | `true` while Fade to Black is active. Omitted when `false`. |
| `audioChannels` | `object` | Map of source key to `AudioChannel` state |
| `masterLevel` | `float` | Master output level in dB |
| `programPeak` | `[float, float]` | Stereo peak levels in dBFS for the program output `[left, right]` |
| `gainReduction` | `float` | Brickwall limiter gain reduction in dB. Omitted when `0`. |
| `momentaryLufs` | `float` | BS.1770-4 momentary loudness in LUFS (400ms window). Omitted when `0`. |
| `shortTermLufs` | `float` | BS.1770-4 short-term loudness in LUFS (3s window). Omitted when `0`. |
| `integratedLufs` | `float` | BS.1770-4 integrated loudness in LUFS (dual-gated). Omitted when `0`. |
| `tallyState` | `object` | Map of source key to tally status: `"program"`, `"preview"`, or `"idle"` |
| `recording` | `object` or `null` | Recording status. Omitted when not configured. |
| `srtOutput` | `object` or `null` | SRT output status. Omitted when not active. |
| `destinations` | `array` | List of `DestinationInfo` for multi-destination SRT outputs. Omitted when empty. |
| `sources` | `object` | Map of source key to `SourceInfo` |
| `presets` | `array` | List of saved preset summaries `[{id, name}]`. Omitted when empty. |
| `graphics` | `object` or `null` | Multi-layer graphics state with `layers` array. See [GraphicsState Fields](#graphicsstate-fields). |
| `layout` | `object` or `null` | DVE multi-source compositor state. Omitted when no DVE layout is active. |
| `replay` | `object` or `null` | Instant replay state. Omitted when replay manager is not configured. |
| `pipelineFormat` | `object` or `null` | Current video pipeline format (`width`, `height`, `fpsNum`, `fpsDen`, `name`). Omitted when not set. |
| `scte35` | `object` or `null` | SCTE-35 ad insertion state. Omitted when SCTE-35 is not enabled. |
| `captions` | `object` or `null` | Closed caption state (`mode`, `authorBuffer`, `sourceCaptions`). Omitted when captions not enabled. |
| `macro` | `object` or `null` | Macro execution state (`running`, `macroName`, `steps`, `currentStep`). `null` when no macro has run. |
| `operators` | `array` | List of registered operators. Omitted when empty. |
| `locks` | `object` | Map of subsystem name to `LockInfo`. Omitted when no locks are held. |
| `comms` | `object` or `null` | Operator voice comms channel state. See [CommsState](#commsstate). Omitted when comms not active. |
| `lastChangedBy` | `string` | Name of the operator who last made a change. Omitted when not set. |
| `seq` | `int` | Monotonically increasing sequence number |
| `timestamp` | `int` | Unix timestamp in milliseconds |

### SourceInfo

| Field | Type | Description |
|-------|------|-------------|
| `key` | `string` | Unique identifier for the source (e.g., `"cam1"`, `"srt:encoder1"`) |
| `label` | `string` | Human-readable label. Omitted if not set. |
| `type` | `string` | Source type: `"demo"`, `"mxl"`, `"srt"`, `"replay"`, or `"clip"` |
| `status` | `string` | Health status: `"healthy"`, `"stale"`, `"no_signal"`, or `"offline"` |
| `position` | `int` | Display position index (1-based). Always present. |
| `delayMs` | `int` | Input delay in milliseconds. Omitted when `0`. |
| `srt` | `SRTSourceInfo` or `null` | SRT connection metadata. Present only for SRT sources. |
| `keyConfig` | `object` or `null` | Upstream key configuration. Omitted when no key is configured. See [Source Keying](#put-apisourceskeykey). |
| `isVirtual` | `bool` | `true` for virtual sources (e.g., replay). Omitted when `false`. |
| `hasCaptions` | `bool` | `true` when this source has upstream captions. Omitted when `false`. |

### SRTSourceInfo

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `string` | Connection mode: `"listener"` (push) or `"caller"` (pull) |
| `streamID` | `string` | SRT stream identifier |
| `remoteAddr` | `string` | Remote encoder address (`IP:port`). Omitted when not connected. |
| `latencyMs` | `int` | Configured SRT latency in milliseconds |
| `rttMs` | `float` | Round-trip time in milliseconds |
| `lossRate` | `float` | Packet loss ratio (`0.0` to `1.0`) |
| `bitrateKbps` | `float` | Receive bitrate in Kbps |
| `recvBufMs` | `float` | Receive buffer fill level in milliseconds |
| `connected` | `bool` | Whether the SRT connection is currently active |

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
| `audioDelayMs` | `int` | Lip-sync audio delay in milliseconds (0-500). Omitted when `0`. |

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
| `state` | `string` | Player state: `"idle"`, `"loading"`, `"playing"`, or `"paused"` |
| `source` | `string` | Source key being played. Omitted when idle. |
| `speed` | `float` | Playback speed (`0.25` to `1.0`). Omitted when idle. |
| `paused` | `bool` | Whether playback is currently paused. Omitted when `false`. |
| `loop` | `bool` | Whether playback loops. Omitted when idle. |
| `position` | `float` | Playback progress from `0.0` to `1.0`. Omitted when idle. |
| `progress` | `float` | Normalized playback progress from `0.0` to `1.0`. Alias for `position`. Omitted when idle. |
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

### Info

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique operator identifier |
| `name` | `string` | Operator display name |
| `role` | `string` | Operator role: `"director"`, `"audio"`, `"graphics"`, `"captioner"`, or `"viewer"` |
| `connected` | `bool` | Whether the operator has an active session |

### LockInfo

| Field | Type | Description |
|-------|------|-------------|
| `holderId` | `string` | Operator ID holding the lock |
| `holderName` | `string` | Operator name holding the lock |
| `acquiredAt` | `int` | Unix timestamp in milliseconds when the lock was acquired |

### CommsState

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | `true` when the comms channel has at least one participant |
| `participants` | `array` | List of `CommsParticipant` objects. Omitted when empty. |

### CommsParticipant

| Field | Type | Description |
|-------|------|-------------|
| `operatorId` | `string` | Operator ID of the participant |
| `name` | `string` | Display name of the participant |
| `muted` | `bool` | Whether the participant is muted |
| `speaking` | `bool` | Whether the participant is currently speaking (voice activity detection) |

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
  "wipeDirection": "h-left",
  "easing": { "type": "smoothstep" }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `source` | `string` | Yes | Key of the source to transition to |
| `type` | `string` | Yes | Transition type: `"mix"`, `"dip"`, `"wipe"`, or `"stinger"` |
| `durationMs` | `int` | Yes | Duration in milliseconds. Must be `100`-`5000`. |
| `wipeDirection` | `string` | Wipe only | Direction for wipe transitions. Required when `type` is `"wipe"`. |
| `stingerName` | `string` | Stinger only | Name of the loaded stinger clip. Required when `type` is `"stinger"`. |
| `easing` | `object` | No | Easing curve configuration. Defaults to `smoothstep`. |

**Easing object fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | Yes | Easing type: `"linear"`, `"ease"`, `"ease-in"`, `"ease-out"`, `"ease-in-out"`, `"smoothstep"`, or `"custom"` |
| `x1` | `float` | Custom only | Cubic bezier control point X1. Required when `type` is `"custom"`. |
| `y1` | `float` | Custom only | Cubic bezier control point Y1. Required when `type` is `"custom"`. |
| `x2` | `float` | Custom only | Cubic bezier control point X2. Required when `type` is `"custom"`. |
| `y2` | `float` | Custom only | Cubic bezier control point Y2. Required when `type` is `"custom"`. |

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
| `400` | Invalid `type`, `durationMs` out of range, source already on program, invalid `wipeDirection`, invalid easing type/params, or missing `stingerName` for stinger type |
| `404` | Source not found, or stinger clip not found |
| `409` | Another transition or FTB is already active |
| `501` | Stinger store not configured (when `type` is `"stinger"`) |

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

**Request Body:** None (body is ignored if sent)

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
    "type": "demo",
    "status": "healthy",
    "position": 1
  },
  "cam2": {
    "key": "cam2",
    "type": "demo",
    "status": "healthy",
    "position": 2,
    "delayMs": 100
  },
  "srt:encoder1": {
    "key": "srt:encoder1",
    "label": "Remote Encoder",
    "type": "srt",
    "status": "healthy",
    "position": 3,
    "srt": {
      "mode": "caller",
      "streamID": "live/encoder1",
      "remoteAddr": "192.168.1.100:6464",
      "latencyMs": 200,
      "rttMs": 2.5,
      "lossRate": 0.001,
      "bitrateKbps": 5000.0,
      "recvBufMs": 245.0,
      "connected": true
    }
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

### GET /api/sources/{key}

Get a single source by key.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`, `srt:encoder1`) |

**Request Body:** None

**Response:** `200 OK` with `SourceInfo`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Source not found |

**Example:**

```bash
curl http://localhost:8081/api/sources/srt:encoder1 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/sources

Create an SRT input source in caller (pull) mode. The server initiates an outbound SRT connection to the specified address and begins ingesting video/audio.

Listener (push) mode sources are created automatically when an encoder pushes to the SRT listener port (default `:6464`). Only caller mode sources can be created via this endpoint.

**Request Body:**

```json
{
  "type": "srt",
  "mode": "caller",
  "address": "srt://192.168.1.100:6464",
  "streamID": "live/camera1",
  "label": "Remote Camera",
  "latencyMs": 200
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | Yes | Must be `"srt"` |
| `mode` | `string` | Yes | Must be `"caller"` |
| `address` | `string` | Yes | Remote SRT address (e.g., `srt://host:port` or `host:port`) |
| `streamID` | `string` | Yes | SRT stream identifier. Used to derive the source key. |
| `label` | `string` | No | Human-readable label for the source |
| `latencyMs` | `int` | No | SRT latency in milliseconds (0-10000). Default: server `--srt-latency` value. |

**Source Key Derivation:**

The source key is derived from `streamID` with the `srt:` prefix:
- `live/camera1` → `srt:camera1`
- `encoder/main` → `srt:main`
- `#!::r=live/cam1` (SRT Access Control format) → `srt:cam1`

**Response:** `201 Created`

```json
{
  "key": "srt:camera1"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing required fields, invalid type/mode, malformed JSON, or invalid latency range |
| `500` | SRT connection error |
| `501` | SRT not configured (server started without `--srt-listen`) |

**Caller Reconnection:**

Once created, the caller maintains the connection with automatic reconnection:
- Exponential backoff: 1s → 2s → 4s → ... → 30s max
- ±25% jitter on each backoff interval
- Source stays visible as `"no_signal"` during reconnection
- Configuration persists across server restarts (`~/.switchframe/srt_sources.json`)

**Example:**

```bash
curl -X POST http://localhost:8081/api/sources \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"type": "srt", "mode": "caller", "address": "srt://192.168.1.100:6464", "streamID": "live/camera1", "label": "Remote Camera", "latencyMs": 200}'
```

---

### DELETE /api/sources/{key}

Remove an SRT input source. Only SRT caller (pull) mode sources can be deleted via API. Listener (push) mode sources are managed by the encoder connection lifecycle. Non-SRT sources (demo, MXL, replay, clip) cannot be deleted.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `srt:camera1`) |

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Source not found |
| `405` | Attempt to delete a non-SRT source |
| `501` | SRT not configured |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/sources/srt:camera1 \
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
| `position` | `int` | Yes | Display position index (1-based, must be >= 1) |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or invalid position (must be >= 1) |
| `404` | Source not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/sources/cam1/position \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"position": 2}'
```

---

### GET /api/sources/{key}/srt/stats

Get detailed SRT connection statistics for a source. Only available for SRT sources.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | SRT source key (e.g., `srt:encoder1`) |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "mode": "caller",
  "streamID": "live/encoder1",
  "remoteAddr": "192.168.1.100:6464",
  "state": "connected",
  "connected": true,
  "uptimeMs": 3600000,
  "latencyMs": 200,
  "negotiatedLatencyMs": 200,
  "rttMs": 2.5,
  "rttVarMs": 0.8,
  "recvRateMbps": 4.95,
  "lossRatePct": 0.01,
  "packetsReceived": 1234567,
  "packetsLost": 12,
  "packetsDropped": 0,
  "packetsRetransmitted": 5,
  "packetsBelated": 0,
  "recvBufMs": 245.0,
  "recvBufPackets": 98,
  "flightSize": 156,
  "reconnectCount": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `string` | `"listener"` or `"caller"` |
| `streamID` | `string` | SRT stream identifier |
| `remoteAddr` | `string` | Remote encoder address |
| `state` | `string` | Connection state: `"connected"` or `"disconnected"` |
| `connected` | `bool` | Whether the connection is active |
| `uptimeMs` | `int` | Time since source creation in milliseconds |
| `latencyMs` | `int` | Configured SRT latency |
| `negotiatedLatencyMs` | `int` | Negotiated SRT latency (may differ from configured) |
| `rttMs` | `float` | Round-trip time in milliseconds |
| `rttVarMs` | `float` | RTT variance in milliseconds |
| `recvRateMbps` | `float` | Receive bitrate in Mbps |
| `lossRatePct` | `float` | Packet loss percentage (0-100) |
| `packetsReceived` | `int` | Total packets received |
| `packetsLost` | `int` | Total packets lost |
| `packetsDropped` | `int` | Total packets dropped (too late) |
| `packetsRetransmitted` | `int` | Total packets retransmitted |
| `packetsBelated` | `int` | Total belated packets |
| `recvBufMs` | `float` | Receive buffer fill level in milliseconds |
| `recvBufPackets` | `int` | Receive buffer fill level in packets |
| `flightSize` | `int` | Packets in flight |
| `reconnectCount` | `int` | Number of reconnections since source creation |

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Source not found or not an SRT source |
| `501` | SRT not configured |

**Example:**

```bash
curl http://localhost:8081/api/sources/srt:encoder1/srt/stats \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/sources/{key}/srt

Update the SRT configuration for a source. Currently supports changing the SRT latency. The change takes effect on the next connection (caller mode reconnects automatically).

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | SRT source key (e.g., `srt:encoder1`) |

**Request Body:**

```json
{
  "latencyMs": 300
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `latencyMs` | `int` | Yes | SRT latency in milliseconds. Range: `0`-`10000`. |

**Response:** `200 OK`

```json
{
  "status": "ok"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or `latencyMs` out of range |
| `404` | Source not found or not an SRT source |
| `501` | SRT not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/sources/srt:encoder1/srt \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"latencyMs": 300}'
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
| `spillReplaceCb` | `uint8` | No | Cb value for spill replacement (default 128 = neutral). Omitted when `0`. |
| `spillReplaceCr` | `uint8` | No | Cr value for spill replacement (default 128 = neutral). Omitted when `0`. |
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

### GET /api/sources/{key}/corrector

Get the per-source color corrector state. Each source can have an independent color corrector applied before the mix point, allowing per-camera color matching.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "lift": [0, 0, 0],
  "gamma": [1, 1, 1],
  "gain": [1, 1, 1],
  "temperature": 0,
  "tint": 0,
  "saturation": 1,
  "contrast": 0,
  "shadows": [0, 0, 0],
  "midtones": [0, 0, 0],
  "highlights": [0, 0, 0],
  "sharpness": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `lift` | `[3]float` | RGB lift (shadows), -1 to +1 |
| `gamma` | `[3]float` | RGB gamma (midtones), 0.1 to 4.0 |
| `gain` | `[3]float` | RGB gain (highlights), 0 to 4.0 |
| `temperature` | `float` | Color temperature shift, -100 to +100 |
| `tint` | `float` | Green-magenta tint, -100 to +100 |
| `saturation` | `float` | Saturation, 0 to 2.0 |
| `contrast` | `float` | Contrast, -1 to +1 |
| `shadows` | `[3]float` | RGB shadow adjustments, -1 to +1 |
| `midtones` | `[3]float` | RGB midtone adjustments, -1 to +1 |
| `highlights` | `[3]float` | RGB highlight adjustments, -1 to +1 |
| `curveMaster` | `array` | Master tone curve points (optional) |
| `curveR` | `array` | Red channel curve points (optional) |
| `curveG` | `array` | Green channel curve points (optional) |
| `curveB` | `array` | Blue channel curve points (optional) |
| `sharpness` | `float` | Sharpness, 0 to 1.0 |

**Example:**

```bash
curl http://localhost:8081/api/sources/cam1/corrector \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/sources/{key}/corrector

Set the per-source color corrector state. Replaces the full corrector state (partial updates are not supported).

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:** Full `CorrectorState` object (see GET response for field descriptions).

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or corrector values out of range |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/sources/cam1/corrector \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lift": [0, 0, 0], "gamma": [1.1, 1.0, 0.9], "gain": [1, 1, 1], "temperature": 10, "tint": 0, "saturation": 1.1, "contrast": 0.05, "shadows": [0, 0, 0], "midtones": [0, 0, 0], "highlights": [0, 0, 0], "sharpness": 0.3}'
```

---

### DELETE /api/sources/{key}/corrector

Reset the per-source color corrector to defaults (neutral, no color change).

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `key` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`.

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/sources/cam1/corrector \
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

### PUT /api/audio/{source}/audio-delay

Set the audio delay in milliseconds for a source channel. Used for lip-sync correction in multi-camera setups where audio and video from different sources may be offset.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:**

```json
{
  "delayMs": 80
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `delayMs` | `int` | Yes | Audio delay in milliseconds (0-500) |

**Response:** `200 OK` with updated `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing source or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/cam1/audio-delay \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"delayMs": 80}'
```

---

### POST /api/audio/phase

Invert the phase (polarity) of a source's audio channel. Phase inversion is useful for correcting microphone wiring issues or cancelling bleed between closely-placed microphones.

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `source` | `string` | Source key (e.g., `cam1`) |
| `invert` | `bool` | `true` to invert phase, `false` for normal polarity |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing source or invalid JSON |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/phase \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "invert": true}'
```

---

### POST /api/audio/balance

Set the stereo balance for a source's audio channel. Balance pans the signal between left and right output channels.

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `source` | `string` | Source key (e.g., `cam1`) |
| `balance` | `float` | Stereo balance from `-1.0` (full left) to `1.0` (full right). `0.0` is center. |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing source, invalid JSON, or balance out of range |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X POST http://localhost:8081/api/audio/balance \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "balance": -0.5}'
```

---

### PUT /api/audio/{source}/hpf

Configure the high-pass filter (HPF) for a source channel. The HPF removes low-frequency rumble, handling noise, and HVAC hum from microphone inputs.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `frequency` | `float` | Cutoff frequency in Hz |
| `enabled` | `bool` | Whether the HPF is active |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing source, invalid JSON, or frequency out of range |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/cam1/hpf \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"frequency": 80, "enabled": true}'
```

---

### GET /api/audio/{source}/hpf

Get the current high-pass filter settings for a source channel.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "frequency": 80,
  "enabled": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `frequency` | `float` | Cutoff frequency in Hz |
| `enabled` | `bool` | Whether the HPF is active |

**Example:**

```bash
curl http://localhost:8081/api/audio/cam1/hpf \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/audio/{source}/gate

Configure the noise gate for a source channel. The gate attenuates audio below a threshold, reducing background noise during pauses in speech.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Gate open threshold in dBFS |
| `range` | `float` | Attenuation depth in dB when gate is closed |
| `attack` | `float` | Attack time in milliseconds |
| `release` | `float` | Release time in milliseconds |
| `hold` | `float` | Hold time in milliseconds (keeps gate open after signal drops below threshold) |
| `enabled` | `bool` | Whether the gate is active |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing source, invalid JSON, or parameters out of range |
| `404` | Source audio channel not found |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/cam1/gate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"threshold": -40, "range": -80, "attack": 0.5, "release": 50, "hold": 10, "enabled": true}'
```

---

### GET /api/audio/{source}/gate

Get the current noise gate settings and gain reduction for a source channel.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `cam1`) |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "threshold": -40,
  "range": -80,
  "attack": 0.5,
  "release": 50,
  "hold": 10,
  "enabled": true,
  "gainReduction": -12.5
}
```

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Gate open threshold in dBFS |
| `range` | `float` | Attenuation depth in dB |
| `attack` | `float` | Attack time in ms |
| `release` | `float` | Release time in ms |
| `hold` | `float` | Hold time in ms |
| `enabled` | `bool` | Whether the gate is active |
| `gainReduction` | `float` | Current gain reduction in dB (live metering value) |

**Example:**

```bash
curl http://localhost:8081/api/audio/cam1/gate \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/audio/limiter

Configure the master output limiter. The limiter prevents program audio from exceeding a ceiling level, protecting downstream equipment and broadcast compliance.

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Limiter threshold in dBFS |
| `ceiling` | `float` | Output ceiling in dBFS |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or parameters out of range |
| `501` | Audio mixer not configured |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/audio/limiter \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"threshold": -3, "ceiling": -1}'
```

---

### GET /api/audio/limiter

Get the current master limiter settings and gain reduction.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "threshold": -3,
  "ceiling": -1,
  "gainReduction": -1.2
}
```

| Field | Type | Description |
|-------|------|-------------|
| `threshold` | `float` | Limiter threshold in dBFS |
| `ceiling` | `float` | Output ceiling in dBFS |
| `gainReduction` | `float` | Current gain reduction in dB (live metering value) |

**Example:**

```bash
curl http://localhost:8081/api/audio/limiter \
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
  "durationSecs": 0.0,
  "droppedPackets": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | Whether recording is in progress |
| `filename` | `string` | Current output filename. Omitted when not recording. |
| `bytesWritten` | `int` | Bytes written to current file. Omitted when `0`. |
| `durationSecs` | `float` | Recording duration in seconds. Omitted when `0`. |
| `droppedPackets` | `int` | Number of dropped packets. Omitted when `0`. |
| `error` | `string` | Error message if recording failed. Omitted when empty. |

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

**Response:** `200 OK` with `SRTStatus`:

```json
{
  "active": false,
  "mode": "caller",
  "address": "ingest.example.com",
  "port": 9000,
  "state": "starting",
  "connections": 0,
  "bytesWritten": 0,
  "droppedPackets": 0,
  "overflowCount": 0
}
```

> **Note:** The `active` field is `true` only when `state` is `"active"` or `"reconnecting"`. During `"starting"`, `active` is `false`.

| Field | Type | Description |
|-------|------|-------------|
| `active` | `bool` | `true` when state is `"active"` or `"reconnecting"` |
| `mode` | `string` | `"caller"` or `"listener"`. Omitted when not active. |
| `address` | `string` | Remote address (caller mode). Omitted when not set. |
| `port` | `int` | Port number. Omitted when `0`. |
| `state` | `string` | Connection state: `"starting"`, `"active"`, `"reconnecting"`, `"stopped"`. Omitted when not set. |
| `connections` | `int` | Number of active connections (listener mode). Omitted when `0`. |
| `bytesWritten` | `int` | Total bytes written. Omitted when `0`. |
| `droppedPackets` | `int` | Number of dropped packets. Omitted when `0`. |
| `overflowCount` | `int` | Number of ring buffer overflows (caller mode). Omitted when `0`. |
| `error` | `string` | Error message if SRT failed. Omitted when empty. |

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

**Response:** `200 OK` with `SRTStatus`:

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

**Response:** `200 OK` with `SRTStatus`:

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

### SRTStatus Fields

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
  "type": "srt-caller",
  "address": "ingest.example.com",
  "port": 9000,
  "latency": 200,
  "streamID": "live/stream1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | No | Human-readable name for the destination |
| `type` | `string` | Yes | `"srt-caller"` (push) or `"srt-listener"` (pull) |
| `address` | `string` | Caller only | Remote hostname or IP |
| `port` | `int` | Yes | Port number |
| `latency` | `int` | No | SRT latency in milliseconds |
| `streamID` | `string` | No | SRT stream ID |
| `encryption` | `string` | No | SRT encryption mode. Omitted when not set. |
| `passphrase` | `string` | No | SRT passphrase for encryption. Omitted when not set. |
| `maxBandwidth` | `int` | No | Maximum bandwidth in bytes/sec. Omitted when `0`. |
| `maxConns` | `int` | No | Maximum listener connections. Omitted when `0`. |
| `scte35Enabled` | `bool` | No | Enable SCTE-35 for this destination. Omitted when `false`. |

**Response:** `201 Created` with the destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `type` (must be `"srt-caller"` or `"srt-listener"`), missing `port`, or missing `address` for caller |
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
| `501` | Output manager not configured |

---

### DELETE /api/output/destinations/{id}

Remove a destination. If the destination is currently active, it is automatically stopped before removal.

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `501` | Output manager not configured |

---

### POST /api/output/destinations/{id}/start

Start a specific destination.

**Response:** `200 OK` with destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `409` | Destination is already active |
| `501` | Output manager not configured |

---

### POST /api/output/destinations/{id}/stop

Stop a specific destination.

**Response:** `200 OK` with destination status

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Destination not found |
| `409` | Destination is not active |
| `501` | Output manager not configured |

---

## Graphics (Multi-Layer DSK)

The downstream keyer (DSK) composites multiple RGBA overlay layers (lower thirds, logos, score bugs, etc.) onto the program output. Each layer has an independent z-order, position/size, opacity, and animation state. Overlays are rendered in the browser and uploaded as raw RGBA pixel data. The server decodes program frames, alpha-blends all active layers in z-order, and re-encodes. When no layers are active, frames pass through unchanged with zero CPU overhead.

All graphics endpoints return the full `GraphicsState` (all layers) on success.

### POST /api/graphics

Add a new graphics layer. Returns the assigned layer ID.

**Request Body:** Empty JSON object `{}`

**Response:** `201 Created`:

```json
{
  "id": 0
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `409` | Maximum number of layers reached |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### GET /api/graphics

Get the current graphics state including all layers, their positions, z-order, and animation state.

**Request Body:** None

**Response:** `200 OK` with full `GraphicsState`:

```json
{
  "layers": [
    {
      "id": 0,
      "active": true,
      "template": "lower-third",
      "fadePosition": 1.0,
      "animationMode": "",
      "animationHz": 0,
      "zOrder": 0,
      "rect": { "x": 0, "y": 810, "width": 1920, "height": 270 }
    },
    {
      "id": 1,
      "active": true,
      "template": "logo",
      "fadePosition": 1.0,
      "animationMode": "pulse",
      "animationHz": 1.0,
      "zOrder": 1,
      "rect": { "x": 1720, "y": 20, "width": 180, "height": 80 }
    }
  ],
  "programWidth": 1920,
  "programHeight": 1080
}
```

**Example:**

```bash
curl http://localhost:8081/api/graphics \
  -H "Authorization: Bearer $TOKEN"
```

---

### DELETE /api/graphics/{id}

Remove a graphics layer. The layer is deactivated and deleted.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/graphics/1 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/on

Activate a layer immediately (CUT ON). The layer appears on the program output at full opacity in a single frame. Requires that an overlay frame has been previously uploaded to this layer via `POST /api/graphics/{id}/frame`.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, or no overlay frame has been uploaded |
| `404` | Layer not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/on \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/{id}/off

Deactivate a layer immediately (CUT OFF). The layer disappears from the program output in a single frame.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/off \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/{id}/auto-on

Start a 500ms fade-in transition (AUTO ON) on a layer. The layer fades in smoothly from invisible to fully opaque over 500ms. Requires that an overlay frame has been previously uploaded to this layer.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `GraphicsState`

The layer's `fadePosition` will progress from `0.0` to `1.0` over the 500ms duration. State updates are broadcast via MoQ at ~60fps during the fade.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, or no overlay frame has been uploaded |
| `404` | Layer not found |
| `409` | A fade transition is already in progress on this layer |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/auto-on \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/{id}/auto-off

Start a 500ms fade-out transition (AUTO OFF) on a layer. The layer fades out smoothly from fully opaque to invisible over 500ms. The layer becomes inactive when the fade completes.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `GraphicsState`

The layer's `fadePosition` will progress from `1.0` to `0.0` over the 500ms duration.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |
| `409` | Layer is not active, or a fade transition is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/auto-off \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/graphics/{id}/frame

Upload an RGBA overlay frame to a specific layer. The RGBA pixel data is provided as a number array in the JSON body. This can be called while the layer is active to update the graphics in real-time (e.g., animated score bug).

Maximum body size: 16MB. Maximum resolution: 3840x2160 (4K).

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "width": 1920,
  "height": 1080,
  "template": "lower-third",
  "rgba": [255, 255, 255, 128, 0, 0, 0, 0]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `width` | `int` | Yes | Overlay width in pixels. Must be `1`-`3840`. |
| `height` | `int` | Yes | Overlay height in pixels. Must be `1`-`2160`. |
| `rgba` | `number[]` | Yes | Raw RGBA pixel data as a number array. Must be exactly `width * height * 4` elements. |
| `template` | `string` | No | Template name for identification in status/state |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, invalid dimensions, resolution exceeds 4K, RGBA data size mismatch, or invalid JSON |
| `404` | Layer not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/frame \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"width": 320, "height": 240, "template": "test", "rgba": [255,255,255,128,...]}'
```

---

### POST /api/graphics/{id}/animate

Start an animation on a layer. Two animation modes are supported: `pulse` (continuous opacity oscillation) and `transition` (one-shot position/opacity animation).

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body (pulse mode):**

```json
{
  "mode": "pulse",
  "minAlpha": 0.3,
  "maxAlpha": 1.0,
  "speedHz": 2.0
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `mode` | `string` | Yes | `"pulse"` for continuous oscillation |
| `minAlpha` | `float` | Yes | Minimum opacity during pulse (`0.0`-`1.0`) |
| `maxAlpha` | `float` | Yes | Maximum opacity during pulse (`0.0`-`1.0`) |
| `speedHz` | `float` | Yes | Oscillation frequency in Hz |

**Request Body (transition mode):**

```json
{
  "mode": "transition",
  "toRect": { "x": 100, "y": 50, "width": 800, "height": 600 },
  "toAlpha": 0.5,
  "durationMs": 1000,
  "easing": "ease-in-out"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `mode` | `string` | Yes | `"transition"` for one-shot animation |
| `toRect` | `object` | No | Target position and size `{x, y, width, height}`. At least one of `toRect` or `toAlpha` is required. |
| `toAlpha` | `float` | No | Target opacity (`0.0`-`1.0`). At least one of `toRect` or `toAlpha` is required. |
| `durationMs` | `int` | Yes | Animation duration in milliseconds. Must be positive. |
| `easing` | `string` | No | Easing function. Default varies by implementation. |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, invalid mode, missing required fields for mode, or invalid parameter values |
| `404` | Layer not found |
| `409` | Layer is not active, or a fade/animation is already in progress |

**Example:**

```bash
# Pulse animation
curl -X POST http://localhost:8081/api/graphics/0/animate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "pulse", "minAlpha": 0.3, "maxAlpha": 1.0, "speedHz": 2.0}'

# Transition animation
curl -X POST http://localhost:8081/api/graphics/0/animate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "transition", "toRect": {"x": 100, "y": 50, "width": 800, "height": 600}, "durationMs": 1000}'
```

---

### POST /api/graphics/{id}/animate/stop

Stop any running animation on a layer and restore the layer's `fadePosition` to `1.0` (fully visible).

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/animate/stop \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### PUT /api/graphics/{id}/rect

Set the position and size of a graphics layer. Coordinates are in pixels relative to the program resolution.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "x": 100,
  "y": 800,
  "width": 600,
  "height": 200
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `x` | `int` | Yes | Horizontal position in pixels from left edge |
| `y` | `int` | Yes | Vertical position in pixels from top edge |
| `width` | `int` | Yes | Layer width in pixels |
| `height` | `int` | Yes | Layer height in pixels |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID or invalid dimensions |
| `404` | Layer not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/graphics/0/rect \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"x": 100, "y": 800, "width": 600, "height": 200}'
```

---

### PUT /api/graphics/{id}/zorder

Set the z-order (stacking priority) of a graphics layer. Layers with higher z-order values are composited on top of layers with lower values.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "zOrder": 5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `zOrder` | `int` | Yes | Stacking order value (higher = on top) |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/graphics/0/zorder \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"zOrder": 5}'
```

---

### POST /api/graphics/{id}/fly-in

Animate a layer flying in from off-screen. The layer slides into view from the specified edge over the given duration. The layer must be active (CUT ON first).

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "direction": "left",
  "durationMs": 500
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `direction` | `string` | Yes | Edge to fly in from: `"left"`, `"right"`, `"top"`, or `"bottom"` |
| `durationMs` | `int` | No | Animation duration in milliseconds. Defaults to `500`. |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, invalid direction, or invalid duration |
| `404` | Layer not found |
| `409` | Layer is not active, or a fade/animation is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/fly-in \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"direction": "left", "durationMs": 500}'
```

---

### POST /api/graphics/{id}/fly-out

Animate a layer flying out to off-screen. The layer slides out of view toward the specified edge over the given duration. The layer must be active.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "direction": "right",
  "durationMs": 500
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `direction` | `string` | Yes | Edge to fly out to: `"left"`, `"right"`, `"top"`, or `"bottom"` |
| `durationMs` | `int` | No | Animation duration in milliseconds. Defaults to `500`. |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, invalid direction, or invalid duration |
| `404` | Layer not found |
| `409` | Layer is not active, or a fade/animation is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/fly-out \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"direction": "right", "durationMs": 500}'
```

---

### POST /api/graphics/{id}/slide

Slide a layer to a new position and/or size over a specified duration. This is a smooth position animation, useful for repositioning lower thirds or sliding elements across the screen.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Layer ID |

**Request Body:**

```json
{
  "x": 200,
  "y": 850,
  "width": 800,
  "height": 150,
  "durationMs": 750
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `x` | `int` | Yes | Target horizontal position in pixels |
| `y` | `int` | Yes | Target vertical position in pixels |
| `width` | `int` | Yes | Target width in pixels |
| `height` | `int` | Yes | Target height in pixels |
| `durationMs` | `int` | No | Animation duration in milliseconds. Defaults to `500`. |

**Response:** `200 OK` with full `GraphicsState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, invalid dimensions, or invalid duration |
| `404` | Layer not found |
| `409` | Layer is not active, or a fade/animation is already in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/slide \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"x": 200, "y": 850, "width": 800, "height": 150, "durationMs": 750}'
```

---

### POST /api/graphics/{id}/fly-on

Atomically activate a layer and animate it from off-screen to its current position. Combines the `on` and `fly-in` operations into a single call to avoid a flash of content at the target position before the animation starts.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `direction` | `string` | Direction to fly from: `"left"`, `"right"`, `"top"`, or `"bottom"` |
| `durationMs` | `int` | Animation duration in milliseconds (default: 500) |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID or invalid direction |
| `404` | Layer not found |
| `409` | Layer already active, or an animation is in progress |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/fly-on \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"direction": "bottom", "durationMs": 600}'
```

---

### POST /api/graphics/{id}/image

Upload a PNG image to a graphics layer. The image is stored server-side and decoded to RGBA for compositing. Images persist until explicitly deleted or the layer is removed.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** Multipart form data with an `image` field containing the PNG file (max 16MB).

**Response:** `201 Created` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, missing `image` field, or invalid multipart form |
| `404` | Layer not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/image \
  -H "Authorization: Bearer $TOKEN" \
  -F "image=@logo.png"
```

---

### GET /api/graphics/{id}/image

Retrieve the stored PNG image from a graphics layer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `Content-Type: image/png` and the PNG data as the response body.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found or no image stored |

**Example:**

```bash
curl http://localhost:8081/api/graphics/0/image \
  -H "Authorization: Bearer $TOKEN" \
  -o layer0.png
```

---

### DELETE /api/graphics/{id}/image

Remove the stored image from a graphics layer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `204 No Content` (empty body)

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found or no image stored |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/graphics/0/image \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/text-animate

Start a text animation on a graphics layer. Renders animated text overlays using the embedded Inter font. Supports typewriter (character-by-character reveal) and fade-word (word-by-word fade-in) modes.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `string` | Animation mode: `"typewriter"` or `"fade-word"` |
| `text` | `string` | Text to animate (required) |
| `fontSize` | `float` | Font size in pixels |
| `bold` | `bool` | Whether to use bold weight |
| `charsPerSec` | `float` | Characters per second (typewriter mode) |
| `wordDelayMs` | `int` | Delay between words in ms (fade-word mode) |
| `fadeDurationMs` | `int` | Fade duration per word in ms (fade-word mode) |
| `width` | `int` | Render width in pixels |
| `height` | `int` | Render height in pixels |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, missing text, or invalid mode |
| `404` | Layer not found |
| `501` | Text animation engine not available |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/text-animate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "typewriter", "text": "Breaking News", "fontSize": 48, "bold": true, "charsPerSec": 15, "width": 800, "height": 60}'
```

---

### POST /api/graphics/{id}/text-animate/stop

Stop the text animation on a graphics layer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |
| `501` | Text animation engine not available |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/text-animate/stop \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/ticker

Start a scrolling news ticker on a graphics layer. The ticker renders text that scrolls horizontally across the layer at a configurable speed. Text can optionally loop.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `text` | `string` | Ticker text content (required) |
| `fontSize` | `float` | Font size in pixels (default: 24) |
| `speed` | `float` | Scroll speed in pixels per second (default: 100) |
| `bold` | `bool` | Whether to use bold weight |
| `loop` | `bool` | Whether the ticker loops after completing |
| `height` | `int` | Render height in pixels |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID or missing text |
| `404` | Layer not found |
| `501` | Ticker engine not available |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/ticker \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text": "BREAKING: Live coverage continues...", "fontSize": 28, "speed": 120, "bold": true, "loop": true, "height": 40}'
```

---

### POST /api/graphics/{id}/ticker/stop

Stop the scrolling ticker on a graphics layer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | Layer not found |
| `501` | Ticker engine not available |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/ticker/stop \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/graphics/{id}/ticker/text

Update the text content of a running ticker without stopping it. The new text takes effect on the next scroll cycle.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `text` | `string` | New ticker text (required) |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID or missing text |
| `404` | Layer not found or no ticker running |
| `501` | Ticker engine not available |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/graphics/0/ticker/text \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text": "UPDATED: New information available..."}'
```

---

### GraphicsState Fields

The `GraphicsState` object is returned by all graphics endpoints. The `ControlRoomState` broadcast uses a flattened variant (see note below).

```json
{
  "layers": [
    {
      "id": 0,
      "active": true,
      "template": "lower-third",
      "fadePosition": 1.0,
      "animationMode": "pulse",
      "animationHz": 1.0,
      "zOrder": 0,
      "rect": { "x": 0, "y": 0, "width": 1920, "height": 1080 }
    }
  ],
  "programWidth": 1920,
  "programHeight": 1080
}
```

| Field | Type | Description |
|-------|------|-------------|
| `layers` | `array` | List of all graphics layers, sorted by z-order |
| `programWidth` | `int` | Current program video width in pixels. Omitted when unknown. |
| `programHeight` | `int` | Current program video height in pixels. Omitted when unknown. |

### GraphicsLayer Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | `int` | Unique layer identifier |
| `active` | `bool` | Whether the layer is currently composited onto program |
| `template` | `string` | Name of the overlay template. Omitted if not set. |
| `fadePosition` | `float` | Opacity level: `0.0` = invisible, `1.0` = fully visible. Omitted when `0`. |
| `animationMode` | `string` | Current animation mode: `"pulse"` or `"transition"`. Omitted when no animation. |
| `animationHz` | `float` | Pulse frequency in Hz. Omitted when not pulsing. |
| `zOrder` | `int` | Stacking order (higher = on top) |
| `rect` | `object` | Layer position and size `{x, y, width, height}` in pixels |

> **Note:** In the `ControlRoomState` broadcast, graphics layers use flat fields (`x`, `y`, `width`, `height`) instead of a nested `rect` object, and the `programWidth`/`programHeight` fields are omitted. See the [ControlRoomState](#controlroomstate) example.

---

## HTML5 Graphics

The HTML5 graphics system uses a CEF (Chromium Embedded Framework) sidecar process to render browser-based overlays. Each graphics layer can load an HTML5 template URL, receive data updates, and respond to play/stop/next lifecycle commands. The sidecar manages a pool of browser instances. Templates are auto-discovered from the configured template directories (OGraf and CasparCG formats are supported).

All HTML5 endpoints (except template listing) return `503 Service Unavailable` if the CEF sidecar is not running.

### GET /api/graphics/html5/templates

List available HTML5 graphics templates. Returns templates discovered from the configured template directories. Works even if the sidecar is not running (returns an empty array).

**Request Body:** None

**Response:** `200 OK`

```json
[
  {
    "id": "lower-third",
    "name": "Lower Third",
    "description": "Animated lower-third with name and title",
    "format": "ograf",
    "url": "file:///templates/lower-third/index.html",
    "dataSchema": {"name": "string", "title": "string"}
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Template directory name |
| `name` | `string` | Display name (from manifest or directory name) |
| `description` | `string` | Template description (optional) |
| `format` | `string` | Template format: `"ograf"` or `"casparcg"` |
| `url` | `string` | File URL for loading the template |
| `dataSchema` | `object` | OGraf manifest data schema (optional) |

**Example:**

```bash
curl http://localhost:8081/api/graphics/html5/templates \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/html5

Load an HTML5 graphic onto a layer. Allocates a browser from the CEF sidecar pool, navigates it to the given URL, and optionally sends initial data.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `url` | `string` | URL to load in the browser (required) |
| `data` | `object` | Initial data payload to send after loading (optional) |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, missing URL, or invalid JSON |
| `409` | Browser pool exhausted (all browser slots in use) |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/html5 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"url": "file:///templates/lower-third/index.html", "data": {"name": "John Smith", "title": "Reporter"}}'
```

---

### PUT /api/graphics/{id}/html5/data

Send updated data to the HTML5 graphic on a layer. The data is forwarded to the browser via the adapter JavaScript bridge.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `data` | `object` | Data payload to send to the graphic |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID or invalid JSON |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/graphics/0/html5/data \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"data": {"name": "Jane Doe", "title": "Anchor"}}'
```

---

### POST /api/graphics/{id}/html5/play

Send a play command to the HTML5 graphic on a layer. Triggers the template's play/animate-in behavior.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/html5/play \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/html5/stop

Send a stop command to the HTML5 graphic on a layer. Triggers the template's stop/animate-out behavior.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/html5/stop \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/html5/next

Send a next command to the HTML5 graphic on a layer. Advances the template to its next state (e.g., next slide, next data item). Behavior is template-specific.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/html5/next \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/graphics/{id}/html5/custom

Send a custom action to the HTML5 graphic on a layer. Dispatches an arbitrary action name with an optional JSON payload to the template.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `action` | `string` | Custom action name (required) |
| `payload` | `object` | Action payload (optional) |

**Response:** `200 OK` with `GraphicsState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID, missing action, or invalid JSON |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/graphics/0/html5/custom \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"action": "highlight", "payload": {"team": "home"}}'
```

---

### DELETE /api/graphics/{id}/html5

Unload the HTML5 graphic from a layer and return the browser to the pool.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Layer ID (integer) |

**Request Body:** None

**Response:** `204 No Content` (empty body)

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid layer ID |
| `404` | No HTML5 graphic loaded on this layer |
| `503` | HTML5 graphics sidecar not running |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/graphics/0/html5 \
  -H "Authorization: Bearer $TOKEN"
```

---

## DVE (Multi-Source Compositor)

The DVE (Digital Video Effects) compositor controls all multi-source composition: picture-in-picture (PIP), split-screen, quad, and custom layouts. A DVE configuration consists of up to 8 indexed slots (0–7), each bound to a source with position, size, rotation, opacity, border, and scale mode. DVE presets provide built-in configurations (e.g., PIP corner, side-by-side). DVE slot transforms can also be updated in real-time via WebTransport datagrams (0x04-0x09) for low-latency drag and parameter control (see fast-control datagrams).

### GET /api/dve

Get the current DVE state including all slots.

**Request Body:** None

**Response:** `200 OK`:

When a DVE layout is active:

```json
{
  "name": "pip-bottom-right",
  "slots": [
    {
      "sourceKey": "cam1",
      "rect": { "Min": { "X": 0, "Y": 0 }, "Max": { "X": 1920, "Y": 1080 } },
      "zOrder": 0,
      "border": { "width": 0, "colorY": 0, "colorCb": 0, "colorCr": 0 },
      "transition": { "type": "", "durationMs": 0 },
      "enabled": true,
      "scaleMode": "fill"
    },
    {
      "sourceKey": "cam2",
      "rect": { "Min": { "X": 1440, "Y": 720 }, "Max": { "X": 1920, "Y": 1080 } },
      "zOrder": 1,
      "border": { "width": 2, "colorY": 235, "colorCb": 128, "colorCr": 128 },
      "transition": { "type": "dissolve", "durationMs": 300 },
      "enabled": true,
      "scaleMode": "stretch"
    }
  ]
}
```

When no DVE layout is active:

```json
{
  "layout": null
}
```

#### Slot Fields

| Field | Type | Description |
|-------|------|-------------|
| `sourceKey` | `string` | Source key assigned to this slot |
| `rect` | `object` | Position rectangle `{"Min": {"X", "Y"}, "Max": {"X", "Y"}}` in pixels |
| `zOrder` | `int` | Stacking order (higher = on top) |
| `border` | `object` | Border config: `width` (pixels), `colorY`/`colorCb`/`colorCr` (BT.709) |
| `transition` | `object` | Slot transition: `type` (`"cut"`, `"dissolve"`, `"fly"`) and `durationMs` |
| `enabled` | `bool` | Whether the slot is visible in the composition |
| `scaleMode` | `string` | `"stretch"` (default, may distort), `"fill"` (crop to cover), or `"fit"` (letterbox). Omitted when empty. |
| `cropAnchor` | `[2]float` | `[x, y]` anchor for fill crop, range 0.0–1.0. Default center `[0.5, 0.5]`. Omitted when zero. |

**Example:**

```bash
curl http://localhost:8081/api/dve \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/dve

Set the current DVE layout from a preset name or a custom slot array.

**Request Body (preset):**

```json
{
  "preset": "pip-bottom-right"
}
```

**Request Body (custom slots):**

```json
{
  "slots": [
    {
      "sourceKey": "cam1",
      "rect": { "Min": { "X": 0, "Y": 0 }, "Max": { "X": 1920, "Y": 1080 } },
      "enabled": true,
      "scaleMode": "fill"
    },
    {
      "sourceKey": "cam2",
      "rect": { "Min": { "X": 1440, "Y": 720 }, "Max": { "X": 1920, "Y": 1080 } },
      "enabled": true
    }
  ]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `preset` | `string` | No | Name of a built-in or saved DVE preset |
| `slots` | `array` | No | Array of `Slot` objects (max 8). One of `preset` or `slots` is required. |
| `name` | `string` | No | Optional name for the custom layout |

**Response:** `200 OK` with the applied DVE state

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, missing both `preset` and `slots`, invalid slot configuration (>8 slots, unknown scaleMode) |
| `404` | Preset not found |

**Example:**

```bash
# Apply a preset
curl -X PUT http://localhost:8081/api/dve \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"preset": "pip-bottom-right"}'

# Apply a custom layout
curl -X PUT http://localhost:8081/api/dve \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slots": [{"sourceKey": "cam1", "rect": {"Min": {"X": 0, "Y": 0}, "Max": {"X": 1920, "Y": 1080}}, "enabled": true, "scaleMode": "fill"}]}'
```

---

### DELETE /api/dve

Clear the current DVE layout. All slots are disabled, returning to single-source program output.

**Request Body:** None

**Response:** `204 No Content`

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/dve \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/dve/slots/{id}

Update properties of a DVE slot. All fields are optional; only provided fields are updated.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Slot index (0–7) |

**Request Body:**

```json
{
  "sourceKey": "cam3",
  "x": 1440,
  "y": 720,
  "width": 480,
  "height": 360,
  "scaleMode": "fill",
  "cropAnchor": [0.5, 0.0],
  "border": { "width": 2, "colorY": 235, "colorCb": 128, "colorCr": 128 },
  "transition": { "type": "dissolve", "durationMs": 500 }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `sourceKey` | `string` | No | Source key to assign to this slot |
| `x` | `int` | No | New X position |
| `y` | `int` | No | New Y position |
| `width` | `int` | No | New width |
| `height` | `int` | No | New height |
| `scaleMode` | `string` | No | `"stretch"`, `"fill"`, or `"fit"` |
| `cropAnchor` | `[2]float` | No | `[x, y]` anchor for fill mode, range 0.0–1.0 |
| `border` | `object` | No | Border config: `width`, `colorY`, `colorCb`, `colorCr` |
| `transition` | `object` | No | Slot transition: `type` and `durationMs` |

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, non-integer slot ID, or slot ID out of range |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/dve/slots/1 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sourceKey": "cam3", "x": 100, "y": 100, "width": 640, "height": 480}'
```

---

### POST /api/dve/slots/{id}/on

Enable a DVE slot, making it visible in the composition.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Slot index (0–7) |

**Request Body:** None (body is ignored if sent)

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Non-integer or out-of-range slot ID |

**Example:**

```bash
curl -X POST http://localhost:8081/api/dve/slots/1/on \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/dve/slots/{id}/off

Disable a DVE slot, removing it from the composition.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `int` | Slot index (0–7) |

**Request Body:** None (body is ignored if sent)

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Non-integer or out-of-range slot ID |

**Example:**

```bash
curl -X POST http://localhost:8081/api/dve/slots/1/off \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/dve/presets

List all saved DVE presets (user-saved presets only; built-in presets are resolved by name in PUT /api/dve).

**Request Body:** None

**Response:** `200 OK` with an array of preset name strings:

```json
["my-pip-layout", "interview-split", "three-up"]
```

**Example:**

```bash
curl http://localhost:8081/api/dve/presets \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/dve/presets

Save the current active DVE layout as a named preset.

**Request Body:**

```json
{
  "name": "my-custom-layout"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Preset name |

**Response:** `201 Created`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing or empty `name`, or no active DVE layout to save |

**Example:**

```bash
curl -X POST http://localhost:8081/api/dve/presets \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-custom-layout"}'
```

---

### PUT /api/dve/presets/{id}/recall

Recall a saved DVE preset, optionally with an animated transition.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Preset name |

**Request Body:**

```json
{
  "durationMs": 500
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `durationMs` | `int` | No | Animation duration in milliseconds. 0 = instant. |

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Preset not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/dve/presets/my-custom-layout/recall \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"durationMs": 300}'
```

---

### DELETE /api/dve/presets/{id}

Delete a saved DVE preset.

**URL Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `id` | `string` | Preset name |

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Preset not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/dve/presets/my-custom-layout \
  -H "Authorization: Bearer $TOKEN"
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
| `name` | `string` | No | New name for the preset. If empty or omitted, the preset is returned unchanged. |

**Response:** `200 OK` with updated `Preset`

**Errors:**

| Status | Condition |
|--------|-----------|
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

### GET /api/output/cbr

Get the current CBR (Constant Bit Rate) pacer status. The CBR pacer inserts null MPEG-TS packets to maintain a constant bitrate for SRT output, ensuring stable transport over bandwidth-constrained links. Returns `{"enabled": false}` if CBR pacing is not active.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "enabled": true,
  "muxrateBps": 10000000,
  "nullPacketsTotal": 524288,
  "realBytesTotal": 1048576,
  "padBytesTotal": 786432,
  "burstTicksTotal": 12345
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether CBR pacing is active |
| `muxrateBps` | `int` | Target mux rate in bits per second |
| `nullPacketsTotal` | `int` | Total null TS packets inserted |
| `realBytesTotal` | `int` | Total real (content) bytes transmitted |
| `padBytesTotal` | `int` | Total padding bytes transmitted |
| `burstTicksTotal` | `int` | Total burst ticks (pacing events) |

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | Output manager not configured |

**Example:**

```bash
curl http://localhost:8081/api/output/cbr \
  -H "Authorization: Bearer $TOKEN"
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
| `409` | A clip with this name already exists, or maximum clip limit reached (`ErrMaxClipsReached`, default 16) |
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

**Response:** `200 OK` with `Status`:

```json
{
  "state": "playing",
  "source": "cam1",
  "speed": 0.5,
  "loop": false,
  "position": 0.35,
  "markIn": 1741185010000,
  "markOut": 1741185020000,
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

### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Player state: `"idle"`, `"loading"`, or `"playing"` |
| `source` | `string` | Source key being played. Omitted when idle. |
| `speed` | `float` | Playback speed. Omitted when idle. |
| `loop` | `bool` | Whether playback loops. Omitted when idle. |
| `position` | `float` | Playback progress from `0.0` to `1.0`. Omitted when idle. |
| `markIn` | `int64` | Mark-in time as Unix milliseconds. Omitted when not set. |
| `markOut` | `int64` | Mark-out time as Unix milliseconds. Omitted when not set. |
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

### POST /api/replay/quick

One-call quick replay: automatically sets mark-in/mark-out relative to the current time and begins playback. This is a convenience endpoint that combines mark-in, mark-out, and play into a single request.

**Request Body:**

```json
{
  "source": "cam1",
  "seconds": 10,
  "speed": 0.5
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `source` | `string` | Yes | -- | Source key to replay from |
| `seconds` | `float` | Yes | -- | Number of seconds to replay (counted back from now) |
| `speed` | `float` | No | `1.0` | Playback speed. Range: `0.25` to `1.0`. Values below `1.0` produce slow motion. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` or `seconds`, invalid JSON, `speed` out of range, or insufficient buffer |
| `404` | Source not found in replay buffers |
| `409` | A replay is already playing |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/quick \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"source": "cam1", "seconds": 10, "speed": 0.5}'
```

---

### POST /api/replay/pause

Pause the currently active replay playback. The player holds the current frame on output.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No replay is currently playing |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/pause \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### POST /api/replay/resume

Resume a paused replay playback from the current position.

**Request Body:** Empty JSON object `{}`

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Replay is not paused |

**Example:**

```bash
curl -X POST http://localhost:8081/api/replay/resume \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### PATCH /api/replay/seek

Seek to a normalized position within the current replay clip. Can be used while playing or paused.

**Request Body:**

```json
{
  "position": 0.5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `position` | `float` | Yes | Normalized seek position from `0.0` (start) to `1.0` (end) |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No replay is active, `position` out of range, or invalid JSON |

**Example:**

```bash
curl -X PATCH http://localhost:8081/api/replay/seek \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"position": 0.5}'
```

---

### PATCH /api/replay/speed

Change the playback speed of an active replay. Takes effect immediately without interrupting playback.

**Request Body:**

```json
{
  "speed": 0.25
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `speed` | `float` | Yes | Playback speed. Range: `0.25` to `1.0`. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No replay is active, `speed` out of range, or invalid JSON |

**Example:**

```bash
curl -X PATCH http://localhost:8081/api/replay/speed \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"speed": 0.25}'
```

---

### PATCH /api/replay/marks

Adjust mark-in and/or mark-out points on an active or idle replay. Either or both fields may be provided. Times are Unix timestamps in milliseconds.

**Request Body:**

```json
{
  "markInMs": 5000,
  "markOutMs": 15000
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `markInMs` | `int` | No | New mark-in time as Unix timestamp in milliseconds |
| `markOutMs` | `int` | No | New mark-out time as Unix timestamp in milliseconds |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | No mark source set, mark-out before mark-in, or invalid JSON |

**Example:**

```bash
curl -X PATCH http://localhost:8081/api/replay/marks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"markInMs": 5000, "markOutMs": 15000}'
```

---

### GET /api/replay/peek

Get a keyframe thumbnail from a source's replay buffer. Returns the most recent H.264 keyframe data for preview purposes.

**Query Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `source` | Yes | Source key to peek into |

**Response:** `200 OK` with H.264 keyframe data (binary)

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `source` query parameter |
| `404` | Source not found in replay buffers or no frames available |

**Example:**

```bash
curl http://localhost:8081/api/replay/peek?source=cam1 \
  -H "Authorization: Bearer $TOKEN" \
  -o keyframe.h264
```

---

### PUT /api/replay/shutter-angle

Set the shutter angle for slow-motion replay playback. Shutter angle controls the amount of motion blur applied during frame interpolation. A shutter angle of 180° (the cinematic standard) applies moderate blur; 360° applies maximum blur; 0° disables motion blur entirely.

**Request Body:**

| Field | Type | Description |
|-------|------|-------------|
| `angle` | `float` | Shutter angle in degrees (0–360) |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or angle out of 0–360 range |
| `501` | Replay not enabled |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/replay/shutter-angle \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"angle": 180}'
```

---

### GET /api/replay/shutter-angle

Get the current shutter angle setting for replay playback.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "angle": 180
}
```

| Field | Type | Description |
|-------|------|-------------|
| `angle` | `float` | Current shutter angle in degrees (0–360) |

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | Replay not enabled |

**Example:**

```bash
curl http://localhost:8081/api/replay/shutter-angle \
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
| `steps` | `array` | Ordered list of `Step` to execute |

### Step Fields

| Field | Type | Description |
|-------|------|-------------|
| `action` | `string` | Step type (see valid actions below) |
| `params` | `object` | Action-specific parameters |

### Valid Macro Actions

48 action types across 12 categories:

**Switching**

| Action | Params | Description |
|--------|--------|-------------|
| `"cut"` | `{ "source": "cam1" }` | Hard cut to source |
| `"preview"` | `{ "source": "cam2" }` | Set preview source |
| `"transition"` | `{ "source": "cam2", "type": "mix", "durationMs": 1000, "wipeDirection": "", "stingerName": "" }` | Start a transition |
| `"ftb"` | `{}` | Fade to black |
| `"wait"` | `{ "ms": 2000 }` | Pause execution for the specified duration |

**Audio**

| Action | Params | Description |
|--------|--------|-------------|
| `"set_audio"` | `{ "source": "cam1", "level": -6.0 }` | Set audio fader level (legacy) |
| `"audio_mute"` | `{ "source": "cam1", "muted": true }` | Mute/unmute a source |
| `"audio_afv"` | `{ "source": "cam1", "afv": true }` | Enable/disable audio-follow-video |
| `"audio_trim"` | `{ "source": "cam1", "trim": 3.0 }` | Set input trim (dB) |
| `"audio_master"` | `{ "level": -3.0 }` | Set master audio level (dB) |
| `"audio_eq"` | `{ "source": "cam1", "band": 0, "frequency": 1000, "gain": 3.0, "q": 1.0, "enabled": true }` | Configure per-channel EQ band |
| `"audio_compressor"` | `{ "source": "cam1", "threshold": -20, "ratio": 4, "attack": 10, "release": 100, "makeupGain": 0 }` | Configure per-channel compressor |
| `"audio_delay"` | `{ "source": "cam1", "delayMs": 100 }` | Set per-channel audio delay (lip-sync) |

**Graphics**

| Action | Params | Description |
|--------|--------|-------------|
| `"graphics_on"` | `{ "layerId": 0 }` | Cut graphics layer on |
| `"graphics_off"` | `{ "layerId": 0 }` | Cut graphics layer off |
| `"graphics_auto_on"` | `{ "layerId": 0, "durationMs": 500 }` | Fade graphics layer on |
| `"graphics_auto_off"` | `{ "layerId": 0, "durationMs": 500 }` | Fade graphics layer off |
| `"graphics_add_layer"` | `{}` | Add a new graphics layer |
| `"graphics_remove_layer"` | `{ "layerId": 0 }` | Remove a graphics layer |
| `"graphics_set_rect"` | `{ "layerId": 0, "x": 0, "y": 0, "width": 1920, "height": 1080 }` | Set layer position/size |
| `"graphics_set_zorder"` | `{ "layerId": 0, "zOrder": 1 }` | Set layer stacking order |
| `"graphics_fly_in"` | `{ "layerId": 0, "direction": "left", "durationMs": 500 }` | Fly layer in from direction |
| `"graphics_fly_out"` | `{ "layerId": 0, "direction": "right", "durationMs": 500 }` | Fly layer out to direction |
| `"graphics_slide"` | `{ "layerId": 0, "x": 100, "y": 100, "width": 640, "height": 480, "durationMs": 500 }` | Slide layer to new position |
| `"graphics_animate"` | `{ "layerId": 0, "mode": "pulse", "minAlpha": 0, "maxAlpha": 1, "speedHz": 1 }` | Start layer animation |
| `"graphics_animate_stop"` | `{ "layerId": 0 }` | Stop layer animation |
| `"graphics_upload_frame"` | `{ "layerId": 0, "width": 1920, "height": 1080, "template": "lower-third", "rgba": "<base64>" }` | Upload RGBA overlay frame |

**Output**

| Action | Params | Description |
|--------|--------|-------------|
| `"recording_start"` | `{ "directory": "/path" }` | Start recording (directory optional) |
| `"recording_stop"` | `{}` | Stop recording |

**Presets**

| Action | Params | Description |
|--------|--------|-------------|
| `"preset_recall"` | `{ "id": "<uuid>" }` | Recall a saved preset |

**Source / Keying**

| Action | Params | Description |
|--------|--------|-------------|
| `"key_set"` | `{ "source": "cam1", "config": { "type": "chroma", ... } }` | Set upstream key config |
| `"key_delete"` | `{ "source": "cam1" }` | Remove upstream key |
| `"source_label"` | `{ "source": "cam1", "label": "Camera 1" }` | Set source display label |
| `"source_delay"` | `{ "source": "cam1", "delayMs": 100 }` | Set source video delay |
| `"source_position"` | `{ "source": "cam1", "position": 1 }` | Set source ordering position |

**Replay**

| Action | Params | Description |
|--------|--------|-------------|
| `"replay_mark_in"` | `{ "source": "cam1" }` | Set replay mark-in point |
| `"replay_mark_out"` | `{ "source": "cam1" }` | Set replay mark-out point |
| `"replay_play"` | `{ "source": "cam1", "speed": 0.5, "loop": false }` | Play marked replay clip |
| `"replay_stop"` | `{}` | Stop replay playback |
| `"replay_quick_clip"` | `{ "source": "cam1", "speed": 0.5 }` | Quick mark + play |
| `"replay_play_last"` | `{}` | Play last clip (stub) |
| `"replay_play_clip"` | `{ "clipId": "..." }` | Play specific clip (stub) |

**DVE**

| Action | Params | Description |
|--------|--------|-------------|
| `"dve_preset"` | `{ "preset": "pip-bottom-right" }` | Apply DVE preset |
| `"dve_slot_on"` | `{ "source": "0" }` | Enable DVE slot |
| `"dve_slot_off"` | `{ "source": "0" }` | Disable DVE slot |
| `"dve_slot_source"` | `{ "source": "0" }` | Set DVE slot source |
| `"dve_clear"` | `{}` | Clear DVE layout |

**SCTE-35**

| Action | Params | Description |
|--------|--------|-------------|
| `"scte35_cue"` | `{ "commandType": "splice_insert", "isOut": true, "durationMs": 30000, ... }` | Inject SCTE-35 cue |
| `"scte35_return"` | `{ "eventId": 42 }` | Return event to program |
| `"scte35_cancel"` | `{ "eventId": 42 }` | Cancel active event |
| `"scte35_hold"` | `{ "eventId": 42 }` | Hold event auto-return |
| `"scte35_extend"` | `{ "eventId": 42, "durationMs": 30000 }` | Extend event duration |

**Captions**

| Action | Params | Description |
|--------|--------|-------------|
| `"caption_mode"` | `{ "mode": "author" }` | Set caption mode (off/passthrough/author) |
| `"caption_text"` | `{ "text": "Hello", "newline": false }` | Ingest caption text |
| `"caption_clear"` | `{}` | Clear caption display |

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
| `409` | Another macro is already running |
| `500` | A step in the macro failed (error message includes the failing step) |

**Example:**

```bash
curl -X POST http://localhost:8081/api/macros/Opening%20Sequence/run \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

---

### DELETE /api/macros/execution

Dismiss the macro execution state. Clears the last execution result from the state broadcast so it is no longer shown in the UI. Does not affect a currently running macro.

**Request Body:** None

**Response:** `204 No Content`

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/macros/execution \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/macros/execution/cancel

Cancel a currently running macro. The macro will stop after its current step completes. Has no effect if no macro is running.

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | No macro is currently running |

**Example:**

```bash
curl -X POST http://localhost:8081/api/macros/execution/cancel \
  -H "Authorization: Bearer $TOKEN"
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
| `"captioner"` | Can command and lock the `captions` subsystem only. |
| `"viewer"` | Read-only. Cannot command or lock any subsystem. |

### Lockable Subsystems

| Subsystem | Description |
|-----------|-------------|
| `"switching"` | Program/preview cuts and transitions |
| `"audio"` | Audio mixer controls |
| `"graphics"` | DSK graphics overlay |
| `"replay"` | Instant replay system |
| `"output"` | Recording and SRT output |
| `"captions"` | Caption system controls |

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
| `role` | `string` | Yes | Operator role: `"director"`, `"audio"`, `"graphics"`, `"captioner"`, or `"viewer"` |

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

**Response:** `200 OK` with array of `Info`:

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
| `401` | Missing or invalid token (when operators are registered) |
| `403` | Only self or director can delete operators |
| `404` | Operator not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/operator/op_abc123 \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/operator/invite-tokens

Retrieve the invite token map for building shareable invite links. Returns a mapping of role names to their invite tokens. Only accessible to operators with the `director` role. If no invite tokens are configured on the server, returns an empty object.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "director": "tok_abc123",
  "td": "tok_def456",
  "operator": "tok_ghi789",
  "observer": "tok_jkl012"
}
```

| Field | Type | Description |
|-------|------|-------------|
| *(role name)* | `string` | Invite token for the given role |

**Errors:**

| Status | Condition |
|--------|-----------|
| `401` | Missing or invalid bearer token |
| `403` | Caller is not a director |

**Example:**

```bash
curl http://localhost:8081/api/operator/invite-tokens \
  -H "Authorization: Bearer $TOKEN"
```

---

## Operator Comms

The operator comms system provides a voice communication channel between registered operators. Operators can join a shared comms channel, mute/unmute themselves, and see who is speaking. The channel supports a maximum of 6 concurrent participants. Comms state is broadcast via `ControlRoomState.comms`.

### POST /api/comms/join

Join the operator voice comms channel. The operator becomes a participant and can send/receive audio.

**Request Body:**

```json
{
  "operatorId": "op1",
  "name": "Director"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operatorId` | `string` | Yes | Operator ID of the participant joining |
| `name` | `string` | Yes | Display name of the participant |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `operatorId` or `name`, or invalid JSON |
| `409` | Channel is full (maximum 6 participants) |

**Example:**

```bash
curl -X POST http://localhost:8081/api/comms/join \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"operatorId": "op1", "name": "Director"}'
```

---

### POST /api/comms/leave

Leave the comms channel. The operator is removed from the participants list.

**Request Body:**

```json
{
  "operatorId": "op1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operatorId` | `string` | Yes | Operator ID of the participant leaving |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `operatorId` or invalid JSON |
| `404` | Operator is not in the comms channel |

**Example:**

```bash
curl -X POST http://localhost:8081/api/comms/leave \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"operatorId": "op1"}'
```

---

### PUT /api/comms/mute

Set the mute state for a participant in the comms channel.

**Request Body:**

```json
{
  "operatorId": "op1",
  "muted": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `operatorId` | `string` | Yes | Operator ID of the participant |
| `muted` | `bool` | Yes | `true` to mute, `false` to unmute |

**Response:** `200 OK`

```json
{"ok": true}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `operatorId` or `muted`, or invalid JSON |
| `404` | Operator is not in the comms channel |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/comms/mute \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"operatorId": "op1", "muted": true}'
```

---

### GET /api/comms/status

Get the current comms channel state including all participants and their mute/speaking status.

**Request Body:** None

**Response:** `200 OK` with `CommsState`:

```json
{
  "active": true,
  "participants": [
    { "operatorId": "op_abc123", "name": "Director", "muted": false, "speaking": true },
    { "operatorId": "op_def456", "name": "Audio Eng", "muted": false, "speaking": false }
  ]
}
```

**Example:**

```bash
curl http://localhost:8081/api/comms/status \
  -H "Authorization: Bearer $TOKEN"
```

---

## SCTE-35 Ad Insertion

The SCTE-35 API provides real-time ad insertion signaling for MPEG-TS output streams. SCTE-35 splice_insert and time_signal commands are injected into the transport stream with PTS-synchronized timing. The system supports auto-return timers, break hold/extend for live overruns, splice_null heartbeats, and a signal conditioning rules engine for filtering and transforming pass-through signals.

SCTE-35 must be enabled at startup with the `--scte35` CLI flag. All SCTE-35 endpoints return `501 Not Implemented` if the flag is not set. Additional CLI flags: `--scte35-pid` (default 258/0x102), `--scte35-preroll` (default 4000ms), `--scte35-heartbeat` (default 5000ms), `--scte35-verify` (CRC verification), `--scte35-webhook` (event notification URL).

### POST /api/scte35/cue

Inject an SCTE-35 cue message into the MPEG-TS output. Supports both `splice_insert` (ad break start) and `time_signal` (segmentation descriptors) command types. When `preRollMs` is specified, the cue is scheduled ahead of time using PTS-synchronized timing rather than injected immediately.

**Request Body:**

```json
{
  "commandType": "splice_insert",
  "isOut": true,
  "durationMs": 30000,
  "autoReturn": true,
  "preRollMs": 4000,
  "eventId": 42,
  "uniqueProgramId": 1234,
  "availNum": 1,
  "availsExpected": 4,
  "descriptors": [
    {
      "segmentationType": 52,
      "durationMs": 60000,
      "upidType": 15,
      "upid": "https://ads.example.com/avail/1"
    }
  ]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `commandType` | `string` | Yes | Command type: `"splice_insert"`, `"time_signal"`, or `"splice_null"` |
| `isOut` | `bool` | No | Out-of-network indicator. `true` for ad break start, `false` for return. Default `false`. |
| `durationMs` | `int` | No | Break duration in milliseconds. Used for auto-return timing. |
| `autoReturn` | `bool` | No | Automatically return to program when the break expires. Default `false`. |
| `preRollMs` | `int` | No | Schedule the splice this many milliseconds ahead using PTS. When omitted or `0`, the cue is injected immediately. |
| `eventId` | `uint32` | No | Explicit event ID. Auto-assigned if omitted. |
| `uniqueProgramId` | `uint16` | No | Identifies the program within the avail. |
| `availNum` | `uint8` | No | Avail number within the avail group. |
| `availsExpected` | `uint8` | No | Total number of avails expected in the group. |
| `descriptors` | `array` | No | Array of segmentation descriptors. Used with `time_signal` commands. |

### Descriptor Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `segmentationType` | `uint8` | Yes | Segmentation type ID (e.g., `52` for provider placement opportunity start) |
| `segEventId` | `uint32` | No | Segmentation event ID. Auto-assigned if omitted. |
| `durationMs` | `int` | No | Descriptor duration in milliseconds |
| `upidType` | `uint8` | Yes | UPID type (e.g., `15` for URI) |
| `upid` | `string` | Yes | UPID value |

**Response:** `200 OK`

```json
{
  "eventId": 42,
  "state": { "...ControlRoomState..." }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `eventId` | `uint32` | The assigned event ID (matches request or auto-generated) |
| `state` | `ControlRoomState` | Full switcher state after the cue injection |

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `commandType`, invalid JSON, or missing required fields |
| `500` | Encoding or injection failure |
| `501` | SCTE-35 not enabled (`--scte35` flag not set) |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/cue \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"commandType": "splice_insert", "isOut": true, "durationMs": 30000, "autoReturn": true}'
```

---

### POST /api/scte35/return

Return the most recent active event to program. Sends a splice_insert with `out_of_network_indicator` set to `false`, signaling the end of the current ad break. If multiple events are active, the most recently injected event is returned.

**Request Body:** None required (empty body or `{}`)

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `500` | No active events, or encoding failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/return \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/return/{eventId}

Return a specific active event to program by event ID.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `eventId` | Numeric event ID (uint32) |

**Request Body:** None required (empty body or `{}`)

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `eventId` (not a valid uint32) |
| `500` | Event not active, or encoding failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/return/42 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/cancel/{eventId}

Cancel a specific active event by sending a `splice_event_cancel_indicator`. This removes the event from the active events list and cancels any pending auto-return timer.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `eventId` | Numeric event ID (uint32) |

**Request Body:** None required (empty body or `{}`)

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `eventId` (not a valid uint32) |
| `500` | Event not active, or encoding failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/cancel/42 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/cancel-segmentation/{segEventId}

Cancel a specific segmentation event by sending a `segmentation_event_cancel_indicator`. This is used for cancelling individual segmentation descriptors within a time_signal command.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `segEventId` | Numeric segmentation event ID (uint32) |

**Request Body:** None required (empty body or `{}`)

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `segEventId` (not a valid uint32) |
| `500` | Encoding failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/cancel-segmentation/100 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/hold/{eventId}

Pause the auto-return timer for an active event. Use this when a live segment is running long and the break needs to be held past its scheduled return time. The event remains active but will not auto-return until released via return or extend.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `eventId` | Numeric event ID (uint32) |

**Request Body:** None required (empty body or `{}`)

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `eventId` (not a valid uint32) |
| `500` | Event not active |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/hold/42 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/extend/{eventId}

Extend the auto-return timer for an active event by adding additional time. The new duration is added to the remaining time (or the original duration if the event is held). A new splice_insert with the updated duration is injected.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `eventId` | Numeric event ID (uint32) |

**Request Body:**

```json
{
  "durationMs": 30000
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `durationMs` | `int` | Yes | Additional time in milliseconds to add to the break. Must be positive. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid `eventId`, missing `durationMs`, `durationMs` not positive, or invalid JSON |
| `500` | Event not active, or encoding failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/extend/42 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"durationMs": 30000}'
```

---

### GET /api/scte35/status

Get the full SCTE-35 subsystem status including configuration, active events, event log, and heartbeat state.

**Request Body:** None

**Response:** `200 OK` with `SCTE35Status`:

```json
{
  "enabled": true,
  "activeEvents": {
    "42": {
      "eventId": 42,
      "commandType": "splice_insert",
      "isOut": true,
      "durationMs": 30000,
      "elapsedMs": 5000,
      "remainingMs": 25000,
      "autoReturn": true,
      "held": false,
      "spliceTimePts": 8100000,
      "startedAt": 1709942400000,
      "descriptors": []
    }
  },
  "eventLog": [
    {
      "eventId": 42,
      "commandType": "splice_insert",
      "isOut": true,
      "durationMs": 30000,
      "autoReturn": true,
      "timestamp": 1709942400000,
      "status": "injected"
    }
  ],
  "heartbeatOk": true
}
```

### SCTE35Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether SCTE-35 is enabled |
| `activeEvents` | `object` | Map of event ID (as string key) to `ActiveEventState` |
| `eventLog` | `array` | Array of recent `EventLogEntry` objects (most recent first, up to 256) |
| `heartbeatOk` | `bool` | Whether the splice_null heartbeat goroutine is running |

### ActiveEventState Fields

| Field | Type | Description |
|-------|------|-------------|
| `eventId` | `uint32` | Event ID |
| `commandType` | `string` | `"splice_insert"` or `"time_signal"` |
| `isOut` | `bool` | Out-of-network indicator |
| `durationMs` | `int` or `null` | Break duration in milliseconds. Omitted when not set. |
| `elapsedMs` | `int` | Milliseconds elapsed since the event started |
| `remainingMs` | `int` or `null` | Milliseconds remaining before auto-return. Omitted when duration not set. |
| `autoReturn` | `bool` | Whether auto-return is enabled |
| `held` | `bool` | Whether the auto-return timer is paused |
| `spliceTimePts` | `int` | Splice time in 90 kHz PTS ticks |
| `startedAt` | `int` | Unix timestamp in milliseconds when the event started |
| `descriptors` | `array` | Array of segmentation descriptors. Omitted when empty. |

### EventLogEntry Fields

| Field | Type | Description |
|-------|------|-------------|
| `eventID` | `uint32` | Event ID |
| `commandType` | `string` | `"splice_insert"` or `"time_signal"` |
| `isOut` | `bool` | Out-of-network indicator |
| `durationMs` | `int` or `null` | Break duration in milliseconds. Omitted when not set. |
| `autoReturn` | `bool` | Whether auto-return was enabled |
| `timestamp` | `int` | Unix timestamp in milliseconds |
| `status` | `string` | Event status: `"injected"`, `"returned"`, `"cancelled"`, `"held"`, or `"extended"` |
| `descriptors` | `array` | Segmentation descriptors. Omitted when empty. |
| `spliceTimePts` | `int` or `null` | Splice time in PTS ticks. Omitted when not set. |
| `source` | `string` | Event source identifier. Omitted when not set. |
| `availNum` | `uint8` | Avail number. Omitted when `0`. |
| `availsExpected` | `uint8` | Total avails expected. Omitted when `0`. |

**Example:**

```bash
curl http://localhost:8081/api/scte35/status \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/scte35/log

Get the SCTE-35 event log. Returns an array of recent event log entries, ordered from oldest to newest, up to 256 entries.

**Request Body:** None

**Response:** `200 OK` with array of `EventLogEntry`:

```json
[
  {
    "eventID": 42,
    "commandType": "splice_insert",
    "isOut": true,
    "durationMs": 30000,
    "autoReturn": true,
    "timestamp": 1709942400000,
    "status": "injected"
  },
  {
    "eventID": 42,
    "commandType": "splice_insert",
    "isOut": false,
    "autoReturn": true,
    "timestamp": 1709942430000,
    "status": "returned"
  }
]
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl http://localhost:8081/api/scte35/log \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/scte35/active

Get the list of currently active event IDs. Returns an array of uint32 event IDs.

**Request Body:** None

**Response:** `200 OK` with array of event IDs:

```json
[42, 43, 44]
```

If no events are active, returns an empty array `[]`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl http://localhost:8081/api/scte35/active \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/scte35/rules

List all signal conditioning rules. Rules are evaluated in priority order (first match wins) against incoming SCTE-35 signals for pass-through processing.

**Request Body:** None

**Response:** `200 OK` with array of `Rule`:

```json
[
  {
    "id": "a1b2c3d4",
    "name": "Strip short avails",
    "enabled": true,
    "priority": 1,
    "conditions": [
      { "field": "command_type", "operator": "=", "value": "5" },
      { "field": "duration", "operator": "<", "value": "15000" }
    ],
    "logic": "and",
    "action": "delete",
    "destinations": []
  }
]
```

If no rules exist, returns an empty array `[]`.

### Rule Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique rule identifier (auto-assigned on creation) |
| `name` | `string` | Human-readable rule name |
| `enabled` | `bool` | Whether the rule is active for evaluation |
| `priority` | `int` | Evaluation priority (lower = higher priority). Omitted when `0`. |
| `conditions` | `array` | Array of `RuleCondition` objects. Rules with no conditions match everything. |
| `logic` | `string` | How conditions combine: `"and"` (all must match) or `"or"` (any must match). Default `"and"`. |
| `action` | `string` | Action on match: `"pass"` (allow through), `"delete"` (drop signal), or `"replace"` (modify signal) |
| `replaceWith` | `object` or `null` | Replacement parameters when `action` is `"replace"`. Omitted otherwise. |
| `destinations` | `array` | Restrict rule to specific destination IDs. Empty array means all destinations. |

### RuleCondition Fields

| Field | Type | Description |
|-------|------|-------------|
| `field` | `string` | Field to match against (e.g., `"command_type"`, `"duration"`, `"segmentation_type"`) |
| `operator` | `string` | Comparison operator: `"="`, `"!="`, `">"`, `"<"`, `">="`, `"<="`, `"range"`, `"contains"`, or `"matches"` |
| `value` | `string` | Value to compare against. For `"range"`, use format `"min-max"`. For `"matches"`, use a regex pattern. |

### ReplaceParams Fields

| Field | Type | Description |
|-------|------|-------------|
| `duration` | `duration` or `null` | Replacement break duration. Omitted when not set. |
| `eventID` | `uint32` or `null` | Replacement event ID. Omitted when not set. |

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl http://localhost:8081/api/scte35/rules \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/rules

Create a new signal conditioning rule. The rule ID is auto-assigned.

**Request Body:**

```json
{
  "name": "Strip short avails",
  "enabled": true,
  "priority": 1,
  "conditions": [
    { "field": "command_type", "operator": "=", "value": "5" },
    { "field": "duration", "operator": "<", "value": "15000" }
  ],
  "logic": "and",
  "action": "delete"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Human-readable rule name |
| `enabled` | `bool` | Yes | Whether the rule is active |
| `priority` | `int` | No | Evaluation priority (lower = higher priority) |
| `conditions` | `array` | No | Array of `RuleCondition` objects |
| `logic` | `string` | No | Condition logic: `"and"` or `"or"`. Default `"and"`. |
| `action` | `string` | Yes | Action: `"pass"`, `"delete"`, or `"replace"` |
| `replaceWith` | `object` | No | Replacement parameters (required when `action` is `"replace"`) |
| `destinations` | `array` | No | Restrict to specific destination IDs |

**Response:** `200 OK` with the created `Rule` (including auto-assigned `id`):

```json
{
  "id": "a1b2c3d4",
  "name": "Strip short avails",
  "enabled": true,
  "priority": 1,
  "conditions": [
    { "field": "command_type", "operator": "=", "value": "5" },
    { "field": "duration", "operator": "<", "value": "15000" }
  ],
  "logic": "and",
  "action": "delete"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `500` | Storage failure |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/rules \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Strip short avails", "enabled": true, "conditions": [{"field": "duration", "operator": "<", "value": "15000"}], "action": "delete"}'
```

---

### PUT /api/scte35/rules/{id}

Update an existing signal conditioning rule. The rule ID in the URL must match an existing rule.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Rule ID |

**Request Body:**

```json
{
  "name": "Updated rule name",
  "enabled": true,
  "priority": 2,
  "conditions": [
    { "field": "duration", "operator": ">=", "value": "30000" }
  ],
  "logic": "and",
  "action": "pass"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Updated rule name |
| `enabled` | `bool` | Yes | Whether the rule is active |
| `priority` | `int` | No | Evaluation priority |
| `conditions` | `array` | No | Array of `RuleCondition` objects |
| `logic` | `string` | No | Condition logic: `"and"` or `"or"` |
| `action` | `string` | Yes | Action: `"pass"`, `"delete"`, or `"replace"` |
| `replaceWith` | `object` | No | Replacement parameters |
| `destinations` | `array` | No | Restrict to specific destination IDs |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `404` | Rule not found |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/scte35/rules/a1b2c3d4 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Allow long avails", "enabled": true, "conditions": [{"field": "duration", "operator": ">=", "value": "30000"}], "action": "pass"}'
```

---

### DELETE /api/scte35/rules/{id}

Delete a signal conditioning rule by ID. This is irreversible.

**URL Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Rule ID |

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Rule not found |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/scte35/rules/a1b2c3d4 \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/scte35/rules/default

Set the default action for signals that do not match any rule. The default action applies when no rules match an incoming SCTE-35 signal.

**Request Body:**

```json
{
  "action": "pass"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `action` | `string` | Yes | Default action: `"pass"` or `"delete"` |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `action` or invalid JSON |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/scte35/rules/default \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"action": "pass"}'
```

---

### POST /api/scte35/rules/reorder

Reorder signal conditioning rules by providing the complete list of rule IDs in the desired evaluation order. All existing rule IDs must be included.

**Request Body:**

```json
{
  "ids": ["rule-id-2", "rule-id-1", "rule-id-3"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ids` | `string[]` | Yes | Ordered list of all rule IDs |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, missing IDs, or IDs do not match existing rules |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/rules/reorder \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ids": ["rule-id-2", "rule-id-1", "rule-id-3"]}'
```

---

### GET /api/scte35/rules/templates

List available preset rule templates. Templates are pre-configured rules that can be instantiated via `POST /api/scte35/rules/from-template`. Templates do not have IDs assigned.

**Request Body:** None

**Response:** `200 OK` with array of `Rule` objects (without `id` fields):

```json
[
  {
    "name": "Strip short avails",
    "enabled": true,
    "conditions": [
      { "field": "duration", "operator": "<", "value": "15000" }
    ],
    "logic": "and",
    "action": "delete"
  },
  {
    "name": "Pass all signals",
    "enabled": true,
    "conditions": [],
    "logic": "and",
    "action": "pass"
  }
]
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl http://localhost:8081/api/scte35/rules/templates \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/scte35/rules/from-template

Create a new rule from a preset template by name. The template is instantiated as a new rule with an auto-assigned ID.

**Request Body:**

```json
{
  "name": "Strip short avails"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Template name. Must match an available template exactly. |

**Response:** `200 OK` with the created `Rule` (including auto-assigned `id`):

```json
{
  "id": "e5f6g7h8",
  "name": "Strip short avails",
  "enabled": true,
  "conditions": [
    { "field": "duration", "operator": "<", "value": "15000" }
  ],
  "logic": "and",
  "action": "delete"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `name` or invalid JSON |
| `404` | Template not found |
| `501` | SCTE-35 not enabled |

**Example:**

```bash
curl -X POST http://localhost:8081/api/scte35/rules/from-template \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Strip short avails"}'
```

---

## Captions

Closed captioning support with three operating modes: `off` (no captions), `passthrough` (relay captions from upstream sources), and `author` (manually type caption text via the API). Captions are encoded as CEA-608 data embedded in the program output.

Caption endpoints are only registered when the caption manager is enabled. If captions are not enabled, these routes return `404 Not Found` (unregistered).

### POST /api/captions/mode

Set the caption operating mode.

**Request Body:**

```json
{
  "mode": "author"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `string` | Caption mode: `"off"`, `"passthrough"`, or `"author"` |

**Response:** `200 OK` with current caption state:

```json
{
  "mode": "author"
}
```

Fields `authorBuffer` and `sourceCaptions` are omitted when empty.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or invalid mode value |

**Example:**

```bash
curl -X POST http://localhost:8081/api/captions/mode \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"mode": "author"}'
```

---

### POST /api/captions/text

Ingest caption text, trigger a newline, or clear the caption display. Requires `author` mode to be active. Exactly one of `text`, `newline`, or `clear` must be provided.

**Request Body:**

```json
{
  "text": "Hello, viewers!"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `text` | `string` | Caption text to append (optional) |
| `newline` | `bool` | Trigger a new caption line (optional) |
| `clear` | `bool` | Clear the caption display (optional) |

**Response:** `200 OK` with updated caption state

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, not in author mode, or no action specified |

**Examples:**

```bash
# Send text
curl -X POST http://localhost:8081/api/captions/text \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text": "Hello, viewers!"}'

# New line
curl -X POST http://localhost:8081/api/captions/text \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"newline": true}'

# Clear display
curl -X POST http://localhost:8081/api/captions/text \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"clear": true}'
```

---

### GET /api/captions/state

Get the current caption system state, including operating mode, author buffer contents, and which sources have active upstream captions.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "mode": "passthrough",
  "authorBuffer": "",
  "sourceCaptions": {
    "cam1": true,
    "cam3": true
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `mode` | `string` | Current caption mode (`"off"`, `"passthrough"`, `"author"`) |
| `authorBuffer` | `string` | Pending author text not yet sent (only in `author` mode) |
| `sourceCaptions` | `object` | Map of source keys to `true` for sources with active upstream captions |

**Example:**

```bash
curl http://localhost:8081/api/captions/state \
  -H "Authorization: Bearer $TOKEN"
```

---

## Clips

Media clip storage and 4-slot clip player for pre-recorded content playback. Clips can be uploaded as media files (MP4, MOV, TS, MKV, AVI, WebM) or imported from recordings. Non-H.264 files are automatically transcoded. Each clip player slot can independently load, play, pause, seek, and loop clips. Clip player sources appear as switchable sources alongside cameras. Clip endpoints are only registered when a `ClipManager` and `ClipStore` are configured.

### GET /api/clips

List all clips in the library, sorted by creation time.

**Request Body:** None

**Response:** `200 OK` with a JSON array of clip objects:

```json
[
  {
    "id": "abc123",
    "name": "Intro Video",
    "filename": "abc123.ts",
    "source": "upload",
    "codec": "h264",
    "audioCodec": "aac",
    "width": 1920,
    "height": 1080,
    "fpsNum": 30000,
    "fpsDen": 1001,
    "durationMs": 15000,
    "sampleRate": 48000,
    "channels": 2,
    "byteSize": 5242880,
    "loop": false,
    "createdAt": "2026-03-20T10:30:00Z",
    "ephemeral": false
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `string` | Unique clip identifier |
| `name` | `string` | Display name |
| `filename` | `string` | Storage filename |
| `source` | `string` | How the clip was ingested: `"upload"`, `"replay"`, or `"recording"` |
| `codec` | `string` | Video codec (e.g., `"h264"`) |
| `audioCodec` | `string` | Audio codec (e.g., `"aac"`), empty if no audio |
| `width` | `int` | Video width in pixels |
| `height` | `int` | Video height in pixels |
| `fpsNum` | `int` | Frame rate numerator |
| `fpsDen` | `int` | Frame rate denominator |
| `durationMs` | `int` | Duration in milliseconds |
| `sampleRate` | `int` | Audio sample rate in Hz |
| `channels` | `int` | Audio channel count |
| `byteSize` | `int` | File size in bytes |
| `loop` | `bool` | Whether the clip is set to loop |
| `createdAt` | `string` | ISO 8601 creation timestamp |
| `ephemeral` | `bool` | If `true`, clip may be auto-cleaned (e.g., replay captures) |

**Example:**

```bash
curl http://localhost:8081/api/clips \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/clips/{id}

Return metadata for a single clip.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Clip ID |

**Request Body:** None

**Response:** `200 OK` with a clip object (same schema as list items above).

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Clip not found |

**Example:**

```bash
curl http://localhost:8081/api/clips/abc123 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/upload

Upload a media file as a new clip. Accepts multipart form data with a maximum file size of 2 GB. Non-H.264 files are automatically transcoded to H.264+AAC MPEG-TS. Upload progress is broadcast to connected clients via state updates. Only one upload can be in progress at a time.

**Request:** Multipart form data.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `file` | file | Yes | Media file to upload. Accepted extensions: `.ts`, `.mp4`, `.mov`, `.mkv`, `.avi`, `.webm` |

**Response:** `201 Created` with the new clip object.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `file` field, unsupported file type, invalid format, or corrupt file |
| `409` | Another upload is already in progress |
| `500` | Internal error writing or transcoding file |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/upload \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@/path/to/video.mp4"
```

---

### PUT /api/clips/{id}

Update mutable fields on a clip (name and/or loop setting).

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Clip ID |

**Request Body:**

```json
{
  "name": "New Name",
  "loop": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | No | New display name for the clip |
| `loop` | `bool` | No | Set the default loop behavior |

**Response:** `200 OK` with the updated clip object.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON |
| `404` | Clip not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/clips/abc123 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "Intro v2", "loop": true}'
```

---

### DELETE /api/clips/{id}

Delete a clip from the library and remove its media file from disk.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Clip ID |

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Clip not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/clips/abc123 \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/{id}/pin

Pin an ephemeral clip (e.g., a replay capture) to make it permanent, preventing auto-cleanup.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `id` | Clip ID |

**Request Body:** None

**Response:** `200 OK` with the updated clip object (ephemeral will be `false`).

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Clip not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/abc123/pin \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/clips/recordings

List recording files available for import as clips. Returns `.ts` files from the recording directory, excluding the currently active recording (if any).

**Request Body:** None

**Response:** `200 OK` with a JSON array of recording file info:

```json
[
  {
    "filename": "switchframe-2026-03-20T14-30-00.ts",
    "path": "/recordings/switchframe-2026-03-20T14-30-00.ts",
    "byteSize": 104857600,
    "modTime": "2026-03-20T14:45:00Z"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `filename` | `string` | Recording filename |
| `path` | `string` | Full filesystem path |
| `byteSize` | `int` | File size in bytes |
| `modTime` | `string` | Last modification time (ISO 8601) |

**Example:**

```bash
curl http://localhost:8081/api/clips/recordings \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/from-recording

Import a recording file as a clip. The recording file is copied (not moved) into the clip store, validated, and added to the library.

**Request Body:**

```json
{
  "path": "/recordings/switchframe-2026-03-20T14-30-00.ts"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | `string` | Yes | Full filesystem path to the recording file. Must be within the configured recording directory. |

**Response:** `201 Created` with the new clip object.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing path, path outside recording directory, recording directory not configured, or invalid file |
| `500` | Internal error copying or validating file |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/from-recording \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path": "/recordings/switchframe-2026-03-20T14-30-00.ts"}'
```

---

### GET /api/clips/players

Return the state of all 4 clip player slots.

**Request Body:** None

**Response:** `200 OK` with a JSON array of player states:

```json
[
  {
    "id": 1,
    "clipId": "abc123",
    "clipName": "Intro Video",
    "state": "playing",
    "speed": 1.0,
    "position": 0.45,
    "loop": false
  },
  {
    "id": 2,
    "state": "empty"
  },
  {
    "id": 3,
    "state": "empty"
  },
  {
    "id": 4,
    "state": "empty"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | `int` | Player slot number (1-4) |
| `clipId` | `string` | ID of the loaded clip (empty when slot is empty) |
| `clipName` | `string` | Name of the loaded clip |
| `state` | `string` | One of: `"empty"`, `"loaded"`, `"playing"`, `"paused"`, `"holding"` |
| `speed` | `float` | Current playback speed |
| `position` | `float` | Current position (0.0 to 1.0) |
| `loop` | `bool` | Whether looping is enabled |

**Example:**

```bash
curl http://localhost:8081/api/clips/players \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/players/{n}/load

Load a clip into a player slot. The player must be empty or stopped.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:**

```json
{
  "clipId": "abc123"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `clipId` | `string` | Yes | ID of the clip to load |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID, missing `clipId`, or invalid JSON |
| `404` | Clip not found |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/load \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"clipId": "abc123"}'
```

---

### POST /api/clips/players/{n}/eject

Eject the clip from a player slot, returning it to the empty state.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID or player slot is empty |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/eject \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/players/{n}/play

Start or resume clip playback. Optionally set speed and loop mode.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:**

```json
{
  "speed": 1.0,
  "loop": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `speed` | `float` | No | Playback speed (0.25 to 2.0). Defaults to `1.0`. |
| `loop` | `bool` | No | Enable looping. Defaults to `false`. |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID, invalid JSON, or player slot is empty |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/play \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"speed": 0.5, "loop": true}'
```

---

### POST /api/clips/players/{n}/pause

Pause clip playback. The clip remains at its current position.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID or player is not playing |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/pause \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/players/{n}/stop

Stop clip playback. The clip remains loaded but resets to the beginning.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID or player slot is empty |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/stop \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/clips/players/{n}/seek

Seek to a normalized position within the clip.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:**

```json
{
  "position": 0.5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `position` | `float` | Yes | Seek position from `0.0` (start) to `1.0` (end) |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID, invalid JSON, or position out of range |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/seek \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"position": 0.75}'
```

---

### POST /api/clips/players/{n}/speed

Change the playback speed of a playing clip.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:**

```json
{
  "speed": 0.5
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `speed` | `float` | Yes | Playback speed from `0.25` to `2.0` |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID, invalid JSON, or speed out of range |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/speed \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"speed": 2.0}'
```

---

### POST /api/clips/players/{n}/loop

Change the loop setting of a playing or loaded clip.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `n` | Player slot number (1-4) |

**Request Body:**

```json
{
  "loop": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `loop` | `bool` | Yes | Enable or disable looping |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid player ID or invalid JSON |

**Example:**

```bash
curl -X POST http://localhost:8081/api/clips/players/1/loop \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"loop": true}'
```

---

## ST Maps

ST maps (per-pixel coordinate remapping) apply spatial warping effects to individual sources or the program output. Maps can be generated from built-in generators (lens correction, creative effects) or uploaded as EXR, PNG, or raw binary files. Animated ST maps support multi-frame looping effects. ST map endpoints are only registered when an `STMapRegistry` is configured.

### GET /api/stmap

List all stored ST map names.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "maps": ["barrel-correction", "fisheye-fix", "heat-shimmer"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `maps` | `array` | List of stored map names |

**Example:**

```bash
curl http://localhost:8081/api/stmap \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/stmap/state

Return the current ST map assignment state, showing which maps are assigned to sources and the program output.

**Request Body:** None

**Response:** `200 OK` with the assignment state object.

**Example:**

```bash
curl http://localhost:8081/api/stmap/state \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/stmap/generators

List all available ST map generators with their parameter schemas.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "generators": [
    {
      "name": "barrel",
      "displayName": "Barrel Distortion",
      "params": {
        "strength": {"min": 0, "max": 1, "default": 0.5}
      },
      "animated": false
    }
  ]
}
```

**Example:**

```bash
curl http://localhost:8081/api/stmap/generators \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/stmap/generate

Generate a new ST map using a built-in generator and store it. Supports both static and animated (multi-frame) generators. Optionally assigns the generated map to a source or the program output in the same request.

**Request Body:**

```json
{
  "type": "barrel",
  "name": "my-barrel-fix",
  "params": {"strength": 0.3},
  "width": 1920,
  "height": 1080,
  "frame_count": 90,
  "assign_source": "cam1",
  "assign_program": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | Yes | Generator type name |
| `name` | `string` | Yes | Name to store the map under |
| `params` | `object` | No | Generator parameters (key-value pairs of float values) |
| `width` | `int` | No | Map width in pixels. Defaults to pipeline format width. |
| `height` | `int` | No | Map height in pixels. Defaults to pipeline format height. |
| `frame_count` | `int` | No | Number of frames for animated generators (1-300). Defaults to 90. |
| `assign_source` | `string` | No | Source key to assign the map to immediately |
| `assign_program` | `bool` | No | If `true`, assign the map to program output immediately |

**Response:** `201 Created`

```json
{
  "name": "my-barrel-fix",
  "width": 1920,
  "height": 1080,
  "type": "static"
}
```

For animated maps, the response also includes `"frame_count"`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `type` or `name`, unknown generator, invalid parameters, or `frame_count` > 300 |

**Example:**

```bash
curl -X POST http://localhost:8081/api/stmap/generate \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"type": "barrel", "name": "cam1-fix", "params": {"strength": 0.3}, "assign_source": "cam1"}'
```

---

### POST /api/stmap/upload/{name}

Upload an ST map file (EXR, PNG, or raw binary). The format is auto-detected from the Content-Type header, file extension, or magic bytes. Maximum upload size is 64 MB.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Name to store the map under (may include extension: `.exr`, `.png`, `.stmap`) |

**Request Body:** Raw file bytes. Set `Content-Type` to `image/x-exr`, `image/png`, or `application/octet-stream`.

**Response:** `201 Created`

```json
{
  "name": "my-custom-map",
  "width": 1920,
  "height": 1080,
  "type": "static"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid name or failed to parse upload |
| `413` | Upload exceeds 64 MB size limit |

**Example:**

```bash
curl -X POST http://localhost:8081/api/stmap/upload/lens-fix.exr \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: image/x-exr" \
  --data-binary @/path/to/lens-correction.exr
```

---

### GET /api/stmap/{name}

Return metadata about a stored ST map.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Map name |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "name": "barrel-correction",
  "width": 1920,
  "height": 1080,
  "type": "static"
}
```

For animated maps:

```json
{
  "name": "heat-shimmer",
  "width": 1920,
  "height": 1080,
  "type": "animated",
  "frame_count": 90
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Map not found |

**Example:**

```bash
curl http://localhost:8081/api/stmap/barrel-correction \
  -H "Authorization: Bearer $TOKEN"
```

---

### DELETE /api/stmap/{name}

Delete a stored ST map. If the map is currently assigned to any source or the program, the assignment is removed.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Map name |

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Map not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/stmap/barrel-correction \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/stmap/{name}/download

Download a static ST map as raw binary data.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Map name |

**Request Body:** None

**Response:** `200 OK` with `Content-Type: application/octet-stream` and `Content-Disposition: attachment; filename="<name>.stmap"`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | Map not found |
| `500` | Internal error serializing map |

**Example:**

```bash
curl -O http://localhost:8081/api/stmap/barrel-correction/download \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/stmap/source/{sourceKey}

Assign an ST map to a specific source. The map must already be stored.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `sourceKey` | Source key (e.g., `"cam1"`) |

**Request Body:**

```json
{
  "map": "barrel-correction"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `map` | `string` | Yes | Name of the map to assign |

**Response:** `200 OK` with the updated assignment state.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `map` name or invalid JSON |
| `404` | Map not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/stmap/source/cam1 \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"map": "barrel-correction"}'
```

---

### DELETE /api/stmap/source/{sourceKey}

Remove the ST map assignment from a source.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `sourceKey` | Source key (e.g., `"cam1"`) |

**Request Body:** None

**Response:** `204 No Content`

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/stmap/source/cam1 \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/stmap/program

Assign an ST map to the program output.

**Request Body:**

```json
{
  "map": "heat-shimmer"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `map` | `string` | Yes | Name of the map to assign |

**Response:** `200 OK` with the updated assignment state.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `map` name or invalid JSON |
| `404` | Map not found |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/stmap/program \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"map": "heat-shimmer"}'
```

---

### DELETE /api/stmap/program

Remove the ST map assignment from the program output.

**Request Body:** None

**Response:** `204 No Content`

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/stmap/program \
  -H "Authorization: Bearer $TOKEN"
```

---

## Format

The format API allows querying and changing the pipeline video format (resolution and frame rate). Changes take effect on the next keyframe.

### GET /api/format

Retrieve the current pipeline format and available presets.

**Request Body:** None

**Response:** `200 OK` with format info and preset list:

```json
{
  "format": {
    "width": 1920,
    "height": 1080,
    "fpsNum": 30000,
    "fpsDen": 1001,
    "name": "1080p29.97"
  },
  "presets": ["720p25", "720p29.97", "720p30", "720p50", "720p59.94", "720p60", "1080p23.976", "1080p24", "1080p25", "1080p29.97", "1080p30", "1080p50", "1080p59.94", "1080p60", "2160p25", "2160p29.97", "2160p30", "2160p50", "2160p59.94", "2160p60"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `format.width` | `int` | Current pipeline width in pixels |
| `format.height` | `int` | Current pipeline height in pixels |
| `format.fpsNum` | `int` | Frame rate numerator (e.g., `30000` for 29.97fps) |
| `format.fpsDen` | `int` | Frame rate denominator (e.g., `1001` for 29.97fps) |
| `format.name` | `string` | Preset name if a known preset matches, otherwise empty |
| `presets` | `string[]` | List of available format preset names |

**Example:**

```bash
curl https://localhost:8080/api/format \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/format

Change the pipeline format. Provide either a preset name or custom dimensions.

**Request Body (preset):**

```json
{
  "format": "1080p25"
}
```

**Request Body (custom):**

```json
{
  "width": 1280,
  "height": 720,
  "fpsNum": 50,
  "fpsDen": 1
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `format` | `string` | One of | Preset name (e.g., `"1080p29.97"`, `"720p50"`) |
| `width` | `int` | One of | Custom width in pixels. Range: `320`-`7680`. Must be even. |
| `height` | `int` | One of | Custom height in pixels. Range: `180`-`4320`. Must be even. |
| `fpsNum` | `int` | One of | Frame rate numerator. Must be positive. |
| `fpsDen` | `int` | One of | Frame rate denominator. Must be positive. |

Provide either `format` (preset name) or all four custom fields (`width`, `height`, `fpsNum`, `fpsDen`).

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Unknown preset name, dimensions out of range, odd width/height, or invalid JSON |

**Example:**

```bash
curl -X PUT https://localhost:8080/api/format \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"format": "1080p25"}'
```

---

## Encoder

Runtime video encoder selection. The server auto-detects available hardware encoders at startup (NVENC, VA-API, VideoToolbox) and falls back to software x264. These endpoints allow querying and switching the active encoder without restarting.

### GET /api/encoder

Return the currently active video encoder and the list of all available encoders.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "current": "videotoolbox",
  "available": [
    {"name": "videotoolbox", "displayName": "VideoToolbox (Apple)", "isDefault": true},
    {"name": "x264", "displayName": "x264 (Software)", "isDefault": false}
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `current` | `string` | Name of the currently active encoder |
| `available` | `array` | List of available encoders |
| `available[].name` | `string` | Encoder identifier used in `PUT` requests |
| `available[].displayName` | `string` | Human-readable encoder name |
| `available[].isDefault` | `bool` | Whether this is the auto-detected default encoder |

**Example:**

```bash
curl http://localhost:8081/api/encoder \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/encoder

Switch the active video encoder at runtime. The pipeline rebuilds with the new encoder. Active transitions or recordings continue uninterrupted.

**Request Body:**

```json
{
  "encoder": "x264"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `encoder` | `string` | Yes | Encoder name from the `available` list |

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or missing encoder name |
| `404` | Encoder not found in available list |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/encoder \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"encoder": "x264"}'
```

---

## ASR (Automatic Speech Recognition)

Automatic speech recognition using Whisper models for live captioning. ASR endpoints are always registered but return `501 Not Implemented` when the ASR engine is not available (no Whisper model installed or ASR manager not configured).

When ASR is not configured at all (no `ASRManager` wired), the status endpoint returns `{"available": false}` with all other fields at zero values.

### GET /api/asr/status

Return the current ASR system state including availability, active status, language, model, and tuning parameters.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "available": true,
  "active": true,
  "translate": false,
  "modelName": "large-v3-turbo",
  "language": "en",
  "tentative": "",
  "confidence": 0.0,
  "speed": "balanced",
  "confidenceFilter": "medium",
  "noiseFloor": "normal",
  "speakerDetection": "normal"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `available` | `bool` | Whether an ASR engine is present and usable |
| `active` | `bool` | Whether ASR is currently running |
| `translate` | `bool` | Whether translation to English is enabled |
| `modelName` | `string` | Name of the loaded Whisper model (e.g., `"large-v3-turbo"`) |
| `language` | `string` | ISO 639-1 language code (e.g., `"en"`, `"fr"`, `"es"`) |
| `tentative` | `string` | Tentative (unconfirmed) transcription text. Empty when idle. |
| `confidence` | `float` | Confidence score of the last transcription segment. `0.0` when idle. |
| `speed` | `string` | Inference speed preset: `"fast"`, `"balanced"`, or `"accurate"` |
| `confidenceFilter` | `string` | Confidence holdback preset: `"low"`, `"medium"`, or `"high"` |
| `noiseFloor` | `string` | VAD sensitivity preset: `"quiet"`, `"normal"`, or `"noisy"` |
| `speakerDetection` | `string` | Speaker change gap preset: `"sensitive"`, `"normal"`, or `"relaxed"` |

When ASR is not configured, only `available: false` is meaningful; all other fields are zero values.

**Example:**

```bash
curl http://localhost:8081/api/asr/status \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/asr/config

Update ASR configuration. Accepts a partial JSON body -- only the fields you include are changed. All fields are optional; omitted fields retain their current values.

**Request Body:**

```json
{
  "active": true,
  "language": "es",
  "translate": true,
  "model": "large-v3-turbo",
  "speed": "fast",
  "confidenceFilter": "high",
  "noiseFloor": "noisy",
  "speakerDetection": "sensitive"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `active` | `bool` | No | Enable or disable ASR |
| `language` | `string` | No | ISO 639-1 language code (e.g., `"en"`, `"fr"`, `"ja"`) |
| `translate` | `bool` | No | Enable translation to English |
| `model` | `string` | No | Switch to a different Whisper model by name |
| `speed` | `string` | No | Inference speed preset: `"fast"`, `"balanced"`, or `"accurate"` |
| `confidenceFilter` | `string` | No | Confidence holdback: `"low"`, `"medium"`, or `"high"` |
| `noiseFloor` | `string` | No | VAD sensitivity: `"quiet"`, `"normal"`, or `"noisy"` |
| `speakerDetection` | `string` | No | Speaker change gap: `"sensitive"`, `"normal"`, or `"relaxed"` |

The `model` field is processed first (before `active`), so you can switch models and activate in a single request.

**Response:** `200 OK` with updated `ASRState`:

```json
{
  "available": true,
  "active": true,
  "translate": true,
  "modelName": "large-v3-turbo",
  "language": "es",
  "speed": "fast",
  "confidenceFilter": "high",
  "noiseFloor": "noisy",
  "speakerDetection": "sensitive"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or invalid model name |
| `500` | Internal error activating/deactivating ASR |
| `501` | ASR not available (no engine configured or engine reports unavailable) |

**Examples:**

```bash
# Enable ASR with Spanish language
curl -X PUT http://localhost:8081/api/asr/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"active": true, "language": "es"}'

# Change just the speed preset
curl -X PUT http://localhost:8081/api/asr/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"speed": "accurate"}'

# Switch model and enable translation
curl -X PUT http://localhost:8081/api/asr/config \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model": "large-v3", "translate": true}'
```

---

### GET /api/asr/models

List available Whisper models installed on the server. The server scans its models directory for `ggml-*.bin` files.

**Request Body:** None

**Response:** `200 OK` with a JSON array of model name strings:

```json
["base", "medium.en", "large-v3", "large-v3-turbo"]
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `501` | ASR not available (no ASR manager configured) |

**Example:**

```bash
curl http://localhost:8081/api/asr/models \
  -H "Authorization: Bearer $TOKEN"
```

---

## Color Grading

Color grading applies 3D LUT (Look-Up Table) color transforms to the program output. The system includes built-in looks and supports uploading custom `.cube` LUT files. Color grading endpoints are only registered when a `ColorGradeManager` is configured; otherwise these routes return `404 Not Found` (unregistered).

### GET /api/colorgrade

Return the current color grading state, including whether grading is enabled, the active look name, intensity, and list of available looks.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "enabled": true,
  "lookName": "cinematic-warm",
  "intensity": 0.75,
  "available": ["cinematic-warm", "cool-blue", "film-noir", "vintage", "my-custom-lut"]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether color grading is active |
| `lookName` | `string` | Name of the currently applied look. Empty when disabled. |
| `intensity` | `float` | Blend intensity from `0.0` (original) to `1.0` (full effect) |
| `available` | `array` | List of all available look names (built-in + uploaded) |

**Example:**

```bash
curl http://localhost:8081/api/colorgrade \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/colorgrade

Enable, configure, or disable color grading. To disable, set `enabled` to `false`. To enable or change the look, provide `lookName` and optionally `intensity`. To adjust only the intensity of the currently active look, provide `intensity` alone (without `lookName`).

**Request Body:**

```json
{
  "enabled": true,
  "lookName": "cinematic-warm",
  "intensity": 0.8
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | `bool` | No | Set to `false` to disable color grading |
| `lookName` | `string` | Conditional | Name of the look to apply. Required when enabling unless only adjusting intensity. |
| `intensity` | `float` | No | Blend intensity from `0.0` to `1.0`. Defaults to `1.0` if not specified. |

**Response:** `200 OK` with full `ControlRoomState`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, missing `lookName` when required, look not found, or no active look for intensity-only update |

**Examples:**

```bash
# Enable a look at full intensity
curl -X PUT http://localhost:8081/api/colorgrade \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lookName": "cinematic-warm"}'

# Enable at reduced intensity
curl -X PUT http://localhost:8081/api/colorgrade \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lookName": "film-noir", "intensity": 0.5}'

# Adjust intensity of current look
curl -X PUT http://localhost:8081/api/colorgrade \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"intensity": 0.3}'

# Disable color grading
curl -X PUT http://localhost:8081/api/colorgrade \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

---

### POST /api/colorgrade/upload

Upload a custom `.cube` LUT file. The file is validated (parsed as a 3D LUT) before being saved. The look becomes immediately available in the `available` list.

**Request:** Multipart form data with a maximum upload size of 16 MB.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` (form value) | Yes | Name for the uploaded look. Must be alphanumeric with hyphens/underscores, max 64 characters. |
| `file` | file (form file) | Yes | The `.cube` LUT file to upload |

**Response:** `201 Created` with updated color grade state:

```json
{
  "enabled": false,
  "lookName": "",
  "intensity": 0,
  "available": ["cinematic-warm", "cool-blue", "film-noir", "my-custom-lut", "vintage"]
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing `name`, missing `file`, invalid `.cube` file format, or invalid name |
| `413` | Upload exceeds 16 MB size limit |

**Example:**

```bash
curl -X POST http://localhost:8081/api/colorgrade/upload \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=my-custom-lut" \
  -F "file=@/path/to/my-look.cube"
```

---

### DELETE /api/colorgrade/{name}

Delete an uploaded look. Built-in looks cannot be deleted.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Name of the look to delete |

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing or invalid name |
| `403` | Attempted to delete a built-in look |
| `404` | Look not found |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/colorgrade/my-custom-lut \
  -H "Authorization: Bearer $TOKEN"
```

---

### GET /api/colorgrade/corrector

Return the current color corrector state. The corrector provides real-time color adjustments (lift/gamma/gain, temperature, tint, saturation, contrast, shadows/midtones/highlights, tone curves, and sharpness) that are applied on top of the active LUT look.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "lift": [0.0, 0.0, 0.0],
  "gamma": [1.0, 1.0, 1.0],
  "gain": [1.0, 1.0, 1.0],
  "temperature": 0,
  "tint": 0,
  "saturation": 1.0,
  "contrast": 0,
  "shadows": [0.0, 0.0, 0.0],
  "midtones": [0.0, 0.0, 0.0],
  "highlights": [0.0, 0.0, 0.0],
  "curveMaster": [{"x": 0, "y": 0}, {"x": 1, "y": 1}],
  "curveR": [],
  "curveG": [],
  "curveB": [],
  "sharpness": 0
}
```

| Field | Type | Range | Description |
|-------|------|-------|-------------|
| `lift` | `[R,G,B]` | -1 to +1 | Shadow lift (RGB offsets) |
| `gamma` | `[R,G,B]` | 0.1 to 4.0 | Midtone gamma (RGB multipliers) |
| `gain` | `[R,G,B]` | 0 to 4.0 | Highlight gain (RGB multipliers) |
| `temperature` | `float` | -100 to +100 | Color temperature shift (warm/cool) |
| `tint` | `float` | -100 to +100 | Green/magenta tint shift |
| `saturation` | `float` | 0 to 2.0 | Color saturation multiplier |
| `contrast` | `float` | -1 to +1 | Contrast adjustment |
| `shadows` | `[R,G,B]` | -1 to +1 | Shadow zone RGB adjustments |
| `midtones` | `[R,G,B]` | -1 to +1 | Midtone zone RGB adjustments |
| `highlights` | `[R,G,B]` | -1 to +1 | Highlight zone RGB adjustments |
| `curveMaster` | `array` | x,y: 0-1 | Master tone curve control points |
| `curveR` | `array` | x,y: 0-1 | Red channel curve control points |
| `curveG` | `array` | x,y: 0-1 | Green channel curve control points |
| `curveB` | `array` | x,y: 0-1 | Blue channel curve control points |
| `sharpness` | `float` | 0 to 1.0 | Output sharpening amount |

**Example:**

```bash
curl http://localhost:8081/api/colorgrade/corrector \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/colorgrade/corrector

Set the color corrector state. The full `CorrectorState` object must be provided (partial updates are not supported). All parameter values are validated against their allowed ranges.

**Request Body:** Full corrector state object (same schema as the GET response).

```json
{
  "lift": [0.0, 0.0, 0.0],
  "gamma": [1.0, 1.0, 1.0],
  "gain": [1.0, 1.0, 1.0],
  "temperature": 15,
  "tint": -5,
  "saturation": 1.1,
  "contrast": 0.1,
  "shadows": [0.0, 0.0, 0.0],
  "midtones": [0.0, 0.0, 0.0],
  "highlights": [0.0, 0.0, 0.0],
  "sharpness": 0.3
}
```

**Response:** `200 OK` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON or parameter out of range (e.g., gamma below 0.1, saturation above 2.0, curve point x/y outside 0-1) |

**Example:**

```bash
curl -X PUT http://localhost:8081/api/colorgrade/corrector \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lift":[0,0,0],"gamma":[1,1,1],"gain":[1,1,1],"temperature":15,"tint":0,"saturation":1.1,"contrast":0,"shadows":[0,0,0],"midtones":[0,0,0],"highlights":[0,0,0],"sharpness":0.3}'
```

---

### DELETE /api/colorgrade/corrector

Reset the color corrector to its neutral default (no color change). Equivalent to setting all values to their identity/zero state.

**Request Body:** None

**Response:** `200 OK` with full `ControlRoomState`.

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/colorgrade/corrector \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/colorgrade/save

Bake the current corrector adjustments combined with the active look into a new custom `.cube` LUT file. The resulting look appears in the available looks list and can be recalled independently of corrector state.

**Request Body:**

```json
{
  "name": "my-graded-look"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Name for the saved look (max 64 characters) |

**Response:** `201 Created` with full `ControlRoomState`.

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing or invalid name |
| `500` | Internal error saving the LUT file |

**Example:**

```bash
curl -X POST http://localhost:8081/api/colorgrade/save \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-graded-look"}'
```

---

### GET /api/colorgrade/looks/{name}/settings

Return the saved corrector settings for a custom look. When a look is saved via `POST /api/colorgrade/save`, the corrector state at the time of saving is recorded. This endpoint retrieves those settings so they can be restored in the corrector UI.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Look name |

**Request Body:** None

**Response:** `200 OK` with the saved `CorrectorState` (same schema as `GET /api/colorgrade/corrector`).

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing name |
| `404` | No corrector settings saved for this look (built-in looks have no saved settings) |

**Example:**

```bash
curl http://localhost:8081/api/colorgrade/looks/my-graded-look/settings \
  -H "Authorization: Bearer $TOKEN"
```

---

## AI Background Segmentation

GPU-accelerated AI background segmentation for per-source background removal or replacement. Requires a GPU with TensorRT support. AI segmentation endpoints are only registered when an `AISegmentManager` is configured; otherwise these routes return `404 Not Found` (unregistered).

### GET /api/ai-segment/status

Check whether AI background segmentation is available on this server. Returns `true` when both a GPU and the TensorRT segmentation engine are present.

**Request Body:** None

**Response:** `200 OK`

```json
{
  "available": true
}
```

| Field | Type | Description |
|-------|------|-------------|
| `available` | `bool` | Whether AI segmentation can be used |

**Example:**

```bash
curl http://localhost:8081/api/ai-segment/status \
  -H "Authorization: Bearer $TOKEN"
```

---

### PUT /api/sources/{source}/ai-segment

Enable and configure AI background segmentation for a specific source. If already enabled, reconfigures with the new parameters.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `"cam1"`, `"srt:encoder1"`) |

**Request Body:**

```json
{
  "sensitivity": 0.7,
  "edgeSmooth": 0.5,
  "background": "color:00FF00"
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `sensitivity` | `float` | No | `0.7` | Segmentation sensitivity from `0.0` to `1.0`. Higher values detect more of the subject. |
| `edgeSmooth` | `float` | No | `0.5` | Edge smoothing from `0.0` (hard edge) to `1.0` (soft feathered edge) |
| `background` | `string` | No | `""` | Background mode: `""` or `"transparent"` for transparent, `"color:RRGGBB"` for solid color (hex). `"blur:N"` format is recognized but not yet implemented. |

**Response:** `200 OK` with the applied configuration:

```json
{
  "enabled": true,
  "sensitivity": 0.7,
  "edgeSmooth": 0.5,
  "background": "color:00FF00"
}
```

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Invalid JSON, `sensitivity` or `edgeSmooth` out of range (0.0-1.0), or invalid `background` value |
| `503` | AI segmentation engine unavailable (GPU/TensorRT not present) |

**Examples:**

```bash
# Enable with transparent background (defaults)
curl -X PUT http://localhost:8081/api/sources/cam1/ai-segment \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'

# Enable with green background
curl -X PUT http://localhost:8081/api/sources/cam1/ai-segment \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sensitivity": 0.8, "edgeSmooth": 0.6, "background": "color:00FF00"}'

# Enable with transparent background, high sensitivity
curl -X PUT http://localhost:8081/api/sources/srt:encoder1/ai-segment \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sensitivity": 0.9, "background": "transparent"}'
```

---

### GET /api/sources/{source}/ai-segment

Get the current AI segmentation configuration for a specific source.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `"cam1"`, `"srt:encoder1"`) |

**Request Body:** None

**Response:** `200 OK`

```json
{
  "enabled": true,
  "sensitivity": 0.7,
  "edgeSmooth": 0.5,
  "background": "color:00FF00"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether AI segmentation is active for this source |
| `sensitivity` | `float` | Current sensitivity (0.0-1.0) |
| `edgeSmooth` | `float` | Current edge smoothing (0.0-1.0) |
| `background` | `string` | Current background mode |

**Errors:**

| Status | Condition |
|--------|-----------|
| `404` | No AI segmentation configured for this source |

**Example:**

```bash
curl http://localhost:8081/api/sources/cam1/ai-segment \
  -H "Authorization: Bearer $TOKEN"
```

---

### DELETE /api/sources/{source}/ai-segment

Disable AI background segmentation for a specific source and free associated GPU resources.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `source` | Source key (e.g., `"cam1"`, `"srt:encoder1"`) |

**Request Body:** None

**Response:** `204 No Content`

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/sources/cam1/ai-segment \
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
      "ts": "2026-03-05T14:30:20.000Z",
      "type": "cut",
      "detail": {"from": "cam2", "to": "cam1"}
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

## Performance

Live performance sampling and statistics. The performance endpoint provides windowed timing statistics (1s, 10s, 60s) for all major subsystems: per-source decode, video pipeline, end-to-end latency, audio mixing, broadcast delivery, and output. Baselines can be saved and compared against to track performance regressions.

### GET /api/perf

Return a comprehensive performance snapshot with windowed statistics for all subsystems. Optionally compare against a saved baseline.

**Query Parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `baseline` | No | Name of a saved baseline to compare against. If provided and the baseline exists, the response includes a `baseline` diff object. |

**Request Body:** None

**Response:** `200 OK` (with `Cache-Control: no-store`)

```json
{
  "timestamp": "2026-03-23T10:30:00.123456789Z",
  "uptime_ms": 3600000,
  "frame_budget_ns": 33333333,
  "sources": {
    "cam1": {
      "health": "healthy",
      "decode": {
        "current": {
          "last_ns": 1500000,
          "drops": 0,
          "avg_fps": 29.97,
          "avg_frame_bytes": 45000,
          "ingest_fps": 29.97
        },
        "windows": {
          "1s":  {"min_ns": 1200000, "max_ns": 2100000, "mean_ns": 1500000, "p95_ns": 1900000},
          "10s": {"min_ns": 1100000, "max_ns": 2500000, "mean_ns": 1450000, "p95_ns": 2000000},
          "60s": {"min_ns": 1000000, "max_ns": 3000000, "mean_ns": 1400000, "p95_ns": 2100000}
        }
      },
      "srt": {
        "rtt_ms": 12.5,
        "loss_rate_pct": 0.01,
        "recv_buf_ms": 120.0
      }
    }
  },
  "pipeline": {
    "current": {"last_ns": 5000000, "queue_len": 0},
    "windows": {
      "1s":  {"min_ns": 4000000, "max_ns": 7000000, "mean_ns": 5000000, "p95_ns": 6500000},
      "10s": {"min_ns": 3500000, "max_ns": 8000000, "mean_ns": 4800000, "p95_ns": 6800000},
      "60s": {"min_ns": 3000000, "max_ns": 9000000, "mean_ns": 4600000, "p95_ns": 7000000}
    },
    "nodes": {
      "encode": {
        "current": {"last_ns": 2000000},
        "windows": {
          "1s":  {"min_ns": 1500000, "max_ns": 3000000, "mean_ns": 2000000, "p95_ns": 2800000},
          "10s": {"min_ns": 1400000, "max_ns": 3500000, "mean_ns": 1900000, "p95_ns": 2900000},
          "60s": {"min_ns": 1300000, "max_ns": 4000000, "mean_ns": 1850000, "p95_ns": 3000000}
        }
      }
    },
    "deadline_violations": 0,
    "proc_dropped": 0,
    "budget_pct": 21.0,
    "gpu": null
  },
  "e2e": {
    "current": {"last_ns": 12000000},
    "windows": {
      "1s":  {"min_ns": 10000000, "max_ns": 15000000, "mean_ns": 12000000, "p95_ns": 14000000},
      "10s": {"min_ns": 9000000, "max_ns": 16000000, "mean_ns": 11500000, "p95_ns": 14500000},
      "60s": {"min_ns": 8000000, "max_ns": 18000000, "mean_ns": 11000000, "p95_ns": 15000000}
    },
    "stages": {
      "decode_queue": {"current": {"last_ns": 500000}, "windows": {"1s": {}, "10s": {}, "60s": {}}},
      "decode":       {"current": {"last_ns": 1500000}, "windows": {"1s": {}, "10s": {}, "60s": {}}},
      "sync_wait":    {"current": {"last_ns": 8000000}, "windows": {"1s": {}, "10s": {}, "60s": {}}},
      "proc_queue":   {"current": {"last_ns": 200000}, "windows": {"1s": {}, "10s": {}, "60s": {}}}
    }
  },
  "audio": {
    "mode": "passthrough",
    "mix_cycle": {
      "current": {"last_ns": 150000},
      "windows": {
        "1s":  {"min_ns": 100000, "max_ns": 200000, "mean_ns": 150000, "p95_ns": 180000},
        "10s": {"min_ns": 90000, "max_ns": 250000, "mean_ns": 145000, "p95_ns": 200000},
        "60s": {"min_ns": 80000, "max_ns": 300000, "mean_ns": 140000, "p95_ns": 220000}
      }
    },
    "counters": {"output": 168000, "mixed": 168000, "decode_errors": 0, "encode_errors": 0},
    "loudness": {"momentary_lufs": -23.5, "short_term_lufs": -24.0, "integrated_lufs": -24.0}
  },
  "broadcast": {
    "frames": 108000,
    "output_fps": 29.97,
    "gap": {
      "current": {"max_ns": 33400000},
      "windows": {"1s": {}, "10s": {}, "60s": {}}
    }
  },
  "output": {
    "muxer_pts": 3240000000,
    "srt": {"bytes_written": 0, "overflow_count": 0},
    "recording": {"active": false}
  },
  "frame_sync": {
    "source_count": 4,
    "release_fps": 29.97
  },
  "baseline": null
}
```

When a `baseline` query parameter is provided and matches a saved baseline, the `baseline` field contains a diff:

```json
{
  "baseline": {
    "name": "before-optimization",
    "saved_at": "2026-03-23T09:00:00Z",
    "pipeline": {"mean_ns_delta": -500000, "p95_ns_delta": -800000, "pct_change": -10.5},
    "e2e":      {"mean_ns_delta": -200000, "p95_ns_delta": -400000, "pct_change": -1.8},
    "mix_cycle": {"mean_ns_delta": -10000, "p95_ns_delta": -20000, "pct_change": -6.7}
  }
}
```

**Example:**

```bash
# Basic performance snapshot
curl http://localhost:8081/api/perf \
  -H "Authorization: Bearer $TOKEN"

# Compare against a saved baseline
curl "http://localhost:8081/api/perf?baseline=before-optimization" \
  -H "Authorization: Bearer $TOKEN"
```

---

### POST /api/perf/baseline

Save the current 60-second performance window as a named baseline for future comparison.

**Request Body:**

```json
{
  "name": "before-optimization"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | Unique name for the baseline snapshot |

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing or empty `name` |

**Example:**

```bash
curl -X POST http://localhost:8081/api/perf/baseline \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "before-optimization"}'
```

---

### DELETE /api/perf/baseline/{name}

Delete a saved performance baseline.

**Path Parameters:**

| Parameter | Description |
|-----------|-------------|
| `name` | Baseline name to delete |

**Request Body:** None

**Response:** `204 No Content`

**Errors:**

| Status | Condition |
|--------|-----------|
| `400` | Missing name |

**Example:**

```bash
curl -X DELETE http://localhost:8081/api/perf/baseline/before-optimization \
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

- `switchframe_http_requests_total` -- Counter by method, pattern, status
- `switchframe_http_request_duration_seconds` -- Histogram by method, pattern
- Switcher, mixer, and output subsystem metrics

---

### GET /api/cert-hash

Returns the WebTransport TLS certificate fingerprint. This is needed by browsers to establish a WebTransport connection to the QUIC server. This endpoint is exempt from authentication.

Served on the QUIC server (port 8080), the admin server (port 9090, for Vite dev proxy bootstrapping), and the HTTP fallback server (port 8081, when `--http-fallback` is enabled). The response includes a `trusted` field indicating whether the certificate is CA-signed (e.g., from mkcert) vs self-signed.

**Response:**

```json
{
  "hash": "sha-256 base64-encoded fingerprint",
  "addr": ":8080",
  "trusted": false
}
```

**Example:**

```bash
curl http://localhost:9090/api/cert-hash
```

---

## Real-Time State Updates

State updates are pushed to browsers in real time via the MoQ (Media over QUIC) `"control"` track. Each message is a full `ControlRoomState` JSON snapshot (not a delta), enabling late-join clients to receive the complete current state immediately. This is the primary mechanism used by the Switchframe UI for sub-frame latency state synchronization.

`GET /api/switch/state` is available as a polling fallback when WebTransport/MoQ is unavailable (e.g., behind a reverse proxy that does not support QUIC). The UI automatically falls back to REST polling and switches back to MoQ when the connection recovers.
