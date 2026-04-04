package playout

import (
	"testing"
	"time"
)

func TestPodState_String(t *testing.T) {
	tests := []struct {
		state PodState
		want  string
	}{
		{PodScheduled, "scheduled"},
		{PodPreRoll, "pre_roll"},
		{PodSplicePending, "splice_pending"},
		{PodBreakActive, "break_active"},
		{PodReturnPending, "return_pending"},
		{PodRejoined, "rejoined"},
		{PodDumpedOut, "dumped_out"},
		{PodState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("PodState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestPodPriority_String(t *testing.T) {
	tests := []struct {
		p    PodPriority
		want string
	}{
		{PriorityNormal, "normal"},
		{PriorityHigh, "high"},
		{PriorityMandatory, "mandatory"},
		{PodPriority(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("PodPriority(%d).String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestPodPriority_Less(t *testing.T) {
	if PriorityNormal.Less(PriorityHigh) != true {
		t.Error("normal should be less than high")
	}
	if PriorityHigh.Less(PriorityNormal) != false {
		t.Error("high should not be less than normal")
	}
	if PriorityMandatory.Less(PriorityMandatory) != false {
		t.Error("equal priorities should not be less")
	}
}

func TestPodState_IsTerminal(t *testing.T) {
	if PodScheduled.IsTerminal() {
		t.Error("scheduled should not be terminal")
	}
	if PodBreakActive.IsTerminal() {
		t.Error("break_active should not be terminal")
	}
	if !PodRejoined.IsTerminal() {
		t.Error("rejoined should be terminal")
	}
	if !PodDumpedOut.IsTerminal() {
		t.Error("dumped_out should be terminal")
	}
}

func TestPodBoard_HighestPriorityArmed(t *testing.T) {
	pb := NewPodBoard(1)

	p1, _ := pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Priority: PriorityNormal})
	p2, _ := pb.CreatePod(PodConfig{Type: "mandatory_sponsor", DurationMs: 120000, Priority: PriorityMandatory})
	_ = pb.Arm(p1.ID)
	_ = pb.Arm(p2.ID)

	highest := pb.HighestPriorityArmed()
	if highest == nil {
		t.Fatal("expected a highest priority pod")
	}
	if highest.ID != p2.ID {
		t.Errorf("expected mandatory pod %s, got %s", p2.ID, highest.ID)
	}
}

func TestPodBoard_HighestPriorityArmed_NoneArmed(t *testing.T) {
	pb := NewPodBoard(1)
	_, _ = pb.CreatePod(PodConfig{Type: "quarter", DurationMs: 180000, Priority: PriorityNormal})

	highest := pb.HighestPriorityArmed()
	if highest != nil {
		t.Error("expected nil when no pods are armed")
	}
}

func TestPodBoard_FullScenario_ShortBreakThenHoldBreak(t *testing.T) {
	pb := NewPodBoard(1)
	store := NewPodTemplateStore()

	// Load manifest-defined templates (replaces old sport-specific builtins).
	store.LoadFromPlaylist([]PodTemplateDef{
		{Label: "short_break", DurationMs: 100, AutoReturn: true, Priority: PriorityNormal},
		{Label: "hold_break", DurationMs: 0, AutoReturn: false, Priority: PriorityHigh},
	})
	pb.SetTemplateStore(store)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	// Create short break pod.
	shortPod, _ := pb.CreateFromTemplate("short_break")
	_ = pb.Arm(shortPod.ID)

	// Fire short break.
	_ = pb.Fire(shortPod.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(shortPod.ID)
		return p.State == PodBreakActive
	})

	// Short break auto-returns.
	waitFor(t, 2*time.Second, func() bool {
		p, _ := pb.GetPod(shortPod.ID)
		return p.State == PodRejoined
	})

	// Now fire a hold break pod.
	holdPod, _ := pb.CreateFromTemplate("hold_break")
	_ = pb.Arm(holdPod.ID)
	_ = pb.Fire(holdPod.ID)

	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(holdPod.ID)
		return p.State == PodBreakActive
	})

	// Hold break stays active (no auto-return, open-ended).
	time.Sleep(100 * time.Millisecond)
	got, _ := pb.GetPod(holdPod.ID)
	if got.State != PodBreakActive {
		t.Errorf("hold_break should still be active, got %v", got.State)
	}

	// Dump out of hold break.
	_ = pb.DumpOut(holdPod.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(holdPod.ID)
		return p.State == PodDumpedOut
	})
}
