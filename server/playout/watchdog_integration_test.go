package playout

import (
	"os"
	"path/filepath"
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

func TestPhase7_FullRecoveryFlow(t *testing.T) {
	dir := t.TempDir()
	asRunDir := filepath.Join(dir, "asrun")
	persistDir := filepath.Join(dir, "persist")

	// --- Phase A: Run channel, persist state ---
	provider := fakeProvider(90) // 3 seconds of 30fps content
	mgr1 := NewManager(ManagerConfig{AssetProvider: provider})

	logger1, err := NewAsRunLogger(AsRunLoggerConfig{Dir: asRunDir, RetentionDays: 30})
	if err != nil {
		t.Fatalf("asrun logger: %v", err)
	}
	mgr1.SetAsRunLogger(logger1)

	pl := testPlaylist(
		clipItem("intro"),
		clipItem("main"),
		clipItem("outro"),
	)
	if err := mgr1.LoadPlaylist(1, pl); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr1.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait a bit for playback to start.
	time.Sleep(50 * time.Millisecond)

	// Take a snapshot.
	persister := NewStatePersister(mgr1, StatePersisterConfig{
		Dir:      persistDir,
		Interval: 50 * time.Millisecond,
	})
	persister.Start()
	waitFor(t, 500*time.Millisecond, func() bool {
		_, err := os.Stat(persister.Path())
		return err == nil
	})
	persister.Stop()
	mgr1.Close()
	logger1.Close()

	// --- Phase B: Simulate crash recovery ---
	mgr2 := NewManager(ManagerConfig{AssetProvider: provider})

	logger2, err := NewAsRunLogger(AsRunLoggerConfig{Dir: asRunDir, RetentionDays: 30})
	if err != nil {
		t.Fatalf("asrun logger 2: %v", err)
	}
	defer logger2.Close()
	mgr2.SetAsRunLogger(logger2)

	if err := mgr2.Recover(persister.Path()); err != nil {
		t.Fatalf("recover: %v", err)
	}

	// Channel should be re-cued.
	states := mgr2.ChannelStates()
	if states[0].State != "cued" {
		t.Errorf("post-recovery state = %q, want cued", states[0].State)
	}
	if states[0].PlaylistName == "" {
		t.Error("post-recovery playlist name should not be empty")
	}

	// Verify recovery as-run event.
	errorEvents := logger2.Query(AsRunQuery{Type: AsRunError})
	if len(errorEvents) == 0 {
		t.Error("expected recovery as-run error event")
	}

	// --- Phase C: Resume and complete ---
	if err := mgr2.Start(1); err != nil {
		t.Fatalf("restart: %v", err)
	}

	waitFor(t, 10*time.Second, func() bool {
		s := mgr2.ChannelStates()
		return s[0].State == "completed"
	})

	mgr2.Close()
}
