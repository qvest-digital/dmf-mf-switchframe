package switcher

import (
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
	programSource         string
	previewSource         string
	state                 State
	transTypeOrig         string
	transitionMaxForward  float64
	wallClockVideoEnabled bool
	transEngine           *transition.Engine
	onFirstVideoPTS       func(pts int64)
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

func (s *Switcher) updateFrameSyncBundle(fn func(*frameSyncBundle)) {
	var next frameSyncBundle
	if old := s.frameSyncBundle.Load(); old != nil {
		next = *old
	}
	fn(&next)
	s.frameSyncBundle.Store(&next)
}

// withSourcesMut CoW-updates the atomic sources map. fn receives a fresh
// shallow copy of the current map; mutations applied to fn's parameter are
// published atomically via sourcesAtomic.Store when fn returns. The CoW
// new-map-pointer swap is the atomic unit of visibility for lock-free
// readers. The legacy s.sources field is kept as a read-through mirror for
// Phase 5; Phase 6 deletes it entirely.
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
	// Legacy mirror — still read by any sites not yet migrated in this
	// phase. Writes go here via this assignment so every future read of
	// s.sources observes the post-fn state. Deleted in Phase 6.
	s.sources = next
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
	s.frameSyncBundle.Store(&frameSyncBundle{delayBuffer: s.delayBuffer})
	// Phase 5: sourcesAtomic is the primary map. s.sources remains a
	// read-through mirror; withSourcesMut keeps both in sync on every CoW
	// mutation. Seed both pointers to a fresh empty map so a reader's
	// *sourcesAtomic.Load() is never nil even before the first Register.
	empty := map[string]*sourceState{}
	s.sources = empty
	s.sourcesAtomic.Store(&empty)
	s.ftbDurationMsAtomic.Store(int64(s.ftbDurationMs))
	s.storeTransitionAbortThresholdAtomic(s.transitionAbortThreshold)
}
