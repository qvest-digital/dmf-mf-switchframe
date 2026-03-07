package mxl

import (
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

	groups := width / 6
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
		v210Row := v210[row*stride:]
		yRow := yPlane[row*width:]
		cbRow := cb422[row*chromaW:]
		crRow := cr422[row*chromaW:]
		v210UnpackRow(&yRow[0], &cbRow[0], &crRow[0], &v210Row[0], groups)
	}

	// Downsample chroma from 4:2:2 to 4:2:0: average pairs of adjacent rows
	for row := 0; row < chromaH; row++ {
		topCb := cb422[(row*2)*chromaW:]
		botCb := cb422[(row*2+1)*chromaW:]
		dstCb := cbPlane[row*chromaW:]
		chromaVAvg(&dstCb[0], &topCb[0], &botCb[0], chromaW)

		topCr := cr422[(row*2)*chromaW:]
		botCr := cr422[(row*2+1)*chromaW:]
		dstCr := crPlane[row*chromaW:]
		chromaVAvg(&dstCr[0], &topCr[0], &botCr[0], chromaW)
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

	groups := width / 6
	stride := V210LineStride(width)
	out := make([]byte, stride*height)

	for row := 0; row < height; row++ {
		yRow := yPlane[row*width:]
		// 4:2:0 chroma row index: row/2
		cbRow := cbPlane[(row/2)*chromaW:]
		crRow := crPlane[(row/2)*chromaW:]
		v210Row := out[row*stride:]
		v210PackRow(&v210Row[0], &yRow[0], &cbRow[0], &crRow[0], groups)
	}

	return out, nil
}
