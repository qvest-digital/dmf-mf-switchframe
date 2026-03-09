package scte35

import (
	"sync"
	"testing"
	"time"
)

// Integration tests wire real components together (injector + rules engine)
// to verify end-to-end SCTE-35 pipeline behavior.

func TestIntegration_InjectCue_ToMuxer(t *testing.T) {
	// Create injector with real muxer sink, inject cue, verify
	// muxer sink received valid SCTE-35 bytes that decode correctly.
	var received []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		received = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("no data received by sink")
	}

	// Decode the received bytes
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode injected bytes failed: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if !decoded.IsOut {
		t.Fatal("expected IsOut=true")
	}
	if decoded.BreakDuration == nil {
		t.Fatal("expected non-nil BreakDuration")
	}
	// 30 second break duration (allow small rounding tolerance)
	dur := decoded.BreakDuration.Milliseconds()
	if dur < 29999 || dur > 30001 {
		t.Fatalf("expected ~30000ms break duration, got %d", dur)
	}
}

func TestIntegration_RulesEngine_EvaluatesCue(t *testing.T) {
	// Create rules engine that deletes splice_insert commands.
	// Verify evaluation returns ActionDelete for a matching cue.
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "strip all splice_inserts",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Action: ActionDelete,
	})

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	action, _ := re.Evaluate(msg, "")
	if action != ActionDelete {
		t.Fatalf("expected delete action, got %q", action)
	}

	// time_signal should pass through (no matching rule)
	tsMsg := NewTimeSignal(0x34, 30*time.Second, 0x09, []byte("AD123"))
	action, _ = re.Evaluate(tsMsg, "")
	if action != ActionPass {
		t.Fatalf("expected pass action for time_signal, got %q", action)
	}
}

func TestIntegration_RulesEngine_ReplaceDuration(t *testing.T) {
	// Rules engine replaces break duration to a capped value.
	re := NewRuleEngine()
	cappedDur := 60 * time.Second
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "cap duration",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
			{Field: "duration", Operator: ">", Value: "30000"},
		},
		Action:      ActionReplace,
		ReplaceWith: &ReplaceParams{Duration: &cappedDur},
	})

	// 120s break should be capped to 60s
	msg := NewSpliceInsert(0, 120*time.Second, true, true)
	action, modified := re.Evaluate(msg, "")
	if action != ActionReplace {
		t.Fatalf("expected replace action, got %q", action)
	}
	if modified == nil {
		t.Fatal("expected modified message")
	}
	if modified.BreakDuration == nil || *modified.BreakDuration != 60*time.Second {
		t.Fatalf("expected 60s duration, got %v", modified.BreakDuration)
	}

	// 20s break should pass through (below threshold)
	msg2 := NewSpliceInsert(0, 20*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	if action2 != ActionPass {
		t.Fatalf("expected pass for 20s break, got %q", action2)
	}
}

func TestIntegration_AutoReturn_SendsCueIn(t *testing.T) {
	// Inject cue-out with 100ms duration, wait 300ms,
	// verify cue-in was auto-injected (2+ sink calls).
	var mu sync.Mutex
	callCount := 0
	var lastData []byte
	sink := func(data []byte) {
		mu.Lock()
		callCount++
		lastData = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 100*time.Millisecond, true, true)
	_, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	c := callCount
	data := lastData
	mu.Unlock()

	if c < 2 {
		t.Fatalf("expected at least 2 sink calls (cue-out + cue-in), got %d", c)
	}
	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events after auto-return")
	}

	// Verify the last sink call was a cue-in (IsOut=false)
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode last sink data: %v", err)
	}
	if decoded.IsOut {
		t.Fatal("expected cue-in (IsOut=false) from auto-return")
	}
}

func TestIntegration_HoldAndExtend(t *testing.T) {
	// Inject cue with auto-return, hold it, verify no auto-return
	// after original duration. Then extend, verify new timer.
	var mu sync.Mutex
	callCount := 0
	sink := func(data []byte) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 200*time.Millisecond, true, true)
	eventID, _ := inj.InjectCue(msg)

	// Hold immediately
	if err := inj.HoldBreak(eventID); err != nil {
		t.Fatalf("hold failed: %v", err)
	}

	// Wait past original duration
	time.Sleep(400 * time.Millisecond)

	// Should still be active (held)
	if len(inj.ActiveEventIDs()) == 0 {
		t.Fatal("event should still be active (held)")
	}

	// Verify state shows held
	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	if !ok {
		t.Fatal("event not in active events")
	}
	if !active.Held {
		t.Fatal("expected Held=true in state")
	}

	// Extend with new duration — this should un-hold and set a new timer
	if err := inj.ExtendBreak(eventID, 200); err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	// Verify the event is no longer held
	state = inj.State()
	active, ok = state.ActiveEvents[eventID]
	if !ok {
		t.Fatal("event not in active events after extend")
	}
	if active.Held {
		t.Fatal("expected Held=false after extend")
	}
}

func TestIntegration_SyntheticBreakState(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// No events -> nil
	if inj.SyntheticBreakState() != nil {
		t.Fatal("expected nil with no active events")
	}

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, _ = inj.InjectCue(msg)

	synth := inj.SyntheticBreakState()
	if synth == nil || len(synth) == 0 {
		t.Fatal("expected non-empty synthetic break state")
	}

	// Decode and verify it's a valid splice_insert
	decoded, err := Decode(synth)
	if err != nil {
		t.Fatalf("decode synthetic: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if !decoded.IsOut {
		t.Fatal("expected IsOut=true in synthetic state")
	}
	// Synthetic duration should be less than original 60s (time has passed)
	if decoded.BreakDuration != nil {
		dur := decoded.BreakDuration.Milliseconds()
		if dur > 60000 {
			t.Fatalf("synthetic duration %dms should be <= 60000ms", dur)
		}
	}
}

func TestIntegration_ConcurrentEvents(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg1 := NewSpliceInsert(0, 60*time.Second, true, false)
	msg2 := NewSpliceInsert(0, 120*time.Second, true, false)
	id1, _ := inj.InjectCue(msg1)
	id2, _ := inj.InjectCue(msg2)

	if len(inj.ActiveEventIDs()) != 2 {
		t.Fatalf("expected 2 active events, got %d", len(inj.ActiveEventIDs()))
	}

	_ = inj.ReturnToProgram(id1)
	ids := inj.ActiveEventIDs()
	if len(ids) != 1 || ids[0] != id2 {
		t.Fatalf("expected only event %d active, got %v", id2, ids)
	}

	_ = inj.ReturnToProgram(id2)
	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events")
	}
}

func TestIntegration_EventLogCaptures(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)
	_ = inj.ReturnToProgram(eventID)

	log := inj.EventLog()
	if len(log) < 2 {
		t.Fatalf("expected at least 2 log entries (inject + return), got %d", len(log))
	}

	// Verify log entry statuses
	foundInjected := false
	foundReturned := false
	for _, entry := range log {
		if entry.EventID == eventID {
			switch entry.Status {
			case "injected":
				foundInjected = true
			case "returned":
				foundReturned = true
			}
		}
	}
	if !foundInjected {
		t.Fatal("missing 'injected' log entry")
	}
	if !foundReturned {
		t.Fatal("missing 'returned' log entry")
	}
}

func TestIntegration_FullLifecycle_InjectHoldExtendReturn(t *testing.T) {
	// End-to-end lifecycle: inject -> hold -> extend -> manual return.
	// Verify all state transitions are logged and sink receives correct messages.
	var mu sync.Mutex
	var sinkCalls [][]byte
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	// Step 1: Inject cue-out
	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	eventID, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	ids := inj.ActiveEventIDs()
	if len(ids) != 1 || ids[0] != eventID {
		t.Fatalf("expected active event %d, got %v", eventID, ids)
	}

	// Step 2: Hold the break
	if err := inj.HoldBreak(eventID); err != nil {
		t.Fatalf("hold failed: %v", err)
	}

	// Step 3: Extend with new duration
	if err := inj.ExtendBreak(eventID, 60000); err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	// Step 4: Manual return
	if err := inj.ReturnToProgram(eventID); err != nil {
		t.Fatalf("return failed: %v", err)
	}

	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events after return")
	}

	// Verify sink calls: inject (cue-out), extend (updated splice_insert), return (cue-in)
	mu.Lock()
	numCalls := len(sinkCalls)
	mu.Unlock()

	if numCalls < 3 {
		t.Fatalf("expected at least 3 sink calls, got %d", numCalls)
	}

	// Verify first call is cue-out
	mu.Lock()
	firstData := sinkCalls[0]
	lastData := sinkCalls[numCalls-1]
	mu.Unlock()

	decoded, err := Decode(firstData)
	if err != nil {
		t.Fatalf("decode first sink call: %v", err)
	}
	if !decoded.IsOut {
		t.Fatal("first sink call should be cue-out")
	}

	// Verify last call is cue-in
	decoded, err = Decode(lastData)
	if err != nil {
		t.Fatalf("decode last sink call: %v", err)
	}
	if decoded.IsOut {
		t.Fatal("last sink call should be cue-in")
	}

	// Verify all lifecycle events are logged
	log := inj.EventLog()
	statusSet := make(map[string]bool)
	for _, entry := range log {
		if entry.EventID == eventID {
			statusSet[entry.Status] = true
		}
	}
	for _, expected := range []string{"injected", "held", "extended", "returned"} {
		if !statusSet[expected] {
			t.Fatalf("missing log entry with status %q", expected)
		}
	}
}

func TestIntegration_StateChangeCallback(t *testing.T) {
	// Verify onStateChange fires at each lifecycle step.
	var mu sync.Mutex
	stateChangeCount := 0
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.OnStateChange(func() {
		mu.Lock()
		stateChangeCount++
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	eventID, _ := inj.InjectCue(msg)

	mu.Lock()
	if stateChangeCount != 1 {
		t.Fatalf("expected 1 state change after inject, got %d", stateChangeCount)
	}
	mu.Unlock()

	_ = inj.HoldBreak(eventID)
	mu.Lock()
	if stateChangeCount != 2 {
		t.Fatalf("expected 2 state changes after hold, got %d", stateChangeCount)
	}
	mu.Unlock()

	_ = inj.ExtendBreak(eventID, 120000)
	mu.Lock()
	if stateChangeCount != 3 {
		t.Fatalf("expected 3 state changes after extend, got %d", stateChangeCount)
	}
	mu.Unlock()

	_ = inj.ReturnToProgram(eventID)
	mu.Lock()
	if stateChangeCount != 4 {
		t.Fatalf("expected 4 state changes after return, got %d", stateChangeCount)
	}
	mu.Unlock()
}

func TestIntegration_RulesEngine_DestinationFiltering(t *testing.T) {
	// Rules engine with destination-specific rules.
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "r1",
		Name:    "delete for cdn1 only",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "command_type", Operator: "=", Value: "5"},
		},
		Action:       ActionDelete,
		Destinations: []string{"cdn1"},
	})

	msg := NewSpliceInsert(0, 30*time.Second, true, true)

	// Should delete for cdn1
	action, _ := re.Evaluate(msg, "cdn1")
	if action != ActionDelete {
		t.Fatalf("expected delete for cdn1, got %q", action)
	}

	// Should pass for cdn2 (not in destination list)
	action, _ = re.Evaluate(msg, "cdn2")
	if action != ActionPass {
		t.Fatalf("expected pass for cdn2, got %q", action)
	}

	// Should pass for empty destination
	action, _ = re.Evaluate(msg, "")
	if action != ActionPass {
		t.Fatalf("expected pass for empty dest, got %q", action)
	}
}

func TestIntegration_EncodeDecodeRoundTrip(t *testing.T) {
	// Verify that encoding then decoding preserves message semantics.
	tests := []struct {
		name string
		msg  *CueMessage
	}{
		{
			name: "splice_insert cue-out 30s",
			msg:  NewSpliceInsert(42, 30*time.Second, true, true),
		},
		{
			name: "splice_insert cue-in",
			msg:  NewSpliceInsert(42, 0, false, false),
		},
		{
			name: "splice_null",
			msg:  &CueMessage{CommandType: CommandSpliceNull},
		},
		{
			name: "time_signal with descriptor",
			msg:  NewTimeSignal(0x34, 30*time.Second, 0x09, []byte("AD123")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := tt.msg.Encode(true) // verify=true
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}

			if decoded.CommandType != tt.msg.CommandType {
				t.Fatalf("command type mismatch: %d vs %d", decoded.CommandType, tt.msg.CommandType)
			}
			if decoded.IsOut != tt.msg.IsOut {
				t.Fatalf("IsOut mismatch: %v vs %v", decoded.IsOut, tt.msg.IsOut)
			}
			if decoded.AutoReturn != tt.msg.AutoReturn {
				t.Fatalf("AutoReturn mismatch: %v vs %v", decoded.AutoReturn, tt.msg.AutoReturn)
			}
		})
	}
}

func TestIntegration_TimeSignal_InjectAndDecode(t *testing.T) {
	// Wire a time_signal through the injector and verify the output.
	var mu sync.Mutex
	var received []byte
	sink := func(data []byte) {
		mu.Lock()
		received = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 2700000 } // 30s into stream

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewTimeSignal(0x34, 30*time.Second, 0x09, []byte("AD123"))
	_, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject time_signal failed: %v", err)
	}

	mu.Lock()
	data := received
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("no data received by sink")
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode time_signal: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got %d", decoded.CommandType)
	}
	if len(decoded.Descriptors) == 0 {
		t.Fatal("expected at least 1 segmentation descriptor")
	}
	if decoded.Descriptors[0].SegmentationType != 0x34 {
		t.Fatalf("expected segmentation type 0x34, got 0x%02x", decoded.Descriptors[0].SegmentationType)
	}
}

func TestIntegration_CancelEvent_StopsAutoReturn(t *testing.T) {
	// Inject with auto-return, cancel before it fires, verify no cue-in.
	var mu sync.Mutex
	callCount := 0
	sink := func(data []byte) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 300*time.Millisecond, true, true)
	eventID, _ := inj.InjectCue(msg)

	// Cancel immediately (before auto-return fires)
	if err := inj.CancelEvent(eventID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// Wait past original auto-return time
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	c := callCount
	mu.Unlock()

	// Should have exactly 2 calls: inject + cancel (no auto-return cue-in)
	if c != 2 {
		t.Fatalf("expected 2 sink calls (inject + cancel), got %d", c)
	}

	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events")
	}

	// Verify log has "cancelled" entry
	log := inj.EventLog()
	foundCancelled := false
	for _, entry := range log {
		if entry.EventID == eventID && entry.Status == "cancelled" {
			foundCancelled = true
		}
	}
	if !foundCancelled {
		t.Fatal("missing 'cancelled' log entry")
	}
}
