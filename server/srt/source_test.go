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

		gotAudioKey   string
		gotAudioPCM   []float32
		gotAudioPTS   int64
		gotAudioRate  int
		gotAudioChans int
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

	// Poll until stats are updated instead of using a fixed sleep.
	deadline := time.After(5 * time.Second)
	for {
		snap := stats.Snapshot()
		if snap.RTTMs != 0 && snap.PacketsReceived != 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("stats never polled within deadline")
		case <-time.After(100 * time.Millisecond):
		}
	}

	close(blockCh)
	src.Stop()

	if !mockConn.statsCalled.Load() {
		t.Error("expected Stats() to be called on SRT conn")
	}
	if !mockConn.statsIntervalMode.Load() {
		t.Error("expected Stats(true), which restarts the receive-rate window")
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

// TestSourceNaturalEOFReleasesResources verifies that when the decoder returns
// on its own (natural disconnect/EOF) without any explicit Stop() call, the
// Source cancels its context and closes the SRT conn, so the context-watcher
// and stats-poller goroutines exit instead of leaking. Regression test for the
// two-goroutine + conn/fd leak on every natural decode-EOF.
func TestSourceNaturalEOFReleasesResources(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:eof",
		Mode:     ModeListener,
		StreamID: "eof",
	}
	stats := NewConnStats(ModeListener, "eof", 120)
	conn := &mockReadCloser{}
	src := NewSource(config, conn, stats, slog.Default())

	// Mock decoder whose Run() returns immediately, simulating a natural EOF
	// with no subsequent Stop() from the app layer.
	var decStopCount atomic.Int32
	src.decoderFactory = func(cfg StreamDecoderConfig) (streamDecoder, error) {
		return &mockDecoder{
			runFunc:  func() {}, // returns immediately => natural EOF
			stopFunc: func() { decStopCount.Add(1) },
		}, nil
	}

	stoppedCh := make(chan struct{})
	src.OnStopped = func(key string) { close(stoppedCh) }

	if err := src.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// OnStopped must fire on the natural-EOF path.
	select {
	case <-stoppedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("OnStopped was not called after natural decode EOF")
	}

	// The context-watcher must have observed cancellation and called dec.Stop(),
	// and the conn must be closed — both proving the decode goroutine released
	// resources without an explicit Stop().
	deadline := time.After(2 * time.Second)
	for decStopCount.Load() == 0 || !conn.isClosed() {
		select {
		case <-deadline:
			t.Fatalf("resources not released after natural EOF: decStopCount=%d connClosed=%v",
				decStopCount.Load(), conn.isClosed())
		case <-time.After(10 * time.Millisecond):
		}
	}

	// All goroutines (context-watcher + stats-poller) must have exited; if they
	// leaked, Stop()'s wg.Wait() would block forever. Detect via a timeout.
	done := make(chan struct{})
	go func() {
		src.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return: goroutines leaked after natural EOF")
	}
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

func (m *mockReadCloser) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
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
	stats             srtgo.ConnStats
	statsCalled       atomic.Bool
	statsIntervalMode atomic.Bool
}

func (m *mockSRTConn) Stats(clear bool) srtgo.ConnStats {
	m.statsCalled.Store(true)
	m.statsIntervalMode.Store(clear)
	return m.stats
}

// TestSourceStatsCountCumulativeCounters covers the packet totals of an SRT
// input. srtgo reports packet counters cumulatively for a connection, also
// under Stats(true), while the ConnStats behind a source key lives across
// reconnects. Adding each reading as is counts every packet again on every
// one-second poll, so an hour-old input reports billions of packets. Each
// poll has to add only what changed, and a reconnect starts again from zero.
func TestSourceStatsCountCumulativeCounters(t *testing.T) {
	t.Parallel()

	config := SourceConfig{
		Key:      "srt:cam7",
		Mode:     ModeListener,
		StreamID: "cam7",
	}
	stats := NewConnStats(ModeListener, "cam7", 120)
	reading := func(n uint64) srtgo.ConnStats {
		return srtgo.ConnStats{
			RTT:          5 * time.Millisecond,
			RecvPackets:  1000 * n,
			RecvLoss:     10 * n,
			RecvDropped:  4 * n,
			RecvRetrans:  6 * n,
			RecvBelated:  2 * n,
			MbpsRecvRate: 6.0,
		}
	}

	mockConn := &mockSRTConn{}
	src := NewSource(config, mockConn, stats, slog.Default())
	ctx := context.Background()
	for n := uint64(1); n <= 3; n++ {
		mockConn.stats = reading(n)
		src.updateStats(ctx)
	}

	// The next connection under the same key counts from zero again.
	next := &mockSRTConn{stats: reading(1)}
	NewSource(config, next, stats, slog.Default()).updateStats(ctx)

	snap := stats.Snapshot()
	if snap.PacketsReceived != 4000 {
		t.Errorf("PacketsReceived = %d, want 4000 (3000 on the first connection, 1000 on the second)", snap.PacketsReceived)
	}
	if snap.PacketsLost != 40 {
		t.Errorf("PacketsLost = %d, want 40", snap.PacketsLost)
	}
	if snap.PacketsDropped != 16 {
		t.Errorf("PacketsDropped = %d, want 16", snap.PacketsDropped)
	}
	if snap.PacketsRetransmitted != 24 {
		t.Errorf("PacketsRetransmitted = %d, want 24", snap.PacketsRetransmitted)
	}
	if snap.PacketsBelated != 8 {
		t.Errorf("PacketsBelated = %d, want 8", snap.PacketsBelated)
	}
	// Rate/gauge fields are window values and must NOT accumulate.
	if snap.RecvRateMbps != 6.0 {
		t.Errorf("RecvRateMbps = %f, want 6.0 (window value, not accumulated)", snap.RecvRateMbps)
	}
}

// TestSourceStatsCountRealConnection polls a real srtgo connection the way
// a running input does: the received total has to match what was sent, not
// the sum of the cumulative readings.
func TestSourceStatsCountRealConnection(t *testing.T) {
	ln, err := srtgo.Listen("127.0.0.1:0", srtgo.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan *srtgo.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	tx, err := srtgo.Dial(ln.Addr().String(), srtgo.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Close() }()
	var rx *srtgo.Conn
	select {
	case rx = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection accepted")
	}
	defer func() { _ = rx.Close() }()
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, err := rx.Read(buf); err != nil {
				return
			}
		}
	}()

	stats := NewConnStats(ModeListener, "loop", 120)
	src := NewSource(SourceConfig{Key: "srt:loop", Mode: ModeListener, StreamID: "loop"}, rx, stats, slog.Default())
	ctx := context.Background()
	const perRound, rounds = 100, 3
	payload := make([]byte, 1316)
	for r := 1; r <= rounds; r++ {
		for i := 0; i < perRound; i++ {
			if _, err := tx.Write(payload); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for rx.Stats(false).RecvPackets < uint64(r*perRound) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		src.updateStats(ctx)
	}

	if got, want := stats.Snapshot().PacketsReceived, int64(rounds*perRound); got != want {
		t.Errorf("PacketsReceived = %d after %d packets sent", got, want)
	}
}
