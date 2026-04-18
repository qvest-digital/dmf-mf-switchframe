# API: color grade

> **Where you are:** [docs](../../../README.md) → reference → api → colorgrade
> **Read this first:** [api.md](../api.md)
> **See also:** [api/source.md](source.md) — per-source corrector

**TL;DR** Nine endpoints manage color grading: enable/disable a LUT-based look, upload a custom LUT, manage looks, adjust the master corrector (lift/gamma/gain wheels + RGB curves), save the current corrector as a named look, and recall look settings.

## Endpoints

### `GET /api/colorgrade`

**Purpose:** return the current color grade state.
**Handler:** [`control/api_colorgrade.go`](../../../server/control/api_colorgrade.go) → `(*API).handleColorGradeGet`.

**Response 200:** `internal.ColorGradeState`.

---

### `PUT /api/colorgrade`

**Purpose:** enable a named look at a given intensity, or disable color grading entirely.
**Handler:** `(*API).handleColorGradeSet`.

**Request body:** `{ "lookName": "vivid", "intensity": 0.8 }` — to enable. `{ "enabled": false }` — to disable. Intensity clamped to [0, 1].

**Response 200:** full `ControlRoomState`.

**Errors:** 400 (unknown look, invalid intensity).

---

### `POST /api/colorgrade/upload`

**Purpose:** upload a custom `.cube` LUT file and register it as a named look.
**Handler:** `(*API).handleColorGradeUpload`.

**Request:** `multipart/form-data` with `file` field and `name` form field.

**Errors:** 400 (parse failure, duplicate name).

---

### `DELETE /api/colorgrade/{name}`

**Purpose:** delete a custom look.
**Handler:** `(*API).handleColorGradeDelete`.

**Response 204.**

---

### `GET /api/colorgrade/corrector`

**Purpose:** return the master corrector state.
**Handler:** `(*API).handleCorrectorGet`.

**Response 200:** `colorgrade.CorrectorState`.

---

### `PUT /api/colorgrade/corrector`

**Purpose:** apply a corrector state to the program bus.
**Handler:** `(*API).handleCorrectorSet`.

**Request body:** `colorgrade.CorrectorState`.

**Errors:** 400 (validation failure).

---

### `DELETE /api/colorgrade/corrector`

**Handler:** `(*API).handleCorrectorReset`.

---

### `POST /api/colorgrade/save`

**Purpose:** save the current corrector state as a named user look.
**Handler:** `(*API).handleSaveLook`.

**Request body:** `{ "name": "my-look" }`.

---

### `GET /api/colorgrade/looks/{name}/settings`

**Purpose:** return the saved corrector settings for a look (for UI preview / recall).
**Handler:** `(*API).handleLookSettings`.

**Response 200:** `colorgrade.CorrectorState`. **Errors:** 404.

## Related docs

- **Reference:** [api.md](../api.md) · [api/source.md](source.md) · [state-broadcast.md](../state-broadcast.md#effects)
- **Subsystems:** [control-plane.md](../../subsystems/control-plane.md)
