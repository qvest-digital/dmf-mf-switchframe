package replay

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
	require.NotNil(t, m)
	defer m.Close()
}

func TestReplayManager_AddSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	status := m.Status()
	require.Len(t, status.Buffers, 1)
	require.Equal(t, "cam1", status.Buffers[0].Source)
}

func TestReplayManager_AddSource_Duplicate(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.AddSource("cam1") // Duplicate: should return nil, not create second buffer.

	status := m.Status()
	require.Len(t, status.Buffers, 1)
}

func TestReplayManager_RemoveSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	m.RemoveSource("cam1")

	status := m.Status()
	require.Empty(t, status.Buffers)
}

func TestReplayManager_MarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.MarkIn("cam1")
	require.NoError(t, err)

	status := m.Status()
	require.NotNil(t, status.MarkIn, "expected mark-in to be set")
	require.Equal(t, "cam1", status.MarkSource)
}

func TestReplayManager_MarkIn_UnknownSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.MarkIn("unknown")
	require.ErrorIs(t, err, ErrNoSource)
}

func TestReplayManager_MarkOut(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	err := m.MarkOut("cam1")
	require.NoError(t, err)

	status := m.Status()
	require.NotNil(t, status.MarkOut, "expected mark-out to be set")
}

func TestReplayManager_MarkOut_NoMarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.MarkOut("cam1")
	require.ErrorIs(t, err, ErrNoMarkIn)
}

func TestReplayManager_Play_NoMarkIn(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	err := m.Play("cam1", 1.0, false)
	require.ErrorIs(t, err, ErrNoMarkIn)
}

func TestReplayManager_Play_NoMarkOut(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	_ = m.MarkIn("cam1")
	err := m.Play("cam1", 1.0, false)
	require.ErrorIs(t, err, ErrNoMarkOut)
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
	require.ErrorIs(t, err, ErrInvalidSpeed)
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
	require.NoError(t, err)

	status := m.Status()
	require.True(t, status.State == PlayerPlaying || status.State == PlayerLoading,
		"expected playing or loading state, got %v", status.State)

	// Wait for playback to complete.
	time.Sleep(1 * time.Second)
	status = m.Status()
	require.Equal(t, PlayerIdle, status.State, "expected idle after playback")
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
	require.NoError(t, err)

	// Immediately after Play(), state should be loading (may race to playing
	// if the player goroutine's OnReady fires before we check).
	status := m.Status()
	require.True(t, status.State == PlayerLoading || status.State == PlayerPlaying,
		"expected loading or playing state immediately after Play(), got %v", status.State)

	// Wait for playback to complete — should transition through playing to idle.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond, "playback did not complete within timeout")
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
	require.ErrorIs(t, err, ErrPlayerActive)

	_ = m.Stop()
	time.Sleep(100 * time.Millisecond)
}

func TestReplayManager_Stop(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Stop with no active player should return ErrNoPlayer.
	err := m.Stop()
	require.ErrorIs(t, err, ErrNoPlayer)
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
	require.NotNil(t, v)
	require.Equal(t, "replay:cam1", v.ID())
}

func TestReplayManager_Viewer_UnknownSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	v := m.Viewer("unknown")
	require.Nil(t, v, "expected nil viewer for unknown source")
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
	require.ErrorIs(t, err, ErrSourceMismatch)
}

func TestReplayManager_AddSource_MaxSources(t *testing.T) {
	relay := &mockRelay{}
	cfg := Config{BufferDurationSecs: 60, MaxSources: 2}
	m := NewManager(relay, cfg, mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	require.NoError(t, m.AddSource("cam1"))
	require.NoError(t, m.AddSource("cam2"))

	// Third source should be rejected.
	err := m.AddSource("cam3")
	require.ErrorIs(t, err, ErrMaxSources)

	// Duplicate of existing source should still succeed.
	require.NoError(t, m.AddSource("cam1"))
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
	require.Len(t, status.Buffers, 3)
	for i := 1; i < len(status.Buffers); i++ {
		require.GreaterOrEqual(t, status.Buffers[i].Source, status.Buffers[i-1].Source,
			"buffers not sorted: %q comes after %q",
			status.Buffers[i].Source, status.Buffers[i-1].Source)
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
	require.NoError(t, err)

	// Wait for playback to complete.
	time.Sleep(2 * time.Second)

	// After playback completes, player is nil so position resets to 0.
	status := m.Status()
	require.Equal(t, PlayerIdle, status.State, "expected idle after playback")
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
	require.NoError(t, err)

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
	require.NoError(t, err)

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

func TestReplayManager_OnVideoInfoChange(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	var gotSPS, gotPPS []byte
	var gotW, gotH int
	infoCh := make(chan struct{}, 1)
	m.OnVideoInfoChange(func(sps, pps []byte, width, height int) {
		gotSPS = sps
		gotPPS = pps
		gotW = width
		gotH = height
		select {
		case infoCh <- struct{}{}:
		default:
		}
	})

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	require.NoError(t, err)

	select {
	case <-infoCh:
	case <-time.After(5 * time.Second):
		t.Fatal("OnVideoInfoChange callback not called during playback")
	}

	require.NotEmpty(t, gotSPS)
	require.NotEmpty(t, gotPPS)
	require.Equal(t, 320, gotW)
	require.Equal(t, 240, gotH)

	// Wait for playback to complete.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)
}

func TestReplayManager_SetPTSProvider(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	programPTS := int64(500_000)
	m.SetPTSProvider(func() int64 { return programPTS })

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	require.NoError(t, err)

	// Wait for playback to complete.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)

	// Verify output frames started from around programPTS (not from 0).
	relay.mu.Lock()
	defer relay.mu.Unlock()
	require.NotEmpty(t, relay.videos)
	require.Greater(t, relay.videos[0].PTS, programPTS-1,
		"first frame PTS should be >= programPTS, got %d", relay.videos[0].PTS)
}

func makeVideoFrameAVC1(pts int64, keyframe bool, size int) *media.VideoFrame {
	f := makeVideoFrame(pts, keyframe, size)
	// Override wireData with valid AVC1 format.
	f.WireData = makeAVC1Data(size)
	return f
}
