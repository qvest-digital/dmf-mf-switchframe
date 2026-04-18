# API: ST map

> **Where you are:** [docs](../../../README.md) → reference → api → stmap
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/stmap.md](../../subsystems/stmap.md) · [stmap.md](../../subsystems/stmap.md)

**TL;DR** Twelve endpoints manage the per-pixel coordinate-remapping subsystem (lens correction, creative warps). Maps can be uploaded from EXR/PNG/raw-float32 files, generated from parameterized templates, or assigned per-source or globally on the program bus.

## Endpoints

### `GET /api/stmap`

**Purpose:** list all stored map names.
**Handler:** [`control/api_stmap.go`](../../../server/control/api_stmap.go) → `(*API).handleSTMapList`.

**Response 200:** `["identity", "barrel", "lens-cam1", ...]`.

---

### `GET /api/stmap/state`

**Purpose:** return the current program/source assignments plus the available map list.
**Handler:** `(*API).handleSTMapState`.

**Response 200:** `internal.STMapState`.

---

### `GET /api/stmap/generators`

**Purpose:** list available parametric generators (identity, barrel, pincushion, fisheye_to_rectilinear, corner_pin, heat_shimmer, dream, ripple, lens_breathe, vortex).
**Handler:** `(*API).handleSTMapGenerators`.

---

### `POST /api/stmap/generate`

**Purpose:** generate a map from a generator preset.
**Handler:** `(*API).handleSTMapGenerate`.

**Request body** (`control.generateRequest`):

```json
{
  "type": "barrel",
  "params": { "k1": 0.1, "k2": 0.05 },
  "name": "lens-cam1",
  "width": 1920, "height": 1080,
  "assign_source": "cam1",
  "assign_program": false,
  "frame_count": 1
}
```

`frame_count > 1` produces an animated map (valid only for animated generators like heat_shimmer / ripple / vortex).

**Errors:** 400 (`stmap.ErrInvalidDimensions`, `stmap.ErrInvalidName`, unknown generator type).

**Emits:** state broadcast with the map added to the registry and optionally assigned.

---

### `POST /api/stmap/upload/{name}`

**Purpose:** upload an ST map file (EXR, 16-bit PNG, or raw float32).
**Handler:** `(*API).handleSTMapUpload`.

**Request:** `multipart/form-data` with `file` field. File type detected by extension.

**Response 201:** `stmap.STMap` metadata.

**Errors:** 400 (`stmap.ErrInvalidName`, `stmap.ErrInvalidDimensions`, parse failure).

---

### `GET /api/stmap/{name}`

**Purpose:** return map metadata.
**Handler:** `(*API).handleSTMapGet`. **Response 200:** `stmap.STMap`.

**Errors:** 404 (`stmap.ErrNotFound`).

---

### `DELETE /api/stmap/{name}`

**Handler:** `(*API).handleSTMapDelete`. **Response 204.**

---

### `GET /api/stmap/{name}/download`

**Purpose:** download the original uploaded file (when available) or the raw float32 representation.
**Handler:** `(*API).handleSTMapDownload`.

**Response 200:** `application/octet-stream` or original image MIME type.

---

### `PUT /api/stmap/source/{sourceKey}`

**Purpose:** assign a map to a specific source.
**Handler:** `(*API).handleSTMapAssignSource`.

**Request body** (`control.stmapAssignRequest`): `{ "map": "lens-cam1" }`.

**Response 200:** full `ControlRoomState`.

**Errors:** 404 (`stmap.ErrNotFound`).

---

### `DELETE /api/stmap/source/{sourceKey}`

**Handler:** `(*API).handleSTMapRemoveSource`.

---

### `PUT /api/stmap/program`

**Purpose:** apply a map to the entire program output.
**Handler:** `(*API).handleSTMapAssignProgram`.

**Request body:** `{ "map": "heat-shimmer" }`.

---

### `DELETE /api/stmap/program`

**Handler:** `(*API).handleSTMapRemoveProgram`.

## Related docs

- **Concepts:** [stmap.md](../../subsystems/stmap.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#effects)
- **Subsystems:** [stmap.md](../../subsystems/stmap.md)
