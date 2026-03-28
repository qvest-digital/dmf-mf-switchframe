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
