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

// V210LineStride returns the byte stride for one line of V210 data.
func V210LineStride(width int) int {
	groups := (width + 5) / 6
	rawBytes := groups * 16
	return (rawBytes + 127) &^ 127
}

// V210ToNV12 converts a GPU-resident V210 buffer to an NV12 GPUFrame.
func V210ToNV12(ctx *Context, dst *GPUFrame, v210DevPtr uintptr, v210Stride, width, height int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}
	if height%2 != 0 {
		return fmt.Errorf("gpu: V210 height %d not even", height)
	}

	// V210 data is assumed to be in a Metal buffer already
	// This function would need the V210 data as a MetalBufferRef.
	// For now, return not available as the interface expects raw device pointers.
	return ErrGPUNotAvailable
}

// NV12ToV210 converts an NV12 GPUFrame to a GPU-resident V210 buffer.
func NV12ToV210(ctx *Context, v210DevPtr uintptr, v210Stride int, src *GPUFrame, width, height int) error {
	if ctx == nil || ctx.mtl == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if width%6 != 0 {
		return fmt.Errorf("gpu: V210 width %d not divisible by 6", width)
	}
	if height%2 != 0 {
		return fmt.Errorf("gpu: V210 height %d not even", height)
	}
	return ErrGPUNotAvailable
}

// UploadV210 uploads a CPU V210 buffer to GPU memory and converts to NV12.
func UploadV210(ctx *Context, dst *GPUFrame, v210 []byte, width, height int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil {
		return ErrGPUNotAvailable
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 buffer too small: %d < %d", len(v210), expected)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("v210_to_nv12")
	if err != nil {
		return fmt.Errorf("gpu: V210 upload: %w", err)
	}

	// Upload V210 data to a Metal buffer
	v210Buf, err := mtl.allocBuffer(expected)
	if err != nil {
		return fmt.Errorf("gpu: V210 upload alloc: %w", err)
	}
	defer C.metal_buffer_free(v210Buf)
	C.memcpy(C.metal_buffer_contents(v210Buf), unsafe.Pointer(&v210[0]), C.size_t(expected))

	params := C.MetalV210Params{
		width:        C.uint32_t(width),
		height:       C.uint32_t(height),
		nv12Pitch:    C.uint32_t(dst.Pitch),
		v210Stride32: C.uint32_t(v210Stride / 4),
	}

	rc := C.metal_v210_to_nv12(mtl.queue, pipeline, dst.MetalBuf, v210Buf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: V210→NV12 failed: %d", rc)
	}
	return nil
}

// DownloadV210 converts an NV12 GPUFrame to V210 and downloads to CPU.
func DownloadV210(ctx *Context, v210 []byte, src *GPUFrame, width, height int) error {
	if ctx == nil || ctx.mtl == nil || src == nil {
		return ErrGPUNotAvailable
	}

	v210Stride := V210LineStride(width)
	expected := v210Stride * height
	if len(v210) < expected {
		return fmt.Errorf("gpu: V210 download buffer too small: %d < %d", len(v210), expected)
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("nv12_to_v210")
	if err != nil {
		return fmt.Errorf("gpu: V210 download: %w", err)
	}

	v210Buf, err := mtl.allocBuffer(expected)
	if err != nil {
		return fmt.Errorf("gpu: V210 download alloc: %w", err)
	}
	defer C.metal_buffer_free(v210Buf)

	params := C.MetalV210Params{
		width:        C.uint32_t(width),
		height:       C.uint32_t(height),
		nv12Pitch:    C.uint32_t(src.Pitch),
		v210Stride32: C.uint32_t(v210Stride / 4),
	}

	rc := C.metal_nv12_to_v210(mtl.queue, pipeline, v210Buf, src.MetalBuf, &params)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: NV12→V210 failed: %d", rc)
	}

	C.memcpy(unsafe.Pointer(&v210[0]), C.metal_buffer_contents(v210Buf), C.size_t(expected))
	return nil
}
