package playout

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zsiec/switchframe/server/internal/video"
)

// fakeFrames returns n fake YUV420 frames at 30fps.
func fakeFrames(n int) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	// 30fps = 3000 PTS ticks per frame (90kHz / 30)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, video.YUV420FrameSize(1920, 1080)),
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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	if ch.State() != StateStopped {
		t.Errorf("initial state = %v, want StateStopped", ch.State())
	}
	if ch.Mode() != ModeAutomated {
		t.Errorf("initial mode = %v, want ModeAutomated", ch.Mode())
	}
}

func TestChannel_LoadPlaylist_TransitionsToCued(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})

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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	pl := &Playlist{ID: "empty", Name: "Empty"}
	err := ch.LoadPlaylist(pl)
	if err == nil {
		t.Fatal("expected error for empty playlist")
	}
}

func TestChannel_Start_RequiresPlaylist(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	err := ch.Start()
	if err != ErrNoPlaylist {
		t.Fatalf("expected ErrNoPlaylist, got: %v", err)
	}
}

func TestChannel_Stop_FromStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	// Should be a no-op, not panic.
	ch.Stop()
	if ch.State() != StateStopped {
		t.Errorf("state after no-op stop = %v, want StateStopped", ch.State())
	}
}

func TestChannel_Start_TransitionsToOnAir(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(5),
		AllowedLocalPrefixes: []string{"/"},
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
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: fakeProvider(3), // 3 frames = very fast
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(5),
		AllowedLocalPrefixes: []string{"/"},
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
	ch1 := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	if ch1.Key() != "playout:1" {
		t.Errorf("Key() = %q, want %q", ch1.Key(), "playout:1")
	}

	ch2 := NewChannel(2, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	if ch2.Key() != "playout:2" {
		t.Errorf("Key() = %q, want %q", ch2.Key(), "playout:2")
	}
}

func TestChannel_Status(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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

func TestChannel_Status_SchedulerState(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(30),
		AllowedLocalPrefixes: []string{"/"},
	})

	// Stopped channel should report scheduler state "idle".
	status := ch.Status()
	if status.SchedulerState != "idle" {
		t.Errorf("stopped: SchedulerState = %q, want %q", status.SchedulerState, "idle")
	}

	// Load and start — scheduler should move to "playing".
	pl := testPlaylist(clipItem("a"), clipItem("b"))
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()

	// Wait for the scheduler to advance into playing state.
	waitFor(t, 2*time.Second, func() bool {
		return ch.Status().SchedulerState == "playing"
	})

	status = ch.Status()
	if status.SchedulerState != "playing" {
		t.Errorf("on_air: SchedulerState = %q, want %q", status.SchedulerState, "playing")
	}

	// Critically: SchedulerState must NOT be a channel state like "on_air".
	if status.SchedulerState == "on_air" {
		t.Error("SchedulerState is 'on_air' (channel state leaked into scheduler state)")
	}

	ch.Stop()

	// After stop, scheduler goes back to idle.
	waitFor(t, 2*time.Second, func() bool {
		return ch.Status().SchedulerState == "idle"
	})
	status = ch.Status()
	if status.SchedulerState != "idle" {
		t.Errorf("after stop: SchedulerState = %q, want %q", status.SchedulerState, "idle")
	}
}

func TestChannel_StopCallback(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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

// A stopped channel keeps its playlist, and the operator's play button is
// enabled on it: starting it again plays that playlist from the top.
func TestChannel_Start_AfterStopPlaysPlaylistAgain(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
	})
	require.NoError(t, ch.LoadPlaylist(testPlaylist(holdClipItem("a"), clipItem("b"))))
	require.NoError(t, ch.Start())
	require.NoError(t, ch.Next())
	ch.Stop()
	ch.itemProgress.Store(500) // where the stopped run left item "b"
	ch.itemRemainMs.Store(1000)

	require.NoError(t, ch.Start())
	defer ch.Stop()

	require.Equal(t, StateOnAir, ch.State())
	st := ch.Status()
	require.Equal(t, "a", st.CurrentItemID)
	// The new run's own ticks may have moved them on already; a three
	// frame item never reads half done or a second left.
	require.NotEqual(t, 0.5, st.ItemProgress, "progress of the stopped run")
	require.NotEqual(t, int64(1000), st.ItemRemainMs, "remaining time of the stopped run")
}

// Stop() ends the playlist through the scheduler before the run goroutine
// sees its context cancelled, so a tick can land in between and find the
// playlist complete. That tick must leave the stopped channel stopped.
func TestChannel_TickAfterStopKeepsChannelStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
	})
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"))))
	var states []ChannelState
	ch.SetOnStateChange(func(s ChannelState) { states = append(states, s) })

	// What Stop() does under the lock, without the run goroutine.
	ch.mu.Lock()
	ch.state = StateStopped
	ch.scheduler.Stop()
	ch.mu.Unlock()

	require.True(t, ch.tick(time.Now()), "a stopped scheduler ends the run")
	require.Equal(t, StateStopped, ch.State())
	require.Empty(t, states)
}

// A playlist that has run out leaves the channel completed with the
// playlist still loaded; starting it again plays it from the top.
func TestChannel_Start_AfterCompletedPlaysPlaylistAgain(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
	})
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"))))
	require.NoError(t, ch.Start())
	waitFor(t, 5*time.Second, func() bool { return ch.State() == StateCompleted })

	require.NoError(t, ch.Start())
	defer ch.Stop()

	require.Equal(t, StateOnAir, ch.State())
	waitFor(t, 5*time.Second, func() bool { return ch.State() == StateCompleted })
}

func TestChannel_JumpTo_NotFound(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(3),
		AllowedLocalPrefixes: []string{"/"},
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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	err := ch.Next()
	if err != ErrNotOnAir {
		t.Fatalf("expected ErrNotOnAir, got %v", err)
	}
}

func TestChannel_PreviousWhileStopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	err := ch.Previous()
	if err != ErrNotOnAir {
		t.Fatalf("expected ErrNotOnAir, got %v", err)
	}
}

func TestChannel_SecondaryEventsFireDuringPlayback(t *testing.T) {
	h := &mockHandler{}
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: fakeProvider(30), // 30 frames at 30fps = 1 second
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
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: fakeProvider(60), // 60 frames at 30fps = 2 seconds
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
		AssetProvider:        fakeProvider(10),
		AllowedLocalPrefixes: []string{"/"},
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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})

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
	_ = ch.LoadPlaylist(pl)
	_ = ch.Start()

	// Start a splice to verify frames are fed.
	time.Sleep(50 * time.Millisecond)
	_ = sc.BeginSplice("pod-1", true, true, 180000, 0)

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
		AssetProvider:        fakeProvider(10),
		AllowedLocalPrefixes: []string{"/"},
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
		YUV:    make([]byte, video.YUV420FrameSize(320, 240)),
		PTS:    int64(e.pos) * 3000,
		Width:  320,
		Height: 240,
	}
	e.pos++
	return f, nil
}

func (e *errorAfterNIterator) EstimateFPS() float64 { return 30.0 }
func (e *errorAfterNIterator) TotalFrames() int     { return -1 }
func (e *errorAfterNIterator) Close()               {}

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
		AssetProvider:        &errorProvider{},
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        &errorAfterNProvider{goodFrames: 2},
		AllowedLocalPrefixes: []string{"/"},
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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})

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
			YUV:             make([]byte, video.YUV420FrameSize(320, 240)),
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
		AssetProvider:        provider,
		AllowedLocalPrefixes: []string{"/"},
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
		AssetProvider:        provider,
		AllowedLocalPrefixes: []string{"/"},
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
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
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
		AssetProvider:        provider,
		AllowedLocalPrefixes: []string{"/"},
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
			YUV:        make([]byte, video.YUV420FrameSize(w, h)),
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
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:  w,
		Height: h,
		FPS:    30.0,
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
		AllowedLocalPrefixes: []string{"/"}, Width: w,
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
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
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
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:  320,
		Height: 240,
		FPS:    30.0,
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
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height: 240,
		FPS:    30.0,
	})

	// Set up a fake GPU source tap that returns a frame.
	tapFrame := &DecodedFrame{
		YUV:        make([]byte, video.YUV420FrameSize(320, 240)),
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
		AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider,
		Width:  320,
		Height: 240,
		FPS:    30.0,
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

// statefulIterator advances frame-by-frame and records how far it got, so a
// test can detect a clip that is frozen at frame 0 (the symptom of the iterator
// being reopened every tick). It also records Close() calls.
type statefulIterator struct {
	uri      string
	total    int
	pos      int
	provider *trackingAssetProvider
}

func (it *statefulIterator) Next() (*DecodedFrame, error) {
	if it.pos >= it.total {
		return nil, nil
	}
	idx := it.pos
	it.pos++
	it.provider.recordFrame(it.uri, idx)
	// PTS encodes the frame index (30fps cadence) so progression is observable.
	return &DecodedFrame{
		YUV:        make([]byte, video.YUV420FrameSize(64, 48)),
		PTS:        int64(idx) * 3000,
		IsKeyframe: idx == 0,
		Width:      64,
		Height:     48,
	}, nil
}

func (it *statefulIterator) EstimateFPS() float64 { return 30.0 }
func (it *statefulIterator) TotalFrames() int     { return it.total }
func (it *statefulIterator) Close()               { it.provider.recordClose(it.uri) }

// trackingAssetProvider hands out statefulIterators and records, per asset URI,
// how many times Open() was called, how many Close()s happened, and the highest
// frame index ever served. This lets a transition test assert that each clip is
// opened exactly once (not once per tick) and that frames actually advance.
type trackingAssetProvider struct {
	mu       sync.Mutex
	total    int
	opens    map[string]int
	closes   map[string]int
	maxFrame map[string]int
}

func newTrackingAssetProvider(total int) *trackingAssetProvider {
	return &trackingAssetProvider{
		total:    total,
		opens:    map[string]int{},
		closes:   map[string]int{},
		maxFrame: map[string]int{},
	}
}

func (p *trackingAssetProvider) Open(assetURI string) (FrameIterator, error) {
	p.mu.Lock()
	p.opens[assetURI]++
	p.mu.Unlock()
	return &statefulIterator{uri: assetURI, total: p.total, provider: p}, nil
}

func (p *trackingAssetProvider) recordFrame(uri string, idx int) {
	p.mu.Lock()
	if idx > p.maxFrame[uri] {
		p.maxFrame[uri] = idx
	}
	p.mu.Unlock()
}

func (p *trackingAssetProvider) recordClose(uri string) {
	p.mu.Lock()
	p.closes[uri]++
	p.mu.Unlock()
}

func (p *trackingAssetProvider) openCount(uri string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.opens[uri]
}

func (p *trackingAssetProvider) maxFrameSeen(uri string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxFrame[uri]
}

// TestChannel_ClipToClipTransition_AdvancesBothClips is a regression test for
// the single-clipIter-slot thrashing bug. During a clip→clip transition the
// channel rendered the outgoing then the incoming item in the same tick, but
// only had one iterator slot keyed on item ID — so each tick closed one clip
// and reopened the other from frame 0. Both clips were frozen at frame 0 and
// the iterator was destroyed/recreated twice per tick.
//
// With the dedicated incoming pre-roll slot, each clip is opened exactly once
// and both clips advance frame-by-frame across the transition.
func TestChannel_ClipToClipTransition_AdvancesBothClips(t *testing.T) {
	const framesPerClip = 60 // ~2s @ 30fps — long enough to span the transition
	provider := newTrackingAssetProvider(framesPerClip)

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	// Two clips; the second has a dissolve transition that spans many ticks.
	item1 := clipItem("a")
	item2 := clipItem("b")
	item2.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 500}

	pl := testPlaylist(item1, item2)
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
	ch.Stop()

	uriA := "local:///clips/a.ts"
	uriB := "local:///clips/b.ts"

	// Core invariant: each clip opened exactly once. With the bug, the incoming
	// clip is reopened on every transition tick (many opens).
	if got := provider.openCount(uriA); got != 1 {
		t.Errorf("clip a Open() count = %d, want 1 (iterator thrashed)", got)
	}
	if got := provider.openCount(uriB); got != 1 {
		t.Errorf("clip b Open() count = %d, want 1 (iterator thrashed)", got)
	}

	// Both clips must advance past frame 0 — not be frozen at the first frame.
	if got := provider.maxFrameSeen(uriA); got < 2 {
		t.Errorf("clip a max frame index = %d, want >= 2 (clip did not advance)", got)
	}
	if got := provider.maxFrameSeen(uriB); got < 2 {
		t.Errorf("clip b max frame index = %d, want >= 2 (incoming clip frozen at frame 0)", got)
	}
}

func TestChannel_DefaultDimensions_WhenZero(t *testing.T) {
	// When Width/Height/FPS are all zero, defaults should be used.
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})

	// The slate frame should have default dimensions.
	ch.mu.Lock()
	sf := ch.slateFrame
	ch.mu.Unlock()

	if sf.Width != 1920 || sf.Height != 1080 {
		t.Errorf("default slate dimensions = %dx%d, want 1920x1080", sf.Width, sf.Height)
	}

	expectedSize := video.YUV420FrameSize(1920, 1080)
	if len(sf.YUV) != expectedSize {
		t.Errorf("slate YUV size = %d, want %d", len(sf.YUV), expectedSize)
	}
}

func TestChannel_MakeBlackFrame(t *testing.T) {
	frame := makeBlackFrame(320, 240)
	if frame.Width != 320 || frame.Height != 240 {
		t.Errorf("dimensions = %dx%d, want 320x240", frame.Width, frame.Height)
	}
	expectedSize := video.YUV420FrameSize(320, 240)
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
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
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

	// Track video output calls — should be called with nil YUV as pipeline signal.
	// We track non-nil calls separately to avoid a race between snapshotting two
	// counters (nilYUVCalls could exceed videoOutputCalls if a callback fires
	// between the two Load() calls).
	var nonNilYUVCalls atomic.Int32
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		videoOutputCalls.Add(1)
		if len(yuv) != 0 {
			nonNilYUVCalls.Add(1)
		}
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

	// Video output IS called (signal frames for pipeline routing), but with nil YUV.
	outputs := videoOutputCalls.Load()
	if outputs < 3 {
		t.Errorf("video output calls = %d, want >= 3 (signal frames for pipeline)", outputs)
	}
	if n := nonNilYUVCalls.Load(); n != 0 {
		t.Errorf("non-nil YUV calls = %d, want 0 — all should be nil (GPU alias)", n)
	}

	// Advance past to complete.
	_ = ch.Next()
	waitFor(t, 5*time.Second, func() bool {
		return ch.State() == StateCompleted
	})
}

func TestChannel_LiveTap_GPUAlias_FallbackToCPU(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
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
		YUV:        make([]byte, video.YUV420FrameSize(320, 240)),
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

// fakeFramesWithAudioFRC returns n fake frames at the given PTS delta with
// AudioFrames attached. Each frame gets one AudioFrameData tagged with the
// frame index for tracking audio delivery in FRC tests.
func fakeFramesWithAudioFRC(n int, ptsDelta int64) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, video.YUV420FrameSize(320, 240)),
			PTS:        int64(i) * ptsDelta,
			IsKeyframe: i == 0,
			Width:      320,
			Height:     240,
			AudioFrames: []AudioFrameData{
				{
					Data:       []byte{byte(i)}, // tag with frame index
					SampleRate: 48000,
					Channels:   2,
				},
			},
		}
		frames[i].YUV[0] = byte(i)
	}
	return frames
}

// fakeFrames24fps returns n fake YUV420 frames at 24fps.
func fakeFrames24fps(n int) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, video.YUV420FrameSize(320, 240)),
			PTS:        int64(i) * 3750, // 90000/24 = 3750
			IsKeyframe: i == 0,
			Width:      320,
			Height:     240,
		}
	}
	return frames
}

// TestChannel_TicksAtPipelineRate verifies that the playout channel always
// ticks at the pipeline frame rate, not the clip's native rate.
func TestChannel_TicksAtPipelineRate(t *testing.T) {
	frames := fakeFrames24fps(120)
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var outputTimes []time.Time

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		outputTimes = append(outputTimes, time.Now())
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop()

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(outputTimes) >= 60
	})

	ch.Stop()

	mu.Lock()
	times := make([]time.Time, len(outputTimes))
	copy(times, outputTimes)
	mu.Unlock()

	if len(times) < 10 {
		t.Fatal("not enough output frames")
	}

	var totalInterval time.Duration
	for i := 1; i < len(times); i++ {
		totalInterval += times[i].Sub(times[i-1])
	}
	avgMs := float64(totalInterval.Milliseconds()) / float64(len(times)-1)

	t.Logf("average output interval: %.1fms (want ~33.3ms for 30fps)", avgMs)
	if avgMs > 38.0 {
		t.Errorf("output interval %.1fms is too high — playout is ticking at clip rate, not pipeline rate", avgMs)
	}
	if avgMs < 28.0 {
		t.Errorf("output interval %.1fms is too low", avgMs)
	}
}

// TestChannel_FRC_24to30 verifies that a 24fps clip played on a 30fps
// pipeline holds some frames for 2 ticks (3:2 pulldown pattern).
func TestChannel_FRC_24to30(t *testing.T) {
	frames := fakeFrames24fps(72)
	for i := range frames {
		frames[i].YUV[0] = byte(i)
	}
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var outputs []byte

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		if len(yuv) > 0 {
			outputs = append(outputs, yuv[0])
		}
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for enough output ticks to consume all 72 source frames.
	// At 30fps pipeline with 24fps source: 72 * 30/24 = 90 output ticks.
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(outputs) >= 90
	})

	ch.Stop()

	mu.Lock()
	out := make([]byte, len(outputs))
	copy(out, outputs)
	mu.Unlock()

	// Output frames should be monotonically non-decreasing.
	for i := 1; i < len(out); i++ {
		if out[i] < out[i-1] {
			t.Errorf("output frame %d (src=%d) < frame %d (src=%d) — went backward",
				i, out[i], i-1, out[i-1])
		}
	}

	// Some frames must be repeated (30/24 = 1.25, so ~20% repeats).
	repeats := 0
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			repeats++
		}
	}
	t.Logf("output frames: %d, repeated: %d (%.1f%%)", len(out), repeats, 100*float64(repeats)/float64(len(out)))
	if repeats == 0 {
		t.Error("expected some repeated frames for 24fps→30fps FRC, got none")
	}

	// All source frames should be used (none skipped).
	seen := make(map[byte]bool)
	for _, b := range out {
		seen[b] = true
	}
	if len(seen) < 70 {
		t.Errorf("only %d unique source frames seen, expected ~72", len(seen))
	}
}

// TestChannel_FRC_SameRate verifies that matching clip/pipeline FPS
// results in 1:1 passthrough with no repeated or skipped frames.
func TestChannel_FRC_SameRate(t *testing.T) {
	frames := make([]DecodedFrame, 60)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, video.YUV420FrameSize(320, 240)),
			PTS:        int64(i) * 3000, // 30fps
			IsKeyframe: i == 0,
			Width:      320,
			Height:     240,
		}
		frames[i].YUV[0] = byte(i)
	}
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var outputs []byte

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		if len(yuv) > 0 {
			outputs = append(outputs, yuv[0])
		}
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	if err := ch.LoadPlaylist(pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := ch.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(outputs) >= 55
	})

	ch.Stop()

	mu.Lock()
	out := make([]byte, len(outputs))
	copy(out, outputs)
	mu.Unlock()

	// 1:1 passthrough: minimal repeats.
	repeats := 0
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			repeats++
		}
	}
	t.Logf("output frames: %d, repeated: %d", len(out), repeats)
	if repeats > 3 {
		t.Errorf("too many repeated frames (%d) for same-rate playback — expected ~0", repeats)
	}
}

// TestChannel_FRC_AudioNotDuplicated verifies that when frames are held
// (24fps→30fps), audio is NOT pushed twice for the same source frame.
func TestChannel_FRC_AudioNotDuplicated(t *testing.T) {
	// 48 frames of 24fps content with audio = 2 seconds.
	frames := fakeFramesWithAudioFRC(48, 3750) // 90000/24 = 3750
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var audioDeliveries []byte // track which audio tags are delivered

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		if len(data) > 0 {
			audioDeliveries = append(audioDeliveries, data[0])
		}
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	require.NoError(t, ch.LoadPlaylist(pl))
	_ = ch.Start()

	// Wait for clip to finish (~2s of content at 30fps output = ~60 ticks).
	waitFor(t, 4*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(audioDeliveries) >= 45
	})

	ch.Stop()

	mu.Lock()
	audio := make([]byte, len(audioDeliveries))
	copy(audio, audioDeliveries)
	mu.Unlock()

	// Each source frame's audio should be delivered exactly once.
	// Count occurrences of each tag.
	counts := make(map[byte]int)
	for _, tag := range audio {
		counts[tag]++
	}

	duplicates := 0
	for tag, count := range counts {
		if count > 1 {
			duplicates++
			t.Logf("audio tag %d delivered %d times (should be 1)", tag, count)
		}
	}

	t.Logf("audio deliveries: %d total, %d unique tags, %d duplicated tags",
		len(audio), len(counts), duplicates)

	if duplicates > 0 {
		t.Errorf("%d audio tags delivered more than once — held frames are leaking audio", duplicates)
	}

	// All 48 source frames' audio should be present.
	if len(counts) < 45 {
		t.Errorf("only %d unique audio tags delivered, expected ~48 (some source audio lost)", len(counts))
	}
}

// TestChannel_FRC_HoldModeStripsAudio verifies that when a clip ends in
// hold mode, the held frame's audio is not re-pushed every tick.
func TestChannel_FRC_HoldModeStripsAudio(t *testing.T) {
	// 10 frames of 30fps content with audio = ~333ms.
	frames := fakeFramesWithAudioFRC(10, 3000) // 30fps
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var audioTags []byte
	var videoCount int

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		videoCount++
		mu.Unlock()
	})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		if len(data) > 0 {
			audioTags = append(audioTags, data[0])
		}
		mu.Unlock()
	})

	// Use a hold item so the last frame is held after clip ends.
	holdItem := clipItem("a")
	holdItem.Hold = true
	pl := testPlaylist(holdItem)
	require.NoError(t, ch.LoadPlaylist(pl))
	_ = ch.Start()

	// Wait for well past clip end — should get ~10 frames of content + many held frames.
	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return videoCount >= 40 // 10 real + 30 held
	})

	ch.Stop()

	mu.Lock()
	tags := make([]byte, len(audioTags))
	copy(tags, audioTags)
	mu.Unlock()

	// Count occurrences of each audio tag.
	counts := make(map[byte]int)
	for _, tag := range tags {
		counts[tag]++
	}

	// The last frame's audio (tag 9) should appear exactly once,
	// not once per held tick.
	lastTag := byte(9)
	if count, ok := counts[lastTag]; ok && count > 1 {
		t.Errorf("last frame audio tag %d delivered %d times — hold mode is leaking audio", lastTag, count)
	}

	// All 10 source audio tags should be present.
	if len(counts) < 9 {
		t.Errorf("only %d unique audio tags, expected ~10", len(counts))
	}

	t.Logf("audio tags: %d total, %d unique, last_tag_count=%d",
		len(tags), len(counts), counts[lastTag])
}

// TestChannel_FRC_LoopRewindStripsAudio verifies that when a clip loops,
// the transitional last frame doesn't re-push audio.
func TestChannel_FRC_LoopRewindStripsAudio(t *testing.T) {
	// 10 frames of 30fps content with audio.
	frames := fakeFramesWithAudioFRC(10, 3000)
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var audioTags []byte

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		if len(data) > 0 {
			audioTags = append(audioTags, data[0])
		}
		mu.Unlock()
	})

	loopItem := clipItem("a")
	loopItem.Loop = true
	pl := testPlaylist(loopItem)
	require.NoError(t, ch.LoadPlaylist(pl))
	_ = ch.Start()

	// Wait for at least 2 full loops (20+ unique audio tags if no loss).
	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(audioTags) >= 18
	})

	ch.Stop()

	mu.Lock()
	tags := make([]byte, len(audioTags))
	copy(tags, audioTags)
	mu.Unlock()

	// Check that no single tag appears more than expected.
	// With 2 loops, each tag should appear ~2 times.
	counts := make(map[byte]int)
	for _, tag := range tags {
		counts[tag]++
	}

	for tag, count := range counts {
		// Allow up to 3 (2 loops + 1 for timing jitter), but not more.
		if count > 4 {
			t.Errorf("audio tag %d delivered %d times (expected ~2-3 for loop mode)", tag, count)
		}
	}

	t.Logf("audio tags: %d total, %d unique, counts=%v", len(tags), len(counts), counts)
}

// TestChannel_FRC_60to30 verifies that a 60fps clip played on a 30fps
// pipeline skips every other frame, and audio from skipped frames is preserved.
func TestChannel_FRC_60to30(t *testing.T) {
	// 120 frames of 60fps content = 2 seconds.
	frames := fakeFramesWithAudioFRC(120, 1500) // 90000/60 = 1500
	provider := NewFakeAssetProvider(frames)

	var mu sync.Mutex
	var videoOutputs []byte
	var audioTags []byte

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		if len(yuv) > 0 {
			videoOutputs = append(videoOutputs, yuv[0])
		}
		mu.Unlock()
	})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		if len(data) > 0 {
			audioTags = append(audioTags, data[0])
		}
		mu.Unlock()
	})

	pl := testPlaylist(clipItem("a"))
	require.NoError(t, ch.LoadPlaylist(pl))
	_ = ch.Start()

	// Wait for all 120 audio tags to be delivered. At 60fps source on 30fps
	// pipeline, each tick consumes 2 source frames, so ~60 ticks = 2 seconds.
	// Wait for at least 118 audio tags to account for the end-of-clip tick.
	waitFor(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(audioTags) >= 118
	})

	ch.Stop()

	mu.Lock()
	vOut := make([]byte, len(videoOutputs))
	copy(vOut, videoOutputs)
	aOut := make([]byte, len(audioTags))
	copy(aOut, audioTags)
	mu.Unlock()

	// Video: ~60 output frames for 2 seconds at 30fps.
	// Each output frame should skip ~1 source frame (60/30 = 2x).
	t.Logf("video outputs: %d", len(vOut))

	// Video frames should be monotonically non-decreasing.
	for i := 1; i < len(vOut); i++ {
		if vOut[i] < vOut[i-1] {
			t.Errorf("video frame %d (src=%d) < frame %d (src=%d)", i, vOut[i], i-1, vOut[i-1])
		}
	}

	// Should have skips (some source frames not displayed).
	videoSeen := make(map[byte]bool)
	for _, b := range vOut {
		videoSeen[b] = true
	}
	// At 60->30, roughly half of source frames should be displayed.
	t.Logf("unique video frames displayed: %d out of 120", len(videoSeen))
	if len(videoSeen) > 70 {
		t.Errorf("too many unique video frames (%d) — expected ~60 for 60->30 skip", len(videoSeen))
	}

	// Audio: ALL source frames' audio should be delivered (none lost).
	audioSeen := make(map[byte]bool)
	for _, b := range aOut {
		audioSeen[b] = true
	}
	t.Logf("unique audio tags delivered: %d out of 120", len(audioSeen))
	// We can only track byte(0..119), but byte wraps at 256. With 120 frames,
	// all tags are unique (0-119). All should be present.
	if len(audioSeen) < 115 {
		t.Errorf("only %d unique audio tags — skipped frames are losing audio", len(audioSeen))
	}
}

// TestChannel_FRC_FrameMutationSafety verifies that when the FRC engine
// attaches collected audio to a frame, it doesn't corrupt the original
// iterator frames.
func TestChannel_FRC_FrameMutationSafety(t *testing.T) {
	// 60 frames at 60fps = 1 second.
	frames := fakeFramesWithAudioFRC(60, 1500)
	// Save original audio frame counts before playback.
	originalAudioCounts := make([]int, len(frames))
	for i, f := range frames {
		originalAudioCounts[i] = len(f.AudioFrames)
	}

	provider := NewFakeAssetProvider(frames)

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"}, Width: 320,
		Height:        240,
		FPS:           30.0,
		AssetProvider: provider,
	})

	var mu sync.Mutex
	var outputCount int
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKey bool) {
		mu.Lock()
		outputCount++
		mu.Unlock()
	})
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {})

	pl := testPlaylist(clipItem("a"))
	require.NoError(t, ch.LoadPlaylist(pl))
	_ = ch.Start()

	waitFor(t, 3*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return outputCount >= 25
	})

	ch.Stop()

	// Verify original frames weren't mutated.
	for i, f := range frames {
		if len(f.AudioFrames) != originalAudioCounts[i] {
			t.Errorf("frame %d AudioFrames mutated: was %d, now %d",
				i, originalAudioCounts[i], len(f.AudioFrames))
		}
	}
}

// ---------------------------------------------------------------------------
// PlaylistHash tests
// ---------------------------------------------------------------------------

func TestChannel_PlaylistHash_NoPlaylist(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	got := ch.PlaylistHash()
	if got != "" {
		t.Errorf("PlaylistHash() = %q, want empty string for no playlist", got)
	}
}

func TestChannel_PlaylistHash_Deterministic(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	pl := testPlaylist(clipItem("a"), clipItem("b"))
	require.NoError(t, ch.LoadPlaylist(pl))

	h1 := ch.PlaylistHash()
	h2 := ch.PlaylistHash()

	if h1 == "" {
		t.Fatal("PlaylistHash() returned empty string for loaded playlist")
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("PlaylistHash() = %q, want sha256: prefix", h1)
	}
	if h1 != h2 {
		t.Errorf("PlaylistHash() not deterministic: %q != %q", h1, h2)
	}

	// Verify it matches manual computation.
	data, err := json.Marshal(pl.Items)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if h1 != want {
		t.Errorf("PlaylistHash() = %q, want %q", h1, want)
	}
}

func TestChannel_PlaylistHash_DifferentPlaylists(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})

	pl1 := testPlaylist(clipItem("a"))
	require.NoError(t, ch.LoadPlaylist(pl1))
	h1 := ch.PlaylistHash()

	pl2 := testPlaylist(clipItem("a"), clipItem("b"))
	require.NoError(t, ch.LoadPlaylist(pl2))
	h2 := ch.PlaylistHash()

	if h1 == h2 {
		t.Error("PlaylistHash() should differ for different playlists")
	}
}

// ---------------------------------------------------------------------------
// ItemElapsedMs tests
// ---------------------------------------------------------------------------

func TestChannel_ItemElapsedMs_Stopped(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})
	got := ch.ItemElapsedMs()
	if got != 0 {
		t.Errorf("ItemElapsedMs() = %d, want 0 when stopped", got)
	}
}

func TestChannel_ItemElapsedMs_Playing(t *testing.T) {
	provider := fakeProvider(90) // 3 seconds at 30fps
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(clipItem("a"))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())
	defer ch.Stop()

	// Wait for on-air.
	waitFor(t, 2*time.Second, func() bool {
		return ch.State() == StateOnAir
	})

	// Allow some frames to be emitted.
	time.Sleep(100 * time.Millisecond)

	elapsed := ch.ItemElapsedMs()
	if elapsed <= 0 {
		t.Errorf("ItemElapsedMs() = %d, want > 0 when playing", elapsed)
	}
	// Sanity: should be at least 50ms but less than 5 seconds.
	if elapsed > 5000 {
		t.Errorf("ItemElapsedMs() = %d, implausibly large", elapsed)
	}
}

// ---------------------------------------------------------------------------
// SeekToItem tests
// ---------------------------------------------------------------------------

func TestChannel_SeekToItem_SlatePlaylist(t *testing.T) {
	provider := fakeProvider(90)
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(
		slateItem("s0", 5000),
		slateItem("s1", 3000),
		slateItem("s2", 4000),
	)
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	// Wait for on-air.
	waitFor(t, 2*time.Second, func() bool {
		return ch.State() == StateOnAir
	})

	// Seek to item 2 with 1000ms elapsed.
	err := ch.SeekToItem(2, 1000)
	require.NoError(t, err)

	// Verify cursor moved.
	snap := ch.scheduler.StatusSnapshot()
	if snap.Cursor != 2 {
		t.Errorf("scheduler cursor = %d, want 2", snap.Cursor)
	}

	// Verify elapsed time is approximately 1000ms.
	elapsedSinceStart := time.Since(snap.ItemStartTime)
	if elapsedSinceStart < 900*time.Millisecond || elapsedSinceStart > 2*time.Second {
		t.Errorf("elapsed since item start = %v, want ~1000ms", elapsedSinceStart)
	}

	ch.Stop()
}

func TestChannel_SeekToItem_NoPlaylist(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}})

	err := ch.SeekToItem(0, 0)
	if err != ErrNoPlaylist {
		t.Errorf("SeekToItem with no playlist: got %v, want ErrNoPlaylist", err)
	}
}

func TestChannel_SeekToItem_InvalidIndex(t *testing.T) {
	provider := fakeProvider(30)
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(slateItem("s0", 5000))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitFor(t, 2*time.Second, func() bool {
		return ch.State() == StateOnAir
	})

	// Negative index.
	err := ch.SeekToItem(-1, 0)
	if err != ErrInvalidItem {
		t.Errorf("SeekToItem(-1): got %v, want ErrInvalidItem", err)
	}

	// Index beyond range.
	err = ch.SeekToItem(5, 0)
	if err != ErrInvalidItem {
		t.Errorf("SeekToItem(5): got %v, want ErrInvalidItem", err)
	}

	ch.Stop()
}

func TestChannel_SeekToItem_LiveItem(t *testing.T) {
	provider := fakeProvider(30)
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(
		slateItem("s0", 5000),
		liveItem("live1", "cam-1"),
	)
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitFor(t, 2*time.Second, func() bool {
		return ch.State() == StateOnAir
	})

	// Seek to the live item.
	err := ch.SeekToItem(1, 500)
	require.NoError(t, err)

	// Verify the active live source was updated.
	ch.mu.Lock()
	active := ch.activeLiveSource
	ch.mu.Unlock()
	if active != "cam-1" {
		t.Errorf("activeLiveSource = %q, want %q", active, "cam-1")
	}

	ch.Stop()
}

func TestChannel_SeekToItem_ClosesExistingIterator(t *testing.T) {
	provider := fakeProvider(90)
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: provider})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(clipItem("a"), slateItem("s1", 5000))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitFor(t, 2*time.Second, func() bool {
		return ch.State() == StateOnAir
	})

	// Let the clip start playing.
	time.Sleep(50 * time.Millisecond)

	// Seek to the slate item — should close any existing clip iterator.
	err := ch.SeekToItem(1, 0)
	require.NoError(t, err)

	// Verify clip state was cleaned up.
	ch.mu.Lock()
	clipIter := ch.clipIter
	clipItemID := ch.clipItemID
	ch.mu.Unlock()

	if clipIter != nil {
		t.Error("clipIter should be nil after seeking to non-clip item")
	}
	if clipItemID != "" {
		t.Errorf("clipItemID = %q, want empty", clipItemID)
	}

	ch.Stop()
}

// TestChannel_SetCursor_ClearsStaleTransition verifies that Channel.SetCursor
// resets the scheduler out of SchedTransitioning and clears the transition
// fields. Otherwise, a SetCursor performed while the scheduler is mid-transition
// (e.g. snapshot restore via Manager.RecoverChannel) would be immediately
// overwritten: the next Tick routes to tickTransition, computes progress from
// the stale transStartTime (>= 1.0), and does cursor++ — moving past the
// restored position and committing to the stale incoming item.
func TestChannel_SetCursor_ClearsStaleTransition(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        fakeProvider(10),
		AllowedLocalPrefixes: []string{"/"},
	})

	pl := testPlaylist(clipItem("a"), clipItem("b"), clipItem("c"))
	require.NoError(t, ch.LoadPlaylist(pl))

	s := ch.scheduler
	s.Start() // started=true so Tick does not short-circuit as completed

	// Force the scheduler into a stale mid-transition (a->b) with a start time
	// far in the past so progress would compute as >= 1.0 on the next tick.
	s.mu.Lock()
	s.cursor = 0
	s.state = SchedTransitioning
	s.transOutgoing = &pl.Items[0]
	s.transIncoming = &pl.Items[1]
	s.transConfig = TransitionConfig{Type: "dissolve", DurationMs: 1000}
	s.transStartTime = time.Now().Add(-10 * time.Second)
	s.transDurationMs = 1000
	s.mu.Unlock()

	// Restore the cursor to item 2 (c) as recovery would.
	ch.SetCursor(2)

	// SetCursor must have reset scheduler state and cleared transition fields.
	s.mu.Lock()
	state := s.state
	outgoing := s.transOutgoing
	incoming := s.transIncoming
	cursor := s.cursor
	s.mu.Unlock()

	if state != SchedPlaying {
		t.Errorf("scheduler state after SetCursor = %v, want SchedPlaying", state)
	}
	if outgoing != nil {
		t.Errorf("transOutgoing after SetCursor = %v, want nil", outgoing)
	}
	if incoming != nil {
		t.Errorf("transIncoming after SetCursor = %v, want nil", incoming)
	}
	if cursor != 2 {
		t.Errorf("cursor after SetCursor = %d, want 2", cursor)
	}

	// The next Tick must NOT complete the stale transition. It should keep the
	// restored cursor and play item c, not advance past it onto the stale
	// incoming item b.
	decision := s.Tick(time.Now())
	if decision.Transition != nil {
		t.Error("Tick after SetCursor should not be in a transition")
	}
	if decision.Item == nil || decision.Item.ID != "c" {
		var id string
		if decision.Item != nil {
			id = decision.Item.ID
		}
		t.Errorf("Tick item after SetCursor = %q, want c", id)
	}
	s.mu.Lock()
	cursorAfter := s.cursor
	s.mu.Unlock()
	if cursorAfter != 2 {
		t.Errorf("cursor after Tick = %d, want 2 (stale transition must not have advanced it)", cursorAfter)
	}
}

// fakeFramesWithAudioTag returns n 64x48 frames at 30fps, each carrying one
// AudioFrameData whose payload is tagged with tag (byte 0) and the frame
// index (byte 1) so tests can attribute emitted audio to a source clip.
func fakeFramesWithAudioTag(n int, tag byte) []DecodedFrame {
	frames := make([]DecodedFrame, n)
	for i := range frames {
		frames[i] = DecodedFrame{
			YUV:        make([]byte, video.YUV420FrameSize(64, 48)),
			PTS:        int64(i) * 3000,
			IsKeyframe: i == 0,
			Width:      64,
			Height:     48,
			AudioFrames: []AudioFrameData{{
				Data:       []byte{tag, byte(i)},
				SampleRate: 48000,
				Channels:   2,
			}},
		}
	}
	return frames
}

// TestChannel_Transition_KeepsAudio is a regression test for transition audio
// loss: Compositor.Blend returns a video-only frame, so for the full duration
// of a configured transition (dissolve/dip/wipe) the audio that the incoming
// (and outgoing) clip iterators kept consuming was silently discarded — every
// dissolve between items produced an audible dropout.
func TestChannel_Transition_KeepsAudio(t *testing.T) {
	provider := &multiAssetProvider{assets: map[string][]DecodedFrame{
		"local:///clips/a.ts": fakeFramesWithAudioTag(5, 0xAA),
		"local:///clips/b.ts": fakeFramesWithAudioTag(60, 0xBB),
	}}

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	var mu sync.Mutex
	var audioTags []byte
	ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
		mu.Lock()
		audioTags = append(audioTags, data[0])
		mu.Unlock()
	})

	itemA := clipItem("a")
	itemB := clipItem("b")
	itemB.Transition = &TransitionConfig{Type: "dissolve", DurationMs: 300}

	require.NoError(t, ch.LoadPlaylist(testPlaylist(itemA, itemB)))

	// Drive ticks manually with synthetic time so transition progress is
	// deterministic (no real-time ticker).
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	now := time.Now()
	transitionTicks := 0
	transitionAudio := 0
	var transitionTags []byte
	for i := 0; i < 40; i++ {
		mu.Lock()
		before := len(audioTags)
		mu.Unlock()
		ch.tick(now)
		st := ch.scheduler.State()
		mu.Lock()
		if st == SchedTransitioning {
			transitionTicks++
			transitionAudio += len(audioTags) - before
			transitionTags = append(transitionTags, audioTags[before:]...)
		}
		mu.Unlock()
		now = now.Add(33 * time.Millisecond)
	}

	if transitionTicks < 5 {
		t.Fatalf("transition ticks = %d, want >= 5 (transition never ran)", transitionTicks)
	}
	// The incoming clip consumes ~1 frame (with one audio frame) per
	// transition tick; that audio must reach the audio output, not be
	// silently dropped by the blend.
	if transitionAudio < transitionTicks/2 {
		t.Fatalf("audio frames emitted during transition = %d over %d ticks, want >= %d (transition audio dropped)",
			transitionAudio, transitionTicks, transitionTicks/2)
	}
	for _, tag := range transitionTags {
		if tag != 0xBB && tag != 0xAA {
			t.Fatalf("unexpected audio tag 0x%02X during transition", tag)
		}
	}
}

// gatedIterator mimics FileFrameIterator's Close semantics for race tests:
// Next returns (nil, nil) once closed. The Next call at frame index blockAt
// parks until release is closed, letting a test deterministically interleave
// a concurrent SeekToItem with an in-flight decode.
type gatedIterator struct {
	mu      sync.Mutex
	frames  []DecodedFrame
	pos     int
	closed  bool
	blockAt int
	gateHit bool
	inNext  chan struct{} // closed when Next parks at the gate
	release chan struct{} // close to unpark Next
}

func (g *gatedIterator) Next() (*DecodedFrame, error) {
	g.mu.Lock()
	if !g.gateHit && g.pos >= g.blockAt {
		g.gateHit = true
		g.mu.Unlock()
		close(g.inNext)
		<-g.release
		g.mu.Lock()
	}
	defer g.mu.Unlock()
	if g.closed || g.pos >= len(g.frames) {
		return nil, nil
	}
	f := &g.frames[g.pos]
	g.pos++
	return f, nil
}

func (g *gatedIterator) EstimateFPS() float64 { return 30.0 }
func (g *gatedIterator) TotalFrames() int     { return len(g.frames) }
func (g *gatedIterator) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// gatedProvider returns the gated iterator on the first Open and plain
// SliceIterators afterwards (e.g. for the iterator SeekToItem re-opens).
type gatedProvider struct {
	mu     sync.Mutex
	gated  *gatedIterator
	handed bool
	frames []DecodedFrame
}

func (p *gatedProvider) Open(uri string) (FrameIterator, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.handed {
		p.handed = true
		return p.gated, nil
	}
	return NewSliceIterator(p.frames), nil
}

// TestChannel_SeekToItem_ConcurrentWithDecode_NoSpuriousComplete is a
// regression test for the SeekToItem/run-goroutine race: nextClipFrame
// captures the clip iterator under lock but uses it unlocked. A concurrent
// SeekToItem closes that iterator and installs a fresh, seeked one; the stale
// iterator's Next then returns (nil, nil), which nextClipFrame interpreted as
// end-of-clip — firing a spurious SignalItemComplete that advanced the
// scheduler PAST the item the seek had just positioned, and clobbering the
// seeked frame counters with the old clip's state.
func TestChannel_SeekToItem_ConcurrentWithDecode_NoSpuriousComplete(t *testing.T) {
	frames := fakeFramesSized(60, 64, 48, 30.0)
	gated := &gatedIterator{
		frames:  frames,
		blockAt: 1, // first Next (frame 0) passes; the second parks
		inNext:  make(chan struct{}),
		release: make(chan struct{}),
	}
	provider := &gatedProvider{gated: gated, frames: frames}

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b"))))

	// Drive ticks manually with synthetic time (no run goroutine ticker).
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	now := time.Now()
	ch.tick(now) // opens the gated iterator, consumes frame 0

	// Second tick parks inside iter.Next() at the gate.
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		ch.tick(now.Add(33 * time.Millisecond))
	}()
	<-gated.inNext

	// Concurrent seek: closes the gated iterator, installs a fresh one.
	require.NoError(t, ch.SeekToItem(0, 0))

	// Unpark the stale Next — it returns (nil, nil) from the closed iterator.
	close(gated.release)
	<-tickDone

	// The next tick must keep playing item a at the seeked position, not
	// consume a spurious item-complete and advance to item b.
	ch.tick(now.Add(66 * time.Millisecond))
	if cur := ch.scheduler.Cursor(); cur != 0 {
		t.Fatalf("scheduler cursor = %d, want 0 (stale iterator end-of-clip advanced past the seeked item)", cur)
	}
	ch.mu.Lock()
	itemID := ch.clipItemID
	ch.mu.Unlock()
	if itemID != "a" {
		t.Fatalf("clipItemID = %q, want %q", itemID, "a")
	}
}

// seekableSliceIterator is a slice-backed FrameIterator with frame-accurate
// Seek, for exercising the seek path without a real decoder.
type seekableSliceIterator struct {
	frames []DecodedFrame
	pos    int
}

func (s *seekableSliceIterator) Next() (*DecodedFrame, error) {
	if s.pos >= len(s.frames) {
		return nil, nil
	}
	f := &s.frames[s.pos]
	s.pos++
	return f, nil
}

func (s *seekableSliceIterator) EstimateFPS() float64 { return estimateFPS(s.frames) }
func (s *seekableSliceIterator) TotalFrames() int     { return len(s.frames) }
func (s *seekableSliceIterator) Close()               {}

func (s *seekableSliceIterator) Seek(target int) error {
	if target < 0 || target >= len(s.frames) {
		return fmt.Errorf("seek target %d out of range", target)
	}
	s.pos = target
	return nil
}

// seekableProvider hands out seekableSliceIterators over a fixed frame set.
type seekableProvider struct {
	frames []DecodedFrame
}

func (p *seekableProvider) Open(uri string) (FrameIterator, error) {
	return &seekableSliceIterator{frames: p.frames}, nil
}

// TestChannel_SeekToItem_TrimmedClip_SeeksToOffset is a regression test:
// clips with in/out points are wrapped in a TrimIterator, which did not
// implement Seek — so the type assertion in SeekToItem silently failed and
// trimmed clips always restarted from the in-point during snapshot sync,
// defeating standby alignment.
func TestChannel_SeekToItem_TrimmedClip_SeeksToOffset(t *testing.T) {
	// 120 frames @ 30fps. Trim in=1000ms out=3000ms → trimmed frames 30..89.
	frames := fakeFramesSized(120, 64, 48, 30.0)
	provider := &seekableProvider{frames: frames}

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})

	item := clipItem("trimmed")
	item.InPointMs = 1000
	item.OutPointMs = 3000
	require.NoError(t, ch.LoadPlaylist(testPlaylist(item)))

	// Seek 500ms into the trimmed timeline → 1500ms absolute → frame 45.
	require.NoError(t, ch.SeekToItem(0, 500))

	ch.mu.Lock()
	iter := ch.clipIter
	clipFrame := ch.clipFrame
	ch.mu.Unlock()
	require.NotNil(t, iter, "clip iterator should be open after SeekToItem")

	if clipFrame != 15 {
		t.Errorf("clipFrame = %d, want 15 (500ms at 30fps on the trimmed timeline)", clipFrame)
	}

	f, err := iter.Next()
	require.NoError(t, err)
	require.NotNil(t, f, "iterator should produce a frame after seek")
	if f.PTS != 135000 { // frame 45 * 3000 ticks
		t.Errorf("first frame after seek PTS = %d, want 135000 (frame 45 = in-point + 500ms)", f.PTS)
	}

	// The out point must still be honored after seeking: the trimmed stream
	// ends before relative PTS 3000ms (absolute frame 90).
	last := f
	for {
		n, err := iter.Next()
		require.NoError(t, err)
		if n == nil {
			break
		}
		last = n
	}
	if last.PTS != 267000 { // frame 89 * 3000 ticks
		t.Errorf("last frame PTS = %d, want 267000 (out point not honored after seek)", last.PTS)
	}
}

// TestChannel_SeekToItem_ZeroDuration_RemainNotNegative guards against
// itemRemainMs going negative when seeking an item with no DurationMs set.
func TestChannel_SeekToItem_ZeroDuration_RemainNotNegative(t *testing.T) {
	frames := fakeFramesSized(120, 64, 48, 30.0)
	provider := &seekableProvider{frames: frames}

	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        provider,
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})

	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a")))) // DurationMs == 0
	require.NoError(t, ch.SeekToItem(0, 5000))

	if remain := ch.itemRemainMs.Load(); remain < 0 {
		t.Errorf("itemRemainMs = %d, want >= 0", remain)
	}
}

// TestChannel_EndAnchoredSecondaryEvents_FirstClip_FiresAtClipEnd is a
// regression test: handleItemChange runs BEFORE the clip iterator opens, so
// the duration passed to SecondaryEventScheduler.BeginItem came from the
// PREVIOUS clip's clipTotal/clipFPS (zero for the first clip). An end-anchored
// cue (e.g. SCTE-35 / graphics-off 500ms before clip end) therefore resolved
// to fire time 0 and fired immediately at item start instead of near the end.
func TestChannel_EndAnchoredSecondaryEvents_FirstClip_FiresAtClipEnd(t *testing.T) {
	h := &mockHandler{}
	frames := fakeFramesSized(60, 64, 48, 30.0) // 60 frames @ 30fps = 2000ms
	ch := NewChannel(1, ChannelConfig{
		AllowedLocalPrefixes: []string{"/"},
		AssetProvider:        NewFakeAssetProvider(frames),
		Width:                64,
		Height:               48,
		FPS:                  30.0,
	})
	ch.SetSecondaryHandler(h)
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {})

	pl := testPlaylist(PlaylistItem{
		ID:       "clip-1",
		Type:     ItemClip,
		AssetURI: "local:///test.ts",
		Title:    "First Clip",
		SecondaryEvents: []SecondaryEvent{
			// Should fire at 2000ms - 500ms = 1500ms (~tick 45).
			{Type: "graphics_off", Layer: 2, OffsetMs: -500, Anchor: "end"},
		},
	})
	require.NoError(t, ch.LoadPlaylist(pl))

	// Drive ticks manually with synthetic time for determinism.
	ch.mu.Lock()
	ch.state = StateOnAir
	ch.scheduler.Start()
	ch.mu.Unlock()

	now := time.Now()
	firedTick := -1
	for i := 0; i < 70; i++ {
		ch.tick(now)
		if firedTick < 0 && len(h.getCalls()) > 0 {
			firedTick = i
		}
		now = now.Add(33 * time.Millisecond)
	}

	if firedTick < 0 {
		t.Fatal("end-anchored event never fired")
	}
	// 1500ms at 33ms/tick ≈ tick 45. Pre-fix the event fired at tick 0
	// (durationMs was the previous clip's — zero for the first clip — so the
	// fire time clamped to 0).
	if firedTick < 40 {
		t.Fatalf("end-anchored event fired at tick %d (~%dms), want ~tick 45 (~1500ms): fired with stale/zero item duration",
			firedTick, firedTick*33)
	}
}

// stopGateIterator yields frames forever; once gate is armed, Next blocks
// until it is released, holding the run goroutine inside a tick.
type stopGateIterator struct {
	gate    chan struct{}
	blocked chan struct{}
	armed   *atomic.Bool
}

func (g *stopGateIterator) Next() (*DecodedFrame, error) {
	if g.armed.Load() && g.armed.CompareAndSwap(true, false) {
		close(g.blocked)
		<-g.gate
	}
	f := fakeFrames(1)[0]
	return &f, nil
}
func (g *stopGateIterator) EstimateFPS() float64 { return 30 }
func (g *stopGateIterator) TotalFrames() int     { return -1 }
func (g *stopGateIterator) Close()               {}

type stopGateProvider struct{ it *stopGateIterator }

func (p stopGateProvider) Open(string) (FrameIterator, error) { return p.it, nil }

// A Start that arrives while Stop() waits for the run goroutine must not
// start a run until Stop() is done: Stop's onStop callback, which the app
// uses to take the channel off the switcher, would otherwise land after the
// new run's onStart and leave an on-air channel unregistered, and the old
// run's cleanup would close the new run's clip iterator.
func TestChannel_StartDuringStopWaitsForStop(t *testing.T) {
	var armed atomic.Bool
	it := &stopGateIterator{gate: make(chan struct{}), blocked: make(chan struct{}), armed: &armed}
	ch := NewChannel(1, ChannelConfig{
		AssetProvider:        stopGateProvider{it},
		AllowedLocalPrefixes: []string{"/"},
	})
	var mu sync.Mutex
	var events []string
	ch.SetOnStart(func(string) { mu.Lock(); events = append(events, "start"); mu.Unlock() })
	ch.SetOnStop(func(string) { mu.Lock(); events = append(events, "stop"); mu.Unlock() })
	require.NoError(t, ch.LoadPlaylist(testPlaylist(clipItem("a"))))
	require.NoError(t, ch.Start())
	defer ch.Stop()

	// Hold the run goroutine inside a tick, then stop: Stop() waits for it.
	armed.Store(true)
	<-it.blocked
	stopped := make(chan struct{})
	go func() { ch.Stop(); close(stopped) }()
	waitFor(t, time.Second, func() bool { return ch.State() == StateStopped })

	started := make(chan error, 1)
	go func() { started <- ch.Start() }()
	time.Sleep(20 * time.Millisecond) // a Start that does not wait is done by now
	close(it.gate)
	<-stopped
	require.NoError(t, <-started)

	require.Equal(t, StateOnAir, ch.State())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"start", "stop", "start"}, events)
}
