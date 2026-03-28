//go:build correctness

package playout

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zsiec/switchframe/server/playout"
)

// ==========================================================================
// Test Helpers
// ==========================================================================

const (
	testWidth  = 320
	testHeight = 240
)

// frameSize returns the YUV420 frame size for given dimensions.
func frameSize(w, h int) int {
	return w*h + (w/2)*(h/2)*2
}

// makeFrames creates n fake YUV420 frames at 30fps with distinguishable Y values.
// Each frame's Y plane is filled with (index+1), so frame 0 has Y=1, frame 1 has Y=2, etc.
func makeFrames(n int, w, h int) []playout.DecodedFrame {
	frames := make([]playout.DecodedFrame, n)
	ySize := w * h
	uvSize := ySize / 4
	for i := range frames {
		yuv := make([]byte, ySize+2*uvSize)
		yVal := byte((i + 1) % 256)
		for j := 0; j < ySize; j++ {
			yuv[j] = yVal
		}
		for j := ySize; j < len(yuv); j++ {
			yuv[j] = 128
		}
		frames[i] = playout.DecodedFrame{
			YUV:        yuv,
			PTS:        int64(i) * 3000, // 30fps = 3000 PTS ticks per frame (90kHz/30)
			IsKeyframe: i == 0,
			Width:      w,
			Height:     h,
		}
	}
	return frames
}

// mapAssetProvider returns different frames based on asset URI.
type mapAssetProvider struct {
	mu     sync.Mutex
	assets map[string][]playout.DecodedFrame
}

func newMapAssetProvider() *mapAssetProvider {
	return &mapAssetProvider{
		assets: make(map[string][]playout.DecodedFrame),
	}
}

func (p *mapAssetProvider) addAsset(uri string, frames []playout.DecodedFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assets[uri] = frames
}

func (p *mapAssetProvider) Open(assetURI string) (playout.FrameIterator, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	frames, ok := p.assets[assetURI]
	if !ok {
		return nil, fmt.Errorf("asset not found: %s", assetURI)
	}
	// Deep copy frames to avoid aliasing.
	copied := make([]playout.DecodedFrame, len(frames))
	for i, f := range frames {
		yuv := make([]byte, len(f.YUV))
		copy(yuv, f.YUV)
		copied[i] = f
		copied[i].YUV = yuv
	}
	return playout.NewSliceIterator(copied), nil
}

// makeClipItem returns a clip PlaylistItem referencing a given asset URI.
func makeClipItem(id, assetURI string) playout.PlaylistItem {
	return playout.PlaylistItem{
		ID:       id,
		Type:     playout.ItemClip,
		AssetURI: assetURI,
		Title:    "Clip " + id,
	}
}

// makeHoldClipItem returns a clip item with Hold=true.
func makeHoldClipItem(id, assetURI string) playout.PlaylistItem {
	item := makeClipItem(id, assetURI)
	item.Hold = true
	item.Title = "Hold " + id
	return item
}

// makeLiveItem returns a live PlaylistItem.
func makeLiveItem(id, source string) playout.PlaylistItem {
	return playout.PlaylistItem{
		ID:     id,
		Type:   playout.ItemLive,
		Source: source,
		Title:  "Live " + id,
	}
}

// makeSlateItem returns a slate PlaylistItem with a given duration.
func makeSlateItem(id string, durationMs int64) playout.PlaylistItem {
	return playout.PlaylistItem{
		ID:         id,
		Type:       playout.ItemSlate,
		Title:      "Slate " + id,
		DurationMs: durationMs,
	}
}

// makePlaylist creates a Playlist with given items.
func makePlaylist(items ...playout.PlaylistItem) *playout.Playlist {
	return &playout.Playlist{
		ID:    "correctness-test",
		Name:  "Correctness Test Playlist",
		Items: items,
	}
}

// waitForCondition polls at 5ms intervals until condition returns true or timeout.
func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitForCondition: timed out")
}

// ==========================================================================
// Test 1: Scheduled Item Starts On Time (first output frame PTS)
// ==========================================================================

func TestScheduledItemStartsOnTime(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(10, testWidth, testHeight))
	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
		Width:         testWidth,
		Height:        testHeight,
	})

	var firstPTS atomic.Int64
	firstPTS.Store(-1)
	var ptsReceived atomic.Int32

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		if ptsReceived.CompareAndSwap(0, 1) {
			firstPTS.Store(pts)
		}
	})

	pl := makePlaylist(makeClipItem("clip-1", "local:///test.ts"))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())
	defer ch.Stop()

	// Wait for the first frame to be emitted.
	waitForCondition(t, 5*time.Second, func() bool {
		return ptsReceived.Load() > 0
	})

	pts := firstPTS.Load()
	require.GreaterOrEqual(t, pts, int64(0), "first frame PTS should be non-negative")
	// Without a PTS provider, the channel starts from PTS 0. The first frame should
	// be at PTS 0 or very close (one frame interval at most).
	assert.LessOrEqual(t, pts, int64(6000), // 2 frame intervals at 30fps
		"first frame PTS should be near 0 (got %d)", pts)
}

// ==========================================================================
// Test 2: Playlist Advancement (3-item sequential playback)
// ==========================================================================

func TestPlaylistAdvancement(t *testing.T) {
	provider := newMapAssetProvider()

	// Create 3 distinct clips with 5 frames each.
	for _, id := range []string{"clip-1", "clip-2", "clip-3"} {
		uri := "local:///clips/" + id + ".ts"
		provider.addAsset(uri, makeFrames(5, testWidth, testHeight))
	}

	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
		Width:         testWidth,
		Height:        testHeight,
	})

	// Track state changes.
	var mu sync.Mutex
	stateChanges := []playout.ChannelState{}

	ch.SetOnStateChange(func(s playout.ChannelState) {
		mu.Lock()
		stateChanges = append(stateChanges, s)
		mu.Unlock()
	})

	// Track frame count to verify all items played.
	var frameCount atomic.Int64
	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := makePlaylist(
		makeClipItem("clip-1", "local:///clips/clip-1.ts"),
		makeClipItem("clip-2", "local:///clips/clip-2.ts"),
		makeClipItem("clip-3", "local:///clips/clip-3.ts"),
	)
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	// Wait for completion.
	waitForCondition(t, 10*time.Second, func() bool {
		return ch.State() == playout.StateCompleted
	})

	// Verify at least 15 frames were emitted (5 per clip x 3 clips).
	count := frameCount.Load()
	assert.GreaterOrEqual(t, count, int64(15),
		"expected at least 15 frames (5 per clip x 3), got %d", count)

	// Verify state transitions included Cued -> OnAir -> Completed.
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(stateChanges), 2, "expected at least 2 state changes")

	// First state change should be Cued (from LoadPlaylist).
	assert.Equal(t, playout.StateCued, stateChanges[0], "first state change should be Cued")
	// Second should be OnAir (from Start).
	assert.Equal(t, playout.StateOnAir, stateChanges[1], "second state change should be OnAir")
	// Last should be Completed.
	assert.Equal(t, playout.StateCompleted, stateChanges[len(stateChanges)-1],
		"last state change should be Completed")
}

// ==========================================================================
// Test 3: Idle Loop Continuity
// ==========================================================================

func TestIdleLoopContinuity(t *testing.T) {
	// Create an idle loop with content frames.
	idleFrames := makeFrames(5, testWidth, testHeight)
	provider := playout.NewFakeAssetProvider(idleFrames)

	idleLoop := playout.NewIdleLoop("test://idle.ts", provider, testWidth, testHeight)
	defer idleLoop.Close()

	// Read 15 frames (3 full loops of 5-frame content).
	ySize := testWidth * testHeight
	for i := 0; i < 15; i++ {
		frame := idleLoop.NextFrame()
		require.NotNil(t, frame, "frame %d should not be nil", i)
		require.Len(t, frame.YUV, frameSize(testWidth, testHeight),
			"frame %d has wrong YUV size", i)

		// Verify the frame is NOT a black frame (Y=16). Our test frames have Y >= 1.
		// Since our frames cycle through Y=1,2,3,4,5 and then rewind, the first
		// Y byte should match the expected cycle position.
		expectedY := byte((i % 5) + 1)
		assert.Equal(t, expectedY, frame.YUV[0],
			"frame %d: Y[0]=%d, expected %d (content cycling)", i, frame.YUV[0], expectedY)

		// Verify this is NOT a black frame (BT.709 black Y=16).
		isBlack := true
		for j := 0; j < min(ySize, 100); j++ {
			if frame.YUV[j] != 16 {
				isBlack = false
				break
			}
		}
		assert.False(t, isBlack, "frame %d should not be black when idle content is configured", i)
	}
}

func TestIdleLoopBlackWhenNoContent(t *testing.T) {
	// No asset configured: should produce black frames.
	idleLoop := playout.NewIdleLoop("", nil, testWidth, testHeight)
	defer idleLoop.Close()

	ySize := testWidth * testHeight

	for i := 0; i < 5; i++ {
		frame := idleLoop.NextFrame()
		require.NotNil(t, frame, "frame %d should not be nil", i)

		// Y should be 16 (BT.709 black).
		assert.Equal(t, byte(16), frame.YUV[0], "frame %d: Y[0] should be 16 (black)", i)
		// UV should be 128 (neutral).
		assert.Equal(t, byte(128), frame.YUV[ySize], "frame %d: UV should be 128 (neutral)", i)
	}
}

// ==========================================================================
// Test 4: Pod Fire Inserts Correctly
// ==========================================================================

func TestPodFireInsertsCorrectly(t *testing.T) {
	pb := playout.NewPodBoard(1)

	// Track state changes.
	var stateChanges atomic.Int32
	pb.SetOnStateChange(func() {
		stateChanges.Add(1)
	})

	var breakStarted atomic.Int32
	pb.SetOnBreakStart(func() {
		breakStarted.Store(1)
	})

	// Create and arm a pod.
	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "commercial",
		DurationMs:   5000,
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     playout.PriorityNormal,
	})
	require.NoError(t, err)
	require.NotEmpty(t, pod.ID)

	err = pb.Arm(pod.ID)
	require.NoError(t, err)

	// Verify armed state.
	armed, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.True(t, armed.Armed, "pod should be armed")
	assert.Equal(t, playout.PodScheduled, armed.State, "pod should be in scheduled state")

	// Fire the pod.
	err = pb.Fire(pod.ID)
	require.NoError(t, err)

	// Verify state transition to break_active.
	fired, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodBreakActive, fired.State, "pod should be in break_active state")
	assert.Greater(t, fired.FiredAt, int64(0), "FiredAt should be set")

	// Verify break start callback fired.
	assert.Equal(t, int32(1), breakStarted.Load(), "onBreakStart should have been called")

	// Verify status reports active pod.
	status := pb.Status()
	assert.NotNil(t, status.ActivePod, "status should have an active pod")
	if status.ActivePod != nil {
		assert.Equal(t, pod.ID, status.ActivePod.ID)
		assert.Equal(t, "break_active", status.ActivePod.State)
	}

	// Wait for auto-return (5 seconds is too long for a test; verify manually).
	// Instead, just verify the state is correct.
	assert.Greater(t, stateChanges.Load(), int32(0), "state changes should have been fired")
}

// ==========================================================================
// Test 5: Pod Dump Out
// ==========================================================================

func TestPodDumpOut(t *testing.T) {
	pb := playout.NewPodBoard(1)

	var breakEnded atomic.Int32
	pb.SetOnBreakEnd(func() {
		breakEnded.Store(1)
	})

	// Create, arm, and fire a pod with dump out allowed.
	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "commercial",
		DurationMs:   60000, // 60 seconds -- long enough we won't auto-return.
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     playout.PriorityNormal,
	})
	require.NoError(t, err)

	require.NoError(t, pb.Arm(pod.ID))
	require.NoError(t, pb.Fire(pod.ID))

	// Verify pod is break_active.
	active, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodBreakActive, active.State)

	// Dump out.
	err = pb.DumpOut(pod.ID)
	require.NoError(t, err)

	// Verify pod transitioned to dumped_out.
	dumped, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodDumpedOut, dumped.State, "pod should be dumped_out")
	assert.Greater(t, dumped.CompletedAt, int64(0), "CompletedAt should be set")

	// Verify break end callback fired.
	assert.Equal(t, int32(1), breakEnded.Load(), "onBreakEnd should have been called")

	// Verify status no longer shows active pod.
	status := pb.Status()
	assert.Nil(t, status.ActivePod, "no active pod after dump out")
	assert.Equal(t, 1, status.PodsCompleted, "should have 1 completed pod")
}

func TestPodDumpOutNotAllowed(t *testing.T) {
	pb := playout.NewPodBoard(1)

	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "contractual",
		DurationMs:   60000,
		AutoReturn:   true,
		AllowDumpOut: false, // dump out NOT allowed
		Priority:     playout.PriorityMandatory,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod.ID))
	require.NoError(t, pb.Fire(pod.ID))

	err = pb.DumpOut(pod.ID)
	assert.ErrorIs(t, err, playout.ErrPodNoDumpOut, "dump out should be rejected")

	// Pod should still be active.
	got, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodBreakActive, got.State)
}

// ==========================================================================
// Test 6: Watchdog Recovery
// ==========================================================================

func TestWatchdogRecovery(t *testing.T) {
	var mu sync.Mutex
	stalledChannels := []int{}

	w := playout.NewWatchdog(playout.WatchdogConfig{
		StallThreshold: 100 * time.Millisecond,
		CheckInterval:  20 * time.Millisecond,
		OnStall: func(channelID int) {
			mu.Lock()
			stalledChannels = append(stalledChannels, channelID)
			mu.Unlock()
		},
	})

	// Register channel and report one frame, then stop reporting.
	w.RegisterChannel(1)
	w.ReportFrame(1)
	w.Start()
	defer w.Stop()

	// Wait for stall detection.
	waitForCondition(t, 2*time.Second, func() bool {
		mu.Lock()
		n := len(stalledChannels)
		mu.Unlock()
		return n > 0
	})

	mu.Lock()
	assert.Contains(t, stalledChannels, 1, "channel 1 should be reported as stalled")
	mu.Unlock()

	// Verify health reports stalled.
	healths := w.Health()
	require.Len(t, healths, 1)
	assert.True(t, healths[0].Stalled, "health should report stalled")
	assert.False(t, healths[0].Healthy, "health should report unhealthy")

	// Resume frame reporting -- stall flag should clear.
	w.ReportFrame(1)

	// After reporting a frame, the stalled flag should be cleared.
	healths = w.Health()
	require.Len(t, healths, 1)
	assert.False(t, healths[0].Stalled, "stall flag should clear after frame report")
	assert.True(t, healths[0].Healthy, "health should be healthy after frame report")
}

func TestWatchdogNoStallWhenFramesFlowing(t *testing.T) {
	var stallCount atomic.Int32

	w := playout.NewWatchdog(playout.WatchdogConfig{
		StallThreshold: 200 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})

	w.RegisterChannel(1)
	w.Start()
	defer w.Stop()

	// Report frames every 20ms for 500ms -- well within the 200ms threshold.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 25; i++ {
			w.ReportFrame(1)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	<-done

	assert.Equal(t, int32(0), stallCount.Load(),
		"no stalls should be detected when frames are flowing")
}

// ==========================================================================
// Test 7: Dual Channel Independence
// ==========================================================================

func TestDualChannelIndependence(t *testing.T) {
	m := playout.NewManager(playout.ManagerConfig{
		AssetProvider: playout.NewFakeAssetProvider(makeFrames(10, 1920, 1080)),
	})
	defer m.Close()

	var ch1Frames atomic.Int64
	var ch2Frames atomic.Int64

	m.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		switch key {
		case "playout:1":
			ch1Frames.Add(1)
		case "playout:2":
			ch2Frames.Add(1)
		}
	})

	// Channel 1: 2 clips.
	pl1 := makePlaylist(
		makeClipItem("ch1-a", "local:///ch1-a.ts"),
		makeClipItem("ch1-b", "local:///ch1-b.ts"),
	)
	require.NoError(t, m.LoadPlaylist(1, pl1))

	// Channel 2: 3 clips.
	pl2 := makePlaylist(
		makeClipItem("ch2-a", "local:///ch2-a.ts"),
		makeClipItem("ch2-b", "local:///ch2-b.ts"),
		makeClipItem("ch2-c", "local:///ch2-c.ts"),
	)
	require.NoError(t, m.LoadPlaylist(2, pl2))

	// Start both channels.
	require.NoError(t, m.Start(1))
	require.NoError(t, m.Start(2))

	// Wait for both to complete.
	waitForCondition(t, 15*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed" && states[1].State == "completed"
	})

	states := m.ChannelStates()
	assert.Equal(t, "completed", states[0].State, "channel 1 should be completed")
	assert.Equal(t, "completed", states[1].State, "channel 2 should be completed")

	// Channel 1: 2 clips x 10 frames = 20 frames minimum.
	assert.GreaterOrEqual(t, ch1Frames.Load(), int64(20),
		"channel 1 should have >= 20 frames")

	// Channel 2: 3 clips x 10 frames = 30 frames minimum.
	assert.GreaterOrEqual(t, ch2Frames.Load(), int64(30),
		"channel 2 should have >= 30 frames")

	// Verify channels don't cross-contaminate: ch2 should have more frames than ch1.
	assert.Greater(t, ch2Frames.Load(), ch1Frames.Load(),
		"channel 2 (3 clips) should have more frames than channel 1 (2 clips)")
}

// ==========================================================================
// Test 8: Playlist Load and State
// ==========================================================================

func TestPlaylistLoadAndState(t *testing.T) {
	ch := playout.NewChannel(1, playout.ChannelConfig{
		Width:  testWidth,
		Height: testHeight,
	})

	// Initially stopped.
	assert.Equal(t, playout.StateStopped, ch.State())

	status := ch.Status()
	assert.Equal(t, "stopped", status.State)
	assert.Equal(t, "", status.PlaylistName)
	assert.Equal(t, 0, status.ItemCount)

	// Load first playlist.
	pl1 := makePlaylist(
		makeClipItem("a", "local:///a.ts"),
		makeClipItem("b", "local:///b.ts"),
	)
	pl1.Name = "Playlist Alpha"
	require.NoError(t, ch.LoadPlaylist(pl1))
	assert.Equal(t, playout.StateCued, ch.State())

	status = ch.Status()
	assert.Equal(t, "cued", status.State)
	assert.Equal(t, "Playlist Alpha", status.PlaylistName)
	assert.Equal(t, 2, status.ItemCount)

	// Load a different playlist (replacing the first).
	pl2 := makePlaylist(
		makeClipItem("x", "local:///x.ts"),
		makeClipItem("y", "local:///y.ts"),
		makeClipItem("z", "local:///z.ts"),
	)
	pl2.Name = "Playlist Beta"
	require.NoError(t, ch.LoadPlaylist(pl2))
	assert.Equal(t, playout.StateCued, ch.State())

	status = ch.Status()
	assert.Equal(t, "cued", status.State)
	assert.Equal(t, "Playlist Beta", status.PlaylistName)
	assert.Equal(t, 3, status.ItemCount)

	// Verify the items are from the second playlist.
	items := ch.PlaylistItems()
	require.Len(t, items, 3)
	assert.Equal(t, "x", items[0].ID)
	assert.Equal(t, "y", items[1].ID)
	assert.Equal(t, "z", items[2].ID)
}

func TestPlaylistLoadRejectsInvalid(t *testing.T) {
	ch := playout.NewChannel(1, playout.ChannelConfig{})

	// Empty playlist.
	err := ch.LoadPlaylist(&playout.Playlist{ID: "empty", Name: "Empty"})
	assert.Error(t, err, "empty playlist should be rejected")

	// Item with missing asset_uri for clip type.
	err = ch.LoadPlaylist(&playout.Playlist{
		ID:   "bad",
		Name: "Bad",
		Items: []playout.PlaylistItem{
			{ID: "item-1", Type: playout.ItemClip, Title: "No Asset"},
		},
	})
	assert.Error(t, err, "clip without asset_uri should be rejected")
}

// ==========================================================================
// Test 9: Channel Start/Stop Lifecycle
// ==========================================================================

func TestChannelStartStop(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(5, testWidth, testHeight))
	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
		Width:         testWidth,
		Height:        testHeight,
	})

	var mu sync.Mutex
	stateTransitions := []playout.ChannelState{}
	ch.SetOnStateChange(func(s playout.ChannelState) {
		mu.Lock()
		stateTransitions = append(stateTransitions, s)
		mu.Unlock()
	})

	// State 1: Stopped (initial).
	assert.Equal(t, playout.StateStopped, ch.State())

	// Start requires a playlist.
	err := ch.Start()
	assert.ErrorIs(t, err, playout.ErrNoPlaylist)

	// Load playlist -> Cued.
	pl := makePlaylist(makeLiveItem("live-1", "cam-1"))
	require.NoError(t, ch.LoadPlaylist(pl))
	assert.Equal(t, playout.StateCued, ch.State())

	// Start -> OnAir.
	require.NoError(t, ch.Start())
	assert.Equal(t, playout.StateOnAir, ch.State())

	// Double start should fail.
	err = ch.Start()
	assert.ErrorIs(t, err, playout.ErrAlreadyOnAir)

	// Stop -> Stopped.
	ch.Stop()
	assert.Equal(t, playout.StateStopped, ch.State())

	// Verify state transitions: Cued -> OnAir -> Stopped.
	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(stateTransitions), 3,
		"expected at least 3 state transitions (Cued, OnAir, Stopped)")

	assert.Equal(t, playout.StateCued, stateTransitions[0])
	assert.Equal(t, playout.StateOnAir, stateTransitions[1])
	assert.Equal(t, playout.StateStopped, stateTransitions[len(stateTransitions)-1])
}

func TestChannelStopFromStopped(t *testing.T) {
	ch := playout.NewChannel(1, playout.ChannelConfig{})

	// Stop on a stopped channel is a no-op (should not panic).
	ch.Stop()
	assert.Equal(t, playout.StateStopped, ch.State())
}

func TestChannelDoubleStop(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(5, testWidth, testHeight))
	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
	})

	pl := makePlaylist(makeLiveItem("live-1", "cam"))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	time.Sleep(50 * time.Millisecond)

	// Double stop should not panic.
	ch.Stop()
	ch.Stop()
	assert.Equal(t, playout.StateStopped, ch.State())
}

func TestChannelRestartAfterStop(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(3, testWidth, testHeight))
	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
		Width:         testWidth,
		Height:        testHeight,
	})

	// First run.
	pl := makePlaylist(makeClipItem("first", "local:///first.ts"))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitForCondition(t, 5*time.Second, func() bool {
		return ch.State() == playout.StateCompleted
	})

	// Stop and load a new playlist.
	ch.Stop()
	assert.Equal(t, playout.StateStopped, ch.State())

	pl2 := makePlaylist(makeClipItem("second", "local:///second.ts"))
	require.NoError(t, ch.LoadPlaylist(pl2))
	require.NoError(t, ch.Start())

	waitForCondition(t, 5*time.Second, func() bool {
		return ch.State() == playout.StateCompleted
	})
}

// ==========================================================================
// Test 10: Next/Previous Navigation
// ==========================================================================

func TestNextPrevious(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(5, 1920, 1080))
	m := playout.NewManager(playout.ManagerConfig{
		AssetProvider: provider,
	})
	defer m.Close()

	// Use hold items so they block and we can observe cursor position.
	pl := makePlaylist(
		makeHoldClipItem("item-1", "local:///1.ts"),
		makeHoldClipItem("item-2", "local:///2.ts"),
		makeHoldClipItem("item-3", "local:///3.ts"),
	)
	require.NoError(t, m.LoadPlaylist(1, pl))
	require.NoError(t, m.Start(1))

	// Wait for first item to be on-air.
	waitForCondition(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air" && states[0].CurrentItemID == "item-1"
	})

	// Next -> item-2.
	require.NoError(t, m.Next(1))
	waitForCondition(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	states := m.ChannelStates()
	assert.Equal(t, "item-2", states[0].CurrentItemID)

	// Next -> item-3.
	require.NoError(t, m.Next(1))
	waitForCondition(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	// Previous -> back to item-2.
	require.NoError(t, m.Previous(1))
	waitForCondition(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	states = m.ChannelStates()
	assert.Equal(t, "item-2", states[0].CurrentItemID)

	// Next twice more to complete.
	require.NoError(t, m.Next(1))
	waitForCondition(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	require.NoError(t, m.Next(1))
	waitForCondition(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

// ==========================================================================
// Scheduler Unit Tests (pure state machine, no goroutines)
// ==========================================================================

func TestSchedulerSlateAutoAdvance(t *testing.T) {
	s := playout.NewScheduler()

	pl := makePlaylist(
		makeSlateItem("slate", 100),
		makeClipItem("clip", "local:///clip.ts"),
	)
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	start := time.Now()

	// First tick: should be playing the slate.
	d := s.Tick(start)
	assert.Equal(t, playout.SchedPlaying, d.State)
	require.NotNil(t, d.Item)
	assert.Equal(t, "slate", d.Item.ID)

	// At 50ms: still on slate.
	d = s.Tick(start.Add(50 * time.Millisecond))
	assert.Equal(t, "slate", d.Item.ID)

	// At 101ms: slate duration expired, should advance to clip.
	d = s.Tick(start.Add(101 * time.Millisecond))
	require.NotNil(t, d.Item)
	assert.Equal(t, "clip", d.Item.ID, "should auto-advance past slate after duration")
}

func TestSchedulerTransitionProgress(t *testing.T) {
	s := playout.NewScheduler()

	itemA := makeClipItem("a", "local:///a.ts")
	itemB := makeClipItem("b", "local:///b.ts")
	itemB.Transition = &playout.TransitionConfig{
		Type:       "dissolve",
		DurationMs: 1000,
	}

	pl := makePlaylist(itemA, itemB)
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	now := time.Now()

	// Start playing item A.
	d := s.Tick(now)
	assert.Equal(t, "a", d.Item.ID)

	// Signal item A complete -- should start transition.
	s.SignalItemComplete()
	d = s.Tick(now)
	assert.Equal(t, playout.SchedTransitioning, d.State)
	require.NotNil(t, d.Transition)
	assert.Equal(t, "a", d.Transition.Outgoing.ID)
	assert.Equal(t, "b", d.Transition.Incoming.ID)
	assert.InDelta(t, 0.0, d.Transition.Progress, 0.05)

	// At 500ms: progress ~0.5.
	d = s.Tick(now.Add(500 * time.Millisecond))
	assert.Equal(t, playout.SchedTransitioning, d.State)
	assert.InDelta(t, 0.5, d.Transition.Progress, 0.1)

	// At 1001ms: transition complete, playing item B.
	d = s.Tick(now.Add(1001 * time.Millisecond))
	assert.Equal(t, playout.SchedPlaying, d.State)
	require.NotNil(t, d.Item)
	assert.Equal(t, "b", d.Item.ID)
}

func TestSchedulerHoldItem(t *testing.T) {
	s := playout.NewScheduler()

	holdItem := makeClipItem("hold", "local:///hold.ts")
	holdItem.Hold = true

	pl := makePlaylist(holdItem, makeClipItem("next", "local:///next.ts"))
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	now := time.Now()

	// First tick: playing hold item.
	d := s.Tick(now)
	assert.Equal(t, "hold", d.Item.ID)

	// Signal complete -- hold item should NOT advance.
	s.SignalItemComplete()
	d = s.Tick(now.Add(5 * time.Second))
	assert.Equal(t, "hold", d.Item.ID, "hold item should not advance after SignalItemComplete")

	// Explicit Next() should advance past hold.
	require.NoError(t, s.Next())
	d = s.Tick(now.Add(6 * time.Second))
	require.NotNil(t, d.Item)
	assert.Equal(t, "next", d.Item.ID, "Next() should advance past hold item")
}

func TestSchedulerPlaylistCompletion(t *testing.T) {
	s := playout.NewScheduler()

	pl := makePlaylist(makeClipItem("only", "local:///only.ts"))
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	assert.Equal(t, "only", d.Item.ID)

	// Signal the one and only item complete.
	s.SignalItemComplete()
	d = s.Tick(now)

	assert.True(t, d.Completed, "playlist should be completed")
	assert.Equal(t, playout.SchedIdle, d.State)
}

func TestSchedulerManualModeBlocksAdvance(t *testing.T) {
	s := playout.NewScheduler()

	pl := makePlaylist(
		makeSlateItem("slate", 100),
		makeClipItem("clip", "local:///clip.ts"),
	)
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	// Enter manual mode.
	s.TakeManual()
	assert.Equal(t, playout.ModeManual, s.Mode())

	now := time.Now()
	d := s.Tick(now)
	assert.Equal(t, "slate", d.Item.ID)

	// After slate duration, manual mode blocks advance.
	d = s.Tick(now.Add(200 * time.Millisecond))
	assert.Equal(t, "slate", d.Item.ID, "manual mode should block auto-advance")

	// Return to auto.
	s.ReturnToAuto()
	d = s.Tick(now.Add(201 * time.Millisecond))
	require.NotNil(t, d.Item)
	assert.Equal(t, "clip", d.Item.ID, "should advance after returning to auto")
}

func TestSchedulerJumpTo(t *testing.T) {
	s := playout.NewScheduler()

	pl := makePlaylist(
		makeClipItem("a", "local:///a.ts"),
		makeClipItem("b", "local:///b.ts"),
		makeClipItem("c", "local:///c.ts"),
	)
	require.NoError(t, s.LoadPlaylist(pl))
	s.Start()

	now := time.Now()
	d := s.Tick(now)
	assert.Equal(t, "a", d.Item.ID)

	// Jump to item C.
	require.NoError(t, s.JumpTo("c"))
	d = s.Tick(now)
	require.NotNil(t, d.Item)
	assert.Equal(t, "c", d.Item.ID)
	assert.Equal(t, 2, s.Cursor())

	// JumpTo unknown ID should fail.
	err := s.JumpTo("nonexistent")
	assert.ErrorIs(t, err, playout.ErrItemNotFound)
}

// ==========================================================================
// Pod Board Integration Tests
// ==========================================================================

func TestPodAutoReturn(t *testing.T) {
	pb := playout.NewPodBoard(1)

	var breakEnded atomic.Int32
	pb.SetOnBreakEnd(func() {
		breakEnded.Store(1)
	})

	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "short-break",
		DurationMs:   200, // 200ms auto-return.
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     playout.PriorityNormal,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod.ID))
	require.NoError(t, pb.Fire(pod.ID))

	// Wait for auto-return.
	waitForCondition(t, 3*time.Second, func() bool {
		p, err := pb.GetPod(pod.ID)
		if err != nil {
			return false
		}
		return p.State.IsTerminal()
	})

	final, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodRejoined, final.State, "pod should auto-return to rejoined")
	assert.Equal(t, int32(1), breakEnded.Load(), "onBreakEnd should have been called")
}

func TestPodHold(t *testing.T) {
	pb := playout.NewPodBoard(1)

	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "hold-test",
		DurationMs:   200, // Would auto-return in 200ms.
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     playout.PriorityNormal,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod.ID))
	require.NoError(t, pb.Fire(pod.ID))

	// Hold the pod (cancels auto-return).
	require.NoError(t, pb.Hold(pod.ID))

	// Wait well beyond the original auto-return duration.
	time.Sleep(400 * time.Millisecond)

	// Pod should still be break_active (hold prevents auto-return).
	held, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodBreakActive, held.State,
		"pod should remain break_active when held")

	// Manual dump out should still work.
	require.NoError(t, pb.DumpOut(pod.ID))

	dumped, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodDumpedOut, dumped.State)
}

func TestPodExtend(t *testing.T) {
	pb := playout.NewPodBoard(1)

	pod, err := pb.CreatePod(playout.PodConfig{
		Type:         "extend-test",
		DurationMs:   200,
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     playout.PriorityNormal,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod.ID))
	require.NoError(t, pb.Fire(pod.ID))

	// Extend by 500ms.
	require.NoError(t, pb.Extend(pod.ID, 500))

	// After original 200ms, should still be active (was extended).
	time.Sleep(250 * time.Millisecond)
	ext, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodBreakActive, ext.State,
		"pod should still be active after extend")

	// Wait for total extended duration.
	waitForCondition(t, 3*time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State.IsTerminal()
	})

	final, err := pb.GetPod(pod.ID)
	require.NoError(t, err)
	assert.Equal(t, playout.PodRejoined, final.State)
}

func TestPodFireRequiresArm(t *testing.T) {
	pb := playout.NewPodBoard(1)

	pod, err := pb.CreatePod(playout.PodConfig{
		Type:       "test",
		DurationMs: 1000,
	})
	require.NoError(t, err)

	// Fire without arming should fail.
	err = pb.Fire(pod.ID)
	assert.ErrorIs(t, err, playout.ErrPodNotArmed)
}

func TestPodOnlyOneActive(t *testing.T) {
	pb := playout.NewPodBoard(1)

	pod1, err := pb.CreatePod(playout.PodConfig{
		Type:       "first",
		DurationMs: 60000,
		AutoReturn: true,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod1.ID))
	require.NoError(t, pb.Fire(pod1.ID))

	pod2, err := pb.CreatePod(playout.PodConfig{
		Type:       "second",
		DurationMs: 60000,
		AutoReturn: true,
	})
	require.NoError(t, err)
	require.NoError(t, pb.Arm(pod2.ID))

	// Firing a second pod while one is active should fail.
	err = pb.Fire(pod2.ID)
	assert.ErrorIs(t, err, playout.ErrPodAlreadyActive)
}

// ==========================================================================
// Playlist Validation Tests
// ==========================================================================

func TestPlaylistValidation(t *testing.T) {
	t.Run("empty_playlist", func(t *testing.T) {
		err := playout.ValidatePlaylist(&playout.Playlist{ID: "e", Name: "Empty"})
		assert.ErrorIs(t, err, playout.ErrEmptyPlaylist)
	})

	t.Run("duplicate_item_ids", func(t *testing.T) {
		pl := &playout.Playlist{
			ID:   "dup",
			Name: "Dup",
			Items: []playout.PlaylistItem{
				{ID: "a", Type: playout.ItemClip, AssetURI: "local:///a.ts", Title: "A"},
				{ID: "a", Type: playout.ItemClip, AssetURI: "local:///b.ts", Title: "B"},
			},
		}
		err := playout.ValidatePlaylist(pl)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate")
	})

	t.Run("clip_requires_asset_uri", func(t *testing.T) {
		pl := &playout.Playlist{
			ID:   "no-uri",
			Name: "No URI",
			Items: []playout.PlaylistItem{
				{ID: "a", Type: playout.ItemClip, Title: "A"},
			},
		}
		err := playout.ValidatePlaylist(pl)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "asset_uri")
	})

	t.Run("live_requires_source", func(t *testing.T) {
		pl := &playout.Playlist{
			ID:   "no-src",
			Name: "No Source",
			Items: []playout.PlaylistItem{
				{ID: "a", Type: playout.ItemLive, Title: "A"},
			},
		}
		err := playout.ValidatePlaylist(pl)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "source")
	})

	t.Run("loop_only_on_clips", func(t *testing.T) {
		pl := &playout.Playlist{
			ID:   "loop-slate",
			Name: "Loop Slate",
			Items: []playout.PlaylistItem{
				{ID: "a", Type: playout.ItemSlate, Title: "Slate", Loop: true, DurationMs: 1000},
			},
		}
		err := playout.ValidatePlaylist(pl)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "loop")
	})

	t.Run("valid_playlist", func(t *testing.T) {
		pl := &playout.Playlist{
			ID:   "good",
			Name: "Good",
			Items: []playout.PlaylistItem{
				{ID: "a", Type: playout.ItemClip, AssetURI: "local:///a.ts", Title: "A"},
				{ID: "b", Type: playout.ItemLive, Source: "cam-1", Title: "B"},
				{ID: "c", Type: playout.ItemSlate, Title: "C", DurationMs: 5000},
			},
		}
		assert.NoError(t, playout.ValidatePlaylist(pl))
	})
}

// ==========================================================================
// Output Callback Tests
// ==========================================================================

func TestRawVideoOutputCallback(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(5, testWidth, testHeight))
	ch := playout.NewChannel(1, playout.ChannelConfig{
		AssetProvider: provider,
		Width:         testWidth,
		Height:        testHeight,
	})

	var mu sync.Mutex
	var outputFrames []struct {
		PTS        int64
		Width      int
		Height     int
		IsKeyframe bool
		YUVLen     int
	}

	ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
		mu.Lock()
		outputFrames = append(outputFrames, struct {
			PTS        int64
			Width      int
			Height     int
			IsKeyframe bool
			YUVLen     int
		}{pts, w, h, isKeyframe, len(yuv)})
		mu.Unlock()
	})

	pl := makePlaylist(makeClipItem("vid", "local:///vid.ts"))
	require.NoError(t, ch.LoadPlaylist(pl))
	require.NoError(t, ch.Start())

	waitForCondition(t, 5*time.Second, func() bool {
		return ch.State() == playout.StateCompleted
	})

	mu.Lock()
	defer mu.Unlock()

	// Should have received at least 5 frames.
	require.GreaterOrEqual(t, len(outputFrames), 5, "expected at least 5 output frames")

	// Verify frame dimensions.
	for i, f := range outputFrames {
		assert.Equal(t, testWidth, f.Width, "frame %d width", i)
		assert.Equal(t, testHeight, f.Height, "frame %d height", i)
		assert.Equal(t, frameSize(testWidth, testHeight), f.YUVLen,
			"frame %d YUV size", i)
	}

	// Verify PTS is monotonically increasing.
	for i := 1; i < len(outputFrames); i++ {
		assert.GreaterOrEqual(t, outputFrames[i].PTS, outputFrames[i-1].PTS,
			"frame %d PTS should be >= frame %d PTS", i, i-1)
	}
}

// ==========================================================================
// Lifecycle Callback Tests
// ==========================================================================

func TestChannelLifecycleCallbacks(t *testing.T) {
	provider := playout.NewFakeAssetProvider(makeFrames(3, 1920, 1080))
	m := playout.NewManager(playout.ManagerConfig{
		AssetProvider: provider,
	})

	var startCalled atomic.Int32
	var stopCalled atomic.Int32
	var startKey atomic.Value
	var stopKey atomic.Value

	m.OnChannelLifecycle(
		func(channelID int, key string) {
			startKey.Store(key)
			startCalled.Add(1)
		},
		func(channelID int, key string) {
			stopKey.Store(key)
			stopCalled.Add(1)
		},
	)

	pl := makePlaylist(makeLiveItem("live", "cam"))
	require.NoError(t, m.LoadPlaylist(1, pl))
	require.NoError(t, m.Start(1))

	// Verify onStart called.
	waitForCondition(t, 2*time.Second, func() bool {
		return startCalled.Load() >= 1
	})

	sk, _ := startKey.Load().(string)
	assert.Equal(t, "playout:1", sk)

	// Stop the channel.
	require.NoError(t, m.Stop(1))

	// Verify onStop called.
	waitForCondition(t, 2*time.Second, func() bool {
		return stopCalled.Load() >= 1
	})

	spk, _ := stopKey.Load().(string)
	assert.Equal(t, "playout:1", spk)
}
