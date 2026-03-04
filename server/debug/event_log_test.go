package debug

import (
	"testing"
)

func TestEventLog_AddAndSnapshot(t *testing.T) {
	log := NewEventLog(3) // capacity 3

	log.Add("test_event", map[string]any{"key": "val1"})
	log.Add("test_event", map[string]any{"key": "val2"})

	events := log.Snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "test_event" {
		t.Errorf("expected type test_event, got %s", events[0].Type)
	}
	if events[0].Detail["key"] != "val1" {
		t.Errorf("expected val1, got %v", events[0].Detail["key"])
	}
	if events[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestEventLog_Wraparound(t *testing.T) {
	log := NewEventLog(3)

	log.Add("a", nil)
	log.Add("b", nil)
	log.Add("c", nil)
	log.Add("d", nil) // overwrites "a"

	events := log.Snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Should be in chronological order: b, c, d
	if events[0].Type != "b" {
		t.Errorf("expected b, got %s", events[0].Type)
	}
	if events[2].Type != "d" {
		t.Errorf("expected d, got %s", events[2].Type)
	}
}

func TestEventLog_ConcurrentAccess(t *testing.T) {
	log := NewEventLog(100)
	done := make(chan struct{})

	// Writer
	go func() {
		for i := 0; i < 1000; i++ {
			log.Add("concurrent", nil)
		}
		close(done)
	}()

	// Reader
	for i := 0; i < 100; i++ {
		_ = log.Snapshot()
	}
	<-done
}
