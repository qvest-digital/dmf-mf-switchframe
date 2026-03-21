//go:build darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Upload transfers a CPU YUV420p frame to a GPU NV12 frame.
//
// On Apple Silicon with unified memory, this writes NV12 data directly
// into the frame's Metal buffer via CPU. Each GPUFrame has its own
// MTLBuffer (StorageModeShared), so concurrent uploads to different
// frames are inherently race-free — no staging buffers needed.
//
// The YUV420p→NV12 conversion (CbCr interleave) is done in C where
// Clang auto-vectorizes the inner loop with ARM64 NEON vst2 instructions.
func Upload(ctx *Context, frame *GPUFrame, yuv []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	expectedSize := ySize + cbSize + crSize
	if len(yuv) < expectedSize {
		return fmt.Errorf("gpu: upload: YUV buffer too small: %d < %d", len(yuv), expectedSize)
	}

	// Apple Silicon unified memory: write NV12 directly into the frame's
	// Metal buffer via CPU. No staging buffers, no GPU kernel needed.
	// Each frame has its own memory, so concurrent uploads to different
	// frames are inherently race-free.
	C.metal_yuv420p_to_nv12_cpu(
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

// FillBlack fills a GPU NV12 frame with black (Y=16, Cb=128, Cr=128 for BT.709 limited range).
func FillBlack(ctx *Context, frame *GPUFrame) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("nv12_fill")
	if err != nil {
		return fmt.Errorf("gpu: fill black: %w", err)
	}

	params := C.MetalFillParams{
		width:  C.uint32_t(frame.Width),
		height: C.uint32_t(frame.Height),
		pitch:  C.uint32_t(frame.Pitch),
		yVal:   C.uint8_t(16),
		cbVal:  C.uint8_t(128),
		crVal:  C.uint8_t(128),
	}

	rc := C.metal_nv12_fill(mtl.queue, pipeline, frame.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: fill black: kernel failed: %d", rc)
	}

	return nil
}
