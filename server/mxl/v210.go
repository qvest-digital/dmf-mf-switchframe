package mxl

import (
	"encoding/binary"
	"fmt"
)

// V210LineStride returns the byte stride for one line of V210 data at the given width.
// Each group of 6 pixels occupies 4 x 32-bit words (16 bytes).
// The result is padded to 128-byte alignment per the V210 spec.
func V210LineStride(width int) int {
	// Number of 6-pixel groups, rounded up
	groups := (width + 5) / 6
	// Each group is 16 bytes (4 x 32-bit words)
	rawBytes := groups * 16
	// Round up to next multiple of 128
	return (rawBytes + 127) &^ 127
}

// V210ToYUV420p converts a V210 (10-bit 4:2:2 packed) frame to YUV420p (8-bit 4:2:0 planar).
// Width must be divisible by 6 and height must be even.
//
// V210 packing (per 6 horizontal pixels = 4 x 32-bit words):
//
//	Word 0: bits [9:0]=Cb0, [19:10]=Y0, [29:20]=Cr0
//	Word 1: bits [9:0]=Y1,  [19:10]=Cb2, [29:20]=Y2
//	Word 2: bits [9:0]=Cr2, [19:10]=Y3,  [29:20]=Cb4
//	Word 3: bits [9:0]=Y4,  [19:10]=Cr4, [29:20]=Y5
//
// 10-bit to 8-bit conversion: right shift by 2.
// 4:2:2 to 4:2:0 chroma downsampling: average adjacent rows.
func V210ToYUV420p(v210 []byte, width, height int) ([]byte, error) {
	if width%6 != 0 {
		return nil, fmt.Errorf("v210: width %d is not divisible by 6", width)
	}
	if height%2 != 0 || height < 2 {
		return nil, fmt.Errorf("v210: height %d must be even and >= 2", height)
	}

	stride := V210LineStride(width)
	requiredSize := stride * height
	if len(v210) < requiredSize {
		return nil, fmt.Errorf("v210: buffer size %d too small, need %d for %dx%d", len(v210), requiredSize, width, height)
	}

	ySize := width * height
	chromaW := width / 2
	chromaH := height / 2
	cSize := chromaW * chromaH
	out := make([]byte, ySize+2*cSize)

	yPlane := out[:ySize]
	cbPlane := out[ySize : ySize+cSize]
	crPlane := out[ySize+cSize:]

	// Temporary 4:2:2 chroma buffers (one Cb and one Cr per 2 horizontal pixels per row)
	cb422 := make([]byte, chromaW*height)
	cr422 := make([]byte, chromaW*height)

	// Unpack V210 to extract Y (full resolution) and Cb/Cr (4:2:2)
	for row := 0; row < height; row++ {
		rowBase := row * stride
		yRow := row * width
		cRow := row * chromaW

		for group := 0; group < width/6; group++ {
			offset := rowBase + group*16

			w0 := binary.LittleEndian.Uint32(v210[offset:])
			w1 := binary.LittleEndian.Uint32(v210[offset+4:])
			w2 := binary.LittleEndian.Uint32(v210[offset+8:])
			w3 := binary.LittleEndian.Uint32(v210[offset+12:])

			// Extract 10-bit values from each word
			cb0 := w0 & 0x3FF
			y0 := (w0 >> 10) & 0x3FF
			cr0 := (w0 >> 20) & 0x3FF

			y1 := w1 & 0x3FF
			cb2 := (w1 >> 10) & 0x3FF
			y2 := (w1 >> 20) & 0x3FF

			cr2 := w2 & 0x3FF
			y3 := (w2 >> 10) & 0x3FF
			cb4 := (w2 >> 20) & 0x3FF

			y4 := w3 & 0x3FF
			cr4 := (w3 >> 10) & 0x3FF
			y5 := (w3 >> 20) & 0x3FF

			// Store Y (10-bit >> 2 = 8-bit)
			px := yRow + group*6
			yPlane[px+0] = byte(y0 >> 2)
			yPlane[px+1] = byte(y1 >> 2)
			yPlane[px+2] = byte(y2 >> 2)
			yPlane[px+3] = byte(y3 >> 2)
			yPlane[px+4] = byte(y4 >> 2)
			yPlane[px+5] = byte(y5 >> 2)

			// Store 4:2:2 chroma (3 Cb/Cr samples per 6-pixel group)
			cx := cRow + group*3
			cb422[cx+0] = byte(cb0 >> 2)
			cb422[cx+1] = byte(cb2 >> 2)
			cb422[cx+2] = byte(cb4 >> 2)
			cr422[cx+0] = byte(cr0 >> 2)
			cr422[cx+1] = byte(cr2 >> 2)
			cr422[cx+2] = byte(cr4 >> 2)
		}
	}

	// Downsample chroma from 4:2:2 to 4:2:0: average pairs of adjacent rows
	for row := 0; row < chromaH; row++ {
		topRow := row * 2
		botRow := topRow + 1
		topBase := topRow * chromaW
		botBase := botRow * chromaW
		outBase := row * chromaW

		for x := 0; x < chromaW; x++ {
			cbPlane[outBase+x] = byte((uint16(cb422[topBase+x]) + uint16(cb422[botBase+x]) + 1) >> 1)
			crPlane[outBase+x] = byte((uint16(cr422[topBase+x]) + uint16(cr422[botBase+x]) + 1) >> 1)
		}
	}

	return out, nil
}

// YUV420pToV210 converts a YUV420p (8-bit 4:2:0 planar) frame to V210 (10-bit 4:2:2 packed).
// Width must be divisible by 6 and height must be even.
//
// 8-bit to 10-bit conversion: left shift by 2.
// 4:2:0 to 4:2:2 chroma upsampling: duplicate each chroma row for the row below.
func YUV420pToV210(yuv []byte, width, height int) ([]byte, error) {
	if width%6 != 0 {
		return nil, fmt.Errorf("v210: width %d is not divisible by 6", width)
	}
	if height%2 != 0 || height < 2 {
		return nil, fmt.Errorf("v210: height %d must be even and >= 2", height)
	}

	ySize := width * height
	chromaW := width / 2
	chromaH := height / 2
	cSize := chromaW * chromaH
	requiredSize := ySize + 2*cSize
	if len(yuv) < requiredSize {
		return nil, fmt.Errorf("v210: YUV420p buffer size %d too small, need %d for %dx%d", len(yuv), requiredSize, width, height)
	}

	yPlane := yuv[:ySize]
	cbPlane := yuv[ySize : ySize+cSize]
	crPlane := yuv[ySize+cSize:]

	stride := V210LineStride(width)
	out := make([]byte, stride*height)

	for row := 0; row < height; row++ {
		rowBase := row * stride
		yRow := row * width
		// 4:2:0 chroma row index: row/2
		cRow := (row / 2) * chromaW

		for group := 0; group < width/6; group++ {
			px := yRow + group*6
			cx := cRow + group*3

			// Read Y values and convert 8-bit to 10-bit
			y0 := uint32(yPlane[px+0]) << 2
			y1 := uint32(yPlane[px+1]) << 2
			y2 := uint32(yPlane[px+2]) << 2
			y3 := uint32(yPlane[px+3]) << 2
			y4 := uint32(yPlane[px+4]) << 2
			y5 := uint32(yPlane[px+5]) << 2

			// Read chroma values (upsampled: duplicate 4:2:0 row for both even/odd rows)
			// and convert 8-bit to 10-bit
			cb0 := uint32(cbPlane[cx+0]) << 2
			cb2 := uint32(cbPlane[cx+1]) << 2
			cb4 := uint32(cbPlane[cx+2]) << 2
			cr0 := uint32(crPlane[cx+0]) << 2
			cr2 := uint32(crPlane[cx+1]) << 2
			cr4 := uint32(crPlane[cx+2]) << 2

			// Pack into V210 words
			// Word 0: Cb0, Y0, Cr0
			w0 := (cb0 & 0x3FF) | ((y0 & 0x3FF) << 10) | ((cr0 & 0x3FF) << 20)
			// Word 1: Y1, Cb2, Y2
			w1 := (y1 & 0x3FF) | ((cb2 & 0x3FF) << 10) | ((y2 & 0x3FF) << 20)
			// Word 2: Cr2, Y3, Cb4
			w2 := (cr2 & 0x3FF) | ((y3 & 0x3FF) << 10) | ((cb4 & 0x3FF) << 20)
			// Word 3: Y4, Cr4, Y5
			w3 := (y4 & 0x3FF) | ((cr4 & 0x3FF) << 10) | ((y5 & 0x3FF) << 20)

			offset := rowBase + group*16
			binary.LittleEndian.PutUint32(out[offset:], w0)
			binary.LittleEndian.PutUint32(out[offset+4:], w1)
			binary.LittleEndian.PutUint32(out[offset+8:], w2)
			binary.LittleEndian.PutUint32(out[offset+12:], w3)
		}
	}

	return out, nil
}
