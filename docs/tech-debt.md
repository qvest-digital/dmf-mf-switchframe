# Tech Debt & Deferred Review Findings

Captured from Phase 1 and Phase 2 code reviews. Address these before or during the relevant phase.

## Performance

### Write lock on video hot path
- **File:** `server/switcher/switcher.go` `handleVideoFrame()`
- **Issue:** Uses `mu.Lock()` (write lock) on every program-source video frame to check/clear `pendingIDR`. After the first keyframe, `pendingIDR` is always false, so we only need `mu.RLock()` on the steady-state path.
- **Fix:** Check `pendingIDR` under RLock first. Only upgrade to write lock when clearing it (once per cut). Reduces contention at high frame rates (60fps+) with many sources.
- **Priority:** Low. At 30fps, lock hold time is nanoseconds. Revisit if profiling shows contention.

## Correctness

### health.recordFrame only called for video, not audio
- **File:** `server/switcher/switcher.go` `handleVideoFrame()` line ~210
- **Issue:** `health.recordFrame()` is only called from `handleVideoFrame`, not `handleAudioFrame`. Audio-only sources (if they ever exist) would always show as offline.
- **Fix:** Call `health.recordFrame()` in `handleAudioFrame()` too.
- **Priority:** Low. No audio-only sources exist in Phase 1.

### Caption passthrough missing
- **File:** `server/switcher/source_viewer.go` `SendCaptions()`
- **Issue:** Captions are counted but dropped. In production (FCC compliance for US broadcasts), captions from the program source should be forwarded like audio.
- **Fix:** Add `handleCaptionFrame` to `frameHandler` interface, forward from program source in Switcher.
- **Priority:** Medium. Address before any deployment where captions are expected.

## Design

### ~~Single state callback (OnStateChange overwrites)~~ RESOLVED in Phase 2
- **Resolution:** Converted to fan-out callbacks via `OnStateChange()` appending to a slice. Multiple consumers (MoQ publisher, health monitor, etc.) now supported.

### Program relay not bridged to Prism's MoQ relay
- **File:** `server/cmd/switchframe/main.go` lines 83-91
- **Issue:** `main.go` creates a `programRelay` for the switcher and separately calls `server.RegisterStream("program")`, which creates its own relay. Frames routed through the switcher's program relay don't reach MoQ viewers subscribed to "program" via Prism.
- **Fix:** Either expose relay replacement in Prism's `distribution.Server`, or add a bridging viewer that copies frames from the switcher's relay to the server's relay.
- **Priority:** High. Required for MoQ video playback (Phase 3).

### Transition endpoint returns 501
- **File:** `server/control/api.go` `handleTransition()`
- **Issue:** Mix/wipe transitions are not yet implemented. The endpoint returns 501 Not Implemented.
- **Fix:** Implement transition state machine in Phase 3/4.
- **Priority:** Medium. Phase 4 feature.

### No context.Context on Switcher methods
- **File:** `server/switcher/switcher.go`
- **Issue:** `Cut()`, `SetPreview()`, etc. don't accept `context.Context`. Transitions in Phase 3+ will need cancellation (abort dissolve mid-transition).
- **Fix:** Add `ctx context.Context` as first parameter when implementing transitions.
- **Priority:** Low for Phase 1 (cuts are instantaneous). Required for Phase 3.

### ~~main.go standalone HTTP, not wired to Prism~~ RESOLVED in Phase 2
- **Resolution:** main.go now uses `ServerConfig.ExtraRoutes` to mount the REST API on Prism's HTTP/3 mux. MoQ control track publisher wired to switcher state callbacks.

## Testing

### time.Sleep in integration tests
- **File:** `server/switcher/integration_test.go`
- **Issue:** Uses `time.Sleep(10ms)` to wait for frame delivery. The frame path is fully synchronous (Relay -> sourceViewer -> Switcher -> programRelay -> viewer), so sleeps are unnecessary. If the path ever becomes async, they'll be flaky.
- **Fix:** Remove sleeps (path is sync) or replace with channels/polling with deadlines.
- **Priority:** Low. Tests pass reliably today.

## JSON/API

### ControlRoomState zero-valued fields in JSON
- **File:** `server/internal/types.go`
- **Issue:** Fields like `TransitionDurationMs`, `TransitionPosition`, `InTransition`, `AudioLevels` are always zero-valued in Phase 1. Every JSON response includes them as `0`/`false`/`null`.
- **Fix:** Add `omitempty` to future-phase fields. Be careful with `bool` (false is "empty" in Go).
- **Priority:** Low. A few extra bytes per response.

## Phase 2 — Frontend

### REST polling fallback instead of MoQ
- **File:** `ui/src/routes/+page.svelte`
- **Issue:** The MoQ control track subscriber is implemented but WebTransport connection is not yet established. The UI falls back to REST polling (`GET /api/switch/state` every 500ms) for state updates.
- **Fix:** Wire WebTransport connection in production mode. Keep REST polling as a fallback for browsers without WebTransport.
- **Priority:** Medium. REST polling works but adds latency and server load compared to event-driven MoQ updates.

### Vendored Prism TS files need sync strategy
- **File:** `ui/src/lib/prism/` (35+ files)
- **Issue:** Prism TypeScript modules are copied wholesale into the Switchframe repo. When Prism's TS source changes, the vendored copy must be manually updated. No automated diffing or version tracking exists.
- **Fix:** Add a sync script or Makefile target that diffs `ui/src/lib/prism/` against Prism's source directory and reports changes. Consider git submodule or npm package for Prism's TS client.
- **Priority:** Low for now (Prism TS API is stable). Will matter when Prism ships breaking changes.

### MoQ video playback not wired
- **File:** `ui/src/lib/prism/` (transport, decoder, renderer modules vendored but unused)
- **Issue:** The vendored Prism modules include full MoQ video transport, WebCodecs decode, and WebGPU render pipeline. These are imported but not connected to the multiview tiles — tiles currently show placeholder content.
- **Fix:** Wire `MoQMultiviewTransport` to per-source video decoders and renderers in each `SourceTile`.
- **Priority:** High. Required for Phase 3 (live video in multiview tiles).
