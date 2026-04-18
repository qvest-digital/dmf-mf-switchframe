# API: replay

> **Where you are:** [docs](../../../README.md) → reference → api → replay
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/replay.md](../../subsystems/replay.md) · [api/clips.md](clips.md)

**TL;DR** Fifteen endpoints drive the instant-replay system: mark-in/out, play with speed and loop, pause/resume, seek, adjust marks mid-playback, per-source buffer status, single-frame peek, shutter-angle (motion blur) control, and a one-shot "quick replay" for N seconds of history.

## Endpoints

All replay endpoints return 501 when the replay manager isn't configured.

### `POST /api/replay/mark-in`

**Purpose:** set the mark-in point for a source at the current wall-clock.
**Handler:** [`control/api_replay.go`](../../../server/control/api_replay.go) → `(*API).handleReplayMarkIn`.

**Request body** (`replay.MarkInRequest`): `{ "source": "cam1" }`.

**Errors:** 400 (missing source), 404 (`replay.ErrNoSource`), 501.

---

### `POST /api/replay/mark-out`

**Purpose:** set the mark-out point.
**Handler:** `(*API).handleReplayMarkOut`.

**Request body** (`replay.MarkOutRequest`): `{ "source": "cam1" }`.

**Errors:** 400 (`replay.ErrNoMarkIn`, `replay.ErrInvalidMarks`), 404, 501.

---

### `POST /api/replay/play`

**Purpose:** start playback of the marked region.
**Handler:** `(*API).handleReplayPlay`.

**Request body** (`replay.PlayRequest`): `{ "source": "cam1", "speed": 0.5, "loop": true }`. Speed defaults to 1.0 if zero.

**Errors:** 400 (`replay.ErrInvalidSpeed`, `replay.ErrNoMarkOut`, `replay.ErrEmptyClip`), 409 (`replay.ErrPlayerActive`, `replay.ErrMaxSources`), 501.

---

### `POST /api/replay/stop`

**Purpose:** stop playback and return control to the program bus.
**Handler:** `(*API).handleReplayStop`.

---

### `GET /api/replay/status`

**Purpose:** return full replay state including per-source buffer info and GPU acceleration flag.
**Handler:** `(*API).handleReplayStatus`.

**Response 200:** typed JSON inline — see `internal.ReplayState`.

---

### `GET /api/replay/sources`

**Purpose:** return per-source buffer info (frame count, GOP count, duration, bytes used).
**Handler:** `(*API).handleReplaySources`.

**Response 200:** `[]replay.SourceBufferInfo`.

---

### `POST /api/replay/quick`

**Purpose:** play the last N seconds of a source (or the current program source if omitted).
**Handler:** `(*API).handleReplayQuick`.

**Request body** (`replay.QuickReplayRequest`): `{ "source": "cam1", "seconds": 10, "speed": 0.5 }`.

**Errors:** 400 (seconds ≤ 0, invalid speed).

---

### `POST /api/replay/pause`

**Handler:** `(*API).handleReplayPause`. **Errors:** 400 (`replay.ErrNotPlaying`), 501.

---

### `POST /api/replay/resume`

**Handler:** `(*API).handleReplayResume`. **Errors:** 400 (`replay.ErrNotPaused`), 501.

---

### `PATCH /api/replay/seek`

**Purpose:** seek to a normalized position (0.0-1.0) within the clip.
**Handler:** `(*API).handleReplaySeek`.

**Request body** (`replay.SeekRequest`): `{ "position": 0.25 }`.

**Errors:** 400 (`replay.ErrInvalidSeek`, `replay.ErrNoPlayer`), 501.

---

### `PATCH /api/replay/speed`

**Purpose:** change playback speed mid-play (0.1 - 4.0).
**Handler:** `(*API).handleReplaySpeed`.

**Request body** (`replay.SpeedRequest`): `{ "speed": 0.5 }`.

---

### `PATCH /api/replay/marks`

**Purpose:** adjust mark-in / mark-out timestamps (Unix ms).
**Handler:** `(*API).handleReplayMarks`.

**Request body** (`replay.AdjustMarksRequest`): `{ "markIn": 1713200000000, "markOut": 1713200005000 }`.

---

### `GET /api/replay/peek`

**Purpose:** return a single JPEG thumbnail of the current frame at the head of a source's replay buffer. Used by the UI to show replay preview.
**Handler:** `(*API).handleReplayPeek`.

**Query:** `?source=<key>` (required).

**Response 200:** `image/jpeg` with `Cache-Control: no-store`. Returns 204 if buffer is empty.

**Errors:** 400 (missing source), 404 (`replay.ErrNoSource`), 501.

---

### `PUT /api/replay/shutter-angle`

**Purpose:** set the motion-blur shutter angle (0-360 degrees; higher = more blur on slow-motion).
**Handler:** `(*API).handleSetShutterAngle`.

**Request body:** `{ "angle": 180 }`.

**Errors:** 400 (out of range).

---

### `GET /api/replay/shutter-angle`

**Handler:** `(*API).handleGetShutterAngle`. **Response 200:** `{ "angle": 180 }`.

## Related docs

- **Reference:** [api.md](../api.md) · [api/clips.md](clips.md) · [state-broadcast.md](../state-broadcast.md#replay-and-clips)
- **Subsystems:** [replay.md](../../subsystems/replay.md)
