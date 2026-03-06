// Package replay provides an instant replay system with variable-speed
// playback for live video switching.
//
// Each source gets a GOP-aligned circular buffer that captures encoded
// H.264 frames with wall-clock timestamps. The [Manager] orchestrates
// mark-in/out points, playback, and per-source buffer lifecycle. The
// replayPlayer decodes clips, sorts by PTS, and re-encodes with frame
// duplication for slow-motion (0.25x-1x speed).
//
// Key types:
//   - [Manager]: Replay orchestration (mark-in/out, play, stop, buffer management)
//   - [Config]: Buffer duration, codec factories, replay relay reference
//   - [ReplayStatus]: Current player state, mark points, active source
//   - [SourceBufferInfo]: Per-source buffer fill level and time range
//
// Replay output is routed to a dedicated "replay" relay so browsers can
// subscribe via MoQ for replay monitoring. Audio is muted in the current
// version; only video frames are replayed.
package replay
