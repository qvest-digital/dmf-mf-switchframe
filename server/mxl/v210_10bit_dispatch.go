package mxl

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// v210ToYUV422P10SIMD converts V210 to YUV422P10LE using SIMD-accelerated row kernels.
// Output is bit-identical to V210ToYUV422P10 (the Go reference implementation).
func v210ToYUV422P10SIMD(v210 []byte, srcStride, width, height int) ([]byte, error) {
	if width < 2 || width%2 != 0 {
		return nil, fmt.Errorf("v210_10bit: width %d must be even and >= 2", width)
	}
	if height < 1 {
		return nil, fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}

	requiredSize := srcStride * height
	if len(v210) < requiredSize {
		return nil, fmt.Errorf("v210_10bit: V210 buffer size %d too small, need %d for %dx%d (stride %d)",
			len(v210), requiredSize, width, height, srcStride)
	}

	chromaW := width / 2
	ySize := width * height
	cbSize := chromaW * height
	crSize := chromaW * height
	totalBytes := (ySize + cbSize + crSize) * 2
	out := make([]byte, totalBytes)

	// Create uint16 slice views directly over the output byte buffer.
	// This avoids allocating separate intermediate buffers.
	yPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&out[0])), ySize)
	cbPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&out[ySize*2])), cbSize)
	crPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&out[ySize*2+cbSize*2])), crSize)

	groups := width / 6
	remainder := width - groups*6

	for row := 0; row < height; row++ {
		v210Row := v210[row*srcStride:]
		yOff := row * width
		cOff := row * chromaW

		if groups > 0 {
			v210UnpackRow10(yPlane[yOff:], cbPlane[cOff:], crPlane[cOff:], v210Row, groups)
		}

		// Handle remainder pixels (width not divisible by 6).
		if remainder > 0 {
			wordOff := groups * 16
			yBase := yOff + groups*6
			cBase := cOff + groups*3

			w0 := binary.LittleEndian.Uint32(v210Row[wordOff:])
			yPlane[yBase] = uint16((w0 >> 10) & 0x3FF)
			cbPlane[cBase] = uint16(w0 & 0x3FF)
			crPlane[cBase] = uint16((w0 >> 20) & 0x3FF)

			if remainder >= 4 {
				w1 := binary.LittleEndian.Uint32(v210Row[wordOff+4:])
				yPlane[yBase+1] = uint16(w1 & 0x3FF)
				yPlane[yBase+2] = uint16((w1 >> 20) & 0x3FF)
				cbPlane[cBase+1] = uint16((w1 >> 10) & 0x3FF)

				w2 := binary.LittleEndian.Uint32(v210Row[wordOff+8:])
				yPlane[yBase+3] = uint16((w2 >> 10) & 0x3FF)
				crPlane[cBase+1] = uint16(w2 & 0x3FF)
			} else {
				// remainder == 2: Y0, Y1, Cb0, Cr0
				w1 := binary.LittleEndian.Uint32(v210Row[wordOff+4:])
				yPlane[yBase+1] = uint16(w1 & 0x3FF)
			}
		}
	}

	return out, nil
}

// yuv422P10ToV210SIMD converts YUV422P10LE to V210 using SIMD-accelerated row kernels.
// Output is bit-identical to YUV422P10ToV210 (the Go reference implementation).
func yuv422P10ToV210SIMD(yuv []byte, width, height int) ([]byte, error) {
	if width < 6 || width%6 != 0 {
		return nil, fmt.Errorf("v210_10bit: width %d must be divisible by 6 and >= 6", width)
	}
	if height < 1 {
		return nil, fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}

	chromaW := width / 2
	ySize := width * height
	cbSize := chromaW * height
	crSize := chromaW * height
	requiredBytes := (ySize + cbSize + crSize) * 2
	if len(yuv) < requiredBytes {
		return nil, fmt.Errorf("v210_10bit: YUV422P10 buffer size %d too small, need %d for %dx%d",
			len(yuv), requiredBytes, width, height)
	}

	stride := V210LineStride(width)
	out := make([]byte, stride*height)
	groups := width / 6

	// Create uint16 slice views directly over the input byte buffer.
	yPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[0])), ySize)
	cbPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[ySize*2])), cbSize)
	crPlane := unsafe.Slice((*uint16)(unsafe.Pointer(&yuv[ySize*2+cbSize*2])), crSize)

	for row := 0; row < height; row++ {
		yOff := row * width
		cOff := row * chromaW
		v210Row := out[row*stride:]

		v210PackRow10(v210Row, yPlane[yOff:], cbPlane[cOff:], crPlane[cOff:], groups)
	}

	return out, nil
}
