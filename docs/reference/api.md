# REST API reference

> **Where you are:** [docs](../../README.md) → reference → api
> **Read this first:** [architecture.md](../architecture.md)
> **See also:** [state-broadcast.md](state-broadcast.md) · [fast-control.md](../reference/fast-control.md) · [subsystems/control-plane.md](../subsystems/control-plane.md)

**TL;DR** The Switchframe REST API exposes the full state and command surface of the switcher over HTTP/3 (with HTTP/1.1 fallback). Every UI button, macro action, and automation hook ultimately lands on one of the 291 endpoints documented here (plus one `/api/v1/` wildcard compatibility route). Endpoints are grouped by subsystem — switching, audio, transition, graphics, DVE, clips, playout, replay, SCTE-35, operator, captions, comms, ST map, color grade, AI segmentation, ASR, output, peer, sync, pipeline, and debug — and share a common auth, error, and state-broadcast model. This is the biggest doc in the project; drill into the per-section files below rather than reading end-to-end.

## Table of contents

The API has 291 real endpoints split across the files below. Each sub-file follows the same per-endpoint format (Purpose, Auth, Handler, Request, Response, Errors, Emits, Related) and opens with a short narrative explaining the subsystem.

### Core

- [switching](api/switching.md) — program/preview cut, state, sync-health (3 endpoints)
- [transition](api/transition.md) — auto transition, T-bar position, fade-to-black (3 endpoints)
- [format-encoder](api/format-encoder.md) — pipeline format + runtime encoder (4 endpoints)
- [source](api/source.md) — source CRUD, label, delay, position, SRT (12 endpoints)
- [audio](api/audio.md) — level, mute, AFV, trim, EQ, compressor, HPF, gate, limiter, master, delay (18 endpoints)

### Production

- [transition-wipes](api/transition-wipes.md) — wipe pattern catalog, custom upload (5 endpoints)
- [graphics](api/graphics.md) — DSK layer CRUD + animation, image/sequence/ticker, stingers (31 endpoints)
- [graphics-html5](api/graphics-html5.md) — browser-based HTML5 graphics (8 endpoints)
- [dve](api/dve.md) — DVE layout, slots, presets (10 endpoints)
- [keying](api/keying.md) — per-source upstream key configuration (3 endpoints)
- [replay](api/replay.md) — mark-in/out, play, pause, speed, shutter-angle (15 endpoints)
- [clips](api/clips.md) — clip CRUD, upload, from-recording, 4-player slots (18 endpoints)
- [captions](api/captions.md) — closed-caption mode and author-mode text input (3 endpoints)
- [stmap](api/stmap.md) — per-pixel coordinate remapping library and assignments (12 endpoints)
- [colorgrade](api/colorgrade.md) — LUT-based looks and per-look corrector settings (9 endpoints)
- [ai-segment](api/ai-segment.md) — per-source AI background segmentation (4 endpoints)
- [asr](api/asr.md) — automatic speech recognition status and config (5 endpoints)

### Output

- [output](api/output.md) — recording, SRT output, destinations (CBR pacer), confidence thumbnail (15 endpoints)

### Automation

- [scte35](api/scte35.md) — cue inject, return, cancel, hold, extend, rules CRUD, webhook (20 endpoints)
- [preset](api/preset.md) — switcher preset CRUD and recall (8 endpoints)
- [macro](api/macro.md) — macro CRUD, run, cancel, dismiss (7 endpoints)
- [playout](api/playout.md) — playout channels, playlists, pods, cache, BXF (48 endpoints)

### Platform

- [operator](api/operator.md) — operator registration, sessions, subsystem locks (9 endpoints)
- [comms](api/comms.md) — operator voice comms join/leave/mute/status (4 endpoints)
- [peer-sync](api/peer-sync.md) — dual-engine peer health, force-leader, reconcile lock, state snapshot (8 endpoints)
- [pipeline](api/pipeline.md) — pipeline snapshot, per-node bypass, thumbnails (4 endpoints)
- [debug-perf](api/debug-perf.md) — debug snapshot, live perf, baseline CRUD, connection counter (5 endpoints)

## Global conventions

Everything in this section applies to every endpoint. Per-section files only note departures.

### Versioning

Every route is registered under both `/api/<path>` and `/api/v1/<path>`. The v1 alias is served by the same handler through an internal request rewrite. New clients should prefer `/api/v1/` to protect against a future `/api/v2/` bump; existing clients on `/api/` continue to work.

Registration happens in `control.(*API).RegisterOnMux`. Routes use Go 1.22+ `ServeMux` patterns (`"POST /api/switch/cut"`) so the method is part of the match — hitting a URL with the wrong method returns 405 Method Not Allowed rather than a generic 404.

### Authentication

Auth is applied by `control.AuthMiddleware` wrapping the entire mux. The middleware accepts:

1. **Session API token** — a 32-byte hex string minted at startup (or supplied via `--api-token`) and compared with `crypto/subtle.ConstantTimeCompare`. Used by the browser UI after WebTransport handshake.
2. **Operator bearer token** — issued by `POST /api/operator/register`. Checked via the `OperatorTokenChecker` callback that wraps `operator.Store.GetByToken`.

Both forms use the `Authorization: Bearer <token>` header. Missing or invalid tokens return 401 with `WWW-Authenticate: Bearer realm="switchframe"`.

**Exempt paths** (no auth required) are listed in `control.authExemptPaths`:

- `/api/cert-hash` — WebTransport certificate fingerprint for self-signed bootstrap
- `/health`, `/metrics` — Prometheus scraping
- `/api/operator/register`, `/api/operator/reconnect`, `/api/operator/heartbeat` — authenticated via invite token or stored operator token inside the handler
- `/api/peer/health` — called by the peer engine with no shared secret; returns only non-sensitive status
- `/api/peer/force-leader` — authenticated with the separate `X-Control-Plane-Secret` header (disabled when no secret is configured)

In demo mode the CLI installs `NoopAuthMiddleware`, skipping the bearer check entirely.

### Request bodies

JSON is the default for command bodies. Multipart/form-data or application/octet-stream is used for binary uploads (graphics images, stingers, clips, ST maps, color LUTs). The global `MaxBytesMiddleware` caps JSON bodies at 16 MB (enough for a 1080p RGBA graphics frame). Upload endpoints have per-handler limits (typically 256 MB for stingers/sequences, 2 GB for clips).

Path parameters use Go 1.22 ServeMux syntax (`{key}`, `{id}`, `{name}`, `{n}`, `{source}`, `{eventId}`, `{segEventId}`, `{podId}`, `{type}`, `{lockId}`, `{sourceKey}`) accessed via `r.PathValue()`. Query parameters are used sparingly — chiefly `?source=...` on read-only peek/filter endpoints.

### Response bodies

Most state-mutating endpoints return the full updated `ControlRoomState` JSON object so clients can reconcile immediately without waiting for the next broadcast. See [state-broadcast.md](state-broadcast.md) for the schema. Read-only endpoints return purpose-specific JSON (typed responses from `control.*Response` structs) or binary content (JPEG thumbnails, PNG images, `.ts` recordings).

Successful responses are 200 OK for updates, 201 Created for creations, 202 Accepted for async operations (macro run, clip upload, graphics sequence upload), and 204 No Content for deletions and fire-and-forget operations.

### Errors

Every error response is `application/json` shaped as `{"error": "<message>"}` and written through `control/httperr.Write` or `control/httperr.WriteErr`. Sentinel errors from subsystem packages are mapped to HTTP status codes by `control.errorStatus` (see `control/errmap.go`). The canonical mapping:

| Class | Status | Typical sentinel |
|---|---|---|
| Bad input | 400 | `ErrInvalidTrim`, `ErrInvalidSpeed`, `ErrEmptyName`, malformed JSON |
| Unauthorized | 401 | missing/invalid bearer token |
| Forbidden | 403 | `operator.ErrNoPermission`, wrong role |
| Not found | 404 | `switcher.ErrSourceNotFound`, `preset.ErrNotFound`, `clip.ErrNotFound`, etc. |
| Method not allowed | 405 | mux method mismatch, `ErrNotSRTSource` on DELETE |
| Conflict | 409 | `switcher.ErrFormatDuringTransition`, `output.ErrRecorderActive`, `operator.ErrSubsystemLocked`, `clip.ErrPlayerBusy` |
| Unprocessable | 422 | `clip.ErrTranscodeFailed` |
| Payload too large | 413 | body exceeds per-handler limit |
| Not implemented | 501 | subsystem not configured (e.g., no mixer in non-cgo build) |
| Service unavailable | 503 | `graphics.ErrCompositorClosed`, `comms.ErrOpusUnavailable` |
| Internal | 500 | unknown / wrapped errors |

When the server fails a command that went through the sequenced command queue (`control/timed.go`), the error message is also written to `ControlRoomState.LastExecutedErr` and echoed in `RecentCommandErrors` so the UI can surface it even if the HTTP response was lost.

### Timed command queue

A subset of state-mutating endpoints (all cut/preview/transition/audio-fader/graphics-on-off/source-label-delay-position routes, plus format/encoder) are wrapped in `timedHandler` or `timedHandlerWithPath`. These handlers pre-read the body, assign a sequence number, and either execute inline or defer to the command queue so commands carrying a `scheduledUs` header can be timed against the synchronized clock for dual-engine execution. See [subsystems/control-plane.md](../subsystems/control-plane.md#command-queue) for the full lifecycle. On a single-engine deployment these wrappers behave as plain handlers — the sequence number is bumped and the body is read once.

### State broadcast emission

Handlers that mutate state trigger a broadcast of the fresh `ControlRoomState` via `(*API).broadcast()`. Readers receive this over the MoQ `control` track (or the REST polling fallback). Subsystem packages often also emit their own broadcast via `OnStateChange` callbacks — for example, the graphics compositor, DVE compositor, session manager, and macro runner all publish mid-operation updates (e.g., progress during a macro execution). Each endpoint block documents the broadcast trigger in its **Emits** line.

### Metrics emission

`control.MetricsMiddleware` wraps every request and records:

- `metrics.HTTPRequestsTotal{method, pattern, status}` — counter
- `metrics.HTTPRequestDuration{method, pattern}` — histogram in seconds

`pattern` is `r.Pattern` (Go 1.22+ bound path template), not the raw path, so cardinality stays bounded. The `cert-hash` admin endpoint and metrics-scrape path are not routed through this mux.

### Logging and request IDs

`control.LoggerMiddleware` generates (or preserves from `X-Request-ID`) a request ID per request, creates a child slog logger with attributes, echoes the ID in the `X-Request-ID` response header, and logs the completed request at `INFO` (or `DEBUG` for noisy polled paths: `/api/switch/state`, `/metrics`). Handlers pull the child logger via `control.LogFromCtx(r.Context())`.

### Who triggered what

Every authenticated mutating handler calls `(*API).setLastOperator(r)` which resolves the bearer token to an operator name via `operator.Store.GetByToken` and stores it as the "last operator" for the state broadcast. Non-operator-triggered broadcasts (e.g., mixer auto-ducking, SRT reconnect) explicitly clear the field. The UI uses this to badge the most recent change.

## Related docs

- **Concepts:** [pipeline.md](../concepts/pipeline.md) · [locking-and-concurrency.md](../concepts/locking-and-concurrency.md) · [frame-sync-and-timing.md](../concepts/frame-sync-and-timing.md)
- **Reference:** [state-broadcast.md](state-broadcast.md) · [fast-control.md](../reference/fast-control.md) · [metrics.md](metrics.md)
- **Subsystems:** [control-plane.md](../subsystems/control-plane.md) · [switcher.md](../subsystems/switcher.md) · [output.md](../subsystems/output.md)
- **Integration:** [ui-server-contract.md](../integration/ui-server-contract.md)
