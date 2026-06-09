package playout

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultRingCapacity = 10000

// asRunWriteBuffer bounds the number of events queued for durable JSONL
// writing. Sized to absorb realistic bursts (pod-board fires, item
// boundaries) while a slow/stalled disk catches up. On sustained overflow the
// writer drops the oldest queued event rather than blocking the realtime tick
// goroutine.
const asRunWriteBuffer = 4096

// AsRunLoggerConfig configures the as-run event logger.
type AsRunLoggerConfig struct {
	Dir           string
	RingCapacity  int
	RetentionDays int
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
	mu            sync.Mutex
	config        AsRunLoggerConfig
	ring          []AsRunEvent
	head          int // next write position
	count         int // number of valid entries (up to cap)
	stopRetention chan struct{}

	// Durable JSONL writes are decoupled from the hot path: Log pushes events
	// onto writeCh, which a dedicated writer goroutine drains and encodes to
	// disk. This keeps a stalled disk (e.g. NFS as-run dir) off the realtime
	// tick goroutine and avoids serializing concurrent loggers on file I/O.
	// file/enc are owned exclusively by the writer goroutine after construction.
	file      *os.File
	enc       *json.Encoder
	writeCh   chan AsRunEvent
	writerWG  sync.WaitGroup
	closeOnce sync.Once
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
		config:  cfg,
		ring:    make([]AsRunEvent, cfg.RingCapacity),
		file:    f,
		enc:     json.NewEncoder(f),
		writeCh: make(chan AsRunEvent, asRunWriteBuffer),
	}

	l.writerWG.Add(1)
	// Pass the channel explicitly so the writer ranges over the real channel
	// value rather than re-reading l.writeCh (which Close sets to nil).
	go l.runWriter(l.writeCh)

	return l, nil
}

// runWriter drains queued events and encodes them to the JSONL file. It owns
// l.file/l.enc and runs until ch is closed and drained (by Close).
func (l *AsRunLogger) runWriter(ch <-chan AsRunEvent) {
	defer l.writerWG.Done()
	for ev := range ch {
		if err := l.enc.Encode(ev); err != nil {
			slog.Error("asrun: write JSONL", "error", err)
		}
	}
}

// Log records an event in the in-memory ring buffer and queues it for durable
// JSONL writing. The ring update is synchronous; the file write is performed
// asynchronously by a dedicated writer goroutine so a stalled disk never blocks
// the realtime tick goroutine. If the write queue is saturated (sustained disk
// stall), the oldest queued event is dropped to keep Log non-blocking.
func (l *AsRunLogger) Log(ev AsRunEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Write to ring buffer.
	l.ring[l.head] = ev
	l.head = (l.head + 1) % len(l.ring)
	if l.count < len(l.ring) {
		l.count++
	}

	if l.writeCh == nil {
		return // closed
	}

	// Queue for durable write. The send is non-blocking (buffered channel +
	// select/default), so holding l.mu here never stalls on disk I/O — the
	// writer goroutine performs the actual file write without the lock. Holding
	// the lock also makes the send safe against Close closing the channel. On
	// overflow, drop the oldest queued event and retry so the newest event is
	// still written.
	select {
	case l.writeCh <- ev:
	default:
		select {
		case dropped := <-l.writeCh:
			slog.Warn("asrun: write queue full, dropping oldest event", "type", dropped.Type)
		default:
		}
		select {
		case l.writeCh <- ev:
		default:
			// Still full (other goroutines refilled it); drop this event.
			slog.Warn("asrun: write queue full, dropping event", "type", ev.Type)
		}
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

// Reset clears the in-memory ring buffer. The on-disk JSONL file is
// unaffected (it is an append-only historical record).
func (l *AsRunLogger) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.head = 0
	l.count = 0
	for i := range l.ring {
		l.ring[i] = AsRunEvent{}
	}
}

// CleanupOldFiles removes as-run JSONL files older than RetentionDays.
// Returns the number of files deleted. Returns 0 immediately if retention
// is disabled (RetentionDays <= 0).
func (l *AsRunLogger) CleanupOldFiles() int {
	if l.config.RetentionDays <= 0 {
		return 0
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -l.config.RetentionDays)
	entries, err := os.ReadDir(l.config.Dir)
	if err != nil {
		slog.Error("asrun: read dir for cleanup", "error", err)
		return 0
	}

	deleted := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Match pattern: asrun-YYYY-MM-DD.jsonl
		if !strings.HasPrefix(name, "asrun-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		dateStr := strings.TrimPrefix(name, "asrun-")
		dateStr = strings.TrimSuffix(dateStr, ".jsonl")

		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue // not a valid date, skip
		}

		if fileDate.Before(cutoff) {
			path := filepath.Join(l.config.Dir, name)
			if err := os.Remove(path); err != nil {
				slog.Error("asrun: remove old file", "path", path, "error", err)
				continue
			}
			deleted++
		}
	}

	return deleted
}

// StartRetention launches a background goroutine that periodically runs
// CleanupOldFiles. It runs cleanup immediately once, then on the given
// interval. If RetentionDays <= 0 or interval <= 0, defaults to 24 hours.
func (l *AsRunLogger) StartRetention(interval time.Duration) {
	if l.config.RetentionDays <= 0 {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	l.stopRetention = make(chan struct{})
	stop := l.stopRetention

	// Run once immediately.
	l.CleanupOldFiles()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				l.CleanupOldFiles()
			}
		}
	}()
}

// Close flushes queued events, closes the JSONL file, and stops the retention
// goroutine. It waits for the writer goroutine to drain all queued events so
// durability is preserved (events logged before Close are written to disk).
// Safe to call more than once.
func (l *AsRunLogger) Close() error {
	var err error
	l.closeOnce.Do(func() {
		l.mu.Lock()
		if l.stopRetention != nil {
			close(l.stopRetention)
			l.stopRetention = nil
		}
		// Close the write channel under the lock so it cannot race a concurrent
		// Log send; setting writeCh to nil first makes later Log calls no-op.
		ch := l.writeCh
		l.writeCh = nil
		l.mu.Unlock()

		if ch != nil {
			close(ch)
		}
		// Wait outside the lock: the writer drains remaining events and exits.
		l.writerWG.Wait()

		l.mu.Lock()
		if l.file != nil {
			err = l.file.Close()
			l.file = nil
		}
		l.mu.Unlock()
	})
	return err
}
