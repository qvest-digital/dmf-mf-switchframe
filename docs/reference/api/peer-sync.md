# API: peer and sync

> **Where you are:** [docs](../../../README.md) → reference → api → peer-sync
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/switcher.md](../../subsystems/switcher.md) · [deployment.md](../../operations/deployment.md)

**TL;DR** These endpoints handle dual-engine coordination: a lightweight health endpoint polled between engines, a force-takeover endpoint gated by a shared secret, a sync-health probe returning clock and sequence state, and a reconcile-lock protocol (acquire / refresh / release) plus state-snapshot get/apply for active-active reconciliation. The reconcile lock ensures only one engine at a time runs the active reconciler during a failover.

## Peer health

### `GET /api/peer/health`

**Purpose:** unauthenticated health probe called between peer engines at ~5 Hz to detect failures faster than the control plane.
**Auth:** none (in `authExemptPaths`).
**Handler:** [`control/api_peer.go`](../../../server/control/api_peer.go) → `(*API).handlePeerHealth`.

**Response 200** (`peer.HealthResponse`):

```json
{
  "status": "ok",
  "uptimeMs": 12345,
  "programSource": "cam-1",
  "lastCommandSeq": 1729,
  "engineLabel": "a",
  "epoch": 17,
  "sending": ["0a1b2c3d"],
  "armed": ["0a1b2c3d"],
  "destinations": ["0a1b2c3d", "4e5f6a7b"]
}
```

Of the output destinations one engine of a pair sends, `sending` lists those this engine sends, `armed` those started on it and `destinations` all it holds; the other engine reads them for [failover](../../subsystems/output.md#dual-engine-failover).

**Errors:** none. Defensive nil guards keep this endpoint from DoSing on refactor breakage.

---

### `POST /api/peer/force-leader`

**Purpose:** control-plane-initiated takeover. The control plane sends `X-Control-Plane-Secret: <secret>` to force an engine to become the leader regardless of its current follower status. When no secret is configured on the engine, this returns 501.
**Auth:** `X-Control-Plane-Secret` header (not bearer token — in `authExemptPaths`).
**Handler:** [`control/api_force_leader.go`](../../../server/control/api_force_leader.go) → `(*API).handleForceLeader`.

**Request body:** `peer.ForceLeaderRequest`.

**Errors:** 401 (missing / wrong secret), 501 (not configured), 409 (already leader).

---

## Sync

### `GET /api/sync/health`

**Purpose:** return lightweight health info: clock sync state, last command sequence, last executed sequence, last executed error, late-command count, uptime, fast-control pong timestamp.
**Handler:** [`control/api.go`](../../../server/control/api.go) → `(*API).handleSyncHealth`.

**Response 200:**

```json
{
  "clockSync": { "mode": "ptp", "offsetNs": 1234, "jitterNs": 500 },
  "lastCommandSeq": 1729,
  "lastExecutedSeq": 1728,
  "lastExecutedErr": "",
  "lateCommands": 0,
  "uptimeMs": 12345,
  "lastPongSentUs": 1713200000000000
}
```

Polled at 1 Hz by the browser in dual-engine mode for clock calibration.

---

### `GET /api/sync/state-snapshot`

**Purpose:** return a consistent snapshot of the engine's full state for peer reconciliation.
**Handler:** [`control/api_sync.go`](../../../server/control/api_sync.go) → `(*API).handleGetStateSnapshot`.

**Response 200:** `sync.StateSnapshot`.

**Errors:** 409 (reconcile lock not held), 503 (engine still starting: until it has taken its peer's state after its start, failed to read it, or found none to take, only an `?admin=true` read is served).

---

### `POST /api/sync/state-snapshot`

**Purpose:** apply a peer's state snapshot to this engine (used during reconciliation after a split-brain).
**Handler:** `(*API).handleApplyStateSnapshot`.

**Request body:** `sync.StateSnapshot`, at most 64 MiB: it carries every graphics layer's uploaded image and the frame last rendered into it from a template, except for HTML5 and text animation layers, which each engine renders itself.

For a snapshot with `graphicsContent` set, the engine takes the snapshot's set of graphics layers, creating the ones it lacks under the snapshot's IDs with their content and removing the ones the snapshot does not hold. A snapshot without it, from an engine that predates layer content, leaves the set of layers as it is. The engine also drops the ST map assignments the snapshot does not carry. For a snapshot with `graphicsMotion` set it also runs the snapshot's tickers, and loads the PNG sequence of each layer it lacks from the peer engine (`GET /api/sync/graphics-sequence`), removing the sequences the snapshot does not hold.

**Errors:** 400 (invalid snapshot), 409 (reconcile lock not held; or, without `?force=true`, the snapshot would remove graphics layers here while it holds none itself or names no command sequence, with `snapshot_seq` and `current_seq` as for a stale snapshot), 413 (snapshot larger than 64 MiB).

---

### `POST /api/sync/pull-from-peer`

**Purpose:** take the peer engine's state snapshot straight from the peer (`--peer-engine-url`) and apply it as `POST /api/sync/state-snapshot` applies one, so the snapshot travels between the engines rather than through a browser. The browser reconciler uses it, and relays the snapshot itself only to an engine that answers 403, 404, 405 or 501, as one built before the pull does.
**Handler:** `(*API).handlePullFromPeer`.

**Query:** `lockId`, the reconcile lock the caller holds on the peer, without which the peer is read as an `?admin=true&ready=true` read, which a starting peer refuses with 503 as it does a locked read; `force=true`, passed on to the apply. The caller's `Authorization` header is passed on to the peer.

**Response 200 and errors:** as `POST /api/sync/state-snapshot`, and 501 (no peer configured), 502 (peer unreachable or refusing the read), 503 (peer still starting).

---

### `GET /api/sync/state-compare`

**Purpose:** the engine's state as its peer compares it with its own: whether it has come on air after its start (`ready`), its command sequence, uptime, whether a transition runs or a browser reconciles (lock held, or released within 10s), and the snapshot without what legitimately differs between two engines. See [Convergence](#convergence).
**Handler:** `(*API).handleGetStateCompare`.

---

### `GET /api/sync/graphics-sequence?layer=&hash=`

**Purpose:** the ZIP a layer's PNG sequence was uploaded as, served while the layer holds the sequence of that SHA-256 `hash`.
**Handler:** `(*API).handleGetGraphicsSequence`. **Errors:** 404 (no such sequence on the layer).

---

### `GET /api/sync/stores`, `GET /api/sync/stores/item?store=&key=`

**Purpose:** the engine's ledger of the stores an operator writes to, and one item's content with its hash and modification time in `X-Sync-Hash` and `X-Sync-Mod`. Registered on an engine with `--peer-engine-url`. See [Convergence](#convergence).
**Handler:** [`peer/storesync.go`](../../../server/peer/storesync.go): `(*StoreSyncer).ServeManifest`, `ServeItem`.

---

### `POST /api/sync/reconcile-lock`

**Purpose:** acquire the reconcile lock. Returns the lock ID and a TTL. Lock is refreshed via PUT with the ID.
**Handler:** `(*API).handleAcquireReconcileLock`.

**Response 200:** `{ "lockId": "...", "expiresMs": 30000 }`.

**Errors:** 409 (another reconciler holds the lock).

---

### `PUT /api/sync/reconcile-lock/{lockId}`

**Purpose:** refresh the lock's TTL.
**Handler:** `(*API).handleRefreshReconcileLock`. **Errors:** 404 (lock expired or wrong ID).

---

### `DELETE /api/sync/reconcile-lock/{lockId}`

**Handler:** `(*API).handleReleaseReconcileLock`.

## Convergence

The two engines of a pair converge their state between themselves, with no browser open.

**Stores.** What an operator writes to disk converges item by item: presets (by name), macros, operators registered with a token, the SCTE-35 rules (as one item), stingers, custom wipe patterns, DVE presets, ST maps, uploaded LUTs and their corrector settings, saved playout playlists, and uploaded clips where no clip library is attached. Each engine keeps a ledger (`peer_sync.json` in the state directory) of every item's content hash and when it last changed, and a tombstone for each it deleted. Every 5s, and once before the API serves after a start (once the admin server runs, bounded to 2 minutes, the first scan of the stores included), an engine reads its peer's ledger and takes every item the peer wrote, or deleted, later than it did; the later write of an item wins on both engines, and an item deleted later than it was written is not brought back by an older copy. An engine that does not list an item says nothing about it: a freshly started or empty engine lists nothing, so its peer neither takes nor deletes anything from it. Only an item a listing positively lacks counts as deleted: one the engine holds but cannot read now, such as a stinger it did not load, a stinger directory without a frame, or a clip whose media fails to open, keeps its ledger entry; the stinger directories and their files are listed under the stinger store's lock, so one being replaced is never seen missing. An item the ledger does not hold yet and whose store does not date it (presets, macros, operators, SCTE-35 rules) counts as older than any dated write of it. Items transfer one at a time as a stream, checked against their hash and bounded per store (stingers 600 MiB, ST maps 256 MiB, clips the clip storage limit); a transfer that moves no byte for a minute is cut, and reading the manifest is bounded to 30s. A version taken keeps the peer's date while the store holds it as transferred, or in the store's own form: a JSON store decodes an item into its type, so an engine whose type lacks a field the peer's has, or adds one, holds the same version without it or with it, and the two count it as one version. When an engine's type changes with its own upgrade, its stores are listed before it serves; a version taken from the peer that it then holds in another form counts as read anew, not written, and is taken from the peer again in the new form. Held as anything else, as by a write here right after it was stored, it counts as a change here, dated now, and the peer takes it in turn. An item refused for its size or for want of room, which a clip or stinger is before it transfers, or that fails otherwise, is tried again after a minute, doubling to 30 minutes, and at once when the peer holds a new version of it. Left out, as each engine's own: the SRT sources (copied from the booking at every start), the MXL NMOS receiver state, the uploaded ZIPs of graphics sequences (cleared at start), as-run logs, the playout cache, the command queue and sequence, the leadership epoch, the API token, replays and recordings, operators of an identity provider, and every clip where a clip library already shares them.

**Live state.** Every 3s each engine reads its peer's `GET /api/sync/state-compare` and compares the two snapshots field by field. Left out of the comparison, as each engine's own: the capture's time and version; a graphics layer's rendered frame (an image is compared by its hash); clip players' clip IDs and positions (an apply does not set a position); whether a destination is sent here or stood by; recording's engine; playout channels' elapsed time, scheduler state and transition progress; SCTE-35 events' remaining time; macro runs, operator sessions and comms participants; and the stinger list, which converges with the stores. Per-source settings are compared over the sources both engines hold, and replay marks within 2s. Once the two have differed, unchanged, for 15s, longer than a browser takes to reconcile them, the engine without authority over the difference takes the other's state by reading its snapshot and applying it. Authority is the later command sequence; at the same sequence, the engine started more than a minute earlier, which kept the production while the other was away; at about the same start, the engine whose compared state changed more than 5s later, as by a write that reached only it and carries no command sequence; and otherwise the leader. Each engine decides it from its own and its peer's state as last read, so the two can disagree for at most one compare read after a change, and an engine only pulls. Taking the peer's state, by the converger, at start or pushed by a browser, is no write of the engine's own: it does not move the engine's change time, also when it leaves part of the difference in place, so authority over a difference stays with the engine whose write it is. The graphics sequences an apply takes from the peer are read before it starts, so its window spans the apply alone. Nothing is taken while either engine is starting or off air, a transition runs, or a browser reconciles (also when one starts while the peer's state is read); nor a state that would remove this engine's graphics layers while naming no command or holding no layers; nor once the engine took a command the read state predates. Each apply, and each failed one, waits 30s for the next, doubling to 10 minutes until the engines agree; an apply that takes the state only in part counts as failed. A difference an apply cannot repair is thus taken again ever more rarely and stays reported.

**Observability.** `switchframe_peer_state_diverged` and `switchframe_peer_store_diverged_items` show a divergence while it lasts; the `switchframe_peer_state_*` and `switchframe_peer_store_*` counters count divergences, applies, items and bytes taken, and failures. See [metrics.md](../metrics.md#redundancy-and-nmos-metrics).

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#dual-engine)
- **Subsystems:** [switcher.md](../../subsystems/switcher.md)
- **Operations:** [deployment.md](../../operations/deployment.md)
