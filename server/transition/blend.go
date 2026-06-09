package transition

import (
	"errors"
	"fmt"

	"github.com/zsiec/switchframe/server/transition/wipemap"
)

// errOddDimensions is returned when YUV420 operations receive odd width or height.
// YUV420 chroma subsampling requires even dimensions; odd values would cause
// incorrect buffer sizes and plane offsets throughout the pipeline.
var errOddDimensions = errors.New("YUV420 requires even dimensions")

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
// Returns an error if width or height is odd (YUV420 requires even dimensions).
func NewFrameBlender(width, height int) (*FrameBlender, error) {
	if width%2 != 0 || height%2 != 0 {
		return nil, fmt.Errorf("%w: got %dx%d", errOddDimensions, width, height)
	}
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
	}, nil
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

// BlendWipeMap performs a gradient-map-based wipe transition between yuvA and yuvB.
// The gradientMap is a single-channel byte buffer (w*h) where 0 = revealed first
// and 255 = revealed last. The config controls soft edge, border, reverse, and multiply.
//
// Approach:
//  1. Convert gradient map → per-pixel alpha map (applying reverse, multiply, soft edge)
//  2. Handle border pixels with a second pass
//  3. Use existing SIMD blendAlpha for Y plane
//  4. Downsample alpha to chroma, blendAlpha for Cb/Cr planes
//  5. Overwrite border pixels with border color
func (fb *FrameBlender) BlendWipeMap(yuvA, yuvB []byte, position float64, gradientMap []byte, config wipemap.WipeConfig) []byte {
	w := fb.width
	h := fb.height

	// Clamp position.
	if position <= 0.0 {
		copy(fb.yuvBufOut, yuvA[:fb.ySize+2*fb.uvSize])
		return fb.yuvBufOut
	}
	if position >= 1.0 {
		copy(fb.yuvBufOut, yuvB[:fb.ySize+2*fb.uvSize])
		return fb.yuvBufOut
	}

	borderWidth := config.BorderWidth

	// Sanitize the gradient repeat count once: the raw config.Multiply is decoded
	// from API/CEF JSON, and an unclamped value can overflow int(g)*Multiply or
	// (when a multiple of 256) collapse the gradient to a degenerate hard cut.
	mul := config.Multiplier()

	// Step 1: Convert gradient map to alpha map.
	// Use integer-domain computation scaled to 16.16 fixed-point to avoid
	// float64 quantization errors when mapping gradient bytes through the
	// soft-edge threshold function.
	softEdge := config.SoftEdge

	// Scale to 16.16 fixed-point: multiply by 65536.
	// position and softEdge are in [0,1], gradient values in [0,255].
	// To compare: scale gradient to [0, 255*65536] and position to same range.
	pos16 := int(position * 255 * 65536)
	se16 := int(softEdge * 255 * 65536)
	if se16 < 1 {
		se16 = 1
	}
	// 2 * softEdge * 65536 (denominator for linear interpolation)
	twoSE16 := 2 * se16

	for i := 0; i < fb.ySize; i++ {
		g := gradientMap[i]
		if config.Reverse {
			g = 255 - g
		}
		if mul > 1 {
			g = byte((int(g) * mul) % 256)
		}
		// Scale gradient to 16.16 fixed-point (same domain as pos16).
		g16 := int(g) * 65536

		dist := g16 - pos16 // signed distance from threshold

		var alpha byte
		if dist < -se16 {
			alpha = 255 // fully B
		} else if dist > se16 {
			alpha = 0 // fully A
		} else {
			// Linear interpolation: alpha = (se16 - dist) / (2 * se16) * 255
			// Avoid overflow: se16 ≤ 255*65536 ≈ 16.7M, (se16-dist) ≤ 2*se16 ≈ 33.5M
			// (se16 - dist) * 255 ≤ 33.5M * 255 ≈ 8.5G → fits int64 but not int32.
			// Use int64 for the multiplication.
			num := int64(se16-dist) * 255
			den := int64(twoSE16)
			a := int((num + den/2) / den)
			if a < 0 {
				a = 0
			}
			if a > 255 {
				a = 255
			}
			alpha = byte(a)
		}
		fb.wipeAlphaMap[i] = alpha
	}

	// Step 2: Use SIMD blendAlpha for Y plane.
	// Note: both CPU (blendAlpha SIMD) and GPU (wipe_map_blend kernel) use >>8
	// (divide by 256) instead of /255. Max error is 1 LSB. This matches industry
	// standard fixed-point blending used by ATEM, Ross, and other broadcast hardware.
	blendAlpha(&fb.yuvBufOut[0], &yuvA[0], &yuvB[0], &fb.wipeAlphaMap[0], fb.ySize)

	// Step 3: Downsample alpha to chroma resolution, blend Cb/Cr planes.
	downsampleAlphaToChroma(fb.wipeAlphaMap, w, h, fb.wipeAlphaMapChroma)

	cbOffset := fb.ySize
	crOffset := fb.ySize + fb.uvSize
	blendAlpha(&fb.yuvBufOut[cbOffset], &yuvA[cbOffset], &yuvB[cbOffset], &fb.wipeAlphaMapChroma[0], fb.uvSize)
	blendAlpha(&fb.yuvBufOut[crOffset], &yuvA[crOffset], &yuvB[crOffset], &fb.wipeAlphaMapChroma[0], fb.uvSize)

	// Step 4: Border overlay (second pass on Y and chroma planes).
	if borderWidth > 0 {
		borderHalf := int(borderWidth * 255)
		if borderHalf < 1 {
			borderHalf = 1
		}
		threshold256 := int(position * 255)

		// Y plane border pass.
		for i := 0; i < fb.ySize; i++ {
			g := gradientMap[i]
			if config.Reverse {
				g = 255 - g
			}
			if mul > 1 {
				g = byte((int(g) * mul) % 256)
			}
			dist := int(g) - threshold256
			if dist < 0 {
				dist = -dist
			}
			if dist < borderHalf {
				fb.yuvBufOut[i] = config.BorderColorY
			}
		}

		// Chroma plane border pass: recompute the gradient-based border check
		// at chroma resolution directly, instead of comparing Y values which
		// can false-positive when video content matches the border color.
		chromaW := w / 2
		chromaH := h / 2
		for cy := 0; cy < chromaH; cy++ {
			ly := cy * 2
			for cx := 0; cx < chromaW; cx++ {
				lx := cx * 2
				// Check if any of the 4 luma pixels in this 2x2 block are border.
				isBorder := false
				for _, off := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					px := lx + off[0]
					py := ly + off[1]
					g := gradientMap[py*w+px]
					if config.Reverse {
						g = 255 - g
					}
					if mul > 1 {
						g = byte((int(g) * mul) % 256)
					}
					dist := int(g) - threshold256
					if dist < 0 {
						dist = -dist
					}
					if dist < borderHalf {
						isBorder = true
						break
					}
				}
				if isBorder {
					ci := cy*chromaW + cx
					fb.yuvBufOut[cbOffset+ci] = config.BorderColorCb
					fb.yuvBufOut[crOffset+ci] = config.BorderColorCr
				}
			}
		}
	}

	return fb.yuvBufOut
}

// generateWipeMapAlpha fills alphaOut with per-pixel alpha values derived from
// a gradient map and WipeConfig. This is the shared logic used by both 8-bit
// BlendWipeMap and the 10-bit wipe path (which needs only the alpha map).
// alphaOut must be len(gradientMap) bytes.
func generateWipeMapAlpha(alphaOut, gradientMap []byte, position float64, config wipemap.WipeConfig) {
	n := len(alphaOut)
	if position <= 0.0 {
		for i := 0; i < n; i++ {
			alphaOut[i] = 0
		}
		return
	}
	if position >= 1.0 {
		for i := 0; i < n; i++ {
			alphaOut[i] = 255
		}
		return
	}

	softEdge := config.SoftEdge
	pos16 := int(position * 255 * 65536)
	se16 := int(softEdge * 255 * 65536)
	if se16 < 1 {
		se16 = 1
	}
	twoSE16 := 2 * se16
	// Sanitized repeat count (see WipeConfig.Multiplier): guards against
	// overflow / degenerate-cut from unvalidated JSON input.
	mul := config.Multiplier()

	for i := 0; i < n; i++ {
		g := gradientMap[i]
		if config.Reverse {
			g = 255 - g
		}
		if mul > 1 {
			g = byte((int(g) * mul) % 256)
		}
		g16 := int(g) * 65536
		dist := g16 - pos16

		var alpha byte
		if dist < -se16 {
			alpha = 255
		} else if dist > se16 {
			alpha = 0
		} else {
			num := int64(se16-dist) * 255
			den := int64(twoSE16)
			a := int((num + den/2) / den)
			if a < 0 {
				a = 0
			}
			if a > 255 {
				a = 255
			}
			alpha = byte(a)
		}
		alphaOut[i] = alpha
	}
}

// legacyWipeGradient generates a gradient map that exactly matches the old
// generateWipeAlpha threshold calculations for the 6 legacy wipe directions.
// This is used internally to provide byte-exact backward compatibility when
// BlendWipe is called with the old WipeDirection constants.
func legacyWipeGradient(w, h int, direction WipeDirection) []byte {
	buf := make([]byte, w*h)

	switch direction {
	case WipeHLeft:
		invW := 1.0
		if w > 1 {
			invW = 1.0 / float64(w-1)
		}
		for x := 0; x < w; x++ {
			threshold := float64(x) * invW
			v := byte(threshold*255 + 0.5)
			for y := 0; y < h; y++ {
				buf[y*w+x] = v
			}
		}

	case WipeHRight:
		invW := 1.0
		if w > 1 {
			invW = 1.0 / float64(w-1)
		}
		for x := 0; x < w; x++ {
			threshold := 1.0 - float64(x)*invW
			v := byte(threshold*255 + 0.5)
			for y := 0; y < h; y++ {
				buf[y*w+x] = v
			}
		}

	case WipeVTop:
		invH := 1.0
		if h > 1 {
			invH = 1.0 / float64(h-1)
		}
		for y := 0; y < h; y++ {
			threshold := float64(y) * invH
			v := byte(threshold*255 + 0.5)
			row := buf[y*w : y*w+w]
			for i := range row {
				row[i] = v
			}
		}

	case WipeVBottom:
		invH := 1.0
		if h > 1 {
			invH = 1.0 / float64(h-1)
		}
		for y := 0; y < h; y++ {
			threshold := 1.0 - float64(y)*invH
			v := byte(threshold*255 + 0.5)
			row := buf[y*w : y*w+w]
			for i := range row {
				row[i] = v
			}
		}

	case WipeBoxCenterOut:
		cx := float64(w-1) / 2.0
		cy := float64(h-1) / 2.0
		invCx := 1.0 / cx
		invCy := 1.0 / cy
		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			if dy < 0 {
				dy = -dy
			}
			ty := dy * invCy
			row := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				if dx < 0 {
					dx = -dx
				}
				tx := dx * invCx
				threshold := tx
				if ty > tx {
					threshold = ty
				}
				buf[row+x] = byte(threshold*255 + 0.5)
			}
		}

	case WipeBoxEdgesIn:
		cx := float64(w-1) / 2.0
		cy := float64(h-1) / 2.0
		invCx := 1.0 / cx
		invCy := 1.0 / cy
		for y := 0; y < h; y++ {
			dy := float64(y) - cy
			if dy < 0 {
				dy = -dy
			}
			ty := dy * invCy
			row := y * w
			for x := 0; x < w; x++ {
				dx := float64(x) - cx
				if dx < 0 {
					dx = -dx
				}
				tx := dx * invCx
				threshold := tx
				if ty > tx {
					threshold = ty
				}
				threshold = 1.0 - threshold
				buf[row+x] = byte(threshold*255 + 0.5)
			}
		}
	}
	return buf
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

// BlendNAM performs a Non-Additive Mix between yuvA and yuvB in YUV420 space.
// For each pixel, the output is the brighter (higher gained Y) of the two sources.
// position 0.0 = all A, position 1.0 = all B.
// At any position, gain_a = 1-position and gain_b = position.
// Per luma pixel: if A*gain_a >= B*gain_b, output A*gain_a (and use A's chroma);
// otherwise output B*gain_b (and use B's chroma).
// Chroma is resolved per 2x2 block by majority vote of the luma winners.
// Uses fixed-point integer math for the luma comparison and scaling.
func (fb *FrameBlender) BlendNAM(yuvA, yuvB []byte, position float64) []byte {
	gainB := int(position*256 + 0.5)
	if gainB < 0 {
		gainB = 0
	}
	if gainB > 256 {
		gainB = 256
	}
	gainA := 256 - gainB

	w := fb.width
	h := fb.height

	// --- Y plane: per-pixel max of gained values ---
	// Also build a per-pixel winner map for chroma resolution.
	// winner[i] = true means B won at luma pixel i.
	// We reuse wipeAlphaMap as scratch (same size as Y plane).
	winnerMap := fb.wipeAlphaMap // len = w*h, repurposed as scratch

	for i := 0; i < fb.ySize; i++ {
		scaledA := int(yuvA[i]) * gainA
		scaledB := int(yuvB[i]) * gainB
		if scaledB > scaledA {
			fb.yuvBufOut[i] = byte(scaledB >> 8)
			winnerMap[i] = 1 // B won
		} else {
			fb.yuvBufOut[i] = byte(scaledA >> 8)
			winnerMap[i] = 0 // A won
		}
	}

	// --- Chroma planes: for each 2x2 block, use majority winner's chroma ---
	chromaW := w / 2
	chromaH := h / 2
	cbOffsetA := fb.ySize
	crOffsetA := fb.ySize + fb.uvSize
	cbOffsetOut := fb.ySize
	crOffsetOut := fb.ySize + fb.uvSize

	for cy := 0; cy < chromaH; cy++ {
		ly := cy * 2
		for cx := 0; cx < chromaW; cx++ {
			lx := cx * 2
			// Count B winners in the 2x2 luma block
			bCount := int(winnerMap[ly*w+lx]) +
				int(winnerMap[ly*w+lx+1]) +
				int(winnerMap[(ly+1)*w+lx]) +
				int(winnerMap[(ly+1)*w+lx+1])

			// Cb/Cr are offset-binary signals centered at 128, so the
			// winner's gain must be applied about the neutral point:
			// out = 128 + (C-128)*gain. Scaling the raw byte would drag
			// neutral gray toward 0 (a green-yellow cast at mid-mix).
			ci := cy*chromaW + cx
			if bCount >= 2 {
				// B majority: use B's chroma scaled by gainB
				fb.yuvBufOut[cbOffsetOut+ci] = byte(128 + (int(yuvB[cbOffsetA+ci])-128)*gainB>>8)
				fb.yuvBufOut[crOffsetOut+ci] = byte(128 + (int(yuvB[crOffsetA+ci])-128)*gainB>>8)
			} else {
				// A majority: use A's chroma scaled by gainA
				fb.yuvBufOut[cbOffsetOut+ci] = byte(128 + (int(yuvA[cbOffsetA+ci])-128)*gainA>>8)
				fb.yuvBufOut[crOffsetOut+ci] = byte(128 + (int(yuvA[crOffsetA+ci])-128)*gainA>>8)
			}
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
	blendFadeConst(&fb.yuvBufOut[0], &yuvA[0], fb.ySize, gain256, blackYi*invGain256)
	// Cb plane: fade toward 128
	cbOffset := fb.ySize
	blendFadeConst(&fb.yuvBufOut[cbOffset], &yuvA[cbOffset], fb.uvSize, gain256, 128*invGain256)
	// Cr plane: fade toward 128
	crOffset := fb.ySize + fb.uvSize
	blendFadeConst(&fb.yuvBufOut[crOffset], &yuvA[crOffset], fb.uvSize, gain256, 128*invGain256)
	return fb.yuvBufOut
}
