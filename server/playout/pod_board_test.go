package playout

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeSCTE35Injector records pod cue injections.
type fakeSCTE35Injector struct {
	mu     sync.Mutex
	calls  []fakeCueCall
	nextID uint32
}

type fakeCueCall struct {
	IsOut      bool
	AutoReturn bool
	DurationMs int64
}

func (f *fakeSCTE35Injector) InjectPodCue(isOut bool, autoReturn bool, durationMs int64) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.calls = append(f.calls, fakeCueCall{isOut, autoReturn, durationMs})
	return f.nextID, nil
}

func (f *fakeSCTE35Injector) getCalls() []fakeCueCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]fakeCueCall, len(f.calls))
	copy(cp, f.calls)
	return cp
}

func TestPodBoard_CreatePod(t *testing.T) {
	pb := NewPodBoard(1)
	pod, err := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   180000,
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     PriorityNormal,
	})
	if err != nil {
		t.Fatalf("CreatePod: %v", err)
	}
	if pod.ID == "" {
		t.Fatal("pod ID should not be empty")
	}
	if pod.State != PodScheduled {
		t.Errorf("state = %v, want scheduled", pod.State)
	}
	if pod.Armed {
		t.Error("new pod should not be armed")
	}

	pods := pb.ListPods()
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
}

func TestPodBoard_CreateFromTemplate(t *testing.T) {
	store := NewPodTemplateStore()
	pb := NewPodBoard(1)
	pb.SetTemplateStore(store)

	pod, err := pb.CreateFromTemplate("quarter")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if pod.Config.Type != "quarter" {
		t.Errorf("type = %q, want quarter", pod.Config.Type)
	}
	if pod.Config.DurationMs != 180000 {
		t.Errorf("duration = %d, want 180000", pod.Config.DurationMs)
	}
}

func TestPodBoard_CreateFromTemplate_NotFound(t *testing.T) {
	store := NewPodTemplateStore()
	pb := NewPodBoard(1)
	pb.SetTemplateStore(store)

	_, err := pb.CreateFromTemplate("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent template")
	}
}

func TestPodBoard_ArmDisarm(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.Arm(pod.ID); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if !got.Armed {
		t.Error("pod should be armed")
	}

	if err := pb.Disarm(pod.ID); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	got, _ = pb.GetPod(pod.ID)
	if got.Armed {
		t.Error("pod should be disarmed")
	}
}

func TestPodBoard_ArmNotFound(t *testing.T) {
	pb := NewPodBoard(1)
	if err := pb.Arm("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestPodBoard_Delete(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.DeletePod(pod.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(pb.ListPods()) != 0 {
		t.Fatal("expected 0 pods after delete")
	}
}

func TestPodBoard_DeleteNotFound(t *testing.T) {
	pb := NewPodBoard(1)
	if err := pb.DeletePod("nonexistent"); err == nil {
		t.Fatal("expected error deleting nonexistent pod")
	}
}

func TestPodBoard_StateChangeCallback(t *testing.T) {
	pb := NewPodBoard(1)
	var called atomic.Int32
	pb.SetOnStateChange(func() { called.Add(1) })

	pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	if called.Load() < 1 {
		t.Error("onStateChange should be called on create")
	}
}

func TestPodBoard_GetPod(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	got, err := pb.GetPod(pod.ID)
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}
	if got.ID != pod.ID {
		t.Errorf("ID = %q, want %q", got.ID, pod.ID)
	}

	_, err = pb.GetPod("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestPodBoard_Move(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Position: "item-1"})

	if err := pb.Move(pod.ID, "item-5"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if got.Config.Position != "item-5" {
		t.Errorf("position = %q, want %q", got.Config.Position, "item-5")
	}
}

func TestPodBoard_MoveTerminalFails(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	pb.mu.Lock()
	pb.pods[pod.ID].State = PodRejoined
	pb.mu.Unlock()

	if err := pb.Move(pod.ID, "item-5"); err != ErrPodTerminal {
		t.Fatalf("expected ErrPodTerminal, got %v", err)
	}
}

func TestPodBoard_Swap(t *testing.T) {
	pb := NewPodBoard(1)
	p1, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Position: "item-1"})
	p2, _ := pb.CreatePod(PodConfig{Type: "timeout", DurationMs: 60000, Position: "item-5"})

	if err := pb.Swap(p1.ID, p2.ID); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	got1, _ := pb.GetPod(p1.ID)
	got2, _ := pb.GetPod(p2.ID)
	if got1.Config.Position != "item-5" {
		t.Errorf("p1 position = %q, want item-5", got1.Config.Position)
	}
	if got2.Config.Position != "item-1" {
		t.Errorf("p2 position = %q, want item-1", got2.Config.Position)
	}
}

func TestPodBoard_Resize(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.Resize(pod.ID, 240000); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if got.Config.DurationMs != 240000 {
		t.Errorf("duration = %d, want 240000", got.Config.DurationMs)
	}
}

func TestPodBoard_ResizeNegativeFails(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	if err := pb.Resize(pod.ID, -1); err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestPodBoard_Defer(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Position: "item-3"})

	if err := pb.Defer(pod.ID, "item-7"); err != nil {
		t.Fatalf("Defer: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if got.Config.Position != "item-7" {
		t.Errorf("position = %q, want item-7", got.Config.Position)
	}
}

func TestPodBoard_PromoteDemote(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Priority: PriorityNormal})

	if err := pb.Promote(pod.ID); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	got, _ := pb.GetPod(pod.ID)
	if got.Config.Priority != PriorityHigh {
		t.Errorf("priority = %v, want high", got.Config.Priority)
	}

	if err := pb.Promote(pod.ID); err != nil {
		t.Fatalf("Promote again: %v", err)
	}
	got, _ = pb.GetPod(pod.ID)
	if got.Config.Priority != PriorityMandatory {
		t.Errorf("priority = %v, want mandatory", got.Config.Priority)
	}

	// Promote at max is no-op.
	if err := pb.Promote(pod.ID); err != nil {
		t.Fatalf("Promote at max: %v", err)
	}
	got, _ = pb.GetPod(pod.ID)
	if got.Config.Priority != PriorityMandatory {
		t.Errorf("priority should stay mandatory")
	}

	if err := pb.Demote(pod.ID); err != nil {
		t.Fatalf("Demote: %v", err)
	}
	got, _ = pb.GetPod(pod.ID)
	if got.Config.Priority != PriorityHigh {
		t.Errorf("priority = %v, want high", got.Config.Priority)
	}
}

func TestPodBoard_Status(t *testing.T) {
	pb := NewPodBoard(1)
	pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	pod2, _ := pb.CreatePod(PodConfig{Type: "timeout", DurationMs: 60000})
	pb.Arm(pod2.ID)

	status := pb.Status()
	if len(status.Pods) != 2 {
		t.Fatalf("expected 2 pods in status, got %d", len(status.Pods))
	}
	if status.PodsArmed != 1 {
		t.Errorf("armed = %d, want 1", status.PodsArmed)
	}
	if status.ActivePod != nil {
		t.Error("no pod should be active")
	}
}

func TestPodBoard_Fire(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	var breakStarted atomic.Int32
	var breakEnded atomic.Int32
	pb.SetOnBreakStart(func() { breakStarted.Add(1) })
	pb.SetOnBreakEnd(func() { breakEnded.Add(1) })

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "timeout",
		DurationMs:   100, // 100ms for fast test
		AutoReturn:   true,
		AllowDumpOut: true,
		Priority:     PriorityNormal,
	})
	pb.Arm(pod.ID)

	if err := pb.Fire(pod.ID); err != nil {
		t.Fatalf("Fire: %v", err)
	}

	// Should have SCTE-35 cue-out.
	waitFor(t, time.Second, func() bool {
		return len(inj.getCalls()) >= 1
	})
	calls := inj.getCalls()
	if !calls[0].IsOut {
		t.Error("first cue should be out")
	}
	if !calls[0].AutoReturn {
		t.Error("cue should have auto_return")
	}

	// Should be in break_active.
	got, _ := pb.GetPod(pod.ID)
	if got.State != PodBreakActive {
		t.Errorf("state = %v, want break_active", got.State)
	}

	// onBreakStart should have been called.
	if breakStarted.Load() < 1 {
		t.Error("onBreakStart should have been called")
	}

	// Wait for auto-return (100ms duration + margin).
	waitFor(t, 2*time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State == PodRejoined
	})

	// onBreakEnd should have been called.
	if breakEnded.Load() < 1 {
		t.Error("onBreakEnd should have been called")
	}

	// Should have SCTE-35 cue-in.
	waitFor(t, time.Second, func() bool {
		return len(inj.getCalls()) >= 2
	})
	calls = inj.getCalls()
	if calls[1].IsOut {
		t.Error("second cue should be in (return)")
	}
}

func TestPodBoard_FireNotArmed(t *testing.T) {
	pb := NewPodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})

	err := pb.Fire(pod.ID)
	if err != ErrPodNotArmed {
		t.Fatalf("expected ErrPodNotArmed, got %v", err)
	}
}

func TestPodBoard_FireAlreadyActive(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	p1, _ := pb.CreatePod(PodConfig{Type: "injury", DurationMs: 0, HoldUntilManualReturn: true})
	pb.Arm(p1.ID)
	pb.Fire(p1.ID)

	p2, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	pb.Arm(p2.ID)

	err := pb.Fire(p2.ID)
	if err != ErrPodAlreadyActive {
		t.Fatalf("expected ErrPodAlreadyActive, got %v", err)
	}

	// Cleanup: force terminal so goroutine stops.
	pb.mu.Lock()
	if p, ok := pb.pods[p1.ID]; ok {
		p.State = PodDumpedOut
		p.CompletedAt = time.Now().UnixMilli()
	}
	pb.activePodID = ""
	pb.mu.Unlock()
}

func TestPodBoard_InjuryPodNoAutoReturn(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	pod, _ := pb.CreatePod(PodConfig{
		Type:                  "injury",
		DurationMs:            0,
		AutoReturn:            false,
		AllowDumpOut:          true,
		HoldUntilManualReturn: true,
	})
	pb.Arm(pod.ID)
	pb.Fire(pod.ID)

	// Should stay in break_active (no auto-return timer).
	time.Sleep(50 * time.Millisecond)
	got, _ := pb.GetPod(pod.ID)
	if got.State != PodBreakActive {
		t.Errorf("injury pod state = %v, want break_active (should hold)", got.State)
	}

	// Cleanup: force terminal.
	pb.mu.Lock()
	if p, ok := pb.pods[pod.ID]; ok {
		p.State = PodDumpedOut
		p.CompletedAt = time.Now().UnixMilli()
	}
	pb.activePodID = ""
	pb.mu.Unlock()
}
