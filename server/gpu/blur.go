//go:build cgo && cuda

package gpu

/*
#include <cuda.h>
#include <cuda_runtime.h>
#include <stdint.h>

cudaError_t gaussian_blur_nv12(
    uint8_t* dst, const uint8_t* src,
    float* tmpBuf, const float* weights,
    int width, int height, int pitch, int radius,
    cudaStream_t stream);

cudaError_t gaussian_blur_p210(
    uint16_t* dst, const uint16_t* src,
    float* tmpBuf, const float* weights,
    int width, int height, int pitch, int radius,
    cudaStream_t stream);
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
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if radius < 1 {
		return fmt.Errorf("gpu: gaussian blur: radius must be >= 1, got %d", radius)
	}
	if radius > 50 {
		return fmt.Errorf("gpu: gaussian blur: radius must be <= 50, got %d", radius)
	}

	// Compute Gaussian weights on CPU
	weights := gaussianWeights(radius)

	// Upload weights to device
	var dWeights unsafe.Pointer
	weightsSize := len(weights) * 4
	if rc := C.cudaMalloc(&dWeights, C.size_t(weightsSize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur: alloc weights failed: %d", rc)
	}
	defer C.cudaFree(dWeights)

	if rc := C.cudaMemcpyAsync(dWeights, unsafe.Pointer(&weights[0]),
		C.size_t(weightsSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur: copy weights failed: %d", rc)
	}

	// Ensure cached temp float buffer: pitch * height * 3/2 floats
	tmpFloats := src.Pitch * src.Height * 3 / 2
	if err := ctx.ensureBlurTemp(tmpFloats); err != nil {
		return fmt.Errorf("gpu: gaussian blur: %w", err)
	}

	rc := C.gaussian_blur_nv12(
		(*C.uint8_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint8_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		ctx.blurTmp,
		(*C.float)(dWeights),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch), C.int(radius),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur sync failed: %d", syncRc)
	}
	return nil
}

// GaussianBlurP210 applies a 2-pass separable Gaussian blur to a P210 GPU frame.
// radius controls the blur strength (1..50). A larger radius means more blur.
// dst and src must have the same dimensions. dst may alias src.
// P210 has uint16 samples (0-1023) and full-height UV (4:2:2).
func GaussianBlurP210(ctx *Context, dst, src *GPUFrame, radius int) error {
	if ctx == nil || dst == nil || src == nil {
		return ErrGPUNotAvailable
	}
	if radius < 1 {
		return fmt.Errorf("gpu: gaussian blur p210: radius must be >= 1, got %d", radius)
	}
	if radius > 50 {
		return fmt.Errorf("gpu: gaussian blur p210: radius must be <= 50, got %d", radius)
	}

	// Compute Gaussian weights on CPU
	weights := gaussianWeights(radius)

	// Upload weights to device
	var dWeights unsafe.Pointer
	weightsSize := len(weights) * 4
	if rc := C.cudaMalloc(&dWeights, C.size_t(weightsSize)); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur p210: alloc weights failed: %d", rc)
	}
	defer C.cudaFree(dWeights)

	if rc := C.cudaMemcpyAsync(dWeights, unsafe.Pointer(&weights[0]),
		C.size_t(weightsSize), C.cudaMemcpyHostToDevice, ctx.stream); rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur p210: copy weights failed: %d", rc)
	}

	// Ensure cached temp float buffer: samplesPerRow * height * 2 floats (Y + UV)
	samplesPerRow := src.Pitch / 2
	tmpFloats := samplesPerRow * src.Height * 2
	if err := ctx.ensureBlurTemp(tmpFloats); err != nil {
		return fmt.Errorf("gpu: gaussian blur p210: %w", err)
	}

	rc := C.gaussian_blur_p210(
		(*C.uint16_t)(unsafe.Pointer(uintptr(dst.DevPtr))),
		(*C.uint16_t)(unsafe.Pointer(uintptr(src.DevPtr))),
		ctx.blurTmp,
		(*C.float)(dWeights),
		C.int(src.Width), C.int(src.Height), C.int(src.Pitch), C.int(radius),
		ctx.stream,
	)
	if rc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur p210 failed: %d", rc)
	}

	if syncRc := C.cudaStreamSynchronize(ctx.stream); syncRc != C.cudaSuccess {
		return fmt.Errorf("gpu: gaussian blur p210 sync failed: %d", syncRc)
	}
	return nil
}

// GPUGaussianBlurDispatch routes to the P210 or NV12 Gaussian blur variant
// based on the destination frame's format.
func GPUGaussianBlurDispatch(ctx *Context, dst, src *GPUFrame, radius int) error {
	if dst != nil && dst.Format == GPUFormatP210 {
		return GaussianBlurP210(ctx, dst, src, radius)
	}
	return GaussianBlur(ctx, dst, src, radius)
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
