package clock

import (
	"encoding/binary"
	"math"
	"testing"
)

// mockEncode returns the input PCM length encoded as 4 bytes, followed by
// a copy of the PCM data as little-endian float32. This lets tests inspect
// what was fed to the encoder.
func mockEncode(pcm []float32) ([]byte, error) {
	out := make([]byte, 4+len(pcm)*4)
	binary.LittleEndian.PutUint32(out[:4], uint32(len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint32(out[4+i*4:], math.Float32bits(v))
	}
	return out, nil
}

func TestAACBufferExactFrame(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(1000)

	// Feed exactly 1024 stereo samples = 2048 float32 values.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = float32(i) * 0.001
	}

	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if frames[0].PTS != 1000 {
		t.Errorf("expected PTS=1000, got %d", frames[0].PTS)
	}
	if frames[0].SampleRate != 48000 {
		t.Errorf("expected SampleRate=48000, got %d", frames[0].SampleRate)
	}
	if frames[0].Channels != 2 {
		t.Errorf("expected Channels=2, got %d", frames[0].Channels)
	}
	if buf.Pending() != 0 {
		t.Errorf("expected 0 pending, got %d", buf.Pending())
	}
	if buf.FrameCount() != 1 {
		t.Errorf("expected FrameCount=1, got %d", buf.FrameCount())
	}
}

func TestAACBufferPartialAccumulation(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)

	// Feed 3202 float32 values = 1601 stereo samples.
	// 1601 >= 1024, so 1 frame emitted, remainder = 1601 - 1024 = 577 samples.
	pcm := make([]float32, 3202)
	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}
	if buf.Pending() != 577 {
		t.Errorf("expected 577 pending samples, got %d", buf.Pending())
	}
}

func TestAACBufferMultipleFrames(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)

	// Feed 4358 float32 values = 2179 stereo samples.
	// 2179 / 1024 = 2 full frames, remainder = 2179 - 2048 = 131 samples.
	pcm := make([]float32, 4358)
	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if buf.Pending() != 131 {
		t.Errorf("expected 131 pending samples, got %d", buf.Pending())
	}
}

func TestAACBufferPTSProgression(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(5000)

	// frameDuration90k = 1024 * 90000 / 48000 = 1920
	const frameDuration = int64(1920)

	// Feed 10 chunks of exactly 1024 stereo samples each.
	for i := 0; i < 10; i++ {
		pcm := make([]float32, 2048)
		frames, err := buf.Feed(pcm)
		if err != nil {
			t.Fatalf("Feed %d error: %v", i, err)
		}
		if len(frames) != 1 {
			t.Fatalf("chunk %d: expected 1 frame, got %d", i, len(frames))
		}
		expectedPTS := int64(5000) + int64(i)*frameDuration
		if frames[0].PTS != expectedPTS {
			t.Errorf("chunk %d: expected PTS=%d, got %d", i, expectedPTS, frames[0].PTS)
		}
	}

	if buf.FrameCount() != 10 {
		t.Errorf("expected FrameCount=10, got %d", buf.FrameCount())
	}
}

func TestAACBufferWithMockEncoder(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(0)

	// Fill with known values.
	pcm := make([]float32, 2048)
	for i := range pcm {
		pcm[i] = float32(i)
	}

	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	data := frames[0].Data
	// First 4 bytes = sample count (2048).
	sampleCount := binary.LittleEndian.Uint32(data[:4])
	if sampleCount != 2048 {
		t.Errorf("expected encoded sample count 2048, got %d", sampleCount)
	}

	// Verify first and last PCM values survived the encode round-trip.
	first := math.Float32frombits(binary.LittleEndian.Uint32(data[4:8]))
	if first != 0.0 {
		t.Errorf("expected first sample 0.0, got %f", first)
	}
	last := math.Float32frombits(binary.LittleEndian.Uint32(data[4+2047*4:]))
	if last != 2047.0 {
		t.Errorf("expected last sample 2047.0, got %f", last)
	}
}

func TestAACBufferFlush(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(0)

	// Feed 1000 float32 values = 500 stereo samples (less than 1024).
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = 1.0 // non-zero so we can verify padding
	}

	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("expected 0 frames before flush, got %d", len(frames))
	}
	if buf.Pending() != 500 {
		t.Errorf("expected 500 pending, got %d", buf.Pending())
	}

	// Flush should emit 1 padded frame.
	frames, err = buf.Flush()
	if err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 flushed frame, got %d", len(frames))
	}

	// Verify the encoded data: first 4 bytes = count (2048 = 1024*2 channels).
	data := frames[0].Data
	sampleCount := binary.LittleEndian.Uint32(data[:4])
	if sampleCount != 2048 {
		t.Errorf("expected 2048 encoded values, got %d", sampleCount)
	}

	// Verify padding: values [1000..2047] should be 0.0 (silence).
	for i := 1000; i < 2048; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(data[4+i*4:]))
		if v != 0.0 {
			t.Errorf("padding sample %d: expected 0.0, got %f", i, v)
			break
		}
	}

	// Verify original data: values [0..999] should be 1.0.
	for i := 0; i < 1000; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(data[4+i*4:]))
		if v != 1.0 {
			t.Errorf("original sample %d: expected 1.0, got %f", i, v)
			break
		}
	}

	if buf.Pending() != 0 {
		t.Errorf("expected 0 pending after flush, got %d", buf.Pending())
	}
}

func TestAACBuffer29_97Pattern(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(0)
	acc := NewAudioAccumulator(48000, 30000, 1001)

	totalSamples := 0
	totalFrames := 0

	for tick := 0; tick < 30; tick++ {
		samples := acc.SamplesForTick()
		totalSamples += samples

		// Generate interleaved stereo PCM.
		pcm := make([]float32, samples*2)
		frames, err := buf.Feed(pcm)
		if err != nil {
			t.Fatalf("tick %d: Feed error: %v", tick, err)
		}
		totalFrames += len(frames)
	}

	// 30 ticks at 29.97fps = ~1.001 seconds.
	// At 48kHz, that's ~48048 samples.
	// Each AAC frame = 1024 samples, so ~48048/1024 ≈ 46.9 → 46 full frames.
	// The remaining samples stay pending.
	expectedFrames := totalSamples / 1024
	if totalFrames != expectedFrames {
		t.Errorf("expected %d AAC frames from %d samples, got %d", expectedFrames, totalSamples, totalFrames)
	}

	expectedPending := totalSamples - expectedFrames*1024
	if buf.Pending() != expectedPending {
		t.Errorf("expected %d pending, got %d", expectedPending, buf.Pending())
	}

	// Sanity: should be approximately 46-47 frames.
	if totalFrames < 45 || totalFrames > 48 {
		t.Errorf("AAC frames %d outside expected range [45,48]", totalFrames)
	}

	t.Logf("29.97fps: %d ticks, %d total samples, %d AAC frames, %d pending",
		30, totalSamples, totalFrames, buf.Pending())
}

// Issue 5: 44.1kHz AAC PTS truncation drift test.
// frameDuration90k = 1024 * 90000 / 44100 = 2088.435... truncates to 2088.
// Over 1 second (43 frames at 44100Hz), PTS drifts by ~216 ticks.
func TestAACBufferPTSAccuracy44100(t *testing.T) {
	buf := NewAACFrameBuffer(2, 44100, mockEncode)
	buf.SetBasePTS(0)

	// Feed exactly 1 second of audio at 44100Hz: 44100 samples.
	// Each feed: 1024 stereo samples = 2048 float32.
	// 44100 / 1024 = 43.066... so 43 full frames, 76 samples remaining.
	totalSamples := 0
	for totalSamples < 44100 {
		chunk := 1024
		if totalSamples+chunk > 44100 {
			chunk = 44100 - totalSamples
		}
		pcm := make([]float32, chunk*2) // stereo
		_, err := buf.Feed(pcm)
		if err != nil {
			t.Fatalf("Feed error at sample %d: %v", totalSamples, err)
		}
		totalSamples += chunk
	}

	// 43 full frames emitted. The PTS of frame 43 (0-indexed: frame index 42)
	// should represent exactly 42 * 1024 / 44100 seconds = 42 * 1024 * 90000 / 44100
	// = 42 * 92160000 / 44100 = 42 * 2088.435... = 87714.29...
	// With Bresenham: frame 42 PTS should be exact integer closest to 87714.
	// With truncation bug: frame 42 PTS = 42 * 2088 = 87696 (18 ticks short).
	//
	// More importantly, frame 42's PTS should be: 42 * 1024 * 90000 / 44100
	// = 3,870,720,000 / 44100 = 87,771.428... wait, let me recompute.
	// 1024 * 90000 = 92,160,000 per frame
	// frame N PTS = N * 92,160,000 / 44100
	// frame 42 PTS = 42 * 92,160,000 / 44100 = 3,870,720,000 / 44100 = 87,771.428...
	// With truncation: 42 * 2088 = 87,696 (75 ticks short)
	// With Bresenham: should be 87,771 or 87,772 (within 1 of exact)

	// Check the last emitted frame's PTS.
	frameCount := buf.FrameCount()
	if frameCount != 43 {
		t.Fatalf("expected 43 frames from 44100 samples, got %d", frameCount)
	}

	// Feed one more complete frame to get frame 43's PTS and check it.
	// Frame 43 PTS should be 43 * 92160000 / 44100 = 43 * 2088.435... = 89802.72...
	// With truncation: 43 * 2088 = 89784 (18.72 ticks short)
	// With Bresenham: should be 89802 or 89803
	pcm := make([]float32, 1024*2)
	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	// We had 76 remaining + 1024 new = 1100 samples. That's 1 frame (1024) with 76 remaining.
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	// Frame 43 (index 43) PTS
	frame43PTS := frames[0].PTS
	exactPTS := float64(43) * float64(1024) * float64(90000) / float64(44100)
	// With truncation: 43 * 2088 = 89784
	// Exact: 89802.72...
	// Bresenham should give 89802 or 89803
	drift := float64(frame43PTS) - exactPTS
	if drift < 0 {
		drift = -drift
	}
	if drift > 1.0 {
		t.Fatalf("44.1kHz AAC PTS drift at frame 43: PTS=%d, exact=%.2f, drift=%.2f (>1 tick)",
			frame43PTS, exactPTS, drift)
	}
}

// Issue 12: Flush produces frame from partial data (already tested in TestAACBufferFlush,
// but let's add a specific test for sync loop shutdown scenario).
func TestAACBufferFlushOnShutdown(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)
	buf.SetBasePTS(90000)

	// Feed 500 stereo samples (less than 1024).
	pcm := make([]float32, 500*2)
	for i := range pcm {
		pcm[i] = 0.5
	}
	frames, err := buf.Feed(pcm)
	if err != nil {
		t.Fatalf("Feed error: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("should not produce frames before 1024 samples, got %d", len(frames))
	}

	// Flush (simulating shutdown) should produce exactly 1 padded frame.
	frames, err = buf.Flush()
	if err != nil {
		t.Fatalf("Flush error: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("Flush should produce 1 frame, got %d", len(frames))
	}

	// Verify the frame has the correct base PTS.
	if frames[0].PTS != 90000 {
		t.Errorf("Flush frame PTS: expected 90000, got %d", frames[0].PTS)
	}

	// Verify buffer is empty after flush.
	if buf.Pending() != 0 {
		t.Errorf("expected 0 pending after flush, got %d", buf.Pending())
	}
}

func TestAACBufferEmptyFeed(t *testing.T) {
	buf := NewAACFrameBuffer(2, 48000, mockEncode)

	frames, err := buf.Feed(nil)
	if err != nil {
		t.Fatalf("Feed nil error: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("expected 0 frames from nil, got %d", len(frames))
	}

	frames, err = buf.Feed([]float32{})
	if err != nil {
		t.Fatalf("Feed empty error: %v", err)
	}
	if len(frames) != 0 {
		t.Errorf("expected 0 frames from empty, got %d", len(frames))
	}

	if buf.Pending() != 0 {
		t.Errorf("expected 0 pending, got %d", buf.Pending())
	}
	if buf.FrameCount() != 0 {
		t.Errorf("expected FrameCount=0, got %d", buf.FrameCount())
	}
}
