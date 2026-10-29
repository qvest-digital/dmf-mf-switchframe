package playout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// AudioFrameData holds one audio frame (typically AAC) for playout.
type AudioFrameData struct {
	Data       []byte
	PTS        int64
	SampleRate int
	Channels   int
}

// DecodedFrame is a pre-decoded YUV420 frame for the playout decode ring.
type DecodedFrame struct {
	YUV        []byte
	PTS        int64
	IsKeyframe bool
	Width      int
	Height     int

	// Single audio frame (used by simple paths like idle loop).
	AudioData       []byte
	AudioPTS        int64
	AudioSampleRate int
	AudioChannels   int

	// Multiple audio frames for this video tick. When populated, emitFrame
	// sends ALL of these (required for correct A/V sync since AAC runs at
	// ~47fps vs video at 24-30fps). The single-frame fields above are ignored
	// when AudioFrames is non-empty.
	AudioFrames []AudioFrameData
}

// ChannelConfig configures a playout channel.
type ChannelConfig struct {
	// AssetProvider opens asset URIs and returns streaming frame iterators.
	AssetProvider AssetProvider

	// Width is the pipeline format width. Zero means use default (1920).
	Width int

	// Height is the pipeline format height. Zero means use default (1080).
	Height int

	// FPS is the pipeline frame rate (e.g., 29.97, 30.0, 59.94). Zero means use default (30.0).
	FPS float64

	// AllowedLocalPrefixes constrains which local-scheme asset URIs LoadPlaylist
	// will accept. Paths must resolve inside one of these prefixes; nil/empty
	// rejects every local-scheme URI (deny by default).
	AllowedLocalPrefixes []string
}

// Channel is a tick-driven frame router that registers as a switcher source
// ("playout:N"). It delegates playlist advancement to a Scheduler, idle content
// to an IdleLoop, and transitions to a Compositor. Each tick it asks the
// Scheduler what to play, obtains the frame, and emits it via callbacks.
type Channel struct {
	id     int
	config ChannelConfig

	// lifecycleMu serializes Start and Stop end to end, callbacks
	// included: Stop waits for the run goroutine outside mu, and a Start
	// interleaved there would run its onStart before Stop's onStop.
	lifecycleMu sync.Mutex

	mu         sync.Mutex
	state      ChannelState
	scheduler  *Scheduler
	idleLoop   *IdleLoop
	compositor *Compositor

	// Active clip playback.
	clipIter        FrameIterator
	clipItemID      string // ID of item currently being decoded
	clipFPS         float64
	clipTotal       int
	clipFrame       int // source frames consumed from iterator
	clipOutputFrame int // pipeline ticks since clip started (for FRC timestamp selection)
	clipStartTime   time.Time
	clipLastFrame   *DecodedFrame // for hold mode

	// Incoming clip playback during a transition. The primary clip slot above
	// continues iterating the outgoing item; this second slot pre-rolls the
	// incoming item frame-by-frame so a clip→clip transition blends two
	// advancing streams instead of thrashing a single iterator (open/close
	// twice per tick, both clips frozen at frame 0). When the transition
	// completes, this slot is promoted into the primary slot so playback
	// continues seamlessly rather than reopening from frame 0.
	inClipIter        FrameIterator
	inClipItemID      string
	inClipFPS         float64
	inClipTotal       int
	inClipFrame       int
	inClipOutputFrame int
	inClipLastFrame   *DecodedFrame

	// GPU-native live source aliasing — zero-copy frame routing in VRAM.
	// When set, live items alias the playout channel's GPU frame to the
	// source's GPU frame without any CPU involvement.
	gpuAlias func(liveSourceKey string) bool

	// CPU fallback: returns latest decoded frame for a source key.
	gpuSourceTap     func(sourceKey string) *DecodedFrame
	lastLiveTapMiss  string // for once-per-item logging of tap failures
	activeLiveSource string // current live source key (for audio routing)

	// Sentinel frame returned by tapLiveSource when GPU alias succeeded.
	// emitFrame skips CPU output for this frame (GPU pipeline already has it).
	gpuAliasedSentinel DecodedFrame

	// Live audio routing: called when entering/leaving a live item to redirect
	// the live source's audio to/from the playout mixer channel.
	onLiveAudio func(liveSourceKey string, active bool)

	// Slate frame (pre-allocated BT.709 black: Y=16, UV=128).
	slateFrame *DecodedFrame

	// Output callbacks (set before Start, read under lock then called without lock).
	rawVideoOutput func(yuv []byte, w, h int, pts int64, isKeyframe bool)
	audioOutput    func(data []byte, pts int64, sampleRate, channels int)
	onStateChange  func(ChannelState)
	onStart        func(key string)
	onStop         func(key string)
	ptsProvider    func() int64

	// Secondary event scheduling.
	secondaryHandler   SecondaryEventHandler
	secondaryScheduler *SecondaryEventScheduler
	secondaryItemID    string // ID of item whose secondary events are active

	// Splice coordinator for frame-accurate break entry.
	spliceCoordinator *SpliceCoordinator

	// As-run event logger callback.
	asRunLog      func(AsRunEvent)
	itemStartTime time.Time // when the current item began

	// Frame timing.
	frameDuration  time.Duration
	outputPTS      int64
	outputAudioPTS int64 // independent audio PTS counter (AAC frame cadence)

	// Sub-tick PTS remainders carried across frames so accumulated PTS stays
	// exact over long (24/7) broadcasts. The per-frame PTS step in the 90 kHz
	// timebase is fractional (e.g. 90000/29.97 = 3003.003, 1024*90000/44100 =
	// 2089.795); without carrying the dropped fraction, output PTS drifts from
	// true media time and video/audio drift at different rates. Each remainder
	// is kept in fixed-point units of 1/ptsFracScale of a tick, in [0, scale).
	outputPTSFrac      int64
	outputAudioPTSFrac int64

	// Run control.
	cancel context.CancelFunc
	done   chan struct{}

	// Atomic progress counters for lock-free reads.
	itemProgress atomic.Int64 // 0-1000 milliprogress
	itemRemainMs atomic.Int64
}

// NewChannel creates a new playout channel with the given ID and config.
// Dimensions and frame rate are taken from config; zero values fall back to
// sensible defaults (1920x1080 @ 30fps).
func NewChannel(id int, config ChannelConfig) *Channel {
	w, h := config.Width, config.Height
	if w == 0 {
		w = 1920
	}
	if h == 0 {
		h = 1080
	}
	fps := config.FPS
	if fps == 0 {
		fps = 30.0
	}

	return &Channel{
		id:            id,
		config:        config,
		state:         StateStopped,
		scheduler:     NewScheduler(),
		idleLoop:      NewIdleLoop("", config.AssetProvider, w, h),
		compositor:    NewCompositor(w, h, nil),
		slateFrame:    makeBlackFrame(w, h),
		frameDuration: time.Duration(float64(time.Second) / fps),
	}
}

// makeBlackFrame allocates a BT.709-legal black YUV420 frame (Y=16, UV=128).
func makeBlackFrame(w, h int) *DecodedFrame {
	return &DecodedFrame{
		YUV:        video.NewBlackFrame(video.YUV420_8bit, w, h),
		Width:      w,
		Height:     h,
		IsKeyframe: true,
	}
}

// Key returns the virtual source key for this channel (e.g., "playout:1").
func (c *Channel) Key() string {
	return fmt.Sprintf("playout:%d", c.id)
}

// State returns the current channel state.
func (c *Channel) State() ChannelState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Mode returns the current on-air mode.
func (c *Channel) Mode() OnAirMode {
	return c.scheduler.Mode()
}

// Status builds the broadcast state for this channel.
func (c *Channel) Status() ChannelStatus {
	c.mu.Lock()
	st := ChannelStatus{
		ID:    c.id,
		State: c.state.String(),
	}

	// Take a consistent snapshot of scheduler state under one lock.
	snap := c.scheduler.StatusSnapshot()

	st.SchedulerState = snap.State.String()

	if c.state == StateOnAir {
		st.Mode = snap.Mode.String()
	}

	pl := c.schedulerPlaylist()
	if pl != nil {
		st.PlaylistName = pl.Name
		st.ItemCount = len(pl.Items)
		st.ItemIndex = snap.Cursor

		if snap.Cursor >= 0 && snap.Cursor < len(pl.Items) {
			item := pl.Items[snap.Cursor]
			st.CurrentItemID = item.ID
			st.CurrentTitle = item.Title
			if c.state == StateOnAir && item.Loop && item.Type == ItemClip {
				st.Looping = true
				st.LoopExit = snap.LoopExit
			}
		}
		if snap.Cursor+1 < len(pl.Items) {
			next := pl.Items[snap.Cursor+1]
			st.NextItemID = next.ID
			st.NextTitle = next.Title
		}
	}
	c.mu.Unlock()

	st.ItemProgress = float64(c.itemProgress.Load()) / 1000.0
	st.ItemRemainMs = c.itemRemainMs.Load()
	return st
}

// Snapshot returns a PersistedChannel capturing the channel's current state.
func (c *Channel) Snapshot() PersistedChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := PersistedChannel{
		ID:     c.id,
		State:  c.state,
		Mode:   c.scheduler.Mode(),
		Cursor: c.scheduler.Cursor(),
	}
	pl := c.schedulerPlaylist()
	if pl != nil {
		plCopy := *pl
		plCopy.Items = make([]PlaylistItem, len(pl.Items))
		copy(plCopy.Items, pl.Items)
		snap.Playlist = &plCopy
	}
	return snap
}

// PlaylistItems returns a copy of the loaded playlist items, or nil if no playlist.
func (c *Channel) PlaylistItems() []PlaylistItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	pl := c.schedulerPlaylist()
	if pl == nil {
		return nil
	}
	items := make([]PlaylistItem, len(pl.Items))
	copy(items, pl.Items)
	return items
}

// PlaylistHash returns a deterministic SHA-256 hash of the playlist items,
// prefixed with "sha256:". Returns an empty string if no playlist is loaded.
func (c *Channel) PlaylistHash() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	pl := c.schedulerPlaylist()
	if pl == nil || len(pl.Items) == 0 {
		return ""
	}
	data, err := json.Marshal(pl.Items)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// ItemElapsedMs returns the number of milliseconds since the current item
// started playing. Returns 0 if the channel is not on-air or no item is active.
func (c *Channel) ItemElapsedMs() int64 {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	if state != StateOnAir {
		return 0
	}

	snap := c.scheduler.StatusSnapshot()
	if snap.ItemStartTime.IsZero() {
		return 0
	}
	return time.Since(snap.ItemStartTime).Milliseconds()
}

// schedulerPlaylist returns the scheduler's playlist. Returns nil if no
// playlist is loaded. Safe to call with or without c.mu held (the scheduler
// acquires its own lock internally).
func (c *Channel) schedulerPlaylist() *Playlist {
	return c.scheduler.Playlist()
}

// ---------------------------------------------------------------------------
// Playlist management
// ---------------------------------------------------------------------------

// LoadPlaylist validates and loads a playlist, transitioning the channel to Cued.
func (c *Channel) LoadPlaylist(pl *Playlist) error {
	if err := ValidatePlaylist(pl); err != nil {
		return err
	}
	if err := ValidateAssetURIs(pl, c.config.AllowedLocalPrefixes); err != nil {
		return err
	}

	c.mu.Lock()
	if c.state == StateOnAir {
		c.mu.Unlock()
		return ErrAlreadyOnAir
	}

	if err := c.scheduler.LoadPlaylist(pl); err != nil {
		c.mu.Unlock()
		return err
	}

	c.state = StateCued
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(0)

	fn := c.onStateChange
	c.mu.Unlock()

	if fn != nil {
		fn(StateCued)
	}
	return nil
}

// Start puts the loaded playlist on air from its first item and launches
// the run goroutine. A stopped or completed channel keeps its playlist, so
// it starts again the same way a cued one does.
func (c *Channel) Start() error {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	c.mu.Lock()
	// A run goroutine whose playlist completed may still be cleaning up; a
	// second one must not start until it has, or its cleanup closes the new
	// run's iterator.
	for c.state != StateOnAir && c.done != nil {
		prev := c.done
		c.mu.Unlock()
		<-prev
		c.mu.Lock()
		if c.done == prev {
			break
		}
	}
	pl := c.schedulerPlaylist()
	if pl == nil {
		c.mu.Unlock()
		return ErrNoPlaylist
	}
	if c.state == StateOnAir {
		c.mu.Unlock()
		return ErrAlreadyOnAir
	}

	// A run that completed on its own was never cancelled.
	if c.cancel != nil {
		c.cancel()
	}
	c.state = StateOnAir
	c.scheduler.Start()
	// The previous run's position no longer describes the item on air.
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(0)

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	c.outputPTS = 0
	c.outputAudioPTS = 0
	c.outputPTSFrac = 0
	c.outputAudioPTSFrac = 0

	// Seed PTS from provider if available.
	if c.ptsProvider != nil {
		pts := c.ptsProvider()
		if pts > 0 {
			c.outputPTS = pts
			c.outputAudioPTS = pts
		}
	}

	onStartFn := c.onStart
	stateChangeFn := c.onStateChange
	c.mu.Unlock()

	if onStartFn != nil {
		onStartFn(c.Key())
	}
	if stateChangeFn != nil {
		stateChangeFn(StateOnAir)
	}

	go c.run(ctx)
	return nil
}

// Stop cancels the run goroutine and transitions to STOPPED.
func (c *Channel) Stop() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()

	c.mu.Lock()
	if c.state == StateStopped {
		c.mu.Unlock()
		return
	}

	cancelFn := c.cancel
	doneCh := c.done
	c.cancel = nil
	c.state = StateStopped
	c.scheduler.Stop()

	onStopFn := c.onStop
	stateChangeFn := c.onStateChange
	c.mu.Unlock()

	if cancelFn != nil {
		cancelFn()
	}

	// Wait for run goroutine to finish.
	if doneCh != nil {
		<-doneCh
	}

	if onStopFn != nil {
		onStopFn(c.Key())
	}
	if stateChangeFn != nil {
		stateChangeFn(StateStopped)
	}
}

// Close stops the channel.
func (c *Channel) Close() {
	c.Stop()
}

// ---------------------------------------------------------------------------
// Navigation and mode control — delegate to Scheduler
// ---------------------------------------------------------------------------

// Next signals the scheduler to advance to the next item.
func (c *Channel) Next() error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	c.mu.Unlock()
	return c.scheduler.Next()
}

// Previous moves the cursor back one item.
func (c *Channel) Previous() error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	c.mu.Unlock()
	return c.scheduler.Previous()
}

// JumpTo finds the item with the given ID and moves the cursor to it.
func (c *Channel) JumpTo(itemID string) error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	c.mu.Unlock()
	return c.scheduler.JumpTo(itemID)
}

// EndLoop lets the looping clip on air finish its current pass, then end as
// a clip that does not loop.
func (c *Channel) EndLoop() error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	c.mu.Unlock()
	return c.scheduler.EndLoop()
}

// SeekToItem repositions the channel to a specific playlist item at a given
// elapsed offset. Used by snapshot-driven sync to align a standby engine's
// playout cursor with the active engine's state. For clip items, the existing
// iterator is closed and re-opened, then seeked to the target frame. For live
// and slate items, only the scheduler cursor is updated.
func (c *Channel) SeekToItem(itemIndex int, elapsedMs int64) error {
	c.mu.Lock()

	// Must be on-air (or at least cued with a playlist).
	pl := c.schedulerPlaylist()
	if pl == nil {
		c.mu.Unlock()
		return ErrNoPlaylist
	}
	if itemIndex < 0 || itemIndex >= len(pl.Items) {
		c.mu.Unlock()
		return ErrInvalidItem
	}

	item := pl.Items[itemIndex]
	mode := c.scheduler.Mode()
	elapsed := time.Duration(elapsedMs) * time.Millisecond

	// Update scheduler cursor with elapsed time.
	c.scheduler.SetCursor(itemIndex, mode, elapsed)

	// Close existing clip iterator since we're changing position.
	if c.clipIter != nil {
		c.clipIter.Close()
		c.clipIter = nil
	}
	c.clipItemID = ""
	c.clipFrame = 0
	c.clipOutputFrame = 0
	c.clipLastFrame = nil
	// Any in-flight transition pre-roll is invalidated by the seek.
	c.discardIncomingClipLocked()

	// For clip items, open a new iterator and seek to the target frame.
	if item.Type == ItemClip && c.config.AssetProvider != nil {
		iter, err := c.config.AssetProvider.Open(item.AssetURI)
		if err == nil {
			// Wrap with trim if in/out points set.
			frameSource := iter
			if item.InPointMs > 0 || item.OutPointMs > 0 {
				frameSource = NewTrimIterator(iter, item.InPointMs, item.OutPointMs)
			}

			c.clipIter = frameSource
			c.clipItemID = item.ID
			c.clipFPS = frameSource.EstimateFPS()
			c.clipTotal = frameSource.TotalFrames()
			c.clipStartTime = time.Now().Add(-elapsed)

			// Calculate target frame from elapsed time.
			fps := c.clipFPS
			if fps <= 0 {
				fps = 30.0
			}
			targetFrame := int(float64(elapsedMs) * fps / 1000.0)

			// Seek the iterator if it supports seeking. targetFrame is on the
			// item's playback timeline: for trimmed clips TrimIterator.Seek
			// translates it past the in-point on the full timeline.
			if targetFrame > 0 {
				if seeker, ok := frameSource.(interface{ Seek(int) error }); ok {
					if err := seeker.Seek(targetFrame); err != nil {
						slog.Warn("playout: SeekToItem seek failed, starting from beginning",
							"item", item.ID, "targetFrame", targetFrame, "error", err)
					} else {
						c.clipFrame = targetFrame
						c.clipOutputFrame = targetFrame
					}
				} else {
					slog.Warn("playout: SeekToItem iterator does not support seeking, starting from beginning",
						"item", item.ID, "targetFrame", targetFrame)
				}
			}
		}
	}

	// For live items, update the active live source.
	if item.Type == ItemLive && item.Source != "" {
		c.activeLiveSource = item.Source
	}

	// Update progress atomics. Clamp remain at zero — items without a
	// DurationMs (clips/live) would otherwise go negative.
	remainMs := item.DurationMs - elapsedMs
	if remainMs < 0 {
		remainMs = 0
	}
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(remainMs)
	c.secondaryItemID = item.ID
	c.itemStartTime = time.Now().Add(-elapsed)

	c.mu.Unlock()
	return nil
}

// TakeManual pauses playlist advancement.
func (c *Channel) TakeManual() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.scheduler.TakeManual()
	fn := c.onStateChange
	asRunFn := c.asRunLog
	channelID := c.id
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
	if asRunFn != nil {
		asRunFn(AsRunEvent{
			Timestamp: time.Now().UTC(),
			Type:      AsRunManualOverride,
			ChannelID: channelID,
			Operation: "enter_manual",
		})
	}
}

// ReturnToAuto resumes automated playlist advancement.
func (c *Channel) ReturnToAuto() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.scheduler.ReturnToAuto()
	fn := c.onStateChange
	asRunFn := c.asRunLog
	channelID := c.id
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
	if asRunFn != nil {
		asRunFn(AsRunEvent{
			Timestamp: time.Now().UTC(),
			Type:      AsRunManualOverride,
			ChannelID: channelID,
			Operation: "exit_manual",
		})
	}
}

// TakeBreak sets the channel mode to ModeBreak.
func (c *Channel) TakeBreak() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.scheduler.TakeBreak()
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
}

// ReturnFromBreak sets the channel mode back to ModeAutomated.
func (c *Channel) ReturnFromBreak() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.scheduler.ReturnFromBreak()
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
}

// ---------------------------------------------------------------------------
// Callback setters
// ---------------------------------------------------------------------------

// SetOnStateChange registers a callback invoked on state transitions.
func (c *Channel) SetOnStateChange(fn func(ChannelState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// SetOnStart registers a callback invoked when the channel starts.
func (c *Channel) SetOnStart(fn func(key string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStart = fn
}

// SetOnStop registers a callback invoked when the channel stops.
func (c *Channel) SetOnStop(fn func(key string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStop = fn
}

// SetRawVideoOutput registers the frame output callback.
func (c *Channel) SetRawVideoOutput(fn func(yuv []byte, w, h int, pts int64, isKeyframe bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rawVideoOutput = fn
}

// SetAudioOutput registers a callback for audio output from clip playback.
func (c *Channel) SetAudioOutput(fn func(data []byte, pts int64, sampleRate, channels int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.audioOutput = fn
}

// SetPTSProvider registers a function that returns the current program PTS.
func (c *Channel) SetPTSProvider(fn func() int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ptsProvider = fn
}

// SetSecondaryHandler registers a handler for secondary events (graphics, audio, etc.).
func (c *Channel) SetSecondaryHandler(h SecondaryEventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secondaryHandler = h
	if h != nil {
		c.secondaryScheduler = NewSecondaryEventScheduler(h)
		if c.asRunLog != nil {
			c.secondaryScheduler.SetAsRunLog(c.asRunLog)
		}
	} else {
		c.secondaryScheduler = nil
	}
}

// SetSpliceCoordinator registers a splice coordinator for frame-accurate break entry.
func (c *Channel) SetSpliceCoordinator(sc *SpliceCoordinator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spliceCoordinator = sc
}

// SetAsRunLog registers a callback for emitting as-run events.
func (c *Channel) SetAsRunLog(fn func(AsRunEvent)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asRunLog = fn
	if c.secondaryScheduler != nil {
		c.secondaryScheduler.SetAsRunLog(fn)
	}
}

// SetGPUAlias registers a function that aliases the playout channel's GPU
// frame to a live source's GPU frame. Zero-copy, GPU-native. Returns true
// if the alias was applied (source exists and has a frame).
func (c *Channel) SetGPUAlias(fn func(liveSourceKey string) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gpuAlias = fn
}

// SetOnLiveAudio registers a callback for live audio routing. Called when
// entering a live item (active=true) and leaving (active=false). The app
// layer uses this to forward the live source's audio to the playout mixer channel.
func (c *Channel) SetOnLiveAudio(fn func(liveSourceKey string, active bool)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onLiveAudio = fn
}

// SetGPUSourceTap registers a function that returns the latest decoded frame
// for a live source key. Used as CPU fallback for live playlist items.
func (c *Channel) SetGPUSourceTap(fn func(sourceKey string) *DecodedFrame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gpuSourceTap = fn
}

// SetCursor overrides the scheduler's cursor position. Used by the Manager
// during state recovery to restore the exact playlist position.
func (c *Channel) SetCursor(cursor int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scheduler.mu.Lock()
	c.scheduler.cursor = cursor
	c.scheduler.resetItemState()
	// Restoring a cursor must also clear any in-flight transition. If the
	// scheduler was mid-transition (SchedTransitioning), the next Tick would
	// route to tickTransition, compute progress from the stale transStartTime
	// (almost certainly >= 1.0), and do cursor++ — overwriting the position we
	// just restored and committing to the stale transIncoming item.
	c.scheduler.state = SchedPlaying
	c.scheduler.transOutgoing = nil
	c.scheduler.transIncoming = nil
	c.scheduler.mu.Unlock()
}

// SetIdleConfig configures the idle loop asset and provider.
func (c *Channel) SetIdleConfig(assetURI string, provider AssetProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.idleLoop.Close()
	w, h := c.configDimensions()
	c.idleLoop = NewIdleLoop(assetURI, provider, w, h)
}

// configDimensions returns the resolved width and height from the channel config.
// Must be called with c.mu held or during initialization.
func (c *Channel) configDimensions() (int, int) {
	w, h := c.config.Width, c.config.Height
	if w == 0 {
		w = 1920
	}
	if h == 0 {
		h = 1080
	}
	return w, h
}

// ---------------------------------------------------------------------------
// Tick-driven run loop
// ---------------------------------------------------------------------------

// run is the main playback goroutine. It ticks at frame rate and delegates
// all scheduling decisions to the Scheduler. The ticker always runs at the
// pipeline frame rate; clips with a different native FPS are handled by
// timestamp-based frame selection in nextClipFrame().
func (c *Channel) run(ctx context.Context) {
	defer close(c.done)

	ticker := time.NewTicker(c.frameDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.cleanup()
			return
		case <-ticker.C:
			// Always tick at pipeline rate. Frame rate conversion for
			// clips with a different native FPS is handled by timestamp-
			// based frame selection in nextClipFrame().
			if c.tick(time.Now()) {
				c.cleanup()
				return // playlist completed
			}
		}
	}
}

// tick processes one frame period. Returns true when the playlist is done.
func (c *Channel) tick(now time.Time) bool {
	c.mu.Lock()
	decision := c.scheduler.Tick(now)

	// Grab callbacks under lock.
	outputFn := c.rawVideoOutput
	audioFn := c.audioOutput
	sc := c.spliceCoordinator
	secScheduler := c.secondaryScheduler
	asRunFn := c.asRunLog
	channelID := c.id
	c.mu.Unlock()

	if decision.Completed {
		// Emit item_end for the last item if one was active.
		c.emitFinalItemEnd(secScheduler, asRunFn, channelID, now)
		c.completeIfOnAir()
		return true
	}

	var frame *DecodedFrame

	if decision.Transition != nil {
		outFrame := c.frameForItem(decision.Transition.Outgoing)
		inFrame := c.incomingFrameForItem(decision.Transition.Incoming)
		// GPU-aliased sentinels have nil YUV and cannot be blended.
		// Force hard cut when either frame is a sentinel.
		outIsAlias := outFrame == &c.gpuAliasedSentinel
		inIsAlias := inFrame == &c.gpuAliasedSentinel
		if outIsAlias || inIsAlias {
			// Hard cut to incoming (or outgoing if incoming unavailable).
			if inFrame != nil {
				frame = inFrame
			} else {
				frame = outFrame
			}
		} else if outFrame != nil && inFrame != nil {
			frame = c.compositor.Blend(outFrame, inFrame, decision.Transition.Progress, decision.Transition.Config)
			attachTransitionAudio(frame, outFrame, inFrame)
		} else if inFrame != nil {
			frame = inFrame
		} else if outFrame != nil {
			frame = outFrame
		}
	} else if decision.IsIdle {
		frame = c.idleLoop.NextFrame()
		c.discardIncomingClip()
	} else if decision.Item != nil {
		// A transition just completed: if the incoming slot was pre-rolling this
		// exact item, promote it into the primary slot so playback continues from
		// where the pre-roll left off instead of reopening from frame 0.
		c.promoteIncomingClip(decision.Item)
		// Track item changes for as-run logging and secondary events.
		c.handleItemChange(decision.Item, secScheduler, asRunFn, channelID, now)
		frame = c.frameForItem(decision.Item)
	}

	// Always emit a frame — never let the output go dark.
	// If the source returned nil, emit idle or slate as fallback.
	if frame == nil {
		frame = c.idleLoop.NextFrame()
	}
	if frame == nil {
		frame = c.slateFrame
	}
	if frame != nil {
		c.emitFrame(frame, outputFn, audioFn, sc)
	}

	return false
}

// attachTransitionAudio attaches audio to a blended transition frame.
// Compositor.Blend returns a video-only frame, but both source iterators keep
// consuming audio for every transition tick; without reattaching it here every
// configured dissolve/dip/wipe would silently drop ALL program audio for its
// duration (the consumed audio is permanently lost, not delayed). Encoded
// (AAC) audio frames cannot be cross-faded, so the incoming item's audio is
// used as soon as it is available — in the common case the outgoing clip has
// already played out fully when the transition starts, so its audio stream is
// finished anyway — falling back to the outgoing item's audio (e.g. a manual
// mid-clip Next into a slate keeps the tail audio under the fade).
func attachTransitionAudio(blended, out, in *DecodedFrame) {
	if blended == nil || blended == out || blended == in {
		// Hard-cut path (Compositor.Cut for unknown transition types) returns
		// one of the input frames, which already carries its own audio.
		return
	}
	src := in
	if len(in.AudioFrames) == 0 && len(in.AudioData) == 0 {
		src = out
	}
	blended.AudioFrames = src.AudioFrames
	blended.AudioData = src.AudioData
	blended.AudioPTS = src.AudioPTS
	blended.AudioSampleRate = src.AudioSampleRate
	blended.AudioChannels = src.AudioChannels
}

// handleItemChange detects when the scheduler moves to a new item and fires
// as-run events and secondary event scheduling.
func (c *Channel) handleItemChange(item *PlaylistItem, secScheduler *SecondaryEventScheduler, asRunFn func(AsRunEvent), channelID int, now time.Time) {
	c.mu.Lock()
	prevItemID := c.secondaryItemID
	c.mu.Unlock()

	if item.ID == prevItemID {
		// Same item — tick secondary events if applicable.
		c.tickSecondaryEvents(item, secScheduler, now)
		return
	}

	// New item — end the old one, start the new one.
	c.mu.Lock()

	// End previous item's secondary events.
	if secScheduler != nil && prevItemID != "" {
		secScheduler.EndItem()
	}

	// Emit item_end for previous item if there was one.
	if prevItemID != "" && asRunFn != nil && !c.itemStartTime.IsZero() {
		asRunFn(AsRunEvent{
			Timestamp:        now.UTC(),
			Type:             AsRunItemEnd,
			ChannelID:        channelID,
			ItemID:           prevItemID,
			Source:           "automated",
			ActualDurationMs: now.Sub(c.itemStartTime).Milliseconds(),
		})
	}

	// Close old clip iterator if switching items.
	if c.clipIter != nil && c.clipItemID != item.ID {
		c.clipIter.Close()
		c.clipIter = nil
		c.clipItemID = ""
		c.clipLastFrame = nil
	}

	// Track previous item for live audio routing.
	prevLiveSource := c.activeLiveSource
	c.secondaryItemID = item.ID
	c.lastLiveTapMiss = "" // reset so new item gets a fresh warning
	c.itemStartTime = now.UTC()
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(item.DurationMs)

	// Update live audio routing.
	liveAudioFn := c.onLiveAudio
	if item.Type == ItemLive && item.Source != "" {
		c.activeLiveSource = item.Source
	} else {
		c.activeLiveSource = ""
	}
	newLiveSource := c.activeLiveSource
	c.mu.Unlock()

	// Deactivate old live audio, activate new.
	if liveAudioFn != nil {
		if prevLiveSource != "" && prevLiveSource != newLiveSource {
			liveAudioFn(prevLiveSource, false)
		}
		if newLiveSource != "" && newLiveSource != prevLiveSource {
			liveAudioFn(newLiveSource, true)
		}
	}

	// Emit item_start.
	if asRunFn != nil {
		ev := AsRunEvent{
			Timestamp: now.UTC(),
			Type:      AsRunItemStart,
			ChannelID: channelID,
			ItemID:    item.ID,
			Title:     item.Title,
			Source:    "automated",
		}
		if item.ScheduledTime != nil {
			ev.ScheduledTS = item.ScheduledTime
			ev.DeltaMs = float64(now.UTC().Sub(*item.ScheduledTime).Microseconds()) / 1000.0
		}
		asRunFn(ev)
	}

	// Begin secondary events for new item.
	if secScheduler != nil && len(item.SecondaryEvents) > 0 {
		var durationMs int64
		switch item.Type {
		case ItemSlate:
			durationMs = item.DurationMs
		case ItemClip:
			// The clip iterator usually isn't open yet (frameForItem runs
			// AFTER handleItemChange), so clipTotal/clipFPS still describe the
			// PREVIOUS clip — only when a transition pre-roll was just
			// promoted do they belong to this item. Otherwise fall back to
			// the manifest duration (or the trim window); nextClipFrame
			// reanchors with the measured duration once the iterator opens.
			c.mu.Lock()
			if c.clipItemID == item.ID && c.clipTotal > 0 && c.clipFPS > 0 {
				durationMs = int64(float64(c.clipTotal) * 1000.0 / c.clipFPS)
			} else {
				durationMs = item.DurationMs
				if durationMs <= 0 && item.OutPointMs > item.InPointMs {
					durationMs = item.OutPointMs - item.InPointMs
				}
			}
			c.mu.Unlock()
		}
		secScheduler.BeginItem(item.SecondaryEvents, durationMs)
		// Fire t=0 events immediately.
		secScheduler.Tick(0)
	}
}

// tickSecondaryEvents fires secondary events based on elapsed time.
func (c *Channel) tickSecondaryEvents(item *PlaylistItem, secScheduler *SecondaryEventScheduler, now time.Time) {
	if secScheduler == nil || len(item.SecondaryEvents) == 0 {
		return
	}
	c.mu.Lock()
	elapsed := now.Sub(c.itemStartTime)
	c.mu.Unlock()
	secScheduler.Tick(elapsed.Milliseconds())
}

// emitFinalItemEnd emits item_end for the last active item when the playlist
// completes. This mirrors what handleItemChange does when transitioning between
// items, but covers the case where the scheduler signals Completed and there is
// no subsequent handleItemChange call.
func (c *Channel) emitFinalItemEnd(secScheduler *SecondaryEventScheduler, asRunFn func(AsRunEvent), channelID int, now time.Time) {
	c.mu.Lock()
	prevItemID := c.secondaryItemID
	if secScheduler != nil && prevItemID != "" {
		secScheduler.EndItem()
	}
	startTime := c.itemStartTime
	c.secondaryItemID = ""
	c.mu.Unlock()

	if prevItemID != "" && asRunFn != nil && !startTime.IsZero() {
		asRunFn(AsRunEvent{
			Timestamp:        now.UTC(),
			Type:             AsRunItemEnd,
			ChannelID:        channelID,
			ItemID:           prevItemID,
			Source:           "automated",
			ActualDurationMs: now.Sub(startTime).Milliseconds(),
		})
	}
}

// frameForItem returns the next frame for the given playlist item.
func (c *Channel) frameForItem(item *PlaylistItem) *DecodedFrame {
	switch item.Type {
	case ItemClip:
		return c.nextClipFrame(item)
	case ItemLive:
		return c.tapLiveSource(item.Source)
	case ItemSlate:
		return c.nextSlateFrame(item)
	}
	return nil
}

// nextClipFrame opens or continues a clip iterator and returns the next frame.
func (c *Channel) nextClipFrame(item *PlaylistItem) *DecodedFrame {
	var reanchorSched *SecondaryEventScheduler
	var reanchorMs int64

	c.mu.Lock()
	provider := c.config.AssetProvider
	asRunFn := c.asRunLog
	channelID := c.id

	// If this is a different clip than what we're currently iterating, open it.
	if c.clipItemID != item.ID {
		if c.clipIter != nil {
			c.clipIter.Close()
		}
		c.clipIter = nil
		c.clipItemID = item.ID
		c.clipFrame = 0
		c.clipOutputFrame = 0
		c.clipLastFrame = nil
		c.clipStartTime = time.Now()

		if provider == nil {
			slog.Warn("playout: no AssetProvider configured", "item", item.ID)
			c.mu.Unlock()
			return nil
		}

		iter, err := provider.Open(item.AssetURI)
		if err != nil {
			slog.Warn("playout: asset open failed", "item", item.ID, "error", err)
			if asRunFn != nil {
				asRunFn(AsRunEvent{
					Timestamp: time.Now().UTC(),
					Type:      AsRunError,
					ChannelID: channelID,
					ItemID:    item.ID,
					Title:     item.Title,
					Message:   fmt.Sprintf("asset open failed: %v", err),
				})
			}
			// Signal complete so scheduler advances. Done while holding c.mu
			// so a concurrent SeekToItem (which holds c.mu while it calls
			// scheduler.SetCursor) cannot interleave and have this stale
			// signal advance the cursor past the freshly-seeked item.
			c.scheduler.SignalItemComplete()
			c.mu.Unlock()
			return nil
		}

		// Wrap with trim if in/out points set.
		frameSource := iter
		if item.InPointMs > 0 || item.OutPointMs > 0 {
			frameSource = NewTrimIterator(iter, item.InPointMs, item.OutPointMs)
		}

		c.clipIter = frameSource
		c.clipFPS = frameSource.EstimateFPS()
		c.clipTotal = frameSource.TotalFrames()

		// Reanchor end-anchored secondary events with the clip's measured
		// duration. handleItemChange ran BEFORE this iterator was opened, so
		// BeginItem only had the manifest duration (possibly zero, or the
		// previous clip's stale values) — without this, "fire N ms before
		// clip end" events fire at the wrong time (or immediately at t=0).
		if c.secondaryScheduler != nil && c.secondaryItemID == item.ID &&
			c.clipTotal > 0 && c.clipFPS > 0 {
			reanchorSched = c.secondaryScheduler
			reanchorMs = int64(float64(c.clipTotal) * 1000.0 / c.clipFPS)
		}
	}

	iter := c.clipIter
	clipFPS := c.clipFPS
	total := c.clipTotal
	sourceFrame := c.clipFrame
	outputFrame := c.clipOutputFrame
	c.mu.Unlock()

	if reanchorSched != nil {
		// Safe here: Reanchor is only ever driven from the channel's run
		// goroutine, same as BeginItem/Tick (see SecondaryEventScheduler doc).
		reanchorSched.Reanchor(reanchorMs)
	}

	if iter == nil {
		return nil
	}

	// Determine which source frame should be displayed at this pipeline tick.
	// This is nearest-neighbor frame rate conversion:
	//   targetSourceFrame = outputFrame * clipFPS / pipelineFPS
	pipelineFPS := c.config.FPS
	if pipelineFPS == 0 {
		pipelineFPS = 30.0
	}
	if clipFPS == 0 {
		clipFPS = pipelineFPS
	}

	targetSourceFrame := int(float64(outputFrame) * clipFPS / pipelineFPS)

	// Always increment output frame counter — unless a concurrent SeekToItem
	// replaced the iterator since we captured it (the seek reset the counter).
	c.mu.Lock()
	if c.clipIter == iter {
		c.clipOutputFrame++
	}
	c.mu.Unlock()

	// If the target source frame hasn't advanced, hold the last frame.
	// Return a video-only copy — audio was already pushed on the first
	// delivery, so repeating it would double-deliver and desync A/V.
	if targetSourceFrame < sourceFrame && sourceFrame > 0 {
		c.mu.Lock()
		lf := c.clipLastFrame
		c.mu.Unlock()
		if lf != nil {
			if total > 0 {
				prog := int64(sourceFrame * 1000 / total)
				c.itemProgress.Store(prog)
				remainFrames := total - sourceFrame
				if remainFrames < 0 {
					remainFrames = 0
				}
				c.itemRemainMs.Store(int64(float64(remainFrames) * 1000.0 / clipFPS))
			}
			// Strip audio — it was already pushed when this frame was first emitted.
			return &DecodedFrame{
				YUV:        lf.YUV,
				Width:      lf.Width,
				Height:     lf.Height,
				PTS:        lf.PTS,
				IsKeyframe: lf.IsKeyframe,
			}
		}
	}

	// Advance to the target source frame. For same-rate this is always +1.
	// For fast clips (e.g. 60fps on 30fps pipeline) this skips frames.
	// Collect audio from ALL consumed frames (including skipped ones) so
	// no audio is lost during frame rate conversion.
	var frame *DecodedFrame
	var lastGoodFrame *DecodedFrame
	var err error
	var collectedAudio []AudioFrameData
	for sourceFrame <= targetSourceFrame {
		var next *DecodedFrame
		next, err = iter.Next()
		if err != nil {
			break
		}
		if next == nil {
			// End of clip — frame stays as last good frame from previous iteration.
			frame = nil
			break
		}
		frame = next
		// Collect audio from every consumed frame (including skipped ones).
		if len(frame.AudioFrames) > 0 {
			collectedAudio = append(collectedAudio, frame.AudioFrames...)
		} else if len(frame.AudioData) > 0 {
			collectedAudio = append(collectedAudio, AudioFrameData{
				Data:       frame.AudioData,
				SampleRate: frame.AudioSampleRate,
				Channels:   frame.AudioChannels,
			})
		}
		lastGoodFrame = frame
		sourceFrame++
	}

	// Publish the decode-loop results — but only if the iterator we used is
	// still current. A concurrent SeekToItem (snapshot-driven standby sync)
	// may have closed the captured iterator and installed a fresh, seeked one
	// mid-decode. In that case the (nil, nil) returned by the closed iterator
	// must NOT be treated as end-of-clip (the spurious SignalItemComplete
	// below would advance the scheduler PAST the item the seek just
	// positioned), and our stale frame counters must not clobber the
	// freshly-seeked position. Drop this tick's result; the next tick reads
	// the new iterator.
	c.mu.Lock()
	if c.clipIter != iter {
		c.mu.Unlock()
		return videoOnlyCopy(lastGoodFrame)
	}
	if lastGoodFrame != nil {
		c.clipFrame = sourceFrame
		c.clipLastFrame = lastGoodFrame
	}
	c.mu.Unlock()

	// Build the result frame. When we consumed multiple source frames
	// (skipping), attach all collected audio to a NEW DecodedFrame so we
	// don't mutate the iterator's backing data.
	//
	// If the iterator hit end-of-clip mid-skip (frame==nil but we consumed
	// frames earlier in the loop), use lastGoodFrame so the collected audio
	// is still delivered.
	resultFrame := frame
	if resultFrame == nil && lastGoodFrame != nil {
		resultFrame = lastGoodFrame
	}
	if resultFrame != nil && len(collectedAudio) > 0 {
		resultFrame = &DecodedFrame{
			YUV:         resultFrame.YUV,
			PTS:         resultFrame.PTS,
			IsKeyframe:  resultFrame.IsKeyframe,
			Width:       resultFrame.Width,
			Height:      resultFrame.Height,
			AudioFrames: collectedAudio,
		}
	}

	if err != nil {
		slog.Warn("playout: frame read error", "item", item.ID, "frame", sourceFrame, "error", err)

		// Any frame read error is treated as terminal — signal complete so
		// the scheduler advances to the next item.
		if asRunFn != nil {
			asRunFn(AsRunEvent{
				Timestamp: time.Now().UTC(),
				Type:      AsRunError,
				ChannelID: channelID,
				ItemID:    item.ID,
				Title:     item.Title,
				Message:   fmt.Sprintf("persistent frame error after %d frames: %v", sourceFrame, err),
			})
		}
		// Signal complete only if our iterator is still current, under c.mu so
		// a concurrent SeekToItem cannot interleave between check and signal.
		c.mu.Lock()
		if c.clipIter == iter {
			c.scheduler.SignalItemComplete()
		}
		lf := c.clipLastFrame
		c.mu.Unlock()
		// Return last good frame (video only) to avoid output gap.
		// Audio was already delivered when this frame was first emitted.
		if lf != nil {
			return &DecodedFrame{
				YUV:        lf.YUV,
				Width:      lf.Width,
				Height:     lf.Height,
				PTS:        lf.PTS,
				IsKeyframe: lf.IsKeyframe,
			}
		}
		return c.slateFrame
	}

	if frame == nil {
		// End of clip — or hit end-of-clip mid-skip during FRC.
		// If we consumed frames before hitting nil, resultFrame will be non-nil
		// with collected audio attached. Return it now and let the next tick
		// handle the end-of-clip state transition (hold/loop/advance).
		if resultFrame != nil {
			return resultFrame
		}

		c.mu.Lock()
		c.itemProgress.Store(1000)
		c.itemRemainMs.Store(0)
		lastFrame := c.clipLastFrame
		c.mu.Unlock()

		// If hold mode, return video-only copy — audio was already delivered
		// when this frame was first emitted. Repeating it would leak audio
		// on every hold tick.
		if item.Hold && lastFrame != nil {
			return &DecodedFrame{
				YUV:        lastFrame.YUV,
				Width:      lastFrame.Width,
				Height:     lastFrame.Height,
				PTS:        lastFrame.PTS,
				IsKeyframe: lastFrame.IsKeyframe,
			}
		}

		// If loop mode, rewind — close the iterator so it re-opens on the
		// next tick with the same item ID forced to mismatch. Guarded on
		// iterator identity so a concurrent SeekToItem's fresh iterator is
		// not torn down by the old clip's end-of-loop. After EndLoop the pass
		// that just finished was the last one.
		if item.Loop && !c.scheduler.LoopExitPending() {
			c.mu.Lock()
			if c.clipIter == iter {
				if c.clipIter != nil {
					c.clipIter.Close()
				}
				c.clipIter = nil
				c.clipItemID = "" // force re-open on next tick
				c.clipFrame = 0
				c.clipOutputFrame = 0
				c.clipLastFrame = nil
			}
			c.mu.Unlock()
			// Return video-only copy to avoid gap during rewind.
			// Audio was already delivered on the frame's first emission.
			if lastFrame != nil {
				return &DecodedFrame{
					YUV:        lastFrame.YUV,
					Width:      lastFrame.Width,
					Height:     lastFrame.Height,
					PTS:        lastFrame.PTS,
					IsKeyframe: lastFrame.IsKeyframe,
				}
			}
			return c.slateFrame
		}

		// Signal complete only if our iterator is still current, under c.mu so
		// a concurrent SeekToItem cannot interleave between check and signal.
		c.mu.Lock()
		if c.clipIter == iter {
			c.scheduler.SignalItemComplete()
		}
		c.mu.Unlock()

		// Return video-only copy to avoid a gap while the scheduler advances
		// and the next item opens on the following tick. Audio was already
		// delivered on the frame's first emission.
		if lastFrame != nil {
			return &DecodedFrame{
				YUV:        lastFrame.YUV,
				Width:      lastFrame.Width,
				Height:     lastFrame.Height,
				PTS:        lastFrame.PTS,
				IsKeyframe: lastFrame.IsKeyframe,
			}
		}
		return c.slateFrame
	}

	// Update progress.
	if total > 0 {
		prog := int64(sourceFrame * 1000 / total)
		c.itemProgress.Store(prog)
		remainFrames := total - sourceFrame
		if remainFrames < 0 {
			remainFrames = 0
		}
		c.itemRemainMs.Store(int64(float64(remainFrames) * 1000.0 / clipFPS))
	}

	return resultFrame
}

// incomingFrameForItem returns the next frame for the incoming side of a
// transition. Clip items get their own pre-roll iterator (the inClip* slot) so
// the outgoing clip's iterator is not torn down every tick; non-clip items
// (live/slate) carry no per-frame iterator state and route through the normal
// path. Any leftover incoming clip state is discarded when the incoming side is
// not a clip so a later clip→clip transition starts clean.
func (c *Channel) incomingFrameForItem(item *PlaylistItem) *DecodedFrame {
	if item != nil && item.Type == ItemClip {
		return c.nextIncomingClipFrame(item)
	}
	c.discardIncomingClip()
	return c.frameForItem(item)
}

// nextIncomingClipFrame pre-rolls the incoming clip of a transition using a
// dedicated iterator slot (inClip*), independent of the primary (outgoing) clip
// slot. It mirrors nextClipFrame's nearest-neighbor frame-rate conversion but
// is intentionally simpler: it never signals item-complete or applies
// loop/hold (those belong to the current item once it is promoted to the
// primary slot). On end-of-clip it holds the last decoded frame so the blend
// stays smooth. The iterator is opened once per item and advanced once per
// tick — not opened/closed every tick.
func (c *Channel) nextIncomingClipFrame(item *PlaylistItem) *DecodedFrame {
	c.mu.Lock()
	provider := c.config.AssetProvider

	// Open the incoming iterator once, the first time we see this item.
	if c.inClipItemID != item.ID {
		if c.inClipIter != nil {
			c.inClipIter.Close()
		}
		c.inClipIter = nil
		c.inClipItemID = item.ID
		c.inClipFrame = 0
		c.inClipOutputFrame = 0
		c.inClipLastFrame = nil

		if provider == nil {
			c.mu.Unlock()
			return nil
		}

		iter, err := provider.Open(item.AssetURI)
		if err != nil {
			slog.Warn("playout: incoming asset open failed", "item", item.ID, "error", err)
			c.mu.Unlock()
			return nil
		}

		frameSource := iter
		if item.InPointMs > 0 || item.OutPointMs > 0 {
			frameSource = NewTrimIterator(iter, item.InPointMs, item.OutPointMs)
		}

		c.inClipIter = frameSource
		c.inClipFPS = frameSource.EstimateFPS()
		c.inClipTotal = frameSource.TotalFrames()
	}

	iter := c.inClipIter
	clipFPS := c.inClipFPS
	sourceFrame := c.inClipFrame
	outputFrame := c.inClipOutputFrame
	c.inClipOutputFrame++
	c.mu.Unlock()

	if iter == nil {
		return nil
	}

	pipelineFPS := c.config.FPS
	if pipelineFPS == 0 {
		pipelineFPS = 30.0
	}
	if clipFPS == 0 {
		clipFPS = pipelineFPS
	}

	targetSourceFrame := int(float64(outputFrame) * clipFPS / pipelineFPS)

	// Hold the last frame if the target source frame hasn't advanced.
	// Return a video-only copy — the frame's audio was already delivered on
	// its first emission (transition blends carry the incoming audio), so
	// repeating it would double-deliver and desync A/V.
	if targetSourceFrame < sourceFrame && sourceFrame > 0 {
		c.mu.Lock()
		lf := c.inClipLastFrame
		c.mu.Unlock()
		return videoOnlyCopy(lf)
	}

	// Advance to the target source frame (+1 for same-rate playback).
	// Collect audio from ALL consumed frames (including skipped ones) so no
	// audio is lost during frame rate conversion, mirroring nextClipFrame.
	var frame *DecodedFrame
	var collectedAudio []AudioFrameData
	for sourceFrame <= targetSourceFrame {
		next, err := iter.Next()
		if err != nil {
			break
		}
		if next == nil {
			break
		}
		frame = next
		if len(frame.AudioFrames) > 0 {
			collectedAudio = append(collectedAudio, frame.AudioFrames...)
		} else if len(frame.AudioData) > 0 {
			collectedAudio = append(collectedAudio, AudioFrameData{
				Data:       frame.AudioData,
				SampleRate: frame.AudioSampleRate,
				Channels:   frame.AudioChannels,
			})
		}
		sourceFrame++
		c.mu.Lock()
		c.inClipFrame = sourceFrame
		c.inClipLastFrame = frame
		c.mu.Unlock()
	}

	if frame == nil {
		// End of clip during pre-roll (or no new frame this tick) — hold last.
		// Video-only: its audio was already delivered on first emission.
		c.mu.Lock()
		lf := c.inClipLastFrame
		c.mu.Unlock()
		return videoOnlyCopy(lf)
	}

	if len(collectedAudio) > 0 {
		// Attach all collected audio to a NEW DecodedFrame so we don't mutate
		// the iterator's backing data.
		return &DecodedFrame{
			YUV:         frame.YUV,
			PTS:         frame.PTS,
			IsKeyframe:  frame.IsKeyframe,
			Width:       frame.Width,
			Height:      frame.Height,
			AudioFrames: collectedAudio,
		}
	}

	return frame
}

// videoOnlyCopy returns a copy of frame with all audio fields stripped, for
// re-emitting a held frame whose audio was already delivered. Returns nil for
// a nil input.
func videoOnlyCopy(frame *DecodedFrame) *DecodedFrame {
	if frame == nil {
		return nil
	}
	return &DecodedFrame{
		YUV:        frame.YUV,
		Width:      frame.Width,
		Height:     frame.Height,
		PTS:        frame.PTS,
		IsKeyframe: frame.IsKeyframe,
	}
}

// promoteIncomingClip moves the incoming pre-roll slot into the primary clip
// slot when a transition completes and the scheduler commits to item. This lets
// nextClipFrame continue the now-current clip from where the pre-roll left off
// instead of closing it and reopening from frame 0. If the incoming slot was
// not pre-rolling this item (e.g. the incoming side was live/slate), the
// incoming slot is simply discarded.
func (c *Channel) promoteIncomingClip(item *PlaylistItem) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item == nil || c.inClipItemID != item.ID || c.inClipIter == nil {
		c.discardIncomingClipLocked()
		return
	}

	// Close whatever the primary slot held (the outgoing clip) and adopt the
	// pre-rolled incoming iterator and its progress.
	if c.clipIter != nil && c.clipIter != c.inClipIter {
		c.clipIter.Close()
	}
	c.clipIter = c.inClipIter
	c.clipItemID = c.inClipItemID
	c.clipFPS = c.inClipFPS
	c.clipTotal = c.inClipTotal
	c.clipFrame = c.inClipFrame
	c.clipOutputFrame = c.inClipOutputFrame
	c.clipLastFrame = c.inClipLastFrame
	c.clipStartTime = time.Now()

	c.clearIncomingClipStateLocked()
}

// discardIncomingClip closes and clears the incoming pre-roll slot. Safe to
// call when no transition is active.
func (c *Channel) discardIncomingClip() {
	c.mu.Lock()
	c.discardIncomingClipLocked()
	c.mu.Unlock()
}

// discardIncomingClipLocked closes the incoming iterator and clears its state.
// Caller must hold c.mu.
func (c *Channel) discardIncomingClipLocked() {
	if c.inClipIter != nil {
		c.inClipIter.Close()
	}
	c.clearIncomingClipStateLocked()
}

// clearIncomingClipStateLocked resets the incoming slot fields WITHOUT closing
// the iterator (used after the iterator has been adopted by the primary slot).
// Caller must hold c.mu.
func (c *Channel) clearIncomingClipStateLocked() {
	c.inClipIter = nil
	c.inClipItemID = ""
	c.inClipFPS = 0
	c.inClipTotal = 0
	c.inClipFrame = 0
	c.inClipOutputFrame = 0
	c.inClipLastFrame = nil
}

// tapLiveSource routes live frames through the playout channel.
// GPU alias swaps the VRAM frame pointer (zero-copy), then returns a
// signal frame (empty YUV) that triggers the switcher pipeline to process
// the already-cached GPU frame via RunFromCache.
func (c *Channel) tapLiveSource(sourceKey string) *DecodedFrame {
	// GPU alias — zero-copy VRAM pointer swap.
	c.mu.Lock()
	alias := c.gpuAlias
	c.mu.Unlock()

	if alias != nil && alias(sourceKey) {
		// Return a signal frame with empty YUV. The switcher's handleRawVideoFrame
		// will call IngestYUV (which no-ops on empty YUV), then route to the
		// GPU pipeline which calls RunFromCache — reading the aliased VRAM frame.
		return &c.gpuAliasedSentinel
	}

	// CPU fallback — only used when GPU is not available.
	c.mu.Lock()
	tap := c.gpuSourceTap
	c.mu.Unlock()

	if tap != nil {
		frame := tap(sourceKey)
		if frame != nil {
			return frame
		}
	}

	// Log once when the tap fails so operators know the source is misconfigured.
	c.logLiveTapMissOnce(sourceKey)

	// Fallback: return idle frame.
	return c.idleLoop.NextFrame()
}

// logLiveTapMissOnce logs a warning the first time a live source tap fails per item.
// Prevents log spam (one warning per source per item change).
func (c *Channel) logLiveTapMissOnce(sourceKey string) {
	c.mu.Lock()
	if c.lastLiveTapMiss == sourceKey {
		c.mu.Unlock()
		return
	}
	c.lastLiveTapMiss = sourceKey
	c.mu.Unlock()
	slog.Warn("playout: live source not available, showing idle/slate fallback",
		"source", sourceKey, "channel", c.id,
		"hint", "check that the source exists and is connected")
}

// nextSlateFrame returns the pre-allocated black frame for slate items.
// The scheduler handles slate duration timing.
func (c *Channel) nextSlateFrame(item *PlaylistItem) *DecodedFrame {
	// Update progress based on elapsed time for slates with duration.
	if item.DurationMs > 0 {
		c.mu.Lock()
		elapsed := time.Since(c.itemStartTime)
		c.mu.Unlock()

		prog := int64(elapsed.Milliseconds() * 1000 / item.DurationMs)
		if prog > 1000 {
			prog = 1000
		}
		c.itemProgress.Store(prog)
		remain := item.DurationMs - elapsed.Milliseconds()
		if remain < 0 {
			remain = 0
		}
		c.itemRemainMs.Store(remain)
	}

	c.mu.Lock()
	sf := c.slateFrame
	c.mu.Unlock()

	// Return a copy with updated PTS.
	return &DecodedFrame{
		YUV:        sf.YUV,
		Width:      sf.Width,
		Height:     sf.Height,
		PTS:        c.outputPTS,
		IsKeyframe: true,
	}
}

// ptsFracScale is the fixed-point denominator used to carry sub-tick PTS
// remainders across frames. A power of two keeps the carry arithmetic exact;
// 2^30 gives a per-frame rounding bias below 1e-9 ticks, so accumulated PTS
// stays within a small fraction of a tick of true media time even over a 24/7
// broadcast.
const ptsFracScale = 1 << 30

// ptsStep splits the exact per-frame PTS increment incTicks (in the 90 kHz
// timebase) into an integer tick count and a fixed-point fractional remainder
// in units of 1/ptsFracScale. Callers accumulate the fraction with advancePTS
// so the dropped remainder is never lost.
func ptsStep(incTicks float64) (stepInt, stepFrac int64) {
	if incTicks <= 0 {
		return 0, 0
	}
	stepInt = int64(incTicks)
	stepFrac = int64(math.Round((incTicks - float64(stepInt)) * ptsFracScale))
	// Rounding may push the fraction up to a whole tick; normalize.
	if stepFrac >= ptsFracScale {
		stepInt += stepFrac / ptsFracScale
		stepFrac %= ptsFracScale
	}
	return stepInt, stepFrac
}

// advancePTS adds one fractional step to a PTS accumulator, carrying whole
// ticks out of the fixed-point remainder. pts is the integer 90 kHz PTS and
// frac is the carried remainder in [0, ptsFracScale).
func advancePTS(pts, frac, stepInt, stepFrac int64) (newPTS, newFrac int64) {
	pts += stepInt
	frac += stepFrac
	if frac >= ptsFracScale {
		pts++
		frac -= ptsFracScale
	}
	return pts, frac
}

// emitFrame sends the frame to output callbacks and increments PTS.
// When frame is the GPU-aliased sentinel, the GPU pipeline already has the
// correct frame — skip CPU video output and splice coordinator (both need
// CPU YUV data we don't have). Audio from live sources is handled by the
// mixer's direct source path, not the playout channel.
func (c *Channel) emitFrame(frame *DecodedFrame, outputFn func([]byte, int, int, int64, bool), audioFn func([]byte, int64, int, int), sc *SpliceCoordinator) {
	fps := c.config.FPS
	if fps == 0 {
		fps = 30.0
	}
	// Carry the dropped sub-tick fraction of 90000/fps so PTS stays exact over
	// long broadcasts instead of drifting by ~0.5 tick/frame.
	vStepInt, vStepFrac := ptsStep(90000.0 / fps)
	if vStepInt == 0 && vStepFrac == 0 {
		vStepInt = 3000
	}

	pts := c.outputPTS

	if frame == &c.gpuAliasedSentinel {
		// GPU-aliased signal frame: emit with nil YUV to trigger the switcher
		// pipeline's RunFromCache path. IngestYUV no-ops on nil, then the GPU
		// pipeline reads the already-aliased VRAM frame.
		if outputFn != nil {
			outputFn(nil, c.config.Width, c.config.Height, pts, true)
		}
		c.outputPTS, c.outputPTSFrac = advancePTS(c.outputPTS, c.outputPTSFrac, vStepInt, vStepFrac)
		return
	}

	if outputFn != nil {
		outputFn(frame.YUV, frame.Width, frame.Height, pts, frame.IsKeyframe)
	}

	// Push audio to mixer. Audio frames are attached to each DecodedFrame
	// by the FileFrameIterator. During FRC, nextClipFrame collects audio
	// from all consumed source frames (including skipped ones) and strips
	// audio from held frames, ensuring correct delivery regardless of
	// clip-vs-pipeline rate mismatch.
	if audioFn != nil {
		if len(frame.AudioFrames) > 0 {
			// The FileFrameIterator attaches audio proportionally to encoded video
			// frames. But we may be ticking slower than the clip's native rate,
			// so we push ALL attached frames immediately — the mixer ring buffer
			// absorbs the burst.
			for i := range frame.AudioFrames {
				af := &frame.AudioFrames[i]
				audioFn(af.Data, c.outputAudioPTS, af.SampleRate, af.Channels)
				if af.SampleRate > 0 {
					aStepInt, aStepFrac := ptsStep(1024.0 * 90000.0 / float64(af.SampleRate))
					c.outputAudioPTS, c.outputAudioPTSFrac = advancePTS(c.outputAudioPTS, c.outputAudioPTSFrac, aStepInt, aStepFrac)
				}
			}
		} else if len(frame.AudioData) > 0 {
			audioFn(frame.AudioData, c.outputAudioPTS, frame.AudioSampleRate, frame.AudioChannels)
			if frame.AudioSampleRate > 0 {
				aStepInt, aStepFrac := ptsStep(1024.0 * 90000.0 / float64(frame.AudioSampleRate))
				c.outputAudioPTS, c.outputAudioPTSFrac = advancePTS(c.outputAudioPTS, c.outputAudioPTSFrac, aStepInt, aStepFrac)
			}
		}
	}

	// Feed splice coordinator.
	if sc != nil {
		sc.IngestFrame(frame.YUV, frame.Width, frame.Height, pts)
	}

	c.outputPTS, c.outputPTSFrac = advancePTS(c.outputPTS, c.outputPTSFrac, vStepInt, vStepFrac)
}

// completeIfOnAir moves an on-air channel whose playlist ran out to
// COMPLETED and fires the callback. Stop() also ends the playlist, through
// the scheduler, so a tick racing it sees the playlist complete; the channel
// is STOPPED by then and stays so.
func (c *Channel) completeIfOnAir() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.state = StateCompleted
	fn := c.onStateChange
	c.mu.Unlock()

	if fn != nil {
		fn(StateCompleted)
	}
}

// cleanup closes the active clip iterator and idle loop.
func (c *Channel) cleanup() {
	c.mu.Lock()
	if c.clipIter != nil {
		c.clipIter.Close()
		c.clipIter = nil
		c.clipItemID = ""
	}
	c.discardIncomingClipLocked()

	// End secondary events.
	if c.secondaryScheduler != nil && c.secondaryItemID != "" {
		c.secondaryScheduler.EndItem()
		c.secondaryItemID = ""
	}

	// Clean up live audio forwarding if a live item was active.
	liveAudioFn := c.onLiveAudio
	liveSource := c.activeLiveSource
	c.activeLiveSource = ""
	c.mu.Unlock()

	if liveAudioFn != nil && liveSource != "" {
		liveAudioFn(liveSource, false)
	}
}

// estimateFPS estimates frames per second from PTS values.
func estimateFPS(frames []DecodedFrame) float64 {
	if len(frames) < 2 {
		return 30.0
	}

	minPTS, maxPTS := frames[0].PTS, frames[0].PTS
	for _, f := range frames[1:] {
		if f.PTS < minPTS {
			minPTS = f.PTS
		}
		if f.PTS > maxPTS {
			maxPTS = f.PTS
		}
	}

	ptsSpan := maxPTS - minPTS
	if ptsSpan <= 0 {
		return 30.0
	}

	fps := float64(len(frames)-1) * 90000.0 / float64(ptsSpan)
	if fps < 10 {
		fps = 10
	}
	if fps > 120 {
		fps = 120
	}
	return fps
}
