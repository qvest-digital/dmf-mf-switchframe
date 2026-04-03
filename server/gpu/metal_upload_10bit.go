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

// Upload10 transfers a CPU YUV422P10LE frame to a GPU P210 frame.
//
// YUV422P10LE layout (planar): Y plane (w*h uint16 LE) + Cb plane (w/2*h uint16 LE) + Cr plane (w/2*h uint16 LE).
// P210 layout (semi-planar): Y plane (pitch*height bytes, uint16 per sample) + UV plane (pitch*height bytes, interleaved Cb/Cr uint16 pairs).
//
// On Apple Silicon with unified memory, this writes P210 data directly
// into the frame's Metal buffer via CPU. The Cb/Cr interleaving is done
// in C where Clang auto-vectorizes the inner loop.
func Upload10(ctx *Context, frame *GPUFrame, yuv422p10 []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	chromaW := width / 2
	ySize := width * height * 2    // uint16 per sample
	cbSize := chromaW * height * 2 // uint16 per sample, full height (4:2:2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv422p10) < expectedSize {
		return fmt.Errorf("gpu: upload10: YUV422P10LE buffer too small: %d < %d", len(yuv422p10), expectedSize)
	}

	C.metal_yuv422p10_to_p210_cpu(
		frame.contentsPtr(),
		C.int(frame.Pitch),
		unsafe.Pointer(&yuv422p10[0]),
		C.int(width),
		C.int(height),
	)

	return nil
}

// UploadYUV420PToP210 converts 8-bit YUV420P to 10-bit P210 in GPU unified memory.
// Bit-promotes each sample (<<2) and upsamples chroma from 4:2:0 half-height
// to 4:2:2 full-height by duplicating rows. No intermediate buffer needed
// on Apple Silicon unified memory.
func UploadYUV420PToP210(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}
	if width < 2 || height < 2 {
		return fmt.Errorf("gpu: UploadYUV420PToP210 requires dimensions >= 2x2 (got %dx%d)", width, height)
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv) < expectedSize {
		return fmt.Errorf("gpu: UploadYUV420PToP210: buffer too small: %d < %d", len(yuv), expectedSize)
	}

	C.metal_yuv420p_to_p210_cpu(
		frame.contentsPtr(),
		C.int(frame.Pitch),
		unsafe.Pointer(&yuv[0]),
		unsafe.Pointer(&yuv[ySize]),
		unsafe.Pointer(&yuv[ySize+cbSize]),
		C.int(width),
		C.int(height),
	)
	return nil
}
