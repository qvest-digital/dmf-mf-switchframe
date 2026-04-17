package playout

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

func TestManager_NewManager(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	if m == nil {
		t.Fatal("NewManager returned nil")
	}

	// All channels should be pre-created.
	for i := 0; i < MaxChannels; i++ {
		if m.channels[i] == nil {
			t.Errorf("channel[%d] is nil", i)
		}
	}
}

func TestManager_ValidateChannelID(t *testing.T) {
	m := NewManager(ManagerConfig{})

	tests := []struct {
		id      int
		wantErr bool
	}{
		{0, true},
		{1, false},
		{2, false},
		{MaxChannels, false},
		{MaxChannels + 1, true},
		{-1, true},
	}

	for _, tt := range tests {
		err := m.validateChannelID(tt.id)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateChannelID(%d): err=%v, wantErr=%v", tt.id, err, tt.wantErr)
		}
		if err != nil && err != ErrInvalidChannel {
			t.Errorf("validateChannelID(%d): got %v, want ErrInvalidChannel", tt.id, err)
		}
	}
}

func TestManager_LoadAndStart(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	pl := testPlaylist(clipItem("a"), clipItem("b"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the channel to complete its playlist.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestManager_ChannelStates(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	states := m.ChannelStates()
	if len(states) != MaxChannels {
		t.Fatalf("ChannelStates returned %d states, want %d", len(states), MaxChannels)
	}

	for i, s := range states {
		if s.ID != i+1 {
			t.Errorf("states[%d].ID = %d, want %d", i, s.ID, i+1)
		}
		if s.State != "stopped" {
			t.Errorf("states[%d].State = %q, want %q", i, s.State, "stopped")
		}
	}
}

func TestManager_Close(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	// Load and start a channel with a live item (blocks until advance).
	pl := testPlaylist(liveItem("live-1", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the goroutine time to enter the live item wait.
	time.Sleep(50 * time.Millisecond)

	// Close should stop the running channel.
	m.Close()

	states := m.ChannelStates()
	if states[0].State != "stopped" {
		t.Errorf("state after Close = %q, want %q", states[0].State, "stopped")
	}
}

func TestManager_Next(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	// First item holds, second completes.
	pl := testPlaylist(holdClipItem("hold"), clipItem("end"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for hold state.
	time.Sleep(300 * time.Millisecond)

	// Advance past hold.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestManager_TakeManualAndReturnToAuto(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	// Live item (blocks) then a clip item.
	pl := testPlaylist(liveItem("live", "cam"), clipItem("end"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Take manual control.
	if err := m.TakeManual(1); err != nil {
		t.Fatalf("TakeManual: %v", err)
	}

	states := m.ChannelStates()
	if states[0].Mode != "manual" {
		t.Errorf("mode after TakeManual = %q, want %q", states[0].Mode, "manual")
	}

	// Advance past the live item; manual mode blocks before next item.
	_ = m.Next(1)
	time.Sleep(100 * time.Millisecond)

	states = m.ChannelStates()
	if states[0].State != "on_air" {
		t.Errorf("state during manual = %q, want %q", states[0].State, "on_air")
	}

	// Return to auto.
	if err := m.ReturnToAuto(1); err != nil {
		t.Fatalf("ReturnToAuto: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestManager_LifecycleCallbacks(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	var startCalled atomic.Int32
	var stopCalled atomic.Int32
	var startChannelID atomic.Int32
	var stopChannelID atomic.Int32

	m.OnChannelLifecycle(
		func(channelID int, key string) {
			startCalled.Store(1)
			startChannelID.Store(int32(channelID))
		},
		func(channelID int, key string) {
			stopCalled.Store(1)
			stopChannelID.Store(int32(channelID))
		},
	)

	pl := testPlaylist(liveItem("live-1", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for onStart to be called.
	waitFor(t, 2*time.Second, func() bool {
		return startCalled.Load() == 1
	})
	if startChannelID.Load() != 1 {
		t.Errorf("onStart channelID = %d, want 1", startChannelID.Load())
	}

	// Stop the channel.
	_ = m.Stop(1)

	// Wait for onStop to be called.
	waitFor(t, 2*time.Second, func() bool {
		return stopCalled.Load() == 1
	})
	if stopChannelID.Load() != 1 {
		t.Errorf("onStop channelID = %d, want 1", stopChannelID.Load())
	}
}

func TestManager_RawVideoOutput(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})
	defer m.Close()

	var frameCount atomic.Int32
	var lastKey atomic.Value

	m.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
		lastKey.Store(key)
	})

	pl := testPlaylist(clipItem("video"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	count := frameCount.Load()
	if count < 5 {
		t.Errorf("frame count = %d, want >= 5", count)
	}

	key, _ := lastKey.Load().(string)
	if key != "playout:1" {
		t.Errorf("frame key = %q, want %q", key, "playout:1")
	}
}

func TestManager_InvalidChannelID(t *testing.T) {
	m := NewManager(ManagerConfig{})

	// All operations should reject invalid channel IDs.
	pl := testPlaylist(clipItem("a"))

	if err := m.LoadPlaylist(0, pl); err != ErrInvalidChannel {
		t.Errorf("LoadPlaylist(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.Start(0); err != ErrInvalidChannel {
		t.Errorf("Start(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.Stop(0); err != ErrInvalidChannel {
		t.Errorf("Stop(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.Next(0); err != ErrInvalidChannel {
		t.Errorf("Next(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.Previous(0); err != ErrInvalidChannel {
		t.Errorf("Previous(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.JumpTo(0, "x"); err != ErrInvalidChannel {
		t.Errorf("JumpTo(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.TakeManual(0); err != ErrInvalidChannel {
		t.Errorf("TakeManual(0): got %v, want ErrInvalidChannel", err)
	}
	if err := m.ReturnToAuto(0); err != ErrInvalidChannel {
		t.Errorf("ReturnToAuto(0): got %v, want ErrInvalidChannel", err)
	}

	// Also test above max.
	if err := m.LoadPlaylist(MaxChannels+1, pl); err != ErrInvalidChannel {
		t.Errorf("LoadPlaylist(%d): got %v, want ErrInvalidChannel", MaxChannels+1, err)
	}
}

func TestManager_SetSpliceCoordinator(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	m.SetSpliceCoordinator(sc)

	// Verify splice coordinator was stored on the manager.
	m.mu.Lock()
	if m.spliceCoordinator != sc {
		t.Error("splice coordinator not stored on manager")
	}
	m.mu.Unlock()

	// Verify it was propagated to all pod boards.
	for i := 0; i < MaxChannels; i++ {
		m.mu.Lock()
		pb := m.podBoards[i]
		m.mu.Unlock()

		pb.mu.Lock()
		if pb.spliceCoordinator != sc {
			t.Errorf("podBoard[%d] splice coordinator not set", i)
		}
		pb.mu.Unlock()
	}
}

func TestManager_SetPTSProvider_PropagatesToPodBoards(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	ptsFn := func() int64 { return 42 }
	m.SetPTSProvider(ptsFn)

	// Verify PTS provider was propagated to all pod boards.
	for i := 0; i < MaxChannels; i++ {
		m.mu.Lock()
		pb := m.podBoards[i]
		m.mu.Unlock()

		pb.mu.Lock()
		if pb.ptsProvider == nil {
			t.Errorf("podBoard[%d] PTS provider is nil", i)
		} else if pb.ptsProvider() != 42 {
			t.Errorf("podBoard[%d] PTS provider returned %d, want 42", i, pb.ptsProvider())
		}
		pb.mu.Unlock()
	}
}

func TestManager_SetSpliceCoordinator_PropagatesPTSProvider(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	// Set PTS provider first.
	ptsFn := func() int64 { return 99 }
	m.SetPTSProvider(ptsFn)

	// Now set splice coordinator — it should also propagate PTS provider.
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})
	m.SetSpliceCoordinator(sc)

	// Verify both splice coordinator and PTS provider are set on pod boards.
	for i := 0; i < MaxChannels; i++ {
		m.mu.Lock()
		pb := m.podBoards[i]
		m.mu.Unlock()

		pb.mu.Lock()
		if pb.spliceCoordinator != sc {
			t.Errorf("podBoard[%d] splice coordinator not set", i)
		}
		if pb.ptsProvider == nil {
			t.Errorf("podBoard[%d] PTS provider not propagated", i)
		}
		pb.mu.Unlock()
	}
}

func TestManager_Start_WiresSpliceCoordinator(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})
	m.SetSpliceCoordinator(sc)

	// Load and start a channel.
	pl := testPlaylist(liveItem("live", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify the channel got the splice coordinator.
	m.mu.Lock()
	ch := m.channels[0]
	m.mu.Unlock()

	ch.mu.Lock()
	if ch.spliceCoordinator != sc {
		t.Error("channel splice coordinator not set after Start")
	}
	ch.mu.Unlock()
}

func TestManager_PlaylistItems(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	// No playlist loaded → nil items, no error.
	items, err := m.PlaylistItems(1)
	if err != nil {
		t.Fatalf("PlaylistItems(1): unexpected error: %v", err)
	}
	if items != nil {
		t.Fatalf("expected nil items, got %v", items)
	}

	// Invalid channel ID → error.
	_, err = m.PlaylistItems(0)
	if err != ErrInvalidChannel {
		t.Errorf("PlaylistItems(0): got %v, want ErrInvalidChannel", err)
	}
	_, err = m.PlaylistItems(MaxChannels + 1)
	if err != ErrInvalidChannel {
		t.Errorf("PlaylistItems(%d): got %v, want ErrInvalidChannel", MaxChannels+1, err)
	}

	// Load playlist then retrieve.
	pl := testPlaylist(clipItem("a"), liveItem("b", "cam-1"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	items, err = m.PlaylistItems(1)
	if err != nil {
		t.Fatalf("PlaylistItems(1): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "a" {
		t.Errorf("items[0].ID = %q, want %q", items[0].ID, "a")
	}
	if items[1].ID != "b" {
		t.Errorf("items[1].ID = %q, want %q", items[1].ID, "b")
	}
}

func TestManager_SetAudioOutput_PropagatesToChannels(t *testing.T) {
	frames := fakeFramesWithAudio(5)
	provider := NewFakeAssetProvider(frames)

	m := NewManager(ManagerConfig{
		AssetProvider: provider,
	})

	var mu sync.Mutex
	var audioCalls []struct {
		key        string
		sampleRate int
		channels   int
	}

	m.SetAudioOutput(func(key string, data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		audioCalls = append(audioCalls, struct {
			key        string
			sampleRate int
			channels   int
		}{key: key, sampleRate: sampleRate, channels: channels})
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("audio-mgr"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	mu.Lock()
	defer mu.Unlock()

	if len(audioCalls) < 5 {
		t.Fatalf("audio callback count = %d, want >= 5", len(audioCalls))
	}
	for i, call := range audioCalls[:5] {
		if call.key != "playout:1" {
			t.Errorf("call[%d].key = %q, want %q", i, call.key, "playout:1")
		}
		if call.sampleRate != 48000 {
			t.Errorf("call[%d].sampleRate = %d, want 48000", i, call.sampleRate)
		}
		if call.channels != 2 {
			t.Errorf("call[%d].channels = %d, want 2", i, call.channels)
		}
	}
}

func TestManager_SetGPUSourceTap_PropagatesToChannels(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	// Register a GPU source tap.
	called := atomic.Int32{}
	m.SetGPUSourceTap(func(channelKey, sourceKey string) *DecodedFrame {
		called.Add(1)
		return &DecodedFrame{
			YUV:    make([]byte, video.YUV420FrameSize(1920, 1080)),
			Width:  1920,
			Height: 1080,
		}
	})

	// Start a channel with a live item so the tap gets wired during Start().
	pl := testPlaylist(liveItem("live-tap", "cam-1"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for some frames to be emitted (live item uses the tap).
	time.Sleep(200 * time.Millisecond)

	if called.Load() == 0 {
		t.Error("GPU source tap was never called for live item")
	}
}

func TestManager_SetPipelineFormat(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	m.SetPipelineFormat(1280, 720, 59.94)

	m.mu.Lock()
	w := m.pipelineWidth
	h := m.pipelineHeight
	fps := m.pipelineFPS
	m.mu.Unlock()

	if w != 1280 {
		t.Errorf("pipelineWidth = %d, want 1280", w)
	}
	if h != 720 {
		t.Errorf("pipelineHeight = %d, want 720", h)
	}
	if fps != 59.94 {
		t.Errorf("pipelineFPS = %f, want 59.94", fps)
	}
}

func TestManager_SetPipelineFormat_AffectsNewChannels(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})
	defer m.Close()

	m.SetPipelineFormat(1280, 720, 59.94)

	// Load and start a channel — it should use the pipeline format.
	pl := testPlaylist(liveItem("live-fmt", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The channel's config should reflect the pipeline format.
	m.mu.Lock()
	ch := m.channels[0]
	m.mu.Unlock()

	ch.mu.Lock()
	chW := ch.config.Width
	chH := ch.config.Height
	chFPS := ch.config.FPS
	ch.mu.Unlock()

	if chW != 1280 {
		t.Errorf("channel width = %d, want 1280", chW)
	}
	if chH != 720 {
		t.Errorf("channel height = %d, want 720", chH)
	}
	if chFPS != 59.94 {
		t.Errorf("channel FPS = %f, want 59.94", chFPS)
	}
}

func TestManager_LoadPlaylist_IdleAsset(t *testing.T) {
	m := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	// Load a playlist with an idle asset URI.
	pl := testPlaylist(clipItem("a"))
	pl.IdleAssetURI = "local:///idle/bars.ts"

	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	// Verify idle loop was reconfigured on the channel.
	m.mu.Lock()
	ch := m.channels[0]
	m.mu.Unlock()

	ch.mu.Lock()
	idleURI := ch.idleLoop.assetURI
	ch.mu.Unlock()

	if idleURI != "local:///idle/bars.ts" {
		t.Errorf("idle asset URI = %q, want %q", idleURI, "local:///idle/bars.ts")
	}
}

func TestManager_PlaylistHash(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(3)})
	defer m.Close()

	// No playlist loaded — should return empty.
	if got := m.PlaylistHash(1); got != "" {
		t.Errorf("PlaylistHash(1) = %q, want empty for no playlist", got)
	}

	// Invalid channel — should return empty.
	if got := m.PlaylistHash(0); got != "" {
		t.Errorf("PlaylistHash(0) = %q, want empty for invalid channel", got)
	}

	// Load playlist — should return non-empty hash.
	pl := testPlaylist(clipItem("a"), clipItem("b"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	got := m.PlaylistHash(1)
	if got == "" {
		t.Fatal("PlaylistHash(1) = empty, want non-empty after loading playlist")
	}
	if len(got) < len("sha256:")+64 {
		t.Errorf("PlaylistHash(1) = %q, want sha256: prefix + 64-char hex", got)
	}
}

func TestManager_ChannelElapsedMs_Stopped(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(3)})
	defer m.Close()

	// Stopped channel — should return 0.
	if got := m.ChannelElapsedMs(1); got != 0 {
		t.Errorf("ChannelElapsedMs(1) = %d, want 0 when stopped", got)
	}

	// Invalid channel — should return 0.
	if got := m.ChannelElapsedMs(0); got != 0 {
		t.Errorf("ChannelElapsedMs(0) = %d, want 0 for invalid channel", got)
	}
}

func TestManager_ChannelElapsedMs_Playing(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(90)})
	defer m.Close()

	pl := testPlaylist(clipItem("a"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air.
	waitFor(t, 2*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Allow some frames to be emitted.
	time.Sleep(100 * time.Millisecond)

	elapsed := m.ChannelElapsedMs(1)
	if elapsed <= 0 {
		t.Errorf("ChannelElapsedMs(1) = %d, want > 0 when playing", elapsed)
	}
}

// ---------------------------------------------------------------------------
// SeekToItem tests
// ---------------------------------------------------------------------------

func TestManager_SeekToItem_InvalidChannel(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(30)})
	defer m.Close()

	err := m.SeekToItem(0, 0, 0)
	if err != ErrInvalidChannel {
		t.Errorf("SeekToItem(0, ...): got %v, want ErrInvalidChannel", err)
	}

	err = m.SeekToItem(MaxChannels+1, 0, 0)
	if err != ErrInvalidChannel {
		t.Errorf("SeekToItem(%d, ...): got %v, want ErrInvalidChannel", MaxChannels+1, err)
	}
}

func TestManager_SeekToItem_NoPlaylist(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(30)})
	defer m.Close()

	err := m.SeekToItem(1, 0, 0)
	if err != ErrNoPlaylist {
		t.Errorf("SeekToItem with no playlist: got %v, want ErrNoPlaylist", err)
	}
}

func TestManager_SeekToItem_MovesSlate(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(90)})
	defer m.Close()

	pl := testPlaylist(
		slateItem("s0", 5000),
		slateItem("s1", 3000),
		slateItem("s2", 4000),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Seek to item 2.
	err := m.SeekToItem(1, 2, 500)
	if err != nil {
		t.Fatalf("SeekToItem: %v", err)
	}

	// Verify cursor moved via channel states.
	states := m.ChannelStates()
	if states[0].ItemIndex != 2 {
		t.Errorf("ItemIndex = %d, want 2", states[0].ItemIndex)
	}

	if err := m.Stop(1); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestManager_SeekToItem_InvalidIndex(t *testing.T) {
	m := NewManager(ManagerConfig{AssetProvider: fakeProvider(30)})
	defer m.Close()

	pl := testPlaylist(slateItem("s0", 5000))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	err := m.SeekToItem(1, 5, 0)
	if err != ErrInvalidItem {
		t.Errorf("SeekToItem with invalid index: got %v, want ErrInvalidItem", err)
	}

	if err := m.Stop(1); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
