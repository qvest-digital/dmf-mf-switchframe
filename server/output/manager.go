package output

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/metrics"
)

// asyncAdapterBufSize is the default buffer size for async adapter wrappers.
// Each slot holds one muxed TS packet batch. At ~188*7 bytes per batch and
// 30fps video, 256 slots provides ~8 seconds of buffering.
const asyncAdapterBufSize = 256

// OutputManager orchestrates the outputViewer, TSMuxer, and output adapters.
// It auto-starts the viewer (registers on the program relay) when the first
// output is enabled and removes it when the last output is disabled, ensuring
// zero CPU overhead when no outputs are active.
type OutputManager struct {
	log   *slog.Logger
	relay *distribution.Relay

	mu       sync.Mutex
	viewer   *OutputViewer
	muxer    *TSMuxer
	viewerWg sync.WaitGroup // tracks the viewer Run goroutine

	recorder     *FileRecorder
	srtOutput    OutputAdapter // SRTCaller or SRTListener (legacy single output)
	destinations map[string]*OutputDestination
	adapters     []OutputAdapter

	// asyncWrappers tracks AsyncAdapter wrappers by inner adapter ID,
	// so they can be stopped when adapters are removed.
	asyncWrappers map[string]*AsyncAdapter

	onState func() // triggers ControlRoomState broadcast
	closed  bool

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// SRT wiring functions injected from main.go.
	srtConnectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)
	srtAcceptFn  func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error

	// Confidence monitor for program output thumbnails.
	confidence *ConfidenceMonitor
}

// NewOutputManager creates an OutputManager bound to the given program relay.
func NewOutputManager(relay *distribution.Relay) *OutputManager {
	return &OutputManager{
		log:          slog.With("component", "output"),
		relay:        relay,
		destinations: make(map[string]*OutputDestination),
	}
}

// SetSRTWiring injects real SRT connection functions. Called from main.go
// after construction so the output package stays testable without srtgo.
func (m *OutputManager) SetSRTWiring(
	connectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error),
	acceptFn func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.srtConnectFn = connectFn
	m.srtAcceptFn = acceptFn
}

// SetMetrics attaches Prometheus metrics to the output manager.
func (m *OutputManager) SetMetrics(pm *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promMetrics = pm
}

// OnStateChange registers a callback fired when output state changes.
// The callback is invoked outside the manager's lock to avoid deadlock
// with the state publisher.
func (m *OutputManager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onState = fn
}

// StartRecording begins recording program output to a file.
// Returns an error if recording is already active.
func (m *OutputManager) StartRecording(config RecorderConfig) error {
	m.mu.Lock()
	if m.recorder != nil {
		m.mu.Unlock()
		return ErrRecorderActive
	}

	rec := NewFileRecorder(config)
	if err := rec.Start(context.TODO()); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start recorder: %w", err)
	}

	m.recorder = rec
	stale := m.rebuildAdaptersLocked()
	m.ensureMuxerLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Notify outside lock (lock discipline).
	if fn != nil {
		fn()
	}

	m.log.Info("recording started", "dir", config.Dir, "file", rec.Filename())
	return nil
}

// StopRecording stops the active recording. Returns an error if no
// recording is active.
func (m *OutputManager) StopRecording() error {
	m.mu.Lock()
	if m.recorder == nil {
		m.mu.Unlock()
		return ErrRecorderNotActive
	}

	rec := m.recorder
	m.recorder = nil
	stale := m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close the recorder outside the lock.
	if err := rec.Close(); err != nil {
		m.log.Error("error closing recorder", "err", err)
	}

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	m.log.Info("recording stopped")
	return nil
}

// StartSRTOutput begins SRT output with the given configuration.
// Returns an error if SRT output is already active.
func (m *OutputManager) StartSRTOutput(config SRTOutputConfig) error {
	m.mu.Lock()
	if m.srtOutput != nil {
		m.mu.Unlock()
		return ErrSRTActive
	}

	var adapter OutputAdapter
	switch config.Mode {
	case "caller":
		caller := NewSRTCaller(SRTCallerConfig{
			Address:  config.Address,
			Port:     config.Port,
			Latency:  config.Latency,
			StreamID: config.StreamID,
		})
		if m.srtConnectFn != nil {
			caller.connectFn = m.srtConnectFn
		}
		caller.onReconnect = func(overflowed bool) {
			if overflowed {
				m.log.Warn("SRT ring buffer overflowed during reconnect, data was lost",
					"address", config.Address, "port", config.Port)
			}
			m.mu.Lock()
			fn := m.onState
			pm := m.promMetrics
			m.mu.Unlock()
			if pm != nil {
				pm.SRTReconnectsTotal.Inc()
				if overflowed {
					pm.RingbufOverflowsTotal.Inc()
				}
			}
			if fn != nil {
				fn()
			}
		}
		adapter = caller
	case "listener":
		listener := NewSRTListener(SRTListenerConfig{
			Port:    config.Port,
			Latency: config.Latency,
		})
		if m.srtAcceptFn != nil {
			lCfg := listener.config
			listener.acceptFn = func(ctx context.Context, _ SRTListenerConfig) error {
				return m.srtAcceptFn(ctx, lCfg, listener)
			}
		}
		adapter = listener
	default:
		m.mu.Unlock()
		return fmt.Errorf("unknown SRT mode: %s", config.Mode)
	}

	ctx := context.Background()
	if err := adapter.Start(ctx); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start SRT output: %w", err)
	}

	m.srtOutput = adapter
	stale := m.rebuildAdaptersLocked()
	m.ensureMuxerLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	m.log.Info("SRT output started", "mode", config.Mode, "port", config.Port)
	return nil
}

// StopSRTOutput stops the active SRT output. Returns an error if no
// SRT output is active.
func (m *OutputManager) StopSRTOutput() error {
	m.mu.Lock()
	if m.srtOutput == nil {
		m.mu.Unlock()
		return ErrSRTNotActive
	}

	adapter := m.srtOutput
	m.srtOutput = nil
	stale := m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close the adapter outside the lock.
	if err := adapter.Close(); err != nil {
		m.log.Error("error closing SRT output", "err", err)
	}

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	m.log.Info("SRT output stopped")
	return nil
}

// --- Multi-destination management ---

// AddDestination creates a new output destination with the given config.
// The destination is created in stopped state and must be started explicitly.
// Returns the generated destination ID.
func (m *OutputManager) AddDestination(config DestinationConfig) (string, error) {
	// Validate config.
	switch config.Type {
	case "srt-caller":
		if config.Address == "" {
			return "", fmt.Errorf("address is required for srt-caller")
		}
	case "srt-listener":
		// No address needed.
	default:
		return "", fmt.Errorf("unsupported destination type: %s", config.Type)
	}
	if config.Port <= 0 {
		return "", fmt.Errorf("port is required")
	}

	id := generateDestinationID()
	dest := &OutputDestination{
		id:        id,
		config:    config,
		createdAt: time.Now(),
	}

	m.mu.Lock()
	m.destinations[id] = dest
	m.mu.Unlock()

	m.log.Info("destination added", "id", id, "type", config.Type, "name", config.Name)
	return id, nil
}

// RemoveDestination removes a destination by ID. If the destination is active,
// it is stopped first. Returns ErrDestinationNotFound if the ID doesn't exist.
func (m *OutputManager) RemoveDestination(id string) error {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}

	// If active, stop it.
	var adapterToClose OutputAdapter
	var stale []*AsyncAdapter
	if dest.active {
		dest.mu.Lock()
		adapterToClose = dest.adapter
		dest.adapter = nil
		dest.async = nil
		dest.active = false
		dest.mu.Unlock()

		stale = m.rebuildAdaptersLocked()
		m.stopMuxerIfNoAdaptersLocked()
	}

	delete(m.destinations, id)
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close adapter outside lock.
	if adapterToClose != nil {
		if err := adapterToClose.Close(); err != nil {
			m.log.Error("error closing destination adapter on remove", "id", id, "err", err)
		}
	}

	if fn != nil && dest.active {
		fn()
	}

	m.log.Info("destination removed", "id", id)
	return nil
}

// StartDestination starts the adapter for the given destination ID.
// Returns ErrDestinationNotFound if the ID doesn't exist, or
// ErrDestinationActive if already running.
func (m *OutputManager) StartDestination(id string) error {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}

	dest.mu.Lock()
	if dest.active {
		dest.mu.Unlock()
		m.mu.Unlock()
		return ErrDestinationActive
	}

	// Create adapter based on config type.
	var adapter OutputAdapter
	switch dest.config.Type {
	case "srt-caller":
		caller := NewSRTCaller(SRTCallerConfig{
			Address:  dest.config.Address,
			Port:     dest.config.Port,
			Latency:  dest.config.Latency,
			StreamID: dest.config.StreamID,
		})
		if m.srtConnectFn != nil {
			caller.connectFn = m.srtConnectFn
		}
		caller.onReconnect = func(overflowed bool) {
			if overflowed {
				m.log.Warn("SRT ring buffer overflowed during reconnect",
					"id", id, "address", dest.config.Address, "port", dest.config.Port)
			}
			m.mu.Lock()
			fn := m.onState
			pm := m.promMetrics
			m.mu.Unlock()
			if pm != nil {
				pm.SRTReconnectsTotal.Inc()
				if overflowed {
					pm.RingbufOverflowsTotal.Inc()
				}
			}
			if fn != nil {
				fn()
			}
		}
		adapter = caller

	case "srt-listener":
		maxConns := dest.config.MaxConns
		if maxConns == 0 {
			maxConns = defaultMaxConns
		}
		listener := NewSRTListener(SRTListenerConfig{
			Port:     dest.config.Port,
			Latency:  dest.config.Latency,
			MaxConns: maxConns,
		})
		if m.srtAcceptFn != nil {
			lCfg := listener.config
			listener.acceptFn = func(ctx context.Context, _ SRTListenerConfig) error {
				return m.srtAcceptFn(ctx, lCfg, listener)
			}
		}
		adapter = listener
	}

	ctx := context.Background()
	if err := adapter.Start(ctx); err != nil {
		dest.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("start destination %s: %w", id, err)
	}

	now := time.Now()
	dest.adapter = adapter
	dest.active = true
	dest.startedAt = &now
	dest.mu.Unlock()

	stale := m.rebuildAdaptersLocked()
	m.ensureMuxerLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	if fn != nil {
		fn()
	}

	m.log.Info("destination started", "id", id, "type", dest.config.Type)
	return nil
}

// StopDestination stops the adapter for the given destination ID.
// The destination remains in the list and can be restarted.
// Returns ErrDestinationNotFound if the ID doesn't exist, or
// ErrDestinationStopped if not currently active.
func (m *OutputManager) StopDestination(id string) error {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}

	dest.mu.Lock()
	if !dest.active {
		dest.mu.Unlock()
		m.mu.Unlock()
		return ErrDestinationStopped
	}

	adapter := dest.adapter
	dest.adapter = nil
	dest.async = nil
	dest.active = false
	dest.mu.Unlock()

	stale := m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close adapter outside the lock.
	if adapter != nil {
		if err := adapter.Close(); err != nil {
			m.log.Error("error closing destination adapter on stop", "id", id, "err", err)
		}
	}

	if fn != nil {
		fn()
	}

	m.log.Info("destination stopped", "id", id)
	return nil
}

// ListDestinations returns the status of all configured destinations.
func (m *OutputManager) ListDestinations() []DestinationStatus {
	m.mu.Lock()
	dests := make([]*OutputDestination, 0, len(m.destinations))
	for _, d := range m.destinations {
		dests = append(dests, d)
	}
	m.mu.Unlock()

	result := make([]DestinationStatus, 0, len(dests))
	for _, d := range dests {
		result = append(result, d.status())
	}
	return result
}

// GetDestination returns the status of a single destination by ID.
// Returns ErrDestinationNotFound if the ID doesn't exist.
func (m *OutputManager) GetDestination(id string) (DestinationStatus, error) {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	m.mu.Unlock()

	if !ok {
		return DestinationStatus{}, ErrDestinationNotFound
	}
	return dest.status(), nil
}

// RecordingStatus returns the current recording status for inclusion in
// ControlRoomState. Safe to call at any time.
func (m *OutputManager) RecordingStatus() RecordingStatus {
	m.mu.Lock()
	rec := m.recorder
	var wrapper *AsyncAdapter
	if rec != nil && m.asyncWrappers != nil {
		wrapper = m.asyncWrappers[rec.ID()]
	}
	m.mu.Unlock()

	if rec == nil {
		return RecordingStatus{Active: false}
	}
	status := rec.RecordingStatusSnapshot()
	if wrapper != nil {
		status.DroppedPackets = wrapper.Dropped()
	}
	return status
}

// SRTOutputStatus returns the current SRT output status for inclusion in
// ControlRoomState. Safe to call at any time.
func (m *OutputManager) SRTOutputStatus() SRTOutputStatus {
	m.mu.Lock()
	adapter := m.srtOutput
	var wrapper *AsyncAdapter
	if adapter != nil && m.asyncWrappers != nil {
		wrapper = m.asyncWrappers[adapter.ID()]
	}
	m.mu.Unlock()

	if adapter == nil {
		return SRTOutputStatus{Active: false}
	}

	var status SRTOutputStatus
	switch a := adapter.(type) {
	case *SRTCaller:
		status = a.SRTStatusSnapshot()
	case *SRTListener:
		status = a.SRTStatusSnapshot()
	default:
		return SRTOutputStatus{Active: false}
	}
	if wrapper != nil {
		status.DroppedPackets = wrapper.Dropped()
	}
	return status
}

// Close stops all outputs, the muxer, and the viewer. Safe to call
// multiple times.
func (m *OutputManager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true

	rec := m.recorder
	srt := m.srtOutput
	m.recorder = nil
	m.srtOutput = nil
	m.adapters = nil

	// Snapshot active destinations and clear them.
	var destAdapters []OutputAdapter
	for _, dest := range m.destinations {
		dest.mu.Lock()
		if dest.active && dest.adapter != nil {
			destAdapters = append(destAdapters, dest.adapter)
			dest.adapter = nil
			dest.async = nil
			dest.active = false
		}
		dest.mu.Unlock()
	}
	m.destinations = make(map[string]*OutputDestination)

	// Snapshot and clear async wrappers so we can stop them outside the lock.
	wrappers := m.asyncWrappers
	m.asyncWrappers = nil

	cm := m.confidence
	m.confidence = nil
	m.stopMuxerLocked()
	m.mu.Unlock()

	// Close the confidence monitor's decoder.
	if cm != nil {
		cm.Close()
	}

	// Stop async wrappers first so no more writes reach inner adapters.
	for _, w := range wrappers {
		w.Stop()
	}

	// Close adapters outside the lock.
	if rec != nil {
		if err := rec.Close(); err != nil {
			m.log.Error("error closing recorder on shutdown", "err", err)
		}
	}
	if srt != nil {
		if err := srt.Close(); err != nil {
			m.log.Error("error closing SRT output on shutdown", "err", err)
		}
	}
	for _, a := range destAdapters {
		if err := a.Close(); err != nil {
			m.log.Error("error closing destination on shutdown", "err", err)
		}
	}

	return nil
}

// rebuildAdaptersLocked rebuilds the adapter slice from the active outputs.
// Each adapter is wrapped in an AsyncAdapter for non-blocking writes.
// Wrappers for removed adapters are stopped; new wrappers are started.
// Must be called with m.mu held.
// rebuildAdaptersLocked rebuilds the adapter slice and returns any stale
// async wrappers that should be stopped. Callers MUST stop the returned
// wrappers outside the lock to avoid blocking the muxer output callback.
func (m *OutputManager) rebuildAdaptersLocked() []*AsyncAdapter {
	// Collect the current set of raw adapters.
	raw := make(map[string]OutputAdapter)
	if m.recorder != nil {
		raw[m.recorder.ID()] = m.recorder
	}
	if m.srtOutput != nil {
		raw[m.srtOutput.ID()] = m.srtOutput
	}
	// Include active destination adapters.
	for id, dest := range m.destinations {
		dest.mu.Lock()
		if dest.active && dest.adapter != nil {
			// Use destination ID as key to avoid collisions with legacy adapter IDs.
			raw["dest:"+id] = dest.adapter
		}
		dest.mu.Unlock()
	}

	if m.asyncWrappers == nil {
		m.asyncWrappers = make(map[string]*AsyncAdapter)
	}

	// Collect stale wrappers for adapters that are no longer present.
	// Do NOT stop them here — caller stops outside the lock.
	var stale []*AsyncAdapter
	for id, wrapper := range m.asyncWrappers {
		if _, ok := raw[id]; !ok {
			stale = append(stale, wrapper)
			delete(m.asyncWrappers, id)
		}
	}

	// Create wrappers for new adapters, reuse existing ones.
	var adapters []OutputAdapter
	for id, adapter := range raw {
		wrapper, exists := m.asyncWrappers[id]
		if !exists {
			wrapper = NewAsyncAdapter(adapter, asyncAdapterBufSize)
			// Start the drain goroutine. We pass a background context
			// since the inner adapter's Start() was already called.
			wrapper.startDrain()
			m.asyncWrappers[id] = wrapper
		}
		adapters = append(adapters, wrapper)
	}

	m.adapters = adapters
	return stale
}

// ensureMuxerLocked creates the muxer, viewer, and registers the viewer
// on the relay if they don't already exist. Must be called with m.mu held.
func (m *OutputManager) ensureMuxerLocked() {
	if m.viewer != nil {
		// Already running.
		return
	}

	muxer := NewTSMuxer()
	muxer.SetOutput(func(tsData []byte) {
		// Snapshot the adapter list under lock, write outside lock.
		m.mu.Lock()
		snapshot := make([]OutputAdapter, len(m.adapters))
		copy(snapshot, m.adapters)
		m.mu.Unlock()

		for _, a := range snapshot {
			if _, err := a.Write(tsData); err != nil {
				m.log.Error("adapter write error", "adapter", a.ID(), "err", err)
			}
		}
	})

	var onVideo func(*media.VideoFrame)
	if m.confidence != nil {
		onVideo = m.confidence.IngestVideo
	}
	viewer := NewOutputViewer(muxer, onVideo)
	m.muxer = muxer
	m.viewer = viewer

	// Start the viewer's drain goroutine.
	m.viewerWg.Add(1)
	go func() {
		defer m.viewerWg.Done()
		viewer.Run()
	}()

	// Register viewer on the relay so it receives program frames.
	m.relay.AddViewer(viewer)

	m.log.Info("output pipeline started")
}

// stopMuxerIfNoAdaptersLocked tears down the muxer and viewer if no
// adapters remain. Must be called with m.mu held.
func (m *OutputManager) stopMuxerIfNoAdaptersLocked() {
	if len(m.adapters) > 0 || m.viewer == nil {
		return
	}
	m.stopMuxerLocked()
}

// stopMuxerLocked tears down the muxer and viewer unconditionally.
// Must be called with m.mu held. Temporarily releases the lock while
// waiting for the viewer goroutine to exit, to avoid deadlock with
// the muxer output callback which also acquires m.mu.
func (m *OutputManager) stopMuxerLocked() {
	if m.viewer == nil {
		return
	}

	viewer := m.viewer
	muxer := m.muxer
	m.viewer = nil
	m.muxer = nil

	// Remove viewer from relay first so no new frames arrive.
	m.relay.RemoveViewer(viewer.ID())

	// Release the lock before blocking on viewer stop. The viewer's
	// drain loop invokes the muxer output callback which needs m.mu.
	// Without releasing here, we'd deadlock.
	m.mu.Unlock()

	// Stop the viewer (signals its drain goroutine to exit).
	viewer.Stop()

	// Wait for the viewer goroutine to finish.
	m.viewerWg.Wait()

	// Close the muxer.
	if err := muxer.Close(); err != nil {
		m.log.Error("error closing muxer", "err", err)
	}

	// Re-acquire the lock (callers expect it held on return).
	m.mu.Lock()

	m.log.Info("output pipeline stopped")
}

// Status returns a summary of all active outputs. This is a convenience
// method for diagnostics.
func (m *OutputManager) Status() OutputManagerStatus {
	return OutputManagerStatus{
		Recording: m.RecordingStatus(),
		SRT:       m.SRTOutputStatus(),
	}
}

// OutputManagerStatus is the combined status of all outputs.
type OutputManagerStatus struct {
	Recording RecordingStatus  `json:"recording"`
	SRT       SRTOutputStatus  `json:"srt"`
}

// DebugSnapshot implements debug.SnapshotProvider.
func (m *OutputManager) DebugSnapshot() map[string]any {
	m.mu.Lock()
	var viewerSnap map[string]any
	if m.viewer != nil {
		viewerSnap = m.viewer.DebugSnapshot()
	}
	m.mu.Unlock()

	snap := map[string]any{
		"viewer":    viewerSnap,
		"recording": m.RecordingStatus(),
		"srt":       m.SRTOutputStatus(),
	}

	if dests := m.ListDestinations(); len(dests) > 0 {
		snap["destinations"] = dests
	}

	return snap
}

// SetConfidenceMonitor attaches a confidence monitor to the output manager.
// The monitor will receive video frames from the output viewer when active.
// Must be called before any outputs are started (the callback is set at
// viewer construction time for thread safety).
func (m *OutputManager) SetConfidenceMonitor(cm *ConfidenceMonitor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confidence = cm
}

// ConfidenceThumbnail returns the latest JPEG thumbnail from the confidence
// monitor, or nil if unavailable.
func (m *OutputManager) ConfidenceThumbnail() []byte {
	m.mu.Lock()
	cm := m.confidence
	m.mu.Unlock()
	if cm == nil {
		return nil
	}
	return cm.LatestThumbnail()
}

// HasActiveOutputs returns true if at least one output is active.
func (m *OutputManager) HasActiveOutputs() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.adapters) > 0
}

// StartedAt returns when recording started (zero value if not recording).
func (m *OutputManager) StartedAt() time.Time {
	m.mu.Lock()
	rec := m.recorder
	m.mu.Unlock()

	if rec == nil {
		return time.Time{}
	}
	status := rec.Status()
	return status.StartedAt
}
