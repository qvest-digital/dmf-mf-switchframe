package playout

import (
	"slices"
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
	advancePend  int  // Next() presses not applied yet, see pendingFrom
	holdComplete bool // hold item got its complete signal, waiting for Next()
	loopExit     bool // EndLoop: the looping item plays its pass out, then ends

	// onAir is the index of the item the last tick played, -1 for none:
	// Previous, JumpTo and NextNow move the cursor off it before it goes.
	// onAirState is its per-item state as that tick left it, which the
	// item keeps when the moves come back to it.
	onAir      int
	onAirState itemState
	// take means NextNow moved the cursor: the next tick transitions into
	// the item at the cursor from the one on air.
	take bool

	// Transition state.
	transStartTime  time.Time
	transDurationMs int64
	transOutgoing   *PlaylistItem
	transIncoming   *PlaylistItem
	transConfig     TransitionConfig
	transTarget     int // index of transIncoming

	// Flags.
	started bool
	stopped bool

	// startAt is the item Start puts on air. Cue sets it while the
	// scheduler is off air; anything else starts from the first item.
	startAt int

	// idleWaiting means we've advanced past a completed item but the next
	// item has a future ScheduledTime, so we sit idle until then.
	idleWaiting bool
}

// itemState is the state of the item on air that resetItemState clears and
// a later tick of the same item reads.
type itemState struct {
	startTime    time.Time
	durationMs   int64
	holdComplete bool
	loopExit     bool
}

// NewScheduler creates a new Scheduler in the idle state.
func NewScheduler() *Scheduler {
	return &Scheduler{
		state: SchedIdle,
		mode:  ModeAutomated,
		onAir: -1,
	}
}

// LoadPlaylist validates and loads a playlist, resetting cursor to 0.
func (s *Scheduler) LoadPlaylist(pl *Playlist) error {
	if err := ValidatePlaylist(pl); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The index of the item on air means nothing in another playlist.
	s.onAir = -1
	s.playlist = pl
	s.cursor = 0
	s.startAt = 0
	s.resetItemState()
	return nil
}

// Start transitions the scheduler to the playing state, from the item Cue
// selected or else from the first item.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Nothing is on air until the first tick, whatever played before a Stop.
	s.onAir = -1
	if s.playlist == nil || len(s.playlist.Items) == 0 {
		return
	}
	s.started = true
	s.stopped = false
	s.state = SchedPlaying
	s.cursor = s.startAt
	s.startAt = 0
	s.itemStartTime = time.Time{} // will be set on first Tick
	s.itemComplete = false
	s.advancePend = 0
	s.holdComplete = false
	s.loopExit = false
	s.take = false
	s.idleWaiting = false
}

// Stop halts the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.started = false
	s.state = SchedIdle
	// Stop drops a cue, so the next Start begins at the top on every
	// engine, whatever cue a restart left on one of them.
	s.startAt = 0
	s.resetItemState()
}

// Tick is called once per frame period. It returns a SchedulerDecision
// describing what the channel should render for this tick.
func (s *Scheduler) Tick(now time.Time) SchedulerDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.tick(now)
	s.onAir = -1
	if d.State == SchedPlaying && d.Item != nil {
		s.onAir = s.cursor
		s.onAirState = itemState{s.itemStartTime, s.itemDurationMs, s.holdComplete, s.loopExit}
	}
	return d
}

// tick is Tick with s.mu held.
func (s *Scheduler) tick(now time.Time) SchedulerDecision {

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

	// The operator moved the cursor off the item on air since the last
	// tick, so the item at the cursor has not aired. Next presses count
	// from it, and NextNow takes it, but the move goes out from the item
	// on air, as an advance from it would. Moves that came back to the
	// item on air leave it as it was.
	take := s.take
	s.take = false
	if take && s.onAir < 0 && s.advancePend > 0 {
		// NextNow left no single item on air, as during a transition, so
		// the presses after it cut to where they land, as NextNow cuts.
		s.cursor += s.advancePend
		s.advancePend = 0
		if s.cursor >= len(s.playlist.Items) {
			s.state = SchedIdle
			return SchedulerDecision{
				State:     SchedIdle,
				Completed: true,
			}
		}
		item = &s.playlist.Items[s.cursor]
	}
	if s.onAir >= 0 {
		switch {
		case s.cursor != s.onAir && s.advancePend > 0:
			target := s.cursor + s.advancePend
			s.advancePend = 0
			return s.moveFrom(now, s.onAir, target, true)
		case s.cursor != s.onAir && take:
			return s.moveFrom(now, s.onAir, s.cursor, false)
		case s.cursor == s.onAir && s.itemStartTime.IsZero():
			s.resumeOnAir()
		}
	}

	// 5. Initialize item start time on first tick of an item.
	if s.itemStartTime.IsZero() {
		s.itemStartTime = now
		s.itemDurationMs = s.resolvedDuration(item)
	}

	// 6. Check if we should advance.
	shouldAdvance := false

	// Every Next pressed since the last tick, applied as one move: N
	// presses land N items ahead with one item change, so no item between
	// them goes to air for a frame.
	steps := 1
	if s.advancePend > 0 {
		steps = s.advancePend
		s.advancePend = 0
		shouldAdvance = true
	}

	// Don't auto-advance in manual or break mode.
	if !shouldAdvance && (s.mode == ModeManual || s.mode == ModeBreak) {
		return s.playingDecision(item)
	}

	// Loop items never auto-advance, but explicit Next() overrides. After
	// EndLoop the item ends like any clip once its pass completes.
	if item.Loop && !s.loopExit && !shouldAdvance {
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
		return s.advance(now, steps)
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

// Next signals the scheduler to advance to the next item. Presses before
// the next tick add up, each one item further, up to the end of the
// playlist; a press past it is ignored.
func (s *Scheduler) Next() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.pendingFrom()+s.advancePend < len(s.playlist.Items) {
		s.advancePend++
	}
	s.holdComplete = false
	s.itemComplete = false
	return nil
}

// NextNow moves the cursor at once to the item after the one on air and
// drops every Next not applied yet: the operator takes that item, whatever
// was queued. A further NextNow or Next before the tick moves on from
// there. The next tick airs the item the cursor ends on the way an advance
// from the item on air does, through the playlist's transition into it.
// During a transition it takes the item after the one coming in and cuts
// to it, since the picture on air is a blend no transition can start
// from, and Next presses after it cut on from there. In a wait for a
// scheduled item it starts that item. At the last item it completes the
// playlist on the next tick.
func (s *Scheduler) NextNow() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.playlist == nil {
		return ErrNoPlaylist
	}
	s.cursor = min(s.pendingFrom()+1, len(s.playlist.Items))
	s.resetItemState()
	s.take = true
	s.transOutgoing = nil
	s.transIncoming = nil
	s.idleWaiting = false
	s.state = SchedPlaying
	return nil
}

// Previous takes back a Next not applied yet, or else moves the cursor
// back one item.
func (s *Scheduler) Previous() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.advancePend > 0 {
		s.advancePend--
		return nil
	}
	if s.cursor > 0 {
		s.cursor--
	} else if s.cursor == s.onAir {
		// Previous on the first item restarts it.
		s.onAir = -1
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
			if i == s.cursor && i == s.onAir {
				// A jump to the item on air restarts it.
				s.onAir = -1
			}
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

// EndLoop lets the looping clip on air finish its current pass and then end
// as a clip that does not loop: the playlist moves on, or completes after
// its last item. Next still cuts away at once.
func (s *Scheduler) EndLoop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.stopped {
		return ErrNotOnAir
	}
	if s.playlist == nil || s.cursor >= len(s.playlist.Items) {
		return ErrNotLooping
	}
	item := s.playlist.Items[s.cursor]
	if !item.Loop || item.Type != ItemClip {
		return ErrNotLooping
	}
	s.loopExit = true
	if s.cursor == s.onAir {
		s.onAirState.loopExit = true
	}
	return nil
}

// LoopExitPending reports whether the item on air leaves its loop at the end
// of the current pass.
func (s *Scheduler) LoopExitPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loopExit
}

// Cue moves the cursor to the item with the given ID, so the next Start
// puts that item on air first. The channel calls it only while off air; a
// scheduler whose playlist ran out still counts as started.
func (s *Scheduler) Cue(itemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil {
		return ErrNoPlaylist
	}
	for i, item := range s.playlist.Items {
		if item.ID == itemID {
			s.cursor = i
			s.startAt = i
			s.resetItemState()
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
	Next          int       // the item Next or the schedule airs next
	ItemStartTime time.Time // when the current item started playing
	LoopExit      bool      // the looping item ends after this pass
}

// StatusSnapshot returns mode, cursor, and state under a single lock acquisition.
func (s *Scheduler) StatusSnapshot() SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SchedulerSnapshot{
		State:         s.state,
		Mode:          s.mode,
		Cursor:        s.cursor,
		Next:          s.nextIndex(),
		ItemStartTime: s.itemStartTime,
		LoopExit:      s.loopExit,
	}
}

// SetCursor restores the scheduler to a specific item position with mode and
// elapsed time. Used by snapshot-driven sync to align a standby engine's
// playout cursor with the active engine's state.
func (s *Scheduler) SetCursor(index int, mode OnAirMode, elapsed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAir = -1
	if s.playlist == nil || index < 0 || index >= len(s.playlist.Items) {
		return
	}
	s.cursor = index
	s.mode = mode
	s.state = SchedPlaying
	s.itemStartTime = time.Now().Add(-elapsed)
	s.itemComplete = false
	s.advancePend = 0
	s.holdComplete = false
	s.take = false
	// A loop release belongs to the item it was given for and to the engine
	// state the cursor is taken from; the snapshot carries it separately.
	s.loopExit = false
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

// LiveSourcesAhead returns the sources of the live items at the cursor and
// right after it: the item on air or about to start, and the one Next or
// the schedule starts after it. An empty string means no live item there.
func (s *Scheduler) LiveSourcesAhead() (cur, next string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil {
		return "", ""
	}
	liveSource := func(i int) string {
		if i < 0 || i >= len(s.playlist.Items) || s.playlist.Items[i].Type != ItemLive {
			return ""
		}
		return s.playlist.Items[i].Source
	}
	return liveSource(s.cursor), liveSource(s.nextIndex())
}

// ClipsAhead returns the clip items the channel airs next: cur holds the
// one at the cursor, which is on air or starts next, and next up to n
// clips after it. Off air the cursor is the item Start puts on air. Items
// whose IDs are in open are left out, and count towards n when they follow
// the cursor.
func (s *Scheduler) ClipsAhead(n int, open ...string) (cur, next []PlaylistItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.playlist == nil {
		return nil, nil
	}
	at := s.cursor
	if !s.started {
		at = s.startAt
	}
	if at < 0 || at >= len(s.playlist.Items) {
		return nil, nil
	}
	if item := s.playlist.Items[at]; item.Type == ItemClip && !slices.Contains(open, item.ID) {
		cur = append(cur, item)
	}
	for i := at + 1; i < len(s.playlist.Items) && n > 0; i++ {
		item := s.playlist.Items[i]
		if item.Type != ItemClip {
			continue
		}
		n--
		if !slices.Contains(open, item.ID) {
			next = append(next, item)
		}
	}
	return cur, next
}

// --- internal helpers (must be called with s.mu held) ---

// resetItemState clears per-item tracking fields.
func (s *Scheduler) resetItemState() {
	s.itemStartTime = time.Time{}
	s.itemDurationMs = 0
	s.itemComplete = false
	s.advancePend = 0
	s.holdComplete = false
	s.loopExit = false
	s.take = false
}

// nextIndex returns the index of the item that airs next: the one the
// pending Next presses land on, the one a transition in progress brings
// in, or else the one after the cursor.
func (s *Scheduler) nextIndex() int {
	switch {
	case s.advancePend > 0:
		return s.pendingFrom() + s.advancePend
	case s.idleWaiting:
		return s.cursor
	case s.state == SchedTransitioning:
		return s.transTarget
	}
	return s.cursor + 1
}

// pendingFrom returns the index the pending Next presses count from: the
// item a transition in progress brings in, the one before an item waiting
// for its scheduled time, which one press starts, or else the cursor.
func (s *Scheduler) pendingFrom() int {
	switch {
	case s.state == SchedTransitioning:
		return s.transTarget
	case s.idleWaiting:
		return s.cursor - 1
	}
	return s.cursor
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

// advance moves steps items ahead. It may start a transition if one is
// configured, or do a hard cut. Returns the decision for this tick.
func (s *Scheduler) advance(now time.Time, steps int) SchedulerDecision {
	nextCursor := s.cursor + steps

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

	outgoing := s.playlist.Items[s.cursor]
	if d, ok := s.startTransition(now, &outgoing, nextCursor); ok {
		return d
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

// resumeOnAir gives the item on air back the state its last tick left it.
func (s *Scheduler) resumeOnAir() {
	st := s.onAirState
	s.itemStartTime, s.itemDurationMs = st.startTime, st.durationMs
	s.holdComplete, s.loopExit = st.holdComplete, st.loopExit
}

// moveFrom moves from the item at index from, on air, to the one at
// target, as advance moves from the cursor: through a transition when one
// is configured into it, else as a cut, and into the wait for a scheduled
// target when schedule is set. The cursor stays on the item on air until
// the transition commits. Presses that come back to the item on air leave
// it playing as it was.
func (s *Scheduler) moveFrom(now time.Time, from, target int, schedule bool) SchedulerDecision {
	if target >= len(s.playlist.Items) {
		s.cursor = len(s.playlist.Items)
		s.state = SchedIdle
		return SchedulerDecision{
			State:     SchedIdle,
			Completed: true,
		}
	}
	if target == from {
		s.cursor = from
		s.resumeOnAir()
		return s.playingDecision(&s.playlist.Items[from])
	}
	if next := &s.playlist.Items[target]; schedule && next.ScheduledTime != nil && now.Before(*next.ScheduledTime) {
		s.cursor = target
		s.resetItemState()
		s.idleWaiting = true
		s.state = SchedIdle
		return SchedulerDecision{
			State:  SchedIdle,
			IsIdle: true,
		}
	}
	outgoing := s.playlist.Items[from]
	if d, ok := s.startTransition(now, &outgoing, target); ok {
		s.cursor = from
		return d
	}
	s.cursor = target
	s.resetItemState()
	return s.playingDecision(&s.playlist.Items[target])
}

// startTransition starts the transition from outgoing to the item at
// index target, when one is configured into it, and reports whether it did.
func (s *Scheduler) startTransition(now time.Time, outgoing *PlaylistItem, target int) (SchedulerDecision, bool) {
	// Per-item transition takes priority, then playlist default.
	incoming := &s.playlist.Items[target]
	tc := s.effectiveTransition(incoming)
	if tc == nil || tc.DurationMs <= 0 {
		return SchedulerDecision{}, false
	}
	s.transOutgoing = outgoing
	s.transIncoming = incoming
	s.transConfig = *tc
	s.transTarget = target
	s.transStartTime = now
	s.transDurationMs = int64(tc.DurationMs)
	s.state = SchedTransitioning

	return SchedulerDecision{
		State: SchedTransitioning,
		Item:  outgoing,
		Transition: &TransitionReq{
			Outgoing: s.transOutgoing,
			Incoming: s.transIncoming,
			Config:   s.transConfig,
			Progress: 0.0,
		},
	}, true
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
		// Transition complete: commit to the incoming item. A Next
		// pressed during the transition counts from it and applies on the
		// next tick.
		s.cursor = s.transTarget
		pend := s.advancePend
		s.resetItemState()
		s.advancePend = pend
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

	// An explicit operator advance (Next) overrides the schedule wait: one
	// press starts the waiting item immediately, each further one an item
	// after it. The count must be consumed here: leaving it latched used
	// to make the scheduled item play for exactly one tick and then be
	// skipped on air by the stale advancePend.
	if s.advancePend > 0 {
		s.cursor += s.advancePend - 1
		s.advancePend = 0
		s.idleWaiting = false
		if s.cursor >= len(s.playlist.Items) {
			s.state = SchedIdle
			return SchedulerDecision{
				State:     SchedIdle,
				Completed: true,
			}
		}
		item = &s.playlist.Items[s.cursor]
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
