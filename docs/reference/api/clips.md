# API: clips and clip players

> **Where you are:** [docs](../../../README.md) → reference → api → clips
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/clips.md](../../subsystems/clips.md) · [clips.md](../../subsystems/clips.md)

**TL;DR** Nineteen endpoints manage the clip library (upload, CRUD, pin for lifecycle retention, import from recordings, rescan of `--clip-library-dir`) and the four player slots (load, eject, play/pause/stop, seek, speed, loop). Uploads go through a transcode pipeline; progress is broadcast via `ClipUploadProgress` so the UI can show a status bar.

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

**Handler:** `(*API).handleClipDelete`. **Response 204.** A clip in `--clip-library-dir` is deleted from the library, for every engine and later run that uses it.

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

**Related:** [subsystems/clips.md](../../subsystems/clips.md)

---

### `POST /api/clips/rescan`

**Purpose:** list clips added to `--clip-library-dir` since the last scan and drop clips whose library file is gone.
**Handler:** `(*API).handleClipRescan`.

**Response 200:** `{ "added": 1, "removed": 0, "skipped": ["unplayable.mp4"] }`.

**Errors:** 409 (no clip library attached).

---

### `GET /api/clips/recordings`

**Purpose:** list the `.ts` files in the recording directory and in `--recording-archive-dir`, with how far along each one is.
**Handler:** `(*API).handleClipRecordings`.

**Response 200:** `[{ "filename": "...", "path": "...", "byteSize": ..., "modTime": "...", "state": "...", "error": "..." }, ...]`. `state` is `recording` (the file the running recording writes), `finalizing` (closed, still being copied into the archive), `ready`, or `failed` (empty: no program video reached the recorder; `error` says so).

---

### `POST /api/clips/recordings/download-url`

**Purpose:** issue a download URL for one recording file. A browser download cannot send the `Authorization` header, so the URL carries a ticket valid for one request within a minute, for that file only.
**Handler:** `(*API).handleRecordingDownloadURL`.

**Request body:** `{ "path": "/abs/path/to/recording.ts" }`.

**Response 200:** `{ "url": "/api/clips/recordings/download?path=...&ticket=..." }`.

**Errors:** 400 (missing path), 403 (path outside the recording directories), 404.

---

### `GET /api/clips/recordings/download`

**Purpose:** stream a recording file back to the client.
**Handler:** `(*API).handleRecordingDownload`. **Query:** `?path=<absolute path>`, plus `&ticket=` in place of the `Authorization` header.

**Response 200:** `video/mp2t` with `Content-Disposition: attachment`.

**Errors:** 400 (missing/invalid path), 401 (ticket expired, used or issued for another file), 403 (path outside the recording directories), 404.

---

### `POST /api/clips/from-recording`

**Purpose:** copy a recording into the clip store as a new clip.
**Handler:** `(*API).handleClipFromRecording`.

**Request body:** `{ "path": "/abs/path/to/recording.ts" }`.

**Response 201:** `clip.Clip`.

**Errors:** 400 (missing path, path outside the recording directories, empty recording, or no playable video).

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

- **Concepts:** [clips.md](../../subsystems/clips.md)
- **Reference:** [api.md](../api.md) · [state-broadcast.md](../state-broadcast.md#replay-and-clips)
- **Subsystems:** [clips.md](../../subsystems/clips.md)
