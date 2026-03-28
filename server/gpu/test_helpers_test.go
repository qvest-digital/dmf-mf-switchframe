//go:build (cgo && darwin) || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// testContext creates a GPU context for testing, skipping the test if
// the GPU is not available (e.g., Metal language version unsupported on
// CI runner, no CUDA device). Registers cleanup via t.Cleanup.
func testContext(t *testing.T) *Context {
	t.Helper()
	ctx, err := NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	return ctx
}

// makeTestFrame creates a GPU frame filled with a uniform YUV color.
func makeTestFrame(t *testing.T, ctx *Context, pool *FramePool, width, height int, y, cb, cr byte) *GPUFrame {
	t.Helper()
	frame, err := pool.Acquire()
	require.NoError(t, err)

	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = y
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = cb
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = cr
	}

	err = Upload(ctx, frame, yuv, width, height)
	require.NoError(t, err)
	return frame
}

// avgPlanes downloads a GPU frame and returns the average Y, Cb, Cr values.
func avgPlanes(t *testing.T, ctx *Context, frame *GPUFrame, width, height int) (yAvg, cbAvg, crAvg float64) {
	t.Helper()
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	err := Download(ctx, yuv, frame, width, height)
	require.NoError(t, err)

	var ySum, cbSum, crSum int64
	for i := 0; i < ySize; i++ {
		ySum += int64(yuv[i])
	}
	for i := 0; i < cbSize; i++ {
		cbSum += int64(yuv[ySize+i])
	}
	for i := 0; i < crSize; i++ {
		crSum += int64(yuv[ySize+cbSize+i])
	}
	yAvg = float64(ySum) / float64(ySize)
	cbAvg = float64(cbSum) / float64(cbSize)
	crAvg = float64(crSum) / float64(crSize)
	return
}
