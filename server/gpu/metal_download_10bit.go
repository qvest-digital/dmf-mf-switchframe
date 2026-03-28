//go:build cgo && darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Download10 transfers a GPU P210 frame to a CPU YUV422P10LE buffer.
//
// P210 layout (semi-planar): Y plane (pitch*height bytes, uint16) + UV plane (pitch*height bytes, interleaved Cb/Cr uint16 pairs).
// YUV422P10LE layout (planar): Y plane (w*h uint16 LE) + Cb plane (w/2*h uint16 LE) + Cr plane (w/2*h uint16 LE).
//
// On Apple Silicon with unified memory, this reads P210 data directly
// from the frame's Metal buffer via CPU and deinterleaves UV to planar Cb/Cr.
func Download10(ctx *Context, frame *GPUFrame, dst []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	chromaW := width / 2
	ySize := width * height * 2
	cbSize := chromaW * height * 2
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(dst) < expectedSize {
		return fmt.Errorf("gpu: download10: YUV422P10LE buffer too small: %d < %d", len(dst), expectedSize)
	}

	C.metal_p210_to_yuv422p10_cpu(
		frame.contentsPtr(),
		C.int(frame.Pitch),
		unsafe.Pointer(&dst[0]),
		C.int(width),
		C.int(height),
	)

	return nil
}
