# API: peer and sync

> **Where you are:** [docs](../../../README.md) → reference → api → peer-sync
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/switcher.md](../../subsystems/switcher.md) · [deployment.md](../../operations/deployment.md)

**TL;DR** Nine endpoints handle dual-engine coordination: a lightweight health endpoint polled between engines, a force-takeover endpoint gated by a shared secret, a sync-health probe returning clock and sequence state, and a reconcile-lock protocol (acquire / refresh / release) plus state-snapshot get/apply for active-active reconciliation. The reconcile lock ensures only one engine at a time runs the active reconciler during a failover.

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
  "sending": ["0a1b2c3d"]
}
```

`sending` lists the output destinations this engine sends, of those one engine of a pair sends; the other engine reads it for [failover](../../subsystems/output.md#dual-engine-failover).

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

---

### `POST /api/sync/state-snapshot`

**Purpose:** apply a peer's state snapshot to this engine (used during reconciliation after a split-brain).
**Handler:** `(*API).handleApplyStateSnapshot`.

**Request body:** `sync.StateSnapshot`.

**Errors:** 400 (invalid snapshot), 409 (reconcile-lock not held).

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

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#dual-engine)
- **Subsystems:** [switcher.md](../../subsystems/switcher.md)
- **Operations:** [deployment.md](../../operations/deployment.md)
