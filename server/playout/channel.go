package playout

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// DecodedFrame is a pre-decoded YUV420 frame for the playout decode ring.
type DecodedFrame struct {
	YUV        []byte
	PTS        int64
	IsKeyframe bool
	Width      int
	Height     int

	// Optional audio data associated with this video frame's time position.
	AudioData       []byte // raw AAC payload or PCM samples
	AudioPTS        int64
	AudioSampleRate int
	AudioChannels   int
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
}

// Channel is a tick-driven frame router that registers as a switcher source
// ("playout:N"). It delegates playlist advancement to a Scheduler, idle content
// to an IdleLoop, and transitions to a Compositor. Each tick it asks the
// Scheduler what to play, obtains the frame, and emits it via callbacks.
type Channel struct {
	id     int
	config ChannelConfig

	mu         sync.Mutex
	state      ChannelState
	scheduler  *Scheduler
	idleLoop   *IdleLoop
	compositor *Compositor

	// Active clip playback.
	clipIter      FrameIterator
	clipItemID    string  // ID of item currently being decoded
	clipFPS       float64
	clipTotal     int
	clipFrame     int
	clipStartTime time.Time
	clipLastFrame *DecodedFrame // for hold mode

	// Live source tap — returns latest frame for a source key.
	gpuSourceTap    func(sourceKey string) *DecodedFrame
	lastLiveTapMiss string // for once-per-item logging of tap failures

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
	frameDuration time.Duration
	outputPTS     int64

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
	ySize := w * h
	uvSize := ySize / 4
	yuv := make([]byte, ySize+2*uvSize)
	for i := 0; i < ySize; i++ {
		yuv[i] = 16
	}
	for i := ySize; i < len(yuv); i++ {
		yuv[i] = 128
	}
	return &DecodedFrame{YUV: yuv, Width: w, Height: h, IsKeyframe: true}
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

	if c.state == StateOnAir {
		st.Mode = c.scheduler.Mode().String()
	}

	pl := c.schedulerPlaylist()
	if pl != nil {
		st.PlaylistName = pl.Name
		st.ItemCount = len(pl.Items)
		cursor := c.scheduler.Cursor()
		st.ItemIndex = cursor

		if cursor >= 0 && cursor < len(pl.Items) {
			item := pl.Items[cursor]
			st.CurrentItemID = item.ID
			st.CurrentTitle = item.Title
		}
		if cursor+1 < len(pl.Items) {
			next := pl.Items[cursor+1]
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

// schedulerPlaylist returns the scheduler's playlist. Must be called with c.mu held
// or during initialization. Returns nil if no playlist is loaded.
func (c *Channel) schedulerPlaylist() *Playlist {
	// The scheduler stores the playlist internally. We access it via Snapshot-like
	// methods. For now, we keep a reference ourselves too.
	return c.scheduler.playlist
}

// ---------------------------------------------------------------------------
// Playlist management
// ---------------------------------------------------------------------------

// LoadPlaylist validates and loads a playlist, transitioning the channel to Cued.
func (c *Channel) LoadPlaylist(pl *Playlist) error {
	if err := ValidatePlaylist(pl); err != nil {
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

// Start transitions from CUED to ON-AIR and launches the run goroutine.
func (c *Channel) Start() error {
	c.mu.Lock()
	pl := c.schedulerPlaylist()
	if pl == nil {
		c.mu.Unlock()
		return ErrNoPlaylist
	}
	if c.state == StateOnAir {
		c.mu.Unlock()
		return ErrAlreadyOnAir
	}
	if c.state != StateCued {
		c.mu.Unlock()
		return ErrInvalidState
	}

	c.state = StateOnAir
	c.scheduler.Start()

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})
	c.outputPTS = 0

	// Seed PTS from provider if available.
	if c.ptsProvider != nil {
		pts := c.ptsProvider()
		if pts > 0 {
			c.outputPTS = pts
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

// SetGPUSourceTap registers a function that returns the latest decoded frame
// for a live source key. Used for live playlist items.
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
// all scheduling decisions to the Scheduler.
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
		c.transitionState(StateCompleted)
		return true
	}

	var frame *DecodedFrame

	if decision.Transition != nil {
		outFrame := c.frameForItem(decision.Transition.Outgoing)
		inFrame := c.frameForItem(decision.Transition.Incoming)
		if outFrame != nil && inFrame != nil {
			frame = c.compositor.Blend(outFrame, inFrame, decision.Transition.Progress, decision.Transition.Config)
		} else if inFrame != nil {
			frame = inFrame
		} else if outFrame != nil {
			frame = outFrame
		}
	} else if decision.IsIdle {
		frame = c.idleLoop.NextFrame()
	} else if decision.Item != nil {
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

	c.secondaryItemID = item.ID
	c.lastLiveTapMiss = "" // reset so new item gets a fresh warning
	c.itemStartTime = now.UTC()
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(item.DurationMs)
	c.mu.Unlock()

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
		if item.Type == ItemSlate {
			durationMs = item.DurationMs
		} else if item.Type == ItemClip {
			// We'll estimate once the clip iter is open.
			c.mu.Lock()
			if c.clipTotal > 0 && c.clipFPS > 0 {
				durationMs = int64(float64(c.clipTotal) * 1000.0 / c.clipFPS)
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
			// Signal complete so scheduler advances.
			c.mu.Unlock()
			c.scheduler.SignalItemComplete()
			return nil
		}

		// Wrap with trim if in/out points set.
		var frameSource FrameIterator = iter
		if item.InPointMs > 0 || item.OutPointMs > 0 {
			frameSource = NewTrimIterator(iter, item.InPointMs, item.OutPointMs)
		}

		c.clipIter = frameSource
		c.clipFPS = frameSource.EstimateFPS()
		c.clipTotal = frameSource.TotalFrames()
	}

	iter := c.clipIter
	fps := c.clipFPS
	total := c.clipTotal
	frameIdx := c.clipFrame
	c.mu.Unlock()

	if iter == nil {
		return nil
	}

	frame, err := iter.Next()
	if err != nil {
		slog.Warn("playout: frame read error", "item", item.ID, "frame", frameIdx, "error", err)
		c.mu.Lock()
		c.clipFrame++
		c.mu.Unlock()

		// Count consecutive errors via a simple heuristic: if the frame
		// returned is nil with error, signal complete after a few failures.
		// For now, we treat any error as terminal after 3 consecutive.
		if asRunFn != nil {
			asRunFn(AsRunEvent{
				Timestamp: time.Now().UTC(),
				Type:      AsRunError,
				ChannelID: channelID,
				ItemID:    item.ID,
				Title:     item.Title,
				Message:   fmt.Sprintf("persistent frame error after %d frames: %v", frameIdx, err),
			})
		}
		c.scheduler.SignalItemComplete()
		// Return last good frame to avoid output gap.
		c.mu.Lock()
		lf := c.clipLastFrame
		c.mu.Unlock()
		if lf != nil {
			return lf
		}
		return c.slateFrame
	}

	if frame == nil {
		// End of clip.
		c.mu.Lock()
		c.itemProgress.Store(1000)
		c.itemRemainMs.Store(0)
		lastFrame := c.clipLastFrame
		c.mu.Unlock()

		// If hold mode, return the last frame instead of signaling complete.
		if item.Hold && lastFrame != nil {
			return lastFrame
		}

		// If loop mode, rewind — close the iterator so it re-opens on the
		// next tick with the same item ID forced to mismatch.
		if item.Loop {
			c.mu.Lock()
			if c.clipIter != nil {
				c.clipIter.Close()
			}
			c.clipIter = nil
			c.clipItemID = "" // force re-open on next tick
			c.clipFrame = 0
			c.clipLastFrame = nil
			c.mu.Unlock()
			// Return last frame to avoid gap during rewind.
			if lastFrame != nil {
				return lastFrame
			}
			return c.slateFrame
		}

		c.scheduler.SignalItemComplete()

		// Return the last frame to avoid a gap while the scheduler advances
		// and the next item opens on the following tick.
		if lastFrame != nil {
			return lastFrame
		}
		return c.slateFrame
	}

	// Update progress.
	c.mu.Lock()
	c.clipFrame++
	frameIdx = c.clipFrame
	c.clipLastFrame = frame
	c.mu.Unlock()

	if total > 0 {
		prog := int64(frameIdx * 1000 / total)
		c.itemProgress.Store(prog)
		remainFrames := total - frameIdx
		if remainFrames < 0 {
			remainFrames = 0
		}
		c.itemRemainMs.Store(int64(float64(remainFrames) * 1000.0 / fps))
	}

	return frame
}

// tapLiveSource returns the latest frame from a live source, or idle frame as fallback.
func (c *Channel) tapLiveSource(sourceKey string) *DecodedFrame {
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

// emitFrame sends the frame to output callbacks and increments PTS.
func (c *Channel) emitFrame(frame *DecodedFrame, outputFn func([]byte, int, int, int64, bool), audioFn func([]byte, int64, int, int), sc *SpliceCoordinator) {
	pts := c.outputPTS

	fps := c.config.FPS
	if fps == 0 {
		fps = 30.0
	}
	ptsPerFrame := int64(90000.0 / fps)
	if ptsPerFrame == 0 {
		ptsPerFrame = 3000
	}

	if outputFn != nil {
		outputFn(frame.YUV, frame.Width, frame.Height, pts, frame.IsKeyframe)
	}

	// Output audio if present.
	if len(frame.AudioData) > 0 && audioFn != nil {
		audioFn(frame.AudioData, frame.AudioPTS, frame.AudioSampleRate, frame.AudioChannels)
	}

	// Feed splice coordinator.
	if sc != nil {
		sc.IngestFrame(frame.YUV, frame.Width, frame.Height, pts)
	}

	c.outputPTS += ptsPerFrame
}

// transitionState changes the channel state and fires the callback.
func (c *Channel) transitionState(newState ChannelState) {
	c.mu.Lock()
	c.state = newState
	fn := c.onStateChange
	c.mu.Unlock()

	if fn != nil {
		fn(newState)
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

	// End secondary events.
	if c.secondaryScheduler != nil && c.secondaryItemID != "" {
		c.secondaryScheduler.EndItem()
		c.secondaryItemID = ""
	}
	c.mu.Unlock()
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
