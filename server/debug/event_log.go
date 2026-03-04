package debug

import (
	"sync"
	"time"
)

// Event is a single notable event in the pipeline.
type Event struct {
	Timestamp time.Time      `json:"ts"`
	Type      string         `json:"type"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// EventLog is a fixed-size ring buffer of events. Safe for concurrent use.
type EventLog struct {
	mu     sync.Mutex
	events []Event
	cap    int
	idx    int
	count  int
}

// NewEventLog creates an EventLog with the given capacity.
func NewEventLog(capacity int) *EventLog {
	return &EventLog{
		events: make([]Event, capacity),
		cap:    capacity,
	}
}

// Add records a new event.
func (l *EventLog) Add(eventType string, detail map[string]any) {
	l.mu.Lock()
	l.events[l.idx] = Event{
		Timestamp: time.Now(),
		Type:      eventType,
		Detail:    detail,
	}
	l.idx = (l.idx + 1) % l.cap
	if l.count < l.cap {
		l.count++
	}
	l.mu.Unlock()
}

// Snapshot returns all events in chronological order.
func (l *EventLog) Snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.count == 0 {
		return nil
	}

	result := make([]Event, l.count)
	if l.count < l.cap {
		copy(result, l.events[:l.count])
	} else {
		// Ring has wrapped: oldest is at l.idx, newest at l.idx-1
		n := copy(result, l.events[l.idx:])
		copy(result[n:], l.events[:l.idx])
	}
	return result
}
