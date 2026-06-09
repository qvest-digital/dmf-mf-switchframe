package playout

import (
	"sync"
	"time"
)

// SchedulerState represents the state of the playlist scheduler.
type SchedulerState int

const (
	SchedIdle SchedulerState = iota
	SchedPlaying
	SchedTransitioning
)

func (s SchedulerState) String() string {
	switch s {
	case SchedIdle:
		return "idle"
	case SchedPlaying:
		return "playing"
	case SchedTransitioning:
		return "transitioning"
	default:
		return "unknown"
	}
}

// SchedulerDecision is the result of a single Tick call, describing what the
// channel should do for this frame period.
type SchedulerDecision struct {
	State      SchedulerState
	Item       *PlaylistItem  // current item (nil when idle or completed)
	IsIdle     bool           // true = no active item, play idle asset
	Transition *TransitionReq // non-nil during a transition
	Completed  bool           // true = playlist exhausted or scheduler stopped
}

// TransitionReq describes an active transition between two playlist items.
type TransitionReq struct {
	Outgoing *PlaylistItem
	Incoming *PlaylistItem
	Config   TransitionConfig
	Progress float64 // 0.0 → 1.0
}

// Scheduler is a pure state machine that manages playlist advancement.
// Each frame tick, the channel calls Tick(now) and gets back a decision
// describing what to play, whether idle, and whether a transition is active.
//
// The Scheduler does NOT decode video, manage pipelines, or handle as-run
// logging. It is a deterministic function of (state, time) → decision.
type Scheduler struct {
	mu       sync.Mutex
	playlist *Playlist
	cursor   int
	state    SchedulerState
	mode     OnAirMode

	// Item timing — set when an item begins playing.
	itemStartTime  time.Time
	itemDurationMs int64 // resolved duration for current item (slates)

	// Signals from the channel or operator.
	itemComplete bool // channel signaled the current clip/slate finished
	advancePend  bool // Next() / JumpTo() / Previous() requested an advance
	holdComplete bool // hold item got its complete signal, waiting for Next()

	// Transition state.
	transStartTime  time.Time
	transDurationMs int64
	transOutgoing   *PlaylistItem
	transIncoming   *PlaylistItem
	transConfig     TransitionConfig

	// Flags.
	started bool
	stopped bool

	// idleWaiting means we've advanced past a completed item but the next
	// item has a future ScheduledTime, so we sit idle until then.
	idleWaiting bool
}

// NewScheduler creates a new Scheduler in the idle state.
func NewScheduler() *Scheduler {
	return &Scheduler{
		state: SchedIdle,
		mode:  ModeAutomated,
	}
}

// LoadPlaylist validates and loads a playlist, resetting cursor to 0.
func (s *Scheduler) LoadPlaylist(pl *Playlist) error {
	if err := ValidatePlaylist(pl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playlist = pl
	s.cursor = 0
	s.resetItemState()
	return nil
}

// Start transitions the scheduler to the playing state.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil || len(s.playlist.Items) == 0 {
		return
	}
	s.started = true
	s.stopped = false
	s.state = SchedPlaying
	s.cursor = 0
	s.itemStartTime = time.Time{} // will be set on first Tick
	s.itemComplete = false
	s.advancePend = false
	s.holdComplete = false
	s.idleWaiting = false
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.started = false
	s.state = SchedIdle
	s.resetItemState()
}

// Tick is called once per frame period. It returns a SchedulerDecision
// describing what the channel should render for this tick.
func (s *Scheduler) Tick(now time.Time) SchedulerDecision {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Stopped or no playlist → completed.
	if s.stopped || !s.started || s.playlist == nil {
		return SchedulerDecision{
			State:     SchedIdle,
			Completed: true,
		}
	}

	// 2. If in transition, advance transition progress.
	if s.state == SchedTransitioning {
		return s.tickTransition(now)
	}

	// 3. If idle-waiting for a scheduled item, check if it's time.
	if s.idleWaiting {
		return s.tickIdleWaiting(now)
	}

	// 4. Bounds check — if cursor is past the end, we're done.
	if s.cursor >= len(s.playlist.Items) {
		s.state = SchedIdle
		return SchedulerDecision{
			State:     SchedIdle,
			Completed: true,
		}
	}

	item := &s.playlist.Items[s.cursor]

	// 5. Initialize item start time on first tick of an item.
	if s.itemStartTime.IsZero() {
		s.itemStartTime = now
		s.itemDurationMs = s.resolvedDuration(item)
	}

	// 6. Check if we should advance.
	shouldAdvance := false

	// Explicit advance request (Next / JumpTo / Previous).
	if s.advancePend {
		s.advancePend = false
		shouldAdvance = true
	}

	// Don't auto-advance in manual or break mode.
	if !shouldAdvance && (s.mode == ModeManual || s.mode == ModeBreak) {
		return s.playingDecision(item)
	}

	// Loop items never auto-advance, but explicit Next() overrides.
	if item.Loop && !shouldAdvance {
		s.itemComplete = false
		return s.playingDecision(item)
	}

	// Hold items: once complete, wait for explicit Next().
	if item.Hold {
		if s.itemComplete {
			s.holdComplete = true
			s.itemComplete = false
		}
		if !shouldAdvance {
			return s.playingDecision(item)
		}
	}

	// Clip items advance when channel signals complete.
	if !shouldAdvance && item.Type == ItemClip && s.itemComplete {
		s.itemComplete = false
		shouldAdvance = true
	}

	// Live items never auto-advance (no duration, no complete signal).
	// They only advance via Next(), scheduled time, or JumpTo().

	// Slate items with duration auto-advance by elapsed time.
	if !shouldAdvance && item.Type == ItemSlate && s.itemDurationMs > 0 {
		elapsed := now.Sub(s.itemStartTime)
		if elapsed.Milliseconds() >= s.itemDurationMs {
			shouldAdvance = true
		}
	}

	// Check if next item's scheduled time has arrived.
	if !shouldAdvance {
		shouldAdvance = s.nextScheduledTimeReached(now)
	}

	if shouldAdvance {
		return s.advance(now)
	}

	return s.playingDecision(item)
}

// SignalItemComplete is called by the channel when a clip or slate finishes
// its natural playback.
func (s *Scheduler) SignalItemComplete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.itemComplete = true
}

// Next signals the scheduler to advance to the next item.
func (s *Scheduler) Next() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	s.advancePend = true
	s.holdComplete = false
	s.itemComplete = false
	return nil
}

// Previous moves the cursor back one item.
func (s *Scheduler) Previous() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.cursor > 0 {
		s.cursor--
	}
	s.resetItemState()
	// An explicit operator navigation overrides a pending schedule wait —
	// otherwise the next Tick would still route through tickIdleWaiting.
	s.idleWaiting = false
	s.state = SchedPlaying
	return nil
}

// JumpTo finds the item with the given ID and moves the cursor to it.
func (s *Scheduler) JumpTo(itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.playlist == nil {
		return ErrNoPlaylist
	}
	for i, item := range s.playlist.Items {
		if item.ID == itemID {
			s.cursor = i
			s.resetItemState()
			// An explicit operator jump overrides a pending schedule wait —
			// even if the target item has a future ScheduledTime, the
			// operator asked for it NOW. Without this the next Tick would
			// route back through tickIdleWaiting and re-enter the wait.
			s.idleWaiting = false
			s.state = SchedPlaying
			return nil
		}
	}
	return ErrItemNotFound
}

// TakeManual switches to manual mode, blocking auto-advancement.
func (s *Scheduler) TakeManual() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeManual
}

// ReturnToAuto switches back to automated mode.
func (s *Scheduler) ReturnToAuto() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeAutomated
}

// TakeBreak switches to break mode.
func (s *Scheduler) TakeBreak() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeBreak
}

// ReturnFromBreak switches back to automated mode from break.
func (s *Scheduler) ReturnFromBreak() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = ModeAutomated
}

// State returns the current scheduler state.
func (s *Scheduler) State() SchedulerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Mode returns the current on-air mode.
func (s *Scheduler) Mode() OnAirMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// Cursor returns the current playlist item index.
func (s *Scheduler) Cursor() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

// SchedulerSnapshot captures mode, cursor, and state under one lock.
type SchedulerSnapshot struct {
	State         SchedulerState
	Mode          OnAirMode
	Cursor        int
	ItemStartTime time.Time // when the current item started playing
}

// StatusSnapshot returns mode, cursor, and state under a single lock acquisition.
func (s *Scheduler) StatusSnapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SchedulerSnapshot{
		State:         s.state,
		Mode:          s.mode,
		Cursor:        s.cursor,
		ItemStartTime: s.itemStartTime,
	}
}

// SetCursor restores the scheduler to a specific item position with mode and
// elapsed time. Used by snapshot-driven sync to align a standby engine's
// playout cursor with the active engine's state.
func (s *Scheduler) SetCursor(index int, mode OnAirMode, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil || index < 0 || index >= len(s.playlist.Items) {
		return
	}
	s.cursor = index
	s.mode = mode
	s.state = SchedPlaying
	s.itemStartTime = time.Now().Add(-elapsed)
	s.itemComplete = false
	s.advancePend = false
	s.holdComplete = false
	s.idleWaiting = false
	item := s.playlist.Items[index]
	if item.DurationMs > 0 {
		s.itemDurationMs = int64(item.DurationMs)
	} else {
		s.itemDurationMs = 0
	}
}

// Playlist returns the loaded playlist, or nil. The returned pointer is
// the scheduler's internal reference; the caller must not mutate it.
func (s *Scheduler) Playlist() *Playlist {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playlist
}

// CurrentItem returns a pointer to the current playlist item, or nil.
func (s *Scheduler) CurrentItem() *PlaylistItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil || s.cursor < 0 || s.cursor >= len(s.playlist.Items) {
		return nil
	}
	item := s.playlist.Items[s.cursor]
	return &item
}

// --- internal helpers (must be called with s.mu held) ---

// resetItemState clears per-item tracking fields.
func (s *Scheduler) resetItemState() {
	s.itemStartTime = time.Time{}
	s.itemDurationMs = 0
	s.itemComplete = false
	s.advancePend = false
	s.holdComplete = false
}

// resolvedDuration returns the effective duration for an item.
// For slates, this is DurationMs. For clips, the channel manages duration
// externally and signals via SignalItemComplete. For live items, duration
// is infinite (0 means no timeout).
func (s *Scheduler) resolvedDuration(item *PlaylistItem) int64 {
	switch item.Type {
	case ItemSlate:
		return item.DurationMs
	default:
		return 0
	}
}

// playingDecision returns a Playing decision for the current item.
func (s *Scheduler) playingDecision(item *PlaylistItem) SchedulerDecision {
	s.state = SchedPlaying
	return SchedulerDecision{
		State: SchedPlaying,
		Item:  item,
	}
}

// nextScheduledTimeReached checks if the next item has a ScheduledTime
// that has been reached.
func (s *Scheduler) nextScheduledTimeReached(now time.Time) bool {
	next := s.cursor + 1
	if next >= len(s.playlist.Items) {
		return false
	}
	nextItem := &s.playlist.Items[next]
	if nextItem.ScheduledTime == nil {
		return false
	}
	return !now.Before(*nextItem.ScheduledTime)
}

// advance moves to the next item. It may start a transition if one is
// configured, or do a hard cut. Returns the decision for this tick.
func (s *Scheduler) advance(now time.Time) SchedulerDecision {
	nextCursor := s.cursor + 1

	// Playlist exhausted.
	if nextCursor >= len(s.playlist.Items) {
		s.state = SchedIdle
		s.cursor = nextCursor
		return SchedulerDecision{
			State:     SchedIdle,
			Completed: true,
		}
	}

	nextItem := &s.playlist.Items[nextCursor]

	// If the next item has a future scheduled time and we're advancing
	// because the current item completed (not because the schedule arrived),
	// enter idle-waiting.
	if nextItem.ScheduledTime != nil && now.Before(*nextItem.ScheduledTime) {
		s.cursor = nextCursor
		s.resetItemState()
		s.idleWaiting = true
		s.state = SchedIdle
		return SchedulerDecision{
			State:  SchedIdle,
			IsIdle: true,
		}
	}

	// Determine transition config: per-item takes priority, then playlist default.
	tc := s.effectiveTransition(nextItem)

	if tc != nil && tc.DurationMs > 0 {
		// Start a transition.
		outgoing := s.playlist.Items[s.cursor]
		s.transOutgoing = &outgoing
		s.transIncoming = nextItem
		s.transConfig = *tc
		s.transStartTime = now
		s.transDurationMs = int64(tc.DurationMs)
		s.state = SchedTransitioning

		return SchedulerDecision{
			State: SchedTransitioning,
			Item:  &outgoing,
			Transition: &TransitionReq{
				Outgoing: s.transOutgoing,
				Incoming: s.transIncoming,
				Config:   s.transConfig,
				Progress: 0.0,
			},
		}
	}

	// Hard cut — advance immediately.
	s.cursor = nextCursor
	s.resetItemState()
	s.state = SchedPlaying
	return SchedulerDecision{
		State: SchedPlaying,
		Item:  nextItem,
	}
}

// effectiveTransition returns the transition config to use when advancing
// to the given item. Per-item transition takes priority over playlist default.
// Returns nil if no transition is configured (hard cut).
func (s *Scheduler) effectiveTransition(nextItem *PlaylistItem) *TransitionConfig {
	if nextItem.Transition != nil && nextItem.Transition.DurationMs > 0 {
		return nextItem.Transition
	}
	if s.playlist.DefaultTransition.DurationMs > 0 {
		return &s.playlist.DefaultTransition
	}
	return nil
}

// tickTransition handles a tick while in the transitioning state.
func (s *Scheduler) tickTransition(now time.Time) SchedulerDecision {
	elapsed := now.Sub(s.transStartTime)
	progress := float64(elapsed.Milliseconds()) / float64(s.transDurationMs)
	if progress < 0 {
		progress = 0
	}

	if progress >= 1.0 {
		// Transition complete — commit to the incoming item.
		s.cursor++
		s.resetItemState()
		s.state = SchedPlaying
		incoming := s.transIncoming
		s.transOutgoing = nil
		s.transIncoming = nil
		return SchedulerDecision{
			State: SchedPlaying,
			Item:  incoming,
		}
	}

	return SchedulerDecision{
		State: SchedTransitioning,
		Item:  s.transOutgoing,
		Transition: &TransitionReq{
			Outgoing: s.transOutgoing,
			Incoming: s.transIncoming,
			Config:   s.transConfig,
			Progress: progress,
		},
	}
}

// tickIdleWaiting handles a tick while idle-waiting for a scheduled item.
func (s *Scheduler) tickIdleWaiting(now time.Time) SchedulerDecision {
	if s.cursor >= len(s.playlist.Items) {
		s.idleWaiting = false
		s.state = SchedIdle
		return SchedulerDecision{
			State:     SchedIdle,
			Completed: true,
		}
	}

	item := &s.playlist.Items[s.cursor]

	// An explicit operator advance (Next) overrides the schedule wait: start
	// the waiting item immediately. The flag must be consumed here — leaving
	// it latched used to make the scheduled item play for exactly one tick
	// and then be skipped on air by the stale advancePend.
	if s.advancePend {
		s.advancePend = false
		s.idleWaiting = false
		s.state = SchedPlaying
		s.itemStartTime = now
		s.itemDurationMs = s.resolvedDuration(item)
		return SchedulerDecision{
			State: SchedPlaying,
			Item:  item,
		}
	}

	if item.ScheduledTime != nil && now.Before(*item.ScheduledTime) {
		// Still waiting.
		return SchedulerDecision{
			State:  SchedIdle,
			IsIdle: true,
		}
	}

	// Scheduled time arrived — start playing.
	s.idleWaiting = false
	s.state = SchedPlaying
	s.itemStartTime = now
	s.itemDurationMs = s.resolvedDuration(item)
	return SchedulerDecision{
		State: SchedPlaying,
		Item:  item,
	}
}
