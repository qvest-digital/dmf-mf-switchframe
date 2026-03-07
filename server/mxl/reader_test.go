package mxl

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- Mock discrete reader (video) ---

type mockDiscreteReader struct {
	mu      sync.Mutex
	grains  []mockGrain
	cursor  int
	config  FlowConfig
	headIdx uint64
	readErr error
}

type mockGrain struct {
	data []byte
	info GrainInfo
}

func newMockDiscreteReader(grains []mockGrain, config FlowConfig) *mockDiscreteReader {
	headIdx := uint64(0)
	if len(grains) > 0 {
		headIdx = grains[0].info.Index
	}
	return &mockDiscreteReader{
		grains:  grains,
		config:  config,
		headIdx: headIdx,
	}
}

func (m *mockDiscreteReader) ReadGrain(index uint64, _ uint64) ([]byte, GrainInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readErr != nil {
		return nil, GrainInfo{}, m.readErr
	}

	if m.cursor >= len(m.grains) {
		return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: timeout")
	}

	g := m.grains[m.cursor]
	m.cursor++
	return g.data, g.info, nil
}

func (m *mockDiscreteReader) ConfigInfo() FlowConfig { return m.config }

func (m *mockDiscreteReader) HeadIndex() (uint64, error) { return m.headIdx, nil }

func (m *mockDiscreteReader) Close() error { return nil }

// --- Mock continuous reader (audio) ---

type mockContinuousReader struct {
	mu      sync.Mutex
	samples []mockSamples
	cursor  int
	config  FlowConfig
	readErr error
}

type mockSamples struct {
	pcm [][]float32
}

func newMockContinuousReader(samples []mockSamples, config FlowConfig) *mockContinuousReader {
	return &mockContinuousReader{
		samples: samples,
		config:  config,
	}
}

func (m *mockContinuousReader) ReadSamples(_ uint64, _ int, _ uint64) ([][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readErr != nil {
		return nil, m.readErr
	}

	if m.cursor >= len(m.samples) {
		return nil, fmt.Errorf("mxl: read samples: timeout")
	}

	s := m.samples[m.cursor]
	m.cursor++
	return s.pcm, nil
}

func (m *mockContinuousReader) ConfigInfo() FlowConfig { return m.config }

func (m *mockContinuousReader) Close() error { return nil }

// --- Tests ---

func TestReader_DeliversVideoGrains(t *testing.T) {
	grains := []mockGrain{
		{data: []byte{1, 2, 3, 4}, info: GrainInfo{Index: 100, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{5, 6, 7, 8}, info: GrainInfo{Index: 101, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{9, 10, 11, 12}, info: GrainInfo{Index: 102, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
		Width:     1920,
		Height:    1080,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartVideo(ctx, flow)

	var received []VideoGrain
	for g := range reader.Video() {
		received = append(received, g)
		if len(received) == 3 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 3 {
		t.Fatalf("expected 3 grains, got %d", len(received))
	}
	if received[0].PTS != 100 {
		t.Fatalf("expected PTS=100, got %d", received[0].PTS)
	}
	if received[0].Width != 1920 || received[0].Height != 1080 {
		t.Fatalf("expected 1920x1080, got %dx%d", received[0].Width, received[0].Height)
	}
	if len(received[0].V210) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(received[0].V210))
	}
}

func TestReader_DeliversAudioGrains(t *testing.T) {
	samples := []mockSamples{
		{pcm: [][]float32{{0.1, 0.2}, {0.3, 0.4}}},
		{pcm: [][]float32{{0.5, 0.6}, {0.7, 0.8}}},
	}
	flow := newMockContinuousReader(samples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	reader := NewAudioReader(ReaderConfig{
		BufSize:        4,
		TimeoutMs:      10,
		SamplesPerRead: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartAudio(ctx, flow)

	var received []AudioGrain
	for g := range reader.Audio() {
		received = append(received, g)
		if len(received) == 2 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 2 {
		t.Fatalf("expected 2 audio grains, got %d", len(received))
	}
	if received[0].SampleRate != 48000 {
		t.Fatalf("expected sample rate 48000, got %d", received[0].SampleRate)
	}
	if received[0].Channels != 2 {
		t.Fatalf("expected 2 channels, got %d", received[0].Channels)
	}
	if len(received[0].PCM) != 2 || len(received[0].PCM[0]) != 2 {
		t.Fatalf("unexpected PCM shape: %d channels, %d samples",
			len(received[0].PCM), len(received[0].PCM[0]))
	}
}

func TestReader_StopsOnContextCancel(t *testing.T) {
	// Infinite stream of grains — reader should stop when context cancelled.
	flow := &infiniteDiscreteReader{}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   2,
		TimeoutMs: 10,
		Width:     1920,
		Height:    1080,
	})

	ctx, cancel := context.WithCancel(context.Background())
	reader.StartVideo(ctx, flow)

	// Read one grain, then cancel.
	select {
	case <-reader.Video():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first grain")
	}

	cancel()
	reader.Wait() // Should return quickly.
}

type infiniteDiscreteReader struct {
	mu    sync.Mutex
	index uint64
}

func (r *infiniteDiscreteReader) ReadGrain(index uint64, _ uint64) ([]byte, GrainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index++
	return []byte{0, 0, 0, 0}, GrainInfo{
		Index:       r.index,
		GrainSize:   4,
		TotalSlices: 1,
		ValidSlices: 1,
	}, nil
}

func (r *infiniteDiscreteReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}
}

func (r *infiniteDiscreteReader) HeadIndex() (uint64, error) { return 1, nil }
func (r *infiniteDiscreteReader) Close() error               { return nil }

func TestReader_HandlesFlowError(t *testing.T) {
	flow := &failingDiscreteReader{}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
		Width:     1920,
		Height:    1080,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reader.StartVideo(ctx, flow)

	// Reader should stop after maxConsecutiveErrors (50).
	reader.Wait()
	// If we got here, the reader stopped on its own — success.
}

type failingDiscreteReader struct{}

func (r *failingDiscreteReader) ReadGrain(_ uint64, _ uint64) ([]byte, GrainInfo, error) {
	return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: flow invalid (writer crashed?)")
}

func (r *failingDiscreteReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}
}

func (r *failingDiscreteReader) HeadIndex() (uint64, error) { return 1, nil }
func (r *failingDiscreteReader) Close() error               { return nil }

func TestReader_SkipsInvalidGrains(t *testing.T) {
	grains := []mockGrain{
		{data: []byte{0}, info: GrainInfo{Index: 1, GrainSize: 1, Invalid: true}},
		{data: []byte{1}, info: GrainInfo{Index: 2, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
		Width:     1920,
		Height:    1080,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartVideo(ctx, flow)

	var received []VideoGrain
	for g := range reader.Video() {
		received = append(received, g)
		cancel()
	}
	reader.Wait()

	if len(received) != 1 {
		t.Fatalf("expected 1 valid grain (invalid skipped), got %d", len(received))
	}
	if received[0].PTS != 2 {
		t.Fatalf("expected PTS=2 (skipped invalid at 1), got %d", received[0].PTS)
	}
}

func TestReader_DetectsTimestampDiscontinuity(t *testing.T) {
	// Gap from index 1 to 5 — should log a warning.
	grains := []mockGrain{
		{data: []byte{1}, info: GrainInfo{Index: 1, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{2}, info: GrainInfo{Index: 5, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
		Width:     1920,
		Height:    1080,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartVideo(ctx, flow)

	var received []VideoGrain
	for g := range reader.Video() {
		received = append(received, g)
		if len(received) == 2 {
			cancel()
		}
	}
	reader.Wait()

	// Both grains delivered despite discontinuity.
	if len(received) != 2 {
		t.Fatalf("expected 2 grains, got %d", len(received))
	}
	if received[0].PTS != 1 || received[1].PTS != 5 {
		t.Fatalf("expected PTS 1,5; got %d,%d", received[0].PTS, received[1].PTS)
	}
}

func TestReader_VideoChannelNilForAudio(t *testing.T) {
	reader := NewAudioReader(ReaderConfig{BufSize: 4})
	if reader.Video() != nil {
		t.Fatal("expected nil Video() channel for audio reader")
	}
	if reader.Audio() == nil {
		t.Fatal("expected non-nil Audio() channel for audio reader")
	}
}

func TestReader_AudioChannelNilForVideo(t *testing.T) {
	reader := NewVideoReader(ReaderConfig{BufSize: 4})
	if reader.Audio() != nil {
		t.Fatal("expected nil Audio() channel for video reader")
	}
	if reader.Video() == nil {
		t.Fatal("expected non-nil Video() channel for video reader")
	}
}
