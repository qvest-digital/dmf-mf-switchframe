package mxl

import (
	"fmt"
	"sync"

	"github.com/zsiec/switchframe/server/mxl/v210asm"
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
// Width and height must be even.
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
	ySize := width * height
	chromaW := width / 2
	chromaH := height / 2
	cSize := chromaW * chromaH
	out := make([]byte, ySize+2*cSize)

	cb422Tmp := make([]byte, chromaW*2)
	cr422Tmp := make([]byte, chromaW*2)

	return out, V210ToYUV420pInto(v210, out, cb422Tmp, cr422Tmp, width, height)
}

// V210ToYUV420pInto converts V210 to YUV420p writing into caller-provided buffers.
// out must be at least ySize + 2*cSize bytes (w*h + 2*(w/2)*(h/2)).
// cb422Tmp and cr422Tmp must each be at least (w/2)*2 bytes (two rows of 4:2:2 chroma).
//
// The conversion fuses V210 unpacking and 4:2:2→4:2:0 chroma downsampling
// into a single pass that processes two rows at a time, averaging their chroma
// values directly into the 4:2:0 output without full-frame intermediate buffers.
func V210ToYUV420pInto(v210, out, cb422Tmp, cr422Tmp []byte, width, height int) error {
	if err := v210CheckWidth(width); err != nil {
		return err
	}
	if height%2 != 0 || height < 2 {
		return fmt.Errorf("v210: height %d must be even and >= 2", height)
	}

	stride := V210LineStride(width)
	requiredSize := stride * height
	if len(v210) < requiredSize {
		return fmt.Errorf("v210: buffer size %d too small, need %d for %dx%d", len(v210), requiredSize, width, height)
	}

	groups, tail := width/6, width%6
	ySize := width * height
	chromaW := width / 2
	chromaH := height / 2
	cSize := chromaW * chromaH
	outSize := ySize + 2*cSize

	if len(out) < outSize {
		return fmt.Errorf("v210: output buffer size %d too small, need %d", len(out), outSize)
	}
	if len(cb422Tmp) < chromaW*2 {
		return fmt.Errorf("v210: cb422Tmp buffer size %d too small, need %d", len(cb422Tmp), chromaW*2)
	}
	if len(cr422Tmp) < chromaW*2 {
		return fmt.Errorf("v210: cr422Tmp buffer size %d too small, need %d", len(cr422Tmp), chromaW*2)
	}

	yPlane := out[:ySize]
	cbPlane := out[ySize : ySize+cSize]
	crPlane := out[ySize+cSize:]

	// Process two rows at a time: unpack both rows' V210 data, then
	// downsample chroma by averaging the two rows' 4:2:2 values directly
	// into 4:2:0 output. Only 2 rows of temp chroma are needed at a time.
	cbTop := cb422Tmp[:chromaW]
	cbBot := cb422Tmp[chromaW : chromaW*2]
	crTop := cr422Tmp[:chromaW]
	crBot := cr422Tmp[chromaW : chromaW*2]

	for rowPair := 0; rowPair < chromaH; rowPair++ {
		topRow := rowPair * 2
		botRow := topRow + 1

		// Unpack top row
		v210asm.V210UnpackRow(&yPlane[topRow*width], &cbTop[0], &crTop[0], &v210[topRow*stride], groups)
		// Unpack bottom row
		v210asm.V210UnpackRow(&yPlane[botRow*width], &cbBot[0], &crBot[0], &v210[botRow*stride], groups)
		if tail != 0 {
			unpackV210Tail8(yPlane[topRow*width+groups*6:], cbTop[groups*3:], crTop[groups*3:], v210[topRow*stride+groups*16:], tail)
			unpackV210Tail8(yPlane[botRow*width+groups*6:], cbBot[groups*3:], crBot[groups*3:], v210[botRow*stride+groups*16:], tail)
		}

		// Downsample: average top and bottom chroma rows → 4:2:0
		dstCb := cbPlane[rowPair*chromaW:]
		v210asm.ChromaVAvg(&dstCb[0], &cbTop[0], &cbBot[0], chromaW)
		dstCr := crPlane[rowPair*chromaW:]
		v210asm.ChromaVAvg(&dstCr[0], &crTop[0], &crBot[0], chromaW)
	}

	return nil
}

// YUV420pToV210 converts a YUV420p (8-bit 4:2:0 planar) frame to V210 (10-bit 4:2:2 packed).
// Width and height must be even.
//
// 8-bit to 10-bit conversion: left shift by 2.
// 4:2:0 to 4:2:2 chroma upsampling: duplicate each chroma row for the row below.
// V210BufSize returns the required buffer size for V210 output at the given dimensions.
func V210BufSize(width, height int) int {
	return V210LineStride(width) * height
}

func YUV420pToV210(yuv []byte, width, height int) ([]byte, error) {
	stride := V210LineStride(width)
	out := make([]byte, stride*height)
	return out, YUV420pToV210Into(yuv, out, width, height)
}

// YUV420pToV210Into converts YUV420p to V210 writing into a caller-provided buffer.
// out must be at least V210LineStride(width)*height bytes.
func YUV420pToV210Into(yuv, out []byte, width, height int) error {
	if err := v210CheckWidth(width); err != nil {
		return err
	}
	if height%2 != 0 || height < 2 {
		return fmt.Errorf("v210: height %d must be even and >= 2", height)
	}

	ySize := width * height
	chromaW := width / 2
	chromaH := height / 2
	cSize := chromaW * chromaH
	requiredSize := ySize + 2*cSize
	if len(yuv) < requiredSize {
		return fmt.Errorf("v210: YUV420p buffer size %d too small, need %d for %dx%d", len(yuv), requiredSize, width, height)
	}

	groups, tail := width/6, width%6
	stride := V210LineStride(width)
	outSize := stride * height
	if len(out) < outSize {
		return fmt.Errorf("v210: output buffer size %d too small, need %d", len(out), outSize)
	}

	yPlane := yuv[:ySize]
	cbPlane := yuv[ySize : ySize+cSize]
	crPlane := yuv[ySize+cSize:]

	packRows := func(lo, hi int) {
		for row := lo; row < hi; row++ {
			yRow := yPlane[row*width:]
			cbRow := cbPlane[(row/2)*chromaW:]
			crRow := crPlane[(row/2)*chromaW:]
			v210Row := out[row*stride:]
			v210asm.V210PackRow(&v210Row[0], &yRow[0], &cbRow[0], &crRow[0], groups)
			if tail != 0 {
				packV210Tail8(v210Row[groups*16:], yRow[groups*6:], cbRow[groups*3:], crRow[groups*3:], tail)
			}
		}
	}
	bands := min(v210PackBands, height)
	var wg sync.WaitGroup
	for b := 1; b < bands; b++ {
		wg.Go(func() { packRows(height*b/bands, height*(b+1)/bands) })
	}
	packRows(0, height/bands)
	wg.Wait()

	return nil
}

// v210PackBands is how many row bands YUV420pToV210Into packs in parallel. The
// conversion runs on the program's frame path, once per frame: packed on
// one core, a 1080p frame took up to the whole frame period on a node
// whose memory other work kept busy, and a frame that misses its grain is
// dropped from the MXL output.
var v210PackBands = 8
