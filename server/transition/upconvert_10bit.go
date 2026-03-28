package transition

import (
	"encoding/binary"
	"fmt"
)

// upconvert420to422_10 converts a YUV420P 8-bit frame to YUV422P10LE.
// This is used internally by the transition engine to upconvert 8-bit stinger
// overlay frames for compositing onto 10-bit program frames.
//
// Performs:
//  1. Bit-depth promotion: 8-bit to 10-bit (val << 2, i.e. val * 4)
//  2. Chroma upsample: 4:2:0 to 4:2:2 (vertical interpolation of chroma rows)
//
// src must be at least w*h*3/2 bytes (YUV420P 8-bit).
// dst must be at least w*h*4 bytes (YUV422P10LE).
// Width and height must both be even and positive.
func upconvert420to422_10(src []byte, dst []byte, width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d", width, height)
	}
	if width%2 != 0 || height%2 != 0 {
		return fmt.Errorf("dimensions must be even: %dx%d", width, height)
	}

	srcSize := width*height + 2*(width/2)*(height/2) // YUV420P 8-bit
	dstSize := width * height * 4                     // YUV422P10LE
	if len(src) < srcSize {
		return fmt.Errorf("src buffer too small: need %d, got %d", srcSize, len(src))
	}
	if len(dst) < dstSize {
		return fmt.Errorf("dst buffer too small: need %d, got %d", dstSize, len(dst))
	}

	chromaW := width / 2
	chromaH420 := height / 2

	// Y plane: 8-bit to 10-bit.
	for i := 0; i < width*height; i++ {
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(src[i])<<2)
	}

	// Cb plane: upsample 420->422 + 8->10 bit.
	srcCbOff := width * height
	dstCbOff := width * height * 2
	upconvertChromaPlane10(
		src[srcCbOff:srcCbOff+chromaW*chromaH420],
		dst[dstCbOff:dstCbOff+chromaW*height*2],
		chromaW, height,
	)

	// Cr plane.
	srcCrOff := srcCbOff + chromaW*chromaH420
	dstCrOff := dstCbOff + chromaW*height*2
	upconvertChromaPlane10(
		src[srcCrOff:srcCrOff+chromaW*chromaH420],
		dst[dstCrOff:dstCrOff+chromaW*height*2],
		chromaW, height,
	)

	return nil
}

// upconvertChromaPlane10 upsamples one chroma plane from 420 (h/2 rows) to 422
// (h rows) with 8-bit to 10-bit promotion. Uses MPEG-2 style cosited vertical
// interpolation: even rows are direct copies, odd rows are averaged from
// adjacent source rows.
func upconvertChromaPlane10(src420 []byte, dst422 []byte, chromaW, fullH int) {
	srcH := fullH / 2

	for dstRow := 0; dstRow < fullH; dstRow++ {
		srcRow := dstRow / 2
		dstOff := dstRow * chromaW * 2

		if dstRow%2 == 0 {
			// Even row: direct copy with bit-depth promotion.
			srcOff := srcRow * chromaW
			for x := 0; x < chromaW; x++ {
				binary.LittleEndian.PutUint16(dst422[dstOff+x*2:], uint16(src420[srcOff+x])<<2)
			}
		} else {
			// Odd row: interpolate between adjacent source rows.
			srcOff0 := srcRow * chromaW
			nextRow := srcRow + 1
			if nextRow >= srcH {
				nextRow = srcH - 1
			}
			srcOff1 := nextRow * chromaW
			for x := 0; x < chromaW; x++ {
				v0 := uint16(src420[srcOff0+x])
				v1 := uint16(src420[srcOff1+x])
				avg := ((v0 + v1 + 1) >> 1) << 2
				binary.LittleEndian.PutUint16(dst422[dstOff+x*2:], avg)
			}
		}
	}
}
