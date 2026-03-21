package asr

import (
	"testing"
)

func TestAudioRingBuf_WriteAndSnapshot(t *testing.T) {
	// Write 1 second of audio at 16kHz, verify snapshot has 16000 samples.
	buf := NewAudioRingBuf(16000, 30)

	samples := make([]float32, 16000)
	for i := range samples {
		samples[i] = float32(i) / 16000.0
	}
	buf.Write(samples)

	snap := buf.Snapshot()
	if len(snap) != 16000 {
		t.Fatalf("expected 16000 samples, got %d", len(snap))
	}
	// Verify first and last sample values.
	if snap[0] != 0.0 {
		t.Errorf("expected first sample 0.0, got %f", snap[0])
	}
	if snap[15999] != float32(15999)/16000.0 {
		t.Errorf("expected last sample %f, got %f", float32(15999)/16000.0, snap[15999])
	}
}

func TestAudioRingBuf_WrapsAt30s(t *testing.T) {
	// Write 35 seconds of audio. Only the last 30 seconds should be retained.
	const sampleRate = 16000
	const maxSec = 30
	buf := NewAudioRingBuf(sampleRate, maxSec)

	// Write 35 seconds in 1-second chunks.
	for sec := 0; sec < 35; sec++ {
		chunk := make([]float32, sampleRate)
		for i := range chunk {
			// Encode the second number into each sample for identification.
			chunk[i] = float32(sec)
		}
		buf.Write(chunk)
	}

	snap := buf.Snapshot()
	expectedLen := sampleRate * maxSec
	if len(snap) != expectedLen {
		t.Fatalf("expected %d samples, got %d", expectedLen, len(snap))
	}

	// The first sample should be from second 5 (seconds 0-4 were overwritten).
	if snap[0] != 5.0 {
		t.Errorf("expected first sample to be from second 5, got %f", snap[0])
	}
	// The last sample should be from second 34.
	if snap[len(snap)-1] != 34.0 {
		t.Errorf("expected last sample to be from second 34, got %f", snap[len(snap)-1])
	}
}

func TestAudioRingBuf_PartialFill(t *testing.T) {
	buf := NewAudioRingBuf(16000, 30)

	samples := make([]float32, 100)
	for i := range samples {
		samples[i] = float32(i)
	}
	buf.Write(samples)

	snap := buf.Snapshot()
	if len(snap) != 100 {
		t.Fatalf("expected 100 samples, got %d", len(snap))
	}
	for i := range snap {
		if snap[i] != float32(i) {
			t.Errorf("sample %d: expected %f, got %f", i, float32(i), snap[i])
			break
		}
	}
}

func TestAudioRingBuf_Clear(t *testing.T) {
	buf := NewAudioRingBuf(16000, 30)

	buf.Write(make([]float32, 1000))
	if buf.SampleCount() != 1000 {
		t.Fatalf("expected 1000 samples before clear, got %d", buf.SampleCount())
	}

	buf.Clear()

	if buf.SampleCount() != 0 {
		t.Errorf("expected 0 samples after clear, got %d", buf.SampleCount())
	}
	snap := buf.Snapshot()
	if snap != nil {
		t.Errorf("expected nil snapshot after clear, got len %d", len(snap))
	}
}

func TestAudioRingBuf_SampleCount(t *testing.T) {
	buf := NewAudioRingBuf(16000, 30)

	if buf.SampleCount() != 0 {
		t.Fatalf("expected 0 samples initially, got %d", buf.SampleCount())
	}

	// Write 500 samples.
	buf.Write(make([]float32, 500))
	if buf.SampleCount() != 500 {
		t.Errorf("expected 500 samples, got %d", buf.SampleCount())
	}

	// Write 1000 more.
	buf.Write(make([]float32, 1000))
	if buf.SampleCount() != 1500 {
		t.Errorf("expected 1500 samples, got %d", buf.SampleCount())
	}

	// Write enough to fill and wrap (total capacity = 16000 * 30 = 480000).
	buf.Write(make([]float32, 480000))
	if buf.SampleCount() != 480000 {
		t.Errorf("expected 480000 samples (capped at capacity), got %d", buf.SampleCount())
	}
}

func TestAudioRingBuf_EmptySnapshot(t *testing.T) {
	buf := NewAudioRingBuf(16000, 30)
	snap := buf.Snapshot()
	if snap != nil {
		t.Errorf("expected nil snapshot for empty buffer, got len %d", len(snap))
	}
}

func TestAudioRingBuf_MultipleSmallWrites(t *testing.T) {
	// Verify that multiple small writes produce a correct contiguous snapshot.
	buf := NewAudioRingBuf(16000, 30)

	for i := 0; i < 100; i++ {
		buf.Write([]float32{float32(i)})
	}

	snap := buf.Snapshot()
	if len(snap) != 100 {
		t.Fatalf("expected 100 samples, got %d", len(snap))
	}
	for i := range snap {
		if snap[i] != float32(i) {
			t.Errorf("sample %d: expected %f, got %f", i, float32(i), snap[i])
			break
		}
	}
}
