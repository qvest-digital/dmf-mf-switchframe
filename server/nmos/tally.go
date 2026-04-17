package nmos

import "sync"

// TallyEvent represents a tally state change for an NMOS source (IS-07).
//
// Program/Preview map 1:1 to the NMOS IS-07 boolean schema. KeyFill and
// Auxiliary are internal-only fields used by Switchframe's UI tally and do
// not cross the IS-07 wire format — the IS-07 payload continues to emit only
// Program and Preview; both of those remain true independently of KeyFill.
// KeyFill indicates the source is a fill input on any enabled upstream key
// (treated as RED by the UI because it is visibly on air once keyed).
// Auxiliary indicates the source is a key/alpha input (cut signal) on an
// enabled upstream key — rendered as AMBER in the UI because it is
// contributing to the composite but is not itself visible.
type TallyEvent struct {
	SourceID string `json:"source_id"`
	Program  bool   `json:"program"`
	Preview  bool   `json:"preview"`

	// KeyFill / Auxiliary are UI-only and must NEVER appear on the IS-07 wire:
	// no json tags so a future bridge that `json.Marshal`s TallyEvent directly
	// cannot accidentally leak them. Consumers who need the fields access them
	// through struct fields; the IS-07 bridge should build its own DTO.
	KeyFill   bool `json:"-"`
	Auxiliary bool `json:"-"`
}

// TallyPublisher tracks program/preview/key-role state and emits events on change.
type TallyPublisher struct {
	mu       sync.Mutex
	program  string
	preview  string
	fills    map[string]struct{}
	alphas   map[string]struct{}
	onChange func(TallyEvent)
}

// NewTallyPublisher creates a TallyPublisher that calls onChange on every state transition.
// onChange may be nil; events are silently dropped in that case.
func NewTallyPublisher(onChange func(TallyEvent)) *TallyPublisher {
	return &TallyPublisher{
		onChange: onChange,
		fills:    make(map[string]struct{}),
		alphas:   make(map[string]struct{}),
	}
}

// SetProgram updates the program source. If the source changes, an off event is
// emitted for the previous source (preserving its preview state) and an on event
// is emitted for the new source. No-op if sourceID matches current.
func (tp *TallyPublisher) SetProgram(sourceID string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.program == sourceID {
		return
	}

	prev := tp.program
	tp.program = sourceID

	if prev != "" {
		tp.emitForLocked(prev, true)
	}

	if sourceID != "" {
		tp.emitForLocked(sourceID, true)
	}
}

// SetPreview updates the preview source. If the source changes, an off event is
// emitted for the previous source (preserving its program state) and an on event
// is emitted for the new source. No-op if sourceID matches current.
func (tp *TallyPublisher) SetPreview(sourceID string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if tp.preview == sourceID {
		return
	}

	prev := tp.preview
	tp.preview = sourceID

	if prev != "" {
		tp.emitForLocked(prev, true)
	}

	if sourceID != "" {
		tp.emitForLocked(sourceID, true)
	}
}

// SetKeyRoles replaces the set of source IDs currently acting as key fills
// and key alphas. Sources that changed role (added or removed) emit an event
// with the latest composite state; unchanged sources do not emit.
func (tp *TallyPublisher) SetKeyRoles(fills, alphas []string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	newFills := make(map[string]struct{}, len(fills))
	for _, s := range fills {
		if s != "" {
			newFills[s] = struct{}{}
		}
	}
	newAlphas := make(map[string]struct{}, len(alphas))
	for _, s := range alphas {
		if s != "" {
			newAlphas[s] = struct{}{}
		}
	}

	changed := make(map[string]struct{})
	for s := range newFills {
		if _, ok := tp.fills[s]; !ok {
			changed[s] = struct{}{}
		}
	}
	for s := range tp.fills {
		if _, ok := newFills[s]; !ok {
			changed[s] = struct{}{}
		}
	}
	for s := range newAlphas {
		if _, ok := tp.alphas[s]; !ok {
			changed[s] = struct{}{}
		}
	}
	for s := range tp.alphas {
		if _, ok := newAlphas[s]; !ok {
			changed[s] = struct{}{}
		}
	}

	tp.fills = newFills
	tp.alphas = newAlphas

	for s := range changed {
		tp.emitForLocked(s, true)
	}
}

// Program returns the current program source ID.
func (tp *TallyPublisher) Program() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.program
}

// Preview returns the current preview source ID.
func (tp *TallyPublisher) Preview() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.preview
}

// emitForLocked emits the current composite event for sourceID. When force is
// false (reserved for future optimization) the event is skipped if all role
// flags would be false. Caller must hold tp.mu.
func (tp *TallyPublisher) emitForLocked(sourceID string, force bool) {
	if sourceID == "" {
		return
	}
	event := TallyEvent{
		SourceID: sourceID,
		Program:  tp.program == sourceID,
		Preview:  tp.preview == sourceID,
	}
	if _, ok := tp.fills[sourceID]; ok {
		event.KeyFill = true
	}
	if _, ok := tp.alphas[sourceID]; ok {
		event.Auxiliary = true
	}
	if !force && !event.Program && !event.Preview && !event.KeyFill && !event.Auxiliary {
		return
	}
	tp.emit(event)
}

// emit calls onChange if non-nil. Must be called with mu held.
func (tp *TallyPublisher) emit(event TallyEvent) {
	if tp.onChange != nil {
		tp.onChange(event)
	}
}
