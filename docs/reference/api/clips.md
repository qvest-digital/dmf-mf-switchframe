# API: clips and clip players

> **Where you are:** [docs](../../../README.md) → reference → api → clips
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/clips.md](../../subsystems/clips.md) · [clips.md](../../clips.md)

**TL;DR** Eighteen endpoints manage the clip library (upload, CRUD, pin for lifecycle retention, import from recordings) and the four player slots (load, eject, play/pause/stop, seek, speed, loop). Uploads go through a transcode pipeline; progress is broadcast via `ClipUploadProgress` so the UI can show a status bar.

## Clip library

### `GET /api/clips`

**Purpose:** list all stored clips.
**Handler:** [`control/api_clips.go`](../../../server/control/api_clips.go) → `(*API).handleClipsList`.

**Response 200:** `[]internal.ClipInfo`.

---

### `GET /api/clips/{id}`

**Handler:** `(*API).handleClipGet`. **Response 200:** `internal.ClipInfo`. **Errors:** 404 (`clip.ErrNotFound`).

---

### `PUT /api/clips/{id}`

**Purpose:** update clip metadata (name, loop setting).
**Handler:** `(*API).handleClipUpdate`.

---

### `DELETE /api/clips/{id}`

**Handler:** `(*API).handleClipDelete`. **Response 204.**

---

### `POST /api/clips/{id}/pin`

**Purpose:** pin/unpin a clip. Pinned clips are not auto-evicted by the retention policy.
**Handler:** `(*API).handleClipPin`.

**Request body:** `{ "pinned": true }`.

---

### `POST /api/clips/upload`

**Purpose:** upload a media file as a new clip. Accepts common container formats; the server transcodes to the internal pipeline format. Progress is streamed through `ClipUploadProgress` in the state broadcast.
**Handler:** `(*API).handleClipUpload`.

**Request:** `multipart/form-data` with a `file` field (up to 2 GB). Optional `name` field.

**Response 202:** `internal.ClipInfo` (metadata; transcode may still be in progress).

**Errors:** 400 (`clip.ErrInvalidFormat`, `clip.ErrCorruptFile`, `clip.ErrOddDimensions`, `clip.ErrNoVideo`, `clip.ErrInvalidName`), 409 (`clip.ErrStorageFull`, `clip.ErrAlreadyExists`), 413 (payload too large), 422 (`clip.ErrTranscodeFailed`).

**Emits:** state broadcast with progressing `ClipUpload`, then final `Sources` entry if the clip is a source.

**Related:** [subsystems/clips.md#upload-pipeline](../../subsystems/clips.md#upload-pipeline)

---

### `GET /api/clips/recordings`

**Purpose:** list `.ts` files in the recording directory that can be imported as clips.
**Handler:** `(*API).handleClipRecordings`.

**Response 200:** `[{ "filename": "...", "sizeBytes": ..., "modifiedMs": ... }, ...]`.

---

### `GET /api/clips/recordings/download`

**Purpose:** stream a recording file back to the client.
**Handler:** `(*API).handleRecordingDownload`. **Query:** `?filename=<basename>`.

**Response 200:** `video/mp2t` with `Content-Disposition: attachment`.

**Errors:** 400 (missing/invalid filename), 404.

---

### `POST /api/clips/from-recording`

**Purpose:** import a saved recording into the clip library (no transcode — the `.ts` file is referenced directly).
**Handler:** `(*API).handleClipFromRecording`.

**Request body:** `{ "filename": "recording-2026-04-17T12-30-00.ts", "name": "show-open" }`.

**Response 201:** `internal.ClipInfo`.

**Errors:** 400 (invalid name / filename), 404 (file not in recording dir), 409 (already exists).

## Clip players

Four numbered player slots live at `/api/clips/players/{n}` (n = 1-4). Each slot loads one clip at a time.

### `GET /api/clips/players`

**Purpose:** return state of all four players.
**Handler:** `(*API).handleClipPlayersList`.

**Response 200:** `[]internal.ClipPlayerInfo`.

---

### `POST /api/clips/players/{n}/load`

**Purpose:** load a clip into a player.
**Handler:** `(*API).handleClipPlayerLoad`.

**Request body:** `{ "clipId": "abc123" }`.

**Errors:** 400 (`clip.ErrInvalidPlayer`, `clip.ErrInvalidName`), 404 (`clip.ErrNotFound`), 409 (`clip.ErrPlayerBusy`, `clip.ErrPlayerFull`).

---

### `POST /api/clips/players/{n}/eject`

**Handler:** `(*API).handleClipPlayerEject`. Unloads the clip.

---

### `POST /api/clips/players/{n}/play`

**Handler:** `(*API).handleClipPlayerPlay`. **Errors:** 400 (`clip.ErrPlayerEmpty`).

---

### `POST /api/clips/players/{n}/pause`

**Handler:** `(*API).handleClipPlayerPause`.

---

### `POST /api/clips/players/{n}/stop`

**Handler:** `(*API).handleClipPlayerStop`. Resets position to 0 but keeps the clip loaded.

---

### `POST /api/clips/players/{n}/seek`

**Purpose:** seek to a normalized position (0.0-1.0).
**Handler:** `(*API).handleClipPlayerSeek`.

**Request body:** `{ "position": 0.5 }`.

**Errors:** 400 (`clip.ErrInvalidSeek`).

---

### `POST /api/clips/players/{n}/speed`

**Purpose:** set playback speed.
**Handler:** `(*API).handleClipPlayerSpeed`.

**Request body:** `{ "speed": 1.5 }`.

**Errors:** 400 (`clip.ErrInvalidSpeed`).

---

### `POST /api/clips/players/{n}/loop`

**Purpose:** enable/disable loop.
**Handler:** `(*API).handleClipPlayerLoop`.

**Request body:** `{ "loop": true }`.

## Related docs

- **Concepts:** [clips.md](../../clips.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#clipplayerinfo)
- **Subsystems:** [clips.md](../../subsystems/clips.md)
