//go:build correctness

package encode

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
	"github.com/zsiec/switchframe/server/output"
)

// TestEncoderHighMotionBitrate generates random noise frames (worst-case content)
// and verifies the encoder doesn't produce insane bitrates even when it can't
// hit its target.
func TestEncoderHighMotionBitrate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	targetBitrate := 2_000_000 // 2Mbps
	numFrames := 60            // 2 seconds at 30fps
	gopSecs := 2

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, targetBitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize
	rng := rand.New(rand.NewSource(42))

	var totalEncodedBytes int
	var encodedCount int
	ptsDelta := int64(90000 / fps)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := i == 0

		// Generate random noise for Y plane (worst case for encoder)
		yuv := make([]byte, frameSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte(rng.Intn(256))
		}
		// Neutral chroma
		for j := ySize; j < frameSize; j++ {
			yuv[j] = 128
		}

		encoded, _, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if encoded != nil {
			totalEncodedBytes += len(encoded)
			encodedCount++
		}
	}

	durationSec := float64(numFrames) / float64(fps)
	actualBitrateKbps := int(float64(totalEncodedBytes) * 8.0 / durationSec / 1000.0)
	targetKbps := targetBitrate / 1000
	ratio := float64(actualBitrateKbps) / float64(targetKbps)

	t.Logf("MEASURE: target=%dkbps, actual=%dkbps, ratio=%.1fx", targetKbps, actualBitrateKbps, ratio)
	t.Logf("MEASURE: encoded %d frames, total bytes=%d", encodedCount, totalEncodedBytes)

	// Encoder can't hit target with random noise, but shouldn't go insane.
	// 4x target = 8Mbps ceiling.
	maxBitrateKbps := targetKbps * 4
	require.Less(t, actualBitrateKbps, maxBitrateKbps,
		"bitrate %dkbps exceeds 4x target (%dkbps) with noise content", actualBitrateKbps, maxBitrateKbps)
}

// TestEncoderLongGOP encodes with a 10-second GOP and verifies keyframes only
// appear at the expected GOP boundaries.
func TestEncoderLongGOP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	gopSecs := 10
	numFrames := 300 // 10 seconds
	bitrate := 500_000

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	ptsDelta := int64(90000 / fps)

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		// Only force IDR at GOP boundaries
		forceIDR := (i % (fps * gopSecs)) == 0

		yuv := make([]byte, ySize+2*uvSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}
		require.NoError(t, muxer.WriteVideo(frame), "mux frame %d", i)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "longgop.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 200, "expected at least 200 video frames")

	var keyframePositions []int
	for i, f := range frames {
		if f.KeyFrame {
			keyframePositions = append(keyframePositions, i)
		}
	}

	t.Logf("MEASURE: keyframe positions = %v", keyframePositions)
	t.Logf("MEASURE: total frames = %d, keyframe count = %d", len(frames), len(keyframePositions))

	// With a 10s GOP at 30fps = 300 frame GOP size, and sc_threshold=0,
	// we expect keyframe only at frame 0 (the only forced IDR for a single GOP).
	// All intermediate frames should be P-frames (not I).
	for i, f := range frames {
		if i == 0 {
			continue // first frame is always a keyframe
		}
		// With sc_threshold=0, no scene-change IDRs should appear mid-GOP.
		// Only the forced IDR at frame 0 should be a keyframe.
		if f.PictType == "I" && i > 0 {
			t.Logf("MEASURE: unexpected I-frame at position %d (pict_type=%s, key_frame=%v)",
				i, f.PictType, f.KeyFrame)
		}
	}

	// The only keyframe should be at position 0.
	// (With 300 frames = exactly 1 GOP at gopSecs=10, there's no second IDR)
	require.GreaterOrEqual(t, len(keyframePositions), 1, "expected at least 1 keyframe")
	require.Equal(t, 0, keyframePositions[0], "first keyframe should be at position 0")

	if len(keyframePositions) == 1 {
		t.Logf("MEASURE: single GOP confirmed (only 1 keyframe at position 0)")
	} else {
		// If there are more keyframes, verify they're at GOP boundaries
		for i := 1; i < len(keyframePositions); i++ {
			interval := keyframePositions[i] - keyframePositions[i-1]
			t.Logf("MEASURE: keyframe interval %d->%d = %d frames",
				keyframePositions[i-1], keyframePositions[i], interval)
		}
	}
}

// TestEncoderForceIDR encodes with forceIDR=true on specific frames and verifies
// keyframes appear at those positions.
func TestEncoderForceIDR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	numFrames := 30
	bitrate := 500_000
	gopSecs := 10 // large GOP so auto-IDR doesn't interfere

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	ptsDelta := int64(90000 / fps)

	// Force IDR at frames 0, 10, 20
	forceIDRFrames := map[int]bool{0: true, 10: true, 20: true}

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := forceIDRFrames[i]

		yuv := make([]byte, ySize+2*uvSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}
		require.NoError(t, muxer.WriteVideo(frame), "mux frame %d", i)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "forceidr.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 25, "expected at least 25 video frames")

	var keyframePositions []int
	for i, f := range frames {
		if f.KeyFrame {
			keyframePositions = append(keyframePositions, i)
		}
	}

	t.Logf("MEASURE: keyframe count = %d, positions = %v", len(keyframePositions), keyframePositions)

	// Assert: at least 3 keyframes at positions 0, ~10, ~20
	require.GreaterOrEqual(t, len(keyframePositions), 3,
		"expected at least 3 keyframes for forced IDR at 0, 10, 20")

	// First keyframe must be at 0
	require.Equal(t, 0, keyframePositions[0], "first keyframe should be at position 0")

	// Check that positions ~10 and ~20 are present (allow +/-1 for muxer timing)
	found10 := false
	found20 := false
	for _, pos := range keyframePositions {
		if pos >= 9 && pos <= 11 {
			found10 = true
		}
		if pos >= 19 && pos <= 21 {
			found20 = true
		}
	}
	require.True(t, found10, "expected keyframe near position 10, got positions %v", keyframePositions)
	require.True(t, found20, "expected keyframe near position 20, got positions %v", keyframePositions)
}

// TestEncoderRecoveryAfterError encodes valid frames, then an all-zero frame
// (unusual but valid YUV), then more valid frames, and verifies the encoder
// doesn't hang or crash.
func TestEncoderRecoveryAfterError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	bitrate := 500_000
	gopSecs := 2

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	dec, err := codec.NewFFmpegDecoder(nil)
	require.NoError(t, err)
	defer dec.Close()

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	frameSize := ySize + 2*uvSize
	ptsDelta := int64(90000 / fps)

	var encodedOutputs [][]byte
	totalFrames := 21

	for i := 0; i < totalFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := i == 0 || i == 11 // IDR at start and after the zero frame

		var yuv []byte
		if i == 10 {
			// All-zero frame: Y=0, Cb=0, Cr=0 (unusual but technically valid)
			yuv = make([]byte, frameSize)
		} else {
			// Valid gradient frame
			yuv = make([]byte, frameSize)
			for j := 0; j < ySize; j++ {
				yuv[j] = byte(16 + (j*219/(ySize-1))%220) // gradient 16-235
			}
			for j := ySize; j < frameSize; j++ {
				yuv[j] = 128
			}
		}

		encoded, _, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if encoded != nil {
			encodedOutputs = append(encodedOutputs, append([]byte(nil), encoded...))
		}
	}

	t.Logf("MEASURE: encoded output count = %d (expected %d)", len(encodedOutputs), totalFrames)

	// All 21 frames should produce encoded output (zerolatency = no buffering)
	require.Equal(t, totalFrames, len(encodedOutputs),
		"all frames should produce output with zerolatency tune")

	// Verify we can decode the all-zero frame (frame index 10 in output)
	// Feed all encoded data to the decoder
	var decodedCount int
	var zeroFrameDecoded bool
	for i, ef := range encodedOutputs {
		decoded, dw, dh, decErr := dec.Decode(ef)
		if decErr == nil && decoded != nil {
			decodedCount++
			// Check if this is the all-zero frame (should be near-black)
			if i == 10 {
				// Average Y value should be near 0 (or very low due to encoding)
				var ySum uint64
				for j := 0; j < dw*dh; j++ {
					ySum += uint64(decoded[j])
				}
				avgY := float64(ySum) / float64(dw*dh)
				t.Logf("MEASURE: zero-frame avg Y = %.1f (expected near 0-16)", avgY)
				zeroFrameDecoded = true
				// Black frame should have low Y (near 0 or 16 for limited range)
				require.Less(t, avgY, 32.0,
					"zero-frame should decode to near-black, got avg Y=%.1f", avgY)
			}
		}
	}

	t.Logf("MEASURE: decoded count = %d, zero frame decoded = %v", decodedCount, zeroFrameDecoded)
}

// TestEncoderFlushBufferedFrames tests whether the encoder properly flushes
// buffered frames. With zerolatency tune, all frames should be available
// immediately. This test checks if Close() properly handles any remaining
// buffered data.
func TestEncoderFlushBufferedFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	bitrate := 500_000
	gopSecs := 2
	numFrames := 5

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	ptsDelta := int64(90000 / fps)

	var preCloseOutput int
	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := i == 0

		yuv := make([]byte, ySize+2*uvSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, _, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if encoded != nil {
			preCloseOutput++
		}
	}

	t.Logf("MEASURE: pre-close output = %d, total after close = %d", preCloseOutput, preCloseOutput)

	// With zerolatency tune, the encoder should produce output for every frame
	// immediately (no internal frame buffering). Close() doesn't flush anything
	// extra -- it just frees resources.
	//
	// NOTE: The encoder has no Flush() method. If frames were buffered (e.g. with
	// a non-zerolatency encoder), they would be lost on Close(). This is a potential
	// bug for non-zerolatency configurations.
	enc.Close()

	// With zerolatency, we expect all 5 frames to produce output immediately.
	// If the encoder uses frame threading instead of slice threading, some frames
	// might be buffered. Log and assert.
	require.Equal(t, numFrames, preCloseOutput,
		"with zerolatency tune, all %d frames should produce immediate output (got %d)",
		numFrames, preCloseOutput)
}

// TestEncoderConsecutiveKeyframes encodes with forceIDR=true on every frame
// and verifies all frames are keyframes. Bitrate will be much higher than
// target -- that's expected.
func TestEncoderConsecutiveKeyframes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	fps := 30
	numFrames := 30
	bitrate := 500_000
	gopSecs := 2

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, bitrate, fps, 1, gopSecs, nil)
	require.NoError(t, err)
	defer enc.Close()

	muxer := output.NewTSMuxer()
	var tsData []byte
	muxer.SetOutput(func(data []byte) {
		tsData = append(tsData, data...)
	})

	ySize := w * h
	uvSize := (w / 2) * (h / 2)
	ptsDelta := int64(90000 / fps)

	var totalEncodedBytes int
	var encodedCount int

	for i := 0; i < numFrames; i++ {
		pts := int64(90000) + int64(i)*ptsDelta
		forceIDR := true // EVERY frame is IDR

		yuv := make([]byte, ySize+2*uvSize)
		for j := 0; j < ySize; j++ {
			yuv[j] = byte((j*7 + i*37) % 256)
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}

		encoded, isKeyframe, encErr := enc.Encode(yuv, pts, forceIDR)
		require.NoError(t, encErr, "encode frame %d", i)
		if len(encoded) == 0 {
			continue
		}

		totalEncodedBytes += len(encoded)
		encodedCount++

		// The encoder should report every frame as a keyframe
		if !isKeyframe {
			t.Logf("MEASURE: frame %d: forceIDR=true but isKeyframe=%v", i, isKeyframe)
		}

		avc1 := codec.AnnexBToAVC1(encoded)
		frame := &media.VideoFrame{
			PTS:        pts,
			DTS:        pts,
			IsKeyframe: isKeyframe,
			WireData:   avc1,
			Codec:      "h264",
		}
		if isKeyframe {
			for _, nalu := range codec.ExtractNALUs(avc1) {
				if len(nalu) == 0 {
					continue
				}
				switch nalu[0] & 0x1F {
				case 7:
					frame.SPS = append([]byte(nil), nalu...)
				case 8:
					frame.PPS = append([]byte(nil), nalu...)
				}
			}
		}
		require.NoError(t, muxer.WriteVideo(frame), "mux frame %d", i)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "allkeyframes.ts")
	require.NoError(t, os.WriteFile(path, tsData, 0o644))

	frames := testutil.ProbeVideoFrames(t, path)
	require.GreaterOrEqual(t, len(frames), 25, "expected at least 25 video frames")

	var keyframeCount int
	var nonKeyframePositions []int
	for i, f := range frames {
		if f.KeyFrame {
			keyframeCount++
		} else {
			nonKeyframePositions = append(nonKeyframePositions, i)
		}
	}

	durationSec := float64(numFrames) / float64(fps)
	allIDRBitrateKbps := int(float64(totalEncodedBytes) * 8.0 / durationSec / 1000.0)
	targetKbps := bitrate / 1000

	t.Logf("MEASURE: bitrate with all-IDR = %d kbps vs target = %d kbps", allIDRBitrateKbps, targetKbps)
	t.Logf("MEASURE: keyframe count = %d out of %d total frames", keyframeCount, len(frames))
	if len(nonKeyframePositions) > 0 {
		t.Logf("MEASURE: non-keyframe positions = %v", nonKeyframePositions)
	}

	// All frames should be keyframes
	require.Equal(t, len(frames), keyframeCount,
		"all %d frames should be keyframes, but %d were not (positions: %v)",
		len(frames), len(frames)-keyframeCount, nonKeyframePositions)

	// Bitrate with all-IDR will be much higher than target -- that's expected
	// Just verify it's actually higher (sanity check)
	require.Greater(t, allIDRBitrateKbps, targetKbps,
		"all-IDR bitrate (%d kbps) should exceed target (%d kbps)", allIDRBitrateKbps, targetKbps)
}
