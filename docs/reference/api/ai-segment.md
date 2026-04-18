# API: AI background segmentation

> **Where you are:** [docs](../../../README.md) → reference → api → ai-segment
> **Read this first:** [api.md](../api.md)
> **See also:** [gpu.md](../../gpu.md) · [api/keying.md](keying.md)

**TL;DR** Four endpoints configure per-source AI-driven background segmentation (people/foreground detection) with transparent, blurred, or solid-color backgrounds. Requires a GPU build with TensorRT; these endpoints are only registered when the segmentation engine is available. In non-GPU builds the registration is a no-op.

## Endpoints

### `PUT /api/sources/{source}/ai-segment`

**Purpose:** enable / reconfigure AI segmentation for a source.
**Handler:** [`control/api_ai_segment.go`](../../../server/control/api_ai_segment.go) → `(*API).handleEnableAISegment`.

**Request body** (`control.aiSegmentRequest`):

```json
{ "sensitivity": 0.7, "edgeSmooth": 0.5, "background": "blur:20" }
```

| Field | Type | Range | Meaning |
|---|---|---|---|
| `sensitivity` | float | 0.0-1.0 | matte decision threshold |
| `edgeSmooth` | float | 0.0-1.0 | feathering on matte edges |
| `background` | string | special | `""` or `"transparent"`, `"blur:N"` (N=1-50), `"color:RRGGBB"` |

Defaults: sensitivity 0.7, edgeSmooth 0.5, background `""`.

**Response 200:** full `ControlRoomState`.

**Errors:** 400 (out-of-range sensitivity / edgeSmooth, bad background syntax, invalid hex color).

---

### `GET /api/sources/{source}/ai-segment`

**Purpose:** return the current config for a source, or `null` if no config is set.
**Handler:** `(*API).handleGetAISegment`.

**Response 200:** `internal.AISegmentConfig` or `null`.

---

### `DELETE /api/sources/{source}/ai-segment`

**Purpose:** disable AI segmentation for a source and free GPU resources.
**Handler:** `(*API).handleDisableAISegment`.

**Response 204.**

---

### `GET /api/ai-segment/status`

**Purpose:** return global availability + loaded model name.
**Handler:** `(*API).handleAISegmentStatus`.

**Response 200:**

```json
{ "available": true, "modelName": "rvm-mobilenetv3-1.0", "sources": { "cam1": { "enabled": true, ... } } }
```

## Related docs

- **Concepts:** [gpu.md](../../gpu.md)
- **Reference:** [api.md](../api.md) · [api/keying.md](keying.md) · [state-broadcast.md](../state-broadcast.md#aisegmentationstate)
