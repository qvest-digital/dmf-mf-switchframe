//go:build cgo && !noffmpeg

package codec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCVBR_NoFillerNALUs verifies that cVBR mode (ABR + VBV) does NOT produce
// filler NALUs (type 12). Filler NALUs are a sign of nal-hrd=cbr which we
// explicitly avoid — the CBR pacer handles transport-level padding instead.
func TestCVBR_NoFillerNALUs(t *testing.T) {
	w, h := 320, 240
	// High bitrate for simple content — this would force fillers under nal-hrd=cbr.
	bitrate := 2_000_000
	enc, err := NewFFmpegEncoder("libx264", w, h, bitrate, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128 // uniform gray — minimal complexity
	}

	fillerCount := 0
	for i := 0; i < 90; i++ {
		data, _, err := enc.Encode(yuv, int64(i*3000), i == 0)
		require.NoError(t, err)
		if data == nil {
			continue
		}
		// Search for filler NALU (type 12) in Annex B output.
		avc1 := AnnexBToAVC1(data)
		for _, nalu := range ExtractNALUs(avc1) {
			if len(nalu) > 0 && nalu[0]&0x1F == 12 {
				fillerCount++
			}
		}
	}
	require.Zero(t, fillerCount,
		"cVBR encoder should NOT produce filler NALUs (got %d); transport-level padding is the CBR pacer's job", fillerCount)
}

// TestCVBR_BitrateConvergence verifies that the cVBR encoder's output bitrate
// converges near the target. Unlike CRF which ignores the bitrate parameter
// entirely, cVBR uses ABR + VBV to target a specific bitrate.
//
// Uses 640x480 resolution with high-complexity content so ABR can actually
// reach the target — at tiny resolutions (320x240) the encoder is too efficient
// and undershoots regardless of target.
func TestCVBR_BitrateConvergence(t *testing.T) {
	w, h := 640, 480
	bitrate := 2_000_000 // 2 Mbps target — realistic for 480p
	fps := 30

	enc, err := NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	totalBytes := 0
	frameCount := 0

	for i := 0; i < 120; i++ {
		// High-frequency noise-like content to stress rate control.
		for j := 0; j < w*h; j++ {
			yuv[j] = byte((j*13 + i*37 + (j>>8)*7) % 256)
		}
		for j := w * h; j < len(yuv); j++ {
			yuv[j] = byte(128 + (j*3+i*11)%64 - 32)
		}

		data, _, err := enc.Encode(yuv, int64(i*3000), i%60 == 0)
		require.NoError(t, err)
		if data != nil {
			totalBytes += len(data)
			frameCount++
		}
	}

	require.Greater(t, frameCount, 0, "encoder should produce output")

	// Compute actual bitrate: totalBytes * 8 * fps / frameCount
	actualBitrate := float64(totalBytes) * 8.0 * float64(fps) / float64(frameCount)
	ratio := actualBitrate / float64(bitrate)

	t.Logf("target=%d actual=%.0f ratio=%.2f frames=%d", bitrate, actualBitrate, ratio, frameCount)

	// cVBR should converge within ±40% of target. With superfast preset and
	// rc-lookahead=0 (set by tune zerolatency), rate control has less context
	// for bit allocation, so tolerance is slightly wider than with lookahead.
	// Production camera content at 1080p converges much tighter (±20%).
	require.Greater(t, ratio, 0.5, "actual bitrate %.0f too low vs target %d (ratio %.2f)", actualBitrate, bitrate, ratio)
	require.Less(t, ratio, 1.4, "actual bitrate %.0f too high vs target %d (ratio %.2f)", actualBitrate, bitrate, ratio)
}

// TestCVBR_VBVConstrainedFrameSize verifies that the VBV buffer at 1.2x target
// prevents any single frame from exceeding the buffer capacity.
func TestCVBR_VBVConstrainedFrameSize(t *testing.T) {
	w, h := 320, 240
	bitrate := 500_000

	enc, err := NewFFmpegEncoder("libx264", w, h, bitrate, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	var maxSize int

	for i := 0; i < 90; i++ {
		// Create high-complexity content to stress rate control.
		for j := 0; j < w*h; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := w * h; j < len(yuv); j++ {
			yuv[j] = 128
		}

		data, _, err := enc.Encode(yuv, int64(i*3000), i%30 == 0)
		require.NoError(t, err)
		if len(data) > maxSize {
			maxSize = len(data)
		}
	}

	// VBV buffer = 1.2x bitrate in bits, convert to bytes.
	// rc_buffer_size = bitrate * 6 / 5 (1.2x), in bits → bytes = / 8.
	vbvBufferBytes := int(float64(bitrate) * 1.2 / 8.0)
	require.Less(t, maxSize, vbvBufferBytes,
		"max frame size %d should be less than VBV buffer %d bytes (1.2x target)", maxSize, vbvBufferBytes)
}

// TestCVBR_SlicedThreadingImmediate verifies the encoder with sliced threading
// (tune zerolatency) produces output on the very first frame — zero internal
// frame buffering, unlike frame threading which buffers (thread_count-1) frames.
func TestCVBR_SlicedThreadingImmediate(t *testing.T) {
	w, h := 640, 480
	enc, err := NewFFmpegEncoder("libx264", w, h, 2_000_000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	// With sliced threading, output is immediate on first frame.
	data, _, err := enc.Encode(yuv, 0, true)
	require.NoError(t, err)
	require.NotNil(t, data,
		"encoder with sliced threading should produce output on first frame (zero internal latency)")
}

// TestCVBR_ProducesOutput verifies basic encoder functionality with the new
// cVBR configuration (no cbr parameter — always cVBR).
func TestCVBR_ProducesOutput(t *testing.T) {
	w, h := 320, 240
	enc, err := NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 2, nil)
	require.NoError(t, err)
	defer enc.Close()

	yuv := make([]byte, w*h*3/2)
	for i := range yuv {
		yuv[i] = 128
	}

	// Sliced threading produces output on first frame.
	encoded, isKey, err := enc.Encode(yuv, 0, true)
	require.NoError(t, err)
	require.NotEmpty(t, encoded, "cVBR encoder should produce output on first frame")
	require.True(t, isKey, "first output should be a keyframe")

	// Verify Annex B start code.
	require.GreaterOrEqual(t, len(encoded), 4)
	require.Equal(t, byte(0x00), encoded[0])
	require.Equal(t, byte(0x00), encoded[1])
	require.Equal(t, byte(0x00), encoded[2])
	require.Equal(t, byte(0x01), encoded[3])
}

// TestCVBR_ProbeFrameCount verifies the probe sends enough frames to get output.
// With sliced threading (tune zerolatency), libx264 succeeds on frame 1.
// The 30-frame loop provides headroom for hardware encoders with warmup latency.
func TestCVBR_ProbeFrameCount(t *testing.T) {
	result := tryEncoder("libx264")
	require.True(t, result, "tryEncoder should succeed for libx264 with sufficient frame count")
}
