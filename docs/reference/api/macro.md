# API: macros

> **Where you are:** [docs](../../../README.md) → reference → api → macro
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/control-plane.md#macros](../../subsystems/control-plane.md#macros) · [api/preset.md](preset.md)

**TL;DR** Seven endpoints cover the macro system: list, get, save (upsert), delete, run (async), cancel a running macro, and dismiss the final execution state. Only one macro can run at a time; progress is broadcast on every step transition.

## Endpoints

### `GET /api/macros`

**Purpose:** list all saved macros.
**Handler:** [`control/api_macro.go`](../../../server/control/api_macro.go) → `(*API).handleListMacros`.

**Response 200:** `[]macro.Macro`.

---

### `GET /api/macros/{name}`

**Handler:** `(*API).handleGetMacro`. **Errors:** 404 (`macro.ErrNotFound`).

---

### `PUT /api/macros/{name}`

**Purpose:** create or update a macro. The `name` path parameter overrides the body's name.
**Handler:** `(*API).handleSaveMacro`.

**Request body** (`macro.Macro`):

```json
{
  "name": "opener",
  "steps": [
    { "action": "cut", "params": { "source": "cam1" } },
    { "action": "wait", "params": { "ms": 2000 } },
    { "action": "transition", "params": { "source": "graphics-layer", "type": "mix", "durationMs": 1000 } }
  ]
}
```

Valid actions are listed in `macro.allActions` (~60 actions covering cut, preview, transition, wait, audio controls, graphics, keys, clips, SCTE-35, etc.). Each action validates its own params; invalid steps are rejected on save by `macro.ValidateSteps`.

**Response 200:** saved `macro.Macro`.

**Errors:** 400 (`macro.ErrEmptyName`, `macro.ErrNoSteps`, unknown action, bad params).

---

### `DELETE /api/macros/{name}`

**Handler:** `(*API).handleDeleteMacro`. **Response 204.**

---

### `POST /api/macros/{name}/run`

**Purpose:** execute a macro asynchronously. Returns immediately with 202 Accepted; progress streams through state broadcasts.
**Handler:** `(*API).handleRunMacro`.

**Response 202:** `{ "status": "started", "name": "<macro name>" }`.

**Errors:** 404 (`macro.ErrNotFound`), 409 ("macro already running").

**Emits:** continuous state broadcasts with evolving `Macro` field — each step transitions from `pending` → `running` → `done` / `failed` / `skipped`, and wait steps populate `WaitStartMs` / `WaitMs` so the UI can show a countdown.

**Related:** [subsystems/control-plane.md#macros](../../subsystems/control-plane.md#macros)

---

### `POST /api/macros/execution/cancel`

**Purpose:** cancel the currently-running macro. Any subsequent steps are marked `skipped`.
**Handler:** `(*API).handleCancelMacro`.

**Response 204.**

---

### `DELETE /api/macros/execution`

**Purpose:** dismiss a completed-or-cancelled macro's execution state, clearing it from the broadcast. If the macro is still running, it is also cancelled (so a silent background run doesn't continue unnoticed).
**Handler:** `(*API).handleDismissMacro`.

**Response 204.**

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#macroexecutionstate) · [api/preset.md](preset.md)
- **Subsystems:** [control-plane.md](../../subsystems/control-plane.md)
