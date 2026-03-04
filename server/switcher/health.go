package switcher

import (
	"sync"
	"time"

	"github.com/zsiec/switchframe/server/internal"
)

const (
	staleThreshold    = 1 * time.Second
	noSignalThreshold = 2 * time.Second
	offlineThreshold  = 10 * time.Second
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
	mu         sync.RWMutex
	sources    map[string]bool                         // all registered source keys
	lastFrame  map[string]time.Time                    // last frame time per source
	lastStatus map[string]internal.SourceHealthStatus  // last broadcast status per source
	running    bool
	stopCh     chan struct{}
	callback   func(internal.ControlRoomState)
}

func newHealthMonitor(callback func(internal.ControlRoomState)) *healthMonitor {
	return &healthMonitor{
		sources:    make(map[string]bool),
		lastFrame:  make(map[string]time.Time),
		lastStatus: make(map[string]internal.SourceHealthStatus),
		stopCh:     make(chan struct{}),
		callback:   callback,
	}
}

func (hm *healthMonitor) registerSource(sourceKey string) {
	hm.mu.Lock()
	hm.sources[sourceKey] = true
	hm.mu.Unlock()
}

func (hm *healthMonitor) recordFrame(sourceKey string) {
	hm.mu.Lock()
	hm.lastFrame[sourceKey] = time.Now()
	hm.mu.Unlock()
}

func (hm *healthMonitor) status(sourceKey string) internal.SourceHealthStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.computeStatusLocked(sourceKey, time.Now())
}

// computeStatusLocked computes the health status for a source at a given time.
// Caller must hold at least hm.mu.RLock().
func (hm *healthMonitor) computeStatusLocked(sourceKey string, now time.Time) internal.SourceHealthStatus {
	lastTime, ok := hm.lastFrame[sourceKey]
	if !ok {
		return internal.SourceOffline
	}
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

// checkForChanges compares current health status of all registered sources
// against their last-known status. Returns true if any source changed.
func (hm *healthMonitor) checkForChanges() bool {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	changed := false
	now := time.Now()
	for key := range hm.sources {
		newStatus := hm.computeStatusLocked(key, now)
		if prev, ok := hm.lastStatus[key]; !ok || prev != newStatus {
			hm.lastStatus[key] = newStatus
			changed = true
		}
	}
	return changed
}

func (hm *healthMonitor) removeSource(sourceKey string) {
	hm.mu.Lock()
	delete(hm.sources, sourceKey)
	delete(hm.lastFrame, sourceKey)
	delete(hm.lastStatus, sourceKey)
	hm.mu.Unlock()
}

func (hm *healthMonitor) stop() {
	select {
	case <-hm.stopCh:
	default:
		close(hm.stopCh)
	}
}
