package replay

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
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

func TestReplayManager_RawVideoOutput(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	var rawCount int64
	m.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64) {
		atomic.AddInt64(&rawCount, 1)
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

	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)

	require.Greater(t, atomic.LoadInt64(&rawCount), int64(0),
		"RawVideoOutput should receive frames during playback")
}

func TestReplayManager_AudioOutput(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	var directAudioCount int64
	m.SetAudioOutput(func(frame *media.AudioFrame) {
		atomic.AddInt64(&directAudioCount, 1)
	})

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	// Record audio frames within the mark window (between mark-in and mark-out).
	buf := m.buffers["cam1"]
	buf.RecordAudioFrame(&media.AudioFrame{PTS: 3003, Data: make([]byte, 50), SampleRate: 48000, Channels: 2})
	buf.RecordAudioFrame(&media.AudioFrame{PTS: 4923, Data: make([]byte, 50), SampleRate: 48000, Channels: 2})

	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, false, 50))
	time.Sleep(10 * time.Millisecond)
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)

	// Audio goes to both relay AND direct output.
	count := atomic.LoadInt64(&directAudioCount)
	require.Greater(t, count, int64(0),
		"AudioOutput should receive frames during playback")
}

func TestReplayManager_SetAudioCodecFactories(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	m.SetAudioCodecFactories(
		func(sampleRate, channels int) (audio.Decoder, error) {
			return &mockAudioDecoder{}, nil
		},
		func(sampleRate, channels int) (audio.Encoder, error) {
			return &mockAudioEncoder{}, nil
		},
	)

	// Verify factories are set (they'll be wired into PlayerConfig on Play).
	m.mu.Lock()
	require.NotNil(t, m.audioDecoderFactory)
	require.NotNil(t, m.audioEncoderFactory)
	m.mu.Unlock()
}

func TestReplayManager_OnClipExported(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Track callback invocations.
	type exportEvent struct {
		source   string
		filePath string
	}
	exportCh := make(chan exportEvent, 1)
	m.SetOnClipExported(func(source string, filePath string) {
		exportCh <- exportEvent{source: source, filePath: filePath}
	})

	_ = m.AddSource("cam1")

	// Record frames so ExtractClip has data.
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)

	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 1.0, false)
	require.NoError(t, err)

	// The callback should be invoked with the source and a temp TS file path.
	select {
	case ev := <-exportCh:
		require.Equal(t, "cam1", ev.source)
		require.NotEmpty(t, ev.filePath)

		// Verify the temp file exists and has content.
		info, statErr := os.Stat(ev.filePath)
		require.NoError(t, statErr, "temp TS file should exist")
		require.Greater(t, info.Size(), int64(0), "temp TS file should have content")

		// Clean up the temp file.
		_ = os.Remove(ev.filePath)
	case <-time.After(5 * time.Second):
		t.Fatal("OnClipExported callback not called within timeout")
	}

	// Wait for playback to complete.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)
}

func TestReplayManager_OnClipExported_NilCallback(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Don't set callback — Play should still work without panicking.
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

	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)
}

func TestReplayManager_CloseWaitsForExportGoroutine(t *testing.T) {
	// Regression test: Manager.Close() must wait for background export
	// goroutines to finish before returning.
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)

	var exportFinished atomic.Int32
	m.SetOnClipExported(func(source string, filePath string) {
		// Simulate slow export work.
		time.Sleep(200 * time.Millisecond)
		exportFinished.Add(1)
		_ = os.Remove(filePath)
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

	// Wait for playback to complete so the export goroutine has time to start.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerIdle
	}, 5*time.Second, 50*time.Millisecond)

	// Close should block until the export goroutine finishes.
	m.Close()

	// After Close returns, the export callback must have completed.
	require.Equal(t, int32(1), exportFinished.Load(),
		"export goroutine should have completed before Close() returned")
}

// setupPlayingManager creates a manager with an active player for testing
// pause/resume/seek/speed methods. The player is looping so it stays active.
func setupPlayingManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))
	_ = m.MarkOut("cam1")

	err := m.Play("cam1", 0.5, true) // slow + loop = stays active
	require.NoError(t, err)

	// Wait for player to reach playing state.
	require.Eventually(t, func() bool {
		return m.Status().State == PlayerPlaying
	}, 3*time.Second, 10*time.Millisecond, "player did not reach playing state")

	cleanup := func() {
		_ = m.Stop()
		m.Close()
	}
	return m, cleanup
}

func TestManager_Pause_NotPlaying(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// No player at all.
	err := m.Pause()
	require.ErrorIs(t, err, ErrNotPlaying)
}

func TestManager_Pause_Success(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	err := m.Pause()
	require.NoError(t, err)

	status := m.Status()
	require.Equal(t, PlayerPaused, status.State)
}

func TestManager_Resume_NotPaused(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// No player at all.
	err := m.Resume()
	require.ErrorIs(t, err, ErrNotPaused)
}

func TestManager_Resume_NotPaused_WhilePlaying(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	// Player is playing, not paused.
	err := m.Resume()
	require.ErrorIs(t, err, ErrNotPaused)
}

func TestManager_Resume_Success(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	// Pause first.
	err := m.Pause()
	require.NoError(t, err)
	require.Equal(t, PlayerPaused, m.Status().State)

	// Resume.
	err = m.Resume()
	require.NoError(t, err)
	require.Equal(t, PlayerPlaying, m.Status().State)
}

func TestManager_SetSpeed_NoPlayer(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.SetSpeed(0.5)
	require.ErrorIs(t, err, ErrNoPlayer)
}

func TestManager_SetSpeed_InvalidSpeed(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	err := m.SetSpeed(0.1)
	require.ErrorIs(t, err, ErrInvalidSpeed)

	err = m.SetSpeed(2.0)
	require.ErrorIs(t, err, ErrInvalidSpeed)
}

func TestManager_SetSpeed_Success(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	stateChanged := make(chan struct{}, 10)
	m.OnStateChange(func() {
		select {
		case stateChanged <- struct{}{}:
		default:
		}
	})

	err := m.SetSpeed(0.75)
	require.NoError(t, err)

	status := m.Status()
	require.Equal(t, 0.75, status.Speed)

	// Verify state change was notified.
	select {
	case <-stateChanged:
	case <-time.After(1 * time.Second):
		t.Error("OnStateChange not called for SetSpeed")
	}
}

func TestManager_Seek_NoPlayer(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.Seek(0.5)
	require.ErrorIs(t, err, ErrNoPlayer)
}

func TestManager_Seek_InvalidPosition(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	err := m.Seek(-0.1)
	require.ErrorIs(t, err, ErrInvalidSeek)

	err = m.Seek(1.5)
	require.ErrorIs(t, err, ErrInvalidSeek)
}

func TestManager_Seek_Success(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	err := m.Seek(0.5)
	require.NoError(t, err)

	// Seek at boundaries.
	err = m.Seek(0.0)
	require.NoError(t, err)

	err = m.Seek(1.0)
	require.NoError(t, err)
}

func TestManager_SetMarks(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()
	_ = m.AddSource("cam1")

	// Set initial marks via MarkIn/MarkOut.
	_ = m.MarkIn("cam1")
	time.Sleep(10 * time.Millisecond)
	_ = m.MarkOut("cam1")

	// Now adjust via SetMarks.
	inMs := time.Now().Add(-30 * time.Second).UnixMilli()
	outMs := time.Now().Add(-5 * time.Second).UnixMilli()
	err := m.SetMarks(&inMs, &outMs)
	require.NoError(t, err)

	status := m.Status()
	require.NotNil(t, status.MarkIn)
	require.NotNil(t, status.MarkOut)
	require.Equal(t, inMs, status.MarkIn.UnixMilli())
	require.Equal(t, outMs, status.MarkOut.UnixMilli())
}

func TestManager_SetMarks_InvalidOrder(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Set markOut before markIn.
	inMs := time.Now().UnixMilli()
	outMs := time.Now().Add(-10 * time.Second).UnixMilli() // Before markIn
	err := m.SetMarks(&inMs, &outMs)
	require.ErrorIs(t, err, ErrInvalidMarks)
}

func TestManager_SetMarks_PartialUpdate(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	// Set only markIn.
	inMs := time.Now().Add(-30 * time.Second).UnixMilli()
	err := m.SetMarks(&inMs, nil)
	require.NoError(t, err)

	status := m.Status()
	require.NotNil(t, status.MarkIn)
	require.Nil(t, status.MarkOut)

	// Now set only markOut.
	outMs := time.Now().UnixMilli()
	err = m.SetMarks(nil, &outMs)
	require.NoError(t, err)

	status = m.Status()
	require.NotNil(t, status.MarkIn)
	require.NotNil(t, status.MarkOut)
}

func TestManager_QuickReplay_NoSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	err := m.QuickReplay("nonexistent", 10, 0.5)
	require.ErrorIs(t, err, ErrNoSource)
}

func TestManager_QuickReplay_DefaultSpeed(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")
	m.RecordFrame("cam1", makeVideoFrameAVC1(0, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(3003, false, 50))
	time.Sleep(10 * time.Millisecond)
	m.RecordFrame("cam1", makeVideoFrameAVC1(6006, true, 100))
	m.RecordFrame("cam1", makeVideoFrameAVC1(9009, false, 50))

	err := m.QuickReplay("cam1", 10, 0) // 0 = default to 0.5
	require.NoError(t, err)

	status := m.Status()
	require.Equal(t, 0.5, status.Speed)
	require.NotNil(t, status.MarkIn)
	require.NotNil(t, status.MarkOut)
	require.Equal(t, "cam1", status.MarkSource)

	// Wait for completion or stop.
	_ = m.Stop()
}

func TestManager_QuickReplay_StopsActivePlayer(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	// Player is already active and looping. QuickReplay should stop it.
	err := m.QuickReplay("cam1", 10, 0.5)
	require.NoError(t, err)

	status := m.Status()
	require.True(t, status.State == PlayerPlaying || status.State == PlayerLoading,
		"expected playing or loading after QuickReplay, got %v", status.State)
}

func TestManager_PeekFrame_NoSource(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_, err := m.PeekFrame("nonexistent")
	require.ErrorIs(t, err, ErrNoSource)
}

func TestManager_PeekFrame_NoFrames(t *testing.T) {
	relay := &mockRelay{}
	m := NewManager(relay, DefaultConfig(), mockDecoderFactory, mockEncoderFactory)
	defer m.Close()

	_ = m.AddSource("cam1")

	data, err := m.PeekFrame("cam1")
	require.NoError(t, err)
	require.Nil(t, data, "expected nil data (no thumbnail available)")
}

func TestManager_Pause_StateChangeNotified(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	stateChanged := make(chan struct{}, 10)
	m.OnStateChange(func() {
		select {
		case stateChanged <- struct{}{}:
		default:
		}
	})

	err := m.Pause()
	require.NoError(t, err)

	select {
	case <-stateChanged:
	case <-time.After(1 * time.Second):
		t.Error("OnStateChange not called for Pause")
	}
}

func TestManager_Resume_StateChangeNotified(t *testing.T) {
	m, cleanup := setupPlayingManager(t)
	defer cleanup()

	_ = m.Pause()

	stateChanged := make(chan struct{}, 10)
	m.OnStateChange(func() {
		select {
		case stateChanged <- struct{}{}:
		default:
		}
	})

	err := m.Resume()
	require.NoError(t, err)

	select {
	case <-stateChanged:
	case <-time.After(1 * time.Second):
		t.Error("OnStateChange not called for Resume")
	}
}

func makeVideoFrameAVC1(pts int64, keyframe bool, size int) *media.VideoFrame {
	f := makeVideoFrame(pts, keyframe, size)
	// Override wireData with valid AVC1 format.
	f.WireData = makeAVC1Data(size)
	return f
}
