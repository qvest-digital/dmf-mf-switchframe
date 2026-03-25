package playout

import (
	"errors"
	"sync"
)

// ErrSpliceAlreadyActive is returned when BeginSplice is called while a splice is in progress.
var ErrSpliceAlreadyActive = errors.New("playout: splice already active")

// spliceScheduleInjector extends podSCTE35Injector with scheduled cue support.
type spliceScheduleInjector interface {
	podSCTE35Injector
	SchedulePodCue(isOut, autoReturn bool, durationMs, preRollMs int64) (uint32, error)
}

// SpliceCoordinatorConfig configures the splice coordinator.
type SpliceCoordinatorConfig struct {
	Injector      spliceScheduleInjector
	BlackDetector *BlackDetector
	PreRollMs     int64 // default: 5000
	WindowMs      int64 // default: 500
	TimeoutMs     int64 // default: 2000
}

// SpliceState represents the state of a splice operation.
type SpliceState int

const (
	SpliceIdle     SpliceState = 0
	SpliceScanning SpliceState = 1
	SpliceLocked   SpliceState = 2
	SpliceComplete SpliceState = 3
)

// String returns the string representation of a SpliceState.
func (s SpliceState) String() string {
	switch s {
	case SpliceIdle:
		return "idle"
	case SpliceScanning:
		return "scanning"
	case SpliceLocked:
		return "locked"
	case SpliceComplete:
		return "complete"
	default:
		return "unknown"
	}
}

// SpliceOperation tracks a single splice-point search operation.
type SpliceOperation struct {
	PodID          string
	IsOut          bool
	AutoReturn     bool
	DurationMs     int64
	TargetPTS      int64
	WindowStartPTS int64
	WindowEndPTS   int64
	State          SpliceState
	LockedPTS      int64
	SpliceReason   SpliceReason
	EventID        uint32
	candidates     []CandidateFrame
	frameIdx       int
}

// SpliceCoordinator manages splice-point detection between PodBoard and SCTE-35 injector.
type SpliceCoordinator struct {
	mu            sync.Mutex
	config        SpliceCoordinatorConfig
	active        *SpliceOperation
	onSpliceReady func(podID string, pts int64)
	preRollMs     int64
	windowMs      int64
	timeoutMs     int64
}

// NewSpliceCoordinator creates a SpliceCoordinator with the given config.
// Zero-value timing fields are replaced with defaults.
func NewSpliceCoordinator(cfg SpliceCoordinatorConfig) *SpliceCoordinator {
	preRoll := cfg.PreRollMs
	if preRoll == 0 {
		preRoll = 5000
	}
	window := cfg.WindowMs
	if window == 0 {
		window = 500
	}
	timeout := cfg.TimeoutMs
	if timeout == 0 {
		timeout = 2000
	}
	return &SpliceCoordinator{
		config:    cfg,
		preRollMs: preRoll,
		windowMs:  window,
		timeoutMs: timeout,
	}
}

// Active returns true if a splice operation is in progress (scanning or locked).
func (sc *SpliceCoordinator) Active() bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.active != nil && (sc.active.State == SpliceScanning || sc.active.State == SpliceLocked)
}

// SetOnSpliceReady registers a callback that fires when a splice point is finalized.
func (sc *SpliceCoordinator) SetOnSpliceReady(fn func(podID string, pts int64)) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.onSpliceReady = fn
}
