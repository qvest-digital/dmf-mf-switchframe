package playout

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Manager orchestrates multiple playout channels (up to MaxChannels).
// It owns the channel array, wires callbacks between channels and the
// app layer, and provides aggregated state for broadcast. Follows the
// clip.Manager pattern.
type Manager struct {
	mu       sync.Mutex
	channels [MaxChannels]*Channel
	config   ManagerConfig
	log      *slog.Logger

	podBoards     [MaxChannels]*PodBoard
	templateStore *PodTemplateStore

	asRunLogger *AsRunLogger

	onStateChange     func()
	onStart           func(channelID int, key string)
	onStop            func(channelID int, key string)
	rawVideoOutput    func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)
	videoOutput       func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)
	audioOutput       func(key string, data []byte, pts int64, sampleRate, channels int)
	ptsProvider       func() int64
	secondaryHandler  SecondaryEventHandler
	spliceCoordinator *SpliceCoordinator
	watchdog          *Watchdog

	// GPU-native live source aliasing (zero-copy frame routing in VRAM).
	gpuAlias func(channelKey, liveSourceKey string) bool

	// GPU source tap for live source pass-through (CPU fallback).
	gpuSourceTap func(channelKey, sourceKey string) *DecodedFrame

	// Pipeline format for all channels.
	pipelineWidth  int
	pipelineHeight int
	pipelineFPS    float64
}

// ManagerConfig configures the playout Manager.
type ManagerConfig struct {
	// AssetProvider opens asset URIs and returns streaming frame iterators.
	// If nil, channels will have no AssetProvider configured.
	AssetProvider AssetProvider
}

// NewManager creates a playout Manager and pre-creates all channels.
func NewManager(config ManagerConfig) *Manager {
	m := &Manager{
		config: config,
		log:    slog.With("component", "playout-manager"),
	}

	for i := 0; i < MaxChannels; i++ {
		m.channels[i] = NewChannel(i+1, ChannelConfig{
			AssetProvider: config.AssetProvider,
		})
		m.podBoards[i] = NewPodBoard(i + 1)
	}

	m.templateStore = NewPodTemplateStore()

	return m
}

// validateChannelID checks that channelID is in the 1..MaxChannels range.
func (m *Manager) validateChannelID(id int) error {
	if id < 1 || id > MaxChannels {
		return ErrInvalidChannel
	}
	return nil
}

// LoadPlaylist validates the channel ID and delegates to the channel.
func (m *Manager) LoadPlaylist(channelID int, pl *Playlist) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	if err := ch.LoadPlaylist(pl); err != nil {
		return err
	}

	// Load manifest-defined pod templates into the shared template store.
	if len(pl.PodTemplates) > 0 {
		m.mu.Lock()
		ts := m.templateStore
		m.mu.Unlock()
		ts.LoadFromPlaylist(pl.PodTemplates)
	}

	// Configure idle loop if the playlist specifies an idle asset.
	if pl.IdleAssetURI != "" {
		m.mu.Lock()
		provider := m.config.AssetProvider
		m.mu.Unlock()
		ch.SetIdleConfig(pl.IdleAssetURI, provider)
	}

	m.notifyStateChange()
	return nil
}

// Start wires Manager callbacks to the channel, then starts playback.
func (m *Manager) Start(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]

	// Wire callbacks from Manager to Channel before starting.
	stateChangeFn := m.onStateChange
	onStartFn := m.onStart
	onStopFn := m.onStop
	rawVideoFn := m.rawVideoOutput
	audioFn := m.audioOutput
	ptsFn := m.ptsProvider
	secondaryH := m.secondaryHandler
	sc := m.spliceCoordinator
	wd := m.watchdog
	gpuAliasFn := m.gpuAlias
	gpuTap := m.gpuSourceTap
	m.mu.Unlock()

	// Register with watchdog.
	if wd != nil {
		wd.RegisterChannel(channelID)
	}

	ch.SetOnStateChange(func(s ChannelState) {
		if stateChangeFn != nil {
			stateChangeFn()
		}
	})
	ch.SetOnStart(func(key string) {
		if onStartFn != nil {
			onStartFn(channelID, key)
		}
	})
	ch.SetOnStop(func(key string) {
		if wd != nil {
			wd.UnregisterChannel(channelID)
		}
		if onStopFn != nil {
			onStopFn(channelID, key)
		}
	})
	if rawVideoFn != nil {
		ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			rawVideoFn(ch.Key(), yuv, w, h, pts, isKeyframe)
			if wd != nil {
				wd.ReportFrame(channelID)
			}
		})
	} else if wd != nil {
		ch.SetRawVideoOutput(func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			wd.ReportFrame(channelID)
		})
	}
	if audioFn != nil {
		ch.SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
			audioFn(ch.Key(), data, pts, sampleRate, channels)
		})
	}
	if ptsFn != nil {
		ch.SetPTSProvider(ptsFn)
	}
	if secondaryH != nil {
		ch.SetSecondaryHandler(secondaryH)
	}
	if sc != nil {
		ch.SetSpliceCoordinator(sc)
	}

	// Wire GPU-native alias — zero-copy frame routing in VRAM.
	if gpuAliasFn != nil {
		channelKey := ch.Key()
		ch.SetGPUAlias(func(liveSourceKey string) bool {
			return gpuAliasFn(channelKey, liveSourceKey)
		})
	}

	// Wire GPU source tap (CPU fallback) — wrap the Manager-level tap with
	// the channel key so the channel only needs to pass the source key.
	if gpuTap != nil {
		channelKey := ch.Key()
		ch.SetGPUSourceTap(func(sourceKey string) *DecodedFrame {
			return gpuTap(channelKey, sourceKey)
		})
	}

	// Wire pod board callbacks.
	m.mu.Lock()
	pb := m.podBoards[channelID-1]
	m.mu.Unlock()

	pb.SetOnBreakStart(func() { ch.TakeBreak() })
	pb.SetOnBreakEnd(func() { ch.ReturnFromBreak() })
	pb.SetOnStateChange(func() {
		if stateChangeFn != nil {
			stateChangeFn()
		}
	})

	if err := ch.Start(); err != nil {
		return err
	}

	m.log.Info("playout channel started", "channel", channelID)
	return nil
}

// Stop stops the given playout channel.
func (m *Manager) Stop(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	ch.Stop()
	m.notifyStateChange()
	return nil
}

// Next advances the given channel to the next playlist item.
func (m *Manager) Next(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	return ch.Next()
}

// Previous moves the given channel back to the previous playlist item.
func (m *Manager) Previous(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	return ch.Previous()
}

// JumpTo jumps the given channel to the playlist item with the specified ID.
func (m *Manager) JumpTo(channelID int, itemID string) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	return ch.JumpTo(itemID)
}

// TakeManual pauses automated playlist advancement on the given channel.
func (m *Manager) TakeManual(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	ch.TakeManual()
	m.notifyStateChange()
	return nil
}

// ReturnToAuto resumes automated playlist advancement on the given channel.
func (m *Manager) ReturnToAuto(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()

	ch.ReturnToAuto()
	m.notifyStateChange()
	return nil
}

// PlaylistItems returns the playlist items for a channel, or nil if not loaded.
func (m *Manager) PlaylistItems(channelID int) ([]PlaylistItem, error) {
	if err := m.validateChannelID(channelID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	ch := m.channels[channelID-1]
	m.mu.Unlock()
	return ch.PlaylistItems(), nil
}

// ChannelStates returns the status of all playout channels.
func (m *Manager) ChannelStates() []ChannelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	states := make([]ChannelStatus, MaxChannels)
	for i := 0; i < MaxChannels; i++ {
		states[i] = m.channels[i].Status()
		pbStatus := m.podBoards[i].Status()
		states[i].Pods = &pbStatus
	}
	return states
}

// Snapshot captures the full manager state (all channels + pods) for persistence.
// The returned PersistedState is a deep copy safe for serialization.
func (m *Manager) Snapshot() PersistedState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := PersistedState{
		Version:   1,
		Timestamp: time.Now().UTC(),
		Channels:  make([]PersistedChannel, MaxChannels),
	}
	for i := 0; i < MaxChannels; i++ {
		state.Channels[i] = m.channels[i].Snapshot()
		pods := m.podBoards[i].ListPods()
		for _, p := range pods {
			state.Channels[i].Pods = append(state.Channels[i].Pods, PersistedPod{
				ID:            p.ID,
				Config:        p.Config,
				State:         p.State,
				Armed:         p.Armed,
				ElapsedMs:     p.ElapsedMs,
				FiredAt:       p.FiredAt,
				SCTE35EventID: p.SCTE35EventID,
			})
		}
	}
	return state
}

// Recover loads persisted state from disk and restores channel playlists,
// cursors, and non-terminal pods. Channels that were OnAir or Cued are
// re-cued at their saved cursor position; Start must be called separately
// to resume playback. Active pods have their ElapsedMs adjusted by the
// wall-clock delta since the snapshot was taken.
//
// Recover must be called on a fresh Manager before Start(), not on a running
// system. Calling Recover while channels are on-air leads to undefined behavior.
func (m *Manager) Recover(statePath string) error {
	state, err := loadPersistedState(statePath)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	wallDelta := time.Since(state.Timestamp)

	m.mu.Lock()
	asRunLogger := m.asRunLogger
	m.mu.Unlock()

	for _, pch := range state.Channels {
		if err := m.validateChannelID(pch.ID); err != nil {
			continue
		}

		m.mu.Lock()
		ch := m.channels[pch.ID-1]
		pb := m.podBoards[pch.ID-1]
		m.mu.Unlock()

		// Restore playlist and cursor.
		if pch.Playlist != nil {
			if err := ch.LoadPlaylist(pch.Playlist); err != nil {
				m.log.Warn("recover: LoadPlaylist failed",
					"channel", pch.ID, "error", err)
				continue
			}
			// Override cursor to the saved position.
			ch.SetCursor(pch.Cursor)
		}

		// Restore non-terminal pods.
		for _, pp := range pch.Pods {
			if PodState(pp.State).IsTerminal() {
				continue
			}

			pod := &Pod{
				ID:            pp.ID,
				Config:        pp.Config,
				State:         pp.State,
				Armed:         pp.Armed,
				ElapsedMs:     pp.ElapsedMs,
				FiredAt:       pp.FiredAt,
				SCTE35EventID: pp.SCTE35EventID,
			}

			// Adjust elapsed time for active pods based on wall-clock delta.
			if pod.State == PodBreakActive || pod.State == PodPreRoll {
				pod.ElapsedMs += wallDelta.Milliseconds()
				if pod.Config.DurationMs > 0 {
					remain := pod.Config.DurationMs - pod.ElapsedMs
					if remain < 0 {
						remain = 0
					}
					pod.RemainingMs = remain
				}
			}

			pb.restorePod(pod)
		}

		// Emit as-run error for channels that were OnAir or Cued.
		if pch.State == StateOnAir || pch.State == StateCued {
			if asRunLogger != nil {
				asRunLogger.Log(AsRunEvent{
					Timestamp: time.Now().UTC(),
					Type:      AsRunError,
					ChannelID: pch.ID,
					Message: fmt.Sprintf(
						"crash recovery: channel %d restored from state %q (wall-clock delta: %s)",
						pch.ID, pch.State.String(), wallDelta.Truncate(time.Millisecond)),
				})
			}
		}
	}

	m.notifyStateChange()
	return nil
}

// SetOnStateChange registers a callback invoked when any channel state changes.
func (m *Manager) SetOnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// OnChannelLifecycle registers callbacks invoked when a channel starts or stops.
// Callbacks receive the channel ID (1-based) and source key (e.g., "playout:1").
func (m *Manager) OnChannelLifecycle(onStart func(int, string), onStop func(int, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStart = onStart
	m.onStop = onStop
}

// SetRawVideoOutput registers a callback for raw YUV frame output from channels.
// The callback receives the channel source key, frame data, and keyframe flag.
func (m *Manager) SetRawVideoOutput(fn func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawVideoOutput = fn
}

// SetVideoOutput registers a callback for encoded video output from channels.
func (m *Manager) SetVideoOutput(fn func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoOutput = fn
}

// SetAudioOutput registers a callback for audio output from channels.
// Propagates per-channel wrappers so that audio is emitted during playback.
func (m *Manager) SetAudioOutput(fn func(key string, data []byte, pts int64, sampleRate, channels int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioOutput = fn
	// Propagate to all channels.
	for i := 0; i < MaxChannels; i++ {
		capturedKey := fmt.Sprintf("playout:%d", i+1)
		m.channels[i].SetAudioOutput(func(data []byte, pts int64, sampleRate, channels int) {
			fn(capturedKey, data, pts, sampleRate, channels)
		})
	}
}

// SetPTSProvider registers a function that returns the current program PTS.
// It also propagates the provider to all pod boards for splice window calculation.
func (m *Manager) SetPTSProvider(fn func() int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ptsProvider = fn
	for i := 0; i < MaxChannels; i++ {
		m.podBoards[i].SetPTSProvider(fn)
	}
}

// SetSecondaryHandler registers a handler for secondary events dispatched
// during playlist item playback (graphics, audio, captions, SCTE-35).
func (m *Manager) SetSecondaryHandler(h SecondaryEventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secondaryHandler = h
}

// SetSpliceCoordinator registers a splice coordinator and propagates it to all
// pod boards and (on Start) to channels. It also propagates the current PTS
// provider to pod boards if one is already set.
func (m *Manager) SetSpliceCoordinator(sc *SpliceCoordinator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spliceCoordinator = sc
	for i := 0; i < MaxChannels; i++ {
		m.podBoards[i].SetSpliceCoordinator(sc)
		if m.ptsProvider != nil {
			m.podBoards[i].SetPTSProvider(m.ptsProvider)
		}
	}
	if m.asRunLogger != nil && sc != nil {
		sc.SetAsRunLog(func(ev AsRunEvent) { m.asRunLogger.Log(ev) })
	}
}

// SetAsRunLogger sets the as-run logger and propagates to channels and pod boards.
func (m *Manager) SetAsRunLogger(logger *AsRunLogger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asRunLogger = logger
	logFn := func(ev AsRunEvent) { logger.Log(ev) }
	for i := 0; i < MaxChannels; i++ {
		m.channels[i].SetAsRunLog(logFn)
		m.podBoards[i].SetAsRunLog(logFn)
	}
}

// AsRunLogger returns the current as-run logger (may be nil).
func (m *Manager) AsRunLogger() *AsRunLogger {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.asRunLogger
}

// PodBoard returns the pod board for the given channel.
func (m *Manager) PodBoard(channelID int) (*PodBoard, error) {
	if err := m.validateChannelID(channelID); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.podBoards[channelID-1], nil
}

// TemplateStore returns the shared pod template store.
func (m *Manager) TemplateStore() *PodTemplateStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.templateStore
}

// SetSCTE35Injector sets the SCTE-35 injection interface on all pod boards.
func (m *Manager) SetSCTE35Injector(inj podSCTE35Injector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < MaxChannels; i++ {
		m.podBoards[i].SetSCTE35Injector(inj)
		m.podBoards[i].SetTemplateStore(m.templateStore)
	}
}

// SetGPUAlias registers a function for GPU-native live source aliasing.
// The function receives the channel key (e.g., "playout:1") and the live
// source key (e.g., "cam-1"), and aliases the channel's GPU frame to the
// source's GPU frame. Zero-copy — no CPU involvement. Returns true if
// the alias was applied.
func (m *Manager) SetGPUAlias(fn func(channelKey, liveSourceKey string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gpuAlias = fn
}

// SetGPUSourceTap registers a function that reads cached GPU frames for live
// source pass-through (CPU fallback). The function receives the channel key
// (e.g., "playout:1") and source key (e.g., "cam-1") and returns the latest
// decoded frame or nil.
func (m *Manager) SetGPUSourceTap(fn func(channelKey, sourceKey string) *DecodedFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gpuSourceTap = fn
}

// SetPipelineFormat configures the video dimensions and frame rate for all channels.
// Channels are recreated with the new format on next Start(). This should be called
// before loading playlists or starting channels.
func (m *Manager) SetPipelineFormat(width, height int, fps float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pipelineWidth = width
	m.pipelineHeight = height
	m.pipelineFPS = fps

	// Recreate all stopped channels with the new format so that subsequent
	// LoadPlaylist + Start picks up the dimensions immediately.
	for i := 0; i < MaxChannels; i++ {
		if m.channels[i].State() == StateStopped {
			m.channels[i] = NewChannel(i+1, ChannelConfig{
				AssetProvider: m.config.AssetProvider,
				Width:         width,
				Height:        height,
				FPS:           fps,
			})
		}
	}
}

// SetWatchdog sets the watchdog health monitor.
func (m *Manager) SetWatchdog(wd *Watchdog) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watchdog = wd
}

// HealthSummary returns the watchdog health status, or nil if no watchdog.
func (m *Manager) HealthSummary() []ChannelHealth {
	m.mu.Lock()
	wd := m.watchdog
	m.mu.Unlock()
	if wd == nil {
		return nil
	}
	return wd.Health()
}

// RecoverChannel stops a channel, re-cues it at its current position, and restarts.
// Pod board state is not preserved during recovery; active pods are lost.
func (m *Manager) RecoverChannel(channelID int) error {
	if err := m.validateChannelID(channelID); err != nil {
		return err
	}

	m.mu.Lock()
	ch := m.channels[channelID-1]
	asRunLogger := m.asRunLogger
	m.mu.Unlock()

	// Snapshot current state before stopping.
	snap := ch.Snapshot()

	// Stop the channel.
	ch.Stop()

	// Re-load playlist at the current cursor position.
	if snap.Playlist != nil && len(snap.Playlist.Items) > 0 {
		if err := ch.LoadPlaylist(snap.Playlist); err != nil {
			return fmt.Errorf("recover: reload playlist: %w", err)
		}
		if snap.Cursor >= 0 && snap.Cursor < len(snap.Playlist.Items) {
			ch.SetCursor(snap.Cursor)
		}
	}

	// Emit recovery as-run event.
	if asRunLogger != nil {
		asRunLogger.Log(AsRunEvent{
			Timestamp: time.Now().UTC(),
			Type:      AsRunError,
			ChannelID: channelID,
			Message:   fmt.Sprintf("manual recovery triggered at cursor %d", snap.Cursor),
		})
	}

	// Restart.
	return m.Start(channelID)
}

// Close stops all active channels.
func (m *Manager) Close() {
	m.mu.Lock()
	channels := m.channels
	m.mu.Unlock()

	for _, ch := range channels {
		if ch != nil {
			ch.Stop()
		}
	}
}

// notifyStateChange safely reads the callback under lock, releases the lock,
// then invokes the callback. Must be called without m.mu held.
func (m *Manager) notifyStateChange() {
	m.mu.Lock()
	fn := m.onStateChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}
