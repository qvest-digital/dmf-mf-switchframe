# API: playout

> **Where you are:** [docs](../../../README.md) → reference → api → playout
> **Read this first:** [api.md](../api.md)
> **See also:** [playout.md](../../subsystems/playout.md) · [subsystems/playout.md](../../subsystems/playout.md)

**TL;DR** Forty-seven endpoints drive the playout channel engine: channel transport (start/stop/next/previous/jump), manual vs. auto mode, playlist loading, pod boards (20 endpoints for pod CRUD and lifecycle), pod templates (CRUD), BXF import, as-run logs (query / export / reset), asset-cache inspection and eviction, health and recovery, and sample/clip/template/saved-playlist discovery. Channels are addressed as `1..N` (capped by `playout.MaxChannels`); pod IDs are strings assigned by the pod board.

## Channel transport

### `GET /api/playout/channels`

**Purpose:** list all channel states.
**Handler:** [`control/api_playout.go`](../../../server/control/api_playout.go) → `(*API).handlePlayoutChannelsList`. **Response 200:** `[]playout.ChannelState`.

---

### `GET /api/playout/channels/{n}`

**Handler:** `(*API).handlePlayoutChannelGet`. **Response 200:** one `playout.ChannelState`. **Errors:** 400 (`playout.ErrInvalidChannel`).

---

### `POST /api/playout/channels/{n}/load`

**Purpose:** load a playlist into a channel. Triggers the prefetcher to cache assets.
**Handler:** `(*API).handlePlayoutLoad`.

**Request body:** `playout.Playlist`.

**Errors:** 400 (invalid channel, `playout.ErrEmptyPlaylist`, `playout.ErrInvalidItem`), 409 (`playout.ErrAlreadyOnAir`).

---

### `POST /api/playout/channels/{n}/start`

**Handler:** `(*API).handlePlayoutStart`. **Errors:** 400 (`playout.ErrNoPlaylist`), 409 (`playout.ErrAlreadyOnAir`).

---

### `POST /api/playout/channels/{n}/stop`

**Handler:** `(*API).handlePlayoutStop`. **Errors:** 400 (`playout.ErrNotOnAir`).

---

### `POST /api/playout/channels/{n}/next`

**Handler:** `(*API).handlePlayoutNext`.

---

### `POST /api/playout/channels/{n}/previous`

**Handler:** `(*API).handlePlayoutPrevious`.

---

### `POST /api/playout/channels/{n}/jump`

**Purpose:** jump to a playlist item by ID.
**Handler:** `(*API).handlePlayoutJump`. **Request body:** `{ "itemId": "item-17" }`. **Errors:** 400 (missing ID), 404 (`playout.ErrItemNotFound`).

---

### `POST /api/playout/channels/{n}/manual`

**Purpose:** take manual control — stop auto-advancing the playlist.
**Handler:** `(*API).handlePlayoutTakeManual`.

---

### `POST /api/playout/channels/{n}/auto`

**Purpose:** return to auto-advance mode.
**Handler:** `(*API).handlePlayoutReturnToAuto`.

---

### `GET /api/playout/channels/{n}/playlist`

**Purpose:** return the loaded playlist items.
**Handler:** `(*API).handlePlayoutPlaylist`. **Response 200:** `[]playout.PlaylistItem`.

---

## As-run logs

### `GET /api/playout/channels/{n}/asrun`

**Purpose:** query the as-run event log, filtered by type and/or since-timestamp.
**Handler:** `(*API).handlePlayoutAsRunQuery`.

**Query:** `?type=<event type>&since=<ms>&limit=<int>`.

**Response 200:** `[]playout.AsRunEvent`.

---

### `GET /api/playout/channels/{n}/asrun/export`

**Purpose:** export the as-run log as JSONL.
**Handler:** `(*API).handlePlayoutAsRunExport`. **Response 200:** `application/x-ndjson`.

---

### `POST /api/playout/channels/{n}/asrun/reset`

**Purpose:** reset the as-run log (clear ring buffer + truncate JSONL file).
**Handler:** `(*API).handlePlayoutAsRunReset`.

---

## BXF import

### `POST /api/playout/import/bxf`

**Purpose:** import a BXF (Broadcast Exchange Format, SMPTE) XML file as a playlist.
**Handler:** `(*API).handlePlayoutBXFImport`.

**Request:** `application/xml` body.

**Response 200:** `playout.Playlist`.

**Errors:** 400 (invalid XML, validation failure).

---

## Health and recovery

### `GET /api/playout/health`

**Purpose:** return health info for all channels (watchdog stalls, last-recovery).
**Handler:** `(*API).handlePlayoutHealth`.

---

### `POST /api/playout/channels/{n}/recover`

**Purpose:** trigger a manual recovery on a stalled channel.
**Handler:** `(*API).handlePlayoutRecover`.

---

## Asset cache

### `GET /api/playout/cache/status`

**Purpose:** return aggregate cache status (bytes used, entries, hit rate).
**Handler:** `(*API).handlePlayoutCacheStatus`. **Errors:** 501.

---

### `POST /api/playout/cache/lookup`

**Purpose:** look up cache status for an asset by URI.
**Handler:** `(*API).handlePlayoutCacheLookup`.

**Request body:** `{ "asset_uri": "https://.../clip.mp4" }`.

---

### `POST /api/playout/cache/evict`

**Purpose:** evict an asset from the cache.
**Handler:** `(*API).handlePlayoutCacheEvict`. **Response 200:** `{ "status": "evicted" }`.

---

## Discovery

### `GET /api/playout/samples`

**Purpose:** return built-in sample clips (pre-generated via FFmpeg at startup).
**Handler:** `(*API).handlePlayoutSamples`.

---

### `GET /api/playout/clips`

**Purpose:** return the clip library as playlist-compatible items.
**Handler:** `(*API).handlePlayoutClips`.

---

### `GET /api/playout/templates`

**Purpose:** list built-in template playlists.
**Handler:** `(*API).handlePlayoutTemplates`.

---

### `GET /api/playout/templates/{name}`

**Handler:** `(*API).handlePlayoutTemplate`. **Response 200:** `playout.Playlist`. **Errors:** 404.

---

## Saved playlists

### `GET /api/playout/saved`

**Handler:** `(*API).handlePlayoutSavedList`.

---

### `POST /api/playout/saved`

**Purpose:** save a playlist to disk.
**Handler:** `(*API).handlePlayoutSave`. **Request body:** `{ "name": "morning-show", "playlist": { ... } }`.

---

### `GET /api/playout/saved/{name}`

**Handler:** `(*API).handlePlayoutSavedGet`. **Response 200:** `playout.Playlist`.

---

### `DELETE /api/playout/saved/{name}`

**Handler:** `(*API).handlePlayoutSavedDelete`.

---

## Pod boards

Pods are timed secondary events (cut-ins, squeezebacks, overlays) associated with a primary playlist item. The pod board manages their lifecycle: arm → fire → hold → extend → dump. See [playout.md#pods](../../subsystems/playout.md) for the state machine.

### `GET /api/playout/channels/{n}/pods`

**Handler:** [`control/api_playout_pods.go`](../../../server/control/api_playout_pods.go) → `(*API).handlePodList`. **Response 200:** `[]playout.PodStatus`.

---

### `POST /api/playout/channels/{n}/pods`

**Purpose:** create a pod on a channel.
**Handler:** `(*API).handlePodCreate`. **Request body:** `{ "id": "pod-<8 hex>", "template": "<name>" }` or `{ "id": ..., "config": playout.PodConfig }`. `id` is optional; engines sent the same request create the pod under it, so a request by pod ID reaches it on each.

**Errors:** 400 (`playout.ErrInvalidPodID`), 409 (`playout.ErrPodExists`).

---

### `GET /api/playout/channels/{n}/pods/{podId}`

**Handler:** `(*API).handlePodGet`. **Errors:** 404 (`playout.ErrPodNotFound`).

---

### `DELETE /api/playout/channels/{n}/pods/{podId}`

**Handler:** `(*API).handlePodDelete`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/arm`

**Purpose:** arm a pod (prep for fire).
**Handler:** `(*API).handlePodArm`. **Errors:** 400 (`playout.ErrPodTerminal`), 409 (`playout.ErrPodAlreadyActive`).

---

### `POST /api/playout/channels/{n}/pods/{podId}/disarm`

**Handler:** `(*API).handlePodDisarm`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/fire`

**Purpose:** fire an armed pod (start execution).
**Handler:** `(*API).handlePodFire`. **Errors:** 400 (`playout.ErrPodNotArmed`).

---

### `POST /api/playout/channels/{n}/pods/{podId}/defer`

**Purpose:** defer a pod's scheduled fire time.
**Handler:** `(*API).handlePodDefer`. **Request body:** `{ "deferMs": 5000 }`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/hold`

**Purpose:** pause an active pod (extends its display).
**Handler:** `(*API).handlePodHold`. **Errors:** 400 (`playout.ErrPodNotActive`).

---

### `POST /api/playout/channels/{n}/pods/{podId}/extend`

**Purpose:** extend an active pod's duration.
**Handler:** `(*API).handlePodExtend`. **Request body:** `{ "durationMs": 10000 }`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/dump`

**Purpose:** short-circuit an active pod (cut to its exit).
**Handler:** `(*API).handlePodDump`. **Errors:** 403 (`playout.ErrPodNoDumpOut`).

---

### `POST /api/playout/channels/{n}/pods/{podId}/move`

**Purpose:** reorder a pod in the queue.
**Handler:** `(*API).handlePodMove`. **Request body:** `{ "position": 3 }`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/swap`

**Purpose:** swap with another pod in the queue.
**Handler:** `(*API).handlePodSwap`. **Request body:** `{ "otherPodId": "pod-5" }`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/resize`

**Purpose:** resize the pod's visual area (for graphic / cut-in pods).
**Handler:** `(*API).handlePodResize`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/promote`

**Purpose:** elevate the pod's priority (e.g., promote a secondary to primary).
**Handler:** `(*API).handlePodPromote`.

---

### `POST /api/playout/channels/{n}/pods/{podId}/demote`

**Handler:** `(*API).handlePodDemote`.

---

## Pod templates

### `GET /api/playout/pods/templates`

**Handler:** `(*API).handlePodTemplateList`. **Response 200:** `[]playout.PodTemplate`.

---

### `POST /api/playout/pods/templates`

**Handler:** `(*API).handlePodTemplateCreate`.

---

### `PUT /api/playout/pods/templates/{type}`

**Handler:** `(*API).handlePodTemplateUpdate`.

---

### `DELETE /api/playout/pods/templates/{type}`

**Handler:** `(*API).handlePodTemplateDelete`.

## Related docs

- **Concepts:** [playout.md](../../subsystems/playout.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#playout)
- **Subsystems:** [playout.md](../../subsystems/playout.md)
