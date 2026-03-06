// Package graphics provides downstream keyer (DSK) compositing and
// upstream chroma/luma keying for the Switchframe video switcher.
//
// The [Compositor] overlays browser-rendered RGBA graphics onto the
// program video output using [AlphaBlendRGBA], which composites in
// YUV420 space with BT.709 coefficients. The fast path skips fully
// transparent pixels, which is the common case for lower-third graphics.
//
// Upstream keying is handled by [KeyProcessor], which applies per-source
// key chains (chroma or luma) before the DSK compositing stage. Chroma
// keying uses Cb/Cr distance in YUV420 space; luma keying uses Y
// threshold with smoothness feathering.
//
// Key types:
//   - [Compositor]: DSK overlay lifecycle (on/off, auto fade, frame processing)
//   - [KeyProcessor]: Per-source upstream key chain management
//   - [KeyConfig]: Chroma/luma key parameters (key color, tolerance, smoothness)
//   - [YCbCr]: Color representation in YCbCr space for key configuration
package graphics
