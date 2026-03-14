package clip

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
)

// Manager orchestrates clip player slots, bridging the Store and Players.
// It owns the 4 player slots, handles source registration lifecycle,
// and provides state for broadcast. Follows the replay.Manager pattern.
type Manager struct {
	mu      sync.Mutex
	store   *Store
	config  ManagerConfig
	players [MaxPlayers]*playerSlot
	log     *slog.Logger

	onStateChange func()
	ptsProvider   func() int64
	// Callbacks set by app wiring.
	onPlayerStart  func(playerID int, key string)
	onPlayerStop   func(playerID int, key string)
	rawVideoOutput func(key string, yuv []byte, w, h int, pts int64)
	audioOutput    func(key string, data []byte, pts int64, sampleRate, channels int)
}

// ManagerConfig configures the clip Manager.
type ManagerConfig struct {
	// DemuxFunc overrides DemuxFile for testing. If nil, uses DemuxFile.
	DemuxFunc func(path string) ([]bufferedFrame, []bufferedAudioFrame, error)
	// DecoderFactory creates an H.264 decoder for each clip player.
	// If nil, wireData is passed through without decoding (for testing).
	DecoderFactory func() (VideoDecoder, error)
}

// playerSlot holds the state of a single clip player slot.
type playerSlot struct {
	player        *Player
	clipID        string
	clipName      string
	clip          *Clip // metadata
	frames        []bufferedFrame
	audio         []bufferedAudioFrame
	speed         float64
	loop          bool
	cancel        context.CancelFunc
	decoderCloser func()
}

// NewManager creates a clip Manager backed by the given Store.
func NewManager(store *Store, cfg ManagerConfig) *Manager {
	if cfg.DemuxFunc == nil {
		cfg.DemuxFunc = DemuxFile
	}
	return &Manager{
		store:  store,
		config: cfg,
		log:    slog.With("component", "clip-manager"),
	}
}

// validatePlayerID checks that playerID is in the 1-4 range.
func validatePlayerID(playerID int) error {
	if playerID < 1 || playerID > MaxPlayers {
		return ErrInvalidPlayer
	}
	return nil
}

// Load validates the player ID (1-4), looks up the clip in the store, demuxes
// the media file, and stores the frames in the slot as StateLoaded.
// If the slot already has content, it ejects the existing content first.
func (m *Manager) Load(playerID int, clipID string) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	// Look up clip in store (outside main lock — store has its own lock).
	c, err := m.store.Get(clipID)
	if err != nil {
		return err
	}

	// Demux the media file.
	path := filepath.Join(m.store.Dir(), c.Filename)
	frames, audio, err := m.config.DemuxFunc(path)
	if err != nil {
		return fmt.Errorf("demux clip %s: %w", clipID, err)
	}
	if len(frames) == 0 {
		return ErrNoVideo
	}

	idx := playerID - 1

	m.mu.Lock()

	// If slot already has content, eject first.
	if slot := m.players[idx]; slot != nil {
		wasPlaying := slot.player != nil
		if wasPlaying {
			player := slot.player
			cancel := slot.cancel
			closer := slot.decoderCloser
			stopCb := m.onPlayerStop
			m.players[idx] = nil
			m.mu.Unlock()

			// Stop outside lock.
			if cancel != nil {
				cancel()
			}
			player.Stop()
			player.Wait()
			if closer != nil {
				closer()
			}
			if stopCb != nil {
				stopCb(playerID, playerKey(playerID))
			}
			m.mu.Lock()
		}
	}

	m.players[idx] = &playerSlot{
		clipID:   c.ID,
		clipName: c.Name,
		clip:     c,
		frames:   frames,
		audio:    audio,
	}

	m.log.Info("clip loaded", "player", playerID, "clipID", clipID, "clipName", c.Name, "frames", len(frames))
	m.mu.Unlock()
	m.notifyStateChange()
	return nil
}

// Eject stops the player if active, calls onPlayerStop if it was playing,
// and clears the slot to empty state.
func (m *Manager) Eject(playerID int) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil {
		m.mu.Unlock()
		return nil
	}

	wasPlaying := slot.player != nil
	player := slot.player
	cancel := slot.cancel
	closer := slot.decoderCloser
	stopCb := m.onPlayerStop
	m.players[idx] = nil
	m.mu.Unlock()

	if wasPlaying {
		if cancel != nil {
			cancel()
		}
		player.Stop()
		player.Wait()
		if closer != nil {
			closer()
		}
		if stopCb != nil {
			stopCb(playerID, playerKey(playerID))
		}
	}

	m.log.Info("clip ejected", "player", playerID)
	m.notifyStateChange()
	return nil
}

// Play creates a Player with the stored frames and starts playback.
// If the player is paused, it resumes instead. Valid speed range: 0.25-2.0.
func (m *Manager) Play(playerID int, speed float64, loop bool) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}
	if speed < 0.25 || speed > 2.0 {
		return ErrInvalidSpeed
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}

	// If paused, resume instead of error.
	if slot.player != nil && slot.player.State() == StatePaused {
		slot.player.Resume()
		m.mu.Unlock()
		m.notifyStateChange()
		return nil
	}

	// If already playing, return error.
	if slot.player != nil {
		m.mu.Unlock()
		return ErrPlayerBusy
	}

	// Anchor PTS to program timeline.
	var initialPTS int64
	if m.ptsProvider != nil {
		initialPTS = m.ptsProvider()
		if initialPTS > 0 {
			initialPTS += 3003 // one frame ahead at 30fps
		}
	}

	startCb := m.onPlayerStart
	// Capture output callbacks once under lock — avoids per-frame mutex in hot path.
	rawVideoFn := m.rawVideoOutput
	audioFn := m.audioOutput
	key := playerKey(playerID)

	// Create a per-player decoder if factory is available.
	var decodeFrame func([]byte) ([]byte, int, int, error)
	var decoderCloser func()
	if m.config.DecoderFactory != nil {
		dec, err := m.config.DecoderFactory()
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("create decoder: %w", err)
		}
		decodeFrame = dec.Decode
		decoderCloser = dec.Close
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := NewPlayer(PlayerConfig{
		Clip:       slot.frames,
		AudioClip:  slot.audio,
		Speed:      speed,
		Loop:       loop,
		InitialPTS: initialPTS,
		Width:      slot.clip.Width,
		Height:     slot.clip.Height,
		DecodeFrame: decodeFrame,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64) {
			if rawVideoFn != nil {
				rawVideoFn(key, yuv, w, h, pts)
			}
		},
		AudioOutput: func(data []byte, pts int64, sampleRate, channels int) {
			if audioFn != nil {
				audioFn(key, data, pts, sampleRate, channels)
			}
		},
		OnDone: func() {
			m.mu.Lock()
			if s := m.players[idx]; s != nil {
				if s.decoderCloser != nil {
					s.decoderCloser()
					s.decoderCloser = nil
				}
				s.player = nil
				s.cancel = nil
			}
			stopCb := m.onPlayerStop
			m.mu.Unlock()
			if stopCb != nil {
				stopCb(playerID, playerKey(playerID))
			}
			m.notifyStateChange()
		},
	})

	slot.player = p
	slot.cancel = cancel
	slot.decoderCloser = decoderCloser
	slot.speed = speed
	slot.loop = loop

	p.Start(ctx)

	m.log.Info("clip playback started", "player", playerID, "speed", speed, "loop", loop)
	m.mu.Unlock()

	// Call start callback outside lock.
	if startCb != nil {
		startCb(playerID, playerKey(playerID))
	}

	m.notifyStateChange()
	return nil
}

// Pause pauses playback for the given player slot.
func (m *Manager) Pause(playerID int) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil || slot.player == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	player := slot.player
	m.mu.Unlock()

	player.Pause()
	m.notifyStateChange()
	return nil
}

// Resume resumes playback from a paused state.
func (m *Manager) Resume(playerID int) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil || slot.player == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	player := slot.player
	m.mu.Unlock()

	player.Resume()
	m.notifyStateChange()
	return nil
}

// Stop stops the player but keeps the clip loaded (can replay).
func (m *Manager) Stop(playerID int) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil || slot.player == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	player := slot.player
	cancel := slot.cancel
	closer := slot.decoderCloser
	slot.player = nil
	slot.cancel = nil
	slot.decoderCloser = nil
	stopCb := m.onPlayerStop
	m.mu.Unlock()

	// Stop outside lock.
	if cancel != nil {
		cancel()
	}
	player.Stop()
	player.Wait()

	if closer != nil {
		closer()
	}

	if stopCb != nil {
		stopCb(playerID, playerKey(playerID))
	}

	m.notifyStateChange()
	return nil
}

// SetSpeed changes the playback speed of an active player mid-playback.
func (m *Manager) SetSpeed(playerID int, speed float64) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}
	if speed < 0.25 || speed > 2.0 {
		return ErrInvalidSpeed
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil || slot.player == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	player := slot.player
	slot.speed = speed
	m.mu.Unlock()

	player.SetSpeed(speed)
	m.notifyStateChange()
	return nil
}

// SetLoop changes the loop setting of a player slot.
func (m *Manager) SetLoop(playerID int, loop bool) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	slot.loop = loop
	m.mu.Unlock()

	m.notifyStateChange()
	return nil
}

// Seek delegates to the player's Seek method. Returns an error if no active player.
func (m *Manager) Seek(playerID int, position float64) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	slot := m.players[idx]
	if slot == nil || slot.player == nil {
		m.mu.Unlock()
		return ErrPlayerEmpty
	}
	player := slot.player
	m.mu.Unlock()

	player.Seek(position)
	m.notifyStateChange()
	return nil
}

// PlayerStates returns a 4-element array of ClipPlayerState for broadcast.
func (m *Manager) PlayerStates() []ClipPlayerState {
	m.mu.Lock()
	defer m.mu.Unlock()

	states := make([]ClipPlayerState, MaxPlayers)
	for i := 0; i < MaxPlayers; i++ {
		states[i] = ClipPlayerState{
			ID:    i + 1,
			State: StateEmpty,
		}

		slot := m.players[i]
		if slot == nil {
			continue
		}

		states[i].ClipID = slot.clipID
		states[i].ClipName = slot.clipName

		states[i].Loop = slot.loop

		if slot.player != nil {
			states[i].State = slot.player.State()
			states[i].Speed = slot.speed
			states[i].Position = slot.player.Progress()
		} else {
			states[i].State = StateLoaded
		}
	}
	return states
}

// SetOnStateChange registers a callback invoked when clip state changes.
func (m *Manager) SetOnStateChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onStateChange = fn
}

// SetPTSProvider registers a function that returns the current program PTS.
// The clip player uses this to anchor its output PTS to the program
// timeline, preventing backward PTS jumps when cut to program.
func (m *Manager) SetPTSProvider(fn func() int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ptsProvider = fn
}

// OnPlayerLifecycle registers callbacks invoked when a player starts or stops.
// onStart is called when a clip player begins playback.
// onStop is called when a clip player finishes or is stopped/ejected.
// Callbacks receive the player ID (1-based) and source key (e.g., "clip:1").
func (m *Manager) OnPlayerLifecycle(onStart func(int, string), onStop func(int, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onPlayerStart = onStart
	m.onPlayerStop = onStop
}

// SetRawVideoOutput registers a callback for raw YUV frame output from clip players.
// The callback receives the player source key and frame data.
func (m *Manager) SetRawVideoOutput(fn func(key string, yuv []byte, w, h int, pts int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawVideoOutput = fn
}

// SetAudioOutput registers a callback for audio frame output from clip players.
// The callback receives the player source key, raw audio data, PTS, sample rate,
// and channel count (needed by the mixer for ADTS header construction).
func (m *Manager) SetAudioOutput(fn func(key string, data []byte, pts int64, sampleRate, channels int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audioOutput = fn
}

// LoadedClipIDs returns the set of clip IDs currently loaded in player slots.
// Used to protect loaded clips from ephemeral cleanup.
func (m *Manager) LoadedClipIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make(map[string]bool)
	for _, slot := range m.players {
		if slot != nil && slot.clipID != "" {
			ids[slot.clipID] = true
		}
	}
	return ids
}

// Close stops all active players.
func (m *Manager) Close() {
	m.mu.Lock()
	// Collect all active players and cancels.
	type activePlayer struct {
		player        *Player
		cancel        context.CancelFunc
		decoderCloser func()
	}
	var active []activePlayer
	for i := range m.players {
		if m.players[i] != nil && m.players[i].player != nil {
			active = append(active, activePlayer{
				player:        m.players[i].player,
				cancel:        m.players[i].cancel,
				decoderCloser: m.players[i].decoderCloser,
			})
			m.players[i].player = nil
			m.players[i].cancel = nil
			m.players[i].decoderCloser = nil
		}
	}
	m.mu.Unlock()

	// Stop all players outside lock.
	for _, a := range active {
		if a.cancel != nil {
			a.cancel()
		}
		a.player.Stop()
		a.player.Wait()
		if a.decoderCloser != nil {
			a.decoderCloser()
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

// playerKey returns the virtual source key for a clip player slot.
// Format: "clip:N" where N is the 1-based player ID.
func playerKey(playerID int) string {
	return fmt.Sprintf("clip:%d", playerID)
}
