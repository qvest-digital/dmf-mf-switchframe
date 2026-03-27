//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t launch_fuse_occlusion(
    uint8_t* output,
    const void* depth,
    const uint8_t* sam_mask,
    int width, int height,
    int depth_w, int depth_h,
    float depth_near, float softness,
    cudaStream_t stream);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// OcclusionFuseParams controls the depth-to-occlusion mapping.
type OcclusionFuseParams struct {
	DepthNear float32 // depth threshold for foreground (default 0.3)
	Softness  float32 // transition softness (default 0.1, range 0.01-0.5)
}

// DefaultOcclusionFuseParams returns sensible defaults for occlusion fusion.
func DefaultOcclusionFuseParams() OcclusionFuseParams {
	return OcclusionFuseParams{
		DepthNear: 0.3,
		Softness:  0.1,
	}
}

// FuseOcclusion combines a depth map and SAM segmentation mask into a single
// occlusion confidence map on the GPU. The output is uint8: 255 = definite
// foreground (don't overlay AR), 0 = background (safe to overlay).
//
// Parameters:
//   - output: device pointer to uint8 buffer (width * height bytes)
//   - depth: device pointer to float16 depth map (depthW * depthH), may be 0 if unavailable
//   - samMask: device pointer to uint8 SAM mask (width * height), may be 0 if unavailable
//   - width, height: output/SAM mask dimensions (full frame resolution)
//   - depthW, depthH: depth map dimensions (typically smaller, e.g. 512x288)
//   - params: depth threshold and softness controls
func FuseOcclusion(ctx *Context, output, depth, samMask uintptr,
	width, height, depthW, depthH int, params OcclusionFuseParams) error {
	if ctx == nil {
		return ErrGPUNotAvailable
	}
	if output == 0 {
		return fmt.Errorf("gpu: FuseOcclusion: output pointer is nil")
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("gpu: FuseOcclusion: invalid dimensions %dx%d", width, height)
	}

	// depth and samMask may be NULL (0) — the kernel handles both cases
	var depthPtr unsafe.Pointer
	if depth != 0 {
		depthPtr = unsafe.Pointer(depth)
		if depthW <= 0 || depthH <= 0 {
			return fmt.Errorf("gpu: FuseOcclusion: invalid depth dimensions %dx%d", depthW, depthH)
		}
	}

	var samPtr *C.uint8_t
	if samMask != 0 {
		samPtr = (*C.uint8_t)(unsafe.Pointer(samMask))
	}

	rc := C.launch_fuse_occlusion(
		(*C.uint8_t)(unsafe.Pointer(output)),
		depthPtr,
		samPtr,
		C.int(width), C.int(height),
		C.int(depthW), C.int(depthH),
		C.float(params.DepthNear), C.float(params.Softness),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: FuseOcclusion: kernel failed: %d", rc)
	}
	return ctx.Sync()
}
