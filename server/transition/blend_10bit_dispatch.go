package transition

import (
	"encoding/binary"
	"unsafe"
)

// blendMix10Dispatch performs 10-bit mix blend using SIMD-accelerated row processing.
// This dispatches to blendMix10Row which has platform-specific implementations:
//   - amd64: AVX2 (8 samples/iter via 32-bit widening)
//   - arm64: NEON (8 samples/iter via widening multiply)
//   - generic: pure Go fallback
//
// The function is bit-identical to BlendMix10 for all inputs.
func blendMix10Dispatch(dst, a, b []byte, width, height, pos256 int) error {
	if err := validate10(dst, a, b, width, height); err != nil {
		return err
	}
	if pos256 < 0 {
		pos256 = 0
	}
	if pos256 > 256 {
		pos256 = 256
	}

	ySize := width * height
	chromaW := width / 2
	chromaSize := chromaW * height

	// Y plane: ySize samples starting at byte offset 0
	blendMix10Row(&dst[0], &a[0], &b[0], ySize, pos256)

	// Cb plane: chromaSize samples starting at byte offset ySize*2
	cbOff := ySize * 2
	blendMix10Row(&dst[cbOff], &a[cbOff], &b[cbOff], chromaSize, pos256)

	// Cr plane: chromaSize samples starting at byte offset ySize*2 + chromaSize*2
	crOff := cbOff + chromaSize*2
	blendMix10Row(&dst[crOff], &a[crOff], &b[crOff], chromaSize, pos256)

	return nil
}

// blendMix10RowGo is the pure Go reference implementation for blending a row
// of uint16 LE samples. Used as the fallback on platforms without SIMD.
//
//	dst[i] = clamp10((a[i] * (256-pos) + b[i] * pos + 128) >> 8)
func blendMix10RowGo(dst, a, b *byte, count, pos256 int) {
	if count <= 0 {
		return
	}

	inv := int32(256 - pos256)
	pos := int32(pos256)

	dstSlice := unsafe.Slice(dst, count*2)
	aSlice := unsafe.Slice(a, count*2)
	bSlice := unsafe.Slice(b, count*2)

	for i := 0; i < count; i++ {
		off := i * 2
		sa := int32(binary.LittleEndian.Uint16(aSlice[off:]))
		sb := int32(binary.LittleEndian.Uint16(bSlice[off:]))
		result := (sa*inv + sb*pos + 128) >> 8
		binary.LittleEndian.PutUint16(dstSlice[off:], clamp10(result))
	}
}
