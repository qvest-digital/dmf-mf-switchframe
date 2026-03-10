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

func TestSource_AVSyncAligned(t *testing.T) {
	// Bug: video and audio use independent counter-based PTS starting from 0.
	// When video takes longer to produce its first grain (ring buffer errors,
	// codec warmup, etc.), its PTS starts at 3003 (~33ms) while audio has
	// been running for 200ms with PTS at ~18000. The browser sees the PTS
	// values as the canonical timeline and computes a ~167ms AV sync offset
	// that persists for the entire session.
	//
	// Fix: PTS should reflect wall-clock time relative to a shared epoch,
	// so video starting 200ms late gets a PTS of ~18000 (matching audio).

	const videoDelay = 200 * time.Millisecond

	// Audio flow: produces immediately. Provide plenty of grains.
	audioSamples := make([]mockSamples, 30)
	for i := range audioSamples {
		audioSamples[i] = mockSamples{pcm: [][]float32{{0.1, 0.2}, {0.3, 0.4}}}
	}
	audioFlow := newMockContinuousReader(audioSamples, FlowConfig{
		Format:       DataFormatAudio,
		GrainRate:    Rational{48000, 1},
		ChannelCount: 2,
	})

	// Video flow: delays 200ms before producing first grain.
	v210Data := makeV210Frame(12, 2)
	videoGrains := []mockGrain{
		{data: v210Data, info: GrainInfo{Index: 1, GrainSize: uint32(len(v210Data)), TotalSlices: 1, ValidSlices: 1}},
		{data: v210Data, info: GrainInfo{Index: 2, GrainSize: uint32(len(v210Data)), TotalSlices: 1, ValidSlices: 1}},
	}
	videoFlow := &delayedDiscreteReader{
		inner: newMockDiscreteReader(videoGrains, FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}}),
		delay: videoDelay,
	}

	var videoPTS struct {
		mu  sync.Mutex
		pts []int64
	}

	src := NewSource(SourceConfig{
		FlowName:   "test",
		Width:      12,
		Height:     2,
		SampleRate: 48000,
		Channels:   2,
		FPSNum:     30000,
		FPSDen:     1001,
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			videoPTS.mu.Lock()
			defer videoPTS.mu.Unlock()
			videoPTS.pts = append(videoPTS.pts, pts)
		},
		OnRawAudio: func(key string, pcm []float32, pts int64) {
			// don't need to track audio PTS for this test
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	src.Start(ctx, videoFlow, audioFlow)

	// Wait for video to produce at least one frame.
	deadline := time.After(2 * time.Second)
	for {
		videoPTS.mu.Lock()
		vn := len(videoPTS.pts)
		videoPTS.mu.Unlock()
		if vn >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout: no video PTS received")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancel()
	src.Stop()

	videoPTS.mu.Lock()
	defer videoPTS.mu.Unlock()

	firstVideoPTS := videoPTS.pts[0]

	// The first video PTS should reflect the ~200ms wall-clock delay.
	// In 90kHz ticks: 200ms = 18000 ticks.
	// With the bug, PTS = grain.PTS(1) * 90000 * 1001/30000 = 3003 ticks (~33ms).
	// With the fix, PTS ≈ 18000 ticks (200ms, from wall-clock).
	//
	// We check that PTS > 9000 ticks (100ms) — proving it accounts for the
	// delay rather than using the counter-based 3003.
	const minExpectedPTS int64 = 9000 // 100ms — conservative lower bound for 200ms delay
	if firstVideoPTS < minExpectedPTS {
		t.Errorf("first video PTS = %d ticks (%.1f ms), want >= %d ticks (100 ms); "+
			"PTS should reflect wall-clock time, not counter-based %d",
			firstVideoPTS, float64(firstVideoPTS)/90.0, minExpectedPTS, 3003)
	}
}

// delayedDiscreteReader wraps a discrete reader and blocks for the
// specified delay duration before delegating to the inner reader.
// This simulates hardware warmup / ring buffer initialization delay.
type delayedDiscreteReader struct {
	inner   DiscreteReader
	delay   time.Duration
	started time.Time
	once    sync.Once
}

func (d *delayedDiscreteReader) ReadGrain(index uint64, timeout uint64) ([]byte, GrainInfo, error) {
	d.once.Do(func() { d.started = time.Now() })
	remaining := d.delay - time.Since(d.started)
	if remaining > 0 {
		time.Sleep(remaining) // block until delay expires
	}
	return d.inner.ReadGrain(index, timeout)
}

func (d *delayedDiscreteReader) ConfigInfo() FlowConfig { return d.inner.ConfigInfo() }
func (d *delayedDiscreteReader) HeadIndex() (uint64, error) { return d.inner.HeadIndex() }
func (d *delayedDiscreteReader) Close() error { return d.inner.Close() }

func TestSource_NilFlowsNoOp(t *testing.T) {
	src := NewSource(SourceConfig{FlowName: "cam1"})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, nil, nil) // Both nil — should not crash.

	cancel()
	src.Stop()
}
