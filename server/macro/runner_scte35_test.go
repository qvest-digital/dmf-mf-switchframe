package macro

import (
	"context"
	"testing"
)

type mockSCTE35Target struct {
	mockTarget // embed existing mock
	scte35CueCalled    bool
	scte35ReturnCalled bool
	scte35CancelCalled bool
	scte35HoldCalled   bool
	scte35ExtendCalled bool
	lastEventID        uint32
	lastDurationMs     int64
	lastCueParams      map[string]interface{}
}

func (m *mockSCTE35Target) SCTE35Cue(ctx context.Context, params map[string]interface{}) (uint32, error) {
	m.scte35CueCalled = true
	m.lastCueParams = params
	return 42, nil
}

func (m *mockSCTE35Target) SCTE35Return(ctx context.Context, eventID uint32) error {
	m.scte35ReturnCalled = true
	m.lastEventID = eventID
	return nil
}

func (m *mockSCTE35Target) SCTE35Cancel(ctx context.Context, eventID uint32) error {
	m.scte35CancelCalled = true
	m.lastEventID = eventID
	return nil
}

func (m *mockSCTE35Target) SCTE35Hold(ctx context.Context, eventID uint32) error {
	m.scte35HoldCalled = true
	m.lastEventID = eventID
	return nil
}

func (m *mockSCTE35Target) SCTE35Extend(ctx context.Context, eventID uint32, durationMs int64) error {
	m.scte35ExtendCalled = true
	m.lastEventID = eventID
	m.lastDurationMs = durationMs
	return nil
}

func TestMacro_SCTE35Cue(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "ad_break",
		Steps: []MacroStep{
			{Action: ActionSCTE35Cue, Params: map[string]interface{}{
				"commandType": "splice_insert",
				"isOut":       true,
				"durationMs":  float64(60000),
				"autoReturn":  true,
			}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35CueCalled {
		t.Fatal("SCTE35Cue not called")
	}
}

func TestMacro_SCTE35Return(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "return",
		Steps: []MacroStep{
			{Action: ActionSCTE35Return, Params: map[string]interface{}{}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35ReturnCalled {
		t.Fatal("SCTE35Return not called")
	}
	if target.lastEventID != 0 {
		t.Fatalf("expected eventID=0 (most recent), got %d", target.lastEventID)
	}
}

func TestMacro_SCTE35Cancel(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "cancel",
		Steps: []MacroStep{
			{Action: ActionSCTE35Cancel, Params: map[string]interface{}{"eventId": float64(42)}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35CancelCalled {
		t.Fatal("SCTE35Cancel not called")
	}
}

func TestMacro_SCTE35Hold(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "hold",
		Steps: []MacroStep{
			{Action: ActionSCTE35Hold, Params: map[string]interface{}{"eventId": float64(42)}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35HoldCalled {
		t.Fatal("SCTE35Hold not called")
	}
}

func TestMacro_SCTE35Extend(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "extend",
		Steps: []MacroStep{
			{Action: ActionSCTE35Extend, Params: map[string]interface{}{
				"eventId":    float64(42),
				"durationMs": float64(120000),
			}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35ExtendCalled {
		t.Fatal("SCTE35Extend not called")
	}
	if target.lastDurationMs != 120000 {
		t.Fatalf("expected 120000ms, got %d", target.lastDurationMs)
	}
}

func TestMacro_SCTE35Cue_WithPreRoll(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "preroll_break",
		Steps: []MacroStep{
			{Action: ActionSCTE35Cue, Params: map[string]interface{}{
				"commandType": "splice_insert",
				"isOut":       true,
				"durationMs":  float64(60000),
				"autoReturn":  true,
				"preRollMs":   float64(5000),
			}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35CueCalled {
		t.Fatal("SCTE35Cue not called")
	}
	// Verify preRollMs was passed through in params.
	v, ok := target.lastCueParams["preRollMs"].(float64)
	if !ok || v != 5000 {
		t.Fatalf("expected preRollMs=5000 in params, got %v", target.lastCueParams["preRollMs"])
	}
}

func TestMacro_SCTE35_FullWorkflow(t *testing.T) {
	target := &mockSCTE35Target{}
	m := Macro{
		Name: "full_break",
		Steps: []MacroStep{
			{Action: ActionSCTE35Cue, Params: map[string]interface{}{
				"commandType": "splice_insert",
				"isOut":       true,
				"durationMs":  float64(60000),
			}},
			{Action: ActionWait, Params: map[string]interface{}{"ms": float64(10)}},
			{Action: ActionSCTE35Return, Params: map[string]interface{}{}},
		},
	}
	err := Run(context.Background(), m, target, nil)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !target.scte35CueCalled || !target.scte35ReturnCalled {
		t.Fatal("expected both cue and return called")
	}
}
