package playout

import (
	"sync/atomic"
	"testing"
	"time"
)

// fakeDemux returns a DemuxFunc that produces n fake YUV420 frames at 30fps.
func fakeDemux(n int) func(string) ([]DecodedFrame, error) {
	return func(path string) ([]DecodedFrame, error) {
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
		return frames, nil
	}
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
		DemuxFunc: fakeDemux(5),
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
		DemuxFunc: fakeDemux(3), // 3 frames = very fast
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(5),
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
	if count != 5 {
		t.Errorf("frame count = %d, want 5", count)
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

func TestChannel_InOutPoints(t *testing.T) {
	// 10 frames at 30fps: PTS = 0, 3000, 6000, ..., 27000
	// 3000 PTS ticks = 33.33ms
	frames := make([]DecodedFrame, 10)
	for i := range frames {
		frames[i] = DecodedFrame{
			PTS:    int64(i) * 3000,
			Width:  1920,
			Height: 1080,
		}
	}

	t.Run("no_trim", func(t *testing.T) {
		result := applyInOutPoints(frames, 0, 0)
		if len(result) != 10 {
			t.Errorf("no trim: got %d frames, want 10", len(result))
		}
	})

	t.Run("in_point", func(t *testing.T) {
		// InPointMs = 100ms = 9000 PTS ticks. First frame at PTS >= 9000 is frame 3 (PTS=9000).
		result := applyInOutPoints(frames, 100, 0)
		if len(result) != 7 {
			t.Errorf("in point 100ms: got %d frames, want 7", len(result))
		}
		if result[0].PTS != 9000 {
			t.Errorf("first frame PTS = %d, want 9000", result[0].PTS)
		}
	})

	t.Run("out_point", func(t *testing.T) {
		// OutPointMs = 200ms = 18000 PTS ticks. First frame at PTS >= 18000 is frame 6 (PTS=18000).
		result := applyInOutPoints(frames, 0, 200)
		if len(result) != 6 {
			t.Errorf("out point 200ms: got %d frames, want 6", len(result))
		}
	})

	t.Run("in_and_out", func(t *testing.T) {
		// In=100ms (frame 3), Out=200ms (frame 6). Expect frames 3,4,5 = 3 frames.
		result := applyInOutPoints(frames, 100, 200)
		if len(result) != 3 {
			t.Errorf("in+out: got %d frames, want 3", len(result))
		}
	})

	t.Run("empty_result", func(t *testing.T) {
		// In point past end of clip — no playable content.
		result := applyInOutPoints(frames, 10000, 0)
		if result != nil {
			t.Errorf("in point past end: got %d frames, want nil", len(result))
		}
	})
}

func TestChannel_Status(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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

func TestChannel_ResolveAssetURI(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"local:///clips/intro.ts", "/clips/intro.ts"},
		{"file:///clips/intro.ts", "/clips/intro.ts"},
		{"/absolute/path.ts", "/absolute/path.ts"},
	}
	for _, tt := range tests {
		got := resolveAssetURI(tt.uri)
		if got != tt.want {
			t.Errorf("resolveAssetURI(%q) = %q, want %q", tt.uri, got, tt.want)
		}
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
		DemuxFunc: fakeDemux(3),
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
