package mxl

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"

	"github.com/zsiec/switchframe/server/transition"
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
		OnRawAudio: func(key string, pcm []float32, pts int64, channels int) {
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

func TestSource_DoubleStopNoPanic(t *testing.T) {
	src := NewSource(SourceConfig{
		FlowName:   "cam1",
		Width:      12,
		Height:     2,
		OnRawVideo: func(string, []byte, int, int, int64) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	flow := &infiniteDiscreteReader{}
	src.Start(ctx, flow, nil)

	time.Sleep(20 * time.Millisecond)
	cancel()

	// Calling Stop twice must not panic.
	src.Stop()
	src.Stop()
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
		OnRawAudio: func(key string, pcm []float32, pts int64, channels int) {
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

func (d *delayedDiscreteReader) ConfigInfo() FlowConfig     { return d.inner.ConfigInfo() }
func (d *delayedDiscreteReader) HeadIndex() (uint64, error) { return d.inner.HeadIndex() }
func (d *delayedDiscreteReader) Close() error               { return d.inner.Close() }

// makeV210FrameWithY creates a V210 frame where all Y samples are set to the given value.
// Cb/Cr are set to 128 (neutral chroma). Width must be divisible by 6, height must be even.
func makeV210FrameWithY(width, height int, yVal byte) []byte {
	ySize := width * height
	chromaW := width / 2
	cSize := chromaW * (height / 2)
	yuv := make([]byte, ySize+2*cSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = yVal
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}
	v210, _ := YUV420pToV210(yuv, width, height)
	return v210
}

func TestSource_OnRawVideoBufferNotAliased(t *testing.T) {
	// Bug: processVideoGrain passes s.v210Bufs.yuvOut directly to OnRawVideo.
	// The next frame's V210→YUV420 conversion overwrites this buffer, corrupting
	// any data retained by the OnRawVideo consumer. The encoder path was previously
	// fixed (copies to s.encoderYUV), but the raw video sink was not.
	//
	// This test captures the buffer reference from OnRawVideo (without copying),
	// then processes a second frame with different pixel content, and verifies
	// the first buffer was NOT overwritten.

	const (
		width  = 12
		height = 2
		yVal1  = 64  // Y value for frame 1
		yVal2  = 200 // Y value for frame 2 (distinctly different)
	)

	v210Frame1 := makeV210FrameWithY(width, height, yVal1)
	v210Frame2 := makeV210FrameWithY(width, height, yVal2)

	var capturedBuf []byte
	var capturedSnapshot []byte

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    width,
		Height:   height,
		OnRawVideo: func(key string, yuv []byte, w, h int, pts int64) {
			if capturedBuf == nil {
				// First call: capture the buffer reference (no copy!)
				capturedBuf = yuv
				// Also snapshot the content for comparison.
				capturedSnapshot = make([]byte, len(yuv))
				copy(capturedSnapshot, yuv)
			}
		},
	})

	now := time.Now()

	// Process frame 1.
	src.processVideoGrain(VideoGrain{
		V210:     v210Frame1,
		Width:    width,
		Height:   height,
		PTS:      1,
		ReadTime: now,
	})

	if capturedBuf == nil {
		t.Fatal("OnRawVideo was not called for frame 1")
	}

	// Verify the Y plane of captured frame 1 contains yVal1.
	for i := 0; i < width*height; i++ {
		if capturedSnapshot[i] != yVal1 {
			t.Fatalf("frame 1 Y[%d] = %d, want %d", i, capturedSnapshot[i], yVal1)
		}
	}

	// Process frame 2 with distinctly different Y values.
	src.processVideoGrain(VideoGrain{
		V210:     v210Frame2,
		Width:    width,
		Height:   height,
		PTS:      2,
		ReadTime: now.Add(33 * time.Millisecond),
	})

	// The captured buffer from frame 1 should still contain its original data.
	// With the bug, v210Bufs.yuvOut is reused, so capturedBuf now contains
	// frame 2's data (yVal2 instead of yVal1).
	for i := 0; i < width*height; i++ {
		if capturedBuf[i] != capturedSnapshot[i] {
			t.Fatalf("OnRawVideo buffer was mutated by subsequent frame: "+
				"Y[%d] = %d (frame 2 value), want %d (frame 1 value); "+
				"buffer passed to OnRawVideo must not alias v210Bufs.yuvOut",
				i, capturedBuf[i], capturedSnapshot[i])
		}
	}
}

func TestSource_StopClosesFlowReaders(t *testing.T) {
	// Finding #5: Flow readers opened by inst.OpenReader() / inst.OpenAudioReader()
	// hold C handles (in cgo builds). Source.Stop() stops goroutines but never
	// calls Close() on the readers. When mxlInstance.Close() is called next,
	// these unclosed handles become dangling pointers — use-after-free in cgo.
	//
	// After the fix: Source.Stop() must call Close() on all flow readers.
	videoFlow := &closeTrackingDiscreteReader{
		inner: newMockDiscreteReader(
			[]mockGrain{
				{data: makeV210Frame(12, 2), info: GrainInfo{Index: 1, GrainSize: uint32(len(makeV210Frame(12, 2))), TotalSlices: 1, ValidSlices: 1}},
			},
			FlowConfig{Format: DataFormatVideo, GrainRate: Rational{30, 1}},
		),
	}
	audioFlow := &closeTrackingContinuousReader{
		inner: newMockContinuousReader(
			[]mockSamples{{pcm: [][]float32{{0.1}, {0.2}}}},
			FlowConfig{Format: DataFormatAudio, GrainRate: Rational{48000, 1}, ChannelCount: 2},
		),
	}
	dataFlow := &closeTrackingDiscreteReader{
		inner: newMockDiscreteReader(
			[]mockGrain{
				{data: []byte{0x01}, info: GrainInfo{Index: 1, GrainSize: 1, TotalSlices: 1, ValidSlices: 1}},
			},
			FlowConfig{Format: DataFormatData, GrainRate: Rational{25, 1}},
		),
	}

	src := NewSource(SourceConfig{
		FlowName:    "cam1",
		Width:       12,
		Height:      2,
		SampleRate:  48000,
		Channels:    2,
		OnRawVideo:  func(string, []byte, int, int, int64) {},
		OnRawAudio:  func(string, []float32, int64, int) {},
		OnDataGrain: func(string, []byte, int64) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, videoFlow, audioFlow, dataFlow)

	// Let flows produce some data.
	time.Sleep(50 * time.Millisecond)

	cancel()
	src.Stop()

	// After Stop returns, all flow readers must be closed.
	videoFlow.mu.Lock()
	videoClosed := videoFlow.closed
	videoFlow.mu.Unlock()
	if !videoClosed {
		t.Fatal("video flow reader not closed by Source.Stop(); " +
			"unclosed C handle would be use-after-free when mxlInstance.Close() is called")
	}

	audioFlow.mu.Lock()
	audioClosed := audioFlow.closed
	audioFlow.mu.Unlock()
	if !audioClosed {
		t.Fatal("audio flow reader not closed by Source.Stop(); " +
			"unclosed C handle would be use-after-free when mxlInstance.Close() is called")
	}

	dataFlow.mu.Lock()
	dataClosed := dataFlow.closed
	dataFlow.mu.Unlock()
	if !dataClosed {
		t.Fatal("data flow reader not closed by Source.Stop(); " +
			"unclosed C handle would be use-after-free when mxlInstance.Close() is called")
	}
}

func TestSource_StopClosesFlowReadersVideoOnly(t *testing.T) {
	// Verify Close is called even when only a video flow is provided.
	videoFlow := &closeTrackingDiscreteReader{
		inner: &infiniteDiscreteReader{},
	}

	src := NewSource(SourceConfig{
		FlowName:   "cam1",
		Width:      12,
		Height:     2,
		OnRawVideo: func(string, []byte, int, int, int64) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, videoFlow, nil)

	time.Sleep(30 * time.Millisecond)
	cancel()
	src.Stop()

	videoFlow.mu.Lock()
	closed := videoFlow.closed
	videoFlow.mu.Unlock()
	if !closed {
		t.Fatal("video flow reader not closed by Source.Stop() (video-only case)")
	}
}

// closeTrackingDiscreteReader wraps a DiscreteReader and tracks Close() calls.
type closeTrackingDiscreteReader struct {
	inner  DiscreteReader
	mu     sync.Mutex
	closed bool
}

func (r *closeTrackingDiscreteReader) ReadGrain(index uint64, timeout uint64) ([]byte, GrainInfo, error) {
	return r.inner.ReadGrain(index, timeout)
}

func (r *closeTrackingDiscreteReader) ConfigInfo() FlowConfig { return r.inner.ConfigInfo() }
func (r *closeTrackingDiscreteReader) HeadIndex() (uint64, error) {
	return r.inner.HeadIndex()
}

func (r *closeTrackingDiscreteReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.inner.Close()
}

// closeTrackingContinuousReader wraps a ContinuousReader and tracks Close() calls.
type closeTrackingContinuousReader struct {
	inner  ContinuousReader
	mu     sync.Mutex
	closed bool
}

func (r *closeTrackingContinuousReader) ReadSamples(index uint64, count int, timeout uint64) ([][]float32, error) {
	return r.inner.ReadSamples(index, count, timeout)
}

func (r *closeTrackingContinuousReader) ConfigInfo() FlowConfig { return r.inner.ConfigInfo() }
func (r *closeTrackingContinuousReader) HeadIndex() (uint64, error) {
	return r.inner.HeadIndex()
}

func (r *closeTrackingContinuousReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return r.inner.Close()
}

func TestSource_NilFlowsNoOp(t *testing.T) {
	src := NewSource(SourceConfig{FlowName: "cam1"})

	ctx, cancel := context.WithCancel(context.Background())
	src.Start(ctx, nil, nil) // Both nil — should not crash.

	cancel()
	src.Stop()
}

// --- Mock types for dual-encode tests ---

// mockPreviewEncoder records raw YUV sends.
type mockPreviewEncoder struct {
	mu     sync.Mutex
	frames []previewFrame
}

type previewFrame struct {
	yuv []byte
	w   int
	h   int
	pts int64
}

func (m *mockPreviewEncoder) Send(yuv []byte, w, h int, pts int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(yuv))
	copy(cp, yuv)
	m.frames = append(m.frames, previewFrame{yuv: cp, w: w, h: h, pts: pts})
}

func (m *mockPreviewEncoder) getFrames() []previewFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]previewFrame, len(m.frames))
	copy(cp, m.frames)
	return cp
}

// mockReplayViewer records encoded video and audio frames sent directly.
type mockReplayViewer struct {
	mu          sync.Mutex
	videoFrames []*media.VideoFrame
	audioFrames []*media.AudioFrame
}

func (m *mockReplayViewer) SendVideo(frame *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoFrames = append(m.videoFrames, frame)
}

func (m *mockReplayViewer) SendAudio(frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioFrames = append(m.audioFrames, frame)
}

func (m *mockReplayViewer) getVideoFrames() []*media.VideoFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.VideoFrame, len(m.videoFrames))
	copy(cp, m.videoFrames)
	return cp
}

func (m *mockReplayViewer) getAudioFrames() []*media.AudioFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.AudioFrame, len(m.audioFrames))
	copy(cp, m.audioFrames)
	return cp
}

// mockMediaBroadcasterForTest records broadcasts for verification.
type mockMediaBroadcasterForTest struct {
	mu          sync.Mutex
	videoFrames []*media.VideoFrame
	audioFrames []*media.AudioFrame
}

func (m *mockMediaBroadcasterForTest) BroadcastVideo(frame *media.VideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoFrames = append(m.videoFrames, frame)
}

func (m *mockMediaBroadcasterForTest) BroadcastAudio(frame *media.AudioFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioFrames = append(m.audioFrames, frame)
}

func (m *mockMediaBroadcasterForTest) getVideoFrames() []*media.VideoFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.VideoFrame, len(m.videoFrames))
	copy(cp, m.videoFrames)
	return cp
}

func (m *mockMediaBroadcasterForTest) getAudioFrames() []*media.AudioFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]*media.AudioFrame, len(m.audioFrames))
	copy(cp, m.audioFrames)
	return cp
}

// fakeVideoEncoder produces minimal valid Annex B output.
// First call produces SPS + PPS + IDR (keyframe). Subsequent calls produce P-frames.
type fakeVideoEncoder struct {
	callCount int
}

func (e *fakeVideoEncoder) Encode(yuv []byte, pts int64, forceIDR bool) ([]byte, bool, error) {
	e.callCount++
	if e.callCount == 1 || forceIDR {
		// Keyframe: SPS (NAL type 7) + PPS (NAL type 8) + IDR (NAL type 5)
		var out []byte
		out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
		out = append(out, 0x67, 0x42, 0xC0, 0x1E)  // SPS: type 7, baseline, constraint, level 3.0
		out = append(out, 0x00, 0x00, 0x00, 0x01)  // start code
		out = append(out, 0x68, 0xCE, 0x38, 0x80)  // PPS: type 8
		out = append(out, 0x00, 0x00, 0x00, 0x01)  // start code
		out = append(out, 0x65, 0x88)               // IDR: type 5
		return out, true, nil
	}
	// P-frame (NAL type 1)
	var out []byte
	out = append(out, 0x00, 0x00, 0x00, 0x01) // start code
	out = append(out, 0x41, 0x9A)              // non-IDR: type 1
	return out, false, nil
}

func (e *fakeVideoEncoder) Close() {}

// fakeAudioEncoder produces minimal AAC-like output.
type fakeAudioEncoder struct{}

func (e *fakeAudioEncoder) Encode(pcm []float32) ([]byte, error) {
	// Return minimal non-empty data to simulate AAC encoding.
	return []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x00, 0xFC, 0xDE, 0x04, 0x00}, nil
}

func (e *fakeAudioEncoder) Close() error { return nil }

func TestSource_DualEncode_PreviewAndReplay(t *testing.T) {
	// When both PreviewEncoder and ReplayViewer are set:
	// - PreviewEncoder should receive raw YUV (for browser relay at preview quality)
	// - ReplayViewer should receive full-quality encoded H.264 frames
	// - The relay broadcaster should NOT receive video (preview encoder handles relay)

	preview := &mockPreviewEncoder{}
	replay := &mockReplayViewer{}
	relay := &mockMediaBroadcasterForTest{}

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    12,
		Height:   2,
		Relay:    relay,
		EncoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &fakeVideoEncoder{}, nil
		},
		AudioEncoderFactory: func(sampleRate, channels int) (AudioEnc, error) {
			return &fakeAudioEncoder{}, nil
		},
		PreviewEncoder: preview,
		ReplayViewer:   replay,
		OnRawVideo:     func(string, []byte, int, int, int64) {},
		OnRawAudio:     func(string, []float32, int64, int) {},
	})

	now := time.Now()

	// Process 2 video frames directly (bypassing MXL reader for test isolation).
	src.processVideoGrain(VideoGrain{
		V210:     makeV210Frame(12, 2),
		Width:    12,
		Height:   2,
		PTS:      1,
		ReadTime: now,
	})
	src.processVideoGrain(VideoGrain{
		V210:     makeV210Frame(12, 2),
		Width:    12,
		Height:   2,
		PTS:      2,
		ReadTime: now.Add(33 * time.Millisecond),
	})

	// Process 1 audio grain.
	src.processAudioGrain(AudioGrain{
		PCM:        [][]float32{{0.1, 0.2}, {0.3, 0.4}},
		SampleRate: 48000,
		Channels:   2,
		ReadTime:   now,
	})

	// Verify preview encoder received raw YUV for both frames.
	previewFrames := preview.getFrames()
	if len(previewFrames) != 2 {
		t.Fatalf("preview encoder: expected 2 frames, got %d", len(previewFrames))
	}
	expectedYUVSize := 12*2 + 6*1 + 6*1 // 12x2 YUV420p
	if len(previewFrames[0].yuv) != expectedYUVSize {
		t.Fatalf("preview frame YUV size: expected %d, got %d", expectedYUVSize, len(previewFrames[0].yuv))
	}

	// Verify replay viewer received encoded H.264 video frames.
	replayVideoFrames := replay.getVideoFrames()
	if len(replayVideoFrames) != 2 {
		t.Fatalf("replay viewer: expected 2 video frames, got %d", len(replayVideoFrames))
	}

	// First frame should be a keyframe with SPS/PPS.
	if !replayVideoFrames[0].IsKeyframe {
		t.Fatal("replay first frame should be a keyframe")
	}
	if replayVideoFrames[0].SPS == nil {
		t.Fatal("replay first frame should have SPS")
	}
	if replayVideoFrames[0].PPS == nil {
		t.Fatal("replay first frame should have PPS")
	}
	if replayVideoFrames[0].Codec != "h264" {
		t.Fatalf("replay frame codec: expected h264, got %s", replayVideoFrames[0].Codec)
	}

	// WireData should be AVC1 format (4-byte length prefix, not Annex B start codes).
	wd := replayVideoFrames[0].WireData
	if len(wd) < 4 {
		t.Fatal("replay frame WireData too short")
	}
	firstNALULen := binary.BigEndian.Uint32(wd[:4])
	if firstNALULen == 0 || firstNALULen > uint32(len(wd)) {
		t.Fatalf("replay frame WireData not valid AVC1: first NALU length = %d", firstNALULen)
	}

	// Second frame should be a P-frame (not keyframe).
	if replayVideoFrames[1].IsKeyframe {
		t.Fatal("replay second frame should not be a keyframe")
	}

	// Verify replay viewer received encoded audio.
	replayAudioFrames := replay.getAudioFrames()
	if len(replayAudioFrames) != 1 {
		t.Fatalf("replay viewer: expected 1 audio frame, got %d", len(replayAudioFrames))
	}
	if len(replayAudioFrames[0].Data) == 0 {
		t.Fatal("replay audio frame should have non-empty data")
	}

	// Verify relay did NOT receive video (preview encoder handles relay path).
	relayVideoFrames := relay.getVideoFrames()
	if len(relayVideoFrames) != 0 {
		t.Fatalf("relay should NOT receive video in dual-encode mode, got %d frames", len(relayVideoFrames))
	}

	// Verify relay DID receive audio (audio always goes through relay).
	relayAudioFrames := relay.getAudioFrames()
	if len(relayAudioFrames) != 1 {
		t.Fatalf("relay: expected 1 audio frame, got %d", len(relayAudioFrames))
	}

	src.Stop()
}

func TestSource_PreviewOnlyPath_NoReplay(t *testing.T) {
	// When PreviewEncoder is set but ReplayViewer is nil:
	// - PreviewEncoder should receive raw YUV
	// - No full-quality encode should happen (existing behavior)

	preview := &mockPreviewEncoder{}
	relay := &mockMediaBroadcasterForTest{}

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    12,
		Height:   2,
		Relay:    relay,
		EncoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			t.Fatal("encoder factory should not be called in preview-only mode")
			return nil, nil
		},
		PreviewEncoder: preview,
		OnRawVideo:     func(string, []byte, int, int, int64) {},
	})

	now := time.Now()
	src.processVideoGrain(VideoGrain{
		V210:     makeV210Frame(12, 2),
		Width:    12,
		Height:   2,
		PTS:      1,
		ReadTime: now,
	})

	previewFrames := preview.getFrames()
	if len(previewFrames) != 1 {
		t.Fatalf("preview encoder: expected 1 frame, got %d", len(previewFrames))
	}

	// Relay should not receive anything (preview encoder handles it).
	relayVideoFrames := relay.getVideoFrames()
	if len(relayVideoFrames) != 0 {
		t.Fatalf("relay should NOT receive video in preview-only mode, got %d frames", len(relayVideoFrames))
	}

	src.Stop()
}

func TestSource_StandardPath_NoPreview(t *testing.T) {
	// When neither PreviewEncoder nor ReplayViewer is set:
	// - Full-quality encode should feed the relay

	relay := &mockMediaBroadcasterForTest{}

	src := NewSource(SourceConfig{
		FlowName: "cam1",
		Width:    12,
		Height:   2,
		Relay:    relay,
		EncoderFactory: func(w, h, bitrate, fpsNum, fpsDen int) (transition.VideoEncoder, error) {
			return &fakeVideoEncoder{}, nil
		},
		OnRawVideo: func(string, []byte, int, int, int64) {},
	})

	now := time.Now()
	src.processVideoGrain(VideoGrain{
		V210:     makeV210Frame(12, 2),
		Width:    12,
		Height:   2,
		PTS:      1,
		ReadTime: now,
	})

	relayVideoFrames := relay.getVideoFrames()
	if len(relayVideoFrames) != 1 {
		t.Fatalf("relay: expected 1 video frame, got %d", len(relayVideoFrames))
	}
	if !relayVideoFrames[0].IsKeyframe {
		t.Fatal("relay first frame should be a keyframe")
	}

	src.Stop()
}
