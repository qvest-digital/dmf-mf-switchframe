package mxl

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- Test helpers ---

// makeV210Frame creates a minimal valid V210 frame (12x2, all black).
func makeV210Frame(width, height int) []byte {
	// Create YUV420p first, then convert.
	yuvSize := width*height + width/2*height/2 + width/2*height/2
	yuv := make([]byte, yuvSize)
	for i := 0; i < width*height; i++ {
		yuv[i] = 16 // Y limited range black
	}
	for i := width * height; i < yuvSize; i++ {
		yuv[i] = 128 // Cb/Cr neutral
	}
	v210, _ := YUV420pToV210(yuv, width, height)
	return v210
}

func TestSource_FansOutToSwitcher(t *testing.T) {
	var received struct {
		mu  sync.Mutex
		yuv [][]byte
	}

	videoGrains := []mockGrain{
		{data: makeV210Frame(12, 2), info: GrainInfo{Index: 1, GrainSize: uint32(len(makeV210Frame(12, 2))), TotalSlices: 1, ValidSlices: 1}},
		{data: makeV210Frame(12, 2), info: GrainInfo{Index: 2, GrainSize: uint32(len(makeV210Frame(12, 2))), TotalSlices: 1, ValidSlices: 1}},
	}
	flow := newMockDiscreteReader(videoGrains, FlowConfig{
		Format:    DataFormatVideo,
		GrainRate: Rational{30, 1},
	})

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    12,
		Height:   2,
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			received.mu.Lock()
			defer received.mu.Unlock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			received.yuv = append(received.yuv, cp)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	src.Start(ctx, flow, nil)

	// Wait for frames to be delivered.
	deadline := time.After(2 * time.Second)
	for {
		received.mu.Lock()
		n := len(received.yuv)
		received.mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: only got %d frames", n)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	received.mu.Lock()
	defer received.mu.Unlock()

	// Verify YUV420p data was delivered.
	expectedSize := 12*2 + 6*1 + 6*1 // 12x2 YUV420p
	if len(received.yuv[0]) != expectedSize {
		t.Fatalf("expected YUV size %d, got %d", expectedSize, len(received.yuv[0]))
	}
}

func TestSource_FansOutToMixer(t *testing.T) {
	var received struct {
		mu  sync.Mutex
		pcm [][]float32
	}

	audioSamples := []mockSamples{
		{pcm: [][]float32{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}}},
	}
	flow := newMockContinuousReader(audioSamples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	src := NewSource(SourceConfig{
		FlowName:   "cam1",
		SampleRate: 48000,
		Channels:   2,
		OnRawAudio: func(key string, pcm []float32, pts int64) {
			received.mu.Lock()
			defer received.mu.Unlock()
			cp := make([]float32, len(pcm))
			copy(cp, pcm)
			received.pcm = append(received.pcm, cp)
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	src.Start(ctx, nil, flow)

	deadline := time.After(2 * time.Second)
	for {
		received.mu.Lock()
		n := len(received.pcm)
		received.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for audio")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	received.mu.Lock()
	defer received.mu.Unlock()

	// Should be interleaved: [L0,R0,L1,R1,L2,R2]
	if len(received.pcm[0]) != 6 {
		t.Fatalf("expected 6 interleaved samples, got %d", len(received.pcm[0]))
	}
	// L0=0.1, R0=0.4, L1=0.2, R1=0.5, L2=0.3, R2=0.6
	wantInterleaved := []float32{0.1, 0.4, 0.2, 0.5, 0.3, 0.6}
	for i, v := range wantInterleaved {
		if received.pcm[0][i] != v {
			t.Fatalf("pcm[%d] = %f, want %f", i, received.pcm[0][i], v)
		}
	}
}

func TestSource_StopsCleanly(t *testing.T) {
	flow := &infiniteDiscreteReader{}

	src := NewSource(SourceConfig{
		FlowName:   "cam1",
		Width:      12,
		Height:     2,
		OnRawVideo: func(string, []byte, int, int, int64) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, flow, nil)

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
		// Good — stopped cleanly.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: source did not stop within 5s")
	}
}

func TestInterleaveChannels(t *testing.T) {
	tests := []struct {
		name     string
		channels [][]float32
		want     []float32
	}{
		{
			name:     "stereo",
			channels: [][]float32{{1, 2, 3}, {4, 5, 6}},
			want:     []float32{1, 4, 2, 5, 3, 6},
		},
		{
			name:     "mono",
			channels: [][]float32{{1, 2, 3}},
			want:     []float32{1, 2, 3},
		},
		{
			name:     "empty",
			channels: [][]float32{},
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := interleaveChannels(tt.channels)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d] = %f, want %f", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSource_NilFlowsNoOp(t *testing.T) {
	src := NewSource(SourceConfig{FlowName: "cam1"})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, nil, nil) // Both nil — should not crash.

	cancel()
	src.Stop()
}
