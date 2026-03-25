package playout

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchdog_DetectsStall(t *testing.T) {
	var stallID atomic.Int32
	stallID.Store(-1)

	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 100 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
		OnStall: func(channelID int) {
			stallID.Store(int32(channelID))
		},
	})

	w.RegisterChannel(1)
	w.ReportFrame(1) // one frame, then silence
	w.Start()
	defer w.Stop()

	waitFor(t, 2*time.Second, func() bool {
		return stallID.Load() == 1
	})

	// Verify Health() reports stalled.
	healths := w.Health()
	if len(healths) != 1 {
		t.Fatalf("Health() returned %d entries, want 1", len(healths))
	}
	if !healths[0].Stalled {
		t.Error("Health() should report Stalled=true")
	}
	if healths[0].Healthy {
		t.Error("Health() should report Healthy=false when stalled")
	}
}

func TestWatchdog_NoStallWhenFramesFlowing(t *testing.T) {
	var stallCount atomic.Int32

	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 100 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})

	w.RegisterChannel(1)
	w.Start()
	defer w.Stop()

	// Report frames every 20ms for 400ms -- well within the 100ms threshold.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			w.ReportFrame(1)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	<-done

	if stallCount.Load() != 0 {
		t.Errorf("stall callback fired %d times, want 0", stallCount.Load())
	}

	// Health should show healthy.
	healths := w.Health()
	if len(healths) != 1 {
		t.Fatalf("Health() returned %d entries, want 1", len(healths))
	}
	if !healths[0].Healthy {
		t.Error("Health() should report Healthy=true when frames are flowing")
	}
	if healths[0].Stalled {
		t.Error("Health() should report Stalled=false when frames are flowing")
	}
}

func TestWatchdog_UnregisteredChannelIgnored(t *testing.T) {
	var stallCount atomic.Int32

	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 50 * time.Millisecond,
		CheckInterval:  10 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})

	w.Start()
	defer w.Stop()

	// Should not panic and should not fire stall.
	w.ReportFrame(99)

	time.Sleep(100 * time.Millisecond)

	if stallCount.Load() != 0 {
		t.Errorf("stall callback fired %d times, want 0", stallCount.Load())
	}

	// Health should be empty.
	healths := w.Health()
	if len(healths) != 0 {
		t.Fatalf("Health() returned %d entries, want 0", len(healths))
	}
}

func TestWatchdog_Recovery(t *testing.T) {
	var stallCount atomic.Int32

	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 80 * time.Millisecond,
		CheckInterval:  20 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})

	w.RegisterChannel(1)
	w.ReportFrame(1)
	w.Start()
	defer w.Stop()

	// Wait for stall.
	waitFor(t, 2*time.Second, func() bool {
		return stallCount.Load() == 1
	})

	// Resume frames -- this should clear the stalled flag.
	w.ReportFrame(1)

	// Health should now show healthy again.
	healths := w.Health()
	found := false
	for _, h := range healths {
		if h.ChannelID == 1 {
			found = true
			if h.Stalled {
				t.Error("Health() should report Stalled=false after recovery")
			}
			if !h.Healthy {
				t.Error("Health() should report Healthy=true after recovery")
			}
		}
	}
	if !found {
		t.Error("channel 1 not found in Health()")
	}

	// After recovery, a second stall should re-fire the callback.
	time.Sleep(150 * time.Millisecond)
	waitFor(t, 2*time.Second, func() bool {
		return stallCount.Load() == 2
	})
}

func TestWatchdog_UnregisterChannel(t *testing.T) {
	var stallCount atomic.Int32

	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 80 * time.Millisecond,
		CheckInterval:  20 * time.Millisecond,
		OnStall: func(channelID int) {
			stallCount.Add(1)
		},
	})

	w.RegisterChannel(1)
	w.ReportFrame(1)
	w.Start()
	defer w.Stop()

	// Unregister before stall can fire.
	w.UnregisterChannel(1)

	time.Sleep(150 * time.Millisecond)

	if stallCount.Load() != 0 {
		t.Errorf("stall callback fired %d times after unregister, want 0", stallCount.Load())
	}

	healths := w.Health()
	if len(healths) != 0 {
		t.Errorf("Health() returned %d entries after unregister, want 0", len(healths))
	}
}

func TestWatchdog_Defaults(t *testing.T) {
	w := NewWatchdog(WatchdogConfig{})

	// Check that defaults are applied.
	if w.config.StallThreshold != 5*time.Second {
		t.Errorf("default StallThreshold = %v, want 5s", w.config.StallThreshold)
	}
	if w.config.CheckInterval != 1*time.Second {
		t.Errorf("default CheckInterval = %v, want 1s", w.config.CheckInterval)
	}
}

func TestWatchdog_StopIdempotent(t *testing.T) {
	w := NewWatchdog(WatchdogConfig{
		StallThreshold: 100 * time.Millisecond,
		CheckInterval:  25 * time.Millisecond,
	})
	w.Start()
	w.Stop()
	w.Stop() // second stop should not panic
}
