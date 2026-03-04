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

### ~~Program relay not bridged to Prism's MoQ relay~~ RESOLVED in Phase 3
- **Resolution:** Restructured main.go to use relay from `server.RegisterStream("program")` directly. Switcher's BroadcastVideo/Audio goes directly to MoQ viewers.

### Transition endpoint returns 501
- **File:** `server/control/api.go` `handleTransition()`
- **Issue:** Mix/wipe transitions are not yet implemented. The endpoint returns 501 Not Implemented.
- **Fix:** Implement transition state machine in Phase 3/4.
- **Priority:** Medium. Phase 4 feature.

### ~~No context.Context on Switcher methods~~ RESOLVED in Phase 3
- **Resolution:** Added `ctx context.Context` as first parameter to `Cut`, `SetPreview`, `SetLabel`.

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

### ~~REST polling fallback instead of MoQ~~ RESOLVED in Phase 3
- **Resolution:** WebTransport connection manager with automatic MoQ state sync. REST polling kept as automatic fallback when WebTransport unavailable.

### Vendored Prism TS files need sync strategy
- **File:** `ui/src/lib/prism/` (35+ files)
- **Issue:** Prism TypeScript modules are copied wholesale into the Switchframe repo. When Prism's TS source changes, the vendored copy must be manually updated. No automated diffing or version tracking exists.
- **Fix:** Add a sync script or Makefile target that diffs `ui/src/lib/prism/` against Prism's source directory and reports changes. Consider git submodule or npm package for Prism's TS client.
- **Priority:** Low for now (Prism TS API is stable). Will matter when Prism ships breaking changes.

### ~~MoQ video playback not wired~~ RESOLVED in Phase 3
- **Resolution:** Video playback manager connects MoQ subscriptions to decoders. Canvas elements added to multiview tiles and program/preview windows for live video rendering.

## Phase 3 — Audio / Video

### FDK AAC cgo bindings require system library
- **File:** `server/audio/fdk_cgo.go`, `fdk_decoder.go`, `fdk_encoder.go`
- **Issue:** Direct cgo bindings to system `fdk-aac` library. Requires `fdk-aac` installed via Homebrew (macOS) or apt (Linux). No pure-Go fallback.
- **Fix:** Document build requirements. Consider build tags to allow compile without cgo for development/testing.
- **Priority:** Low. All target deployments will have fdk-aac available.

### Audio crossfade not wired to production code path
- **File:** `server/audio/mixer.go`, `server/switcher/switcher.go`
- **Issue:** `OnCut(oldSource, newSource)` is implemented and tested but never called from `Switcher.Cut()`. Audio cuts are abrupt (no equal-power crossfade ramp). Additionally, `OnProgramChange` sets crossfade state internally but the crossfade requires both old and new source to deliver frames. If the old source stops sending (SRT disconnect), the crossfade hangs with no timeout.
- **Fix:** (1) Call `mixer.OnCut(oldProgram, newProgram)` from `Switcher.Cut()`. (2) Add a ~50ms timeout to crossfade state — if the outgoing source doesn't deliver, complete the transition without crossfade.
- **Priority:** Medium. Crossfade is implemented but not active in production.

### PFL manager is stub-only
- **File:** `ui/src/lib/audio/pfl.ts`
- **Issue:** PFL management API is implemented but actual audio decode/routing through Web Audio API is not wired. The `enablePFL` function creates placeholder entries but doesn't decode source audio to speakers.
- **Fix:** Wire PrismAudioDecoder → AudioContext → GainNode for PFL routing when WebTransport video sources are connected.
- **Priority:** Low. PFL is operator convenience, not required for basic operation.

### Video playback manager not connected to canvas rendering
- **File:** `ui/src/lib/video/playback.ts`, `ui/src/components/SourceTile.svelte`
- **Issue:** The video playback manager tracks sources and decoder state, but actual frame decode → canvas rendering is not wired. Canvas elements exist in tiles and program/preview windows but show black.
- **Fix:** Connect playback manager to vendored Prism decoder/renderer when WebTransport delivers video tracks.
- **Priority:** High. Required for live video display.
