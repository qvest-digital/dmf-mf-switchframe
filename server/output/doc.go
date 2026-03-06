// Package output implements the recording and streaming output engine.
//
// The [OutputManager] owns the lifecycle of all output adapters (file
// recording, SRT caller, SRT listener) and automatically registers a
// viewer on the program relay when the first output starts, removing it
// when the last output stops. This ensures zero CPU usage when no
// outputs are active.
//
// Key types:
//   - [OutputManager]: Lifecycle management, viewer registration, fan-out
//   - [FileRecorder]: MPEG-TS file recording with time/size-based rotation
//   - [SRTCaller]: Push-mode SRT output with reconnect and ring buffer
//   - [SRTListener]: Pull-mode SRT server accepting up to N connections
//   - [TSMuxer]: MPEG-TS muxer wrapping go-astits
//   - [ConfidenceMonitor]: 1fps JPEG thumbnail from program keyframes
//   - [AsyncAdapter]: Non-blocking write wrapper for output adapters
//
// Recording uses MPEG-TS format (.ts) for crash resilience (no moov atom
// required). SRT reconnection uses exponential backoff (1s-30s) with a
// 4 MB ring buffer, resuming from the nearest keyframe on overflow.
package output
