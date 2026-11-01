// Package audio implements the server-side audio mixing engine.
//
// The [Mixer] decodes AAC audio from each source, applies per-channel
// processing and mixes to a stereo master bus. An external reference clock
// calls [Mixer.ProduceSamples] once per period for the program audio at a
// sample index of the media timeline; it reads per-channel ring buffers
// that the ingest path fills and stamps with media time, and the caller
// encodes the result. Output cadence is decoupled from source arrival.
//
// Per-channel processing pipeline (in order):
//   - Trim (-20 to +20 dB input gain)
//   - [EQ]: 3-band parametric equalizer (RBJ biquad filters)
//   - [Compressor]: Single-band dynamics with envelope follower
//   - Fader (channel level)
//   - Mix (sum to stereo master)
//   - Master fader
//   - [Limiter]: Brickwall limiter at -1 dBFS
//
// Key types:
//   - [Mixer]: Main mixer with per-channel decode/mix/encode
//   - [EQ]: 3-band parametric equalizer (Direct Form II Transposed)
//   - [Compressor]: Single-band compressor with makeup gain
//   - [Limiter]: Brickwall limiter preventing clipping
package audio
