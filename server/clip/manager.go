package clip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"
)

// Manager orchestrates clip player slots, bridging the Store and Players.
// It owns the 4 player slots, handles source registration lifecycle,
// and provides state for broadcast. Follows the replay.Manager pattern.
type Manager struct {
	mu sync.Mutex
	// loadMu[idx] serializes Load calls on the same slot. Load does its slow
	// work (store.Get + DemuxFunc) outside m.mu and then releases m.mu in the
	// middle of its eject path to Stop+Wait the prior player — two windows
	// that would otherwise let concurrent Loads (and a Play racing in)
	// overwrite m.players[idx], orphaning the Play-spawned player goroutine
	// that nothing else holds a cancel for. The per-slot lock makes each
	// Load's demux-plus-eject transactional against other Loads on the slot.
	loadMu [MaxPlayers]sync.Mutex
	// syncMu[idx] serializes SyncPlayer calls on the same slot, so two
	// snapshot applies cannot interleave their load and transport steps.
	syncMu  [MaxPlayers]sync.Mutex
	store   *Store
	config  ManagerConfig
	players [MaxPlayers]*playerSlot
	// loading[idx] is the clip a Load on the slot is demuxing, nil when none.
	// Reported in PlayerStates so a peer engine that already shows the clip
	// loaded reads this slot as catching up rather than diverged.
	loading [MaxPlayers]*Clip
	log     *slog.Logger

	onStateChange func()
	ptsProvider   func() int64
	// Callbacks set by app wiring.
	onPlayerStart  func(playerID int, key string)
	onPlayerStop   func(playerID int, key string)
	rawVideoOutput func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)
	videoOutput    func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)
	audioOutput    func(key string, data []byte, pts int64, sampleRate, channels int)
}

// ManagerConfig configures the clip Manager.
type ManagerConfig struct {
	// DemuxFunc overrides DemuxFile for testing. If nil, uses DemuxFile.
	DemuxFunc func(path string) ([]BufferedFrame, []BufferedAudioFrame, error)
	// DecoderFactory creates a video decoder for each clip player.
	// If nil, wireData is passed through without decoding (for testing).
	// Deprecated: Use DecoderFactoryForCodec for codec-aware decoder creation.
	DecoderFactory func() (VideoDecoder, error)
	// OutputRate returns the frame rate players output at, normally the
	// pipeline's; read when a player starts. If nil, players output at
	// their clip's rate.
	OutputRate func() (num, den int)
	// DecoderFactoryForCodec creates a video decoder for the specified codec
	// ("h264" or "h265"). Takes precedence over DecoderFactory when set.
	// If nil, falls back to DecoderFactory.
	DecoderFactoryForCodec func(codec string) (VideoDecoder, error)
	// EncoderFactory creates a video encoder for re-encoding decoded frames
	// to browser-compatible format. If nil, original wire data is forwarded.
	// fpsNum/fpsDen express the frame rate as a rational number
	// (e.g. 30000/1001 for 29.97fps, 25000/1000 for 25fps).
	EncoderFactory func(w, h, fpsNum, fpsDen int) (VideoEncoder, error)
}

// playerSlot holds the state of a single clip player slot.
type playerSlot struct {
	player        *Player
	clipID        string
	clipName      string
	clip          *Clip // metadata
	frames        []BufferedFrame
	audio         []BufferedAudioFrame
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

	idx := playerID - 1

	// Serialize Loads targeting this slot; see loadMu field comment for why.
	// Held across notifyStateChange at the bottom of the function: any
	// onStateChange callback must not call back into Manager.Load on the same
	// slot (current callback is read-only broadcast enrichment).
	m.loadMu[idx].Lock()
	defer m.loadMu[idx].Unlock()

	// Look up clip in store (outside main lock — store has its own lock).
	c, err := m.store.Get(clipID)
	if err != nil {
		return err
	}

	m.mu.Lock()
	// A Load queued behind another Load of the same clip, as a snapshot
	// apply arriving while the operator's load still demuxes, finds the clip
	// loaded and not started; demuxing it again would only delay the slot.
	if slot := m.players[idx]; slot != nil && slot.clipID == c.ID && slot.player == nil {
		m.mu.Unlock()
		return nil
	}
	m.loading[idx] = c
	m.mu.Unlock()
	m.notifyStateChange()
	loaded := false
	defer func() {
		if loaded {
			return
		}
		m.mu.Lock()
		m.loading[idx] = nil
		m.mu.Unlock()
		m.notifyStateChange()
	}()

	// Demux the media file.
	path := filepath.Join(m.store.Dir(), c.Filename)
	frames, audio, err := m.config.DemuxFunc(path)
	if err != nil {
		return fmt.Errorf("demux clip %s: %w", clipID, err)
	}
	if len(frames) == 0 {
		return ErrNoVideo
	}

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

	m.loading[idx] = nil
	loaded = true
	m.players[idx] = &playerSlot{
		clipID:   c.ID,
		clipName: c.Name,
		clip:     c,
		frames:   frames,
		audio:    audio,
	}

	// Count keyframes and check for SPS/PPS presence.
	keyframes := 0
	firstKeyIdx := -1
	hasSPS := false
	for i, f := range frames {
		if f.isKeyframe {
			keyframes++
			if firstKeyIdx < 0 {
				firstKeyIdx = i
				hasSPS = len(f.sps) > 0
			}
		}
	}
	m.log.Info("clip loaded",
		"player", playerID,
		"clipID", clipID,
		"clipName", c.Name,
		"frames", len(frames),
		"keyframes", keyframes,
		"firstKeyframeIdx", firstKeyIdx,
		"hasSPS", hasSPS,
		"audioFrames", len(audio),
		"width", c.Width,
		"height", c.Height,
	)
	m.mu.Unlock()
	m.notifyStateChange()
	return nil
}

// LoadByName resolves a clip name to its ID via the store and loads it
// into the given player slot. Used for cross-engine snapshot restore where
// clip IDs differ between engines but names are consistent.
func (m *Manager) LoadByName(playerID int, clipName string) error {
	c, err := m.store.GetByName(clipName)
	if err != nil {
		return fmt.Errorf("clip %q: %w", clipName, err)
	}
	return m.Load(playerID, c.ID)
}

// SyncPlayer brings a player slot to the given clip and playback state.
// The clip is resolved by clipID when the store holds that ID, otherwise by
// clipName, since clips created before IDs were shared between engines have
// different IDs on each engine. It is idempotent: a slot that already holds
// the clip is not reloaded, and a slot already in the wanted state is left
// untouched, so applying the same state repeatedly does not interrupt
// playback. An empty clip or StateEmpty ejects the slot.
//
// A StateHolding target is reached by playing the clip to its end. Playback
// position is not synchronized.
func (m *Manager) SyncPlayer(playerID int, clipID, clipName string, state PlayerState, speed float64, loop bool) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}
	switch state {
	case StateEmpty, StateLoaded, StatePlaying, StatePaused, StateHolding:
	default:
		return fmt.Errorf("clip: unsupported player state %q", state)
	}
	idx := playerID - 1
	m.syncMu[idx].Lock()
	defer m.syncMu[idx].Unlock()

	if (clipID == "" && clipName == "") || state == StateEmpty {
		return m.Eject(playerID)
	}

	cur := m.PlayerStates()[idx]
	if m.hasClip(clipID) {
		if cur.ClipID != clipID {
			if err := m.Load(playerID, clipID); err != nil {
				return err
			}
		}
	} else if cur.ClipName != clipName {
		if err := m.LoadByName(playerID, clipName); err != nil {
			return err
		}
	}

	cur = m.PlayerStates()[idx]
	if cur.Loop != loop {
		if err := m.SetLoop(playerID, loop); err != nil {
			return err
		}
	}
	if speed < 0.25 || speed > 2.0 {
		speed = 1.0
	}
	if (cur.State == StatePlaying || cur.State == StatePaused) && state != StateLoaded && cur.Speed != speed {
		if err := m.SetSpeed(playerID, speed); err != nil {
			return err
		}
	}

	switch state {
	case StateLoaded:
		if cur.State != StateLoaded {
			return m.Stop(playerID)
		}
	case StatePlaying:
		switch cur.State {
		case StateLoaded:
			return m.playIgnoringBusy(playerID, speed, loop)
		case StatePaused:
			return m.Resume(playerID)
		case StateHolding:
			// A looping clip restarts on the peer instead of holding, so
			// restart it here. A non-looping one reaches its hold on the
			// peer too.
			if loop {
				if err := m.Stop(playerID); err != nil {
					return err
				}
				return m.playIgnoringBusy(playerID, speed, loop)
			}
		}
	case StateHolding:
		switch cur.State {
		case StateLoaded:
			return m.playIgnoringBusy(playerID, speed, loop)
		case StatePaused:
			return m.Resume(playerID)
		}
	case StatePaused:
		switch cur.State {
		case StateLoaded:
			return m.playPaused(playerID, speed, loop)
		case StatePlaying:
			return m.Pause(playerID)
		case StateHolding:
			// Pause has no effect on a held clip; restart it paused.
			if err := m.Stop(playerID); err != nil {
				return err
			}
			return m.playPaused(playerID, speed, loop)
		}
	}
	return nil
}

// hasClip reports whether the store holds a clip with the given ID.
func (m *Manager) hasClip(clipID string) bool {
	if clipID == "" {
		return false
	}
	_, err := m.store.Get(clipID)
	return err == nil
}

// playIgnoringBusy starts playback, treating a player that is already
// running (started concurrently by an operator command) as success.
func (m *Manager) playIgnoringBusy(playerID int, speed float64, loop bool) error {
	if err := m.Play(playerID, speed, loop); err != nil && !errors.Is(err, ErrPlayerBusy) {
		return err
	}
	return nil
}

// playStartTimeout bounds how long playPaused waits for a player to start.
const playStartTimeout = 250 * time.Millisecond

// playPaused starts playback and pauses it once the player is running. The
// player goroutine marks itself playing asynchronously and Pause has no
// effect before that.
func (m *Manager) playPaused(playerID int, speed float64, loop bool) error {
	if err := m.playIgnoringBusy(playerID, speed, loop); err != nil {
		return err
	}
	deadline := time.Now().Add(playStartTimeout)
	for m.PlayerStates()[playerID-1].State == StateLoaded {
		if time.Now().After(deadline) {
			return fmt.Errorf("clip: player %d did not start within %s", playerID, playStartTimeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return m.Pause(playerID)
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
	videoFn := m.videoOutput
	audioFn := m.audioOutput
	key := playerKey(playerID)

	// Create a per-player decoder if factory is available.
	// Use codec-aware factory when available (HEVC clips need HEVC decoder).
	var dec VideoDecoder
	var decodeFrame func([]byte) ([]byte, int, int, error)
	var decoderCloser func()
	clipCodec := slot.clip.Codec
	if clipCodec == "" {
		clipCodec = "h264"
	}
	if m.config.DecoderFactoryForCodec != nil {
		var err error
		dec, err = m.config.DecoderFactoryForCodec(clipCodec)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("create %s decoder: %w", clipCodec, err)
		}
		decodeFrame = dec.Decode
		decoderCloser = dec.Close
	} else if m.config.DecoderFactory != nil {
		var err error
		dec, err = m.config.DecoderFactory()
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("create decoder: %w", err)
		}
		decodeFrame = dec.Decode
		decoderCloser = dec.Close
	}

	var outNum, outDen int
	if m.config.OutputRate != nil {
		outNum, outDen = m.config.OutputRate()
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := NewPlayer(PlayerConfig{
		OutputFPSNum:   outNum,
		OutputFPSDen:   outDen,
		Clip:           slot.frames,
		AudioClip:      slot.audio,
		Speed:          speed,
		Loop:           loop,
		InitialPTS:     initialPTS,
		Width:          slot.clip.Width,
		Height:         slot.clip.Height,
		DecodeFrame:    decodeFrame,
		Decoder:        dec,
		EncoderFactory: m.config.EncoderFactory,
		RawVideoOutput: func(yuv []byte, w, h int, pts int64, isKeyframe bool) {
			if rawVideoFn != nil {
				rawVideoFn(key, yuv, w, h, pts, isKeyframe)
			}
		},
		VideoOutput: func(wireData []byte, pts int64, isKeyframe bool, sps, pps []byte) {
			if videoFn != nil {
				videoFn(key, wireData, pts, isKeyframe, sps, pps)
			}
		},
		AudioOutput: func(data []byte, pts int64, sampleRate, channels int) {
			if audioFn != nil {
				audioFn(key, data, pts, sampleRate, channels)
			}
		},
		OnDone: func() {
			// Playback finished — clean up decoder (not needed for hold loop)
			// but keep player/cancel alive so Stop/Eject can cancel holdLoop.
			// Do NOT call onPlayerStop: the source stays registered during hold.
			m.mu.Lock()
			if s := m.players[idx]; s != nil {
				if s.decoderCloser != nil {
					s.decoderCloser()
					s.decoderCloser = nil
				}
			}
			m.mu.Unlock()
			m.notifyStateChange()
		},
	})

	slot.player = p
	slot.cancel = cancel
	slot.decoderCloser = decoderCloser
	slot.speed = speed
	slot.loop = loop

	m.log.Info("clip playback started", "player", playerID, "speed", speed, "loop", loop)
	m.mu.Unlock()

	// Register source BEFORE starting player to prevent race where the
	// player outputs frames before the source is registered.
	if startCb != nil {
		startCb(playerID, playerKey(playerID))
	}

	p.Start(ctx)
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
	if slot.player != nil {
		slot.player.SetLoop(loop)
	}
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
		if c := m.loading[i]; c != nil {
			states[i].LoadingClipID = c.ID
			states[i].LoadingClipName = c.Name
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
// The callback receives the player source key, frame data, and keyframe flag.
func (m *Manager) SetRawVideoOutput(fn func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawVideoOutput = fn
}

// SetVideoOutput registers a callback for forwarding original H.264 wire data
// from clip players to browser MoQ relays. The callback receives the player
// source key, AVC1 wire data, PTS, keyframe flag, and SPS/PPS (on keyframes).
func (m *Manager) SetVideoOutput(fn func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.videoOutput = fn
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
