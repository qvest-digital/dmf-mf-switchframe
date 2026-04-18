# API: switching

> **Where you are:** [docs](../../../README.md) → reference → api → switching
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/switcher.md](../../subsystems/switcher.md) · [state-broadcast.md](../state-broadcast.md) · [api/transition.md](transition.md)

**TL;DR** Three endpoints cover the core switcher state machine: hard cut (`POST /api/switch/cut`), set preview (`POST /api/switch/preview`), and get the full control-room state (`GET /api/switch/state`). The cut and preview handlers are wrapped by the timed command queue so a coordinated dual-engine deployment can schedule them on a shared PTP clock; the state handler is polled frequently by the browser and logged at `DEBUG` to keep logs clean.

## Endpoints

### `POST /api/switch/cut`

**Purpose:** hard-cut the program bus to the given source.
**Auth:** required.
**Handler:** [`control/api.go`](../../../server/control/api.go) → `(*API).handleCutInner` (wrapped by `timedHandler`).

**Request body** (`control.switchRequest`):

```json
{ "source": "camera-1" }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `source` | string | yes | registered source key to make program |

**Response 200:** full `ControlRoomState` (see [state-broadcast.md](../state-broadcast.md#schema)).

**Errors:**

| Code | When |
|---|---|
| 400 | body is not JSON, or `source` is empty |
| 404 | `switcher.ErrSourceNotFound` — source key not registered |
| 409 | `switcher.ErrFormatDuringTransition` — a transition is in progress |

**Emits:** state broadcast with updated `ProgramSource`, `TallyState`, and `LastChangedBy`. Increments the sequenced-command counter for dual-engine tracking. Bumps `metrics.HTTPRequestsTotal` and `HTTPRequestDuration`.

**Related:** [subsystems/switcher.md](../../subsystems/switcher.md) · [api/transition.md](transition.md)

---

### `POST /api/switch/preview`

**Purpose:** set the preview bus without touching program.
**Auth:** required.
**Handler:** [`control/api.go`](../../../server/control/api.go) → `(*API).handlePreviewInner` (wrapped by `timedHandler`).

**Request body** (`control.switchRequest`):

```json
{ "source": "camera-2" }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `source` | string | yes | registered source key to make preview |

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON / empty `source` |
| 404 | source not registered |
| 409 | transition in progress |

**Emits:** state broadcast with updated `PreviewSource`, green tally on the new preview source.

**Related:** [subsystems/switcher.md](../../subsystems/switcher.md)

---

### `GET /api/switch/state`

**Purpose:** return the full current `ControlRoomState` JSON. The UI polls this on startup and as a fallback when the MoQ state track is unavailable.
**Auth:** required.
**Handler:** [`control/api.go`](../../../server/control/api.go) → `(*API).handleState`.

**Request body:** none.

**Response 200:** full `ControlRoomState` — every mixer channel, source info, transition state, graphics layers, replay status, SCTE-35 state, etc. See [state-broadcast.md](../state-broadcast.md#schema) for the complete schema.

**Errors:** none in normal operation (500 on JSON marshal failure is theoretically possible but has never been observed).

**Emits:** nothing — read-only. Logged at `DEBUG` level because this path is polled.

**Related:** [state-broadcast.md](../state-broadcast.md)

---

A sibling endpoint, `GET /api/sync/health`, is registered alongside the core switcher routes for dual-engine polling. It is documented in [peer-sync.md](peer-sync.md). Mentioned here because the UI always polls this alongside `/api/switch/state`.

## Related docs

- **Concepts:** [pipeline.md](../../concepts/pipeline.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md) · [fast-control.md](../fast-control.md)
- **Subsystems:** [switcher.md](../../subsystems/switcher.md) · [control-plane.md](../../subsystems/control-plane.md)
