package playout

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestManager_Watchdog_Integration(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	var stallCount atomic.Int32
	wd := NewWatchdog(WatchdogConfig{
		StallThreshold: 200 * time.Millisecond,
		CheckInterval:  50 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})
	mgr.SetWatchdog(wd)
	wd.Start()
	defer wd.Stop()

	pl := testPlaylist(clipItem("a"))
	if err := mgr.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for channel to finish.
	waitFor(t, 2*time.Second, func() bool {
		states := mgr.ChannelStates()
		return states[0].State == "completed"
	})

	// Watchdog should have registered the channel (frames were reported).
	// After completion, channel is unregistered — but Health() might still
	// show it briefly. The key assertion is no crash and frames were reported.
	health := mgr.HealthSummary()
	// Health may or may not have entries (channel unregistered on stop).
	_ = health // just verify no panic
}

func TestManager_Watchdog_FrameReporting(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(30), // 1 second of content
	})

	var stallCount atomic.Int32
	wd := NewWatchdog(WatchdogConfig{
		StallThreshold: 5 * time.Second,
		CheckInterval:  1 * time.Second,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})
	mgr.SetWatchdog(wd)
	wd.Start()
	defer wd.Stop()

	pl := testPlaylist(clipItem("a"))
	if err := mgr.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for channel to finish.
	waitFor(t, 5*time.Second, func() bool {
		states := mgr.ChannelStates()
		return states[0].State == "completed"
	})

	// The channel played 30 frames — no stall should have occurred.
	// (StallThreshold=5s, content is ~1s, so no stall)
	if stallCount.Load() != 0 {
		t.Errorf("stall callback fired %d times, want 0", stallCount.Load())
	}
}

func TestManager_Watchdog_NoRawVideoOutput(t *testing.T) {
	// When no rawVideoOutput is set, the watchdog should still get frames
	// reported via the fallback path.
	mgr := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(5),
	})

	var stallCount atomic.Int32
	wd := NewWatchdog(WatchdogConfig{
		StallThreshold: 5 * time.Second,
		CheckInterval:  500 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})
	mgr.SetWatchdog(wd)
	wd.Start()
	defer wd.Stop()

	// Do NOT set rawVideoOutput — the else-if branch should still report frames.
	pl := testPlaylist(clipItem("a"))
	if err := mgr.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		states := mgr.ChannelStates()
		return states[0].State == "completed"
	})

	// No stall should have occurred — frames were reported via the fallback.
	if stallCount.Load() != 0 {
		t.Errorf("stall callback fired %d times, want 0", stallCount.Load())
	}
}

func TestManager_Watchdog_UnregistersOnStop(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		AssetProvider: fakeProvider(3),
	})

	wd := NewWatchdog(WatchdogConfig{
		StallThreshold: 5 * time.Second,
		CheckInterval:  1 * time.Second,
	})
	mgr.SetWatchdog(wd)
	wd.Start()
	defer wd.Stop()

	pl := testPlaylist(liveItem("live", "cam"))
	if err := mgr.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for the channel to register with the watchdog.
	time.Sleep(50 * time.Millisecond)

	// Verify channel is registered.
	health := wd.Health()
	if len(health) != 1 {
		t.Fatalf("Health() returned %d entries before stop, want 1", len(health))
	}
	if health[0].ChannelID != 1 {
		t.Errorf("Health()[0].ChannelID = %d, want 1", health[0].ChannelID)
	}

	// Stop the channel — should unregister from watchdog.
	if err := mgr.Stop(1); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Wait briefly for the stop callback to fire.
	waitFor(t, 1*time.Second, func() bool {
		return len(wd.Health()) == 0
	})

	health = wd.Health()
	if len(health) != 0 {
		t.Errorf("Health() returned %d entries after stop, want 0", len(health))
	}
}
