//go:build darwin || (cgo && cuda)

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewEncoder(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	srcW, srcH := 1920, 1080
	dstW, dstH := 854, 480

	pe, err := NewPreviewEncoder(ctx, srcW, srcH, dstW, dstH, 500_000, 30, 1)
	require.NoError(t, err)
	defer pe.Close()

	// Create source frame pool and upload test pattern
	srcPool, err := NewFramePool(ctx, srcW, srcH, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, _ := srcPool.Acquire()
	defer src.Release()

	yuv := make([]byte, srcW*srcH*3/2)
	for i := 0; i < srcW*srcH; i++ {
		yuv[i] = byte(i%220) + 16
	}
	for i := srcW * srcH; i < len(yuv); i++ {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, srcW, srcH))

	// Encode multiple frames (NVENC may buffer first few)
	var encoded int
	var totalBytes int
	for i := 0; i < 30; i++ {
		src.PTS = int64(i * 3000)
		data, _, err := pe.Encode(src, i == 0)
		if err != nil {
			t.Logf("Frame %d encode error: %v", i, err)
			continue
		}
		if len(data) > 0 {
			encoded++
			totalBytes += len(data)
		}
	}

	assert.Greater(t, encoded, 0, "preview encoder should produce output")
	t.Logf("Preview encode: %d/30 frames, %d total bytes (avg %d bytes/frame)",
		encoded, totalBytes, totalBytes/max(encoded, 1))
}

func TestPreviewEncoderSmallResolution(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	// 640x480 → 320x240 preview
	pe, err := NewPreviewEncoder(ctx, 640, 480, 320, 240, 300_000, 30, 1)
	require.NoError(t, err)
	defer pe.Close()

	srcPool, err := NewFramePool(ctx, 640, 480, 2)
	require.NoError(t, err)
	defer srcPool.Close()

	src, _ := srcPool.Acquire()
	defer src.Release()

	yuv := make([]byte, 640*480*3/2)
	for i := range yuv {
		yuv[i] = 128
	}
	require.NoError(t, Upload(ctx, src, yuv, 640, 480))

	var data []byte
	for i := 0; i < 10; i++ {
		src.PTS = int64(i * 3000)
		data, _, err = pe.Encode(src, i == 0)
		if err == nil && len(data) > 0 {
			break
		}
	}
	assert.NotEmpty(t, data, "small preview should produce output")
}

func TestPreviewEncoderNilArgs(t *testing.T) {
	_, err := NewPreviewEncoder(nil, 1920, 1080, 854, 480, 500_000, 30, 1)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestPreviewEncoderDoubleClose(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pe, err := NewPreviewEncoder(ctx, 640, 480, 320, 240, 300_000, 30, 1)
	require.NoError(t, err)

	pe.Close()
	pe.Close() // should not panic
}
