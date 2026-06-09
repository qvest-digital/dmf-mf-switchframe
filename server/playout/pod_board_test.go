package playout

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
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

	pod, err := pb.CreateFromTemplate("standard")
	if err != nil {
		t.Fatalf("CreateFromTemplate: %v", err)
	}
	if pod.Config.Type != "standard" {
		t.Errorf("type = %q, want standard", pod.Config.Type)
	}
	if pod.Config.DurationMs != 120000 {
		t.Errorf("duration = %d, want 120000", pod.Config.DurationMs)
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

	_, _ = pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
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
	_, _ = pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	pod2, _ := pb.CreatePod(PodConfig{Type: "timeout", DurationMs: 60000})
	_ = pb.Arm(pod2.ID)

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
	_ = pb.Arm(pod.ID)

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
	_ = pb.Arm(p1.ID)
	_ = pb.Fire(p1.ID)

	p2, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	_ = pb.Arm(p2.ID)

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
	if cancel, ok := pb.trackCancels[p1.ID]; ok {
		cancel()
		delete(pb.trackCancels, p1.ID)
	}
	pb.mu.Unlock()
}

func TestPodBoard_DumpOut(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   60000,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	time.Sleep(20 * time.Millisecond)

	if err := pb.DumpOut(pod.ID); err != nil {
		t.Fatalf("DumpOut: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State == PodDumpedOut
	})
}

func TestPodBoard_DumpOutDisallowed(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "mandatory_sponsor",
		DurationMs:   60000,
		AutoReturn:   true,
		AllowDumpOut: false,
		Priority:     PriorityMandatory,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	time.Sleep(20 * time.Millisecond)

	err := pb.DumpOut(pod.ID)
	if err != ErrPodNoDumpOut {
		t.Fatalf("expected ErrPodNoDumpOut, got %v", err)
	}

	// Cleanup.
	pb.mu.Lock()
	if p, ok := pb.pods[pod.ID]; ok {
		p.State = PodRejoined
		p.CompletedAt = time.Now().UnixMilli()
	}
	pb.activePodID = ""
	if cancel, ok := pb.autoReturnCancels[pod.ID]; ok {
		cancel()
		delete(pb.autoReturnCancels, pod.ID)
	}
	if cancel, ok := pb.trackCancels[pod.ID]; ok {
		cancel()
		delete(pb.trackCancels, pod.ID)
	}
	pb.mu.Unlock()
}

func TestPodBoard_Hold(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   100,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Hold before auto-return fires.
	if err := pb.Hold(pod.ID); err != nil {
		t.Fatalf("Hold: %v", err)
	}

	// Wait past auto-return time — should NOT return.
	time.Sleep(200 * time.Millisecond)
	got, _ := pb.GetPod(pod.ID)
	if got.State != PodBreakActive {
		t.Errorf("state = %v, want break_active (held)", got.State)
	}

	// Manual dump to clean up.
	_ = pb.DumpOut(pod.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State.IsTerminal()
	})
}

func TestPodBoard_Extend(t *testing.T) {
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   100,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Extend by 5 seconds.
	if err := pb.Extend(pod.ID, 5000); err != nil {
		t.Fatalf("Extend: %v", err)
	}

	got, _ := pb.GetPod(pod.ID)
	if got.Config.DurationMs != 5100 {
		t.Errorf("duration = %d, want 5100", got.Config.DurationMs)
	}

	// Should not auto-return at 100ms since we extended.
	time.Sleep(200 * time.Millisecond)
	got, _ = pb.GetPod(pod.ID)
	if got.State != PodBreakActive {
		t.Errorf("state = %v, want break_active (extended)", got.State)
	}

	// Dump to clean up.
	_ = pb.DumpOut(pod.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State.IsTerminal()
	})
}

func TestPodBoard_DumpOutNoActivePod(t *testing.T) {
	pb := NewPodBoard(1)
	err := pb.DumpOut("nonexistent")
	if err != ErrPodNotFound {
		t.Fatalf("expected ErrPodNotFound, got %v", err)
	}
}

func TestPodBoard_ChannelModeBreakIntegration(t *testing.T) {
	ch := NewChannel(1, ChannelConfig{AllowedLocalPrefixes: []string{"/"}, AssetProvider: fakeProvider(90)})
	pb := NewPodBoard(1)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	// Wire break callbacks to channel.
	pb.SetOnBreakStart(func() { ch.TakeBreak() })
	pb.SetOnBreakEnd(func() { ch.ReturnFromBreak() })

	_ = ch.LoadPlaylist(testPlaylist(clipItem("a"), clipItem("b")))
	_ = ch.Start()
	defer ch.Stop()

	// Wait for on-air.
	waitFor(t, time.Second, func() bool { return ch.State() == StateOnAir })

	// Fire a pod.
	pod, _ := pb.CreatePod(PodConfig{
		Type:         "timeout",
		DurationMs:   100,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Channel should be in ModeBreak.
	waitFor(t, time.Second, func() bool { return ch.Mode() == ModeBreak })

	// After auto-return, should go back to ModeAutomated.
	waitFor(t, 2*time.Second, func() bool { return ch.Mode() == ModeAutomated })
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
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

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
	if cancel, ok := pb.trackCancels[pod.ID]; ok {
		cancel()
		delete(pb.trackCancels, pod.ID)
	}
	pb.mu.Unlock()
}

func TestPodBoard_Fire_UsesSpliceCoordinator(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     200,
	})

	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	pb.SetSpliceCoordinator(sc)
	pb.SetPTSProvider(func() int64 { return 1000000 })

	var breakStarted atomic.Bool
	pb.SetOnBreakStart(func() { breakStarted.Store(true) })

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   180000,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// With splice coordinator, cue should NOT be injected immediately.
	if !sc.Active() {
		t.Fatal("splice coordinator should be active after Fire")
	}

	// No immediate injection.
	time.Sleep(50 * time.Millisecond)
	calls := inj.getCalls()
	if len(calls) > 0 {
		t.Error("should not have injected cue immediately when using splice coordinator")
	}

	// Break start callback should still fire.
	if !breakStarted.Load() {
		t.Error("onBreakStart should have been called")
	}

	// Pod state should be PodPreRoll.
	p, _ := pb.GetPod(pod.ID)
	if p.State != PodPreRoll {
		t.Errorf("state = %v, want pre_roll", p.State)
	}

	// Cleanup: cancel splice and force terminal.
	sc.Cancel()
	pb.mu.Lock()
	if pp, ok := pb.pods[pod.ID]; ok {
		pp.State = PodDumpedOut
		pp.CompletedAt = time.Now().UnixMilli()
	}
	pb.activePodID = ""
	if cancel, ok := pb.trackCancels[pod.ID]; ok {
		cancel()
		delete(pb.trackCancels, pod.ID)
	}
	pb.mu.Unlock()
}

func TestPodBoard_Fire_FallbackWithoutCoordinator(t *testing.T) {
	inj := &fakeSCTE35Injector{}
	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	// Do NOT set splice coordinator.

	pod, _ := pb.CreatePod(PodConfig{
		Type:       "quarter",
		DurationMs: 180000,
		AutoReturn: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Should inject immediately.
	calls := inj.getCalls()
	if len(calls) != 1 {
		t.Errorf("expected 1 immediate cue call, got %d", len(calls))
	}

	p, _ := pb.GetPod(pod.ID)
	if p.State != PodBreakActive {
		t.Errorf("state = %v, want break_active", p.State)
	}

	// Cleanup.
	pb.mu.Lock()
	if pp, ok := pb.pods[pod.ID]; ok {
		pp.State = PodRejoined
		pp.CompletedAt = time.Now().UnixMilli()
	}
	pb.activePodID = ""
	if cancel, ok := pb.autoReturnCancels[pod.ID]; ok {
		cancel()
		delete(pb.autoReturnCancels, pod.ID)
	}
	if cancel, ok := pb.trackCancels[pod.ID]; ok {
		cancel()
		delete(pb.trackCancels, pod.ID)
	}
	pb.mu.Unlock()
}

func TestPodBoard_SpliceReady_TransitionsToBreakActive(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     100, // short pre-roll
		WindowMs:      50,
		TimeoutMs:     500,
	})

	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	pb.SetSpliceCoordinator(sc)
	pb.SetPTSProvider(func() int64 { return 0 })

	pod, _ := pb.CreatePod(PodConfig{
		Type:       "quarter",
		DurationMs: 500,
		AutoReturn: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Pod is in PreRoll. Feed frames through window to trigger finalization.
	w, h := 320, 240
	ySize := video.YUV420FrameSize(w, h)
	// Window: target=9000, window=[4500, 13500] at 90kHz
	for pts := int64(0); pts <= 15000; pts += 3000 {
		frame := make([]byte, ySize)
		sc.IngestFrame(frame, w, h, pts)
	}

	// Wait for splice ready callback to fire and transition pod.
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State == PodBreakActive
	})

	p, _ := pb.GetPod(pod.ID)
	if p.State != PodBreakActive {
		t.Errorf("state = %v, want break_active after splice ready", p.State)
	}

	// Cleanup: dump out.
	_ = pb.DumpOut(pod.ID)
	waitFor(t, time.Second, func() bool {
		pp, _ := pb.GetPod(pod.ID)
		return pp.State.IsTerminal()
	})
}

func TestPodBoard_DumpOut_CancelsSplice(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     5000, // long timeout so it doesn't auto-fire
	})

	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	pb.SetSpliceCoordinator(sc)
	pb.SetPTSProvider(func() int64 { return 0 })

	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   180000,
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)
	_ = pb.Fire(pod.ID)

	// Pod should be in PodPreRoll with splice active.
	if !sc.Active() {
		t.Fatal("splice should be active")
	}

	// Dump out while still in pre-roll.
	err := pb.DumpOut(pod.ID)
	if err != nil {
		t.Fatalf("DumpOut: %v", err)
	}

	// Splice coordinator should be cancelled.
	if sc.Active() {
		t.Error("splice should be cancelled after dump-out")
	}

	// Pod should be dumped out.
	p, _ := pb.GetPod(pod.ID)
	if p.State != PodDumpedOut {
		t.Errorf("state = %v, want dumped_out", p.State)
	}
}

func TestManager_ChannelStatesIncludePods(t *testing.T) {
	mgr := NewManager(ManagerConfig{AssetProvider: fakeProvider(30), AllowedLocalPrefixes: []string{"/"}})
	defer mgr.Close()

	pb, _ := mgr.PodBoard(1)
	pod, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000})
	_ = pb.Arm(pod.ID)

	states := mgr.ChannelStates()
	if states[0].Pods == nil {
		t.Fatal("expected non-nil Pods in channel status")
	}
	if states[0].Pods.PodsArmed != 1 {
		t.Errorf("podsArmed = %d, want 1", states[0].Pods.PodsArmed)
	}
}

// TestPodBoard_Fire_SpliceFallback_ArmsAutoReturn is a regression test: when
// BeginSplice fails (e.g. another channel's splice is in flight on the shared
// coordinator), Fire falls back to immediate break activation — but unlike the
// legacy no-coordinator path it never armed the auto-return timer, so a pod
// with AutoReturn stayed in break forever (channel stuck in ModeBreak).
func TestPodBoard_Fire_SpliceFallback_ArmsAutoReturn(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     60000, // long timeout so the other splice stays active
	})

	// Occupy the coordinator so the pod board's BeginSplice fails.
	if err := sc.BeginSplice("other-channel-pod", true, true, 180000, 0); err != nil {
		t.Fatalf("BeginSplice (occupier): %v", err)
	}

	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	pb.SetSpliceCoordinator(sc)
	pb.SetPTSProvider(func() int64 { return 0 })

	pod, _ := pb.CreatePod(PodConfig{
		Type:       "quarter",
		DurationMs: 100, // short so auto-return fires quickly
		AutoReturn: true,
	})
	_ = pb.Arm(pod.ID)
	if err := pb.Fire(pod.ID); err != nil {
		t.Fatalf("Fire: %v", err)
	}

	// Fallback path: pod goes straight to break_active...
	p, _ := pb.GetPod(pod.ID)
	if p.State != PodBreakActive {
		t.Fatalf("state after fallback fire = %v, want break_active", p.State)
	}

	// ...and auto-return must still complete the break after DurationMs.
	waitFor(t, 2*time.Second, func() bool {
		pp, _ := pb.GetPod(pod.ID)
		return pp.State == PodRejoined
	})

	sc.Cancel()
}

// TestPodBoard_Fire_FailedSplice_DoesNotClobberOtherBoardCallback is a
// regression test for the shared-coordinator callback clobber: Fire registered
// the board's splice-ready callback on the coordinator BEFORE BeginSplice, so a
// second board's failed Fire overwrote the first board's pending callback. When
// the first board's splice finalized, the callback was routed to the wrong
// board, the podID lookup missed, and the first board's pod was stuck in
// PodPreRoll with no auto-return ever armed.
func TestPodBoard_Fire_FailedSplice_DoesNotClobberOtherBoardCallback(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     100,
		WindowMs:      50,
		TimeoutMs:     60000, // finalization driven by IngestFrame below
	})

	pb1 := NewPodBoard(1)
	pb1.SetSCTE35Injector(inj)
	pb1.SetSpliceCoordinator(sc)
	pb1.SetPTSProvider(func() int64 { return 0 })

	pb2 := NewPodBoard(2)
	pb2.SetSCTE35Injector(inj)
	pb2.SetSpliceCoordinator(sc)
	pb2.SetPTSProvider(func() int64 { return 0 })

	pod1, _ := pb1.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, AllowDumpOut: true})
	_ = pb1.Arm(pod1.ID)
	if err := pb1.Fire(pod1.ID); err != nil {
		t.Fatalf("board1 Fire: %v", err)
	}
	p1, _ := pb1.GetPod(pod1.ID)
	if p1.State != PodPreRoll {
		t.Fatalf("board1 pod state = %v, want pre_roll", p1.State)
	}

	// Board 2 fires while board 1's splice is scanning — BeginSplice fails and
	// board 2 falls back to immediate activation. This must NOT disturb board
	// 1's pending splice-ready callback.
	pod2, _ := pb2.CreatePod(PodConfig{Type: "timeout", DurationMs: 180000, AllowDumpOut: true})
	_ = pb2.Arm(pod2.ID)
	if err := pb2.Fire(pod2.ID); err != nil {
		t.Fatalf("board2 Fire: %v", err)
	}
	p2, _ := pb2.GetPod(pod2.ID)
	if p2.State != PodBreakActive {
		t.Fatalf("board2 pod state = %v, want break_active (fallback)", p2.State)
	}

	// Finalize board 1's splice: feed frames through and past the scan window
	// (target=9000, window=[4500, 13500] at 90kHz).
	w, h := 320, 240
	ySize := video.YUV420FrameSize(w, h)
	for pts := int64(0); pts <= 15000; pts += 3000 {
		sc.IngestFrame(make([]byte, ySize), w, h, pts)
	}

	// Board 1's pod must transition to break_active via ITS callback.
	waitFor(t, 2*time.Second, func() bool {
		pp, _ := pb1.GetPod(pod1.ID)
		return pp.State == PodBreakActive
	})

	// Cleanup.
	_ = pb1.DumpOut(pod1.ID)
	_ = pb2.DumpOut(pod2.ID)
}
