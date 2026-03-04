# Tech Debt & Deferred Review Findings

Captured from Phase 1, Phase 2, Phase 3, and Phase 4 code reviews. Address these before or during the relevant phase.

## Performance

### ~~Write lock on video hot path~~ RESOLVED
- **Resolution:** `handleVideoFrame` now uses RLock fast path for steady-state. Write lock only acquired when clearing `pendingIDR` (once per cut).

## Correctness

### ~~health.recordFrame only called for video, not audio~~ RESOLVED
- **Resolution:** `handleAudioFrame` now calls `health.recordFrame(sourceKey)` at the top, matching `handleVideoFrame`.

### ~~Caption passthrough missing~~ RESOLVED
- **Resolution:** Added `handleCaptionFrame` to `frameHandler` interface. `sourceViewer.SendCaptions` forwards to handler. `Switcher.handleCaptionFrame` forwards program source captions to program Relay, gated by `pendingIDR`.

## Design

### ~~Single state callback (OnStateChange overwrites)~~ RESOLVED in Phase 2
- **Resolution:** Converted to fan-out callbacks via `OnStateChange()` appending to a slice. Multiple consumers (MoQ publisher, health monitor, etc.) now supported.

### ~~Program relay not bridged to Prism's MoQ relay~~ RESOLVED in Phase 3
- **Resolution:** Restructured main.go to use relay from `server.RegisterStream("program")` directly. Switcher's BroadcastVideo/Audio goes directly to MoQ viewers.

### ~~Transition endpoint returns 501~~ RESOLVED in Phase 4
- **Resolution:** Full transition REST API implemented: `/api/transition` (POST auto/manual), `/api/transition/position` (PUT T-bar), `/api/ftb` (POST fade to black). TransitionEngine handles Mix, Dip to Black, and FTB.

### ~~No context.Context on Switcher methods~~ RESOLVED in Phase 3
- **Resolution:** Added `ctx context.Context` as first parameter to `Cut`, `SetPreview`, `SetLabel`.

### ~~main.go standalone HTTP, not wired to Prism~~ RESOLVED in Phase 2
- **Resolution:** main.go now uses `ServerConfig.ExtraRoutes` to mount the REST API on Prism's HTTP/3 mux. MoQ control track publisher wired to switcher state callbacks.

## Testing

### ~~time.Sleep in integration tests~~ RESOLVED
- **Resolution:** Removed all `time.Sleep(10ms)` calls from integration tests. Frame path is fully synchronous (Relay → sourceViewer → Switcher → programRelay → viewer), so no async waits needed.

## JSON/API

### ~~ControlRoomState zero-valued fields in JSON~~ RESOLVED
- **Resolution:** Added `omitempty` to `TransitionDurationMs`, `TransitionPosition`, `InTransition`, and `AudioLevels` fields in `ControlRoomState`.

## Phase 2 — Frontend

### ~~REST polling fallback instead of MoQ~~ RESOLVED in Phase 3
- **Resolution:** WebTransport connection manager with automatic MoQ state sync. REST polling kept as automatic fallback when WebTransport unavailable.

### ~~Vendored Prism TS files need sync strategy~~ RESOLVED
- **Resolution:** Added `make sync-prism-ts` Makefile target that diffs `ui/src/lib/prism/` against Prism's `web/src/` directory and reports changes. Configurable via `PRISM_TS_SRC` variable.

### ~~MoQ video playback not wired~~ RESOLVED in Phase 3
- **Resolution:** Video playback manager connects MoQ subscriptions to decoders. Canvas elements added to multiview tiles and program/preview windows for live video rendering.

## Phase 3 — Audio / Video

### FDK AAC cgo bindings require system library
- **File:** `server/audio/fdk_cgo.go`, `fdk_decoder.go`, `fdk_encoder.go`
- **Issue:** Direct cgo bindings to system `fdk-aac` library via pkg-config. Requires `fdk-aac` installed via Homebrew (macOS) or apt (Linux). No pure-Go fallback.
- **Fix:** Consider build tags to allow compile without cgo for development/testing.
- **Priority:** Low. All target deployments will have fdk-aac available.

### ~~Audio crossfade not wired to production code path~~ RESOLVED
- **Resolution:** `Switcher.Cut()` now auto-calls `mixer.OnCut(oldProgram, newProgram)` and `mixer.OnProgramChange(newProgram)` via the `audioCutHandler` interface. Added 50ms crossfade timeout — if the outgoing source stops delivering frames, the crossfade completes with only the incoming source's audio.

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

## Phase 4 — Transitions

### OpenH264 cgo bindings require system library
- **File:** `server/transition/openh264_cgo.go`, `openh264_decoder.go`, `openh264_encoder.go`
- **Issue:** Direct cgo bindings to system `openh264` library via pkg-config. Requires `openh264` installed via Homebrew (macOS) or apt (Linux). No pure-Go fallback.
- **Fix:** Consider build tags to allow compile without cgo for development/testing.
- **Priority:** Low. Same pattern as fdk-aac.

### FTB reverse toggle not implemented
- **File:** `server/switcher/switcher.go` `FadeToBlack()`
- **Issue:** FTB toggle off (unfade from black) is a simple state reset, not a reverse transition animation.
- **Fix:** Add reverse mode to TransitionEngine.
- **Priority:** Medium.

### WebGPU dissolve not implemented (Canvas 2D only)
- **File:** `ui/src/lib/video/dissolve.ts`
- **Issue:** The dissolve renderer always uses Canvas 2D fallback. WebGPU path is designed but not wired.
- **Fix:** Init GPUDevice during page load, create pipeline with WGSL shader.
- **Priority:** Low. Canvas 2D dissolve is visually identical for the operator.

### No AVC1↔Annex B conversion in transition pipeline
- **File:** `server/transition/openh264_decoder.go`, `openh264_encoder.go`
- **Issue:** OpenH264 expects Annex B but Prism stores AVC1 format.
- **Fix:** Use Prism's `mp4ff/avc` package for conversion.
- **Priority:** High. Required for real video sources. Mock codecs work without conversion.
