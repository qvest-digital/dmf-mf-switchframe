# API: operators

> **Where you are:** [docs](../../../README.md) → reference → api → operator
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/control-plane.md#operators](../../subsystems/control-plane.md#operators-sessions-and-locks) · [api/comms.md](comms.md)

**TL;DR** Nine endpoints manage multi-operator production: register (optionally via invite token), reconnect with a saved token, heartbeat, list operators with connection flags, acquire/release/force-release per-subsystem locks, delete (self or by director), and list invite tokens (director only). Registered operators get their own bearer tokens and show up in `ControlRoomState.Operators` with live connection status.

## Endpoints

### `POST /api/operator/register`

**Purpose:** register a new operator and return a bearer token. When invite tokens are configured, the `inviteToken` field is required (unless the caller authenticates with the session API token — then the role is taken from the body).
**Auth:** none required (this endpoint is in `authExemptPaths`).
**Handler:** [`control/api_operator.go`](../../../server/control/api_operator.go) → `(*API).handleOperatorRegister`.

**Request body:**

```json
{ "name": "Alice", "role": "director", "id": "optional-stable-id", "inviteToken": "abc123", "token": "optional-64-hex-chars" }
```

Roles: `director`, `audio`, `graphics`, `captioner`, `viewer` — see `operator.Role`.

`id` and `token` are optional and chosen by the client. A dual-engine UI sends the same registration to both engines, so both must store the same ID and the same bearer token; a token minted by each engine would leave the browser holding one the other engine rejects. `token` must be 64 lowercase hex characters (the format the engine generates) and not already in use; omitted, the engine generates one. Re-registering an existing `id` returns that operator only with the same name, role and token.

**Response 200:** `{ "id": "...", "name": "...", "role": "...", "token": "..." }`. The token is the bearer token the operator uses for subsequent calls.

**Errors:** 400 (empty name, `operator.ErrInvalidRole`, `operator.ErrInvalidToken`), 403 (missing / invalid invite token when configured), 409 (`operator.ErrDuplicateName`).

---

### `POST /api/operator/reconnect`

**Purpose:** re-establish an operator session using a previously-issued token. Useful for reconnects after a browser refresh.
**Auth:** none (exempt); the bearer token in the `Authorization` header is checked inside the handler.
**Handler:** `(*API).handleOperatorReconnect`.

**Response 200:** `{ "id": "...", "name": "...", "role": "..." }`.

**Errors:** 401 (missing or invalid token).

---

### `POST /api/operator/heartbeat`

**Purpose:** mark the operator's session as alive; prevents the 60-second stale-session cleanup.
**Auth:** none; bearer token checked in handler.
**Handler:** `(*API).handleOperatorHeartbeat`.

**Response 200:** `{ "ok": true }`.

**Errors:** 401.

**Related:** [subsystems/control-plane.md#operators](../../subsystems/control-plane.md#operators-sessions-and-locks)

---

### `GET /api/operator/list`

**Purpose:** list all registered operators with a `connected` flag indicating whether a session is currently active.
**Handler:** `(*API).handleOperatorList`. **Response 200:** `[]operator.Info`.

---

### `POST /api/operator/lock`

**Purpose:** acquire a subsystem lock. While held, other operators get 409 when commanding that subsystem.
**Handler:** `(*API).handleOperatorLock`.

**Request body:** `{ "subsystem": "audio" }`.

Valid subsystems: `switching`, `audio`, `graphics`, `replay`, `output`, `captions` — see `operator.Subsystem`.

**Response 200:** `{ "ok": true }`.

**Errors:** 400 (`operator.ErrInvalidSubsystem`, `operator.ErrLockNotOwned`), 401 (bad token), 403 (`operator.ErrNoPermission` — role can't lock this subsystem), 404 (`operator.ErrSessionNotFound`), 409 (`operator.ErrSubsystemLocked`).

---

### `POST /api/operator/unlock`

**Purpose:** release a lock you hold.
**Handler:** `(*API).handleOperatorUnlock`.

**Errors:** 400 (`operator.ErrNotLocked`, `operator.ErrLockNotOwned`).

---

### `POST /api/operator/force-unlock`

**Purpose:** director-only — release any operator's lock.
**Handler:** `(*API).handleOperatorForceUnlock`.

**Errors:** 401, 403 (non-director), 400 (`operator.ErrNotLocked`).

---

### `DELETE /api/operator/{id}`

**Purpose:** delete an operator record. Allowed for self, or for any operator if the requester is a director. If no operators are registered yet (bootstrap case), auth is skipped.
**Handler:** `(*API).handleOperatorDelete`.

**Response 200:** `{ "ok": true }`.

**Errors:** 400 (empty ID), 401 (invalid token with operators present), 403 (non-self non-director), 404 (`operator.ErrNotFound`).

---

### `GET /api/operator/invite-tokens`

**Purpose:** return the role → invite-token map for building invite links. Director role required.
**Handler:** `(*API).handleOperatorInviteTokens`.

**Response 200:** `{ "director": "token1", "audio": "token2", ... }` or `{}` when invite tokens aren't configured.

**Errors:** 401 (missing token), 403 (non-director).

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#operators) · [api/comms.md](comms.md)
- **Subsystems:** [control-plane.md](../../subsystems/control-plane.md)
