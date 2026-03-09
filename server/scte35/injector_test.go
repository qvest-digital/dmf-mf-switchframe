package scte35

import (
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
	callCount := 0
	sink := func(data []byte) { callCount++ }
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
}
