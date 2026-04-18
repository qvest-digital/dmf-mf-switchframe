# API: presets

> **Where you are:** [docs](../../../README.md) → reference → api → preset
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/control-plane.md](../../subsystems/control-plane.md) · [api/macro.md](macro.md)

**TL;DR** Eight endpoints expose switcher-state presets: list, create (snapshot current state), get, update name, delete, recall. Name-based variants (`by-name`) exist to support dual-engine setups where each engine mints different IDs for the same-named preset. Presets capture program/preview sources, transition type/duration, audio channel levels/mute/AFV, and master level — not graphics, DVE, or SCTE-35 state.

## Endpoints

### `GET /api/presets`

**Purpose:** list all presets.
**Handler:** [`control/api_preset.go`](../../../server/control/api_preset.go) → `(*API).handleListPresets`.

**Response 200:** `[]preset.Preset`.

---

### `POST /api/presets`

**Purpose:** save a new preset from the current state.
**Handler:** `(*API).handleCreatePreset`.

**Request body** (`control.createPresetRequest`): `{ "name": "Show Open" }`.

**Response 201:** `preset.Preset`.

**Errors:** 400 (`preset.ErrEmptyName`, name too long).

---

### `GET /api/presets/{id}`

**Handler:** `(*API).handleGetPreset`. **Errors:** 404 (`preset.ErrNotFound`).

---

### `PUT /api/presets/{id}`

**Purpose:** rename a preset.
**Handler:** `(*API).handleUpdatePreset`.

**Request body** (`control.updatePresetRequest`): `{ "name": "Show Open v2" }`.

---

### `DELETE /api/presets/{id}`

**Handler:** `(*API).handleDeletePreset`. **Response 204.**

---

### `DELETE /api/presets/by-name/{name}`

**Purpose:** delete a preset by name rather than ID.
**Handler:** `(*API).handleDeletePresetByName`.

---

### `POST /api/presets/{id}/recall`

**Purpose:** apply a preset to the switcher and mixer. Best-effort — missing sources produce warnings rather than errors.
**Handler:** `(*API).handleRecallPreset`.

**Response 200** (`control.recallPresetResponse`):

```json
{ "preset": { "id": "...", "name": "...", ... }, "warnings": ["audio channel \"cam-gone\" level: source not found"] }
```

Warnings are surfaced to the operator in the UI.

**Related:** [subsystems/control-plane.md#presets](../../subsystems/control-plane.md#presets)

---

### `POST /api/presets/recall`

**Purpose:** recall by name (for dual-engine setups).
**Handler:** `(*API).handleRecallPresetByName`.

**Request body:** `{ "name": "Show Open" }`.

**Errors:** 400 (missing name), 404.

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#presetinfo) · [api/macro.md](macro.md)
- **Subsystems:** [control-plane.md](../../subsystems/control-plane.md)
