//go:build cgo && darwin

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

// ColorGradeLUT3D applies a 3D LUT color grade to an NV12 frame in-place.
// The LUT must already be uploaded to GPU memory via UploadLUT3D.
// intensity controls blend: 0.0 = bypass, 1.0 = full grade.
func ColorGradeLUT3D(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("colorgrade_lut3d_nv12")
	if err != nil {
		return fmt.Errorf("gpu: colorgrade LUT3D NV12: %w", err)
	}

	type colorgradeNV12Params struct {
		width     uint32
		height    uint32
		pitch     uint32
		lutSize   int32
		intensity float32
	}
	params := colorgradeNV12Params{
		width:     uint32(frame.Width),
		height:    uint32(frame.Height),
		pitch:     uint32(frame.Pitch),
		lutSize:   int32(lutSize),
		intensity: intensity,
	}

	lutBuf := C.MetalBufferRef(unsafe.Pointer(lutDevPtr)) //nolint:govet // intentional Metal buffer pointer conversion
	bufs := [2]C.MetalBufferRef{frame.MetalBuf, lutBuf}
	offsets := [2]C.int64_t{0, 0}

	rc := C.metal_dispatch_2d_offset(mtl.queue, pipeline,
		&bufs[0], &offsets[0], 2,
		unsafe.Pointer(&params), C.size_t(unsafe.Sizeof(params)), 2,
		C.uint32_t(frame.Width), C.uint32_t(frame.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: colorgrade LUT3D NV12 failed: %d", rc)
	}
	return nil
}

// ColorGradeLUT3DP210 applies a 3D LUT color grade to a P210 (10-bit 4:2:2)
// frame in-place. Uses 10-bit limited-range BT.709 scaling:
//
//	Y:  [64, 940]  -> excursion 876
//	Cb: [64, 960]  -> centered at 512, excursion 896
//	Cr: [64, 960]  -> centered at 512, excursion 896
//
// The LUT must already be uploaded to GPU memory via UploadLUT3D.
// intensity controls blend: 0.0 = bypass, 1.0 = full grade.
func ColorGradeLUT3DP210(ctx *Context, frame *GPUFrame, lutDevPtr uintptr, lutSize int, intensity float32) error {
	if ctx == nil || ctx.mtl == nil || frame == nil {
		return ErrGPUNotAvailable
	}

	mtl := ctx.mtl
	pipeline, err := mtl.getPipeline("colorgrade_lut3d_p210")
	if err != nil {
		return fmt.Errorf("gpu: colorgrade LUT3D P210: %w", err)
	}

	type colorgradeP210Params struct {
		width     uint32
		height    uint32
		pitch     uint32
		lutSize   int32
		intensity float32
	}
	params := colorgradeP210Params{
		width:     uint32(frame.Width),
		height:    uint32(frame.Height),
		pitch:     uint32(frame.Pitch),
		lutSize:   int32(lutSize),
		intensity: intensity,
	}

	lutBuf := C.MetalBufferRef(unsafe.Pointer(lutDevPtr)) //nolint:govet // intentional Metal buffer pointer conversion
	bufs := [2]C.MetalBufferRef{frame.MetalBuf, lutBuf}
	offsets := [2]C.int64_t{0, 0}

	rc := C.metal_dispatch_2d_offset(mtl.queue, pipeline,
		&bufs[0], &offsets[0], 2,
		unsafe.Pointer(&params), C.size_t(unsafe.Sizeof(params)), 2,
		C.uint32_t(frame.Width), C.uint32_t(frame.Height))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: colorgrade LUT3D P210 failed: %d", rc)
	}
	return nil
}

// UploadLUT3D copies a flat float32 LUT array to a Metal buffer.
// Returns a handle (as uintptr) to the Metal buffer. Caller must call
// FreeLUT3D when done. Uses the system default Metal device for allocation
// (Apple Silicon returns the same device object on every call).
func UploadLUT3D(data []float32) (uintptr, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("gpu: empty LUT data")
	}

	byteSize := len(data) * 4 // float32 = 4 bytes
	buf := C.metal_buffer_alloc_default(C.size_t(byteSize))
	if buf == nil {
		return 0, fmt.Errorf("gpu: upload LUT alloc failed (size=%d)", byteSize)
	}

	C.memcpy(C.metal_buffer_contents(buf), unsafe.Pointer(&data[0]), C.size_t(byteSize))

	return uintptr(unsafe.Pointer(buf)), nil
}

// FreeLUT3D frees a LUT Metal buffer previously uploaded via UploadLUT3D.
func FreeLUT3D(devPtr uintptr) {
	if devPtr != 0 {
		C.metal_buffer_free(C.MetalBufferRef(unsafe.Pointer(devPtr))) //nolint:govet // intentional Metal buffer pointer conversion
	}
}
