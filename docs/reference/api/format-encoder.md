# API: format and encoder

> **Where you are:** [docs](../../../README.md) → reference → api → format-encoder
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/codec.md](../../subsystems/codec.md) · [deployment.md](../../deployment.md) · [subsystems/switcher.md](../../subsystems/switcher.md)

**TL;DR** Two tiny REST surfaces cover the overall pipeline format (resolution and fps) and the live video encoder choice. Format is a shared invariant — changing it rebuilds decoders/encoders across the whole pipeline — so it's blocked while a transition is in flight. Encoder selection is hot-swappable across the detected backends (x264, NVENC, VideoToolbox, VA-API, OpenH264).

## Endpoints

### `GET /api/format`

**Purpose:** return the current pipeline format and the list of supported presets.
**Auth:** required.
**Handler:** [`control/api_format.go`](../../../server/control/api_format.go) → `(*API).handleGetFormat`.

**Request body:** none.

**Response 200** (`control.formatResponse`):

```json
{
  "format": { "width": 1920, "height": 1080, "fpsNum": 60, "fpsDen": 1, "name": "1080p60" },
  "presets": ["1080p25", "1080p29.97", "1080p50", "1080p59.94", "1080p60", "720p50", "720p60"]
}
```

**Errors:** none.

**Emits:** nothing.

**Related:** [subsystems/switcher.md#pipeline-format](../../subsystems/switcher.md#pipeline-format)

---

### `PUT /api/format`

**Purpose:** change the pipeline format to a named preset, or to a fully-specified `width/height/fpsNum/fpsDen` tuple.
**Auth:** required.
**Handler:** [`control/api_format.go`](../../../server/control/api_format.go) → `(*API).handleSetFormatInner` (wrapped by `timedHandler`).

**Request body** (`control.formatRequest`):

```json
{ "format": "1080p59.94" }
```

or

```json
{ "width": 1920, "height": 1080, "fpsNum": 60000, "fpsDen": 1001 }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `format` | string | either this or width/height/fpsNum/fpsDen | preset name from `switcher.FormatPresets` |
| `width` | int | see above | horizontal pixels |
| `height` | int | see above | vertical pixels |
| `fpsNum` | int | see above | frame rate numerator |
| `fpsDen` | int | see above | frame rate denominator (default 1) |

**Response 200:** full `ControlRoomState` with updated `PipelineFormat`.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON, unknown preset, invalid dimensions |
| 409 | `switcher.ErrFormatDuringTransition` — a transition or FTB is active |

**Emits:** state broadcast with updated `PipelineFormat`. The switcher rebuilds its encoder pool, preview streams, and pipeline codecs internally — this is an expensive operation (tens of ms) so avoid toggling mid-show.

**Related:** [subsystems/switcher.md#pipeline-format](../../subsystems/switcher.md#pipeline-format) · [deployment.md](../../deployment.md)

---

### `GET /api/encoder`

**Purpose:** return the active video encoder and the list of detected alternatives.
**Auth:** required.
**Handler:** [`control/api_encoder.go`](../../../server/control/api_encoder.go) → `(*API).handleGetEncoder`.

**Request body:** none.

**Response 200** (`control.encoderResponse`):

```json
{
  "current": "videotoolbox",
  "available": [
    { "name": "x264", "displayName": "x264 (software)", "isDefault": false },
    { "name": "videotoolbox", "displayName": "Apple VideoToolbox (HW)", "isDefault": true }
  ]
}
```

`EncoderInfo` is defined in `codec.EncoderInfo`. Availability is determined at startup by `codec.ProbeEncoders` — detection is platform-specific.

**Errors:** none.

**Emits:** nothing.

**Related:** [subsystems/codec.md#probe](../../subsystems/codec.md#probe) · [deployment.md](../../deployment.md)

---

### `PUT /api/encoder`

**Purpose:** switch the live video encoder backend without restarting the server.
**Auth:** required.
**Handler:** [`control/api_encoder.go`](../../../server/control/api_encoder.go) → `(*API).handleSetEncoderInner` (wrapped by `timedHandler`).

**Request body** (`control.encoderRequest`):

```json
{ "encoder": "nvenc" }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `encoder` | string | yes | name from `available[].name` |

**Response 200:** full `ControlRoomState` with updated `Encoder.Current`.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON, empty name, or `switcher.ErrEncoderNotAvailable` |

**Emits:** state broadcast with updated `Encoder`. The new encoder kicks in on the next frame; currently-transcoding work uses the previous encoder until it drains.

**Related:** [subsystems/codec.md](../../subsystems/codec.md)

## Related docs

- **Concepts:** [gpu.md](../../gpu.md) · [pipeline.md](../../pipeline.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md)
- **Subsystems:** [codec.md](../../subsystems/codec.md) · [switcher.md](../../subsystems/switcher.md)
- **Operations:** [deployment.md](../../deployment.md)
