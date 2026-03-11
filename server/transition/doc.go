// Package transition implements the video transition engine for dissolves,
// dips, wipes, fade-to-black, and stinger transitions.
//
// All blending operates directly in YUV420 (BT.709) space, matching
// hardware broadcast mixers (ATEM, Ross) and avoiding costly YUV-RGB
// round-trip conversions. The [Engine] is created per-transition
// and destroyed on complete or abort, returning to zero-CPU passthrough
// between transitions.
//
// Key types:
//   - [Engine]: Per-transition lifecycle (start, ingest frames, complete/abort)
//   - [FrameBlender]: YUV420 blending for mix, dip, wipe, FTB, and stinger
//   - [EngineConfig]: Encoder/decoder factories and transition parameters
//   - [StingerData]: Pre-decoded PNG sequence with per-pixel alpha plane
//
// Wipe transitions support 6 directions (horizontal, vertical, box) using
// per-pixel threshold masks with a 4px soft edge. The T-bar provides
// manual position control via throttled REST updates at 20 Hz.
package transition
