package switcher

import (
	"sync"
	"sync/atomic"

	"math"

	"github.com/zsiec/switchframe/server/transition"
)

// hotState bundles Switcher fields that are read together on the per-frame
// hot path and mutated together by Cut, StartTransition, FadeToBlack, and
// handleTransitionComplete. Published via atomic.Pointer[hotState] using
// copy-on-write: load the current pointer, copy the struct, mutate the copy,
// Store the new pointer. The struct is immutable once published — never
// mutate fields on a pointer returned by Load.
type hotState struct {
	programSource        string
	previewSource        string
	state                State
	transTypeOrig        string
	transitionMaxForward float64
	transEngine          *transition.Engine
	// programCut is the last cut made while the sync loop runs. The tick
	// whose snapshot first carries it hands it to the mixer, before its
	// audio, so the cut's crossfade starts on the first tick that shows
	// the cut.
	programCut *programCut
}

// programCut is a cut for the mixer: the source that aired before it, the
// source it puts on program, and the transitions it aborted, whose audio
// is released after it.
type programCut struct {
	from, to string
	handler  audioCutHandler
	aborts   []transitionAbort
	// once hands the cut over exactly once, to whoever comes first: the
	// tick, a transition starting, the clock stopping, or a later cut
	// that takes it over. Whoever comes second waits until the hand-over
	// is complete.
	once  sync.Once
	taken atomic.Bool // set once the cut is with the mixer or taken over
}

// take hands the cut to the mixer, unless that has happened already, and
// returns once the mixer has it.
func (pc *programCut) take() {
	pc.once.Do(func() {
		pc.apply()
		pc.taken.Store(true)
	})
}

// claim takes the cut over for a later cut, which hands it to the mixer
// in its place. It reports false when the cut is with the mixer already.
func (pc *programCut) claim() bool {
	claimed := false
	pc.once.Do(func() {
		claimed = true
		pc.taken.Store(true)
	})
	return claimed
}

// transitionAbort is an aborted transition's audio, released with the
// token its OnTransitionStart returned.
type transitionAbort struct {
	handler audioTransitionHandler
	token   uint64
}

// updateHotState publishes a new hotState snapshot via copy-on-write. The
// caller must hold s.configMu to serialize writers; readers call
// s.hotState.Load() without any lock. Lazy-initializes to zero value if the
// atomic slot has not been seeded — this keeps lightweight test fixtures
// that skip initAtomicSlots working.
func (s *Switcher) updateHotState(fn func(*hotState)) {
	var next hotState
	if old := s.hotState.Load(); old != nil {
		next = *old
	}
	fn(&next)
	s.hotState.Store(&next)
}

func (s *Switcher) updateGraphicsHandlers(fn func(*graphicsHandlers)) {
	var next graphicsHandlers
	if old := s.graphicsHandlers.Load(); old != nil {
		next = *old
	}
	fn(&next)
	s.graphicsHandlers.Store(&next)
}

func (s *Switcher) updateAudioHandlers(fn func(*audioHandlers)) {
	var next audioHandlers
	if old := s.audioHandlers.Load(); old != nil {
		next = *old
	}
	fn(&next)
	s.audioHandlers.Store(&next)
}

// withSourcesMut CoW-updates the atomic sources map. fn receives a fresh
// shallow copy of the current map; mutations applied to fn's parameter are
// published atomically via sourcesAtomic.Store when fn returns. The CoW
// new-map-pointer swap is the atomic unit of visibility for lock-free
// readers.
//
// Caller must hold s.configMu.Lock for writer serialization. Reads via
// *s.sourcesAtomic.Load() do not take the lock.
func (s *Switcher) withSourcesMut(fn func(m map[string]*sourceState)) {
	old := *s.sourcesAtomic.Load()
	next := make(map[string]*sourceState, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	fn(next)
	s.sourcesAtomic.Store(&next)
}

// storeTransitionAbortThreshold bit-packs a float64 into the uint64 atomic.
func (s *Switcher) storeTransitionAbortThresholdAtomic(v float64) {
	s.transitionAbortThresholdAtomic.Store(math.Float64bits(v))
}

// initAtomicSlots seeds every atomic.Pointer and scalar-atomic slot to the
// legacy field's initial value. Called from New() and the test helpers so
// that hot-path Load()s never see a nil pointer. Must be invoked AFTER
// s.delayBuffer is constructed and the legacy scalar fields are populated.
func (s *Switcher) initAtomicSlots() {
	s.hotState.Store(&hotState{state: StateIdle})
	s.graphicsHandlers.Store(&graphicsHandlers{})
	s.audioHandlers.Store(&audioHandlers{})
	// Seed sourcesAtomic to an empty map so *sourcesAtomic.Load() is never
	// nil before the first Register call.
	empty := map[string]*sourceState{}
	s.sourcesAtomic.Store(&empty)
	s.ftbDurationMsAtomic.Store(int64(s.ftbDurationMs))
	s.storeTransitionAbortThresholdAtomic(s.transitionAbortThreshold)
}
