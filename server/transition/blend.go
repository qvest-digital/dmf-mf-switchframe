package transition

// FrameBlender alpha-blends two interleaved RGB byte slices.
// Three blend modes are supported:
//   - Mix: linear interpolation between source A and source B
//   - Dip: two-phase blend through black (A fades out, then B fades in)
//   - FTB: fade to black (single source fades to black)
//
// The blender pre-allocates its output buffer at construction time and
// reuses it across frames. All blend methods return the internal rgbBufOut
// slice — callers must consume it before the next call.
type FrameBlender struct {
	width, height int
	rgbBufOut     []byte
}

// NewFrameBlender creates a FrameBlender with a pre-allocated output buffer
// sized for the given resolution.
func NewFrameBlender(width, height int) *FrameBlender {
	return &FrameBlender{
		width:     width,
		height:    height,
		rgbBufOut: make([]byte, width*height*3),
	}
}

// BlendMix performs linear interpolation between rgbA and rgbB.
// position 0.0 = all A, position 1.0 = all B.
func (fb *FrameBlender) BlendMix(rgbA, rgbB []byte, position float64) []byte {
	invPos := 1.0 - position
	for i := 0; i < len(fb.rgbBufOut); i++ {
		v := float64(rgbA[i])*invPos + float64(rgbB[i])*position
		fb.rgbBufOut[i] = clampByte(v)
	}
	return fb.rgbBufOut
}

// BlendDip performs a two-phase dip-to-black transition.
// Phase 1 (position 0.0–0.5): source A fades to black.
// Phase 2 (position 0.5–1.0): source B fades up from black.
// At position 0.5, the output is fully black.
func (fb *FrameBlender) BlendDip(rgbA, rgbB []byte, position float64) []byte {
	if position < 0.5 {
		// Phase 1: fade A to black. gain goes from 1.0 at pos=0 to 0.0 at pos=0.5
		gain := 1.0 - 2.0*position
		for i := 0; i < len(fb.rgbBufOut); i++ {
			fb.rgbBufOut[i] = clampByte(float64(rgbA[i]) * gain)
		}
	} else {
		// Phase 2: fade B from black. gain goes from 0.0 at pos=0.5 to 1.0 at pos=1.0
		gain := 2.0*position - 1.0
		for i := 0; i < len(fb.rgbBufOut); i++ {
			fb.rgbBufOut[i] = clampByte(float64(rgbB[i]) * gain)
		}
	}
	return fb.rgbBufOut
}

// BlendFTB fades a single source to black.
// position 0.0 = full source, position 1.0 = fully black.
func (fb *FrameBlender) BlendFTB(rgbA []byte, position float64) []byte {
	gain := 1.0 - position
	for i := 0; i < len(fb.rgbBufOut); i++ {
		fb.rgbBufOut[i] = clampByte(float64(rgbA[i]) * gain)
	}
	return fb.rgbBufOut
}
