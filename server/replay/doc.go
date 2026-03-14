// Package replay provides an instant replay system with variable-speed
// playback for live video switching.
//
// Each source gets a GOP-aligned circular buffer that captures encoded
// H.264 frames with wall-clock timestamps. The [Manager] orchestrates
// mark-in/out points, playback, and per-source buffer lifecycle. The
// replayPlayer decodes clips, sorts by PTS, and re-encodes with frame
// duplication for slow-motion (0.25x-1x speed).
//
// Audio is time-stretched for pitch-preserved slow-motion playback using
// a phase vocoder (STFT-based spectral processing) as the primary path,
// with WSOLA (Waveform Similarity Overlap-Add) as a fallback. Frame
// interpolation is pluggable via the [FrameInterpolator] interface, with
// alpha-blend and MCFI (motion-compensated frame interpolation)
// implementations available.
//
// Key types:
//   - [Manager]: Replay orchestration (mark-in/out, play, stop, buffer management)
//   - [Config]: Buffer duration, codec factories, replay relay reference
//   - [Status]: Current player state, mark points, active source
//   - [SourceBufferInfo]: Per-source buffer fill level and time range
//   - [FrameInterpolator]: Pluggable frame interpolation (blend, MCFI)
//
// Replay output is routed to a dedicated "replay" relay so browsers can
// subscribe via MoQ for replay monitoring.
package replay
