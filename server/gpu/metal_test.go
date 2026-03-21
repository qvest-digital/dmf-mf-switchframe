//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetalContext(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	props := ctx.DeviceProperties()
	assert.NotEmpty(t, props.Name)
	assert.Greater(t, props.TotalMemory, int64(0))

	t.Logf("GPU: %s, Memory: %d MB", props.Name, props.TotalMemory/(1024*1024))
}

func TestMetalContextSync(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	err = ctx.Sync()
	require.NoError(t, err)
}

func TestMetalContextMemoryStats(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	stats := ctx.MemoryStats()
	assert.Greater(t, stats.TotalMB, 0)
	t.Logf("Unified Memory: %d MB", stats.TotalMB)
}

func TestMetalContextCloseNilSafe(t *testing.T) {
	var ctx *Context
	err := ctx.Close()
	assert.NoError(t, err)
}

func TestMetalContextDoubleClose(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	err = ctx.Close()
	assert.NoError(t, err)
	err = ctx.Close()
	assert.NoError(t, err)
}

func TestMetalFramePool(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 1920, 1080, 4)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	require.NotNil(t, frame)

	assert.Equal(t, 1920, frame.Width)
	assert.Equal(t, 1080, frame.Height)
	assert.Greater(t, frame.Pitch, 0)
	assert.True(t, frame.Pitch%256 == 0, "pitch should be 256-byte aligned: %d", frame.Pitch)
	assert.NotEqual(t, uintptr(0), frame.DevPtr, "DevPtr should be non-zero (unified memory)")

	frame.Release()

	// Verify pool reuse
	hits, misses := pool.Stats()
	assert.Equal(t, uint64(1), hits)
	assert.Equal(t, uint64(0), misses)
}

func TestMetalUploadDownloadRoundTrip(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Create test pattern
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = byte(i % 256)
	}
	for i := 0; i < cbSize; i++ {
		yuv[ySize+i] = 100
	}
	for i := 0; i < crSize; i++ {
		yuv[ySize+cbSize+i] = 200
	}

	// Upload
	err = Upload(ctx, frame, yuv, width, height)
	require.NoError(t, err)

	// Download
	out := make([]byte, len(yuv))
	err = Download(ctx, out, frame, width, height)
	require.NoError(t, err)

	// Verify Y plane values match
	for i := 0; i < ySize; i++ {
		assert.Equal(t, yuv[i], out[i], "Y mismatch at index %d", i)
	}
	// Verify Cb plane
	for i := 0; i < cbSize; i++ {
		assert.Equal(t, yuv[ySize+i], out[ySize+i], "Cb mismatch at index %d", i)
	}
	// Verify Cr plane
	for i := 0; i < crSize; i++ {
		assert.Equal(t, yuv[ySize+cbSize+i], out[ySize+cbSize+i], "Cr mismatch at index %d", i)
	}
}

func TestMetalFillBlack(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	err = FillBlack(ctx, frame)
	require.NoError(t, err)

	// Download and verify
	ySize := width * height
	cbSize := (width / 2) * (height / 2)
	crSize := cbSize
	yuv := make([]byte, ySize+cbSize+crSize)
	err = Download(ctx, yuv, frame, width, height)
	require.NoError(t, err)

	// Verify Y=16 (BT.709 limited-range black)
	for i := 0; i < ySize; i++ {
		assert.Equal(t, byte(16), yuv[i], "Y should be 16 at index %d, got %d", i, yuv[i])
	}
	// Verify Cb=128
	for i := 0; i < cbSize; i++ {
		assert.Equal(t, byte(128), yuv[ySize+i], "Cb should be 128 at index %d", i)
	}
	// Verify Cr=128
	for i := 0; i < crSize; i++ {
		assert.Equal(t, byte(128), yuv[ySize+cbSize+i], "Cr should be 128 at index %d", i)
	}
}

func TestMetalSTMapIdentity(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	width, height := 64, 64
	pool, err := NewFramePool(ctx, width, height, 4)
	require.NoError(t, err)
	defer pool.Close()

	// Create identity ST map
	n := width * height
	s := make([]float32, n)
	tv := make([]float32, n)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			s[idx] = float32(x) / float32(width-1)
			tv[idx] = float32(y) / float32(height-1)
		}
	}

	stmap, err := UploadSTMap(ctx, s, tv, width, height)
	require.NoError(t, err)
	defer stmap.Free()

	assert.Equal(t, width, stmap.Width)
	assert.Equal(t, height, stmap.Height)
}

func TestMetalBuildLumaKeyLUT(t *testing.T) {
	lut := BuildLumaKeyLUT(32, 200, 10)
	// Below lowClip-softness should be 0
	assert.Equal(t, byte(0), lut[0])
	assert.Equal(t, byte(0), lut[20])
	// Above highClip+softness should be 255
	assert.Equal(t, byte(255), lut[215])
	assert.Equal(t, byte(255), lut[255])
	// Middle range should be 255 (fully opaque)
	assert.Equal(t, byte(255), lut[100])
}
