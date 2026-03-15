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

// mockDemuxFunc returns a DemuxFunc that returns synthetic frames.
// It avoids needing real TS/MP4 files for manager tests.
func mockDemuxFunc(numFrames int) func(string) ([]bufferedFrame, []bufferedAudioFrame, error) {
	return func(path string) ([]bufferedFrame, []bufferedAudioFrame, error) {
		frames := make([]bufferedFrame, numFrames)
		for i := range frames {
			frames[i] = bufferedFrame{
				wireData:   make([]byte, 100),
				pts:        int64(i) * 3000,
				isKeyframe: i == 0,
			}
			if i == 0 {
				frames[i].sps = []byte{0x67, 0x42, 0xC0, 0x1E}
				frames[i].pps = []byte{0x68, 0xCE, 0x38, 0x80}
			}
		}
		audio := make([]bufferedAudioFrame, numFrames/3+1)
		for i := range audio {
			audio[i] = bufferedAudioFrame{
				data:       make([]byte, 50),
				pts:        int64(i) * 9000,
				sampleRate: 48000,
				channels:   2,
			}
		}
		return frames, audio, nil
	}
}

// addTestClip creates a minimal clip entry in the store with a dummy file.
func addTestClip(t *testing.T, store *Store, dir string) *Clip {
	t.Helper()
	// Create a dummy media file in the store directory.
	fname := "test-clip.ts"
	fpath := filepath.Join(dir, fname)
	require.NoError(t, os.WriteFile(fpath, []byte("dummy"), 0o644))

	c := &Clip{
		Name:       "Test Clip",
		Filename:   fname,
		Source:     SourceUpload,
		Codec:      "h264",
		Width:      1920,
		Height:     1080,
		FPSNum:     30000,
		FPSDen:     1001,
		DurationMs: 5000,
		ByteSize:   5,
	}
	require.NoError(t, store.Add(c))
	return c
}

func newTestManager(t *testing.T) (*Manager, *Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc: mockDemuxFunc(30),
	})
	return mgr, store, dir
}

func TestManagerPlayerStatesDefault(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	states := mgr.PlayerStates()
	require.Len(t, states, MaxPlayers)
	for i, s := range states {
		assert.Equal(t, i+1, s.ID)
		assert.Equal(t, StateEmpty, s.State)
		assert.Empty(t, s.ClipID)
		assert.Empty(t, s.ClipName)
	}
}

func TestManagerInvalidPlayerID(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	assert.ErrorIs(t, mgr.Load(0, clip.ID), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Load(5, clip.ID), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Load(-1, clip.ID), ErrInvalidPlayer)

	assert.ErrorIs(t, mgr.Eject(0), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Play(0, 1.0, false), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Pause(0), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Resume(0), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Stop(0), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.Seek(0, 0.5), ErrInvalidPlayer)
}

func TestManagerLoadNonexistentClip(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	err := mgr.Load(1, "nonexistent-id")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestManagerLoadAndEject(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	// Load into player 1.
	require.NoError(t, mgr.Load(1, clip.ID))

	states := mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State)
	assert.Equal(t, clip.ID, states[0].ClipID)
	assert.Equal(t, clip.Name, states[0].ClipName)

	// Eject player 1.
	require.NoError(t, mgr.Eject(1))

	states = mgr.PlayerStates()
	assert.Equal(t, StateEmpty, states[0].State)
	assert.Empty(t, states[0].ClipID)
}

func TestManagerLoadReplacesExisting(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()

	clip1 := addTestClip(t, store, dir)

	// Create a second clip.
	fname2 := "test-clip2.ts"
	require.NoError(t, os.WriteFile(filepath.Join(dir, fname2), []byte("dummy2"), 0o644))
	clip2 := &Clip{
		Name:     "Clip Two",
		Filename: fname2,
		Source:   SourceUpload,
		Codec:    "h264",
		Width:    1920,
		Height:   1080,
		ByteSize: 6,
	}
	require.NoError(t, store.Add(clip2))

	// Load clip1 into player 1.
	require.NoError(t, mgr.Load(1, clip1.ID))
	assert.Equal(t, clip1.ID, mgr.PlayerStates()[0].ClipID)

	// Load clip2 into same player — should auto-eject clip1.
	require.NoError(t, mgr.Load(1, clip2.ID))
	assert.Equal(t, clip2.ID, mgr.PlayerStates()[0].ClipID)
	assert.Equal(t, clip2.Name, mgr.PlayerStates()[0].ClipName)
}

func TestManagerPlay(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 1.0, false))

	// Give the player a moment to start.
	time.Sleep(50 * time.Millisecond)

	states := mgr.PlayerStates()
	state := states[0].State
	// Player should be playing or holding (short clips finish fast).
	assert.True(t, state == StatePlaying || state == StateHolding || state == StateLoaded,
		"unexpected state: %s", state)
}

func TestManagerPlayInvalidSpeed(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))

	assert.ErrorIs(t, mgr.Play(1, 0.1, false), ErrInvalidSpeed)
	assert.ErrorIs(t, mgr.Play(1, 3.0, false), ErrInvalidSpeed)
}

func TestManagerPlayEmptySlot(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	assert.ErrorIs(t, mgr.Play(1, 1.0, false), ErrPlayerEmpty)
}

func TestManagerPause(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true)) // slow + loop = stays playing

	// Wait for play to start.
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.Pause(1))

	// Give pause a moment.
	time.Sleep(50 * time.Millisecond)

	states := mgr.PlayerStates()
	assert.Equal(t, StatePaused, states[0].State)
}

func TestManagerPauseEmptySlot(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	assert.ErrorIs(t, mgr.Pause(1), ErrPlayerEmpty)
}

func TestManagerResume(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.Pause(1))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, StatePaused, mgr.PlayerStates()[0].State)

	require.NoError(t, mgr.Resume(1))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, StatePlaying, mgr.PlayerStates()[0].State)
}

func TestManagerPlayResumesIfPaused(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.Pause(1))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, StatePaused, mgr.PlayerStates()[0].State)

	// Play while paused should resume.
	require.NoError(t, mgr.Play(1, 0.25, true))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, StatePlaying, mgr.PlayerStates()[0].State)
}

func TestManagerStop(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.Stop(1))

	// Stop should keep clip loaded (not eject).
	states := mgr.PlayerStates()
	assert.Equal(t, StateLoaded, states[0].State)
	assert.Equal(t, clip.ID, states[0].ClipID)
}

func TestManagerStopEmptySlot(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	assert.ErrorIs(t, mgr.Stop(1), ErrPlayerEmpty)
}

func TestManagerSeek(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))
	time.Sleep(100 * time.Millisecond)

	err := mgr.Seek(1, 0.5)
	assert.NoError(t, err)
}

func TestManagerSeekNoPlayer(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	// Not playing, so seek should error.
	assert.ErrorIs(t, mgr.Seek(1, 0.5), ErrPlayerEmpty)
}

func TestManagerSetSpeed(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 1.0, false))
	time.Sleep(50 * time.Millisecond)

	// Valid speed change.
	assert.NoError(t, mgr.SetSpeed(1, 0.5))

	// Check state reflects new speed.
	states := mgr.PlayerStates()
	assert.Equal(t, 0.5, states[0].Speed)

	// Invalid speed.
	assert.ErrorIs(t, mgr.SetSpeed(1, 3.0), ErrInvalidSpeed)
	assert.ErrorIs(t, mgr.SetSpeed(1, 0.1), ErrInvalidSpeed)

	// Invalid player.
	assert.ErrorIs(t, mgr.SetSpeed(0, 1.0), ErrInvalidPlayer)
	assert.ErrorIs(t, mgr.SetSpeed(5, 1.0), ErrInvalidPlayer)
}

func TestManagerSetSpeedNoPlayer(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	// Not playing, so SetSpeed should error.
	assert.ErrorIs(t, mgr.SetSpeed(1, 0.5), ErrPlayerEmpty)
}

func TestManagerSetLoop(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))

	// SetLoop works on loaded (not playing) slot too.
	assert.NoError(t, mgr.SetLoop(1, true))
	states := mgr.PlayerStates()
	assert.True(t, states[0].Loop)

	assert.NoError(t, mgr.SetLoop(1, false))
	states = mgr.PlayerStates()
	assert.False(t, states[0].Loop)

	// Invalid player.
	assert.ErrorIs(t, mgr.SetLoop(0, true), ErrInvalidPlayer)

	// Empty slot.
	assert.ErrorIs(t, mgr.SetLoop(2, true), ErrPlayerEmpty)
}

func TestManagerLoadedClipIDs(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()

	clip1 := addTestClip(t, store, dir)

	fname2 := "test-clip2.ts"
	require.NoError(t, os.WriteFile(filepath.Join(dir, fname2), []byte("dummy2"), 0o644))
	clip2 := &Clip{
		Name:     "Clip Two",
		Filename: fname2,
		Source:   SourceUpload,
		Codec:    "h264",
		Width:    1920,
		Height:   1080,
		ByteSize: 6,
	}
	require.NoError(t, store.Add(clip2))

	require.NoError(t, mgr.Load(1, clip1.ID))
	require.NoError(t, mgr.Load(3, clip2.ID))

	ids := mgr.LoadedClipIDs()
	assert.True(t, ids[clip1.ID])
	assert.True(t, ids[clip2.ID])
	assert.Len(t, ids, 2)
}

func TestManagerEjectWhilePlaying(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	var stopCalled atomic.Bool
	mgr.OnPlayerLifecycle(
		func(id int, key string) {},
		func(id int, key string) { stopCalled.Store(true) },
	)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, mgr.Eject(1))

	states := mgr.PlayerStates()
	assert.Equal(t, StateEmpty, states[0].State)

	// onPlayerStop should have been called.
	assert.True(t, stopCalled.Load(), "expected onPlayerStop to be called")
}

func TestManagerLifecycleCallbacks(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	var (
		startCalled atomic.Bool
		stopCalled  atomic.Bool
		startID     atomic.Int32
		stopID      atomic.Int32
		startKey    atomic.Value
		stopKey     atomic.Value
	)
	mgr.OnPlayerLifecycle(
		func(id int, key string) {
			startCalled.Store(true)
			startID.Store(int32(id))
			startKey.Store(key)
		},
		func(id int, key string) {
			stopCalled.Store(true)
			stopID.Store(int32(id))
			stopKey.Store(key)
		},
	)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 1.0, false))

	// Wait for play to start and then stop naturally (short clip).
	time.Sleep(500 * time.Millisecond)

	// Verify start callback was called.
	assert.True(t, startCalled.Load(), "expected onPlayerStart to be called")
	assert.Equal(t, int32(1), startID.Load())
	assert.Equal(t, "clip:1", startKey.Load())
}

func TestManagerSetOnStateChange(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	var stateChanges atomic.Int32
	mgr.SetOnStateChange(func() {
		stateChanges.Add(1)
	})

	require.NoError(t, mgr.Load(1, clip.ID))

	// Load should trigger a state change notification.
	time.Sleep(50 * time.Millisecond)
	assert.Greater(t, stateChanges.Load(), int32(0), "expected state change notification on load")
}

func TestManagerSetPTSProvider(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	var called atomic.Bool
	mgr.SetPTSProvider(func() int64 {
		called.Store(true)
		return 90000
	})

	// Just verify it doesn't panic; PTS provider is called during Play.
	assert.False(t, called.Load())
}

func TestManagerClose(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, true))
	time.Sleep(100 * time.Millisecond)

	// Close should not panic and should stop all players.
	mgr.Close()

	states := mgr.PlayerStates()
	// After close, players should be stopped.
	for _, s := range states {
		assert.NotEqual(t, StatePlaying, s.State)
	}
}

func TestManagerConcurrentAccess(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.PlayerStates()
			_ = mgr.LoadedClipIDs()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = mgr.Load(1, clip.ID)
	}()

	wg.Wait()
}

func TestManagerSetLoopPropagatesToActivePlayer(t *testing.T) {
	// Bug: Manager.SetLoop sets slot.loop but doesn't propagate to the active
	// Player, which reads p.config.Loop at creation time. The player continues
	// using the original loop value and ignores the manager's update.
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	clip := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, clip.ID))
	require.NoError(t, mgr.Play(1, 0.25, false)) // start without loop

	// Wait for player to start.
	time.Sleep(100 * time.Millisecond)

	// Change loop setting while playing.
	require.NoError(t, mgr.SetLoop(1, true))

	// Verify the player's internal config was updated.
	mgr.mu.Lock()
	slot := mgr.players[0]
	require.NotNil(t, slot)
	require.NotNil(t, slot.player, "player should be active")
	assert.True(t, slot.player.loop.Load(),
		"player.loop should be true after Manager.SetLoop(true)")
	mgr.mu.Unlock()

	// Clean up.
	_ = mgr.Stop(1)
}

func TestManagerEjectEmptySlot(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	// Ejecting an already-empty slot should not error.
	assert.NoError(t, mgr.Eject(1))
}
