package switcher

import (
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
