package switcher

import (
	"context"
	"sync"
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
		want internal.SourceHealthStatus
	}{
		{"fresh", 500 * time.Millisecond, internal.SourceHealthy},
		{"stale", 1500 * time.Millisecond, internal.SourceStale},
		{"no_signal", 5 * time.Second, internal.SourceNoSignal},
		{"offline", 15 * time.Second, internal.SourceOffline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := healthStatusFromAge(tt.age)
			if got != tt.want {
				t.Errorf("healthStatusFromAge(%v) = %q, want %q", tt.age, got, tt.want)
			}
		})
	}
}

func TestHealthMonitorRecordAndStatus(t *testing.T) {
	hm := newHealthMonitor(nil)
	hm.recordFrame("camera1")
	status := hm.status("camera1")
	if status != internal.SourceHealthy {
		t.Errorf("status = %q, want %q", status, internal.SourceHealthy)
	}
	hm.stop()
}

func TestHealthMonitorStaleAfterNoFrames(t *testing.T) {
	hm := newHealthMonitor(nil)
	hm.mu.Lock()
	hm.lastFrame["camera1"] = time.Now().Add(-3 * time.Second)
	hm.mu.Unlock()
	status := hm.status("camera1")
	if status != internal.SourceNoSignal {
		t.Errorf("status = %q, want %q", status, internal.SourceNoSignal)
	}
	hm.stop()
}

func TestHealthMonitorUnknownSource(t *testing.T) {
	hm := newHealthMonitor(nil)
	status := hm.status("nonexistent")
	if status != internal.SourceOffline {
		t.Errorf("status = %q, want %q", status, internal.SourceOffline)
	}
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
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	found := false
	for _, s := range states {
		if s.Sources["cam1"].Status == internal.SourceStale {
			found = true
			break
		}
	}
	mu.Unlock()
	require.True(t, found, "health monitor should broadcast state when source becomes stale")
}
