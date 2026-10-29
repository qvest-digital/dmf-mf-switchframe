package output

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/caption"
	"github.com/zsiec/switchframe/server/internal/video"
	"github.com/zsiec/switchframe/server/metrics"
	"github.com/zsiec/switchframe/server/srt"
)

// asyncAdapterBufSize is the default buffer size for async adapter wrappers.
// Each slot holds one muxed TS packet batch. At ~188*7 bytes per batch and
// 30fps video, 256 slots provides ~8 seconds of buffering.
const asyncAdapterBufSize = 256

// scte35InjectorInterface provides synthetic break state for late-joining viewers.
type scte35InjectorInterface interface {
	SyntheticBreakState() []byte
}

// Manager orchestrates the TSMuxer and output adapters for SRT/recording.
// Frames arrive via DirectWriteVideo/DirectWriteAudio callbacks from the
// switcher and mixer, bypassing the program relay entirely.
// It creates the muxer when the first output is enabled and tears it down
// when the last output is disabled, ensuring zero CPU when inactive.
//
// Lock ordering: Manager.mu must be acquired before Destination.mu.
// Never acquire Manager.mu while holding a destination lock.
type Manager struct {
	log *slog.Logger

	mu    sync.Mutex
	muxer *TSMuxer

	// Direct muxer reference for zero-latency writes from switcher/mixer.
	// Bypasses the relay → viewer → channel path entirely.
	directMuxer atomic.Pointer[TSMuxer]

	recorder *FileRecorder
	// Stopped recorders, newest last: the newest says how the last
	// recording ended, the others stay while a file of theirs is still
	// being copied into the recording archive.
	stoppedRecorders []*FileRecorder
	srtOutput        Adapter // SRTCaller or SRTListener (legacy single output)
	destinations     map[string]*Destination
	adapters         atomic.Pointer[[]Adapter]
	frameAdapters    atomic.Pointer[[]FrameAdapter] // RTMP direct (raw frames, bypass TSMuxer)

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
	stopping   bool               // true while stopMuxerLocked is in progress (guards ensureMuxerLocked)
	ctx        context.Context    // cancelled on Close()
	cancel     context.CancelFunc // cancels ctx

	// Prometheus metrics (optional, set via SetMetrics)
	promMetrics *metrics.Metrics

	// SRT wiring functions injected from main.go.
	srtConnectFn func(ctx context.Context, config SRTCallerConfig) (srtConn, error)
	srtAcceptFn  func(ctx context.Context, config SRTListenerConfig, listener *SRTListener) error

	// Confidence monitor for program output thumbnails (atomic for lock-free
	// read from DirectWriteVideo hot path).
	confidence atomic.Pointer[ConfidenceMonitor]

	// SCTE-35 injector provides synthetic break state for late-joining viewers.
	scte35Injector scte35InjectorInterface

	// scte35PID is the configured SCTE-35 MPEG-TS PID. 0 = disabled.
	scte35PID uint16

	// Lip-sync for SRT/recording output: reads ring buffer depth from mixer.
	// lipSyncSource is boxed in atomic.Pointer so SetLipSyncSource can swap
	// the callback without racing the DirectWriteVideo hot path that invokes
	// it. smoothedLipSync and lipSyncLatched are atomic for the same reason.
	lipSyncSource   atomic.Pointer[lipSyncProvider]
	smoothedLipSync atomic.Int64
	lipSyncLatched  atomic.Bool

	// Video resolution for SDT service_type in TS output.
	videoWidth  int
	videoHeight int

	// PTS delta diagnostic: detect jitter at DirectWriteVideo stage.
	// Atomic so the hot-path read/write doesn't race if DirectWriteVideo
	// is ever driven from more than one goroutine (single-writer in
	// production; the race-stress test intentionally exercises multi-writer).
	lastDirectVideoPTS atomic.Int64

	// SRT stats manager for tracking per-output SRT connection metrics.
	// When set, ConnStats entries are created/removed with SRT adapter lifecycle.
	srtStatsManager *srt.StatsManager

	// engineLabel identifies which engine this manager belongs to ("a", "b", or "").
	// When set, newly added destinations default their Engine field to this label.
	engineLabel string

	// recordingArchiveDir is where a recording started without an
	// ArchiveDir of its own archives, before the engine label is added.
	recordingArchiveDir string

	captionMgr *caption.Manager
}

// NewManager creates an output Manager. Frames are delivered via
// DirectWriteVideo/DirectWriteAudio callbacks from the switcher/mixer,
// bypassing the relay entirely for zero-latency output.
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		log:          slog.With("component", "output"),
		destinations: make(map[string]*Destination),
		ctx:          ctx,
		cancel:       cancel,
	}
	empty := make([]Adapter, 0)
	m.adapters.Store(&empty)
	m.directAdapters.Store(&empty)
	emptyFrameAdapters := make([]FrameAdapter, 0)
	m.frameAdapters.Store(&emptyFrameAdapters)
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

// SetSRTStatsManager attaches an SRT stats manager so the output manager
// can create/remove ConnStats entries when SRT output adapters start and stop.
// Stats entries are keyed as "output:<adapter-id>" (e.g., "output:srt-caller")
// with DirectionSend so they appear alongside ingest stats in the debug snapshot.
func (m *Manager) SetSRTStatsManager(sm *srt.StatsManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.srtStatsManager = sm
}

// SetMetrics attaches Prometheus metrics to the output manager.
func (m *Manager) SetMetrics(pm *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promMetrics = pm
}

// SetResolution sets the video resolution for SDT service_type metadata.
func (m *Manager) SetResolution(width, height int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoWidth = width
	m.videoHeight = height
}

// lipSyncProvider boxes a lip-sync offset callback so it can live in
// atomic.Pointer. The func type can't be stored directly; wrapping in a
// struct gives atomic.Pointer a concrete type to work with.
type lipSyncProvider struct {
	fn func() int64
}

// SetLipSyncSource registers a function that returns the total lip-sync
// offset in 90kHz ticks for SRT/recording output. Applied to video PTS
// in the muxer to delay video relative to audio.
func (m *Manager) SetLipSyncSource(fn func() int64) {
	if fn == nil {
		m.lipSyncSource.Store(nil)
		return
	}
	m.lipSyncSource.Store(&lipSyncProvider{fn: fn})
}

// LipSyncOffset returns the current smoothed lip-sync offset.
func (m *Manager) LipSyncOffset() int64 {
	return m.smoothedLipSync.Load()
}

// LatchLipSync permanently sets the lip-sync offset to a fixed value
// and disables further updates from the lip-sync source function.
// Used in ref clock mode to freeze the offset after initial measurement.
func (m *Manager) LatchLipSync(offset int64) {
	// Write the offset before setting latched=true so any reader that
	// observes latched=true also observes the committed offset. Go's
	// atomic operations are sequentially consistent, so this order is
	// also visible to all observers.
	m.smoothedLipSync.Store(offset)
	m.lipSyncLatched.Store(true)
	if muxer := m.directMuxer.Load(); muxer != nil {
		muxer.SetLipSyncOffset90k(offset)
	}
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
// (i.e., when the first output is enabled). Used to request an IDR keyframe
// from the encoder so the muxer can initialize immediately instead of
// waiting for the next GOP boundary.
func (m *Manager) OnMuxerStart(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMuxStart = fn
}

// SetEngineLabel sets the engine identifier for this manager ("a", "b", or "").
// Newly added destinations will default their Engine field to this label.
func (m *Manager) SetEngineLabel(label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engineLabel = label
}

// SetRecordingArchiveDir sets where a recording started without an
// ArchiveDir of its own archives its finished files; "" archives nothing.
// With an engine label the files go to a subdirectory named after it, so
// two engines sharing one archive never write the same file: both name
// their recordings after the same start second.
func (m *Manager) SetRecordingArchiveDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordingArchiveDir = dir
}

// RecordingArchiveDir returns the directory a recording started without an
// ArchiveDir of its own archives to, "" for none.
func (m *Manager) RecordingArchiveDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordingArchiveDirLocked()
}

func (m *Manager) recordingArchiveDirLocked() string {
	if m.recordingArchiveDir == "" {
		return ""
	}
	return filepath.Join(m.recordingArchiveDir, m.engineLabel)
}

// EngineLabel returns the manager's configured engine label.
func (m *Manager) EngineLabel() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engineLabel
}

// SetCaptionManager attaches a caption manager for per-frame SEI injection in
// the underlying TSMuxer. Applies immediately when a muxer is already running
// and propagates to muxers created later.
func (m *Manager) SetCaptionManager(cm *caption.Manager) {
	m.mu.Lock()
	m.captionMgr = cm
	mux := m.directMuxer.Load()
	m.mu.Unlock()
	if mux != nil {
		mux.SetCaptionManager(cm)
	}
}

// ReassignDestinationEngine changes the engine assignment for a destination.
// Valid values are "a", "b", or "" (local engine).
func (m *Manager) ReassignDestinationEngine(id, engine string) error {
	if engine != "a" && engine != "b" && engine != "" {
		return fmt.Errorf("output: engine must be 'a', 'b', or empty")
	}
	m.mu.Lock()
	dest, ok := m.destinations[id]
	if !ok {
		m.mu.Unlock()
		return ErrDestinationNotFound
	}
	dest.mu.Lock()
	dest.config.Engine = engine
	dest.mu.Unlock()

	fn := m.onState
	m.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// StartRecording begins recording program output to a file.
// Returns an error if recording is already active.
func (m *Manager) StartRecording(config RecorderConfig) error {
	m.mu.Lock()
	if m.recorder != nil {
		m.mu.Unlock()
		return ErrRecorderActive
	}

	if config.ArchiveDir == "" {
		config.ArchiveDir = m.recordingArchiveDirLocked()
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
	m.stoppedRecorders = append(m.stoppedRecorders, rec)
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
		if cs := m.createSRTStats(caller.ID(), "", config.Mode, config.Latency); cs != nil {
			caller.SetStats(cs)
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
		if cs := m.createSRTStats(listener.ID(), "", config.Mode, config.Latency); cs != nil {
			listener.SetStats(cs)
		}
		adapter = listener
	default:
		m.mu.Unlock()
		return fmt.Errorf("unknown SRT mode: %s", config.Mode)
	}

	// Reserve the output slot, then run adapter.Start outside Manager.mu:
	// for caller mode Start performs a synchronous SRT dial that can block
	// up to the 5s ConnTimeout and must not stall the control plane.
	m.srtOutput = adapter
	m.mu.Unlock()

	if err := adapter.Start(m.ctx); err != nil {
		m.mu.Lock()
		if m.srtOutput == adapter {
			m.srtOutput = nil
			// Only remove the stats entry while this adapter still owns the
			// slot. Adapter IDs are type constants, so a concurrently
			// restarted output reuses the same stats key and Create replaces
			// the entry — an unconditional remove here would delete the live
			// adapter's stats. If a concurrent Stop already claimed the slot,
			// it removed (or a restart replaced) the entry.
			m.removeSRTStats(adapter.ID(), "")
		}
		m.mu.Unlock()
		return fmt.Errorf("start SRT output: %w", err)
	}

	m.mu.Lock()
	if m.srtOutput != adapter {
		// A concurrent StopSRTOutput/Close claimed the adapter while Start
		// was dialing. It already closed the adapter, but the dial may have
		// completed after that Close — close again so no live connection
		// survives, and do not publish.
		m.mu.Unlock()
		_ = adapter.Close()
		return ErrSRTNotActive
	}
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
	m.removeSRTStats(adapter.ID(), "")
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
		if config.Port <= 0 {
			return "", errors.New("port is required")
		}
	case "srt-listener":
		if config.Port <= 0 {
			return "", errors.New("port is required")
		}
	case "rtmp":
		if config.URL == "" {
			return "", errors.New("url is required for rtmp")
		}
		if len(config.URL) < 8 || (config.URL[:7] != "rtmp://" && config.URL[:8] != "rtmps://") {
			return "", errors.New("url must start with rtmp:// or rtmps://")
		}
	default:
		return "", fmt.Errorf("unsupported destination type: %s", config.Type)
	}

	// Validate encryption settings up front: silently dropping them later
	// would stream an "encrypted" destination in cleartext.
	if err := validateEncryptionConfig(config); err != nil {
		return "", err
	}

	// Default engine label from manager if not explicitly set.
	m.mu.Lock()
	if config.Engine == "" {
		config.Engine = m.engineLabel
	}
	m.mu.Unlock()

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
	var frameAdapterToClose FrameAdapter
	var stale []*AsyncAdapter
	dest.mu.Lock()
	if dest.active {
		adapterToClose = dest.adapter
		frameAdapterToClose = dest.frameAdapter
		dest.adapter = nil
		dest.frameAdapter = nil
		dest.async = nil
		dest.active = false
	}
	dest.mu.Unlock()

	// Remove SRT stats entry for this destination.
	if adapterToClose != nil {
		m.removeSRTStats(adapterToClose.ID(), id)
	}

	// Remove from map before rebuild so rebuildAdaptersLocked won't
	// iterate over the stale destination entry.
	delete(m.destinations, id)

	if adapterToClose != nil || frameAdapterToClose != nil {
		stale = m.rebuildAdaptersLocked()
		m.stopMuxerIfNoAdaptersLocked()
	}
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close adapters outside lock.
	if adapterToClose != nil {
		if err := adapterToClose.Close(); err != nil {
			m.log.Error("error closing destination adapter on remove", "id", id, "err", err)
		}
	}
	if frameAdapterToClose != nil {
		if err := frameAdapterToClose.Close(); err != nil {
			m.log.Error("error closing frame adapter on remove", "id", id, "err", err)
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
	if m.engineLabel != "" && dest.config.Engine != "" && dest.config.Engine != m.engineLabel {
		configured := dest.config.Engine
		dest.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("%w: destination %q is assigned to engine %q, not local engine %q",
			ErrDestinationWrongEngine, id, configured, m.engineLabel)
	}
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

	// Map encryption config to the srtgo key length. Config was validated
	// at AddDestination time; a failure here means the stored config is
	// corrupt, so refuse to start rather than stream in cleartext.
	srtKeyLen, err := srtKeyLength(dest.config.Encryption)
	if err != nil {
		dest.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("destination %s: %w", id, err)
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
			Passphrase: dest.config.Passphrase,
			KeyLength:  srtKeyLen,
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
		if cs := m.createSRTStats(caller.ID(), id, dest.config.Type, dest.config.Latency); cs != nil {
			caller.SetStats(cs)
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
			Passphrase: dest.config.Passphrase,
			KeyLength:  srtKeyLen,
			InputBW:    srtInputBW,
			OverheadBW: srtOverheadBW,
		})
		if m.srtAcceptFn != nil {
			lCfg := listener.config
			listener.acceptFn = func(ctx context.Context, _ SRTListenerConfig) error {
				return m.srtAcceptFn(ctx, lCfg, listener)
			}
		}
		// Force IDR keyframe when a new SRT client connects so it can
		// start decoding immediately without waiting for the next GOP.
		if m.onMuxStart != nil {
			listener.OnConnect(m.onMuxStart)
		}
		if cs := m.createSRTStats(listener.ID(), id, dest.config.Type, dest.config.Latency); cs != nil {
			listener.SetStats(cs)
		}
		adapter = listener

	case "rtmp":
		rtmpDirect, err := NewRTMPDirect(RTMPCallerConfig{
			URL:                dest.config.URL,
			AllowUntrustedCert: dest.config.AllowUntrustedCert,
		})
		if err != nil {
			dest.mu.Unlock()
			m.mu.Unlock()
			return fmt.Errorf("create RTMP adapter: %w", err)
		}
		rtmpDirect.onReconnect = func() {
			m.mu.Lock()
			fn := m.onState
			m.mu.Unlock()
			if fn != nil {
				fn()
			}
		}
		if err := rtmpDirect.Start(m.ctx); err != nil {
			dest.mu.Unlock()
			m.mu.Unlock()
			return fmt.Errorf("start RTMP: %w", err)
		}

		now := time.Now()
		dest.frameAdapter = rtmpDirect
		dest.active = true
		dest.startedAt = &now
		dest.mu.Unlock()

		_ = m.rebuildAdaptersLocked()
		fn := m.onState
		m.mu.Unlock()

		if fn != nil {
			fn()
		}
		m.log.Info("destination started", "id", id, "type", dest.config.Type)
		return nil
	}

	// Reserve the destination before dialing: mark it active and publish
	// the adapter reference so concurrent Start/Stop/Remove operations see
	// a consistent state, then run adapter.Start outside BOTH locks. For
	// srt-caller adapters Start performs a synchronous SRT dial that can
	// block up to the 5s ConnTimeout; holding Manager.mu across the dial
	// would stall the entire control plane for the duration.
	now := time.Now()
	dest.adapter = adapter
	dest.active = true
	dest.startedAt = &now
	dest.mu.Unlock()
	m.mu.Unlock()

	if err := adapter.Start(m.ctx); err != nil {
		// Roll back the reservation (unless a concurrent Stop/Remove
		// already claimed it).
		m.mu.Lock()
		dest.mu.Lock()
		if dest.adapter == adapter {
			dest.adapter = nil
			dest.active = false
			dest.startedAt = nil
			// Only remove the stats entry while this adapter still owns the
			// slot. Adapter IDs are type constants, so a concurrently
			// restarted destination reuses the same stats key and Create
			// replaces the entry — an unconditional remove here would delete
			// the live adapter's stats. If a concurrent Stop/Remove already
			// claimed the slot, it removed (or a restart replaced) the entry.
			m.removeSRTStats(adapter.ID(), id)
		}
		dest.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("start destination %s: %w", id, err)
	}

	m.mu.Lock()
	dest.mu.Lock()
	current := dest.active && dest.adapter == adapter
	dest.mu.Unlock()
	if !current {
		// A concurrent StopDestination/RemoveDestination/Close claimed the
		// adapter while Start was dialing. It already closed the adapter,
		// but the dial may have completed after that Close — close again
		// so no live connection survives, and do not publish.
		m.mu.Unlock()
		_ = adapter.Close()
		return ErrDestinationStopped
	}

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
		// Distinguish "genuinely stopped" from "stopped locally because this
		// destination runs on a different engine" — the latter is a routing
		// error that a plain ErrDestinationStopped would hide. An active
		// mismatched destination (legacy state) is still stoppable here as
		// a recovery path.
		if m.engineLabel != "" && dest.config.Engine != "" && dest.config.Engine != m.engineLabel {
			configured := dest.config.Engine
			dest.mu.Unlock()
			m.mu.Unlock()
			return fmt.Errorf("%w: destination %q is assigned to engine %q, not local engine %q",
				ErrDestinationWrongEngine, id, configured, m.engineLabel)
		}
		dest.mu.Unlock()
		m.mu.Unlock()
		return ErrDestinationStopped
	}

	adapter := dest.adapter
	frameAdapter := dest.frameAdapter
	dest.adapter = nil
	dest.frameAdapter = nil
	dest.async = nil
	dest.active = false
	dest.mu.Unlock()

	// Remove SRT stats entry for this destination.
	if adapter != nil {
		m.removeSRTStats(adapter.ID(), id)
	}

	stale := m.rebuildAdaptersLocked()
	m.stopMuxerIfNoAdaptersLocked()
	fn := m.onState
	m.mu.Unlock()

	// Stop stale wrappers outside the lock.
	for _, w := range stale {
		w.Stop()
	}

	// Close adapters outside the lock.
	if adapter != nil {
		if err := adapter.Close(); err != nil {
			m.log.Error("error closing destination adapter on stop", "id", id, "err", err)
		}
	}
	if frameAdapter != nil {
		if err := frameAdapter.Close(); err != nil {
			m.log.Error("error closing frame adapter on stop", "id", id, "err", err)
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
		// A recording that recorded nothing says so until the next one
		// starts; one that did is just inactive.
		if last := m.lastStoppedRecorder(); last != nil {
			if final := last.FinalStatus(); final.Error != "" {
				return final
			}
		}
		return RecordingStatus{Active: false}
	}
	status := rec.RecordingStatusSnapshot()
	if wrapper != nil {
		status.DroppedPackets = wrapper.Dropped()
	}
	return status
}

func (m *Manager) lastStoppedRecorder() *FileRecorder {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.stoppedRecorders); n > 0 {
		return m.stoppedRecorders[n-1]
	}
	return nil
}

// FinalizingRecordings returns the basenames of finished recording files
// still being copied into the recording archive: listed, they are neither
// the running recording nor ready to download from where they will end up.
func (m *Manager) FinalizingRecordings() []string {
	m.mu.Lock()
	recs := append([]*FileRecorder(nil), m.stoppedRecorders...)
	if m.recorder != nil {
		recs = append(recs, m.recorder)
	}
	m.mu.Unlock()

	var names []string
	busy := map[*FileRecorder]bool{}
	for _, r := range recs {
		a := r.Archiving()
		busy[r] = len(a) > 0
		names = append(names, a...)
	}

	// Forget stopped recorders with nothing left to copy, except the
	// newest, which RecordingStatus reads.
	m.mu.Lock()
	kept := m.stoppedRecorders[:0]
	for i, r := range m.stoppedRecorders {
		if busy[r] || i == len(m.stoppedRecorders)-1 {
			kept = append(kept, r)
		}
	}
	m.stoppedRecorders = kept
	m.mu.Unlock()
	return names
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

// Close stops all outputs and the muxer. Safe to call multiple times.
func (m *Manager) Close() error {
	m.cancel() // signal all adapters started with m.ctx

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true

	rec := m.recorder
	srtAdapter := m.srtOutput
	m.recorder = nil
	m.srtOutput = nil
	empty := make([]Adapter, 0)
	m.adapters.Store(&empty)
	m.directAdapters.Store(&empty)
	emptyFA := make([]FrameAdapter, 0)
	m.frameAdapters.Store(&emptyFA)

	// Remove stats for legacy SRT output.
	if srtAdapter != nil {
		m.removeSRTStats(srtAdapter.ID(), "")
	}

	// Snapshot active destinations and clear them.
	var destAdapters []Adapter
	var destFrameAdapters []FrameAdapter
	for id, dest := range m.destinations {
		dest.mu.Lock()
		if dest.active {
			if dest.adapter != nil {
				m.removeSRTStats(dest.adapter.ID(), id)
				destAdapters = append(destAdapters, dest.adapter)
				dest.adapter = nil
			}
			if dest.frameAdapter != nil {
				destFrameAdapters = append(destFrameAdapters, dest.frameAdapter)
				dest.frameAdapter = nil
			}
			dest.async = nil
			dest.active = false
		}
		dest.mu.Unlock()
	}
	m.destinations = make(map[string]*Destination)

	// Snapshot and clear async wrappers so we can stop them outside the lock.
	wrappers := m.asyncWrappers
	m.asyncWrappers = nil

	cm := m.confidence.Swap(nil)
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
	if srtAdapter != nil {
		if err := srtAdapter.Close(); err != nil {
			m.log.Error("error closing SRT output on shutdown", "err", err)
		}
	}
	for _, a := range destAdapters {
		if err := a.Close(); err != nil {
			m.log.Error("error closing destination on shutdown", "err", err)
		}
	}
	for _, fa := range destFrameAdapters {
		if err := fa.Close(); err != nil {
			m.log.Error("error closing frame adapter on shutdown", "err", err)
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
			// Only SRT destinations use CBR pacing; RTMP uses TCP flow control.
			if dest.config.Type == "srt-caller" || dest.config.Type == "srt-listener" {
				srtIDs[key] = true
			}
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

	// Build frame adapter list (RTMP direct).
	var frameAdapters []FrameAdapter
	for _, dest := range m.destinations {
		dest.mu.Lock()
		if dest.active && dest.frameAdapter != nil {
			frameAdapters = append(frameAdapters, dest.frameAdapter)
		}
		dest.mu.Unlock()
	}
	m.frameAdapters.Store(&frameAdapters)

	return stale
}

// ensureMuxerLocked creates the muxer if it doesn't already exist.
// Uses direct write mode: frames arrive via DirectWriteVideo/DirectWriteAudio
// callbacks from the switcher/mixer, bypassing the relay → viewer → channel
// path entirely. This eliminates 3 goroutine hops and 3 channel buffers
// from the output path.
// Must be called with m.mu held.
// Returns true if a new muxer was created (caller should fire onMuxStart
// outside the lock).
func (m *Manager) ensureMuxerLocked() bool {
	if m.muxer != nil {
		// Already running.
		return false
	}
	if m.stopping {
		// A stop is in progress. Don't create a new muxer.
		return false
	}

	muxer := NewTSMuxer()
	if m.videoWidth > 0 && m.videoHeight > 0 {
		muxer.SetResolution(m.videoWidth, m.videoHeight)
	}
	if m.scte35PID != 0 {
		muxer.SetSCTE35PID(m.scte35PID)
	}
	if m.captionMgr != nil {
		muxer.SetCaptionManager(m.captionMgr)
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

	// Propagate latched lip-sync offset to the new muxer. LatchLipSync
	// is called at startup before the muxer exists, so the offset must
	// be applied here when the muxer is first created.
	if m.lipSyncLatched.Load() {
		if smoothed := m.smoothedLipSync.Load(); smoothed != 0 {
			muxer.SetLipSyncOffset90k(smoothed)
		}
	}

	m.muxer = muxer
	// Store in atomic pointer for lock-free access from direct write callbacks.
	m.directMuxer.Store(muxer)

	m.log.Info("output pipeline started (direct mode)")
	return true
}

// stopMuxerIfNoAdaptersLocked tears down the muxer if no
// adapters remain. Must be called with m.mu held.
func (m *Manager) stopMuxerIfNoAdaptersLocked() {
	if len(*m.adapters.Load()) > 0 || m.muxer == nil {
		return
	}
	m.stopMuxerLocked()
}

// stopMuxerLocked tears down the muxer unconditionally.
// Must be called with m.mu held.
func (m *Manager) stopMuxerLocked() {
	if m.muxer == nil {
		return
	}

	muxer := m.muxer
	pacer := m.cbrPacer.Load()
	m.muxer = nil
	// Clear atomic pointer first so no new direct writes start.
	m.directMuxer.Store(nil)
	m.cbrPacer.Store(nil)

	m.stopping = true

	// Close the muxer. Any in-progress DirectWriteVideo/DirectWriteAudio
	// calls will complete (muxer has its own mutex) before Close returns.
	if err := muxer.Close(); err != nil {
		m.log.Error("error closing muxer", "err", err)
	}

	// Stop pacer after muxer (drains residual data).
	if pacer != nil {
		pacer.Stop()
	}

	m.stopping = false

	// If adapters were added while we were stopping, start a new
	// muxer now so they can receive data.
	if len(*m.adapters.Load()) > 0 && m.ensureMuxerLocked() {
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
	var muxerPTS int64
	var lipSyncOffset int64
	var hasMuxer bool
	if m.muxer != nil {
		muxerPTS = m.muxer.CurrentPTS()
		lipSyncOffset = m.muxer.LipSyncOffset90k()
		hasMuxer = true
	}
	m.mu.Unlock()

	snap := map[string]any{
		"recording": m.RecordingStatus(),
		"srt":       m.SRTOutputStatus(),
	}

	if hasMuxer {
		snap["muxer_pts"] = muxerPTS
		snap["lip_sync_offset_90k"] = lipSyncOffset
		snap["lip_sync_offset_ms"] = float64(lipSyncOffset) / 90.0
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
// The monitor receives video frames via DirectWriteVideo. Pass nil to clear.
func (m *Manager) SetConfidenceMonitor(cm *ConfidenceMonitor) {
	if cm != nil {
		m.confidence.Store(cm)
	} else {
		m.confidence.Store(nil)
	}
}

// srtStatsKey returns the stats manager key for an output SRT adapter.
// For the legacy single output, adapterID is "srt-caller" or "srt-listener".
// For destinations, it includes the destination ID (e.g., "output:dest:abc123:srt-caller").
func srtStatsKey(adapterID, destID string) string {
	if destID != "" {
		return "output:dest:" + destID + ":" + adapterID
	}
	return "output:" + adapterID
}

// createSRTStats creates a ConnStats entry for an SRT output adapter.
// Returns the ConnStats (or nil if no stats manager is set).
// Must be called with m.mu held (reads m.srtStatsManager).
func (m *Manager) createSRTStats(adapterID, destID, mode string, latencyMs int) *srt.ConnStats {
	sm := m.srtStatsManager
	if sm == nil {
		return nil
	}
	key := srtStatsKey(adapterID, destID)
	return sm.Create(key, "send", mode+":"+adapterID, latencyMs)
}

// removeSRTStats removes a ConnStats entry for an SRT output adapter.
// Must be called with m.mu held (reads m.srtStatsManager).
func (m *Manager) removeSRTStats(adapterID, destID string) {
	sm := m.srtStatsManager
	if sm == nil {
		return
	}
	key := srtStatsKey(adapterID, destID)
	sm.Remove(key)
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
	cm := m.confidence.Load()
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

// DirectWriteVideo writes a video frame directly to the MPEG-TS muxer
// and any frame adapters (RTMP direct FLV), bypassing the relay →
// viewer → channel path. Called synchronously from the switcher's encode
// goroutine — no channels, no goroutine hops.
func (m *Manager) DirectWriteVideo(frame *media.VideoFrame) {
	// TS-based outputs (SRT, recording).
	if muxer := m.directMuxer.Load(); muxer != nil {
		if p := m.lipSyncSource.Load(); p != nil && !m.lipSyncLatched.Load() {
			raw := p.fn()
			cur := m.smoothedLipSync.Load()
			if cur == 0 {
				m.smoothedLipSync.Store(raw)
				muxer.SetLipSyncOffset90k(raw)
			} else if delta := raw - cur; delta > 450 || delta < -450 {
				// Smoothed EMA update. In production this RMW is
				// single-writer (encode goroutine); LatchLipSync sets
				// latched=true, which short-circuits this block on the
				// next frame. A plain Store is safe.
				next := (cur*15 + raw) / 16
				m.smoothedLipSync.Store(next)
				muxer.SetLipSyncOffset90k(next)
			}
		}
		// Diagnostic: verify upstream PTS deltas at DirectWriteVideo
		// stage. Uses the shared nominal-delta helper so non-NTSC
		// pipelines (24/25/30/50/60 fps) don't produce false warnings.
		if prev := m.lastDirectVideoPTS.Load(); prev != 0 {
			delta := frame.PTS - prev
			if !video.IsNominalPTSDelta(delta) {
				slog.Warn("DirectWriteVideo: non-nominal PTS delta",
					"delta", delta, "pts", frame.PTS, "prevPts", prev)
			}
		}
		m.lastDirectVideoPTS.Store(frame.PTS)
		if err := muxer.WriteVideo(frame); err != nil {
			m.log.Error("direct video write error", "err", err)
		}
	}
	// Frame adapters (RTMP direct FLV).
	if fas := m.frameAdapters.Load(); fas != nil {
		for _, fa := range *fas {
			if err := fa.WriteVideo(frame); err != nil {
				m.log.Error("frame adapter video write error", "adapter", fa.ID(), "err", err)
			}
		}
	}
	// Feed the confidence monitor (1fps JPEG thumbnail).
	if cm := m.confidence.Load(); cm != nil {
		cm.IngestVideo(frame)
	}
}

// DirectWriteAudio writes an audio frame directly to the MPEG-TS muxer
// and any frame adapters (RTMP direct FLV), bypassing the relay →
// viewer → channel path. Called synchronously from the mixer's output
// ticker goroutine.
func (m *Manager) DirectWriteAudio(frame *media.AudioFrame) {
	if muxer := m.directMuxer.Load(); muxer != nil {
		if err := muxer.WriteAudio(frame); err != nil {
			m.log.Error("direct audio write error", "err", err)
		}
	}
	if fas := m.frameAdapters.Load(); fas != nil {
		for _, fa := range *fas {
			if err := fa.WriteAudio(frame); err != nil {
				m.log.Error("frame adapter audio write error", "adapter", fa.ID(), "err", err)
			}
		}
	}
}

// HasActiveOutputs returns true if at least one output is active.
func (m *Manager) HasActiveOutputs() bool {
	if len(*m.adapters.Load()) > 0 {
		return true
	}
	if fas := m.frameAdapters.Load(); fas != nil && len(*fas) > 0 {
		return true
	}
	return false
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
