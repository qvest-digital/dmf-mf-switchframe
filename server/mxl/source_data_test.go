package mxl

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSource_FansOutDataGrains(t *testing.T) {
	var received struct {
		mu     sync.Mutex
		grains []struct {
			key  string
			data []byte
			pts  int64
		}
	}

	dataGrains := []mockGrain{
		{data: []byte{0xAA, 0xBB}, info: GrainInfo{Index: 1, GrainSize: 2, TotalSlices: 1, ValidSlices: 1}},
		{data: []byte{0xCC, 0xDD, 0xEE}, info: GrainInfo{Index: 2, GrainSize: 3, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(dataGrains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		OnDataGrain: func(key string, data []byte, pts int64) {
			received.mu.Lock()
			defer received.mu.Unlock()
			cp := make([]byte, len(data))
			copy(cp, data)
			received.grains = append(received.grains, struct {
				key  string
				data []byte
				pts  int64
			}{key, cp, pts})
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Pass nil for video and audio, provide data flow.
	src.Start(ctx, nil, nil, flow)

	// Wait for data grains to be delivered.
	deadline := time.After(2 * time.Second)
	for {
		received.mu.Lock()
		n := len(received.grains)
		received.mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: only got %d data grains", n)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	received.mu.Lock()
	defer received.mu.Unlock()

	// Verify source key is correct.
	if received.grains[0].key != "cam1" {
		t.Fatalf("expected key 'cam1', got '%s'", received.grains[0].key)
	}

	// Verify data payload.
	if len(received.grains[0].data) != 2 || received.grains[0].data[0] != 0xAA {
		t.Fatalf("unexpected data in grain 0: %v", received.grains[0].data)
	}
	if len(received.grains[1].data) != 3 || received.grains[1].data[0] != 0xCC {
		t.Fatalf("unexpected data in grain 1: %v", received.grains[1].data)
	}

	// Verify PTS is monotonic from the reader.
	if received.grains[0].pts != 1 || received.grains[1].pts != 2 {
		t.Fatalf("expected PTS 1,2; got %d,%d", received.grains[0].pts, received.grains[1].pts)
	}
}

func TestSource_NilDataFlowNoDataReader(t *testing.T) {
	src := NewSource(SourceConfig{FlowName: "cam1"})

	ctx, cancel := context.WithCancel(context.Background())
	// No data flow provided.
	src.Start(ctx, nil, nil)

	// dataReader should not be created.
	if src.dataReader != nil {
		t.Fatal("expected nil dataReader when no data flow provided")
	}

	cancel()
	src.Stop()
}

func TestSource_NilDataFlowExplicitNil(t *testing.T) {
	src := NewSource(SourceConfig{FlowName: "cam1"})

	ctx, cancel := context.WithCancel(context.Background())
	// Explicit nil data flow.
	src.Start(ctx, nil, nil, nil)

	if src.dataReader != nil {
		t.Fatal("expected nil dataReader when explicit nil data flow provided")
	}

	cancel()
	src.Stop()
}

func TestSource_DataFanOutStopsCleanly(t *testing.T) {
	flow := &infiniteDiscreteReader{}

	src := NewSource(SourceConfig{
		FlowName:    "cam1",
		OnDataGrain: func(string, []byte, int64) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, nil, nil, flow)

	// Let it run briefly.
	time.Sleep(50 * time.Millisecond)

	cancel()
	done := make(chan struct{})
	go func() {
		src.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: source did not stop within 5s")
	}
}

func TestSource_AllThreeFlows(t *testing.T) {
	// Verify that video, audio, and data flows all work together.
	var videoReceived, audioReceived, dataReceived struct {
		mu    sync.Mutex
		count int
	}

	videoGrains := []mockGrain{
		{data: makeV210Frame(12, 2), info: GrainInfo{Index: 1, GrainSize: uint32(len(makeV210Frame(12, 2))), TotalSlices: 1, ValidSlices: 1}},
	}
	videoFlow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	audioSamples := []mockSamples{
		{pcm: [][]float32{{0.1, 0.2}, {0.3, 0.4}}},
	}
	audioFlow := newMockContinuousReader(audioSamples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	dataGrains := []mockGrain{
		{data: []byte{0x01, 0x02}, info: GrainInfo{Index: 1, GrainSize: 2, TotalSlices: 1, ValidSlices: 1}},
	}
	dataFlow := newMockDiscreteReader(dataGrains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    12,
		Height:   2,
		OnRawVideo: func(string, []byte, int, int, int64) {
			videoReceived.mu.Lock()
			videoReceived.count++
			videoReceived.mu.Unlock()
		},
		OnRawAudio: func(string, []float32, int64) {
			audioReceived.mu.Lock()
			audioReceived.count++
			audioReceived.mu.Unlock()
		},
		OnDataGrain: func(string, []byte, int64) {
			dataReceived.mu.Lock()
			dataReceived.count++
			dataReceived.mu.Unlock()
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	src.Start(ctx, videoFlow, audioFlow, dataFlow)

	// Wait for all three types of media to be delivered.
	deadline := time.After(2 * time.Second)
	for {
		videoReceived.mu.Lock()
		vn := videoReceived.count
		videoReceived.mu.Unlock()
		audioReceived.mu.Lock()
		an := audioReceived.count
		audioReceived.mu.Unlock()
		dataReceived.mu.Lock()
		dn := dataReceived.count
		dataReceived.mu.Unlock()

		if vn >= 1 && an >= 1 && dn >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: video=%d audio=%d data=%d", vn, an, dn)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()
}

func TestSource_DataWithNilCallback(t *testing.T) {
	// OnDataGrain is nil -- dataFanOut should not panic.
	dataGrains := []mockGrain{
		{data: []byte{0x01}, info: GrainInfo{Index: 1, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(dataGrains, FlowConfig{
		Format:    DataFormatData,
		GrainRate: Rational{25, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		// OnDataGrain intentionally nil.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	src.Start(ctx, nil, nil, flow)

	// Give it time to process the grain without panicking.
	time.Sleep(100 * time.Millisecond)

	cancel()
	src.Stop()
}
