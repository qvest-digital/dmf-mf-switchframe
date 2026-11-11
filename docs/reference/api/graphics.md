# API: graphics (DSK) and stingers

> **Where you are:** [docs](../../../README.md) → reference → api → graphics
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/graphics-and-dve.md](../../subsystems/graphics-and-dve.md) · [api/graphics-html5.md](graphics-html5.md) · [html5-graphics.md](../../integration/cef-protocol.md)

**TL;DR** Thirty-eight endpoints cover downstream-key (DSK) graphics: layer CRUD, settings, order, export and import, frame upload, live fill+key sources, cut/auto on/off, programmatic animation (pulse, transition, fly-in/out/on, slide), static image upload, animated PNG-sequence upload, server-side text animation (typewriter + fade-word), scrolling ticker, and the stinger clip library. Per-layer HTML5-browser endpoints are documented separately in [graphics-html5.md](graphics-html5.md).

All graphics endpoints are only registered when a compositor is attached. Stinger endpoints are registered independently when a stinger store is configured.

Most mutating endpoints respond with the compositor's current `Status()` (which duplicates the graphics slice of `ControlRoomState`). All `{id}` path parameters are integer layer IDs.

## Layer CRUD

### `POST /api/graphics`

**Purpose:** create a new graphics layer.
**Handler:** [`control/api_graphics.go`](../../../server/control/api_graphics.go) → `(*API).handleGraphicsAddLayer`.

**Request body** (optional): `{ "id": 3 }`, the layer ID to take, at least 0 and below 2^31 - 1, so engines sent the same request can address the layer by one ID. Without it the engine picks the next ID it has not handed out.

**Response 201:** `{ "id": 3 }`.

**Errors:** 400 (invalid JSON, `graphics.ErrInvalidLayerID`), 409 (`graphics.ErrTooManyLayers`, `graphics.ErrLayerExists`).

---

### `GET /api/graphics`

**Purpose:** return full compositor status (all layers + their state).
**Handler:** `(*API).handleGraphicsStatus`.

**Response 200:** `graphics.Status` (mirrors `internal.GraphicsState`).

---

### `DELETE /api/graphics/{id}`

**Purpose:** remove a layer.
**Handler:** `(*API).handleGraphicsRemoveLayer`.

**Response 204.** **Errors:** 400 (invalid id), 404 (`graphics.ErrLayerNotFound`).

---

### `PATCH /api/graphics/{id}`

**Purpose:** store a layer's name and control-surface config. The config is opaque to the engine; it is kept so every session and both engines show the layer as it was set up.
**Handler:** `(*API).handleGraphicsLayerSettings`.

**Request body** (`control.layerSettingsRequest`): `{ "name": "Sponsor", "config": { ... } }`. An absent field is left as it is. The name is at most 64 characters; the config is a JSON object of at most 64 KiB.

**Response 200:** compositor status. **Errors:** 400 (invalid id or JSON, `graphics.ErrInvalidLayerName`, `graphics.ErrInvalidLayerConfig`), 404 (`graphics.ErrLayerNotFound`).

---

### `PUT /api/graphics/order`

**Purpose:** restack every layer bottom to top. Z-orders become 0..n-1; layer IDs stay as they are.
**Handler:** `(*API).handleGraphicsLayerOrder`.

**Request body:** `{ "ids": [2, 0, 1] }`, naming every layer exactly once.

**Response 200:** compositor status. **Errors:** 400 (invalid JSON, `graphics.ErrInvalidLayerOrder`).

---

### `GET /api/graphics/export`

**Purpose:** download every layer, bottom to top, as a `graphics.LayerExport` document (`format` `switchframe-graphics`, `version` 1): ID, name, z-order, rect, config and uploaded PNG. On-air state, fades, animations, tickers, sequences and live sources are not part of it.
**Handler:** `(*API).handleGraphicsExport`.

**Response 200:** `application/json` with `Content-Disposition: attachment; filename="graphics-layers.json"`.

---

### `POST /api/graphics/import`

**Purpose:** replace every layer with an export document's, keeping their IDs. Imported layers start off air; nothing changes when the document is invalid.
**Handler:** `(*API).handleGraphicsImport`.

**Request body:** a `graphics.LayerExport`, at most 64 MiB.

**Response 200:** compositor status. **Errors:** 400 (invalid JSON, `graphics.ErrInvalidLayerExport`), 409 (`graphics.ErrLayersOnAir` while a layer is on, fading, running a ticker or sequence, or holding an HTML5 template).

---

## Layer content

### `POST /api/graphics/{id}/frame`

**Purpose:** upload a single RGBA frame as the layer's overlay content.
**Handler:** `(*API).handleGraphicsFrame`.

**Request body** (`control.graphicsFrameRequest`): `{ "width": 1920, "height": 1080, "template": "lower-third", "rgba": "<raw bytes>" }`. Max 4K resolution; RGBA byte array length must equal `width*height*4`.

**Errors:** 400 (dimensions, size mismatch, template mismatch), 409 (`graphics.ErrFadeActive`).

---

### `POST /api/graphics/{id}/image`

**Purpose:** upload a PNG image for a layer as multipart form data (`image` field).
**Handler:** `(*API).handleGraphicsImageUpload`.

**Response 201:** compositor status.

**Errors:** 400 (bad multipart, image field missing, read failure, corrupt PNG).

---

### `GET /api/graphics/{id}/image`

**Purpose:** return the stored PNG image for a layer.
**Handler:** `(*API).handleGraphicsImageGet`.

**Response 200:** `image/png`.

**Errors:** 404 (`graphics.ErrNoImage`).

---

### `DELETE /api/graphics/{id}/image`

**Purpose:** clear the stored image on a layer.
**Handler:** `(*API).handleGraphicsImageDelete`.

**Response 204.**

---

### `PUT /api/graphics/{id}/live`

**Purpose:** make the layer show a live fill source keyed by a live key source, in place of its overlay. The key's legal-range luma is the fill's alpha (16 transparent, 235 opaque; 64 and 940 in 10-bit), as with a hardware fill+key pair. The layer is full frame and keeps on/off, fade and z-order; rect changes, fly and slide answer 409. Fill and key are read at each tick's read point from the frame synchronizer, and when one is a grain ahead of the other its frame of the other's grain is used, so both show one grain. Both are keyed from CPU pixels, so both have to be sources the switcher decodes on the CPU, such as MXL; an SRT source decoded on the GPU has none. They must match the program's size and format. A layer that cannot draw its pair, for these reasons or because the GPU pipeline has no kernel for the current program format, draws nothing: this is logged once, and the layer is left out of tally until it draws again. While the layer is on and drawing, the fill tallies `key_fill` and the key `key_alpha`.
**Handler:** `(*API).handleGraphicsLiveSet`.

**Request body** (`control.graphicsLiveRequest`): `{ "fill": "gfx-fill", "key": "gfx-key", "premultiplied": false }`. `premultiplied` marks a shaped fill, already multiplied by its key: it is added over the keyed-out background instead of mixed in.

**Errors:** 400 for an unknown source, a missing source, or fill equal to key; 404 for an unknown layer; 409 (`graphics.ErrLiveLayer`) while a fly or slide moves the layer, and 409 (`graphics.ErrLiveUnsupported`) when the pipeline cannot draw it: a premultiplied fill on the GPU pipeline, or a 10-bit program on the CUDA pipeline.

---

### `DELETE /api/graphics/{id}/live`

**Purpose:** return the layer to its overlay. A layer without one goes off.
**Handler:** `(*API).handleGraphicsLiveClear`.

---

## On/off

### `POST /api/graphics/{id}/on`

**Purpose:** cut the layer on instantly.
**Handler:** `(*API).handleGraphicsOnInner` (`timedHandlerWithPath`).

**Errors:** 400 (`graphics.ErrNoOverlay`), 409 (`graphics.ErrAlreadyActive`, `graphics.ErrFadeActive`).

---

### `POST /api/graphics/{id}/off`

**Purpose:** cut the layer off instantly.
**Handler:** `(*API).handleGraphicsOffInner` (`timedHandlerWithPath`).

**Errors:** 409 (`graphics.ErrNotActive`, `graphics.ErrFadeActive`).

---

### `POST /api/graphics/{id}/auto-on`

**Purpose:** fade the layer in over the DSK fade duration.
**Handler:** `(*API).handleGraphicsAutoOn`.

---

### `POST /api/graphics/{id}/auto-off`

**Purpose:** fade the layer out over the DSK fade duration.
**Handler:** `(*API).handleGraphicsAutoOff`.

---

### `PUT /api/graphics/dsk-fade-duration`

**Purpose:** set the duration of every layer's auto-on and auto-off fade. A fade in flight keeps its rate; the next one uses the new duration.
**Handler:** `(*API).handleSetDSKFadeDurationInner` (wrapped by `timedHandler`).

**Request body:** `{ "durationMs": 500 }`, 100-5000.

**Response 200:** full `ControlRoomState`. **Errors:** 400 (invalid JSON, `graphics.ErrInvalidDSKFadeDuration`).

---

## Layer geometry

### `PUT /api/graphics/{id}/rect`

**Purpose:** update a layer's position/size rectangle. Coordinates are even-aligned for YUV420 chroma compatibility.
**Handler:** `(*API).handleGraphicsLayerRect`.

**Request body** (`control.rectUpdateRequest`): `{ "x": 40, "y": 800, "width": 600, "height": 200 }`.

---

### `PUT /api/graphics/{id}/zorder`

**Purpose:** set a layer's stacking order (higher = on top).
**Handler:** `(*API).handleGraphicsLayerZOrder`.

**Request body** (`control.zorderUpdateRequest`): `{ "zOrder": 10 }`.

---

## Animated entry/exit

### `POST /api/graphics/{id}/fly-in`

**Purpose:** animate the layer from off-screen toward its current rect (does not change Active state).
**Handler:** `(*API).handleGraphicsFlyIn`.

**Request body** (`control.flyRequest`): `{ "direction": "left|right|top|bottom", "durationMs": 500 }`.

---

### `POST /api/graphics/{id}/fly-out`

**Purpose:** animate from current rect to off-screen.
**Handler:** `(*API).handleGraphicsFlyOut`.

---

### `POST /api/graphics/{id}/fly-on`

**Purpose:** atomic "activate + fly-in" — puts the layer on and animates it in.
**Handler:** `(*API).handleGraphicsFlyOn`.

---

### `POST /api/graphics/{id}/slide`

**Purpose:** animate the layer to a new rect.
**Handler:** `(*API).handleGraphicsSlide`.

**Request body** (`control.slideRequest`): `{ "x": 40, "y": 40, "width": 600, "height": 200, "durationMs": 500, "easing": "ease-in-out" }`.

---

### `POST /api/graphics/{id}/animate`

**Purpose:** start a sustained animation (pulse or transition).
**Handler:** `(*API).handleGraphicsAnimate`.

**Request body** (`control.animateRequest`):

```json
{ "mode": "pulse", "minAlpha": 0.3, "maxAlpha": 1.0, "speedHz": 2.0 }
```

or

```json
{ "mode": "transition", "toRect": { "x": 40, "y": 40, "width": 600, "height": 200 }, "toAlpha": 0.5, "durationMs": 800, "easing": "ease-in-out" }
```

For pulse: alpha values in [0,1], min < max, speed in (0, 10].
For transition: at least one of `toRect` / `toAlpha` required; `durationMs` must be positive.

---

### `POST /api/graphics/{id}/animate/stop`

**Purpose:** stop a running animation.
**Handler:** `(*API).handleGraphicsAnimateStop`.

---

## Text animation (server-rendered)

### `POST /api/graphics/{id}/text-animate`

**Purpose:** server renders an animated text overlay onto the layer (typewriter or fade-word modes). Only registered when a text-animation engine is attached.
**Handler:** `(*API).handleGraphicsTextAnimStart`.

**Request body** (`control.textAnimRequest`):

```json
{ "mode": "typewriter", "text": "Hello, world", "fontSize": 48, "bold": false, "charsPerSec": 20, "wordDelayMs": 0, "fadeDurationMs": 250, "width": 1920, "height": 120 }
```

**Errors:** 400 (empty text, bad mode), 409 (`graphics.ErrTextAnimActive`), 501.

---

### `POST /api/graphics/{id}/text-animate/stop`

**Purpose:** stop the text animation.
**Handler:** `(*API).handleGraphicsTextAnimStop`.

**Errors:** 404 (`graphics.ErrTextAnimNotFound`), 501.

---

## Ticker (scrolling text)

### `POST /api/graphics/{id}/ticker`

**Purpose:** start a scrolling news-ticker on a layer.
**Handler:** `(*API).handleGraphicsTickerStart`.

**Request body** (`control.tickerRequest`):

```json
{ "text": "Breaking news...", "fontSize": 24, "speed": 100, "bold": true, "loop": true, "height": 60 }
```

Defaults: speed 100 px/s, fontSize 24.

**Errors:** 400 (empty text), 409 (`graphics.ErrTickerActive`), 501.

---

### `POST /api/graphics/{id}/ticker/stop`

**Handler:** `(*API).handleGraphicsTickerStop`. **Errors:** 404 (`graphics.ErrTickerNotFound`), 501.

---

### `PUT /api/graphics/{id}/ticker/text`

**Purpose:** update the text of a running ticker.
**Handler:** `(*API).handleGraphicsTickerText`.

**Request body** (`control.tickerTextRequest`): `{ "text": "Updated..." }`.

---

## Animated PNG sequence

### `POST /api/graphics/{id}/sequence`

**Purpose:** upload a ZIP of PNG frames and play them as an animation on the layer. The engine transcodes to the pipeline frame rate on upload. Only registered when a sequence engine is attached.
**Handler:** `(*API).handleGraphicsSequenceUpload`.

**Request:** raw ZIP body (max 256 MB) with `?fps=<source-fps>` query parameter (1-240).

**Response 202** (async): compositor status.

**Errors:** 400 (missing/invalid fps, empty body, read failure), 409 (`graphics.ErrSequenceProcessing`, `graphics.ErrSequenceTooLarge`, `graphics.ErrSequenceEmpty`, `graphics.ErrSequenceSizeMismatch`), 501.

---

### `DELETE /api/graphics/{id}/sequence`

**Purpose:** remove the animated sequence from a layer.
**Handler:** `(*API).handleGraphicsSequenceDelete`.

**Errors:** 404 (`graphics.ErrSequenceNotFound`), 409 (processing).

---

### `PUT /api/graphics/{id}/sequence/speed`

**Purpose:** set sequence playback speed (1.0 = native fps, 0.5 = half, 2.0 = double).
**Handler:** `(*API).handleGraphicsSequenceSpeed`.

**Request body:** `{ "speed": 1.5 }`.

---

## Stinger clips

Stinger transitions use pre-rendered PNG sequences with a cut point. These endpoints manage the library.

### `GET /api/stinger/list`

**Purpose:** list stored stinger clips.
**Handler:** `(*API).handleStingerList`.

**Response 200:** `[]stinger.ClipInfo`.

---

### `POST /api/stinger/{name}/upload`

**Purpose:** upload a ZIP containing PNG frames (and optionally a WAV) as a new stinger clip. Max 256 MB. Name must match `control.MaxNameLen` / validation rules.
**Handler:** `(*API).handleStingerUpload`.

**Errors:** 400 (bad name), 409 (`stinger.ErrAlreadyExists`, `stinger.ErrMaxClipsReached`), 413.

---

### `DELETE /api/stinger/{name}`

**Handler:** `(*API).handleStingerDelete`. **Errors:** 404 (`stinger.ErrNotFound`).

---

### `POST /api/stinger/{name}/cut-point`

**Purpose:** set the cut point (fraction 0-1) that separates the "cover" and "reveal" halves of the stinger.
**Handler:** `(*API).handleStingerCutPoint`.

**Request body** (`control.stingerCutPointRequest`): `{ "cutPoint": 0.5 }`.

**Errors:** 400 (`stinger.ErrInvalidCutPoint`), 404.

## Related docs

- **Reference:** [api.md](../api.md) · [api/graphics-html5.md](graphics-html5.md) · [state-broadcast.md](../state-broadcast.md#graphics-and-composition)
- **Subsystems:** [graphics-and-dve.md](../../subsystems/graphics-and-dve.md)
- **Concepts:** [html5-graphics.md](../../integration/cef-protocol.md)
