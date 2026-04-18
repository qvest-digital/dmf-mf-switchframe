# API: ASR (automatic speech recognition)

> **Where you are:** [docs](../../../README.md) → reference → api → asr
> **Read this first:** [api.md](../api.md)
> **See also:** [asr.md](../../asr.md) · [integration/asr-sidecar.md](../../integration/asr-sidecar.md) · [api/captions.md](captions.md)

**TL;DR** Five endpoints expose the ASR subsystem: status, pacing/confidence config, available models per backend, and per-backend opaque config (JSON pass-through). ASR drives automatic captions — see [api/captions.md](captions.md) for human-authored caption control. ASR endpoints are always registered; behavior degrades gracefully when no ASR manager is configured.

## Endpoints

### `GET /api/asr/status`

**Purpose:** return the current ASR state (available, active, backend, available backends, model, confidence filter, speaker detection, and opaque backend config).
**Handler:** [`control/api_asr.go`](../../../server/control/api_asr.go) → `(*API).handleASRStatus`.

**Response 200:** `internal.ASRState`. Always returns `{ "available": false }` when no ASR manager is configured.

---

### `PUT /api/asr/config`

**Purpose:** update ASR pacing settings (active, backend choice, model, confidence filter, speaker detection).
**Handler:** `(*API).handleASRConfig`.

**Request body:** all fields optional — see the handler for the exact shape:

```json
{
  "active": true,
  "backend": "whisper",
  "modelName": "large-v3-turbo",
  "confidence": "medium",
  "speakerDetection": "on"
}
```

Accepted values:

- `confidence`: `off`, `low`, `medium`, `high`
- `speakerDetection`: `off`, `on`
- `backend`: any string from `availableBackends` in the status response

**Response 200:** full `ControlRoomState`.

**Errors:** 400 (unknown backend/model/preset).

---

### `GET /api/asr/models`

**Purpose:** list models available in the active backend.
**Handler:** `(*API).handleASRModels`.

**Response 200:** `{ "models": ["tiny", "base", "small", ...] }`.

---

### `GET /api/asr/backend/config`

**Purpose:** return the opaque JSON config for the active backend.
**Handler:** `(*API).handleASRBackendConfigGet`.

**Response 200:** `json.RawMessage` passed through as-is from the backend.

---

### `PUT /api/asr/backend/config`

**Purpose:** set the opaque JSON config for the active backend. Structure depends on backend.
**Handler:** `(*API).handleASRBackendConfigPut`.

**Request body:** arbitrary JSON.

**Response 200:** full `ControlRoomState`.

**Errors:** 400 (invalid JSON / backend-rejected config), serialized through the backend's own validation.

## Related docs

- **Concepts:** [asr.md](../../asr.md)
- **Reference:** [api.md](../api.md) · [api/captions.md](captions.md) · [state-broadcast.md](../state-broadcast.md#asrstate)
- **Integration:** [asr-sidecar.md](../../integration/asr-sidecar.md)
