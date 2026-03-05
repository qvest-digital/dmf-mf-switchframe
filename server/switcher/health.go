package switcher

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

const (
	staleThreshold    = 1 * time.Second
	noSignalThreshold = 2 * time.Second
	offlineThreshold  = 10 * time.Second

	// Hysteresis thresholds: require N consecutive checks at a new status
	// before transitioning, to prevent tally flicker on congested networks.
	degradationThreshold = 3 // require 3 consecutive checks for degradation
	recoveryThreshold    = 1 // instant recovery to healthy
)

func healthStatusFromAge(age time.Duration) internal.SourceHealthStatus {
	switch {
	case age >= offlineThreshold:
		return internal.SourceOffline
	case age >= noSignalThreshold:
		return internal.SourceNoSignal
	case age >= staleThreshold:
		return internal.SourceStale
	default:
		return internal.SourceHealthy
	}
}

type healthMonitor struct {
	mu               sync.RWMutex
	sources          map[string]bool                        // all registered source keys
	lastFrame        sync.Map                               // map[string]*atomic.Int64 (UnixNano)
	lastStatus       map[string]internal.SourceHealthStatus // last broadcast status per source
	pendingStatus    map[string]internal.SourceHealthStatus // status awaiting hysteresis threshold
	consecutiveCount map[string]int                         // consecutive checks at pending status
	running          bool
	stopCh           chan struct{}
}

func newHealthMonitor() *healthMonitor {
	return &healthMonitor{
		sources:          make(map[string]bool),
		lastStatus:       make(map[string]internal.SourceHealthStatus),
		pendingStatus:    make(map[string]internal.SourceHealthStatus),
		consecutiveCount: make(map[string]int),
		stopCh:           make(chan struct{}),
	}
}

func (hm *healthMonitor) registerSource(sourceKey string) {
	hm.mu.Lock()
	hm.sources[sourceKey] = true
	hm.mu.Unlock()
}

func (hm *healthMonitor) recordFrame(sourceKey string) {
	now := time.Now().UnixNano()

	// Load-or-store an *atomic.Int64 for this source.
	if val, ok := hm.lastFrame.Load(sourceKey); ok {
		val.(*atomic.Int64).Store(now)
		return
	}

	// First frame for this source: store a new atomic.
	v := &atomic.Int64{}
	v.Store(now)
	if actual, loaded := hm.lastFrame.LoadOrStore(sourceKey, v); loaded {
		// Another goroutine beat us; use the existing entry.
		actual.(*atomic.Int64).Store(now)
	}
}

// status returns the hysteresis-filtered health status for a source.
// It reads from the committed lastStatus map (set by checkForChanges)
// so that transient jitter does not appear in state broadcasts.
// Falls back to raw computeStatus when no committed status exists yet.
func (hm *healthMonitor) status(sourceKey string) internal.SourceHealthStatus {
	hm.mu.RLock()
	if s, ok := hm.lastStatus[sourceKey]; ok {
		hm.mu.RUnlock()
		return s
	}
	hm.mu.RUnlock()
	return hm.computeStatus(sourceKey, time.Now())
}

// rawStatus returns the instantaneous (non-hysteresis-filtered) health status.
// Used for debug/diagnostic endpoints where the operator wants to see the
// real-time computed status, not the committed broadcast status.
func (hm *healthMonitor) rawStatus(sourceKey string) internal.SourceHealthStatus {
	return hm.computeStatus(sourceKey, time.Now())
}

// lastFrameAgoMs returns how many milliseconds ago the last frame was received
// for the given source. Returns -1 if no frame has ever been recorded.
func (hm *healthMonitor) lastFrameAgoMs(sourceKey string) int64 {
	val, ok := hm.lastFrame.Load(sourceKey)
	if !ok {
		return -1
	}
	ns := val.(*atomic.Int64).Load()
	if ns == 0 {
		return -1
	}
	return time.Since(time.Unix(0, ns)).Milliseconds()
}

// computeStatus computes the health status for a source at a given time.
func (hm *healthMonitor) computeStatus(sourceKey string, now time.Time) internal.SourceHealthStatus {
	val, ok := hm.lastFrame.Load(sourceKey)
	if !ok {
		return internal.SourceOffline
	}
	ns := val.(*atomic.Int64).Load()
	lastTime := time.Unix(0, ns)
	return healthStatusFromAge(now.Sub(lastTime))
}

// start begins a ticker that periodically checks for health status changes
// and calls publishFn when any source's status has changed.
func (hm *healthMonitor) start(interval time.Duration, publishFn func()) {
	hm.mu.Lock()
	if hm.running {
		hm.mu.Unlock()
		return
	}
	hm.running = true
	hm.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if hm.checkForChanges() {
					publishFn()
				}
			case <-hm.stopCh:
				return
			}
		}
	}()
}

// healthChange records a source health status transition for logging outside the lock.
type healthChange struct {
	source     string
	fromStatus internal.SourceHealthStatus
	toStatus   internal.SourceHealthStatus
}

// checkForChanges compares current health status of all registered sources
// against their last-known status. Uses hysteresis to prevent tally flicker:
// degradation requires N consecutive checks, recovery is immediate.
// Returns true if any source's committed status changed.
func (hm *healthMonitor) checkForChanges() bool {
	hm.mu.Lock()

	changed := false
	var changes []healthChange
	now := time.Now()
	for key := range hm.sources {
		newStatus := hm.computeStatus(key, now)

		prev, hasPrev := hm.lastStatus[key]

		// First-time source: apply immediately.
		if !hasPrev {
			hm.lastStatus[key] = newStatus
			changed = true
			delete(hm.pendingStatus, key)
			delete(hm.consecutiveCount, key)
			continue
		}

		// Status matches current committed status: reset hysteresis, no change.
		if newStatus == prev {
			delete(hm.pendingStatus, key)
			delete(hm.consecutiveCount, key)
			continue
		}

		// Status differs from committed status. Determine threshold.
		threshold := degradationThreshold
		if newStatus == internal.SourceHealthy {
			threshold = recoveryThreshold
		}

		// Track consecutive checks at this pending status.
		if pending, ok := hm.pendingStatus[key]; ok && pending == newStatus {
			hm.consecutiveCount[key]++
		} else {
			hm.pendingStatus[key] = newStatus
			hm.consecutiveCount[key] = 1
		}

		// Apply the change if threshold is met.
		if hm.consecutiveCount[key] >= threshold {
			changes = append(changes, healthChange{source: key, fromStatus: prev, toStatus: newStatus})
			hm.lastStatus[key] = newStatus
			changed = true
			delete(hm.pendingStatus, key)
			delete(hm.consecutiveCount, key)
		}
	}
	hm.mu.Unlock()

	// Log outside the lock
	for _, c := range changes {
		slog.Warn("switcher: source health changed",
			"source", c.source,
			"from_status", string(c.fromStatus),
			"to_status", string(c.toStatus))
	}

	return changed
}

func (hm *healthMonitor) removeSource(sourceKey string) {
	hm.mu.Lock()
	delete(hm.sources, sourceKey)
	delete(hm.lastStatus, sourceKey)
	delete(hm.pendingStatus, sourceKey)
	delete(hm.consecutiveCount, sourceKey)
	hm.mu.Unlock()
	hm.lastFrame.Delete(sourceKey)
}

func (hm *healthMonitor) stop() {
	select {
	case <-hm.stopCh:
	default:
		close(hm.stopCh)
	}
}
