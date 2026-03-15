package scte35

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	scte35lib "github.com/Comcast/scte35-go/pkg/scte35"
	"github.com/stretchr/testify/require"
)

func TestInjector_InjectCue_Immediate(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 } // 90s into stream

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0, // disable heartbeat for test
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true) // auto-assign ID
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID, "expected non-zero auto-assigned event ID")

	mu.Lock()
	require.NotEmpty(t, captured, "muxer sink not called")
	mu.Unlock()

	// Verify event appears in log
	log := inj.EventLog()
	require.NotEmpty(t, log, "event log empty")
	require.Equal(t, eventID, log[0].EventID)
}

func TestInjector_DefaultTier(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		DefaultTier:       500, // restricted tier
	}, sink, ptsFn)
	defer inj.Close()

	// Message has Tier=0 (unset) — injector should apply DefaultTier.
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint16(500), decoded.Tier)
}

func TestInjector_DefaultTier_NotOverrideExplicit(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		DefaultTier:       500,
	}, sink, ptsFn)
	defer inj.Close()

	// Message has explicit Tier=200 — should NOT be overridden by DefaultTier.
	msg := NewSpliceInsert(1, 30*time.Second, true, true)
	msg.Tier = 200
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint16(200), decoded.Tier)
}

func TestInjector_InjectCue_Scheduled(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var captured []byte
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		DefaultPreRollMs:  4000,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, err := inj.ScheduleCue(msg, 4000)
	require.NoError(t, err)

	mu.Lock()
	require.NotEmpty(t, captured, "muxer sink not called")
	mu.Unlock()
}

func TestInjector_ReturnToProgram(t *testing.T) {
	t.Parallel()
	callCount := 0
	sink := func(data []byte) { callCount++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false) // no auto-return
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.ReturnToProgram(eventID))

	// Active events should be empty
	require.Len(t, inj.ActiveEventIDs(), 0, "expected no active events after return")
}

func TestInjector_HoldBreak(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true) // auto-return
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.HoldBreak(eventID))

	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "event not in active events")
	require.True(t, active.Held, "expected Held=true")
}

func TestInjector_AutoReturn(t *testing.T) {
	t.Parallel()
	callCount := 0
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 100*time.Millisecond, true, true) // auto-return after 100ms
	_, _ = inj.InjectCue(msg)

	time.Sleep(300 * time.Millisecond) // wait for auto-return to fire

	require.Len(t, inj.ActiveEventIDs(), 0, "expected auto-return to clear active events")
	mu.Lock()
	c := callCount
	mu.Unlock()
	require.GreaterOrEqual(t, c, 2, "expected at least 2 sink calls (cue-out + cue-in)")
}

func TestInjector_ConcurrentEvents(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg1 := NewSpliceInsert(0, 60*time.Second, true, false)
	msg2 := NewSpliceInsert(0, 120*time.Second, true, false)
	id1, _ := inj.InjectCue(msg1)
	id2, _ := inj.InjectCue(msg2)

	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 2)

	// Return first, second still active
	_ = inj.ReturnToProgram(id1)
	ids = inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, id2, ids[0])
}

func TestInjector_SyntheticBreakState(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// No active events -> nil
	require.Nil(t, inj.SyntheticBreakState(), "expected nil synthetic state with no active events")

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, _ = inj.InjectCue(msg)

	synth := inj.SyntheticBreakState()
	require.NotNil(t, synth, "expected non-nil synthetic state during active break")
	require.NotEmpty(t, synth, "expected non-empty synthetic bytes")
}

func TestInjector_Heartbeat(t *testing.T) {
	t.Parallel()
	callCount := 0
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		callCount++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 50 * time.Millisecond,
	}, sink, ptsFn)
	defer inj.Close()

	time.Sleep(180 * time.Millisecond) // should get ~3 heartbeats

	mu.Lock()
	c := callCount
	mu.Unlock()
	require.GreaterOrEqual(t, c, 2, "expected at least 2 heartbeats")
}

func TestInjector_ExtendBreak(t *testing.T) {
	t.Parallel()
	callCount := 0
	sink := func(data []byte) { callCount++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.ExtendBreak(eventID, 120000))

	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "event not in active events after extend")
	require.NotNil(t, active.DurationMs)
	require.Equal(t, int64(120000), *active.DurationMs)
}

func TestInjector_CancelEvent(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		sinkCalls = append(sinkCalls, cp)
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.CancelEvent(eventID))
	require.Len(t, inj.ActiveEventIDs(), 0, "expected no active events after cancel")

	// Verify the cancel message (second sink call) has the cancel indicator set.
	require.GreaterOrEqual(t, len(sinkCalls), 2)
	cancelData := sinkCalls[1]
	decoded, err := Decode(cancelData)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.Equal(t, eventID, decoded.EventID)
	require.True(t, decoded.SpliceEventCancelIndicator, "expected SpliceEventCancelIndicator=true in cancel message")
}

func TestInjector_InjectCue_TimeSignal_PopulatesPTS(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var captured []byte
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 } // 90s at 90kHz

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		VerifyEncoding:    true,
	}, sink, ptsFn)
	defer inj.Close()

	// Create a time_signal with no SpliceTimePTS set (nil).
	// Set Timing="" (not "immediate") so PTS auto-assignment triggers.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))
	msg.Timing = "" // non-immediate: PTS should be auto-assigned
	require.Nil(t, msg.SpliceTimePTS, "expected SpliceTimePTS to be nil before injection")

	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Decode the captured sink data and verify PTS was populated.
	mu.Lock()
	data := captured
	mu.Unlock()

	require.NotEmpty(t, data, "muxer sink not called")

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.NotNil(t, decoded.SpliceTimePTS, "expected SpliceTimePTS to be populated by injector")
	require.Equal(t, int64(8100000), *decoded.SpliceTimePTS)
}

func TestInjector_InjectCue_TimeSignal_ImmediateNoPTS(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var captured []byte
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
	}, sink, ptsFn)
	defer inj.Close()

	// Create a time_signal with Timing="immediate" -- PTS should NOT be assigned.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test"))
	// NewTimeSignal sets Timing="immediate" by default
	require.Equal(t, "immediate", msg.Timing)

	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	data := captured
	mu.Unlock()

	require.NotEmpty(t, data, "muxer sink not called")

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	// Immediate time_signal should have time_specified_flag=0 (no PTS).
	require.Nil(t, decoded.SpliceTimePTS, "expected SpliceTimePTS=nil for immediate time_signal")
}

func TestInjector_CancelSegmentationEvent(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject something first so the injector isn't completely bare.
	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	_, _ = inj.InjectCue(msg)

	// Cancel segmentation event 42.
	require.NoError(t, inj.CancelSegmentationEvent(42, "api"))

	// Verify sink was called with the cancel message (second call after the inject).
	require.GreaterOrEqual(t, len(sinkCalls), 2)
	cancelData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(cancelData)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.True(t, d.SegmentationEventCancelIndicator, "expected SegmentationEventCancelIndicator=true")
	require.Equal(t, uint32(42), d.SegEventID)

	// Verify event log has a "cancelled" entry.
	log := inj.EventLog()
	found := false
	for _, entry := range log {
		if entry.EventID == 42 && entry.Status == "cancelled" {
			found = true
			break
		}
	}
	require.True(t, found, "expected event log entry with eventID=42 and status=cancelled")
}

func TestInjector_CancelSegmentationEvent_SCTE104EchoPrevention(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Register an SCTE-104 sink and track whether it's called.
	var s104Calls int
	inj.SetSCTE104Sink(func(msg *CueMessage) {
		s104Calls++
	})

	// Cancel with source="scte104" -- should NOT fire SCTE-104 sink.
	require.NoError(t, inj.CancelSegmentationEvent(100, "scte104"))
	require.Zero(t, s104Calls, "SCTE-104 sink called, expected 0 (echo prevention)")

	// Cancel with source="api" -- SHOULD fire SCTE-104 sink.
	require.NoError(t, inj.CancelSegmentationEvent(200, "api"))
	require.Equal(t, 1, s104Calls)
}

func TestInjector_TimeSignal_AutoAssignSegEventID(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with SegEventID=0 -- should be auto-assigned.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 0, UPIDType: 0x0F, UPID: []byte("auto")},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID, "expected non-zero auto-assigned eventID")

	// Verify active events contain the assigned ID.
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, eventID, ids[0])

	// Verify the encoded message has the assigned SegEventID (not 0).
	decoded, err := Decode(sinkCalls[0])
	require.NoError(t, err)
	require.Len(t, decoded.Descriptors, 1)
	require.NotZero(t, decoded.Descriptors[0].SegEventID, "expected non-zero SegEventID in encoded message")
}

func TestInjector_TimeSignal_ExplicitSegEventID_Preserved(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with an explicit SegEventID.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 9999, UPIDType: 0x0F, UPID: []byte("explicit")},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, uint32(9999), eventID)

	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, uint32(9999), ids[0])
}

func TestInjector_WebhookDispatch(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var received []WebhookEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("webhook decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		WebhookURL:        ts.URL,
		WebhookTimeoutMs:  5000,
	}, sink, ptsFn)
	defer inj.Close()

	// 1. Inject cue-out
	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// 2. Hold the break
	require.NoError(t, inj.HoldBreak(eventID))

	// 3. Extend the break
	require.NoError(t, inj.ExtendBreak(eventID, 120000))

	// 4. Return to program
	require.NoError(t, inj.ReturnToProgram(eventID))

	// Wait for async dispatches to complete.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, received, 4)

	// Webhook dispatches are async goroutines, so verify all expected types
	// are present and each has the correct event ID. The first event (cue_out)
	// is always first since subsequent actions depend on it.
	typeCounts := make(map[string]int)
	eventsByType := make(map[string]WebhookEvent)
	for _, evt := range received {
		typeCounts[evt.Type]++
		eventsByType[evt.Type] = evt
		require.Equal(t, eventID, evt.EventID, "event type %q has wrong eventId", evt.Type)
	}

	for _, wantType := range []string{"cue_out", "hold", "extend", "cue_in"} {
		require.Equal(t, 1, typeCounts[wantType], "expected exactly 1 %q event", wantType)
	}

	// Verify cue_out has correct fields.
	cueOut := eventsByType["cue_out"]
	require.True(t, cueOut.IsOut, "cue_out event should have isOut=true")
	require.Equal(t, int64(60000), cueOut.Duration)
	require.Equal(t, "splice_insert", cueOut.Command)

	// Verify extend has new duration.
	extendEvt := eventsByType["extend"]
	require.Equal(t, int64(120000), extendEvt.Duration)

	// Verify cue_in is not out.
	cueIn := eventsByType["cue_in"]
	require.False(t, cueIn.IsOut, "cue_in event should have isOut=false")
}

func TestInjector_WebhookDispatch_Cancel(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var received []WebhookEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("webhook decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		WebhookURL:        ts.URL,
	}, sink, ptsFn)
	defer inj.Close()

	// Inject, then cancel.
	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	require.NoError(t, inj.CancelEvent(eventID))

	// Wait for async dispatches.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, received, 2)

	// Webhook dispatches are async goroutines, so verify all expected types
	// are present rather than relying on delivery order.
	typeCounts := make(map[string]int)
	for _, evt := range received {
		typeCounts[evt.Type]++
		require.Equal(t, eventID, evt.EventID, "event type %q has wrong eventId", evt.Type)
	}
	require.Equal(t, 1, typeCounts["cue_out"])
	require.Equal(t, 1, typeCounts["cancel"])
}

func TestInjector_WebhookNilSafe(t *testing.T) {
	t.Parallel()
	// Ensure no webhook is dispatched when WebhookURL is empty.
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		// WebhookURL intentionally empty
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)
	// Just verifying no panic occurs with nil webhook.
}

func TestInjector_Rules_Pass(t *testing.T) {
	t.Parallel()
	var sinkCalls int
	sink := func(data []byte) { sinkCalls++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Create a rule that matches nothing (event_id=99999).
	re := NewRuleEngine()
	re.SetDefaultAction(ActionPass)
	re.AddRule(Rule{
		ID:      "no-match",
		Name:    "No match rule",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "event_id", Operator: "=", Value: "99999"},
		},
		Logic:  LogicAND,
		Action: ActionDelete,
	})
	inj.SetRuleEngine(re)

	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID)
	require.NotZero(t, sinkCalls, "expected sink to be called (rule should not match)")
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
}

func TestInjector_Rules_Delete(t *testing.T) {
	t.Parallel()
	var sinkCalls int
	sink := func(data []byte) { sinkCalls++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Rule: delete short avails under 15s (duration < 15000ms).
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "delete-short",
		Name:    "Delete short avails",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "duration", Operator: "<", Value: "15000"},
		},
		Logic:  LogicAND,
		Action: ActionDelete,
	})
	inj.SetRuleEngine(re)

	// Inject a 10s splice_insert -- should be deleted by the rule.
	msg := NewSpliceInsert(0, 10*time.Second, true, false)
	eventID, err := inj.InjectCue(msg)

	// Delete is not an error.
	require.NoError(t, err)
	// Deleted cue returns 0 event ID.
	require.Zero(t, eventID, "expected 0 event ID for deleted cue")
	// Sink should NOT have been called.
	require.Zero(t, sinkCalls, "expected 0 sink calls for deleted cue")
	// No active events.
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 0)
}

func TestInjector_LogEventPopulatesFields(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a splice_insert with descriptors, avail fields, and SpliceTimePTS.
	dur := 30 * time.Second
	spliceTimePTS := int64(8100000)
	msg := &CueMessage{
		CommandType:    CommandSpliceInsert,
		IsOut:          true,
		AutoReturn:     true,
		BreakDuration:  &dur,
		SpliceTimePTS:  &spliceTimePTS,
		AvailNum:       3,
		AvailsExpected: 5,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       1001,
				UPIDType:         0x0F,
				UPID:             []byte("https://example.com/ad"),
			},
		},
	}

	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	log := inj.EventLog()
	require.NotEmpty(t, log, "event log empty")

	entry := log[0]
	require.Equal(t, eventID, entry.EventID)

	// Verify Source is populated.
	require.Equal(t, "injector", entry.Source)

	// Verify AvailNum is populated.
	require.Equal(t, uint8(3), entry.AvailNum)

	// Verify AvailsExpected is populated.
	require.Equal(t, uint8(5), entry.AvailsExpected)

	// Verify SpliceTimePTS is populated.
	require.NotNil(t, entry.SpliceTimePTS)
	require.Equal(t, int64(8100000), *entry.SpliceTimePTS)

	// Verify Descriptors are populated.
	require.Len(t, entry.Descriptors, 1)
	d := entry.Descriptors[0]
	require.Equal(t, uint8(0x34), d.SegmentationType)
	require.Equal(t, uint32(1001), d.SegEventID)
}

func TestInjector_LogEventPopulatesFields_Return(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.ReturnToProgram(eventID))

	log := inj.EventLog()
	// Find the "returned" entry.
	var returnEntry *EventLogEntry
	for i := range log {
		if log[i].Status == "returned" {
			returnEntry = &log[i]
			break
		}
	}
	require.NotNil(t, returnEntry, "expected returned entry in event log")
	require.Equal(t, "injector", returnEntry.Source)
}

func TestInjector_LogEventPopulatesFields_Cancel(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.CancelEvent(eventID))

	log := inj.EventLog()
	var cancelEntry *EventLogEntry
	for i := range log {
		if log[i].Status == "cancelled" {
			cancelEntry = &log[i]
			break
		}
	}
	require.NotNil(t, cancelEntry, "expected cancelled entry in event log")
	require.Equal(t, "injector", cancelEntry.Source)
}

func TestInjector_Rules_Replace(t *testing.T) {
	t.Parallel()
	var captured []byte
	sink := func(data []byte) {
		captured = append([]byte(nil), data...)
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Rule: replace duration to 120s for all cue-out messages.
	replaceDur := 120 * time.Second
	re := NewRuleEngine()
	re.AddRule(Rule{
		ID:      "replace-duration",
		Name:    "Standardize break duration",
		Enabled: true,
		Conditions: []RuleCondition{
			{Field: "is_out", Operator: "=", Value: "true"},
		},
		Logic:  LogicAND,
		Action: ActionReplace,
		ReplaceWith: &ReplaceParams{
			Duration: &replaceDur,
		},
	})
	inj.SetRuleEngine(re)

	// Inject a 30s splice_insert -- should be replaced to 120s.
	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Verify sink was called.
	require.NotEmpty(t, captured, "sink was not called")

	// Decode the captured data and verify the duration was replaced.
	decoded, err := Decode(captured)
	require.NoError(t, err)
	require.NotNil(t, decoded.BreakDuration, "expected BreakDuration to be set in decoded message")
	// 120s = 120000ms
	require.Equal(t, int64(120000), decoded.BreakDuration.Milliseconds())
}

func TestInjector_SCTE104Sink_InjectCue(t *testing.T) {
	t.Parallel()
	var muxerCalled bool
	var scte104Msg *CueMessage
	var mu sync.Mutex

	sink := func(data []byte) {
		mu.Lock()
		muxerCalled = true
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Msg = msg
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	msg.Source = "api"
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.True(t, muxerCalled, "expected muxerSink to be called")
	require.NotNil(t, scte104Msg, "expected scte104Sink to be called")
	require.Equal(t, uint8(CommandSpliceInsert), scte104Msg.CommandType)
	require.True(t, scte104Msg.IsOut, "expected IsOut=true in scte104 message")
	require.Equal(t, "api", scte104Msg.Source)
}

func TestInjector_SCTE104Sink_ReturnToProgram(t *testing.T) {
	t.Parallel()
	var scte104Msg *CueMessage
	var mu sync.Mutex

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Msg = msg
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	// Reset to capture only the return message.
	mu.Lock()
	scte104Msg = nil
	mu.Unlock()

	require.NoError(t, inj.ReturnToProgram(eventID))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, scte104Msg, "expected scte104Sink to be called on ReturnToProgram")
	require.Equal(t, uint8(CommandSpliceInsert), scte104Msg.CommandType)
	require.False(t, scte104Msg.IsOut, "expected IsOut=false for cue-in")
}

func TestInjector_SCTE104Sink_CancelEvent(t *testing.T) {
	t.Parallel()
	var scte104Msg *CueMessage
	var mu sync.Mutex

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Msg = msg
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	// Reset to capture only the cancel message.
	mu.Lock()
	scte104Msg = nil
	mu.Unlock()

	require.NoError(t, inj.CancelEvent(eventID))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, scte104Msg, "expected scte104Sink to be called on CancelEvent")
	require.Equal(t, uint8(CommandSpliceInsert), scte104Msg.CommandType)
	require.True(t, scte104Msg.SpliceEventCancelIndicator, "expected SpliceEventCancelIndicator=true")
	require.Equal(t, eventID, scte104Msg.EventID)
}

func TestInjector_MsgSource_InEventLog(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject with Source="macro"
	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	msg.Source = "macro"
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	log := inj.EventLog()
	require.NotEmpty(t, log)

	var found bool
	for _, entry := range log {
		if entry.EventID == eventID && entry.Source == "macro" {
			found = true
			break
		}
	}
	require.True(t, found, "expected event log entry with Source='macro'")

	// Inject without Source set -- should default to "injector"
	msg2 := NewSpliceInsert(0, 30*time.Second, true, false)
	eventID2, err := inj.InjectCue(msg2)
	require.NoError(t, err)

	log = inj.EventLog()
	for _, entry := range log {
		if entry.EventID == eventID2 && entry.Source == "injector" {
			return // success
		}
	}
	require.Fail(t, "expected event log entry with default Source='injector'")
}

func TestInjector_TimeSignal_CueOut_TrackedAsActive(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	// time_signal with Provider Placement Opportunity Start (0x34) should track.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 42},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 1)
	require.Equal(t, uint32(42), ids[0])
}

func TestInjector_TimeSignal_NonCueOut_NotTracked(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	// time_signal with Program Start (0x10) is not a cue-out.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x10, SegEventID: 42},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 0)
}

func TestInjector_ScheduleCue_PreRollRepeat(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sinkCalls int
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	_, err := inj.ScheduleCue(msg, 3000) // 3s pre-roll
	require.NoError(t, err)

	// Wait for at least one pre-roll repeat.
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	calls := sinkCalls
	mu.Unlock()

	// Initial inject (1) + at least 1 repeat = >= 2
	require.GreaterOrEqual(t, calls, 2, "expected at least 2 sink calls (initial + repeat)")
}

func TestInjector_DefaultPID(t *testing.T) {
	t.Parallel()
	inj := NewInjector(InjectorConfig{}, func([]byte) {}, func() int64 { return 0 })
	defer inj.Close()

	require.Equal(t, uint16(0x102), inj.config.SCTE35PID)
}

func TestInjector_ReturnToProgram_TimeSignal_SendsEndType(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with Provider PO Start (0x34).
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 100, UPIDType: 0x0F, UPID: []byte("test")},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Return to program -- should send time_signal with End type (0x35).
	require.NoError(t, inj.ReturnToProgram(100))

	// Decode the return message (last sink call).
	require.GreaterOrEqual(t, len(sinkCalls), 2)
	returnData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(returnData)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	require.Equal(t, uint8(0x35), decoded.Descriptors[0].SegmentationType)
}

func TestInjector_IsSegCueOut_AllTypes(t *testing.T) {
	t.Parallel()
	// All ad insertion Start types from SCTE-35 Table 22.
	cueOutTypes := []uint8{
		0x22, // Break Start
		0x30, // Provider Advertisement Start
		0x32, // Distributor Advertisement Start
		0x34, // Provider Placement Opportunity Start
		0x36, // Distributor Placement Opportunity Start
		0x38, // Provider Overlay Placement Opportunity Start
		0x3A, // Distributor Overlay Placement Opportunity Start
		0x3C, // Provider Promo Start
		0x3E, // Distributor Promo Start
		0x40, // Unscheduled Event Start
		0x42, // Alternate Content Opportunity Start
		0x44, // Provider Ad Block Start
		0x46, // Distributor Ad Block Start
	}
	for _, typeID := range cueOutTypes {
		require.True(t, isSegCueOut(typeID), "expected 0x%02x to be cue-out", typeID)
	}
	// Corresponding End types should NOT be cue-out.
	cueInTypes := []uint8{0x23, 0x31, 0x33, 0x35, 0x37, 0x39, 0x3B, 0x3D, 0x3F, 0x41, 0x43, 0x45, 0x47}
	for _, typeID := range cueInTypes {
		require.False(t, isSegCueOut(typeID), "expected 0x%02x (End type) to not be cue-out", typeID)
	}
	// Non-ad types should not be cue-out.
	require.False(t, isSegCueOut(0x10), "expected 0x10 (Program Start) to not be cue-out")
	require.False(t, isSegCueOut(0x20), "expected 0x20 (Chapter Start) to not be cue-out")
	require.False(t, isSegCueOut(0x50), "expected 0x50 (Network Start) to not be cue-out")
}

func TestInjector_TimeSignal_AutoReturn(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sinkCalls int
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with a short duration (100ms) so auto-return fires quickly.
	ticks := scte35lib.DurationToTicks(100 * time.Millisecond)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Timing:      "immediate",
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       500,
				SegmentationType: 0x34, // Provider Placement Opportunity Start
				DurationTicks:    &ticks,
			},
		},
	}

	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID)

	// Verify event is active and has AutoReturn set.
	ids := inj.ActiveEventIDs()
	require.NotEmpty(t, ids, "expected active events")

	state := inj.State()
	ae, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "event %d not in active events", eventID)
	require.True(t, ae.AutoReturn, "AutoReturn should be true for time_signal with duration")

	// Wait for auto-return to fire.
	time.Sleep(300 * time.Millisecond)

	// Event should be cleared.
	ids = inj.ActiveEventIDs()
	require.Len(t, ids, 0, "expected no active events after auto-return")

	// Check the log contains both "injected" and "returned" entries.
	log := inj.EventLog()
	var foundInjected, foundReturned bool
	for _, e := range log {
		if e.Status == "injected" {
			foundInjected = true
		}
		if e.Status == "returned" {
			foundReturned = true
		}
	}
	require.True(t, foundInjected, "expected 'injected' log entry")
	require.True(t, foundReturned, "expected 'returned' log entry from auto-return timer")
}

func TestInjector_TimeSignal_NoDuration_NoAutoReturn(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	// Time_signal without duration should NOT get auto-return.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Timing:      "immediate",
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       600,
				SegmentationType: 0x34,
				// No DurationTicks
			},
		},
	}

	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	state := inj.State()
	ae, ok := state.ActiveEvents[600]
	require.True(t, ok, "event should be active")
	require.False(t, ae.AutoReturn, "AutoReturn should be false when no duration specified")
}

func TestInjector_ReturnToProgram_DeliveryRestrictions(t *testing.T) {
	t.Parallel()
	var lastData []byte
	sink := func(data []byte) {
		lastData = append([]byte(nil), data...)
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{}, sink, ptsFn)
	defer inj.Close()

	ticks := scte35lib.DurationToTicks(30 * time.Second)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Timing:      "immediate",
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       700,
				SegmentationType: 0x34,
				DurationTicks:    &ticks,
				DeliveryRestrictions: &DeliveryRestrictions{
					WebDeliveryAllowed: true,
					NoRegionalBlackout: true,
					ArchiveAllowed:     false,
					DeviceRestrictions: 2,
				},
			},
		},
	}

	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Return to program -- the End descriptor should carry DeliveryRestrictions.
	require.NoError(t, inj.ReturnToProgram(eventID))

	// Decode the return message.
	decoded, err := Decode(lastData)
	require.NoError(t, err)
	require.NotEmpty(t, decoded.Descriptors, "expected descriptors in return message")
	dr := decoded.Descriptors[0].DeliveryRestrictions
	require.NotNil(t, dr, "expected DeliveryRestrictions on End descriptor")
	require.True(t, dr.WebDeliveryAllowed, "WebDeliveryAllowed should be true")
	require.True(t, dr.NoRegionalBlackout, "NoRegionalBlackout should be true")
	require.False(t, dr.ArchiveAllowed, "ArchiveAllowed should be false")
	require.Equal(t, uint8(2), dr.DeviceRestrictions)
}

func TestInjector_HoldBreak_CancelsPreRoll(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sinkCalls int
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	eventID, err := inj.ScheduleCue(msg, 5000) // 5s pre-roll
	require.NoError(t, err)

	// Hold immediately -- should cancel pre-roll.
	require.NoError(t, inj.HoldBreak(eventID))

	mu.Lock()
	callsBefore := sinkCalls
	mu.Unlock()

	// Wait and verify no more pre-roll repeats.
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	callsAfter := sinkCalls
	mu.Unlock()

	require.Equal(t, callsBefore, callsAfter, "expected no more sink calls after hold")
}

func TestInjector_ScheduleCue_TimeSignal_PreRollRepeat(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sinkCalls int
	sink := func(data []byte) {
		mu.Lock()
		sinkCalls++
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Schedule a time_signal with cue-out descriptor and 3s pre-roll.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 200},
		},
	}
	eventID, err := inj.ScheduleCue(msg, 3000)
	require.NoError(t, err)

	// eventID should be the SegEventID, not 0.
	require.Equal(t, uint32(200), eventID)

	// Wait for at least one pre-roll repeat.
	time.Sleep(1500 * time.Millisecond)

	mu.Lock()
	calls := sinkCalls
	mu.Unlock()

	// Initial inject (1) + at least 1 repeat = >= 2
	require.GreaterOrEqual(t, calls, 2, "expected at least 2 sink calls (initial + repeat)")
}

func TestInjector_InjectCue_TimeSignal_ReturnsSegEventID(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 555},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Should return the SegEventID, not 0.
	require.Equal(t, uint32(555), eventID)
}

func TestInjector_ReturnToProgram_TimeSignal_PreservesSegNum(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with SegNum/SegExpected.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       300,
				UPIDType:         0x0F,
				UPID:             []byte("test"),
				SegNum:           2,
				SegExpected:      4,
			},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	require.NoError(t, inj.ReturnToProgram(300))

	// Decode the return message.
	returnData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(returnData)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.Equal(t, uint8(0x35), d.SegmentationType)
	require.Equal(t, uint8(2), d.SegNum)
	require.Equal(t, uint8(4), d.SegExpected)
}

func TestInjector_CancelEvent_TimeSignal_SendsSegmentationCancel(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with Provider PO Start (0x34) -- this creates an active event.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 500, UPIDType: 0x0F, UPID: []byte("test")},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, uint32(500), eventID)

	// Cancel the time_signal event -- should send time_signal with
	// segmentation_event_cancel_indicator, NOT splice_insert cancel.
	require.NoError(t, inj.CancelEvent(500))

	// Should have no active events.
	require.Len(t, inj.ActiveEventIDs(), 0, "expected no active events after cancel")

	// Decode the cancel message (last sink call).
	require.GreaterOrEqual(t, len(sinkCalls), 2)
	cancelData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(cancelData)
	require.NoError(t, err)

	// Must be time_signal, not splice_insert.
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)

	// Must have exactly one descriptor with the cancel indicator set.
	require.Len(t, decoded.Descriptors, 1)
	d := decoded.Descriptors[0]
	require.True(t, d.SegmentationEventCancelIndicator, "expected SegmentationEventCancelIndicator=true in cancel message")
	require.Equal(t, uint32(500), d.SegEventID)

	// Verify event log has a "cancelled" entry with command type "time_signal".
	log := inj.EventLog()
	var cancelEntry *EventLogEntry
	for i := range log {
		if log[i].EventID == 500 && log[i].Status == "cancelled" {
			cancelEntry = &log[i]
			break
		}
	}
	require.NotNil(t, cancelEntry, "expected cancelled entry in event log")
	require.Equal(t, "time_signal", cancelEntry.CommandType)
}

func TestInjector_CancelEvent_TimeSignal_SCTE104Sink(t *testing.T) {
	t.Parallel()
	var scte104Msg *CueMessage
	var mu sync.Mutex

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Msg = msg
		mu.Unlock()
	})

	// Inject time_signal cue-out.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 600},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Reset to capture only the cancel message.
	mu.Lock()
	scte104Msg = nil
	mu.Unlock()

	// Cancel the event.
	require.NoError(t, inj.CancelEvent(600))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, scte104Msg, "expected scte104Sink to be called on CancelEvent")
	// SCTE-104 sink should receive a time_signal, not splice_insert.
	require.Equal(t, uint8(CommandTimeSignal), scte104Msg.CommandType)
	require.Len(t, scte104Msg.Descriptors, 1)
	require.True(t, scte104Msg.Descriptors[0].SegmentationEventCancelIndicator, "expected SegmentationEventCancelIndicator=true in SCTE-104 cancel message")
}

func TestInjector_CancelEvent_TimeSignal_MultipleDescriptors(t *testing.T) {
	t.Parallel()
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with two cue-out descriptors.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 700, UPIDType: 0x0F, UPID: []byte("ad1")},
			{SegmentationType: 0x36, SegEventID: 701, UPIDType: 0x0F, UPID: []byte("ad2")},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Cancel event 700.
	require.NoError(t, inj.CancelEvent(700))

	// Decode the cancel message.
	cancelData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(cancelData)
	require.NoError(t, err)

	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)

	// Should have cancel descriptors for all descriptors from the original event.
	require.Len(t, decoded.Descriptors, 2)
	for i, d := range decoded.Descriptors {
		require.True(t, d.SegmentationEventCancelIndicator, "descriptor %d: expected cancel indicator", i)
	}
}

func TestInjector_CancelEvent_SpliceInsert_StillWorks(t *testing.T) {
	t.Parallel()
	// Verify the original splice_insert cancel path is preserved.
	var sinkCalls [][]byte
	sink := func(data []byte) {
		sinkCalls = append(sinkCalls, append([]byte(nil), data...))
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	require.NoError(t, inj.CancelEvent(eventID))

	// Decode the cancel message.
	cancelData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(cancelData)
	require.NoError(t, err)

	// Must still be splice_insert cancel for splice_insert events.
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
	require.True(t, decoded.SpliceEventCancelIndicator, "expected SpliceEventCancelIndicator=true")
}

func TestWebhook_DispatchAfterClose(t *testing.T) {
	t.Parallel()
	wh := NewWebhookDispatcher("http://127.0.0.1:1", 100*time.Millisecond)
	wh.Close()

	// Dispatch after Close should not panic.
	wh.Dispatch(WebhookEvent{Type: "test", EventID: 1})
}

func TestInjector_ScheduleCue_PTSWraparound(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	// PTS near 33-bit max (2^33 - 90000 = 8589844592)
	nearMax := int64(1<<33 - 90000)
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return nearMax }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	eventID, err := inj.ScheduleCue(msg, 2000) // 2 second preroll -> PTS wraps
	require.NoError(t, err)

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.NotNil(t, decoded.SpliceTimePTS, "expected SpliceTimePTS to be set")

	// PTS must be masked to 33 bits.
	pts := *decoded.SpliceTimePTS
	require.True(t, pts >= 0 && pts < 1<<33, "PTS %d exceeds 33-bit range", pts)

	// Verify wraparound: nearMax + 2000*90 = 8589844592 + 180000 = 8590024592
	// Masked: 8590024592 & 0x1FFFFFFFF = 8590024592 - 8589934592 = 90000
	expected := int64((nearMax + 2000*90) & 0x1FFFFFFFF)
	require.Equal(t, expected, pts)

	// Verify the in-memory state also stores the masked PTS (not raw overflow).
	state := inj.State()
	ae, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "expected active event in state")
	require.True(t, ae.SpliceTimePTS >= 0 && ae.SpliceTimePTS < 1<<33, "State().SpliceTimePTS %d exceeds 33-bit range", ae.SpliceTimePTS)
}

func TestInjector_PreRoll_PTSWrap33Bit(t *testing.T) {
	t.Parallel()

	// PTS near the 33-bit wrap boundary: 2^33 - 90000 (~1 second before wrap).
	// With a 2-second pre-roll, SpliceTimePTS wraps to a small value (~90000)
	// while currentPTS is near 2^33. The old code used a simple > comparison
	// which fails because 90000 > 8589844592 is false, skipping the pre-roll.
	nearMax := int64(1<<33 - 90000) // 8589844592

	sink := func(data []byte) {}
	ptsFn := func() int64 { return nearMax }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Create a splice_insert with auto-return, 200ms break duration,
	// and a SpliceTimePTS that wraps past 2^33.
	preRollTicks := int64(180000) // 2 seconds in 90kHz ticks
	spliceTimePTS := maskPTS33(nearMax + preRollTicks)
	breakDur := 200 * time.Millisecond

	msg := NewSpliceInsert(0, breakDur, true, true) // auto-return
	msg.SpliceTimePTS = &spliceTimePTS

	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.NotZero(t, eventID)

	// The auto-return timer should be set to breakDur + pre-roll time.
	// Pre-roll = 180000 ticks / 90000 = 2 seconds.
	// Total timer = 200ms + 2s = 2.2s.
	// Without the fix, the timer would be set to just 200ms (pre-roll skipped).

	// Verify the event is still active immediately (should not have returned yet).
	require.Len(t, inj.ActiveEventIDs(), 1, "event should still be active")

	// Wait long enough for breakDur alone (200ms) but not breakDur + preRoll (2.2s).
	time.Sleep(500 * time.Millisecond)

	// With the fix: event should still be active (timer = 2.2s, only 500ms elapsed).
	// Without the fix: event would already be returned (timer = 200ms, 500ms elapsed).
	activeIDs := inj.ActiveEventIDs()
	require.Len(t, activeIDs, 1, "event should still be active after 500ms "+
		"because pre-roll (2s) is included in auto-return delay; "+
		"if this fails, PTS wrap pre-roll calculation is broken")
}

func TestInjector_InjectCue_TimeSignal_PTSWraparound(t *testing.T) {
	t.Parallel()
	// PTS that exceeds 33-bit range to verify masking.
	overMax := int64(1<<33 + 5000)
	sink := func(data []byte) {}
	ptsFn := func() int64 { return overMax }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 1},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Verify the in-memory state stores the masked PTS.
	state := inj.State()
	ae, ok := state.ActiveEvents[eventID]
	require.True(t, ok, "expected active event in state")
	require.True(t, ae.SpliceTimePTS >= 0 && ae.SpliceTimePTS < 1<<33, "State().SpliceTimePTS %d exceeds 33-bit range", ae.SpliceTimePTS)

	// The masked value should be 5000 (overMax & 0x1FFFFFFFF).
	expected := int64(overMax & 0x1FFFFFFFF)
	require.Equal(t, expected, ae.SpliceTimePTS)
}

func TestInjector_SCTE104Sink_SkipsEchoForSCTE104Source(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	// Inject with Source="scte104" -- simulates SCTE-104 input path.
	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	msg.Source = "scte104"
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.False(t, scte104Called, "scte104Sink should NOT be called when Source='scte104' (echo prevention)")
}

func TestInjector_SCTE104Sink_FiresForAPISource(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	msg.Source = "api"
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.True(t, scte104Called, "scte104Sink should be called when Source='api'")
}

func TestInjector_ExtendBreak_TimeSignal(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with Provider PO Start (0x34).
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       500,
				UPIDType:         0x0F,
				UPID:             []byte("test-upid"),
			},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Extend the break.
	require.NoError(t, inj.ExtendBreak(eventID, 60000))

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)

	// The extended message must be a time_signal, not splice_insert.
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	require.Equal(t, uint8(0x34), decoded.Descriptors[0].SegmentationType)
	require.NotNil(t, decoded.Descriptors[0].DurationTicks, "expected DurationTicks on descriptor")
}

func TestInjector_ExtendBreak_SpliceInsert_Unchanged(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, true)
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	require.NoError(t, inj.ExtendBreak(eventID, 60000))

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)

	// splice_insert events must still produce splice_insert on extend.
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
}

func TestInjector_SyntheticBreakState_TimeSignal(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject with a 60-second descriptor duration.
	dur60s := uint64(60 * 90000) // 60 seconds in 90kHz ticks
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegmentationType: 0x34,
				SegEventID:       300,
				UPIDType:         0x0F,
				UPID:             []byte("test"),
				DurationTicks:    &dur60s,
			},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Small sleep to ensure some time has elapsed.
	time.Sleep(10 * time.Millisecond)

	synth := inj.SyntheticBreakState()
	require.NotNil(t, synth, "expected non-nil synthetic state")

	decoded, err := Decode(synth)
	require.NoError(t, err)

	// Must be time_signal, not splice_insert.
	require.Equal(t, uint8(CommandTimeSignal), decoded.CommandType)
	require.Len(t, decoded.Descriptors, 1)
	require.Equal(t, uint8(0x34), decoded.Descriptors[0].SegmentationType)

	// Verify the duration ticks have been adjusted down from the original.
	// The synthetic state should reflect remaining time, not the original duration.
	require.NotNil(t, decoded.Descriptors[0].DurationTicks, "expected DurationTicks on synthetic descriptor")
	synthTicks := *decoded.Descriptors[0].DurationTicks
	require.Less(t, synthTicks, dur60s, "synthetic DurationTicks should be less than original")
}

func TestInjector_SyntheticBreakState_SpliceInsert(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, _ = inj.InjectCue(msg)

	synth := inj.SyntheticBreakState()
	require.NotNil(t, synth, "expected non-nil synthetic state")

	decoded, err := Decode(synth)
	require.NoError(t, err)
	require.Equal(t, uint8(CommandSpliceInsert), decoded.CommandType)
}

// Bug 1: ReturnToProgram with multi-descriptor time_signal should clean all sibling entries.
func TestReturnToProgram_MultiDescriptor_CleansAllEntries(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with two cue-out descriptors (0x34 and 0x36).
	// Each descriptor gets its own active event entry.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 1000, UPIDType: 0x0F, UPID: []byte("ad1")},
			{SegmentationType: 0x36, SegEventID: 1001, UPIDType: 0x0F, UPID: []byte("ad2")},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Both descriptors should be tracked.
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 2)

	// Return to program using the first descriptor's event ID.
	require.NoError(t, inj.ReturnToProgram(1000))

	// Both sibling entries must be cleaned up -- no orphaned events.
	ids = inj.ActiveEventIDs()
	require.Len(t, ids, 0, "expected 0 active events after return (orphaned siblings)")
}

// Bug 3: CancelEvent with multi-descriptor time_signal should clean all sibling entries.
func TestCancelEvent_MultiDescriptor_CleansAllEntries(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with two cue-out descriptors.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 2000, UPIDType: 0x0F, UPID: []byte("ad1")},
			{SegmentationType: 0x36, SegEventID: 2001, UPIDType: 0x0F, UPID: []byte("ad2")},
		},
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Both descriptors should be tracked.
	ids := inj.ActiveEventIDs()
	require.Len(t, ids, 2)

	// Cancel using the second descriptor's event ID.
	require.NoError(t, inj.CancelEvent(2001))

	// Both sibling entries must be cleaned up -- no orphaned events.
	ids = inj.ActiveEventIDs()
	require.Len(t, ids, 0, "expected 0 active events after cancel (orphaned siblings)")
}

// Bug 2: ExtendBreak should use scte35lib.DurationToTicks for precise tick conversion.
func TestExtendBreak_DurationTicksPrecision(t *testing.T) {
	t.Parallel()
	var captured []byte
	var mu sync.Mutex
	sink := func(data []byte) {
		mu.Lock()
		captured = append([]byte(nil), data...)
		mu.Unlock()
	}
	ptsFn := func() int64 { return 90000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with a cue-out descriptor.
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{SegmentationType: 0x34, SegEventID: 3000, UPIDType: 0x0F, UPID: []byte("test")},
		},
	}
	eventID, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Extend with 33333ms -- a value where float64 truncation vs math.Round
	// would produce different results.
	require.NoError(t, inj.ExtendBreak(eventID, 33333))

	mu.Lock()
	data := captured
	mu.Unlock()

	decoded, err := Decode(data)
	require.NoError(t, err)

	require.Len(t, decoded.Descriptors, 1)
	require.NotNil(t, decoded.Descriptors[0].DurationTicks, "expected DurationTicks on descriptor")

	// scte35lib.DurationToTicks(33333ms) uses math.Round internally.
	// 33.333s * 90000 = 2999970 ticks (both float and round agree here,
	// but the key point is we're using the library function, not hand-rolled).
	dur := 33333 * time.Millisecond
	expected := scte35lib.DurationToTicks(dur)
	got := *decoded.Descriptors[0].DurationTicks
	require.Equal(t, expected, got)
}

// Bug 4: Webhook events should populate Remaining and PTS fields.
func TestWebhook_PopulatesRemainingAndPTS(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var received []WebhookEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("webhook decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ptsFn := func() int64 { return 8100000 } // 90s at 90kHz
	sink := func(data []byte) {}

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		WebhookURL:        ts.URL,
		WebhookTimeoutMs:  5000,
	}, sink, ptsFn)
	defer inj.Close()

	// Inject a splice_insert cue-out with 60s duration.
	dur := 60 * time.Second
	spliceTimePTS := int64(8100000)
	msg := &CueMessage{
		CommandType:   CommandSpliceInsert,
		IsOut:         true,
		AutoReturn:    false,
		BreakDuration: &dur,
		SpliceTimePTS: &spliceTimePTS,
	}
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Wait for async dispatch.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, received, "expected at least 1 webhook event")

	cueOut := received[0]
	require.Equal(t, "cue_out", cueOut.Type)
	require.Equal(t, int64(60000), cueOut.Duration)
	// Remaining should be populated (equal to duration at injection time).
	require.NotZero(t, cueOut.Remaining, "expected non-zero Remaining in webhook event")
	// PTS should be populated from the splice time.
	require.Equal(t, int64(8100000), cueOut.PTS)
}

// Bug 6: SCTE-104 source should not echo back on ReturnToProgram.
func TestSCTE104Source_NoEchoOnReturn(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	// Inject from SCTE-104 source.
	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	msg.Source = "scte104"
	eventID, _ := inj.InjectCue(msg)

	// Reset tracker for the return call.
	mu.Lock()
	scte104Called = false
	mu.Unlock()

	require.NoError(t, inj.ReturnToProgram(eventID))

	mu.Lock()
	defer mu.Unlock()
	require.False(t, scte104Called, "scte104Sink should NOT be called on ReturnToProgram when source is 'scte104'")
}

// Bug 6: SCTE-104 source should not echo back on CancelEvent.
func TestSCTE104Source_NoEchoOnCancel(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	msg.Source = "scte104"
	eventID, _ := inj.InjectCue(msg)

	mu.Lock()
	scte104Called = false
	mu.Unlock()

	require.NoError(t, inj.CancelEvent(eventID))

	mu.Lock()
	defer mu.Unlock()
	require.False(t, scte104Called, "scte104Sink should NOT be called on CancelEvent when source is 'scte104'")
}

// Bug 6: SCTE-104 source should not echo back on ExtendBreak.
func TestSCTE104Source_NoEchoOnExtend(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	msg.Source = "scte104"
	eventID, _ := inj.InjectCue(msg)

	mu.Lock()
	scte104Called = false
	mu.Unlock()

	require.NoError(t, inj.ExtendBreak(eventID, 120000))

	mu.Lock()
	defer mu.Unlock()
	require.False(t, scte104Called, "scte104Sink should NOT be called on ExtendBreak when source is 'scte104'")
}

// Bug 6: API source SHOULD fire SCTE-104 sink on ReturnToProgram.
func TestAPISource_DoesFireSCTE104SinkOnReturn(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var scte104Called bool

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	inj.SetSCTE104Sink(func(msg *CueMessage) {
		mu.Lock()
		scte104Called = true
		mu.Unlock()
	})

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	msg.Source = "api"
	eventID, _ := inj.InjectCue(msg)

	mu.Lock()
	scte104Called = false
	mu.Unlock()

	require.NoError(t, inj.ReturnToProgram(eventID))

	mu.Lock()
	defer mu.Unlock()
	require.True(t, scte104Called, "scte104Sink SHOULD be called on ReturnToProgram when source is 'api'")
}

// Gap 12: Circular event log wraparound.
func TestCircularLog_Wraparound(t *testing.T) {
	t.Parallel()
	cl := newCircularLog(3)

	// Add 5 entries to a size-3 log.
	for i := 0; i < 5; i++ {
		cl.add(EventLogEntry{
			EventID: uint32(i + 1),
			Status:  "injected",
		})
	}

	entries := cl.list()
	require.Len(t, entries, 3)

	// Should contain entries 3, 4, 5 in order (oldest to newest).
	require.Equal(t, uint32(3), entries[0].EventID)
	require.Equal(t, uint32(4), entries[1].EventID)
	require.Equal(t, uint32(5), entries[2].EventID)
}

// Bug 13: time_signal with cue-out descriptor should produce "cue_out" webhook type.
// Previously, webhookIsOut was always false for time_signal commands because
// msg.IsOut is never set for time_signal (it's only used by splice_insert).
func TestInjector_WebhookDispatch_TimeSignal_CueOutType(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var received []WebhookEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("webhook decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		WebhookURL:        ts.URL,
		WebhookTimeoutMs:  5000,
	}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with a cue-out segmentation descriptor
	// (0x34 = Provider Placement Opportunity Start).
	dur := scte35lib.DurationToTicks(30 * time.Second)
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       100,
				SegmentationType: 0x34, // Provider Placement Opportunity Start (cue-out)
				DurationTicks:    &dur,
			},
		},
	}

	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Wait for async dispatches to complete.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, received, "expected at least 1 webhook event")

	// The webhook type should be "cue_out" for a time_signal with cue-out descriptor.
	evt := received[0]
	require.Equal(t, "cue_out", evt.Type, "webhook type should be cue_out for time_signal with cue-out descriptor")
	require.Equal(t, "time_signal", evt.Command)
	require.True(t, evt.IsOut, "webhook isOut should be true for time_signal with cue-out descriptor")
}

// Bug 13: time_signal with cue-in descriptor should produce "cue_in" webhook type.
func TestInjector_WebhookDispatch_TimeSignal_CueInType(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var received []WebhookEvent

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			t.Errorf("webhook decode error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		WebhookURL:        ts.URL,
		WebhookTimeoutMs:  5000,
	}, sink, ptsFn)
	defer inj.Close()

	// Inject a time_signal with a cue-in descriptor
	// (0x35 = Provider Placement Opportunity End).
	msg := &CueMessage{
		CommandType: CommandTimeSignal,
		Descriptors: []SegmentationDescriptor{
			{
				SegEventID:       200,
				SegmentationType: 0x35, // Provider Placement Opportunity End (cue-in)
			},
		},
	}

	_, err := inj.InjectCue(msg)
	require.NoError(t, err)

	// Wait for async dispatches to complete.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	require.NotEmpty(t, received, "expected at least 1 webhook event")

	// The webhook type should be "cue_in" for a time_signal with cue-in descriptor.
	evt := received[0]
	require.Equal(t, "cue_in", evt.Type, "webhook type should be cue_in for time_signal with cue-in descriptor")
	require.False(t, evt.IsOut, "webhook isOut should be false for time_signal with cue-in descriptor")
}

func TestInjector_OnSpliceOut_CalledForSpliceInsertCueOut(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	var called int
	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		OnSpliceOut:       func() { called++ },
	}, sink, ptsFn)
	defer inj.Close()

	// splice_insert with OutOfNetwork=true should trigger the callback.
	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, 1, called, "OnSpliceOut should be called once for splice_insert cue-out")
}

func TestInjector_OnSpliceOut_NotCalledForCueIn(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	var called int
	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		OnSpliceOut:       func() { called++ },
	}, sink, ptsFn)
	defer inj.Close()

	// splice_insert with OutOfNetwork=false (cue-in) should NOT trigger.
	msg := NewSpliceInsert(1, 0, false, false)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, 0, called, "OnSpliceOut should not be called for cue-in")
}

func TestInjector_OnSpliceOut_NilSafe(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	// No OnSpliceOut configured — should not panic.
	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
	}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)
}

func TestInjector_OnSpliceOut_CalledForTimeSignalCueOut(t *testing.T) {
	t.Parallel()
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 8100000 }

	var called int
	inj := NewInjector(InjectorConfig{
		HeartbeatInterval: 0,
		OnSpliceOut:       func() { called++ },
	}, sink, ptsFn)
	defer inj.Close()

	// time_signal with cue-out segmentation descriptor should trigger.
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("test-upid"))
	_, err := inj.InjectCue(msg)
	require.NoError(t, err)
	require.Equal(t, 1, called, "OnSpliceOut should be called for time_signal with cue-out descriptor")
}
