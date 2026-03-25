package playout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// PodBoard manages all pods for a single playout channel.
type PodBoard struct {
	channelID int
	mu        sync.Mutex
	pods      map[string]*Pod

	templateStore *PodTemplateStore
	onStateChange func()
	onBreakStart  func()
	onBreakEnd    func()

	scte35Injector    podSCTE35Injector
	spliceCoordinator *SpliceCoordinator
	ptsProvider       func() int64

	activePodID       string
	autoReturnCancels map[string]context.CancelFunc
	trackCancels      map[string]context.CancelFunc
}

// podSCTE35Injector is the narrow interface for SCTE-35 injection.
type podSCTE35Injector interface {
	InjectPodCue(isOut bool, autoReturn bool, durationMs int64) (uint32, error)
}

// NewPodBoard creates a pod board for the given channel.
func NewPodBoard(channelID int) *PodBoard {
	return &PodBoard{
		channelID:         channelID,
		pods:              make(map[string]*Pod),
		autoReturnCancels: make(map[string]context.CancelFunc),
		trackCancels:      make(map[string]context.CancelFunc),
	}
}

// SetTemplateStore sets the pod template store.
func (pb *PodBoard) SetTemplateStore(store *PodTemplateStore) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.templateStore = store
}

// SetOnStateChange registers a callback for pod state mutations.
func (pb *PodBoard) SetOnStateChange(fn func()) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.onStateChange = fn
}

// SetOnBreakStart registers a callback when a pod fires (enter ModeBreak).
func (pb *PodBoard) SetOnBreakStart(fn func()) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.onBreakStart = fn
}

// SetOnBreakEnd registers a callback when a pod completes (exit ModeBreak).
func (pb *PodBoard) SetOnBreakEnd(fn func()) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.onBreakEnd = fn
}

// SetSCTE35Injector sets the SCTE-35 injection interface.
func (pb *PodBoard) SetSCTE35Injector(inj podSCTE35Injector) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.scte35Injector = inj
}

// SetSpliceCoordinator sets the splice coordinator for frame-accurate break entry.
func (pb *PodBoard) SetSpliceCoordinator(sc *SpliceCoordinator) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.spliceCoordinator = sc
}

// SetPTSProvider sets the function that returns the current program PTS.
func (pb *PodBoard) SetPTSProvider(fn func() int64) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.ptsProvider = fn
}

// CreatePod creates a new pod with the given config.
func (pb *PodBoard) CreatePod(config PodConfig) (*Pod, error) {
	pb.mu.Lock()
	id := generatePodID()
	for _, exists := pb.pods[id]; exists; _, exists = pb.pods[id] {
		id = generatePodID()
	}

	pod := &Pod{
		ID:     id,
		Config: config,
		State:  PodScheduled,
	}
	pb.pods[id] = pod
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return pod, nil
}

// CreateFromTemplate creates a pod from a named template.
func (pb *PodBoard) CreateFromTemplate(typeName string) (*Pod, error) {
	pb.mu.Lock()
	store := pb.templateStore
	pb.mu.Unlock()

	if store == nil {
		return nil, ErrNoPodBoard
	}

	tmpl, err := store.Get(typeName)
	if err != nil {
		return nil, err
	}

	// Generate ID and use template's Instantiate.
	id := generatePodID()
	pod := tmpl.Instantiate(id)

	pb.mu.Lock()
	// Check for ID collision.
	for _, exists := pb.pods[id]; exists; _, exists = pb.pods[id] {
		id = generatePodID()
		pod.ID = id
	}
	pb.pods[id] = pod
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return pod, nil
}

// GetPod returns a snapshot of the pod with the given ID.
func (pb *PodBoard) GetPod(id string) (*Pod, error) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pod, ok := pb.pods[id]
	if !ok {
		return nil, ErrPodNotFound
	}
	cp := *pod
	return &cp, nil
}

// DeletePod removes a pod. Active pods cannot be deleted.
func (pb *PodBoard) DeletePod(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pb.activePodID == id && (pod.State == PodBreakActive || pod.State == PodPreRoll) {
		pb.mu.Unlock()
		return ErrPodAlreadyActive
	}
	delete(pb.pods, id)
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Arm sets a pod's armed flag.
func (pb *PodBoard) Arm(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	pod.Armed = true
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Disarm clears a pod's armed flag.
func (pb *PodBoard) Disarm(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	pod.Armed = false
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// ListPods returns snapshots of all pods.
func (pb *PodBoard) ListPods() []Pod {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	out := make([]Pod, 0, len(pb.pods))
	for _, p := range pb.pods {
		out = append(out, *p)
	}
	return out
}

// Status returns the broadcast-safe pod board status.
func (pb *PodBoard) Status() PodBoardStatus {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	status := PodBoardStatus{
		Pods: make([]PodStatus, 0, len(pb.pods)),
	}

	for _, p := range pb.pods {
		ps := PodStatus{
			ID:          p.ID,
			Type:        p.Config.Type,
			State:       p.State.String(),
			Armed:       p.Armed,
			ElapsedMs:   p.ElapsedMs,
			RemainingMs: p.RemainingMs,
			DurationMs:  p.Config.DurationMs,
			Priority:    p.Config.Priority.String(),
			Position:    p.Config.Position,
		}
		status.Pods = append(status.Pods, ps)

		if p.Armed {
			status.PodsArmed++
		}
		if p.State.IsTerminal() {
			status.PodsCompleted++
		}
		if pb.activePodID == p.ID && p.State == PodBreakActive {
			psCopy := ps
			status.ActivePod = &psCopy
		}
	}

	return status
}

// Move reassigns a pod to a new playlist position.
func (pb *PodBoard) Move(id, newPosition string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	pod.Config.Position = newPosition
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Swap exchanges positions of two pods.
func (pb *PodBoard) Swap(id1, id2 string) error {
	pb.mu.Lock()
	p1, ok1 := pb.pods[id1]
	p2, ok2 := pb.pods[id2]
	if !ok1 || !ok2 {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if p1.State.IsTerminal() || p2.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	p1.Config.Position, p2.Config.Position = p2.Config.Position, p1.Config.Position
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Resize changes the duration of a pod.
func (pb *PodBoard) Resize(id string, newDurationMs int64) error {
	if newDurationMs < 0 {
		return fmt.Errorf("playout: pod duration must be non-negative")
	}

	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	pod.Config.DurationMs = newDurationMs
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Defer pushes a pod to a new position.
func (pb *PodBoard) Defer(id, newPosition string) error {
	return pb.Move(id, newPosition)
}

// Promote increases a pod's priority by one level.
func (pb *PodBoard) Promote(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	if pod.Config.Priority < PriorityMandatory {
		pod.Config.Priority++
	}
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Demote decreases a pod's priority by one level.
func (pb *PodBoard) Demote(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	if pod.Config.Priority > PriorityNormal {
		pod.Config.Priority--
	}
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Fire activates an armed pod: transitions to BREAK-ACTIVE, injects SCTE-35
// cue-out, starts the auto-return timer if configured.
//
// When a SpliceCoordinator is configured, Fire transitions to PodPreRoll instead
// of PodBreakActive and delegates splice-point detection to the coordinator. The
// onSpliceReady callback transitions the pod to PodBreakActive and starts the
// auto-return timer.
func (pb *PodBoard) Fire(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if !pod.Armed {
		pb.mu.Unlock()
		return ErrPodNotArmed
	}
	if pod.State.IsTerminal() {
		pb.mu.Unlock()
		return ErrPodTerminal
	}
	if pb.activePodID != "" {
		pb.mu.Unlock()
		return ErrPodAlreadyActive
	}

	sc := pb.spliceCoordinator
	ptsProvider := pb.ptsProvider
	inj := pb.scte35Injector
	breakStartFn := pb.onBreakStart
	stateChangeFn := pb.onStateChange
	config := pod.Config

	if sc != nil && ptsProvider != nil {
		// Splice coordinator path: PreRoll first, break later.
		pod.State = PodPreRoll
		pod.FiredAt = time.Now().UnixMilli()
		pb.activePodID = id
		pb.mu.Unlock()

		// Wire the onSpliceReady callback to transition pod to break_active.
		sc.SetOnSpliceReady(func(podID string, pts int64) {
			pb.onSpliceReady(podID, pts)
		})

		currentPTS := ptsProvider()
		err := sc.BeginSplice(id, true, config.AutoReturn, config.DurationMs, currentPTS)
		if err != nil {
			slog.Warn("playout: splice BeginSplice failed, falling back to immediate", "pod", id, "error", err)
			// Fall back: transition directly to break_active.
			pb.mu.Lock()
			if p, ok := pb.pods[id]; ok {
				p.State = PodBreakActive
			}
			pb.mu.Unlock()
			pb.fireImmediate(id, inj, config)
		}

		if breakStartFn != nil {
			breakStartFn()
		}
		if stateChangeFn != nil {
			stateChangeFn()
		}

		// Start elapsed time tracker.
		trackCtx, trackCancel := context.WithCancel(context.Background())
		pb.mu.Lock()
		pb.trackCancels[id] = trackCancel
		pb.mu.Unlock()
		go pb.trackElapsed(trackCtx, id)

		return nil
	}

	// Legacy path: immediate break activation.
	pod.State = PodBreakActive
	pod.FiredAt = time.Now().UnixMilli()
	pb.activePodID = id
	pb.mu.Unlock()

	pb.fireImmediate(id, inj, config)

	if breakStartFn != nil {
		breakStartFn()
	}
	if stateChangeFn != nil {
		stateChangeFn()
	}

	// Start auto-return goroutine if configured.
	if config.AutoReturn && config.DurationMs > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		pb.mu.Lock()
		pb.autoReturnCancels[id] = cancel
		pb.mu.Unlock()
		go pb.runAutoReturn(ctx, id, config.DurationMs)
	}

	// Start elapsed time tracker.
	trackCtx, trackCancel := context.WithCancel(context.Background())
	pb.mu.Lock()
	pb.trackCancels[id] = trackCancel
	pb.mu.Unlock()
	go pb.trackElapsed(trackCtx, id)

	return nil
}

// fireImmediate injects the SCTE-35 cue-out immediately (legacy path / fallback).
func (pb *PodBoard) fireImmediate(id string, inj podSCTE35Injector, config PodConfig) {
	if inj != nil {
		eventID, err := inj.InjectPodCue(true, config.AutoReturn, config.DurationMs)
		if err != nil {
			slog.Warn("playout: pod SCTE-35 cue-out failed", "pod", id, "error", err)
		} else {
			pb.mu.Lock()
			if p, ok := pb.pods[id]; ok {
				p.SCTE35EventID = eventID
			}
			pb.mu.Unlock()
		}
	}
}

// onSpliceReady is called by the SpliceCoordinator when a splice point is locked.
// It transitions the pod from PodPreRoll to PodBreakActive and starts the auto-return timer.
func (pb *PodBoard) onSpliceReady(podID string, pts int64) {
	pb.mu.Lock()
	pod, ok := pb.pods[podID]
	if !ok || pod.State != PodPreRoll {
		pb.mu.Unlock()
		return
	}

	pod.State = PodBreakActive
	config := pod.Config
	stateChangeFn := pb.onStateChange
	pb.mu.Unlock()

	// Start auto-return goroutine if configured.
	if config.AutoReturn && config.DurationMs > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		pb.mu.Lock()
		pb.autoReturnCancels[podID] = cancel
		pb.mu.Unlock()
		go pb.runAutoReturn(ctx, podID, config.DurationMs)
	}

	if stateChangeFn != nil {
		stateChangeFn()
	}
}

// trackElapsed updates ElapsedMs and RemainingMs every 50ms while a pod is active.
func (pb *PodBoard) trackElapsed(ctx context.Context, id string) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pb.mu.Lock()
		pod, ok := pb.pods[id]
		if !ok || (pod.State != PodBreakActive && pod.State != PodPreRoll) {
			pb.mu.Unlock()
			return
		}
		elapsed := time.Now().UnixMilli() - pod.FiredAt
		pod.ElapsedMs = elapsed
		if pod.Config.DurationMs > 0 {
			remain := pod.Config.DurationMs - elapsed
			if remain < 0 {
				remain = 0
			}
			pod.RemainingMs = remain
		}
		pb.mu.Unlock()
	}
}

// runAutoReturn waits for the pod duration, then completes the break.
func (pb *PodBoard) runAutoReturn(ctx context.Context, id string, durationMs int64) {
	timer := time.NewTimer(time.Duration(durationMs) * time.Millisecond)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok || pod.State != PodBreakActive {
		pb.mu.Unlock()
		return
	}
	delete(pb.autoReturnCancels, id)
	pb.mu.Unlock()

	pb.completePod(id, PodRejoined)
}

// completePod transitions a pod to a terminal state, injects cue-in, calls callbacks.
func (pb *PodBoard) completePod(id string, finalState PodState) {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return
	}
	if pod.State != PodBreakActive {
		pb.mu.Unlock()
		return
	}

	pod.State = finalState
	pod.CompletedAt = time.Now().UnixMilli()
	pod.ElapsedMs = pod.CompletedAt - pod.FiredAt
	if pod.Config.DurationMs > 0 {
		pod.RemainingMs = 0
	}
	pb.activePodID = ""

	// Cancel auto-return timer if still running.
	if cancelFn, ok := pb.autoReturnCancels[id]; ok {
		cancelFn()
		delete(pb.autoReturnCancels, id)
	}

	// Cancel elapsed tracker goroutine.
	if cancelFn, ok := pb.trackCancels[id]; ok {
		cancelFn()
		delete(pb.trackCancels, id)
	}

	inj := pb.scte35Injector
	breakEndFn := pb.onBreakEnd
	stateChangeFn := pb.onStateChange
	pb.mu.Unlock()

	// Inject SCTE-35 cue-in (return to program).
	if inj != nil {
		_, err := inj.InjectPodCue(false, false, 0)
		if err != nil {
			slog.Warn("playout: pod SCTE-35 cue-in failed", "pod", id, "error", err)
		}
	}

	if breakEndFn != nil {
		breakEndFn()
	}
	if stateChangeFn != nil {
		stateChangeFn()
	}
}

// DumpOut performs an early return from break.
func (pb *PodBoard) DumpOut(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State != PodBreakActive {
		pb.mu.Unlock()
		return ErrPodNotActive
	}
	if !pod.Config.AllowDumpOut {
		pb.mu.Unlock()
		return ErrPodNoDumpOut
	}
	pb.mu.Unlock()

	pb.completePod(id, PodDumpedOut)
	return nil
}

// Hold cancels the auto-return timer, keeping the break active indefinitely.
func (pb *PodBoard) Hold(id string) error {
	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State != PodBreakActive {
		pb.mu.Unlock()
		return ErrPodNotActive
	}

	if cancelFn, ok := pb.autoReturnCancels[id]; ok {
		cancelFn()
		delete(pb.autoReturnCancels, id)
	}
	pod.Config.AutoReturn = false
	pod.Config.HoldUntilManualReturn = true
	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// Extend adds additional time to an active pod's duration and restarts the auto-return timer.
func (pb *PodBoard) Extend(id string, additionalMs int64) error {
	if additionalMs < 0 {
		return fmt.Errorf("playout: extend duration must be non-negative")
	}

	pb.mu.Lock()
	pod, ok := pb.pods[id]
	if !ok {
		pb.mu.Unlock()
		return ErrPodNotFound
	}
	if pod.State != PodBreakActive {
		pb.mu.Unlock()
		return ErrPodNotActive
	}

	// Cancel existing auto-return.
	if cancelFn, ok := pb.autoReturnCancels[id]; ok {
		cancelFn()
		delete(pb.autoReturnCancels, id)
	}

	pod.Config.DurationMs += additionalMs

	// Restart auto-return with remaining time (launched under lock to prevent TOCTOU).
	elapsed := time.Now().UnixMilli() - pod.FiredAt
	remaining := pod.Config.DurationMs - elapsed
	if remaining > 0 && pod.Config.AutoReturn {
		ctx, cancel := context.WithCancel(context.Background())
		pb.autoReturnCancels[id] = cancel
		go pb.runAutoReturn(ctx, id, remaining)
	}

	fn := pb.onStateChange
	pb.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// HighestPriorityArmed returns the armed pod with the highest priority, or nil.
func (pb *PodBoard) HighestPriorityArmed() *Pod {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	var best *Pod
	for _, p := range pb.pods {
		if !p.Armed || p.State.IsTerminal() || p.State == PodBreakActive {
			continue
		}
		if best == nil || best.Config.Priority.Less(p.Config.Priority) {
			cp := *p
			best = &cp
		}
	}
	return best
}

// generatePodID creates a short random hex ID.
func generatePodID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		slog.Warn("playout: random pod ID generation failed, using fallback")
		return "pod-0000"
	}
	return "pod-" + hex.EncodeToString(b)
}
