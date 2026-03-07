package graphics

import (
	"math"
)

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
// Uses squared distance comparisons to avoid per-pixel sqrt for the majority of
// pixels. Only the smoothness transition zone requires the actual Euclidean distance
// for linear interpolation.
//
// Returns an alpha mask with one byte per pixel (0 = transparent, 255 = opaque).
func ChromaKeyWithSpillColor(frame []byte, width, height int, keyColor YCbCr, similarity, smoothness, spillSuppress float32, spillReplaceCb, spillReplaceCr uint8) []byte {
	pixelCount := width * height
	if pixelCount == 0 {
		return nil
	}

	mask := make([]byte, pixelCount)
	ySize := width * height
	uvWidth := width / 2
	uvSize := uvWidth * (height / 2)

	keyCb := float32(keyColor.Cb)
	keyCr := float32(keyColor.Cr)

	// Normalize similarity/smoothness to chroma distance scale.
	// Max Cb/Cr distance is ~181 (diagonal of 128x128 quadrant).
	simDist := similarity * 181.0
	smoothDist := smoothness * 181.0

	// Precompute squared thresholds for fast comparison.
	simDistSq := simDist * simDist
	totalDist := simDist + smoothDist
	totalDistSq := totalDist * totalDist

	// Spill suppression thresholds (squared).
	spillDist := (simDist + smoothDist) * 2
	spillDistSq := spillDist * spillDist

	replaceCb := float32(spillReplaceCb)
	replaceCr := float32(spillReplaceCr)

	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			pixIdx := row*width + col
			uvIdx := (row/2)*uvWidth + (col / 2)

			// Bounds check for frame
			if uvIdx >= uvSize || ySize+uvIdx >= len(frame) || ySize+uvSize+uvIdx >= len(frame) {
				mask[pixIdx] = 255
				continue
			}

			cb := float32(frame[ySize+uvIdx])
			cr := float32(frame[ySize+uvSize+uvIdx])

			// Squared distance in Cb/Cr space (avoids sqrt for majority of pixels).
			dCb := cb - keyCb
			dCr := cr - keyCr
			distSq := dCb*dCb + dCr*dCr

			var alpha float32
			if distSq < simDistSq {
				// Inside similarity threshold: fully transparent (no sqrt needed).
				alpha = 0.0
			} else if smoothDist > 0 && distSq < totalDistSq {
				// In smoothness zone: need actual distance for linear interpolation.
				dist := float32(math.Sqrt(float64(distSq)))
				alpha = (dist - simDist) / smoothDist
			} else {
				// Outside both: fully opaque (no sqrt needed).
				alpha = 1.0
			}

			// Spill suppression: desaturate near-key pixels proportionally.
			if spillSuppress > 0 && spillDist > 0 && distSq < spillDistSq {
				// Only compute sqrt when inside the spill zone.
				dist := float32(math.Sqrt(float64(distSq)))
				spillAmount := spillSuppress * (1.0 - dist/spillDist)
				if spillAmount > 0 {
					// Pull chroma toward the replacement color.
					newCb := cb + (replaceCb-cb)*spillAmount
					newCr := cr + (replaceCr-cr)*spillAmount
					frame[ySize+uvIdx] = uint8(clampFloat(newCb, 0, 255))
					frame[ySize+uvSize+uvIdx] = uint8(clampFloat(newCr, 0, 255))
				}
			}

			mask[pixIdx] = uint8(clampFloat(alpha*255.0, 0, 255))
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
// Returns an alpha mask with one byte per pixel (0 = transparent, 255 = opaque).
func LumaKey(frame []byte, width, height int, lowClip, highClip, softness float32) []byte {
	pixelCount := width * height
	if pixelCount == 0 {
		return nil
	}

	mask := make([]byte, pixelCount)

	for i := 0; i < pixelCount; i++ {
		if i >= len(frame) {
			mask[i] = 255
			continue
		}

		luma := float32(frame[i]) / 255.0

		var alpha float32
		if luma < lowClip {
			// Below low clip: transparent
			if softness > 0 && luma > lowClip-softness {
				// Soft edge: gradual
				alpha = (lowClip - luma) / softness
				alpha = 1.0 - alpha // invert: closer to lowClip = more opaque
			} else {
				alpha = 0.0
			}
		} else if luma > highClip {
			// Above high clip: transparent
			if softness > 0 && luma < highClip+softness {
				// Soft edge: gradual
				alpha = (luma - highClip) / softness
				alpha = 1.0 - alpha // invert: closer to highClip = more opaque
			} else {
				alpha = 0.0
			}
		} else {
			// Between clips: opaque
			alpha = 1.0
		}

		mask[i] = uint8(clampFloat(alpha*255.0, 0, 255))
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
