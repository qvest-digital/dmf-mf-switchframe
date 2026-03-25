package playout

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFrames returns n fake YUV420 frames at 30fps.
func fakeFrames(n int) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	// 30fps = 3000 PTS ticks per frame (90kHz / 30)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, 1920*1080*3/2),
			PTS:        int64(i) * 3000,
			IsKeyframe: i == 0,
			Width:      1920,
			Height:     1080,
		}
	}
	return frames
}

// fakeProvider returns a FakeAssetProvider with n frames (replaces old fakeDemux pattern).
func fakeProvider(n int) *FakeAssetProvider {
	return NewFakeAssetProvider(fakeFrames(n))
}

// waitFor polls condition at 5ms intervals until it returns true or timeout expires.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: timed out waiting for condition")
}

// testPlaylist returns a simple playlist with the given items.
func testPlaylist(items ...PlaylistItem) *Playlist {
	return &Playlist{
		ID:    "test-pl",
		Name:  "Test Playlist",
		Items: items,
	}
}

// clipItem returns a clip PlaylistItem with sensible defaults.
func clipItem(id string) PlaylistItem {
	return PlaylistItem{
		ID:       id,
		Type:     ItemClip,
		AssetURI: "local:///clips/" + id + ".ts",
		Title:    "Clip " + id,
	}
}

// holdClipItem returns a clip PlaylistItem with Hold enabled.
func holdClipItem(id string) PlaylistItem {
	return PlaylistItem{
		ID:       id,
		Type:     ItemClip,
		AssetURI: "local:///clips/" + id + ".ts",
		Title:    "Hold Clip " + id,
		Hold:     true,
	}
}

// liveItem returns a live PlaylistItem.
func liveItem(id, source string) PlaylistItem {
	return PlaylistItem{
		ID:     id,
		Type:   ItemLive,
		Source: source,
		Title:  "Live " + id,
	}
}

// slateItem returns a slate PlaylistItem with the given duration.
func slateItem(id string, durationMs int64) PlaylistItem {
	return PlaylistItem{
		ID:         id,
		Type:       ItemSlate,
		AssetURI:   "local:///slates/" + id + ".ts",
		Title:      "Slate " + id,
		DurationMs: durationMs,
	}
}

func TestChannel_InitialState(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	if ch.State() != StateStopped {
		t.Errorf("initial state = %v, want StateStopped", ch.State())
	}
	if ch.Mode() != ModeAutomated {
		t.Errorf("initial mode = %v, want ModeAutomated", ch.Mode())
	}
}

func TestChannel_LoadPlaylist_TransitionsToCued(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})

	var stateChanged atomic.Int32
	ch.SetOnStateChange(func(s ChannelState) {
		if s == StateCued {
			stateChanged.Store(1)
		}
	})

	pl := testPlaylist(clipItem("a"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if ch.State() != StateCued {
		t.Errorf("state after load = %v, want StateCued", ch.State())
	}
	if stateChanged.Load() != 1 {
		t.Error("onStateChange was not called with StateCued")
	}
}

func TestChannel_LoadPlaylist_RejectsEmpty(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	pl := &Playlist{ID: "empty", Name: "Empty"}
	err := ch.LoadPlaylist(pl)
	if err == nil {
		t.Fatal("expected error for empty playlist")
	}
}

func TestChannel_Start_RequiresPlaylist(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	err := ch.Start()
	if err != ErrNoPlaylist {
		t.Fatalf("expected ErrNoPlaylist, got: %v", err)
	}
}

func TestChannel_Stop_FromStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	// Should be a no-op, not panic.
	ch.Stop()
	if ch.State() != StateStopped {
		t.Errorf("state after no-op stop = %v, want StateStopped", ch.State())
	}
}

func TestChannel_Start_TransitionsToOnAir(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(5),
	})

	var startKey string
	ch.SetOnStart(func(key string) {
		startKey = key
	})

	pl := testPlaylist(liveItem("live-1", "camera-1"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	if ch.State() != StateOnAir {
		t.Errorf("state after start = %v, want StateOnAir", ch.State())
	}
	if startKey != "playout:1" {
		t.Errorf("onStart key = %q, want %q", startKey, "playout:1")
	}
}

func TestChannel_PlaysThroughToCompleted(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3), // 3 frames = very fast
	})

	pl := testPlaylist(clipItem("a"), clipItem("b"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_Next_AdvancesItem(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// First item holds, second completes quickly.
	pl := testPlaylist(holdClipItem("hold"), clipItem("end"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for hold state (item finishes, enters hold loop).
	time.Sleep(300 * time.Millisecond)

	// Advance past hold.
	if err := ch.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_ManualOverride(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// Use a live item that blocks, so we have time to TakeManual.
	// Then a clip item that completes.
	pl := testPlaylist(liveItem("live", "cam"), clipItem("end"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for channel to be blocking on the live item.
	time.Sleep(100 * time.Millisecond)

	// Take manual control while blocked on live item.
	ch.TakeManual()
	if ch.Mode() != ModeManual {
		t.Errorf("mode after TakeManual = %v, want ModeManual", ch.Mode())
	}

	// Advance past the live item; in manual mode, run loop will block
	// before playing the next item.
	_ = ch.Next()

	// Give the goroutine time to process advance and enter manual wait.
	time.Sleep(100 * time.Millisecond)

	// Channel should still be on air (manual mode blocks before next item).
	if ch.State() != StateOnAir {
		t.Errorf("state during manual = %v, want StateOnAir", ch.State())
	}

	// Return to auto should resume and play through to completion.
	ch.ReturnToAuto()
	if ch.Mode() != ModeAutomated {
		t.Errorf("mode after ReturnToAuto = %v, want ModeAutomated", ch.Mode())
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_JumpTo(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// 3 live items; jump from first to third.
	pl := testPlaylist(
		liveItem("first", "cam-1"),
		liveItem("second", "cam-2"),
		liveItem("third", "cam-3"),
	)
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	// Wait for channel to be on the first live item.
	time.Sleep(100 * time.Millisecond)

	// Jump to "third".
	if err := ch.JumpTo("third"); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	// Wait a bit, then advance past "third" to complete.
	time.Sleep(100 * time.Millisecond)
	_ = ch.Next()

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_Previous(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// Two live items.
	pl := testPlaylist(
		liveItem("first", "cam-1"),
		liveItem("second", "cam-2"),
	)
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	// Advance to second item.
	time.Sleep(50 * time.Millisecond)
	_ = ch.Next()

	// Let the second item start.
	time.Sleep(50 * time.Millisecond)

	// Go back to first.
	if err := ch.Previous(); err != nil {
		t.Fatalf("Previous: %v", err)
	}

	// Now advance through both to complete.
	time.Sleep(50 * time.Millisecond)
	_ = ch.Next()
	time.Sleep(50 * time.Millisecond)
	_ = ch.Next()

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_LiveItem_HoldsUntilAdvance(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	pl := testPlaylist(liveItem("live-1", "camera-1"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Channel should stay on-air (live item blocks).
	time.Sleep(200 * time.Millisecond)
	if ch.State() != StateOnAir {
		t.Errorf("state during live = %v, want StateOnAir", ch.State())
	}

	// Advance past the live item.
	_ = ch.Next()

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_SlateItem_Duration(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// Slate with 200ms duration.
	pl := testPlaylist(slateItem("brb", 200))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for slate duration to expire and playlist to complete.
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_RawVideoOutput(t *testing.T) {
	var frameCount atomic.Int32

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(5),
	})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(clipItem("video"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	count := frameCount.Load()
	// At least 5 frames from the clip; may get 1-2 extra from the
	// fallback frame emitted during the completion/transition tick.
	if count < 5 {
		t.Errorf("frame count = %d, want >= 5", count)
	}
}

func TestChannel_Key(t *testing.T) {
	ch1 := NewChannel(1, ChannelConfig{})
	if ch1.Key() != "playout:1" {
		t.Errorf("Key() = %q, want %q", ch1.Key(), "playout:1")
	}

	ch2 := NewChannel(2, ChannelConfig{})
	if ch2.Key() != "playout:2" {
		t.Errorf("Key() = %q, want %q", ch2.Key(), "playout:2")
	}
}

func TestChannel_Status(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// Status when stopped.
	status := ch.Status()
	if status.ID != 1 {
		t.Errorf("status.ID = %d, want 1", status.ID)
	}
	if status.State != "stopped" {
		t.Errorf("status.State = %q, want %q", status.State, "stopped")
	}

	// Load playlist and check cued status.
	pl := testPlaylist(clipItem("a"), clipItem("b"))
	_ = ch.LoadPlaylist(pl)

	status = ch.Status()
	if status.State != "cued" {
		t.Errorf("status.State = %q, want %q", status.State, "cued")
	}
	if status.PlaylistName != "Test Playlist" {
		t.Errorf("status.PlaylistName = %q, want %q", status.PlaylistName, "Test Playlist")
	}
	if status.ItemCount != 2 {
		t.Errorf("status.ItemCount = %d, want 2", status.ItemCount)
	}
}

func TestChannel_StopCallback(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	var stopKey string
	ch.SetOnStop(func(key string) {
		stopKey = key
	})

	pl := testPlaylist(liveItem("live-1", "cam"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()

	time.Sleep(50 * time.Millisecond)
	ch.Stop()

	if stopKey != "playout:1" {
		t.Errorf("onStop key = %q, want %q", stopKey, "playout:1")
	}
	if ch.State() != StateStopped {
		t.Errorf("state after stop = %v, want StateStopped", ch.State())
	}
}

func TestChannel_EstimateFPS(t *testing.T) {
	t.Run("single_frame", func(t *testing.T) {
		frames := []DecodedFrame{{PTS: 0}}
		fps := estimateFPS(frames)
		if fps != 30.0 {
			t.Errorf("single frame FPS = %f, want 30.0", fps)
		}
	})

	t.Run("30fps", func(t *testing.T) {
		frames := make([]DecodedFrame, 31)
		for i := range frames {
			frames[i] = DecodedFrame{PTS: int64(i) * 3000} // 90000/30 = 3000
		}
		fps := estimateFPS(frames)
		if fps < 29.5 || fps > 30.5 {
			t.Errorf("30fps estimate = %f, want ~30.0", fps)
		}
	})

	t.Run("25fps", func(t *testing.T) {
		frames := make([]DecodedFrame, 26)
		for i := range frames {
			frames[i] = DecodedFrame{PTS: int64(i) * 3600} // 90000/25 = 3600
		}
		fps := estimateFPS(frames)
		if fps < 24.5 || fps > 25.5 {
			t.Errorf("25fps estimate = %f, want ~25.0", fps)
		}
	})
}

func TestChannel_DoubleStop(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	pl := testPlaylist(liveItem("live-1", "cam"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()

	time.Sleep(50 * time.Millisecond)

	// Double stop should not panic.
	ch.Stop()
	ch.Stop()

	if ch.State() != StateStopped {
		t.Errorf("state = %v, want StateStopped", ch.State())
	}
}

func TestChannel_Close(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	pl := testPlaylist(liveItem("live-1", "cam"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()

	time.Sleep(50 * time.Millisecond)

	// Close should stop the channel.
	ch.Close()
	if ch.State() != StateStopped {
		t.Errorf("state after Close = %v, want StateStopped", ch.State())
	}
}

func TestChannel_StartRequiresCued(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	// Start without loading should return ErrNoPlaylist.
	err := ch.Start()
	if err != ErrNoPlaylist {
		t.Fatalf("expected ErrNoPlaylist, got %v", err)
	}

	// Load, start, and try to start again.
	pl := testPlaylist(liveItem("live", "cam"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()
	defer ch.Stop()

	err = ch.Start()
	if err != ErrAlreadyOnAir {
		t.Fatalf("expected ErrAlreadyOnAir, got %v", err)
	}
}

func TestChannel_JumpTo_NotFound(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(3),
	})

	pl := testPlaylist(liveItem("live-1", "cam"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()
	defer ch.Stop()

	time.Sleep(50 * time.Millisecond)
	err := ch.JumpTo("nonexistent")
	if err != ErrItemNotFound {
		t.Fatalf("expected ErrItemNotFound, got %v", err)
	}
}

func TestChannel_NextWhileStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	err := ch.Next()
	if err != ErrNotOnAir {
		t.Fatalf("expected ErrNotOnAir, got %v", err)
	}
}

func TestChannel_PreviousWhileStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	err := ch.Previous()
	if err != ErrNotOnAir {
		t.Fatalf("expected ErrNotOnAir, got %v", err)
	}
}

func TestChannel_SecondaryEventsFireDuringPlayback(t *testing.T) {
	h := &mockHandler{}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(30), // 30 frames at 30fps = 1 second
	})
	ch.SetSecondaryHandler(h)

	pl := testPlaylist(PlaylistItem{
		ID:       "clip-1",
		Type:     ItemClip,
		AssetURI: "local:///test.ts",
		Title:    "Test Clip",
		SecondaryEvents: []SecondaryEvent{
			{Type: "graphics_on", Layer: 1, Template: "bug", OffsetMs: 0, Anchor: "start"},
			{Type: "graphics_off", Layer: 1, OffsetMs: 500, Anchor: "start"},
		},
	})

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	if len(h.calls) < 2 {
		t.Fatalf("handler calls = %d, want >= 2", len(h.calls))
	}
	if h.calls[0].Method != "OnGraphicsOn" {
		t.Errorf("call[0] = %q, want OnGraphicsOn", h.calls[0].Method)
	}
	foundOff := false
	for _, c := range h.calls {
		if c.Method == "OnGraphicsOff" {
			foundOff = true
		}
	}
	if !foundOff {
		t.Error("OnGraphicsOff never fired")
	}
}

func TestChannel_EndAnchoredSecondaryEvents(t *testing.T) {
	h := &mockHandler{}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(60), // 60 frames at 30fps = 2 seconds
	})
	ch.SetSecondaryHandler(h)

	pl := testPlaylist(PlaylistItem{
		ID:       "clip-1",
		Type:     ItemClip,
		AssetURI: "local:///test.ts",
		Title:    "Test Clip",
		SecondaryEvents: []SecondaryEvent{
			{Type: "graphics_off", Layer: 2, OffsetMs: -500, Anchor: "end"},
		},
	})

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	if len(h.calls) != 1 {
		t.Fatalf("handler calls = %d, want 1", len(h.calls))
	}
	if h.calls[0].Method != "OnGraphicsOff" {
		t.Errorf("call[0] = %q, want OnGraphicsOff", h.calls[0].Method)
	}
}

func TestChannel_SecondaryEventsOnLiveItem(t *testing.T) {
	h := &mockHandler{}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(10),
	})
	ch.SetSecondaryHandler(h)

	pl := testPlaylist(
		PlaylistItem{
			ID:     "live-1",
			Type:   ItemLive,
			Source: "camera-1",
			Title:  "Live",
			SecondaryEvents: []SecondaryEvent{
				{Type: "graphics_on", Layer: 1, Template: "lower-third", OffsetMs: 0, Anchor: "start"},
			},
		},
		clipItem("outro"),
	)

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait briefly for the live item to start and fire its event.
	time.Sleep(200 * time.Millisecond)

	calls := h.getCalls()
	if len(calls) < 1 {
		t.Fatal("expected at least 1 secondary event call on live item start")
	}
	if calls[0].Method != "OnGraphicsOn" {
		t.Errorf("call[0] = %q, want OnGraphicsOn", calls[0].Method)
	}

	// Advance past the live item.
	if err := ch.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_FeedsSpliceCoordinator(t *testing.T) {
	provider := fakeProvider(30) // 1 second at 30fps
	ch := NewChannel(1, ChannelConfig{AssetProvider: provider})

	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     100, // short for test
		WindowMs:      50,
		TimeoutMs:     500,
	})
	ch.SetSpliceCoordinator(sc)

	var frameCount atomic.Int64
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(clipItem("test"))
	ch.LoadPlaylist(pl)
	ch.Start()

	// Start a splice to verify frames are fed.
	time.Sleep(50 * time.Millisecond)
	sc.BeginSplice("pod-1", true, true, 180000, 0)

	// Wait for channel to finish playing.
	waitFor(t, 3*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	// The splice coordinator should have finalized (timeout or window close).
	waitFor(t, time.Second, func() bool {
		return !sc.Active()
	})
}

func TestChannel_SecondaryEventsOnSlateItem(t *testing.T) {
	h := &mockHandler{}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: fakeProvider(10),
	})
	ch.SetSecondaryHandler(h)

	pl := testPlaylist(
		PlaylistItem{
			ID:         "slate-1",
			Type:       ItemSlate,
			AssetURI:   "local:///slates/black.ts",
			Title:      "Slate",
			DurationMs: 2000, // 2 seconds
			SecondaryEvents: []SecondaryEvent{
				{Type: "graphics_on", Layer: 1, Template: "coming-up", OffsetMs: 0, Anchor: "start"},
				{Type: "graphics_off", Layer: 1, OffsetMs: -500, Anchor: "end"}, // fires at 1500ms
			},
		},
	)

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	calls := h.getCalls()
	if len(calls) < 2 {
		t.Fatalf("handler calls = %d, want >= 2, got: %+v", len(calls), calls)
	}
	if calls[0].Method != "OnGraphicsOn" {
		t.Errorf("call[0] = %q, want OnGraphicsOn", calls[0].Method)
	}
	if calls[1].Method != "OnGraphicsOff" {
		t.Errorf("call[1] = %q, want OnGraphicsOff", calls[1].Method)
	}
}

// --- Graceful degradation test helpers ---

// errorProvider always returns an error from Open.
type errorProvider struct{}

func (e *errorProvider) Open(uri string) (FrameIterator, error) {
	return nil, fmt.Errorf("media not found: %s", uri)
}

// errorAfterNIterator returns N good frames then persistent errors.
type errorAfterNIterator struct {
	good int
	pos  int
}

func (e *errorAfterNIterator) Next() (*DecodedFrame, error) {
	if e.pos >= e.good {
		return nil, fmt.Errorf("decode error at frame %d", e.pos)
	}
	f := &DecodedFrame{
		YUV:    make([]byte, 320*240*3/2),
		PTS:    int64(e.pos) * 3000,
		Width:  320,
		Height: 240,
	}
	e.pos++
	return f, nil
}

func (e *errorAfterNIterator) EstimateFPS() float64 { return 30.0 }
func (e *errorAfterNIterator) TotalFrames() int      { return -1 }
func (e *errorAfterNIterator) Close()                {}

type errorAfterNProvider struct {
	goodFrames int
}

func (p *errorAfterNProvider) Open(uri string) (FrameIterator, error) {
	return &errorAfterNIterator{good: p.goodFrames}, nil
}

func filterEvents(events []AsRunEvent, typ AsRunEventType) []AsRunEvent {
	var out []AsRunEvent
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestChannel_SkipBadAsset_EmitsAsRunError(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: &errorProvider{},
	})

	var mu sync.Mutex
	var events []AsRunEvent
	ch.SetAsRunLog(func(ev AsRunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("bad-1"), clipItem("bad-2"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	mu.Lock()
	errs := filterEvents(events, AsRunError)
	mu.Unlock()

	if len(errs) < 2 {
		t.Fatalf("expected at least 2 AsRunError events, got %d: %+v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Message == "" {
			t.Error("AsRunError event has empty message")
		}
	}
}

func TestChannel_FrameErrorSkipsItem(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider: &errorAfterNProvider{goodFrames: 2},
	})

	var mu sync.Mutex
	var events []AsRunEvent
	ch.SetAsRunLog(func(ev AsRunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("err-1"), clipItem("err-2"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	mu.Lock()
	errs := filterEvents(events, AsRunError)
	mu.Unlock()

	if len(errs) < 2 {
		t.Fatalf("expected at least 2 AsRunError events for frame decode failures, got %d: %+v", len(errs), errs)
	}
	for _, e := range errs {
		if e.Message == "" {
			t.Error("AsRunError event has empty message")
		}
	}
}

func TestChannel_PlaylistItems(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})

	// No playlist → nil.
	items := ch.PlaylistItems()
	if items != nil {
		t.Fatalf("expected nil items when no playlist, got %v", items)
	}

	// Load a playlist.
	pl := &Playlist{
		ID:   "test",
		Name: "Test",
		Items: []PlaylistItem{
			{ID: "a", Type: ItemClip, Title: "Clip A", DurationMs: 5000, AssetURI: "local:///a.ts"},
			{ID: "b", Type: ItemLive, Title: "Live B", Source: "camera-1"},
		},
	}
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}

	items = ch.PlaylistItems()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ID != "a" {
		t.Errorf("items[0].ID = %q, want %q", items[0].ID, "a")
	}
	if items[1].ID != "b" {
		t.Errorf("items[1].ID = %q, want %q", items[1].ID, "b")
	}

	// Returned slice is a copy — mutating it should not affect the channel.
	items[0].Title = "MUTATED"
	fresh := ch.PlaylistItems()
	if fresh[0].Title != "Clip A" {
		t.Errorf("PlaylistItems returned shared slice: fresh[0].Title = %q, want %q", fresh[0].Title, "Clip A")
	}
}

// fakeFramesWithAudio returns n fake YUV420 frames with audio data at 30fps.
func fakeFramesWithAudio(n int) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:             make([]byte, 320*240*3/2),
			PTS:             int64(i) * 3000,
			IsKeyframe:      i == 0,
			Width:           320,
			Height:          240,
			AudioData:       []byte{0xAA, 0xBB, byte(i)}, // synthetic audio payload
			AudioPTS:        int64(i) * 3000,
			AudioSampleRate: 48000,
			AudioChannels:   2,
		}
	}
	return frames
}

func TestChannel_AudioOutput(t *testing.T) {
	frames := fakeFramesWithAudio(5)
	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
	})

	var mu sync.Mutex
	var audioCalls []struct {
		data       []byte
		pts        int64
		sampleRate int
		channels   int
	}

	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		audioCalls = append(audioCalls, struct {
			data       []byte
			pts        int64
			sampleRate int
			channels   int
		}{
			data:       append([]byte(nil), data...),
			pts:        pts,
			sampleRate: sampleRate,
			channels:   channels,
		})
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("audio-test"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	mu.Lock()
	defer mu.Unlock()

	if len(audioCalls) < 5 {
		t.Fatalf("audio callback count = %d, want >= 5", len(audioCalls))
	}

	// Check the first 5 calls (the clip frames with audio).
	for i, call := range audioCalls[:5] {
		if call.sampleRate != 48000 {
			t.Errorf("call[%d].sampleRate = %d, want 48000", i, call.sampleRate)
		}
		if call.channels != 2 {
			t.Errorf("call[%d].channels = %d, want 2", i, call.channels)
		}
		if len(call.data) != 3 {
			t.Errorf("call[%d].data len = %d, want 3", i, len(call.data))
		}
	}
}

func TestChannel_AudioOutput_NoAudioData(t *testing.T) {
	// Frames without audio data should not trigger audio callback.
	provider := fakeProvider(5)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
	})

	var audioCount atomic.Int32
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		audioCount.Add(1)
	})

	pl := testPlaylist(clipItem("no-audio"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	if count := audioCount.Load(); count != 0 {
		t.Errorf("audio callback called %d times for frames with no audio, want 0", count)
	}
}

func TestChannel_SlateOutputsBlackFrames(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{})
	// No AssetProvider needed — slate doesn't use it.

	var frameCount atomic.Int32
	var firstY atomic.Int32
	firstY.Store(-1)
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		n := frameCount.Add(1)
		if n == 1 && len(yuv) > 0 {
			firstY.Store(int32(yuv[0]))
		}
	})

	pl := testPlaylist(PlaylistItem{
		ID:         "s1",
		Type:       ItemSlate,
		AssetURI:   "local:///slates/black.ts",
		Title:      "Black Slate",
		DurationMs: 200,
	})
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	count := frameCount.Load()
	if count <= 0 {
		t.Fatalf("slate should output black frames, got %d", count)
	}
	// BT.709 black: Y=16
	y := firstY.Load()
	if y != 16 {
		t.Errorf("Y plane should be 16 (BT.709 black), got %d", y)
	}
}

func TestChannel_AudioOutput_NilCallback(t *testing.T) {
	// Frames with audio but no callback set should not panic.
	frames := fakeFramesWithAudio(3)
	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
	})
	// Do NOT set audio output callback.

	pl := testPlaylist(clipItem("no-callback"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
	// If we get here without panic, the test passes.
}

// ---------------------------------------------------------------------------
// Config-driven dimension and FPS tests
// ---------------------------------------------------------------------------

// fakeFramesSized returns n fake YUV420 frames at the given resolution and fps.
func fakeFramesSized(n, w, h int, fps float64) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	ptsTick := int64(90000.0 / fps)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, w*h*3/2),
			PTS:        int64(i) * ptsTick,
			IsKeyframe: i == 0,
			Width:      w,
			Height:     h,
		}
	}
	return frames
}

func TestChannel_ConfigDimensions_UsedByCompositor(t *testing.T) {
	w, h := 1280, 720
	frames := fakeFramesSized(5, w, h, 30.0)
	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
		Width:         w,
		Height:        h,
		FPS:           30.0,
	})

	var mu sync.Mutex
	var widths []int
	ch.SetRawVideoOutput(func(yuv []byte, fw, fh int, pts int64, isKeyframe bool) {
		mu.Lock()
		widths = append(widths, fw)
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	mu.Lock()
	defer mu.Unlock()

	if len(widths) == 0 {
		t.Fatal("no frames emitted")
	}
	// Clip frames carry their own dimensions, so we check the clip output.
	for i, fw := range widths {
		if fw != w {
			t.Errorf("frame[%d] width = %d, want %d", i, fw, w)
		}
	}
}

func TestChannel_ConfigDimensions_SlateMatchesConfig(t *testing.T) {
	w, h := 640, 480
	ch := NewChannel(1, ChannelConfig{
		Width:  w,
		Height: h,
		FPS:    25.0,
	})

	var mu sync.Mutex
	var dims []struct{ w, h int }
	ch.SetRawVideoOutput(func(yuv []byte, fw, fh int, pts int64, isKeyframe bool) {
		mu.Lock()
		dims = append(dims, struct{ w, h int }{fw, fh})
		mu.Unlock()
	})

	pl := testPlaylist(slateItem("brb", 200))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	mu.Lock()
	defer mu.Unlock()

	if len(dims) == 0 {
		t.Fatal("no frames emitted for slate")
	}
	for i, d := range dims {
		if d.w != w || d.h != h {
			t.Errorf("slate frame[%d] = %dx%d, want %dx%d", i, d.w, d.h, w, h)
		}
	}
}

func TestChannel_ConfigFPS_AffectsTickRate(t *testing.T) {
	// At 60fps, frame duration should be ~16.6ms, so a 200ms slate should produce
	// roughly 12 frames. At 10fps it would be ~2.
	ch := NewChannel(1, ChannelConfig{
		Width:  320,
		Height: 240,
		FPS:    60.0,
	})

	var frameCount atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(slateItem("fast", 200))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	count := frameCount.Load()
	// 200ms at 60fps should produce ~12 frames; allow a generous range.
	if count < 5 {
		t.Errorf("60fps slate produced only %d frames in 200ms, expected >= 5", count)
	}
}

func TestChannel_IdleLoop_PlaysBetweenItems(t *testing.T) {
	frames := fakeFramesSized(3, 320, 240, 30.0)
	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
		Width:         320,
		Height:        240,
		FPS:           30.0,
	})

	// First item completes quickly, second has a future scheduled time.
	future := time.Now().Add(500 * time.Millisecond)
	pl := testPlaylist(
		clipItem("first"),
		PlaylistItem{
			ID:            "second",
			Type:          ItemClip,
			AssetURI:      "local:///clips/second.ts",
			Title:         "Second",
			ScheduledTime: &future,
		},
	)

	var mu sync.Mutex
	var framesSeen int
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		mu.Lock()
		framesSeen++
		mu.Unlock()
	})

	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the second item to start and complete.
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	// Verify frames were emitted during idle gap and during clip playback.
	mu.Lock()
	total := framesSeen
	mu.Unlock()

	// 3 frames for first clip + idle frames during 500ms gap + 3 frames for second clip.
	// Should be significantly more than just 6.
	if total < 6 {
		t.Errorf("total frames = %d, expected at least 6 (clips + idle gap)", total)
	}
}

func TestChannel_LiveTap_EmitsFrames(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		Width:  320,
		Height: 240,
		FPS:    30.0,
	})

	// Set up a fake GPU source tap that returns a frame.
	tapFrame := &DecodedFrame{
		YUV:        make([]byte, 320*240*3/2),
		Width:      320,
		Height:     240,
		IsKeyframe: true,
	}
	ch.SetGPUSourceTap(func(sourceKey string) *DecodedFrame {
		if sourceKey == "camera-1" {
			return tapFrame
		}
		return nil
	})

	var frameCount atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(liveItem("live-1", "camera-1"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let it emit a few frames.
	time.Sleep(200 * time.Millisecond)

	count := frameCount.Load()
	if count < 3 {
		t.Errorf("live tap frame count = %d, want >= 3", count)
	}

	// Advance past to complete.
	_ = ch.Next()
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_Transition_BlendsFrames(t *testing.T) {
	frames := fakeFramesSized(30, 320, 240, 30.0) // enough frames for transition
	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AssetProvider: provider,
		Width:         320,
		Height:        240,
		FPS:           30.0,
	})

	var frameCount atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	// Two clips with a 200ms dissolve transition.
	tc := &TransitionConfig{Type: "dissolve", DurationMs: 200}
	item1 := clipItem("first")
	item2 := clipItem("second")
	item2.Transition = tc

	pl := testPlaylist(item1, item2)
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})

	count := frameCount.Load()
	// Both clips + transition frames should produce a good number of frames.
	if count < 5 {
		t.Errorf("transition test frame count = %d, expected >= 5", count)
	}
}

func TestChannel_DefaultDimensions_WhenZero(t *testing.T) {
	// When Width/Height/FPS are all zero, defaults should be used.
	ch := NewChannel(1, ChannelConfig{})

	// The slate frame should have default dimensions.
	ch.mu.Lock()
	sf := ch.slateFrame
	ch.mu.Unlock()

	if sf.Width != 1920 || sf.Height != 1080 {
		t.Errorf("default slate dimensions = %dx%d, want 1920x1080", sf.Width, sf.Height)
	}

	expectedSize := 1920 * 1080 * 3 / 2
	if len(sf.YUV) != expectedSize {
		t.Errorf("slate YUV size = %d, want %d", len(sf.YUV), expectedSize)
	}
}

func TestChannel_MakeBlackFrame(t *testing.T) {
	frame := makeBlackFrame(320, 240)
	if frame.Width != 320 || frame.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", frame.Width, frame.Height)
	}
	expectedSize := 320 * 240 * 3 / 2
	if len(frame.YUV) != expectedSize {
		t.Errorf("YUV size = %d, want %d", len(frame.YUV), expectedSize)
	}
	// Y plane should be 16 (BT.709 black).
	ySize := 320 * 240
	for i := 0; i < ySize; i++ {
		if frame.YUV[i] != 16 {
			t.Errorf("Y[%d] = %d, want 16", i, frame.YUV[i])
			break
		}
	}
	// UV planes should be 128 (neutral chroma).
	for i := ySize; i < len(frame.YUV); i++ {
		if frame.YUV[i] != 128 {
			t.Errorf("UV[%d] = %d, want 128", i, frame.YUV[i])
			break
		}
	}
	if !frame.IsKeyframe {
		t.Error("black frame should be a keyframe")
	}
}

func TestChannel_LiveTap_GPUAlias(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		Width:  320,
		Height: 240,
		FPS:    30.0,
	})

	// Track alias calls and video output calls.
	var aliasCalls atomic.Int32
	var videoOutputCalls atomic.Int32

	// Set GPU alias that always succeeds.
	ch.SetGPUAlias(func(sourceKey string) bool {
		if sourceKey == "camera-1" {
			aliasCalls.Add(1)
			return true
		}
		return false
	})

	// Also set CPU fallback tap — should NOT be called when alias succeeds.
	ch.SetGPUSourceTap(func(sourceKey string) *DecodedFrame {
		t.Error("CPU fallback tap should not be called when GPU alias succeeds")
		return nil
	})

	// Track video output calls — should NOT be called for aliased frames.
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		videoOutputCalls.Add(1)
	})

	pl := testPlaylist(liveItem("live-1", "camera-1"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let it emit a few frames.
	time.Sleep(200 * time.Millisecond)

	aliases := aliasCalls.Load()
	if aliases < 3 {
		t.Errorf("GPU alias calls = %d, want >= 3", aliases)
	}

	// Video output should NOT have been called — GPU alias skips CPU path.
	outputs := videoOutputCalls.Load()
	if outputs != 0 {
		t.Errorf("video output calls = %d, want 0 (GPU alias should skip CPU output)", outputs)
	}

	// Advance past to complete.
	_ = ch.Next()
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_LiveTap_GPUAlias_FallbackToCPU(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		Width:  320,
		Height: 240,
		FPS:    30.0,
	})

	var cpuTapCalls atomic.Int32

	// GPU alias that always fails — source not available.
	ch.SetGPUAlias(func(sourceKey string) bool {
		return false
	})

	// CPU fallback should be called.
	tapFrame := &DecodedFrame{
		YUV:        make([]byte, 320*240*3/2),
		Width:      320,
		Height:     240,
		IsKeyframe: true,
	}
	ch.SetGPUSourceTap(func(sourceKey string) *DecodedFrame {
		if sourceKey == "camera-1" {
			cpuTapCalls.Add(1)
			return tapFrame
		}
		return nil
	})

	var frameCount atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(liveItem("live-1", "camera-1"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	cpuCalls := cpuTapCalls.Load()
	if cpuCalls < 3 {
		t.Errorf("CPU tap calls = %d, want >= 3 (fallback when GPU alias fails)", cpuCalls)
	}

	// Video output SHOULD have been called for CPU frames.
	outputs := frameCount.Load()
	if outputs < 3 {
		t.Errorf("video output calls = %d, want >= 3", outputs)
	}

	_ = ch.Next()
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}
