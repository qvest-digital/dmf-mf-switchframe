//go:build correctness

package encode

import (
	"runtime"
	"testing"

	"github.com/zsiec/switchframe/server/codec"
	"github.com/zsiec/switchframe/server/correctness/testutil"
)

// TestDecoderRapidCreateDestroy creates and closes 50 decoder instances
// in a tight loop, measuring heap growth to detect gross memory leaks.
func TestDecoderRapidCreateDestroy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	// Force GC and measure baseline.
	runtime.GC()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	const iterations = 50
	for i := 0; i < iterations; i++ {
		dec, err := codec.NewFFmpegDecoder(nil)
		if err != nil {
			t.Fatalf("iteration %d: failed to create decoder: %v", i, err)
		}
		dec.Close()
	}

	// Force GC and measure after.
	runtime.GC()
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	beforeMB := memBefore.HeapAlloc / (1024 * 1024)
	afterMB := memAfter.HeapAlloc / (1024 * 1024)
	var deltaMB int64
	if memAfter.HeapAlloc > memBefore.HeapAlloc {
		deltaMB = int64(memAfter.HeapAlloc-memBefore.HeapAlloc) / (1024 * 1024)
	} else {
		deltaMB = -int64(memBefore.HeapAlloc-memAfter.HeapAlloc) / (1024 * 1024)
	}

	t.Logf("MEASURE: heap before=%dMB, after=%dMB, delta=%dMB", beforeMB, afterMB, deltaMB)

	if deltaMB > 50 {
		t.Errorf("heap grew by %dMB after %d create/destroy cycles — possible memory leak", deltaMB, iterations)
	}
}

// TestDecoderInterleavedStreams encodes two streams with different content
// (different SPS/PPS) and feeds alternating frames to a single decoder.
// The decoder should not crash or panic, though output may be wrong.
func TestDecoderInterleavedStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	numFrames := 15

	// Encoder A: solid red (Y=81 in BT.709 limited range, Cb=90, Cr=240)
	encA, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 1, nil)
	if err != nil {
		t.Fatalf("failed to create encoder A: %v", err)
	}
	defer encA.Close()

	// Encoder B: solid blue (Y=41 in BT.709 limited range, Cb=240, Cr=110)
	// Use different resolution to guarantee different SPS/PPS.
	wB, hB := 160, 120
	encB, err := codec.NewFFmpegEncoder("libx264", wB, hB, 300_000, 30, 1, 1, nil)
	if err != nil {
		t.Fatalf("failed to create encoder B: %v", err)
	}
	defer encB.Close()

	yuvA := testutil.GenerateSolidYUV420(w, h, 81, 90, 240)
	yuvB := testutil.GenerateSolidYUV420(wB, hB, 41, 240, 110)

	// Encode stream A NALUs.
	var nalusA [][]byte
	for i := 0; i < numFrames; i++ {
		data, _, encErr := encA.Encode(yuvA, int64(i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode A frame %d: %v", i, encErr)
		}
		if data != nil {
			nalusA = append(nalusA, append([]byte(nil), data...))
		}
	}

	// Encode stream B NALUs.
	var nalusB [][]byte
	for i := 0; i < numFrames; i++ {
		data, _, encErr := encB.Encode(yuvB, int64(i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode B frame %d: %v", i, encErr)
		}
		if data != nil {
			nalusB = append(nalusB, append([]byte(nil), data...))
		}
	}

	if len(nalusA) == 0 || len(nalusB) == 0 {
		t.Fatal("encoders produced no output")
	}

	// Create a single decoder and feed alternating frames.
	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	decodedCount := 0
	errorCount := 0
	maxY := byte(0)

	minLen := len(nalusA)
	if len(nalusB) < minLen {
		minLen = len(nalusB)
	}

	// Feed interleaved: A[0], B[0], A[1], B[1], ...
	// This must not crash or panic.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				t.Errorf("PANIC during interleaved decode: %v", r)
			}
		}()

		for i := 0; i < minLen; i++ {
			// Feed A
			decoded, dw, _, decErr := dec.Decode(nalusA[i])
			if decErr != nil {
				errorCount++
			} else if decoded != nil {
				decodedCount++
				// Scan Y plane for max value.
				yPlaneSize := dw * (len(decoded) * 2 / 3 / dw) // approximate
				if yPlaneSize > len(decoded) {
					yPlaneSize = len(decoded)
				}
				for j := 0; j < yPlaneSize && j < len(decoded); j++ {
					if decoded[j] > maxY {
						maxY = decoded[j]
					}
				}
			}

			// Feed B
			decoded, dw, _, decErr = dec.Decode(nalusB[i])
			if decErr != nil {
				errorCount++
			} else if decoded != nil {
				decodedCount++
				yPlaneSize := dw * (len(decoded) * 2 / 3 / dw)
				if yPlaneSize > len(decoded) {
					yPlaneSize = len(decoded)
				}
				for j := 0; j < yPlaneSize && j < len(decoded); j++ {
					if decoded[j] > maxY {
						maxY = decoded[j]
					}
				}
			}
		}
	}()

	t.Logf("MEASURE: %d frames decoded, %d errors, max decoded Y = %d", decodedCount, errorCount, maxY)

	if didPanic {
		t.Error("decoder panicked on interleaved streams — must not crash")
	}
}

// TestDecoderMissingSPSPPS encodes frames and feeds only P-frames (skipping the
// initial keyframe) to a fresh decoder. The decoder should error until it
// receives a keyframe, then recover.
func TestDecoderMissingSPSPPS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	numFrames := 15

	// Use gopSecs=1 to get keyframes every 30 frames.
	// With 15 frames and forceIDR only on frame 0, frame 0 is the only keyframe.
	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 1, nil)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	yuv := testutil.GenerateGradientYUV420(w, h)

	var nalus [][]byte
	var isKeyframe []bool
	for i := 0; i < numFrames; i++ {
		data, kf, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode frame %d: %v", i, encErr)
		}
		if data != nil {
			nalus = append(nalus, append([]byte(nil), data...))
			isKeyframe = append(isKeyframe, kf)
		}
	}

	if len(nalus) < 2 {
		t.Fatal("encoder produced fewer than 2 NALUs, can't test")
	}

	// Verify first NALU is a keyframe.
	if !isKeyframe[0] {
		t.Fatal("first encoded NALU is not a keyframe")
	}

	// Create decoder and skip the first keyframe — feed only P-frames.
	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	errorsBeforeKey := 0
	framesBeforeKey := 0

	// Feed P-frames (skip frame 0 which is the keyframe).
	for i := 1; i < len(nalus); i++ {
		decoded, _, _, decErr := dec.Decode(nalus[i])
		if decErr != nil {
			errorsBeforeKey++
		} else if decoded != nil {
			framesBeforeKey++
		}
	}

	// Now flush and feed the keyframe — decoder should recover.
	dec.Flush()

	framesAfterKey := 0
	// Feed the keyframe.
	decoded, _, _, decErr := dec.Decode(nalus[0])
	if decErr == nil && decoded != nil {
		framesAfterKey++
	}
	// Feed remaining P-frames after keyframe to fill the decoder pipeline.
	for i := 1; i < len(nalus); i++ {
		decoded, _, _, decErr = dec.Decode(nalus[i])
		if decErr == nil && decoded != nil {
			framesAfterKey++
		}
	}

	t.Logf("MEASURE: errors before keyframe = %d, frames decoded before keyframe = %d, frames after keyframe = %d",
		errorsBeforeKey, framesBeforeKey, framesAfterKey)

	// Without SPS/PPS, the decoder should produce errors or no output.
	// After feeding a keyframe, it should recover and produce some output.
	if framesAfterKey == 0 {
		t.Error("decoder produced no frames after receiving keyframe — expected recovery")
	}
}

// TestDecoderFrameReorderingRecovery encodes 30 frames and feeds them in
// locally reordered groups of 3. The decoder may error but must not crash.
func TestDecoderFrameReorderingRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	numFrames := 30

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 2, nil)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	yuv := testutil.GenerateGradientYUV420(w, h)

	var nalus [][]byte
	for i := 0; i < numFrames; i++ {
		data, _, encErr := enc.Encode(yuv, int64(i*3000), i == 0)
		if encErr != nil {
			t.Fatalf("encode frame %d: %v", i, encErr)
		}
		if data != nil {
			nalus = append(nalus, append([]byte(nil), data...))
		}
	}

	if len(nalus) < 6 {
		t.Fatalf("encoder produced only %d NALUs, need at least 6", len(nalus))
	}

	// Create reordered sequence: [0, 1, 4, 3, 2, 5, 8, 7, 6, 9, ...]
	// Within each group of 3 (starting at offset 2), reverse the order.
	reordered := make([][]byte, len(nalus))
	for i := 0; i < len(nalus); i++ {
		reordered[i] = nalus[i]
	}
	// Swap within groups of 3, starting from index 2.
	for groupStart := 2; groupStart+2 < len(reordered); groupStart += 3 {
		reordered[groupStart], reordered[groupStart+2] = reordered[groupStart+2], reordered[groupStart]
	}

	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	decodedCount := 0
	errorCount := 0

	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				t.Errorf("PANIC during reordered decode: %v", r)
			}
		}()

		for _, nalu := range reordered {
			decoded, _, _, decErr := dec.Decode(nalu)
			if decErr != nil {
				errorCount++
			} else if decoded != nil {
				decodedCount++
			}
		}
	}()

	t.Logf("MEASURE: %d decoded, %d errors out of %d fed", decodedCount, errorCount, len(reordered))

	if didPanic {
		t.Error("decoder panicked on reordered frames — must not crash")
	}
}

// TestDecoderEmptyInput feeds empty and nil byte slices to the decoder,
// verifying it returns errors and does not panic.
func TestDecoderEmptyInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	// Test 1: empty byte slice.
	t.Run("empty_slice", func(t *testing.T) {
		didPanic := false
		var decErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					didPanic = true
					t.Errorf("PANIC on empty slice: %v", r)
				}
			}()
			_, _, _, decErr = dec.Decode([]byte{})
		}()

		t.Logf("MEASURE: empty slice error=%v, panic=%v", decErr, didPanic)

		if didPanic {
			t.Error("decoder panicked on empty input — must return error")
		}
		if decErr == nil {
			t.Error("decoder should return error for empty input, got nil")
		}
	})

	// Test 2: nil input.
	t.Run("nil_input", func(t *testing.T) {
		didPanic := false
		var decErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					didPanic = true
					t.Errorf("PANIC on nil input: %v", r)
				}
			}()
			_, _, _, decErr = dec.Decode(nil)
		}()

		t.Logf("MEASURE: nil input error=%v, panic=%v", decErr, didPanic)

		if didPanic {
			t.Error("decoder panicked on nil input — must return error")
		}
		if decErr == nil {
			t.Error("decoder should return error for nil input, got nil")
		}
	})
}

// TestDecoderRepeatedKeyframes encodes 5 IDR keyframes of the same content
// and feeds them to a decoder. All should decode successfully.
func TestDecoderRepeatedKeyframes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long correctness test in short mode")
	}

	w, h := 320, 240
	numKeyframes := 5

	enc, err := codec.NewFFmpegEncoder("libx264", w, h, 500_000, 30, 1, 2, nil)
	if err != nil {
		t.Fatalf("failed to create encoder: %v", err)
	}
	defer enc.Close()

	yuv := testutil.GenerateSolidYUV420(w, h, 128, 128, 128)

	// Encode 5 keyframes (forceIDR=true each time).
	var keyframeNALUs [][]byte
	for i := 0; i < numKeyframes; i++ {
		data, isKey, encErr := enc.Encode(yuv, int64(i*3000), true)
		if encErr != nil {
			t.Fatalf("encode keyframe %d: %v", i, encErr)
		}
		if data != nil {
			if !isKey {
				t.Logf("WARNING: frame %d was not flagged as keyframe despite forceIDR=true", i)
			}
			keyframeNALUs = append(keyframeNALUs, append([]byte(nil), data...))
		}
	}

	if len(keyframeNALUs) == 0 {
		t.Fatal("encoder produced no keyframes")
	}

	dec, err := codec.NewFFmpegDecoder(nil)
	if err != nil {
		t.Fatalf("failed to create decoder: %v", err)
	}
	defer dec.Close()

	decodedCount := 0
	var decodedFrames [][]byte

	for i, nalu := range keyframeNALUs {
		decoded, dw, dh, decErr := dec.Decode(nalu)
		if decErr != nil {
			t.Logf("keyframe %d: decode error: %v", i, decErr)
			continue
		}
		if decoded != nil {
			decodedCount++
			decodedFrames = append(decodedFrames, append([]byte(nil), decoded...))
			t.Logf("keyframe %d: decoded %dx%d (%d bytes)", i, dw, dh, len(decoded))
		} else {
			t.Logf("keyframe %d: no output (buffering)", i)
		}
	}

	t.Logf("MEASURE: %d decoded from %d keyframes", decodedCount, len(keyframeNALUs))

	if decodedCount == 0 {
		t.Error("decoder produced no output from repeated keyframes")
	}

	// All decoded frames should be similar (same solid content).
	if len(decodedFrames) >= 2 {
		psnr := testutil.YUVFramePSNR(decodedFrames[0], decodedFrames[len(decodedFrames)-1], w, h)
		t.Logf("MEASURE: PSNR between first and last decoded keyframe = %.1f dB", psnr)
		if psnr < 30.0 {
			t.Errorf("decoded keyframes should be similar (same content), but PSNR=%.1f dB", psnr)
		}
	}
}
