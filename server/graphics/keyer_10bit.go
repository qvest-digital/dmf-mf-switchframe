package graphics

import (
	"encoding/binary"
)

// ChromaKey10 generates an 8-bit alpha mask from a YUV422P10LE frame using chroma keying.
// The mask has one byte per luma pixel (w*h). Pixels close to (targetCb, targetCr) in
// chroma space become transparent (mask=255). Pixels far away remain opaque (mask=0).
//
// Parameters:
//   - yuv: YUV422P10LE planar data (Y[w*h*2] + Cb[w/2*h*2] + Cr[w/2*h*2])
//   - w, h: frame dimensions (w must be even)
//   - targetCb, targetCr: chroma values to key out (10-bit range 0-1023)
//   - tolerance: chroma distance threshold below which pixels are fully keyed
//   - softness: additional distance for the soft edge transition zone
//
// The soft edge uses linear interpolation in squared-distance space (not Euclidean
// distance). This produces a slightly non-linear feather that is visually acceptable
// and enables efficient SIMD implementation without per-pixel sqrt.
//
// Returns an alpha mask with one byte per pixel (0 = opaque/keep, 255 = transparent/keyed).
func ChromaKey10(yuv []byte, w, h int, targetCb, targetCr uint16, tolerance, softness int) []byte {
	pixelCount := w * h
	if pixelCount == 0 {
		return nil
	}
	if w%2 != 0 {
		return nil
	}

	ySize := w * h
	chromaW := w / 2
	chromaSize := chromaW * h

	needBytes := ySize*2 + chromaSize*2*2
	if len(yuv) < needBytes {
		return nil
	}

	mask := make([]byte, pixelCount)

	cbByteOff := ySize * 2
	crByteOff := cbByteOff + chromaSize*2

	tCb := int(targetCb)
	tCr := int(targetCr)

	// Precompute squared thresholds for the inner loop.
	simDistSq := tolerance * tolerance
	totalDist := tolerance + softness
	totalDistSq := totalDist * totalDist

	// Precompute fixed-point inverse range for smooth zone interpolation.
	// invRange = 255 * 65536 / (totalDistSq - simDistSq)
	// The kernel computes: 255 - (distSq - simDistSq) * invRange >> 16
	rangeSq := totalDistSq - simDistSq
	invRange := 0
	if rangeSq > 0 {
		invRange = 255 * 65536 / rangeSq
	}

	// Compute chroma mask at chroma resolution, then expand to luma resolution.
	// Each chroma sample covers 2 horizontal luma pixels (4:2:2).
	for py := 0; py < h; py++ {
		for cx := 0; cx < chromaW; cx++ {
			cbOff := cbByteOff + (py*chromaW+cx)*2
			crOff := crByteOff + (py*chromaW+cx)*2

			cb := int(binary.LittleEndian.Uint16(yuv[cbOff:]))
			cr := int(binary.LittleEndian.Uint16(yuv[crOff:]))

			dCb := cb - tCb
			dCr := cr - tCr
			distSq := dCb*dCb + dCr*dCr

			var keyVal byte
			if distSq <= simDistSq {
				// Fully keyed
				keyVal = 255
			} else if distSq < totalDistSq && rangeSq > 0 {
				// Soft edge: linear interpolation in squared-distance space
				v := 255 - (distSq-simDistSq)*invRange>>16
				if v < 0 {
					v = 0
				}
				keyVal = byte(v)
			}
			// else: keyVal = 0 (opaque, default)

			// Expand to 2 horizontal luma pixels
			lx := cx * 2
			lumaIdx := py*w + lx
			mask[lumaIdx] = keyVal
			if lx+1 < w {
				mask[lumaIdx+1] = keyVal
			}
		}
	}

	return mask
}

// LumaKey10 generates an 8-bit alpha mask from a YUV422P10LE frame using luma keying.
// The mask has one byte per luma pixel (w*h). Pixels with Y values between lowThresh
// and highThresh (inclusive) are opaque (mask=255). Pixels outside this range are
// transparent (mask=0, keyed out).
//
// Parameters:
//   - yuv: YUV422P10LE planar data (only the Y plane is used)
//   - w, h: frame dimensions
//   - lowThresh, highThresh: Y value thresholds in 10-bit range (0-1023)
//
// Returns an alpha mask with one byte per pixel (0 = transparent/keyed, 255 = opaque/keep).
func LumaKey10(yuv []byte, w, h int, lowThresh, highThresh uint16) []byte {
	pixelCount := w * h
	if pixelCount == 0 {
		return nil
	}

	// Need at least the Y plane
	if len(yuv) < pixelCount*2 {
		return nil
	}

	mask := make([]byte, pixelCount)

	for i := 0; i < pixelCount; i++ {
		y := binary.LittleEndian.Uint16(yuv[i*2:])
		if y >= lowThresh && y <= highThresh {
			mask[i] = 255 // opaque (in range, keep)
		}
		// else: mask[i] = 0 (transparent, keyed out)
	}

	return mask
}
