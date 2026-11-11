# API: DVE

> **Where you are:** [docs](../../../README.md) → reference → api → dve
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/graphics-and-dve.md](../../subsystems/graphics-and-dve.md) · [fast-control.md](../fast-control.md)

**TL;DR** Ten endpoints configure the Digital Video Effects (DVE) compositor: get/set/delete the current layout, per-slot update and on/off, and preset CRUD and recall. Real-time transform scrubbing (during drag) flows through fast-control datagrams; the REST endpoints here are used for discrete changes and preset recall. Four more drive the [squeezeback](#squeezeback), which shrinks the whole program into an L-frame.

## Endpoints

### `GET /api/dve`

**Purpose:** return the current DVE layout.
**Handler:** [`control/api_dve.go`](../../../server/control/api_dve.go) → `(*API).handleGetDVE`.

**Response 200:** `dve.Layout` or `{ "layout": null }` when no layout is set.

---

### `PUT /api/dve`

**Purpose:** set the DVE layout, either by preset name or by explicit slot definitions.
**Handler:** `(*API).handleSetDVEInner` (`timedHandler`).

**Request body** (`control.dveLayoutRequest`):

```json
{ "preset": "side-by-side" }
```

or

```json
{ "name": "custom-show", "slots": [ { "sourceKey": "cam1", "transform": { ... }, "zOrder": 0, "enabled": true } ] }
```

Preset lookup: built-in presets first (`dve.ResolveBuiltinPreset` with the current pipeline format), then the user preset store. Slot validation via `dve.ValidateLayout`.

**Errors:** 400 (invalid JSON, missing preset/slots, validation failure), 404 (preset not found).

**Emits:** state broadcast with updated `Layout.DVEPresets`.

---

### `DELETE /api/dve`

**Purpose:** clear the active DVE layout (return to single-source composition).
**Handler:** `(*API).handleDeleteDVE`.

**Response 204.**

---

### `PUT /api/dve/slots/{id}`

**Purpose:** update a single slot's properties (partial patch).
**Handler:** `(*API).handleDVESlotUpdateInner` (`timedHandlerWithPath`).

**Request body** (`control.dveSlotUpdateRequest`):

```json
{ "sourceKey": "cam2", "transform": { "positionX": 0.7, "positionY": 0.3, "width": 0.4, "height": 0.4 }, "zOrder": 1, "enabled": true }
```

Any field may be omitted. Convenience fields: `scaleMode` and `cropAnchor` map to `transform.scaleMode` / `transform.cropAnchor`. With `durationMs` > 0 the slot animates to the result instead of jumping, timed by an optional `easing` as for preset recall. An update without `durationMs` ends a morph in flight and applies at once.

**Errors:** 400 (invalid id/JSON/slot range, `dve.ValidateSlot` failure, unknown easing, `durationMs` above 5000), 404 (layout changed).

---

### `POST /api/dve/slots/{id}/on`

**Purpose:** enable a slot.
**Handler:** `(*API).handleDVESlotOnInner` (`timedHandlerWithPath`).

**Response 204.**

---

### `POST /api/dve/slots/{id}/off`

**Purpose:** disable a slot without removing it.
**Handler:** `(*API).handleDVESlotOffInner` (`timedHandlerWithPath`).

**Response 204.**

---

### `GET /api/dve/presets`

**Purpose:** list all DVE presets (built-ins + user presets).
**Handler:** `(*API).handleListDVEPresets`.

**Response 200:** `[]dve.PresetInfo`.

---

### `POST /api/dve/presets`

**Purpose:** save the current layout as a new user preset. Only registered when a DVE preset store is configured.
**Handler:** `(*API).handleSaveDVEPreset`.

**Request body** (`control.dvePresetSaveRequest`): `{ "name": "My Layout" }`.

**Response 201.**

**Errors:** 400 (no active layout, missing/long name), 500 (save failure).

---

### `PUT /api/dve/presets/{id}/recall`

**Purpose:** recall a preset, cutting to it or, with `durationMs` > 0, morphing every slot from what is on screen to the preset.
**Handler:** `(*API).handleRecallDVEPresetInner` (`timedHandlerWithPath`).

**Request body** (`control.dvePresetRecallRequest`): `{ "durationMs": 800, "easing": "ease-in-out" }`, both optional. `easing` is `linear`, `ease`, `ease-in`, `ease-out`, `ease-in-out`, `smoothstep` or `cubic-bezier(x1, y1, x2, y2)`, and defaults to the preset's own easing. `durationMs` is at most 5000 (`dve.MaxMorphDurationMs`). A cubic-bezier `y` outside [0, 1] is clamped: the morph holds at its start or target rather than overshooting.

Slots of the preset without a `sourceKey`, such as every built-in, keep the source the slot shows when the recall morphs. A recall without `durationMs` applies the preset as stored, so a built-in recalled instantly leaves its slots without a source.

**Response 200:** resulting `dve.Layout`, the target even while the morph runs.

**Errors:** 400 (unknown easing, `durationMs` above 5000), 404 (preset not found).

---

### `DELETE /api/dve/presets/{id}`

**Purpose:** delete a user preset. Only registered with a preset store.
**Handler:** `(*API).handleDeleteDVEPreset`.

**Response 204.** **Errors:** 404.

## Squeezeback

A squeezeback is an L-frame: the whole composited program, upstream keys and DVE slots included, shrinks into a rect while a background source or black fills the rest of the frame. Downstream graphics stay full frame on top. It runs after the DVE compositor in both the CPU and the GPU pipeline. Not to be confused with the `dve-squeeze` transition type.

The background is any registered source (`clip:N`, `playout:N`, an MXL source, ...) and tallies as program while it shows.

### `GET /api/squeezeback`

**Purpose:** return the squeezeback.
**Handler:** [`control/api_squeeze.go`](../../../server/control/api_squeeze.go) -> `(*API).handleGetSqueeze`.

**Response 200:** `dve.SqueezeState`: the configuration, `configured`, `on` (what the last on or off moves to) and `animating`.

---

### `PUT /api/squeezeback`

**Purpose:** configure the rect, background and default timing. While on, the program moves to the new rect over the new duration.
**Handler:** `(*API).handleSetSqueezeInner` (`timedHandler`).

**Request body** (`dve.SqueezeConfig`):

```json
{ "rect": { "x": 480, "y": 0, "w": 1440, "h": 810 }, "background": "clip:1", "durationMs": 800, "easing": "ease-in-out" }
```

`rect` is in pixels of the program frame, every value an even whole number, with a positive size, inside the frame. `background` is a source key; omitted or `""` is black. `durationMs` and `easing` are the defaults of on and off; `durationMs` is at most 5000 (`dve.MaxMorphDurationMs`) and `easing` takes the values of [preset recall](#put-apidvepresetsidrecall).

**Response 200:** `dve.SqueezeState`.

**Errors:** 400 (invalid JSON, odd or out-of-frame rect, unknown background source, `durationMs` outside 0..5000, unknown easing).

---

### `POST /api/squeezeback/on`

**Purpose:** squeeze the program into the rect, from wherever it is.
**Handler:** `(*API).handleSqueezeOnInner` (`timedHandler`).

**Request body** (`dve.SqueezeMove`), optional: `{ "durationMs": 800, "easing": "ease-out" }`. Each field left out takes the configured one; `durationMs` 0 cuts.

**Response 204.** **Errors:** 400 (not configured, invalid JSON, `durationMs` outside 0..5000, unknown easing).

---

### `POST /api/squeezeback/off`

**Purpose:** return the program to full frame, from wherever it is. The background leaves the air, and tally, when the program arrives.
**Handler:** `(*API).handleSqueezeOffInner` (`timedHandler`).

**Request body:** as for on.

**Response 204.** **Errors:** 400 (invalid JSON, `durationMs` outside 0..5000, unknown easing).

## Related docs

- **Reference:** [api.md](../api.md), [fast-control.md](../fast-control.md), [macro.md](macro.md), [state-broadcast.md](../state-broadcast.md#graphics-and-composition)
- **Subsystems:** [graphics-and-dve.md](../../subsystems/graphics-and-dve.md)
