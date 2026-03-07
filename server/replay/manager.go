package replay

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/transition"
)

// ReplayRelay is the interface for the replay output relay.
type ReplayRelay interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
}

// Manager orchestrates the replay system: per-source buffers, viewers,
// mark-in/out points, and the active player.
type Manager struct {
	log            *slog.Logger
	mu             sync.Mutex
	relay          ReplayRelay
	config         Config
	decoderFactory transition.DecoderFactory
	encoderFactory transition.EncoderFactory

	buffers map[string]*replayBuffer
	viewers map[string]*replayViewer

	markSource string
	markIn     *time.Time
	markOut    *time.Time

	player       *replayPlayer
	playerState  PlayerState
	playerSource string
	playerSpeed  float64
	playerLoop   bool
	playerCtx    context.Context
	playerCancel context.CancelFunc

	onStateChange   func()
	onPlaybackStart func() // called when player transitions to playing
	onPlaybackStop  func() // called when player finishes or is stopped
}

// NewManager creates a replay manager.
func NewManager(relay ReplayRelay, cfg Config, decoderFactory transition.DecoderFactory, encoderFactory transition.EncoderFactory) *Manager {
	if cfg.BufferDurationSecs <= 0 {
		cfg.BufferDurationSecs = 60
	}
	if cfg.BufferDurationSecs > 300 {
		cfg.BufferDurationSecs = 300
	}
	if cfg.MaxBufferBytes == 0 {
		cfg.MaxBufferBytes = 200 * 1024 * 1024 // 200MB default
	}
	return &Manager{
		log:            slog.With("component", "replay"),
		relay:          relay,
		config:         cfg,
		decoderFactory: decoderFactory,
		encoderFactory: encoderFactory,
		buffers:        make(map[string]*replayBuffer),
		viewers:        make(map[string]*replayViewer),
		playerState:    PlayerIdle,
	}
}

// AddSource registers a source for replay buffering. Returns an error if
// the maximum number of sources has been reached.
func (m *Manager) AddSource(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.buffers[key]; exists {
		return nil
	}

	if m.config.MaxSources > 0 && len(m.buffers) >= m.config.MaxSources {
		return ErrMaxSources
	}

	buf := newReplayBuffer(m.config.BufferDurationSecs, m.config.MaxBufferBytes)
	v := newReplayViewer(key, buf)
	m.buffers[key] = buf
	m.viewers[key] = v

	m.log.Info("added source", "key", key, "bufferSecs", m.config.BufferDurationSecs)
	return nil
}

// RemoveSource stops buffering for a source.
func (m *Manager) RemoveSource(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.buffers, key)
	delete(m.viewers, key)

	m.log.Info("removed source", "key", key)
}

// Viewer returns the replay viewer for the given source, or nil if
// the source is not registered for replay. The returned viewer implements
// distribution.Viewer and should be registered on the source's relay.
func (m *Manager) Viewer(key string) *replayViewer {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.viewers[key]
}

// RecordFrame records a frame into the source's replay buffer.
// Called directly from the streamCallbackRouter's viewer.
func (m *Manager) RecordFrame(key string, frame *media.VideoFrame) {
	m.mu.Lock()
	buf, ok := m.buffers[key]
	m.mu.Unlock()
	if !ok {
		return
	}
	buf.RecordFrame(frame)
}

// MarkIn sets the mark-in point to the current time for the given source.
func (m *Manager) MarkIn(source string) error {
	m.mu.Lock()

	if _, ok := m.buffers[source]; !ok {
		m.mu.Unlock()
		return ErrNoSource
	}

	now := time.Now()
	m.markSource = source
	m.markIn = &now
	m.markOut = nil

	m.log.Info("mark-in set", "source", source, "time", now)
	m.mu.Unlock()
	m.notifyStateChange()
	return nil
}

// MarkOut sets the mark-out point to the current time.
func (m *Manager) MarkOut(source string) error {
	m.mu.Lock()

	if _, ok := m.buffers[source]; !ok {
		m.mu.Unlock()
		return ErrNoSource
	}
	if m.markIn == nil {
		m.mu.Unlock()
		return ErrNoMarkIn
	}
	if source != m.markSource {
		m.mu.Unlock()
		return ErrSourceMismatch
	}

	now := time.Now()
	if !now.After(*m.markIn) {
		m.mu.Unlock()
		return ErrInvalidMarks
	}
	m.markOut = &now

	m.log.Info("mark-out set", "source", source, "time", now)
	m.mu.Unlock()
	m.notifyStateChange()
	return nil
}

// Play starts playback of the marked clip at the given speed.
func (m *Manager) Play(source string, speed float64, loop bool) error {
	err := func() error {
		m.mu.Lock()
		defer m.mu.Unlock()

		if _, ok := m.buffers[source]; !ok {
			return ErrNoSource
		}
		if m.markIn == nil {
			return ErrNoMarkIn
		}
		if m.markOut == nil {
			return ErrNoMarkOut
		}
		if speed < 0.25 || speed > 1.0 {
			return ErrInvalidSpeed
		}
		if m.player != nil {
			return ErrPlayerActive
		}

		// Extract clip from buffer.
		buf := m.buffers[source]
		clip, err := buf.ExtractClip(*m.markIn, *m.markOut)
		if err != nil {
			return err
		}

		m.playerState = PlayerLoading
		m.playerSource = source
		m.playerSpeed = speed
		m.playerLoop = loop

		ctx, cancel := context.WithCancel(context.Background())
		m.playerCtx = ctx
		m.playerCancel = cancel

		m.player = newReplayPlayer(PlayerConfig{
			Clip:           clip,
			Speed:          speed,
			Loop:           loop,
			Interpolation:  InterpolationBlend,
			DecoderFactory: m.decoderFactory,
			EncoderFactory: m.encoderFactory,
			Output: func(frame *media.VideoFrame) {
				m.relay.BroadcastVideo(frame)
			},
			OnDone: func() {
				m.mu.Lock()
				m.player = nil
				m.playerState = PlayerIdle
				m.playerCancel = nil
				stopCb := m.onPlaybackStop
				m.mu.Unlock()
				if stopCb != nil {
					stopCb()
				}
				m.notifyStateChange()
			},
			OnReady: func() {
				m.mu.Lock()
				m.playerState = PlayerPlaying
				startCb := m.onPlaybackStart
				m.mu.Unlock()
				if startCb != nil {
					startCb()
				}
				m.notifyStateChange()
			},
		})

		m.player.Start(ctx)

		m.log.Info("playback started", "source", source, "speed", speed, "loop", loop, "clipFrames", len(clip))
		return nil
	}()
	if err != nil {
		return err
	}
	m.notifyStateChange()
	return nil
}

// Stop stops the active player.
func (m *Manager) Stop() error {
	m.mu.Lock()
	player := m.player
	m.mu.Unlock()

	if player == nil {
		return ErrNoPlayer
	}

	player.Stop()
	player.Wait()
	return nil
}

// Status returns the current replay status for state broadcasts.
func (m *Manager) Status() ReplayStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := ReplayStatus{
		State:      m.playerState,
		Source:     m.playerSource,
		Speed:      m.playerSpeed,
		Loop:       m.playerLoop,
		MarkIn:     m.markIn,
		MarkOut:    m.markOut,
		MarkSource: m.markSource,
	}

	if m.player != nil {
		status.Position = m.player.Progress()
	}

	for key, buf := range m.buffers {
		info := buf.Status()
		info.Source = key
		status.Buffers = append(status.Buffers, info)
	}

	sort.Slice(status.Buffers, func(i, j int) bool {
		return status.Buffers[i].Source < status.Buffers[j].Source
	})

	return status
}

// OnPlaybackLifecycle registers callbacks invoked when playback starts and stops.
// onStart is called when the player transitions to playing (first frame decoded).
// onStop is called when the player finishes naturally or is stopped manually.
func (m *Manager) OnPlaybackLifecycle(onStart, onStop func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPlaybackStart = onStart
	m.onPlaybackStop = onStop
}

// OnStateChange registers a callback invoked when replay state changes.
func (m *Manager) OnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// Close stops any active player and releases resources.
func (m *Manager) Close() {
	m.mu.Lock()
	player := m.player
	cancel := m.playerCancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if player != nil {
		player.Wait()
	}
}

// DebugSnapshot returns debug information about the replay system.
func (m *Manager) DebugSnapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()

	buffers := make(map[string]any)
	for key, buf := range m.buffers {
		info := buf.Status()
		buffers[key] = map[string]any{
			"frameCount":   info.FrameCount,
			"gopCount":     info.GOPCount,
			"durationSecs": info.DurationSecs,
			"bytesUsed":    info.BytesUsed,
		}
	}

	return map[string]any{
		"state":      string(m.playerState),
		"source":     m.playerSource,
		"speed":      m.playerSpeed,
		"loop":       m.playerLoop,
		"markSource": m.markSource,
		"buffers":    buffers,
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
