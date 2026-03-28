package preview

import (
	"encoding/binary"
	"fmt"
)

// downconvert422_10to420 converts a YUV422P10LE frame to YUV420P 8-bit.
// This is used as a safety net when 10-bit frames reach the preview encoder
// (which only supports 8-bit H.264 encoding).
//
// Performs:
//  1. Bit-depth demotion: 10-bit to 8-bit ((val + 2) >> 2, rounded)
//  2. Chroma downsample: 4:2:2 to 4:2:0 (average pairs of vertical rows)
//
// src must be at least w*h*4 bytes (YUV422P10LE).
// dst must be at least w*h*3/2 bytes (YUV420P 8-bit).
func downconvert422_10to420(src []byte, dst []byte, width, height int) error {
	if width <= 0 || height <= 0 || width%2 != 0 || height%2 != 0 {
		return fmt.Errorf("preview: invalid dimensions %dx%d for downconvert", width, height)
	}

	srcSize := width * height * 4 // YUV422P10LE
	dstSize := yuvFrameSize(width, height)
	if len(src) < srcSize {
		return fmt.Errorf("preview: src buffer too small: need %d, got %d", srcSize, len(src))
	}
	if len(dst) < dstSize {
		return fmt.Errorf("preview: dst buffer too small: need %d, got %d", dstSize, len(dst))
	}

	chromaW := width / 2
	chromaH := height / 2

	// Y plane: 10-bit to 8-bit.
	for i := 0; i < width*height; i++ {
		val10 := binary.LittleEndian.Uint16(src[i*2:])
		dst[i] = byte((val10 + 2) >> 2)
	}

	// Cb plane: 422→420 downsample + 10→8 bit.
	srcCbOff := width * height * 2
	dstCbOff := width * height
	downconvertChromaPlane(src[srcCbOff:], dst[dstCbOff:], chromaW, height)

	// Cr plane.
	srcCrOff := srcCbOff + chromaW*height*2
	dstCrOff := dstCbOff + chromaW*chromaH
	downconvertChromaPlane(src[srcCrOff:], dst[dstCrOff:], chromaW, height)

	return nil
}

// downconvertChromaPlane downsamples one chroma plane from 422 (fullH rows)
// to 420 (fullH/2 rows) with 10-bit to 8-bit demotion.
func downconvertChromaPlane(src422 []byte, dst420 []byte, chromaW, fullH int) {
	dstH := fullH / 2
	for dstRow := 0; dstRow < dstH; dstRow++ {
		srcRow0 := dstRow * 2
		srcRow1 := srcRow0 + 1
		dstOff := dstRow * chromaW
		srcOff0 := srcRow0 * chromaW * 2
		srcOff1 := srcRow1 * chromaW * 2
		for x := 0; x < chromaW; x++ {
			a := binary.LittleEndian.Uint16(src422[srcOff0+x*2:])
			b := binary.LittleEndian.Uint16(src422[srcOff1+x*2:])
			avg := (uint32(a) + uint32(b) + 1) >> 1
			dst420[dstOff+x] = byte((avg + 2) >> 2)
		}
	}
}
