package switcher

import (
	"math"
	"time"

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
	videoPTSStart         int64
	videoPTS              int64
	videoPTSInited        bool
	wallClockVideoEnabled bool
	firstVideoPTSSeeded   bool
	videoPTSEpoch         time.Time
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

// publishSourcesSnapshot mirrors s.sources to s.sourcesAtomic. Caller must
// hold s.configMu. Phase-1 only — phase 5 moves the map to atomic-primary
// and this helper goes away.
func (s *Switcher) publishSourcesSnapshot() {
	snap := make(map[string]*sourceState, len(s.sources))
	for k, v := range s.sources {
		snap[k] = v
	}
	s.sourcesAtomic.Store(&snap)
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
	empty := map[string]*sourceState{}
	s.sourcesAtomic.Store(&empty)
	s.ftbDurationMsAtomic.Store(int64(s.ftbDurationMs))
	s.storeTransitionAbortThresholdAtomic(s.transitionAbortThreshold)
}
