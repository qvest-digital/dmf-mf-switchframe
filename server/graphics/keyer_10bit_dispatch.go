package graphics

import (
	"encoding/binary"
	"unsafe"
)

// lumaKey10Dispatch performs 10-bit luma keying using SIMD-accelerated row processing.
// This dispatches to lumaKey10Row which has platform-specific implementations:
//   - amd64: AVX2 (16 samples/iter via unsigned saturating subtract)
//   - arm64: NEON (8 samples/iter via CMHS unsigned compare)
//   - generic: pure Go fallback
//
// The function is bit-identical to LumaKey10 for all inputs.
func lumaKey10Dispatch(yuv []byte, w, h int, lowThresh, highThresh uint16) []byte {
	pixelCount := w * h
	if pixelCount == 0 {
		return nil
	}

	// Need at least the Y plane
	if len(yuv) < pixelCount*2 {
		return nil
	}

	mask := make([]byte, pixelCount)

	lumaKey10Row(&mask[0], &yuv[0], pixelCount, int(lowThresh), int(highThresh))

	return mask
}

// lumaKey10RowGo is the pure Go reference implementation for 10-bit luma key row processing.
// For each uint16 LE sample y: if y >= lowThresh && y <= highThresh, mask = 255; else mask = 0.
// Used as the fallback on platforms without SIMD and as the reference for testing.
func lumaKey10RowGo(mask *byte, y *byte, count int, lowThresh, highThresh uint16) {
	if count <= 0 {
		return
	}

	maskS := unsafe.Slice(mask, count)
	yS := unsafe.Slice(y, count*2)

	for i := 0; i < count; i++ {
		yVal := binary.LittleEndian.Uint16(yS[i*2:])
		if yVal >= lowThresh && yVal <= highThresh {
			maskS[i] = 255
		} else {
			maskS[i] = 0
		}
	}
}
