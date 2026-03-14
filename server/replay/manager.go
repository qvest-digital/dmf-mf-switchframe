package replay

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/zsiec/prism/media"
	"github.com/zsiec/switchframe/server/audio"
	"github.com/zsiec/switchframe/server/output"
	"github.com/zsiec/switchframe/server/transition"
)

// Relay is the interface for the replay output relay.
type Relay interface {
	BroadcastVideo(frame *media.VideoFrame)
	BroadcastAudio(frame *media.AudioFrame)
}

// Manager orchestrates the replay system: per-source buffers, viewers,
// mark-in/out points, and the active player.
type Manager struct {
	log            *slog.Logger
	mu             sync.Mutex
	relay          Relay
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

	onStateChange     func()
	onPlaybackStart   func() // called when player transitions to playing
	onPlaybackStop    func() // called when player finishes or is stopped
	onVideoInfoChange func(sps, pps []byte, width, height int)
	ptsProvider       func() int64

	// Raw output callbacks for uncompressed pipeline.
	rawVideoOutput   func(yuv []byte, w, h int, pts int64)
	rawMonitorOutput func(yuv []byte, w, h int, pts int64)
	audioOutput      func(frame *media.AudioFrame) // direct audio output (e.g. to mixer)

	// Audio codec factories for WSOLA time-stretching.
	audioDecoderFactory audio.DecoderFactory
	audioEncoderFactory audio.EncoderFactory

	// onClipExported is called after Play() extracts a clip, with the
	// source name and path to a temp TS file containing the muxed frames.
	onClipExported func(source string, filePath string)
}

// NewManager creates a replay manager.
func NewManager(relay Relay, cfg Config, decoderFactory transition.DecoderFactory, encoderFactory transition.EncoderFactory) *Manager {
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
		clip, audioClip, err := buf.ExtractClip(*m.markIn, *m.markOut)
		if err != nil {
			return err
		}

		// Export clip to temp TS file for the clip store (background).
		if exportFn := m.onClipExported; exportFn != nil {
			go func() {
				path, muxErr := muxClipToTempFile(clip, audioClip)
				if muxErr != nil {
					m.log.Error("failed to export replay clip", "source", source, "error", muxErr)
					return
				}
				exportFn(source, path)
			}()
		}

		m.playerState = PlayerLoading
		m.playerSource = source
		m.playerSpeed = speed
		m.playerLoop = loop

		// Anchor replay PTS to program timeline to prevent backward jumps.
		var initialPTS int64
		if m.ptsProvider != nil {
			initialPTS = m.ptsProvider()
			if initialPTS > 0 {
				initialPTS += 3003 // one frame ahead at 30fps to avoid exact duplicate
			}
		}

		ctx, cancel := context.WithCancel(context.Background())
		m.playerCtx = ctx
		m.playerCancel = cancel

		videoInfoCb := m.onVideoInfoChange
		rawVideoCb := m.rawVideoOutput
		rawMonitorCb := m.rawMonitorOutput
		audioOutputCb := m.audioOutput

		m.player = newReplayPlayer(PlayerConfig{
			Clip:           clip,
			AudioClip:      audioClip,
			Speed:          speed,
			Loop:           loop,
			InitialPTS:     initialPTS,
			Interpolation:  InterpolationPulldown,
			DecoderFactory: m.decoderFactory,
			EncoderFactory: m.encoderFactory,
			Output: func(frame *media.VideoFrame) {
				m.relay.BroadcastVideo(frame)
			},
			AudioOutput: func(frame *media.AudioFrame) {
				m.relay.BroadcastAudio(frame)
				if audioOutputCb != nil {
					audioOutputCb(frame)
				}
			},
			RawVideoOutput:      rawVideoCb,
			RawMonitorOutput:    rawMonitorCb,
			AudioDecoderFactory: m.audioDecoderFactory,
			AudioEncoderFactory: m.audioEncoderFactory,
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
			OnVideoInfo: videoInfoCb,
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
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	status := Status{
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

	slices.SortFunc(status.Buffers, func(a, b SourceBufferInfo) int {
		return cmp.Compare(a.Source, b.Source)
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

// OnVideoInfoChange registers a callback invoked when the replay player
// produces its first keyframe with SPS/PPS. Used to set VideoInfo on the
// replay relay so MoQ subscribers can discover tracks.
func (m *Manager) OnVideoInfoChange(fn func(sps, pps []byte, width, height int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onVideoInfoChange = fn
}

// SetPTSProvider registers a function that returns the current program PTS.
// The replay player uses this to anchor its output PTS to the program
// timeline, preventing backward PTS jumps when cut to program.
func (m *Manager) SetPTSProvider(fn func() int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ptsProvider = fn
}

// SetRawVideoOutput registers a callback for sending decoded YUV frames
// directly to the switcher pipeline (primary output path).
func (m *Manager) SetRawVideoOutput(fn func(yuv []byte, w, h int, pts int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawVideoOutput = fn
}

// SetRawMonitorOutput registers a callback for sending raw YUV to a
// monitoring relay (e.g. "replay-raw" track).
func (m *Manager) SetRawMonitorOutput(fn func(yuv []byte, w, h int, pts int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawMonitorOutput = fn
}

// SetAudioOutput registers a callback for sending audio directly to the
// mixer, bypassing the relay encode/decode hop.
func (m *Manager) SetAudioOutput(fn func(frame *media.AudioFrame)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioOutput = fn
}

// SetAudioCodecFactories sets the AAC decoder/encoder factories used for
// WSOLA audio time-stretching during slow-motion playback.
func (m *Manager) SetAudioCodecFactories(decFactory audio.DecoderFactory, encFactory audio.EncoderFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioDecoderFactory = decFactory
	m.audioEncoderFactory = encFactory
}

// SetOnClipExported registers a callback invoked after Play() extracts a
// clip from the replay buffer. The callback receives the source name and
// a path to a temporary MPEG-TS file containing the muxed clip frames.
// The caller is responsible for moving or removing the temp file.
func (m *Manager) SetOnClipExported(fn func(source string, filePath string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onClipExported = fn
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

// muxClipToTempFile muxes extracted replay frames into a temporary MPEG-TS
// file and returns its path. The caller is responsible for moving or removing
// the file. Uses output.TSMuxer to produce valid MPEG-TS with PAT/PMT.
func muxClipToTempFile(videoFrames []bufferedFrame, audioFrames []bufferedAudioFrame) (string, error) {
	tmpFile, err := os.CreateTemp("", "replay-clip-*.ts")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	muxer := output.NewTSMuxer()
	muxer.SetOutput(func(data []byte) {
		_, _ = tmpFile.Write(data)
	})

	// Interleave video and audio by PTS order.
	vi, ai := 0, 0
	for vi < len(videoFrames) || ai < len(audioFrames) {
		// Determine which frame comes next by PTS.
		writeVideo := false
		if vi < len(videoFrames) && ai < len(audioFrames) {
			writeVideo = videoFrames[vi].pts <= audioFrames[ai].pts
		} else {
			writeVideo = vi < len(videoFrames)
		}

		if writeVideo {
			f := videoFrames[vi]
			vf := &media.VideoFrame{
				WireData:   f.wireData,
				PTS:        f.pts,
				DTS:        f.pts,
				IsKeyframe: f.isKeyframe,
				SPS:        f.sps,
				PPS:        f.pps,
				Codec:      "avc1",
			}
			if muxErr := muxer.WriteVideo(vf); muxErr != nil {
				_ = tmpFile.Close()
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("mux video frame %d: %w", vi, muxErr)
			}
			vi++
		} else {
			f := audioFrames[ai]
			af := &media.AudioFrame{
				Data:       f.data,
				PTS:        f.pts,
				SampleRate: f.sampleRate,
				Channels:   f.channels,
			}
			if muxErr := muxer.WriteAudio(af); muxErr != nil {
				_ = tmpFile.Close()
				_ = os.Remove(tmpPath)
				return "", fmt.Errorf("mux audio frame %d: %w", ai, muxErr)
			}
			ai++
		}
	}

	// Close muxer before file to ensure all data is flushed to the output callback.
	_ = muxer.Close()

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}

	return tmpPath, nil
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
