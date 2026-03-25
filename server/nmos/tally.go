package nmos

import "sync"

// TallyEvent represents a tally state change for an NMOS source (IS-07).
type TallyEvent struct {
	SourceID string `json:"source_id"`
	Program  bool   `json:"program"`
	Preview  bool   `json:"preview"`
}

// TallyPublisher tracks program/preview tally state and emits events on change.
type TallyPublisher struct {
	mu       sync.Mutex
	program  string
	preview  string
	onChange func(TallyEvent)
}

// NewTallyPublisher creates a TallyPublisher that calls onChange on every state transition.
// onChange may be nil; events are silently dropped in that case.
func NewTallyPublisher(onChange func(TallyEvent)) *TallyPublisher {
	return &TallyPublisher{onChange: onChange}
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
		tp.emit(TallyEvent{
			SourceID: prev,
			Program:  false,
			Preview:  tp.preview == prev,
		})
	}

	if sourceID != "" {
		tp.emit(TallyEvent{
			SourceID: sourceID,
			Program:  true,
			Preview:  tp.preview == sourceID,
		})
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
		tp.emit(TallyEvent{
			SourceID: prev,
			Program:  tp.program == prev,
			Preview:  false,
		})
	}

	if sourceID != "" {
		tp.emit(TallyEvent{
			SourceID: sourceID,
			Program:  tp.program == sourceID,
			Preview:  true,
		})
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

// emit calls onChange if non-nil. Must be called with mu held.
func (tp *TallyPublisher) emit(event TallyEvent) {
	if tp.onChange != nil {
		tp.onChange(event)
	}
}
