package mxl

import (
	"context"
	"testing"
	"time"
)

// --- Mock sink setters ---

type mockSwitcherSink struct {
	sink func(yuv []byte, width, height int, pts int64)
}

func (m *mockSwitcherSink) SetRawVideoSink(sink func(yuv []byte, width, height int, pts int64)) {
	m.sink = sink
}

type mockMixerSink struct {
	sink func(pcm []float32, pts int64, sampleRate, channels int)
}

func (m *mockMixerSink) SetRawAudioSink(sink func(pcm []float32, pts int64, sampleRate, channels int)) {
	m.sink = sink
}

// --- Tests ---

func TestOutput_ReceivesVideoFromSink(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	sw := &mockSwitcherSink{}

	out := NewOutput(OutputConfig{
		FlowName: "program",
		Width:    12,
		Height:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.Start(ctx, vMock, nil, sw, nil)

	if sw.sink == nil {
		t.Fatal("expected switcher sink to be set")
	}

	// Simulate a video frame through the sink.
	yuvSize := 12*2 + 6 + 6
	yuv := make([]byte, yuvSize)
	for i := 0; i < 12*2; i++ {
		yuv[i] = 16
	}
	for i := 12 * 2; i < yuvSize; i++ {
		yuv[i] = 128
	}

	sw.sink(yuv, 12, 2, 1000)

	// Wait for the steady-rate ticker to write the grain.
	deadline := time.After(500 * time.Millisecond)
	for {
		grains := vMock.getGrains()
		if len(grains) >= 1 {
			if len(grains[0].data) == 0 {
				t.Fatal("expected non-empty V210 data")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for grain write, got %d grains", len(vMock.getGrains()))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestOutput_ReceivesAudioFromSink(t *testing.T) {
	aMock := &mockContinuousWriter{}
	mixer := &mockMixerSink{}

	out := NewOutput(OutputConfig{
		FlowName:   "program",
		SampleRate: 48000,
		Channels:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.Start(ctx, nil, aMock, nil, mixer)

	if mixer.sink == nil {
		t.Fatal("expected mixer sink to be set")
	}

	// Simulate mixed audio through the sink.
	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	mixer.sink(pcm, 1000, 48000, 2)

	samples := aMock.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample batch, got %d", len(samples))
	}
	// De-interleaved: 2 channels, 2 samples each.
	if len(samples[0].channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(samples[0].channels))
	}
}

func TestOutput_StopsCleanly(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	aMock := &mockContinuousWriter{}
	sw := &mockSwitcherSink{}
	mixer := &mockMixerSink{}

	out := NewOutput(OutputConfig{
		FlowName:   "program",
		Width:      12,
		Height:     2,
		SampleRate: 48000,
		Channels:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	out.Start(ctx, vMock, aMock, sw, mixer)

	// Write something.
	yuv := make([]byte, 12*2*3/2)
	sw.sink(yuv, 12, 2, 0)

	cancel()
	time.Sleep(50 * time.Millisecond)

	out.Stop()

	vMock.mu.Lock()
	vClosed := vMock.closed
	vMock.mu.Unlock()
	if !vClosed {
		t.Fatal("expected video writer closed")
	}

	aMock.mu.Lock()
	aClosed := aMock.closed
	aMock.mu.Unlock()
	if !aClosed {
		t.Fatal("expected audio writer closed")
	}
}

func TestMXLOutputConfigurableFrameRate(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	sw := &mockSwitcherSink{}

	out := NewOutput(OutputConfig{
		FlowName:  "program",
		Width:     12,
		Height:    2,
		VideoRate: Rational{25, 1}, // PAL 25fps
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.Start(ctx, vMock, nil, sw, nil)

	// Verify the writer received the configured rate, not the default 29.97fps.
	writer := out.Writer()
	ref := writer.videoRef.Load()
	if ref == nil {
		t.Fatal("expected videoRef to be set")
	}

	if ref.rate.Numerator != 25 || ref.rate.Denominator != 1 {
		t.Fatalf("expected video rate 25/1 (PAL), got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}
}

func TestMXLOutputDefaultFrameRate(t *testing.T) {
	vMock := &mockDiscreteWriter{}
	sw := &mockSwitcherSink{}

	// Zero-value VideoRate should default to 29.97fps (30000/1001).
	out := NewOutput(OutputConfig{
		FlowName: "program",
		Width:    12,
		Height:   2,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.Start(ctx, vMock, nil, sw, nil)

	writer := out.Writer()
	ref := writer.videoRef.Load()
	if ref == nil {
		t.Fatal("expected videoRef to be set")
	}

	if ref.rate.Numerator != 30000 || ref.rate.Denominator != 1001 {
		t.Fatalf("expected default video rate 30000/1001, got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}
}

func TestMXLOutputConfigurableFrameRateLifecycle(t *testing.T) {
	vMock := &mockDiscreteWriter{}

	out := NewOutput(OutputConfig{
		FlowName:  "program",
		Width:     12,
		Height:    2,
		VideoRate: Rational{50, 1}, // 50fps
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out.StartLifecycle(ctx, vMock, nil)

	writer := out.Writer()
	ref := writer.videoRef.Load()
	if ref == nil {
		t.Fatal("expected videoRef to be set")
	}

	if ref.rate.Numerator != 50 || ref.rate.Denominator != 1 {
		t.Fatalf("expected video rate 50/1, got %d/%d", ref.rate.Numerator, ref.rate.Denominator)
	}
}

func TestOutput_NilWritersNoOp(t *testing.T) {
	sw := &mockSwitcherSink{}
	mixer := &mockMixerSink{}

	out := NewOutput(OutputConfig{FlowName: "program"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start with nil writers — should not crash.
	out.Start(ctx, nil, nil, sw, mixer)
	out.Stop()
}
