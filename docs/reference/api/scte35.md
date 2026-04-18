# API: SCTE-35

> **Where you are:** [docs](../../../README.md) → reference → api → scte35
> **Read this first:** [api.md](../api.md)
> **See also:** [scte35.md](../../subsystems/scte35.md) · [subsystems/scte35.md](../../subsystems/scte35.md)

**TL;DR** Twenty endpoints cover ad-insertion signalling: inject cues (splice_insert / time_signal), return to program, cancel, hold (block auto-return), extend a break, and manage the rules engine (CRUD + default action + reorder + templates). Cue-state endpoints are only registered when the SCTE-35 injector is configured; rules endpoints require a separate rules store.

## Cue lifecycle

### `POST /api/scte35/cue`

**Purpose:** inject a SCTE-35 cue (immediate) or schedule one with a pre-roll.
**Handler:** [`control/api_scte35.go`](../../../server/control/api_scte35.go) → `(*API).handleSCTE35Cue`.

**Request body** (`control.scte35CueRequest`):

```json
{
  "commandType": "splice_insert",
  "isOut": true,
  "durationMs": 120000,
  "autoReturn": true,
  "eventId": 1001,
  "preRollMs": 4000
}
```

| Field | Type | Description |
|---|---|---|
| `commandType` | string | `splice_insert`, `time_signal`, or `splice_null` |
| `isOut` | bool? | splice-out (break start) vs. splice-in |
| `durationMs` | int64? | ad break duration in ms (converted to 90 kHz ticks) |
| `autoReturn` | bool? | auto-return to program at duration |
| `preRollMs` | int64? | schedule-ahead delay; 0 = immediate |
| `eventId` | uint32? | caller-supplied event ID (otherwise auto-generated) |
| `uniqueProgramId` | uint16? | for dual-feed tracking |
| `availNum`, `availsExpected` | uint8? | optional avail numbering |
| `descriptors` | array | segmentation descriptors (required for `time_signal`, forbidden for `splice_insert`) |

Each descriptor has `segmentationType`, `segEventId`, `durationMs`, `upidType`, `upid`.

**Response 200:** `{ "eventId": 1001, "state": <ControlRoomState> }`.

**Errors:** 400 (unknown command type, descriptor/command mismatch, negative duration).

**Emits:** state broadcast with the event added to `SCTE35.ActiveEvents` / `EventLog`.

**Related:** [scte35.md](../../subsystems/scte35.md)

---

### `POST /api/scte35/return`

**Purpose:** return the most recent active event to program (splice-in without waiting for auto-return).
**Handler:** `(*API).handleSCTE35Return`.

---

### `POST /api/scte35/return/{eventId}`

**Purpose:** return a specific event by ID.
**Handler:** `(*API).handleSCTE35ReturnEvent`.

**Errors:** 400 (invalid ID), 404.

---

### `POST /api/scte35/cancel/{eventId}`

**Purpose:** cancel a pending / active event.
**Handler:** `(*API).handleSCTE35Cancel`.

---

### `POST /api/scte35/cancel-segmentation/{segEventId}`

**Purpose:** cancel a specific segmentation descriptor within an event.
**Handler:** `(*API).handleSCTE35CancelSegmentation`.

---

### `POST /api/scte35/hold/{eventId}`

**Purpose:** prevent an event from auto-returning (manual return required).
**Handler:** `(*API).handleSCTE35Hold`.

---

### `POST /api/scte35/extend/{eventId}`

**Purpose:** extend an active break by a new total duration.
**Handler:** `(*API).handleSCTE35Extend`.

**Request body:** `{ "durationMs": 180000 }`.

**Errors:** 400 (missing / non-positive duration).

---

## State

### `GET /api/scte35/status`

**Purpose:** return the full injector state.
**Handler:** `(*API).handleSCTE35Status`. **Response 200:** `scte35.InjectorState`.

---

### `GET /api/scte35/log`

**Purpose:** return the last N events in the log (ring buffer).
**Handler:** `(*API).handleSCTE35Log`. **Response 200:** `[]scte35.EventLogEntry`.

---

### `GET /api/scte35/active`

**Purpose:** return currently-active event IDs.
**Handler:** `(*API).handleSCTE35Active`. **Response 200:** `[]uint32`.

---

## Rules engine

The rules engine conditions incoming SCTE-35 signals before they hit the injector — for example, to strip distributor avails or rewrite segmentation descriptors.

### `GET /api/scte35/rules`

**Handler:** `(*API).handleSCTE35ListRules`. **Response 200:** `[]scte35.Rule`.

---

### `POST /api/scte35/rules`

**Purpose:** create a new rule.
**Handler:** `(*API).handleSCTE35CreateRule`. **Response 201:** created rule with generated ID.

---

### `PUT /api/scte35/rules/{id}`

**Handler:** `(*API).handleSCTE35UpdateRule`. **Errors:** 404 (`scte35.ErrRuleNotFound`).

---

### `DELETE /api/scte35/rules/{id}`

**Handler:** `(*API).handleSCTE35DeleteRule`.

---

### `PUT /api/scte35/rules/by-name/{name}`

**Purpose:** update a rule by name (for dual-engine setups where IDs differ between engines).
**Handler:** `(*API).handleSCTE35UpdateRuleByName`.

---

### `DELETE /api/scte35/rules/by-name/{name}`

**Handler:** `(*API).handleSCTE35DeleteRuleByName`.

---

### `PUT /api/scte35/rules/default`

**Purpose:** set the default action when no rules match (pass / drop / rewrite).
**Handler:** `(*API).handleSCTE35SetDefault`.

**Request body:** `{ "action": "pass" | "drop" | "rewrite" | ... }` — see `scte35.RuleAction`.

---

### `POST /api/scte35/rules/reorder`

**Purpose:** reorder rule evaluation priority.
**Handler:** `(*API).handleSCTE35ReorderRules`.

**Request body:** `{ "ids": ["id1", "id2", "id3"] }`.

---

### `GET /api/scte35/rules/templates`

**Purpose:** list built-in rule templates.
**Handler:** `(*API).handleSCTE35Templates`.

---

### `POST /api/scte35/rules/from-template`

**Purpose:** create a new rule from a named template.
**Handler:** `(*API).handleSCTE35FromTemplate`.

**Request body:** `{ "name": "strip-distributor-avails" }`.

**Errors:** 404 (`scte35.ErrTemplateNotFound`).

## Related docs

- **Concepts:** [scte35.md](../../subsystems/scte35.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#scte-35)
- **Subsystems:** [scte35.md](../../subsystems/scte35.md)
