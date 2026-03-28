//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fillP210AlphaMask writes a uint8 alpha mask into a P210 frame's buffer:
// top half = 255, bottom half = 0. Uses Upload (NV12 path) to write uint8
// data into the Y-plane region of the GPU buffer — no cgo needed in test.
func fillP210AlphaMask(t *testing.T, ctx *Context, frame *GPUFrame, w, h int) {
	t.Helper()
	// Build YUV420P with Y = alpha mask, UV = 128
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	halfH := h / 2
	for row := 0; row < h; row++ {
		val := byte(0)
		if row < halfH {
			val = 255
		}
		for col := 0; col < w; col++ {
			yuv[row*w+col] = val
		}
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}
	// Upload as NV12 — writes uint8 Y data into the frame buffer.
	// The alpha blend kernel only reads the Y plane as uint8, so NV12
	// upload is fine even though the frame format is P210.
	err := Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err, "upload alpha mask failed")
}

// fillP210AlphaUniform writes a uniform uint8 alpha value into a P210 frame's buffer.
func fillP210AlphaUniform(t *testing.T, ctx *Context, frame *GPUFrame, w, h int, alphaVal byte) {
	t.Helper()
	ySize := w * h
	cbSize := (w / 2) * (h / 2)
	yuv := make([]byte, ySize+cbSize+cbSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = alphaVal
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}
	err := Upload(ctx, frame, yuv, w, h)
	require.NoError(t, err, "upload alpha uniform failed")
}
