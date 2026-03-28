//go:build cgo && cuda

package gpu

/*
#include <cuda_runtime.h>
*/
import "C"

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// fillP210AlphaMask writes a uint8 alpha mask into a P210 frame's buffer:
// top half = 255, bottom half = 0. The alpha mask is written as uint8 values
// at the frame's pitch, occupying the Y-plane region of the P210 buffer.
//
// On CUDA, device memory is not CPU-accessible; we build the mask on the CPU
// and upload via cudaMemcpy.
func fillP210AlphaMask(t *testing.T, ctx *Context, frame *GPUFrame, w, h int) {
	t.Helper()
	size := frame.FrameSize()
	buf := make([]byte, size)
	halfH := h / 2
	pitch := int(frame.Pitch)
	for row := 0; row < h; row++ {
		val := byte(0)
		if row < halfH {
			val = 255
		}
		for col := 0; col < w; col++ {
			buf[row*pitch+col] = val
		}
	}
	rc := C.cudaMemcpy(
		unsafe.Pointer(uintptr(frame.DevPtr)),
		unsafe.Pointer(&buf[0]),
		C.size_t(size),
		C.cudaMemcpyHostToDevice,
	)
	require.Equal(t, C.cudaSuccess, rc, "cudaMemcpy failed for alpha mask: %d", rc)
}

// fillP210AlphaUniform writes a uniform uint8 alpha value into a P210 frame's buffer.
//
// On CUDA, device memory is not CPU-accessible; we build the buffer on the CPU
// and upload via cudaMemcpy.
func fillP210AlphaUniform(t *testing.T, ctx *Context, frame *GPUFrame, w, h int, alphaVal byte) {
	t.Helper()
	size := frame.FrameSize()
	buf := make([]byte, size)
	pitch := int(frame.Pitch)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			buf[row*pitch+col] = alphaVal
		}
	}
	rc := C.cudaMemcpy(
		unsafe.Pointer(uintptr(frame.DevPtr)),
		unsafe.Pointer(&buf[0]),
		C.size_t(size),
		C.cudaMemcpyHostToDevice,
	)
	require.Equal(t, C.cudaSuccess, rc, "cudaMemcpy failed for alpha uniform: %d", rc)
}
