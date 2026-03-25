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

func TestPodBoard_FullScenario_NFLInjuryDuringTimeout(t *testing.T) {
	pb := NewPodBoard(1)
	store := NewPodTemplateStore()
	pb.SetTemplateStore(store)
	inj := &fakeSCTE35Injector{}
	pb.SetSCTE35Injector(inj)

	// Create timeout pod.
	timeout, _ := pb.CreateFromTemplate("timeout")
	pb.mu.Lock()
	pb.pods[timeout.ID].Config.DurationMs = 100
	pb.mu.Unlock()
	pb.Arm(timeout.ID)

	// Fire timeout.
	pb.Fire(timeout.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(timeout.ID)
		return p.State == PodBreakActive
	})

	// Timeout auto-returns.
	waitFor(t, 2*time.Second, func() bool {
		p, _ := pb.GetPod(timeout.ID)
		return p.State == PodRejoined
	})

	// Now fire an injury pod.
	injury, _ := pb.CreateFromTemplate("injury")
	pb.Arm(injury.ID)
	pb.Fire(injury.ID)

	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(injury.ID)
		return p.State == PodBreakActive
	})

	// Injury holds.
	time.Sleep(100 * time.Millisecond)
	got, _ := pb.GetPod(injury.ID)
	if got.State != PodBreakActive {
		t.Errorf("injury should still be active, got %v", got.State)
	}

	// Dump out of injury.
	pb.DumpOut(injury.ID)
	waitFor(t, time.Second, func() bool {
		p, _ := pb.GetPod(injury.ID)
		return p.State == PodDumpedOut
	})
}
