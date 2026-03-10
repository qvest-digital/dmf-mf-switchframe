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

func (m *mockContinuousReader) HeadIndex() (uint64, error) { return 0, nil }

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
	if received[0].PTS != 1 {
		t.Fatalf("expected PTS=1 (monotonic counter), got %d", received[0].PTS)
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
	if received[0].PTS != 1 {
		t.Fatalf("expected PTS=1 (monotonic counter, invalid grain skipped), got %d", received[0].PTS)
	}
}

func TestReader_DetectsTimestampDiscontinuity(t *testing.T) {
	// Gap from MXL index 1 to 5 — grains still delivered.
	// PTS is now a monotonic counter, so values are 1, 2.
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

	// Both grains delivered; PTS is monotonic counter (1, 2).
	if len(received) != 2 {
		t.Fatalf("expected 2 grains, got %d", len(received))
	}
	if received[0].PTS != 1 || received[1].PTS != 2 {
		t.Fatalf("expected PTS 1,2 (monotonic); got %d,%d", received[0].PTS, received[1].PTS)
	}
}

func TestMXLReaderPTSMonotonic(t *testing.T) {
	// Video grains with high ring-buffer indices (wall-clock derived).
	// PTS should be monotonic from 1, NOT the MXL index values.
	grains := []mockGrain{
		{data: []byte{1, 2, 3, 4}, info: GrainInfo{Index: 50000, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{5, 6, 7, 8}, info: GrainInfo{Index: 50001, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{9, 10, 11, 12}, info: GrainInfo{Index: 50002, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{13, 14, 15, 16}, info: GrainInfo{Index: 50003, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{17, 18, 19, 20}, info: GrainInfo{Index: 50004, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})
	flow.headIdx = 50000

	reader := NewVideoReader(ReaderConfig{
		BufSize:   8,
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
		if len(received) == 5 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 5 {
		t.Fatalf("expected 5 grains, got %d", len(received))
	}

	// PTS should start at 1 and increment by 1 (monotonic counter domain).
	for i, g := range received {
		expectedPTS := int64(i + 1)
		if g.PTS != expectedPTS {
			t.Fatalf("grain[%d]: expected PTS=%d (monotonic), got %d (MXL index would be %d)",
				i, expectedPTS, g.PTS, 50000+i)
		}
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

func TestVideoReaderTooLateRecovery(t *testing.T) {
	// Simulate a video reader that returns "too late" after the first grain,
	// then succeeds after re-sync to HeadIndex.
	flow := &tooLateDiscreteReader{
		headIdx: 200,
		grains: []mockGrain{
			// First read succeeds at index 100.
			{data: []byte{1, 2, 3, 4}, info: GrainInfo{Index: 100, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
			// Second read will return "too late" (triggered by tooLateAt).
			// After re-sync, third read succeeds at headIdx-2 = 198.
			{data: []byte{5, 6, 7, 8}, info: GrainInfo{Index: 198, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
			{data: []byte{9, 10, 11, 12}, info: GrainInfo{Index: 199, GrainSize: 4, TotalSlices: 1, ValidSlices: 1}},
		},
		tooLateAt: 1, // Return "too late" on the 2nd ReadGrain call.
	}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   8,
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
		t.Fatalf("expected 3 grains (1 before too-late, 2 after re-sync), got %d", len(received))
	}

	// PTS should be monotonic from 1 (using counter, not MXL index).
	for i, g := range received {
		expectedPTS := int64(i + 1)
		if g.PTS != expectedPTS {
			t.Fatalf("grain[%d]: expected PTS=%d, got %d", i, expectedPTS, g.PTS)
		}
	}
}

// tooLateDiscreteReader returns a "too late" error on the Nth ReadGrain call,
// then succeeds with remaining grains after re-sync.
type tooLateDiscreteReader struct {
	mu        sync.Mutex
	grains    []mockGrain
	cursor    int
	callCount int
	tooLateAt int // 0-based call index to return "too late"
	headIdx   uint64
}

func (r *tooLateDiscreteReader) ReadGrain(index uint64, _ uint64) ([]byte, GrainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	call := r.callCount
	r.callCount++

	if call == r.tooLateAt {
		return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: too late (index %d already overwritten)", index)
	}

	if r.cursor >= len(r.grains) {
		return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: timeout")
	}

	g := r.grains[r.cursor]
	r.cursor++
	return g.data, g.info, nil
}

func (r *tooLateDiscreteReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}
}

func (r *tooLateDiscreteReader) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headIdx, nil
}

func (r *tooLateDiscreteReader) Close() error { return nil }

func TestAudioReaderTooLateRecovery(t *testing.T) {
	flow := &tooLateContinuousReader{
		headIdx: 96000,
		samples: []mockSamples{
			{pcm: [][]float32{{0.1, 0.2}, {0.3, 0.4}}},
			// 2nd ReadSamples call returns "too late" (triggered by tooLateAt).
			// After re-sync to headIdx, 3rd and 4th calls succeed.
			{pcm: [][]float32{{0.5, 0.6}, {0.7, 0.8}}},
			{pcm: [][]float32{{0.9, 1.0}, {1.1, 1.2}}},
		},
		tooLateAt: 1,
	}

	reader := NewAudioReader(ReaderConfig{
		BufSize:        8,
		TimeoutMs:      10,
		SamplesPerRead: 2,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartAudio(ctx, flow)

	var received []AudioGrain
	for g := range reader.Audio() {
		received = append(received, g)
		if len(received) == 3 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 3 {
		t.Fatalf("expected 3 audio grains (1 before too-late, 2 after re-sync), got %d", len(received))
	}

	if received[0].SampleRate != 48000 {
		t.Fatalf("expected sample rate 48000, got %d", received[0].SampleRate)
	}
	if received[0].Channels != 2 {
		t.Fatalf("expected 2 channels, got %d", received[0].Channels)
	}
}

// tooLateContinuousReader returns a "too late" error on the Nth ReadSamples call,
// then succeeds with remaining samples after re-sync.
type tooLateContinuousReader struct {
	mu        sync.Mutex
	samples   []mockSamples
	cursor    int
	callCount int
	tooLateAt int
	headIdx   uint64
}

func (r *tooLateContinuousReader) ReadSamples(index uint64, count int, timeoutNs uint64) ([][]float32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	call := r.callCount
	r.callCount++

	if call == r.tooLateAt {
		return nil, fmt.Errorf("mxl: read samples: too late (index %d already overwritten)", index)
	}

	if r.cursor >= len(r.samples) {
		return nil, fmt.Errorf("mxl: read samples: timeout")
	}

	s := r.samples[r.cursor]
	r.cursor++
	return s.pcm, nil
}

func (r *tooLateContinuousReader) ConfigInfo() FlowConfig {
	return FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	}
}

func (r *tooLateContinuousReader) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headIdx, nil
}

func (r *tooLateContinuousReader) Close() error { return nil }

func TestReader_AudioChannelNilForVideo(t *testing.T) {
	reader := NewVideoReader(ReaderConfig{BufSize: 4})
	if reader.Audio() != nil {
		t.Fatal("expected nil Audio() channel for video reader")
	}
	if reader.Video() == nil {
		t.Fatal("expected non-nil Video() channel for video reader")
	}
}

// underflowDiscreteReader simulates a "too late" error followed by HeadIndex
// returning a small value (0 or 1), which previously caused uint64 underflow
// in the `index = headIdx - 2` re-sync logic.
type underflowDiscreteReader struct {
	mu        sync.Mutex
	headIdx   uint64
	callCount int
	indices   []uint64 // record all indices passed to ReadGrain
}

func (r *underflowDiscreteReader) ReadGrain(index uint64, _ uint64) ([]byte, GrainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indices = append(r.indices, index)
	r.callCount++

	// First call: return "too late" to trigger re-sync.
	if r.callCount == 1 {
		return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: too late (index %d)", index)
	}

	// After re-sync, return timeout to let context cancel cleanly.
	return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: timeout")
}

func (r *underflowDiscreteReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}
}

func (r *underflowDiscreteReader) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headIdx, nil
}

func (r *underflowDiscreteReader) Close() error { return nil }

func (r *underflowDiscreteReader) getIndices() []uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]uint64, len(r.indices))
	copy(result, r.indices)
	return result
}

func TestVideoLoop_HeadIndexZero_NoUnderflow(t *testing.T) {
	// When headIdx is 0, the old code does `index = headIdx - 2` which wraps
	// uint64 to 18446744073709551614. The fix should clamp to headIdx (0).
	flow := &underflowDiscreteReader{headIdx: 0}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
		Width:     320,
		Height:    240,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reader.StartVideo(ctx, flow)
	reader.Wait()

	// Check that no index was a huge underflowed value.
	indices := flow.getIndices()
	for _, idx := range indices {
		if idx > 1000 {
			t.Fatalf("index underflowed to %d; expected small value after re-sync with headIdx=0", idx)
		}
	}
}

func TestVideoLoop_HeadIndexOne_NoUnderflow(t *testing.T) {
	// headIdx=1: old code does `index = 1 - 2` which wraps.
	// Fix should clamp to headIdx (1).
	flow := &underflowDiscreteReader{headIdx: 1}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
		Width:     320,
		Height:    240,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reader.StartVideo(ctx, flow)
	reader.Wait()

	indices := flow.getIndices()
	for _, idx := range indices {
		if idx > 1000 {
			t.Fatalf("index underflowed to %d; expected small value after re-sync with headIdx=1", idx)
		}
	}
}

func TestDataLoop_HeadIndexZero_NoUnderflow(t *testing.T) {
	// Same underflow bug exists in dataLoop.
	flow := &underflowDiscreteReader{headIdx: 0}

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reader.StartData(ctx, flow)
	reader.Wait()

	indices := flow.getIndices()
	for _, idx := range indices {
		if idx > 1000 {
			t.Fatalf("index underflowed to %d; expected small value after re-sync with headIdx=0", idx)
		}
	}
}

func TestDataLoop_HeadIndexOne_NoUnderflow(t *testing.T) {
	flow := &underflowDiscreteReader{headIdx: 1}

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reader.StartData(ctx, flow)
	reader.Wait()

	indices := flow.getIndices()
	for _, idx := range indices {
		if idx > 1000 {
			t.Fatalf("index underflowed to %d; expected small value after re-sync with headIdx=1", idx)
		}
	}
}

// timeoutCountingReader always returns timeout errors.
// Used to verify that timeout/too-early errors are NOT counted as fatal.
type timeoutCountingReader struct {
	mu        sync.Mutex
	headIdx   uint64
	readCount int
}

func (r *timeoutCountingReader) ReadGrain(_ uint64, _ uint64) ([]byte, GrainInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readCount++
	return nil, GrainInfo{}, fmt.Errorf("mxl: read grain: timeout")
}

func (r *timeoutCountingReader) ConfigInfo() FlowConfig {
	return FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}
}

func (r *timeoutCountingReader) HeadIndex() (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headIdx, nil
}

func (r *timeoutCountingReader) Close() error { return nil }

func TestVideoLoop_TimeoutNotCountedAsFatalError(t *testing.T) {
	// In the buggy code, timeout errors increment consecutiveErrors,
	// causing premature termination after maxConsecutiveErrors (50).
	// After the fix, timeout and too-early are transient and should
	// NOT count as consecutive errors.
	flow := &timeoutCountingReader{headIdx: 10}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
		Width:     320,
		Height:    240,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	reader.StartVideo(ctx, flow)
	reader.Wait()

	flow.mu.Lock()
	count := flow.readCount
	flow.mu.Unlock()

	// The reader should have made many reads without dying from consecutive errors.
	// With maxConsecutiveErrors=50 and timeout counting as errors, it would die
	// after exactly 50 reads. Without counting, it should continue until context
	// cancels, making far more than 50 reads.
	if count <= 50 {
		t.Fatalf("timeout errors were counted as fatal: reader stopped after %d reads (max consecutive = 50)", count)
	}
}

func TestVideoLoop_TooLateResync_LargeHeadIdx(t *testing.T) {
	// When headIdx >= 2, headIdx - 2 is fine (no underflow). Verify it
	// correctly re-syncs to headIdx-2 = 98.
	flow := &underflowDiscreteReader{headIdx: 100}

	reader := NewVideoReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
		Width:     320,
		Height:    240,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	reader.StartVideo(ctx, flow)
	reader.Wait()

	indices := flow.getIndices()
	found98 := false
	for _, idx := range indices {
		if idx == 98 {
			found98 = true
			break
		}
	}
	if !found98 {
		t.Fatalf("expected index 98 (headIdx-2) after resync with headIdx=100; indices: %v", indices)
	}
}
