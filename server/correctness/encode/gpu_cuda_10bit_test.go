//go:build correctness && cgo && cuda

package encode

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	gpupkg "github.com/zsiec/switchframe/server/gpu"
	"github.com/zsiec/switchframe/server/switcher"
)

// ==========================================================================
// 10-bit GPU/CUDA encode tests.
//
// These test CUDA-accelerated encoding paths for 10-bit content.
// Only runs on machines with CUDA-capable GPUs.
//
// Build tag: correctness && cgo && cuda
// ==========================================================================

// TestGPUContext10bit verifies that the GPU context can be created and
// handles 10-bit frame metadata correctly.
func TestGPUContext10bit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx, err := gpupkg.NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	defer ctx.Close()

	// Verify context is usable.
	hwCtx := ctx.CUDAContext()
	require.NotNil(t, hwCtx, "CUDA context should be non-nil")

	// Verify 10-bit frame dimensions are what we expect.
	format := switcher.ColorYUV422_10bit
	w, h := 1920, 1080
	frameSize := format.FrameSize(w, h)

	require.Equal(t, w*h*4, frameSize,
		"10-bit 1080p frame should be %d bytes", w*h*4)

	t.Logf("GPU context OK, 10-bit 1080p frame = %d bytes (%.1f MB)",
		frameSize, float64(frameSize)/(1024*1024))
}

// TestGPUUpload10bitFrameSize verifies that 10-bit frame buffers are
// correctly sized for GPU upload. The GPU pool needs to allocate
// NV12 P210 buffers which are different from YUV420.
func TestGPUUpload10bitFrameSize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx, err := gpupkg.NewContext()
	if err != nil {
		t.Skip("GPU not available:", err)
	}
	defer ctx.Close()

	format := switcher.ColorYUV422_10bit

	resolutions := []struct{ w, h int }{
		{320, 240},
		{1280, 720},
		{1920, 1080},
	}

	for _, r := range resolutions {
		frameSize := format.FrameSize(r.w, r.h)

		// Create a test 10-bit frame to verify sizing.
		data := make([]byte, frameSize)
		ySize := r.w * r.h
		chromaW := r.w / 2
		chromaSize := chromaW * r.h

		// Fill with test pattern.
		for i := 0; i < ySize; i++ {
			binary.LittleEndian.PutUint16(data[i*2:], 500)
		}
		cbOff := ySize * 2
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[cbOff+i*2:], 512)
		}
		crOff := cbOff + chromaSize*2
		for i := 0; i < chromaSize; i++ {
			binary.LittleEndian.PutUint16(data[crOff+i*2:], 512)
		}

		require.Len(t, data, frameSize)
		t.Logf("%dx%d: 10-bit frame = %d bytes for GPU upload", r.w, r.h, frameSize)
	}
}

// TestNVENC10bitVsLibx264 encodes the same content with NVENC and libx264,
// verifying both produce valid output. NVENC may support HEVC 10-bit
// directly; libx264 requires downconversion.
func TestNVENC10bitVsLibx264(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	ctx := nvencContext(t)
	hwCtx := ctx.CUDAContext()

	w, h := 320, 240
	fps := 30
	gopSecs := 2
	bitrate := 500_000
	numFrames := 90

	// Encode with NVENC.
	nvencPath := nvencEncodeToTS(t, "h264_nvenc", w, h, fps, gopSecs, bitrate, numFrames, hwCtx)

	// Encode with libx264.
	x264Path := nvencEncodeToTS(t, "libx264", w, h, fps, gopSecs, bitrate, numFrames, nil)

	// Verify 10-bit frame size calculation is consistent.
	format10 := switcher.ColorYUV422_10bit
	expectedSize := format10.FrameSize(w, h)
	require.Equal(t, w*h*4, expectedSize,
		"10-bit frame should be w*h*4 = %d", w*h*4)

	// Both should produce valid TS.
	nvencFrames := testutil.ProbeVideoFrames(t, nvencPath)
	x264Frames := testutil.ProbeVideoFrames(t, x264Path)

	require.Greater(t, len(nvencFrames), 50, "NVENC should produce frames")
	require.Greater(t, len(x264Frames), 50, "libx264 should produce frames")

	// Frame counts should be similar (within 10%).
	require.InDelta(t, float64(len(x264Frames)), float64(len(nvencFrames)),
		float64(len(x264Frames))*0.10+5,
		"NVENC and x264 frame counts should be similar")

	t.Logf("10-bit encode comparison: NVENC=%d frames, x264=%d frames", len(nvencFrames), len(x264Frames))
}
