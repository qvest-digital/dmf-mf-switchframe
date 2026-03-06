package replay

import (
	"sync"
	"testing"
	"time"

	"github.com/zsiec/prism/media"
)

// mockRelay implements the minimal relay interface for testing.
type mockRelay struct {
	mu     sync.Mutex
	videos []*media.VideoFrame
}

func (r *mockRelay) BroadcastVideo(frame *media.VideoFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.videos = append(r.videos, frame)
}

func (r *mockRelay) BroadcastAudio(frame *media.AudioFrame) {}

func TestReplayManager_New(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	defer m.Close()
}

func TestReplayManager_AddSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	status := m.Status()
	if len(status.Buffers) != 1 {
		t.Errorf("expected 1 buffer, got %d", len(status.Buffers))
	}
	if status.Buffers[0].Source != "cam1" {
		t.Errorf("expected source 'cam1', got %q", status.Buffers[0].Source)
	}
}

func TestReplayManager_AddSource_Duplicate(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.AddSource("cam1") // Duplicate: should return nil, not create second buffer.

	status := m.Status()
	if len(status.Buffers) != 1 {
		t.Errorf("expected 1 buffer after duplicate add, got %d", len(status.Buffers))
	}
}

func TestReplayManager_RemoveSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	m.RemoveSource("cam1")

	status := m.Status()
	if len(status.Buffers) != 0 {
		t.Errorf("expected 0 buffers after remove, got %d", len(status.Buffers))
	}
}

func TestReplayManager_MarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.MarkIn("cam1")
	if err != nil {
		t.Fatalf("MarkIn: %v", err)
	}

	status := m.Status()
	if status.MarkIn == nil {
		t.Error("expected mark-in to be set")
	}
	if status.MarkSource != "cam1" {
		t.Errorf("expected mark source 'cam1', got %q", status.MarkSource)
	}
}

func TestReplayManager_MarkIn_UnknownSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.MarkIn("unknown")
	if err != ErrNoSource {
		t.Errorf("expected ErrNoSource, got %v", err)
	}
}

func TestReplayManager_MarkOut(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	err := m.MarkOut("cam1")
	if err != nil {
		t.Fatalf("MarkOut: %v", err)
	}

	status := m.Status()
	if status.MarkOut == nil {
		t.Error("expected mark-out to be set")
	}
}

func TestReplayManager_MarkOut_NoMarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.MarkOut("cam1")
	if err != ErrNoMarkIn {
		t.Errorf("expected ErrNoMarkIn, got %v", err)
	}
}

func TestReplayManager_Play_NoMarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.Play("cam1", 1.0, false)
	if err != ErrNoMarkIn {
		t.Errorf("expected ErrNoMarkIn, got %v", err)
	}
}

func TestReplayManager_Play_NoMarkOut(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.MarkIn("cam1")
	err := m.Play("cam1", 1.0, false)
	if err != ErrNoMarkOut {
		t.Errorf("expected ErrNoMarkOut, got %v", err)
	}
}

func TestReplayManager_Play_InvalidSpeed(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 2.0, false)
	if err != ErrInvalidSpeed {
		t.Errorf("expected ErrInvalidSpeed, got %v", err)
	}
}

func TestReplayManager_Play_Success(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	// Record some frames into the buffer.
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))

	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))

	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	status := m.Status()
	if status.State != PlayerPlaying && status.State != PlayerLoading {
		t.Errorf("expected playing or loading state, got %v", status.State)
	}

	// Wait for playback to complete.
	time.Sleep(1 * time.Second)
	status = m.Status()
	if status.State != PlayerIdle {
		t.Errorf("expected idle after playback, got %v", status.State)
	}
}

func TestReplayManager_Play_LoadingThenPlaying(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))

	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))

	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Immediately after Play(), state should be loading (may race to playing
	// if the player goroutine's OnReady fires before we check).
	status := m.Status()
	if status.State != PlayerLoading && status.State != PlayerPlaying {
		t.Errorf("expected loading or playing state immediately after Play(), got %v", status.State)
	}

	// Wait for playback to complete — should transition through playing to idle.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("playback did not complete within timeout")
		default:
			status = m.Status()
			if status.State == PlayerIdle {
				return // success
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestReplayManager_Play_AlreadyActive(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, true, 100))
	_ = m.MarkOut("cam1")

	_ = m.Play("cam1", 0.25, true) // Slow, looping — stays active

	time.Sleep(200 * time.Millisecond) // Give player time to start
	err := m.Play("cam1", 1.0, false)
	if err != ErrPlayerActive {
		t.Errorf("expected ErrPlayerActive, got %v", err)
	}

	_ = m.Stop()
	time.Sleep(100 * time.Millisecond)
}

func TestReplayManager_Stop(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Stop with no active player should return ErrNoPlayer.
	err := m.Stop()
	if err != ErrNoPlayer {
		t.Errorf("expected ErrNoPlayer, got %v", err)
	}
}

func TestReplayManager_OnStateChange(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	called := make(chan struct{}, 10)
	m.OnStateChange(func() {
		select {
		case called <- struct{}{}:
		default:
		}
	})

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))

	_ = m.MarkIn("cam1")
	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Error("OnStateChange not called for MarkIn")
	}
}

func TestReplayManager_Viewer(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	v := m.Viewer("cam1")
	if v == nil {
		t.Fatal("expected non-nil viewer")
	}
	if v.ID() != "replay:cam1" {
		t.Errorf("expected viewer ID 'replay:cam1', got %q", v.ID())
	}
}

func TestReplayManager_Viewer_UnknownSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	v := m.Viewer("unknown")
	if v != nil {
		t.Error("expected nil viewer for unknown source")
	}
}

func TestReplayManager_MarkOut_SourceMismatch(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.AddSource("cam2")
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	err := m.MarkOut("cam2")
	if err != ErrSourceMismatch {
		t.Errorf("expected ErrSourceMismatch, got %v", err)
	}
}

func TestReplayManager_AddSource_MaxSources(t *testing.T) {
	relay := &mockRelay{}
	cfg := Config{BufferDurationSecs: 60, MaxSources: 2}
	m := NewManager(relay, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	if err := m.AddSource("cam1"); err != nil {
		t.Fatalf("AddSource cam1: %v", err)
	}
	if err := m.AddSource("cam2"); err != nil {
		t.Fatalf("AddSource cam2: %v", err)
	}

	// Third source should be rejected.
	err := m.AddSource("cam3")
	if err != ErrMaxSources {
		t.Errorf("expected ErrMaxSources, got %v", err)
	}

	// Duplicate of existing source should still succeed.
	if err := m.AddSource("cam1"); err != nil {
		t.Errorf("duplicate AddSource should succeed, got %v", err)
	}
}

func TestReplayManager_Status_BuffersSorted(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Add sources in reverse order.
	_ = m.AddSource("cam3")
	_ = m.AddSource("cam1")
	_ = m.AddSource("cam2")

	status := m.Status()
	if len(status.Buffers) != 3 {
		t.Fatalf("expected 3 buffers, got %d", len(status.Buffers))
	}
	for i := 1; i < len(status.Buffers); i++ {
		if status.Buffers[i].Source < status.Buffers[i-1].Source {
			t.Errorf("buffers not sorted: %q comes after %q",
				status.Buffers[i].Source, status.Buffers[i-1].Source)
		}
	}
}

func TestReplayManager_PlayerProgress(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	// Record frames.
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))

	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))

	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Wait for playback to complete.
	time.Sleep(2 * time.Second)

	// After playback completes, player is nil so position resets to 0.
	status := m.Status()
	if status.State != PlayerIdle {
		t.Errorf("expected idle after playback, got %v", status.State)
	}
}

func TestReplayManager_PlaybackLifecycleCallbacks(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	startCalled := make(chan struct{}, 1)
	stopCalled := make(chan struct{}, 1)
	m.OnPlaybackLifecycle(
		func() { startCalled <- struct{}{} },
		func() { stopCalled <- struct{}{} },
	)

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Verify onStart callback is called.
	select {
	case <-startCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("onPlaybackStart not called within timeout")
	}

	// Wait for playback to finish — onStop should be called.
	select {
	case <-stopCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("onPlaybackStop not called within timeout")
	}
}

func TestReplayManager_PlaybackLifecycleCallbacks_ManualStop(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	startCalled := make(chan struct{}, 1)
	stopCalled := make(chan struct{}, 1)
	m.OnPlaybackLifecycle(
		func() { startCalled <- struct{}{} },
		func() { stopCalled <- struct{}{} },
	)

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	// Play with loop so it doesn't end naturally.
	err := m.Play("cam1", 0.25, true)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}

	// Wait for start callback.
	select {
	case <-startCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("onPlaybackStart not called within timeout")
	}

	// Manual stop should trigger onStop.
	_ = m.Stop()

	select {
	case <-stopCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("onPlaybackStop not called after manual Stop()")
	}
}

func makeVideoFrameAVC1(pts int64, keyframe bool, size int) *media.VideoFrame {
	f := makeVideoFrame(pts, keyframe, size)
	// Override wireData with valid AVC1 format.
	f.WireData = makeAVC1Data(size)
	return f
}
