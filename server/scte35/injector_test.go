package scte35

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestInjector_InjectCue_Immediate(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}
	if eventID == 0 {
		t.Fatal("expected non-zero auto-assigned event ID")
	}

	mu.Lock()
	if len(captured) == 0 {
		t.Fatal("muxer sink not called")
	}
	mu.Unlock()

	// Verify event appears in log
	log := inj.EventLog()
	if len(log) == 0 {
		t.Fatal("event log empty")
	}
	if log[0].EventID != eventID {
		t.Fatalf("log event ID %d != %d", log[0].EventID, eventID)
	}
}

func TestInjector_InjectCue_Scheduled(t *testing.T) {
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
	if err != nil {
		t.Fatalf("schedule failed: %v", err)
	}

	mu.Lock()
	if len(captured) == 0 {
		t.Fatal("muxer sink not called")
	}
	mu.Unlock()
}

func TestInjector_ReturnToProgram(t *testing.T) {
	callCount := 0
	sink := func(data []byte) { callCount++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false) // no auto-return
	eventID, _ := inj.InjectCue(msg)

	if err := inj.ReturnToProgram(eventID); err != nil {
		t.Fatalf("return failed: %v", err)
	}

	// Active events should be empty
	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events after return")
	}
}

func TestInjector_HoldBreak(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true) // auto-return
	eventID, _ := inj.InjectCue(msg)

	if err := inj.HoldBreak(eventID); err != nil {
		t.Fatalf("hold failed: %v", err)
	}

	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	if !ok {
		t.Fatal("event not in active events")
	}
	if !active.Held {
		t.Fatal("expected Held=true")
	}
}

func TestInjector_AutoReturn(t *testing.T) {
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

	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected auto-return to clear active events")
	}
	mu.Lock()
	c := callCount
	mu.Unlock()
	if c < 2 {
		t.Fatalf("expected at least 2 sink calls (cue-out + cue-in), got %d", c)
	}
}

func TestInjector_ConcurrentEvents(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg1 := NewSpliceInsert(0, 60*time.Second, true, false)
	msg2 := NewSpliceInsert(0, 120*time.Second, true, false)
	id1, _ := inj.InjectCue(msg1)
	id2, _ := inj.InjectCue(msg2)

	ids := inj.ActiveEventIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 active events, got %d", len(ids))
	}

	// Return first, second still active
	_ = inj.ReturnToProgram(id1)
	ids = inj.ActiveEventIDs()
	if len(ids) != 1 || ids[0] != id2 {
		t.Fatalf("expected only event %d active, got %v", id2, ids)
	}
}

func TestInjector_SyntheticBreakState(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// No active events → nil
	if inj.SyntheticBreakState() != nil {
		t.Fatal("expected nil synthetic state with no active events")
	}

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	_, _ = inj.InjectCue(msg)

	synth := inj.SyntheticBreakState()
	if synth == nil {
		t.Fatal("expected non-nil synthetic state during active break")
	}
	if len(synth) == 0 {
		t.Fatal("expected non-empty synthetic bytes")
	}
}

func TestInjector_Heartbeat(t *testing.T) {
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
	if c < 2 {
		t.Fatalf("expected at least 2 heartbeats, got %d", c)
	}
}

func TestInjector_ExtendBreak(t *testing.T) {
	callCount := 0
	sink := func(data []byte) { callCount++ }
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, true)
	eventID, _ := inj.InjectCue(msg)

	if err := inj.ExtendBreak(eventID, 120000); err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	state := inj.State()
	active, ok := state.ActiveEvents[eventID]
	if !ok {
		t.Fatal("event not in active events after extend")
	}
	if active.DurationMs == nil || *active.DurationMs != 120000 {
		t.Fatalf("expected 120000ms duration after extend, got %v", active.DurationMs)
	}
}

func TestInjector_CancelEvent(t *testing.T) {
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

	if err := inj.CancelEvent(eventID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	if len(inj.ActiveEventIDs()) != 0 {
		t.Fatal("expected no active events after cancel")
	}

	// Verify the cancel message (second sink call) has the cancel indicator set.
	if len(sinkCalls) < 2 {
		t.Fatalf("expected at least 2 sink calls, got %d", len(sinkCalls))
	}
	cancelData := sinkCalls[1]
	decoded, err := Decode(cancelData)
	if err != nil {
		t.Fatalf("failed to decode cancel message: %v", err)
	}
	if decoded.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", decoded.CommandType)
	}
	if decoded.EventID != eventID {
		t.Fatalf("expected event ID %d, got %d", eventID, decoded.EventID)
	}
	if !decoded.SpliceEventCancelIndicator {
		t.Fatal("expected SpliceEventCancelIndicator=true in cancel message")
	}
}

func TestInjector_InjectCue_TimeSignal_PopulatesPTS(t *testing.T) {
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
	msg := NewTimeSignal(0x34, 60*time.Second, 0x0F, []byte("https://example.com"))
	if msg.SpliceTimePTS != nil {
		t.Fatal("expected SpliceTimePTS to be nil before injection")
	}

	_, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	// Decode the captured sink data and verify PTS was populated.
	mu.Lock()
	data := captured
	mu.Unlock()

	if len(data) == 0 {
		t.Fatal("muxer sink not called")
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("decode captured data failed: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got %d", decoded.CommandType)
	}
	if decoded.SpliceTimePTS == nil {
		t.Fatal("expected SpliceTimePTS to be populated by injector")
	}
	if *decoded.SpliceTimePTS != 8100000 {
		t.Fatalf("expected SpliceTimePTS=8100000, got %d", *decoded.SpliceTimePTS)
	}
}

func TestInjector_CancelSegmentationEvent(t *testing.T) {
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
	if err := inj.CancelSegmentationEvent(42); err != nil {
		t.Fatalf("cancel segmentation event failed: %v", err)
	}

	// Verify sink was called with the cancel message (second call after the inject).
	if len(sinkCalls) < 2 {
		t.Fatalf("expected at least 2 sink calls, got %d", len(sinkCalls))
	}
	cancelData := sinkCalls[len(sinkCalls)-1]
	decoded, err := Decode(cancelData)
	if err != nil {
		t.Fatalf("failed to decode cancel segmentation message: %v", err)
	}
	if decoded.CommandType != CommandTimeSignal {
		t.Fatalf("expected time_signal, got %d", decoded.CommandType)
	}
	if len(decoded.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(decoded.Descriptors))
	}
	d := decoded.Descriptors[0]
	if !d.SegmentationEventCancelIndicator {
		t.Fatal("expected SegmentationEventCancelIndicator=true")
	}
	if d.SegEventID != 42 {
		t.Fatalf("expected SegEventID=42, got %d", d.SegEventID)
	}

	// Verify event log has a "cancelled" entry.
	log := inj.EventLog()
	found := false
	for _, entry := range log {
		if entry.EventID == 42 && entry.Status == "cancelled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected event log entry with eventID=42 and status=cancelled")
	}
}

func TestInjector_WebhookDispatch(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	// 2. Hold the break
	if err := inj.HoldBreak(eventID); err != nil {
		t.Fatalf("hold failed: %v", err)
	}

	// 3. Extend the break
	if err := inj.ExtendBreak(eventID, 120000); err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	// 4. Return to program
	if err := inj.ReturnToProgram(eventID); err != nil {
		t.Fatalf("return failed: %v", err)
	}

	// Wait for async dispatches to complete.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 4 {
		t.Fatalf("expected 4 webhook events, got %d", len(received))
	}

	// Webhook dispatches are async goroutines, so verify all expected types
	// are present and each has the correct event ID. The first event (cue_out)
	// is always first since subsequent actions depend on it.
	typeCounts := make(map[string]int)
	eventsByType := make(map[string]WebhookEvent)
	for _, evt := range received {
		typeCounts[evt.Type]++
		eventsByType[evt.Type] = evt
		if evt.EventID != eventID {
			t.Errorf("event type %q has eventId = %d, want %d", evt.Type, evt.EventID, eventID)
		}
	}

	for _, wantType := range []string{"cue_out", "hold", "extend", "cue_in"} {
		if typeCounts[wantType] != 1 {
			t.Errorf("expected exactly 1 %q event, got %d", wantType, typeCounts[wantType])
		}
	}

	// Verify cue_out has correct fields.
	cueOut := eventsByType["cue_out"]
	if !cueOut.IsOut {
		t.Error("cue_out event should have isOut=true")
	}
	if cueOut.Duration != 60000 {
		t.Errorf("cue_out duration = %d, want 60000", cueOut.Duration)
	}
	if cueOut.Command != "splice_insert" {
		t.Errorf("cue_out command = %q, want splice_insert", cueOut.Command)
	}

	// Verify extend has new duration.
	extendEvt := eventsByType["extend"]
	if extendEvt.Duration != 120000 {
		t.Errorf("extend duration = %d, want 120000", extendEvt.Duration)
	}

	// Verify cue_in is not out.
	cueIn := eventsByType["cue_in"]
	if cueIn.IsOut {
		t.Error("cue_in event should have isOut=false")
	}
}

func TestInjector_WebhookDispatch_Cancel(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	if err := inj.CancelEvent(eventID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	// Wait for async dispatches.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("expected 2 webhook events, got %d", len(received))
	}

	// Webhook dispatches are async goroutines, so verify all expected types
	// are present rather than relying on delivery order.
	typeCounts := make(map[string]int)
	for _, evt := range received {
		typeCounts[evt.Type]++
		if evt.EventID != eventID {
			t.Errorf("event type %q has eventId = %d, want %d", evt.Type, evt.EventID, eventID)
		}
	}
	if typeCounts["cue_out"] != 1 {
		t.Errorf("expected 1 cue_out event, got %d", typeCounts["cue_out"])
	}
	if typeCounts["cancel"] != 1 {
		t.Errorf("expected 1 cancel event, got %d", typeCounts["cancel"])
	}
}

func TestInjector_WebhookNilSafe(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}
	// Just verifying no panic occurs with nil webhook.
}

func TestInjector_Rules_Pass(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}
	if eventID == 0 {
		t.Fatal("expected non-zero event ID")
	}
	if sinkCalls == 0 {
		t.Fatal("expected sink to be called (rule should not match)")
	}
	ids := inj.ActiveEventIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 active event, got %d", len(ids))
	}
}

func TestInjector_Rules_Delete(t *testing.T) {
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
	if err != nil {
		t.Fatalf("expected no error for deleted cue, got: %v", err)
	}
	// Deleted cue returns 0 event ID.
	if eventID != 0 {
		t.Fatalf("expected 0 event ID for deleted cue, got %d", eventID)
	}
	// Sink should NOT have been called.
	if sinkCalls != 0 {
		t.Fatalf("expected 0 sink calls for deleted cue, got %d", sinkCalls)
	}
	// No active events.
	ids := inj.ActiveEventIDs()
	if len(ids) != 0 {
		t.Fatalf("expected 0 active events, got %d", len(ids))
	}
}

func TestInjector_LogEventPopulatesFields(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 8100000 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject a splice_insert with descriptors, avail fields, and SpliceTimePTS.
	dur := 30 * time.Second
	spliceTimePTS := int64(8100000)
	msg := &CueMessage{
		CommandType:   CommandSpliceInsert,
		IsOut:         true,
		AutoReturn:    true,
		BreakDuration: &dur,
		SpliceTimePTS: &spliceTimePTS,
		AvailNum:      3,
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	log := inj.EventLog()
	if len(log) == 0 {
		t.Fatal("event log empty")
	}

	entry := log[0]
	if entry.EventID != eventID {
		t.Fatalf("log event ID %d != %d", entry.EventID, eventID)
	}

	// Verify Source is populated.
	if entry.Source != "injector" {
		t.Fatalf("expected Source='injector', got %q", entry.Source)
	}

	// Verify AvailNum is populated.
	if entry.AvailNum != 3 {
		t.Fatalf("expected AvailNum=3, got %d", entry.AvailNum)
	}

	// Verify AvailsExpected is populated.
	if entry.AvailsExpected != 5 {
		t.Fatalf("expected AvailsExpected=5, got %d", entry.AvailsExpected)
	}

	// Verify SpliceTimePTS is populated.
	if entry.SpliceTimePTS == nil {
		t.Fatal("expected SpliceTimePTS to be set")
	}
	if *entry.SpliceTimePTS != 8100000 {
		t.Fatalf("expected SpliceTimePTS=8100000, got %d", *entry.SpliceTimePTS)
	}

	// Verify Descriptors are populated.
	if len(entry.Descriptors) != 1 {
		t.Fatalf("expected 1 descriptor, got %d", len(entry.Descriptors))
	}
	d := entry.Descriptors[0]
	if d.SegmentationType != 0x34 {
		t.Fatalf("expected SegmentationType=0x34, got 0x%02x", d.SegmentationType)
	}
	if d.SegEventID != 1001 {
		t.Fatalf("expected SegEventID=1001, got %d", d.SegEventID)
	}
}

func TestInjector_LogEventPopulatesFields_Return(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	if err := inj.ReturnToProgram(eventID); err != nil {
		t.Fatalf("return failed: %v", err)
	}

	log := inj.EventLog()
	// Find the "returned" entry.
	var returnEntry *EventLogEntry
	for i := range log {
		if log[i].Status == "returned" {
			returnEntry = &log[i]
			break
		}
	}
	if returnEntry == nil {
		t.Fatal("expected returned entry in event log")
	}
	if returnEntry.Source != "injector" {
		t.Fatalf("expected Source='injector' for return, got %q", returnEntry.Source)
	}
}

func TestInjector_LogEventPopulatesFields_Cancel(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	msg := NewSpliceInsert(0, 60*time.Second, true, false)
	eventID, _ := inj.InjectCue(msg)

	if err := inj.CancelEvent(eventID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	log := inj.EventLog()
	var cancelEntry *EventLogEntry
	for i := range log {
		if log[i].Status == "cancelled" {
			cancelEntry = &log[i]
			break
		}
	}
	if cancelEntry == nil {
		t.Fatal("expected cancelled entry in event log")
	}
	if cancelEntry.Source != "injector" {
		t.Fatalf("expected Source='injector' for cancel, got %q", cancelEntry.Source)
	}
}

func TestInjector_Rules_Replace(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	// Verify sink was called.
	if len(captured) == 0 {
		t.Fatal("sink was not called")
	}

	// Decode the captured data and verify the duration was replaced.
	decoded, err := Decode(captured)
	if err != nil {
		t.Fatalf("decode captured data failed: %v", err)
	}
	if decoded.BreakDuration == nil {
		t.Fatal("expected BreakDuration to be set in decoded message")
	}
	// 120s = 120000ms
	if decoded.BreakDuration.Milliseconds() != 120000 {
		t.Fatalf("expected break duration 120000ms, got %dms", decoded.BreakDuration.Milliseconds())
	}
}

func TestInjector_SCTE104Sink_InjectCue(t *testing.T) {
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
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !muxerCalled {
		t.Fatal("expected muxerSink to be called")
	}
	if scte104Msg == nil {
		t.Fatal("expected scte104Sink to be called")
	}
	if scte104Msg.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", scte104Msg.CommandType)
	}
	if !scte104Msg.IsOut {
		t.Fatal("expected IsOut=true in scte104 message")
	}
	if scte104Msg.Source != "api" {
		t.Fatalf("expected Source='api', got %q", scte104Msg.Source)
	}
}

func TestInjector_SCTE104Sink_ReturnToProgram(t *testing.T) {
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

	if err := inj.ReturnToProgram(eventID); err != nil {
		t.Fatalf("return failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if scte104Msg == nil {
		t.Fatal("expected scte104Sink to be called on ReturnToProgram")
	}
	if scte104Msg.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", scte104Msg.CommandType)
	}
	if scte104Msg.IsOut {
		t.Fatal("expected IsOut=false for cue-in")
	}
}

func TestInjector_SCTE104Sink_CancelEvent(t *testing.T) {
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

	if err := inj.CancelEvent(eventID); err != nil {
		t.Fatalf("cancel failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if scte104Msg == nil {
		t.Fatal("expected scte104Sink to be called on CancelEvent")
	}
	if scte104Msg.CommandType != CommandSpliceInsert {
		t.Fatalf("expected splice_insert, got %d", scte104Msg.CommandType)
	}
	if !scte104Msg.SpliceEventCancelIndicator {
		t.Fatal("expected SpliceEventCancelIndicator=true")
	}
	if scte104Msg.EventID != eventID {
		t.Fatalf("expected event ID %d, got %d", eventID, scte104Msg.EventID)
	}
}

func TestInjector_MsgSource_InEventLog(t *testing.T) {
	sink := func(data []byte) {}
	ptsFn := func() int64 { return 0 }

	inj := NewInjector(InjectorConfig{HeartbeatInterval: 0}, sink, ptsFn)
	defer inj.Close()

	// Inject with Source="macro"
	msg := NewSpliceInsert(0, 30*time.Second, true, false)
	msg.Source = "macro"
	eventID, err := inj.InjectCue(msg)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	log := inj.EventLog()
	if len(log) == 0 {
		t.Fatal("event log empty")
	}

	var found bool
	for _, entry := range log {
		if entry.EventID == eventID && entry.Source == "macro" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected event log entry with Source='macro'")
	}

	// Inject without Source set — should default to "injector"
	msg2 := NewSpliceInsert(0, 30*time.Second, true, false)
	eventID2, err := inj.InjectCue(msg2)
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}

	log = inj.EventLog()
	for _, entry := range log {
		if entry.EventID == eventID2 && entry.Source == "injector" {
			return // success
		}
	}
	t.Fatal("expected event log entry with default Source='injector'")
}
