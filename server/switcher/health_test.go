package switcher

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/internal"
)

func TestHealthStatusFromAge(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want SourceHealthStatus
	}{
		{"fresh", 500 * time.Millisecond, SourceHealthy},
		{"stale", 1500 * time.Millisecond, SourceStale},
		{"no_signal", 5 * time.Second, SourceNoSignal},
		{"offline", 15 * time.Second, SourceOffline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := healthStatusFromAge(tt.age)
			require.Equal(t, tt.want, got, "healthStatusFromAge(%v)", tt.age)
		})
	}
}

func TestHealthMonitorRecordAndStatus(t *testing.T) {
	hm := newHealthMonitor()
	hm.recordFrame("camera1", time.Now())
	status := hm.status("camera1")
	require.Equal(t, SourceHealthy, status)
	hm.stop()
}

func TestHealthMonitorStaleAfterNoFrames(t *testing.T) {
	hm := newHealthMonitor()
	v := &atomic.Int64{}
	v.Store(time.Now().Add(-3 * time.Second).UnixNano())
	hm.lastFrame.Store("camera1", v)
	status := hm.status("camera1")
	require.Equal(t, SourceNoSignal, status)
	hm.stop()
}

func TestHealthMonitorUnknownSource(t *testing.T) {
	hm := newHealthMonitor()
	status := hm.status("nonexistent")
	require.Equal(t, SourceOffline, status)
	hm.stop()
}

func TestProactiveHealthBroadcast(t *testing.T) {
	programRelay := newTestRelay()
	sw := New(programRelay)
	defer sw.Close()

	var mu sync.Mutex
	var states []internal.ControlRoomState
	sw.OnStateChange(func(state internal.ControlRoomState) {
		mu.Lock()
		states = append(states, state)
		mu.Unlock()
	})

	relay := newTestRelay()
	sw.RegisterSource("cam1", relay)
	require.NoError(t, sw.Cut(context.Background(), "cam1"))

	// Send a keyframe so the source is initially healthy.
	relay.BroadcastVideo(&media.VideoFrame{PTS: 100, IsKeyframe: true})

	// Clear recorded states from cut and frame activity.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	states = nil
	mu.Unlock()

	// Start health monitor with fast tick for testing.
	sw.StartHealthMonitor(100 * time.Millisecond)

	// Wait for source to become stale (>1s with no frames, per staleThreshold).
	// The health monitor should detect this and publish state.
	// With hysteresis (3 consecutive checks at 100ms), stale detection takes ~1300ms.
	time.Sleep(2000 * time.Millisecond)

	mu.Lock()
	found := false
	for _, s := range states {
		if s.Sources["cam1"].Status == string(SourceStale) {
			found = true
			break
		}
	}
	mu.Unlock()
	require.True(t, found, "health monitor should broadcast state when source becomes stale")
}

func TestHealthHysteresis_DegradationRequiresConsecutiveChecks(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// Initial check establishes the source as healthy (first-time: applies immediately).
	changed := hm.checkForChanges()
	require.True(t, changed, "first-time source should apply status immediately")
	hm.mu.RLock()
	require.Equal(t, SourceHealthy, hm.lastStatus["cam1"])
	hm.mu.RUnlock()

	// Let the source become stale (wait past 1s stale threshold).
	time.Sleep(1100 * time.Millisecond)

	// First degradation check: should NOT transition yet (hysteresis count = 1 of 3).
	changed = hm.checkForChanges()
	require.False(t, changed, "first degradation check should not cause transition (count=1)")

	hm.mu.RLock()
	require.Equal(t, SourceHealthy, hm.lastStatus["cam1"], "status should remain healthy after 1 check")
	hm.mu.RUnlock()

	// Second check: should NOT transition yet (count = 2 of 3).
	changed = hm.checkForChanges()
	require.False(t, changed, "second degradation check should not cause transition (count=2)")

	// Third check: SHOULD transition now (count = 3, meets degradationThreshold).
	changed = hm.checkForChanges()
	require.True(t, changed, "third degradation check should cause transition (count=3)")

	hm.mu.RLock()
	finalStatus := hm.lastStatus["cam1"]
	hm.mu.RUnlock()
	require.Equal(t, SourceStale, finalStatus, "status should be stale after 3 consecutive checks")

	hm.stop()
}

func TestHealthHysteresis_RecoveryIsImmediate(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// Establish initial status as healthy.
	hm.checkForChanges()

	// Let the source become stale (wait past 1s threshold).
	time.Sleep(1100 * time.Millisecond)

	// Run 3 degradation checks to get through hysteresis.
	hm.checkForChanges()
	hm.checkForChanges()
	changed := hm.checkForChanges()
	require.True(t, changed, "third check should trigger stale transition")

	hm.mu.RLock()
	require.Equal(t, SourceStale, hm.lastStatus["cam1"])
	hm.mu.RUnlock()

	// Now record a fresh frame to simulate recovery.
	hm.recordFrame("cam1", time.Now())

	// First check after recovery: should recover immediately (threshold = 1).
	changed = hm.checkForChanges()
	require.True(t, changed, "recovery should be immediate (threshold=1)")

	hm.mu.RLock()
	require.Equal(t, SourceHealthy, hm.lastStatus["cam1"], "status should be healthy after immediate recovery")
	hm.mu.RUnlock()

	hm.stop()
}

func TestHealthHysteresis_IntermittentFramesResetCounter(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// Establish initial status as healthy.
	hm.checkForChanges()

	// Let the source become stale.
	time.Sleep(1100 * time.Millisecond)

	// First degradation check: count=1 toward stale.
	changed := hm.checkForChanges()
	require.False(t, changed, "first check should not trigger transition")

	// Second degradation check: count=2 toward stale.
	changed = hm.checkForChanges()
	require.False(t, changed, "second check should not trigger transition")

	// Now a frame arrives — source is healthy again.
	hm.recordFrame("cam1", time.Now())

	// Next check: source is healthy, which matches current status → reset counter.
	changed = hm.checkForChanges()
	require.False(t, changed, "check after recovery frame should not change (already healthy)")

	// Let source go stale again.
	time.Sleep(1100 * time.Millisecond)

	// Counter should have been reset — need 3 fresh consecutive checks.
	changed = hm.checkForChanges()
	require.False(t, changed, "first check of new stale period should not trigger (count=1)")
	changed = hm.checkForChanges()
	require.False(t, changed, "second check should not trigger (count=2)")
	changed = hm.checkForChanges()
	require.True(t, changed, "third check should trigger stale transition (count=3)")

	hm.stop()
}

func TestHealthStatus_ReturnsCommittedNotRaw(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// Establish initial healthy status.
	hm.checkForChanges()
	require.Equal(t, SourceHealthy, hm.status("cam1"))

	// Let the source become stale (past 1s threshold).
	time.Sleep(1100 * time.Millisecond)

	// One degradation check: hysteresis count = 1 of 3, not committed yet.
	hm.checkForChanges()

	// status() should return the committed status (healthy), not the raw (stale).
	require.Equal(t, SourceHealthy, hm.status("cam1"),
		"status() should return committed hysteresis-filtered status, not raw")

	// rawStatus() should return the actual computed status (stale).
	require.Equal(t, SourceStale, hm.rawStatus("cam1"),
		"rawStatus() should return the instantaneous computed status")

	hm.stop()
}

func TestHealthMonitor_StopWaitsForGoroutine(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	published := make(chan struct{}, 100)
	hm.start(10*time.Millisecond, func() {
		published <- struct{}{}
	})

	// Let the goroutine run a few ticks.
	time.Sleep(50 * time.Millisecond)

	// stop() must block until the goroutine has exited.
	hm.stop()

	// After stop returns, the done channel must be closed.
	hm.mu.RLock()
	done := hm.done
	hm.mu.RUnlock()

	select {
	case <-done:
		// success — goroutine has exited
	default:
		t.Fatal("stop() returned before monitor goroutine exited")
	}
}

func TestHealthMonitor_StopWithoutStart(t *testing.T) {
	// stop() must not block or panic when the monitor was never started.
	hm := newHealthMonitor()
	hm.stop()
}

func TestHealthMonitorDoubleStop(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// Start the monitor so that stopCh and done are active.
	hm.start(10*time.Millisecond, func() {})

	// Let it run briefly.
	time.Sleep(30 * time.Millisecond)

	// Call stop() from 10 concurrent goroutines.
	// The old select/close pattern will panic with "close of closed channel"
	// or trigger a race detector warning.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hm.stop()
		}()
	}
	wg.Wait()
}

func TestHealthHysteresis_FirstSourceAppliesImmediately(t *testing.T) {
	hm := newHealthMonitor()
	hm.registerSource("cam1")
	hm.recordFrame("cam1", time.Now())

	// First check for a new source should apply the initial status immediately.
	changed := hm.checkForChanges()
	require.True(t, changed, "first-time source should apply status immediately")

	hm.mu.RLock()
	require.Equal(t, SourceHealthy, hm.lastStatus["cam1"])
	hm.mu.RUnlock()

	hm.stop()
}
