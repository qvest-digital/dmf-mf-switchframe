package output

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/distribution"
	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/metrics"
)

// asyncAdapterBufSize is the default buffer size for async adapter wrappers.
// Each slot holds one muxed TS packet batch. At ~188*7 bytes per batch and
// 30fps video, 256 slots provides ~8 seconds of buffering.
const asyncAdapterBufSize = 256

// scte35InjectorInterface provides synthetic break state for late-joining viewers.
type scte35InjectorInterface interface {
	SyntheticBreakState() []byte
}

// Manager orchestrates the outputViewer, TSMuxer, and output adapters.
// It auto-starts the viewer (registers on the program relay) when the first
// output is enabled and removes it when the last output is disabled, ensuring
// zero CPU overhead when no outputs are active.
//
// Lock ordering: Manager.mu must be acquired before Destination.mu.
// Never acquire Manager.mu while holding a destination lock.
type Manager struct {
	log   *slog.Logger
	relay *distribution.Relay

	mu       sync.Mutex
	viewer   *Viewer
	muxer    *TSMuxer
	viewerWg sync.WaitGroup // tracks the viewer Run goroutine

	recorder     *FileRecorder
	srtOutput    Adapter // SRTCaller or SRTListener (legacy single output)
	destinations map[string]*Destination
	adapters     atomic.Pointer[[]Adapter]

	// asyncWrappers tracks AsyncAdapter wrappers by inner adapter ID,
	// so they can be stopped when adapters are removed.
	asyncWrappers map[string]*AsyncAdapter

	// CBR pacing. When cbrMuxrate > 0, SRT adapters receive null-padded
	// CBR TS via the pacer instead of direct writes. The recorder and
	// non-SRT adapters continue to receive raw variable-rate TS.
	// cbrPacer is accessed from the muxer output callback (lock-free read)
	// and from stopMuxerLocked (write under mu), so it uses atomic.Pointer.
	cbrPacer   atomic.Pointer[CBRPacer]
	cbrMuxrate int64 // target muxrate in bps, 0 = disabled

	// directAdapters holds non-paced adapters (recorder) for the muxer
	// output callback. When CBR is disabled, equals m.adapters.
	directAdapters atomic.Pointer[[]Adapter]

	onState    func() // triggers ControlRoomState broadcast
	onMuxStart func() // triggers IDR keyframe request when muxer starts
	closed     bool
	stopping   bool               // true while stopMuxerLocked is draining the viewer (lock released)
	ctx        context.Context    // cancelled on Close()
	cancel     context.CancelFunc // cancels ctx

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// SRT wiring functions injected from main.go.
	srtConnectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)
	srtAcceptFn  func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error

	// Confidence monitor for program output thumbnails.
	confidence *ConfidenceMonitor

	// SCTE-35 injector provides synthetic break state for late-joining viewers.
	scte35Injector scte35InjectorInterface

	// scte35PID is the configured SCTE-35 MPEG-TS PID. 0 = disabled.
	scte35PID uint16
}

// NewManager creates a Manager bound to the given program relay.
func NewManager(relay *distribution.Relay) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		log:          slog.With("component", "output"),
		relay:        relay,
		destinations: make(map[string]*Destination),
		ctx:          ctx,
		cancel:       cancel,
	}
	empty := make([]Adapter, 0)
	m.adapters.Store(&empty)
	m.directAdapters.Store(&empty)
	return m
}

// SetSRTWiring injects real SRT connection functions. Called from main.go
// after construction so the output package stays testable without srtgo.
func (m *Manager) SetSRTWiring(
	connectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error),
	acceptFn func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.srtConnectFn = connectFn
	m.srtAcceptFn = acceptFn
}

// SetMetrics attaches Prometheus metrics to the output manager.
func (m *Manager) SetMetrics(pm *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promMetrics = pm
}

// SetCBRMuxrate configures CBR pacing for SRT outputs. When muxrateBps > 0,
// SRT adapters receive null-padded CBR TS via the pacer. The recorder
// continues to receive raw variable-rate TS. Must be called before starting
// any outputs.
func (m *Manager) SetCBRMuxrate(muxrateBps int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cbrMuxrate = muxrateBps
}

// OnStateChange registers a callback fired when output state changes.
// The callback is invoked outside the manager's lock to avoid deadlock
// with the state publisher.
func (m *Manager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onState = fn
}

// OnMuxerStart registers a callback fired when the output muxer starts
// (i.e., when the first output is enabled and the viewer joins the relay).
// Used to request an IDR keyframe from the encoder so the muxer can
// initialize immediately instead of waiting for the next GOP boundary.
func (m *Manager) OnMuxerStart(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMuxStart = fn
}

// StartRecording begins recording program output to a file.
// Returns an error if recording is already active.
func (m *Manager) StartRecording(config RecorderConfig) error {
	m.mu.Lock()
	if m.recorder != nil {
		m.mu.Unlock()
		return ErrRecorderActive
	}

	rec := NewFileRecorder(config)
	if err := rec.Start(m.ctx); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start recorder: %w", err)
	}

	m.recorder = rec
	stale := m.rebuildAdaptersLocked()
	muxStarted := m.ensureMuxerLocked()
	fn := m.onState
	muxFn := m.onMuxStart
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Notify outside lock (lock discipline).
	if muxStarted && muxFn != nil {
		muxFn()
	}
	if fn != nil {
		fn()
	}

	m.log.Info("recording started", "dir", config.Dir, "file", rec.Filename())
	return nil
}

// StopRecording stops the active recording. Returns an error if no
// recording is active.
func (m *Manager) StopRecording() error {
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
func (m *Manager) StartSRTOutput(config SRTConfig) error {
	m.mu.Lock()
	if m.srtOutput != nil {
		m.mu.Unlock()
		return ErrSRTActive
	}

	var adapter Adapter
	switch config.Mode {
	case "caller":
		callerCfg := SRTCallerConfig{
			Address:  config.Address,
			Port:     config.Port,
			Latency:  config.Latency,
			StreamID: config.StreamID,
		}
		// Set SRT bandwidth hints from CBR muxrate.
		if m.cbrMuxrate > 0 {
			callerCfg.InputBW = m.cbrMuxrate / 8 // bps → bytes/sec
			callerCfg.OverheadBW = 5             // SRT protocol overhead on top of TS muxrate
		}
		caller := NewSRTCaller(callerCfg)
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
		listenerCfg := SRTListenerConfig{
			Port:    config.Port,
			Latency: config.Latency,
		}
		// Set SRT bandwidth hints from CBR muxrate.
		if m.cbrMuxrate > 0 {
			listenerCfg.InputBW = m.cbrMuxrate / 8
			listenerCfg.OverheadBW = 5
		}
		listener := NewSRTListener(listenerCfg)
		if m.srtAcceptFn != nil {
			lCfg := listener.config
			listener.acceptFn = func(ctx context.Context, _ SRTListenerConfig) error {
				return m.srtAcceptFn(ctx, lCfg, listener)
			}
		}
		// Force IDR keyframe when a new SRT client connects so it can
		// start decoding immediately without waiting up to 2s for the
		// next natural keyframe in the GOP.
		if m.onMuxStart != nil {
			listener.OnConnect(m.onMuxStart)
		}
		adapter = listener
	default:
		m.mu.Unlock()
		return fmt.Errorf("unknown SRT mode: %s", config.Mode)
	}

	if err := adapter.Start(m.ctx); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start SRT output: %w", err)
	}

	m.srtOutput = adapter
	stale := m.rebuildAdaptersLocked()
	muxStarted := m.ensureMuxerLocked()
	fn := m.onState
	muxFn := m.onMuxStart
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Notify outside lock (lock discipline).
	if muxStarted && muxFn != nil {
		muxFn()
	}
	if fn != nil {
		fn()
	}

	m.log.Info("SRT output started", "mode", config.Mode, "port", config.Port)
	return nil
}

// StopSRTOutput stops the active SRT output. Returns an error if no
// SRT output is active.
func (m *Manager) StopSRTOutput() error {
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
func (m *Manager) AddDestination(config DestinationConfig) (string, error) {
	// Validate config.
	switch config.Type {
	case "srt-caller":
		if config.Address == "" {
			return "", errors.New("address is required for srt-caller")
		}
	case "srt-listener":
		// No address needed.
	default:
		return "", fmt.Errorf("unsupported destination type: %s", config.Type)
	}
	if config.Port <= 0 {
		return "", errors.New("port is required")
	}

	id := generateDestinationID()
	dest := &Destination{
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
func (m *Manager) RemoveDestination(id string) error {
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}

	// If active, stop it. Must hold dest.mu to read dest.active consistently
	// with StartDestination/StopDestination which modify it under dest.mu.
	var adapterToClose Adapter
	var stale []*AsyncAdapter
	dest.mu.Lock()
	if dest.active {
		adapterToClose = dest.adapter
		dest.adapter = nil
		dest.async = nil
		dest.active = false
	}
	dest.mu.Unlock()

	// Remove from map before rebuild so rebuildAdaptersLocked won't
	// iterate over the stale destination entry.
	delete(m.destinations, id)

	if adapterToClose != nil {
		stale = m.rebuildAdaptersLocked()
		m.stopMuxerIfNoAdaptersLocked()
	}
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

	if fn != nil {
		fn()
	}

	m.log.Info("destination removed", "id", id)
	return nil
}

// StartDestination starts the adapter for the given destination ID.
// Returns ErrDestinationNotFound if the ID doesn't exist, or
// ErrDestinationActive if already running.
func (m *Manager) StartDestination(id string) error {
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

	// Derive SRT bandwidth: per-destination MaxBW overrides CBR muxrate.
	srtInputBW := int64(0)
	srtOverheadBW := 0
	if dest.config.MaxBW > 0 {
		srtInputBW = dest.config.MaxBW / 8 // bps → bytes/sec
		srtOverheadBW = 5
	} else if m.cbrMuxrate > 0 {
		srtInputBW = m.cbrMuxrate / 8
		srtOverheadBW = 5
	}

	// Create adapter based on config type.
	var adapter Adapter
	switch dest.config.Type {
	case "srt-caller":
		callerCfg := SRTCallerConfig{
			Address:    dest.config.Address,
			Port:       dest.config.Port,
			Latency:    dest.config.Latency,
			StreamID:   dest.config.StreamID,
			InputBW:    srtInputBW,
			OverheadBW: srtOverheadBW,
		}
		caller := NewSRTCaller(callerCfg)
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
			Port:       dest.config.Port,
			Latency:    dest.config.Latency,
			MaxConns:   maxConns,
			InputBW:    srtInputBW,
			OverheadBW: srtOverheadBW,
		})
		if m.srtAcceptFn != nil {
			lCfg := listener.config
			listener.acceptFn = func(ctx context.Context, _ SRTListenerConfig) error {
				return m.srtAcceptFn(ctx, lCfg, listener)
			}
		}
		adapter = listener
	}

	if err := adapter.Start(m.ctx); err != nil {
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
	muxStarted := m.ensureMuxerLocked()
	fn := m.onState
	muxFn := m.onMuxStart
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Notify outside lock (lock discipline).
	if muxStarted && muxFn != nil {
		muxFn()
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
func (m *Manager) StopDestination(id string) error {
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
func (m *Manager) ListDestinations() []DestinationStatus {
	m.mu.Lock()
	dests := make([]*Destination, 0, len(m.destinations))
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
func (m *Manager) GetDestination(id string) (DestinationStatus, error) {
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
func (m *Manager) RecordingStatus() RecordingStatus {
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
func (m *Manager) SRTOutputStatus() SRTStatus {
	m.mu.Lock()
	adapter := m.srtOutput
	var wrapper *AsyncAdapter
	if adapter != nil && m.asyncWrappers != nil {
		wrapper = m.asyncWrappers[adapter.ID()]
	}
	m.mu.Unlock()

	if adapter == nil {
		return SRTStatus{Active: false}
	}

	var status SRTStatus
	switch a := adapter.(type) {
	case *SRTCaller:
		status = a.SRTStatusSnapshot()
	case *SRTListener:
		status = a.SRTStatusSnapshot()
	default:
		return SRTStatus{Active: false}
	}
	if wrapper != nil {
		status.DroppedPackets = wrapper.Dropped()
	}
	return status
}

// Close stops all outputs, the muxer, and the viewer. Safe to call
// multiple times.
func (m *Manager) Close() error {
	m.cancel() // signal all adapters started with m.ctx

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
	empty := make([]Adapter, 0)
	m.adapters.Store(&empty)
	m.directAdapters.Store(&empty)

	// Snapshot active destinations and clear them.
	var destAdapters []Adapter
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
	m.destinations = make(map[string]*Destination)

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
//
// When CBR is enabled (cbrMuxrate > 0), adapters are split into two paths:
//   - Direct adapters (recorder): wrapped in AsyncAdapter, receive raw TS
//   - Paced adapters (SRT): NOT wrapped, receive null-padded CBR TS via pacer
//
// When CBR is disabled, all adapters take the direct path (unchanged behavior).
func (m *Manager) rebuildAdaptersLocked() []*AsyncAdapter {
	// Collect the current set of raw adapters.
	raw := make(map[string]Adapter)
	srtIDs := make(map[string]bool) // tracks SRT adapters (paced when CBR)

	if m.recorder != nil {
		raw[m.recorder.ID()] = m.recorder
	}
	if m.srtOutput != nil {
		raw[m.srtOutput.ID()] = m.srtOutput
		srtIDs[m.srtOutput.ID()] = true
	}
	// Include active destination adapters.
	for id, dest := range m.destinations {
		dest.mu.Lock()
		if dest.active && dest.adapter != nil {
			adapter := dest.adapter
			// Wrap with SCTE-35 filter when destination has SCTE-35 disabled.
			if !dest.config.SCTE35Enabled && m.scte35PID != 0 {
				adapter = newSCTE35Filter(adapter, m.scte35PID)
			}
			// Use destination ID as key to avoid collisions with legacy adapter IDs.
			key := "dest:" + id
			raw[key] = adapter
			srtIDs[key] = true
		}
		dest.mu.Unlock()
	}

	// Split into direct (non-SRT) and paced (SRT) when CBR is enabled.
	cbrEnabled := m.cbrMuxrate > 0
	directRaw := make(map[string]Adapter)
	var pacedAdapters []Adapter

	for id, a := range raw {
		if cbrEnabled && srtIDs[id] {
			pacedAdapters = append(pacedAdapters, a)
		} else {
			directRaw[id] = a
		}
	}

	if m.asyncWrappers == nil {
		m.asyncWrappers = make(map[string]*AsyncAdapter)
	}

	// Collect stale wrappers for adapters no longer in the direct set.
	// Do NOT stop them here — caller stops outside the lock.
	var stale []*AsyncAdapter
	for id, wrapper := range m.asyncWrappers {
		if _, ok := directRaw[id]; !ok {
			stale = append(stale, wrapper)
			delete(m.asyncWrappers, id)
		}
	}

	// Create/reuse async wrappers for direct adapters.
	var directAdapters []Adapter
	for id, adapter := range directRaw {
		wrapper, exists := m.asyncWrappers[id]
		if !exists {
			wrapper = NewAsyncAdapter(adapter, asyncAdapterBufSize)
			// Start the drain goroutine. We pass a background context
			// since the inner adapter's Start() was already called.
			wrapper.startDrain()
			m.asyncWrappers[id] = wrapper
		}
		directAdapters = append(directAdapters, wrapper)
	}

	m.directAdapters.Store(&directAdapters)

	// Update pacer's adapter list (paced adapters bypass AsyncAdapter).
	if p := m.cbrPacer.Load(); p != nil {
		p.SetAdapters(&pacedAdapters)
	}

	// Combined list for HasActiveOutputs / stopMuxerIfNoAdaptersLocked.
	allAdapters := make([]Adapter, 0, len(directAdapters)+len(pacedAdapters))
	allAdapters = append(allAdapters, directAdapters...)
	allAdapters = append(allAdapters, pacedAdapters...)
	m.adapters.Store(&allAdapters)

	return stale
}

// ensureMuxerLocked creates the muxer, viewer, and registers the viewer
// on the relay if they don't already exist. Must be called with m.mu held.
// Returns true if a new muxer was created (caller should fire onMuxStart
// outside the lock).
func (m *Manager) ensureMuxerLocked() bool {
	if m.viewer != nil {
		// Already running.
		return false
	}
	if m.stopping {
		// A stop is in progress (lock temporarily released during viewer drain).
		// Don't create a new muxer — the stop will re-acquire the lock shortly.
		return false
	}

	muxer := NewTSMuxer()
	if m.scte35PID != 0 {
		muxer.SetSCTE35PID(m.scte35PID)
	}
	muxer.SetOutput(func(tsData []byte) {
		// Path 1: Direct adapters (recorder, non-SRT) get raw TS.
		directAdapters := m.directAdapters.Load()
		if directAdapters != nil {
			for _, a := range *directAdapters {
				if _, err := a.Write(tsData); err != nil {
					m.log.Error("adapter write error", "adapter", a.ID(), "err", err)
				}
			}
		}
		// Path 2: SRT adapters get null-padded CBR TS via pacer.
		if p := m.cbrPacer.Load(); p != nil {
			p.Enqueue(tsData)
		}
	})

	// Create CBR pacer if muxrate is configured.
	if m.cbrMuxrate > 0 {
		pacer := NewCBRPacer(m.cbrMuxrate, 0)
		if m.promMetrics != nil {
			pacer.SetMetrics(m.promMetrics)
		}
		m.cbrPacer.Store(pacer)
		// Re-rebuild to populate pacer adapter list (pacer now exists).
		_ = m.rebuildAdaptersLocked()
		pacer.Start()
	}

	var onVideo func(*media.VideoFrame)
	if m.confidence != nil {
		onVideo = m.confidence.IngestVideo
	}
	viewer := NewViewer(muxer, onVideo)
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
	return true
}

// stopMuxerIfNoAdaptersLocked tears down the muxer and viewer if no
// adapters remain. Must be called with m.mu held.
func (m *Manager) stopMuxerIfNoAdaptersLocked() {
	if len(*m.adapters.Load()) > 0 || m.viewer == nil {
		return
	}
	m.stopMuxerLocked()
}

// stopMuxerLocked tears down the muxer and viewer unconditionally.
// Must be called with m.mu held. Temporarily releases the lock while
// waiting for the viewer goroutine to exit.
func (m *Manager) stopMuxerLocked() {
	if m.viewer == nil {
		return
	}

	viewer := m.viewer
	muxer := m.muxer
	pacer := m.cbrPacer.Load()
	m.viewer = nil
	m.muxer = nil
	m.cbrPacer.Store(nil)

	// Guard against concurrent ensureMuxerLocked() calls while the lock is
	// released below. Without this, a concurrent start could create a new
	// muxer/viewer and increment viewerWg, causing the Wait() below to
	// block on the *new* viewer (which won't stop until the next shutdown).
	m.stopping = true

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

	// Stop pacer after muxer (drains residual data).
	if pacer != nil {
		pacer.Stop()
	}

	// Re-acquire the lock (callers expect it held on return).
	m.mu.Lock()
	m.stopping = false

	// If adapters were added while we were draining (ensureMuxerLocked
	// would have been a no-op due to the stopping flag), start a new
	// muxer now so they can receive data.
	if len(*m.adapters.Load()) > 0 && m.ensureMuxerLocked() {
		// Fire the muxer-start callback outside the lock. We capture the
		// callback here; the caller of stopMuxerLocked will handle its own
		// mux start notification separately if needed.
		muxFn := m.onMuxStart
		if muxFn != nil {
			m.mu.Unlock()
			muxFn()
			m.mu.Lock()
		}
	}

	m.log.Info("output pipeline stopped")
}

// Status returns a summary of all active outputs. This is a convenience
// method for diagnostics.
func (m *Manager) Status() ManagerStatus {
	return ManagerStatus{
		Recording: m.RecordingStatus(),
		SRT:       m.SRTOutputStatus(),
	}
}

// ManagerStatus is the combined status of all outputs.
type ManagerStatus struct {
	Recording RecordingStatus `json:"recording"`
	SRT       SRTStatus       `json:"srt"`
}

// DebugSnapshot implements debug.SnapshotProvider.
func (m *Manager) DebugSnapshot() map[string]any {
	m.mu.Lock()
	var viewerSnap map[string]any
	if m.viewer != nil {
		viewerSnap = m.viewer.DebugSnapshot()
	}
	var muxerPTS int64
	var hasMuxer bool
	if m.muxer != nil {
		muxerPTS = m.muxer.CurrentPTS()
		hasMuxer = true
	}
	m.mu.Unlock()

	snap := map[string]any{
		"viewer":    viewerSnap,
		"recording": m.RecordingStatus(),
		"srt":       m.SRTOutputStatus(),
	}

	if hasMuxer {
		snap["muxer_pts"] = muxerPTS
	}

	// CBRStatus() acquires m.mu internally, so call it after releasing
	// the lock above to avoid deadlock.
	if cbr := m.CBRStatus(); cbr != nil {
		snap["cbr_pacer"] = cbr
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
func (m *Manager) SetConfidenceMonitor(cm *ConfidenceMonitor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confidence = cm
}

// SetSCTE35Injector attaches a SCTE-35 injector to the output manager.
// The injector provides synthetic break state for late-joining viewers.
// pid is the MPEG-TS PID for SCTE-35 data (e.g., 0x102).
func (m *Manager) SetSCTE35Injector(inj scte35InjectorInterface, pid uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scte35Injector = inj
	m.scte35PID = pid
}

// InjectSCTE35 writes a SCTE-35 section to the output MPEG-TS stream.
// If no muxer is active (no outputs running), the call is a no-op.
func (m *Manager) InjectSCTE35(data []byte) error {
	m.mu.Lock()
	muxer := m.muxer
	m.mu.Unlock()
	if muxer == nil {
		return nil
	}
	return muxer.WriteSCTE35(data)
}

// ConfidenceThumbnail returns the latest JPEG thumbnail from the confidence
// monitor, or nil if unavailable.
func (m *Manager) ConfidenceThumbnail() []byte {
	m.mu.Lock()
	cm := m.confidence
	m.mu.Unlock()
	if cm == nil {
		return nil
	}
	return cm.LatestThumbnail()
}

// CBRMuxrate returns the configured CBR muxrate in bps, or 0 if disabled.
func (m *Manager) CBRMuxrate() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cbrMuxrate
}

// CBRStatus returns the current CBR pacer status. Returns nil if CBR is disabled.
func (m *Manager) CBRStatus() *CBRPacerStatus {
	m.mu.Lock()
	muxrate := m.cbrMuxrate
	m.mu.Unlock()

	if muxrate == 0 {
		return nil
	}

	status := &CBRPacerStatus{
		Enabled:    true,
		MuxrateBps: muxrate,
	}

	if p := m.cbrPacer.Load(); p != nil {
		status.NullPacketsTotal = p.NullPacketsTotal()
		status.RealBytesTotal = p.RealBytesTotal()
		status.PadBytesTotal = p.PadBytesTotal()
		status.BurstTicksTotal = p.BurstTicks()
	}

	return status
}

// CBRPacerStatus holds the current CBR pacer state for API/state broadcast.
type CBRPacerStatus struct {
	Enabled          bool  `json:"enabled"`
	MuxrateBps       int64 `json:"muxrateBps"`
	NullPacketsTotal int64 `json:"nullPacketsTotal"`
	RealBytesTotal   int64 `json:"realBytesTotal"`
	PadBytesTotal    int64 `json:"padBytesTotal"`
	BurstTicksTotal  int64 `json:"burstTicksTotal"`
}

// HasActiveOutputs returns true if at least one output is active.
func (m *Manager) HasActiveOutputs() bool {
	return len(*m.adapters.Load()) > 0
}

// StartedAt returns when recording started (zero value if not recording).
func (m *Manager) StartedAt() time.Time {
	m.mu.Lock()
	rec := m.recorder
	m.mu.Unlock()

	if rec == nil {
		return time.Time{}
	}
	status := rec.Status()
	return status.StartedAt
}
