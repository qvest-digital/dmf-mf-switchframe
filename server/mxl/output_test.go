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

	// Check that a grain was written.
	grains := vMock.getGrains()
	if len(grains) != 1 {
		t.Fatalf("expected 1 grain written, got %d", len(grains))
	}
	if len(grains[0].data) == 0 {
		t.Fatal("expected non-empty V210 data")
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

	if !vMock.closed {
		t.Fatal("expected video writer closed")
	}
	if !aMock.closed {
		t.Fatal("expected audio writer closed")
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
