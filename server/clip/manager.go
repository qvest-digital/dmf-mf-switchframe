package clip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
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
	// cue[idx] is players[idx].cue, set and cleared with it under mu, for
	// StartCuedAt, which runs on a program tick and does not wait for mu.
	cue [MaxPlayers]atomic.Pointer[Player]
	// loading[idx] is the clip a Load on the slot is demuxing, nil when none.
	// Reported in PlayerStates so a peer engine that already shows the clip
	// loaded reads this slot as catching up rather than diverged.
	loading [MaxPlayers]*Clip
	log     *slog.Logger
	starts  *startStats

	onStateChange func()
	ptsProvider   func() int64
	// Callbacks set by app wiring.
	onPlayerStart  func(playerID int, key string)
	onPlayerStop   func(playerID int, key string)
	rawVideoOutput func(key string, yuv []byte, w, h int, pts int64, isKeyframe bool)
	rawVideoStage  func(key string, yuv []byte, w, h int, isKeyframe bool) (put func(pts int64), drop func())
	videoOutput    func(key string, wireData []byte, pts int64, isKeyframe bool, sps, pps []byte)
	audioOutput    func(key string, data []byte, pts int64, sampleRate, channels int)
	pcmOutput      func(key string, pcm []float32, pts int64, channels int, start bool)
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
	// Timed places the players on the engine timeline (PlayerConfig.Timed)
	// and keeps a slot that holds a clip and plays none cued
	// (PlayerConfig.Cued), so a start puts the clip's first picture out at
	// once. The lifecycle callbacks then follow the clip rather than its
	// playback: the start one runs when a slot gets a clip, the stop one
	// when it is ejected, so the slot's source can be cut to before it
	// plays.
	Timed bool
	// AudioRate is the rate of the PCM the players hand to SetPCMOutput,
	// 48 kHz when 0, and PCMCodec what they decode it with.
	AudioRate int
	PCMCodec  PCMCodec
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
	// cue is the cued player of a slot that plays nothing
	// (ManagerConfig.Timed), with its cancel and its decoder's closer.
	cue       *Player
	cueCancel context.CancelFunc
	cueCloser func()
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
		starts: newStartStats(),
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
	path := m.store.MediaPath(c)
	frames, audio, err := m.config.DemuxFunc(path)
	if err != nil {
		return fmt.Errorf("demux clip %s: %w", clipID, err)
	}
	if len(frames) == 0 {
		return ErrNoVideo
	}
	demuxed := len(frames)
	frames, audio = fromFirstKeyframe(frames, audio)

	m.mu.Lock()

	// If slot already has content, eject first. Timed, its source stays.
	hadClip := m.players[idx] != nil
	if slot := m.players[idx]; slot != nil {
		if stopCue := m.takeCueLocked(idx); stopCue != nil {
			m.mu.Unlock()
			stopCue()
			m.mu.Lock()
		}
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
			if stopCb != nil && !m.config.Timed {
				stopCb(playerID, playerKey(playerID))
			}
			m.mu.Lock()
		}
	}

	m.loading[idx] = nil
	loaded = true
	slot := &playerSlot{
		clipID:   c.ID,
		clipName: c.Name,
		clip:     c,
		frames:   frames,
		audio:    audio,
	}
	m.players[idx] = slot
	var startCb func(int, string)
	if m.config.Timed && !hadClip {
		startCb = m.onPlayerStart
	}

	// Count keyframes and check for SPS/PPS presence.
	keyframes := 0
	hasSPS := false
	for _, f := range frames {
		if f.isKeyframe {
			if keyframes == 0 {
				hasSPS = len(f.sps) > 0
			}
			keyframes++
		}
	}
	m.log.Info("clip loaded",
		"player", playerID,
		"clipID", clipID,
		"clipName", c.Name,
		"frames", len(frames),
		"framesDropped", demuxed-len(frames),
		"keyframes", keyframes,
		"hasSPS", hasSPS,
		"audioFrames", len(audio),
		"width", c.Width,
		"height", c.Height,
	)
	m.mu.Unlock()
	// The source exists before the cued player puts its first picture out.
	if startCb != nil {
		startCb(playerID, playerKey(playerID))
	}
	m.mu.Lock()
	if m.players[idx] == slot {
		m.cueLocked(idx, slot)
	}
	m.mu.Unlock()
	m.notifyStateChange()
	return nil
}

// takeCueLocked takes the cued player out of slot idx and returns what
// stops it, to run without m.mu, or nil when the slot has none.
func (m *Manager) takeCueLocked(idx int) func() {
	slot := m.players[idx]
	p, cancel, closer := slot.cue, slot.cueCancel, slot.cueCloser
	if p == nil {
		return nil
	}
	slot.cue, slot.cueCancel, slot.cueCloser = nil, nil, nil
	m.cue[idx].Store(nil)
	return func() {
		cancel()
		p.Stop()
		p.Wait()
		if closer != nil {
			closer()
		}
	}
}

// fromFirstKeyframe drops the frames before the first keyframe, the
// leading pictures after it, and the audio before it. A stream cut
// mid-GOP starts with frames that reference pictures it does not carry,
// and one cut at an open GOP has leading pictures: frames after the
// keyframe in decode order that show before it and reference the GOP
// before. A fresh decoder drops both, but the decoder a looping player
// keeps into the next pass holds the previous pass's references and puts
// out wrong pictures for them. Starting at the keyframe gives every pass
// the same pictures, and the audio, spread over the pictures, stays with
// them. Audio is cut only when a frame was: the AAC frame that spans the
// keyframe stays, and so does all audio of a clip that starts on a
// keyframe.
//
// A frame demuxed without a PTS carries 0 and is never a leading
// picture. Across a PTS wrap presentation order cannot be read from the
// PTS: the pictures after the wrap compare below the keyframe, and so
// does the audio after it, which the demuxer sorts to the front. Leading
// pictures and audio are then kept and only the frames before the
// keyframe go. A clip with no keyframe is returned as it is.
func fromFirstKeyframe(frames []BufferedFrame, audio []BufferedAudioFrame) ([]BufferedFrame, []BufferedAudioFrame) {
	first := slices.IndexFunc(frames, func(f BufferedFrame) bool { return f.isKeyframe })
	if first < 0 {
		return frames, audio
	}
	key := frames[first].pts
	wrapped := ptsWraps(frames)
	// Capacity 1 makes the first append copy, leaving the demuxed slice as
	// it was.
	kept := frames[first : first+1 : first+1]
	leading := !wrapped
	for _, f := range frames[first+1:] {
		if f.isKeyframe {
			leading = false
		}
		if leading && f.pts != 0 && f.pts < key {
			continue
		}
		kept = append(kept, f)
	}
	if len(kept) == len(frames) {
		return frames, audio
	}
	if wrapped {
		return kept, audio
	}
	return kept, AudioFrom(audio, key)
}

// AudioFrom drops the audio frames that end before pts, keeping the one
// before the first that does not, hidden: a frame decodes right only
// after the one before it.
func AudioFrom(audio []BufferedAudioFrame, pts int64) []BufferedAudioFrame {
	i := sort.Search(len(audio), func(j int) bool {
		return audio[j].pts >= pts || audio[j].pts+aacFrameTicks(audio[j]) > pts
	})
	if i == 0 {
		return audio
	}
	out := append([]BufferedAudioFrame(nil), audio[i-1:]...)
	out[0].hidden = true
	return out
}

// PresentedAudio returns the frames of audio that are not hidden, the
// ones a stream of AAC frames carries.
func PresentedAudio(audio []BufferedAudioFrame) []BufferedAudioFrame {
	if !slices.ContainsFunc(audio, func(f BufferedAudioFrame) bool { return f.hidden }) {
		return audio
	}
	return slices.DeleteFunc(slices.Clone(audio), func(f BufferedAudioFrame) bool { return f.hidden })
}

// ptsWraps reports whether the video PTS steps back by more than half the
// 33-bit PTS range, which reordering never does and a wrap always does. A
// frame without a PTS carries 0 and is skipped, or a PTS above 2^32
// before it would read as a wrap. The audio is not checked: the demuxer
// sorts it by PTS, so a wrap never shows in it as a step back.
func ptsWraps(frames []BufferedFrame) bool {
	var last int64
	for _, f := range frames {
		if f.pts == 0 {
			continue
		}
		if last-f.pts > 1<<32 {
			return true
		}
		last = f.pts
	}
	return false
}

// aacFrameTicks is the duration of an AAC frame of 1024 samples in 90 kHz
// ticks, 0 when the sample rate is unknown.
func aacFrameTicks(a BufferedAudioFrame) int64 {
	if a.sampleRate <= 0 {
		return 0
	}
	return 1024 * 90000 / int64(a.sampleRate)
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
	stopCue := m.takeCueLocked(idx)
	m.players[idx] = nil
	m.mu.Unlock()

	if stopCue != nil {
		stopCue()
	}
	if wasPlaying {
		if cancel != nil {
			cancel()
		}
		player.Stop()
		player.Wait()
		if closer != nil {
			closer()
		}
		if stopCb != nil && !m.config.Timed {
			stopCb(playerID, playerKey(playerID))
		}
	}
	// Timed, the source lives as long as the clip, and goes once nothing
	// outputs to it any more.
	if m.config.Timed && stopCb != nil {
		stopCb(playerID, playerKey(playerID))
	}

	m.log.Info("clip ejected", "player", playerID)
	m.notifyStateChange()
	return nil
}

// Play creates a Player with the stored frames and starts playback.
// If the player is paused, it resumes instead. Valid speed range: 0.25-2.0.
// A slot cued (ManagerConfig.Timed) starts its cued player, whose first
// picture is decoded already.
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

	var startCb func(int, string)
	if !m.config.Timed {
		startCb = m.onPlayerStart
	}

	p, cancel, closer := slot.cue, slot.cueCancel, slot.cueCloser
	slot.cue, slot.cueCancel, slot.cueCloser = nil, nil, nil
	m.cue[idx].Store(nil)
	cued := p != nil
	var ctx context.Context
	if cued {
		p.SetSpeed(speed)
		p.SetLoop(loop)
	} else {
		var err error
		ctx, cancel = context.WithCancel(context.Background())
		p, closer, err = m.newPlayer(idx, slot, speed, loop, false)
		if err != nil {
			cancel()
			m.mu.Unlock()
			return err
		}
	}

	slot.player = p
	slot.cancel = cancel
	slot.decoderCloser = closer
	slot.speed = speed
	slot.loop = loop

	m.log.Info("clip playback started", "player", playerID, "speed", speed, "loop", loop, "cued", cued)
	m.mu.Unlock()

	// Register source BEFORE starting player to prevent race where the
	// player outputs frames before the source is registered.
	if startCb != nil {
		startCb(playerID, playerKey(playerID))
	}

	if cued {
		p.Release()
	} else {
		p.Start(ctx)
	}
	m.notifyStateChange()
	return nil
}

// StartCued starts a slot that holds a clip it is not playing, at the
// speed and loop setting it holds: the start a cut or a transition to the
// slot's source makes. It reports whether the slot started.
func (m *Manager) StartCued(playerID int) bool {
	if validatePlayerID(playerID) != nil {
		return false
	}
	m.mu.Lock()
	slot := m.players[playerID-1]
	if slot == nil || slot.player != nil {
		m.mu.Unlock()
		return false
	}
	speed, loop := slot.speed, slot.loop
	m.mu.Unlock()
	if speed < 0.25 || speed > 2.0 {
		speed = 1.0
	}
	return m.Play(playerID, speed, loop) == nil
}

// StartCuedAt starts a slot's cue as StartCued does, on the program tick
// whose read point is readPTS (Player.StartAt). It runs on that tick, so
// it waits for no lock of the manager: the slot takes the started player
// as its own, and the state change is reported, apart. A slot without a
// cue starts nothing.
func (m *Manager) StartCuedAt(playerID int, readPTS int64) {
	if validatePlayerID(playerID) != nil {
		return
	}
	if p := m.cue[playerID-1].Load(); p != nil && p.StartAt(readPTS) {
		go m.playCue(playerID, p)
	}
}

// playCue makes p, the cue of a slot StartCuedAt started, the slot's
// player, as StartCued does, and reports the start. A slot that dropped
// the cue meanwhile is left alone.
func (m *Manager) playCue(playerID int, p *Player) {
	idx := playerID - 1
	m.mu.Lock()
	slot := m.players[idx]
	ours := slot != nil && (slot.cue == p || slot.player == p)
	m.promoteCueLocked(idx)
	m.mu.Unlock()
	if ours {
		m.notifyStateChange()
	}
}

// promoteCueLocked makes the cue of slot idx its player once a program
// tick started it (StartCuedAt): until playCue runs, the slot would read
// as loaded and refuse a stop, a pause or a seek of the clip it plays.
// Caller holds m.mu.
func (m *Manager) promoteCueLocked(idx int) {
	slot := m.players[idx]
	if slot == nil || slot.player != nil || slot.cue == nil || !slot.cue.released() {
		return
	}
	slot.player, slot.cancel, slot.decoderCloser = slot.cue, slot.cueCancel, slot.cueCloser
	slot.cue, slot.cueCancel, slot.cueCloser = nil, nil, nil
	m.cue[idx].Store(nil)
	if slot.speed < 0.25 || slot.speed > 2.0 {
		slot.speed = 1.0
	}
	m.log.Info("clip playback started", "player", idx+1, "speed", slot.speed, "loop", slot.loop, "cued", true)
}

// cueLocked gives a slot that holds a clip and plays none a cued player,
// which decodes the clip's first picture and holds it. Caller holds m.mu.
func (m *Manager) cueLocked(idx int, slot *playerSlot) {
	if !m.config.Timed || slot.player != nil || slot.cue != nil {
		return
	}
	speed := slot.speed
	if speed < 0.25 || speed > 2.0 {
		speed = 1.0
	}
	p, closer, err := m.newPlayer(idx, slot, speed, slot.loop, true)
	if err != nil {
		m.log.Warn("clip: cannot cue", "player", idx+1, "error", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	slot.cue, slot.cueCancel, slot.cueCloser = p, cancel, closer
	m.cue[idx].Store(p)
	p.Start(ctx)
}

// newPlayer creates a player for the clip in slot, with its own decoder.
// Caller holds m.mu.
func (m *Manager) newPlayer(idx int, slot *playerSlot, speed float64, loop, cued bool) (*Player, func(), error) {
	playerID := idx + 1
	// Anchor PTS to program timeline.
	var initialPTS int64
	if m.ptsProvider != nil {
		initialPTS = m.ptsProvider()
		if initialPTS > 0 {
			initialPTS += 3003 // one frame ahead at 30fps
		}
	}

	// Capture output callbacks once under lock — avoids per-frame mutex in hot path.
	rawVideoFn := m.rawVideoOutput
	stageFn := m.rawVideoStage
	videoFn := m.videoOutput
	audioFn := m.audioOutput
	pcmFn := m.pcmOutput
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
			return nil, nil, fmt.Errorf("create %s decoder: %w", clipCodec, err)
		}
		decodeFrame = dec.Decode
		decoderCloser = dec.Close
	} else if m.config.DecoderFactory != nil {
		var err error
		dec, err = m.config.DecoderFactory()
		if err != nil {
			return nil, nil, fmt.Errorf("create decoder: %w", err)
		}
		decodeFrame = dec.Decode
		decoderCloser = dec.Close
	}

	// Without an output rate a player plays at its clip's, which noteStart
	// times its start against too.
	outNum, outDen := clipRational(estimateFPSFromClipFrames(slot.frames))
	if m.config.OutputRate != nil {
		outNum, outDen = m.config.OutputRate()
	}

	var pcmOut func([]float32, int64, int, bool)
	if pcmFn != nil {
		pcmOut = func(pcm []float32, pts int64, channels int, start bool) {
			pcmFn(key, pcm, pts, channels, start)
		}
	}

	var p *Player
	p = NewPlayer(PlayerConfig{
		PCMOutput:      pcmOut,
		OutputFPSNum:   outNum,
		OutputFPSDen:   outDen,
		Clip:           slot.frames,
		AudioClip:      slot.audio,
		Speed:          speed,
		Loop:           loop,
		InitialPTS:     initialPTS,
		Timed:          m.config.Timed,
		Cued:           cued,
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
		StageVideo: stageVideo(key, stageFn),
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
		AudioRate: m.config.AudioRate,
		PCMCodec:  m.config.PCMCodec,
		OnStart: func(media string, delay time.Duration, preroll string) {
			m.noteStart(playerID, media, delay, preroll, outNum, outDen)
		},
		OnDone: func() {
			// Playback finished — clean up decoder (not needed for hold loop)
			// but keep player/cancel alive so Stop/Eject can cancel holdLoop.
			// Do NOT call onPlayerStop: the source stays registered during hold.
			m.mu.Lock()
			if s := m.players[idx]; s != nil && s.player == p {
				if s.decoderCloser != nil {
					s.decoderCloser()
					s.decoderCloser = nil
				}
			}
			m.mu.Unlock()
			m.notifyStateChange()
		},
	})
	return p, decoderCloser, nil
}

// Pause pauses playback for the given player slot.
func (m *Manager) Pause(playerID int) error {
	if err := validatePlayerID(playerID); err != nil {
		return err
	}

	idx := playerID - 1

	m.mu.Lock()
	m.promoteCueLocked(idx)
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
	m.promoteCueLocked(idx)
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
	m.promoteCueLocked(idx)
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

	if m.config.Timed {
		// The clip stays loaded, cued again; its source stays.
		m.mu.Lock()
		if m.players[idx] == slot {
			m.cueLocked(idx, slot)
		}
		m.mu.Unlock()
	} else if stopCb != nil {
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
	m.promoteCueLocked(idx)
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
	if slot.cue != nil {
		slot.cue.SetLoop(loop)
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
	m.promoteCueLocked(idx)
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

		m.promoteCueLocked(i)
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

// SetRawVideoStage registers what readies a cued player's held picture
// for the start on a program tick (PlayerConfig.StageVideo), with the
// player's source key. Without it the start puts the picture out through
// SetRawVideoOutput.
func (m *Manager) SetRawVideoStage(fn func(key string, yuv []byte, w, h int, isKeyframe bool) (put func(pts int64), drop func())) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rawVideoStage = fn
}

// stageVideo binds fn to key, nil without fn.
func stageVideo(key string, fn func(string, []byte, int, int, bool) (func(int64), func())) func([]byte, int, int, bool) (func(int64), func()) {
	if fn == nil {
		return nil
	}
	return func(yuv []byte, w, h int, isKeyframe bool) (func(int64), func()) {
		return fn(key, yuv, w, h, isKeyframe)
	}
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

// SetPCMOutput registers a callback for the PCM of timed clip players
// (PlayerConfig.PCMOutput), with the player source key. start marks the
// first PCM of a playback, whose source the mixer should place afresh.
func (m *Manager) SetPCMOutput(fn func(key string, pcm []float32, pts int64, channels int, start bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pcmOutput = fn
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
	var cues []func()
	for i := range m.players {
		if m.players[i] != nil {
			if stop := m.takeCueLocked(i); stop != nil {
				cues = append(cues, stop)
			}
		}
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

	for _, stop := range cues {
		stop()
	}
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
