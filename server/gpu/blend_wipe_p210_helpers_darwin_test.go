//go:build cgo && darwin

package gpu

import (
	"testing"
	"unsafe"
)

// fillP210AlphaMask writes a uint8 alpha mask into a P210 frame's buffer:
// top half = 255, bottom half = 0. The alpha mask is written as uint8 values
// at the frame's pitch, occupying the Y-plane region of the P210 buffer.
//
// On Metal (Apple Silicon unified memory), DevPtr is a direct CPU-accessible pointer.
func fillP210AlphaMask(t *testing.T, _ *Context, frame *GPUFrame, w, h int) {
	t.Helper()
	buf := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize()) //nolint:govet // intentional Metal unified memory pointer conversion
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
}

// fillP210AlphaUniform writes a uniform uint8 alpha value into a P210 frame's buffer.
//
// On Metal (Apple Silicon unified memory), DevPtr is a direct CPU-accessible pointer.
func fillP210AlphaUniform(t *testing.T, _ *Context, frame *GPUFrame, w, h int, alphaVal byte) {
	t.Helper()
	buf := unsafe.Slice((*byte)(unsafe.Pointer(frame.DevPtr)), frame.FrameSize()) //nolint:govet // intentional Metal unified memory pointer conversion
	pitch := int(frame.Pitch)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			buf[row*pitch+col] = alphaVal
		}
	}
}
