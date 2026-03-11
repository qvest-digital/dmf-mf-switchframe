package scte35

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Integration tests wire real components together (injector + rules engine)
// to verify end-to-end SCTE-35 pipeline behavior.

func TestIntegration_InjectCue_ToMuxer(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	mu.Lock()
	data := received
	mu.Unlock()

	require.NotEmpty(t, data)

	// Decode the received bytes
	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.True(t, decoded.IsOut, "expected IsOut=true")
	require.NotNil(t, decoded.BreakDuration)
	// 30 second break duration (allow small rounding tolerance)
	dur := decoded.BreakDuration.Milliseconds()
	require.True(t, dur >= 29999 && dur <= 30001, "expected ~30000ms break duration, got %d", dur)
}

func TestIntegration_RulesEngine_EvaluatesCue(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	// time_signal should pass through (no matching rule)
	tsMsg := NewTimeSignal(0x34, 30*time.Second, 0x09, []byte("AD123"))
	action, _ = re.Evaluate(tsMsg, "")
	require.Equal(t, ActionPass, action)
}

func TestIntegration_RulesEngine_ReplaceDuration(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionReplace, action)
	require.NotNil(t, modified)
	require.NotNil(t, modified.BreakDuration)
	require.Equal(t, 60*time.Second, *modified.BreakDuration)

	// 20s break should pass through (below threshold)
	msg2 := NewSpliceInsert(0, 20*time.Second, true, true)
	action2, _ := re.Evaluate(msg2, "")
	require.Equal(t, ActionPass, action2)
}

func TestIntegration_AutoReturn_SendsCueIn(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	c := callCount
	data := lastData
	mu.Unlock()

	require.GreaterOrEqual(t, c, 2, "expected at least 2 sink calls (cue-out + cue-in)")
	require.Empty(t, inj.ActiveEventIDs(), "expected no active events after auto-return")

	// Verify the last sink call was a cue-in (IsOut=false)
	decoded, err := Decode(data)
	require.NoError(t, err)
	require.False(t, decoded.IsOut, "expected cue-in (IsOut=false) from auto-return")
}

func TestIntegration_HoldAndExtend(t *testing.T) {
	t.Parallel()
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
	err := inj.HoldBreak(eventID)
	require.NoError(t, err)

	// Wait past original duration
	time.Sleep(400 * time.Millisecond)

	// Should still be active (held)
	require.NotEmpty(t, inj.ActiveEventIDs(), "event should still be active (held)")

	// Verify state shows held
	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "event not in active events")
	require.True(t, active.Held, "expected Held=true in state")

	// Extend with new duration — this should un-hold and set a new timer
	err = inj.ExtendBreak(eventID, 200)
	require.NoError(t, err)

	// Verify the event is no longer held
	state = inj.State()
	active, ok = state.ActiveEvents[eventID]
	require.True(t, ok, "event not in active events after extend")
	require.False(t, active.Held, "expected Held=false after extend")
}

func TestIntegration_SyntheticBreakState(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// No events -> nil
	require.Nil(t, inj.SyntheticBreakState())

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, _ = inj.InjectCue(msg)

	synth := inj.SyntheticBreakState()
	require.NotEmpty(t, synth)

	// Decode and verify it's a valid splice_insert
	decoded, err := Decode(synth)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.True(t, decoded.IsOut, "expected IsOut=true in synthetic state")
	// Synthetic duration should be less than original 60s (time has passed)
	if decoded.BreakDuration != nil {
		dur := decoded.BreakDuration.Milliseconds()
		require.LessOrEqual(t, dur, int64(60000), "synthetic duration %dms should be <= 60000ms", dur)
	}
}

func TestIntegration_ConcurrentEvents(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg1 := NewSpliceInsert(0, 60*time.Second, true, false)
	msg2 := NewSpliceInsert(0, 120*time.Second, true, false)
	id1, _ := inj.InjectCue(msg1)
	id2, _ := inj.InjectCue(msg2)

	require.Len(t, inj.ActiveEventIDs(), 2)

	_ = inj.ReturnToProgram(id1)
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, id2, ids[0])

	_ = inj.ReturnToProgram(id2)
	require.Empty(t, inj.ActiveEventIDs())
}

func TestIntegration_EventLogCaptures(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)
	_ = inj.ReturnToProgram(eventID)

	log := inj.EventLog()
	require.GreaterOrEqual(t, len(log), 2, "expected at least 2 log entries (inject + return)")

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
	require.True(t, foundInjected, "missing 'injected' log entry")
	require.True(t, foundReturned, "missing 'returned' log entry")
}

func TestIntegration_FullLifecycle_InjectHoldExtendReturn(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, eventID, ids[0])

	// Step 2: Hold the break
	err = inj.HoldBreak(eventID)
	require.NoError(t, err)

	// Step 3: Extend with new duration
	err = inj.ExtendBreak(eventID, 60000)
	require.NoError(t, err)

	// Step 4: Manual return
	err = inj.ReturnToProgram(eventID)
	require.NoError(t, err)

	require.Empty(t, inj.ActiveEventIDs())

	// Verify sink calls: inject (cue-out), extend (updated splice_insert), return (cue-in)
	mu.Lock()
	numCalls := len(sinkCalls)
	mu.Unlock()

	require.GreaterOrEqual(t, numCalls, 3, "expected at least 3 sink calls")

	// Verify first call is cue-out
	mu.Lock()
	firstData := sinkCalls[0]
	lastData := sinkCalls[numCalls-1]
	mu.Unlock()

	decoded, err := Decode(firstData)
	require.NoError(t, err)
	require.True(t, decoded.IsOut, "first sink call should be cue-out")

	// Verify last call is cue-in
	decoded, err = Decode(lastData)
	require.NoError(t, err)
	require.False(t, decoded.IsOut, "last sink call should be cue-in")

	// Verify all lifecycle events are logged
	log := inj.EventLog()
	statusSet := make(map[string]bool)
	for _, entry := range log {
		if entry.EventID == eventID {
			statusSet[entry.Status] = true
		}
	}
	for _, expected := range []string{"injected", "held", "extended", "returned"} {
		require.True(t, statusSet[expected], "missing log entry with status %q", expected)
	}
}

func TestIntegration_StateChangeCallback(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, 1, stateChangeCount, "expected 1 state change after inject")
	mu.Unlock()

	_ = inj.HoldBreak(eventID)
	mu.Lock()
	require.Equal(t, 2, stateChangeCount, "expected 2 state changes after hold")
	mu.Unlock()

	_ = inj.ExtendBreak(eventID, 120000)
	mu.Lock()
	require.Equal(t, 3, stateChangeCount, "expected 3 state changes after extend")
	mu.Unlock()

	_ = inj.ReturnToProgram(eventID)
	mu.Lock()
	require.Equal(t, 4, stateChangeCount, "expected 4 state changes after return")
	mu.Unlock()
}

func TestIntegration_RulesEngine_DestinationFiltering(t *testing.T) {
	t.Parallel()
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
	require.Equal(t, ActionDelete, action)

	// Should pass for cdn2 (not in destination list)
	action, _ = re.Evaluate(msg, "cdn2")
	require.Equal(t, ActionPass, action)

	// Should pass for empty destination
	action, _ = re.Evaluate(msg, "")
	require.Equal(t, ActionPass, action)
}

func TestIntegration_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
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
			require.NoError(t, err)

			decoded, err := Decode(encoded)
			require.NoError(t, err)

			require.Equal(t, tt.msg.CommandType, decoded.CommandType)
			require.Equal(t, tt.msg.IsOut, decoded.IsOut)
			require.Equal(t, tt.msg.AutoReturn, decoded.AutoReturn)
		})
	}
}

func TestIntegration_TimeSignal_InjectAndDecode(t *testing.T) {
	t.Parallel()
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
	require.NoError(t, err)

	mu.Lock()
	data := received
	mu.Unlock()

	require.NotEmpty(t, data)

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.NotEmpty(t, decoded.Descriptors)
	require.Equal(t, uint8(0x34), decoded.Descriptors[0].SegmentationType)
}

func TestIntegration_CancelEvent_StopsAutoReturn(t *testing.T) {
	t.Parallel()
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
	err := inj.CancelEvent(eventID)
	require.NoError(t, err)

	// Wait past original auto-return time
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	c := callCount
	mu.Unlock()

	// Should have exactly 2 calls: inject + cancel (no auto-return cue-in)
	require.Equal(t, 2, c, "expected 2 sink calls (inject + cancel)")

	require.Empty(t, inj.ActiveEventIDs())

	// Verify log has "cancelled" entry
	log := inj.EventLog()
	foundCancelled := false
	for _, entry := range log {
		if entry.EventID == eventID && entry.Status == "cancelled" {
			foundCancelled = true
		}
	}
	require.True(t, foundCancelled, "missing 'cancelled' log entry")
}
