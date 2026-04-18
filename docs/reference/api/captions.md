# API: captions

> **Where you are:** [docs](../../../README.md) → reference → api → captions
> **Read this first:** [api.md](../api.md)
> **See also:** [subsystems/captions.md](../../subsystems/captions.md) · [asr.md](../../asr.md) · [api/asr.md](asr.md)

**TL;DR** Three endpoints control closed captioning: set mode (`off`/`passthrough`/`author`), feed author-mode text, and read current state. ASR-driven captions flow through a separate backend (see [api/asr.md](asr.md)); these endpoints are for human-authored captions and pass-through from source SEI.

## Endpoints

### `POST /api/captions/mode`

**Purpose:** set the caption operating mode.
**Auth:** required.
**Handler:** [`control/api_captions.go`](../../../server/control/api_captions.go) → `(*API).handleCaptionMode`.

**Request body** (`control.captionModeRequest`): `{ "mode": "off" | "passthrough" | "author" }`.

- `off` — strip all captions from program output.
- `passthrough` — forward captions embedded in the selected source (SEI or VANC).
- `author` — operator types captions; the caption manager injects them into the program SEI.

**Response 200:** `caption.State`.

**Errors:** 400 (invalid mode), 501 (caption manager not configured).

**Related:** [subsystems/captions.md](../../subsystems/captions.md)

---

### `POST /api/captions/text`

**Purpose:** ingest a text fragment, trigger a newline, or clear the display buffer. Only valid in `author` mode.
**Auth:** required.
**Handler:** `(*API).handleCaptionText`.

**Request body** (`control.captionTextRequest`):

```json
{ "text": "Welcome to the show!" }
```

or

```json
{ "newline": true }
```

or

```json
{ "clear": true }
```

**Response 200:** `caption.State`.

**Errors:** 400 (not in author mode, no action specified), 501.

---

### `GET /api/captions/state`

**Purpose:** return the current caption state (mode, author buffer, per-source caption availability flags).
**Handler:** `(*API).handleCaptionState`.

**Response 200:** `caption.State` / `internal.CaptionState`.

## Related docs

- **Reference:** [api.md](../api.md) · [api/asr.md](asr.md) · [state-broadcast.md](../state-broadcast.md#captionstate)
- **Subsystems:** [captions.md](../../subsystems/captions.md)
- **Concepts:** [asr.md](../../asr.md)
