package mxl

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- Mock discrete writer (video) ---

type mockDiscreteWriter struct {
	mu     sync.Mutex
	grains []writtenGrain
	closed bool
}

type writtenGrain struct {
	index uint64
	data  []byte
}

func (m *mockDiscreteWriter) WriteGrain(index uint64, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.grains = append(m.grains, writtenGrain{index: index, data: cp})
	return nil
}

func (m *mockDiscreteWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockDiscreteWriter) getGrains() []writtenGrain {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]writtenGrain, len(m.grains))
	copy(cp, m.grains)
	return cp
}

// --- Mock continuous writer (audio) ---

type mockContinuousWriter struct {
	mu      sync.Mutex
	samples []writtenSamples
	closed  bool
}

type writtenSamples struct {
	index    uint64
	channels [][]float32
}

func (m *mockContinuousWriter) WriteSamples(index uint64, channels [][]float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([][]float32, len(channels))
	for i, ch := range channels {
		cp[i] = make([]float32, len(ch))
		copy(cp[i], ch)
	}
	m.samples = append(m.samples, writtenSamples{index: index, channels: cp})
	return nil
}

func (m *mockContinuousWriter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockContinuousWriter) getSamples() []writtenSamples {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]writtenSamples, len(m.samples))
	copy(cp, m.samples)
	return cp
}

// --- Tests ---

func TestWriter_WritesVideoGrain(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	w.SetVideoWriter(mock, Rational{30, 1})

	// Start the ticker so grains actually get written.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Create a minimal YUV420p frame (12x2 = 12*2 + 6*1 + 6*1 = 36 bytes).
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	// Set Y=16 (limited range black), Cb=Cr=128.
	for i := 0; i < width*height; i++ {
		yuv[i] = 16
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128
	}

	w.WriteVideo(yuv, width, height, 0)

	// Wait for the ticker to fire and write the grain.
	deadline := time.After(200 * time.Millisecond)
	for {
		grains := mock.getGrains()
		if len(grains) >= 1 {
			if len(grains[0].data) == 0 {
				t.Fatal("expected non-empty V210 data")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for grain write, got %d grains", len(mock.getGrains()))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestWriter_WritesAudioSamples(t *testing.T) {
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	w.SetAudioWriter(mock, Rational{48000, 1})

	// Interleaved stereo PCM: L0, R0, L1, R1.
	pcm := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6}
	w.WriteAudio(pcm, 0, 48000, 2)

	samples := mock.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample batch, got %d", len(samples))
	}

	// Should be de-interleaved: 2 channels, 3 samples each.
	if len(samples[0].channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(samples[0].channels))
	}
	if len(samples[0].channels[0]) != 3 {
		t.Fatalf("expected 3 samples per channel, got %d", len(samples[0].channels[0]))
	}

	// Verify de-interleaving.
	wantL := []float32{0.1, 0.3, 0.5}
	wantR := []float32{0.2, 0.4, 0.6}
	for i := range wantL {
		if samples[0].channels[0][i] != wantL[i] {
			t.Fatalf("L[%d] = %f, want %f", i, samples[0].channels[0][i], wantL[i])
		}
		if samples[0].channels[1][i] != wantR[i] {
			t.Fatalf("R[%d] = %f, want %f", i, samples[0].channels[1][i], wantR[i])
		}
	}
}

func TestWriter_StopsOnContextCancel(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	// Write a video frame and wait for the ticker to pick it up.
	yuv := make([]byte, 12*2*3/2)
	w.WriteVideo(yuv, 12, 2, 0)

	deadline := time.After(200 * time.Millisecond)
	for len(vMock.getGrains()) < 1 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for grain before cancel")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	time.Sleep(50 * time.Millisecond) // Allow goroutine to close.

	countBefore := len(vMock.getGrains())

	// Writes after close should be silently dropped (no new grains from ticker).
	w.WriteVideo(yuv, 12, 2, 1)
	time.Sleep(100 * time.Millisecond)

	if len(vMock.getGrains()) != countBefore {
		t.Fatal("expected no new grains after close")
	}
}

func TestWriter_ClosesFlows(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2, SampleRate: 48000, Channels: 2})
	w.SetVideoWriter(vMock, Rational{30, 1})
	w.SetAudioWriter(aMock, Rational{48000, 1})

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if !vMock.closed {
		t.Fatal("expected video writer to be closed")
	}
	if !aMock.closed {
		t.Fatal("expected audio writer to be closed")
	}
}

func TestWriter_NilWritersNoOp(t *testing.T) {
	w := NewWriter(WriterConfig{})

	// Should not panic with nil writers.
	w.WriteVideo(make([]byte, 36), 12, 2, 0)
	w.WriteAudio([]float32{0.1, 0.2}, 0, 48000, 2)

	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestWriter_DeinterleavesMono(t *testing.T) {
	mock := &mockContinuousWriter{}
	w := NewWriter(WriterConfig{SampleRate: 48000, Channels: 1})
	w.SetAudioWriter(mock, Rational{48000, 1})

	pcm := []float32{0.1, 0.2, 0.3}
	w.WriteAudio(pcm, 0, 48000, 1)

	samples := mock.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(samples))
	}
	if len(samples[0].channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(samples[0].channels))
	}
	want := []float32{0.1, 0.2, 0.3}
	for i, v := range want {
		if samples[0].channels[0][i] != v {
			t.Fatalf("[%d] = %f, want %f", i, samples[0].channels[0][i], v)
		}
	}
}

func TestWriter_SkipsMismatchedResolution(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 1920, Height: 1080})
	w.SetVideoWriter(mock, Rational{30, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Write a frame at wrong resolution — should be silently skipped.
	yuv := make([]byte, 640*128*3/2)
	w.WriteVideo(yuv, 640, 128, 0)

	time.Sleep(100 * time.Millisecond)
	if len(mock.getGrains()) != 0 {
		t.Fatal("expected no grains for mismatched resolution")
	}
}

func TestWriter_SteadyRateMultipleFrames(t *testing.T) {
	mock := &mockDiscreteWriter{}
	w := NewWriter(WriterConfig{Width: 12, Height: 2})
	// Use a fast rate (100fps = 10ms ticks) so the test runs quickly.
	w.SetVideoWriter(mock, Rational{100, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)

	// Write a frame.
	width, height := 12, 2
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	for i := 0; i < width*height; i++ {
		yuv[i] = 16
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128
	}
	w.WriteVideo(yuv, width, height, 0)

	// Wait for multiple ticks — the same frame should be written repeatedly.
	time.Sleep(55 * time.Millisecond) // ~5 ticks at 100fps
	grains := mock.getGrains()
	if len(grains) < 3 {
		t.Fatalf("expected at least 3 grains from steady ticker, got %d", len(grains))
	}
}
