# API: transition wipes

> **Where you are:** [docs](../../../README.md) → reference → api → transition-wipes
> **Read this first:** [api.md](../api.md)
> **See also:** [api/transition.md](transition.md) · [subsystems/transition.md](../../subsystems/transition.md)

**TL;DR** Five endpoints manage wipe patterns for gradient-map wipes (beyond the six directional wipes built into the engine). Patterns are PNG gradient files that the wipe engine interpolates; clients can list, preview, upload, and delete custom ones.

## Endpoints

### `GET /api/wipes/patterns`

**Purpose:** list all wipe patterns (built-in + user-uploaded).
**Handler:** [`control/api_wipes.go`](../../../server/control/api_wipes.go) → `(*API).handleListWipePatterns`.

**Response 200:** `[]wipemap.WipePattern` — each has `id`, `name`, `builtin`.

---

### `GET /api/wipes/patterns/{id}`

**Purpose:** return pattern metadata.
**Handler:** `(*API).handleGetWipePattern`.

**Errors:** 404.

---

### `GET /api/wipes/patterns/{id}/preview`

**Purpose:** return a preview image (PNG) of the gradient.
**Handler:** `(*API).handleWipePatternPreview`.

**Response 200:** `image/png`.

---

### `POST /api/wipes/custom`

**Purpose:** upload a custom wipe pattern as a greyscale PNG gradient.
**Handler:** `(*API).handleUploadCustomWipe`.

**Request:** `multipart/form-data` with `file` field and `name` form field.

**Response 201:** `wipemap.WipePattern`.

---

### `DELETE /api/wipes/custom/{id}`

**Handler:** `(*API).handleDeleteCustomWipe`. **Response 204.**

## Related docs

- **Reference:** [api.md](../api.md) · [api/transition.md](transition.md)
- **Subsystems:** [transition.md](../../subsystems/transition.md)
