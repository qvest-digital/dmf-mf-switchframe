//go:build darwin

package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUEncode(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
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
	require.NoError(t, err)
	defer enc.Close()

	// Native VT encoder should be active on Apple Silicon
	t.Logf("native VT encoder: %v", enc.IsNativeVT())

	// Encode from GPU frame — may need multiple frames for warmup
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
	t.Logf("GPU encode: %d bytes, IDR=%v, nativeVT=%v", len(data), isIDR, enc.IsNativeVT())

	// Verify it's valid H.264 Annex B (starts with 0x00 0x00 0x00 0x01 or 0x00 0x00 0x01)
	assert.True(t, len(data) > 4, "encoded data should be > 4 bytes")
	if enc.IsNativeVT() {
		// Annex B start code check
		assert.True(t,
			(data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1) ||
				(data[0] == 0 && data[1] == 0 && data[2] == 1),
			"output should be Annex B format (starts with 00 00 00 01 or 00 00 01)")
	}
}

func TestGPUEncodeMultipleFrames(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
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

	t.Logf("GPU encode: %d/30 frames, %d total bytes, nativeVT=%v", encoded, totalBytes, enc.IsNativeVT())
	assert.Greater(t, encoded, 0, "should encode at least one frame")
}

func TestGPUEncodeNilContext(t *testing.T) {
	_, err := NewGPUEncoder(nil, 640, 480, 30, 1, 2_000_000)
	require.ErrorIs(t, err, ErrGPUNotAvailable)
}

func TestGPUEncodeNilFrame(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	enc, err := NewGPUEncoder(ctx, 320, 240, 30, 1, 1_000_000)
	require.NoError(t, err)
	defer enc.Close()

	_, _, err = enc.EncodeGPU(nil, false)
	require.Error(t, err)
}

func TestGPUEncodeNativeVTIDRKeyframes(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	w, h := 640, 480
	pool, err := NewFramePool(ctx, w, h, 2)
	require.NoError(t, err)
	defer pool.Close()

	frame, err := pool.Acquire()
	require.NoError(t, err)
	defer frame.Release()

	// Upload test pattern
	testYUV := make([]byte, w*h*3/2)
	for i := 0; i < w*h; i++ {
		testYUV[i] = byte(i%220) + 16
	}
	for i := w * h; i < len(testYUV); i++ {
		testYUV[i] = 128
	}
	err = Upload(ctx, frame, testYUV, w, h)
	require.NoError(t, err)

	enc, err := NewGPUEncoder(ctx, w, h, 30, 1, 2_000_000)
	require.NoError(t, err)
	defer enc.Close()

	if !enc.IsNativeVT() {
		t.Skip("native VT encoder not available")
	}

	// Encode 60 frames, forcing IDR every 15th frame
	idrCount := 0
	totalFrames := 0
	for i := 0; i < 60; i++ {
		frame.PTS = int64(i * 3000)
		forceIDR := i%15 == 0
		data, isIDR, err := enc.EncodeGPU(frame, forceIDR)
		if err != nil {
			t.Logf("frame %d: %v", i, err)
			continue
		}
		if len(data) == 0 {
			continue
		}
		totalFrames++
		if isIDR {
			idrCount++
		}
	}

	t.Logf("encoded %d frames, %d IDRs", totalFrames, idrCount)
	assert.Greater(t, totalFrames, 30, "should encode most frames")
	assert.Greater(t, idrCount, 0, "should have at least one IDR")
}

func TestGPUEncoderClose(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		t.Skipf("Metal not available: %v", err)
	}
	defer ctx.Close()

	enc, err := NewGPUEncoder(ctx, 320, 240, 30, 1, 1_000_000)
	require.NoError(t, err)

	// Close should not panic
	enc.Close()
	// Double close should also not panic
	enc.Close()
}
