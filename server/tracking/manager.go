package tracking

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Manager manages tracking sources per camera with priority blending
// and Kalman filtering for temporal pose smoothing.
type Manager struct {
	mu      sync.RWMutex
	sources map[string]*sourceEntry // sourceKey → tracking entry
	onPose  func(sourceKey string, pose CameraPose)
	closed  atomic.Bool
	polling atomic.Bool // single-caller guard for Poll()

	pollCancel context.CancelFunc // non-nil when polling goroutine is running
	pollDone   chan struct{}      // closed when polling goroutine exits
}

// sourceEntry holds the trackers and filter state for a single camera source.
type sourceEntry struct {
	trackers   []TrackingSource
	filter     *PoseFilter
	latest     atomic.Pointer[CameraPose]
	intrinsics *CameraIntrinsics
}

// defaultProcessNoise controls how much the filter trusts the motion model.
const defaultProcessNoise = 0.5

// defaultMeasurementNoise controls how much the filter trusts measurements.
const defaultMeasurementNoise = 0.2

// NewManager creates a new tracking manager.
func NewManager() *Manager {
	return &Manager{
		sources: make(map[string]*sourceEntry),
	}
}

// SetOnPose registers a callback invoked when a source's pose is updated
// during Poll(). The callback receives the source key and the filtered pose.
func (m *Manager) SetOnPose(fn func(sourceKey string, pose CameraPose)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPose = fn
}

// AddSource registers a tracking source for a camera. Multiple trackers can
// be added per source key (e.g., both Free-D and visual tracking). Returns
// ErrSourceExists if a tracker with the same Name() is already registered
// for that source key.
func (m *Manager) AddSource(sourceKey string, tracker TrackingSource) error {
	if m.closed.Load() {
		return fmt.Errorf("tracking: manager closed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.sources[sourceKey]
	if !ok {
		entry = &sourceEntry{
			filter: NewPoseFilter(defaultProcessNoise, defaultMeasurementNoise),
		}
		m.sources[sourceKey] = entry
	}

	// Check for duplicate tracker name.
	for _, existing := range entry.trackers {
		if existing.Name() == tracker.Name() {
			return ErrSourceExists
		}
	}

	entry.trackers = append(entry.trackers, tracker)
	return nil
}

// RemoveSource removes a tracker by name from a camera source. The tracker
// is closed. Returns ErrSourceNotFound if the source key or tracker name
// does not exist.
func (m *Manager) RemoveSource(sourceKey, trackerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.sources[sourceKey]
	if !ok {
		return ErrSourceNotFound
	}

	for i, t := range entry.trackers {
		if t.Name() == trackerName {
			_ = t.Close()
			entry.trackers = append(entry.trackers[:i], entry.trackers[i+1:]...)

			// Clean up empty entries.
			if len(entry.trackers) == 0 {
				delete(m.sources, sourceKey)
			}
			return nil
		}
	}

	return ErrSourceNotFound
}

// Sources returns a snapshot of all registered tracking sources and their
// status, keyed by source key.
func (m *Manager) Sources() map[string][]TrackingSourceInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]TrackingSourceInfo, len(m.sources))
	for key, entry := range m.sources {
		infos := make([]TrackingSourceInfo, len(entry.trackers))
		for i, t := range entry.trackers {
			var conf float32
			if p := t.LatestPose(); p != nil {
				conf = p.Confidence
			}
			infos[i] = TrackingSourceInfo{
				Name:       t.Name(),
				Type:       t.Type(),
				Running:    t.Running(),
				Confidence: conf,
			}
		}
		result[key] = infos
	}
	return result
}

// LatestPose returns the most recent filtered pose for a source, or nil
// if no pose data is available.
func (m *Manager) LatestPose(sourceKey string) *CameraPose {
	m.mu.RLock()
	entry, ok := m.sources[sourceKey]
	m.mu.RUnlock()

	if !ok {
		return nil
	}
	return entry.latest.Load()
}

// Poll reads the latest pose from all trackers, selects the highest-
// confidence pose per source, filters it, and stores the result.
// It should be called periodically at frame rate from a goroutine.
func (m *Manager) Poll() {
	if !m.polling.CompareAndSwap(false, true) {
		return // another Poll is already running
	}
	defer m.polling.Store(false)

	m.mu.RLock()
	// Snapshot sources under read lock.
	entries := make(map[string]*sourceEntry, len(m.sources))
	for k, v := range m.sources {
		entries[k] = v
	}
	onPose := m.onPose
	m.mu.RUnlock()

	for sourceKey, entry := range entries {
		best := m.selectBestPose(entry)
		if best == nil {
			continue
		}

		filtered := entry.filter.Update(best)
		entry.latest.Store(filtered)

		if onPose != nil {
			onPose(sourceKey, *filtered)
		}
	}
}

// selectBestPose picks the pose with the highest confidence from the
// entry's trackers. Returns nil if no tracker has data.
func (m *Manager) selectBestPose(entry *sourceEntry) *CameraPose {
	var best *CameraPose
	var bestConf float32 = -1

	for _, t := range entry.trackers {
		pose := t.LatestPose()
		if pose == nil {
			continue
		}
		if pose.Confidence > bestConf {
			best = pose
			bestConf = pose.Confidence
		}
	}

	return best
}

// SetCalibration sets the lens calibration intrinsics for a source.
// Returns ErrSourceNotFound if the source key does not exist.
func (m *Manager) SetCalibration(sourceKey string, cal CameraIntrinsics) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.sources[sourceKey]
	if !ok {
		return ErrSourceNotFound
	}

	c := cal // copy
	entry.intrinsics = &c
	return nil
}

// GetCalibration returns the lens calibration intrinsics for a source,
// or nil if none have been set or the source key is unknown.
func (m *Manager) GetCalibration(sourceKey string) *CameraIntrinsics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.sources[sourceKey]
	if !ok {
		return nil
	}
	return entry.intrinsics
}

// SetArucoPositions sets marker positions on all ArUco detectors registered
// with this manager. Each entry maps a marker ID to its center position
// [X, Y, Z] in meters in the studio coordinate system.
func (m *Manager) SetArucoPositions(positions map[int][3]float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, entry := range m.sources {
		for _, t := range entry.trackers {
			if aruco, ok := t.(*ArucoDetector); ok {
				aruco.SetPositions(positions)
			}
		}
	}
}

// StartPolling starts a background goroutine that calls Poll() at the
// given interval (e.g., 33ms for ~30Hz). Only one polling goroutine
// runs at a time; calling StartPolling while already polling is a no-op.
// Use StopPolling() or Close() to stop.
func (m *Manager) StartPolling(interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.pollCancel != nil || m.closed.Load() {
		return // already polling or closed
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.pollCancel = cancel
	m.pollDone = make(chan struct{})

	go func() {
		defer close(m.pollDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Poll()
			}
		}
	}()
}

// StopPolling stops the background polling goroutine. Safe to call
// when not polling (no-op). Blocks until the goroutine exits.
func (m *Manager) StopPolling() {
	m.mu.Lock()
	cancel := m.pollCancel
	done := m.pollDone
	m.pollCancel = nil
	m.pollDone = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

// Close stops all tracking sources and releases resources. It is
// safe to call multiple times.
func (m *Manager) Close() error {
	if m.closed.Swap(true) {
		return nil
	}

	// Stop polling goroutine first (outside the write lock to avoid deadlock
	// since Poll() takes a read lock).
	m.StopPolling()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range m.sources {
		for _, t := range entry.trackers {
			_ = t.Close()
		}
	}

	return nil
}
