//go:build !arm64 && !amd64

package graphics

import "unsafe"

// alphaBlendRGBARowY processes one row of the Y plane using integer fixed-point
// BT.709 coefficients. For each pixel:
//
//	A' = A + (A >> 7)                       (map 0-255 to 0-256 range)
//	a256 = (A' * alphaScale256) >> 8        (effective alpha, 0-256 range)
//	overlayY = (54*R + 183*G + 18*B + 128) >> 8
//	yRow[i] = (yRow[i]*(256-a256) + overlayY*a256 + 128) >> 8
//
// Transparent pixels (a256 == 0) are skipped for speed.
func alphaBlendRGBARowY(yRow *byte, rgba *byte, width int, alphaScale256 int) {
	if width <= 0 {
		return
	}
	yS := unsafe.Slice(yRow, width)
	rgbaS := unsafe.Slice(rgba, width*4)
	for i := 0; i < width; i++ {
		ri := i * 4
		A := int(rgbaS[ri+3])
		A += A >> 7 // map 0-255 to 0-256
		a256 := (A * alphaScale256) >> 8
		if a256 == 0 {
			continue
		}
		R := int(rgbaS[ri])
		G := int(rgbaS[ri+1])
		B := int(rgbaS[ri+2])
		overlayY := (54*R + 183*G + 18*B + 128) >> 8
		inv := 256 - a256
		yS[i] = byte((int(yS[i])*inv + overlayY*a256 + 128) >> 8)
	}
}
