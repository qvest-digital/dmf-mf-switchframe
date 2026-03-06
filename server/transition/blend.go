package transition

// FrameBlender alpha-blends two YUV420 planar byte slices directly,
// avoiding the cost and chroma resampling error of a YUV→RGB→YUV round-trip.
// This matches how hardware broadcast mixers (ATEM, Ross, Datavideo) and
// FFmpeg's xfade filter operate: blending in the native Y'CbCr domain.
//
// Four blend modes are supported:
//   - Mix: linear interpolation between source A and source B
//   - Dip: two-phase blend through black (A fades out, then B fades in)
//   - Wipe: per-pixel threshold mask with 4px soft edge (6 directions)
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

// BlendWipe performs a directional wipe transition between yuvA and yuvB in
// YUV420 space. For each pixel, a threshold is computed based on its position
// and the wipe direction. Pixels whose threshold is below the transition
// position show source B; those above show source A. A 4-pixel soft edge
// provides a smooth gradient at the wipe boundary.
//
// Wipe directions:
//   - h-left: wipes from left to right (B reveals from the left)
//   - h-right: wipes from right to left (B reveals from the right)
//   - v-top: wipes from top to bottom (B reveals from the top)
//   - v-bottom: wipes from bottom to top (B reveals from the bottom)
//   - box-center-out: B reveals from center expanding outward
//   - box-edges-in: B reveals from edges contracting inward
func (fb *FrameBlender) BlendWipe(yuvA, yuvB []byte, position float64, direction WipeDirection) []byte {
	w := fb.width
	h := fb.height

	// Soft edge half-width in normalized coordinates.
	// 4 pixels total (2px on each side of boundary). Use the frame width
	// for horizontal wipes and height for vertical; for box wipes use the
	// larger dimension. The divisor is always the relevant axis dimension.
	softEdge := 2.0 / float64(w)
	switch direction {
	case WipeVTop, WipeVBottom:
		softEdge = 2.0 / float64(h)
	case WipeBoxCenterOut, WipeBoxEdgesIn:
		dim := w
		if h > w {
			dim = h
		}
		softEdge = 2.0 / float64(dim)
	}

	// Pre-compute center for box modes
	cx := float64(w-1) / 2.0
	cy := float64(h-1) / 2.0

	// --- Y plane (full resolution) ---
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			threshold := wipeThreshold(px, py, w, h, cx, cy, direction)
			alpha := wipeAlpha(threshold, position, softEdge)
			idx := py*w + px
			fb.yuvBufOut[idx] = clampByte(float64(yuvA[idx])*(1.0-alpha) + float64(yuvB[idx])*alpha)
		}
	}

	// --- Cb and Cr planes (half resolution) ---
	uvW := w / 2
	uvH := h / 2
	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize

	for py := 0; py < uvH; py++ {
		for px := 0; px < uvW; px++ {
			// Map chroma pixel back to luma coordinates for threshold
			threshold := wipeThreshold(px*2, py*2, w, h, cx, cy, direction)
			alpha := wipeAlpha(threshold, position, softEdge)
			idx := py*uvW + px
			fb.yuvBufOut[cbOffset+idx] = clampByte(float64(yuvA[cbOffset+idx])*(1.0-alpha) + float64(yuvB[cbOffset+idx])*alpha)
			fb.yuvBufOut[crOffset+idx] = clampByte(float64(yuvA[crOffset+idx])*(1.0-alpha) + float64(yuvB[crOffset+idx])*alpha)
		}
	}

	return fb.yuvBufOut
}

// wipeThreshold computes the normalized threshold [0,1] for a pixel at (px,py)
// in a frame of dimensions (w,h) with center (cx,cy) for the given direction.
func wipeThreshold(px, py, w, h int, cx, cy float64, direction WipeDirection) float64 {
	switch direction {
	case WipeHLeft:
		return float64(px) / float64(w)
	case WipeHRight:
		return 1.0 - float64(px)/float64(w)
	case WipeVTop:
		return float64(py) / float64(h)
	case WipeVBottom:
		return 1.0 - float64(py)/float64(h)
	case WipeBoxCenterOut:
		dx := float64(px) - cx
		dy := float64(py) - cy
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		tx := dx / cx
		ty := dy / cy
		if tx > ty {
			return tx
		}
		return ty
	case WipeBoxEdgesIn:
		dx := float64(px) - cx
		dy := float64(py) - cy
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		tx := dx / cx
		ty := dy / cy
		if tx > ty {
			return 1.0 - tx
		}
		return 1.0 - ty
	default:
		return float64(px) / float64(w) // fallback to h-left
	}
}

// wipeAlpha computes the blend alpha for a pixel given its threshold, the
// transition position, and the soft edge half-width. Returns 0.0 (fully A)
// to 1.0 (fully B) with a linear ramp in the soft edge region.
// At position 0.0 the result is always fully A; at position 1.0 always fully B.
func wipeAlpha(threshold, position, softEdge float64) float64 {
	// Guarantee clean edges at position extremes
	if position <= 0.0 {
		return 0.0
	}
	if position >= 1.0 {
		return 1.0
	}

	if threshold < position-softEdge {
		return 1.0 // fully B
	}
	if threshold > position+softEdge {
		return 0.0 // fully A
	}
	// Linear interpolation within soft edge
	return (position + softEdge - threshold) / (2.0 * softEdge)
}

// BlendStinger composites a stinger frame (with alpha) over a base YUV420 source.
// The stinger frame's YUV data is blended with the base using per-pixel alpha.
// alpha is a per-luma-pixel alpha map [0-255], same dimensions as the Y plane.
// stingerYUV is YUV420 planar format matching base dimensions.
func (fb *FrameBlender) BlendStinger(baseYUV []byte, stingerYUV []byte, alpha []byte) []byte {
	// Bounds check: all inputs must be large enough for the configured resolution.
	expected := fb.ySize + 2*fb.uvSize
	if len(baseYUV) < expected || len(stingerYUV) < expected || len(alpha) < fb.ySize {
		return nil
	}

	w := fb.width
	h := fb.height

	// --- Y plane: per-pixel alpha blend ---
	for i := 0; i < fb.ySize; i++ {
		a := float64(alpha[i]) / 255.0
		fb.yuvBufOut[i] = clampByte(float64(baseYUV[i])*(1.0-a) + float64(stingerYUV[i])*a)
	}

	// --- Cb and Cr planes: average alpha over corresponding 2x2 luma block ---
	uvW := w / 2
	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize

	for py := 0; py < h/2; py++ {
		for px := 0; px < uvW; px++ {
			// Average the alpha of the 4 luma pixels in this 2x2 block
			ly := py * 2
			lx := px * 2
			a00 := float64(alpha[ly*w+lx])
			a01 := float64(alpha[ly*w+lx+1])
			a10 := float64(alpha[(ly+1)*w+lx])
			a11 := float64(alpha[(ly+1)*w+lx+1])
			a := (a00 + a01 + a10 + a11) / (4.0 * 255.0)

			idx := py*uvW + px
			fb.yuvBufOut[cbOffset+idx] = clampByte(float64(baseYUV[cbOffset+idx])*(1.0-a) + float64(stingerYUV[cbOffset+idx])*a)
			fb.yuvBufOut[crOffset+idx] = clampByte(float64(baseYUV[crOffset+idx])*(1.0-a) + float64(stingerYUV[crOffset+idx])*a)
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
