package clip

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDemuxFunc returns a DemuxFunc that returns synthetic frames.
// It avoids needing real TS/MP4 files for manager tests.
func mockDemuxFunc(numFrames int) func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
	return func(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
		frames := make([]BufferedFrame, numFrames)
		for i := range frames {
			frames[i] = BufferedFrame{
				wireData:   make([]byte, 100),
				pts:        int64(i) * 3000,
				isKeyframe: i == 0,
			}
			if i == 0 {
				frames[i].sps = []byte{0x67, 0x42, 0xC0, 0x1E}
				frames[i].pps = []byte{0x68, 0xCE, 0x38, 0x80}
			}
		}
		audio := make([]BufferedAudioFrame, numFrames/3+1)
		for i := range audio {
			audio[i] = BufferedAudioFrame{
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

// TestClipManager_ConcurrentLoadSameSlotNoLeak exercises the TOCTOU window
// between demux (outside m.mu) and the post-demux eject-and-replace (under
// m.mu in Load). Two assertions:
//
//  1. Demux on the same slot serializes — only one DemuxFunc runs at a time
//     for a given playerID. This is the deterministic RED/GREEN signal: on
//     current (buggy) main, N concurrent Loads all demux in parallel; with
//     the per-slot load mutex fix, they run strictly sequentially.
//  2. No orphan player goroutine after cleanup. On current main, a Play
//     racing into Load A's eject window can spawn a player on an
//     intermediate slot that Load A then overwrites unconditionally at
//     m.players[idx] = &playerSlot{...}, leaving a running goroutine no
//     Stop/Eject/Close can reach.
func TestClipManager_ConcurrentLoadSameSlotNoLeak(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)

	clips := make([]*Clip, 6)
	for i := range clips {
		fname := "clip-" + string(rune('a'+i)) + ".ts"
		require.NoError(t, os.WriteFile(filepath.Join(dir, fname), []byte("dummy"), 0o644))
		c := &Clip{
			Name:       "Clip " + string(rune('A'+i)),
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
		clips[i] = c
	}

	// Concurrency-counting demux: each in-flight DemuxFunc call bumps a
	// counter; the max observed is asserted == 1 under the fix.
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	slowDemux := func(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
		n := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if n <= prev {
				break
			}
			if maxInFlight.CompareAndSwap(prev, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		frames := make([]BufferedFrame, 30)
		for i := range frames {
			frames[i] = BufferedFrame{
				wireData:   make([]byte, 100),
				pts:        int64(i) * 3000,
				isKeyframe: i == 0,
			}
			if i == 0 {
				frames[i].sps = []byte{0x67, 0x42, 0xC0, 0x1E}
				frames[i].pps = []byte{0x68, 0xCE, 0x38, 0x80}
			}
		}
		return frames, nil, nil
	}

	mgr := NewManager(store, ManagerConfig{DemuxFunc: slowDemux})
	defer mgr.Close()

	runtime.GC()
	baseline := runtime.NumGoroutine()

	// Pre-Load + Play so the first storm Load enters the eject path on a live
	// player. Loop=true keeps any orphan goroutine spinning so it remains
	// visible to NumGoroutine at test end.
	require.NoError(t, mgr.Load(2, clips[0].ID))
	require.NoError(t, mgr.Play(2, 1.0, true))
	time.Sleep(20 * time.Millisecond)

	// Reset the demux counter so the pre-Load isn't counted.
	maxInFlight.Store(0)

	// Storm: 4 concurrent Loads (distinct clipIDs) on same slot + Play spinner.
	var loadsWG sync.WaitGroup
	for i := 1; i <= 4; i++ {
		loadsWG.Add(1)
		clipID := clips[i].ID
		go func() {
			defer loadsWG.Done()
			_ = mgr.Load(2, clipID)
		}()
	}

	spinnerDone := make(chan struct{})
	var spinnerWG sync.WaitGroup
	spinnerWG.Add(1)
	go func() {
		defer spinnerWG.Done()
		for {
			select {
			case <-spinnerDone:
				return
			default:
				_ = mgr.Play(2, 1.0, true)
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	loadsWG.Wait()
	time.Sleep(80 * time.Millisecond)
	close(spinnerDone)
	spinnerWG.Wait()

	// Primary RED/GREEN assertion: demux serialization on same slot.
	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("max concurrent DemuxFunc on same slot = %d, want 1 (Loads must serialize per-slot)", got)
	}

	// Cleanup.
	_ = mgr.Stop(2)
	_ = mgr.Eject(2)
	mgr.Close()

	// Orphan players (whose slot was overwritten) have no reachable cancel
	// func, so Close cannot reach them. Give them time to exit (they won't —
	// they loop) or for legit goroutines to drain.
	time.Sleep(300 * time.Millisecond)
	runtime.GC()

	// Tolerance of +2 for CI-pressure scheduler slack (GC-assist, finalizer,
	// timer goroutines can transiently push the count up even at rest).
	final := runtime.NumGoroutine()
	if final > baseline+2 {
		buf := make([]byte, 1<<15)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutine leak: baseline=%d, final=%d, delta=%d\n%s",
			baseline, final, final-baseline, buf[:n])
	}
}

// waitPlayerState polls until player 1..4 reaches want or the timeout expires.
func waitPlayerState(t *testing.T, mgr *Manager, playerID int, want PlayerState) {
	t.Helper()
	require.Eventually(t, func() bool {
		return mgr.PlayerStates()[playerID-1].State == want
	}, 2*time.Second, 5*time.Millisecond, "player %d never reached %s", playerID, want)
}

func TestManagerSyncPlayerLoadsByName(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StateLoaded, 0, false))

	st := mgr.PlayerStates()[0]
	assert.Equal(t, c.Name, st.ClipName)
	assert.Equal(t, StateLoaded, st.State)
}

func TestManagerSyncPlayerMissingClip(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	defer mgr.Close()

	assert.ErrorIs(t, mgr.SyncPlayer(1, "", "absent", StateLoaded, 0, false), ErrNotFound)
	assert.ErrorIs(t, mgr.SyncPlayer(0, "", "absent", StateLoaded, 0, false), ErrInvalidPlayer)
}

// Applying the same playing state repeatedly must neither reload the clip
// nor restart playback.
func TestManagerSyncPlayerIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	var demuxes atomic.Int32
	inner := mockDemuxFunc(30)
	mgr := NewManager(store, ManagerConfig{DemuxFunc: func(p string) ([]BufferedFrame, []BufferedAudioFrame, error) {
		demuxes.Add(1)
		return inner(p)
	}})
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	var starts atomic.Int32
	mgr.OnPlayerLifecycle(func(int, string) { starts.Add(1) }, func(int, string) {})

	require.NoError(t, mgr.SyncPlayer(2, "", c.Name, StatePlaying, 0.25, true))
	waitPlayerState(t, mgr, 2, StatePlaying)
	for range 3 {
		require.NoError(t, mgr.SyncPlayer(2, "", c.Name, StatePlaying, 0.25, true))
	}

	assert.Equal(t, int32(1), demuxes.Load(), "clip reloaded")
	assert.Equal(t, int32(1), starts.Load(), "playback restarted")
	assert.Equal(t, StatePlaying, mgr.PlayerStates()[1].State)
	assert.True(t, mgr.PlayerStates()[1].Loop)
}

func TestManagerSyncPlayerStateTransitions(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	// loaded -> paused starts playback and pauses it.
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePaused, 0.25, true))
	waitPlayerState(t, mgr, 1, StatePaused)

	// paused -> playing resumes.
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePlaying, 0.25, true))
	waitPlayerState(t, mgr, 1, StatePlaying)

	// playing -> loaded stops but keeps the clip.
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StateLoaded, 0, true))
	waitPlayerState(t, mgr, 1, StateLoaded)
	assert.Equal(t, c.Name, mgr.PlayerStates()[0].ClipName)

	// An out-of-range speed falls back to 1x instead of failing.
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePlaying, 0, true))
	waitPlayerState(t, mgr, 1, StatePlaying)
	assert.Equal(t, 1.0, mgr.PlayerStates()[0].Speed)

	// A speed change on a playing slot is applied without a restart.
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePlaying, 0.5, true))
	assert.Equal(t, 0.5, mgr.PlayerStates()[0].Speed)
	assert.Equal(t, StatePlaying, mgr.PlayerStates()[0].State)

	// empty ejects.
	require.NoError(t, mgr.SyncPlayer(1, "", "", StateEmpty, 0, false))
	assert.Equal(t, StateEmpty, mgr.PlayerStates()[0].State)
	assert.Empty(t, mgr.PlayerStates()[0].ClipName)
}

func TestManagerSyncPlayerUnknownState(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	assert.Error(t, mgr.SyncPlayer(1, "", c.Name, PlayerState("rewinding"), 1, false))
	assert.Equal(t, StateEmpty, mgr.PlayerStates()[0].State)
}

// Repeated paused and loaded applies leave the slot as it is.
func TestManagerSyncPlayerIdempotentPausedAndLoaded(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)
	var starts atomic.Int32
	mgr.OnPlayerLifecycle(func(int, string) { starts.Add(1) }, func(int, string) {})

	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePaused, 0.25, true))
	waitPlayerState(t, mgr, 1, StatePaused)
	for range 3 {
		require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePaused, 0.25, true))
	}
	assert.Equal(t, StatePaused, mgr.PlayerStates()[0].State)
	assert.Equal(t, int32(1), starts.Load())

	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StateLoaded, 0, true))
	for range 3 {
		require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StateLoaded, 0, true))
	}
	assert.Equal(t, StateLoaded, mgr.PlayerStates()[0].State)
	assert.Equal(t, int32(1), starts.Load())
}

// A slot whose clip already ended (holding) converges to a paused or a
// looping peer instead of staying in the hold.
func TestManagerSyncPlayerFromHolding(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	mgr := NewManager(store, ManagerConfig{DemuxFunc: mockDemuxFunc(3)})
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePlaying, 2.0, false))
	waitPlayerState(t, mgr, 1, StateHolding)
	require.NoError(t, mgr.SyncPlayer(1, "", c.Name, StatePaused, 0.25, false))
	waitPlayerState(t, mgr, 1, StatePaused)

	require.NoError(t, mgr.SyncPlayer(2, "", c.Name, StatePlaying, 2.0, false))
	waitPlayerState(t, mgr, 2, StateHolding)
	require.NoError(t, mgr.SyncPlayer(2, "", c.Name, StatePlaying, 0.25, true))
	waitPlayerState(t, mgr, 2, StatePlaying)
	assert.True(t, mgr.PlayerStates()[1].Loop)

	// A non-looping playing peer leaves the hold alone.
	require.NoError(t, mgr.SyncPlayer(3, "", c.Name, StatePlaying, 2.0, false))
	waitPlayerState(t, mgr, 3, StateHolding)
	require.NoError(t, mgr.SyncPlayer(3, "", c.Name, StatePlaying, 2.0, false))
	assert.Equal(t, StateHolding, mgr.PlayerStates()[2].State)
}

// With two clips of the same name, the clip ID picks the right one, and a
// slot already holding it is not reloaded.
func TestManagerSyncPlayerPrefersClipID(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	first := addTestClip(t, store, dir)
	second := addTestClip(t, store, dir)
	require.Equal(t, first.Name, second.Name)

	require.NoError(t, mgr.SyncPlayer(1, second.ID, second.Name, StateLoaded, 0, false))
	assert.Equal(t, second.ID, mgr.PlayerStates()[0].ClipID)
	require.NoError(t, mgr.SyncPlayer(1, first.ID, first.Name, StateLoaded, 0, false))
	assert.Equal(t, first.ID, mgr.PlayerStates()[0].ClipID)

	// An ID this store does not hold falls back to the name and keeps the
	// clip already loaded under that name.
	require.NoError(t, mgr.SyncPlayer(1, "ffffffffffffffffffffffffffffffff", first.Name, StateLoaded, 0, false))
	assert.Equal(t, first.ID, mgr.PlayerStates()[0].ClipID)
}

// gatedDemux returns a demux func that blocks until release is closed and
// counts its calls.
func gatedDemux(calls *atomic.Int32, started chan<- struct{}, release <-chan struct{}, fail bool) func(string) ([]BufferedFrame, []BufferedAudioFrame, error) {
	return func(path string) ([]BufferedFrame, []BufferedAudioFrame, error) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		if fail {
			return nil, nil, errors.New("demux failed")
		}
		return mockDemuxFunc(3)(path)
	}
}

// A snapshot apply that reaches an engine while the operator's load of the
// same clip still demuxes queues a second Load behind it. That Load finds the
// clip loaded and must not demux the whole file again.
func TestManagerLoadQueuedBehindSameClipDemuxesOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	c := addTestClip(t, store, dir)

	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mgr := NewManager(store, ManagerConfig{DemuxFunc: gatedDemux(&calls, started, release, false)})
	defer mgr.Close()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() { defer wg.Done(); errs[0] = mgr.Load(1, c.ID) }()
	<-started
	wg.Add(1)
	go func() { defer wg.Done(); errs[1] = mgr.Load(1, c.ID) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, int32(1), calls.Load())
	st := mgr.PlayerStates()[0]
	assert.Equal(t, c.ID, st.ClipID)
	assert.Equal(t, StateLoaded, st.State)
}

// An operator loading the clip a slot already plays restarts it.
func TestManagerLoadSameClipWhilePlayingReloads(t *testing.T) {
	mgr, store, dir := newTestManager(t)
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 1.0, true))
	require.NoError(t, mgr.Load(1, c.ID))

	assert.Equal(t, StateLoaded, mgr.PlayerStates()[0].State)
}

// While a slot demuxes a clip, it reports that clip as loading, so a peer
// engine that already shows it loaded reads the slot as catching up. The
// slot keeps reporting its previous content until the load completes.
func TestManagerPlayerStatesReportLoadingClip(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "succeeds"
		if fail {
			name = "fails"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewStore(dir, 1<<30)
			require.NoError(t, err)
			c := addTestClip(t, store, dir)

			var calls atomic.Int32
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			mgr := NewManager(store, ManagerConfig{DemuxFunc: gatedDemux(&calls, started, release, fail)})
			defer mgr.Close()
			var notified atomic.Int32
			mgr.SetOnStateChange(func() { notified.Add(1) })

			done := make(chan error, 1)
			go func() { done <- mgr.Load(2, c.ID) }()
			<-started

			st := mgr.PlayerStates()[1]
			assert.Equal(t, c.ID, st.LoadingClipID)
			assert.Equal(t, c.Name, st.LoadingClipName)
			assert.Equal(t, StateEmpty, st.State)
			assert.Empty(t, st.ClipID)
			assert.GreaterOrEqual(t, notified.Load(), int32(1), "the loading state must be broadcast")

			close(release)
			err = <-done
			st = mgr.PlayerStates()[1]
			assert.Empty(t, st.LoadingClipID)
			assert.Empty(t, st.LoadingClipName)
			if fail {
				require.Error(t, err)
				assert.Equal(t, StateEmpty, st.State)
			} else {
				require.NoError(t, err)
				assert.Equal(t, StateLoaded, st.State)
				assert.Equal(t, c.ID, st.ClipID)
			}
		})
	}
}

// A player outputs at the rate the manager's OutputRate reports when it
// starts, so a clip at another rate than the pipeline's gets the pipeline's
// cadence rather than its own.
func TestManagerPlayOutputsAtOutputRate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, 1<<30)
	require.NoError(t, err)
	rate := [2]int{50, 1}
	mgr := NewManager(store, ManagerConfig{
		DemuxFunc:  mockDemuxFunc(30),
		OutputRate: func() (int, int) { return rate[0], rate[1] },
	})
	defer mgr.Close()
	c := addTestClip(t, store, dir)

	require.NoError(t, mgr.Load(1, c.ID))
	require.NoError(t, mgr.Play(1, 1.0, true))
	mgr.mu.Lock()
	cfg := mgr.players[0].player.config
	mgr.mu.Unlock()
	assert.Equal(t, 50, cfg.OutputFPSNum)
	assert.Equal(t, 1, cfg.OutputFPSDen)

	rate = [2]int{60000, 1001}
	require.NoError(t, mgr.Stop(1))
	require.NoError(t, mgr.Play(1, 1.0, true))
	mgr.mu.Lock()
	cfg = mgr.players[0].player.config
	mgr.mu.Unlock()
	assert.Equal(t, 60000, cfg.OutputFPSNum)
	assert.Equal(t, 1001, cfg.OutputFPSDen)
}
