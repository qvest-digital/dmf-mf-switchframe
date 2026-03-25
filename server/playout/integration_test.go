package playout

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_PlaylistExecution(t *testing.T) {
	var frameCount atomic.Int64

	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(10),
	})
	defer m.Close()

	m.SetRawVideoOutput(func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool) {
		frameCount.Add(1)
	})

	pl := testPlaylist(clipItem("clip-1"), clipItem("clip-2"), clipItem("clip-3"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	count := frameCount.Load()
	if count < 30 {
		t.Errorf("total frames = %d, want >= 30 (10 per clip x 3 clips)", count)
	}

	states := m.ChannelStates()
	if states[0].State != "completed" {
		t.Errorf("final state = %q, want %q", states[0].State, "completed")
	}
}

func TestIntegration_ManualOverrideAndReturn(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(10),
	})
	defer m.Close()

	// hold item blocks after playback; regular item completes.
	pl := testPlaylist(holdClipItem("item-1"), clipItem("item-2"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air state.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Take manual control.
	if err := m.TakeManual(1); err != nil {
		t.Fatalf("TakeManual: %v", err)
	}

	states := m.ChannelStates()
	if states[0].Mode != "manual" {
		t.Errorf("mode after TakeManual = %q, want %q", states[0].Mode, "manual")
	}

	// Advance past the hold item; manual mode should block before next item.
	_ = m.Next(1)
	time.Sleep(150 * time.Millisecond)

	// Should still be on-air in manual mode.
	states = m.ChannelStates()
	if states[0].State != "on_air" {
		t.Errorf("state during manual = %q, want %q", states[0].State, "on_air")
	}

	// Return to auto — should resume and complete.
	if err := m.ReturnToAuto(1); err != nil {
		t.Fatalf("ReturnToAuto: %v", err)
	}

	states = m.ChannelStates()
	if states[0].Mode != "automated" {
		t.Errorf("mode after ReturnToAuto = %q, want %q", states[0].Mode, "automated")
	}

	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_NextSkipsItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(200), // long clip: 200 frames at 30fps ~ 6.6s
	})
	defer m.Close()

	pl := testPlaylist(clipItem("long-clip"), clipItem("short-clip"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air state.
	waitFor(t, 2*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Let a few frames play, then skip.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := m.Next(1); err != nil {
		t.Fatalf("Next: %v", err)
	}

	// Wait for completion — should be much faster than playing all 400 frames.
	waitFor(t, 10*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})

	elapsed := time.Since(start)
	// Playing 200+200 frames at 30fps would take ~13s. With skip it should be
	// under 10s (only the second 200-frame clip plays fully).
	if elapsed > 12*time.Second {
		t.Errorf("elapsed = %v, expected much less than full playback time", elapsed)
	}
}

func TestIntegration_JumpToItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(5),
	})
	defer m.Close()

	pl := testPlaylist(
		holdClipItem("item-1"),
		holdClipItem("item-2"),
		holdClipItem("item-3"),
		holdClipItem("item-4"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for item-1 to be on-air (hold items play then block).
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air" && states[0].CurrentItemID == "item-1"
	})

	// Jump to item-3.
	if err := m.JumpTo(1, "item-3"); err != nil {
		t.Fatalf("JumpTo: %v", err)
	}

	// Wait for current item to be item-3.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	// Advance past item-3's hold to item-4.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past item-3: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-4"
	})

	// Advance past item-4's hold to complete.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past item-4: %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_PreviousItem(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(5),
	})
	defer m.Close()

	pl := testPlaylist(
		holdClipItem("item-1"),
		holdClipItem("item-2"),
		holdClipItem("item-3"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for item-1 on-air.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air" && states[0].CurrentItemID == "item-1"
	})

	// Advance to item-2.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next to item-2: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	// Go back to item-1.
	if err := m.Previous(1); err != nil {
		t.Fatalf("Previous to item-1: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-1"
	})

	// Advance through all to completion: item-1 → item-2 → item-3 → done.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-1 (second time): %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-2"
	})

	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-2: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "item-3"
	})

	if err := m.Next(1); err != nil {
		t.Fatalf("Next from item-3: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_LiveItemHold(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(5),
	})
	defer m.Close()

	pl := testPlaylist(
		clipItem("intro"),
		liveItem("live-cam", "camera-1"),
		clipItem("outro"),
	)
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the live item to become current.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].CurrentItemID == "live-cam"
	})

	// Channel should hold on the live item.
	time.Sleep(200 * time.Millisecond)
	states := m.ChannelStates()
	if states[0].CurrentItemID != "live-cam" {
		t.Errorf("still on live item: currentItemID = %q, want %q", states[0].CurrentItemID, "live-cam")
	}
	if states[0].State != "on_air" {
		t.Errorf("state = %q, want %q", states[0].State, "on_air")
	}

	// Advance past live item.
	if err := m.Next(1); err != nil {
		t.Fatalf("Next past live: %v", err)
	}

	// Wait for completion (outro clip plays out).
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_LifecycleCallbacks(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(5),
	})

	var startCalled atomic.Int32
	var stopCalled atomic.Int32
	var startChID atomic.Int32
	var stopChID atomic.Int32
	var startKey atomic.Value
	var stopKey atomic.Value

	m.OnChannelLifecycle(
		func(channelID int, key string) {
			startChID.Store(int32(channelID))
			startKey.Store(key)
			startCalled.Add(1)
		},
		func(channelID int, key string) {
			stopChID.Store(int32(channelID))
			stopKey.Store(key)
			stopCalled.Add(1)
		},
	)

	pl := testPlaylist(liveItem("live-1", "cam"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify onStart called.
	waitFor(t, 2*time.Second, func() bool {
		return startCalled.Load() >= 1
	})

	if startChID.Load() != 1 {
		t.Errorf("onStart channelID = %d, want 1", startChID.Load())
	}
	sk, _ := startKey.Load().(string)
	if sk != "playout:1" {
		t.Errorf("onStart key = %q, want %q", sk, "playout:1")
	}

	// Stop the channel.
	if err := m.Stop(1); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify onStop called.
	waitFor(t, 2*time.Second, func() bool {
		return stopCalled.Load() >= 1
	})

	if stopChID.Load() != 1 {
		t.Errorf("onStop channelID = %d, want 1", stopChID.Load())
	}
	spk, _ := stopKey.Load().(string)
	if spk != "playout:1" {
		t.Errorf("onStop key = %q, want %q", spk, "playout:1")
	}
}

func TestIntegration_StopMidPlayback(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(5),
	})
	defer m.Close()

	// Load a playlist with a hold item that will block.
	pl := testPlaylist(holdClipItem("hold-1"))
	if err := m.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("LoadPlaylist: %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for on-air.
	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "on_air"
	})

	// Stop mid-playback.
	if err := m.Stop(1); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	states := m.ChannelStates()
	if states[0].State != "stopped" {
		t.Errorf("state after stop = %q, want %q", states[0].State, "stopped")
	}

	// Verify we can load a new playlist and start again.
	pl2 := testPlaylist(clipItem("new-clip"))
	if err := m.LoadPlaylist(1, pl2); err != nil {
		t.Fatalf("LoadPlaylist (second): %v", err)
	}
	if err := m.Start(1); err != nil {
		t.Fatalf("Start (second): %v", err)
	}

	waitFor(t, 5*time.Second, func() bool {
		states := m.ChannelStates()
		return states[0].State == "completed"
	})
}

func TestIntegration_TwoChannels(t *testing.T) {
	m := NewManager(ManagerConfig{
		DemuxFunc: fakeDemux(10),
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
	pl1 := testPlaylist(clipItem("ch1-a"), clipItem("ch1-b"))
	if err := m.LoadPlaylist(1, pl1); err != nil {
		t.Fatalf("LoadPlaylist ch1: %v", err)
	}

	// Channel 2: 3 clips.
	pl2 := testPlaylist(clipItem("ch2-a"), clipItem("ch2-b"), clipItem("ch2-c"))
	if err := m.LoadPlaylist(2, pl2); err != nil {
		t.Fatalf("LoadPlaylist ch2: %v", err)
	}

	// Start both channels.
	if err := m.Start(1); err != nil {
		t.Fatalf("Start ch1: %v", err)
	}
	if err := m.Start(2); err != nil {
		t.Fatalf("Start ch2: %v", err)
	}

	// Wait for both to complete.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		waitFor(t, 10*time.Second, func() bool {
			states := m.ChannelStates()
			return states[0].State == "completed"
		})
	}()

	go func() {
		defer wg.Done()
		waitFor(t, 10*time.Second, func() bool {
			states := m.ChannelStates()
			return states[1].State == "completed"
		})
	}()

	wg.Wait()

	states := m.ChannelStates()
	if states[0].State != "completed" {
		t.Errorf("ch1 final state = %q, want %q", states[0].State, "completed")
	}
	if states[1].State != "completed" {
		t.Errorf("ch2 final state = %q, want %q", states[1].State, "completed")
	}

	// Channel 1: 2 clips x 10 frames = 20 frames.
	if ch1Frames.Load() < 20 {
		t.Errorf("ch1 frames = %d, want >= 20", ch1Frames.Load())
	}

	// Channel 2: 3 clips x 10 frames = 30 frames.
	if ch2Frames.Load() < 30 {
		t.Errorf("ch2 frames = %d, want >= 30", ch2Frames.Load())
	}
}
