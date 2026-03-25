package playout

import (
	"sync"
	"testing"
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

	sc.SetOnSpliceReady(func(podID string, pts int64) {})

	if sc.onSpliceReady == nil {
		t.Error("onSpliceReady should be set after SetOnSpliceReady")
	}
}
