package playout

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
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
	onSpliceReady func(podID string, pts int64, preRollMs int64)
	asRunLog      func(AsRunEvent)
	preRollMs     int64
	windowMs      int64
	timeoutMs     int64
	timeoutCancel context.CancelFunc
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
// preRollMs is the remaining pre-roll from finalization to the actual splice point.
func (sc *SpliceCoordinator) SetOnSpliceReady(fn func(podID string, pts int64, preRollMs int64)) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.onSpliceReady = fn
}

// SetAsRunLog registers a callback for emitting as-run events.
func (sc *SpliceCoordinator) SetAsRunLog(fn func(AsRunEvent)) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.asRunLog = fn
}

// BeginSplice starts a splice-point search for the given pod.
// It calculates the target PTS based on the current PTS and pre-roll, opens a
// scan window, and transitions to SpliceScanning. Returns ErrSpliceAlreadyActive
// if a splice is already in progress.
func (sc *SpliceCoordinator) BeginSplice(podID string, isOut, autoReturn bool, durationMs, currentPTS int64) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.active != nil && (sc.active.State == SpliceScanning || sc.active.State == SpliceLocked) {
		return ErrSpliceAlreadyActive
	}

	preRollTicks := sc.preRollMs * 90 // 90kHz ticks per ms
	windowTicks := sc.windowMs * 90
	targetPTS := currentPTS + preRollTicks

	sc.active = &SpliceOperation{
		PodID:          podID,
		IsOut:          isOut,
		AutoReturn:     autoReturn,
		DurationMs:     durationMs,
		TargetPTS:      targetPTS,
		WindowStartPTS: targetPTS - windowTicks,
		WindowEndPTS:   targetPTS + windowTicks,
		State:          SpliceScanning,
	}

	// Start timeout goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	sc.timeoutCancel = cancel
	go sc.runTimeout(ctx)

	return nil
}

// IngestFrame feeds a decoded frame to the splice coordinator for analysis.
// Frames before the scan window are ignored. Frames inside the window are
// collected as candidates. A frame past the window end triggers finalization.
func (sc *SpliceCoordinator) IngestFrame(yuv []byte, w, h int, pts int64) {
	sc.mu.Lock()
	if sc.active == nil || sc.active.State != SpliceScanning {
		sc.mu.Unlock()
		return
	}

	op := sc.active

	// Before window: ignore.
	if pts < op.WindowStartPTS {
		sc.mu.Unlock()
		return
	}

	// Past window end: trigger finalization.
	if pts > op.WindowEndPTS {
		sc.mu.Unlock()
		sc.finalizeSplice()
		return
	}

	// Inside window: copy YUV and append as candidate.
	yuvCopy := make([]byte, len(yuv))
	copy(yuvCopy, yuv)
	op.candidates = append(op.candidates, CandidateFrame{
		YUV:    yuvCopy,
		PTS:    pts,
		Index:  op.frameIdx,
		Width:  w,
		Height: h,
	})
	op.frameIdx++
	sc.mu.Unlock()
}

// finalizeSplice runs the black/scene-change detector on collected candidates,
// locks the splice PTS, and calls SchedulePodCue on the injector.
func (sc *SpliceCoordinator) finalizeSplice() {
	sc.mu.Lock()
	op := sc.active
	if op == nil || op.State != SpliceScanning {
		sc.mu.Unlock()
		return
	}

	// Cancel timeout since we're finalizing.
	if sc.timeoutCancel != nil {
		sc.timeoutCancel()
		sc.timeoutCancel = nil
	}

	// Transition to SpliceLocked to prevent double-finalization, then extract
	// what we need and release the lock before the CPU-intensive analysis.
	op.State = SpliceLocked

	bd := sc.config.BlackDetector
	inj := sc.config.Injector
	candidates := op.candidates
	op.candidates = nil // free candidate refs under lock

	targetPTS := op.TargetPTS
	podID := op.PodID
	isOut := op.IsOut
	autoReturn := op.AutoReturn
	durationMs := op.DurationMs
	callbackFn := sc.onSpliceReady
	asRunFn := sc.asRunLog
	preRollTicks := sc.preRollMs * 90

	sc.mu.Unlock()

	// Run black detection / scene-change analysis outside the lock.
	var lockedPTS int64
	var reason SpliceReason

	if len(candidates) > 0 && bd != nil {
		result := bd.FindBestSpliceFrame(candidates)
		if result.Index >= 0 {
			lockedPTS = result.PTS
			reason = result.Reason
		} else {
			lockedPTS = targetPTS
			reason = SpliceReasonScheduled
		}
	} else {
		lockedPTS = targetPTS
		reason = SpliceReasonScheduled
	}

	// Calculate remaining pre-roll in ms from locked PTS.
	// originPTS is the PTS at which BeginSplice was called;
	// remainingPreRollMs may differ from configured pre-roll when the locked
	// splice point is offset from the target (window start vs window end).
	originPTS := targetPTS - preRollTicks
	remainingPreRollMs := (lockedPTS - originPTS) / 90
	if remainingPreRollMs < 0 {
		remainingPreRollMs = 0
	}

	// Store analysis results.
	sc.mu.Lock()
	if sc.active != nil {
		sc.active.LockedPTS = lockedPTS
		sc.active.SpliceReason = reason
	}
	sc.mu.Unlock()

	// Emit as-run splice_active event.
	if asRunFn != nil {
		offsetMs := float64(lockedPTS-targetPTS) / 90.0
		asRunFn(AsRunEvent{
			Timestamp:           time.Now().UTC(),
			Type:                AsRunSpliceActive,
			PodID:               podID,
			ScheduledDurationMs: durationMs,
			Reason:              string(reason),
			BlackDetect: &BlackDetectInfo{
				Found:    reason == SpliceReasonBlack,
				OffsetMs: offsetMs,
			},
		})
	}

	// Call injector outside the lock.
	if inj != nil {
		eventID, err := inj.SchedulePodCue(isOut, autoReturn, durationMs, remainingPreRollMs)
		if err != nil {
			slog.Warn("playout: splice SchedulePodCue failed", "pod", podID, "error", err)
		} else {
			sc.mu.Lock()
			if sc.active != nil {
				sc.active.EventID = eventID
			}
			sc.mu.Unlock()
		}
	}

	// Set state to complete.
	sc.mu.Lock()
	if sc.active != nil {
		sc.active.State = SpliceComplete
	}
	sc.mu.Unlock()

	// Fire callback with remaining pre-roll so the pod board can set
	// its auto-return timer to breakDuration + preRoll (not just breakDuration).
	if callbackFn != nil {
		callbackFn(podID, lockedPTS, remainingPreRollMs)
	}
}

// runTimeout waits for the configured timeout duration, then finalizes the splice
// with whatever candidates have been collected. This prevents indefinite scanning
// if no frame past the window end ever arrives.
func (sc *SpliceCoordinator) runTimeout(ctx context.Context) {
	timer := time.NewTimer(time.Duration(sc.timeoutMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	sc.mu.Lock()
	if sc.active == nil || sc.active.State != SpliceScanning {
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()

	sc.finalizeSplice()
}

// Cancel aborts the current splice operation without injecting a cue.
func (sc *SpliceCoordinator) Cancel() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.timeoutCancel != nil {
		sc.timeoutCancel()
		sc.timeoutCancel = nil
	}

	if sc.active != nil {
		sc.active.candidates = nil
		sc.active = nil
	}
}

// Reset is an alias for Cancel that resets the coordinator to idle.
func (sc *SpliceCoordinator) Reset() {
	sc.Cancel()
}
