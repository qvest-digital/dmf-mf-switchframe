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
	width, height      int
	yuvBufOut          []byte
	ySize              int    // w*h (luma plane size)
	uvSize             int    // w/2 * h/2 (each chroma plane size)
	blackY             byte   // Y value for black (0 = full-range, 16 = limited-range)
	wipeAlphaMap       []byte // precomputed per-pixel alpha for wipe transitions (w*h)
	wipeAlphaMapChroma []byte // subsampled chroma alpha map (w/2 * h/2)
	stingerChromaAlpha []byte // precomputed chroma-resolution alpha for stinger blending (w/2 * h/2)
}

// NewFrameBlender creates a FrameBlender with a pre-allocated output buffer
// sized for the given resolution in YUV420 format.
func NewFrameBlender(width, height int) *FrameBlender {
	ySize := width * height
	uvSize := (width / 2) * (height / 2)
	return &FrameBlender{
		width:              width,
		height:             height,
		yuvBufOut:          make([]byte, ySize+2*uvSize),
		ySize:              ySize,
		uvSize:             uvSize,
		blackY:             16, // BT.709 limited-range black
		wipeAlphaMap:       make([]byte, ySize),
		wipeAlphaMapChroma: make([]byte, uvSize),
		stingerChromaAlpha: make([]byte, uvSize),
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
	blendUniform(&fb.yuvBufOut[0], &yuvA[0], &yuvB[0], totalSize, pos, inv)
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
		blendFadeConst(&fb.yuvBufOut[0], &yuvA[0], fb.ySize, gain256, blackYi*invGain256)
		// Cb plane: fade toward 128
		cbOffset := fb.ySize
		blendFadeConst(&fb.yuvBufOut[cbOffset], &yuvA[cbOffset], fb.uvSize, gain256, 128*invGain256)
		// Cr plane: fade toward 128
		crOffset := fb.ySize + fb.uvSize
		blendFadeConst(&fb.yuvBufOut[crOffset], &yuvA[crOffset], fb.uvSize, gain256, 128*invGain256)
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
		blendFadeConst(&fb.yuvBufOut[0], &yuvB[0], fb.ySize, gain256, blackYi*invGain256)
		// Cb plane: fade from 128
		cbOffset := fb.ySize
		blendFadeConst(&fb.yuvBufOut[cbOffset], &yuvB[cbOffset], fb.uvSize, gain256, 128*invGain256)
		// Cr plane: fade from 128
		crOffset := fb.ySize + fb.uvSize
		blendFadeConst(&fb.yuvBufOut[crOffset], &yuvB[crOffset], fb.uvSize, gain256, 128*invGain256)
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
	// Generate luma and chroma alpha maps (byte values 0-255)
	fb.generateWipeAlpha(position, direction)

	// --- Y plane: blend using precomputed alpha map ---
	blendAlpha(&fb.yuvBufOut[0], &yuvA[0], &yuvB[0], &fb.wipeAlphaMap[0], fb.ySize)

	// --- Chroma planes: use chroma alpha computed directly by generateWipeAlpha ---
	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize
	blendAlpha(&fb.yuvBufOut[cbOffset], &yuvA[cbOffset], &yuvB[cbOffset], &fb.wipeAlphaMapChroma[0], fb.uvSize)
	blendAlpha(&fb.yuvBufOut[crOffset], &yuvA[crOffset], &yuvB[crOffset], &fb.wipeAlphaMapChroma[0], fb.uvSize)

	return fb.yuvBufOut
}

// generateWipeAlpha populates fb.wipeAlphaMap with per-pixel alpha values
// (0-255) for the given wipe position and direction.
//
// For linear wipes (H/V), alpha varies along only one axis:
//   - Horizontal: compute 1D alpha[x] array, fill each row by copying it
//   - Vertical: compute alpha per row, memset each row to that value
//
// Box wipes remain per-pixel (2D threshold function).
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
		fb.generateWipeAlphaHorizontal(position, softEdge, false)

	case WipeHRight:
		fb.generateWipeAlphaHorizontal(position, softEdge, true)

	case WipeVTop:
		fb.generateWipeAlphaVertical(position, softEdge, false)

	case WipeVBottom:
		fb.generateWipeAlphaVertical(position, softEdge, true)

	case WipeBoxCenterOut, WipeBoxEdgesIn:
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

		// Compute chroma alpha directly at half resolution
		chromaW := w / 2
		chromaH := h / 2
		chromaCx := float64(chromaW-1) / 2.0
		chromaCy := float64(chromaH-1) / 2.0
		chromaInvCx := 1.0 / chromaCx
		chromaInvCy := 1.0 / chromaCy

		for py := 0; py < chromaH; py++ {
			dy := float64(py) - chromaCy
			if dy < 0 {
				dy = -dy
			}
			ty := dy * chromaInvCy

			row := py * chromaW
			for px := 0; px < chromaW; px++ {
				dx := float64(px) - chromaCx
				if dx < 0 {
					dx = -dx
				}
				tx := dx * chromaInvCx

				threshold := tx
				if ty > tx {
					threshold = ty
				}
				if invert {
					threshold = 1.0 - threshold
				}
				fb.wipeAlphaMapChroma[row+px] = wipeAlphaByte(threshold, position, softEdge)
			}
		}

	default:
		fb.generateWipeAlphaHorizontal(position, softEdge, false)
	}
}

// generateWipeAlphaHorizontal computes a 1D alpha array along the X axis,
// then replicates it across all rows. O(w + w*h_copy) instead of O(w*h).
// If invert is true, threshold is mirrored (right-to-left).
func (fb *FrameBlender) generateWipeAlphaHorizontal(position, softEdge float64, invert bool) {
	w := fb.width
	h := fb.height

	// Compute 1D alpha for the first row (used as template).
	row0 := fb.wipeAlphaMap[:w]
	invW := 1.0 // guard for w <= 1
	if w > 1 {
		invW = 1.0 / float64(w-1)
	}
	for px := 0; px < w; px++ {
		threshold := float64(px) * invW
		if invert {
			threshold = 1.0 - threshold
		}
		row0[px] = wipeAlphaByte(threshold, position, softEdge)
	}

	// Copy row 0 to all subsequent rows.
	for py := 1; py < h; py++ {
		copy(fb.wipeAlphaMap[py*w:py*w+w], row0)
	}

	// Compute chroma alpha directly at half resolution (1D, replicate rows).
	chromaW := w / 2
	chromaH := h / 2
	chromaRow0 := fb.wipeAlphaMapChroma[:chromaW]
	invCW := invW // use same denominator as luma for matching thresholds
	for cx := 0; cx < chromaW; cx++ {
		threshold := float64(cx*2) * invCW
		if invert {
			threshold = 1.0 - threshold
		}
		chromaRow0[cx] = wipeAlphaByte(threshold, position, softEdge)
	}
	for cy := 1; cy < chromaH; cy++ {
		copy(fb.wipeAlphaMapChroma[cy*chromaW:cy*chromaW+chromaW], chromaRow0)
	}
}

// generateWipeAlphaVertical computes one alpha value per row and fills
// each row with that constant value. O(h + w*h_memset) instead of O(w*h).
// If invert is true, threshold is mirrored (bottom-to-top).
func (fb *FrameBlender) generateWipeAlphaVertical(position, softEdge float64, invert bool) {
	w := fb.width
	h := fb.height
	invH := 1.0 // guard for h <= 1
	if h > 1 {
		invH = 1.0 / float64(h-1)
	}

	for py := 0; py < h; py++ {
		threshold := float64(py) * invH
		if invert {
			threshold = 1.0 - threshold
		}
		a := wipeAlphaByte(threshold, position, softEdge)
		row := fb.wipeAlphaMap[py*w : py*w+w]
		for i := range row {
			row[i] = a
		}
	}

	// Compute chroma alpha directly at half resolution (constant per row).
	chromaW := w / 2
	chromaH := h / 2
	invCH := invH // use same denominator as luma for matching thresholds
	for cy := 0; cy < chromaH; cy++ {
		threshold := float64(cy*2) * invCH
		if invert {
			threshold = 1.0 - threshold
		}
		a := wipeAlphaByte(threshold, position, softEdge)
		row := fb.wipeAlphaMapChroma[cy*chromaW : cy*chromaW+chromaW]
		for i := range row {
			row[i] = a
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

// downsampleAlphaToChroma converts a full-resolution alpha map to chroma resolution
// by averaging each 2x2 block. Writes into dst which must be (w/2)*(h/2) in size.
// Uses the SIMD-accelerated downsampleAlpha2x2 kernel per row pair.
func downsampleAlphaToChroma(alpha []byte, w, h int, dst []byte) {
	chromaW := w / 2
	chromaH := h / 2
	for cy := 0; cy < chromaH; cy++ {
		ly := cy * 2
		row0 := alpha[ly*w:]
		row1 := alpha[(ly+1)*w:]
		downsampleAlpha2x2(&dst[cy*chromaW], &row0[0], &row1[0], chromaW)
	}
}

// BlendStinger composites a stinger frame (with alpha) over a base YUV420 source.
// The stinger frame's YUV data is blended with the base using per-pixel alpha.
// alpha is a per-luma-pixel alpha map [0-255], same dimensions as the Y plane.
// stingerYUV is YUV420 planar format matching base dimensions.
// Uses integer math with >>8 shift (GPU-standard approximation for /255).
func (fb *FrameBlender) BlendStinger(baseYUV []byte, stingerYUV []byte, alpha []byte) []byte {
	expected := fb.ySize + 2*fb.uvSize
	if len(baseYUV) < expected || len(stingerYUV) < expected || len(alpha) < fb.ySize {
		return nil
	}

	// --- Y plane: per-pixel alpha blend ---
	blendAlpha(&fb.yuvBufOut[0], &baseYUV[0], &stingerYUV[0], &alpha[0], fb.ySize)

	// --- Chroma planes: downsample alpha to chroma resolution, then use SIMD kernel ---
	downsampleAlphaToChroma(alpha, fb.width, fb.height, fb.stingerChromaAlpha)

	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize
	blendAlpha(&fb.yuvBufOut[cbOffset], &baseYUV[cbOffset], &stingerYUV[cbOffset], &fb.stingerChromaAlpha[0], fb.uvSize)
	blendAlpha(&fb.yuvBufOut[crOffset], &baseYUV[crOffset], &stingerYUV[crOffset], &fb.stingerChromaAlpha[0], fb.uvSize)

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
	blendFadeConst(&fb.yuvBufOut[0], &yuvA[0], fb.ySize, gain256, blackYi*invGain256)
	// Cb plane: fade toward 128
	cbOffset := fb.ySize
	blendFadeConst(&fb.yuvBufOut[cbOffset], &yuvA[cbOffset], fb.uvSize, gain256, 128*invGain256)
	// Cr plane: fade toward 128
	crOffset := fb.ySize + fb.uvSize
	blendFadeConst(&fb.yuvBufOut[crOffset], &yuvA[crOffset], fb.uvSize, gain256, 128*invGain256)
	return fb.yuvBufOut
}
