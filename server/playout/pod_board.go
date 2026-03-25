package playout

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
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

	scte35Injector podSCTE35Injector

	activePodID string
}

// podSCTE35Injector is the narrow interface for SCTE-35 injection.
type podSCTE35Injector interface {
	InjectPodCue(isOut bool, autoReturn bool, durationMs int64) (uint32, error)
}

// NewPodBoard creates a pod board for the given channel.
func NewPodBoard(channelID int) *PodBoard {
	return &PodBoard{
		channelID: channelID,
		pods:      make(map[string]*Pod),
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

// CreatePod creates a new pod with the given config.
func (pb *PodBoard) CreatePod(config PodConfig) (*Pod, error) {
	id := generatePodID()
	pod := &Pod{
		ID:     id,
		Config: config,
		State:  PodScheduled,
	}

	pb.mu.Lock()
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

	return pb.CreatePod(PodConfig{
		Type:                  tmpl.Type,
		DurationMs:            tmpl.DefaultDurationMs,
		AutoReturn:            tmpl.AutoReturn,
		AllowDumpOut:          tmpl.AllowDumpOut,
		Priority:              tmpl.Priority,
		Contractual:           tmpl.Contractual,
		Ephemeral:             tmpl.Ephemeral,
		HoldUntilManualReturn: tmpl.HoldUntilManualReturn,
	})
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
	if pb.activePodID == id && pod.State == PodBreakActive {
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

// generatePodID creates a short random hex ID.
func generatePodID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		slog.Warn("playout: random pod ID generation failed, using fallback")
		return "pod-0000"
	}
	return "pod-" + hex.EncodeToString(b)
}
