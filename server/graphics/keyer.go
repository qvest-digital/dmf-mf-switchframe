package graphics

// keyer.go — chroma and luma key generation in YUV420 domain.

// YCbCr represents a color in YCbCr space (BT.709).
type YCbCr struct {
	Y  uint8
	Cb uint8
	Cr uint8
}

// ChromaKey generates an alpha mask for chroma keying in YUV420 space.
// Pixels close to keyColor in Cb/Cr space become transparent.
// Spill suppression pulls chroma toward neutral (128, 128).
//
// This is a backward-compatible wrapper around ChromaKeyWithSpillColor.
func ChromaKey(frame []byte, width, height int, keyColor YCbCr, similarity, smoothness, spillSuppress float32) []byte {
	return ChromaKeyWithSpillColor(frame, width, height, keyColor, similarity, smoothness, spillSuppress, 128, 128)
}

// ChromaKeyWithSpillColor generates an alpha mask for chroma keying in YUV420 space.
// Pixels close to keyColor in Cb/Cr space become transparent.
//
// Parameters:
//   - frame: YUV420 planar data (Y[w*h] + Cb[w/2*h/2] + Cr[w/2*h/2])
//   - width, height: frame dimensions
//   - keyColor: the color to key out (in YCbCr)
//   - similarity: Cb/Cr distance threshold (0-1). Higher = more keyed.
//   - smoothness: soft edge feathering beyond the similarity boundary (0-1).
//   - spillSuppress: desaturate near-key pixels (0-1). Modifies the frame in-place.
//   - spillReplaceCb, spillReplaceCr: chroma values to pull spill toward (128,128 = neutral).
//
// Uses squared distance comparisons throughout to avoid per-pixel sqrt. The
// smoothness transition zone interpolates linearly in squared-distance space
// (not Euclidean distance), producing a slightly non-linear feather that is
// visually acceptable and significantly faster than a true sqrt-based ramp.
//
// Returns an alpha mask with one byte per pixel (0 = transparent, 255 = opaque).
func ChromaKeyWithSpillColor(frame []byte, width, height int, keyColor YCbCr, similarity, smoothness, spillSuppress float32, spillReplaceCb, spillReplaceCr uint8) []byte {
	return ChromaKeyWithSpillColorInto(nil, nil, frame, width, height, keyColor, similarity, smoothness, spillSuppress, spillReplaceCb, spillReplaceCr)
}

// ChromaKeyWithSpillColorInto is like ChromaKeyWithSpillColor but writes into
// pre-allocated mask and chromaMask buffers to avoid per-frame allocation.
// If mask or chromaMask are nil or too small, new buffers are allocated.
func ChromaKeyWithSpillColorInto(mask, chromaMask, frame []byte, width, height int, keyColor YCbCr, similarity, smoothness, spillSuppress float32, spillReplaceCb, spillReplaceCr uint8) []byte {
	pixelCount := width * height
	if pixelCount == 0 {
		return nil
	}

	if cap(mask) >= pixelCount {
		mask = mask[:pixelCount]
		// Zero the mask — keyer writes only non-opaque values in some paths.
		for i := range mask {
			mask[i] = 0
		}
	} else {
		mask = make([]byte, pixelCount)
	}
	ySize := width * height
	uvWidth := width / 2
	uvHeight := height / 2
	uvSize := uvWidth * uvHeight

	// Normalize similarity/smoothness to chroma distance scale.
	// Max Cb/Cr distance is ~181 (diagonal of 128x128 quadrant).
	simDist := similarity * 181.0
	smoothDist := smoothness * 181.0

	// Precompute squared thresholds (integer) for the kernel.
	simDistSqF := simDist * simDist
	totalDist := simDist + smoothDist
	totalDistSqF := totalDist * totalDist

	simThreshSq := int(simDistSqF)
	totalThreshSq := int(totalDistSqF)

	// Precompute fixed-point inverse range for smooth zone interpolation.
	// invRange = 255 * 65536 / (totalThreshSq - simThreshSq)
	// The kernel computes: (distSq - simThreshSq) * invRange >> 16
	rangeSq := totalThreshSq - simThreshSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	// Validate frame size before passing pointers to kernel.
	if len(frame) < ySize+2*uvSize || uvSize == 0 {
		// Frame too small or degenerate dimensions: fall back to all-opaque.
		for i := range mask {
			mask[i] = 255
		}
		return mask
	}

	// Step 1: Compute chroma-resolution mask using the assembly/Go kernel.
	// The chroma key distance is identical for all 4 luma pixels in a 2x2 block
	// (they share the same Cb/Cr values), so computing at chroma resolution is
	// both correct and 4x fewer distance computations.
	if cap(chromaMask) >= uvSize {
		chromaMask = chromaMask[:uvSize]
		for i := range chromaMask {
			chromaMask[i] = 0
		}
	} else {
		chromaMask = make([]byte, uvSize)
	}
	chromaKeyMaskChroma(&chromaMask[0], &frame[ySize], &frame[ySize+uvSize],
		int(keyColor.Cb), int(keyColor.Cr), simThreshSq, totalThreshSq, invRange, uvSize)

	// Step 2: Expand chroma mask to luma resolution (each chroma value covers 2x2 luma block).
	// Per-chroma-row: expand horizontally (each byte → 2 bytes), then copy to odd row.
	for chromaRow := 0; chromaRow < uvHeight; chromaRow++ {
		srcOff := chromaRow * uvWidth
		dstOff := (chromaRow * 2) * width
		expandChromaMaskRow(&mask[dstOff], &chromaMask[srcOff], uvWidth)
		copy(mask[dstOff+width:dstOff+2*width], mask[dstOff:dstOff+width])
	}

	// Step 3: Spill suppression via per-row kernel (assembly on arm64).
	if spillSuppress > 0 {
		keyCb := float32(keyColor.Cb)
		keyCr := float32(keyColor.Cr)
		spillDist := (simDist + smoothDist) * 2
		spillDistSq := spillDist * spillDist
		replaceCb := float32(spillReplaceCb)
		replaceCr := float32(spillReplaceCr)

		if spillDist > 0 && spillDistSq > 0 {
			invSpillDistSq := 1.0 / spillDistSq
			spillSuppressChroma(&frame[ySize], &frame[ySize+uvSize],
				keyCb, keyCr, spillSuppress, invSpillDistSq, replaceCb, replaceCr, uvSize)
		}
	}

	return mask
}

// LumaKey generates an alpha mask based on luma (Y channel) thresholds.
// Pixels below lowClip or above highClip become transparent.
//
// Parameters:
//   - frame: YUV420 planar data (only the Y plane is used)
//   - width, height: frame dimensions
//   - lowClip: normalized threshold (0-1). Pixels with Y/255 < lowClip are transparent.
//   - highClip: normalized threshold (0-1). Pixels with Y/255 > highClip are transparent.
//   - softness: gradual transition zone around clip points (0-1).
//
// Uses a precomputed 256-byte lookup table mapping each Y value to the output
// alpha byte, then delegates to lumaKeyMaskLUT (assembly on amd64/arm64) for
// bounds-check-free bulk application.
//
// Returns an alpha mask with one byte per pixel (0 = transparent, 255 = opaque).
func LumaKey(frame []byte, width, height int, lowClip, highClip, softness float32) []byte {
	return LumaKeyInto(nil, frame, width, height, lowClip, highClip, softness)
}

// LumaKeyInto is like LumaKey but writes into a pre-allocated mask buffer
// to avoid per-frame allocation. If mask is nil or too small, a new buffer is allocated.
func LumaKeyInto(mask, frame []byte, width, height int, lowClip, highClip, softness float32) []byte {
	pixelCount := width * height
	if pixelCount == 0 {
		return nil
	}

	// Clamp effective pixel count to frame length for safety.
	n := pixelCount
	if n > len(frame) {
		n = len(frame)
	}

	if cap(mask) >= pixelCount {
		mask = mask[:pixelCount]
	} else {
		mask = make([]byte, pixelCount)
	}

	// Build the 256-byte LUT: lut[y] = alpha byte for Y value y.
	var lut [256]byte
	for y := 0; y < 256; y++ {
		luma := float32(y) / 255.0
		var alpha float32
		if luma < lowClip {
			if softness > 0 && luma > lowClip-softness {
				alpha = (lowClip - luma) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else if luma > highClip {
			if softness > 0 && luma < highClip+softness {
				alpha = (luma - highClip) / softness
				alpha = 1.0 - alpha
			} else {
				alpha = 0.0
			}
		} else {
			alpha = 1.0
		}
		lut[y] = uint8(clampFloat(alpha*255.0, 0, 255))
	}

	// Apply LUT via assembly kernel (no bounds checks in inner loop).
	lumaKeyMaskLUT(&mask[0], &frame[0], &lut[0], n)

	// If the frame was shorter than pixelCount, fill remaining with 255 (opaque).
	for i := n; i < pixelCount; i++ {
		mask[i] = 255
	}

	return mask
}

// clampFloat clamps a float32 to [min, max].
func clampFloat(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
