package playout

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zsiec/switchframe/server/internal/video"
)

// fakeSpliceInjector records SchedulePodCue and InjectPodCue calls.
type fakeSpliceInjector struct {
	mu     sync.Mutex
	calls  []fakeScheduleCall
	nextID uint32
}

type fakeScheduleCall struct {
	IsOut      bool
	AutoReturn bool
	DurationMs int64
	PreRollMs  int64
}

func (f *fakeSpliceInjector) SchedulePodCue(isOut, autoReturn bool, durationMs, preRollMs int64) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.calls = append(f.calls, fakeScheduleCall{isOut, autoReturn, durationMs, preRollMs})
	return f.nextID, nil
}

func (f *fakeSpliceInjector) InjectPodCue(isOut, autoReturn bool, durationMs int64) (uint32, error) {
	return f.SchedulePodCue(isOut, autoReturn, durationMs, 0)
}

func (f *fakeSpliceInjector) getCalls() []fakeScheduleCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]fakeScheduleCall, len(f.calls))
	copy(cp, f.calls)
	return cp
}

func TestSpliceCoordinator_New(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})

	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})

	if sc == nil {
		t.Fatal("NewSpliceCoordinator returned nil")
	}
	if sc.Active() {
		t.Error("new SpliceCoordinator should not be active")
	}

	// Verify defaults.
	if sc.preRollMs != 5000 {
		t.Errorf("preRollMs = %d, want 5000", sc.preRollMs)
	}
	if sc.windowMs != 500 {
		t.Errorf("windowMs = %d, want 500", sc.windowMs)
	}
	if sc.timeoutMs != 2000 {
		t.Errorf("timeoutMs = %d, want 2000", sc.timeoutMs)
	}
}

func TestSpliceCoordinator_NewWithConfig(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})

	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     3000,
		WindowMs:      200,
		TimeoutMs:     1000,
	})

	if sc.preRollMs != 3000 {
		t.Errorf("preRollMs = %d, want 3000", sc.preRollMs)
	}
	if sc.windowMs != 200 {
		t.Errorf("windowMs = %d, want 200", sc.windowMs)
	}
	if sc.timeoutMs != 1000 {
		t.Errorf("timeoutMs = %d, want 1000", sc.timeoutMs)
	}
}

func TestSpliceState_String(t *testing.T) {
	tests := []struct {
		state SpliceState
		want  string
	}{
		{SpliceIdle, "idle"},
		{SpliceScanning, "scanning"},
		{SpliceLocked, "locked"},
		{SpliceComplete, "complete"},
		{SpliceState(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("SpliceState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestSpliceCoordinator_SetOnSpliceReady(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})

	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {})

	if sc.onSpliceReady == nil {
		t.Error("onSpliceReady should be set after SetOnSpliceReady")
	}
}

func TestSpliceCoordinator_BeginSplice(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	err := sc.BeginSplice("pod-1", true, true, 180000, 0)
	if err != nil {
		t.Fatalf("BeginSplice: %v", err)
	}
	if !sc.Active() {
		t.Error("should be active after BeginSplice")
	}

	// Second call should fail.
	err = sc.BeginSplice("pod-2", true, false, 60000, 0)
	if err != ErrSpliceAlreadyActive {
		t.Fatalf("expected ErrSpliceAlreadyActive, got %v", err)
	}

	// Verify PTS math: preRollMs=5000, at 90kHz that's 450000 ticks.
	// currentPTS=0, so targetPTS=450000, window=[405000, 495000].
	sc.mu.Lock()
	op := sc.active
	sc.mu.Unlock()

	if op.TargetPTS != 450000 {
		t.Errorf("TargetPTS = %d, want 450000", op.TargetPTS)
	}
	if op.WindowStartPTS != 405000 {
		t.Errorf("WindowStartPTS = %d, want 405000", op.WindowStartPTS)
	}
	if op.WindowEndPTS != 495000 {
		t.Errorf("WindowEndPTS = %d, want 495000", op.WindowEndPTS)
	}
	if op.State != SpliceScanning {
		t.Errorf("State = %v, want SpliceScanning", op.State)
	}

	// Cleanup.
	sc.Cancel()
}

func TestSpliceCoordinator_IngestFrame_CollectsInWindow(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	// Begin splice at PTS 0. Window = [405000, 495000].
	_ = sc.BeginSplice("pod-1", true, true, 180000, 0)

	// Frame before window: should be ignored.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 400000)

	sc.mu.Lock()
	candCount := len(sc.active.candidates)
	sc.mu.Unlock()
	if candCount != 0 {
		t.Errorf("candidates before window = %d, want 0", candCount)
	}

	// Frame inside window: should be collected.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 410000)
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 450000)
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 490000)

	sc.mu.Lock()
	candCount = len(sc.active.candidates)
	sc.mu.Unlock()
	if candCount != 3 {
		t.Errorf("candidates in window = %d, want 3", candCount)
	}

	// Cleanup.
	sc.Cancel()
}

func TestSpliceCoordinator_IngestFrame_TriggersAnalysis(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	var readyPodID string
	var readyPTS int64
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		readyPodID = podID
		readyPTS = pts
	})

	// Begin splice at PTS 0. Window = [405000, 495000].
	_ = sc.BeginSplice("pod-test", true, true, 180000, 0)

	// Create a black frame (all zeros) inside the window.
	blackFrame := make([]byte, video.YUV420FrameSize(100, 100)) // all zeros = black
	sc.IngestFrame(blackFrame, 100, 100, 420000)

	// Create a non-black frame inside the window.
	nonBlack := make([]byte, video.YUV420FrameSize(100, 100))
	for i := range nonBlack[:100*100] { // fill Y plane with 200
		nonBlack[i] = 200
	}
	sc.IngestFrame(nonBlack, 100, 100, 450000)

	// Send a frame past the window end to trigger finalization.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 500000)

	// Verify SchedulePodCue was called.
	calls := inj.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 SchedulePodCue call, got %d", len(calls))
	}
	if !calls[0].IsOut {
		t.Error("expected isOut=true")
	}
	if !calls[0].AutoReturn {
		t.Error("expected autoReturn=true")
	}
	if calls[0].DurationMs != 180000 {
		t.Errorf("DurationMs = %d, want 180000", calls[0].DurationMs)
	}

	// Black frame at PTS 420000 should be chosen.
	if calls[0].PreRollMs < 0 {
		t.Errorf("PreRollMs = %d, should be >= 0", calls[0].PreRollMs)
	}

	// Verify callback was called.
	if readyPodID != "pod-test" {
		t.Errorf("readyPodID = %q, want pod-test", readyPodID)
	}
	if readyPTS != 420000 {
		t.Errorf("readyPTS = %d, want 420000 (black frame)", readyPTS)
	}

	// Should no longer be active.
	if sc.Active() {
		t.Error("should not be active after finalization")
	}
}

func TestSpliceCoordinator_Cancel(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})

	_ = sc.BeginSplice("pod-1", true, true, 180000, 0)
	if !sc.Active() {
		t.Fatal("should be active")
	}

	sc.Cancel()
	if sc.Active() {
		t.Error("should not be active after Cancel")
	}

	// Should be able to start a new splice after cancel.
	err := sc.BeginSplice("pod-2", true, false, 60000, 0)
	if err != nil {
		t.Fatalf("BeginSplice after cancel: %v", err)
	}
	sc.Cancel()
}

func TestSpliceCoordinator_Reset(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
	})

	_ = sc.BeginSplice("pod-1", true, true, 180000, 0)
	sc.Reset()

	if sc.Active() {
		t.Error("should not be active after Reset")
	}
}

func TestSpliceCoordinator_FinalizeTriggersSceneChange(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	var readyPTS int64
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		readyPTS = pts
	})

	_ = sc.BeginSplice("pod-scene", true, true, 180000, 0)

	// Two very different frames: should detect scene change.
	darkFrame := make([]byte, video.YUV420FrameSize(100, 100))
	for i := 0; i < 100*100; i++ {
		darkFrame[i] = 50 // not black (above threshold), but dark
	}
	sc.IngestFrame(darkFrame, 100, 100, 420000)

	brightFrame := make([]byte, video.YUV420FrameSize(100, 100))
	for i := 0; i < 100*100; i++ {
		brightFrame[i] = 250 // bright
	}
	sc.IngestFrame(brightFrame, 100, 100, 450000)

	// Close the window.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 500000)

	// Scene change should pick the second frame (index 1).
	if readyPTS != 450000 {
		t.Errorf("readyPTS = %d, want 450000 (scene change at bright frame)", readyPTS)
	}
}

func TestSpliceCoordinator_FallbackToScheduled(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
	})

	var readyPTS int64
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		readyPTS = pts
	})

	_ = sc.BeginSplice("pod-sched", true, true, 180000, 0)

	// All similar frames: no black, no scene change -> fallback to middle.
	for pts := int64(410000); pts <= 490000; pts += 10000 {
		frame := make([]byte, video.YUV420FrameSize(100, 100))
		for i := 0; i < 100*100; i++ {
			frame[i] = 128 // uniform mid-gray
		}
		sc.IngestFrame(frame, 100, 100, pts)
	}

	// Close window.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 500000)

	// Middle of 9 frames (indices 0-8) is index 4, which is PTS 450000.
	if readyPTS != 450000 {
		t.Errorf("readyPTS = %d, want 450000 (scheduled middle frame)", readyPTS)
	}
}

func TestSpliceCoordinator_Timeout(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     200, // short timeout
	})

	readyCh := make(chan struct{}, 1)
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		readyCh <- struct{}{}
	})

	// Begin splice and send one frame inside the window but never close it.
	_ = sc.BeginSplice("pod-timeout", true, true, 180000, 0)

	// Send one candidate frame inside the window.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(100, 100)), 100, 100, 450000)

	// Wait for timeout to trigger finalization.
	select {
	case <-readyCh:
		// Good, timeout triggered.
	case <-time.After(2 * time.Second):
		t.Fatal("timeout did not trigger finalization")
	}

	// Verify cue was injected.
	calls := inj.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call after timeout, got %d", len(calls))
	}
	if !calls[0].IsOut {
		t.Error("expected isOut=true")
	}

	// Should no longer be active.
	if sc.Active() {
		t.Error("should not be active after timeout finalization")
	}
}

func TestSpliceCoordinator_TimeoutNoCandidates(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     200, // short timeout
	})

	readyCh := make(chan struct{}, 1)
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		readyCh <- struct{}{}
	})

	// Begin splice but send NO frames -- timeout should still finalize with fallback.
	_ = sc.BeginSplice("pod-empty", true, true, 180000, 0)

	select {
	case <-readyCh:
		// Good.
	case <-time.After(2 * time.Second):
		t.Fatal("timeout did not trigger finalization with no candidates")
	}

	// Should have used target PTS as fallback.
	calls := inj.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}

	if sc.Active() {
		t.Error("should not be active after timeout")
	}
}

func TestSpliceCoordinator_CancelStopsTimeout(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     200,
	})

	_ = sc.BeginSplice("pod-cancel", true, true, 180000, 0)

	// Cancel immediately before timeout fires.
	sc.Cancel()

	// Wait past the timeout period.
	time.Sleep(400 * time.Millisecond)

	// No cue should have been injected.
	calls := inj.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 calls after cancel, got %d", len(calls))
	}
}

func TestSpliceCoordinator_Integration_FireToReturn(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     300, // short for test (27000 PTS ticks)
		WindowMs:      100, // ±100ms = ±9000 ticks
		TimeoutMs:     500,
	})

	pb := NewPodBoard(1)
	pb.SetSCTE35Injector(inj)
	pb.SetSpliceCoordinator(sc)

	currentPTS := int64(0)
	pb.SetPTSProvider(func() int64 { return currentPTS })

	var breakStarted, breakEnded atomic.Bool
	pb.SetOnBreakStart(func() { breakStarted.Store(true) })
	pb.SetOnBreakEnd(func() { breakEnded.Store(true) })

	// Create and fire pod.
	pod, _ := pb.CreatePod(PodConfig{
		Type:         "quarter",
		DurationMs:   500, // 500ms for fast auto-return
		AutoReturn:   true,
		AllowDumpOut: true,
	})
	_ = pb.Arm(pod.ID)

	err := pb.Fire(pod.ID)
	if err != nil {
		t.Fatalf("Fire: %v", err)
	}

	// Verify pod is in pre-roll.
	p, _ := pb.GetPod(pod.ID)
	if p.State != PodPreRoll {
		t.Fatalf("state after Fire = %v, want pre_roll", p.State)
	}

	// Feed frames through the window.
	// Target PTS = 0 + 300*90 = 27000. Window: [18000, 36000].
	w, h := 320, 240
	ySize := video.YUV420FrameSize(w, h)

	for pts := int64(0); pts <= 40000; pts += 3000 {
		frame := make([]byte, ySize)
		luma := byte(128)
		if pts == 27000 {
			luma = 0 // black at target
		}
		for j := 0; j < w*h; j++ {
			frame[j] = luma
		}
		sc.IngestFrame(frame, w, h, pts)
		currentPTS = pts
	}

	// Wait for splice to finalize and pod to transition to break_active.
	waitFor(t, 2*time.Second, func() bool {
		p, _ := pb.GetPod(pod.ID)
		return p.State == PodBreakActive
	})

	// Verify cue-out was injected.
	calls := inj.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected cue-out injection after splice")
	}
	if !calls[0].IsOut {
		t.Error("first cue should be cue-out")
	}
	if calls[0].DurationMs != 500 {
		t.Errorf("duration = %d, want 500", calls[0].DurationMs)
	}

	// Verify break started.
	if !breakStarted.Load() {
		t.Error("onBreakStart should have fired")
	}

	// Wait for auto-return (500ms pod duration).
	waitFor(t, 2*time.Second, func() bool {
		return breakEnded.Load()
	})

	// Verify cue-in was injected.
	calls = inj.getCalls()
	cueInFound := false
	for _, c := range calls {
		if !c.IsOut {
			cueInFound = true
		}
	}
	if !cueInFound {
		t.Error("expected cue-in after auto-return")
	}

	// Pod should be in terminal state.
	p, _ = pb.GetPod(pod.ID)
	if !p.State.IsTerminal() {
		t.Errorf("pod state = %v, want terminal", p.State)
	}
}

func TestSpliceCoordinator_Integration_FireAndDumpOut(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     5000, // long timeout
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

	// Pod is in pre-roll, splice is scanning.
	if !sc.Active() {
		t.Fatal("splice should be active")
	}

	// Dump out immediately.
	err := pb.DumpOut(pod.ID)
	if err != nil {
		t.Fatalf("DumpOut: %v", err)
	}

	// Splice should be cancelled.
	if sc.Active() {
		t.Error("splice should be cancelled")
	}

	// Pod should be dumped out.
	p, _ := pb.GetPod(pod.ID)
	if p.State != PodDumpedOut {
		t.Errorf("state = %v, want dumped_out", p.State)
	}
}

// hookInjector lets a test run arbitrary code inside SchedulePodCue, which the
// coordinator calls with sc.mu released — exactly the window in which a
// concurrent DumpOut->Cancel + Fire->BeginSplice can replace the operation.
type hookInjector struct {
	fakeSpliceInjector
	onSchedule func()
}

func (h *hookInjector) SchedulePodCue(isOut, autoReturn bool, durationMs, preRollMs int64) (uint32, error) {
	if h.onSchedule != nil {
		h.onSchedule()
	}
	return h.fakeSpliceInjector.SchedulePodCue(isOut, autoReturn, durationMs, preRollMs)
}

// TestSpliceCoordinator_FinalizeDoesNotCorruptReplacedOp verifies that if the
// active operation is cancelled and replaced (DumpOut -> Cancel, then
// Fire -> BeginSplice) while finalizeSplice is mid-analysis with sc.mu
// released, the post-analysis writes do NOT land on the brand-new operation and
// the onSpliceReady callback is not fired for the old pod against the new op.
func TestSpliceCoordinator_FinalizeDoesNotCorruptReplacedOp(t *testing.T) {
	inj := &hookInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     5000,
	})

	var oldReadyCalls int32
	sc.SetOnSpliceReady(func(podID string, pts int64, preRollMs int64) {
		if podID == "pod-old" {
			atomic.AddInt32(&oldReadyCalls, 1)
		}
	})

	// During the unlocked injector call (analysis window), simulate the racing
	// DumpOut->Cancel of the old op followed by a fresh Fire->BeginSplice that
	// installs a brand-new operation for a different pod.
	inj.onSchedule = func() {
		sc.Cancel()
		if err := sc.BeginSplice("pod-new", false, false, 60000, 1000000); err != nil {
			t.Errorf("BeginSplice for replacement op: %v", err)
		}
	}

	// Begin and finalize the OLD splice. Window = [405000, 495000].
	_ = sc.BeginSplice("pod-old", true, true, 180000, 0)
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(64, 64)), 64, 64, 450000)
	// Frame past window end triggers finalizeSplice synchronously.
	sc.IngestFrame(make([]byte, video.YUV420FrameSize(64, 64)), 64, 64, 500000)

	// The new operation must be intact: not stamped with the old op's EventID,
	// not flipped to SpliceComplete, and still scanning its own window.
	sc.mu.Lock()
	newOp := sc.active
	sc.mu.Unlock()

	if newOp == nil {
		t.Fatal("expected a new active operation after replacement")
	}
	if newOp.PodID != "pod-new" {
		t.Fatalf("active PodID = %q, want pod-new", newOp.PodID)
	}
	if newOp.State != SpliceScanning {
		t.Errorf("new op State = %v, want SpliceScanning (must not be overwritten to complete)", newOp.State)
	}
	if newOp.EventID != 0 {
		t.Errorf("new op EventID = %d, want 0 (old op's eventID must not leak in)", newOp.EventID)
	}
	if newOp.LockedPTS != 0 {
		t.Errorf("new op LockedPTS = %d, want 0 (old op's lockedPTS must not leak in)", newOp.LockedPTS)
	}
	// The new op opened its own scan window from currentPTS=1000000 (+5000ms
	// pre-roll), so it must not have inherited the old op's window.
	if newOp.TargetPTS == 450000 {
		t.Error("new op inherited the old op's TargetPTS — operation was corrupted")
	}

	// onSpliceReady must not have fired for the cancelled/replaced old pod.
	if got := atomic.LoadInt32(&oldReadyCalls); got != 0 {
		t.Errorf("onSpliceReady fired %d times for cancelled old pod, want 0", got)
	}

	sc.Cancel()
}

func TestSpliceCoordinator_ReturnSplice(t *testing.T) {
	inj := &fakeSpliceInjector{}
	bd := NewBlackDetector(BlackDetectorConfig{})
	sc := NewSpliceCoordinator(SpliceCoordinatorConfig{
		Injector:      inj,
		BlackDetector: bd,
		PreRollMs:     5000,
		WindowMs:      500,
		TimeoutMs:     2000,
	})

	// Start a return splice (cue-in).
	err := sc.BeginSplice("pod-1", false, false, 0, 0)
	if err != nil {
		t.Fatalf("BeginSplice for return: %v", err)
	}

	// Send frames with a black frame in the window.
	w, h := 320, 240
	ySize := video.YUV420FrameSize(w, h)
	// Target PTS = 450000, window = [405000, 495000]
	for pts := int64(400000); pts <= 500000; pts += 3000 {
		frame := make([]byte, ySize)
		luma := byte(128)
		if pts == 450000 {
			luma = 0
		}
		for j := 0; j < w*h; j++ {
			frame[j] = luma
		}
		sc.IngestFrame(frame, w, h, pts)
	}

	waitFor(t, time.Second, func() bool {
		return !sc.Active()
	})

	calls := inj.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected return cue injection")
	}
	if calls[0].IsOut {
		t.Error("return splice should inject cue-in (isOut=false)")
	}
}
