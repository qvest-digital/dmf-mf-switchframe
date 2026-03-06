// Package switcher implements the core video switching engine.
//
// The [Switcher] type manages source registration, preview/program selection,
// and frame routing. It uses [atomic.Pointer] for lock-free reads on the
// hot path (frame forwarding) and a mutex for state-changing commands
// (Cut, SetPreview, StartTransition).
//
// Key types:
//   - [Switcher]: Main state machine with Cut, SetPreview, StartTransition
//   - [TransitionConfig]: Codec factory configuration for dissolve/wipe transitions
//   - [DelayBuffer]: Per-source configurable frame delay (0-500ms)
//   - [FrameSynchronizer]: Freerun frame alignment across sources (90 kHz PTS)
//
// Frame routing: each registered source gets a sourceViewer that tags
// frames with the source key. Only the program source's frames are
// forwarded to the program relay for downstream viewers. After a cut,
// video and audio are gated until the first IDR keyframe from the new
// source to prevent decoder artifacts.
package switcher
