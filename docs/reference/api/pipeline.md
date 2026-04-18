# API: pipeline

> **Where you are:** [docs](../../../README.md) → reference → api → pipeline
> **Read this first:** [api.md](../api.md)
> **See also:** [pipeline.md](../../concepts/pipeline.md) · [subsystems/switcher.md](../../subsystems/switcher.md)

**TL;DR** Four endpoints expose the switcher's video pipeline for introspection and per-node bypass toggles: a snapshot of the node graph with health and latency, a per-node thumbnail endpoint for routing-view UIs, and set/clear bypass on individual pipeline nodes.

## Endpoints

### `GET /api/pipeline/snapshot`

**Purpose:** return the current pipeline node graph (nodes, connections, per-node health and latency).
**Handler:** [`control/api_pipeline.go`](../../../server/control/api_pipeline.go) → `(*API).handlePipelineSnapshot`.

**Response 200:** `switcher.PipelineSnapshot` — array of nodes with per-node state.

---

### `GET /api/pipeline/thumbnails`

**Purpose:** return per-node thumbnails (JPEG tiles) for pipeline visualization. Only registered when the thumbnail cache is attached.
**Handler:** `(*API).handlePipelineThumbnails`.

**Response 200:** JSON containing base64-encoded JPEGs keyed by node name.

---

### `PUT /api/pipeline/nodes/{name}/bypass`

**Purpose:** bypass a pipeline node (route input → output, skipping the node's work). Useful for isolating issues and taking emergency action.
**Handler:** `(*API).handlePipelineBypassSet`.

**Response 200:** current bypass state.

**Errors:** 404 (node not found), 400 (node can't be bypassed).

---

### `DELETE /api/pipeline/nodes/{name}/bypass`

**Purpose:** clear a bypass.
**Handler:** `(*API).handlePipelineBypassClear`.

## Related docs

- **Concepts:** [pipeline.md](../../concepts/pipeline.md)
- **Reference:** [api.md](../api.md)
- **Subsystems:** [switcher.md](../../subsystems/switcher.md)
