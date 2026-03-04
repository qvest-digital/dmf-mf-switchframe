package switcher

import (
	"sync"
	"sync/atomic"
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
	lastFrame  sync.Map                                // map[string]*atomic.Int64 (UnixNano)
	lastStatus map[string]internal.SourceHealthStatus  // last broadcast status per source
	running    bool
	stopCh     chan struct{}
}

func newHealthMonitor() *healthMonitor {
	return &healthMonitor{
		sources:    make(map[string]bool),
		lastStatus: make(map[string]internal.SourceHealthStatus),
		stopCh:     make(chan struct{}),
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

func (hm *healthMonitor) status(sourceKey string) internal.SourceHealthStatus {
	now := time.Now()
	return hm.computeStatus(sourceKey, now)
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

// checkForChanges compares current health status of all registered sources
// against their last-known status. Returns true if any source changed.
func (hm *healthMonitor) checkForChanges() bool {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	changed := false
	now := time.Now()
	for key := range hm.sources {
		newStatus := hm.computeStatus(key, now)
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
	delete(hm.lastStatus, sourceKey)
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
