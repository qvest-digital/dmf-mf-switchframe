# Tech Debt & Deferred Review Findings

Captured from Phase 1 code review. Address these before or during the relevant phase.

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

### Single state callback (OnStateChange overwrites)
- **File:** `server/switcher/switcher.go` `OnStateChange()` line ~50
- **Issue:** Second call to `OnStateChange()` silently replaces the first callback. If multiple consumers need state updates (MoQ publisher + metrics + health), the second registration kills the first.
- **Fix:** Change `stateCallback` to `[]func(internal.ControlRoomState)`, or use a channel/fan-out.
- **Priority:** Medium. Fix when adding a second state consumer.

### No context.Context on Switcher methods
- **File:** `server/switcher/switcher.go`
- **Issue:** `Cut()`, `SetPreview()`, etc. don't accept `context.Context`. Transitions in Phase 3+ will need cancellation (abort dissolve mid-transition).
- **Fix:** Add `ctx context.Context` as first parameter when implementing transitions.
- **Priority:** Low for Phase 1 (cuts are instantaneous). Required for Phase 3.

### main.go standalone HTTP, not wired to Prism
- **File:** `server/cmd/switchframe/main.go`
- **Issue:** REST API runs on its own `http.Server` on `:8080`, separate from Prism's WebTransport/HTTP3 server. The `ExtraRoutes` extension point was added to Prism but isn't used yet.
- **Fix:** When integrating full Prism server, use `ServerConfig.ExtraRoutes` to mount the API on Prism's mux.
- **Priority:** Required for Phase 2 (browser UI needs both MoQ and REST on same origin).

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
