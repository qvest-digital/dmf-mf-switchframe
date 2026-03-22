//go:build cgo && darwin

package gpu

/*
#include "metal_bridge.h"
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"
)

// GaussianBlur applies a 2-pass separable Gaussian blur to an NV12 GPU frame.
// radius controls the blur strength (1..50). A larger radius means more blur.
// dst and src must have the same dimensions. dst may alias src.
func GaussianBlur(ctx *Context, dst, src *GPUFrame, radius int) error {
	if ctx == nil || ctx.mtl == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if radius < 1 {
		return fmt.Errorf("gpu: gaussian blur: radius must be >= 1, got %d", radius)
	}
	if radius > 50 {
		return fmt.Errorf("gpu: gaussian blur: radius must be <= 50, got %d", radius)
	}

	mtl := ctx.mtl

	// Compute Gaussian weights on CPU
	weights := gaussianWeights(radius)

	// Upload weights to a small Metal buffer
	weightsBuf, err := mtl.allocBuffer(len(weights) * 4)
	if err != nil {
		return fmt.Errorf("gpu: gaussian blur: alloc weights: %w", err)
	}
	defer C.metal_buffer_free(weightsBuf)

	// Copy weights into the buffer
	wPtr := C.metal_buffer_contents(weightsBuf)
	wSlice := unsafe.Slice((*float32)(wPtr), len(weights))
	copy(wSlice, weights)

	// Ensure cached temp float buffer: pitch * height * 3/2 floats (covers Y + UV)
	tmpFloats := src.Pitch * src.Height * 3 / 2
	if err := mtl.ensureBlurTemp(tmpFloats); err != nil {
		return fmt.Errorf("gpu: gaussian blur: %w", err)
	}
	tmpBuf := mtl.blurTmpBuf

	// Get pipelines
	hPipeline, err := mtl.getPipeline("gaussian_blur_h")
	if err != nil {
		return fmt.Errorf("gpu: gaussian blur h pipeline: %w", err)
	}
	vPipeline, err := mtl.getPipeline("gaussian_blur_v")
	if err != nil {
		return fmt.Errorf("gpu: gaussian blur v pipeline: %w", err)
	}
	uvHPipeline, err := mtl.getPipeline("gaussian_blur_uv_h")
	if err != nil {
		return fmt.Errorf("gpu: gaussian blur uv h pipeline: %w", err)
	}
	uvVPipeline, err := mtl.getPipeline("gaussian_blur_uv_v")
	if err != nil {
		return fmt.Errorf("gpu: gaussian blur uv v pipeline: %w", err)
	}

	// --- Y plane blur ---
	yParams := C.MetalBlurParams{
		width:  C.uint32_t(src.Width),
		height: C.uint32_t(src.Height),
		pitch:  C.uint32_t(src.Pitch),
		radius: C.uint32_t(radius),
	}

	// Pass 1: horizontal (src Y -> tmpBuf)
	rc := C.metal_gaussian_blur_h(mtl.queue, hPipeline, tmpBuf, src.MetalBuf, weightsBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: gaussian blur h Y failed: %d", rc)
	}

	// Pass 2: vertical (tmpBuf -> dst Y)
	rc = C.metal_gaussian_blur_v(mtl.queue, vPipeline, dst.MetalBuf, tmpBuf, weightsBuf, &yParams)
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: gaussian blur v Y failed: %d", rc)
	}

	// --- UV plane blur ---
	chromaH := src.Height / 2
	uvParams := C.MetalBlurParams{
		width:  C.uint32_t(src.Width), // full byte width for UV indexing
		height: C.uint32_t(chromaH),
		pitch:  C.uint32_t(src.Pitch),
		radius: C.uint32_t(radius),
	}

	// UV offsets
	srcUVOffset := C.int64_t(src.Pitch * src.Height)
	dstUVOffset := C.int64_t(dst.Pitch * dst.Height)
	// tmpBuf UV offset: same layout as NV12 but in floats (4 bytes each)
	tmpUVOffset := C.int64_t(src.Pitch * src.Height * 4) // float offset in bytes

	// Pass 3: horizontal UV (src UV -> tmpBuf UV)
	chromaW := src.Width / 2
	uvHBufs := [3]C.MetalBufferRef{tmpBuf, src.MetalBuf, weightsBuf}
	uvHOffsets := [3]C.int64_t{tmpUVOffset, srcUVOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvHPipeline,
		&uvHBufs[0], &uvHOffsets[0], 3,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 3,
		C.uint32_t(chromaW), C.uint32_t(chromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: gaussian blur h UV failed: %d", rc)
	}

	// Pass 4: vertical UV (tmpBuf UV -> dst UV)
	uvVBufs := [3]C.MetalBufferRef{dst.MetalBuf, tmpBuf, weightsBuf}
	uvVOffsets := [3]C.int64_t{dstUVOffset, tmpUVOffset, 0}
	rc = C.metal_dispatch_2d_offset(mtl.queue, uvVPipeline,
		&uvVBufs[0], &uvVOffsets[0], 3,
		unsafe.Pointer(&uvParams), C.size_t(unsafe.Sizeof(uvParams)), 3,
		C.uint32_t(chromaW), C.uint32_t(chromaH))
	if rc != C.METAL_SUCCESS {
		return fmt.Errorf("gpu: gaussian blur v UV failed: %d", rc)
	}

	return nil
}

// gaussianWeights computes normalized Gaussian kernel weights for the given radius.
// Returns 2*radius+1 weights.
func gaussianWeights(radius int) []float32 {
	n := 2*radius + 1
	weights := make([]float32, n)
	sigma := math.Max(float64(radius)/3.0, 0.5)
	var sum float64
	for i := -radius; i <= radius; i++ {
		w := math.Exp(-float64(i*i) / (2 * sigma * sigma))
		weights[i+radius] = float32(w)
		sum += w
	}
	// Normalize to sum to 1.0
	for i := range weights {
		weights[i] = float32(float64(weights[i]) / sum)
	}
	return weights
}
