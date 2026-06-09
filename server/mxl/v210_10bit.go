package mxl

import (
	"encoding/binary"
	"fmt"
)

// V210ToYUV422P10 converts a V210 (10-bit 4:2:2 packed) frame to YUV422P10LE
// (10-bit 4:2:2 planar, little-endian uint16) format LOSSLESSLY.
//
// No bit depth loss: 10-bit values are preserved as-is in uint16 containers.
// No chroma subsampling loss: 4:2:2 chroma is preserved at full horizontal resolution.
//
// srcStride is the V210 line stride in bytes (from V210LineStride).
//
// Output layout (all samples are uint16 LE):
//   - Y plane:  w * h samples  (offset 0)
//   - Cb plane: w/2 * h samples (offset w*h*2)
//   - Cr plane: w/2 * h samples (offset w*h*2 + w/2*h*2)
//
// V210 packing per 6-pixel group (4 x 32-bit LE words):
//
//	Word 0: bits[9:0]=Cb0, bits[19:10]=Y0, bits[29:20]=Cr0
//	Word 1: bits[9:0]=Y1,  bits[19:10]=Cb2, bits[29:20]=Y2
//	Word 2: bits[9:0]=Cr2, bits[19:10]=Y3,  bits[29:20]=Cb4
//	Word 3: bits[9:0]=Y4,  bits[19:10]=Cr4, bits[29:20]=Y5
func V210ToYUV422P10(v210 []byte, srcStride, width, height int) ([]byte, error) {
	if width < 2 || width%2 != 0 {
		return nil, fmt.Errorf("v210_10bit: width %d must be even and >= 2", width)
	}
	if height < 1 {
		return nil, fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}
	out := make([]byte, width*height*4)
	if err := V210ToYUV422P10Into(v210, out, srcStride, width, height); err != nil {
		return nil, err
	}
	return out, nil
}

// V210ToYUV422P10Into is the allocation-free variant of V210ToYUV422P10: it
// writes the YUV422P10LE planes into out, which must be at least
// width*height*4 bytes. This conversion runs on the per-frame hot path
// (Source.processVideoGrain in Lossless10bit mode), so callers should reuse
// out across frames to avoid ~8 MB/frame of garbage at 1080p.
func V210ToYUV422P10Into(v210, out []byte, srcStride, width, height int) error {
	if width < 2 || width%2 != 0 {
		return fmt.Errorf("v210_10bit: width %d must be even and >= 2", width)
	}
	if height < 1 {
		return fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}

	requiredSize := srcStride * height
	if len(v210) < requiredSize {
		return fmt.Errorf("v210_10bit: V210 buffer size %d too small, need %d for %dx%d (stride %d)",
			len(v210), requiredSize, width, height, srcStride)
	}

	chromaW := width / 2
	ySize := width * height
	cbSize := chromaW * height
	crSize := chromaW * height
	totalBytes := (ySize + cbSize + crSize) * 2
	if len(out) < totalBytes {
		return fmt.Errorf("v210_10bit: output buffer size %d too small, need %d for %dx%d",
			len(out), totalBytes, width, height)
	}

	yPlane := out
	cbPlane := out[ySize*2:]
	crPlane := out[ySize*2+cbSize*2:]

	groups := width / 6
	remainder := width - groups*6 // 0, 2, or 4 pixels remaining

	for row := 0; row < height; row++ {
		v210Row := v210[row*srcStride:]
		yOff := row * width * 2
		cOff := row * chromaW * 2

		for g := 0; g < groups; g++ {
			wordOff := g * 16

			w0 := binary.LittleEndian.Uint32(v210Row[wordOff:])
			w1 := binary.LittleEndian.Uint32(v210Row[wordOff+4:])
			w2 := binary.LittleEndian.Uint32(v210Row[wordOff+8:])
			w3 := binary.LittleEndian.Uint32(v210Row[wordOff+12:])

			// Extract 6 Y samples
			yBase := yOff + g*6*2
			binary.LittleEndian.PutUint16(yPlane[yBase:], uint16((w0>>10)&0x3FF))    // Y0
			binary.LittleEndian.PutUint16(yPlane[yBase+2:], uint16(w1&0x3FF))        // Y1
			binary.LittleEndian.PutUint16(yPlane[yBase+4:], uint16((w1>>20)&0x3FF))  // Y2
			binary.LittleEndian.PutUint16(yPlane[yBase+6:], uint16((w2>>10)&0x3FF))  // Y3
			binary.LittleEndian.PutUint16(yPlane[yBase+8:], uint16(w3&0x3FF))        // Y4
			binary.LittleEndian.PutUint16(yPlane[yBase+10:], uint16((w3>>20)&0x3FF)) // Y5

			// Extract 3 Cb samples
			cBase := cOff + g*3*2
			binary.LittleEndian.PutUint16(cbPlane[cBase:], uint16(w0&0x3FF))         // Cb0
			binary.LittleEndian.PutUint16(cbPlane[cBase+2:], uint16((w1>>10)&0x3FF)) // Cb2
			binary.LittleEndian.PutUint16(cbPlane[cBase+4:], uint16((w2>>20)&0x3FF)) // Cb4

			// Extract 3 Cr samples
			binary.LittleEndian.PutUint16(crPlane[cBase:], uint16((w0>>20)&0x3FF))   // Cr0
			binary.LittleEndian.PutUint16(crPlane[cBase+2:], uint16(w2&0x3FF))       // Cr2
			binary.LittleEndian.PutUint16(crPlane[cBase+4:], uint16((w3>>10)&0x3FF)) // Cr4
		}

		// Handle remainder pixels (width not divisible by 6).
		// V210 still packs partial groups into full 4-word groups with padding.
		if remainder > 0 {
			wordOff := groups * 16
			yBase := yOff + groups*6*2
			cBase := cOff + groups*3*2

			w0 := binary.LittleEndian.Uint32(v210Row[wordOff:])
			// Always have at least 2 remainder pixels
			binary.LittleEndian.PutUint16(yPlane[yBase:], uint16((w0>>10)&0x3FF))  // Y0
			binary.LittleEndian.PutUint16(cbPlane[cBase:], uint16(w0&0x3FF))       // Cb0
			binary.LittleEndian.PutUint16(crPlane[cBase:], uint16((w0>>20)&0x3FF)) // Cr0

			if remainder >= 4 {
				w1 := binary.LittleEndian.Uint32(v210Row[wordOff+4:])
				binary.LittleEndian.PutUint16(yPlane[yBase+2:], uint16(w1&0x3FF))        // Y1
				binary.LittleEndian.PutUint16(yPlane[yBase+4:], uint16((w1>>20)&0x3FF))  // Y2
				binary.LittleEndian.PutUint16(cbPlane[cBase+2:], uint16((w1>>10)&0x3FF)) // Cb2

				w2 := binary.LittleEndian.Uint32(v210Row[wordOff+8:])
				binary.LittleEndian.PutUint16(yPlane[yBase+6:], uint16((w2>>10)&0x3FF)) // Y3
				binary.LittleEndian.PutUint16(crPlane[cBase+2:], uint16(w2&0x3FF))      // Cr2
			} else {
				// remainder == 2: just Y0, Y1, Cb0, Cr0
				w1 := binary.LittleEndian.Uint32(v210Row[wordOff+4:])
				binary.LittleEndian.PutUint16(yPlane[yBase+2:], uint16(w1&0x3FF)) // Y1
			}
		}
	}

	return nil
}

// YUV422P10ToV210 converts a YUV422P10LE (10-bit 4:2:2 planar) frame to
// V210 (10-bit 4:2:2 packed) format LOSSLESSLY.
//
// Width must be divisible by 6 (V210 group constraint) and even (4:2:2).
// Height must be >= 1.
//
// Input layout (all samples are uint16 LE):
//   - Y plane:  w * h samples  (offset 0)
//   - Cb plane: w/2 * h samples (offset w*h*2)
//   - Cr plane: w/2 * h samples (offset w*h*2 + w/2*h*2)
func YUV422P10ToV210(yuv []byte, width, height int) ([]byte, error) {
	if width < 6 || width%6 != 0 {
		return nil, fmt.Errorf("v210_10bit: width %d must be divisible by 6 and >= 6", width)
	}
	if height < 1 {
		return nil, fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}
	out := make([]byte, V210LineStride(width)*height)
	if err := YUV422P10ToV210Into(yuv, out, width, height); err != nil {
		return nil, err
	}
	return out, nil
}

// YUV422P10ToV210Into is the allocation-free variant of YUV422P10ToV210: it
// packs the V210 frame into out, which must be at least
// V210LineStride(width)*height bytes. This conversion runs on the per-frame
// hot path (Writer.WriteVideo10bit), so callers should reuse out across
// frames to avoid ~5.5 MB/frame of garbage at 1080p.
func YUV422P10ToV210Into(yuv, out []byte, width, height int) error {
	if width < 6 || width%6 != 0 {
		return fmt.Errorf("v210_10bit: width %d must be divisible by 6 and >= 6", width)
	}
	if height < 1 {
		return fmt.Errorf("v210_10bit: height %d must be >= 1", height)
	}

	chromaW := width / 2
	ySize := width * height
	cbSize := chromaW * height
	crSize := chromaW * height
	requiredBytes := (ySize + cbSize + crSize) * 2
	if len(yuv) < requiredBytes {
		return fmt.Errorf("v210_10bit: YUV422P10 buffer size %d too small, need %d for %dx%d",
			len(yuv), requiredBytes, width, height)
	}

	stride := V210LineStride(width)
	if len(out) < stride*height {
		return fmt.Errorf("v210_10bit: output buffer size %d too small, need %d for %dx%d",
			len(out), stride*height, width, height)
	}
	groups := width / 6

	yPlane := yuv
	cbPlane := yuv[ySize*2:]
	crPlane := yuv[ySize*2+cbSize*2:]

	for row := 0; row < height; row++ {
		yOff := row * width * 2
		cOff := row * chromaW * 2
		v210Row := out[row*stride:]

		for g := 0; g < groups; g++ {
			yBase := yOff + g*6*2
			cBase := cOff + g*3*2

			// Read 6 Y samples
			y0 := uint32(binary.LittleEndian.Uint16(yPlane[yBase:]))
			y1 := uint32(binary.LittleEndian.Uint16(yPlane[yBase+2:]))
			y2 := uint32(binary.LittleEndian.Uint16(yPlane[yBase+4:]))
			y3 := uint32(binary.LittleEndian.Uint16(yPlane[yBase+6:]))
			y4 := uint32(binary.LittleEndian.Uint16(yPlane[yBase+8:]))
			y5 := uint32(binary.LittleEndian.Uint16(yPlane[yBase+10:]))

			// Read 3 Cb samples
			cb0 := uint32(binary.LittleEndian.Uint16(cbPlane[cBase:]))
			cb2 := uint32(binary.LittleEndian.Uint16(cbPlane[cBase+2:]))
			cb4 := uint32(binary.LittleEndian.Uint16(cbPlane[cBase+4:]))

			// Read 3 Cr samples
			cr0 := uint32(binary.LittleEndian.Uint16(crPlane[cBase:]))
			cr2 := uint32(binary.LittleEndian.Uint16(crPlane[cBase+2:]))
			cr4 := uint32(binary.LittleEndian.Uint16(crPlane[cBase+4:]))

			// Pack into 4 V210 words
			wordOff := g * 16
			binary.LittleEndian.PutUint32(v210Row[wordOff:],
				(cb0&0x3FF)|((y0&0x3FF)<<10)|((cr0&0x3FF)<<20))
			binary.LittleEndian.PutUint32(v210Row[wordOff+4:],
				(y1&0x3FF)|((cb2&0x3FF)<<10)|((y2&0x3FF)<<20))
			binary.LittleEndian.PutUint32(v210Row[wordOff+8:],
				(cr2&0x3FF)|((y3&0x3FF)<<10)|((cb4&0x3FF)<<20))
			binary.LittleEndian.PutUint32(v210Row[wordOff+12:],
				(y4&0x3FF)|((cr4&0x3FF)<<10)|((y5&0x3FF)<<20))
		}
	}

	return nil
}
