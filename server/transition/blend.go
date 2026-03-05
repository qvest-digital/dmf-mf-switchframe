package transition

// FrameBlender alpha-blends two YUV420 planar byte slices directly,
// avoiding the cost and chroma resampling error of a YUV→RGB→YUV round-trip.
// This matches how hardware broadcast mixers (ATEM, Ross, Datavideo) and
// FFmpeg's xfade filter operate: blending in the native Y'CbCr domain.
//
// Three blend modes are supported:
//   - Mix: linear interpolation between source A and source B
//   - Dip: two-phase blend through black (A fades out, then B fades in)
//   - FTB: fade to black (single source fades to black)
//
// The blender pre-allocates its output buffer at construction time and
// reuses it across frames. All blend methods return the internal yuvBufOut
// slice — callers must consume it before the next call.
//
// YUV420 layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
// Black in full-range YUV: Y=0, Cb=128, Cr=128
type FrameBlender struct {
	width, height int
	yuvBufOut     []byte
	ySize         int // w*h (luma plane size)
	uvSize        int // w/2 * h/2 (each chroma plane size)
}

// NewFrameBlender creates a FrameBlender with a pre-allocated output buffer
// sized for the given resolution in YUV420 format.
func NewFrameBlender(width, height int) *FrameBlender {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	return &FrameBlender{
		width:     width,
		height:    height,
		yuvBufOut: make([]byte, ySize+2*uvSize),
		ySize:     ySize,
		uvSize:    uvSize,
	}
}

// BlendMix performs linear interpolation between yuvA and yuvB in YUV420 space.
// position 0.0 = all A, position 1.0 = all B.
func (fb *FrameBlender) BlendMix(yuvA, yuvB []byte, position float64) []byte {
	invPos := 1.0 - position
	totalSize := fb.ySize + 2*fb.uvSize
	for i := 0; i < totalSize; i++ {
		fb.yuvBufOut[i] = clampByte(float64(yuvA[i])*invPos + float64(yuvB[i])*position)
	}
	return fb.yuvBufOut
}

// BlendDip performs a two-phase dip-to-black transition in YUV420 space.
// Phase 1 (position 0.0–0.5): source A fades to black.
// Phase 2 (position 0.5–1.0): source B fades up from black.
// At position 0.5, the output is fully black (Y=0, Cb=128, Cr=128).
func (fb *FrameBlender) BlendDip(yuvA, yuvB []byte, position float64) []byte {
	if position < 0.5 {
		// Phase 1: fade A to black. gain goes from 1.0 at pos=0 to 0.0 at pos=0.5
		gain := 1.0 - 2.0*position
		invGain := 1.0 - gain // how much black to mix in

		// Y plane: fade toward 0
		for i := 0; i < fb.ySize; i++ {
			fb.yuvBufOut[i] = clampByte(float64(yuvA[i]) * gain)
		}
		// Cb plane: fade toward 128
		cbOffset := fb.ySize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[cbOffset+i] = clampByte(float64(yuvA[cbOffset+i])*gain + 128.0*invGain)
		}
		// Cr plane: fade toward 128
		crOffset := fb.ySize + fb.uvSize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[crOffset+i] = clampByte(float64(yuvA[crOffset+i])*gain + 128.0*invGain)
		}
	} else {
		// Phase 2: fade B from black. gain goes from 0.0 at pos=0.5 to 1.0 at pos=1.0
		gain := 2.0*position - 1.0
		invGain := 1.0 - gain

		// Y plane: fade from 0
		for i := 0; i < fb.ySize; i++ {
			fb.yuvBufOut[i] = clampByte(float64(yuvB[i]) * gain)
		}
		// Cb plane: fade from 128
		cbOffset := fb.ySize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[cbOffset+i] = clampByte(float64(yuvB[cbOffset+i])*gain + 128.0*invGain)
		}
		// Cr plane: fade from 128
		crOffset := fb.ySize + fb.uvSize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[crOffset+i] = clampByte(float64(yuvB[crOffset+i])*gain + 128.0*invGain)
		}
	}
	return fb.yuvBufOut
}

// BlendFTB fades a single source to black in YUV420 space.
// position 0.0 = full source, position 1.0 = fully black (Y=0, Cb=128, Cr=128).
func (fb *FrameBlender) BlendFTB(yuvA []byte, position float64) []byte {
	gain := 1.0 - position
	invGain := position

	// Y plane: fade toward 0
	for i := 0; i < fb.ySize; i++ {
		fb.yuvBufOut[i] = clampByte(float64(yuvA[i]) * gain)
	}
	// Cb plane: fade toward 128
	cbOffset := fb.ySize
	for i := 0; i < fb.uvSize; i++ {
		fb.yuvBufOut[cbOffset+i] = clampByte(float64(yuvA[cbOffset+i])*gain + 128.0*invGain)
	}
	// Cr plane: fade toward 128
	crOffset := fb.ySize + fb.uvSize
	for i := 0; i < fb.uvSize; i++ {
		fb.yuvBufOut[crOffset+i] = clampByte(float64(yuvA[crOffset+i])*gain + 128.0*invGain)
	}
	return fb.yuvBufOut
}
