# API: transition

> **Where you are:** [docs](../../../README.md) → reference → api → transition
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/transition.md](../../subsystems/transition.md) · [api/transition-wipes.md](transition-wipes.md) · [api/graphics.md](graphics.md)

**TL;DR** Three endpoints drive the main transition engine: kick off an auto mix / dip / wipe / stinger / DVE move (`POST /api/switch/transition`), nudge the T-bar position mid-transition (`POST /api/switch/transition/position`), and arm or release Fade-to-Black (`POST /api/switch/ftb`). All three go through the timed command queue so they can be scheduled on a synchronized clock.

## Endpoints

### `POST /api/switch/transition`

**Purpose:** start an automated transition from preview to program.
**Auth:** required.
**Handler:** [`control/api_transition.go`](../../../server/control/api_transition.go) → `(*API).handleTransitionInner` (wrapped by `timedHandler`).

**Request body** (`control.transitionRequest`):

```json
{
  "source": "camera-2",
  "type": "wipe",
  "durationMs": 1000,
  "wipeDirection": "h-right",
  "easing": { "type": "ease-in-out" }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `source` | string | yes | target source for program |
| `type` | string | yes | one of `mix`, `dip`, `wipe`, `stinger`, or any `dve-*` slot-move preset |
| `durationMs` | int | yes | 100-5000 ms |
| `wipeDirection` | string | for directional wipes | `h-left`, `h-right`, `v-top`, `v-bottom`, `box-center-out`, `box-edges-in` |
| `wipeConfig` | `wipemap.WipeConfig` | for pattern wipes | `{ "pattern": "<id>" }` referencing a stored wipe pattern |
| `stingerName` | string | for `type=stinger` | name of a registered stinger clip |
| `easing` | `transition.EasingConfig` | no | `{ "type": "linear\|ease\|ease-in\|ease-out\|ease-in-out\|smoothstep\|custom", "x1,y1,x2,y2": float }` |

**Response 200:** full `ControlRoomState` with `InTransition: true` and `TransitionType` updated.

**Errors:**

| Code | When |
|---|---|
| 400 | invalid JSON, unknown `type`, bad `durationMs`, unknown wipe direction/pattern, missing `stingerName` on stinger, invalid easing custom bezier |
| 404 | stinger clip not found |
| 409 | another transition already active |
| 501 | wipe-pattern store or stinger store not configured |

**Emits:** state broadcast with `InTransition`, `TransitionType`, `TransitionDurationMs`, `TransitionEasing`. The transition engine then publishes updates on each frame via its own state callback, pushing `TransitionPosition` from 0 → 1.

**Related:** [subsystems/transition.md](../../subsystems/transition.md) · [api/transition-wipes.md](transition-wipes.md) · [api/graphics.md#stinger-clips](graphics.md#stinger-clips) · [api/dve.md](dve.md)

---

### `POST /api/switch/transition/position`

**Purpose:** set the T-bar position (0.0-1.0) during an active transition. The browser throttles this to roughly 60 Hz when the user drags the slider; fast-control datagrams are preferred for low-latency scrubbing (see [fast-control.md](../fast-control.md)) but the REST endpoint is available as a fallback.
**Auth:** required.
**Handler:** [`control/api_transition.go`](../../../server/control/api_transition.go) → `(*API).handleTransitionPositionInner` (wrapped by `timedHandler`).

**Request body** (`control.transitionPositionRequest`):

```json
{ "position": 0.42 }
```

| Field | Type | Required | Description |
|---|---|---|---|
| `position` | float | yes | 0.0 (program) to 1.0 (preview) |

**Response 200:** full `ControlRoomState`.

**Errors:**

| Code | When |
|---|---|
| 400 | position out of range |
| 409 | `switcher.ErrNoTransition` — no transition active |

**Emits:** state broadcast with updated `TransitionPosition`.

**Related:** [fast-control.md](../fast-control.md) · [subsystems/transition.md](../../subsystems/transition.md)

---

### `POST /api/switch/ftb`

**Purpose:** start or release the Fade-to-Black overlay. Idempotent toggle — calling twice fades to black and back.
**Auth:** required.
**Handler:** [`control/api_transition.go`](../../../server/control/api_transition.go) → `(*API).handleFTBInner` (wrapped by `timedHandler`).

**Request body:** empty.

**Response 200:** full `ControlRoomState` with `FTBActive` toggled.

**Errors:**

| Code | When |
|---|---|
| 409 | `transition.ErrActive` — a non-FTB transition is running |

**Emits:** state broadcast with updated `FTBActive`. Amber tally applied to all sources during FTB. The fade duration is server-configured (see [deployment.md](../../deployment.md)).

**Related:** [subsystems/transition.md](../../subsystems/transition.md)

## Related docs

- **Concepts:** [pipeline.md](../../pipeline.md)
- **Reference:** [api.md](../api.md) · [api/transition-wipes.md](transition-wipes.md) · [fast-control.md](../fast-control.md)
- **Subsystems:** [transition.md](../../subsystems/transition.md) · [switcher.md](../../subsystems/switcher.md)
