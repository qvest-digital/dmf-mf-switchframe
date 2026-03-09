package scte35

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// InjectorConfig holds injector configuration.
type InjectorConfig struct {
	// HeartbeatInterval is the interval between splice_null heartbeats.
	// Set to 0 to disable heartbeats.
	HeartbeatInterval time.Duration

	// DefaultPreRollMs is the default pre-roll time in milliseconds for scheduled cues.
	DefaultPreRollMs int64

	// SCTE35PID is the MPEG-TS PID for SCTE-35 data. Default: 0x0500.
	SCTE35PID uint16

	// EventIDStart is the starting value for auto-assigned event IDs. Default: 1.
	EventIDStart uint32

	// MaxEventLog is the maximum number of event log entries to retain.
	// Default: 256.
	MaxEventLog int

	// VerifyEncoding when true causes encoded SCTE-35 to be decoded back for
	// CRC-32 verification.
	VerifyEncoding bool

	// WebhookURL is the URL to POST event notifications to (optional).
	WebhookURL string

	// WebhookTimeoutMs is the timeout for webhook POST requests in milliseconds.
	WebhookTimeoutMs int64
}

// activeEvent tracks an in-progress SCTE-35 event (internal, not exported).
type activeEvent struct {
	EventID       uint32
	CommandType   uint8
	IsOut         bool
	Duration      time.Duration
	StartedAt     time.Time
	AutoReturn    bool
	Held          bool
	SpliceTimePTS int64
	Descriptors   []SegmentationDescriptor
	returnTimer   *time.Timer
}

// ActiveEventState is the serializable snapshot of an active event, used by
// State() and the control API.
type ActiveEventState struct {
	EventID       uint32                   `json:"eventId"`
	CommandType   string                   `json:"commandType"`
	IsOut         bool                     `json:"isOut"`
	DurationMs    *int64                   `json:"durationMs,omitempty"`
	ElapsedMs     int64                    `json:"elapsedMs"`
	RemainingMs   *int64                   `json:"remainingMs,omitempty"`
	AutoReturn    bool                     `json:"autoReturn"`
	Held          bool                     `json:"held"`
	SpliceTimePTS int64                    `json:"spliceTimePts"`
	StartedAt     int64                    `json:"startedAt"` // unix ms
	Descriptors   []SegmentationDescriptor `json:"descriptors,omitempty"`
}

// InjectorState is a snapshot of injector state for serialization.
type InjectorState struct {
	ActiveEvents map[uint32]ActiveEventState `json:"activeEvents"`
	EventLog     []EventLogEntry             `json:"eventLog"`
	HeartbeatOK  bool                        `json:"heartbeatOk"`
	Enabled      bool                        `json:"enabled"`
}

// EventLogEntry records a single SCTE-35 event for the event log.
type EventLogEntry struct {
	EventID        uint32                   `json:"eventID"`
	CommandType    string                   `json:"commandType"`
	IsOut          bool                     `json:"isOut"`
	DurationMs     *int64                   `json:"durationMs,omitempty"`
	AutoReturn     bool                     `json:"autoReturn"`
	Timestamp      int64                    `json:"timestamp"` // unix ms
	Status         string                   `json:"status"`    // "injected", "returned", "cancelled", "held", "extended"
	Descriptors    []SegmentationDescriptor `json:"descriptors,omitempty"`
	SpliceTimePTS  *int64                   `json:"spliceTimePts,omitempty"`
	Source         string                   `json:"source,omitempty"`
	AvailNum       uint8                    `json:"availNum,omitempty"`
	AvailsExpected uint8                    `json:"availsExpected,omitempty"`
}

// circularLog is a ring buffer for event log entries.
type circularLog struct {
	entries []EventLogEntry
	size    int
	head    int
	count   int
}

func newCircularLog(size int) *circularLog {
	if size <= 0 {
		size = 256
	}
	return &circularLog{
		entries: make([]EventLogEntry, size),
		size:    size,
	}
}

func (cl *circularLog) add(entry EventLogEntry) {
	cl.entries[cl.head] = entry
	cl.head = (cl.head + 1) % cl.size
	if cl.count < cl.size {
		cl.count++
	}
}

func (cl *circularLog) list() []EventLogEntry {
	if cl.count == 0 {
		return nil
	}
	result := make([]EventLogEntry, cl.count)
	start := cl.head - cl.count
	if start < 0 {
		start += cl.size
	}
	for i := 0; i < cl.count; i++ {
		result[i] = cl.entries[(start+i)%cl.size]
	}
	return result
}

// Injector manages the lifecycle of SCTE-35 splice events.
type Injector struct {
	config         InjectorConfig
	activeEvents   map[uint32]*activeEvent
	eventIDCounter atomic.Uint32
	eventLog       *circularLog
	rules          *RuleEngine
	webhook        *WebhookDispatcher
	muxerSink      func([]byte)
	ptsFn          func() int64
	onStateChange  func()
	heartbeatStop  chan struct{}
	closed         atomic.Bool
	mu             sync.Mutex
}

// NewInjector creates a new SCTE-35 injector.
// muxerSink is called with encoded SCTE-35 binary data to inject into the TS
// muxer. ptsFn returns the current video PTS in 90 kHz ticks.
func NewInjector(config InjectorConfig, muxerSink func([]byte), ptsFn func() int64) *Injector {
	if config.SCTE35PID == 0 {
		config.SCTE35PID = 0x0500
	}
	if config.MaxEventLog <= 0 {
		config.MaxEventLog = 256
	}

	startID := config.EventIDStart
	if startID == 0 {
		startID = 1
	}

	inj := &Injector{
		config:        config,
		activeEvents:  make(map[uint32]*activeEvent),
		eventLog:      newCircularLog(config.MaxEventLog),
		muxerSink:     muxerSink,
		ptsFn:         ptsFn,
		heartbeatStop: make(chan struct{}),
	}
	inj.eventIDCounter.Store(startID)

	if config.WebhookURL != "" {
		timeout := 5 * time.Second
		if config.WebhookTimeoutMs > 0 {
			timeout = time.Duration(config.WebhookTimeoutMs) * time.Millisecond
		}
		inj.webhook = NewWebhookDispatcher(config.WebhookURL, timeout)
	}

	if config.HeartbeatInterval > 0 {
		go inj.heartbeatLoop()
	}

	return inj
}

// dispatchWebhook sends a webhook event if a dispatcher is configured.
func (inj *Injector) dispatchWebhook(eventType string, eventID uint32, cmdType uint8, isOut bool, durationMs int64) {
	if inj.webhook == nil {
		return
	}
	inj.webhook.Dispatch(WebhookEvent{
		Type:     eventType,
		EventID:  eventID,
		Command:  commandTypeName(cmdType),
		IsOut:    isOut,
		Duration: durationMs,
	})
}

// heartbeatLoop sends splice_null at the configured interval.
func (inj *Injector) heartbeatLoop() {
	ticker := time.NewTicker(inj.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			inj.sendHeartbeat()
		case <-inj.heartbeatStop:
			return
		}
	}
}

// sendHeartbeat encodes and sends a splice_null message.
func (inj *Injector) sendHeartbeat() {
	msg := &CueMessage{CommandType: CommandSpliceNull}
	data, err := msg.Encode(false)
	if err != nil {
		return
	}
	inj.muxerSink(data)
}

// InjectCue injects an immediate SCTE-35 cue message.
// If msg.EventID is 0 for splice_insert commands, an ID is auto-assigned.
// Returns the event ID used and any error.
func (inj *Injector) InjectCue(msg *CueMessage) (uint32, error) {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return 0, fmt.Errorf("injector is closed")
	}

	// Auto-assign event ID for splice_insert if not set.
	if msg.CommandType == CommandSpliceInsert && msg.EventID == 0 {
		msg.EventID = inj.eventIDCounter.Add(1) - 1
	}

	// Populate PTS for time_signal if not already set.
	if msg.CommandType == CommandTimeSignal && msg.SpliceTimePTS == nil {
		currentPTS := inj.ptsFn()
		msg.SpliceTimePTS = &currentPTS
	}

	// Evaluate rules if configured.
	if inj.rules != nil {
		action, modified := inj.rules.Evaluate(msg, "")
		switch action {
		case ActionDelete:
			inj.mu.Unlock()
			return 0, nil // silently drop
		case ActionReplace:
			if modified != nil {
				msg = modified
			}
			// ActionPass: proceed normally
		}
	}

	// Encode the message.
	data, err := msg.Encode(inj.config.VerifyEncoding)
	if err != nil {
		inj.mu.Unlock()
		return 0, fmt.Errorf("encode cue: %w", err)
	}

	// Send to muxer.
	inj.muxerSink(data)

	// Track active event for splice_insert cue-out.
	if msg.CommandType == CommandSpliceInsert && msg.IsOut {
		ae := &activeEvent{
			EventID:     msg.EventID,
			CommandType: msg.CommandType,
			IsOut:       msg.IsOut,
			StartedAt:   time.Now(),
			AutoReturn:  msg.AutoReturn,
		}

		if msg.SpliceTimePTS != nil {
			ae.SpliceTimePTS = *msg.SpliceTimePTS
		}
		if msg.BreakDuration != nil {
			ae.Duration = *msg.BreakDuration
		}
		ae.Descriptors = append([]SegmentationDescriptor(nil), msg.Descriptors...)

		// Start auto-return timer if configured.
		if msg.AutoReturn && msg.BreakDuration != nil && *msg.BreakDuration > 0 {
			dur := *msg.BreakDuration
			eid := msg.EventID
			ae.returnTimer = time.AfterFunc(dur, func() {
				_ = inj.ReturnToProgram(eid)
			})
		}

		inj.activeEvents[msg.EventID] = ae
	}

	// Log the event.
	var durMs *int64
	if msg.BreakDuration != nil {
		ms := msg.BreakDuration.Milliseconds()
		durMs = &ms
	}
	inj.logEventLocked(msg.EventID, msg.CommandType, msg.IsOut, durMs, msg.AutoReturn, "injected", msg)

	eventID := msg.EventID
	webhookIsOut := msg.IsOut
	webhookCmdType := msg.CommandType
	var webhookDurMs int64
	if msg.BreakDuration != nil {
		webhookDurMs = msg.BreakDuration.Milliseconds()
	}
	cb := inj.onStateChange
	inj.mu.Unlock()

	// Fire state change callback outside the lock to avoid deadlock
	// (callback may call State() which also acquires mu).
	if cb != nil {
		cb()
	}

	// Dispatch webhook after releasing lock.
	webhookType := "cue_out"
	if !webhookIsOut {
		webhookType = "cue_in"
	}
	inj.dispatchWebhook(webhookType, eventID, webhookCmdType, webhookIsOut, webhookDurMs)

	return eventID, nil
}

// ScheduleCue sets the splice time PTS based on current PTS + preRollMs,
// then injects the cue.
func (inj *Injector) ScheduleCue(msg *CueMessage, preRollMs int64) (uint32, error) {
	currentPTS := inj.ptsFn()
	spliceTimePTS := currentPTS + preRollMs*90 // 90 kHz ticks per ms
	msg.SpliceTimePTS = &spliceTimePTS
	msg.Timing = "scheduled"
	return inj.InjectCue(msg)
}

// ReturnToProgram sends a cue-in (return to network) for the given event ID.
// If eventID is 0, the most recent active event is used.
func (inj *Injector) ReturnToProgram(eventID uint32) error {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return fmt.Errorf("injector is closed")
	}

	if eventID == 0 {
		eventID = inj.mostRecentActiveIDLocked()
		if eventID == 0 {
			inj.mu.Unlock()
			return fmt.Errorf("no active events")
		}
	}

	ae, ok := inj.activeEvents[eventID]
	if !ok {
		inj.mu.Unlock()
		return fmt.Errorf("event %d not active", eventID)
	}

	// Stop the auto-return timer if running.
	if ae.returnTimer != nil {
		ae.returnTimer.Stop()
	}

	// Build and send cue-in.
	cueIn := NewSpliceInsert(eventID, 0, false, false)
	data, err := cueIn.Encode(inj.config.VerifyEncoding)
	if err != nil {
		inj.mu.Unlock()
		return fmt.Errorf("encode cue-in: %w", err)
	}
	inj.muxerSink(data)

	// Remove from active.
	delete(inj.activeEvents, eventID)

	// Log.
	inj.logEventLocked(eventID, CommandSpliceInsert, false, nil, false, "returned", cueIn)

	cb := inj.onStateChange
	inj.mu.Unlock()

	if cb != nil {
		cb()
	}

	inj.dispatchWebhook("cue_in", eventID, CommandSpliceInsert, false, 0)

	return nil
}

// CancelEvent cancels an active event by sending a cancel indicator and
// removing it from active tracking.
func (inj *Injector) CancelEvent(eventID uint32) error {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return fmt.Errorf("injector is closed")
	}

	ae, ok := inj.activeEvents[eventID]
	if !ok {
		inj.mu.Unlock()
		return fmt.Errorf("event %d not active", eventID)
	}

	// Stop the auto-return timer if running.
	if ae.returnTimer != nil {
		ae.returnTimer.Stop()
	}

	// Build a splice_insert with splice_event_cancel_indicator set.
	cancelMsg := &CueMessage{
		CommandType:                 CommandSpliceInsert,
		EventID:                    eventID,
		SpliceEventCancelIndicator: true,
	}
	data, err := cancelMsg.Encode(inj.config.VerifyEncoding)
	if err != nil {
		inj.mu.Unlock()
		return fmt.Errorf("encode cancel: %w", err)
	}
	inj.muxerSink(data)

	// Remove from active.
	delete(inj.activeEvents, eventID)

	// Log.
	inj.logEventLocked(eventID, CommandSpliceInsert, false, nil, false, "cancelled", cancelMsg)

	cb := inj.onStateChange
	inj.mu.Unlock()

	if cb != nil {
		cb()
	}

	inj.dispatchWebhook("cancel", eventID, CommandSpliceInsert, false, 0)

	return nil
}

// CancelSegmentationEvent sends a time_signal with a segmentation descriptor
// that has the segmentation_event_cancel_indicator set, per the SCTE-35 spec.
// Unlike CancelEvent (which cancels tracked splice_insert events), this method
// does not require the event to be tracked -- it simply emits the cancel message.
func (inj *Injector) CancelSegmentationEvent(segEventID uint32) error {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return fmt.Errorf("injector is closed")
	}

	// Build a time_signal with a cancel descriptor.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:                      segEventID,
				SegmentationEventCancelIndicator: true,
			},
		},
	}

	data, err := msg.Encode(inj.config.VerifyEncoding)
	if err != nil {
		inj.mu.Unlock()
		return fmt.Errorf("encode cancel segmentation: %w", err)
	}
	inj.muxerSink(data)

	// Log the event.
	inj.logEventLocked(segEventID, CommandTimeSignal, false, nil, false, "cancelled", msg)

	cb := inj.onStateChange
	inj.mu.Unlock()

	if cb != nil {
		cb()
	}

	return nil
}

// HoldBreak prevents auto-return from firing for the given event.
func (inj *Injector) HoldBreak(eventID uint32) error {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return fmt.Errorf("injector is closed")
	}

	ae, ok := inj.activeEvents[eventID]
	if !ok {
		inj.mu.Unlock()
		return fmt.Errorf("event %d not active", eventID)
	}

	// Stop the auto-return timer.
	if ae.returnTimer != nil {
		ae.returnTimer.Stop()
		ae.returnTimer = nil
	}

	ae.Held = true

	// Log.
	inj.logEventLocked(eventID, ae.CommandType, ae.IsOut, nil, ae.AutoReturn, "held", nil)

	// Capture fields before unlocking.
	cmdType := ae.CommandType
	isOut := ae.IsOut
	cb := inj.onStateChange
	inj.mu.Unlock()

	if cb != nil {
		cb()
	}

	inj.dispatchWebhook("hold", eventID, cmdType, isOut, 0)

	return nil
}

// ExtendBreak updates the duration of an active event and resets the
// auto-return timer.
func (inj *Injector) ExtendBreak(eventID uint32, newDurationMs int64) error {
	inj.mu.Lock()

	if inj.closed.Load() {
		inj.mu.Unlock()
		return fmt.Errorf("injector is closed")
	}

	ae, ok := inj.activeEvents[eventID]
	if !ok {
		inj.mu.Unlock()
		return fmt.Errorf("event %d not active", eventID)
	}

	// Stop existing timer.
	if ae.returnTimer != nil {
		ae.returnTimer.Stop()
		ae.returnTimer = nil
	}

	// Update duration.
	newDur := time.Duration(newDurationMs) * time.Millisecond
	ae.Duration = newDur
	ae.Held = false

	// Calculate remaining duration from when the event started.
	elapsed := time.Since(ae.StartedAt)
	remaining := newDur - elapsed
	if remaining > 0 && ae.AutoReturn {
		ae.returnTimer = time.AfterFunc(remaining, func() {
			_ = inj.ReturnToProgram(eventID)
		})
	}

	// Send updated splice_insert with new duration.
	updateMsg := NewSpliceInsert(eventID, newDur, true, ae.AutoReturn)
	data, err := updateMsg.Encode(inj.config.VerifyEncoding)
	if err != nil {
		inj.mu.Unlock()
		return fmt.Errorf("encode extend: %w", err)
	}
	inj.muxerSink(data)

	// Log.
	durMs := newDurationMs
	inj.logEventLocked(eventID, ae.CommandType, ae.IsOut, &durMs, ae.AutoReturn, "extended", updateMsg)

	// Capture fields before unlocking.
	cmdType := ae.CommandType
	isOut := ae.IsOut
	cb := inj.onStateChange
	inj.mu.Unlock()

	if cb != nil {
		cb()
	}

	inj.dispatchWebhook("extend", eventID, cmdType, isOut, newDurationMs)

	return nil
}

// SyntheticBreakState builds a splice_insert with the remaining duration for
// late-joining clients. Returns nil if no active events.
func (inj *Injector) SyntheticBreakState() []byte {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	if len(inj.activeEvents) == 0 {
		return nil
	}

	// Use the most recent active event.
	eventID := inj.mostRecentActiveIDLocked()
	if eventID == 0 {
		return nil
	}

	ae := inj.activeEvents[eventID]

	// Calculate remaining duration.
	var remaining time.Duration
	if ae.Duration > 0 {
		elapsed := time.Since(ae.StartedAt)
		remaining = ae.Duration - elapsed
		if remaining < 0 {
			remaining = 0
		}
	}

	msg := NewSpliceInsert(ae.EventID, remaining, ae.IsOut, ae.AutoReturn)
	data, err := msg.Encode(false)
	if err != nil {
		return nil
	}

	return data
}

// State returns a snapshot of the injector state.
func (inj *Injector) State() InjectorState {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	now := time.Now()

	activeMap := make(map[uint32]ActiveEventState, len(inj.activeEvents))
	for id, ae := range inj.activeEvents {
		state := ActiveEventState{
			EventID:       ae.EventID,
			CommandType:   commandTypeName(ae.CommandType),
			IsOut:         ae.IsOut,
			AutoReturn:    ae.AutoReturn,
			Held:          ae.Held,
			SpliceTimePTS: ae.SpliceTimePTS,
			StartedAt:     ae.StartedAt.UnixMilli(),
			Descriptors:   ae.Descriptors,
		}

		elapsedMs := now.Sub(ae.StartedAt).Milliseconds()
		state.ElapsedMs = elapsedMs

		if ae.Duration > 0 {
			durMs := ae.Duration.Milliseconds()
			state.DurationMs = &durMs

			remainMs := durMs - elapsedMs
			if remainMs < 0 {
				remainMs = 0
			}
			state.RemainingMs = &remainMs
		}

		activeMap[id] = state
	}

	return InjectorState{
		ActiveEvents: activeMap,
		EventLog:     inj.eventLog.list(),
		HeartbeatOK:  inj.config.HeartbeatInterval > 0 && !inj.closed.Load(),
		Enabled:      !inj.closed.Load(),
	}
}

// EventLog returns the event log entries.
func (inj *Injector) EventLog() []EventLogEntry {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	return inj.eventLog.list()
}

// ActiveEventIDs returns a sorted list of active event IDs.
func (inj *Injector) ActiveEventIDs() []uint32 {
	inj.mu.Lock()
	defer inj.mu.Unlock()

	ids := make([]uint32, 0, len(inj.activeEvents))
	for id := range inj.activeEvents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// SetRuleEngine sets the rule engine for processing cues.
func (inj *Injector) SetRuleEngine(re *RuleEngine) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.rules = re
}

// OnStateChange registers a callback invoked when injector state changes.
func (inj *Injector) OnStateChange(fn func()) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.onStateChange = fn
}

// Close stops the heartbeat and all auto-return timers.
func (inj *Injector) Close() {
	if inj.closed.Swap(true) {
		return // already closed
	}

	close(inj.heartbeatStop)

	inj.mu.Lock()
	defer inj.mu.Unlock()

	for _, ae := range inj.activeEvents {
		if ae.returnTimer != nil {
			ae.returnTimer.Stop()
		}
	}
}

// logEventLocked adds an event log entry. Must be called with mu held.
// When msg is non-nil, additional fields (Descriptors, SpliceTimePTS, AvailNum,
// AvailsExpected) are extracted from the message.
func (inj *Injector) logEventLocked(eventID uint32, cmdType uint8, isOut bool, durationMs *int64, autoReturn bool, status string, msg *CueMessage) {
	var durPtr *int64
	if durationMs != nil {
		d := *durationMs
		durPtr = &d
	}

	entry := EventLogEntry{
		EventID:     eventID,
		CommandType: commandTypeName(cmdType),
		IsOut:       isOut,
		DurationMs:  durPtr,
		AutoReturn:  autoReturn,
		Timestamp:   time.Now().UnixMilli(),
		Status:      status,
		Source:      "injector",
	}
	if msg != nil {
		entry.Descriptors = msg.Descriptors
		if msg.SpliceTimePTS != nil {
			pts := *msg.SpliceTimePTS
			entry.SpliceTimePTS = &pts
		}
		entry.AvailNum = msg.AvailNum
		entry.AvailsExpected = msg.AvailsExpected
	}

	inj.eventLog.add(entry)
}

// mostRecentActiveIDLocked returns the event ID of the most recently started
// active event. Must be called with mu held.
func (inj *Injector) mostRecentActiveIDLocked() uint32 {
	var bestID uint32
	var bestTime time.Time
	for id, ae := range inj.activeEvents {
		if bestID == 0 || ae.StartedAt.After(bestTime) {
			bestID = id
			bestTime = ae.StartedAt
		}
	}
	return bestID
}

// commandTypeName returns a human-readable name for a command type.
func commandTypeName(cmdType uint8) string {
	switch cmdType {
	case CommandSpliceNull:
		return "splice_null"
	case CommandSpliceInsert:
		return "splice_insert"
	case CommandTimeSignal:
		return "time_signal"
	default:
		return fmt.Sprintf("unknown(0x%02x)", cmdType)
	}
}
