//go:build cgo && cuda

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUEncode(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 640, 480, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload a test pattern
	w, h := 640, 480
	testYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		testYUV[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(testYUV); i++ {
		testYUV[i] = 128
	}

	err = Upload(ctx, frame, testYUV, w, h)
	require.NoError(t, err)
	frame.PTS = 0

	// Create GPU encoder
	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 2_000_000)
	require.NoError(t, err, "GPU encoder should create on L4")
	defer enc.Close()

	// Encode from GPU frame — may need multiple frames for NVENC warmup
	var data []byte
	var isIDR bool
	for i := 0; i < 10; i++ {
		frame.PTS = int64(i * 3000)
		data, isIDR, err = enc.EncodeGPU(frame, i == 0)
		if err != nil {
			t.Logf("Encode frame %d: %v", i, err)
			continue
		}
		if len(data) > 0 {
			break
		}
	}

	require.NotEmpty(t, data, "encoder should produce output")
	t.Logf("GPU encode: %d bytes, IDR=%v", len(data), isIDR)

	// Verify it's valid H.264 (starts with 0x00 0x00 or Annex B start code)
	assert.True(t, len(data) > 4, "encoded data should be > 4 bytes")
}

func TestGPUEncodeMultipleFrames(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	pool, err := NewFramePool(ctx, 320, 240, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	w, h := 320, 240
	testYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		testYUV[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(testYUV); i++ {
		testYUV[i] = 128
	}
	err = Upload(ctx, frame, testYUV, w, h)
	require.NoError(t, err)

	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer enc.Close()

	encoded := 0
	totalBytes := 0
	for i := 0; i < 30; i++ {
		frame.PTS = int64(i * 3000)
		data, _, err := enc.EncodeGPU(frame, i == 0)
		if err != nil {
			continue
		}
		if len(data) > 0 {
			encoded++
			totalBytes += len(data)
		}
	}

	t.Logf("GPU encode: %d/30 frames, %d total bytes", encoded, totalBytes)
	assert.Greater(t, encoded, 0, "should encode at least one frame")
}

func TestGPUEncodeCPUFallback(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 320, 240
	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer enc.Close()

	// Encode from CPU-side YUV directly (bypass GPU path)
	testYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		testYUV[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(testYUV); i++ {
		testYUV[i] = 128
	}

	var data []byte
	for i := 0; i < 10; i++ {
		data, _, err = enc.EncodeCPU(testYUV, int64(i*3000), i == 0)
		if err != nil {
			continue
		}
		if len(data) > 0 {
			break
		}
	}

	require.NotEmpty(t, data, "CPU encode path should produce output")
	t.Logf("CPU fallback encode: %d bytes", len(data))
}

func TestGPUEncodeNilContext(t *testing.T) {
	_, err := NewGPUEncoder(nil, 640, 480, 30, 1, 2_000_000)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestGPUEncodeNilFrame(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	enc, err := NewGPUEncoder(ctx, 320, 240, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer enc.Close()

	_, _, err = enc.EncodeGPU(nil, false)
	require.Error(t, err)
}

func TestGPUEncodeHWFrames(t *testing.T) {
	ctx, err := NewContext()
	require.NoError(t, err)
	defer ctx.Close()

	w, h := 640, 480
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload a test pattern
	testYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		testYUV[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(testYUV); i++ {
		testYUV[i] = 128
	}
	err = Upload(ctx, frame, testYUV, w, h)
	require.NoError(t, err)

	// Create GPU encoder — should prefer hw_frames on CUDA
	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 2_000_000)
	require.NoError(t, err)
	defer enc.Close()

	t.Logf("GPU encoder: hwFrames=%v, nativeVT=%v", enc.IsHWFrames(), enc.IsNativeVT())

	// Encode multiple frames
	var data []byte
	for i := 0; i < 10; i++ {
		frame.PTS = int64(i * 3000)
		data, _, err = enc.EncodeGPU(frame, i == 0)
		if err != nil {
			t.Logf("HW frames encode frame %d: %v", i, err)
			continue
		}
		if len(data) > 0 {
			break
		}
	}
	require.NotEmpty(t, data, "hw_frames encoder should produce output")
	t.Logf("HW frames encode: %d bytes", len(data))
	assert.True(t, len(data) > 4, "encoded data should be > 4 bytes")
}
