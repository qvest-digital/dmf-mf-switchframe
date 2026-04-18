# API: DVE

> **Where you are:** [docs](../../../README.md) → reference → api → dve
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/graphics-and-dve.md](../../subsystems/graphics-and-dve.md) · [fast-control.md](../../fast-control.md)

**TL;DR** Eight endpoints configure the Digital Video Effects (DVE) compositor: get/set/delete the current layout, per-slot update and on/off, and preset CRUD. Real-time transform scrubbing (during drag) flows through fast-control datagrams; the REST endpoints here are used for discrete changes and preset recall.

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

Any field may be omitted. Convenience fields: `scaleMode` and `cropAnchor` map to `transform.scaleMode` / `transform.cropAnchor`.

**Errors:** 400 (invalid id/JSON/slot range, `dve.ValidateSlot` failure), 404 (layout changed).

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

**Purpose:** recall a preset. The body accepts animated-recall fields for forward compatibility; currently presets apply instantly.
**Handler:** `(*API).handleRecallDVEPresetInner` (`timedHandlerWithPath`).

**Request body** (`control.dvePresetRecallRequest`): `{ "durationMs": 800, "easing": "ease-in-out" }` (both optional, currently unused).

**Response 200:** resulting `dve.Layout`.

**Errors:** 404 (preset not found).

---

### `DELETE /api/dve/presets/{id}`

**Purpose:** delete a user preset. Only registered with a preset store.
**Handler:** `(*API).handleDeleteDVEPreset`.

**Response 204.** **Errors:** 404.

## Related docs

- **Reference:** [api.md](../api.md) · [fast-control.md](../../fast-control.md) · [state-broadcast.md](../state-broadcast.md#layoutstate)
- **Subsystems:** [graphics-and-dve.md](../../subsystems/graphics-and-dve.md)
