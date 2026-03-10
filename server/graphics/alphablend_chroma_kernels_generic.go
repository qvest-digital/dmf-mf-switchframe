//go:build !arm64 && !amd64

package graphics

import "unsafe"

// alphaBlendRGBAChromaRow processes one row of Cb/Cr chroma planes.
// Generic scalar fallback for non-SIMD architectures.
//
// For each chroma pixel:
//   1. Read RGBA from full-res row at stride 8 (every other pixel)
//   2. Compute scaled alpha
//   3. Skip transparent pixels
//   4. Compute overlay Cb/Cr via BT.709 integer coefficients
//   5. Blend with existing Cb/Cr values
func alphaBlendRGBAChromaRow(cbRow *byte, crRow *byte, rgba *byte, chromaWidth int, alphaScale256 int) {
	if chromaWidth <= 0 {
		return
	}
	cbS := unsafe.Slice(cbRow, chromaWidth)
	crS := unsafe.Slice(crRow, chromaWidth)
	rgbaS := unsafe.Slice(rgba, chromaWidth*8) // stride-8 access over 2× full-res pixels

	for i := 0; i < chromaWidth; i++ {
		rgbaOff := i * 8
		A := int(rgbaS[rgbaOff+3])
		A += A >> 7
		a256 := (A * alphaScale256) >> 8
		if a256 == 0 {
			continue
		}
		R := int(rgbaS[rgbaOff])
		G := int(rgbaS[rgbaOff+1])
		B := int(rgbaS[rgbaOff+2])

		overlayCb := ((-29*R - 99*G + 128*B + 128) >> 8) + 128
		overlayCr := ((128*R - 116*G - 12*B + 128) >> 8) + 128

		inv := 256 - a256
		cb := (int(cbS[i])*inv + overlayCb*a256 + 128) >> 8
		cr := (int(crS[i])*inv + overlayCr*a256 + 128) >> 8
		if cb < 0 {
			cb = 0
		}
		if cb > 255 {
			cb = 255
		}
		if cr < 0 {
			cr = 0
		}
		if cr > 255 {
			cr = 255
		}
		cbS[i] = byte(cb)
		crS[i] = byte(cr)
	}
}
