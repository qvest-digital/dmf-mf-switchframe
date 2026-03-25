package playout

import "testing"

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
