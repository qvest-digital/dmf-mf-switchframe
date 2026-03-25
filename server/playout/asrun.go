package playout

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const defaultRingCapacity = 10000

// AsRunLoggerConfig configures the as-run event logger.
type AsRunLoggerConfig struct {
	Dir          string
	RingCapacity int
}

// AsRunQuery filters as-run events for retrieval.
type AsRunQuery struct {
	Type      AsRunEventType
	ChannelID *int
	Since     *time.Time
	Until     *time.Time
	Limit     int
}

// AsRunLogger writes as-run events to an in-memory ring buffer and
// append-only JSONL files for durable storage.
type AsRunLogger struct {
	mu   sync.Mutex
	ring []AsRunEvent
	head int // next write position
	count int // number of valid entries (up to cap)
	file *os.File
	enc  *json.Encoder
}

// NewAsRunLogger creates a new as-run logger. It creates the output
// directory if needed and opens a date-stamped JSONL file for appending.
func NewAsRunLogger(cfg AsRunLoggerConfig) (*AsRunLogger, error) {
	if cfg.RingCapacity <= 0 {
		cfg.RingCapacity = defaultRingCapacity
	}

	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("asrun: create dir: %w", err)
	}

	filename := time.Now().UTC().Format("asrun-2006-01-02") + ".jsonl"
	path := filepath.Join(cfg.Dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("asrun: open file: %w", err)
	}

	l := &AsRunLogger{
		ring: make([]AsRunEvent, cfg.RingCapacity),
		file: f,
		enc:  json.NewEncoder(f),
	}
	return l, nil
}

// Log writes an event to both the ring buffer and the JSONL file.
func (l *AsRunLogger) Log(ev AsRunEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Write to ring buffer.
	l.ring[l.head] = ev
	l.head = (l.head + 1) % len(l.ring)
	if l.count < len(l.ring) {
		l.count++
	}

	// Write to JSONL file.
	if err := l.enc.Encode(ev); err != nil {
		slog.Error("asrun: write JSONL", "error", err)
	}
}

// Query returns events from the ring buffer matching the given filters,
// in chronological (oldest-first) order.
func (l *AsRunLogger) Query(q AsRunQuery) []AsRunEvent {
	l.mu.Lock()
	defer l.mu.Unlock()

	var result []AsRunEvent

	// Determine the start position for reading in chronological order.
	start := 0
	if l.count == len(l.ring) {
		// Ring is full; oldest event is at head.
		start = l.head
	}

	for i := 0; i < l.count; i++ {
		idx := (start + i) % len(l.ring)
		ev := l.ring[idx]

		// Apply type filter.
		if q.Type != "" && ev.Type != q.Type {
			continue
		}

		// Apply channel filter.
		if q.ChannelID != nil && ev.ChannelID != *q.ChannelID {
			continue
		}

		// Apply time range filters.
		if q.Since != nil && ev.Timestamp.Before(*q.Since) {
			continue
		}
		if q.Until != nil && !ev.Timestamp.Before(*q.Until) {
			continue
		}

		result = append(result, ev)

		// Apply limit.
		if q.Limit > 0 && len(result) >= q.Limit {
			break
		}
	}

	return result
}

// All returns all events in the ring buffer in chronological order.
func (l *AsRunLogger) All() []AsRunEvent {
	return l.Query(AsRunQuery{})
}

// Close flushes and closes the JSONL file.
func (l *AsRunLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}
