// Package audio implements the server-side audio mixing engine.
//
// The [Mixer] decodes AAC audio from each source, applies per-channel
// processing, mixes to a stereo master bus, and re-encodes to AAC for the
// program output. A passthrough optimization bypasses decode/mix/encode
// entirely when only one source is active at 0 dB with EQ and compressor
// bypassed, achieving zero CPU usage in the common single-camera case.
//
// Per-channel processing pipeline (in order):
//   - Trim (-20 to +20 dB input gain)
//   - [EQ]: 3-band parametric equalizer (RBJ biquad filters)
//   - [Compressor]: Single-band dynamics with envelope follower
//   - Fader (channel level)
//   - Mix (sum to stereo master)
//   - Master fader
//   - [Limiter]: Brickwall limiter at -1 dBFS
//   - Encode (AAC output)
//
// Key types:
//   - [Mixer]: Main mixer with per-channel decode/mix/encode
//   - [EQ]: 3-band parametric equalizer (Direct Form II Transposed)
//   - [Compressor]: Single-band compressor with makeup gain
//   - [Limiter]: Brickwall limiter preventing clipping
package audio
