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
}

// ChannelConfig configures a playout channel.
type ChannelConfig struct {
	// AssetProvider opens asset URIs and returns streaming frame iterators.
	AssetProvider AssetProvider
}

// Channel is a gapless sequential clip player that registers as a switcher
// source ("playout:N"). It plays through a Playlist sequentially, outputting
// raw YUV frames at the correct frame rate via callbacks.
type Channel struct {
	id     int
	config ChannelConfig

	mu       sync.Mutex
	state    ChannelState
	mode     OnAirMode
	playlist *Playlist
	cursor   int // current item index

	cancel    context.CancelFunc
	done      chan struct{}
	advanceCh chan struct{} // buffered 1 — signaled by Next/ReturnToAuto

	// Callbacks (set before Start, read under lock then called without lock).
	onStateChange  func(ChannelState)
	onStart        func(key string)
	onStop         func(key string)
	rawVideoOutput func(yuv []byte, w, h int, pts int64, isKeyframe bool)
	ptsProvider    func() int64

	// Secondary event scheduling.
	secondaryHandler   SecondaryEventHandler
	secondaryScheduler *SecondaryEventScheduler

	// Splice coordinator for frame-accurate break entry.
	spliceCoordinator *SpliceCoordinator

	// As-run event logger callback.
	asRunLog func(AsRunEvent)

	// Atomic progress counters for lock-free reads.
	itemProgress atomic.Int64 // 0-1000 milliprogress
	itemRemainMs atomic.Int64
}

// NewChannel creates a new playout channel with the given ID and config.
func NewChannel(id int, config ChannelConfig) *Channel {
	return &Channel{
		id:        id,
		config:    config,
		state:     StateStopped,
		mode:      ModeAutomated,
		advanceCh: make(chan struct{}, 1),
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// Status builds the broadcast state for this channel.
func (c *Channel) Status() ChannelStatus {
	c.mu.Lock()
	st := ChannelStatus{
		ID:    c.id,
		State: c.state.String(),
	}

	if c.state == StateOnAir {
		st.Mode = c.mode.String()
	}

	if c.playlist != nil {
		st.PlaylistName = c.playlist.Name
		st.ItemCount = len(c.playlist.Items)
		st.ItemIndex = c.cursor

		if c.cursor >= 0 && c.cursor < len(c.playlist.Items) {
			item := c.playlist.Items[c.cursor]
			st.CurrentItemID = item.ID
			st.CurrentTitle = item.Title
		}
		if c.cursor+1 < len(c.playlist.Items) {
			next := c.playlist.Items[c.cursor+1]
			st.NextItemID = next.ID
			st.NextTitle = next.Title
		}
	}
	c.mu.Unlock()

	st.ItemProgress = float64(c.itemProgress.Load()) / 1000.0
	st.ItemRemainMs = c.itemRemainMs.Load()
	return st
}

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

	c.playlist = pl
	c.cursor = 0
	c.state = StateCued
	c.mode = ModeAutomated
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
	if c.playlist == nil {
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
	c.mode = ModeAutomated

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.done = make(chan struct{})

	// Drain any stale advance signals.
	select {
	case <-c.advanceCh:
	default:
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
	c.mode = ModeAutomated

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

// Next signals the run goroutine to advance to the next item.
func (c *Channel) Next() error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	c.mu.Unlock()

	// Non-blocking send (buffered 1).
	select {
	case c.advanceCh <- struct{}{}:
	default:
	}
	return nil
}

// Previous decrements the cursor and signals advance.
func (c *Channel) Previous() error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	// Move back 2 positions: the run loop will increment to cursor when it
	// processes the advance signal, landing on cursor-1 from current.
	if c.cursor > 0 {
		c.cursor -= 2
		if c.cursor < -1 {
			c.cursor = -1
		}
	} else {
		c.cursor = -1
	}
	c.mu.Unlock()

	select {
	case c.advanceCh <- struct{}{}:
	default:
	}
	return nil
}

// JumpTo finds the item with the given ID and signals advance to it.
func (c *Channel) JumpTo(itemID string) error {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return ErrNotOnAir
	}
	if c.playlist == nil {
		c.mu.Unlock()
		return ErrNoPlaylist
	}

	found := -1
	for i, item := range c.playlist.Items {
		if item.ID == itemID {
			found = i
			break
		}
	}
	if found < 0 {
		c.mu.Unlock()
		return ErrItemNotFound
	}

	// Set cursor so the advance signal will land on the target item.
	// The run loop reads cursor and increments, so set to found-1.
	c.cursor = found - 1
	c.mu.Unlock()

	select {
	case c.advanceCh <- struct{}{}:
	default:
	}
	return nil
}

// TakeManual pauses playlist advancement.
func (c *Channel) TakeManual() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.mode = ModeManual
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
}

// ReturnToAuto resumes automated playlist advancement.
func (c *Channel) ReturnToAuto() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.mode = ModeAutomated
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}

	select {
	case c.advanceCh <- struct{}{}:
	default:
	}
}

// TakeBreak sets the channel mode to ModeBreak (called by PodBoard when a pod fires).
func (c *Channel) TakeBreak() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.mode = ModeBreak
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}
}

// ReturnFromBreak sets the channel mode back to ModeAutomated after a pod completes.
func (c *Channel) ReturnFromBreak() {
	c.mu.Lock()
	if c.state != StateOnAir {
		c.mu.Unlock()
		return
	}
	c.mode = ModeAutomated
	fn := c.onStateChange
	c.mu.Unlock()
	if fn != nil {
		fn(StateOnAir)
	}

	// Signal advance in case the channel was waiting.
	select {
	case c.advanceCh <- struct{}{}:
	default:
	}
}

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

// SetPTSProvider registers a function that returns the current program PTS.
func (c *Channel) SetPTSProvider(fn func() int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ptsProvider = fn
}

// SetSecondaryHandler registers a handler for secondary events (graphics, audio, etc.).
// Creating the scheduler is deferred until a handler is set.
func (c *Channel) SetSecondaryHandler(h SecondaryEventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.secondaryHandler = h
	if h != nil {
		c.secondaryScheduler = NewSecondaryEventScheduler(h)
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
}

// run is the main playback goroutine. It iterates through playlist items.
func (c *Channel) run(ctx context.Context) {
	defer close(c.done)

	for {
		c.mu.Lock()
		playlist := c.playlist
		cursor := c.cursor
		mode := c.mode
		c.mu.Unlock()

		if playlist == nil || cursor >= len(playlist.Items) {
			break
		}
		if cursor < 0 {
			// Previous() set cursor to -1; advance to 0.
			c.mu.Lock()
			c.cursor = 0
			cursor = 0
			c.mu.Unlock()
		}

		// In manual or break mode, wait for advance signal before playing.
		if mode == ModeManual || mode == ModeBreak {
			select {
			case <-ctx.Done():
				return
			case <-c.advanceCh:
				// Check if mode changed back to auto.
				c.mu.Lock()
				mode = c.mode
				cursor = c.cursor
				if cursor < 0 {
					c.cursor = 0
					cursor = 0
				}
				if cursor >= len(playlist.Items) {
					c.mu.Unlock()
					break
				}
				c.mu.Unlock()
			}
		}

		// Re-read cursor (may have been changed by JumpTo/Previous).
		c.mu.Lock()
		cursor = c.cursor
		if cursor < 0 {
			c.cursor = 0
			cursor = 0
		}
		if cursor >= len(playlist.Items) {
			c.mu.Unlock()
			break
		}
		item := playlist.Items[cursor]
		c.mu.Unlock()

		var cancelled bool
		switch item.Type {
		case ItemClip:
			cancelled = c.playClipItem(ctx, &item)
		case ItemLive:
			cancelled = c.playLiveItem(ctx, &item)
		case ItemSlate:
			cancelled = c.playSlateItem(ctx, &item)
		default:
			slog.Warn("playout: unknown item type, skipping", "type", item.Type, "id", item.ID)
		}

		if cancelled {
			return
		}

		// Advance cursor.
		c.mu.Lock()
		c.cursor++
		c.mu.Unlock()
	}

	// Playlist complete.
	c.mu.Lock()
	c.state = StateCompleted
	fn := c.onStateChange
	c.mu.Unlock()

	if fn != nil {
		fn(StateCompleted)
	}
}

// playClipItem plays a clip item, outputting frames at the correct rate.
// Returns true if the context was cancelled (stop requested).
func (c *Channel) playClipItem(ctx context.Context, item *PlaylistItem) bool {
	c.mu.Lock()
	provider := c.config.AssetProvider
	asRunFn := c.asRunLog
	channelID := c.id
	c.mu.Unlock()

	if provider == nil {
		slog.Warn("playout: no AssetProvider configured", "item", item.ID)
		return false
	}

	// Emit item_start as-run event.
	itemStartTime := time.Now().UTC()
	if asRunFn != nil {
		asRunFn(AsRunEvent{
			Timestamp: itemStartTime,
			Type:      AsRunItemStart,
			ChannelID: channelID,
			ItemID:    item.ID,
			Title:     item.Title,
			Source:    "automated",
		})
	}

	iter, err := provider.Open(item.AssetURI)
	if err != nil {
		slog.Warn("playout: asset open failed", "item", item.ID, "error", err)
		return false
	}
	defer iter.Close()

	// Wrap with trim if in/out points set.
	var frameSource FrameIterator = iter
	if item.InPointMs > 0 || item.OutPointMs > 0 {
		frameSource = NewTrimIterator(iter, item.InPointMs, item.OutPointMs)
	}

	fps := frameSource.EstimateFPS()
	ptsPerFrame := int64(90000.0 / fps)
	frameDuration := time.Duration(float64(time.Second) / fps)
	totalFrames := frameSource.TotalFrames()

	// Secondary event scheduling.
	c.mu.Lock()
	scheduler := c.secondaryScheduler
	c.mu.Unlock()

	if scheduler != nil && len(item.SecondaryEvents) > 0 {
		var durationMs int64
		if totalFrames > 0 && fps > 0 {
			durationMs = int64(float64(totalFrames) * 1000.0 / fps)
		}
		scheduler.BeginItem(item.SecondaryEvents, durationMs)
		defer scheduler.EndItem()
	}

	// Get initial PTS.
	c.mu.Lock()
	ptsFn := c.ptsProvider
	c.mu.Unlock()

	var outputPTS int64
	if ptsFn != nil {
		outputPTS = ptsFn()
		if outputPTS > 0 {
			outputPTS += ptsPerFrame
		}
	}

	playbackStart := time.Now()
	timer := time.NewTimer(frameDuration)
	defer timer.Stop()
	if !timer.Stop() {
		<-timer.C
	}

	var lastFrame *DecodedFrame
	i := 0

	for {
		// Check for advance/cancel.
		select {
		case <-c.advanceCh:
			return false
		case <-ctx.Done():
			return true
		default:
		}

		frame, err := frameSource.Next()
		if err != nil {
			slog.Warn("playout: frame read error", "item", item.ID, "error", err)
			return false
		}
		if frame == nil {
			break // end of stream
		}

		// Pace using absolute time (same as before).
		if i > 0 {
			deadline := playbackStart.Add(time.Duration(i) * frameDuration)
			wait := time.Until(deadline)
			if wait > 0 {
				timer.Reset(wait)
				select {
				case <-ctx.Done():
					return true
				case <-c.advanceCh:
					return false
				case <-timer.C:
				}
			}
		}

		// Update progress.
		if totalFrames > 0 {
			prog := int64(i * 1000 / totalFrames)
			c.itemProgress.Store(prog)
			remainFrames := totalFrames - i
			c.itemRemainMs.Store(int64(float64(remainFrames) * 1000.0 / fps))
		}

		// Tick secondary events.
		if scheduler != nil && len(item.SecondaryEvents) > 0 {
			elapsedMs := int64(float64(i) * 1000.0 / fps)
			scheduler.Tick(elapsedMs)
		}

		// Output frame.
		c.mu.Lock()
		outputFn := c.rawVideoOutput
		c.mu.Unlock()

		if outputFn != nil {
			outputFn(frame.YUV, frame.Width, frame.Height, outputPTS, frame.IsKeyframe)
		}

		// Feed splice coordinator.
		c.mu.Lock()
		sc := c.spliceCoordinator
		c.mu.Unlock()
		if sc != nil {
			sc.IngestFrame(frame.YUV, frame.Width, frame.Height, outputPTS)
		}

		lastFrame = frame
		outputPTS += ptsPerFrame
		i++
	}

	if i == 0 {
		slog.Warn("playout: no frames from asset", "item", item.ID)
		return false
	}

	// Mark complete.
	c.itemProgress.Store(1000)
	c.itemRemainMs.Store(0)

	// Emit item_end as-run event.
	c.mu.Lock()
	asRunEndFn := c.asRunLog
	c.mu.Unlock()
	if asRunEndFn != nil {
		asRunEndFn(AsRunEvent{
			Timestamp:      time.Now().UTC(),
			Type:           AsRunItemEnd,
			ChannelID:      channelID,
			ItemID:         item.ID,
			Title:          item.Title,
			Source:         "automated",
			ActualDurationMs: time.Now().UTC().Sub(itemStartTime).Milliseconds(),
		})
	}

	// Hold mode.
	if item.Hold && lastFrame != nil {
		return c.holdLastFrame(ctx, *lastFrame, &outputPTS)
	}

	return false
}

// playLiveItem holds until Next() is called (live items represent a camera switch).
// Returns true if context was cancelled.
func (c *Channel) playLiveItem(ctx context.Context, item *PlaylistItem) bool {
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(0)

	// Fire start-anchored secondary events.
	c.mu.Lock()
	scheduler := c.secondaryScheduler
	c.mu.Unlock()

	if scheduler != nil && len(item.SecondaryEvents) > 0 {
		scheduler.BeginItem(item.SecondaryEvents, 0)
		scheduler.Tick(0)
		defer scheduler.EndItem()
	}

	select {
	case <-ctx.Done():
		return true
	case <-c.advanceCh:
		return false
	}
}

// playSlateItem holds for DurationMs or until Next().
// Returns true if context was cancelled.
// Phase 1 limitation: slate items are timing markers only — no frames are
// rendered. The switcher source will hold its last frame. Phase 2 will add
// color bar / black / slate asset rendering.
func (c *Channel) playSlateItem(ctx context.Context, item *PlaylistItem) bool {
	c.itemProgress.Store(0)
	c.itemRemainMs.Store(item.DurationMs)

	// Secondary event scheduling.
	c.mu.Lock()
	scheduler := c.secondaryScheduler
	c.mu.Unlock()

	if scheduler != nil && len(item.SecondaryEvents) > 0 {
		scheduler.BeginItem(item.SecondaryEvents, item.DurationMs)
		scheduler.Tick(0) // fire t=0 events immediately
		defer scheduler.EndItem()
	}

	if item.DurationMs <= 0 {
		// No duration: hold until advance (like a live item).
		select {
		case <-ctx.Done():
			return true
		case <-c.advanceCh:
			return false
		}
	}

	duration := time.Duration(item.DurationMs) * time.Millisecond
	timer := time.NewTimer(duration)
	defer timer.Stop()

	// Update progress periodically.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return true
		case <-c.advanceCh:
			return false
		case <-timer.C:
			c.itemProgress.Store(1000)
			c.itemRemainMs.Store(0)
			return false
		case <-tick.C:
			elapsed := time.Since(startTime)
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

			if scheduler != nil && len(item.SecondaryEvents) > 0 {
				scheduler.Tick(elapsed.Milliseconds())
			}
		}
	}
}

// holdLastFrame outputs the last frame at 1fps until advance signal or context cancel.
// Returns true if context was cancelled.
func (c *Channel) holdLastFrame(ctx context.Context, frame DecodedFrame, outputPTS *int64) bool {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return true
		case <-c.advanceCh:
			return false
		case <-ticker.C:
			c.mu.Lock()
			outputFn := c.rawVideoOutput
			c.mu.Unlock()

			if outputFn != nil {
				outputFn(frame.YUV, frame.Width, frame.Height, *outputPTS, true)
			}
			*outputPTS += 90000 // 1 second in 90kHz PTS
		}
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
