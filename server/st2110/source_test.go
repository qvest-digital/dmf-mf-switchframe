package st2110

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// --- Mock receivers ---

// mockVideoReceiver delivers pre-loaded frames, then blocks on ctx.Done().
type mockVideoReceiver struct {
	frames []mockVideoFrame
	closed bool
	mu     sync.Mutex
}

type mockVideoFrame struct {
	yuv  []byte
	w, h int
	pts  int64
}

func (m *mockVideoReceiver) GetFrame(ctx context.Context) ([]byte, int, int, int64, error) {
	m.mu.Lock()
	if len(m.frames) > 0 {
		f := m.frames[0]
		m.frames = m.frames[1:]
		m.mu.Unlock()
		return f.yuv, f.w, f.h, f.pts, nil
	}
	m.mu.Unlock()
	// Exhausted: block until context cancelled.
	<-ctx.Done()
	return nil, 0, 0, 0, ctx.Err()
}

func (m *mockVideoReceiver) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// mockAudioReceiver delivers pre-loaded frames, then blocks on ctx.Done().
type mockAudioReceiver struct {
	frames []mockAudioFrame
	closed bool
	mu     sync.Mutex
}

type mockAudioFrame struct {
	pcm          []float32
	samplesPerCh int
}

func (m *mockAudioReceiver) GetFrame(ctx context.Context) ([]float32, int, error) {
	m.mu.Lock()
	if len(m.frames) > 0 {
		f := m.frames[0]
		m.frames = m.frames[1:]
		m.mu.Unlock()
		return f.pcm, f.samplesPerCh, nil
	}
	m.mu.Unlock()
	<-ctx.Done()
	return nil, 0, ctx.Err()
}

func (m *mockAudioReceiver) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// mockAncillaryReceiver delivers pre-loaded frames, then blocks on ctx.Done().
type mockAncillaryReceiver struct {
	frames [][]byte
	closed bool
	mu     sync.Mutex
}

func (m *mockAncillaryReceiver) GetFrame(ctx context.Context) ([]byte, error) {
	m.mu.Lock()
	if len(m.frames) > 0 {
		f := m.frames[0]
		m.frames = m.frames[1:]
		m.mu.Unlock()
		return f, nil
	}
	m.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockAncillaryReceiver) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// --- Tests ---

func TestSource_VideoFanOut(t *testing.T) {
	frame1 := []byte{1, 2, 3, 4}
	frame2 := []byte{5, 6, 7, 8}
	videoRx := &mockVideoReceiver{
		frames: []mockVideoFrame{
			{yuv: frame1, w: 1920, h: 1080, pts: 0},
			{yuv: frame2, w: 1920, h: 1080, pts: 3003},
		},
	}

	var mu sync.Mutex
	var received []struct {
		key           string
		yuv           []byte
		width, height int
		pts           int64
	}

	src := NewSource(SourceConfig{
		Key:      "cam1",
		Channels: 2,
		OnRawVideo: func(key string, yuv []byte, width, height int, pts int64) {
			mu.Lock()
			defer mu.Unlock()
			cp := make([]byte, len(yuv))
			copy(cp, yuv)
			received = append(received, struct {
				key           string
				yuv           []byte
				width, height int
				pts           int64
			}{key, cp, width, height, pts})
		},
		Logger: slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, videoRx, nil, nil)

	// Wait for both frames to be delivered.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for video frames")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	src.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) < 2 {
		t.Fatalf("expected at least 2 frames, got %d", len(received))
	}

	// Verify frame 1
	if received[0].key != "cam1" {
		t.Errorf("frame 0 key = %q, want %q", received[0].key, "cam1")
	}
	if received[0].width != 1920 || received[0].height != 1080 {
		t.Errorf("frame 0 size = %dx%d, want 1920x1080", received[0].width, received[0].height)
	}
	for i, b := range frame1 {
		if received[0].yuv[i] != b {
			t.Errorf("frame 0 yuv[%d] = %d, want %d", i, received[0].yuv[i], b)
		}
	}

	// Verify frame 2
	if received[1].key != "cam1" {
		t.Errorf("frame 1 key = %q, want %q", received[1].key, "cam1")
	}
	for i, b := range frame2 {
		if received[1].yuv[i] != b {
			t.Errorf("frame 1 yuv[%d] = %d, want %d", i, received[1].yuv[i], b)
		}
	}

	// PTS: since mock returns pts=0 for first frame and pts=3003 for second,
	// and non-zero PTP timestamps should be used directly.
	// First frame pts=0 triggers wall-clock fallback; second frame pts=3003
	// is non-zero so it should be used as-is.
	if received[1].pts != 3003 {
		t.Errorf("frame 1 pts = %d, want 3003 (direct PTP)", received[1].pts)
	}
}

func TestSource_AudioFanOut(t *testing.T) {
	pcm := []float32{0.1, -0.1, 0.2, -0.2}
	audioRx := &mockAudioReceiver{
		frames: []mockAudioFrame{
			{pcm: pcm, samplesPerCh: 2},
		},
	}

	var mu sync.Mutex
	var received []struct {
		key      string
		pcm      []float32
		pts      int64
		channels int
	}

	src := NewSource(SourceConfig{
		Key:      "cam2",
		Channels: 2,
		OnRawAudio: func(key string, pcm []float32, pts int64, channels int) {
			mu.Lock()
			defer mu.Unlock()
			cp := make([]float32, len(pcm))
			copy(cp, pcm)
			received = append(received, struct {
				key      string
				pcm      []float32
				pts      int64
				channels int
			}{key, cp, pts, channels})
		},
		Logger: slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, nil, audioRx, nil)

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for audio frame")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	src.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) < 1 {
		t.Fatalf("expected at least 1 audio frame, got %d", len(received))
	}

	if received[0].key != "cam2" {
		t.Errorf("key = %q, want %q", received[0].key, "cam2")
	}
	if received[0].channels != 2 {
		t.Errorf("channels = %d, want 2", received[0].channels)
	}
	if len(received[0].pcm) != len(pcm) {
		t.Fatalf("pcm length = %d, want %d", len(received[0].pcm), len(pcm))
	}
	for i, v := range pcm {
		if received[0].pcm[i] != v {
			t.Errorf("pcm[%d] = %f, want %f", i, received[0].pcm[i], v)
		}
	}
}

func TestSource_AncillaryFanOut(t *testing.T) {
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	ancRx := &mockAncillaryReceiver{
		frames: [][]byte{data},
	}

	var mu sync.Mutex
	var received []struct {
		key  string
		data []byte
		pts  int64
	}

	src := NewSource(SourceConfig{
		Key:      "cam3",
		Channels: 2,
		OnDataGrain: func(key string, data []byte, pts int64) {
			mu.Lock()
			defer mu.Unlock()
			cp := make([]byte, len(data))
			copy(cp, data)
			received = append(received, struct {
				key  string
				data []byte
				pts  int64
			}{key, cp, pts})
		},
		Logger: slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, nil, nil, ancRx)

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for ancillary data")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	src.Stop()

	mu.Lock()
	defer mu.Unlock()

	if len(received) < 1 {
		t.Fatalf("expected at least 1 data grain, got %d", len(received))
	}

	if received[0].key != "cam3" {
		t.Errorf("key = %q, want %q", received[0].key, "cam3")
	}
	for i, b := range data {
		if received[0].data[i] != b {
			t.Errorf("data[%d] = %02X, want %02X", i, received[0].data[i], b)
		}
	}
}

func TestSource_StopIdempotent(t *testing.T) {
	videoRx := &mockVideoReceiver{}
	audioRx := &mockAudioReceiver{}

	src := NewSource(SourceConfig{
		Key:      "cam4",
		Channels: 2,
		Logger:   slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, videoRx, audioRx, nil)

	// Calling Stop() twice should not panic.
	src.Stop()
	src.Stop()

	// Also verify receivers were closed.
	videoRx.mu.Lock()
	defer videoRx.mu.Unlock()
	if !videoRx.closed {
		t.Error("video receiver should have been closed")
	}

	audioRx.mu.Lock()
	defer audioRx.mu.Unlock()
	if !audioRx.closed {
		t.Error("audio receiver should have been closed")
	}
}

func TestSource_NilReceivers(t *testing.T) {
	// Starting with all nil receivers should not panic.
	src := NewSource(SourceConfig{
		Key:    "cam5",
		Logger: slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, nil, nil, nil)
	src.Stop()
}

func TestSource_WallClockPTS(t *testing.T) {
	// When PTP timestamp is 0, wall-clock PTS should be used.
	// First frame sets epoch (pts=0), subsequent frames get wall-clock offset.
	videoRx := &mockVideoReceiver{
		frames: []mockVideoFrame{
			{yuv: []byte{1}, w: 16, h: 16, pts: 0},
			{yuv: []byte{2}, w: 16, h: 16, pts: 0},
		},
	}

	var mu sync.Mutex
	var ptsValues []int64

	src := NewSource(SourceConfig{
		Key:      "cam6",
		Channels: 2,
		OnRawVideo: func(key string, yuv []byte, width, height int, pts int64) {
			mu.Lock()
			defer mu.Unlock()
			ptsValues = append(ptsValues, pts)
		},
		Logger: slog.Default(),
	})

	ctx := context.Background()
	src.Start(ctx, videoRx, nil, nil)

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(ptsValues)
		mu.Unlock()
		if n >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for video frames")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	src.Stop()

	mu.Lock()
	defer mu.Unlock()

	// First frame should have pts ~0 (it sets the epoch).
	// Second frame should have pts > 0 (wall-clock elapsed).
	if ptsValues[0] != 0 {
		t.Errorf("first frame pts = %d, want 0 (epoch)", ptsValues[0])
	}
	if ptsValues[1] < 0 {
		t.Errorf("second frame pts = %d, want >= 0", ptsValues[1])
	}
}
