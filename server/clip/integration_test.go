package clip

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIntegrationManager creates a Manager with a synthetic DemuxFunc that
// returns the specified number of video and audio frames with realistic PTS
// values (30fps at 90kHz). Uses t.TempDir() for the store directory.
func newIntegrationManager(t *testing.T, numFrames int) (*Manager, *Store, *Clip) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: mockDemuxFunc(numFrames),
	})

	c := addTestClip(t, store, dir)
	return mgr, store, c
}

// waitForCallback waits for a channel signal with a timeout.
// Returns true if the signal was received, false on timeout.
func waitForCallback(ch <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// waitForCondition polls a condition function until it returns true or timeout.
func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// --- Integration Tests ---

func TestClipEndToEnd(t *testing.T) {
	mgr, _, c := newIntegrationManager(t, 30)
	defer mgr.Close()

	// Track raw video output with a channel for synchronization.
	var videoCount atomic.Int32
	videoReceived := make(chan struct{}, 1)
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		videoCount.Add(1)
		select {
		case videoReceived <- struct{}{}:
		default:
		}
	})

	// Track audio output.
	var audioCount atomic.Int32
	mgr.SetAudioOutput(func(key string, data []byte, pts int64, sampleRate, channels int) {
		audioCount.Add(1)
	})

	// Step 1: Load into player 1.
	require.NoError(t, mgr.Load(1, c.ID))
	states := mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State, "after Load: player 1 should be loaded")
	assert.Equal(t, c.ID, states[0].ClipID)
	assert.Equal(t, c.Name, states[0].ClipName)

	// Step 2: Play at slow speed with loop to keep playback alive.
	require.NoError(t, mgr.Play(1, 0.25, true))

	// Wait for at least one raw video frame callback.
	require.True(t, waitForCallback(videoReceived, 2*time.Second),
		"expected RawVideoOutput callback to fire during playback")
	assert.Greater(t, videoCount.Load(), int32(0), "should have received video frames")

	// Step 3: Pause and verify state.
	require.NoError(t, mgr.Pause(1))
	require.True(t, waitForCondition(time.Second, func() bool {
		return mgr.PlayerStates()[0].State == StatePaused
	}), "expected player 1 to be paused")

	// Record frame count at pause and verify no new frames arrive.
	countAtPause := videoCount.Load()
	time.Sleep(200 * time.Millisecond)
	countAfterPause := videoCount.Load()
	// Allow at most 1 frame in flight during pause transition.
	assert.InDelta(t, float64(countAtPause), float64(countAfterPause), 1,
		"no new frames should arrive while paused")

	// Step 4: Resume and verify more frames arrive.
	require.NoError(t, mgr.Resume(1))
	require.True(t, waitForCondition(time.Second, func() bool {
		return mgr.PlayerStates()[0].State == StatePlaying
	}), "expected player 1 to resume playing")

	// Wait for additional frames after resume.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return videoCount.Load() > countAfterPause+1
	}), "expected more video frames after resume")

	// Step 5: Stop -- clip stays loaded.
	require.NoError(t, mgr.Stop(1))
	states = mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State, "after Stop: clip should remain loaded")
	assert.Equal(t, c.ID, states[0].ClipID, "after Stop: clipID should persist")

	// Step 6: Eject -- slot becomes empty.
	require.NoError(t, mgr.Eject(1))
	states = mgr.PlayerStates()
	assert.Equal(t, StateEmpty, states[0].State, "after Eject: player should be empty")
	assert.Empty(t, states[0].ClipID, "after Eject: clipID should be cleared")
	assert.Empty(t, states[0].ClipName, "after Eject: clipName should be cleared")
}

func TestClipMultiPlayer(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)

	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: mockDemuxFunc(20),
	})
	defer mgr.Close()

	// Create two clips with distinct files.
	clipA := addTestClip(t, store, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "clip-b.ts"), []byte("dummy-b"), 0o644))
	clipB := &Clip{
		Name:       "Clip B",
		Filename:   "clip-b.ts",
		Source:     SourceUpload,
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		FPSNum:     30000,
		FPSDen:     1001,
		DurationMs: 3000,
		ByteSize:   7,
	}
	require.NoError(t, store.Add(clipB))

	// Track which player keys produce video output.
	keysSeen := &sync.Map{}
	var totalFrames atomic.Int32
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		keysSeen.Store(key, true)
		totalFrames.Add(1)
	})

	// Load clip A into player 1, clip B into player 3.
	require.NoError(t, mgr.Load(1, clipA.ID))
	require.NoError(t, mgr.Load(3, clipB.ID))

	states := mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State, "player 1 should be loaded")
	assert.Equal(t, StateEmpty, states[1].State, "player 2 should be empty")
	assert.Equal(t, StateLoaded, states[2].State, "player 3 should be loaded")
	assert.Equal(t, StateEmpty, states[3].State, "player 4 should be empty")

	// Play both players.
	require.NoError(t, mgr.Play(1, 0.25, true))
	require.NoError(t, mgr.Play(3, 0.25, true))

	// Wait for frames from both players.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		_, saw1 := keysSeen.Load("clip:1")
		_, saw3 := keysSeen.Load("clip:3")
		return saw1 && saw3
	}), "expected output from both clip:1 and clip:3")

	// Both should be playing.
	states = mgr.PlayerStates()
	assert.Equal(t, StatePlaying, states[0].State, "player 1 should be playing")
	assert.Equal(t, StatePlaying, states[2].State, "player 3 should be playing")

	// Stop both.
	require.NoError(t, mgr.Stop(1))
	require.NoError(t, mgr.Stop(3))

	states = mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State, "player 1 should be loaded after stop")
	assert.Equal(t, StateLoaded, states[2].State, "player 3 should be loaded after stop")

	// Total frames should be > 0 from both.
	assert.Greater(t, totalFrames.Load(), int32(0), "should have received frames")
}

func TestClipLifecycleCallbacks(t *testing.T) {
	mgr, _, c := newIntegrationManager(t, 20)
	defer mgr.Close()

	var (
		startCalls  atomic.Int32
		stopCalls   atomic.Int32
		startIDs    sync.Map
		startKeys   sync.Map
		stopIDs     sync.Map
		stopKeys    sync.Map
		stopSignal  = make(chan struct{}, 4)
		startSignal = make(chan struct{}, 4)
	)

	mgr.OnPlayerLifecycle(
		func(playerID int, key string) {
			startCalls.Add(1)
			startIDs.Store(playerID, true)
			startKeys.Store(key, true)
			select {
			case startSignal <- struct{}{}:
			default:
			}
		},
		func(playerID int, key string) {
			stopCalls.Add(1)
			stopIDs.Store(playerID, true)
			stopKeys.Store(key, true)
			select {
			case stopSignal <- struct{}{}:
			default:
			}
		},
	)

	// --- Test 1: Play triggers start callback ---
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))

	require.True(t, waitForCallback(startSignal, 2*time.Second),
		"expected start callback to fire on Play")
	assert.Equal(t, int32(1), startCalls.Load(), "start callback should fire once")
	_, sawID1 := startIDs.Load(1)
	assert.True(t, sawID1, "start callback should receive playerID=1")
	_, sawKey1 := startKeys.Load("clip:1")
	assert.True(t, sawKey1, "start callback should receive key='clip:1'")

	// --- Test 2: Stop triggers stop callback ---
	require.NoError(t, mgr.Stop(1))

	require.True(t, waitForCallback(stopSignal, 2*time.Second),
		"expected stop callback to fire on Stop")
	assert.Equal(t, int32(1), stopCalls.Load(), "stop callback should fire once for Stop")
	_, sawStopID1 := stopIDs.Load(1)
	assert.True(t, sawStopID1, "stop callback should receive playerID=1")
	_, sawStopKey1 := stopKeys.Load("clip:1")
	assert.True(t, sawStopKey1, "stop callback should receive key='clip:1'")

	// --- Test 3: Eject while playing triggers stop callback ---
	// Reset counters.
	startCalls.Store(0)
	stopCalls.Store(0)

	require.NoError(t, mgr.Play(1, 0.25, true))
	require.True(t, waitForCallback(startSignal, 2*time.Second),
		"expected start callback on second Play")

	require.NoError(t, mgr.Eject(1))
	require.True(t, waitForCallback(stopSignal, 2*time.Second),
		"expected stop callback to fire on Eject while playing")
	assert.Equal(t, int32(1), stopCalls.Load(), "stop callback should fire for Eject")
}

func TestClipSeekDuringPlayback(t *testing.T) {
	// Use more frames so we can observe progress changes.
	mgr, _, c := newIntegrationManager(t, 60)
	defer mgr.Close()

	var videoCount atomic.Int32
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		videoCount.Add(1)
	})

	require.NoError(t, mgr.Load(1, c.ID))
	// Play at slow speed to keep playback alive for the seek.
	require.NoError(t, mgr.Play(1, 0.25, true))

	// Wait for playback to start producing frames.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return videoCount.Load() > 2
	}), "expected initial playback frames")

	// Seek to 50% position.
	require.NoError(t, mgr.Seek(1, 0.5))

	// Wait a bit for seek to take effect.
	time.Sleep(200 * time.Millisecond)

	// Verify playback continues from new position.
	states := mgr.PlayerStates()
	assert.Equal(t, StatePlaying, states[0].State,
		"expected playing state after seek")
	assert.GreaterOrEqual(t, states[0].Position, 0.3,
		"progress after seeking to 0.5 should be at least 0.3 (accounting for timing)")

	// Verify more frames continue to arrive after seek.
	countAfterSeek := videoCount.Load()
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return videoCount.Load() > countAfterSeek+2
	}), "expected playback to continue producing frames after seek")

	require.NoError(t, mgr.Stop(1))
}

func TestClipSpeedChange(t *testing.T) {
	// Use enough frames to observe speed differences.
	mgr, _, c := newIntegrationManager(t, 30)
	defer mgr.Close()

	var videoCount atomic.Int32
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		videoCount.Add(1)
	})

	// Play at 0.5x -- dupCount should double output frames.
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 0.5, true))

	// Wait for some frames to arrive.
	require.True(t, waitForCondition(2*time.Second, func() bool {
		return videoCount.Load() > 5
	}), "expected frames at 0.5x speed")

	states := mgr.PlayerStates()
	assert.Equal(t, StatePlaying, states[0].State)
	assert.InDelta(t, 0.5, states[0].Speed, 0.01, "speed should be 0.5")

	require.NoError(t, mgr.Stop(1))
}

func TestClipStateChangeNotifications(t *testing.T) {
	mgr, _, c := newIntegrationManager(t, 20)
	defer mgr.Close()

	var stateChanges atomic.Int32
	mgr.SetOnStateChange(func() {
		stateChanges.Add(1)
	})

	// Load should trigger state change.
	require.NoError(t, mgr.Load(1, c.ID))
	time.Sleep(50 * time.Millisecond)
	loadChanges := stateChanges.Load()
	assert.Greater(t, loadChanges, int32(0), "Load should trigger state change")

	// Play should trigger state change.
	require.NoError(t, mgr.Play(1, 0.25, true))
	time.Sleep(100 * time.Millisecond)
	playChanges := stateChanges.Load()
	assert.Greater(t, playChanges, loadChanges, "Play should trigger state change")

	// Pause should trigger state change.
	require.NoError(t, mgr.Pause(1))
	time.Sleep(100 * time.Millisecond)
	pauseChanges := stateChanges.Load()
	assert.Greater(t, pauseChanges, playChanges, "Pause should trigger state change")

	// Resume should trigger state change.
	require.NoError(t, mgr.Resume(1))
	time.Sleep(100 * time.Millisecond)
	resumeChanges := stateChanges.Load()
	assert.Greater(t, resumeChanges, pauseChanges, "Resume should trigger state change")

	// Stop should trigger state change.
	require.NoError(t, mgr.Stop(1))
	time.Sleep(100 * time.Millisecond)
	stopChanges := stateChanges.Load()
	assert.Greater(t, stopChanges, resumeChanges, "Stop should trigger state change")

	// Eject should trigger state change.
	require.NoError(t, mgr.Eject(1))
	time.Sleep(100 * time.Millisecond)
	ejectChanges := stateChanges.Load()
	assert.Greater(t, ejectChanges, stopChanges, "Eject should trigger state change")
}

func TestClipPTSProviderAnchors(t *testing.T) {
	mgr, _, c := newIntegrationManager(t, 10)
	defer mgr.Close()

	// Set PTS provider to return a known program PTS.
	const programPTS int64 = 270000 // 3 seconds at 90kHz
	var called atomic.Bool
	mgr.SetPTSProvider(func() int64 {
		called.Store(true)
		return programPTS
	})

	var firstPTS atomic.Int64
	firstPTS.Store(-1)
	mgr.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64) {
		firstPTS.CompareAndSwap(-1, pts)
	})

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))

	require.True(t, waitForCondition(2*time.Second, func() bool {
		return firstPTS.Load() >= 0
	}), "expected at least one frame output")

	assert.True(t, called.Load(), "PTS provider should be called during Play")
	// The initial PTS should be anchored to the program PTS (programPTS + one frame ahead).
	expectedInitialPTS := programPTS + 3003
	assert.Equal(t, expectedInitialPTS, firstPTS.Load(),
		"first output PTS should be anchored to program timeline")

	require.NoError(t, mgr.Stop(1))
}

func TestClipStoreIntegrationWithManager(t *testing.T) {
	mgr, store, c := newIntegrationManager(t, 10)
	defer mgr.Close()

	// Verify clip is in the store.
	listed := store.List()
	require.Len(t, listed, 1)
	assert.Equal(t, c.ID, listed[0].ID)

	// Load and verify LoadedClipIDs tracks it.
	require.NoError(t, mgr.Load(1, c.ID))
	ids := mgr.LoadedClipIDs()
	assert.True(t, ids[c.ID], "loaded clip should appear in LoadedClipIDs")

	// Eject and verify LoadedClipIDs no longer includes it.
	require.NoError(t, mgr.Eject(1))
	ids = mgr.LoadedClipIDs()
	assert.False(t, ids[c.ID], "ejected clip should not appear in LoadedClipIDs")

	// Delete from store should succeed (not loaded).
	require.NoError(t, store.Delete(c.ID))
	listed = store.List()
	assert.Len(t, listed, 0)

	// Loading deleted clip should fail.
	err := mgr.Load(1, c.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestClipCloseStopsAllPlayers(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: mockDemuxFunc(30),
	})

	c := addTestClip(t, store, dir)

	// Load and play on two slots.
	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Load(3, c.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))
	require.NoError(t, mgr.Play(3, 0.25, true))
	time.Sleep(100 * time.Millisecond)

	// Close should stop all players without hanging.
	done := make(chan struct{})
	go func() {
		mgr.Close()
		close(done)
	}()

	require.True(t, waitForCallback(done, 5*time.Second),
		"Close should complete within timeout")

	// After close, no player should be in playing state.
	states := mgr.PlayerStates()
	for i, s := range states {
		assert.NotEqual(t, StatePlaying, s.State,
			"player %d should not be playing after Close", i+1)
	}
}
