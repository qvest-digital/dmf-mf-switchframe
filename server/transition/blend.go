package transition

// FrameBlender alpha-blends two YUV420 planar byte slices directly,
// avoiding the cost and chroma resampling error of a YUV->RGB->YUV round-trip.
// This matches how hardware broadcast mixers (ATEM, Ross, Datavideo) and
// FFmpeg's xfade filter operate: blending in the native Y'CbCr domain.
//
// All blend loops use fixed-point integer arithmetic (0-256 weight with >>8
// shift) instead of float64, eliminating per-pixel float conversions.
// The result of blending two [0,255] values with a [0,256] weight always
// fits in [0,65280], so no clamping is needed after the shift.
//
// Five blend modes are supported:
//   - Mix: linear interpolation between source A and source B
//   - Dip: two-phase blend through black (A fades out, then B fades in)
//   - Wipe: precomputed alpha map with 4px soft edge (6 directions)
//   - FTB: fade to black (single source fades to black)
//   - Stinger: per-pixel alpha composite from stinger overlay
//
// The blender pre-allocates its output buffer at construction time and
// reuses it across frames. All blend methods return the internal yuvBufOut
// slice -- callers must consume it before the next call.
//
// YUV420 layout: Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2]
// Black in full-range YUV: Y=0, Cb=128, Cr=128
type FrameBlender struct {
	width, height int
	yuvBufOut     []byte
	ySize         int  // w*h (luma plane size)
	uvSize        int  // w/2 * h/2 (each chroma plane size)
	blackY        byte // Y value for black (0 = full-range, 16 = limited-range)
	wipeAlphaMap  []byte // precomputed per-pixel alpha for wipe transitions
}

// NewFrameBlender creates a FrameBlender with a pre-allocated output buffer
// sized for the given resolution in YUV420 format.
func NewFrameBlender(width, height int) *FrameBlender {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	return &FrameBlender{
		width:        width,
		height:       height,
		yuvBufOut:    make([]byte, ySize+2*uvSize),
		ySize:        ySize,
		uvSize:       uvSize,
		blackY:       16, // BT.709 limited-range black
		wipeAlphaMap: make([]byte, ySize),
	}
}

// SetLimitedRange configures the blender for limited-range (broadcast) or
// full-range YUV. Limited-range uses Y=16 for black; full-range uses Y=0.
// The default is limited-range (Y=16) to match BT.709 broadcast standard.
func (fb *FrameBlender) SetLimitedRange(limited bool) {
	if limited {
		fb.blackY = 16
	} else {
		fb.blackY = 0
	}
}

// BlendMix performs linear interpolation between yuvA and yuvB in YUV420 space.
// position 0.0 = all A, position 1.0 = all B.
// Uses fixed-point integer math: weight 0-256 with >>8 division.
func (fb *FrameBlender) BlendMix(yuvA, yuvB []byte, position float64) []byte {
	pos := int(position*256 + 0.5)
	if pos < 0 {
		pos = 0
	}
	if pos > 256 {
		pos = 256
	}
	inv := 256 - pos
	totalSize := fb.ySize + 2*fb.uvSize
	for i := 0; i < totalSize; i++ {
		fb.yuvBufOut[i] = byte((int(yuvA[i])*inv + int(yuvB[i])*pos) >> 8)
	}
	return fb.yuvBufOut
}

// BlendDip performs a two-phase dip-to-black transition in YUV420 space.
// Phase 1 (position 0.0-0.5): source A fades to black.
// Phase 2 (position 0.5-1.0): source B fades up from black.
// At position 0.5, the output is fully black.
// Black level depends on SetLimitedRange: Y=0 (full-range) or Y=16 (limited-range).
// Uses fixed-point integer math: weight 0-256 with >>8 division.
func (fb *FrameBlender) BlendDip(yuvA, yuvB []byte, position float64) []byte {
	blackYi := int(fb.blackY)

	if position < 0.5 {
		// Phase 1: fade A to black. gain goes from 1.0 at pos=0 to 0.0 at pos=0.5
		gainF := 1.0 - 2.0*position
		gain256 := int(gainF*256 + 0.5)
		if gain256 < 0 {
			gain256 = 0
		}
		if gain256 > 256 {
			gain256 = 256
		}
		invGain256 := 256 - gain256

		// Y plane: fade toward blackY
		for i := 0; i < fb.ySize; i++ {
			fb.yuvBufOut[i] = byte((int(yuvA[i])*gain256 + blackYi*invGain256) >> 8)
		}
		// Cb plane: fade toward 128
		cbOffset := fb.ySize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[cbOffset+i] = byte((int(yuvA[cbOffset+i])*gain256 + 128*invGain256) >> 8)
		}
		// Cr plane: fade toward 128
		crOffset := fb.ySize + fb.uvSize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[crOffset+i] = byte((int(yuvA[crOffset+i])*gain256 + 128*invGain256) >> 8)
		}
	} else {
		// Phase 2: fade B from black. gain goes from 0.0 at pos=0.5 to 1.0 at pos=1.0
		gainF := 2.0*position - 1.0
		gain256 := int(gainF*256 + 0.5)
		if gain256 < 0 {
			gain256 = 0
		}
		if gain256 > 256 {
			gain256 = 256
		}
		invGain256 := 256 - gain256

		// Y plane: fade from blackY
		for i := 0; i < fb.ySize; i++ {
			fb.yuvBufOut[i] = byte((int(yuvB[i])*gain256 + blackYi*invGain256) >> 8)
		}
		// Cb plane: fade from 128
		cbOffset := fb.ySize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[cbOffset+i] = byte((int(yuvB[cbOffset+i])*gain256 + 128*invGain256) >> 8)
		}
		// Cr plane: fade from 128
		crOffset := fb.ySize + fb.uvSize
		for i := 0; i < fb.uvSize; i++ {
			fb.yuvBufOut[crOffset+i] = byte((int(yuvB[crOffset+i])*gain256 + 128*invGain256) >> 8)
		}
	}
	return fb.yuvBufOut
}

// BlendWipe performs a directional wipe transition between yuvA and yuvB in
// YUV420 space. A precomputed alpha map is generated once per call at the
// Y-plane resolution, then the fast integer blend loop applies it.
// For linear wipes, alpha is constant along the perpendicular axis, so only
// one value per row/column is computed and replicated.
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

	// Generate the alpha map (byte values 0-255) for the Y plane
	fb.generateWipeAlpha(position, direction)

	// --- Y plane: blend using precomputed alpha map ---
	// Alpha map values are 0-255. Convert to 0-256 weight for exact pass-through
	// at both extremes: a + (a >> 7) maps 0->0, 255->256.
	for i := 0; i < fb.ySize; i++ {
		a := int(fb.wipeAlphaMap[i])
		w256 := a + (a >> 7) // 0-255 -> 0-256
		inv := 256 - w256
		fb.yuvBufOut[i] = byte((int(yuvA[i])*inv + int(yuvB[i])*w256) >> 8)
	}

	// --- Cb and Cr planes: subsample alpha by reading every other row/column ---
	uvW := w / 2
	uvH := h / 2
	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize

	for py := 0; py < uvH; py++ {
		for px := 0; px < uvW; px++ {
			// Use the alpha from the corresponding top-left luma pixel
			a := int(fb.wipeAlphaMap[py*2*w+px*2])
			w256 := a + (a >> 7)
			inv := 256 - w256
			idx := py*uvW + px
			fb.yuvBufOut[cbOffset+idx] = byte((int(yuvA[cbOffset+idx])*inv + int(yuvB[cbOffset+idx])*w256) >> 8)
			fb.yuvBufOut[crOffset+idx] = byte((int(yuvA[crOffset+idx])*inv + int(yuvB[crOffset+idx])*w256) >> 8)
		}
	}

	return fb.yuvBufOut
}

// generateWipeAlpha populates fb.wipeAlphaMap with per-pixel alpha values
// (0-255) for the given wipe position and direction. For linear wipes,
// alpha is constant along the perpendicular axis, so only one value per
// row or column is computed and filled across the entire line.
func (fb *FrameBlender) generateWipeAlpha(position float64, direction WipeDirection) {
	w := fb.width
	h := fb.height

	// Soft edge half-width in normalized coordinates.
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

	switch direction {
	case WipeHLeft:
		// Threshold = px/w, constant per column
		invW := 1.0 / float64(w)
		for px := 0; px < w; px++ {
			threshold := float64(px) * invW
			a := wipeAlphaByte(threshold, position, softEdge)
			// Fill this column for all rows
			for py := 0; py < h; py++ {
				fb.wipeAlphaMap[py*w+px] = a
			}
		}

	case WipeHRight:
		// Threshold = 1 - px/w, constant per column
		invW := 1.0 / float64(w)
		for px := 0; px < w; px++ {
			threshold := 1.0 - float64(px)*invW
			a := wipeAlphaByte(threshold, position, softEdge)
			for py := 0; py < h; py++ {
				fb.wipeAlphaMap[py*w+px] = a
			}
		}

	case WipeVTop:
		// Threshold = py/h, constant per row
		invH := 1.0 / float64(h)
		for py := 0; py < h; py++ {
			threshold := float64(py) * invH
			a := wipeAlphaByte(threshold, position, softEdge)
			row := py * w
			for px := 0; px < w; px++ {
				fb.wipeAlphaMap[row+px] = a
			}
		}

	case WipeVBottom:
		// Threshold = 1 - py/h, constant per row
		invH := 1.0 / float64(h)
		for py := 0; py < h; py++ {
			threshold := 1.0 - float64(py)*invH
			a := wipeAlphaByte(threshold, position, softEdge)
			row := py * w
			for px := 0; px < w; px++ {
				fb.wipeAlphaMap[row+px] = a
			}
		}

	case WipeBoxCenterOut, WipeBoxEdgesIn:
		// Per-pixel: threshold = max(|px-cx|/cx, |py-cy|/cy) for center-out,
		// or 1 - that for edges-in.
		cx := float64(w-1) / 2.0
		cy := float64(h-1) / 2.0
		invCx := 1.0 / cx
		invCy := 1.0 / cy
		invert := direction == WipeBoxEdgesIn

		for py := 0; py < h; py++ {
			dy := float64(py) - cy
			if dy < 0 {
				dy = -dy
			}
			ty := dy * invCy

			row := py * w
			for px := 0; px < w; px++ {
				dx := float64(px) - cx
				if dx < 0 {
					dx = -dx
				}
				tx := dx * invCx

				threshold := tx
				if ty > tx {
					threshold = ty
				}
				if invert {
					threshold = 1.0 - threshold
				}
				fb.wipeAlphaMap[row+px] = wipeAlphaByte(threshold, position, softEdge)
			}
		}

	default:
		// Fallback to h-left
		invW := 1.0 / float64(w)
		for px := 0; px < w; px++ {
			threshold := float64(px) * invW
			a := wipeAlphaByte(threshold, position, softEdge)
			for py := 0; py < h; py++ {
				fb.wipeAlphaMap[py*w+px] = a
			}
		}
	}
}

// wipeAlphaByte computes the blend alpha as a byte [0-255] for a pixel
// given its threshold, the transition position, and the soft edge half-width.
// 0 = fully A, 255 = fully B.
func wipeAlphaByte(threshold, position, softEdge float64) byte {
	if position <= 0.0 {
		return 0
	}
	if position >= 1.0 {
		return 255
	}
	if threshold < position-softEdge {
		return 255 // fully B
	}
	if threshold > position+softEdge {
		return 0 // fully A
	}
	// Linear interpolation within soft edge, scaled to 0-255
	alpha := (position + softEdge - threshold) / (2.0 * softEdge)
	a := int(alpha*255 + 0.5)
	if a < 0 {
		a = 0
	}
	if a > 255 {
		a = 255
	}
	return byte(a)
}

// BlendStinger composites a stinger frame (with alpha) over a base YUV420 source.
// The stinger frame's YUV data is blended with the base using per-pixel alpha.
// alpha is a per-luma-pixel alpha map [0-255], same dimensions as the Y plane.
// stingerYUV is YUV420 planar format matching base dimensions.
// Uses integer math with >>8 shift (GPU-standard approximation for /255).
func (fb *FrameBlender) BlendStinger(baseYUV []byte, stingerYUV []byte, alpha []byte) []byte {
	// Bounds check: all inputs must be large enough for the configured resolution.
	expected := fb.ySize + 2*fb.uvSize
	if len(baseYUV) < expected || len(stingerYUV) < expected || len(alpha) < fb.ySize {
		return nil
	}

	w := fb.width
	h := fb.height

	// --- Y plane: per-pixel alpha blend ---
	// Alpha values are 0-255. Convert to 0-256 weight for exact pass-through
	// at both extremes: a + (a >> 7) maps 0->0, 255->256.
	for i := 0; i < fb.ySize; i++ {
		a := int(alpha[i])
		w256 := a + (a >> 7) // 0-255 -> 0-256
		inv := 256 - w256
		fb.yuvBufOut[i] = byte((int(baseYUV[i])*inv + int(stingerYUV[i])*w256) >> 8)
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
			a := (int(alpha[ly*w+lx]) + int(alpha[ly*w+lx+1]) + int(alpha[(ly+1)*w+lx]) + int(alpha[(ly+1)*w+lx+1])) / 4
			w256 := a + (a >> 7) // 0-255 -> 0-256
			inv := 256 - w256

			idx := py*uvW + px
			fb.yuvBufOut[cbOffset+idx] = byte((int(baseYUV[cbOffset+idx])*inv + int(stingerYUV[cbOffset+idx])*w256) >> 8)
			fb.yuvBufOut[crOffset+idx] = byte((int(baseYUV[crOffset+idx])*inv + int(stingerYUV[crOffset+idx])*w256) >> 8)
		}
	}

	return fb.yuvBufOut
}

// BlendFTB fades a single source to black in YUV420 space.
// position 0.0 = full source, position 1.0 = fully black.
// Black level depends on SetLimitedRange: Y=0 (full-range) or Y=16 (limited-range).
// Chroma neutral is 128 in both ranges.
// Uses fixed-point integer math: weight 0-256 with >>8 division.
func (fb *FrameBlender) BlendFTB(yuvA []byte, position float64) []byte {
	gain256 := int((1.0-position)*256 + 0.5)
	if gain256 < 0 {
		gain256 = 0
	}
	if gain256 > 256 {
		gain256 = 256
	}
	invGain256 := 256 - gain256
	blackYi := int(fb.blackY)

	// Y plane: fade toward blackY
	for i := 0; i < fb.ySize; i++ {
		fb.yuvBufOut[i] = byte((int(yuvA[i])*gain256 + blackYi*invGain256) >> 8)
	}
	// Cb plane: fade toward 128
	cbOffset := fb.ySize
	for i := 0; i < fb.uvSize; i++ {
		fb.yuvBufOut[cbOffset+i] = byte((int(yuvA[cbOffset+i])*gain256 + 128*invGain256) >> 8)
	}
	// Cr plane: fade toward 128
	crOffset := fb.ySize + fb.uvSize
	for i := 0; i < fb.uvSize; i++ {
		fb.yuvBufOut[crOffset+i] = byte((int(yuvA[crOffset+i])*gain256 + 128*invGain256) >> 8)
	}
	return fb.yuvBufOut
}
