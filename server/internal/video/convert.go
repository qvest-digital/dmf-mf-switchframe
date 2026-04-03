package video

import (
	"encoding/binary"
	"fmt"
)

// Upconvert420to422_10 converts a YUV420P 8-bit frame to YUV422P10LE.
// This performs:
//  1. Bit-depth promotion: 8-bit to 10-bit (val * 4, i.e. val << 2)
//  2. Chroma upsample: 4:2:0 to 4:2:2 (vertical interpolation of chroma rows)
//
// src must be at least w*h*3/2 bytes (YUV420P 8-bit).
// dst must be at least w*h*4 bytes (YUV422P10LE).
// Width and height must both be even and positive.
//
// The multiplication by 4 maps limited-range values correctly:
// 16->64 (black), 235->940 (white), 128->512 (neutral chroma).
func Upconvert420to422_10(src []byte, dst []byte, width, height int) error {
	if err := ValidateConvertParams(width, height); err != nil {
		return err
	}
	srcSize := YUV420_8bit.FrameSize(width, height)
	dstSize := YUV422_10bit.FrameSize(width, height)
	if len(src) < srcSize {
		return fmt.Errorf("src buffer too small: need %d, got %d", srcSize, len(src))
	}
	if len(dst) < dstSize {
		return fmt.Errorf("dst buffer too small: need %d, got %d", dstSize, len(dst))
	}

	chromaW := width / 2
	chromaH420 := height / 2

	// --- Y plane: 8-bit to 10-bit ---
	// src Y: width*height bytes at offset 0
	// dst Y: width*height*2 bytes at offset 0 (16-bit LE samples)
	for i := 0; i < width*height; i++ {
		val10 := uint16(src[i]) << 2
		binary.LittleEndian.PutUint16(dst[i*2:], val10)
	}

	// --- Cb plane: upsample 420->422 + 8->10 bit ---
	srcCbOff := width * height
	dstCbOff := width * height * 2 // after Y plane in 10-bit

	upconvertChromaPlane(
		src[srcCbOff:srcCbOff+chromaW*chromaH420],
		dst[dstCbOff:dstCbOff+chromaW*height*2],
		chromaW, height,
	)

	// --- Cr plane: same treatment ---
	srcCrOff := srcCbOff + chromaW*chromaH420
	dstCrOff := dstCbOff + chromaW*height*2

	upconvertChromaPlane(
		src[srcCrOff:srcCrOff+chromaW*chromaH420],
		dst[dstCrOff:dstCrOff+chromaW*height*2],
		chromaW, height,
	)

	return nil
}

// upconvertChromaPlane upsamples one chroma plane from 420 (h/2 rows) to 422
// (h rows) with 8-bit to 10-bit promotion. Uses MPEG-2 style cosited vertical
// interpolation: even rows are direct copies, odd rows are averaged from
// adjacent source rows.
func upconvertChromaPlane(src420 []byte, dst422 []byte, chromaW, fullH int) {
	srcH := fullH / 2

	for dstRow := 0; dstRow < fullH; dstRow++ {
		srcRow := dstRow / 2
		dstOff := dstRow * chromaW * 2

		if dstRow%2 == 0 {
			// Even row: direct copy from source row with bit-depth promotion.
			srcOff := srcRow * chromaW
			for x := 0; x < chromaW; x++ {
				val10 := uint16(src420[srcOff+x]) << 2
				binary.LittleEndian.PutUint16(dst422[dstOff+x*2:], val10)
			}
		} else {
			// Odd row: interpolate between srcRow and srcRow+1.
			nextRow := srcRow + 1
			if nextRow >= srcH {
				nextRow = srcRow // clamp at bottom edge
			}
			srcOff0 := srcRow * chromaW
			srcOff1 := nextRow * chromaW
			for x := 0; x < chromaW; x++ {
				a := int(src420[srcOff0+x])
				b := int(src420[srcOff1+x])
				avg := (a + b + 1) >> 1
				val10 := uint16(avg) << 2
				binary.LittleEndian.PutUint16(dst422[dstOff+x*2:], val10)
			}
		}
	}
}

// Downconvert422_10to420 converts a YUV422P10LE frame to YUV420P 8-bit.
// This performs:
//  1. Bit-depth demotion: 10-bit to 8-bit ((val + 2) >> 2, rounded)
//  2. Chroma downsample: 4:2:2 to 4:2:0 (average pairs of vertical rows)
//
// src must be at least w*h*4 bytes (YUV422P10LE).
// dst must be at least w*h*3/2 bytes (YUV420P 8-bit).
// Width and height must both be even and positive.
func Downconvert422_10to420(src []byte, dst []byte, width, height int) error {
	if err := ValidateConvertParams(width, height); err != nil {
		return err
	}
	srcSize := YUV422_10bit.FrameSize(width, height)
	dstSize := YUV420_8bit.FrameSize(width, height)
	if len(src) < srcSize {
		return fmt.Errorf("src buffer too small: need %d, got %d", srcSize, len(src))
	}
	if len(dst) < dstSize {
		return fmt.Errorf("dst buffer too small: need %d, got %d", dstSize, len(dst))
	}

	chromaW := width / 2
	chromaH420 := height / 2

	// --- Y plane: 10-bit to 8-bit ---
	for i := 0; i < width*height; i++ {
		val10 := binary.LittleEndian.Uint16(src[i*2:])
		dst[i] = byte((val10 + 2) >> 2)
	}

	// --- Cb plane: 422->420 downsample + 10->8 bit ---
	srcCbOff := width * height * 2
	dstCbOff := width * height

	downconvertChromaPlane(
		src[srcCbOff:srcCbOff+chromaW*height*2],
		dst[dstCbOff:dstCbOff+chromaW*chromaH420],
		chromaW, height,
	)

	// --- Cr plane: same treatment ---
	srcCrOff := srcCbOff + chromaW*height*2
	dstCrOff := dstCbOff + chromaW*chromaH420

	downconvertChromaPlane(
		src[srcCrOff:srcCrOff+chromaW*height*2],
		dst[dstCrOff:dstCrOff+chromaW*chromaH420],
		chromaW, height,
	)

	return nil
}

// downconvertChromaPlane downsamples one chroma plane from 422 (h rows) to 420
// (h/2 rows) with 10-bit to 8-bit demotion. Pairs of vertical rows are averaged:
// dst row 0 = avg(src row 0, src row 1), dst row 1 = avg(src row 2, src row 3), etc.
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

// ValidateConvertParams checks common preconditions for format conversion.
func ValidateConvertParams(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid dimensions: %dx%d (must be positive)", width, height)
	}
	if width%2 != 0 {
		return fmt.Errorf("width must be even, got %d", width)
	}
	if height%2 != 0 {
		return fmt.Errorf("height must be even, got %d", height)
	}
	return nil
}
