package mxl

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReader_DeliversDataGrains(t *testing.T) {
	grains := []mockGrain{
		{data: []byte{0xAA, 0xBB}, info: GrainInfo{Index: 10, GrainSize: 2, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{0xCC, 0xDD, 0xEE}, info: GrainInfo{Index: 11, GrainSize: 3, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{0xFF}, info: GrainInfo{Index: 12, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)

	var received []DataGrain
	for g := range reader.Data() {
		received = append(received, g)
		if len(received) == 3 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 3 {
		t.Fatalf("expected 3 data grains, got %d", len(received))
	}

	// PTS should be monotonic counter starting at 1.
	if received[0].PTS != 1 {
		t.Fatalf("expected PTS=1, got %d", received[0].PTS)
	}
	if received[1].PTS != 2 {
		t.Fatalf("expected PTS=2, got %d", received[1].PTS)
	}
	if received[2].PTS != 3 {
		t.Fatalf("expected PTS=3, got %d", received[2].PTS)
	}

	// Verify payload data is correct.
	if len(received[0].Data) != 2 || received[0].Data[0] != 0xAA || received[0].Data[1] != 0xBB {
		t.Fatalf("unexpected data in grain 0: %v", received[0].Data)
	}
	if len(received[1].Data) != 3 || received[1].Data[0] != 0xCC {
		t.Fatalf("unexpected data in grain 1: %v", received[1].Data)
	}
	if len(received[2].Data) != 1 || received[2].Data[0] != 0xFF {
		t.Fatalf("unexpected data in grain 2: %v", received[2].Data)
	}
}

func TestReader_DataSkipsInvalidGrains(t *testing.T) {
	grains := []mockGrain{
		{data: []byte{0x01}, info: GrainInfo{Index: 1, GrainSize: 1, Invalid: true}},
		{data: []byte{0x02}, info: GrainInfo{Index: 2, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)

	var received []DataGrain
	for g := range reader.Data() {
		received = append(received, g)
		cancel()
	}
	reader.Wait()

	if len(received) != 1 {
		t.Fatalf("expected 1 valid grain (invalid skipped), got %d", len(received))
	}
	if received[0].PTS != 1 {
		t.Fatalf("expected PTS=1 (monotonic, invalid grain skipped), got %d", received[0].PTS)
	}
	if received[0].Data[0] != 0x02 {
		t.Fatalf("expected data byte 0x02, got 0x%02X", received[0].Data[0])
	}
}

func TestReader_DataTooLateRecovery(t *testing.T) {
	flow := &tooLateDiscreteReader{
		headIdx: 200,
		grains: []mockGrain{
			{data: []byte{0x01}, info: GrainInfo{Index: 100, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
			// 2nd call returns "too late", then re-syncs.
			{data: []byte{0x02}, info: GrainInfo{Index: 198, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
			{data: []byte{0x03}, info: GrainInfo{Index: 199, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
		},
		tooLateAt: 1,
	}

	reader := NewDataReader(ReaderConfig{
		BufSize:   8,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)

	var received []DataGrain
	for g := range reader.Data() {
		received = append(received, g)
		if len(received) == 3 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 3 {
		t.Fatalf("expected 3 grains (1 before too-late, 2 after re-sync), got %d", len(received))
	}

	// PTS should be monotonic from 1.
	for i, g := range received {
		expectedPTS := int64(i + 1)
		if g.PTS != expectedPTS {
			t.Fatalf("grain[%d]: expected PTS=%d, got %d", i, expectedPTS, g.PTS)
		}
	}
}

func TestReader_DataStopsOnContextCancel(t *testing.T) {
	flow := &infiniteDiscreteReader{}

	reader := NewDataReader(ReaderConfig{
		BufSize:   2,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	reader.StartData(ctx, flow)

	// Read one grain, then cancel.
	select {
	case <-reader.Data():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first data grain")
	}

	cancel()
	reader.Wait() // Should return quickly.
}

func TestReader_DataStopsOnMaxErrors(t *testing.T) {
	flow := &failingDiscreteReader{}

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)

	// Reader should stop after maxConsecutiveErrors (50).
	reader.Wait()
	// If we got here, the reader stopped on its own.
}

func TestReader_DataChannelNilForVideo(t *testing.T) {
	reader := NewVideoReader(ReaderConfig{BufSize: 4})
	if reader.Data() != nil {
		t.Fatal("expected nil Data() channel for video reader")
	}
}

func TestReader_DataChannelNilForAudio(t *testing.T) {
	reader := NewAudioReader(ReaderConfig{BufSize: 4})
	if reader.Data() != nil {
		t.Fatal("expected nil Data() channel for audio reader")
	}
}

func TestReader_VideoAndAudioChannelNilForData(t *testing.T) {
	reader := NewDataReader(ReaderConfig{BufSize: 4})
	if reader.Video() != nil {
		t.Fatal("expected nil Video() channel for data reader")
	}
	if reader.Audio() != nil {
		t.Fatal("expected nil Audio() channel for data reader")
	}
	if reader.Data() == nil {
		t.Fatal("expected non-nil Data() channel for data reader")
	}
}

func TestReader_DataPTSMonotonic(t *testing.T) {
	// Data grains with high ring-buffer indices.
	// PTS should be monotonic from 1, NOT the MXL index values.
	grains := []mockGrain{
		{data: []byte{1}, info: GrainInfo{Index: 90000, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{2}, info: GrainInfo{Index: 90001, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{3}, info: GrainInfo{Index: 90002, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(grains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})
	flow.headIdx = 90000

	reader := NewDataReader(ReaderConfig{
		BufSize:   8,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)

	var received []DataGrain
	for g := range reader.Data() {
		received = append(received, g)
		if len(received) == 3 {
			cancel()
		}
	}
	reader.Wait()

	if len(received) != 3 {
		t.Fatalf("expected 3 grains, got %d", len(received))
	}

	for i, g := range received {
		expectedPTS := int64(i + 1)
		if g.PTS != expectedPTS {
			t.Fatalf("grain[%d]: expected PTS=%d (monotonic), got %d", i, expectedPTS, g.PTS)
		}
	}
}

func TestReader_DataHeadIndexError(t *testing.T) {
	// If HeadIndex fails at startup, dataLoop should return immediately.
	flow := &headIndexErrorReader{}

	reader := NewDataReader(ReaderConfig{
		BufSize:   4,
		TimeoutMs: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reader.StartData(ctx, flow)
	reader.Wait()

	// Channel should be closed (no grains delivered).
	select {
	case _, ok := <-reader.Data():
		if ok {
			t.Fatal("expected closed data channel, got a grain")
		}
	default:
		// Channel closed immediately.
	}
}

// headIndexErrorReader returns an error from HeadIndex.
type headIndexErrorReader struct{}

func (r *headIndexErrorReader) ReadGrain(_ uint64, _ uint64) ([]byte, GrainInfo, error) {
	return nil, GrainInfo{}, fmt.Errorf("should not be called")
}
func (r *headIndexErrorReader) ConfigInfo() FlowConfig { return FlowConfig{} }
func (r *headIndexErrorReader) HeadIndex() (uint64, error) {
	return 0, fmt.Errorf("head index unavailable")
}
func (r *headIndexErrorReader) Close() error { return nil }
