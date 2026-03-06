// Package codec provides shared video and audio codec infrastructure for
// Switchframe's server-side pipeline.
//
// It includes AVC1/Annex B NALU conversion, ADTS header construction and
// parsing, and unified encoder/decoder factories that auto-detect the best
// available backend at startup (NVENC, VA-API, VideoToolbox, libx264, or
// OpenH264 fallback).
//
// Key functions:
//   - [AVC1ToAnnexB], [AnnexBToAVC1]: NALU format conversion
//   - [BuildADTS], [IsADTS], [SplitADTSFrames]: ADTS header helpers
//   - [ProbeEncoders]: Startup auto-detection of available video encoders
//   - [NewVideoEncoder], [NewVideoDecoder]: Unified codec factories
//
// Build tags control codec availability:
//   - cgo && !noffmpeg: FFmpeg libavcodec (primary backend)
//   - cgo && openh264: OpenH264 fallback encoder/decoder
//   - Non-cgo builds: stub implementations that return errors
package codec
