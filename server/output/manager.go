package output

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/zsiec/prism/distribution"
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
	relay *distribution.Relay

	mu       sync.Mutex
	viewer   *OutputViewer
	muxer    *TSMuxer
	viewerWg sync.WaitGroup // tracks the viewer Run goroutine

	recorder  *FileRecorder
	srtOutput OutputAdapter // SRTCaller or SRTListener
	adapters  []OutputAdapter

	// asyncWrappers tracks AsyncAdapter wrappers by inner adapter ID,
	// so they can be stopped when adapters are removed.
	asyncWrappers map[string]*AsyncAdapter

	onState func() // triggers ControlRoomState broadcast
	closed  bool

	// SRT wiring functions injected from main.go.
	srtConnectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)
	srtAcceptFn  func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error
}

// NewOutputManager creates an OutputManager bound to the given program relay.
func NewOutputManager(relay *distribution.Relay) *OutputManager {
	return &OutputManager{relay: relay}
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
	if err := rec.Start(nil); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start recorder: %w", err)
	}

	m.recorder = rec
	m.rebuildAdaptersLocked()
	m.ensureMuxerLocked()
	fn := m.onState
	m.mu.Unlock()

	// Notify outside lock (lock discipline).
	if fn != nil {
		fn()
	}

	slog.Info("recording started", "dir", config.Dir, "file", rec.Filename())
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
	m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Close the recorder outside the lock.
	if err := rec.Close(); err != nil {
		slog.Error("error closing recorder", "err", err)
	}

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	slog.Info("recording stopped")
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
				slog.Warn("SRT ring buffer overflowed during reconnect, data was lost",
					"address", config.Address, "port", config.Port)
			}
			m.mu.Lock()
			fn := m.onState
			m.mu.Unlock()
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
	m.rebuildAdaptersLocked()
	m.ensureMuxerLocked()
	fn := m.onState
	m.mu.Unlock()

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	slog.Info("SRT output started", "mode", config.Mode, "port", config.Port)
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
	m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Close the adapter outside the lock.
	if err := adapter.Close(); err != nil {
		slog.Error("error closing SRT output", "err", err)
	}

	// Notify outside lock.
	if fn != nil {
		fn()
	}

	slog.Info("SRT output stopped")
	return nil
}

// RecordingStatus returns the current recording status for inclusion in
// ControlRoomState. Safe to call at any time.
func (m *OutputManager) RecordingStatus() RecordingStatus {
	m.mu.Lock()
	rec := m.recorder
	m.mu.Unlock()

	if rec == nil {
		return RecordingStatus{Active: false}
	}
	return rec.RecordingStatusSnapshot()
}

// SRTOutputStatus returns the current SRT output status for inclusion in
// ControlRoomState. Safe to call at any time.
func (m *OutputManager) SRTOutputStatus() SRTOutputStatus {
	m.mu.Lock()
	adapter := m.srtOutput
	m.mu.Unlock()

	if adapter == nil {
		return SRTOutputStatus{Active: false}
	}

	switch a := adapter.(type) {
	case *SRTCaller:
		return a.SRTStatusSnapshot()
	case *SRTListener:
		return a.SRTStatusSnapshot()
	default:
		return SRTOutputStatus{Active: false}
	}
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

	// Snapshot and clear async wrappers so we can stop them outside the lock.
	wrappers := m.asyncWrappers
	m.asyncWrappers = nil

	m.stopMuxerLocked()
	m.mu.Unlock()

	// Stop async wrappers first so no more writes reach inner adapters.
	for _, w := range wrappers {
		w.Stop()
	}

	// Close adapters outside the lock.
	if rec != nil {
		if err := rec.Close(); err != nil {
			slog.Error("error closing recorder on shutdown", "err", err)
		}
	}
	if srt != nil {
		if err := srt.Close(); err != nil {
			slog.Error("error closing SRT output on shutdown", "err", err)
		}
	}

	return nil
}

// rebuildAdaptersLocked rebuilds the adapter slice from the active outputs.
// Each adapter is wrapped in an AsyncAdapter for non-blocking writes.
// Wrappers for removed adapters are stopped; new wrappers are started.
// Must be called with m.mu held.
func (m *OutputManager) rebuildAdaptersLocked() {
	// Collect the current set of raw adapters.
	raw := make(map[string]OutputAdapter)
	if m.recorder != nil {
		raw[m.recorder.ID()] = m.recorder
	}
	if m.srtOutput != nil {
		raw[m.srtOutput.ID()] = m.srtOutput
	}

	if m.asyncWrappers == nil {
		m.asyncWrappers = make(map[string]*AsyncAdapter)
	}

	// Stop wrappers for adapters that are no longer present.
	for id, wrapper := range m.asyncWrappers {
		if _, ok := raw[id]; !ok {
			wrapper.Stop()
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
				slog.Error("adapter write error", "adapter", a.ID(), "err", err)
			}
		}
	})

	viewer := NewOutputViewer(muxer)
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

	slog.Info("output pipeline started")
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
		slog.Error("error closing muxer", "err", err)
	}

	// Re-acquire the lock (callers expect it held on return).
	m.mu.Lock()

	slog.Info("output pipeline stopped")
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

	return map[string]any{
		"viewer":    viewerSnap,
		"recording": m.RecordingStatus(),
		"srt":       m.SRTOutputStatus(),
	}
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
