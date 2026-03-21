//go:build darwin

package gpu

/*
#include "metal_bridge.h"
#include <string.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Upload transfers a CPU YUV420p frame to a GPU NV12 frame.
//
// On Apple Silicon with unified memory, this is a memcpy into the
// Metal buffer (CPU and GPU share the same physical RAM), followed
// by a compute kernel to convert YUV420p planar to NV12 interleaved.
// Much faster than CUDA (no PCIe transfer).
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

	mtl := ctx.mtl
	mtl.mu.Lock()
	defer mtl.mu.Unlock()

	// Allocate temporary staging buffers for the 3 planar inputs.
	// On Metal with unified memory, these are just regular buffers that
	// both CPU and GPU can access.
	yBuf, err := mtl.allocBuffer(ySize)
	if err != nil {
		return fmt.Errorf("gpu: upload: alloc Y staging: %w", err)
	}
	defer C.metal_buffer_free(yBuf)

	cbBuf, err := mtl.allocBuffer(cbSize)
	if err != nil {
		return fmt.Errorf("gpu: upload: alloc Cb staging: %w", err)
	}
	defer C.metal_buffer_free(cbBuf)

	crBuf, err := mtl.allocBuffer(crSize)
	if err != nil {
		return fmt.Errorf("gpu: upload: alloc Cr staging: %w", err)
	}
	defer C.metal_buffer_free(crBuf)

	// Copy planar data into Metal buffers (zero-copy on unified memory —
	// this is just a memcpy within the same address space)
	C.memcpy(C.metal_buffer_contents(yBuf), unsafe.Pointer(&yuv[0]), C.size_t(ySize))
	C.memcpy(C.metal_buffer_contents(cbBuf), unsafe.Pointer(&yuv[ySize]), C.size_t(cbSize))
	C.memcpy(C.metal_buffer_contents(crBuf), unsafe.Pointer(&yuv[ySize+cbSize]), C.size_t(crSize))

	// Launch conversion kernel: YUV420p → NV12
	pipeline, err := mtl.getPipeline("yuv420p_to_nv12")
	if err != nil {
		return fmt.Errorf("gpu: upload: %w", err)
	}

	params := C.MetalConvertParams{
		width:    C.uint32_t(width),
		height:   C.uint32_t(height),
		nv12Pitch: C.uint32_t(frame.Pitch),
		srcStride: C.uint32_t(width),
	}

	rc := C.metal_yuv420p_to_nv12(mtl.queue, pipeline, yBuf, cbBuf, crBuf, frame.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: upload: yuv420p_to_nv12 kernel failed: %d", rc)
	}

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
