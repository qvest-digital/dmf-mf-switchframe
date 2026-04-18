# API: comms

> **Where you are:** [docs](../../../README.md) → reference → api → comms
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/comms.md](../../subsystems/comms.md) · [api/operator.md](operator.md)

**TL;DR** Four endpoints manage operator voice comms: join, leave, set mute, get status. Audio itself flows over WebTransport bidirectional streams, not REST — see [subsystems/comms.md](../../subsystems/comms.md) for the full protocol.

## Endpoints

### `POST /api/comms/join`

**Purpose:** join the voice comms channel.
**Handler:** [`control/api_comms.go`](../../../server/control/api_comms.go) → `(*API).handleCommsJoin`.

**Request body** (`control.commsJoinRequest`): `{ "operatorId": "op-1", "name": "Alice" }`.

**Response 200:** full `ControlRoomState`.

**Errors:** 400 (missing operatorId/name), 409 (`comms.ErrCommsFull`), 501 (comms disabled), 503 (`comms.ErrOpusUnavailable`).

---

### `POST /api/comms/leave`

**Handler:** `(*API).handleCommsLeave`.

**Request body** (`control.commsLeaveRequest`): `{ "operatorId": "op-1" }`.

---

### `PUT /api/comms/mute`

**Purpose:** mute / unmute a participant (self or someone else depending on role).
**Handler:** `(*API).handleCommsMute`.

**Request body** (`control.commsMuteRequest`): `{ "operatorId": "op-1", "muted": true }`.

**Errors:** 404 (`comms.ErrNotInComms`).

---

### `GET /api/comms/status`

**Handler:** `(*API).handleCommsStatus`. **Response 200:** `internal.CommsState`.

## Related docs

- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#commsstate) · [api/operator.md](operator.md)
- **Subsystems:** [comms.md](../../subsystems/comms.md)
