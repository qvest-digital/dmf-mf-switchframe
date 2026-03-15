package srt

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	srtgo "github.com/zsiec/srtgo"
)

func TestSourceCallbackWiring(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam1",
		Mode:     ModeListener,
		StreamID: "cam1",
	}
	stats := NewConnStats(ModeListener, "cam1", 120)
	src := NewSource(config, &mockReadCloser{}, stats, slog.Default())

	// Wire callbacks
	var (
		gotVideoKey    string
		gotVideoWidth  int
		gotVideoHeight int
		gotVideoPTS    int64
		gotVideoYUV    []byte

		gotAudioKey     string
		gotAudioPCM     []float32
		gotAudioPTS     int64
		gotAudioRate    int
		gotAudioChans   int
	)

	src.OnRawVideo = func(key string, yuv []byte, width, height int, pts int64) {
		gotVideoKey = key
		gotVideoYUV = yuv
		gotVideoWidth = width
		gotVideoHeight = height
		gotVideoPTS = pts
	}
	src.OnRawAudio = func(key string, pcm []float32, pts int64, sampleRate, channels int) {
		gotAudioKey = key
		gotAudioPCM = pcm
		gotAudioPTS = pts
		gotAudioRate = sampleRate
		gotAudioChans = channels
	}

	// Call the internal handle methods directly
	testYUV := []byte{1, 2, 3, 4}
	src.handleVideoFrame(testYUV, 1920, 1080, 90000)

	if gotVideoKey != "srt:cam1" {
		t.Errorf("video key: got %q, want %q", gotVideoKey, "srt:cam1")
	}
	if gotVideoWidth != 1920 {
		t.Errorf("video width: got %d, want 1920", gotVideoWidth)
	}
	if gotVideoHeight != 1080 {
		t.Errorf("video height: got %d, want 1080", gotVideoHeight)
	}
	if gotVideoPTS != 90000 {
		t.Errorf("video PTS: got %d, want 90000", gotVideoPTS)
	}
	if len(gotVideoYUV) != 4 {
		t.Errorf("video YUV len: got %d, want 4", len(gotVideoYUV))
	}

	testPCM := []float32{0.1, 0.2, -0.1, -0.2}
	src.handleAudioFrame(testPCM, 180000, 48000, 2)

	if gotAudioKey != "srt:cam1" {
		t.Errorf("audio key: got %q, want %q", gotAudioKey, "srt:cam1")
	}
	if len(gotAudioPCM) != 4 {
		t.Errorf("audio PCM len: got %d, want 4", len(gotAudioPCM))
	}
	if gotAudioPTS != 180000 {
		t.Errorf("audio PTS: got %d, want 180000", gotAudioPTS)
	}
	if gotAudioRate != 48000 {
		t.Errorf("audio sampleRate: got %d, want 48000", gotAudioRate)
	}
	if gotAudioChans != 2 {
		t.Errorf("audio channels: got %d, want 2", gotAudioChans)
	}
}

func TestSourceCallbackNilSafe(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam2",
		Mode:     ModeListener,
		StreamID: "cam2",
	}
	stats := NewConnStats(ModeListener, "cam2", 120)
	src := NewSource(config, &mockReadCloser{}, stats, slog.Default())

	// Don't set any callbacks — should not panic
	src.handleVideoFrame([]byte{1, 2, 3}, 1920, 1080, 0)
	src.handleAudioFrame([]float32{0.1}, 0, 48000, 2)
}

func TestSourceConfig(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:       "srt:cam3",
		Mode:      ModeCaller,
		Address:   "192.168.1.100:6464",
		StreamID:  "cam3",
		Label:     "Camera 3",
		Position:  3,
		LatencyMs: 200,
	}
	stats := NewConnStats(ModeCaller, "cam3", 200)
	src := NewSource(config, &mockReadCloser{}, stats, slog.Default())

	got := src.Config()
	if got.Key != "srt:cam3" {
		t.Errorf("Config().Key: got %q, want %q", got.Key, "srt:cam3")
	}
	if got.Mode != ModeCaller {
		t.Errorf("Config().Mode: got %q, want %q", got.Mode, ModeCaller)
	}
	if got.Label != "Camera 3" {
		t.Errorf("Config().Label: got %q, want %q", got.Label, "Camera 3")
	}
}

func TestSourceStopCancelsContext(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam4",
		Mode:     ModeListener,
		StreamID: "cam4",
	}
	stats := NewConnStats(ModeListener, "cam4", 120)
	src := NewSource(config, &mockReadCloser{}, stats, slog.Default())

	// Inject a mock decoder factory so Start() doesn't need real FFmpeg
	ctxCancelled := make(chan struct{})
	src.decoderFactory = func(cfg StreamDecoderConfig) (streamDecoder, error) {
		return &mockDecoder{
			runFunc: func() {
				// Block until context is cancelled
				<-ctxCancelled
			},
			stopFunc: func() {
				close(ctxCancelled)
			},
		}, nil
	}

	var stoppedCalled atomic.Bool
	src.OnStopped = func(key string) {
		stoppedCalled.Store(true)
	}

	ctx := context.Background()
	err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give goroutines time to start
	time.Sleep(50 * time.Millisecond)

	src.Stop()

	// Verify Stop() completed (wg.Wait returned)
	if !stoppedCalled.Load() {
		// Give a small grace period for the OnStopped callback
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSourceStatsPolling(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam5",
		Mode:     ModeListener,
		StreamID: "cam5",
	}
	stats := NewConnStats(ModeListener, "cam5", 120)

	mockConn := &mockSRTConn{
		stats: srtgo.ConnStats{
			RTT:          5 * time.Millisecond,
			RTTVar:       1 * time.Millisecond,
			RecvPackets:  1000,
			RecvLossRate: 0.5,
			MbpsRecvRate: 6.0,
			RecvDropped:  2,
			RecvBelated:  1,
			FlightSize:   10,
			MsRcvBuf:     50 * time.Millisecond,
			RecvBufSize:  5,
		},
	}

	src := NewSource(config, mockConn, stats, slog.Default())

	// Inject mock decoder factory
	blockCh := make(chan struct{})
	src.decoderFactory = func(cfg StreamDecoderConfig) (streamDecoder, error) {
		return &mockDecoder{
			runFunc:  func() { <-blockCh },
			stopFunc: func() {},
		}, nil
	}

	ctx := context.Background()
	err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait long enough for at least one stats poll
	time.Sleep(1500 * time.Millisecond)

	// Check that stats were updated
	snap := stats.Snapshot()
	if snap.RTTMs == 0 {
		t.Error("expected RTTMs to be updated from stats polling")
	}
	if snap.PacketsReceived == 0 {
		t.Error("expected PacketsReceived to be updated from stats polling")
	}

	close(blockCh)
	src.Stop()

	if !mockConn.statsCalled.Load() {
		t.Error("expected Stats() to be called on SRT conn")
	}
}

func TestSourceDoubleStop(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam6",
		Mode:     ModeListener,
		StreamID: "cam6",
	}
	stats := NewConnStats(ModeListener, "cam6", 120)
	src := NewSource(config, &mockReadCloser{}, stats, slog.Default())

	// Inject mock decoder
	blockCh := make(chan struct{})
	var stopCount atomic.Int32
	src.decoderFactory = func(cfg StreamDecoderConfig) (streamDecoder, error) {
		return &mockDecoder{
			runFunc:  func() { <-blockCh },
			stopFunc: func() { stopCount.Add(1) },
		}, nil
	}

	ctx := context.Background()
	err := src.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	close(blockCh)
	time.Sleep(50 * time.Millisecond)

	// Stop twice — should not panic
	src.Stop()
	src.Stop()
}

// --- mock types ---

type mockReadCloser struct {
	mu     sync.Mutex
	closed bool
}

func (m *mockReadCloser) Read(p []byte) (int, error) {
	// Block forever (simulates waiting for SRT data)
	select {}
}

func (m *mockReadCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// streamDecoder is the interface matching StreamDecoder's methods used by Source.
// Defined in source.go; re-stated here for reference.

type mockDecoder struct {
	runFunc  func()
	stopFunc func()
}

func (m *mockDecoder) Run()  { m.runFunc() }
func (m *mockDecoder) Stop() { m.stopFunc() }

type mockSRTConn struct {
	mockReadCloser
	stats       srtgo.ConnStats
	statsCalled atomic.Bool
}

func (m *mockSRTConn) Stats(clear bool) srtgo.ConnStats {
	m.statsCalled.Store(true)
	return m.stats
}
