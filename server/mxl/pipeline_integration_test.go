package mxl

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

// TestPipelineVideoRoundTrip proves the full MXL video pipeline:
//
//	V210 input → Source (V210→YUV420p) → OnRawVideo callback
//	→ Writer (YUV420p→V210) → mock DiscreteWriter
//
// Verifies that a V210 frame survives the round-trip through the pipeline
// and produces the same YUV420p when decoded at both ends.
func TestPipelineVideoRoundTrip(t *testing.T) {
	const width, height = 12, 2

	// 1. Generate a V210 test frame from known YUV420p.
	inputYUV := makeTestYUV420p(width, height, 16, 128, 128) // limited-range black
	v210Frame, err := YUV420pToV210(inputYUV, width, height)
	if err != nil {
		t.Fatalf("YUV420pToV210: %v", err)
	}

	// 2. Set up Source with OnRawVideo capturing YUV420p.
	var captured struct {
		mu     sync.Mutex
		frames []capturedFrame
	}

	videoGrains := []mockGrain{{
		data: v210Frame,
		info: GrainInfo{Index: 1, GrainSize: uint32(len(v210Frame)), TotalSlices: 1, ValidSlices: 1},
	}}
	flow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "test",
		Width:    width,
		Height:   height,
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			captured.mu.Lock()
			defer captured.mu.Unlock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			captured.frames = append(captured.frames, capturedFrame{yuv: cp, w: w, h: h, pts: pts})
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	src.Start(ctx, flow, nil)

	// Wait for frame delivery.
	waitFor(t, 2*time.Second, func() bool {
		captured.mu.Lock()
		defer captured.mu.Unlock()
		return len(captured.frames) >= 1
	})

	cancel()
	src.Stop()

	captured.mu.Lock()
	cf := captured.frames[0]
	captured.mu.Unlock()

	// 3. Verify the captured YUV420p matches the original input.
	if cf.w != width || cf.h != height {
		t.Fatalf("dimensions: got %dx%d, want %dx%d", cf.w, cf.h, width, height)
	}
	if !bytes.Equal(cf.yuv, inputYUV) {
		t.Fatal("Source output YUV420p does not match input — V210→YUV420p conversion mismatch")
	}

	// 4. Feed the captured YUV420p through Writer (output path).
	vWriter := &mockDiscreteWriter{}
	writer := NewWriter(WriterConfig{Width: width, Height: height})
	writer.SetVideoWriter(vWriter, Rational{30, 1})

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	writer.Start(ctx2)
	writer.WriteVideo(cf.yuv, cf.w, cf.h, cf.pts)

	// Wait for the steady-rate ticker to write the grain.
	waitFor(t, 500*time.Millisecond, func() bool {
		return len(vWriter.getGrains()) >= 1
	})
	cancel2()

	grains := vWriter.getGrains()
	if len(grains) < 1 {
		t.Fatalf("Writer: expected at least 1 V210 grain, got %d", len(grains))
	}

	// 5. Decode the output V210 and compare to original YUV420p.
	outputYUV, err := V210ToYUV420p(grains[0].data, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p on output: %v", err)
	}
	if !bytes.Equal(outputYUV, inputYUV) {
		t.Fatal("Round-trip V210→YUV420p→V210→YUV420p produced different output")
	}

	t.Logf("Pipeline round-trip OK: %d bytes V210 → %d bytes YUV420p → %d bytes V210",
		len(v210Frame), len(inputYUV), len(grains[0].data))
}

// TestPipelineAudioRoundTrip proves the full MXL audio pipeline:
//
//	De-interleaved float32 → Source (interleave) → OnRawAudio callback
//	→ Writer (de-interleave) → mock ContinuousWriter
func TestPipelineAudioRoundTrip(t *testing.T) {
	// Input: de-interleaved stereo (what MXL provides).
	inputL := []float32{0.1, 0.2, 0.3, 0.4}
	inputR := []float32{0.5, 0.6, 0.7, 0.8}

	var captured struct {
		mu  sync.Mutex
		pcm [][]float32
	}

	audioSamples := []mockSamples{{
		pcm: [][]float32{inputL, inputR},
	}}
	flow := newMockContinuousReader(audioSamples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	src := NewSource(SourceConfig{
		FlowName:   "test",
		SampleRate: 48000,
		Channels:   2,
		OnRawAudio: func(key string, pcm []float32, pts int64) {
			captured.mu.Lock()
			defer captured.mu.Unlock()
			cp := make([]float32, len(pcm))
			copy(cp, pcm)
			captured.pcm = append(captured.pcm, cp)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	src.Start(ctx, nil, flow)

	waitFor(t, 2*time.Second, func() bool {
		captured.mu.Lock()
		defer captured.mu.Unlock()
		return len(captured.pcm) >= 1
	})

	cancel()
	src.Stop()

	captured.mu.Lock()
	interleaved := captured.pcm[0]
	captured.mu.Unlock()

	// Verify interleaving: [L0,R0,L1,R1,L2,R2,L3,R3]
	wantInterleaved := []float32{0.1, 0.5, 0.2, 0.6, 0.3, 0.7, 0.4, 0.8}
	if len(interleaved) != len(wantInterleaved) {
		t.Fatalf("interleaved length: got %d, want %d", len(interleaved), len(wantInterleaved))
	}
	for i, v := range wantInterleaved {
		if interleaved[i] != v {
			t.Fatalf("interleaved[%d] = %f, want %f", i, interleaved[i], v)
		}
	}

	// Feed through Writer (output path) — de-interleaves back.
	aWriter := &mockContinuousWriter{}
	writer := NewWriter(WriterConfig{SampleRate: 48000, Channels: 2})
	writer.SetAudioWriter(aWriter, Rational{48000, 1})

	ctx2, cancel2 := context.WithCancel(context.Background())
	writer.Start(ctx2)
	writer.WriteAudio(interleaved, 0, 48000, 2)
	cancel2()
	time.Sleep(20 * time.Millisecond)

	samples := aWriter.getSamples()
	if len(samples) != 1 {
		t.Fatalf("Writer: expected 1 sample batch, got %d", len(samples))
	}
	if len(samples[0].channels) != 2 {
		t.Fatalf("Writer: expected 2 channels, got %d", len(samples[0].channels))
	}

	// Verify de-interleaved output matches original input.
	for i, v := range inputL {
		if samples[0].channels[0][i] != v {
			t.Fatalf("output L[%d] = %f, want %f", i, samples[0].channels[0][i], v)
		}
	}
	for i, v := range inputR {
		if samples[0].channels[1][i] != v {
			t.Fatalf("output R[%d] = %f, want %f", i, samples[0].channels[1][i], v)
		}
	}

	t.Log("Audio round-trip OK: de-interleaved → interleaved → de-interleaved matches")
}

// TestPipelineOutputCapturesSinkFrames proves the Output orchestrator wires
// sinks correctly: switcher sink → Writer → V210, mixer sink → Writer → PCM.
func TestPipelineOutputCapturesSinkFrames(t *testing.T) {
	vWriter := &mockDiscreteWriter{}
	aWriter := &mockContinuousWriter{}
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
	defer cancel()
	out.Start(ctx, vWriter, aWriter, sw, mixer)

	if sw.sink == nil || mixer.sink == nil {
		t.Fatal("expected sinks to be registered")
	}

	// Simulate switcher producing a raw YUV420p frame.
	yuv := makeTestYUV420p(12, 2, 16, 128, 128)
	sw.sink(yuv, 12, 2, 1000)

	// Simulate mixer producing mixed PCM.
	pcm := []float32{0.1, 0.2, 0.3, 0.4}
	mixer.sink(pcm, 2000, 48000, 2)

	// Wait for the steady-rate ticker to write the video grain.
	waitFor(t, 500*time.Millisecond, func() bool {
		return len(vWriter.getGrains()) >= 1
	})

	// Verify video grain was written (YUV420p → V210).
	grains := vWriter.getGrains()
	if len(grains) < 1 {
		t.Fatalf("expected at least 1 video grain, got %d", len(grains))
	}
	if len(grains[0].data) == 0 {
		t.Fatal("expected non-empty V210 data")
	}

	// Verify audio samples were written (interleaved → de-interleaved).
	samples := aWriter.getSamples()
	if len(samples) != 1 {
		t.Fatalf("expected 1 audio batch, got %d", len(samples))
	}
	if len(samples[0].channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(samples[0].channels))
	}

	// Verify the V210 output decodes back to the original YUV.
	decoded, err := V210ToYUV420p(grains[0].data, 12, 2)
	if err != nil {
		t.Fatalf("V210ToYUV420p: %v", err)
	}
	if !bytes.Equal(decoded, yuv) {
		t.Fatal("output V210 does not decode to original YUV420p")
	}

	out.Stop()
	t.Log("Output sink pipeline OK: switcher→V210, mixer→de-interleaved PCM")
}

// TestPipelineFullLoopback connects Source input to Output, simulating
// a complete MXL-in → Switchframe → MXL-out loopback.
func TestPipelineFullLoopback(t *testing.T) {
	const width, height = 12, 2

	// Input: known YUV420p pattern with non-trivial pixel values.
	inputYUV := makeTestYUV420p(width, height, 100, 80, 200) // mid-gray, shifted chroma
	v210Input, err := YUV420pToV210(inputYUV, width, height)
	if err != nil {
		t.Fatalf("YUV420pToV210: %v", err)
	}

	// Output capture: Writer writes to mock.
	outputVWriter := &mockDiscreteWriter{}
	outputAWriter := &mockContinuousWriter{}

	// Wire output Writer.
	outWriter := NewWriter(WriterConfig{Width: width, Height: height, SampleRate: 48000, Channels: 2})
	outWriter.SetVideoWriter(outputVWriter, Rational{30, 1})
	outWriter.SetAudioWriter(outputAWriter, Rational{48000, 1})

	ctx, cancel := context.WithCancel(context.Background())
	outWriter.Start(ctx)

	// Source: reads V210, converts to YUV420p, calls OnRawVideo which
	// feeds directly into the output Writer (simulating switcher passthrough).
	videoGrains := []mockGrain{{
		data: v210Input,
		info: GrainInfo{Index: 1, GrainSize: uint32(len(v210Input)), TotalSlices: 1, ValidSlices: 1},
	}}
	flow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	// Audio: 2ch de-interleaved → Source interleaves → Writer de-interleaves.
	audioL := []float32{0.25, 0.50, 0.75, 1.0}
	audioR := []float32{-0.25, -0.50, -0.75, -1.0}
	audioSamples := []mockSamples{{pcm: [][]float32{audioL, audioR}}}
	audioFlow := newMockContinuousReader(audioSamples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	var videoDelivered, audioDelivered sync.WaitGroup
	videoDelivered.Add(1)
	audioDelivered.Add(1)
	videoOnce := sync.Once{}
	audioOnce := sync.Once{}

	src := NewSource(SourceConfig{
		FlowName:   "loopback",
		Width:      width,
		Height:     height,
		SampleRate: 48000,
		Channels:   2,
		OnRawVideo: func(_ string, yuv []byte, w, h int, pts int64) {
			// Passthrough: source output → output writer input.
			outWriter.WriteVideo(yuv, w, h, pts)
			videoOnce.Do(videoDelivered.Done)
		},
		OnRawAudio: func(_ string, pcm []float32, pts int64) {
			outWriter.WriteAudio(pcm, pts, 48000, 2)
			audioOnce.Do(audioDelivered.Done)
		},
	})

	srcCtx, srcCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer srcCancel()
	src.Start(srcCtx, flow, audioFlow)

	// Wait for both paths to deliver.
	done := make(chan struct{})
	go func() {
		videoDelivered.Wait()
		audioDelivered.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for loopback delivery")
	}

	// Wait for the steady-rate ticker to write at least one video grain.
	waitFor(t, 500*time.Millisecond, func() bool {
		return len(outputVWriter.getGrains()) >= 1
	})

	srcCancel()
	src.Stop()
	cancel()
	time.Sleep(20 * time.Millisecond)

	// Verify video round-trip: input YUV420p → V210 → Source → YUV420p → Writer → V210 → YUV420p.
	grains := outputVWriter.getGrains()
	if len(grains) == 0 {
		t.Fatal("no V210 grains written to output")
	}
	outputYUV, err := V210ToYUV420p(grains[0].data, width, height)
	if err != nil {
		t.Fatalf("V210ToYUV420p on output: %v", err)
	}
	if !bytes.Equal(outputYUV, inputYUV) {
		t.Fatal("full loopback: output YUV420p differs from input")
	}

	// Verify audio round-trip: de-interleaved → interleaved → de-interleaved.
	samples := outputAWriter.getSamples()
	if len(samples) == 0 {
		t.Fatal("no audio samples written to output")
	}
	outCh := samples[0].channels
	if len(outCh) != 2 {
		t.Fatalf("expected 2 output channels, got %d", len(outCh))
	}
	for i, v := range audioL {
		if outCh[0][i] != v {
			t.Fatalf("audio L[%d] = %f, want %f", i, outCh[0][i], v)
		}
	}
	for i, v := range audioR {
		if outCh[1][i] != v {
			t.Fatalf("audio R[%d] = %f, want %f", i, outCh[1][i], v)
		}
	}

	t.Logf("Full loopback OK: V210(%d bytes) → YUV420p(%d bytes) → V210(%d bytes), audio 2ch×4 samples",
		len(v210Input), len(inputYUV), len(grains[0].data))
}

// TestV210RoundTripIdempotent verifies that V210→YUV420p→V210→YUV420p
// produces identical YUV420p on both conversions (conversion is stable).
func TestV210RoundTripIdempotent(t *testing.T) {
	tests := []struct {
		name      string
		y, cb, cr byte
	}{
		{"black", 16, 128, 128},
		{"white", 235, 128, 128},
		{"mid-gray", 128, 128, 128},
		{"red-ish", 81, 90, 240},
		{"green-ish", 145, 54, 34},
		{"blue-ish", 41, 240, 110},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const width, height = 12, 2
			yuv1 := makeTestYUV420p(width, height, tt.y, tt.cb, tt.cr)

			v210a, err := YUV420pToV210(yuv1, width, height)
			if err != nil {
				t.Fatal(err)
			}

			yuv2, err := V210ToYUV420p(v210a, width, height)
			if err != nil {
				t.Fatal(err)
			}

			v210b, err := YUV420pToV210(yuv2, width, height)
			if err != nil {
				t.Fatal(err)
			}

			yuv3, err := V210ToYUV420p(v210b, width, height)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(yuv2, yuv3) {
				t.Fatal("V210 round-trip is not idempotent: second conversion differs")
			}

			// First YUV might differ slightly due to 8→10→8 bit quantization,
			// but subsequent round-trips must be stable.
			if !bytes.Equal(yuv1, yuv2) {
				t.Logf("note: initial YUV differs from round-tripped (expected for 8→10→8 bit)")
			}
		})
	}
}

// --- Test helpers ---

type capturedFrame struct {
	yuv  []byte
	w, h int
	pts  int64
}

// makeTestYUV420p creates a YUV420p frame filled with constant values.
func makeTestYUV420p(width, height int, y, cb, cr byte) []byte {
	ySize := width * height
	cSize := (width / 2) * (height / 2)
	buf := make([]byte, ySize+2*cSize)

	for i := 0; i < ySize; i++ {
		buf[i] = y
	}
	for i := ySize; i < ySize+cSize; i++ {
		buf[i] = cb
	}
	for i := ySize + cSize; i < ySize+2*cSize; i++ {
		buf[i] = cr
	}
	return buf
}

// waitFor polls cond until it returns true or timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for condition")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}
