# API: upstream keying

> **Where you are:** [docs](../../../README.md) → reference → api → keying
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/graphics-and-dve.md](../../subsystems/graphics-and-dve.md) · [api/source.md](source.md)

**TL;DR** Three endpoints configure per-source upstream keys (chroma / luma / neural). Upstream keys run before the mix, so a keyed source feeds into the program/preview with its background already removed. These are separate from the DSK graphics layers documented in [api/graphics.md](graphics.md).

## Endpoints

### `PUT /api/sources/{source}/key`

**Purpose:** configure an upstream key on a source.
**Handler:** [`control/api_keying.go`](../../../server/control/api_keying.go) → `(*API).handleSetSourceKeyInner` (`timedHandlerWithPath`).

**Request body** (`graphics.KeyConfig`) — the exact shape depends on `type`:

```json
{
  "type": "chroma",
  "enabled": true,
  "keyColorY": 128, "keyColorCb": 80, "keyColorCr": 220,
  "similarity": 0.4, "smoothness": 0.1,
  "spillSuppress": 0.5, "spillReplaceCb": 128, "spillReplaceCr": 128,
  "fillSource": "cam-fill"
}
```

Valid types: `chroma`, `luma`, `neural`. See `graphics.KeyConfig` for all fields.

**Response 200:** the accepted `graphics.KeyConfig`.

**Errors:** 400 (bad type), 404 (source unknown).

**Emits:** state broadcast with updated `Sources[source].KeyConfig`.

---

### `GET /api/sources/{source}/key`

**Purpose:** return the current key configuration for a source.
**Handler:** `(*API).handleGetSourceKey`.

**Response 200:** `graphics.KeyConfig`. **Errors:** 404 (no key configured).

---

### `DELETE /api/sources/{source}/key`

**Purpose:** remove the key configuration and clear any associated fill source binding.
**Handler:** `(*API).handleDeleteSourceKeyInner` (`timedHandlerWithPath`).

**Response 204.**

## Related docs

- **Reference:** [api.md](../api.md) · [api/source.md](source.md) · [state-broadcast.md](../state-broadcast.md#effects)
- **Subsystems:** [graphics-and-dve.md](../../subsystems/graphics-and-dve.md)
